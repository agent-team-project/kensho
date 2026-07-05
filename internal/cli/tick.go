package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newTickCmd() *cobra.Command {
	var (
		target        string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		limit         int
		skipReconcile bool
		skipSchedules bool
		skipDrain     bool
		skipAdvance   bool
		allReadySteps bool
		dryRun        bool
		previewRoutes bool
		watch         bool
		untilIdle     bool
		commands      bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
		interval      time.Duration
		maxCycles     int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "tick",
		Short: "Run one orchestration maintenance cycle.",
		Long: "Run one orchestration maintenance cycle against the running daemon: " +
			"reconcile process metadata and job status files, fire due schedules, drain agent outbox and ready queue items, then advance ready pipeline jobs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --interval must be >= 0.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if watch && untilIdle {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: choose one of --watch or --until-idle.")
				return exitErr(2)
			}
			if untilIdle && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --until-idle cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if wait && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if wait && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --wait cannot be combined with --watch.")
				return exitErr(2)
			}
			if wait && untilIdle {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --wait cannot be combined with --until-idle.")
				return exitErr(2)
			}
			if wait && skipAdvance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --wait requires pipeline advancement; remove --skip-advance.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: wait-related flags require --wait.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			if maxCycles <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --max-cycles must be > 0.")
				return exitErr(2)
			}
			if cmd.Flags().Changed("max-cycles") && !untilIdle {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --max-cycles requires --until-idle.")
				return exitErr(2)
			}
			tmpl, err := parseTickFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team tick: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team tick: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			opts := tickOptions{
				SkipReconcile: skipReconcile,
				SkipSchedules: skipSchedules,
				SkipDrain:     skipDrain,
				SkipAdvance:   skipAdvance,
				AllReadySteps: allReadySteps,
				Runtime:       runtimeSelection{Kind: runtimeKind, Binary: runtimeBin},
				DryRun:        dryRun,
				PreviewRoutes: previewRoutes,
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if err := runTickLoop(ctx, cmd, teamDir, workspace, limit, opts, jsonOut, tmpl, interval); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team tick: %v\n", err)
					if errors.Is(err, errDaemonNotRunning) {
						return exitErr(2)
					}
					return exitErr(1)
				}
				return nil
			}
			if untilIdle {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				result, err := runTickUntilIdle(ctx, cmd, teamDir, workspace, limit, opts, maxCycles, interval)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team tick: %v\n", err)
					if errors.Is(err, errDaemonNotRunning) {
						return exitErr(2)
					}
					return exitErr(1)
				}
				return renderTickUntilIdleResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
			}
			result, err := runTick(cmd, teamDir, workspace, limit, opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team tick: %v\n", err)
				if errors.Is(err, errDaemonNotRunning) {
					return exitErr(2)
				}
				return exitErr(1)
			}
			if wait {
				result.Advance, err = waitForPipelineAdvanceResults(cmd, teamDir, result.Advance, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team tick")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
			}
			if commands {
				scope := operatorCommandScopeFromCommand(cmd, target, "target")
				return renderTickCommands(cmd.OutOrStdout(), result, tickApplyCommandOptions{
					BaseArgs:      []string{"agent-team", "tick"},
					Repo:          scope.Repo,
					RepoSet:       scope.Set,
					Workspace:     workspace,
					WorkspaceSet:  cmd.Flags().Changed("workspace"),
					RuntimeKind:   runtimeKind,
					RuntimeBin:    runtimeBin,
					Limit:         limit,
					SkipReconcile: skipReconcile,
					SkipSchedules: skipSchedules,
					SkipDrain:     skipDrain,
					SkipAdvance:   skipAdvance,
					AllReadySteps: allReadySteps,
				})
			}
			if err := renderTickResult(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && pipelineAdvanceResultsHaveFailed(result.Advance) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for advanced step dispatches. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&skipReconcile, "skip-reconcile", false, "Skip daemon metadata and job status reconciliation.")
	cmd.Flags().BoolVar(&skipSchedules, "skip-schedules", false, "Skip firing due schedules.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip outbox and queue draining.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent pipeline step in this tick.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job status reconciliation, schedule firing, outbox/queue drains, and pipeline advancement without mutating state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include route and dispatch payload previews for ready pipeline steps.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Run tick repeatedly until interrupted.")
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Run tick cycles until no immediate schedule, outbox, queue, or pipeline work remains.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching tick apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After one tick, wait for advanced pipeline jobs to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step for every advanced job.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any advanced job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the tick result or until-idle aggregate with a Go template, e.g. '{{.Queue.Dispatched}} {{len .Advance}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch, or delay between --until-idle cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "With --until-idle, stop after this many cycles if work keeps appearing.")
	return cmd
}

