# agent-team

`agent-team` is a massive public self-improving agent experiment: an
agent-team deployment that builds `agent-team` itself.

The project runs its own manager, workers, reviewers, auditors, docs writers,
and comms agents against this repository. Agents pick up tickets, open PRs,
review each other, record gate results, file feedback, sweep for debt, and
propose prompt and harness improvements. Humans set direction; everything
else — implementation, adversarial review, and the merge decision itself — is
agents, with distinct agents at each gate so no change is written, reviewed,
and merged by the same mind.

The software underneath that experiment is a Go CLI plus a per-repo daemon for
declaring, running, observing, and recovering teams of LLM agents.

**Status:** pre-v1. The project is actively dogfooded, released, and changing.
Command shapes and file schemas are product surface, but still allowed to move.

**Hosted docs:** [agent-team.readthedocs.io](https://agent-team.readthedocs.io) · **Community:** [Discord](https://discord.gg/sBrPh3Amc)

Read [The Self-Improving Configuration](./docs/experiment.md) for the live
delivery/platform/quality/pr/docs topology, autonomous loops, budget model, and
ticket/PR evidence behind the current experiment.

## What It Is

`agent-team` vendors an editable `.agent_team/` directory into any repo and uses
that repo-local state as the control plane for agent work.

- **Templates** package agents, skills, topology, and parameters. `agent-team
  init` renders one into `.agent_team/`.
- **Agents and skills** are plain files under `.agent_team/agents/` and
  `.agent_team/skills/`.
- **Topology** in `.agent_team/instances.toml` declares persistent managers,
  ephemeral workers, schedules, teams, triggers, budgets, and pipelines.
- **Jobs** connect tickets, branches, worktrees, PRs, validation gates, pipeline
  steps, queues, logs, and status.
- **Resource URIs** give daemon-owned state stable `agt://` identities; use
  `agent-team deployments ls|resolve` for deployment names and
  `agent-team read <agt-uri>` for daemon-mediated reads.
- **`agent-teamd`** owns runtime lifecycle for daemon-backed work: dispatch,
  stop, resume, queue, mailbox, schedule, log capture, and the embedded `/ui`
  dashboard on the loopback daemon API.
- **Provider integrations** support ticketless jobs, Linear, and GitHub
  Issues/Projects, with `agent-team ticket` for provider-backed create,
  update, comment, and close actions.
- **Runtime profiles** support Claude and Codex, with direct runs still
  available for simple sessions.

Everything is file-backed and repo-scoped. There is no database, marketplace, or
global agent registry required for normal operation. Topology authority
allowlists can audit or enforce which CLI verbs each agent may invoke, using the
agent's trusted job/team/instance origin.

## Five-Minute Quickstart

This starts from a fresh source checkout and a scratch consumer repo. You need
Go 1.22+ and a supported LLM runtime (`claude` or `codex`) on `PATH` before
dispatching real agent work. See [Getting Started](./docs/getting-started.md)
for the full install and runtime-auth walkthrough.

```sh
git clone https://github.com/agent-team-project/agent-team.git
cd agent-team

go build -o bin/agent-team ./cmd/agent-team
go build -o bin/agent-teamd ./cmd/agent-teamd
export PATH="$PWD/bin:$PATH"

mkdir -p /tmp/agent-team-demo
cd /tmp/agent-team-demo
git init
git config user.name "Agent Team Demo"
git config user.email agent-team-demo@example.com

agent-team init --minimal --set pm.provider=none --set team.pm_tool=none --no-input
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

The initial commit matters because worker dispatch creates Git worktrees. In
ticketless mode, the quoted job text is the work item. To use a board as the
dispatch control plane, initialize with Linear or GitHub provider settings
instead:

```sh
agent-team init \
  --set pm.provider=linear \
  --set linear.team_id=<your-team-uuid> \
  --set linear.ticket_prefix=APP
```

```sh
agent-team init \
  --set pm.provider=github \
  --set github.owner=<owner-or-org> \
  --set github.repo=<repo>
```

Move a Linear card or GitHub Project item into the configured agent column
(`Ready for Agent` by default) to dispatch the default ticket-to-PR pipeline.
The bundled template defaults to the slim consumer profile: manager, worker,
verifier, reviewer, and the ticket-to-PR pipeline. `agent-team init --minimal`
selects that path explicitly, and `agent-team init --minimal --dry-run ...`
previews the template, profile, and PM provider before writing `.agent_team/`.
Use `agent-team init --profile full` when you explicitly want the full
self-dogfood topology with release, docs, comms, quality, sentinel, and
product-verifier loops.

For a no-LLM local orchestration walkthrough, run the fake-runtime demo from the
source checkout:

```sh
python3 scripts/demo/local_orchestration.py bin/agent-team
```

## The Bundled Experiment

The bundled template has two profiles. Fresh consumer `agent-team init` uses the
slim starter described above. This repo self-dogfoods the full profile
(`agent-team init --profile full`, equivalent to `--set template.profile=full`),
which includes the framework's own governance and comms loops. Both profiles are
meant to be edited, replaced, or used as a starting point.

The full self-dogfood profile currently includes:

| Team | What It Runs |
| --- | --- |
| `delivery` | Manager, ticket-manager, workers, verifiers, reviewers, and the full ticket-to-PR pipeline. |
| `platform` | Separate worker/verifier/reviewer pool for framework infrastructure work. |
| `quality` | Architecture debt audits, harness review, org review, sentinels, and product verification. |
| `pr` | Public digest, release-announcement, and community-feedback agents. |
| `docs` | Docs-writing agents and the docs-freshness sweep. |

The core delivery loop is event-driven:

```text
ticket enters Ready for Agent
  -> implement worker in a worktree
  -> deterministic verifier gates + evidence
  -> adversarial reviewer
  -> manual approval gate
  -> merge / bounce / follow-up
```

Scheduled loops keep the system improving and communicating around that
delivery path:

| Loop | Cadence | Purpose |
| --- | --- | --- |
| Feedback triage | 12h | Cluster agent feedback and file, fold, or dismiss follow-up tickets. |
| Debt sweep | 24h | Audit one subsystem and file at most three evidence-backed tech-debt tickets. |
| Harness review | 12h | Turn bounce patterns, failures, and feedback into prompt/skill improvement tickets. |
| Org review | 3d | Read outcomes, spend, capacity, cycle-time, and feedback trends; propose strategic process/topology/prompt/budget tickets. |
| Sentinel | 6h | Check post-merge public surfaces and submit incident feedback when they fail. |
| Product verify | 24h | Compare daemon UI data with CLI ground truth and file capped product feedback. |
| Discord digest | 24h | Draft or publish a shipped-work digest through the sanctioned comms path. |
| Docs freshness | 24h | Audit docs against the shipped binary, latest release, repo identity, and quickstart. |

Budgets and watchdogs are part of the model. Topology can declare per-team,
per-job, and per-step token/time allowances; the daemon records usage, sends
soft budget notices, and can opt into hard cutoffs for runaway work.

## Architecture

```text
template ref or bundled default
  -> agent-team init
  -> .agent_team/{config.toml,instances.toml,agents,skills}
  -> agent-teamd
  -> events, schedules, jobs, queues, pipelines
  -> runtime instances in repo/worktree workspaces
  -> PRs, ticket write-back, logs, gates, usage, status
```

The CLI is the operator surface. The daemon is the local runtime control plane.
The repo filesystem is the database. Agents can fail, daemons can restart, and
operators can still inspect and repair work from the files under `.agent_team/`.
Stable resource URIs are the identity layer above those files: a job, instance,
workspace, mailbox, queue item, or topology can be named as an `agt://` URI and
read through the daemon without teaching callers where it is stored today.

## Install

From source:

```sh
go install github.com/agent-team-project/agent-team/cmd/agent-team@latest
go install github.com/agent-team-project/agent-team/cmd/agent-teamd@latest
```

From a release tarball:

```sh
curl -fsSL https://github.com/agent-team-project/agent-team/releases/latest/download/agent-team_<version>_darwin_arm64.tar.gz \
  | tar -xz -C /usr/local/bin agent-team agent-teamd
```

Replace the archive name for your OS and architecture. Current release assets
cover Darwin and Linux on amd64/arm64. Homebrew publishing is not enabled yet.

Verify:

```sh
agent-team --version
agent-team daemon status
```

## Documentation

Hosted docs are published at
[agent-team.readthedocs.io](https://agent-team.readthedocs.io), and the
community lives on [Discord](https://discord.gg/sBrPh3Amc) — meaningful
shipped-work digests may land in #project-updates, with at most one successful
Discord delivery in any rolling 24-hour window across all comms modes.

Start with the guide pages:

- [Getting Started](./docs/getting-started.md)
- [Quickstart](./docs/guide/quickstart.md)
- [Concepts](./docs/guide/concepts.md)
- [Architecture](./docs/guide/architecture.md)
- [Messaging](./docs/guide/messaging.md)
- [Board Control Plane](./docs/guide/board-control-plane.md)
- [Observability and Recovery](./docs/guide/observability-and-recovery.md)

Deeper references live in:

- [Templates](./docs/authoring/templates.md)
- [Topology](./docs/authoring/topology.md)
- [Daemon Runtime](./docs/runtime/daemon.md)
- [Jobs](./docs/workflows/jobs.md)
- [Pipelines and Teams](./docs/workflows/pipelines-and-teams.md)
- [Resource Model](./docs/reference/resource-model.md)
- [CLI Reference](./docs/reference/cli.md)
- [Roadmap Context](./docs/contributing/roadmap.md)
- [Changelog](./CHANGELOG.md)

The developer docs site is generated with VitePress:

```sh
agent-team docs site --commands
npm install
npm run docs:dev
npm run docs:build
```

After changing CLI commands or flags, regenerate the reference from the live
Cobra tree:

```sh
agent-team docs cli --output docs/reference/cli.generated.md
agent-team docs cli --check docs/reference/cli.generated.md
```

## Development

The main contributor loop is:

```sh
go test ./...
go build -o bin/agent-team ./cmd/agent-team
go build -o bin/agent-teamd ./cmd/agent-teamd
python3 scripts/ci/smoke_init.py bin/agent-team
```

The CI job also validates agent frontmatter, TOML, shell scripts, generated docs,
and the init smoke path. See [CLAUDE.md](./CLAUDE.md) for contributor
orientation and [testing](./docs/contributing/testing.md) for the full local
validation story.
