# Repository Layout

This page maps the source tree to the product areas.

## Entrypoints

| Path | Purpose |
| --- | --- |
| `cmd/agent-team/main.go` | User-facing CLI binary |
| `cmd/agent-teamd/main.go` | Per-repo daemon binary |
| `embed.go` | Embeds the bundled `template/` tree |

## Core Packages

| Package | Responsibility |
| --- | --- |
| `internal/cli` | Cobra commands, renderers, operator workflows, CLI clients |
| `internal/daemon` | Daemon process, HTTP API, instance manager, queue, scheduler, metadata, events |
| `internal/job` | Durable job model, TOML persistence, events, reconciliation |
| `internal/topology` | `instances.toml` schema, triggers, pipelines, teams, schedules |
| `internal/template` | Template manifest, config resolution, rendering, ref cache, provenance |
| `internal/loader` | Agent loading, frontmatter parsing, skill resolution |
| `internal/intake` | Normalized external event model |
| `internal/runtimebin` | Runtime binary discovery and profile support |

## Template Assets

| Path | Purpose |
| --- | --- |
| `template/template.toml` | Bundled template manifest |
| `template/instances.toml.tmpl` | Bundled topology defaults |
| `template/agents/` | Bundled agent definitions |
| `template/skills/` | Bundled shared skills |

The repo self-dogfoods the template through `.agent_team/agents` and `.agent_team/skills` symlinks.

## Documentation

| Path | Purpose |
| --- | --- |
| `README.md` | User-facing overview and command survey |
| `AGENTS.md` | Contributor orientation and project rules |
| `documentation/templates.md` | Template architecture notes |
| `documentation/orchestrator.md` | Daemon/orchestrator architecture notes |
| `documentation/topology.md` | Topology architecture notes |
| `docs/` | VitePress developer documentation site |

## CI and Validation

| Path | Purpose |
| --- | --- |
| `.github/workflows/ci.yml` | Go tests, validators, smoke, shellcheck |
| `scripts/ci/validate_frontmatter.py` | Agent frontmatter validator |
| `scripts/ci/validate_toml.py` | TOML validator |
| `scripts/ci/smoke_init.py` | End-to-end init/doctor/daemon smoke |

## Generated or Local-Only Paths

These paths are not source of truth and should not be committed:

- `bin/`
- `node_modules/`
- `.agent_team/daemon/`
- `.agent_team/state/`
- `.worker_agent/`
- `dist/`
- `.vitepress/cache/`
- `.vitepress/dist/`
