# Declarative Reconcile And Apply Semantics

Status: design draft for GH-161 / SQU-133. This document is design only and
does not prescribe implementation in this change.

Related design notes:

- [orchestrator.md](./orchestrator.md): daemon-owned lifecycle, metadata,
  mailbox, queue, topology reload, and the current crash-only reconcile API.
- [distributed-resources.md](./distributed-resources.md): canonical `agt://`
  resource URIs, deployment identity, materialization paths, transport, and
  resource reads.
- [dynamic-teams.md](./dynamic-teams.md): child deployments, charters,
  attenuated capabilities, and reconcile-backed team spawn/reap.
- [topology.md](./topology.md): rendered `instances.toml` as the live topology
  source of truth for instances, teams, pipelines, schedules, budgets, locks,
  channels, and authority policy.

## Summary

`agent-team` already has the first pieces of a declarative controller:

- `instances.toml` declares desired topology.
- The daemon owns runtime metadata, jobs, queues, schedules, mailboxes, status,
  and logs.
- `internal/daemon/reconcile.go` repairs daemon state after crashes by adopting
  live processes, marking missing processes exited, and relaunching declared
  persistent instances when restart policy allows it.
- The resource model gives jobs, instances, workspaces, state, topology, and
  related daemon objects stable `agt://` identities.

The missing layer is a full apply/reconcile contract:

```text
desired state + observed state -> diff -> actions -> observed convergence
```

This document defines that contract. It makes `agent-team apply` an idempotent
declarative write path and makes continuous reconcile the daemon's steady-state
actuator. The model is Kubernetes-like in the narrow sense that files declare
spec, daemon-owned status reports observation, and controllers repeatedly move
observed state toward desired state.

## Goals

- Define desired state across `instances.toml` and declared resources.
- Define observed state through daemon-owned resource reads and runtime
  metadata.
- Separate URI identity from mutable `spec` and daemon-owned `status`.
- Specify the reconcile controller loop, including diff, action planning,
  action execution, observation, backoff, and drift policies.
- Specify `agent-team apply` semantics: validate before mutation,
  idempotence, additive-by-default updates, explicit replace/prune behavior,
  and hot-apply boundaries.
- Explain how this extends the current crash-only reconcile implementation
  without invalidating that behavior.
- Connect static topology, dynamic teams, and the resource model into one
  convergence model.

## Non-Goals

- Implement `agent-team apply`.
- Implement new daemon APIs.
- Replace `instances.toml`.
- Turn every local file into a remote object. Local paths remain valid
  materializations behind daemon-owned resource URIs.
- Make the daemon decide whether a manager should create a dynamic team. The
  daemon admits, validates, actuates, and reports; agents and operators still
  decide intent.

## Terms

**Desired state** is the authoritative declaration of what should exist and how
it should be configured. In the current product, the primary desired-state file
is `.agent_team/instances.toml`; the broader model also includes declared
resources such as child deployments, team charters, workspace leases, authority
policy, schedules, budgets, and lock/channel declarations.

**Observed state** is what the daemon can prove right now: process liveness,
metadata status, job state, queue entries, schedule clocks, status files,
workspace materializations, mailbox/channel ledgers, resource-read snapshots,
and child deployment health.

**Spec** is the desired configuration for one resource. It is authored or
rendered from files, templates, charters, or API requests.

**Status** is daemon-owned observation for one resource. Agents may report
signals, but the daemon decides the status value from trusted stores and runtime
checks.

**URI identity** is the stable `agt://<deployment-id>/<kind>/<id>` resource
address. Identity is not status, and it is not a host-local path. A resource may
move between local paths, container mounts, or remote placements without
changing identity.

**Materialization** is a concrete local representation of a resource: a
worktree path, state directory, log file, daemon metadata file, socket, or mount
path. Materialization paths are operational hints, not durable identity.

**Apply** is an operator or automation action that submits desired state,
validates it, computes a diff, and records accepted spec changes.

**Reconcile** is the daemon controller loop that observes the world, compares it
to accepted desired state, executes safe actions, and repeats until observed
state converges or a policy says to wait, back off, or report drift.

## Desired State

The rendered `.agent_team/instances.toml` remains the root desired-state file
for a deployment. At runtime, it declares:

- instances and their agents, persistence, restart policy, replica limits,
  workspace policy, runtime settings, env allowlists, and budgets
- triggers for normalized events such as `agent.dispatch`, `ticket.*`, `pr.*`,
  `schedule`, and `channel.message`
