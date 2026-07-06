# Embedded UI Design

Status: design draft for SQU-144. This document proposes no product-code,
package, or UI implementation changes.

## Decision Baseline

The UI is a same-repo static SPA embedded into the daemon binary with
`go:embed` and served from the daemon TCP listener. The boundary is the daemon
HTTP API, not a repository boundary. The frontend is a pure API consumer: it
does not import Go internals, read `.agent_team/` files, shell out to the CLI,
or learn local filesystem paths except as redacted materialization hints
returned by the daemon.

The browser client is an untrusted-input surface. Phase 1 should be read-only
observability. Mutation belongs in phase 2 after the loopback HTTP listener has
capability-token auth, token bootstrap, and per-verb authorization aligned with
the security model.

The UI is also the resource model's first broad external consumer. Every view
below maps its data needs to resource-model entities and fields. The gaps are
intentional design-review findings for SQU-128.

## Architecture

- `agent-team ui` starts or locates the daemon TCP listener and opens a
  localhost URL served by that listener.
- The daemon serves static assets from an embedded build output directory, for
  example `/ui/` and `/ui/assets/*`.
- API calls are same-origin requests to daemon routes under `/v1/` or a future
  resource API namespace. No SSR, no sidecar Node server, no direct file reads.
- The SPA treats canonical `agt://<deployment-id>/<kind>/<resource-id>` URIs as
  stable entity keys. Local aliases, paths, sockets, PIDs, and mount paths are
  displayed only as materialization hints.
- The daemon owns redaction and authorization. The UI should not contain
  provider keys, read `.env`, or infer secrets from logs.

Recommended stack for the implementation phase:

- Vite + TypeScript for build speed, typed API clients, and a small static
  output that can be embedded.
- Preact or vanilla TypeScript components, plain CSS, and browser-native
  routing. Avoid a component kit, global state library, query library, charting
  library, and CSS framework until a concrete view justifies each one.
- Generated or hand-maintained API types from daemon JSON contracts. The UI
  should fail at compile time when resource fields change.
- No Go runtime dependencies for the UI. JavaScript dependencies are build-time
  inputs, except the tiny UI runtime if Preact is chosen; the shipped Go binary
  serves static files.

This is consistent with the project's no-runtime-deps culture: the daemon does
not gain a new server, database, package manager at runtime, or deployment
surface. The dependency cost is paid at asset build time and reviewed in the
same repo as the API contract it consumes.

## Resource Field Conventions

The current resource draft defines URI shape and initial kinds more than full
schemas. The mappings below use these field conventions where the draft is
explicit, and name current supporting record fields where the UI would need the
daemon to expose them as resource fields.

- Every durable resource renders `uri`, `kind`, `deployment_id`, and
  `origin.project` when available.
- Materialized resources may render `local_path`, `socket`, `pid`, or `mount`
  only with `*_scope = "host-local"` or equivalent redaction metadata.
- Relationship fields use canonical URI references, for example `job_uri`,
  `instance_uri`, `workspace_uri`, `team_uri`, `pipeline_uri`, and
  `parent_deployment_uri`.
- Timestamps are UTC RFC3339 strings.

## Information Architecture

### Fleet Overview

Purpose: one screen for operator triage across daemon health, running work,
queue pressure, stuck gates, budget pressure, and schedule activity.

Renders these resource-model entities and fields:

