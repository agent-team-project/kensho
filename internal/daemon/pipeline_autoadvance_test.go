package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
)

// autoAdvanceTeamDir is a fixture team with both a worker and a reviewer agent,
// so a two-step pipeline can spawn distinct ephemeral instances per step.
func autoAdvanceTeamDir(t *testing.T) string {
	t.Helper()
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "reviewer")
	return teamDir
}

const autoAdvancePipelineTOML = `
[instances.worker]
agent = "worker"
ephemeral = true
[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.reviewer]
agent = "reviewer"
ephemeral = true
[[instances.reviewer.triggers]]
event = "agent.dispatch"
match.target = "reviewer"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
auto_advance = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
after = ["implement"]
`

// Happy path: when a pipeline opts into auto_advance, the daemon dispatches the
// next ready step as soon as the prior step's instance exits — and threads the
// prior step's last-message sidecar into the next step's kickoff.
func TestEvent_PipelineAutoAdvanceDispatchesNextStep(t *testing.T) {
	root := t.TempDir()
	teamDir := autoAdvanceTeamDir(t)
	top := mustParseCustomTopo(t, autoAdvancePipelineTOML)

	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-92","kickoff":"implement SQU-92","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}

	// Seed the worker's final-message sidecar before it exits, to exercise
	// context passing into the auto-advanced reviewer step.
	sidecar := filepath.Join(teamDir, "state", "worker-squ-92", runtimebin.CodexLastMessageFile)
	if err := os.MkdirAll(filepath.Dir(sidecar), 0o755); err != nil {
		t.Fatalf("mkdir sidecar: %v", err)
	}
	if err := os.WriteFile(sidecar, []byte("Opened PR #999 with new scenarios."), 0o644); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	if err := m.WaitForReaper("worker-squ-92", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls=%d, want 2 (auto-advance should dispatch review)", fake.callCount())
	}

	j, err := jobstore.Read(teamDir, "squ-92")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Steps[0].Status != jobstore.StatusDone {
		t.Fatalf("implement step = %+v, want done", j.Steps[0])
	}
	if j.Steps[1].Status != jobstore.StatusRunning || j.Steps[1].Instance != "reviewer-squ-92" {
		t.Fatalf("review step = %+v, want running on reviewer-squ-92", j.Steps[1])
	}

	// The reviewer's prompt (JSON-encoded payload) must carry the worker's output.
	combined := strings.Join(fake.lastCall(), " ") + fake.lastStdin()
	if !strings.Contains(combined, "Output from previous step") || !strings.Contains(combined, "Opened PR #999") {
		t.Fatalf("reviewer kickoff missing prior-step context; got: %s", combined)
	}

	// Let the reviewer finish; the pipeline should then be done.
	if err := m.WaitForReaper("reviewer-squ-92", 5*time.Second); err != nil {
		t.Fatalf("wait reviewer reaper: %v", err)
	}
	j, err = jobstore.Read(teamDir, "squ-92")
	if err != nil {
		t.Fatalf("re-read job: %v", err)
	}
	if j.Status != jobstore.StatusDone {
		t.Fatalf("job status=%s, want done; steps=%+v", j.Status, j.Steps)
	}
}

// Gate stop: auto-advance must NOT cross a manual gate — the gated step stays
// blocked for `agent-team job approve`, and nothing new spawns.
func TestEvent_PipelineAutoAdvanceStopsAtManualGate(t *testing.T) {
	root := t.TempDir()
	teamDir := autoAdvanceTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true
[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.reviewer]
agent = "reviewer"
ephemeral = true
[[instances.reviewer.triggers]]
event = "agent.dispatch"
match.target = "reviewer"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
auto_advance = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "approve"
target = "reviewer"
after = ["implement"]
gate = "manual"
`)

	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-93","kickoff":"implement SQU-93","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := m.WaitForReaper("worker-squ-93", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1 (manual gate must block auto-advance)", fake.callCount())
	}
	j, err := jobstore.Read(teamDir, "squ-93")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Steps[0].Status != jobstore.StatusDone {
		t.Fatalf("implement step = %+v, want done", j.Steps[0])
	}
	if j.Steps[1].Status != jobstore.StatusBlocked || j.Steps[1].Instance != "" {
		t.Fatalf("gated step = %+v, want blocked+undispatched", j.Steps[1])
	}
	if j.Status == jobstore.StatusDone {
		t.Fatalf("job should not be done while gated; got %s", j.Status)
	}
}

// Opt-out: without auto_advance, the prior behavior is preserved — the next step
// stays blocked until a manual `agent-team pipeline tick`/advance.
func TestEvent_PipelineWithoutAutoAdvanceDoesNotAdvance(t *testing.T) {
	root := t.TempDir()
	teamDir := autoAdvanceTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true
[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.reviewer]
agent = "reviewer"
ephemeral = true
[[instances.reviewer.triggers]]
event = "agent.dispatch"
match.target = "reviewer"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
after = ["implement"]
`)

	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-94","kickoff":"implement SQU-94","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := m.WaitForReaper("worker-squ-94", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want 1 (no auto_advance ⇒ no auto-dispatch)", fake.callCount())
	}
	j, err := jobstore.Read(teamDir, "squ-94")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Steps[1].Status != jobstore.StatusBlocked || j.Steps[1].Instance != "" {
		t.Fatalf("review step = %+v, want still blocked+undispatched without auto_advance", j.Steps[1])
	}
}
