package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newOverviewCmd() *cobra.Command {
	var (
		target        string
		jsonOut       bool
		watch         bool
		noClear       bool
		scheduleLimit int
		interval      time.Duration
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "Show a concise operator overview across health, jobs, queue, pipelines, and schedules.",
		Long: "Show a read-only operator overview with health, topology, job, queue, pipeline, " +
			"schedule, and recommended next-action summaries.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team overview: --interval must be >= 0.")
				return exitErr(2)
			}
			if scheduleLimit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team overview: --schedule-limit must be >= 0.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team overview: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOverviewFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team overview: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runOverviewWatch(ctx, cmd.OutOrStdout(), func(now time.Time) (*overviewResult, error) {
					return collectOverview(teamDir, now, scheduleLimit), nil
				}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			result := collectOverview(teamDir, time.Now().UTC(), scheduleLimit)
			return renderOverview(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit overview as JSON.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh overview until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 5, "Upcoming schedules to inspect after ordering; 0 means all.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringVar(&format, "format", "", "Render the overview result with a Go template, e.g. '{{.State}} {{len .Actions}}'.")
	return cmd
}

func newTeamOverviewCmd() *cobra.Command {
	var (
		repo          string
		jsonOut       bool
		watch         bool
		noClear       bool
		scheduleLimit int
		interval      time.Duration
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "overview <team>",
		Short: "Show a concise operator overview for one declared team.",
		Long: "Show a read-only operator overview scoped to one declared team with health, topology, job, " +
			"queue, pipeline, schedule, and recommended next-action summaries.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team overview: --interval must be >= 0.")
				return exitErr(2)
			}
			if scheduleLimit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team overview: --schedule-limit must be >= 0.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team overview: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOverviewFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team overview: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runOverviewWatch(ctx, cmd.OutOrStdout(), func(now time.Time) (*overviewResult, error) {
					return collectTeamOverview(teamDir, args[0], now, scheduleLimit)
				}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			result, err := collectTeamOverview(teamDir, args[0], time.Now().UTC(), scheduleLimit)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team overview: %v\n", err)
				return exitErr(1)
			}
			return renderOverview(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team overview as JSON.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team overview until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 5, "Upcoming team schedules to inspect after ordering; 0 means all.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team overview result with a Go template, e.g. '{{.Team.Name}} {{.State}}'.")
	return cmd
}

type overviewResult struct {
	OK            bool                    `json:"ok"`
	State         string                  `json:"state"`
	CapturedAt    string                  `json:"captured_at"`
	Team          *teamInfo               `json:"team,omitempty"`
	Health        overviewHealthSummary   `json:"health"`
	Topology      *topologySummary        `json:"topology,omitempty"`
	Jobs          overviewJobSummary      `json:"jobs"`
	Queue         queueSummary            `json:"queue"`
	Pipelines     overviewPipelineSummary `json:"pipelines"`
	Schedules     overviewScheduleSummary `json:"schedules"`
	Intake        overviewIntakeSummary   `json:"intake"`
	Actions       []string                `json:"actions,omitempty"`
	SectionErrors map[string]string       `json:"section_errors,omitempty"`
}

type overviewHealthSummary struct {
	Healthy       bool `json:"healthy"`
	DaemonRunning bool `json:"daemon_running"`
	DaemonReady   bool `json:"daemon_ready"`
	DaemonPID     int  `json:"daemon_pid,omitempty"`
	Issues        int  `json:"issues"`
	Errors        int  `json:"errors"`
	Warnings      int  `json:"warnings"`
}

type overviewJobSummary struct {
	Summary               jobSummary `json:"summary"`
	Attention             int        `json:"attention"`
	CleanupReady          int        `json:"cleanup_ready"`
	ReadySteps            int        `json:"ready_steps"`
	StatusPreviews        int        `json:"status_previews"`
	StatusChanges         int        `json:"status_changes"`
	BlockedStatusPreviews int        `json:"blocked_status_previews"`
}

type overviewPipelineSummary struct {
	Total        int `json:"total"`
	Jobs         int `json:"jobs"`
	ReadySteps   int `json:"ready_steps"`
	QueuedSteps  int `json:"queued_steps"`
	RunningSteps int `json:"running_steps"`
	BlockedSteps int `json:"blocked_steps"`
	ManualGates  int `json:"manual_gates"`
	FailedSteps  int `json:"failed_steps"`
	DoneSteps    int `json:"done_steps"`
}

