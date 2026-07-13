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
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/budget"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/pmprovider"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/usage"
	"github.com/agent-team-project/agent-team/internal/worktreecleanup"
	"github.com/agent-team-project/agent-team/internal/worktreepolicy"
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

func writeFixtureRuntimeCommandSkills(t *testing.T, teamDir, agent string) {
	t.Helper()
	for _, item := range []struct {
		skill  string
		script string
	}{
		{skill: "inbox", script: "inbox.sh"},
		{skill: "channel", script: "channel.sh"},
	} {
		dir := filepath.Join(teamDir, "skills", item.skill, "scripts")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(teamDir, "skills", item.skill, "SKILL.md"), []byte("---\nname: "+item.skill+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, item.script), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(teamDir, "agents", agent, "config.toml")
	if err := os.WriteFile(cfg, []byte("[skills]\nextra = [\"inbox\", \"channel\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertEventRuntimeCommandSurface(t *testing.T, runtimeDir string, env []string) {
	t.Helper()
	wantBin := filepath.Join(runtimeDir, "bin")
	path := lastEnvValue(env, "PATH")
	if path == "" {
		t.Fatalf("runtime env missing PATH: %v", env)
	}
	if got := strings.Split(path, string(os.PathListSeparator))[0]; got != wantBin {
		t.Fatalf("PATH first entry = %q, want runtime shim bin %q; PATH=%q", got, wantBin, path)
	}
	for _, name := range []string{"channel.sh", "inbox"} {
		if st, err := os.Stat(filepath.Join(wantBin, name)); err != nil {
			t.Fatalf("runtime shim %s missing: %v", name, err)
		} else if st.Mode().Perm()&0o111 == 0 {
			t.Fatalf("runtime shim %s is not executable: mode=%s", name, st.Mode())
		}
	}
}

func assertLaunchRootUnderRuntime(t *testing.T, runtimeDir, launchRoot string) {
	t.Helper()
	rel, err := filepath.Rel(runtimeDir, launchRoot)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		t.Fatalf("launch root = %q, want child under runtime dir %q", launchRoot, runtimeDir)
	}
	if !strings.HasPrefix(filepath.Base(launchRoot), "launch-") {
		t.Fatalf("launch root = %q, want launch-* child", launchRoot)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", ref+"^{commit}").Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

func seedPushedBranchArtifact(t *testing.T, teamDir, jobID string) string {
	t.Helper()
	repoRoot := filepath.Dir(teamDir)
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); os.IsNotExist(err) {
		runGit(t, repoRoot, "init")
	}
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	runGit(t, repoRoot, "checkout", "-B", "main")
	runGit(t, repoRoot, "commit", "--allow-empty", "-m", "base")
	remote := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, string(out))
	}
	if out, err := exec.Command("git", "-C", repoRoot, "remote", "get-url", "origin").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) == "" {
		runGit(t, repoRoot, "remote", "add", "origin", remote)
	}
	runGit(t, repoRoot, "push", "-u", "origin", "main")
	branch := jobstore.NormalizeID(jobID) + "-artifact"
	runGit(t, repoRoot, "checkout", "-B", branch)
	artifact := filepath.Join(repoRoot, "artifact-"+jobstore.NormalizeID(jobID)+".txt")
	if err := os.WriteFile(artifact, []byte("deliverable\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	runGit(t, repoRoot, "add", artifact)
	runGit(t, repoRoot, "commit", "-m", "test artifact")
	runGit(t, repoRoot, "push", "-u", "origin", branch)

	j, err := jobstore.Read(teamDir, jobID)
	if err != nil {
		t.Fatalf("read job for artifact: %v", err)
	}
	j.Branch = branch
	j.Worktree = repoRoot
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job artifact: %v", err)
	}
	return branch
}

func stubTicketPullRequestGh(t *testing.T, listJSON, viewJSON string) {
	t.Helper()
	binDir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
cat <<'JSON'
%s
JSON
exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
cat <<'JSON'
%s
JSON
exit 0
fi
exit 1
`, listJSON, viewJSON)
	path := filepath.Join(binDir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
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

func writeBudgetUsageJobForEventTest(t *testing.T, teamDir, ticket, team string, rec usage.Record) {
	t.Helper()
	now := rec.StartedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	j, err := jobstore.New(ticket, "worker", "budget usage test", now)
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Status = jobstore.StatusDone
	j.Origin = origin.Envelope{Team: team}
	j.Instance = rec.Instance
	rec.Origin = j.Origin
	j.Usage, _ = usage.MergeRecord(nil, rec)
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
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

func TestEvent_PersistentAgentDispatchWakesStoppedManager(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	root := DaemonRoot(teamDir)
	sessionID := seedStoppedCodexManager(t, root, teamDir, "manager")
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "agent.dispatch"
match.target = "manager"
`)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()
	t.Cleanup(func() {
		_, _ = m.Stop("manager")
		_ = waitForEventReaper(t, m, "manager")
	})

	resp := mustPost(t, srv.URL+"/v1/event", `{"type":"agent.dispatch","payload":{"target":"manager","ticket":"SQU-301"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var got struct {
		Dispatched []map[string]any `json:"dispatched"`
		Queued     []string         `json:"queued"`
		Messaged   []string         `json:"messaged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Dispatched) != 1 || got.Dispatched[0]["instance_id"] != "manager" || len(got.Queued) != 0 || len(got.Messaged) != 0 {
		t.Fatalf("outcome = %+v, want dispatched manager", got)
	}
	if got, want := fake.lastCall(), []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "resume", sessionID, "-"}; !stringSlicesEqual(got, want) {
		t.Fatalf("resume args = %v, want %v", got, want)
	}
	meta, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatalf("read manager metadata: %v", err)
	}
	if meta.Status != StatusRunning || meta.ResumeCount != 1 {
		t.Fatalf("manager metadata = %+v, want running resume", meta)
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
		_ = waitForEventReaper(t, m, "manager")
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

func seedStoppedCodexManager(t *testing.T, root, teamDir, instance string) string {
	t.Helper()
	sessionID := "11111111-1111-4111-8111-111111111111"
	codexHome := t.TempDir()
	writeCodexRollout(t, codexHome, sessionID)
	workspace := filepath.Dir(teamDir)
	now := time.Now().UTC()
	if err := WriteMetadata(root, &Metadata{
		Instance:      instance,
		Agent:         "manager",
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: "codex",
		Workspace:     workspace,
		PID:           123,
		SessionID:     sessionID,
		StartedAt:     now,
		StoppedAt:     now,
		Status:        StatusStopped,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	if err := WriteInstanceLaunchEnv(root, instance, &LaunchEnv{
		Bin:        "codex",
		Args:       []string{"codex", "exec", "-"},
		Dir:        workspace,
		Env:        []string{"CODEX_HOME=" + codexHome, "MARKER=dispatch"},
		RecordedAt: now,
		Version:    1,
	}); err != nil {
		t.Fatalf("write manager launch env: %v", err)
	}
	return sessionID
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
		_ = waitForEventReaper(t, m, "worker-squ-716-implement")
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
	if containsString(call, "--model") {
		t.Fatalf("spawn call should omit empty model: %#v", call)
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

func TestEvent_EphemeralDispatchBackfillsResourceURIs(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[project]\nid = \"dep\"\nparent_uri = \"agt://parent/project/parent\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := DaemonRoot(teamDir)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":    "worker",
		"name":      "worker-squ-156",
		"ticket":    "SQU-156",
		"job_id":    "squ-156",
		"kickoff":   "implement SQU-156",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-squ-156")
		_ = waitForEventReaper(t, m, "worker-squ-156")
	})
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v", result.Outcomes)
	}
	meta, err := ReadMetadata(root, "worker-squ-156")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if meta.URI != "agt://dep/instance/worker-squ-156" ||
		meta.SpecURI != "agt://dep/instance/worker" ||
		meta.DeploymentParentURI != "agt://parent/project/parent" ||
		meta.JobURI != "agt://dep/job/squ-156" ||
		meta.WorkspaceURI != "agt://dep/workspace/repo" ||
		meta.StateURI != "agt://dep/state/worker-squ-156" {
		t.Fatalf("metadata URIs = %+v", meta)
	}
	env := fake.lastEnv()
	for _, want := range []string{
		"AGENT_TEAM_DEPLOYMENT_URI=agt://dep/project/dep",
		"AGENT_TEAM_DEPLOYMENT_PARENT_URI=agt://parent/project/parent",
		"AGENT_TEAM_INSTANCE_URI=agt://dep/instance/worker-squ-156",
		"AGENT_TEAM_SPEC_URI=agt://dep/instance/worker",
		"AGENT_TEAM_JOB_URI=agt://dep/job/squ-156",
		"AGENT_TEAM_WORKSPACE_URI=agt://dep/workspace/repo",
		"AGENT_TEAM_STATE_URI=agt://dep/state/worker-squ-156",
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q: %#v", want, env)
		}
	}
	prompt, ok := argValue(fake.lastCall(), "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", fake.lastCall())
	}
	for _, want := range []string{
		`"deployment_uri":"agt://dep/project/dep"`,
		`"deployment_parent_uri":"agt://parent/project/parent"`,
		`"instance_uri":"agt://dep/instance/worker-squ-156"`,
		`"spec_uri":"agt://dep/instance/worker"`,
		`"job_uri":"agt://dep/job/squ-156"`,
		`"workspace_uri":"agt://dep/workspace/repo"`,
		`"state_uri":"agt://dep/state/worker-squ-156"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	j, err := jobstore.Read(teamDir, "squ-156")
	if err != nil {
		t.Fatalf("job read: %v", err)
	}
	if j.URI != "agt://dep/job/squ-156" || j.InstanceURI != meta.URI || j.WorkspaceURI != "agt://dep/workspace/repo" {
		t.Fatalf("job URIs = %+v", j)
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
		_ = waitForEventReaper(t, m, "worker-squ-64")
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
		_ = waitForEventReaper(t, m, "worker-hooks")
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
		_ = waitForEventReaper(t, m, "worker-no-hooks")
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
		_ = waitForEventReaper(t, m, "worker-otel")
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

func TestEvent_EphemeralDispatchEnvAllowFiltersInheritedEnv(t *testing.T) {
	t.Setenv("SAFE_FOR_EVENT_ALLOW", "from-parent")
	t.Setenv("LINEAR_API_KEY", "must-not-leak")
	t.Setenv("GITHUB_TOKEN", "must-not-leak")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true
env_allow = ["PATH", "HOME", "SAFE_FOR_EVENT_ALLOW"]

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-env-allow",
		"job_id":  "squ-121",
		"ticket":  "SQU-121",
		"kickoff": "check env allow",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-env-allow")
		_ = waitForEventReaper(t, m, "worker-env-allow")
	})
	env := fake.lastEnv()
	for _, want := range []string{
		"SAFE_FOR_EVENT_ALLOW=from-parent",
		"MAIN_REPO=" + filepath.Dir(teamDir),
		"AGENT_TEAM_ROOT=" + teamDir,
		"AGENT_TEAM_INSTANCE=worker-env-allow",
		"AGENT_TEAM_JOB_ID=squ-121",
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q: %#v", want, env)
		}
	}
	for _, forbidden := range []string{"LINEAR_API_KEY=", "GITHUB_TOKEN="} {
		if containsEnvPrefix(env, forbidden) {
			t.Fatalf("env leaked %q: %#v", forbidden, env)
		}
	}
	snapshot, err := ReadInstanceLaunchEnv(root, "worker-env-allow")
	if err != nil {
		t.Fatalf("read launch env: %v", err)
	}
	if containsEnvPrefix(snapshot.Env, "LINEAR_API_KEY=") || containsEnvPrefix(snapshot.Env, "GITHUB_TOKEN=") {
		t.Fatalf("snapshot leaked filtered secrets: %#v", snapshot.Env)
	}
	if !containsString(snapshot.Env, "MAIN_REPO="+filepath.Dir(teamDir)) {
		t.Fatalf("snapshot missing MAIN_REPO: %#v", snapshot.Env)
	}
}

func TestEvent_EphemeralDispatchEnvAllowUnsetIsNoOp(t *testing.T) {
	t.Setenv("UNSET_ENV_ALLOW_SECRET", "still-present")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "worker",
		"name":    "worker-env-unset",
		"kickoff": "no env allow",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-env-unset")
		_ = waitForEventReaper(t, m, "worker-env-unset")
	})
	if !containsString(fake.lastEnv(), "UNSET_ENV_ALLOW_SECRET=still-present") {
		t.Fatalf("unset env_allow changed inherited env: %#v", fake.lastEnv())
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
		_ = waitForEventReaper(t, m, "worker-codex-hooks")
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
		_ = waitForEventReaper(t, m, "worker-codex-otel")
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
		_ = waitForEventReaper(t, m, "worker-otel-disabled")
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
		_ = waitForEventReaper(t, m, "worker-no-mail")
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

func TestEvent_EphemeralDispatchRendersAndPersistsContract(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":      "worker",
		"name":        "worker-gh324-agent-contracts",
		"ticket":      "GH324-agent-contracts",
		"ticket_url":  "https://github.com/agent-team-project/kensho/issues/324",
		"epic":        "agent-team-project/kensho#324",
		"job_id":      "gh324-agent-contracts",
		"deliverable": "pr",
		"workspace":   "repo",
		"kickoff": "Required PR trailer: `Advances #324`\n\n" +
			"## Contract\n\n" +
			"AC1. Worker kickoff rendering includes a fixed contract section. (verify: go test ./internal/job)\n" +
			"AC2. Reviewer bounces cite unmet clauses.",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-gh324-agent-contracts")
		_ = waitForEventReaper(t, m, "worker-gh324-agent-contracts")
	})

	prompt, ok := argValue(fake.lastCall(), "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", fake.lastCall())
	}
	for _, want := range []string{
		"## Contract",
		"Schema: agent-team.contract.v1",
		"Required PR trailer: Advances #324",
		"AC1: Worker kickoff rendering includes a fixed contract section.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	j, err := jobstore.Read(teamDir, "gh324-agent-contracts")
	if err != nil {
		t.Fatalf("job read: %v", err)
	}
	if j.Contract == nil {
		t.Fatalf("persisted job missing contract")
	}
	if j.Contract.Schema != jobstore.ContractSchemaV1 || j.Contract.Deliverable != "pr" || j.Contract.Trailer != "Advances #324" {
		t.Fatalf("persisted contract = %+v", j.Contract)
	}
	if len(j.Contract.Criteria) != 2 || j.Contract.Criteria[0].ID != "AC1" || j.Contract.Criteria[1].ID != "AC2" {
		t.Fatalf("persisted criteria = %+v", j.Contract.Criteria)
	}
	if j.Contract.Criteria[0].Verify != "go test ./internal/job" {
		t.Fatalf("persisted verify hint = %q, want go test ./internal/job", j.Contract.Criteria[0].Verify)
	}
}

