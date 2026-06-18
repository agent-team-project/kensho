package job

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

func TestJobEventsAppendListTail(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.FixedZone("local", 3600))
	if err := AppendEvent(teamDir, &Event{
		TS:       now,
		JobID:    "SQU-42",
		Type:     "created",
		Status:   StatusQueued,
		Instance: " worker-squ-42 ",
		Message:  " created ",
		Actor:    " cli ",
		Data:     map[string]string{"ticket": "SQU-42"},
	}); err != nil {
		t.Fatalf("AppendEvent first: %v", err)
	}
	if err := AppendEvent(teamDir, &Event{
		JobID:  "squ-42",
		Type:   "closed",
		Status: StatusDone,
	}); err != nil {
		t.Fatalf("AppendEvent second: %v", err)
	}
	events, err := ListEvents(teamDir, "SQU-42")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len=%d, want 2: %+v", len(events), events)
	}
	first := events[0]
	if first.JobID != "squ-42" || first.Type != "created" || first.Status != StatusQueued || first.TS.Location() != time.UTC {
		t.Fatalf("first event = %+v", first)
	}
	if first.Instance != "worker-squ-42" || first.Message != "created" || first.Actor != "cli" || first.Data["ticket"] != "SQU-42" {
		t.Fatalf("first event fields = %+v", first)
	}
	tail := TailEvents(events, 1)
	if len(tail) != 1 || tail[0].Type != "closed" {
		t.Fatalf("tail = %+v", tail)
	}
}

func TestJobEventsMissingAndInvalid(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	events, err := ListEvents(teamDir, "missing")
	if err != nil {
		t.Fatalf("ListEvents missing: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("missing events = %+v", events)
	}
	if err := AppendEvent(teamDir, &Event{JobID: "SQU-42"}); err == nil {
		t.Fatalf("AppendEvent accepted missing type")
	}
	if err := os.MkdirAll(Directory(teamDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(EventPath(teamDir, "squ-43"), []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatalf("write bad event log: %v", err)
	}
	_, err = ListEvents(teamDir, "squ-43")
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("ListEvents invalid err=%v, want line number", err)
	}
}

func TestReconcilePRMarksMergedJobDone(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	j, err := New("SQU-77", "worker", "ship the change", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = StatusRunning
	j.PR = "https://github.com/acme/repo/pull/77"
	j.Branch = "worktree-worker-squ-77"
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	merged := true
	result, err := ReconcilePR(teamDir, ReconcileInput{
		EventType: "pr.merged",
		Action:    "closed",
		PR:        "77",
		PRURL:     "https://github.com/acme/repo/pull/77/",
		Branch:    "worktree-worker-squ-77",
		Merged:    &merged,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ReconcilePR: %v", err)
	}
	if result.MatchedBy != "pr_url" || result.Job.Status != StatusDone || result.Job.LastEvent != "pr.merged" {
		t.Fatalf("result = %+v", result)
	}
	updated, err := Read(teamDir, "squ-77")
	if err != nil {
		t.Fatalf("Read updated: %v", err)
	}
	if updated.Status != StatusDone || updated.LastStatus != "pull request merged" {
		t.Fatalf("updated = %+v", updated)
	}
	events, err := ListEvents(teamDir, "squ-77")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "pr.merged" || events[0].Actor != "reconcile" || events[0].Data["matched_by"] != "pr_url" {
		t.Fatalf("events = %+v", events)
	}
}
