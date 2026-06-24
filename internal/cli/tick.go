package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
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
			"reconcile process metadata and job status files, fire due schedules, drain ready queue items, then advance ready pipeline jobs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if watch && untilIdle {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: choose one of --watch or --until-idle.")
				return exitErr(2)
			}
			if untilIdle && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team tick: --until-idle cannot be combined with --dry-run.")
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
			return renderTickResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for advanced step dispatches. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&skipReconcile, "skip-reconcile", false, "Skip daemon metadata and job status reconciliation.")
	cmd.Flags().BoolVar(&skipSchedules, "skip-schedules", false, "Skip firing due schedules.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip queue draining.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent pipeline step in this tick.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job status reconciliation, schedule firing, queue drain, and pipeline advancement without mutating state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include route and dispatch payload previews for ready pipeline steps.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Run tick repeatedly until interrupted.")
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Run tick cycles until no immediate schedule, queue, or pipeline work remains.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the tick result or until-idle aggregate with a Go template, e.g. '{{.Queue.Dispatched}} {{len .Advance}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch, or delay between --until-idle cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "With --until-idle, stop after this many cycles if work keeps appearing.")
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
	Reconcile *daemonReconcileResponse   `json:"reconcile,omitempty"`
	JobEvents []jobEventReconcileResult  `json:"job_events,omitempty"`
	JobStatus []jobStatusReconcileResult `json:"job_status,omitempty"`
	Schedule  *daemon.ScheduleFireResult `json:"schedule,omitempty"`
	Queue     *daemon.QueueDrainResult   `json:"queue,omitempty"`
	Advance   []pipelineAdvanceResult    `json:"advance,omitempty"`
	DryRun    bool                       `json:"dry_run,omitempty"`
}

type tickUntilIdleResult struct {
	CyclesRun int           `json:"cycles_run"`
	Idle      bool          `json:"idle"`
	HitLimit  bool          `json:"hit_limit,omitempty"`
	Cycles    []*tickResult `json:"cycles"`
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
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
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
	for _, advanced := range result.Advance {
		if advanced.Action == "advanced" || advanced.Action == "would_advance" {
			return false
		}
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
		return renderPipelineAdvanceResults(w, result.Advance, false, nil)
	}
	fmt.Fprintln(w, "Pipeline advance: skipped")
	return nil
}
