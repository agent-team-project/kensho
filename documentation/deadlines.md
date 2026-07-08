# Deadlines (design sketch)

Status: design sketch for GH-231. This document is design only and does not
prescribe implementation in this change.

Related design notes:

- [resource-constraints.md](./resource-constraints.md): budgets, live usage
  watchdogs, priority classes, and preemption as the existing constraint model.
- [dynamic-teams.md](./dynamic-teams.md): budget tree inheritance and
  composable child deployments.
- [reconcile.md](./reconcile.md): desired state, observed state, lifecycle
  events, and controller-owned status.
- [topology.md](./topology.md): where declarative teams, pipelines, schedules,
  budgets, locks, and authority policy are owned today.

## Summary

A deadline is the absolute wall-clock time axis of the resource constraint
model. Budgets constrain spend: tokens, jobs in flight, and relative runtime
allowances. Deadlines constrain time-to-completion: "this job, epic, unit, or
deployment should be complete by 2026-07-10T17:00:00Z."

That distinction matters because a deadline is not just another cutoff. A
relative `time_budget` can kill or warn about one running attempt after 45
minutes, but it cannot answer which queued work should run first, which epic is
slipping, or whether a child unit is allowed to promise a later delivery than
its parent. Deadlines give the daemon a scheduling and health dimension:

- earliest-deadline-first ordering for queues and scarce shared resources
- at-risk projection when remaining work and observed velocity imply the
  deadline will miss
- escalation when a deadline is at risk: alert, request budget, add replicas,
  preempt lower-slack work, or shed scope
- propagation down the budget/unit tree so child promises are bounded by parent
  commitments
- org-review signals for deadline hit rate, slip rate, at-risk lead time, and
  escalation effectiveness

## Goals

- Define deadlines as first-class constraint attributes on durable jobs, epics,
  composable units, and deployments.
- Keep deadlines distinct from `time_budget`, token budgets, and watchdog
  kill semantics.
- Define the parent-bound propagation rule: a child deadline cannot be later
  than the closest enclosing parent deadline.
- Define at-risk projection in terms of remaining work, observed velocity,
  queue/resource delay, and confidence.
- Define the escalation policy surface and its authority boundaries.
- Define the lifecycle events and status fields needed for operators,
  managers, dashboards, and org-review.
- Sequence the rollout from observation to scheduling to escalation to full
  unit-tree propagation.

## Non-Goals

- Implement new CLI flags, topology fields, daemon APIs, or scheduler behavior
  in this change.
- Replace token budgets, job time budgets, hard cutoffs, or watchdogs.
- Promise hard real-time completion. A deadline is an input to scheduling,
  projection, and escalation; it is not a guarantee that external systems,
  reviewers, CI, or humans will act by that time.
- Invent a full effort-estimation system. The projection model should start
  with conservative historical durations and become more precise as outcomes
  telemetry improves.
- Let child units silently relax parent commitments. Extending beyond a parent
  deadline is a parent-level change, not a local override.

## Terms

**Deadline** is an absolute instant, stored and compared in UTC, that represents
the latest intended completion time for a resource.

**Time budget** is a relative allowance for one job or attempt, such as
`45m`. It may notify or kill a running process. It does not imply that the work
must be delivered by a particular wall-clock instant.

**Completion** is resource-specific. For a pipeline job, it means the delivery
contract has reached its terminal success state, such as a merged PR or accepted
report. For an epic or unit, it means all required child work has reached its
own completion condition.

**Slack** is the time between projected completion and the deadline:

```text
slack = deadline - projected_finish
```

Positive slack means the work is projected to finish before the deadline.
Negative slack means the work is projected to miss.

**At-risk** means the confidence-adjusted projection crosses a policy
threshold, usually `projected_finish_p80 > deadline`. At-risk is not the same as
missed; it is early enough to act.

**Preemption** is kill-and-requeue of lower-priority or higher-slack work to
free a scarce resource. It uses the same crash-only recovery mechanics described
in `resource-constraints.md`.

**Unit** is a composable delivery unit: a project, child deployment, dynamic
team charter, or future package of work that can declare a contract to its
parent.

## Constraint Model

Budgets and deadlines are separate axes:

| Axis | Constraint | Shape | Primary behavior |
| --- | --- | --- | --- |
| Spend | token budget, jobs in flight, external quota | amount over a window | admit, queue, notify, kill when hard |
| Attempt runtime | `time_budget` | duration from start | notify, extend, kill when hard |
| Delivery time | `deadline` | absolute instant | order, project, escalate, preempt |

