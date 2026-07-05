# Topology declaration (design sketch)

**Status**: design sketch, not yet built. Captures the v1.2+ model for declaring **which instances exist** and **when each gets called**. Companion to [`templates.md`](./templates.md) (authoring/distribution) and [`orchestrator.md`](./orchestrator.md) (lifecycle/runtime). The runtime side of this lands with the daemon; the schema side ships earlier in templates.

## What it is

`docker-compose` for agents. A repo declares its named instances and the events that route to each. The daemon resolves events to dispatches.

| Concern | Today | With topology |
|---|---|---|
| **Which instances exist** | Whatever the user has spawned ad-hoc via `agent-team run <agent> --name <name>`. No declarative source of truth. | Declared in `instances.toml`. `agent-team start` brings up the declared persistent instances. |
| **What each is configured for** | Single repo-wide `config.toml`. All instances of `ticket-manager` share the same Linear project. | Per-instance config overrides in `instances.toml`. Multiple `ticket-manager` instances can target different Linear projects. |
| **When each gets invoked** | Hand-written. User runs `agent-team run`, or one agent dispatches another via the `assign-worker` skill / Claude Code primitives. | Triggers declared in `instances.toml`. Daemon resolves user invocations, ticket webhooks, PR events, scheduled timers, channel messages, and inter-agent dispatches against the trigger table. |

## Why this model

Three concrete needs not met by the today-style ad-hoc model:

1. **Multiple instances of the same agent with different settings.** "Two `ticket-manager`s in one repo, one routing to Linear project A, one to project B." The motivating case from the design conversation. Today's repo-global `config.toml` doesn't support per-instance overrides.
2. **Predictable bring-up.** A new contributor cloning the repo wants `agent-team start` to start everything that should be running. Today they'd have to know to spawn each one by hand and remember the right flags.
3. **Event-driven dispatch.** Ticket webhooks, scheduled timers, PR events, channel publishes — all need to route to the right instance without a human in the loop. Today, dispatch is in-process: a manager spawns a worker via Claude Code's `Agent` tool. With the daemon and topology, dispatch becomes "any source of events → daemon → declared trigger → instance."

None of these are needed for the simplest "run a manager and chat" workflow — that path keeps working. Topology is the layer for users who outgrow ad-hoc spawning.

## Concepts

### Instance (declared)

A named runtime spawn of an agent, declared with config and triggers. Distinct from today's "instance" (which is just whatever `--name` you typed at `agent-team run`). After topology lands, a *declared* instance has:

- A canonical name (`manager`, `tm-platform`, `worker`).
- A reference to the agent template it runs.
- An `ephemeral` flag (true → spawn-on-demand, exit on completion; false → long-lived, brought up by `agent-team start`).
- Optional config overrides on top of the repo's `config.toml`.
- Zero or more triggers — events that should invoke this instance.

### Trigger

An event-matcher pair. Says "when an event of type X arriving with these properties, route it to this instance." The daemon owns the matching.

Event types in v1.2:

| Type | Source | Payload |
|---|---|---|
| `user_invocation` | `agent-team run <name>` from a human session | name, optional kickoff prompt |
| `agent.dispatch` | One instance dispatching another (e.g. manager → worker) via the orchestrator API | source instance, target name, kickoff |
| `ticket.created`, `ticket.updated`, `ticket.commented`, `ticket.status_changed` | PM intake | ticket fields (project, label, status, assignee) plus actor fields when the provider sends them |
| `pr.opened`, `pr.review_requested`, `pr.commented`, `pr.merged`, etc. | GitHub intake | PR metadata |
| `ticket_webhook`, `pr_webhook` | Legacy topology aliases | match the corresponding normalized intake family; `match.event` receives the suffix |
| `schedule` | Fixed-interval timer in the daemon | schedule name plus optional payload |
| `channel.message` | Subscribed channel receives a publish (see future channels work) | channel name, message body |

Each event source is its own ticket; current intake commands normalize provider
webhooks into these topology events before publishing them to the daemon.

## Schema (`instances.toml`)

Lives at the template root (defaults shipped by template authors) and at `.agent_team/instances.toml` (consumer overrides). Same layered model as `config.toml`: template default → repo override.

