package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/linearwriteback"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/jamesaud/agent-team/internal/worktreecleanup"
	"github.com/jamesaud/agent-team/internal/worktreepolicy"
)

// fixtureTopo parses a small topology used across the event/topology tests.
// One persistent instance (manager) and one ephemeral (worker) with replicas=2.
const fixtureTOML = `
[instances.manager]
agent     = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "user_invocation"

[instances.worker]
agent     = "worker"
ephemeral = true
replicas  = 2

[[instances.worker.triggers]]
event        = "agent.dispatch"
match.target = "worker"
`

func mustParseTopo(t *testing.T) *topology.Topology {
	t.Helper()
	top, err := topology.Parse([]byte(fixtureTOML))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return top
}

func mustParseCustomTopo(t *testing.T, body string) *topology.Topology {
	t.Helper()
	top, err := topology.Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse custom topology: %v", err)
	}
	return top
}

func fixtureTeamDir(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")
	return teamDir
}

func writeFixtureOTelConfig(t *testing.T, teamDir string, enabled bool) {
	t.Helper()
	body := fmt.Sprintf(`[otel]
enabled = %t
endpoint = "http://collector:4318"

[otel.headers]
authorization = "Bearer secret"

[otel.resource]
"deployment.environment" = "test"
`, enabled)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write otel config: %v", err)
	}
}

func writeFixtureAgent(t *testing.T, teamDir, name string) {
	t.Helper()
	agentDir := filepath.Join(teamDir, "agents", name)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ndescription: fixture " + name + "\n---\n\nYou are fixture " + name + ".\n"
	if err := os.WriteFile(filepath.Join(agentDir, "agent.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func (f *fakeSpawner) lastEnv() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.envs) == 0 {
		return nil
	}
	return append([]string(nil), f.envs[len(f.envs)-1]...)
}

func (f *fakeSpawner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsEnvPrefix(items []string, prefix string) bool {
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func argValue(items []string, flag string) (string, bool) {
	for i := 0; i+1 < len(items); i++ {
		if items[i] == flag {
			return items[i+1], true
		}
	}
	return "", false
}

func containsArgSubstring(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(item, want) {
			return true
		}
	}
	return false
}

func TestEvent_PersistentMessages(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, root, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{"type":"user_invocation","payload":{"name":"manager"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Matched  []string         `json:"matched"`
		Messaged []string         `json:"messaged"`
		Rejected []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Matched) != 1 || got.Matched[0] != "manager" {
		t.Errorf("matched: %v", got.Matched)
	}
	if len(got.Messaged) != 1 || got.Messaged[0] != "manager" {
		t.Errorf("messaged: %v", got.Messaged)
	}
	if len(got.Rejected) != 0 {
		t.Errorf("rejected: %v", got.Rejected)
	}

	// Mailbox file should now contain one message.
	body, err := os.ReadFile(MailboxPath(root, "manager"))
	if err != nil {
		t.Fatalf("mailbox: %v", err)
	}
	if !strings.Contains(string(body), `\"event\":\"user_invocation\"`) {
		t.Errorf("mailbox missing event: %s", string(body))
	}
}

func TestEvent_TraceResponseExplainsRejectedTriggers(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, root, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{"type":"agent.dispatch","payload":{"target":"manager"},"trace":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Matched []string            `json:"matched"`
		Trace   topology.EventTrace `json:"trace"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Matched) != 0 || got.Trace.MatchedRules != 0 {
		t.Fatalf("got matched=%v trace=%+v, want no matches", got.Matched, got.Trace)
	}
	worker := traceEntryByScope(t, got.Trace, "instances.worker")
	if worker.Matched || worker.Matcher != "match.target=worker" || worker.Reason != "payload target=manager != worker" {
		t.Fatalf("worker trace = %+v", worker)
	}
	manager := traceEntryByScope(t, got.Trace, "instances.manager")
	if manager.Matched || manager.Reason != "event type mismatch" {
		t.Fatalf("manager trace = %+v", manager)
	}
}

func TestEvent_ZeroMatchLogsWarning(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, root, mustParseTopo(t))
	var logs bytes.Buffer
	resolver.SetLogOutput(&logs)

	result, err := resolver.EventWithResult("agent.dispatch", map[string]any{"target": "manager"})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if result.Trace == nil || result.Trace.MatchedRules != 0 {
		t.Fatalf("trace = %+v, want zero matched rules", result.Trace)
	}
	logText := logs.String()
	if !strings.Contains(logText, "WARNING event \"agent.dispatch\" matched 0 rules") || !strings.Contains(logText, `"target":"manager"`) {
		t.Fatalf("warning log = %q", logText)
	}
}

func TestEvent_PersistentAgentDispatchQueuesWhenStopped(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "agent.dispatch"
match.target = "manager"
`)
	resolver := NewEventResolver(m, root, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{"type":"agent.dispatch","payload":{"target":"manager","ticket":"SQU-301"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Queued   []string `json:"queued"`
		Messaged []string `json:"messaged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Queued) != 1 || got.Queued[0] != "manager" || len(got.Messaged) != 0 {
		t.Fatalf("outcome = %+v, want queued manager", got)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"target":"manager"`) {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestEvent_AgentDispatchPipelinePersistentTargetActuatesOnce(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_review]
trigger.event = "agent.dispatch"
trigger.match.target = "manager"

[[pipelines.ticket_review.steps]]
id = "review"
target = "manager"
`)
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "manager",
		"name":    "manager-squ-715-review",
		"ticket":  "SQU-715",
		"kickoff": "review SQU-715",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Instance != "manager" || result.Outcomes[0].Action != "queued" {
		t.Fatalf("outcomes = %+v, want one queued manager outcome", result.Outcomes)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"target":"manager"`) {
		t.Fatalf("messages = %+v, want one manager dispatch message", messages)
	}
	j, err := jobstore.Read(teamDir, "squ-715")
	if err != nil {
		t.Fatalf("read pipeline job: %v", err)
	}
	if j.Status != jobstore.StatusQueued || len(j.Steps) != 1 || j.Steps[0].Status != jobstore.StatusQueued || j.Steps[0].Instance != "manager" {
		t.Fatalf("pipeline job = %+v, want queued manager step", j)
	}
}

func TestEvent_PersistentAgentDispatchMessagesWhenRunning(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	if _, err := m.Dispatch(DispatchInput{
		Agent:     "manager",
		Name:      "manager",
		Prompt:    "idle",
		Workspace: t.TempDir(),
	}); err != nil {
		t.Fatalf("dispatch manager: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "agent.dispatch"
match.target = "manager"
`)
	resolver := NewEventResolver(m, root, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()
	t.Cleanup(func() {
		_, _ = m.Stop("manager")
		_ = m.WaitForReaper("manager", 5*time.Second)
	})

	resp := mustPost(t, srv.URL+"/v1/event", `{"type":"agent.dispatch","payload":{"target":"manager","ticket":"SQU-302"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Queued   []string `json:"queued"`
		Messaged []string `json:"messaged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Messaged) != 1 || got.Messaged[0] != "manager" || len(got.Queued) != 0 {
		t.Fatalf("outcome = %+v, want messaged manager", got)
	}
}

func TestEvent_EphemeralDispatchUnderCapacity(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, fixtureTeamDir(t), mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Matched    []string         `json:"matched"`
		Dispatched []map[string]any `json:"dispatched"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Dispatched) != 1 {
		t.Fatalf("expected 1 dispatched, got %+v", got)
	}
	id, _ := got.Dispatched[0]["instance_id"].(string)
	if !strings.HasPrefix(id, "worker-") {
		t.Errorf("instance_id should be unique-prefixed, got %q", id)
	}
	running, queued := resolver.QueueDepth("worker")
	if running != 1 || queued != 0 {
		t.Errorf("counts: running=%d queued=%d", running, queued)
	}
}

func TestEvent_AgentDispatchPipelineEphemeralTargetActuatesOnce(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true
replicas = 2

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "agent.dispatch"
trigger.match.target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":    "worker",
		"name":      "worker-squ-716-implement",
		"ticket":    "SQU-716",
		"kickoff":   "implement SQU-716",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-squ-716-implement")
		_ = m.WaitForReaper("worker-squ-716-implement", 5*time.Second)
	})
	if len(result.Outcomes) != 1 || result.Outcomes[0].Instance != "worker" || result.Outcomes[0].Action != "dispatched" || result.Outcomes[0].InstanceID != "worker-squ-716-implement" {
		t.Fatalf("outcomes = %+v, want one dispatched worker outcome", result.Outcomes)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1", fake.callCount())
	}
	j, err := jobstore.Read(teamDir, "squ-716")
	if err != nil {
		t.Fatalf("read pipeline job: %v", err)
	}
	if j.Status != jobstore.StatusRunning || len(j.Steps) != 1 || j.Steps[0].Status != jobstore.StatusRunning || j.Steps[0].Instance != "worker-squ-716-implement" {
		t.Fatalf("pipeline job = %+v, want running worker step", j)
	}
}

func TestEvent_EphemeralDispatchUsesRequestedChildName(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, fixtureTeamDir(t), mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-42","kickoff":"implement SQU-42"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
		Rejected   []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Rejected) != 0 {
		t.Fatalf("unexpected rejection: %+v", got.Rejected)
	}
	if len(got.Dispatched) != 1 {
		t.Fatalf("expected 1 dispatched, got %+v", got)
	}
	if id, _ := got.Dispatched[0]["instance_id"].(string); id != "worker-squ-42" {
		t.Fatalf("instance_id = %q, want worker-squ-42", id)
	}
	call := fake.lastCall()
	if len(call) < 11 || call[0] != "claude" || call[1] != "--session-id" || call[2] == "" {
		t.Fatalf("spawn call = %#v, want claude --session-id <id> with agent runtime args", call)
	}
	for _, want := range []string{"--agents", "--add-dir", "--append-system-prompt-file", "-p"} {
		if !containsString(call, want) {
			t.Fatalf("spawn call missing %q: %#v", want, call)
		}
	}
	var prompt string
	for i := 0; i < len(call)-1; i++ {
		if call[i] == "-p" {
			prompt = call[i+1]
			break
		}
	}
	if !strings.Contains(prompt, `"name":"worker-squ-42"`) {
		t.Fatalf("prompt missing requested child name: %s", prompt)
	}
}

func TestEvent_EphemeralDispatchDeliversUnreadMailboxInKickoff(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	first := &Message{ID: "mail-1", From: "manager", Body: "first steer"}
	second := &Message{ID: "mail-2", From: "reviewer", Body: "second steer"}
	if err := AppendMessage(root, "worker-squ-64", first); err != nil {
		t.Fatalf("append first mail: %v", err)
	}
	if err := AppendMessage(root, "worker-squ-64", second); err != nil {
		t.Fatalf("append second mail: %v", err)
	}

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-squ-64",
		"ticket":  "SQU-64",
		"job_id":  "squ-64",
		"kickoff": "implement SQU-64",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-squ-64")
		_ = m.WaitForReaper("worker-squ-64", 5*time.Second)
	})

	prompt, ok := argValue(fake.lastCall(), "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", fake.lastCall())
	}
	for _, want := range []string{kickoffMailboxHeading, "first steer", "second steer", "From: manager", "From: reviewer"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	unread, err := ReadUnacked(root, "worker-squ-64")
	if err != nil {
		t.Fatalf("ReadUnacked: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread after dispatch = %+v, want none", unread)
	}
	cursor, err := ReadCursor(root, "worker-squ-64")
	if err != nil {
		t.Fatalf("ReadCursor: %v", err)
	}
	if cursor != second.ID {
		t.Fatalf("cursor = %q, want %q", cursor, second.ID)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if !lifecycleEventsContain(events, "kickoff_mail_delivered", "worker-squ-64") {
		t.Fatalf("lifecycle events missing kickoff_mail_delivered: %+v", events)
	}
	jobEvents, err := jobstore.ListEvents(teamDir, "squ-64")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, ev := range jobEvents {
		if ev.Type == "kickoff_mail_delivered" && ev.Actor == "daemon" && ev.Instance == "worker-squ-64" && ev.Data["messages"] == "2" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("job events missing kickoff_mail_delivered: %+v", jobEvents)
	}
}

func TestEvent_EphemeralDispatchWiresMailboxHookForClaude(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-hooks",
		"kickoff": "implement with hooks",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-hooks")
		_ = m.WaitForReaper("worker-hooks", 5*time.Second)
	})

	call := fake.lastCall()
	settingsPath, ok := argValue(call, "--settings")
	if !ok {
		t.Fatalf("spawn call missing --settings mailbox hook: %#v", call)
	}
	body, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	for _, want := range []string{"UserPromptSubmit", "PreToolUse", "agent-team-mailbox-inject.py"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("settings missing %q:\n%s", want, body)
		}
	}
}

func TestEvent_EphemeralDispatchMailboxHookOptOut(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent     = "worker"
ephemeral = true

[instances.worker.config.runtime.hooks]
mailbox_injection = false

[[instances.worker.triggers]]
event        = "agent.dispatch"
match.target = "worker"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-no-hooks",
		"kickoff": "implement without hooks",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-no-hooks")
		_ = m.WaitForReaper("worker-no-hooks", 5*time.Second)
	})
	if _, ok := argValue(fake.lastCall(), "--settings"); ok {
		t.Fatalf("spawn call unexpectedly includes --settings after opt-out: %#v", fake.lastCall())
	}
}

