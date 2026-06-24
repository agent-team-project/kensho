package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newSnapshotCmd() *cobra.Command {
	var (
		target        string
		output        string
		jsonOut       bool
		noRedact      bool
		eventLimit    int
		intakeLimit   int
		scheduleLimit int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture a read-only orchestration diagnostic report.",
		Long: "Capture a read-only diagnostic report with health, plan, instance, job, job status preview, queue, " +
			"schedule, runtime, and recent lifecycle event state. Use --json for stdout or --output to write a JSON file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if eventLimit < -1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot: --events must be >= -1.")
				return exitErr(2)
			}
			if intakeLimit < -1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot: --intake-deliveries must be >= -1.")
				return exitErr(2)
			}
			if scheduleLimit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot: --schedule-limit must be >= 0.")
				return exitErr(2)
			}
			if jsonOut && output != "" && output != "-" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot: choose one of --json or --output.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			repoRoot, err := filepath.Abs(effectiveRepoTarget(cmd, target))
			if err != nil {
				return err
			}
			snapshot := collectSnapshot(teamDir, repoRoot, snapshotOptions{
				EventLimit:    eventLimit,
				IntakeLimit:   intakeLimit,
				ScheduleLimit: scheduleLimit,
				Redact:        !noRedact,
				Now:           time.Now().UTC(),
			})
			switch {
			case jsonOut || output == "-":
				return writeSnapshotJSON(cmd.OutOrStdout(), snapshot)
			case output != "":
				path, err := writeSnapshotFile(output, snapshot)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote snapshot to %s\n", path)
				return nil
			default:
				renderSnapshotSummary(cmd.OutOrStdout(), snapshot)
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the full JSON snapshot to this file. Use '-' for stdout.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the full snapshot JSON to stdout.")
	cmd.Flags().BoolVar(&noRedact, "no-redact", false, "Include raw payload values instead of redacting sensitive keys.")
	cmd.Flags().IntVar(&eventLimit, "events", 50, "Recent lifecycle events to include. Use -1 for all events or 0 to skip events.")
	cmd.Flags().IntVar(&intakeLimit, "intake-deliveries", 50, "Recent intake deliveries to include. Use -1 for all deliveries or 0 to skip deliveries.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 10, "Upcoming schedules to include after ordering; 0 means all.")
	return cmd
}

type snapshotOptions struct {
	EventLimit    int
	IntakeLimit   int
	ScheduleLimit int
	Redact        bool
	Now           time.Time
}

type snapshotResult struct {
	Version         string                     `json:"version"`
	CapturedAt      string                     `json:"captured_at"`
	Repo            string                     `json:"repo"`
	TeamDir         string                     `json:"team_dir"`
	Git             *snapshotGitInfo           `json:"git,omitempty"`
	Team            *teamInfo                  `json:"team,omitempty"`
	Redacted        bool                       `json:"redacted"`
	Overview        *overviewResult            `json:"overview,omitempty"`
	Next            *nextActionResult          `json:"next,omitempty"`
	Runtime         *runtimeInfo               `json:"runtime,omitempty"`
	Health          *healthResult              `json:"health,omitempty"`
	Plan            *planResult                `json:"plan,omitempty"`
	Instances       []psJSONRow                `json:"instances,omitempty"`
	Jobs            []*job.Job                 `json:"jobs,omitempty"`
	JobTriage       *jobTriageSnapshot         `json:"job_triage,omitempty"`
	JobStatus       []jobStatusReconcileResult `json:"job_status_preview,omitempty"`
	PipelineStatus  []pipelineStatusRow        `json:"pipeline_status,omitempty"`
	PipelineAdvance []pipelineAdvanceResult    `json:"pipeline_advance_preview,omitempty"`
	TeamsDoctor     *allTeamDoctorResult       `json:"teams_doctor,omitempty"`
	TeamDoctor      *teamDoctorResult          `json:"team_doctor,omitempty"`
	Queue           []*daemon.QueueItem        `json:"queue,omitempty"`
	QueueSummary    *queueSummary              `json:"queue_summary,omitempty"`
	QueueQuarantine []queueQuarantineItem      `json:"queue_quarantine,omitempty"`
	Schedules       []scheduleInfo             `json:"schedules,omitempty"`
	ScheduleNext    []scheduleInfo             `json:"schedule_next,omitempty"`
	Intake          []intakeDelivery           `json:"intake,omitempty"`
	IntakeSummary   *overviewIntakeSummary     `json:"intake_summary,omitempty"`
	Events          []daemon.LifecycleEvent    `json:"events,omitempty"`
	SectionErrors   map[string]string          `json:"section_errors,omitempty"`
}

type snapshotGitInfo struct {
	Branch   string `json:"branch,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
	Dirty    bool   `json:"dirty"`
	Changes  int    `json:"changes,omitempty"`
}

func collectSnapshot(teamDir, repoRoot string, opts snapshotOptions) *snapshotResult {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := &snapshotResult{
		Version:    Version,
		CapturedAt: now.UTC().Format(time.RFC3339),
		Repo:       filepath.ToSlash(repoRoot),
		TeamDir:    filepath.ToSlash(teamDir),
	}
	out.Git = collectSnapshotGitInfo(repoRoot)

	if runtime, err := collectRuntimeInfoForTeam(teamDir); err != nil {
		out.addError("runtime", err)
	} else {
		out.Runtime = &runtime
	}
	if health, err := collectHealth(teamDir, now); err != nil {
		out.addError("health", err)
	} else {
		out.Health = health
	}
	if plan, err := collectPlan(teamDir); err != nil {
		out.addError("plan", err)
	} else {
		out.Plan = plan
	}
	if rows, err := collectPsRows(teamDir, now); err != nil {
		out.addError("instances", err)
	} else {
		out.Instances = psJSONRows(rows)
	}
	if jobs, err := job.List(teamDir); err != nil {
		out.addError("jobs", err)
	} else {
		out.Jobs = jobs
	}
	if triage, err := collectJobTriageWithPolicy(teamDir, now); err != nil {
		out.addError("job_triage", err)
	} else {
		out.JobTriage = &triage
	}
	if status, err := reconcileJobsFromStatus(teamDir, true, now); err != nil {
		out.addError("job_status_preview", err)
	} else {
		out.JobStatus = status
	}
	if status, err := collectPipelineStatusRows(teamDir, ""); err != nil {
		out.addError("pipeline_status", err)
	} else {
		out.PipelineStatus = status
	}
	if advance, err := advanceReadyPipelineJobs(nil, teamDir, "", "auto", runtimeSelection{}, 0, true, true); err != nil {
		out.addError("pipeline_advance_preview", err)
	} else {
		out.PipelineAdvance = advance
	}
	if teamsDoctor, err := collectAllTeamDoctor(teamDir); err != nil {
		out.addError("teams_doctor", err)
	} else if allTeamDoctorHasSnapshotContent(teamsDoctor) {
		out.TeamsDoctor = teamsDoctor
	}
	if queue, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir)); err != nil {
		out.addError("queue", err)
	} else {
		out.Queue = queue
		summary := summarizeQueueItems(queue, now)
		out.QueueSummary = &summary
	}
	if quarantine, err := listQueueQuarantine(teamDir); err != nil {
		out.addError("queue_quarantine", err)
	} else {
		out.QueueQuarantine = quarantine
		applyQueueQuarantineSummary(ensureSnapshotQueueSummary(out, now), quarantine)
	}
	if schedules, err := loadScheduleInfos(teamDir); err != nil {
		out.addError("schedules", err)
	} else {
		out.Schedules = schedules
		out.ScheduleNext = nextScheduleRows(schedules, now, opts.ScheduleLimit)
	}
	if deliveries, err := listIntakeDeliveries(teamDir); err != nil {
		out.addError("intake", err)
	} else {
		summary := overviewIntakeFromDeliveries(deliveries)
		out.IntakeSummary = &summary
		out.Intake = collectSnapshotIntakeDeliveries(deliveries, opts.IntakeLimit)
	}
	if events, err := collectSnapshotEvents(teamDir, opts.EventLimit); err != nil {
		out.addError("events", err)
	} else {
		out.Events = events
	}
	out.Overview = collectOverview(teamDir, now, opts.ScheduleLimit)
	next := nextActionResultFromOverview(out.Overview, 0)
	out.Next = &next
	if opts.Redact {
		redactSnapshotResult(out)
	}
	return out
}

func collectTeamSnapshot(teamDir, repoRoot, name string, opts snapshotOptions) (*snapshotResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	info := teamInfoFromTopology(team)
	out := &snapshotResult{
		Version:    Version,
		CapturedAt: now.Format(time.RFC3339),
		Repo:       filepath.ToSlash(repoRoot),
		TeamDir:    filepath.ToSlash(teamDir),
		Team:       &info,
	}
	out.Git = collectSnapshotGitInfo(repoRoot)

	if runtime, err := collectRuntimeInfoForTeam(teamDir); err != nil {
		out.addError("runtime", err)
	} else {
		out.Runtime = &runtime
	}
	if health, err := collectTeamHealth(teamDir, name, now, true); err != nil {
		out.addError("health", err)
	} else {
		out.Health = health.Health
	}
	if plan, err := collectTeamPlan(teamDir, name, false, psOptions{}, nil); err != nil {
		out.addError("plan", err)
	} else {
		out.Plan = plan.Plan
	}
	if rows, err := collectTeamPsRows(teamDir, name, now); err != nil {
		out.addError("instances", err)
	} else {
		out.Instances = psJSONRows(rows)
	}

	var ownedJobs []*job.Job
	var ownedJobIDs map[string]bool
	if jobs, err := job.List(teamDir); err != nil {
		out.addError("jobs", err)
	} else {
		ownedJobs = teamJobs(top, team, jobs)
		ownedJobIDs = jobIDSet(ownedJobs)
		out.Jobs = ownedJobs
	}
	var teamQueue []*daemon.QueueItem
	if queue, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir)); err != nil {
		out.addError("queue", err)
	} else {
		teamQueue = teamQueueItems(top, team, ownedJobs, queue)
		out.Queue = teamQueue
		summary := summarizeQueueItems(teamQueue, now)
		out.QueueSummary = &summary
	}
	var teamQuarantine []queueQuarantineItem
	if quarantine, err := listQueueQuarantine(teamDir); err != nil {
		out.addError("queue_quarantine", err)
	} else {
		teamQuarantine = teamQueueQuarantineItems(top, team, ownedJobs, quarantine)
		out.QueueQuarantine = teamQuarantine
		applyQueueQuarantineSummary(ensureSnapshotQueueSummary(out, now), teamQuarantine)
	}
	if triage, err := collectJobTriageWithPolicy(teamDir, now); err != nil {
		out.addError("job_triage", err)
	} else {
		triage.Summary = summarizeJobs(ownedJobs)
		triage.Queue = summarizeQueueItems(teamQueue, now)
		applyQueueQuarantineSummary(&triage.Queue, teamQuarantine)
		triage.Attention = filterJobTriageItemsByJobIDs(triage.Attention, ownedJobIDs)
		triage.ReadySteps = filterJobReadyRowsByJobIDs(triage.ReadySteps, ownedJobIDs)
		triage.StatusPreviews = filterJobStatusPreviewsByJobIDs(triage.StatusPreviews, ownedJobIDs)
		out.JobTriage = &triage
	}
	if status, err := reconcileJobsFromStatus(teamDir, true, now); err != nil {
		out.addError("job_status_preview", err)
	} else {
		out.JobStatus = filterJobStatusPreviewsByJobIDs(status, ownedJobIDs)
	}
	if status, err := collectPipelineStatusRows(teamDir, ""); err != nil {
		out.addError("pipeline_status", err)
	} else {
		out.PipelineStatus = teamPipelineStatus(team, status)
	}
	if advance, err := advanceReadyPipelineJobs(nil, teamDir, "", "auto", runtimeSelection{}, 0, true, true); err != nil {
		out.addError("pipeline_advance_preview", err)
	} else {
		out.PipelineAdvance = filterPipelineAdvanceResultsByJobIDs(advance, ownedJobIDs)
	}
	if teamDoctor, err := collectTeamDoctor(teamDir, name); err != nil {
		out.addError("team_doctor", err)
	} else {
		out.TeamDoctor = teamDoctor
	}
	if schedules, err := loadScheduleInfos(teamDir); err != nil {
		out.addError("schedules", err)
	} else {
		out.Schedules = teamSchedules(team, schedules)
		out.ScheduleNext = nextScheduleRows(out.Schedules, now, opts.ScheduleLimit)
	}
	if events, err := collectTeamSnapshotEvents(teamDir, name, opts.EventLimit, now); err != nil {
		out.addError("events", err)
	} else {
		out.Events = events
	}
	if overview, err := collectTeamOverview(teamDir, name, now, opts.ScheduleLimit); err != nil {
		out.addError("overview", err)
	} else {
		out.Overview = overview
		next := nextActionResultFromOverview(overview, 0)
		out.Next = &next
	}
	if opts.Redact {
		redactSnapshotResult(out)
	}
	return out, nil
}

func ensureSnapshotQueueSummary(snapshot *snapshotResult, now time.Time) *queueSummary {
	if snapshot.QueueSummary == nil {
		summary := summarizeQueueItems(snapshot.Queue, now)
		snapshot.QueueSummary = &summary
	}
	return snapshot.QueueSummary
}

func teamQueueQuarantineItems(top *topology.Topology, team *topology.Team, jobs []*job.Job, items []queueQuarantineItem) []queueQuarantineItem {
	if team == nil {
		return nil
	}
	instanceNames := stringSliceSet(team.Instances)
	agents := teamAgentSet(top, team)
	out := make([]queueQuarantineItem, 0, len(items))
	for _, item := range items {
		if queueQuarantineMatchesAnyJob(item, jobs) || queueQuarantineMatchesTeamTarget(item, instanceNames, agents) {
			out = append(out, item)
		}
	}
	return out
}

func queueQuarantineMatchesAnyJob(item queueQuarantineItem, jobs []*job.Job) bool {
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if item.Job != "" && item.Job == j.ID {
			return true
		}
		if strings.TrimSpace(j.Instance) != "" && item.InstanceID == j.Instance {
			return true
		}
	}
	return false
}

func queueQuarantineMatchesTeamTarget(item queueQuarantineItem, instances, agents map[string]bool) bool {
	for _, value := range []string{item.Instance, item.InstanceID} {
		value = strings.TrimSpace(value)
		if value != "" && (instances[value] || agents[value]) {
			return true
		}
	}
	return false
}

func (r *snapshotResult) addError(section string, err error) {
	if err == nil {
		return
	}
	if r.SectionErrors == nil {
		r.SectionErrors = map[string]string{}
	}
	r.SectionErrors[section] = err.Error()
}

func collectSnapshotGitInfo(repoRoot string) *snapshotGitInfo {
	if strings.TrimSpace(repoRoot) == "" {
		return nil
	}
	if out, ok := snapshotGitCommand(repoRoot, "rev-parse", "--is-inside-work-tree"); !ok || strings.TrimSpace(out) != "true" {
		return nil
	}
	info := &snapshotGitInfo{}
	if out, ok := snapshotGitCommand(repoRoot, "branch", "--show-current"); ok {
		info.Branch = strings.TrimSpace(out)
	}
	if info.Branch == "" {
		if out, ok := snapshotGitCommand(repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); ok {
			info.Branch = strings.TrimSpace(out)
		}
	}
	if out, ok := snapshotGitCommand(repoRoot, "rev-parse", "--short=12", "HEAD"); ok {
		info.Commit = strings.TrimSpace(out)
	}
	if out, ok := snapshotGitCommand(repoRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); ok {
		info.Upstream = strings.TrimSpace(out)
	}
	if info.Upstream != "" {
		if out, ok := snapshotGitCommand(repoRoot, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); ok {
			fields := strings.Fields(out)
			if len(fields) == 2 {
				info.Ahead, _ = strconv.Atoi(fields[0])
				info.Behind, _ = strconv.Atoi(fields[1])
			}
		}
	}
	if out, ok := snapshotGitCommand(repoRoot, "status", "--porcelain"); ok {
		out = strings.TrimRight(out, "\n")
		info.Dirty = strings.TrimSpace(out) != ""
		if info.Dirty {
			info.Changes = len(strings.Split(out, "\n"))
		}
	}
	if info.Branch == "" && info.Commit == "" && info.Upstream == "" && !info.Dirty {
		return nil
	}
	return info
}

func snapshotGitCommand(repoRoot string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoRoot}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func collectSnapshotEvents(teamDir string, limit int) ([]daemon.LifecycleEvent, error) {
	if limit == 0 {
		return nil, nil
	}
	tail := limit
	if limit < 0 {
		tail = 0
	}
	var buf bytes.Buffer
	if err := daemon.StreamLifecycleEvents(context.Background(), &buf, daemon.DaemonRoot(teamDir), false, tail); err != nil {
		return nil, err
	}
	lines, err := collectFilteredEventLines(&buf, eventFilters{})
	if err != nil {
		return nil, err
	}
	out := make([]daemon.LifecycleEvent, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.ev)
	}
	return out, nil
}

func collectSnapshotIntakeDeliveries(deliveries []intakeDelivery, limit int) []intakeDelivery {
	if limit == 0 {
		return nil
	}
	deliveries = tailIntakeDeliveries(deliveries, limit)
	return withIntakeDeliveryActions(deliveries)
}

func collectTeamSnapshotEvents(teamDir, name string, limit int, now time.Time) ([]daemon.LifecycleEvent, error) {
	if limit == 0 {
		return nil, nil
	}
	filters, err := teamEventFilters(teamDir, name, nil, nil, "", func() time.Time { return now })
	if err != nil {
		return nil, err
	}
	events, err := collectSnapshotEvents(teamDir, -1)
	if err != nil {
		return nil, err
	}
	matches := make([]daemon.LifecycleEvent, 0, len(events))
	for _, ev := range events {
		if filters.match(ev) {
			matches = append(matches, ev)
		}
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[len(matches)-limit:]
	}
	return matches, nil
}

func filterPipelineAdvanceResultsByJobIDs(results []pipelineAdvanceResult, ids map[string]bool) []pipelineAdvanceResult {
	if len(ids) == 0 {
		return nil
	}
	out := make([]pipelineAdvanceResult, 0, len(results))
	for _, result := range results {
		if ids[result.JobID] {
			out = append(out, result)
		}
	}
	return out
}

func allTeamDoctorHasSnapshotContent(result *allTeamDoctorResult) bool {
	if result == nil {
		return false
	}
	if len(result.Teams) > 0 || len(result.Problems) > 0 {
		return true
	}
	for _, warning := range result.Warnings {
		if warning.Code != "no_teams" {
			return true
		}
	}
	return false
}

const snapshotRedactedValue = "[redacted]"

func redactSnapshotResult(snapshot *snapshotResult) {
	if snapshot == nil {
		return
	}
	snapshot.Redacted = true
	for _, item := range snapshot.Queue {
		if item == nil {
			continue
		}
		item.Payload = redactSnapshotMap(item.Payload)
	}
	for i := range snapshot.Schedules {
		snapshot.Schedules[i].Payload = redactSnapshotMap(snapshot.Schedules[i].Payload)
	}
	for i := range snapshot.ScheduleNext {
		snapshot.ScheduleNext[i].Payload = redactSnapshotMap(snapshot.ScheduleNext[i].Payload)
	}
	for i := range snapshot.Intake {
		snapshot.Intake[i].Payload = redactSnapshotMap(snapshot.Intake[i].Payload)
	}
	for i := range snapshot.PipelineAdvance {
		redactSnapshotPipelineAdvance(&snapshot.PipelineAdvance[i])
	}
}

func redactSnapshotPipelineAdvance(result *pipelineAdvanceResult) {
	if result == nil || result.Preview == nil || result.Preview.Dispatch == nil || result.Preview.Dispatch.Preview == nil {
		return
	}
	result.Preview.Dispatch.Preview.Payload = redactSnapshotMap(result.Preview.Dispatch.Preview.Payload)
}

func redactSnapshotMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if snapshotSensitiveKey(key) {
			out[key] = snapshotRedactedValue
			continue
		}
		out[key] = redactSnapshotValue(value)
	}
	return out
}

func redactSnapshotValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return redactSnapshotMap(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactSnapshotValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, item := range v {
			out[i] = redactSnapshotMap(item)
		}
		return out
	default:
		return value
	}
}

func snapshotSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, token := range []string{
		"secret",
		"token",
		"password",
		"passwd",
		"api_key",
		"apikey",
		"access_token",
		"refresh_token",
		"auth_token",
		"authorization",
		"bearer",
		"cookie",
		"private_key",
		"client_secret",
		"webhook_secret",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func writeSnapshotFile(path string, snapshot *snapshotResult) (string, error) {
	path = filepath.Clean(path)
	body, err := snapshotJSON(snapshot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}

func writeSnapshotJSON(w io.Writer, snapshot *snapshotResult) error {
	body, err := snapshotJSON(snapshot)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func snapshotJSON(snapshot *snapshotResult) ([]byte, error) {
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func renderSnapshotSummary(w io.Writer, snapshot *snapshotResult) {
	if snapshot == nil {
		fmt.Fprintln(w, "snapshot: unavailable")
		return
	}
	fmt.Fprintf(w, "snapshot: %s\n", snapshot.CapturedAt)
	fmt.Fprintf(w, "repo: %s\n", snapshot.Repo)
	if snapshot.Git != nil {
		branch := snapshot.Git.Branch
		if branch == "" {
			branch = "unknown"
		}
		commit := snapshot.Git.Commit
		if commit == "" {
			commit = "unknown"
		}
		fmt.Fprintf(w, "git: branch=%s commit=%s dirty=%s changes=%d ahead=%d behind=%d\n",
			branch,
			commit,
			yesNo(snapshot.Git.Dirty),
			snapshot.Git.Changes,
			snapshot.Git.Ahead,
			snapshot.Git.Behind)
	}
	if snapshot.Team != nil {
		fmt.Fprintf(w, "team: %s\n", snapshot.Team.Name)
	}
	fmt.Fprintf(w, "redacted: %s\n", yesNo(snapshot.Redacted))
	if snapshot.Health != nil {
		fmt.Fprintf(w, "health: %s\n", repairHealthState(snapshot.Health))
	}
	if snapshot.Next != nil {
		fmt.Fprintf(w, "next: state=%s actions=%d\n", snapshot.Next.State, len(snapshot.Next.Actions))
	}
	if snapshot.Plan != nil {
		fmt.Fprintf(w, "plan: total=%d start=%d resume=%d keep=%d on_demand=%d extra=%d\n",
			snapshot.Plan.Summary.Total,
			snapshot.Plan.Summary.Start,
			snapshot.Plan.Summary.Resume,
			snapshot.Plan.Summary.Keep,
			snapshot.Plan.Summary.OnDemand,
			snapshot.Plan.Summary.Extra)
	}
	fmt.Fprintf(w, "instances: %d\n", len(snapshot.Instances))
	renderSnapshotJobSummary(w, snapshot.Jobs)
	if snapshot.JobTriage != nil {
		fmt.Fprintf(w, "job triage: attention=%d ready_steps=%d\n", len(snapshot.JobTriage.Attention), len(snapshot.JobTriage.ReadySteps))
	}
	if snapshot.JobStatus != nil {
		fmt.Fprintf(w, "job status: previews=%d changes=%d\n", len(snapshot.JobStatus), countChangedJobStatusPreviews(snapshot.JobStatus))
	}
	if snapshot.PipelineStatus != nil {
		fmt.Fprintf(w, "pipeline status: pipelines=%d jobs=%d ready_steps=%d manual_gates=%d failed_steps=%d\n",
			len(snapshot.PipelineStatus),
			countPipelineStatusJobs(snapshot.PipelineStatus),
			countPipelineStatusReadySteps(snapshot.PipelineStatus),
			countPipelineStatusManualGates(snapshot.PipelineStatus),
			countPipelineStatusFailedSteps(snapshot.PipelineStatus))
	}
	if snapshot.PipelineAdvance != nil {
		fmt.Fprintf(w, "pipeline advance: ready=%d route_previews=%d\n", len(snapshot.PipelineAdvance), countPipelineAdvanceRoutePreviews(snapshot.PipelineAdvance))
	}
	if snapshot.TeamsDoctor != nil {
		fmt.Fprintf(w, "teams doctor: teams=%d problems=%d warnings=%d\n",
			len(snapshot.TeamsDoctor.Teams),
			len(snapshot.TeamsDoctor.Problems),
			countSnapshotTeamDoctorWarnings(snapshot.TeamsDoctor.Warnings))
	}
	if snapshot.TeamDoctor != nil {
		fmt.Fprintf(w, "team doctor: problems=%d warnings=%d\n",
			len(snapshot.TeamDoctor.Problems),
			countSnapshotTeamDoctorWarnings(snapshot.TeamDoctor.Warnings))
	}
	if snapshot.QueueSummary != nil {
		fmt.Fprintln(w, queueSummaryLine(*snapshot.QueueSummary))
	}
	fmt.Fprintf(w, "schedules: declared=%d upcoming=%d\n", len(snapshot.Schedules), len(snapshot.ScheduleNext))
	if snapshot.IntakeSummary != nil {
		fmt.Fprintf(w, "intake: deliveries=%d errors=%d recovered=%d replayable=%d\n",
			snapshot.IntakeSummary.Deliveries,
			snapshot.IntakeSummary.Errors,
			snapshot.IntakeSummary.Recovered,
			snapshot.IntakeSummary.Replayable)
	}
	fmt.Fprintf(w, "events: %d\n", len(snapshot.Events))
	if len(snapshot.SectionErrors) > 0 {
		fmt.Fprintln(w, "section errors:")
		keys := make([]string, 0, len(snapshot.SectionErrors))
		for key := range snapshot.SectionErrors {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(w, "  %s: %s\n", key, snapshot.SectionErrors[key])
		}
	}
}

func countPipelineStatusJobs(rows []pipelineStatusRow) int {
	count := 0
	for _, row := range rows {
		count += row.Jobs
	}
	return count
}

func countPipelineStatusReadySteps(rows []pipelineStatusRow) int {
	count := 0
	for _, row := range rows {
		count += row.ReadySteps
	}
	return count
}

func countPipelineStatusManualGates(rows []pipelineStatusRow) int {
	count := 0
	for _, row := range rows {
		count += row.ManualGates
	}
	return count
}

func countPipelineStatusFailedSteps(rows []pipelineStatusRow) int {
	count := 0
	for _, row := range rows {
		count += row.FailedSteps
	}
	return count
}

func countPipelineAdvanceRoutePreviews(results []pipelineAdvanceResult) int {
	count := 0
	for _, result := range results {
		if result.Preview != nil && result.Preview.Dispatch != nil && result.Preview.Dispatch.Preview != nil {
			count++
		}
	}
	return count
}

func countSnapshotTeamDoctorWarnings(warnings []teamDoctorFinding) int {
	count := 0
	for _, warning := range warnings {
		if warning.Code == "no_teams" {
			continue
		}
		count++
	}
	return count
}

func countChangedJobStatusPreviews(results []jobStatusReconcileResult) int {
	changed := 0
	for _, result := range results {
		if result.Changed {
			changed++
		}
	}
	return changed
}

func renderSnapshotJobSummary(w io.Writer, jobs []*job.Job) {
	counts := map[job.Status]int{}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		counts[j.Status]++
	}
	fmt.Fprintf(w, "jobs: total=%d queued=%d running=%d blocked=%d done=%d failed=%d\n",
		len(jobs),
		counts[job.StatusQueued],
		counts[job.StatusRunning],
		counts[job.StatusBlocked],
		counts[job.StatusDone],
		counts[job.StatusFailed])
}
