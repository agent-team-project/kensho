# Instances

Instances are the running form of agents.

## Lifecycle States

Common lifecycle statuses:

| Status | Meaning |
| --- | --- |
| `running` | Child process is live |
| `stopped` | Stopped intentionally and resumable |
| `exited` | Process exited normally |
| `crashed` | Process exited unexpectedly |
| `removed` | Metadata/state removed |
| `unknown` | Declared but no metadata yet |

Work phase is separate and comes from `status.toml`.

## Start, Stop, Restart

```sh
agent-team start
agent-team stop manager --wait
agent-team restart manager --attach --tail 100
agent-team kill worker-squ-42 --wait
```

With no instance arguments, commands generally operate on declared persistent instances. `--all`, `--agent`, `--status`, `--phase`, `--stale`, `--runtime-stale`, `--unhealthy`, `--latest`, and `--last N` narrow or expand the selection.

## Inspect and Monitor

```sh
agent-team ps -a
agent-team inspect manager
agent-team stats --all --summary
agent-team logs worker-squ-42 --tail 200
agent-team logs worker-squ-42 --clean --tail 200
agent-team logs worker-squ-42 --last-message
agent-team job logs squ-42 --last-message
agent-team team logs delivery --last-message
agent-team monitor --jobs --schedules
```

`inspect` combines runtime metadata, daemon-known topology, status file data, and state paths. The runtime block includes both the selected runtime kind and binary/wrapper path when daemon metadata records it; `ps --json` and `stats --json` expose the same `runtime` and `runtime_binary` fields for scripts. Use `ps --runtime codex`, `inspect --runtime codex`, `health --runtime codex`, `stats --runtime codex`, `monitor --runtime codex`, or `logs --runtime codex` to narrow mixed-runtime views. Use `runtime metadata ls` for repo-wide raw persisted daemon metadata and `runtime metadata show <instance>` for one raw record, or `job runtime ls`, `pipeline runtime ls`, and `team runtime ls` when scripts need the same raw view scoped to one work unit, workflow, or team. Add `runtime metadata ls --commands` to print show commands for the filtered rows, or `runtime metadata show <instance> --commands` to print inspect, logs, resume-plan, and job detail follow-ups.
For Codex one-shot runs, the adapter feeds the assembled agent prompt to `codex exec -` on stdin. `logs --last-message`, `job logs --last-message`, and `team logs --last-message` read captured final response sidecars instead of raw Codex diagnostic logs. Use `logs --runtime codex --last-message` to read only Codex final-response sidecars across matching instances. When you need raw logs but not Codex startup/plugin/reconnect diagnostics, add `--clean` to `logs`, `job logs`, or `team logs`.
See [Runtime Profiles](./profiles.md) for the runtime capability matrix.

## Attach

`attach` temporarily hands an interactive runtime session to the user.

```sh
agent-team attach manager
agent-team attach manager --dry-run
agent-team attach manager --dry-run --commands
agent-team attach manager --no-resume
agent-team job attach squ-42 --dry-run
agent-team job attach squ-42 --dry-run --commands
```

The daemon stops supervising the child, the CLI execs the runtime in the terminal, and the daemon resumes supervision afterward unless `--no-resume` is provided.
Use `--dry-run` to preview the session id, runtime binary, stop behavior, command, and daemon resume step without changing daemon state.
Add `--commands` to that dry-run when scripts need only command lines. Managed runtimes print the matching `agent-team attach` or `agent-team job attach` apply command; Codex-managed daemon metadata also prints the interactive `codex resume <session>` command plus log fallbacks.
Interactive daemon attach requires a managed-resume-capable runtime and a recorded session id. For Codex-managed daemon runs with captured session metadata, non-dry-run attach stops the child, execs `codex resume <session>` in the terminal, and lets the daemon return to `codex exec resume <session> -` afterward unless `--no-resume` is passed. `attach --dry-run` prints the interactive command plus `logs --follow` and `logs --last-message` fallbacks without stopping anything.
When the dry-run starts from `job attach`, the output also includes `job logs`
and `job logs --last-message` fallbacks so operators can remain in the job
namespace.

Ephemeral workers are not a good attach target. Use logs and job commands for those.

## Adopt External Processes

Use adoption when a runtime process is already live but was not launched through
the daemon:

```sh
agent-team adopt manager --pid 12345 --workspace "$PWD" --agent manager
agent-team adopt manager --pid-file ./manager.pid --workspace "$PWD" --agent manager
agent-team runtime adopt manager --pid 12345 --workspace "$PWD" --agent manager
agent-team inspect manager
agent-team ps --status running
```

Adopted processes are visible in normal daemon metadata views and can be stopped
by `agent-team stop <instance>`. Because the daemon did not spawn them, it only
learns about later exits during reconciliation:

```sh
agent-team daemon reconcile
```

If the instance is declared in `instances.toml`, `--agent` is inferred. Include
`--session-id` for Claude-compatible processes when you want future
managed-resume attempts to have the session identifier available. Use
`--pid-file <path>` when the PID comes from a service manager, wrapper, or
existing runtime pidfile.
Text and JSON adoption results include follow-up actions such as `inspect`,
`logs --follow`, and `resume-plan`; Codex adoptions also include
`logs --last-message`. Add `--commands` when scripts need only those follow-up
commands, one per line, after a dry-run or apply.

Add `--job <id>` when the external process owns a durable job. If that job file
exists, adoption records the instance, branch, PR, running status, and an
`adopted` audit event on the job so `job show`, `job logs`, and scoped triage
commands immediately point at the recovered process. Job-owned adoption results
also include the matching `job show`, `job logs`, and `job resume-plan`
follow-up actions.

`agent-team runtime adopt` and `agent-team daemon adopt` are the same metadata
operation from narrower namespaces. Use `agent-team job adopt <job-id> --pid <pid>`
or `agent-team job adopt <job-id> --pid-file <path>` when you want to
start from a durable job instead of an instance name.

## Remove and Prune

```sh
agent-team rm worker-squ-42 --dry-run
agent-team prune --older-than 24h --status exited --dry-run
agent-team prune --runtime-stale --dry-run
agent-team team prune delivery --older-than 24h --dry-run
```

Removal deletes state and daemon metadata. Use dry-runs before destructive cleanup.
`prune` normally targets finished rows, but `--runtime-stale` also selects rows still recorded as running when the recorded runtime PID is no longer live.

## Selection Flags

Most instance commands share selection flags:

| Flag | Meaning |
| --- | --- |
| `--all` | Include all daemon-known instances where supported |
| `--agent <name>` | Filter by agent |
| `--instance <pattern>` | Filter by instance name |
| `--status <status>` | Filter lifecycle status |
| `--phase <phase>` | Filter status-file work phase |
| `--stale` | Filter stale non-idle work |
| `--runtime-stale` | Filter running instances whose recorded runtime PID is no longer live |
| `--unhealthy` | Filter crashed, status-stale, or runtime-stale rows |
| `--latest` | Select latest matching row |
| `--last N` | Select N latest matching rows |

## Worktree Ownership

Worker jobs can own:

- worktree path
- branch name
- PR URL

That metadata appears in:

- `job show`
- `ps`
- `inspect`
- `monitor`
- status files
- snapshots

Cleanup is explicit:

```sh
agent-team job cleanup squ-42 --dry-run
agent-team job cleanup squ-42 --merged
```

The cleanup command should only remove job-owned branches and worktrees.
