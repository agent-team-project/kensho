#!/usr/bin/env bash
#
# Daemon-mode worker dispatch helper for the manager's assign-worker skill.
#
# Usage:
#   assign_worker.sh dispatch --ticket SQU-42 --kickoff "implement SQU-42 ..."
#   assign_worker.sh dispatch --ticket SQU-42 --kickoff-file /tmp/kickoff.txt

set -euo pipefail

usage() {
    cat <<'EOF' >&2
usage:
  assign_worker.sh dispatch --ticket <PREFIX-n> (--kickoff <text> | --kickoff-file <path>)
      [--target worker] [--name worker-<ticket>] [--source <instance>] [--workspace worktree|repo]
EOF
    exit 2
}

require_team_root() {
    if [[ -z "${AGENT_TEAM_ROOT:-}" ]]; then
        echo "assign_worker.sh: AGENT_TEAM_ROOT not set — must run inside an agent-team session." >&2
        exit 2
    fi
}

socket_path() {
    echo "$AGENT_TEAM_ROOT/daemon.sock"
}

require_daemon() {
    local sock
    sock="$(socket_path)"
    if [[ ! -S "$sock" ]]; then
        echo "assign_worker.sh: daemon not running ($sock missing)." >&2
        echo "  Start it with \`agent-team daemon start\`." >&2
        exit 1
    fi
}

slugify_ticket() {
    printf '%s' "$1" |
        tr '[:upper:]' '[:lower:]' |
        tr -c 'a-z0-9._-' '-' |
        sed 's/^-*//; s/-*$//; s/--*/-/g'
}

curl_socket() {
    local sock
    sock="$(socket_path)"
    curl --unix-socket "$sock" -sS --fail-with-body "$@"
}

dispatch() {
    local ticket=""
    local kickoff=""
    local kickoff_file=""
    local target="worker"
    local source="${AGENT_TEAM_INSTANCE:-manager}"
    local name=""
    local workspace="worktree"
    local ticket_slug=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --ticket)
                [[ $# -ge 2 ]] || usage
                ticket="$2"
                shift 2
                ;;
            --kickoff)
                [[ $# -ge 2 ]] || usage
                kickoff="$2"
                shift 2
                ;;
            --kickoff-file)
                [[ $# -ge 2 ]] || usage
                kickoff_file="$2"
                shift 2
                ;;
            --target)
                [[ $# -ge 2 ]] || usage
                target="$2"
                shift 2
                ;;
            --name)
                [[ $# -ge 2 ]] || usage
                name="$2"
                shift 2
                ;;
            --source)
                [[ $# -ge 2 ]] || usage
                source="$2"
                shift 2
                ;;
            --workspace)
                [[ $# -ge 2 ]] || usage
                workspace="$2"
                shift 2
                ;;
            -h|--help|help)
                usage
                ;;
            *)
                usage
                ;;
        esac
    done

    [[ -n "$ticket" ]] || usage
    ticket_slug="$(slugify_ticket "$ticket")"
    if [[ -z "$ticket_slug" ]]; then
        echo "assign_worker.sh: ticket produced an empty job id: $ticket" >&2
        exit 2
    fi
    case "$workspace" in
        worktree|repo) ;;
        *)
            echo "assign_worker.sh: --workspace must be 'worktree' or 'repo'." >&2
            exit 2
            ;;
    esac
    if [[ -n "$kickoff_file" ]]; then
        kickoff="$(cat "$kickoff_file")"
    fi
    [[ -n "$kickoff" ]] || usage

    if [[ -z "$name" ]]; then
        name="${target}-${ticket_slug}"
    fi

    export ASSIGN_WORKER_SOURCE="$source"
    export ASSIGN_WORKER_TARGET="$target"
    export ASSIGN_WORKER_NAME="$name"
    export ASSIGN_WORKER_JOB_ID="$ticket_slug"
    export ASSIGN_WORKER_TICKET="$ticket"
    export ASSIGN_WORKER_KICKOFF="$kickoff"
    export ASSIGN_WORKER_WORKSPACE="$workspace"
    payload=$(python3 - <<'PY'
import json
import os

event_payload = {
    "source": os.environ["ASSIGN_WORKER_SOURCE"],
    "target": os.environ["ASSIGN_WORKER_TARGET"],
    "name": os.environ["ASSIGN_WORKER_NAME"],
    "job_id": os.environ["ASSIGN_WORKER_JOB_ID"],
    "ticket": os.environ["ASSIGN_WORKER_TICKET"],
    "kickoff": os.environ["ASSIGN_WORKER_KICKOFF"],
    "workspace": os.environ["ASSIGN_WORKER_WORKSPACE"],
}
print(json.dumps({"type": "agent.dispatch", "payload": event_payload}))
PY
)

    curl_socket -X POST \
        -H "Content-Type: application/json" \
        -d "$payload" \
        http://daemon/v1/event
    echo
}

[[ $# -ge 1 ]] || usage
verb="$1"; shift

case "$verb" in
    dispatch)
        require_team_root
        require_daemon
        dispatch "$@"
        ;;
    -h|--help|help)
        usage
        ;;
    *)
        usage
        ;;
esac
