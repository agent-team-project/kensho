# Daemon

`agent-teamd` is the per-repo process that owns agent instance lifecycle.

## Responsibilities

The daemon handles:

- Unix-socket HTTP API
- child process spawning
- process group stopping and restart
- runtime metadata persistence
- queue draining
- schedule firing
- event resolution
- mailbox delivery
- lifecycle events
- child logs

The user-facing CLI starts and talks to the daemon.

## State Paths

The daemon stores state under `.agent_team/daemon/`.

Important paths:

| Path | Purpose |
| --- | --- |
| `.agent_team/daemon.sock` | Unix socket when the repo path is short enough; otherwise a hashed `/tmp/agent-team-<uid>/*.sock` path |
| `.agent_team/daemon.pid` | PID file |
| `.agent_team/daemon/daemon.log` | Daemon log |
| `.agent_team/daemon/events.jsonl` | Lifecycle event log |
| `.agent_team/daemon/<instance>/metadata.toml` | Runtime metadata |
| `.agent_team/daemon/<instance>/child.log` | Child stdout/stderr |
| `.agent_team/daemon/queue/` | Pending, dead-letter, and quarantine queue state |

## Starting and Stopping

```sh
agent-team daemon start
agent-team daemon status
agent-team daemon logs --tail 50
agent-team daemon stop
```

`daemon start` detaches by default. Use `--detach=false` for foreground debugging.

Most high-level commands start or contact the daemon as needed:

```sh
agent-team sync --wait
agent-team start manager --wait
agent-team run worker --detach
agent-team tick --until-idle
```

## API Shape

The daemon exposes versioned HTTP endpoints over a Unix socket.

Representative endpoints:

| Endpoint | Purpose |
| --- | --- |
| `GET /v1/health` | API readiness |
| `GET /v1/instances` | Runtime metadata snapshot |
| `POST /v1/dispatch` | Spawn or resume an instance |
| `POST /v1/start` | Resume stopped instance |
| `POST /v1/stop` | Stop instance |
| `POST /v1/restart` | Restart instance |
| `POST /v1/remove` | Remove metadata/state |
| `POST /v1/message` | Deliver mailbox message |
| `GET /v1/logs/{instance}` | Read or follow child log |
| `GET /v1/events` | Read or follow lifecycle events |
| `GET /v1/queue` | List active queue entries |
| `POST /v1/queue/drain` | Drain ready queue entries |
| `POST /v1/queue/{id}/retry` | Retry one queue entry |
| `POST /v1/queue/{id}/drop` | Drop one queue entry |
| `POST /v1/schedules/fire` | Fire due schedules |

The public CLI is the supported integration surface. The API exists to keep the CLI and daemon separated and to make future integrations possible.

## Runtime Metadata

Metadata tracks:

- instance name
- agent
- lifecycle status
- PID
- process start/stop times
- workspace
- session ID when supported
- job/ticket/branch/PR metadata when known

The CLI reads metadata even when the daemon is down, which makes `ps`, `inspect`, `logs`, and `monitor` useful after crashes.

## Lifecycle Events

Lifecycle events are appended to `.agent_team/daemon/events.jsonl`.

Job-owned events include job, ticket, branch, PR, lifecycle status, and exit-code metadata when known. This lets `agent-team job reconcile events` recover a durable job from a terminal daemon event even if the per-instance daemon metadata has already been removed.

## Reconcile

Daemon metadata can get stale if:

- the daemon crashes
- a child exits unexpectedly
- a process is killed outside `agent-team`
- the system reboots

Use:

```sh
agent-team daemon reconcile
agent-team health
agent-team repair --dry-run
```

Reconciliation compares metadata against the live process table and updates lifecycle status.

## Code Areas

Daemon behavior lives mostly in:

- `cmd/agent-teamd/main.go`
- `internal/daemon/daemon.go`
- `internal/daemon/http.go`
- `internal/daemon/instance.go`
- `internal/daemon/metadata.go`
- `internal/daemon/events.go`
- `internal/daemon/logs.go`
- `internal/cli/daemon.go`
- `internal/cli/client.go`
