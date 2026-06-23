# Concepts

This page defines the terms used throughout the code and docs.

## Template

A template is a versioned, parameterized directory tree containing agents, skills, an optional topology, and a `template.toml` manifest.

Templates are instantiated into a consumer repo by `agent-team init`. The bundled default template is embedded into the binary via `go:embed` and provides a software-engineering team with:

- `ticket-manager`
- `manager`
- ephemeral `worker`
- `linear`, `pull-request`, `status`, `inbox`, `channel`, and `assign-worker` style skills

Template content is copied into `.agent_team/`; files ending in `.tmpl` are rendered with resolved parameters.

## Repo

A repo is the consumer workspace that owns a `.agent_team/` directory.

The repo is the unit of orchestration. Its `.agent_team/` directory contains:

- agent definitions
- skill definitions
- resolved config
- topology
- job files
- runtime state
- daemon metadata
- queues
- intake delivery history
- logs and events

## Agent

An agent is an authored definition under `.agent_team/agents/<name>/`.

Each agent has:

- `agent.md`: YAML-ish frontmatter plus prompt body
- `config.toml`: local agent configuration, including extra skills
- optional `skills/`: agent-private skills

The CLI loads every agent and registers the team with the selected runtime. The launched agent becomes the primary session while other agents can be registered as subagents or dispatched through the daemon.

## Skill

A skill is a directory with a `SKILL.md` entrypoint and optional scripts.

Skills can be:

- agent-private: `.agent_team/agents/<agent>/skills/<skill>/SKILL.md`
- shared: `.agent_team/skills/<skill>/SKILL.md`
- arbitrary paths referenced from an agent `config.toml`

The launcher builds a temporary runtime discovery directory so selected skills are visible to the runtime.

## Instance

An instance is one runtime spawn of an agent.

One agent can have many instances. For example, `worker-squ-42`, `worker-squ-43`, and `manager` can all run from the same agent definitions.

Instances have:

- name
- agent
- lifecycle status
- process metadata
- workspace
- state dir under `.agent_team/state/<instance>/`
- optional status file
- logs
- mailbox messages

## Persistent Instance

A persistent instance is long-lived and restartable. It is usually declared in `instances.toml`.

Examples:

- `manager`
- `ticket-manager`
- long-running review or triage agent

Persistent instances are brought up by `agent-team start` or `agent-team sync` and are expected to resume previous conversation state when the runtime supports it.

## Ephemeral Instance

An ephemeral instance is spawned for one unit of work, then exits.

The bundled `worker` is the canonical ephemeral agent. It usually runs in a fresh worktree and is associated with a job, ticket, branch, and PR.

## Workspace

A workspace is the directory an instance operates in.

Supported workspace modes include:

- `repo`: use the repo root
- `worktree`: create/use an isolated git worktree
- `auto`: choose based on target and topology

Workers normally use worktrees. Managers and ticket managers normally use the repo root.

## Job

A job is a durable work unit stored as `.agent_team/jobs/<job-id>.toml`.

Jobs are the main product abstraction for work:

- ticket id and URL
- target agent
- current instance
- lifecycle status
- branch and worktree
- PR URL
- pipeline name and step state
- last event and last status
- created/updated timestamps

Jobs let operators ask "what work exists?" instead of only "which process is running?"

## Pipeline

A pipeline is a declared sequence of job steps in `instances.toml`.

The initial engine supports simple dependency edges through `after = [...]`. It is intentionally not a complex DAG engine yet.

Pipeline state is recorded in the job file, so `job show`, `job ready`, `job advance`, `pipeline status`, and team-scoped commands can reason about the next step. Gates such as `manual` and `pr` are also stored on job steps, making waiting reasons visible in the same commands.
Skipped steps are stored as `done` with `skipped = true`, which keeps dependency handling simple while preserving the fact that an operator bypassed a stage.

## Team

A team groups declared instances, pipelines, and schedules.

Teams are an operator scoping construct. They make it possible to run:

```sh
agent-team team overview delivery
agent-team team tick delivery --dry-run
agent-team team queue retry delivery --all --job SQU-42
agent-team team snapshot delivery --output delivery-diagnostics.json
```

without affecting unrelated instances or jobs in the same repo.

## Queue Item

A queue item is a persisted dispatch request under `.agent_team/daemon/queue/`.

Queue items can be:

- `pending`: waiting for capacity or retry
- `dead`: failed beyond retry limits
- quarantined: moved out of the active queue for inspection

Queue entries include event type, payload, target instance, attempts, last error, and retry timing.

## Intake Delivery

An intake delivery records an external event submission.

Examples:

- Linear webhook
- GitHub webhook
- schedule event

Deliveries are normalized, stored, and can be replayed or pruned. This gives webhook handling an audit trail and avoids direct ad-hoc spawning.
