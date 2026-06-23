# Intake Deployment

This guide shows one practical way to run `agent-team intake serve` for real Linear and GitHub webhooks.

The intake server is intentionally small: it listens for provider payloads, normalizes them, records delivery history, and publishes events into the repo-local daemon. It should run next to the repo it operates on.

## Recommended Shape

Run three pieces together:

1. the repo checkout with `.agent_team/`
2. `agent-team daemon`
3. `agent-team intake serve` behind a TLS reverse proxy or tunnel

Keep `intake serve` bound to localhost unless you are deliberately exposing it on a private network:

```sh
cd /srv/agent-team/my-repo
agent-team daemon start

agent-team intake serve \
  --addr 127.0.0.1:8787 \
  --linear-secret "$LINEAR_WEBHOOK_SECRET" \
  --github-secret "$GITHUB_WEBHOOK_SECRET" \
  --github-reconcile-job \
  --github-cleanup-merged \
  --github-verify-pr
```

Provider webhook URLs point at the public proxy:

```text
https://intake.example.com/linear
https://intake.example.com/github
https://intake.example.com/healthz
```

## Before Exposing It

Run the same server in preview mode first:

```sh
agent-team intake serve \
  --addr 127.0.0.1:8787 \
  --dry-run \
  --preview-triggers \
  --linear-secret "$LINEAR_WEBHOOK_SECRET" \
  --github-secret "$GITHUB_WEBHOOK_SECRET"
```

Then send saved provider payloads through the CLI path:

```sh
agent-team intake linear --payload-file linear-webhook.json --dry-run --preview-triggers
agent-team intake github --payload-file github-webhook.json --dry-run --preview-triggers
agent-team intake github --payload-file github-webhook.json --reconcile-job --cleanup-merged --verify-pr --dry-run --json
```

## Reverse Proxy

Any TLS-terminating proxy can forward to the local listener. A minimal Caddy-style shape is:

```text
intake.example.com {
  reverse_proxy 127.0.0.1:8787
}
```

If you use a tunnel instead, point the tunnel at `http://127.0.0.1:8787` and configure provider webhooks with the tunnel HTTPS URL.

Do not depend on network location alone for trust. Configure provider webhook secrets and keep the public proxy limited to `/linear`, `/github`, and `/healthz` when possible.

## Secrets

Secrets can come from flags or environment variables:

```sh
export LINEAR_WEBHOOK_SECRET=...
export GITHUB_WEBHOOK_SECRET=...

agent-team intake serve --addr 127.0.0.1:8787
```

GitHub cleanup verification also needs `gh` available and authenticated for the repository when `--github-verify-pr` is enabled:

```sh
gh auth status
gh pr view https://github.com/OWNER/REPO/pull/123 --json merged,state,mergeCommit
```

If `gh` is unavailable, omit `--github-verify-pr` and keep cleanup manual with `job cleanup --dry-run`, `--merged`, and `--verify-pr` from an operator shell.

## systemd Example

Use the service manager to keep `intake serve` in the foreground. Let `agent-team daemon start` prepare the repo daemon before the listener starts.

```ini
[Unit]
Description=agent-team intake server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/srv/agent-team/my-repo
Environment=LINEAR_WEBHOOK_SECRET=replace-me
Environment=GITHUB_WEBHOOK_SECRET=replace-me
ExecStartPre=/usr/local/bin/agent-team daemon start
ExecStart=/usr/local/bin/agent-team intake serve --addr 127.0.0.1:8787 --github-reconcile-job --github-cleanup-merged --github-verify-pr
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

Use your host's normal secret manager instead of literal `Environment=` values when possible.

## Operations

Check listener health:

```sh
curl -fsS http://127.0.0.1:8787/healthz
agent-team health --jobs
agent-team intake summary
```

Inspect deliveries:

```sh
agent-team intake deliveries --tail 20
agent-team intake deliveries --unresolved
agent-team intake doctor
```

Replay only after inspecting the delivery:

```sh
agent-team intake replay <delivery-id> --dry-run --preview-triggers
agent-team intake replay <delivery-id>
```

Prune resolved history explicitly:

```sh
agent-team intake prune --status ok --older-than 168h --dry-run
agent-team intake prune --status ok --older-than 168h
```

The server also prunes successful and recovered deliveries after requests by default. Tune or disable that with:

```sh
agent-team intake serve \
  --prune-ok-older-than 168h \
  --prune-recovered-older-than 168h
```

Use `0` for either duration to disable that automatic retention path.

## Failure Modes

| Symptom | First checks |
| --- | --- |
| Provider gets a non-2xx response | `agent-team intake deliveries --tail 20`, then `agent-team intake doctor` |
| Delivery says daemon is not running | `agent-team daemon start`, then replay the delivery after dry-run |
| GitHub cleanup fails verification | `gh auth status`, `gh pr view <url> --json merged,state,mergeCommit` |
| Events publish but nothing starts | `agent-team event publish <type> --payload-file ... --dry-run`, `agent-team topology summary` |
| Jobs look stale after webhook delivery | `agent-team job show <id> --events all`, `agent-team job reconcile status` |

`repair` does not replay webhooks automatically. External events can have side effects, so replay remains an explicit operator action.