```toml
[instances.manager]
agent       = "manager"
ephemeral   = false
restart     = "on-failure"   # never (default), on-failure, or always
brief       = true           # generate/reinject recoverable-manager brief
description = "User-facing entry point. Coordinates ticket-managers and workers."

[[instances.manager.triggers]]
event = "user_invocation"
# match defaults to "any" — no filter

[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

[instances.tm-platform]
agent       = "ticket-manager"
ephemeral   = false
description = "Routes Platform-team tickets."

[instances.tm-platform.config.linear]
project_id  = "3d07030a-a372-41a2-b01e-1b4116d0f151"

[[instances.tm-platform.triggers]]
event   = "ticket_webhook"
match.project = "Platform"
match.event   = ["created", "updated"]   # list = OR

[instances.tm-mobile]
agent       = "ticket-manager"
ephemeral   = false

[instances.tm-mobile.config.linear]
project_id  = "50b6cd55-5760-4fd3-9bbe-acb17e544aa2"

[[instances.tm-mobile.triggers]]
event   = "ticket_webhook"
match.project = "Mobile"

[instances.worker]
agent     = "worker"
ephemeral = true        # spawn per dispatch
replicas  = 3            # max 3 concurrent
reap_worktree = "never"  # opt-in cleanup: never, on_close, or on_merge
token_budget = "40M"     # soft per-run allowance, default for dispatches
time_budget = "45m"      # soft per-run wall-clock allowance
# hard = true            # optional runaway cutoff at the allowance
# hard_multiplier = 1.5  # optional cutoff at allowance * multiplier
locks = ["build"]        # optional named dispatch locks held while spawned

[locks.build]
slots = 1                # default 1 = mutex; >1 = counting semaphore
scope = "machine"        # machine (default), team, or job

[channels.supervisor]
scope = "team"           # declared channel storage is namespaced per owner

[budgets]
reminder_levels = [50, 80, 100] # default soft budget notice thresholds

[budgets.delivery]
tokens_per_day = 200_000_000
allocation = "oversubscribe"    # default; "reserve" debits outstanding grants

[[instances.worker.triggers]]
event  = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"
trigger.match.status = "Ready for Agent"
redispatch_on_reentry = false
reap_worktree = "on_merge"
# land = "merge"                  # optional sibling form: squash, merge, or rebase

# Zero-config alternative:
# trigger.event = "ticket.created"
# remove trigger.match.status

[pipelines.ticket_to_pr.merge]
strategy = "squash"              # squash, rebase, or script
land = "merge"                   # optional final PR landing mode; default squash
# script = ".agent_team/scripts/union-merge.sh"   # required when strategy = "script"
# owned_paths = ["coverage/baselines", "coverage/counts.json"]

[pipelines.ticket_to_pr.infra_signatures]
fixture_reaped = 'Os \{ code: 2, kind: NotFound'
missing_deps = 'deps/[^ ]*: No such file'

[[pipelines.ticket_to_pr.steps]]
id     = "implement"
label  = "Implementation"
instructions = "Implement the ticket with tests and summarize the branch state."
target = "worker"
workspace = "worktree"
runtime = "codex"
token_budget = "40M"
time_budget = "45m"
# hard_multiplier = 1.5
reminder_levels = [50, 80, 100]
locks = ["build"]

[[pipelines.ticket_to_pr.steps]]
id     = "review"
label  = "Review"
description = "Check the branch and request PR follow-up."
target = "manager"
workspace = "repo"
runtime = "claude"
after  = ["implement"]
gate   = "manual"
max_attempts = 2
retry_on_crash = true

[schedules.nightly]
every = "24h"
scope = "team"
payload.workspace = "repo"

[teams.delivery]
description = "Default software-delivery team."
instances   = ["manager", "tm-platform", "tm-mobile", "worker"]
pipelines   = ["ticket_to_pr"]
schedules   = ["nightly"]
channels    = ["supervisor"]

[authority]
enforce = false           # phase 1: audit-only, log violations without blocking

[authority.agents.worker]
allow = ["inbox.send", "channel.*", "job.gate.*:own", "feedback.deliver"]

[authority.agents.manager]
allow = ["*"]
```

### Lock field reference

Locks live under `[locks.<name>]`. A declared instance or pipeline step can
reference one or more names with `locks = ["name"]`. Ephemeral dispatches acquire
all required slots before spawn and release them when the spawned instance exits.
If any slot is unavailable, the dispatch is written to the normal pending queue
with `reason = "lock_held"` and later `tick`, `drain`, or `queue retry` attempts
the same dispatch again.

| Field | Required | Default | Meaning |
|---|---|---|---|
| `slots` | no | `1` | Number of concurrent holders allowed for the named lock. `1` is a mutex; values above one are counting semaphores. |
| `scope` | no | `machine` | Storage namespace for this lock: `machine` keeps the historical flat key, `team` prefixes the lock with the origin team, and `job` prefixes it with the origin job id. |

Use `agent-team locks` to inspect declared slots and current holders, and
`agent-team queue ls --reason lock_held` to inspect queued lock contention.

### Channel field reference

Channels live under `[channels.<name>]`. Undeclared channels continue to work
with machine-scoped storage. Declarations are needed only when a channel should
be namespaced.

| Field | Required | Default | Meaning |
|---|---|---|---|
| `scope` | no | `machine` | Storage namespace for this channel: `machine` keeps the historical `#name`, `team` stores messages/cursors under the owning topology team, and `job` stores them under the actor job id. |

Channel reads remain open; the scope controls which storage key a declared
channel write/read maps to, while authority allowlists audit write verbs.

### Instance field reference

| Field | Required | Default | Meaning |
|---|---|---|---|
| `agent` | yes | — | Agent template directory under `.agent_team/agents/`. Must exist after `init`. |
| `ephemeral` | no | `false` | If `true`, spawn-on-trigger and exit on completion. If `false`, brought up by `agent-team start`, runs until stopped. |
| `restart` | no | `never` | Reconcile relaunch policy for persistent instances: `never`, `on-failure`, or `always`. |
| `brief` | no | persistent: `true`; ephemeral: `false` | Generate `<state-dir>/brief.md` and inject it into fresh launches / managed resumes. Set `false` to opt a persistent instance out. |
| `description` | no | empty | Human-readable. Shown in `instance ps`. |
| `config.<dotted.key>` | no | — | Override values for the resolved per-instance config (layers between repo and CLI flags). Same dotted-key syntax as parameter declarations in `template.toml`. |
| `locks` | no | empty | Named dispatch locks this instance's ephemeral children hold until exit. References must exist under `[locks]`. |
| `replicas` | no | `1` | Max concurrent runs. Ephemeral only — for persistent, this is implicitly 1. |
| `reap_worktree` | no | `never` | Opt-in cleanup policy for job-owned worker worktrees created by this instance. Supported values: `"never"`, `"on_close"`, or `"on_merge"`. |
| `token_budget` | no | empty | Soft per-run token allowance applied to dispatches for this instance when the payload or pipeline step does not provide one. Integers and decimal suffix strings such as `"40M"` are accepted. |
| `time_budget` | no | empty | Soft per-run wall-clock allowance applied to dispatches for this instance when the payload or pipeline step does not provide one. This is visibility unless `hard` or `hard_multiplier` is set; use step `timeout` for routine wall-clock watchdog cutoffs. |
| `hard` | no | `false` | Opt into a hard cutoff at the declared token/time allowance. Crossing it records `budget_exceeded_hard`, marks the runtime crashed, and terminates it with watchdog semantics. Use for runaway protection, not normal pacing. |
| `hard_multiplier` | no | empty | Opt into a hard cutoff at allowance multiplied by this number, for example `1.5`. Must be at least `1`. A multiplier is useful when soft notices should remain the primary control and only larger overruns should be killed. |
| `env_allow` | no | unset | Glob allowlist for child process env keys. When unset, launch env behavior is unchanged. When set, only matching keys plus daemon-required `AGENT_TEAM_*` keys are inherited. |
| `triggers` | no | empty | List of trigger blocks. Empty triggers list → instance only invokable by explicit `agent-team run <name>`. |

