# Dynamic Teams

Status: design draft for GH-155 / SQU-142. This document is the review target
for goal-driven dynamic and ephemeral teams. It intentionally specifies the
resource model and interfaces before implementation.

Related design notes:

- [distributed-resources.md](./distributed-resources.md): canonical `agt://`
  resource URIs, deployment identity, path/materialization split, transport,
  and resource reads.
- [resource-constraints.md](./resource-constraints.md): budget tree invariants,
  child allowances, soft notices, hard cutoffs, and authority scoping.
- [security-model.md](./security-model.md): origin identity, API authority,
  capability attenuation, per-instance tokens, sandboxing, and brokered
  secrets.
- [orchestrator.md](./orchestrator.md): daemon-owned lifecycle, event dispatch,
  topology reconcile, mailboxes, logs, and managed runtime metadata.
- [recoverable-managers.md](./recoverable-managers.md): persistent managers,
  restart policy, generated briefs, and managed resume.
- [runtime-contract.md](./runtime-contract.md): runtime adapters, verified
  consequential signals, MCP/CLI shims, and manager tick.
- [topology.md](./topology.md): declared teams, instances, pipelines, schedules,
  budgets, authority policy, and event routing.

## Summary

Today an agent can dispatch an existing declared role, but it cannot define a
new goal-specific team, let that team run under inherited constraints, and tear
it down when the goal is complete. Dynamic teams add that capability.

The design choice is deliberately narrow:

**A dynamic team is a child deployment resource with a parent, a charter, an
attenuated capability set, and a budget allocation.**

That is the manifest insight. Nested static teams, runtime-created ephemeral
teams, and machine-discovered worker pools are the same primitive:

1. A parent deployment declares or discovers a child deployment.
2. The child receives a constrained manifest: agents, instances, teams,
   pipelines, schedules, budgets, authority, runtime profiles, and placement.
3. Reconcile materializes the child and reports observed resources back through
   canonical URIs.
4. Parent and child communicate only through daemon verbs and resource reads,
   not by sharing storage paths.

The result is runtime-instantiated governed topology, not a second dispatch
system. A dynamic team gets the same guardrails as a static team because it is
represented by the same resource graph and enforced by the same daemon verbs.

## Goals

- Let a manager compose a short-lived team for a concrete goal when the static
  roster lacks the right shape.
- Make the dynamically created team inherit the creator's budget, authority,
  provenance, transport, and lifecycle constraints.
- Support manager-created sub-managers without special-casing the manager role.
- Keep static nested teams, ephemeral teams, and discovered remote placements on
  one resource primitive.
- Prefer curated team and role templates first. Free-form prompt authoring is a
  later, higher-risk extension.

## Non-Goals

- Implement `team.spawn` in this ticket.
- Let agents mint unconstrained prompts, tools, secrets, budgets, or
  deployments.
- Replace `instances.toml`; dynamic teams produce temporary topology
  declarations that use the same model.
- Move manager judgment into the daemon. The daemon admits, reconciles, and
  enforces; managers decide when a child team is useful.
- Solve multi-host trust beyond the tokenized deployment transport already
  described in `distributed-resources.md` and `security-model.md`.

## Terms

**Deployment**: one agent-team control plane. In the current resource package,
the deployment self is represented by the `project` resource and
`[project].id`. This design treats deployment as the conceptual resource and can
map it to today's `project` self until a first-class `deployment` kind lands.

**Parent deployment**: the deployment whose manager, operator, schedule, or
pipeline requested a child team.

**Child deployment**: a constrained control-plane instance created, declared, or
discovered under a parent. It can be local, containerized, or remote.

**Dynamic team**: a child deployment created at runtime for a specific goal.

**Ephemeral team**: a dynamic team with a TTL, completion condition, or owner
job that causes automatic reap.

**Charter**: the immutable spawn intent for a child team: goal, requested team
template, role bindings, budget, capability request, lifecycle policy, and
success criteria.

**Manifest**: the topology/config bundle materialized from the charter and
template library. Static nested teams and dynamic teams both reconcile from a
manifest.

**Attenuation**: the rule that a child capability set must be a strict subset of
the parent capability set and may be narrower by verb, resource, duration,
budget, runtime, placement, and template source.

## Invariants

Dynamic teams are safe only if these invariants hold:

1. **Identity is URI-first.** Parent, child, charter, team, job, instance,
   workspace, state, mailbox, budget, and logs have canonical `agt://` URIs.
   Local paths are materializations and never durable cross-boundary identity.
