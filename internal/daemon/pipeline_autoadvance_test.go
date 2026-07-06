package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
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
timeout = "2m"
`

// Happy path: when a pipeline opts into auto_advance, the daemon dispatches the
// next ready step as soon as the prior step's instance exits — and threads the
// prior step's last-message sidecar into the next step's kickoff.
func TestEvent_PipelineAutoAdvanceDispatchesNextStep(t *testing.T) {
	root := t.TempDir()
	teamDir := autoAdvanceTeamDir(t)
	top := mustParseCustomTopo(t, autoAdvancePipelineTOML)

	fake := newSequencedFakeSpawner(time.Second, 3*time.Second)
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
	reviewerMeta, err := ReadMetadata(root, "reviewer-squ-92")
	if err != nil {
		t.Fatalf("read reviewer metadata: %v", err)
	}
	if reviewerMeta.RuntimeBudget != "2m0s" || reviewerMeta.RuntimeDeadline.IsZero() {
		t.Fatalf("reviewer metadata = %+v, want 2m budget with deadline", reviewerMeta)
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
	seedPushedBranchArtifact(t, teamDir, "squ-92")

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

func TestEvent_ProbePipelineSkipsReviewAndCompletesOnImplementExit(t *testing.T) {
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
id = "review"
target = "reviewer"
after = ["implement"]

[[pipelines.ticket_to_pr.steps]]
id = "approve"
target = "reviewer"
after = ["review"]
gate = "manual"
`)

	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-94","kickoff":"measure SQU-94","kind":"probe","workspace":"worktree"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := m.WaitForReaper("worker-squ-94", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want only probe implement step", fake.callCount())
	}
	if _, err := ReadMetadata(root, "reviewer-squ-94"); !os.IsNotExist(err) {
		t.Fatalf("reviewer metadata err=%v, want not spawned", err)
	}
	j, err := jobstore.Read(teamDir, "squ-94")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Kind != jobstore.KindProbe || j.Status != jobstore.StatusDone {
		t.Fatalf("probe job = %+v, want done probe", j)
	}
	if j.Branch != "" || j.Worktree != "" {
		t.Fatalf("probe job recorded branch/worktree: %+v", j)
	}
	if j.Steps[0].Status != jobstore.StatusDone {
		t.Fatalf("implement step = %+v, want done", j.Steps[0])
	}
	if len(j.Steps) != 3 {
		t.Fatalf("steps = %+v, want implement/review/approve", j.Steps)
	}
	for _, step := range j.Steps[1:] {
		if step.Status != jobstore.StatusDone || !step.Skipped || step.SkipReason != jobstore.ProbeSkipReason {
			t.Fatalf("downstream step = %+v, want skipped done", step)
		}
	}
	if !strings.Contains(j.LastStatus, "probe completed") {
		t.Fatalf("last status = %q, want probe artifact hint", j.LastStatus)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("manager messages = %+v, want none for probe", messages)
	}
}