The axes compose. A job can be under its token allowance and still be at risk
because review and CI latency put its finish after the deadline. Another job can
be close to its token budget but have enough deadline slack that it should wait
behind a nearer commitment.

Admission remains the first enforcement point. The scheduler should only order
eligible work after hard gates have passed: authority, topology match, locks,
budget availability, and dependency readiness. Deadlines affect which eligible
work drains first and which running work may be interrupted by explicit
preemption policy.

## Schema Sketch

Deadlines should appear as attributes on the same durable resources that carry
budgets, provenance, and lifecycle state. The exact storage format can evolve,
but the conceptual resource shape is:

```json
{
  "uri": "agt://dep/job/gh231-deadline-design",
  "kind": "job",
  "spec": {
    "pipeline": "platform_ticket_to_pr",
    "deadline": "2026-07-10T17:00:00Z",
    "deadline_policy": "delivery-default",
    "budget_tokens": 40000000,
    "budget_time": "45m"
  },
  "status": {
    "deadline_state": "on_track",
    "projected_finish": "2026-07-10T14:30:00Z",
    "projected_finish_confidence": "p80",
    "slack_ms": 9000000,
    "last_deadline_event": "deadline_recovered"
  }
}
```

### Common fields

| Field | Owner | Meaning |
| --- | --- | --- |
| `deadline` | spec | Absolute RFC3339 timestamp, normalized to UTC. |
| `deadline_source` | spec/status | `explicit`, `inherited`, `computed`, or `unset`. |
| `deadline_parent_uri` | spec/status | The nearest ancestor whose deadline bounds this resource. |
| `deadline_policy` | spec | Named policy that selects projection thresholds and allowed escalations. |
| `deadline_state` | status | `unset`, `on_track`, `at_risk`, `missed`, `met`, or `waived`. |
| `projected_finish` | status | Current confidence-adjusted completion projection. |
| `projected_finish_confidence` | status | Projection percentile, initially `p50` or `p80`. |
| `slack_ms` | status | `deadline - projected_finish`, in milliseconds. |
| `deadline_generation` | status | Generation of spec used to compute the current status. |

Deadlines should not be overloaded into `time_budget`. A relative duration can
still derive an armed watchdog deadline for one runtime process, but that value
is process status, not the durable delivery deadline:

```json
{
  "runtime": {
    "runtime_budget": "45m",
    "runtime_deadline": "2026-07-08T12:45:00Z"
  },
  "spec": {
    "deadline": "2026-07-10T17:00:00Z"
  }
}
```

### Jobs

Durable jobs are the first implementable surface because they already own
pipeline state, budget allowances, branch/PR metadata, gates, and lifecycle
events.

Possible CLI and topology inputs:

```sh
agent-team job create GH231-deadline-design \
  --pipeline platform_ticket_to_pr \
  --deadline 2026-07-10T17:00:00Z \
  --budget-tokens 40M \
  --budget-time 45m
```

```toml
[pipelines.platform_ticket_to_pr.steps.implement]
token_budget = "40M"
time_budget = "45m"
deadline_policy = "delivery-default"
```

The job record should store the explicit deadline, inherited parent deadline if
one exists, and the policy used for projection. Pipeline steps may have
relative internal targets, but the durable job deadline is the end-to-end
contract.

### Epics

Epics are parent resources. They bound the jobs or child epics they contain:

```json
{
  "uri": "agt://dep/epic/github:231",
  "kind": "epic",
  "spec": {
    "deadline": "2026-08-01T17:00:00Z",
    "deadline_policy": "epic-default"
  },
  "status": {
    "deadline_state": "at_risk",
    "projected_finish": "2026-08-03T10:00:00Z",
    "children_at_risk": 3,
    "children_missed": 0
  }
}
```

An epic may be at risk even when no individual child is currently late if the
aggregate remaining work and velocity imply the parent finish will miss. Epic
projection should account for work not yet decomposed into jobs by reserving an
explicit "unplanned work" estimate or by marking confidence low.

### Units and deployments

Composable units and child deployments carry deadline as part of their
contract. A parent can then evaluate whether a unit is useful before admitting
it:

```json
{
  "contract": {
    "unit": "adapter-port",
    "deadline": "2026-07-20T17:00:00Z",
    "confidence": "p80",
    "requires": {
      "budget_tokens": 30000000,
      "budget_time": "90m"
    }
  }
}
```

The admission invariant mirrors the budget tree:

