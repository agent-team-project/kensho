# Board Control Plane

In Linear-backed repos or GitHub Projects-backed repos, the board can be the
dispatch control plane. The bundled template uses one provider status column as
the intentional gesture for starting the default ticket-to-PR pipeline.

## Configure Linear Mode

Opt in during `init` or edit `.agent_team/config.toml`:

```toml
[pm]
provider = "linear"

[linear]
team_id = "00000000-0000-0000-0000-000000000000"
ticket_prefix = "APP"
agent_column = "Ready for Agent"
in_progress_state = "In Progress"
# attention_state = "Todo"
# agent_user_id = "..."
```

`linear.team_id` and `linear.ticket_prefix` are required in Linear mode.
`linear.agent_column` names the status column that dispatches work. The
template default is `Ready for Agent`.

## Configure GitHub Mode

Opt in during `init` or edit `.agent_team/config.toml`:

```toml
[pm]
provider = "github"

[github]
owner = "acme"
repo = "widgets"
agent_column = "Ready for Agent"
in_progress_state = "open"
attention_state = "open"
# agent_login = "agent-bot"
# project_owner = "acme"
# project_number = 7
# project_status_field = "Status"
# in_progress_column = "In Progress"
# attention_column = "Todo"
```

`github.owner` and `github.repo` are required in GitHub mode.
`github.agent_column` names the Projects status option that dispatches work.
When `github.project_number` is configured, write-back can update that Projects
v2 item status as well as issue state, labels, and comments.

## GitHub Project Status Automation

This repo wires GitHub Project #1 through
`.github/workflows/project-status.yml` and
`scripts/github/sync_project_status.py`. The workflow treats the issue card as
the board item and derives its `Status` field from GitHub issue and PR state:

| Observed state | Project status |
| --- | --- |
| Issue opened, reopened, or first added to the project | `Todo` |
| A PR with a GitHub closing keyword references the issue | `In Progress` |
| The PR merges or the issue closes | `Done` |

`Ready for Agent` remains the dispatch gesture used by topology. The scheduled
reconciler preserves an issue already in `Ready for Agent` when the derived
status would otherwise be `Todo`, then takes over when a linked PR opens or the
issue closes. This prevents hourly reconciliation from racing the daemon's
board-column dispatch while still correcting blank, stale, in-progress, and done
statuses.

GitHub's repository `GITHUB_TOKEN` cannot mutate user or organization Projects
v2 fields. Configure a repository secret named `PROJECTS_TOKEN` with repo read
access and GitHub Projects write scope, then set the project coordinates in the
workflow environment:

```yaml
PROJECT_OWNER: agent-team-project
PROJECT_OWNER_KIND: user
PROJECT_NUMBER: "1"
PROJECT_STATUS_FIELD: Status
PROJECT_TODO_STATUS: Todo
PROJECT_IN_PROGRESS_STATUS: In Progress
PROJECT_DONE_STATUS: Done
PROJECT_AGENT_COLUMN: Ready for Agent
```

The workflow runs on:

- `issues` opened, reopened, and closed events;
- `pull_request_target` opened, reopened, edited, synchronized,
  ready-for-review, and closed events;
- an hourly reconciliation of existing project issue cards;
- manual `workflow_dispatch` for a specific issue, PR, or all project items.

`pull_request_target` is intentional: the workflow needs the project secret, so
it checks out the trusted base branch and does not run code from the PR head.
The script only treats GitHub closing keywords (`Closes`, `Fixes`, `Resolves`)
as implementation links. `Advances #<epic>` and `Refs #<epic>` keep epics open
and do not drive `In Progress`/`Done` status.

Local operator examples:

```sh
PROJECTS_TOKEN=... \
  python3 scripts/github/sync_project_status.py \
  --repo agent-team-project/kensho \
  --project-owner agent-team-project \
  --project-owner-kind user \
  --project-number 1 \
  --issue-number 216 \
  --dry-run
```

```sh
PROJECTS_TOKEN=... \
  python3 scripts/github/sync_project_status.py \
  --repo agent-team-project/kensho \
  --project-owner agent-team-project \
  --project-owner-kind user \
  --project-number 1 \
  --all-project-items
```

