#!/usr/bin/env bash
#
# Send REST or GraphQL calls to GitHub.
#
# Usage:
#   github-api.sh graphql '<query-string>' [--variables '<json>']
#   github-api.sh graphql --query-file <path> [--variables '<json>']
#   github-api.sh rest <METHOD> <path> [--data '<json>']

set -euo pipefail

usage() {
    sed -n '3,9p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-1}"
}

MODE="${1:-}"
if [ -z "$MODE" ]; then
    echo "github-api.sh: missing mode: graphql or rest" >&2
    usage 1
fi
shift

check_github_config() {
    python3 - <<'PY'
import sys
import tomllib
from pathlib import Path

cfg_path = Path(".agent_team/config.toml")
if not cfg_path.exists():
    sys.exit(
        "github-api.sh: GitHub not configured; .agent_team/config.toml was not found. "
        "Run `agent-team init` first, then set [pm].provider = \"github\" with "
        "[github].owner and [github].repo."
    )

cfg = tomllib.loads(cfg_path.read_text())
pm_provider = cfg.get("pm", {}).get("provider") or cfg.get("team", {}).get("pm_tool", "none")
if pm_provider != "github":
    sys.exit(
        "github-api.sh: GitHub not configured for this repo. "
        "Set [pm].provider = \"github\" plus [github].owner and [github].repo "
        "in .agent_team/config.toml, or use ticketless jobs."
    )

github = cfg.get("github", {})
missing = [key for key in ("owner", "repo") if not github.get(key)]
if missing:
    sys.exit(
        "github-api.sh: GitHub is enabled but missing config: "
        + ", ".join(f"[github].{key}" for key in missing)
        + ". Set them in .agent_team/config.toml or re-run init with "
        "`--set pm.provider=github --set github.owner=<owner> "
        "--set github.repo=<repo>`."
    )
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

resolve_token() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        return 0
    fi
    if [ -n "${GH_TOKEN:-}" ]; then
        GITHUB_TOKEN="$GH_TOKEN"
        return 0
    fi

    local env_files=()
    [ -f "$PWD/.env" ] && env_files+=("$PWD/.env")
    if command -v git >/dev/null 2>&1; then
        local repo_root
        repo_root="$(git worktree list --porcelain 2>/dev/null | awk '/^worktree/ {print $2; exit}')"
        if [ -n "$repo_root" ] && [ "$repo_root" != "$PWD" ] && [ -f "$repo_root/.env" ]; then
            env_files+=("$repo_root/.env")
        fi
    fi

    for env_file in "${env_files[@]:-}"; do
        [ -z "$env_file" ] && continue
        local token
        if token="$(read_env_value "$env_file" GITHUB_TOKEN GH_TOKEN)"; then
            GITHUB_TOKEN="$token"
            return 0
        fi
    done
    return 1
}

check_github_config

if ! resolve_token; then
    echo "github-api.sh: no GitHub token found (tried GITHUB_TOKEN, GH_TOKEN in env and \$PWD/.env)" >&2
    exit 1
fi

case "$MODE" in
    graphql)
        QUERY=""
        QUERY_FILE=""
        VARIABLES="{}"
        while [ $# -gt 0 ]; do
            case "$1" in
                --query-file)
                    if [ $# -lt 2 ]; then
                        echo "github-api.sh: --query-file requires a path argument" >&2
                        usage 1
                    fi
                    QUERY_FILE="$2"
                    shift 2
                    ;;
                --variables)
                    if [ $# -lt 2 ]; then
                        echo "github-api.sh: --variables requires a JSON argument" >&2
                        usage 1
                    fi
                    VARIABLES="$2"
                    shift 2
                    ;;
                -h|--help)
                    usage 0
                    ;;
                -*)
                    echo "github-api.sh: unknown flag: $1" >&2
                    usage 1
                    ;;
                *)
                    if [ -n "$QUERY" ]; then
                        echo "github-api.sh: multiple positional query arguments" >&2
                        usage 1
                    fi
                    QUERY="$1"
                    shift
                    ;;
            esac
        done
        if [ -n "$QUERY" ] && [ -n "$QUERY_FILE" ]; then
            echo "github-api.sh: pass either an inline query OR --query-file, not both" >&2
            exit 1
        fi
        if [ -z "$QUERY" ] && [ -z "$QUERY_FILE" ]; then
            echo "github-api.sh: missing query (inline arg or --query-file)" >&2
            usage 1
        fi
        if [ -n "$QUERY_FILE" ] && [ ! -f "$QUERY_FILE" ]; then
            echo "github-api.sh: --query-file not found: $QUERY_FILE" >&2
            exit 1
        fi
        if [ -n "$QUERY_FILE" ]; then
            PAYLOAD="$(jq -n --rawfile q "$QUERY_FILE" --argjson v "$VARIABLES" '{query: $q, variables: $v}')"
        else
            PAYLOAD="$(jq -n --arg q "$QUERY" --argjson v "$VARIABLES" '{query: $q, variables: $v}')"
        fi
        curl -sS --fail-with-body "${AGENT_TEAM_GITHUB_GRAPHQL_URL:-https://api.github.com/graphql}" \
            -H "Authorization: Bearer $GITHUB_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            -H "Content-Type: application/json" \
            -d "$PAYLOAD"
        ;;
    rest)
        if [ $# -lt 2 ]; then
            echo "github-api.sh: rest requires METHOD and path" >&2
            usage 1
        fi
        METHOD="$1"
        API_PATH="$2"
        shift 2
        DATA=""
        while [ $# -gt 0 ]; do
            case "$1" in
                --data)
                    if [ $# -lt 2 ]; then
                        echo "github-api.sh: --data requires a JSON argument" >&2
                        usage 1
                    fi
                    DATA="$2"
                    shift 2
                    ;;
                -h|--help)
                    usage 0
                    ;;
                *)
                    echo "github-api.sh: unknown rest argument: $1" >&2
                    usage 1
                    ;;
            esac
        done
        BASE="${AGENT_TEAM_GITHUB_REST_URL:-https://api.github.com}"
        URL="${BASE%/}/${API_PATH#/}"
        curl_args=(
            -sS --fail-with-body
            -X "$METHOD"
            "$URL"
            -H "Authorization: Bearer $GITHUB_TOKEN"
            -H "Accept: application/vnd.github+json"
            -H "X-GitHub-Api-Version: 2022-11-28"
        )
        if [ -n "$DATA" ]; then
            curl_args+=(-H "Content-Type: application/json" -d "$DATA")
        fi
        curl "${curl_args[@]}"
        ;;
    -h|--help)
        usage 0
        ;;
    *)
        echo "github-api.sh: unknown mode: $MODE" >&2
        usage 1
        ;;
esac
