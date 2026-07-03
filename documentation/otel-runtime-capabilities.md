# OTel runtime capabilities

Status: Phase 1 investigation memo for SQU-74.

Date: 2026-07-03.

This memo scopes the current OpenTelemetry surfaces in the locally installed
agent runtimes that `agent-team` may launch. It intentionally does not propose
or implement any launcher, daemon, or config changes.

## Summary recommendation

`agent-team` should propagate and correlate runtime-owned LLM telemetry, not
fabricate `gen_ai.*` model spans itself. The daemon should emit orchestration
spans for jobs and steps, then pass enough runtime-specific configuration into
child processes for each runtime to attach its own API/tool telemetry to that
trace when the runtime supports it.

For Phase 2 and Phase 3 implementation, start with a small stdlib OTLP/HTTP
exporter for `agent-team` orchestration spans:

- Use `net/http` plus hand-built OTLP/HTTP JSON payloads for traces first.
- Keep the `[otel]` repo config narrow: endpoint, headers, enabled flag,
  resource attributes, and a signal selector.
- Do not add the Go OTel SDK in the first pass.

Reason: this repo has a stated minimal-deps rule. Current runtime deps are
`cobra` and `BurntSushi/toml`. Probing the current OTel Go SDK modules showed
that trace-only OTLP/HTTP support pulls in `go.opentelemetry.io/*`, protobuf,
grpc/gateway, `golang.org/x/*`, and related modules; full trace/log/metric
support pulls a larger graph. The latest probed modules also declare Go 1.25,
while this repo currently targets Go 1.22. The SDK is justified later if we
need sampling, processors, baggage/resource merging, gRPC, metrics/logs, or
vendor ecosystem compatibility beyond a simple collector POST.

## Verification legend

- **Verified by probe**: observed in the locally installed CLI help, version,
  feature list, doctor output, or installed binary strings.
- **Docs-only**: present in official runtime documentation, not proven by an
  end-to-end local export in this pass.
- **Inferred**: design consequence from the documented/probed surfaces.

## Probe environment

| Runtime | Local version | Probe commands |
| --- | --- | --- |
| Claude Code | `2.1.199 (Claude Code)` | `claude --version`, `claude --help`, `claude doctor --help`, installed binary `strings` |
| Codex | `codex-cli 0.142.2` | `codex --version`, `codex --help`, `codex features list`, `codex doctor --summary`, Codex manual helper, installed native binary `strings` |

Official docs checked:

- Claude Code monitoring: `https://code.claude.com/docs/en/monitoring-usage`
- Codex advanced config: `https://developers.openai.com/codex/config-advanced`
- Codex config reference: `https://developers.openai.com/codex/config-reference`
- OTel OTLP exporter config: `https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/`
- OTel protocol spec: `https://opentelemetry.io/docs/specs/otlp/`
- OTel GenAI attribute registry: `https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/`

## Capability matrix

