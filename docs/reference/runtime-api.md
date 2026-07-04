# Runtime API

The daemon API is HTTP over a Unix socket. It normally lives at `.agent_team/daemon.sock`; when that path would exceed the OS socket limit, agent-team uses a deterministic hashed socket under `/tmp/agent-team-<uid>/`.

For runtimes whose sandbox blocks Unix sockets, `agent-team daemon start --http-addr 127.0.0.1:0` also exposes the same API on a loopback-only HTTP listener. The actual address is written to `.agent_team/daemon/http.addr`, surfaced by `agent-team daemon status --json` as `http_url`, and exported to launched runtimes as `AGENT_TEAM_DAEMON_URL`. This endpoint is opt-in and must stay bound to localhost.

The CLI is the supported interface, but the API boundary is useful when working on daemon features.

Daemon write requests may include `Agent-Team-Origin` with the same fields as
the `agent-team-origin:` footer, for example
`team=platform instance=worker-squ-92 agent=worker job=squ-92`. The CLI sets
this automatically from launched-agent environment variables. Topology
authority allowlists use it for audit-only `authority_violation` events.

## Health

```http
GET /v1/health
```

Returns daemon readiness metadata.

## Instances

```http
GET /v1/instances
```

Returns runtime metadata rows.

```http
POST /v1/dispatch
```

Starts an instance or enqueues work depending on topology/capacity.

```json
{
  "agent": "worker",
  "name": "worker-squ-42",
  "prompt": "Implement the ticket",
  "workspace": "/repo",
  "args": ["exec", "-"],
  "stdin": "Large runtime prompt for stdin-capable runtimes"
}
```

For Codex one-shot runs, the CLI sends `args` ending in `"-"` and sends the assembled prompt through `stdin` so large agent prompts are not placed in the process argument list. Older clients can keep using `prompt`; Codex fallback dispatch maps it to stdin.

Lifecycle endpoints:

```http
POST /v1/start
POST /v1/stop
POST /v1/restart
POST /v1/remove
POST /v1/reconcile
```

## Messages

```http
POST /v1/message
```

```json
{
  "to": "worker-squ-42",
  "from": "manager",
  "body": "Please continue after the token rotation."
}
```

Messages are persisted and delivered through the instance mailbox.

## Logs and Events

```http
GET /v1/logs/{instance}?tail=100
GET /v1/logs/{instance}?follow=true
GET /v1/events?tail=50
GET /v1/events?follow=true
```

Responses stream text or JSONL depending on endpoint.

## Agent Outbox

```http
GET /v1/outbox
POST /v1/outbox/drain
POST /v1/outbox/drain?dry_run=true
```

Sandboxed agents that cannot reach the Unix socket or optional loopback HTTP listener can write event files under `.agent_team/outbox/pending/`. A drain pass publishes each pending item through the same event resolver used by `POST /v1/event`, then archives the file under `processed/` or `failed/`.

The CLI wraps these with:

```sh
agent-team outbox ls
agent-team outbox show <id>
agent-team outbox drain --dry-run
agent-team outbox retry <id>
agent-team outbox drop <id>
```

`agent-team tick` and `agent-team drain` run this outbox drain before the daemon capacity queue drain. Use `--dry-run` to preview pending outbox events without publishing them.

## Queue

```http
GET /v1/queue
POST /v1/queue/drain
POST /v1/queue/drain?dry_run=true
POST /v1/queue/{id}/retry
POST /v1/queue/{id}/drop
GET /v1/locks
```

The CLI wraps these with:

```sh
agent-team queue ls
agent-team queue ls --reason lock_held
agent-team queue drain --dry-run
agent-team queue retry <id>
agent-team queue drop <id>
agent-team locks
```

## Schedules

```http
POST /v1/schedules/fire
POST /v1/schedules/fire?dry_run=true
```

Used by `schedule fire` and `tick`.

Schedule fire and event-publish responses include event `outcomes`; pipeline outcomes carry `job_id`, `pipeline`, and `step` metadata when the event creates or updates a durable job. The CLI uses that metadata for `schedule fire --wait`, `schedule run <name> --wait`, and `intake schedule <name> --wait`.

## Compatibility Notes

- The API is local to one repo.
- Paths and payloads are versioned under `/v1`.
- CLI behavior is more stable than raw API shape.
- Avoid adding API endpoints without matching CLI tests.
- Prefer dry-run support where an endpoint can mutate state.
