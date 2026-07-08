# Composable Typed Deployment Units

Status: design sketch for GH-223. This document is design only and does not
prescribe implementation in this change.

Related design notes:

- [templates.md](./templates.md): parameterized, versioned directory trees as
  reusable images.
- [dynamic-teams.md](./dynamic-teams.md): child deployments, charters,
  attenuated capabilities, budget grants, and reconcile-backed spawn/reap.
- [topology.md](./topology.md): instances, teams, channels, pipelines,
  schedules, budgets, authority policy, and event routing.
- [distributed-resources.md](./distributed-resources.md): canonical `agt://`
  resource URIs, deployment identity, materialization paths, and transport.
- [resource-constraints.md](./resource-constraints.md): budget tree invariants,
  soft/hard allowances, and optimization under constraints.
- [reconcile.md](./reconcile.md): desired state, observed state, diff/action
  planning, apply semantics, and dynamic child convergence.
- [security-model.md](./security-model.md): origin identity, API authority,
  capability attenuation, sandboxing, and brokered secrets.

## Summary

Today the framework has two durable composition levels:

```text
agent -> team/template -> fleet
```

That skips the level operators actually want to reuse: a goal-shaped bundle of
roles, workflow, budget, gates, and lifecycle policy that can be deployed as
one thing. A feature needs a worker, reviewer, PM/ticket-manager behavior,
handoff channels, PR gates, budget, and an end condition. An incident needs a
different bundle. A migration, research spike, or ops sweep each has its own
shape.

A composable typed deployment unit is that missing middle:

```text
agent -> unit -> deployment composition -> fleet
```

A unit is a typed, parameterized deployment bundle with a uniform connection
contract. The internals can vary by unit type, but the connector is standard:
events in/out, budget draw, capability need, deadline, and reap signal. Units
compose because those contract studs align, not because each pair of units
knows bespoke glue.

The design intentionally reuses existing primitives:

- Templates-as-images define how unit types are authored and versioned.
- Dynamic teams provide the spawn engine for runtime-created unit instances.
- Topology channels, pipelines, and triggers provide the composition fabric.
- The resource model supplies URI identity, budget edges, authority grants,
  lifecycle state, and cross-deployment reads.
- Reconcile actuates desired unit graphs and reaps completed instances.

In this model, "deploy a feature-delivery unit" replaces "manually compose a
worker, reviewer, PM behavior, gates, and meta-analysis every time." A
deployment becomes a composition of units under a budget and authority tree.

## Goals

- Define the unit as the reusable composition level between individual agents
  and whole repo/team templates.
- Make unit composition contract-first: units snap together through the same
  declared studs, independent of their internals.
- Reuse topology primitives as the fabric for cross-unit communication:
  channels, pipelines, triggers, gates, schedules, and budgets.
- Reuse dynamic-team charters for runtime unit instantiation, with parent
  budget and capability attenuation.
- Let multiple unit instances run in parallel under one governance deployment
  without losing observability or accounting.
- Give meta-learning and org-review a concrete output artifact: discovered
  unit types and recommended compositions.
- Preserve the crash-only controller model: unit spawn, health, reports, and
  reap are desired resources reconciled into observed state.

## Non-Goals

- Implement the unit loader, parser, or daemon APIs in this design note.
- Replace ordinary agents, teams, or templates.
- Let agents author arbitrary unreviewed prompts, tools, secrets, budgets, or
  capabilities.
- Add a second event bus or dispatch system.
- Treat unit boundaries as filesystem boundaries. URI identity and daemon
  resources are the contract; local paths remain materializations.
- Solve global catalog governance, marketplace distribution, or cross-org trust
  in the first version.

## Terms

**Unit type**: a versioned, parameterized spec for a deployable bundle. Example:
`feature-delivery`, `research-spike`, `migration`, `ops-watch`.

**Unit instance**: one realized unit type inside a deployment composition. It
has concrete parameters, budget allocation, capability grant, deadline,
resource URIs, and lifecycle status.

**Deployment composition**: a desired graph of unit instances and connections
between their contract studs.

**Contract stud**: one field in the unit's standard connection contract. The
initial studs are events in/out, budget draw, capability need, deadline, and
reap signal. A unit type may expose richer metadata, but these studs must not
be bespoke.

