# Architecture

`agent-team` is deliberately file-backed and split into a few clear layers.

## High-Level Flow

```text
Template source
  -> agent-team init
  -> .agent_team/
  -> topology, agents, skills, config
  -> agent-teamd daemon
  -> instances, jobs, queues, schedules, intake
  -> operator commands and diagnostics
```

## Control Plane

The user-facing control plane is the `agent-team` CLI.

It handles:

- initializing templates
- loading agents and skills
- launching direct runtime sessions
- starting and controlling the daemon
- inspecting instances and logs
- creating and dispatching jobs
- maintaining queues
- running pipeline and team commands
- receiving and replaying intake events
- producing diagnostics and repair plans

The CLI is implemented with Cobra under `internal/cli`.

## Runtime Plane

The runtime plane is `agent-teamd`, a per-repo daemon.

It owns:

- child process lifecycle
- runtime metadata
- daemon socket and HTTP API
- persisted queues
- mailbox delivery
- logs
- lifecycle events
- schedule firing
- topology event resolution

The daemon is intentionally local and repo-scoped. Its socket and state live under `.agent_team/daemon/`.

## State Plane

The state plane is the repo filesystem.

Important paths:

| Path | Purpose |
| --- | --- |
| `.agent_team/config.toml` | Resolved template parameters |
| `.agent_team/instances.toml` | Declared topology |
| `.agent_team/agents/` | Agent definitions |
| `.agent_team/skills/` | Shared skills |
| `.agent_team/jobs/` | Durable job TOML files |
| `.agent_team/jobs/events/` | Job event JSONL logs |
| `.agent_team/state/<instance>/` | Per-instance state |
| `.agent_team/state/<instance>/status.toml` | Agent-reported work status |
| `.agent_team/daemon/` | Daemon metadata and logs |
| `.agent_team/daemon/queue/pending/` | Pending queue entries |
| `.agent_team/daemon/queue/dead/` | Dead-letter queue entries |
| `.agent_team/daemon/queue/quarantine/` | Preserved queue files moved out of active paths |
| `.agent_team/intake/` | Intake delivery history |

The system avoids a database. Operations are implemented with structured TOML, JSON, JSONL, and filesystem paths.

## Runtime Launch

Direct launch still exists:

```sh
agent-team run manager
```

This mode loads the team, prepares runtime discovery directories, writes a kickoff prompt, creates state directories, and execs the selected runtime.

Daemon launch is the durable path:

```sh
agent-team run manager --detach
agent-team dispatch worker SQU-42 "Implement the ticket"
agent-team job dispatch squ-42
```

The daemon starts child processes and persists metadata so later commands can inspect, restart, attach, and recover.

## Event Resolution

Topology events are normalized into event type plus payload.

Examples:

- `agent.dispatch`
- `ticket.created`
- `pr.opened`
- `schedule`

The daemon resolves events against `instances.toml` triggers. Matching persistent instances receive mailbox messages. Matching ephemeral instances spawn a new worker instance or queue when capacity is exhausted.

## Job-Centric Work

Jobs sit above runtime dispatch.

A job owns:

- the ticket identity
- the target agent
- the current instance
- worktree and branch metadata
- PR URL
- queue entries
- pipeline step state
- event history

This makes diagnostics job-centric:

```sh
agent-team job show squ-42
agent-team job triage
agent-team job queue squ-42 --summary
agent-team job queue quarantine squ-42
```

## Diagnostics Philosophy

Diagnostics are designed to be:

- read-only by default
- scoped when possible
- explicit about next actions
- safe for scripts through JSON output
- useful when the daemon is down

Commands such as `overview`, `next`, `health`, `monitor`, `doctor`, `snapshot`, and `repair --dry-run` are meant to explain current state before mutating anything.
