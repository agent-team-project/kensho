# Daemon

`agent-teamd` is the per-repo process that owns agent instance lifecycle.

## Responsibilities

The daemon handles:

- Unix-socket HTTP API
- child process spawning
- process group stopping and restart
- runtime metadata persistence
- agent outbox draining
- queue draining
- schedule firing
- event resolution
- mailbox delivery
- lifecycle events
- child logs
- an embedded operator dashboard under `/ui`
- read-only resource envelopes under `/v1/resources`

The user-facing CLI starts and talks to the daemon.

## State Paths

The daemon stores state under `.agent_team/daemon/`.

Important paths:

| Path | Purpose |
| --- | --- |
| `.agent_team/daemon.sock` | Unix socket when the repo path is short enough; otherwise a hashed `/tmp/agent-team-<uid>/*.sock` path |
| `.agent_team/daemon.pid` | PID file |
| `.agent_team/daemon/agent-teamd.log` | Daemon log |
| `.agent_team/daemon/events.jsonl` | Lifecycle event log |
| `.agent_team/daemon/<instance>/meta.json` | Runtime metadata |
| `.agent_team/daemon/<instance>/child.log` | Child stdout/stderr |
| `.agent_team/daemon/queue/` | Pending, dead-letter, and quarantine queue state |
| `.agent_team/outbox/` | Agent-written event outbox for runtimes that cannot reach daemon transport |

## Starting and Stopping

```sh
agent-team daemon start
agent-team daemon status
agent-team daemon logs --tail 50
agent-team daemon stop
```

`daemon start` detaches by default. Use `--detach=false` for foreground debugging.
Use `daemon start --quiet`, `daemon stop --quiet`, or `daemon restart --quiet` when scripts only need the exit code.
Detached starts expose a loopback-only HTTP listener by default; pass
`--http-addr 127.0.0.1:9000` to choose a port. The selected address is written
to `.agent_team/daemon/http.addr` and appears in `agent-team daemon status --json`
as `http_url`.

Most high-level commands start or contact the daemon as needed:

```sh
agent-team sync --wait
agent-team start manager --wait
agent-team run worker --detach
agent-team tick --until-idle
```

## Adopting External Processes

If a runtime process was started outside `agent-team` but should be visible in
the same operator views, adopt its live PID:

```sh
agent-team adopt manager --pid 12345 --workspace "$PWD" --agent manager
agent-team adopt manager --pid-file /var/run/agent-team/manager.pid --workspace "$PWD" --agent manager
agent-team runtime adopt manager --pid 12345 --workspace "$PWD" --agent manager
agent-team daemon adopt manager --pid 12345 --workspace "$PWD" --agent manager
agent-team daemon adopt worker-squ-42 --pid 12346 --agent worker --job squ-42 --ticket SQU-42 --runtime codex
agent-team adopt manager --pid 12345 --dry-run --json
```

When the instance name is declared in `instances.toml`, `--agent` is inferred.
Use `--pid-file <path>` instead of `--pid <pid>` when a service manager or
wrapper already writes the live runtime PID to disk.
Adoption writes `.agent_team/daemon/<instance>/meta.json`, appends an `adopt`
lifecycle event, and asks a running daemon to reconcile so `ps`, `inspect`,
`monitor`, `stop`, and `health` see the process immediately.
Adoption output includes an `actions` list with the inspect, log, and
resume-plan commands to run next. For Codex-owned metadata, that list also
includes the clean `logs --last-message` path. Use `--commands` when a script
needs only the follow-up commands, one per line.

When `--job <id>` points at an existing durable job, adoption defaults the
agent, ticket, branch, PR, and workspace from that job. It also updates the job
with the adopted instance and running status, then appends an `adopted` job
audit event. Dry-runs include the planned job update in JSON without writing
metadata or the job file, and job-owned adoption output adds job-scoped
`show`, `logs`, and `resume-plan` follow-up actions. For pipeline jobs,
adoption infers or honors the active stage and keeps that stage on job
`logs` and `resume-plan` follow-ups with `--step <id>`.

`agent-team runtime adopt` and `agent-team daemon adopt` expose the same
operation from narrower operator namespaces.

The daemon did not spawn adopted processes, so it cannot wait on their final
exit. A later `agent-team daemon reconcile`, `agent-team health`, or
`agent-team repair` pass observes whether the PID is still live and marks stale
metadata exited when needed.

## API Shape

The daemon exposes versioned HTTP endpoints over a Unix socket.

Representative endpoints:

| Endpoint | Purpose |
| --- | --- |
| `GET /v1/health` | API readiness |
| `GET /v1/instances` | Runtime metadata snapshot |
| `GET /v1/jobs` | Durable job snapshot |
| `GET /v1/topology` | Loaded topology snapshot |
| `GET /v1/resources?uri=...` | Read one canonical `agt://` resource envelope |
| `POST /v1/dispatch` | Spawn or resume an instance |
| `POST /v1/start` | Resume stopped instance, or launch declared fresh with `{"fresh":true}` |
| `POST /v1/stop` | Stop instance |
| `POST /v1/restart` | Restart instance |
| `POST /v1/remove` | Remove metadata/state |
| `POST /v1/message` | Deliver mailbox message |
| `GET /v1/logs/{instance}` | Read or follow child log |
| `GET /v1/events` | Read or follow lifecycle events |
| `GET /v1/outbox` | List agent-written outbox events |
| `POST /v1/outbox/drain` | Publish pending outbox events |
| `GET /v1/queue` | List active queue entries |
| `POST /v1/queue/drain` | Drain ready queue entries |
| `POST /v1/queue/{id}/retry` | Retry one queue entry |
| `POST /v1/queue/{id}/drop` | Drop one queue entry |
| `POST /v1/schedules/fire` | Fire due schedules |

The embedded dashboard is served at `/ui/` on the same loopback listener. The
static shell loads without a token, then uses bearer-authenticated JSON calls to
show instances, jobs, pipelines, budgets, and teams. The operator token lives at
`.agent_team/daemon/operator.token`; launched runtimes use their private
per-instance token file. The public CLI remains the supported integration
surface for scripts; the API keeps the CLI and daemon separated and makes future
integrations possible.

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
- whether the record was explicitly adopted from an external process

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
