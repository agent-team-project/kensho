package budget

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/origin"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/jamesaud/agent-team/internal/usage"
)

func TestStatusesComputesSlidingTokenWindowAndRunningJobs(t *testing.T) {
	teamDir := testTeamDir(t)
	top := testBudgetTopology(t, 150, 2)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	writeBudgetJob(t, teamDir, "SQU-1", jobstore.StatusDone, "delivery", usage.Record{
		Instance:        "worker-squ-1",
		TokensAvailable: true,
		InputTokens:     100,
		OutputTokens:    20,
		StartedAt:       now.Add(-2 * time.Hour),
		EndedAt:         now.Add(-2 * time.Hour),
	})
	writeBudgetJob(t, teamDir, "SQU-2", jobstore.StatusDone, "delivery", usage.Record{
		Instance:        "worker-squ-2",
		TokensAvailable: true,
		InputTokens:     50,
		OutputTokens:    5,
		StartedAt:       now.Add(-time.Hour),
		EndedAt:         now.Add(-time.Hour),
	})
	writeBudgetJob(t, teamDir, "SQU-OLD", jobstore.StatusDone, "delivery", usage.Record{
		Instance:        "worker-old",
		TokensAvailable: true,
		InputTokens:     900,
		OutputTokens:    100,
		StartedAt:       now.Add(-25 * time.Hour),
		EndedAt:         now.Add(-25 * time.Hour),
	})
	writeBudgetJob(t, teamDir, "SQU-RUN", jobstore.StatusRunning, "delivery", usage.Record{})

	rows, err := Statuses(teamDir, top, now)
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.TokensUsed != 175 || !row.TokensExhausted {
		t.Fatalf("tokens = used:%d exhausted:%v row=%+v", row.TokensUsed, row.TokensExhausted, row)
	}
	wantReset := now.Add(-2 * time.Hour).Add(Window)
	if !row.TokenAvailableAt.Equal(wantReset) {
		t.Fatalf("TokenAvailableAt = %s, want %s", row.TokenAvailableAt, wantReset)
	}
	if row.JobsInFlight != 1 || row.JobsAvailable != 1 {
		t.Fatalf("jobs = in_flight:%d available:%d row=%+v", row.JobsInFlight, row.JobsAvailable, row)
	}
}

func TestAdmissionExcludesCurrentRunningJob(t *testing.T) {
	teamDir := testTeamDir(t)
	top := testBudgetTopology(t, 0, 1)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	writeBudgetJob(t, teamDir, "SQU-1", jobstore.StatusRunning, "delivery", usage.Record{})

	blocked, err := AdmissionForTeam(teamDir, top, "delivery", "squ-2", now)
	if err != nil {
		t.Fatalf("AdmissionForTeam blocked: %v", err)
	}
	if blocked.Allowed || !blocked.JobsExhausted {
		t.Fatalf("blocked admission = %+v, want jobs exhausted", blocked)
	}

	allowed, err := AdmissionForTeam(teamDir, top, "delivery", "squ-1", now)
	if err != nil {
		t.Fatalf("AdmissionForTeam allowed: %v", err)
	}
	if !allowed.Allowed || allowed.JobsExhausted {
		t.Fatalf("allowed admission = %+v, want current job excluded", allowed)
	}
}

func testBudgetTopology(t *testing.T, tokensPerDay int64, jobsInFlight int) *topology.Topology {
	t.Helper()
	body := `
[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["worker"]

[budgets.delivery]
`
	if tokensPerDay > 0 {
		body += "tokens_per_day = " + intString(tokensPerDay) + "\n"
	}
	if jobsInFlight > 0 {
		body += "jobs_in_flight = " + intString(int64(jobsInFlight)) + "\n"
	}
	top, err := topology.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return top
}

func testTeamDir(t *testing.T) string {
	t.Helper()
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return teamDir
}

func writeBudgetJob(t *testing.T, teamDir, ticket string, status jobstore.Status, team string, rec usage.Record) *jobstore.Job {
	t.Helper()
	now := rec.StartedAt
	if now.IsZero() {
		now = time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	}
	j, err := jobstore.New(ticket, "worker", "budget test", now)
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Status = status
	j.Origin = origin.Envelope{Team: team}
	j.Instance = rec.Instance
	if rec.TokensAvailable || rec.Instance != "" {
		rec.Origin = j.Origin
		j.Usage, _ = usage.MergeRecord(nil, rec)
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	return j
}

func intString(v int64) string {
	return strconv.FormatInt(v, 10)
}
