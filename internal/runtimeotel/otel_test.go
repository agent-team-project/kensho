package runtimeotel

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/buildinfo"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	teamtemplate "github.com/jamesaud/agent-team/internal/template"
)

func TestFromTreeEnabledParsesHeadersAndResource(t *testing.T) {
	tree := teamtemplate.Tree{}
	tree.SetDotted("otel.enabled", true)
	tree.SetDotted("otel.endpoint", "http://collector:4318")
	tree.SetDotted("otel.headers", map[string]any{"x-api-key": "secret"})
	tree.SetDotted("otel.resource", map[string]any{
		"deployment.environment": "dev",
		"service.name":           "custom",
	})

	cfg, err := FromTree(tree)
	if err != nil {
		t.Fatalf("FromTree: %v", err)
	}
	if !cfg.Enabled || cfg.Endpoint != "http://collector:4318" || !cfg.Configured() {
		t.Fatalf("cfg = %+v, want enabled configured endpoint", cfg)
	}
	if cfg.Headers["x-api-key"] != "secret" {
		t.Fatalf("headers = %+v", cfg.Headers)
	}
	if cfg.Resource["deployment.environment"] != "dev" || cfg.Resource["service.name"] != "custom" {
		t.Fatalf("resource = %+v", cfg.Resource)
	}
}

func TestFromTreeDisabledNoOp(t *testing.T) {
	tree := teamtemplate.Tree{}
	tree.SetDotted("otel.enabled", false)
	cfg, err := FromTree(tree)
	if err != nil {
		t.Fatalf("FromTree disabled: %v", err)
	}
	launch, err := BuildLaunch(cfg, runtimebin.KindClaude, Context{Agent: "worker"})
	if err != nil {
		t.Fatalf("BuildLaunch disabled: %v", err)
	}
	if len(launch.Env) != 0 || len(launch.CodexArgs) != 0 || launch.Traceparent != "" {
		t.Fatalf("disabled launch = %+v, want empty", launch)
	}
}

func TestFromTreeEnabledRequiresEndpoint(t *testing.T) {
	tree := teamtemplate.Tree{}
	tree.SetDotted("otel.enabled", true)
	if _, err := FromTree(tree); err == nil || !strings.Contains(err.Error(), "otel.endpoint is required") {
		t.Fatalf("FromTree error = %v, want endpoint requirement", err)
	}
}

func TestBuildLaunchClaudeEnv(t *testing.T) {
	restoreTraceRand(t)
	cfg := Config{
		Enabled:  true,
		Endpoint: "http://collector:4318",
		Headers:  map[string]string{"authorization": "Bearer secret"},
		Resource: map[string]string{"deployment.environment": "dev", "service.name": "custom"},
	}
	launch, err := BuildLaunch(cfg, runtimebin.KindClaude, Context{
		Agent:        "worker",
		Instance:     "worker-squ-74",
		JobID:        "squ-74",
		Ticket:       "SQU-74",
		Pipeline:     "ticket_to_pr",
		PipelineStep: "implement",
		Team:         "delivery",
		Runtime:      "claude",
		Branch:       "squ-74",
		Worktree:     "/repo/.claude/worktrees/worker",
		Build:        buildinfo.Info{Version: "1.2.3", Revision: "abcdef"},
	})
	if err != nil {
		t.Fatalf("BuildLaunch: %v", err)
	}
	for _, want := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=otlp",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_LOG_USER_PROMPTS=0",
		"OTEL_LOG_TOOL_CONTENT=0",
		"OTEL_LOG_TOOL_DETAILS=0",
		"OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer secret",
		"TRACEPARENT=00-0102030405060708090a0b0c0d0e0f10-1112131415161718-01",
	} {
		if !contains(launch.Env, want) {
			t.Fatalf("env missing %q: %#v", want, launch.Env)
		}
	}
	resource := envValueForTest(launch.Env, "OTEL_RESOURCE_ATTRIBUTES")
	for _, want := range []string{
		"service.name=agent-team/worker",
		"agent_team.instance=worker-squ-74",
		"agent_team.job_id=squ-74",
		"agent_team.ticket=SQU-74",
		"agent_team.pipeline=ticket_to_pr",
		"agent_team.pipeline_step=implement",
		"agent_team.team=delivery",
		"agent_team.runtime=claude",
		"agent_team.build.version=1.2.3",
		"deployment.environment=dev",
	} {
		if !strings.Contains(resource, want) {
			t.Fatalf("resource attrs missing %q in %q", want, resource)
		}
	}
	if strings.Contains(resource, "service.name=custom") {
		t.Fatalf("managed service.name did not override custom attrs: %q", resource)
	}
}

