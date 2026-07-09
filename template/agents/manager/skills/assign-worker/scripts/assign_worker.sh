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
    if [[ -n "${AGENT_TEAM_DAEMON_SOCKET:-}" ]]; then
        echo "$AGENT_TEAM_DAEMON_SOCKET"
    else
        echo "$AGENT_TEAM_ROOT/daemon.sock"
    fi
}

daemon_available() {
    if [[ -n "${AGENT_TEAM_DAEMON_URL:-}" ]]; then
        return 0
    fi
    local sock
    sock="$(socket_path)"
    [[ -S "$sock" ]]
}

daemon_token() {
    if [[ -z "${AGENT_TEAM_DAEMON_TOKEN_FILE:-}" ]]; then
        echo "assign_worker.sh: AGENT_TEAM_DAEMON_TOKEN_FILE not set for daemon HTTP auth." >&2
        exit 2
    fi
    if [[ ! -f "$AGENT_TEAM_DAEMON_TOKEN_FILE" ]]; then
        echo "assign_worker.sh: daemon token file missing: $AGENT_TEAM_DAEMON_TOKEN_FILE" >&2
        exit 1
    fi
    local token
    IFS= read -r token < "$AGENT_TEAM_DAEMON_TOKEN_FILE" || true
    if [[ -z "$token" ]]; then
        echo "assign_worker.sh: daemon token file is empty: $AGENT_TEAM_DAEMON_TOKEN_FILE" >&2
        exit 1
    fi
    printf '%s' "$token"
}

slugify_ticket() {
    printf '%s' "$1" |
        tr '[:upper:]' '[:lower:]' |
        tr -c 'a-z0-9._-' '-' |
        sed 's/^-*//; s/-*$//; s/--*/-/g'
}

curl_daemon() {
    if [[ -n "${AGENT_TEAM_DAEMON_URL:-}" ]]; then
        local args=("$@")
        local last_index=$((${#args[@]} - 1))
        local endpoint="${args[last_index]}"
        args[last_index]="${AGENT_TEAM_DAEMON_URL%/}${endpoint#http://daemon}"
        local token
        token="$(daemon_token)"
        curl -sS --fail-with-body -H "Authorization: Bearer $token" "${args[@]}"
        return
    fi
    local sock
    sock="$(socket_path)"
    curl --unix-socket "$sock" -sS --fail-with-body "$@"
}

write_outbox_event() {
    local payload="$1"
    local ticket_slug="$2"

    export ASSIGN_WORKER_OUTBOX_PAYLOAD="$payload"
    export ASSIGN_WORKER_OUTBOX_TICKET_SLUG="$ticket_slug"
    python3 - <<'PY'
from datetime import datetime, timezone
import json
import os
import pathlib
import tempfile
import uuid

root = os.environ.get("AGENT_TEAM_ROOT", "")
if not root:
    raise SystemExit("assign_worker.sh: AGENT_TEAM_ROOT not set")

event = json.loads(os.environ["ASSIGN_WORKER_OUTBOX_PAYLOAD"])
event_type = event.get("type", "")
if not event_type:
    raise SystemExit("assign_worker.sh: outbox event type missing")
payload = event.get("payload") or {}
ticket_slug = os.environ.get("ASSIGN_WORKER_OUTBOX_TICKET_SLUG", "event")
source = os.environ.get("AGENT_TEAM_INSTANCE", "")

pending = pathlib.Path(root) / "outbox" / "pending"
pending.mkdir(parents=True, exist_ok=True)
now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
stamp = now.replace("-", "").replace(":", "").replace(".", "-").replace("Z", "z")
event_id = f"{stamp}-{ticket_slug}-{uuid.uuid4().hex[:8]}"
item = {
    "id": event_id,
    "state": "pending",
    "type": event_type,
    "payload": payload,
    "source": source,
    "created_at": now,
    "updated_at": now,
}

fd, tmp_name = tempfile.mkstemp(prefix=f"{event_id}-", suffix=".json.tmp", dir=str(pending), text=True)
path = pending / f"{event_id}.json"
try:
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(item, f, indent=2)
        f.write("\n")
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp_name, path)
except Exception:
    try:
        os.unlink(tmp_name)
    except FileNotFoundError:
        pass
    raise

print(json.dumps({
    "matched": 0,
    "dispatched": [],
    "queued": [],
    "outbox": [{
        "id": event_id,
        "type": event_type,
        "path": str(path),
        "action": "queued_to_outbox"
    }],
    "message": "daemon unavailable; run `agent-team tick` or `agent-team drain` to publish the outbox event"
}))
PY
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
    local payload
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
    "deliverable": "pr",
    "workspace": os.environ["ASSIGN_WORKER_WORKSPACE"],
}
print(json.dumps({"type": "agent.dispatch", "payload": event_payload}))
PY
)

    if daemon_available; then
        curl_daemon -X POST \
            -H "Content-Type: application/json" \
            -d "$payload" \
            http://daemon/v1/event
        echo
    else
        write_outbox_event "$payload" "$ticket_slug"
    fi
}

[[ $# -ge 1 ]] || usage
verb="$1"; shift

case "$verb" in
    dispatch)
        require_team_root
        dispatch "$@"
        ;;
    -h|--help|help)
        usage
        ;;
    *)
        usage
        ;;
esac
