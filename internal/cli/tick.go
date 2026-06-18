package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/template"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

func newTickCmd() *cobra.Command {
	var (
		target        string
		workspace     string
		limit         int
		skipReconcile bool
		skipDrain     bool
		skipAdvance   bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "tick",
		Short: "Run one orchestration maintenance cycle.",
		Long: "Run one orchestration maintenance cycle against the running daemon: " +
			"reconcile process metadata, drain ready queue items, then advance ready pipeline jobs.",
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
			tmpl, err := parseTickFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team tick: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := runTick(cmd, teamDir, workspace, limit, tickOptions{
				SkipReconcile: skipReconcile,
				SkipDrain:     skipDrain,
				SkipAdvance:   skipAdvance,
			})
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
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip queue draining.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the tick result with a Go template, e.g. '{{.Queue.Dispatched}} {{len .Advance}}'.")
	return cmd
}

type tickOptions struct {
	SkipReconcile bool
	SkipDrain     bool
	SkipAdvance   bool
}

type tickResult struct {
	Reconcile *daemonReconcileResponse `json:"reconcile,omitempty"`
	Queue     *daemon.QueueDrainResult `json:"queue,omitempty"`
	Advance   []pipelineAdvanceResult  `json:"advance,omitempty"`
}

func runTick(cmd *cobra.Command, teamDir, workspace string, limit int, opts tickOptions) (*tickResult, error) {
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		return nil, err
	}
	result := &tickResult{}
	if !opts.SkipReconcile {
		rec, err := dc.Reconcile()
		if err != nil {
			return nil, err
		}
		result.Reconcile = rec
	}
	if !opts.SkipDrain {
		drain, err := dc.QueueDrain()
		if err != nil {
			return nil, err
		}
		result.Queue = drain
	}
	if !opts.SkipAdvance {
		advanced, err := advanceReadyPipelineJobs(cmd, teamDir, "", workspace, limit, false)
		if err != nil {
			return nil, err
		}
		result.Advance = advanced
	}
	return result, nil
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
	if result.Reconcile != nil {
		fmt.Fprintln(w, "Reconcile:")
		if err := renderDaemonReconcile(w, result.Reconcile); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Reconcile: skipped")
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
