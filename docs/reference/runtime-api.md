# Runtime API

The daemon API is HTTP over a Unix socket. It normally lives at `.agent_team/daemon.sock`; when that path would exceed the OS socket limit, agent-team uses a deterministic hashed socket under `/tmp/agent-team-<uid>/`.

The CLI is the supported interface, but the API boundary is useful when working on daemon features.

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
  "workspace": "/repo"
}
```

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

## Queue

```http
GET /v1/queue
POST /v1/queue/drain
POST /v1/queue/drain?dry_run=true
POST /v1/queue/{id}/retry
POST /v1/queue/{id}/drop
```

The CLI wraps these with:

```sh
agent-team queue ls
agent-team queue drain --dry-run
agent-team queue retry <id>
agent-team queue drop <id>
```

## Schedules

```http
POST /v1/schedules/fire
POST /v1/schedules/fire?dry_run=true
```

Used by `schedule fire` and `tick`.

## Compatibility Notes

- The API is local to one repo.
- Paths and payloads are versioned under `/v1`.
- CLI behavior is more stable than raw API shape.
- Avoid adding API endpoints without matching CLI tests.
- Prefer dry-run support where an endpoint can mutate state.
