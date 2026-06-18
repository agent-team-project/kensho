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
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].ID != "q-snapshot" || snapshot.QueueSummary == nil || snapshot.QueueSummary.Dead != 1 {
		t.Fatalf("queue = %+v summary=%+v", snapshot.Queue, snapshot.QueueSummary)
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
	if snapshot.Runtime == nil || snapshot.Runtime.Runtime == "" {
		t.Fatalf("runtime = %+v", snapshot.Runtime)
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

func TestSnapshotSummaryIncludesJobTriage(t *testing.T) {
	now := time.Now().UTC()
	snapshot := &snapshotResult{
		CapturedAt: now.Format(time.RFC3339),
		Repo:       "/repo",
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
	}

	var out bytes.Buffer
	renderSnapshotSummary(&out, snapshot)
	for _, want := range []string{"jobs: total=1", "job triage: attention=1 ready_steps=0", "job status: previews=1 changes=1", "pipeline status: pipelines=1 jobs=1 ready_steps=1 failed_steps=0", "pipeline advance: ready=1 route_previews=1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, out.String())
		}
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
