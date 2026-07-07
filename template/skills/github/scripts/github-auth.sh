#!/usr/bin/env bash
#
# Run GitHub-facing commands with an explicit agent identity instead of the
# ambient `gh auth` active account.

set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage:
  github-auth.sh gh <gh-args...>
  github-auth.sh git <git-args...>
  github-auth.sh token
  github-auth.sh env

Resolves a GitHub token from AGENT_TEAM_GITHUB_TOKEN, configured
github.agent_login via `gh auth token --user`, GITHUB_TOKEN/GH_TOKEN, or .env.
When github.agent_login or AGENT_TEAM_GITHUB_LOGIN is set, the token actor is
verified before any command runs.
EOF
    exit "${1:-1}"
}

MODE="${1:-}"
if [ -z "$MODE" ]; then
    usage 1
fi
shift || true

repo_root() {
    if command -v git >/dev/null 2>&1; then
        git rev-parse --show-toplevel 2>/dev/null && return 0
    fi
    printf '%s\n' "$PWD"
}

main_repo_root() {
    if command -v git >/dev/null 2>&1; then
        git worktree list --porcelain 2>/dev/null | awk '/^worktree/ {print $2; exit}' && return 0
    fi
    return 1
}

read_config_value() {
    local path=$1
    local dotted=$2
    python3 - "$path" "$dotted" <<'PY'
import sys
import tomllib
from pathlib import Path

path = Path(sys.argv[1])
dotted = sys.argv[2]
try:
    value = tomllib.loads(path.read_text())
except OSError:
    sys.exit(1)
except tomllib.TOMLDecodeError as exc:
    sys.exit(f"github-auth.sh: invalid TOML in {path}: {exc}")

for part in dotted.split("."):
    if not isinstance(value, dict) or part not in value:
        sys.exit(1)
    value = value[part]

if value is None:
    sys.exit(1)
if isinstance(value, str):
    value = value.strip()
    if not value:
        sys.exit(1)
    print(value)
else:
    print(value)
PY
}

read_env_value() {
    python3 - "$@" <<'PY'
import sys
from pathlib import Path

path = Path(sys.argv[1])
names = sys.argv[2:]
wanted = set(names)
values = {}

try:
    lines = path.read_text().splitlines()
except OSError:
    sys.exit(1)

for raw in lines:
    line = raw.strip()
    if not line or line.startswith("#"):
        continue
    if line.startswith("export "):
        line = line[len("export "):].lstrip()
    key, sep, value = line.partition("=")
    if not sep:
        continue
    key = key.strip()
    if key not in wanted:
        continue
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
        value = value[1:-1]
    values[key] = value

for name in names:
    value = values.get(name, "").strip()
    if value:
        print(value)
        sys.exit(0)
sys.exit(1)
PY
}

ROOT="$(repo_root)"
TEAM_ROOT="${AGENT_TEAM_ROOT:-$ROOT/.agent_team}"
CONFIG_PATH="$TEAM_ROOT/config.toml"
EXPECTED_LOGIN="${AGENT_TEAM_GITHUB_LOGIN:-}"
if [ -z "$EXPECTED_LOGIN" ] && [ -f "$CONFIG_PATH" ]; then
    EXPECTED_LOGIN="$(read_config_value "$CONFIG_PATH" github.agent_login 2>/dev/null || true)"
fi

github_host="${AGENT_TEAM_GITHUB_HOST:-github.com}"
github_rest_url="${AGENT_TEAM_GITHUB_REST_URL:-https://api.github.com}"
github_rest_url="${github_rest_url%/}"
TOKEN=""
TOKEN_SOURCE=""
ACTOR_LOGIN=""

set_token() {
    local value=$1
    local source=$2
    if [ -n "$value" ]; then
        TOKEN="$value"
        TOKEN_SOURCE="$source"
        return 0
    fi
    return 1
}

token_from_env_file() {
    local env_file=$1
    local token
    if token="$(read_env_value "$env_file" AGENT_TEAM_GITHUB_TOKEN GITHUB_TOKEN GH_TOKEN)"; then
        set_token "$token" "$env_file"
        return 0
    fi
    return 1
}