2. **Parent edge is mandatory.** Every child deployment has
   `parent_deployment_uri`. Ephemeral children also have an owning charter,
   origin, TTL, and reap policy.
3. **Budget allocations form a tree.** A child receives an allocation under the
   parent allocation. Child allocations plus parent consumption may not exceed
   the parent grant under the selected allocation semantics.
4. **Capabilities attenuate.** A child token can never carry a verb or resource
   scope that the creator did not have. The daemon computes the grant and
   records the attenuation decision.
5. **Provenance flows downward and results flow upward.** Child-originated jobs,
   instances, events, and reports include the parent deployment, creator
   instance, creator job, charter, and goal. Parent-visible summaries point back
   to child resource URIs.
6. **Reconcile is the actuator.** `team.spawn` records desired child state.
   Reconcile creates, resumes, adopts, drains, and reaps. No agent writes child
   control-plane files directly.
7. **Templates gate authoring.** The first version only allows curated role and
   team templates. Free-form agent prompts require a separate policy and review
   surface.
8. **Reads stay broad; writes stay scoped.** A parent can inspect child
   resources through `read` and report resources, but child writes are limited
   to its own delegated scope.

## Resource Model

The existing resource model already has deployment-shaped pieces:

- `[project].id` is the deployment id authority.
- `resource.DeploymentURI` maps the deployment self to the project URI.
- `resource.Deployment` already carries `ParentURI`.
- `/v1/resources?uri=...` reads daemon-owned resources by canonical URI.
- dispatch payloads now carry `deployment_uri`, `deployment_parent_uri`,
  `job_uri`, `workspace_uri`, `state_uri`, and instance URIs.

Dynamic teams extend that shape instead of replacing it.

### Deployment Resource

Conceptual shape:

```json
{
  "kind": "deployment",
  "uri": "agt://child-dep/deployment/child-dep",
  "id": "child-dep",
  "parent_deployment_uri": "agt://parent-dep/deployment/parent-dep",
  "relationship": "ephemeral_team",
  "state": "chartered|spawning|running|reconciling|draining|reaped|failed",
  "created_by": "agt://parent-dep/instance/manager-platform",
  "charter_uri": "agt://parent-dep/charter/charter_01j...",
  "goal_uri": "agt://parent-dep/goal/goal_01j...",
  "topology_uri": "agt://child-dep/topology/current",
  "transport": {
    "kind": "loopback|unix|http|remote",
    "endpoint_ref": "brokered-by-parent"
  },
  "placement": {
    "kind": "same_host|container|remote",
    "workspace_policy": "worktree|container_mount|remote_checkout"
  },
  "lifecycle": {
    "ttl": "2h",
    "reap": "on_goal_complete|on_ttl|manual"
  }
}
```

Until `deployment` is a first-class kind, the same fields can live on the
existing `project` resource as the deployment self. The important property is
the parent edge, not the path name.

### Charter Resource

The charter is append-only intent. Reconcile may add status and observations,
but it does not rewrite the requested scope.

```json
{
  "kind": "team_charter",
  "uri": "agt://parent-dep/charter/charter_01j...",
  "parent_deployment_uri": "agt://parent-dep/deployment/parent-dep",
  "creator_instance_uri": "agt://parent-dep/instance/manager-platform",
  "creator_job_uri": "agt://parent-dep/job/gh155-dynamic-teams-design",
  "goal": {
    "summary": "Port provider API tests across three adapters",
    "success": [
      "all adapter tests pass",
      "one integration report is returned to the parent"
    ]
  },
  "template": {
    "team": "adapter-porting-squad",
    "version": "sha256:...",
    "parameters": {
      "adapters": ["linear", "github", "jira"]
    }
  },
  "requested_budget": {
    "tokens": 30000000,
    "time": "90m",
    "allocation": "reserve"
  },
  "requested_authority": {
    "verbs": [
      "inbox.*",
      "channel.*",
      "job.show",
      "job.gate.*:own",
      "read.*"
    ],
    "resources": [
      "agt://parent-dep/job/gh155-dynamic-teams-design",
      "agt://parent-dep/channel/%23adapter-porting"
    ]
  },
  "lifecycle": {
    "ttl": "2h",
    "reap": "on_goal_complete"
  }
}
```

The charter gives reviewers and operators one durable artifact to answer:

- Why does this child exist?
- Who created it?
- What could it spend?
- What could it touch?
- What template was used?
- What condition reaps it?

### Team Resource

The topology docs currently model teams as ownership groups inside
`instances.toml`. Dynamic teams need `team` to be readable as a resource:

```json
{
  "kind": "team",
  "uri": "agt://child-dep/team/adapter-porting",
  "deployment_uri": "agt://child-dep/deployment/child-dep",
  "parent_team_uri": "agt://parent-dep/team/platform",
  "description": "Ephemeral adapter-porting team for one goal.",
  "instances": [
    "agt://child-dep/instance/sub-manager",
    "agt://child-dep/instance/worker-linear",
    "agt://child-dep/instance/worker-github"
  ],
  "pipelines": ["agt://child-dep/pipeline/adapter_port"],
  "budgets": ["agt://child-dep/budget/adapter-porting"],
  "authority": ["agt://child-dep/capability/cap_01j..."]
}
```

This also solves the static case. A nested static team is just a team resource
whose deployment relationship is declared in a manifest rather than created by
`team.spawn`.

### Template Resource

Dynamic team templates are the safe authoring boundary.

```toml
[team_templates.adapter-porting-squad]
description = "Sub-manager plus N implementation workers for adapter parity."
allowed_agents = ["manager", "worker", "reviewer"]
max_replicas = 6
max_ttl = "4h"
required_success_report = true

[[team_templates.adapter-porting-squad.roles]]
name = "sub-manager"
agent = "manager"
persistent = true
brief = true

[[team_templates.adapter-porting-squad.roles]]
name = "implementation-worker"
agent = "worker"
replicas_from = "parameters.adapters"
ephemeral = true
workspace = "worktree"
```

Template policy answers the prompt-authoring question from the issue:

- First version: managers can instantiate templates and fill declared
  parameters.
- Later version: managers can propose new templates as reviewable resources.
- Deferred version: managers can author free-form prompts only under a separate
  untrusted-authoring policy with human or privileged-agent review.

## Agent-Facing Verb

The first agent-facing primitive is `team.spawn`. It is a daemon verb, exposed
through CLI/MCP/mailbox shims the same way other consequential actions are.

Sketch:

```sh
agent-team team spawn \
  --template adapter-porting-squad \
  --name adapter-port-gh155 \
  --goal "Port provider API tests across adapters" \
  --budget-tokens 30M \
  --budget-time 90m \
  --ttl 2h \
  --param adapters=linear,github,jira
```

HTTP/MCP shape:

```json
{
  "verb": "team.spawn",
  "parent_deployment_uri": "agt://parent-dep/deployment/parent-dep",
  "creator_instance_uri": "agt://parent-dep/instance/manager-platform",
  "creator_job_uri": "agt://parent-dep/job/gh155-dynamic-teams-design",
  "template": "adapter-porting-squad",
  "name": "adapter-port-gh155",
  "goal": {
    "summary": "Port provider API tests across adapters",
    "success": ["tests pass", "report posted to parent"]
  },
  "parameters": {
    "adapters": ["linear", "github", "jira"]
  },
  "budget": {
    "tokens": 30000000,
    "time": "90m",
    "hard_multiplier": 1.5
  },
  "authority": {
    "verbs": ["job.show", "job.gate.*:own", "read.*", "inbox.*"],
    "resources": ["agt://parent-dep/job/gh155-dynamic-teams-design"]
  },
  "lifecycle": {
    "ttl": "2h",
    "reap": "on_goal_complete"
  }
}
```

Response:

```json
{
  "accepted": true,
  "charter_uri": "agt://parent-dep/charter/charter_01j...",
  "child_deployment_uri": "agt://child-dep/deployment/child-dep",
  "team_uri": "agt://child-dep/team/adapter-port-gh155",
  "budget_allocation_uri": "agt://parent-dep/allocation/alloc_01j...",
  "capability_uri": "agt://child-dep/capability/cap_01j...",
  "state": "chartered"
}
```

`team.spawn` does not synchronously start every role. It records the charter,
allocates budget, mints attenuated capability, writes the child manifest, and
queues reconcile. That keeps it aligned with the crash-only daemon model.

## Admission And Attenuation

The daemon evaluates a spawn request in this order:

1. **Origin resolution.** Resolve the caller from the trusted token, daemon
   metadata, origin header, and topology. Caller-provided identity is only a
   hint.
2. **Template authorization.** Check that the caller may instantiate the named
   team template and use the requested parameters.
3. **Budget grant.** Grant child budget under the caller's team/job allocation.
   Clamp or reject if requested headroom exceeds parent availability.
4. **Capability attenuation.** Intersect requested verbs and resources with the
   caller's effective capability. Apply template caps, TTL caps, resource scope
   caps, and placement caps.
