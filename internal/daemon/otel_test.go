package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestOrchestrationOTelPipelineExportsJobTrace(t *testing.T) {
	collector := newFakeOTLPCollector(t)
	defer collector.Close()

	root := t.TempDir()
	teamDir := autoAdvanceTeamDir(t)
	writeOrchestrationOTelConfig(t, teamDir, true, collector.URL)
	top := mustParseCustomTopo(t, autoAdvancePipelineTOML)

	fake := newSequencedFakeSpawner(eventShortFakeRuntime, 3*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-974","kickoff":"implement SQU-974","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-974"); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	running, err := jobstore.Read(teamDir, "squ-974")
	if err != nil {
		t.Fatalf("read running job: %v", err)
	}
	seedPushedBranchArtifact(t, teamDir, "squ-974")
	running, err = jobstore.Read(teamDir, "squ-974")
	if err != nil {
		t.Fatalf("read artifact job: %v", err)
	}
	running.PR = "https://github.com/acme/repo/pull/974"
	if err := jobstore.Write(teamDir, running); err != nil {
		t.Fatalf("record PR before review exits: %v", err)
	}
	if err := waitForEventReaper(t, m, "reviewer-squ-974"); err != nil {
		t.Fatalf("wait reviewer reaper: %v", err)
	}

	requests := collector.Requests()
	if len(requests) != 1 {
		t.Fatalf("collector requests=%d, want 1; bodies=%s", len(requests), collector.BodiesText())
	}
	if requests[0].Path != "/v1/traces" {
		t.Fatalf("collector path = %q, want /v1/traces", requests[0].Path)
	}
	if requests[0].Header.Get("authorization") != "Bearer test-token" {
		t.Fatalf("collector authorization header = %q", requests[0].Header.Get("authorization"))
	}

	spans := otelSpansFromBodies(t, collector.Bodies())
	if len(spans) != 3 {
		t.Fatalf("spans=%d, want job + 2 steps\n%s", len(spans), collector.BodiesText())
	}
	rootSpan := spanByName(t, spans, "agent-team.job squ-974")
	implementSpan := spanByName(t, spans, "agent-team.step implement")
	reviewSpan := spanByName(t, spans, "agent-team.step review")
	traceID := stringField(t, rootSpan, "traceId")
	rootSpanID := stringField(t, rootSpan, "spanId")
	for _, span := range []map[string]any{implementSpan, reviewSpan} {
		if got := stringField(t, span, "traceId"); got != traceID {
			t.Fatalf("step trace id = %q, want %q", got, traceID)
		}
		if got := stringField(t, span, "parentSpanId"); got != rootSpanID {
			t.Fatalf("step parent span id = %q, want root %q", got, rootSpanID)
		}
		attrs := otelAttrMap(span["attributes"])
		if attrs["agent_team.queue_wait_ms"] == "" {
			t.Fatalf("step attrs missing queue_wait_ms: %+v", attrs)
		}
		if attrs["agent_team.lock_wait_ms"] == "" {
			t.Fatalf("step attrs missing lock_wait_ms: %+v", attrs)
		}
		if attrs["gen_ai.agent.name"] == "" {
			t.Fatalf("step attrs missing gen_ai.agent.name: %+v", attrs)
		}
	}

	eventNames := otelEventNames(rootSpan)
	for _, want := range []string{"agent_team.dispatch", "agent_team.instance_exit"} {
		if !containsString(eventNames, want) {
			t.Fatalf("root events missing %q: %v", want, eventNames)
		}
	}

	firstEnv := fakeEnvAt(t, fake, 0)
	tp := envValue(firstEnv, "TRACEPARENT")
	if tp == "" {
		t.Fatalf("first runtime env missing TRACEPARENT: %#v", firstEnv)
	}
	tpTrace, tpParent := parseTraceparentForTest(t, tp)
	if tpTrace != traceID {
		t.Fatalf("runtime trace id = %q, want daemon trace id %q", tpTrace, traceID)
	}
	if tpParent != stringField(t, implementSpan, "spanId") {
		t.Fatalf("runtime parent span = %q, want implement span %q", tpParent, stringField(t, implementSpan, "spanId"))
	}

	j, err := jobstore.Read(teamDir, "squ-974")
	if err != nil {
		t.Fatalf("read completed job: %v", err)
	}
	j.PR = "https://github.com/acme/repo/pull/974"
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write PR metadata: %v", err)
	}
	resp = mustPost(t, srv.URL+"/v1/event",
		`{"type":"pr.merged","payload":{"source":"github","pr_url":"https://github.com/acme/repo/pull/974","merged":true}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("merge event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	requests = collector.Requests()
	if len(requests) != 2 {
		t.Fatalf("collector requests after merge=%d, want 2; bodies=%s", len(requests), collector.BodiesText())
	}
	bodies := collector.Bodies()
	mergeSpans := otelSpansFromBodies(t, bodies[1:])
	if len(mergeSpans) != 1 {
		t.Fatalf("merge export spans=%d, want root only\n%s", len(mergeSpans), string(bodies[1]))
	}
	mergeRoot := spanByName(t, mergeSpans, "agent-team.job squ-974")
	if got := stringField(t, mergeRoot, "traceId"); got != traceID {
		t.Fatalf("merge trace id = %q, want %q", got, traceID)
	}
	if got := stringField(t, mergeRoot, "spanId"); got != rootSpanID {
		t.Fatalf("merge root span id = %q, want %q", got, rootSpanID)
	}
	if !containsString(otelEventNames(mergeRoot), "agent_team.merge") {
		t.Fatalf("merge root events missing merge: %v", otelEventNames(mergeRoot))
	}
}

func TestOrchestrationOTelLockQueuedStepExportsWait(t *testing.T) {
	collector := newFakeOTLPCollector(t)
	defer collector.Close()

	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeOrchestrationOTelConfig(t, teamDir, true, collector.URL)
	top := mustParseCustomTopo(t, `
[locks.build]
slots = 1

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 2
locks = ["build"]
[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`)

	fake := newSequencedFakeSpawner(30*time.Second, 3*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	if _, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-lock-holder",
	}); err != nil {
		t.Fatalf("holder dispatch: %v", err)
	}
	result, err := resolver.EventWithResult("ticket.created", map[string]any{
		"ticket":    "SQU-976",
		"kickoff":   "implement SQU-976",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("pipeline dispatch: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "queued" || result.Outcomes[0].Reason != QueueReasonLockHeld {
		t.Fatalf("pipeline outcomes = %+v, want lock-held queue", result.Outcomes)
	}
	queued, err := jobstore.Read(teamDir, "squ-976")
	if err != nil {
		t.Fatalf("read queued job: %v", err)
	}
	if len(queued.Steps) != 1 || queued.Steps[0].Status != jobstore.StatusQueued || queued.Steps[0].QueuedAt.IsZero() {
		t.Fatalf("queued step = %+v", queued.Steps)
	}

	if _, err := m.Stop("worker-lock-holder"); err != nil {
		t.Fatalf("stop holder: %v", err)
	}
	if err := waitForEventReaper(t, m, "worker-lock-holder"); err != nil {
		t.Fatalf("wait holder reaper: %v", err)
	}
	if fake.callCount() < 2 {
		drain, err := resolver.DrainQueuesWithResult()
		if err != nil {
			t.Fatalf("drain queued step: %v", err)
		}
		if drain.Dispatched != 1 {
			t.Fatalf("drain result = %+v, want queued step dispatch", drain)
		}
	}
	running, err := jobstore.Read(teamDir, "squ-976")
	if err != nil {
		t.Fatalf("read running job: %v", err)
	}
	seedPushedBranchArtifact(t, teamDir, "squ-976")
	running, err = jobstore.Read(teamDir, "squ-976")
	if err != nil {
		t.Fatalf("read artifact job: %v", err)
	}
	running.PR = "https://github.com/acme/repo/pull/976"
	if err := jobstore.Write(teamDir, running); err != nil {
		t.Fatalf("record PR before queued step exits: %v", err)
	}
	if err := waitForEventReaper(t, m, "worker-squ-976"); err != nil {
		t.Fatalf("wait queued step reaper: %v", err)
	}

	j, err := jobstore.Read(teamDir, "squ-976")
	if err != nil {
		t.Fatalf("read completed job: %v", err)
	}
	if len(j.Steps) != 1 {
		t.Fatalf("steps = %+v, want one", j.Steps)
	}
	step := j.Steps[0]
	if step.Status != jobstore.StatusDone || step.Attempts != 1 || step.RunningAt.IsZero() || !step.RunningAt.After(step.QueuedAt) {
		t.Fatalf("completed step = %+v, want one queued->running->done attempt", step)
	}

	spans := otelSpansFromBodies(t, collector.Bodies())
	span := spanByName(t, spans, "agent-team.step implement")
	attrs := otelAttrMap(span["attributes"])
	if attrs["agent_team.queue_reason"] != QueueReasonLockHeld {
		t.Fatalf("queue reason attrs = %+v, want %q", attrs, QueueReasonLockHeld)
	}
	if got := intAttrForTest(t, attrs, "agent_team.queue_wait_ms"); got <= 0 {
		t.Fatalf("queue_wait_ms = %d, want > 0; attrs=%+v", got, attrs)
	}
	if got := intAttrForTest(t, attrs, "agent_team.lock_wait_ms"); got <= 0 {
		t.Fatalf("lock_wait_ms = %d, want > 0; attrs=%+v", got, attrs)
	}
}

func TestOrchestrationOTelDisabledNoCollectorTraffic(t *testing.T) {
	collector := newFakeOTLPCollector(t)
	defer collector.Close()

	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeOrchestrationOTelConfig(t, teamDir, false, collector.URL)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true
[[instances.worker.triggers]]
event = "ticket.created"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
auto_advance = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`)

	fake := newFakeSpawner(eventShortFakeRuntime)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	result, err := resolver.EventWithResult("ticket.created", map[string]any{
		"ticket":    "SQU-975",
		"kickoff":   "implement SQU-975",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want dispatched", result.Outcomes)
	}
	if err := waitForEventReaper(t, m, result.Outcomes[0].InstanceID); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if got := collector.Requests(); len(got) != 0 {
		t.Fatalf("collector received %d request(s), want none: %s", len(got), collector.BodiesText())
	}
	matches, err := filepath.Glob(filepath.Join(teamDir, "jobs", "*.otel.json"))
	if err != nil {
		t.Fatalf("glob otel state: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("disabled otel wrote trace state: %v", matches)
	}
}

type fakeOTLPCollector struct {
	*httptest.Server
	mu       sync.Mutex
	requests []fakeOTLPRequest
}

type fakeOTLPRequest struct {
	Path   string
	Header http.Header
	Body   []byte
}

func newFakeOTLPCollector(t *testing.T) *fakeOTLPCollector {
	t.Helper()
	c := &fakeOTLPCollector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read collector body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.requests = append(c.requests, fakeOTLPRequest{
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   body,
		})
		c.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	return c
}

func (c *fakeOTLPCollector) Requests() []fakeOTLPRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]fakeOTLPRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func (c *fakeOTLPCollector) Bodies() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, 0, len(c.requests))
	for _, req := range c.requests {
		out = append(out, append([]byte(nil), req.Body...))
	}
	return out
}

