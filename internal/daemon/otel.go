package daemon

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimeotel"
	teamtemplate "github.com/jamesaud/agent-team/internal/template"
)

const (
	otelTraceEndpointSuffix = "/v1/traces"
	otelHTTPTimeout         = 5 * time.Second
)

var daemonOtelRand io.Reader = rand.Reader

type orchestrationTracer struct {
	teamDir    string
	daemonRoot string
	cfg        runtimeotel.Config
	endpoint   string
	client     *http.Client
	mu         sync.Mutex
}

type orchestrationTraceState struct {
	TraceID         string            `json:"trace_id"`
	RootSpanID      string            `json:"root_span_id"`
	StepSpanIDs     map[string]string `json:"step_span_ids"`
	RootExported    bool              `json:"root_exported,omitempty"`
	RootFingerprint string            `json:"root_fingerprint,omitempty"`
	StepExported    map[string]string `json:"step_exported,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	LastExportedAt  time.Time         `json:"last_exported_at,omitempty"`
}

type pendingSpanExport struct {
	span        map[string]any
	root        bool
	stepID      string
	fingerprint string
}

type otelSpanEvent struct {
	Name       string
	Time       time.Time
	Attributes map[string]any
}

func loadOrchestrationTracer(teamDir, daemonRoot string) *orchestrationTracer {
	if strings.TrimSpace(teamDir) == "" {
		return nil
	}
	tree, err := teamtemplate.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		return nil
	}
	cfg, err := runtimeotel.FromTree(tree)
	if err != nil || !cfg.Configured() || !cfg.Enabled {
		return nil
	}
	return newOrchestrationTracer(teamDir, daemonRoot, cfg)
}

func newOrchestrationTracer(teamDir, daemonRoot string, cfg runtimeotel.Config) *orchestrationTracer {
	if !cfg.Enabled {
		return nil
	}
	endpoint := otelTraceEndpoint(cfg.Endpoint)
	if endpoint == "" {
		return nil
	}
	return &orchestrationTracer{
		teamDir:    teamDir,
		daemonRoot: daemonRoot,
		cfg:        cfg,
		endpoint:   endpoint,
		client:     &http.Client{Timeout: otelHTTPTimeout},
	}
}

// ExportOrchestrationJob exports daemon-owned orchestration spans for a
// terminal job when the repo has enabled [otel] config. It is used by CLI
// commands that write durable job events outside the live daemon resolver.
func ExportOrchestrationJob(teamDir, daemonRoot string, j *jobstore.Job) error {
	tracer := loadOrchestrationTracer(teamDir, daemonRoot)
	if tracer == nil {
		return nil
	}
	return tracer.exportJob(j)
}

func otelTraceEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, otelTraceEndpointSuffix) {
		return endpoint
	}
	return endpoint + otelTraceEndpointSuffix
}

func (t *orchestrationTracer) traceparentForStep(jobID, stepID string) (string, error) {
	if t == nil {
		return "", nil
	}
	jobID = jobstore.IDFromInput(jobID)
	stepID = strings.TrimSpace(stepID)
	if jobID == "" || stepID == "" {
		return "", nil
	}
	state, err := t.ensureState(jobID, []string{stepID})
	if err != nil {
		return "", err
	}
	return "00-" + state.TraceID + "-" + state.StepSpanIDs[stepID] + "-01", nil
}

func (t *orchestrationTracer) exportJob(j *jobstore.Job) error {
	if t == nil || j == nil || !jobStatusTerminal(j.Status) {
		return nil
	}
	stepIDs := jobStepIDs(j)
	state, err := t.ensureState(j.ID, stepIDs)
	if err != nil {
		return err
	}
	events := t.collectSpanEvents(j.ID)
	pending := t.pendingSpans(j, state, events)
	if len(pending) == 0 {
		return nil
	}
	spans := make([]map[string]any, 0, len(pending))
	for _, item := range pending {
		spans = append(spans, item.span)
	}
	if err := t.post(spans, j); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, item := range pending {
		if item.root {
			state.RootExported = true
			state.RootFingerprint = item.fingerprint
			continue
		}
		if item.stepID != "" {
			if state.StepExported == nil {
				state.StepExported = map[string]string{}
			}
			state.StepExported[item.stepID] = item.fingerprint
		}
	}
	state.LastExportedAt = now
	return t.writeState(j.ID, state)
}

func (t *orchestrationTracer) ensureState(jobID string, stepIDs []string) (*orchestrationTraceState, error) {
	jobID = jobstore.IDFromInput(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("otel trace state: job id is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state, err := t.readState(jobID)
	if err != nil {
		return nil, err
	}
	changed := false
	now := time.Now().UTC()
	if state == nil {
		traceID, err := nonZeroRandomHexFrom(daemonOtelRand, 16)
		if err != nil {
			return nil, err
		}
		rootSpanID, err := nonZeroRandomHexFrom(daemonOtelRand, 8)
		if err != nil {
			return nil, err
		}
		state = &orchestrationTraceState{
			TraceID:      traceID,
			RootSpanID:   rootSpanID,
			StepSpanIDs:  map[string]string{},
			StepExported: map[string]string{},
			CreatedAt:    now,
		}
		changed = true
	}
	if state.StepSpanIDs == nil {
		state.StepSpanIDs = map[string]string{}
		changed = true
	}
	if state.StepExported == nil {
		state.StepExported = map[string]string{}
		changed = true
	}
	for _, stepID := range stepIDs {
		stepID = strings.TrimSpace(stepID)
		if stepID == "" || state.StepSpanIDs[stepID] != "" {
			continue
		}
		spanID, err := nonZeroRandomHexFrom(daemonOtelRand, 8)
		if err != nil {
			return nil, err
		}
		state.StepSpanIDs[stepID] = spanID
		changed = true
	}
	if changed {
		if err := t.writeStateLocked(jobID, state); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (t *orchestrationTracer) readState(jobID string) (*orchestrationTraceState, error) {
	body, err := os.ReadFile(otelTraceStatePath(t.teamDir, jobID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var state orchestrationTraceState
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("otel trace state: parse %s: %w", jobID, err)
	}
	if state.TraceID == "" || state.RootSpanID == "" {
		return nil, fmt.Errorf("otel trace state: %s missing trace/root span ids", jobID)
	}
	return &state, nil
}

func (t *orchestrationTracer) writeState(jobID string, state *orchestrationTraceState) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeStateLocked(jobID, state)
}

func (t *orchestrationTracer) writeStateLocked(jobID string, state *orchestrationTraceState) error {
	if state == nil {
		return nil
	}
	path := otelTraceStatePath(t.teamDir, jobID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func otelTraceStatePath(teamDir, jobID string) string {
	return filepath.Join(jobstore.Directory(teamDir), jobstore.IDFromInput(jobID)+".otel.json")
}

func jobStepIDs(j *jobstore.Job) []string {
	if j == nil {
		return nil
	}
	out := make([]string, 0, len(j.Steps))
	for _, step := range j.Steps {
		if strings.TrimSpace(step.ID) != "" {
			out = append(out, step.ID)
		}
	}
	return out
}

func (t *orchestrationTracer) pendingSpans(j *jobstore.Job, state *orchestrationTraceState, events []otelSpanEvent) []pendingSpanExport {
	if j == nil || state == nil {
		return nil
	}
	pending := []pendingSpanExport{}
	rootFP := rootExportFingerprint(j, events)
	if !state.RootExported || state.RootFingerprint != rootFP {
		rootEnd := j.UpdatedAt
		if rootEnd.IsZero() {
			rootEnd = time.Now().UTC()
		}
		pending = append(pending, pendingSpanExport{
			root:        true,
			fingerprint: rootFP,
			span: otelSpan(
				state.TraceID,
				state.RootSpanID,
				"",
				"agent-team.job "+j.ID,
				j.CreatedAt,
				rootEnd,
				otelJobAttributes(j),
				events,
				nil,
			),
		})
	}
	for _, step := range j.Steps {
		if step.ID == "" || step.FinishedAt.IsZero() {
			continue
		}
		spanID := state.StepSpanIDs[step.ID]
		if spanID == "" {
			continue
		}
		fp := stepExportFingerprint(step)
		if state.StepExported[step.ID] == fp {
			continue
		}
		stepEvents := eventsForStep(events, step.ID, step.Instance)
		pending = append(pending, pendingSpanExport{
			stepID:      step.ID,
			fingerprint: fp,
			span: otelSpan(
				state.TraceID,
				spanID,
				state.RootSpanID,
				"agent-team.step "+step.ID,
				stepSpanStart(step, j),
				step.FinishedAt,
				otelStepAttributes(j, step),
				stepEvents,
				nil,
			),
		})
	}
	return pending
}

func rootExportFingerprint(j *jobstore.Job, events []otelSpanEvent) string {
	parts := []string{
		string(j.Status),
		j.LastEvent,
		j.LastStatus,
		j.PR,
		j.Branch,
		j.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	for _, ev := range events {
		parts = append(parts, spanEventFingerprint(ev))
	}
	return strings.Join(parts, "|")
}

func spanEventFingerprint(ev otelSpanEvent) string {
	parts := []string{
		ev.Name,
		ev.Time.UTC().Format(time.RFC3339Nano),
	}
	keys := make([]string, 0, len(ev.Attributes))
	for key := range ev.Attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+fmt.Sprint(ev.Attributes[key]))
	}
	return strings.Join(parts, ";")
}

func stepExportFingerprint(step jobstore.Step) string {
	return strings.Join([]string{
		string(step.Status),
		step.Instance,
		step.FinishedAt.UTC().Format(time.RFC3339Nano),
	}, "|")
}

func stepSpanStart(step jobstore.Step, j *jobstore.Job) time.Time {
	if !step.QueuedAt.IsZero() {
		return step.QueuedAt
	}
	if !step.StartedAt.IsZero() {
		return step.StartedAt
	}
	if j != nil && !j.CreatedAt.IsZero() {
		return j.CreatedAt
	}
	return step.FinishedAt
}

func stepRunningAt(step jobstore.Step) time.Time {
	if !step.RunningAt.IsZero() {
		return step.RunningAt
	}
	if !step.StartedAt.IsZero() {
		return step.StartedAt
	}
	return step.QueuedAt
}

func otelJobAttributes(j *jobstore.Job) []map[string]any {
	attrs := []map[string]any{
		otelAttribute("agent_team.job_id", j.ID),
		otelAttribute("agent_team.ticket", j.Ticket),
		otelAttribute("agent_team.pipeline", j.Pipeline),
		otelAttribute("agent_team.target", j.Target),
		otelAttribute("agent_team.status", string(j.Status)),
		otelAttribute("gen_ai.agent.name", j.Target),
	}
	if j.PR != "" {
		attrs = append(attrs, otelAttribute("agent_team.pr", j.PR))
	}
	if j.Branch != "" {
		attrs = append(attrs, otelAttribute("agent_team.branch", j.Branch))
	}
	return compactAttributes(attrs)
}

func otelStepAttributes(j *jobstore.Job, step jobstore.Step) []map[string]any {
	queueWait := durationMillis(stepRunningAt(step).Sub(stepSpanStart(step, j)))
	lockWait := int64(0)
	if step.QueueReason == QueueReasonLockHeld {
		lockWait = queueWait
	}
	attrs := []map[string]any{
		otelAttribute("agent_team.job_id", j.ID),
		otelAttribute("agent_team.pipeline", j.Pipeline),
		otelAttribute("agent_team.pipeline_step", step.ID),
		otelAttribute("agent_team.step.status", string(step.Status)),
		otelAttribute("agent_team.step.target", step.Target),
		otelAttribute("agent_team.instance", step.Instance),
		otelAttribute("agent_team.queue_reason", step.QueueReason),
		otelAttribute("agent_team.queue_wait_ms", queueWait),
		otelAttribute("agent_team.lock_wait_ms", lockWait),
		otelAttribute("gen_ai.agent.name", step.Target),
		otelAttribute("gen_ai.agent.id", step.Instance),
	}
	if step.Attempts > 0 {
		attrs = append(attrs, otelAttribute("agent_team.step.attempts", int64(step.Attempts)))
	}
	return compactAttributes(attrs)
}

func durationMillis(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}

func (t *orchestrationTracer) collectSpanEvents(jobID string) []otelSpanEvent {
	var out []otelSpanEvent
	for _, ev := range t.jobAuditSpanEvents(jobID) {
		out = append(out, ev)
	}
	for _, ev := range t.lifecycleSpanEvents(jobID) {
		out = append(out, ev)
	}
	for _, ev := range t.gateSpanEvents(jobID) {
		out = append(out, ev)
	}
	for _, ev := range t.approvalSpanEvents(jobID) {
		out = append(out, ev)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

func (t *orchestrationTracer) jobAuditSpanEvents(jobID string) []otelSpanEvent {
	events, err := jobstore.ListEvents(t.teamDir, jobID)
	if err != nil {
		return nil
	}
	out := make([]otelSpanEvent, 0, len(events))
	for _, ev := range events {
		attrs := map[string]any{
			"agent_team.event.type":  ev.Type,
			"agent_team.event.actor": ev.Actor,
			"agent_team.instance":    ev.Instance,
			"agent_team.status":      string(ev.Status),
			"agent_team.message":     ev.Message,
		}
		for key, value := range ev.Data {
			attrs["agent_team.event.data."+key] = value
		}
		out = append(out, otelSpanEvent{
			Name:       jobEventSpanName(ev.Type),
			Time:       ev.TS,
			Attributes: attrs,
		})
	}
	return out
}

func (t *orchestrationTracer) lifecycleSpanEvents(jobID string) []otelSpanEvent {
	if strings.TrimSpace(t.daemonRoot) == "" {
		return nil
	}
	events, err := ListLifecycleEvents(t.daemonRoot)
	if err != nil {
		return nil
	}
	out := []otelSpanEvent{}
	for _, ev := range events {
		if ev == nil || jobstore.IDFromInput(ev.Job) != jobstore.IDFromInput(jobID) {
			continue
		}
		attrs := map[string]any{
			"agent_team.lifecycle.action": ev.Action,
			"agent_team.instance":         ev.Instance,
			"agent_team.agent":            ev.Agent,
			"agent_team.status":           string(ev.Status),
			"agent_team.message":          ev.Message,
		}
		if ev.PID > 0 {
			attrs["process.pid"] = int64(ev.PID)
		}
		if ev.ExitCode != nil {
			attrs["process.exit.code"] = int64(*ev.ExitCode)
		}
		out = append(out, otelSpanEvent{
			Name:       lifecycleEventSpanName(ev.Action),
			Time:       ev.TS,
			Attributes: attrs,
		})
	}
	return out
}

func (t *orchestrationTracer) gateSpanEvents(jobID string) []otelSpanEvent {
	records, err := jobstore.ListGateRecords(t.teamDir, jobID)
	if err != nil {
		return nil
	}
	out := make([]otelSpanEvent, 0, len(records))
	for _, record := range records {
		out = append(out, otelSpanEvent{
			Name: "agent_team.gate_result",
			Time: record.TS,
			Attributes: map[string]any{
				"agent_team.gate.name":      record.Name,
				"agent_team.gate.status":    string(record.Status),
				"agent_team.gate.signature": record.Signature,
				"agent_team.gate.log_ref":   record.LogRef,
				"agent_team.event.actor":    record.Actor,
			},
		})
	}
	return out
}

func (t *orchestrationTracer) approvalSpanEvents(jobID string) []otelSpanEvent {
	approvals, err := jobstore.ListApprovals(t.teamDir, jobID)
	if err != nil {
		return nil
	}
	out := []otelSpanEvent{}
	for _, approval := range approvals {
		if approval == nil || approval.Decision == nil {
			continue
		}
		out = append(out, otelSpanEvent{
			Name: "agent_team.approval_decision",
			Time: approval.Decision.TS,
			Attributes: map[string]any{
				"agent_team.approval.id":     approval.ID,
				"agent_team.approval.status": string(approval.Status),
				"agent_team.approval.step":   approval.StepID,
				"agent_team.event.actor":     approval.Decision.Actor,
				"agent_team.message":         approval.Decision.Notes,
			},
		})
	}
	return out
}

func jobEventSpanName(eventType string) string {
	switch eventType {
	case "dispatched", "advance_dispatched":
		return "agent_team.dispatch"
	case "instance_crashed":
		return "agent_team.crash_finalize"
	case "gate.updated":
		return "agent_team.gate_result"
	case "manual_gate_approved", "manual_gate_rejected":
		return "agent_team.approval_decision"
	case "merged", "pr.merged":
		return "agent_team.merge"
	}
	if strings.Contains(eventType, "bounce") {
		return "agent_team.bounce"
	}
	return "agent_team.job_event"
}

func lifecycleEventSpanName(action string) string {
	switch action {
	case "dispatch":
		return "agent_team.dispatch"
	case "watchdog":
		return "agent_team.watchdog_kill"
	case "crash":
		return "agent_team.crash_finalize"
	case "exit":
		return "agent_team.instance_exit"
	default:
		return "agent_team.lifecycle_event"
	}
}

func eventsForStep(events []otelSpanEvent, stepID, instance string) []otelSpanEvent {
	stepID = strings.TrimSpace(stepID)
	instance = strings.TrimSpace(instance)
	if stepID == "" && instance == "" {
		return nil
	}
	out := []otelSpanEvent{}
	for _, ev := range events {
		if stepID != "" && attrString(ev.Attributes, "agent_team.event.data.pipeline_step") == stepID {
			out = append(out, ev)
			continue
		}
		if stepID != "" && attrString(ev.Attributes, "agent_team.event.data.step") == stepID {
			out = append(out, ev)
			continue
		}
		if stepID != "" && attrString(ev.Attributes, "agent_team.gate.name") == stepID {
			out = append(out, ev)
			continue
		}
		if stepID != "" && attrString(ev.Attributes, "agent_team.approval.step") == stepID {
			out = append(out, ev)
			continue
		}
		if instance != "" && attrString(ev.Attributes, "agent_team.instance") == instance {
			out = append(out, ev)
		}
	}
	return out
}

func attrString(attrs map[string]any, key string) string {
	v, _ := attrs[key].(string)
	return strings.TrimSpace(v)
}

func otelSpan(traceID, spanID, parentSpanID, name string, start, end time.Time, attrs []map[string]any, events []otelSpanEvent, links []map[string]any) map[string]any {
	if start.IsZero() {
		start = end
	}
	if end.IsZero() {
		end = start
	}
	span := map[string]any{
		"traceId":           traceID,
		"spanId":            spanID,
		"name":              name,
		"kind":              "SPAN_KIND_INTERNAL",
		"startTimeUnixNano": strconv.FormatInt(start.UTC().UnixNano(), 10),
		"endTimeUnixNano":   strconv.FormatInt(end.UTC().UnixNano(), 10),
		"attributes":        attrs,
	}
	if parentSpanID != "" {
		span["parentSpanId"] = parentSpanID
	}
	if len(events) > 0 {
		span["events"] = otelEvents(events)
	}
	if len(links) > 0 {
		span["links"] = links
	}
	return span
}

func otelEvents(events []otelSpanEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		at := ev.Time
		if at.IsZero() {
			at = time.Now().UTC()
		}
		out = append(out, map[string]any{
			"timeUnixNano": strconv.FormatInt(at.UTC().UnixNano(), 10),
			"name":         ev.Name,
			"attributes":   otelAttributesFromMap(ev.Attributes),
		})
	}
	return out
}

func otelAttributesFromMap(attrs map[string]any) []map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, otelAttribute(key, attrs[key]))
	}
	return compactAttributes(out)
}

func compactAttributes(attrs []map[string]any) []map[string]any {
	out := attrs[:0]
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		value, _ := attr["value"].(map[string]any)
		if len(value) == 0 {
			continue
		}
		if s, ok := value["stringValue"].(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		out = append(out, attr)
	}
	return out
}

func otelAttribute(key string, value any) map[string]any {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	switch v := value.(type) {
	case string:
		return map[string]any{"key": key, "value": map[string]any{"stringValue": strings.TrimSpace(v)}}
	case bool:
		return map[string]any{"key": key, "value": map[string]any{"boolValue": v}}
	case int:
		return map[string]any{"key": key, "value": map[string]any{"intValue": strconv.FormatInt(int64(v), 10)}}
	case int64:
		return map[string]any{"key": key, "value": map[string]any{"intValue": strconv.FormatInt(v, 10)}}
	case float64:
		return map[string]any{"key": key, "value": map[string]any{"doubleValue": v}}
	default:
		return map[string]any{"key": key, "value": map[string]any{"stringValue": strings.TrimSpace(fmt.Sprint(v))}}
	}
}

func (t *orchestrationTracer) post(spans []map[string]any, j *jobstore.Job) error {
	body, err := json.Marshal(map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{
				"attributes": t.resourceAttributes(j),
			},
			"scopeSpans": []map[string]any{{
				"scope": map[string]any{
					"name": "agent-team/daemon",
				},
				"spans": spans,
			}},
		}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range t.cfg.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("otel export: collector returned %s", resp.Status)
	}
	return nil
}

func (t *orchestrationTracer) resourceAttributes(j *jobstore.Job) []map[string]any {
	attrs := map[string]any{}
	for key, value := range t.cfg.Resource {
		attrs[key] = value
	}
	attrs["service.name"] = "agent-team/daemon"
	attrs["agent_team.component"] = "daemon"
	attrs["agent_team.job_id"] = j.ID
	attrs["agent_team.ticket"] = j.Ticket
	attrs["agent_team.pipeline"] = j.Pipeline
	build := buildinfo.Current("")
	if build.Version != "" {
		attrs["agent_team.build.version"] = build.Version
	}
	if build.Revision != "" {
		attrs["agent_team.build.revision"] = build.Revision
	}
	if build.Time != "" {
		attrs["agent_team.build.time"] = build.Time
	}
	if build.Modified {
		attrs["agent_team.build.modified"] = true
	}
	return otelAttributesFromMap(attrs)
}

func nonZeroRandomHexFrom(r io.Reader, bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	for attempts := 0; attempts < 10; attempts++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		allZero := true
		for _, b := range buf {
			if b != 0 {
				allZero = false
				break
			}
		}
		if !allZero {
			return hex.EncodeToString(buf), nil
		}
	}
	return "", fmt.Errorf("random trace id was all zero")
}
