package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompactTerminalMetadataArchivesExitedAndCrashed(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	root := DaemonRoot(teamDir)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-15 * 24 * time.Hour)

	for _, meta := range []*Metadata{
		{Instance: "worker-done", Agent: "worker", Job: "squ-40", Status: StatusExited, StartedAt: old.Add(-time.Hour), ExitedAt: old},
		{Instance: "worker-crashed", Agent: "worker", Job: "squ-41", Status: StatusCrashed, StartedAt: old.Add(-time.Hour), ExitedAt: old},
		{Instance: "worker-running", Agent: "worker", Job: "squ-42", Status: StatusRunning, StartedAt: old},
	} {
		if err := WriteMetadata(root, meta); err != nil {
			t.Fatalf("WriteMetadata %s: %v", meta.Instance, err)
		}
	}

	results, err := CompactTerminalMetadata(teamDir, 14*24*time.Hour, now, false)
	if err != nil {
		t.Fatalf("CompactTerminalMetadata: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want two terminal records", results)
	}
	for _, instance := range []string{"worker-done", "worker-crashed"} {
		if _, err := ReadMetadata(root, instance); !os.IsNotExist(err) {
			t.Fatalf("ReadMetadata %s err = %v, want not exist", instance, err)
		}
	}
	if _, err := ReadMetadata(root, "worker-running"); err != nil {
		t.Fatalf("running metadata removed: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(teamDir, "daemon", "archive", "2026-06.jsonl"))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	for _, want := range []string{`"type":"daemon_metadata"`, `"id":"worker-done"`, `"id":"worker-crashed"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("archive missing %q:\n%s", want, string(body))
		}
	}
}

func TestCompactTerminalMetadataDryRunDoesNotRemove(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	root := DaemonRoot(teamDir)
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-15 * 24 * time.Hour)
	if err := WriteMetadata(root, &Metadata{Instance: "worker-done", Agent: "worker", Status: StatusExited, StartedAt: old, ExitedAt: old}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	results, err := CompactTerminalMetadata(teamDir, 14*24*time.Hour, now, true)
	if err != nil {
		t.Fatalf("CompactTerminalMetadata dry-run: %v", err)
	}
	if len(results) != 1 || results[0].Action != "would_archive" || !results[0].DryRun {
		t.Fatalf("results = %+v", results)
	}
	if _, err := ReadMetadata(root, "worker-done"); err != nil {
		t.Fatalf("dry-run removed metadata: %v", err)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "daemon", "archive", "2026-06.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("dry-run archive file err = %v, want not exist", err)
	}
}
