# Getting Started

This guide starts from an empty repository and ends with a repo-local agent
team that can run a manager, dispatch a worker, inspect budget state, submit
feedback, and preview the bundled `ticket_to_pr` pipeline.

Every shell command in this page was run in a fresh `/tmp` repo, source checkout,
or install directory while writing the guide. The one exception is provider
credentials: the setup commands are verified, but the Linear and GitHub API
tokens are yours.

## What You Need

- Git.
- Go 1.22+ if you install from source or build locally.
- `agent-team` and `agent-teamd` on `PATH`.
- One supported runtime on `PATH`: `claude` or `codex`.

`agent-team` does not authenticate to model providers itself. It launches the
runtime binary you selected:

- Claude Code can use the normal Claude subscription login. API-key and
  third-party provider modes are also runtime concerns, not `agent-team` config.
- Codex can use an interactive/subscription login or an OpenAI API-key login.
  The local `codex login --help` output shows the API-key mode as
  `--with-api-key`.

Provider integrations are separate from runtime auth:

- Linear helpers read `LINEAR_API_KEY` or `LINEAR_USER_API_KEY` from the
  environment or `.env`.
- GitHub helpers read `GITHUB_TOKEN` or `GH_TOKEN` from the environment or
  `.env`.

## Install

The release tarball path is the simplest way to get the two binaries. Set
`OS` and `ARCH` for your machine; release assets currently use `darwin` or
`linux` and `amd64` or `arm64`.

```sh
mkdir -p /tmp/agent-team-install
VERSION="$(gh release list --repo agent-team-project/agent-team --limit 1 --json tagName --jq '.[0].tagName')"
OS=darwin
ARCH=arm64
gh release download "$VERSION" \
  --repo agent-team-project/agent-team \
  --pattern "agent-team_${VERSION#v}_${OS}_${ARCH}.tar.gz" \
  --dir /tmp/agent-team-install
tar -xzf "/tmp/agent-team-install/agent-team_${VERSION#v}_${OS}_${ARCH}.tar.gz" \
  -C /tmp/agent-team-install \
  agent-team agent-teamd
export PATH="/tmp/agent-team-install:$PATH"
agent-team --version
agent-teamd --version
```

If you want the current source version instead of the latest release, install
from `main` with Go:

```sh
mkdir -p /tmp/agent-team-install-go-main
GOBIN=/tmp/agent-team-install-go-main \
  GOPRIVATE=github.com/agent-team-project/* \
  GONOSUMDB=github.com/agent-team-project/* \
  GONOPROXY=github.com/agent-team-project/* \
  go install github.com/agent-team-project/agent-team/cmd/agent-team@main
GOBIN=/tmp/agent-team-install-go-main \
  GOPRIVATE=github.com/agent-team-project/* \
  GONOSUMDB=github.com/agent-team-project/* \
  GONOPROXY=github.com/agent-team-project/* \
  go install github.com/agent-team-project/agent-team/cmd/agent-teamd@main
export PATH="/tmp/agent-team-install-go-main:$PATH"
agent-team --version
agent-teamd --version
```

The `GOPRIVATE` settings are harmless for authenticated/private installs and can
be omitted when the module is publicly reachable through the Go proxy.

## Initialize A Ticketless Repo

Start with ticketless mode. In this mode, the quoted job kickoff is the durable
work item. There is no Linear or GitHub issue lookup yet.

```sh
mkdir -p /tmp/agent-team-demo
cd /tmp/agent-team-demo
git init
git config user.name "Agent Team Demo"
git config user.email agent-team-demo@example.com
agent-team init --set pm.provider=none --set team.pm_tool=none --no-input
git add .agent_team
git commit -m "Add agent team"
```

The commit matters. Worker dispatch with `--workspace worktree` creates a Git
worktree, and Git cannot create a worktree from a repository with no `HEAD`.

Check the generated team:

```sh
agent-team doctor --commands
agent-team daemon start --json
agent-team daemon status --format '{{.Ready}} {{.PID}}'
```

`doctor --commands` prints the next safe operator commands when something is
missing. A valid fresh repo usually suggests starting the daemon and previewing
`sync`.

## Check Runtime Auth

List the runtime profiles that `agent-team` can see:

```sh
agent-team runtime ls --format '{{.Runtime}} {{.Binary}} available={{.Available}} selected={{.Selected}}'
agent-team runtime probe --runtime codex --skip-doctor --json
```

Use `--runtime claude` instead of `--runtime codex` when Claude Code is the
runtime you intend to use. The probe checks binary availability and daemon
state. For Codex, the full probe can also run native Codex diagnostics and a
minimal real `codex exec` when you omit `--skip-doctor` and add `--exec`.

