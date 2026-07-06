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
	j.PR = "https://github.com/acme/repo/pull/135"
	j.TokenBudget = 200
	j.TimeBudget = "3h"
	j.Steps = []jobstore.Step{
		{ID: "implement", Target: "worker", Status: jobstore.StatusDone, Attempts: 1, StartedAt: now.Add(-2 * time.Hour), FinishedAt: now.Add(-90 * time.Minute)},
		{ID: "review", Target: "reviewer", Status: jobstore.StatusDone, Attempts: 3, StartedAt: now.Add(-80 * time.Minute), FinishedAt: now.Add(-10 * time.Minute)},
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
	if len(rec.WorkUnits) != 2 || rec.WorkUnits[0].Target != "worker" || rec.WorkUnits[0].StartedAt != now.Add(-2*time.Hour) {
		t.Fatalf("work units = %+v", rec.WorkUnits)
	}
	if rec.GateFailures != 1 || rec.GateFailureClasses["signature"] != 1 {
		t.Fatalf("gate failures = %d %+v", rec.GateFailures, rec.GateFailureClasses)
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
