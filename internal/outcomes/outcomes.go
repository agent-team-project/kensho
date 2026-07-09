// Package outcomes records terminal job outcomes and renders aggregate trends.
package outcomes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/allowance"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/usage"
)

const recordVersion = 1

var bounceHeadingRE = regexp.MustCompile(`(?m)^## Review findings \(bounce ([0-9]+)\)\s*$`)

// Record is the materialized outcome for one terminal durable job.
type Record struct {
	Version                  int              `json:"version"`
	JobID                    string           `json:"job_id"`
	Ticket                   string           `json:"ticket,omitempty"`
	TicketURL                string           `json:"ticket_url,omitempty"`
	Epic                     string           `json:"epic,omitempty"`
	PR                       string           `json:"pr,omitempty"`
	Pipeline                 string           `json:"pipeline,omitempty"`
	Team                     string           `json:"team,omitempty"`
	Agent                    string           `json:"agent,omitempty"`
	Runtime                  string           `json:"runtime,omitempty"`
	Model                    string           `json:"model,omitempty"`
	Tier                     string           `json:"tier,omitempty"`
	Status                   string           `json:"status"`
	Week                     string           `json:"week,omitempty"`
	CreatedAt                time.Time        `json:"created_at,omitempty"`
	FinalizedAt              time.Time        `json:"finalized_at,omitempty"`
	TerminalEvent            string           `json:"terminal_event,omitempty"`
	MergedAt                 time.Time        `json:"merged_at,omitempty"`
	TimeToTerminalMS         int64            `json:"time_to_terminal_ms,omitempty"`
	TimeToMergeMS            int64            `json:"time_to_merge_ms,omitempty"`
	ReviewRounds             int              `json:"review_rounds"`
	BounceCount              int              `json:"bounce_count"`
	BounceClasses            map[string]int   `json:"bounce_classes,omitempty"`
	Bounces                  []BounceRecord   `json:"bounces,omitempty"`
	WatchdogEvents           []EventRef       `json:"watchdog_events,omitempty"`
	BudgetNoticeEvents       []EventRef       `json:"budget_notice_events,omitempty"`
	BudgetExceededEvents     []EventRef       `json:"budget_exceeded_events,omitempty"`
	StepRuns                 []StepRunRecord  `json:"step_runs,omitempty"`
	WorkUnits                []WorkUnitRecord `json:"work_units,omitempty"`
	WorkUnitsExhaustive      bool             `json:"work_units_exhaustive,omitempty"`
	TokenBudget              int64            `json:"token_budget,omitempty"`
	TokensAllocated          int64            `json:"tokens_allocated,omitempty"`
	TokensConsumed           int64            `json:"tokens_consumed,omitempty"`
	TokensReleased           int64            `json:"tokens_released,omitempty"`
	TokenBudgetRatio         float64          `json:"token_budget_ratio,omitempty"`
	TimeBudget               string           `json:"time_budget,omitempty"`
	TimeBudgetMS             int64            `json:"time_budget_ms,omitempty"`
	RuntimeDurationMS        int64            `json:"runtime_duration_ms,omitempty"`
	TimeBudgetRatio          float64          `json:"time_budget_ratio,omitempty"`
	Usage                    usage.Summary    `json:"usage,omitempty"`
	GateFailures             int              `json:"gate_failures,omitempty"`
	GateFailureClasses       map[string]int   `json:"gate_failure_classes,omitempty"`
	PostMergeDefectBacklinks []DefectBacklink `json:"post_merge_defect_backlinks,omitempty"`
	RecordedAt               time.Time        `json:"recorded_at"`
}

// BounceRecord describes one review bounce section captured in the job kickoff.
type BounceRecord struct {
	Number  int      `json:"number"`
	Classes []string `json:"classes,omitempty"`
}