type overviewScheduleSummary struct {
	Declared int      `json:"declared"`
	Due      int      `json:"due"`
	Upcoming int      `json:"upcoming"`
	DueNames []string `json:"due_names,omitempty"`
}

type overviewIntakeSummary struct {
	Deliveries    int    `json:"deliveries"`
	Errors        int    `json:"errors"`
	Recovered     int    `json:"recovered"`
	Replayable    int    `json:"replayable"`
	LatestErrorID string `json:"latest_error_id,omitempty"`
	LatestError   string `json:"latest_error,omitempty"`
}

func collectOverview(teamDir string, now time.Time, scheduleLimit int) *overviewResult {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	out := &overviewResult{
		OK:         true,
		State:      "ok",
		CapturedAt: now.Format(time.RFC3339),
	}

	health, err := collectHealthWithOptions(teamDir, now, healthOptions{includeJobs: true})
	if err != nil {
		out.addError("health", err)
	} else if health != nil {
		out.Health = overviewHealthFromHealth(health)
		out.Queue = health.Queue
		if health.Jobs != nil {
			out.Jobs = overviewJobsFromTriage(*health.Jobs)
		}
		out.Pipelines = overviewPipelinesFromRows(health.PipelineStatus)
	}

	if topology, err := collectTopologySummary(teamDir); err != nil {
		out.addError("topology", err)
	} else {
		out.Topology = topology
	}

	if out.Jobs.Summary.Total == 0 && out.Jobs.Attention == 0 && out.Jobs.ReadySteps == 0 {
		if triage, err := collectJobTriageWithPolicy(teamDir, now); err != nil {
			out.addError("jobs", err)
		} else {
			out.Jobs = overviewJobsFromTriage(triage)
			if out.Queue.Total == 0 {
				out.Queue = triage.Queue
			}
		}
	}

	if out.Pipelines.Total == 0 {
		if rows, err := collectPipelineStatusRows(teamDir, ""); err != nil {
			out.addError("pipelines", err)
		} else {
			out.Pipelines = overviewPipelinesFromRows(rows)
		}
	}

	if schedules, err := loadScheduleInfos(teamDir); err != nil {
		out.addError("schedules", err)
	} else {
		out.Schedules = overviewSchedulesFromRows(schedules, now, scheduleLimit)
	}

	if deliveries, err := listIntakeDeliveries(teamDir); err != nil {
		out.addError("intake", err)
	} else {
		out.Intake = overviewIntakeFromDeliveries(deliveries)
	}

	if quarantined, err := listQueueQuarantine(teamDir); err != nil {
		out.addError("queue_quarantine", err)
	} else {
		applyQueueQuarantineSummary(&out.Queue, quarantined)
	}

	out.Actions = overviewActions(out, health)
	out.OK = overviewOK(out, health)
	out.State = overviewState(out)
	return out
}

func collectTeamOverview(teamDir, name string, now time.Time, scheduleLimit int) (*overviewResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	info := teamInfoFromTopology(team)
	out := &overviewResult{
		OK:         true,
		State:      "ok",
		CapturedAt: now.Format(time.RFC3339),
		Team:       &info,
	}

	var health *healthResult
	if snapshot, err := collectTeamHealth(teamDir, team.Name, now, true); err != nil {
		out.addError("health", err)
	} else if snapshot != nil && snapshot.Health != nil {
		health = snapshot.Health
		out.Health = overviewHealthFromHealth(snapshot.Health)
		out.Queue = snapshot.Health.Queue
		if snapshot.Health.Jobs != nil {
			out.Jobs = overviewJobsFromTriage(*snapshot.Health.Jobs)
		}
		out.Pipelines = overviewPipelinesFromRows(snapshot.Health.PipelineStatus)
	}

	if doctor, err := collectTeamDoctor(teamDir, team.Name); err != nil {
		out.addError("topology", err)
	} else {
		out.Topology = overviewTopologyFromTeam(top, team, doctor)
	}

	if out.Jobs.Summary.Total == 0 && out.Jobs.Attention == 0 && out.Jobs.ReadySteps == 0 {
		if triage, err := collectTeamTriageWithPolicy(teamDir, team.Name, now, jobTriageFilters{}); err != nil {
			out.addError("jobs", err)
		} else {
			out.Jobs = overviewJobsFromTriage(triage)
			if out.Queue.Total == 0 {
				out.Queue = triage.Queue
			}
		}
	}

	if out.Pipelines.Total == 0 {
		if rows, err := collectTeamPipelineStatus(teamDir, team.Name); err != nil {
			out.addError("pipelines", err)
		} else {
			out.Pipelines = overviewPipelinesFromRows(rows)
		}
	}

	if schedules, err := collectTeamSchedules(teamDir, team.Name); err != nil {
		out.addError("schedules", err)
	} else {
		out.Schedules = overviewSchedulesFromRows(schedules, now, scheduleLimit)
	}

	out.Actions = overviewActionsForScope(out, health, team.Name)
	out.OK = overviewOK(out, health)
	out.State = overviewState(out)
	return out, nil
}

