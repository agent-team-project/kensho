package daemon

import (
	"crypto/sha256"
	"encoding/json"
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
	reviewBranch := "squ-92-artifact"
	reviewWorktree := filepath.Dir(teamDir)
	j, err := jobstore.Read(teamDir, "squ-92")
	if err != nil {
		t.Fatalf("read job before auto-advance: %v", err)
	}
	j.Branch = reviewBranch
	j.Worktree = reviewWorktree
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job branch context: %v", err)
	}

	if err := waitForEventReaper(t, m, "worker-squ-92"); err != nil {
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

	env := fake.lastEnv()
	for _, want := range []string{
		"MAIN_REPO=" + filepath.Dir(teamDir),
		"AGENT_TEAM_PIPELINE_STEP=review",
		"AGENT_TEAM_BRANCH=" + reviewBranch,
		"AGENT_TEAM_WORKTREE=" + reviewWorktree,
	} {
		if !containsString(env, want) {
			t.Fatalf("reviewer env missing %q: %#v", want, env)
		}
	}
	snapshot, err := ReadInstanceLaunchEnv(root, "reviewer-squ-92")
	if err != nil {
		t.Fatalf("read reviewer launch env: %v", err)
	}
	for _, want := range []string{
		"MAIN_REPO=" + filepath.Dir(teamDir),
		"AGENT_TEAM_PIPELINE_STEP=review",
		"AGENT_TEAM_BRANCH=" + reviewBranch,
		"AGENT_TEAM_WORKTREE=" + reviewWorktree,
	} {
		if !containsString(snapshot.Env, want) {
			t.Fatalf("reviewer launch env missing %q: %#v", want, snapshot.Env)
		}
	}

	j, err = jobstore.Read(teamDir, "squ-92")
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
	if err := waitForEventReaper(t, m, "reviewer-squ-92"); err != nil {
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

func TestEvent_PipelineStepFailureSurvivesCleanRuntimeExitAndPreservesRetryEvidence(t *testing.T) {
	teamDir := autoAdvanceTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	repoRoot := filepath.Dir(teamDir)
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	runGit(t, repoRoot, "checkout", "-B", "main")
	reportRel := filepath.Join("reports", "study-preregistration.md")
	reportBody := []byte("# Preregistration\n\nProduct verdict and Kensho/process verdict remain separate.\n")
	if err := os.MkdirAll(filepath.Join(repoRoot, "reports"), 0o755); err != nil {
		t.Fatalf("mkdir reports: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, reportRel), reportBody, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	runGit(t, repoRoot, "add", reportRel)
	runGit(t, repoRoot, "commit", "-m", "seed immutable report")

	root := DaemonRoot(teamDir)
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	if err := WriteMetadata(root, &Metadata{
		Instance:  "research-manager",
		Agent:     "manager",
		Workspace: repoRoot,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
		Status:    StatusRunning,
	}); err != nil {
		t.Fatalf("write research-manager metadata: %v", err)
	}
	if err := reconcileCrashOnly(root, m, "", nil); err != nil {
		t.Fatalf("adopt research-manager metadata: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.research-manager]
agent = "manager"
ephemeral = false

[[instances.research-manager.triggers]]
event = "job.completed"
match.pipeline = "research_study"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.research_study]
trigger.event = "research.study.requested"
auto_advance = true
reap_worktree = "on_close"

[[pipelines.research_study.steps]]
id = "verify"
target = "worker"
workspace = "repo"

[[pipelines.research_study.steps]]
id = "review"
target = "worker"
after = ["verify"]

[teams.research]
instances = ["research-manager", "worker"]
pipelines = ["research_study"]
`)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"research.study.requested","payload":{"ticket":"RESEARCH-001","kind":"report","deliverable":"report:reports/study-preregistration.md","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}

	worktree := filepath.Join(repoRoot, ".claude", "worktrees", "research-001-report")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatalf("mkdir worktree parent: %v", err)
	}
	branch := "research-001-report"
	runGit(t, repoRoot, "worktree", "add", "-b", branch, worktree)
	wantReportDigest := sha256.Sum256(reportBody)

	j, err := jobstore.Read(teamDir, "research-001")
	if err != nil {
		t.Fatalf("read running research job: %v", err)
	}
	if len(j.Steps) != 2 || j.Steps[0].Instance == "" {
		t.Fatalf("research job steps = %+v, want running verify owner", j.Steps)
	}
	verifierInstance := j.Steps[0].Instance
	now := time.Now().UTC()
	j.Worktree = worktree
	j.Branch = branch
	j.Steps[0].Status = jobstore.StatusFailed
	j.Steps[0].FinishedAt = now
	j.Status = jobstore.StatusFailed
	j.LastEvent = "step_failed"
	j.LastStatus = "verify fail: report contract gate failed"
	j.UpdatedAt = now
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("record failed verifier step: %v", err)
	}
	if err := jobstore.AppendGateRecord(teamDir, &jobstore.GateRecord{
		TS:        now,
		JobID:     j.ID,
		Name:      "report-preregistration-contract",
		Status:    jobstore.GateStatusFail,
		Signature: "missing required section",
		Actor:     verifierInstance,
	}); err != nil {
		t.Fatalf("record verifier gate: %v", err)
	}

	if err := waitForEventReaper(t, m, verifierInstance); err != nil {
		t.Fatalf("wait verifier reaper: %v", err)
	}
	updated, err := jobstore.Read(teamDir, j.ID)
	if err != nil {
		t.Fatalf("read reaped research job: %v", err)
	}
	if updated.Status != jobstore.StatusFailed || updated.Steps[0].Status != jobstore.StatusFailed {
		t.Fatalf("job/verify status = %s/%s, want failed/failed; steps=%+v", updated.Status, updated.Steps[0].Status, updated.Steps)
	}
	if updated.Steps[1].Status == jobstore.StatusDone {
		t.Fatalf("review step = %+v, must not complete after verifier failure", updated.Steps[1])
	}
	if updated.Worktree != worktree || updated.Branch != branch {
		t.Fatalf("retry source = %q/%q, want preserved %q/%q", updated.Worktree, updated.Branch, worktree, branch)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("failed-job worktree was reaped: %v", err)
	}
	preservedReport, err := os.ReadFile(filepath.Join(worktree, reportRel))
	if err != nil {
		t.Fatalf("read preserved report: %v", err)
	}
	if got := sha256.Sum256(preservedReport); got != wantReportDigest {
		t.Fatalf("preserved report digest = %x, want %x", got, wantReportDigest)
	}
	gates, err := jobstore.ListGateRecords(teamDir, j.ID)
	if err != nil {
		t.Fatalf("read preserved gates: %v", err)
	}
	if len(gates) != 1 || gates[0].Status != jobstore.GateStatusFail || gates[0].Actor != verifierInstance {
		t.Fatalf("gate evidence = %+v, want verifier failure", gates)
	}

	messages, err := ReadMessages(root, "research-manager")
	if err != nil {
		t.Fatalf("read research-manager messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("research-manager messages = %+v, want one job.completed wake", messages)
	}
	var wake struct {
		Event   string         `json:"event"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(messages[0].Body), &wake); err != nil {
		t.Fatalf("decode completion wake: %v", err)
	}
	if wake.Event != "job.completed" || wake.Payload["status"] != "failed" || wake.Payload["job_status"] != "failed" {
		t.Fatalf("completion wake = %+v, want durable failed/failed status", wake)
	}
}

func TestEvent_PipelineManualGateCompletionWakesStoppedManager(t *testing.T) {
	teamDir := autoAdvanceTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	root := DaemonRoot(teamDir)
	sessionID := seedStoppedCodexManager(t, root, teamDir, "manager")
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
ephemeral = false

[[instances.manager.triggers]]
event = "job.step_completed"
match.target = "manager"
match.source = "daemon:completion"

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
target = "manager"
after = ["review"]
gate = "manual"

[teams.delivery]
instances = ["manager", "worker", "reviewer"]
pipelines = ["ticket_to_pr"]
`)
	fake := newSequencedFakeSpawner(eventShortFakeRuntime, eventShortFakeRuntime, 30*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()
	t.Cleanup(func() {
		_, _ = m.Stop("manager")
		_ = waitForEventReaper(t, m, "manager")
	})

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-94","kickoff":"implement SQU-94","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-94"); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if err := waitForEventReaper(t, m, "reviewer-squ-94"); err != nil {
		t.Fatalf("wait reviewer reaper: %v", err)
	}
	if fake.callCount() != 3 {
		t.Fatalf("spawn calls=%d, want worker, reviewer, manager resume", fake.callCount())
	}
	if got, want := fake.lastCall(), []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "resume", sessionID, "-"}; !stringSlicesEqual(got, want) {
		t.Fatalf("manager resume args = %v, want %v", got, want)
	}
	meta, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatalf("read manager metadata: %v", err)
	}
	if meta.Status != StatusRunning || meta.ResumeCount != 1 {
		t.Fatalf("manager metadata = %+v, want auto-resumed manager", meta)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read manager mailbox: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"event":"job.step_completed"`) || !strings.Contains(messages[0].Body, `"manager_gate_ready":true`) {
		t.Fatalf("manager messages = %+v, want completion event with manual gate context", messages)
	}
	j, err := jobstore.Read(teamDir, "squ-94")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if len(j.Steps) != 3 || j.Steps[1].Status != jobstore.StatusDone || j.Steps[2].Status != jobstore.StatusBlocked || j.Steps[2].Gate != jobstore.StepGateManual {
		t.Fatalf("job steps = %+v, want reviewer done and manager gate blocked", j.Steps)
	}
}

func TestEvent_ProbePipelineSkipsReviewAndCompletesOnImplementExit(t *testing.T) {
	root := t.TempDir()
	teamDir := autoAdvanceTeamDir(t)
	top := mustParseCustomTopo(t, `
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
target = "manager"
after = ["review"]
gate = "manual"

[teams.delivery]
instances = ["manager", "worker", "reviewer"]
pipelines = ["ticket_to_pr"]
`)

	fake := newFakeSpawner(eventShortFakeRuntime)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-94","kickoff":"measure SQU-94","kind":"probe","workspace":"worktree"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-94"); err != nil {
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
	if len(messages) != 1 || !strings.Contains(messages[0].Body, `"event":"job.step_completed"`) || !strings.Contains(messages[0].Body, `"job":"squ-94"`) {
		t.Fatalf("manager messages = %+v, want one completion-owner notification for the skipped probe pipeline", messages)
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

	fake := newFakeSpawner(eventShortFakeRuntime)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-155","kickoff":"implement SQU-155","workspace":"repo"}}`)
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
	if err := waitForEventReaper(t, m, "worker-squ-156"); err != nil {
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
	writeFixtureAgent(t, teamDir, "manager")
	top := mustParseCustomTopo(t, `
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
target = "manager"
after = ["implement"]
gate = "manual"

[teams.delivery]
instances = ["manager", "worker", "reviewer"]
pipelines = ["ticket_to_pr"]
`)

	fake := newFakeSpawner(eventShortFakeRuntime)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-93","kickoff":"implement SQU-93","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-93"); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls=%d, want worker plus persistent manager wake (manual gate step must remain undispatched)", fake.callCount())
	}
	if err := waitForEventReaper(t, m, "manager"); err != nil {
		t.Fatalf("wait manager reaper: %v", err)
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

	fake := newFakeSpawner(eventShortFakeRuntime)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-94","kickoff":"implement SQU-94","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-94"); err != nil {
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

	fake := newSequencedFakeSpawner(eventShortFakeRuntime, 30*time.Second, 30*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-172","kickoff":"implement SQU-172","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-172"); err != nil {
		t.Fatalf("wait worker reaper: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls after implement=%d, want review dispatched", fake.callCount())
	}
	killInstance(t, root, "reviewer-squ-172")
	if err := waitForEventReaper(t, m, "reviewer-squ-172"); err != nil {
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
	if err := waitForEventReaper(t, m, "reviewer-squ-172"); err != nil {
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

	fake := newSequencedFakeSpawner(eventShortFakeRuntime, 30*time.Second)
	m := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/event",
		`{"type":"ticket.created","payload":{"ticket":"SQU-173","kickoff":"implement SQU-173","workspace":"repo"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if err := waitForEventReaper(t, m, "worker-squ-173"); err != nil {
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
	if err := waitForEventReaper(t, m, "reviewer-squ-173"); err != nil {
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
	if err := waitForEventReaper(t, m, "worker-squ-174"); err != nil {
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