| Surface | Claude Code 2.1.199 | Codex 0.142.2 |
| --- | --- | --- |
| OTel enable switch | **Verified by probe + docs-only behavior**: binary contains `CLAUDE_CODE_ENABLE_TELEMETRY`; docs require it. Tracing additionally requires `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA`. | **Docs-only behavior, binary verified**: docs configure `[otel]`; binary contains `OtelConfigToml` and exporter strings. |
| Logs/events export | **Verified by probe + docs-only behavior**: binary contains OTLP env vars and many `tengu_*` event names. Docs define `claude_code.*` events. | **Verified by probe + docs-only behavior**: binary contains event names such as `codex.api_request`, `codex.sse_event`, `codex.user_prompt`, `codex.tool_decision`, and `codex.tool_result`. Docs list the same representative log events. |
| Metrics export | **Docs-only behavior, env vars verified**: docs list `claude_code.session.count`, LOC, PR, commit, cost, token, permission, and active-time metrics. Binary contains `OTEL_METRICS_EXPORTER` and related config tokens. | **Docs-only behavior, binary verified**: config reference has `otel.metrics_exporter = none | statsig | otlp-http | otlp-grpc`; binary includes OTLP metrics exporter strings. |
| Trace export | **Docs-only behavior, env vars verified**: docs say tracing is beta and exports interaction, LLM request, hook, and tool spans. Binary contains trace-related env vars and GenAI attributes. | **Verified by probe at symbol level + docs-only behavior**: config reference has `otel.trace_exporter`; binary contains trace exporter strings, `TRACEPARENT`, `TRACESTATE`, and `trace_context` symbols. Docs do not describe a complete span hierarchy. |
| TRACEPARENT input | **Important limitation, docs-only**: docs say Agent SDK and non-interactive `claude -p` sessions read `TRACEPARENT`/`TRACESTATE`; interactive sessions ignore inbound `TRACEPARENT`. Current `agent-team run` historically launches interactive Claude, so Phase 2 must verify whether Claude workers can actually nest under daemon spans without changing launch mode. | **Verified by probe at symbol level**: binary contains `TRACEPARENT detected; continuing trace from parent context` and invalid-traceparent handling strings. |
| TRACEPARENT output to tools | **Docs-only**: when tracing is active, Bash/PowerShell subprocesses inherit `TRACEPARENT`; model and HTTP MCP requests can carry trace context under documented conditions. | **Verified by probe at symbol level**: binary contains `traceparent`/`tracestate` in tool/runtime context strings. Runtime behavior not end-to-end captured here. |
| GenAI semantic conventions | **Docs-only, binary tokens verified**: docs explicitly mark `gen_ai.system`, `gen_ai.request.model`, `gen_ai.response.id`, `gen_ai.response.finish_reasons`, and `gen_ai.tool.call.id` as OTel GenAI semconv attributes. Binary also contains these tokens. | **Partial, verified by probe at symbol level**: binary contains `gen_ai.usage.input_tokens`, `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.output_tokens`, and `codex.usage.reasoning_output_tokens`. Official Codex docs list Codex event names but do not claim full GenAI semconv compliance. |
| Runtime config injection shape | Env vars. | User-level Codex config or CLI `-c` overrides. Docs say project-local `.codex/config.toml` ignores `otel`, so a checked-in project config cannot enable runtime telemetry. |

## Claude Code findings

### Local probes

`claude --help` in 2.1.199 does not list telemetry flags or OTLP options. The
only help-visible telemetry-related surface is the `gateway` subcommand,
described as enterprise auth/telemetry gateway support.

Installed binary strings verified these telemetry tokens:

- `@splunk/otel`
- `CLAUDE_CODE_ENABLE_TELEMETRY`
- `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA`
- `OTEL_EXPORTER_OTLP_ENDPOINT`
- `OTEL_EXPORTER_OTLP_PROTOCOL`
- `OTEL_EXPORTER_OTLP_HEADERS`
- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`
- `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`
- `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
- `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`
- `OTEL_LOGS_EXPORTER`
- `OTEL_METRICS_EXPORTER`
- `OTEL_TRACES_EXPORTER`
- privacy/detail gates such as `OTEL_LOG_USER_PROMPTS`,
  `OTEL_LOG_TOOL_CONTENT`, and `OTEL_LOG_TOOL_DETAILS`
- GenAI attributes including `gen_ai.system`, `gen_ai.request.model`,
  `gen_ai.response.id`, `gen_ai.response.finish_reasons`, and
  `gen_ai.tool.call.id`

That proves the installed binary contains the documented telemetry plumbing,
but it does not prove that a particular env var set successfully exported data
to a collector in this pass.

### Docs-only behavior

Claude Code's monitoring docs describe an env-var driven OTel surface:

- Common OTLP configuration uses the standard `OTEL_EXPORTER_OTLP_*` family.
- Metrics, logs, and traces can be routed independently with
  `OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER`, and `OTEL_TRACES_EXPORTER`.
- Tracing is beta and requires both `CLAUDE_CODE_ENABLE_TELEMETRY=1` and
  `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1`.
- Trace export uses interaction spans with child LLM request, hook, and tool
  spans. Tool spans include permission-wait and execution children.
- Metrics include session count, lines of code modified, PR count, commit
  count, cost, token usage, editing permission decisions, and active time.
- Events include tool result, API request, API error, and related session/tool
  events. API request events include token/cost/duration fields.

### GenAI semconv status

Claude is the stronger runtime for GenAI semconv claims. The docs explicitly
label several LLM/tool attributes as OpenTelemetry GenAI semantic convention
attributes, and the local binary contains those same `gen_ai.*` tokens.

Still, this is not full compliance proof. This pass did not run Claude against
a collector and inspect exported spans. Treat semconv support as
**docs-only with local symbol corroboration**.

### Phase 2 implication

