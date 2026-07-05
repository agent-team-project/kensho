# Messaging

Messaging is the operator path for steering agents after a session has started.
It is file-backed, daemon-aware, and visible in diagnostics.

## Surfaces

Use the narrowest command that knows the ownership you care about:

```sh
agent-team send manager "Please summarize current jobs"
agent-team job send squ-42 "Please continue with the new constraint"
agent-team pipeline send ticket_to_pr --dry-run --commands "checkpoint status"
agent-team team send delivery --all "Pause after your current step"
agent-team inbox ls --unread
agent-team inbox show manager --unread
agent-team inbox ack manager --all
agent-team channel publish standup "worker squ-42 is blocked on review"
```

`send` targets daemon-known instances or names declared in `instances.toml`.
When a declared instance is not running, the message is queued in its durable
mailbox for the next spawn or resume. Unknown undeclared names fail with
near-match suggestions so typos do not create stray mailboxes. Selectors such
as `--agent`, `--runtime`, `--status`, `--phase`, `--latest`, `--last`,
`--stale`, `--runtime-stale`, and `--unhealthy` operate on daemon-known
instances. `job send`, `pipeline send`, and `team send` keep the recipient set
tied to durable work ownership, which is usually safer than naming instances by
hand.

Messages live under the daemon state, so `inbox`, `overview`, `next`,
`monitor`, `snapshot`, `job snapshot`, `pipeline snapshot`, and team-scoped
views can show unread counts even when the runtime is not attached.

## Delivery Paths

There are three delivery paths. They differ in how quickly the message reaches
model context.

**Dispatch kickoff delivery** happens when a daemon dispatch starts a runtime.
Before launch, unread mailbox messages for that instance are appended to the
kickoff under `## Unread messages (delivered at dispatch)`; the daemon advances the mailbox
cursor and records that kickoff mail was delivered. This is useful for queued
work and manager-to-worker instructions that arrive before the child process
starts.

**Runtime hook injection** is the default soft push for already-running
sessions. The launcher generates a per-instance mailbox hook for both supported
runtimes. The hook drains unread messages and returns them as model-visible
additional context at `UserPromptSubmit` and `PreToolUse` boundaries. It does
not preempt an in-flight model response; it delivers at the next prompt or tool
boundary. Disable it at any config layer when needed:

```toml
[runtime.hooks]
mailbox_injection = false
```

For one launch:

```sh
agent-team run worker --set runtime.hooks.mailbox_injection=false
```

**Interrupt delivery** is the hard steer:

```sh
agent-team send worker-squ-42 \
  "Stop and handle the failing review comment first." \
  --interrupt
```

`send --interrupt` records the mailbox message, gracefully stops the runtime
child, and managed-resumes the same captured session with unread mail delivered
through the runtime-specific resume path. It refuses when no captured session
can be resumed unless `--force` is supplied, because a fresh fallback loses
conversation fidelity. Use it for urgent direction changes, not routine status
chatter.

## Agent-Side Reads

Bundled agents use the `inbox` skill when they need to poll directly:

```sh
"$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh check
"$AGENT_TEAM_ROOT"/skills/inbox/scripts/inbox.sh ack <message-id>
```

The script reads the instance mailbox and cursor on disk. A daemon is required
for sending through the skill, but not for checking already-written messages.

## Channels

Use channels when the message is not addressed to one instance:

```sh
agent-team channels
agent-team channel show standup
agent-team channel publish standup "review queue is clear"
agent-team channel rm standup --dry-run --commands
```

Channels are append-only coordination streams, not job ownership. Prefer
mailbox commands when an instance must act on the message.

## References

- [Status, Mailbox, and Channels](../runtime/status-mailbox-channels.md)
- [Jobs: Sending Messages](../workflows/jobs.md#sending-messages)
- [Pipelines and Teams](../workflows/pipelines-and-teams.md)
- [CLI Reference](../reference/cli.md)
