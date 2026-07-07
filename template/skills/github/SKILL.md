---
name: github
description: Access GitHub Issues and Projects when the repo is configured for GitHub as its PM provider. Use to fetch issues, comment, update state/labels/project status, and inspect viewer identity.
---

# GitHub PM access

Call GitHub API endpoints through the helper bundled with this skill at `${AGENT_TEAM_ROOT}/skills/github/scripts/github-api.sh`. The helper checks that this repo is configured with `[pm].provider = "github"`, reads stable repo/project IDs from `.agent_team/config.toml`, loads a token from env or `.env`, and sends REST or GraphQL requests.

For worker-owned `gh` CLI calls and GitHub HTTPS pushes, use `${AGENT_TEAM_ROOT}/skills/github/scripts/github-auth.sh`. It pins the GitHub actor by resolving a token from `AGENT_TEAM_GITHUB_TOKEN`, a configured `[github].agent_login` via `gh auth token --user`, `GITHUB_TOKEN`/`GH_TOKEN`, or `.env`. When `github.agent_login` is set, the helper verifies `/user` before it runs the command, so a personal ambient `gh auth` account cannot silently take over worker pushes.

## Configuration

GitHub is optional. Ticketless repos use:

```toml
[pm]
provider = "none"
```

For GitHub-backed repos, required keys are:

```toml
[pm]
provider = "github"

[github]
owner = "acme"
repo = "widgets"
agent_column = "Ready for Agent"
```

Optional write-back keys:

```toml
[github]
agent_login = "agent-bot"
agent_id = "123456"
in_progress_state = "open"
attention_state = "open"
in_progress_label = "agent-work"
attention_label = "needs-attention"
labels = []
project_owner = "acme"
project_number = 7
project_status_field = "Status"
in_progress_column = "In Progress"
attention_column = "Todo"
```

Secrets belong in environment variables or `.env`, not in config:

```sh
AGENT_TEAM_GITHUB_TOKEN=ghp_...
# or
GITHUB_TOKEN=ghp_...
# or
GH_TOKEN=ghp_...
```

## Surface

```sh
github-api.sh graphql '<query-string>' [--variables '<json>']
github-api.sh graphql --query-file <path> [--variables '<json>']
github-api.sh rest <METHOD> <path> [--data '<json>']
github-auth.sh gh <gh-args...>
github-auth.sh git <git-args...>
```

Examples:

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-api.sh graphql \
  'query { viewer { login id } }' | jq .
```

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-api.sh rest \
  GET /repos/acme/widgets/issues/42 | jq .
```

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-api.sh rest \
  POST /repos/acme/widgets/issues/42/comments \
  --data "$(jq -n --arg body 'progress update' '{body:$body}')" | jq .
```

## Recipes

### Identify the current token actor

```graphql
query { viewer { login id } }
```

Use the returned `login` as `[github].agent_login` to ignore self-authored project status moves before they redispatch the pipeline.

### Run `gh` as the configured agent

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-auth.sh gh pr list --state open
```

### Run Git against GitHub HTTPS with pinned credentials

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-auth.sh git push -u origin "$(git branch --show-current)"
```

### Fetch an issue

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-api.sh rest \
  GET /repos/<owner>/<repo>/issues/<number> | jq .
```

### Comment on an issue

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-api.sh rest \
  POST /repos/<owner>/<repo>/issues/<number>/comments \
  --data "$(jq -n --arg body 'comment body' '{body:$body}')"
```

### Update issue state

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-api.sh rest \
  PATCH /repos/<owner>/<repo>/issues/<number> \
  --data '{"state":"open"}'
```

### Add labels

```sh
"$AGENT_TEAM_ROOT"/skills/github/scripts/github-api.sh rest \
  POST /repos/<owner>/<repo>/issues/<number>/labels \
  --data '{"labels":["agent-work"]}'
```

## Failure modes

- **`GitHub not configured for this repo`**: set `[pm].provider = "github"` and fill `[github].owner` plus `[github].repo`, or use ticketless jobs.
- **`GitHub is enabled but missing config`**: fill the listed `[github]` keys in `.agent_team/config.toml`.
- **`github-api.sh: no GitHub token found`**: export `GITHUB_TOKEN` or `GH_TOKEN`, or place it in `.env`.
- **`github-auth.sh: no deterministic GitHub identity configured`**: set `[github].agent_login`, `AGENT_TEAM_GITHUB_LOGIN`, or an explicit token env (`AGENT_TEAM_GITHUB_TOKEN`, `GITHUB_TOKEN`, or `GH_TOKEN`).
- **`github-auth.sh: GitHub token actor is ... expected ...`**: the resolved token does not belong to the configured agent login. Switch to the configured account or provide the correct token.
- **HTTP 401/403**: the token is invalid or lacks the needed repo/project scopes.
