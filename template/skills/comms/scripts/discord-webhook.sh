#!/usr/bin/env bash
#
# Post comms content to Discord through a webhook read directly from .env.

set -euo pipefail

usage() {
    cat <<'EOF'
Usage:
  discord-webhook.sh --content TEXT [--env-file PATH]
  discord-webhook.sh --content-file PATH [--env-file PATH]
EOF
    exit "${1:-1}"
}

CONTENT=""
CONTENT_FILE=""
ENV_FILE=""

while [ $# -gt 0 ]; do
    case "$1" in
        --content)
            if [ $# -lt 2 ]; then
                echo "discord-webhook.sh: --content requires a value" >&2
                usage 1
            fi
            CONTENT="$2"
            shift 2
            ;;
        --content-file)
            if [ $# -lt 2 ]; then
                echo "discord-webhook.sh: --content-file requires a path" >&2
                usage 1
            fi
            CONTENT_FILE="$2"
            shift 2
            ;;
        --env-file)
            if [ $# -lt 2 ]; then
                echo "discord-webhook.sh: --env-file requires a path" >&2
                usage 1
            fi
            ENV_FILE="$2"
            shift 2
            ;;
        -h|--help)
            usage 0
            ;;
        *)
            echo "discord-webhook.sh: unknown argument: $1" >&2
            usage 1
            ;;
    esac
done

if [ -n "$CONTENT" ] && [ -n "$CONTENT_FILE" ]; then
    echo "discord-webhook.sh: pass --content or --content-file, not both" >&2
    exit 1
fi
if [ -z "$CONTENT" ] && [ -z "$CONTENT_FILE" ]; then
    echo "discord-webhook.sh: missing --content or --content-file" >&2
    usage 1
fi
if [ -n "$CONTENT_FILE" ]; then
    if [ ! -f "$CONTENT_FILE" ]; then
        echo "discord-webhook.sh: --content-file not found: $CONTENT_FILE" >&2
        exit 1
    fi
    CONTENT="$(cat "$CONTENT_FILE")"
fi
if [ -z "${CONTENT//[[:space:]]/}" ]; then
    echo "discord-webhook.sh: content is empty" >&2
    exit 1
fi

read_config_key() {
    python3 - <<'PY'
import tomllib
from pathlib import Path

cfg_path = Path(".agent_team/config.toml")
key = "AGENT_TEAM_DISCORD_WEBHOOK"
if cfg_path.exists():
    try:
        cfg = tomllib.loads(cfg_path.read_text())
    except Exception:
        cfg = {}
    configured = str(cfg.get("comms", {}).get("discord_webhook_env", "")).strip()
    if configured:
        key = configured
print(key)
PY
}

read_env_value() {
    python3 - "$@" <<'PY'
import sys
from pathlib import Path

path = Path(sys.argv[1])
name = sys.argv[2]

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
    if not sep or key.strip() != name:
        continue
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
        value = value[1:-1]
    if value:
        print(value)
        sys.exit(0)
sys.exit(1)
PY
}

candidate_env_files() {
    if [ -n "$ENV_FILE" ]; then
        printf '%s\n' "$ENV_FILE"
        return
    fi
    [ -f "$PWD/.env" ] && printf '%s\n' "$PWD/.env"
    if command -v git >/dev/null 2>&1; then
        local repo_root
        repo_root="$(git worktree list --porcelain 2>/dev/null | awk '/^worktree/ {print $2; exit}')"
        if [ -n "$repo_root" ] && [ "$repo_root" != "$PWD" ] && [ -f "$repo_root/.env" ]; then
            printf '%s\n' "$repo_root/.env"
        fi
    fi
}

resolve_webhook() {
    local key="$1"
    local env_file
    while IFS= read -r env_file; do
        [ -z "$env_file" ] && continue
        if [ ! -f "$env_file" ]; then
            continue
        fi
        local value
        if value="$(read_env_value "$env_file" "$key")"; then
            printf '%s\n' "$value"
            return 0
        fi
    done < <(candidate_env_files)
    return 1
}

WEBHOOK_ENV_KEY="$(read_config_key)"
if ! WEBHOOK="$(resolve_webhook "$WEBHOOK_ENV_KEY")"; then
    echo "discord-webhook.sh: no Discord webhook found for $WEBHOOK_ENV_KEY in .env" >&2
    exit 2
fi

PAYLOAD="$(jq -n --arg content "$CONTENT" '{content: $content}')"
RESP_FILE="$(mktemp)"
cleanup() {
    rm -f "$RESP_FILE"
}
trap cleanup EXIT

post_once() {
    local http_code curl_status
    curl_status=0
    http_code="$(curl -sS -o "$RESP_FILE" -w '%{http_code}' \
        -H "Content-Type: application/json" \
        -d "$PAYLOAD" \
        "$WEBHOOK")" || curl_status=$?
    if [ "$curl_status" -ne 0 ]; then
        echo "discord-webhook.sh: curl failed with exit $curl_status" >&2
        return 1
    fi
    case "$http_code" in
        2*) return 0 ;;
        *)
            echo "discord-webhook.sh: Discord webhook returned HTTP $http_code" >&2
            if [ -s "$RESP_FILE" ]; then
                sed -n '1,8p' "$RESP_FILE" >&2
            fi
            return 1
            ;;
    esac
}

if ! post_once; then
    sleep 2
    post_once
fi
