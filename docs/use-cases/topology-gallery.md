# Topology Gallery

These examples are complete `.agent_team/instances.toml` starting points. Copy
one, edit names and targets, then validate before running it. The same
topologies are tracked as copyable fixtures under
[`examples/topologies/`](https://github.com/agent-team-project/kensho/tree/main/examples/topologies)
and parsed in CI.

```sh
agent-team topology summary
agent-team pipeline doctor --all
agent-team team doctor --all
agent-team sync --dry-run --summary
```

## Single Delivery Team

Use this for one product team that turns incoming tickets into pull requests.
The manager and ticket manager are persistent. Workers are ephemeral and spawn
only when `agent.dispatch` targets `worker`.

Copyable file: [`examples/topologies/single-delivery-team.instances.toml`](https://github.com/agent-team-project/kensho/blob/main/examples/topologies/single-delivery-team.instances.toml)

```toml
[instances.manager]
agent = "manager"
ephemeral = false
description = "Coordinates active delivery work."

[[instances.manager.triggers]]
event = "user_invocation"

[[instances.manager.triggers]]
event = "agent.dispatch"
match.target = "manager"

[instances.ticket-manager]
agent = "ticket-manager"
ephemeral = false
description = "Normalizes incoming ticket events."

[[instances.ticket-manager.triggers]]
event = "ticket.created"

[[instances.ticket-manager.triggers]]
event = "agent.dispatch"
match.target = "ticket-manager"

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 3
description = "Implements assigned tickets in job-owned worktrees."

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

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

[schedules.nightly]
every = "24h"
run_on_start = false
payload.target = "manager"
payload.reason = "nightly delivery review"

[teams.delivery]
description = "Default software delivery team."
instances = ["manager", "ticket-manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
```

Common operations:

```sh
agent-team team overview delivery
agent-team pipeline run ticket_to_pr SQU-42 --dry-run --dispatch
agent-team team tick delivery --dry-run --preview-routes
agent-team team repair delivery --dry-run --jobs
```

## Product And Platform Teams

Use this when one repo contains two ownership areas. Each team gets its own
manager, worker pool, pipeline, and schedule. The target names keep queue,
pipeline, and team filters unambiguous.

Copyable file: [`examples/topologies/product-platform-teams.instances.toml`](https://github.com/agent-team-project/kensho/blob/main/examples/topologies/product-platform-teams.instances.toml)

```toml
[instances.product-manager]
agent = "manager"
ephemeral = false
description = "Coordinates product-facing work."

[[instances.product-manager.triggers]]
event = "agent.dispatch"
match.target = "product-manager"

[instances.product-worker]
agent = "worker"
ephemeral = true
replicas = 2
description = "Implements product tickets."

[[instances.product-worker.triggers]]
event = "agent.dispatch"
match.target = "product-worker"

[instances.platform-manager]
agent = "manager"
ephemeral = false
description = "Coordinates platform work."

[[instances.platform-manager.triggers]]
event = "agent.dispatch"
match.target = "platform-manager"

[instances.platform-worker]
agent = "worker"
ephemeral = true
replicas = 2
description = "Implements platform tickets."

[[instances.platform-worker.triggers]]
event = "agent.dispatch"
match.target = "platform-worker"

[pipelines.product_ticket_to_pr]
trigger.event = "ticket.created"
trigger.match.area = "product"

[[pipelines.product_ticket_to_pr.steps]]
id = "implement"
target = "product-worker"

[[pipelines.product_ticket_to_pr.steps]]
id = "review"
target = "product-manager"
after = ["implement"]
gate = "manual"

[pipelines.platform_ticket_to_pr]
trigger.event = "ticket.created"
trigger.match.area = "platform"

[[pipelines.platform_ticket_to_pr.steps]]
id = "implement"
target = "platform-worker"

[[pipelines.platform_ticket_to_pr.steps]]
id = "review"
target = "platform-manager"
after = ["implement"]
gate = "manual"

[schedules.product_weekly]
every = "168h"
payload.target = "product-manager"
payload.reason = "weekly product queue review"

[schedules.platform_daily]
every = "24h"
payload.target = "platform-manager"
payload.reason = "daily platform queue review"

[teams.product]
description = "Product delivery."
instances = ["product-manager", "product-worker"]
pipelines = ["product_ticket_to_pr"]
schedules = ["product_weekly"]

[teams.platform]
description = "Platform delivery."
instances = ["platform-manager", "platform-worker"]
pipelines = ["platform_ticket_to_pr"]
schedules = ["platform_daily"]
```

Common operations:

```sh
agent-team team overview product
agent-team team overview platform
agent-team intake linear --payload '{"type":"ticket.created","ticket":"SQU-55","area":"platform"}' --dry-run --preview-triggers
agent-team team queue platform --state dead
```

## Reliability Rotation

Use this when a small on-call group needs scheduled checks, incident intake,
and explicit worker handoff. The incident manager stays persistent; workers are
ephemeral so investigation logs stay tied to the job instance.

Copyable file: [`examples/topologies/reliability-rotation.instances.toml`](https://github.com/agent-team-project/kensho/blob/main/examples/topologies/reliability-rotation.instances.toml)

```toml
[instances.incident-manager]
agent = "manager"
ephemeral = false
description = "Owns incident triage and escalation."

[[instances.incident-manager.triggers]]
event = "agent.dispatch"
match.target = "incident-manager"

[[instances.incident-manager.triggers]]
event = "schedule"
match.target = "incident-manager"

[instances.incident-worker]
agent = "worker"
ephemeral = true
replicas = 2
description = "Investigates one incident or reliability task."

[[instances.incident-worker.triggers]]
event = "agent.dispatch"
match.target = "incident-worker"

[pipelines.incident_response]
trigger.event = "ticket.created"
trigger.match.kind = "incident"

[[pipelines.incident_response.steps]]
id = "triage"
target = "incident-manager"

[[pipelines.incident_response.steps]]
id = "investigate"
target = "incident-worker"
after = ["triage"]

[[pipelines.incident_response.steps]]
id = "review"
target = "incident-manager"
after = ["investigate"]
gate = "manual"

[schedules.on_call_handoff]
every = "12h"
run_on_start = true
payload.target = "incident-manager"
payload.reason = "on-call handoff"

[teams.reliability]
description = "On-call and incident response."
instances = ["incident-manager", "incident-worker"]
pipelines = ["incident_response"]
schedules = ["on_call_handoff"]
```

Common operations:

```sh
agent-team team monitor reliability --jobs --schedules
agent-team schedule due
agent-team team tick reliability --dry-run --preview-routes
agent-team team snapshot reliability --output reliability-snapshot.json
```

## Choosing A Pattern

| Pattern | Use when |
| --- | --- |
| Single delivery team | One repo has one main product ownership path |
| Product and platform teams | One repo has multiple independent ownership areas |
| Reliability rotation | Scheduled checks and incident intake need scoped recovery |

Keep target names explicit. Ambiguous targets make dry-run route previews harder
to trust and can cause team doctor warnings when a pipeline step can route to
more than one instance.
