# File Formats

`agent-team` uses small, inspectable file formats instead of a database.

## `.agent_team/config.toml`

Resolved template parameters.

```toml
[linear]
team_id = "00000000-0000-0000-0000-000000000000"
ticket_prefix = "SQU"

[health]
status_stale_after = "10m"
job_stale_after = "24h"
```

Read by skills and the CLI.

`[health]` is optional. `status_stale_after` controls when non-idle/non-done
instance `status.toml` files are marked stale in `ps`, `health`, `monitor`, and
related views. `job_stale_after` controls stale queued/running job triage. Set
either value to `"0"` to disable that stale check.

## `.agent_team/.template.lock`

Template provenance.

Stores source identity and content hash so `upgrade --check` can compare current state against a target ref and `upgrade --apply` can render a conservative three-way plan.

## `template.toml`

Template manifest.

```toml
[template]
name = "software-engineering-team"
version = "0.1.0"
description = "..."

[[parameter]]
key = "linear.team_id"
type = "string"
required = true
description = "Linear team UUID."
```

## `agent.md`

Agent definition.

```md
---
description: Coordinates implementation work.
---

Prompt body...
```

The loader extracts `description` and uses the body as the prompt.

## Agent `config.toml`

Skill assignment.

```toml
[skills]
extra = ["linear", "pull-request", "status"]
```

## `instances.toml`

Topology declaration.

```toml
[instances.manager]
agent = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "user_invocation"

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 3

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "pr"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
```

## Job TOML

Jobs live at `.agent_team/jobs/<job-id>.toml`.

Representative fields:

```toml
id = "squ-42"
ticket = "SQU-42"
target = "worker"
instance = "worker-squ-42"
status = "running"
branch = "worker-squ-42"
worktree = "/repo/.agent_team/worktrees/worker-squ-42"
pr = "https://github.com/acme/app/pull/42"
last_event = "dispatched"
last_status = "implementing API change"
created_at = "2026-06-22T10:00:00Z"
updated_at = "2026-06-22T10:15:00Z"

[[steps]]
id = "implement"
target = "worker"
status = "running"

[[steps]]
id = "review"
target = "manager"
status = "blocked"
after = ["implement"]
gate = "pr"
```

Supported step gates are:

- `manual`: waits for operator approval with `agent-team job step <job-id> <step-id> --status queued`.
- `pr`: waits until the job has PR metadata, usually from `agent-team job update <job-id> --pr <url>` or status reconciliation.

Exact encoding is owned by `internal/job`.

## Job Events JSONL

Job events are append-only JSONL rows.

They record:

- timestamp
- type
- status
- instance
- actor
- message
- structured data

Use `agent-team job events <job-id>` instead of reading raw rows in tooling.

## Runtime Metadata

Daemon metadata is TOML under `.agent_team/daemon/<instance>/`.

It records lifecycle state, PID, session id, workspace, and job ownership metadata.

Prefer `agent-team inspect` or `agent-team ps --json` for consumers.

## `status.toml`

Agent-reported work status.

```toml
[status]
phase = "blocked"
description = "Waiting for credentials"
since = "2026-06-22T10:00:00Z"

[work]
job = "squ-42"
ticket = "SQU-42"
branch = "worker-squ-42"
pr = "https://github.com/acme/app/pull/42"
```

This file is intentionally writable by skills and readable by operators.

## Queue JSON

Queue entries are JSON files.

```json
{
  "id": "q-123",
  "state": "pending",
  "event_type": "agent.dispatch",
  "instance": "worker",
  "instance_id": "worker-squ-42",
  "payload": {
    "job_id": "squ-42",
    "ticket": "SQU-42",
    "target": "worker"
  },
  "attempts": 1,
  "next_retry": "2026-06-22T10:05:00Z",
  "queued_at": "2026-06-22T10:00:00Z",
  "updated_at": "2026-06-22T10:01:00Z"
}
```

Active queue files live in `pending/` or `dead/`. Quarantined files preserve the same JSON shape under `quarantine/`.

## Intake Delivery JSONL

Delivery rows record normalized external events and replay state. Server-created rows may include `request_id`, such as GitHub's `X-GitHub-Delivery`, so intake can reject duplicate signed deliveries inside the configured replay window.

Use:

```sh
agent-team intake deliveries --json
agent-team intake summary --json
agent-team intake doctor --json
```

instead of parsing raw history where possible.
