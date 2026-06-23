#!/usr/bin/env bash
#
# Daemon-mode channel client: publish to / receive from / manage named
# channels managed by `agent-teamd`. Pairs with the `channel` skill.
#
# Usage:
#   channel.sh recv <name> [--wait <duration>]
#   channel.sh ack <name> <cursor>
#   channel.sh publish <name> <body...>            # alias: send
#   channel.sh subscribe <name>
#   channel.sh unsubscribe <name>
#   channel.sh ls

set -euo pipefail

usage() {
    cat <<'EOF' >&2
usage:
  channel.sh recv <name> [--wait <duration>]
  channel.sh ack <name> <cursor>
  channel.sh publish <name> <body...>            (alias: send)
  channel.sh subscribe <name>
  channel.sh unsubscribe <name>
  channel.sh ls
EOF
    exit 2
}

require_team_root() {
    if [[ -z "${AGENT_TEAM_ROOT:-}" ]]; then
        echo "channel.sh: AGENT_TEAM_ROOT not set — must run inside an agent-team session." >&2
        exit 2
    fi
}

require_instance() {
    if [[ -z "${AGENT_TEAM_INSTANCE:-}" ]]; then
        echo "channel.sh: AGENT_TEAM_INSTANCE not set — must run inside an agent-team session." >&2
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

require_daemon() {
    local sock
    sock="$(socket_path)"
    if [[ ! -S "$sock" ]]; then
        echo "channel.sh: daemon not running ($sock missing)." >&2
        echo "  Start it with \`agent-team daemon start\`." >&2
        exit 1
    fi
}

# url_encode_channel_name — turn `#name` into `%23name` for the URL path.
# We only need to encode the leading `#`; channel names are otherwise safe.
url_encode_channel_name() {
    local raw="$1"
    if [[ "$raw" == \#* ]]; then
        echo "%23${raw#\#}"
    else
        echo "$raw"
    fi
}

curl_socket() {
    # All requests go through the unix socket. `-sS` keeps stderr quiet on
    # success but loud on failure. `--fail-with-body` returns non-zero on
    # >= 400 so callers can check `$?` while still printing the error JSON.
    local sock
    sock="$(socket_path)"
    curl --unix-socket "$sock" -sS --fail-with-body "$@"
}

[[ $# -ge 1 ]] || usage
verb="$1"; shift

case "$verb" in
    recv)
        [[ $# -ge 1 ]] || usage
        name="$1"; shift
        wait_arg=""
        if [[ $# -ge 2 && "$1" == "--wait" ]]; then
            wait_arg="$2"
            shift 2
        fi
        require_team_root
        require_instance
        require_daemon
        AGENT_TEAM_ROOT="$AGENT_TEAM_ROOT" \
        AGENT_TEAM_INSTANCE="$AGENT_TEAM_INSTANCE" \
        CHANNEL_VERB=recv \
        CHANNEL_NAME="$name" \
        CHANNEL_WAIT="$wait_arg" \
            python3 "$(dirname "$0")/_channel_recv.py"
        ;;
    ack)
        [[ $# -ge 2 ]] || usage
        name="$1"; cursor="$2"; shift 2
        require_team_root
        require_instance
        require_daemon
        enc="$(url_encode_channel_name "$name")"
        export CHANNEL_INSTANCE="$AGENT_TEAM_INSTANCE"
        export CHANNEL_CURSOR="$cursor"
        payload=$(python3 -c 'import json, os; print(json.dumps({"instance": os.environ["CHANNEL_INSTANCE"], "cursor": int(os.environ["CHANNEL_CURSOR"])}))')
        curl_socket -X POST \
            -H "Content-Type: application/json" \
            -d "$payload" \
            "http://daemon/v1/channel/${enc}/ack" >/dev/null
        echo "acked ${name} up to cursor=${cursor}"
        ;;
    publish|send)
        [[ $# -ge 2 ]] || usage
        name="$1"; shift
        body="$*"
        require_team_root
        require_instance
        require_daemon
        enc="$(url_encode_channel_name "$name")"
        export CHANNEL_SENDER="$AGENT_TEAM_INSTANCE"
        export CHANNEL_BODY="$body"
        payload=$(python3 -c 'import json, os; print(json.dumps({"sender": os.environ["CHANNEL_SENDER"], "body": os.environ["CHANNEL_BODY"]}))')
        resp=$(curl_socket -X POST \
            -H "Content-Type: application/json" \
            -d "$payload" \
            "http://daemon/v1/channel/${enc}/publish")
        export CHANNEL_RESP="$resp" CHANNEL_NAME_DISPLAY="$name"
        python3 - <<'PY'
import json, os
d = json.loads(os.environ["CHANNEL_RESP"])
print(f"published {os.environ['CHANNEL_NAME_DISPLAY']} seq={d['seq']} ts={d['ts']}")
PY
        ;;
    subscribe)
        [[ $# -ge 1 ]] || usage
        name="$1"; shift
        require_team_root
        require_instance
        require_daemon
        enc="$(url_encode_channel_name "$name")"
        export CHANNEL_INSTANCE="$AGENT_TEAM_INSTANCE"
        payload=$(python3 -c 'import json, os; print(json.dumps({"instance": os.environ["CHANNEL_INSTANCE"]}))')
        resp=$(curl_socket -X POST \
            -H "Content-Type: application/json" \
            -d "$payload" \
            "http://daemon/v1/channel/${enc}/subscribe")
        export CHANNEL_RESP="$resp" CHANNEL_NAME_DISPLAY="$name"
        python3 - <<'PY'
import json, os
d = json.loads(os.environ["CHANNEL_RESP"])
fresh = "(new)" if d["subscribed"] else "(already subscribed)"
print(f"subscribed to {os.environ['CHANNEL_NAME_DISPLAY']} cursor={d['cursor']} {fresh}")
PY
        ;;
    unsubscribe)
        [[ $# -ge 1 ]] || usage
        name="$1"; shift
        require_team_root
        require_instance
        require_daemon
        enc="$(url_encode_channel_name "$name")"
        export CHANNEL_INSTANCE="$AGENT_TEAM_INSTANCE"
        payload=$(python3 -c 'import json, os; print(json.dumps({"instance": os.environ["CHANNEL_INSTANCE"]}))')
        resp=$(curl_socket -X POST \
            -H "Content-Type: application/json" \
            -d "$payload" \
            "http://daemon/v1/channel/${enc}/unsubscribe")
        export CHANNEL_RESP="$resp" CHANNEL_NAME_DISPLAY="$name"
        python3 - <<'PY'
import json, os
d = json.loads(os.environ["CHANNEL_RESP"])
ok = "(was subscribed)" if d["unsubscribed"] else "(was not subscribed)"
print(f"unsubscribed from {os.environ['CHANNEL_NAME_DISPLAY']} {ok}")
PY
        ;;
    ls)
        require_team_root
        require_daemon
        resp=$(curl_socket "http://daemon/v1/channels")
        export CHANNEL_RESP="$resp"
        python3 - <<'PY'
import json, os, sys
infos = json.loads(os.environ["CHANNEL_RESP"])
if not infos:
    print("(no channels)")
    sys.exit(0)
print(f"{'CHANNEL':<24} {'SUBS':<6} {'MSGS':<6} LAST")
for i in infos:
    last = i.get("last_message_ts") or "—"
    print(f"{i['name']:<24} {i['subscribers']:<6} {i['message_count']:<6} {last}")
PY
        ;;
    -h|--help|help) usage ;;
    *) usage ;;
esac
