# Topology architecture

**Status**: implemented. Topology is the live `instances.toml` model that
declares which agent instances exist, how event triggers route to them, and how
pipelines, schedules, locks, channels, budgets, and authority policy are owned.

This is a contributor-orientation note. For user-facing schema reference and
authoring examples, use [`docs/authoring/topology.md`](../docs/authoring/topology.md).
Keep field-level examples there unless they explain an internal boundary or
runtime behavior.

## What ships today

The implemented topology subsystem includes:

- a parser and validator in `internal/topology`
- persisted topology at `.agent_team/instances.toml`
- template and repo authoring support for shipping starter topologies
- parser tests in `internal/topology/topology_test.go`
- CLI inspection commands under `agent-team topology`
- manual and dry-run event commands under `agent-team event`
- daemon event resolution in `internal/daemon/event.go`
- daemon topology read/reload endpoints in `internal/daemon/http.go`
- schedule, lock, budget, queue, pipeline, and team integrations that consume
  the parsed topology
- public authoring docs at `docs/authoring/topology.md`

Runtime reads `.agent_team/instances.toml` as the source of truth after
`agent-team init`. Template and repo layers are merged during init/rendering;
the daemon and operator commands read the rendered vendored copy.

## Responsibilities

### `internal/topology`

This package owns the data model and pure matching behavior.

- `topology.go` defines the parsed model: instances, triggers, locks,
  channels, pipelines, schedules, teams, budgets, and authority policy.
- `load.go` provides `LoadFromTeamDir`, the runtime entry point for
  `.agent_team/instances.toml`, plus `LoadLayered` for callers that need the
  template/repo merge path.
- `trace.go` explains why an event matched or missed each declared instance
  and pipeline trigger.
- `topology_test.go` is the main regression suite for parsing, validation,
  exact normalized event matching, match behavior, and trace output.

Keep this package side-effect-free. It should not know about daemon sockets,
processes, inboxes, worktrees, PM providers, or GitHub/Linear APIs.

### `internal/cli`

The CLI is the operator surface over the parsed model and daemon state.

Current `agent-team topology --help` output exposes:

| Command | Purpose |
| --- | --- |
| `topology show` | Print declared instances and triggers, preferring daemon state and falling back to local parsing. |
| `topology graph` | Render teams, instances, pipelines, schedules, and dispatch wiring. |
| `topology summary` | Summarize declared topology and workflow health. |
| `topology reload` | Ask the running daemon to re-read `.agent_team/instances.toml`. |

Current `agent-team event --help` output exposes:

| Command | Purpose |
| --- | --- |
| `event publish <type>` | Publish a manual event to the daemon for trigger matching and dispatch. |
| `event trace <type>` | Dry-run an event against local topology and explain trigger decisions. |

`event trace` is the preferred contributor tool for reasoning about trigger
changes because it uses `Topology.Trace` without requiring a running daemon.
Use `event publish --dry-run --trace` when you want the publish command's
payload parsing plus trace output; use non-dry-run publish only when you intend
to actuate dispatch.

The top-level `agent-team reload` command wraps topology reload and daemon
metadata reconciliation. `topology reload` only swaps the live topology pointer;
running instances are not restarted by that endpoint.

### `internal/daemon`

The daemon turns matched topology rules into runtime effects.

- `EventResolver.EventWithResult` traces the inbound event against the current
  topology, actuates matched instances, then actuates matched pipelines.
- Persistent instances receive event payloads through the daemon mailbox.
- Ephemeral instances spawn a fresh runtime through `InstanceManager`, honoring
  replica limits, locks, worktree policy, runtime selection, env allowlists,
  budgets, and queueing.
- Pipeline triggers create or update durable jobs, then dispatch the first
  ready step according to step dependencies, gates, retry policy, workspace,
  runtime, and budget settings.
- `/v1/topology` returns the daemon's current parsed topology plus runtime
  counters.
- `/v1/topology/reload` re-parses `.agent_team/instances.toml` and swaps the
  resolver's topology pointer. Existing running children and queues are
  preserved.

Event resolution should remain topology-driven. Avoid adding one-off dispatch
paths that bypass `EventResolver` unless they are explicitly lower-level
implementation helpers.

## Runtime Flow

1. `agent-team init` vendors a template into `.agent_team/`, including
   `instances.toml` when the template declares one.
2. Daemon startup loads `.agent_team/instances.toml` with
   `topology.LoadFromTeamDir`.
3. Intake commands, schedules, channel publishes, explicit dispatches, and
   manual `event publish` calls produce normalized event types and payloads.