| Entity | Fields |
| --- | --- |
| `project` / deployment self | `uri`, `deployment_id`, local registry alias, `transport`, `url`, `ready`, `daemon_pid`, `started_at`, `build`, optional `root_path` and `socket_path` as host-local hints |
| `instance` | `uri`, `name`, `agent`, `lifecycle_status`, `work_phase`, `status_description`, `last_action`, `status_since`, `last_activity_at`, `runtime`, `runtime_binary`, `pid`, `started_at`, `stopped_at`, `exited_at`, `exit_code`, `stale`, `runtime_stale` |
| `job` | `uri`, `id`, `ticket`, `ticket_url`, `pipeline`, `status`, `held`, `hold_reason`, `branch`, `pr`, `updated_at`, `usage.summary`, `last_event`, `last_status` |
| `pipeline_step` | `uri`, `job_uri`, `id`, `label`, `target`, `status`, `gate`, `approval_status`, `queued_at`, `running_at`, `started_at`, `finished_at`, `attempts`, `max_attempts` |
| `queue_item` | `uri`, `id`, `state`, `event_type`, `instance`, `instance_id`, `reason`, `locks`, `attempts`, `last_error`, `next_retry`, `queued_at`, `updated_at` |
| `lock` | `uri`, `name`, `scope`, `team`, `job`, `slots`, `used`, `available`, `holders[].instance`, `holders[].pid`, `holders[].acquired_at` |
| `channel` / mailbox summary | `uri`, `name`, `subscribers`, `message_count`, `last_message_ts`, unread counts per `mailbox` where available |
| `budget` | `uri`, `team`, `allocation`, `window_start`, `window_end`, `tokens_per_day`, `tokens_used`, `tokens_allocated`, `tokens_remaining`, `jobs_in_flight_cap`, `jobs_in_flight`, `jobs_available` |
| `schedule` | `uri`, `name`, `every`, `run_on_start`, `scope`, `payload.kind`, `last_seen_at`, `last_fired_at`, `next_due_at`, `due` |

Phase-1 UI behavior:

- Use compact summary bands for `health`, `runtime`, `jobs`, `queue`,
  `pipelines`, `budgets`, and `schedules`.
- Link every count to the filtered detail view that produced it.
- Prefer daemon resource fields over CLI-derived summaries. If a summary is
  still CLI-only, the API should expose the same data as a read endpoint before
  the UI depends on it.

SQU-128 findings:

- `queue_item`, `lock`, `budget`, `schedule`, and `pipeline_step` are not
  initial resource kinds in the draft.
- The draft has no canonical fleet summary resource or stable relationship
  schema for joining project, instance, job, pipeline, queue, and budget data.
- The current daemon exposes several read endpoints, but jobs, budgets, usage,
  and overview summaries are still mostly CLI/file-backed.

### Deployments

Purpose: show the operator which control planes exist, how they are reached,
which one is active, and how parent/child deployments relate.

Renders these resource-model entities and fields:

| Entity | Fields |
| --- | --- |
| `project` / deployment self | `uri`, `deployment_id`, display name, local alias, `build`, `ready`, `started_at`, `transport`, `url`, `token_file` or `secret_uri`, host-local `root` and `socket` hints |
| `route` | `uri`, `name`, `type`, `deployment_alias`, resolved `deployment_uri`, `transport`, `url`, `last_resolved_at`, `last_error` |
| `secret` | `uri`, `name`, `purpose`, `delivery` (`file` or `brokered`), `expires_at`, never the secret value |
| deployment relationship | `parent_deployment_uri`, `child_deployment_uri`, `relationship`, `placement`, `created_at`, `health_status` |

Nested-deployment rendering:

- Render deployments as a collapsible tree keyed by canonical deployment id.
- The root is the local daemon opened by `agent-team ui`.
- Child nodes represent container worker pools, remote daemons, or downstream
  deployments reached through routes.
- Each node shows health, transport, build, running/queued counts, and budget
  pressure for that deployment only.
- Aggregated fleet counts should be explicit rollups, not hidden merges. A
  parent row should show both local counts and subtree totals.
- If a child is known only by alias and has not handshaken, render it as
  unresolved and do not fabricate a canonical URI.

SQU-128 findings:

- The draft uses `project` as the deployment self resource but does not define a
  first-class `deployment` kind. The UI needs a deployment object separate from
  source-project metadata.
- Parent/child deployment edges are not represented.
- Registry fields such as alias, transport, URL, token file, root, socket,
  health, build, and last handshake are example TOML fields, not resource
  schema.
