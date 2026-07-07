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

	"github.com/agent-team-project/agent-team/internal/daemon"
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

	stdout, stderr, err = executeInboxCommand("--repo", tmp, "inbox", "ls", "--commands")
	if err != nil {
		t.Fatalf("inbox ls --commands: %v\nstderr=%s", err, stderr)
	}
	wantCommands := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "check", "future-worker", "--repo", tmp}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "check", "manager", "--repo", tmp}), " "),
	}, "\n")
	if got := strings.TrimSpace(stdout); got != wantCommands {
		t.Fatalf("inbox ls --commands = %q, want %q", got, wantCommands)
	}
}

func TestInboxLsSortAndLimit(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	messages := map[string][]*daemon.Message{
		"worker-a": {
			{ID: "a-1", From: "tester", Body: "one", TS: now},
			{ID: "a-2", From: "tester", Body: "two", TS: now.Add(9 * time.Minute)},
		},
		"worker-b": {
			{ID: "b-1", From: "tester", Body: "one", TS: now.Add(time.Minute)},
			{ID: "b-2", From: "tester", Body: "two", TS: now.Add(2 * time.Minute)},
			{ID: "b-3", From: "tester", Body: "three", TS: now.Add(5 * time.Minute)},
		},
		"worker-c": {
			{ID: "c-1", From: "tester", Body: "one", TS: now.Add(3 * time.Minute)},
			{ID: "c-2", From: "tester", Body: "two", TS: now.Add(4 * time.Minute)},
			{ID: "c-3", From: "tester", Body: "three", TS: now.Add(6 * time.Minute)},
		},
	}
	for instance, items := range messages {
		for _, msg := range items {
			if err := daemon.AppendMessage(root, instance, msg); err != nil {
				t.Fatalf("append %s/%s: %v", instance, msg.ID, err)
			}
		}
	}
	for instance, cursor := range map[string]string{"worker-a": "a-1", "worker-b": "b-1"} {
		if err := daemon.WriteCursor(root, instance, cursor); err != nil {
			t.Fatalf("write cursor %s: %v", instance, err)
		}
	}

	stdout, stderr, err := executeInboxCommand("inbox", "ls", "--target", tmp, "--sort", "unread", "--limit", "2", "--format", "{{.Instance}} {{.Unread}}")
	if err != nil {
		t.Fatalf("inbox ls sort unread: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "worker-c 3\nworker-b 2"; got != want {
		t.Fatalf("inbox ls sort unread = %q, want %q", got, want)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "ls", "--target", tmp, "--sort", "latest", "--limit", "2", "--format", "{{.Instance}} {{.LatestID}}")
	if err != nil {
		t.Fatalf("inbox ls sort latest: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "worker-a a-2\nworker-c c-3"; got != want {
		t.Fatalf("inbox ls sort latest = %q, want %q", got, want)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "ls", "--target", tmp, "--sort", "unread", "--limit", "1", "--commands")
	if err != nil {
		t.Fatalf("inbox ls sorted commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "check", "worker-c", "--repo", tmp}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("inbox ls sorted commands = %q, want %q", got, wantCommand)
	}
}

func TestInboxLsCommandsValidation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "rejects json",
			args: []string{"inbox", "ls", "--target", tmp, "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "rejects format",
			args: []string{"inbox", "ls", "--target", tmp, "--commands", "--format", "{{.Instance}}"},
			want: wantCommandsModeConflict("--format"),
		},
		{
			name: "rejects negative limit",
			args: []string{"inbox", "ls", "--target", tmp, "--limit", "-1"},
			want: "--limit must be >= 0",
		},
		{
			name: "rejects unknown sort",
			args: []string{"inbox", "ls", "--target", tmp, "--sort", "status"},
			want: "--sort must be instance, unread, latest, or total",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := executeInboxCommand(tt.args...)
			var code ExitCode
			if !errors.As(err, &code) || code != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr, tt.want)
			}
		})
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

	stdout, stderr, err = executeInboxCommand("inbox", "show", "worker", "--target", tmp, "--unread", "--commands")
	if err != nil {
		t.Fatalf("inbox show unread commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "ack", "worker", "msg-2", "--repo", tmp}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("inbox show unread commands = %q, want %q", got, wantCommand)
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

	stdout, stderr, err = executeInboxCommand("inbox", "ack", "worker", "msg-2", "--target", tmp, "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("inbox ack dry-run commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand = strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "ack", "worker", "msg-2", "--repo", tmp}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("inbox ack dry-run commands = %q, want %q", got, wantCommand)
	}

	stdout, stderr, err = executeInboxCommand("--repo", tmp, "inbox", "ack", "worker", "--all", "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("inbox ack --repo dry-run commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand = strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "ack", "worker", "--all", "--repo", tmp}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("inbox ack --repo dry-run commands = %q, want %q", got, wantCommand)
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

	stdout, stderr, err = executeInboxCommand("inbox", "ack", "worker", "msg-2", "--target", tmp, "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("inbox ack no-op dry-run commands: %v\nstderr=%s", err, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "" {
		t.Fatalf("inbox ack no-op dry-run commands = %q, want empty", got)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "show", "worker", "--target", tmp, "--unread", "--commands")
	if err != nil {
		t.Fatalf("inbox show no-op commands: %v\nstderr=%s", err, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "" {
		t.Fatalf("inbox show no-op commands = %q, want empty", got)
	}
}

func TestInboxCheckDefaultsToSelfAndSuggestsFirstUnreadAck(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	for _, msg := range []*daemon.Message{
		{ID: "msg-1", From: "manager", Body: "first", TS: now},
		{ID: "msg-2", From: "reviewer", Body: "second", TS: now.Add(time.Minute)},
	} {
		if err := daemon.AppendMessage(root, "worker", msg); err != nil {
			t.Fatalf("append %s: %v", msg.ID, err)
		}
	}
	t.Setenv("AGENT_TEAM_INSTANCE", "worker")

	stdout, stderr, err := executeInboxCommand("inbox", "check", "--target", tmp, "--json")
	if err != nil {
		t.Fatalf("inbox check self: %v\nstderr=%s", err, stderr)
	}
	var messages []inboxMessageRow
	if err := json.Unmarshal([]byte(stdout), &messages); err != nil {
		t.Fatalf("decode check messages: %v\nbody=%s", err, stdout)
	}
	if len(messages) != 2 || messages[0].ID != "msg-1" || messages[1].ID != "msg-2" || !messages[0].Unread || !messages[1].Unread {
		t.Fatalf("check messages = %+v", messages)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "check", "--self", "--target", tmp, "--commands")
	if err != nil {
		t.Fatalf("inbox check commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "ack", "worker", "msg-1", "--repo", tmp}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("inbox check commands = %q, want %q", got, wantCommand)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "check", "worker", "--target", tmp, "--tail", "1", "--commands")
	if err != nil {
		t.Fatalf("inbox check tail commands: %v\nstderr=%s", err, stderr)
	}
	wantTailCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "ack", "worker", "msg-2", "--repo", tmp}), " ")
	if got := strings.TrimSpace(stdout); got != wantTailCommand {
		t.Fatalf("inbox check tail commands = %q, want first displayed unread %q", got, wantTailCommand)
	}
}

func TestInboxCheckMissingInboxUsesCheckErrorPrefix(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	_, stderr, err := executeInboxCommand("inbox", "check", "missing", "--target", tmp)
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if got, want := strings.TrimSpace(stderr), "agent-team inbox check: no such inbox: missing"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestInboxAckByIDRequiresNextUnreadMessage(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	for _, id := range []string{"msg-1", "msg-2", "msg-3"} {
		if err := daemon.AppendMessage(root, "worker", &daemon.Message{ID: id, Body: id}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	t.Setenv("AGENT_TEAM_INSTANCE", "worker")

	_, stderr, err := executeInboxCommand("inbox", "ack", "msg-2", "--target", tmp)
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr, `message id "msg-2" is not the next unread message`) || !strings.Contains(stderr, `handle "msg-1" first`) {
		t.Fatalf("stderr = %q, want ordered ack hint", stderr)
	}
	cursor, err := daemon.ReadCursor(root, "worker")
	if err != nil {
		t.Fatalf("read cursor after rejected ack: %v", err)
	}
	if cursor != "" {
		t.Fatalf("cursor after rejected ack = %q, want empty", cursor)
	}

	stdout, stderr, err := executeInboxCommand("inbox", "ack", "msg-1", "--target", tmp, "--json")
	if err != nil {
		t.Fatalf("inbox ack next unread: %v\nstderr=%s", err, stderr)
	}
	var ack inboxAckResult
	if err := json.Unmarshal([]byte(stdout), &ack); err != nil {
		t.Fatalf("decode ack: %v\nbody=%s", err, stdout)
	}
	if ack.Acked != 1 || ack.CursorAfter != "msg-1" || ack.UnreadAfter != 2 {
		t.Fatalf("ack msg-1 = %+v", ack)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "ack", "--all", "--self", "--target", tmp, "--json")
	if err != nil {
		t.Fatalf("inbox ack all: %v\nstderr=%s", err, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &ack); err != nil {
		t.Fatalf("decode ack all: %v\nbody=%s", err, stdout)
	}
	if !ack.All || ack.Acked != 2 || ack.CursorAfter != "msg-3" || ack.UnreadAfter != 0 {
		t.Fatalf("ack all = %+v", ack)
	}
}

func TestInboxSendDirectMessage(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	stdout, stderr, err := executeInboxCommand("inbox", "send", "manager", "hello", "--target", tmp, "--json")
	if err != nil {
		t.Fatalf("inbox send: %v\nstderr=%s", err, stderr)
	}
	var sent sendJSON
	if err := json.Unmarshal([]byte(stdout), &sent); err != nil {
		t.Fatalf("decode send: %v\nbody=%s", err, stdout)
	}
	if !sent.Delivered || sent.To != "manager" || sent.From != "(cli)" || sent.ID == "" {
		t.Fatalf("send result = %+v", sent)
	}
	messages, err := daemon.ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "hello" {
		t.Fatalf("messages = %+v", messages)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "send", "manager", "preview", "--target", tmp, "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("inbox send commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "send", "manager", "--repo", tmp, "preview"}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("inbox send commands = %q, want %q", got, wantCommand)
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

func TestInboxAckCommandsValidation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	if err := daemon.AppendMessage(root, "worker", &daemon.Message{ID: "msg-1", Body: "hello"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "requires dry run",
			args: []string{"inbox", "ack", "worker", "msg-1", "--target", tmp, "--commands"},
			want: wantCommandsModeRequiresDryRun(),
		},
		{
			name: "rejects json",
			args: []string{"inbox", "ack", "worker", "msg-1", "--target", tmp, "--dry-run", "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "rejects format",
			args: []string{"inbox", "ack", "worker", "msg-1", "--target", tmp, "--dry-run", "--commands", "--format", "{{.Acked}}"},
			want: wantCommandsModeConflict("--format"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := executeInboxCommand(tt.args...)
			var code ExitCode
			if !errors.As(err, &code) || code != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr, tt.want)
			}
		})
	}
}

func TestInboxShowCommandsValidation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	if err := daemon.AppendMessage(root, "worker", &daemon.Message{ID: "msg-1", Body: "hello"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "rejects json",
			args: []string{"inbox", "show", "worker", "--target", tmp, "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "rejects format",
			args: []string{"inbox", "show", "worker", "--target", tmp, "--commands", "--format", "{{.ID}}"},
			want: wantCommandsModeConflict("--format"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := executeInboxCommand(tt.args...)
			var code ExitCode
			if !errors.As(err, &code) || code != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr, tt.want)
			}
		})
	}
}

func TestInboxPruneCompactsAcknowledgedMessages(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	for _, msg := range []*daemon.Message{
		{ID: "msg-1", From: "tester", Body: "first", TS: now.Add(-3 * time.Hour)},
		{ID: "msg-2", From: "tester", Body: "cursor", TS: now.Add(-2 * time.Hour)},
		{ID: "msg-3", From: "tester", Body: "third", TS: now.Add(-time.Hour)},
		{ID: "msg-4", From: "tester", Body: "fourth", TS: now},
	} {
		if err := daemon.AppendMessage(root, "worker", msg); err != nil {
			t.Fatalf("append %s: %v", msg.ID, err)
		}
	}
	if err := daemon.WriteCursor(root, "worker", "msg-2"); err != nil {
		t.Fatalf("write cursor: %v", err)
	}

	stdout, stderr, err := executeInboxCommand("inbox", "prune", "worker", "--target", tmp, "--dry-run", "--json")
	if err != nil {
		t.Fatalf("inbox prune dry-run: %v\nstderr=%s", err, stderr)
	}
	var dry []inboxPruneResult
	if err := json.Unmarshal([]byte(stdout), &dry); err != nil {
		t.Fatalf("decode prune dry-run: %v\nbody=%s", err, stdout)
	}
	if len(dry) != 1 || !dry[0].DryRun || dry[0].Dropped != 1 || dry[0].Kept != 3 || dry[0].Unread != 2 || dry[0].Action != "would-prune" {
		t.Fatalf("dry-run prune = %+v", dry)
	}
	messages, err := daemon.ReadMessages(root, "worker")
	if err != nil {
		t.Fatalf("read after dry-run: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("dry-run rewrote messages: got %d want 4", len(messages))
	}

	stdout, stderr, err = executeInboxCommand("inbox", "prune", "worker", "--target", tmp, "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("inbox prune commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "prune", "worker", "--repo", tmp}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("inbox prune commands = %q, want %q", got, wantCommand)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "prune", "worker", "--target", tmp, "--format", "{{.Instance}} {{.Dropped}} {{.Action}}")
	if err != nil {
		t.Fatalf("inbox prune: %v\nstderr=%s", err, stderr)
	}
	if got, want := strings.TrimSpace(stdout), "worker 1 pruned"; got != want {
		t.Fatalf("inbox prune format = %q, want %q", got, want)
	}
	messages, err = daemon.ReadMessages(root, "worker")
	if err != nil {
		t.Fatalf("read after prune: %v", err)
	}
	gotIDs := []string{}
	for _, msg := range messages {
		gotIDs = append(gotIDs, msg.ID)
	}
	if strings.Join(gotIDs, ",") != "msg-2,msg-3,msg-4" {
		t.Fatalf("messages after prune = %v", gotIDs)
	}
	unread, err := daemon.ReadUnacked(root, "worker")
	if err != nil {
		t.Fatalf("read unacked after prune: %v", err)
	}
	if len(unread) != 2 || unread[0].ID != "msg-3" || unread[1].ID != "msg-4" {
		t.Fatalf("unread after prune = %+v", unread)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "prune", "worker", "--target", tmp, "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("inbox prune no-op commands: %v\nstderr=%s", err, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "" {
		t.Fatalf("inbox prune no-op commands = %q, want empty", got)
	}
}

func TestInboxPruneOlderThanKeepsRecentAcknowledgedMessages(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	now := time.Now().UTC()
	for _, msg := range []*daemon.Message{
		{ID: "old-acked", Body: "old", TS: now.Add(-48 * time.Hour)},
		{ID: "recent-acked", Body: "recent", TS: now.Add(-time.Hour)},
		{ID: "cursor", Body: "cursor", TS: now.Add(-30 * time.Minute)},
		{ID: "unread", Body: "unread", TS: now},
	} {
		if err := daemon.AppendMessage(root, "worker", msg); err != nil {
			t.Fatalf("append %s: %v", msg.ID, err)
		}
	}
	if err := daemon.WriteCursor(root, "worker", "cursor"); err != nil {
		t.Fatalf("write cursor: %v", err)
	}

	stdout, stderr, err := executeInboxCommand("inbox", "prune", "worker", "--target", tmp, "--older-than", "24h", "--json")
	if err != nil {
		t.Fatalf("inbox prune older-than: %v\nstderr=%s", err, stderr)
	}
	var rows []inboxPruneResult
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("decode prune older-than: %v\nbody=%s", err, stdout)
	}
	if len(rows) != 1 || rows[0].Dropped != 1 || rows[0].Kept != 3 {
		t.Fatalf("older-than prune rows = %+v", rows)
	}
	messages, err := daemon.ReadMessages(root, "worker")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	gotIDs := []string{}
	for _, msg := range messages {
		gotIDs = append(gotIDs, msg.ID)
	}
	if strings.Join(gotIDs, ",") != "recent-acked,cursor,unread" {
		t.Fatalf("messages after older-than prune = %v", gotIDs)
	}
}

