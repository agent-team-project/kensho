# Use Case: External Intake

This scenario covers Linear and GitHub webhooks.

## Goal

External systems should create or update durable jobs through normalized events, not spawn ad-hoc agents directly.

## Preview a Payload

```sh
agent-team intake linear \
  --payload-file linear-ticket-created.json \
  --dry-run \
  --preview-triggers
```

For GitHub:

```sh
agent-team intake github \
  --payload-file github-pr-merged.json \
  --dry-run \
  --preview-triggers
```

## Run a Local Listener

```sh
agent-team intake serve \
  --addr 127.0.0.1:8787 \
  --linear-secret "$LINEAR_WEBHOOK_SECRET" \
  --github-secret "$GITHUB_WEBHOOK_SECRET" \
  --github-reconcile-job \
  --github-cleanup-merged \
  --github-verify-pr
```

The listener records deliveries and can publish normalized events to the daemon.
For a production-oriented listener setup, see [Intake Deployment](./intake-deployment.md).

## Delivery History

```sh
agent-team intake summary
agent-team intake deliveries --unresolved
agent-team intake deliveries --unresolved --commands
agent-team intake doctor
```

The ledger distinguishes:

- successful deliveries
- failed deliveries
- unresolved failures
- recovered failures
- replayable failures
- replay failures

## Replay

Preview:

```sh
agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers
agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers --commands
```

Apply:

```sh
agent-team intake replay <delivery-id>
```

Successful replay marks the original failure recovered.

## GitHub PR Reconciliation

GitHub intake can update job metadata from PR events.

Example flow:

1. Worker opens PR and records URL.
2. GitHub sends PR merged webhook.
3. Intake runs with `--reconcile-job --cleanup-merged --verify-pr`.
4. Job is reconciled as done, then the job-owned worktree and branch are removed after PR verification.

## Operational Rule

`repair` does not replay webhook failures automatically.

External events can have side effects, so replay remains an explicit operator command.