- Route resources do not currently carry enough live health or resolved-id
  metadata for a tree view.

### Teams

Purpose: show declared ownership: which instances, pipelines, schedules,
channels, budgets, and authority rules belong to each team.

Renders these resource-model entities and fields:

| Entity | Fields |
| --- | --- |
| `team` | `uri`, `name`, `description`, `instances[]`, `pipelines[]`, `schedules[]`, `channels[]` |
| `instance` declaration | `uri`, `name`, `agent`, `ephemeral`, `description`, `replicas`, `reap_worktree`, `restart`, `brief`, `token_budget`, `time_budget`, `hard_budget`, `env_allow`, `triggers[]` |
| `pipeline` | `uri`, `name`, `trigger.event`, `trigger.match`, `auto_advance`, `redispatch_on_reentry`, `reap_worktree`, `merge.strategy`, `merge.land`, `merge.owned_paths` |
| `schedule` | `uri`, `name`, `every`, `run_on_start`, `scope`, `payload` |
| `budget` | `uri`, `team`, `allocation`, `tokens_per_day`, `jobs_in_flight` plus live status fields from the budget view |
| `authority` | `uri`, `enforce`, `agents.*.allow`, `teams.*.allow` |

SQU-128 findings:

- `team`, `budget`, `pipeline`, `schedule`, and `authority` are not initial
  resource kinds.
- Topology declarations and runtime resources are mixed today. The UI needs to
  distinguish declared capacity from observed running instances.
- Team membership is represented by names in topology, not URI references.
- Authority is currently a policy blob, not addressable per rule or explainable
  per denied/allowed action.

### Instances

Purpose: inspect each declared or spawned runtime instance, including current
phase, lifecycle state, workspace, logs, mailbox, and budget notices.

Renders these resource-model entities and fields:

| Entity | Fields |
| --- | --- |
| `instance` declaration | `uri`, `name`, `agent`, `ephemeral`, `description`, `replicas`, `locks`, `reap_worktree`, `restart`, `brief`, `token_budget`, `time_budget`, `hard_budget`, `hard_multiplier`, `env_allow`, `config`, `triggers` |
| `instance` runtime metadata | `uri`, `name`, `agent`, `job_uri`, `ticket`, `branch`, `pr`, `origin`, `runtime`, `runtime_binary`, `workspace_uri`, `pid`, `session_id`, `started_at`, `runtime_budget`, `runtime_deadline`, `resume_count`, `fresh_fallback`, `stopped_at`, `exited_at`, `status`, `exit_code`, `usage`, `adopted`, `restart_backoff_until` |
| `state` | `uri`, `instance_uri`, `status.phase`, `status.description`, `status.since`, `status.last_action`, `work.job`, `work.ticket`, `work.pr`, `work.branch`, `blocking.reason`, `blocking.ask_to` |
| `workspace` | `uri`, `kind`, `repo_uri`, `branch`, `mount_path`, `path_scope`, `cleanup_policy`, `owning_job_uri`, `owning_instance_uri`, `status` |
| `mailbox` | `uri`, `instance_uri`, unread count, latest message timestamp, latest sender, latest message id |
| `log` | `uri`, `instance_uri`, `runtime`, `started_at`, `ended_at`, `redaction_status`, `tail_available` |

SQU-128 findings:

- The draft has `instance`, `state`, `workspace`, and `mailbox`, but not
  `runtime_run`, `session`, or `log` resources.
- Instance declaration and runtime instance share a name but have different
  fields and lifecycles; the resource model should name that split.
- Status fields live in `status.toml`; they need a daemon API schema before a
  browser can consume them without filesystem access.
- Workspace leases are only sketched. The UI needs `kind`, `repo_uri`, `branch`,
  `mount_path`, cleanup policy, owner, and status.
- Logs need redaction and sensitivity metadata before they are safe to expose in
  a browser.

