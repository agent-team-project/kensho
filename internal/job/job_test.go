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

func TestTicketIdentity(t *testing.T) {
	for _, tc := range []struct {
		raw       string
		ticket    string
		ticketURL string
		id        string
	}{
		{raw: "SQU-42", ticket: "SQU-42", id: "squ-42"},
		{
			raw:       "https://linear.app/squirtlesquad/issue/SQU-42/status-monitor",
			ticket:    "SQU-42",
			ticketURL: "https://linear.app/squirtlesquad/issue/SQU-42/status-monitor",
			id:        "squ-42",
		},
		{
			raw:       "https://linear.app/squirtlesquad/issue/squ-43/lowercase",
			ticket:    "SQU-43",
			ticketURL: "https://linear.app/squirtlesquad/issue/squ-43/lowercase",
			id:        "squ-43",
		},
	} {
		ticket, ticketURL := TicketIdentity(tc.raw)
		if ticket != tc.ticket || ticketURL != tc.ticketURL {
			t.Fatalf("TicketIdentity(%q) = %q, %q; want %q, %q", tc.raw, ticket, ticketURL, tc.ticket, tc.ticketURL)
		}
		j, err := New(tc.raw, "worker", "kickoff", time.Now())
		if err != nil {
			t.Fatalf("New(%q): %v", tc.raw, err)
		}
		if j.ID != tc.id || j.Ticket != tc.ticket || j.TicketURL != tc.ticketURL {
			t.Fatalf("job from %q = %+v", tc.raw, j)
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

func TestJobReadAndEventsAcceptTicketURL(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	ticketURL := "https://linear.app/squirtlesquad/issue/SQU-44/from-url"
	j, err := New(ticketURL, "worker", "from url", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.LastEvent = "created"
	j.LastStatus = "created"
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := AppendSnapshotEvent(teamDir, j, "", "test", "", nil); err != nil {
		t.Fatalf("AppendSnapshotEvent: %v", err)
	}
	read, err := Read(teamDir, ticketURL)
	if err != nil {
		t.Fatalf("Read URL: %v", err)
	}
	if read.ID != "squ-44" || read.Ticket != "SQU-44" || read.TicketURL != ticketURL {
		t.Fatalf("read = %+v", read)
	}
	events, err := ListEvents(teamDir, ticketURL)
	if err != nil {
		t.Fatalf("ListEvents URL: %v", err)
	}
	if len(events) != 1 || events[0].JobID != "squ-44" {
		t.Fatalf("events = %+v", events)
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
	j.Status = StatusQueued
	j.Steps = []Step{{ID: "review", Target: "manager", Status: StatusBlocked, Gate: StepGatePR}}
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected PR gate: %v", err)
	}
	j.Steps[0].Gate = "robot"
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid gate")
	}
	j.Steps[0].Gate = ""
	j.Steps[0].Skipped = true
	j.Steps[0].Status = StatusBlocked
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted skipped non-done step")
	}
	j.Steps[0].Status = StatusDone
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected skipped done step: %v", err)
	}
	j.Steps = []Step{{ID: "implement", Target: "worker", Status: StatusQueued, Timeout: "soon"}}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid step timeout")
	}
	j.Steps[0].Timeout = "0s"
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted zero step timeout")
	}
	j.Steps[0].Timeout = "15m"
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected valid step timeout: %v", err)
	}
	j.Steps[0].Attempts = -1
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted negative step attempts")
	}
	j.Steps[0].Attempts = 1
	j.Steps[0].MaxAttempts = -1
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted negative step max_attempts")
	}
	j.Steps[0].MaxAttempts = 2
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected valid attempt metadata: %v", err)
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
		Source:    "github",
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
	if len(events) != 1 || events[0].Type != "pr.merged" || events[0].Actor != "github" || events[0].Data["matched_by"] != "pr_url" || events[0].Data["source"] != "github" {
		t.Fatalf("events = %+v", events)
	}
}

func TestPreviewReconcilePRDoesNotWrite(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	j, err := New("SQU-78", "worker", "preview the change", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = StatusRunning
	j.PR = "https://github.com/acme/repo/pull/78"
	j.Branch = "worktree-worker-squ-78"
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	merged := true
	result, err := PreviewReconcilePR(teamDir, ReconcileInput{
		EventType: "pr.merged",
		Action:    "closed",
		PRURL:     "https://github.com/acme/repo/pull/78",
		Branch:    "worker-squ-78",
		Merged:    &merged,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("PreviewReconcilePR: %v", err)
	}
	if result.MatchedBy != "pr_url" || result.Job.Status != StatusDone || result.Job.Branch != "worker-squ-78" {
		t.Fatalf("preview result = %+v", result)
	}
	unchanged, err := Read(teamDir, "squ-78")
	if err != nil {
		t.Fatalf("Read unchanged: %v", err)
	}
	if unchanged.Status != StatusRunning || unchanged.LastEvent != "" || unchanged.Branch != "worktree-worker-squ-78" {
		t.Fatalf("preview mutated persisted job = %+v", unchanged)
	}
	events, err := ListEvents(teamDir, "squ-78")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("preview wrote events = %+v", events)
	}
}
