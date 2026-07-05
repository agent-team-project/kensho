package job

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/usage"
)

func TestCompactTerminalArchivesJobAndEvents(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-15 * 24 * time.Hour)

	j, err := New("SQU-40", "worker", "archive me", old)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = StatusDone
	j.UpdatedAt = old
	j.LastEvent = "closed"
	j.LastStatus = "done"
	j.Usage, _ = usage.MergeRecord(nil, usage.Record{
		Instance:          "worker-squ-40",
		Agent:             "worker",
		Runtime:           "codex",
		TokensAvailable:   true,
		InputTokens:       100,
		CachedInputTokens: 80,
		OutputTokens:      12,
		Turns:             1,
		DurationMS:        2500,
		StartedAt:         old.Add(-time.Hour),
		EndedAt:           old,
	})
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := AppendEvent(teamDir, &Event{TS: old, JobID: j.ID, Type: "closed", Status: StatusDone, Message: "done"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := AppendGateRecord(teamDir, &GateRecord{TS: old, JobID: j.ID, Name: "rust-checks", Status: GateStatusFail, Signature: "missing-binary"}); err != nil {
		t.Fatalf("AppendGateRecord: %v", err)
	}

	active, err := New("SQU-41", "worker", "keep me", old)
	if err != nil {
		t.Fatalf("New active: %v", err)
	}
	active.Status = StatusRunning
	active.UpdatedAt = old
	if err := Write(teamDir, active); err != nil {
		t.Fatalf("Write active: %v", err)
	}

	results, err := CompactTerminal(teamDir, 14*24*time.Hour, now, false)
	if err != nil {
		t.Fatalf("CompactTerminal: %v", err)
	}
	if len(results) != 1 || results[0].ID != "squ-40" || results[0].Action != "archived" {
		t.Fatalf("results = %+v", results)
	}
	if !results[0].EventLog || !results[0].GateLog {
		t.Fatalf("archive result logs = %+v, want event and gate logs", results[0])
	}
	if _, err := os.Stat(filepath.Join(teamDir, "daemon", "archive", "2026-06.jsonl")); err != nil {
		t.Fatalf("archive file missing: %v", err)
	}
	if _, err := Read(teamDir, "squ-40"); !os.IsNotExist(err) {
		t.Fatalf("live Read err = %v, want not exist", err)
	}
	archived, err := ReadLiveOrArchive(teamDir, "SQU-40")
	if err != nil {
		t.Fatalf("ReadLiveOrArchive: %v", err)
	}
	if archived.ID != j.ID || archived.Status != StatusDone || archived.LastEvent != "closed" {
		t.Fatalf("archived job = %+v", archived)
	}
	if archived.Usage == nil || archived.Usage.Summary.InputTokens != 100 || len(archived.Usage.Records) != 1 {
		t.Fatalf("archived usage = %+v", archived.Usage)
	}
	archivedJobs, err := ListArchived(teamDir)
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	if len(archivedJobs) != 1 || archivedJobs[0].ID != j.ID || archivedJobs[0].Usage == nil {
		t.Fatalf("archived jobs = %+v", archivedJobs)
	}
	events, err := ListEvents(teamDir, "squ-40")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "closed" {
		t.Fatalf("events = %+v", events)
	}
	gates, err := ListGateRecords(teamDir, "squ-40")
	if err != nil {
		t.Fatalf("ListGateRecords: %v", err)
	}
	if len(gates) != 1 || gates[0].Name != "rust-checks" || gates[0].Status != GateStatusFail {
		t.Fatalf("gates = %+v", gates)
	}
	if _, err := os.Stat(GatePath(teamDir, "squ-40")); !os.IsNotExist(err) {
		t.Fatalf("live gate log err = %v, want not exist", err)
	}
	if _, err := Read(teamDir, "squ-41"); err != nil {
		t.Fatalf("active job removed: %v", err)
	}
}

func TestCompactTerminalDryRunDoesNotRemove(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-15 * 24 * time.Hour)

	j, err := New("SQU-42", "worker", "preview me", old)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = StatusFailed
	j.UpdatedAt = old
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}

	results, err := CompactTerminal(teamDir, 14*24*time.Hour, now, true)
	if err != nil {
		t.Fatalf("CompactTerminal dry-run: %v", err)
	}
	if len(results) != 1 || results[0].Action != "would_archive" || !results[0].DryRun {
		t.Fatalf("results = %+v", results)
	}
	if _, err := Read(teamDir, "squ-42"); err != nil {
		t.Fatalf("dry-run removed job: %v", err)
	}
	if _, err := os.Stat(filepath.Join(teamDir, "daemon", "archive", "2026-06.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("dry-run archive file err = %v, want not exist", err)
	}
}
