package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newPipelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Inspect declared pipeline workflows.",
		Long:  "Inspect pipeline declarations loaded from .agent_team/instances.toml.",
	}
	cmd.AddCommand(newPipelineLsCmd())
	cmd.AddCommand(newPipelineShowCmd())
	cmd.AddCommand(newPipelineGraphCmd())
	cmd.AddCommand(newPipelineDoctorCmd())
	cmd.AddCommand(newPipelineJobsCmd())
	cmd.AddCommand(newPipelineStatusCmd())
	cmd.AddCommand(newPipelineExplainCmd())
	cmd.AddCommand(newPipelineSnapshotCmd())
	cmd.AddCommand(newPipelineNextCmd())
	cmd.AddCommand(newPipelineReadyCmd())
	cmd.AddCommand(newPipelineAdvanceCmd())
	cmd.AddCommand(newPipelineApproveCmd())
	cmd.AddCommand(newPipelineRetryCmd())
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
		repo    string
		all     bool
		jsonOut bool
		format  string
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
			return renderPipelineDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Validate all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline doctor findings as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.")
	return cmd
}

func newPipelineJobsCmd() *cobra.Command {
	var (
		repo           string
		status         string
		runtimeFilters []string
		summary        bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "jobs <pipeline>",
		Short: "List jobs for one pipeline.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline jobs: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline jobs: %v\n", err)
				return exitErr(2)
			}
			filters, err := newJobListFilters(status, "", "", args[0], "", "", "", runtimeFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline jobs: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
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
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate pipeline job counts instead of job rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit jobs as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newPipelineStatusCmd() *cobra.Command {
	var (
		repo    string
		all     bool
		jsonOut bool
		format  string
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
			rows, err := collectPipelineStatusRows(teamDir, pipelineName)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline status: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineStatusRows(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Summarize all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline status rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Pipeline}} {{.Jobs}} {{.ReadySteps}}'.")
	return cmd
}

func newPipelineExplainCmd() *cobra.Command {
	var (
		repo    string
		all     bool
		limit   int
		states  []string
		jsonOut bool
		format  string
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
			rows, err := collectPipelineExplainRows(teamDir, pipelineName, limit, stateFilter)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline explain: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineExplainRows(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Explain all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit job explanations per pipeline; 0 means no limit.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Only explain jobs whose next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
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
			var rows []pipelineStatusRow
			if strings.TrimSpace(teamName) != "" {
				rows, err = collectTeamPipelineStatus(teamDir, teamName)
				if err == nil && pipelineName != "" {
					rows, err = filterPipelineNextRowsForPipeline(rows, pipelineName, teamName)
				}
			} else {
				rows, err = collectPipelineStatusRows(teamDir, pipelineName)
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline next: %v\n", err)
				return exitErr(1)
			}
			actions := pipelineNextActionsFromStatus(rows, limit)
			return renderPipelineNextActions(cmd.OutOrStdout(), actions, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&teamName, "team", "", "Only consider pipelines owned by this declared team; actions are rendered with team-scoped commands.")
	cmd.Flags().BoolVar(&all, "all", false, "Consider all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of actions to print (0 = no limit).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit recommended actions as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action with a Go template, e.g. '{{.Pipeline}} {{.Action}}'.")
	return cmd
}

func newPipelineReadyCmd() *cobra.Command {
	var (
		repo    string
		states  []string
		all     bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ready <pipeline>|--all",
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
			if !all && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: pass a pipeline name or --all.")
				return exitErr(2)
			}
			stateFilter, err := parseJobNextStateFilter(states, !cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline ready: %v\n", err)
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
			if !all {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline ready: pipeline name is required.")
				return exitErr(2)
			}
			return runJobReady(cmd.OutOrStdout(), teamDir, pipelineName, stateFilter, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&all, "all", false, "List ready jobs across all pipelines.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit ready rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.")
	return cmd
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
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline advance: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineAdvanceFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline advance: %v\n", err)
				return exitErr(2)
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
			return renderPipelineAdvanceResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
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
		dryRun        bool
		previewRoutes bool
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
			if previewRoutes && (!dryRun || !dispatchNow) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: --preview-routes requires --dry-run and --dispatch.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineApproveFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline approve: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline approve: pipeline name is required.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := approvePipelineManualGates(cmd, teamDir, pipelineName, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, step, message, limit, dispatchNow, dryRun, previewRoutes)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline approve: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineApproveResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview manual gate approvals and optional dispatches without writing job or daemon state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run --dispatch, include route and payload previews.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit approval results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each approval result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
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
		dryRun        bool
		previewRoutes bool
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
			if previewRoutes && (!dryRun || !dispatchNow) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: --preview-routes requires --dry-run and --dispatch.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineRetryFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline retry: %v\n", err)
				return exitErr(2)
			}
			pipelineName := ""
			if len(args) == 1 {
				pipelineName = strings.TrimSpace(args[0])
			}
			if !all && pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline retry: pipeline name is required.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := retryPipelineJobs(cmd, teamDir, pipelineName, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, step, message, limit, dispatchNow, dryRun, previewRoutes)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline retry: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineRetryResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview failed-step resets and optional dispatches without writing job or daemon state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run --dispatch, include route and payload previews.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit retry results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each retry result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newPipelineRunCmd() *cobra.Command {
	var (
		repo        string
		id          string
		ticketURL   string
		kickoff     string
		kickoffFile string
		dispatchNow bool
		workspace   string
		runtimeKind string
		runtimeBin  string
		dryRun      bool
		jsonOut     bool
		format      string
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
				ID:          id,
				TicketURL:   ticketURL,
				Kickoff:     kickoff,
				KickoffFile: kickoffFile,
				DispatchNow: dispatchNow,
				Workspace:   workspace,
				Runtime:     runtimeSelection{Kind: runtimeKind, Binary: runtimeBin},
				DryRun:      dryRun,
				JSON:        jsonOut,
				Format:      format,
				ErrPrefix:   "agent-team pipeline run",
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Canonical ticket URL to store on the job.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the first pipeline step.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the first ready pipeline step immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the pipeline job that would be created without writing it.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the created job or advance result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the created or advanced job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

type pipelineRunOptions struct {
	ID          string
	TicketURL   string
	Kickoff     string
	KickoffFile string
	DispatchNow bool
	Workspace   string
	Runtime     runtimeSelection
	DryRun      bool
	JSON        bool
	Format      string
	ErrPrefix   string
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
	tmpl, err := parseJobFormat(opts.Format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return exitErr(2)
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
		if opts.JSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
		}
		if tmpl != nil {
			return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
		}
		return renderJobAdvanceResult(cmd.OutOrStdout(), res)
	}
	return renderJobResult(cmd.OutOrStdout(), j, opts.JSON, tmpl)
}

type pipelineInfo struct {
	Name    string             `json:"name"`
	Trigger map[string]any     `json:"trigger"`
	Steps   []pipelineStepInfo `json:"steps"`
}

type pipelineStepInfo struct {
	ID       string   `json:"id"`
	Target   string   `json:"target"`
	After    []string `json:"after,omitempty"`
	Gate     string   `json:"gate,omitempty"`
	Optional bool     `json:"optional,omitempty"`
}

type pipelineGraph struct {
	Name    string              `json:"name"`
	Trigger map[string]any      `json:"trigger,omitempty"`
	Summary string              `json:"summary"`
	Nodes   []pipelineGraphNode `json:"nodes"`
	Edges   []pipelineGraphEdge `json:"edges"`
}

type pipelineGraphNode struct {
	ID       string   `json:"id"`
	Target   string   `json:"target,omitempty"`
	After    []string `json:"after,omitempty"`
	Gate     string   `json:"gate,omitempty"`
	Optional bool     `json:"optional,omitempty"`
	Routes   []string `json:"routes,omitempty"`
	Missing  bool     `json:"missing,omitempty"`
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
	BlockedSteps       int      `json:"blocked_steps"`
	ManualGates        int      `json:"manual_gates"`
	FailedSteps        int      `json:"failed_steps"`
	HeldSteps          int      `json:"held_steps,omitempty"`
	DoneSteps          int      `json:"done_steps"`
	NoStep             int      `json:"no_step"`
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

type pipelineRetryResult struct {
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
			ID:       id,
			Target:   strings.TrimSpace(step.Target),
			After:    trimStringSlice(step.After),
			Gate:     strings.TrimSpace(step.Gate),
			Optional: step.Optional,
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
		report := doctorPipeline(top, pipeline)
		result.Pipelines = append(result.Pipelines, report)
		result.Problems = append(result.Problems, report.Problems...)
		result.Warnings = append(result.Warnings, report.Warnings...)
	}
	result.OK = len(result.Problems) == 0
	return result, nil
}

func doctorPipeline(top *topology.Topology, pipeline *topology.Pipeline) pipelineDoctorPipeline {
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
	report.OK = len(report.Problems) == 0
	return report
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
			ID:       step.ID,
			Target:   step.Target,
			After:    append([]string(nil), step.After...),
			Gate:     step.Gate,
			Optional: step.Optional,
		})
	}
	return pipelineInfo{
		Name:    p.Name,
		Trigger: triggerAsMap(p.Trigger),
		Steps:   steps,
	}
}

func collectPipelineStatusRows(teamDir, pipeline string) ([]pipelineStatusRow, error) {
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
		applyPipelineStatusJob(rowFor(name), j)
	}
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

func collectPipelineExplainRows(teamDir, pipeline string, limit int, stateFilter map[string]bool) ([]pipelineExplainRow, error) {
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
			explained := explainJobPipeline(j)
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

func applyPipelineStatusJob(row *pipelineStatusRow, j *job.Job) {
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
		row.QueuedSteps++
	case "running":
		row.RunningSteps++
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
	if row.ReadySteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline advance %s --dry-run --preview-routes", row.Pipeline))
	}
	if row.ParallelReadySteps > 1 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline advance %s --all-ready-steps --dry-run --preview-routes", row.Pipeline))
	}
	if row.FailedSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline retry %s --dry-run --dispatch --preview-routes", row.Pipeline))
		actions = append(actions, "agent-team repair --retry-pipelines --dry-run --preview-routes")
		actions = append(actions, fmt.Sprintf("agent-team pipeline explain %s --state failed", row.Pipeline))
		actions = append(actions, fmt.Sprintf("agent-team pipeline ready %s --state failed", row.Pipeline))
	}
	if row.HeldSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline explain %s --state held", row.Pipeline))
		actions = append(actions, fmt.Sprintf("agent-team pipeline ready %s --state held", row.Pipeline))
	}
	if row.ManualGates > 0 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline approve %s --dry-run --dispatch --preview-routes", row.Pipeline))
	}
	if row.BlockedSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team pipeline explain %s --state blocked", row.Pipeline))
		actions = append(actions, fmt.Sprintf("agent-team pipeline ready %s --state blocked", row.Pipeline))
	}
	if row.QueuedSteps > 0 {
		actions = append(actions, "agent-team tick")
	}
	row.Actions = actions
}

