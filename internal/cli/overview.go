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

	"github.com/jamesaud/agent-team/internal/daemon"
	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newOverviewCmd() *cobra.Command {
	var (
		target        string
		jsonOut       bool
		commands      bool
		lastMessage   bool
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
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team overview: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team overview: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team overview: --commands cannot be combined with --watch.")
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
					return overviewResultWithLastMessageActions(collectOverview(teamDir, now, scheduleLimit), lastMessage), nil
				}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			result := overviewResultWithLastMessageActions(collectOverview(teamDir, time.Now().UTC(), scheduleLimit), lastMessage)
			if commands {
				return renderOverviewCommands(cmd.OutOrStdout(), result, operatorCommandScopeFromCommand(cmd, target, "target"))
			}
			return renderOverview(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit overview as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended actions, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&lastMessage, "last-message", false, "When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.")
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
		commands      bool
		lastMessage   bool
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
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team overview: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team overview: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team overview: --commands cannot be combined with --watch.")
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
					result, err := collectTeamOverview(teamDir, args[0], now, scheduleLimit)
					if err != nil {
						return nil, err
					}
					return overviewResultWithLastMessageActions(result, lastMessage), nil
				}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			result, err := collectTeamOverview(teamDir, args[0], time.Now().UTC(), scheduleLimit)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team overview: %v\n", err)
				return exitErr(1)
			}
			result = overviewResultWithLastMessageActions(result, lastMessage)
			if commands {
				return renderOverviewCommands(cmd.OutOrStdout(), result, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderOverview(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team overview as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended team actions, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&lastMessage, "last-message", false, "When runtime recovery actions use resume-plan log fallbacks, prefer clean Codex final-message commands.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team overview until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 5, "Upcoming team schedules to inspect after ordering; 0 means all.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team overview result with a Go template, e.g. '{{.Team.Name}} {{.State}}'.")
	return cmd
}

type overviewResult struct {
	OK                    bool                    `json:"ok"`
	State                 string                  `json:"state"`
	CapturedAt            string                  `json:"captured_at"`
	Team                  *teamInfo               `json:"team,omitempty"`
	Health                overviewHealthSummary   `json:"health"`
	Topology              *topologySummary        `json:"topology,omitempty"`
	Runtime               overviewRuntimeSummary  `json:"runtime"`
	Inbox                 overviewInboxSummary    `json:"inbox"`
	Jobs                  overviewJobSummary      `json:"jobs"`
	JobQuarantine         jobQuarantineSummary    `json:"job_quarantine"`
	Outbox                outboxSummary           `json:"outbox"`
	OutboxOwner           *overviewOutboxOwner    `json:"outbox_owner,omitempty"`
	OutboxQuarantine      outboxQuarantineSummary `json:"outbox_quarantine"`
	OutboxQuarantineOwner string                  `json:"outbox_quarantine_owner,omitempty"`
	Queue                 queueSummary            `json:"queue"`
	Pipelines             overviewPipelineSummary `json:"pipelines"`
	Schedules             overviewScheduleSummary `json:"schedules"`
	Intake                overviewIntakeSummary   `json:"intake"`
	Actions               []string                `json:"actions,omitempty"`
	ActionDetails         []operatorActionHint    `json:"action_details,omitempty"`
	SectionErrors         map[string]string       `json:"section_errors,omitempty"`
}

type operatorActionHint struct {
	Command string `json:"command"`
	Source  string `json:"source,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Team    string `json:"team,omitempty"`
}

type overviewOutboxOwner struct {
	PendingJob string `json:"pending_job,omitempty"`
	FailedJob  string `json:"failed_job,omitempty"`
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
	ExpiredHolds          int        `json:"expired_holds"`
	StaleRunning          int        `json:"stale_running,omitempty"`
	ReadySteps            int        `json:"ready_steps"`
	StatusPreviews        int        `json:"status_previews"`
	StatusChanges         int        `json:"status_changes"`
	BlockedStatusPreviews int        `json:"blocked_status_previews"`
}

type overviewRuntimeSummary struct {
	Total            int      `json:"total"`
	Running          int      `json:"running"`
	Stopped          int      `json:"stopped"`
	Exited           int      `json:"exited"`
	Crashed          int      `json:"crashed"`
	Unknown          int      `json:"unknown"`
	StaleRunning     int      `json:"stale_running,omitempty"`
	CrashedInstances []string `json:"crashed_instances,omitempty"`
	StaleInstances   []string `json:"stale_instances,omitempty"`
	CrashedPipelines []string `json:"crashed_pipelines,omitempty"`
	StalePipelines   []string `json:"stale_pipelines,omitempty"`
	crashedUnscoped  int
	staleUnscoped    int
}

type overviewInboxSummary struct {
	Instances       int      `json:"instances"`
	Total           int      `json:"total"`
	Unread          int      `json:"unread"`
	UnreadInstances int      `json:"unread_instances"`
	UnreadNames     []string `json:"unread_names,omitempty"`
}

type overviewPipelineSummary struct {
	Total              int `json:"total"`
	Jobs               int `json:"jobs"`
	ReadySteps         int `json:"ready_steps"`
	ParallelReadySteps int `json:"parallel_ready_steps,omitempty"`
	QueuedSteps        int `json:"queued_steps"`
	RunningSteps       int `json:"running_steps"`
	StaleRunningSteps  int `json:"stale_running_steps,omitempty"`
	BlockedSteps       int `json:"blocked_steps"`
	ManualGates        int `json:"manual_gates"`
	FailedSteps        int `json:"failed_steps"`
	DoneSteps          int `json:"done_steps"`
}

type overviewScheduleSummary struct {
	Declared int      `json:"declared"`
	Due      int      `json:"due"`
	Upcoming int      `json:"upcoming"`
	DueNames []string `json:"due_names,omitempty"`
}

type overviewIntakeSummary struct {
	Deliveries          int    `json:"deliveries"`
	Errors              int    `json:"errors"`
	Recovered           int    `json:"recovered"`
	Replayable          int    `json:"replayable"`
	DuplicateRequestIDs int    `json:"duplicate_request_ids,omitempty"`
	LatestErrorID       string `json:"latest_error_id,omitempty"`
	LatestError         string `json:"latest_error,omitempty"`
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

	if runtime, err := collectOverviewRuntime(teamDir); err != nil {
		out.addError("runtime", err)
	} else {
		out.Runtime = runtime
	}

	if inbox, err := collectOverviewInbox(teamDir, nil, nil); err != nil {
		out.addError("inbox", err)
	} else {
		out.Inbox = inbox
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

	if quarantine, err := listJobQuarantine(teamDir); err != nil {
		out.addError("job_quarantine", err)
	} else {
		out.JobQuarantine = summarizeJobQuarantineItems(quarantine)
	}

	if outbox, owner, err := collectOverviewOutbox(teamDir); err != nil {
		out.addError("outbox", err)
	} else {
		out.Outbox = outbox
		out.OutboxOwner = owner
	}

	if quarantine, owner, err := collectOverviewOutboxQuarantine(teamDir); err != nil {
		out.addError("outbox_quarantine", err)
	} else {
		out.OutboxQuarantine = quarantine
		out.OutboxQuarantineOwner = owner
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

	out.ActionDetails = overviewActionHints(out, health)
	out.Actions = overviewActionCommands(out.ActionDetails)
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

	if runtime, err := collectOverviewRuntimeForTeam(teamDir, top, team); err != nil {
		out.addError("runtime", err)
	} else {
		out.Runtime = runtime
	}

	if inbox, err := collectOverviewInbox(teamDir, top, team); err != nil {
		out.addError("inbox", err)
	} else {
		out.Inbox = inbox
	}

	if outbox, err := collectOverviewOutboxForTeam(teamDir, top, team); err != nil {
		out.addError("outbox", err)
	} else {
		out.Outbox = outbox
	}

	if quarantine, err := collectOverviewOutboxQuarantineForTeam(teamDir, top, team); err != nil {
		out.addError("outbox_quarantine", err)
	} else {
		out.OutboxQuarantine = quarantine
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

	out.ActionDetails = overviewActionHintsForScope(out, health, team.Name)
	out.Actions = overviewActionCommands(out.ActionDetails)
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
		ExpiredHolds:          countJobTriageReason(triage.Attention, "expired_hold"),
		StaleRunning:          countJobTriageReason(triage.Attention, "stale_running"),
		ReadySteps:            len(triage.ReadySteps),
		StatusPreviews:        len(triage.StatusPreviews),
		StatusChanges:         countChangedJobStatusPreviews(triage.StatusPreviews),
		BlockedStatusPreviews: countJobStatusPreviewsByAfter(triage.StatusPreviews, "blocked"),
	}
}

func collectOverviewRuntime(teamDir string) (overviewRuntimeSummary, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	jobs, err := jobstore.List(teamDir)
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	return overviewRuntimeFromMetadata(metas, jobs), nil
}

func collectOverviewRuntimeForTeam(teamDir string, top *topology.Topology, team *topology.Team) (overviewRuntimeSummary, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	jobs, err := jobstore.List(teamDir)
	if err != nil {
		return overviewRuntimeSummary{}, err
	}
	return overviewRuntimeFromMetadata(teamMetadata(top, team, metas), jobs), nil
}

func overviewRuntimeFromMetadata(metas []*daemon.Metadata, jobs []*jobstore.Job) overviewRuntimeSummary {
	out := overviewRuntimeSummary{}
	jobByInstance := overviewRuntimeJobByInstance(metas, jobs)
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		out.Total++
		switch meta.Status {
		case daemon.StatusRunning:
			out.Running++
		case daemon.StatusStopped:
			out.Stopped++
		case daemon.StatusExited:
			out.Exited++
		case daemon.StatusCrashed:
			out.Crashed++
			if strings.TrimSpace(meta.Instance) != "" {
				out.CrashedInstances = append(out.CrashedInstances, meta.Instance)
			}
			overviewRuntimeAddOwnership(&out.CrashedPipelines, &out.crashedUnscoped, jobByInstance[meta.Instance])
		default:
			out.Unknown++
		}
		if runtimeResumeMetadataIsStale(meta) {
			out.StaleRunning++
			if strings.TrimSpace(meta.Instance) != "" {
				out.StaleInstances = append(out.StaleInstances, meta.Instance)
			}
			overviewRuntimeAddOwnership(&out.StalePipelines, &out.staleUnscoped, jobByInstance[meta.Instance])
		}
	}
	sort.Strings(out.CrashedInstances)
	sort.Strings(out.StaleInstances)
	sort.Strings(out.CrashedPipelines)
	sort.Strings(out.StalePipelines)
	return out
}

func overviewRuntimeJobByInstance(metas []*daemon.Metadata, jobs []*jobstore.Job) map[string]*jobstore.Job {
	if len(metas) == 0 || len(jobs) == 0 {
		return nil
	}
	byInstance := map[string]*daemon.Metadata{}
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		byInstance[meta.Instance] = meta
	}
	out := map[string]*jobstore.Job{}
	for _, j := range jobs {
		for _, meta := range metadataForResumePlanJob(metas, byInstance, j) {
			if meta == nil || strings.TrimSpace(meta.Instance) == "" {
				continue
			}
			if out[meta.Instance] == nil {
				out[meta.Instance] = j
			}
		}
	}
	return out
}

func overviewRuntimeAddOwnership(pipelines *[]string, unscoped *int, j *jobstore.Job) {
	if j == nil {
		*unscoped = *unscoped + 1
		return
	}
	pipeline := strings.TrimSpace(j.Pipeline)
	if pipeline == "" {
		*unscoped = *unscoped + 1
		return
	}
	if !stringSliceContains(*pipelines, pipeline) {
		*pipelines = append(*pipelines, pipeline)
	}
}

func collectOverviewInbox(teamDir string, top *topology.Topology, team *topology.Team) (overviewInboxSummary, error) {
	daemonRoot := daemon.DaemonRoot(teamDir)
	instances, metaByInstance, err := listInboxInstances(daemonRoot)
	if err != nil {
		return overviewInboxSummary{}, err
	}
	if team != nil {
		instances = filterInboxInstancesForTeam(top, team, instances, metaByInstance)
	}
	rows, err := collectInboxSummaryRows(daemonRoot, instances, metaByInstance, false)
	if err != nil {
		return overviewInboxSummary{}, err
	}
	return overviewInboxFromRows(rows), nil
}

func overviewInboxFromRows(rows []inboxSummaryRow) overviewInboxSummary {
	out := overviewInboxSummary{Instances: len(rows)}
	for _, row := range rows {
		out.Total += row.Total
		out.Unread += row.Unread
		if row.Unread > 0 {
			out.UnreadInstances++
			out.UnreadNames = append(out.UnreadNames, row.Instance)
		}
	}
	sort.Strings(out.UnreadNames)
	return out
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
		out.ParallelReadySteps += row.ParallelReadySteps
		out.QueuedSteps += row.QueuedSteps
		out.RunningSteps += row.RunningSteps
		out.StaleRunningSteps += row.StaleRunningSteps
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
		Deliveries:          summary.Deliveries,
		Errors:              summary.Unresolved,
		Recovered:           summary.Recovered,
		Replayable:          summary.Replayable,
		DuplicateRequestIDs: summary.DuplicateRequestIDs,
		LatestErrorID:       summary.LatestErrorID,
		LatestError:         summary.LatestError,
	}
}

func collectOverviewOutbox(teamDir string) (outboxSummary, *overviewOutboxOwner, error) {
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return outboxSummary{}, nil, err
	}
	jobs, err := jobstore.List(teamDir)
	if err != nil {
		return outboxSummary{}, nil, err
	}
	return summarizeOutboxItems(items), overviewOutboxOwnerForItems(items, jobs), nil
}

func collectOverviewOutboxForTeam(teamDir string, top *topology.Topology, team *topology.Team) (outboxSummary, error) {
	jobs, err := jobstore.List(teamDir)
	if err != nil {
		return outboxSummary{}, err
	}
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return outboxSummary{}, err
	}
	return summarizeOutboxItems(teamOutboxItems(top, team, teamJobs(top, team, jobs), items)), nil
}

func collectOverviewOutboxQuarantine(teamDir string) (outboxQuarantineSummary, string, error) {
	items, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return outboxQuarantineSummary{}, "", err
	}
	jobs, err := jobstore.List(teamDir)
	if err != nil {
		return outboxQuarantineSummary{}, "", err
	}
	return summarizeOutboxQuarantineItems(items), overviewOutboxQuarantineSingleJob(items, jobs), nil
}

func collectOverviewOutboxQuarantineForTeam(teamDir string, top *topology.Topology, team *topology.Team) (outboxQuarantineSummary, error) {
	jobs, err := jobstore.List(teamDir)
	if err != nil {
		return outboxQuarantineSummary{}, err
	}
	items, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return outboxQuarantineSummary{}, err
	}
	return summarizeOutboxQuarantineItems(teamOutboxQuarantineItems(top, team, teamJobs(top, team, jobs), items)), nil
}

func overviewActions(out *overviewResult, health *healthResult) []string {
	return overviewActionCommands(overviewActionHints(out, health))
}

func overviewActionsForScope(out *overviewResult, health *healthResult, teamName string) []string {
	return overviewActionCommands(overviewActionHintsForScope(out, health, teamName))
}

func overviewActionHints(out *overviewResult, health *healthResult) []operatorActionHint {
	return overviewActionHintsForScope(out, health, "")
}

func overviewActionHintsForScope(out *overviewResult, health *healthResult, teamName string) []operatorActionHint {
	seen := map[string]bool{}
	var actions []operatorActionHint
	add := func(action, source, reason string) {
		action = strings.TrimSpace(action)
		if action == "" || seen[action] {
			return
		}
		seen[action] = true
		hint := operatorActionHint{
			Command: action,
			Source:  strings.TrimSpace(source),
			Reason:  strings.TrimSpace(reason),
		}
		if strings.TrimSpace(teamName) != "" {
			hint.Team = strings.TrimSpace(teamName)
		}
		actions = append(actions, hint)
	}

	if health != nil && !health.Healthy {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team repair %s --dry-run --jobs", teamName), "health", "unhealthy")
		} else {
			add("agent-team repair --dry-run --jobs", "health", "unhealthy")
		}
	}
	if out.Runtime.Crashed > 0 {
		add(overviewRuntimeResumePlanAction(out.Runtime, teamName), "runtime", fmt.Sprintf("crashed=%d", out.Runtime.Crashed))
	}
	if out.Runtime.StaleRunning > 0 {
		add(overviewRuntimeStaleResumePlanAction(out.Runtime, teamName), "runtime", fmt.Sprintf("stale=%d", out.Runtime.StaleRunning))
	}
	if out.Inbox.Unread > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team inbox ls --team %s --unread", teamName), "inbox", fmt.Sprintf("unread=%d", out.Inbox.Unread))
		} else {
			add("agent-team inbox ls --unread", "inbox", fmt.Sprintf("unread=%d", out.Inbox.Unread))
		}
	}
	if health != nil {
		for _, issue := range health.Issues {
			if issue.Code == "daemon_not_running" || issue.Code == "daemon_not_ready" {
				add("agent-team daemon start", "health", issue.Code)
			}
			if issue.Code == "declared_missing" || issue.Code == "declared_not_running" {
				if teamName != "" {
					add(fmt.Sprintf("agent-team team sync %s --dry-run", teamName), "health", issue.Code)
				} else {
					add("agent-team sync --dry-run", "health", issue.Code)
				}
			}
			for _, action := range issue.Actions {
				action = overviewIssueAction(action)
				if out != nil && out.OutboxQuarantine.Quarantined > 0 && overviewOutboxQuarantinePrimaryAction(action) {
					continue
				}
				add(action, overviewIssueActionSource(action, issue.Code), issue.Code)
			}
		}
	}
	if out.Topology != nil && !out.Topology.OK {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team doctor %s", teamName), "topology", "topology_not_ok")
		} else {
			add("agent-team topology summary", "topology", "topology_not_ok")
			add("agent-team pipeline doctor --all", "topology", "topology_not_ok")
			add("agent-team team doctor --all", "topology", "topology_not_ok")
		}
	}
	if out.Queue.Dead > 0 {
		add(overviewQueueDeadRetryAction(health, teamName), "queue", fmt.Sprintf("dead=%d", out.Queue.Dead))
	}
	if out.Queue.Pending > out.Queue.Delayed {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team tick %s --dry-run --skip-schedules --skip-advance", teamName), "queue", fmt.Sprintf("ready_pending=%d", out.Queue.Pending-out.Queue.Delayed))
		} else {
			add("agent-team queue drain --dry-run", "queue", fmt.Sprintf("ready_pending=%d", out.Queue.Pending-out.Queue.Delayed))
		}
	}
	if out.Queue.Quarantined > 0 {
		add(overviewQueueQuarantineAction(health, teamName), "queue", fmt.Sprintf("quarantined=%d", out.Queue.Quarantined))
	}
	if out.OutboxQuarantine.Quarantined > 0 {
		add(overviewOutboxQuarantineAction(out, health, teamName), "outbox", fmt.Sprintf("quarantined=%d", out.OutboxQuarantine.Quarantined))
	}
	if out.Outbox.Failed > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team outbox %s --state failed", teamName), "outbox", fmt.Sprintf("failed=%d", out.Outbox.Failed))
		} else if jobID := overviewOutboxOwnerJob(out.OutboxOwner, daemon.OutboxStateFailed); jobID != "" {
			add(fmt.Sprintf("agent-team job outbox %s --state failed", jobID), "outbox", fmt.Sprintf("failed=%d", out.Outbox.Failed))
		} else {
			add("agent-team outbox ls --state failed", "outbox", fmt.Sprintf("failed=%d", out.Outbox.Failed))
		}
	}
	if out.Outbox.Pending > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team outbox %s --state pending", teamName), "outbox", fmt.Sprintf("pending=%d", out.Outbox.Pending))
		} else if jobID := overviewOutboxOwnerJob(out.OutboxOwner, daemon.OutboxStatePending); jobID != "" {
			add(fmt.Sprintf("agent-team job outbox %s --state pending", jobID), "outbox", fmt.Sprintf("pending=%d", out.Outbox.Pending))
		} else {
			add("agent-team outbox drain --dry-run", "outbox", fmt.Sprintf("pending=%d", out.Outbox.Pending))
		}
	}
	if out.Jobs.Attention > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team triage %s", teamName), "jobs", fmt.Sprintf("attention=%d", out.Jobs.Attention))
		} else {
			add("agent-team job triage", "jobs", fmt.Sprintf("attention=%d", out.Jobs.Attention))
		}
	}
	if out.Jobs.CleanupReady > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team cleanup %s --dry-run", teamName), "jobs", fmt.Sprintf("cleanup_ready=%d", out.Jobs.CleanupReady))
		} else {
			add("agent-team job cleanup --all --dry-run", "jobs", fmt.Sprintf("cleanup_ready=%d", out.Jobs.CleanupReady))
		}
	}
	if out.Jobs.ExpiredHolds > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team release %s --expired --dry-run", teamName), "jobs", fmt.Sprintf("expired_holds=%d", out.Jobs.ExpiredHolds))
		} else {
			add("agent-team job release --all --expired --dry-run", "jobs", fmt.Sprintf("expired_holds=%d", out.Jobs.ExpiredHolds))
		}
	}
	if out.Jobs.StaleRunning > 0 {
		reason := fmt.Sprintf("stale_running=%d", out.Jobs.StaleRunning)
		if teamName != "" {
			add(fmt.Sprintf("agent-team team repair %s --timeout-jobs --dry-run", teamName), "jobs", reason)
		} else {
			add("agent-team repair --timeout-jobs --dry-run", "jobs", reason)
		}
	}
	if out.Jobs.StatusChanges > 0 {
		add("agent-team job reconcile status --dry-run", "jobs", fmt.Sprintf("status_changes=%d", out.Jobs.StatusChanges))
	}
	if out.Jobs.ReadySteps > 0 || out.Pipelines.ReadySteps > 0 {
		reason := fmt.Sprintf("ready_steps=%d", out.Jobs.ReadySteps+out.Pipelines.ReadySteps)
		if teamName != "" {
			add(teamTickPreviewAction(teamName, false), "pipelines", reason)
		} else {
			add("agent-team tick --dry-run --preview-routes", "pipelines", reason)
		}
	}
	if out.Pipelines.ParallelReadySteps > 1 {
		reason := fmt.Sprintf("parallel_ready_steps=%d", out.Pipelines.ParallelReadySteps)
		if teamName != "" {
			add(teamTickPreviewAction(teamName, true), "pipelines", reason)
		} else {
			add("agent-team tick --all-ready-steps --dry-run --preview-routes", "pipelines", reason)
		}
	}
	if out.Pipelines.ManualGates > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team approve %s --dry-run --dispatch --preview-routes", teamName), "pipelines", fmt.Sprintf("manual_gates=%d", out.Pipelines.ManualGates))
		} else {
			add("agent-team pipeline approve --all --dry-run --dispatch --preview-routes", "pipelines", fmt.Sprintf("manual_gates=%d", out.Pipelines.ManualGates))
		}
	}
	if out.Pipelines.StaleRunningSteps > 0 {
		reason := fmt.Sprintf("stale_running_steps=%d", out.Pipelines.StaleRunningSteps)
		add("agent-team job reconcile events --dry-run", "pipelines", reason)
		if teamName != "" {
			add(fmt.Sprintf("agent-team team timeout %s --dry-run", teamName), "pipelines", reason)
			add(fmt.Sprintf("agent-team team repair %s --timeout-jobs --dry-run", teamName), "pipelines", reason)
			add(fmt.Sprintf("agent-team team explain %s --state running", teamName), "pipelines", reason)
		} else {
			add("agent-team pipeline timeout --all --dry-run", "pipelines", reason)
			add("agent-team repair --timeout-jobs --dry-run", "pipelines", reason)
			add("agent-team pipeline explain --all --state running", "pipelines", reason)
		}
	}
	if out.Schedules.Due > 0 {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team tick %s --dry-run --skip-drain --skip-advance", teamName), "schedules", fmt.Sprintf("due=%d", out.Schedules.Due))
		} else {
			add("agent-team schedule fire --dry-run --preview-triggers", "schedules", fmt.Sprintf("due=%d", out.Schedules.Due))
		}
	}
	if overviewHasDrainableWorkForScope(out, teamName) {
		if teamName != "" {
			add(fmt.Sprintf("agent-team team drain %s", teamName), "overview", overviewDrainableWorkReasonForScope(out, teamName))
		} else {
			add("agent-team drain", "overview", overviewDrainableWorkReasonForScope(out, teamName))
		}
	}
	if teamName == "" && out.Intake.Errors > 0 {
		add("agent-team intake summary", "intake", fmt.Sprintf("errors=%d", out.Intake.Errors))
		add("agent-team intake deliveries --status error", "intake", fmt.Sprintf("errors=%d", out.Intake.Errors))
		if out.Intake.Replayable > 0 {
			add(intakeReplayAllDryRunAction(), "intake", fmt.Sprintf("replayable=%d", out.Intake.Replayable))
		}
	}
	if teamName == "" && out.Intake.DuplicateRequestIDs > 0 {
		add("agent-team intake duplicates", "intake", fmt.Sprintf("duplicate_request_ids=%d", out.Intake.DuplicateRequestIDs))
	}
	if len(out.SectionErrors) > 0 {
		if teamName == "" && strings.TrimSpace(out.SectionErrors["intake"]) != "" {
			add("agent-team intake doctor", "section_errors", "intake")
		}
		if overviewHasQueueSectionError(out) {
			add("agent-team queue doctor", "section_errors", "queue")
		}
		if teamName != "" {
			add(fmt.Sprintf("agent-team team snapshot %s --json", teamName), "section_errors", fmt.Sprintf("count=%d", len(out.SectionErrors)))
		} else {
			add("agent-team snapshot --json", "section_errors", fmt.Sprintf("count=%d", len(out.SectionErrors)))
		}
	}
	return actions
}

func overviewHasDrainableWork(out *overviewResult) bool {
	return overviewHasDrainableWorkForScope(out, "")
}

func overviewHasDrainableWorkForScope(out *overviewResult, teamName string) bool {
	if out == nil {
		return false
	}
	outboxPending := out.Outbox.Pending
	if strings.TrimSpace(teamName) != "" {
		outboxPending = 0
	}
	return out.Queue.Pending > out.Queue.Delayed ||
		outboxPending > 0 ||
		out.Jobs.StatusChanges > 0 ||
		out.Jobs.ReadySteps > 0 ||
		out.Pipelines.ReadySteps > 0 ||
		out.Schedules.Due > 0
}

func overviewDrainableWorkReason(out *overviewResult) string {
	return overviewDrainableWorkReasonForScope(out, "")
}

func overviewDrainableWorkReasonForScope(out *overviewResult, teamName string) string {
	if out == nil {
		return "drainable_work"
	}
	total := 0
	if out.Queue.Pending > out.Queue.Delayed {
		total += out.Queue.Pending - out.Queue.Delayed
	}
	if strings.TrimSpace(teamName) == "" {
		total += out.Outbox.Pending
	}
	total += out.Jobs.StatusChanges
	total += out.Jobs.ReadySteps
	total += out.Pipelines.ReadySteps
	total += out.Schedules.Due
	if total <= 0 {
		return "drainable_work"
	}
	return fmt.Sprintf("drainable_work=%d", total)
}

func overviewActionCommands(hints []operatorActionHint) []string {
	if len(hints) == 0 {
		return nil
	}
	commands := make([]string, 0, len(hints))
	for _, hint := range hints {
		if strings.TrimSpace(hint.Command) == "" {
			continue
		}
		commands = append(commands, hint.Command)
	}
	return commands
}

func overviewResultWithLastMessageActions(out *overviewResult, lastMessage bool) *overviewResult {
	if out == nil || !lastMessage {
		return out
	}
	if len(out.ActionDetails) == 0 {
		details := make([]operatorActionHint, 0, len(out.Actions))
		for _, action := range out.Actions {
			details = append(details, operatorActionHint{Command: action, Source: "overview"})
		}
		out.ActionDetails = details
	}
	out.ActionDetails = operatorActionHintsWithLastMessage(out.ActionDetails)
	out.Actions = overviewActionCommands(out.ActionDetails)
	return out
}

func operatorActionHintsWithLastMessage(hints []operatorActionHint) []operatorActionHint {
	if len(hints) == 0 {
		return nil
	}
	out := make([]operatorActionHint, 0, len(hints))
	for _, hint := range hints {
		hint.Command = operatorActionWithLastMessage(hint.Command)
		out = append(out, hint)
	}
	return out
}

func operatorActionWithLastMessage(action string) string {
	command := strings.TrimSpace(action)
	if command == "" || strings.Contains(command, "--last-message") || !operatorActionIsResumePlan(command) {
		return action
	}
	return command + " --last-message"
}

func operatorActionIsResumePlan(command string) bool {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "agent-team" {
		return false
	}
	idx := operatorActionSubcommandIndex(fields)
	if idx >= len(fields) {
		return false
	}
	switch fields[idx] {
	case "resume-plan":
		return true
	case "runtime", "job", "pipeline":
		return idx+1 < len(fields) && fields[idx+1] == "resume-plan"
	case "team":
		return (idx+1 < len(fields) && fields[idx+1] == "resume-plan") ||
			(idx+2 < len(fields) && fields[idx+1] == "runtime" && fields[idx+2] == "resume-plan")
	default:
		return false
	}
}

func operatorActionSubcommandIndex(fields []string) int {
	idx := 1
	for idx < len(fields) {
		field := fields[idx]
		if !strings.HasPrefix(field, "-") {
			return idx
		}
		if field == "--repo" || field == "--target" {
			idx += 2
			continue
		}
		idx++
	}
	return idx
}

func overviewOutboxOwnerForItems(items []*daemon.OutboxItem, jobs []*jobstore.Job) *overviewOutboxOwner {
	owner := &overviewOutboxOwner{
		PendingJob: overviewOutboxSingleJobForState(items, jobs, daemon.OutboxStatePending),
		FailedJob:  overviewOutboxSingleJobForState(items, jobs, daemon.OutboxStateFailed),
	}
	if owner.PendingJob == "" && owner.FailedJob == "" {
		return nil
	}
	return owner
}

func overviewOutboxSingleJobForState(items []*daemon.OutboxItem, jobs []*jobstore.Job, state string) string {
	var owner string
	for _, item := range items {
		if item == nil || item.State != state {
			continue
		}
		jobID := overviewOutboxItemJobID(item, jobs)
		if jobID == "" {
			return ""
		}
		if owner == "" {
			owner = jobID
			continue
		}
		if owner != jobID {
			return ""
		}
	}
	return owner
}

func overviewOutboxItemJobID(item *daemon.OutboxItem, jobs []*jobstore.Job) string {
	if item == nil {
		return ""
	}
	itemJobID := normalizeOutboxJob(outboxItemJob(item))
	itemName := strings.TrimSpace(outboxPayloadString(item.Payload, "name"))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if itemJobID != "" && itemJobID == j.ID {
			return j.ID
		}
		if itemName != "" && strings.TrimSpace(j.Instance) != "" && itemName == j.Instance {
			return j.ID
		}
	}
	return ""
}

func overviewOutboxOwnerJob(owner *overviewOutboxOwner, state string) string {
	if owner == nil {
		return ""
	}
	switch state {
	case daemon.OutboxStatePending:
		return strings.TrimSpace(owner.PendingJob)
	case daemon.OutboxStateFailed:
		return strings.TrimSpace(owner.FailedJob)
	default:
		return ""
	}
}

func overviewOutboxQuarantineSingleJob(items []outboxQuarantineItem, jobs []*jobstore.Job) string {
	var owner string
	for _, item := range items {
		var matched string
		for _, j := range jobs {
			if j == nil {
				continue
			}
			if outboxQuarantineItemMatchesJob(item, j) {
				matched = j.ID
				break
			}
		}
		if matched == "" {
			return ""
		}
		if owner == "" {
			owner = matched
			continue
		}
		if owner != matched {
			return ""
		}
	}
	return owner
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
		return teamQueueRetryAllRecoveryAction(teamName, true)
	}
	return globalQueueRetryAllRecoveryAction(true)
}

func overviewQueueRetryDryRunAction(action, teamName string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(action, "agent-team job queue retry "):
		return queueRetryRecoveryDryRunAction(action)
	case strings.HasPrefix(action, "agent-team pipeline queue retry "):
		return queueRetryRecoveryDryRunAction(action)
	case teamName != "" && strings.HasPrefix(action, fmt.Sprintf("agent-team team queue retry %s ", teamName)):
		return queueRetryRecoveryDryRunAction(action)
	case teamName == "" && strings.HasPrefix(action, "agent-team queue retry "):
		return queueRetryRecoveryDryRunAction(action)
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
	if isQueueRetryAction(action) {
		return queueRetryRecoveryDryRunAction(action)
	}
	if overviewIssueActionPrefersDryRun(action) {
		return appendDryRunFlag(action)
	}
	return action
}

func overviewOutboxQuarantinePrimaryAction(action string) bool {
	action = strings.TrimSpace(action)
	switch {
	case action == "agent-team outbox quarantine ls":
		return true
	case strings.HasPrefix(action, "agent-team job outbox quarantine "),
		strings.HasPrefix(action, "agent-team pipeline outbox quarantine "),
		strings.HasPrefix(action, "agent-team team outbox quarantine "):
		return !strings.Contains(action, " --")
	default:
		return false
	}
}

func overviewIssueActionSource(action, code string) string {
	action = strings.TrimSpace(action)
	code = strings.TrimSpace(code)
	switch {
	case strings.HasPrefix(code, "queue_") || strings.Contains(action, " queue "):
		return "queue"
	case strings.HasPrefix(code, "outbox_") || strings.Contains(action, " outbox "):
		return "outbox"
	case strings.HasPrefix(code, "job_") || strings.HasPrefix(action, "agent-team job "):
		return "jobs"
	case strings.HasPrefix(code, "pipeline_") || strings.HasPrefix(action, "agent-team pipeline "):
		return "pipelines"
	case strings.HasPrefix(code, "intake_") || strings.HasPrefix(action, "agent-team intake "):
		return "intake"
	case strings.HasPrefix(code, "topology_") || strings.HasPrefix(action, "agent-team topology ") || strings.Contains(action, " doctor"):
		return "topology"
	default:
		return "health"
	}
}

func overviewIssueActionPrefersDryRun(action string) bool {
	switch {
	case action == "agent-team repair" || strings.HasPrefix(action, "agent-team repair "):
		return true
	case action == "agent-team queue retry" || strings.HasPrefix(action, "agent-team queue retry "):
		return true
	case strings.HasPrefix(action, "agent-team job queue retry "):
		return true
	case strings.HasPrefix(action, "agent-team pipeline queue retry "):
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

func overviewOutboxQuarantineAction(out *overviewResult, health *healthResult, teamName string) string {
	if teamName != "" {
		return fmt.Sprintf("agent-team team outbox quarantine %s", teamName)
	}
	if health != nil {
		for _, issue := range health.Issues {
			if issue.Code != "outbox_quarantined" {
				continue
			}
			for _, action := range issue.Actions {
				action = strings.TrimSpace(action)
				if strings.HasPrefix(action, "agent-team job outbox quarantine ") ||
					strings.HasPrefix(action, "agent-team pipeline outbox quarantine ") ||
					action == "agent-team outbox quarantine ls" {
					return action
				}
			}
		}
	}
	if out != nil && strings.TrimSpace(out.OutboxQuarantineOwner) != "" {
		return fmt.Sprintf("agent-team job outbox quarantine %s", strings.TrimSpace(out.OutboxQuarantineOwner))
	}
	return "agent-team outbox quarantine ls"
}

func overviewRuntimeResumePlanAction(summary overviewRuntimeSummary, teamName string) string {
	flag := runtimeResumePlanHintFlag("--status crashed")
	if teamName != "" {
		return fmt.Sprintf("agent-team team resume-plan %s %s", teamName, flag)
	}
	return overviewRuntimePipelineResumeAction(summary.CrashedPipelines, summary.crashedUnscoped, flag)
}

func overviewRuntimeStaleResumePlanAction(summary overviewRuntimeSummary, teamName string) string {
	flag := runtimeResumePlanHintFlag("--runtime-stale")
	if teamName != "" {
		return fmt.Sprintf("agent-team team resume-plan %s %s", teamName, flag)
	}
	return overviewRuntimePipelineResumeAction(summary.StalePipelines, summary.staleUnscoped, flag)
}

func overviewRuntimePipelineResumeAction(pipelines []string, unscoped int, flag string) string {
	flag = strings.TrimSpace(flag)
	if unscoped == 0 {
		switch len(pipelines) {
		case 1:
			return fmt.Sprintf("agent-team pipeline resume-plan %s %s", pipelines[0], flag)
		default:
			if len(pipelines) > 1 {
				return fmt.Sprintf("agent-team pipeline resume-plan --all %s", flag)
			}
		}
	}
	return fmt.Sprintf("agent-team resume-plan %s", flag)
}

func runtimeResumePlanHintFlag(flag string) string {
	flag = strings.TrimSpace(flag)
	switch flag {
	case "--runtime-stale":
		return flag + " --sort stale --limit 10"
	case "--status crashed":
		return flag + " --sort action --limit 10"
	default:
		return flag
	}
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
		out.JobQuarantine.Quarantined == 0 &&
		out.Outbox.Pending == 0 &&
		out.Outbox.Failed == 0 &&
		out.OutboxQuarantine.Quarantined == 0 &&
		out.Inbox.Unread == 0 &&
		out.Jobs.Attention == 0 &&
		out.Jobs.ReadySteps == 0 &&
		out.Jobs.StatusChanges == 0 &&
		out.Pipelines.ReadySteps == 0 &&
		out.Pipelines.StaleRunningSteps == 0 &&
		out.Pipelines.BlockedSteps == 0 &&
		out.Pipelines.FailedSteps == 0 &&
		out.Schedules.Due == 0 &&
		out.Intake.Errors == 0 &&
		out.Intake.DuplicateRequestIDs == 0
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
		out.JobQuarantine.Quarantined > 0 ||
		out.Outbox.Failed > 0 ||
		out.OutboxQuarantine.Quarantined > 0 ||
		out.Jobs.Attention > 0 ||
		out.Pipelines.BlockedSteps > 0 ||
		out.Pipelines.FailedSteps > 0 ||
		out.Pipelines.StaleRunningSteps > 0 ||
		out.Intake.Errors > 0 ||
		out.Intake.DuplicateRequestIDs > 0 {
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
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
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
	fmt.Fprintf(w, "runtime: total=%d running=%d stopped=%d exited=%d crashed=%d unknown=%d stale_running=%d\n",
		result.Runtime.Total,
		result.Runtime.Running,
		result.Runtime.Stopped,
		result.Runtime.Exited,
		result.Runtime.Crashed,
		result.Runtime.Unknown,
		result.Runtime.StaleRunning)
	fmt.Fprintf(w, "inbox: instances=%d total=%d unread=%d unread_instances=%d\n",
		result.Inbox.Instances,
		result.Inbox.Total,
		result.Inbox.Unread,
		result.Inbox.UnreadInstances)
	fmt.Fprintf(w, "jobs: total=%d queued=%d running=%d blocked=%d done=%d failed=%d attention=%d cleanup_ready=%d expired_holds=%d stale_running=%d ready_steps=%d status_changes=%d\n",
		result.Jobs.Summary.Total,
		result.Jobs.Summary.Queued,
		result.Jobs.Summary.Running,
		result.Jobs.Summary.Blocked,
		result.Jobs.Summary.Done,
		result.Jobs.Summary.Failed,
		result.Jobs.Attention,
		result.Jobs.CleanupReady,
		result.Jobs.ExpiredHolds,
		result.Jobs.StaleRunning,
		result.Jobs.ReadySteps,
		result.Jobs.StatusChanges)
	if result.JobQuarantine.Quarantined > 0 {
		fmt.Fprintln(w, jobQuarantineSummaryLine(result.JobQuarantine))
	}
	fmt.Fprintln(w, queueSummaryLine(result.Queue))
	fmt.Fprintf(w, "outbox: total=%d pending=%d failed=%d processed=%d\n",
		result.Outbox.Total,
		result.Outbox.Pending,
		result.Outbox.Failed,
		result.Outbox.Processed)
	if result.OutboxQuarantine.Quarantined > 0 {
		fmt.Fprintln(w, outboxQuarantineSummaryLine(result.OutboxQuarantine))
	}
	fmt.Fprintf(w, "pipelines: total=%d jobs=%d ready_steps=%d parallel_ready_steps=%d stale_running_steps=%d blocked_steps=%d failed_steps=%d\n",
		result.Pipelines.Total,
		result.Pipelines.Jobs,
		result.Pipelines.ReadySteps,
		result.Pipelines.ParallelReadySteps,
		result.Pipelines.StaleRunningSteps,
		result.Pipelines.BlockedSteps,
		result.Pipelines.FailedSteps)
	fmt.Fprintf(w, "schedules: declared=%d due=%d upcoming=%d\n",
		result.Schedules.Declared,
		result.Schedules.Due,
		result.Schedules.Upcoming)
	fmt.Fprintf(w, "intake: deliveries=%d errors=%d recovered=%d replayable=%d duplicate_request_ids=%d latest_error=%s\n",
		result.Intake.Deliveries,
		result.Intake.Errors,
		result.Intake.Recovered,
		result.Intake.Replayable,
		result.Intake.DuplicateRequestIDs,
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

func renderOverviewCommands(w io.Writer, result *overviewResult, scope operatorCommandScope) error {
	if result == nil {
		return nil
	}
	return renderOperatorActionCommands(w, result.Actions, scope)
}

type operatorCommandScope struct {
	Repo string
	Set  bool
}

func renderOperatorActionCommands(w io.Writer, actions []string, scope operatorCommandScope) error {
	return renderActionCommands(w, scopedOperatorActions(commandActionsOnly(actions), scope))
}

func scopedOperatorActions(actions []string, scope operatorCommandScope) []string {
	if len(actions) == 0 {
		return nil
	}
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		out = append(out, scopedOperatorAction(action, scope))
	}
	return out
}

func scopedOperatorAction(action string, scope operatorCommandScope) string {
	action = strings.TrimSpace(action)
	if action == "" || !scope.Set || strings.TrimSpace(scope.Repo) == "" {
		return action
	}
	fields := strings.Fields(action)
	if len(fields) == 0 || fields[0] != "agent-team" {
		return action
	}
	if operatorActionHasRepoScope(fields) {
		return action
	}
	prefix := strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", scope.Repo}), " ")
	remainder := strings.TrimSpace(action[len("agent-team"):])
	if remainder == "" {
		return prefix
	}
	return prefix + " " + remainder
}

func operatorActionHasRepoScope(fields []string) bool {
	for _, field := range fields[1:] {
		switch {
		case field == "--repo", field == "--target":
			return true
		case strings.HasPrefix(field, "--repo="), strings.HasPrefix(field, "--target="):
			return true
		}
	}
	return false
}

func operatorCommandScopeFromCommand(cmd *cobra.Command, target string, localFlag string) operatorCommandScope {
	if cmd == nil {
		return operatorCommandScope{}
	}
	if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
		if value := strings.TrimSpace(flag.Value.String()); value != "" {
			return operatorCommandScope{Repo: value, Set: true}
		}
	}
	if flagName := strings.TrimSpace(localFlag); flagName != "" && cmd.Flags().Changed(flagName) {
		if value := strings.TrimSpace(target); value != "" {
			return operatorCommandScope{Repo: value, Set: true}
		}
	}
	return operatorCommandScope{}
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
