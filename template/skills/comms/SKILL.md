---
name: comms
description: Deliver comms posts through sanctioned automation, including Discord webhook delivery from .env without inheriting the secret in the process environment.
---

# Comms delivery helper

Every Discord delivery mode — scheduled digest, release announcement, manual
or `agent.dispatch` request, retry, and future comms modes — MUST call
`scripts/discord-webhook.sh`. It is the canonical delivery boundary: a
team-wide lock and durable ledger enforce at most one successful webhook call
in any rolling 24-hour window. A schedule becoming due is only an opportunity;
it is never permission to bypass the helper or a requirement to post.

The helper reads the webhook URL from `.env` directly instead of from the
inherited process environment, so `env_allow` does not need to pass the secret
into the runtime.

Default `.env` key:

```sh
AGENT_TEAM_DISCORD_WEBHOOK=https://discord.com/api/webhooks/...
```

Repos may rename the key in committed config without committing the secret:

```toml
[comms]
discord_webhook_env = "MY_DISCORD_WEBHOOK"
```

Pass a stable logical delivery ID on every call. Retries reuse the same ID.
Scheduled digests should derive it from their canonical source range, release
announcements use the release tag, and manual requests use the durable job or
event ID.

Usage:

```sh
"$AGENT_TEAM_ROOT"/skills/comms/scripts/discord-webhook.sh \
  --kind digest \
  --delivery-id "digest:<last-success>:<newest-source-id>" \
  --content-file digest.md

"$AGENT_TEAM_ROOT"/skills/comms/scripts/discord-webhook.sh \
  --kind release \
  --delivery-id "release:v0.6.0" \
  --content-file announcement.md
```

The script searches `$PWD/.env` first, then the main working tree's `.env` when
running from a linked worktree. It writes shared state beneath
`$AGENT_TEAM_ROOT/state/comms/discord-delivery/`, never an ephemeral instance
directory:

- `state.json` records the last confirmed success, its stable delivery ID and
  timestamp, idempotency history, and conservative holds for ambiguous sends.
- `attempts/` journals reservations and confirmed HTTP receipts so a process
  restart after a successful post cannot post it again.
- `pending.json` and `pending.md` preserve deferred material. Release content
  is selected before digest/manual content at the next eligible meaningful
  post.
- `supervisor-notifications.jsonl` durably records local notices; the helper
  also sends the supervisor an inbox message when available.

The helper emits one JSON result. `delivered` and `duplicate` exit zero;
`deferred` exits 3; unavailable webhook configuration exits 2; definitive HTTP
failure or an ambiguous response exits 1. Definitive HTTP failures do not
advance the allowance and are immediately retryable. Never add an automatic
HTTP retry outside this helper.

Read the canonical catch-up boundary before gathering material:

```sh
"$AGENT_TEAM_ROOT"/skills/comms/scripts/discord-webhook.sh --status
```

Use `last_success.timestamp` from that output. Do not advance the source window
for deferred, failed, unavailable, or ambiguous attempts. If there is no
meaningful material, do not call the delivery helper; quiet windows stay quiet.
