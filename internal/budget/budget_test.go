package budget

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/usage"
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

func TestReserveAllocationGatesOnOutstandingAllowances(t *testing.T) {
	teamDir := testTeamDir(t)
	top := testBudgetTopologyWithAllocation(t, 100, 0, topology.BudgetAllocationReserve)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	first, err := GrantTokens(teamDir, top, GrantRequest{
		Team:     "delivery",
		JobID:    "squ-1",
		Instance: "worker-squ-1",
		Tokens:   60,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if !first.Allowed || first.GrantedTokens != 60 {
		t.Fatalf("first grant = %+v, want 60 allowed", first)
	}
	second, err := GrantTokens(teamDir, top, GrantRequest{
		Team:     "delivery",
		JobID:    "squ-2",
		Instance: "worker-squ-2",
		Tokens:   60,
		Now:      now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("second grant: %v", err)
	}
	if second.Allowed || !second.TokenExhausted {
		t.Fatalf("second grant = %+v, want reserve denial", second)
	}
	rows, err := Statuses(teamDir, top, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(rows) != 1 || rows[0].TokensAllocated != 60 || rows[0].TokensRemaining != 40 {
		t.Fatalf("rows = %+v, want allocated=60 remaining=40", rows)
	}

	j := writeBudgetJob(t, teamDir, "SQU-1", jobstore.StatusDone, "delivery", usage.Record{
		Instance:        "worker-squ-1",
		TokensAvailable: true,
		InputTokens:     30,
		StartedAt:       now,
		EndedAt:         now.Add(time.Minute),
	})
	if _, err := ReleaseJobInstanceAllocations(teamDir, j, "worker-squ-1", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("release: %v", err)
	}
	third, err := GrantTokens(teamDir, top, GrantRequest{
		Team:     "delivery",
		JobID:    "squ-2",
		Instance: "worker-squ-2",
		Tokens:   60,
		Now:      now.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("third grant: %v", err)
	}
	if !third.Allowed {
		t.Fatalf("third grant = %+v, want allowed after release", third)
	}
}

func TestOversubscribeAllocationCanExceedCapWithoutGating(t *testing.T) {
	teamDir := testTeamDir(t)
	top := testBudgetTopologyWithAllocation(t, 100, 0, topology.BudgetAllocationOversubscribe)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	for _, id := range []string{"squ-1", "squ-2"} {
		grant, err := GrantTokens(teamDir, top, GrantRequest{
			Team:     "delivery",
			JobID:    id,
			Instance: "worker-" + id,
			Tokens:   80,
			Now:      now,
		})
		if err != nil {
			t.Fatalf("grant %s: %v", id, err)
		}
		if !grant.Allowed || grant.GrantedTokens != 80 {
			t.Fatalf("grant %s = %+v, want full oversubscribe grant", id, grant)
		}
	}
	rows, err := Statuses(teamDir, top, now)
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(rows) != 1 || rows[0].TokensAllocated != 160 || rows[0].TokensRemaining != 100 || rows[0].TokensExhausted {
		t.Fatalf("rows = %+v, want allocated over cap without exhaustion", rows)
	}
}

func TestConcurrentReserveGrantEnforcesTreeInvariant(t *testing.T) {
	teamDir := testTeamDir(t)
	top := testBudgetTopologyWithAllocation(t, 100, 0, topology.BudgetAllocationReserve)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	var allowed int64
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			grant, err := GrantTokens(teamDir, top, GrantRequest{
				Team:     "delivery",
				JobID:    "squ-" + strconv.Itoa(i),
				Instance: "worker-squ-" + strconv.Itoa(i),
				Tokens:   60,
				Now:      now.Add(time.Duration(i) * time.Nanosecond),
			})
			if err != nil {
				errs <- err
				return
			}
			if grant.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("grant error: %v", err)
	}
	if got := atomic.LoadInt64(&allowed); got != 1 {
		t.Fatalf("allowed grants = %d, want 1", got)
	}
	rows, err := Statuses(teamDir, top, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(rows) != 1 || rows[0].TokensAllocated != 60 {
		t.Fatalf("rows = %+v, want one outstanding 60-token allocation", rows)
	}
}

func TestReleaseAllocationByIDLeavesSiblingsOutstanding(t *testing.T) {
	teamDir := testTeamDir(t)
	top := testBudgetTopologyWithAllocation(t, 100, 0, topology.BudgetAllocationReserve)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	first, err := GrantTokens(teamDir, top, GrantRequest{
		Team:     "delivery",
		JobID:    "squ-1",
		Instance: "worker-squ-1",
		Tokens:   40,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("first grant: %v", err)
	}
	second, err := GrantTokens(teamDir, top, GrantRequest{
		Team:     "delivery",
		JobID:    "squ-1",
		Instance: "worker-squ-1",
		Tokens:   40,
		Now:      now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("second grant: %v", err)
	}
	if first.Allocation == nil || second.Allocation == nil {
		t.Fatalf("grants = %+v %+v, want allocation records", first, second)
	}
	if _, err := ReleaseAllocations(teamDir, ReleaseRequest{ID: second.Allocation.ID, Now: now.Add(2 * time.Second)}); err != nil {
		t.Fatalf("release by id: %v", err)
	}
	rows, err := Statuses(teamDir, top, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(rows) != 1 || rows[0].TokensAllocated != 40 {
		t.Fatalf("rows = %+v, want only first allocation outstanding", rows)
	}
}

func testBudgetTopology(t *testing.T, tokensPerDay int64, jobsInFlight int) *topology.Topology {
	return testBudgetTopologyWithAllocation(t, tokensPerDay, jobsInFlight, "")
}

func testBudgetTopologyWithAllocation(t *testing.T, tokensPerDay int64, jobsInFlight int, allocation string) *topology.Topology {
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
	if allocation != "" {
		body += "allocation = \"" + allocation + "\"\n"
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