**Composition fabric**: the shared bus that carries cross-unit signal:
channels, mailboxes, pipelines, topology triggers, gates, and event resolver
rules.

**Deadline**: a wall-clock contract by which the unit must emit a terminal
signal or request extension. It is not the same as time budget: time budget is
spend/allowance; deadline is an externally visible promise and scheduling
constraint.

**Reap signal**: the terminal output that tells the parent deployment that the
unit can be drained, archived, and have live capability revoked. Reap removes
live authority and materializations; it does not erase evidence.

**Unit report**: a resource-shaped result emitted by a unit. It references
evidence URIs for deliverables, gates, logs, PRs, tickets, or child reports.

## The Standard Interface

The design north star is the Lego stud-and-tube connector: bricks compose
because they all share one interface. The unit equivalent is the resource-model
spine. A unit does not become composable because it has clever internals. It
becomes composable because it exposes the same contract every other unit
understands.

The standard interface is:

| Stud | Meaning | Resource-model backing |
| --- | --- | --- |
| Events in/out | Normalized events the unit consumes and emits, including schemas and channel or pipeline handoff points. | Topology triggers, channels, mailboxes, pipelines, event resolver. |
| Budget draw | Token, time, job, slot, or external quota allowance the unit needs and the parent must grant. | Budget allocation tree, soft/hard notices, usage rollup. |
| Capability need | Daemon/API verbs and resource scopes required for the unit to do its work. | Origin identity, capability attenuation, authority policy. |
| Deadline | Absolute or relative wall-clock completion contract and escalation behavior. | Charter deadline, schedule/priority policy, timeout/reap conditions. |
| Reap signal | Terminal event/report that says live capability can be revoked and materializations can be cleaned up. | Lifecycle policy, reconcile, tombstone resources, retention. |

Two units compose if their studs align under policy. A unit that needs a
bespoke RPC, private file path, ambient credential, or out-of-band instruction
is a brick that does not fit the system.

## Layering

The intended layering is:

```text
agent
  -> unit type
    -> unit instance
      -> deployment composition
        -> fleet
```

### Agent

An authored role prompt and skill set. Agents remain the smallest reusable
behavioral primitive.

### Unit Type

A unit type bundles agents, topology, pipeline steps, gates, budget defaults,
authority needs, lifecycle policy, deadline policy, and output expectations.
It is a template-like image at sub-team granularity.

### Unit Instance

A concrete instantiation of a unit type. It is admitted under a parent
deployment, receives an attenuated grant, and appears as a resource with URI
identity and status.

### Deployment Composition

A declared graph of unit instances and connections. The deployment says which
studs connect, not how to hand-write glue. The framework materializes the
channels, pipeline edges, trigger routes, budget edges, and capability grants.

### Fleet

Multiple deployments and compositions, potentially spread across machines or
containers, coordinated through the same resource and transport model.

## Unit Spec

A unit spec is the authored image for one unit type. The exact file location
and schema can change, but the shape should stay close to templates and
topology:

```toml
[unit]
name = "feature-delivery"
version = "0.1.0"
description = "Take a GitHub issue from ready work item to reviewed PR."
relationship = "ephemeral_team"
max_ttl = "8h"

[[parameter]]
key = "work_item.url"
type = "string"
required = true

[[parameter]]
key = "delivery.deadline"
type = "duration"
default = "4h"
description = "Wall-clock target for terminal report or extension request."

[[roles]]
name = "pm"
agent = "ticket-manager"
persistent = false

[[roles]]
name = "implement"
agent = "worker"
workspace = "worktree"

[[roles]]
name = "review"
agent = "reviewer"
workspace = "repo"

[[contract.inputs]]
name = "work_ready"
event = "ticket.ready"
schema = "agent-team.work-item.v1"
channel = "#delivery"

[[contract.outputs]]
name = "pr_ready"
event = "pr.opened"
schema = "agent-team.pull-request.v1"
channel = "#review-requests"
evidence = ["pr_url", "branch", "gate.tests"]

[contract.budget]
tokens = "40M"
time = "90m"
allocation = "reserve"

[contract.capability]
need = [
  "ticket.read",
  "ticket.comment",
  "git.push:own",
  "pr.create:own",
  "pr.comment:own",
  "job.gate.set:own",
  "read.*"
]

[contract.deadline]
required = true
param = "delivery.deadline"
on_miss = "emit_deadline_missed"
extension_event = "unit.extension_requested"

[contract.reap]
signal = "unit.completed"
success_events = ["pr.ready_for_review"]
failure_events = ["unit.failed", "unit.deadline_missed", "budget_exceeded_hard"]
retain = "report_and_tombstone"
```

