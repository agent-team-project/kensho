package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

// attachTestEnv stands up a real agent-teamd over a unix socket inside a short
// tempdir, with a fake spawner that runs `sleep` for the child. This is the
// same shape as `internal/daemon`'s daemon_test.go so `attach`'s codepath
// (which goes through newDaemonClient + the daemon's pidfile + socket probe)
// exercises real wire behaviour.
type attachTestEnv struct {
	target  string
	teamDir string
	dmn     *daemon.Daemon
}

// shortAttachTempDir returns a tempdir under /tmp so unix-socket paths stay
// within macOS's 104-char limit. Mirrors the helper in internal/daemon's tests
// since CLI tests can't reuse it (unexported).
func shortAttachTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "agt-cli-")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newAttachTestEnv(t *testing.T) *attachTestEnv {
	t.Helper()
	target := shortAttachTempDir(t)
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir teamDir: %v", err)
	}

	d, err := daemon.New(daemon.Config{
		TeamDir:         teamDir,
		LogOut:          io.Discard,
		SpawnerOverride: fakeSpawnerForTest(t, 30*time.Second),
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = d.Shutdown(context.Background())
	})
	go func() { _ = d.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(daemon.SocketPath(teamDir)); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(daemon.SocketPath(teamDir)); err != nil {
		t.Fatalf("daemon socket never appeared: %v", err)
	}
	return &attachTestEnv{target: target, teamDir: teamDir, dmn: d}
}

// dispatchOne uses the daemon's manager directly (matching the cli test
// helpers) to seed an instance the daemon knows about. The manager's reaper
// goroutine handles cleanup later.
func (e *attachTestEnv) dispatchOne(t *testing.T, instance string) *daemon.Metadata {
	t.Helper()
	meta, err := e.dmn.Manager().Dispatch(daemon.DispatchInput{
		Agent:     "manager",
		Name:      instance,
		Workspace: e.target,
	})
	if err != nil {
		t.Fatalf("dispatch %s: %v", instance, err)
	}
	return meta
}

// captureAttachExec replaces the execClaudeAttach hook with a recorder. The
// optional rc is returned by the recorder so tests can simulate non-zero
// claude exits.
type attachCapture struct {
	called bool
	bin    string
	args   []string
	cwd    string
	rc     error
}

func captureAttachExec(t *testing.T, rc error) (*attachCapture, func()) {
	t.Helper()
	cap := &attachCapture{rc: rc}
	prev := execClaudeAttach
	execClaudeAttach = func(cmd *cobra.Command, bin string, args []string, cwd string) error {
		cap.called = true
		cap.bin = bin
		cap.args = args
		cap.cwd = cwd
		return cap.rc
	}
	return cap, func() { execClaudeAttach = prev }
}

func TestAttach_StopsAndResumes(t *testing.T) {
	env := newAttachTestEnv(t)
	meta := env.dispatchOne(t, "manager")

	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"attach", "manager", "--target", env.target})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach: %v", err)
	}

	if !cap.called {
		t.Fatal("execClaudeAttach was not called")
	}
	if len(cap.args) != 2 || cap.args[0] != "--resume" || cap.args[1] != meta.SessionID {
		t.Errorf("expected [--resume %s], got %v", meta.SessionID, cap.args)
	}
	if cap.bin != meta.RuntimeBinary {
		t.Errorf("runtime binary: got %q want %q", cap.bin, meta.RuntimeBinary)
	}

	// After the simulated claude exit, the daemon should have re-Started the
	// instance — Start replaced the reaper channel, so the in-memory map now
	// shows StatusRunning for the new incarnation. SessionID must be the same
	// since Start uses --resume against the saved id.
	insts := env.dmn.Manager().List()
	var found *daemon.Metadata
	for _, m := range insts {
		if m.Instance == "manager" {
			found = m
		}
	}
	if found == nil {
		t.Fatal("manager not in instance list after attach")
	}
	if found.Status != daemon.StatusRunning {
		t.Errorf("post-attach status: got %s want running", found.Status)
	}
	if found.SessionID != meta.SessionID {
		t.Errorf("session id changed: got %s want %s", found.SessionID, meta.SessionID)
	}
	if !strings.Contains(out.String(), "resumed under daemon") {
		t.Errorf("missing post-resume message: %s", out.String())
	}

	// Cleanup: stop the resumed incarnation so its reaper finalises before
	// t.TempDir's cleanup races the spawner's fd close.
	stopAndWaitForTest(t, env.dmn.Manager(), "manager")
}

