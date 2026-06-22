# Use Case: Multi-Team Repo

This scenario covers one repository with multiple product areas.

## Goal

Let several teams share one `.agent_team/` installation while preserving scoped operations and safe recovery.

## Topology

```toml
[instances.delivery-manager]
agent = "manager"
ephemeral = false

[instances.platform-manager]
agent = "manager"
ephemeral = false

[instances.delivery-worker]
agent = "worker"
ephemeral = true
replicas = 2

[instances.platform-worker]
agent = "worker"
ephemeral = true
replicas = 2

[pipelines.delivery_ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.delivery_ticket_to_pr.steps]]
id = "implement"
target = "delivery-worker"

[pipelines.platform_ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.platform_ticket_to_pr.steps]]
id = "implement"
target = "platform-worker"

[teams.delivery]
instances = ["delivery-manager", "delivery-worker"]
pipelines = ["delivery_ticket_to_pr"]

[teams.platform]
instances = ["platform-manager", "platform-worker"]
pipelines = ["platform_ticket_to_pr"]
```

## Operate One Team

```sh
agent-team team overview delivery
agent-team team jobs delivery --status running
agent-team team triage delivery
agent-team team tick delivery --dry-run
agent-team team repair delivery --dry-run --jobs
```

## Scoped Queue Recovery

```sh
agent-team team queue delivery --state dead
agent-team team queue retry delivery --all --dry-run
agent-team team queue quarantine delivery --restorable
```

These commands should not mutate platform-owned work.

## Scoped Diagnostics

```sh
agent-team team snapshot delivery --output delivery-diagnostics.json
agent-team team health delivery --jobs
agent-team team monitor delivery --jobs --schedules
```

## Why This Matters

Team scoping avoids two problems:

1. a broad repair command touching unrelated work
2. a broad diagnostic view hiding the real owner of stuck work

When adding team-scoped features, ensure tests prove unrelated jobs, queue files, schedules, and pipeline steps do not leak into the scoped output.