func overviewTopologyFromTeam(top *topology.Topology, team *topology.Team, doctor *teamDoctorResult) *topologySummary {
	summary := &topologySummary{OK: true}
	if top == nil || team == nil {
		return summary
	}
	summary.Teams = 1
	for _, name := range team.Instances {
		inst := top.Instances[name]
		if inst == nil {
			continue
		}
		summary.Instances++
		if inst.Ephemeral {
			summary.Ephemeral++
		} else {
			summary.Persistent++
		}
		summary.Triggers += len(inst.Triggers)
	}
	for _, name := range team.Pipelines {
		pipeline := top.Pipelines[name]
		if pipeline == nil {
			continue
		}
		summary.Pipelines++
		summary.PipelineSteps += len(pipeline.Steps)
	}
	for _, name := range team.Schedules {
		if top.Schedules[name] != nil {
			summary.Schedules++
		}
	}
	if doctor != nil {
		summary.TeamProblems = len(doctor.Problems)
		summary.TeamWarnings = countSnapshotTeamDoctorWarnings(doctor.Warnings)
	}
	summary.OK = summary.TeamProblems == 0
	return summary
}

func overviewHealthFromHealth(health *healthResult) overviewHealthSummary {
	out := overviewHealthSummary{
		Healthy:       health.Healthy,
		DaemonRunning: health.Daemon.Running,
		DaemonReady:   health.Daemon.Ready,
		DaemonPID:     health.Daemon.PID,
		Issues:        len(health.Issues),
	}
	for _, issue := range health.Issues {
		switch issue.Severity {
		case "warning":
			out.Warnings++
		default:
			out.Errors++
		}
	}
	return out
}

func overviewJobsFromTriage(triage jobTriageSnapshot) overviewJobSummary {
	return overviewJobSummary{
		Summary:               triage.Summary,
		Attention:             len(triage.Attention),
		CleanupReady:          countJobTriageReason(triage.Attention, "cleanup_ready"),
		ReadySteps:            len(triage.ReadySteps),
		StatusPreviews:        len(triage.StatusPreviews),
		StatusChanges:         countChangedJobStatusPreviews(triage.StatusPreviews),
		BlockedStatusPreviews: countJobStatusPreviewsByAfter(triage.StatusPreviews, "blocked"),
	}
}

func countJobTriageReason(items []jobTriageItem, reason string) int {
	count := 0
	for _, item := range items {
		if stringSliceContains(item.Reasons, reason) {
			count++
		}
	}
	return count
}

func overviewPipelinesFromRows(rows []pipelineStatusRow) overviewPipelineSummary {
	out := overviewPipelineSummary{Total: len(rows)}
	for _, row := range rows {
		out.Jobs += row.Jobs
		out.ReadySteps += row.ReadySteps
		out.QueuedSteps += row.QueuedSteps
		out.RunningSteps += row.RunningSteps
		out.BlockedSteps += row.BlockedSteps
		out.ManualGates += row.ManualGates
		out.FailedSteps += row.FailedSteps
		out.DoneSteps += row.DoneSteps
	}
	return out
}