func TestEvent_AssignWorkerDispatchPersistsPRContract(t *testing.T) {
	daemonRoot := t.TempDir()
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

	kickoff := "Required PR trailer: `Closes #14`\n\n## Contract\n\nAC1. Worker opens a PR."
	script := filepath.Join("..", "..", "template", "agents", "manager", "skills", "assign-worker", "scripts", "assign_worker.sh")
	cmd := exec.Command("bash", script, "dispatch", "--ticket", "SQU-14", "--kickoff", kickoff)
	cmd.Env = append(os.Environ(),
		"AGENT_TEAM_ROOT="+teamDir,
		"AGENT_TEAM_INSTANCE=manager",
		"AGENT_TEAM_DAEMON_URL=",
		"AGENT_TEAM_DAEMON_SOCKET="+filepath.Join(daemonRoot, "missing.sock"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("assign_worker.sh dispatch: %v\n%s", err, string(out))
	}
	matches, err := filepath.Glob(filepath.Join(teamDir, "outbox", "pending", "*.json"))
	if err != nil {
		t.Fatalf("glob outbox: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("outbox files = %+v, want one", matches)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	var item struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(body, &item); err != nil {
		t.Fatalf("decode outbox: %v\n%s", err, string(body))
	}
	if item.Type != topology.EventAgentDispatch {
		t.Fatalf("outbox type = %q, want %q", item.Type, topology.EventAgentDispatch)
	}
	if item.Payload["deliverable"] != deliveryContractPR {
		t.Fatalf("outbox deliverable = %v, want %q", item.Payload["deliverable"], deliveryContractPR)
	}
	if item.Payload["workspace"] != "worktree" {
		t.Fatalf("outbox workspace = %v, want worktree", item.Payload["workspace"])
	}

	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(daemonRoot, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	result, err := resolver.EventWithResult(topology.EventAgentDispatch, item.Payload)
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("worker-squ-14")
		_ = waitForEventReaper(t, m, "worker-squ-14")
	})

	prompt, ok := argValue(fake.lastCall(), "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", fake.lastCall())
	}
	for _, want := range []string{
		"## Contract",
		"Deliverable: pr",
		"Required PR trailer: Closes #14",
		"AC1: Worker opens a PR.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	j, err := jobstore.Read(teamDir, "squ-14")
	if err != nil {
		t.Fatalf("job read: %v", err)
	}
	if j.DeliveryContract != deliveryContractPR {
		t.Fatalf("delivery contract = %q, want %q", j.DeliveryContract, deliveryContractPR)
	}
	if j.Contract == nil {
		t.Fatalf("persisted job missing contract")
	}
	if j.Contract.Deliverable != deliveryContractPR || j.Contract.Trailer != "Closes #14" {
		t.Fatalf("persisted contract = %+v", j.Contract)
	}
	if len(j.Contract.Criteria) != 1 || j.Contract.Criteria[0].ID != "AC1" || j.Contract.Criteria[0].Text != "Worker opens a PR." {
		t.Fatalf("persisted criteria = %+v", j.Contract.Criteria)
	}
}

func TestAgentContractPromptExpectations(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	files := map[string][]string{
		filepath.Join(root, "template", "agents", "manager", "agent.md"): {
			"durable `[contract]` block",
			"observable outcomes, not implementation plans",
		},
		filepath.Join(root, "template", "agents", "reviewer", "agent.md"): {
			"Read the durable job contract first",
			"`clause=ACn`",
			"`clause=none`",
			"clause-keyed ledger",
		},
		filepath.Join(root, "template", "instances.toml.tmpl"): {
			"durable job contract first",
			"clause-keyed ledger",
			"`clause=ACn` or `clause=none`",
		},
	}
	for path, wants := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(body)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
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
		_ = waitForEventReaper(t, m, "worker-big-mail")
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
	fake := newFakeSpawner(eventShortFakeRuntime)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-96","ticket":"SQU-96","job_id":"squ-96","kickoff":"finish quickly"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-96"); err != nil {
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
	writeFixtureRuntimeCommandSkills(t, teamDir, "worker")
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
	env := fake.lastEnv()
	runtimeDir := filepath.Join(teamDir, "state", "worker-squ-42", "runtime")
	addDir, ok := argValue(call, "--add-dir")
	if !ok {
		t.Fatalf("codex spawn call missing --add-dir value: %#v", call)
	}
	assertLaunchRootUnderRuntime(t, runtimeDir, addDir)
	assertEventRuntimeCommandSurface(t, addDir, env)
	for _, want := range []string{
		"shell_environment_policy.set.AGENT_TEAM_ROOT=" + strconv.Quote(teamDir),
		"shell_environment_policy.set.AGENT_TEAM_INSTANCE=" + strconv.Quote("worker-squ-42"),
		"shell_environment_policy.set.AGENT_TEAM_STATE_DIR=" + strconv.Quote(filepath.Join(teamDir, "state", "worker-squ-42")),
		"shell_environment_policy.set.AGENT_TEAM_DAEMON_SOCKET=" + strconv.Quote(SocketPath(teamDir)),
		"shell_environment_policy.set.PATH=" + strconv.Quote(lastEnvValue(env, "PATH")),
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
	_ = waitForEventReaper(t, m, "worker-squ-42")
}

func TestEvent_EphemeralDispatchUsesPerLaunchRootAndPreservesPersistentPrompt(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindClaude))
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureRuntimeCommandSkills(t, teamDir, "worker")
	instance := "worker-squ-42"
	stateDir := filepath.Join(teamDir, "state", instance)
	runtimeDir := filepath.Join(stateDir, "runtime")
	stablePrompt := filepath.Join(runtimeDir, "system_prompt.md")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if err := os.WriteFile(stablePrompt, []byte("persistent resume prompt"), 0o644); err != nil {
		t.Fatalf("write stable prompt: %v", err)
	}

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
	addDir, ok := argValue(call, "--add-dir")
	if !ok {
		t.Fatalf("claude spawn call missing --add-dir value: %#v", call)
	}
	assertLaunchRootUnderRuntime(t, runtimeDir, addDir)
	promptFile, ok := argValue(call, "--append-system-prompt-file")
	if !ok {
		t.Fatalf("claude spawn call missing prompt file value: %#v", call)
	}
	if filepath.Dir(promptFile) != addDir {
		t.Fatalf("prompt file = %q, want inside add-dir %q", promptFile, addDir)
	}
	assertEventRuntimeCommandSurface(t, addDir, fake.lastEnv())
	stableBody, err := os.ReadFile(stablePrompt)
	if err != nil {
		t.Fatalf("read stable prompt: %v", err)
	}
	if string(stableBody) != "persistent resume prompt" {
		t.Fatalf("stable prompt changed to %q", string(stableBody))
	}
	launchBody, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("read launch prompt: %v", err)
	}
	if !strings.Contains(string(launchBody), "You are fixture worker.") {
		t.Fatalf("launch prompt missing agent prompt:\n%s", string(launchBody))
	}
	if got := launchArgValue(call, "-p"); !strings.Contains(got, "implement SQU-42") {
		t.Fatalf("claude task prompt = %q, want kickoff", got)
	}
	snapshot, err := ReadInstanceLaunchEnv(root, instance)
	if err != nil {
		t.Fatalf("read launch snapshot: %v", err)
	}
	if got := launchArgValue(snapshot.Args, "--append-system-prompt-file"); got != promptFile {
		t.Fatalf("snapshot prompt = %q, want %q", got, promptFile)
	}

	_, _ = m.Stop(instance)
	_ = waitForEventReaper(t, m, instance)
	if _, err := os.Stat(addDir); !os.IsNotExist(err) {
		t.Fatalf("launch root should be cleaned after reap, stat err=%v", err)
	}
	if body, err := os.ReadFile(stablePrompt); err != nil || string(body) != "persistent resume prompt" {
		t.Fatalf("stable prompt after cleanup = %q err=%v", string(body), err)
	}
}

