package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/topology"
)

func TestReconcile_LiveProcessStaysRunning(t *testing.T) {
	root := t.TempDir()
	// Use the test process's own PID as a guaranteed-alive PID.
	if err := WriteMetadata(root, &Metadata{
		Instance:  "alive",
		Agent:     "x",
		Workspace: "/tmp",
		PID:       os.Getpid(),
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	m := NewInstanceManager(root, nil)
	if err := Reconcile(root, m); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	disk, err := ReadMetadata(root, "alive")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Status != StatusRunning {
		t.Errorf("status: got %s want running", disk.Status)
	}
	got := m.List()
	if len(got) != 1 {
		t.Errorf("manager map: want 1, got %d", len(got))
	}
}

func TestReconcile_PreservesReaperForManagerSpawnedProcess(t *testing.T) {
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
	if err := Reconcile(root, m); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := m.Stop(meta.Instance); err != nil {
		t.Fatalf("stop after reconcile: %v", err)
	}
	if err := m.WaitForReaper(meta.Instance, 10*time.Second); err != nil {
		t.Fatalf("wait reaper after reconcile: %v", err)
	}
}

func TestReconcile_DeadProcessMarkedExited(t *testing.T) {
	root := t.TempDir()
	// Pick a PID that's almost certainly not in use. PID 1 (init) is alive
	// but we can't kill it; we want one that's gone. Use 999_999_999 — far
	// above any realistic PID.
	if err := WriteMetadata(root, &Metadata{
		Instance:  "dead",
		Agent:     "x",
		Workspace: "/tmp",
		PID:       999_999_999,
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	m := NewInstanceManager(root, nil)
	if err := Reconcile(root, m); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	disk, err := ReadMetadata(root, "dead")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Status != StatusExited {
		t.Errorf("status: got %s want exited", disk.Status)
	}
	if disk.ExitedAt.IsZero() {
		t.Errorf("ExitedAt not set")
	}
}

func TestReconcile_StoppedAndExitedUntouched(t *testing.T) {
	root := t.TempDir()
	for _, st := range []Status{StatusStopped, StatusExited, StatusCrashed} {
		instance := "i-" + string(st)
		if err := WriteMetadata(root, &Metadata{
			Instance:  instance,
			Agent:     "x",
			Workspace: "/tmp",
			PID:       1,
			Status:    st,
			StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	m := NewInstanceManager(root, nil)
	if err := Reconcile(root, m); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, st := range []Status{StatusStopped, StatusExited, StatusCrashed} {
		disk, _ := ReadMetadata(root, "i-"+string(st))
		if disk.Status != st {
			t.Errorf("%s changed to %s", st, disk.Status)
		}
	}
}

func TestReconcile_PidLiveCheckReportsZero(t *testing.T) {
	if PidLiveCheck(0) {
		t.Errorf("PID 0 should not be live")
	}
	if PidLiveCheck(-1) {
		t.Errorf("negative PID should not be live")
	}
	if !PidLiveCheck(os.Getpid()) {
		t.Errorf("self should be live")
	}
}

func TestReconcileWithTopology_RestartPolicyMatrix(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	now := time.Now().UTC()
	exit0 := 0
	exit1 := 1
	for _, meta := range []*Metadata{
		{Instance: "never-crashed", Agent: "manager", Workspace: t.TempDir(), Status: StatusCrashed, SessionID: "sid-never", StartedAt: now},
		{Instance: "failure-crashed", Agent: "manager", Workspace: t.TempDir(), Status: StatusCrashed, SessionID: "sid-failure-crashed", StartedAt: now},
		{Instance: "failure-clean", Agent: "manager", Workspace: t.TempDir(), Status: StatusExited, ExitCode: &exit0, SessionID: "sid-failure-clean", StartedAt: now},
		{Instance: "failure-nonzero", Agent: "manager", Workspace: t.TempDir(), Status: StatusExited, ExitCode: &exit1, SessionID: "sid-failure-nonzero", StartedAt: now},
		{Instance: "always-clean", Agent: "manager", Workspace: t.TempDir(), Status: StatusExited, ExitCode: &exit0, SessionID: "sid-always-clean", StartedAt: now},
	} {
		if err := WriteMetadata(root, meta); err != nil {
			t.Fatalf("write %s: %v", meta.Instance, err)
		}
	}
	topo := mustParseCustomTopo(t, `
[instances.never-crashed]
agent = "manager"
restart = "never"

[instances.failure-crashed]
agent = "manager"
restart = "on-failure"

[instances.failure-clean]
agent = "manager"
restart = "on-failure"

[instances.failure-nonzero]
agent = "manager"
restart = "on-failure"

[instances.always-clean]
agent = "manager"
restart = "always"
`)

	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got, want := fake.callCount(), 3; got != want {
		t.Fatalf("spawn calls = %d, want %d", got, want)
	}
	for _, name := range []string{"failure-crashed", "failure-nonzero", "always-clean"} {
		disk, err := ReadMetadata(root, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if disk.Status != StatusRunning {
			t.Fatalf("%s status = %s, want running", name, disk.Status)
		}
		if !disk.RestartBackoffUntil.IsZero() {
			t.Fatalf("%s restart backoff should be cleared, got %s", name, disk.RestartBackoffUntil)
		}
		_, _ = m.Stop(name)
		_ = m.WaitForReaper(name, 2*time.Second)
	}
	for _, name := range []string{"never-crashed", "failure-clean"} {
		disk, err := ReadMetadata(root, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if disk.Status == StatusRunning {
			t.Fatalf("%s restarted unexpectedly", name)
		}
	}
}

func TestReconcileWithTopology_PersistsCappedRestartBackoff(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	spawnErr := errors.New("spawn failed")
	m := NewInstanceManager(root, func(args []string, env []string, workspace, stdoutPath, stderrPath, stdin string) (*os.Process, error) {
		return nil, spawnErr
	})
	now := time.Now().UTC()
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: t.TempDir(),
		Status:    StatusCrashed,
		SessionID: "sid-manager",
		StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	topo := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
restart = "on-failure"
`)
	oldDelay := restartBackoffDelay
	restartBackoffDelay = 10 * time.Minute
	t.Cleanup(func() { restartBackoffDelay = oldDelay })

	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	disk, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatal(err)
	}
	if !disk.RestartBackoffUntil.After(now) {
		t.Fatalf("restart_backoff_until = %s, want after %s", disk.RestartBackoffUntil, now)
	}
	if disk.RestartBackoffUntil.After(now.Add(restartBackoffCap + time.Second)) {
		t.Fatalf("restart_backoff_until = %s, want capped within %s", disk.RestartBackoffUntil, restartBackoffCap)
	}
}

func TestReconcileWithTopology_SkipsWhileRestartBackoffActive(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	if err := WriteMetadata(root, &Metadata{
		Instance:            "manager",
		Agent:               "manager",
		Workspace:           t.TempDir(),
		Status:              StatusCrashed,
		SessionID:           "sid-manager",
		StartedAt:           time.Now().UTC(),
		RestartBackoffUntil: time.Now().UTC().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	topo := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
restart = "always"
`)
	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("spawn calls = %d, want 0 while backoff active", got)
	}
}

func TestReconcileWithTopology_AdoptedWatcherMarksExitPromptly(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	var live atomic.Bool
	live.Store(true)
	oldCheck := PidLiveCheck
	PidLiveCheck = func(pid int) bool { return live.Load() }
	oldInterval := adoptedPollInterval
	adoptedPollInterval = 10 * time.Millisecond
	var m *InstanceManager
	t.Cleanup(func() {
		live.Store(false)
		if m != nil {
			_ = m.WaitForReaper("manager", time.Second)
		}
		PidLiveCheck = oldCheck
		adoptedPollInterval = oldInterval
	})
	started := time.Now().UTC()
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: t.TempDir(),
		PID:       4242,
		Status:    StatusRunning,
		StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	m = NewInstanceManager(root, nil)
	topo := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
restart = "never"
`)
	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	live.Store(false)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		disk, err := ReadMetadata(root, "manager")
		if err != nil {
			t.Fatal(err)
		}
		if disk.Status == StatusExited && !disk.ExitedAt.IsZero() {
			if err := m.WaitForReaper("manager", time.Second); err != nil {
				t.Fatalf("wait watcher: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	disk, _ := ReadMetadata(root, "manager")
	t.Fatalf("status = %s exited_at=%s, want exited promptly", disk.Status, disk.ExitedAt)
}

func TestReconcileWithTopology_ReprobePreventsDuplicateForAdoptedSurvivor(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	var checks atomic.Int32
	var live atomic.Bool
	live.Store(true)
	oldCheck := PidLiveCheck
	PidLiveCheck = func(pid int) bool {
		return live.Load() && checks.Add(1) > 1
	}
	oldInterval := adoptedPollInterval
	adoptedPollInterval = 10 * time.Millisecond
	var m *InstanceManager
	var topo *topology.Topology
	t.Cleanup(func() {
		if topo != nil && topo.Instances["manager"] != nil {
			topo.Instances["manager"].Restart = topology.RestartNever
		}
		live.Store(false)
		if m != nil {
			_ = m.WaitForReaper("manager", time.Second)
		}
		PidLiveCheck = oldCheck
		adoptedPollInterval = oldInterval
	})
	started := time.Now().UTC()
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: t.TempDir(),
		PID:       7777,
		Status:    StatusRunning,
		SessionID: "sid-manager",
		StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m = NewInstanceManager(root, fake.spawn)
	topo = mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
restart = "on-failure"
`)
	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("spawn calls = %d, want 0 after live re-probe", got)
	}
	disk, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Status != StatusRunning || !disk.ExitedAt.IsZero() {
		t.Fatalf("metadata = %+v, want revived running survivor", disk)
	}
	topo.Instances["manager"].Restart = topology.RestartNever
	live.Store(false)
	if err := m.WaitForReaper("manager", time.Second); err != nil {
		t.Fatalf("wait revived watcher: %v", err)
	}
}

func restartFixtureTeamDir(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	return teamDir
}