func newDrainCmd() *cobra.Command {
	var (
		target        string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		limit         int
		skipReconcile bool
		skipSchedules bool
		skipDrain     bool
		skipAdvance   bool
		allReadySteps bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
		interval      time.Duration
		maxCycles     int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drain",
		Short: "Run maintenance cycles until idle.",
		Long: "Run orchestration maintenance cycles until no immediate job-status, schedule, outbox, queue, or pipeline work remains. " +
			"This is the script-friendly shortcut for `agent-team tick --until-idle`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team drain: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team drain: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team drain: --interval must be >= 0.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team drain: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team drain: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if maxCycles <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team drain: --max-cycles must be > 0.")
				return exitErr(2)
			}
			if wait && skipAdvance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team drain: --wait requires pipeline advancement; remove --skip-advance.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team drain: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parseTickFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team drain: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team drain: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			result, err := runTickUntilIdle(ctx, cmd, teamDir, workspace, limit, tickOptions{
				SkipReconcile: skipReconcile,
				SkipSchedules: skipSchedules,
				SkipDrain:     skipDrain,
				SkipAdvance:   skipAdvance,
				AllReadySteps: allReadySteps,
				Runtime:       runtimeSelection{Kind: runtimeKind, Binary: runtimeBin},
			}, maxCycles, interval)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team drain: %v\n", err)
				if errors.Is(err, errDaemonNotRunning) {
					return exitErr(2)
				}
				return exitErr(1)
			}
			if wait {
				if err := waitForTickUntilIdleResult(cmd, teamDir, result, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team drain"); err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
			}
			if err := renderTickUntilIdleResult(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && tickUntilIdleResultHasFailed(result) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for advanced step dispatches. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs per cycle, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&skipReconcile, "skip-reconcile", false, "Skip daemon metadata and job status reconciliation.")
	cmd.Flags().BoolVar(&skipSchedules, "skip-schedules", false, "Skip firing due schedules.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip outbox and queue draining.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent pipeline step in each drain cycle.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After drain reaches idle, wait for jobs advanced during drain cycles to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step for every drain-advanced job.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any drain-advanced job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drain result with a Go template, e.g. '{{.CyclesRun}} {{.Idle}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Delay between drain cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "Stop after this many cycles if work keeps appearing.")
	return cmd
}

type tickOptions struct {
	SkipReconcile bool
	SkipSchedules bool
	SkipDrain     bool
	SkipAdvance   bool
	AllReadySteps bool
	Runtime       runtimeSelection
	DryRun        bool
	PreviewRoutes bool
}

type tickResult struct {
	Reconcile  *daemonReconcileResponse   `json:"reconcile,omitempty"`
	JobEvents  []jobEventReconcileResult  `json:"job_events,omitempty"`
	JobStatus  []jobStatusReconcileResult `json:"job_status,omitempty"`
	Schedule   *daemon.ScheduleFireResult `json:"schedule,omitempty"`
	Outbox     *daemon.OutboxDrainResult  `json:"outbox,omitempty"`
	Queue      *daemon.QueueDrainResult   `json:"queue,omitempty"`
	Advance    []pipelineAdvanceResult    `json:"advance,omitempty"`
	Compaction *terminalCompactionResult  `json:"compaction,omitempty"`
	DryRun     bool                       `json:"dry_run,omitempty"`
}

type tickUntilIdleResult struct {
	CyclesRun int           `json:"cycles_run"`
	Idle      bool          `json:"idle"`
	HitLimit  bool          `json:"hit_limit,omitempty"`
	Cycles    []*tickResult `json:"cycles"`
}

type tickApplyCommandOptions struct {
	BaseArgs      []string
	Repo          string
	RepoSet       bool
	Workspace     string
	WorkspaceSet  bool
	RuntimeKind   string
	RuntimeBin    string
	Limit         int
	SkipReconcile bool
	SkipSchedules bool
	SkipDrain     bool
	SkipAdvance   bool
	AllReadySteps bool
}

func renderTickCommands(w fmtWriter, result *tickResult, opts tickApplyCommandOptions) error {
	if result == nil || !result.DryRun || tickResultIsIdle(result) {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(tickApplyCommandArgs(opts)), " "))
	return err
}