### Pipeline field reference

Pipelines live under `[pipelines.<name>]`. A pipeline trigger creates or updates a durable job, and each `[[pipelines.<name>.steps]]` entry becomes a stored job step.

| Field | Required | Default | Meaning |
|---|---|---|---|
| `trigger.event` | yes | — | Event type that creates or updates a pipeline job. |
| `trigger.match.<key>` | no | — | Payload filters using the same match syntax as instance triggers. The bundled PM-backed pipeline uses `trigger.match.status = "<provider.agent_column>"`, so dragging a card/item into that column is the dispatch gesture. |
| `redispatch_on_reentry` | no | `false` | Controls what happens when a matching trigger arrives for a ticket whose durable job already exists. Non-terminal jobs always no-op with an audit event. Terminal jobs no-op by default; set true to reopen and dispatch the first ready step again. |
| `reap_worktree` | no | `never` | Opt-in cleanup policy for job-owned worker worktrees created by this pipeline. Pipeline policy takes precedence over the target instance policy. |
| `land` | no | `"squash"` | Final GitHub PR landing mode for jobs created by this pipeline. Supported values: `"squash"`, `"merge"`, or `"rebase"`. This is equivalent to `merge.land`; declare only one form unless both values match. |
| `merge.strategy` | no | — | Mechanical merge strategy for pipeline jobs. Supported values: `"squash"`, `"rebase"`, or `"script"`. When present, `agent-team job merge <job-id>` applies this strategy and records the outcome on the job. |
| `merge.land` | no | `"squash"` | Final GitHub PR landing mode used by `agent-team job merge <job-id>` when the job has a recorded PR. Supported values: `"squash"`, `"merge"`, or `"rebase"`. This controls the final `gh pr merge` flag and is orthogonal to `merge.strategy`. |
| `merge.script` | for `script` | — | Repo-relative or absolute executable path for custom merge mechanics. The script receives three positional arguments: base branch, head branch, and worktree path. A nonzero exit blocks the merge. |
| `merge.owned_paths` | no | empty | Repo-relative path prefixes or glob patterns owned by the merge strategy. If every changed file between base and head matches these paths, `job merge --dry-run` and `job merge` surface `drift: reconcilable` on the job/result; otherwise drift is `unclassified`. |
| `infra_signatures.<name>` | no | empty | Regex used to classify explicit failed job gate signatures as infrastructure failures. Failed gates that do not match are classified as content failures. Signatures classify only; agents still report pass/fail explicitly with `agent-team job gate set`. Anchor these to stable error shapes, not broad keywords: `fixture_reaped = 'Os \{ code: 2, kind: NotFound'` is useful evidence; `fixture_reaped = 'NotFound'` is too broad and can hide content failures that merely assert an error string. |
| `steps[].id` | yes | — | Unique step identifier within the pipeline. |
| `steps[].label` | no | empty | Human-readable step name for CLI, graph, and job diagnostics. The stable `id` is still used for commands. |
| `steps[].description` | no | empty | Longer human-readable step note copied into durable job step snapshots. |
| `steps[].instructions` | no | empty | Step-specific runtime instructions appended to the job kickoff when this step dispatches. |
| `steps[].target` | yes | — | Dispatch target. The target should resolve through an `agent.dispatch` trigger. |
| `steps[].locks` | no | empty | Additional named dispatch locks held for this pipeline step's spawned instance. They are unioned with locks declared on the target instance. |
| `steps[].workspace` | no | auto | Dispatch workspace default for this stage. Supported values: `"auto"`, `"worktree"`, or `"repo"`. Operator `--workspace` flags override it. |
| `steps[].runtime` | no | repo default | Dispatch runtime default for this stage. Supported values: `"claude"` or `"codex"`. Operator `--runtime` flags override it. |
| `steps[].runtime_bin` | no | runtime default | Runtime binary or wrapper default for this stage. Operator `--runtime-bin` flags override it. |
| `steps[].after` | no | empty | Step dependency or list of dependencies. All referenced steps must be done before this step is ready. |
| `steps[].gate` | no | empty | Set to `"manual"` to require operator approval before the step can dispatch, even after dependencies are done. Approve with `agent-team job step <job-id> <step-id> --status queued`. |
| `steps[].approval_required` | no | `false` | Only valid with `gate = "manual"`. When true, the step must be linked to a first-class job approval request and that approval must be approved before the step can advance. Existing manual gates keep the old `job approve` behavior unless they opt in. |
| `steps[].optional` | no | `false` | If `true`, a failed step does not block downstream dependencies. |
| `steps[].timeout` | no | empty | Duration string used by stale-step timeout commands before falling back to repo stale-job thresholds. |
| `steps[].token_budget` | no | target instance default | Soft token allowance for this step's runtime. In `oversubscribe` budget mode it is clamped to remaining consumed team headroom at dispatch; in `reserve` mode the full requested allowance must fit consumed + outstanding headroom or the dispatch queues. The granted value is exported as `AGENT_TEAM_BUDGET_TOKENS`. |
| `steps[].time_budget` | no | target instance default | Soft wall-clock allowance for this step's runtime, exported as `AGENT_TEAM_BUDGET_TIME`. This does not kill the runtime unless hard cutoffs are enabled. |
| `steps[].hard` | no | target/job default | Opt into a hard cutoff at the step's token/time allowance. Crossing it records `budget_exceeded_hard`, crash-finalizes the instance, frees its slot, and lets normal failure attention/write-back run. |
| `steps[].hard_multiplier` | no | target/job default | Opt into a hard cutoff at allowance multiplied by this number. Must be at least `1`; use values above `1` for runaway protection while preserving soft notice workflow. |
| `steps[].reminder_levels` | no | `[budgets].reminder_levels` or `[50, 80, 100]` | Percentage thresholds that create `budget_notice` job events and mailbox messages when live usage crosses them. Job-level reminder levels override this at dispatch. |
| `steps[].max_attempts` | no | unlimited | Positive integer cap for dispatch attempts. Retry commands skip failed steps once the stored attempt count reaches this value. |
| `steps[].retry_on_crash` | no | `false` | If `true`, daemon auto-advance may retry this step once after an instance crash/nonzero exit, but only when that instance recorded no job gate/verdict. Use only for read-only/idempotent steps such as reviewers; implementation steps should leave this false to avoid duplicate PR side effects. |