func overviewSchedulesFromRows(schedules []scheduleInfo, now time.Time, limit int) overviewScheduleSummary {
	due := dueScheduleRows(schedules, now)
	next := nextScheduleRows(schedules, now, limit)
	out := overviewScheduleSummary{
		Declared: len(schedules),
		Due:      len(due),
		Upcoming: len(next),
	}
	for _, schedule := range due {
		out.DueNames = append(out.DueNames, schedule.Name)
	}
	sort.Strings(out.DueNames)
	return out
}

func overviewIntakeFromDeliveries(deliveries []intakeDelivery) overviewIntakeSummary {
	summary := summarizeIntakeDeliveries(deliveries)
	return overviewIntakeSummary{
		Deliveries:    summary.Deliveries,
		Errors:        summary.Unresolved,
		Recovered:     summary.Recovered,
		Replayable:    summary.Replayable,
		LatestErrorID: summary.LatestErrorID,
		LatestError:   summary.LatestError,
	}
}

func overviewActions(out *overviewResult, health *healthResult) []string {
	return overviewActionsForScope(out, health, "")
}

func overviewActionsForScope(out *overviewResult, health *healthResult, teamName string) []string {
	seen := map[string]bool{}
	var actions []string
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" || seen[action] {
			return
		}
		seen[action] = true
		actions = append(actions, action)
	}

	if health != nil && !health.Healthy {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team repair %s --dry-run --jobs", teamName))
		} else {
			add("agent-team repair --dry-run --jobs")
		}
	}
	if health != nil {
		for _, issue := range health.Issues {
			if issue.Code == "daemon_not_running" || issue.Code == "daemon_not_ready" {
				add("agent-team daemon start")
			}
			for _, action := range issue.Actions {
				add(overviewIssueAction(action))
			}
		}
	}
	if out.Topology != nil && !out.Topology.OK {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team doctor %s", teamName))
		} else {
			add("agent-team topology summary")
			add("agent-team pipeline doctor --all")
			add("agent-team team doctor --all")
		}
	}
	if out.Queue.Dead > 0 {
		add(overviewQueueDeadRetryAction(health, teamName))
	}
	if out.Queue.Pending > out.Queue.Delayed {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team tick %s --dry-run --skip-schedules --skip-advance", teamName))
		} else {
			add("agent-team queue drain --dry-run")
		}
	}
	if out.Queue.Quarantined > 0 {
		add(overviewQueueQuarantineAction(health, teamName))
	}
	if out.Jobs.Attention > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team triage %s", teamName))
		} else {
			add("agent-team job triage")
		}
	}
	if out.Jobs.CleanupReady > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team cleanup %s --dry-run", teamName))
		} else {
			add("agent-team job cleanup --all --dry-run")
		}
	}
	if out.Jobs.StatusChanges > 0 {
		add("agent-team job reconcile status")
	}
	if out.Jobs.ReadySteps > 0 || out.Pipelines.ReadySteps > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team advance %s --dry-run --preview-routes", teamName))
		} else {
			add("agent-team pipeline advance --all --dry-run --preview-routes")
		}
	}
	if out.Pipelines.ManualGates > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team approve %s --dry-run --dispatch --preview-routes", teamName))
		} else {
			add("agent-team pipeline approve --all --dry-run --dispatch --preview-routes")
		}
	}
	if out.Schedules.Due > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team tick %s --dry-run --skip-drain --skip-advance", teamName))
		} else {
			add("agent-team schedule fire --dry-run --preview-triggers")
		}
	}
	if teamName == "" && out.Intake.Errors > 0 {
		add("agent-team intake summary")
		add("agent-team intake deliveries --status error")
		if out.Intake.Replayable > 0 {
			add("agent-team intake replay --all --dry-run --preview-triggers")
		}
	}
	if len(out.SectionErrors) > 0 {
		if teamName == "" && strings.TrimSpace(out.SectionErrors["intake"]) != "" {
			add("agent-team intake doctor")
		}
		if overviewHasQueueSectionError(out) {
			add("agent-team queue doctor")
		}
		if teamName != "" {
			add(fmt.Sprintf("agent-team team snapshot %s --json", teamName))
		} else {
			add("agent-team snapshot --json")
		}
	}
	return actions
}

