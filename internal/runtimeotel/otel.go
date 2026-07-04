// Package runtimeotel translates agent-team's repo-level [otel] config into
// runtime-specific launch env/args for child LLM runtimes.
package runtimeotel

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	teamtemplate "github.com/jamesaud/agent-team/internal/template"
)

const (
	claudeOTLPProtocol = "http/protobuf"
	codexOTLPProtocol  = "binary"

	// CodexHeaderEnvPrefix is intentionally not AGENT_TEAM_: Codex launch
	// helpers project AGENT_TEAM_* vars into shell_environment_policy args,
	// which would put these header secrets back into argv.
	CodexHeaderEnvPrefix = "AGENTTEAM_OTEL_HEADER_"
)

var traceRand io.Reader = rand.Reader

// Config is the narrow repo-level OTel shape supported by Phase 2.
type Config struct {
	Enabled    bool
	Endpoint   string
	Headers    map[string]string
	Resource   map[string]string
	configured bool
}

// Context is the agent-team-owned correlation metadata added to every child.
type Context struct {
	Agent        string
	Instance     string
	JobID        string
	Ticket       string
	Pipeline     string
	PipelineStep string
	Team         string
	Runtime      string
	Branch       string
	Worktree     string
	Build        buildinfo.Info
}

// Launch is the runtime-specific propagation that callers append to a spawn.
type Launch struct {
	Env         []string
	CodexArgs   []string
	Traceparent string
}

// FromTree parses [otel] from a resolved config tree. Missing or disabled
// config is a valid no-op.
func FromTree(tree teamtemplate.Tree) (Config, error) {
	cfg := Config{
		Headers:  map[string]string{},
		Resource: map[string]string{},
	}
	if tree == nil {
		return cfg, nil
	}
	if _, ok := tree.GetDotted("otel"); !ok {
		return cfg, nil
	}
	cfg.configured = true
	if value, ok := tree.GetDotted("otel.enabled"); ok {
		enabled, err := boolValue(value, "otel.enabled")
		if err != nil {
			return cfg, err
		}
		cfg.Enabled = enabled
	}
	if value, ok := tree.GetDotted("otel.endpoint"); ok {
		endpoint, err := stringValue(value, "otel.endpoint")
		if err != nil {
			return cfg, err
		}
		cfg.Endpoint = endpoint
	}
	if headers, ok, err := stringMapFromDotted(tree, "otel.headers"); err != nil {
		return cfg, err
	} else if ok {
		cfg.Headers = headers
	}
	for _, key := range []string{"otel.resource", "otel.resource_attributes", "otel.resource_attrs"} {
		attrs, ok, err := stringMapFromDotted(tree, key)
		if err != nil {
			return cfg, err
		}
		if !ok {
			continue
		}
		for attrKey, attrValue := range attrs {
			cfg.Resource[attrKey] = attrValue
		}
	}
	if cfg.Enabled && strings.TrimSpace(cfg.Endpoint) == "" {
		return cfg, fmt.Errorf("otel.endpoint is required when otel.enabled is true")
	}
	return cfg, nil
}

// Configured reports whether an [otel] table was present.
func (c Config) Configured() bool {
	return c.configured
}

// BuildLaunch creates runtime env/config propagation. Disabled config returns
// an empty Launch and does not allocate a TRACEPARENT.
func BuildLaunch(cfg Config, rt runtimebin.Kind, ctx Context) (Launch, error) {
	if !cfg.Enabled {
		return Launch{}, nil
	}
	tp, err := NewTraceparent()
	if err != nil {
		return Launch{}, err
	}
	attrs := resourceAttributes(cfg.Resource, ctx)
	resourceValue := joinKeyValues(attrs)
	headersValue := joinKeyValues(cfg.Headers)

	launch := Launch{Traceparent: tp}
	switch rt {
	case runtimebin.KindClaude:
		launch.Env = []string{
			"CLAUDE_CODE_ENABLE_TELEMETRY=1",
			"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
			"OTEL_EXPORTER_OTLP_ENDPOINT=" + strings.TrimSpace(cfg.Endpoint),
			"OTEL_EXPORTER_OTLP_PROTOCOL=" + claudeOTLPProtocol,
			"OTEL_TRACES_EXPORTER=otlp",
			"OTEL_METRICS_EXPORTER=otlp",
			"OTEL_LOGS_EXPORTER=otlp",
			"OTEL_LOG_USER_PROMPTS=0",
			"OTEL_LOG_TOOL_CONTENT=0",
			"OTEL_LOG_TOOL_DETAILS=0",
			"OTEL_RESOURCE_ATTRIBUTES=" + resourceValue,
			"TRACEPARENT=" + tp,
		}
		if headersValue != "" {
			launch.Env = append(launch.Env, "OTEL_EXPORTER_OTLP_HEADERS="+headersValue)
		}
	case runtimebin.KindCodex:
		headerRefs, headerEnv := codexHeaderRefs(cfg.Headers)
		launch.Env = []string{
			"TRACEPARENT=" + tp,
			"OTEL_RESOURCE_ATTRIBUTES=" + resourceValue,
		}
		launch.Env = append(launch.Env, headerEnv...)
		launch.CodexArgs = codexConfigArgs(cfg, attrs, headerRefs, tp)
	default:
		return Launch{}, fmt.Errorf("unsupported runtime %q", rt)
	}
	return launch, nil
}