func TestEvent_EphemeralDispatchInjectsClaudeOTel(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureOTelConfig(t, teamDir, true)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":        "worker",
		"name":          "worker-otel",
		"job_id":        "squ-74",
		"ticket":        "SQU-74",
		"pipeline":      "ticket_to_pr",
		"pipeline_step": "implement",
		"team":          "delivery",
		"kickoff":       "implement otel",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-otel")
		_ = m.WaitForReaper("worker-otel", 5*time.Second)
	})
	env := fake.lastEnv()
	for _, want := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318",
		"OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer secret",
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q: %#v", want, env)
		}
	}
	if !containsEnvPrefix(env, "TRACEPARENT=00-") {
		t.Fatalf("env missing TRACEPARENT: %#v", env)
	}
	resource := envValue(env, "OTEL_RESOURCE_ATTRIBUTES")
	for _, want := range []string{
		"service.name=agent-team/worker",
		"agent_team.instance=worker-otel",
		"agent_team.job_id=squ-74",
		"agent_team.ticket=SQU-74",
		"agent_team.pipeline=ticket_to_pr",
		"agent_team.pipeline_step=implement",
		"agent_team.team=delivery",
		"agent_team.runtime=claude",
		"deployment.environment=test",
	} {
		if !strings.Contains(resource, want) {
			t.Fatalf("resource attrs missing %q in %q", want, resource)
		}
	}
}

func TestEvent_EphemeralCodexDispatchWiresMailboxHook(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-codex-hooks",
		"runtime": "codex",
		"kickoff": "implement with codex hooks",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-codex-hooks")
		_ = m.WaitForReaper("worker-codex-hooks", 5*time.Second)
	})
	call := fake.lastCall()
	if len(call) < 2 || call[0] != "codex" || call[1] != "exec" {
		t.Fatalf("spawn call = %#v, want codex exec", call)
	}
	if !containsString(call, "--dangerously-bypass-hook-trust") {
		t.Fatalf("codex spawn missing hook trust bypass: %#v", call)
	}
	for _, want := range []string{"hooks.UserPromptSubmit", "hooks.PreToolUse", "agent-team-mailbox-inject.py"} {
		if !containsArgSubstring(call, want) {
			t.Fatalf("codex spawn missing %q: %#v", want, call)
		}
	}
}

func TestEvent_EphemeralCodexDispatchInjectsOTel(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureOTelConfig(t, teamDir, true)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-codex-otel",
		"runtime": "codex",
		"job_id":  "squ-74",
		"ticket":  "SQU-74",
		"team":    "delivery",
		"kickoff": "implement with codex otel",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-codex-otel")
		_ = m.WaitForReaper("worker-codex-otel", 5*time.Second)
	})
	env := fake.lastEnv()
	if !containsEnvPrefix(env, "TRACEPARENT=00-") {
		t.Fatalf("codex env missing TRACEPARENT: %#v", env)
	}
	if !containsString(env, "AGENTTEAM_OTEL_HEADER_0=Bearer secret") {
		t.Fatalf("codex env missing header indirection: %#v", env)
	}
	call := fake.lastCall()
	joined := strings.Join(call, "\n")
	if strings.Contains(joined, "Bearer secret") {
		t.Fatalf("codex spawn leaked header secret in argv:\n%s", joined)
	}
	for _, want := range []string{
		"otel.exporter={ otlp-http = { endpoint = \"http://collector:4318\", protocol = \"binary\", headers = { \"authorization\" = \"${AGENTTEAM_OTEL_HEADER_0}\" } } }",
		"otel.trace_exporter=\"otlp-http\"",
		"otel.trace_exporter.\"otlp-http\".endpoint=\"http://collector:4318\"",
		"otel.trace_exporter.\"otlp-http\".protocol=\"binary\"",
		"otel.trace_exporter.\"otlp-http\".headers={ \"authorization\" = \"${AGENTTEAM_OTEL_HEADER_0}\" }",
		"otel.log_user_prompt=false",
		"otel.span_attributes={",
		"\"service.name\" = \"agent-team/worker\"",
		"shell_environment_policy.set.TRACEPARENT=",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("codex spawn missing %q:\n%s", want, joined)
		}
	}
}

func TestEvent_EphemeralDispatchOTelDisabledNoOp(t *testing.T) {
	t.Setenv("CLAUDE_CODE_ENABLE_TELEMETRY", "1")
	t.Setenv("CLAUDE_CODE_ENHANCED_TELEMETRY_BETA", "1")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://stale")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "stale=true")
	t.Setenv("TRACEPARENT", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
	t.Setenv("TRACESTATE", "stale")
	t.Setenv("AGENTTEAM_OTEL_HEADER_0", "stale-secret")

	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureOTelConfig(t, teamDir, false)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-otel-disabled",
		"runtime": "codex",
		"kickoff": "disabled otel",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-otel-disabled")
		_ = m.WaitForReaper("worker-otel-disabled", 5*time.Second)
	})
	env := fake.lastEnv()
	for _, forbidden := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=",
		"OTEL_EXPORTER_OTLP_ENDPOINT=",
		"OTEL_RESOURCE_ATTRIBUTES=",
		"TRACEPARENT=",
		"TRACESTATE=",
		"AGENTTEAM_OTEL_HEADER_",
	} {
		if containsEnvPrefix(env, forbidden) {
			t.Fatalf("disabled otel env included %q: %#v", forbidden, env)
		}
	}
	if containsArgSubstring(fake.lastCall(), "otel.exporter") || containsArgSubstring(fake.lastCall(), "otel.trace_exporter") {
		t.Fatalf("disabled otel args included otel config: %#v", fake.lastCall())
	}
}

