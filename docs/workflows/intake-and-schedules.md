# Intake and Schedules

Intake and schedules are event sources.

They create normalized events that flow through topology and jobs instead of directly spawning ad-hoc agents.

## Schedules

Schedules live in `instances.toml`.

```toml
[schedules.nightly]
every = "24h"
run_on_start = true
payload.target = "manager"
payload.reason = "nightly repo maintenance"
```

Commands:

```sh
agent-team schedule ls
agent-team schedule due
agent-team schedule next --limit 5
agent-team schedule fire --dry-run --preview-triggers
agent-team schedule fire --wait --wait-next-state queued --wait-step triage --wait-timeout 30s
agent-team schedule run nightly --dry-run --preview-triggers
agent-team schedule run nightly --payload '{"ticket":"SQU-610"}' --wait --wait-next-state queued --wait-step triage --wait-timeout 30s
agent-team intake schedule nightly --payload '{"ticket":"SQU-611"}' --wait --wait-next-state queued --wait-step triage --wait-timeout 30s
```

When a schedule routes into a pipeline, `--wait` can block on the durable jobs the schedule event creates or updates. Use `--wait-next-state` with `--wait-step` for stage-aware handoff; unstarted persistent targets commonly leave the step `queued`, while live runtime handoffs can reach `running`.

Schedules without deterministic ticket payloads may create generated job ids. Daemon event outcomes include `job_id`, `pipeline`, and `step` metadata so schedule waits can follow those jobs after publish.

Schedules are also processed by:

```sh
agent-team tick
agent-team tick --until-idle
agent-team team tick delivery
```

## Intake Providers

Supported intake command groups include:

- `intake linear`
- `intake github`
- `intake schedule`
- `intake serve`
- delivery history commands

The important design principle is that intake normalizes external input into internal events and durable delivery records.

Linear intake emits normalized `ticket.*` events such as `ticket.created`.
GitHub PR intake emits normalized `pr.*` events such as `pr.opened` and
`pr.merged`. Older topology files that use `ticket_webhook` or `pr_webhook`
still route: those trigger names match the corresponding normalized events,
and `match.event` sees the suffix (`created`, `opened`, `merged`, and so on).

## Linear Intake

```sh
agent-team intake linear --payload-file linear-webhook.json --dry-run --preview-triggers
```

Expected use:

1. Receive Linear webhook payload.
2. Validate and normalize event.
3. Preview or publish topology event.
4. Record delivery.
5. Create or update durable jobs through pipeline triggers where appropriate.

## GitHub Intake

```sh
agent-team intake github --payload-file github-webhook.json --dry-run --preview-triggers
agent-team intake github --payload-file github-webhook.json --reconcile-job --advance
agent-team intake github --payload-file github-webhook.json --reconcile-job --advance --wait --wait-status running --wait-timeout 30s
agent-team intake github --payload-file github-webhook.json --reconcile-job --cleanup-merged --verify-pr
```

GitHub intake can reconcile PR metadata back to jobs, including PR-gate pipeline advancement and merged PR cleanup flows when requested. Add `--advance` to dispatch the next ready pipeline step after a PR URL or branch is reconciled. Add `--wait --wait-status running` for foreground scripts that should block until the advanced step has a live owner. Add `--verify-pr` when cleanup should confirm the recorded PR is merged with `gh` before removing job-owned worktrees or branches.

## Intake Server

```sh
agent-team intake serve \
  --addr 127.0.0.1:8787 \
  --dry-run \
  --preview-triggers
```

The local server can receive provider webhooks and write delivery history.

Provider secrets and max-age checks are available for safer webhook handling.
For GitHub PR-gate automation, combine `--github-reconcile-job --github-advance-job`. The server path stays non-blocking so webhook responses are not held open while a runtime starts. For merged-PR cleanup, combine `--github-reconcile-job --github-cleanup-merged --github-verify-pr`.
For reverse proxy, service manager, and recovery guidance, see [Intake Deployment](../use-cases/intake-deployment.md).

## Delivery History

```sh
agent-team intake summary
agent-team intake summary --commands
agent-team intake deliveries --tail 20
agent-team intake deliveries --unresolved
agent-team intake deliveries --unresolved --commands
agent-team intake duplicates --commands
agent-team intake doctor
agent-team intake doctor --commands
agent-team intake replay <delivery-id> --dry-run --preview-triggers
agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers
agent-team intake prune --older-than 168h --dry-run
```

Delivery history records:

- provider
- status
- replay status
- event type
- ticket
- error message
- normalized payload
- timestamps

`agent-team intake doctor` warns when the ledger contains repeated provider
request IDs, such as duplicate GitHub delivery IDs, while keeping warning-only
ledgers exit-code clean. Add `--commands` when automation should print only
the duplicate-inspection commands from warning rows.

`agent-team intake summary` reports duplicate request-id group counts and
replay/prune actions. Use `agent-team intake duplicates` to list duplicate
groups and copy the generated `intake deliveries --request-id ...` inspection
command for each group. Add `--commands` to summary, deliveries, duplicates, or
doctor when scripts should receive only shell commands.
Duplicate request-id doctor warnings include an action that opens the matching
`intake duplicates --request-id ...` view.

When replaying several recorded deliveries after an outage, use
`agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers`
first to skip later duplicate provider deliveries before publishing.

Successful replays mark failed deliveries recovered.

## Diagnostics

`overview`, `health`, `next`, `repair --dry-run`, and `snapshot` surface unresolved intake failures with replay commands. `overview`, `next`, and `repair --dry-run` also surface duplicate provider request IDs with an `intake duplicates` action.

Important distinction: `repair` does not automatically replay webhooks. It surfaces replay commands so operators can choose when external events are safe to replay.

## Code Areas

- `internal/intake/intake.go`
- `internal/cli/intake.go`
- `internal/cli/intake_delivery.go`
- `internal/cli/intake_doctor.go`
- `internal/daemon/scheduler.go`
- `internal/daemon/schedule_state.go`
- `internal/cli/schedule.go`
