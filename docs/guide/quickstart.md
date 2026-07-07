# Quickstart

This path starts from an empty repo and does not require Linear or any other PM
tool. For the full narrative, including install options, runtime auth, feedback,
budgets, and provider setup, read [Getting Started](../getting-started.md).

```sh
mkdir my-app && cd my-app
git init
git config user.name "Agent Team Demo"
git config user.email agent-team-demo@example.com
agent-team init --set pm.provider=none --set team.pm_tool=none --no-input
git add .agent_team
git commit -m "Add agent team"
agent-team doctor --commands
agent-team daemon start --json
agent-team deployments ls
agent-team job create "Probe this repo layout and report the available agents" \
  --id gs-probe \
  --profile probe \
  --target worker \
  --dispatch \
  --workspace worktree \
  --runtime codex \
  --wait \
  --wait-status running \
  --wait-timeout 30s \
  --json
agent-team job show gs-probe --events all
agent-team job logs gs-probe --tail 80 --clean
```

`agent-team init` writes `.agent_team/` and the explicit settings above keep
both `[pm].provider` and legacy `[team].pm_tool` in ticketless mode. In that
mode, the durable job kickoff is the work item. The generated
`.agent_team/config.toml` starts with a first-run checklist showing the selected
template profile, PM provider, provider keys required now, and optional sections
that can stay blank. Commit `.agent_team/` before dispatching worktree-backed
jobs; Git needs an initial `HEAD` to create worker worktrees.

`agent-team daemon start --json` includes the loopback `http_url` for the local
daemon API and embedded dashboard. Open `<http_url>/ui/` from the same machine
when you want a browser view of instances, jobs, pipelines, budgets, and teams;
the dashboard data calls use the bearer token in `.agent_team/daemon/operator.token`.

## Linear Opt-In

To use Linear-backed tickets, opt in explicitly:

```sh
agent-team init \
  --set pm.provider=linear \
  --set team.pm_tool=linear \
  --set linear.team_id=<your-team-uuid> \
  --set linear.ticket_prefix=APP \
  --set linear.agent_column="Ready for Agent"
```

When `pm.provider = "linear"`, `linear.team_id` and `linear.ticket_prefix` are
required and validated during init. `team.pm_tool` is a deprecated
compatibility alias; setting it alongside `pm.provider` keeps older scripts and
generated configs aligned.

## GitHub Opt-In

To use GitHub Issues and GitHub Projects as the PM provider, opt in explicitly:

```sh
agent-team init \
  --set pm.provider=github \
  --set team.pm_tool=github \
  --set github.owner=<owner-or-org> \
  --set github.repo=<repo> \
  --set github.agent_column="Ready for Agent"
```

When `pm.provider = "github"`, `github.owner` and `github.repo` are required.
Set `github.project_number` when write-back should move the issue through a
GitHub Projects v2 status field. GitHub API calls use `GITHUB_TOKEN` or
`GH_TOKEN` from the environment or `.env`.