func TestInboxPruneLimitBoundsDroppedMessagesPerInbox(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	for _, id := range []string{"msg-1", "msg-2", "msg-3", "msg-4", "msg-5"} {
		if err := daemon.AppendMessage(root, "worker", &daemon.Message{ID: id, Body: id}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	if err := daemon.WriteCursor(root, "worker", "msg-4"); err != nil {
		t.Fatalf("write cursor: %v", err)
	}

	stdout, stderr, err := executeInboxCommand("inbox", "prune", "worker", "--target", tmp, "--limit", "2", "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("inbox prune limit commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "prune", "worker", "--repo", tmp, "--limit", "2"}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("inbox prune limit commands = %q, want %q", got, wantCommand)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "prune", "worker", "--target", tmp, "--limit", "2", "--json")
	if err != nil {
		t.Fatalf("inbox prune limit: %v\nstderr=%s", err, stderr)
	}
	var rows []inboxPruneResult
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("decode prune limit: %v\nbody=%s", err, stdout)
	}
	if len(rows) != 1 || rows[0].Dropped != 2 || rows[0].Kept != 3 || rows[0].Unread != 1 {
		t.Fatalf("limit prune rows = %+v", rows)
	}
	messages, err := daemon.ReadMessages(root, "worker")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	gotIDs := []string{}
	for _, msg := range messages {
		gotIDs = append(gotIDs, msg.ID)
	}
	if strings.Join(gotIDs, ",") != "msg-3,msg-4,msg-5" {
		t.Fatalf("messages after limit prune = %v", gotIDs)
	}
}