### Jobs And Pipelines

Purpose: inspect durable work units, pipeline progress, gates, approvals,
review findings, PR links, events, and retry/hold state.

Renders these resource-model entities and fields:

| Entity | Fields |
| --- | --- |
| `job` | `uri`, `id`, `ticket`, `ticket_url`, `target`, `kickoff` redaction status, `kind`, `instance_uri`, `pipeline_uri`, `status`, `held`, `hold_reason`, `hold_until`, `branch`, `workspace_uri`, `reap_worktree`, `pr`, `origin`, `merge`, `drift`, `last_event`, `last_status`, `created_at`, `updated_at`, `usage`, `token_budget`, `time_budget`, `hard_budget`, `reminder_levels` |
| `pipeline` | `uri`, `name`, `trigger.event`, `trigger.match`, `auto_advance`, `redispatch_on_reentry`, `reap_worktree`, `merge`, `infra_signatures` |
| `pipeline_step` | `uri`, `job_uri`, `pipeline_uri`, `id`, `label`, `description`, `instructions` redaction status, `target`, `workspace`, `runtime`, `runtime_bin`, `status`, `instance_uri`, `after`, `gate`, `approval_required`, `approval_id`, `approval_status`, `optional`, `timeout`, `attempts`, `max_attempts`, `retry_on_crash`, `skipped`, `skip_reason`, `queue_reason`, `queued_at`, `running_at`, `started_at`, `finished_at`, `token_budget`, `time_budget`, `reminder_levels` |
| `gate` | `uri`, `job_uri`, `name`, `status`, `signature`, `log_ref`, `actor`, `ts`, `classification`, `matched_signature` |
| `approval` | `uri`, `job_uri`, `step_uri`, `status`, `requested_by`, `approved_by`, `reason`, `created_at`, `updated_at` |
| `event` | `uri`, `job_uri`, `type`, `action`, `reason`, `origin`, `ts`, `payload` redaction status |
| `workspace` | `uri`, `kind`, `branch`, `path_scope`, `cleanup_policy`, `status` |

SQU-128 findings:

- The draft treats a pipeline step as a possible `job` URI fragment. The UI
  needs steps as addressable resources because they have status, gates,
  approvals, attempts, instances, and actions.
- `pipeline`, `gate`, `approval`, and `event` are not initial resource kinds.
- PRs are plain URLs on jobs, not resources. That is acceptable for phase 1,
  but merge/land status will need either a `pr` resource or typed external-link
  fields.
- Kickoffs and step instructions may contain untrusted or sensitive text; the
  resource model needs redaction/sensitivity fields.
- Current pipeline summaries are CLI-derived. A pure API UI needs daemon read
  endpoints for jobs, steps, gates, events, and pipeline status rows.

### Budgets And Outcomes

Purpose: show spend, headroom, outstanding allocations, hard/soft notices, and
eventual outcome yield per team, theme, and pipeline.

Renders these resource-model entities and fields:

| Entity | Fields |
| --- | --- |
| `budget` | `uri`, `team_uri`, `allocation`, `window_start`, `window_end`, `tokens_per_day`, `tokens_used`, `tokens_allocated`, `tokens_remaining`, `tokens_exhausted`, `token_available_at`, `jobs_in_flight_cap`, `jobs_in_flight`, `jobs_available`, `jobs_exhausted`, `usage.summary` |
| `allocation` | `uri`, `team`, `parent`, `child`, `job_uri`, `step_uri`, `instance_uri`, `allocation`, `status`, `tokens`, `consumed_tokens`, `released_tokens`, `origin`, `created_at`, `released_at`, `updated_at` |
| `usage_record` | `uri`, `job_uri`, `instance_uri`, `agent`, `runtime`, `tokens_available`, `input_tokens`, `cached_input_tokens`, `output_tokens`, `reasoning_output_tokens`, `turns`, `duration_ms`, `started_at`, `ended_at`, `captured_at`, `source` redacted, `origin` |
| `outcome` | `uri`, `job_uri`, `team_uri`, `goal_uri`, `pipeline_uri`, `status`, `completed_at`, `pr_url`, `ticket_url`, `review_bounces`, `gate_failures`, `tokens_spent`, `duration_ms`, `yield_score` |
| `goal` | `uri`, `name`, `weight`, `owner_team_uri`, `theme`, `status` |