- teams and ownership of instances, pipelines, channels, schedules, budgets,
  and authority policy
- pipelines, steps, dependencies, gates, retry rules, merge policy, and
  delivery contract
- schedules, locks, channels, resource scopes, and authority enforcement mode

The future desired-state graph also includes daemon-recorded resources that are
not hand-authored directly in `instances.toml`:

- dynamic-team charters accepted from `team.spawn`
- rendered child deployment manifests
- workspace lease specs for repo checkouts, worktrees, or container mounts
- capability and secret-handle grants
- durable job specs, including pipeline step specs
- explicit prune/reap policy for resources previously owned by a declaration

These resources should be addressable by URI. For example:

```text
agt://dep/project/dep
agt://dep/topology/current
agt://dep/instance/manager
agt://dep/job/gh161-reconcile-design#step=implement
agt://dep/workspace/branch:gh161-reconcile-design-20d6bc63
agt://dep/state/worker-gh161-reconcile-design-implement
```

Desired state is layered before it reaches the controller. Template defaults,
repo overrides, operator edits, and runtime-created charters produce one
accepted desired snapshot per deployment. Reconcile consumes that accepted
snapshot; it does not re-interpret template layering on every action.

## Observed State

Observed state comes from daemon-owned stores and runtime checks:

- instance metadata under the daemon root, including lifecycle status, PID,
  runtime, workspace, job, branch, PR, session id, resource URIs, log URI, usage,
  and restart backoff
- process-table checks for whether recorded PIDs are still live
- job records under `.agent_team/jobs`, including status, PR, branch, step
  status, gates, usage, and resource URIs
- queue, outbox, mailbox, channel, lock, schedule, and budget stores
- `status.toml` files written by instances and interpreted by the daemon as
  reported phase, blocker, and PR metadata
- resource reads served by `/v1/resources?uri=...`
- child deployment health and reports for dynamic teams

Observed state is not automatically desired state. A running process proves that
something exists, but it does not prove that it is still declared. A path on disk
proves that a materialization exists, but not that it is the authoritative
resource. A manager status file can say "done", but a pipeline gate still needs
the durable job, PR, and gate records to converge.

The daemon should prefer structured observations over path scraping. Direct
file reads can remain local fallbacks, but the reconciliation model should use
daemon-owned resource reads as the stable API boundary.

## Spec, Status, And Identity

Every reconciled resource should have three conceptual parts:

```json
{
  "uri": "agt://dep/instance/manager",
  "kind": "instance",
  "spec": {
    "agent": "manager",
    "persistent": true,
    "restart": "on-failure",
    "workspace": "repo"
  },
  "status": {
    "lifecycle": "running",
    "pid": 12345,
    "session_id": "runtime-session",
    "observed_generation": 7,
    "conditions": [
      {
        "type": "Ready",
        "status": "True",
        "reason": "ProcessLive"
      }
    ]
  }
}
```

The current code does not need to store that exact JSON shape to honor the
model. Existing records can map into it:

- `uri`, `spec_uri`, `deployment_uri`, `job_uri`, `workspace_uri`, `state_uri`,
  and `log_uri` fields identify resources.
- `instances.toml` and job step definitions provide spec.
- daemon metadata, job status, process checks, queue state, and status files
  provide status.

The important invariant is ownership:

- Users, templates, managers, and admission APIs write spec.
- The daemon writes status.
- The resource URI remains stable across spec edits and status changes.
- Host-local paths are fields inside spec or status only when they describe a
  materialization. They are never the cross-deployment identity.

### Generations

Apply should stamp accepted spec changes with a monotonically increasing
generation per resource or per desired snapshot. Reconcile should record the
latest observed generation in status after it has attempted the relevant
actions.

This gives operators a simple convergence test:

```text
metadata.generation == status.observed_generation
conditions.Ready == True
```

Generation tracking also prevents a stale reconcile pass from reporting success
against an older spec after a newer apply has landed.

## Reconcile Controller Loop

The daemon should run reconcile at startup, after topology reload/apply, after
terminal child events, after queue/schedule maintenance ticks, and periodically
for continuous convergence.

One controller pass:

1. Load the accepted desired snapshot.
2. Read observed resources through daemon stores and runtime probes.
3. Normalize both sides by resource URI.
4. Compute a diff.
5. Convert the diff into typed actions.
6. Execute actions with idempotent preconditions.
7. Refresh observed state for acted resources.
8. Update status, conditions, lifecycle events, job events, and backoff.
9. Requeue work that is not yet converged.