func TestInboxPruneAllTeamCommandsScopesInboxes(t *testing.T) {
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
	for _, instance := range []string{"manager", "worker-squ-1", "outsider"} {
		for _, id := range []string{"a", "b"} {
			msgID := instance + "-" + id
			if err := daemon.AppendMessage(root, instance, &daemon.Message{ID: msgID, Body: msgID}); err != nil {
				t.Fatalf("append %s: %v", msgID, err)
			}
		}
		if err := daemon.WriteCursor(root, instance, instance+"-b"); err != nil {
			t.Fatalf("cursor %s: %v", instance, err)
		}
	}

	stdout, stderr, err := executeInboxCommand("inbox", "prune", "--target", tmp, "--all", "--team", "delivery", "--dry-run", "--commands")
	if err != nil {
		t.Fatalf("team prune commands: %v\nstderr=%s", err, stderr)
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "prune", "--repo", tmp, "--all", "--team", "delivery"}), " ")
	if got := strings.TrimSpace(stdout); got != wantCommand {
		t.Fatalf("team prune commands = %q, want %q", got, wantCommand)
	}

	stdout, stderr, err = executeInboxCommand("inbox", "prune", "--target", tmp, "--all", "--team", "delivery", "--json")
	if err != nil {
		t.Fatalf("team prune: %v\nstderr=%s", err, stderr)
	}
	var rows []inboxPruneResult
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("decode team prune: %v\nbody=%s", err, stdout)
	}
	if len(rows) != 2 || rows[0].Instance != "manager" || rows[1].Instance != "worker-squ-1" {
		t.Fatalf("team prune rows = %+v", rows)
	}
	outsider, err := daemon.ReadMessages(root, "outsider")
	if err != nil {
		t.Fatalf("read outsider: %v", err)
	}
	if len(outsider) != 2 {
		t.Fatalf("outsider messages = %d, want untouched 2", len(outsider))
	}
}