// NewTraceparent returns a sampled W3C TRACEPARENT value.
func NewTraceparent() (string, error) {
	traceID, err := nonZeroRandomHex(16)
	if err != nil {
		return "", err
	}
	spanID, err := nonZeroRandomHex(8)
	if err != nil {
		return "", err
	}
	return "00-" + traceID + "-" + spanID + "-01", nil
}

// SanitizeArgs redacts Codex OTEL header args before launch snapshots are
// persisted. Current generated args use env indirection rather than literal
// secrets; this remains a defense-in-depth guard for older snapshots and
// future callers.
func SanitizeArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i, arg := range out {
		if strings.Contains(arg, "otel.") && strings.Contains(arg, "headers") {
			out[i] = "<otel headers stripped>"
		}
	}
	return out
}

// StripOwnedEnv removes runtime telemetry variables that agent-team owns when
// [otel] is configured. This lets callers regenerate OTel state from current
// config instead of inheriting stale TRACEPARENT/exporter values from a prior
// launch snapshot.
func StripOwnedEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		key := envKey(item)
		if isOwnedEnvKey(key) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// StripGeneratedHeaderEnv removes Codex header env vars generated by
// BuildLaunch without stripping other telemetry variables.
func StripGeneratedHeaderEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(envKey(item), CodexHeaderEnvPrefix) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func boolValue(value any, path string) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, fmt.Errorf("%s must be a boolean", path)
		}
		return parsed, nil
	default:
		return false, fmt.Errorf("%s must be a boolean", path)
	}
}

func stringValue(value any, path string) (string, error) {
	v, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", path)
	}
	v = strings.TrimSpace(v)
	if strings.ContainsAny(v, "\r\n") {
		return "", fmt.Errorf("%s must not contain newlines", path)
	}
	return v, nil
}

func stringMapFromDotted(tree teamtemplate.Tree, path string) (map[string]string, bool, error) {
	value, ok := tree.GetDotted(path)
	if !ok {
		return nil, false, nil
	}
	raw, ok := asMap(value)
	if !ok {
		return nil, true, fmt.Errorf("%s must be a TOML table of string values", path)
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		cleanKey := strings.TrimSpace(key)
		if cleanKey == "" {
			return nil, true, fmt.Errorf("%s must not contain empty keys", path)
		}
		if strings.ContainsAny(cleanKey, "\r\n=,") {
			return nil, true, fmt.Errorf("%s key %q contains unsupported characters", path, key)
		}
		cleanValue, err := stringValue(value, path+"."+cleanKey)
		if err != nil {
			return nil, true, err
		}
		if cleanValue == "" {
			return nil, true, fmt.Errorf("%s.%s must not be empty", path, cleanKey)
		}
		out[cleanKey] = cleanValue
	}
	return out, true, nil
}

func asMap(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case map[string]any:
		return v, true
	case teamtemplate.Tree:
		return map[string]any(v), true
	default:
		return nil, false
	}
}

