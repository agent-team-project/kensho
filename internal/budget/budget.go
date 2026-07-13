// Package budget evaluates declarative team resource caps from topology and
// finalized job usage records.
package budget

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/usage"
)

// Window is the sliding usage window for tokens_per_day caps.
const Window = 24 * time.Hour

// TeamStatus is one team's current budget position.
type TeamStatus struct {
	Team             string        `json:"team"`
	Allocation       string        `json:"allocation"`
	WindowStart      time.Time     `json:"window_start"`
	WindowEnd        time.Time     `json:"window_end"`
	TokensPerDay     int64         `json:"tokens_per_day,omitempty"`
	TokensUsed       int64         `json:"tokens_used"`
	TokensAllocated  int64         `json:"tokens_allocated,omitempty"`
	TokensRemaining  int64         `json:"tokens_remaining,omitempty"`
	TokensExhausted  bool          `json:"tokens_exhausted,omitempty"`
	TokenAvailableAt time.Time     `json:"token_available_at,omitempty"`
	JobsInFlightCap  int           `json:"jobs_in_flight_cap,omitempty"`
	JobsInFlight     int           `json:"jobs_in_flight"`
	JobsAvailable    int           `json:"jobs_available,omitempty"`
	JobsExhausted    bool          `json:"jobs_exhausted,omitempty"`
	Usage            usage.Summary `json:"usage"`
}

// Admission is the admission-control decision for a prospective dispatch.
type Admission struct {
	Allowed         bool
	Noop            bool
	Team            string
	Status          TeamStatus
	Diagnostics     []InputDiagnostic
	RequestedTokens int64
	TokenExhausted  bool
	JobsExhausted   bool
	NextTokenRetry  time.Time
}

// InputDiagnostic identifies one unrelated persisted job record omitted from
// an explicitly isolated budget calculation.
type InputDiagnostic struct {
	Record string `json:"record"`
	Error  string `json:"error"`
}

// Statuses returns current status rows for every configured budget. Missing
// topology, missing budgets, or teams without budgets produce a strict no-op.
func Statuses(teamDir string, top *topology.Topology, now time.Time) ([]TeamStatus, error) {
	if top == nil || len(top.Budgets) == 0 {
		return nil, nil
	}
	now = normalizeNow(now)
	inputs, err := collectInputs(teamDir, top, now, "", false)
	if err != nil {
		return nil, err
	}
	rows := make([]TeamStatus, 0, len(top.Budgets))
	for _, b := range top.SortedBudgets() {
		rows = append(rows, statusForBudget(b, inputs.recordsByTeam[b.Team], inputs.runningByTeam[b.Team], inputs.allocatedByTeam[b.Team], now))
	}
	return rows, nil
}

// AdmissionForTeam evaluates whether a dispatch owned by team can start now.
// currentJobID is excluded from the jobs_in_flight count so later steps of an
// already-running pipeline job do not block themselves.
func AdmissionForTeam(teamDir string, top *topology.Topology, team, currentJobID string, now time.Time) (Admission, error) {
	return AdmissionForTeamWithRequest(teamDir, top, team, currentJobID, 0, now)
}

// AdmissionForTeamWithRequest evaluates whether a dispatch owned by team can
// start now with requestedTokens of child allowance. Reserve-mode budgets gate
// on consumed + outstanding allocated + requested. Oversubscribe mode preserves
// phase-1 consumption gating and ignores requested allowance for admission.
func AdmissionForTeamWithRequest(teamDir string, top *topology.Topology, team, currentJobID string, requestedTokens int64, now time.Time) (Admission, error) {
	return admissionForTeamWithRequest(teamDir, top, team, currentJobID, requestedTokens, now, false)
}

