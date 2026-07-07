#!/usr/bin/env python3
"""Read or ack messages in $AGENT_TEAM_ROOT/daemon/<instance>/mailbox.jsonl.

Helper for the `inbox` skill. The bash dispatcher (`inbox.sh`) parses
arguments and re-invokes this script with state passed via environment
variables — the same pattern the `status` skill uses. Stdlib-only.

Reading semantics:
- `mailbox.jsonl` is the source of truth. One JSON object per line.
- The cursor at `mailbox-cursor.txt` stores the highest-acked message ID.
  Messages strictly after the cursor's match are "unread"; if the cursor
  is empty or points at an ID not in the file, every message is unread.
- `inbox check` prints unread messages and exits 0.
- `inbox ack <id>` writes the cursor atomically (tmp + rename) only when
  `<id>` is the next unread message. Use `inbox ack --all` to advance through
  every current message.

The on-disk schema is documented in `documentation/orchestrator.md`.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path


def main() -> int:
    team_root = Path(os.environ["AGENT_TEAM_ROOT"])
    instance = os.environ["AGENT_TEAM_INSTANCE"]
    daemon_dir = team_root / "daemon" / instance
    mailbox = daemon_dir / "mailbox.jsonl"
    cursor_file = daemon_dir / "mailbox-cursor.txt"

    verb = os.environ["INBOX_VERB"]

    if verb == "check":
        msgs = _read_unacked(mailbox, cursor_file)
        if not msgs:
            print("(no new messages)")
            return 0
        plural = "" if len(msgs) == 1 else "s"
        print(f"{len(msgs)} new message{plural}:")
        print()
        for m in msgs:
            sender = m.get("from") or "(unknown)"
            ts = m.get("ts") or ""
            mid = m.get("id") or "(no-id)"
            body = m.get("body") or ""
            print(f"[{mid}] from {sender}  ({ts})")
            for line in body.splitlines() or [""]:
                print(f"   {line}")
            print()
        print("Ack with: inbox ack <id>")
        return 0

    if verb == "ack":
        ack_all = os.environ.get("INBOX_ACK_ALL") == "1"
        ack_id = os.environ.get("INBOX_ACK_ID", "").strip()
        if not ack_all and not ack_id:
            print("_inbox_read.py: ack: missing INBOX_ACK_ID", file=sys.stderr)
            return 2
        # Validate the id is in the file — silently ack'ing a non-existent
        # id would let typos hide real messages.
        all_msgs = _read_all(mailbox)
        if not all_msgs:
            if ack_all:
                print("acked 0 messages")
                return 0
            print(f"_inbox_read.py: ack: id {ack_id!r} not in mailbox", file=sys.stderr)
            return 2
        if ack_all:
            target_index = len(all_msgs) - 1
        else:
            target_index = _message_index(all_msgs, ack_id)
            if target_index < 0:
                print(f"_inbox_read.py: ack: id {ack_id!r} not in mailbox", file=sys.stderr)
                return 2
        cursor_index = _message_index(all_msgs, _read_cursor(cursor_file))
        if cursor_index >= target_index and cursor_index >= 0:
            print(f"already acked {all_msgs[target_index].get('id')}")
            return 0
        next_unread_index = max(cursor_index + 1, 0)
        if not ack_all and target_index > next_unread_index:
            next_id = all_msgs[next_unread_index].get("id") or "(no-id)"
            print(
                f"_inbox_read.py: ack: id {ack_id!r} is not the next unread message; "
                f"handle {next_id!r} first or use inbox ack --all",
                file=sys.stderr,
            )
            return 2
        ack_id = all_msgs[target_index].get("id") or ""
        if not ack_id:
            print("_inbox_read.py: ack: target message has no id", file=sys.stderr)
            return 2
        _write_cursor(cursor_file, ack_id)
        if ack_all:
            print(f"acked all through {ack_id}")
        else:
            print(f"acked {ack_id}")
        return 0

    print(f"_inbox_read.py: unknown verb: {verb}", file=sys.stderr)
    return 2


def _read_all(path: Path) -> list[dict]:
    if not path.exists():
        return []
    out: list[dict] = []
    with path.open("r", encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                out.append(json.loads(line))
            except json.JSONDecodeError:
                # One bad line shouldn't blind every other message.
                continue
    return out


def _read_cursor(path: Path) -> str:
    if not path.exists():
        return ""
    return path.read_text(encoding="utf-8").strip()


def _read_unacked(mailbox: Path, cursor_file: Path) -> list[dict]:
    msgs = _read_all(mailbox)
    cursor = _read_cursor(cursor_file)
    if not cursor:
        return msgs
    for i, m in enumerate(msgs):
        if m.get("id") == cursor:
            return msgs[i + 1 :]
    # Cursor points at an ID we no longer have — surface everything.
    return msgs


def _message_index(msgs: list[dict], message_id: str) -> int:
    if not message_id:
        return -1
    for i, msg in enumerate(msgs):
        if msg.get("id") == message_id:
            return i
    return -1


def _write_cursor(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(value + "\n", encoding="utf-8")
    os.replace(tmp, path)


if __name__ == "__main__":
    sys.exit(main())