Pseudo-flow:

```text
for each tick:
  desired = loadDesiredSnapshot()
  observed = readObservedResources()
  diff = compare(desired, observed)
  actions = plan(diff, policy)

  for action in actions:
    if action.preconditionStillHolds():
      action.apply()

  refreshed = readObservedResources(actions.resources)
  writeStatusAndEvents(desired, refreshed)
```

The controller is level-triggered, not edge-triggered. Losing an event should
not permanently lose convergence because the next pass recomputes desired vs
observed from durable state.

## Diff Model

A diff is a resource-scoped statement that can be rendered to humans and fed to
the action planner.

Suggested diff types:

| Diff | Meaning | Typical action |
| --- | --- | --- |
| `missing` | Desired resource has no observed counterpart. | create, start, resume, enqueue |
| `extra` | Observed resource is not in desired state for the selected ownership scope. | leave, stop, prune, reap |
| `spec_drift` | Observed spec generation or materialization does not match desired spec. | hot-update, restart, rollout, report |
| `status_drift` | Status violates policy while spec is unchanged. | restart, backoff, mark degraded |
| `blocked` | Desired action cannot proceed until a gate, budget, lock, or capability is available. | wait, queue, report condition |
| `invalid` | Desired spec failed validation. | reject apply; do not mutate |
| `unknown` | Observation is incomplete or transport failed. | retry, mark degraded |

Diff output should be available without mutation:

```sh
agent-team plan
agent-team apply --dry-run
agent-team status --plan
```

The diff view is what prevents "silent ready on broken topology": invalid
desired state must produce an explicit rejected/invalid result before the daemon
claims the deployment is ready.

## Action Model

Actions are daemon-owned effects with resource preconditions. They should be
small, replay-safe, and auditable.

Core actions:

- `create`: persist a missing daemon resource, job, queue item, workspace lease,
  child deployment, or status record.
- `start`: launch a desired persistent instance that has no live process.
- `resume`: use managed runtime resume for a persistent instance with a valid
  session id and workspace.
- `restart`: stop and relaunch when policy requires a rollout or failure
  recovery.
- `stop`: gracefully stop an observed process that should no longer be live.
- `prune`: remove a daemon-owned materialization after ownership and retention
  checks.
- `reap`: stop, archive, revoke capabilities, and tombstone a dynamic child
  deployment.
- `update`: hot-apply mutable fields such as budget, schedule, authority mode,
  labels, trigger payload defaults, or reminder settings.
- `enqueue`: record work that cannot run now because of locks, replicas,
  budget, gates, or placement.
- `report`: write a condition, event, gate, or drift record without mutating the
  target resource.

Each action should record:

- resource URI
- diff that caused it
- precondition checked
- result
- error or backoff when it failed
- observed generation after action, when known

## Apply Semantics

`agent-team apply` should be the declarative write path for topology and related
resource specs.

### Validate Then Mutate

Apply must validate the entire submitted desired snapshot before mutating live
daemon state. Validation includes:

- TOML/schema validity
- normalized event names and trigger match keys
- unique resource names and URI-safe ids
- valid pipeline dependencies and gates
- budget, schedule, lock, and authority references
- runtime/workspace compatibility
- dynamic child deployment and template constraints
- capability and placement constraints when authority enforcement is enabled

If validation fails, apply returns `invalid` diffs and records an apply failure
event. It should not partially update the accepted desired snapshot.

### Idempotence

Applying the same desired state repeatedly must be a no-op after convergence.
The command may still return observed drift if the world has changed, but it
must not create duplicate jobs, duplicate instances, duplicate queue entries,
duplicate child deployments, or repeated restarts.

Idempotence requires stable resource identity:

- instance names identify instance specs
- pipeline/job/step ids identify work-unit specs
- workspace lease ids identify workspace materializations
- child deployment/charter URIs identify dynamic teams
- branch/path hashes are compatibility backfills, not preferred identity

### Additive By Default

Default apply behavior should be additive inside the selected scope:

- create newly declared resources
- update declared resources that already exist
- leave unmentioned observed resources alone
- never stop or prune a live resource simply because it is absent from an
  additive apply payload

This protects ad-hoc runs, in-flight workers, older dynamic teams, and operator
debug sessions from accidental deletion.

### Explicit Replace And Prune

Replace semantics must be explicit and scoped:

```sh
agent-team apply --replace --scope team:delivery
agent-team apply --prune --selector owner=topology
agent-team sync --stop-extras
```

