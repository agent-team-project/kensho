package job

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeID(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{raw: "SQU-42", want: "squ-42"},
		{raw: " Linear / Ticket 42 ", want: "linear-ticket-42"},
		{raw: "Feature: PR_owner", want: "feature-pr_owner"},
		{raw: "###", want: ""},
	} {
		if got := NormalizeID(tc.raw); got != tc.want {
			t.Fatalf("NormalizeID(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestJobReadWriteList(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	first, err := New("SQU-42", "worker", "SQU-42: fix it", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first.Instance = "worker-squ-42"
	first.Status = StatusRunning
	first.LastEvent = "dispatched"
	if err := Write(teamDir, first); err != nil {
		t.Fatalf("Write first: %v", err)
	}
	second, err := New("SQU-41", "manager", "SQU-41", now)
	if err != nil {
		t.Fatalf("New second: %v", err)
	}
	if err := Write(teamDir, second); err != nil {
		t.Fatalf("Write second: %v", err)
	}

	got, err := Read(teamDir, "SQU-42")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ID != "squ-42" || got.Status != StatusRunning || got.Instance != "worker-squ-42" {
		t.Fatalf("Read job = %+v", got)
	}
	jobs, err := List(teamDir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != "squ-41" || jobs[1].ID != "squ-42" {
		t.Fatalf("List = %+v, want sorted ids", jobs)
	}
}

func TestJobValidation(t *testing.T) {
	now := time.Now().UTC()
	j := &Job{
		ID:        "SQU-42",
		Ticket:    "SQU-42",
		Target:    "worker",
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted unnormalized id")
	}
	j.ID = "squ-42"
	j.Status = Status("paused")
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid status")
	}
}

func TestReadMissingJob(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), ".agent_team"), "squ-404")
	if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		t.Fatalf("Read missing err=%v, want not exist", err)
	}
}
