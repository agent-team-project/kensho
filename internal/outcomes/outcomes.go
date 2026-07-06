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
	PR                       string           `json:"pr,omitempty"`
	Pipeline                 string           `json:"pipeline,omitempty"`
	Team                     string           `json:"team,omitempty"`
	Agent                    string           `json:"agent,omitempty"`
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

// EventRef is the compact event shape persisted on outcome records.
type EventRef struct {
	TS      time.Time         `json:"ts,omitempty"`
	Type    string            `json:"type"`
	Message string            `json:"message,omitempty"`
	Data    map[string]string `json:"data,omitempty"`
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
	Since time.Time
	Team  string
	Agent string
	Now   time.Time
}

// Report is the structured output for `agent-team outcomes report`.
type Report struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Since       time.Time  `json:"since,omitempty"`
	Rows        []TrendRow `json:"rows"`
	Summary     TrendRow   `json:"summary"`
}

// TrendRow aggregates terminal outcomes by week, team, and agent.
type TrendRow struct {
	Week                    string         `json:"week,omitempty"`
	Team                    string         `json:"team,omitempty"`
	Agent                   string         `json:"agent,omitempty"`
	Jobs                    int            `json:"jobs"`
	Done                    int            `json:"done,omitempty"`
	Failed                  int            `json:"failed,omitempty"`
	Merged                  int            `json:"merged,omitempty"`
	ReviewRounds            int            `json:"review_rounds,omitempty"`
	AverageReviewRounds     float64        `json:"average_review_rounds,omitempty"`
	Bounces                 int            `json:"bounces,omitempty"`
	AverageBounces          float64        `json:"average_bounces,omitempty"`
	BounceClasses           map[string]int `json:"bounce_classes,omitempty"`
	WatchdogEvents          int            `json:"watchdog_events,omitempty"`
	BudgetNoticeEvents      int            `json:"budget_notice_events,omitempty"`
	BudgetExceededEvents    int            `json:"budget_exceeded_events,omitempty"`
	TokenBudget             int64          `json:"token_budget,omitempty"`
	TokensConsumed          int64          `json:"tokens_consumed,omitempty"`
	TokenBudgetRatio        float64        `json:"token_budget_ratio,omitempty"`
	RuntimeDurationMS       int64          `json:"runtime_duration_ms,omitempty"`
	AverageTimeToMergeMS    int64          `json:"average_time_to_merge_ms,omitempty"`
	AverageTimeToTerminalMS int64          `json:"average_time_to_terminal_ms,omitempty"`

	timeToMergeSamples    int
	timeToTerminalSamples int
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
	report := Report{GeneratedAt: now.UTC(), Since: utcOrZero(opts.Since)}
	byKey := map[string]*TrendRow{}
	for _, rec := range records {
		if !recordMatches(rec, opts) {
			continue
		}
		key := trendKey(rec)
		row := byKey[key]
		if row == nil {
			row = &TrendRow{Week: rec.Week, Team: rec.Team, Agent: rec.Agent}
			byKey[key] = row
		}
		row.add(rec)
		report.Summary.add(rec)
	}
	report.Rows = make([]TrendRow, 0, len(byKey))
	for _, row := range byKey {
		row.finalize()
		report.Rows = append(report.Rows, *row)
	}
	sort.SliceStable(report.Rows, func(i, j int) bool {
		if report.Rows[i].Week != report.Rows[j].Week {
			return report.Rows[i].Week < report.Rows[j].Week
		}
		if report.Rows[i].Team != report.Rows[j].Team {
			return report.Rows[i].Team < report.Rows[j].Team
		}
		return report.Rows[i].Agent < report.Rows[j].Agent
	})
	report.Summary.finalize()
	return report
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
	rules := []struct {
		class string
		keys  []string
	}{
		{"infra", []string{"infra", "flake", "flaky", "timeout", "rate limit", "credential", "auth", "network", "no space", "ci unavailable"}},
		{"validation", []string{"test", "lint", "vet", "build", "compile", "gate", "typecheck", "format"}},
		{"content", []string{"bug", "regression", "behavior", "requirement", "acceptance", "missing", "scope", "incorrect", "wrong"}},
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

func trendKey(rec Record) string {
	return rec.Week + "\x00" + rec.Team + "\x00" + rec.Agent
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
}

func (r *TrendRow) finalize() {
	if r.Jobs > 0 {
		r.AverageReviewRounds = round2(float64(r.ReviewRounds) / float64(r.Jobs))
		r.AverageBounces = round2(float64(r.Bounces) / float64(r.Jobs))
	}
	r.TokenBudgetRatio = ratio(r.TokensConsumed, r.TokenBudget)
	if r.timeToMergeSamples > 0 {
		r.AverageTimeToMergeMS /= int64(r.timeToMergeSamples)
	}
	if r.timeToTerminalSamples > 0 {
		r.AverageTimeToTerminalMS /= int64(r.timeToTerminalSamples)
	}
}

func utcOrZero(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Time{}
	}
	return ts.UTC()
}
