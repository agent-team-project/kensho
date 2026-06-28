package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestJobTimelineCombinesAuditAndLifecycle(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-170",
		Ticket:    "SQU-170",
		Target:    "worker",
		Status:    job.StatusRunning,
		Instance:  "worker-squ-170",
		CreatedAt: now,
		UpdatedAt: now.Add(2 * time.Minute),
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	for _, ev := range []job.Event{
		{TS: now, JobID: j.ID, Type: "created", Status: job.StatusQueued, Actor: "cli", Message: "created job"},
		{TS: now.Add(2 * time.Minute), JobID: j.ID, Type: "note", Status: job.StatusRunning, Actor: "operator", Message: "checked progress"},
	} {
		if err := job.AppendEvent(teamDir, &ev); err != nil {
			t.Fatalf("append job event: %v", err)
		}
	}
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "life-170",
		TS:       now.Add(time.Minute),
		Action:   "dispatch",
		Instance: j.Instance,
		Agent:    "worker",
		Job:      j.ID,
		Status:   daemon.StatusRunning,
		Message:  "started worker",
	}); err != nil {
		t.Fatalf("append lifecycle event: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "life-other",
		TS:       now.Add(3 * time.Minute),
		Action:   "dispatch",
		Instance: "other-worker",
		Job:      "squ-999",
		Message:  "unrelated",
	}); err != nil {
		t.Fatalf("append unrelated lifecycle event: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--tail", "2", "--sort", "newest", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job timeline: %v\nstderr=%s", err, stderr.String())
	}
	var entries []jobTimelineEntry
	if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
		t.Fatalf("decode timeline: %v\nbody=%s", err, out.String())
	}
	if len(entries) != 2 {
		t.Fatalf("timeline entries = %+v", entries)
	}
	if entries[0].Source != "job" || entries[0].Kind != "note" || entries[1].Source != "lifecycle" || entries[1].Kind != "dispatch" {
		t.Fatalf("timeline order = %+v", entries)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Message, "unrelated") {
			t.Fatalf("timeline included unrelated event: %+v", entries)
		}
	}

	formatted := NewRootCmd()
	formattedOut, formattedErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formattedOut)
	formatted.SetErr(formattedErr)
	formatted.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--source", "lifecycle", "--format", "{{.Source}}:{{.Kind}}:{{.Instance}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("job timeline format: %v\nstderr=%s", err, formattedErr.String())
	}
	if got, want := formattedOut.String(), "lifecycle:dispatch:worker-squ-170\n"; got != want {
		t.Fatalf("timeline format = %q, want %q", got, want)
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--source", "bogus"})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("job timeline invalid source succeeded")
	}
	if !strings.Contains(invalidErr.String(), "--source must be all, job, or lifecycle") {
		t.Fatalf("invalid source stderr = %q", invalidErr.String())
	}
}

func TestScopedTimelineCommands(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 26, 13, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-181",
		Ticket:    "SQU-181",
		Target:    "worker",
		Status:    job.StatusRunning,
		Instance:  "worker-squ-181",
		Pipeline:  "ticket_to_pr",
		CreatedAt: now,
		UpdatedAt: now.Add(2 * time.Minute),
	}
	other := &job.Job{
		ID:        "oth-181",
		Ticket:    "OTH-181",
		Target:    "external",
		Status:    job.StatusRunning,
		Instance:  "external-oth-181",
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, candidate := range []*job.Job{j, other} {
		if err := job.Write(teamDir, candidate); err != nil {
			t.Fatalf("write job %s: %v", candidate.ID, err)
		}
	}
	for _, ev := range []job.Event{
		{TS: now, JobID: j.ID, Type: "created", Status: job.StatusQueued, Actor: "cli", Message: "created scoped job"},
		{TS: now.Add(time.Minute), JobID: j.ID, Type: "note", Status: job.StatusRunning, Actor: "operator", Message: "pipeline note"},
		{TS: now.Add(2 * time.Minute), JobID: other.ID, Type: "note", Status: job.StatusRunning, Actor: "operator", Message: "unrelated note"},
	} {
		if err := job.AppendEvent(teamDir, &ev); err != nil {
			t.Fatalf("append job event: %v", err)
		}
	}
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "life-181",
		TS:       now.Add(90 * time.Second),
		Action:   "dispatch",
		Instance: j.Instance,
		Agent:    "worker",
		Job:      j.ID,
		Status:   daemon.StatusRunning,
		Message:  "pipeline worker started",
	}); err != nil {
		t.Fatalf("append lifecycle event: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "life-other-181",
		TS:       now.Add(3 * time.Minute),
		Action:   "dispatch",
		Instance: other.Instance,
		Agent:    "external",
		Job:      other.ID,
		Message:  "unrelated worker started",
	}); err != nil {
		t.Fatalf("append unrelated lifecycle event: %v", err)
	}

	pipelineCmd := NewRootCmd()
	pipelineOut, pipelineErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineCmd.SetOut(pipelineOut)
	pipelineCmd.SetErr(pipelineErr)
	pipelineCmd.SetArgs([]string{"pipeline", "timeline", "ticket_to_pr", "--repo", tmp, "--tail", "2", "--sort", "newest", "--json"})
	if err := pipelineCmd.Execute(); err != nil {
		t.Fatalf("pipeline timeline: %v\nstderr=%s", err, pipelineErr.String())
	}
	var pipelineEntries []jobTimelineEntry
	if err := json.Unmarshal(pipelineOut.Bytes(), &pipelineEntries); err != nil {
		t.Fatalf("decode pipeline timeline: %v\nbody=%s", err, pipelineOut.String())
	}
	if len(pipelineEntries) != 2 || pipelineEntries[0].JobID != j.ID || pipelineEntries[0].Kind != "dispatch" || pipelineEntries[1].Kind != "note" {
		t.Fatalf("pipeline timeline entries = %+v", pipelineEntries)
	}
	for _, entry := range pipelineEntries {
		if entry.JobID == other.ID || strings.Contains(entry.Message, "unrelated") {
			t.Fatalf("pipeline timeline included unrelated event: %+v", pipelineEntries)
		}
	}

	teamCmd := NewRootCmd()
	teamOut, teamErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamCmd.SetOut(teamOut)
	teamCmd.SetErr(teamErr)
	teamCmd.SetArgs([]string{"team", "timeline", "delivery", "--repo", tmp, "--source", "lifecycle", "--format", "{{.JobID}} {{.Source}} {{.Kind}} {{.Instance}}"})
	if err := teamCmd.Execute(); err != nil {
		t.Fatalf("team timeline: %v\nstderr=%s", err, teamErr.String())
	}
	if got, want := teamOut.String(), "squ-181 lifecycle dispatch worker-squ-181\n"; got != want {
		t.Fatalf("team timeline output = %q, want %q", got, want)
	}
}