Replace/prune may stop or remove resources only when all of these are true:

- the resource is inside the selected scope
- the daemon can prove ownership from metadata, job, charter, or topology
- retention/reap policy allows removal
- live work is not protected by a gate, lock, blocker, or hard safety check

For dynamic teams, replace should normally mark the child charter for reap
rather than deleting child state synchronously. Reap preserves evidence and
revokes live capability.

### Hot Apply

Some spec edits can converge without restarting an instance:

- budget headroom, reminder levels, and hard cutoff policy
- schedule intervals and `run_on_start` clocks
- queue limits, lock policy, and replica caps for future dispatch
- pipeline gate policy for not-yet-started steps
- authority policy in audit mode
- channel subscriptions and team ownership metadata
- PM write-back/project routing config that affects future events

Other edits require a rollout, restart, or fresh dispatch:

- agent prompt or skill bundle changes for an already-running runtime
- runtime kind or runtime binary changes
- workspace policy changes for a running instance
- env allowlist changes that affect already-launched process env
- authority changes in enforce mode that revoke a live capability
- state path or deployment identity changes

Apply should not silently restart long-lived managers for rollout-required
changes. It should report `spec_drift` with `restart_required`, then require an
operator policy such as `--rollout`, `restart`, or a topology restart policy to
actuate it.

## Continuous Convergence

Continuous reconcile is the daemon's periodic and event-driven convergence
loop. It should handle more than daemon startup:

- daemon restart adoption and crash-only cleanup
- persistent instance restart policy
- declared replicas for worker-like roles
- queue drain when locks, budgets, or replica capacity become available
- schedule hot-apply and due schedule firing
- budget extension or cutoff changes
- status transitions and stale/blocker detection
- dynamic child deployment spawn, health propagation, and reap
- workspace lease cleanup after merge or terminal failure
- desired/observed diff refresh for operator views

The loop should avoid tight retry storms. Failed actions should write a
condition and backoff until a bounded retry time. Backoff is per resource/action,
not a global daemon sleep.

Convergence does not mean every desired resource is always `Ready=True`.
Convergence means the controller has done everything policy permits and status
accurately explains the result: ready, progressing, blocked, degraded, backing
off, waiting for budget, waiting for gate, or failed validation.

## Relationship To Current Crash-Only Reconcile

`internal/daemon/reconcile.go` is the seed of this model.

Today:

- `Reconcile` lists metadata records and compares `status=running` PIDs to the
  process table.
- Live PIDs are adopted into the in-memory manager map.
- Missing running PIDs are marked `exited`, usage is captured, lifecycle events
  are appended, and terminal notifications fire.
- Stopped, exited, and crashed records are left alone.
- `ReconcileWithTopology` runs the crash-only pass, then applies declared
  restart policy for non-ephemeral topology instances.
- Persistent instances can be resumed when managed resume is supported; else the
  daemon can launch a fresh declared instance and apply restart backoff.

That behavior should remain the base layer. The broader controller adds:

- desired-state loading beyond restart policy
- URI-normalized desired/observed diffing
- structured action planning
- apply generation tracking
- additive vs replace/prune ownership rules
- hot-apply vs rollout-required classification
- dynamic-team child deployment reconciliation
- human-readable diff/status views

In other words, crash-only reconcile is not replaced. It becomes one controller
within the larger reconcile loop: the process-liveness controller for observed
instance status.

## Dynamic Teams

Dynamic teams rely on the same model.

`team.spawn` records desired child state: a charter, child deployment identity,
budget allocation, attenuated capability, template digest, rendered child
topology, workspace/state leases, and lifecycle/reap policy. It should not
synchronously run the whole team.

Reconcile actuates that desired state:

- materialize the child deployment
- start or resume the bootstrap manager
- dispatch child workers as child jobs become ready
- read child reports and gates through resource URIs
- propagate child health to the parent charter status
- enforce TTL, budget, capability expiry, and authority policy
- reap and tombstone the child when lifecycle policy says it is done

This keeps static teams, nested static deployments, runtime-created ephemeral
teams, and discovered placements on one primitive: desired resources reconciled
into observed resources under URI identity.

## Resource Model Interaction

The resource model makes reconcile safe across processes, containers, and
deployments:

- The controller keys desired and observed resources by `agt://` URI.
- Local paths are materialization fields with `path_scope = "host-local"`.
- Dispatch payloads and metadata carry both URI identity and local convenience
  paths.
