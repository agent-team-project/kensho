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

With no instance arguments, commands generally operate on declared persistent instances. `--all`, `--agent`, `--status`, `--phase`, `--stale`, `--unhealthy`, `--latest`, and `--last N` narrow or expand the selection.

## Inspect and Monitor

```sh
agent-team ps -a
agent-team inspect manager
agent-team stats --all --summary
agent-team logs worker-squ-42 --tail 200
agent-team logs worker-squ-42 --last-message
agent-team job logs squ-42 --last-message
agent-team team logs delivery --last-message
agent-team monitor --jobs --schedules
```

`inspect` combines runtime metadata, daemon-known topology, status file data, and state paths. The runtime block includes both the selected runtime kind and binary/wrapper path when daemon metadata records it; `ps --json` and `stats --json` expose the same `runtime` and `runtime_binary` fields for scripts. Use `ps --runtime codex`, `inspect --runtime codex`, `health --runtime codex`, `stats --runtime codex`, `monitor --runtime codex`, or `logs --runtime codex` to narrow mixed-runtime views.
For Codex one-shot runs, the adapter feeds the assembled agent prompt to `codex exec -` on stdin. `logs --last-message`, `job logs --last-message`, and `team logs --last-message` read captured final response sidecars instead of raw Codex diagnostic logs. Use `logs --runtime codex --last-message` to read only Codex final-response sidecars across matching instances.
See [Runtime Profiles](./profiles.md) for the Claude/Codex capability matrix.

## Attach

`attach` temporarily hands an interactive runtime session to the user.

```sh
agent-team attach manager
agent-team attach manager --dry-run
agent-team attach manager --no-resume
```

The daemon stops supervising the child, the CLI execs the runtime in the terminal, and the daemon resumes supervision afterward unless `--no-resume` is provided.
Use `--dry-run` to preview the session id, runtime binary, stop behavior, and daemon resume step without changing daemon state.
Interactive daemon attach requires a managed-resume-capable runtime; Codex-managed daemon runs should be inspected with `logs --follow` or `logs --last-message`.

Ephemeral workers are not a good attach target. Use logs and job commands for those.

## Remove and Prune

```sh
agent-team rm worker-squ-42 --dry-run
agent-team prune --older-than 24h --status exited --dry-run
agent-team team prune delivery --older-than 24h --dry-run
```

Removal deletes state and daemon metadata. Use dry-runs before destructive cleanup.

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
| `--unhealthy` | Filter blocked/stale/crashed/problem rows |
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
