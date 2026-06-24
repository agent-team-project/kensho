package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestSnapshotCommandJSONCollectsRepoState(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()

	j, err := job.New("SQU-501", "worker", "capture diagnostics", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Status = job.StatusRunning
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-501"), `[status]
phase = "blocked"
description = "waiting on queue failure"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-501"
ticket = "SQU-501"
branch = "worker-squ-501"
`, now)
	queue := &daemon.QueueItem{
		ID:         "q-snapshot",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-501",
		Payload: map[string]any{
			"target":       "worker",
			"ticket":       "SQU-501",
			"access_token": "secret-token",
			"headers": map[string]any{
				"authorization": "Bearer secret",
				"safe":          "visible",
			},
		},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now.Add(-30 * time.Minute),
		DeadLetteredAt: now.Add(-30 * time.Minute),
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), queue); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-snapshot-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-501",
		Payload:    map[string]any{"job_id": "squ-501", "target": "worker", "ticket": "SQU-501"},
		QueuedAt:   now.Add(-45 * time.Minute),
		UpdatedAt:  now.Add(-40 * time.Minute),
	})
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "intake-snapshot",
		Time:       now.Add(-20 * time.Minute),
		Provider:   "linear",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: 503,
		EventType:  "ticket.created",
		Payload: map[string]any{
			"source":       "linear",
			"ticket":       "SQU-501",
			"access_token": "intake-secret",
			"headers": map[string]any{
				"authorization": "Bearer intake-secret",
				"safe":          "visible",
			},
		},
		Ticket: "SQU-501",
		Error:  "daemon is not running",
	}); err != nil {
		t.Fatalf("append intake delivery: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), &daemon.LifecycleEvent{
		TS:       now,
		Action:   "dispatch",
		Instance: "worker-squ-501",
		Agent:    "worker",
		Status:   daemon.StatusRunning,
		Message:  "started worker",
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "5", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot json: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Version == "" || snapshot.CapturedAt == "" || snapshot.Repo == "" || snapshot.TeamDir == "" {
		t.Fatalf("snapshot metadata missing: %+v", snapshot)
	}
	if snapshot.Health == nil || snapshot.Health.Queue.Dead != 1 {
		t.Fatalf("health = %+v", snapshot.Health)
	}
	if snapshot.Overview == nil || snapshot.Next == nil || len(snapshot.Next.ActionDetails) == 0 {
		t.Fatalf("overview/next missing: overview=%+v next=%+v", snapshot.Overview, snapshot.Next)
	}
	if detail, ok := findOperatorActionHint(snapshot.Next.ActionDetails, "agent-team repair --dry-run --jobs"); !ok || detail.Source != "health" || detail.Reason != "unhealthy" {
		t.Fatalf("snapshot next repair detail = %+v, ok=%v", detail, ok)
	}
	if snapshot.Plan == nil || snapshot.Plan.Summary.Total == 0 {
		t.Fatalf("plan = %+v", snapshot.Plan)
	}
	if len(snapshot.Jobs) != 1 || snapshot.Jobs[0].ID != "squ-501" || snapshot.Jobs[0].Status != job.StatusRunning {
		t.Fatalf("jobs = %+v", snapshot.Jobs)
	}
	if snapshot.JobTriage == nil || snapshot.JobTriage.Summary.Total != 1 || len(snapshot.JobTriage.Attention) == 0 {
		t.Fatalf("job triage = %+v", snapshot.JobTriage)
	}
	if len(snapshot.JobStatus) != 1 || snapshot.JobStatus[0].JobID != "squ-501" || snapshot.JobStatus[0].After != job.StatusBlocked || !snapshot.JobStatus[0].Changed || !snapshot.JobStatus[0].DryRun {
		t.Fatalf("job status preview = %+v", snapshot.JobStatus)
	}
	if !snapshot.Redacted {
		t.Fatalf("snapshot should redact by default: %+v", snapshot)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].ID != "q-snapshot" || snapshot.QueueSummary == nil || snapshot.QueueSummary.Dead != 1 || snapshot.QueueSummary.Quarantined != 1 || snapshot.QueueSummary.QuarantineRestorable != 1 || snapshot.QueueSummary.QuarantineUnrestorable != 0 {
		t.Fatalf("queue = %+v summary=%+v", snapshot.Queue, snapshot.QueueSummary)
	}
	if len(snapshot.QueueQuarantine) != 1 || snapshot.QueueQuarantine[0].ID != "q-snapshot-quarantined" || !snapshot.QueueQuarantine[0].Restorable || snapshot.QueueQuarantine[0].Job != "squ-501" {
		t.Fatalf("queue quarantine = %+v", snapshot.QueueQuarantine)
	}
	if snapshot.Queue[0].Payload["target"] != "worker" || snapshot.Queue[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("redacted payload = %+v", snapshot.Queue[0].Payload)
	}
	headers, ok := snapshot.Queue[0].Payload["headers"].(map[string]any)
	if !ok || headers["authorization"] != snapshotRedactedValue || headers["safe"] != "visible" {
		t.Fatalf("nested redacted payload = %+v", snapshot.Queue[0].Payload["headers"])
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Instance != "worker-squ-501" {
		t.Fatalf("events = %+v", snapshot.Events)
	}
	if len(snapshot.Intake) != 1 || snapshot.Intake[0].ID != "intake-snapshot" || snapshot.IntakeSummary == nil || snapshot.IntakeSummary.Errors != 1 || snapshot.IntakeSummary.Replayable != 1 {
		t.Fatalf("intake = %+v summary=%+v", snapshot.Intake, snapshot.IntakeSummary)
	}
	if snapshot.Intake[0].Payload["ticket"] != "SQU-501" || snapshot.Intake[0].Payload["access_token"] != snapshotRedactedValue {
		t.Fatalf("redacted intake payload = %+v", snapshot.Intake[0].Payload)
	}
	intakeHeaders, ok := snapshot.Intake[0].Payload["headers"].(map[string]any)
	if !ok || intakeHeaders["authorization"] != snapshotRedactedValue || intakeHeaders["safe"] != "visible" {
		t.Fatalf("nested redacted intake payload = %+v", snapshot.Intake[0].Payload["headers"])
	}
	if len(snapshot.Intake[0].Actions) == 0 || !strings.Contains(snapshot.Intake[0].Actions[0], "agent-team intake replay intake-snapshot") {
		t.Fatalf("intake actions = %+v", snapshot.Intake[0].Actions)
	}
	if snapshot.Runtime == nil || snapshot.Runtime.Runtime == "" {
		t.Fatalf("runtime = %+v", snapshot.Runtime)
	}
	if snapshot.TeamsDoctor == nil || !snapshot.TeamsDoctor.OK || len(snapshot.TeamsDoctor.Teams) != 1 {
		t.Fatalf("teams doctor = %+v", snapshot.TeamsDoctor)
	}
}

func TestSnapshotIncludesPipelineAdvancePreview(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-503",
		Ticket:    "SQU-503",
		Target:    "worker",
		Kickoff:   "SQU-503: implement",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
			{ID: "implement", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write ready job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", target, "--events", "0", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot pipeline advance: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if len(snapshot.PipelineAdvance) != 1 || snapshot.PipelineAdvance[0].JobID != "squ-503" || snapshot.PipelineAdvance[0].Action != "would_advance" || !snapshot.PipelineAdvance[0].DryRun {
		t.Fatalf("pipeline advance preview = %+v", snapshot.PipelineAdvance)
	}
	if len(snapshot.PipelineStatus) != 1 || snapshot.PipelineStatus[0].Pipeline != "ticket_to_pr" || snapshot.PipelineStatus[0].Declared || snapshot.PipelineStatus[0].Jobs != 1 || snapshot.PipelineStatus[0].ReadySteps != 1 {
		t.Fatalf("pipeline status = %+v", snapshot.PipelineStatus)
	}
	preview := snapshot.PipelineAdvance[0].Preview
	if preview == nil || preview.Step == nil || preview.Step.ID != "implement" || preview.Dispatch == nil || preview.Dispatch.RequestedName != "worker-squ-503-implement" {
		t.Fatalf("pipeline advance route preview = %+v", preview)
	}
	if preview.Dispatch.Preview == nil || len(preview.Dispatch.Preview.Matched) != 1 || preview.Dispatch.Preview.Matched[0] != "worker" {
		t.Fatalf("pipeline advance dispatch preview = %+v", preview.Dispatch.Preview)
	}
	payload := preview.Dispatch.Preview.Payload
	if payload["job_id"] != "squ-503" || payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["workspace"] != "worktree" {
		t.Fatalf("pipeline advance payload = %+v", payload)
	}
	unchanged, err := job.Read(teamDir, "squ-503")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("snapshot mutated job = %+v", unchanged)
	}
}

func TestSnapshotIncludesGitMetadata(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	initInto(t, tmp)
	runGitForJobTest(t, tmp, "init")
	runGitForJobTest(t, tmp, "config", "user.email", "agent-team@example.test")
	runGitForJobTest(t, tmp, "config", "user.name", "Agent Team")
	runGitForJobTest(t, tmp, "add", ".")
	runGitForJobTest(t, tmp, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(tmp, "uncommitted.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot git metadata: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Git == nil || snapshot.Git.Branch == "" || snapshot.Git.Commit == "" {
		t.Fatalf("git metadata = %+v, want branch and commit", snapshot.Git)
	}
	if !snapshot.Git.Dirty || snapshot.Git.Changes == 0 {
		t.Fatalf("git metadata = %+v, want dirty working tree", snapshot.Git)
	}
}

func TestSnapshotIntakeSummaryUsesFullLedger(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, delivery := range []intakeDelivery{
		{
			ID:         "older-error",
			Time:       now.Add(-time.Hour),
			Provider:   "linear",
			Status:     intakeDeliveryStatusError,
			HTTPStatus: 503,
			EventType:  "ticket.created",
			Payload:    map[string]any{"source": "linear", "ticket": "SQU-504"},
			Ticket:     "SQU-504",
			Error:      "daemon is not running",
		},
		{
			ID:         "newer-ok",
			Time:       now,
			Provider:   "linear",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: 200,
			EventType:  "ticket.created",
			Ticket:     "SQU-505",
		},
	} {
		if err := appendIntakeDelivery(teamDir, delivery); err != nil {
			t.Fatalf("append %s: %v", delivery.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--intake-deliveries", "1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot intake limit: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if len(snapshot.Intake) != 1 || snapshot.Intake[0].ID != "newer-ok" {
		t.Fatalf("limited intake rows = %+v", snapshot.Intake)
	}
	if snapshot.IntakeSummary == nil || snapshot.IntakeSummary.Deliveries != 2 || snapshot.IntakeSummary.Errors != 1 || snapshot.IntakeSummary.Replayable != 1 || snapshot.IntakeSummary.LatestErrorID != "older-error" {
		t.Fatalf("intake summary = %+v", snapshot.IntakeSummary)
	}
}

func TestSnapshotSummaryIncludesJobTriage(t *testing.T) {
	now := time.Now().UTC()
	snapshot := &snapshotResult{
		CapturedAt: now.Format(time.RFC3339),
		Repo:       "/repo",
		Git:        &snapshotGitInfo{Branch: "main", Commit: "abcdef123456", Dirty: true, Changes: 2, Ahead: 1},
		Redacted:   true,
		Jobs: []*job.Job{
			{ID: "squ-601", Ticket: "SQU-601", Target: "worker", Status: job.StatusFailed, CreatedAt: now, UpdatedAt: now},
		},
		JobTriage: &jobTriageSnapshot{
			Summary: jobSummary{Total: 1, Failed: 1},
			Attention: []jobTriageItem{{
				JobID:    "squ-601",
				Ticket:   "SQU-601",
				Status:   job.StatusFailed,
				Severity: "critical",
				Reasons:  []string{"failed"},
			}},
		},
		JobStatus: []jobStatusReconcileResult{{
			JobID:   "squ-601",
			Changed: true,
		}},
		PipelineStatus: []pipelineStatusRow{{
			Pipeline:   "ticket_to_pr",
			Jobs:       1,
			ReadySteps: 1,
		}},
		PipelineAdvance: []pipelineAdvanceResult{{
			JobID:  "squ-601",
			Action: "would_advance",
			Preview: &jobAdvancePreview{
				Dispatch: &dispatchRoutePreview{
					Preview: &eventPublishPreview{},
				},
			},
		}},
		TeamsDoctor: &allTeamDoctorResult{
			OK:    false,
			Teams: []teamDoctorResult{{Team: teamInfo{Name: "delivery"}, OK: false}},
			Problems: []teamDoctorFinding{{
				Code:    "pipeline_target_outside_team",
				Team:    "delivery",
				Message: "pipeline target outside team",
			}},
			Warnings: []teamDoctorFinding{{
				Code:    "schedule_routes_outside_team",
				Team:    "delivery",
				Message: "schedule routes outside team",
			}},
		},
		TeamDoctor: &teamDoctorResult{
			Team: teamInfo{Name: "delivery"},
			OK:   false,
			Problems: []teamDoctorFinding{{
				Code:    "pipeline_target_outside_team",
				Message: "pipeline target outside team",
			}},
		},
		QueueSummary: &queueSummary{Total: 1, Pending: 1, Quarantined: 1, QuarantineRestorable: 1},
		IntakeSummary: &overviewIntakeSummary{
			Deliveries: 1,
			Errors:     1,
			Recovered:  0,
			Replayable: 1,
		},
	}

	var out bytes.Buffer
	renderSnapshotSummary(&out, snapshot)
	for _, want := range []string{"git: branch=main commit=abcdef123456 dirty=yes changes=2 ahead=1 behind=0", "jobs: total=1", "job triage: attention=1 ready_steps=0", "job status: previews=1 changes=1", "pipeline status: pipelines=1 jobs=1 ready_steps=1 manual_gates=0 failed_steps=0", "pipeline advance: ready=1 route_previews=1", "teams doctor: teams=1 problems=1 warnings=1", "team doctor: problems=1 warnings=0", "queue: total=1 pending=1 dead=0 delayed=0 attempts=0 quarantined=1 restorable=1 unrestorable=0", "intake: deliveries=1 errors=1 recovered=0 replayable=1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, out.String())
		}
	}
}

func TestSnapshotIncludesTeamDoctorFindings(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[instances.other]
agent = "other"

[[instances.other.triggers]]
event = "agent.dispatch"
match.target = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "other"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot team doctor: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.TeamsDoctor == nil || snapshot.TeamsDoctor.OK || !hasTeamDoctorFindingForTeam(snapshot.TeamsDoctor.Problems, "delivery", "pipeline_target_outside_team") {
		t.Fatalf("teams doctor = %+v", snapshot.TeamsDoctor)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0"})
	if err := text.Execute(); err != nil {
		t.Fatalf("snapshot team doctor text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "teams doctor: teams=1 problems=1 warnings=0") {
		t.Fatalf("snapshot text missing teams doctor summary:\n%s", textOut.String())
	}
}

func TestSnapshotCommandWritesOutputFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	outPath := filepath.Join(tmp, "diagnostics", "snapshot.json")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--output", outPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot output: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "Wrote snapshot to") {
		t.Fatalf("stdout = %q", out.String())
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatalf("decode output file: %v\nbody=%s", err, string(body))
	}
	if len(snapshot.Events) != 0 {
		t.Fatalf("--events 0 should skip events: %+v", snapshot.Events)
	}
}

func TestSnapshotNoRedactPreservesPayloadSecrets(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:         "q-sensitive",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-502",
		Payload:    map[string]any{"target": "worker", "api_key": "raw-key"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("write queue: %v", err)
	}
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "intake-raw",
		Time:       now,
		Provider:   "github",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: 503,
		EventType:  "pr.opened",
		Payload:    map[string]any{"source": "github", "api_key": "raw-intake-key"},
		Error:      "daemon is not running",
	}); err != nil {
		t.Fatalf("append intake delivery: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--target", tmp, "--events", "0", "--no-redact", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot no-redact: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Redacted {
		t.Fatalf("--no-redact should disable redaction: %+v", snapshot)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].Payload["api_key"] != "raw-key" {
		t.Fatalf("queue payload = %+v", snapshot.Queue)
	}
	if len(snapshot.Intake) != 1 || snapshot.Intake[0].Payload["api_key"] != "raw-intake-key" {
		t.Fatalf("intake payload = %+v", snapshot.Intake)
	}
}

func TestSnapshotRejectsJSONAndOutputFile(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--json", "--output", "snapshot.json"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("snapshot invalid flags succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "choose one of --json or --output") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSnapshotRejectsInvalidIntakeLimit(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "--intake-deliveries", "-2"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("snapshot invalid intake limit succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "--intake-deliveries must be >= -1") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writeQuarantinedQueueItem(t *testing.T, teamDir, stamp, state string, item *daemon.QueueItem) {
	t.Helper()
	if item == nil {
		t.Fatal("nil queue item")
	}
	item.State = state
	body, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		t.Fatalf("marshal quarantined queue item: %v", err)
	}
	body = append(body, '\n')
	dir := filepath.Join(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), "quarantine", stamp, state)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir queue quarantine: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, item.ID+".json"), body, 0o644); err != nil {
		t.Fatalf("write quarantined queue item: %v", err)
	}
}
