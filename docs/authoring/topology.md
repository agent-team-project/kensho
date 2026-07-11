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

## Runtime And Model Policy

Use one shared policy when most seats should run on the same runtime/model:

```toml
[model_policy]
runtime = "codex"
model = "gpt-5.6-sol"
effort = "xhigh"
```

Every instance inherits omitted `runtime`, `model`, and `effort` values from
this table. A pipeline step inherits omitted values from its target instance,
so an explicit instance exception also survives pipeline dispatch. Explicit
instance fields override the shared policy, and explicit step fields override
the resolved target instance. Model and effort are runtime-family-specific: a
runtime-only override that changes family clears inherited model and effort
instead of passing Codex selectors to Claude or Claude selectors to Codex. Set
model or effort alongside the new runtime when that selector should remain
explicitly authoritative.

The bundled full topology uses this policy for every non-Fable seat. Its only
exceptions are `advisor`, `harness-reviewer`, and `org-review`, each declared
with `runtime = "claude"`, `model = "claude-fable-5"`, and `effort = "max"`.
This keeps selection repo-owned and reviewable instead of relying on a
developer's Claude or Codex user configuration.

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
| `runtime` | Optional runtime kind override: `claude`, `codex`, or `docker` |
| `runtime_bin` | Optional runtime binary override |
| `model` | Optional model id; passed as `--model` for Claude and Codex |
| `effort` | Optional reasoning effort; passed as `--effort` for Claude and `model_reasoning_effort` for Codex |
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
model = "gpt-5.6-sol"
after = ["triage"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "manual"
```

Pipeline state is stored in jobs, not in a separate scheduler database. A step with `gate = "manual"` stays blocked after its dependencies finish until an operator approves it with `agent-team pipeline approve <pipeline>`, `agent-team team approve <team>`, or `agent-team job approve <job-id> --step <step-id>`; after that, normal `job advance`, `pipeline advance`, `team advance`, or `tick` dispatch can run it. Use `agent-team job reject <job-id> --step <step-id>` when the manual gate should fail instead.

When daemon-managed jobs reach manager-relevant completion points, the daemon
publishes `job.step_completed`, `job.completed`, and `deliverable.ready` events.
A persistent manager can subscribe to those events to wake after workers,
verifiers, or reviewers finish, inspect blocked manual gates, and continue the
unattended review/merge loop. In a multi-manager topology, route ownership by
pipeline as well as direct target: completion payloads may retain the step role
in `target`, and a broad manager target can wake the wrong portfolio owner.

```toml
[[instances.research-manager.triggers]]
event = "job.completed"
match.pipeline = "research_study"

[[instances.research-manager.triggers]]
event = "deliverable.ready"
match.pipeline = "research_study"
```

Verifier steps can declare deterministic gates directly in their instructions.
The bundled verifier executes a fenced `agent-team-verify-gates` block before
considering repository defaults, which is important for report pipelines that
must not inherit unrelated build/test gates:

````toml
instructions = """
```agent-team-verify-gates
report-exists :: p="${AGENT_TEAM_ROOT%/.agent_team}/reports/study.md"; test -s "$p"
report-digest :: p="${AGENT_TEAM_ROOT%/.agent_team}/reports/study.md"; if command -v sha256sum >/dev/null 2>&1; then sha256sum "$p"; else shasum -a 256 "$p"; fi
```
"""
````

Use `gate = "pr"` when a later step should wait for PR metadata. The step remains blocked with `waiting_for = ["pr"]` until the job has `pr` set, for example through `agent-team job update <job-id> --pr <url> --advance --dry-run` followed by the non-dry-run update, GitHub intake reconciliation with `agent-team intake github --payload-file github-webhook.json --reconcile-job --advance --dry-run` plus optional `--commands`, or status-file reconciliation.

Use `agent-team job step <job-id> <step-id> --skip` when a stage is intentionally bypassed. The job stores that step as `status = "done"` plus `skipped = true`, allowing dependent steps to continue while preserving the operator decision.

Use `optional = true` when a stage is useful but should not block the workflow if it fails. Optional failures still appear in `job explain`, `pipeline explain`, and retry views, but downstream `after` dependencies are treated as satisfied.

Pipeline steps may set `runtime`, `runtime_bin`, `model`, and `effort` to override the spawned runtime for that step. `model` becomes `--model <id>` for Claude and Codex. `effort` becomes `--effort <level>` for Claude and `-c model_reasoning_effort="<level>"` for Codex. Omitted or empty fields inherit the resolved target instance, including `[model_policy]` defaults, unless an explicit `runtime` changes family; that clears omitted model and effort fields so selectors cannot cross runtime families.

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

## Budgets

Team budgets gate dispatch admission for spend, in-flight job count, and
adaptive concurrency load:

```toml
[budgets.delivery]
tokens_per_day = 200_000_000
jobs_in_flight = 4
allocation = "oversubscribe"
load_weight = 1.0
```

`load_weight` defaults to `1.0`. Values greater than `1.0` make each dispatch
consume more adaptive concurrency headroom; for example `load_weight = 2.5`
means two running dispatches consume five governor units. Use it for teams whose
runtime or build system is materially heavier than the daemon-wide
`[concurrency].load_per_dispatch` baseline.

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

Allow entries are exact verbs or prefix wildcards such as `queue.*`. Job verbs
can add one of two scope qualifiers:

- `:own`, such as `job.gate.*:own`, matches when the target job id equals the
  caller's origin job. Use it for workers, verifiers, and reviewers dispatched
  as part of that job.
- `:team`, such as `job.bounce:team`, matches when the target job's recorded
  origin team equals the caller instance's topology-derived team. Use it for a
  persistent manager that operates its team's pipeline jobs but is not itself
  dispatched as part of those jobs.

Unqualified entries match any target job. Instance, agent, and team rules are
additive.

Under `enforcement = "enforce"`, launched runtimes get an `agent-team` shim
that resolves invocations through the live Cobra command tree and denies
unknown or ungranted verbs before they reach the real CLI. The allowlist is
baked into that shim at launch time. If an enforced agent needs daemon resource
reads, include `read` in its allowlist; URI identity does not bypass verb
authority.

## Validation

Use:

```sh
agent-team topology validate
agent-team topology summary
agent-team pipeline doctor --all
agent-team team doctor --all
agent-team doctor
```

`topology validate` is also run by this repository's TOML CI gate. In addition
to schema and reference errors, it independently resolves every manual-decision
and terminal merge/reap route and rejects missing or ambiguous completion-event
owners in both authority modes. With `enforcement = "enforce"`, it also rejects
an owner that cannot satisfiably perform its route's required job mutations
after instance, agent, team, and scope rules are composed. Audit mode keeps
those grant denials observable and non-blocking, but does not make an ambiguous
or unsupported topology structurally valid.

The remaining commands catch missing agents, unrouteable pipeline steps, and
runtime team ownership problems.

## Code Areas

Topology behavior lives mostly in:

- `internal/topology/topology.go`
- `internal/topology/load.go`
- `internal/cli/topology.go`
- `internal/cli/pipeline.go`
- `internal/cli/team.go`
- `internal/daemon/event.go`
- `internal/daemon/scheduler.go`
