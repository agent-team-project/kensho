package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newPipelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pipeline",
		Aliases: []string{"pipelines"},
		Short:   "Inspect declared pipeline workflows.",
		Long:    "Inspect pipeline declarations loaded from .agent_team/instances.toml.",
	}
	cmd.AddCommand(newPipelineLsCmd())
	cmd.AddCommand(newPipelineShowCmd())
	cmd.AddCommand(newPipelineGraphCmd())
	cmd.AddCommand(newPipelineDoctorCmd())
	cmd.AddCommand(newPipelineJobsCmd())
	cmd.AddCommand(newPipelineStatusCmd())
	cmd.AddCommand(newPipelineTriageCmd())
	cmd.AddCommand(newPipelineExplainCmd())
	cmd.AddCommand(newPipelineSnapshotCmd())
	cmd.AddCommand(newPipelineNextCmd())
	cmd.AddCommand(newPipelineWaitCmd())
	cmd.AddCommand(newPipelineQueueCmd())
	cmd.AddCommand(newPipelineReadyCmd())
	cmd.AddCommand(newPipelineHoldCmd())
	cmd.AddCommand(newPipelineReleaseCmd())
	cmd.AddCommand(newPipelineAdvanceCmd())
	cmd.AddCommand(newPipelineApproveCmd())
	cmd.AddCommand(newPipelineRejectCmd())
	cmd.AddCommand(newPipelineUnblockCmd())
	cmd.AddCommand(newPipelineSkipCmd())
	cmd.AddCommand(newPipelineCancelCmd())
	cmd.AddCommand(newPipelineAdoptCmd())
	cmd.AddCommand(newPipelineCleanupCmd())
	cmd.AddCommand(newPipelineResumePlanCmd())
	cmd.AddCommand(newPipelineSendCmd())
	cmd.AddCommand(newPipelinePsCmd())
	cmd.AddCommand(newPipelineStatsCmd())
	cmd.AddCommand(newPipelineLogsCmd())
	cmd.AddCommand(newPipelineEventsCmd())
	cmd.AddCommand(newPipelineRetryCmd())
	cmd.AddCommand(newPipelineTimeoutCmd())
	cmd.AddCommand(newPipelineTickCmd())
	cmd.AddCommand(newPipelineRepairCmd())
	cmd.AddCommand(newPipelineDrainCmd())
	cmd.AddCommand(newPipelineRunCmd())
	return cmd
}

func newPipelineLsCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List declared pipelines.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineInfoFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelines, err := loadPipelineInfos(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ls: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineList(cmd.OutOrStdout(), pipelines, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipelines as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline with a Go template, e.g. '{{.Name}} {{len .Steps}}'.")
	return cmd
}

func newPipelineShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <pipeline>",
		Short: "Show one declared pipeline.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineInfoFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadPipelineInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline show: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineDetail(cmd.OutOrStdout(), info, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the pipeline as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline with a Go template, e.g. '{{.Name}} {{len .Steps}}'.")
	return cmd
}

func newPipelineGraphCmd() *cobra.Command {
	var (
		repo          string
		graphFormat   string
		includeRoutes bool
		jsonOut       bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "graph <pipeline>",
		Short: "Render a declared pipeline step graph.",
		Long:  "Render a read-only graph of one declared pipeline in text, Mermaid, DOT, or JSON form.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && cmd.Flags().Changed("format") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline graph: --format cannot be combined with --json.")
				return exitErr(2)
			}
			format, err := parsePipelineGraphFormat(graphFormat)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline graph: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			graph, err := collectPipelineGraph(teamDir, args[0], includeRoutes)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline graph: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineGraph(cmd.OutOrStdout(), graph, format, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&graphFormat, "format", "text", "Graph output format: text, mermaid, or dot.")
	cmd.Flags().BoolVar(&includeRoutes, "routes", false, "Annotate step targets with matching agent.dispatch route instances.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit graph nodes and edges as JSON.")
	return cmd
}

func newPipelineDoctorCmd() *cobra.Command {
	var (
		repo          string
		all           bool
		strictRuntime bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor [<pipeline>|--all]",
		Short: "Validate pipeline workflow wiring.",
		Long: "Validate declared pipeline workflow wiring: dependency graphs must be acyclic, " +
			"step targets should resolve through agent.dispatch topology routes, and schedule-triggered pipelines should have a matching schedule source.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline doctor: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline doctor: pass at most one pipeline name.")
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline doctor: pipeline name is required.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline doctor: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := collectPipelineDoctor(teamDir, pipelineName)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline doctor: %v\n", err)
				return exitErr(1)
			}
			if strictRuntime {
				promotePipelineDoctorRuntimeWarnings(result)
			}
			return renderPipelineDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Validate all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVar(&strictRuntime, "strict-runtime", false, "Fail when a step-declared runtime default cannot be resolved or is not discoverable.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline doctor findings as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.")
	return cmd
}

func newPipelineJobsCmd() *cobra.Command {
	var (
		repo           string
		status         string
		runtimeFilters []string
		all            bool
		watch          bool
		noClear        bool
		interval       time.Duration
		limit          int
		held           bool
		unheld         bool
		expiredHold    bool
		activeHold     bool
		summary        bool
		jsonOut        bool
		format         string
		sortBy         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "jobs [<pipeline>|--all]",
		Short: "List pipeline jobs.",
		Long:  "List durable jobs for one pipeline. With no pipeline, all pipeline-owned jobs are listed.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: pass at most one pipeline name.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && cmd.Flags().Changed("limit") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --interval must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline jobs: %v\n", err)
				return exitErr(2)
			}
			if held && unheld {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --held and --unheld cannot be combined.")
				return exitErr(2)
			}
			if expiredHold && activeHold {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --expired-hold and --active-hold cannot be combined.")
				return exitErr(2)
			}
			sortMode, err := parseJobSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline jobs: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: pipeline name is required.")
				return exitErr(2)
			}
			filters, err := newJobListFilters(status, "", "", pipelineName, "", "", "", runtimeFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline jobs: %v\n", err)
				return exitErr(2)
			}
			if pipelineName == "" {
				filters.PipelineOwned = true
			}
			filters.Held = jobHeldFilter(held, unheld)
			filters.HoldExpired = jobHoldExpiredFilter(expiredHold, activeHold)
			filters.Sort = sortMode
			filters.Limit = limit
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runJobSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runJobListWatch(ctx, cmd.OutOrStdout(), teamDir, filters, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				filtered, err := filteredJobs(teamDir, filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline jobs: %v\n", err)
					return exitErr(1)
				}
				s := summarizeJobsWithRuntime(teamDir, filtered)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(s)
				}
				renderJobSummary(cmd.OutOrStdout(), s)
				return nil
			}
			return runJobList(cmd.OutOrStdout(), teamDir, filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&status, "status", "", "Filter by job status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show jobs whose instance metadata has this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&all, "all", false, "List jobs across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh pipeline jobs until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&held, "held", false, "Only show held jobs.")
	cmd.Flags().BoolVar(&unheld, "unheld", false, "Only show jobs that are not held.")
	cmd.Flags().BoolVar(&expiredHold, "expired-hold", false, "Only show held jobs whose hold_until has passed.")
	cmd.Flags().BoolVar(&activeHold, "active-hold", false, "Only show held jobs whose hold is still active or has no deadline.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate pipeline job counts instead of job rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit jobs as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "id", "Sort jobs by id, status, target, ticket, created, updated, instance, branch, or pr.")
	return cmd
}

func newPipelineStatusCmd() *cobra.Command {
	var (
		repo     string
		all      bool
		watch    bool
		noClear  bool
		interval time.Duration
		limit    int
		jsonOut  bool
		format   string
		sortBy   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status [<pipeline>|--all]",
		Short: "Summarize pipeline jobs and next steps.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: pass at most one pipeline name.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: --interval must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parsePipelineStatusSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline status: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parsePipelineStatusFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline status: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline status: pipeline name is required.")
				return exitErr(2)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runPipelineStatusWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, sortMode, limit, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if err := runPipelineStatus(cmd.OutOrStdout(), teamDir, pipelineName, sortMode, limit, jsonOut, tmpl); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline status: %v\n", err)
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Summarize all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the pipeline status table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline status rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Pipeline}} {{.Jobs}} {{.ReadySteps}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "declared", "Sort rows by declared, pipeline, steps, jobs, queued, running, blocked, done, failed, ready, stale, manual, held, none, queue, queue-pending, queue-dead, or quarantined.")
	return cmd
}

func newPipelineTriageCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		staleAfter  time.Duration
		minSeverity string
		reasons     []string
		watch       bool
		noClear     bool
		interval    time.Duration
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "triage [<pipeline>|--all]",
		Short: "Show pipeline-owned jobs that need operator attention.",
		Long: "Show a compact pipeline-scoped work queue triage view from durable jobs, " +
			"persisted daemon queue items, status-file update previews, and ready pipeline steps. " +
			"With no pipeline, all pipeline-owned jobs are considered.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline triage: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline triage: pass at most one pipeline name.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline triage: --interval must be >= 0.")
				return exitErr(2)
			}
			if format != "" && (watch || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline triage: --format cannot be combined with --watch or --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobTriageFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline triage: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseJobTriageFilters(minSeverity, reasons)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline triage: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("stale-after") {
				configured, err := configuredJobTriageStaleAfter(teamDir)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline triage: %v\n", err)
					return exitErr(2)
				}
				staleAfter = configured
			}
			if staleAfter < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline triage: --stale-after must be >= 0.")
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline triage: pipeline name is required.")
				return exitErr(2)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runPipelineTriageWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, staleAfter, filters, jsonOut, interval, !noClear)
			}
			snapshot, err := collectPipelineTriage(teamDir, pipelineName, time.Now().UTC(), staleAfter, filters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline triage: %v\n", err)
				return exitErr(1)
			}
			return renderJobTriage(cmd.OutOrStdout(), snapshot, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Triage all pipeline-owned jobs. This is the default when no pipeline is passed.")
	cmd.Flags().DurationVar(&staleAfter, "stale-after", defaultJobTriageStaleAfter, "Flag queued or running jobs with no update after this duration (default: [health].job_stale_after or 24h; 0 disables stale checks).")
	cmd.Flags().StringVar(&minSeverity, "min-severity", "", "Only show attention rows at least this severe: critical, warning, or info.")
	cmd.Flags().StringSliceVar(&reasons, "reason", nil, "Only show attention rows with this reason. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the pipeline triage view until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline triage snapshot as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.")
	return cmd
}

func newPipelineExplainCmd() *cobra.Command {
	var (
		repo     string
		all      bool
		limit    int
		states   []string
		step     string
		watch    bool
		noClear  bool
		interval time.Duration
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "explain [<pipeline>|--all]",
		Short: "Explain pipeline jobs and step blockers.",
		Long: "Explain pipeline state from durable jobs, expanding each matching job with step readiness, " +
			"dependency blockers, gates, active instances, and suggested next actions.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline explain: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline explain: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline explain: pass at most one pipeline name.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline explain: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline explain: --interval must be >= 0.")
				return exitErr(2)
			}
			var stateFilter map[string]bool
			if cmd.Flags().Changed("state") {
				var err error
				stateFilter, err = parseJobNextStateFilter(states, false)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline explain: %v\n", err)
					return exitErr(2)
				}
			}
			tmpl, err := parsePipelineExplainFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline explain: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline explain: pipeline name is required.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runPipelineExplainWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, limit, stateFilter, step, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if err := runPipelineExplain(cmd.OutOrStdout(), teamDir, pipelineName, limit, stateFilter, step, jsonOut, tmpl); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline explain: %v\n", err)
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Explain all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit job explanations per pipeline; 0 means no limit.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Only explain jobs whose next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&step, "step", "", "Only include jobs and step details for this pipeline step id.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh pipeline explanations until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline explanations as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline explanation with a Go template, e.g. '{{.Pipeline}} {{len .Jobs}}'.")
	return cmd
}

func newPipelineNextCmd() *cobra.Command {
	var (
		repo     string
		teamName string
		all      bool
		limit    int
		watch    bool
		noClear  bool
		interval time.Duration
		sortBy   string
		reasons  []string
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "next [<pipeline>|--all]",
		Short: "Print recommended next actions for pipeline jobs.",
		Long:  "Print read-only recommended next actions from pipeline status rows. With no pipeline, all declared pipelines are considered.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline next: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline next: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline next: pass at most one pipeline name.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline next: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline next: --interval must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parsePipelineStatusSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline next: %v\n", err)
				return exitErr(2)
			}
			reasonFilters, err := parsePipelineNextReasonFilters(reasons)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline next: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parsePipelineNextFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline next: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline next: pipeline name is required.")
				return exitErr(2)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runPipelineNextWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, teamName, sortMode, limit, reasonFilters, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if err := runPipelineNext(cmd.OutOrStdout(), teamDir, pipelineName, teamName, sortMode, limit, reasonFilters, jsonOut, tmpl); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline next: %v\n", err)
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&teamName, "team", "", "Only consider pipelines owned by this declared team; actions are rendered with team-scoped commands.")
	cmd.Flags().BoolVar(&all, "all", false, "Consider all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of actions to print (0 = no limit).")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh recommended pipeline actions until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringVar(&sortBy, "sort", "declared", "Sort pipelines before selecting actions by declared, pipeline, steps, jobs, queued, running, blocked, done, failed, ready, stale, manual, held, none, queue, queue-pending, queue-dead, or quarantined.")
	cmd.Flags().StringSliceVar(&reasons, "reason", nil, "Only show actions with this reason. Values match exactly, or as prefixes before '='. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit recommended actions as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action with a Go template, e.g. '{{.Pipeline}} {{.Action}}'.")
	return cmd
}

func newPipelineWaitCmd() *cobra.Command {
	var (
		repo         string
		jobFilters   []string
		statuses     []string
		events       []string
		all          bool
		timeout      time.Duration
		interval     time.Duration
		failOnFailed bool
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "wait [<pipeline>|--all]",
		Short: "Wait for pipeline jobs to reach a lifecycle status or event.",
		Long: "Wait for every selected pipeline-owned job to reach one of the requested lifecycle statuses and/or last events. " +
			"By default this waits for terminal statuses: done or failed. When --event is set without --status, any status is accepted.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline wait: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline wait: pass at most one pipeline name.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline wait: --interval must be >= 0.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline wait: --timeout must be >= 0.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline wait: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline wait: --format cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			waitEvents := parseJobWaitEvents(events)
			waitStatuses, err := parseJobWaitStatuses(statuses, !cmd.Flags().Changed("status") && len(waitEvents) == 0)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline wait: %v\n", err)
				return exitErr(2)
			}
			if len(waitStatuses) == 0 && len(waitEvents) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline wait: pass at least one non-empty --status or --event.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline wait: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline wait: pipeline name is required.")
				return exitErr(2)
			}
			waitLabel := pipelineName
			if waitLabel == "" {
				waitLabel = "all pipelines"
			}
			jobs, err := selectedPipelineJobs(teamDir, pipelineName)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline wait: %v\n", err)
				return exitErr(1)
			}
			jobs, err = filterPipelineWaitJobs(jobs, jobFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline wait: %v\n", err)
				return exitErr(2)
			}
			if len(jobs) == 0 {
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode([]*job.Job{})
				}
				if !quiet && tmpl == nil {
					fmt.Fprintln(cmd.OutOrStdout(), "(no jobs)")
				}
				return nil
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			cancel := func() {}
			if timeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, timeout)
			}
			defer cancel()
			finalJobs, err := runPipelineWait(ctx, teamDir, jobs, waitStatuses, waitEvents, interval)
			if err != nil {
				if timeoutErr, ok := err.(*pipelineWaitTimeoutError); ok {
					if !quiet {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline wait: timed out waiting for %s to reach %s: %s\n",
							waitLabel, jobWaitConditionList(waitStatuses, waitEvents), pipelineWaitPendingSummary(timeoutErr.Pending))
					}
					return exitErr(1)
				}
				if err == context.Canceled {
					return nil
				}
				return err
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(finalJobs); err != nil {
					return err
				}
			} else if tmpl != nil {
				for _, j := range finalJobs {
					if err := renderJobTemplate(cmd.OutOrStdout(), j, tmpl); err != nil {
						return err
					}
				}
			} else if !quiet {
				for _, j := range finalJobs {
					fmt.Fprintf(cmd.OutOrStdout(), "  wait   %-20s %s\n", j.ID, j.Status)
				}
			}
			if failOnFailed && pipelineWaitHasFailed(finalJobs) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&jobFilters, "job", nil, "Only wait for these pipeline-owned job ids. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&events, "event", nil, "Last event to wait for, e.g. closed, adopted, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&all, "all", false, "Wait for jobs across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 500*time.Millisecond, "Polling interval.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "Exit 1 if any selected job resolves to failed.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit final pipeline jobs as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each final job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newPipelineQueueCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		eventTypes  []string
		jobs        []string
		runtimes    []string
		readyOnly   bool
		all         bool
		sortBy      string
		limit       int
		watch       bool
		noClear     bool
		summary     bool
		jsonOut     bool
		format      string
		interval    time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "queue [<pipeline>|--all]",
		Short: "List or control pipeline-owned queue items.",
		Long:  "List active queue items owned by one pipeline. With no pipeline, all pipeline-owned queue items are listed. Queue subcommands still require an explicit pipeline.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue: pass at most one pipeline name.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue: --interval must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseQueueListSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFiltersWithRuntime(stateFilter, nil, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue: pipeline name is required.")
				return exitErr(2)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runPipelineQueueSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runPipelineQueueListWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, filters, queueListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runPipelineQueueSummary(cmd.OutOrStdout(), teamDir, pipelineName, filters, jsonOut)
			}
			return runPipelineQueueList(cmd.OutOrStdout(), teamDir, pipelineName, filters, queueListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only show pending queue items whose next retry is due now.")
	cmd.Flags().BoolVar(&all, "all", false, "List queue items across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the pipeline queue table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate queue counts instead of queue rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline queue rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.AddCommand(newPipelineQueueShowCmd())
	cmd.AddCommand(newPipelineQueueQuarantineCmd())
	cmd.AddCommand(newPipelineQueueRetryCmd())
	cmd.AddCommand(newPipelineQueueDropCmd())
	cmd.AddCommand(newPipelineQueuePruneCmd())
	return cmd
}

func newPipelineQueueShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <pipeline> <id>",
		Short: "Show one queue item owned by one pipeline.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readPipelineQueueItem(cmd, teamDir, args[0], args[1], "show")
			if err != nil {
				return err
			}
			return renderQueueItemResultWithActions(cmd.OutOrStdout(), item, jsonOut, tmpl, pipelineQueueActionResolver(args[0]), queueRuntimeMap(teamDir))
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the queue item as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newPipelineQueueQuarantineCmd() *cobra.Command {
	var (
		repo         string
		stateFilter  string
		eventTypes   []string
		jobs         []string
		restorable   bool
		unrestorable bool
		all          bool
		sortBy       string
		limit        int
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "quarantine [<pipeline>|--all]",
		Short: "List pipeline-owned quarantined queue files.",
		Long:  "List quarantined queue files owned by one pipeline. With no pipeline, all pipeline-owned quarantined queue files are listed. Show, restore, and drop still require an explicit pipeline.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine: pass at most one pipeline name.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseQueueQuarantineSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team pipeline queue quarantine", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 && !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine: pipeline name is required.")
				return exitErr(2)
			}
			items, err := collectPipelineQueueQuarantineItems(teamDir, pipelineName, filters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine: %v\n", err)
				return exitErr(1)
			}
			items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
			items = prepareQueueQuarantineItems(items, sortMode, limit)
			return renderQueueQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined files that cannot be restored.")
	cmd.Flags().BoolVar(&all, "all", false, "List quarantined queue files across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", queueQuarantineSortFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline-owned quarantined queue files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline-owned quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	cmd.AddCommand(newPipelineQueueQuarantineShowCmd())
	cmd.AddCommand(newPipelineQueueQuarantineRestoreCmd())
	cmd.AddCommand(newPipelineQueueQuarantineDropCmd())
	return cmd
}

func newPipelineQueueQuarantineShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <pipeline> <quarantine-path>",
		Short: "Show one pipeline-owned quarantined queue file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team pipeline queue quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readPipelineQueueQuarantineItem(teamDir, args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine show: %v\n", err)
				return exitErr(1)
			}
			result, err := showQueueQuarantine(teamDir, item.Path)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine show: %v\n", err)
				return exitErr(1)
			}
			result.Pipeline = args[0]
			return renderQueueQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the pipeline-owned quarantined queue file as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline-owned quarantined queue file with a Go template, e.g. '{{.Pipeline}} {{.ID}}'.")
	return cmd
}

func newPipelineQueueQuarantineRestoreCmd() *cobra.Command {
	var (
		repo        string
		restoreAll  bool
		dryRun      bool
		force       bool
		stateFilter string
		eventTypes  []string
		jobs        []string
		sortBy      string
		limit       int
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <pipeline> [quarantine-path]",
		Short: "Restore pipeline-owned quarantined queue files.",
		Long:  "Restore one pipeline-owned quarantined queue file by path, or restore a filtered pipeline-owned batch of restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team pipeline queue quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: --limit must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if restoreAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: --all requires exactly one pipeline and cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseQueueQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: %v\n", err)
					return exitErr(2)
				}
				items, err := collectPipelineQueueQuarantineItems(teamDir, args[0], filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				items = filterQueueQuarantineRestorable(items, true, false)
				results, err := restoreQueueQuarantineItems(teamDir, items, dryRun, force, sortMode, limit)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				return renderQueueQuarantineRestoreMany(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: requires <pipeline> and one path unless --all is set.")
				return exitErr(2)
			}
			if !filters.empty() || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			if _, err := readPipelineQueueQuarantineItem(teamDir, args[0], args[1]); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			result, err := restoreQueueQuarantine(teamDir, args[1], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching pipeline-owned restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active queue file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching pipeline-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, restore at most this many matching pipeline-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newPipelineQueueQuarantineDropCmd() *cobra.Command {
	var (
		repo         string
		dropAll      bool
		dryRun       bool
		stateFilter  string
		eventTypes   []string
		jobs         []string
		restorable   bool
		unrestorable bool
		olderThan    time.Duration
		sortBy       string
		limit        int
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <pipeline> [quarantine-path]",
		Short: "Drop pipeline-owned quarantined queue files after inspection.",
		Long:  "Drop one pipeline-owned quarantined queue file by path, or drop a filtered pipeline-owned batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: --limit must be >= 0.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team pipeline queue quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: --all requires exactly one pipeline and cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseQueueQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: %v\n", err)
					return exitErr(2)
				}
				items, err := collectPipelineQueueQuarantineItems(teamDir, args[0], filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
				results, err := dropQueueQuarantineItems(teamDir, items, dryRun, olderThan, unrestorable, sortMode, limit, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				return renderQueueQuarantineDrop(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: requires <pipeline> and one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !filters.empty() || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			item, err := readPipelineQueueQuarantineItem(teamDir, args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: %v\n", err)
				return exitErr(1)
			}
			result, err := dropQueueQuarantineItem(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), item, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue quarantine drop: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineDrop(cmd.OutOrStdout(), []queueQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching pipeline-owned quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching pipeline-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching pipeline-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newPipelineQueueRetryCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		retryAll    bool
		dryRun      bool
		stateFilter string
		eventTypes  []string
		jobs        []string
		runtimes    []string
		readyOnly   bool
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <pipeline> [id]",
		Short: "Retry pipeline-owned queue items.",
		Long:  "Retry one pipeline-owned queue item by id, or retry a filtered pipeline-owned batch with --all. Batch retries default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue retry: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if retryAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue retry: --all requires exactly one pipeline and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue retry: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseQueueListSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue retry: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFiltersWithRuntime(effectiveState, nil, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue retry: %v\n", err)
					return exitErr(2)
				}
				return runPipelineQueueRetryAll(cmd.OutOrStdout(), teamDir, args[0], filters, sortMode, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue retry: requires <pipeline> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || len(jobs) > 0 || len(runtimes) > 0 || readyOnly || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue retry: --state, --event-type, --job, --runtime, --ready, --sort, and --limit require --all.")
				return exitErr(2)
			}
			item, err := readPipelineQueueItem(cmd, teamDir, args[0], args[1], "retry")
			if err != nil {
				return err
			}
			results, err := retryQueueItemMatches(teamDir, []*daemon.QueueItem{item}, dryRun)
			if err != nil {
				return err
			}
			return renderQueueRetryResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching pipeline-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching pipeline-owned queue items without retrying them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only retry pending queue items whose next retry is due now.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newPipelineQueueDropCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		dropAll     bool
		dryRun      bool
		stateFilter string
		eventTypes  []string
		jobs        []string
		runtimes    []string
		readyOnly   bool
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <pipeline> [id]",
		Short: "Drop pipeline-owned queue items.",
		Long:  "Drop one pipeline-owned queue item by id, or drop a filtered pipeline-owned batch with --all. Batch drops default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue drop: --all requires exactly one pipeline and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue drop: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseQueueListSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue drop: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFiltersWithRuntime(effectiveState, nil, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue drop: %v\n", err)
					return exitErr(2)
				}
				return runPipelineQueueDropAll(cmd.OutOrStdout(), teamDir, args[0], filters, sortMode, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue drop: requires <pipeline> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || len(jobs) > 0 || len(runtimes) > 0 || readyOnly || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue drop: --state, --event-type, --job, --runtime, --ready, --sort, and --limit require --all.")
				return exitErr(2)
			}
			item, err := readPipelineQueueItem(cmd, teamDir, args[0], args[1], "drop")
			if err != nil {
				return err
			}
			results, err := dropQueueItemMatches(teamDir, []*daemon.QueueItem{item}, dryRun)
			if err != nil {
				return err
			}
			return renderQueueDropResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching pipeline-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching pipeline-owned queue items without dropping them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only drop pending queue items whose next retry is due now.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newPipelineQueuePruneCmd() *cobra.Command {
	var (
		repo       string
		stateFlag  string
		olderThan  time.Duration
		dryRun     bool
		jsonOut    bool
		format     string
		eventTypes []string
		jobs       []string
		runtimes   []string
		readyOnly  bool
		limit      int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune <pipeline>",
		Short: "Prune pipeline-owned queue items.",
		Long:  "Prune pipeline-owned queue items. By default this removes dead-letter items owned by the selected pipeline.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline queue prune: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueuePruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseQueuePruneStateWithReady(stateFlag, readyOnly, cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue prune: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFiltersWithRuntime("", nil, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			return runPipelineQueuePrune(cmd.OutOrStdout(), teamDir, args[0], state, olderThan, filters, limit, time.Now().UTC(), dryRun, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFlag, "state", daemon.QueueStateDead, "Queue state to prune: dead, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune pipeline-owned items older than this duration based on retry/dead-letter/update time.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Filter by queued dispatch runtime before pruning: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only prune pending queue items whose next retry is due now. Defaults --state to pending when --state is omitted.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Prune at most this many matching pipeline-owned queue items; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview pipeline-owned queue items that would be pruned without dropping them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each prune result with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newPipelineReadyCmd() *cobra.Command {
	var (
		repo     string
		states   []string
		step     string
		sortBy   string
		limit    int
		all      bool
		watch    bool
		noClear  bool
		interval time.Duration
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ready [<pipeline>|--all]",
		Short: "List ready pipeline jobs.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: pass at most one pipeline name.")
				return exitErr(2)
			}
			stateFilter, err := parseJobNextStateFilter(states, !cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ready: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseJobReadySort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ready: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: --interval must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseJobReadyFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ready: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if !all && len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: pipeline name is required.")
				return exitErr(2)
			}
			opts := jobReadyOptions{
				Pipeline: pipelineName,
				States:   stateFilter,
				Step:     step,
				Sort:     sortMode,
				Limit:    limit,
			}
			allPipelines := all || pipelineName == ""
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runPipelineReadyWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, allPipelines, opts, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			return runPipelineReady(cmd.OutOrStdout(), teamDir, pipelineName, allPipelines, opts, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&step, "step", "", "Only include rows whose next step has this id.")
	cmd.Flags().StringVar(&sortBy, "sort", "job", "Sort rows by job, state, step, target, pipeline, updated, ticket, instance, or label.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&all, "all", false, "List ready jobs across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the ready-step table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit ready rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.")
	return cmd
}

func runPipelineReady(w io.Writer, teamDir, pipeline string, allPipelines bool, opts jobReadyOptions, jsonOut bool, tmpl *template.Template) error {
	rows, err := collectJobReadyRows(teamDir, opts.Pipeline, opts.States)
	if err != nil {
		return err
	}
	if allPipelines {
		rows = scopePipelineReadyRowsByOwner(rows)
	} else {
		rows = scopePipelineReadyRows(pipeline, rows)
	}
	rows = prepareJobReadyRows(rows, opts)
	return renderJobReadyRows(w, rows, jsonOut, tmpl)
}

func runPipelineReadyWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, allPipelines bool, opts jobReadyOptions, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runPipelineReady(w, teamDir, pipeline, allPipelines, opts, jsonOut, tmpl); err != nil {
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

func newPipelineAdvanceCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		limit         int
		all           bool
		allReadySteps bool
		dryRun        bool
		previewRoutes bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "advance <pipeline>|--all",
		Short: "Dispatch ready pipeline steps.",
		Long:  "Dispatch ready next steps for jobs in one pipeline, or across all pipelines with --all, using the same path as `agent-team job advance`.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if !all && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --limit must be >= 0.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineAdvanceFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline advance: %v\n", err)
				return exitErr(2)
			}
			waitEventsSet := map[string]bool{}
			waitStatusesSet := map[job.Status]bool{}
			if wait {
				waitEventsSet = parseJobWaitEvents(waitEvents)
				waitStatusesSet, err = parseJobWaitStatuses(waitStatuses, !cmd.Flags().Changed("wait-status") && len(waitEventsSet) == 0)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline advance: %v\n", err)
					return exitErr(2)
				}
				if len(waitStatusesSet) == 0 && len(waitEventsSet) == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: pass at least one non-empty --wait-status or --wait-event.")
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: pipeline name is required.")
				return exitErr(2)
			}
			results, err := advanceReadyPipelineJobs(cmd, teamDir, pipelineName, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, limit, dryRun, previewRoutes, allReadySteps)
			if err != nil {
				return err
			}
			if wait {
				results, err = waitForPipelineAdvanceResults(cmd, teamDir, results, waitStatusesSet, waitEventsSet, waitTimeout, waitInterval, "agent-team pipeline advance")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
			}
			if err := renderPipelineAdvanceResults(cmd.OutOrStdout(), results, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && pipelineAdvanceResultsHaveFailed(results) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced steps: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for advanced step dispatches. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready jobs, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&all, "all", false, "Advance ready steps across all pipelines.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent step for each selected job.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview ready steps without dispatching them.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include local topology route and dispatch payload previews.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After advancing, wait for advanced jobs to reach a lifecycle status or event.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any advanced job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit advance results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineApproveCmd() *cobra.Command {
	var (
		repo          string
		all           bool
		limit         int
		dispatchNow   bool
		workspace     string
		runtimeKind   string
		runtimeBin    string
		step          string
		message       string
		messageFile   string
		dryRun        bool
		previewRoutes bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "approve <pipeline>|--all",
		Short: "Approve blocked manual pipeline gates.",
		Long: "Approve blocked manual-gate steps for jobs in one pipeline, or all pipelines with --all. " +
			"By default this marks matching manual gates queued; pass --step to target one stage, or --dispatch to immediately dispatch each approved step.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: --limit must be >= 0.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if previewRoutes && (!dryRun || !dispatchNow) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: --preview-routes requires --dry-run and --dispatch.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineApproveFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline approve: %v\n", err)
				return exitErr(2)
			}
			waitEventsSet := map[string]bool{}
			waitStatusesSet := map[job.Status]bool{}
			if wait {
				waitEventsSet = parseJobWaitEvents(waitEvents)
				waitStatusesSet, err = parseJobWaitStatuses(waitStatuses, !cmd.Flags().Changed("wait-status") && len(waitEventsSet) == 0)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline approve: %v\n", err)
					return exitErr(2)
				}
				if len(waitStatusesSet) == 0 && len(waitEventsSet) == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: pass at least one non-empty --wait-status or --wait-event.")
					return exitErr(2)
				}
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: pipeline name is required.")
				return exitErr(2)
			}
			approvalMessage, err := optionalSendMessageBody(message, messageFile, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline approve: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := approvePipelineManualGates(cmd, teamDir, pipelineName, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, step, approvalMessage, limit, dispatchNow, dryRun, previewRoutes)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline approve: %v\n", err)
				return exitErr(1)
			}
			if wait {
				results, err = waitForPipelineApproveResults(cmd, teamDir, results, waitStatusesSet, waitEventsSet, waitTimeout, waitInterval, "agent-team pipeline approve")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
			}
			if err := renderPipelineApproveResults(cmd.OutOrStdout(), results, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && pipelineApproveResultsHaveFailed(results) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Approve manual gates across all pipelines.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum manual gates to approve (0 = no limit).")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch each approved manual gate immediately.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
	cmd.Flags().StringVar(&step, "step", "", "Approve only manual gates whose next blocked step has this id.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on each approved job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read approval message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview manual gate approvals and optional dispatches without writing job or daemon state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run --dispatch, include route and payload previews.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After approving or dispatching, wait for approved jobs to reach a lifecycle status or event.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any approved job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit approval results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each approval result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineRejectCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		limit       int
		step        string
		message     string
		messageFile string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "reject <pipeline>|--all",
		Short: "Reject blocked manual pipeline gates.",
		Long: "Reject blocked manual-gate steps for jobs in one pipeline, or all pipelines with --all. " +
			"Rejected gates are marked failed and record a manual_gate_rejected audit event.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline reject: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline reject: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline reject: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline reject: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineApproveFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline reject: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline reject: pipeline name is required.")
				return exitErr(2)
			}
			rejectionMessage, err := optionalSendMessageBody(message, messageFile, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline reject: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := rejectPipelineManualGates(teamDir, pipelineName, step, rejectionMessage, limit, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline reject: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineApproveResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Reject manual gates across all pipelines.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum manual gates to reject (0 = no limit).")
	cmd.Flags().StringVar(&step, "step", "", "Reject only manual gates whose next blocked step has this id.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on each rejected job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read rejection reason from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview manual gate rejections without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit rejection results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each rejection result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineUnblockCmd() *cobra.Command {
	var (
		repo         string
		all          bool
		limit        int
		step         string
		status       string
		from         string
		message      string
		messageFile  string
		allowMissing bool
		dryRun       bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "unblock <pipeline>|--all [message...]",
		Short: "Answer blocked pipeline workers.",
		Long: "Send the same operator answer to blocked pipeline step owners for jobs in one pipeline, or all pipelines with --all. " +
			"By default a job is selected when it has a single blocked step owner; pass --step to target one stage explicitly.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline unblock: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if len(args) == 0 && !all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline unblock: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if len(args) > 0 && !all && strings.TrimSpace(args[0]) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline unblock: pipeline name is required.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline unblock: --limit must be >= 0.")
				return exitErr(2)
			}
			next, err := parseJobUnblockStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline unblock: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parsePipelineUnblockFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline unblock: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			messageArgs := args
			if !all {
				pipelineName = strings.TrimSpace(args[0])
				messageArgs = args[1:]
			}
			body, err := sendMessageBody(message, messageFile, messageArgs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline unblock: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			results, err := unblockPipelineJobs(teamDir, pipelineName, client, step, body, normalizedJobUnblockSender(from), next, limit, allowMissing, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline unblock: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineUnblockResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Unblock matching jobs across all pipelines.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum blocked jobs to unblock or report (0 = no limit).")
	cmd.Flags().StringVar(&step, "step", "", "Unblock only blocked jobs whose selected step has this id.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusRunning), "Status after unblocking: running or queued.")
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with each unblock message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&allowMissing, "allow-missing", false, "Allow queueing messages for owning instances the daemon does not know yet.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching unblocks without writing job state or mailbox messages.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit unblock results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each unblock result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}} {{.Instance}}'.")
	return cmd
}

func newPipelineSkipCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		limit       int
		step        string
		message     string
		messageFile string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "skip <pipeline>|--all --step <id>",
		Short: "Mark matching pipeline steps intentionally skipped.",
		Long: "Mark matching non-running pipeline steps as done with skipped metadata for jobs in one pipeline, or all pipelines with --all. " +
			"The step id is required to prevent accidental broad bypasses.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline skip: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline skip: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline skip: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if strings.TrimSpace(step) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline skip: --step is required.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline skip: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineSkipFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline skip: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline skip: pipeline name is required.")
				return exitErr(2)
			}
			skipMessage, err := optionalSendMessageBody(message, messageFile, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline skip: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := skipPipelineSteps(teamDir, pipelineName, step, skipMessage, limit, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline skip: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineSkipResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Skip matching steps across all pipelines.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum matching steps to skip or report (0 = no limit).")
	cmd.Flags().StringVar(&step, "step", "", "Required pipeline step id to mark skipped.")
	cmd.Flags().StringVar(&message, "message", "", "Skip reason recorded on each updated job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read skip reason from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview skipped steps without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit skip results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each skip result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineCancelCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		actor       string
		message     string
		messageFile string
		limit       int
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cancel <pipeline>|--all",
		Short: "Cancel non-terminal pipeline jobs.",
		Long: "Cancel queued, running, or blocked jobs in one pipeline, or all pipelines with --all, by marking the durable job failed with a cancelled audit event. " +
			"Batch cancellation only updates job files; use job cancel --stop or --kill when an owning instance should also be stopped.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline cancel: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline cancel: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline cancel: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline cancel: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineCancelFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline cancel: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline cancel: pipeline name is required.")
				return exitErr(2)
			}
			cancelMessage, err := optionalSendMessageBody(message, messageFile, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline cancel: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := cancelPipelineJobs(teamDir, pipelineName, cancelMessage, actor, limit, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline cancel: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineCancelResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Cancel non-terminal jobs across all pipelines.")
	cmd.Flags().StringVar(&actor, "actor", "cli", "Actor label recorded in cancellation audit events.")
	cmd.Flags().StringVar(&message, "message", "", "Cancellation reason recorded on each cancelled job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read cancellation reason from a file, or '-' for stdin.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum matching jobs to cancel (0 = no limit).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview cancellations without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit cancellation results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each cancellation result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StatusAfter}}'.")
	return cmd
}

func newPipelineResumePlanCmd() *cobra.Command {
	var (
		repo          string
		stepID        string
		statusFilters []string
		runtimeFilter []string
		actionFilters []string
		staleOnly     bool
		runtimeStale  bool
		unhealthyOnly bool
		summary       bool
		jsonOut       bool
		all           bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "resume-plan [<pipeline>|--all]",
		Short: "Show runtime resume and fallback commands for pipeline-owned jobs.",
		Long: "Show runtime resume and fallback commands for daemon metadata owned by jobs in one declared pipeline, or omit the pipeline/pass --all to inspect every pipeline-owned job. " +
			"This is the pipeline-scoped form of `agent-team runtime resume-plan`.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline resume-plan: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline resume-plan: --summary cannot be combined with --format.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline resume-plan: pass at most one pipeline name.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline resume-plan: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline resume-plan: pipeline name is required.")
				return exitErr(2)
			}
			tmpl, err := parseRuntimeResumePlanFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline resume-plan: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			plans, err := collectPipelineRuntimeResumePlans(teamDir, pipelineName, stepID, statusFilters, runtimeFilter, actionFilters, staleOnly || runtimeStale, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline resume-plan: %v\n", err)
				return exitErr(1)
			}
			if summary {
				out := summarizeRuntimeResumePlans(plans)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				renderRuntimeResumeSummary(cmd.OutOrStdout(), out)
				return nil
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(plans)
			}
			if tmpl != nil {
				return renderRuntimeResumePlanFormat(cmd.OutOrStdout(), plans, tmpl)
			}
			renderRuntimeResumePlans(cmd.OutOrStdout(), plans)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Only include plans for this pipeline step id.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilter, "runtime", nil, "Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Only include running metadata whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only include crashed or stale running metadata.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching pipeline resume plans by recommended action, runtime, and status.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&all, "all", false, "Plan runtime recovery across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.")
	return cmd
}

func newPipelineCleanupCmd() *cobra.Command {
	var (
		repo        string
		merged      bool
		forceBranch bool
		verifyPR    bool
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cleanup <pipeline>",
		Short: "Clean up done jobs owned by one pipeline.",
		Long:  "Preview or remove job-owned worktrees and branches for done jobs owned by one declared pipeline. Applying cleanup requires --merged after confirming the matching PRs have merged.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline cleanup: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobCleanupFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline cleanup: %v\n", err)
				return exitErr(2)
			}
			if !merged && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline cleanup: pass --merged after confirming the pipeline's PRs have merged.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			jobs, err := selectedPipelineJobs(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline cleanup: %v\n", err)
				return exitErr(1)
			}
			result := runJobCleanupJobs(teamDir, filepath.Dir(teamDir), jobs, dryRun, merged, forceBranch, verifyPR)
			result.Pipeline = strings.TrimSpace(args[0])
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return err
				}
			} else if tmpl != nil {
				if err := renderJobCleanupFormat(cmd.OutOrStdout(), result, tmpl); err != nil {
					return err
				}
			} else {
				renderJobCleanupBatch(cmd.OutOrStdout(), result)
			}
			if result.Failed > 0 {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview done pipeline-owned job cleanup without removing worktrees or branches.")
	cmd.Flags().BoolVar(&merged, "merged", false, "Confirm matching done pipeline jobs' PRs are merged and apply cleanup.")
	cmd.Flags().BoolVar(&forceBranch, "force-branch", false, "Delete recorded branches even when git does not consider them merged.")
	cmd.Flags().BoolVar(&verifyPR, "verify-pr", false, "Use gh to verify each recorded PR is merged before cleanup.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline cleanup result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the cleanup result with a Go template, e.g. '{{.Pipeline}} {{.Cleaned}} {{.Failed}}'.")
	return cmd
}

