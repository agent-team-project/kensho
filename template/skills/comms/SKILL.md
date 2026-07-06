---
name: comms
description: Deliver comms posts through sanctioned automation, including Discord webhook delivery from .env without inheriting the secret in the process environment.
---

# Comms delivery helpers

Use `scripts/discord-webhook.sh` to post digest or release-announcement text to
Discord. The helper reads the webhook URL from `.env` directly instead of from
the inherited process environment, so `env_allow` does not need to pass the
secret into the runtime.

Default `.env` key:

```sh
AGENT_TEAM_DISCORD_WEBHOOK=https://discord.com/api/webhooks/...
```

Repos may rename the key in committed config without committing the secret:

```toml
[comms]
discord_webhook_env = "MY_DISCORD_WEBHOOK"
```

Usage:

```sh
"$AGENT_TEAM_ROOT"/skills/comms/scripts/discord-webhook.sh --content-file digest.md
```

The script searches `$PWD/.env` first, then the main working tree's `.env` when
running from a linked worktree. If no webhook is configured or delivery fails,
fall back to the pending-digest or pending-announcement path described in the
comms agent prompt.