func admissionForTeamWithRequest(teamDir string, top *topology.Topology, team, currentJobID string, requestedTokens int64, now time.Time, isolateInvalidJobs bool) (Admission, error) {
	team = strings.TrimSpace(team)
	if top == nil || len(top.Budgets) == 0 || team == "" {
		return Admission{Allowed: true, Noop: true, Team: team, RequestedTokens: requestedTokens}, nil
	}
	b := top.FindBudget(team)
	if b == nil {
		return Admission{Allowed: true, Noop: true, Team: team, RequestedTokens: requestedTokens}, nil
	}
	now = normalizeNow(now)
	inputs, err := collectInputs(teamDir, top, now, currentJobID, isolateInvalidJobs)
	if err != nil {
		return Admission{}, err
	}
	status := statusForBudget(b, inputs.recordsByTeam[b.Team], inputs.runningByTeam[b.Team], inputs.allocatedByTeam[b.Team], now)
	tokenExhausted := tokenAdmissionExhausted(b, status, requestedTokens)
	jobsExhausted := b.JobsInFlight > 0 && status.JobsInFlight >= b.JobsInFlight
	return Admission{
		Allowed:         !tokenExhausted && !jobsExhausted,
		Team:            team,
		Status:          status,
		Diagnostics:     inputs.diagnostics,
		RequestedTokens: requestedTokens,
		TokenExhausted:  tokenExhausted,
		JobsExhausted:   jobsExhausted,
		NextTokenRetry:  nextAdmissionRetry(status, tokenExhausted),
	}, nil
}

type inputs struct {
	recordsByTeam   map[string][]usage.Record
	runningByTeam   map[string]int
	allocatedByTeam map[string]int64
	diagnostics     []InputDiagnostic
}

func collectInputs(teamDir string, top *topology.Topology, now time.Time, excludeRunningJobID string, isolateInvalidJobs bool) (inputs, error) {
	out := inputs{recordsByTeam: map[string][]usage.Record{}, runningByTeam: map[string]int{}, allocatedByTeam: map[string]int64{}}
	jobs, diagnostics, err := listBudgetJobs(teamDir, excludeRunningJobID, isolateInvalidJobs)
	if err != nil {
		return out, err
	}
	out.diagnostics = diagnostics
	archived, err := jobstore.ListArchived(teamDir)
	if err != nil {
		return out, err
	}
	windowStart := now.Add(-Window)
	seenRecords := map[string]bool{}
	for _, j := range append(jobs, archived...) {
		if j == nil {
			continue
		}
		if j.Status == jobstore.StatusRunning && jobstore.NormalizeID(j.ID) != jobstore.NormalizeID(excludeRunningJobID) {
			if team := teamForJob(top, j, usage.Record{}); team != "" {
				out.runningByTeam[team]++
			}
		}
		if j.Usage == nil {
			continue
		}
		for _, rec := range j.Usage.Records {
			ts := recordTime(rec)
			if ts.IsZero() || ts.Before(windowStart) {
				continue
			}
			key := j.ID + "|" + usage.RecordKey(rec)
			if seenRecords[key] {
				continue
			}
			seenRecords[key] = true
			team := teamForJob(top, j, rec)
			if team == "" {
				continue
			}
			out.recordsByTeam[team] = append(out.recordsByTeam[team], rec)
		}
	}
	allocations, err := ListAllocations(teamDir)
	if err != nil {
		return out, err
	}
	for _, b := range top.SortedBudgets() {
		out.allocatedByTeam[b.Team] = outstandingTokens(allocations, b.Team)
	}
	return out, nil
}

func listBudgetJobs(teamDir, targetJobID string, isolateInvalid bool) ([]*jobstore.Job, []InputDiagnostic, error) {
	if !isolateInvalid {
		jobs, err := jobstore.List(teamDir)
		return jobs, nil, err
	}
	dir := jobstore.Directory(teamDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	targetJobID = jobstore.NormalizeID(targetJobID)
	jobs := make([]*jobstore.Job, 0, len(entries))
	diagnostics := make([]InputDiagnostic, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".toml")
		j, err := jobstore.Read(teamDir, id)
		if err == nil {
			jobs = append(jobs, j)
			continue
		}
		if targetJobID != "" && jobstore.NormalizeID(id) == targetJobID {
			return nil, nil, err
		}
		diagnostics = append(diagnostics, InputDiagnostic{
			Record: filepath.Join(dir, entry.Name()),
			Error:  err.Error(),
		})
	}
	return jobs, diagnostics, nil
}

