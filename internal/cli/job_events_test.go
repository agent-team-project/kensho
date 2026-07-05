package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/job"
)

func TestJobEventsAll(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{ID: "squ-701", Ticket: "SQU-701", Target: "worker", Status: job.StatusRunning, CreatedAt: base, UpdatedAt: base},
		{ID: "squ-702", Ticket: "SQU-702", Target: "manager", Status: job.StatusDone, CreatedAt: base, UpdatedAt: base},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
	}
	for _, ev := range []job.Event{
		{TS: base.Add(time.Minute), JobID: "squ-701", Type: "created", Status: job.StatusQueued, Actor: "cli", Message: "created"},
		{TS: base.Add(2 * time.Minute), JobID: "squ-701", Type: "updated", Status: job.StatusRunning, Instance: "worker-squ-701", Actor: "daemon", Message: "started"},
		{TS: base.Add(3 * time.Minute), JobID: "squ-702", Type: "closed", Status: job.StatusDone, Instance: "manager-squ-702", Actor: "cli", Message: "closed"},
	} {
		ev := ev
		if err := job.AppendEvent(teamDir, &ev); err != nil {
			t.Fatalf("append event %s/%s: %v", ev.JobID, ev.Type, err)
		}
	}

	filtered := NewRootCmd()
	filteredOut, filteredErr := &bytes.Buffer{}, &bytes.Buffer{}
	filtered.SetOut(filteredOut)
	filtered.SetErr(filteredErr)
	filtered.SetArgs([]string{"job", "events", "--all", "--repo", root, "--status", "running", "--json"})
	if err := filtered.Execute(); err != nil {
		t.Fatalf("job events --all filtered: %v\nstderr=%s", err, filteredErr.String())
	}
	var events []job.Event
	if err := json.Unmarshal(filteredOut.Bytes(), &events); err != nil {
		t.Fatalf("decode job events --all: %v\nbody=%s", err, filteredOut.String())
	}
	if len(events) != 1 || events[0].JobID != "squ-701" || events[0].Status != job.StatusRunning {
		t.Fatalf("job events --all filtered = %+v", events)
	}

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{"job", "events", "--all", "--repo", root, "--summary", "--json"})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("job events --all summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary jobEventSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode job events --all summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.Scope != "jobs" || summary.Total != 3 || summary.Jobs["squ-701"] != 2 || summary.Jobs["squ-702"] != 1 {
		t.Fatalf("job events --all summary = %+v", summary)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	follow := NewRootCmd()
	followOut, followErr := &bytes.Buffer{}, &bytes.Buffer{}
	follow.SetContext(ctx)
	follow.SetOut(followOut)
	follow.SetErr(followErr)
	follow.SetArgs([]string{"job", "events", "--all", "--repo", root, "--follow", "--tail", "1", "--interval", "1ms", "--format", "{{.JobID}} {{.Type}} {{.Status}}"})
	if err := follow.Execute(); err != nil {
		t.Fatalf("job events --all follow: %v\nstderr=%s", err, followErr.String())
	}
	if got := strings.TrimSpace(followOut.String()); got != "squ-702 closed done" {
		t.Fatalf("job events --all follow = %q", got)
	}

	newestAll := NewRootCmd()
	newestAllOut, newestAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	newestAll.SetOut(newestAllOut)
	newestAll.SetErr(newestAllErr)
	newestAll.SetArgs([]string{"job", "events", "--all", "--repo", root, "--tail", "2", "--sort", "newest", "--format", "{{.JobID}} {{.Type}}"})
	if err := newestAll.Execute(); err != nil {
		t.Fatalf("job events --all newest: %v\nstderr=%s", err, newestAllErr.String())
	}
	if got := strings.TrimSpace(newestAllOut.String()); got != "squ-702 closed\nsqu-701 updated" {
		t.Fatalf("job events --all newest = %q", got)
	}

	jobs, err := job.List(teamDir)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	indexes := map[string]int{}
	if initial, err := collectScopedJobEventsSnapshot(teamDir, jobs, jobEventFilters{}, indexes); err != nil {
		t.Fatalf("collect initial scoped events: %v", err)
	} else if len(initial) != 3 {
		t.Fatalf("initial scoped events = %+v", initial)
	}
	appended := job.Event{TS: base.Add(4 * time.Minute), JobID: "squ-702", Type: "note", Status: job.StatusDone, Instance: "manager-squ-702", Actor: "cli", Message: "follow-up"}
	if err := job.AppendEvent(teamDir, &appended); err != nil {
		t.Fatalf("append follow-up event: %v", err)
	}
	next, err := collectNewScopedJobEvents(teamDir, jobs, jobEventFilters{}, indexes)
	if err != nil {
		t.Fatalf("collect new scoped events: %v", err)
	}
	if len(next) != 1 || next[0].JobID != "squ-702" || next[0].Type != "note" {
		t.Fatalf("new scoped events = %+v", next)
	}
	again, err := collectNewScopedJobEvents(teamDir, jobs, jobEventFilters{}, indexes)
	if err != nil {
		t.Fatalf("collect repeated scoped events: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("repeated scoped events = %+v", again)
	}

	invalidAll := NewRootCmd()
	invalidAllOut, invalidAllErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAll.SetOut(invalidAllOut)
	invalidAll.SetErr(invalidAllErr)
	invalidAll.SetArgs([]string{"job", "events", "squ-701", "--all", "--repo", root})
	if err := invalidAll.Execute(); err == nil {
		t.Fatalf("job events accepted --all with job id: stdout=%s", invalidAllOut.String())
	}
	if !strings.Contains(invalidAllErr.String(), "--all cannot be combined with a job id") {
		t.Fatalf("job events --all error = %q", invalidAllErr.String())
	}

	missingID := NewRootCmd()
	missingIDOut, missingIDErr := &bytes.Buffer{}, &bytes.Buffer{}
	missingID.SetOut(missingIDOut)
	missingID.SetErr(missingIDErr)
	missingID.SetArgs([]string{"job", "events", "--repo", root})
	if err := missingID.Execute(); err == nil {
		t.Fatalf("job events accepted missing job id: stdout=%s", missingIDOut.String())
	}
	if !strings.Contains(missingIDErr.String(), "job id is required") {
		t.Fatalf("job events missing id error = %q", missingIDErr.String())
	}

	invalidSort := NewRootCmd()
	invalidSortOut, invalidSortErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSort.SetOut(invalidSortOut)
	invalidSort.SetErr(invalidSortErr)
	invalidSort.SetArgs([]string{"job", "events", "--all", "--repo", root, "--sort", "sideways"})
	if err := invalidSort.Execute(); err == nil {
		t.Fatalf("job events accepted invalid sort: stdout=%s", invalidSortOut.String())
	}
	if !strings.Contains(invalidSortErr.String(), "--sort must be oldest or newest") {
		t.Fatalf("job events invalid sort error = %q", invalidSortErr.String())
	}

	invalidSortFollow := NewRootCmd()
	invalidSortFollowOut, invalidSortFollowErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSortFollow.SetOut(invalidSortFollowOut)
	invalidSortFollow.SetErr(invalidSortFollowErr)
	invalidSortFollow.SetArgs([]string{"job", "events", "--all", "--repo", root, "--follow", "--sort", "newest"})
	if err := invalidSortFollow.Execute(); err == nil {
		t.Fatalf("job events accepted newest follow: stdout=%s", invalidSortFollowOut.String())
	}
	if !strings.Contains(invalidSortFollowErr.String(), "--sort newest cannot be combined with --follow") {
		t.Fatalf("job events newest follow error = %q", invalidSortFollowErr.String())
	}
}

func TestJobEventsFilters(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-75",
		Ticket:    "SQU-75",
		Target:    "worker",
		Status:    job.StatusRunning,
		CreatedAt: base.Add(-2 * time.Hour),
		UpdatedAt: base,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, ev := range []job.Event{
		{
			TS:      base.Add(-2 * time.Hour),
			JobID:   j.ID,
			Type:    "created",
			Status:  job.StatusQueued,
			Actor:   "cli",
			Message: "created",
		},
		{
			TS:       base.Add(-30 * time.Minute),
			JobID:    j.ID,
			Type:     "updated",
			Status:   job.StatusRunning,
			Instance: "worker-squ-75",
			Actor:    "daemon",
			Message:  "started",
		},
		{
			TS:       base.Add(-10 * time.Minute),
			JobID:    j.ID,
			Type:     "closed",
			Status:   job.StatusDone,
			Instance: "worker-squ-75",
			Actor:    "cli",
			Message:  "closed",
		},
	} {
		ev := ev
		if err := job.AppendEvent(teamDir, &ev); err != nil {
			t.Fatalf("append event %s: %v", ev.Type, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"job", "events", "squ-75",
		"--repo", tmp,
		"--type", "closed",
		"--actor", "cli",
		"--status", "done",
		"--instance", "worker-squ-75",
		"--since", base.Add(-time.Hour).Format(time.RFC3339),
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job events filters: %v\nstderr=%s", err, stderr.String())
	}
	var got []job.Event
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode filtered events json: %v\nbody=%s", err, out.String())
	}
	if len(got) != 1 || got[0].Type != "closed" || got[0].Actor != "cli" || got[0].Status != job.StatusDone || got[0].Instance != "worker-squ-75" {
		t.Fatalf("filtered events = %+v", got)
	}

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{
		"job", "events", "squ-75",
		"--repo", tmp,
		"--type", "closed",
		"--actor", "cli",
		"--status", "done",
		"--instance", "worker-squ-75",
		"--since", base.Add(-time.Hour).Format(time.RFC3339),
		"--summary",
		"--json",
	})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("job events filtered summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var filteredSummary jobEventSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &filteredSummary); err != nil {
		t.Fatalf("decode filtered summary json: %v\nbody=%s", err, summaryOut.String())
	}
	if filteredSummary.Total != 1 || filteredSummary.Types["closed"] != 1 || filteredSummary.Actors["cli"] != 1 || filteredSummary.Instances["worker-squ-75"] != 1 {
		t.Fatalf("filtered summary = %+v", filteredSummary)
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"job", "events", "squ-75",
		"--repo", tmp,
		"--type", "created",
		"--since", base.Add(-time.Hour).Format(time.RFC3339),
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job events filtered empty: %v\nstderr=%s", err, stderr.String())
	}
	got = nil
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode empty filtered events json: %v\nbody=%s", err, out.String())
	}
	if len(got) != 0 {
		t.Fatalf("expected no events, got %+v", got)
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "events", "squ-75", "--repo", tmp, "--type", ","})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job events empty type filter succeeded")
	}
	if !strings.Contains(stderr.String(), "--type requires at least one non-empty event type") {
		t.Fatalf("missing empty filter error:\n%s", stderr.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "events", "squ-75", "--repo", tmp, "--status", "paused"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job events unknown status filter succeeded")
	}
	if !strings.Contains(stderr.String(), `unknown --status "paused"`) {
		t.Fatalf("missing unknown status error:\n%s", stderr.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "events", "squ-75", "--repo", tmp, "--status", ","})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job events empty status filter succeeded")
	}
	if !strings.Contains(stderr.String(), "--status requires at least one non-empty status") {
		t.Fatalf("missing empty status error:\n%s", stderr.String())
	}

	cmd = NewRootCmd()
	out, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "events", "squ-75", "--repo", tmp, "--instance", ","})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("job events empty instance filter succeeded")
	}
	if !strings.Contains(stderr.String(), "--instance requires at least one non-empty instance") {
		t.Fatalf("missing empty instance error:\n%s", stderr.String())
	}
}

func TestJobEventsSummaryValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "follow",
			args: []string{"job", "events", "squ-42", "--summary", "--follow"},
			want: "--summary cannot be combined with --follow",
		},
		{
			name: "format",
			args: []string{"job", "events", "squ-42", "--summary", "--format", "{{.Type}}"},
			want: "--summary cannot be combined with --format",
		},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%s unexpectedly succeeded", tc.name)
		}
		var code ExitCode
		if !errors.As(err, &code) || int(code) != 2 {
			t.Fatalf("%s err = %v, want exit 2", tc.name, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%s stderr = %q, want %q", tc.name, stderr.String(), tc.want)
		}
	}
}