func overviewQueueDeadRetryAction(health *healthResult, teamName string) string {
	if health != nil {
		for _, issue := range health.Issues {
			if issue.Code != "queue_dead_letter" {
				continue
			}
			for _, action := range issue.Actions {
				if retry := overviewQueueRetryDryRunAction(action, teamName); retry != "" {
					return retry
				}
			}
		}
	}
	if teamName != "" {
		return fmt.Sprintf("agent-team team queue retry %s --all --dry-run", teamName)
	}
	return "agent-team queue retry --all --dry-run"
}

func overviewQueueRetryDryRunAction(action, teamName string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(action, "agent-team job queue retry "):
		return appendDryRunFlag(action)
	case teamName != "" && strings.HasPrefix(action, fmt.Sprintf("agent-team team queue retry %s ", teamName)):
		return appendDryRunFlag(action)
	case teamName == "" && action == "agent-team queue retry --all":
		return appendDryRunFlag(action)
	default:
		return ""
	}
}

func appendDryRunFlag(action string) string {
	if strings.Contains(" "+action+" ", " --dry-run ") {
		return action
	}
	return action + " --dry-run"
}

func overviewIssueAction(action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return ""
	}
	if overviewIssueActionPrefersDryRun(action) {
		return appendDryRunFlag(action)
	}
	return action
}

func overviewIssueActionPrefersDryRun(action string) bool {
	switch {
	case action == "agent-team repair" || strings.HasPrefix(action, "agent-team repair "):
		return true
	case action == "agent-team queue retry" || strings.HasPrefix(action, "agent-team queue retry "):
		return true
	case strings.HasPrefix(action, "agent-team job queue retry "):
		return true
	case strings.HasPrefix(action, "agent-team team queue retry "):
		return true
	default:
		return false
	}
}

func overviewQueueQuarantineAction(health *healthResult, teamName string) string {
	if teamName != "" {
		return fmt.Sprintf("agent-team team queue quarantine %s", teamName)
	}
	if health != nil {
		for _, issue := range health.Issues {
			if issue.Code != "queue_quarantined" {
				continue
			}
			for _, action := range issue.Actions {
				if strings.HasPrefix(action, "agent-team job queue quarantine ") || action == "agent-team queue quarantine ls" {
					return action
				}
			}
		}
	}
	return "agent-team queue quarantine ls"
}

func overviewHasQueueSectionError(out *overviewResult) bool {
	if out == nil {
		return false
	}
	for section, message := range out.SectionErrors {
		section = strings.ToLower(strings.TrimSpace(section))
		message = strings.ToLower(strings.TrimSpace(message))
		if section == "queue" || strings.Contains(message, "queue:") || strings.Contains(message, "/queue/") {
			return true
		}
	}
	return false
}

func overviewOK(out *overviewResult, health *healthResult) bool {
	if out == nil {
		return true
	}
	if len(out.SectionErrors) > 0 {
		return false
	}
	if health != nil && !health.Healthy {
		return false
	}
	if out.Topology != nil && !out.Topology.OK {
		return false
	}
	return out.Queue.Dead == 0 &&
		out.Queue.Pending <= out.Queue.Delayed &&
		out.Queue.Quarantined == 0 &&
		out.Jobs.Attention == 0 &&
		out.Jobs.ReadySteps == 0 &&
		out.Jobs.StatusChanges == 0 &&
		out.Pipelines.ReadySteps == 0 &&
		out.Pipelines.BlockedSteps == 0 &&
		out.Pipelines.FailedSteps == 0 &&
		out.Schedules.Due == 0 &&
		out.Intake.Errors == 0
}

func overviewState(out *overviewResult) string {
	if out == nil || out.OK {
		return "ok"
	}
	if len(out.SectionErrors) > 0 ||
		out.Health.Errors > 0 ||
		out.Health.Warnings > 0 ||
		(out.Topology != nil && !out.Topology.OK) ||
		out.Queue.Dead > 0 ||
		out.Queue.Quarantined > 0 ||
		out.Jobs.Attention > 0 ||
		out.Pipelines.BlockedSteps > 0 ||
		out.Pipelines.FailedSteps > 0 ||
		out.Intake.Errors > 0 {
		return "attention"
	}
	return "active"
}

