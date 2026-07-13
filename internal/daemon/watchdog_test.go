package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/topology"
)

const helperWedgedEnv = "AGENTTEAM_HELPER_WEDGED"

const (
	helperWatchdogTreeRoleEnv     = "AGENTTEAM_HELPER_WATCHDOG_TREE_ROLE"
	helperWatchdogTreeStateDirEnv = "AGENTTEAM_HELPER_WATCHDOG_TREE_STATE_DIR"
	helperWatchdogTreeOutputEnv   = "AGENTTEAM_HELPER_WATCHDOG_TREE_OUTPUT"
)

// TestHelperProcessWedged simulates a genuinely hung child — the real codex/
// Claude failure shape. It ignores SIGTERM (so graceful termination is futile)
// and parks on a long timer rather than a bare select{} so the Go runtime's
// deadlock detector does not exit it for us. Only SIGKILL can reap it.
func TestHelperProcessWedged(t *testing.T) {
	if os.Getenv(helperWedgedEnv) != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
	time.Sleep(10 * time.Minute)
}

// TestHelperProcessWatchdogTree provides a real root -> child -> grandchild
// process tree for the watchdog regression test. The root keeps the default
// SIGTERM behavior while its descendants ignore SIGTERM; before GH-404, the
// root's prompt exit made the watchdog return without escalating the surviving
// owned process group. Grandchild and unrelated roles continuously append to
// their configured output so the test can also prove post-reap write isolation.
func TestHelperProcessWatchdogTree(t *testing.T) {
	role := os.Getenv(helperWatchdogTreeRoleEnv)
	if role == "" {
		return
	}
	stateDir := os.Getenv(helperWatchdogTreeStateDirEnv)
	output := os.Getenv(helperWatchdogTreeOutputEnv)
	if stateDir == "" {
		t.Fatal("missing watchdog tree state dir")
	}
	if err := os.WriteFile(filepath.Join(stateDir, role+".pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("write %s pid: %v", role, err)
	}

	switch role {
	case "root":
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessWatchdogTree$")
		cmd.Env = replaceEnv(os.Environ(), helperWatchdogTreeRoleEnv, "child")
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child: %v", err)
		}
		if err := cmd.Process.Release(); err != nil {
			t.Fatalf("release child: %v", err)
		}
		time.Sleep(10 * time.Minute)
	case "child":
		signal.Ignore(syscall.SIGTERM)
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessWatchdogTree$")
		cmd.Env = replaceEnv(os.Environ(), helperWatchdogTreeRoleEnv, "grandchild")
		if err := cmd.Start(); err != nil {
			t.Fatalf("start grandchild: %v", err)
		}
		if err := cmd.Process.Release(); err != nil {
			t.Fatalf("release grandchild: %v", err)
		}
		time.Sleep(10 * time.Minute)
	case "grandchild", "unrelated":
		signal.Ignore(syscall.SIGTERM)
		if output == "" {
			t.Fatalf("missing %s output", role)
		}
		f, err := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open %s output: %v", role, err)
		}
		defer f.Close()
		for {
			if _, err := fmt.Fprintln(f, role); err != nil {
				t.Fatalf("write %s output: %v", role, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	default:
		t.Fatalf("unknown watchdog tree role %q", role)
	}
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}

func watchdogTreeSpawner(t *testing.T, stateDir, output string) Spawner {
	t.Helper()
	return func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		stdin, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		stdout, err := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		stderr, err := os.OpenFile(stderrPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, err
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessWatchdogTree$")
		cmd.Env = replaceEnv(env, helperWatchdogTreeRoleEnv, "root")
		cmd.Env = replaceEnv(cmd.Env, helperWatchdogTreeStateDirEnv, stateDir)
		cmd.Env = replaceEnv(cmd.Env, helperWatchdogTreeOutputEnv, output)
		cmd.Dir = workspace
		cmd.Stdin = stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		err = cmd.Start()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		if err != nil {
			return nil, err
		}
		if _, err := waitForWatchdogTreePID(filepath.Join(stateDir, "child.pid"), 3*time.Second); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return nil, err
		}
		if _, err := waitForWatchdogTreePID(filepath.Join(stateDir, "grandchild.pid"), 3*time.Second); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return nil, err
		}
		if err := waitForWatchdogTreeOutput(output, 1, 3*time.Second); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return nil, err
		}
		return cmd.Process, nil
	}
}