// StepRunRecord captures the runtime/model/tier binding for one progressed
// pipeline step in a job.
type StepRunRecord struct {
	ID             string    `json:"id,omitempty"`
	Target         string    `json:"target,omitempty"`
	Agent          string    `json:"agent,omitempty"`
	Instance       string    `json:"instance,omitempty"`
	Runtime        string    `json:"runtime,omitempty"`
	RuntimeBinary  string    `json:"runtime_binary,omitempty"`
	Model          string    `json:"model,omitempty"`
	Tier           string    `json:"tier,omitempty"`
	Status         string    `json:"status,omitempty"`
	Attempts       int       `json:"attempts,omitempty"`
	TokenBudget    int64     `json:"token_budget,omitempty"`
	TokensConsumed int64     `json:"tokens_consumed,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	FinishedAt     time.Time `json:"finished_at,omitempty"`
}

// EventRef is the compact event shape persisted on outcome records.
type EventRef struct {
	TS      time.Time         `json:"ts,omitempty"`
	Type    string            `json:"type"`
	Message string            `json:"message,omitempty"`
	Data    map[string]string `json:"data,omitempty"`
}

// WorkUnitRecord captures one runtime usage interval used for
// effective-concurrency reporting. It records runtime work, not queue wait.
type WorkUnitRecord struct {
	ID         string    `json:"id,omitempty"`
	Target     string    `json:"target,omitempty"`
	Instance   string    `json:"instance,omitempty"`
	Status     string    `json:"status,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// DefectBacklink reserves the ledger surface for later fix jobs that cite this
// job's merged PR. Current records are written at finalization, so these links
// are populated by future report/rebuild paths rather than invented eagerly.
type DefectBacklink struct {
	JobID string `json:"job_id"`
	PR    string `json:"pr,omitempty"`
}

// ReportOptions controls outcome trend rendering.
type ReportOptions struct {
	Since   time.Time
	Team    string
	Agent   string
	ByEpic  bool
	TeamDir string
	Now     time.Time
}

// Report is the structured output for `agent-team outcomes report`.
type Report struct {
	GeneratedAt     time.Time        `json:"generated_at"`
	Since           time.Time        `json:"since,omitempty"`
	ByEpic          bool             `json:"by_epic,omitempty"`
	Rows            []TrendRow       `json:"rows"`
	Summary         TrendRow         `json:"summary"`
	ModelTierRows   []ModelTierRow   `json:"model_tier_rows,omitempty"`
	BounceClassRows []BounceClassRow `json:"bounce_class_rows,omitempty"`
}

// TrendRow aggregates terminal outcomes by week, team, and agent.
type TrendRow struct {
	Week                    string         `json:"week,omitempty"`
	Epic                    string         `json:"epic,omitempty"`
	Team                    string         `json:"team,omitempty"`
	Agent                   string         `json:"agent,omitempty"`
	Jobs                    int            `json:"jobs"`
	Done                    int            `json:"done,omitempty"`
	Failed                  int            `json:"failed,omitempty"`
	Merged                  int            `json:"merged,omitempty"`
	EffectiveConcurrency    float64        `json:"effective_concurrency,omitempty"`
	PeakConcurrentWorkUnits int            `json:"peak_concurrent_work_units,omitempty"`
	DeclaredReplicaCapacity int            `json:"declared_replica_capacity,omitempty"`
	ConcurrencyUtilization  float64        `json:"concurrency_utilization,omitempty"`
	ReviewRounds            int            `json:"review_rounds,omitempty"`
	AverageReviewRounds     float64        `json:"average_review_rounds,omitempty"`
	Bounces                 int            `json:"bounces,omitempty"`
	AverageBounces          float64        `json:"average_bounces,omitempty"`
	BounceClasses           map[string]int `json:"bounce_classes,omitempty"`
	ModelTiers              map[string]int `json:"model_tiers,omitempty"`
	WatchdogEvents          int            `json:"watchdog_events,omitempty"`
	BudgetNoticeEvents      int            `json:"budget_notice_events,omitempty"`
	BudgetExceededEvents    int            `json:"budget_exceeded_events,omitempty"`
	TokenBudget             int64          `json:"token_budget,omitempty"`
	TokensConsumed          int64          `json:"tokens_consumed,omitempty"`
	TokenBudgetRatio        float64        `json:"token_budget_ratio,omitempty"`
	EpicAllocation          int64          `json:"epic_allocation,omitempty"`
	EpicAllocationRatio     float64        `json:"epic_allocation_ratio,omitempty"`
	RuntimeDurationMS       int64          `json:"runtime_duration_ms,omitempty"`
	AverageTimeToMergeMS    int64          `json:"average_time_to_merge_ms,omitempty"`
	AverageTimeToTerminalMS int64          `json:"average_time_to_terminal_ms,omitempty"`

	timeToMergeSamples    int
	timeToTerminalSamples int
	workIntervals         []workInterval
}

// ModelTierRow aggregates jobs by their primary implementation model/tier.
type ModelTierRow struct {
	Week                string  `json:"week,omitempty"`
	Epic                string  `json:"epic,omitempty"`
	Team                string  `json:"team,omitempty"`
	Agent               string  `json:"agent,omitempty"`
	Runtime             string  `json:"runtime,omitempty"`
	Model               string  `json:"model,omitempty"`
	Tier                string  `json:"tier,omitempty"`
	Jobs                int     `json:"jobs"`
	Done                int     `json:"done,omitempty"`
	Failed              int     `json:"failed,omitempty"`
	Bounces             int     `json:"bounces,omitempty"`
	AverageBounces      float64 `json:"average_bounces,omitempty"`
	ReviewRounds        int     `json:"review_rounds,omitempty"`
	AverageReviewRounds float64 `json:"average_review_rounds,omitempty"`
	TokenBudget         int64   `json:"token_budget,omitempty"`
	TokensConsumed      int64   `json:"tokens_consumed,omitempty"`
	TokenBudgetRatio    float64 `json:"token_budget_ratio,omitempty"`
}

// BounceClassRow aggregates bounce counts by model-economy bounce class.
type BounceClassRow struct {
	Week           string  `json:"week,omitempty"`
	Epic           string  `json:"epic,omitempty"`
	Team           string  `json:"team,omitempty"`
	Agent          string  `json:"agent,omitempty"`
	Class          string  `json:"class"`
	Jobs           int     `json:"jobs"`
	Done           int     `json:"done,omitempty"`
	Failed         int     `json:"failed,omitempty"`
	Bounces        int     `json:"bounces"`
	AverageBounces float64 `json:"average_bounces,omitempty"`
}

// Directory returns the root outcomes directory for a team.
func Directory(teamDir string) string {
	return filepath.Join(teamDir, "outcomes")
}

// JobsDirectory returns the per-job outcome directory.
func JobsDirectory(teamDir string) string {
	return filepath.Join(Directory(teamDir), "jobs")
}

// RecordPath returns the JSON path for one job outcome record.
func RecordPath(teamDir, rawID string) string {
	return filepath.Join(JobsDirectory(teamDir), jobstore.IDFromInput(rawID)+".json")
}

// RecordFinalizedJob writes or refreshes the outcome record for a terminal job.
func RecordFinalizedJob(teamDir string, j *jobstore.Job) (*Record, bool, error) {
	if j == nil || !jobstore.IsTerminalStatus(j.Status) {
		return nil, false, nil
	}
	rec, err := BuildRecord(teamDir, j, time.Now().UTC())
	if err != nil {
		return nil, false, err
	}
	if err := WriteRecord(teamDir, rec); err != nil {
		return nil, false, err
	}
	return rec, true, nil
}

// BuildRecord derives a terminal job outcome from durable job state and events.
func BuildRecord(teamDir string, j *jobstore.Job, now time.Time) (*Record, error) {
	if j == nil {
		return nil, errors.New("outcomes: job is nil")
	}
	if !jobstore.IsTerminalStatus(j.Status) {
		return nil, fmt.Errorf("outcomes: job %s is not terminal", j.ID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	events, err := jobstore.ListEvents(teamDir, j.ID)
	if err != nil {
		return nil, err
	}
	gates, err := jobstore.ListGateRecords(teamDir, j.ID)
	if err != nil {
		return nil, err
	}
	finalizedAt, terminalEvent := terminalEvent(events, j)
	mergedAt := mergedEventTime(events)
	weekAt := finalizedAt
	if weekAt.IsZero() {
		weekAt = j.UpdatedAt
	}
	rec := &Record{
		Version:           recordVersion,
		JobID:             j.ID,
		Ticket:            j.Ticket,
		TicketURL:         j.TicketURL,
		Epic:              jobstore.EpicForJob(j),
		PR:                j.PR,
		Pipeline:          j.Pipeline,
		Team:              teamForJob(teamDir, j),
		Agent:             agentForJob(j),
		Status:            string(j.Status),
		Week:              WeekKey(weekAt),
		CreatedAt:         utcOrZero(j.CreatedAt),
		FinalizedAt:       utcOrZero(finalizedAt),
		TerminalEvent:     terminalEvent,
		MergedAt:          utcOrZero(mergedAt),
		TokenBudget:       tokenBudget(j),
		TimeBudget:        timeBudgetString(j),
		TimeBudgetMS:      timeBudgetMS(j),
		Usage:             usageSummary(j),
		RuntimeDurationMS: runtimeDurationMS(j),
		RecordedAt:        now.UTC(),
	}
	rec.TokensConsumed = rec.Usage.InputTokens + rec.Usage.OutputTokens
	rec.TokenBudgetRatio = ratio(rec.TokensConsumed, rec.TokenBudget)
	rec.TimeBudgetRatio = ratio(rec.RuntimeDurationMS, rec.TimeBudgetMS)
	rec.TimeToTerminalMS = durationMS(j.CreatedAt, finalizedAt)
	rec.TimeToMergeMS = durationMS(j.CreatedAt, mergedAt)
	rec.Bounces = parseBounces(j.Kickoff)
	rec.BounceCount = len(rec.Bounces)
	rec.BounceClasses = countBounceClasses(rec.Bounces)
	rec.ReviewRounds = reviewRounds(j, rec.BounceCount)
	rec.WatchdogEvents = selectEvents(events, isWatchdogEvent)
	rec.BudgetNoticeEvents = selectEvents(events, isBudgetNoticeEvent)
	rec.BudgetExceededEvents = selectEvents(events, isBudgetExceededEvent)
	rec.StepRuns = stepRunsForJob(teamDir, j, rec.Agent)
	rec.Runtime, rec.Model, rec.Tier = primaryRunBinding(rec.StepRuns, rec.Agent)
	rec.WorkUnits = workUnitsForJob(j, rec.Agent)
	rec.WorkUnitsExhaustive = true
	var budgetConsumed int64
	rec.TokensAllocated, budgetConsumed, rec.TokensReleased = budgetAllocationTotals(events)
	if rec.TokensConsumed == 0 && budgetConsumed > 0 {
		rec.TokensConsumed = budgetConsumed
		rec.TokenBudgetRatio = ratio(rec.TokensConsumed, rec.TokenBudget)
	}
	if rec.TokenBudget == 0 && rec.TokensAllocated > 0 {
		rec.TokenBudget = rec.TokensAllocated
		rec.TokenBudgetRatio = ratio(rec.TokensConsumed, rec.TokenBudget)
	}
	rec.GateFailures, rec.GateFailureClasses = gateFailureSummary(gates)
	return rec, nil
}

// WriteRecord stores one outcome record atomically.
func WriteRecord(teamDir string, rec *Record) error {
	if rec == nil {
		return errors.New("outcomes: record is nil")
	}
	rec.JobID = jobstore.IDFromInput(rec.JobID)
	if rec.JobID == "" {
		return errors.New("outcomes: job_id is required")
	}
	if rec.Version == 0 {
		rec.Version = recordVersion
	}
	dir := JobsDirectory(teamDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("outcomes: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, rec.JobID+"-*.json.tmp")
	if err != nil {
		return fmt.Errorf("outcomes: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("outcomes: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("outcomes: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("outcomes: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), RecordPath(teamDir, rec.JobID)); err != nil {
		return fmt.Errorf("outcomes: rename: %w", err)
	}
	return nil
}

// ReadRecord loads one persisted outcome record.
func ReadRecord(teamDir, rawID string) (*Record, error) {
	path := RecordPath(teamDir, rawID)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rec Record
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		return nil, fmt.Errorf("outcomes %s: %w", jobstore.IDFromInput(rawID), err)
	}
	return &rec, nil
}

// LoadRecords reads every persisted job outcome record in stable order.
func LoadRecords(teamDir string) ([]Record, error) {
	entries, err := os.ReadDir(JobsDirectory(teamDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		rec, err := ReadRecord(teamDir, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].JobID < out[j].JobID
	})
	return out, nil
}

// BuildReport aggregates persisted outcome records into trend rows.
func BuildReport(records []Record, opts ReportOptions) Report {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := Report{GeneratedAt: now.UTC(), Since: utcOrZero(opts.Since), ByEpic: opts.ByEpic}
	byKey := map[string]*TrendRow{}
	byModelTier := map[string]*ModelTierRow{}
	byBounceClass := map[string]*BounceClassRow{}
	capacities := declaredReplicaCapacities(opts.TeamDir)
	summaryCapacityKeys := map[string]bool{}
	allocations := epicAllocations(opts.TeamDir)
	summaryAllocationKeys := map[string]bool{}
	for _, rec := range records {
		if !recordMatches(rec, opts) {
			continue
		}
		key := trendKey(rec, opts)
		row := byKey[key]
		if row == nil {
			row = trendRowForRecord(rec, opts, capacities, allocations)
			byKey[key] = row
		}
		if !opts.ByEpic {
			if cap := replicaCapacityFor(capacities, rec.Team, rec.Agent); cap > 0 {
				capKey := capacityKey(rec.Team, rec.Agent)
				if !summaryCapacityKeys[capKey] {
					summaryCapacityKeys[capKey] = true
					report.Summary.DeclaredReplicaCapacity += cap
				}
			}
		}
		if opts.ByEpic {
			epic := strings.TrimSpace(rec.Epic)
			if allocation := allocations[epic]; allocation > 0 {
				if !summaryAllocationKeys[epic] {
					summaryAllocationKeys[epic] = true
					report.Summary.EpicAllocation += allocation
				}
			}
		}
		row.add(rec)
		report.Summary.add(rec)
		addModelTierReportRow(byModelTier, rec, opts)
		addBounceClassReportRows(byBounceClass, rec, opts)
	}
	report.Rows = make([]TrendRow, 0, len(byKey))
	for _, row := range byKey {
		row.finalize()
		report.Rows = append(report.Rows, *row)
	}
	sort.SliceStable(report.Rows, func(i, j int) bool {
		if opts.ByEpic {
			return report.Rows[i].Epic < report.Rows[j].Epic
		}
		if report.Rows[i].Week != report.Rows[j].Week {
			return report.Rows[i].Week < report.Rows[j].Week
		}
		if report.Rows[i].Team != report.Rows[j].Team {
			return report.Rows[i].Team < report.Rows[j].Team
		}
		return report.Rows[i].Agent < report.Rows[j].Agent
	})
	report.Summary.finalize()
	report.ModelTierRows = sortedModelTierRows(byModelTier, opts)
	report.BounceClassRows = sortedBounceClassRows(byBounceClass, opts)
	return report
}

func trendRowForRecord(rec Record, opts ReportOptions, capacities map[string]int, allocations map[string]int64) *TrendRow {
	if opts.ByEpic {
		row := &TrendRow{Epic: strings.TrimSpace(rec.Epic)}
		row.EpicAllocation = allocations[row.Epic]
		return row
	}
	row := &TrendRow{Week: rec.Week, Team: rec.Team, Agent: rec.Agent}
	row.DeclaredReplicaCapacity = replicaCapacityFor(capacities, rec.Team, rec.Agent)
	return row
}

func addModelTierReportRow(rows map[string]*ModelTierRow, rec Record, opts ReportOptions) {
	bindingKey := modelTierKey(rec.Runtime, rec.Model, rec.Tier)
	if bindingKey == "" {
		return
	}
	key := reportScopeKey(rec, opts) + "\x00" + bindingKey
	row := rows[key]
	if row == nil {
		row = &ModelTierRow{
			Week:    rec.Week,
			Epic:    strings.TrimSpace(rec.Epic),
			Team:    rec.Team,
			Agent:   rec.Agent,
			Runtime: strings.TrimSpace(rec.Runtime),
			Model:   strings.TrimSpace(rec.Model),
			Tier:    strings.TrimSpace(rec.Tier),
		}
		rows[key] = row
	}
	row.Jobs++
	switch rec.Status {
	case string(jobstore.StatusDone):
		row.Done++
	case string(jobstore.StatusFailed):
		row.Failed++
	}
	row.Bounces += rec.BounceCount
	row.ReviewRounds += rec.ReviewRounds
	row.TokenBudget += rec.TokenBudget
	row.TokensConsumed += rec.TokensConsumed
}

func addBounceClassReportRows(rows map[string]*BounceClassRow, rec Record, opts ReportOptions) {
	for class, count := range rec.BounceClasses {
		class = strings.TrimSpace(class)
		if class == "" || count <= 0 {
			continue
		}
		key := reportScopeKey(rec, opts) + "\x00" + class
		row := rows[key]
		if row == nil {
			row = &BounceClassRow{
				Week:  rec.Week,
				Epic:  strings.TrimSpace(rec.Epic),
				Team:  rec.Team,
				Agent: rec.Agent,
				Class: class,
			}
			rows[key] = row
		}
		row.Jobs++
		switch rec.Status {
		case string(jobstore.StatusDone):
			row.Done++
		case string(jobstore.StatusFailed):
			row.Failed++
		}
		row.Bounces += count
	}
}

func reportScopeKey(rec Record, opts ReportOptions) string {
	if opts.ByEpic {
		return strings.TrimSpace(rec.Epic)
	}
	return rec.Week + "\x00" + rec.Team + "\x00" + rec.Agent
}

func sortedModelTierRows(rows map[string]*ModelTierRow, opts ReportOptions) []ModelTierRow {
	if len(rows) == 0 {
		return nil
	}
	out := make([]ModelTierRow, 0, len(rows))
	for _, row := range rows {
		row.finalize()
		out = append(out, *row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return modelTierRowSortKey(out[i], opts) < modelTierRowSortKey(out[j], opts)
	})
	return out
}

func sortedBounceClassRows(rows map[string]*BounceClassRow, opts ReportOptions) []BounceClassRow {
	if len(rows) == 0 {
		return nil
	}
	out := make([]BounceClassRow, 0, len(rows))
	for _, row := range rows {
		row.finalize()
		out = append(out, *row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return bounceClassRowSortKey(out[i], opts) < bounceClassRowSortKey(out[j], opts)
	})
	return out
}

func modelTierRowSortKey(row ModelTierRow, opts ReportOptions) string {
	scope := row.Week + "\x00" + row.Team + "\x00" + row.Agent
	if opts.ByEpic {
		scope = row.Epic
	}
	return scope + "\x00" + row.Runtime + "\x00" + row.Model + "\x00" + row.Tier
}

func bounceClassRowSortKey(row BounceClassRow, opts ReportOptions) string {
	scope := row.Week + "\x00" + row.Team + "\x00" + row.Agent
	if opts.ByEpic {
		scope = row.Epic
	}
	return scope + "\x00" + row.Class
}

// WeekKey returns an ISO week key for trend grouping.
func WeekKey(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	year, week := ts.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

func parseBounces(kickoff string) []BounceRecord {
	matches := bounceHeadingRE.FindAllStringSubmatchIndex(kickoff, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]BounceRecord, 0, len(matches))
	for i, match := range matches {
		n := 0
		if len(match) >= 4 && match[2] >= 0 {
			n, _ = strconv.Atoi(kickoff[match[2]:match[3]])
		}
		start := match[1]
		end := len(kickoff)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		out = append(out, BounceRecord{Number: n, Classes: classifyBounce(kickoff[start:end])})
	}
	return out
}

func classifyBounce(text string) []string {
	lower := strings.ToLower(text)
	if explicit := explicitBounceClasses(lower); len(explicit) > 0 {
		return explicit
	}
	rules := []struct {
		class string
		keys  []string
	}{
		{"infra", []string{"infra", "flake", "flaky", "timeout", "rate limit", "credential", "auth", "network", "no space", "ci unavailable", "base drift", "runner", "environment"}},
		{"spec-ambiguity", []string{"spec ambiguity", "spec-ambiguity", "ambiguous", "ambiguity", "intent", "clarify", "not what was meant", "underspecified", "under-specified", "vague", "question"}},
		{"scope", []string{"scope", "sprawl", "drive-by", "unrelated", "oversized", "split the ticket", "split ticket", "multiple concerns", "too broad", "out of scope", "owned path"}},
		{"capability", []string{"capability", "logic error", "edge case", "misapplied", "missed", "missing test", "shallow test", "didn't understand", "did not understand", "incorrect", "wrong", "bug", "regression", "behavior", "requirement", "acceptance", "failed to", "doesn't", "does not"}},
	}
	var classes []string
	for _, rule := range rules {
		for _, key := range rule.keys {
			if strings.Contains(lower, key) {
				classes = append(classes, rule.class)
				break
			}
		}
	}
	if len(classes) == 0 {
		classes = append(classes, "unknown")
	}
	return classes
}

func explicitBounceClasses(lower string) []string {
	known := []string{"capability", "spec-ambiguity", "scope", "infra"}
	var out []string
	for _, line := range strings.Split(lower, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "class") {
			continue
		}
		for _, class := range known {
			if strings.Contains(line, class) || class == "spec-ambiguity" && strings.Contains(line, "spec ambiguity") {
				out = appendUniqueString(out, class)
			}
		}
	}
	return out
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func countBounceClasses(bounces []BounceRecord) map[string]int {
	if len(bounces) == 0 {
		return nil
	}
	out := map[string]int{}
	for _, bounce := range bounces {
		for _, class := range bounce.Classes {
			class = strings.TrimSpace(class)
			if class != "" {
				out[class]++
			}
		}
	}
	return out
}

type usageRollup struct {
	runtime  string
	tokens   int64
	started  time.Time
	finished time.Time
}

func stepRunsForJob(teamDir string, j *jobstore.Job, primaryAgent string) []StepRunRecord {
	if j == nil {
		return nil
	}
	top, _ := topology.LoadFromTeamDir(teamDir)
	defaultRuntime := defaultRuntimeForTeam(teamDir)
	byInstance, byAgent := usageRollups(j)
	out := make([]StepRunRecord, 0, len(j.Steps))
	for _, step := range j.Steps {
		if !stepProgressed(step) {
			continue
		}
		inst := topologyInstanceForStep(top, step)
		rollup := usageRollupForStep(step, inst, byInstance, byAgent)
		runtime := firstNonEmpty(step.Runtime, rollup.runtime, instanceRuntime(inst), defaultRuntime)
		model := strings.TrimSpace(instanceModel(inst))
		tier := tierForModel(model)
		agent := strings.TrimSpace(instanceAgent(inst))
		if agent == "" {
			agent = strings.TrimSpace(step.Target)
		}
		if tier == "" {
			tier = tierForUnit(step.Target, agent, runtime)
		}
		if model == "" {
			model = modelForRuntime(runtime)
		}
		out = append(out, StepRunRecord{
			ID:             strings.TrimSpace(step.ID),
			Target:         strings.TrimSpace(step.Target),
			Agent:          agent,
			Instance:       strings.TrimSpace(step.Instance),
			Runtime:        runtime,
			RuntimeBinary:  firstNonEmpty(step.RuntimeBin, instanceRuntimeBin(inst)),
			Model:          model,
			Tier:           tier,
			Status:         string(step.Status),
			Attempts:       step.Attempts,
			TokenBudget:    step.TokenBudget,
			TokensConsumed: rollup.tokens,
			StartedAt:      stepRunStart(step, rollup),
			FinishedAt:     stepRunFinish(step, rollup),
		})
	}
	if len(out) == 0 && j.Usage != nil {
		for _, rec := range j.Usage.Records {
			if !usage.RecordUseful(rec) {
				continue
			}
			runtime := firstNonEmpty(rec.Runtime, defaultRuntime)
			agent := strings.TrimSpace(rec.Agent)
			out = append(out, StepRunRecord{
				ID:             usage.RecordKey(rec),
				Target:         firstNonEmpty(agent, primaryAgent),
				Agent:          agent,
				Instance:       strings.TrimSpace(rec.Instance),
				Runtime:        runtime,
				Model:          modelForRuntime(runtime),
				Tier:           tierForUnit(primaryAgent, agent, runtime),
				TokensConsumed: rec.InputTokens + rec.OutputTokens,
				StartedAt:      utcOrZero(rec.StartedAt),
				FinishedAt:     utcOrZero(rec.EndedAt),
			})
		}
	}
	return out
}

func usageRollups(j *jobstore.Job) (map[string]usageRollup, map[string]usageRollup) {
	byInstance := map[string]usageRollup{}
	byAgent := map[string]usageRollup{}
	if j == nil || j.Usage == nil {
		return byInstance, byAgent
	}
	for _, rec := range j.Usage.Records {
		rollup := usageRollup{
			runtime:  strings.TrimSpace(rec.Runtime),
			tokens:   rec.InputTokens + rec.OutputTokens,
			started:  utcOrZero(rec.StartedAt),
			finished: utcOrZero(rec.EndedAt),
		}
		if instance := strings.TrimSpace(rec.Instance); instance != "" {
			byInstance[instance] = mergeUsageRollup(byInstance[instance], rollup)
		}
		if agent := strings.TrimSpace(rec.Agent); agent != "" {
			byAgent[agent] = mergeUsageRollup(byAgent[agent], rollup)
		}
	}
	return byInstance, byAgent
}

func mergeUsageRollup(a, b usageRollup) usageRollup {
	if a.runtime == "" {
		a.runtime = b.runtime
	}
	a.tokens += b.tokens
	if a.started.IsZero() || !b.started.IsZero() && b.started.Before(a.started) {
		a.started = b.started
	}
	if a.finished.IsZero() || b.finished.After(a.finished) {
		a.finished = b.finished
	}
	return a
}

func usageRollupForStep(step jobstore.Step, inst *topology.Instance, byInstance, byAgent map[string]usageRollup) usageRollup {
	if step.Instance != "" {
		if rollup, ok := byInstance[strings.TrimSpace(step.Instance)]; ok {
			return rollup
		}
	}
	if inst != nil && strings.TrimSpace(inst.Agent) != "" {
		if rollup, ok := byAgent[strings.TrimSpace(inst.Agent)]; ok {
			return rollup
		}
	}
	if step.Target != "" {
		if rollup, ok := byAgent[strings.TrimSpace(step.Target)]; ok {
			return rollup
		}
	}
	return usageRollup{}
}

func stepProgressed(step jobstore.Step) bool {
	if step.Attempts > 0 || strings.TrimSpace(step.Instance) != "" {
		return true
	}
	if !step.RunningAt.IsZero() || !step.FinishedAt.IsZero() {
		return true
	}
	switch step.Status {
	case jobstore.StatusRunning, jobstore.StatusDone, jobstore.StatusFailed:
		return true
	default:
		return false
	}
}

func topologyInstanceForStep(top *topology.Topology, step jobstore.Step) *topology.Instance {
	if top == nil {
		return nil
	}
	if instance := strings.TrimSpace(step.Instance); instance != "" {
		if inst := top.FindRuntimeInstance(instance, strings.TrimSpace(step.Target)); inst != nil {
			return inst
		}
	}
	if target := strings.TrimSpace(step.Target); target != "" {
		if inst := top.FindRuntimeInstance(target, ""); inst != nil {
			return inst
		}
		if inst := top.Find(target); inst != nil {
			return inst
		}
	}
	return nil
}

func instanceRuntime(inst *topology.Instance) string {
	if inst == nil {
		return ""
	}
	return strings.TrimSpace(inst.Runtime)
}

func instanceRuntimeBin(inst *topology.Instance) string {
	if inst == nil {
		return ""
	}
	return strings.TrimSpace(inst.RuntimeBin)
}

func instanceModel(inst *topology.Instance) string {
	if inst == nil {
		return ""
	}
	return strings.TrimSpace(inst.Model)
}

func instanceAgent(inst *topology.Instance) string {
	if inst == nil {
		return ""
	}
	return strings.TrimSpace(inst.Agent)
}

func stepRunStart(step jobstore.Step, rollup usageRollup) time.Time {
	if !rollup.started.IsZero() {
		return rollup.started
	}
	if !step.RunningAt.IsZero() {
		return utcOrZero(step.RunningAt)
	}
	return utcOrZero(step.StartedAt)
}

func stepRunFinish(step jobstore.Step, rollup usageRollup) time.Time {
	if !rollup.finished.IsZero() {
		return rollup.finished
	}
	return utcOrZero(step.FinishedAt)
}

func primaryRunBinding(runs []StepRunRecord, primaryAgent string) (runtime, model, tier string) {
	for _, run := range runs {
		if strings.EqualFold(run.ID, "implement") ||
			primaryAgent != "" && (run.Target == primaryAgent || run.Agent == primaryAgent) {
			return run.Runtime, run.Model, run.Tier
		}
	}
	for _, run := range runs {
		if run.Runtime != "" || run.Model != "" || run.Tier != "" {
			return run.Runtime, run.Model, run.Tier
		}
	}
	return "", "", ""
}

func defaultRuntimeForTeam(teamDir string) string {
	path := filepath.Join(teamDir, "config.toml")
	var cfg map[string]any
	if _, err := toml.DecodeFile(path, &cfg); err == nil {
		if runtimeCfg, ok := anyMap(cfg["runtime"]); ok {
			if kind := anyString(runtimeCfg["kind"]); kind != "" {
				return kind
			}
		}
	}
	return "claude"
}

func tierForModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "claude-fable-5":
		return "T0"
	case "claude-opus-4-8":
		return "T1"
	case "claude-sonnet-5":
		return "T2"
	case "claude-haiku-4-5":
		return "T3"
	default:
		return ""
	}
}

func tierForUnit(target, agent, runtime string) string {
	haystack := strings.ToLower(strings.TrimSpace(target) + " " + strings.TrimSpace(agent))
	switch {
	case strings.Contains(haystack, "advisor") || strings.Contains(haystack, "org-review"):
		return "T0"
	case strings.Contains(haystack, "reviewer") || strings.Contains(haystack, "manager") || strings.Contains(haystack, "harness-review"):
		return "T1"
	case strings.Contains(haystack, "verifier") || strings.Contains(haystack, "ticket-manager") || strings.Contains(haystack, "sentinel") || strings.Contains(haystack, "product-verify"):
		return "T3"
	case strings.Contains(haystack, "worker") || strings.Contains(haystack, "docs") || strings.Contains(haystack, "comms") || strings.Contains(haystack, "feedback") || strings.Contains(haystack, "auditor"):
		return "T2"
	case strings.EqualFold(strings.TrimSpace(runtime), "codex"):
		return "T2"
	default:
		return ""
	}
}

func modelForRuntime(runtime string) string {
	runtime = strings.TrimSpace(runtime)
	if runtime == "" {
		return ""
	}
	return runtime
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func reviewRounds(j *jobstore.Job, bounces int) int {
	rounds := 0
	if j != nil {
		for _, step := range j.Steps {
			id := strings.ToLower(strings.TrimSpace(step.ID))
			target := strings.ToLower(strings.TrimSpace(step.Target))
			if !strings.Contains(id, "review") && !strings.Contains(target, "review") {
				continue
			}
			if step.Attempts > 0 {
				rounds += step.Attempts
			} else if step.Status == jobstore.StatusDone || step.Status == jobstore.StatusFailed {
				rounds++
			}
		}
	}
	if want := bounces + 1; bounces > 0 && rounds < want {
		rounds = want
	}
	return rounds
}

func workUnitsForJob(j *jobstore.Job, target string) []WorkUnitRecord {
	if j == nil || j.Usage == nil {
		return nil
	}
	target = strings.TrimSpace(target)
	out := make([]WorkUnitRecord, 0, len(j.Usage.Records))
	for _, usageRec := range j.Usage.Records {
		startedAt := utcOrZero(usageRec.StartedAt)
		finishedAt := utcOrZero(usageRec.EndedAt)
		if !validWorkInterval(startedAt, finishedAt) {
			continue
		}
		unitTarget := strings.TrimSpace(usageRec.Agent)
		if unitTarget == "" {
			unitTarget = target
		}
		out = append(out, WorkUnitRecord{
			ID:         usage.RecordKey(usageRec),
			Target:     unitTarget,
			Instance:   strings.TrimSpace(usageRec.Instance),
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
		})
	}
	return out
}

func progressedWorkUnitStatus(status string) bool {
	switch jobstore.Status(strings.TrimSpace(status)) {
	case "", jobstore.StatusRunning, jobstore.StatusDone, jobstore.StatusFailed:
		return true
	default:
		return false
	}
}

func terminalEvent(events []jobstore.Event, j *jobstore.Job) (time.Time, string) {
	var first jobstore.Event
	for _, ev := range events {
		if ev.Status != j.Status {
			continue
		}
		if !jobstore.IsTerminalStatus(ev.Status) {
			continue
		}
		if first.TS.IsZero() || ev.TS.Before(first.TS) {
			first = ev
		}
	}
	if !first.TS.IsZero() {
		return first.TS.UTC(), first.Type
	}
	return utcOrZero(j.UpdatedAt), strings.TrimSpace(j.LastEvent)
}

func mergedEventTime(events []jobstore.Event) time.Time {
	var out time.Time
	for _, ev := range events {
		switch ev.Type {
		case "merged", "pr.merged":
			if out.IsZero() || ev.TS.After(out) {
				out = ev.TS.UTC()
			}
		}
	}
	return out
}

func selectEvents(events []jobstore.Event, pred func(jobstore.Event) bool) []EventRef {
	var out []EventRef
	for _, ev := range events {
		if pred(ev) {
			out = append(out, eventRef(ev))
		}
	}
	return out
}

func eventRef(ev jobstore.Event) EventRef {
	return EventRef{TS: utcOrZero(ev.TS), Type: ev.Type, Message: ev.Message, Data: copyStringMap(ev.Data)}
}

func isWatchdogEvent(ev jobstore.Event) bool {
	return eventContains(ev, "watchdog") || ev.Type == "job_timeout" || ev.Type == "instance_killed" && strings.Contains(strings.ToLower(ev.Message), "timeout")
}

func isBudgetNoticeEvent(ev jobstore.Event) bool {
	return ev.Type == "budget_notice"
}

func isBudgetExceededEvent(ev jobstore.Event) bool {
	if eventContains(ev, "budget_exceeded") {
		return true
	}
	if ev.Type == "budget_notice" {
		return budgetNoticeLevel(ev.Data) >= 100
	}
	return false
}

func eventContains(ev jobstore.Event, needle string) bool {
	needle = strings.ToLower(needle)
	return strings.Contains(strings.ToLower(ev.Type), needle) ||
		strings.Contains(strings.ToLower(ev.Message), needle)
}

func budgetNoticeLevel(data map[string]string) int {
	for _, key := range []string{"level", "percent", "threshold"} {
		if raw := strings.TrimSpace(data[key]); raw != "" {
			n, _ := strconv.Atoi(strings.TrimSuffix(raw, "%"))
			return n
		}
	}
	return 0
}

func budgetAllocationTotals(events []jobstore.Event) (allocated, consumed, released int64) {
	for _, ev := range events {
		if ev.Data == nil {
			continue
		}
		allocated += int64Data(ev.Data, "budget_tokens_allocated")
		consumed += int64Data(ev.Data, "budget_tokens_consumed")
		released += int64Data(ev.Data, "budget_tokens_released")
	}
	return allocated, consumed, released
}

func int64Data(data map[string]string, key string) int64 {
	raw := strings.TrimSpace(data[key])
	if raw == "" {
		return 0
	}
	n, _ := strconv.ParseInt(raw, 10, 64)
	return n
}

func gateFailureSummary(records []jobstore.GateRecord) (int, map[string]int) {
	if len(records) == 0 {
		return 0, nil
	}
	classes := map[string]int{}
	failures := 0
	for _, rec := range jobstore.LatestGateRecords(records) {
		if rec.Status != jobstore.GateStatusFail {
			continue
		}
		failures++
		class := jobstore.GateClassContent
		if strings.TrimSpace(rec.Signature) != "" {
			class = "signature"
		}
		classes[class]++
	}
	if failures == 0 {
		return 0, nil
	}
	return failures, classes
}

func teamForJob(teamDir string, j *jobstore.Job) string {
	if j == nil {
		return ""
	}
	if team := strings.TrimSpace(j.Origin.Team); team != "" {
		return team
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil || top == nil {
		return ""
	}
	for _, team := range top.SortedTeams() {
		if j.Pipeline != "" && stringInList(team.Pipelines, j.Pipeline) {
			return team.Name
		}
		if j.Instance != "" && instanceMatches(j.Instance, team.Instances) {
			return team.Name
		}
		if j.Target != "" && stringInList(team.Instances, j.Target) {
			return team.Name
		}
	}
	return ""
}

func agentForJob(j *jobstore.Job) string {
	if j == nil {
		return ""
	}
	if agent := jobstore.ImplementationAgentForJob(j); agent != "" {
		return agent
	}
	return strings.TrimSpace(j.Origin.Agent)
}

func stringInList(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func instanceMatches(instance string, names []string) bool {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return false
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if instance == name || strings.HasPrefix(instance, name+"-") {
			return true
		}
	}
	return false
}

func tokenBudget(j *jobstore.Job) int64 {
	if j == nil {
		return 0
	}
	if j.TokenBudget > 0 {
		return j.TokenBudget
	}
	var total int64
	for _, step := range j.Steps {
		if step.TokenBudget > 0 {
			total += step.TokenBudget
		}
	}
	return total
}

func timeBudgetString(j *jobstore.Job) string {
	if j == nil {
		return ""
	}
	if strings.TrimSpace(j.TimeBudget) != "" {
		return j.TimeBudget
	}
	var total time.Duration
	for _, step := range j.Steps {
		if d, err := time.ParseDuration(strings.TrimSpace(step.TimeBudget)); err == nil && d > 0 {
			total += d
		}
	}
	if total <= 0 {
		return ""
	}
	return total.String()
}

func timeBudgetMS(j *jobstore.Job) int64 {
	raw := timeBudgetString(j)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func usageSummary(j *jobstore.Job) usage.Summary {
	if j == nil || j.Usage == nil {
		return usage.Summary{}
	}
	return j.Usage.Summary
}

func runtimeDurationMS(j *jobstore.Job) int64 {
	if j == nil || j.Usage == nil {
		return 0
	}
	return j.Usage.Summary.DurationMS
}

func durationMS(start, end time.Time) int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func ratio(used, allowance int64) float64 {
	if allowance <= 0 {
		return 0
	}
	return round2(float64(used) / float64(allowance))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func recordMatches(rec Record, opts ReportOptions) bool {
	if !opts.Since.IsZero() {
		ts := rec.FinalizedAt
		if ts.IsZero() {
			ts = rec.RecordedAt
		}
		if !ts.IsZero() && ts.Before(opts.Since.UTC()) {
			return false
		}
	}
	if opts.Team != "" && rec.Team != opts.Team {
		return false
	}
	if opts.Agent != "" && rec.Agent != opts.Agent {
		return false
	}
	return true
}

func trendKey(rec Record, opts ReportOptions) string {
	if opts.ByEpic {
		return strings.TrimSpace(rec.Epic)
	}
	return rec.Week + "\x00" + rec.Team + "\x00" + rec.Agent
}

func capacityKey(team, agent string) string {
	return strings.TrimSpace(team) + "\x00" + strings.TrimSpace(agent)
}

func (r *TrendRow) add(rec Record) {
	r.Jobs++
	switch rec.Status {
	case string(jobstore.StatusDone):
		r.Done++
	case string(jobstore.StatusFailed):
		r.Failed++
	}
	if !rec.MergedAt.IsZero() || rec.TimeToMergeMS > 0 {
		r.Merged++
	}
	r.ReviewRounds += rec.ReviewRounds
	r.Bounces += rec.BounceCount
	if len(rec.BounceClasses) > 0 {
		if r.BounceClasses == nil {
			r.BounceClasses = map[string]int{}
		}
		for class, count := range rec.BounceClasses {
			r.BounceClasses[class] += count
		}
	}
	if key := modelTierKey(rec.Runtime, rec.Model, rec.Tier); key != "" {
		if r.ModelTiers == nil {
			r.ModelTiers = map[string]int{}
		}
		r.ModelTiers[key]++
	}
	r.WatchdogEvents += len(rec.WatchdogEvents)
	r.BudgetNoticeEvents += len(rec.BudgetNoticeEvents)
	r.BudgetExceededEvents += len(rec.BudgetExceededEvents)
	r.TokenBudget += rec.TokenBudget
	r.TokensConsumed += rec.TokensConsumed
	r.RuntimeDurationMS += rec.RuntimeDurationMS
	if rec.TimeToMergeMS > 0 {
		r.AverageTimeToMergeMS += rec.TimeToMergeMS
		r.timeToMergeSamples++
	}
	if rec.TimeToTerminalMS > 0 {
		r.AverageTimeToTerminalMS += rec.TimeToTerminalMS
		r.timeToTerminalSamples++
	}
	target := strings.TrimSpace(r.Agent)
	if target == "" {
		target = strings.TrimSpace(rec.Agent)
	}
	r.workIntervals = append(r.workIntervals, workIntervalsForRecord(rec, target)...)
}

func (r *TrendRow) finalize() {
	if r.Jobs > 0 {
		r.AverageReviewRounds = round2(float64(r.ReviewRounds) / float64(r.Jobs))
		r.AverageBounces = round2(float64(r.Bounces) / float64(r.Jobs))
	}
	r.TokenBudgetRatio = ratio(r.TokensConsumed, r.TokenBudget)
	r.EpicAllocationRatio = ratio(r.TokensConsumed, r.EpicAllocation)
	if r.timeToMergeSamples > 0 {
		r.AverageTimeToMergeMS /= int64(r.timeToMergeSamples)
	}
	if r.timeToTerminalSamples > 0 {
		r.AverageTimeToTerminalMS /= int64(r.timeToTerminalSamples)
	}
	r.EffectiveConcurrency, r.PeakConcurrentWorkUnits = effectiveConcurrency(r.workIntervals)
	if r.DeclaredReplicaCapacity > 0 && r.EffectiveConcurrency > 0 {
		r.ConcurrencyUtilization = round2(r.EffectiveConcurrency / float64(r.DeclaredReplicaCapacity))
	}
}

func (r *ModelTierRow) finalize() {
	if r.Jobs > 0 {
		r.AverageBounces = round2(float64(r.Bounces) / float64(r.Jobs))
		r.AverageReviewRounds = round2(float64(r.ReviewRounds) / float64(r.Jobs))
	}
	r.TokenBudgetRatio = ratio(r.TokensConsumed, r.TokenBudget)
}

func (r *BounceClassRow) finalize() {
	if r.Jobs > 0 {
		r.AverageBounces = round2(float64(r.Bounces) / float64(r.Jobs))
	}
}

func utcOrZero(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Time{}
	}
	return ts.UTC()
}

type workInterval struct {
	start time.Time
	end   time.Time
}

func workIntervalsForRecord(rec Record, target string) []workInterval {
	target = strings.TrimSpace(target)
	var out []workInterval
	for _, unit := range rec.WorkUnits {
		if !progressedWorkUnitStatus(unit.Status) {
			continue
		}
		unitTarget := strings.TrimSpace(unit.Target)
		if target != "" && unitTarget != "" && unitTarget != target {
			continue
		}
		if !validWorkInterval(unit.StartedAt, unit.FinishedAt) {
			continue
		}
		out = append(out, workInterval{start: unit.StartedAt.UTC(), end: unit.FinishedAt.UTC()})
	}
	return out
}

func validWorkInterval(start, end time.Time) bool {
	return !start.IsZero() && !end.IsZero() && end.After(start)
}

type concurrencyEvent struct {
	at    time.Time
	delta int
}

func effectiveConcurrency(intervals []workInterval) (float64, int) {
	if len(intervals) == 0 {
		return 0, 0
	}
	events := make([]concurrencyEvent, 0, len(intervals)*2)
	for _, interval := range intervals {
		if !validWorkInterval(interval.start, interval.end) {
			continue
		}
		events = append(events,
			concurrencyEvent{at: interval.start.UTC(), delta: 1},
			concurrencyEvent{at: interval.end.UTC(), delta: -1},
		)
	}
	if len(events) == 0 {
		return 0, 0
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].at.Before(events[j].at)
	})
	var (
		active          int
		peak            int
		prev            time.Time
		activeSeconds   float64
		weightedSeconds float64
	)
	for i := 0; i < len(events); {
		at := events[i].at
		if !prev.IsZero() && at.After(prev) && active > 0 {
			seconds := at.Sub(prev).Seconds()
			activeSeconds += seconds
			weightedSeconds += seconds * float64(active)
		}
		for i < len(events) && events[i].at.Equal(at) {
			active += events[i].delta
			i++
		}
		if active > peak {
			peak = active
		}
		prev = at
	}
	if activeSeconds == 0 {
		return 0, peak
	}
	return round2(weightedSeconds / activeSeconds), peak
}

func declaredReplicaCapacities(teamDir string) map[string]int {
	if strings.TrimSpace(teamDir) == "" {
		return nil
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil || top == nil {
		return nil
	}
	out := map[string]int{}
	for _, inst := range top.Instances {
		out[capacityKey("", inst.Name)] = instanceCapacity(inst)
	}
	for _, team := range top.SortedTeams() {
		for _, instName := range team.Instances {
			inst := top.Instances[instName]
			if inst == nil {
				continue
			}
			out[capacityKey(team.Name, inst.Name)] = instanceCapacity(inst)
		}
	}
	return out
}

func instanceCapacity(inst *topology.Instance) int {
	if inst == nil {
		return 0
	}
	if inst.Ephemeral {
		return inst.Replicas
	}
	return 1
}

func replicaCapacityFor(capacities map[string]int, team, agent string) int {
	if len(capacities) == 0 || strings.TrimSpace(agent) == "" {
		return 0
	}
	if team = strings.TrimSpace(team); team != "" {
		if cap := capacities[capacityKey(team, agent)]; cap > 0 {
			return cap
		}
	}
	return capacities[capacityKey("", agent)]
}

func epicAllocations(teamDir string) map[string]int64 {
	if strings.TrimSpace(teamDir) == "" {
		return nil
	}
	path := filepath.Join(teamDir, "config.toml")
	var cfg map[string]any
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil
	}
	outcomesCfg, ok := anyMap(cfg["outcomes"])
	if !ok {
		return nil
	}
	rawAllocations, ok := anyMap(outcomesCfg["epic_allocations"])
	if !ok {
		return nil
	}
	allocations := map[string]int64{}
	for epic, raw := range rawAllocations {
		epic = strings.TrimSpace(epic)
		if epic == "" {
			continue
		}
		value, err := allowance.ParseTokenValue(raw, "outcomes.epic_allocations."+epic)
		if err != nil || value <= 0 {
			continue
		}
		allocations[epic] = value
	}
	return allocations
}

func anyMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	default:
		return nil, false
	}
}

func anyString(v any) string {
	switch s := v.(type) {
	case string:
		return strings.TrimSpace(s)
	default:
		return ""
	}
}

func modelTierKey(runtime, model, tier string) string {
	runtime = strings.TrimSpace(runtime)
	model = strings.TrimSpace(model)
	tier = strings.TrimSpace(tier)
	if runtime == "" && model == "" && tier == "" {
		return ""
	}
	return runtime + "\x00" + model + "\x00" + tier
}