func pipelineNextActionsFromStatus(rows []pipelineStatusRow, limit int) []pipelineNextAction {
	actions := []pipelineNextAction{}
	for _, row := range rows {
		for _, action := range row.Actions {
			action = strings.TrimSpace(action)
			if action == "" {
				continue
			}
			actions = append(actions, pipelineNextAction{
				Pipeline: row.Pipeline,
				Action:   action,
				Reason:   pipelineNextActionReason(row, action),
				Status:   row,
			})
			if limit > 0 && len(actions) >= limit {
				return actions
			}
		}
	}
	return actions
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
	case strings.Contains(action, " advance "):
		return fmt.Sprintf("ready_steps=%d", row.ReadySteps)
	case strings.Contains(action, " retry "),
		strings.Contains(action, " --retry-pipelines "),
		strings.Contains(action, " --state failed"):
		return fmt.Sprintf("failed_steps=%d", row.FailedSteps)
	case strings.Contains(action, " approve "):
		return fmt.Sprintf("manual_gates=%d", row.ManualGates)
	case strings.Contains(action, " --state blocked"):
		return fmt.Sprintf("blocked_steps=%d", row.BlockedSteps)
	case strings.Contains(action, " --state held"):
		return fmt.Sprintf("held_steps=%d", row.HeldSteps)
	case action == "agent-team tick", strings.Contains(action, " tick "):
		return fmt.Sprintf("queued_steps=%d", row.QueuedSteps)
	default:
		return ""
	}
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