func TestEvent_EphemeralDispatchForwardsModelForCodexRuntime(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureRuntimeCommandSkills(t, teamDir, "worker")
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[runtime]\nkind = \"claude\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	top := mustParseCustomTopo(t, `
[model_policy]
runtime = "codex"
model = "gpt-5.6-sol"
effort = "xhigh"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-44","kickoff":"implement SQU-44"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	call := fake.lastCall()
	if len(call) < 2 || call[0] != "codex" || call[1] != "exec" {
		t.Fatalf("spawn call = %#v, want codex exec", call)
	}
	if got, ok := argValue(call, "--model"); !ok || got != "gpt-5.6-sol" {
		t.Fatalf("codex spawn call model = %q, %v; want gpt-5.6-sol in %#v", got, ok, call)
	}
	if !containsArgSubstring(call, `model_reasoning_effort="xhigh"`) {
		t.Fatalf("codex spawn call missing effort config: %#v", call)
	}
	meta, err := ReadMetadata(root, "worker-squ-44")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Model != "gpt-5.6-sol" || meta.Effort != "xhigh" {
		t.Fatalf("metadata model/effort = %q/%q, want gpt-5.6-sol/xhigh", meta.Model, meta.Effort)
	}
	_, _ = m.Stop("worker-squ-44")
	_ = waitForEventReaper(t, m, "worker-squ-44")
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
	_ = waitForEventReaper(t, m, "worker-squ-43")
}

func TestEvent_EphemeralDispatchUsesDeclaredInstanceRuntime(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[runtime]\nkind = \"codex\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.docs-writer]
agent = "worker"
ephemeral = true
runtime = "claude"
runtime_bin = "claude-docs"
model = "claude-fable-5"
effort = "max"

[[instances.docs-writer.triggers]]
event = "agent.dispatch"
match.target = "docs-writer"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"docs-writer","name":"docs-writer-squ-134","kickoff":"write docs"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	call := fake.lastCall()
	if len(call) < 2 || call[0] != "claude-docs" || call[1] != "--session-id" {
		t.Fatalf("spawn call = %#v, want declared instance claude runtime", call)
	}
	if got, ok := argValue(call, "--model"); !ok || got != "claude-fable-5" {
		t.Fatalf("spawn call model = %q, %v; want claude-fable-5 in %#v", got, ok, call)
	}
	if got, ok := argValue(call, "--effort"); !ok || got != "max" {
		t.Fatalf("spawn call effort = %q, %v; want max in %#v", got, ok, call)
	}
	meta, err := ReadMetadata(root, "docs-writer-squ-134")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Runtime != string(runtimebin.KindClaude) || meta.RuntimeBinary != "claude-docs" || meta.SessionID == "" {
		t.Fatalf("metadata = %+v, want declared instance claude runtime", meta)
	}
	if meta.Effort != "max" {
		t.Fatalf("metadata effort = %q, want max", meta.Effort)
	}
	_, _ = m.Stop("docs-writer-squ-134")
	_ = waitForEventReaper(t, m, "docs-writer-squ-134")
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
	_ = waitForEventReaper(t, m, "worker-squ-77")
}

func TestEvent_EphemeralDispatchPayloadRuntimeOverridesDeclaredInstanceRuntime(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.docs-writer]
agent = "worker"
ephemeral = true
runtime = "claude"

[[instances.docs-writer.triggers]]
event = "agent.dispatch"
match.target = "docs-writer"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"docs-writer","name":"docs-writer-squ-135","kickoff":"write docs","runtime":"codex","runtime_binary":"codex-dev"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	call := fake.lastCall()
	if len(call) < 2 || call[0] != "codex-dev" || call[1] != "exec" {
		t.Fatalf("spawn call = %#v, want payload-backed codex runtime", call)
	}
	meta, err := ReadMetadata(root, "docs-writer-squ-135")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Runtime != string(runtimebin.KindCodex) || meta.RuntimeBinary != "codex-dev" || meta.SessionID != "" {
		t.Fatalf("metadata = %+v, want payload-backed codex without Claude session", meta)
	}
	_, _ = m.Stop("docs-writer-squ-135")
	_ = waitForEventReaper(t, m, "docs-writer-squ-135")
}

func TestEvent_EphemeralDispatchKeepsPolicyWithinEffectiveRuntimeFamily(t *testing.T) {
	tests := []struct {
		name        string
		topology    string
		target      string
		instance    string
		payload     map[string]any
		wantRuntime string
		wantModel   string
		wantEffort  string
	}{
		{
			name: "non-Fable to Claude clears inherited selectors",
			topology: `
[model_policy]
runtime = "codex"
model = "gpt-5.6-sol"
effort = "xhigh"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`,
			target: "worker", instance: "worker-runtime-family", payload: map[string]any{"runtime": "claude"},
			wantRuntime: "claude",
		},
		{
			name: "Fable to Codex clears inherited selectors",
			topology: `
[model_policy]
runtime = "codex"
model = "gpt-5.6-sol"
effort = "xhigh"

[instances.advisor]
agent = "worker"
ephemeral = true
runtime = "claude"
model = "claude-fable-5"
effort = "max"

[[instances.advisor.triggers]]
event = "agent.dispatch"
match.target = "advisor"
`,
			target: "advisor", instance: "advisor-runtime-family", payload: map[string]any{"runtime": "codex"},
			wantRuntime: "codex",
		},
		{
			name: "explicit new-family payload selectors remain authoritative",
			topology: `
[model_policy]
runtime = "codex"
model = "gpt-5.6-sol"
effort = "xhigh"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`,
			target: "worker", instance: "worker-explicit-family", payload: map[string]any{"runtime": "claude", "model": "claude-fable-5", "effort": "max"},
			wantRuntime: "claude", wantModel: "claude-fable-5", wantEffort: "max",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(runtimebin.EnvRuntime, "")
			t.Setenv(runtimebin.EnvBinary, "")
			root := t.TempDir()
			teamDir := fixtureTeamDir(t)
			top := mustParseCustomTopo(t, tt.topology)
			fake := newFakeSpawner(30 * time.Second)
			m := NewInstanceManager(root, fake.spawn)
			resolver := NewEventResolver(m, teamDir, top)
			payload := map[string]any{"target": tt.target, "name": tt.instance, "kickoff": "runtime family test"}
			for key, value := range tt.payload {
				payload[key] = value
			}
			result, err := resolver.EventWithResult(topology.EventAgentDispatch, payload)
			if err != nil {
				t.Fatalf("EventWithResult: %v", err)
			}
			if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
				t.Fatalf("outcomes = %+v", result.Outcomes)
			}
			assertRuntimePolicyArgs(t, fake.lastCall(), tt.wantRuntime, tt.wantModel, tt.wantEffort)
			meta, err := ReadMetadata(root, tt.instance)
			if err != nil {
				t.Fatalf("ReadMetadata: %v", err)
			}
			if meta.Runtime != tt.wantRuntime || meta.Model != tt.wantModel || meta.Effort != tt.wantEffort {
				t.Fatalf("metadata policy = %q/%q/%q, want %q/%q/%q", meta.Runtime, meta.Model, meta.Effort, tt.wantRuntime, tt.wantModel, tt.wantEffort)
			}
			_, _ = m.Stop(tt.instance)
			_ = waitForEventReaper(t, m, tt.instance)
		})
	}
}

func TestEvent_RuntimeOnlyDeclaredInstanceOverrideClearsInheritedSelectors(t *testing.T) {
	tests := []struct {
		name                                     string
		policyRuntime, policyModel, policyEffort string
		instanceRuntime                          string
	}{
		{"non-Fable to Claude", "codex", "gpt-5.6-sol", "xhigh", "claude"},
		{"Fable to Codex", "claude", "claude-fable-5", "max", "codex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(runtimebin.EnvRuntime, "")
			t.Setenv(runtimebin.EnvBinary, "")
			root := t.TempDir()
			teamDir := fixtureTeamDir(t)
			top := mustParseCustomTopo(t, fmt.Sprintf(`
[model_policy]
runtime = %q
model = %q
effort = %q

[instances.override]
agent = "worker"
ephemeral = true
runtime = %q

[[instances.override.triggers]]
event = "agent.dispatch"
match.target = "override"
`, tt.policyRuntime, tt.policyModel, tt.policyEffort, tt.instanceRuntime))
			fake := newFakeSpawner(30 * time.Second)
			m := NewInstanceManager(root, fake.spawn)
			resolver := NewEventResolver(m, teamDir, top)
			instance := "override-" + strings.ReplaceAll(strings.ToLower(tt.instanceRuntime), "_", "-")
			result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
				"target": "override", "name": instance, "kickoff": "declared runtime family test",
			})
			if err != nil {
				t.Fatalf("EventWithResult: %v", err)
			}
			if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
				t.Fatalf("outcomes = %+v", result.Outcomes)
			}
			assertRuntimePolicyArgs(t, fake.lastCall(), tt.instanceRuntime, "", "")
			meta, err := ReadMetadata(root, instance)
			if err != nil {
				t.Fatalf("ReadMetadata: %v", err)
			}
			if meta.Model != "" || meta.Effort != "" {
				t.Fatalf("metadata inherited incompatible selectors: %+v", meta)
			}
			_, _ = m.Stop(instance)
			_ = waitForEventReaper(t, m, instance)
		})
	}
}

func TestEvent_RuntimeOnlyPipelineOverrideClearsTargetSelectors(t *testing.T) {
	tests := []struct {
		name                                     string
		policyRuntime, policyModel, policyEffort string
		stepRuntime                              string
	}{
		{"non-Fable to Claude", "codex", "gpt-5.6-sol", "xhigh", "claude"},
		{"Fable to Codex", "claude", "claude-fable-5", "max", "codex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(runtimebin.EnvRuntime, "")
			t.Setenv(runtimebin.EnvBinary, "")
			root := t.TempDir()
			teamDir := fixtureTeamDir(t)
			top := mustParseCustomTopo(t, fmt.Sprintf(`
[model_policy]
runtime = %q
model = %q
effort = %q

[instances.worker]
agent = "worker"
ephemeral = true

[pipelines.compatibility]
trigger.event = "ticket.created"

[[pipelines.compatibility.steps]]
id = "implement"
target = "worker"
runtime = %q
`, tt.policyRuntime, tt.policyModel, tt.policyEffort, tt.stepRuntime))
			fake := newFakeSpawner(30 * time.Second)
			m := NewInstanceManager(root, fake.spawn)
			resolver := NewEventResolver(m, teamDir, top)
			result, err := resolver.EventWithResult("ticket.created", map[string]any{
				"ticket": "SQU-9364", "kickoff": "pipeline runtime family test", "workspace": "repo",
			})
			if err != nil {
				t.Fatalf("EventWithResult: %v", err)
			}
			if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
				t.Fatalf("outcomes = %+v", result.Outcomes)
			}
			instance := result.Outcomes[0].InstanceID
			assertRuntimePolicyArgs(t, fake.lastCall(), tt.stepRuntime, "", "")
			meta, err := ReadMetadata(root, instance)
			if err != nil {
				t.Fatalf("ReadMetadata: %v", err)
			}
			if meta.Model != "" || meta.Effort != "" {
				t.Fatalf("metadata inherited incompatible selectors: %+v", meta)
			}
			job, err := jobstore.Read(teamDir, "SQU-9364")
			if err != nil {
				t.Fatalf("read job: %v", err)
			}
			if job.Steps[0].Model != "" || job.Steps[0].Effort != "" {
				t.Fatalf("pipeline step inherited incompatible selectors: %+v", job.Steps[0])
			}
			_, _ = m.Stop(instance)
			_ = waitForEventReaper(t, m, instance)
		})
	}
}