SQU-128 findings:

- `budget`, `allocation`, `usage_record`, `outcome`, and `goal` are not initial
  resource kinds.
- The outcomes ledger described by SQU-135 does not exist yet, so yield and
  value cannot be rendered without inventing data.
- Budgets are team-scoped, but teams are not resource-model entities.
- Usage source is still a log path in current records; the model needs a log URI
  and redacted source hint.
- Allocation parent/child strings are not URI references, which will make nested
  team or nested deployment rollups ambiguous.

### Org-Review Reports

Purpose: make scheduled quality and portfolio review legible: what evidence was
scanned, what findings were filed, what was dismissed, and what budget/team
changes were proposed.

Renders these resource-model entities and fields:

| Entity | Fields |
| --- | --- |
| `report` | `uri`, `kind`, `title`, `status`, `source_schedule_uri`, `job_uri`, `instance_uri`, `team_uri`, `window_start`, `window_end`, `created_at`, `summary`, `report_path` host-local hint |
| `finding` | `uri`, `report_uri`, `category`, `severity`, `evidence_refs[]`, `recommendation`, `ticket_url`, `status` |
| `feedback_item` | `uri`, `id`, `ts`, `category`, `body` redaction status, `status`, `fingerprint`, `context.instance`, `context.job`, `context.ticket`, `context.pipeline`, `context.step`, `context.runtime`, `context.build`, `origin`, `resolution` |
| `outcome` | `uri`, `goal_uri`, `team_uri`, `yield_score`, `tokens_spent`, `review_quality`, `completed_at` |
| `schedule` | `uri`, `name`, `payload.kind`, `last_fired_at`, `next_due_at` |

SQU-128 findings:

- `report`, `finding`, and `feedback_item` are not initial resource kinds.
- Auditor and harness-review logs are state files, not report resources.
- Org-review depends on outcomes and goals, which are not yet modeled.
- Evidence references need typed links to jobs, PR comments, gates, feedback
  items, events, and logs; the current draft has no generic evidence-ref shape.
- Report mutation is inherently privileged because it may file PM tickets or
  propose budget changes. Phase 1 should only render finalized report resources.

### Schedules And Loops

Purpose: show periodic automation, when it last ran, when it will run next,
what it dispatches, and whether its outputs are healthy.

Renders these resource-model entities and fields:

| Entity | Fields |
| --- | --- |
| `schedule` | `uri`, `name`, `every`, `run_on_start`, `scope`, `payload`, `last_seen_at`, `last_fired_at`, `next_due_at`, `due`, `team_uri` |
| `event` | `uri`, `type`, `payload`, `trace`, `origin`, `published_at`, `outcomes[]` |
| `event_outcome` | `uri`, `event_uri`, `instance`, `action`, `reason`, `job_uri`, `queue_item_uri`, `message_id`, `error` |
| `instance` | `uri`, `name`, `agent`, `ephemeral`, `description`, `triggers` matching the schedule payload |
| `pipeline` | `uri`, `name`, `trigger`, `steps` when a schedule starts a pipeline |
| `report` | `uri`, `kind`, `source_schedule_uri`, `status`, `summary`, `tickets_filed` for review loops |

SQU-128 findings:

- `schedule`, `event`, and `event_outcome` are not initial resource kinds.
- Schedule state is a daemon-local JSON record, not an API resource.
- Trigger matching is declarative topology, while fired events are runtime
  records; the model needs both and an edge between them.
