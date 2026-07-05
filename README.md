# agent-team

`agent-team` is a massive public self-improving agent experiment: an
agent-team deployment that builds `agent-team` itself.

The project runs its own manager, workers, reviewers, auditors, docs writers,
and comms agents against this repository. Agents pick up tickets, open PRs,
review each other, record gate results, file feedback, sweep for debt, and
propose prompt and harness improvements. Humans sit at exactly two gates:
setting direction and approving merges.

The software underneath that experiment is a Go CLI plus a per-repo daemon for
declaring, running, observing, and recovering teams of LLM agents.

**Status:** pre-v1. The project is actively dogfooded, released, and changing.
Command shapes and file schemas are product surface, but still allowed to move.

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
- **`agent-teamd`** owns runtime lifecycle for daemon-backed work: dispatch,
  stop, resume, queue, mailbox, schedule, and log capture.
- **Provider integrations** support ticketless jobs, Linear, and GitHub
  Issues/Projects.
- **Runtime profiles** support Claude and Codex, with direct runs still
  available for simple sessions.

Everything is file-backed and repo-scoped. There is no database, marketplace, or
global agent registry required for normal operation.

## Five-Minute Quickstart

This starts from a fresh source checkout and a scratch consumer repo. You need
Go 1.22+ and a supported LLM runtime (`claude` or `codex`) on `PATH` before
dispatching real agent work.

```sh
git clone https://github.com/agent-team-project/agent-team.git
cd agent-team

go build -o bin/agent-team ./cmd/agent-team
go build -o bin/agent-teamd ./cmd/agent-teamd
export PATH="$PWD/bin:$PATH"

mkdir -p /tmp/agent-team-demo
cd /tmp/agent-team-demo
git init

agent-team init
agent-team doctor --commands
agent-team daemon start
agent-team job create "fix the flaky login test" --dispatch --workspace worktree
agent-team job show <job-id> --events all
agent-team logs --job <job-id> --follow
```

`agent-team init` defaults to `[pm].provider = "none"`, so the quoted job text
is the work item. To use a board as the dispatch control plane, initialize with
Linear or GitHub provider settings instead:

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

For a no-LLM local orchestration walkthrough, run the fake-runtime demo from the
source checkout:

```sh
python3 scripts/demo/local_orchestration.py bin/agent-team
```

## The Bundled Experiment

The default template ships the team that this repo uses on itself. It is meant
to be edited, replaced, or used as a starting point.

| Team | What It Runs |
| --- | --- |
| `delivery` | Manager, ticket-manager, workers, reviewers, and the default ticket-to-PR pipeline. |
| `platform` | Separate worker/reviewer pool for framework infrastructure work. |
| `quality` | Architecture debt audits and harness-review work. |
| `pr` | Public digest, release-announcement, community-feedback, and docs-writing agents. |

The core delivery loop is event-driven:

```text
ticket enters Ready for Agent
  -> implement worker in a worktree
  -> adversarial reviewer
  -> manual approval gate
  -> merge / bounce / follow-up
```

Four scheduled loops keep the system improving around that delivery path:

| Loop | Cadence | Purpose |
| --- | --- | --- |
| Feedback triage | 12h | Cluster agent feedback and file, fold, or dismiss follow-up tickets. |
| Debt sweep | weekly | Audit one subsystem and file at most three evidence-backed tech-debt tickets. |
| Harness review | weekly | Turn bounce patterns, failures, and feedback into prompt/skill improvement tickets. |
| Discord digest | daily | Draft or publish a shipped-work digest through the sanctioned comms path. |

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

Start with the guide pages:

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
