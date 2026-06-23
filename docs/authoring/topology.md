# Topology

Topology is the repo's declaration of desired agent runtime shape.

It lives in `.agent_team/instances.toml` after init and is also shipped by templates.

## Why Topology Exists

Without topology, instances are ad-hoc: someone has to remember which agents should run and how to dispatch work.

Topology adds:

- declared persistent instances
- ephemeral worker definitions
- trigger routing
- schedule declarations
- pipeline declarations
- team ownership

## Instances

```toml
[instances.manager]
agent = "manager"
ephemeral = false
description = "Coordinates work."

[[instances.manager.triggers]]
event = "user_invocation"

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 3
description = "Implements assigned tickets."

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
```

Fields:

| Field | Meaning |
| --- | --- |
| `agent` | Agent directory name |
| `ephemeral` | Spawn per event and exit when complete |
| `description` | Human-readable purpose |
| `replicas` | Max concurrent ephemeral runs |
| `triggers` | Event matchers |

## Triggers

Triggers route events to instances.

```toml
[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
```

The match DSL is intentionally simple:

- exact match: `match.target = "worker"`
- OR list: `match.project = ["platform", "infra"]`
- AND across keys: multiple `match.<key>` entries

## Schedules

Schedules publish `schedule` events.

```toml
[schedules.nightly]
every = "24h"
run_on_start = false
payload.target = "manager"
payload.reason = "nightly maintenance"
```

Operators can inspect and fire schedules:

```sh
agent-team schedule ls
agent-team schedule due
agent-team schedule fire --dry-run --preview-triggers
agent-team tick --skip-drain --skip-advance
```

## Pipelines

Pipelines define job steps:

```toml
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "triage"
target = "ticket-manager"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
after = ["triage"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "manual"
```

Pipeline state is stored in jobs, not in a separate scheduler database. A step with `gate = "manual"` stays blocked after its dependencies finish until an operator approves it with `agent-team job step <job-id> <step-id> --status queued`; after that, normal `job advance`, `pipeline advance`, `team advance`, or `tick` dispatch can run it.

## Teams

Teams scope operations:

```toml
[teams.delivery]
description = "Software delivery team."
instances = ["manager", "ticket-manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
```

Team commands operate only on owned resources:

```sh
agent-team team overview delivery
agent-team team tick delivery --dry-run
agent-team team queue quarantine delivery --restorable
agent-team team snapshot delivery --output delivery.json
```

## Validation

Use:

```sh
agent-team topology summary
agent-team pipeline doctor --all
agent-team team doctor --all
agent-team doctor
```

These catch missing agents, invalid topology references, unrouteable pipeline steps, and team ownership problems.

## Code Areas

Topology behavior lives mostly in:

- `internal/topology/topology.go`
- `internal/topology/load.go`
- `internal/cli/topology.go`
- `internal/cli/pipeline.go`
- `internal/cli/team.go`
- `internal/daemon/event.go`
- `internal/daemon/scheduler.go`