```text
child.deadline <= parent.deadline
```

If a child needs a later deadline, it must request a parent deadline extension
or scope change. It cannot locally promise beyond the parent and remain
composable.

### Missing deadlines

Unset deadlines are allowed unless the parent resource or policy marks them
required. Unset means:

- no earliest-deadline scheduling boost
- excluded from deadline hit-rate denominators
- still subject to any ancestor that requires children to carry bounded
  deadlines
- still visible in org-review as "uncommitted work" if it belongs to a
  deadline-bound epic or unit

## Propagation

Deadlines propagate down the same ownership tree as budgets and capability
grants:

```text
operator commitment
  -> team / deployment deadline
    -> epic deadline
      -> job deadline
        -> pipeline step target
          -> runtime attempt watchdog deadline
```

Only the durable delivery deadline participates in the parent-bound invariant.
A runtime watchdog deadline is derived from an attempt start time and
`time_budget`; it may be later or earlier than the durable deadline depending
on when the attempt starts. If the watchdog would finish after the durable
deadline, the job is already at risk and should escalate before the attempt is
allowed to consume scarce capacity.

Propagation rules:

1. A child with no explicit deadline inherits the nearest required parent
   deadline when policy says child deadlines are required.
2. A child with an explicit deadline must be no later than the nearest parent
   deadline.
3. A child may choose an earlier deadline to preserve integration, review, or
   deployment buffer.
4. Parent deadline extension revalidates children but does not automatically
   relax explicit child deadlines.
5. Parent deadline contraction is a spec change that may make children invalid
   or immediately at risk; reconcile should report the affected resources
   before mutating live work.

These rules keep promises attenuated. A manager can split a deadline-bound
epic into jobs only by allocating time inside the parent envelope.

## At-Risk Projection

Projection answers one question: "Given what we know now, will this resource
complete by its deadline?" The minimum useful model combines remaining work,
observed velocity, queue/resource delay, and uncertainty.

For one pipeline job:

```text
projected_finish_p80 =
  now
  + queue_wait_p80
  + current_step_remaining_p80
  + sum(remaining_step_duration_p80)
  + review_or_gate_latency_p80

slack_p80 = deadline - projected_finish_p80
at_risk = slack_p80 < policy.min_slack
```

For an epic or unit:

```text
remaining_work_units =
  incomplete_child_work
  + estimated_unplanned_work
  + review_rework_buffer

projected_finish_p80 =
  now + remaining_work_units / observed_velocity_p80

slack_p80 = deadline - projected_finish_p80
```

Inputs should start conservative:

- historical p50/p80 durations for each pipeline step and gate
- current queue depth and lock wait times
- current running step elapsed time and remaining watchdog allowance
- observed team or unit velocity from outcomes: completed weighted work per day
- review bounce rate and CI latency
- explicit manager estimates for undecomposed work

The daemon should emit a projection with a confidence label. Low-confidence
projections are still useful, but org-review should treat them differently from
high-confidence misses.

### At-risk lifecycle

Deadline status should be evented, not inferred by dashboards alone:

| Event | When |
| --- | --- |
| `deadline_projected` | Projection refreshed for a deadline-bound resource. |
| `deadline_at_risk` | Resource crosses the at-risk threshold. |
| `deadline_recovered` | Previously at-risk resource returns above threshold. |
| `deadline_missed` | Now is later than the deadline and completion is not terminal success. |
| `deadline_met` | Resource reaches terminal success before or at the deadline. |
| `deadline_waived` | Operator or owning manager removes the commitment with a reason. |

Events should include resource URI, parent URI, deadline, projected finish,
slack, confidence, policy name, and the projection inputs that made the decision
auditable.

## Scheduling

Earliest-deadline-first scheduling should apply after eligibility checks:

1. Drop or defer work whose dependencies, locks, authority, or budgets are not
   satisfied.
2. Order eligible deadline-bound work by earliest deadline.
3. Order eligible work without deadlines after deadline-bound work, unless an
   explicit priority class says otherwise.
4. Break ties by priority class, then by lower slack, then by FIFO age.

Priority classes and deadlines should compose rather than replace each other.
An incident may still jump a routine deadline-bound job. Among work in the same
priority class, the earliest deadline drains first.

A queue item with negative slack should not be starved just because it has
already missed. It should either drain first, escalate for scope/budget change,
or be explicitly waived/cancelled. Silent starvation makes deadline health
metrics meaningless.

## Escalation Policy

