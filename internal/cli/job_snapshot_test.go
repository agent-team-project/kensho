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

func TestJobSnapshotCapturesPostMortemRuntimeState(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-160",
		Ticket:    "SQU-160",
		Target:    "worker",
		Instance:  "worker-squ-160",
		Status:    job.StatusDone,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
		LastEvent: "instance_exited",
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	if err := job.AppendSnapshotEvent(teamDir, j, "created", "test", "created", nil); err != nil {
		t.Fatalf("append created event: %v", err)
	}
	if err := job.AppendSnapshotEvent(teamDir, j, "instance_exited", "daemon", "done", map[string]string{"instance": j.Instance}); err != nil {
		t.Fatalf("append exit event: %v", err)
	}

	stateDir := filepath.Join(teamDir, "state", j.Instance)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	writeStatus(t, stateDir, `[status]
phase = "done"
description = "complete"
`, now)
	writeLastMessageForTest(t, teamDir, j.Instance, "clean final")

	root := daemon.DaemonRoot(teamDir)
	exitCode := 0
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:      j.Instance,
		Agent:         "worker",
		Job:           j.ID,
		Ticket:        j.Ticket,
		Runtime:       "codex",
		RuntimeBinary: "codex-dev",
		Workspace:     tmp,
		Status:        daemon.StatusExited,
		StartedAt:     now.Add(-30 * time.Minute),
		ExitedAt:      now,
		ExitCode:      &exitCode,
		LogPath:       filepath.Join(root, j.Instance, "child.log"),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeChildLogForTest(t, root, j.Instance, "first\nsecond\nthird\n")
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "dispatch-160",
		TS:       now.Add(-30 * time.Minute),
		Action:   "dispatch",
		Instance: j.Instance,
		Agent:    "worker",
		Job:      j.ID,
		Ticket:   j.Ticket,
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("append dispatch lifecycle: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "exit-160",
		TS:       now,
		Action:   "exit",
		Instance: j.Instance,
		Agent:    "worker",
		Job:      j.ID,
		Ticket:   j.Ticket,
		Status:   daemon.StatusExited,
		ExitCode: &exitCode,
	}); err != nil {
		t.Fatalf("append exit lifecycle: %v", err)
	}
	if err := daemon.WriteQueueItem(root, &daemon.QueueItem{
		ID:         "q-160",
		State:      daemon.QueueStateDead,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: j.Instance,
		Payload:    map[string]any{"job_id": j.ID, "target": "worker", "api_key": "secret-key"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("write queue: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "snapshot", "SQU-160", "--repo", tmp, "--events", "-1", "--tail", "2", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job snapshot: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot jobSnapshotResult
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\nbody=%s", err, out.String())
	}
	if snapshot.Job == nil || snapshot.Job.ID != j.ID || snapshot.Instance != j.Instance {
		t.Fatalf("job snapshot identity = %+v", snapshot)
	}
	if snapshot.Runtime == nil || snapshot.Runtime.Lifecycle != "exited" || snapshot.Runtime.Runtime != "codex" || snapshot.Runtime.ExitCode == nil || *snapshot.Runtime.ExitCode != 0 {
		t.Fatalf("runtime = %+v", snapshot.Runtime)
	}
	if snapshot.State == nil || !snapshot.State.Exists || snapshot.Status == nil || snapshot.Status.Phase != "done" {
		t.Fatalf("state/status = %+v / %+v", snapshot.State, snapshot.Status)
	}
	if snapshot.Log == nil || !snapshot.Log.Exists || snapshot.Log.Tail != "second\nthird\n" {
		t.Fatalf("log = %+v", snapshot.Log)
	}
	if snapshot.LastMessage == nil || !snapshot.LastMessage.Exists || snapshot.LastMessage.Tail != "clean final" {
		t.Fatalf("last message = %+v", snapshot.LastMessage)
	}
	if len(snapshot.JobEvents) != 2 || len(snapshot.LifecycleEvents) != 2 {
		t.Fatalf("events: job=%d lifecycle=%d", len(snapshot.JobEvents), len(snapshot.LifecycleEvents))
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].Payload["api_key"] != snapshotRedactedValue {
		t.Fatalf("queue not redacted: %+v", snapshot.Queue)
	}
	for _, want := range []string{
		"agent-team inspect worker-squ-160",
		"agent-team job logs squ-160 --tail 100",
		"agent-team job logs squ-160 --last-message",
		"agent-team job queue squ-160 --summary",
	} {
		if !containsString(snapshot.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, snapshot.Actions)
		}
	}
}

func TestJobSnapshotHumanSummaryAndOutputFile(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-161",
		Ticket:    "SQU-161",
		Target:    "worker",
		Status:    job.StatusFailed,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}

	summary := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(out)
	summary.SetErr(stderr)
	summary.SetArgs([]string{"job", "snapshot", "squ-161", "--repo", tmp})
	if err := summary.Execute(); err != nil {
		t.Fatalf("job snapshot summary: %v\nstderr=%s", err, stderr.String())
	}
	for _, want := range []string{"job snapshot:", "job: squ-161", "events: job=0 lifecycle=0", "actions:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, out.String())
		}
	}

	outputPath := filepath.Join(tmp, "snapshots", "job.json")
	fileCmd := NewRootCmd()
	fileOut, fileErr := &bytes.Buffer{}, &bytes.Buffer{}
	fileCmd.SetOut(fileOut)
	fileCmd.SetErr(fileErr)
	fileCmd.SetArgs([]string{"job", "snapshot", "squ-161", "--repo", tmp, "--output", outputPath})
	if err := fileCmd.Execute(); err != nil {
		t.Fatalf("job snapshot output: %v\nstderr=%s", err, fileErr.String())
	}
	if !strings.Contains(fileOut.String(), "Wrote job snapshot to ") {
		t.Fatalf("output message = %q", fileOut.String())
	}
	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var snapshot jobSnapshotResult
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatalf("decode output: %v\nbody=%s", err, string(body))
	}
	if snapshot.Job == nil || snapshot.Job.ID != "squ-161" {
		t.Fatalf("output snapshot = %+v", snapshot)
	}
}
