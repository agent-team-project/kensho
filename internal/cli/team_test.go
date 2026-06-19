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

	"github.com/jamesaud/agent-team/internal/daemon"
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

	ps := NewRootCmd()
	psOut, psErr := &bytes.Buffer{}, &bytes.Buffer{}
	ps.SetOut(psOut)
	ps.SetErr(psErr)
	ps.SetArgs([]string{"team", "ps", "delivery", "--repo", root, "--json"})
	if err := ps.Execute(); err != nil {
		t.Fatalf("team ps: %v\nstderr=%s", err, psErr.String())
	}
	var instanceRows []psJSONRow
	if err := json.Unmarshal(psOut.Bytes(), &instanceRows); err != nil {
		t.Fatalf("decode team ps: %v\nbody=%s", err, psOut.String())
	}
	if len(instanceRows) != 3 {
		t.Fatalf("team ps rows = %+v", instanceRows)
	}
	instances := map[string]psJSONRow{}
	for _, row := range instanceRows {
		instances[row.Instance] = row
	}
	if instances["manager"].Phase != "idle" || instances["ticket-manager"].Agent != "ticket-manager" || instances["worker"].Agent != "worker" {
		t.Fatalf("team ps instances = %+v", instances)
	}

	psAlias := NewRootCmd()
	psAliasOut, psAliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	psAlias.SetOut(psAliasOut)
	psAlias.SetErr(psAliasErr)
	psAlias.SetArgs([]string{"team", "instances", "delivery", "--repo", root})
	if err := psAlias.Execute(); err != nil {
		t.Fatalf("team instances alias: %v\nstderr=%s", err, psAliasErr.String())
	}
	for _, want := range []string{"INSTANCE", "manager", "ticket-manager", "worker"} {
		if !strings.Contains(psAliasOut.String(), want) {
			t.Fatalf("team ps text missing %q:\n%s", want, psAliasOut.String())
		}
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

	pipelines := NewRootCmd()
	pipelinesOut, pipelinesErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelines.SetOut(pipelinesOut)
	pipelines.SetErr(pipelinesErr)
	pipelines.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--json"})
	if err := pipelines.Execute(); err != nil {
		t.Fatalf("team pipelines: %v\nstderr=%s", err, pipelinesErr.String())
	}
	var pipelineRows []pipelineStatusRow
	if err := json.Unmarshal(pipelinesOut.Bytes(), &pipelineRows); err != nil {
		t.Fatalf("decode team pipelines: %v\nbody=%s", err, pipelinesOut.String())
	}
	if len(pipelineRows) != 1 || pipelineRows[0].Pipeline != "ticket_to_pr" || pipelineRows[0].ReadySteps != 1 {
		t.Fatalf("team pipeline rows = %+v", pipelineRows)
	}

	pipelinesFormat := NewRootCmd()
	pipelinesFormatOut, pipelinesFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	pipelinesFormat.SetOut(pipelinesFormatOut)
	pipelinesFormat.SetErr(pipelinesFormatErr)
	pipelinesFormat.SetArgs([]string{"team", "pipelines", "delivery", "--repo", root, "--format", "{{.Pipeline}} {{.ReadySteps}}"})
	if err := pipelinesFormat.Execute(); err != nil {
		t.Fatalf("team pipelines format: %v\nstderr=%s", err, pipelinesFormatErr.String())
	}
	if got := strings.TrimSpace(pipelinesFormatOut.String()); got != "ticket_to_pr 1" {
		t.Fatalf("team pipelines format = %q", got)
	}

	schedules := NewRootCmd()
	schedulesOut, schedulesErr := &bytes.Buffer{}, &bytes.Buffer{}
	schedules.SetOut(schedulesOut)
	schedules.SetErr(schedulesErr)
	schedules.SetArgs([]string{"team", "schedules", "delivery", "--repo", root, "--json"})
	if err := schedules.Execute(); err != nil {
		t.Fatalf("team schedules: %v\nstderr=%s", err, schedulesErr.String())
	}
	var scheduleRows []scheduleInfo
	if err := json.Unmarshal(schedulesOut.Bytes(), &scheduleRows); err != nil {
		t.Fatalf("decode team schedules: %v\nbody=%s", err, schedulesOut.String())
	}
	if len(scheduleRows) != 1 || scheduleRows[0].Name != "nightly" || scheduleRows[0].Every != "24h0m0s" {
		t.Fatalf("team schedule rows = %+v", scheduleRows)
	}

	schedulesText := NewRootCmd()
	schedulesTextOut, schedulesTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	schedulesText.SetOut(schedulesTextOut)
	schedulesText.SetErr(schedulesTextErr)
	schedulesText.SetArgs([]string{"team", "schedules", "delivery", "--repo", root})
	if err := schedulesText.Execute(); err != nil {
		t.Fatalf("team schedules text: %v\nstderr=%s", err, schedulesTextErr.String())
	}
	for _, want := range []string{"SCHEDULE", "nightly", "24h0m0s"} {
		if !strings.Contains(schedulesTextOut.String(), want) {
			t.Fatalf("team schedules text missing %q:\n%s", want, schedulesTextOut.String())
		}
	}

	schedulesFormat := NewRootCmd()
	schedulesFormatOut, schedulesFormatErr := &bytes.Buffer{}, &bytes.Buffer{}
	schedulesFormat.SetOut(schedulesFormatOut)
	schedulesFormat.SetErr(schedulesFormatErr)
	schedulesFormat.SetArgs([]string{"team", "schedules", "delivery", "--repo", root, "--format", "{{.Name}} {{.Every}}"})
	if err := schedulesFormat.Execute(); err != nil {
		t.Fatalf("team schedules format: %v\nstderr=%s", err, schedulesFormatErr.String())
	}
	if got := strings.TrimSpace(schedulesFormatOut.String()); got != "nightly 24h0m0s" {
		t.Fatalf("team schedules format = %q", got)
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

func TestTeamPsWatchRendersSnapshot(t *testing.T) {
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
	if err := runTeamPsWatch(ctx, &out, teamDir, "delivery", time.Millisecond, false, false); err != nil {
		t.Fatalf("team ps watch: %v", err)
	}
	if !strings.Contains(out.String(), "INSTANCE") || !strings.Contains(out.String(), "manager") {
		t.Fatalf("watch output missing instance rows:\n%s", out.String())
	}

	jsonCtx, jsonCancel := context.WithCancel(context.Background())
	jsonCancel()
	var jsonOut bytes.Buffer
	if err := runTeamPsWatch(jsonCtx, &jsonOut, teamDir, "delivery", time.Millisecond, true, false); err != nil {
		t.Fatalf("team ps watch json: %v", err)
	}
	var rows []psJSONRow
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut.Bytes()), &rows); err != nil {
		t.Fatalf("decode watch json: %v\nbody=%s", err, jsonOut.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" {
		t.Fatalf("watch json rows = %+v", rows)
	}
}

func TestTeamLifecycleDryRunScopesInstances(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.ticket-manager]
agent = "ticket-manager"

[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "ticket-manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "worker-squ-101", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "other", Agent: "other", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	up := NewRootCmd()
	upOut, upErr := &bytes.Buffer{}, &bytes.Buffer{}
	up.SetOut(upOut)
	up.SetErr(upErr)
	up.SetArgs([]string{"team", "up", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := up.Execute(); err != nil {
		t.Fatalf("team up dry-run: %v\nstderr=%s", err, upErr.String())
	}
	var upRows []lifecycleActionResult
	if err := json.Unmarshal(upOut.Bytes(), &upRows); err != nil {
		t.Fatalf("decode team up: %v\nbody=%s", err, upOut.String())
	}
	if got := lifecycleResultInstances(upRows); strings.Join(got, ",") != "manager,ticket-manager" {
		t.Fatalf("team up instances = %v", got)
	}

	down := NewRootCmd()
	downOut, downErr := &bytes.Buffer{}, &bytes.Buffer{}
	down.SetOut(downOut)
	down.SetErr(downErr)
	down.SetArgs([]string{"team", "down", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := down.Execute(); err != nil {
		t.Fatalf("team down dry-run: %v\nstderr=%s", err, downErr.String())
	}
	var downRows []instanceDownResult
	if err := json.Unmarshal(downOut.Bytes(), &downRows); err != nil {
		t.Fatalf("decode team down: %v\nbody=%s", err, downOut.String())
	}
	downNames := instanceDownResultNames(downRows)
	for _, want := range []string{"manager", "ticket-manager", "worker-squ-101"} {
		if !stringInSlice(want, downNames) {
			t.Fatalf("team down instances = %v, missing %s", downNames, want)
		}
	}
	for _, unwanted := range []string{"worker", "build-worker-1", "other"} {
		if stringInSlice(unwanted, downNames) {
			t.Fatalf("team down instances = %v, included %s", downNames, unwanted)
		}
	}

	restart := NewRootCmd()
	restartOut, restartErr := &bytes.Buffer{}, &bytes.Buffer{}
	restart.SetOut(restartOut)
	restart.SetErr(restartErr)
	restart.SetArgs([]string{"team", "restart", "delivery", "--repo", root, "--dry-run", "--json"})
	if err := restart.Execute(); err != nil {
		t.Fatalf("team restart dry-run: %v\nstderr=%s", err, restartErr.String())
	}
	var restartRows []lifecycleActionResult
	if err := json.Unmarshal(restartOut.Bytes(), &restartRows); err != nil {
		t.Fatalf("decode team restart: %v\nbody=%s", err, restartOut.String())
	}
	if got := lifecycleResultInstances(restartRows); strings.Join(got, ",") != "manager,ticket-manager" {
		t.Fatalf("team restart instances = %v", got)
	}
}

func TestTeamLifecycleOutputFlagConflicts(t *testing.T) {
	for _, args := range [][]string{
		{"team", "up", "delivery", "--quiet", "--json"},
		{"team", "up", "delivery", "--tail", "10", "--dry-run"},
		{"team", "down", "delivery", "--quiet", "--json"},
		{"team", "restart", "delivery", "--quiet", "--json"},
		{"team", "sync", "delivery", "--quiet", "--json"},
		{"team", "sync", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "sync", "delivery", "--dry-run", "--wait"},
		{"team", "plan", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "queue", "delivery", "--format", "{{.ID}}", "--json"},
		{"team", "logs", "delivery", "--json"},
		{"team", "events", "delivery", "--format", "{{.Instance}}", "--json"},
		{"team", "events", "delivery", "--summary", "--follow"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%v succeeded", args)
		}
		if strings.TrimSpace(stderr.String()) == "" {
			t.Fatalf("%v produced empty stderr", args)
		}
	}
}

func TestTeamEventsScopesLifecycleEvents(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	base := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: base, Action: "start", Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, Message: "manager up"},
		{TS: base.Add(time.Minute), Action: "stop", Instance: "build-worker-1", Agent: "worker", Status: daemon.StatusStopped, Message: "platform stop"},
		{TS: base.Add(2 * time.Minute), Action: "dispatch", Instance: "worker-squ-501", Agent: "worker", Status: daemon.StatusRunning, Message: "delivery worker"},
		{TS: base.Add(3 * time.Minute), Action: "stop", Instance: "other", Agent: "other", Status: daemon.StatusStopped, Message: "other stop"},
		{TS: base.Add(4 * time.Minute), Action: "stop", Instance: "worker-squ-501", Agent: "worker", Status: daemon.StatusStopped, Message: "delivery done"},
	} {
		if err := daemon.AppendLifecycleEvent(daemonRoot, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("team events json: %v\nstderr=%s", err, listErr.String())
	}
	events := decodeLifecycleEventJSONL(t, listOut.String())
	if got := lifecycleEventInstances(events); strings.Join(got, ",") != "manager,worker-squ-501,worker-squ-501" {
		t.Fatalf("team events instances = %v\nbody=%s", got, listOut.String())
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--tail", "1", "--format", "{{.Instance}} {{.Action}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team events format: %v\nstderr=%s", err, formatErr.String())
	}
	if got := strings.TrimSpace(formatOut.String()); got != "worker-squ-501 stop" {
		t.Fatalf("team events tail format = %q", got)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"team", "events", "delivery", "--repo", root, "--summary", "--action", "stop", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("team events summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var eventSummary eventSummaryJSON
	if err := json.Unmarshal(summaryOut.Bytes(), &eventSummary); err != nil {
		t.Fatalf("decode team events summary: %v\nbody=%s", err, summaryOut.String())
	}
	if eventSummary.Total != 1 || eventSummary.Actions["stop"] != 1 || eventSummary.Instances["worker-squ-501"] != 1 {
		t.Fatalf("team events summary = %+v", eventSummary)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "events", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team events text: %v\nstderr=%s", err, textErr.String())
	}
	if strings.Contains(textOut.String(), "build-worker-1") || strings.Contains(textOut.String(), "other stop") {
		t.Fatalf("team events text leaked unrelated event:\n%s", textOut.String())
	}
}

func TestTeamLogsScopesRowsAndStreams(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "worker-squ-201", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Hour)},
		{Instance: "other", Agent: "other", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now.Add(-time.Hour)},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeChildLogForTest(t, daemonRoot, "manager", "manager first\nmanager second\n")
	writeChildLogForTest(t, daemonRoot, "worker-squ-201", "worker first\nworker latest\n")
	writeChildLogForTest(t, daemonRoot, "build-worker-1", "build worker log\n")
	writeChildLogForTest(t, daemonRoot, "other", "other log\n")

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--list", "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("team logs list: %v\nstderr=%s", err, listErr.String())
	}
	var rows []logListRow
	if err := json.Unmarshal(listOut.Bytes(), &rows); err != nil {
		t.Fatalf("decode team logs list: %v\nbody=%s", err, listOut.String())
	}
	if got := logRowInstances(rows); strings.Join(got, ",") != "manager,worker-squ-201" {
		t.Fatalf("team log rows = %v", got)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--list", "--format", "{{.Instance}} {{.Size}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team logs format: %v\nstderr=%s", err, formatErr.String())
	}
	formatBody := formatOut.String()
	for _, want := range []string{"manager ", "worker-squ-201 "} {
		if !strings.Contains(formatBody, want) {
			t.Fatalf("team logs format missing %q:\n%s", want, formatBody)
		}
	}
	if strings.Contains(formatBody, "build-worker") || strings.Contains(formatBody, "other") {
		t.Fatalf("team logs format leaked unrelated rows:\n%s", formatBody)
	}

	logs := NewRootCmd()
	logsOut, logsErr := &bytes.Buffer{}, &bytes.Buffer{}
	logs.SetOut(logsOut)
	logs.SetErr(logsErr)
	logs.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--tail", "1"})
	if err := logs.Execute(); err != nil {
		t.Fatalf("team logs: %v\nstderr=%s", err, logsErr.String())
	}
	body := logsOut.String()
	for _, want := range []string{"manager              | manager second", "worker-squ-201       | worker latest"} {
		if !strings.Contains(body, want) {
			t.Fatalf("team logs missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "build worker") || strings.Contains(body, "other log") {
		t.Fatalf("team logs leaked unrelated content:\n%s", body)
	}

	latest := NewRootCmd()
	latestOut, latestErr := &bytes.Buffer{}, &bytes.Buffer{}
	latest.SetOut(latestOut)
	latest.SetErr(latestErr)
	latest.SetArgs([]string{"team", "logs", "delivery", "--repo", root, "--latest", "--tail", "1"})
	if err := latest.Execute(); err != nil {
		t.Fatalf("team logs latest: %v\nstderr=%s", err, latestErr.String())
	}
	if got := latestOut.String(); got != "worker latest\n" {
		t.Fatalf("team logs latest = %q", got)
	}
}

func TestTeamQueueScopesItems(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[teams.delivery]
instances = ["manager", "worker"]

[teams.platform]
instances = ["other"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	teamJob := &job.Job{
		ID:        "squ-501",
		Ticket:    "SQU-501",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, teamJob); err != nil {
		t.Fatalf("write team job: %v", err)
	}
	otherJob := &job.Job{
		ID:        "oth-1",
		Ticket:    "OTH-1",
		Target:    "other",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, otherJob); err != nil {
		t.Fatalf("write other job: %v", err)
	}
	for _, item := range []*daemon.QueueItem{
		{
			ID:         "q-team-job",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-squ-501",
			Payload:    map[string]any{"job_id": "squ-501", "target": "worker"},
			Attempts:   daemon.MaxQueueAttempts,
			LastError:  "spawn failed",
			QueuedAt:   now.Add(-time.Hour),
			UpdatedAt:  now,
		},
		{
			ID:         "q-team-target",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: "worker-direct",
			Payload:    map[string]any{"target": "worker"},
			QueuedAt:   now,
			UpdatedAt:  now,
		},
		{
			ID:         "q-other-job",
			State:      daemon.QueueStateDead,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-oth-1",
			Payload:    map[string]any{"job_id": "oth-1", "target": "other"},
			QueuedAt:   now,
			UpdatedAt:  now,
		},
		{
			ID:         "q-other-target",
			State:      daemon.QueueStatePending,
			EventType:  "agent.dispatch",
			Instance:   "other",
			InstanceID: "other-direct",
			Payload:    map[string]any{"target": "other"},
			QueuedAt:   now,
			UpdatedAt:  now,
		},
	} {
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
			t.Fatalf("write queue item %s: %v", item.ID, err)
		}
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("team queue: %v\nstderr=%s", err, listErr.String())
	}
	var items []daemon.QueueItem
	if err := json.Unmarshal(listOut.Bytes(), &items); err != nil {
		t.Fatalf("decode team queue: %v\nbody=%s", err, listOut.String())
	}
	if got := queueItemIDs(items); strings.Join(got, ",") != "q-team-job,q-team-target" {
		t.Fatalf("team queue ids = %v", got)
	}

	summary := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summary.SetOut(summaryOut)
	summary.SetErr(summaryErr)
	summary.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--state", "dead", "--summary", "--json"})
	if err := summary.Execute(); err != nil {
		t.Fatalf("team queue summary: %v\nstderr=%s", err, summaryErr.String())
	}
	var queueSummary queueSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &queueSummary); err != nil {
		t.Fatalf("decode queue summary: %v\nbody=%s", err, summaryOut.String())
	}
	if queueSummary.Total != 1 || queueSummary.Dead != 1 || queueSummary.Instances["worker"] != 1 {
		t.Fatalf("queue summary = %+v", queueSummary)
	}

	jobFiltered := NewRootCmd()
	jobOut, jobErr := &bytes.Buffer{}, &bytes.Buffer{}
	jobFiltered.SetOut(jobOut)
	jobFiltered.SetErr(jobErr)
	jobFiltered.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--job", "SQU-501", "--json"})
	if err := jobFiltered.Execute(); err != nil {
		t.Fatalf("team queue job filter: %v\nstderr=%s", err, jobErr.String())
	}
	var jobItems []daemon.QueueItem
	if err := json.Unmarshal(jobOut.Bytes(), &jobItems); err != nil {
		t.Fatalf("decode team queue job filter: %v\nbody=%s", err, jobOut.String())
	}
	if got := queueItemIDs(jobItems); strings.Join(got, ",") != "q-team-job" {
		t.Fatalf("team queue job-filtered ids = %v", got)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "queue", "delivery", "--repo", root, "--format", "{{.ID}} {{.State}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team queue format: %v\nstderr=%s", err, formatErr.String())
	}
	formatBody := formatOut.String()
	for _, want := range []string{"q-team-job dead", "q-team-target pending"} {
		if !strings.Contains(formatBody, want) {
			t.Fatalf("team queue format missing %q:\n%s", want, formatBody)
		}
	}
	if strings.Contains(formatBody, "q-other") {
		t.Fatalf("team queue format leaked unrelated item:\n%s", formatBody)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "queue", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team queue text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "q-team-job") || strings.Contains(textOut.String(), "q-other") {
		t.Fatalf("team queue text =\n%s", textOut.String())
	}

	retryDry := NewRootCmd()
	retryDryOut, retryDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryDry.SetOut(retryDryOut)
	retryDry.SetErr(retryDryErr)
	retryDry.SetArgs([]string{"team", "queue", "retry", "delivery", "--repo", root, "--all", "--job", "SQU-501", "--dry-run", "--json"})
	if err := retryDry.Execute(); err != nil {
		t.Fatalf("team queue retry --all dry-run: %v\nstderr=%s", err, retryDryErr.String())
	}
	var retryDryResults []queueRetryResult
	if err := json.Unmarshal(retryDryOut.Bytes(), &retryDryResults); err != nil {
		t.Fatalf("decode team queue retry dry-run: %v\nbody=%s", err, retryDryOut.String())
	}
	if len(retryDryResults) != 1 || retryDryResults[0].ID != "q-team-job" || retryDryResults[0].Action != "would_retry" || !retryDryResults[0].DryRun {
		t.Fatalf("retry dry-run results = %+v", retryDryResults)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-job"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("retry dry-run changed item=%+v err=%v", item, err)
	}

	otherRetry := NewRootCmd()
	otherRetryOut, otherRetryErr := &bytes.Buffer{}, &bytes.Buffer{}
	otherRetry.SetOut(otherRetryOut)
	otherRetry.SetErr(otherRetryErr)
	otherRetry.SetArgs([]string{"team", "queue", "retry", "delivery", "--repo", root, "q-other-job", "--dry-run", "--json"})
	if err := otherRetry.Execute(); err == nil {
		t.Fatal("team queue retry unrelated item unexpectedly succeeded")
	}
	if !strings.Contains(otherRetryErr.String(), "not owned by team") {
		t.Fatalf("team queue retry unrelated stderr = %q stdout=%q", otherRetryErr.String(), otherRetryOut.String())
	}

	retryApply := NewRootCmd()
	retryApplyOut, retryApplyErr := &bytes.Buffer{}, &bytes.Buffer{}
	retryApply.SetOut(retryApplyOut)
	retryApply.SetErr(retryApplyErr)
	retryApply.SetArgs([]string{"team", "queue", "retry", "delivery", "--repo", root, "q-team-job", "--json"})
	if err := retryApply.Execute(); err != nil {
		t.Fatalf("team queue retry single: %v\nstderr=%s", err, retryApplyErr.String())
	}
	var retried daemon.QueueItem
	if err := json.Unmarshal(retryApplyOut.Bytes(), &retried); err != nil {
		t.Fatalf("decode team queue retry single: %v\nbody=%s", err, retryApplyOut.String())
	}
	if retried.ID != "q-team-job" || retried.State != daemon.QueueStatePending || retried.LastError != "" {
		t.Fatalf("retried item = %+v", retried)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-job"); err != nil || item.State != daemon.QueueStateDead {
		t.Fatalf("unrelated retry item changed=%+v err=%v", item, err)
	}

	dropReady := NewRootCmd()
	dropReadyOut, dropReadyErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropReady.SetOut(dropReadyOut)
	dropReady.SetErr(dropReadyErr)
	dropReady.SetArgs([]string{"team", "queue", "drop", "delivery", "--repo", root, "--all", "--ready", "--dry-run", "--json"})
	if err := dropReady.Execute(); err != nil {
		t.Fatalf("team queue drop --all ready dry-run: %v\nstderr=%s", err, dropReadyErr.String())
	}
	var dropReadyResults []queueDropResult
	if err := json.Unmarshal(dropReadyOut.Bytes(), &dropReadyResults); err != nil {
		t.Fatalf("decode team queue drop ready dry-run: %v\nbody=%s", err, dropReadyOut.String())
	}
	dropReadyIDs := map[string]bool{}
	for _, result := range dropReadyResults {
		dropReadyIDs[result.ID] = true
		if result.Action != "would_drop" || !result.DryRun {
			t.Fatalf("drop ready result = %+v, want dry-run would_drop", result)
		}
	}
	if !dropReadyIDs["q-team-job"] || !dropReadyIDs["q-team-target"] || dropReadyIDs["q-other-target"] {
		t.Fatalf("drop ready results = %+v", dropReadyResults)
	}

	dropApply := NewRootCmd()
	dropApplyOut, dropApplyErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropApply.SetOut(dropApplyOut)
	dropApply.SetErr(dropApplyErr)
	dropApply.SetArgs([]string{"team", "queue", "drop", "delivery", "--repo", root, "q-team-target", "--json"})
	if err := dropApply.Execute(); err != nil {
		t.Fatalf("team queue drop single: %v\nstderr=%s", err, dropApplyErr.String())
	}
	var dropped map[string]any
	if err := json.Unmarshal(dropApplyOut.Bytes(), &dropped); err != nil {
		t.Fatalf("decode team queue drop single: %v\nbody=%s", err, dropApplyOut.String())
	}
	if dropped["dropped"] != true || dropped["id"] != "q-team-target" || dropped["team"] != "delivery" {
		t.Fatalf("dropped result = %+v", dropped)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-team-target"); !os.IsNotExist(err) {
		t.Fatalf("team queue target still exists or unexpected err=%v", err)
	}
	if item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-other-target"); err != nil || item.State != daemon.QueueStatePending {
		t.Fatalf("unrelated drop item changed=%+v err=%v", item, err)
	}
}

func setupTeamScopedPlanFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.ticket-manager]
agent = "ticket-manager"

[instances.build-worker]
agent = "worker"
ephemeral = true

[instances.other]
agent = "other"

[teams.delivery]
description = "Delivery team"
instances = ["manager", "ticket-manager", "worker"]

[teams.platform]
instances = ["other", "build-worker"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "worker-squ-101", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "build-worker-1", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "adhoc-worker", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
		{Instance: "other", Agent: "other", Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: root, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	return root
}

func TestTeamPlanScopesRowsAndStopExtras(t *testing.T) {
	root := setupTeamScopedPlanFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--stop-extras", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team plan: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot teamPlanSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team plan: %v\nbody=%s", err, out.String())
	}
	if snapshot.Team.Name != "delivery" || snapshot.Plan == nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	rows := planRowsByInstance(snapshot.Plan.Instances)
	for _, want := range []string{"manager", "ticket-manager", "worker", "worker-squ-101", "adhoc-worker"} {
		if _, ok := rows[want]; !ok {
			t.Fatalf("team plan rows = %+v, missing %s", snapshot.Plan.Instances, want)
		}
	}
	for _, unwanted := range []string{"build-worker", "build-worker-1", "other"} {
		if _, ok := rows[unwanted]; ok {
			t.Fatalf("team plan rows = %+v, included %s", snapshot.Plan.Instances, unwanted)
		}
	}
	if rows["adhoc-worker"].Action != "stop" || rows["adhoc-worker"].Kind != "extra" {
		t.Fatalf("adhoc-worker row = %+v, want stop extra", rows["adhoc-worker"])
	}

	noExtras := NewRootCmd()
	noExtrasOut, noExtrasErr := &bytes.Buffer{}, &bytes.Buffer{}
	noExtras.SetOut(noExtrasOut)
	noExtras.SetErr(noExtrasErr)
	noExtras.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--json"})
	if err := noExtras.Execute(); err != nil {
		t.Fatalf("team plan without extras: %v\nstderr=%s", err, noExtrasErr.String())
	}
	var noExtrasSnapshot teamPlanSnapshot
	if err := json.Unmarshal(noExtrasOut.Bytes(), &noExtrasSnapshot); err != nil {
		t.Fatalf("decode team plan without extras: %v\nbody=%s", err, noExtrasOut.String())
	}
	if _, ok := planRowsByInstance(noExtrasSnapshot.Plan.Instances)["adhoc-worker"]; ok {
		t.Fatalf("team plan without --stop-extras included adhoc-worker: %+v", noExtrasSnapshot.Plan.Instances)
	}

	startOnly := NewRootCmd()
	startOut, startErr := &bytes.Buffer{}, &bytes.Buffer{}
	startOnly.SetOut(startOut)
	startOnly.SetErr(startErr)
	startOnly.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--action", "start", "--json"})
	if err := startOnly.Execute(); err != nil {
		t.Fatalf("team plan action start: %v\nstderr=%s", err, startErr.String())
	}
	var startSnapshot teamPlanSnapshot
	if err := json.Unmarshal(startOut.Bytes(), &startSnapshot); err != nil {
		t.Fatalf("decode team plan action start: %v\nbody=%s", err, startOut.String())
	}
	if startSnapshot.Plan.Summary.Total != 1 || startSnapshot.Plan.Summary.Start != 1 || startSnapshot.Plan.Instances[0].Instance != "ticket-manager" {
		t.Fatalf("start-filtered plan = %+v", startSnapshot.Plan)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "plan", "delivery", "--repo", root, "--format", "{{.Instance}} {{.Action}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team plan format: %v\nstderr=%s", err, formatErr.String())
	}
	formatBody := formatOut.String()
	for _, want := range []string{"manager keep", "ticket-manager start", "worker on-demand", "worker-squ-101 keep"} {
		if !strings.Contains(formatBody, want) {
			t.Fatalf("formatted team plan missing %q:\n%s", want, formatBody)
		}
	}
	if strings.Contains(formatBody, "adhoc-worker") || strings.Contains(formatBody, "build-worker") {
		t.Fatalf("formatted team plan included unrelated/extra rows:\n%s", formatBody)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "plan", "delivery", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("team plan text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Team: delivery", "daemon:", "INSTANCE", "summary:"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team plan text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestTeamSyncDryRunScopesRowsAndFilters(t *testing.T) {
	root := setupTeamScopedPlanFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "sync", "delivery", "--repo", root, "--dry-run", "--stop-extras", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team sync dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var snapshot teamPlanSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team sync dry-run: %v\nbody=%s", err, out.String())
	}
	if snapshot.Team.Name != "delivery" || snapshot.Plan == nil {
		t.Fatalf("sync snapshot = %+v", snapshot)
	}
	rows := planRowsByInstance(snapshot.Plan.Instances)
	for _, want := range []string{"manager", "ticket-manager", "worker", "worker-squ-101", "adhoc-worker"} {
		if _, ok := rows[want]; !ok {
			t.Fatalf("team sync rows = %+v, missing %s", snapshot.Plan.Instances, want)
		}
	}
	for _, unwanted := range []string{"build-worker", "build-worker-1", "other"} {
		if _, ok := rows[unwanted]; ok {
			t.Fatalf("team sync rows = %+v, included %s", snapshot.Plan.Instances, unwanted)
		}
	}
	if rows["adhoc-worker"].Action != "stop" || rows["adhoc-worker"].Kind != "extra" {
		t.Fatalf("adhoc-worker row = %+v, want stop extra", rows["adhoc-worker"])
	}

	startOnly := NewRootCmd()
	startOut, startErr := &bytes.Buffer{}, &bytes.Buffer{}
	startOnly.SetOut(startOut)
	startOnly.SetErr(startErr)
	startOnly.SetArgs([]string{"team", "sync", "delivery", "--repo", root, "--dry-run", "--action", "start", "--json"})
	if err := startOnly.Execute(); err != nil {
		t.Fatalf("team sync action start: %v\nstderr=%s", err, startErr.String())
	}
	var startSnapshot teamPlanSnapshot
	if err := json.Unmarshal(startOut.Bytes(), &startSnapshot); err != nil {
		t.Fatalf("decode team sync action start: %v\nbody=%s", err, startOut.String())
	}
	if startSnapshot.Plan.Summary.Total != 1 || startSnapshot.Plan.Summary.Start != 1 || startSnapshot.Plan.Instances[0].Instance != "ticket-manager" {
		t.Fatalf("start-filtered sync = %+v", startSnapshot.Plan)
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"team", "sync", "delivery", "--repo", root, "--dry-run", "--format", "{{.Instance}} {{.Action}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("team sync format: %v\nstderr=%s", err, formatErr.String())
	}
	formatBody := formatOut.String()
	for _, want := range []string{"manager keep", "ticket-manager start", "worker on-demand", "worker-squ-101 keep"} {
		if !strings.Contains(formatBody, want) {
			t.Fatalf("formatted team sync missing %q:\n%s", want, formatBody)
		}
	}
	if strings.Contains(formatBody, "adhoc-worker") || strings.Contains(formatBody, "build-worker") {
		t.Fatalf("formatted team sync included unrelated/extra rows:\n%s", formatBody)
	}
}

func lifecycleResultInstances(rows []lifecycleActionResult) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func instanceDownResultNames(rows []instanceDownResult) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func planRowsByInstance(rows []planRow) map[string]planRow {
	out := map[string]planRow{}
	for _, row := range rows {
		out[row.Instance] = row
	}
	return out
}

func queueItemIDs(items []daemon.QueueItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

func logRowInstances(rows []logListRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func decodeLifecycleEventJSONL(t *testing.T, body string) []daemon.LifecycleEvent {
	t.Helper()
	var events []daemon.LifecycleEvent
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev daemon.LifecycleEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode lifecycle event %q: %v\nbody=%s", line, err, body)
		}
		events = append(events, ev)
	}
	return events
}

func lifecycleEventInstances(events []daemon.LifecycleEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Instance)
	}
	return out
}

func stringInSlice(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func TestTeamHealthJobsAreTeamScoped(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[instances.other]
agent = "other"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	teamJob := &job.Job{
		ID:         "squ-901",
		Ticket:     "SQU-901",
		Target:     "worker",
		Pipeline:   "ticket_to_pr",
		Status:     job.StatusFailed,
		LastStatus: "tests failed",
		CreatedAt:  now,
		UpdatedAt:  now,
		Steps: []job.Step{
			{ID: "implement", Target: "worker", Status: job.StatusFailed},
		},
	}
	if err := job.Write(teamDir, teamJob); err != nil {
		t.Fatalf("write team job: %v", err)
	}
	unrelated := &job.Job{
		ID:         "oth-1",
		Ticket:     "OTH-1",
		Target:     "other",
		Status:     job.StatusFailed,
		LastStatus: "unrelated failed",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := job.Write(teamDir, unrelated); err != nil {
		t.Fatalf("write unrelated job: %v", err)
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-team-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-901",
		Payload:        map[string]any{"job_id": "squ-901", "target": "worker", "ticket": "SQU-901"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("write team queue item: %v", err)
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-other-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "other",
		InstanceID:     "other-oth-1",
		Payload:        map[string]any{"job_id": "oth-1", "target": "other", "ticket": "OTH-1"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}); err != nil {
		t.Fatalf("write unrelated queue item: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "health", "delivery", "--repo", root, "--jobs", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("team health unexpectedly succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("err = %v, want exit 1\nstderr=%s", err, stderr.String())
	}
	var snapshot teamHealthSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode team health: %v\nbody=%s", err, out.String())
	}
	if snapshot.Team.Name != "delivery" || snapshot.Health == nil || snapshot.Health.Healthy {
		t.Fatalf("team health snapshot = %+v", snapshot)
	}
	if snapshot.Health.Jobs == nil || snapshot.Health.Jobs.Summary.Total != 1 || snapshot.Health.Jobs.Summary.Failed != 1 {
		t.Fatalf("team job summary = %+v", snapshot.Health.Jobs)
	}
	if snapshot.Health.Queue.Dead != 1 {
		t.Fatalf("team queue summary = %+v", snapshot.Health.Queue)
	}
	if len(snapshot.Health.PipelineStatus) != 1 || snapshot.Health.PipelineStatus[0].Pipeline != "ticket_to_pr" || snapshot.Health.PipelineStatus[0].FailedSteps != 1 {
		t.Fatalf("pipeline status = %+v", snapshot.Health.PipelineStatus)
	}
	for _, issue := range snapshot.Health.Issues {
		if issue.Job == "oth-1" || strings.Contains(issue.Message, "OTH-1") {
			t.Fatalf("unrelated issue leaked into team health: %+v", snapshot.Health.Issues)
		}
	}
	codes := map[string]bool{}
	var sawTeamJob bool
	var sawScopedQueueAction bool
	for _, issue := range snapshot.Health.Issues {
		codes[issue.Code] = true
		if issue.Code == "job_attention" && issue.Job == "squ-901" {
			sawTeamJob = true
		}
		if issue.Code == "queue_dead_letter" && containsString(issue.Actions, "agent-team team queue retry delivery --all --job squ-901") {
			sawScopedQueueAction = true
		}
	}
	for _, want := range []string{"daemon_not_running", "queue_dead_letter", "job_attention", "pipeline_failed_step"} {
		if !codes[want] {
			t.Fatalf("issues = %+v, missing %s", snapshot.Health.Issues, want)
		}
	}
	if !sawTeamJob {
		t.Fatalf("issues = %+v, missing team job_attention", snapshot.Health.Issues)
	}
	if !sawScopedQueueAction {
		t.Fatalf("issues = %+v, missing scoped team queue retry action", snapshot.Health.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"team", "health", "delivery", "--repo", root, "--jobs"})
	if err := text.Execute(); err == nil {
		t.Fatal("team health text unexpectedly succeeded")
	}
	for _, want := range []string{"Team: delivery", "health: unhealthy", "jobs: total=1", "pipeline_failed_step", "queue_dead_letter"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("team health text missing %q:\n%s", want, textOut.String())
		}
	}
	if strings.Contains(textOut.String(), "oth-1") || strings.Contains(textOut.String(), "OTH-1") {
		t.Fatalf("team health text included unrelated job:\n%s", textOut.String())
	}
}

func TestTeamHealthQuietAndJSONConflict(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "health", "delivery", "--quiet", "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("team health --quiet --json succeeded")
	}
	if !strings.Contains(stderr.String(), "choose one of --quiet or --json") {
		t.Fatalf("stderr = %q", stderr.String())
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

func TestTeamPsRejectsNegativeInterval(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "ps", "delivery", "--watch", "--interval", "-1s"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("team ps negative interval succeeded")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
