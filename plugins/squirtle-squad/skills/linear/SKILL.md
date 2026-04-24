---
name: linear
description: Access Linear via the GraphQL API — fetch issues, comment, update state, search, create. Use when any agent needs to read or write Linear tickets.
---

# Linear GraphQL access

Call Linear's public GraphQL API through the helper bundled with this plugin at `${CLAUDE_PLUGIN_ROOT}/scripts/linear-graphql.sh`. All team/project/label IDs that vary per consumer live in `$PWD/.agent_squad/config.toml` — the skill reads them at call time via inline Python. No IDs are hardcoded in this skill.

## Configuration

Required keys in `$PWD/.agent_squad/config.toml`:

```toml
[linear]
team_id       = "..."          # Linear team UUID
ticket_prefix = "..."          # e.g. "BENCH", "SQU"

[linear.projects]              # optional — used by ticket-manager / routing
# <project_name> = "<uuid>"

# Optional:
# initiative_id = "..."
# labels        = ["eval-harness", "..."]
```

**Canonical TOML read pattern** (use this in bash blocks):

```bash
TEAM_ID=$(python3 -c 'import tomllib; print(tomllib.load(open(".agent_squad/config.toml","rb"))["linear"]["team_id"])')
TICKET_PREFIX=$(python3 -c 'import tomllib; print(tomllib.load(open(".agent_squad/config.toml","rb"))["linear"]["ticket_prefix"])')
```

If `.agent_squad/config.toml` is missing, fail early with a clear message rather than hardcoding fallbacks — consumers must configure the squad before this skill will work. The Python one-liner raises `FileNotFoundError` on the `open` call, which produces a clear stderr message.

## Preflight: confirm Linear is the configured PM tool

Before any Linear call, run this check. It enforces that `[squad] pm_tool = "linear"` is set in `.agent_squad/config.toml` — so a consumer who wired up the wrong PM tool (or forgot to) gets a loud failure here instead of silently corrupting tickets in whichever system this skill assumes. Fail and stop if the check errors — do not proceed to any other bash in this skill.

```bash
python3 - <<'PY'
import sys, tomllib
try:
    cfg = tomllib.load(open(".agent_squad/config.toml", "rb"))
except FileNotFoundError:
    sys.exit("preflight: .agent_squad/config.toml not found in $PWD — this skill requires a configured squad.")
pm = cfg.get("squad", {}).get("pm_tool")
if pm is None:
    sys.exit('preflight: [squad].pm_tool is not set in .agent_squad/config.toml. The linear skill requires pm_tool = "linear". Jira and GitHub Issues adapters are planned for v1.1+ (see documentation/roadmap.md § parking lot "PM tool adapter pattern").')
if pm != "linear":
    sys.exit(f'preflight: [squad].pm_tool = "{pm}" but the linear skill only supports "linear". Other PM tools are planned for v1.1+ (see documentation/roadmap.md § parking lot "PM tool adapter pattern") — for now, do not invoke this skill from a repo configured for another PM tool.')
PY
```

## The helper

`${CLAUDE_PLUGIN_ROOT}/scripts/linear-graphql.sh` loads a Linear API key (prefers `LINEAR_API_KEY`, falls back to `LINEAR_USER_API_KEY`) from env or `$PWD/.env`, builds the request body with `jq`, and POSTs to `https://api.linear.app/graphql`. The raw response streams to stdout — pipe through `jq` to pretty-print or filter.

Usage:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/linear-graphql.sh" '<query-string>' [--variables '<json>']
"${CLAUDE_PLUGIN_ROOT}/scripts/linear-graphql.sh" --query-file <path> [--variables '<json>']
```

Sanity check:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/linear-graphql.sh" 'query { viewer { id name email } }' | jq .
```

## Critical rules

1. **Never modify tickets belonging to other users.** Identify yourself first (`viewer { id }`), then scope writes and `assignee` filters to that ID.
2. **Search before creating.** If the user's current tickets include a close match, comment on or update it rather than creating a duplicate.
3. **Create on the configured team.** Read `linear.team_id` from `.agent_squad/config.toml` — never embed a team UUID in your code.
4. **Use the configured ticket prefix.** When searching conversation context for ticket references, use `linear.ticket_prefix` — don't assume a specific prefix.

## Sending a GraphQL call

Short queries go inline as the positional argument; pass any GraphQL variables as a JSON string via `--variables`:

```bash
TICKET_ID="BENCH-166"  # replace with an actual ticket identifier; prefix from config
"${CLAUDE_PLUGIN_ROOT}/scripts/linear-graphql.sh" \
  'query($id: String!) { issue(id: $id) { identifier title state { name } } }' \
  --variables "$(jq -n --arg id "$TICKET_ID" '{id:$id}')" | jq .
```

For multi-line queries or mutations, write the query to a file and use `--query-file`. Build the variables JSON with `jq -n` so values are escaped safely:

```bash
cat > /tmp/linear-comment.graphql <<'EOF'
mutation($input: CommentCreateInput!) {
  commentCreate(input: $input) { success comment { id url } }
}
EOF

"${CLAUDE_PLUGIN_ROOT}/scripts/linear-graphql.sh" --query-file /tmp/linear-comment.graphql \
  --variables "$(jq -n \
      --arg issueId "$ISSUE_UUID" \
      --arg body "progress update" \
      '{input:{issueId:$issueId, body:$body}}')" | jq .
```

## Recipe snippets

### Identify the current user

```graphql
query { viewer { id name email } }
```

### Fetch an issue with comments

```graphql
query($id: String!) {
  issue(id: $id) {
    identifier title url description
    state { name } priorityLabel
    assignee { id name email }
    labels { nodes { name } }
    comments(first: 50) { nodes { body user { name } createdAt } }
  }
}
```

Variables: `{ "id": "<prefix>-<n>" }` — e.g. `BENCH-166` or `SQU-13`. The `id` argument accepts either the identifier or the UUID.

### Search your own issues

```graphql
query($viewerId: ID!) {
  issues(
    filter: { assignee: { id: { eq: $viewerId } } }
    first: 25
    orderBy: updatedAt
  ) { nodes { identifier title state { name } url } }
}
```

Pass `viewerId` from a prior `viewer { id }` call. Add `state: { name: { eq: "Backlog" } }` inside `filter` to narrow by status.

### Comment on an issue

Mutations that operate on an issue need its UUID `id`, not the identifier — fetch it first with `issue(id: "<prefix>-<n>") { id }`.

```graphql
mutation($input: CommentCreateInput!) {
  commentCreate(input: $input) { success comment { id url } }
}
```

Variables: `{ "input": { "issueId": "<uuid>", "body": "progress update" } }`.

### Update state or description

```graphql
mutation($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) {
    success issue { identifier state { name } }
  }
}
```

State IDs are team-scoped. Look them up once per team:

```graphql
query($teamId: ID!) {
  workflowStates(filter: { team: { id: { eq: $teamId } } }) {
    nodes { id name }
  }
}
```

Common state names: `Backlog`, `Todo`, `In Progress`, `In Review`, `Done`, `Canceled`. Exact names and IDs depend on the team's workflow.

### Create an issue

```graphql
mutation($input: IssueCreateInput!) {
  issueCreate(input: $input) { success issue { identifier url } }
}
```

Variables (build with `jq -n` so values are escaped):

```bash
TEAM_ID=$(python3 -c 'import tomllib; print(tomllib.load(open(".agent_squad/config.toml","rb"))["linear"]["team_id"])')
VARIABLES="$(jq -n \
    --arg teamId "$TEAM_ID" \
    --arg title "..." \
    --arg desc "..." \
    '{input:{teamId:$teamId, title:$title, description:$desc}}')"
```

To apply labels, resolve their UUIDs once per team:

```graphql
query($teamId: ID!) {
  issueLabels(filter: { team: { id: { eq: $teamId } } }) {
    nodes { id name }
  }
}
```

## Failure modes

- **`preflight: ...` error on `[squad].pm_tool`** — the consumer repo either has no `pm_tool` set or has it set to something other than `"linear"`. Fix `.agent_squad/config.toml`; the linear skill will refuse to run until it reads `[squad] pm_tool = "linear"`. Do not comment out the preflight.
- **`.agent_squad/config.toml` not found** (or `KeyError` for `linear.team_id`) — the consumer repo is not configured for squirtle-squad. Create the file with at least `[linear].team_id` and `[linear].ticket_prefix`.
- **`linear-graphql.sh: no Linear API key found`** — the helper couldn't locate a key. Ensure `LINEAR_API_KEY` or `LINEAR_USER_API_KEY` is exported, or present in `$PWD/.env`.
- **`AuthenticationError` from Linear** — key is present but rejected. Regenerate it.
- **`Entity not found` on a mutation** — you passed the identifier (e.g. `BENCH-166`) where a UUID was required. Resolve the UUID first via `issue(id: "<identifier>") { id }`.
- **GraphQL validation error about `String!` vs `ID!`.** Linear's schema is strict about scalar types. Two rules:
  - **`ID!` when filtering on an `id` field** — e.g. `filter: { team: { id: { eq: $teamId } } }` or `filter: { assignee: { id: { eq: $viewerId } } }`. The `id` field returns `ID!`, and the filter's `eq` must match.
  - **`String!` when used as an `issue(id: ...)` argument** — the top-level `issue(id:)` takes `String!`, which accepts both the identifier (`BENCH-166`) and the UUID. Same for `issueUpdate(id:)`.
  Mnemonic: *filtering by id → `ID!`; looking up by id → `String!`*.