Operators can intentionally bypass a stored step with `agent-team job step <job-id> <step-id> --skip`. The job records `status = "done"` and `skipped = true` on that step, so later `after` dependencies treat it as terminal while `job show` still surfaces the bypass.

Use `agent-team signatures test <pipeline> --against <log-file>` before trusting a new `[pipelines.<name>.infra_signatures]` entry. The dry run prints every configured signature as match/no-match and includes the matched excerpt so broad patterns are visible before they start classifying failed gates as infra.

`agent-team job merge <job-id>` is a merge-only operator action. It does not dispatch another agent, rerun gates, or retry failed stages. Jobs with a recorded PR merge through `gh pr merge <pr> --squash`, `--merge`, or `--rebase` according to `merge.land` (or `land` on the pipeline); omitted land defaults to `squash`. Use `agent-team job merge <job-id> --land merge` for a one-off apply override, or `agent-team job update <job-id> --land merge` to persist a per-job override before the merge gate. Jobs without a recorded PR are refused unless the operator passes `--branch <head>`, in which case branch-local squash/rebase mechanics are applied in the selected worktree from `merge.strategy`. Script strategy executes the configured script with `(base, head, worktree)` and records a blocked merge if it exits nonzero or leaves tracked files dirty.

### PM board dispatch

The bundled PM-backed default treats the board column as the control plane:

```toml
[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"
trigger.match.status = "Ready for Agent" # usually the provider agent_column
redispatch_on_reentry = false
```

`agent-team intake linear` normalizes Linear state-change webhooks into
`ticket.status_changed` and stores the destination column in `payload.status`.
`agent-team intake github` normalizes GitHub Projects status edits the same way.
Moving a card or project item into the configured column dispatches the
pipeline; moving it to any other column does not match.

Loop protection is actor based. For Linear, if `[linear].agent_user_id` is set,
or `.agent_team/state/linear/viewer.json` contains a cached `viewer.id`, Linear
status-change events authored by that user are ignored before topology
matching. For GitHub, `[github].agent_login`, `[github].agent_id`, or
`.agent_team/state/github/viewer.json` provides the actor identity used to
ignore self-authored project status moves. This prevents agent-driven
write-backs from dispatching new workers.

Re-entry is intentionally idempotent. If a matching status-change arrives for a
ticket with an existing queued, running, or blocked job, the daemon records a
`pipeline_reentry_noop` audit event and dispatches nothing. Existing terminal
jobs also no-op unless `redispatch_on_reentry = true`, in which case the daemon
records a `reopened` audit event, resets the first step, and dispatches it.

`agent-team job merge <job-id>` is a merge-only operator action. It does not dispatch another agent, rerun gates, or retry failed stages. Jobs with a recorded PR merge through `gh pr merge <pr> --squash` or `--rebase` for the built-in strategies. Jobs without a recorded PR are refused unless the operator passes `--branch <head>`, in which case branch-local squash/rebase mechanics are applied in the selected worktree. Script strategy executes the configured script with `(base, head, worktree)` and records a blocked merge if it exits nonzero or leaves tracked files dirty.

When an advance dispatch targets a persistent instance, `agent-team` only records the step as `running` if the daemon can message a live instance. If the persistent target is stopped or reconciles as stale, the daemon appends the dispatch payload to that instance mailbox and returns a queued outcome; the durable job step stays `queued` with the persistent instance name until the instance is started and drains its inbox. Manual edits that mark a step `running` also require `--instance` unless `--force` is supplied, which keeps ownerless running stages out of the normal operator path.

### Per-agent runtime default

An agent can declare its own default runtime in `agent.md` frontmatter, so the
runtime travels with the agent definition instead of being threaded through
every dispatch:

```yaml
---
name: worker
description: ...
runtime: codex          # this agent's instances default to the Codex runtime
runtime_bin: codex      # optional explicit binary / wrapper
---
```

This is what lets one team run, e.g., the `manager` on Claude Code while
`worker` and `ticket-manager` run on Codex — declared once per agent rather than
remembered on each `assign-worker` dispatch. The daemon resolves a dispatched
instance's runtime with this precedence (highest first):

```
1. Explicit dispatch runtime   (CLI --runtime / --runtime-bin, pipeline step runtime, dispatch payload)
2. AGENT_TEAM_RUNTIME env override
3. Agent frontmatter            (runtime: / runtime_bin:)   ← NEW
4. Repo [runtime] config        (.agent_team/config.toml)
5. Built-in default             (claude)
```

A static per-agent default is intentionally outranked by both an explicit
dispatch runtime and a deliberate env override, so operators can still force a
runtime for a one-off run or an incident without editing agent files. Mixed
runtimes require the daemon: in no-daemon interactive mode every subagent
inherits the launching agent's runtime.

### Schedule field reference

Schedules live under `[schedules.<name>]`. They publish a `schedule` event with payload `source = "schedule"` and `name = "<name>"`; keys under `[schedules.<name>.payload]` are merged into that payload.

