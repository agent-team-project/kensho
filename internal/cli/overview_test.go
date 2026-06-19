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

func TestOverviewReportsIntakeErrors(t *testing.T) {
	root := writeIntakeErrorFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview intake json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview intake: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.State != "attention" {
		t.Fatalf("overview state = ok:%v state:%q", overview.OK, overview.State)
	}
	if overview.Intake.Deliveries != 1 || overview.Intake.Errors != 1 || overview.Intake.Replayable != 1 || overview.Intake.LatestErrorID != "intake-failed" {
		t.Fatalf("intake summary = %+v", overview.Intake)
	}
	for _, want := range []string{
		"agent-team intake summary",
		"agent-team intake deliveries --status error",
		"agent-team intake replay --all --dry-run --preview-triggers",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}
}

func TestOverviewRecommendsBatchCleanupReadyJobs(t *testing.T) {
	root := writeOverviewCleanupFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview cleanup json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview cleanup: %v\nbody=%s", err, out.String())
	}
	if overview.Jobs.Summary.Total != 1 || overview.Jobs.Summary.Done != 1 || overview.Jobs.Attention != 1 || overview.Jobs.CleanupReady != 1 {
		t.Fatalf("jobs = %+v", overview.Jobs)
	}
	for _, want := range []string{
		"agent-team job triage",
		"agent-team job cleanup --all --dry-run",
	} {
		if !stringSliceContains(overview.Actions, want) {
			t.Fatalf("actions missing %q: %+v", want, overview.Actions)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"overview", "--target", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("overview cleanup text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{
		"cleanup_ready=1",
		"agent-team job cleanup --all --dry-run",
	} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("overview cleanup text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestOverviewRecommendsIntakeDoctorForLedgerParseErrors(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intakeDeliveryLogPath(teamDir), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview corrupt intake json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview corrupt intake: %v\nbody=%s", err, out.String())
	}
	if overview.OK || overview.SectionErrors["intake"] == "" {
		t.Fatalf("overview = %+v", overview)
	}
	if !stringSliceContains(overview.Actions, "agent-team intake doctor") {
		t.Fatalf("actions missing intake doctor: %+v", overview.Actions)
	}
}

func TestOverviewIgnoresRecoveredIntakeErrors(t *testing.T) {
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	replayedAt := time.Date(2026, 6, 19, 12, 5, 0, 0, time.UTC)
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:           "intake-recovered",
		Time:         time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Provider:     "linear",
		Status:       intakeDeliveryStatusError,
		HTTPStatus:   503,
		EventType:    "ticket.created",
		Payload:      map[string]any{"source": "linear", "ticket": "SQU-801", "title": "Recovered intake"},
		Ticket:       "SQU-801",
		Error:        "daemon is not running",
		ReplayStatus: intakeDeliveryReplayStatusOK,
		ReplayedAt:   &replayedAt,
	}); err != nil {
		t.Fatalf("append recovered intake delivery: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"overview", "--target", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("overview recovered intake json: %v\nstderr=%s", err, stderr.String())
	}
	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview recovered intake: %v\nbody=%s", err, out.String())
	}
	if overview.Intake.Deliveries != 1 || overview.Intake.Errors != 0 || overview.Intake.Recovered != 1 || overview.Intake.Replayable != 0 || overview.Intake.LatestErrorID != "" {
		t.Fatalf("intake summary = %+v", overview.Intake)
	}
	for _, unwanted := range []string{
		"agent-team intake summary",
		"agent-team intake deliveries --status error",
		"agent-team intake replay --all --dry-run --preview-triggers",
	} {
		if stringSliceContains(overview.Actions, unwanted) {
			t.Fatalf("actions should not contain %q: %+v", unwanted, overview.Actions)
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

func TestTeamOverviewRecommendsScopedCleanupCommand(t *testing.T) {
	root := writeOverviewCleanupFixture(t)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"team", "overview", "delivery", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("team overview cleanup json: %v\nstderr=%s", err, stderr.String())
	}

	var overview overviewResult
	if err := json.Unmarshal(out.Bytes(), &overview); err != nil {
		t.Fatalf("decode team overview cleanup: %v\nbody=%s", err, out.String())
	}
	if overview.Team == nil || overview.Team.Name != "delivery" || overview.Jobs.CleanupReady != 1 {
		t.Fatalf("team overview = %+v", overview)
	}
	if !stringSliceContains(overview.Actions, "agent-team team cleanup delivery --dry-run") {
		t.Fatalf("actions missing scoped cleanup command: %+v", overview.Actions)
	}
	if stringSliceContains(overview.Actions, "agent-team job cleanup --all --dry-run") {
		t.Fatalf("team actions should not include unscoped batch cleanup: %+v", overview.Actions)
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

func writeOverviewCleanupFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instances := `
[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["worker"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(instances), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	j := &job.Job{
		ID:        "squ-710",
		Ticket:    "SQU-710",
		Target:    "worker",
		Status:    job.StatusDone,
		Branch:    "worktree-worker-squ-710",
		Worktree:  filepath.Join(root, ".claude", "worktrees", "worker-squ-710"),
		PR:        "https://github.com/acme/repo/pull/710",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	return root
}

func writeIntakeErrorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "intake-failed",
		Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Provider:   "linear",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: 503,
		EventType:  "ticket.created",
		Payload:    map[string]any{"source": "linear", "ticket": "SQU-800", "title": "Failed intake"},
		Ticket:     "SQU-800",
		Error:      "daemon is not running",
	}); err != nil {
		t.Fatalf("append intake delivery: %v", err)
	}
	return root
}
