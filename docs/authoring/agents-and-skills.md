# Agents and Skills

Agents and skills are the runtime-visible content installed by a template.

## Agent Layout

An agent directory looks like:

```text
.agent_team/agents/worker/
├── agent.md
├── config.toml
└── skills/
    └── agent-private-skill/
        └── SKILL.md
```

`agent.md` contains frontmatter plus the system prompt body.

```md
---
description: Implements one assigned engineering task in an isolated worktree.
runtime: codex
runtime_bin: /opt/bin/codex-wrapper
---

You are the worker agent...
```

The directory name becomes the agent name.

## Frontmatter

The loader uses a small stdlib parser, not a full YAML runtime dependency.

Supported frontmatter values are scalar and block-scalar fields needed by agent definitions. Keep the metadata simple and avoid relying on advanced YAML constructs.

`description` becomes the runtime-facing agent description.

`runtime` can be `claude` or `codex`, and `runtime_bin` can point at a binary
or wrapper for that runtime. These agent-level defaults apply when dispatch does
not pass an explicit runtime and `AGENT_TEAM_RUNTIME` is not set. Repo runtime
config remains the fallback after agent frontmatter. Use
`agent-team agent doctor --strict`,
`agent-team pipeline doctor --strict` or
`agent-team team doctor --strict` to fail fast when an agent or pipeline
route would rely on an unavailable agent-level runtime binary.

## Inspecting Installed Agents

Use the CLI to inspect the definitions installed in a repo before launching one:

```sh
agent-team agent ls
agent-team agent show worker
agent-team agent doctor worker
```

These commands accept `--json` for automation and `--format` for shell-friendly output. The doctor command also accepts `--strict` (or `--strict-runtime`) to promote invalid or unavailable agent runtime defaults to failures. `agent doctor --json` includes top-level `actions` matching `--commands` for focused follow-ups. The plural alias also works:

```sh
agent-team agents ls --format '{{.Name}} {{.Runtime}} {{.RuntimeBin}} {{len .Skills}}'
```

## Skills Ownership Model

Skills can be registered through three ownership layers:

| Layer | Declared in | Scope |
| --- | --- | --- |
| Per-agent | `.agent_team/agents/<agent>/skills/<skill>/` | Only agents using that agent definition |
| Extra | `.agent_team/agents/<agent>/config.toml` `[skills].extra` | Shared skills pulled into one agent definition |
| Team | `.agent_team/config.toml` `[skills].team` | Shared skills registered for every launched agent |

Per-agent skills are useful for private workflow affordances that should travel
with one agent definition. Extra skills are shared capabilities that only some
agents should see. Team skills are shared capabilities that must not disappear
when one instance or agent role is retired, such as the status, inbox, or PM-tool
integration skills a whole deployment depends on.

Team skills must be names under `.agent_team/skills/`:

```toml
[skills]
team = ["linear", "status"]
```

The loader adds team skills after per-agent `disable` handling, so they are
available to every launched agent independent of each agent's `extra` list.

`agent-team instance rm` protects this model: when removing a declared instance
would leave a non-team skill with no declared instance using it, the command
prints the orphaned skill names and requires `--force`. Move shared capabilities
to `[skills].team` before retiring their last declared owner.

## Agent Config

`config.toml` assigns shared skills:

```toml
[skills]
extra = ["linear", "pull-request", "status"]
```

The loader resolves:

1. agent-private skills under `<agent>/skills/`
2. shared skills under `.agent_team/skills/`
3. arbitrary paths listed in config
4. team skills under `.agent_team/skills/`

## Skill Layout

A skill directory contains a `SKILL.md` entrypoint and optional supporting files.

```text
.agent_team/skills/status/
├── SKILL.md
└── scripts/
    ├── status.sh
    └── _status_write.py
```

Bundled skills include:

| Skill | Purpose |
| --- | --- |
| `linear` | Query or mutate Linear through configured team metadata |
| `pull-request` | Guide PR creation and review handoff |
| `status` | Write `status.toml` for daemon/operator visibility |
| `inbox` | Read daemon mailbox messages |
| `channel` | Pub/sub channel helpers |
| `assign-worker` | Manager workflow for assigning work to workers |

## Runtime Registration

`agent-team run <agent>`:

1. loads every installed agent
2. resolves each agent's prompt and description
3. builds a runtime-specific agent registry
4. creates a temporary discovery tree for selected skills
5. writes a kickoff prompt file
6. creates `.agent_team/state/<instance>/`
7. execs the selected runtime

Environment passed to the runtime includes:

| Variable | Meaning |
| --- | --- |
| `AGENT_TEAM_ROOT` | Absolute path to `.agent_team/` |
| `AGENT_TEAM_INSTANCE` | Current instance name |
| `AGENT_TEAM_STATE_DIR` | Absolute path to the current instance state dir |
| `AGENT_TEAM_DAEMON_SOCKET` | Resolved Unix socket path for `agent-teamd`; falls back to `.agent_team/daemon.sock` on short paths |
| `AGENT_TEAM_DAEMON_URL` | Optional loopback HTTP base URL when `agent-teamd` was started with `--http-addr`; helpers should prefer it when Unix sockets are blocked |

## Status Reporting

Agents should write status through the status skill.

`status.toml` lets operators and repair commands understand:

- current phase
- human-readable description
- whether work is blocked
- job/ticket ownership
- branch and PR metadata

Typical phases include:

- `planning`
- `implementing`
- `awaiting_review`
- `blocked`
- `idle`
- `done`

Status files are read by `ps`, `inspect`, `monitor`, `health --jobs`, `job reconcile status`, `job triage`, and team-scoped views.

## Skill Design Guidelines

Good skills should:

- be explicit about required config
- use structured files where possible
- avoid hidden global state
- write durable status when they make long-running progress
- fail loudly when required external tools are missing
- be safe to run from worktrees
- keep scripts portable and dependency-light

## Code Areas

Agent and skill loading lives mostly in:

- `internal/loader/frontmatter.go`
- `internal/loader/loader.go`
- `internal/cli/run.go`
- `internal/cli/runtime.go`
- `template/agents/`
- `template/skills/`