| Field | Required | Default | Meaning |
|---|---|---|---|
| `every` | yes | — | Go duration string such as `15m`, `1h`, or `24h`. |
| `run_on_start` | no | `false` | If true, publish once when the daemon scheduler starts, then follow `every`. |
| `scope` | no | `machine` | Storage namespace for the schedule clock. `team` uses the owning `[teams.<name>].schedules` entry; the emitted event payload still uses the declared schedule name. |
| `payload.<key>` | no | — | Extra payload keys used by trigger matches or downstream agents. |

Example usage digest schedule:

```toml
[schedules.usage-digest]
every = "168h" # weekly

[schedules.usage-digest.payload]
kind = "usage_digest"
since = "7d"
channel = "#ops"

[instances.usage-reporter]
agent = "manager"
ephemeral = true

[[instances.usage-reporter.triggers]]
event = "schedule"
match.name = "usage-digest"
match.kind = "usage_digest"
```

The reporting agent can run `agent-team usage --since 7d --by job` plus any
agent/runtime rollups it needs, then post a compact summary to the configured
channel or ticket comment. This is a scheduled reporting hook, not a nested
sub-team.

### Team field reference

Teams live under `[teams.<name>]`. They group declared instances, pipelines, and schedules into an operator-facing ownership unit for commands such as `agent-team team status <name>` and `agent-team team tick <name>`.

| Field | Required | Default | Meaning |
|---|---|---|---|
| `description` | no | empty | Human-readable purpose for the team. |
| `instances` | no | empty | Declared instance names owned by the team. References must exist under `[instances]`. |
| `pipelines` | no | empty | Declared pipeline names owned by the team. References must exist under `[pipelines]`. |
| `schedules` | no | empty | Declared schedule names owned by the team. References must exist under `[schedules]`. |
| `channels` | no | empty | Declared channel names owned by the team. References must exist under `[channels]`. |

At least one of `instances`, `pipelines`, `schedules`, or `channels` is required.

### Budget field reference

Budgets live under `[budgets.<team>]`; `<team>` must match a declared team.
They gate dispatch admission and are also rendered by `agent-team budget status`.

| Field | Required | Default | Meaning |
|---|---|---|---|
| `tokens_per_day` | no | disabled | Sliding 24-hour token cap from finalized usage records. |
| `jobs_in_flight` | no | disabled | Max active jobs attributed to this team. Later steps of the same job do not block themselves. |
| `allocation` | no | `oversubscribe` | Token allowance semantics: `oversubscribe` preserves phase-1 behavior, gating on consumed tokens and clamping dispatch allowances to consumed headroom while still showing outstanding allocated promises; `reserve` records each granted allowance as an outstanding child allocation and gates on consumed + outstanding + requested so the tree invariant is enforced. |

`agent-team budget status` shows `TOKENS` (consumed finalized usage) and
`ALLOCATED` (outstanding child allowances). In `reserve` mode, token remaining
subtracts both; in `oversubscribe` mode, remaining subtracts consumed usage only,
so `ALLOCATED` may exceed the cap by design.

### Authority field reference

Authority policy lives under `[authority]`. Phase 1 is audit-only: when a
configured allowlist does not include a daemon write verb, the daemon appends
`authority_violation` lifecycle and job events and still performs the request.
Those job events appear in `agent-team job triage` with the
`authority_violation` reason. `enforce` is parsed for the future flag flip but
defaults to false.

```toml
[authority]
enforce = false

[authority.agents.worker]
allow = ["inbox.send", "channel.*", "job.gate.*:own", "feedback.deliver"]

[authority.teams.platform]
allow = ["event.publish", "queue.*"]
```

Allow entries are exact verbs (`inbox.send`) or prefix wildcards (`queue.*`);
`*` allows every audited verb. Job-targeting entries can add `:own`, such as
`job.gate.*:own`, to match only when the target job id equals the caller's
origin job (`AGENT_TEAM_ORIGIN_JOB`, falling back to `AGENT_TEAM_JOB_ID`).
Unqualified entries match any target job. Per-agent and per-team rules are
additive: a write is considered allowed if either rule matches. Reads are
intentionally not audited or blocked so cross-team inspection remains possible.

### Origin envelope and project id

`agent-team init` writes a stable `[project].id` into `.agent_team/config.toml`. Existing repos are backfilled by `agent-team doctor --fix` or the first daemon start. The id is local to the vendored team and is used to identify the source deployment when jobs or agent-written PM artifacts cross repo boundaries.

The daemon stamps a fixed origin envelope on resources it creates or updates:

```
project = "<[project].id>"
team = "<topology team>"
instance = "<runtime instance>"
agent = "<agent name>"
job = "<durable job id>"
trigger = "<event, schedule:name, or pipeline:name:step>"
build = "<binary build identity>"
```

The envelope is persisted on jobs, job events, queue items, lock leases, lifecycle metadata, and usage records. Team ownership is resolved from the declared topology: pipeline-created jobs use `[teams.*].pipelines`, spawned instances use `[teams.*].instances`, and unique ephemeral names match their declared instance prefix.

Operator queries can use the envelope directly:

```
agent-team job ls --team platform
agent-team job ls --trigger schedule:feedback-triage
agent-team ps --team platform
agent-team usage --by team
```

PM writes append a machine-readable footer such as:

```
agent-team-origin: project=<id> team=platform instance=platform-worker-squ-90 trigger=pipeline:platform_ticket_to_pr:implement
```

### Trigger field reference

| Field | Required | Meaning |
|---|---|---|
| `event` | yes | Event type from the table above. |
| `match.<key>` | no | Filter on payload keys. Single value = exact match; list = OR-of-values. Multiple `match.<key>` entries = AND across keys. |

### Match-expression scope (v1.2)

