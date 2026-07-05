# File Formats

`agent-team` uses small, inspectable file formats instead of a database.

## `.agent_team/config.toml`

Resolved template parameters.

```toml
[team]
pm_tool = "none"

[linear]
team_id = ""
ticket_prefix = ""

[health]
status_stale_after = "10m"
job_stale_after = "24h"

[notifications]
phase_transitions = ["blocked"]
idle_renotify = "0"

[skills]
team = []
```

Read by skills and the CLI.

`[team].pm_tool` defaults to `"none"` in the bundled template. When it is
`"linear"`, `[linear].team_id` and `[linear].ticket_prefix` must be non-empty.

`[health]` is optional. `status_stale_after` controls when non-idle/non-done
instance `status.toml` files are marked stale in `ps`, `health`, `monitor`, and
related views. `job_stale_after` controls stale queued/running job triage. Set
either value to `"0"` to disable that stale check.

`[notifications]` is optional. `phase_transitions` controls which daemon-observed
instance `status.toml` phase transitions are published to `#supervisor`.
`"blocked"` reports any transition into blocked; `"idle"` reports busy-to-idle
transitions. `idle_renotify` is a duration string such as `"30m"`; `"0"` disables
repeat idle notifications.

`[skills].team` defaults to an empty list in the bundled template. Each entry
names a shared skill under `.agent_team/skills/` that should be registered for
every launched agent.

## `.agent_team/.template.lock`

Template provenance.

Stores source identity and content hash so `upgrade --check` can compare current state against a target ref and `upgrade --apply` can render a conservative three-way plan.

## `~/.agent-team/cache/**/.agent-team-meta.json`

Cache-only provenance for pulled git templates.

It records the requested ref, clone URL, revision, revision kind, resolved commit
SHA, cache key, and pull time. It is ignored by template content hashing and is
not copied into consumer `.agent_team/` directories.

## `template.toml`

Template manifest.

```toml
[template]
name = "software-engineering-team"
version = "0.1.0"
description = "..."

[[parameter]]
key = "team.pm_tool"
type = "string"
default = "none"
pattern = "^(none|linear)$"
description = "Which PM tool the team talks to."

[[parameter]]
key = "linear.team_id"
type = "string"
default = ""
required_when_key = "team.pm_tool"
required_when_value = "linear"
description = "Linear team UUID."
```

Parameter `type` supports `string`, `int`, `bool`, and `list<string>`.
Use `required = true` for unconditional values. Use `required_when_key` and
`required_when_value` together when a value is required only under a selected
config mode.

## `agent.md`

Agent definition.

```md
---
description: Coordinates implementation work.
---

Prompt body...
```

The loader extracts `description` and uses the body as the prompt.

## Agent `config.toml`

Skill assignment.

```toml
[skills]
extra = ["linear", "pull-request", "status"]
```

## `instances.toml`

Topology declaration.

```toml
[instances.manager]
agent = "manager"
ephemeral = false
brief = true

[[instances.manager.triggers]]
event = "user_invocation"

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 3
token_budget = "40M"
time_budget = "45m"
locks = ["build"]

[locks.build]
slots = 1
scope = "machine"

[channels.supervisor]
scope = "team"

[budgets]
reminder_levels = [50, 80, 100]

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[pipelines.ticket_to_pr.infra_signatures]
fixture_reaped = 'Os \{ code: 2, kind: NotFound'
missing_deps = 'deps/[^ ]*: No such file'

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
workspace = "worktree"
runtime = "codex"
token_budget = "40M"
time_budget = "45m"
reminder_levels = [50, 80, 100]
locks = ["build"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
workspace = "repo"
runtime = "claude"
after = ["implement"]
gate = "pr"
optional = true

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
channels = ["supervisor"]

[authority]
enforce = false

[authority.agents.worker]
allow = ["inbox.send", "channel.*", "job.gate.*:own"]
```

Normalized intake events use names like `ticket.created`, `ticket.updated`,
`pr.opened`, and `pr.merged`. Older topology files may still use
`ticket_webhook` or `pr_webhook`; those trigger names match the corresponding
normalized events, with the suffix available as `match.event`.