func assertRuntimePolicyArgs(t *testing.T, call []string, runtime, model, effort string) {
	t.Helper()
	if len(call) == 0 || call[0] != runtime {
		t.Fatalf("spawn call = %#v, want runtime binary %q", call, runtime)
	}
	if got, ok := argValue(call, "--model"); model == "" && ok {
		t.Fatalf("spawn call received incompatible model %q: %#v", got, call)
	} else if model != "" && (!ok || got != model) {
		t.Fatalf("spawn model = %q, %v; want %q in %#v", got, ok, model, call)
	}
	if effort == "" {
		if got, ok := argValue(call, "--effort"); ok {
			t.Fatalf("spawn call received incompatible Claude effort %q: %#v", got, call)
		}
		if containsArgSubstring(call, "model_reasoning_effort=") {
			t.Fatalf("spawn call received incompatible Codex effort: %#v", call)
		}
		return
	}
	if runtime == string(runtimebin.KindClaude) {
		if got, ok := argValue(call, "--effort"); !ok || got != effort {
			t.Fatalf("Claude effort = %q, %v; want %q in %#v", got, ok, effort, call)
		}
		return
	}
	if !containsArgSubstring(call, `model_reasoning_effort="`+effort+`"`) {
		t.Fatalf("Codex effort %q missing from %#v", effort, call)
	}
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
	_ = waitForEventReaper(t, m, "worker-squ-44")
}

func TestEvent_DirectDispatchWithJobIDWritesLinearInProgress(t *testing.T) {
	// SQU-68 round-5 finding: a direct agent.dispatch that attaches a job via
	// job_id (no pipeline_step) must still attempt the in-progress write-back.
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "worker")
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[pm]\nprovider = \"linear\"\n\n[linear]\nteam_id = \"demo\"\nticket_prefix = \"SQU\"\nin_progress_state = \"In Progress\"\n"), 0o644); err != nil {
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
		if ev.Type == "linear_writeback_skipped" && ev.Data["action"] == string(pmprovider.ActionDispatchInProgress) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("direct job_id dispatch missing in-progress write-back attempt: %+v", events)
	}
	_, _ = m.Stop("worker-squ-96")
	_ = waitForEventReaper(t, m, "worker-squ-96")
}

func TestEvent_ProbeDispatchMarksJobAndSkipsDeliverySideEffects(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_USER_API_KEY", "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[pm]\nprovider = \"linear\"\n\n[linear]\nteam_id = \"demo\"\nticket_prefix = \"SQU\"\nin_progress_state = \"In Progress\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-97","ticket":"SQU-97","job_id":"squ-97","kind":"probe","kickoff":"measure harness behavior","workspace":"worktree"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	j, err := jobstore.Read(teamDir, "squ-97")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Kind != jobstore.KindProbe || j.Status != jobstore.StatusRunning {
		t.Fatalf("job = %+v, want running probe", j)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-97")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, ev := range events {
		if ev.Type == "linear_writeback_skipped" {
			t.Fatalf("probe dispatch attempted Linear writeback: %+v", events)
		}
	}
	meta, err := ReadMetadata(root, "worker-squ-97")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Branch != "" || meta.Workspace != filepath.Dir(teamDir) {
		t.Fatalf("metadata = %+v, want repo workspace without branch", meta)
	}
	env := fake.lastEnv()
	if !containsString(env, "AGENT_TEAM_JOB_KIND=probe") {
		t.Fatalf("env missing probe kind: %v", env)
	}
	if containsEnvPrefix(env, "AGENT_TEAM_BRANCH=") || containsEnvPrefix(env, "AGENT_TEAM_WORKTREE=") {
		t.Fatalf("probe env unexpectedly contains branch/worktree: %v", env)
	}
	combined := strings.Join(fake.lastCall(), " ") + fake.lastStdin()
	for _, want := range []string{"## Probe job", "do not open a PR", "do not create or use a branch"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("probe prompt missing %q:\n%s", want, combined)
		}
	}
	_, _ = m.Stop("worker-squ-97")
	_ = waitForEventReaper(t, m, "worker-squ-97")
}

func TestEvent_TicketDispatchCreatesJobAndExportsContext(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_USER_API_KEY", "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`
[pm]
provider = "linear"

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
			ev.Data["action"] == string(pmprovider.ActionDispatchInProgress) &&
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
	_ = waitForEventReaper(t, m, "worker-squ-95")
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
	env := fake.lastEnv()
	for _, want := range []string{
		"MAIN_REPO=" + repoRoot,
		"AGENT_TEAM_BRANCH=" + meta.Branch,
		"AGENT_TEAM_WORKTREE=" + meta.Workspace,
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q in %v", want, env)
		}
	}
	snapshot, err := ReadInstanceLaunchEnv(root, "worker-squ-42")
	if err != nil {
		t.Fatalf("read launch env: %v", err)
	}
	for _, want := range []string{
		"MAIN_REPO=" + repoRoot,
		"AGENT_TEAM_BRANCH=" + meta.Branch,
		"AGENT_TEAM_WORKTREE=" + meta.Workspace,
	} {
		if !containsString(snapshot.Env, want) {
			t.Fatalf("launch env missing %q in %v", want, snapshot.Env)
		}
	}
	_, _ = m.Stop("worker-squ-42")
	_ = waitForEventReaper(t, m, "worker-squ-42")
}

func TestEvent_WorktreeDispatchBasesRequeuedJobOnExistingBranch(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "base")
	runGit(t, repoRoot, "checkout", "-B", "main")
	runGit(t, repoRoot, "checkout", "-b", "squ-198-rejected")
	if err := os.WriteFile(filepath.Join(repoRoot, "rejected.txt"), []byte("rejected implementation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "add", "rejected.txt")
	runGit(t, repoRoot, "commit", "-m", "rejected implementation")
	rejectedHead := gitRevParse(t, repoRoot, "squ-198-rejected")
	runGit(t, repoRoot, "checkout", "main")

	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")
	now := time.Now().UTC()
	j := &jobstore.Job{
		ID:        "squ-198",
		Ticket:    "SQU-198",
		Target:    "worker",
		Status:    jobstore.StatusQueued,
		Branch:    "squ-198-rejected",
		Worktree:  repoRoot,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, root))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-198","ticket":"SQU-198","job_id":"squ-198","workspace":"worktree","kickoff":"fix review findings"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	meta, err := ReadMetadata(root, "worker-squ-198")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if got := gitRevParse(t, meta.Workspace, "HEAD"); got != rejectedHead {
		t.Fatalf("worktree HEAD = %s, want rejected branch head %s", got, rejectedHead)
	}
	if _, err := os.Stat(filepath.Join(meta.Workspace, "rejected.txt")); err != nil {
		t.Fatalf("bounced worktree missing rejected implementation file: %v", err)
	}

	_, _ = m.Stop("worker-squ-198")
	_ = waitForEventReaper(t, m, "worker-squ-198")
}

func TestEvent_DirectWorktreeDispatchDoneWithoutDeliverableFailsAndMessagesManager(t *testing.T) {
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

	fake := newFakeSpawner(eventShortFakeRuntime)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"agent.dispatch","payload":{"target":"worker","name":"worker-squ-155","ticket":"SQU-155","job_id":"squ-155","workspace":"worktree","kickoff":"write docs"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-155"); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	j, err := jobstore.Read(teamDir, "squ-155")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Pipeline != "" || j.DeliveryContract != deliveryContractBranch {
		t.Fatalf("job contract = pipeline %q delivery %q, want direct branch contract", j.Pipeline, j.DeliveryContract)
	}
	if j.Status != jobstore.StatusFailed || j.LastEvent != "deliverable_missing" {
		t.Fatalf("job = %+v, want failed deliverable_missing", j)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, "squ-155") || !strings.Contains(messages[0].Body, "delivery artifact missing") {
		t.Fatalf("manager messages = %+v, want missing-deliverable notification", messages)
	}
}

func TestEvent_StalePipelineExitDoesNotFailActiveBouncedStep(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")
	now := time.Now().UTC()
	j := &jobstore.Job{
		ID:               "squ-198",
		Ticket:           "SQU-198",
		Target:           "worker",
		Pipeline:         "ticket_to_pr",
		Attempt:          2,
		DeliveryContract: deliveryContractTicketToPR,
		Status:           jobstore.StatusRunning,
		Instance:         "worker-squ-198-bounce",
		LastEvent:        "advance_dispatched",
		LastStatus:       "running implement",
		CreatedAt:        now.Add(-time.Hour),
		UpdatedAt:        now,
		Steps: []jobstore.Step{
			{ID: "implement", Target: "worker", Status: jobstore.StatusRunning, Instance: "worker-squ-198-bounce", StartedAt: now.Add(-time.Minute), RunningAt: now.Add(-time.Minute)},
			{ID: "review", Target: "reviewer", Status: jobstore.StatusBlocked, After: []string{"implement"}},
		},
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	zero := 0
	resolver.reconcileEphemeralJobExit(&Metadata{
		Instance: "worker-squ-198-bounce",
		Job:      "squ-198",
		Attempt:  1,
		Ticket:   "SQU-198",
		Status:   StatusExited,
		ExitCode: &zero,
	})

	updated, err := jobstore.Read(teamDir, "squ-198")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != jobstore.StatusRunning || updated.Instance != "worker-squ-198-bounce" || updated.LastEvent != "advance_dispatched" {
		t.Fatalf("updated job = %+v, want active bounced implement unchanged", updated)
	}
	if updated.Steps[0].Status != jobstore.StatusRunning || updated.Steps[0].Instance != "worker-squ-198-bounce" || updated.Steps[1].Status != jobstore.StatusBlocked {
		t.Fatalf("steps = %+v, want active implement and blocked review unchanged", updated.Steps)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-198")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, ev := range events {
		if ev.Type == "deliverable_missing" {
			t.Fatalf("stale reviewer exit emitted deliverable_missing: %+v", events)
		}
	}
}

func TestEvent_StaleHeadExitDoesNotCompleteCurrentStep(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	now := time.Now().UTC()
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	j := &jobstore.Job{
		ID: "gh-230-head-exit", Ticket: "GH-230", Target: "worker", Pipeline: "ticket_to_pr", Attempt: 1,
		Head: headB, Status: jobstore.StatusRunning, CreatedAt: now, UpdatedAt: now,
		Steps: []jobstore.Step{
			{ID: "implement", Target: "worker", Status: jobstore.StatusDone},
			{ID: "review", Target: "reviewer", Status: jobstore.StatusRunning, Instance: "reviewer-gh-230-review", After: []string{"implement"}},
			{ID: "approve", Target: "manager", Status: jobstore.StatusBlocked, After: []string{"review"}, Gate: jobstore.StepGateManual},
		},
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))
	zero := 0
	resolver.reconcileEphemeralJobExit(&Metadata{
		Instance: "reviewer-gh-230-review", Job: j.ID, Attempt: 1, Head: headA,
		Status: StatusExited, ExitCode: &zero,
	})
	unchanged, err := jobstore.Read(teamDir, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Head != headB || unchanged.Steps[1].Status != jobstore.StatusRunning || unchanged.Steps[2].Status != jobstore.StatusBlocked {
		t.Fatalf("stale head exit changed current job = %+v", unchanged)
	}

	resolver.reconcileEphemeralJobExit(&Metadata{
		Instance: "reviewer-gh-230-review", Job: j.ID, Attempt: 1, Head: headB,
		Status: StatusExited, ExitCode: &zero,
	})
	updated, err := jobstore.Read(teamDir, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Head != headB || updated.Steps[1].Status != jobstore.StatusDone || updated.Steps[2].Status != jobstore.StatusBlocked {
		t.Fatalf("current head exit did not complete step = %+v", updated)
	}
}

func TestEvent_CurrentAttemptSupersedesQueuedPriorReviewer(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "reviewer")
	top := mustParseCustomTopo(t, `
[instances.reviewer]
agent = "reviewer"
ephemeral = true
replicas = 1

[[instances.reviewer.triggers]]
event = "agent.dispatch"
match.target = "reviewer"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	blocker := resolver.actuateEphemeral(top.Instances["reviewer"], topology.EventAgentDispatch, map[string]any{
		"target": "reviewer", "name": "reviewer-capacity-blocker", "workspace": "repo",
	})
	if blocker.Action != "dispatched" {
		t.Fatalf("blocker = %+v", blocker)
	}
	t.Cleanup(func() {
		_, _ = m.Stop("reviewer-capacity-blocker")
		_ = waitForEventReaper(t, m, "reviewer-capacity-blocker")
		_, _ = m.Stop("reviewer-gh-230-review")
		_ = waitForEventReaper(t, m, "reviewer-gh-230-review")
	})

	now := time.Now().UTC()
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	j := &jobstore.Job{
		ID: "gh-230", Ticket: "GH-230", Target: "worker", Pipeline: "ticket_to_pr", Attempt: 1,
		Head: headA, Status: jobstore.StatusRunning, CreatedAt: now, UpdatedAt: now,
		Steps: []jobstore.Step{{ID: "review", Target: "reviewer", Status: jobstore.StatusQueued, Instance: "reviewer-gh-230-review"}},
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"target": "reviewer", "name": "reviewer-gh-230-review", "job_id": j.ID,
		"pipeline": j.Pipeline, "pipeline_step": "review", "attempt": 1, "head": headA, "workspace": "repo",
	}
	old := resolver.actuateEphemeral(top.Instances["reviewer"], topology.EventAgentDispatch, payload)
	if old.Action != "queued" {
		t.Fatalf("old reviewer = %+v", old)
	}
	j.Head = headB
	j.Steps[0].Status = jobstore.StatusBlocked
	j.Steps[0].Instance = ""
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	payload["head"] = headB
	current := resolver.actuateEphemeral(top.Instances["reviewer"], topology.EventAgentDispatch, payload)
	if current.Action != "queued" {
		t.Fatalf("current reviewer = %+v, want superseding queue admission", current)
	}
	_, queued := resolver.QueueDepth("reviewer")
	if queued != 1 {
		t.Fatalf("queued = %d, want one current reviewer", queued)
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || payloadAttempt(items[0].Payload) != 1 || payloadString(items[0].Payload, "head") != headB || items[0].InstanceID != "reviewer-gh-230-review" {
		t.Fatalf("queue items = %+v", items)
	}
	stalePayload := copyPayload(payload)
	stalePayload["name"] = "reviewer-gh-230-stale"
	stalePayload["head"] = headA
	stale := resolver.actuateEphemeral(top.Instances["reviewer"], topology.EventAgentDispatch, stalePayload)
	if stale.Action != "rejected" || !strings.Contains(stale.Reason, "stale job head") {
		t.Fatalf("stale reviewer = %+v, want head-generation rejection", stale)
	}
}

func TestEvent_QueueDrainDropsStaleHead(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "reviewer")
	top := mustParseCustomTopo(t, `
[instances.reviewer]
agent = "reviewer"
ephemeral = true
replicas = 1

[[instances.reviewer.triggers]]
event = "agent.dispatch"
match.target = "reviewer"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	t.Cleanup(func() {
		_, _ = m.Stop("reviewer-gh-230-old")
		_ = waitForEventReaper(t, m, "reviewer-gh-230-old")
	})
	blocker := resolver.actuateEphemeral(top.Instances["reviewer"], topology.EventAgentDispatch, map[string]any{
		"target": "reviewer", "name": "reviewer-capacity-blocker", "workspace": "repo",
	})
	if blocker.Action != "dispatched" {
		t.Fatalf("blocker = %+v", blocker)
	}

	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	j := &jobstore.Job{
		ID: "gh-230-drain", Ticket: "GH-230", Target: "worker", Pipeline: "ticket_to_pr", Attempt: 1,
		Head: headA, Status: jobstore.StatusRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		Steps: []jobstore.Step{{ID: "review", Target: "reviewer", Status: jobstore.StatusQueued, Instance: "reviewer-gh-230-old"}},
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	old := resolver.actuateEphemeral(top.Instances["reviewer"], topology.EventAgentDispatch, map[string]any{
		"target": "reviewer", "name": "reviewer-gh-230-old", "job_id": j.ID,
		"pipeline": j.Pipeline, "pipeline_step": "review", "attempt": 1, "head": headA, "workspace": "repo",
	})
	if old.Action != "queued" {
		t.Fatalf("old reviewer = %+v", old)
	}
	j.Head = headB
	j.Steps[0].Status = jobstore.StatusBlocked
	j.Steps[0].Instance = ""
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Stop("reviewer-capacity-blocker"); err != nil {
		t.Fatal(err)
	}
	if err := waitForEventReaper(t, m, "reviewer-capacity-blocker"); err != nil {
		t.Fatal(err)
	}
	if m.isRunning("reviewer-gh-230-old") || fake.callCount() != 1 {
		t.Fatalf("stale queued reviewer drained: running=%t spawn_calls=%d", m.isRunning("reviewer-gh-230-old"), fake.callCount())
	}
	_, queued := resolver.QueueDepth("reviewer")
	if queued != 0 {
		t.Fatalf("stale queue depth = %d, want 0", queued)
	}
}

func TestEvent_CurrentHeadSupersedesRunningPriorReviewer(t *testing.T) {
	root := t.TempDir()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "reviewer")
	top := mustParseCustomTopo(t, `
[instances.reviewer]
agent = "reviewer"
ephemeral = true
replicas = 1

[[instances.reviewer.triggers]]
event = "agent.dispatch"
match.target = "reviewer"

[teams.platform]
instances = ["reviewer"]

[budgets.platform]
tokens_per_day = 100
allocation = "reserve"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	t.Cleanup(func() {
		_, _ = m.Stop("reviewer-gh-230-review")
		_ = waitForEventReaper(t, m, "reviewer-gh-230-review")
	})

	now := time.Now().UTC()
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	j := &jobstore.Job{
		ID: "gh-230-running", Ticket: "GH-230", Target: "worker", Pipeline: "ticket_to_pr", Attempt: 1,
		Head: headA, Status: jobstore.StatusRunning, CreatedAt: now, UpdatedAt: now,
		Steps: []jobstore.Step{{ID: "review", Target: "reviewer", Status: jobstore.StatusRunning, Instance: "reviewer-gh-230-review"}},
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"target": "reviewer", "name": "reviewer-gh-230-review", "job_id": j.ID,
		"pipeline": j.Pipeline, "pipeline_step": "review", "attempt": 1, "head": headA, "workspace": "repo",
		"budget_tokens": int64(60),
	}
	old := resolver.actuateEphemeral(top.Instances["reviewer"], topology.EventAgentDispatch, payload)
	if old.Action != "dispatched" {
		t.Fatalf("old reviewer = %+v", old)
	}
	allocations, err := budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 1 || allocations[0].Status != budget.AllocationStatusOutstanding || allocations[0].Tokens != 60 {
		t.Fatalf("old reviewer allocations = %+v, want one outstanding 60-token reserve", allocations)
	}
	j.Head = headB
	j.Steps[0].Status = jobstore.StatusBlocked
	j.Steps[0].Instance = ""
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatal(err)
	}
	payload["head"] = headB
	current := resolver.actuateEphemeral(top.Instances["reviewer"], topology.EventAgentDispatch, payload)
	if current.Action != "dispatched" {
		t.Fatalf("current reviewer = %+v, want old-head runtime superseded", current)
	}
	allocations, err = budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatal(err)
	}
	var outstanding, released int
	for _, allocation := range allocations {
		switch allocation.Status {
		case budget.AllocationStatusOutstanding:
			outstanding++
		case budget.AllocationStatusReleased:
			released++
		}
	}
	if len(allocations) != 2 || released != 1 || outstanding != 1 {
		t.Fatalf("superseded reviewer allocations = %+v, want old released and current outstanding", allocations)
	}
	rows, err := budget.Statuses(teamDir, top, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TokensAllocated != 60 || rows[0].TokensRemaining != 40 {
		t.Fatalf("current reviewer budget = %+v, want only current 60-token reserve", rows)
	}
	meta, err := ReadMetadata(root, "reviewer-gh-230-review")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Attempt != 1 || meta.Head != headB || meta.Status != StatusRunning {
		t.Fatalf("current reviewer metadata = %+v", meta)
	}
}

