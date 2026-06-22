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
  --github-reconcile-job
```

The listener records deliveries and can publish normalized events to the daemon.

## Delivery History

```sh
agent-team intake summary
agent-team intake deliveries --unresolved
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
agent-team intake replay --all --unresolved --dry-run --preview-triggers
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
3. Intake runs with `--reconcile-job --cleanup-merged`.
4. Job is marked ready for cleanup or cleanup is applied when explicitly requested.

## Operational Rule

`repair` does not replay webhook failures automatically.

External events can have side effects, so replay remains an explicit operator command.
