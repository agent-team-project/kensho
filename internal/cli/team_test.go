package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
)

func TestTeamCommandsListShowAndStatus(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.ticket-manager]
agent = "ticket-manager"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]

[schedules.nightly]
every = "24h"

[teams.delivery]
description = "Default delivery team."
instances = ["manager", "ticket-manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "idle"
description = "ready"
since = "2026-06-18T12:00:00Z"
`, now)
	pipelineJob := &job.Job{
		ID:        "squ-801",
		Ticket:    "SQU-801",
		Target:    "worker",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusDone},
			{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
		},
	}
	if err := job.Write(teamDir, pipelineJob); err != nil {
		t.Fatalf("write job: %v", err)
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"team", "ls", "--repo", root})
	if err := list.Execute(); err != nil {
		t.Fatalf("team ls: %v\nstderr=%s", err, listErr.String())
	}
	for _, want := range []string{"TEAM", "delivery", "Default delivery team.", "3", "1"} {
		if !strings.Contains(listOut.String(), want) {
			t.Fatalf("team ls missing %q:\n%s", want, listOut.String())
		}
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"team", "show", "delivery", "--repo", root, "--json"})
	if err := show.Execute(); err != nil {
		t.Fatalf("team show: %v\nstderr=%s", err, showErr.String())
	}
	var info teamInfo
	if err := json.Unmarshal(showOut.Bytes(), &info); err != nil {
		t.Fatalf("decode team show: %v\nbody=%s", err, showOut.String())
	}
	if info.Name != "delivery" || len(info.Instances) != 3 || len(info.Pipelines) != 1 || len(info.Schedules) != 1 {
		t.Fatalf("team info = %+v", info)
	}

	jobs := NewRootCmd()
	jobsOut, jobsErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobs.SetOut(jobsOut)
	jobs.SetErr(jobsErr)
	jobs.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--status", "running", "--json"})
	if err := jobs.Execute(); err != nil {
		t.Fatalf("team jobs: %v\nstderr=%s", err, jobsErr.String())
	}
	var ownedJobs []job.Job
	if err := json.Unmarshal(jobsOut.Bytes(), &ownedJobs); err != nil {
		t.Fatalf("decode team jobs: %v\nbody=%s", err, jobsOut.String())
	}
	if len(ownedJobs) != 1 || ownedJobs[0].ID != "squ-801" {
		t.Fatalf("owned jobs = %+v", ownedJobs)
	}

	format := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	format.SetOut(formatOut)
	format.SetErr(formatErr)
	format.SetArgs([]string{"team", "jobs", "delivery", "--repo", root, "--format", "{{.ID}} {{.Pipeline}}"})
	if err := format.Execute(); err != nil {
		t.Fatalf("team jobs format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.TrimSpace(formatOut.String()); got != "squ-801 ticket_to_pr" {
		t.Fatalf("team jobs format = %q", got)
	}

	status := NewRootCmd()
	statusOut, statusErr := &bytes.Buffer{}, &bytes.Buffer{}
	status.SetOut(statusOut)
	status.SetErr(statusErr)
	status.SetArgs([]string{"team", "status", "delivery", "--repo", root, "--json"})
	if err := status.Execute(); err != nil {
		t.Fatalf("team status: %v\nstderr=%s", err, statusErr.String())
	}
	var snapshot teamStatusSnapshot
	if err := json.Unmarshal(statusOut.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team status: %v\nbody=%s", err, statusOut.String())
	}
	if snapshot.Team.Name != "delivery" || snapshot.InstanceSummary.Total != 3 || snapshot.JobSummary.Total != 1 {
		t.Fatalf("team status summary = %+v", snapshot)
	}
	if len(snapshot.PipelineStatus) != 1 || snapshot.PipelineStatus[0].Pipeline != "ticket_to_pr" || snapshot.PipelineStatus[0].ReadySteps != 1 {
		t.Fatalf("pipeline status = %+v", snapshot.PipelineStatus)
	}
	if !containsString(snapshot.Actions, "agent-team start manager ticket-manager") {
		t.Fatalf("actions missing persistent start hint: %+v", snapshot.Actions)
	}
	if containsString(snapshot.Actions, "agent-team start worker") {
		t.Fatalf("actions should not start ephemeral worker: %+v", snapshot.Actions)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "status", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team status text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Team: delivery", "instances: total=3", "jobs: total=1", "pipeline status: pipelines=1 jobs=1 ready_steps=1", "Actions:", "agent-team pipeline advance ticket_to_pr --dry-run --preview-routes"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team status text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestTeamShowMissingFails(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "show", "missing", "--repo", root})
	if err := cmd.Execute(); err == nil {
		t.Fatal("team show missing succeeded")
	}
	if !strings.Contains(stderr.String(), `team "missing" not found`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTeamStatusWatchRendersSnapshot(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"

[teams.delivery]
instances = ["manager"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := runTeamStatusWatch(ctx, &out, teamDir, "delivery", time.Millisecond, false, false); err != nil {
		t.Fatalf("team status watch: %v", err)
	}
	if !strings.Contains(out.String(), "Team: delivery") || !strings.Contains(out.String(), "instances: total=1") {
		t.Fatalf("watch output missing team snapshot:\n%s", out.String())
	}

	jsonCtx, jsonCancel := context.WithCancel(context.Background())
	jsonCancel()
	var jsonOut bytes.Buffer
	if err := runTeamStatusWatch(jsonCtx, &jsonOut, teamDir, "delivery", time.Millisecond, true, false); err != nil {
		t.Fatalf("team status watch json: %v", err)
	}
	var snapshot teamStatusSnapshot
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut.Bytes()), &snapshot); err != nil {
		t.Fatalf("decode watch json: %v\nbody=%s", err, jsonOut.String())
	}
	if snapshot.Team.Name != "delivery" {
		t.Fatalf("watch json snapshot = %+v", snapshot)
	}
}

func TestTeamStatusRejectsNegativeInterval(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "status", "delivery", "--watch", "--interval", "-1s"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("team status negative interval succeeded")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
