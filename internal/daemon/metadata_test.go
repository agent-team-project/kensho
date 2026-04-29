package daemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMetadata_RoundTrip(t *testing.T) {
	root := t.TempDir()
	m := &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: "/repo",
		PID:       1234,
		SessionID: "abc",
		StartedAt: time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC),
		Status:    StatusRunning,
	}
	if err := WriteMetadata(root, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Instance != m.Instance || got.PID != m.PID || got.SessionID != m.SessionID {
		t.Errorf("read mismatch: got %+v want %+v", got, m)
	}
	if !got.StartedAt.Equal(m.StartedAt) {
		t.Errorf("StartedAt mismatch: got %v want %v", got.StartedAt, m.StartedAt)
	}
	if got.Status != StatusRunning {
		t.Errorf("Status: got %s want %s", got.Status, StatusRunning)
	}
}

func TestMetadata_ReadMissing(t *testing.T) {
	root := t.TempDir()
	_, err := ReadMetadata(root, "nope")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want fs.ErrNotExist, got %v", err)
	}
}

func TestListMetadata_Empty(t *testing.T) {
	root := t.TempDir()
	got, err := ListMetadata(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %d", len(got))
	}
}

func TestListMetadata_DaemonRootMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "no-such-dir")
	got, err := ListMetadata(root)
	if err != nil {
		t.Fatalf("list (missing dir): %v", err)
	}
	if got != nil {
		t.Errorf("want nil for missing root, got %v", got)
	}
}

func TestListMetadata_SortedAndSkipsBareDirs(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"worker-2", "manager", "worker-1"} {
		if err := WriteMetadata(root, &Metadata{Instance: name, Agent: name, Status: StatusRunning}); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// A directory without meta.json should be skipped, not error.
	if err := os.MkdirAll(filepath.Join(root, "no-meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ListMetadata(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	want := []string{"manager", "worker-1", "worker-2"}
	for i, m := range got {
		if m.Instance != want[i] {
			t.Errorf("[%d] got %s want %s", i, m.Instance, want[i])
		}
	}
}

func TestRemoveInstance(t *testing.T) {
	root := t.TempDir()
	m := &Metadata{Instance: "x", Agent: "x", Status: StatusRunning}
	if err := WriteMetadata(root, m); err != nil {
		t.Fatal(err)
	}
	if err := RemoveInstance(root, "x"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := ReadMetadata(root, "x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("want ErrNotExist after remove, got %v", err)
	}
}
