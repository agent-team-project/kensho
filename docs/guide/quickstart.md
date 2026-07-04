# Quickstart

This path starts from an empty repo and does not require Linear or any other PM tool.

```sh
mkdir my-app && cd my-app
git init
agent-team init
agent-team daemon start
agent-team job create "fix the flaky login test" --dispatch --workspace worktree
agent-team job show <job-id>
agent-team logs --job <job-id> --follow
```

`agent-team init` writes `.agent_team/` and defaults `[team].pm_tool` to `"none"`. In that mode, the durable job kickoff is the work item. `job create` prints the normalized job id; use that id with `job show`, `job logs`, or `logs --job`.

## Linear Opt-In

To use Linear-backed tickets, opt in explicitly:

```sh
agent-team init \
  --set team.pm_tool=linear \
  --set linear.team_id=<your-team-uuid> \
  --set linear.ticket_prefix=APP
```

When `team.pm_tool = "linear"`, `linear.team_id` and `linear.ticket_prefix` are required and validated during init. Passing `linear.*` values without `team.pm_tool` still enables Linear for compatibility with older setup commands.

## GitHub Opt-In

To use GitHub Issues and GitHub Projects as the PM provider, opt in explicitly:

```sh
agent-team init \
  --set pm.provider=github \
  --set github.owner=<owner-or-org> \
  --set github.repo=<repo>
```

When `pm.provider = "github"`, `github.owner` and `github.repo` are required.
Set `github.project_number` when write-back should move the issue through a
GitHub Projects v2 status field. GitHub API calls use `GITHUB_TOKEN` or
`GH_TOKEN` from the environment or `.env`.