func startUnrelatedWatchdogWriter(t *testing.T, stateDir, output string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessWatchdogTree$")
	cmd.Env = replaceEnv(os.Environ(), helperWatchdogTreeRoleEnv, "unrelated")
	cmd.Env = replaceEnv(cmd.Env, helperWatchdogTreeStateDirEnv, stateDir)
	cmd.Env = replaceEnv(cmd.Env, helperWatchdogTreeOutputEnv, output)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start unrelated writer: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	if _, err := waitForWatchdogTreePID(filepath.Join(stateDir, "unrelated.pid"), 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := waitForWatchdogTreeOutput(output, 1, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func waitForWatchdogTreePID(path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
			if err == nil && pid > 0 {
				return pid, nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 0, fmt.Errorf("timed out waiting for pid file %s", path)
}

func waitForWatchdogTreeOutput(path string, minSize int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() >= minSize {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for output %s", path)
}

// wedgedSpawner launches TestHelperProcessWedged in its own session (Setsid),
// matching the production DefaultSpawner so the watchdog's process-group signal
// targets the child cleanly.
func wedgedSpawner(t *testing.T) Spawner {
	t.Helper()
	return func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		stdin, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		stdout, err := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		stderr, err := os.OpenFile(stderrPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, err
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessWedged")
		cmd.Env = append(append([]string(nil), env...), helperWedgedEnv+"=1")
		cmd.Dir = workspace
		cmd.Stdin = stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		err = cmd.Start()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		if err != nil {
			return nil, err
		}
		return cmd.Process, nil
	}
}

// TestInstance_WatchdogCrashesOverBudget proves the core contract: an instance
// that outlives its runtime budget is force-killed and finalised as Crashed (NOT
// Stopped — Stopped would suppress pipeline auto-advance, the exact stall the
// watchdog exists to break), so the pipeline treats it as a failure and retries.
func TestInstance_WatchdogCrashesOverBudget(t *testing.T) {
	root := t.TempDir()
	// A 60s child stands in for a wedged codex/Claude turn; the 50ms budget fires
	// long before it would ever exit on its own.
	fake := newFakeSpawner(60 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	start := time.Now()
	if _, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "hung", Workspace: t.TempDir(),
		Budget: 50 * time.Millisecond,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := m.WaitForReaper("hung", 8*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("watchdog took too long to kill: %s (child sleeps 60s)", elapsed)
	}
	disk, err := ReadMetadata(root, "hung")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusCrashed {
		t.Fatalf("status after watchdog = %s, want crashed", disk.Status)
	}
	if disk.RuntimeBudget != "50ms" {
		t.Fatalf("runtime budget = %q, want 50ms", disk.RuntimeBudget)
	}
	if disk.RuntimeDeadline.IsZero() || !disk.RuntimeDeadline.After(disk.StartedAt) {
		t.Fatalf("runtime deadline = %s, started_at = %s", disk.RuntimeDeadline, disk.StartedAt)
	}
	if disk.ExitedAt.IsZero() {
		t.Fatalf("ExitedAt not set after watchdog kill")
	}
}

func TestInstance_WatchdogKillsOwnedNestedProcessTreeOnly(t *testing.T) {
	root := t.TempDir()
	ownedState := t.TempDir()
	unrelatedState := t.TempDir()
	ownedOutput := filepath.Join(t.TempDir(), "owned.log")
	unrelatedOutput := filepath.Join(t.TempDir(), "unrelated.log")
	unrelated := startUnrelatedWatchdogWriter(t, unrelatedState, unrelatedOutput)
	m := NewInstanceManager(root, watchdogTreeSpawner(t, ownedState, ownedOutput))

	meta, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "process-tree", Workspace: t.TempDir(),
		Budget: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-meta.PID, syscall.SIGKILL) })
	childPID, err := waitForWatchdogTreePID(filepath.Join(ownedState, "child.pid"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	grandchildPID, err := waitForWatchdogTreePID(filepath.Join(ownedState, "grandchild.pid"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for name, pid := range map[string]int{"root": meta.PID, "child": childPID, "grandchild": grandchildPID} {
		pgid, err := syscall.Getpgid(pid)
		if err != nil {
			t.Fatalf("get %s pgid: %v", name, err)
		}
		if pgid != meta.PID {
			t.Fatalf("%s pgid = %d, want owned group %d", name, pgid, meta.PID)
		}
	}
	if pgid, err := syscall.Getpgid(unrelated.Process.Pid); err != nil {
		t.Fatalf("get unrelated pgid: %v", err)
	} else if pgid == meta.PID {
		t.Fatalf("unrelated process unexpectedly joined owned group %d", meta.PID)
	}

	if err := m.WaitForReaper("process-tree", 8*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	for name, pid := range map[string]int{"root": meta.PID, "child": childPID, "grandchild": grandchildPID} {
		if PidLiveCheck(pid) {
			t.Errorf("owned %s pid %d remained live after watchdog completion", name, pid)
		}
	}
	if !PidLiveCheck(unrelated.Process.Pid) {
		t.Fatalf("unrelated pid %d was killed with owned process group", unrelated.Process.Pid)
	}

	ownedAtRetry, err := os.Stat(ownedOutput)
	if err != nil {
		t.Fatalf("stat owned output: %v", err)
	}
	unrelatedBefore, err := os.Stat(unrelatedOutput)
	if err != nil {
		t.Fatalf("stat unrelated output: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	ownedAfterRetry, err := os.Stat(ownedOutput)
	if err != nil {
		t.Fatalf("stat owned output after retry window: %v", err)
	}
	if ownedAfterRetry.Size() != ownedAtRetry.Size() {
		t.Fatalf("owned descendant wrote after watchdog completion: size %d -> %d", ownedAtRetry.Size(), ownedAfterRetry.Size())
	}
	unrelatedAfter, err := os.Stat(unrelatedOutput)
	if err != nil {
		t.Fatalf("stat unrelated output after retry window: %v", err)
	}
	if unrelatedAfter.Size() <= unrelatedBefore.Size() {
		t.Fatalf("unrelated writer stopped during watchdog cleanup: size %d -> %d", unrelatedBefore.Size(), unrelatedAfter.Size())
	}
}

func TestInstance_ExtendRuntimeBudgetMovesWatchdogDeadline(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(60 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	start := time.Now()
	if _, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "extended", Workspace: t.TempDir(),
		Budget: 80 * time.Millisecond,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	extension, err := m.ExtendRuntimeBudget("extended", 220*time.Millisecond, "test")
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if extension.PreviousDeadline.IsZero() || extension.NewDeadline.IsZero() || !extension.NewDeadline.After(extension.PreviousDeadline) {
		t.Fatalf("extension deadlines = %+v", extension)
	}
	if got := extension.NewDeadline.Sub(extension.PreviousDeadline); got != 220*time.Millisecond {
		t.Fatalf("deadline moved by %s, want 220ms", got)
	}

	if err := m.WaitForReaper("extended", 8*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 180*time.Millisecond {
		t.Fatalf("watchdog fired before extended deadline: elapsed=%s", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("watchdog took too long after extension: elapsed=%s", elapsed)
	}
	disk, err := ReadMetadata(root, "extended")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusCrashed {
		t.Fatalf("status after watchdog = %s, want crashed", disk.Status)
	}
	if disk.RuntimeBudget != "300ms" {
		t.Fatalf("runtime budget = %q, want 300ms", disk.RuntimeBudget)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !lifecycleEventsContain(events, "extended", "extended") {
		t.Fatalf("events missing extended lifecycle event: %+v", events)
	}
}

func TestInstance_ExtendRuntimeBudgetRefusals(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)

	if _, err := m.ExtendRuntimeBudget("", time.Second, "test"); err == nil || !strings.Contains(err.Error(), "instance is required") {
		t.Fatalf("empty instance err = %v, want required", err)
	}
	if _, err := m.ExtendRuntimeBudget("missing", time.Second, "test"); err == nil || !strings.Contains(err.Error(), "unknown instance") {
		t.Fatalf("missing instance err = %v, want unknown", err)
	}
	if _, err := m.Dispatch(DispatchInput{Agent: "worker", Name: "free", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("dispatch free: %v", err)
	}
	if _, err := m.ExtendRuntimeBudget("free", time.Second, "test"); err == nil || !strings.Contains(err.Error(), "no armed watchdog") {
		t.Fatalf("free extend err = %v, want no armed watchdog", err)
	}
	if _, err := m.StopWithOptions("free", StopOptions{Force: true, Timeout: 25 * time.Millisecond}); err != nil {
		t.Fatalf("cleanup stop free: %v", err)
	}
	_ = m.WaitForReaper("free", 5*time.Second)

	if _, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "budgeted", Workspace: t.TempDir(),
		Budget: time.Second,
	}); err != nil {
		t.Fatalf("dispatch budgeted: %v", err)
	}
	if _, err := m.ExtendRuntimeBudget("budgeted", 0, "test"); err == nil || !strings.Contains(err.Error(), "--by must be > 0") {
		t.Fatalf("zero extend err = %v, want by validation", err)
	}
	if _, err := m.StopWithOptions("budgeted", StopOptions{Force: true, Timeout: 25 * time.Millisecond}); err != nil {
		t.Fatalf("stop budgeted: %v", err)
	}
	if err := m.WaitForReaper("budgeted", 5*time.Second); err != nil {
		t.Fatalf("wait budgeted: %v", err)
	}
	if _, err := m.ExtendRuntimeBudget("budgeted", time.Second, "test"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stopped extend err = %v, want not running", err)
	}
}

// TestInstance_WatchdogLeavesWithinBudgetUntouched proves the watchdog never
// interferes with an instance that finishes inside its budget: the child exits
// cleanly on its own and is finalised Exited, not Crashed.
func TestInstance_WatchdogLeavesWithinBudgetUntouched(t *testing.T) {
	root := t.TempDir()
	// Child exits on its own at ~1s; a 30s budget never elapses.
	fake := newFakeSpawner(1 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	if _, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "quick", Workspace: t.TempDir(),
		Budget: 30 * time.Second,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := m.WaitForReaper("quick", 8*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	disk, err := ReadMetadata(root, "quick")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusExited {
		t.Fatalf("status after clean exit within budget = %s, want exited", disk.Status)
	}
	if disk.ExitCode == nil || *disk.ExitCode != 0 {
		t.Fatalf("expected clean exit code 0, got %v", disk.ExitCode)
	}
}

// TestInstance_WatchdogEscalatesToSIGKILL proves a wedged child that ignores
// SIGTERM (the real codex-hang shape) is still reaped: the watchdog escalates to
// SIGKILL after the grace window and the instance is finalised Crashed. The
// lower-bound timing assertion makes the test meaningful — it confirms the kill
// genuinely required SIGKILL escalation rather than the child exiting on its own.
func TestInstance_WatchdogEscalatesToSIGKILL(t *testing.T) {
	if testing.Short() {
		t.Skip("escalation test waits out the SIGKILL grace window")
	}
	root := t.TempDir()
	m := NewInstanceManager(root, wedgedSpawner(t))

	start := time.Now()
	if _, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "wedged", Workspace: t.TempDir(),
		Budget: 20 * time.Millisecond,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// SIGTERM is ignored, so the watchdog waits stopKillWaitTimeout (5s) before
	// SIGKILL; allow generous headroom over that grace.
	if err := m.WaitForReaper("wedged", 20*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	// The child only dies to SIGKILL, which the watchdog sends after its grace
	// window — so the whole sequence must have taken at least most of that grace.
	// (Lower bound only: slow CI can make it longer, never shorter.)
	if elapsed := time.Since(start); elapsed < 3*time.Second {
		t.Fatalf("reaped in %s — too fast to have escalated to SIGKILL; child may have exited on its own", elapsed)
	}
	disk, err := ReadMetadata(root, "wedged")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusCrashed {
		t.Fatalf("status after SIGKILL escalation = %s, want crashed", disk.Status)
	}
}

// TestInstance_NoWatchdogWhenBudgetZero proves the watchdog is strictly opt-in:
// a dispatch with no budget is never killed, preserving existing behaviour.
func TestInstance_NoWatchdogWhenBudgetZero(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(60 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	if _, err := m.Dispatch(DispatchInput{
		Agent: "worker", Name: "free", Workspace: t.TempDir(),
		// Budget left zero — no watchdog.
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Well past any plausible watchdog interval, the instance must still be live.
	time.Sleep(300 * time.Millisecond)
	disk, err := ReadMetadata(root, "free")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusRunning {
		t.Fatalf("status with no budget = %s, want running (no watchdog)", disk.Status)
	}
	// Clean up the long-lived child.
	if _, err := m.StopWithOptions("free", StopOptions{Force: true, Timeout: 25 * time.Millisecond}); err != nil {
		t.Fatalf("cleanup stop: %v", err)
	}
	_ = m.WaitForReaper("free", 5*time.Second)
}

// TestEphemeralRuntimeBudget exercises the policy resolver that the ephemeral
// spawn path uses: time_budget, timeout, and the env default all contribute
// wall-clock ceilings; the earliest valid one arms the watchdog.
func TestEphemeralRuntimeBudget(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "")
		if got := ephemeralRuntimeBudget(nil); got != 0 {
			t.Fatalf("no config: got %s, want 0", got)
		}
	})
	t.Run("env default", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "40m")
		if got := ephemeralRuntimeBudget(map[string]any{}); got != 40*time.Minute {
			t.Fatalf("env default: got %s, want 40m", got)
		}
	})
	t.Run("time budget beats longer timeout", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "40m")
		got := ephemeralRuntimeBudget(map[string]any{"budget_time": "15m", "timeout": "30m"})
		if got != 15*time.Minute {
			t.Fatalf("time budget: got %s, want 15m", got)
		}
	})
	t.Run("timeout still beats longer time budget", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "40m")
		got := ephemeralRuntimeBudget(map[string]any{"budget_time": "30m", "timeout": "15m"})
		if got != 15*time.Minute {
			t.Fatalf("shorter timeout: got %s, want 15m", got)
		}
	})
	t.Run("timeout beats env default", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "40m")
		got := ephemeralRuntimeBudget(map[string]any{"timeout": "15m"})
		if got != 15*time.Minute {
			t.Fatalf("timeout: got %s, want 15m", got)
		}
	})
	t.Run("hard multiplier stretches time budget", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "")
		got := ephemeralRuntimeBudget(map[string]any{"budget_time": "10m", "budget_hard_multiplier": 1.5})
		if got != 15*time.Minute {
			t.Fatalf("multiplied time budget: got %s, want 15m", got)
		}
	})
	t.Run("instance hard multiplier stretches payload time budget", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "")
		inst := &topology.Instance{HardMultiplier: 1.5}
		got := ephemeralRuntimeBudgetForInstance(inst, map[string]any{"budget_time": "10m"})
		if got != 15*time.Minute {
			t.Fatalf("instance-multiplied time budget: got %s, want 15m", got)
		}
	})
	t.Run("instance time budget default arms watchdog", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "")
		inst := &topology.Instance{TimeBudget: 10 * time.Minute, HardMultiplier: 1.5}
		got := ephemeralRuntimeBudgetForInstance(inst, map[string]any{})
		if got != 15*time.Minute {
			t.Fatalf("instance default time budget: got %s, want 15m", got)
		}
	})
	t.Run("unparseable step falls through to env", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "40m")
		got := ephemeralRuntimeBudget(map[string]any{"budget_time": "soon", "timeout": "later"})
		if got != 40*time.Minute {
			t.Fatalf("bad step budget should fall through to env: got %s, want 40m", got)
		}
	})
	t.Run("unparseable env is ignored", func(t *testing.T) {
		t.Setenv(envInstanceMaxRuntime, "forever")
		if got := ephemeralRuntimeBudget(nil); got != 0 {
			t.Fatalf("bad env should disable: got %s, want 0", got)
		}
	})
}
