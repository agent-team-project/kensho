package daemon

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

func TestAdoptMetadataWritesRunningRecordAndEvent(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	meta, changed, err := AdoptMetadata(root, AdoptInput{
		Instance:  "manager",
		Agent:     "manager",
		Runtime:   "claude",
		Workspace: "/repo",
		PID:       os.Getpid(),
		SessionID: "sid-1",
		Job:       "squ-1",
	}, now)
	if err != nil {
		t.Fatalf("AdoptMetadata: %v", err)
	}
	if !changed {
		t.Fatalf("AdoptMetadata changed=false")
	}
	if meta.Status != StatusRunning || !meta.Adopted || meta.PID != os.Getpid() || meta.SessionID != "sid-1" || meta.Job != "squ-1" {
		t.Fatalf("metadata = %+v", meta)
	}
	disk, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !disk.Adopted || disk.Status != StatusRunning || !disk.StartedAt.Equal(now) {
		t.Fatalf("disk metadata = %+v", disk)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != "adopt" || events[0].Instance != "manager" || events[0].PID != os.Getpid() {
		t.Fatalf("events = %+v", events)
	}
}

func TestPrepareAdoptRejectsDeadPID(t *testing.T) {
	_, _, err := PrepareAdoptMetadata(t.TempDir(), AdoptInput{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: "/repo",
		PID:       999999999,
	}, time.Now())
	if err == nil {
		t.Fatal("PrepareAdoptMetadata succeeded for dead pid")
	}
}

func TestPrepareAdoptRejectsReplacingLiveMetadataWithoutForce(t *testing.T) {
	root := t.TempDir()
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: "/repo",
		PID:       os.Getpid(),
		Status:    StatusRunning,
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	_, _, err := PrepareAdoptMetadata(root, AdoptInput{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: "/repo",
		PID:       os.Getppid(),
	}, time.Now())
	if err == nil {
		t.Fatal("PrepareAdoptMetadata replaced live metadata without force")
	}
}

func TestAdoptMetadataIdempotentForSamePID(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	input := AdoptInput{
		Instance:  "manager",
		Agent:     "manager",
		Runtime:   "claude",
		Workspace: "/repo",
		PID:       os.Getpid(),
		SessionID: "sid-1",
	}
	if _, changed, err := AdoptMetadata(root, input, now); err != nil || !changed {
		t.Fatalf("first adopt changed=%v err=%v", changed, err)
	}
	if _, changed, err := AdoptMetadata(root, input, now.Add(time.Hour)); err != nil || changed {
		t.Fatalf("second adopt changed=%v err=%v", changed, err)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one adopt event", events)
	}
}

func TestAdoptMetadataDryRunDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	if _, changed, err := PrepareAdoptMetadata(root, AdoptInput{
		Instance:  "manager",
		Agent:     "manager",
		Runtime:   "claude",
		Workspace: "/repo",
		PID:       os.Getpid(),
	}, time.Now()); err != nil || !changed {
		t.Fatalf("PrepareAdoptMetadata changed=%v err=%v", changed, err)
	}
	if _, err := ReadMetadata(root, "manager"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("metadata should not exist after prepare: %v", err)
	}
}