func TestAttach_NoResume(t *testing.T) {
	env := newAttachTestEnv(t)
	meta := env.dispatchOne(t, "manager")

	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"attach", "manager", "--target", env.target, "--no-resume"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --no-resume: %v", err)
	}

	if !cap.called {
		t.Fatal("execClaudeAttach was not called")
	}
	if err := env.dmn.Manager().WaitForReaper("manager", 5*time.Second); err != nil {
		t.Fatalf("wait stop reaper: %v", err)
	}
	insts := env.dmn.Manager().List()
	var found *daemon.Metadata
	for _, m := range insts {
		if m.Instance == "manager" {
			found = m
		}
	}
	if found == nil || found.Status != daemon.StatusStopped {
		t.Errorf("expected stopped status, got %+v", found)
	}
	if found.SessionID != meta.SessionID {
		t.Errorf("session id changed: got %s want %s", found.SessionID, meta.SessionID)
	}
	if !strings.Contains(out.String(), "left in stopped state") {
		t.Errorf("missing --no-resume message: %s", out.String())
	}
}

func TestAttach_EphemeralRejected(t *testing.T) {
	env := newAttachTestEnv(t)
	// Declare an ephemeral instance in instances.toml and seed it under the
	// daemon. The daemon's instance list says it's running — the topology
	// check should still reject the attach.
	if err := os.WriteFile(filepath.Join(env.teamDir, "instances.toml"), []byte(`
[instances.worker]
agent     = "worker"
ephemeral = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	env.dispatchOne(t, "worker")
	defer stopAndWaitForTest(t, env.dmn.Manager(), "worker")

	// Belt-and-braces: even though we don't expect execClaudeAttach to be
	// invoked, install a recorder that fails the test if it is.
	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	errOut := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"attach", "worker", "--target", env.target})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for ephemeral instance")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "ephemeral") {
		t.Errorf("missing ephemeral diagnostic: %s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "logs") {
		t.Errorf("ephemeral error should point user at logs --follow: %s", errOut.String())
	}
	if cap.called {
		t.Error("execClaudeAttach should not run for an ephemeral instance")
	}
}

func TestAttach_DaemonNotRunning(t *testing.T) {
	target := shortAttachTempDir(t)
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No daemon running — attach should bail with exit code 2.
	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	errOut := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"attach", "manager", "--target", target})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "daemon is not running") {
		t.Errorf("missing daemon-not-running diagnostic: %s", errOut.String())
	}
	if cap.called {
		t.Error("execClaudeAttach should not run when daemon is down")
	}
}

func TestAttach_InstanceUnknownToDaemon(t *testing.T) {
	env := newAttachTestEnv(t)
	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	errOut := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"attach", "ghost", "--target", env.target})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown instance")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "ghost") {
		t.Errorf("error should name the missing instance: %s", errOut.String())
	}
	if cap.called {
		t.Error("execClaudeAttach should not run for an unknown instance")
	}
}

func TestAttach_AlreadyStoppedSkipsStop(t *testing.T) {
	env := newAttachTestEnv(t)
	meta := env.dispatchOne(t, "manager")
	// Stop the instance first; attach should observe StatusStopped, skip the
	// daemon-side stop, and exec --resume directly.
	stopAndWaitForTest(t, env.dmn.Manager(), "manager")

	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"attach", "manager", "--target", env.target, "--no-resume"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach against already-stopped: %v", err)
	}
	if !cap.called {
		t.Fatal("execClaudeAttach was not called")
	}
	if cap.args[1] != meta.SessionID {
		t.Errorf("session id mismatch: got %s want %s", cap.args[1], meta.SessionID)
	}
}

func TestAttach_DryRunDoesNotStopOrExec(t *testing.T) {
	env := newAttachTestEnv(t)
	meta := env.dispatchOne(t, "manager")
	defer stopAndWaitForTest(t, env.dmn.Manager(), "manager")

	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"attach", "manager", "--target", env.target, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --dry-run: %v\nstderr=%s", err, errOut.String())
	}
	if cap.called {
		t.Fatal("execClaudeAttach should not run during dry-run")
	}
	if err := env.dmn.Manager().WaitForReaper("manager", 20*time.Millisecond); err == nil {
		t.Fatal("dry-run stopped the running daemon child")
	}
	body := out.String()
	for _, want := range []string{
		"instance:             manager",
		"runtime:              claude",
		"runtime_binary:       " + meta.RuntimeBinary,
		"session_id:           " + meta.SessionID,
		"would_stop:           yes",
		"command:              " + meta.RuntimeBinary + " --resume " + meta.SessionID,
		"would_resume_daemon:  yes",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, body)
		}
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"attach", "manager", "--target", env.target, "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("attach --dry-run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "attach", "--repo", env.target, "manager"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("attach --dry-run --commands = %q, want %q", got, wantCommand)
	}

	rootScopedCommands := NewRootCmd()
	rootScopedCommandsOut, rootScopedCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScopedCommands.SetOut(rootScopedCommandsOut)
	rootScopedCommands.SetErr(rootScopedCommandsErr)
	rootScopedCommands.SetArgs([]string{"--repo", env.target, "attach", "manager", "--dry-run", "--commands"})
	if err := rootScopedCommands.Execute(); err != nil {
		t.Fatalf("attach root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedCommandsErr.String())
	}
	if got := strings.TrimSpace(rootScopedCommandsOut.String()); got != wantCommand {
		t.Fatalf("attach root --repo --dry-run --commands = %q, want %q", got, wantCommand)
	}
	if cap.called {
		t.Fatal("execClaudeAttach should not run during commands dry-run")
	}
}

func TestAttach_DryRunNoResumePreview(t *testing.T) {
	env := newAttachTestEnv(t)
	env.dispatchOne(t, "manager")
	defer stopAndWaitForTest(t, env.dmn.Manager(), "manager")

	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"attach", "manager", "--target", env.target, "--dry-run", "--no-resume"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach --dry-run --no-resume: %v\nstderr=%s", err, errOut.String())
	}
	if cap.called {
		t.Fatal("execClaudeAttach should not run during dry-run")
	}
	if !strings.Contains(out.String(), "would_resume_daemon:  no") {
		t.Fatalf("dry-run output = %q, want no daemon resume", out.String())
	}
}

func TestAttach_DryRunUnsupportedCodexShowsUnmanagedResume(t *testing.T) {
	env := newAttachTestEnv(t)
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = sleep.Process.Kill()
		_, _ = sleep.Process.Wait()
	})

	meta := &daemon.Metadata{
		Instance:      "codex-worker",
		Agent:         "worker",
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: runtimebin.DefaultBinaryForKind(runtimebin.KindCodex),
		Workspace:     env.target,
		PID:           sleep.Process.Pid,
		SessionID:     "legacy-session",
		StartedAt:     time.Now().UTC(),
		Status:        daemon.StatusRunning,
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(env.teamDir), meta); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := env.dmn.Manager().LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}

	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"attach", "codex-worker", "--target", env.target, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach codex --dry-run: %v\nstderr=%s", err, errOut.String())
	}
	if cap.called {
		t.Fatal("execClaudeAttach should not run during unsupported dry-run")
	}
	if err := env.dmn.Manager().WaitForReaper("codex-worker", 20*time.Millisecond); err == nil {
		t.Fatal("unsupported dry-run stopped the running daemon child")
	}
	body := out.String()
	for _, want := range []string{
		"runtime:              codex",
		"managed_resume:       no",
		"would_stop:           no",
		"command:              codex resume legacy-session",
		"would_resume_daemon:  no",
		`managed_detail:       runtime "codex" does not support managed resume`,
		"logs_command:         agent-team logs codex-worker --follow",
		"last_message_command: agent-team logs codex-worker --last-message",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, body)
		}
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"attach", "codex-worker", "--target", env.target, "--dry-run", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("attach codex --dry-run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommands := strings.Join([]string{
		"codex resume legacy-session",
		strings.Join(shellQuoteArgs([]string{"agent-team", "logs", "--repo", env.target, "codex-worker", "--follow"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "logs", "--repo", env.target, "codex-worker", "--last-message"}), " "),
	}, "\n")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommands {
		t.Fatalf("attach codex --dry-run --commands = %q, want %q", got, wantCommands)
	}

	rootScopedCommands := NewRootCmd()
	rootScopedCommandsOut, rootScopedCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScopedCommands.SetOut(rootScopedCommandsOut)
	rootScopedCommands.SetErr(rootScopedCommandsErr)
	rootScopedCommands.SetArgs([]string{"--repo", env.target, "attach", "codex-worker", "--dry-run", "--commands"})
	if err := rootScopedCommands.Execute(); err != nil {
		t.Fatalf("attach codex root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedCommandsErr.String())
	}
	if got := strings.TrimSpace(rootScopedCommandsOut.String()); got != wantCommands {
		t.Fatalf("attach codex root --repo --dry-run --commands = %q, want %q", got, wantCommands)
	}
	got, err := daemon.ReadMetadata(daemon.DaemonRoot(env.teamDir), "codex-worker")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if got.Status != daemon.StatusRunning {
		t.Fatalf("dry-run changed unsupported runtime status: got %s want running", got.Status)
	}
}

func TestAttach_DryRunRequiresDirectAttachMode(t *testing.T) {
	cmd := NewRootCmd()
	errOut := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"attach", "--dry-run"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected dry-run without instance to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "--dry-run requires an instance name") {
		t.Fatalf("stderr = %q", errOut.String())
	}

	commands := NewRootCmd()
	commandsErr := &bytes.Buffer{}
	commands.SetOut(&bytes.Buffer{})
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"attach", "manager", "--commands"})
	err = commands.Execute()
	if err == nil {
		t.Fatal("expected commands without dry-run to fail")
	}
	if !strings.Contains(commandsErr.String(), "--commands requires --dry-run") {
		t.Fatalf("stderr = %q", commandsErr.String())
	}
}

func TestAttach_RejectsUnsupportedRuntimeBeforeStop(t *testing.T) {
	env := newAttachTestEnv(t)
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = sleep.Process.Kill()
		_, _ = sleep.Process.Wait()
	})

	meta := &daemon.Metadata{
		Instance:      "codex-worker",
		Agent:         "worker",
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: runtimebin.DefaultBinaryForKind(runtimebin.KindCodex),
		Workspace:     env.target,
		PID:           sleep.Process.Pid,
		SessionID:     "legacy-session",
		StartedAt:     time.Now().UTC(),
		Status:        daemon.StatusRunning,
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(env.teamDir), meta); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := env.dmn.Manager().LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}

	cap, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	errOut := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"attach", "codex-worker", "--target", env.target})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unsupported runtime error")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if cap.called {
		t.Fatal("execClaudeAttach should not run for unsupported runtime")
	}
	stderr := errOut.String()
	for _, want := range []string{
		`runtime "codex" does not support managed resume`,
		`agent-team logs codex-worker --follow`,
		`agent-team logs codex-worker --last-message`,
		`codex resume legacy-session`,
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	}
	got, err := daemon.ReadMetadata(daemon.DaemonRoot(env.teamDir), "codex-worker")
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if got.Status != daemon.StatusRunning {
		t.Fatalf("attach changed unsupported runtime status: got %s want running", got.Status)
	}
}

// TestAttach_ClaudeExitCodeIsPropagated covers the case where the user's
// interactive session exited non-zero. With --no-resume that exit code should
// be the command's exit code.
func TestAttach_ClaudeExitCodeIsPropagated(t *testing.T) {
	env := newAttachTestEnv(t)
	env.dispatchOne(t, "manager")
	defer stopAndWaitForTest(t, env.dmn.Manager(), "manager")

	_, restore := captureAttachExec(t, ExitCode(7))
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"attach", "manager", "--target", env.target, "--no-resume"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected non-zero exit when claude returns non-zero")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 7 {
		t.Errorf("expected exit 7 (forwarded from claude), got %v", err)
	}
}

// TestAttach_StateDirSurvivesTransfer confirms files in the per-instance state
// dir are untouched by the stop+resume handoff. The acceptance criteria call
// this out explicitly.
func TestAttach_StateDirSurvivesTransfer(t *testing.T) {
	env := newAttachTestEnv(t)
	env.dispatchOne(t, "manager")

	stateDir := filepath.Join(env.teamDir, "state", "manager")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	preserved := filepath.Join(stateDir, "journal.md")
	body := []byte("preserved across attach\n")
	if err := os.WriteFile(preserved, body, 0o644); err != nil {
		t.Fatal(err)
	}
	// Snapshot mtime so we can prove attach did not rewrite the file.
	orig, err := os.Stat(preserved)
	if err != nil {
		t.Fatal(err)
	}

	_, restore := captureAttachExec(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"attach", "manager", "--target", env.target, "--no-resume"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach: %v", err)
	}

	got, err := os.ReadFile(preserved)
	if err != nil {
		t.Fatalf("read preserved file: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("state file body changed: got %q want %q", got, body)
	}
	st, err := os.Stat(preserved)
	if err != nil {
		t.Fatalf("stat preserved file: %v", err)
	}
	if !st.ModTime().Equal(orig.ModTime()) {
		t.Errorf("state file mtime changed: %s -> %s", orig.ModTime(), st.ModTime())
	}
}
