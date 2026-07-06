---
name: inbox
description: Read pending messages from your daemon-managed mailbox and ack them once handled. Pairs with `agent-team daemon`'s `/v1/message` endpoint. Use when the daemon is running and another instance has POSTed a message to you; in no-daemon team-mode, keep using SendMessage.
---

# Daemon-mode inbox

When `agent-teamd` is running, cross-instance messages go through it: a sender POSTs `/v1/message {to, from, body}`, the daemon appends the message to your `mailbox.jsonl`, and you read it via this skill. The skill is a thin wrapper around `<state-dir>/../../../daemon/<your-instance>/mailbox.jsonl` — the file is the contract.

In Claude Code's tmux team mode (no daemon, `~/.claude/teams/` populated) the existing `SendMessage` tool is the right channel — it surfaces messages as native conversation events. The inbox skill is the daemon-mode equivalent for cases where Claude Code's tmux team isn't in play.

## When to call

- **At the start of every action** — `inbox check` so you don't keep working on a stale plan when a teammate has redirected you.
- **Before going idle** — re-check; the field a blocker resolution lands on is your inbox.
- **After a teammate explicitly says "see inbox"** — obvious, but worth stating.

`inbox check` returns 0 with `(no new messages)` when nothing is unread; that's the cheap normal case, run it freely.

## Surface

```sh
inbox check                 # list unread messages (since last ack)
inbox ack <id>              # mark message <id> handled; cursor advances past it
inbox send <to> <body>      # POST /v1/message — convenience for sending
```

`inbox send` exists so any agent can talk back to a teammate without learning the curl-over-unix-socket dance. The daemon's `/v1/message` endpoint is the source of truth; the skill is a wrapper.

## Examples

**Routine inbox check at the top of a step:**

```sh
"$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh check
```

Output (when there's a message):

```
1 new message(s):

[7c8e2d4a-...] from manager  (2026-04-29T14:02:11Z)
   Switch to SQU-30 — SQU-29 is on hold pending review.
```

**Acking the message after acting on it:**

```sh
"$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh ack 7c8e2d4a-...
```

**Sending a message to a teammate:**

```sh
"$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh send manager "SQU-30 PR opened: https://github.com/.../pull/42"
```

## Implementation notes

- Reads `mailbox.jsonl` since the last ID written to `mailbox-cursor.txt`. If the cursor is empty / points at a non-existent ID, every message is treated as unread.
- `ack` writes the cursor atomically (`tmp` + `rename`).
- `send` uses `AGENT_TEAM_DAEMON_URL` with `Authorization: Bearer $(<"$AGENT_TEAM_DAEMON_TOKEN_FILE")` when set, otherwise `curl --unix-socket "$AGENT_TEAM_DAEMON_SOCKET"` and falls back to `$AGENT_TEAM_ROOT/daemon.sock`. The host portion of Unix-socket URLs doesn't matter — the unix dial overrides it.
- The daemon must be running for sends. If neither `AGENT_TEAM_DAEMON_URL` nor the resolved daemon socket is available, `inbox check` reads the file directly (still works — the messages live on disk regardless of daemon liveness); `inbox send` errors with a clear "daemon not running" message.
- All scripts sign nothing on your behalf. Compose your own messages.