5. **Provenance envelope.** Stamp parent deployment, creator team, creator
   agent, creator instance, creator job, trigger, charter, and goal.
6. **Manifest render.** Render a child topology/config bundle from the template
   and granted constraints.
7. **Queue reconcile.** Persist a desired child deployment and enqueue reconcile.

The attenuation result should be durable and readable:

```json
{
  "requested_verbs": ["*", "job.merge", "job.gate.*:own"],
  "granted_verbs": ["job.gate.*:own", "job.show", "inbox.*"],
  "denied": [
    {
      "verb": "*",
      "reason": "not present in parent capability"
    },
    {
      "verb": "job.merge",
      "reason": "template does not allow merge authority"
    }
  ],
  "expires_at": "2026-07-07T18:00:00Z"
}
```

In audit mode, denied requested grants are recorded and omitted. In enforce mode,
the request fails if a required grant cannot be satisfied. Optional grants can be
omitted in both modes.

## Budget Tree

Dynamic teams use the budget economy from `resource-constraints.md`.

Static topology today is mostly operator -> team -> job. Dynamic teams add:

```text
operator budget
  -> parent team budget
    -> parent job or manager allocation
      -> child deployment budget
        -> child team budget
          -> child job / child instance allowance
```

Rules:

- The child allocation is a child of the creator allocation, not a sibling of
  the parent team budget.
- `reserve` mode debits parent outstanding allocation immediately.
- `oversubscribe` mode may admit based on spend, but the child still records
  the parent edge for accountability.
- Child usage reports roll up to both child team and parent charter.
- Hard cutoffs kill child instances through normal runtime watchdog semantics.
- Reap releases unspent reserved allocation and closes outstanding child
  allowances.

This is what prevents runaway spawn. A manager can create a sub-team only by
spending part of its own grant.

## Provenance Flow

Parent-to-child launch env and resource records should include both local labels
and canonical URIs:

```text
AGENT_TEAM_DEPLOYMENT_URI=agt://child-dep/deployment/child-dep
AGENT_TEAM_DEPLOYMENT_PARENT_URI=agt://parent-dep/deployment/parent-dep
AGENT_TEAM_CHARTER_URI=agt://parent-dep/charter/charter_01j...
AGENT_TEAM_GOAL_URI=agt://parent-dep/goal/goal_01j...
AGENT_TEAM_CREATOR_INSTANCE_URI=agt://parent-dep/instance/manager-platform
AGENT_TEAM_DAEMON_URL=http://127.0.0.1:...
AGENT_TEAM_DAEMON_TOKEN_FILE=/state/capability.token
```

Every child job event, gate result, status update, report, and outbox item
should carry an origin envelope extended with:

- parent deployment URI
- child deployment URI
- charter URI
- goal URI
- parent job URI when the dynamic team exists for one job
- creator instance URI
- template URI or digest

The parent should not scrape child logs for outcomes. The child publishes a
report resource, gate result, or mailbox message that references child evidence
URIs. Parent reads use `agent-team read <agt-uri>` or the equivalent daemon API.

## Lifecycle

Dynamic teams follow four phases.

### 1. Charter

The creator asks for a team to satisfy a goal. The daemon admits or rejects the
charter and persists:

- immutable request
- granted budget allocation
- granted capability
- parent/child deployment relationship
- template digest
- lifecycle policy

If admission fails, the manager receives a typed rejection that can be reasoned
about: `budget_exhausted`, `capability_denied`, `template_denied`,
`invalid_parameters`, or `placement_unavailable`.

### 2. Spawn

Spawn materializes the child deployment:

- render child `.agent_team/` from the template and parameters
- write child config with `[project].id`, `parent_uri`, and deployment metadata
- write child `instances.toml`
- create workspace/state leases
- mint per-child and per-instance capability tokens
- dispatch the bootstrap role, usually a sub-manager

For same-host children, this may be a directory under the parent daemon's
managed state. For container or remote children, the materialization is a
workspace mount plus deployment endpoint and token. The resource URI is stable
across those placements.

### 3. Reconcile

Reconcile is the steady-state actuator:

- start missing desired persistent roles
- dispatch queued child pipeline steps
- adopt surviving child processes after daemon restart
- resume recoverable managers when runtime metadata permits
- drain mailbox and verified signal queues
- propagate child health and reports to the parent
- enforce TTL, budget, and capability expiry

