package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestRepairDryRunPreviewsDeadQueueWithoutDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	writeDeadQueueItemForRepairTest(t, teamDir, "q-repair-preview")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-tick", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Daemon.Action != "skipped" || result.Queue.Action != "would_retry" {
		t.Fatalf("repair result = %+v", result)
	}
	if len(result.Queue.Results) != 1 || result.Queue.Results[0].Action != "would_retry" {
		t.Fatalf("queue results = %+v", result.Queue.Results)
	}
	if result.HealthBefore == nil || result.HealthBefore.Queue.Dead != 1 || result.HealthBefore.Queue.Pending != 0 {
		t.Fatalf("health before = %+v", result.HealthBefore)
	}
	if result.HealthAfter != nil {
		t.Fatalf("dry-run should not collect health after: %+v", result.HealthAfter)
	}
	item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-repair-preview")
	if err != nil {
		t.Fatalf("read queue item: %v", err)
	}
	if item.State != daemon.QueueStateDead || item.LastError == "" {
		t.Fatalf("dry-run mutated queue item = %+v", item)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-tick"})
	if err := text.Execute(); err != nil {
		t.Fatalf("repair text dry-run: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"ISSUE", "queue_dead_letter", "agent-team queue retry --all; agent-team repair --skip-tick"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("repair text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestRepairDryRunReportsIntakeRecoveryActions(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "intake-repair",
		Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Provider:   "linear",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: 503,
		EventType:  "ticket.created",
		Payload:    map[string]any{"source": "linear", "ticket": "SQU-219", "title": "Repair intake"},
		Ticket:     "SQU-219",
		Error:      "daemon is not running",
	}); err != nil {
		t.Fatalf("append intake delivery: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-queue", "--skip-tick", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair intake dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair intake json: %v\nbody=%s", err, out.String())
	}
	if result.Intake.Action != "would_review" || result.Intake.Unresolved != 1 || result.Intake.Replayable != 1 || result.Intake.LatestErrorID != "intake-repair" {
		t.Fatalf("intake repair step = %+v", result.Intake)
	}
	for _, want := range []string{
		"agent-team intake deliveries --unresolved",
		"agent-team intake replay --all --dry-run --preview-triggers",
		"agent-team intake replay --all",
	} {
		if !containsString(result.Intake.Actions, want) {
			t.Fatalf("intake actions missing %q: %+v", want, result.Intake.Actions)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-queue", "--skip-tick"})
	if err := text.Execute(); err != nil {
		t.Fatalf("repair intake text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Intake: would_review", "unresolved=1", "agent-team intake deliveries --unresolved"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("repair intake text missing %q:\n%s", want, textOut.String())
		}
	}

	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		t.Fatalf("list intake deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].ReplayStatus != "" {
		t.Fatalf("repair dry-run mutated intake delivery = %+v", deliveries)
	}
}

func TestRepairJobsIncludesStatusPreview(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-121",
		Ticket:    "SQU-121",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-121"), `[status]
phase = "blocked"
description = "needs credentials"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-121"
ticket = "SQU-121"
branch = "worker-squ-121"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-queue", "--skip-tick", "--jobs", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair --jobs dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair json: %v\nbody=%s", err, out.String())
	}
	if result.HealthBefore == nil {
		t.Fatal("missing health_before")
	}
	if result.HealthBefore.Jobs == nil || result.HealthBefore.Jobs.Summary.Total != 1 {
		t.Fatalf("jobs snapshot = %+v", result.HealthBefore.Jobs)
	}
	if len(result.HealthBefore.JobStatus) != 1 ||
		result.HealthBefore.JobStatus[0].JobID != "squ-121" ||
		result.HealthBefore.JobStatus[0].After != job.StatusBlocked ||
		!result.HealthBefore.JobStatus[0].Changed {
		t.Fatalf("job status preview = %+v", result.HealthBefore.JobStatus)
	}
	var sawBlocked bool
	for _, issue := range result.HealthBefore.Issues {
		if issue.Code == "job_status_blocked" && issue.Job == "squ-121" && issue.Phase == "blocked" {
			sawBlocked = true
			break
		}
	}
	if !sawBlocked {
		t.Fatalf("issues = %+v, missing job_status_blocked", result.HealthBefore.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-queue", "--skip-tick", "--jobs"})
	if err := text.Execute(); err != nil {
		t.Fatalf("repair --jobs text dry-run: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"job_attention=1", "job_status_changes=1", "job_status_blocked=1", "job_status_blocked", "agent-team job unblock squ-121 <answer...>"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("repair text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestRepairDryRunCanPreviewTickRoutes(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-122",
		Ticket:    "SQU-122",
		Target:    "worker",
		Kickoff:   "SQU-122: implement",
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
	cmd.SetArgs([]string{"repair", "--target", target, "--dry-run", "--preview-routes", "--skip-daemon", "--skip-queue", "--workspace", "repo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair dry-run preview-routes: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair preview-routes json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Tick.Action != "would_tick" || result.Tick.Result == nil || !result.Tick.Result.DryRun {
		t.Fatalf("repair route preview result = %+v", result)
	}
	if len(result.Tick.Result.Advance) != 1 || result.Tick.Result.Advance[0].Preview == nil || result.Tick.Result.Advance[0].Preview.Step == nil || result.Tick.Result.Advance[0].Preview.Step.ID != "implement" {
		t.Fatalf("repair route preview advance = %+v", result.Tick.Result.Advance)
	}
	dispatch := result.Tick.Result.Advance[0].Preview.Dispatch
	if dispatch == nil || dispatch.RequestedName != "worker-squ-122-implement" || dispatch.Preview == nil || len(dispatch.Preview.Matched) != 1 || dispatch.Preview.Matched[0] != "worker" {
		t.Fatalf("repair route dispatch preview = %+v", dispatch)
	}
	payload := dispatch.Preview.Payload
	if payload["job_id"] != "squ-122" || payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["workspace"] != "repo" {
		t.Fatalf("repair route preview payload = %+v", payload)
	}
	unchanged, err := job.Read(teamDir, "squ-122")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("repair route dry-run mutated job = %+v", unchanged)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"repair", "--target", target, "--dry-run", "--preview-routes", "--skip-daemon", "--skip-queue", "--workspace", "repo"})
	if err := text.Execute(); err != nil {
		t.Fatalf("repair dry-run preview-routes text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Tick: would_tick", "Routes:", "squ-122 step=implement target=worker instance=worker-squ-122-implement", "Matched: worker"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("repair route preview text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestRepairRetriesDeadQueueOffline(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	writeDeadQueueItemForRepairTest(t, teamDir, "q-repair-reset")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"repair", "--target", tmp, "--skip-daemon", "--skip-tick", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair apply: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair json: %v\nbody=%s", err, out.String())
	}
	if result.DryRun || result.Daemon.Action != "skipped" || result.Queue.Action != "retried" {
		t.Fatalf("repair result = %+v", result)
	}
	if len(result.Queue.Results) != 1 || result.Queue.Results[0].Action != "reset" {
		t.Fatalf("queue results = %+v", result.Queue.Results)
	}
	if result.HealthAfter == nil || result.HealthAfter.Queue.Dead != 0 || result.HealthAfter.Queue.Pending != 1 {
		t.Fatalf("health after = %+v", result.HealthAfter)
	}
	item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-repair-reset")
	if err != nil {
		t.Fatalf("read queue item: %v", err)
	}
	if item.State != daemon.QueueStatePending || item.LastError != "" || !item.DeadLetteredAt.IsZero() {
		t.Fatalf("repair did not reset queue item = %+v", item)
	}
}

func TestRepairRejectsInvalidFlagCombinations(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"repair", "--until-idle", "--dry-run"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("repair invalid flags succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "--until-idle cannot be combined with --dry-run") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	previewRoutes := NewRootCmd()
	previewRoutesOut, previewRoutesErr := &bytes.Buffer{}, &bytes.Buffer{}
	previewRoutes.SetOut(previewRoutesOut)
	previewRoutes.SetErr(previewRoutesErr)
	previewRoutes.SetArgs([]string{"repair", "--preview-routes"})
	if err := previewRoutes.Execute(); err == nil {
		t.Fatalf("repair --preview-routes without --dry-run succeeded: stdout=%s", previewRoutesOut.String())
	}
	if !strings.Contains(previewRoutesErr.String(), "--preview-routes requires --dry-run") {
		t.Fatalf("stderr = %q", previewRoutesErr.String())
	}
}

func writeDeadQueueItemForRepairTest(t *testing.T, teamDir, id string) {
	t.Helper()
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:             id,
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-120",
		Payload:        map[string]any{"target": "worker", "ticket": "SQU-120"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now.Add(-30 * time.Minute),
		DeadLetteredAt: now.Add(-30 * time.Minute),
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
}

func TestRepairDaemonRetryDispatchesDeadQueue(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	writeDeadQueueItemForRepairTest(t, teamDir, "q-repair-dispatch")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"repair", "--target", target, "--skip-tick", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair daemon: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair json: %v\nbody=%s", err, out.String())
	}
	if result.Daemon.Action != "reconciled" || result.Queue.Action != "retried" {
		t.Fatalf("repair result = %+v", result)
	}
	if len(result.Queue.Results) != 1 || result.Queue.Results[0].Action != "dispatched" {
		t.Fatalf("queue results = %+v", result.Queue.Results)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-repair-dispatch"); !os.IsNotExist(err) {
		t.Fatalf("queue item should be removed after daemon retry, err=%v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-120")
}