func TestEvent_EphemeralDispatchLeavesKickoffAloneWhenNoUnreadMailbox(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-no-mail",
		"kickoff": "implement without mail",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-no-mail")
		_ = m.WaitForReaper("worker-no-mail", 5*time.Second)
	})
	prompt, ok := argValue(fake.lastCall(), "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", fake.lastCall())
	}
	if strings.Contains(prompt, kickoffMailboxHeading) {
		t.Fatalf("prompt unexpectedly contains mailbox section:\n%s", prompt)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if lifecycleEventsContain(events, "kickoff_mail_delivered", "worker-no-mail") {
		t.Fatalf("lifecycle events unexpectedly include kickoff_mail_delivered: %+v", events)
	}
}

func TestEvent_EphemeralDispatchTruncatesUnreadMailboxKickoff(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	msg := &Message{ID: "mail-big", From: "manager", Body: strings.Repeat("x", kickoffMailboxMaxBytes*2)}
	if err := AppendMessage(root, "worker-big-mail", msg); err != nil {
		t.Fatalf("append mail: %v", err)
	}
	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-big-mail",
		"ticket":  "SQU-65",
		"job_id":  "squ-65",
		"kickoff": "implement SQU-65",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-big-mail")
		_ = m.WaitForReaper("worker-big-mail", 5*time.Second)
	})

	prompt, ok := argValue(fake.lastCall(), "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", fake.lastCall())
	}
	idx := strings.Index(prompt, kickoffMailboxHeading)
	if idx < 0 {
		t.Fatalf("prompt missing mailbox heading:\n%s", prompt)
	}
	section := prompt[idx:]
	if len(section) > kickoffMailboxMaxBytes {
		t.Fatalf("mailbox section length = %d, want <= %d", len(section), kickoffMailboxMaxBytes)
	}
	if !strings.Contains(section, "truncated") {
		t.Fatalf("truncated mailbox section missing note:\n%s", section)
	}
	unread, err := ReadUnacked(root, "worker-big-mail")
	if err != nil {
		t.Fatalf("ReadUnacked: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread after truncated dispatch = %+v, want none", unread)
	}
	jobEvents, err := jobstore.ListEvents(teamDir, "squ-65")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, ev := range jobEvents {
		if ev.Type == "kickoff_mail_delivered" && ev.Data["truncated"] == "true" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("job events missing truncated kickoff_mail_delivered: %+v", jobEvents)
	}
}

func TestEvent_EphemeralJobExitPreservesMetadataAndCompletesJob(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Now().UTC()
	j, err := jobstore.New("SQU-96", "worker", "finish quickly", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-96","ticket":"SQU-96","job_id":"squ-96","kickoff":"finish quickly"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := m.WaitForReaper("worker-squ-96", 3*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	meta, err := ReadMetadata(root, "worker-squ-96")
	if err != nil {
		t.Fatalf("metadata should be preserved after ephemeral exit: %v", err)
	}
	if meta.Status != StatusExited || meta.Job != "squ-96" || meta.ExitCode == nil || *meta.ExitCode != 0 {
		t.Fatalf("metadata = %+v", meta)
	}
	updated, err := jobstore.Read(teamDir, "squ-96")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != jobstore.StatusDone || updated.LastEvent != "instance_exited" || updated.Instance != "worker-squ-96" {
		t.Fatalf("updated job = %+v", updated)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-96")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != "instance_exited" || events[len(events)-1].Actor != "daemon" {
		t.Fatalf("events = %+v", events)
	}
	running, queued := resolver.QueueDepth("worker")
	if running != 0 || queued != 0 {
		t.Fatalf("queue depth running=%d queued=%d, want 0/0", running, queued)
	}
}

func TestEvent_EphemeralDispatchUsesCodexRuntime(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-42","kickoff":"implement SQU-42"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	call := fake.lastCall()
	if len(call) < 2 || call[0] != "codex" || call[1] != "exec" {
		t.Fatalf("spawn call = %#v, want codex exec", call)
	}
	for _, forbidden := range []string{"--session-id", "--agents", "--append-system-prompt-file"} {
		if containsString(call, forbidden) {
			t.Fatalf("codex spawn call should not include %q: %#v", forbidden, call)
		}
	}
	for _, want := range []string{"-C", "--add-dir"} {
		if !containsString(call, want) {
			t.Fatalf("codex spawn call missing %q: %#v", want, call)
		}
	}
	// Autonomous workers must run unsandboxed (codex exec is read-only /
	// no-network by default, which makes a worker a no-op). Isolation comes
	// from the per-worker git worktree, not the codex sandbox.
	if !containsString(call, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("codex spawn call must bypass the sandbox for autonomous work: %#v", call)
	}
	wantLastMessage := filepath.Join(teamDir, "state", "worker-squ-42", runtimebin.CodexLastMessageFile)
	if got, ok := argValue(call, "--output-last-message"); !ok || got != wantLastMessage {
		t.Fatalf("codex spawn call last-message path = %q, %v; want %q in %#v", got, ok, wantLastMessage, call)
	}
	for _, want := range []string{
		"shell_environment_policy.set.AGENT_TEAM_ROOT=" + strconv.Quote(teamDir),
		"shell_environment_policy.set.AGENT_TEAM_INSTANCE=" + strconv.Quote("worker-squ-42"),
		"shell_environment_policy.set.AGENT_TEAM_STATE_DIR=" + strconv.Quote(filepath.Join(teamDir, "state", "worker-squ-42")),
		"shell_environment_policy.set.AGENT_TEAM_DAEMON_SOCKET=" + strconv.Quote(SocketPath(teamDir)),
	} {
		if !containsString(call, want) {
			t.Fatalf("codex spawn call missing env config %q: %#v", want, call)
		}
	}
	if call[len(call)-1] != "-" {
		t.Fatalf("codex spawn call prompt arg = %q, want stdin marker '-' in %#v", call[len(call)-1], call)
	}
	stdin := fake.lastStdin()
	if !strings.Contains(stdin, "implement SQU-42") || !strings.Contains(stdin, "This session is running through the Codex adapter.") {
		t.Fatalf("codex stdin missing kickoff or adapter prompt:\n%s", stdin)
	}
	meta, err := ReadMetadata(root, "worker-squ-42")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Runtime != string(runtimebin.KindCodex) || meta.SessionID != "" {
		t.Fatalf("metadata = %+v, want codex without Claude session", meta)
	}
	_, _ = m.Stop("worker-squ-42")
	_ = m.WaitForReaper("worker-squ-42", 5*time.Second)
}

func TestEvent_EphemeralDispatchUsesRepoCodexRuntimeConfig(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[runtime]\nkind = \"codex\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-43","kickoff":"implement SQU-43"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	call := fake.lastCall()
	if len(call) < 2 || call[0] != "codex" || call[1] != "exec" {
		t.Fatalf("spawn call = %#v, want config-backed codex exec", call)
	}
	meta, err := ReadMetadata(root, "worker-squ-43")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Runtime != string(runtimebin.KindCodex) || meta.RuntimeBinary != "codex" || meta.SessionID != "" {
		t.Fatalf("metadata = %+v, want config-backed codex without Claude session", meta)
	}
	_, _ = m.Stop("worker-squ-43")
	_ = m.WaitForReaper("worker-squ-43", 5*time.Second)
}

func TestEvent_EphemeralDispatchUsesAgentFrontmatterRuntime(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	// Declare the worker as a Codex agent in its frontmatter. With no env
	// override and no repo [runtime] config, only the agent-level default can
	// make this spawn Codex instead of the built-in Claude default.
	agentMD := "---\ndescription: fixture worker\nruntime: codex\n---\n\nYou are fixture worker.\n"
	if err := os.WriteFile(filepath.Join(teamDir, "agents", "worker", "agent.md"), []byte(agentMD), 0o644); err != nil {
		t.Fatalf("write agent.md: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-77","kickoff":"implement SQU-77"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	call := fake.lastCall()
	if len(call) < 2 || call[0] != "codex" || call[1] != "exec" {
		t.Fatalf("spawn call = %#v, want agent-frontmatter codex exec", call)
	}
	meta, err := ReadMetadata(root, "worker-squ-77")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Runtime != string(runtimebin.KindCodex) || meta.SessionID != "" {
		t.Fatalf("metadata = %+v, want agent-frontmatter codex without Claude session", meta)
	}
	_, _ = m.Stop("worker-squ-77")
	_ = m.WaitForReaper("worker-squ-77", 5*time.Second)
}

func TestDispatchRuntime_AgentFrontmatterPrecedence(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	teamDir := fixtureTeamDir(t)
	agentMD := "---\ndescription: fixture worker\nruntime: codex\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(teamDir, "agents", "worker", "agent.md"), []byte(agentMD), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewInstanceManager(DaemonRoot(teamDir), nil)

	// The agent's frontmatter runtime is used when nothing overrides it.
	rt, err := m.dispatchRuntime(DispatchInput{Agent: "worker", Name: "w", Workspace: "ws"})
	if err != nil {
		t.Fatalf("dispatchRuntime: %v", err)
	}
	if rt.Kind != runtimebin.KindCodex {
		t.Fatalf("frontmatter runtime = %q, want codex", rt.Kind)
	}

	// An explicit dispatch runtime outranks the agent default.
	rt, err = m.dispatchRuntime(DispatchInput{Agent: "worker", Runtime: "claude", Name: "w", Workspace: "ws"})
	if err != nil {
		t.Fatalf("dispatchRuntime explicit: %v", err)
	}
	if rt.Kind != runtimebin.KindClaude {
		t.Fatalf("explicit runtime = %q, want claude", rt.Kind)
	}

	// An AGENT_TEAM_RUNTIME env override also outranks the agent default.
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindClaude))
	rt, err = m.dispatchRuntime(DispatchInput{Agent: "worker", Name: "w", Workspace: "ws"})
	if err != nil {
		t.Fatalf("dispatchRuntime env: %v", err)
	}
	if rt.Kind != runtimebin.KindClaude {
		t.Fatalf("env runtime = %q, want claude", rt.Kind)
	}
}

func TestEvent_EphemeralDispatchPayloadRuntimeOverridesEnv(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindClaude))
	t.Setenv(runtimebin.EnvBinary, "claude-wrapper")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-44","kickoff":"implement SQU-44","runtime":"codex","runtime_binary":"codex-dev"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	call := fake.lastCall()
	if len(call) < 2 || call[0] != "codex-dev" || call[1] != "exec" {
		t.Fatalf("spawn call = %#v, want payload-backed codex-dev exec", call)
	}
	meta, err := ReadMetadata(root, "worker-squ-44")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Runtime != string(runtimebin.KindCodex) || meta.RuntimeBinary != "codex-dev" || meta.SessionID != "" {
		t.Fatalf("metadata = %+v, want payload-backed codex-dev without Claude session", meta)
	}
	_, _ = m.Stop("worker-squ-44")
	_ = m.WaitForReaper("worker-squ-44", 5*time.Second)
}

