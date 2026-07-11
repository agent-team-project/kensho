# Status, Mailbox, and Channels

These file-backed communication surfaces let agents expose progress and receive messages without requiring a direct interactive session.

## Status Files

Agents write `.agent_team/state/<instance>/status.toml`.

Example:

```toml
[status]
phase = "blocked"
description = "Waiting for GitHub token"
since = "2026-06-22T10:00:00Z"

[work]
job = "squ-42"
ticket = "SQU-42"
branch = "worker-squ-42"
pr = "https://github.com/acme/app/pull/42"
```

Status is consumed by:

- `ps`
- `inspect`
- `monitor`
- `health --jobs`
- `job reconcile status`
- `job triage`
- `job show`
- team-scoped status and health commands

## Blocked Work

When status reports a blocked phase, operators can unblock the owning job:

```sh
agent-team job unblock squ-42 "Token configured; continue."
```

`job unblock`:

1. accepts blocked status-file previews
2. sends a mailbox message to the instance
3. marks the job running
4. records a job event

## Mailbox

The daemon mailbox provides durable messages to instances.

CLI:

```sh
agent-team send manager "Please summarize current jobs"
agent-team job send squ-42 "Please continue with the new constraint"
agent-team team send delivery --all "Pause after current step"
agent-team inbox ls --unread
agent-team inbox ls --team delivery --unread
agent-team inbox check manager
agent-team inbox ack manager --all
agent-team next --source inbox --reason unread
agent-team next --source inbox --reason unread --commands
```

Agent-side skills can read messages from the instance inbox.

Messages are stored locally so they can be inspected or delivered even when a runtime process restarts.

When a reply should go to a specific durable mailbox, include `--reply-to <instance>`.
Operator CLI consults to the advisor require this because the default sender
`(cli)` is not a durable inbox after the command exits:

```sh
agent-team send advisor --reply-to manager "Should we accept this architecture trade-off?"
```

For a hard steer to a running daemon-managed instance, use:

```sh
agent-team send manager "Stop and handle this before continuing." --interrupt
```

`send --interrupt` first records the message in the durable mailbox, then gracefully stops the runtime child and managed-resumes the same captured session with the unread mail delivered as resume context. Codex resumes receive the unread mailbox on `codex exec resume <session> -` stdin; Claude resumes read the already-delivered mailbox after restart. Without a captured session, the command refuses unless `--force` is supplied, because a fresh fallback loses conversation fidelity. The cost model is that at most the in-flight turn is lost; completed turns remain in the runtime session store. Avoid using it while an agent is intentionally in a critical shell section such as a push, even though the daemon only sends SIGTERM to the child process group and git operations are generally atomic enough to recover.

`agent-team inbox` is the human-facing read side of the mailbox:

- `inbox ls` summarizes total and unread messages per known instance mailbox.
- `inbox ls --team <team>` narrows the summary to declared team instances and their daemon-known ephemeral children.
- `inbox check [instance]` lists unread message IDs, senders, and bodies. With no instance argument, it reads `AGENT_TEAM_INSTANCE`.
- `inbox ack [instance] MESSAGE_ID` acknowledges only the next unread message and refuses to skip earlier unread messages; `--all` marks every current unread message read after you have handled them. Use `--dry-run` before changing the cursor.

`overview`, `team overview`, `next`, `team next`, `monitor`, and `team monitor` include unread inbox counts and actions. `snapshot`, `team snapshot`, `pipeline snapshot`, and `job snapshot` also include inbox summaries for handoff artifacts; latest message bodies are redacted unless `--no-redact` is used. Use `next --source inbox --reason unread` when a script or operator view should focus only on unread mailbox work, and add `--commands` when that script needs one command per line.

## Channels

Channels provide simple pub/sub style communication:

```sh
agent-team channels
agent-team channel show standup
agent-team channel publish standup "Worker squ-42 is blocked on review"
agent-team channel rm standup -f
```

Channels are useful for shared coordination that is not addressed to one instance.

## Design Notes

The status, mailbox, and channel surfaces are intentionally basic:

- no external broker
- no global service
- file-backed durability
- JSON/TOML/JSONL formats
- readable while daemon is down where possible

This keeps the local repo the source of truth and makes diagnostics reproducible.
