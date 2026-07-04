# OTel orchestration traces

Status: SQU-74 Phase 3 implementation notes.

`agent-teamd` can export orchestration-layer traces when repo config contains an
enabled `[otel]` block. The exporter is intentionally stdlib-only: it hand-builds
OTLP/HTTP JSON trace payloads and posts them with `net/http`. The Go OTel SDK is
not a runtime dependency.

## Config

```toml
[otel]
enabled = true
endpoint = "http://127.0.0.1:4318"

[otel.resource]
"deployment.environment" = "local"

[otel.headers]
# authorization = "Bearer ..."
```

When `[otel]` is absent or `enabled = false`, the daemon does not create trace
state, allocate daemon trace IDs, inject runtime telemetry config, or send
collector requests.

## Trace model

- A durable job is the trace root.
- Pipeline steps are child spans.
- Step spans use explicit `queued_at`, `running_at`, and `finished_at` job
  fields to report `agent_team.queue_wait_ms` and `agent_team.lock_wait_ms`.
- Runtime child telemetry is correlated through `TRACEPARENT`: the daemon
  allocates the step span first, then passes that span context to Claude/Codex
  launch prep.
- Daemon/job events are attached as span events, including dispatch,
  watchdog kill, crash finalization, bounce, gate results, approval decisions,
  and merge.
- Agent-owned spans use `gen_ai.agent.name` / `gen_ai.agent.id` where those
  attributes fit. Runtime-owned LLM/tool spans remain owned by Claude/Codex.

Trace state lives beside durable job files as `.agent_team/jobs/<job>.otel.json`.
It stores only trace/span IDs and export markers.

## Local Collector

`docker-compose.yaml`:

```yaml
services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    command: ["--config=/etc/otelcol/config.yaml"]
    ports:
      - "4318:4318"
    volumes:
      - ./otelcol.yaml:/etc/otelcol/config.yaml:ro
```

`otelcol.yaml`:

```yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318

exporters:
  debug:
    verbosity: detailed

service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [debug]
```

Run:

```sh
docker compose up otel-collector
```

Then point `.agent_team/config.toml` at `http://127.0.0.1:4318` and dispatch a
pipeline job. The collector logs should show one trace containing the job root
and child pipeline-step spans.