## Column Dispatch

The bundled topology declares:

```toml
[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"
trigger.match.status = "Ready for Agent"
auto_advance = true
redispatch_on_reentry = false
```

`agent-team intake linear` normalizes Linear webhooks into events such as
`ticket.status_changed` and places the destination column in `payload.status`.
`agent-team intake github` does the same for GitHub Projects status edits. When
that status equals the configured agent column, topology resolution creates or
updates the durable job and dispatches the first ready step. Moving the card or
project item to any other column does not match this trigger.

Preview before publishing a webhook payload:

```sh
agent-team intake linear \
  --payload-file linear-webhook.json \
  --dry-run \
  --preview-triggers
```

```sh
agent-team intake github \
  --payload-file github-webhook.json \
  --dry-run \
  --preview-triggers
```

For demos or ticketless setups, the topology can instead trigger on
`ticket.created` without `trigger.match.status`, but the board-column pattern is
the field-tested production default.

## Write-Back

Provider write-back is best-effort and audited on the job. It is intentionally
separate from dispatch matching:

- dispatch or bounce can move the ticket to `[linear].in_progress_state`
- failed work can move the ticket to `[linear].attention_state` when that value
  is configured
- GitHub mode can also apply configured labels and move a Projects v2 status
  option when project write-back is configured
- failures leave job audit events instead of hiding the local job state

The write-back layer uses the same token resolution as the provider skill. If
no token or matching provider config exists, the local durable job still records
the work state and the write-back result is skipped or failed in the audit
trail.

Operators and scripts can also act on the configured provider directly:

```sh
agent-team ticket create --title "Investigate webhook replay gap" --body-file body.md
agent-team ticket comment APP-42 --body "Queued for agent review."
agent-team ticket update APP-42 --state "Ready for Agent"
agent-team ticket close APP-42 --state Done
```

`agent-team ticket` uses `[pm].provider` and supports Linear and GitHub. It is a
provider bridge, not a dispatch command; board-column dispatch still comes from
normalized intake events matching topology triggers.

## Loop Protection

Agent-authored status changes must not dispatch another worker. Provider intake
therefore filters status-change events by actor when it can identify the agent
user. For Linear, configure:

```toml
[linear]
agent_user_id = "..."
```

If `agent_user_id` is not set, a cached `.agent_team/state/linear/viewer.json`
with `viewer.id` can provide the same actor id. Status-change events from that
actor are ignored before topology matching.

For GitHub, configure:

```toml
[github]
agent_login = "agent-bot"
# or
agent_id = "123456"
```

If neither value is set, a cached `.agent_team/state/github/viewer.json` can
provide the actor identity. Status-change events from that actor are ignored
before topology matching.

## Re-Entry

Re-entry is idempotent by default. If a matching status-change event arrives
for a ticket whose job is queued, running, or blocked, the daemon records a
`pipeline_reentry_noop` audit event and dispatches nothing.

Terminal jobs also no-op unless the pipeline opts in:

```toml
[pipelines.ticket_to_pr]
redispatch_on_reentry = true
```

With that setting, a matching re-entry reopens the terminal job, resets the
first step, and dispatches again. Leave it `false` when dragging a completed
card through the agent column should be harmless.

## Operating Commands

Use these commands to inspect and operate board-driven work:

```sh
agent-team topology graph --routes
agent-team pipeline status ticket_to_pr
agent-team job show squ-42 --events all
agent-team job timeline squ-42
agent-team job bounce squ-42 --findings-file review.md --advance
agent-team job update squ-42 --pr https://github.com/acme/repo/pull/42 --advance --dry-run
```

`job show`, `job events`, and `job timeline` are the best places to see whether
a board event dispatched work, no-oped as re-entry, or recorded a write-back
attempt.

## References

- [Authoring Topology](../authoring/topology.md)
- [Intake and Schedules](../workflows/intake-and-schedules.md)
- [Ticket to PR](../use-cases/ticket-to-pr.md)
- [File Formats](../reference/file-formats.md)
