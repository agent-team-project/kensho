package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

func newRepairCmd() *cobra.Command {
	var (
		target       string
		workspace    string
		limit        int
		dryRun       bool
		jsonOut      bool
		skipDaemon   bool
		skipQueue    bool
		skipTick     bool
		untilIdle    bool
		readyTimeout time.Duration
		interval     time.Duration
		maxCycles    int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Recover common unhealthy orchestration state.",
		Long: "Recover common unhealthy orchestration state: ensure the daemon is ready, retry dead-letter queue items, " +
			"and run a maintenance tick to drain ready work and advance pipelines. Use --dry-run to preview.",
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
			if untilIdle && skipTick {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team repair: --until-idle cannot be combined with --skip-tick.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := runRepair(cmd, target, teamDir, repairOptions{
				Workspace:     workspace,
				Limit:         limit,
				DryRun:        dryRun,
				SkipDaemon:    skipDaemon,
				SkipQueue:     skipQueue,
				SkipTick:      skipTick,
				UntilIdle:     untilIdle,
				ReadyTimeout:  readyTimeout,
				Interval:      interval,
				MaxCycles:     maxCycles,
				CollectHealth: true,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team repair: %v\n", err)
				return exitErr(1)
			}
			return renderRepairResult(cmd.OutOrStdout(), result, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for pipeline steps during the maintenance tick: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Retry at most this many dead-letter queue items and advance at most this many ready pipeline jobs; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview repair actions without mutating state or starting the daemon.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&skipDaemon, "skip-daemon", false, "Do not start or reconcile the daemon.")
	cmd.Flags().BoolVar(&skipQueue, "skip-queue", false, "Do not retry dead-letter queue items.")
	cmd.Flags().BoolVar(&skipTick, "skip-tick", false, "Do not run a maintenance tick after queue retry.")
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Run maintenance ticks until no immediate queue, schedule, or pipeline work remains.")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Delay between --until-idle maintenance cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "With --until-idle, stop after this many cycles if work keeps appearing.")
	return cmd
}

type repairOptions struct {
	Workspace     string
	Limit         int
	DryRun        bool
	SkipDaemon    bool
	SkipQueue     bool
	SkipTick      bool
	UntilIdle     bool
	ReadyTimeout  time.Duration
	Interval      time.Duration
	MaxCycles     int
	CollectHealth bool
}

type repairResult struct {
	DryRun       bool             `json:"dry_run,omitempty"`
	HealthBefore *healthResult    `json:"health_before,omitempty"`
	Daemon       repairStepResult `json:"daemon"`
	Queue        repairQueueStep  `json:"queue"`
	Tick         repairTickStep   `json:"tick"`
	HealthAfter  *healthResult    `json:"health_after,omitempty"`
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
		health, err := collectHealth(teamDir, time.Now())
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

	result.Tick = runRepairTickStep(cmd, teamDir, opts)
	if result.Tick.Action == "error" {
		return nil, fmt.Errorf("tick: %s", result.Tick.Reason)
	}

	if opts.CollectHealth && !opts.DryRun {
		health, err := collectHealth(teamDir, time.Now())
		if err != nil {
			return nil, err
		}
		result.HealthAfter = health
	}
	return result, nil
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
		until, err := runTickUntilIdle(ctx, cmd, teamDir, opts.Workspace, opts.Limit, tickOptions{}, opts.MaxCycles, opts.Interval)
		if err != nil {
			return repairTickStep{Action: "error", Reason: err.Error()}
		}
		action := "until_idle"
		if until.HitLimit {
			action = "hit_limit"
		}
		return repairTickStep{Action: action, UntilIdle: until}
	}
	tick, err := runTick(cmd, teamDir, opts.Workspace, opts.Limit, tickOptions{DryRun: opts.DryRun})
	if err != nil {
		return repairTickStep{Action: "error", Reason: err.Error()}
	}
	action := "tick"
	if opts.DryRun {
		action = "would_tick"
	}
	return repairTickStep{Action: action, Result: tick}
}

func renderRepairResult(w io.Writer, result *repairResult, jsonOut bool) error {
	if result == nil {
		result = &repairResult{}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if result.DryRun {
		fmt.Fprintln(w, "Repair dry-run: true")
	} else {
		fmt.Fprintln(w, "Repair dry-run: false")
	}
	if result.HealthBefore != nil {
		fmt.Fprintf(w, "Health before: %s\n", repairHealthState(result.HealthBefore))
	}
	renderRepairDaemonStep(w, result.Daemon)
	fmt.Fprintln(w)
	renderRepairQueueStep(w, result.Queue)
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

func repairHealthState(h *healthResult) string {
	if h == nil {
		return "unknown"
	}
	state := "healthy"
	if !h.Healthy {
		state = "unhealthy"
	}
	return fmt.Sprintf("%s (issues=%d, queue_dead=%d, queue_pending=%d)", state, len(h.Issues), h.Queue.Dead, h.Queue.Pending)
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