func newPipelineSendCmd() *cobra.Command {
	var (
		repo           string
		from           string
		message        string
		messageFile    string
		allStatuses    bool
		latest         bool
		last           int
		statusFilters  []string
		runtimeFilters []string
		phaseFilters   []string
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		dryRun         bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "send <pipeline> [message...]",
		Short: "Send a mailbox message to pipeline-owned instances.",
		Long: "Send a mailbox message to daemon-known instances owned by jobs in one declared pipeline. " +
			"Use --all to include every lifecycle status, or combine selectors such as --status, --runtime, --phase, --latest, --last, --stale, --runtime-stale, and --unhealthy.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline send: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline send: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline send: choose one of --latest or --last.")
				return exitErr(2)
			}
			formatTemplate, err := parseSendFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline send: %v\n", err)
				return exitErr(2)
			}
			body, err := sendMessageBody(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline send: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := strings.TrimSpace(args[0])
			if pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline send: pipeline name is required.")
				return exitErr(2)
			}
			if _, err := loadPipelineInfo(teamDir, pipelineName); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline send: %v\n", err)
				return exitErr(1)
			}
			baseClient, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			effectiveStatuses := append([]string(nil), statusFilters...)
			if !allStatuses && len(effectiveStatuses) == 0 && !staleOnly && !runtimeStale && !unhealthyOnly {
				effectiveStatuses = []string{string(daemon.StatusRunning)}
			}
			opts := sendOptions{
				From:            from,
				All:             true,
				Latest:          latest,
				Limit:           last,
				StatusFilters:   effectiveStatuses,
				RuntimeFilters:  runtimeFilters,
				PhaseFilters:    phaseFilters,
				Stale:           staleOnly,
				RuntimeStale:    runtimeStale,
				Unhealthy:       unhealthyOnly,
				StaleByInstance: staleInstanceSet(teamDir, time.Now()),
				DryRun:          dryRun,
				JSON:            jsonOut,
				Format:          formatTemplate,
			}
			if len(phaseFilters) > 0 {
				opts.PhaseByInstance = sendPhaseByInstance(teamDir, time.Now())
			}
			client := pipelineSendClient{sendClient: baseClient, teamDir: teamDir, pipeline: pipelineName}
			return runSendSelectionWithClient(cmd.OutOrStdout(), cmd.ErrOrStderr(), client, body, opts)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&allStatuses, "all", false, "Send to every daemon-known pipeline instance regardless of lifecycle status.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Send to the most recently started pipeline-owned daemon-known instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Send to the N most recently started pipeline-owned daemon-known instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Send to pipeline-owned instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Send to pipeline-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Send to pipeline-owned instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Send to pipeline-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Send to pipeline-owned running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Send to pipeline-owned instances that are crashed, status-stale, or runtime-stale.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching recipients without appending mailbox messages.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.")
	return cmd
}

func newPipelinePsCmd() *cobra.Command {
	var (
		repo             string
		allPipelines     bool
		watch            bool
		jsonOut          bool
		quiet            bool
		summary          bool
		latest           bool
		last             int
		noClear          bool
		format           string
		sortBy           string
		interval         time.Duration
		statusFilters    []string
		runtimeFilters   []string
		agentFilters     []string
		phaseFilters     []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ps [<pipeline>|--all]",
		Short: "List pipeline-owned instances.",
		Long: "List daemon-aware instance rows owned by jobs in one declared pipeline. " +
			"Omit the pipeline or pass --all to inspect every pipeline-owned job while excluding ad hoc instances.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: pass at most one pipeline name.")
				return exitErr(2)
			}
			if allPipelines && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: --interval must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: choose one of --latest or --last.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: --quiet cannot be combined with --watch.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: --quiet cannot be combined with --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			opts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, nil, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ps: %v\n", err)
				return exitErr(2)
			}
			opts.runtimeStale = runtimeStaleOnly
			sortMode, err := parsePsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ps: %v\n", err)
				return exitErr(2)
			}
			opts.Sort = sortMode
			opts.SortSet = cmd.Flags().Changed("sort")
			opts.Limit = last
			if latest {
				opts.Limit = 1
			}
			formatTemplate, err := parsePsFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ps: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ps: pipeline name is required.")
				return exitErr(2)
			}
			if quiet {
				return runPipelinePsQuiet(cmd.OutOrStdout(), teamDir, pipelineName, time.Now(), opts)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				switch {
				case summary:
					return runPipelinePsSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, interval, time.Now, jsonOut, opts, clear)
				case formatTemplate != nil:
					return runPipelinePsFormatWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, interval, time.Now, opts, formatTemplate)
				default:
					return runPipelinePsWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, interval, time.Now, jsonOut, opts, clear)
				}
			}
			switch {
			case summary && jsonOut:
				return runPipelinePsSummaryJSON(cmd.OutOrStdout(), teamDir, pipelineName, time.Now(), opts)
			case summary:
				return runPipelinePsSummary(cmd.OutOrStdout(), teamDir, pipelineName, time.Now(), opts)
			case jsonOut:
				return runPipelinePsJSON(cmd.OutOrStdout(), teamDir, pipelineName, time.Now(), opts)
			case formatTemplate != nil:
				return runPipelinePsFormat(cmd.OutOrStdout(), teamDir, pipelineName, time.Now(), opts, formatTemplate)
			default:
				return runPipelinePs(cmd.OutOrStdout(), teamDir, pipelineName, time.Now(), opts)
			}
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&allPipelines, "all", false, "List instances across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh pipeline instance rows until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON array per refresh.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only print matching pipeline-owned instance names.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show lifecycle counts instead of pipeline instance rows.")
	cmd.Flags().BoolVarP(&latest, "latest", "l", false, "Show only the most recently started pipeline-owned instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started pipeline-owned instances after other filters (0 = all).")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show pipeline-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show pipeline-owned running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed, status-stale, or runtime-stale pipeline-owned instances.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Instance}} {{.Status}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show pipeline-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show pipeline-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show pipeline-owned instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show pipeline-owned work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	return cmd
}

func newPipelineStatsCmd() *cobra.Command {
	var (
		repo             string
		allPipelines     bool
		latest           bool
		last             int
		watch            bool
		jsonOut          bool
		summary          bool
		noClear          bool
		format           string
		sortBy           string
		interval         time.Duration
		statusFilters    []string
		runtimeFilters   []string
		phaseFilters     []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "stats [<pipeline>|--all]",
		Short: "Show CPU and memory usage for pipeline-owned instances.",
		Long: "Show a one-shot or watchable resource snapshot for daemon-known instances owned by durable jobs in one declared pipeline. " +
			"Omit the pipeline or pass --all to inspect every pipeline-owned job. With no filters, only running pipeline-owned instances are shown; use --status or --unhealthy to include inactive rows.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline stats: pass at most one pipeline name.")
				return exitErr(2)
			}
			if allPipelines && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline stats: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline stats: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline stats: choose one of --latest or --last.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline stats: --interval must be >= 0.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline stats: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseStatsFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline stats: %v\n", err)
				return exitErr(2)
			}
			opts, err := newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(false, statusFilters, runtimeFilters, nil, phaseFilters, nil, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline stats: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseStatsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline stats: %v\n", err)
				return exitErr(2)
			}
			opts.Sort = sortMode
			opts.SortSet = cmd.Flags().Changed("sort")
			opts.Latest = latest
			opts.Limit = last
			opts.Stale = staleOnly
			opts.RuntimeStale = runtimeStaleOnly
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline stats: pipeline name is required.")
				return exitErr(2)
			}
			opts.phaseByInstance = statsPhaseByInstance(teamDir, time.Now())
			opts.staleByInstance = staleInstanceSet(teamDir, time.Now())
			var base instanceLister
			base, err = newDaemonClient(teamDir)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					base = localInstanceLister{daemonRoot: daemon.DaemonRoot(teamDir)}
				} else {
					return err
				}
			}
			lister := pipelineStatsLister{instanceLister: base, teamDir: teamDir, pipeline: pipelineName}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				switch {
				case summary:
					return runStatsSummaryWatchWithClear(ctx, cmd.OutOrStdout(), lister, nil, opts, interval, time.Now, readProcessStats, jsonOut, clear)
				case formatTemplate != nil:
					return runStatsFormatWatch(ctx, cmd.OutOrStdout(), lister, nil, opts, interval, time.Now, readProcessStats, formatTemplate)
				default:
					return runStatsWatchWithClear(ctx, cmd.OutOrStdout(), lister, nil, opts, interval, time.Now, readProcessStats, jsonOut, clear)
				}
			}
			switch {
			case summary && jsonOut:
				return runStatsSummaryJSON(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats)
			case summary:
				return runStatsSummary(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats)
			case jsonOut:
				return runStatsJSON(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats)
			case formatTemplate != nil:
				return runStatsFormat(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats, formatTemplate)
			default:
				return runStats(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats)
			}
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&allPipelines, "all", false, "Show stats across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show stats for the most recently started pipeline-owned instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show stats for the N most recently started pipeline-owned instances after other filters (0 = all).")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh pipeline stats until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON array per refresh.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate CPU, memory, and RSS totals instead of pipeline instance rows.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Instance}} {{.CPUPercent}} {{.RSS}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show pipeline-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show pipeline-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show pipeline-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show pipeline-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show pipeline-owned running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed, status-stale, or runtime-stale pipeline-owned instances.")
	return cmd
}

func newPipelineLogsCmd() *cobra.Command {
	var (
		repo             string
		follow           bool
		latest           bool
		last             int
		list             bool
		jsonOut          bool
		noPrefix         bool
		statuses         []string
		runtimes         []string
		phases           []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthy        bool
		lastMsg          bool
		clean            bool
		all              bool
		tail             string
		since            string
		grep             string
		format           string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "logs [<pipeline>|--all]",
		Short: "Show daemon-captured logs for pipeline-owned jobs.",
		Long:  "Show daemon-captured logs for jobs in one declared pipeline, or omit the pipeline/pass --all to inspect every pipeline-owned job.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: pass at most one pipeline name.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last must be >= 0.")
				return exitErr(2)
			}
			if jsonOut && !list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --json requires --list.")
				return exitErr(2)
			}
			if format != "" && !list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --format requires --list.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if list && (follow || cmd.Flags().Changed("tail")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --list cannot be combined with --follow or --tail.")
				return exitErr(2)
			}
			if lastMsg {
				if follow {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last-message cannot be combined with --follow.")
					return exitErr(2)
				}
				if list {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last-message cannot be combined with --list.")
					return exitErr(2)
				}
				if jsonOut {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last-message cannot be combined with --json.")
					return exitErr(2)
				}
				if format != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last-message cannot be combined with --format.")
					return exitErr(2)
				}
				if cmd.Flags().Changed("tail") {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last-message cannot be combined with --tail.")
					return exitErr(2)
				}
				if strings.TrimSpace(since) != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last-message cannot be combined with --since.")
					return exitErr(2)
				}
				if strings.TrimSpace(grep) != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last-message cannot be combined with --grep.")
					return exitErr(2)
				}
				if clean {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --last-message cannot be combined with --clean.")
					return exitErr(2)
				}
			}
			if clean && list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --clean cannot be combined with --list.")
				return exitErr(2)
			}
			formatTemplate, err := parseLogListFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline logs: %v\n", err)
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline logs: %v\n", err)
				return exitErr(2)
			}
			sinceCutoff, err := parseLogSince(since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline logs: %v\n", err)
				return exitErr(2)
			}
			grepPattern, err := parseLogGrep(grep)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline logs: %v\n", err)
				return exitErr(2)
			}
			if sinceCutoff != nil && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --since cannot be combined with --follow because captured logs are not timestamped.")
				return exitErr(2)
			}
			if grepPattern != nil && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --grep cannot be combined with --follow.")
				return exitErr(2)
			}
			if grepPattern != nil && list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: --grep cannot be combined with --list.")
				return exitErr(2)
			}
			listOpts, err := newLogListOptionsWithRuntimeAndUnhealthy(statuses, runtimes, nil, phases, staleOnly, unhealthy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline logs: %v\n", err)
				return exitErr(2)
			}
			listOpts.runtimeStale = runtimeStaleOnly
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline logs: pipeline name is required.")
				return exitErr(2)
			}
			opts := logsOptions{
				Follow:       follow,
				Latest:       latest,
				Limit:        last,
				List:         list,
				JSON:         jsonOut,
				NoPrefix:     noPrefix,
				Tail:         tailLines,
				TailSet:      cmd.Flags().Changed("tail"),
				Since:        sinceCutoff,
				Grep:         grepPattern,
				Format:       formatTemplate,
				RuntimeStale: runtimeStaleOnly,
				Unhealthy:    unhealthy,
				LastMessage:  lastMsg,
				Clean:        clean,
			}
			return runPipelineLogs(cmd, teamDir, pipelineName, opts, listOpts)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail selected pipeline logs.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show the most recently started pipeline instance log.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show logs for the N most recently started pipeline instances (0 = all).")
	cmd.Flags().BoolVar(&list, "list", false, "List pipeline log streams instead of printing log content.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON with --list.")
	cmd.Flags().BoolVar(&noPrefix, "no-prefix", false, "Do not prefix lines when streaming multiple pipeline logs.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Only show logs for pipeline-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Only show logs for work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show logs for pipeline instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show logs for pipeline instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthy, "unhealthy", false, "Only show logs for crashed, status-stale, or runtime-stale pipeline instances.")
	cmd.Flags().BoolVar(&lastMsg, "last-message", false, "Show clean final Codex response sidecars instead of raw runtime logs.")
	cmd.Flags().BoolVar(&clean, "clean", false, "Hide known Codex runtime diagnostic noise when printing raw pipeline logs.")
	cmd.Flags().BoolVar(&all, "all", false, "Show logs across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	cmd.Flags().StringVar(&format, "format", "", "With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.")
	return cmd
}

func newPipelineEventsCmd() *cobra.Command {
	var (
		repo             string
		follow           bool
		tail             int
		jsonOut          bool
		summary          bool
		format           string
		actionFilters    []string
		statusFilters    []string
		runtimeFilters   []string
		phaseFilters     []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
		all              bool
		sinceRaw         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "events [<pipeline>|--all]",
		Short: "Show lifecycle events scoped to pipeline-owned jobs.",
		Long:  "Show or follow daemon lifecycle events for daemon-known instances owned by jobs in one declared pipeline, or omit the pipeline/pass --all to inspect every pipeline-owned job.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline events: pass at most one pipeline name.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline events: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if tail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline events: --tail must be >= 0.")
				return exitErr(2)
			}
			if summary && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline events: --summary cannot be combined with --follow.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline events: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseEventFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline events: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if len(args) == 1 && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline events: pipeline name is required.")
				return exitErr(2)
			}
			filters, err := pipelineEventFilters(teamDir, pipelineName, actionFilters, statusFilters, sinceRaw, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline events: %v\n", err)
				return exitErr(2)
			}
			filters, err = pipelineEventRuntimeFilter(teamDir, pipelineName, filters, runtimeFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline events: %v\n", err)
				return exitErr(2)
			}
			phases, err := lifecyclePhaseFilterSet(phaseFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline events: %v\n", err)
				return exitErr(2)
			}
			if len(phases) > 0 || staleOnly || runtimeStaleOnly || unhealthyOnly {
				filters, err = applyCurrentEventInstanceFilter(teamDir, filters, phases, staleOnly, runtimeStaleOnly, unhealthyOnly, time.Now())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline events: %v\n", err)
					return exitErr(1)
				}
			}
			var client eventsClient
			if dc, err := newDaemonClient(teamDir); err == nil {
				client = dc
			} else if errors.Is(err, errDaemonNotRunning) {
				client = localEventsClient{daemonRoot: daemon.DaemonRoot(teamDir)}
			} else {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return runEvents(ctx, cmd.OutOrStdout(), client, eventsOptions{Follow: follow, Tail: tail, JSON: jsonOut, Summary: summary, Format: formatTemplate, Filters: filters})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep streaming new lifecycle events.")
	cmd.Flags().IntVar(&tail, "tail", 0, "Show only the last N matching pipeline events before returning or following (0 = all).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit raw JSONL events.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching pipeline events by action, status, agent, and instance.")
	cmd.Flags().StringVar(&format, "format", "", "Render each event with a Go template, e.g. '{{.Action}} {{.Instance}} {{.Status}}'.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only show events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show events with this lifecycle status. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show pipeline events for daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show pipeline events for instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show pipeline events for instances whose status.toml is currently stale or missing.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show pipeline events for instances whose recorded runtime PID is currently no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show pipeline events for instances that are currently crashed, status-stale, or runtime-stale.")
	cmd.Flags().BoolVar(&all, "all", false, "Show events across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().StringVar(&sinceRaw, "since", "", "Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	return cmd
}

func newPipelineRetryCmd() *cobra.Command {
	var (
		repo          string
		all           bool
		limit         int
		dispatchNow   bool
		workspace     string
		runtimeKind   string
		runtimeBin    string
		step          string
		message       string
		messageFile   string
		force         bool
		dryRun        bool
		previewRoutes bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <pipeline>|--all",
		Short: "Reset failed pipeline steps for another attempt.",
		Long: "Reset failed pipeline steps for jobs in one pipeline, or all pipelines with --all. " +
			"By default this makes failed steps ready for the next pipeline advance; pass --step to target one stage, or --dispatch to immediately dispatch each retry.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: --limit must be >= 0.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if previewRoutes && (!dryRun || !dispatchNow) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: --preview-routes requires --dry-run and --dispatch.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineRetryFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline retry: %v\n", err)
				return exitErr(2)
			}
			waitEventsSet := map[string]bool{}
			waitStatusesSet := map[job.Status]bool{}
			if wait {
				waitEventsSet = parseJobWaitEvents(waitEvents)
				waitStatusesSet, err = parseJobWaitStatuses(waitStatuses, !cmd.Flags().Changed("wait-status") && len(waitEventsSet) == 0)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline retry: %v\n", err)
					return exitErr(2)
				}
				if len(waitStatusesSet) == 0 && len(waitEventsSet) == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: pass at least one non-empty --wait-status or --wait-event.")
					return exitErr(2)
				}
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: pipeline name is required.")
				return exitErr(2)
			}
			retryMessage, err := optionalSendMessageBody(message, messageFile, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline retry: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := retryPipelineJobs(cmd, teamDir, pipelineName, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, step, retryMessage, limit, force, dispatchNow, dryRun, previewRoutes)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline retry: %v\n", err)
				return exitErr(1)
			}
			if wait {
				results, err = waitForPipelineRetryResults(cmd, teamDir, results, waitStatusesSet, waitEventsSet, waitTimeout, waitInterval, "agent-team pipeline retry")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
			}
			if err := renderPipelineRetryResults(cmd.OutOrStdout(), results, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && pipelineRetryResultsHaveFailed(results) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Retry failed steps across all pipelines.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum failed jobs to retry (0 = no limit).")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch each reset failed step immediately.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
	cmd.Flags().StringVar(&step, "step", "", "Retry only failed jobs whose next failed step has this id.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on each retried job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read retry message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&force, "force", false, "Ignore step max_attempts caps for this explicit retry.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview failed-step resets and optional dispatches without writing job or daemon state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run --dispatch, include route and payload previews.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After retrying or dispatching, wait for retried jobs to reach a lifecycle status or event.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any retried job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit retry results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each retry result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineTimeoutCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		limit       int
		step        string
		target      string
		message     string
		messageFile string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "timeout <pipeline>|--all",
		Short: "Mark stale running pipeline steps failed.",
		Long: "Mark stale running pipeline steps failed so they can be retried through the normal retry flow. " +
			"A running step is stale when it exceeds its step timeout, or [health].job_stale_after when no step timeout is declared.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline timeout: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline timeout: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline timeout: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline timeout: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineTimeoutFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline timeout: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline timeout: pipeline name is required.")
				return exitErr(2)
			}
			timeoutMessage, err := optionalSendMessageBody(message, messageFile, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline timeout: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := timeoutPipelineJobs(teamDir, pipelineName, step, target, timeoutMessage, limit, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline timeout: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineTimeoutResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Mark stale running steps failed across all pipelines.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum stale running steps to mark failed (0 = no limit).")
	cmd.Flags().StringVar(&step, "step", "", "Mark only stale running steps with this id.")
	cmd.Flags().StringVar(&target, "target-agent", "", "Mark only stale running steps targeting this agent.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on each timed-out job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read timeout message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview stale-step failures without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit timeout results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each timeout result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineRepairCmd() *cobra.Command {
	var (
		repo               string
		workspace          string
		runtimeKind        string
		runtimeBin         string
		limit              int
		dryRun             bool
		previewRoutes      bool
		jsonOut            bool
		format             string
		skipDaemon         bool
		skipQueue          bool
		skipAdvance        bool
		timeoutJobs        bool
		timeoutPipelines   bool
		retryPipelines     bool
		allReadySteps      bool
		timeoutStep        string
		timeoutMessage     string
		timeoutMessageFile string
		timeoutTarget      string
		retryStep          string
		retryMessage       string
		retryMessageFile   string
		retryForce         bool
		readyTimeout       time.Duration
		wait               bool
		waitStatuses       []string
		waitEvents         []string
		waitTimeout        time.Duration
		waitInterval       time.Duration
		failOnFailed       bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "repair <pipeline>",
		Short: "Recover unhealthy orchestration state for one pipeline.",
		Long: "Recover unhealthy orchestration state scoped to one pipeline: ensure the daemon is ready, retry pipeline-owned dead-letter queue items, " +
			"optionally time out stale work, retry failed steps, and advance ready steps. Use --dry-run to preview.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --limit must be >= 0.")
				return exitErr(2)
			}
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			if wait && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if wait && skipAdvance && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --wait requires repair dispatch; remove --skip-advance or add --retry-pipelines.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: wait-related flags require --wait.")
				return exitErr(2)
			}
			if retryPipelines && skipDaemon && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --retry-pipelines requires daemon access unless --dry-run is set.")
				return exitErr(2)
			}
			if timeoutJobs && timeoutPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --timeout-jobs cannot be combined with --timeout-pipelines.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutMessage) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --timeout-message requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutMessageFile) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --timeout-message-file requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutStep) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --timeout-step requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutTarget) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --timeout-target-agent requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(retryMessage) != "" && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --retry-message requires --retry-pipelines.")
				return exitErr(2)
			}
			if strings.TrimSpace(retryMessageFile) != "" && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --retry-message-file requires --retry-pipelines.")
				return exitErr(2)
			}
			if strings.TrimSpace(retryStep) != "" && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --retry-step requires --retry-pipelines.")
				return exitErr(2)
			}
			if retryForce && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --retry-force requires --retry-pipelines.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseRepairFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline repair: %v\n", err)
				return exitErr(2)
			}
			waitEventsSet := map[string]bool{}
			waitStatusesSet := map[job.Status]bool{}
			if wait {
				waitEventsSet = parseJobWaitEvents(waitEvents)
				waitStatusesSet, err = parseJobWaitStatuses(waitStatuses, !cmd.Flags().Changed("wait-status") && len(waitEventsSet) == 0)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline repair: %v\n", err)
					return exitErr(2)
				}
				if len(waitStatusesSet) == 0 && len(waitEventsSet) == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline repair: pass at least one non-empty --wait-status or --wait-event.")
					return exitErr(2)
				}
			}
			resolvedTimeoutMessage, err := optionalMessageBodyWithFlagNames(timeoutMessage, timeoutMessageFile, nil, "--timeout-message", "--timeout-message-file")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline repair: %v\n", err)
				return exitErr(2)
			}
			resolvedRetryMessage, err := optionalMessageBodyWithFlagNames(retryMessage, retryMessageFile, nil, "--retry-message", "--retry-message-file")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline repair: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := runPipelineRepair(cmd, repo, teamDir, args[0], pipelineRepairOptions{
				Workspace:        workspace,
				Runtime:          runtimeSelection{Kind: runtimeKind, Binary: runtimeBin},
				Limit:            limit,
				DryRun:           dryRun,
				PreviewRoutes:    previewRoutes,
				SkipDaemon:       skipDaemon,
				SkipQueue:        skipQueue,
				SkipAdvance:      skipAdvance,
				TimeoutJobs:      timeoutJobs,
				TimeoutPipelines: timeoutPipelines,
				RetryPipelines:   retryPipelines,
				AllReadySteps:    allReadySteps,
				TimeoutStep:      timeoutStep,
				TimeoutMessage:   resolvedTimeoutMessage,
				TimeoutTarget:    timeoutTarget,
				RetryStep:        retryStep,
				RetryMessage:     resolvedRetryMessage,
				RetryForce:       retryForce,
				ReadyTimeout:     readyTimeout,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline repair: %v\n", err)
				return exitErr(1)
			}
			if wait {
				if err := waitForPipelineRepairResult(cmd, teamDir, result, waitStatusesSet, waitEventsSet, waitTimeout, waitInterval); err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
			}
			if err := renderPipelineRepairResult(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && pipelineRepairResultHasFailed(result) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for retried or advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for retried or advanced step dispatches (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for retried or advanced step dispatches. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Retry at most this many pipeline-owned dead-letter queue items or failed pipeline jobs, and advance at most this many ready jobs or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview pipeline repair actions without mutating state or starting the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include route and dispatch payload previews for retried or ready pipeline steps.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline repair result with a Go template, e.g. '{{.Pipeline}} {{.Queue.Action}}'.")
	cmd.Flags().BoolVar(&skipDaemon, "skip-daemon", false, "Do not start or reconcile the daemon.")
	cmd.Flags().BoolVar(&skipQueue, "skip-queue", false, "Do not retry pipeline-owned dead-letter queue items.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Do not advance ready pipeline steps after repair.")
	cmd.Flags().BoolVar(&timeoutJobs, "timeout-jobs", false, "Mark stale running pipeline job work failed before retrying failed steps.")
	cmd.Flags().BoolVar(&timeoutPipelines, "timeout-pipelines", false, "Mark stale running pipeline steps failed before retrying failed steps.")
	cmd.Flags().BoolVar(&retryPipelines, "retry-pipelines", false, "Reset failed pipeline steps and dispatch them before the scoped advance.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent pipeline step during the scoped repair advance.")
	cmd.Flags().StringVar(&timeoutStep, "timeout-step", "", "With --timeout-jobs or --timeout-pipelines, mark only stale running steps with this id failed.")
	cmd.Flags().StringVar(&timeoutMessage, "timeout-message", "", "Audit message to record when pipeline timeout repair marks stale work failed.")
	cmd.Flags().StringVar(&timeoutMessageFile, "timeout-message-file", "", "Read pipeline timeout repair audit message from a file, or '-' for stdin.")
	cmd.Flags().StringVar(&timeoutTarget, "timeout-target-agent", "", "With --timeout-jobs or --timeout-pipelines, mark only stale work targeting this agent.")
	cmd.Flags().StringVar(&retryStep, "retry-step", "", "With --retry-pipelines, retry only failed jobs whose next failed step has this id.")
	cmd.Flags().StringVar(&retryMessage, "retry-message", "", "Audit message to record when --retry-pipelines resets failed pipeline steps.")
	cmd.Flags().StringVar(&retryMessageFile, "retry-message-file", "", "Read pipeline retry repair audit message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&retryForce, "retry-force", false, "With --retry-pipelines, ignore step max_attempts caps for explicit pipeline repair retry.")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&wait, "wait", false, "After repair dispatches retried or ready steps, wait for those jobs to reach a lifecycle status or event.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any repaired job resolves to failed.")
	return cmd
}

func newPipelineTickCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		limit         int
		skipDrain     bool
		skipAdvance   bool
		allReadySteps bool
		dryRun        bool
		previewRoutes bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "tick <pipeline>",
		Short: "Run one pipeline's orchestration maintenance work.",
		Long:  "Run or preview one pipeline's drainable queue items and ready steps.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: --limit must be >= 0.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if wait && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if wait && skipAdvance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: --wait requires pipeline advancement; remove --skip-advance.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: wait-related flags require --wait.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineTickFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline tick: %v\n", err)
				return exitErr(2)
			}
			waitEventsSet := map[string]bool{}
			waitStatusesSet := map[job.Status]bool{}
			if wait {
				waitEventsSet = parseJobWaitEvents(waitEvents)
				waitStatusesSet, err = parseJobWaitStatuses(waitStatuses, !cmd.Flags().Changed("wait-status") && len(waitEventsSet) == 0)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline tick: %v\n", err)
					return exitErr(2)
				}
				if len(waitStatusesSet) == 0 && len(waitEventsSet) == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: pass at least one non-empty --wait-status or --wait-event.")
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := runPipelineTick(cmd, teamDir, args[0], workspace, limit, tickOptions{
				SkipDrain:     skipDrain,
				SkipAdvance:   skipAdvance,
				AllReadySteps: allReadySteps,
				Runtime:       runtimeSelection{Kind: runtimeKind, Binary: runtimeBin},
				DryRun:        dryRun,
				PreviewRoutes: previewRoutes,
			})
			if err != nil {
				var code ExitCode
				if errors.As(err, &code) {
					return err
				}
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline tick: daemon is not running — start it with `agent-team start`, or use --dry-run.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline tick: %v\n", err)
				return exitErr(1)
			}
			if wait {
				result.Tick.Advance, err = waitForPipelineAdvanceResults(cmd, teamDir, result.Tick.Advance, waitStatusesSet, waitEventsSet, waitTimeout, waitInterval, "agent-team pipeline tick")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
			}
			if err := renderPipelineTickCommandResult(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && tickResultAdvanceRowsHaveFailed(&result.Tick) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for advanced step dispatches. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip pipeline-owned queue drain work.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement work.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent pipeline step in this tick.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview pipeline-owned maintenance work without mutating state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include route and dispatch payload previews for ready pipeline steps.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After one pipeline tick, wait for advanced pipeline jobs to reach a lifecycle status or event.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any advanced pipeline job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline tick result with a Go template, e.g. '{{.Pipeline}} {{.Tick.Queue.WouldDispatch}} {{len .Tick.Advance}}'.")
	return cmd
}

func newPipelineDrainCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		limit         int
		skipDrain     bool
		skipAdvance   bool
		allReadySteps bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
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
		Use:   "drain <pipeline>",
		Short: "Run one pipeline's maintenance loop until idle.",
		Long: "Run scoped pipeline maintenance cycles until no immediate pipeline-owned queue or ready-step work remains. " +
			"Use pipeline repair for dead-letter retry, stale-work timeout, or failed-step retry.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: --interval must be >= 0.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if maxCycles <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: --max-cycles must be > 0.")
				return exitErr(2)
			}
			if wait && skipAdvance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: --wait requires pipeline advancement; remove --skip-advance.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineDrainFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline drain: %v\n", err)
				return exitErr(2)
			}
			waitEventsSet := map[string]bool{}
			waitStatusesSet := map[job.Status]bool{}
			if wait {
				waitEventsSet = parseJobWaitEvents(waitEvents)
				waitStatusesSet, err = parseJobWaitStatuses(waitStatuses, !cmd.Flags().Changed("wait-status") && len(waitEventsSet) == 0)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline drain: %v\n", err)
					return exitErr(2)
				}
				if len(waitStatusesSet) == 0 && len(waitEventsSet) == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: pass at least one non-empty --wait-status or --wait-event.")
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			result, err := runPipelineDrainUntilIdle(ctx, cmd, teamDir, args[0], workspace, limit, tickOptions{
				SkipDrain:     skipDrain,
				SkipAdvance:   skipAdvance,
				AllReadySteps: allReadySteps,
				Runtime:       runtimeSelection{Kind: runtimeKind, Binary: runtimeBin},
			}, maxCycles, interval)
			if err != nil {
				var code ExitCode
				if errors.As(err, &code) {
					return err
				}
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline drain: daemon is not running — start it with `agent-team start`, or use `agent-team pipeline advance --dry-run` to preview.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline drain: %v\n", err)
				return exitErr(1)
			}
			if wait {
				if err := waitForPipelineDrainResult(cmd, teamDir, result, waitStatusesSet, waitEventsSet, waitTimeout, waitInterval); err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
			}
			if err := renderPipelineDrainResult(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && pipelineDrainResultHasFailed(result) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for advanced step dispatches. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs per cycle, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip pipeline-owned queue drain work.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement work.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent pipeline step in each drain cycle.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After pipeline drain reaches idle, wait for jobs advanced during drain cycles to reach a lifecycle status or event.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any pipeline drain-advanced job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline drain result with a Go template, e.g. '{{.Pipeline}} {{.CyclesRun}} {{.Idle}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Delay between drain cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "Stop after this many cycles if work keeps appearing.")
	return cmd
}

func newPipelineHoldCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		limit       int
		states      []string
		message     string
		messageFile string
		holdFor     time.Duration
		untilRaw    string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "hold <pipeline>|--all [reason...]",
		Short: "Hold pipeline jobs so automation will not advance them.",
		Long: "Hold jobs in one pipeline, or all pipelines with --all, without changing their lifecycle status. " +
			"Held jobs report next-step state held until released.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline hold: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if !all && len(args) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline hold: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if len(args) > 0 && !all {
				args[0] = strings.TrimSpace(args[0])
			}
			if !all && args[0] == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline hold: pipeline name is required.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline hold: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineHoldFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline hold: %v\n", err)
				return exitErr(2)
			}
			var stateFilter map[string]bool
			stateDefault := !cmd.Flags().Changed("state")
			if !stateDefault {
				stateFilter, err = parseJobNextStateFilter(states, false)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline hold: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			holdUntil, err := parseJobHoldUntil(holdFor, cmd.Flags().Changed("for"), untilRaw, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline hold: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			reasonArgs := args
			if !all {
				pipelineName = args[0]
				reasonArgs = args[1:]
			}
			reason, err := jobActionMessageWithFile(message, messageFile, reasonArgs, "held")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline hold: %v\n", err)
				return exitErr(2)
			}
			results, err := holdPipelineJobs(teamDir, pipelineName, reason, holdUntil, stateFilter, stateDefault, limit, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline hold: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineHoldResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Hold jobs across all pipelines.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Hold at most this many matching jobs; 0 means no limit.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to hold: ready, queued, running, blocked, failed, held, done, none, or all. Defaults to active non-held, non-done jobs.")
	cmd.Flags().StringVar(&message, "message", "", "Hold reason recorded on each job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read hold reason from a file, or '-' for stdin.")
	cmd.Flags().DurationVar(&holdFor, "for", 0, "Hold for this duration, for example 30m or 2h.")
	cmd.Flags().StringVar(&untilRaw, "until", "", "Hold until this RFC3339 timestamp.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview holds without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit hold results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each hold result with a Go template, e.g. '{{.JobID}} {{.Action}}'.")
	return cmd
}

func newPipelineReleaseCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		limit       int
		message     string
		messageFile string
		expiredOnly bool
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "release <pipeline>|--all [message...]",
		Short: "Release held pipeline jobs so automation can advance them.",
		Long:  "Release held jobs in one pipeline, or all pipelines with --all, without changing their lifecycle status.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline release: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if !all && len(args) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline release: pass a pipeline name or --all.")
				return exitErr(2)
			}
			if len(args) > 0 && !all {
				args[0] = strings.TrimSpace(args[0])
			}
			if !all && args[0] == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline release: pipeline name is required.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline release: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineHoldFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline release: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			pipelineName := ""
			messageArgs := args
			if !all {
				pipelineName = args[0]
				messageArgs = args[1:]
			}
			statusMessage, err := jobActionMessageWithFile(message, messageFile, messageArgs, "released")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline release: %v\n", err)
				return exitErr(2)
			}
			results, err := releasePipelineJobs(teamDir, pipelineName, statusMessage, limit, expiredOnly, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline release: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineHoldResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Release held jobs across all pipelines.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Release at most this many held jobs; 0 means no limit.")
	cmd.Flags().StringVar(&message, "message", "", "Release message recorded on each job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read release message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&expiredOnly, "expired", false, "Only release held jobs whose hold_until has passed.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview releases without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit release results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each release result with a Go template, e.g. '{{.JobID}} {{.Action}}'.")
	return cmd
}

func newPipelineRunCmd() *cobra.Command {
	var (
		repo         string
		id           string
		ticketURL    string
		kickoff      string
		kickoffFile  string
		dispatchNow  bool
		workspace    string
		runtimeKind  string
		runtimeBin   string
		dryRun       bool
		wait         bool
		waitStatuses []string
		waitEvents   []string
		waitTimeout  time.Duration
		waitInterval time.Duration
		failOnFailed bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "run <pipeline> <ticket> [kickoff...]",
		Short: "Create a durable job from a pipeline declaration.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			return runPipelineJobCreate(cmd, teamDir, args[0], args[1], args[2:], pipelineRunOptions{
				ID:           id,
				TicketURL:    ticketURL,
				Kickoff:      kickoff,
				KickoffFile:  kickoffFile,
				DispatchNow:  dispatchNow,
				Workspace:    workspace,
				Runtime:      runtimeSelection{Kind: runtimeKind, Binary: runtimeBin},
				DryRun:       dryRun,
				Wait:         wait,
				WaitStatuses: waitStatuses,
				WaitEvents:   waitEvents,
				WaitTimeout:  waitTimeout,
				WaitInterval: waitInterval,
				FailOnFailed: failOnFailed,
				JSON:         jsonOut,
				Format:       format,
				ErrPrefix:    "agent-team pipeline run",
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Canonical ticket URL to store on the job.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the first pipeline step.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the first ready pipeline step immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the pipeline job that would be created without writing it.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After creating or dispatching, wait for the job to reach a lifecycle status or event.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the created job or advance result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the created or advanced job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

type pipelineRunOptions struct {
	ID           string
	TicketURL    string
	Kickoff      string
	KickoffFile  string
	DispatchNow  bool
	Workspace    string
	Runtime      runtimeSelection
	DryRun       bool
	Wait         bool
	WaitStatuses []string
	WaitEvents   []string
	WaitTimeout  time.Duration
	WaitInterval time.Duration
	FailOnFailed bool
	JSON         bool
	Format       string
	ErrPrefix    string
}

func runPipelineJobCreate(cmd *cobra.Command, teamDir, pipelineName, ticket string, positional []string, opts pipelineRunOptions) error {
	prefix := strings.TrimSpace(opts.ErrPrefix)
	if prefix == "" {
		prefix = "agent-team pipeline run"
	}
	if opts.Format != "" && opts.JSON {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", prefix)
		return exitErr(2)
	}
	if opts.WaitInterval < 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --wait-interval must be >= 0.\n", prefix)
		return exitErr(2)
	}
	if opts.WaitTimeout < 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --wait-timeout must be >= 0.\n", prefix)
		return exitErr(2)
	}
	if opts.DryRun && opts.Wait {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --wait cannot be combined with --dry-run.\n", prefix)
		return exitErr(2)
	}
	if !opts.Wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || opts.FailOnFailed) {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: wait-related flags require --wait.\n", prefix)
		return exitErr(2)
	}
	tmpl, err := parseJobFormat(opts.Format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return exitErr(2)
	}
	waitEvents := map[string]bool{}
	waitStatuses := map[job.Status]bool{}
	if opts.Wait {
		waitEvents = parseJobWaitEvents(opts.WaitEvents)
		waitStatuses, err = parseJobWaitStatuses(opts.WaitStatuses, !cmd.Flags().Changed("wait-status") && len(waitEvents) == 0)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
			return exitErr(2)
		}
		if len(waitStatuses) == 0 && len(waitEvents) == 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: pass at least one non-empty --wait-status or --wait-event.\n", prefix)
			return exitErr(2)
		}
	}
	pipelineDef, err := loadJobCreatePipeline(teamDir, pipelineName)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return exitErr(2)
	}
	kickoffText, err := dispatchKickoff(ticket, opts.Kickoff, opts.KickoffFile, positional)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return exitErr(2)
	}
	j, err := job.New(ticket, pipelineDef.Steps[0].Target, kickoffText, time.Now())
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return exitErr(2)
	}
	if strings.TrimSpace(opts.ID) != "" {
		normalized := job.NormalizeID(opts.ID)
		if normalized == "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: --id %q produced an empty normalized id.\n", prefix, opts.ID)
			return exitErr(2)
		}
		j.ID = normalized
	}
	if strings.TrimSpace(opts.TicketURL) != "" {
		j.TicketURL = strings.TrimSpace(opts.TicketURL)
	}
	j.Pipeline = pipelineDef.Name
	j.Steps = jobStepsFromPipeline(pipelineDef)
	j.LastEvent = "created"
	j.LastStatus = "created"
	if _, err := os.Stat(job.Path(teamDir, j.ID)); err == nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: job %q already exists.\n", prefix, j.ID)
		return exitErr(2)
	}
	if opts.DryRun {
		if opts.DispatchNow {
			preview, err := previewJobAdvanceDispatch(teamDir, j, opts.Workspace, opts.Runtime)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
				return exitErr(1)
			}
			return renderJobAdvancePreview(cmd.OutOrStdout(), preview, opts.JSON, tmpl)
		}
		return renderJobCreatePreview(cmd.OutOrStdout(), j, opts.JSON, tmpl)
	}
	data := map[string]string{
		"ticket":   j.Ticket,
		"target":   j.Target,
		"pipeline": j.Pipeline,
	}
	if j.TicketURL != "" {
		data["ticket_url"] = j.TicketURL
	}
	if err := writeJobWithAudit(teamDir, j, "created", "cli", "created "+j.Ticket, data); err != nil {
		return err
	}
	if opts.DispatchNow {
		res, err := advanceJob(cmd, teamDir, j, opts.Workspace, opts.Runtime)
		if err != nil {
			return err
		}
		if opts.Wait {
			waited, err := waitForPipelineRunJob(cmd, teamDir, res.Job.ID, waitStatuses, waitEvents, opts, prefix)
			if err != nil {
				if err == context.Canceled {
					return nil
				}
				return err
			}
			refreshJobAdvanceResultAfterWait(res, waited)
		}
		if opts.JSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
				return err
			}
			if opts.FailOnFailed && res.Job.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		}
		if tmpl != nil {
			if err := renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl); err != nil {
				return err
			}
			if opts.FailOnFailed && res.Job.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		}
		if err := renderJobAdvanceResult(cmd.OutOrStdout(), res); err != nil {
			return err
		}
		if opts.FailOnFailed && res.Job.Status == job.StatusFailed {
			return exitErr(1)
		}
		return nil
	}
	if opts.Wait {
		waited, err := waitForPipelineRunJob(cmd, teamDir, j.ID, waitStatuses, waitEvents, opts, prefix)
		if err != nil {
			if err == context.Canceled {
				return nil
			}
			return err
		}
		j = waited
	}
	if err := renderJobResult(cmd.OutOrStdout(), j, opts.JSON, tmpl); err != nil {
		return err
	}
	if opts.FailOnFailed && j.Status == job.StatusFailed {
		return exitErr(1)
	}
	return nil
}

func waitForPipelineRunJob(cmd *cobra.Command, teamDir, id string, statuses map[job.Status]bool, events map[string]bool, opts pipelineRunOptions, prefix string) (*job.Job, error) {
	return waitForJobCommand(cmd, teamDir, id, statuses, events, opts.WaitTimeout, opts.WaitInterval, prefix)
}

type pipelineInfo struct {
	Name    string             `json:"name"`
	Trigger map[string]any     `json:"trigger"`
	Steps   []pipelineStepInfo `json:"steps"`
}

type pipelineStepInfo struct {
	ID           string   `json:"id"`
	Label        string   `json:"label,omitempty"`
	Description  string   `json:"description,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Target       string   `json:"target"`
	Workspace    string   `json:"workspace,omitempty"`
	Runtime      string   `json:"runtime,omitempty"`
	RuntimeBin   string   `json:"runtime_bin,omitempty"`
	After        []string `json:"after,omitempty"`
	Gate         string   `json:"gate,omitempty"`
	Optional     bool     `json:"optional,omitempty"`
	Timeout      string   `json:"timeout,omitempty"`
	MaxAttempts  int      `json:"max_attempts,omitempty"`
}

type pipelineGraph struct {
	Name    string              `json:"name"`
	Trigger map[string]any      `json:"trigger,omitempty"`
	Summary string              `json:"summary"`
	Nodes   []pipelineGraphNode `json:"nodes"`
	Edges   []pipelineGraphEdge `json:"edges"`
}

type pipelineGraphNode struct {
	ID           string   `json:"id"`
	Label        string   `json:"label,omitempty"`
	Description  string   `json:"description,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Target       string   `json:"target,omitempty"`
	Workspace    string   `json:"workspace,omitempty"`
	Runtime      string   `json:"runtime,omitempty"`
	RuntimeBin   string   `json:"runtime_bin,omitempty"`
	After        []string `json:"after,omitempty"`
	Gate         string   `json:"gate,omitempty"`
	Optional     bool     `json:"optional,omitempty"`
	Timeout      string   `json:"timeout,omitempty"`
	MaxAttempts  int      `json:"max_attempts,omitempty"`
	Routes       []string `json:"routes,omitempty"`
	Missing      bool     `json:"missing,omitempty"`
}

type pipelineGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type pipelineStatusRow struct {
	Pipeline           string   `json:"pipeline"`
	Declared           bool     `json:"declared"`
	Steps              int      `json:"steps"`
	Jobs               int      `json:"jobs"`
	Queued             int      `json:"queued"`
	Running            int      `json:"running"`
	Blocked            int      `json:"blocked"`
	Done               int      `json:"done"`
	Failed             int      `json:"failed"`
	ReadySteps         int      `json:"ready_steps"`
	ParallelReadySteps int      `json:"parallel_ready_steps,omitempty"`
	QueuedSteps        int      `json:"queued_steps"`
	RunningSteps       int      `json:"running_steps"`
	StaleRunningSteps  int      `json:"stale_running_steps,omitempty"`
	BlockedSteps       int      `json:"blocked_steps"`
	ManualGates        int      `json:"manual_gates"`
	FailedSteps        int      `json:"failed_steps"`
	HeldSteps          int      `json:"held_steps,omitempty"`
	DoneSteps          int      `json:"done_steps"`
	NoStep             int      `json:"no_step"`
	QueuePending       int      `json:"queue_pending,omitempty"`
	QueueDead          int      `json:"queue_dead,omitempty"`
	QueueQuarantined   int      `json:"queue_quarantined,omitempty"`
	QueueRestorable    int      `json:"queue_restorable,omitempty"`
	QueueUnrestorable  int      `json:"queue_unrestorable,omitempty"`
	Actions            []string `json:"actions,omitempty"`
}

type pipelineExplainRow struct {
	Pipeline      string             `json:"pipeline"`
	Declared      bool               `json:"declared"`
	Status        pipelineStatusRow  `json:"status"`
	TotalJobs     int                `json:"total_jobs"`
	ExplainedJobs int                `json:"explained_jobs"`
	Truncated     bool               `json:"truncated,omitempty"`
	Jobs          []jobExplainResult `json:"jobs,omitempty"`
	Actions       []string           `json:"actions,omitempty"`
}

type pipelineNextAction struct {
	Pipeline string            `json:"pipeline"`
	Action   string            `json:"action"`
	Reason   string            `json:"reason,omitempty"`
	Status   pipelineStatusRow `json:"status"`
}

type pipelineAdvanceResult struct {
	JobID      string             `json:"job_id"`
	Ticket     string             `json:"ticket"`
	Pipeline   string             `json:"pipeline"`
	StepID     string             `json:"step_id,omitempty"`
	Target     string             `json:"target,omitempty"`
	StepStatus job.Status         `json:"step_status,omitempty"`
	Instance   string             `json:"instance,omitempty"`
	Action     string             `json:"action"`
	DryRun     bool               `json:"dry_run,omitempty"`
	Message    string             `json:"message,omitempty"`
	Job        *job.Job           `json:"job,omitempty"`
	Step       *job.Step          `json:"step,omitempty"`
	Event      *eventResponse     `json:"event,omitempty"`
	Preview    *jobAdvancePreview `json:"preview,omitempty"`
}

type pipelineRepairOptions struct {
	Workspace        string
	Runtime          runtimeSelection
	Limit            int
	DryRun           bool
	PreviewRoutes    bool
	SkipDaemon       bool
	SkipQueue        bool
	SkipAdvance      bool
	TimeoutJobs      bool
	TimeoutPipelines bool
	RetryPipelines   bool
	AllReadySteps    bool
	TimeoutStep      string
	TimeoutMessage   string
	TimeoutTarget    string
	RetryStep        string
	RetryMessage     string
	RetryForce       bool
	ReadyTimeout     time.Duration
}

type pipelineRepairResult struct {
	Pipeline        string                    `json:"pipeline"`
	DryRun          bool                      `json:"dry_run"`
	StatusBefore    []pipelineStatusRow       `json:"status_before,omitempty"`
	Daemon          repairStepResult          `json:"daemon"`
	Queue           repairQueueStep           `json:"queue"`
	JobTimeout      repairPipelineTimeoutStep `json:"job_timeout"`
	PipelineTimeout repairPipelineTimeoutStep `json:"pipeline_timeout"`
	PipelineRetry   repairPipelineRetryStep   `json:"pipeline_retry"`
	Advance         pipelineRepairAdvanceStep `json:"advance"`
	StatusAfter     []pipelineStatusRow       `json:"status_after,omitempty"`
}

type pipelineTickResult struct {
	Pipeline  string     `json:"pipeline"`
	CheckedAt string     `json:"checked_at"`
	Tick      tickResult `json:"tick"`
}

type pipelineDrainResult struct {
	Pipeline  string                `json:"pipeline"`
	CyclesRun int                   `json:"cycles_run"`
	Idle      bool                  `json:"idle"`
	HitLimit  bool                  `json:"hit_limit,omitempty"`
	Cycles    []*pipelineTickResult `json:"cycles"`
}

type pipelineRepairAdvanceStep struct {
	Action  string                  `json:"action"`
	Reason  string                  `json:"reason,omitempty"`
	Results []pipelineAdvanceResult `json:"results,omitempty"`
}

type pipelineApproveResult struct {
	JobID      string             `json:"job_id"`
	Ticket     string             `json:"ticket"`
	Pipeline   string             `json:"pipeline"`
	StepID     string             `json:"step_id,omitempty"`
	Target     string             `json:"target,omitempty"`
	StepStatus job.Status         `json:"step_status,omitempty"`
	Instance   string             `json:"instance,omitempty"`
	Action     string             `json:"action"`
	DryRun     bool               `json:"dry_run,omitempty"`
	Message    string             `json:"message,omitempty"`
	Job        *job.Job           `json:"job,omitempty"`
	Step       *job.Step          `json:"step,omitempty"`
	Event      *eventResponse     `json:"event,omitempty"`
	Preview    *jobAdvancePreview `json:"preview,omitempty"`
	WaitingFor []string           `json:"waiting_for,omitempty"`
}

type pipelineUnblockResult struct {
	JobID        string     `json:"job_id"`
	Ticket       string     `json:"ticket"`
	Pipeline     string     `json:"pipeline"`
	StepID       string     `json:"step_id,omitempty"`
	Target       string     `json:"target,omitempty"`
	StatusBefore job.Status `json:"status_before"`
	StatusAfter  job.Status `json:"status_after"`
	StepStatus   job.Status `json:"step_status,omitempty"`
	Instance     string     `json:"instance,omitempty"`
	Action       string     `json:"action"`
	DryRun       bool       `json:"dry_run,omitempty"`
	Message      string     `json:"message,omitempty"`
	From         string     `json:"from,omitempty"`
	MessageID    string     `json:"message_id,omitempty"`
	Delivered    bool       `json:"delivered,omitempty"`
	Job          *job.Job   `json:"job,omitempty"`
	Step         *job.Step  `json:"step,omitempty"`
}

type pipelineSkipResult struct {
	JobID      string     `json:"job_id"`
	Ticket     string     `json:"ticket"`
	Pipeline   string     `json:"pipeline"`
	StepID     string     `json:"step_id,omitempty"`
	Target     string     `json:"target,omitempty"`
	StepStatus job.Status `json:"step_status,omitempty"`
	Instance   string     `json:"instance,omitempty"`
	Action     string     `json:"action"`
	DryRun     bool       `json:"dry_run,omitempty"`
	Message    string     `json:"message,omitempty"`
	Skipped    bool       `json:"skipped,omitempty"`
	SkipReason string     `json:"skip_reason,omitempty"`
	Job        *job.Job   `json:"job,omitempty"`
	Step       *job.Step  `json:"step,omitempty"`
}

