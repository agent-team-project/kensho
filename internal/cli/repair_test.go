package cli

import (
	"bytes"
	"encoding/json"
	"errors"
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

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-tick", "--format", "{{.DryRun}} {{.Daemon.Action}} {{.Queue.Action}} {{len .Queue.Results}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("repair formatted dry-run: %v\nstderr=%s", err, formatErr.String())
	}
	if formatErr.Len() != 0 {
		t.Fatalf("repair formatted stderr = %q", formatErr.String())
	}
	if got, want := formatOut.String(), "true skipped would_retry 1\n"; got != want {
		t.Fatalf("repair formatted output = %q, want %q", got, want)
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
		"agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers",
		"agent-team intake replay --all --dedupe-request-id",
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

func TestRepairDryRunReportsIntakeDuplicateRequestIDs(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	for _, delivery := range []intakeDelivery{
		{
			ID:         "first",
			Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
			Provider:   "github",
			RequestID:  "github-delivery-1",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: 200,
			EventType:  "pr.opened",
		},
		{
			ID:         "second",
			Time:       time.Date(2026, 6, 19, 12, 1, 0, 0, time.UTC),
			Provider:   "github",
			RequestID:  "github-delivery-1",
			Status:     intakeDeliveryStatusOK,
			HTTPStatus: 200,
			EventType:  "pr.opened",
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
	cmd.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-queue", "--skip-tick", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair intake duplicate dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair duplicate intake json: %v\nbody=%s", err, out.String())
	}
	if result.Intake.Action != "would_review" || result.Intake.Unresolved != 0 || result.Intake.Replayable != 0 || result.Intake.DuplicateRequestIDs != 1 {
		t.Fatalf("intake repair step = %+v", result.Intake)
	}
	if !containsString(result.Intake.Actions, "agent-team intake duplicates") {
		t.Fatalf("intake duplicate actions = %+v", result.Intake.Actions)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"repair", "--target", tmp, "--dry-run", "--skip-daemon", "--skip-queue", "--skip-tick"})
	if err := text.Execute(); err != nil {
		t.Fatalf("repair duplicate intake text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"duplicate_request_ids=1", "agent-team intake duplicates"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("repair duplicate intake text missing %q:\n%s", want, textOut.String())
		}
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
	cmd.SetArgs([]string{"repair", "--target", target, "--dry-run", "--preview-routes", "--skip-daemon", "--skip-queue", "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--json"})
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
	if payload["job_id"] != "squ-122" || payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["workspace"] != "repo" || payload["runtime"] != "codex" || payload["runtime_binary"] != "codex-dev" {
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

func TestRepairAllReadyStepsDryRun(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.parallel_checks]
trigger.event = "ticket.created"

[[pipelines.parallel_checks.steps]]
id = "lint"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "test"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "review"
target = "manager"
after = ["lint", "test"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"pipeline", "run", "parallel_checks", "SQU-321", "--repo", target, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"repair", "--target", target, "--dry-run", "--skip-daemon", "--skip-queue", "--all-ready-steps", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair all-ready dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair all-ready: %v\nbody=%s", err, out.String())
	}
	if result.Tick.Result == nil || len(result.Tick.Result.Advance) != 2 || result.Tick.Result.Advance[0].StepID != "lint" || result.Tick.Result.Advance[0].StepStatus != job.StatusQueued || result.Tick.Result.Advance[1].StepID != "test" {
		t.Fatalf("repair all-ready advance = %+v, want queued lint then ready test", result.Tick.Result)
	}
}

func TestRepairDryRunCanRetryFailedPipelineRoutes(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:         "squ-123",
		Ticket:     "SQU-123",
		Target:     "worker",
		Kickoff:    "retry implementation",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusFailed,
		LastEvent:  "step_failed",
		LastStatus: "implementation failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-old", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write failed job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"repair",
		"--target", target,
		"--dry-run",
		"--retry-pipelines",
		"--preview-routes",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--workspace", "repo",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair retry pipelines dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair retry json: %v\nbody=%s", err, out.String())
	}
	if result.PipelineRetry.Action != "would_dispatch" || len(result.PipelineRetry.Results) != 1 {
		t.Fatalf("pipeline retry step = %+v", result.PipelineRetry)
	}
	row := result.PipelineRetry.Results[0]
	if row.JobID != "squ-123" || row.Action != "would_dispatch" || row.Step == nil || row.Step.Status != job.StatusBlocked || row.Preview == nil || row.Preview.Dispatch == nil {
		t.Fatalf("pipeline retry row = %+v", row)
	}
	if row.Preview.Dispatch.RequestedName != "worker-squ-123-implement" {
		t.Fatalf("requested name = %q", row.Preview.Dispatch.RequestedName)
	}
	payload := row.Preview.Dispatch.Preview.Payload
	if payload["job_id"] != "squ-123" || payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["workspace"] != "repo" || payload["runtime"] != "codex" || payload["runtime_binary"] != "codex-dev" {
		t.Fatalf("preview payload = %+v", payload)
	}
	unchanged, err := job.Read(teamDir, "squ-123")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusFailed || unchanged.Steps[0].Status != job.StatusFailed || unchanged.Steps[0].Instance != "worker-old" || unchanged.Steps[0].FinishedAt.IsZero() {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{
		"repair",
		"--target", target,
		"--dry-run",
		"--retry-pipelines",
		"--preview-routes",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--workspace", "repo",
	})
	if err := text.Execute(); err != nil {
		t.Fatalf("repair retry pipelines text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Pipeline retry: would_dispatch", "Routes:", "squ-123 step=implement target=worker instance=worker-squ-123-implement", "Matched: worker"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("repair retry text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestRepairRetryPipelinesDispatchesAndAudits(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:         "squ-124",
		Ticket:     "SQU-124",
		Target:     "worker",
		Kickoff:    "retry implementation",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusFailed,
		LastEvent:  "step_failed",
		LastStatus: "implementation failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-old", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write failed job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	retryMessageFile := filepath.Join(target, "repair-retry-message.txt")
	if err := os.WriteFile(retryMessageFile, []byte("repair approved by operator from file\n"), 0o644); err != nil {
		t.Fatalf("write retry message: %v", err)
	}
	cmd.SetArgs([]string{
		"repair",
		"--target", target,
		"--retry-pipelines",
		"--retry-message-file", retryMessageFile,
		"--skip-queue",
		"--skip-tick",
		"--workspace", "repo",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair retry pipelines: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair retry json: %v\nbody=%s", err, out.String())
	}
	if result.PipelineRetry.Action != "retried" || len(result.PipelineRetry.Results) != 1 {
		t.Fatalf("pipeline retry result = %+v", result.PipelineRetry)
	}
	row := result.PipelineRetry.Results[0]
	if row.JobID != "squ-124" || row.Action != "dispatched" || row.StepStatus != job.StatusRunning || row.Instance != "worker-squ-124-implement" {
		t.Fatalf("pipeline retry row = %+v", row)
	}
	updated, err := job.Read(teamDir, "squ-124")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusRunning || updated.LastEvent != "advance_dispatched" || updated.Steps[0].Status != job.StatusRunning || updated.Steps[0].Instance != "worker-squ-124-implement" {
		t.Fatalf("updated job = %+v", updated)
	}
	events, err := job.ListEvents(teamDir, "squ-124")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var sawRetry bool
	for _, event := range events {
		if event.Type == "reopened" && event.Message == "repair approved by operator from file" && event.Data["step"] == "implement" {
			sawRetry = true
			break
		}
	}
	if !sawRetry {
		t.Fatalf("events missing retry audit = %+v", events)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-124-implement")
}

func TestRepairTimeoutPipelinesMarksStaleRunningSteps(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
timeout = "1h"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stale := &job.Job{
		ID:        "squ-820",
		Ticket:    "SQU-820",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-90 * time.Minute),
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-820", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
		},
	}
	if err := job.Write(teamDir, stale); err != nil {
		t.Fatalf("write stale job: %v", err)
	}
	other := &job.Job{
		ID:        "oth-820",
		Ticket:    "OTH-820",
		Target:    "worker",
		Pipeline:  "other",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-90 * time.Minute),
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-oth-820", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
		},
	}
	if err := job.Write(teamDir, other); err != nil {
		t.Fatalf("write other job: %v", err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{
		"repair",
		"--target", root,
		"--dry-run",
		"--timeout-pipelines",
		"--timeout-pipeline", "ticket_to_pr",
		"--timeout-target-agent", "worker",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := dry.Execute(); err != nil {
		t.Fatalf("repair timeout dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResult repairResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResult); err != nil {
		t.Fatalf("decode dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if dryResult.PipelineTimeout.Action != "would_fail" || len(dryResult.PipelineTimeout.Results) != 1 || dryResult.PipelineTimeout.Results[0].JobID != "squ-820" {
		t.Fatalf("dry pipeline timeout = %+v", dryResult.PipelineTimeout)
	}
	unchanged, err := job.Read(teamDir, "squ-820")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.Steps[0].Status != job.StatusRunning || unchanged.Steps[0].Instance != "worker-squ-820" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	timeoutMessageFile := filepath.Join(root, "repair-timeout-message.txt")
	if err := os.WriteFile(timeoutMessageFile, []byte("repair timeout approved from file\n"), 0o644); err != nil {
		t.Fatalf("write timeout message: %v", err)
	}
	apply.SetArgs([]string{
		"repair",
		"--target", root,
		"--timeout-pipelines",
		"--timeout-pipeline", "ticket_to_pr",
		"--timeout-target-agent", "worker",
		"--timeout-message-file", timeoutMessageFile,
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := apply.Execute(); err != nil {
		t.Fatalf("repair timeout apply: %v\nstderr=%s", err, applyErr.String())
	}
	var result repairResult
	if err := json.Unmarshal(applyOut.Bytes(), &result); err != nil {
		t.Fatalf("decode apply: %v\nbody=%s", err, applyOut.String())
	}
	if result.PipelineTimeout.Action != "timed_out" || len(result.PipelineTimeout.Results) != 1 {
		t.Fatalf("apply pipeline timeout = %+v", result.PipelineTimeout)
	}
	updated, err := job.Read(teamDir, "squ-820")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusFailed || updated.Steps[0].Status != job.StatusFailed || updated.Steps[0].Instance != "" || updated.LastStatus != "repair timeout approved from file" {
		t.Fatalf("updated job = %+v", updated)
	}
	otherUpdated, err := job.Read(teamDir, "oth-820")
	if err != nil {
		t.Fatalf("read other job: %v", err)
	}
	if otherUpdated.Status != job.StatusRunning || otherUpdated.Steps[0].Status != job.StatusRunning || otherUpdated.Steps[0].Instance != "worker-oth-820" {
		t.Fatalf("other job changed = %+v", otherUpdated)
	}
}

func TestRepairTimeoutJobsMarksStaleRunningWork(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-821",
			Ticket:    "SQU-821",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-821", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-822",
			Ticket:    "SQU-822",
			Target:    "worker",
			Instance:  "worker-squ-822",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
		{
			ID:        "squ-823",
			Ticket:    "SQU-823",
			Target:    "worker",
			Instance:  "worker-squ-823",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-30 * time.Minute),
			UpdatedAt: now.Add(-30 * time.Minute),
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{
		"repair",
		"--target", root,
		"--dry-run",
		"--timeout-jobs",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := dry.Execute(); err != nil {
		t.Fatalf("repair timeout jobs dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResult repairResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResult); err != nil {
		t.Fatalf("decode dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if dryResult.JobTimeout.Action != "would_fail" || len(dryResult.JobTimeout.Results) != 2 {
		t.Fatalf("dry job timeout = %+v", dryResult.JobTimeout)
	}
	dryJobs := map[string]pipelineTimeoutResult{}
	for _, result := range dryResult.JobTimeout.Results {
		dryJobs[result.JobID] = result
	}
	if dryJobs["squ-821"].StepID != "implement" || dryJobs["squ-822"].StepID != "" {
		t.Fatalf("dry timeout jobs = %+v", dryJobs)
	}
	unchanged, err := job.Read(teamDir, "squ-822")
	if err != nil {
		t.Fatalf("read unchanged job: %v", err)
	}
	if unchanged.Status != job.StatusRunning || unchanged.Instance != "worker-squ-822" {
		t.Fatalf("dry-run mutated job = %+v", unchanged)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{
		"repair",
		"--target", root,
		"--timeout-jobs",
		"--timeout-message", "repair timed out job work",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := apply.Execute(); err != nil {
		t.Fatalf("repair timeout jobs apply: %v\nstderr=%s", err, applyErr.String())
	}
	var result repairResult
	if err := json.Unmarshal(applyOut.Bytes(), &result); err != nil {
		t.Fatalf("decode apply: %v\nbody=%s", err, applyOut.String())
	}
	if result.JobTimeout.Action != "timed_out" || len(result.JobTimeout.Results) != 2 {
		t.Fatalf("apply job timeout = %+v", result.JobTimeout)
	}
	stepJob, err := job.Read(teamDir, "squ-821")
	if err != nil {
		t.Fatalf("read timed out step job: %v", err)
	}
	if stepJob.Status != job.StatusFailed || stepJob.Steps[0].Status != job.StatusFailed || stepJob.Steps[0].Instance != "" || stepJob.LastStatus != "repair timed out job work" {
		t.Fatalf("step job = %+v", stepJob)
	}
	lifecycleJob, err := job.Read(teamDir, "squ-822")
	if err != nil {
		t.Fatalf("read timed out lifecycle job: %v", err)
	}
	if lifecycleJob.Status != job.StatusFailed || lifecycleJob.Instance != "worker-squ-822" || lifecycleJob.LastEvent != "job_timeout" || lifecycleJob.LastStatus != "repair timed out job work" {
		t.Fatalf("lifecycle job = %+v", lifecycleJob)
	}
	freshJob, err := job.Read(teamDir, "squ-823")
	if err != nil {
		t.Fatalf("read fresh job: %v", err)
	}
	if freshJob.Status != job.StatusRunning || freshJob.Instance != "worker-squ-823" {
		t.Fatalf("fresh job changed = %+v", freshJob)
	}
}

func TestRepairTimeoutJobsFiltersByPipelineAndTargetAgent(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-824",
			Ticket:    "SQU-824",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-824", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-825",
			Ticket:    "SQU-825",
			Target:    "worker",
			Pipeline:  "other",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-825", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-826",
			Ticket:    "SQU-826",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-90 * time.Minute),
			Steps: []job.Step{
				{ID: "review", Target: "manager", Status: job.StatusRunning, Instance: "manager-squ-826", StartedAt: now.Add(-90 * time.Minute), Timeout: "1h0m0s"},
			},
		},
		{
			ID:        "squ-827",
			Ticket:    "SQU-827",
			Target:    "manager",
			Instance:  "manager-squ-827",
			Status:    job.StatusRunning,
			CreatedAt: now.Add(-48 * time.Hour),
			UpdatedAt: now.Add(-48 * time.Hour),
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{
		"repair",
		"--target", root,
		"--dry-run",
		"--timeout-jobs",
		"--timeout-pipeline", "ticket_to_pr",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := dry.Execute(); err != nil {
		t.Fatalf("repair timeout jobs pipeline dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var dryResult repairResult
	if err := json.Unmarshal(dryOut.Bytes(), &dryResult); err != nil {
		t.Fatalf("decode pipeline dry-run: %v\nbody=%s", err, dryOut.String())
	}
	if len(dryResult.JobTimeout.Results) != 2 || dryResult.JobTimeout.Results[0].JobID != "squ-824" || dryResult.JobTimeout.Results[1].JobID != "squ-826" {
		t.Fatalf("pipeline timeout rows = %+v", dryResult.JobTimeout.Results)
	}

	apply := NewRootCmd()
	applyOut, applyErr := &bytes.Buffer{}, &bytes.Buffer{}
	apply.SetOut(applyOut)
	apply.SetErr(applyErr)
	apply.SetArgs([]string{
		"repair",
		"--target", root,
		"--timeout-jobs",
		"--timeout-target-agent", "manager",
		"--timeout-message", "manager repair timeout",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--json",
	})
	if err := apply.Execute(); err != nil {
		t.Fatalf("repair timeout jobs target apply: %v\nstderr=%s", err, applyErr.String())
	}
	var result repairResult
	if err := json.Unmarshal(applyOut.Bytes(), &result); err != nil {
		t.Fatalf("decode target apply: %v\nbody=%s", err, applyOut.String())
	}
	if result.JobTimeout.Action != "timed_out" || len(result.JobTimeout.Results) != 2 || result.JobTimeout.Results[0].JobID != "squ-826" || result.JobTimeout.Results[1].JobID != "squ-827" {
		t.Fatalf("target timeout rows = %+v", result.JobTimeout)
	}
	for _, id := range []string{"squ-824", "squ-825"} {
		unchanged, err := job.Read(teamDir, id)
		if err != nil {
			t.Fatalf("read unchanged %s: %v", id, err)
		}
		if unchanged.Status != job.StatusRunning {
			t.Fatalf("%s changed = %+v", id, unchanged)
		}
	}
}

func TestRepairRetryPipelinesStepFilter(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:         "squ-125",
			Ticket:     "SQU-125",
			Target:     "worker",
			Kickoff:    "implement failed",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "implement failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed, Instance: "worker-implement", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "squ-126",
			Ticket:     "SQU-126",
			Target:     "worker",
			Kickoff:    "review failed",
			Pipeline:   "ticket_to_pr",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "review failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "review", Target: "worker", Status: job.StatusFailed, Instance: "worker-review", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
		{
			ID:         "ops-126",
			Ticket:     "OPS-126",
			Target:     "worker",
			Kickoff:    "ops review failed",
			Pipeline:   "ops_review",
			Status:     job.StatusFailed,
			LastEvent:  "step_failed",
			LastStatus: "ops review failed",
			CreatedAt:  now,
			UpdatedAt:  now,
			Steps: []job.Step{
				{ID: "review", Target: "worker", Status: job.StatusFailed, Instance: "worker-ops-review", StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-30 * time.Minute)},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"repair",
		"--target", target,
		"--dry-run",
		"--retry-pipelines",
		"--retry-pipeline", "ticket_to_pr",
		"--retry-step", "review",
		"--skip-daemon",
		"--skip-queue",
		"--skip-tick",
		"--workspace", "repo",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("repair retry pipelines --retry-step: %v\nstderr=%s", err, stderr.String())
	}
	var result repairResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode repair retry step json: %v\nbody=%s", err, out.String())
	}
	if result.PipelineRetry.Action != "would_dispatch" || len(result.PipelineRetry.Results) != 1 {
		t.Fatalf("pipeline retry step = %+v", result.PipelineRetry)
	}
	row := result.PipelineRetry.Results[0]
	if row.JobID != "squ-126" || row.StepID != "review" || row.Action != "would_dispatch" {
		t.Fatalf("retry row = %+v", row)
	}
	if strings.Contains(out.String(), "squ-125") {
		t.Fatalf("repair retry step leaked nonmatching job:\n%s", out.String())
	}
	if strings.Contains(out.String(), "ops-126") {
		t.Fatalf("repair retry pipeline filter leaked other pipeline:\n%s", out.String())
	}
	unchanged, err := job.Read(teamDir, "squ-125")
	if err != nil {
		t.Fatalf("read unchanged: %v", err)
	}
	if unchanged.Status != job.StatusFailed || unchanged.Steps[0].Status != job.StatusFailed || unchanged.Steps[0].Instance != "worker-implement" {
		t.Fatalf("dry-run changed nonmatching job = %+v", unchanged)
	}
	otherPipeline, err := job.Read(teamDir, "ops-126")
	if err != nil {
		t.Fatalf("read other pipeline: %v", err)
	}
	if otherPipeline.Status != job.StatusFailed || otherPipeline.Steps[0].Status != job.StatusFailed || otherPipeline.Steps[0].Instance != "worker-ops-review" {
		t.Fatalf("dry-run changed other pipeline job = %+v", otherPipeline)
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

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"repair", "--format", "{{.DryRun}}", "--json"},
			want: "--format cannot be combined",
		},
		{
			name: "invalid format",
			args: []string{"repair", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "retry pipelines without daemon",
			args: []string{"repair", "--retry-pipelines", "--skip-daemon"},
			want: "--retry-pipelines requires daemon access",
		},
		{
			name: "retry message without retry pipelines",
			args: []string{"repair", "--retry-message", "incident"},
			want: "--retry-message requires --retry-pipelines",
		},
		{
			name: "retry message file without retry pipelines",
			args: []string{"repair", "--retry-message-file", "incident.txt"},
			want: "--retry-message-file requires --retry-pipelines",
		},
		{
			name: "retry step without retry pipelines",
			args: []string{"repair", "--retry-step", "review"},
			want: "--retry-step requires --retry-pipelines",
		},
		{
			name: "retry pipeline without retry pipelines",
			args: []string{"repair", "--retry-pipeline", "ticket_to_pr"},
			want: "--retry-pipeline requires --retry-pipelines",
		},
		{
			name: "retry force without retry pipelines",
			args: []string{"repair", "--retry-force"},
			want: "--retry-force requires --retry-pipelines",
		},
		{
			name: "timeout jobs with timeout pipelines",
			args: []string{"repair", "--timeout-jobs", "--timeout-pipelines"},
			want: "--timeout-jobs cannot be combined with --timeout-pipelines",
		},
		{
			name: "timeout pipeline without timeout mode",
			args: []string{"repair", "--timeout-pipeline", "ticket_to_pr"},
			want: "--timeout-pipeline requires --timeout-pipelines or --timeout-jobs",
		},
		{
			name: "timeout message file without timeout mode",
			args: []string{"repair", "--timeout-message-file", "incident.txt"},
			want: "--timeout-message-file requires --timeout-pipelines or --timeout-jobs",
		},
		{
			name: "timeout target without timeout mode",
			args: []string{"repair", "--timeout-target-agent", "worker"},
			want: "--timeout-target-agent requires --timeout-pipelines or --timeout-jobs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("repair invalid flags succeeded: stdout=%s", out.String())
			}
			var exit ExitCode
			if !errors.As(err, &exit) || int(exit) != 2 {
				t.Fatalf("repair err = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
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