func (c *fakeOTLPCollector) BodiesText() string {
	bodies := c.Bodies()
	parts := make([]string, 0, len(bodies))
	for _, body := range bodies {
		parts = append(parts, string(body))
	}
	return strings.Join(parts, "\n")
}

func writeOrchestrationOTelConfig(t *testing.T, teamDir string, enabled bool, endpoint string) {
	t.Helper()
	body := `[otel]
enabled = ` + strconv.FormatBool(enabled) + `
endpoint = "` + endpoint + `"

[otel.headers]
authorization = "Bearer test-token"

[otel.resource]
"deployment.environment" = "test"
`
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write otel config: %v", err)
	}
}

func otelSpansFromBodies(t *testing.T, bodies [][]byte) []map[string]any {
	t.Helper()
	var spans []map[string]any
	for _, body := range bodies {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode collector payload: %v\n%s", err, string(body))
		}
		for _, resourceSpan := range anySlice(payload["resourceSpans"]) {
			rs, _ := resourceSpan.(map[string]any)
			for _, scopeSpan := range anySlice(rs["scopeSpans"]) {
				ss, _ := scopeSpan.(map[string]any)
				for _, rawSpan := range anySlice(ss["spans"]) {
					span, _ := rawSpan.(map[string]any)
					spans = append(spans, span)
				}
			}
		}
	}
	return spans
}