Escalation is the action plan when a resource becomes at risk. It should be
declared policy, not hardcoded daemon instinct:

```toml
[deadlines.policies.delivery-default]
projection = "p80"
min_slack = "0s"
warn_before = "4h"
actions = [
  "notify_owner",
  "raise_priority",
  "request_budget_extension",
  "add_replica",
  "preempt_batch",
]
```

Actions should be typed and authority-checked:

| Action | Effect | Authority boundary |
| --- | --- | --- |
| `notify_owner` | Message manager/operator and record status. | Safe for daemon-owned status. |
| `raise_priority` | Move queued work ahead within policy bounds. | Requires scheduler authority. |
| `request_budget_extension` | Ask parent budget owner for tokens/time. | Parent owns the grant. |
| `add_replica` | Dispatch another worker/reviewer if idempotent. | Must respect locks, max replicas, and budget. |
| `preempt_batch` | Kill-and-requeue lower-priority or higher-slack work. | Requires explicit preemption policy. |
| `shed_scope` | Propose dropping optional child work. | Manager/operator decision, not automatic deletion. |
| `extend_deadline` | Move the commitment later. | Parent/operator decision with audit reason. |

Escalation should be staged:

```text
warn window crossed -> notify + raise priority
at-risk threshold crossed -> request budget or add replica
negative slack while still possible -> preempt eligible lower-slack work
deadline missed -> mark missed, keep or cancel per owner policy
```

The policy must preserve the existing design principle from
`resource-constraints.md`: admission and step-boundary decisions are preferred;
mid-run interruption is last resort. Preemption should only target work that is
safe to requeue and whose own deadline slack or priority class allows it.

## Preemption

Deadlines supply the missing reason for preemption. Without a nearer deadline,
preemption is arbitrary churn. With deadlines, the scheduler can compare the
cost of interrupting one resource against the projected miss of another.

A preemption candidate is eligible only when all are true:

- it holds a resource needed by the at-risk work, such as a worker slot, review
  slot, build lock, or provider quota
- its job is restartable or has reached a safe step boundary
- its priority class is lower, or its deadline slack is materially larger
- preemption is enabled for the resource and the actor has authority
- the expected reclaimed time can change the at-risk projection

The action remains crash-only:

```text
record deadline_preemption_planned
signal lower-priority runtime through normal stop/kill path
mark attempt preempted, not failed
requeue durable job at its previous step boundary
dispatch at-risk work
record deadline_preemption_completed
```

Org-review should account for preemption cost. A system that meets deadlines by
constantly wasting in-flight work is not healthy.

## Composable Unit Contract

Composable units need deadlines as contract studs alongside budget, authority,
interfaces, and expected artifacts. The parent can then compose units without
reading every child implementation detail:

```json
{
  "unit": "release-notes-team",
  "contract": {
    "inputs": ["merged-prs", "changelog"],
    "outputs": ["release-draft"],
    "deadline": "2026-07-15T12:00:00Z",
    "budget_tokens": 10000000,
    "authority": ["github.pr.read", "docs.write"],
    "health": {
      "deadline_state": "on_track",
      "projected_finish": "2026-07-15T10:30:00Z"
    }
  }
}
```

The parent only admits the unit if the contract fits inside parent constraints:

- requested budget fits the parent allocation mode
- requested verbs fit the parent capability grant
- requested deadline is no later than the parent deadline
- expected outputs satisfy the parent unit's dependency graph

This makes deadlines composable in the same way budgets are composable: a unit
cannot spend tokens or time that its parent did not allocate.

## Org-Review Signal

`org-review` should treat deadlines as commitment health, not throughput. Useful
signals:

| Signal | Meaning |
| --- | --- |
| `deadline_hit_rate` | Completed deadline-bound resources finished on time. |
| `deadline_miss_rate` | Deadline-bound resources completed late or remain late. |
| `mean_slip_duration` | Average lateness for missed resources. |
| `p95_slip_duration` | Tail lateness; catches severe misses hidden by averages. |
| `at_risk_lead_time` | How early the system predicted misses. |
| `escalation_success_rate` | At-risk resources recovered after escalation. |
| `preemption_cost` | Wasted attempts or delay imposed on preempted work. |
| `uncommitted_work_ratio` | Work under deadline-bound parents with no child deadline. |
| `false_positive_rate` | At-risk resources that would have finished on time without action. |
| `false_negative_rate` | Misses that were not predicted before the deadline. |

The strategic question is different from budget review:

```text
budget review: are we spending the right amount on the right goals?
deadline review: are we meeting commitments, and do we detect slips early?
```