This is the same lifecycle philosophy as `orchestrator.md` and
`recoverable-managers.md`: desired state plus crash-only reconciliation, not
long in-memory promises.

### 4. Reap

Reap runs when one of these is true:

- charter success condition is met
- TTL expires
- parent cancels the charter
- child exhausts hard budget
- child violates enforced authority policy
- operator reaps manually

Reap should:

- stop child instances
- collect final reports, usage, gates, and event summaries
- release unspent reserved budget
- revoke child capability tokens
- archive child daemon state according to retention policy
- remove workspaces according to cleanup policy
- leave a parent-readable tombstone resource

Reap does not erase evidence. It removes live capability and local
materializations while preserving the outcome record.

## Manager To Sub-Manager Flow

The native-manager arc makes dynamic teams useful. A persistent manager already
has restart policy, generated briefs, managed resume, and mailbox delivery. The
runtime contract adds verified signals and a manager tick, so a manager can
react without a human driving each step.

Sub-manager flow:

1. Parent manager receives a consequential signal or job that needs more
   parallel judgment than the static roster provides.
2. Parent manager writes a charter through `team.spawn` using a curated template
   that includes a persistent `manager` role.
3. Daemon admits the charter, attenuates authority, grants budget, and renders
   the child deployment.
4. Reconcile starts the child sub-manager with a generated brief containing:
   charter, goal, budget, granted verbs, parent mailbox/channel, expected
   report shape, and child team topology.
5. Child sub-manager decomposes the goal inside its own deployment, dispatching
   child workers under its allocation.
6. Child sub-manager publishes reports or gate results to parent-visible
   resources.
7. Parent manager reads the reports, decides whether the parent goal is
   satisfied, and either closes the charter, grants an extension, or reaps the
   child.

The child sub-manager is not privileged because it is a manager. It is powerful
only inside the attenuated deployment it was granted.

## Static, Dynamic, And Discovered Deployments

One primitive covers three cases:

### Nested Static Team

A template or repo declares a child deployment relationship up front:

```toml
[deployments.docs]
relationship = "static_child"
parent = "self"
template = "docs-team"
```

Reconcile keeps it alive according to its manifest. Operators review it like any
other topology.

### Dynamic Ephemeral Team

A manager calls `team.spawn` at runtime. The child manifest is derived from a
reviewed template plus charter parameters. Reap is automatic.

### Machine Discovery

A daemon discovers another deployment in the registry or through a handshake:

```toml
[deployments.m2-runner]
relationship = "placement"
parent = "self"
placement = "remote"
```

The discovered child can receive work only after the parent grants capability
and budget. Discovery alone does not authorize dispatch.

The common fields are parent URI, relationship type, manifest/template source,
transport, capability, and budget. The source differs; the resource primitive
does not.

## Interface Sketches

### Topology Extension

Static declarations and rendered dynamic manifests can share a schema:

```toml
[deployments.adapter-port-gh155]
relationship = "ephemeral_team"
parent = "self"
template = "adapter-porting-squad"
ttl = "2h"
reap = "on_goal_complete"

[deployments.adapter-port-gh155.budget]
tokens = "30M"
time = "90m"
allocation = "reserve"

[deployments.adapter-port-gh155.authority]
allow = ["inbox.*", "channel.*", "job.show", "job.gate.*:own", "read.*"]

[deployments.adapter-port-gh155.parameters]
adapters = ["linear", "github", "jira"]
```

The rendered child still has ordinary topology:

```toml
[instances.sub-manager]
agent = "manager"
ephemeral = false
brief = true
restart = "on-failure"

[[instances.sub-manager.triggers]]
event = "user_invocation"

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 3
token_budget = "10M"
time_budget = "30m"

[teams.adapter-porting]
instances = ["sub-manager", "worker"]
pipelines = ["adapter_port"]

[authority]
enforcement = "enforce"

[authority.agents.manager]
allow = ["inbox.*", "channel.*", "job.show", "job.step.*:own", "team.reap:own"]

[authority.agents.worker]
allow = ["inbox.*", "channel.*", "job.show", "job.gate.*:own", "status.*"]
```

### Daemon Endpoints

Possible HTTP shape:

```text
POST /v1/team/spawn
GET  /v1/team/charters/{id}
POST /v1/team/charters/{id}/reconcile
POST /v1/team/charters/{id}/reap
GET  /v1/resources?uri=agt://child-dep/deployment/child-dep
```

The exact routes can change, but the verbs should remain resource-scoped:

| Verb | Purpose | Scope |
| --- | --- | --- |
| `team.spawn` | Create a child charter and desired deployment | parent deployment/team/job |
| `team.reconcile` | Actuate desired child state | child deployment |
| `team.reap` | Stop child and revoke live capability | child deployment/charter |
| `team.extend` | Add child budget or time | parent allocation and child charter |
| `team.report` | Publish child outcome to parent | child charter |

### Manager Tool Surface

MCP-capable runtimes can receive ergonomic tools:

```ts
type SpawnTeamRequest = {
  template: string
  name: string
  goal: {
    summary: string
    success: string[]
  }
  parameters?: Record<string, unknown>
  budget: {
    tokens?: string
    time?: string
    hardMultiplier?: number
  }
  lifecycle: {
    ttl?: string
    reap: "on_goal_complete" | "on_ttl" | "manual"
  }
  authority?: {
    verbs?: string[]
    resources?: string[]
  }
}
```

The tool response should include a compact human-readable summary plus the
canonical URIs. The manager should store and reason over URIs, not paths.

## Failure Modes

| Failure | Behavior |
| --- | --- |
| Parent lacks budget | Reject or queue with `budget_exhausted`; no child is created. |
| Parent requests too much authority | Deny required grants or omit optional grants; record attenuation. |
| Template parameters invalid | Reject before budget reservation. |
| Child bootstrap crashes | Reconcile retries according to restart/backoff; parent sees child health. |
| Parent daemon restarts | Reconcile adopts or resumes child deployments from durable metadata. |
| Child daemon unreachable | Parent marks child degraded and may retry transport, pause, or reap. |
| TTL expires mid-work | Reap or request extension based on lifecycle policy and parent authority. |
| Child reports success without evidence | Parent treats it as advisory; consequential completion requires a report/gate resource with evidence URIs. |
| Free-form prompt requested | Reject in v1 unless routed through a reviewed template proposal workflow. |

## Observability

Dynamic teams should be visible through the same operator surfaces as static
teams:

- `team graph` shows parent/child deployment edges.
- `team monitor` includes child health, budgets, and ready work.
- `budget status` rolls child allocation and spend into the parent tree.
- `job explain` links parent jobs to child charters and reports.
- `read <agt-uri>` can inspect charter, child deployment, team, report, and
  tombstone resources.
- `events` includes `team_spawned`, `team_reconciled`, `team_reported`,
  `team_reaped`, and `capability_attenuated` rows.

The UI design pressure in `ui-design.md` already identified that deployments,
teams, budgets, allocations, gates, reports, and workspace leases need
resource-shaped reads. Dynamic teams depend on the same work and give those
resources a concrete lifecycle.

## Phasing

1. **Resource projection phase.** Add readable deployment/team/charter/report
   projections and parent edges. Keep mutations out.
2. **Template phase.** Define curated team templates and validation. Support
   local same-host child manifests only.
3. **Spawn phase.** Add `team.spawn` in audit mode with budget grant,
   attenuation, provenance, and reconcile-backed startup.
4. **Reap phase.** Add TTL/completion reap, token revocation, budget release,
   tombstones, and parent report rollup.
5. **Native-manager phase.** Let persistent managers spawn sub-managers through
   MCP/CLI shims and react to verified signals through manager tick.
6. **Placement phase.** Extend from same-host child deployments to container and
   remote placements using the same deployment parent edge and capability
   transport.
7. **Authoring phase.** Allow reviewed template proposals. Defer free-form
   prompt/team authoring until the untrusted-input and review policy exists.

## Open Questions

- Should `deployment` become its own URI kind immediately, or should the current
  `project` self resource carry deployment fields until the broader resource
  model stabilizes?
- Should charters live under the parent deployment, the child deployment, or
  both as mirrored resources? Parent-owned charters simplify admission and
  audit; child mirrors simplify local recovery.
- What is the minimum report schema that lets a parent mark a charter complete
  without scraping logs?
- How much of the template library is repo-authored versus bundled? Bundled
  templates are safer defaults; repo-authored templates are necessary for real
  specialization.
- Should dynamic teams be allowed to outlive their creator instance if their
  parent job remains active? The likely answer is yes, but only under the parent
  deployment and charter TTL.

## Design Constraints To Preserve

- Dynamic teams must never bypass topology, budget, authority, or provenance.
- Child resources must be addressable by URI before any local path is exposed.
- A child deployment must be useful without access to parent filesystem state.
- Managers can request child teams; only the daemon grants them.
- Reap removes live capability, not audit history.
- Template-library-first is a security boundary, not a UX preference.