func TestEvent_DirectDispatchWithJobIDWritesLinearInProgress(t *testing.T) {
	// SQU-68 round-5 finding: a direct agent.dispatch that attaches a job via
	// job_id (no pipeline_step) must still attempt the in-progress write-back.
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "worker")
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[team]\npm_tool = \"linear\"\n\n[linear]\nteam_id = \"demo\"\nticket_prefix = \"SQU\"\nin_progress_state = \"In Progress\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-96","ticket":"SQU-96","job_id":"squ-96","kickoff":"direct dispatch","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	events, err := jobstore.ListEvents(teamDir, "squ-96")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "linear_writeback_skipped" && ev.Data["action"] == string(linearwriteback.ActionDispatchInProgress) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("direct job_id dispatch missing in-progress write-back attempt: %+v", events)
	}
	_, _ = m.Stop("worker-squ-96")
	_ = m.WaitForReaper("worker-squ-96", 5*time.Second)
}

func TestEvent_TicketDispatchCreatesJobAndExportsContext(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_USER_API_KEY", "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`
[team]
pm_tool = "linear"

[linear]
team_id = "team-1"
ticket_prefix = "SQU"
in_progress_state = "In Progress"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-95","ticket":"SQU-95","ticket_url":"https://linear.app/squirtlesquad/issue/SQU-95/context","kickoff":"implement SQU-95","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	j, err := jobstore.Read(teamDir, "squ-95")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Status != jobstore.StatusRunning || j.Instance != "worker-squ-95" || j.Target != "worker" || j.Kickoff != "implement SQU-95" || j.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-95/context" {
		t.Fatalf("job = %+v", j)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-95")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	foundLinearDispatch := false
	for _, ev := range events {
		if ev.Type == "linear_writeback_skipped" &&
			ev.Message == "no Linear API key found" &&
			ev.Data["action"] == string(linearwriteback.ActionDispatchInProgress) &&
			ev.Data["state"] == "In Progress" {
			foundLinearDispatch = true
			break
		}
	}
	if !foundLinearDispatch {
		t.Fatalf("events missing dispatch in-progress write-back attempt: %+v", events)
	}
	meta, err := ReadMetadata(root, "worker-squ-95")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Job != "squ-95" || meta.Ticket != "SQU-95" {
		t.Fatalf("metadata = %+v", meta)
	}
	env := fake.lastEnv()
	for _, want := range []string{
		"AGENT_TEAM_JOB_ID=squ-95",
		"AGENT_TEAM_TICKET=SQU-95",
		"AGENT_TEAM_TICKET_URL=https://linear.app/squirtlesquad/issue/SQU-95/context",
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q in %v", want, env)
		}
	}
	_, _ = m.Stop("worker-squ-95")
	_ = m.WaitForReaper("worker-squ-95", 5*time.Second)
}

func TestEvent_EphemeralDispatchRejectsDuplicateRequestedChildName(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, fixtureTeamDir(t), mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	body := `{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-42","kickoff":"implement SQU-42"}}`
	resp := mustPost(t, srv.URL+"/v1/event", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	resp = mustPost(t, srv.URL+"/v1/event", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
		Rejected   []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Dispatched) != 0 {
		t.Fatalf("second dispatch should not spawn, got %+v", got.Dispatched)
	}
	if len(got.Rejected) != 1 {
		t.Fatalf("expected duplicate rejection, got %+v", got)
	}
	reason, _ := got.Rejected[0]["reason"].(string)
	if !strings.Contains(reason, `instance "worker-squ-42" already running`) {
		t.Fatalf("rejection reason = %q", reason)
	}
	if calls := fake.callCount(); calls != 1 {
		t.Fatalf("spawner calls = %d, want 1", calls)
	}
}

func TestEvent_TicketWorktreeDispatchNamesBranchFromTicket(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "init")
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")

	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-42","ticket":"SQU-42","workspace":"worktree","kickoff":"implement SQU-42"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
		Rejected   []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Rejected) != 0 || len(got.Dispatched) != 1 {
		t.Fatalf("dispatch response = %+v", got)
	}
	meta, err := ReadMetadata(root, "worker-squ-42")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if !regexp.MustCompile(`^squ-42-[0-9a-f]{8}$`).MatchString(meta.Branch) {
		t.Fatalf("branch = %q, want squ-42-<8hex>", meta.Branch)
	}
	current, err := exec.Command("git", "-C", meta.Workspace, "branch", "--show-current").Output()
	if err != nil {
		t.Fatalf("show worktree branch: %v", err)
	}
	if strings.TrimSpace(string(current)) != meta.Branch {
		t.Fatalf("worktree branch = %q, want metadata branch %q", strings.TrimSpace(string(current)), meta.Branch)
	}
	j, err := jobstore.Read(teamDir, "squ-42")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Branch != meta.Branch || j.Worktree != meta.Workspace {
		t.Fatalf("job ownership = branch %q worktree %q, want %q %q", j.Branch, j.Worktree, meta.Branch, meta.Workspace)
	}
	if !containsString(fake.lastEnv(), "AGENT_TEAM_BRANCH="+meta.Branch) {
		t.Fatalf("env missing AGENT_TEAM_BRANCH=%s in %v", meta.Branch, fake.lastEnv())
	}
	_, _ = m.Stop("worker-squ-42")
	_ = m.WaitForReaper("worker-squ-42", 5*time.Second)
}

func TestEvent_EphemeralDispatchCanCreateWorktreeWorkspace(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "init")
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")

	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-42","workspace":"worktree","kickoff":"implement SQU-42"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
		Rejected   []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Rejected) != 0 || len(got.Dispatched) != 1 {
		t.Fatalf("dispatch response = %+v", got)
	}
	meta, err := ReadMetadata(root, "worker-squ-42")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	wantPrefix := filepath.Join(repoRoot, ".claude", "worktrees", "worker-squ-42-")
	if !strings.HasPrefix(meta.Workspace, wantPrefix) {
		t.Fatalf("workspace = %q, want prefix %q", meta.Workspace, wantPrefix)
	}
	wantBranchPrefix := "worktree-worker-squ-42-"
	if !strings.HasPrefix(meta.Branch, wantBranchPrefix) || len(meta.Branch) != len(wantBranchPrefix)+8 {
		t.Fatalf("branch = %q, want prefix %q plus 8-char tag", meta.Branch, wantBranchPrefix)
	}
	if _, err := os.Stat(filepath.Join(meta.Workspace, "README.md")); err != nil {
		t.Fatalf("worktree missing README: %v", err)
	}
	// The worktree's own git exclude keeps the per-worker scratch dir out of commits.
	gd, gerr := exec.Command("git", "-C", meta.Workspace, "rev-parse", "--absolute-git-dir").Output()
	if gerr != nil {
		t.Fatalf("rev-parse worktree git dir: %v", gerr)
	}
	exc, eerr := os.ReadFile(filepath.Join(strings.TrimSpace(string(gd)), "info", "exclude"))
	if eerr != nil || !strings.Contains(string(exc), ".worker_agent/") {
		t.Fatalf("worktree exclude missing .worker_agent/: err=%v content=%q", eerr, string(exc))
	}
	_, _ = m.Stop("worker-squ-42")
	_ = m.WaitForReaper("worker-squ-42", 5*time.Second)
}

