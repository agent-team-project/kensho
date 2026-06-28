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

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
)

func TestTickRunsMaintenanceCycle(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	queued := &daemon.QueueItem{
		ID:         "q-tick",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-91",
		Payload:    map[string]any{"target": "worker", "name": "worker-squ-91", "ticket": "SQU-91"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), queued); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	j := &job.Job{
		ID:        "squ-93",
		Ticket:    "SQU-93",
		Target:    "worker",
		Kickoff:   "SQU-93: implement",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
			{ID: "implement", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write ready job: %v", err)
	}
	statusJob := &job.Job{
		ID:        "squ-94",
		Ticket:    "SQU-94",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, statusJob); err != nil {
		t.Fatalf("write status job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-94"), `[status]
phase = "blocked"
description = "waiting on failing test details"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-94"
ticket = "SQU-94"
branch = "worker-squ-94"
`, now)

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--dry-run", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("tick dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview tickResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode tick dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if !preview.DryRun || preview.Reconcile != nil || preview.Queue == nil || !preview.Queue.DryRun || preview.Queue.WouldDispatch != 1 {
		t.Fatalf("tick preview = %+v", preview)
	}
	if len(preview.JobStatus) != 1 || preview.JobStatus[0].JobID != "squ-94" || preview.JobStatus[0].After != job.StatusBlocked || !preview.JobStatus[0].Changed || !preview.JobStatus[0].DryRun {
		t.Fatalf("tick preview job status = %+v", preview.JobStatus)
	}
	if len(preview.Advance) != 1 || preview.Advance[0].Action != "would_advance" || !preview.Advance[0].DryRun {
		t.Fatalf("tick preview advance = %+v", preview.Advance)
	}
	if preview.Advance[0].Preview != nil {
		t.Fatalf("plain tick dry-run unexpectedly included route preview = %+v", preview.Advance[0].Preview)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-tick"); err != nil {
		t.Fatalf("tick dry-run removed queue item: %v", err)
	}
	unchanged, err := job.Read(teamDir, "squ-93")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Steps[1].Status != job.StatusBlocked {
		t.Fatalf("tick dry-run mutated job = %+v", unchanged)
	}
	statusUnchanged, err := job.Read(teamDir, "squ-94")
	if err != nil {
		t.Fatalf("read dry-run status job: %v", err)
	}
	if statusUnchanged.Status != job.StatusQueued {
		t.Fatalf("tick dry-run mutated status job = %+v", statusUnchanged)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--limit", "2", "--dry-run", "--preview-routes", "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("tick dry-run commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "tick", "--target", target, "--workspace", "repo", "--runtime", "codex", "--runtime-bin", "codex-dev", "--limit", "2"}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("tick dry-run commands = %q, want %q", got, wantCommand)
	}

	idleCommands := NewRootCmd()
	idleCommandsOut, idleCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	idleCommands.SetOut(idleCommandsOut)
	idleCommands.SetErr(idleCommandsErr)
	idleCommands.SetArgs([]string{"tick", "--target", target, "--dry-run", "--skip-reconcile", "--skip-schedules", "--skip-drain", "--skip-advance", "--commands"})
	if err := idleCommands.Execute(); err != nil {
		t.Fatalf("idle tick dry-run commands: %v\nstderr=%s", err, idleCommandsErr.String())
	}
	if got := strings.TrimSpace(idleCommandsOut.String()); got != "" {
		t.Fatalf("idle tick dry-run commands = %q, want no output", got)
	}

	routeDry := NewRootCmd()
	routeDryOut, routeDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	routeDry.SetOut(routeDryOut)
	routeDry.SetErr(routeDryErr)
	routeDry.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--dry-run", "--preview-routes", "--runtime", "codex", "--runtime-bin", "codex-dev", "--json"})
	if err := routeDry.Execute(); err != nil {
		t.Fatalf("tick dry-run preview-routes: %v\nstderr=%s", err, routeDryErr.String())
	}
	var routePreview tickResult
	if err := json.Unmarshal(routeDryOut.Bytes(), &routePreview); err != nil {
		t.Fatalf("decode tick dry-run preview-routes json: %v\nbody=%s", err, routeDryOut.String())
	}
	if len(routePreview.Advance) != 1 || routePreview.Advance[0].Preview == nil || routePreview.Advance[0].Preview.Step == nil || routePreview.Advance[0].Preview.Step.ID != "implement" {
		t.Fatalf("tick route preview advance = %+v", routePreview.Advance)
	}
	if routePreview.Advance[0].Preview.Dispatch == nil || routePreview.Advance[0].Preview.Dispatch.RequestedName != "worker-squ-93-implement" {
		t.Fatalf("tick route dispatch preview = %+v", routePreview.Advance[0].Preview.Dispatch)
	}
	dispatchPreview := routePreview.Advance[0].Preview.Dispatch.Preview
	if dispatchPreview == nil || dispatchPreview.Type != "agent.dispatch" || len(dispatchPreview.Matched) != 1 || dispatchPreview.Matched[0] != "worker" {
		t.Fatalf("tick dispatch route preview = %+v", dispatchPreview)
	}
	payload := dispatchPreview.Payload
	if payload["job_id"] != "squ-93" || payload["pipeline"] != "ticket_to_pr" || payload["pipeline_step"] != "implement" || payload["workspace"] != "repo" || payload["runtime"] != "codex" || payload["runtime_binary"] != "codex-dev" {
		t.Fatalf("tick route preview payload = %+v", payload)
	}

	textDry := NewRootCmd()
	textDryOut, textDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	textDry.SetOut(textDryOut)
	textDry.SetErr(textDryErr)
	textDry.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--dry-run", "--preview-routes", "--skip-reconcile", "--skip-schedules", "--skip-drain"})
	if err := textDry.Execute(); err != nil {
		t.Fatalf("tick dry-run preview-routes text: %v\nstderr=%s", err, textDryErr.String())
	}
	for _, want := range []string{"Pipeline advance:", "Routes:", "squ-93 step=implement target=worker instance=worker-squ-93-implement", "Matched: worker"} {
		if !strings.Contains(textDryOut.String(), want) {
			t.Fatalf("tick route preview text missing %q:\n%s", want, textDryOut.String())
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick json: %v\nbody=%s", err, out.String())
	}
	if result.Reconcile == nil || result.Queue == nil {
		t.Fatalf("tick missing daemon results = %+v", result)
	}
	if len(result.JobStatus) != 1 || result.JobStatus[0].JobID != "squ-94" || result.JobStatus[0].After != job.StatusBlocked || !result.JobStatus[0].Changed {
		t.Fatalf("tick job status result = %+v", result.JobStatus)
	}
	if result.Queue.Attempted != 1 || result.Queue.Dispatched != 1 || result.Queue.Pending != 0 {
		t.Fatalf("queue result = %+v", result.Queue)
	}
	if len(result.Advance) != 1 || result.Advance[0].JobID != "squ-93" || result.Advance[0].Action != "advanced" || result.Advance[0].StepStatus != job.StatusRunning {
		t.Fatalf("advance result = %+v", result.Advance)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-tick"); !os.IsNotExist(err) {
		t.Fatalf("queue item should be removed after tick, err=%v", err)
	}
	updated, err := job.Read(teamDir, "squ-93")
	if err != nil {
		t.Fatalf("read advanced job: %v", err)
	}
	if updated.Steps[1].Status != job.StatusRunning || updated.Steps[1].Instance == "" {
		t.Fatalf("advanced job = %+v", updated)
	}
	statusUpdated, err := job.Read(teamDir, "squ-94")
	if err != nil {
		t.Fatalf("read updated status job: %v", err)
	}
	if statusUpdated.Status != job.StatusBlocked || statusUpdated.Instance != "worker-squ-94" || statusUpdated.LastEvent != "status_reconcile" {
		t.Fatalf("status reconciled job = %+v", statusUpdated)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-91")
	stopAndWaitForTest(t, mgr, updated.Steps[1].Instance)
}

func TestTickDrainsAgentOutbox(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Date(2026, 6, 27, 11, 0, 0, 0, time.UTC)
	if err := daemon.WriteOutboxItem(teamDir, &daemon.OutboxItem{
		ID:        "outbox-tick",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Payload:   map[string]any{"target": "worker", "name": "worker-squ-95", "ticket": "SQU-95", "workspace": "repo"},
		Source:    "manager",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("WriteOutboxItem: %v", err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--dry-run", "--skip-reconcile", "--skip-schedules", "--skip-advance", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("tick dry-run: %v\nstderr=%s", err, dryErr.String())
	}
	var preview tickResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode tick dry-run json: %v\nbody=%s", err, dryOut.String())
	}
	if preview.Outbox == nil || !preview.Outbox.DryRun || preview.Outbox.WouldPublish != 1 || preview.Outbox.Pending != 1 {
		t.Fatalf("tick outbox preview = %+v", preview.Outbox)
	}
	if _, err := daemon.ReadOutboxItem(teamDir, "outbox-tick"); err != nil {
		t.Fatalf("tick dry-run removed outbox item: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"tick", "--target", target, "--workspace", "repo", "--skip-reconcile", "--skip-schedules", "--skip-advance", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick json: %v\nbody=%s", err, out.String())
	}
	if result.Outbox == nil || result.Outbox.Published != 1 || result.Outbox.Pending != 0 || result.Outbox.Processed != 1 {
		t.Fatalf("tick outbox result = %+v", result.Outbox)
	}
	processed, err := daemon.ReadOutboxItem(teamDir, "outbox-tick")
	if err != nil {
		t.Fatalf("ReadOutboxItem: %v", err)
	}
	if processed.State != daemon.OutboxStateProcessed {
		t.Fatalf("outbox state = %s, want processed", processed.State)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-95")
}

func TestTickWaitsForAdvancedJobs(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-421",
		Ticket:    "SQU-421",
		Target:    "worker",
		Kickoff:   "SQU-421: implement",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
			{ID: "implement", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write ready job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"tick",
		"--target", target,
		"--workspace", "repo",
		"--skip-reconcile",
		"--skip-schedules",
		"--skip-drain",
		"--wait",
		"--wait-status", "running",
		"--wait-next-state", "running",
		"--wait-step", "implement",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick --wait: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick wait json: %v\nbody=%s", err, out.String())
	}
	if len(result.Advance) != 1 || result.Advance[0].Action != "advanced" || result.Advance[0].Job == nil || result.Advance[0].Job.Status != job.StatusRunning || result.Advance[0].Job.LastEvent != "advance_dispatched" {
		t.Fatalf("tick wait advance = %+v", result.Advance)
	}
	if result.Advance[0].Step == nil || result.Advance[0].Step.ID != "implement" || result.Advance[0].Step.Status != job.StatusRunning || result.Advance[0].Step.Instance != "worker-squ-421-implement" {
		t.Fatalf("tick wait step = %+v", result.Advance[0].Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-421-implement")
}

func TestTickWaitValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "dry run", args: []string{"tick", "--wait", "--dry-run"}, want: "--wait cannot be combined with --dry-run"},
		{name: "watch", args: []string{"tick", "--wait", "--watch"}, want: "--wait cannot be combined with --watch"},
		{name: "until idle", args: []string{"tick", "--wait", "--until-idle"}, want: "--wait cannot be combined with --until-idle"},
		{name: "skip advance", args: []string{"tick", "--wait", "--skip-advance"}, want: "--wait requires pipeline advancement"},
		{name: "wait flag without wait", args: []string{"tick", "--wait-status", "running"}, want: "wait-related flags require --wait"},
		{name: "wait next-state without wait", args: []string{"tick", "--wait-next-state", "running"}, want: "wait-related flags require --wait"},
		{name: "wait step without wait", args: []string{"tick", "--wait-step", "implement"}, want: "wait-related flags require --wait"},
		{name: "invalid wait next-state", args: []string{"tick", "--wait", "--wait-next-state", "missing"}, want: "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all"},
		{name: "negative wait timeout", args: []string{"tick", "--wait", "--wait-timeout", "-1s"}, want: "--wait-timeout must be >= 0"},
		{name: "commands requires dry run", args: []string{"tick", "--commands"}, want: "--commands requires --dry-run"},
		{name: "commands rejects json", args: []string{"tick", "--dry-run", "--commands", "--json"}, want: "--commands cannot be combined with --json"},
		{name: "commands rejects format", args: []string{"tick", "--dry-run", "--commands", "--format", "{{.DryRun}}"}, want: "--commands cannot be combined with --format"},
		{name: "commands rejects watch", args: []string{"tick", "--dry-run", "--commands", "--watch"}, want: "--commands cannot be combined with --watch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("tick invalid wait flags succeeded: stdout=%s", out.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestTickAllReadyStepsDryRun(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.parallel_checks]
trigger.event = "ticket.created"

[[pipelines.parallel_checks.steps]]
id = "lint"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "test"
target = "worker"

[[pipelines.parallel_checks.steps]]
id = "review"
target = "manager"
after = ["lint", "test"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	create := NewRootCmd()
	createOut, createErr := &bytes.Buffer{}, &bytes.Buffer{}
	create.SetOut(createOut)
	create.SetErr(createErr)
	create.SetArgs([]string{"pipeline", "run", "parallel_checks", "SQU-320", "--repo", target, "--json"})
	if err := create.Execute(); err != nil {
		t.Fatalf("pipeline run: %v\nstderr=%s", err, createErr.String())
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"tick", "--target", target, "--dry-run", "--skip-reconcile", "--skip-schedules", "--skip-drain", "--all-ready-steps", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick all-ready dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick all-ready: %v\nbody=%s", err, out.String())
	}
	if len(result.Advance) != 2 || result.Advance[0].StepID != "lint" || result.Advance[0].StepStatus != job.StatusQueued || result.Advance[1].StepID != "test" {
		t.Fatalf("tick all-ready advance = %+v, want queued lint then ready test", result.Advance)
	}

	limited := NewRootCmd()
	limitedOut, limitedErr := &bytes.Buffer{}, &bytes.Buffer{}
	limited.SetOut(limitedOut)
	limited.SetErr(limitedErr)
	limited.SetArgs([]string{"tick", "--target", target, "--dry-run", "--skip-reconcile", "--skip-schedules", "--skip-drain", "--all-ready-steps", "--limit", "1", "--json"})
	if err := limited.Execute(); err != nil {
		t.Fatalf("tick all-ready limited: %v\nstderr=%s", err, limitedErr.String())
	}
	var limitedResult tickResult
	if err := json.Unmarshal(limitedOut.Bytes(), &limitedResult); err != nil {
		t.Fatalf("decode limited tick all-ready: %v\nbody=%s", err, limitedOut.String())
	}
	if len(limitedResult.Advance) != 1 || limitedResult.Advance[0].StepID != "lint" {
		t.Fatalf("limited tick advance = %+v, want queued first step", limitedResult.Advance)
	}
}

func TestTickReconcilesJobEvents(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-141",
		Ticket:    "SQU-141",
		Target:    "worker",
		Instance:  "worker-squ-141",
		Status:    job.StatusRunning,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	exitCode := 0
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "worker-squ-141",
		Agent:     "worker",
		Job:       "squ-141",
		Ticket:    "SQU-141",
		Workspace: target,
		Status:    daemon.StatusExited,
		ExitCode:  &exitCode,
		StartedAt: now.Add(-time.Minute),
		ExitedAt:  now,
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	dry := NewRootCmd()
	dryOut, dryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dry.SetOut(dryOut)
	dry.SetErr(dryErr)
	dry.SetArgs([]string{"tick", "--target", target, "--dry-run", "--skip-schedules", "--skip-drain", "--skip-advance", "--json"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("tick dry-run event reconcile: %v\nstderr=%s", err, dryErr.String())
	}
	var preview tickResult
	if err := json.Unmarshal(dryOut.Bytes(), &preview); err != nil {
		t.Fatalf("decode tick dry-run event json: %v\nbody=%s", err, dryOut.String())
	}
	if len(preview.JobEvents) != 1 || preview.JobEvents[0].JobID != "squ-141" || preview.JobEvents[0].After != job.StatusDone || !preview.JobEvents[0].Changed || !preview.JobEvents[0].DryRun {
		t.Fatalf("tick preview job events = %+v", preview.JobEvents)
	}
	unchanged, err := job.Read(teamDir, "squ-141")
	if err != nil {
		t.Fatalf("read dry-run job: %v", err)
	}
	if unchanged.Status != job.StatusRunning {
		t.Fatalf("tick dry-run mutated job = %+v", unchanged)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"tick", "--target", target, "--skip-schedules", "--skip-drain", "--skip-advance", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick event reconcile: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick event json: %v\nbody=%s", err, out.String())
	}
	if len(result.JobEvents) != 1 || result.JobEvents[0].JobID != "squ-141" || result.JobEvents[0].After != job.StatusDone || !result.JobEvents[0].Changed {
		t.Fatalf("tick job events result = %+v", result.JobEvents)
	}
	updated, err := job.Read(teamDir, "squ-141")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if updated.Status != job.StatusDone || updated.LastEvent != "instance_exited" {
		t.Fatalf("tick reconciled job = %+v", updated)
	}
}

func TestTickDryRunIncludesDueSchedules(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-tick-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"tick",
		"--target", tmp,
		"--dry-run",
		"--skip-reconcile",
		"--skip-drain",
		"--skip-advance",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick schedule dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick schedule dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Schedule == nil || !result.Schedule.DryRun || result.Schedule.WouldFire != 1 {
		t.Fatalf("tick schedule result = %+v", result)
	}
	if len(result.Schedule.Schedules) != 1 || result.Schedule.Schedules[0].Name != "nightly" || result.Schedule.Schedules[0].Reason != "run_on_start" {
		t.Fatalf("tick schedule rows = %+v", result.Schedule.Schedules)
	}
	if result.Reconcile != nil || result.Queue != nil || result.Advance != nil {
		t.Fatalf("skipped tick sections = %+v", result)
	}
	if _, err := daemon.ReadScheduleState(daemon.DaemonRoot(teamDir), "nightly"); !os.IsNotExist(err) {
		t.Fatalf("tick dry-run wrote schedule state, err=%v", err)
	}
}

func TestTickUntilIdleRunsUntilScheduleClears(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-tick-idle-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	writeScheduleTopology(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"tick",
		"--target", tmp,
		"--until-idle",
		"--max-cycles", "3",
		"--interval", "0s",
		"--skip-reconcile",
		"--skip-drain",
		"--skip-advance",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tick until-idle: %v\nstderr=%s", err, stderr.String())
	}
	var result tickUntilIdleResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode tick until-idle json: %v\nbody=%s", err, out.String())
	}
	if !result.Idle || result.HitLimit || result.CyclesRun != 2 || len(result.Cycles) != 2 {
		t.Fatalf("until-idle result = %+v", result)
	}
	if result.Cycles[0].Schedule == nil || result.Cycles[0].Schedule.Fired != 1 {
		t.Fatalf("first cycle schedule = %+v", result.Cycles[0].Schedule)
	}
	if result.Cycles[1].Schedule == nil || result.Cycles[1].Schedule.Fired != 0 || len(result.Cycles[1].Schedule.Schedules) != 0 {
		t.Fatalf("second cycle schedule = %+v", result.Cycles[1].Schedule)
	}
	if _, err := daemon.ReadScheduleState(daemon.DaemonRoot(teamDir), "nightly"); err != nil {
		t.Fatalf("schedule state not written: %v", err)
	}
	messages, err := daemon.ReadMessages(daemon.DaemonRoot(teamDir), "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestDrainRunsUntilIdle(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:         "q-drain",
		State:      daemon.QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-drain",
		Payload:    map[string]any{"target": "worker", "name": "worker-drain", "ticket": "SQU-DRAIN"},
		QueuedAt:   now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("write queue item: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"drain",
		"--target", target,
		"--skip-reconcile",
		"--skip-schedules",
		"--skip-advance",
		"--interval", "0s",
		"--max-cycles", "3",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("drain: %v\nstderr=%s", err, stderr.String())
	}
	var result tickUntilIdleResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode drain json: %v\nbody=%s", err, out.String())
	}
	if !result.Idle || result.HitLimit || result.CyclesRun != 2 || len(result.Cycles) != 2 {
		t.Fatalf("drain result = %+v", result)
	}
	if result.Cycles[0].Queue == nil || result.Cycles[0].Queue.Dispatched != 1 {
		t.Fatalf("first drain cycle queue = %+v", result.Cycles[0].Queue)
	}
	if result.Cycles[1].Queue == nil || result.Cycles[1].Queue.Dispatched != 0 || result.Cycles[1].Queue.Pending != 0 {
		t.Fatalf("second drain cycle queue = %+v", result.Cycles[1].Queue)
	}
	if _, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), "q-drain"); !os.IsNotExist(err) {
		t.Fatalf("drain queue item still exists or unexpected err=%v", err)
	}
	stopAndWaitForTest(t, mgr, "worker-drain")
}

func TestDrainWaitsForAdvancedJobs(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-427",
		Ticket:    "SQU-427",
		Target:    "worker",
		Kickoff:   "SQU-427: implement",
		Pipeline:  "ticket_to_pr",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Steps: []job.Step{
			{ID: "triage", Target: "manager", Status: job.StatusDone, Instance: "manager", StartedAt: now, FinishedAt: now},
			{ID: "implement", Target: "worker", Status: job.StatusBlocked, After: []string{"triage"}},
		},
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write ready job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"drain",
		"--target", target,
		"--workspace", "repo",
		"--skip-reconcile",
		"--skip-schedules",
		"--skip-drain",
		"--wait",
		"--wait-status", "running",
		"--wait-next-state", "running",
		"--wait-step", "implement",
		"--wait-timeout", "2s",
		"--wait-interval", "10ms",
		"--interval", "0s",
		"--max-cycles", "3",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("drain --wait: %v\nstderr=%s", err, stderr.String())
	}
	var result tickUntilIdleResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode drain wait json: %v\nbody=%s", err, out.String())
	}
	if !result.Idle || result.HitLimit || result.CyclesRun != 2 || len(result.Cycles) != 2 {
		t.Fatalf("drain wait result = %+v", result)
	}
	if len(result.Cycles[0].Advance) != 1 || result.Cycles[0].Advance[0].Action != "advanced" || result.Cycles[0].Advance[0].Job == nil || result.Cycles[0].Advance[0].Job.Status != job.StatusRunning || result.Cycles[0].Advance[0].Job.LastEvent != "advance_dispatched" {
		t.Fatalf("drain wait advance = %+v", result.Cycles[0].Advance)
	}
	if result.Cycles[0].Advance[0].Step == nil || result.Cycles[0].Advance[0].Step.ID != "implement" || result.Cycles[0].Advance[0].Step.Status != job.StatusRunning || result.Cycles[0].Advance[0].Step.Instance != "worker-squ-427-implement" {
		t.Fatalf("drain wait step = %+v", result.Cycles[0].Advance[0].Step)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-427-implement")
}

func TestTickUntilIdleRejectsDryRun(t *testing.T) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"tick", "--until-idle", "--dry-run"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("tick --until-idle --dry-run succeeded: stdout=%s", out.String())
	}
	if !strings.Contains(stderr.String(), "--until-idle cannot be combined with --dry-run") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	previewRoutes := NewRootCmd()
	previewRoutesOut, previewRoutesErr := &bytes.Buffer{}, &bytes.Buffer{}
	previewRoutes.SetOut(previewRoutesOut)
	previewRoutes.SetErr(previewRoutesErr)
	previewRoutes.SetArgs([]string{"tick", "--preview-routes"})
	if err := previewRoutes.Execute(); err == nil {
		t.Fatalf("tick --preview-routes without --dry-run succeeded: stdout=%s", previewRoutesOut.String())
	}
	if !strings.Contains(previewRoutesErr.String(), "--preview-routes requires --dry-run") {
		t.Fatalf("stderr = %q", previewRoutesErr.String())
	}
}

func TestDrainRejectsInvalidFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "negative interval", args: []string{"drain", "--interval", "-1s"}, want: "--interval must be >= 0"},
		{name: "negative wait interval", args: []string{"drain", "--wait", "--wait-interval", "-1s"}, want: "--wait-interval must be >= 0"},
		{name: "negative wait timeout", args: []string{"drain", "--wait", "--wait-timeout", "-1s"}, want: "--wait-timeout must be >= 0"},
		{name: "zero max cycles", args: []string{"drain", "--max-cycles", "0"}, want: "--max-cycles must be > 0"},
		{name: "format with json", args: []string{"drain", "--format", "{{.CyclesRun}}", "--json"}, want: "--format cannot be combined with --json"},
		{name: "wait skip advance", args: []string{"drain", "--wait", "--skip-advance"}, want: "--wait requires pipeline advancement"},
		{name: "wait flag without wait", args: []string{"drain", "--wait-status", "running"}, want: "wait-related flags require --wait"},
		{name: "wait next-state without wait", args: []string{"drain", "--wait-next-state", "running"}, want: "wait-related flags require --wait"},
		{name: "wait step without wait", args: []string{"drain", "--wait-step", "implement"}, want: "wait-related flags require --wait"},
		{name: "invalid wait next-state", args: []string{"drain", "--wait", "--wait-next-state", "missing"}, want: "--wait-next-state must be ready, queued, running, blocked, failed, held, done, none, or all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("drain invalid flags succeeded: stdout=%s", out.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestTickWatchRendersCanceledSnapshot(t *testing.T) {
	target, _, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()
	teamDir := filepath.Join(target, ".agent_team")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	if err := runTickLoop(ctx, cmd, teamDir, "repo", 0, tickOptions{
		SkipReconcile: true,
		SkipSchedules: true,
		SkipDrain:     true,
		SkipAdvance:   true,
	}, true, nil, time.Millisecond); err != nil {
		t.Fatalf("tick watch loop: %v\nstderr=%s", err, stderr.String())
	}
	var result tickResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode watch tick json: %v\nbody=%s", err, out.String())
	}
	if result.Reconcile != nil || result.Schedule != nil || result.Queue != nil || result.Advance != nil {
		t.Fatalf("watch result = %+v, want skipped sections", result)
	}
}
