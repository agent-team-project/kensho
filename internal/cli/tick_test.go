package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestTickRunsMaintenanceCycle(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	queued := &daemon.QueueItem{
		ID:         "q-tick",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-91",
		Payload:    map[string]any{"target": "worker", "name": "worker-squ-91", "ticket": "SQU-91"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), queued); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	j := &job.Job{
		ID:        "squ-93",
		Ticket:    "SQU-93",
		Target:    "worker",
		Kickoff:   "SQU-93: implement",
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

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("tick dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview tickResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode tick dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.Reconcile != nil || preview.Queue == nil || !preview.Queue.DryRun || preview.Queue.WouldDispatch != 1 {
		t.Fatalf("tick preview = %+v", preview)
	}
	if len(preview.Advance) != 1 || preview.Advance[0].Action != "would_advance" || !preview.Advance[0].DryRun {
		t.Fatalf("tick preview advance = %+v", preview.Advance)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-tick"); err != nil {
		t.Fatalf("tick dry-run removed queue item: %v", err)
	}
	unchanged, err := job.Read(teamDir, "squ-93")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("tick dry-run mutated job = %+v", unchanged)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick json: %v\nbody=%s", err, out.String())
	}
	if result.Reconcile == nil || result.Queue == nil {
		t.Fatalf("tick missing daemon results = %+v", result)
	}
	if result.Queue.Attempted != 1 || result.Queue.Dispatched != 1 || result.Queue.Pending != 0 {
		t.Fatalf("queue result = %+v", result.Queue)
	}
	if len(result.Advance) != 1 || result.Advance[0].JobID != "squ-93" || result.Advance[0].Action != "advanced" || result.Advance[0].StepStatus != job.StatusRunning {
		t.Fatalf("advance result = %+v", result.Advance)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-tick"); !os.IsNotExist(err) {
		t.Fatalf("queue item should be removed after tick, err=%v", err)
	}
	updated, err := job.Read(teamDir, "squ-93")
	if err != nil {
		t.Fatalf("read advanced job: %v", err)
	}
	if updated.Steps[1].Status != job.StatusRunning || updated.Steps[1].Instance == "" {
		t.Fatalf("advanced job = %+v", updated)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-91")
	stopAndWaitForTest(t, mgr, updated.Steps[1].Instance)
}

func TestTickDryRunIncludesDueSchedules(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-tick-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"tick",
		"--target", tmp,
		"--dry-run",
		"--skip-reconcile",
		"--skip-drain",
		"--skip-advance",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick schedule dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick schedule dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Schedule == nil || !result.Schedule.DryRun || result.Schedule.WouldFire != 1 {
		t.Fatalf("tick schedule result = %+v", result)
	}
	if len(result.Schedule.Schedules) != 1 || result.Schedule.Schedules[0].Name != "nightly" || result.Schedule.Schedules[0].Reason != "run_on_start" {
		t.Fatalf("tick schedule rows = %+v", result.Schedule.Schedules)
	}
	if result.Reconcile != nil || result.Queue != nil || result.Advance != nil {
		t.Fatalf("skipped tick sections = %+v", result)
	}
	if _, err := daemon.ReadScheduleState(daemon.DaemonRoot(teamDir), "nightly"); !os.IsNotExist(err) {
		t.Fatalf("tick dry-run wrote schedule state, err=%v", err)
	}
}

func TestTickWatchRendersCanceledSnapshot(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	if err := runTickLoop(ctx, cmd, teamDir, "repo", 0, tickOptions{
		SkipReconcile: true,
		SkipSchedules: true,
		SkipDrain:     true,
		SkipAdvance:   true,
	}, true, nil, time.Millisecond); err != nil {
		t.Fatalf("tick watch loop: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode watch tick json: %v\nbody=%s", err, out.String())
	}
	if result.Reconcile != nil || result.Schedule != nil || result.Queue != nil || result.Advance != nil {
		t.Fatalf("watch result = %+v, want skipped sections", result)
	}
}