func TestEvent_TicketToPRPipelineDoneWithoutDeliverableFailsAndMessagesManager(t *testing.T) {
	root := t.TempDir()
	teamDir := autoAdvanceTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true
[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
auto_advance = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`)

	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-155","kickoff":"implement SQU-155","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := m.WaitForReaper("worker-squ-155", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	j, err := jobstore.Read(teamDir, "squ-155")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Status != jobstore.StatusFailed || j.LastEvent != "deliverable_missing" {
		t.Fatalf("job = %+v, want failed deliverable_missing", j)
	}
	if len(j.Steps) != 1 || j.Steps[0].Status != jobstore.StatusFailed {
		t.Fatalf("steps = %+v, want failed implement step", j.Steps)
	}
	if !strings.Contains(j.LastStatus, "delivery artifact missing") {
		t.Fatalf("last status = %q, want clear missing-artifact reason", j.LastStatus)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, "squ-155") || !strings.Contains(messages[0].Body, "delivery artifact missing") {
		t.Fatalf("manager messages = %+v, want missing-deliverable notification", messages)
	}
}

func TestEvent_TicketToPRPipelineDoneWithBogusPRStillFails(t *testing.T) {
	root := t.TempDir()
	teamDir := autoAdvanceTeamDir(t)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true
[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
auto_advance = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`)

	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-156","kickoff":"implement SQU-156","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	j, err := jobstore.Read(teamDir, "squ-156")
	if err != nil {
		t.Fatalf("read running job: %v", err)
	}
	j.PR = "https://github.com/acme/repo/pull/999999"
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write bogus PR: %v", err)
	}
	if err := m.WaitForReaper("worker-squ-156", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	j, err = jobstore.Read(teamDir, "squ-156")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Status != jobstore.StatusFailed || j.LastEvent != "deliverable_missing" {
		t.Fatalf("job = %+v, want failed deliverable_missing despite bogus PR URL", j)
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

func TestEvent_PipelineAutoRetriesReviewCrashWithoutGateOnce(t *testing.T) {
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
max_attempts = 1

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
after = ["implement"]
max_attempts = 1
retry_on_crash = true
`)

	fake := newSequencedFakeSpawner(time.Second, 30*time.Second, 30*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-172","kickoff":"implement SQU-172","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := m.WaitForReaper("worker-squ-172", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls after implement=%d, want review dispatched", fake.callCount())
	}
	killInstance(t, root, "reviewer-squ-172")
	if err := m.WaitForReaper("reviewer-squ-172", 5*time.Second); err != nil {
		t.Fatalf("wait first reviewer reaper: %v", err)
	}
	if fake.callCount() != 3 {
		t.Fatalf("spawn calls after review crash=%d, want one auto-retry", fake.callCount())
	}

	j, err := jobstore.Read(teamDir, "squ-172")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Steps[1].Status != jobstore.StatusRunning || j.Steps[1].Attempts != 2 || !j.Steps[1].RetryOnCrash {
		t.Fatalf("review step after retry = %+v, want running attempts=2 retry_on_crash", j.Steps[1])
	}
	events, err := jobstore.ListEvents(teamDir, "squ-172")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if !jobEventsContain(events, "pipeline_advanced", "auto-retried crashed step review") {
		t.Fatalf("events missing auto-retry record: %+v", events)
	}

	killInstance(t, root, "reviewer-squ-172")
	if err := m.WaitForReaper("reviewer-squ-172", 5*time.Second); err != nil {
		t.Fatalf("wait second reviewer reaper: %v", err)
	}
	if fake.callCount() != 3 {
		t.Fatalf("spawn calls after second crash=%d, want no second auto-retry", fake.callCount())
	}
}

func TestEvent_PipelineDoesNotAutoRetryCrashWithRecordedGate(t *testing.T) {
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
id = "review"
target = "reviewer"
after = ["implement"]
retry_on_crash = true
`)

	fake := newSequencedFakeSpawner(time.Second, 30*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-173","kickoff":"implement SQU-173","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := m.WaitForReaper("worker-squ-173", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if err := jobstore.AppendGateRecord(teamDir, &jobstore.GateRecord{
		JobID:  "squ-173",
		Name:   "review",
		Status: jobstore.GateStatusFail,
		Actor:  "reviewer-squ-173",
	}); err != nil {
		t.Fatalf("append gate: %v", err)
	}
	killInstance(t, root, "reviewer-squ-173")
	if err := m.WaitForReaper("reviewer-squ-173", 5*time.Second); err != nil {
		t.Fatalf("wait reviewer reaper: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls after gated crash=%d, want no auto-retry", fake.callCount())
	}
	j, err := jobstore.Read(teamDir, "squ-173")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Steps[1].Status != jobstore.StatusFailed || j.Steps[1].Attempts != 1 {
		t.Fatalf("review step after gated crash = %+v, want failed attempts=1", j.Steps[1])
	}
}

func TestEvent_PipelineDoesNotAutoRetryImplementationCrash(t *testing.T) {
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
max_attempts = 2

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
after = ["implement"]
retry_on_crash = true
`)

	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-174","kickoff":"implement SQU-174","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	killInstance(t, root, "worker-squ-174")
	if err := m.WaitForReaper("worker-squ-174", 5*time.Second); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls after implement crash=%d, want no auto-retry despite max_attempts", fake.callCount())
	}
	j, err := jobstore.Read(teamDir, "squ-174")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if j.Steps[0].Status != jobstore.StatusFailed || j.Steps[0].Attempts != 1 {
		t.Fatalf("implement step after crash = %+v, want failed attempts=1", j.Steps[0])
	}
}

func killInstance(t *testing.T, root, instance string) {
	t.Helper()
	meta, err := ReadMetadata(root, instance)
	if err != nil {
		t.Fatalf("read metadata for %s: %v", instance, err)
	}
	if err := syscall.Kill(meta.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill %s pid %d: %v", instance, meta.PID, err)
	}
}

func jobEventsContain(events []jobstore.Event, typ, message string) bool {
	for _, ev := range events {
		if ev.Type == typ && ev.Message == message {
			return true
		}
	}
	return false
}
