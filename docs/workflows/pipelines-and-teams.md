# Pipelines and Teams

Pipelines and teams are the first layer above individual jobs.

Pipelines define multi-step work. Teams define ownership and scoping.

## Pipelines

Example:

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
```

The current engine supports:

- step ids
- target instances/agents
- simple `after` dependencies
- job-file step state
- ready-step inspection
- dry-run route previews
- team-scoped advancement

It intentionally does not try to be a full DAG workflow engine yet.

## Pipeline Commands

```sh
agent-team pipeline ls
agent-team pipeline show ticket_to_pr
agent-team pipeline graph ticket_to_pr --format mermaid --routes
agent-team pipeline doctor --all
agent-team pipeline run ticket_to_pr SQU-42 --dry-run --dispatch
agent-team pipeline status
agent-team pipeline ready
agent-team pipeline advance ticket_to_pr --dry-run --preview-routes
```

Job-level equivalents:

```sh
agent-team job next squ-42
agent-team job ready
agent-team job advance squ-42 --dry-run --preview-routes
agent-team job step squ-42 implement --advance --dry-run
agent-team job step squ-42 review --skip --message "review folded into implementation"
```

## Step State

Pipeline step state is stored inside the job file.

Common states:

- `queued`
- `running`
- `blocked`
- `failed`
- `done`
- `none`

`job triage`, `pipeline status`, `pipeline ready`, and `team triage` all read the same job state.
When an operator intentionally bypasses a stage, `agent-team job step <job-id> <step-id> --skip` records that step as `done` with `skipped = true`, so dependency checks can continue while `job show` still reports the bypass.

Supported gates:

- `gate = "manual"`: wait for operator approval with `agent-team job step <job-id> <step-id> --status queued`.
- `gate = "pr"`: wait until the job has PR metadata, then advance normally.

When a ready step targets a persistent instance that is not currently running,
advancement writes the mailbox message and leaves the step `queued` with the
persistent instance name. Once that instance is started, it can drain its
mailbox and report progress. Steps are marked `running` only when dispatch
starts an ephemeral worker or messages a live persistent instance.

## Teams

Teams group resources:

```toml
[teams.delivery]
instances = ["manager", "ticket-manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
```

Team commands provide scoped variants:

```sh
agent-team team overview delivery
agent-team team jobs delivery
agent-team team triage delivery
agent-team team ready delivery
agent-team team advance delivery --dry-run --preview-routes
agent-team team tick delivery --dry-run
agent-team team repair delivery --dry-run --jobs
agent-team team snapshot delivery --output delivery.json
```

## Why Teams Matter

In a repo with several product areas, broad recovery commands can be too coarse.

Teams let operators say:

- only delivery-owned jobs
- only delivery-owned queues
- only delivery-owned schedules
- only delivery-owned pipelines
- only delivery-owned instance state

This makes diagnostics safer and less noisy.

## Team Health

```sh
agent-team team health delivery --jobs
agent-team team status delivery
agent-team team monitor delivery --jobs --schedules
```

Team health includes:

- daemon readiness
- team-owned queue dead letters
- team-owned queue quarantine
- job attention
- pipeline failures
- schedule state
- team topology warnings

## Development Guidelines

When adding pipeline or team features:

1. Preserve job-file ownership of step state.
2. Add dry-run behavior before mutating commands.
3. Add team-scoped tests whenever global behavior is changed.
4. Ensure global health/overview and team health/overview stay consistent.
5. Avoid adding a complex workflow engine until the simple step model proves insufficient.

## Code Areas

- `internal/topology/topology.go`
- `internal/cli/pipeline.go`
- `internal/cli/job.go`
- `internal/cli/team.go`
- `internal/cli/tick.go`
- `internal/cli/repair.go`
