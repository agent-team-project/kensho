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