The spec is not only a spawn recipe. It is also a type contract. Admission can
validate it before anything runs, composition can connect it to other units,
and reconcile can observe whether the instance converged.

## Unit Resources

The resource model should make units addressable without relying on paths.
Initial resource projections can be read-only before mutation APIs exist.

Suggested URI kinds:

| Kind | Example | Purpose |
| --- | --- | --- |
| `unit-type` | `agt://dep/unit-type/feature-delivery@sha256:...` | Versioned authored spec. |
| `unit` | `agt://dep/unit/feature-delivery-01j...` | Concrete unit instance in a composition. |
| `unit-contract` | `agt://dep/unit/feature-delivery-01j...#contract` | Effective contract after parameter resolution and attenuation. |
| `unit-connection` | `agt://dep/unit-connection/delivery-to-review` | Declared connection between studs. |
| `unit-report` | `agt://dep/unit-report/feature-delivery-01j...` | Outcome and evidence summary. |
| `unit-tombstone` | `agt://dep/unit-tombstone/feature-delivery-01j...` | Reaped instance record. |

The unit instance should carry both spec and status:

```json
{
  "kind": "unit",
  "uri": "agt://dep/unit/feature-delivery-01j...",
  "type_uri": "agt://dep/unit-type/feature-delivery@sha256:...",
  "parent_deployment_uri": "agt://dep/deployment/dep",
  "charter_uri": "agt://dep/charter/charter_01j...",
  "spec": {
    "parameters": {
      "work_item.url": "https://github.com/acme/widgets/issues/42",
      "delivery.deadline": "4h"
    },
    "connections": ["agt://dep/unit-connection/delivery-to-review"],
    "budget_allocation_uri": "agt://dep/allocation/alloc_01j...",
    "capability_uri": "agt://dep/capability/cap_01j...",
    "deadline": "2026-07-08T18:00:00Z",
    "reap_policy": "on_unit_completed"
  },
  "status": {
    "phase": "running",
    "observed_generation": 3,
    "last_event": "pr.opened",
    "conditions": [
      {
        "type": "DeadlineSatisfiable",
        "status": "True"
      }
    ]
  }
}
```

Spec is authored or admitted desired state. Status is daemon-owned
observation.

## Composition Fabric

Kensho already has the fabric needed to compose units. Today topology wires
instances inside a team. The unit layer lifts the same primitives one level up
to wire units inside a deployment.

| Primitive | Instance-level use today | Unit-level use |
| --- | --- | --- |
| Channels | Broadcast messages between instances or teams. | Carry normalized events across unit boundaries. |
| Pipelines | Sequence work steps, gates, and retry policy. | Span unit outputs and downstream unit inputs. |
| Triggers | Match normalized events to instances or pipelines. | Route emitted unit events to consuming units. |
| Gates | Decide whether a step can advance. | Validate unit reports and evidence before downstream composition continues. |
| Budgets | Govern team/job spend. | Allocate parent budget to unit instances and roll usage up. |
| Authority policy | Limit instance verbs. | Attenuate unit capabilities from the parent and template caps. |
| Schedules/deadlines | Fire due work and detect stale work. | Prioritize or reap units against declared deadlines. |

Composition should be declarative:

```toml
[units.delivery]
type = "feature-delivery"
version = "0.1.0"

[units.delivery.parameters]
work_item.url = "https://github.com/acme/widgets/issues/42"
delivery.deadline = "4h"

[units.review]
type = "review-gate"
version = "0.1.0"

[[connections]]
name = "delivery-pr-to-review"
from = "units.delivery.outputs.pr_ready"
to = "units.review.inputs.review_requested"
channel = "#review-requests"
pipeline = "ticket_to_pr"
```

