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
agent-team schedule run nightly --dry-run --preview-triggers
```

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
agent-team intake github --payload-file github-webhook.json --reconcile-job --cleanup-merged --verify-pr
```

GitHub intake can reconcile PR metadata back to jobs, including PR-gate pipeline advancement and merged PR cleanup flows when requested. Add `--advance` to dispatch the next ready pipeline step after a PR URL or branch is reconciled. Add `--verify-pr` when cleanup should confirm the recorded PR is merged with `gh` before removing job-owned worktrees or branches.

## Intake Server

```sh
agent-team intake serve \
  --addr 127.0.0.1:8787 \
  --dry-run \
  --preview-triggers
```

The local server can receive provider webhooks and write delivery history.

Provider secrets and max-age checks are available for safer webhook handling.
For GitHub PR-gate automation, combine `--github-reconcile-job --github-advance-job`. For merged-PR cleanup, combine `--github-reconcile-job --github-cleanup-merged --github-verify-pr`.
For reverse proxy, service manager, and recovery guidance, see [Intake Deployment](../use-cases/intake-deployment.md).

## Delivery History

```sh
agent-team intake summary
agent-team intake deliveries --tail 20
agent-team intake deliveries --unresolved
agent-team intake doctor
agent-team intake replay <delivery-id> --dry-run --preview-triggers
agent-team intake replay --all --dry-run --preview-triggers
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

Successful replays mark failed deliveries recovered.

## Diagnostics

`overview`, `health`, `next`, `repair --dry-run`, and `snapshot` surface unresolved intake failures with replay commands.

Important distinction: `repair` does not automatically replay webhooks. It surfaces replay commands so operators can choose when external events are safe to replay.

## Code Areas

- `internal/intake/intake.go`
- `internal/cli/intake.go`
- `internal/cli/intake_delivery.go`
- `internal/cli/intake_doctor.go`
- `internal/daemon/scheduler.go`
- `internal/daemon/schedule_state.go`
- `internal/cli/schedule.go`