Claude's inbound parent-context behavior matters. The docs say non-interactive
`claude -p` and Agent SDK sessions read `TRACEPARENT`/`TRACESTATE` from their
environment, while interactive sessions ignore inbound `TRACEPARENT`. If the
daemon launches Claude workers in interactive mode, simply injecting
`TRACEPARENT` may not nest Claude's interaction span under the job span.

Phase 2 should include a fake-collector test for the exact launch mode
`agent-team` uses before promising parent-child trace correlation for Claude.

## Codex findings

### Local probes

The local binary is `codex-cli 0.142.2`. `codex doctor --summary` reported
`0.142.5` available, but this memo targets the installed 0.142.2 runtime.

`codex --help` and `codex features list` do not show an explicit telemetry or
OTel feature flag. Feature list does include `runtime_metrics` as
`under development false`, but the documented `[otel]` surface is config, not
a feature flag.

The Codex manual helper fetched the current official manual and found an
"Observability and telemetry" section. Installed native binary strings
verified these local symbols:

- `OtelConfigToml`
- `OtelExporterKind`
- `otel.exporter`, `otel.trace_exporter`, `otel.metrics_exporter`
- `otlp-http`, `otlp-grpc`
- `log_user_prompt`, `environment`, `span_attributes`, `tracestate`
- `OpenTelemetry logs export failed`
- `OpenTelemetry trace export failed`
- `OpenTelemetry metrics export failed`
- `Using OTLP Http exporter`, `Using OTLP Grpc exporter`
- `TRACEPARENT detected; continuing trace from parent context`
- `TRACEPARENT is set but invalid; ignoring trace context`
- event names including `codex.api_request`, `codex.sse_event`,
  `codex.websocket.request`, `codex.websocket.event`, `codex.user_prompt`,
  `codex.tool_decision`, `codex.tool_result`, `codex.tool.call`, and
  `codex.conversation_starts`
- GenAI-ish attributes including `gen_ai.usage.input_tokens`,
  `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.output_tokens`,
  and `codex.usage.reasoning_output_tokens`

`codex doctor --summary -c 'otel.exporter="none"'` loaded config, but the same
command also accepted a deliberately bogus key. Therefore `doctor` is not a
strict local config-shape validator.

### Docs-only behavior

Codex's official docs describe `[otel]` under advanced config:

- OTel is disabled by default.
- `exporter = "none"` records events but sends nothing.
- `exporter = { otlp-http = { endpoint, protocol, headers } }` exports logs.
- `exporter = { otlp-grpc = { endpoint, headers } }` is also documented.
- Exporters batch asynchronously and flush on shutdown.
- Event metadata includes service name, CLI version, environment tag,
  conversation id, model, sandbox/approval settings, and per-event fields.

The config reference adds:

- `otel.environment`
- `otel.exporter`
- `otel.log_user_prompt`
- `otel.metrics_exporter = none | statsig | otlp-http | otlp-grpc`
- `otel.trace_exporter = none | otlp-http | otlp-grpc`
- per-exporter endpoint/header/protocol/TLS fields

The advanced config docs also say project-local `.codex/config.toml` cannot
override `otel`; Codex ignores that key in project-local config. Phase 2 should
therefore avoid a checked-in `.codex/config.toml` approach for runtime
telemetry. Prefer one of:

- CLI `-c` overrides for non-secret values, with secrets referenced through
  environment variables rather than literal command-line values.
- A generated temporary `CODEX_HOME` or profile config with mode `0600`, if
  command-line exposure is unacceptable.

### GenAI semconv status

Codex is weaker than Claude on explicit semconv claims. The official Codex docs
list Codex-specific events and metrics; they do not claim full OpenTelemetry
GenAI semantic convention compliance. The installed binary contains some
`gen_ai.usage.*` attributes, so token-usage semconv support is plausible, but
this pass did not prove complete GenAI semconv coverage or inspect exported
payloads.

Treat Codex GenAI support as **partial verified-by-probe at symbol level,
full compliance not established**.

## Agent-team implications

### Do not synthesize runtime LLM spans

The ticket's boundary principle is correct. Claude and Codex own the actual
model calls and tool execution telemetry. `agent-team` should not invent
`gen_ai.*` spans for those calls.

`agent-team` should emit orchestration spans for concepts only it owns:

- job trace root
- pipeline step spans
- queue wait and lock wait timings
- dispatch, crash-finalize, watchdog kill, bounce, gate result, approval, and
  merge events