func spanByName(t *testing.T, spans []map[string]any, name string) map[string]any {
	t.Helper()
	for _, span := range spans {
		if span["name"] == name {
			return span
		}
	}
	t.Fatalf("span %q not found in %+v", name, spans)
	return nil
}

func stringField(t *testing.T, values map[string]any, key string) string {
	t.Helper()
	value, ok := values[key].(string)
	if !ok || value == "" {
		t.Fatalf("field %s = %#v, want string", key, values[key])
	}
	return value
}

func otelAttrMap(raw any) map[string]string {
	out := map[string]string{}
	for _, item := range anySlice(raw) {
		attr, _ := item.(map[string]any)
		key, _ := attr["key"].(string)
		value, _ := attr["value"].(map[string]any)
		if key == "" || value == nil {
			continue
		}
		for _, valueKey := range []string{"stringValue", "intValue", "boolValue", "doubleValue"} {
			if v, ok := value[valueKey]; ok {
				out[key] = strings.TrimSpace(anyToString(v))
				break
			}
		}
	}
	return out
}

func anyToString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case bool:
		if value {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		body, _ := json.Marshal(v)
		return strings.TrimSpace(string(body))
	}
}

func intAttrForTest(t *testing.T, attrs map[string]string, key string) int64 {
	t.Helper()
	raw := strings.TrimSpace(attrs[key])
	if raw == "" {
		t.Fatalf("missing attr %s in %+v", key, attrs)
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("attr %s = %q: %v", key, raw, err)
	}
	return value
}

func otelEventNames(span map[string]any) []string {
	events := anySlice(span["events"])
	out := make([]string, 0, len(events))
	for _, raw := range events {
		event, _ := raw.(map[string]any)
		name, _ := event["name"].(string)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func fakeEnvAt(t *testing.T, fake *fakeSpawner, idx int) []string {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if idx < 0 || idx >= len(fake.envs) {
		t.Fatalf("fake env index %d out of range; envs=%d", idx, len(fake.envs))
	}
	return append([]string(nil), fake.envs[idx]...)
}

func parseTraceparentForTest(t *testing.T, traceparent string) (traceID, parentSpanID string) {
	t.Helper()
	parts := strings.Split(traceparent, "-")
	if len(parts) != 4 {
		t.Fatalf("traceparent %q has %d parts, want 4", traceparent, len(parts))
	}
	return parts[1], parts[2]
}

func anySlice(raw any) []any {
	switch v := raw.(type) {
	case []any:
		return v
	default:
		return nil
	}
}