func retryPipelineJobs(cmd *cobra.Command, teamDir, pipeline, workspace string, selection runtimeSelection, stepFilter, message string, limit int, dispatchNow, dryRun bool, previewRoutes bool) ([]pipelineRetryResult, error) {
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
			JobID:      j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			StepID:     row.StepID,
			Target:     row.Target,
			StepStatus: row.StepStatus,
			Instance:   row.Instance,
			Action:     "would_retry",
			DryRun:     dryRun,
			Message:    row.Message,
		}
		stepID := resetFailedPipelineStepForRetryByID(j, row.StepID)
		if stepID == "" {
			result.Action = "skipped"
			result.Message = "no retryable failed step"
			results = append(results, result)
			continue
		}
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
			}
		}
		results = append(results, result)
	}
	return results, nil
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
		fmt.Fprintf(w, "  %s target=%s after=%s%s%s\n", step.ID, step.Target, after, gate, optional)
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
		missing := ""
		if node.Missing {
			missing = " missing=true"
		}
		fmt.Fprintf(w, "  %s target=%s after=%s%s%s%s%s\n", node.ID, node.Target, after, gate, optional, routes, missing)
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
	if node.Target != "" {
		parts = append(parts, "target: "+node.Target)
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
		if len(step.After) > 0 {
			parts = append(parts, fmt.Sprintf("%s:%s after=%s%s%s", step.ID, step.Target, strings.Join(step.After, ","), gate, optional))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%s%s%s", step.ID, step.Target, gate, optional))
		}
	}
	return strings.Join(parts, " -> ")
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

func renderPipelineStatusTable(w io.Writer, rows []pipelineStatusRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no pipelines)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PIPELINE\tDECLARED\tSTEPS\tJOBS\tJOB_STATUS\tREADY\tQUEUED\tRUNNING\tBLOCKED\tMANUAL_GATES\tFAILED\tHELD\tDONE\tNONE\tACTION")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			row.Pipeline,
			yesNo(row.Declared),
			row.Steps,
			row.Jobs,
			pipelineStatusJobSummary(row),
			row.ReadySteps,
			row.QueuedSteps,
			row.RunningSteps,
			row.BlockedSteps,
			row.ManualGates,
			row.FailedSteps,
			row.HeldSteps,
			row.DoneSteps,
			row.NoStep,
			emptyDash(strings.Join(row.Actions, "; ")),
		)
	}
	_ = tw.Flush()
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
