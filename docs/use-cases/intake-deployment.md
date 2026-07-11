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
  --require-linear-secret \
  --require-github-secret \
  --github-reconcile-job \
  --github-advance-job \
  --github-cleanup-merged \
  --github-verify-pr
```

Provider webhook URLs point at the public proxy:

```text
https://intake.example.com/linear
https://intake.example.com/github
https://intake.example.com/healthz
```

`--github-advance-job` lets GitHub PR events unlock PR-gated pipeline steps
after job PR metadata is reconciled. Keep `--github-cleanup-merged` and
`--github-verify-pr` for post-merge branch/worktree cleanup.
The server path stays non-blocking for webhook latency. When running a saved
GitHub payload from a shell or CI job, use the one-shot CLI with
`--reconcile-job --advance --wait --wait-status running` if the command should
block until the advanced step has a live owner.

## Before Exposing It

Run the same server in preview mode first:

```sh
agent-team intake serve \
  --addr 127.0.0.1:8787 \
  --dry-run \
  --preview-triggers \
  --linear-secret "$LINEAR_WEBHOOK_SECRET" \
  --github-secret "$GITHUB_WEBHOOK_SECRET" \
  --require-linear-secret \
  --require-github-secret
```

Then send saved provider payloads through the CLI path:

```sh
agent-team intake linear --payload-file linear-webhook.json --dry-run --preview-triggers
agent-team intake linear --payload-file linear-webhook.json --dry-run --preview-triggers --commands
agent-team intake github --payload-file github-webhook.json --dry-run --preview-triggers
agent-team intake github --payload-file github-webhook.json --dry-run --preview-triggers --commands
agent-team intake github --payload-file github-webhook.json --reconcile-job --advance --wait --wait-status running --wait-timeout 30s --json
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

Signed GitHub webhooks also use `X-GitHub-Delivery` replay protection by default. The server rejects duplicate delivery IDs seen in the last 24 hours; tune this with `--github-replay-window`, or set it to `0` only for nonstandard test senders.

Webhook request bodies are capped at 1 MiB by default. Tune this with `--max-body-bytes` for providers or proxies that legitimately send larger payloads, but keep it bounded for public endpoints.

For exposed servers, pass `--require-linear-secret` and `--require-github-secret` so an empty environment variable fails startup instead of silently disabling signature verification.

To inspect a duplicate or provider retry, filter delivery history by that request ID:

```sh
agent-team intake deliveries --provider github --request-id <x-github-delivery>
```

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

Generate a unit from the repo where `.agent_team/` lives:

```sh
agent-team intake service systemd \
  --bin /usr/local/bin/agent-team \
  --name agent-team-intake-my-repo \
  --require-linear-secret \
  --require-github-secret \
  --github-reconcile-job \
  --github-advance-job \
  --github-cleanup-merged \
  --github-verify-pr \
  > agent-team-intake-my-repo.service
```

The generated unit has this shape:

```ini
# Save as /etc/systemd/system/agent-team-intake-my-repo.service
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
ExecStart=/usr/local/bin/agent-team intake serve --addr 127.0.0.1:8787 --linear-max-age 1m0s --github-replay-window 24h0m0s --max-body-bytes 1048576 --prune-ok-older-than 168h0m0s --prune-recovered-older-than 168h0m0s --github-reconcile-job --github-cleanup-merged --github-verify-pr --github-advance-job --require-linear-secret --require-github-secret
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

Use your host's normal secret manager instead of literal `Environment=` values when possible.
For systemd, pass `--env-file /etc/agent-team/intake.env` to generate an `EnvironmentFile=` reference instead of placeholder secret values.

## launchd Example

For a macOS development host, generate a LaunchAgent plist from the repo where `.agent_team/` lives:

```sh
agent-team intake service launchd \
  --bin /opt/homebrew/bin/agent-team \
  --name com.example.agent-team-intake-my-repo \
  --github-reconcile-job \
  --github-advance-job \
  --github-cleanup-merged \
  --github-verify-pr \
  > com.example.agent-team-intake-my-repo.plist
```

The generated plist starts the repo daemon, then replaces the shell with the foreground intake server:

```xml
# Save as ~/Library/LaunchAgents/com.example.agent-team-intake-my-repo.plist
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.example.agent-team-intake-my-repo</string>
  <key>WorkingDirectory</key>
  <string>/srv/agent-team/my-repo</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>LINEAR_WEBHOOK_SECRET</key>
    <string>replace-me</string>
    <key>GITHUB_WEBHOOK_SECRET</key>
    <string>replace-me</string>
  </dict>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-lc</string>
    <string>/opt/homebrew/bin/agent-team daemon start &amp;&amp; exec /opt/homebrew/bin/agent-team intake serve --addr 127.0.0.1:8787 --linear-max-age 1m0s --github-replay-window 24h0m0s --max-body-bytes 1048576 --prune-ok-older-than 168h0m0s --prune-recovered-older-than 168h0m0s --github-reconcile-job --github-cleanup-merged --github-verify-pr --github-advance-job</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
