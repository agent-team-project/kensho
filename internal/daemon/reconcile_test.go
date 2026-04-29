package daemon

import (
	"os"
	"testing"
	"time"
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