4. `Topology.Trace` evaluates instance triggers and pipeline triggers with the
   same matching rules used for dispatch.
5. `EventResolver` actuates matched persistent instances, ephemeral instances,
   and pipelines.
6. Reload commands re-parse the same vendored `instances.toml` and update the
   daemon's in-memory topology without restarting running instances.

## Event Model

New topology declarations and docs should use normalized event names:

| Event family | Typical source |
| --- | --- |
| `user_invocation` | Human starts or resumes an agent. |
| `agent.dispatch` | One agent or operator dispatches another agent through the daemon. |
| `ticket.created`, `ticket.updated`, `ticket.commented`, `ticket.status_changed` | PM intake. |
| `pr.opened`, `pr.review_requested`, `pr.commented`, `pr.merged` | GitHub intake. |
| `job.step_completed`, `job.completed`, `deliverable.ready` | Daemon job and pipeline completion hooks. |
| `schedule` | Daemon scheduler. |
| `channel.message` | Channel publish events. |

Topology declarations match event names exactly. Use the normalized `ticket.*`
and `pr.*` families from the table above, and declare one trigger per normalized
event a consumer should handle.

## Trigger Matching

The match DSL is deliberately small and should stay easy to reason about:

- single value: exact string equality
- list value: OR membership
- multiple `match.<key>` entries: AND across keys
- empty `match`: match any payload for the event type

There is no regex, negation, or nested boolean expression support. If richer
matching becomes necessary, prefer adding explicit tests around the proposed
behavior before widening the DSL.

Trace output is part of the operator experience. When changing matching logic,
update both the resolver behavior and the explanation path in
`internal/topology/trace.go`.

## Config And Ownership

Runtime topology is a repo-local file, not global state. A consumer repo can
edit `.agent_team/instances.toml` without changing the installed binary.

Important layering rules:

- During init, template defaults and repo overrides can be merged; repo entries
  replace whole resources by name rather than merging field-by-field.
- At runtime, `.agent_team/instances.toml` is the single source of truth.
- `[instances.<name>.config]` becomes declared per-instance config and is
  layered above repo config when ephemeral runtime state is prepared.
- `[instances.<name>]` fields `runtime`, `runtime_bin`, and `model` customize the
  launched runtime. `model` is passed as `--model <value>` only for Claude
  runtime launches; omitted or empty model values leave existing default model
  behavior unchanged, and Codex/Docker launches ignore it.
- Teams own instances, pipelines, schedules, and channels for operator
  commands, origin envelopes, usage accounting, and scoped resources.
- Budgets are keyed by team; locks and channels may be scoped by machine, team,
  or job.
- Authority policy supports audit and enforce modes. Audit records violations
  for triage; enforce denies disallowed audited mutations while operator/no
  origin calls and reads remain open.

## Contributor Checks

When touching topology code, pick the narrowest validation set that covers the
change:

- Parser or schema changes: `go test -count=1 ./internal/topology`
- CLI command changes: `go test -count=1 ./internal/cli`
- Daemon event resolution, queues, locks, budgets, or reload: targeted daemon
  tests plus the relevant CLI tests
- Docs-only command references: verify current help with
  `agent-team topology --help`, `agent-team event --help`, and, when tracing is
  mentioned, `agent-team event trace --help`

Before changing topology behavior, also check the public guide at
`docs/authoring/topology.md`. Public examples should describe what users write;
this file should describe where contributors change the implementation and how
the pieces interact.

## Code Map

| Area | Files |
| --- | --- |
| Schema, validation, matching | `internal/topology/topology.go`, `internal/topology/load.go`, `internal/topology/trace.go` |
| Parser and trace tests | `internal/topology/topology_test.go` |
| Topology operator commands | `internal/cli/topology.go`, `internal/cli/reload.go` |
| Event publish and trace commands | `internal/cli/event.go` |
| Daemon event dispatch | `internal/daemon/event.go` |
| Daemon topology HTTP endpoints | `internal/daemon/http.go` |
| Schedules | `internal/daemon/scheduler.go` |
| User-facing guide | `docs/authoring/topology.md` |
| Authoring and examples | `docs/authoring/templates.md`, `docs/use-cases/topology-gallery.md` |

## What Topology Does Not Change

- Agent definitions remain file-based under `.agent_team/agents/`.
- Skills remain file-based under `.agent_team/skills/`.
- `agent-team run <agent>` still supports ad-hoc operation.
- Topology controls wiring, dispatch, ownership, and resource policy; it does
  not change what an agent prompt or skill means.
