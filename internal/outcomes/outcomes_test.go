package outcomes

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/usage"
)

func TestBuildRecordDerivesTerminalOutcome(t *testing.T) {
	teamDir := testOutcomeTeamDir(t)
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	kickoff := `Implement SQU-135

## Review findings (bounce 1)

Build failed because the behavior was missing a required test.

## Review findings (bounce 2)

CI timeout looked like infra, but the implementation still missed scope.`
	j, err := jobstore.New("SQU-135", "worker", kickoff, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = jobstore.StatusDone
	j.Pipeline = "ticket_to_pr"
	j.Instance = "worker-squ-135"
	j.Epic = "resource-governance"
	j.PR = "https://github.com/acme/repo/pull/135"
	j.TokenBudget = 200
	j.TimeBudget = "3h"
	j.Steps = []jobstore.Step{
		{ID: "implement", Target: "worker", Status: jobstore.StatusDone, Attempts: 1, StartedAt: now.Add(-2 * time.Hour), RunningAt: now.Add(-2 * time.Hour), FinishedAt: now.Add(-90 * time.Minute)},
		{ID: "review", Target: "reviewer", Status: jobstore.StatusDone, Attempts: 3, StartedAt: now.Add(-80 * time.Minute), RunningAt: now.Add(-80 * time.Minute), FinishedAt: now.Add(-10 * time.Minute)},
	}
	j.Usage, _ = usage.MergeRecord(nil, usage.Record{
		Instance:        "worker-squ-135",
		Agent:           "worker",
		Runtime:         "codex",
		TokensAvailable: true,
		InputTokens:     120,
		OutputTokens:    30,
		DurationMS:      int64((90 * time.Minute).Milliseconds()),
		StartedAt:       now.Add(-2 * time.Hour),
		EndedAt:         now.Add(-30 * time.Minute),
	})
	j.UpdatedAt = now
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	appendOutcomeEvent(t, teamDir, j.ID, "budget_notice", jobstore.StatusRunning, now.Add(-50*time.Minute), "100%", map[string]string{"level": "100"})
	appendOutcomeEvent(t, teamDir, j.ID, "watchdog_extended", jobstore.StatusRunning, now.Add(-45*time.Minute), "watchdog extended", nil)
	appendOutcomeEvent(t, teamDir, j.ID, "pr.merged", jobstore.StatusDone, now, "pull request merged", map[string]string{
		"budget_tokens_allocated": "200",
		"budget_tokens_consumed":  "150",
		"budget_tokens_released":  "50",
	})
	if err := jobstore.AppendGateRecord(teamDir, &jobstore.GateRecord{
		TS:        now.Add(-time.Hour),
		JobID:     j.ID,
		Name:      "tests",
		Status:    jobstore.GateStatusFail,
		Signature: "go test failed",
	}); err != nil {
		t.Fatalf("AppendGateRecord: %v", err)
	}

	rec, err := BuildRecord(teamDir, j, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildRecord: %v", err)
	}
	if rec.JobID != "squ-135" || rec.Status != "done" || rec.Team != "delivery" || rec.Agent != "worker" {
		t.Fatalf("identity = %+v", rec)
	}
	if rec.Epic != "resource-governance" {
		t.Fatalf("epic = %q", rec.Epic)
	}
	if rec.ReviewRounds != 3 || rec.BounceCount != 2 {
		t.Fatalf("review/bounce = rounds %d bounces %d", rec.ReviewRounds, rec.BounceCount)
	}
	if rec.BounceClasses["content"] != 2 || rec.BounceClasses["validation"] != 1 || rec.BounceClasses["infra"] != 1 {
		t.Fatalf("bounce classes = %+v", rec.BounceClasses)
	}
	if rec.TimeToMergeMS != int64((2*time.Hour).Milliseconds()) || rec.TimeToTerminalMS != rec.TimeToMergeMS {
		t.Fatalf("merge/terminal durations = %d/%d", rec.TimeToMergeMS, rec.TimeToTerminalMS)
	}
	if rec.TokensConsumed != 150 || rec.TokenBudget != 200 || rec.TokenBudgetRatio != 0.75 {
		t.Fatalf("token budget = consumed %d budget %d ratio %.2f", rec.TokensConsumed, rec.TokenBudget, rec.TokenBudgetRatio)
	}
	if rec.TokensAllocated != 200 || rec.TokensReleased != 50 {
		t.Fatalf("allocation totals = allocated %d released %d", rec.TokensAllocated, rec.TokensReleased)
	}
	if len(rec.WatchdogEvents) != 1 || len(rec.BudgetNoticeEvents) != 1 || len(rec.BudgetExceededEvents) != 1 {
		t.Fatalf("events = watchdog %d notices %d exceeded %d", len(rec.WatchdogEvents), len(rec.BudgetNoticeEvents), len(rec.BudgetExceededEvents))
	}
	if len(rec.WorkUnits) != 1 || rec.WorkUnits[0].Target != "worker" || rec.WorkUnits[0].StartedAt != now.Add(-2*time.Hour) || rec.WorkUnits[0].FinishedAt != now.Add(-30*time.Minute) {
		t.Fatalf("work units = %+v", rec.WorkUnits)
	}
	if !rec.WorkUnitsExhaustive {
		t.Fatalf("work units should be marked exhaustive")
	}
	if rec.GateFailures != 1 || rec.GateFailureClasses["signature"] != 1 {
		t.Fatalf("gate failures = %d %+v", rec.GateFailures, rec.GateFailureClasses)
	}
}

func TestWorkUnitsForJobUseRuntimeUsageRecords(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	queuedAt := now.Add(-2 * time.Hour)
	runningAt := now.Add(-90 * time.Minute)
	finishedAt := now.Add(-30 * time.Minute)

	queuedOnly := &jobstore.Job{
		ID:        "squ-161-queued",
		Status:    jobstore.StatusDone,
		CreatedAt: queuedAt,
		Instance:  "worker-squ-161",
		Steps: []jobstore.Step{{
			ID:         "implement",
			Target:     "worker",
			Status:     jobstore.StatusDone,
			StartedAt:  queuedAt,
			FinishedAt: finishedAt,
		}},
	}
	if units := workUnitsForJob(queuedOnly, "worker"); len(units) != 0 {
		t.Fatalf("queued-only step produced work units: %+v", units)
	}

	runningWithoutUsage := &jobstore.Job{
		ID:        "squ-161-running",
		Status:    jobstore.StatusDone,
		CreatedAt: queuedAt,
		Instance:  "worker-squ-161",
		Steps: []jobstore.Step{{
			ID:         "implement",
			Target:     "worker",
			Status:     jobstore.StatusDone,
			StartedAt:  queuedAt,
			RunningAt:  runningAt,
			FinishedAt: finishedAt,
		}},
	}
	if units := workUnitsForJob(runningWithoutUsage, "worker"); len(units) != 0 {
		t.Fatalf("running step without usage produced work units: %+v", units)
	}

	withUsage := *runningWithoutUsage
	withUsage.Usage, _ = usage.MergeRecord(nil, usage.Record{
		Instance:  "worker-squ-161",
		Agent:     "worker",
		Runtime:   "codex",
		StartedAt: runningAt,
		EndedAt:   finishedAt,
	})
	units := workUnitsForJob(&withUsage, "worker")
	if len(units) != 1 {
		t.Fatalf("running step work units = %+v", units)
	}
	if units[0].StartedAt != runningAt || units[0].FinishedAt != finishedAt {
		t.Fatalf("work interval = %s..%s, want %s..%s", units[0].StartedAt, units[0].FinishedAt, runningAt, finishedAt)
	}
}

func TestBuildReportDoesNotFallbackWithoutUsageRecords(t *testing.T) {
	teamDir := testOutcomeTeamDir(t)
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	queuedAt := now.Add(-2 * time.Hour)
	finishedAt := now.Add(-30 * time.Minute)

	j, err := jobstore.New("SQU-161", "worker", "Implement SQU-161", queuedAt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = jobstore.StatusDone
	j.Pipeline = "ticket_to_pr"
	j.Instance = "worker-squ-161"
	j.UpdatedAt = finishedAt
	j.Steps = []jobstore.Step{{
		ID:         "implement",
		Target:     "worker",
		Status:     jobstore.StatusDone,
		StartedAt:  queuedAt,
		FinishedAt: finishedAt,
	}}

	rec, err := BuildRecord(teamDir, j, now)
	if err != nil {
		t.Fatalf("BuildRecord: %v", err)
	}
	if len(rec.WorkUnits) != 0 || !rec.WorkUnitsExhaustive {
		t.Fatalf("work units = %+v exhaustive=%v", rec.WorkUnits, rec.WorkUnitsExhaustive)
	}

	report := BuildReport([]Record{*rec}, ReportOptions{Team: "delivery", Agent: "worker", TeamDir: teamDir, Now: now})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v", report.Rows)
	}
	row := report.Rows[0]
	if row.EffectiveConcurrency != 0 || row.PeakConcurrentWorkUnits != 0 {
		t.Fatalf("row concurrency = %+v", row)
	}
	if report.Summary.EffectiveConcurrency != 0 || report.Summary.PeakConcurrentWorkUnits != 0 {
		t.Fatalf("summary concurrency = %+v", report.Summary)
	}
}

func TestBuildRecordUsesRuntimeUsageWindowForNoStepJob(t *testing.T) {
	teamDir := testOutcomeTeamDir(t)
	createdAt := time.Date(2026, 7, 5, 15, 46, 7, 0, time.UTC)
	runningAt := time.Date(2026, 7, 5, 16, 16, 1, 0, time.UTC)
	finishedAt := runningAt.Add(30 * time.Minute)

	j, err := jobstore.New("SQU-112", "worker", "Implement SQU-112", createdAt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = jobstore.StatusDone
	j.Instance = "worker-squ-112"
	j.UpdatedAt = finishedAt
	j.Usage, _ = usage.MergeRecord(nil, usage.Record{
		Instance:  "worker-squ-112",
		Agent:     "worker",
		Runtime:   "codex",
		StartedAt: runningAt,
		EndedAt:   finishedAt,
	})

	rec, err := BuildRecord(teamDir, j, finishedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildRecord: %v", err)
	}
	if len(rec.WorkUnits) != 1 {
		t.Fatalf("work units = %+v", rec.WorkUnits)
	}
	if rec.WorkUnits[0].StartedAt != runningAt || rec.WorkUnits[0].FinishedAt != finishedAt {
		t.Fatalf("work interval = %s..%s, want %s..%s", rec.WorkUnits[0].StartedAt, rec.WorkUnits[0].FinishedAt, runningAt, finishedAt)
	}

	earlier := Record{
		JobID:       "squ-earlier",
		Status:      "done",
		Week:        rec.Week,
		Team:        "delivery",
		Agent:       "worker",
		FinalizedAt: runningAt.Add(-6 * time.Minute),
		WorkUnits: []WorkUnitRecord{{
			ID:         "usage-earlier",
			Target:     "worker",
			StartedAt:  createdAt.Add(4 * time.Minute),
			FinishedAt: runningAt.Add(-6 * time.Minute),
		}},
	}
	report := BuildReport([]Record{earlier, *rec}, ReportOptions{Team: "delivery", Agent: "worker", TeamDir: teamDir, Now: finishedAt})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v", report.Rows)
	}
	row := report.Rows[0]
	if row.EffectiveConcurrency != 1 || row.PeakConcurrentWorkUnits != 1 {
		t.Fatalf("row concurrency = %+v", row)
	}
}

func TestBuildRecordUsesImplementationAgentAfterPipelineTargetRewrite(t *testing.T) {
	teamDir := testOutcomeTeamDir(t)
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New("SQU-136", "worker", "Implement SQU-136", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = jobstore.StatusDone
	j.Pipeline = "ticket_to_pr"
	j.Steps = []jobstore.Step{
		{ID: "implement", Target: "worker", Status: jobstore.StatusDone, Attempts: 1},
		{ID: "review", Target: "reviewer", Status: jobstore.StatusDone, Attempts: 1, After: []string{"implement"}},
		{ID: "approve", Target: "manager", Status: jobstore.StatusDone, Attempts: 1, After: []string{"review"}},
	}
	jobstore.SetImplementationAgentFromSteps(j)
	j.Target = "manager"
	j.Instance = "manager-squ-136"
	j.UpdatedAt = now
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	appendOutcomeEvent(t, teamDir, j.ID, "closed", jobstore.StatusDone, now, "done", nil)

	rec, err := BuildRecord(teamDir, j, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildRecord: %v", err)
	}
	if rec.Agent != "worker" {
		t.Fatalf("agent = %q, want worker; record=%+v", rec.Agent, rec)
	}
}

func TestBuildReportAggregatesTrends(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	records := []Record{
		{
			JobID:       "squ-1",
			Status:      "done",
			Week:        "2026-W28",
			Team:        "delivery",
			Agent:       "worker",
			FinalizedAt: now,
			WorkUnits: []WorkUnitRecord{{
				ID:         "implement",
				Target:     "worker",
				StartedAt:  now.Add(-2 * time.Hour),
				FinishedAt: now.Add(-90 * time.Minute),
			}},
			ReviewRounds:     2,
			BounceCount:      1,
			BounceClasses:    map[string]int{"content": 1},
			TokenBudget:      100,
			TokensConsumed:   70,
			TimeToMergeMS:    1000,
			TimeToTerminalMS: 1200,
		},
		{
			JobID:       "squ-2",
			Status:      "failed",
			Week:        "2026-W28",
			Team:        "delivery",
			Agent:       "worker",
			FinalizedAt: now.Add(time.Hour),
			WorkUnits: []WorkUnitRecord{{
				ID:         "implement",
				Target:     "worker",
				StartedAt:  now.Add(-105 * time.Minute),
				FinishedAt: now.Add(-75 * time.Minute),
			}},
			ReviewRounds:     0,
			BounceCount:      0,
			TokenBudget:      100,
			TokensConsumed:   30,
			TimeToTerminalMS: 800,
			WatchdogEvents:   []EventRef{{Type: "watchdog"}},
		},
	}
	report := BuildReport(records, ReportOptions{Team: "delivery", Agent: "worker", TeamDir: testOutcomeTeamDir(t), Now: now})
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v", report.Rows)
	}
	row := report.Rows[0]
	if row.Jobs != 2 || row.Done != 1 || row.Failed != 1 || row.Bounces != 1 || row.AverageBounces != 0.5 {
		t.Fatalf("row counts = %+v", row)
	}
	if row.TokensConsumed != 100 || row.TokenBudget != 200 || row.TokenBudgetRatio != 0.5 {
		t.Fatalf("row budget = %+v", row)
	}
	if row.AverageTimeToMergeMS != 1000 || row.AverageTimeToTerminalMS != 1000 {
		t.Fatalf("row durations = %+v", row)
	}
	if row.EffectiveConcurrency != 1.33 || row.PeakConcurrentWorkUnits != 2 || row.DeclaredReplicaCapacity != 2 || row.ConcurrencyUtilization != 0.67 {
		t.Fatalf("row concurrency = %+v", row)
	}
	if report.Summary.Jobs != 2 || report.Summary.WatchdogEvents != 1 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	if report.Summary.EffectiveConcurrency != 1.33 || report.Summary.DeclaredReplicaCapacity != 2 {
		t.Fatalf("summary concurrency = %+v", report.Summary)
	}
}

func TestBuildReportByEpicAggregatesAllocations(t *testing.T) {
	teamDir := testOutcomeTeamDir(t)
	config := `[pm]
provider = "none"

[outcomes.epic_allocations]
resource-governance = "200"
project-2 = 80
`
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	records := []Record{
		{
			JobID:          "squ-1",
			Status:         "done",
			Epic:           "resource-governance",
			Team:           "delivery",
			Agent:          "worker",
			FinalizedAt:    now,
			BounceCount:    1,
			TokenBudget:    100,
			TokensConsumed: 70,
		},
		{
			JobID:          "squ-2",
			Status:         "failed",
			Epic:           "resource-governance",
			Team:           "platform",
			Agent:          "worker",
			FinalizedAt:    now.Add(time.Hour),
			TokenBudget:    100,
			TokensConsumed: 30,
		},
		{
			JobID:          "squ-3",
			Status:         "done",
			Epic:           "project-2",
			Team:           "delivery",
			Agent:          "manager",
			FinalizedAt:    now.Add(2 * time.Hour),
			TokenBudget:    50,
			TokensConsumed: 40,
		},
	}
	report := BuildReport(records, ReportOptions{ByEpic: true, TeamDir: teamDir, Now: now})
	if !report.ByEpic || len(report.Rows) != 2 {
		t.Fatalf("report = %+v", report)
	}
	row := report.Rows[1]
	if row.Epic != "resource-governance" || row.Jobs != 2 || row.Done != 1 || row.Failed != 1 || row.Bounces != 1 || row.AverageBounces != 0.5 {
		t.Fatalf("resource row = %+v", row)
	}
	if row.TokensConsumed != 100 || row.TokenBudget != 200 || row.EpicAllocation != 200 || row.EpicAllocationRatio != 0.5 {
		t.Fatalf("resource tokens = %+v", row)
	}
	if report.Summary.Jobs != 3 || report.Summary.TokensConsumed != 140 || report.Summary.EpicAllocation != 280 || report.Summary.EpicAllocationRatio != 0.5 {
		t.Fatalf("summary = %+v", report.Summary)
	}
}

func testOutcomeTeamDir(t *testing.T) string {
	t.Helper()
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	config := `[pm]
provider = "none"
`
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	topology := `[instances.worker]
agent = "worker"
ephemeral = true
replicas = 2

[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topology), 0o644); err != nil {
		t.Fatalf("write topology: %v", err)
	}
	return teamDir
}

func appendOutcomeEvent(t *testing.T, teamDir, jobID, eventType string, status jobstore.Status, ts time.Time, message string, data map[string]string) {
	t.Helper()
	if err := jobstore.AppendEvent(teamDir, &jobstore.Event{
		TS:      ts,
		JobID:   jobID,
		Type:    eventType,
		Status:  status,
		Message: message,
		Data:    data,
	}); err != nil {
		t.Fatalf("AppendEvent %s: %v", eventType, err)
	}
}
