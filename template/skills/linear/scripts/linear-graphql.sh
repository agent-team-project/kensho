#!/usr/bin/env bash
#
# Send a GraphQL call to Linear.
#
# Bundled with the `linear` skill in agent-team. Invoked as
#   ${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh
#
# Loads a Linear API key from env or $PWD/.env. Prefers LINEAR_API_KEY,
# falls back to LINEAR_USER_API_KEY. Builds the request body with jq,
# POSTs to https://api.linear.app/graphql. Raw response is streamed to
# stdout so callers can pipe through `jq` to pretty-print or filter.
#
# Usage:
#   linear-graphql.sh '<query-string>' [--variables '<json>']
#   linear-graphql.sh --query-file <path> [--variables '<json>']
#
# Examples:
#   linear-graphql.sh 'query { viewer { id name } }' | jq .
#
#   linear-graphql.sh \
#     'query($id: String!) { issue(id: $id) { identifier title } }' \
#     --variables '{"id":"BENCH-166"}' | jq .

set -euo pipefail

usage() {
    sed -n '3,22p' "$0" | sed 's/^# \{0,1\}//'
    exit "${1:-1}"
}

QUERY=""
QUERY_FILE=""
VARIABLES="{}"

while [ $# -gt 0 ]; do
    case "$1" in
        --query-file)
            if [ $# -lt 2 ]; then
                echo "linear-graphql.sh: --query-file requires a path argument" >&2
                usage 1
            fi
            QUERY_FILE="$2"
            shift 2
            ;;
        --variables)
            if [ $# -lt 2 ]; then
                echo "linear-graphql.sh: --variables requires a JSON argument" >&2
                usage 1
            fi
            VARIABLES="$2"
            shift 2
            ;;
        -h|--help)
            usage 0
            ;;
        --)
            shift
            break
            ;;
        -*)
            echo "linear-graphql.sh: unknown flag: $1" >&2
            usage 1
            ;;
        *)
            if [ -n "$QUERY" ]; then
                echo "linear-graphql.sh: multiple positional query arguments" >&2
                usage 1
            fi
            QUERY="$1"
            shift
            ;;
    esac
done

if [ -n "$QUERY" ] && [ -n "$QUERY_FILE" ]; then
    echo "linear-graphql.sh: pass either an inline query OR --query-file, not both" >&2
    exit 1
fi

if [ -z "$QUERY" ] && [ -z "$QUERY_FILE" ]; then
    echo "linear-graphql.sh: missing query (inline arg or --query-file)" >&2
    usage 1
fi

if [ -n "$QUERY_FILE" ] && [ ! -f "$QUERY_FILE" ]; then
    echo "linear-graphql.sh: --query-file not found: $QUERY_FILE" >&2
    exit 1
fi

check_linear_config() {
    python3 - <<'PY'
import sys
import tomllib
from pathlib import Path

cfg_path = Path(".agent_team/config.toml")
if not cfg_path.exists():
    sys.exit(
        "linear-graphql.sh: Linear not configured; .agent_team/config.toml was not found. "
        "Run `agent-team init` first, then set [pm].provider = \"linear\" with "
        "[linear].team_id and [linear].ticket_prefix."
    )

cfg = tomllib.loads(cfg_path.read_text())
pm_provider = cfg.get("pm", {}).get("provider") or cfg.get("team", {}).get("pm_tool", "none")
if pm_provider != "linear":
    sys.exit(
        "linear-graphql.sh: Linear not configured for this repo. "
        "Set [pm].provider = \"linear\" plus [linear].team_id and "
        "[linear].ticket_prefix in .agent_team/config.toml, or use "
        "`agent-team job create \"<kickoff>\" --dispatch --workspace worktree` "
        "for ticketless work."
    )

linear = cfg.get("linear", {})
missing = [key for key in ("team_id", "ticket_prefix") if not linear.get(key)]
if missing:
    sys.exit(
        "linear-graphql.sh: Linear is enabled but missing config: "
        + ", ".join(f"[linear].{key}" for key in missing)
        + ". Set them in .agent_team/config.toml or re-run init with "
        "`--set pm.provider=linear --set linear.team_id=<uuid> "
        "--set linear.ticket_prefix=<PREFIX>`."
    )
PY
}

check_linear_config

# Resolve an API key. Prefer LINEAR_API_KEY; fall back to LINEAR_USER_API_KEY.
# If neither is set in the shell, try to source $PWD/.env (consumer repo convention)
# or the main working tree's .env if inside a git tree. `git worktree list
# --porcelain` is used instead of `git rev-parse --show-toplevel` so that calls
# from inside a linked worktree still find the primary repo's .env.
resolve_api_key() {
    if [ -n "${LINEAR_API_KEY:-}" ]; then
        return 0
    fi
    if [ -n "${LINEAR_USER_API_KEY:-}" ]; then
        LINEAR_API_KEY="$LINEAR_USER_API_KEY"
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
        set -a
        # shellcheck disable=SC1090
        source "$env_file"
        set +a
        if [ -n "${LINEAR_API_KEY:-}" ]; then
            return 0
        fi
        if [ -n "${LINEAR_USER_API_KEY:-}" ]; then
            LINEAR_API_KEY="$LINEAR_USER_API_KEY"
            return 0
        fi
    done
    return 1
}

if ! resolve_api_key; then
    echo "linear-graphql.sh: no Linear API key found (tried LINEAR_API_KEY, LINEAR_USER_API_KEY in env and \$PWD/.env)" >&2
    exit 1
fi

if [ -n "$QUERY_FILE" ]; then
    PAYLOAD="$(jq -n \
        --rawfile q "$QUERY_FILE" \
        --argjson v "$VARIABLES" \
        '{query: $q, variables: $v}')"
else
    PAYLOAD="$(jq -n \
        --arg q "$QUERY" \
        --argjson v "$VARIABLES" \
        '{query: $q, variables: $v}')"
fi

curl -sS --fail-with-body https://api.linear.app/graphql \
    -H "Authorization: $LINEAR_API_KEY" \
    -H "Content-Type: application/json" \
    -d "$PAYLOAD"