The framework can then instantiate:

- a channel subscription for `#review-requests`
- a pipeline edge from delivery output to review input
- an event resolver route for `pr.opened`
- a gate requiring the delivery report evidence
- a budget child allocation for each unit
- attenuated capabilities for each unit boundary
- deadline monitors and extension routes

No per-composition glue should be required.

## Deadline Contract

Deadlines are first-class contract studs because composition needs to reason
about time before work starts. A parent cannot safely compose five units if
each unit hides its completion promise in a prompt.

A deadline differs from time budget:

- **Time budget** is how much runtime a unit may consume.
- **Deadline** is when the unit must emit a terminal report, extension request,
  or deadline-missed event.

The deadline stud should support:

- absolute deadlines from a parent job or incident
- relative deadlines declared by a unit type, such as `4h`
- attenuation, where a child deadline cannot exceed the parent deadline
- admission checks for impossible deadlines
- priority hints for queueing and scheduling
- warning events before miss
- an extension request event with budget/capability implications
- a terminal miss event that can trigger reap or downstream fallback

Deadline handling belongs in the same resource path as budgets and lifecycle.
It should be visible to the unit instance, the parent, reconcile, and operator
surfaces.

## Relationship To Existing Designs

### Templates As Images

Templates define versioned, parameterized trees. Unit types use the same idea
at sub-team granularity. The difference is composability: a repo template
creates a whole `.agent_team/` tree, while a unit type is meant to be one
typed bundle inside a larger deployment composition.

Unit specs should therefore inherit template disciplines:

- explicit parameters
- version and content hash
- rendered config from defaults plus caller overrides
- reproducible instantiation
- conservative upgrade semantics

### Dynamic Teams

Dynamic teams provide the spawn and lifecycle engine. A `team.spawn` charter
creates a child deployment under parent budget and capability constraints. A
unit is the typed thing the charter names.

The later `team.spawn` shape should be able to reference a unit type:

```json
{
  "verb": "team.spawn",
  "unit_type": "feature-delivery",
  "unit_version": "0.1.0",
  "parameters": {
    "work_item.url": "https://github.com/acme/widgets/issues/42",
    "delivery.deadline": "4h"
  },
  "connect": {
    "pr_ready": "agt://dep/unit/review-gate-01j...#inputs/review_requested"
  }
}
```

The dynamic-team admission path still applies: origin resolution, template
authorization, budget grant, capability attenuation, provenance envelope,
manifest render, and reconcile enqueue.

### Topology

Topology is the composition vocabulary. Unit composition should not invent a
new bus. Instead, the schema becomes recursive:

```text
instances in a team today
units in a deployment tomorrow
same channels/pipelines/triggers vocabulary
```

The important change is boundary. A connection that today routes from one
instance to another can route from one unit output to another unit input, with
the framework materializing the instance-level wiring inside each unit.

### Resource Constraints

Units are budget-bearing resources. A parent grants a unit allocation, the unit
may grant child allocations to jobs or sub-units, and usage rolls up to both
the unit and the parent charter.

The invariant remains a tree:

```text
operator budget
  -> parent deployment/team budget
    -> composition budget
      -> unit allocation
        -> child job or sub-unit allowance
```

This prevents runaway spawn: a unit can create sub-units only by spending from
its own grant.

### Security And Authority

Units make authority attenuation more important, not less. A unit type declares
what it needs; the parent and daemon decide what it gets. The granted set is
the intersection of:

- caller capability
- unit type maximums
- deployment policy
- budget/deadline/lifecycle constraints
- placement and sandbox constraints

Unit composition must not pass ambient provider tokens, filesystem paths, or
unscoped CLI authority across boundaries. Cross-unit work should happen through
daemon verbs and resource URIs.

### Reconcile

A unit composition is desired state. Reconcile observes unit resources, plans
actions, starts missing children, routes events, applies backoff, records
conditions, and reaps completed unit instances.

Losing one event must not permanently break a composition. The next reconcile
pass should be able to compare desired connections to observed reports,
channels, gates, and child health, then explain whether each unit is ready,
running, blocked, degraded, deadline-missed, or reaped.

