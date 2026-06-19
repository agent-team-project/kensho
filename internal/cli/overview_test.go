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

func TestOverviewReportsAttentionAndActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if overview.Health.DaemonRunning || overview.Health.Issues == 0 {
		t.Fatalf("health = %+v, want daemon down with issues", overview.Health)
	}
	if overview.Topology == nil || overview.Topology.Instances != 2 || overview.Topology.Pipelines != 1 {
		t.Fatalf("topology = %+v", overview.Topology)
	}
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Summary.Blocked != 1 || overview.Jobs.Attention != 1 {
		t.Fatalf("jobs = %+v", overview.Jobs)
	}
	if overview.Queue.Total != 1 || overview.Queue.Dead != 1 || overview.Queue.Attempts != daemon.MaxQueueAttempts {
		t.Fatalf("queue = %+v", overview.Queue)
	}
	if overview.Pipelines.Total != 1 || overview.Pipelines.ReadySteps != 1 || overview.Pipelines.BlockedSteps != 0 {
		t.Fatalf("pipelines = %+v", overview.Pipelines)
	}
	if overview.Schedules.Declared != 1 || overview.Schedules.Due != 1 {
		t.Fatalf("schedules = %+v", overview.Schedules)
	}
	for _, want := range []string{
		"agent-team repair --dry-run --jobs",
		"agent-team daemon start",
		"agent-team queue retry --all --dry-run",
		"agent-team job triage",
		"agent-team schedule fire --dry-run --preview-triggers",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
}

func TestOverviewTextRendersOperatorSummary(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview text: %v\nstderr=%s", err, stderr.String())
	}

	for _, want := range []string{
		"overview: attention",
		"health: unhealthy daemon=down",
		"topology: instances=2 persistent=1 ephemeral=1",
		"jobs: total=1 queued=0 running=0 blocked=1 done=0 failed=0 attention=1",
		"queue: total=1 pending=0 dead=1",
		"pipelines: total=1 jobs=1 ready_steps=1 blocked_steps=0 failed_steps=0",
		"schedules: declared=1 due=1 upcoming=1",
		"actions:",
		"agent-team repair --dry-run --jobs",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("overview text missing %q:\n%s", want, out.String())
		}
	}
}

func TestTeamOverviewScopesCountsAndActions(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode team overview: %v\nbody=%s", err, out.String())
	}
	if overview.Team == nil || overview.Team.Name != "delivery" {
		t.Fatalf("team = %+v", overview.Team)
	}
	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if overview.Topology == nil || overview.Topology.Instances != 2 || overview.Topology.Teams != 1 || overview.Topology.Pipelines != 1 || overview.Topology.Schedules != 1 {
		t.Fatalf("topology = %+v", overview.Topology)
	}
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Attention != 1 || overview.Queue.Dead != 1 || overview.Pipelines.ReadySteps != 1 || overview.Schedules.Due != 1 {
		t.Fatalf("overview = %+v", overview)
	}
	for _, want := range []string{
		"agent-team team repair delivery --dry-run --jobs",
		"agent-team team queue retry delivery --all --dry-run",
		"agent-team team triage delivery",
		"agent-team team advance delivery --dry-run --preview-routes",
		"agent-team team tick delivery --dry-run --skip-drain --skip-advance",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
}

func TestTeamOverviewTextRendersTeamSummary(t *testing.T) {
	root := writeOverviewAttentionFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview text: %v\nstderr=%s", err, stderr.String())
	}

	for _, want := range []string{
		"overview: attention",
		"team: delivery",
		"topology: instances=2 persistent=1 ephemeral=1",
		"jobs: total=1 queued=0 running=0 blocked=1 done=0 failed=0 attention=1",
		"queue: total=1 pending=0 dead=1",
		"schedules: declared=1 due=1 upcoming=1",
		"agent-team team repair delivery --dry-run --jobs",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("team overview text missing %q:\n%s", want, out.String())
		}
	}
}

func TestOverviewStateReportsActiveForReadyWork(t *testing.T) {
	overview := &overviewResult{
		OK:    true,
		State: "ok",
		Queue: queueSummary{
			Pending: 1,
			Delayed: 0,
		},
	}
	overview.Actions = overviewActions(overview, nil)
	overview.OK = overviewOK(overview, nil)
	overview.State = overviewState(overview)

	if overview.OK || overview.State != "active" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if !stringSliceContains(overview.Actions, "agent-team queue drain --dry-run") {
		t.Fatalf("actions = %+v", overview.Actions)
	}
}

func TestOverviewStateReportsAttentionForFailures(t *testing.T) {
	overview := &overviewResult{
		OK: true,
		Queue: queueSummary{
			Dead: 1,
		},
	}
	overview.Actions = overviewActions(overview, nil)
	overview.OK = overviewOK(overview, nil)
	overview.State = overviewState(overview)

	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if !stringSliceContains(overview.Actions, "agent-team queue retry --all --dry-run") {
		t.Fatalf("actions = %+v", overview.Actions)
	}
}

func TestOverviewWatchRendersUntilContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &bytes.Buffer{}
	calls := 0

	err := runOverviewWatch(ctx, out, func(now time.Time) (*overviewResult, error) {
		calls++
		cancel()
		return &overviewResult{
			OK:         true,
			State:      "ok",
			CapturedAt: now.UTC().Format(time.RFC3339),
			Health: overviewHealthSummary{
				Healthy: true,
			},
		}, nil
	}, false, time.Millisecond, false)
	if err != nil {
		t.Fatalf("runOverviewWatch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !strings.Contains(out.String(), "overview: ok") || !strings.Contains(out.String(), "actions: none") {
		t.Fatalf("watch output:\n%s", out.String())
	}
}

func writeOverviewAttentionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[schedules.nightly]
every = "1h"
run_on_start = true
payload.kind = "nightly"

[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	j, err := job.New("SQU-700", "worker", "test kickoff", now)
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Status = job.StatusBlocked
	j.Pipeline = "ticket_to_pr"
	j.Steps = []job.Step{{
		ID:        "implement",
		Target:    "worker",
		Status:    job.StatusBlocked,
		StartedAt: now.Add(-time.Hour),
	}}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}

	item := &daemon.QueueItem{
		ID:             "q-overview",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-700",
		Payload:        map[string]any{"target": "worker", "ticket": "SQU-700", "job_id": "squ-700"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now,
		UpdatedAt:      now,
		DeadLetteredAt: now.Add(time.Minute),
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}
	return root
}