func statusForBudget(b *topology.Budget, records []usage.Record, running int, allocated int64, now time.Time) TeamStatus {
	if b == nil {
		return TeamStatus{}
	}
	summary := usage.Summarize(records)
	used := tokensUsed(records)
	row := TeamStatus{
		Team:            b.Team,
		Allocation:      b.Allocation,
		WindowStart:     now.Add(-Window),
		WindowEnd:       now,
		TokensPerDay:    b.TokensPerDay,
		TokensUsed:      used,
		TokensAllocated: allocated,
		JobsInFlightCap: b.JobsInFlight,
		JobsInFlight:    running,
		Usage:           summary,
	}
	if b.TokensPerDay > 0 {
		committed := used
		if b.Allocation == topology.BudgetAllocationReserve {
			committed += allocated
		}
		if committed < b.TokensPerDay {
			row.TokensRemaining = b.TokensPerDay - committed
		} else {
			row.TokensExhausted = true
			if used >= b.TokensPerDay {
				row.TokenAvailableAt = nextTokenAvailableAt(records, b.TokensPerDay, used)
			}
		}
	}
	if b.JobsInFlight > 0 {
		if running < b.JobsInFlight {
			row.JobsAvailable = b.JobsInFlight - running
		} else {
			row.JobsExhausted = true
		}
	}
	return row
}

func tokenAdmissionExhausted(b *topology.Budget, status TeamStatus, requestedTokens int64) bool {
	if b == nil || b.TokensPerDay <= 0 {
		return false
	}
	switch b.Allocation {
	case topology.BudgetAllocationReserve:
		committed := status.TokensUsed + status.TokensAllocated
		if requestedTokens > 0 {
			return committed+requestedTokens > b.TokensPerDay
		}
		return committed >= b.TokensPerDay
	default:
		return status.TokensUsed >= b.TokensPerDay
	}
}

func nextAdmissionRetry(status TeamStatus, exhausted bool) time.Time {
	if !exhausted {
		return time.Time{}
	}
	return status.TokenAvailableAt
}

func tokensUsed(records []usage.Record) int64 {
	var total int64
	for _, rec := range records {
		if !rec.TokensAvailable {
			continue
		}
		total += tokenTotal(rec)
	}
	return total
}

func tokenTotal(rec usage.Record) int64 {
	return rec.InputTokens + rec.OutputTokens
}

func nextTokenAvailableAt(records []usage.Record, cap, used int64) time.Time {
	type timedRecord struct {
		at     time.Time
		tokens int64
	}
	timed := make([]timedRecord, 0, len(records))
	for _, rec := range records {
		tokens := tokenTotal(rec)
		if !rec.TokensAvailable || tokens <= 0 {
			continue
		}
		if at := recordTime(rec); !at.IsZero() {
			timed = append(timed, timedRecord{at: at, tokens: tokens})
		}
	}
	sort.Slice(timed, func(i, j int) bool { return timed[i].at.Before(timed[j].at) })
	remaining := used
	for _, rec := range timed {
		remaining -= rec.tokens
		if remaining < cap {
			return rec.at.Add(Window)
		}
	}
	return time.Time{}
}

func recordTime(rec usage.Record) time.Time {
	for _, ts := range []time.Time{rec.EndedAt, rec.CapturedAt, rec.StartedAt} {
		if !ts.IsZero() {
			return ts.UTC()
		}
	}
	return time.Time{}
}

func normalizeNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func teamForJob(top *topology.Topology, j *jobstore.Job, rec usage.Record) string {
	if team := strings.TrimSpace(rec.Origin.Team); team != "" {
		return team
	}
	if j == nil {
		return ""
	}
	if team := strings.TrimSpace(j.Origin.Team); team != "" {
		return team
	}
	if team := topologyTeamForPipeline(top, j.Pipeline); team != "" {
		return team
	}
	if team := topologyTeamForInstance(top, j.Instance); team != "" {
		return team
	}
	return topologyTeamForInstance(top, j.Target)
}

func topologyTeamForPipeline(top *topology.Topology, pipeline string) string {
	pipeline = strings.TrimSpace(pipeline)
	if top == nil || pipeline == "" {
		return ""
	}
	for _, team := range top.SortedTeams() {
		for _, name := range team.Pipelines {
			if strings.TrimSpace(name) == pipeline {
				return team.Name
			}
		}
	}
	return ""
}

func topologyTeamForInstance(top *topology.Topology, instance string) string {
	instance = strings.TrimSpace(instance)
	if top == nil || instance == "" {
		return ""
	}
	for _, team := range top.SortedTeams() {
		for _, name := range team.Instances {
			name = strings.TrimSpace(name)
			if instance == name || strings.HasPrefix(instance, name+"-") {
				return team.Name
			}
		}
	}
	return ""
}
