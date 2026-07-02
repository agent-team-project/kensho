package daemon

import (
	crand "crypto/rand"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/runtimebin"
)

const helperIgnoreTermEnv = "AGENTTEAM_HELPER_IGNORE_TERM"

type failingRandReader struct{}

func (failingRandReader) Read([]byte) (int, error) {
	return 0, errors.New("forced rand failure")
}

// fakeSpawner records args and returns a controllable, real-but-trivial child
// process so the reaper goroutine has something to Wait() on.
//
// We start a `sleep <seconds>` subprocess. Tests that need the child to exit
// immediately pass a tiny duration; tests that need it to stay alive pass
// minutes. SIGTERM-handling is fine because /bin/sleep exits with a non-zero
// code on signal.
type fakeSpawner struct {
	mu       sync.Mutex
	calls    [][]string
	envs     [][]string
	stdins   []string
	holdSecs string // duration for the spawned sleep
}

func newFakeSpawner(hold time.Duration) *fakeSpawner {
	s := int(hold.Seconds())
	if s < 1 {
		s = 1
	}
	return &fakeSpawner{holdSecs: strconv.Itoa(s)}
}

func (f *fakeSpawner) spawn(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.envs = append(f.envs, append([]string(nil), env...))
	f.stdins = append(f.stdins, stdinContent)
	f.mu.Unlock()
	bin, err := exec.LookPath("sleep")
	if err != nil {
		return nil, err
	}
	stdin, _ := os.Open(os.DevNull)
	stdout, _ := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	stderr, _ := os.OpenFile(stderrPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	defer stdin.Close()
	defer stdout.Close()
	defer stderr.Close()
	return os.StartProcess(bin, []string{"sleep", f.holdSecs}, &os.ProcAttr{
		Dir:   workspace,
		Env:   env,
		Files: []*os.File{stdin, stdout, stderr},
	})
}

func (f *fakeSpawner) lastCall() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeSpawner) lastStdin() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.stdins) == 0 {
		return ""
	}
	return f.stdins[len(f.stdins)-1]
}

