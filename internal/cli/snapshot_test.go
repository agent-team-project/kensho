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
	if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), "manager", &daemon.Message{
		ID:   "msg-snapshot",
		From: "tester",
		Body: "snapshot inbox secret",
		TS:   now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("append inbox message: %v", err)
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
	if snapshot.Provenance == nil || snapshot.Provenance.Command != "agent-team snapshot" || snapshot.Provenance.Scope != "global" || snapshot.Provenance.Subject != "" || snapshot.Provenance.Options.Events == nil || *snapshot.Provenance.Options.Events != 5 || snapshot.Provenance.Options.IntakeDeliveries == nil || *snapshot.Provenance.Options.IntakeDeliveries != 50 || !snapshot.Provenance.Options.Redacted {
		t.Fatalf("snapshot provenance = %+v", snapshot.Provenance)
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
	if snapshot.InboxSummary == nil || snapshot.InboxSummary.Total != 1 || snapshot.InboxSummary.Unread != 1 || snapshot.InboxSummary.UnreadInstances != 1 {
		t.Fatalf("inbox summary = %+v", snapshot.InboxSummary)
	}
	if snapshot.Overview == nil || snapshot.Overview.Inbox.Unread != 1 {
		t.Fatalf("overview inbox = %+v", snapshot.Overview)
	}
	if len(snapshot.Inbox) != 1 || snapshot.Inbox[0].Instance != "manager" || snapshot.Inbox[0].LatestID != "msg-snapshot" || snapshot.Inbox[0].LatestBody != snapshotRedactedValue {
		t.Fatalf("snapshot inbox = %+v", snapshot.Inbox)
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
	if len(snapshot.PipelineExplain) != 1 || snapshot.PipelineExplain[0].Pipeline != "ticket_to_pr" || snapshot.PipelineExplain[0].ExplainedJobs != 1 || len(snapshot.PipelineExplain[0].Jobs) != 1 || snapshot.PipelineExplain[0].Jobs[0].JobID != "squ-503" || len(snapshot.PipelineExplain[0].Jobs[0].Steps) != 2 {
		t.Fatalf("pipeline explain = %+v", snapshot.PipelineExplain)
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

func TestSnapshotDiffCommandReportsChanges(t *testing.T) {
	tmp := t.TempDir()
	beforePath := filepath.Join(tmp, "before.json")
	afterPath := filepath.Join(tmp, "after.json")
	exitCode := 1
	before := snapshotDiffInput{
		CapturedAt: "2026-06-18T12:00:00Z",
		Pipeline:   "ticket_to_pr",
		Git:        &snapshotGitInfo{Branch: "main", Commit: "1111111", Upstream: "origin/main", Behind: 1},
		Runtime: &runtimeInfo{
			Runtime:        "claude",
			Binary:         "claude",
			Path:           "/usr/local/bin/claude",
			Available:      true,
			DirectRun:      true,
			DaemonDispatch: true,
			DirectResume:   true,
			ManagedResume:  true,
			Resume:         true,
			Subagents:      true,
			ConfigPath:     "/repo/.agent_team/config.toml",
		},
		Health: &healthResult{
			Healthy: false,
			Daemon:  healthDaemon{Running: false, Ready: false},
			Summary: psSummaryJSON{
				Total:     1,
				Crashed:   1,
				Unhealthy: 1,
			},
			Queue:    queueSummary{Total: 1, Dead: 1},
			Intake:   overviewIntakeSummary{Deliveries: 1, Errors: 1},
			Declared: healthDeclared{Persistent: 1, Missing: 1},
			Issues: []healthIssue{
				{Code: "daemon_down", Severity: "error"},
			},
		},
		Plan: &planResult{
			Daemon: planDaemon{Running: false},
			Summary: planSummary{
				Total:       2,
				Start:       1,
				Unsupported: 1,
			},
			Instances: []planRow{
				{Instance: "manager", Agent: "manager", Kind: "persistent", Status: "missing", Phase: "unknown", Action: "start"},
				{Instance: "worker", Agent: "worker", Kind: "on_demand", Status: "stopped", Phase: "idle", Action: "unsupported"},
			},
		},
		Next: &nextActionResult{
			OK:      false,
			State:   "attention",
			Actions: []string{"agent-team health --jobs", "agent-team sync --dry-run"},
			ActionDetails: []operatorActionHint{
				{Command: "agent-team health --jobs", Source: "health", Reason: "issues"},
				{Command: "agent-team sync --dry-run", Source: "topology", Reason: "missing"},
			},
			TotalActions: 2,
		},
		Provenance: newSnapshotProvenance("agent-team snapshot", "global", "", snapshotProvenanceOptions{
			Events:           intValuePtr(20),
			IntakeDeliveries: intValuePtr(20),
			ScheduleLimit:    intValuePtr(10),
			Redacted:         true,
		}),
		Instances: []snapshotDiffInstance{
			{Instance: "manager", Agent: "manager", Status: "running", Phase: "idle"},
			{Instance: "worker-squ-801", Agent: "worker", Status: "running", Phase: "blocked", Runtime: "codex", Job: "squ-801"},
		},
		Jobs: []snapshotDiffJob{
			{ID: "squ-801", Status: "running", Pipeline: "ticket_to_pr", Target: "worker"},
			{ID: "squ-802", Status: "blocked", Pipeline: "ticket_to_pr", Target: "worker"},
		},
		Inbox: []snapshotDiffInbox{
			{Instance: "manager", Agent: "manager", Status: "running", Total: 1, Unread: 1, LatestID: "msg-1", LatestFrom: "tester", LatestTS: "2026-06-18T11:59:00Z"},
		},
		Queue: []snapshotDiffQueueItem{
			{ID: "q-1", State: "pending"},
		},
		QueueQuarantine: []snapshotDiffQuarantine{
			{Path: "dead/q-dead.json", State: "dead", ID: "q-dead", EventType: "ticket.created", Instance: "worker", Job: "squ-802", Restorable: false, Problem: "invalid json"},
		},
		Schedules: []snapshotDiffSchedule{
			{Name: "delivery_due", Event: "schedule", Every: "1h", RunOnStart: true, NextRun: "2026-06-18T13:00:00Z"},
		},
		ScheduleNext: []snapshotDiffSchedule{
			{Name: "delivery_due", Event: "schedule", Every: "1h", RunOnStart: true, Due: true, DueReason: "run_on_start"},
		},
		Intake: []snapshotDiffIntake{
			{ID: "delivery-1", Provider: "linear", Status: "error", HTTPStatus: 500, ReplayStatus: "error", EventType: "ticket.created", Ticket: "SQU-801", JobID: "squ-801"},
		},
		IntakeDuplicates: []snapshotDiffIntakeDuplicate{
			{Provider: "github", RequestID: "github-delivery-1", Count: 2, IDs: []string{"delivery-1", "delivery-old"}},
		},
		Events: []snapshotDiffEvent{
			{ID: "ev-1", Action: "start", Instance: "worker-squ-801", Agent: "worker", Job: "squ-801", Status: "running"},
		},
		Status: &pipelineStatusRow{
			Pipeline:     "ticket_to_pr",
			Jobs:         2,
			ReadySteps:   1,
			FailedSteps:  0,
			BlockedSteps: 1,
		},
		AdvancePreview: []snapshotDiffAdvance{
			{JobID: "squ-801", Pipeline: "ticket_to_pr", StepID: "implement", Target: "worker", Action: "would_advance"},
		},
		SectionErrors: map[string]string{"queue": "parse failed"},
	}
	after := snapshotDiffInput{
		CapturedAt: "2026-06-18T12:05:00Z",
		Pipeline:   "ticket_to_pr",
		Git:        &snapshotGitInfo{Branch: "feature/squ-801", Commit: "2222222", Upstream: "origin/feature/squ-801", Dirty: true, Changes: 3, Ahead: 2},
		Runtime: &runtimeInfo{
			Runtime:        "codex",
			Binary:         "codex",
			Path:           "/usr/local/bin/codex",
			Available:      true,
			DirectRun:      true,
			DaemonDispatch: true,
			DirectResume:   true,
			EnvRuntime:     "codex",
			ConfigPath:     "/repo/.agent_team/config.toml",
		},
		Health: &healthResult{
			Healthy: true,
			Daemon:  healthDaemon{Running: true, Ready: true},
			Summary: psSummaryJSON{
				Total:   1,
				Running: 1,
			},
			Intake:   overviewIntakeSummary{Deliveries: 1, Recovered: 1},
			Declared: healthDeclared{Persistent: 1, Running: 1},
		},
		Plan: &planResult{
			Daemon: planDaemon{Running: true},
			Summary: planSummary{
				Total:    2,
				Keep:     1,
				OnDemand: 1,
			},
			Instances: []planRow{
				{Instance: "manager", Agent: "manager", Kind: "persistent", Status: "running", Phase: "idle", Action: "keep"},
				{Instance: "worker", Agent: "worker", Kind: "on_demand", Status: "stopped", Phase: "idle", Action: "on-demand"},
			},
		},
		Next: &nextActionResult{
			OK:      true,
			State:   "ok",
			Actions: []string{"agent-team snapshot --output diagnostics.json"},
			ActionDetails: []operatorActionHint{
				{Command: "agent-team snapshot --output diagnostics.json", Source: "snapshot", Reason: "handoff"},
			},
			TotalActions: 1,
		},
		Provenance: newSnapshotProvenance("agent-team team snapshot", "team", "delivery", snapshotProvenanceOptions{
			Events:        intValuePtr(5),
			ScheduleLimit: intValuePtr(0),
			Redacted:      false,
		}),
		Instances: []snapshotDiffInstance{
			{Instance: "manager", Agent: "manager", Status: "running", Phase: "idle"},
			{Instance: "reviewer-squ-803", Agent: "manager", Status: "running", Phase: "working", Runtime: "claude", Job: "squ-803"},
			{Instance: "worker-squ-801", Agent: "worker", Status: "exited", Phase: "done", Runtime: "codex", Job: "squ-801"},
		},
		Jobs: []snapshotDiffJob{
			{ID: "squ-801", Status: "done", Pipeline: "ticket_to_pr", Target: "worker"},
			{ID: "squ-803", Status: "queued", Pipeline: "ticket_to_pr", Target: "manager"},
		},
		Inbox: []snapshotDiffInbox{
			{Instance: "manager", Agent: "manager", Status: "running", Total: 2, Unread: 0, Cursor: "msg-2", LatestID: "msg-2", LatestFrom: "worker", LatestTS: "2026-06-18T12:04:00Z"},
			{Instance: "worker-squ-803", Agent: "worker", Status: "running", Total: 1, Unread: 1, LatestID: "msg-3", LatestFrom: "manager", LatestTS: "2026-06-18T12:05:00Z"},
		},
		Queue: []snapshotDiffQueueItem{
			{ID: "q-1", State: "dead"},
			{ID: "q-2", State: "pending"},
		},
		QueueQuarantine: []snapshotDiffQuarantine{
			{Path: "dead/q-dead.json", State: "dead", ID: "q-dead", EventType: "ticket.created", Instance: "worker", Job: "squ-802", Restorable: true},
			{Path: "pending/q-new.json", State: "pending", ID: "q-new", EventType: "ticket.created", Instance: "worker", Job: "squ-803", Restorable: true},
		},
		Schedules: []snapshotDiffSchedule{
			{Name: "delivery_due", Event: "schedule", Every: "2h", RunOnStart: true, NextRun: "2026-06-18T14:00:00Z"},
		},
		ScheduleNext: []snapshotDiffSchedule{
			{Name: "delivery_due", Event: "schedule", Every: "2h", RunOnStart: true, Due: false, NextRun: "2026-06-18T14:00:00Z"},
		},
		Intake: []snapshotDiffIntake{
			{ID: "delivery-1", Provider: "linear", Status: "ok", HTTPStatus: 200, ReplayStatus: "ok", EventType: "ticket.created", Ticket: "SQU-801", JobID: "squ-801"},
			{ID: "delivery-2", Provider: "github", Status: "ok", HTTPStatus: 202, EventType: "pr.merged", PR: "https://github.test/pr/1", JobID: "squ-801"},
		},
		IntakeDuplicates: []snapshotDiffIntakeDuplicate{
			{Provider: "github", RequestID: "github-delivery-1", Count: 3, IDs: []string{"delivery-1", "delivery-2", "delivery-old"}},
		},
		Events: []snapshotDiffEvent{
			{ID: "ev-1", Action: "exit", Instance: "worker-squ-801", Agent: "worker", Job: "squ-801", Status: "exited", ExitCode: &exitCode},
			{ID: "ev-2", Action: "queued", Instance: "manager", Agent: "manager", Job: "squ-803", Status: "running"},
		},
		Status: &pipelineStatusRow{
			Pipeline:     "ticket_to_pr",
			Jobs:         2,
			ReadySteps:   0,
			FailedSteps:  1,
			BlockedSteps: 0,
		},
		AdvancePreview: []snapshotDiffAdvance{
			{JobID: "squ-803", Pipeline: "ticket_to_pr", StepID: "review", Target: "manager", Action: "would_advance"},
		},
		SectionErrors: map[string]string{"advance_preview": "route missing"},
	}
	writeSnapshotDiffInput(t, beforePath, before)
	writeSnapshotDiffInput(t, afterPath, after)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("snapshot diff json: %v\nstderr=%s", err, stderr.String())
	}
	var result snapshotDiffResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode snapshot diff json: %v\nbody=%s", err, out.String())
	}
	if result.Before.Kind != "pipeline" || result.Before.Scope != "ticket_to_pr" || result.After.Scope != "ticket_to_pr" {
		t.Fatalf("snapshot diff metadata = %+v -> %+v", result.Before, result.After)
	}
	if result.Summary.Provenance.Added != 1 || result.Summary.Provenance.Removed != 1 || result.Summary.Provenance.Changed != 5 {
		t.Fatalf("provenance counters = %+v", result.Summary.Provenance)
	}
	if result.Summary.Git.Changed != 7 {
		t.Fatalf("git counters = %+v", result.Summary.Git)
	}
	if result.Summary.Runtime.Added != 1 || result.Summary.Runtime.Changed != 6 {
		t.Fatalf("runtime counters = %+v", result.Summary.Runtime)
	}
	if result.Summary.Health.Removed != 2 || result.Summary.Health.Changed != 12 {
		t.Fatalf("health counters = %+v", result.Summary.Health)
	}
	if result.Summary.Plan.Changed != 7 {
		t.Fatalf("plan counters = %+v", result.Summary.Plan)
	}
	if result.Summary.Next.Added != 1 || result.Summary.Next.Removed != 2 || result.Summary.Next.Changed != 3 {
		t.Fatalf("next counters = %+v", result.Summary.Next)
	}
	if result.Summary.Jobs.Added != 1 || result.Summary.Jobs.Removed != 1 || result.Summary.Jobs.Changed != 1 {
		t.Fatalf("job counters = %+v", result.Summary.Jobs)
	}
	if result.Summary.Instances.Added != 1 || result.Summary.Instances.Changed != 1 {
		t.Fatalf("instance counters = %+v", result.Summary.Instances)
	}
	if result.Summary.Inbox.Added != 1 || result.Summary.Inbox.Changed != 1 {
		t.Fatalf("inbox counters = %+v", result.Summary.Inbox)
	}
	if result.Summary.Queue.Added != 1 || result.Summary.Queue.Changed != 1 {
		t.Fatalf("queue counters = %+v", result.Summary.Queue)
	}
	if result.Summary.QueueQuarantine.Added != 1 || result.Summary.QueueQuarantine.Changed != 1 {
		t.Fatalf("queue quarantine counters = %+v", result.Summary.QueueQuarantine)
	}
	if result.Summary.Schedules.Changed != 2 {
		t.Fatalf("schedule counters = %+v", result.Summary.Schedules)
	}
	if result.Summary.Intake.Added != 1 || result.Summary.Intake.Changed != 2 {
		t.Fatalf("intake counters = %+v", result.Summary.Intake)
	}
	if result.Summary.Events.Added != 1 || result.Summary.Events.Changed != 1 {
		t.Fatalf("event counters = %+v", result.Summary.Events)
	}
	if result.Summary.Advance.Added != 1 || result.Summary.Advance.Removed != 1 {
		t.Fatalf("advance counters = %+v", result.Summary.Advance)
	}
	if result.Summary.SectionErrors.Added != 1 || result.Summary.SectionErrors.Removed != 1 {
		t.Fatalf("section error counters = %+v", result.Summary.SectionErrors)
	}
	if !hasSnapshotDiffChange(result.Changes, "provenance", "command", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "provenance", "subject", "added") ||
		!hasSnapshotDiffChange(result.Changes, "provenance", "intake_deliveries", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "git", "branch", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "git", "dirty", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "runtime", "runtime", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "runtime", "env_runtime", "added") ||
		!hasSnapshotDiffChange(result.Changes, "runtime", "subagents", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "health", "healthy", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "health", "issues.code.daemon_down", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "plan", "daemon.running", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "plan", "instance.manager", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "next", "ok", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "next", "action/agent-team health --jobs", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "next", "action/agent-team snapshot --output diagnostics.json", "added") ||
		!hasSnapshotDiffChange(result.Changes, "jobs", "squ-801", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "jobs", "squ-802", "removed") ||
		!hasSnapshotDiffChange(result.Changes, "jobs", "squ-803", "added") ||
		!hasSnapshotDiffChange(result.Changes, "instances", "reviewer-squ-803", "added") ||
		!hasSnapshotDiffChange(result.Changes, "instances", "worker-squ-801", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "inbox", "manager", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "inbox", "worker-squ-803", "added") ||
		!hasSnapshotDiffChange(result.Changes, "queue_quarantine", "dead/q-dead.json", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "schedules", "declared/delivery_due", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "intake", "duplicate/github/github-delivery-1", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "intake", "delivery-1", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "events", "ev-1", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "pipelines", "ticket_to_pr.ready_steps", "changed") ||
		!hasSnapshotDiffChange(result.Changes, "advance", "squ-803:review", "added") {
		t.Fatalf("missing expected changes: %+v", result.Changes)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"snapshot", "diff", beforePath, afterPath})
	if err := text.Execute(); err != nil {
		t.Fatalf("snapshot diff text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"snapshot diff:",
		"provenance: added=1 removed=1 changed=5",
		"git: added=0 removed=0 changed=7",
		"runtime: added=1 removed=0 changed=6",
		"health: added=0 removed=2 changed=12",
		"plan: added=0 removed=0 changed=7",
		"next: added=1 removed=2 changed=3",
		"instances: added=1 removed=0 changed=1",
		"jobs: added=1 removed=1 changed=1",
		"pipelines:",
		"inbox: added=1 removed=0 changed=1",
		"queue: added=1 removed=0 changed=1",
		"queue_quarantine: added=1 removed=0 changed=1",
		"schedules: added=0 removed=0 changed=2",
		"intake: added=1 removed=0 changed=2",
		"events: added=1 removed=0 changed=1",
		"advance: added=1 removed=1 changed=0",
		"section_errors: added=1 removed=1 changed=0",
		"squ-801",
		"ticket_to_pr.ready_steps",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("snapshot diff text missing %q:\n%s", want, textOut.String())
		}
	}

	changedExit := NewRootCmd()
	changedExitOut, changedExitErr := &bytes.Buffer{}, &bytes.Buffer{}
	changedExit.SetOut(changedExitOut)
	changedExit.SetErr(changedExitErr)
	changedExit.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--json", "--exit-code"})
	if err := changedExit.Execute(); err == nil {
		t.Fatalf("snapshot diff --exit-code with changes succeeded")
	}
	var changedExitResult snapshotDiffResult
	if err := json.Unmarshal(changedExitOut.Bytes(), &changedExitResult); err != nil {
		t.Fatalf("decode changed exit-code diff json: %v\nbody=%s\nstderr=%s", err, changedExitOut.String(), changedExitErr.String())
	}
	if changedExitResult.Summary.TotalChanges == 0 {
		t.Fatalf("changed exit-code diff result has no changes: %+v", changedExitResult)
	}

	sameExit := NewRootCmd()
	sameExitOut, sameExitErr := &bytes.Buffer{}, &bytes.Buffer{}
	sameExit.SetOut(sameExitOut)
	sameExit.SetErr(sameExitErr)
	sameExit.SetArgs([]string{"snapshot", "diff", beforePath, beforePath, "--exit-code"})
	if err := sameExit.Execute(); err != nil {
		t.Fatalf("snapshot diff --exit-code identical: %v\nstderr=%s", err, sameExitErr.String())
	}
	if !strings.Contains(sameExitOut.String(), "changes: total=0") || !strings.Contains(sameExitOut.String(), "details: none") {
		t.Fatalf("identical snapshot diff text = %s", sameExitOut.String())
	}

	provenanceOnly := NewRootCmd()
	provenanceOnlyOut, provenanceOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	provenanceOnly.SetOut(provenanceOnlyOut)
	provenanceOnly.SetErr(provenanceOnlyErr)
	provenanceOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "provenance", "--json"})
	if err := provenanceOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff provenance section: %v\nstderr=%s", err, provenanceOnlyErr.String())
	}
	var provenanceOnlyResult snapshotDiffResult
	if err := json.Unmarshal(provenanceOnlyOut.Bytes(), &provenanceOnlyResult); err != nil {
		t.Fatalf("decode provenance-only snapshot diff: %v\nbody=%s", err, provenanceOnlyOut.String())
	}
	if provenanceOnlyResult.Summary.TotalChanges != 7 || provenanceOnlyResult.Summary.Provenance.Added != 1 || provenanceOnlyResult.Summary.Provenance.Removed != 1 || provenanceOnlyResult.Summary.Provenance.Changed != 5 || provenanceOnlyResult.Summary.Queue.Added != 0 {
		t.Fatalf("provenance-only diff summary = %+v", provenanceOnlyResult.Summary)
	}
	for _, change := range provenanceOnlyResult.Changes {
		if change.Section != "provenance" {
			t.Fatalf("provenance-only diff included %q change: %+v", change.Section, provenanceOnlyResult.Changes)
		}
	}

	gitOnly := NewRootCmd()
	gitOnlyOut, gitOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	gitOnly.SetOut(gitOnlyOut)
	gitOnly.SetErr(gitOnlyErr)
	gitOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "git", "--json"})
	if err := gitOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff git section: %v\nstderr=%s", err, gitOnlyErr.String())
	}
	var gitOnlyResult snapshotDiffResult
	if err := json.Unmarshal(gitOnlyOut.Bytes(), &gitOnlyResult); err != nil {
		t.Fatalf("decode git-only snapshot diff: %v\nbody=%s", err, gitOnlyOut.String())
	}
	if gitOnlyResult.Summary.TotalChanges != 7 || gitOnlyResult.Summary.Git.Changed != 7 || gitOnlyResult.Summary.Provenance.Changed != 0 {
		t.Fatalf("git-only diff summary = %+v", gitOnlyResult.Summary)
	}
	for _, change := range gitOnlyResult.Changes {
		if change.Section != "git" {
			t.Fatalf("git-only diff included %q change: %+v", change.Section, gitOnlyResult.Changes)
		}
	}

	runtimeOnly := NewRootCmd()
	runtimeOnlyOut, runtimeOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	runtimeOnly.SetOut(runtimeOnlyOut)
	runtimeOnly.SetErr(runtimeOnlyErr)
	runtimeOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "runtime", "--json"})
	if err := runtimeOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff runtime section: %v\nstderr=%s", err, runtimeOnlyErr.String())
	}
	var runtimeOnlyResult snapshotDiffResult
	if err := json.Unmarshal(runtimeOnlyOut.Bytes(), &runtimeOnlyResult); err != nil {
		t.Fatalf("decode runtime-only snapshot diff: %v\nbody=%s", err, runtimeOnlyOut.String())
	}
	if runtimeOnlyResult.Summary.TotalChanges != 7 || runtimeOnlyResult.Summary.Runtime.Added != 1 || runtimeOnlyResult.Summary.Runtime.Changed != 6 || runtimeOnlyResult.Summary.Git.Changed != 0 {
		t.Fatalf("runtime-only diff summary = %+v", runtimeOnlyResult.Summary)
	}
	for _, change := range runtimeOnlyResult.Changes {
		if change.Section != "runtime" {
			t.Fatalf("runtime-only diff included %q change: %+v", change.Section, runtimeOnlyResult.Changes)
		}
	}

	healthOnly := NewRootCmd()
	healthOnlyOut, healthOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	healthOnly.SetOut(healthOnlyOut)
	healthOnly.SetErr(healthOnlyErr)
	healthOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "health", "--json"})
	if err := healthOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff health section: %v\nstderr=%s", err, healthOnlyErr.String())
	}
	var healthOnlyResult snapshotDiffResult
	if err := json.Unmarshal(healthOnlyOut.Bytes(), &healthOnlyResult); err != nil {
		t.Fatalf("decode health-only snapshot diff: %v\nbody=%s", err, healthOnlyOut.String())
	}
	if healthOnlyResult.Summary.TotalChanges != 14 || healthOnlyResult.Summary.Health.Removed != 2 || healthOnlyResult.Summary.Health.Changed != 12 || healthOnlyResult.Summary.Plan.Changed != 0 {
		t.Fatalf("health-only diff summary = %+v", healthOnlyResult.Summary)
	}
	for _, change := range healthOnlyResult.Changes {
		if change.Section != "health" {
			t.Fatalf("health-only diff included %q change: %+v", change.Section, healthOnlyResult.Changes)
		}
	}

	planOnly := NewRootCmd()
	planOnlyOut, planOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	planOnly.SetOut(planOnlyOut)
	planOnly.SetErr(planOnlyErr)
	planOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "plan", "--json"})
	if err := planOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff plan section: %v\nstderr=%s", err, planOnlyErr.String())
	}
	var planOnlyResult snapshotDiffResult
	if err := json.Unmarshal(planOnlyOut.Bytes(), &planOnlyResult); err != nil {
		t.Fatalf("decode plan-only snapshot diff: %v\nbody=%s", err, planOnlyOut.String())
	}
	if planOnlyResult.Summary.TotalChanges != 7 || planOnlyResult.Summary.Plan.Changed != 7 || planOnlyResult.Summary.Health.Changed != 0 {
		t.Fatalf("plan-only diff summary = %+v", planOnlyResult.Summary)
	}
	for _, change := range planOnlyResult.Changes {
		if change.Section != "plan" {
			t.Fatalf("plan-only diff included %q change: %+v", change.Section, planOnlyResult.Changes)
		}
	}

	nextOnly := NewRootCmd()
	nextOnlyOut, nextOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	nextOnly.SetOut(nextOnlyOut)
	nextOnly.SetErr(nextOnlyErr)
	nextOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "next", "--json"})
	if err := nextOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff next section: %v\nstderr=%s", err, nextOnlyErr.String())
	}
	var nextOnlyResult snapshotDiffResult
	if err := json.Unmarshal(nextOnlyOut.Bytes(), &nextOnlyResult); err != nil {
		t.Fatalf("decode next-only snapshot diff: %v\nbody=%s", err, nextOnlyOut.String())
	}
	if nextOnlyResult.Summary.TotalChanges != 6 || nextOnlyResult.Summary.Next.Added != 1 || nextOnlyResult.Summary.Next.Removed != 2 || nextOnlyResult.Summary.Next.Changed != 3 || nextOnlyResult.Summary.Plan.Changed != 0 {
		t.Fatalf("next-only diff summary = %+v", nextOnlyResult.Summary)
	}
	for _, change := range nextOnlyResult.Changes {
		if change.Section != "next" {
			t.Fatalf("next-only diff included %q change: %+v", change.Section, nextOnlyResult.Changes)
		}
	}

	queueOnly := NewRootCmd()
	queueOnlyOut, queueOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	queueOnly.SetOut(queueOnlyOut)
	queueOnly.SetErr(queueOnlyErr)
	queueOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "queue", "--json"})
	if err := queueOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff queue section: %v\nstderr=%s", err, queueOnlyErr.String())
	}
	var queueOnlyResult snapshotDiffResult
	if err := json.Unmarshal(queueOnlyOut.Bytes(), &queueOnlyResult); err != nil {
		t.Fatalf("decode queue-only snapshot diff: %v\nbody=%s", err, queueOnlyOut.String())
	}
	if queueOnlyResult.Summary.TotalChanges != 2 || queueOnlyResult.Summary.Queue.Added != 1 || queueOnlyResult.Summary.Queue.Changed != 1 || queueOnlyResult.Summary.Jobs.Added != 0 {
		t.Fatalf("queue-only diff summary = %+v", queueOnlyResult.Summary)
	}
	for _, change := range queueOnlyResult.Changes {
		if change.Section != "queue" {
			t.Fatalf("queue-only diff included %q change: %+v", change.Section, queueOnlyResult.Changes)
		}
	}

	inboxOnly := NewRootCmd()
	inboxOnlyOut, inboxOnlyErr := &bytes.Buffer{}, &bytes.Buffer{}
	inboxOnly.SetOut(inboxOnlyOut)
	inboxOnly.SetErr(inboxOnlyErr)
	inboxOnly.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "inbox", "--json"})
	if err := inboxOnly.Execute(); err != nil {
		t.Fatalf("snapshot diff inbox section: %v\nstderr=%s", err, inboxOnlyErr.String())
	}
	var inboxOnlyResult snapshotDiffResult
	if err := json.Unmarshal(inboxOnlyOut.Bytes(), &inboxOnlyResult); err != nil {
		t.Fatalf("decode inbox-only snapshot diff: %v\nbody=%s", err, inboxOnlyOut.String())
	}
	if inboxOnlyResult.Summary.TotalChanges != 2 || inboxOnlyResult.Summary.Inbox.Added != 1 || inboxOnlyResult.Summary.Inbox.Changed != 1 || inboxOnlyResult.Summary.Queue.Added != 0 {
		t.Fatalf("inbox-only diff summary = %+v", inboxOnlyResult.Summary)
	}
	for _, change := range inboxOnlyResult.Changes {
		if change.Section != "inbox" {
			t.Fatalf("inbox-only diff included %q change: %+v", change.Section, inboxOnlyResult.Changes)
		}
	}

	invalidSection := NewRootCmd()
	invalidSectionOut, invalidSectionErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSection.SetOut(invalidSectionOut)
	invalidSection.SetErr(invalidSectionErr)
	invalidSection.SetArgs([]string{"snapshot", "diff", beforePath, afterPath, "--section", "telemetry"})
	if err := invalidSection.Execute(); err == nil {
		t.Fatalf("snapshot diff invalid section succeeded")
	}
	if !strings.Contains(invalidSectionErr.String(), "--section must be provenance, git, runtime, health, plan, next") {
		t.Fatalf("invalid section stderr = %q", invalidSectionErr.String())
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

func writeSnapshotDiffInput(t *testing.T, path string, input snapshotDiffInput) {
	t.Helper()
	body, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot diff input: %v", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func hasSnapshotDiffChange(changes []snapshotDiffChange, section, id, action string) bool {
	for _, change := range changes {
		if change.Section == section && change.ID == id && change.Action == action {
			return true
		}
	}
	return false
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
			RequestID:  "linear-delivery-505",
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
			RequestID:  "linear-delivery-505",
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
	if len(snapshot.IntakeDuplicates) != 1 || snapshot.IntakeDuplicates[0].RequestID != "linear-delivery-505" || snapshot.IntakeDuplicates[0].Count != 2 {
		t.Fatalf("intake duplicates = %+v", snapshot.IntakeDuplicates)
	}
}

func TestSnapshotSummaryIncludesJobTriage(t *testing.T) {
	now := time.Now().UTC()
	snapshot := &snapshotResult{
		CapturedAt: now.Format(time.RFC3339),
		Repo:       "/repo",
		Provenance: newSnapshotProvenance("agent-team snapshot", "global", "", snapshotProvenanceOptions{
			Redacted: true,
		}),
		Git:      &snapshotGitInfo{Branch: "main", Commit: "abcdef123456", Dirty: true, Changes: 2, Ahead: 1},
		Redacted: true,
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
		InboxSummary: &overviewInboxSummary{Instances: 2, Total: 3, Unread: 1, UnreadInstances: 1},
		IntakeSummary: &overviewIntakeSummary{
			Deliveries: 1,
			Errors:     1,
			Recovered:  0,
			Replayable: 1,
		},
		IntakeDuplicates: []intakeDuplicateRequest{{
			Provider:  "github",
			RequestID: "delivery-1",
			Count:     2,
		}},
	}

	var out bytes.Buffer
	renderSnapshotSummary(&out, snapshot)
	for _, want := range []string{"command: agent-team snapshot scope=global", "git: branch=main commit=abcdef123456 dirty=yes changes=2 ahead=1 behind=0", "jobs: total=1", "job triage: attention=1 ready_steps=0", "job status: previews=1 changes=1", "pipeline status: pipelines=1 jobs=1 ready_steps=1 manual_gates=0 stale_running_steps=0 failed_steps=0", "pipeline advance: ready=1 route_previews=1", "teams doctor: teams=1 problems=1 warnings=1", "team doctor: problems=1 warnings=0", "queue: total=1 pending=1 dead=0 delayed=0 attempts=0 quarantined=1 restorable=1 unrestorable=0", "inbox: instances=2 total=3 unread=1 unread_instances=1", "intake: deliveries=1 errors=1 recovered=0 replayable=1 duplicate_request_ids=1"} {
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
	if err := daemon.AppendMessage(daemon.DaemonRoot(teamDir), "manager", &daemon.Message{
		ID:   "msg-raw",
		From: "tester",
		Body: "raw inbox body",
		TS:   now,
	}); err != nil {
		t.Fatalf("append inbox message: %v", err)
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
	if len(snapshot.Inbox) != 1 || snapshot.Inbox[0].LatestBody != "raw inbox body" {
		t.Fatalf("inbox body = %+v", snapshot.Inbox)
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