resolve_token() {
    if set_token "${AGENT_TEAM_GITHUB_TOKEN:-}" "AGENT_TEAM_GITHUB_TOKEN"; then
        return 0
    fi

    if [ -n "$EXPECTED_LOGIN" ] && command -v gh >/dev/null 2>&1; then
        local gh_token
        if gh_token="$(
            env -u AGENT_TEAM_GITHUB_TOKEN -u GITHUB_TOKEN -u GH_TOKEN \
                gh auth token --hostname "$github_host" --user "$EXPECTED_LOGIN" 2>/dev/null
        )" && [ -n "$gh_token" ]; then
            set_token "$gh_token" "gh:$EXPECTED_LOGIN"
            return 0
        fi
    fi

    if set_token "${GITHUB_TOKEN:-}" "GITHUB_TOKEN"; then
        return 0
    fi
    if set_token "${GH_TOKEN:-}" "GH_TOKEN"; then
        return 0
    fi

    local env_files=()
    [ -f "$PWD/.env" ] && env_files+=("$PWD/.env")
    [ -f "$ROOT/.env" ] && env_files+=("$ROOT/.env")

    local main_root
    main_root="$(main_repo_root 2>/dev/null || true)"
    if [ -n "$main_root" ] && [ "$main_root" != "$ROOT" ] && [ -f "$main_root/.env" ]; then
        env_files+=("$main_root/.env")
    fi

    local env_file
    for env_file in "${env_files[@]:-}"; do
        [ -z "$env_file" ] && continue
        if token_from_env_file "$env_file"; then
            return 0
        fi
    done

    if [ -n "$EXPECTED_LOGIN" ]; then
        echo "github-auth.sh: no GitHub token found for configured github.agent_login=$EXPECTED_LOGIN" >&2
        echo "github-auth.sh: authenticate that account with gh, or set AGENT_TEAM_GITHUB_TOKEN/GITHUB_TOKEN/GH_TOKEN" >&2
    else
        echo "github-auth.sh: no deterministic GitHub identity configured" >&2
        echo "github-auth.sh: set [github].agent_login, AGENT_TEAM_GITHUB_LOGIN, or AGENT_TEAM_GITHUB_TOKEN/GITHUB_TOKEN/GH_TOKEN" >&2
    fi
    return 1
}

verify_actor() {
    if [ -z "$EXPECTED_LOGIN" ]; then
        return 0
    fi
    if ! command -v curl >/dev/null 2>&1; then
        echo "github-auth.sh: curl is required to verify github.agent_login=$EXPECTED_LOGIN" >&2
        return 1
    fi
    if ! command -v jq >/dev/null 2>&1; then
        echo "github-auth.sh: jq is required to verify github.agent_login=$EXPECTED_LOGIN" >&2
        return 1
    fi

    local body
    if ! body="$(
        curl -sS --fail-with-body "$github_rest_url/user" \
            -H "Authorization: Bearer $TOKEN" \
            -H "Accept: application/vnd.github+json" \
            -H "X-GitHub-Api-Version: 2022-11-28"
    )"; then
        echo "github-auth.sh: failed to verify GitHub token actor from $TOKEN_SOURCE" >&2
        return 1
    fi

    ACTOR_LOGIN="$(printf '%s' "$body" | jq -r '.login // empty')"
    if [ -z "$ACTOR_LOGIN" ]; then
        echo "github-auth.sh: GitHub token actor response did not include a login" >&2
        return 1
    fi
    if [ "$ACTOR_LOGIN" != "$EXPECTED_LOGIN" ]; then
        echo "github-auth.sh: GitHub token actor is $ACTOR_LOGIN, expected $EXPECTED_LOGIN" >&2
        return 1
    fi
}

resolve_token
verify_actor

IDENTITY="${ACTOR_LOGIN:-$EXPECTED_LOGIN}"
if [ -z "$IDENTITY" ]; then
    IDENTITY="token:$TOKEN_SOURCE"
fi

export GITHUB_TOKEN="$TOKEN"
export GH_TOKEN="$TOKEN"
export AGENT_TEAM_GITHUB_LOGIN="$IDENTITY"

case "$MODE" in
    token)
        printf '%s\n' "$TOKEN"
        ;;
    env)
        printf 'export GITHUB_TOKEN=%q\n' "$TOKEN"
        printf 'export GH_TOKEN=%q\n' "$TOKEN"
        printf 'export AGENT_TEAM_GITHUB_LOGIN=%q\n' "$IDENTITY"
        ;;
    gh)
        if [ $# -eq 0 ]; then
            echo "github-auth.sh: missing gh arguments" >&2
            usage 1
        fi
        tmp_config="$(mktemp -d "${TMPDIR:-/tmp}/agent-team-gh-config.XXXXXX")"
        trap 'rm -rf "$tmp_config"' EXIT
        echo "github-auth.sh: using GitHub identity $IDENTITY for gh" >&2
        GH_CONFIG_DIR="$tmp_config" gh "$@"
        ;;
    git)
        if [ $# -eq 0 ]; then
            echo "github-auth.sh: missing git arguments" >&2
            usage 1
        fi
        askpass="$(mktemp "${TMPDIR:-/tmp}/agent-team-git-askpass.XXXXXX")"
        trap 'rm -f "$askpass"' EXIT
        cat >"$askpass" <<'EOF'
#!/bin/sh
case "$1" in
    *sername*) printf '%s\n' "x-access-token" ;;
    *assword*) printf '%s\n' "$GITHUB_TOKEN" ;;
    *) printf '\n' ;;
esac
EOF
        chmod 700 "$askpass"
        echo "github-auth.sh: using GitHub identity $IDENTITY for git" >&2
        GIT_TERMINAL_PROMPT=0 GIT_ASKPASS="$askpass" \
            git -c credential.helper= "$@"
        ;;
    -h|--help|help)
        usage 0
        ;;
    *)
        echo "github-auth.sh: unknown mode: $MODE" >&2
        usage 1
        ;;
esac