func TestEvent_NotifyManagerMissingDeliveryArtifactUsesReportContract(t *testing.T) {
	root := t.TempDir()
	reportPath := ".agent_team/state/worker-squ-193/report.md"
	j := &jobstore.Job{
		ID:               "squ-193",
		DeliveryContract: "report:" + reportPath,
	}
	reason := "delivery artifact missing: expected non-empty report artifact at " + reportPath + " before accepting done"

	NotifyManagerMissingDeliveryArtifact(root, j, "worker-squ-193", reason)

	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("manager messages = %+v, want one notification", messages)
	}
	body := messages[0].Body
	if !strings.Contains(body, "Expected a non-empty report artifact at "+reportPath+" before accepting done.") {
		t.Fatalf("manager message = %q, want report artifact expectation", body)
	}
	for _, unwanted := range []string{"Expected an open PR", "pushed branch", "committed diff"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("manager message = %q, should not include %q for report contract", body, unwanted)
		}
	}
}

func TestEvent_NotifyManagerMissingDeliveryArtifactKeepsTicketToPRExpectation(t *testing.T) {
	root := t.TempDir()
	j := &jobstore.Job{
		ID:               "squ-155",
		DeliveryContract: deliveryContractTicketToPR,
	}
	reason := "delivery artifact missing: expected an open PR, pushed branch, or non-empty committed diff before accepting done"

	NotifyManagerMissingDeliveryArtifact(root, j, "worker-squ-155", reason)

	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("manager messages = %+v, want one notification", messages)
	}
	body := messages[0].Body
	if !strings.Contains(body, "Expected an open PR, pushed branch, or committed diff before accepting done.") {
		t.Fatalf("manager message = %q, want PR expectation", body)
	}
	if strings.Contains(body, "report artifact") {
		t.Fatalf("manager message = %q, should not include report expectation for ticket_to_pr", body)
	}
}

func TestEvent_DeliveryArtifactAllowsLinkedTicketOpenPRWithNewCommit(t *testing.T) {
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	dispatchedAt := time.Date(2026, 7, 7, 12, 0, 0, 500_000_000, time.UTC)
	stubTicketPullRequestGh(t,
		`[{"number":169,"url":"https://github.com/acme/repo/pull/169"}]`,
		fmt.Sprintf(`{"state":"OPEN","commits":[{"committedDate":%q}]}`, dispatchedAt.Add(time.Minute).Format(time.RFC3339)),
	)
	j := &jobstore.Job{
		ID:               "squ-167b",
		Ticket:           "SQU-167",
		Target:           "worker",
		DeliveryContract: deliveryContractBranch,
		CreatedAt:        dispatchedAt.Add(-time.Hour),
		UpdatedAt:        dispatchedAt.Add(-time.Hour),
	}
	meta := &Metadata{
		Instance:  "worker-squ-167b",
		Workspace: repoRoot,
		StartedAt: dispatchedAt,
	}

	if reason := MissingDeliveryArtifactReason(teamDir, j, meta); reason != "" {
		t.Fatalf("missing reason = %q, want linked ticket PR with new commit to count as deliverable", reason)
	}
}