Run the manager once as a bounded Codex one-shot:

```sh
agent-team run manager --runtime codex --prompt "Reply exactly: manager online" --last-message
```

The manager is now running with the vendored agent definitions and skills. The
`--last-message` flag prints the clean final Codex response for a one-shot
prompt.

## Dispatch Your First Job

Use a probe job for the first daemon-backed worker dispatch. Probe jobs are
report-only: the worker may read and run inspection commands, but it should not
edit the repo or open a PR.

```sh
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
```

The useful handoff point is `running`. A real runtime can take minutes to finish
even a small probe, so do not make your first automation wait for terminal state
unless you are prepared for that runtime cost.

Inspect the job and logs:

```sh
agent-team job show gs-probe --events all
agent-team logs --job gs-probe --tail 80 --clean
```

Budget rows appear once a job has an allowance. The bundled worker instance
declares a 40M token, 45 minute soft allowance, so the job row is visible even
before usage is captured:

```sh
agent-team budget status --job gs-probe
```

If this was only a smoke test and you do not want the probe to keep running,
stop the job-owned instance:

```sh
agent-team job stop gs-probe --wait --timeout 10s --json
```

## Submit Feedback

Agents and operators can leave local harness feedback without filing a ticket
manually. The feedback store is local under `.agent_team/`; the bundled
feedback-triage schedule later clusters new items and routes them.

```sh
agent-team feedback submit --category docs "getting-started tutorial smoke"
agent-team feedback ls --status new --group
```

Use feedback for observations about the agent harness itself: unclear prompts,
missing recovery commands, flaky dispatch behavior, or docs gaps.

## Preview The Ticket-To-PR Pipeline

The default template includes `ticket_to_pr`: implement, review, then a manual
approval gate. Preview the pipeline before letting it start a full worker.

```sh
agent-team pipeline run ticket_to_pr DEMO-1 "Implement the first demo ticket" --dry-run --dispatch --commands
agent-team pipeline run ticket_to_pr DEMO-1 "Implement the first demo ticket" --dry-run --dispatch --json
```

The command-only preview prints the apply command that would create the job and
dispatch the first ready step. The JSON preview shows the step state and the
`agent.dispatch` payload, including the generated worker instance name and
`workspace = "worktree"`.

For a no-spend end-to-end exercise of the pipeline mechanics, run the bundled
fake-runtime demo from an `agent-team` source checkout:

```sh
python3 scripts/demo/local_orchestration.py bin/agent-team --runtime codex
```

That demo initializes a temporary repo, installs a fake runtime, creates a
`ticket_to_pr` job, drains the ready work, marks the fake worker complete, and
checks the operator views. It does not call a real LLM service.

When you are ready for the real ticket-to-PR loop, run the command printed by
the dry-run preview. That starts live runtime work and can open a PR depending
on the worker result.

## Upgrade To Linear Or GitHub

Ticketless mode is the safest first run. For a new repo that should use a PM
provider immediately, initialize with provider parameters.

Linear:

```sh
mkdir -p /tmp/agent-team-linear
cd /tmp/agent-team-linear
git init
agent-team init \
  --set pm.provider=linear \
  --set team.pm_tool=linear \
  --set linear.team_id=00000000-0000-0000-0000-000000000000 \
  --set linear.ticket_prefix=APP \
  --set linear.agent_column="Ready for Agent" \
  --no-input
```

Replace the all-zero team id with your Linear team UUID. With Linear enabled,
workers treat `APP-123` or a Linear issue URL as the work item and the helpers
use your Linear API key for ticket reads and write-back.

GitHub Issues and Projects:

```sh
mkdir -p /tmp/agent-team-github
cd /tmp/agent-team-github
git init
agent-team init \
  --set pm.provider=github \
  --set team.pm_tool=github \
  --set github.owner=acme \
  --set github.repo=demo \
  --set github.agent_column="Ready for Agent" \
  --no-input
```

Replace `acme/demo` with your repository. Add `github.project_number` and
related project settings when GitHub Projects v2 status write-back should move
items between columns.

For an existing ticketless repo, make the same provider changes in
`.agent_team/config.toml`, then run `agent-team doctor --commands` before
dispatching provider-backed work.

## Where To Go Next

- Read [Concepts](./guide/concepts.md) for the object model.
- Read [Jobs](./workflows/jobs.md) for durable job state and recovery commands.
- Read [Pipelines and Teams](./workflows/pipelines-and-teams.md) for
  `ticket_to_pr`, gates, retries, and team-scoped operations.
- Read [Runtime Profiles](./runtime/profiles.md) before standardizing on
  Claude or Codex for a team.
