#!/usr/bin/env python3
"""Receive unread messages from a daemon-managed channel.

Helper for the `channel` skill's `recv` verb. The bash dispatcher
(`channel.sh`) parses arguments and re-invokes this script with state passed
via environment variables — same pattern as `_inbox_read.py`.

Reads pull from the daemon over its unix socket using stdlib `http.client`
(no curl dependency for the GET path). Writes pretty output to stdout.

Environment:
- AGENT_TEAM_ROOT: absolute path to .agent_team/
- AGENT_TEAM_INSTANCE: this instance's name
- CHANNEL_NAME: channel name (e.g. "#blocked")
- CHANNEL_WAIT: optional duration string (e.g. "30s") for long-poll
"""

from __future__ import annotations

import http.client
import json
import os
import socket
import sys
import urllib.parse
from pathlib import Path


def main() -> int:
    team_root = Path(os.environ["AGENT_TEAM_ROOT"])
    instance = os.environ["AGENT_TEAM_INSTANCE"]
    name = os.environ["CHANNEL_NAME"]
    wait = os.environ.get("CHANNEL_WAIT", "").strip()

    if not name.startswith("#"):
        print(f"channel: name {name!r} must start with '#'", file=sys.stderr)
        return 2

    socket_path = str(team_root / "daemon.sock")
    if not Path(socket_path).exists():
        print(f"channel: daemon not running ({socket_path} missing).", file=sys.stderr)
        return 1

    encoded_name = urllib.parse.quote(name, safe="")
    qs = {"instance": instance}
    if wait:
        qs["wait"] = wait
    path = f"/v1/channel/{encoded_name}/messages?{urllib.parse.urlencode(qs)}"

    body = _get_unix(socket_path, path)
    payload = json.loads(body)
    msgs = payload.get("messages") or []
    cursor = payload.get("cursor", 0)

    if not msgs:
        print("(no new messages)")
        return 0

    plural = "" if len(msgs) == 1 else "s"
    first_seq = msgs[0]["seq"]
    last_seq = msgs[-1]["seq"]
    cursor_was = first_seq - 1
    print(f"{len(msgs)} new message{plural} on {name} (cursor was {cursor_was}, now {last_seq}):")
    print()
    for m in msgs:
        sender = m.get("sender") or "(unknown)"
        ts = m.get("ts") or ""
        seq = m.get("seq")
        body_text = m.get("body") or ""
        print(f"[seq={seq}] from {sender}  ({ts})")
        for line in body_text.splitlines() or [""]:
            print(f"   {line}")
        print()
    print(f'Ack with: channel.sh ack "{name}" {cursor}')
    return 0


def _get_unix(socket_path: str, path: str) -> str:
    """GET against a unix-socket HTTP server."""
    conn = http.client.HTTPConnection("localhost")
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        sock.connect(socket_path)
        conn.sock = sock
        conn.request("GET", path)
        resp = conn.getresponse()
        if resp.status != 200:
            err_body = resp.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"daemon: GET {path}: {resp.status} {err_body}")
        return resp.read().decode("utf-8")
    finally:
        sock.close()
        conn.close()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
