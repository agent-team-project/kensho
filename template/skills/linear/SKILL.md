---
name: linear
description: Access Linear via the GraphQL API when the repo is configured for Linear — fetch issues, comment, update state, search, create. Use when any agent needs to read or write Linear tickets.
---

# Linear GraphQL access

Call Linear's public GraphQL API through the helper bundled with this skill at `${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh`. All team/project/label IDs that vary per consumer live in `$PWD/.agent_team/config.toml` — the helper checks that Linear is enabled before it sends any request. No IDs are hardcoded in this skill.

For normal PM ticket writes, prefer the provider-abstracted CLI surface:

```sh
agent-team ticket create|update|comment|close ...
```

Use this Linear helper for low-level reads/searches, provider-specific operations that the ticket verb does not expose yet, and backward reference while older prompts are being migrated. Do not choose it as the default create/update/comment/close path.

## Configuration

Linear is optional. Ticketless repos use:

```toml
[pm]
provider = "none"

[team]
pm_tool = "none"
```

For Linear-backed repos, required keys in `$PWD/.agent_team/config.toml` are:

```toml
[pm]
provider = "linear"

[team]
pm_tool = "linear"             # deprecated alias for [pm].provider

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
TEAM_ID=$(python3 -c 'import tomllib; print(tomllib.load(open(".agent_team/config.toml","rb"))["linear"]["team_id"])')
TICKET_PREFIX=$(python3 -c 'import tomllib; print(tomllib.load(open(".agent_team/config.toml","rb"))["linear"]["ticket_prefix"])')
```

If `.agent_team/config.toml` is missing or `[pm].provider` (falling back to `[team].pm_tool` for legacy configs) is not `"linear"`, stop and report the helper's message. Do not hardcode fallbacks or continue with partial Linear behavior. For ticketless work, point the user at:

```sh
agent-team job create "<kickoff>" --dispatch --workspace worktree
```

## Preflight: confirm Linear is the configured PM tool

The helper enforces that `[pm] provider = "linear"` is set (or legacy `[team] pm_tool = "linear"` is present) and that `[linear].team_id` and `[linear].ticket_prefix` are non-empty. A ticketless repo gets an actionable error instead of a Python traceback. Fail and stop if the helper reports an unconfigured repo — do not proceed to any other bash in this skill.

```bash
"${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh" 'query { viewer { id } }' | jq .
```

## The helper

`${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh` loads a Linear API key (prefers `LINEAR_API_KEY`, falls back to `LINEAR_USER_API_KEY`) from env or `$PWD/.env`, builds the request body with `jq`, and POSTs to `https://api.linear.app/graphql`. For `commentCreate` and `issueCreate` mutations, it appends the machine-readable `agent-team-origin: ...` footer when project/dispatch origin context is available in `.agent_team/config.toml` and `AGENT_TEAM_*`. The raw response streams to stdout — pipe through `jq` to pretty-print or filter.

Usage:

```bash
"${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh" '<query-string>' [--variables '<json>']
"${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh" --query-file <path> [--variables '<json>']
```

Sanity check:

```bash
"${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh" 'query { viewer { id name email } }' | jq .
```

## Critical rules

1. **Never modify tickets belonging to other users.** Identify yourself first (`viewer { id }`), then scope writes and `assignee` filters to that ID.
2. **Search before creating.** If the user's current tickets include a close match, comment on or update it rather than creating a duplicate.
3. **Create on the configured team.** Read `linear.team_id` from `.agent_team/config.toml` — never embed a team UUID in your code.
4. **Use the configured ticket prefix.** When searching conversation context for ticket references, use `linear.ticket_prefix` — don't assume a specific prefix.

## Sending a GraphQL call

Short queries go inline as the positional argument; pass any GraphQL variables as a JSON string via `--variables`:

```bash
TICKET_ID="BENCH-166"  # replace with an actual ticket identifier; prefix from config
"${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh" \
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

"${AGENT_TEAM_ROOT}/skills/linear/scripts/linear-graphql.sh" --query-file /tmp/linear-comment.graphql \
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
TEAM_ID=$(python3 -c 'import tomllib; print(tomllib.load(open(".agent_team/config.toml","rb"))["linear"]["team_id"])')
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

- **`Linear not configured for this repo`** — `[pm].provider` is absent, `"none"`, or another value (legacy `[team].pm_tool` is accepted when `[pm].provider` is absent). For ticketless work, use `agent-team job create "<kickoff>" --dispatch --workspace worktree`. To enable Linear, set `[pm].provider = "linear"` plus `[linear].team_id` and `[linear].ticket_prefix`.
- **`Linear is enabled but missing config`** — `[pm].provider = "linear"` but one or both required `[linear]` fields are empty. Fill them in or re-run init with `--set pm.provider=linear --set linear.team_id=<uuid> --set linear.ticket_prefix=<PREFIX>`.
- **`linear-graphql.sh: no Linear API key found`** — the helper couldn't locate a key. Ensure `LINEAR_API_KEY` or `LINEAR_USER_API_KEY` is exported, or present in `$PWD/.env`.
- **`AuthenticationError` from Linear** — key is present but rejected. Regenerate it.
- **`Entity not found` on a mutation** — you passed the identifier (e.g. `BENCH-166`) where a UUID was required. Resolve the UUID first via `issue(id: "<identifier>") { id }`.
- **GraphQL validation error about `String!` vs `ID!`.** Linear's schema is strict about scalar types. Two rules:
  - **`ID!` when filtering on an `id` field** — e.g. `filter: { team: { id: { eq: $teamId } } }` or `filter: { assignee: { id: { eq: $viewerId } } }`. The `id` field returns `ID!`, and the filter's `eq` must match.
  - **`String!` when used as an `issue(id: ...)` argument** — the top-level `issue(id:)` takes `String!`, which accepts both the identifier (`BENCH-166`) and the UUID. Same for `issueUpdate(id:)`.
  Mnemonic: *filtering by id → `ID!`; looking up by id → `String!`*.
