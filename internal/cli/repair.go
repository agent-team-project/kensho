package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

func newRepairCmd() *cobra.Command {
	var (
		target           string
		workspace        string
		limit            int
		dryRun           bool
		previewRoutes    bool
		jsonOut          bool
		format           string
		skipDaemon       bool
		skipQueue        bool
		skipTick         bool
		includeJobs      bool
		timeoutJobs      bool
		timeoutPipelines bool
		retryPipelines   bool
		allReadySteps    bool
		timeoutStep      string
		timeoutMessage   string
		retryStep        string
		retryMessage     string
		untilIdle        bool
		readyTimeout     time.Duration
		interval         time.Duration
		maxCycles        int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Recover common unhealthy orchestration state.",
		Long: "Recover common unhealthy orchestration state: ensure the daemon is ready, retry dead-letter queue items, " +
			"optionally time out stale job work, optionally retry failed pipeline steps, and run a maintenance tick to drain ready work and advance pipelines. Use --dry-run to preview.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --limit must be >= 0.")
				return exitErr(2)
			}
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --interval must be >= 0.")
				return exitErr(2)
			}
			if maxCycles <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --max-cycles must be > 0.")
				return exitErr(2)
			}
			if cmd.Flags().Changed("max-cycles") && !untilIdle {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --max-cycles requires --until-idle.")
				return exitErr(2)
			}
			if untilIdle && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --until-idle cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			if untilIdle && skipTick {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --until-idle cannot be combined with --skip-tick.")
				return exitErr(2)
			}
			if retryPipelines && skipDaemon && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --retry-pipelines requires daemon access unless --dry-run is set.")
				return exitErr(2)
			}
			if timeoutJobs && timeoutPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --timeout-jobs cannot be combined with --timeout-pipelines.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutMessage) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --timeout-message requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutStep) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --timeout-step requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(retryMessage) != "" && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --retry-message requires --retry-pipelines.")
				return exitErr(2)
			}
			if strings.TrimSpace(retryStep) != "" && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --retry-step requires --retry-pipelines.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseRepairFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team repair: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := runRepair(cmd, target, teamDir, repairOptions{
				Workspace:        workspace,
				Limit:            limit,
				DryRun:           dryRun,
				PreviewRoutes:    previewRoutes,
				SkipDaemon:       skipDaemon,
				SkipQueue:        skipQueue,
				SkipTick:         skipTick,
				IncludeJobs:      includeJobs,
				TimeoutJobs:      timeoutJobs,
				TimeoutPipelines: timeoutPipelines,
				RetryPipelines:   retryPipelines,
				AllReadySteps:    allReadySteps,
				TimeoutStep:      timeoutStep,
				TimeoutMessage:   timeoutMessage,
				RetryStep:        retryStep,
				RetryMessage:     retryMessage,
				UntilIdle:        untilIdle,
				ReadyTimeout:     readyTimeout,
				Interval:         interval,
				MaxCycles:        maxCycles,
				CollectHealth:    true,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team repair: %v\n", err)
				return exitErr(1)
			}
			return renderRepairResult(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for retried or advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Retry at most this many dead-letter queue items or failed pipeline jobs, and advance at most this many ready pipeline jobs or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview repair actions without mutating state or starting the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include route and dispatch payload previews for retried or ready pipeline steps.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the repair result with a Go template, e.g. '{{.DryRun}} {{.Queue.Action}}'.")
	cmd.Flags().BoolVar(&skipDaemon, "skip-daemon", false, "Do not start or reconcile the daemon.")
	cmd.Flags().BoolVar(&skipQueue, "skip-queue", false, "Do not retry dead-letter queue items.")
	cmd.Flags().BoolVar(&skipTick, "skip-tick", false, "Do not run a maintenance tick after queue retry.")
	cmd.Flags().BoolVar(&includeJobs, "jobs", false, "Include durable job triage and status-file previews in health snapshots.")
	cmd.Flags().BoolVar(&timeoutJobs, "timeout-jobs", false, "Mark stale running durable job work failed before retrying failed pipeline steps.")
	cmd.Flags().BoolVar(&timeoutPipelines, "timeout-pipelines", false, "Mark stale running pipeline steps failed before retrying failed pipeline steps.")
	cmd.Flags().BoolVar(&retryPipelines, "retry-pipelines", false, "Reset failed pipeline steps and dispatch them before the maintenance tick.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent pipeline step during the repair tick.")
	cmd.Flags().StringVar(&timeoutStep, "timeout-step", "", "With --timeout-jobs or --timeout-pipelines, mark only stale running steps with this id failed.")
	cmd.Flags().StringVar(&timeoutMessage, "timeout-message", "", "Audit message to record when timeout repair marks stale work failed.")
	cmd.Flags().StringVar(&retryStep, "retry-step", "", "With --retry-pipelines, retry only failed jobs whose next failed step has this id.")
	cmd.Flags().StringVar(&retryMessage, "retry-message", "", "Audit message to record when --retry-pipelines resets failed steps.")
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Run maintenance ticks until no immediate queue, schedule, or pipeline work remains.")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Delay between --until-idle maintenance cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "With --until-idle, stop after this many cycles if work keeps appearing.")
	return cmd
}

type repairOptions struct {
	Workspace        string
	Limit            int
	DryRun           bool
	PreviewRoutes    bool
	SkipDaemon       bool
	SkipQueue        bool
	SkipTick         bool
	IncludeJobs      bool
	TimeoutJobs      bool
	TimeoutPipelines bool
	RetryPipelines   bool
	AllReadySteps    bool
	TimeoutStep      string
	TimeoutMessage   string
	RetryStep        string
	RetryMessage     string
	UntilIdle        bool
	ReadyTimeout     time.Duration
	Interval         time.Duration
	MaxCycles        int
	CollectHealth    bool
}

type repairResult struct {
	DryRun          bool                      `json:"dry_run,omitempty"`
	HealthBefore    *healthResult             `json:"health_before,omitempty"`
	Daemon          repairStepResult          `json:"daemon"`
	Queue           repairQueueStep           `json:"queue"`
	Intake          repairIntakeStep          `json:"intake"`
	JobTimeout      repairPipelineTimeoutStep `json:"job_timeout"`
	PipelineTimeout repairPipelineTimeoutStep `json:"pipeline_timeout"`
	PipelineRetry   repairPipelineRetryStep   `json:"pipeline_retry"`
	Tick            repairTickStep            `json:"tick"`
	HealthAfter     *healthResult             `json:"health_after,omitempty"`
}

type repairStepResult struct {
	Action    string                   `json:"action"`
	Reason    string                   `json:"reason,omitempty"`
	Running   bool                     `json:"running,omitempty"`
	Ready     bool                     `json:"ready,omitempty"`
	PID       int                      `json:"pid,omitempty"`
	Reconcile *daemonReconcileResponse `json:"reconcile,omitempty"`
}

type repairQueueStep struct {
	Action  string             `json:"action"`
	Reason  string             `json:"reason,omitempty"`
	Results []queueRetryResult `json:"results,omitempty"`
}

type repairIntakeStep struct {
	Action              string   `json:"action"`
	Reason              string   `json:"reason,omitempty"`
	Unresolved          int      `json:"unresolved"`
	Replayable          int      `json:"replayable"`
	DuplicateRequestIDs int      `json:"duplicate_request_ids,omitempty"`
	LatestErrorID       string   `json:"latest_error_id,omitempty"`
	Actions             []string `json:"actions,omitempty"`
}

type repairPipelineRetryStep struct {
	Action  string                `json:"action"`
	Reason  string                `json:"reason,omitempty"`
	Results []pipelineRetryResult `json:"results,omitempty"`
}

type repairPipelineTimeoutStep struct {
	Action  string                  `json:"action"`
	Reason  string                  `json:"reason,omitempty"`
	Results []pipelineTimeoutResult `json:"results,omitempty"`
}

type repairTickStep struct {
	Action    string               `json:"action"`
	Reason    string               `json:"reason,omitempty"`
	Result    *tickResult          `json:"result,omitempty"`
	UntilIdle *tickUntilIdleResult `json:"until_idle,omitempty"`
}

func runRepair(cmd *cobra.Command, target, teamDir string, opts repairOptions) (*repairResult, error) {
	result := &repairResult{DryRun: opts.DryRun}
	if opts.MaxCycles <= 0 {
		opts.MaxCycles = 1
	}
	if opts.CollectHealth {
		health, err := collectRepairHealth(teamDir, opts)
		if err != nil {
			return nil, err
		}
		result.HealthBefore = health
	}

	beforeDaemon := collectDaemonStatus(teamDir)
	result.Daemon = repairDaemonStepResult(beforeDaemon, opts)
	if !opts.SkipDaemon && !opts.DryRun {
		if err := ensureDaemonReadyWithTimeout(cmd, target, true, opts.ReadyTimeout); err != nil {
			return nil, err
		}
		dc, err := newDaemonClient(teamDir)
		if err != nil {
			return nil, err
		}
		if _, err := dc.TopologyReload(); err != nil {
			return nil, fmt.Errorf("reload topology: %w", err)
		}
		rec, err := dc.Reconcile()
		if err != nil {
			return nil, err
		}
		afterDaemon := collectDaemonStatus(teamDir)
		result.Daemon.Action = "reconciled"
		if !beforeDaemon.Running {
			result.Daemon.Action = "started"
		}
		result.Daemon.Running = afterDaemon.Running
		result.Daemon.Ready = afterDaemon.Ready
		result.Daemon.PID = afterDaemon.PID
		result.Daemon.Reconcile = rec
	}

	if opts.SkipQueue {
		result.Queue = repairQueueStep{Action: "skipped", Reason: "--skip-queue set"}
	} else {
		filters, err := parseQueueListFilters(daemon.QueueStateDead, nil, nil, nil, false, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		retries, err := queueRetryAllResults(teamDir, filters, opts.Limit, opts.DryRun)
		if err != nil {
			return nil, err
		}
		result.Queue = repairQueueStep{Action: "retried", Results: retries}
		if opts.DryRun {
			result.Queue.Action = "would_retry"
		}
		if len(retries) == 0 {
			result.Queue.Action = "none"
		}
	}

	result.Intake = collectRepairIntakeStep(teamDir, opts)

	jobTimeout, err := runRepairJobTimeoutStep(teamDir, opts)
	if err != nil {
		return nil, err
	}
	result.JobTimeout = jobTimeout

	pipelineTimeout, err := runRepairPipelineTimeoutStep(teamDir, opts)
	if err != nil {
		return nil, err
	}
	result.PipelineTimeout = pipelineTimeout

	pipelineRetry, err := runRepairPipelineRetryStep(cmd, teamDir, opts)
	if err != nil {
		return nil, err
	}
	result.PipelineRetry = pipelineRetry

	result.Tick = runRepairTickStep(cmd, teamDir, opts)
	if result.Tick.Action == "error" {
		return nil, fmt.Errorf("tick: %s", result.Tick.Reason)
	}

	if opts.CollectHealth && !opts.DryRun {
		health, err := collectRepairHealth(teamDir, opts)
		if err != nil {
			return nil, err
		}
		result.HealthAfter = health
	}
	return result, nil
}

func collectRepairHealth(teamDir string, opts repairOptions) (*healthResult, error) {
	return collectHealthWithOptions(teamDir, time.Now(), healthOptions{includeJobs: opts.IncludeJobs})
}

func collectRepairIntakeStep(teamDir string, opts repairOptions) repairIntakeStep {
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		return repairIntakeStep{Action: "error", Reason: err.Error()}
	}
	out := repairIntakeStep{Action: "none"}
	out.DuplicateRequestIDs = len(duplicateIntakeRequestIDs(deliveries, "", ""))
	var latest time.Time
	for _, delivery := range deliveries {
		if !intakeDeliveryNeedsReplay(delivery) {
			continue
		}
		out.Unresolved++
		if strings.TrimSpace(delivery.EventType) != "" && len(delivery.Payload) > 0 {
			out.Replayable++
		}
		if out.LatestErrorID == "" || delivery.Time.After(latest) {
			latest = delivery.Time
			out.LatestErrorID = delivery.ID
		}
	}
	if out.Unresolved == 0 && out.DuplicateRequestIDs == 0 {
		return out
	}
	out.Action = "manual"
	switch {
	case out.Unresolved > 0 && out.DuplicateRequestIDs > 0:
		out.Reason = "intake replay and duplicate review are not automatic"
	case out.Unresolved > 0:
		out.Reason = "intake replay is not automatic"
	default:
		out.Reason = "duplicate intake request-id review is not automatic"
	}
	if opts.DryRun {
		out.Action = "would_review"
	}
	if out.Unresolved > 0 {
		out.Actions = append(out.Actions, "agent-team intake deliveries --unresolved")
		if out.Replayable > 0 {
			out.Actions = append(out.Actions,
				intakeReplayAllDryRunAction(),
				intakeReplayAllAction(),
			)
		}
	}
	if out.DuplicateRequestIDs > 0 {
		out.Actions = append(out.Actions, "agent-team intake duplicates")
	}
	return out
}

func runRepairPipelineRetryStep(cmd *cobra.Command, teamDir string, opts repairOptions) (repairPipelineRetryStep, error) {
	if !opts.RetryPipelines {
		return repairPipelineRetryStep{Action: "skipped", Reason: "--retry-pipelines not set"}, nil
	}
	message := strings.TrimSpace(opts.RetryMessage)
	if message == "" {
		message = "repair retry failed pipeline step"
	}
	results, err := retryPipelineJobs(cmd, teamDir, "", opts.Workspace, runtimeSelection{}, opts.RetryStep, message, opts.Limit, true, opts.DryRun, opts.PreviewRoutes)
	if err != nil {
		return repairPipelineRetryStep{Action: "error", Reason: err.Error()}, err
	}
	action := "retried"
	if opts.DryRun {
		action = "would_dispatch"
	}
	if len(results) == 0 {
		action = "none"
	}
	return repairPipelineRetryStep{Action: action, Results: results}, nil
}

func runRepairPipelineTimeoutStep(teamDir string, opts repairOptions) (repairPipelineTimeoutStep, error) {
	if !opts.TimeoutPipelines {
		return repairPipelineTimeoutStep{Action: "skipped", Reason: "--timeout-pipelines not set"}, nil
	}
	message := strings.TrimSpace(opts.TimeoutMessage)
	if message == "" {
		message = "repair timed out stale pipeline step"
	}
	results, err := timeoutPipelineJobs(teamDir, "", opts.TimeoutStep, message, opts.Limit, opts.DryRun)
	if err != nil {
		return repairPipelineTimeoutStep{Action: "error", Reason: err.Error()}, err
	}
	action := "timed_out"
	if opts.DryRun {
		action = "would_fail"
	}
	if len(results) == 0 {
		action = "none"
	}
	return repairPipelineTimeoutStep{Action: action, Results: results}, nil
}

func runRepairJobTimeoutStep(teamDir string, opts repairOptions) (repairPipelineTimeoutStep, error) {
	if !opts.TimeoutJobs {
		return repairPipelineTimeoutStep{Action: "skipped", Reason: "--timeout-jobs not set"}, nil
	}
	message := strings.TrimSpace(opts.TimeoutMessage)
	if message == "" {
		message = "repair timed out stale job work"
	}
	results, err := timeoutAllStaleJobWork(teamDir, opts.TimeoutStep, message, opts.Limit, opts.DryRun, jobTimeoutFilters{})
	if err != nil {
		return repairPipelineTimeoutStep{Action: "error", Reason: err.Error()}, err
	}
	action := "timed_out"
	if opts.DryRun {
		action = "would_fail"
	}
	if len(results) == 0 {
		action = "none"
	}
	return repairPipelineTimeoutStep{Action: action, Results: results}, nil
}

func repairDaemonStepResult(status daemonStatusJSON, opts repairOptions) repairStepResult {
	out := repairStepResult{
		Running: status.Running,
		Ready:   status.Ready,
		PID:     status.PID,
	}
	switch {
	case opts.SkipDaemon:
		out.Action = "skipped"
		out.Reason = "--skip-daemon set"
	case opts.DryRun && !status.Running:
		out.Action = "would_start"
	case opts.DryRun && !status.Ready:
		out.Action = "would_wait_ready"
	case opts.DryRun:
		out.Action = "would_reconcile"
	default:
		out.Action = "reconcile"
	}
	return out
}

func runRepairTickStep(cmd *cobra.Command, teamDir string, opts repairOptions) repairTickStep {
	if opts.SkipTick {
		return repairTickStep{Action: "skipped", Reason: "--skip-tick set"}
	}
	status := collectDaemonStatus(teamDir)
	if !status.Running || !status.Ready {
		return repairTickStep{Action: "skipped", Reason: "daemon is not running"}
	}
	if opts.UntilIdle {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		until, err := runTickUntilIdle(ctx, cmd, teamDir, opts.Workspace, opts.Limit, tickOptions{AllReadySteps: opts.AllReadySteps}, opts.MaxCycles, opts.Interval)
		if err != nil {
			return repairTickStep{Action: "error", Reason: err.Error()}
		}
		action := "until_idle"
		if until.HitLimit {
			action = "hit_limit"
		}
		return repairTickStep{Action: action, UntilIdle: until}
	}
	tick, err := runTick(cmd, teamDir, opts.Workspace, opts.Limit, tickOptions{DryRun: opts.DryRun, PreviewRoutes: opts.PreviewRoutes, AllReadySteps: opts.AllReadySteps})
	if err != nil {
		return repairTickStep{Action: "error", Reason: err.Error()}
	}
	action := "tick"
	if opts.DryRun {
		action = "would_tick"
	}
	return repairTickStep{Action: action, Result: tick}
}

func parseRepairFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("repair-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderRepairResult(w io.Writer, result *repairResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &repairResult{}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderRepairFormat(w, result, tmpl)
	}
	if result.DryRun {
		fmt.Fprintln(w, "Repair dry-run: true")
	} else {
		fmt.Fprintln(w, "Repair dry-run: false")
	}
	if result.HealthBefore != nil {
		fmt.Fprintf(w, "Health before: %s\n", repairHealthState(result.HealthBefore))
		renderRepairHealthActions(w, result.HealthBefore)
	}
	renderRepairDaemonStep(w, result.Daemon)
	fmt.Fprintln(w)
	renderRepairQueueStep(w, result.Queue)
	fmt.Fprintln(w)
	renderRepairIntakeStep(w, result.Intake)
	fmt.Fprintln(w)
	renderRepairJobTimeoutStep(w, result.JobTimeout)
	fmt.Fprintln(w)
	renderRepairPipelineTimeoutStep(w, result.PipelineTimeout)
	fmt.Fprintln(w)
	if err := renderRepairPipelineRetryStep(w, result.PipelineRetry); err != nil {
		return err
	}
	fmt.Fprintln(w)
	if err := renderRepairTickStep(w, result.Tick); err != nil {
		return err
	}
	if result.HealthAfter != nil {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Health after: %s\n", repairHealthState(result.HealthAfter))
	}
	return nil
}

