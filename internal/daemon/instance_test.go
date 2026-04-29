package daemon

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	holdSecs string // duration for the spawned sleep
}

func newFakeSpawner(hold time.Duration) *fakeSpawner {
	s := int(hold.Seconds())
	if s < 1 {
		s = 1
	}
	return &fakeSpawner{holdSecs: strconv.Itoa(s)}
}

func (f *fakeSpawner) spawn(args []string, env []string, workspace, stdoutPath, stderrPath string) (*os.Process, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.envs = append(f.envs, append([]string(nil), env...))
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