## Feature Delivery Unit

The first bundled unit type should be intentionally narrow:

```text
feature-delivery =
  roles:
    ticket-manager/PM behavior
    worker
    reviewer
    meta-analyzer hook
  fabric:
    work-ready input
    implementation pipeline
    review gate
    PR-ready output
    final unit report
  contract:
    events in/out
    budget draw
    capability need
    deadline
    reap signal
```

Inputs:

- `ticket.ready` or `agent.dispatch` with a work item URI
- optional `pr.commented` or review-feedback events when a prior attempt
  bounces

Outputs:

- `branch.pushed`
- `pr.opened`
- `gate.tests.pass` or `gate.tests.fail`
- `review.requested`
- `unit.completed`, `unit.failed`, or `unit.extension_requested`

Budget draw:

- default implementation token/time allowance
- review allowance
- optional meta-analysis allowance after merge or bounce loops

Capability need:

- read work item
- write branch
- create/update PR
- comment on work item and PR
- set gates on own job
- read resources needed for evidence

Deadline:

- supplied by parent job or defaulted by unit type
- surfaced to each role in the unit
- miss emits a terminal or extension event, according to policy

Reap:

- on merge, cancellation, hard budget failure, deadline miss, or explicit parent
  cancellation
- archive reports and tombstone before removing live workspaces/capabilities

This unit becomes the reusable answer to ordinary "take this issue to a
reviewable PR" work. Other unit types should prove they need different
contracts before they become catalog entries.

## Lifecycle

### 1. Author

A human, privileged agent, or reviewed proposal creates a unit type. The spec
declares parameters, roles, internal topology, contract studs, budget defaults,
capability needs, deadline policy, gates, and reap behavior.

### 2. Validate

The framework validates the unit type before it can be cataloged:

- parameter declarations are complete
- event names and schemas are normalized
- inputs and outputs have stable names
- budget and deadline fields are parseable
- capability needs are declared, not ambient
- roles refer to known agents/templates
- lifecycle and reap policy are explicit

### 3. Compose

A deployment declares unit instances and connections between studs. Composition
validation checks:

- output event schema matches the downstream input schema
- channel and pipeline scopes permit the connection
- parent budget can satisfy requested unit draw
- requested capability attenuates from parent authority
- child deadline is no later than the parent deadline
- cycles are explicit and have backpressure or retry policy
- every terminal path has a reap signal

### 4. Admit

The daemon admits a unit instance like a dynamic-team charter: resolve origin,
authorize the unit type, grant budget, attenuate capability, resolve deadline,
stamp provenance, render child topology, and record desired state.

Admission should fail before mutation when the unit cannot be safely composed.

### 5. Run

Reconcile materializes the unit, starts or resumes its roles, routes events
through the declared fabric, watches budgets/deadlines, and records status.

### 6. Report

The unit emits resource-shaped reports with evidence URIs. Parents and
downstream units consume reports through resource reads and gates, not log
scraping.

### 7. Reap

When the reap signal or policy fires, reconcile stops live roles, revokes
capabilities, releases unspent reserved budget, archives reports, removes
materializations according to cleanup policy, and leaves a tombstone resource.

## Version Sequencing

### v1: Unit Spec

Define the unit spec and contract model. Add one pre-built type:
`feature-delivery`. Render and validate it like a template, but do not require
full runtime composition yet.

The v1 deliverable is a stable authoring model and validation discipline:
parameters, roles, internal topology, contract studs, budget, capability,
deadline, reap, gates, and report schema.

### v2: Charter From Unit Type

After dynamic-team spawn lands, allow a charter to name `unit_type + params`
instead of only inline team config. Parent attenuation still applies. The unit
type is the cataloged image; `team.spawn` is the engine.

Blocking dependency from GH-223: nested/transitive spawn needs the `team.spawn`
audit resource reconciled with charter-grant resource URIs. Today HTTP auth
checks `team.spawn` against a `team:<name>` resource form, while chartered
grants are expressed as `agt://` deployment or charter URIs. Single-level spawn
works for a fully privileged top-level manager; a chartered child spawning a
sub-unit is rejected until those resource forms align.

