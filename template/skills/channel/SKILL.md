---
name: channel
description: Subscribe to and exchange messages on daemon-managed pub/sub channels — broadcast to many listeners, durable replay across restarts, long-poll receive. Pairs with `agent-teamd`'s `/v1/channel/...` endpoints. Use when you want fan-out (one publisher, many subscribers) or topic-shaped coordination; for direct one-to-one messages, use the `inbox` skill.
---

# Channels (publish / subscribe)

When `agent-teamd` is running, named channels carry broadcast messages between instances. A publisher POSTs `/v1/channel/<name>/publish`, the daemon appends to the channel's log, and every subscriber drains messages they haven't seen yet via `/v1/channel/<name>/messages`. Cursor advancement is explicit (you call `ack`), so a crash mid-handle doesn't lose work.

Channel names look like `#deploys`, `#blocked`, `#review-requests` — lowercase letters, digits, dashes, leading `#`.

In Claude Code's tmux team mode (no daemon, `~/.claude/teams/` populated) channels don't exist; use `SendMessage` for direct messages, or stand up the daemon if you want broadcast.

## Auto-subscription

Persistent agents declare interest in channels via frontmatter:

```yaml
---
description: ...
subscribes:
  - "#blocked"
  - "#review-requests"
---
```

When `agent-team run` launches such an agent against a running daemon, the launcher POSTs `/subscribe` before the spawn. Across daemon stop+start, your cursor stays where it was — you replay messages published while you were down.

## Surface

```sh
channel.sh recv <name> [--wait <duration>]   # drain unread; does NOT auto-ack
channel.sh ack <name> <cursor>               # mark up-to-cursor handled
channel.sh publish <name> <body...>          # send a message (alias: send)
channel.sh subscribe <name>                  # manual subscribe (rarely needed)
channel.sh unsubscribe <name>                # leave a channel
channel.sh ls                                # list known channels
```

`recv` does not auto-ack — read first, act, then ack. Acking before handling is how you lose messages on crash.

## When to call

- **At step boundaries**, alongside `inbox check`: `channel.sh recv "#blocked"`. New broadcasts on channels you subscribe to surface here, not in inbox.
- **Before going idle.** Same reasoning as inbox.
- **After publishing**, only if you want to confirm. Publishes are one-shot — you don't need to wait for delivery acknowledgement.

`recv` returns 0 with `(no new messages)` when nothing is unread; it's cheap.

## Examples

**Receive on a channel you're subscribed to:**

```sh
"$AGENT_TEAM_ROOT"/skills/channel/scripts/channel.sh recv "#blocked"
```

Output (when there's a message):

```
2 new message(s) on #blocked (cursor was 7, now 9):

[seq=8] from worker-squ-30  (2026-04-29T14:12:01Z)
   Stuck on whether to roll back the migration; pinging both for input.

[seq=9] from manager  (2026-04-29T14:14:22Z)
   Roll back. Reverting now.

Ack with: channel.sh ack "#blocked" 9
```

**Acking after handling:**

```sh
"$AGENT_TEAM_ROOT"/skills/channel/scripts/channel.sh ack "#blocked" 9
```

**Publishing a broadcast:**

```sh
"$AGENT_TEAM_ROOT"/skills/channel/scripts/channel.sh publish "#deploys" "v1.42 rolled out — see https://..."
```

**Long-poll for the next message (waits up to 30s):**

```sh
"$AGENT_TEAM_ROOT"/skills/channel/scripts/channel.sh recv "#blocked" --wait 30s
```

## Implementation notes

- Calls `curl --unix-socket "$AGENT_TEAM_DAEMON_SOCKET"` when set, falling back to `$AGENT_TEAM_ROOT/daemon.sock`. The host portion of the URL doesn't matter — the unix dial overrides it.
- The `#` in the channel name is URL-encoded automatically; pass it verbatim on the command line (use quotes so the shell doesn't read it as a comment).
- `recv` reads from the daemon, prints the messages, and exits without modifying cursor state. The daemon's `messages` endpoint is read-only — only `ack` advances the cursor.
- Subscribe is idempotent: re-subscribing returns the existing cursor. Unsubscribe is also idempotent.
- The daemon must be running for any channel verb. If the resolved daemon socket is missing, the script errors with a clear "daemon not running" message — `agent-team daemon start` to fix.
