package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/feedback"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestFeedbackSubmitCapturesDispatchContext(t *testing.T) {
	root, teamDir := feedbackTestRepo(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-79",
		Ticket:    "SQU-79",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{{
			ID:       "implement",
			Target:   "worker",
			Status:   job.StatusRunning,
			Instance: "worker-squ-79",
			Runtime:  "codex",
		}},
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:      "worker-squ-79",
		Agent:         "worker",
		Job:           "squ-79",
		Ticket:        "SQU-79",
		Runtime:       "codex",
		RuntimeBinary: "codex",
		Workspace:     root,
		PID:           os.Getpid(),
		StartedAt:     now,
		Status:        daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	t.Setenv("AGENT_TEAM_ROOT", teamDir)
	t.Setenv("AGENT_TEAM_INSTANCE", "worker-squ-79")
	t.Setenv("AGENT_TEAM_JOB_ID", "squ-79")
	t.Setenv("AGENT_TEAM_TICKET", "SQU-79")
	t.Setenv("AGENT_TEAM_PIPELINE", "ticket_to_pr")
	t.Setenv("AGENT_TEAM_PIPELINE_STEP", "implement")
	t.Setenv("AGENT_TEAM_RUNTIME", "codex")

	out, stderr, err := runFeedbackCommand("feedback", "submit", "Harness instructions were unclear", "--category", "docs")
	if err != nil {
		t.Fatalf("feedback submit: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(out, "submitted fb-") {
		t.Fatalf("submit output = %q", out)
	}
	items, err := feedback.List(teamDir)
	if err != nil {
		t.Fatalf("list feedback: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	item := items[0]
	if item.Category != feedback.CategoryDocs || item.Body != "Harness instructions were unclear" {
		t.Fatalf("item = %+v", item)
	}
	if item.Context.Instance != "worker-squ-79" ||
		item.Context.Agent != "worker" ||
		item.Context.Job != "squ-79" ||
		item.Context.Ticket != "SQU-79" ||
		item.Context.Pipeline != "ticket_to_pr" ||
		item.Context.Step != "implement" ||
		item.Context.Runtime != "codex" {
		t.Fatalf("context = %+v", item.Context)
	}
}

func TestFeedbackListShowAndResolve(t *testing.T) {
	_, teamDir := feedbackTestRepo(t)
	t.Setenv("AGENT_TEAM_ROOT", teamDir)
	t.Setenv("AGENT_TEAM_INSTANCE", "")

	firstOut, stderr, err := runFeedbackCommand("feedback", "submit", "Repeated friction")
	if err != nil {
		t.Fatalf("submit first: %v\nstderr=%s", err, stderr)
	}
	secondOut, stderr, err := runFeedbackCommand("feedback", "submit", " repeated   friction ")
	if err != nil {
		t.Fatalf("submit second: %v\nstderr=%s", err, stderr)
	}
	firstID := submittedFeedbackID(t, firstOut)
	secondID := submittedFeedbackID(t, secondOut)

	groupOut, stderr, err := runFeedbackCommand("feedback", "ls", "--group")
	if err != nil {
		t.Fatalf("group ls: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(groupOut, "\t2\t") && !strings.Contains(groupOut, "  2  ") {
		t.Fatalf("group output missing count 2:\n%s", groupOut)
	}

	if _, stderr, err := runFeedbackCommand("feedback", "resolve", firstID, "--dismiss", "duplicate report"); err != nil {
		t.Fatalf("resolve dismiss: %v\nstderr=%s", err, stderr)
	}
	defaultOut, stderr, err := runFeedbackCommand("feedback", "ls")
	if err != nil {
		t.Fatalf("default ls: %v\nstderr=%s", err, stderr)
	}
	if strings.Contains(defaultOut, firstID) || !strings.Contains(defaultOut, secondID) {
		t.Fatalf("default ls = %q, want only unresolved %s", defaultOut, secondID)
	}
	dismissedOut, stderr, err := runFeedbackCommand("feedback", "ls", "--status", "dismissed")
	if err != nil {
		t.Fatalf("dismissed ls: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(dismissedOut, firstID) || strings.Contains(dismissedOut, secondID) {
		t.Fatalf("dismissed ls = %q, want only %s", dismissedOut, firstID)
	}
	showOut, stderr, err := runFeedbackCommand("feedback", "show", firstID)
	if err != nil {
		t.Fatalf("show: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(showOut, "reason: duplicate report") || !strings.Contains(showOut, "Status:      dismissed") {
		t.Fatalf("show output missing resolution:\n%s", showOut)
	}
}

func TestFeedbackSubmitFromLinkedWorktreeUsesPrimaryTeamDir(t *testing.T) {
	root, teamDir := feedbackTestRepo(t)
	gitWorktreeDir := filepath.Join(root, ".git", "worktrees", "worker")
	if err := os.MkdirAll(gitWorktreeDir, 0o755); err != nil {
		t.Fatalf("mkdir git worktree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitWorktreeDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatalf("write commondir: %v", err)
	}
	worktree := filepath.Join(root, ".claude", "worktrees", "worker")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitWorktreeDir+"\n"), 0o644); err != nil {
		t.Fatalf("write worktree git file: %v", err)
	}
	t.Setenv("AGENT_TEAM_ROOT", "")
	chdirForFeedbackTest(t, worktree)

	if _, stderr, err := runFeedbackCommand("feedback", "submit", "worktree submit routes home"); err != nil {
		t.Fatalf("feedback submit from worktree: %v\nstderr=%s", err, stderr)
	}
	items, err := feedback.List(teamDir)
	if err != nil {
		t.Fatalf("list primary feedback: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("primary items len = %d, want 1", len(items))
	}
	if _, err := os.Stat(filepath.Join(worktree, ".agent_team")); !os.IsNotExist(err) {
		t.Fatalf("worktree .agent_team exists or stat failed: %v", err)
	}
}

func TestFeedbackResolveRequiresOneDispositionCLI(t *testing.T) {
	_, teamDir := feedbackTestRepo(t)
	t.Setenv("AGENT_TEAM_ROOT", teamDir)
	out, stderr, err := runFeedbackCommand("feedback", "submit", "needs disposition")
	if err != nil {
		t.Fatalf("submit: %v\nstderr=%s", err, stderr)
	}
	id := submittedFeedbackID(t, out)
	if _, stderr, err := runFeedbackCommand("feedback", "resolve", id); err == nil {
		t.Fatalf("resolve without disposition succeeded; stderr=%s", stderr)
	}
	if _, stderr, err := runFeedbackCommand("feedback", "resolve", id, "--ticket", "SQU-80", "--dismiss", "no"); err == nil {
		t.Fatalf("resolve with both dispositions succeeded; stderr=%s", stderr)
	}
}

func feedbackTestRepo(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir .agent_team: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[runtime]\nkind = \"codex\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root, teamDir
}

func runFeedbackCommand(args ...string) (string, string, error) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func chdirForFeedbackTest(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore cwd %s: %v", old, err)
		}
	})
}

func submittedFeedbackID(t *testing.T, output string) string {
	t.Helper()
	fields := strings.Fields(output)
	if len(fields) != 2 || fields[0] != "submitted" {
		t.Fatalf("unexpected submit output %q", output)
	}
	return fields[1]
}