- Loops such as `feedback-triage`, `debt-sweep`, `harness-review`,
  `discord-digest`, and `docs-freshness` produce different artifact types. The
  resource model needs a common `loop_run` or `report` shape rather than forcing
  each loop into a job-only view.

## Read-Only Phase 1

Phase 1 should make the fleet observable without letting a browser mutate it.

Allowed:

- Read daemon health and build identity.
- Read topology declarations, teams, pipelines, schedules, triggers, and
  budgets.
- Read instance lifecycle metadata and status summaries.
- Read jobs, pipeline steps, gates, approvals, queue items, locks, events,
  channels, mailbox summaries, usage records, budget status, and report
  summaries once daemon read APIs exist.
- Tail logs only through a daemon endpoint that applies redaction and access
  checks.
- Render host-local paths only as optional diagnostic hints.

Not allowed:

- Dispatch, start, stop, restart, interrupt, remove, merge, approve, reject,
  retry, drop, drain, publish, ack, reload topology, edit budgets, edit
  schedules, write tickets, or write PR comments.
- Read provider secrets, `.env`, launch-env secret values, or raw unredacted
  logs.
- Fall back to reading `.agent_team/` files from the browser or a companion
  server.

Phase-1 API shape should prefer resource reads over CLI-shaped summaries:

- `GET /v1/resources/project`
- `GET /v1/resources/deployments`
- `GET /v1/resources/topology`
- `GET /v1/resources/instances`
- `GET /v1/resources/jobs`
- `GET /v1/resources/pipelines`
- `GET /v1/resources/budgets`
- `GET /v1/resources/schedules`
- `GET /v1/resources/reports`

The exact route names can change, but the important requirement is that the UI
does not depend on local files or command output.

## Mutation Phase 2

Phase 2 can add actions after the auth plane exists.

Token and browser constraints:

- `agent-team ui` should mint a short-lived dashboard token or one-time URL and
  open the browser with that token in a way that avoids shell history and avoids
  persistent browser storage.
- The browser keeps the token in memory. Do not store it in localStorage.
- The token audience is the local deployment URI. It expires and can be revoked.
- Every mutating endpoint checks capability verbs and resource scopes.
- Provider credentials stay brokered by the daemon. The browser never receives
  Linear, GitHub, runtime, or cloud secrets.
- Mutating requests should include origin and build metadata so audit logs can
  distinguish browser actions from agent actions.

Candidate phase-2 actions and required verbs:

| Action | Verb | Resource scope |
| --- | --- | --- |
| Dispatch job or pipeline | `event.publish` / `instance.dispatch` | target deployment, pipeline, target instance |
| Retry or drop queue item | `queue.retry` / `queue.drop` | queue item |
| Drain queue or outbox | `queue.drain` / `outbox.drain` | deployment or team |
| Start, stop, restart, interrupt instance | `instance.start`, `instance.stop`, `instance.restart`, `instance.interrupt` | instance |
| Approve, reject, or skip a pipeline gate | `job.approve`, `job.reject`, `job.step.update` | job and step |
| Extend budget or runtime time | `job.extend`, `instance.extend` | job or instance |
| Edit budget declarations | `topology.budget.update` | team budget |
| Fire schedule now | `schedule.fire` | schedule |
| Publish or ack channel messages | `channel.publish`, `channel.ack` | channel and subscription |
| Reload topology | `topology.reload` | deployment |
| File ticket/comment through provider | `pm.ticket.create`, `pm.comment.create` | provider route and source report |

The browser should render disabled actions with the missing verb, not hide them,
so operators can see whether a capability problem or a system state problem is
blocking action.

## Consolidated SQU-128 Findings

These are the resource-model gaps exposed by the UI catalog.

