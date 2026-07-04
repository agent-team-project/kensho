package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/origin"
)

func TestHTTP_Dispatch_StopList(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	// POST /v1/dispatch
	if err := AppendMessage(root, "w-1", &Message{ID: "direct-mail", From: "manager", Body: "do not append"}); err != nil {
		t.Fatalf("append mailbox: %v", err)
	}
	body := `{"agent":"worker","name":"w-1","prompt":"hi","workspace":"` + t.TempDir() + `"}`
	resp := mustPost(t, srv.URL+"/v1/dispatch", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var dispBody struct {
		InstanceID string    `json:"instance_id"`
		StartedAt  time.Time `json:"started_at"`
		PID        int       `json:"pid"`
		SessionID  string    `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dispBody); err != nil {
		t.Fatalf("dispatch body: %v", err)
	}
	if dispBody.InstanceID != "w-1" {
		t.Errorf("instance_id: got %s", dispBody.InstanceID)
	}
	if dispBody.PID == 0 || dispBody.SessionID == "" {
		t.Errorf("missing pid/session: %+v", dispBody)
	}
	if prompt, ok := argValue(fake.lastCall(), "-p"); !ok || prompt != "hi" {
		t.Fatalf("direct dispatch prompt = %q, %v; want caller prompt only in %#v", prompt, ok, fake.lastCall())
	}
	unread, err := ReadUnacked(root, "w-1")
	if err != nil {
		t.Fatalf("ReadUnacked: %v", err)
	}
	if len(unread) != 1 || unread[0].Body != "do not append" {
		t.Fatalf("direct dispatch should not advance mailbox, got %+v", unread)
	}

	// GET /v1/instances
	resp = mustGet(t, srv.URL+"/v1/instances")
	var list []*Metadata
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("instances body: %v", err)
	}
	if len(list) != 1 || list[0].Instance != "w-1" {
		t.Errorf("instances: got %+v", list)
	}
	if list[0].Status != StatusRunning {
		t.Errorf("status: got %s want running", list[0].Status)
	}

	// POST /v1/stop
	resp = mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	waitForStatusNot(t, m, "w-1", StatusRunning)

	resp = mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-1","timeout_ms":-1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative timeout status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestHTTP_ExtendRuntimeBudget(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	meta, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "w-extend", Workspace: t.TempDir(),
		Budget: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	resp := mustPost(t, srv.URL+"/v1/extend", `{"instance":"w-extend","by_ms":500,"actor":"ops"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("extend status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		InstanceID       string    `json:"instance_id"`
		ByMillis         int64     `json:"by_ms"`
		PreviousDeadline time.Time `json:"previous_deadline"`
		NewDeadline      time.Time `json:"new_deadline"`
		Actor            string    `json:"actor"`
		Metadata         Metadata  `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode extend: %v", err)
	}
	if body.InstanceID != "w-extend" || body.ByMillis != 500 || body.Actor != "ops" {
		t.Fatalf("extend body = %+v", body)
	}
	if !body.PreviousDeadline.Equal(meta.RuntimeDeadline) || !body.NewDeadline.Equal(meta.RuntimeDeadline.Add(500*time.Millisecond)) {
		t.Fatalf("deadlines = %s -> %s, want %s -> %s", body.PreviousDeadline, body.NewDeadline, meta.RuntimeDeadline, meta.RuntimeDeadline.Add(500*time.Millisecond))
	}
	if body.Metadata.RuntimeBudget != "2.5s" || !body.Metadata.RuntimeDeadline.Equal(body.NewDeadline) {
		t.Fatalf("metadata = %+v, want extended budget/deadline", body.Metadata)
	}

	resp = mustPost(t, srv.URL+"/v1/extend", `{"instance":"w-extend","by_ms":0}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("zero extend status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-extend","force":true,"timeout_ms":25}`)
	waitForStatusNot(t, m, "w-extend", StatusRunning)
}

func TestHTTP_DispatchValidation(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	cases := []struct {
		name string
		body string
	}{
		{"missing agent", `{"name":"x","workspace":"/tmp"}`},
		{"missing name", `{"agent":"w","workspace":"/tmp"}`},
		{"missing workspace", `{"agent":"w","name":"x"}`},
		{"bad json", `{not-json}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := mustPost(t, srv.URL+"/v1/dispatch", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status: got %d want 400 for %s", resp.StatusCode, c.name)
			}
		})
	}

	// Unknown fields are tolerated on the wire: a newer CLI's additive field
	// must not brick an older daemon (SQU-55).
	t.Run("unknown field tolerated", func(t *testing.T) {
		resp := mustPost(t, srv.URL+"/v1/dispatch", `{"agent":"w","name":"x-tolerant","workspace":"/tmp","extra":1}`)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: got %d want 200, body=%s", resp.StatusCode, readBody(t, resp))
		}
	})
}