func (r *overviewResult) addError(section string, err error) {
	if err == nil {
		return
	}
	if r.SectionErrors == nil {
		r.SectionErrors = map[string]string{}
	}
	r.SectionErrors[section] = err.Error()
}

func runOverviewWatch(ctx context.Context, w io.Writer, collect func(time.Time) (*overviewResult, error), jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !jsonOut {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
		}
		result, err := collect(time.Now().UTC())
		if err != nil {
			return err
		}
		if err := renderOverview(w, result, jsonOut, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

func renderOverview(w io.Writer, result *overviewResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &overviewResult{OK: true, State: "ok"}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderOverviewFormat(w, result, tmpl)
	}
	fmt.Fprintf(w, "overview: %s\n", result.State)
	if result.CapturedAt != "" {
		fmt.Fprintf(w, "captured: %s\n", result.CapturedAt)
	}
	if result.Team != nil {
		fmt.Fprintf(w, "team: %s\n", result.Team.Name)
	}
	daemon := "down"
	if result.Health.DaemonRunning {
		daemon = "running"
		if result.Health.DaemonReady {
			daemon = "ready"
		}
	}
	healthState := "healthy"
	if !result.Health.Healthy {
		healthState = "unhealthy"
	}
	fmt.Fprintf(w, "health: %s daemon=%s issues=%d errors=%d warnings=%d\n",
		healthState,
		daemon,
		result.Health.Issues,
		result.Health.Errors,
		result.Health.Warnings)
	if result.Topology != nil {
		fmt.Fprintf(w, "topology: instances=%d persistent=%d ephemeral=%d triggers=%d pipelines=%d teams=%d problems=%d warnings=%d\n",
			result.Topology.Instances,
			result.Topology.Persistent,
			result.Topology.Ephemeral,
			result.Topology.Triggers,
			result.Topology.Pipelines,
			result.Topology.Teams,
			result.Topology.PipelineProblems+result.Topology.TeamProblems,
			result.Topology.PipelineWarnings+result.Topology.TeamWarnings)
	}
	fmt.Fprintf(w, "jobs: total=%d queued=%d running=%d blocked=%d done=%d failed=%d attention=%d cleanup_ready=%d ready_steps=%d status_changes=%d\n",
		result.Jobs.Summary.Total,
		result.Jobs.Summary.Queued,
		result.Jobs.Summary.Running,
		result.Jobs.Summary.Blocked,
		result.Jobs.Summary.Done,
		result.Jobs.Summary.Failed,
		result.Jobs.Attention,
		result.Jobs.CleanupReady,
		result.Jobs.ReadySteps,
		result.Jobs.StatusChanges)
	fmt.Fprintln(w, queueSummaryLine(result.Queue))
	fmt.Fprintf(w, "pipelines: total=%d jobs=%d ready_steps=%d blocked_steps=%d failed_steps=%d\n",
		result.Pipelines.Total,
		result.Pipelines.Jobs,
		result.Pipelines.ReadySteps,
		result.Pipelines.BlockedSteps,
		result.Pipelines.FailedSteps)
	fmt.Fprintf(w, "schedules: declared=%d due=%d upcoming=%d\n",
		result.Schedules.Declared,
		result.Schedules.Due,
		result.Schedules.Upcoming)
	fmt.Fprintf(w, "intake: deliveries=%d errors=%d recovered=%d replayable=%d latest_error=%s\n",
		result.Intake.Deliveries,
		result.Intake.Errors,
		result.Intake.Recovered,
		result.Intake.Replayable,
		emptyDash(result.Intake.LatestErrorID))
	if len(result.Actions) == 0 {
		fmt.Fprintln(w, "actions: none")
	} else {
		fmt.Fprintln(w, "actions:")
		for _, action := range result.Actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	if len(result.SectionErrors) > 0 {
		fmt.Fprintln(w, "section errors:")
		keys := make([]string, 0, len(result.SectionErrors))
		for key := range result.SectionErrors {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(w, "  %s: %s\n", key, result.SectionErrors[key])
		}
	}
	return nil
}

func parseOverviewFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("overview-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderOverviewFormat(w io.Writer, result *overviewResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