### v3: Deployment Composition

Let a deployment declare multiple unit instances and connections:

```text
feature-delivery -> review-gate -> release-note
research-spike -> design-review -> feature-delivery
ops-watch -> incident-response -> postmortem
```

The resource model tracks unit boundaries, budgets, deadlines, reports, and
reap state. The topology fabric materializes the cross-unit channels,
pipelines, and triggers.

### v4: Typed Catalog And Learning

Promote proven compositions into a typed catalog: `feature-delivery`,
`research-spike`, `migration`, `ops-watch`, `incident-response`, and repo- or
domain-specific variants.

The org-review/meta-learning loop should feed this catalog by discovering
which compositions repeatedly work. A recommended starter topology from a
retro is a unit-type candidate. Graduation should be reviewable and evidenced,
not automatic.

## Failure Modes

| Failure | Behavior |
| --- | --- |
| Contract mismatch | Reject composition before admitting any unit. |
| Budget unavailable | Queue or reject with `budget_exhausted`; do not partially spawn. |
| Capability need denied | Omit optional grants or reject required grants; record attenuation. |
| Deadline impossible | Reject admission or ask parent for a later deadline before spawn. |
| Deadline missed | Emit `unit.deadline_missed`; policy decides extension, fallback, or reap. |
| Bespoke coupling requested | Reject the unit type or mark it non-composable until it uses the fabric. |
| Nested spawn denied by resource-form mismatch | Block v2 sub-unit spawn until `team.spawn` authorization accepts charter/deployment URIs. |
| Output lacks evidence | Downstream gate stays blocked; parent treats the report as advisory only. |
| Unit crashes mid-run | Reconcile retries, resumes, or marks degraded according to policy. |
| Unit omits reap signal | Reconcile can reap only by TTL, deadline, parent cancel, or operator action; validation should prevent this for cataloged units. |
| Cycle floods event bus | Backpressure and retry policy must be explicit on cyclic compositions. |

## Observability

Units should appear in operator surfaces as first-class resources:

- graph views show unit nodes, connections, and underlying roles
- `read <agt-uri>` can inspect unit type, unit instance, contract, connection,
  report, and tombstone resources
- budget status rolls spend up by unit, charter, team, and parent deployment
- deadline views show remaining time, warnings, misses, and extension requests
- job explain links parent work items to unit instances and reports
- event history includes `unit_admitted`, `unit_started`, `unit_reported`,
  `unit_deadline_missed`, `unit_completed`, `unit_reaped`, and
  `capability_attenuated`
- CI/review gates can reference unit reports instead of child logs

The parent should never need to scrape child workspaces or logs to understand a
unit. It should read daemon-owned resources by URI.

## Open Questions

- Where should unit specs live first: bundled `template/units/`, repo-local
  `.agent_team/units/`, or a template manifest section?
- How strict should event payload schema compatibility be in v1: named schema
  strings only, JSON Schema, Go structs, or a smaller typed envelope?
- Should `unit` become its own URI kind immediately, or should unit instances
  initially project through child deployment/charter resources?
- Are deadlines always required for cataloged units, or can long-running
  persistent units declare `deadline = "none"` with a health-check contract?
- How does deadline priority interact with hard budget cutoffs and queue
  fairness when multiple units contend for the same build slots?
- What is the minimum report schema that lets downstream gates advance without
  coupling to unit internals?
- Who can promote discovered compositions into the typed catalog, and what
  evidence is required?
- How should unit version upgrades behave for long-running compositions with
  active unit instances?

## Design Constraints To Preserve

- Units compose through a uniform contract, not bespoke glue.
- The resource model is the standard interface; local paths are only
  materializations.
- Channels, pipelines, triggers, gates, budgets, and authority policy remain
  the composition fabric.
- Dynamic spawn must not bypass template review, budget grants, capability
  attenuation, provenance, deadlines, or reconcile.
- A unit may be internally complex, but its boundary must stay small and typed.
- Deadlines are contract data, not prompt prose.
- Reap removes live authority and materializations, not audit history.
- The first catalog should be narrow and proven; free-form unit authoring is a
  later reviewed workflow.
