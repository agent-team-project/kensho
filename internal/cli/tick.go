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
		limit         int
		skipReconcile bool
		skipSchedules bool
		skipDrain     bool
		skipAdvance   bool
		dryRun        bool
		watch         bool
		jsonOut       bool
		format        string
		interval      time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "tick",
		Short: "Run one orchestration maintenance cycle.",
		Long: "Run one orchestration maintenance cycle against the running daemon: " +
			"reconcile process metadata, fire due schedules, drain ready queue items, then advance ready pipeline jobs.",
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
				DryRun:        dryRun,
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
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs; 0 means no limit.")
	cmd.Flags().BoolVar(&skipReconcile, "skip-reconcile", false, "Skip daemon metadata reconciliation.")
	cmd.Flags().BoolVar(&skipSchedules, "skip-schedules", false, "Skip firing due schedules.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip queue draining.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview schedule firing, queue drain, and pipeline advancement without mutating state.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Run tick repeatedly until interrupted.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the tick result with a Go template, e.g. '{{.Queue.Dispatched}} {{len .Advance}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	return cmd
}

type tickOptions struct {
	SkipReconcile bool
	SkipSchedules bool
	SkipDrain     bool
	SkipAdvance   bool
	DryRun        bool
}

type tickResult struct {
	Reconcile *daemonReconcileResponse   `json:"reconcile,omitempty"`
	Schedule  *daemon.ScheduleFireResult `json:"schedule,omitempty"`
	Queue     *daemon.QueueDrainResult   `json:"queue,omitempty"`
	Advance   []pipelineAdvanceResult    `json:"advance,omitempty"`
	DryRun    bool                       `json:"dry_run,omitempty"`
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
		advanced, err := advanceReadyPipelineJobs(cmd, teamDir, "", workspace, limit, opts.DryRun)
		if err != nil {
			return nil, err
		}
		result.Advance = advanced
	}
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