func TestHTTP_DispatchPassesStdin(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	body := `{"agent":"worker","name":"w-stdin","workspace":"` + t.TempDir() + `","runtime":"codex","runtime_binary":"codex","args":["exec","-"],"stdin":"hello via http"}`
	resp := mustPost(t, srv.URL+"/v1/dispatch", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := fake.lastStdin(); got != "hello via http" {
		t.Fatalf("stdin = %q, want request body stdin", got)
	}
	mustPost(t, srv.URL+"/v1/stop", `{"instance":"w-stdin"}`)
	waitForStatusNot(t, m, "w-stdin", StatusRunning)
}

func TestHTTP_StartResumesSession(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+t.TempDir()+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var disp struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(resp.Body).Decode(&disp)

	// Stop and wait for finalisation.
	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)

	// Start.
	resp = mustPost(t, srv.URL+"/v1/start", `{"instance":"mgr"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %d %s", resp.StatusCode, readBody(t, resp))
	}

	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == disp.SessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected --resume %s, got: %v", disp.SessionID, args)
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_RestartResumesSession(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+t.TempDir()+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var disp struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(resp.Body).Decode(&disp)

	resp = mustPost(t, srv.URL+"/v1/restart", `{"instance":"mgr","timeout_ms":10000}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restart: %d %s", resp.StatusCode, readBody(t, resp))
	}
	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == disp.SessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected --resume %s, got: %v", disp.SessionID, args)
	}

	resp = mustPost(t, srv.URL+"/v1/restart", `{"instance":"mgr","timeout_ms":-1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative restart timeout: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_InterruptResumesSession(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+t.TempDir()+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var disp struct {
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(resp.Body).Decode(&disp)

	resp = mustPost(t, srv.URL+"/v1/interrupt", `{"to":"mgr","from":"ops","body":"hard steer"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("interrupt: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Delivered   bool   `json:"delivered"`
		Interrupted bool   `json:"interrupted"`
		ID          string `json:"id"`
		SessionID   string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode interrupt: %v", err)
	}
	if !out.Delivered || !out.Interrupted || out.ID == "" || out.SessionID != disp.SessionID {
		t.Fatalf("interrupt response = %+v, want delivered interrupted same session %s", out, disp.SessionID)
	}
	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == disp.SessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected --resume %s, got: %v", disp.SessionID, args)
	}
	messages, err := ReadUnacked(root, "mgr")
	if err != nil {
		t.Fatalf("read mailbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "hard steer" || messages[0].From != "ops" {
		t.Fatalf("mailbox = %+v, want delivered interrupt message", messages)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !lifecycleEventsContain(events, "interrupted", "mgr") {
		t.Fatalf("events missing interrupted: %+v", events)
	}

	mustPost(t, srv.URL+"/v1/stop", `{"instance":"mgr"}`)
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestHTTP_RemoveRequiresForceForRunning(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"manager","name":"mgr","workspace":"`+t.TempDir()+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}

	resp = mustPost(t, srv.URL+"/v1/remove", `{"instance":"mgr"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("remove running without force: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}

	resp = mustPost(t, srv.URL+"/v1/remove", `{"instance":"mgr","force":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("force remove: %d %s", resp.StatusCode, readBody(t, resp))
	}
	listResp := mustGet(t, srv.URL+"/v1/instances")
	var list []*Metadata
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("instances body: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("instances after remove = %+v, want empty", list)
	}
}

func TestHTTP_MethodGuards(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	// GET on a POST endpoint
	resp := mustGet(t, srv.URL+"/v1/dispatch")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("dispatch GET: got %d want 405", resp.StatusCode)
	}
	// POST on a GET endpoint
	resp = mustPost(t, srv.URL+"/v1/instances", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("instances POST: got %d want 405", resp.StatusCode)
	}
	resp = mustGet(t, srv.URL+"/v1/reconcile")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("reconcile GET: got %d want 405", resp.StatusCode)
	}
}

func TestHTTP_InstancesEmptyArray(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()
	resp := mustGet(t, srv.URL+"/v1/instances")
	body := readBody(t, resp)
	if !strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Errorf("expected JSON array, got %q", body)
	}
}

func TestHTTP_StatusIncludesBuildIdentity(t *testing.T) {
	root := t.TempDir()
	teamDir := t.TempDir()
	build := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "deadbeefcafebabefeedface1234567890abcdef",
		Time:     "2026-07-02T12:34:56Z",
	}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	if _, err := m.Dispatch(DispatchInput{Agent: "manager", Name: "manager", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = m.Stop("manager")
		waitForStatusNot(t, m, "manager", StatusRunning)
	}()
	srv := httptest.NewServer(Handler(m, nil, nil, teamDir, build))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		Ready     bool           `json:"ready"`
		PID       int            `json:"pid"`
		Instances int            `json:"instances"`
		TeamDir   string         `json:"team_dir"`
		Build     buildinfo.Info `json:"build"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !body.Ready || body.PID == 0 || body.Instances != 1 || body.TeamDir != teamDir {
		t.Fatalf("status body = %+v", body)
	}
	if body.Build.Revision != build.Revision || body.Build.Time != build.Time || body.Build.Version != build.Version {
		t.Fatalf("status build = %+v, want %+v", body.Build, build)
	}

	resp = mustPost(t, srv.URL+"/v1/status", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status POST: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestHTTP_BuildHandshakeMatchedIdentityLogsNothing(t *testing.T) {
	root := t.TempDir()
	build := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Time:     "2026-07-03T00:00:00Z",
	}
	logs := &bytes.Buffer{}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(HandlerWithLog(m, nil, nil, "", logs, build))
	defer srv.Close()

	resp := mustGetWithBuild(t, srv.URL+"/v1/status", build)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	if logs.Len() != 0 {
		t.Fatalf("logs = %q, want no build skew warning", logs.String())
	}
}

func TestHTTP_BuildHandshakeSkewLogsOncePerClientIdentity(t *testing.T) {
	root := t.TempDir()
	daemonBuild := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Time:     "2026-07-03T00:00:00Z",
	}
	clientBuild := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Time:     "2026-07-03T00:05:00Z",
	}
	logs := &bytes.Buffer{}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(HandlerWithLog(m, nil, nil, "", logs, daemonBuild))
	defer srv.Close()

	for i := 0; i < 3; i++ {
		resp := mustGetWithBuild(t, srv.URL+"/v1/status", clientBuild)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d: got %d body=%s", i, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
	if count := strings.Count(logs.String(), "daemon build skew"); count != 1 {
		t.Fatalf("skew warning count = %d, want 1; logs=%q", count, logs.String())
	}
	if !strings.Contains(logs.String(), clientBuild.Display()) || !strings.Contains(logs.String(), daemonBuild.Display()) {
		t.Fatalf("logs = %q, want client and daemon identities", logs.String())
	}

	otherClientBuild := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "cccccccccccccccccccccccccccccccccccccccc",
		Time:     "2026-07-03T00:10:00Z",
	}
	resp := mustGetWithBuild(t, srv.URL+"/v1/status", otherClientBuild)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second client status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	if count := strings.Count(logs.String(), "daemon build skew"); count != 2 {
		t.Fatalf("skew warning count after second identity = %d, want 2; logs=%q", count, logs.String())
	}
}

func TestHTTP_ErrorBodyIncludesDaemonBuild(t *testing.T) {
	root := t.TempDir()
	build := buildinfo.Info{
		Version:  "0.1.0",
		Revision: "dddddddddddddddddddddddddddddddddddddddd",
		Time:     "2026-07-03T00:15:00Z",
		Modified: true,
	}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	srv := httptest.NewServer(HandlerWithLog(m, nil, nil, "", io.Discard, build))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("event status: got %d want 503", resp.StatusCode)
	}
	var body struct {
		Error       string         `json:"error"`
		DaemonBuild buildinfo.Info `json:"daemon_build"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error != "topology not configured" {
		t.Fatalf("error = %q, want topology not configured", body.Error)
	}
	if body.DaemonBuild != build {
		t.Fatalf("daemon_build = %+v, want %+v", body.DaemonBuild, build)
	}
}

func TestHTTP_OutboxDrain(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 6, 27, 10, 30, 0, 0, time.UTC)
	if err := WriteOutboxItem(teamDir, &OutboxItem{
		ID:        "outbox-http",
		State:     OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"target": "worker", "name": "worker-squ-402", "ticket": "SQU-402", "workspace": "repo"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("WriteOutboxItem: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/outbox")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outbox list: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var items []*OutboxItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatalf("outbox list decode: %v", err)
	}
	if len(items) != 1 || items[0].ID != "outbox-http" {
		t.Fatalf("outbox items = %+v, want outbox-http", items)
	}

	resp = mustPost(t, srv.URL+"/v1/outbox/drain?dry_run=true", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outbox dry drain: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var preview OutboxDrainResult
	if err := json.NewDecoder(resp.Body).Decode(&preview); err != nil {
		t.Fatalf("preview decode: %v", err)
	}
	if preview.WouldPublish != 1 || preview.Pending != 1 {
		t.Fatalf("preview = %+v, want would_publish=1 pending=1", preview)
	}
	if fake.callCount() != 0 {
		t.Fatalf("dry-run spawned %d processes", fake.callCount())
	}

	resp = mustPost(t, srv.URL+"/v1/outbox/drain", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("outbox drain: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var result OutboxDrainResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("result decode: %v", err)
	}
	if result.Published != 1 || result.Pending != 0 || result.Processed != 1 {
		t.Fatalf("result = %+v, want published=1 pending=0 processed=1", result)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1", fake.callCount())
	}
}

func TestHTTP_ReconcileMarksDeadRunningProcessExited(t *testing.T) {
	root := t.TempDir()
	restorePIDLiveCheck := SetPidLiveCheckForTest(func(pid int) bool { return false })
	defer restorePIDLiveCheck()

	if err := WriteMetadata(root, &Metadata{
		Instance:  "orphan",
		Agent:     "manager",
		Status:    StatusRunning,
		PID:       999999,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	m := NewInstanceManager(root, nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/reconcile", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reconcile status: got %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var body reconcileResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode reconcile body: %v", err)
	}
	if !body.Reconciled || body.Changed != 1 {
		t.Fatalf("reconcile body = %+v, want one change", body)
	}
	if len(body.Instances) != 1 || body.Instances[0].Status != StatusExited {
		t.Fatalf("instances = %+v, want orphan exited", body.Instances)
	}
	if len(body.Changes) != 1 || body.Changes[0].Before != StatusRunning || body.Changes[0].After != StatusExited {
		t.Fatalf("changes = %+v, want running -> exited", body.Changes)
	}
	list := m.List()
	if len(list) != 1 || list[0].Status != StatusExited {
		t.Fatalf("manager list = %+v, want reconciled exited metadata", list)
	}
}

func TestHTTP_Message_AppendsToMailbox(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/message", `{"to":"worker-1","from":"manager","body":"hello"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var rb struct {
		Delivered bool   `json:"delivered"`
		ID        string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rb); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rb.Delivered {
		t.Errorf("delivered=false")
	}
	if rb.ID == "" {
		t.Errorf("missing id")
	}

	got, err := ReadMessages(root, "worker-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("messages: got %d want 1", len(got))
	}
	if got[0].Body != "hello" || got[0].From != "manager" || got[0].To != "worker-1" {
		t.Errorf("message: %+v", got[0])
	}
}

func TestHTTP_DirectDispatchOTelDisabledStripsInheritedEnv(t *testing.T) {
	// SQU-74 round-3 finding: the /v1/dispatch path must derive the OTel
	// strip decision from the repo config; with [otel] enabled=false, stale
	// telemetry env inherited by the daemon must not reach the child.
	t.Setenv("CLAUDE_CODE_ENABLE_TELEMETRY", "1")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://stale")
	t.Setenv("TRACEPARENT", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
	t.Setenv("AGENTTEAM_OTEL_HEADER_0", "stale-secret")

	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureOTelConfig(t, teamDir, false)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	srv := httptest.NewServer(Handler(m, nil, nil, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/dispatch",
		`{"agent":"worker","name":"direct-otel-disabled","workspace":"/tmp","prompt":"noop"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dispatch: %d %s", resp.StatusCode, readBody(t, resp))
	}
	t.Cleanup(func() {
		_, _ = m.Stop("direct-otel-disabled")
		_ = m.WaitForReaper("direct-otel-disabled", 5*time.Second)
	})
	env := fake.lastEnv()
	for _, forbidden := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=",
		"OTEL_EXPORTER_OTLP_ENDPOINT=",
		"TRACEPARENT=",
		"AGENTTEAM_OTEL_HEADER_",
	} {
		if containsEnvPrefix(env, forbidden) {
			t.Fatalf("direct dispatch with disabled otel leaked %q: %#v", forbidden, env)
		}
	}
}

func TestHTTP_Message_Validation(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	cases := []struct {
		name string
		body string
	}{
		{"missing to", `{"body":"hi"}`},
		{"missing body", `{"to":"x"}`},
		{"bad json", `{not-json}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := mustPost(t, srv.URL+"/v1/message", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status: %d want 400", resp.StatusCode)
			}
		})
	}

	// Unknown fields are tolerated on the wire (SQU-55).
	t.Run("unknown field tolerated", func(t *testing.T) {
		resp := mustPost(t, srv.URL+"/v1/message", `{"to":"x","body":"y","foo":1}`)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status: got %d want 200, body=%s", resp.StatusCode, readBody(t, resp))
		}
	})
}

func TestHTTP_Message_MethodGuard(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()
	resp := mustGet(t, srv.URL+"/v1/message")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: %d want 405", resp.StatusCode)
	}
}

func TestHTTP_AuthorityViolationAuditOnly(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New("SQU-92", "worker", "kickoff", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	top := mustParseCustomTopo(t, `
[authority.agents.manager]
allow = ["inbox.send"]
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/message", bytes.NewReader([]byte(`{"from":"worker-squ-92","to":"manager","body":"hello"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{
		Team:     "platform",
		Agent:    "worker",
		Instance: "worker-squ-92",
		Job:      "squ-92",
	}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("message: %d %s", resp.StatusCode, readBody(t, resp))
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != authorityViolationAction || events[0].Origin.Agent != "worker" {
		t.Fatalf("lifecycle events = %+v", events)
	}
	jobEvents, err := jobstore.ListEvents(teamDir, "squ-92")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(jobEvents) != 1 || jobEvents[0].Type != authorityViolationAction || jobEvents[0].Data["verb"] != "inbox.send" {
		t.Fatalf("job events = %+v", jobEvents)
	}
}

func TestHTTP_Channel_PublishSubscribeDrainAck(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	// Publish before any subscriber → message is on disk; subscriber comes
	// in after, gets cursor=1 (head), shouldn't see "first".
	resp := mustPost(t, srv.URL+"/v1/channel/%23room/publish",
		`{"sender":"manager","body":"first"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var pubResp struct {
		Seq int64 `json:"seq"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pubResp); err != nil {
		t.Fatalf("publish decode: %v", err)
	}
	if pubResp.Seq != 1 {
		t.Errorf("first seq: got %d", pubResp.Seq)
	}

	// Subscribe alice.
	resp = mustPost(t, srv.URL+"/v1/channel/%23room/subscribe", `{"instance":"alice"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var subResp struct {
		Cursor     int64 `json:"cursor"`
		Subscribed bool  `json:"subscribed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&subResp); err != nil {
		t.Fatal(err)
	}
	if !subResp.Subscribed {
		t.Errorf("subscribed=false on first subscribe")
	}
	if subResp.Cursor != 1 {
		t.Errorf("cursor: got %d want 1", subResp.Cursor)
	}

	// Re-subscribe is idempotent.
	resp = mustPost(t, srv.URL+"/v1/channel/%23room/subscribe", `{"instance":"alice"}`)
	json.NewDecoder(resp.Body).Decode(&subResp)
	if subResp.Subscribed {
		t.Errorf("subscribed=true on re-subscribe")
	}

	// Drain immediately → empty (cursor at head).
	resp = mustGet(t, srv.URL+"/v1/channel/%23room/messages?instance=alice")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drain: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var drainResp struct {
		Messages []ChannelMessage `json:"messages"`
		Cursor   int64            `json:"cursor"`
	}
	json.NewDecoder(resp.Body).Decode(&drainResp)
	if len(drainResp.Messages) != 0 {
		t.Errorf("immediate drain: got %d want 0", len(drainResp.Messages))
	}

	// Publish two more.
	mustPost(t, srv.URL+"/v1/channel/%23room/publish", `{"sender":"manager","body":"two"}`)
	mustPost(t, srv.URL+"/v1/channel/%23room/publish", `{"sender":"manager","body":"three"}`)

	resp = mustGet(t, srv.URL+"/v1/channel/%23room/messages?instance=alice")
	json.NewDecoder(resp.Body).Decode(&drainResp)
	if len(drainResp.Messages) != 2 {
		t.Errorf("post-publish drain: got %d want 2", len(drainResp.Messages))
	}
	if drainResp.Cursor != 3 {
		t.Errorf("cursor: got %d want 3", drainResp.Cursor)
	}

	// Ack and re-drain → empty.
	ackBody := `{"instance":"alice","cursor":` + jsonNumber(drainResp.Cursor) + `}`
	resp = mustPost(t, srv.URL+"/v1/channel/%23room/ack", ackBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack: %d %s", resp.StatusCode, readBody(t, resp))
	}
	resp = mustGet(t, srv.URL+"/v1/channel/%23room/messages?instance=alice")
	json.NewDecoder(resp.Body).Decode(&drainResp)
	if len(drainResp.Messages) != 0 {
		t.Errorf("post-ack drain: got %d want 0", len(drainResp.Messages))
	}
}

func TestHTTP_Channel_TeamScopedDeclarationUsesScopedStorage(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[channels.supervisor]
scope = "team"

[teams.platform]
channels = ["supervisor"]
`)
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, cs, resolver, teamDir))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/channel/%23supervisor/publish", bytes.NewReader([]byte(`{"sender":"manager","body":"scoped"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(origin.Envelope{Team: "platform", Agent: "manager", Instance: "manager"}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d %s", resp.StatusCode, readBody(t, resp))
	}
	scoped, err := readChannelMessagesSince(root, "#team-platform-supervisor", 0)
	if err != nil {
		t.Fatalf("read scoped channel: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Body != "scoped" {
		t.Fatalf("scoped messages = %+v", scoped)
	}
	unscoped, err := readChannelMessagesSince(root, "#supervisor", 0)
	if err != nil {
		t.Fatalf("read unscoped channel: %v", err)
	}
	if len(unscoped) != 0 {
		t.Fatalf("unscoped messages = %+v, want none", unscoped)
	}
}

func TestHTTP_Channel_DrainSinceParam(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	mustPost(t, srv.URL+"/v1/channel/%23x/publish", `{"sender":"s","body":"a"}`)
	mustPost(t, srv.URL+"/v1/channel/%23x/publish", `{"sender":"s","body":"b"}`)
	mustPost(t, srv.URL+"/v1/channel/%23x/publish", `{"sender":"s","body":"c"}`)
	mustPost(t, srv.URL+"/v1/channel/%23x/subscribe", `{"instance":"bob"}`)

	// since=0 → all three.
	resp := mustGet(t, srv.URL+"/v1/channel/%23x/messages?instance=bob&since=0")
	var dr struct {
		Messages []ChannelMessage `json:"messages"`
	}
	json.NewDecoder(resp.Body).Decode(&dr)
	if len(dr.Messages) != 3 {
		t.Errorf("since=0: got %d want 3", len(dr.Messages))
	}

	// since=2 → only seq 3.
	resp = mustGet(t, srv.URL+"/v1/channel/%23x/messages?instance=bob&since=2")
	json.NewDecoder(resp.Body).Decode(&dr)
	if len(dr.Messages) != 1 || dr.Messages[0].Seq != 3 {
		t.Errorf("since=2: got %+v", dr.Messages)
	}
}

func TestHTTP_Channel_LongPollWait(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	mustPost(t, srv.URL+"/v1/channel/%23live/subscribe", `{"instance":"alice"}`)

	// Issue a wait drain in a goroutine; publish from the main thread; expect
	// the goroutine to wake up before the deadline.
	type result struct {
		body string
		dur  time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, _ := http.Get(srv.URL + "/v1/channel/%23live/messages?instance=alice&wait=3s")
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		resp.Body.Close()
		done <- result{body: buf.String(), dur: time.Since(start)}
	}()

	time.Sleep(100 * time.Millisecond)
	mustPost(t, srv.URL+"/v1/channel/%23live/publish", `{"sender":"x","body":"woke!"}`)

	select {
	case r := <-done:
		if r.dur > 2*time.Second {
			t.Errorf("waited too long: %s — should have woken on publish", r.dur)
		}
		if !strings.Contains(r.body, "woke!") {
			t.Errorf("body=%q", r.body)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("long-poll never returned")
	}
}

func TestHTTP_Channel_List(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	mustPost(t, srv.URL+"/v1/channel/%23a/publish", `{"sender":"s","body":"x"}`)
	mustPost(t, srv.URL+"/v1/channel/%23b/subscribe", `{"instance":"alice"}`)

	resp := mustGet(t, srv.URL+"/v1/channels")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	var infos []ChannelInfo
	json.NewDecoder(resp.Body).Decode(&infos)
	if len(infos) != 2 {
		t.Fatalf("infos: got %d want 2 (%+v)", len(infos), infos)
	}
}

func TestHTTP_Channel_Delete(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	mustPost(t, srv.URL+"/v1/channel/%23gone/publish", `{"sender":"s","body":"x"}`)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/channel/%23gone", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("delete: %d %s", resp.StatusCode, readBody(t, resp))
	}

	// Deleting again → 404.
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/v1/channel/%23gone", nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete-again: got %d want 404", resp.StatusCode)
	}
}

func TestHTTP_Channel_Validation(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	cs := NewChannelStore(root)
	srv := httptest.NewServer(Handler(m, cs, nil, ""))
	defer srv.Close()

	// Bad name (uppercase).
	resp := mustPost(t, srv.URL+"/v1/channel/%23BadName/publish", `{"sender":"s","body":"x"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad name: got %d want 400", resp.StatusCode)
	}
	// Missing body.
	resp = mustPost(t, srv.URL+"/v1/channel/%23ok/publish", `{"sender":"s"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing body: got %d want 400", resp.StatusCode)
	}
	// Drain with missing instance.
	resp = mustGet(t, srv.URL+"/v1/channel/%23ok/messages")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing instance: got %d want 400", resp.StatusCode)
	}
	// Unknown verb.
	resp = mustPost(t, srv.URL+"/v1/channel/%23ok/strange-verb", `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown verb: got %d want 404", resp.StatusCode)
	}
}

func jsonNumber(n int64) string { return strconv.FormatInt(n, 10) }

func mustPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustGetWithBuild(t *testing.T, url string, build buildinfo.Info) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	req.Header.Set(buildinfo.HeaderName, build.HeaderValue())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	return buf.String()
}
