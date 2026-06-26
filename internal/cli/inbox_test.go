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

func TestInboxLsJSONIncludesMetadataAndBareMailbox(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		StartedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := daemon.AppendMessage(root, "manager", &daemon.Message{
		ID:   "msg-manager",
		From: "tester",
		Body: "metadata backed message",
		TS:   now,
	}); err != nil {
		t.Fatalf("append manager message: %v", err)
	}
	if err := daemon.AppendMessage(root, "future-worker", &daemon.Message{
		ID:   "msg-future",
		From: "tester",
		Body: "queued before metadata exists",
		TS:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("append future message: %v", err)
	}

	stdout, stderr, err := executeInboxCommand("inbox", "ls", "--target", tmp, "--json")
	if err != nil {
		t.Fatalf("inbox ls: %v\nstderr=%s", err, stderr)
	}
	var rows []inboxSummaryRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("decode inbox rows: %v\nbody=%s", err, stdout)
	}
	manager := findInboxSummary(rows, "manager")
	if manager == nil {
		t.Fatalf("manager row missing: %+v", rows)
	}
	if !manager.HasMetadata || manager.Agent != "manager" || manager.Status != string(daemon.StatusStopped) || manager.Total != 1 || manager.Unread != 1 {
		t.Fatalf("manager row = %+v", manager)
	}
	future := findInboxSummary(rows, "future-worker")
	if future == nil {
		t.Fatalf("future-worker row missing: %+v", rows)
	}
	if future.HasMetadata || future.Total != 1 || future.Unread != 1 || future.LatestID != "msg-future" {
		t.Fatalf("future-worker row = %+v", future)
	}
}

func TestInboxShowUnreadAndAckCursor(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	for _, msg := range []*daemon.Message{
		{ID: "msg-1", From: "tester", Body: "first", TS: now},
		{ID: "msg-2", From: "tester", Body: "second", TS: now.Add(time.Minute)},
	} {
		if err := daemon.AppendMessage(root, "worker", msg); err != nil {
			t.Fatalf("append message %s: %v", msg.ID, err)
		}
	}
	if err := daemon.WriteCursor(root, "worker", "msg-1"); err != nil {
		t.Fatalf("write cursor: %v", err)
	}

	stdout, stderr, err := executeInboxCommand("inbox", "show", "worker", "--target", tmp, "--unread", "--json")
	if err != nil {
		t.Fatalf("inbox show unread: %v\nstderr=%s", err, stderr)
	}
	var messages []inboxMessageRow
	if err := json.Unmarshal([]byte(stdout), &messages); err != nil {
		t.Fatalf("decode unread messages: %v\nbody=%s", err, stdout)
	}
	if len(messages) != 1 || messages[0].ID != "msg-2" || !messages[0].Unread {
		t.Fatalf("unread messages = %+v", messages)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "ack", "worker", "msg-2", "--target", tmp, "--dry-run", "--json")
	if err != nil {
		t.Fatalf("inbox ack dry-run: %v\nstderr=%s", err, stderr)
	}
	var dryRun inboxAckResult
	if err := json.Unmarshal([]byte(stdout), &dryRun); err != nil {
		t.Fatalf("decode dry-run ack: %v\nbody=%s", err, stdout)
	}
	if !dryRun.DryRun || dryRun.Acked != 1 || dryRun.CursorAfter != "msg-2" || dryRun.UnreadAfter != 0 {
		t.Fatalf("dry-run result = %+v", dryRun)
	}
	cursor, err := daemon.ReadCursor(root, "worker")
	if err != nil {
		t.Fatalf("read cursor after dry-run: %v", err)
	}
	if cursor != "msg-1" {
		t.Fatalf("cursor after dry-run = %q, want msg-1", cursor)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "ack", "worker", "msg-2", "--target", tmp, "--json")
	if err != nil {
		t.Fatalf("inbox ack: %v\nstderr=%s", err, stderr)
	}
	var ack inboxAckResult
	if err := json.Unmarshal([]byte(stdout), &ack); err != nil {
		t.Fatalf("decode ack: %v\nbody=%s", err, stdout)
	}
	if ack.Acked != 1 || !ack.CursorChanged || ack.CursorAfter != "msg-2" {
		t.Fatalf("ack result = %+v", ack)
	}
	cursor, err = daemon.ReadCursor(root, "worker")
	if err != nil {
		t.Fatalf("read cursor after ack: %v", err)
	}
	if cursor != "msg-2" {
		t.Fatalf("cursor after ack = %q, want msg-2", cursor)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "show", "worker", "--target", tmp, "--unread", "--json")
	if err != nil {
		t.Fatalf("inbox show after ack: %v\nstderr=%s", err, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &messages); err != nil {
		t.Fatalf("decode unread after ack: %v\nbody=%s", err, stdout)
	}
	if len(messages) != 0 {
		t.Fatalf("unread after ack = %+v, want none", messages)
	}
}

func TestInboxAckMissingMessageReturnsUsageError(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	if err := daemon.AppendMessage(root, "worker", &daemon.Message{ID: "msg-1", Body: "hello"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	_, stderr, err := executeInboxCommand("inbox", "ack", "worker", "missing", "--target", tmp)
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr, `message id "missing" not found`) {
		t.Fatalf("stderr = %q, want missing message hint", stderr)
	}
}

func TestInboxLsTeamScopesUnreadSummaries(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["manager", "worker"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	root := daemon.DaemonRoot(teamDir)
	for _, target := range []string{"manager", "worker-squ-1", "outsider"} {
		if err := daemon.AppendMessage(root, target, &daemon.Message{
			ID:   "msg-" + target,
			From: "tester",
			Body: "hello " + target,
		}); err != nil {
			t.Fatalf("append %s: %v", target, err)
		}
	}

	stdout, stderr, err := executeInboxCommand("inbox", "ls", "--target", tmp, "--team", "delivery", "--unread", "--json")
	if err != nil {
		t.Fatalf("inbox ls --team: %v\nstderr=%s", err, stderr)
	}
	var rows []inboxSummaryRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("decode team inbox rows: %v\nbody=%s", err, stdout)
	}
	if len(rows) != 2 || findInboxSummary(rows, "manager") == nil || findInboxSummary(rows, "worker-squ-1") == nil || findInboxSummary(rows, "outsider") != nil {
		t.Fatalf("team inbox rows = %+v", rows)
	}
}

func executeInboxCommand(args ...string) (string, string, error) {
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func findInboxSummary(rows []inboxSummaryRow, instance string) *inboxSummaryRow {
	for i := range rows {
		if rows[i].Instance == instance {
			return &rows[i]
		}
	}
	return nil
}
