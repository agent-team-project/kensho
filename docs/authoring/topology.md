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
locks = ["build"]

[locks.build]
slots = 1

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
| `brief` | Generate and inject a recoverable catch-up brief for persistent instances |
| `locks` | Named dispatch locks held by spawned ephemeral children |
| `replicas` | Max concurrent ephemeral runs |
| `env_allow` | Glob allowlist for inherited environment keys; unset is a no-op, and `AGENT_TEAM_*` is always kept |
| `triggers` | Event matchers |

## Locks

Locks serialize dispatches around shared resources such as build caches.

```toml
[locks.build]
slots = 1
scope = "machine"

[instances.worker]
locks = ["build"]

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
locks = ["build"]
```

`slots = 1` is a mutex; larger values act as a counting semaphore. When a
dispatch cannot acquire every required lock, the daemon writes it to the normal
pending queue with `reason = "lock_held"`. Inspect holders with
`agent-team locks` and held work with `agent-team queue ls --reason lock_held`.

Locks, channels, and schedules accept `scope = "machine" | "team" | "job"`.
Omitting scope preserves the historical machine-wide namespace. Team scope uses
the owning topology team for schedules/channels and the dispatch origin team for
locks.

Declared channels are optional unless a channel needs scoped storage:

```toml
[channels.supervisor]
scope = "team"

[teams.delivery]
channels = ["supervisor"]
```

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

Intake emits normalized event names. Linear ticket webhooks become `ticket.*`
events such as `ticket.created`, and GitHub pull-request webhooks become
`pr.*` events such as `pr.merged`. Triggers match those event names exactly;
declare separate triggers when one instance or pipeline should handle multiple
normalized events.

## Schedules

Schedules publish `schedule` events.

```toml
[schedules.nightly]
every = "24h"
scope = "team"
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

[pipelines.ticket_to_pr.infra_signatures]
fixture_reaped = 'Os \{ code: 2, kind: NotFound'
missing_deps = 'deps/[^ ]*: No such file'

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

Pipeline state is stored in jobs, not in a separate scheduler database. A step with `gate = "manual"` stays blocked after its dependencies finish until an operator approves it with `agent-team pipeline approve <pipeline>`, `agent-team team approve <team>`, or `agent-team job approve <job-id> --step <step-id>`; after that, normal `job advance`, `pipeline advance`, `team advance`, or `tick` dispatch can run it. Use `agent-team job reject <job-id> --step <step-id>` when the manual gate should fail instead.

Use `gate = "pr"` when a later step should wait for PR metadata. The step remains blocked with `waiting_for = ["pr"]` until the job has `pr` set, for example through `agent-team job update <job-id> --pr <url> --advance --dry-run` followed by the non-dry-run update, GitHub intake reconciliation with `agent-team intake github --payload-file github-webhook.json --reconcile-job --advance --dry-run` plus optional `--commands`, or status-file reconciliation.

Use `agent-team job step <job-id> <step-id> --skip` when a stage is intentionally bypassed. The job stores that step as `status = "done"` plus `skipped = true`, allowing dependent steps to continue while preserving the operator decision.

Use `optional = true` when a stage is useful but should not block the workflow if it fails. Optional failures still appear in `job explain`, `pipeline explain`, and retry views, but downstream `after` dependencies are treated as satisfied.

Use `[pipelines.<name>.infra_signatures]` to classify failed gate signatures
reported with `agent-team job gate set`. These regexes only classify explicit
failed results as `infra`; unmatched failed gates are `content`, and pass/fail
is always decided by the reporting agent. Anchor signatures to observed error
shapes, not broad keywords: `fixture_reaped = 'NotFound'` is too broad, while
`fixture_reaped = 'Os \{ code: 2, kind: NotFound'` and
`missing_deps = 'deps/[^ ]*: No such file'` point at concrete failure shapes.
Use `agent-team signatures test <pipeline> --against <log-file>` to dry-run all
configured signatures and inspect the matching excerpts before classifying real
failed gates.

## Teams

Teams scope operations:

```toml
[teams.delivery]
description = "Software delivery team."
instances = ["manager", "ticket-manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
channels = ["supervisor"]
```

Team commands operate only on owned resources:

```sh
agent-team team overview delivery
agent-team team tick delivery --dry-run
agent-team team queue quarantine delivery --restorable
agent-team team snapshot delivery --output delivery.json
```

## Authority

Authority allowlists live in topology. `enforcement = "audit"` records
violations without blocking; `enforcement = "enforce"` records the same
violation and denies disallowed audited mutations.

```toml
[authority]
enforcement = "audit"

[authority.instances.manager]
allow = ["*"]

[authority.agents.worker]
allow = ["inbox.send", "channel.*", "job.gate.*:own"]

[authority.agents.manager]
allow = ["*"]
```

Allow entries are exact verbs or prefix wildcards such as `queue.*`. Job
verbs can add `:own`, such as `job.gate.*:own`, to match only when the target
job id equals the caller's origin job. Unqualified entries match any target
job. Instance, agent, and team rules are additive.

Under `enforcement = "enforce"`, launched runtimes get an `agent-team` shim
that resolves invocations through the live Cobra command tree and denies
unknown or ungranted verbs before they reach the real CLI. The allowlist is
baked into that shim at launch time. If an enforced agent needs daemon resource
reads, include `read` in its allowlist; URI identity does not bypass verb
authority.

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