func resourceAttributes(custom map[string]string, ctx Context) map[string]string {
	out := make(map[string]string, len(custom)+14)
	for key, value := range custom {
		out[key] = value
	}
	managed := map[string]string{
		"service.name":              "agent-team/" + strings.TrimSpace(ctx.Agent),
		"agent_team.agent":          strings.TrimSpace(ctx.Agent),
		"agent_team.instance":       strings.TrimSpace(ctx.Instance),
		"agent_team.job_id":         strings.TrimSpace(ctx.JobID),
		"agent_team.ticket":         strings.TrimSpace(ctx.Ticket),
		"agent_team.pipeline":       strings.TrimSpace(ctx.Pipeline),
		"agent_team.pipeline_step":  strings.TrimSpace(ctx.PipelineStep),
		"agent_team.team":           strings.TrimSpace(ctx.Team),
		"agent_team.runtime":        strings.TrimSpace(ctx.Runtime),
		"agent_team.branch":         strings.TrimSpace(ctx.Branch),
		"agent_team.worktree":       strings.TrimSpace(ctx.Worktree),
		"agent_team.build.version":  strings.TrimSpace(ctx.Build.Version),
		"agent_team.build.revision": strings.TrimSpace(ctx.Build.Revision),
		"agent_team.build.time":     strings.TrimSpace(ctx.Build.Time),
	}
	if ctx.Build.Modified {
		managed["agent_team.build.modified"] = "true"
	}
	for key, value := range managed {
		if value != "" {
			out[key] = value
		}
	}
	if out["service.name"] == "agent-team/" {
		delete(out, "service.name")
	}
	return out
}

func joinKeyValues(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := sortedKeys(values)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(values[key])
		if value == "" {
			continue
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func codexConfigArgs(cfg Config, attrs, headerRefs map[string]string, traceparent string) []string {
	logExporter := codexExporterTable(cfg, headerRefs)
	traceExporter := codexTraceExporterTable(cfg, headerRefs)
	args := []string{
		"-c", "otel.exporter=" + logExporter,
		"-c", "otel.trace_exporter=" + traceExporter,
		"-c", "otel.log_user_prompt=false",
		"-c", "otel.span_attributes=" + tomlInlineStringMap(attrs),
		"-c", "shell_environment_policy.set.TRACEPARENT=" + strconv.Quote(traceparent),
	}
	return args
}

func codexExporterTable(cfg Config, headers map[string]string) string {
	return codexOTLPHTTPExporterTable(strings.TrimSpace(cfg.Endpoint), headers)
}

func codexTraceExporterTable(cfg Config, headers map[string]string) string {
	return codexOTLPHTTPExporterTable(strings.TrimSpace(cfg.Endpoint), headers)
}

func codexOTLPHTTPExporterTable(endpoint string, headers map[string]string) string {
	fields := []string{
		"endpoint = " + strconv.Quote(endpoint),
		"protocol = " + strconv.Quote(codexOTLPProtocol),
	}
	if len(headers) > 0 {
		fields = append(fields, "headers = "+tomlInlineStringMap(headers))
	}
	return "{ otlp-http = { " + strings.Join(fields, ", ") + " } }"
}

func codexHeaderRefs(headers map[string]string) (map[string]string, []string) {
	if len(headers) == 0 {
		return nil, nil
	}
	keys := sortedKeys(headers)
	refs := make(map[string]string, len(keys))
	env := make([]string, 0, len(keys))
	for i, key := range keys {
		value := strings.TrimSpace(headers[key])
		if value == "" {
			continue
		}
		envKey := CodexHeaderEnvPrefix + strconv.Itoa(i)
		refs[key] = "${" + envKey + "}"
		env = append(env, envKey+"="+value)
	}
	return refs, env
}

func envKey(item string) string {
	key := item
	if before, _, ok := strings.Cut(item, "="); ok {
		key = before
	}
	return strings.TrimSpace(key)
}

func isOwnedEnvKey(key string) bool {
	switch key {
	case "TRACEPARENT", "TRACESTATE",
		"CLAUDE_CODE_ENABLE_TELEMETRY", "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA":
		return true
	default:
		return strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, CodexHeaderEnvPrefix)
	}
}

func tomlInlineStringMap(values map[string]string) string {
	if len(values) == 0 {
		return "{}"
	}
	keys := sortedKeys(values)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(values[key])
		if value == "" {
			continue
		}
		parts = append(parts, strconv.Quote(key)+" = "+strconv.Quote(value))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nonZeroRandomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	for attempts := 0; attempts < 10; attempts++ {
		if _, err := io.ReadFull(traceRand, buf); err != nil {
			return "", err
		}
		if !allZero(buf) {
			return hex.EncodeToString(buf), nil
		}
	}
	return "", fmt.Errorf("random trace id was all zero")
}

func allZero(buf []byte) bool {
	for _, b := range buf {
		if b != 0 {
			return false
		}
	}
	return true
}