func TestEvent_EphemeralJobExitAutoReapsWorktreeOnClose(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "init")
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")
	now := time.Now().UTC()
	j, err := jobstore.New("SQU-142", "worker", "finish and clean", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.ReapWorktree = worktreepolicy.OnClose
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	previousCheck := worktreecleanup.LiveProcessReferenceCheck
	worktreecleanup.LiveProcessReferenceCheck = func(string) (bool, error) {
		return false, nil
	}
	defer func() {
		worktreecleanup.LiveProcessReferenceCheck = previousCheck
	}()

	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-142","workspace":"worktree","ticket":"SQU-142","job_id":"squ-142","reap_worktree":"on_close","kickoff":"finish and clean"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	meta, err := ReadMetadata(root, "worker-squ-142")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	worktreePath := meta.Workspace
	branch := meta.Branch
	if worktreePath == "" || branch == "" {
		t.Fatalf("metadata missing worktree ownership: %+v", meta)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree missing before stop: %v", err)
	}

	if err := m.WaitForReaper("worker-squ-142", 5*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	updated, err := jobstore.Read(teamDir, "squ-142")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != jobstore.StatusDone || updated.Worktree != "" || updated.Branch != "" || updated.LastEvent != "cleanup" {
		t.Fatalf("updated job = %+v", updated)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists or stat error: %v", err)
	}
	out, err := exec.Command("git", "-C", repoRoot, "branch", "--list", branch, "--format", "%(refname:short)").Output()
	if err != nil {
		t.Fatalf("list branch: %v", err)
	}
	if strings.TrimSpace(string(out)) == branch {
		t.Fatalf("branch %s still exists after daemon cleanup", branch)
	}
}

func TestEvent_CodexWorktreeRunsInWorktreeCwd(t *testing.T) {
	// A worktree-isolated Codex worker must run `codex exec -C <worktree>`, not
	// `-C <repo root>` — otherwise its edits/branch/commits land on the main
	// checkout and break isolation.
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	root := t.TempDir()
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "init")
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")

	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-42","workspace":"worktree","kickoff":"go"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	meta, err := ReadMetadata(root, "worker-squ-42")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	call := fake.lastCall()
	got, ok := argValue(call, "-C")
	if !ok || got != meta.Workspace {
		t.Fatalf("codex -C = %q (ok=%v), want the worktree %q: %#v", got, ok, meta.Workspace, call)
	}
	if got == repoRoot {
		t.Fatalf("codex -C must be the worktree, not the repo root %q", repoRoot)
	}
	_, _ = m.Stop("worker-squ-42")
	_ = m.WaitForReaper("worker-squ-42", 5*time.Second)
}

func TestEvent_EphemeralDispatchRejectsUnsafeRequestedChildName(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, fixtureTeamDir(t), mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"../worker-squ-42"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
		Rejected   []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Dispatched) != 0 {
		t.Fatalf("dispatched should be empty, got %+v", got.Dispatched)
	}
	if len(got.Rejected) != 1 {
		t.Fatalf("expected rejection, got %+v", got)
	}
	reason, _ := got.Rejected[0]["reason"].(string)
	if !strings.Contains(reason, "path segments are not allowed") {
		t.Fatalf("rejection reason = %q", reason)
	}
	if call := fake.lastCall(); call != nil {
		t.Fatalf("spawner should not be called, got %#v", call)
	}
}

func TestEvent_EphemeralDispatchPreparesRuntimeState(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureAgent(t, teamDir, "worker")
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`
[linear]
team_id = "repo-team"
ticket_prefix = "BASE"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	top, err := topology.Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true
replicas = 1

[instances.worker.config.linear]
ticket_prefix = "WORK"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}

	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Dispatched) != 1 {
		t.Fatalf("expected 1 dispatched, got %+v", got)
	}
	id, _ := got.Dispatched[0]["instance_id"].(string)
	stateDir := filepath.Join(teamDir, "state", id)
	configBody, err := os.ReadFile(filepath.Join(stateDir, "config.toml"))
	if err != nil {
		t.Fatalf("read state config: %v", err)
	}
	for _, want := range []string{`team_id = "repo-team"`, `ticket_prefix = "WORK"`} {
		if !strings.Contains(string(configBody), want) {
			t.Fatalf("state config missing %q:\n%s", want, string(configBody))
		}
	}
	env := fake.lastEnv()
	for _, want := range []string{
		"AGENT_TEAM_ROOT=" + teamDir,
		"AGENT_TEAM_INSTANCE=" + id,
		"AGENT_TEAM_STATE_DIR=" + stateDir,
		"AGENT_TEAM_DAEMON_SOCKET=" + SocketPath(teamDir),
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q in %v", want, env)
		}
	}
	if meta, err := ReadMetadata(root, id); err != nil || meta.Workspace != repoRoot {
		t.Fatalf("metadata workspace = %+v err=%v, want repo root %s", meta, err, repoRoot)
	}
	_, _ = m.Stop(id)
	_ = m.WaitForReaper(id, 5*time.Second)
}

func TestEvent_EphemeralReapPreservesMetadataAndState(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Dispatched) != 1 {
		t.Fatalf("expected 1 dispatched, got %+v", got)
	}
	id, _ := got.Dispatched[0]["instance_id"].(string)
	if id == "" {
		t.Fatalf("missing dispatched instance id: %+v", got)
	}
	stateDir := filepath.Join(teamDir, "state", id)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ch := m.reapedChan(id)
	if ch == nil {
		t.Fatalf("instance %s has no reaper channel", id)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("reaper for %s did not finish", id)
	}

	meta, err := ReadMetadata(root, id)
	if err != nil {
		t.Fatalf("metadata for %s should be preserved: %v", id, err)
	}
	if meta.Status != StatusExited {
		t.Fatalf("metadata status = %q, want %q", meta.Status, StatusExited)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("state dir for %s should be preserved: %v", id, err)
	}
	running, queued := resolver.QueueDepth("worker")
	if running != 0 || queued != 0 {
		t.Fatalf("queue depth after reap = running:%d queued:%d, want zero", running, queued)
	}
}

func TestEvent_EphemeralReplicasQueueing(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, fixtureTeamDir(t), mustParseTopo(t))
	resolver.SetQueueCap(2) // small cap so we can hit it deterministically.
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	post := func(label string) (string, []map[string]any, []string) {
		resp := mustPost(t, srv.URL+"/v1/event",
			`{"type":"agent.dispatch","payload":{"target":"worker"}}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", label, resp.StatusCode, readBody(t, resp))
		}
		var got struct {
			Matched    []string         `json:"matched"`
			Dispatched []map[string]any `json:"dispatched"`
			Queued     []string         `json:"queued"`
			Rejected   []map[string]any `json:"rejected"`
		}
		json.NewDecoder(resp.Body).Decode(&got)
		var rej string
		if len(got.Rejected) > 0 {
			rej, _ = got.Rejected[0]["reason"].(string)
		}
		return rej, got.Dispatched, got.Queued
	}

	// Replicas=2; cap=2; so 4 events fit (2 dispatched + 2 queued); 5th rejected.
	for i := 0; i < 4; i++ {
		_, _, _ = post("post#" + string(rune('A'+i)))
	}
	running, queued := resolver.QueueDepth("worker")
	if running != 2 || queued != 2 {
		t.Errorf("after 4: running=%d queued=%d", running, queued)
	}

	rej, _, _ := post("post#5")
	if rej == "" {
		t.Errorf("5th event should have been rejected, was not")
	}
}

func TestEvent_DispatchLockContentionQueuesAndReapDrains(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
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
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	first, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-squ-100",
		"ticket": "SQU-100",
	})
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if len(first.Outcomes) != 1 || first.Outcomes[0].Action != "dispatched" || first.Outcomes[0].InstanceID != "worker-squ-100" {
		t.Fatalf("first outcomes = %+v", first.Outcomes)
	}
	second, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-squ-101",
		"ticket": "SQU-101",
	})
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if len(second.Outcomes) != 1 || second.Outcomes[0].Action != "queued" || second.Outcomes[0].Reason != QueueReasonLockHeld {
		t.Fatalf("second outcomes = %+v, want lock-held queue", second.Outcomes)
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 1 || items[0].Reason != QueueReasonLockHeld || !reflect.DeepEqual(items[0].Locks, []string{"build"}) {
		t.Fatalf("queue items = %+v", items)
	}
	snapshots := resolver.LockSnapshots()
	if len(snapshots) != 1 || snapshots[0].Name != "build" || snapshots[0].Used != 1 || snapshots[0].Holders[0].Instance != "worker-squ-100" {
		t.Fatalf("snapshots = %+v", snapshots)
	}

	if _, err := m.Stop("worker-squ-100"); err != nil {
		t.Fatalf("stop first: %v", err)
	}
	if err := m.WaitForReaper("worker-squ-100", 5*time.Second); err != nil {
		t.Fatalf("wait first reap: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls=%d, want queued dispatch to drain", fake.callCount())
	}
	if _, err := ReadQueueItem(root, items[0].ID); !os.IsNotExist(err) {
		t.Fatalf("queued item should be removed after lock release, err=%v", err)
	}
	snapshots = resolver.LockSnapshots()
	if len(snapshots) != 1 || snapshots[0].Used != 1 || snapshots[0].Holders[0].Instance != "worker-squ-101" {
		t.Fatalf("snapshots after drain = %+v", snapshots)
	}
	_, _ = m.Stop("worker-squ-101")
	_ = m.WaitForReaper("worker-squ-101", 5*time.Second)
}

func TestEvent_LockReleaseDrainsCrossInstanceWaiters(t *testing.T) {
	// SQU-76: lock_held waiters queued under a DIFFERENT declared instance
	// than the lock holder must dispatch when the lock frees — the reap-time
	// same-instance queue pop cannot reach them.
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "reviewer")
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

[instances.reviewer]
agent = "reviewer"
ephemeral = true
replicas = 2
locks = ["build"]

[[instances.reviewer.triggers]]
event = "agent.dispatch"
match.target = "reviewer"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	first, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-squ-200",
		"ticket": "SQU-200",
	})
	if err != nil {
		t.Fatalf("worker dispatch: %v", err)
	}
	if len(first.Outcomes) != 1 || first.Outcomes[0].Action != "dispatched" {
		t.Fatalf("worker outcomes = %+v", first.Outcomes)
	}
	second, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "reviewer",
		"name":   "reviewer-squ-201",
		"ticket": "SQU-201",
	})
	if err != nil {
		t.Fatalf("reviewer dispatch: %v", err)
	}
	if len(second.Outcomes) != 1 || second.Outcomes[0].Action != "queued" || second.Outcomes[0].Reason != QueueReasonLockHeld {
		t.Fatalf("reviewer outcomes = %+v, want lock-held queue", second.Outcomes)
	}

	if _, err := m.Stop("worker-squ-200"); err != nil {
		t.Fatalf("stop worker: %v", err)
	}
	if err := m.WaitForReaper("worker-squ-200", 5*time.Second); err != nil {
		t.Fatalf("wait worker reap: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for fake.callCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls=%d, want cross-instance lock waiter dispatched on release", fake.callCount())
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("queue should be empty after cross-instance drain, items=%+v", items)
	}
	snapshots := resolver.LockSnapshots()
	if len(snapshots) != 1 || snapshots[0].Used != 1 || snapshots[0].Holders[0].Instance != "reviewer-squ-201" {
		t.Fatalf("snapshots after drain = %+v", snapshots)
	}
	_, _ = m.Stop("reviewer-squ-201")
	_ = m.WaitForReaper("reviewer-squ-201", 5*time.Second)
}