func TestEvent_DeliveryArtifactRejectsLinkedTicketOpenPRWithoutNewCommit(t *testing.T) {
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	dispatchedAt := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	stubTicketPullRequestGh(t,
		`[{"number":169,"url":"https://github.com/acme/repo/pull/169"}]`,
		fmt.Sprintf(`{"state":"OPEN","commits":[{"committedDate":%q}]}`, dispatchedAt.Add(-time.Minute).Format(time.RFC3339)),
	)
	j := &jobstore.Job{
		ID:               "squ-167c",
		Ticket:           "SQU-167",
		Target:           "worker",
		DeliveryContract: deliveryContractBranch,
		CreatedAt:        dispatchedAt.Add(-time.Hour),
		UpdatedAt:        dispatchedAt.Add(-time.Hour),
	}
	meta := &Metadata{
		Instance:  "worker-squ-167c",
		Workspace: repoRoot,
		StartedAt: dispatchedAt,
	}

	reason := MissingDeliveryArtifactReason(teamDir, j, meta)
	if !strings.Contains(reason, "delivery artifact missing") {
		t.Fatalf("missing reason = %q, want stale ticket PR commit to fail the gate", reason)
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
	_ = waitForEventReaper(t, m, "worker-squ-42")
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

	fake := newFakeSpawner(3 * time.Second)
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
	if err := os.WriteFile(filepath.Join(worktreePath, "deliverable.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatalf("write deliverable: %v", err)
	}
	runGit(t, worktreePath, "add", "deliverable.txt")
	runGit(t, worktreePath, "commit", "-m", "add deliverable")

	if err := waitForEventReaper(t, m, "worker-squ-142"); err != nil {
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
	_ = waitForEventReaper(t, m, "worker-squ-42")
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
	writeFixtureRuntimeCommandSkills(t, teamDir, "worker")
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
	call := fake.lastCall()
	addDir, ok := argValue(call, "--add-dir")
	if !ok {
		t.Fatalf("spawn call missing --add-dir value: %#v", call)
	}
	assertLaunchRootUnderRuntime(t, filepath.Join(stateDir, "runtime"), addDir)
	assertEventRuntimeCommandSurface(t, addDir, env)
	if meta, err := ReadMetadata(root, id); err != nil || meta.Workspace != repoRoot {
		t.Fatalf("metadata workspace = %+v err=%v, want repo root %s", meta, err, repoRoot)
	}
	_, _ = m.Stop(id)
	_ = waitForEventReaper(t, m, id)
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
	if err := waitForEventReaper(t, m, id); err != nil {
		t.Fatalf("wait reaper: %v", err)
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

func TestEvent_JobsInFlightBudgetQueuesAndReapDrains(t *testing.T) {
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

[teams.delivery]
instances = ["worker"]

[budgets.delivery]
jobs_in_flight = 1
`)
	fake := newSequencedFakeSpawner(2*time.Second, 30*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	first, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-squ-300",
		"ticket": "SQU-300",
	})
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if len(first.Outcomes) != 1 || first.Outcomes[0].Action != "dispatched" {
		t.Fatalf("first outcomes = %+v", first.Outcomes)
	}
	second, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-squ-301",
		"ticket": "SQU-301",
	})
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if len(second.Outcomes) != 1 || second.Outcomes[0].Action != "queued" || second.Outcomes[0].Reason != QueueReasonBudgetExhausted {
		t.Fatalf("second outcomes = %+v, want budget queue", second.Outcomes)
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 1 || items[0].Reason != QueueReasonBudgetExhausted || !items[0].NextRetry.IsZero() {
		t.Fatalf("queue items = %+v, want budget_exhausted without retry time", items)
	}

	if err := waitForEventReaper(t, m, "worker-squ-300"); err != nil {
		t.Fatalf("wait first reap: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls=%d, want queued budget dispatch to drain", fake.callCount())
	}
	if _, err := ReadQueueItem(root, items[0].ID); !os.IsNotExist(err) {
		t.Fatalf("queued item should be removed after budget frees, err=%v", err)
	}
	_, _ = m.Stop("worker-squ-301")
	_ = waitForEventReaper(t, m, "worker-squ-301")
}

func TestEvent_TokenBudgetQueuesDispatchFromUsageRecords(t *testing.T) {
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

[teams.delivery]
instances = ["worker"]

[budgets.delivery]
tokens_per_day = 100
`)
	now := time.Now().UTC().Truncate(time.Second)
	ended := now.Add(-time.Hour)
	writeBudgetUsageJobForEventTest(t, teamDir, "SQU-299", "delivery", usage.Record{
		Instance:        "worker-squ-299",
		TokensAvailable: true,
		InputTokens:     90,
		OutputTokens:    20,
		StartedAt:       ended.Add(-time.Minute),
		EndedAt:         ended,
	})
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-squ-302",
		"ticket": "SQU-302",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "queued" || result.Outcomes[0].Reason != QueueReasonBudgetExhausted {
		t.Fatalf("outcomes = %+v, want token budget queue", result.Outcomes)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls=%d, want none", fake.callCount())
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	wantRetry := ended.Add(24 * time.Hour)
	if len(items) != 1 || items[0].Reason != QueueReasonBudgetExhausted || !items[0].NextRetry.Equal(wantRetry) {
		t.Fatalf("queue items = %+v, want retry %s", items, wantRetry)
	}
}

func TestEvent_ReserveTokenAllocationQueuesUntilRelease(t *testing.T) {
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

[teams.delivery]
instances = ["worker"]

[budgets.delivery]
tokens_per_day = 100
allocation = "reserve"
`)
	fake := newSequencedFakeSpawner(100*time.Millisecond, 30*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	first, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":        "worker",
		"name":          "worker-squ-410",
		"ticket":        "SQU-410",
		"budget_tokens": int64(60),
	})
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if len(first.Outcomes) != 1 || first.Outcomes[0].Action != "dispatched" {
		t.Fatalf("first outcomes = %+v", first.Outcomes)
	}
	second, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":        "worker",
		"name":          "worker-squ-411",
		"ticket":        "SQU-411",
		"budget_tokens": int64(60),
	})
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if len(second.Outcomes) != 1 || second.Outcomes[0].Action != "queued" || second.Outcomes[0].Reason != QueueReasonBudgetExhausted {
		t.Fatalf("second outcomes = %+v, want budget queue", second.Outcomes)
	}
	rows, err := budget.Statuses(teamDir, top, time.Now().UTC())
	if err != nil {
		t.Fatalf("budget statuses: %v", err)
	}
	if len(rows) != 1 || rows[0].TokensAllocated != 60 || rows[0].TokensRemaining != 40 {
		t.Fatalf("rows before release = %+v, want one outstanding 60-token reserve", rows)
	}
	if err := waitForEventReaper(t, m, "worker-squ-410"); err != nil {
		t.Fatalf("wait first reap: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls=%d, want queued reserve dispatch to drain", fake.callCount())
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	for _, item := range items {
		if item.InstanceID == "worker-squ-411" && item.State == QueueStatePending {
			t.Fatalf("queued reserve item should have drained: %+v", item)
		}
	}
	rows, err = budget.Statuses(teamDir, top, time.Now().UTC())
	if err != nil {
		t.Fatalf("budget statuses after drain: %v", err)
	}
	if len(rows) != 1 || rows[0].TokensAllocated != 60 {
		t.Fatalf("rows after drain = %+v, want second 60-token allocation outstanding", rows)
	}
	_, _ = m.Stop("worker-squ-411")
	_ = waitForEventReaper(t, m, "worker-squ-411")
}

func TestEvent_DispatchLockContentionQueuesAndReapDrains(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`[project]
id = "project-1"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
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

[teams.platform]
instances = ["worker"]
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
	if items[0].Origin.Project != "project-1" || items[0].Origin.Team != "platform" || items[0].Origin.Instance != "worker-squ-101" || items[0].Origin.Trigger != topology.EventAgentDispatch {
		t.Fatalf("queue item origin = %+v", items[0].Origin)
	}
	lease, err := ReadLockLease(root, "build", "worker-squ-100")
	if err != nil {
		t.Fatalf("ReadLockLease: %v", err)
	}
	if lease.Origin.Project != "project-1" || lease.Origin.Team != "platform" || lease.Origin.Instance != "worker-squ-100" {
		t.Fatalf("lock lease origin = %+v", lease.Origin)
	}
	snapshots := resolver.LockSnapshots()
	if len(snapshots) != 1 || snapshots[0].Name != "build" || snapshots[0].Used != 1 || snapshots[0].Holders[0].Instance != "worker-squ-100" {
		t.Fatalf("snapshots = %+v", snapshots)
	}

	if _, err := m.Stop("worker-squ-100"); err != nil {
		t.Fatalf("stop first: %v", err)
	}
	if err := waitForEventReaper(t, m, "worker-squ-100"); err != nil {
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
	_ = waitForEventReaper(t, m, "worker-squ-101")
}

func TestEvent_TeamScopedDispatchLockDoesNotContendAcrossTeams(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[locks.build]
slots = 1
scope = "team"

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 1
locks = ["build"]

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.platform-worker]
agent = "worker"
ephemeral = true
replicas = 1
locks = ["build"]

[[instances.platform-worker.triggers]]
event = "agent.dispatch"
match.target = "platform-worker"

[teams.delivery]
instances = ["worker"]

[teams.platform]
instances = ["platform-worker"]
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	first, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-squ-100",
		"job":    "squ-100",
	})
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	second, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "platform-worker",
		"name":   "platform-worker-squ-101",
		"job":    "squ-101",
	})
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if len(first.Outcomes) != 1 || first.Outcomes[0].Action != "dispatched" {
		t.Fatalf("first outcomes = %+v", first.Outcomes)
	}
	if len(second.Outcomes) != 1 || second.Outcomes[0].Action != "dispatched" {
		t.Fatalf("second outcomes = %+v, want independent team-scoped dispatch", second.Outcomes)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls=%d, want both dispatched", fake.callCount())
	}
	deliveryLease, err := ReadLockLease(root, "team.delivery.build", "worker-squ-100")
	if err != nil {
		t.Fatalf("delivery ReadLockLease: %v", err)
	}
	if deliveryLease.Name != "build" || deliveryLease.Scope != topology.ScopeTeam || deliveryLease.Origin.Team != "delivery" {
		t.Fatalf("delivery lease = %+v", deliveryLease)
	}
	platformLease, err := ReadLockLease(root, "team.platform.build", "platform-worker-squ-101")
	if err != nil {
		t.Fatalf("platform ReadLockLease: %v", err)
	}
	if platformLease.Name != "build" || platformLease.Scope != topology.ScopeTeam || platformLease.Origin.Team != "platform" {
		t.Fatalf("platform lease = %+v", platformLease)
	}
	snapshots := resolver.LockSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %+v, want two scoped lock rows", snapshots)
	}
	storage := []string{snapshots[0].Storage, snapshots[1].Storage}
	sort.Strings(storage)
	if !reflect.DeepEqual(storage, []string{"team.delivery.build", "team.platform.build"}) {
		t.Fatalf("snapshot storage = %v", storage)
	}
	_, _ = m.Stop("worker-squ-100")
	_, _ = m.Stop("platform-worker-squ-101")
	_ = waitForEventReaper(t, m, "worker-squ-100")
	_ = waitForEventReaper(t, m, "platform-worker-squ-101")
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
	if err := waitForEventReaper(t, m, "worker-squ-200"); err != nil {
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
	_ = waitForEventReaper(t, m, "reviewer-squ-201")
}

func TestEvent_ScheduleDispatchGeneratesUniqueChildName(t *testing.T) {
	// A schedule's payload "name" identifies the SCHEDULE (trigger match.name),
	// not a requested instance name — ephemeral dispatch must generate one.
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.feedback-triage]
agent = "worker"
ephemeral = true

[[instances.feedback-triage.triggers]]
event = "schedule"
match.name = "feedback-triage"
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult("schedule", map[string]any{
		"source": "schedule",
		"name":   "feedback-triage",
		"kind":   "feedback_triage",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want dispatched", result.Outcomes)
	}
	child := result.Outcomes[0].InstanceID
	if !strings.HasPrefix(child, "feedback-triage-") {
		t.Fatalf("child name = %q, want generated unique name with declared prefix", child)
	}
	_, _ = m.Stop(child)
	_ = waitForEventReaper(t, m, child)
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
	if err := waitForEventReaper(t, m, "worker-queued-timeout"); err != nil {
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

func TestApplyTopologyReminderDefaultsToPayload(t *testing.T) {
	top := &topology.Topology{ReminderLevels: []int{25, 75, 100}}
	payload := map[string]any{"budget_tokens": int64(1000)}
	applyTopologyReminderDefaultsToPayload(top, payload)
	if !reflect.DeepEqual(payload["reminder_levels"], []int{25, 75, 100}) {
		t.Fatalf("reminder_levels = %+v", payload["reminder_levels"])
	}

	explicit := map[string]any{"budget_tokens": int64(1000), "reminder_levels": []int{50, 100}}
	applyTopologyReminderDefaultsToPayload(top, explicit)
	if !reflect.DeepEqual(explicit["reminder_levels"], []int{50, 100}) {
		t.Fatalf("explicit reminder_levels = %+v", explicit["reminder_levels"])
	}

	noAllowance := map[string]any{"ticket": "SQU-104"}
	applyTopologyReminderDefaultsToPayload(top, noAllowance)
	if _, ok := noAllowance["reminder_levels"]; ok {
		t.Fatalf("set reminder levels without an allowance: %+v", noAllowance)
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
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`[project]
id = "project-1"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
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
model = "claude-sonnet-5"
effort = "high"
token_budget = 80
time_budget = "45m"
reminder_levels = [50, 80, 100]

[[pipelines.ticket_to_pr.steps]]
id = "review"
label = "Manager review"
description = "Review the worker output."
instructions = "Prepare review notes for the implementation branch."
target = "manager"
after = ["implement"]
optional = true
timeout = "2h"

[teams.platform]
instances = ["worker"]
pipelines = ["ticket_to_pr"]

[budgets.platform]
tokens_per_day = 100
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	usedAt := time.Now().UTC().Add(-time.Hour)
	usedRec := usage.Record{
		Instance:        "worker-squ-91",
		Agent:           "worker",
		Runtime:         "codex",
		TokensAvailable: true,
		InputTokens:     70,
		EndedAt:         usedAt,
		Origin:          origin.Envelope{Team: "platform"},
	}
	if err := jobstore.Write(teamDir, &jobstore.Job{
		ID:        "squ-91",
		Ticket:    "SQU-91",
		Target:    "worker",
		Status:    jobstore.StatusDone,
		Origin:    origin.Envelope{Team: "platform"},
		CreatedAt: usedAt,
		UpdatedAt: usedAt,
		Usage: &usage.JobUsage{
			Summary: usage.Summarize([]usage.Record{usedRec}),
			Records: []usage.Record{usedRec},
		},
	}); err != nil {
		t.Fatalf("write used job: %v", err)
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
	if j.Origin.Project != "project-1" || j.Origin.Team != "platform" || j.Origin.Job != "squ-92" || j.Origin.Trigger != "ticket.created" {
		t.Fatalf("job origin = %+v", j.Origin)
	}
	if j.Steps[0].ID != "implement" || j.Steps[0].Instructions != "Implement the ticket with regression coverage." || j.Steps[0].Status != jobstore.StatusRunning || j.Steps[0].Instance != "worker-squ-92" || j.Steps[0].Model != "claude-sonnet-5" || j.Steps[0].Effort != "high" || j.Steps[0].TokenBudget != 30 || j.Steps[0].TimeBudget != "45m0s" || !reflect.DeepEqual(j.Steps[0].ReminderLevels, []int{50, 80, 100}) {
		t.Fatalf("first step = %+v", j.Steps[0])
	}
	if j.Steps[1].ID != "review" || j.Steps[1].Label != "Manager review" || j.Steps[1].Description != "Review the worker output." || j.Steps[1].Instructions != "Prepare review notes for the implementation branch." || !j.Steps[1].Optional || j.Steps[1].Timeout != "2h0m0s" {
		t.Fatalf("optional review step = %+v", j.Steps[1])
	}
	meta, err := ReadMetadata(root, "worker-squ-92")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if meta.Origin.Project != "project-1" || meta.Origin.Team != "platform" || meta.Origin.Instance != "worker-squ-92" || meta.Origin.Trigger != "pipeline:ticket_to_pr:implement" {
		t.Fatalf("metadata origin = %+v", meta.Origin)
	}
	const stepSpecURI = "agt://project-1/job/squ-92#step=implement"
	if meta.SpecURI != stepSpecURI || meta.JobURI != "agt://project-1/job/squ-92" {
		t.Fatalf("metadata URIs = %+v", meta)
	}
	if meta.Model != "claude-sonnet-5" || meta.Effort != "high" {
		t.Fatalf("metadata model/effort = %q/%q, want claude-sonnet-5/high", meta.Model, meta.Effort)
	}
	env := fake.lastEnv()
	if !containsString(env, "AGENT_TEAM_BUDGET_TOKENS=30") || !containsString(env, "AGENT_TEAM_BUDGET_TIME=45m0s") {
		t.Fatalf("env missing clamped budget values: %#v", env)
	}
	if !containsString(env, "AGENT_TEAM_SPEC_URI="+stepSpecURI) {
		t.Fatalf("env missing step spec URI: %#v", env)
	}
	call := fake.lastCall()
	if got, ok := argValue(call, "--model"); !ok || got != "claude-sonnet-5" {
		t.Fatalf("spawn call model = %q, %v; want claude-sonnet-5 in %#v", got, ok, call)
	}
	if got, ok := argValue(call, "--effort"); !ok || got != "high" {
		t.Fatalf("spawn call effort = %q, %v; want high in %#v", got, ok, call)
	}
	prompt, ok := argValue(call, "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", call)
	}
	if !strings.Contains(prompt, `"spec_uri":"`+stepSpecURI+`"`) {
		t.Fatalf("prompt missing step spec URI:\n%s", prompt)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-92")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("events = %+v, want pipeline and clamp events", events)
	}
	foundClamp := false
	for _, event := range events {
		if event.Type != "budget_clamped" {
			continue
		}
		if event.Data["team"] == "platform" && event.Data["requested_tokens"] == "80" && event.Data["clamped_tokens"] == "30" {
			foundClamp = true
		}
	}
	if !foundClamp {
		t.Fatalf("events missing expected clamp: %+v", events)
	}
}

func TestGH403DirectDispatchAdoptionPreservesPipelineOwningTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.alpha-manager]
agent = "manager"

[instances.beta-manager]
agent = "manager"

[[instances.beta-manager.triggers]]
event = "job.step_completed"
match.pipeline = "beta"
match.target = "beta-manager"

[instances.shared-worker]
agent = "worker"
ephemeral = true

[[instances.shared-worker.triggers]]
event = "ticket.created"

[pipelines.beta]
trigger.event = "ticket.created"

[[pipelines.beta.steps]]
id = "implement"
target = "shared-worker"
workspace = "repo"

[[pipelines.beta.steps]]
id = "approve"
target = "beta-manager"
after = ["implement"]
gate = "manual"

[teams.alpha]
instances = ["alpha-manager", "shared-worker"]

[teams.beta]
instances = ["beta-manager", "shared-worker"]
pipelines = ["beta"]

[authority]
enforcement = "enforce"

[authority.instances.alpha-manager]
allow = ["job.bounce:team"]

[authority.instances.beta-manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team", "job.approve:team", "job.reject:team"]
`)
	fake := newFakeSpawner(30 * time.Second)
	mgr := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(mgr, teamDir, top)
	const child = "shared-worker-gh-403-direct"
	defer func() {
		_, _ = mgr.Stop(child)
		_ = waitForEventReaper(t, mgr, child)
	}()

	outcomes, err := resolver.Event("ticket.created", map[string]any{
		"ticket":    "GH-403-DIRECT",
		"name":      child,
		"kickoff":   "exercise direct dispatch adoption",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("Event: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Action != "dispatched" || outcomes[0].InstanceID != child {
		t.Fatalf("outcomes = %+v, want adopted direct dispatch", outcomes)
	}

	j, err := jobstore.Read(teamDir, "gh-403-direct")
	if err != nil {
		t.Fatalf("read adopted pipeline job: %v", err)
	}
	if j.Pipeline != "beta" {
		t.Fatalf("adopted job pipeline = %q, want beta", j.Pipeline)
	}
	if j.Origin.Team != "beta" {
		t.Fatalf("adopted pipeline job origin team = %q, want beta; origin=%+v", j.Origin.Team, j.Origin)
	}

	if err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     origin.Envelope{Team: "beta", Instance: "beta-manager", Agent: "manager"},
		Verb:      "job.bounce",
		TargetJob: j.ID,
	}); err != nil {
		t.Fatalf("validated beta manager denied its adopted beta pipeline job: %v", err)
	}
	if err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     origin.Envelope{Team: "alpha", Instance: "alpha-manager", Agent: "manager"},
		Verb:      "job.bounce",
		TargetJob: j.ID,
	}); err == nil {
		t.Fatal("cross-team alpha manager authorized for adopted beta pipeline job")
	}
}

func TestEvent_OriginAgentIsResolvedTemplateNotTargetAlias(t *testing.T) {
	// SQU-92 round-3 finding: a declared instance alias (platform-reviewer)
	// must stamp origin.agent with its resolved agent template (reviewer) so
	// agent-type-keyed authority allowlists apply; payload target is not
	// identity.
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "reviewer")
	top := mustParseCustomTopo(t, `
[instances.platform-reviewer]
agent = "reviewer"
ephemeral = true

[[instances.platform-reviewer.triggers]]
event = "agent.dispatch"
match.target = "platform-reviewer"

[teams.platform]
instances = ["platform-reviewer"]
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target":  "platform-reviewer",
		"name":    "platform-reviewer-x1",
		"kickoff": "review something",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v", result.Outcomes)
	}
	meta, err := ReadMetadata(root, "platform-reviewer-x1")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Origin.Agent != "reviewer" || meta.Origin.Team != "platform" {
		t.Fatalf("origin = %+v, want agent=reviewer team=platform", meta.Origin)
	}
	_, _ = m.Stop("platform-reviewer-x1")
	_ = waitForEventReaper(t, m, "platform-reviewer-x1")
}

func TestEvent_PipelineDispatchOriginIgnoresPayloadTeam(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`[project]
id = "project-1"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	top, err := topology.Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
instructions = "Implement the ticket."
target = "worker"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult("ticket.status_changed", map[string]any{
		"ticket":     "SQU-93",
		"ticket_url": "https://linear.app/squirtlesquad/issue/SQU-93/pipeline",
		"team":       "SQU",
		"status":     "Ready for Agent",
		"workspace":  "repo",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("outcomes = %+v, want one dispatched worker", result.Outcomes)
	}
	if id := result.Outcomes[0].InstanceID; id != "worker-squ-93" {
		t.Fatalf("instance_id = %q, want worker-squ-93", id)
	}
	j, err := jobstore.Read(teamDir, "squ-93")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Origin.Project != "project-1" || j.Origin.Team != "delivery" || j.Origin.Job != "squ-93" || j.Origin.Trigger != "ticket.status_changed" {
		t.Fatalf("job origin = %+v", j.Origin)
	}
	meta, err := ReadMetadata(root, "worker-squ-93")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if meta.Origin.Project != "project-1" || meta.Origin.Team != "delivery" || meta.Origin.Instance != "worker-squ-93" || meta.Origin.Trigger != "pipeline:ticket_to_pr:implement" {
		t.Fatalf("metadata origin = %+v", meta.Origin)
	}
}

func TestEvent_DocsFreshnessScheduleDispatchesDurableWorktreeJob(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`
[pm]
provider = "github"

[github]
owner = "agent-team-project"
repo = "kensho"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.docs-manager]
agent = "manager"

[[instances.docs-manager.triggers]]
event = "job.completed"
match.pipeline = "docs_freshness"

[instances.docs-writer]
agent = "worker"
ephemeral = true

[[instances.docs-writer.triggers]]
event = "agent.dispatch"
match.target = "docs-writer"

[schedules.docs-freshness]
every = "6h"

[schedules.docs-freshness.payload]
kind = "docs_freshness"
ticket = "GH-228"
ticket_url = "https://github.com/agent-team-project/kensho/issues/228"
deliverable = "none"
kickoff = "Scheduled docs-freshness audit for GitHub issue #228."

[pipelines.docs_freshness]
trigger.event = "schedule"
trigger.match.name = "docs-freshness"
trigger.match.kind = "docs_freshness"
redispatch_on_reentry = true
reap_worktree = "on_close"

[[pipelines.docs_freshness.steps]]
id = "audit"
target = "docs-writer"
workspace = "worktree"
timeout = "45m"
token_budget = "40M"
time_budget = "45m"
max_attempts = 1

[teams.docs]
instances = ["docs-manager", "docs-writer"]
pipelines = ["docs_freshness"]
schedules = ["docs-freshness"]
`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	payload := top.Schedules["docs-freshness"].EventPayload()
	result, err := resolver.EventWithResult(topology.EventSchedule, payload)
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if got := result.Trace.MatchedInstanceNames(); len(got) != 0 {
		t.Fatalf("matched direct instances = %+v, want none", got)
	}
	if got := result.Trace.MatchedPipelineNames(); !reflect.DeepEqual(got, []string{"docs_freshness"}) {
		t.Fatalf("matched pipelines = %+v, want docs_freshness", got)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" || result.Outcomes[0].Instance != "docs-writer" || result.Outcomes[0].InstanceID != "docs-writer-gh-228" {
		t.Fatalf("outcomes = %+v, want dispatched docs-writer-gh-228", result.Outcomes)
	}
	if result.Outcomes[0].JobID != "gh-228" || result.Outcomes[0].Pipeline != "docs_freshness" || result.Outcomes[0].Step != "audit" {
		t.Fatalf("outcome context = %+v, want job/pipeline/step context", result.Outcomes[0])
	}
	t.Cleanup(func() {
		_, _ = m.Stop("docs-writer-gh-228")
		_ = m.WaitForReaper("docs-writer-gh-228", 5*time.Second)
	})

	meta, err := ReadMetadata(root, "docs-writer-gh-228")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if meta.Workspace == repoRoot || !strings.Contains(meta.Workspace, filepath.Join(".claude", "worktrees")) {
		t.Fatalf("workspace = %q, want isolated worktree under .claude/worktrees", meta.Workspace)
	}
	if meta.Branch == "" || meta.Branch == "main" || !strings.HasPrefix(meta.Branch, "gh-228-") {
		t.Fatalf("branch = %q, want gh-228-* worktree branch", meta.Branch)
	}
	if meta.Job != "gh-228" || meta.Ticket != "GH-228" {
		t.Fatalf("metadata job/ticket = %q/%q, want gh-228/GH-228", meta.Job, meta.Ticket)
	}
	if meta.Origin.Team != "docs" || meta.Origin.Trigger != "pipeline:docs_freshness:audit" {
		t.Fatalf("metadata origin = %+v", meta.Origin)
	}

	env := fake.lastEnv()
	if got := lastEnvValue(env, "AGENT_TEAM_JOB_ID"); got != "gh-228" {
		t.Fatalf("AGENT_TEAM_JOB_ID = %q, want gh-228; env=%v", got, env)
	}
	if got := lastEnvValue(env, "AGENT_TEAM_TICKET"); got != "GH-228" {
		t.Fatalf("AGENT_TEAM_TICKET = %q, want GH-228; env=%v", got, env)
	}
	if got := lastEnvValue(env, "AGENT_TEAM_TICKET_URL"); got != "https://github.com/agent-team-project/kensho/issues/228" {
		t.Fatalf("AGENT_TEAM_TICKET_URL = %q, want stale-docs issue URL; env=%v", got, env)
	}
	if got := lastEnvValue(env, "AGENT_TEAM_PIPELINE"); got != "docs_freshness" {
		t.Fatalf("AGENT_TEAM_PIPELINE = %q, want docs_freshness; env=%v", got, env)
	}
	if got := lastEnvValue(env, "AGENT_TEAM_PIPELINE_STEP"); got != "audit" {
		t.Fatalf("AGENT_TEAM_PIPELINE_STEP = %q, want audit; env=%v", got, env)
	}
	if got := lastEnvValue(env, "AGENT_TEAM_BRANCH"); got != meta.Branch {
		t.Fatalf("AGENT_TEAM_BRANCH = %q, want metadata branch %q; env=%v", got, meta.Branch, env)
	}
	if got := lastEnvValue(env, "AGENT_TEAM_WORKTREE"); got != meta.Workspace {
		t.Fatalf("AGENT_TEAM_WORKTREE = %q, want metadata workspace %q; env=%v", got, meta.Workspace, env)
	}

	j, err := jobstore.Read(teamDir, "gh-228")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Ticket != "GH-228" || j.TicketURL != "https://github.com/agent-team-project/kensho/issues/228" {
		t.Fatalf("job PM context = %q/%q, want GH-228/#228", j.Ticket, j.TicketURL)
	}
	if j.Pipeline != "docs_freshness" || j.DeliveryContract != "none" || j.ReapWorktree != worktreepolicy.OnClose {
		t.Fatalf("job pipeline/contract/reap = %q/%q/%q, want docs_freshness/none/on_close", j.Pipeline, j.DeliveryContract, j.ReapWorktree)
	}
	if len(j.Steps) != 1 || j.Steps[0].ID != "audit" || j.Steps[0].Target != "docs-writer" || j.Steps[0].Workspace != "worktree" || j.Steps[0].Status != jobstore.StatusRunning {
		t.Fatalf("job steps = %+v, want running docs-writer worktree audit step", j.Steps)
	}
	if j.Worktree != meta.Workspace || j.Branch != meta.Branch {
		t.Fatalf("job worktree/branch = %q/%q, want metadata %q/%q", j.Worktree, j.Branch, meta.Workspace, meta.Branch)
	}
	if j.Contract == nil || j.Contract.WorkItem != "https://github.com/agent-team-project/kensho/issues/228" || j.Contract.Deliverable != "none" {
		t.Fatalf("job contract = %+v, want issue #228 with no PR deliverable", j.Contract)
	}
}

func TestEvent_PipelineInitialDispatchRejectionWritesLinearFailureAttention(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_USER_API_KEY", "")
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`
[pm]
provider = "linear"

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
			ev.Data["action"] == string(pmprovider.ActionFailureAttention) &&
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
		_ = waitForEventReaper(t, m, "worker-squ-92")
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
	if err := waitForEventReaper(t, m, "worker-squ-501"); err != nil {
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

func TestEvent_PipelineStepTimeBudgetHardKillsBusyWorker(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.budgeted]
trigger.event = "ticket.created"

[[pipelines.budgeted.steps]]
id = "implement"
target = "worker"
timeout = "30s"
time_budget = "100ms"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	start := time.Now()
	result, err := resolver.EventWithResult("ticket.created", map[string]any{
		"ticket":    "SQU-502",
		"kickoff":   "implement SQU-502",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" || result.Outcomes[0].InstanceID != "worker-squ-502" {
		t.Fatalf("outcomes = %+v, want dispatched worker-squ-502", result.Outcomes)
	}
	if err := waitForEventReaper(t, m, "worker-squ-502"); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("time budget watchdog took too long: %s", elapsed)
	}
	meta, err := ReadMetadata(root, "worker-squ-502")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.RuntimeBudget != "100ms" || meta.RuntimeDeadline.IsZero() || meta.Status != StatusCrashed {
		t.Fatalf("metadata = %+v, want crashed with 100ms runtime budget", meta)
	}
	j, err := jobstore.Read(teamDir, "squ-502")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.LastEvent != managerOverdueWakeEventType || !strings.Contains(j.LastStatus, "exceeded time budget") {
		t.Fatalf("job last event/status = %q/%q, want time budget exceeded", j.LastEvent, j.LastStatus)
	}
	if len(j.Steps) != 1 || j.Steps[0].Status != jobstore.StatusFailed {
		t.Fatalf("job steps after time budget kill = %+v, want failed implement", j.Steps)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-502")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if !jobEventsContain(events, managerOverdueWakeEventType, j.LastStatus) {
		t.Fatalf("job events missing %s: %+v", managerOverdueWakeEventType, events)
	}
}

func TestEvent_PipelineStepWithinTimeBudgetUntouched(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.budgeted]
trigger.event = "ticket.created"

[[pipelines.budgeted.steps]]
id = "implement"
target = "worker"
timeout = "10s"
time_budget = "3s"
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	fake := newFakeSpawner(eventShortFakeRuntime)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.EventWithResult("ticket.created", map[string]any{
		"ticket":    "SQU-503",
		"kickoff":   "implement SQU-503",
		"workspace": "repo",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" || result.Outcomes[0].InstanceID != "worker-squ-503" {
		t.Fatalf("outcomes = %+v, want dispatched worker-squ-503", result.Outcomes)
	}
	if err := waitForEventReaper(t, m, "worker-squ-503"); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	meta, err := ReadMetadata(root, "worker-squ-503")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.RuntimeBudget != "3s" || meta.RuntimeDeadline.IsZero() {
		t.Fatalf("metadata budget = %+v, want 3s runtime budget", meta)
	}
	if meta.Status != StatusExited {
		t.Fatalf("metadata status = %s, want exited", meta.Status)
	}
	j, err := jobstore.Read(teamDir, "squ-503")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.LastEvent == managerOverdueWakeEventType {
		t.Fatalf("job unexpectedly timed out: %+v", j)
	}
	if j.Status != jobstore.StatusDone || len(j.Steps) != 1 || j.Steps[0].Status != jobstore.StatusDone {
		t.Fatalf("job after within-budget exit = %+v, want done", j)
	}
}

func TestEvent_PipelineInitialManualGateBlocksWithoutDispatch(t *testing.T) {
	root := t.TempDir()
	teamDir := fixtureTeamDir(t)
	top, err := topology.Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "job.step_completed"
match.target = "manager"

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
target = "manager"
gate = "manual"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
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

func TestEvent_TeamScopedSchedulePersistsScopedState(t *testing.T) {
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
scope = "team"

[teams.platform]
instances = ["manager"]
schedules = ["nightly"]
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	m := NewInstanceManager(root, nil)
	resolver := NewEventResolver(m, teamDir, top)
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	fired, err := resolver.FireDueSchedulesWithResult(now)
	if err != nil {
		t.Fatalf("FireDueSchedulesWithResult: %v", err)
	}
	if fired.Fired != 1 || len(fired.Schedules) != 1 || fired.Schedules[0].Name != "nightly" {
		t.Fatalf("fired = %+v", fired)
	}
	persisted, err := ReadScheduleState(root, "team.platform.nightly")
	if err != nil {
		t.Fatalf("ReadScheduleState scoped: %v", err)
	}
	if persisted.Name != "team.platform.nightly" || !persisted.LastFiredAt.Equal(now) {
		t.Fatalf("persisted = %+v", persisted)
	}
	if _, err := ReadScheduleState(root, "nightly"); !os.IsNotExist(err) {
		t.Fatalf("unscoped schedule state changed, err=%v", err)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"name":"nightly"`) {
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
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	root := t.TempDir()
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