func TestBuildLaunchCodexArgs(t *testing.T) {
	restoreTraceRand(t)
	cfg := Config{
		Enabled:  true,
		Endpoint: "http://collector:4318",
		Headers:  map[string]string{"x-otlp-api-key": "secret"},
	}
	launch, err := BuildLaunch(cfg, runtimebin.KindCodex, Context{
		Agent:    "worker",
		Instance: "worker-squ-74",
		JobID:    "squ-74",
		Ticket:   "SQU-74",
		Runtime:  "codex",
	})
	if err != nil {
		t.Fatalf("BuildLaunch: %v", err)
	}
	if !contains(launch.Env, "TRACEPARENT=00-0102030405060708090a0b0c0d0e0f10-1112131415161718-01") {
		t.Fatalf("codex env missing traceparent: %#v", launch.Env)
	}
	if !contains(launch.Env, "AGENTTEAM_OTEL_HEADER_0=secret") {
		t.Fatalf("codex env missing header indirection: %#v", launch.Env)
	}
	joined := strings.Join(launch.CodexArgs, "\n")
	if strings.Contains(joined, "secret") {
		t.Fatalf("codex args leaked header secret:\n%s", joined)
	}
	for _, want := range []string{
		"otel.exporter={ otlp-http = { endpoint = \"http://collector:4318\", protocol = \"binary\", headers = { \"x-otlp-api-key\" = \"${AGENTTEAM_OTEL_HEADER_0}\" } } }",
		"otel.trace_exporter=\"otlp-http\"",
		"otel.trace_exporter.\"otlp-http\".endpoint=\"http://collector:4318\"",
		"otel.trace_exporter.\"otlp-http\".protocol=\"binary\"",
		"otel.trace_exporter.\"otlp-http\".headers={ \"x-otlp-api-key\" = \"${AGENTTEAM_OTEL_HEADER_0}\" }",
		"otel.log_user_prompt=false",
		"otel.span_attributes={",
		"\"service.name\" = \"agent-team/worker\"",
		"shell_environment_policy.set.TRACEPARENT=\"00-0102030405060708090a0b0c0d0e0f10-1112131415161718-01\"",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("codex args missing %q:\n%s", want, joined)
		}
	}
	assertCodexOTelArgsDecode(t, launch.CodexArgs, "http://collector:4318")
}

func TestSanitizeArgsRedactsOTelHeaders(t *testing.T) {
	args := []string{
		"codex",
		"exec",
		"-c",
		"otel.exporter={ otlp-http = { endpoint = \"http://collector\", headers = { \"authorization\" = \"secret\" } } }",
		"-c",
		"otel.log_user_prompt=false",
	}
	got := SanitizeArgs(args)
	if strings.Contains(strings.Join(got, " "), "secret") {
		t.Fatalf("SanitizeArgs leaked secret: %#v", got)
	}
	if !contains(got, "<otel headers stripped>") {
		t.Fatalf("SanitizeArgs missing redaction marker: %#v", got)
	}
}

func TestStripOwnedEnvRemovesTelemetryKeys(t *testing.T) {
	got := StripOwnedEnv([]string{
		"PATH=/bin",
		"TRACEPARENT=00-stale",
		"TRACESTATE=stale",
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://old",
		"OTEL_RESOURCE_ATTRIBUTES=old=true",
		"AGENTTEAM_OTEL_HEADER_0=secret",
		"AGENT_TEAM_INSTANCE=manager",
	})
	for _, forbidden := range []string{
		"TRACEPARENT", "TRACESTATE", "CLAUDE_CODE_ENABLE_TELEMETRY",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA", "OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_RESOURCE_ATTRIBUTES", "AGENTTEAM_OTEL_HEADER_0",
	} {
		if envValueForTest(got, forbidden) != "" {
			t.Fatalf("StripOwnedEnv kept %s in %#v", forbidden, got)
		}
	}
	if !contains(got, "PATH=/bin") || !contains(got, "AGENT_TEAM_INSTANCE=manager") {
		t.Fatalf("StripOwnedEnv removed unrelated env: %#v", got)
	}
}

