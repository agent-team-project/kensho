# Overview

`agent-team` is a Go CLI and daemon for declaring teams of LLM agents, installing those teams into repositories, and running them with durable, inspectable state.

At the smallest scale, it can initialize a starter `.agent_team/` directory and launch a single manager agent. At the larger scale it behaves more like a lightweight orchestration system: a repo declares instances, routes events, creates durable jobs, dispatches workers, tracks queues, advances pipelines, and exposes health and repair commands.

## Core Idea

The project applies a Docker-like model to agent teams:

| Docker concept | agent-team concept |
| --- | --- |
| Image | Template: versioned agents, skills, topology defaults, and parameters |
| Container | Instance: one runtime spawn of an agent with state and metadata |
| Compose file | `instances.toml`: declared instances, triggers, teams, schedules, and pipelines |
| Container logs | Instance child logs plus daemon lifecycle events |
| Container health | `status.toml`, daemon metadata, queue state, job state, and `health` checks |

Everything is per-repo and file-backed. A consumer repo owns its `.agent_team/` directory; there is no marketplace, database, or global install state required for normal operation.

## Current Product Layers

The system is now more than a launcher. The main layers are:

1. **Templates**
   - Versioned directories with `template.toml`.
   - Installed by `agent-team init`.
   - Render `.tmpl` files against resolved parameters.
   - Record provenance in `.agent_team/.template.lock`.

2. **Agents and skills**
   - Agents live under `.agent_team/agents/<name>/`.
   - Shared skills live under `.agent_team/skills/<name>/`.
   - The launcher converts agents into runtime-specific registration data.

3. **Daemon and instances**
   - `agent-teamd` owns runtime lifecycle.
   - CLI commands provide Docker-like `start`, `stop`, `restart`, `ps`, `logs`, `attach`, `rm`, and `prune`.
   - Runtime metadata survives daemon restarts.

4. **Topology**
   - `.agent_team/instances.toml` declares persistent and ephemeral instances.
   - Triggers route events such as `agent.dispatch` and `schedule`.
   - Teams group instances, pipelines, and schedules for scoped operations.

5. **Jobs**
   - Durable work units stored as `.agent_team/jobs/<job-id>.toml`.
   - Track ticket, target agent, lifecycle status, instance, branch, worktree, PR, events, queue, and pipeline steps.
   - Provide the product abstraction above raw instances.

6. **Messaging**
   - Daemon mailbox messages are durable per instance.
   - Dispatch kickoffs, runtime hooks, and `send --interrupt` give operators several delivery modes.
   - Channels provide file-backed pub/sub for shared coordination.

7. **Board control plane**
   - Linear status-change intake can route tickets by board column.
   - Pipelines can write best-effort Linear state changes back to the ticket.
   - Re-entry and agent-authored webhook loops are guarded by explicit config.

8. **Persistent queue**
   - Queue state is stored under `.agent_team/daemon/queue/`.
   - Pending and dead-letter entries survive daemon restarts.
   - Corrupt or suspicious queue files can be quarantined and restored or dropped explicitly.

9. **Pipelines, schedules, and intake**
   - Pipelines define multi-step job workflows.
   - Schedules publish periodic events.
   - Linear/GitHub/schedule intake normalizes external events into daemon events and records delivery history.

10. **Diagnostics and repair**
   - `overview`, `next`, `health`, `monitor`, `snapshot`, `doctor`, and `repair` produce read-only diagnosis and scoped next commands.
   - Recovery is intentionally explicit and previewable.

## Typical Developer Loop

```sh
go test ./...
go build -o bin/agent-team ./cmd/agent-team
go build -o bin/agent-teamd ./cmd/agent-teamd
python3 scripts/ci/smoke_init.py bin/agent-team
```

For a local consumer repo:

```sh
agent-team init
agent-team daemon start
agent-team job create "fix the flaky login test" --dispatch --workspace worktree
agent-team job show <job-id> --events all
agent-team logs --job <job-id> --follow
```

Add Linear later by setting `[team].pm_tool = "linear"` plus `[linear].team_id`
and `[linear].ticket_prefix`, or pass those values during init. See the
[Quickstart](./quickstart.md) for both paths.

## Where To Go Next

- Read [Quickstart](./quickstart.md) for the first ticketless run.
- Read [Concepts](./concepts.md) for vocabulary.
- Read [Architecture](./architecture.md) for the end-to-end control flow.
- Read [Messaging](./messaging.md) for mailbox delivery, runtime hook injection, and hard interrupts.
- Read [Board Control Plane](./board-control-plane.md) for Linear column dispatch, write-back, and re-entry rules.
- Read [Observability and Recovery](./observability-and-recovery.md) for usage, gates, signatures, build identity, and operator verbs.
- Read [Jobs](../workflows/jobs.md) if you are extending the work-unit layer.
- Read [Queues and Recovery](../workflows/queues-and-recovery.md) if you are touching dispatch durability or repair behavior.
- Read [Use Cases](../use-cases/index.md) to see concrete scenarios.