func ignoreTermSpawner(t *testing.T) Spawner {
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
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessIgnoreTerm")
		cmd.Env = append(append([]string(nil), env...), helperIgnoreTermEnv+"=1")
		cmd.Dir = workspace
		cmd.Stdin = stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
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

func TestHelperProcessIgnoreTerm(t *testing.T) {
	if os.Getenv(helperIgnoreTermEnv) != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	select {}
}

func TestNewSessionIDFallsBackWhenRandFails(t *testing.T) {
	originalReader := crand.Reader
	crand.Reader = failingRandReader{}
	t.Cleanup(func() {
		crand.Reader = originalReader
	})

	first := newSessionID()
	second := newSessionID()
	for _, id := range []string{first, second} {
		if len(id) != 36 {
			t.Fatalf("session id %q length = %d, want 36", id, len(id))
		}
		for _, index := range []int{8, 13, 18, 23} {
			if id[index] != '-' {
				t.Fatalf("session id %q missing hyphen at index %d", id, index)
			}
		}
		if id[14] != '4' {
			t.Fatalf("session id %q version nibble = %q, want 4", id, id[14])
		}
		if !strings.ContainsRune("89ab", rune(id[19])) {
			t.Fatalf("session id %q variant nibble = %q, want one of 89ab", id, id[19])
		}
	}
	if first == second {
		t.Fatalf("fallback session IDs should be unique, both were %q", first)
	}
}

func TestSignalProcessGroupStopsChildProcess(t *testing.T) {
	childPIDPath := t.TempDir() + "/child.pid"
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	proc, err := os.StartProcess(shell, []string{"sh", "-c", `sleep 60 & echo $! > "$CHILD_PID_FILE"; wait`}, &os.ProcAttr{
		Env:   append(os.Environ(), "CHILD_PID_FILE="+childPIDPath),
		Files: []*os.File{stdin, os.Stdout, os.Stderr},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		t.Fatalf("start process group: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-proc.Pid, syscall.SIGKILL)
		if body, err := os.ReadFile(childPIDPath); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(body))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	childPID := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(childPIDPath)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(body)))
			if err == nil && childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatalf("child pid file was not written")
	}
	if !PidLiveCheck(childPID) {
		t.Fatalf("child pid %d was not live before signal", childPID)
	}

	if err := signalProcessGroupOrProcess(proc, proc.Pid, syscall.SIGTERM); err != nil {
		t.Fatalf("signal process group: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() {
		_, err := proc.Wait()
		waitDone <- err
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("process group leader did not exit after SIGTERM")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !PidLiveCheck(childPID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child pid %d still live after process-group SIGTERM", childPID)
}

func TestInstance_DispatchPersistsMetadata(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	meta, err := m.Dispatch(DispatchInput{
		Agent:     "worker",
		Name:      "worker-squ-1",
		Prompt:    "hello",
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if meta.PID == 0 {
		t.Errorf("missing PID")
	}
	if meta.SessionID == "" {
		t.Errorf("missing session ID")
	}
	if meta.Status != StatusRunning {
		t.Errorf("status: got %s want running", meta.Status)
	}

	// Persisted to disk.
	disk, err := ReadMetadata(root, "worker-squ-1")
	if err != nil {
		t.Fatalf("read disk: %v", err)
	}
	if disk.PID != meta.PID || disk.SessionID != meta.SessionID {
		t.Errorf("disk mismatch: %+v vs %+v", disk, meta)
	}

	// Spawn args include --session-id <uuid> and -p <prompt>.
	args := fake.lastCall()
	if len(args) < 5 || args[1] != "--session-id" || args[2] == "" {
		t.Errorf("expected --session-id <uuid> in args, got: %v", args)
	}
	foundPrompt := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-p" && args[i+1] == "hello" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Errorf("expected -p hello, got: %v", args)
	}

	// Cleanup: stop the child so the reaper finalises.
	if _, err := m.Stop("worker-squ-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForStatusNot(t, m, "worker-squ-1", StatusRunning)
}

func TestInstance_DispatchUsesRuntimeBinaryEnv(t *testing.T) {
	t.Setenv(runtimebin.EnvBinary, "codex")
	root := t.TempDir()
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	if _, err := m.Dispatch(DispatchInput{
		Agent:     "worker",
		Name:      "worker-runtime",
		Prompt:    "hello",
		Workspace: t.TempDir(),
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	args := fake.lastCall()
	if len(args) == 0 || args[0] != "codex" {
		t.Fatalf("spawn args = %v, want runtime binary codex", args)
	}
	if _, err := m.Stop("worker-runtime"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForStatusNot(t, m, "worker-runtime", StatusRunning)
}

func TestInstance_DispatchCodexRuntimeExecArgs(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	root := t.TempDir()
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	meta, err := m.Dispatch(DispatchInput{
		Agent:     "worker",
		Name:      "worker-runtime",
		Workspace: t.TempDir(),
		Args:      []string{"exec", "-C", t.TempDir(), "hello"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if meta.Runtime != string(runtimebin.KindCodex) || meta.SessionID != "" {
		t.Fatalf("metadata = %+v, want codex without Claude session", meta)
	}
	args := fake.lastCall()
	if len(args) < 2 || args[0] != "codex" || args[1] != "exec" {
		t.Fatalf("spawn args = %v, want codex exec", args)
	}
	if containsString(args, "--session-id") {
		t.Fatalf("codex args should not include Claude session id: %v", args)
	}
	if _, err := m.Stop("worker-runtime"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForStatusNot(t, m, "worker-runtime", StatusRunning)
	if _, err := m.Start("worker-runtime"); err == nil || !strings.Contains(err.Error(), "does not support managed resume") {
		t.Fatalf("start error = %v, want Codex resume rejection", err)
	}
}

func TestInstance_DispatchCodexPromptUsesStdin(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	root := t.TempDir()
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	if _, err := m.Dispatch(DispatchInput{
		Agent:     "worker",
		Name:      "worker-runtime",
		Prompt:    "hello from stdin",
		Workspace: t.TempDir(),
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	args := fake.lastCall()
	// codex exec runs unsandboxed for autonomous work; the prompt still arrives
	// over stdin (final arg "-").
	if len(args) != 4 || args[0] != "codex" || args[1] != "exec" ||
		args[2] != "--dangerously-bypass-approvals-and-sandbox" || args[3] != "-" {
		t.Fatalf("spawn args = %v, want codex exec --dangerously-bypass-approvals-and-sandbox -", args)
	}
	if got := fake.lastStdin(); got != "hello from stdin" {
		t.Fatalf("stdin = %q, want prompt", got)
	}
	if _, err := m.Stop("worker-runtime"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForStatusNot(t, m, "worker-runtime", StatusRunning)
}

func TestInstance_DispatchCodexRejectsClaudeArgs(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	root := t.TempDir()
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	_, err := m.Dispatch(DispatchInput{
		Agent:     "worker",
		Name:      "worker-runtime",
		Workspace: t.TempDir(),
		Args:      []string{"--agents", "{}"},
	})
	if err == nil || !strings.Contains(err.Error(), "requires args beginning with exec") {
		t.Fatalf("dispatch error = %v, want Codex exec args error", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("spawn calls = %d, want none", got)
	}
}

func TestInstance_DispatchRefusesDuplicateName(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	if _, err := m.Dispatch(DispatchInput{Agent: "w", Name: "n", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("dispatch1: %v", err)
	}
	_, err := m.Dispatch(DispatchInput{Agent: "w", Name: "n", Workspace: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("want already-running error, got %v", err)
	}
	_, _ = m.Stop("n")
	waitForStatusNot(t, m, "n", StatusRunning)
}

func TestInstance_StopMarksStopped(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	if _, err := m.Dispatch(DispatchInput{Agent: "w", Name: "n", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := m.Stop("n"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForStatusNot(t, m, "n", StatusRunning)

	disk, err := ReadMetadata(root, "n")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusStopped {
		t.Errorf("status after stop: got %s want stopped", disk.Status)
	}
	if disk.StoppedAt.IsZero() {
		t.Errorf("StoppedAt not set")
	}
}

func TestInstance_StopForceKillsAfterTimeout(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, ignoreTermSpawner(t))

	if _, err := m.Dispatch(DispatchInput{Agent: "w", Name: "stubborn", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	start := time.Now()
	if _, err := m.StopWithOptions("stubborn", StopOptions{Force: true, Timeout: 25 * time.Millisecond}); err != nil {
		t.Fatalf("force stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("force stop took too long: %s", elapsed)
	}
	if err := m.WaitForReaper("stubborn", 2*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	disk, err := ReadMetadata(root, "stubborn")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusStopped {
		t.Fatalf("status after force stop = %s, want stopped", disk.Status)
	}
	if disk.ExitedAt.IsZero() {
		t.Fatalf("ExitedAt not set after force stop")
	}
}

func TestInstance_StartResumesWithSessionID(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	disp, err := m.Dispatch(DispatchInput{Agent: "manager", Name: "mgr", Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	sessionID := disp.SessionID

	if _, err := m.Stop("mgr"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForStatusNot(t, m, "mgr", StatusRunning)

	resumed, err := m.Start("mgr")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if resumed.Status != StatusRunning {
		t.Errorf("status after start: got %s want running", resumed.Status)
	}
	if resumed.SessionID != sessionID {
		t.Errorf("session ID changed: %s -> %s", sessionID, resumed.SessionID)
	}
	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == sessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected --resume %s in args, got: %v", sessionID, args)
	}

	// Cleanup.
	_, _ = m.Stop("mgr")
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestInstance_RestartStopsThenResumes(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	disp, err := m.Dispatch(DispatchInput{Agent: "manager", Name: "mgr", Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	resumed, err := m.Restart("mgr", 10*time.Second)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if resumed.Status != StatusRunning {
		t.Errorf("status after restart: got %s want running", resumed.Status)
	}
	if resumed.SessionID != disp.SessionID {
		t.Errorf("session changed on restart: %s -> %s", disp.SessionID, resumed.SessionID)
	}
	args := fake.lastCall()
	foundResume := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--resume" && args[i+1] == disp.SessionID {
			foundResume = true
		}
	}
	if !foundResume {
		t.Errorf("expected restart to resume %s, got: %v", disp.SessionID, args)
	}

	_, _ = m.Stop("mgr")
	waitForStatusNot(t, m, "mgr", StatusRunning)
}

func TestInstance_RestartWithForceEscalatesThenResumes(t *testing.T) {
	root := t.TempDir()
	m := NewInstanceManager(root, ignoreTermSpawner(t))

	disp, err := m.Dispatch(DispatchInput{Agent: "manager", Name: "mgr", Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = m.StopWithOptions("mgr", StopOptions{Force: true, Timeout: 10 * time.Millisecond})
		waitForStatusNot(t, m, "mgr", StatusRunning)
	})

	resumed, err := m.RestartWithOptions("mgr", RestartOptions{Force: true, Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("force restart: %v", err)
	}
	if resumed.Status != StatusRunning {
		t.Errorf("status after force restart: got %s want running", resumed.Status)
	}
	if resumed.SessionID != disp.SessionID {
		t.Errorf("session changed on force restart: %s -> %s", disp.SessionID, resumed.SessionID)
	}
}

func TestInstance_StaleReaperDoesNotOverwriteResumedRun(t *testing.T) {
	root := t.TempDir()
	base := ignoreTermSpawner(t)
	var mu sync.Mutex
	var procs []*os.Process
	spawn := func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		proc, err := base(args, env, workspace, stdoutPath, stderrPath, stdinContent)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		procs = append(procs, proc)
		mu.Unlock()
		return proc, nil
	}
	m := NewInstanceManager(root, spawn)

	disp, err := m.Dispatch(DispatchInput{Agent: "manager", Name: "mgr", Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = m.StopWithOptions("mgr", StopOptions{Force: true, Timeout: 10 * time.Millisecond})
		_ = m.WaitForReaper("mgr", 2*time.Second)
	})
	oldReaped := m.reapedChan("mgr")
	if oldReaped == nil {
		t.Fatalf("old reaper channel is nil")
	}
	if _, err := m.Stop("mgr"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	resumed, err := m.Start("mgr")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if resumed.Status != StatusRunning {
		t.Fatalf("status after start: got %s want running", resumed.Status)
	}
	if resumed.SessionID != disp.SessionID {
		t.Fatalf("session changed on start: %s -> %s", disp.SessionID, resumed.SessionID)
	}

	mu.Lock()
	if len(procs) < 2 {
		mu.Unlock()
		t.Fatalf("spawned processes = %d, want at least 2", len(procs))
	}
	oldProc := procs[0]
	mu.Unlock()
	if err := oldProc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill old process: %v", err)
	}
	select {
	case <-oldReaped:
	case <-time.After(2 * time.Second):
		t.Fatalf("old reaper did not finish")
	}

	disk, err := ReadMetadata(root, "mgr")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusRunning {
		t.Fatalf("status after stale reaper = %s, want running; disk=%+v", disk.Status, disk)
	}
	if disk.PID != resumed.PID {
		t.Fatalf("pid after stale reaper = %d, want resumed pid %d", disk.PID, resumed.PID)
	}
}

func TestInstance_RemoveStoppedDeletesMetadata(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	if _, err := m.Dispatch(DispatchInput{Agent: "manager", Name: "mgr", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := m.Stop("mgr"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForStatusNot(t, m, "mgr", StatusRunning)

	if err := m.Remove("mgr", false, 10*time.Second); err != nil {
		t.Fatalf("remove stopped: %v", err)
	}
	if _, err := ReadMetadata(root, "mgr"); !os.IsNotExist(err) {
		t.Fatalf("metadata should be removed, err=%v", err)
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("manager should forget removed instance, got %+v", got)
	}
}

func TestInstance_RemoveRunningRequiresForce(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	if _, err := m.Dispatch(DispatchInput{Agent: "manager", Name: "mgr", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := m.Remove("mgr", false, 10*time.Second); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("remove running without force err=%v", err)
	}
	if _, err := ReadMetadata(root, "mgr"); err != nil {
		t.Fatalf("metadata should still exist after refused remove: %v", err)
	}
	if err := m.Remove("mgr", true, 10*time.Second); err != nil {
		t.Fatalf("force remove running: %v", err)
	}
	if _, err := ReadMetadata(root, "mgr"); !os.IsNotExist(err) {
		t.Fatalf("metadata should be removed, err=%v", err)
	}
}

func TestInstance_DispatchPassesArgsAndEnv(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	_, err := m.Dispatch(DispatchInput{
		Agent:     "worker",
		Name:      "w-args",
		Workspace: t.TempDir(),
		Args:      []string{"--agents", `{"a":{"description":"d","prompt":"p"}}`, "--add-dir", "/tmp/x"},
		Env:       []string{"AGENT_TEAM_INSTANCE=w-args", "AGENT_TEAM_ROOT=/tmp/team"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	args := fake.lastCall()
	// The first three are claude / --session-id / <uuid>; then our Args.
	wantSubstring := []string{"--agents", "--add-dir", "/tmp/x"}
	for _, want := range wantSubstring {
		found := false
		for _, a := range args {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("args: missing %q in %v", want, args)
		}
	}
	envs := fake.envs[len(fake.envs)-1]
	wantEnv := "AGENT_TEAM_INSTANCE=w-args"
	found := false
	for _, e := range envs {
		if e == wantEnv {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("env: missing %q", wantEnv)
	}

	_, _ = m.Stop("w-args")
	waitForStatusNot(t, m, "w-args", StatusRunning)
}

func TestInstance_DispatchRequiresFields(t *testing.T) {
	m := NewInstanceManager(t.TempDir(), newFakeSpawner(time.Second).spawn)
	cases := []DispatchInput{
		{Name: "n", Workspace: "/tmp"},
		{Agent: "w", Workspace: "/tmp"},
		{Agent: "w", Name: "n"},
	}
	for i, c := range cases {
		if _, err := m.Dispatch(c); err == nil {
			t.Errorf("case %d: want error, got nil", i)
		}
	}
}

func TestInstance_ListReturnsSnapshot(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)

	for _, name := range []string{"a", "b"} {
		if _, err := m.Dispatch(DispatchInput{Agent: "x", Name: name, Workspace: t.TempDir()}); err != nil {
			t.Fatalf("dispatch %s: %v", name, err)
		}
	}
	got := m.List()
	if len(got) != 2 {
		t.Errorf("want 2, got %d", len(got))
	}
	for _, name := range []string{"a", "b"} {
		_, _ = m.Stop(name)
		waitForStatusNot(t, m, name, StatusRunning)
	}
}

func TestInstance_ReaperFinalisesOnNaturalExit(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(time.Second) // exits in 1s on its own
	m := NewInstanceManager(root, fake.spawn)
	if _, err := m.Dispatch(DispatchInput{Agent: "w", Name: "ephemeral", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	waitForStatusNot(t, m, "ephemeral", StatusRunning)
	disk, err := ReadMetadata(root, "ephemeral")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if disk.Status != StatusExited {
		t.Errorf("status: got %s want exited", disk.Status)
	}
	if disk.ExitCode == nil || *disk.ExitCode != 0 {
		t.Errorf("ExitCode: got %v want 0", disk.ExitCode)
	}
}

func TestInstance_LoadFromDiskRebuildsMap(t *testing.T) {
	root := t.TempDir()
	// Pretend a previous daemon left these on disk.
	for _, name := range []string{"x", "y"} {
		if err := WriteMetadata(root, &Metadata{
			Instance: name, Agent: name, Status: StatusStopped, SessionID: "sid", Workspace: t.TempDir(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	m := NewInstanceManager(root, newFakeSpawner(time.Second).spawn)
	if err := m.LoadFromDisk(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(m.List()) != 2 {
		t.Errorf("want 2, got %d", len(m.List()))
	}
}

// waitForStatusNot blocks until the instance's reaper goroutine has
// finalised its metadata (closes its `reaped` channel). After that, the
// in-memory + on-disk meta is guaranteed consistent — no need to poll.
//
// We don't actually need `want` here any more (the reaper's exit is
// deterministic), but we keep the signature so call sites read clearly.
// A 45s ceiling guards against a stuck goroutine on extremely slow CI.
func waitForStatusNot(t *testing.T, m *InstanceManager, instance string, want Status) {
	t.Helper()
	ch := m.reapedChan(instance)
	if ch == nil {
		t.Fatalf("instance %s has no reaper channel", instance)
	}
	select {
	case <-ch:
	case <-time.After(45 * time.Second):
		disk, _ := ReadMetadata(m.daemonRoot, instance)
		t.Fatalf("reaper for %s did not finish in 45s; disk=%+v", instance, disk)
	}
	disk, err := ReadMetadata(m.daemonRoot, instance)
	if err != nil {
		t.Fatalf("read after reap: %v", err)
	}
	if disk.Status == want {
		t.Fatalf("after reap, instance %s still has status=%s; disk=%+v", instance, want, disk)
	}
	if disk.ExitedAt.IsZero() {
		t.Fatalf("after reap, instance %s ExitedAt is zero; disk=%+v", instance, disk)
	}
}