type pipelineCancelResult struct {
	JobID        string     `json:"job_id"`
	Ticket       string     `json:"ticket"`
	Pipeline     string     `json:"pipeline"`
	StatusBefore job.Status `json:"status_before"`
	StatusAfter  job.Status `json:"status_after"`
	Instance     string     `json:"instance,omitempty"`
	Action       string     `json:"action"`
	DryRun       bool       `json:"dry_run,omitempty"`
	Message      string     `json:"message,omitempty"`
	Job          *job.Job   `json:"job,omitempty"`
}

type pipelineRetryResult struct {
	JobID       string             `json:"job_id"`
	Ticket      string             `json:"ticket"`
	Pipeline    string             `json:"pipeline"`
	StepID      string             `json:"step_id,omitempty"`
	Target      string             `json:"target,omitempty"`
	StepStatus  job.Status         `json:"step_status,omitempty"`
	Instance    string             `json:"instance,omitempty"`
	Attempts    int                `json:"attempts,omitempty"`
	MaxAttempts int                `json:"max_attempts,omitempty"`
	Action      string             `json:"action"`
	DryRun      bool               `json:"dry_run,omitempty"`
	Message     string             `json:"message,omitempty"`
	Job         *job.Job           `json:"job,omitempty"`
	Step        *job.Step          `json:"step,omitempty"`
	Event       *eventResponse     `json:"event,omitempty"`
	Preview     *jobAdvancePreview `json:"preview,omitempty"`
}

type pipelineTimeoutResult struct {
	JobID      string     `json:"job_id"`
	Ticket     string     `json:"ticket"`
	Pipeline   string     `json:"pipeline"`
	StepID     string     `json:"step_id,omitempty"`
	Target     string     `json:"target,omitempty"`
	StepStatus job.Status `json:"step_status,omitempty"`
	Instance   string     `json:"instance,omitempty"`
	Action     string     `json:"action"`
	DryRun     bool       `json:"dry_run,omitempty"`
	Age        string     `json:"age,omitempty"`
	Timeout    string     `json:"timeout,omitempty"`
	Message    string     `json:"message,omitempty"`
	Job        *job.Job   `json:"job,omitempty"`
	Step       *job.Step  `json:"step,omitempty"`
}

type pipelineHoldResult struct {
	JobID      string     `json:"job_id"`
	Ticket     string     `json:"ticket"`
	Pipeline   string     `json:"pipeline"`
	Status     job.Status `json:"status"`
	NextState  string     `json:"next_state,omitempty"`
	Action     string     `json:"action"`
	Message    string     `json:"message,omitempty"`
	HeldBefore bool       `json:"held_before"`
	HeldAfter  bool       `json:"held_after"`
	HoldUntil  string     `json:"hold_until,omitempty"`
	DryRun     bool       `json:"dry_run,omitempty"`
	Job        *job.Job   `json:"job,omitempty"`
}

type pipelineDoctorResult struct {
	OK        bool                     `json:"ok"`
	Pipelines []pipelineDoctorPipeline `json:"pipelines"`
	Problems  []pipelineDoctorFinding  `json:"problems,omitempty"`
	Warnings  []pipelineDoctorFinding  `json:"warnings,omitempty"`
}

type pipelineDoctorPipeline struct {
	Name     string                  `json:"name"`
	Trigger  map[string]any          `json:"trigger,omitempty"`
	Steps    int                     `json:"steps"`
	OK       bool                    `json:"ok"`
	Problems []pipelineDoctorFinding `json:"problems,omitempty"`
	Warnings []pipelineDoctorFinding `json:"warnings,omitempty"`
}