```

## Compose Example

For a container host, generate a Compose service that mounts the repo and runs the intake listener inside an image containing `agent-team`, `agent-teamd`, and any runtime binaries your topology needs:

```sh
docker build -t agent-team:local .
```

CI publishes the same image recipe to `ghcr.io/agent-team-project/agent-team` on pushes to `main` and `v*` release tags, then signs published digests with keyless cosign. Use that image when you want a registry-hosted base instead of a local build.

Verify a published image before pinning it in deployment manifests:

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/agent-team-project/kensho/.github/workflows/container.yml@refs/(heads/main|tags/v.*)' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/agent-team-project/agent-team:latest

cosign verify-attestation \
  --type slsaprovenance \
  --certificate-identity-regexp 'https://github.com/agent-team-project/kensho/.github/workflows/container.yml@refs/(heads/main|tags/v.*)' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/agent-team-project/agent-team:latest
```

```sh
agent-team intake service compose \
  --image agent-team:local \
  --bin agent-team \
  --name agent-team-intake-my-repo \
  --publish 127.0.0.1:8787:8787 \
  --github-reconcile-job \
  --github-advance-job \
  --github-cleanup-merged \
  --github-verify-pr \
  > docker-compose.agent-team-intake-my-repo.yml
```

Compose defaults the listener address to `0.0.0.0:8787` so Docker port publishing can reach it. The generated service has this shape:

```yaml
# Save as docker-compose.agent-team-intake-my-repo.yml
services:
  "agent-team-intake-my-repo":
    image: "agent-team:local"
    working_dir: "/workspace"
    volumes:
      - "/srv/agent-team/my-repo:/workspace"
    ports:
      - "127.0.0.1:8787:8787"
    environment:
      "LINEAR_WEBHOOK_SECRET": "replace-me"
      "GITHUB_WEBHOOK_SECRET": "replace-me"
    command:
      - "/bin/sh"
      - "-lc"
      - "agent-team daemon start && exec agent-team intake serve --addr 0.0.0.0:8787 --linear-max-age 1m0s --github-replay-window 24h0m0s --max-body-bytes 1048576 --prune-ok-older-than 168h0m0s --prune-recovered-older-than 168h0m0s --github-reconcile-job --github-cleanup-merged --github-verify-pr --github-advance-job"
    restart: unless-stopped
```

The included `Dockerfile` is an operational base image for the CLI and daemon. If your deployed topology needs an LLM runtime, `gh`, or cloud credentials, extend the image or mount those tools and secrets explicitly rather than baking private credentials into the image.
For Compose, pass `--env-file ./intake.env` to generate an `env_file:` reference instead of placeholder secret values.

## Kubernetes Example

For a Kubernetes host, generate manifests that reference a workspace PVC containing this repo's `.agent_team/` directory:

```sh
agent-team intake service kubernetes \
  --image ghcr.io/agent-team-project/agent-team:latest \
  --bin agent-team \
  --name agent-team-intake-my-repo \
  --secret-name agent-team-intake-secrets \
  --workspace-claim agent-team-workspace \
  --ingress-host intake.example.com \
  --ingress-class nginx \
  --tls-secret agent-team-intake-tls \
  --github-reconcile-job \
  --github-advance-job \
  --github-cleanup-merged \
  --github-verify-pr \
  > kubernetes.agent-team-intake-my-repo.yaml
```

Kubernetes defaults the listener address to `0.0.0.0:8787` so the generated Service can reach the pod. The manifest includes a Secret with placeholder webhook values, a Deployment that starts the daemon before execing `intake serve`, a Service on port 8787, and an optional Ingress when `--ingress-host` is set. The PVC named by `--workspace-claim` must already contain the repo state that should be served.

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
agent-team intake deliveries --unresolved --commands
agent-team intake doctor
```

Replay only after inspecting the delivery:

```sh
agent-team intake replay <delivery-id> --dry-run --preview-triggers
agent-team intake replay <delivery-id> --dry-run --preview-triggers --commands
agent-team intake replay <delivery-id>
```

Prune resolved history explicitly:

```sh
agent-team intake prune --status ok --older-than 168h --dry-run
agent-team intake prune --status ok --older-than 168h --dry-run --commands
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
| Events publish but nothing starts | `agent-team event publish <type> --payload-file ... --dry-run --commands`, `agent-team topology summary` |
| Jobs look stale after webhook delivery | `agent-team job show <id> --events all`, `agent-team job reconcile status --dry-run` |

`repair` does not replay webhooks automatically. External events can have side effects, so replay remains an explicit operator action.