func TestInboxPruneValidation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	root := daemon.DaemonRoot(filepath.Join(tmp, ".agent_team"))
	if err := daemon.AppendMessage(root, "worker", &daemon.Message{ID: "msg-1", Body: "hello"}); err != nil {
		t.Fatalf("append message: %v", err)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "requires target",
			args: []string{"inbox", "prune", "--target", tmp},
			want: "instance or --all is required",
		},
		{
			name: "all rejects instances",
			args: []string{"inbox", "prune", "worker", "--target", tmp, "--all"},
			want: "--all cannot be combined with explicit instances",
		},
		{
			name: "team requires all",
			args: []string{"inbox", "prune", "worker", "--target", tmp, "--team", "delivery"},
			want: "--team requires --all",
		},
		{
			name: "commands require dry run",
			args: []string{"inbox", "prune", "worker", "--target", tmp, "--commands"},
			want: wantCommandsModeRequiresDryRun(),
		},
		{
			name: "commands reject json",
			args: []string{"inbox", "prune", "worker", "--target", tmp, "--dry-run", "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "commands reject format",
			args: []string{"inbox", "prune", "worker", "--target", tmp, "--dry-run", "--commands", "--format", "{{.Instance}}"},
			want: wantCommandsModeConflict("--format"),
		},
		{
			name: "rejects negative older-than",
			args: []string{"inbox", "prune", "worker", "--target", tmp, "--older-than", "-1s"},
			want: "--older-than must be >= 0",
		},
		{
			name: "rejects negative limit",
			args: []string{"inbox", "prune", "worker", "--target", tmp, "--limit", "-1"},
			want: "--limit must be >= 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := executeInboxCommand(tt.args...)
			var code ExitCode
			if !errors.As(err, &code) || code != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr, tt.want)
			}
		})
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

	stdout, stderr, err = executeInboxCommand("inbox", "ls", "--target", tmp, "--team", "delivery", "--commands")
	if err != nil {
		t.Fatalf("inbox ls --team --commands: %v\nstderr=%s", err, stderr)
	}
	wantCommands := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "check", "manager", "--repo", tmp}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "inbox", "check", "worker-squ-1", "--repo", tmp}), " "),
	}, "\n")
	if got := strings.TrimSpace(stdout); got != wantCommands {
		t.Fatalf("team inbox commands = %q, want %q", got, wantCommands)
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