Deadline health should roll up by team, pipeline, epic, and unit. A team can be
cheap and still unreliable if it repeatedly misses commitments. Another team
can be expensive but predictable if its estimates are honest and its at-risk
events fire early enough to act.

## Operator Surfaces

The first visible surfaces should be read paths and status summaries:

```sh
agent-team job show gh231-deadline-design
agent-team job events gh231-deadline-design --type deadline_at_risk
agent-team budget status --job gh231-deadline-design
agent-team topology summary
```

Potential output fields:

```text
deadline:        2026-07-10T17:00:00Z
deadline_state:  at_risk
projected:       2026-07-10T19:30:00Z p80
slack:           -2h30m
policy:          delivery-default
next_action:     request_budget_extension
```

`budget status` should show deadline only as adjacent context. It should not
pretend deadline is a budget. The value is that spend and time can be inspected
together:

```text
tokens:   28M / 40M
runtime:  31m / 45m
deadline: at_risk, -2h30m slack
```

## Failure Modes

- **Timezone ambiguity.** Accept RFC3339 instants first. Date-only or local-time
  input can come later with explicit timezone policy.
- **False precision.** A projection with weak data should say low confidence
  instead of fabricating exact completion times.
- **Perverse preemption.** Aggressive preemption can waste more work than it
  saves. Policies need max-preemption limits and org-review accounting.
- **Deadline laundering.** Child units must not avoid miss metrics by dropping
  their explicit deadline while still belonging to a deadline-bound parent.
- **Silent extension.** Moving a deadline later without an event destroys the
  health signal. Extensions require owner, reason, old value, and new value.
- **Ignoring undecomposed work.** Epic projections that only sum existing child
  jobs will look falsely healthy. Parents need an unplanned-work estimate or low
  confidence state.
- **Budget-only escalation.** Adding tokens or replicas does not help when the
  bottleneck is human review, CI, or an exclusive lock. Escalation must name the
  bottleneck it expects to relieve.

## Implementation Phasing

### v1 - Deadline attributes and observation

- Add a `deadline` attribute to durable jobs and epic records.
- Normalize to UTC and validate child deadlines against explicit parent
  deadlines where the parent relationship is already known.
- Surface deadline fields in `job show`, status summaries, and
  `budget status` context.
- Compute conservative projections from existing pipeline state, queue depth,
  and historical step durations.
- Emit `deadline_at_risk`, `deadline_recovered`, `deadline_met`, and
  `deadline_missed` lifecycle events.
- No scheduling behavior changes yet.

### v2 - Earliest-deadline-first scheduling

- Apply earliest-deadline-first ordering to eligible queued jobs.
- Preserve existing hard gates: authority, dependencies, locks, and budgets.
- Compose deadlines with priority classes and FIFO age.
- Add queue explanations so operators can see why a later-deadline job ran
  first.
- Track whether EDF reduced miss rate or merely moved misses around.

### v3 - Escalation policy

- Add named deadline policies with projection threshold and allowed actions.
- Wire staged escalation: notify, raise priority, request budget/time,
  add replicas, then preempt only when policy allows.
- Record every escalation decision as an auditable event.
- Add max-preemption and max-replica safeguards.
- Feed escalation outcomes into org-review.

### v4 - Budget/unit tree propagation

- Propagate deadlines through the budget/unit tree, bounded by parent.
- Make composable units declare deadline as a contract stud.
- Validate child deployments, dynamic teams, and child jobs against parent
  deadlines at admission.
- Roll up hit/miss/at-risk metrics by deployment, team, epic, and unit.
- Make org-review report deadline health alongside budget yield.

## Open Questions

1. **Where do epic records live?** GitHub epics are labels/issues today, while
   durable jobs live under `.agent_team/jobs`. The design needs a stable epic
   resource URI before parent-bound validation can be complete.
2. **What is the first effort unit?** Pipeline step p80 duration is easy to
   start with; weighted outcome work units are better for epics but require
   more outcome history.
3. **Who may extend a deadline?** Operators clearly can. Managers may need
   delegated authority, but silent self-extension would undermine the health
   signal.
4. **How should deadlines interact with external PM dates?** GitHub milestones,
   Linear target dates, and local topology deadlines may all exist. The daemon
   needs one resolved deadline plus provenance.
5. **What denominator should org-review use?** Deadline hit rate is meaningful
   only for work that made an explicit commitment. Uncommitted work under a
   committed parent should be measured separately.
