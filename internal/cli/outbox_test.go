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
)

func TestOutboxListShowRetryDrop(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-a",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-501", "ticket": "SQU-501", "target": "worker"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-b",
		State:     daemon.OutboxStateFailed,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-502", "ticket": "SQU-502", "target": "worker"},
		Source:    "manager",
		LastError: "no route",
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
	})

	out := runRootForOutboxTest(t, "outbox", "ls", "--target", target, "--json")
	var listed []*daemon.OutboxItem
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("decode outbox ls: %v\n%s", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != "outbox-a" || listed[1].ID != "outbox-b" {
		t.Fatalf("outbox list = %+v, want outbox-a/outbox-b", listed)
	}

	filtered := runRootForOutboxTest(t, "outbox", "ls", "--target", target, "--state", "pending", "--job", "SQU-501", "--format", "{{.ID}} {{.State}}")
	if strings.TrimSpace(filtered.String()) != "outbox-a pending" {
		t.Fatalf("filtered output = %q", filtered.String())
	}

	shown := runRootForOutboxTest(t, "outbox", "show", "--target", target, "outbox-b", "--json")
	var shownItem daemon.OutboxItem
	if err := json.Unmarshal(shown.Bytes(), &shownItem); err != nil {
		t.Fatalf("decode outbox show: %v\n%s", err, shown.String())
	}
	if shownItem.ID != "outbox-b" || shownItem.State != daemon.OutboxStateFailed || shownItem.LastError != "no route" {
		t.Fatalf("shown item = %+v", shownItem)
	}

	retry := runRootForOutboxTest(t, "outbox", "retry", "--target", target, "outbox-b", "--json")
	var retryRows []outboxActionResult
	if err := json.Unmarshal(retry.Bytes(), &retryRows); err != nil {
		t.Fatalf("decode retry: %v\n%s", err, retry.String())
	}
	if len(retryRows) != 1 || retryRows[0].Action != "retried" || retryRows[0].State != daemon.OutboxStatePending {
		t.Fatalf("retry rows = %+v", retryRows)
	}
	retried, err := daemon.ReadOutboxItem(teamDir, "outbox-b")
	if err != nil {
		t.Fatalf("read retried item: %v", err)
	}
	if retried.State != daemon.OutboxStatePending || retried.LastError != "" {
		t.Fatalf("retried item = %+v, want pending with cleared error", retried)
	}

	drop := runRootForOutboxTest(t, "outbox", "drop", "--target", target, "outbox-a", "--json")
	var dropRows []outboxActionResult
	if err := json.Unmarshal(drop.Bytes(), &dropRows); err != nil {
		t.Fatalf("decode drop: %v\n%s", err, drop.String())
	}
	if len(dropRows) != 1 || dropRows[0].Action != "dropped" {
		t.Fatalf("drop rows = %+v", dropRows)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outbox-a should be removed, err=%v", err)
	}
}

func TestOutboxDrainDryRunOffline(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 12, 30, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-offline",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"job_id": "squ-503", "target": "worker"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})

	out := runRootForOutboxTest(t, "outbox", "drain", "--target", target, "--dry-run", "--json")
	var result daemon.OutboxDrainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode drain dry-run: %v\n%s", err, out.String())
	}
	if !result.DryRun || result.WouldPublish != 1 || result.Pending != 1 || result.Published != 0 {
		t.Fatalf("dry-run result = %+v", result)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-offline"); err != nil {
		t.Fatalf("dry-run removed outbox item: %v", err)
	}
}

func TestOutboxDrainThroughDaemon(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 13, 0, 0, 0, time.UTC)
	writeCLIOutboxItem(t, teamDir, &daemon.OutboxItem{
		ID:        "outbox-daemon",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"target": "worker", "name": "worker-squ-504", "ticket": "SQU-504", "workspace": "repo"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	})

	out := runRootForOutboxTest(t, "outbox", "drain", "--target", target, "--json")
	var result daemon.OutboxDrainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode outbox drain: %v\n%s", err, out.String())
	}
	if result.Published != 1 || result.Pending != 0 || result.Processed != 1 {
		t.Fatalf("drain result = %+v", result)
	}
	processed, err := daemon.ReadOutboxItem(teamDir, "outbox-daemon")
	if err != nil {
		t.Fatalf("read processed item: %v", err)
	}
	if processed.State != daemon.OutboxStateProcessed {
		t.Fatalf("processed state = %s, want processed", processed.State)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-504")
}

func writeCLIOutboxItem(t *testing.T, teamDir string, item *daemon.OutboxItem) {
	t.Helper()
	if err := daemon.WriteOutboxItem(teamDir, item); err != nil {
		t.Fatalf("WriteOutboxItem(%s): %v", item.ID, err)
	}
}

func runRootForOutboxTest(t *testing.T, args ...string) *bytes.Buffer {
	t.Helper()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agent-team %s: %v\nstderr=%s\nstdout=%s", strings.Join(args, " "), err, stderr.String(), out.String())
	}
	return out
}