func TestEvent_LockRecoveryDropsDeadLedgerRows(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[locks.build]
slots = 1

[instances.worker]
agent = "worker"
ephemeral = true
locks = ["build"]
`)
	now := time.Now().UTC()
	if err := WriteLockLease(root, &LockLease{
		Lock:       "build",
		Instance:   "worker-live",
		AcquiredAt: now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("write live lease: %v", err)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance:  "worker-live",
		Agent:     "worker",
		PID:       os.Getpid(),
		Status:    StatusRunning,
		StartedAt: now,
	}); err != nil {
		t.Fatalf("write live metadata: %v", err)
	}
	if err := WriteLockLease(root, &LockLease{
		Lock:       "build",
		Instance:   "worker-dead",
		PID:        999_999_999,
		AcquiredAt: now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("write dead lease: %v", err)
	}

	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	snapshots := resolver.LockSnapshots()
	if len(snapshots) != 1 || snapshots[0].Used != 1 || snapshots[0].Holders[0].Instance != "worker-live" || snapshots[0].Holders[0].PID != os.Getpid() {
		t.Fatalf("snapshots = %+v, want only recovered live holder", snapshots)
	}
	if _, err := ReadLockLease(root, "build", "worker-dead"); !os.IsNotExist(err) {
		t.Fatalf("dead lease should be removed, err=%v", err)
	}
}

func TestEvent_PersistedQueueRecoveryDrainsReadyItem(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Now().UTC()
	item := &QueueItem{
		ID:         "queued-1",
		State:      QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-77",
		Payload:    map[string]any{"target": "worker", "name": "worker-squ-77", "ticket": "SQU-77"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := WriteQueueItem(root, item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	running, queued := resolver.QueueDepth("worker")
	if running != 0 || queued != 1 {
		t.Fatalf("initial depth running=%d queued=%d, want 0/1", running, queued)
	}
	resolver.RecoverQueueState()
	running, queued = resolver.QueueDepth("worker")
	if running != 1 || queued != 0 {
		t.Fatalf("after recover depth running=%d queued=%d, want 1/0", running, queued)
	}
	if _, err := ReadQueueItem(root, "queued-1"); !os.IsNotExist(err) {
		t.Fatalf("queue item should be removed after dispatch, err=%v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1", fake.callCount())
	}
}

func TestEvent_DrainQueuesWithResultReportsOutcomes(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Now().UTC()
	item := &QueueItem{
		ID:         "queued-drain",
		State:      QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-78",
		Payload:    map[string]any{"target": "worker", "name": "worker-squ-78", "ticket": "SQU-78"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := WriteQueueItem(root, item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	result, err := resolver.DrainQueuesWithResult()
	if err != nil {
		t.Fatalf("DrainQueuesWithResult: %v", err)
	}
	if result.Attempted != 1 || result.Dispatched != 1 || result.Rejected != 0 || result.Pending != 0 || result.Dead != 0 {
		t.Fatalf("drain result = %+v", result)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" || result.Outcomes[0].InstanceID != "worker-squ-78" {
		t.Fatalf("outcomes = %+v", result.Outcomes)
	}
	if _, err := ReadQueueItem(root, "queued-drain"); !os.IsNotExist(err) {
		t.Fatalf("queue item should be removed after drain, err=%v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1", fake.callCount())
	}
}

func TestEvent_DrainQueuesArmsQueuedTimeoutBudget(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Now().UTC()
	item := &QueueItem{
		ID:         "queued-timeout",
		State:      QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-queued-timeout",
		Payload: map[string]any{
			"target":    "worker",
			"name":      "worker-queued-timeout",
			"ticket":    "SQU-502",
			"workspace": "repo",
			"timeout":   "50ms",
		},
		QueuedAt:  now,
		UpdatedAt: now,
	}
	if err := WriteQueueItem(root, item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.DrainQueuesWithResult()
	if err != nil {
		t.Fatalf("DrainQueuesWithResult: %v", err)
	}
	if result.Dispatched != 1 || len(result.Outcomes) != 1 || result.Outcomes[0].InstanceID != "worker-queued-timeout" {
		t.Fatalf("drain result = %+v, want dispatched queued timeout", result)
	}
	if err := m.WaitForReaper("worker-queued-timeout", 8*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	meta, err := ReadMetadata(root, "worker-queued-timeout")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.RuntimeBudget != "50ms" || meta.RuntimeDeadline.IsZero() || meta.Status != StatusCrashed {
		t.Fatalf("metadata = %+v, want crashed with 50ms budget", meta)
	}
}

func TestEvent_DrainQueuesWithResultForIDsSkipsUnselectedItems(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Now().UTC()
	for _, item := range []*QueueItem{
		{
			ID:         "queued-keep",
			State:      QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-keep",
			Payload:    map[string]any{"target": "worker", "name": "worker-keep", "ticket": "SQU-KEEP"},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now.Add(-time.Minute),
		},
		{
			ID:         "queued-selected",
			State:      QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-selected",
			Payload:    map[string]any{"target": "worker", "name": "worker-selected", "ticket": "SQU-SEL"},
			QueuedAt:   now,
			UpdatedAt:  now,
		},
	} {
		if err := WriteQueueItem(root, item); err != nil {
			t.Fatalf("WriteQueueItem %s: %v", item.ID, err)
		}
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	preview, err := resolver.PreviewDrainQueuesWithResultForIDs([]string{"queued-selected"})
	if err != nil {
		t.Fatalf("PreviewDrainQueuesWithResultForIDs: %v", err)
	}
	if !preview.DryRun || preview.WouldDispatch != 1 || preview.Pending != 1 || len(preview.Outcomes) != 1 || preview.Outcomes[0].InstanceID != "worker-selected" {
		t.Fatalf("preview = %+v", preview)
	}
	if fake.callCount() != 0 {
		t.Fatalf("preview spawned %d processes", fake.callCount())
	}

	result, err := resolver.DrainQueuesWithResultForIDs([]string{"queued-selected"})
	if err != nil {
		t.Fatalf("DrainQueuesWithResultForIDs: %v", err)
	}
	if result.Attempted != 1 || result.Dispatched != 1 || result.Pending != 0 || len(result.Outcomes) != 1 || result.Outcomes[0].InstanceID != "worker-selected" {
		t.Fatalf("drain result = %+v", result)
	}
	if _, err := ReadQueueItem(root, "queued-selected"); !os.IsNotExist(err) {
		t.Fatalf("selected queue item should be removed, err=%v", err)
	}
	if _, err := ReadQueueItem(root, "queued-keep"); err != nil {
		t.Fatalf("unselected queue item changed: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1", fake.callCount())
	}
}

func TestEvent_PreviewDrainQueuesDoesNotDispatch(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Now().UTC()
	item := &QueueItem{
		ID:         "queued-preview",
		State:      QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-79",
		Payload:    map[string]any{"target": "worker", "name": "worker-squ-79", "ticket": "SQU-79"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := WriteQueueItem(root, item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	result, err := resolver.PreviewDrainQueuesWithResult()
	if err != nil {
		t.Fatalf("PreviewDrainQueuesWithResult: %v", err)
	}
	if !result.DryRun || result.WouldDispatch != 1 || result.Dispatched != 0 || result.Pending != 1 {
		t.Fatalf("preview result = %+v", result)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "would_dispatch" || result.Outcomes[0].InstanceID != "worker-squ-79" {
		t.Fatalf("preview outcomes = %+v", result.Outcomes)
	}
	if _, err := ReadQueueItem(root, "queued-preview"); err != nil {
		t.Fatalf("preview removed queue item: %v", err)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls=%d, want 0", fake.callCount())
	}
}

func TestEvent_DrainOutboxWithResultPublishesPendingEvents(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	item := &OutboxItem{
		ID:        "outbox-squ-401",
		State:     OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"target": "worker", "name": "worker-squ-401", "ticket": "SQU-401", "kickoff": "test outbox", "workspace": "repo"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := WriteOutboxItem(teamDir, item); err != nil {
		t.Fatalf("WriteOutboxItem: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	preview, err := resolver.DrainOutboxWithResult(true)
	if err != nil {
		t.Fatalf("DrainOutboxWithResult dry-run: %v", err)
	}
	if preview.WouldPublish != 1 || preview.Pending != 1 {
		t.Fatalf("preview = %+v, want would_publish=1 pending=1", preview)
	}
	if fake.callCount() != 0 {
		t.Fatalf("dry-run spawned %d processes", fake.callCount())
	}

	result, err := resolver.DrainOutboxWithResult(false)
	if err != nil {
		t.Fatalf("DrainOutboxWithResult: %v", err)
	}
	if result.Attempted != 1 || result.Published != 1 || result.Rejected != 0 {
		t.Fatalf("result = %+v, want attempted=1 published=1 rejected=0", result)
	}
	if result.Pending != 0 || result.Processed != 1 || result.Failed != 0 {
		t.Fatalf("state counts = pending %d processed %d failed %d, want 0/1/0", result.Pending, result.Processed, result.Failed)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1", fake.callCount())
	}
	if _, err := os.Stat(OutboxPath(teamDir, OutboxStatePending, item.ID)); !os.IsNotExist(err) {
		t.Fatalf("pending outbox path still exists or stat failed unexpectedly: %v", err)
	}
	processed, err := ReadOutboxItem(teamDir, item.ID)
	if err != nil {
		t.Fatalf("ReadOutboxItem: %v", err)
	}
	if processed.State != OutboxStateProcessed || processed.ProcessedAt.IsZero() {
		t.Fatalf("processed item = %+v, want processed state with timestamp", processed)
	}
}

func TestEvent_PipelineCreatesJobAndDispatchesFirstStep(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
instructions = "Implement the ticket with regression coverage."
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
label = "Manager review"
description = "Review the worker output."
instructions = "Prepare review notes for the implementation branch."
target = "manager"
after = ["implement"]
optional = true
timeout = "2h"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-92","ticket_url":"https://linear.app/squirtlesquad/issue/SQU-92/pipeline","kickoff":"implement SQU-92","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
		Rejected   []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Rejected) != 0 || len(got.Dispatched) != 1 {
		t.Fatalf("response = %+v", got)
	}
	if id, _ := got.Dispatched[0]["instance_id"].(string); id != "worker-squ-92" {
		t.Fatalf("instance_id = %q, want worker-squ-92", id)
	}
	j, err := jobstore.Read(teamDir, "squ-92")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Pipeline != "ticket_to_pr" || j.Status != jobstore.StatusRunning || len(j.Steps) != 2 || j.TicketURL != "https://linear.app/squirtlesquad/issue/SQU-92/pipeline" {
		t.Fatalf("job = %+v", j)
	}
	if j.Steps[0].ID != "implement" || j.Steps[0].Instructions != "Implement the ticket with regression coverage." || j.Steps[0].Status != jobstore.StatusRunning || j.Steps[0].Instance != "worker-squ-92" {
		t.Fatalf("first step = %+v", j.Steps[0])
	}
	if j.Steps[1].ID != "review" || j.Steps[1].Label != "Manager review" || j.Steps[1].Description != "Review the worker output." || j.Steps[1].Instructions != "Prepare review notes for the implementation branch." || !j.Steps[1].Optional || j.Steps[1].Timeout != "2h0m0s" {
		t.Fatalf("optional review step = %+v", j.Steps[1])
	}
}

func TestEvent_PipelineInitialDispatchRejectionWritesLinearFailureAttention(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_USER_API_KEY", "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`
[team]
pm_tool = "linear"

[linear]
team_id = "team-1"
ticket_prefix = "SQU"
attention_state = "Todo"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	top, err := topology.Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult("ticket.created", map[string]any{
		"ticket":  "SQU-95",
		"kickoff": "implement SQU-95",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "rejected" || !strings.Contains(result.Outcomes[0].Reason, "no agent.dispatch trigger") {
		t.Fatalf("outcomes = %+v, want rejected no-match dispatch", result.Outcomes)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls=%d, want no instance start", fake.callCount())
	}
	j, err := jobstore.Read(teamDir, "squ-95")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Status != jobstore.StatusFailed || j.Steps[0].Status != jobstore.StatusFailed {
		t.Fatalf("job = %+v, want failed initial step", j)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-95")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "linear_writeback_skipped" &&
			ev.Message == "no Linear API key found" &&
			ev.Data["action"] == string(linearwriteback.ActionFailureAttention) &&
			ev.Data["state"] == "Todo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events missing failure-attention write-back attempt: %+v", events)
	}
}

func TestEvent_PipelineStepDispatchDeliversUnreadMailboxInKickoff(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	msg := &Message{ID: "pipeline-mail-1", From: "manager", Body: "pipeline mail"}
	if err := AppendMessage(root, "worker-squ-92", msg); err != nil {
		t.Fatalf("append mail: %v", err)
	}
	result, err := resolver.EventWithResult("ticket.created", map[string]any{
		"ticket":    "SQU-92",
		"kickoff":   "implement SQU-92",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" || result.Outcomes[0].InstanceID != "worker-squ-92" {
		t.Fatalf("outcomes = %+v, want dispatched worker-squ-92", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-squ-92")
		_ = m.WaitForReaper("worker-squ-92", 5*time.Second)
	})

	prompt, ok := argValue(fake.lastCall(), "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", fake.lastCall())
	}
	if !strings.Contains(prompt, kickoffMailboxHeading) || !strings.Contains(prompt, "pipeline mail") {
		t.Fatalf("pipeline prompt missing mailbox delivery:\n%s", prompt)
	}
	unread, err := ReadUnacked(root, "worker-squ-92")
	if err != nil {
		t.Fatalf("ReadUnacked: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread after pipeline dispatch = %+v, want none", unread)
	}
	jobEvents, err := jobstore.ListEvents(teamDir, "squ-92")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, ev := range jobEvents {
		if ev.Type == "kickoff_mail_delivered" && ev.Instance == "worker-squ-92" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("job events missing kickoff_mail_delivered: %+v", jobEvents)
	}
}

func TestEvent_PipelineStepTimeoutArmsWatchdog(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
timeout = "500ms"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult("ticket.created", map[string]any{
		"ticket":    "SQU-501",
		"kickoff":   "implement SQU-501",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" || result.Outcomes[0].InstanceID != "worker-squ-501" {
		t.Fatalf("outcomes = %+v, want dispatched worker-squ-501", result.Outcomes)
	}
	if err := m.WaitForReaper("worker-squ-501", 8*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	meta, err := ReadMetadata(root, "worker-squ-501")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.RuntimeBudget != "500ms" || meta.RuntimeDeadline.IsZero() {
		t.Fatalf("metadata budget = %+v, want 500ms with deadline", meta)
	}
	if meta.Status != StatusCrashed {
		t.Fatalf("metadata status = %s, want crashed", meta.Status)
	}
	j, err := jobstore.Read(teamDir, "squ-501")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if len(j.Steps) != 1 || j.Steps[0].Status != jobstore.StatusFailed {
		t.Fatalf("job steps after watchdog = %+v, want failed implement", j.Steps)
	}
}

func TestEvent_PipelineInitialManualGateBlocksWithoutDispatch(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "approval"
target = "worker"
gate = "manual"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-94","kickoff":"wait for approval"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
		Queued     []string         `json:"queued"`
		Blocked    []map[string]any `json:"blocked"`
		Rejected   []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Dispatched) != 0 || len(got.Queued) != 0 || len(got.Rejected) != 0 || len(got.Blocked) != 1 {
		t.Fatalf("response = %+v, want one blocked pipeline outcome", got)
	}
	if got.Blocked[0]["instance"] != "pipeline:ticket_to_pr" || !strings.Contains(fmt.Sprint(got.Blocked[0]["reason"]), "manual approval") {
		t.Fatalf("blocked outcome = %+v", got.Blocked[0])
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls=%d, want 0", fake.callCount())
	}
	j, err := jobstore.Read(teamDir, "squ-94")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Pipeline != "ticket_to_pr" || len(j.Steps) != 1 || j.Steps[0].Status != jobstore.StatusBlocked || j.Steps[0].Gate != jobstore.StepGateManual || !j.Steps[0].StartedAt.IsZero() {
		t.Fatalf("job = %+v", j)
	}
}

func TestEvent_PipelineQueuesStalePersistentTarget(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "agent.dispatch"
match.target = "manager"

[pipelines.ticket_review]
trigger.event = "ticket.created"

[[pipelines.ticket_review.steps]]
id = "review"
target = "manager"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    StatusRunning,
		PID:       999_999_999,
		Workspace: t.TempDir(),
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write stale metadata: %v", err)
	}
	m := NewInstanceManager(root, nil)
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-93","kickoff":"review SQU-93"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Queued   []string         `json:"queued"`
		Messaged []string         `json:"messaged"`
		Rejected []map[string]any `json:"rejected"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Rejected) != 0 || len(got.Messaged) != 0 || len(got.Queued) != 1 || got.Queued[0] != "manager" {
		t.Fatalf("response = %+v, want queued stale manager", got)
	}
	j, err := jobstore.Read(teamDir, "squ-93")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Status != jobstore.StatusQueued || len(j.Steps) != 1 || j.Steps[0].Status != jobstore.StatusQueued || j.Steps[0].Instance != "manager" {
		t.Fatalf("job = %+v, want queued review step for manager", j)
	}
	meta, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatalf("read manager metadata: %v", err)
	}
	if meta.Status != StatusExited {
		t.Fatalf("manager status = %s, want reconciled exited", meta.Status)
	}
}

func TestEvent_SchedulePublishesDueEvent(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "schedule"
match.name = "nightly"

[schedules.nightly]
every = "1s"
payload.workspace = "repo"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	state := map[string]*ScheduleState{}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	if fired := resolver.fireDueSchedules(now, state); len(fired) != 0 {
		t.Fatalf("first tick fired = %v, want none without run_on_start", fired)
	}
	persisted, err := ReadScheduleState(root, "nightly")
	if err != nil {
		t.Fatalf("ReadScheduleState after first tick: %v", err)
	}
	if !persisted.LastSeenAt.Equal(now) || !persisted.LastFiredAt.IsZero() {
		t.Fatalf("first persisted state = %+v", persisted)
	}
	if fired := resolver.fireDueSchedules(now.Add(500*time.Millisecond), state); len(fired) != 0 {
		t.Fatalf("early tick fired = %v, want none", fired)
	}
	due := now.Add(time.Second)
	if fired := resolver.fireDueSchedules(due, state); len(fired) != 1 || fired[0] != "nightly" {
		t.Fatalf("due tick fired = %v, want nightly", fired)
	}
	persisted, err = ReadScheduleState(root, "nightly")
	if err != nil {
		t.Fatalf("ReadScheduleState after due tick: %v", err)
	}
	if !persisted.LastSeenAt.Equal(due) || !persisted.LastFiredAt.Equal(due) {
		t.Fatalf("due persisted state = %+v", persisted)
	}
	loaded := resolver.loadScheduleStates()
	if loaded["nightly"] == nil || !loaded["nightly"].LastSeenAt.Equal(due) {
		t.Fatalf("loaded schedule states = %+v", loaded)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"event":"schedule"`) || !strings.Contains(messages[0].Body, `"name":"nightly"`) {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestEvent_ScheduleFireResultPreviewsAndPublishesRunOnStart(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "schedule"
match.name = "nightly"

[schedules.nightly]
every = "1s"
run_on_start = true
payload.workspace = "repo"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	preview, err := resolver.PreviewDueSchedulesWithResult(now)
	if err != nil {
		t.Fatalf("PreviewDueSchedulesWithResult: %v", err)
	}
	if !preview.DryRun || preview.WouldFire != 1 || preview.Fired != 0 || len(preview.Schedules) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if item := preview.Schedules[0]; item.Name != "nightly" || item.Reason != "run_on_start" || item.EventType != "schedule" || item.Payload["workspace"] != "repo" {
		t.Fatalf("preview item = %+v", item)
	}
	if _, err := ReadScheduleState(root, "nightly"); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write schedule state, err=%v", err)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read dry-run messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("dry-run published messages = %+v", messages)
	}

	fired, err := resolver.FireDueSchedulesWithResult(now)
	if err != nil {
		t.Fatalf("FireDueSchedulesWithResult: %v", err)
	}
	if fired.DryRun || fired.Fired != 1 || fired.WouldFire != 0 || len(fired.Schedules) != 1 {
		t.Fatalf("fired = %+v", fired)
	}
	if got := fired.Schedules[0]; got.Name != "nightly" || got.Reason != "run_on_start" || len(got.Outcomes) != 1 || got.Outcomes[0].Action != "messaged" || got.Outcomes[0].Instance != "manager" {
		t.Fatalf("fired item = %+v", got)
	}
	persisted, err := ReadScheduleState(root, "nightly")
	if err != nil {
		t.Fatalf("ReadScheduleState after fire: %v", err)
	}
	if !persisted.LastSeenAt.Equal(now) || !persisted.LastFiredAt.Equal(now) {
		t.Fatalf("persisted = %+v", persisted)
	}
	messages, err = ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"event":"schedule"`) || !strings.Contains(messages[0].Body, `"name":"nightly"`) {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestEvent_ScheduleFireResultForNamesScopesStateAndMessages(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "schedule"

[schedules.delivery_due]
every = "1s"
run_on_start = true
payload.team = "delivery"

[schedules.platform_due]
every = "1s"
run_on_start = true
payload.team = "platform"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	preview, err := resolver.PreviewDueSchedulesWithResultForNames(now, []string{"delivery_due"})
	if err != nil {
		t.Fatalf("PreviewDueSchedulesWithResultForNames: %v", err)
	}
	if !preview.DryRun || preview.WouldFire != 1 || len(preview.Schedules) != 1 || preview.Schedules[0].Name != "delivery_due" {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := ReadScheduleState(root, "delivery_due"); !os.IsNotExist(err) {
		t.Fatalf("preview wrote delivery state, err=%v", err)
	}

	fired, err := resolver.FireDueSchedulesWithResultForNames(now, []string{"delivery_due"})
	if err != nil {
		t.Fatalf("FireDueSchedulesWithResultForNames: %v", err)
	}
	if fired.DryRun || fired.Fired != 1 || len(fired.Schedules) != 1 || fired.Schedules[0].Name != "delivery_due" {
		t.Fatalf("fired = %+v", fired)
	}
	if _, err := ReadScheduleState(root, "delivery_due"); err != nil {
		t.Fatalf("delivery schedule state missing: %v", err)
	}
	if _, err := ReadScheduleState(root, "platform_due"); !os.IsNotExist(err) {
		t.Fatalf("platform schedule state changed, err=%v", err)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"name":"delivery_due"`) || strings.Contains(messages[0].Body, "platform_due") {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestIntakeLinearRouteNormalizesAndDispatches(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	top, err := topology.Parse([]byte(`
[instances.manager]
agent = "manager"

[pipelines.ticket_triage]
trigger.event = "ticket.created"

[[pipelines.ticket_triage.steps]]
id = "triage"
target = "manager"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/intake/linear",
		`{"action":"Issue created","data":{"identifier":"SQU-93","title":"route intake"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("intake: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Event    map[string]any `json:"event"`
		Queued   []string       `json:"queued"`
		Messaged []string       `json:"messaged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Event["type"] != "ticket.created" || len(got.Queued) != 1 || got.Queued[0] != "manager" || len(got.Messaged) != 0 {
		t.Fatalf("response = %+v", got)
	}
	if _, err := jobstore.Read(teamDir, "squ-93"); err != nil {
		t.Fatalf("job not created: %v", err)
	}
}

func TestIntakeGitHubMergedReconcilesJob(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Now().UTC()
	j, err := jobstore.New("SQU-94", "worker", "finish webhook reconciliation", now)
	if err != nil {
		t.Fatalf("New job: %v", err)
	}
	j.Status = jobstore.StatusRunning
	j.PR = "https://github.com/acme/repo/pull/94"
	j.Branch = "worktree-worker-squ-94"
	j.Worktree = filepath.Join(filepath.Dir(teamDir), ".claude", "worktrees", "worker-squ-94")
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("Write job: %v", err)
	}
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/intake/github", `{
		"action":"closed",
		"repository":{"full_name":"acme/repo"},
		"pull_request":{
			"number":94,
			"merged":true,
			"html_url":"https://github.com/acme/repo/pull/94",
			"head":{"ref":"worktree-worker-squ-94"}
		}
	}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("intake: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Event     map[string]any            `json:"event"`
		Reconcile *jobstore.ReconcileResult `json:"reconcile"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Event["type"] != "pr.merged" {
		t.Fatalf("event = %+v", got.Event)
	}
	if got.Reconcile == nil || got.Reconcile.Job == nil || got.Reconcile.Job.ID != "squ-94" || got.Reconcile.MatchedBy != "pr_url" {
		t.Fatalf("reconcile response = %+v", got.Reconcile)
	}
	updated, err := jobstore.Read(teamDir, "squ-94")
	if err != nil {
		t.Fatalf("Read updated job: %v", err)
	}
	if updated.Status != jobstore.StatusDone || updated.LastEvent != "pr.merged" || updated.LastStatus != "pull request merged" {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.Worktree == "" || updated.Branch == "" {
		t.Fatalf("daemon reconcile should not cleanup worktree, job = %+v", updated)
	}
}

func TestEvent_QueuedSpawnFailureMovesToDeadLetter(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	now := time.Now().UTC()
	item := &QueueItem{
		ID:         "queued-dead",
		State:      QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-78",
		Payload:    map[string]any{"target": "worker", "name": "worker-squ-78", "ticket": "SQU-78"},
		Attempts:   MaxQueueAttempts - 1,
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := WriteQueueItem(root, item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	m := NewInstanceManager(root, func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		return nil, os.ErrPermission
	})
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	resolver.RecoverQueueState()
	dead, err := ReadQueueItem(root, "queued-dead")
	if err != nil {
		t.Fatalf("ReadQueueItem: %v", err)
	}
	if dead.State != QueueStateDead || dead.Attempts != MaxQueueAttempts || dead.LastError == "" {
		t.Fatalf("dead = %+v, want dead-letter with failure", dead)
	}
}

func TestEvent_EmptyPayloadValidation(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, root, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 on missing type, got %d", resp.StatusCode)
	}
}

func TestTopology_GetAndReload(t *testing.T) {
	teamDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(fixtureTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewInstanceManager(t.TempDir(), nil)
	top, _ := topology.LoadFromTeamDir(teamDir)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustGet(t, srv.URL+"/v1/topology")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topology get: %d", resp.StatusCode)
	}
	var got struct {
		Instances []map[string]any `json:"instances"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Instances) != 2 {
		t.Errorf("instances: %v", got.Instances)
	}

	// Edit the file → reload.
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.solo]
agent = "manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	resp = mustPost(t, srv.URL+"/v1/topology/reload", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topology reload: %d %s", resp.StatusCode, readBody(t, resp))
	}
	resp = mustGet(t, srv.URL+"/v1/topology")
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Instances) != 1 || got.Instances[0]["name"] != "solo" {
		t.Errorf("after reload: %v", got.Instances)
	}
}

func TestTopology_NoEventsConfigured(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), nil)
	srv := httptest.NewServer(Handler(m, nil, nil, ""))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event", `{"type":"user_invocation"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}

	// /v1/topology returns empty instances list, not 503 — the read path is
	// always-on so clients can render an empty state.
	resp = mustGet(t, srv.URL+"/v1/topology")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on empty topology, got %d", resp.StatusCode)
	}
}

func traceEntryByScope(t *testing.T, trace topology.EventTrace, scope string) topology.EventTraceEntry {
	t.Helper()
	for _, entry := range trace.Entries {
		if entry.Scope == scope {
			return entry
		}
	}
	t.Fatalf("trace entry %q missing: %+v", scope, trace.Entries)
	return topology.EventTraceEntry{}
}