func renderRepairFormat(w io.Writer, result *repairResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderRepairHealthActions(w io.Writer, health *healthResult) {
	if health == nil {
		return
	}
	rows := make([]healthIssue, 0, len(health.Issues))
	for _, issue := range health.Issues {
		if len(issue.Actions) > 0 {
			rows = append(rows, issue)
		}
	}
	if len(rows) == 0 {
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ISSUE\tJOB\tINSTANCE\tACTION")
	for _, issue := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			issue.Code, emptyDash(issue.Job), emptyDash(issue.Instance), strings.Join(issue.Actions, "; "))
	}
	_ = tw.Flush()
}

func renderRepairIntakeStep(w io.Writer, step repairIntakeStep) {
	fmt.Fprintf(w, "Intake: %s", emptyDash(step.Action))
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	fmt.Fprintf(w, " unresolved=%d replayable=%d duplicate_request_ids=%d", step.Unresolved, step.Replayable, step.DuplicateRequestIDs)
	if step.LatestErrorID != "" {
		fmt.Fprintf(w, " latest=%s", step.LatestErrorID)
	}
	fmt.Fprintln(w)
	for _, action := range step.Actions {
		fmt.Fprintf(w, "  action %s\n", action)
	}
}

func renderRepairPipelineRetryStep(w io.Writer, step repairPipelineRetryStep) error {
	fmt.Fprintf(w, "Pipeline retry: %s", emptyDash(step.Action))
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	fmt.Fprintln(w)
	if len(step.Results) == 0 {
		fmt.Fprintln(w, "(no failed pipeline jobs)")
		return nil
	}
	renderPipelineRetryTable(w, step.Results)
	return renderPipelineRetryRoutePreviews(w, step.Results)
}

