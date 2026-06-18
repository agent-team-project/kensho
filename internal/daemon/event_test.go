package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/topology"
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

func fixtureTeamDir(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")
	return teamDir
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
	if _, err := os.Stat(filepath.Join(meta.Workspace, "README.md")); err != nil {
		t.Fatalf("worktree missing README: %v", err)
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

func TestEvent_EphemeralReapCleansMetadataAndState(t *testing.T) {
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

	if _, err := ReadMetadata(root, id); !os.IsNotExist(err) {
		t.Fatalf("metadata for %s should be removed, err=%v", id, err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir for %s should be removed, err=%v", id, err)
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
	m := NewInstanceManager(root, func(args []string, env []string, workspace, stdoutPath, stderrPath string) (*os.Process, error) {
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
