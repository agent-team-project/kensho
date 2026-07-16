#!/usr/bin/env bash
# Shared immutable-build resolver for bundled skills that call the daemon
# directly. It returns a header only from a launch-attested shim or native CLI
# whose immutable source identity matches the active daemon.

_agent_team_status_build_header() {
    local token=""
    if [[ -n "${AGENT_TEAM_DAEMON_TOKEN_FILE:-}" && -f "$AGENT_TEAM_DAEMON_TOKEN_FILE" ]]; then
        IFS= read -r token < "$AGENT_TEAM_DAEMON_TOKEN_FILE" || true
    fi

    local status_json=""
    local http_status=0
    if [[ -n "${AGENT_TEAM_DAEMON_URL:-}" ]]; then
        local args=(-sS --fail-with-body)
        if [[ -n "$token" ]]; then
            args+=(-H "Authorization: Bearer $token")
        fi
        status_json=$(curl "${args[@]}" "${AGENT_TEAM_DAEMON_URL%/}/v1/status") || http_status=$?
        if (( http_status != 0 )); then
            # Mirror the command transport's safe pre-delivery fallback. The
            # status lookup is read-only, but an authoritative HTTP response
            # must not silently switch trust domains through the Unix socket.
            case "$http_status" in
                5|6|7) ;;
                *) return "$http_status" ;;
            esac
        fi
    fi
    if [[ -z "$status_json" ]]; then
        local socket="${AGENT_TEAM_DAEMON_SOCKET:-${AGENT_TEAM_ROOT:-}/daemon.sock}"
        [[ -n "$socket" && -S "$socket" ]] || return 1
        status_json=$(curl --unix-socket "$socket" -sS --fail-with-body http://daemon/v1/status) || return 1
    fi

    AGENT_TEAM_DAEMON_STATUS_JSON="$status_json" python3 - <<'PY'
import json
import os
import urllib.parse

status = json.loads(os.environ["AGENT_TEAM_DAEMON_STATUS_JSON"])
build = status.get("build") or status.get("daemon_build") or {}
values = []
for key in ("module_path", "module_version", "modified", "revision", "source_id", "time", "version"):
    value = build.get(key)
    if value is True:
        value = "true"
    if value in (None, "", False):
        continue
    values.append((key, str(value)))
print(urllib.parse.urlencode(values))
PY
}

_agent_team_candidate_header() {
    local candidate="$1"
    local evidence=""
    local header=""
    if evidence=$("$candidate" --build-attestation --json 2>/dev/null); then
        local parse_status=0
        if header=$(AGENT_TEAM_SHIM_ATTESTATION="$evidence" python3 - <<'PY'
import json
import os

try:
    attestation = json.loads(os.environ["AGENT_TEAM_SHIM_ATTESTATION"])
except (KeyError, json.JSONDecodeError):
    raise SystemExit(1)
if attestation.get("schema") != "agent-team.shim-attestation.v1":
    raise SystemExit(1)
if attestation.get("daemon_comparison") != "coherent":
    raise SystemExit(3)
cli_header = attestation.get("cli_header", "")
daemon_header = attestation.get("daemon_header", "")
if not cli_header or cli_header != daemon_header:
    raise SystemExit(3)
print(cli_header)
PY
        ); then
            printf '%s\n' "$header"
            return 0
        else
            parse_status=$?
            if (( parse_status == 3 )); then
                return 1
            fi
        fi
    fi
    # A legacy generated shim may delegate unknown bootstrap commands to its
    # target. Never mistake that delegated answer for native provenance.
    if head -n 16 "$candidate" 2>/dev/null | grep -Eq '^# agent-team-shim-attestation-v1 |REAL_AGENT_TEAM=|Closed-world enforcement baked in'; then
        return 1
    fi
    if header=$("$candidate" --build-attestation --header 2>/dev/null) && [[ -n "$header" ]]; then
        printf '%s\n' "$header"
        return 0
    fi
    if header=$("$candidate" __build-attestation --header 2>/dev/null) && [[ -n "$header" ]]; then
        printf '%s\n' "$header"
        return 0
    fi
    return 1
}

agent_team_build_header() {
    local caller="${1:-bundled skill}"
    local expected="${AGENT_TEAM_DAEMON_BUILD_HEADER:-}"
    if [[ -z "$expected" ]]; then
        expected=$(_agent_team_status_build_header) || true
    fi
    if [[ -z "$expected" ]]; then
        echo "$caller: activation needed: active daemon build provenance is unavailable; start the instance fresh." >&2
        return 1
    fi

    local seen=":"
    local dir=""
    local candidate=""
    local header=""
    local candidates=()
    local path_dirs=()
    if [[ -n "${AGENT_TEAM_SHIM_PATH:-}" ]]; then
        candidates+=("$AGENT_TEAM_SHIM_PATH")
    fi
    IFS=':' read -r -a path_dirs <<< "${PATH:-}"
    for dir in "${path_dirs[@]}"; do
        [[ -n "$dir" ]] || dir="."
        candidates+=("$dir/agent-team")
    done
    for candidate in "${candidates[@]}"; do
        [[ -x "$candidate" ]] || continue
        case "$seen" in
            *":$candidate:"*) continue ;;
        esac
        seen="${seen}${candidate}:"
        header=$(_agent_team_candidate_header "$candidate") || continue
        if [[ "$header" == "$expected" ]]; then
            printf '%s\n' "$header"
            return 0
        fi
    done

    echo "$caller: activation needed: no generated shim or managed CLI on PATH matches the active daemon; start the instance fresh." >&2
    return 1
}