| Finding | Affected views | Needed model change |
| --- | --- | --- |
| `project` and deployment identity are conflated. | Fleet Overview, Deployments | Add a first-class `deployment` resource or explicitly define `project` as the control-plane deployment self, with alias, transport, health, build, and registry fields. |
| Nested deployments have no relationship edge. | Deployments, Fleet Overview | Add parent/child deployment edge fields: `parent_deployment_uri`, `child_deployment_uri`, `relationship`, `placement`, and health rollup semantics. |
| URI kinds exist without field schemas. | All views | Define stable JSON field sets for each resource kind, including redaction and materialization metadata. |
| Teams are not resources. | Teams, Budgets, Org-Review | Add `team` with URI, description, ownership lists, and relationship edges to instances, pipelines, schedules, channels, budgets, and authority. |
| Pipelines and steps are under-modeled. | Jobs And Pipelines, Fleet Overview | Add `pipeline` and `pipeline_step` resources. A job URI fragment is not enough for step status, gates, approvals, attempts, and routing. |
| Gates and approvals are not resources. | Jobs And Pipelines | Add `gate` and `approval` resources with status, actor, timestamps, signatures, classification, and step/job references. |
| Schedules and events are not resources. | Schedules And Loops, Fleet Overview | Add `schedule`, `event`, and `event_outcome` resources with trigger, payload, due/last-fired state, and dispatch outcomes. |
| Budgets and allocations are not resources. | Budgets And Outcomes, Teams | Add `budget` and `allocation` resources and convert allocation parent/child strings to URI references. |
| Usage and logs lack resource identity. | Instances, Budgets And Outcomes | Add `usage_record`, `runtime_run`, and `log` resources; replace log-path identity with log URI plus redacted host-local source hints. |
| Outcomes and goals do not exist yet. | Budgets And Outcomes, Org-Review | Add `goal` and `outcome` resources for SQU-135/SQU-139 yield measurement. |
| Org-review reports are state files or comments. | Org-Review Reports | Add `report`, `finding`, and evidence-reference shapes that can point at jobs, gates, feedback, PRs, events, and logs. |
| Feedback items are not in the initial kinds. | Org-Review Reports, Fleet Overview | Add `feedback_item` or include it under a broader report/evidence model. |
| Queue, lock, outbox, and intake records are missing. | Fleet Overview, Schedules And Loops | Add `queue_item`, `lock`, `outbox_item`, and `intake_delivery` resources or explicitly classify them as daemon-private records with read projections. |
| Mailbox/channel subresources are incomplete. | Fleet Overview, Instances | Add message, subscription, unread cursor, and ack state fields for `mailbox` and `channel`. |
| Workspace leases are underspecified. | Instances, Jobs And Pipelines | Define `workspace.kind`, `repo_uri`, `branch`, `mount_path`, `path_scope`, `cleanup_policy`, owner fields, and status. |
| Capability tokens are not browser-shaped yet. | All mutation phase views | Extend the capability model with dashboard token bootstrap, read/write verbs, expiry, revocation, audience, and resource scopes suitable for a browser client. |
| Sensitivity/redaction is not a general field. | Logs, Jobs, Reports, Events | Add `sensitivity`, `redacted`, or `redaction_status` metadata to text-bearing resources such as logs, kickoffs, instructions, event payloads, feedback, and reports. |
| Declarative topology and runtime state are not separated. | Teams, Instances, Pipelines | Model declarations separately from observed runtime resources, then connect them with URI relationships. |

## Implementation Notes For The Future PR

- Keep the UI source in one directory, for example `web/` or `internal/ui/`, and
  embed only the built static assets into the daemon binary.
- Make `go test ./...` independent of frontend tooling unless a dedicated UI
  build tag or generation step is requested.
- Add API contract tests with small JSON fixtures before building views.
- Start with read endpoints and empty-state rendering. Do not add mutations
  until the tokenized TCP listener and capability enforcement are in place.
- Treat the UI as a design pressure test: when a view needs a field that only
  exists in CLI code or a local file, prefer adding the daemon resource read
  endpoint over teaching the browser a workaround.