func renderRepairPipelineTimeoutStep(w io.Writer, step repairPipelineTimeoutStep) {
	fmt.Fprintf(w, "Pipeline timeout: %s", emptyDash(step.Action))
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	fmt.Fprintln(w)
	if len(step.Results) == 0 {
		fmt.Fprintln(w, "(no stale running pipeline steps)")
		return
	}
	renderPipelineTimeoutTable(w, step.Results)
}

func renderRepairJobTimeoutStep(w io.Writer, step repairPipelineTimeoutStep) {
	fmt.Fprintf(w, "Job timeout: %s", emptyDash(step.Action))
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	fmt.Fprintln(w)
	if len(step.Results) == 0 {
		fmt.Fprintln(w, "(no stale running jobs)")
		return
	}
	renderPipelineTimeoutTable(w, step.Results)
}

func repairHealthState(h *healthResult) string {
	if h == nil {
		return "unknown"
	}
	state := "healthy"
	if !h.Healthy {
		state = "unhealthy"
	}
	parts := []string{
		fmt.Sprintf("issues=%d", len(h.Issues)),
		fmt.Sprintf("queue_dead=%d", h.Queue.Dead),
		fmt.Sprintf("queue_pending=%d", h.Queue.Pending),
	}
	if h.Jobs != nil {
		parts = append(parts, fmt.Sprintf("job_attention=%d", len(h.Jobs.Attention)))
	}
	if h.JobStatus != nil {
		parts = append(parts,
			fmt.Sprintf("job_status_changes=%d", countChangedJobStatusPreviews(h.JobStatus)),
			fmt.Sprintf("job_status_blocked=%d", countJobStatusPreviewsByAfter(h.JobStatus, "blocked")),
		)
	}
	return fmt.Sprintf("%s (%s)", state, strings.Join(parts, ", "))
}