Pipeline `infra_signatures` entries are regexes used to classify failed gate
signatures reported by `agent-team job gate set`. They classify an explicit
`pass`/`fail` result as `infra` or `content`; they do not decide whether the
gate passed. Anchor them to error shapes rather than keywords: `NotFound` alone
is too broad, while `Os \{ code: 2, kind: NotFound` points at a concrete missing
fixture shape.

Named `[locks.<name>]` entries serialize ephemeral dispatches around shared
resources. `slots = 1` is a mutex; higher values are counting semaphores. Locks
listed on an instance and a pipeline step are unioned before spawn. If any slot
is unavailable, the daemon persists the dispatch in the normal queue with
`reason = "lock_held"`. `scope = "machine" | "team" | "job"` is optional on
locks, channels, and schedules; omitted scope is `machine`, preserving the
historical flat namespace. Team-scoped schedules use the declaring team for the
persisted clock key while publishing the same schedule event name.

`token_budget` and `time_budget` are soft per-run allowances. Instance values
are defaults; pipeline step values override them. `[budgets].reminder_levels`
sets the default percentage crossings that create `budget_notice` job events
and inbox messages; pipeline step values and job-level overrides take
precedence.

Declared `[channels.<name>]` entries are only needed for scoped channel storage;
undeclared channels still work. Team-scoped channels always use the owning
topology team for storage; actor team is authority-audit input only.
`[authority]` allowlists are audit-only in this release: violations append
`authority_violation` events and show up in job triage, but requests are not
blocked while `enforce` remains false. Job verbs can use `:own`, such as
`job.gate.*:own`, to match only when the target job id equals the caller's
origin job.

## Job TOML

Jobs live at `.agent_team/jobs/<job-id>.toml`.

Representative fields:

```toml
id = "squ-42"
ticket = "SQU-42"
target = "worker"
instance = "worker-squ-42"
status = "running"
held = true
hold_reason = "waiting for product signoff"
branch = "worker-squ-42"
worktree = "/repo/.agent_team/worktrees/worker-squ-42"
pr = "https://github.com/acme/app/pull/42"
last_event = "dispatched"
last_status = "implementing API change"
created_at = "2026-06-22T10:00:00Z"
updated_at = "2026-06-22T10:15:00Z"

[[steps]]
id = "implement"
target = "worker"
workspace = "worktree"
runtime = "codex"
status = "running"

[[steps]]
id = "review"
target = "manager"
workspace = "repo"
runtime = "claude"
status = "blocked"
after = ["implement"]
gate = "pr"
optional = true
```

Skipped steps are encoded as terminal steps, not a new lifecycle state:

```toml
[[steps]]
id = "triage"
target = "manager"
status = "done"
skipped = true
skip_reason = "triage folded into implementation"
```

Supported step gates are:

- `manual`: waits for operator approval with `agent-team job approve <job-id> --step <step-id>`; reject one gate with `agent-team job reject <job-id> --step <step-id>`.
- `pr`: waits until the job has PR metadata, usually from `agent-team job update <job-id> --pr <url> --advance --dry-run` followed by the non-dry-run update, GitHub intake reconciliation with `agent-team intake github --payload-file github-webhook.json --reconcile-job --advance --dry-run` plus optional `--commands`, or status reconciliation.

Set `optional = true` on a step when its failure should remain visible but should not block downstream `after` dependencies. A job with only optional failures and completed required steps closes as done with `last_status = "all required steps done"`.

Set `held = true` with an optional `hold_reason` when an operator has intentionally paused a job. Add `hold_until = 2026-06-24T18:00:00Z` for a time-boxed hold. This is not a lifecycle status; the job can remain `queued`, `running`, or `blocked` while `job next`, ready lists, pipeline status, and team views report the next-step state as `held` until `agent-team job release <job-id>`. Expired holds stay held until released, but `job ls --expired-hold`, `pipeline jobs --expired-hold`, `pipeline jobs <pipeline> --expired-hold`, `team jobs <team> --expired-hold`, `pipeline release <pipeline> --expired`, and `team release <team> --expired` can target them directly.

