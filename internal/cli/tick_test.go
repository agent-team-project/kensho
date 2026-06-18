package cli

import (
	"bytes"
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