- `/v1/resources?uri=...` is the read boundary for observed state.
- Capability tokens authorize verbs on resources, not arbitrary file paths.

The current `project` resource is the deployment self. A future first-class
`deployment` kind can be introduced without changing the reconcile invariant:
the URI authority is the deployment id, and the daemon owning that deployment is
the source of truth for status.

## Drift Policies

Reconcile should distinguish drift classes because they imply different
responses.

| Drift | Example | Policy |
| --- | --- | --- |
| Process drift | Metadata says running but PID is gone. | Mark exited/crashed, capture usage, apply restart policy. |
| Declaration drift | Desired instance missing from metadata. | Start/resume if persistent and policy allows. |
| Extra live resource | Running instance not declared in current scope. | Leave by default; stop/prune only with explicit replace/prune scope. |
| Spec drift | Runtime, prompt, workspace, budget, or authority differs from desired. | Hot-update when safe; otherwise report rollout required. |
| Status drift | Instance stale, blocked, unhealthy, or past budget. | Notify, cut off, restart, or backoff according to policy. |
| Topology invalid | New desired graph fails validation. | Reject apply; keep previous accepted desired snapshot. |
| Transport drift | Child deployment or remote resource unreadable. | Mark degraded, retry with backoff, maybe reap by policy. |

Drift policy should be declared close to the owning resource when possible. For
example, restart policy belongs on instances; reap policy belongs on charters or
workspace leases; merge/worktree cleanup policy belongs on jobs.

## Operator Surfaces

The design implies these user-facing surfaces, even if exact flags change:

```sh
agent-team apply [file] [--dry-run] [--replace] [--prune] [--scope ...]
agent-team plan [--action start|stop|prune|restart]
agent-team status --plan --resources --strict-topology
agent-team sync [--dry-run] [--stop-extras]
agent-team read agt://dep/instance/manager
agent-team daemon reconcile
```

Expected behavior:

- `apply --dry-run` validates and renders desired/observed diff without
  recording a new accepted desired snapshot.
- `apply` validates, records a new accepted desired snapshot, and either queues
  or runs reconcile.
- `plan` shows the action plan without mutation.
- `sync` is an operator convenience over reload/apply/reconcile for common local
  workflows.
- `daemon reconcile` remains a low-level action that reconciles the daemon's
  current accepted desired state.

## Failure Modes

| Failure | Behavior |
| --- | --- |
| Invalid desired state | Reject apply, keep the prior accepted snapshot, report invalid diffs. |
| Daemon restarts mid-apply | Atomic desired snapshot write means reconcile sees either old or new desired state, not a partial one. |
| Action succeeds but status refresh fails | Record action result, mark observation unknown/degraded, retry read with backoff. |
| Runtime crashes during rollout | Crash-only controller marks status, usage is captured, restart/backoff policy decides next action. |
| Dynamic child unreachable | Parent marks child degraded, preserves charter, retries or reaps according to lifecycle policy. |
| Prune target is ambiguous | Refuse prune unless ownership and scope are provable. |
| Budget or lock unavailable | Queue or block; do not busy-loop. |
| Capability revoked | Stop or isolate affected resources when policy requires, then report status. |

## Implementation Phasing

This design can land incrementally:

1. **Diff view.** Add desired/observed plan output over current topology and
   metadata without mutation.
2. **Apply snapshot.** Add validate-then-record semantics for accepted topology
   snapshots and invalid apply events.
3. **Hot apply.** Converge safe fields such as budgets, schedules, queues, and
   policy defaults without restarts.
4. **Rollout classification.** Report restart-required drift instead of
   silently restarting persistent instances.
5. **Continuous reconcile.** Run the loop periodically and on relevant daemon
   events, with per-resource backoff and conditions.
6. **Dynamic-team reconcile.** Treat charters and child deployments as desired
   resources and add spawn/reap convergence.
7. **Replace/prune.** Add scoped, ownership-checked pruning after plan/diff
   output has proven understandable.

The sequence keeps the current crash-only behavior intact while making the
declarative surface visible before it becomes destructive.

## Open Questions

- Should accepted desired snapshots be stored as rendered TOML, normalized JSON,
  or both?
- Should generation be per resource URI, per topology snapshot, or both?
- Which hot-apply fields are safe enough for v1, and which should report
  rollout-required until proven otherwise?
- Should `apply` be a top-level command only, or should `team apply` and
  `deployment apply` exist for scoped multi-deployment operation?
- How should remote child deployments report observed state when temporarily
  offline: cached last observation, explicit unknown condition, or both?