func tickApplyCommandArgs(opts tickApplyCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	if opts.RepoSet {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.WorkspaceSet {
		args = append(args, "--workspace", opts.Workspace)
	}
	if strings.TrimSpace(opts.RuntimeKind) != "" {
		args = append(args, "--runtime", opts.RuntimeKind)
	}
	if strings.TrimSpace(opts.RuntimeBin) != "" {
		args = append(args, "--runtime-bin", opts.RuntimeBin)
	}
	if opts.Limit > 0 {
		args = append(args, "--limit", fmt.Sprintf("%d", opts.Limit))
	}
	if opts.SkipReconcile {
		args = append(args, "--skip-reconcile")
	}
	if opts.SkipSchedules {
		args = append(args, "--skip-schedules")
	}
	if opts.SkipDrain {
		args = append(args, "--skip-drain")
	}
	if opts.SkipAdvance {
		args = append(args, "--skip-advance")
	}
	if opts.AllReadySteps {
		args = append(args, "--all-ready-steps")
	}
	return args
}

func runTick(cmd *cobra.Command, teamDir, workspace string, limit int, opts tickOptions) (*tickResult, error) {
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		return nil, err
	}
	result := &tickResult{DryRun: opts.DryRun}
	if !opts.SkipReconcile && !opts.DryRun {
		rec, err := dc.Reconcile()
		if err != nil {
			return nil, err
		}
		result.Reconcile = rec
	}
	if !opts.SkipReconcile {
		status, err := reconcileJobsFromStatus(teamDir, opts.DryRun, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		result.JobStatus = status
		events, err := reconcileJobsFromEvents(teamDir, opts.DryRun, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		result.JobEvents = events
	}
	if !opts.SkipSchedules {
		schedule, err := dc.ScheduleFire(opts.DryRun)
		if err != nil {
			return nil, err
		}
		result.Schedule = schedule
	}
	if !opts.SkipDrain {
		outbox, err := dc.OutboxDrain(opts.DryRun)
		if err != nil {
			return nil, err
		}
		result.Outbox = outbox
		drain, err := dc.QueueDrain(opts.DryRun)
		if err != nil {
			return nil, err
		}
		result.Queue = drain
	}
	if !opts.SkipAdvance {
		advanced, err := advanceReadyPipelineJobs(cmd, teamDir, "", workspace, opts.Runtime, limit, opts.DryRun, opts.PreviewRoutes, opts.AllReadySteps)
		if err != nil {
			return nil, err
		}
		result.Advance = advanced
	}
	policy, err := loadHealthPolicy(teamDir)
	if err != nil {
		return nil, err
	}
	compaction, err := runTerminalCompaction(teamDir, policy.TerminalRetention, time.Now().UTC(), opts.DryRun)
	if err != nil {
		return nil, err
	}
	result.Compaction = compaction
	return result, nil
}

func runTickUntilIdle(ctx context.Context, cmd *cobra.Command, teamDir, workspace string, limit int, opts tickOptions, maxCycles int, interval time.Duration) (*tickUntilIdleResult, error) {
	if maxCycles <= 0 {
		maxCycles = 1
	}
	result := &tickUntilIdleResult{Cycles: []*tickResult{}}
	for cycle := 0; cycle < maxCycles; cycle++ {
		if cycle > 0 && interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				result.CyclesRun = len(result.Cycles)
				return result, nil
			case <-timer.C:
			}
		}
		tick, err := runTick(cmd, teamDir, workspace, limit, opts)
		if err != nil {
			result.CyclesRun = len(result.Cycles)
			return result, err
		}
		result.Cycles = append(result.Cycles, tick)
		if tickResultIsIdle(tick) {
			result.Idle = true
			break
		}
	}
	result.CyclesRun = len(result.Cycles)
	result.HitLimit = !result.Idle && result.CyclesRun >= maxCycles
	return result, nil
}

func waitForTickUntilIdleResult(cmd *cobra.Command, teamDir string, result *tickUntilIdleResult, statuses map[job.Status]bool, events map[string]bool, nextStates map[string]bool, nextStateSet bool, step string, timeout, interval time.Duration, prefix string) error {
	if result == nil {
		return nil
	}
	for _, cycle := range result.Cycles {
		if err := waitForTickResultAdvanceRows(cmd, teamDir, cycle, statuses, events, nextStates, nextStateSet, step, timeout, interval, prefix); err != nil {
			return err
		}
	}
	return nil
}

func tickUntilIdleResultHasFailed(result *tickUntilIdleResult) bool {
	if result == nil {
		return false
	}
	for _, cycle := range result.Cycles {
		if tickResultAdvanceRowsHaveFailed(cycle) {
			return true
		}
	}
	return false
}