Match expressions are intentionally limited to a small DSL:
- Single value: `match.project = "Platform"` → exact equality.
- List: `match.project = ["Platform", "Infra"]` → membership.
- Multiple keys AND: `match.project = "Platform"` + `match.label = "bug"` → both must hold.

No regex, no boolean operators across keys, no negation in v1.2. If users need richer matching, they declare multiple instances with overlapping triggers (the daemon dispatches to all matching instances).

## Layered config resolution chain

`templates.md` defines a four-layer chain for parameter resolution. Topology adds the **per-instance declared config** layer:

```
1. CLI flags                              (--set linear.project_id=<x>)
2. Per-instance config file               (.agent_team/state/<instance>/config.toml)
3. Per-instance declared overrides        (instances.toml [instances.<name>.config])  ← NEW
4. Repo config                            (.agent_team/config.toml)
5. Template defaults                      (template.toml [[parameter]] defaults)
```

The new layer (#3) sits between the repo config and per-instance state file: the **declared** override is what the template/repo author intends; the per-instance state file is the per-runtime opportunity to override further (e.g. by `agent-team run --set` flags persisting their values).

In practice, declared overrides and state files rarely conflict — declared overrides set the topology-time intent, state files capture runtime tweaks.

## CLI surface additions

```
agent-team start [<name>...] [--agent <agent>] [--status <status>] [--phase <phase>] [--stale] [--unhealthy] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--wait --timeout <duration>] [--attach --tail N|all]
    Start the daemon if needed, then bring up declared persistent instances.
    With no args: all non-ephemeral declared instances. Idempotent —
    already-running instances are left alone. With explicit names: resume a
    daemon-known instance by name, or start a declared persistent instance.
    With --agent: start/resume declared persistent and daemon-known instances
    for that agent. With --status: limit set selections to running, stopped,
    exited, crashed, or unknown lifecycle status. With --phase: limit set
    selections to planning, implementing, awaiting_review, blocked, idle,
    done, or unknown work phase. With --stale: target only non-idle work
    whose status.toml has not been updated past the stale threshold.
    With --wait on a scoped selection, health waits on the selected instance
    names while still checking daemon readiness globally.
    With --summary: render aggregate action/status counts instead of
    per-instance rows. With --format: render each action result with a Go
    template. With --attach: follow the selected instance log after
    start/resume; exactly one selected instance is required.

agent-team instance up [<name>...] [--latest | --last N] [--agent <agent>] [--status <status>] [--phase <phase>] [--stale] [--unhealthy] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--wait --timeout <duration>] [--attach --tail N|all]
    Lower-level equivalent when the daemon is already running.

agent-team instance down [<name>...] [--latest | --last N] [--agent <agent>] [--status <status>] [--phase <phase>] [--stale] [--unhealthy] [--rm] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--wait --wait-timeout <duration>] [--timeout <duration>] [--json]
    Gracefully stop declared persistent instances. With no args: all running.
    State is preserved by default; --agent, --status, --phase, --stale,
    and --unhealthy
    narrow the selection; --rm explicitly removes selected instance state and
    daemon metadata after stopping. With --wait, confirm selected
    instances reach a terminal state; --wait-timeout controls that deadline.
    If --wait-timeout is omitted, --timeout remains the backward-compatible
    wait deadline.

agent-team stop [<name>...] [--all] [--agent <agent>] [--status <status>] [--phase <phase>] [--stale] [--unhealthy] [--rm] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--wait --wait-timeout <duration>] [--timeout <duration>]
    Top-level equivalent. With no args: running declared persistent instances.
    With --all: every daemon-managed running instance, including ad-hoc and
    ephemeral work. With --rm: remove selected state and daemon metadata after
    stopping.

agent-team kill [<name>...] [--all] [--agent <agent>] [--status <status>] [--phase <phase>] [--stale] [--unhealthy] [--rm] [--dry-run] [--summary] [--wait --wait-timeout <duration>] [--timeout <duration>] [--format '{{.Instance}} {{.Action}}']
    Force-stop equivalent. It follows stop's target selection, but asks the
    daemon to escalate to SIGKILL after --timeout. With --wait, confirm the
    selected instances reach a terminal state before returning. If
    --wait-timeout is omitted, --timeout remains the backward-compatible wait
    deadline.

agent-team restart [<name>...] [--agent <agent>] [--status <status>] [--phase <phase>] [--stale] [--unhealthy] [-f] [--dry-run] [--summary] [--format '{{.Instance}} {{.Action}}'] [--wait --wait-timeout <duration>] [--attach --tail N|all]
    Stop then resume declared persistent instances. With no args: all
    non-ephemeral declared instances. With explicit names: restart a
    daemon-known instance by name, or start a declared persistent instance if
    it has no daemon metadata yet. With --agent: restart/resume declared
    persistent and daemon-known instances for that agent. With --status: limit
    set selections to running, stopped, exited, crashed, or unknown lifecycle
    status. With --phase: limit selections by reported work phase. With
    --stale: limit recovery to stale non-idle work. With --wait on a scoped
    selection, health waits on the selected instance names while still
    checking daemon readiness globally. With -f:
    escalate to SIGKILL if a running instance does not stop within --timeout.
    With --summary: render aggregate action/status counts instead of
    per-instance rows. With --format: render each action result with a Go
    template. With --attach: follow the selected instance log after restart;
    exactly one selected instance is required.

agent-team reload [--json]
agent-team topology reload [--format '{{len .Instances}}'] [--json]
    Top-level operator command: re-read instances.toml in the running daemon,
    then reconcile daemon metadata against the live process table. It does not
    start newly-declared instances or stop undeclared running work.

agent-team plan [--json] [--summary] [--stop-extras] [--agent <agent>] [--instance <name>] [--status <status>] [--phase <phase>] [--action <action>]
    Read-only desired-state preview. Shows persistent declarations that would
    start or resume, running instances that would be kept, ephemeral
    declarations that stay on-demand, and daemon-known extra instances not
    declared in topology. With --stop-extras, running extra instances are
    shown as stop actions, matching `agent-team sync --stop-extras`; running
    children of declared ephemeral instances (for example `worker-<id>`) are
    shown as kept ephemeral work. Filters narrow the displayed rows by agent,
    instance, lifecycle status, reported work phase, or planned action.
    With --summary, plan renders aggregate action/status counts instead of
    per-instance rows.

agent-team sync [--dry-run] [--stop-extras] [--agent <agent>] [--instance <name>] [--status <status>] [--phase <phase>] [--action <action>] [--summary] [--wait --timeout <duration>] [--json]
    Apply the safe subset of the plan: reload topology in the daemon,
    reconcile metadata, then start/resume declared persistent instances.
    Filters narrow the plan rows sync will converge, so operators can apply
    desired state to one agent, instance, lifecycle status, work phase, or
    planned action at a time.
    With --summary, sync renders aggregate action/status counts instead of
    per-instance rows; with --dry-run, the counts summarize the filtered plan.
    With --wait, filtered sync waits on the selected instance names while
    still checking daemon readiness globally.
    By default, sync does not stop or remove daemon-known extra instances.
    With --stop-extras, sync also stops running daemon-known instances not
    declared in `instances.toml`, while leaving running children of declared
    ephemeral instances alone; it still does not remove state or metadata. Use
    `rm` or `prune` explicitly for destructive cleanup.

agent-team health --strict-topology
    Treat running daemon-known instances that are not declared in
    `instances.toml` as unhealthy topology drift. Running children of declared
    ephemeral instances are not drift. This is still read-only; use `plan` to
    inspect the desired-state delta and `stop`, `rm`, or `prune` for explicit
    cleanup.

agent-team instance ls
    List declared instances and their state (running / stopped / never-spawned / crashed).
    Joins instances.toml + daemon process state + status.toml from each state dir.

agent-team instance ps
    Same as `ls` but filtered to currently-running.

agent-team instance show <name>
    Print daemon runtime metadata, the declared instance's config + triggers,
    and current state.

agent-team inspect <name> [--json]
    Top-level alias for `instance show`.

agent-team rm [<name>...] [--all] [--latest | --last N] [--status <status>] [--phase <phase>] [--stale] [--unhealthy] [--agent <agent>] [--dry-run] [--summary] [-f]
    Remove instance state and daemon metadata. Refuses running instances
    unless -f is set. With --all, remove every daemon-known instance,
    optionally narrowed by --status, --phase, --stale, --unhealthy, or
    --agent. With --dry-run, preview the same removal set without deleting
    state or daemon metadata.

agent-team prune [--older-than <duration>] [--agent <agent>] [--status exited|crashed] [--phase <phase>] [--stale] [--unhealthy] [--dry-run] [--summary]
    Remove finished daemon-known instances and their state without prompting.
    Running and stopped instances are intentionally left alone. Use --status,
    --phase, --stale, --unhealthy, --agent, and --older-than to narrow the
    finished cleanup set; --dry-run previews the same set.

agent-team event publish <type> [--payload <json>] [--format '{{len .Matched}} {{len .Dispatched}}'] [--json]
    Manual event injection — useful for testing trigger matching.
    The daemon resolves the event against declared triggers and dispatches accordingly.
```

`agent-team run <agent>` continues to work for ad-hoc spawning. It's now sugar for "publish a `user_invocation` event with target=<agent>". If a declared instance with name = `<agent>` exists, the run targets that declared instance (with its config); otherwise the agent template is spawned with a generated instance name.

## Daemon API additions

The orchestrator daemon (see [`orchestrator.md`](./orchestrator.md)) gains:

```
POST /event
    { "type": "ticket.created", "payload": { "project": "Platform", ... } }
    → { "matched": [<instance-names>], "dispatched": [{instance_id, started_at}, ...] }

GET /topology
    → declared instances + triggers, as parsed from the layered instances.toml

POST /topology/reload
    Re-reads instances.toml. Useful after editing without restarting the daemon.
```

Existing endpoints (`/dispatch`, `/message`, `/instances`, `/logs`) stay the same. `/event` is the public trigger entry point; `/dispatch` becomes its private implementation detail.

## Worked example: multi-ticket-manager routing

The motivating case from the design conversation: a user with two services in one repo wants tickets routed to two different Linear projects.

### `instances.toml` (consumer-authored)

```toml
[instances.manager]
agent     = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "user_invocation"

[[instances.manager.triggers]]
event        = "agent.dispatch"
match.target = "manager"

[instances.tm-platform]
agent     = "ticket-manager"
ephemeral = false

[instances.tm-platform.config.linear]
project_id = "3d07030a-a372-41a2-b01e-1b4116d0f151"

[[instances.tm-platform.triggers]]
event         = "ticket_webhook"
match.project = "Platform"

[instances.tm-mobile]
agent     = "ticket-manager"
ephemeral = false

[instances.tm-mobile.config.linear]
project_id = "50b6cd55-5760-4fd3-9bbe-acb17e544aa2"

[[instances.tm-mobile.triggers]]
event         = "ticket_webhook"
match.project = "Mobile"

[instances.worker]
agent     = "worker"
ephemeral = true
replicas  = 3
reap_worktree = "never"

[[instances.worker.triggers]]
event        = "agent.dispatch"
match.target = "worker"
```

### Bringing it up

```sh
$ agent-team start
Starting manager (manager)         ✓
Starting tm-platform (ticket-manager) ✓
Starting tm-mobile (ticket-manager)   ✓
worker (ephemeral, replicas=3) — spawn-on-trigger, not started
```

`agent-team ps`:

```
NAME           AGENT           STATE     EPHEMERAL  TRIGGERS                          PHASE
manager        manager         running   no         user_invocation                   idle
tm-platform    ticket-manager  running   no         ticket_webhook (project=Platform) idle
tm-mobile      ticket-manager  running   no         ticket_webhook (project=Mobile)   idle
worker         worker          —         yes (3)    agent.dispatch (target=worker)    —
```

### Event flowing through

A Linear ticket lands in the Platform project. Intake normalizes the provider
payload and publishes a topology event. For board-driven dispatch, the event is
the destination column transition:

```
POST /event
    { "type": "ticket.status_changed",
      "payload": { "project": "Platform", "ticket": "PLAT-42",
                   "status": "Ready for Agent", ... } }

→ { "matched": ["tm-platform"],
    "dispatched": [{ "instance_id": "...", "started_at": "..." }] }
```

`tm-platform` still matches because `ticket_webhook` is a legacy alias for the
normalized `ticket.*` family. It wakes up (it's persistent — already running,
the daemon `SendMessage`s it the event payload), reads the ticket, files /
triages / etc. against its declared `linear.project_id = 3d07030a-...`.
`tm-mobile` is unaffected.

If the manager later dispatches a worker via `assign-worker`:

```
POST /event
    { "type": "agent.dispatch",
      "payload": {
        "source": "manager",
        "target": "worker",
        "name": "worker-squ-42",
        "ticket": "SQU-42",
        "kickoff": "implement SQU-42",
        "workspace": "worktree"
      } }

→ { "matched": ["worker"],
    "dispatched": [{ "instance_id": "worker-squ-42", "started_at": "..." }] }
```

A fresh worker spawns under the requested safe child name. If no `payload.name` is supplied, the daemon falls back to `worker-<short-hex>`. The daemon creates `.agent_team/state/<worker-name>/`, writes the resolved config from repo + declared instance overrides, stages the same `--agents` / skill runtime that `agent-team run` uses, exports `AGENT_TEAM_ROOT`, `AGENT_TEAM_INSTANCE`, `AGENT_TEAM_STATE_DIR`, and `AGENT_TEAM_DAEMON_SOCKET`, and, because `workspace = "worktree"` was requested, launches the child in `.claude/worktrees/<worker-name>-<id>/`. When the worker exits, the replica slot frees up — capped at the declared `replicas = 3`.

For manual operation, `agent-team dispatch worker SQU-42 "implement SQU-42"` builds the same `agent.dispatch` payload without requiring the caller to hand-write JSON. `agent-team event publish ...` remains the low-level escape hatch for other event types and custom payloads.

### Inspecting and stopping

```sh
$ agent-team ps
NAME              AGENT           UPTIME  PHASE              SUMMARY
manager           manager         3h      idle               waiting on user
tm-platform       ticket-manager  3h      idle               last triaged PLAT-42 12m ago
tm-mobile         ticket-manager  3h      idle               —
worker-squ-42     worker          8m      implementing       Porting parameter substitution

$ agent-team instance down tm-mobile
Stopping tm-mobile ... ✓ (state preserved at .agent_team/state/tm-mobile/)
```

## Open design questions

1. **Match-expression DSL scope.** v1.2 starts with the small TOML-key DSL (single value / list / multiple AND-keys). Users may eventually want regex (`match.title ~ "^\\[urgent\\]"`) or simple boolean ops. Defer to v1.3 once we see what real workloads look like.

2. **Inter-agent dispatch migration.** Resolved for the bundled team as a compat shim: `assign-worker` remains the user-facing manager skill, but first posts `agent.dispatch` to the daemon. If the daemon is not running or cannot route the event, the skill falls back to Claude Code's legacy `TeamCreate` / `Agent` path.

3. **Replicas semantics.** For ephemeral instances with `replicas = N`: do we queue events that arrive while at capacity, or reject them? Probably queue with a configurable cap; rejection is bad UX for the dispatcher (manager would have to retry).

4. **State preservation on `instance down`.** Resolved: `.agent_team/state/<instance>/` survives stop/start cycles by default. `instance down --rm` and top-level `stop --rm` are explicit destructive cleanup paths for selected instances.

5. **Topology hot-reload.** `agent-team instance reload` re-parses `instances.toml` and applies diffs (start newly-declared, stop newly-undeclared, restart changed). Implementation has a tricky case: a running instance whose declared config changed — graceful restart, or wait for current work to drain? Defer the policy to v1.2 PR; default likely "warn, don't auto-restart, require explicit `instance restart <name>`."

6. **Webhook auth & delivery.** Provider intake now normalizes Linear and
GitHub events before publishing topology events. Hosted deployments still need
a production-grade listener, auth (HMAC verification per provider), replay
windows, and a public URL (ngrok-style tunnel for local dev, real DNS for
hosted).

## Relationship to other docs

- [`templates.md`](./templates.md) — defines the parameterized template that consumers `init` to produce `.agent_team/`. The template ships an `instances.toml` with sensible defaults; consumers can override or extend at the repo level. The four-layer config resolution chain in `templates.md` extends to a five-layer chain when topology adds the `[instances.<name>.config]` layer (see "Layered config resolution chain" above).
- [`orchestrator.md`](./orchestrator.md) — the daemon owns trigger resolution and lifecycle. `POST /event` is the trigger entry point; `/dispatch` and `/message` are the implementation primitives the daemon uses to actuate matched triggers.
- (future) `channels.md` — channels become one event source: a publish to `#some-channel` is a `channel.message` event that subscribed instances' triggers can match against.

## What this doesn't change

- Agent definitions stay file-based and human-authored. Topology doesn't change what an agent *is*, only how it's wired up at the repo level.
- The bundled software-engineering team ships a default `instances.toml` (one `manager`, one `worker`, one `ticket-manager`, with sensible triggers). Consumers who don't need multiple instances see the same UX they have today.
- `agent-team run <agent>` keeps working for ad-hoc spawning. Topology is opt-in beyond that.
- The `assign-worker` skill stays as the user-facing wrapper for inter-agent dispatch (per Open Question #2). Implementation switches to the daemon API; surface is unchanged.