- runtime launch metadata and correlation attributes

### Runtime propagation plan

For both runtimes, the daemon should provide resource attributes such as:

- `service.name=agent-team/<agent>`
- `agent_team.instance`
- `agent_team.job_id`
- `agent_team.ticket`
- `agent_team.pipeline`
- `agent_team.pipeline_step`
- `agent_team.runtime`
- `agent_team.branch`
- `agent_team.worktree`
- build identity

For Claude:

- Set `CLAUDE_CODE_ENABLE_TELEMETRY=1` when `[otel]` is enabled.
- Set `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1` only when trace export is enabled
  and the user has opted into beta tracing.
- Set the standard `OTEL_EXPORTER_OTLP_*` env vars, plus
  signal-specific exporter env vars as needed.
- Set privacy gates off by default: do not export prompt text, tool inputs, or
  tool content unless explicitly configured.
- Verify exact parent-context behavior for the launcher mode before relying on
  `TRACEPARENT`.

For Codex:

- Do not rely on project-local `.codex/config.toml` for `otel`.
- Prefer generated runtime config or CLI `-c` overrides.
- Keep prompt logging disabled by default with `otel.log_user_prompt=false`.
- Use `trace_exporter` only if the collector test proves trace export and
  parent context behavior for 0.142.x.

## Exporter recommendation for agent-team

### Option A: stdlib OTLP/HTTP exporter

Recommended for first implementation.

Pros:

- Keeps runtime dependency surface unchanged.
- Fits this repo's file-based, minimal-deps architecture.
- Easy to make a no-op when `[otel]` is disabled.
- Easy to test with a fake collector by checking HTTP requests to
  `/v1/traces`.
- Avoids a Go toolchain bump if current OTel modules require newer Go than the
  repo targets.

Cons:

- We must build and maintain the small OTLP payload shape ourselves.
- No built-in sampler, batch processor, resource detector, baggage support, or
  gRPC exporter.
- OTLP/HTTP JSON is accepted by the OTel protocol family, but protobuf is the
  normal production encoding. JSON is best for first-pass tests; protobuf may
  require generated types or a small encoder if collectors/backends reject JSON.

### Option B: Go OTel SDK

Not recommended for the first implementation.

Pros:

- Correct SDK semantics for spans, resources, status, events, batching, retry,
  shutdown, and trace context.
- Supports standard OTLP/HTTP and OTLP/gRPC exporters.
- Easier future support for metrics and logs.

Cons:

- Current probe of trace-only OTLP/HTTP added modules including
  `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/sdk`,
  `go.opentelemetry.io/proto/otlp`, `google.golang.org/grpc`,
  `google.golang.org/protobuf`, `grpc-gateway`, and `golang.org/x/*`.
- Full trace/log/metric support adds more modules, including experimental log
  packages.
- Latest probed OTel modules declare Go 1.25, while this repo targets Go 1.22.
- This is the largest dependency expansion in the repo and should clear a
  higher bar than "we need to POST a few spans to a collector."

### Decision

Start with Option A for Phase 3 orchestration spans. Revisit Option B only if:

- collector compatibility requires protobuf/gRPC beyond a small local encoder;
- sampling or batching logic becomes non-trivial;
- logs/metrics become first-class `agent-team` outputs;
- we need W3C baggage/resource propagation richer than traceparent/tracestate;
- the repo has already raised its Go version and dependency tolerance.

## Open questions for later phases

1. Claude parent-context compatibility: does the exact `agent-team` Claude
   launch mode read inbound `TRACEPARENT`, or must the daemon use `claude -p`,
   Agent SDK, or another integration point for proper nesting?
2. Codex trace payloads: does `trace_exporter` emit useful spans in 0.142.2, or
   are logs the only mature surface despite the trace config keys?
3. Codex semconv: which `gen_ai.*` attributes actually appear in exported
   payloads, and are they on spans, log records, or metrics?
4. Runtime resource attributes: do Claude/Codex preserve
   `OTEL_RESOURCE_ATTRIBUTES` exactly, or do they filter/sanitize keys?
5. Secret handling: for headers, prefer env var indirection and launch-env
   strip rules. Do not put collector API keys in committed config or PR text.
6. Disabled overhead: fake-collector and no-collector tests should verify that
   `[otel].enabled=false` skips all daemon telemetry work and runtime injection.