func runTickLoop(ctx context.Context, cmd *cobra.Command, teamDir, workspace string, limit int, opts tickOptions, jsonOut bool, tmpl *template.Template, interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	first := true
	for {
		result, err := runTick(cmd, teamDir, workspace, limit, opts)
		if err != nil {
			return err
		}
		if !first && !jsonOut {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		if err := renderTickResult(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
			return err
		}
		first = false
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
	}
}

func tickResultIsIdle(result *tickResult) bool {
	if result == nil {
		return true
	}
	if result.Schedule != nil && (result.Schedule.Fired > 0 || result.Schedule.WouldFire > 0) {
		return false
	}
	for _, event := range result.JobEvents {
		if event.Changed {
			return false
		}
	}
	for _, status := range result.JobStatus {
		if status.Changed {
			return false
		}
	}
	if result.Queue != nil && (result.Queue.Attempted > 0 || result.Queue.WouldDispatch > 0 || result.Queue.Dispatched > 0 || result.Queue.Rejected > 0) {
		return false
	}
	if result.Outbox != nil && (result.Outbox.Attempted > 0 || result.Outbox.WouldPublish > 0 || result.Outbox.Published > 0 || result.Outbox.Rejected > 0) {
		return false
	}
	for _, advanced := range result.Advance {
		if advanced.Action == "advanced" || advanced.Action == "would_advance" {
			return false
		}
	}
	if result.Compaction != nil && (len(result.Compaction.Jobs) > 0 || len(result.Compaction.Instances) > 0) {
		return false
	}
	return true
}

func parseTickFormat(format string) (*template.Template, error) {
	if format == "" {
		return nil, nil
	}
	tmpl, err := template.New("tick-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderTickUntilIdleResult(w fmtWriter, result *tickUntilIdleResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &tickUntilIdleResult{Cycles: []*tickResult{}}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	for i, cycle := range result.Cycles {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "Cycle %d:\n", i+1)
		if err := renderTickResult(w, cycle, false, nil); err != nil {
			return err
		}
	}
	if len(result.Cycles) > 0 {
		fmt.Fprintln(w)
	}
	if result.Idle {
		fmt.Fprintf(w, "tick: idle after %d cycle(s)\n", result.CyclesRun)
	} else if result.HitLimit {
		fmt.Fprintf(w, "tick: hit max cycles (%d) before idle\n", result.CyclesRun)
	} else {
		fmt.Fprintf(w, "tick: stopped after %d cycle(s)\n", result.CyclesRun)
	}
	return nil
}

func renderTickResult(w fmtWriter, result *tickResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &tickResult{}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if result.DryRun {
		fmt.Fprintln(w, "Dry run: true")
		fmt.Fprintln(w)
	}
	if result.Reconcile != nil {
		fmt.Fprintln(w, "Reconcile:")
		if err := renderDaemonReconcile(w, result.Reconcile); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Reconcile: skipped")
	}
	fmt.Fprintln(w)
	if result.JobStatus != nil {
		fmt.Fprintln(w, "Job status:")
		if err := renderJobStatusReconcileResults(w, result.JobStatus, false, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Job status: skipped")
	}
	fmt.Fprintln(w)
	if result.JobEvents != nil {
		fmt.Fprintln(w, "Job events:")
		if err := renderJobEventReconcileResults(w, result.JobEvents, false, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Job events: skipped")
	}
	fmt.Fprintln(w)
	if result.Schedule != nil {
		fmt.Fprintln(w, "Schedules:")
		if err := renderScheduleFireResult(w, result.Schedule, false, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Schedules: skipped")
	}
	fmt.Fprintln(w)
	if result.Outbox != nil {
		fmt.Fprintln(w, "Outbox:")
		if err := renderOutboxDrainResult(w, result.Outbox); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Outbox: skipped")
	}
	fmt.Fprintln(w)
	if result.Queue != nil {
		fmt.Fprintln(w, "Queue:")
		if err := renderQueueDrainResult(w, result.Queue, false, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Queue: skipped")
	}
	fmt.Fprintln(w)
	if result.Advance != nil {
		fmt.Fprintln(w, "Pipeline advance:")
		if err := renderPipelineAdvanceResults(w, result.Advance, false, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Pipeline advance: skipped")
	}
	if result.Compaction != nil {
		fmt.Fprintln(w)
		renderTerminalCompactionResult(w, result.Compaction)
	}
	return nil
}

func renderOutboxDrainResult(w fmtWriter, result *daemon.OutboxDrainResult) error {
	if result == nil {
		result = &daemon.OutboxDrainResult{}
	}
	if result.DryRun {
		fmt.Fprintf(w, "outbox drain dry-run: would_publish=%d pending=%d failed=%d processed=%d\n",
			result.WouldPublish, result.Pending, result.Failed, result.Processed)
	} else {
		fmt.Fprintf(w, "outbox drain: attempted=%d published=%d rejected=%d pending=%d failed=%d processed=%d\n",
			result.Attempted, result.Published, result.Rejected, result.Pending, result.Failed, result.Processed)
	}
	if len(result.Items) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tACTION\tERROR")
	for _, item := range result.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			item.ID, item.Type, item.Action, emptyDash(item.Error))
	}
	return tw.Flush()
}