func renderRepairDaemonStep(w io.Writer, step repairStepResult) {
	fmt.Fprintf(w, "Daemon: %s", emptyDash(step.Action))
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	if step.PID > 0 {
		fmt.Fprintf(w, " pid=%d", step.PID)
	}
	if step.Reconcile != nil {
		fmt.Fprintf(w, " changed=%d", step.Reconcile.Changed)
	}
	fmt.Fprintln(w)
}

func renderRepairQueueStep(w io.Writer, step repairQueueStep) {
	fmt.Fprintf(w, "Queue: %s", emptyDash(step.Action))
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	fmt.Fprintln(w)
	if len(step.Results) == 0 {
		fmt.Fprintln(w, "(no dead-letter queue items)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tINSTANCE\tINSTANCE_ID\tACTION\tREASON")
	for _, result := range step.Results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			result.ID, result.State, result.Instance, result.InstanceID, result.Action, emptyDash(result.Reason))
	}
	_ = tw.Flush()
}

func renderRepairTickStep(w io.Writer, step repairTickStep) error {
	fmt.Fprintf(w, "Tick: %s", emptyDash(step.Action))
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	fmt.Fprintln(w)
	if step.Result != nil {
		return renderTickResult(w, step.Result, false, nil)
	}
	if step.UntilIdle != nil {
		return renderTickUntilIdleResult(w, step.UntilIdle, false, nil)
	}
	return nil
}