type pipelineDoctorFinding struct {
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	Pipeline     string   `json:"pipeline,omitempty"`
	Step         string   `json:"step,omitempty"`
	Target       string   `json:"target,omitempty"`
	Runtime      string   `json:"runtime,omitempty"`
	RuntimeBin   string   `json:"runtime_bin,omitempty"`
	Routes       []string `json:"routes,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Cycle        []string `json:"cycle,omitempty"`
}

func loadPipelineInfos(teamDir string) ([]pipelineInfo, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, nil
	}
	infos := make([]pipelineInfo, 0, len(top.Pipelines))
	for _, p := range top.SortedPipelines() {
		infos = append(infos, pipelineInfoFromTopology(p))
	}
	return infos, nil
}

func loadPipelineInfo(teamDir, name string) (pipelineInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return pipelineInfo{}, fmt.Errorf("pipeline name is required")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return pipelineInfo{}, err
	}
	if top == nil || top.Pipelines[name] == nil {
		return pipelineInfo{}, fmt.Errorf("pipeline %q not found", name)
	}
	return pipelineInfoFromTopology(top.Pipelines[name]), nil
}

func collectPipelineGraph(teamDir, name string, includeRoutes bool) (pipelineGraph, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return pipelineGraph{}, fmt.Errorf("pipeline name is required")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return pipelineGraph{}, err
	}
	if top == nil || top.Pipelines[name] == nil {
		return pipelineGraph{}, fmt.Errorf("pipeline %q not found", name)
	}
	return pipelineGraphFromTopology(top, top.Pipelines[name], includeRoutes), nil
}

func pipelineGraphFromTopology(top *topology.Topology, pipeline *topology.Pipeline, includeRoutes bool) pipelineGraph {
	if pipeline == nil {
		return pipelineGraph{}
	}
	graph := pipelineGraph{
		Name:    pipeline.Name,
		Trigger: triggerAsMap(pipeline.Trigger),
		Summary: summariseTriggerMap(triggerAsMap(pipeline.Trigger)),
	}
	seen := map[string]bool{}
	for _, step := range pipeline.Steps {
		if step == nil {
			continue
		}
		id := strings.TrimSpace(step.ID)
		if id == "" {
			continue
		}
		node := pipelineGraphNode{
			ID:           id,
			Label:        strings.TrimSpace(step.Label),
			Description:  strings.TrimSpace(step.Description),
			Instructions: strings.TrimSpace(step.Instructions),
			Target:       strings.TrimSpace(step.Target),
			Workspace:    strings.TrimSpace(step.Workspace),
			Runtime:      strings.TrimSpace(step.Runtime),
			RuntimeBin:   strings.TrimSpace(step.RuntimeBin),
			After:        trimStringSlice(step.After),
			Gate:         strings.TrimSpace(step.Gate),
			Optional:     step.Optional,
			Timeout:      formatPipelineStepTimeout(step.Timeout),
			MaxAttempts:  step.MaxAttempts,
		}
		if includeRoutes && node.Target != "" {
			node.Routes = pipelineDispatchRoutes(top, node.Target)
		}
		graph.Nodes = append(graph.Nodes, node)
		seen[id] = true
		if len(node.After) == 0 {
			graph.Edges = append(graph.Edges, pipelineGraphEdge{From: "<trigger>", To: id})
			continue
		}
		for _, dep := range node.After {
			graph.Edges = append(graph.Edges, pipelineGraphEdge{From: dep, To: id})
		}
	}
	missing := map[string]bool{}
	for _, edge := range graph.Edges {
		if edge.From == "<trigger>" || seen[edge.From] || missing[edge.From] {
			continue
		}
		missing[edge.From] = true
	}
	missingIDs := make([]string, 0, len(missing))
	for id := range missing {
		missingIDs = append(missingIDs, id)
	}
	sort.Strings(missingIDs)
	for _, id := range missingIDs {
		graph.Nodes = append(graph.Nodes, pipelineGraphNode{ID: id, Target: "(missing)", Missing: true})
	}
	return graph
}

func collectPipelineDoctor(teamDir, pipelineName string) (*pipelineDoctorResult, error) {
	pipelineName = strings.TrimSpace(pipelineName)
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	result := &pipelineDoctorResult{}
	if top == nil || len(top.Pipelines) == 0 {
		if pipelineName != "" {
			return nil, fmt.Errorf("pipeline %q not found", pipelineName)
		}
		result.OK = true
		result.Warnings = append(result.Warnings, pipelineDoctorFinding{
			Code:    "no_pipelines",
			Message: "no pipelines are declared",
		})
		return result, nil
	}
	pipelines := top.SortedPipelines()
	if pipelineName != "" {
		pipeline := top.Pipelines[pipelineName]
		if pipeline == nil {
			return nil, fmt.Errorf("pipeline %q not found", pipelineName)
		}
		pipelines = []*topology.Pipeline{pipeline}
	}
	for _, pipeline := range pipelines {
		report := doctorPipeline(top, pipeline, teamDir)
		result.Pipelines = append(result.Pipelines, report)
		result.Problems = append(result.Problems, report.Problems...)
		result.Warnings = append(result.Warnings, report.Warnings...)
	}
	result.OK = len(result.Problems) == 0
	return result, nil
}

func promotePipelineDoctorRuntimeWarnings(result *pipelineDoctorResult) {
	if result == nil {
		return
	}
	result.Problems, result.Warnings = promotePipelineRuntimeFindings(result.Problems, result.Warnings)
	for i := range result.Pipelines {
		pipeline := &result.Pipelines[i]
		pipeline.Problems, pipeline.Warnings = promotePipelineRuntimeFindings(pipeline.Problems, pipeline.Warnings)
		pipeline.OK = len(pipeline.Problems) == 0
	}
	result.OK = len(result.Problems) == 0
}

func promotePipelineRuntimeFindings(problems, warnings []pipelineDoctorFinding) ([]pipelineDoctorFinding, []pipelineDoctorFinding) {
	if len(warnings) == 0 {
		return problems, warnings
	}
	nextProblems := append([]pipelineDoctorFinding(nil), problems...)
	nextWarnings := make([]pipelineDoctorFinding, 0, len(warnings))
	for _, warning := range warnings {
		if pipelineRuntimeFindingIsStrict(warning) {
			nextProblems = append(nextProblems, warning)
			continue
		}
		nextWarnings = append(nextWarnings, warning)
	}
	return nextProblems, nextWarnings
}

func pipelineRuntimeFindingIsStrict(finding pipelineDoctorFinding) bool {
	switch strings.TrimSpace(finding.Code) {
	case "step_runtime_invalid", "step_runtime_unavailable":
		return true
	default:
		return false
	}
}

func doctorPipeline(top *topology.Topology, pipeline *topology.Pipeline, teamDir string) pipelineDoctorPipeline {
	report := pipelineDoctorPipeline{}
	if pipeline == nil {
		report.Problems = append(report.Problems, pipelineDoctorFinding{
			Code:    "pipeline_nil",
			Message: "pipeline declaration is empty",
		})
		return report
	}
	report.Name = pipeline.Name
	report.Trigger = triggerAsMap(pipeline.Trigger)
	report.Steps = len(pipeline.Steps)
	if len(pipeline.Steps) == 0 {
		report.Problems = append(report.Problems, pipelineDoctorFinding{
			Code:     "pipeline_no_steps",
			Message:  fmt.Sprintf("pipeline %q has no steps", pipeline.Name),
			Pipeline: pipeline.Name,
		})
		report.OK = false
		return report
	}
	report.Problems = append(report.Problems, pipelineCycleFindings(pipeline)...)
	routeProblems, routeWarnings := pipelineRouteFindings(top, pipeline)
	report.Problems = append(report.Problems, routeProblems...)
	report.Warnings = append(report.Warnings, routeWarnings...)
	report.Warnings = append(report.Warnings, pipelineScheduleWarnings(top, pipeline)...)
	report.Warnings = append(report.Warnings, pipelineOrderingWarnings(pipeline)...)
	report.Warnings = append(report.Warnings, pipelineRuntimeWarnings(teamDir, pipeline)...)
	report.OK = len(report.Problems) == 0
	return report
}

func pipelineRuntimeWarnings(teamDir string, pipeline *topology.Pipeline) []pipelineDoctorFinding {
	if pipeline == nil {
		return nil
	}
	configPath := filepath.Join(teamDir, "config.toml")
	var warnings []pipelineDoctorFinding
	for _, step := range pipeline.Steps {
		if step == nil {
			continue
		}
		selection := runtimeSelection{Kind: strings.TrimSpace(step.Runtime), Binary: strings.TrimSpace(step.RuntimeBin)}
		if selection.Kind == "" && selection.Binary == "" {
			continue
		}
		info, err := collectRuntimeInfoForConfigWithSelection(configPath, selection)
		if err != nil {
			warnings = append(warnings, pipelineDoctorFinding{
				Code:       "step_runtime_invalid",
				Message:    fmt.Sprintf("pipeline %q step %q runtime default could not be resolved: %v", pipeline.Name, step.ID, err),
				Pipeline:   pipeline.Name,
				Step:       step.ID,
				Target:     step.Target,
				Runtime:    selection.Kind,
				RuntimeBin: selection.Binary,
			})
			continue
		}
		if !info.Available {
			warnings = append(warnings, pipelineDoctorFinding{
				Code:       "step_runtime_unavailable",
				Message:    fmt.Sprintf("pipeline %q step %q defaults to runtime %q with binary %q, but that binary was not found in PATH", pipeline.Name, step.ID, info.Runtime, info.Binary),
				Pipeline:   pipeline.Name,
				Step:       step.ID,
				Target:     step.Target,
				Runtime:    info.Runtime,
				RuntimeBin: info.Binary,
			})
		}
	}
	return warnings
}

func pipelineRouteFindings(top *topology.Topology, pipeline *topology.Pipeline) ([]pipelineDoctorFinding, []pipelineDoctorFinding) {
	if top == nil || pipeline == nil {
		return nil, nil
	}
	var problems []pipelineDoctorFinding
	var warnings []pipelineDoctorFinding
	for _, step := range pipeline.Steps {
		if step == nil {
			continue
		}
		target := strings.TrimSpace(step.Target)
		if target == "" {
			continue
		}
		routes := pipelineDispatchRoutes(top, target)
		switch len(routes) {
		case 0:
			problems = append(problems, pipelineDoctorFinding{
				Code:     "target_has_no_dispatch_route",
				Message:  fmt.Sprintf("pipeline %q step %q targets %q, but no agent.dispatch route currently matches that target", pipeline.Name, step.ID, target),
				Pipeline: pipeline.Name,
				Step:     step.ID,
				Target:   target,
			})
		case 1:
		default:
			warnings = append(warnings, pipelineDoctorFinding{
				Code:     "target_matches_multiple_routes",
				Message:  fmt.Sprintf("pipeline %q step %q targets %q, which matches multiple agent.dispatch routes: %s", pipeline.Name, step.ID, target, strings.Join(routes, ",")),
				Pipeline: pipeline.Name,
				Step:     step.ID,
				Target:   target,
				Routes:   routes,
			})
		}
	}
	return problems, warnings
}

func pipelineDispatchRoutes(top *topology.Topology, target string) []string {
	if top == nil {
		return nil
	}
	payload := map[string]any{"target": target}
	matches := top.Resolve(topology.EventAgentDispatch, payload)
	routes := make([]string, 0, len(matches))
	for _, inst := range matches {
		if inst == nil {
			continue
		}
		routes = append(routes, inst.Name)
	}
	sort.Strings(routes)
	return routes
}

func pipelineScheduleWarnings(top *topology.Topology, pipeline *topology.Pipeline) []pipelineDoctorFinding {
	if top == nil || pipeline == nil || pipeline.Trigger == nil || pipeline.Trigger.Event != topology.EventSchedule {
		return nil
	}
	var matched []string
	for _, schedule := range top.SortedSchedules() {
		if schedule == nil {
			continue
		}
		if pipeline.Trigger.Matches(schedule.EventPayload()) {
			matched = append(matched, schedule.Name)
		}
	}
	if len(matched) > 0 {
		return nil
	}
	return []pipelineDoctorFinding{{
		Code:     "schedule_trigger_has_no_source",
		Message:  fmt.Sprintf("pipeline %q is triggered by schedule events, but no declared schedule payload matches it", pipeline.Name),
		Pipeline: pipeline.Name,
	}}
}

func pipelineOrderingWarnings(pipeline *topology.Pipeline) []pipelineDoctorFinding {
	if pipeline == nil || len(pipeline.Steps) == 0 || len(pipeline.Steps[0].After) == 0 {
		return nil
	}
	step := pipeline.Steps[0]
	return []pipelineDoctorFinding{{
		Code:         "first_step_has_dependencies",
		Message:      fmt.Sprintf("pipeline %q first step %q waits for %s; the stored job target will still default to that first step", pipeline.Name, step.ID, strings.Join(step.After, ",")),
		Pipeline:     pipeline.Name,
		Step:         step.ID,
		Target:       step.Target,
		Dependencies: append([]string(nil), step.After...),
	}}
}

func pipelineCycleFindings(pipeline *topology.Pipeline) []pipelineDoctorFinding {
	if pipeline == nil {
		return nil
	}
	if cycle := pipelineDependencyCycle(pipeline); len(cycle) > 0 {
		return []pipelineDoctorFinding{{
			Code:     "dependency_cycle",
			Message:  fmt.Sprintf("pipeline %q has a dependency cycle: %s", pipeline.Name, strings.Join(cycle, " -> ")),
			Pipeline: pipeline.Name,
			Cycle:    cycle,
		}}
	}
	return nil
}

func pipelineDependencyCycle(pipeline *topology.Pipeline) []string {
	deps := map[string][]string{}
	ordered := make([]string, 0, len(pipeline.Steps))
	for _, step := range pipeline.Steps {
		if step == nil {
			continue
		}
		id := strings.TrimSpace(step.ID)
		if id == "" {
			continue
		}
		ordered = append(ordered, id)
		deps[id] = append([]string(nil), step.After...)
	}
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := map[string]int{}
	stack := []string{}
	var visit func(string) []string
	visit = func(id string) []string {
		state[id] = visiting
		stack = append(stack, id)
		for _, dep := range deps[id] {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			switch state[dep] {
			case unvisited:
				if cycle := visit(dep); len(cycle) > 0 {
					return cycle
				}
			case visiting:
				for i, existing := range stack {
					if existing == dep {
						cycle := append([]string(nil), stack[i:]...)
						cycle = append(cycle, dep)
						return cycle
					}
				}
				return []string{dep, dep}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = visited
		return nil
	}
	for _, id := range ordered {
		if state[id] != unvisited {
			continue
		}
		if cycle := visit(id); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

func trimStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func pipelineInfoFromTopology(p *topology.Pipeline) pipelineInfo {
	steps := make([]pipelineStepInfo, 0, len(p.Steps))
	for _, step := range p.Steps {
		steps = append(steps, pipelineStepInfo{
			ID:           step.ID,
			Label:        step.Label,
			Description:  step.Description,
			Instructions: step.Instructions,
			Target:       step.Target,
			Workspace:    step.Workspace,
			Runtime:      step.Runtime,
			RuntimeBin:   step.RuntimeBin,
			After:        append([]string(nil), step.After...),
			Gate:         step.Gate,
			Optional:     step.Optional,
			Timeout:      formatPipelineStepTimeout(step.Timeout),
			MaxAttempts:  step.MaxAttempts,
		})
	}
	return pipelineInfo{
		Name:    p.Name,
		Trigger: triggerAsMap(p.Trigger),
		Steps:   steps,
	}
}

func collectPipelineStatusRows(teamDir, pipeline string) ([]pipelineStatusRow, error) {
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return nil, err
	}
	return collectPipelineStatusRowsAt(teamDir, pipeline, time.Now().UTC(), staleAfter)
}

func collectPipelineStatusRowsAt(teamDir, pipeline string, now time.Time, staleAfter time.Duration) ([]pipelineStatusRow, error) {
	pipeline = strings.TrimSpace(pipeline)
	infos, err := loadPipelineInfos(teamDir)
	if err != nil {
		return nil, err
	}
	rows := map[string]*pipelineStatusRow{}
	declaredOrder := []string{}
	declared := map[string]bool{}
	rowFor := func(name string) *pipelineStatusRow {
		if row := rows[name]; row != nil {
			return row
		}
		row := &pipelineStatusRow{Pipeline: name}
		rows[name] = row
		return row
	}
	for _, info := range infos {
		if pipeline != "" && info.Name != pipeline {
			continue
		}
		row := rowFor(info.Name)
		row.Declared = true
		row.Steps = len(info.Steps)
		declared[info.Name] = true
		declaredOrder = append(declaredOrder, info.Name)
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	jobsByPipeline := map[string][]*job.Job{}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		name := strings.TrimSpace(j.Pipeline)
		if name == "" {
			continue
		}
		if pipeline != "" && name != pipeline {
			continue
		}
		jobsByPipeline[name] = append(jobsByPipeline[name], j)
		applyPipelineStatusJob(rowFor(name), j, now, staleAfter)
	}
	applyPipelineStatusQueueRows(teamDir, rows, jobsByPipeline, now)
	if pipeline != "" {
		row := rows[pipeline]
		if row == nil {
			return nil, fmt.Errorf("pipeline %q not found", pipeline)
		}
		finalizePipelineStatusRow(row)
		return []pipelineStatusRow{*row}, nil
	}
	extras := make([]string, 0, len(rows))
	for name := range rows {
		if !declared[name] {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	out := make([]pipelineStatusRow, 0, len(rows))
	for _, name := range declaredOrder {
		if row := rows[name]; row != nil {
			finalizePipelineStatusRow(row)
			out = append(out, *row)
		}
	}
	for _, name := range extras {
		row := rows[name]
		finalizePipelineStatusRow(row)
		out = append(out, *row)
	}
	return out, nil
}

func parsePipelineStatusSort(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "declared", nil
	}
	switch value {
	case "declared", "pipeline", "steps", "jobs", "queued", "running", "blocked", "done", "failed", "ready", "queued-steps", "running-steps", "stale", "stale-running", "blocked-steps", "manual", "manual-gates", "failed-steps", "held", "done-steps", "none", "no-step", "queue", "queue-pending", "queue-dead", "quarantine", "quarantined":
		return value, nil
	default:
		return "", fmt.Errorf("--sort must be declared, pipeline, steps, jobs, queued, running, blocked, done, failed, ready, queued-steps, running-steps, stale, blocked-steps, manual, failed-steps, held, done-steps, none, queue, queue-pending, queue-dead, or quarantined")
	}
}

func applyPipelineStatusRowOptions(rows []pipelineStatusRow, sortMode string, limit int) []pipelineStatusRow {
	sortPipelineStatusRows(rows, sortMode)
	if limit <= 0 || limit >= len(rows) {
		return rows
	}
	return rows[:limit]
}

func sortPipelineStatusRows(rows []pipelineStatusRow, sortMode string) {
	switch sortMode {
	case "", "declared":
		return
	case "pipeline":
		sort.SliceStable(rows, func(i, j int) bool {
			return rows[i].Pipeline < rows[j].Pipeline
		})
	default:
		sort.SliceStable(rows, func(i, j int) bool {
			left := pipelineStatusSortValue(rows[i], sortMode)
			right := pipelineStatusSortValue(rows[j], sortMode)
			if left == right {
				return false
			}
			return left > right
		})
	}
}

func pipelineStatusSortValue(row pipelineStatusRow, sortMode string) int {
	switch sortMode {
	case "steps":
		return row.Steps
	case "jobs":
		return row.Jobs
	case "queued":
		return row.Queued
	case "running":
		return row.Running
	case "blocked":
		return row.Blocked
	case "done":
		return row.Done
	case "failed":
		return row.Failed
	case "ready":
		return row.ReadySteps
	case "queued-steps":
		return row.QueuedSteps
	case "running-steps":
		return row.RunningSteps
	case "stale", "stale-running":
		return row.StaleRunningSteps
	case "blocked-steps":
		return row.BlockedSteps
	case "manual", "manual-gates":
		return row.ManualGates
	case "failed-steps":
		return row.FailedSteps
	case "held":
		return row.HeldSteps
	case "done-steps":
		return row.DoneSteps
	case "none", "no-step":
		return row.NoStep
	case "queue":
		return row.QueuePending + row.QueueDead + row.QueueQuarantined
	case "queue-pending":
		return row.QueuePending
	case "queue-dead":
		return row.QueueDead
	case "quarantine", "quarantined":
		return row.QueueQuarantined
	default:
		return 0
	}
}

func collectPipelineExplainRows(teamDir, pipeline string, limit int, stateFilter map[string]bool, stepFilter string) ([]pipelineExplainRow, error) {
	statusRows, err := collectPipelineStatusRows(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	sortJobs(jobs, "updated")
	out := make([]pipelineExplainRow, 0, len(statusRows))
	for _, status := range statusRows {
		row := pipelineExplainRow{
			Pipeline: status.Pipeline,
			Declared: status.Declared,
			Status:   status,
			Actions:  append([]string(nil), status.Actions...),
		}
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.Pipeline) != status.Pipeline {
				continue
			}
			row.TotalJobs++
			explained := scopePipelineExplainResultActions(status.Pipeline, explainJobPipeline(j))
			var ok bool
			explained, ok = filterJobExplainResultByStep(explained, stepFilter)
			if !ok {
				continue
			}
			if len(stateFilter) > 0 && !stateFilter[explained.State] {
				continue
			}
			if limit > 0 && row.ExplainedJobs >= limit {
				row.Truncated = true
				continue
			}
			row.Jobs = append(row.Jobs, explained)
			row.ExplainedJobs++
		}
		out = append(out, row)
	}
	return out, nil
}

func scopePipelineExplainResultActions(pipeline string, explained jobExplainResult) jobExplainResult {
	pipeline = strings.TrimSpace(pipeline)
	jobID := strings.TrimSpace(explained.JobID)
	if pipeline == "" || jobID == "" {
		return explained
	}
	explained.Actions = scopePipelineExplainActionList(pipeline, jobID, explained.Next.StepID, explained.Actions)
	explained.Next.Actions = scopePipelineExplainActionList(pipeline, jobID, explained.Next.StepID, explained.Next.Actions)
	for idx := range explained.Steps {
		explained.Steps[idx].Actions = scopePipelineExplainActionList(pipeline, jobID, explained.Steps[idx].ID, explained.Steps[idx].Actions)
	}
	return explained
}

func scopePipelineExplainActionList(pipeline, jobID, stepID string, actions []string) []string {
	if len(actions) == 0 {
		return actions
	}
	out := make([]string, len(actions))
	for idx, action := range actions {
		out[idx] = scopePipelineExplainAction(pipeline, jobID, stepID, action)
	}
	return out
}

func scopePipelineExplainAction(pipeline, jobID, stepID, action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return action
	}
	if action == fmt.Sprintf("agent-team job advance %s", jobID) {
		return pipelineTickPreviewAction(pipeline, false)
	}
	if action == fmt.Sprintf("agent-team job advance %s --all-ready-steps", jobID) {
		return pipelineTickPreviewAction(pipeline, true)
	}
	if action == fmt.Sprintf("agent-team pipeline advance %s --dry-run --preview-routes", pipeline) {
		return pipelineTickPreviewAction(pipeline, false)
	}
	if action == fmt.Sprintf("agent-team pipeline advance %s --all-ready-steps --dry-run --preview-routes", pipeline) {
		return pipelineTickPreviewAction(pipeline, true)
	}
	if action == "agent-team tick" {
		return pipelineTickPreviewAction(pipeline, false)
	}
	if action == fmt.Sprintf("agent-team job retry %s --dispatch", jobID) ||
		action == fmt.Sprintf("agent-team job retry %s --dry-run --dispatch", jobID) {
		scoped := fmt.Sprintf("agent-team pipeline retry %s", pipeline)
		if stepID = strings.TrimSpace(stepID); stepID != "" {
			scoped = fmt.Sprintf("%s --step %s", scoped, stepID)
		}
		return scoped + " --dry-run --dispatch --preview-routes"
	}
	if action == fmt.Sprintf("agent-team job release %s", jobID) {
		return fmt.Sprintf("agent-team pipeline release %s --dry-run", pipeline)
	}
	if action == jobUnblockAction(jobID, stepID) {
		scoped := fmt.Sprintf("agent-team pipeline unblock %s", pipeline)
		if stepID = strings.TrimSpace(stepID); stepID != "" {
			scoped = fmt.Sprintf("%s --step %s", scoped, stepID)
		}
		return scoped + " <answer...> --dry-run"
	}
	stepID = strings.TrimSpace(stepID)
	if stepID != "" && action == fmt.Sprintf("agent-team job approve %s --step %s", jobID, stepID) {
		return fmt.Sprintf("agent-team pipeline approve %s --step %s --dry-run --dispatch --preview-routes", pipeline, stepID)
	}
	if stepID != "" && action == fmt.Sprintf("agent-team job reject %s --step %s", jobID, stepID) {
		return fmt.Sprintf("agent-team pipeline reject %s --step %s --dry-run", pipeline, stepID)
	}
	return action
}

func pipelineTickPreviewAction(pipeline string, allReadySteps bool) string {
	if allReadySteps {
		return fmt.Sprintf("agent-team pipeline tick %s --all-ready-steps --dry-run --preview-routes", pipeline)
	}
	return fmt.Sprintf("agent-team pipeline tick %s --dry-run --preview-routes", pipeline)
}

func filterJobExplainResultByStep(explained jobExplainResult, stepFilter string) (jobExplainResult, bool) {
	stepFilter = strings.TrimSpace(stepFilter)
	if stepFilter == "" {
		return explained, true
	}
	steps := make([]jobExplainStep, 0, len(explained.Steps))
	for _, step := range explained.Steps {
		if step.ID == stepFilter {
			steps = append(steps, step)
		}
	}
	if len(steps) == 0 {
		return explained, false
	}
	explained.Steps = steps
	return explained, true
}

func applyPipelineStatusJob(row *pipelineStatusRow, j *job.Job, now time.Time, staleAfter time.Duration) {
	if row == nil || j == nil {
		return
	}
	row.Jobs++
	switch j.Status {
	case job.StatusQueued:
		row.Queued++
	case job.StatusRunning:
		row.Running++
	case job.StatusBlocked:
		row.Blocked++
	case job.StatusDone:
		row.Done++
	case job.StatusFailed:
		row.Failed++
	}
	if steps := advanceableJobSteps(j); len(steps) > 1 {
		row.ParallelReadySteps += len(steps)
	}
	next := inspectNextJobStep(j)
	switch next.State {
	case "ready":
		row.ReadySteps++
	case "queued":
		if jobNextResultIsAdvanceable(next) {
			row.ReadySteps++
		}
		row.QueuedSteps++
	case "running":
		row.RunningSteps++
		if next.Step != nil && pipelineRunningStepIsStale(next.Step, now, staleAfter) {
			row.StaleRunningSteps++
		}
	case "blocked":
		row.BlockedSteps++
		if next.Step != nil && next.Step.Gate == job.StepGateManual && len(next.WaitingFor) == 0 {
			row.ManualGates++
		}
	case "failed":
		row.FailedSteps++
	case "held":
		row.HeldSteps++
	case "done":
		row.DoneSteps++
	case "none":
		row.NoStep++
	}
}

func finalizePipelineStatusRow(row *pipelineStatusRow) {
	if row == nil {
		return
	}
	actions := []string{}
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" || stringSliceContains(actions, action) {
			return
		}
		actions = append(actions, action)
	}
	if row.ReadySteps > 0 {
		add(pipelineTickPreviewAction(row.Pipeline, false))
	}
	if row.ParallelReadySteps > 1 {
		add(pipelineTickPreviewAction(row.Pipeline, true))
	}
	if row.FailedSteps > 0 {
		add(fmt.Sprintf("agent-team pipeline retry %s --dry-run --dispatch --preview-routes", row.Pipeline))
		add(fmt.Sprintf("agent-team pipeline repair %s --retry-pipelines --dry-run --preview-routes", row.Pipeline))
		add("agent-team repair --retry-pipelines --dry-run --preview-routes")
		add(fmt.Sprintf("agent-team pipeline explain %s --state failed", row.Pipeline))
		add(fmt.Sprintf("agent-team pipeline ready %s --state failed", row.Pipeline))
	}
	if row.StaleRunningSteps > 0 {
		add("agent-team job reconcile events --dry-run")
		add(fmt.Sprintf("agent-team pipeline timeout %s --dry-run", row.Pipeline))
		add(fmt.Sprintf("agent-team pipeline repair %s --timeout-jobs --dry-run --preview-routes", row.Pipeline))
		add("agent-team repair --timeout-jobs --dry-run")
		add(fmt.Sprintf("agent-team pipeline explain %s --state running", row.Pipeline))
		add(fmt.Sprintf("agent-team pipeline ready %s --state running", row.Pipeline))
	}
	if row.HeldSteps > 0 {
		add(fmt.Sprintf("agent-team pipeline explain %s --state held", row.Pipeline))
		add(fmt.Sprintf("agent-team pipeline ready %s --state held", row.Pipeline))
	}
	if row.ManualGates > 0 {
		add(fmt.Sprintf("agent-team pipeline approve %s --dry-run --dispatch --preview-routes", row.Pipeline))
	}
	if row.BlockedSteps > 0 {
		if row.BlockedSteps > row.ManualGates {
			add(fmt.Sprintf("agent-team pipeline unblock %s <answer...> --dry-run", row.Pipeline))
		}
		add(fmt.Sprintf("agent-team pipeline explain %s --state blocked", row.Pipeline))
		add(fmt.Sprintf("agent-team pipeline ready %s --state blocked", row.Pipeline))
	}
	if row.QueuedSteps > 0 {
		add(pipelineTickPreviewAction(row.Pipeline, false))
	}
	if row.QueueDead > 0 {
		add(fmt.Sprintf("agent-team pipeline queue %s --state dead --summary", row.Pipeline))
		add(pipelineQueueRetryAllRecoveryAction(row.Pipeline, true))
	}
	if row.QueueQuarantined > 0 {
		add(fmt.Sprintf("agent-team pipeline queue quarantine %s", row.Pipeline))
		if row.QueueUnrestorable > 0 {
			add(fmt.Sprintf("agent-team pipeline queue quarantine %s --unrestorable", row.Pipeline))
		}
		if row.QueueRestorable > 0 {
			add(fmt.Sprintf("agent-team pipeline queue quarantine %s --restorable", row.Pipeline))
		}
		add(fmt.Sprintf("agent-team pipeline snapshot %s --json", row.Pipeline))
	}
	if row.QueuePending > 0 {
		add(pipelineTickPreviewAction(row.Pipeline, false))
		add(fmt.Sprintf("agent-team pipeline queue %s --state pending", row.Pipeline))
	}
	row.Actions = actions
}

func applyPipelineStatusQueueRows(teamDir string, rows map[string]*pipelineStatusRow, jobsByPipeline map[string][]*job.Job, now time.Time) {
	if len(rows) == 0 {
		return
	}
	if items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir)); err == nil {
		for pipeline, row := range rows {
			if row == nil {
				continue
			}
			summary := summarizeQueueItems(queueItemsForJobs(items, jobsByPipeline[pipeline]), now)
			row.QueuePending = summary.Pending
			row.QueueDead = summary.Dead
		}
	}
	quarantine, err := listQueueQuarantine(teamDir)
	if err != nil {
		return
	}
	for pipeline, row := range rows {
		if row == nil {
			continue
		}
		var summary queueSummary
		applyQueueQuarantineSummary(&summary, queueQuarantineItemsForJobs(quarantine, jobsByPipeline[pipeline]))
		row.QueueQuarantined = summary.Quarantined
		row.QueueRestorable = summary.QuarantineRestorable
		row.QueueUnrestorable = summary.QuarantineUnrestorable
	}
}

func pipelineNextActionsFromStatus(rows []pipelineStatusRow, limit int, reasonFilters []string) []pipelineNextAction {
	actions := []pipelineNextAction{}
	for _, row := range rows {
		for _, action := range row.Actions {
			action = strings.TrimSpace(action)
			if action == "" {
				continue
			}
			next := pipelineNextAction{
				Pipeline: row.Pipeline,
				Action:   action,
				Reason:   pipelineNextActionReason(row, action),
				Status:   row,
			}
			if !pipelineNextActionMatchesReason(next, reasonFilters) {
				continue
			}
			actions = append(actions, next)
			if limit > 0 && len(actions) >= limit {
				return actions
			}
		}
	}
	return actions
}

func parsePipelineNextReasonFilters(raw []string) ([]string, error) {
	filters := []string{}
	for _, value := range splitFilterValues(raw) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			filters = append(filters, value)
		}
	}
	if len(raw) > 0 && len(filters) == 0 {
		return nil, fmt.Errorf("--reason requires at least one non-empty value")
	}
	return filters, nil
}

func pipelineNextActionMatchesReason(action pipelineNextAction, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	reason := strings.ToLower(strings.TrimSpace(action.Reason))
	for _, filter := range filters {
		if reason == filter || strings.HasPrefix(reason, filter+"=") {
			return true
		}
	}
	return false
}

func filterPipelineNextRowsForPipeline(rows []pipelineStatusRow, pipeline, teamName string) ([]pipelineStatusRow, error) {
	pipeline = strings.TrimSpace(pipeline)
	if pipeline == "" {
		return rows, nil
	}
	out := make([]pipelineStatusRow, 0, 1)
	for _, row := range rows {
		if row.Pipeline == pipeline {
			out = append(out, row)
		}
	}
	if len(out) == 0 {
		if strings.TrimSpace(teamName) != "" {
			return nil, fmt.Errorf("pipeline %q not found for team %q", pipeline, teamName)
		}
		return nil, fmt.Errorf("pipeline %q not found", pipeline)
	}
	return out, nil
}

func pipelineNextActionReason(row pipelineStatusRow, action string) string {
	switch {
	case strings.Contains(action, " --all-ready-steps"):
		return fmt.Sprintf("parallel_ready_steps=%d", row.ParallelReadySteps)
	case strings.Contains(action, " tick "):
		if row.ReadySteps > 0 {
			return fmt.Sprintf("ready_steps=%d", row.ReadySteps)
		}
		if row.QueuePending > 0 {
			return fmt.Sprintf("queue_pending=%d", row.QueuePending)
		}
		return fmt.Sprintf("queued_steps=%d", row.QueuedSteps)
	case strings.Contains(action, " advance "):
		return fmt.Sprintf("ready_steps=%d", row.ReadySteps)
	case strings.Contains(action, " queue quarantine "):
		return fmt.Sprintf("queue_quarantined=%d", row.QueueQuarantined)
	case strings.Contains(action, " queue retry ") ||
		strings.Contains(action, " --state dead"):
		return fmt.Sprintf("queue_dead=%d", row.QueueDead)
	case strings.Contains(action, " --state pending"):
		return fmt.Sprintf("queue_pending=%d", row.QueuePending)
	case strings.Contains(action, " snapshot ") && row.QueueQuarantined > 0:
		return fmt.Sprintf("queue_quarantined=%d", row.QueueQuarantined)
	case strings.Contains(action, " retry "),
		strings.Contains(action, " --retry-pipelines "),
		strings.Contains(action, " --state failed"):
		return fmt.Sprintf("failed_steps=%d", row.FailedSteps)
	case strings.Contains(action, " approve "):
		return fmt.Sprintf("manual_gates=%d", row.ManualGates)
	case strings.Contains(action, " --state blocked"):
		return fmt.Sprintf("blocked_steps=%d", row.BlockedSteps)
	case strings.Contains(action, " reconcile events "),
		strings.Contains(action, " timeout "),
		strings.Contains(action, " --timeout-jobs "),
		strings.Contains(action, " --state running"):
		return fmt.Sprintf("stale_running_steps=%d", row.StaleRunningSteps)
	case strings.Contains(action, " --state held"):
		return fmt.Sprintf("held_steps=%d", row.HeldSteps)
	case action == "agent-team tick":
		return fmt.Sprintf("queued_steps=%d", row.QueuedSteps)
	default:
		return ""
	}
}

func pipelineRunningStepIsStale(step *job.Step, now time.Time, staleAfter time.Duration) bool {
	if step == nil || step.StartedAt.IsZero() {
		return false
	}
	staleAfter = pipelineStepStaleAfter(step, staleAfter)
	if staleAfter <= 0 {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.Sub(step.StartedAt) > staleAfter
}

func pipelineStepStaleAfter(step *job.Step, fallback time.Duration) time.Duration {
	if step == nil {
		return fallback
	}
	timeout := strings.TrimSpace(step.Timeout)
	if timeout == "" {
		return fallback
	}
	duration, err := time.ParseDuration(timeout)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

func holdPipelineJobs(teamDir, pipeline, reason string, holdUntil time.Time, stateFilter map[string]bool, stateDefault bool, limit int, dryRun bool) ([]pipelineHoldResult, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "held"
	}
	results := make([]pipelineHoldResult, 0, len(jobs))
	now := time.Now().UTC()
	for _, j := range jobs {
		if limit > 0 && len(results) >= limit {
			break
		}
		next := inspectNextJobStep(j)
		if !holdStateSelected(next.State, stateFilter, stateDefault) {
			continue
		}
		result := pipelineHoldResult{
			JobID:      j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			Status:     j.Status,
			NextState:  next.State,
			Action:     "would_hold",
			Message:    reason,
			HeldBefore: j.Held,
			HeldAfter:  true,
			HoldUntil:  pipelineHoldUntilText(holdUntil),
			DryRun:     dryRun,
		}
		if j.Held {
			result.Action = "skipped"
			result.Message = "already held"
			result.HoldUntil = jobHoldUntilText(j)
			result.Job = j
			results = append(results, result)
			continue
		}
		j.Held = true
		j.HoldReason = reason
		j.HoldUntil = holdUntil
		j.LastEvent = "held"
		j.LastStatus = reason
		j.UpdatedAt = now
		result.Job = j
		if dryRun {
			results = append(results, result)
			continue
		}
		changes := map[string]string{"held": "true", "pipeline": j.Pipeline}
		if !j.HoldUntil.IsZero() {
			changes["hold_until"] = jobHoldUntilText(j)
		}
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", changes); err != nil {
			return nil, err
		}
		result.Action = "held"
		result.DryRun = false
		results = append(results, result)
	}
	return results, nil
}

func releasePipelineJobs(teamDir, pipeline, message string, limit int, expiredOnly bool, dryRun bool) ([]pipelineHoldResult, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "released"
	}
	results := make([]pipelineHoldResult, 0, len(jobs))
	now := time.Now().UTC()
	for _, j := range jobs {
		if limit > 0 && len(results) >= limit {
			break
		}
		if !j.Held {
			continue
		}
		if expiredOnly && !jobHoldExpired(j, now) {
			continue
		}
		next := inspectNextJobStep(j)
		result := pipelineHoldResult{
			JobID:      j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			Status:     j.Status,
			NextState:  next.State,
			Action:     "would_release",
			Message:    message,
			HeldBefore: true,
			HeldAfter:  false,
			HoldUntil:  jobHoldUntilText(j),
			DryRun:     dryRun,
		}
		j.Held = false
		j.HoldReason = ""
		j.HoldUntil = time.Time{}
		j.LastEvent = "released"
		j.LastStatus = message
		j.UpdatedAt = now
		result.Job = j
		if dryRun {
			results = append(results, result)
			continue
		}
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"held": "false", "hold_until": "", "pipeline": j.Pipeline}); err != nil {
			return nil, err
		}
		result.Action = "released"
		result.DryRun = false
		results = append(results, result)
	}
	return results, nil
}

func selectedPipelineJobs(teamDir, pipeline string) ([]*job.Job, error) {
	pipeline = strings.TrimSpace(pipeline)
	if pipeline != "" {
		if _, err := loadPipelineInfo(teamDir, pipeline); err != nil {
			return nil, err
		}
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	sortJobs(jobs, "updated")
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j == nil || strings.TrimSpace(j.Pipeline) == "" {
			continue
		}
		if pipeline != "" && j.Pipeline != pipeline {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

func collectPipelineTriage(teamDir, pipeline string, now time.Time, staleAfter time.Duration, filters jobTriageFilters) (jobTriageSnapshot, error) {
	pipeline = strings.TrimSpace(pipeline)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	ownedIDs := jobIDSet(jobs)
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	pipelineQueue := queueItemsForJobs(queueItems, jobs)
	quarantineItems, err := listQueueQuarantine(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	pipelineQuarantine := queueQuarantineItemsForJobs(quarantineItems, jobs)
	snapshot, err := collectJobTriage(teamDir, now, staleAfter)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	snapshot.Summary = summarizeJobsWithRuntime(teamDir, jobs)
	snapshot.Queue = summarizeQueueItems(pipelineQueue, now)
	applyQueueQuarantineSummary(&snapshot.Queue, pipelineQuarantine)
	snapshot.Attention = scopePipelineTriageActions(pipeline, filterJobTriageItemsByJobIDs(snapshot.Attention, ownedIDs))
	if pipeline == "" {
		snapshot.Attention = scopePipelineTriageActionsByOwner(snapshot.Attention)
	}
	snapshot.ReadySteps = scopePipelineReadyRows(pipeline, filterJobReadyRowsByJobIDs(snapshot.ReadySteps, ownedIDs))
	if pipeline == "" {
		snapshot.ReadySteps = scopePipelineReadyRowsByOwner(snapshot.ReadySteps)
	}
	snapshot.StatusPreviews = filterJobStatusPreviewsByJobIDs(snapshot.StatusPreviews, ownedIDs)
	return filterJobTriageSnapshot(snapshot, filters), nil
}

func runPipelineTriageWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, staleAfter time.Duration, filters jobTriageFilters, jsonOut bool, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectPipelineTriage(teamDir, pipeline, time.Now().UTC(), staleAfter, filters)
		if err != nil {
			return err
		}
		if jsonOut {
			if err := json.NewEncoder(w).Encode(snapshot); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := renderJobTriage(w, snapshot, false, nil); err != nil {
				return err
			}
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

func scopePipelineReadyRows(pipeline string, rows []jobReadyRow) []jobReadyRow {
	if len(rows) == 0 || strings.TrimSpace(pipeline) == "" {
		return rows
	}
	out := make([]jobReadyRow, len(rows))
	copy(out, rows)
	for idx := range out {
		out[idx].Actions = pipelineReadyRowActions(pipeline, out[idx])
	}
	return out
}

func scopePipelineReadyRowsByOwner(rows []jobReadyRow) []jobReadyRow {
	if len(rows) == 0 {
		return rows
	}
	out := make([]jobReadyRow, 0, len(rows))
	for _, row := range rows {
		pipeline := strings.TrimSpace(row.Pipeline)
		if pipeline == "" {
			continue
		}
		row.Actions = pipelineReadyRowActions(pipeline, row)
		out = append(out, row)
	}
	return out
}

func pipelineReadyRowActions(pipeline string, row jobReadyRow) []string {
	if jobReadyRowIsAdvanceable(row) {
		actions := []string{pipelineTickPreviewAction(pipeline, false)}
		if row.ParallelReadySteps > 1 {
			actions = append(actions, pipelineTickPreviewAction(pipeline, true))
		}
		return actions
	}
	return scopePipelineExplainActionList(pipeline, row.JobID, row.StepID, row.Actions)
}

func scopePipelineTriageActions(pipeline string, items []jobTriageItem) []jobTriageItem {
	if len(items) == 0 || strings.TrimSpace(pipeline) == "" {
		return items
	}
	out := make([]jobTriageItem, len(items))
	copy(out, items)
	for idx := range out {
		out[idx].Actions = scopePipelineTriageItemActions(pipeline, out[idx])
	}
	return out
}

func scopePipelineTriageActionsByOwner(items []jobTriageItem) []jobTriageItem {
	if len(items) == 0 {
		return items
	}
	out := make([]jobTriageItem, 0, len(items))
	for _, item := range items {
		pipeline := strings.TrimSpace(item.Pipeline)
		if pipeline == "" {
			continue
		}
		item.Actions = scopePipelineTriageItemActions(pipeline, item)
		out = append(out, item)
	}
	return out
}

func scopePipelineTriageItemActions(pipeline string, item jobTriageItem) []string {
	jobID := strings.TrimSpace(item.JobID)
	if jobID == "" {
		return item.Actions
	}
	actions := append([]string(nil), item.Actions...)
	actions = scopePipelineTriageQueueActions(pipeline, jobID, item, actions)
	if stringSliceContains(item.Reasons, "failed") || stringSliceContains(item.Reasons, "failed_step") {
		actions = replaceOrAppendScopedTriageAction(actions,
			fmt.Sprintf("agent-team job retry %s --dispatch", jobID),
			pipelineTriageRetryAction(pipeline, item),
		)
	}
	if stringSliceContains(item.Reasons, "blocked") || stringSliceContains(item.Reasons, "blocked_step") || stringSliceContains(item.Reasons, "status_file_blocked") {
		actions = replaceOrAppendScopedTriageAction(actions,
			jobUnblockAction(jobID, item.StepID),
			pipelineTriageUnblockAction(pipeline, item),
		)
	}
	if stringSliceContains(item.Reasons, "held") || stringSliceContains(item.Reasons, "expired_hold") {
		actions = replaceOrAppendScopedTriageAction(actions,
			fmt.Sprintf("agent-team job release %s", jobID),
			pipelineTriageReleaseAction(pipeline, item),
		)
	}
	if stringSliceContains(item.Reasons, "cleanup_ready") {
		actions = replaceOrAppendScopedTriageAction(actions,
			fmt.Sprintf("agent-team job cleanup %s --dry-run", jobID),
			fmt.Sprintf("agent-team pipeline cleanup %s --dry-run", pipeline),
		)
	}
	if stringSliceContains(item.Reasons, "stale_running") && strings.TrimSpace(item.StepID) != "" {
		actions = replaceOrAppendScopedTriageAction(actions,
			fmt.Sprintf("agent-team job timeout %s --dry-run", jobID),
			pipelineTriageTimeoutAction(pipeline, item),
		)
	}
	if stringSliceContains(item.Reasons, "running_without_instance") {
		jobAction := fmt.Sprintf("agent-team job adopt %s --pid <pid> --dry-run", jobID)
		pipelineAction := fmt.Sprintf("agent-team pipeline adopt %s %s --pid <pid> --dry-run", pipeline, jobID)
		if stepID := strings.TrimSpace(item.StepID); stepID != "" {
			jobAction = fmt.Sprintf("agent-team job adopt %s --step %s --pid <pid> --dry-run", jobID, stepID)
			pipelineAction = fmt.Sprintf("agent-team pipeline adopt %s %s --step %s --pid <pid> --dry-run", pipeline, jobID, stepID)
		}
		actions = replaceOrAppendScopedTriageAction(actions, jobAction, pipelineAction)
	}
	return actions
}

func scopePipelineTriageQueueActions(pipeline, jobID string, item jobTriageItem, actions []string) []string {
	if stringSliceContains(item.Reasons, "queue_dead") {
		if len(item.QueueIDs) == 1 {
			queueID := item.QueueIDs[0]
			actions = replaceOrAppendScopedTriageAction(actions,
				fmt.Sprintf("agent-team job queue retry %s %s", jobID, queueID),
				fmt.Sprintf("agent-team pipeline queue retry %s %s", pipeline, queueID),
			)
		} else {
			actions = replaceOrAppendScopedTriageAction(actions,
				jobQueueRetryAllRecoveryAction(jobID, false),
				queueRetryAllRecoveryAction(fmt.Sprintf("agent-team pipeline queue retry %s", pipeline), false, fmt.Sprintf("--job %s", jobID)),
			)
		}
	}
	if stringSliceContains(item.Reasons, "queue_quarantined") {
		actions = replaceOrAppendScopedTriageAction(actions,
			fmt.Sprintf("agent-team job queue quarantine %s", jobID),
			fmt.Sprintf("agent-team pipeline queue quarantine %s --job %s", pipeline, jobID),
		)
		if item.QueueQuarantineRestorable == 1 && len(item.QueueQuarantineRestorablePaths) == 1 {
			path := item.QueueQuarantineRestorablePaths[0]
			actions = replaceOrAppendScopedTriageAction(actions,
				fmt.Sprintf("agent-team job queue quarantine restore %s %s --dry-run", jobID, path),
				fmt.Sprintf("agent-team pipeline queue quarantine restore %s %s --dry-run", pipeline, path),
			)
		} else if item.QueueQuarantineRestorable > 1 {
			actions = replaceOrAppendScopedTriageAction(actions,
				fmt.Sprintf("agent-team job queue quarantine restore %s --all --limit %d --dry-run", jobID, queueRecoveryHintLimit),
				fmt.Sprintf("agent-team pipeline queue quarantine restore %s --all --job %s --limit %d --dry-run", pipeline, jobID, queueRecoveryHintLimit),
			)
		}
		if item.QueueQuarantineUnrestorable > 0 {
			actions = replaceOrAppendScopedTriageAction(actions,
				fmt.Sprintf("agent-team job queue quarantine drop %s --all --unrestorable --limit %d --dry-run", jobID, queueRecoveryHintLimit),
				fmt.Sprintf("agent-team pipeline queue quarantine drop %s --all --job %s --unrestorable --limit %d --dry-run", pipeline, jobID, queueRecoveryHintLimit),
			)
		}
	}
	return actions
}

func pipelineTriageRetryAction(pipeline string, item jobTriageItem) string {
	action := fmt.Sprintf("agent-team pipeline retry %s", pipeline)
	if stepID := strings.TrimSpace(item.StepID); stepID != "" {
		action = fmt.Sprintf("%s --step %s", action, stepID)
	}
	return action + " --dry-run --dispatch --preview-routes"
}

func pipelineTriageUnblockAction(pipeline string, item jobTriageItem) string {
	action := fmt.Sprintf("agent-team pipeline unblock %s", pipeline)
	if stepID := strings.TrimSpace(item.StepID); stepID != "" {
		action = fmt.Sprintf("%s --step %s", action, stepID)
	}
	return action + " <answer...> --dry-run"
}

func pipelineTriageReleaseAction(pipeline string, item jobTriageItem) string {
	action := fmt.Sprintf("agent-team pipeline release %s", pipeline)
	if stringSliceContains(item.Reasons, "expired_hold") {
		action += " --expired"
	}
	return action + " --dry-run"
}

func pipelineTriageTimeoutAction(pipeline string, item jobTriageItem) string {
	action := fmt.Sprintf("agent-team pipeline timeout %s", pipeline)
	if stepID := strings.TrimSpace(item.StepID); stepID != "" {
		action = fmt.Sprintf("%s --step %s", action, stepID)
	}
	if target := strings.TrimSpace(item.StepTarget); target != "" {
		action = fmt.Sprintf("%s --target-agent %s", action, target)
	}
	return action + " --dry-run"
}

type pipelineWaitTimeoutError struct {
	Jobs    []*job.Job
	Pending []*job.Job
}

func (e *pipelineWaitTimeoutError) Error() string {
	return "pipeline wait timed out"
}

func filterPipelineWaitJobs(jobs []*job.Job, filters []string) ([]*job.Job, error) {
	values := splitFilterValues(filters)
	if len(values) == 0 {
		return jobs, nil
	}
	byID := map[string]*job.Job{}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		byID[job.NormalizeID(j.ID)] = j
	}
	out := make([]*job.Job, 0, len(values))
	missing := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		id := job.NormalizeID(value)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		j := byID[id]
		if j == nil {
			missing = append(missing, id)
			continue
		}
		out = append(out, j)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("job(s) not owned by pipeline: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func runPipelineWait(ctx context.Context, teamDir string, jobs []*job.Job, statuses map[job.Status]bool, events map[string]bool, interval time.Duration) ([]*job.Job, error) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		ids = append(ids, job.NormalizeID(j.ID))
	}
	last := make([]*job.Job, 0, len(ids))
	pending := make([]*job.Job, 0, len(ids))
	for {
		last = last[:0]
		pending = pending[:0]
		for _, id := range ids {
			j, err := job.Read(teamDir, id)
			if err != nil {
				return nil, err
			}
			last = append(last, j)
			if !pipelineWaitJobMatches(j, statuses, events) {
				pending = append(pending, j)
			}
		}
		if len(pending) == 0 {
			return append([]*job.Job(nil), last...), nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if ctx.Err() == context.DeadlineExceeded {
				return append([]*job.Job(nil), last...), &pipelineWaitTimeoutError{
					Jobs:    append([]*job.Job(nil), last...),
					Pending: append([]*job.Job(nil), pending...),
				}
			}
			return append([]*job.Job(nil), last...), ctx.Err()
		case <-timer.C:
		}
	}
}

func pipelineWaitJobMatches(j *job.Job, statuses map[job.Status]bool, events map[string]bool) bool {
	if j == nil {
		return false
	}
	statusMatched := len(statuses) == 0 || statuses[j.Status]
	eventMatched := len(events) == 0 || events[strings.TrimSpace(j.LastEvent)]
	return statusMatched && eventMatched
}

func pipelineWaitPendingSummary(jobs []*job.Job) string {
	if len(jobs) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		part := fmt.Sprintf("%s=%s", j.ID, j.Status)
		if event := strings.TrimSpace(j.LastEvent); event != "" {
			part += " event=" + event
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

func pipelineWaitHasFailed(jobs []*job.Job) bool {
	for _, j := range jobs {
		if j != nil && j.Status == job.StatusFailed {
			return true
		}
	}
	return false
}

func collectPipelineQueueItems(teamDir, pipeline string, filters queueListFilters, now time.Time) ([]*daemon.QueueItem, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	owned := queueItemsForJobs(items, jobs)
	return filterQueueItems(owned, filters.withNow(now).withRuntimeByInstance(queueRuntimeMap(teamDir))), nil
}

func collectPipelineQueueQuarantineItems(teamDir, pipeline string, filters queueListFilters) ([]queueQuarantineItem, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	items, err := listQueueQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = queueQuarantineItemsForJobs(items, jobs)
	return filterQueueQuarantineItems(items, filters), nil
}

func readPipelineQueueQuarantineItem(teamDir, pipeline, rawPath string) (queueQuarantineItem, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	rel, err := normalizeQueueQuarantinePath(rawPath)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	item, err := inspectQueueQuarantineFile(queueRoot, rel)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	if !queueQuarantineMatchesAnyJob(item, jobs) {
		return queueQuarantineItem{}, fmt.Errorf("quarantined queue file %q is not owned by pipeline %q", item.Path, pipeline)
	}
	return item, nil
}

func readPipelineQueueItem(cmd *cobra.Command, teamDir, pipeline, id, verb string) (*daemon.QueueItem, error) {
	item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue %s: queue item %q not found.\n", verb, id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue %s: %v\n", verb, err)
		return nil, exitErr(1)
	}
	if len(queueItemsForJobs([]*daemon.QueueItem{item}, jobs)) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline queue %s: queue item %q is not owned by pipeline %q.\n", verb, id, pipeline)
		return nil, exitErr(2)
	}
	return item, nil
}

func runPipelineQueueList(w io.Writer, teamDir, pipeline string, filters queueListFilters, opts queueListOptions, jsonOut bool, tmpl *template.Template) error {
	items, err := collectPipelineQueueItems(teamDir, pipeline, filters, time.Now().UTC())
	if err != nil {
		return err
	}
	runtimeByInstance := queueRuntimeMap(teamDir)
	items = prepareQueueListItems(items, opts, runtimeByInstance)
	if jsonOut {
		return json.NewEncoder(w).Encode(items)
	}
	if tmpl != nil {
		return renderQueueItemsFormat(w, items, tmpl)
	}
	actionResolver, err := pipelineQueueActionResolverForScope(teamDir, pipeline)
	if err != nil {
		return err
	}
	renderQueueTableWithActions(w, items, runtimeByInstance, actionResolver)
	return nil
}

func runPipelineQueueSummary(w io.Writer, teamDir, pipeline string, filters queueListFilters, jsonOut bool) error {
	now := time.Now().UTC()
	items, err := collectPipelineQueueItems(teamDir, pipeline, filters, now)
	if err != nil {
		return err
	}
	summary := summarizeQueueItems(items, now, queueRuntimeMap(teamDir))
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	renderQueueSummary(w, summary)
	return nil
}

func runPipelineQueueListWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, filters queueListFilters, opts queueListOptions, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runPipelineQueueList(w, teamDir, pipeline, filters, opts, jsonOut, tmpl); err != nil {
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

func runPipelineQueueSummaryWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, filters queueListFilters, jsonOut bool, interval time.Duration, clear bool) error {
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
		if err := runPipelineQueueSummary(w, teamDir, pipeline, filters, jsonOut); err != nil {
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

func runPipelineQueueRetryAll(w io.Writer, teamDir, pipeline string, filters queueListFilters, sortMode string, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := pipelineQueueRetryResults(teamDir, pipeline, filters, sortMode, limit, dryRun)
	if err != nil {
		return err
	}
	return renderQueueRetryResults(w, results, jsonOut, tmpl)
}

func pipelineQueueRetryResults(teamDir, pipeline string, filters queueListFilters, sortMode string, limit int, dryRun bool) ([]queueRetryResult, error) {
	matches, err := collectPipelineQueueItems(teamDir, pipeline, filters, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	matches = prepareQueueActionMatches(matches, sortMode, limit, queueRuntimeMap(teamDir))
	return retryQueueItemMatches(teamDir, matches, dryRun)
}

func runPipelineQueueDropAll(w io.Writer, teamDir, pipeline string, filters queueListFilters, sortMode string, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	matches, err := collectPipelineQueueItems(teamDir, pipeline, filters, time.Now().UTC())
	if err != nil {
		return err
	}
	matches = prepareQueueActionMatches(matches, sortMode, limit, queueRuntimeMap(teamDir))
	results, err := dropQueueItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderQueueDropResults(w, results, jsonOut, tmpl)
}

func runPipelineQueuePrune(w io.Writer, teamDir, pipeline, state string, olderThan time.Duration, filters queueListFilters, limit int, now time.Time, dryRun, jsonOut bool, tmpl *template.Template) error {
	items, err := collectPipelineQueueItems(teamDir, pipeline, filters, now)
	if err != nil {
		return err
	}
	matches := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if queueItemMatchesPrune(item, state, olderThan, now) {
			matches = append(matches, item)
		}
	}
	matches = prepareQueuePruneMatches(matches, limit)
	results, err := pruneQueueItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderQueuePruneResults(w, results, jsonOut, tmpl)
}

func pipelineQueueActionResolver(pipeline string) queueActionResolver {
	return func(item *daemon.QueueItem, now time.Time) []string {
		return pipelineQueueItemActions(pipeline, item, now)
	}
}

func pipelineQueueActionResolverForScope(teamDir, pipeline string) (queueActionResolver, error) {
	pipeline = strings.TrimSpace(pipeline)
	if pipeline != "" {
		return pipelineQueueActionResolver(pipeline), nil
	}
	jobs, err := selectedPipelineJobs(teamDir, "")
	if err != nil {
		return nil, err
	}
	return func(item *daemon.QueueItem, now time.Time) []string {
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.Pipeline) == "" {
				continue
			}
			if queueItemMatchesJob(item, j) {
				return pipelineQueueItemActions(j.Pipeline, item, now)
			}
		}
		return nil
	}, nil
}

func pipelineQueueItemActions(pipeline string, item *daemon.QueueItem, now time.Time) []string {
	if item == nil {
		return nil
	}
	queueCommand := func(verb string) string {
		return fmt.Sprintf("agent-team pipeline queue %s %s %s", verb, pipeline, item.ID)
	}
	switch item.State {
	case daemon.QueueStateDead:
		return []string{
			queueCommand("retry"),
			queueCommand("drop"),
		}
	case daemon.QueueStatePending:
		if !item.NextRetry.IsZero() && item.NextRetry.After(now.UTC()) {
			return []string{
				queueCommand("show"),
				queueCommand("drop"),
			}
		}
		return []string{
			"agent-team queue drain",
			queueCommand("drop"),
		}
	default:
		return nil
	}
}

type pipelineOwnedMetadata struct {
	Metadata       []*daemon.Metadata
	JobForInstance map[string]string
	JobByInstance  map[string]*job.Job
}

func collectPipelineOwnedMetadata(teamDir, pipeline string, metas []*daemon.Metadata) (pipelineOwnedMetadata, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return pipelineOwnedMetadata{}, err
	}
	byInstance := map[string]*daemon.Metadata{}
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		byInstance[meta.Instance] = meta
	}

	selected := map[string]*daemon.Metadata{}
	jobForInstance := map[string]string{}
	jobByInstance := map[string]*job.Job{}
	for _, j := range jobs {
		jobID := job.NormalizeID(j.ID)
		for _, meta := range metadataForResumePlanJob(metas, byInstance, j) {
			if meta == nil {
				continue
			}
			if _, ok := selected[meta.Instance]; !ok {
				selected[meta.Instance] = meta
			}
			if jobID != "" && strings.TrimSpace(meta.Job) == "" && jobForInstance[meta.Instance] == "" {
				jobForInstance[meta.Instance] = jobID
			}
			if jobByInstance[meta.Instance] == nil {
				jobByInstance[meta.Instance] = j
			}
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*daemon.Metadata, 0, len(names))
	for _, name := range names {
		out = append(out, selected[name])
	}
	return pipelineOwnedMetadata{Metadata: out, JobForInstance: jobForInstance, JobByInstance: jobByInstance}, nil
}

func collectPipelineOwnedInstanceNames(teamDir, pipeline string) (map[string]bool, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	jobIDs := map[string]bool{}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if id := job.NormalizeID(j.ID); id != "" {
			jobIDs[id] = true
		}
		if instance := strings.TrimSpace(j.Instance); instance != "" {
			names[instance] = true
		}
		for _, step := range j.Steps {
			if instance := strings.TrimSpace(step.Instance); instance != "" {
				names[instance] = true
			}
		}
	}
	metas, err := pipelineDaemonMetadata(teamDir)
	if err != nil {
		return nil, err
	}
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		if jobIDs[job.NormalizeID(meta.Job)] {
			names[meta.Instance] = true
		}
	}
	return names, nil
}

func pipelineDaemonMetadata(teamDir string) ([]*daemon.Metadata, error) {
	if dc, err := newDaemonClient(teamDir); err == nil {
		return dc.Instances()
	} else if !errors.Is(err, errDaemonNotRunning) {
		return nil, err
	}
	return daemon.ListMetadata(daemon.DaemonRoot(teamDir))
}

func collectPipelinePsRows(teamDir, pipeline string, now time.Time, opts psOptions) ([]instanceRow, error) {
	names, err := collectPipelineOwnedInstanceNames(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return []instanceRow{}, nil
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	scoped := make([]instanceRow, 0, len(rows))
	for _, row := range rows {
		if names[row.Instance] {
			scoped = append(scoped, row)
		}
	}
	return filterLimitSortPsRows(scoped, opts), nil
}

func runPipelinePs(w io.Writer, teamDir, pipeline string, now time.Time, opts psOptions) error {
	rows, err := collectPipelinePsRows(teamDir, pipeline, now, opts)
	if err != nil {
		return err
	}
	return renderPsTable(w, rows)
}

func runPipelinePsJSON(w io.Writer, teamDir, pipeline string, now time.Time, opts psOptions) error {
	rows, err := collectPipelinePsRows(teamDir, pipeline, now, opts)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(psJSONRows(rows))
}

func runPipelinePsQuiet(w io.Writer, teamDir, pipeline string, now time.Time, opts psOptions) error {
	rows, err := collectPipelinePsRows(teamDir, pipeline, now, opts)
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintln(w, row.Instance)
	}
	return nil
}

func runPipelinePsFormat(w io.Writer, teamDir, pipeline string, now time.Time, opts psOptions, tmpl *template.Template) error {
	rows, err := collectPipelinePsRows(teamDir, pipeline, now, opts)
	if err != nil {
		return err
	}
	return renderPsFormat(w, rows, tmpl)
}

func runPipelinePsSummary(w io.Writer, teamDir, pipeline string, now time.Time, opts psOptions) error {
	rows, err := collectPipelinePsRows(teamDir, pipeline, now, opts)
	if err != nil {
		return err
	}
	return renderPsSummary(w, psSummaryRows(rows))
}

func runPipelinePsSummaryJSON(w io.Writer, teamDir, pipeline string, now time.Time, opts psOptions) error {
	rows, err := collectPipelinePsRows(teamDir, pipeline, now, opts)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(psSummaryRows(rows))
}

func runPipelinePsWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runPipelinePsJSON(w, teamDir, pipeline, now(), opts); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runPipelinePs(w, teamDir, pipeline, now(), opts); err != nil {
				return err
			}
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

func runPipelinePsSummaryWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runPipelinePsSummaryJSON(w, teamDir, pipeline, now(), opts); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runPipelinePsSummary(w, teamDir, pipeline, now(), opts); err != nil {
				return err
			}
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

func runPipelinePsFormatWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, interval time.Duration, now func() time.Time, opts psOptions, tmpl *template.Template) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := runPipelinePsFormat(w, teamDir, pipeline, now(), opts, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func collectPipelineRuntimeResumePlans(teamDir, pipeline string, stepFilter string, statusFilters []string, runtimeFilters []string, actionFilters []string, staleOnly bool, unhealthyOnly bool) ([]runtimeResumePlan, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	owned, err := collectPipelineOwnedMetadata(teamDir, pipeline, metas)
	if err != nil {
		return nil, err
	}
	statusSet, err := parseRuntimeResumeStatusFilter(statusFilters)
	if err != nil {
		return nil, err
	}
	runtimeSet, err := parseRuntimeResumeRuntimeFilter(runtimeFilters)
	if err != nil {
		return nil, err
	}
	actionSet, err := parseRuntimeResumeActionFilter(actionFilters)
	if err != nil {
		return nil, err
	}

	plans := make([]runtimeResumePlan, 0, len(owned.Metadata))
	for _, meta := range owned.Metadata {
		if len(statusSet) > 0 && !statusSet[strings.ToLower(strings.TrimSpace(string(meta.Status)))] {
			continue
		}
		runtimeKind := lifecycleMetadataRuntimeKind(meta)
		if len(runtimeSet) > 0 && !runtimeSet[string(runtimeKind)] {
			continue
		}
		plan := runtimeResumePlanFromMetadata(meta)
		if j := owned.JobByInstance[meta.Instance]; j != nil {
			plan = runtimeResumePlanWithJobContext(plan, j)
		} else if strings.TrimSpace(plan.Job) == "" {
			if jobID := owned.JobForInstance[meta.Instance]; jobID != "" {
				plan = runtimeResumePlanWithJobCommands(plan, jobID)
			}
		}
		if len(actionSet) > 0 && !actionSet[plan.RecommendedAction] {
			continue
		}
		if !runtimeResumePlanMatchesStep(plan, stepFilter) {
			continue
		}
		if staleOnly && !plan.Stale {
			continue
		}
		if unhealthyOnly && !runtimeResumePlanUnhealthy(plan) {
			continue
		}
		plans = append(plans, plan)
	}
	sort.SliceStable(plans, func(i, j int) bool {
		return plans[i].Instance < plans[j].Instance
	})
	return plans, nil
}

type pipelineSendClient struct {
	sendClient
	teamDir  string
	pipeline string
}

func (c pipelineSendClient) Instances() ([]*daemon.Metadata, error) {
	metas, err := c.sendClient.Instances()
	if err != nil {
		return nil, err
	}
	owned, err := collectPipelineOwnedMetadata(c.teamDir, c.pipeline, metas)
	if err != nil {
		return nil, err
	}
	return owned.Metadata, nil
}

type pipelineStatsLister struct {
	instanceLister
	teamDir  string
	pipeline string
}

func (l pipelineStatsLister) Instances() ([]*daemon.Metadata, error) {
	metas, err := l.instanceLister.Instances()
	if err != nil {
		return nil, err
	}
	owned, err := collectPipelineOwnedMetadata(l.teamDir, l.pipeline, metas)
	if err != nil {
		return nil, err
	}
	return owned.Metadata, nil
}

func runPipelineLogs(cmd *cobra.Command, teamDir, pipeline string, opts logsOptions, listOpts logListOptions) error {
	rows, err := collectPipelineLogRows(teamDir, pipeline, listOpts, opts.Since, opts.Limit)
	if err != nil {
		return err
	}
	if opts.Latest {
		rows = latestLogListRowsLimit(rows, 1)
	}
	if opts.List {
		if opts.JSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
		}
		if opts.Format != nil {
			return renderLogListFormat(cmd.OutOrStdout(), rows, opts.Format)
		}
		renderLogList(cmd.OutOrStdout(), rows)
		return nil
	}
	if len(rows) == 0 {
		if opts.Since != nil || opts.Grep != nil {
			fmt.Fprintln(cmd.OutOrStdout(), "(no matching logs)")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		return nil
	}
	if opts.LastMessage {
		if len(rows) == 1 {
			return streamSelectedLastMessageWithPrefix(cmd, teamDir, rows[0], "agent-team pipeline logs")
		}
		return streamLastMessageRows(cmd.OutOrStdout(), teamDir, rows, !opts.NoPrefix)
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()
	if len(rows) == 1 {
		if opts.Follow {
			if err := streamLocalLog(ctx, cmd.OutOrStdout(), rows[0].path, true, opts.Tail, nil, opts.Clean); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline logs: log not found at %s.\n", rows[0].LogPath)
					return exitErr(1)
				}
				return err
			}
			return nil
		}
		if err := streamLogRowOnce(ctx, cmd.OutOrStdout(), rows[0], opts); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline logs: log not found at %s.\n", rows[0].LogPath)
				return exitErr(1)
			}
			return err
		}
		return nil
	}
	if opts.Follow {
		return streamLocalLogRowsFollow(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Clean)
	}
	return streamLocalLogRowsOnce(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Grep, opts.Clean)
}

func collectPipelineLogRows(teamDir, pipeline string, opts logListOptions, since *time.Time, limit int) ([]logListRow, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	owned, err := collectPipelineOwnedMetadata(teamDir, pipeline, metas)
	if err != nil {
		return nil, err
	}
	rows, err := logListRowsFromMetadata(teamDir, owned.Metadata)
	if err != nil {
		return nil, err
	}
	rows = filterLogListRows(rows, opts)
	rows = filterLogListRowsSince(rows, since)
	rows = latestLogListRowsLimit(rows, limit)
	if rows == nil {
		return []logListRow{}, nil
	}
	return rows, nil
}

func pipelineEventFilters(teamDir, pipeline string, actionFilters, statusFilters []string, sinceRaw string, now func() time.Time) (eventFilters, error) {
	filters, err := newEventFilters(actionFilters, nil, nil, statusFilters, sinceRaw, now)
	if err != nil {
		return eventFilters{}, err
	}
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return eventFilters{}, err
	}
	instances := map[string]bool{}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if instance := strings.TrimSpace(j.Instance); instance != "" {
			instances[instance] = true
		}
		for _, step := range j.Steps {
			if instance := strings.TrimSpace(step.Instance); instance != "" {
				instances[instance] = true
			}
		}
	}
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return eventFilters{}, err
	}
	owned, err := collectPipelineOwnedMetadata(teamDir, pipeline, metas)
	if err != nil {
		return eventFilters{}, err
	}
	for _, meta := range owned.Metadata {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		instances[meta.Instance] = true
	}
	if len(instances) == 0 {
		instances[""] = false
	}
	filters.instances = instances
	filters.instancePrefixes = nil
	return filters, nil
}

func pipelineEventRuntimeFilter(teamDir, pipeline string, filters eventFilters, runtimeFilters []string) (eventFilters, error) {
	if len(runtimeFilters) == 0 {
		return filters, nil
	}
	runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
	if err != nil {
		return filters, err
	}
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return filters, err
	}
	owned, err := collectPipelineOwnedMetadata(teamDir, pipeline, metas)
	if err != nil {
		return filters, err
	}
	selected := map[string]bool{}
	for _, meta := range owned.Metadata {
		if meta == nil {
			continue
		}
		if !eventFilterMatchesInstance(filters, meta.Instance) {
			continue
		}
		if runtimes[metadataRuntimeKey(meta)] {
			selected[meta.Instance] = true
		}
	}
	if len(selected) == 0 {
		selected[""] = false
	}
	filters.instances = selected
	filters.instancePrefixes = nil
	return filters, nil
}

func holdStateSelected(state string, stateFilter map[string]bool, stateDefault bool) bool {
	state = strings.TrimSpace(state)
	if stateDefault {
		return state != "held" && state != "done"
	}
	return len(stateFilter) == 0 || stateFilter[state]
}

func advanceReadyPipelineJobs(cmd *cobra.Command, teamDir, pipeline, workspace string, selection runtimeSelection, limit int, dryRun bool, previewRoutes bool, allReadySteps bool) ([]pipelineAdvanceResult, error) {
	if allReadySteps {
		return advanceAllReadyPipelineSteps(cmd, teamDir, pipeline, workspace, selection, limit, dryRun, previewRoutes)
	}
	rows, err := collectJobReadyRows(teamDir, pipeline, map[string]bool{"ready": true, "queued": true})
	if err != nil {
		return nil, err
	}
	rows = filterAdvanceablePipelineRows(rows)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	results := make([]pipelineAdvanceResult, 0, len(rows))
	for _, row := range rows {
		result := pipelineAdvanceResult{
			JobID:      row.JobID,
			Ticket:     row.Ticket,
			Pipeline:   row.Pipeline,
			StepID:     row.StepID,
			Target:     row.Target,
			StepStatus: row.StepStatus,
			Instance:   row.Instance,
			Action:     "would_advance",
			DryRun:     dryRun,
			Message:    row.Message,
		}
		if dryRun {
			if previewRoutes {
				j, err := job.Read(teamDir, row.JobID)
				if err != nil {
					return nil, err
				}
				preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, selection)
				if err != nil {
					return nil, err
				}
				result.Preview = preview
				result.Message = preview.Message
				if preview.Step != nil {
					result.StepID = preview.Step.ID
					result.Target = preview.Step.Target
					result.StepStatus = preview.Step.Status
					result.Instance = preview.Step.Instance
				}
			}
			results = append(results, result)
			continue
		}
		j, err := job.Read(teamDir, row.JobID)
		if err != nil {
			return nil, err
		}
		advanced, err := advanceJob(cmd, teamDir, j, workspace, selection)
		if err != nil {
			return nil, err
		}
		result.Action = pipelineAdvanceAction(advanced)
		result.DryRun = false
		result.Job = advanced.Job
		result.Step = advanced.Step
		result.Event = advanced.Event
		result.Message = advanced.Message
		if advanced.Job != nil {
			result.Ticket = advanced.Job.Ticket
			result.Pipeline = advanced.Job.Pipeline
		}
		if advanced.Step != nil {
			result.StepID = advanced.Step.ID
			result.Target = advanced.Step.Target
			result.StepStatus = advanced.Step.Status
			result.Instance = advanced.Step.Instance
		}
		results = append(results, result)
	}
	return results, nil
}

func advanceAllReadyPipelineSteps(cmd *cobra.Command, teamDir, pipeline, workspace string, selection runtimeSelection, limit int, dryRun bool, previewRoutes bool) ([]pipelineAdvanceResult, error) {
	rows, err := collectJobReadyRows(teamDir, pipeline, map[string]bool{"ready": true, "queued": true})
	if err != nil {
		return nil, err
	}
	rows = filterAdvanceablePipelineRows(rows)
	results := []pipelineAdvanceResult{}
	for _, row := range rows {
		if limit > 0 && len(results) >= limit {
			break
		}
		j, err := job.Read(teamDir, row.JobID)
		if err != nil {
			return nil, err
		}
		if dryRun {
			stepResults, err := previewAllReadyJobSteps(teamDir, j, workspace, selection, previewRoutes, limit-len(results))
			if err != nil {
				return nil, err
			}
			results = append(results, stepResults...)
			continue
		}
		for {
			if limit > 0 && len(results) >= limit {
				break
			}
			steps := advanceableJobSteps(j)
			if len(steps) == 0 {
				break
			}
			advanced, err := advanceJobStep(cmd, teamDir, j, steps[0], workspace, selection)
			if err != nil {
				return nil, err
			}
			result := pipelineAdvanceResultFromAdvance(row, advanced)
			results = append(results, result)
			if pipelineAdvanceAction(advanced) != "advanced" || advanced.Job == nil {
				break
			}
			j = advanced.Job
		}
	}
	return results, nil
}

func previewAllReadyJobSteps(teamDir string, j *job.Job, workspace string, selection runtimeSelection, previewRoutes bool, remaining int) ([]pipelineAdvanceResult, error) {
	steps := advanceableJobSteps(j)
	if remaining > 0 && len(steps) > remaining {
		steps = steps[:remaining]
	}
	results := make([]pipelineAdvanceResult, 0, len(steps))
	for _, step := range steps {
		result := pipelineAdvanceResult{
			JobID:      j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			StepID:     step.ID,
			Target:     step.Target,
			StepStatus: step.Status,
			Instance:   step.Instance,
			Action:     "would_advance",
			DryRun:     true,
			Message:    "step " + step.ID + " is ready",
		}
		if step.Status == job.StatusQueued {
			result.Message = "step " + step.ID + " is queued and ready"
		}
		if previewRoutes {
			preview, err := previewJobStepDispatch(teamDir, j, step, workspace, selection)
			if err != nil {
				return nil, err
			}
			result.Preview = preview
			if preview.Message != "" {
				result.Message = preview.Message
			}
			if preview.Step != nil {
				result.StepID = preview.Step.ID
				result.Target = preview.Step.Target
				result.StepStatus = preview.Step.Status
				result.Instance = preview.Step.Instance
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func pipelineAdvanceResultFromAdvance(row jobReadyRow, advanced *jobAdvanceResult) pipelineAdvanceResult {
	result := pipelineAdvanceResult{
		JobID:      row.JobID,
		Ticket:     row.Ticket,
		Pipeline:   row.Pipeline,
		StepID:     row.StepID,
		Target:     row.Target,
		StepStatus: row.StepStatus,
		Instance:   row.Instance,
		Action:     pipelineAdvanceAction(advanced),
		Message:    row.Message,
	}
	if advanced == nil {
		return result
	}
	result.Job = advanced.Job
	result.Step = advanced.Step
	result.Event = advanced.Event
	result.Message = advanced.Message
	if advanced.Job != nil {
		result.JobID = advanced.Job.ID
		result.Ticket = advanced.Job.Ticket
		result.Pipeline = advanced.Job.Pipeline
	}
	if advanced.Step != nil {
		result.StepID = advanced.Step.ID
		result.Target = advanced.Step.Target
		result.StepStatus = advanced.Step.Status
		result.Instance = advanced.Step.Instance
	}
	return result
}

func waitForPipelineAdvanceResults(cmd *cobra.Command, teamDir string, results []pipelineAdvanceResult, statuses map[job.Status]bool, events map[string]bool, timeout, interval time.Duration, prefix string) ([]pipelineAdvanceResult, error) {
	ids := make([]string, 0, len(results))
	seen := map[string]bool{}
	for _, result := range results {
		id := strings.TrimSpace(result.JobID)
		if id == "" || result.DryRun || result.Action == "skipped" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return results, nil
	}
	jobs := make([]*job.Job, 0, len(ids))
	for _, id := range ids {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	waited, err := runPipelineWait(ctx, teamDir, jobs, statuses, events, interval)
	if err != nil {
		if timeoutErr, ok := err.(*pipelineWaitTimeoutError); ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: timed out waiting for advanced jobs to reach %s (pending=%s).\n",
				prefix, jobWaitConditionList(statuses, events), pipelineWaitPendingSummary(timeoutErr.Pending))
			return nil, exitErr(1)
		}
		return nil, err
	}
	waitedByID := make(map[string]*job.Job, len(waited))
	for _, j := range waited {
		if j != nil {
			waitedByID[j.ID] = j
		}
	}
	refreshed := append([]pipelineAdvanceResult(nil), results...)
	for i := range refreshed {
		if waitedJob := waitedByID[refreshed[i].JobID]; waitedJob != nil {
			refreshPipelineAdvanceResultAfterWait(&refreshed[i], waitedJob)
		}
	}
	return refreshed, nil
}

func refreshPipelineAdvanceResultAfterWait(result *pipelineAdvanceResult, waited *job.Job) {
	if result == nil || waited == nil {
		return
	}
	stepID := strings.TrimSpace(result.StepID)
	result.Job = waited
	result.JobID = waited.ID
	result.Ticket = waited.Ticket
	result.Pipeline = waited.Pipeline
	result.Message = waited.LastStatus
	if stepID == "" {
		return
	}
	idx := jobStepIndex(waited, stepID)
	if idx == -1 {
		result.Step = nil
		return
	}
	step := cloneJobStep(&waited.Steps[idx])
	result.Step = step
	result.StepID = step.ID
	result.Target = step.Target
	result.StepStatus = step.Status
	result.Instance = step.Instance
}

func pipelineAdvanceResultsHaveFailed(results []pipelineAdvanceResult) bool {
	for _, result := range results {
		if result.Job != nil && result.Job.Status == job.StatusFailed {
			return true
		}
	}
	return false
}

func approvePipelineManualGates(cmd *cobra.Command, teamDir, pipeline, workspace string, selection runtimeSelection, stepFilter, message string, limit int, dispatchNow, dryRun bool, previewRoutes bool) ([]pipelineApproveResult, error) {
	rows, err := collectJobReadyRows(teamDir, pipeline, map[string]bool{"blocked": true})
	if err != nil {
		return nil, err
	}
	rows = filterPipelineApproveRows(rows, stepFilter)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	results := make([]pipelineApproveResult, 0, len(rows))
	for _, row := range rows {
		j, err := job.Read(teamDir, row.JobID)
		if err != nil {
			return nil, err
		}
		result := pipelineApproveResult{
			JobID:      j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			StepID:     row.StepID,
			Target:     row.Target,
			StepStatus: row.StepStatus,
			Instance:   row.Instance,
			Action:     "would_approve",
			DryRun:     dryRun,
			Message:    row.Message,
			WaitingFor: append([]string(nil), row.WaitingFor...),
		}
		if strings.TrimSpace(row.StepID) == "" {
			result.Action = "skipped"
			result.Message = "no blocked manual gate"
			results = append(results, result)
			continue
		}
		if len(row.WaitingFor) > 0 {
			result.Action = "skipped"
			result.Message = "waiting for " + strings.Join(row.WaitingFor, ",")
			results = append(results, result)
			continue
		}
		approvalMessage := strings.TrimSpace(message)
		if approvalMessage == "" {
			approvalMessage = "approved manual gate " + row.StepID
		}
		if err := updateJobStep(j, row.StepID, job.StatusQueued, jobStepUpdate{Message: approvalMessage}); err != nil {
			return nil, err
		}
		result.Job = j
		result.Message = j.LastStatus
		if idx := jobStepIndex(j, row.StepID); idx >= 0 {
			result.Step = cloneJobStep(&j.Steps[idx])
			result.StepID = j.Steps[idx].ID
			result.Target = j.Steps[idx].Target
			result.StepStatus = j.Steps[idx].Status
			result.Instance = j.Steps[idx].Instance
		}
		if dispatchNow {
			result.Action = "would_dispatch"
		}
		if dryRun {
			if dispatchNow {
				preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, selection)
				if err != nil {
					return nil, err
				}
				result.Preview = preview
				result.Message = preview.Message
				if preview.Step != nil {
					result.StepID = preview.Step.ID
					result.Target = preview.Step.Target
					result.StepStatus = preview.Step.Status
					result.Instance = preview.Step.Instance
					result.Step = preview.Step
				}
			}
			results = append(results, result)
			continue
		}
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": row.StepID}); err != nil {
			return nil, err
		}
		result.Action = "approved"
		result.DryRun = false
		if dispatchNow {
			advanced, err := advanceJob(cmd, teamDir, j, workspace, selection)
			if err != nil {
				return nil, err
			}
			result.Action = pipelineApproveDispatchAction(advanced)
			result.Job = advanced.Job
			result.Step = advanced.Step
			result.Event = advanced.Event
			result.Message = advanced.Message
			if advanced.Job != nil {
				result.Ticket = advanced.Job.Ticket
				result.Pipeline = advanced.Job.Pipeline
			}
			if advanced.Step != nil {
				result.StepID = advanced.Step.ID
				result.Target = advanced.Step.Target
				result.StepStatus = advanced.Step.Status
				result.Instance = advanced.Step.Instance
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func waitForPipelineApproveResults(cmd *cobra.Command, teamDir string, results []pipelineApproveResult, statuses map[job.Status]bool, events map[string]bool, timeout, interval time.Duration, prefix string) ([]pipelineApproveResult, error) {
	ids := make([]string, 0, len(results))
	seen := map[string]bool{}
	for _, result := range results {
		id := strings.TrimSpace(result.JobID)
		if id == "" || result.DryRun || result.Action == "skipped" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return results, nil
	}
	jobs := make([]*job.Job, 0, len(ids))
	for _, id := range ids {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	waited, err := runPipelineWait(ctx, teamDir, jobs, statuses, events, interval)
	if err != nil {
		if timeoutErr, ok := err.(*pipelineWaitTimeoutError); ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: timed out waiting for approved jobs to reach %s (pending=%s).\n",
				prefix, jobWaitConditionList(statuses, events), pipelineWaitPendingSummary(timeoutErr.Pending))
			return nil, exitErr(1)
		}
		return nil, err
	}
	waitedByID := make(map[string]*job.Job, len(waited))
	for _, j := range waited {
		if j != nil {
			waitedByID[j.ID] = j
		}
	}
	refreshed := append([]pipelineApproveResult(nil), results...)
	for i := range refreshed {
		if waitedJob := waitedByID[refreshed[i].JobID]; waitedJob != nil {
			refreshPipelineApproveResultAfterWait(&refreshed[i], waitedJob)
		}
	}
	return refreshed, nil
}

func refreshPipelineApproveResultAfterWait(result *pipelineApproveResult, waited *job.Job) {
	if result == nil || waited == nil {
		return
	}
	stepID := strings.TrimSpace(result.StepID)
	result.Job = waited
	result.JobID = waited.ID
	result.Ticket = waited.Ticket
	result.Pipeline = waited.Pipeline
	result.Message = waited.LastStatus
	if stepID == "" {
		return
	}
	idx := jobStepIndex(waited, stepID)
	if idx == -1 {
		result.Step = nil
		return
	}
	step := cloneJobStep(&waited.Steps[idx])
	result.Step = step
	result.StepID = step.ID
	result.Target = step.Target
	result.StepStatus = step.Status
	result.Instance = step.Instance
}

func pipelineApproveResultsHaveFailed(results []pipelineApproveResult) bool {
	for _, result := range results {
		if result.Job != nil && result.Job.Status == job.StatusFailed {
			return true
		}
	}
	return false
}

func rejectPipelineManualGates(teamDir, pipeline, stepFilter, message string, limit int, dryRun bool) ([]pipelineApproveResult, error) {
	rows, err := collectJobReadyRows(teamDir, pipeline, map[string]bool{"blocked": true})
	if err != nil {
		return nil, err
	}
	rows = filterPipelineApproveRows(rows, stepFilter)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	results := make([]pipelineApproveResult, 0, len(rows))
	for _, row := range rows {
		j, err := job.Read(teamDir, row.JobID)
		if err != nil {
			return nil, err
		}
		result := pipelineApproveResult{
			JobID:      j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			StepID:     row.StepID,
			Target:     row.Target,
			StepStatus: row.StepStatus,
			Instance:   row.Instance,
			Action:     "would_reject",
			DryRun:     dryRun,
			Message:    row.Message,
			WaitingFor: append([]string(nil), row.WaitingFor...),
		}
		if strings.TrimSpace(row.StepID) == "" {
			result.Action = "skipped"
			result.Message = "no blocked manual gate"
			results = append(results, result)
			continue
		}
		if len(row.WaitingFor) > 0 {
			result.Action = "skipped"
			result.Message = "waiting for " + strings.Join(row.WaitingFor, ",")
			results = append(results, result)
			continue
		}
		rejectionMessage := strings.TrimSpace(message)
		if rejectionMessage == "" {
			rejectionMessage = "rejected manual gate " + row.StepID
		}
		if err := updateJobStep(j, row.StepID, job.StatusFailed, jobStepUpdate{Message: rejectionMessage}); err != nil {
			return nil, err
		}
		j.LastEvent = "manual_gate_rejected"
		result.Job = j
		result.Message = j.LastStatus
		if idx := jobStepIndex(j, row.StepID); idx >= 0 {
			result.Step = cloneJobStep(&j.Steps[idx])
			result.StepID = j.Steps[idx].ID
			result.Target = j.Steps[idx].Target
			result.StepStatus = j.Steps[idx].Status
			result.Instance = j.Steps[idx].Instance
		}
		if dryRun {
			results = append(results, result)
			continue
		}
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": row.StepID}); err != nil {
			return nil, err
		}
		result.Action = "rejected"
		result.DryRun = false
		results = append(results, result)
	}
	return results, nil
}

func unblockPipelineJobs(teamDir, pipeline string, client sendClient, stepFilter, message, from string, next job.Status, limit int, allowMissing bool, dryRun bool) ([]pipelineUnblockResult, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	known := map[string]bool(nil)
	if !allowMissing {
		known, err = knownSendInstanceSet(client)
		if err != nil {
			return nil, err
		}
	}
	results := []pipelineUnblockResult{}
	for _, j := range jobs {
		if limit > 0 && len(results) >= limit {
			break
		}
		result, selected, err := previewPipelineJobUnblock(j, stepFilter, message, from, next, known, allowMissing, dryRun)
		if err != nil {
			return nil, err
		}
		if !selected {
			continue
		}
		if result.Action == "skipped" || dryRun {
			results = append(results, result)
			continue
		}
		response, err := client.SendMessage(result.Instance, from, message)
		if err != nil {
			return nil, err
		}
		if response != nil {
			result.MessageID = response.ID
			result.Delivered = response.Delivered
		}
		if err := writeJobWithAudit(teamDir, result.Job, "unblocked", from, message, pipelineUnblockEventData(result)); err != nil {
			return nil, err
		}
		result.Action = "unblocked"
		result.DryRun = false
		results = append(results, result)
	}
	return results, nil
}

func previewPipelineJobUnblock(j *job.Job, stepFilter, message, from string, next job.Status, known map[string]bool, allowMissing bool, dryRun bool) (pipelineUnblockResult, bool, error) {
	if j == nil {
		return pipelineUnblockResult{}, false, nil
	}
	selection, selected, skippedMessage, err := selectPipelineUnblockTarget(j, stepFilter)
	if err != nil {
		return pipelineUnblockResult{}, false, err
	}
	if !selected {
		return pipelineUnblockResult{}, false, nil
	}
	result := pipelineUnblockResult{
		JobID:        j.ID,
		Ticket:       j.Ticket,
		Pipeline:     j.Pipeline,
		StepID:       selection.StepID,
		StatusBefore: j.Status,
		StatusAfter:  next,
		Instance:     strings.TrimSpace(selection.Instance),
		Action:       "would_unblock",
		DryRun:       dryRun,
		Message:      message,
		From:         from,
	}
	if idx := jobStepIndex(j, selection.StepID); idx >= 0 {
		step := &j.Steps[idx]
		result.Target = step.Target
		result.StepStatus = step.Status
		result.Step = cloneJobStep(step)
	}
	if skippedMessage != "" {
		result.Action = "skipped"
		result.Message = skippedMessage
		return result, true, nil
	}
	if strings.TrimSpace(result.Instance) == "" {
		result.Action = "skipped"
		result.Message = "selected blocked work has no owning instance; dispatch or adopt it first"
		return result, true, nil
	}
	if !allowMissing && !known[result.Instance] {
		result.Action = "skipped"
		result.Message = "owning instance is not known to the daemon; pass --allow-missing to queue anyway"
		return result, true, nil
	}
	updated := clonePipelineUnblockJob(j)
	now := time.Now().UTC()
	updated.Status = next
	if strings.TrimSpace(selection.StepID) != "" {
		applySelectedJobStepStatus(updated, selection.StepID, next, now)
	}
	updated.LastEvent = "unblocked"
	updated.LastStatus = message
	updated.UpdatedAt = now
	result.Job = updated
	result.StepStatus = next
	if idx := jobStepIndex(updated, selection.StepID); idx >= 0 {
		result.Step = cloneJobStep(&updated.Steps[idx])
		result.Target = updated.Steps[idx].Target
		result.StepStatus = updated.Steps[idx].Status
		result.Instance = strings.TrimSpace(updated.Steps[idx].Instance)
	}
	return result, true, nil
}

func selectPipelineUnblockTarget(j *job.Job, stepFilter string) (jobInstanceSelection, bool, string, error) {
	stepFilter = strings.TrimSpace(stepFilter)
	if stepFilter != "" {
		idx := jobStepIndex(j, stepFilter)
		if idx < 0 {
			return jobInstanceSelection{}, false, "", nil
		}
		step := &j.Steps[idx]
		if j.Status != job.StatusBlocked && step.Status != job.StatusBlocked {
			return jobInstanceSelection{}, false, "", nil
		}
		if step.Gate == job.StepGateManual {
			return jobInstanceSelection{Instance: strings.TrimSpace(step.Instance), StepID: step.ID}, true, "blocked step is a manual gate; use pipeline approve", nil
		}
		if step.Gate == job.StepGatePR {
			return jobInstanceSelection{Instance: strings.TrimSpace(step.Instance), StepID: step.ID}, true, "blocked step is waiting for PR metadata", nil
		}
		selection, err := selectJobOwningInstance(j, stepFilter)
		return selection, true, "", err
	}
	if step := uniqueJobStepWithStatusAndInstance(j, job.StatusBlocked); step != nil {
		if step.Gate == job.StepGateManual {
			return jobInstanceSelection{Instance: strings.TrimSpace(step.Instance), StepID: step.ID}, true, "blocked step is a manual gate; use pipeline approve", nil
		}
		if step.Gate == job.StepGatePR {
			return jobInstanceSelection{Instance: strings.TrimSpace(step.Instance), StepID: step.ID}, true, "blocked step is waiting for PR metadata", nil
		}
		return jobInstanceSelection{Instance: strings.TrimSpace(step.Instance), StepID: step.ID}, true, "", nil
	}
	blockedSteps := blockedJobSteps(j)
	if len(blockedSteps) > 1 {
		return jobInstanceSelection{}, true, "multiple blocked steps; pass --step to choose one", nil
	}
	if len(blockedSteps) == 1 {
		step := blockedSteps[0]
		if step.Gate == job.StepGateManual {
			return jobInstanceSelection{Instance: strings.TrimSpace(step.Instance), StepID: step.ID}, true, "blocked step is a manual gate; use pipeline approve", nil
		}
		if step.Gate == job.StepGatePR {
			return jobInstanceSelection{Instance: strings.TrimSpace(step.Instance), StepID: step.ID}, true, "blocked step is waiting for PR metadata", nil
		}
		return jobInstanceSelection{Instance: strings.TrimSpace(step.Instance), StepID: step.ID}, true, "", nil
	}
	if j.Status != job.StatusBlocked {
		return jobInstanceSelection{}, false, "", nil
	}
	return jobInstanceSelection{Instance: strings.TrimSpace(j.Instance)}, true, "", nil
}

func blockedJobSteps(j *job.Job) []job.Step {
	if j == nil {
		return nil
	}
	steps := []job.Step{}
	for _, step := range j.Steps {
		if step.Status == job.StatusBlocked {
			steps = append(steps, step)
		}
	}
	return steps
}

func clonePipelineUnblockJob(j *job.Job) *job.Job {
	if j == nil {
		return nil
	}
	clone := *j
	clone.Steps = append([]job.Step(nil), j.Steps...)
	return &clone
}

func knownSendInstanceSet(client sendClient) (map[string]bool, error) {
	metas, err := client.Instances()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		known[strings.TrimSpace(meta.Instance)] = true
	}
	return known, nil
}

func pipelineUnblockEventData(result pipelineUnblockResult) map[string]string {
	data := map[string]string{
		"from":     strings.TrimSpace(result.From),
		"instance": strings.TrimSpace(result.Instance),
		"status":   string(result.StatusAfter),
	}
	if strings.TrimSpace(result.StepID) != "" {
		data["step"] = strings.TrimSpace(result.StepID)
	}
	return data
}

func skipPipelineSteps(teamDir, pipeline, stepID, message string, limit int, dryRun bool) ([]pipelineSkipResult, error) {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return nil, fmt.Errorf("step id is required")
	}
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(message)
	if reason == "" {
		reason = "skipped step " + stepID
	}
	results := []pipelineSkipResult{}
	for _, j := range jobs {
		if limit > 0 && len(results) >= limit {
			break
		}
		if j == nil {
			continue
		}
		idx := jobStepIndex(j, stepID)
		if idx < 0 {
			continue
		}
		step := &j.Steps[idx]
		if step.Status == job.StatusDone {
			continue
		}
		result := pipelineSkipResult{
			JobID:      j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			StepID:     step.ID,
			Target:     step.Target,
			StepStatus: step.Status,
			Instance:   step.Instance,
			Action:     "would_skip",
			DryRun:     dryRun,
			Message:    reason,
			Step:       cloneJobStep(step),
		}
		if step.Status == job.StatusRunning {
			result.Action = "skipped"
			result.Message = "step is running; timeout or stop the owner before skipping"
			results = append(results, result)
			continue
		}
		if err := updateJobStep(j, stepID, job.StatusDone, jobStepUpdate{Message: reason, Skip: true}); err != nil {
			return nil, err
		}
		result.Job = j
		result.Message = reason
		if idx := jobStepIndex(j, stepID); idx >= 0 {
			result.Step = cloneJobStep(&j.Steps[idx])
			result.StepID = j.Steps[idx].ID
			result.Target = j.Steps[idx].Target
			result.StepStatus = j.Steps[idx].Status
			result.Instance = j.Steps[idx].Instance
			result.Skipped = j.Steps[idx].Skipped
			result.SkipReason = j.Steps[idx].SkipReason
		}
		if dryRun {
			results = append(results, result)
			continue
		}
		if err := writeJobWithAudit(teamDir, j, "step_skipped", "cli", reason, map[string]string{"step": stepID}); err != nil {
			return nil, err
		}
		result.Action = "skipped"
		result.DryRun = false
		results = append(results, result)
	}
	return results, nil
}

func cancelPipelineJobs(teamDir, pipeline, message, actor string, limit int, dryRun bool) ([]pipelineCancelResult, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(message)
	if reason == "" {
		reason = "cancelled by operator"
	}
	cancelActor := strings.TrimSpace(actor)
	if cancelActor == "" {
		cancelActor = "cli"
	}
	results := []pipelineCancelResult{}
	for _, j := range jobs {
		if limit > 0 && len(results) >= limit {
			break
		}
		if j == nil || jobStatusTerminal(j.Status) {
			continue
		}
		statusBefore := j.Status
		cancelled := *j
		applyJobCancelUpdate(&cancelled, reason)
		result := pipelineCancelResult{
			JobID:        j.ID,
			Ticket:       j.Ticket,
			Pipeline:     j.Pipeline,
			StatusBefore: statusBefore,
			StatusAfter:  cancelled.Status,
			Instance:     j.Instance,
			Action:       "would_cancel",
			DryRun:       dryRun,
			Message:      reason,
			Job:          &cancelled,
		}
		if dryRun {
			results = append(results, result)
			continue
		}
		*j = cancelled
		data := map[string]string{
			"status":  string(j.Status),
			"message": reason,
		}
		if strings.TrimSpace(j.Instance) != "" {
			data["instance"] = strings.TrimSpace(j.Instance)
		}
		if err := writeJobWithAudit(teamDir, j, "cancelled", cancelActor, reason, data); err != nil {
			return nil, err
		}
		result.Action = "cancelled"
		result.DryRun = false
		result.Job = j
		results = append(results, result)
	}
	return results, nil
}

func filterPipelineApproveRows(rows []jobReadyRow, stepFilter string) []jobReadyRow {
	stepFilter = strings.TrimSpace(stepFilter)
	out := rows[:0]
	for _, row := range rows {
		if row.Gate != job.StepGateManual {
			continue
		}
		if stepFilter != "" && row.StepID != stepFilter {
			continue
		}
		out = append(out, row)
	}
	return out
}

func retryPipelineJobs(cmd *cobra.Command, teamDir, pipeline, workspace string, selection runtimeSelection, stepFilter, message string, limit int, force bool, dispatchNow, dryRun bool, previewRoutes bool) ([]pipelineRetryResult, error) {
	rows, err := collectJobReadyRows(teamDir, pipeline, map[string]bool{"failed": true})
	if err != nil {
		return nil, err
	}
	rows = filterPipelineRetryRowsByStep(rows, stepFilter)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	results := make([]pipelineRetryResult, 0, len(rows))
	for _, row := range rows {
		j, err := job.Read(teamDir, row.JobID)
		if err != nil {
			return nil, err
		}
		result := pipelineRetryResult{
			JobID:       j.ID,
			Ticket:      j.Ticket,
			Pipeline:    j.Pipeline,
			StepID:      row.StepID,
			Target:      row.Target,
			StepStatus:  row.StepStatus,
			Instance:    row.Instance,
			Attempts:    row.Attempts,
			MaxAttempts: row.MaxAttempts,
			Action:      "would_retry",
			DryRun:      dryRun,
			Message:     row.Message,
		}
		reset := resetFailedPipelineStepForRetryByIDWithReason(j, row.StepID, force)
		if reset.StepID == "" {
			result.Action = "skipped"
			result.Message = reset.Reason
			if reset.MaxAttempts > 0 {
				result.Attempts = reset.Attempts
				result.MaxAttempts = reset.MaxAttempts
			}
			results = append(results, result)
			continue
		}
		stepID := reset.StepID
		now := time.Now().UTC()
		j.Status = job.StatusQueued
		j.LastEvent = "reopened"
		j.LastStatus = "reopened step " + stepID + " for retry"
		if strings.TrimSpace(message) != "" {
			j.LastStatus = strings.TrimSpace(message)
		}
		j.UpdatedAt = now
		result.StepID = stepID
		if idx := jobStepIndex(j, stepID); idx >= 0 {
			result.Step = cloneJobStep(&j.Steps[idx])
			result.Target = j.Steps[idx].Target
			result.StepStatus = j.Steps[idx].Status
			result.Instance = j.Steps[idx].Instance
			result.Attempts = j.Steps[idx].Attempts
			result.MaxAttempts = j.Steps[idx].MaxAttempts
			if reset.Attempts > result.Attempts {
				result.Attempts = reset.Attempts
			}
			if reset.MaxAttempts > result.MaxAttempts {
				result.MaxAttempts = reset.MaxAttempts
			}
		}
		result.Job = j
		result.Message = j.LastStatus
		if dispatchNow {
			result.Action = "would_dispatch"
		}
		if dryRun {
			if dispatchNow {
				preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, selection)
				if err != nil {
					return nil, err
				}
				result.Preview = preview
				result.Message = preview.Message
				if preview.Step != nil {
					result.StepID = preview.Step.ID
					result.Target = preview.Step.Target
					result.StepStatus = preview.Step.Status
					result.Instance = preview.Step.Instance
					result.Attempts = preview.Step.Attempts
					result.MaxAttempts = preview.Step.MaxAttempts
					result.Step = preview.Step
				}
			}
			results = append(results, result)
			continue
		}
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": stepID}); err != nil {
			return nil, err
		}
		result.Action = "retried"
		result.DryRun = false
		if dispatchNow {
			advanced, err := advanceJob(cmd, teamDir, j, workspace, selection)
			if err != nil {
				return nil, err
			}
			result.Action = pipelineRetryDispatchAction(advanced)
			result.Job = advanced.Job
			result.Step = advanced.Step
			result.Event = advanced.Event
			result.Message = advanced.Message
			if advanced.Job != nil {
				result.Ticket = advanced.Job.Ticket
				result.Pipeline = advanced.Job.Pipeline
			}
			if advanced.Step != nil {
				result.StepID = advanced.Step.ID
				result.Target = advanced.Step.Target
				result.StepStatus = advanced.Step.Status
				result.Instance = advanced.Step.Instance
				result.Attempts = advanced.Step.Attempts
				result.MaxAttempts = advanced.Step.MaxAttempts
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func waitForPipelineRetryResults(cmd *cobra.Command, teamDir string, results []pipelineRetryResult, statuses map[job.Status]bool, events map[string]bool, timeout, interval time.Duration, prefix string) ([]pipelineRetryResult, error) {
	ids := make([]string, 0, len(results))
	seen := map[string]bool{}
	for _, result := range results {
		id := strings.TrimSpace(result.JobID)
		if id == "" || result.DryRun || result.Action == "skipped" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return results, nil
	}
	jobs := make([]*job.Job, 0, len(ids))
	for _, id := range ids {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	waited, err := runPipelineWait(ctx, teamDir, jobs, statuses, events, interval)
	if err != nil {
		if timeoutErr, ok := err.(*pipelineWaitTimeoutError); ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: timed out waiting for retried jobs to reach %s (pending=%s).\n",
				prefix, jobWaitConditionList(statuses, events), pipelineWaitPendingSummary(timeoutErr.Pending))
			return nil, exitErr(1)
		}
		return nil, err
	}
	waitedByID := make(map[string]*job.Job, len(waited))
	for _, j := range waited {
		if j != nil {
			waitedByID[j.ID] = j
		}
	}
	refreshed := append([]pipelineRetryResult(nil), results...)
	for i := range refreshed {
		if waitedJob := waitedByID[refreshed[i].JobID]; waitedJob != nil {
			refreshPipelineRetryResultAfterWait(&refreshed[i], waitedJob)
		}
	}
	return refreshed, nil
}

func refreshPipelineRetryResultAfterWait(result *pipelineRetryResult, waited *job.Job) {
	if result == nil || waited == nil {
		return
	}
	stepID := strings.TrimSpace(result.StepID)
	result.Job = waited
	result.JobID = waited.ID
	result.Ticket = waited.Ticket
	result.Pipeline = waited.Pipeline
	result.Message = waited.LastStatus
	if stepID == "" {
		return
	}
	idx := jobStepIndex(waited, stepID)
	if idx == -1 {
		result.Step = nil
		return
	}
	step := cloneJobStep(&waited.Steps[idx])
	result.Step = step
	result.StepID = step.ID
	result.Target = step.Target
	result.StepStatus = step.Status
	result.Instance = step.Instance
	result.Attempts = step.Attempts
	result.MaxAttempts = step.MaxAttempts
}

func pipelineRetryResultsHaveFailed(results []pipelineRetryResult) bool {
	for _, result := range results {
		if result.Job != nil && result.Job.Status == job.StatusFailed {
			return true
		}
	}
	return false
}

func filterPipelineRetryRowsByStep(rows []jobReadyRow, stepFilter string) []jobReadyRow {
	stepFilter = strings.TrimSpace(stepFilter)
	if stepFilter == "" {
		return rows
	}
	out := rows[:0]
	for _, row := range rows {
		if row.StepID == stepFilter {
			out = append(out, row)
		}
	}
	return out
}

func timeoutPipelineJobs(teamDir, pipeline, stepFilter, targetFilter, message string, limit int, dryRun bool) ([]pipelineTimeoutResult, error) {
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	sortJobs(jobs, "updated")
	now := time.Now().UTC()
	pipeline = strings.TrimSpace(pipeline)
	stepFilter = strings.TrimSpace(stepFilter)
	targetFilter = strings.TrimSpace(targetFilter)
	results := []pipelineTimeoutResult{}
	for _, j := range jobs {
		if j == nil || strings.TrimSpace(j.Pipeline) == "" {
			continue
		}
		if pipeline != "" && strings.TrimSpace(j.Pipeline) != pipeline {
			continue
		}
		batchLimit := 0
		if limit > 0 {
			batchLimit = limit - len(results)
		}
		timedOut, err := timeoutJobRunningSteps(teamDir, j, stepFilter, targetFilter, message, batchLimit, dryRun, now, staleAfter)
		if err != nil {
			return nil, err
		}
		results = append(results, timedOut...)
		if limit > 0 && len(results) >= limit {
			return results, nil
		}
	}
	return results, nil
}

func runPipelineRepair(cmd *cobra.Command, repo, teamDir, pipeline string, opts pipelineRepairOptions) (*pipelineRepairResult, error) {
	pipeline = strings.TrimSpace(pipeline)
	if pipeline == "" {
		return nil, fmt.Errorf("pipeline name is required")
	}
	before, err := collectPipelineStatusRows(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	result := &pipelineRepairResult{
		Pipeline:     pipeline,
		DryRun:       opts.DryRun,
		StatusBefore: before,
	}

	beforeDaemon := collectDaemonStatus(teamDir)
	result.Daemon = repairDaemonStepResult(beforeDaemon, repairOptions{
		DryRun:     opts.DryRun,
		SkipDaemon: opts.SkipDaemon,
	})
	if !opts.SkipDaemon && !opts.DryRun {
		if err := ensureDaemonReadyWithTimeout(cmd, repo, true, opts.ReadyTimeout); err != nil {
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
		retries, err := pipelineQueueRetryResults(teamDir, pipeline, filters, "state", opts.Limit, opts.DryRun)
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

	jobTimeout, err := runPipelineRepairJobTimeoutStep(teamDir, pipeline, opts)
	if err != nil {
		return nil, err
	}
	result.JobTimeout = jobTimeout

	pipelineTimeout, err := runPipelineRepairPipelineTimeoutStep(teamDir, pipeline, opts)
	if err != nil {
		return nil, err
	}
	result.PipelineTimeout = pipelineTimeout

	pipelineRetry, err := runPipelineRepairPipelineRetryStep(cmd, teamDir, pipeline, opts)
	if err != nil {
		return nil, err
	}
	result.PipelineRetry = pipelineRetry

	result.Advance = runPipelineRepairAdvanceStep(cmd, teamDir, pipeline, opts)
	if result.Advance.Action == "error" {
		return nil, fmt.Errorf("advance: %s", result.Advance.Reason)
	}

	if !opts.DryRun {
		after, err := collectPipelineStatusRows(teamDir, pipeline)
		if err != nil {
			return nil, err
		}
		result.StatusAfter = after
	}
	return result, nil
}

func runPipelineRepairPipelineRetryStep(cmd *cobra.Command, teamDir, pipeline string, opts pipelineRepairOptions) (repairPipelineRetryStep, error) {
	if !opts.RetryPipelines {
		return repairPipelineRetryStep{Action: "skipped", Reason: "--retry-pipelines not set"}, nil
	}
	message := strings.TrimSpace(opts.RetryMessage)
	if message == "" {
		message = "pipeline repair retry failed step"
	}
	results, err := retryPipelineJobs(cmd, teamDir, pipeline, opts.Workspace, opts.Runtime, opts.RetryStep, message, opts.Limit, opts.RetryForce, true, opts.DryRun, opts.PreviewRoutes)
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

func runPipelineRepairPipelineTimeoutStep(teamDir, pipeline string, opts pipelineRepairOptions) (repairPipelineTimeoutStep, error) {
	if !opts.TimeoutPipelines {
		return repairPipelineTimeoutStep{Action: "skipped", Reason: "--timeout-pipelines not set"}, nil
	}
	message := strings.TrimSpace(opts.TimeoutMessage)
	if message == "" {
		message = "pipeline repair timed out stale step"
	}
	results, err := timeoutPipelineJobs(teamDir, pipeline, opts.TimeoutStep, opts.TimeoutTarget, message, opts.Limit, opts.DryRun)
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

func runPipelineRepairJobTimeoutStep(teamDir, pipeline string, opts pipelineRepairOptions) (repairPipelineTimeoutStep, error) {
	if !opts.TimeoutJobs {
		return repairPipelineTimeoutStep{Action: "skipped", Reason: "--timeout-jobs not set"}, nil
	}
	message := strings.TrimSpace(opts.TimeoutMessage)
	if message == "" {
		message = "pipeline repair timed out stale job work"
	}
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return repairPipelineTimeoutStep{Action: "error", Reason: err.Error()}, err
	}
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return repairPipelineTimeoutStep{Action: "error", Reason: err.Error()}, err
	}
	results, err := timeoutStaleJobWork(teamDir, jobs, opts.TimeoutStep, opts.TimeoutTarget, message, opts.Limit, opts.DryRun, time.Now().UTC(), staleAfter)
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

func runPipelineRepairAdvanceStep(cmd *cobra.Command, teamDir, pipeline string, opts pipelineRepairOptions) pipelineRepairAdvanceStep {
	if opts.SkipAdvance {
		return pipelineRepairAdvanceStep{Action: "skipped", Reason: "--skip-advance set"}
	}
	status := collectDaemonStatus(teamDir)
	if !opts.DryRun && (!status.Running || !status.Ready) {
		return pipelineRepairAdvanceStep{Action: "skipped", Reason: "daemon is not running"}
	}
	results, err := advanceReadyPipelineJobs(cmd, teamDir, pipeline, opts.Workspace, opts.Runtime, opts.Limit, opts.DryRun, opts.PreviewRoutes, opts.AllReadySteps)
	if err != nil {
		return pipelineRepairAdvanceStep{Action: "error", Reason: err.Error()}
	}
	action := "advanced"
	if opts.DryRun {
		action = "would_advance"
	}
	if len(results) == 0 {
		action = "none"
	}
	return pipelineRepairAdvanceStep{Action: action, Results: results}
}

func waitForPipelineRepairResult(cmd *cobra.Command, teamDir string, result *pipelineRepairResult, statuses map[job.Status]bool, events map[string]bool, timeout, interval time.Duration) error {
	if result == nil {
		return nil
	}
	var err error
	result.PipelineRetry.Results, err = waitForPipelineRetryResults(cmd, teamDir, result.PipelineRetry.Results, statuses, events, timeout, interval, "agent-team pipeline repair")
	if err != nil {
		return err
	}
	result.Advance.Results, err = waitForPipelineAdvanceResults(cmd, teamDir, result.Advance.Results, statuses, events, timeout, interval, "agent-team pipeline repair")
	if err != nil {
		return err
	}
	if !result.DryRun {
		status, err := collectPipelineStatusRows(teamDir, result.Pipeline)
		if err != nil {
			return err
		}
		result.StatusAfter = status
	}
	return nil
}

func pipelineRepairResultHasFailed(result *pipelineRepairResult) bool {
	if result == nil {
		return false
	}
	return pipelineRetryResultsHaveFailed(result.PipelineRetry.Results) || pipelineAdvanceResultsHaveFailed(result.Advance.Results)
}

func runPipelineTick(cmd *cobra.Command, teamDir, pipeline, workspace string, limit int, opts tickOptions) (*pipelineTickResult, error) {
	pipeline = strings.TrimSpace(pipeline)
	if _, err := loadPipelineInfo(teamDir, pipeline); err != nil {
		return nil, err
	}
	result := &pipelineTickResult{
		Pipeline:  pipeline,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Tick: tickResult{
			DryRun: opts.DryRun,
		},
	}
	if !opts.SkipDrain {
		now := time.Now().UTC()
		filters, err := parseQueueListFilters(daemon.QueueStatePending, nil, nil, nil, false, now)
		if err != nil {
			return nil, err
		}
		items, err := collectPipelineQueueItems(teamDir, pipeline, filters, now)
		if err != nil {
			return nil, err
		}
		if opts.DryRun {
			top, err := topology.LoadFromTeamDir(teamDir)
			if err != nil {
				return nil, err
			}
			result.Tick.Queue = previewQueueDrainItems(top, items, now)
		} else {
			ids := queueItemIDsFromPointers(items)
			if len(ids) == 0 {
				result.Tick.Queue = &daemon.QueueDrainResult{Outcomes: []daemon.EventOutcome{}}
			} else {
				client, err := newDaemonClient(teamDir)
				if err != nil {
					return nil, err
				}
				queue, err := client.QueueDrainScoped(false, ids)
				if err != nil {
					return nil, err
				}
				result.Tick.Queue = queue
			}
		}
	}
	if !opts.SkipAdvance {
		advanced, err := advanceReadyPipelineJobs(cmd, teamDir, pipeline, workspace, opts.Runtime, limit, opts.DryRun, opts.PreviewRoutes, opts.AllReadySteps)
		if err != nil {
			return nil, err
		}
		result.Tick.Advance = advanced
	}
	return result, nil
}

func runPipelineDrainUntilIdle(ctx context.Context, cmd *cobra.Command, teamDir, pipeline, workspace string, limit int, opts tickOptions, maxCycles int, interval time.Duration) (*pipelineDrainResult, error) {
	if maxCycles <= 0 {
		maxCycles = 1
	}
	pipeline = strings.TrimSpace(pipeline)
	result := &pipelineDrainResult{Pipeline: pipeline, Cycles: []*pipelineTickResult{}}
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
		tick, err := runPipelineTick(cmd, teamDir, pipeline, workspace, limit, opts)
		if err != nil {
			result.CyclesRun = len(result.Cycles)
			return result, err
		}
		if result.Pipeline == "" {
			result.Pipeline = tick.Pipeline
		}
		result.Cycles = append(result.Cycles, tick)
		if pipelineTickResultIsIdle(tick) {
			result.Idle = true
			break
		}
	}
	result.CyclesRun = len(result.Cycles)
	result.HitLimit = !result.Idle && result.CyclesRun >= maxCycles
	return result, nil
}

func pipelineTickResultIsIdle(result *pipelineTickResult) bool {
	if result == nil {
		return true
	}
	return tickResultIsIdle(&result.Tick)
}

func waitForPipelineDrainResult(cmd *cobra.Command, teamDir string, result *pipelineDrainResult, statuses map[job.Status]bool, events map[string]bool, timeout, interval time.Duration) error {
	if result == nil {
		return nil
	}
	for _, cycle := range result.Cycles {
		if cycle == nil {
			continue
		}
		if err := waitForTickResultAdvanceRows(cmd, teamDir, &cycle.Tick, statuses, events, timeout, interval, "agent-team pipeline drain"); err != nil {
			return err
		}
	}
	return nil
}

func pipelineDrainResultHasFailed(result *pipelineDrainResult) bool {
	if result == nil {
		return false
	}
	for _, cycle := range result.Cycles {
		if cycle != nil && tickResultAdvanceRowsHaveFailed(&cycle.Tick) {
			return true
		}
	}
	return false
}

func timeoutJobRunningSteps(teamDir string, j *job.Job, stepFilter, targetFilter, message string, limit int, dryRun bool, now time.Time, staleAfter time.Duration) ([]pipelineTimeoutResult, error) {
	if j == nil {
		return nil, nil
	}
	stepFilter = strings.TrimSpace(stepFilter)
	targetFilter = strings.TrimSpace(targetFilter)
	results := []pipelineTimeoutResult{}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != job.StatusRunning {
			continue
		}
		if stepFilter != "" && step.ID != stepFilter {
			continue
		}
		if targetFilter != "" && step.Target != targetFilter {
			continue
		}
		timeout := pipelineStepStaleAfter(step, staleAfter)
		if !pipelineRunningStepIsStale(step, now, staleAfter) {
			continue
		}
		age := now.Sub(step.StartedAt)
		result := pipelineTimeoutResult{
			JobID:      j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			StepID:     step.ID,
			Target:     step.Target,
			StepStatus: step.Status,
			Instance:   step.Instance,
			Action:     "would_fail",
			DryRun:     dryRun,
			Age:        roundedDurationString(age),
			Timeout:    roundedDurationString(timeout),
			Message:    pipelineTimeoutMessage(step.ID, age, timeout, message),
			Step:       cloneJobStep(step),
		}
		if dryRun {
			results = append(results, result)
			if limit > 0 && len(results) >= limit {
				return results, nil
			}
			continue
		}
		if err := updateJobStep(j, step.ID, job.StatusFailed, jobStepUpdate{Message: result.Message}); err != nil {
			return nil, err
		}
		if idx := jobStepIndex(j, step.ID); idx >= 0 {
			j.Steps[idx].Instance = ""
			result.Step = cloneJobStep(&j.Steps[idx])
			result.StepStatus = j.Steps[idx].Status
			result.Instance = j.Steps[idx].Instance
		}
		data := map[string]string{
			"step":    result.StepID,
			"age":     result.Age,
			"timeout": result.Timeout,
		}
		if err := writeJobWithAudit(teamDir, j, "step_timeout", "cli", result.Message, data); err != nil {
			return nil, err
		}
		result.Action = "failed"
		result.DryRun = false
		result.Job = j
		results = append(results, result)
		if limit > 0 && len(results) >= limit {
			return results, nil
		}
	}
	return results, nil
}

func pipelineTimeoutMessage(stepID string, age, timeout time.Duration, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	return fmt.Sprintf("timed out step %s after %s (threshold %s)", stepID, roundedDurationString(age), roundedDurationString(timeout))
}

func roundedDurationString(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.Round(time.Second).String()
}

func filterAdvanceablePipelineRows(rows []jobReadyRow) []jobReadyRow {
	out := rows[:0]
	for _, row := range rows {
		if row.State == "ready" {
			out = append(out, row)
			continue
		}
		if row.State == "queued" && len(row.WaitingFor) == 0 && strings.TrimSpace(row.Instance) == "" {
			out = append(out, row)
		}
	}
	return out
}

func pipelineAdvanceAction(result *jobAdvanceResult) string {
	if result == nil {
		return "skipped"
	}
	if strings.TrimSpace(result.Message) != "" && result.Step == nil && result.Event == nil {
		return "skipped"
	}
	return "advanced"
}

func pipelineRetryDispatchAction(result *jobAdvanceResult) string {
	if pipelineAdvanceAction(result) == "advanced" {
		return "dispatched"
	}
	return "retried"
}

func pipelineApproveDispatchAction(result *jobAdvanceResult) string {
	if pipelineAdvanceAction(result) == "advanced" {
		return "dispatched"
	}
	return "approved"
}

func renderPipelineList(w io.Writer, pipelines []pipelineInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(pipelines)
	}
	if tmpl != nil {
		for _, info := range pipelines {
			if err := renderPipelineInfoFormat(w, info, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(pipelines) == 0 {
		fmt.Fprintln(w, "(no pipelines declared)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tTRIGGER\tSTEPS")
	for _, info := range pipelines {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", info.Name, summariseTriggerMap(info.Trigger), summarisePipelineInfoSteps(info.Steps))
	}
	_ = tw.Flush()
	return nil
}

func renderPipelineDetail(w io.Writer, info pipelineInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(info)
	}
	if tmpl != nil {
		return renderPipelineInfoFormat(w, info, tmpl)
	}
	fmt.Fprintf(w, "Pipeline: %s\n", info.Name)
	fmt.Fprintf(w, "Trigger:  %s\n", summariseTriggerMap(info.Trigger))
	if len(info.Steps) == 0 {
		fmt.Fprintln(w, "Steps:    -")
		return nil
	}
	fmt.Fprintln(w, "Steps:")
	for _, step := range info.Steps {
		after := "-"
		if len(step.After) > 0 {
			after = strings.Join(step.After, ",")
		}
		gate := ""
		if step.Gate != "" {
			gate = " gate=" + step.Gate
		}
		optional := ""
		if step.Optional {
			optional = " optional=true"
		}
		timeout := ""
		if step.Timeout != "" {
			timeout = " timeout=" + step.Timeout
		}
		maxAttempts := ""
		if step.MaxAttempts > 0 {
			maxAttempts = fmt.Sprintf(" max_attempts=%d", step.MaxAttempts)
		}
		label := ""
		if step.Label != "" {
			label = fmt.Sprintf(" label=%q", step.Label)
		}
		description := ""
		if step.Description != "" {
			description = fmt.Sprintf(" description=%q", step.Description)
		}
		instructions := ""
		if step.Instructions != "" {
			instructions = fmt.Sprintf(" instructions=%q", step.Instructions)
		}
		workspace := ""
		if step.Workspace != "" {
			workspace = " workspace=" + step.Workspace
		}
		runtime := ""
		if formatted := formatStepRuntime(step.Runtime, step.RuntimeBin); formatted != "" {
			runtime = " runtime=" + formatted
		}
		fmt.Fprintf(w, "  %s target=%s after=%s%s%s%s%s%s%s%s%s%s\n", step.ID, step.Target, after, workspace, runtime, label, description, instructions, gate, optional, timeout, maxAttempts)
	}
	return nil
}

type pipelineGraphFormat string

const (
	pipelineGraphText    pipelineGraphFormat = "text"
	pipelineGraphMermaid pipelineGraphFormat = "mermaid"
	pipelineGraphDOT     pipelineGraphFormat = "dot"
)

func parsePipelineGraphFormat(raw string) (pipelineGraphFormat, error) {
	format := pipelineGraphFormat(strings.ToLower(strings.TrimSpace(raw)))
	if format == "" {
		return pipelineGraphText, nil
	}
	switch format {
	case pipelineGraphText, pipelineGraphMermaid, pipelineGraphDOT:
		return format, nil
	default:
		return "", fmt.Errorf("--format must be text, mermaid, or dot")
	}
}

func renderPipelineGraph(w io.Writer, graph pipelineGraph, format pipelineGraphFormat, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(graph)
	}
	switch format {
	case pipelineGraphMermaid:
		renderPipelineGraphMermaid(w, graph)
	case pipelineGraphDOT:
		renderPipelineGraphDOT(w, graph)
	default:
		renderPipelineGraphText(w, graph)
	}
	return nil
}

func renderPipelineGraphText(w io.Writer, graph pipelineGraph) {
	fmt.Fprintf(w, "Pipeline: %s\n", graph.Name)
	fmt.Fprintf(w, "Trigger:  %s\n", graph.Summary)
	if len(graph.Nodes) == 0 {
		fmt.Fprintln(w, "Steps:    -")
		return
	}
	fmt.Fprintln(w, "Steps:")
	for _, node := range graph.Nodes {
		after := "-"
		if len(node.After) > 0 {
			after = strings.Join(node.After, ",")
		}
		routes := ""
		if len(node.Routes) > 0 {
			routes = " routes=" + strings.Join(node.Routes, ",")
		}
		gate := ""
		if node.Gate != "" {
			gate = " gate=" + node.Gate
		}
		optional := ""
		if node.Optional {
			optional = " optional=true"
		}
		timeout := ""
		if node.Timeout != "" {
			timeout = " timeout=" + node.Timeout
		}
		maxAttempts := ""
		if node.MaxAttempts > 0 {
			maxAttempts = fmt.Sprintf(" max_attempts=%d", node.MaxAttempts)
		}
		label := ""
		if node.Label != "" {
			label = fmt.Sprintf(" label=%q", node.Label)
		}
		description := ""
		if node.Description != "" {
			description = fmt.Sprintf(" description=%q", node.Description)
		}
		instructions := ""
		if node.Instructions != "" {
			instructions = fmt.Sprintf(" instructions=%q", node.Instructions)
		}
		workspace := ""
		if node.Workspace != "" {
			workspace = " workspace=" + node.Workspace
		}
		runtime := ""
		if formatted := formatStepRuntime(node.Runtime, node.RuntimeBin); formatted != "" {
			runtime = " runtime=" + formatted
		}
		missing := ""
		if node.Missing {
			missing = " missing=true"
		}
		fmt.Fprintf(w, "  %s target=%s after=%s%s%s%s%s%s%s%s%s%s%s%s\n", node.ID, node.Target, after, workspace, runtime, label, description, instructions, gate, optional, timeout, maxAttempts, routes, missing)
	}
	if len(graph.Edges) == 0 {
		return
	}
	fmt.Fprintln(w, "Edges:")
	for _, edge := range graph.Edges {
		fmt.Fprintf(w, "  %s -> %s\n", edge.From, edge.To)
	}
}

func renderPipelineGraphMermaid(w io.Writer, graph pipelineGraph) {
	fmt.Fprintln(w, "flowchart TD")
	fmt.Fprintf(w, "  trigger[%q]\n", pipelineMermaidLabel("trigger: "+graph.Summary))
	nodeIDs := map[string]string{}
	for i, node := range graph.Nodes {
		id := pipelineGraphNodeMermaidID(node.ID, i)
		nodeIDs[node.ID] = id
		fmt.Fprintf(w, "  %s[%q]\n", id, pipelineMermaidLabel(pipelineGraphNodeLabel(node, "<br/>")))
	}
	for _, edge := range graph.Edges {
		from := "trigger"
		if edge.From != "<trigger>" {
			from = nodeIDs[edge.From]
			if from == "" {
				from = pipelineGraphMermaidID(edge.From)
			}
		}
		to := nodeIDs[edge.To]
		if to == "" {
			to = pipelineGraphMermaidID(edge.To)
		}
		fmt.Fprintf(w, "  %s --> %s\n", from, to)
	}
}

func renderPipelineGraphDOT(w io.Writer, graph pipelineGraph) {
	fmt.Fprintf(w, "digraph %q {\n", graph.Name)
	fmt.Fprintln(w, "  rankdir=TB;")
	fmt.Fprintf(w, "  %q [label=%q, shape=oval];\n", "trigger", "trigger: "+graph.Summary)
	for _, node := range graph.Nodes {
		fmt.Fprintf(w, "  %q [label=%q];\n", node.ID, pipelineGraphNodeLabel(node, "\n"))
	}
	for _, edge := range graph.Edges {
		from := edge.From
		if from == "<trigger>" {
			from = "trigger"
		}
		fmt.Fprintf(w, "  %q -> %q;\n", from, edge.To)
	}
	fmt.Fprintln(w, "}")
}

func pipelineGraphNodeLabel(node pipelineGraphNode, sep string) string {
	parts := []string{node.ID}
	if node.Label != "" {
		parts = append(parts, "label: "+node.Label)
	}
	if node.Description != "" {
		parts = append(parts, "description: "+node.Description)
	}
	if node.Target != "" {
		parts = append(parts, "target: "+node.Target)
	}
	if node.Workspace != "" {
		parts = append(parts, "workspace: "+node.Workspace)
	}
	if runtime := formatStepRuntime(node.Runtime, node.RuntimeBin); runtime != "" {
		parts = append(parts, "runtime: "+runtime)
	}
	if len(node.Routes) > 0 {
		parts = append(parts, "routes: "+strings.Join(node.Routes, ","))
	}
	if node.Gate != "" {
		parts = append(parts, "gate: "+node.Gate)
	}
	if node.Optional {
		parts = append(parts, "optional")
	}
	if node.Timeout != "" {
		parts = append(parts, "timeout: "+node.Timeout)
	}
	if node.MaxAttempts > 0 {
		parts = append(parts, fmt.Sprintf("max attempts: %d", node.MaxAttempts))
	}
	if node.Missing {
		parts = append(parts, "missing dependency")
	}
	return strings.Join(parts, sep)
}

func pipelineGraphNodeMermaidID(id string, index int) string {
	return fmt.Sprintf("step_%d_%s", index+1, pipelineGraphMermaidID(id))
}

func pipelineGraphMermaidID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "node"
	}
	var b strings.Builder
	for i, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "node"
	}
	return out
}

func pipelineMermaidLabel(label string) string {
	return strings.ReplaceAll(label, `"`, "&quot;")
}

func parsePipelineInfoFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-info-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderPipelineInfoFormat(w io.Writer, info pipelineInfo, tmpl *template.Template) error {
	if err := tmpl.Execute(w, info); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderPipelineDoctor(stdout, stderr io.Writer, result *pipelineDoctorResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &pipelineDoctorResult{OK: true}
	}
	if jsonOut {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	if tmpl != nil {
		if err := renderPipelineDoctorFormat(stdout, result, tmpl); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	label := "pipelines"
	if len(result.Pipelines) == 1 {
		label = result.Pipelines[0].Name
	}
	if result.OK {
		if len(result.Pipelines) == 1 {
			fmt.Fprintf(stdout, "agent-team pipeline doctor: OK (%s)\n", label)
		} else {
			fmt.Fprintf(stdout, "agent-team pipeline doctor: OK (%d pipelines)\n", len(result.Pipelines))
		}
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
		}
		return nil
	}
	if len(result.Pipelines) == 1 {
		fmt.Fprintf(stderr, "agent-team pipeline doctor: problems found for %s:\n", label)
	} else {
		fmt.Fprintln(stderr, "agent-team pipeline doctor: problems found:")
	}
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "  - %s\n", problem.Message)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
	}
	return exitErr(1)
}

func parsePipelineDoctorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderPipelineDoctorFormat(w io.Writer, result *pipelineDoctorResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func summarisePipelineInfoSteps(steps []pipelineStepInfo) string {
	if len(steps) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		gate := ""
		if step.Gate != "" {
			gate = " gate=" + step.Gate
		}
		optional := ""
		if step.Optional {
			optional = " optional=true"
		}
		timeout := ""
		if step.Timeout != "" {
			timeout = " timeout=" + step.Timeout
		}
		maxAttempts := ""
		if step.MaxAttempts > 0 {
			maxAttempts = fmt.Sprintf(" max_attempts=%d", step.MaxAttempts)
		}
		workspace := ""
		if step.Workspace != "" {
			workspace = " workspace=" + step.Workspace
		}
		runtime := ""
		if formatted := formatStepRuntime(step.Runtime, step.RuntimeBin); formatted != "" {
			runtime = " runtime=" + formatted
		}
		label := ""
		if step.Label != "" {
			label = fmt.Sprintf(" label=%q", step.Label)
		}
		if len(step.After) > 0 {
			parts = append(parts, fmt.Sprintf("%s:%s%s%s%s after=%s%s%s%s%s", step.ID, step.Target, workspace, runtime, label, strings.Join(step.After, ","), gate, optional, timeout, maxAttempts))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%s%s%s%s%s%s%s%s", step.ID, step.Target, workspace, runtime, label, gate, optional, timeout, maxAttempts))
		}
	}
	return strings.Join(parts, " -> ")
}

