package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
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

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--summary", "--json"})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("job timeline summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary jobTimelineSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode timeline summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.JobID != j.ID || summary.Total != 3 || summary.FirstTS != now.Format(time.RFC3339) || summary.LastTS != now.Add(2*time.Minute).Format(time.RFC3339) {
		t.Fatalf("timeline summary = %+v", summary)
	}
	if summary.Sources["job"] != 2 || summary.Sources["lifecycle"] != 1 || summary.Kinds["created"] != 1 || summary.Kinds["dispatch"] != 1 || summary.Kinds["note"] != 1 {
		t.Fatalf("timeline summary counts = %+v", summary)
	}
	if summary.Statuses[string(job.StatusRunning)] != 2 || summary.Actors["operator"] != 1 || summary.Instances[j.Instance] != 1 || summary.Agents["worker"] != 1 {
		t.Fatalf("timeline summary buckets = %+v", summary)
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

	since := NewRootCmd()
	sinceOut, sinceErr := &bytes.Buffer{}, &bytes.Buffer{}
	since.SetOut(sinceOut)
	since.SetErr(sinceErr)
	since.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--since", now.Add(90 * time.Second).Format(time.RFC3339), "--format", "{{.Kind}}"})
	if err := since.Execute(); err != nil {
		t.Fatalf("job timeline since: %v\nstderr=%s", err, sinceErr.String())
	}
	if got, want := sinceOut.String(), "note\n"; got != want {
		t.Fatalf("timeline since = %q, want %q", got, want)
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

	invalidSince := NewRootCmd()
	invalidSinceOut, invalidSinceErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSince.SetOut(invalidSinceOut)
	invalidSince.SetErr(invalidSinceErr)
	invalidSince.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--since", "not-a-time"})
	if err := invalidSince.Execute(); err == nil {
		t.Fatalf("job timeline invalid since succeeded")
	}
	if !strings.Contains(invalidSinceErr.String(), "invalid --since") {
		t.Fatalf("invalid since stderr = %q", invalidSinceErr.String())
	}

	invalidSummaryFormat := NewRootCmd()
	invalidSummaryFormatOut, invalidSummaryFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSummaryFormat.SetOut(invalidSummaryFormatOut)
	invalidSummaryFormat.SetErr(invalidSummaryFormatErr)
	invalidSummaryFormat.SetArgs([]string{"job", "timeline", "squ-170", "--repo", tmp, "--summary", "--format", "{{.Kind}}"})
	if err := invalidSummaryFormat.Execute(); err == nil {
		t.Fatalf("job timeline accepted summary format: stdout=%s", invalidSummaryFormatOut.String())
	}
	if !strings.Contains(invalidSummaryFormatErr.String(), "--summary cannot be combined with --format") {
		t.Fatalf("summary format stderr = %q", invalidSummaryFormatErr.String())
	}
}

func TestJobTimelineAllIncludesEveryDurableJob(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Date(2026, 6, 26, 12, 30, 0, 0, time.UTC)
	pipelineJob := &job.Job{
		ID:        "squ-171",
		Ticket:    "SQU-171",
		Target:    "worker",
		Status:    job.StatusRunning,
		Instance:  "worker-squ-171",
		Pipeline:  "ticket_to_pr",
		CreatedAt: now,
		UpdatedAt: now.Add(2 * time.Minute),
	}
	adhocJob := &job.Job{
		ID:        "adhoc-171",
		Ticket:    "ADHOC-171",
		Target:    "manager",
		Status:    job.StatusRunning,
		Instance:  "manager-adhoc-171",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}
	for _, candidate := range []*job.Job{pipelineJob, adhocJob} {
		if err := job.Write(teamDir, candidate); err != nil {
			t.Fatalf("write job %s: %v", candidate.ID, err)
		}
	}
	for _, ev := range []job.Event{
		{TS: now, JobID: pipelineJob.ID, Type: "created", Status: job.StatusQueued, Actor: "cli", Message: "created pipeline job"},
		{TS: now.Add(time.Minute), JobID: adhocJob.ID, Type: "note", Status: job.StatusRunning, Instance: adhocJob.Instance, Actor: "operator", Message: "adhoc progress"},
		{TS: now.Add(2 * time.Minute), JobID: pipelineJob.ID, Type: "note", Status: job.StatusRunning, Actor: "operator", Message: "pipeline progress"},
	} {
		if err := job.AppendEvent(teamDir, &ev); err != nil {
			t.Fatalf("append job event: %v", err)
		}
	}
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "life-adhoc-171",
		TS:       now.Add(90 * time.Second),
		Action:   "dispatch",
		Instance: adhocJob.Instance,
		Agent:    "manager",
		Job:      adhocJob.ID,
		Status:   daemon.StatusRunning,
		Message:  "started manager",
	}); err != nil {
		t.Fatalf("append lifecycle event: %v", err)
	}
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		ID:       "life-unowned-171",
		TS:       now.Add(3 * time.Minute),
		Action:   "dispatch",
		Instance: "unowned-worker",
		Job:      "missing-171",
		Message:  "unowned lifecycle event",
	}); err != nil {
		t.Fatalf("append unowned lifecycle event: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"job", "timeline", "--all", "--repo", tmp, "--tail", "3", "--sort", "newest", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job timeline --all: %v\nstderr=%s", err, stderr.String())
	}
	var entries []jobTimelineEntry
	if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
		t.Fatalf("decode all timeline: %v\nbody=%s", err, out.String())
	}
	if len(entries) != 3 {
		t.Fatalf("all timeline entries = %+v", entries)
	}
	if entries[0].JobID != pipelineJob.ID || entries[0].Kind != "note" || entries[1].JobID != adhocJob.ID || entries[1].Kind != "dispatch" || entries[2].JobID != adhocJob.ID || entries[2].Kind != "note" {
		t.Fatalf("all timeline order = %+v", entries)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Message, "unowned") {
			t.Fatalf("all timeline included unowned lifecycle event: %+v", entries)
		}
	}

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{"job", "timeline", "--all", "--repo", tmp, "--source", "job", "--summary", "--json"})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("job timeline --all summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary jobTimelineSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode all timeline summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.Scope != "jobs" || summary.Total != 3 || summary.Jobs[pipelineJob.ID] != 2 || summary.Jobs[adhocJob.ID] != 1 {
		t.Fatalf("all timeline summary = %+v", summary)
	}
	if summary.Sources["job"] != 3 || summary.Sources["lifecycle"] != 0 || summary.Kinds["note"] != 2 || summary.Statuses[string(job.StatusRunning)] != 2 {
		t.Fatalf("all timeline summary counts = %+v", summary)
	}

	filteredCmd := NewRootCmd()
	filteredOut, filteredErr := &bytes.Buffer{}, &bytes.Buffer{}
	filteredCmd.SetOut(filteredOut)
	filteredCmd.SetErr(filteredErr)
	filteredCmd.SetArgs([]string{"job", "timeline", "--all", "--repo", tmp, "--job", "ADHOC-171", "--kind", "dispatch,note", "--instance", adhocJob.Instance, "--sort", "oldest", "--json"})
	if err := filteredCmd.Execute(); err != nil {
		t.Fatalf("job timeline --all filtered: %v\nstderr=%s", err, filteredErr.String())
	}
	var filtered []jobTimelineEntry
	if err := json.Unmarshal(filteredOut.Bytes(), &filtered); err != nil {
		t.Fatalf("decode filtered all timeline: %v\nbody=%s", err, filteredOut.String())
	}
	if len(filtered) != 2 || filtered[0].JobID != adhocJob.ID || filtered[0].Kind != "note" || filtered[1].Source != "lifecycle" || filtered[1].Agent != "manager" {
		t.Fatalf("filtered all timeline = %+v", filtered)
	}

	agentSummaryCmd := NewRootCmd()
	agentSummaryOut, agentSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	agentSummaryCmd.SetOut(agentSummaryOut)
	agentSummaryCmd.SetErr(agentSummaryErr)
	agentSummaryCmd.SetArgs([]string{"job", "timeline", "--all", "--repo", tmp, "--agent", "manager", "--status", "running", "--summary", "--json"})
	if err := agentSummaryCmd.Execute(); err != nil {
		t.Fatalf("job timeline --all agent summary: %v\nstderr=%s", err, agentSummaryErr.String())
	}
	var agentSummary jobTimelineSummaryJSON
	if err := json.Unmarshal(agentSummaryOut.Bytes(), &agentSummary); err != nil {
		t.Fatalf("decode agent timeline summary: %v\nbody=%s", err, agentSummaryOut.String())
	}
	if agentSummary.Total != 1 || agentSummary.Jobs[adhocJob.ID] != 1 || agentSummary.Agents["manager"] != 1 || agentSummary.Sources["lifecycle"] != 1 {
		t.Fatalf("agent-filtered timeline summary = %+v", agentSummary)
	}

	invalidAllWithID := NewRootCmd()
	invalidAllWithIDOut, invalidAllWithIDErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidAllWithID.SetOut(invalidAllWithIDOut)
	invalidAllWithID.SetErr(invalidAllWithIDErr)
	invalidAllWithID.SetArgs([]string{"job", "timeline", "--all", pipelineJob.ID, "--repo", tmp})
	if err := invalidAllWithID.Execute(); err == nil {
		t.Fatalf("job timeline accepted --all with job id: stdout=%s", invalidAllWithIDOut.String())
	}
	if !strings.Contains(invalidAllWithIDErr.String(), "--all cannot be combined with a job id") {
		t.Fatalf("all with id stderr = %q", invalidAllWithIDErr.String())
	}

	missingID := NewRootCmd()
	missingIDOut, missingIDErr := &bytes.Buffer{}, &bytes.Buffer{}
	missingID.SetOut(missingIDOut)
	missingID.SetErr(missingIDErr)
	missingID.SetArgs([]string{"job", "timeline", "--repo", tmp})
	if err := missingID.Execute(); err == nil {
		t.Fatalf("job timeline accepted missing id: stdout=%s", missingIDOut.String())
	}
	if !strings.Contains(missingIDErr.String(), "job id is required") {
		t.Fatalf("missing id stderr = %q", missingIDErr.String())
	}

	tooMany := NewRootCmd()
	tooManyOut, tooManyErr := &bytes.Buffer{}, &bytes.Buffer{}
	tooMany.SetOut(tooManyOut)
	tooMany.SetErr(tooManyErr)
	tooMany.SetArgs([]string{"job", "timeline", pipelineJob.ID, adhocJob.ID, "--repo", tmp})
	if err := tooMany.Execute(); err == nil {
		t.Fatalf("job timeline accepted too many ids: stdout=%s", tooManyOut.String())
	}
	if !strings.Contains(tooManyErr.String(), "pass at most one job id") {
		t.Fatalf("too many ids stderr = %q", tooManyErr.String())
	}

	invalidJobFilter := NewRootCmd()
	invalidJobFilterOut, invalidJobFilterErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidJobFilter.SetOut(invalidJobFilterOut)
	invalidJobFilter.SetErr(invalidJobFilterErr)
	invalidJobFilter.SetArgs([]string{"job", "timeline", "--all", "--repo", tmp, "--job", ","})
	if err := invalidJobFilter.Execute(); err == nil {
		t.Fatalf("job timeline accepted empty job filter: stdout=%s", invalidJobFilterOut.String())
	}
	if !strings.Contains(invalidJobFilterErr.String(), "--job requires at least one non-empty job id") {
		t.Fatalf("empty job filter stderr = %q", invalidJobFilterErr.String())
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

	pipelineSummaryCmd := NewRootCmd()
	pipelineSummaryOut, pipelineSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineSummaryCmd.SetOut(pipelineSummaryOut)
	pipelineSummaryCmd.SetErr(pipelineSummaryErr)
	pipelineSummaryCmd.SetArgs([]string{"pipeline", "timeline", "ticket_to_pr", "--repo", tmp, "--summary", "--json"})
	if err := pipelineSummaryCmd.Execute(); err != nil {
		t.Fatalf("pipeline timeline summary: %v\nstderr=%s", err, pipelineSummaryErr.String())
	}
	var pipelineSummary jobTimelineSummaryJSON
	if err := json.Unmarshal(pipelineSummaryOut.Bytes(), &pipelineSummary); err != nil {
		t.Fatalf("decode pipeline timeline summary: %v\nbody=%s", err, pipelineSummaryOut.String())
	}
	if pipelineSummary.Scope != "pipeline:ticket_to_pr" || pipelineSummary.Total != 3 || pipelineSummary.Jobs[j.ID] != 3 || pipelineSummary.Jobs[other.ID] != 0 {
		t.Fatalf("pipeline timeline summary = %+v", pipelineSummary)
	}
	if pipelineSummary.Sources["job"] != 2 || pipelineSummary.Sources["lifecycle"] != 1 || pipelineSummary.Kinds["dispatch"] != 1 {
		t.Fatalf("pipeline timeline summary counts = %+v", pipelineSummary)
	}

	pipelineFilterCmd := NewRootCmd()
	pipelineFilterOut, pipelineFilterErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelineFilterCmd.SetOut(pipelineFilterOut)
	pipelineFilterCmd.SetErr(pipelineFilterErr)
	pipelineFilterCmd.SetArgs([]string{"pipeline", "timeline", "ticket_to_pr", "--repo", tmp, "--actor", "operator", "--kind", "note", "--format", "{{.JobID}} {{.Kind}} {{.Actor}}"})
	if err := pipelineFilterCmd.Execute(); err != nil {
		t.Fatalf("pipeline timeline filtered: %v\nstderr=%s", err, pipelineFilterErr.String())
	}
	if got, want := pipelineFilterOut.String(), "squ-181 note operator\n"; got != want {
		t.Fatalf("pipeline filtered timeline = %q, want %q", got, want)
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

	teamFilterCmd := NewRootCmd()
	teamFilterOut, teamFilterErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamFilterCmd.SetOut(teamFilterOut)
	teamFilterCmd.SetErr(teamFilterErr)
	teamFilterCmd.SetArgs([]string{"team", "timeline", "delivery", "--repo", tmp, "--job", "SQU-181", "--agent", "worker", "--status", "running", "--format", "{{.JobID}} {{.Source}} {{.Agent}}"})
	if err := teamFilterCmd.Execute(); err != nil {
		t.Fatalf("team timeline filtered: %v\nstderr=%s", err, teamFilterErr.String())
	}
	if got, want := teamFilterOut.String(), "squ-181 lifecycle worker\n"; got != want {
		t.Fatalf("team filtered timeline = %q, want %q", got, want)
	}

	teamSummaryCmd := NewRootCmd()
	teamSummaryOut, teamSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamSummaryCmd.SetOut(teamSummaryOut)
	teamSummaryCmd.SetErr(teamSummaryErr)
	teamSummaryCmd.SetArgs([]string{"team", "timeline", "delivery", "--repo", tmp, "--source", "lifecycle", "--summary"})
	if err := teamSummaryCmd.Execute(); err != nil {
		t.Fatalf("team timeline summary: %v\nstderr=%s", err, teamSummaryErr.String())
	}
	teamSummary := teamSummaryOut.String()
	for _, want := range []string{
		"job timeline: scope=team:delivery total=1 first=2026-06-26T13:01:30Z last=2026-06-26T13:01:30Z\n",
		"jobs: squ-181=1\n",
		"sources: lifecycle=1\n",
		"kinds: dispatch=1\n",
		"statuses: running=1\n",
		"instances: worker-squ-181=1\n",
		"agents: worker=1\n",
	} {
		if !strings.Contains(teamSummary, want) {
			t.Fatalf("team timeline summary missing %q in %q", want, teamSummary)
		}
	}

	teamSince := NewRootCmd()
	teamSinceOut, teamSinceErr := &bytes.Buffer{}, &bytes.Buffer{}
	teamSince.SetOut(teamSinceOut)
	teamSince.SetErr(teamSinceErr)
	teamSince.SetArgs([]string{"team", "timeline", "delivery", "--repo", tmp, "--since", now.Add(80 * time.Second).Format(time.RFC3339), "--format", "{{.JobID}} {{.Kind}}"})
	if err := teamSince.Execute(); err != nil {
		t.Fatalf("team timeline since: %v\nstderr=%s", err, teamSinceErr.String())
	}
	if got, want := teamSinceOut.String(), "squ-181 dispatch\n"; got != want {
		t.Fatalf("team timeline since output = %q, want %q", got, want)
	}
}