func restoreTraceRand(t *testing.T) {
	t.Helper()
	old := traceRand
	traceRand = bytes.NewReader([]byte{
		1, 2, 3, 4, 5, 6, 7, 8,
		9, 10, 11, 12, 13, 14, 15, 16,
		17, 18, 19, 20, 21, 22, 23, 24,
	})
	t.Cleanup(func() { traceRand = old })
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func envValueForTest(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func assertCodexOTelArgsDecode(t *testing.T, args []string, endpoint string) {
	t.Helper()
	values := map[string]any{}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" && strings.HasPrefix(args[i+1], "otel.") {
			key, rawValue, ok := strings.Cut(args[i+1], "=")
			if !ok {
				t.Fatalf("codex otel arg missing value: %q", args[i+1])
			}
			var decoded map[string]any
			if _, err := toml.Decode("value = "+rawValue, &decoded); err != nil {
				t.Fatalf("codex otel arg %q did not decode as TOML value: %v", args[i+1], err)
			}
			values[key] = decoded["value"]
		}
	}
	exporter, ok := values["otel.exporter"].(map[string]any)
	if !ok {
		t.Fatalf("otel.exporter decoded as %T, want inline exporter table", values["otel.exporter"])
	}
	logExporter := codexExporterTableForTest(t, exporter, "otlp-http")
	if got, _ := logExporter["endpoint"].(string); got != endpoint {
		t.Fatalf("otel.exporter endpoint = %q, want %q", got, endpoint)
	}
	if got, _ := logExporter["protocol"].(string); got != codexOTLPProtocol {
		t.Fatalf("otel.exporter protocol = %q, want %q", got, codexOTLPProtocol)
	}
	logHeaders := codexExporterTableForTest(t, logExporter, "headers")
	if got, _ := logHeaders["x-otlp-api-key"].(string); got != "${AGENTTEAM_OTEL_HEADER_0}" {
		t.Fatalf("otel.exporter headers = %+v, want env reference", logHeaders)
	}

	if got, _ := values["otel.trace_exporter"].(string); got != "otlp-http" {
		t.Fatalf("otel.trace_exporter = %#v, want selector string %q", values["otel.trace_exporter"], "otlp-http")
	}
	if _, ok := values["otel.trace_exporter"].(map[string]any); ok {
		t.Fatalf("otel.trace_exporter decoded as inline table, want selector string")
	}
	if got, _ := values[`otel.trace_exporter."otlp-http".endpoint`].(string); got != endpoint {
		t.Fatalf("otel.trace_exporter otlp-http endpoint = %q, want %q", got, endpoint)
	}
	if got, _ := values[`otel.trace_exporter."otlp-http".protocol`].(string); got != codexOTLPProtocol {
		t.Fatalf("otel.trace_exporter otlp-http protocol = %q, want %q", got, codexOTLPProtocol)
	}
	traceHeaders, ok := values[`otel.trace_exporter."otlp-http".headers`].(map[string]any)
	if !ok {
		t.Fatalf("otel.trace_exporter otlp-http headers decoded as %T, want table", values[`otel.trace_exporter."otlp-http".headers`])
	}
	if got, _ := traceHeaders["x-otlp-api-key"].(string); got != "${AGENTTEAM_OTEL_HEADER_0}" {
		t.Fatalf("otel.trace_exporter headers = %+v, want env reference", traceHeaders)
	}
}

func codexExporterTableForTest(t *testing.T, values map[string]any, key string) map[string]any {
	t.Helper()
	table, ok := values[key].(map[string]any)
	if !ok {
		t.Fatalf("%s decoded as %T, want table", key, values[key])
	}
	return table
}