func formatPipelineStepTimeout(timeout time.Duration) string {
	if timeout <= 0 {
		return ""
	}
	return timeout.String()
}

func parsePipelineAdvanceFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-advance-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineApproveFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-approve-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineUnblockFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-unblock-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineSkipFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-skip-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineCancelFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-cancel-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineRetryFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-retry-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineTimeoutFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-timeout-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineHoldFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-hold-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineStatusFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-status-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineExplainFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-explain-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parsePipelineNextFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-next-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderPipelineStatusRows(w io.Writer, rows []pipelineStatusRow, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if tmpl != nil {
		for _, row := range rows {
			if err := tmpl.Execute(w, row); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineStatusTable(w, rows)
	return nil
}

func runPipelineStatus(w io.Writer, teamDir, pipeline, sortMode string, limit int, jsonOut bool, tmpl *template.Template) error {
	rows, err := collectPipelineStatusRows(teamDir, pipeline)
	if err != nil {
		return err
	}
	rows = applyPipelineStatusRowOptions(rows, sortMode, limit)
	return renderPipelineStatusRows(w, rows, jsonOut, tmpl)
}

func runPipelineStatusWatch(ctx context.Context, w io.Writer, teamDir, pipeline, sortMode string, limit int, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runPipelineStatus(w, teamDir, pipeline, sortMode, limit, jsonOut, tmpl); err != nil {
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

func renderPipelineStatusTable(w io.Writer, rows []pipelineStatusRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no pipelines)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tDECLARED\tSTEPS\tJOBS\tJOB_STATUS\tREADY\tQUEUED\tRUNNING\tSTALE_RUNNING\tBLOCKED\tMANUAL_GATES\tFAILED\tHELD\tDONE\tNONE\tQUEUE\tACTION")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n",
			row.Pipeline,
			yesNo(row.Declared),
			row.Steps,
			row.Jobs,
			pipelineStatusJobSummary(row),
			row.ReadySteps,
			row.QueuedSteps,
			row.RunningSteps,
			row.StaleRunningSteps,
			row.BlockedSteps,
			row.ManualGates,
			row.FailedSteps,
			row.HeldSteps,
			row.DoneSteps,
			row.NoStep,
			pipelineStatusQueueSummary(row),
			emptyDash(strings.Join(row.Actions, "; ")),
		)
	}
	_ = tw.Flush()
}

func pipelineRepairStatusSummary(rows []pipelineStatusRow) string {
	if len(rows) == 0 {
		return "unavailable"
	}
	row := rows[0]
	parts := []string{
		fmt.Sprintf("jobs=%d", row.Jobs),
		fmt.Sprintf("ready=%d", row.ReadySteps),
		fmt.Sprintf("running=%d", row.RunningSteps),
		fmt.Sprintf("blocked=%d", row.BlockedSteps),
		fmt.Sprintf("failed=%d", row.FailedSteps),
	}
	if row.StaleRunningSteps > 0 {
		parts = append(parts, fmt.Sprintf("stale=%d", row.StaleRunningSteps))
	}
	if queue := pipelineStatusQueueSummary(row); queue != "-" {
		parts = append(parts, "queue="+queue)
	}
	return strings.Join(parts, " ")
}

func pipelineStatusQueueSummary(row pipelineStatusRow) string {
	parts := []string{}
	if row.QueuePending > 0 {
		parts = append(parts, fmt.Sprintf("pending=%d", row.QueuePending))
	}
	if row.QueueDead > 0 {
		parts = append(parts, fmt.Sprintf("dead=%d", row.QueueDead))
	}
	if row.QueueQuarantined > 0 {
		part := fmt.Sprintf("quarantined=%d", row.QueueQuarantined)
		if row.QueueRestorable > 0 || row.QueueUnrestorable > 0 {
			part = fmt.Sprintf("%s(restorable=%d,unrestorable=%d)", part, row.QueueRestorable, row.QueueUnrestorable)
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func parsePipelineTickFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-tick-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderPipelineTickCommandResult(w io.Writer, result *pipelineTickResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &pipelineTickResult{}
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
	return renderPipelineTickResult(w, result)
}

func parsePipelineDrainFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("pipeline-drain-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderPipelineDrainResult(w io.Writer, result *pipelineDrainResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &pipelineDrainResult{Cycles: []*pipelineTickResult{}}
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
		if err := renderPipelineTickResult(w, cycle); err != nil {
			return err
		}
	}
	if len(result.Cycles) > 0 {
		fmt.Fprintln(w)
	}
	if result.Idle {
		fmt.Fprintf(w, "pipeline drain: idle after %d cycle(s)\n", result.CyclesRun)
	} else if result.HitLimit {
		fmt.Fprintf(w, "pipeline drain: hit max cycles (%d) before idle\n", result.CyclesRun)
	} else {
		fmt.Fprintf(w, "pipeline drain: stopped after %d cycle(s)\n", result.CyclesRun)
	}
	return nil
}

func renderPipelineTickResult(w io.Writer, result *pipelineTickResult) error {
	if result == nil {
		result = &pipelineTickResult{}
	}
	if result.Tick.DryRun {
		fmt.Fprintln(w, "Dry run: true")
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Pipeline: %s\n", result.Pipeline)
	if result.CheckedAt != "" {
		fmt.Fprintf(w, "Checked:  %s\n", result.CheckedAt)
	}
	if result.Tick.Queue != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Queue:")
		if err := renderQueueDrainResult(w, result.Tick.Queue, false, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Queue: skipped")
	}
	if result.Tick.Advance != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Pipeline advance:")
		return renderPipelineAdvanceResults(w, result.Tick.Advance, false, nil)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Pipeline advance: skipped")
	return nil
}

func renderPipelineRepairResult(w io.Writer, result *pipelineRepairResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &pipelineRepairResult{}
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
	fmt.Fprintf(w, "Pipeline: %s\n", result.Pipeline)
	fmt.Fprintf(w, "Dry run: %t\n", result.DryRun)
	fmt.Fprintf(w, "Status before: %s\n", pipelineRepairStatusSummary(result.StatusBefore))
	renderRepairDaemonStep(w, result.Daemon)
	renderRepairQueueStep(w, result.Queue)
	renderRepairJobTimeoutStep(w, result.JobTimeout)
	renderRepairPipelineTimeoutStep(w, result.PipelineTimeout)
	if err := renderRepairPipelineRetryStep(w, result.PipelineRetry); err != nil {
		return err
	}
	if err := renderPipelineRepairAdvanceStep(w, result.Advance); err != nil {
		return err
	}
	if len(result.StatusAfter) > 0 {
		fmt.Fprintf(w, "Status after: %s\n", pipelineRepairStatusSummary(result.StatusAfter))
	}
	return nil
}

func renderPipelineRepairAdvanceStep(w io.Writer, step pipelineRepairAdvanceStep) error {
	fmt.Fprintf(w, "Advance: %s", step.Action)
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	fmt.Fprintln(w)
	if len(step.Results) == 0 {
		return nil
	}
	return renderPipelineAdvanceResults(w, step.Results, false, nil)
}

func renderPipelineExplainRows(w io.Writer, rows []pipelineExplainRow, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if tmpl != nil {
		for _, row := range rows {
			if err := tmpl.Execute(w, row); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no pipelines)")
		return nil
	}
	for idx, row := range rows {
		if idx > 0 {
			fmt.Fprintln(w)
		}
		renderPipelineExplainRow(w, row)
	}
	return nil
}

func runPipelineExplain(w io.Writer, teamDir, pipeline string, limit int, stateFilter map[string]bool, step string, jsonOut bool, tmpl *template.Template) error {
	rows, err := collectPipelineExplainRows(teamDir, pipeline, limit, stateFilter, step)
	if err != nil {
		return err
	}
	return renderPipelineExplainRows(w, rows, jsonOut, tmpl)
}

func runPipelineExplainWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, limit int, stateFilter map[string]bool, step string, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runPipelineExplain(w, teamDir, pipeline, limit, stateFilter, step, jsonOut, tmpl); err != nil {
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

func renderPipelineExplainRow(w io.Writer, row pipelineExplainRow) {
	fmt.Fprintf(w, "Pipeline: %s declared=%s jobs=%d explained=%d\n",
		row.Pipeline, yesNo(row.Declared), row.TotalJobs, row.ExplainedJobs)
	statusSummary := pipelineStatusJobSummary(row.Status)
	if statusSummary == "-" {
		statusSummary = "none"
	}
	fmt.Fprintf(w, "Status: jobs=%s ready=%d queued=%d running=%d blocked=%d manual_gates=%d failed=%d held=%d done=%d none=%d\n",
		statusSummary,
		row.Status.ReadySteps,
		row.Status.QueuedSteps,
		row.Status.RunningSteps,
		row.Status.BlockedSteps,
		row.Status.ManualGates,
		row.Status.FailedSteps,
		row.Status.HeldSteps,
		row.Status.DoneSteps,
		row.Status.NoStep)
	if len(row.Actions) > 0 {
		fmt.Fprintln(w, "Actions:")
		for _, action := range row.Actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	if row.TotalJobs == 0 {
		fmt.Fprintln(w, "Jobs: none")
		return
	}
	if row.ExplainedJobs == 0 {
		fmt.Fprintln(w, "Jobs: none selected")
		return
	}
	if row.Truncated {
		fmt.Fprintf(w, "Jobs: showing %d of %d\n", row.ExplainedJobs, row.TotalJobs)
	} else {
		fmt.Fprintln(w, "Jobs:")
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tTICKET\tSTATUS\tSTATE\tNEXT\tWAITING_FOR\tACTION")
	for _, explained := range row.Jobs {
		next := emptyDash(explained.Next.StepID)
		action := "-"
		if len(explained.Actions) > 0 {
			action = strings.Join(explained.Actions, "; ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			explained.JobID,
			emptyDash(explained.Ticket),
			explained.JobStatus,
			emptyDash(explained.State),
			next,
			listDash(explained.Next.WaitingFor),
			action)
	}
	_ = tw.Flush()
	fmt.Fprintln(w, "Steps:")
	stepWriter := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(stepWriter, "JOB\tSTEP\tTARGET\tSTATUS\tSTATE\tAFTER\tGATE\tOPTIONAL\tWAITING_FOR\tACTION")
	for _, explained := range row.Jobs {
		for _, step := range explained.Steps {
			action := "-"
			if len(step.Actions) > 0 {
				action = strings.Join(step.Actions, "; ")
			}
			fmt.Fprintf(stepWriter, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				explained.JobID,
				step.ID,
				emptyDash(step.Target),
				step.Status,
				emptyDash(step.State),
				listDash(step.After),
				emptyDash(step.Gate),
				yesNo(step.Optional),
				listDash(step.WaitingFor),
				action)
		}
	}
	_ = stepWriter.Flush()
}

func renderPipelineNextActions(w io.Writer, actions []pipelineNextAction, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(actions)
	}
	if tmpl != nil {
		for _, action := range actions {
			if err := tmpl.Execute(w, action); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineNextActionTable(w, actions)
	return nil
}

func runPipelineNext(w io.Writer, teamDir, pipeline, teamName, sortMode string, limit int, reasonFilters []string, jsonOut bool, tmpl *template.Template) error {
	var (
		rows []pipelineStatusRow
		err  error
	)
	if strings.TrimSpace(teamName) != "" {
		rows, err = collectTeamPipelineStatus(teamDir, teamName)
		if err == nil && pipeline != "" {
			rows, err = filterPipelineNextRowsForPipeline(rows, pipeline, teamName)
		}
	} else {
		rows, err = collectPipelineStatusRows(teamDir, pipeline)
	}
	if err != nil {
		return err
	}
	sortPipelineStatusRows(rows, sortMode)
	return renderPipelineNextActions(w, pipelineNextActionsFromStatus(rows, limit, reasonFilters), jsonOut, tmpl)
}

func runPipelineNextWatch(ctx context.Context, w io.Writer, teamDir, pipeline, teamName, sortMode string, limit int, reasonFilters []string, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runPipelineNext(w, teamDir, pipeline, teamName, sortMode, limit, reasonFilters, jsonOut, tmpl); err != nil {
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

func renderPipelineNextActionTable(w io.Writer, actions []pipelineNextAction) {
	if len(actions) == 0 {
		fmt.Fprintln(w, "(no pipeline actions)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tREASON\tACTION")
	for _, action := range actions {
		fmt.Fprintf(tw, "%s\t%s\t%s\n",
			action.Pipeline,
			emptyDash(action.Reason),
			action.Action)
	}
	_ = tw.Flush()
}

func pipelineStatusJobSummary(row pipelineStatusRow) string {
	parts := []string{}
	if row.Queued > 0 {
		parts = append(parts, fmt.Sprintf("queued=%d", row.Queued))
	}
	if row.Running > 0 {
		parts = append(parts, fmt.Sprintf("running=%d", row.Running))
	}
	if row.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("blocked=%d", row.Blocked))
	}
	if row.Done > 0 {
		parts = append(parts, fmt.Sprintf("done=%d", row.Done))
	}
	if row.Failed > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", row.Failed))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func renderPipelineAdvanceResults(w io.Writer, results []pipelineAdvanceResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineAdvanceTable(w, results)
	return renderPipelineAdvanceRoutePreviews(w, results)
}

func renderPipelineAdvanceTable(w io.Writer, results []pipelineAdvanceResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no ready pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tSTEP\tTARGET\tACTION\tSTATUS\tINSTANCE\tMESSAGE")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
			emptyDash(result.StepID),
			emptyDash(result.Target),
			result.Action,
			emptyDash(string(result.StepStatus)),
			emptyDash(result.Instance),
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}

func renderPipelineAdvanceRoutePreviews(w io.Writer, results []pipelineAdvanceResult) error {
	wroteHeader := false
	for _, result := range results {
		if result.Preview == nil {
			continue
		}
		if !wroteHeader {
			fmt.Fprintln(w, "Routes:")
			wroteHeader = true
		}
		requestedName := ""
		if result.Preview.Dispatch != nil {
			requestedName = result.Preview.Dispatch.RequestedName
		}
		fmt.Fprintf(w, "%s step=%s target=%s instance=%s\n",
			result.JobID,
			emptyDash(result.StepID),
			emptyDash(result.Target),
			emptyDash(requestedName),
		)
		if result.Preview.Dispatch == nil || result.Preview.Dispatch.Preview == nil || !eventPublishPreviewHasRoutes(result.Preview.Dispatch.Preview) {
			fmt.Fprintln(w, "(no triggers matched)")
			continue
		}
		if err := renderEventPublishRoutePreview(w, result.Preview.Dispatch.Preview); err != nil {
			return err
		}
	}
	return nil
}

func renderPipelineApproveResults(w io.Writer, results []pipelineApproveResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineApproveTable(w, results)
	return renderPipelineApproveRoutePreviews(w, results)
}

func renderPipelineApproveTable(w io.Writer, results []pipelineApproveResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no blocked manual pipeline gates)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tSTEP\tTARGET\tACTION\tSTATUS\tINSTANCE\tWAITING_FOR\tMESSAGE")
	for _, result := range results {
		waiting := "-"
		if len(result.WaitingFor) > 0 {
			waiting = strings.Join(result.WaitingFor, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
			emptyDash(result.StepID),
			emptyDash(result.Target),
			result.Action,
			emptyDash(string(result.StepStatus)),
			emptyDash(result.Instance),
			waiting,
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}

func renderPipelineApproveRoutePreviews(w io.Writer, results []pipelineApproveResult) error {
	wroteHeader := false
	for _, result := range results {
		if result.Preview == nil {
			continue
		}
		if !wroteHeader {
			fmt.Fprintln(w, "Routes:")
			wroteHeader = true
		}
		requestedName := ""
		if result.Preview.Dispatch != nil {
			requestedName = result.Preview.Dispatch.RequestedName
		}
		fmt.Fprintf(w, "%s step=%s target=%s instance=%s\n",
			result.JobID,
			emptyDash(result.StepID),
			emptyDash(result.Target),
			emptyDash(requestedName),
		)
		if result.Preview.Dispatch == nil || result.Preview.Dispatch.Preview == nil || !eventPublishPreviewHasRoutes(result.Preview.Dispatch.Preview) {
			fmt.Fprintln(w, "(no triggers matched)")
			continue
		}
		if err := renderEventPublishRoutePreview(w, result.Preview.Dispatch.Preview); err != nil {
			return err
		}
	}
	return nil
}

func renderPipelineUnblockResults(w io.Writer, results []pipelineUnblockResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineUnblockTable(w, results)
	return nil
}

func renderPipelineUnblockTable(w io.Writer, results []pipelineUnblockResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no blocked pipeline workers)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tSTEP\tTARGET\tACTION\tBEFORE\tAFTER\tINSTANCE\tMESSAGE")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
			emptyDash(result.StepID),
			emptyDash(result.Target),
			result.Action,
			emptyDash(string(result.StatusBefore)),
			emptyDash(string(result.StatusAfter)),
			emptyDash(result.Instance),
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}

func renderPipelineSkipResults(w io.Writer, results []pipelineSkipResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineSkipTable(w, results)
	return nil
}

func renderPipelineSkipTable(w io.Writer, results []pipelineSkipResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no matching pipeline steps)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tSTEP\tTARGET\tACTION\tSTATUS\tINSTANCE\tSKIPPED\tMESSAGE")
	for _, result := range results {
		skipped := "-"
		if result.Skipped {
			skipped = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
			emptyDash(result.StepID),
			emptyDash(result.Target),
			result.Action,
			emptyDash(string(result.StepStatus)),
			emptyDash(result.Instance),
			skipped,
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}

func renderPipelineCancelResults(w io.Writer, results []pipelineCancelResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineCancelTable(w, results)
	return nil
}

func renderPipelineCancelTable(w io.Writer, results []pipelineCancelResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no cancellable pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tACTION\tBEFORE\tAFTER\tINSTANCE\tMESSAGE")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
			result.Action,
			result.StatusBefore,
			result.StatusAfter,
			emptyDash(result.Instance),
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}

func renderPipelineRetryResults(w io.Writer, results []pipelineRetryResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineRetryTable(w, results)
	return renderPipelineRetryRoutePreviews(w, results)
}

func renderPipelineRetryTable(w io.Writer, results []pipelineRetryResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no failed pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tSTEP\tTARGET\tACTION\tSTATUS\tINSTANCE\tATTEMPTS\tMESSAGE")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
			emptyDash(result.StepID),
			emptyDash(result.Target),
			result.Action,
			emptyDash(string(result.StepStatus)),
			emptyDash(result.Instance),
			formatJobStepAttempts(result.Attempts, result.MaxAttempts),
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}

func renderPipelineRetryRoutePreviews(w io.Writer, results []pipelineRetryResult) error {
	wroteHeader := false
	for _, result := range results {
		if result.Preview == nil {
			continue
		}
		if !wroteHeader {
			fmt.Fprintln(w, "Routes:")
			wroteHeader = true
		}
		requestedName := ""
		if result.Preview.Dispatch != nil {
			requestedName = result.Preview.Dispatch.RequestedName
		}
		fmt.Fprintf(w, "%s step=%s target=%s instance=%s\n",
			result.JobID,
			emptyDash(result.StepID),
			emptyDash(result.Target),
			emptyDash(requestedName),
		)
		if result.Preview.Dispatch == nil || result.Preview.Dispatch.Preview == nil || !eventPublishPreviewHasRoutes(result.Preview.Dispatch.Preview) {
			fmt.Fprintln(w, "(no triggers matched)")
			continue
		}
		if err := renderEventPublishRoutePreview(w, result.Preview.Dispatch.Preview); err != nil {
			return err
		}
	}
	return nil
}

func renderPipelineTimeoutResults(w io.Writer, results []pipelineTimeoutResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineTimeoutTable(w, results)
	return nil
}

func renderPipelineTimeoutTable(w io.Writer, results []pipelineTimeoutResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no stale running pipeline steps)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tSTEP\tTARGET\tACTION\tSTATUS\tINSTANCE\tAGE\tTIMEOUT\tMESSAGE")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
			emptyDash(result.StepID),
			emptyDash(result.Target),
			result.Action,
			emptyDash(string(result.StepStatus)),
			emptyDash(result.Instance),
			emptyDash(result.Age),
			emptyDash(result.Timeout),
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}

func renderPipelineHoldResults(w io.Writer, results []pipelineHoldResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderPipelineHoldTable(w, results)
	return nil
}

func renderPipelineHoldTable(w io.Writer, results []pipelineHoldResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no matching pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tPIPELINE\tSTATUS\tNEXT\tACTION\tHELD_BEFORE\tHELD_AFTER\tHOLD_UNTIL\tMESSAGE")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID,
			emptyDash(result.Pipeline),
			result.Status,
			emptyDash(result.NextState),
			result.Action,
			yesNo(result.HeldBefore),
			yesNo(result.HeldAfter),
			emptyDash(result.HoldUntil),
			emptyDash(result.Message),
		)
	}
	_ = tw.Flush()
}

func pipelineHoldUntilText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