Exact encoding is owned by `internal/job`.

## Job Events JSONL

Job events are append-only JSONL rows.

They record:

- timestamp
- type
- status
- instance
- actor
- message
- structured data

Use `agent-team job events <job-id>` instead of reading raw rows in tooling, or
`agent-team job events --all` for one combined durable audit view.
Use `--type`, `--status`, `--actor`, `--instance`, and `--since` to narrow the
visible audit rows before rendering or summarizing.
Use `agent-team job events <job-id> --summary` when tooling only needs counts by
type, status, actor, or instance.
Use `agent-team job timeline --all --summary` when tooling needs one combined
durable audit and daemon lifecycle count across every job.
Use timeline filters like `--job`, `--kind`, `--status`, `--actor`, `--agent`,
and `--instance` before `--tail` when tooling needs a bounded combined view.
Use `agent-team pipeline job-events [<pipeline>]` to read or summarize the same
audit rows across pipeline-owned jobs without opening each job log separately.
Use `agent-team team job-events <team>` for the same durable audit view inside
one declared team boundary. Add `--follow` to stream newly appended audit rows
from any matching job.

## Job Gates JSONL

Gate results are append-only JSONL rows at
`.agent_team/jobs/<job-id>.gates.jsonl`. Latest row per gate name wins in CLI
views.

They record:

- timestamp
- job id
- gate name
- explicit status (`pass` or `fail`)
- optional failure signature
- optional log reference
- actor

Use `agent-team job gate set <job-id> <gate-name> --status pass|fail` to append
records and `agent-team job gates <job-id> [--json]` to read the latest folded
results. Failed results are classified as `infra` when their signature matches
the job pipeline's `[pipelines.<name>.infra_signatures]`; otherwise they are
`content`. CLI views surface the matched signature name and pattern alongside
the class so broad infra regexes are visible during triage. Use
`agent-team signatures test <pipeline> --against <log-file>` to dry-run the
configured regexes against a log and print match/no-match with the matching
excerpt.

## Runtime Metadata

Daemon metadata is TOML under `.agent_team/daemon/<instance>/`.

It records lifecycle state, PID, session id, workspace, and job ownership metadata.

Prefer `agent-team inspect` or `agent-team ps --json` for consumers.

## `status.toml`

Agent-reported work status.

```toml
[status]
phase = "blocked"
description = "Waiting for credentials"
since = "2026-06-22T10:00:00Z"

[work]
job = "squ-42"
ticket = "SQU-42"
branch = "worker-squ-42"
pr = "https://github.com/acme/app/pull/42"
```

This file is intentionally writable by skills and readable by operators.

## Queue JSON

Queue entries are JSON files.

```json
{
  "id": "q-123",
  "state": "pending",
  "event_type": "agent.dispatch",
  "instance": "worker",
  "instance_id": "worker-squ-42",
  "payload": {
    "job_id": "squ-42",
    "ticket": "SQU-42",
    "target": "worker"
  },
  "attempts": 1,
  "next_retry": "2026-06-22T10:05:00Z",
  "queued_at": "2026-06-22T10:00:00Z",
  "updated_at": "2026-06-22T10:01:00Z"
}
```

Active queue files live in `pending/` or `dead/`. Quarantined files preserve the same JSON shape under `quarantine/`.

## Intake Delivery JSONL

Delivery rows record normalized external events and replay state. Server-created rows may include `request_id`, such as GitHub's `X-GitHub-Delivery`, so intake can reject duplicate signed deliveries inside the configured replay window.

Use:

```sh
agent-team intake deliveries --json
agent-team intake summary --json
agent-team intake summary --commands
agent-team intake doctor --json
```

instead of parsing raw history where possible. Use `--commands` when automation
only needs the replay, prune, duplicate-inspection, or warning follow-up
commands derived from the ledger.
