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
```

Agent-side skills can read messages from the instance inbox.

Messages are stored locally so they can be inspected or delivered even when a runtime process restarts.

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
