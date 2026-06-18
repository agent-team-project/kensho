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

	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
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

func TestEvent_EphemeralDispatchUsesCodexRuntime(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
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
	if !strings.Contains(call[len(call)-1], "implement SQU-42") {
		t.Fatalf("codex prompt missing kickoff: %s", call[len(call)-1])
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

func TestEvent_TicketDispatchCreatesJobAndExportsContext(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-95","ticket":"SQU-95","kickoff":"implement SQU-95","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	j, err := jobstore.Read(teamDir, "squ-95")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Status != jobstore.StatusRunning || j.Instance != "worker-squ-95" || j.Target != "worker" || j.Kickoff != "implement SQU-95" {
		t.Fatalf("job = %+v", j)
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
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
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
		`{"type":"ticket.created","payload":{"ticket":"SQU-92","kickoff":"implement SQU-92","workspace":"repo"}}`)
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
	if j.Pipeline != "ticket_to_pr" || j.Status != jobstore.StatusRunning || len(j.Steps) != 2 {
		t.Fatalf("job = %+v", j)
	}
	if j.Steps[0].ID != "implement" || j.Steps[0].Status != jobstore.StatusRunning || j.Steps[0].Instance != "worker-squ-92" {
		t.Fatalf("first step = %+v", j.Steps[0])
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
		Messaged []string       `json:"messaged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Event["type"] != "ticket.created" || len(got.Messaged) != 1 || got.Messaged[0] != "manager" {
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
