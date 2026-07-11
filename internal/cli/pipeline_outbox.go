package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newPipelineOutboxCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		all         bool
		sortBy      string
		limit       int
		watch       bool
		noClear     bool
		summary     bool
		commands    bool
		jsonOut     bool
		format      string
		interval    time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "outbox [<pipeline>|--all]",
		Short: "List or control pipeline-owned outbox events.",
		Long:  "List sandboxed agent outbox events owned by one pipeline. With no pipeline, all pipeline-owned outbox events are listed. Outbox subcommands still require an explicit pipeline.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: pass at most one pipeline name.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: --interval must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox: %v\n", err)
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
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox: pipeline name is required.")
				return exitErr(2)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runPipelineOutboxSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runPipelineOutboxListWatch(ctx, cmd.OutOrStdout(), teamDir, pipelineName, filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runPipelineOutboxSummary(cmd.OutOrStdout(), teamDir, pipelineName, filters, jsonOut)
			}
			if commands {
				return runPipelineOutboxListCommands(cmd.OutOrStdout(), teamDir, pipelineName, filters, outboxListOptions{Sort: sortMode, Limit: limit}, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return runPipelineOutboxList(cmd.OutOrStdout(), teamDir, pipelineName, filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&all, "all", false, "List outbox events across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the pipeline outbox table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate outbox counts instead of rows.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from the visible pipeline-owned outbox rows, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline-owned outbox rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.AddCommand(newPipelineOutboxShowCmd())
	cmd.AddCommand(newPipelineOutboxRetryCmd())
	cmd.AddCommand(newPipelineOutboxDropCmd())
	cmd.AddCommand(newPipelineOutboxPruneCmd())
	cmd.AddCommand(newPipelineOutboxQuarantineCmd())
	return cmd
}

func newPipelineOutboxShowCmd() *cobra.Command {
	var (
		repo     string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <pipeline> <id>",
		Short: "Show one outbox event owned by one pipeline.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readPipelineOutboxItem(cmd, teamDir, args[0], args[1], "show")
			if err != nil {
				return err
			}
			actions := pipelineOutboxActionResolver(args[0])
			if commands {
				return renderOutboxItemCommands(cmd.OutOrStdout(), item, actions, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderOutboxItemResultWithActions(cmd.OutOrStdout(), item, jsonOut, tmpl, actions)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the pipeline-owned outbox item as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newPipelineOutboxRetryCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		retryAll    bool
		dryRun      bool
		commands    bool
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <pipeline> [id]",
		Short: "Retry outbox events owned by one pipeline.",
		Long:  "Move one pipeline-owned processed or failed outbox event back to pending by id, or retry a filtered pipeline-owned batch with --all. Batch retries default to failed events.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: %v\n", err)
				return exitErr(2)
			}
			if retryAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --all requires exactly one pipeline and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.OutboxStateFailed
				}
				filters, err := parseOutboxFilters(effectiveState, types, sources, jobs)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: %v\n", err)
					return exitErr(2)
				}
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				if commands {
					results, err := pipelineOutboxRetryAllResults(teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, true)
					if err != nil {
						return err
					}
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxActionResultsHaveDryRunAction(results, "would_retry"), outboxApplyCommandOptions{
						BaseArgs: []string{"agent-team", "pipeline", "outbox", "retry", args[0]},
						Repo:     repo,
						RepoSet:  cmd.Flags().Changed("repo"),
						All:      true,
						State:    stateFilter,
						StateSet: cmd.Flags().Changed("state"),
						Types:    types,
						Sources:  sources,
						Jobs:     jobs,
						Sort:     sortBy,
						SortSet:  cmd.Flags().Changed("sort"),
						Limit:    limit,
					})
				}
				return runPipelineOutboxRetryAll(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: requires <pipeline> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(types) > 0 || len(sources) > 0 || len(jobs) > 0 || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox retry: --state, --type, --source, --job, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if _, err := readPipelineOutboxItem(cmd, teamDir, args[0], args[1], "retry"); err != nil {
				return err
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), true, outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "pipeline", "outbox", "retry", args[0], args[1]},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
				})
			}
			result, err := retryOutboxItem(teamDir, args[1], dryRun)
			if err != nil {
				return err
			}
			return renderOutboxActionResults(cmd.OutOrStdout(), []outboxActionResult{result}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching pipeline-owned outbox events instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the retry without moving the event.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching pipeline outbox retry apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newPipelineOutboxDropCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		dropAll     bool
		dryRun      bool
		commands    bool
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <pipeline> [id]",
		Short: "Drop outbox events owned by one pipeline.",
		Long:  "Remove one pipeline-owned outbox event by id, or drop a filtered pipeline-owned batch with --all. Batch drops default to failed events.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: %v\n", err)
				return exitErr(2)
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --all requires exactly one pipeline and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.OutboxStateFailed
				}
				filters, err := parseOutboxFilters(effectiveState, types, sources, jobs)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: %v\n", err)
					return exitErr(2)
				}
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				if commands {
					results, err := pipelineOutboxDropAllResults(teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, true)
					if err != nil {
						return err
					}
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxActionResultsHaveDryRunAction(results, "would_drop"), outboxApplyCommandOptions{
						BaseArgs: []string{"agent-team", "pipeline", "outbox", "drop", args[0]},
						Repo:     repo,
						RepoSet:  cmd.Flags().Changed("repo"),
						All:      true,
						State:    stateFilter,
						StateSet: cmd.Flags().Changed("state"),
						Types:    types,
						Sources:  sources,
						Jobs:     jobs,
						Sort:     sortBy,
						SortSet:  cmd.Flags().Changed("sort"),
						Limit:    limit,
					})
				}
				return runPipelineOutboxDropAll(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: requires <pipeline> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(types) > 0 || len(sources) > 0 || len(jobs) > 0 || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox drop: --state, --type, --source, --job, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if _, err := readPipelineOutboxItem(cmd, teamDir, args[0], args[1], "drop"); err != nil {
				return err
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), true, outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "pipeline", "outbox", "drop", args[0], args[1]},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
				})
			}
			result, err := dropOutboxItem(teamDir, args[1], dryRun)
			if err != nil {
				return err
			}
			return renderOutboxActionResults(cmd.OutOrStdout(), []outboxActionResult{result}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching pipeline-owned outbox events instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the drop without removing the event.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching pipeline outbox drop apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newPipelineOutboxPruneCmd() *cobra.Command {
	var (
		repo      string
		stateFlag string
		olderThan time.Duration
		dryRun    bool
		commands  bool
		jsonOut   bool
		format    string
		types     []string
		sources   []string
		jobs      []string
		limit     int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune <pipeline>",
		Short: "Prune old outbox events owned by one pipeline.",
		Long:  "Prune old sandboxed agent outbox events owned by one pipeline. By default this removes processed events; pass --state failed, pending, or all for explicit cleanup.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxPruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseOutboxPruneState(stateFlag)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters("", types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if commands {
				results, err := pipelineOutboxPruneResults(teamDir, args[0], state, olderThan, time.Now().UTC(), true, filters, limit)
				if err != nil {
					return err
				}
				return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxPruneResultsHaveDryRunAction(results), outboxApplyCommandOptions{
					BaseArgs:     []string{"agent-team", "pipeline", "outbox", "prune", args[0]},
					Repo:         repo,
					RepoSet:      cmd.Flags().Changed("repo"),
					State:        stateFlag,
					StateSet:     cmd.Flags().Changed("state"),
					Types:        types,
					Sources:      sources,
					Jobs:         jobs,
					Limit:        limit,
					OlderThan:    olderThan,
					OlderThanSet: cmd.Flags().Changed("older-than"),
				})
			}
			return runPipelineOutboxPrune(cmd.OutOrStdout(), teamDir, args[0], state, olderThan, time.Now().UTC(), dryRun, filters, limit, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFlag, "state", daemon.OutboxStateProcessed, "Outbox state to prune: processed, failed, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune items older than this duration based on processed/failed/update/create time.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket before pruning; repeat or comma-separate values.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Prune at most this many matching pipeline-owned outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview pipeline-owned outbox events that would be pruned without dropping them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching pipeline outbox prune apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each prune result with a Go template, e.g. '{{.ID}} {{.Dropped}}'.")
	return cmd
}

func newPipelineOutboxQuarantineCmd() *cobra.Command {
	var (
		repo         string
		stateFilter  string
		types        []string
		sources      []string
		jobs         []string
		restorable   bool
		unrestorable bool
		all          bool
		sortBy       string
		limit        int
		summary      bool
		commands     bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "quarantine [<pipeline>|--all]",
		Short: "List pipeline-owned quarantined outbox files.",
		Long:  "List quarantined outbox files owned by one pipeline. With no pipeline, all pipeline-owned quarantined outbox files are listed. Show, restore, and drop still require an explicit pipeline.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: --all cannot be combined with a pipeline argument.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: pass at most one pipeline name.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: --limit must be >= 0.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || limit > 0) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxQuarantineSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team pipeline outbox quarantine", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: %v\n", err)
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
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: pipeline name is required.")
				return exitErr(2)
			}
			items, err := collectPipelineOutboxQuarantineItems(teamDir, pipelineName, filters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: %v\n", err)
				return exitErr(1)
			}
			items = filterOutboxQuarantineRestorable(items, restorable, unrestorable)
			if summary {
				return renderOutboxQuarantineSummary(cmd.OutOrStdout(), summarizeOutboxQuarantineItems(items), jsonOut)
			}
			items = prepareOutboxQuarantineItems(items, sortMode, limit)
			if commands {
				actions, err := pipelineOutboxQuarantineActionResolverForScope(teamDir, pipelineName)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine: %v\n", err)
					return exitErr(1)
				}
				return renderOutboxQuarantineListCommands(cmd.OutOrStdout(), items, actions, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderOutboxQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined files that cannot be restored.")
	cmd.Flags().BoolVar(&all, "all", false, "List quarantined outbox files across all pipelines. This is the default when no pipeline is passed.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", outboxQuarantineSortFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate pipeline-owned quarantined outbox-file counts instead of rows.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from the visible pipeline-owned quarantined outbox files, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit pipeline-owned quarantined outbox files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline-owned quarantined outbox file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	cmd.AddCommand(newPipelineOutboxQuarantineShowCmd())
	cmd.AddCommand(newPipelineOutboxQuarantineRestoreCmd())
	cmd.AddCommand(newPipelineOutboxQuarantineDropCmd())
	return cmd
}

func newPipelineOutboxQuarantineShowCmd() *cobra.Command {
	var (
		repo     string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <pipeline> <quarantine-path>",
		Short: "Show one pipeline-owned quarantined outbox file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team pipeline outbox quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readPipelineOutboxQuarantineItem(teamDir, args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine show: %v\n", err)
				return exitErr(1)
			}
			result, err := showOutboxQuarantine(teamDir, item.Path)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine show: %v\n", err)
				return exitErr(1)
			}
			result.Pipeline = args[0]
			if commands {
				return renderOutboxQuarantineCommands(cmd.OutOrStdout(), result, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderOutboxQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the pipeline-owned quarantined outbox file as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline-owned quarantined outbox file with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newPipelineOutboxQuarantineRestoreCmd() *cobra.Command {
	var (
		repo        string
		restoreAll  bool
		dryRun      bool
		force       bool
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		sortBy      string
		limit       int
		jsonOut     bool
		format      string
		commands    bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <pipeline> [quarantine-path]",
		Short: "Restore pipeline-owned quarantined outbox files.",
		Long:  "Restore one pipeline-owned quarantined outbox file by path, or restore a filtered pipeline-owned batch of restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team pipeline outbox quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: --limit must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if restoreAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: --all requires exactly one pipeline and cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: %v\n", err)
					return exitErr(2)
				}
				items, err := collectPipelineOutboxQuarantineItems(teamDir, args[0], filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: %v\n", err)
					return exitErr(1)
				}
				items = filterOutboxQuarantineRestorable(items, true, false)
				results, err := restoreOutboxQuarantineItems(teamDir, items, dryRun, force, sortMode, limit)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxQuarantineRestoreResultsHaveDryRunAction(results, "would_restore"), outboxApplyCommandOptions{
						BaseArgs: []string{"agent-team", "pipeline", "outbox", "quarantine", "restore", args[0]},
						Repo:     repo,
						RepoSet:  cmd.Flags().Changed("repo"),
						All:      true,
						Force:    force,
						State:    stateFilter,
						StateSet: cmd.Flags().Changed("state"),
						Types:    types,
						Sources:  sources,
						Jobs:     jobs,
						Sort:     sortBy,
						SortSet:  cmd.Flags().Changed("sort"),
						Limit:    limit,
					})
				}
				return renderOutboxQuarantineRestoreMany(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: requires <pipeline> and one path unless --all is set.")
				return exitErr(2)
			}
			if !outboxQuarantineFiltersEmpty(filters) || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			if _, err := readPipelineOutboxQuarantineItem(teamDir, args[0], args[1]); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: %v\n", err)
				return exitErr(1)
			}
			result, err := restoreOutboxQuarantine(teamDir, args[1], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine restore: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_restore", outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "pipeline", "outbox", "quarantine", "restore", args[0], result.Path},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
					Force:    force,
				})
			}
			return renderOutboxQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching pipeline-owned restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active outbox file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching pipeline-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, restore at most this many matching pipeline-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching pipeline outbox quarantine restore apply command when the preview has actionable work.")
	cmd.Flags().StringVar(&format, "format", "", "Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newPipelineOutboxQuarantineDropCmd() *cobra.Command {
	var (
		repo         string
		dropAll      bool
		dryRun       bool
		stateFilter  string
		types        []string
		sources      []string
		jobs         []string
		restorable   bool
		unrestorable bool
		olderThan    time.Duration
		sortBy       string
		limit        int
		jsonOut      bool
		format       string
		commands     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <pipeline> [quarantine-path]",
		Short: "Drop pipeline-owned quarantined outbox files after inspection.",
		Long:  "Drop one pipeline-owned quarantined outbox file by path, or drop a filtered pipeline-owned batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: --limit must be >= 0.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team pipeline outbox quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: --all requires exactly one pipeline and cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: %v\n", err)
					return exitErr(2)
				}
				items, err := collectPipelineOutboxQuarantineItems(teamDir, args[0], filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: %v\n", err)
					return exitErr(1)
				}
				items = filterOutboxQuarantineRestorable(items, restorable, unrestorable)
				results, err := dropOutboxQuarantineItems(teamDir, items, dryRun, olderThan, sortMode, limit, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxQuarantineDropResultsHaveDryRunAction(results, "would_drop"), outboxApplyCommandOptions{
						BaseArgs:     []string{"agent-team", "pipeline", "outbox", "quarantine", "drop", args[0]},
						Repo:         repo,
						RepoSet:      cmd.Flags().Changed("repo"),
						All:          true,
						State:        stateFilter,
						StateSet:     cmd.Flags().Changed("state"),
						Types:        types,
						Sources:      sources,
						Jobs:         jobs,
						Restorable:   restorable,
						Unrestorable: unrestorable,
						Sort:         sortBy,
						SortSet:      cmd.Flags().Changed("sort"),
						Limit:        limit,
						OlderThan:    olderThan,
						OlderThanSet: cmd.Flags().Changed("older-than"),
					})
				}
				return renderOutboxQuarantineDrop(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: requires <pipeline> and one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !outboxQuarantineFiltersEmpty(filters) || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			item, err := readPipelineOutboxQuarantineItem(teamDir, args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: %v\n", err)
				return exitErr(1)
			}
			result, err := dropOutboxQuarantineItem(daemon.OutboxRoot(teamDir), item, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox quarantine drop: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_drop", outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "pipeline", "outbox", "quarantine", "drop", args[0], result.Path},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
				})
			}
			return renderOutboxQuarantineDrop(cmd.OutOrStdout(), []outboxQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching pipeline-owned quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching pipeline-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching pipeline-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching pipeline outbox quarantine drop apply command when the preview has actionable work.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func runPipelineOutboxList(w io.Writer, teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template) error {
	items, err := collectPipelineOutboxItems(teamDir, pipeline)
	if err != nil {
		return err
	}
	return runOutboxListItems(w, items, filters, opts, jsonOut, tmpl)
}

func runPipelineOutboxListCommands(w io.Writer, teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, scope operatorCommandScope) error {
	items, err := collectPipelineOutboxItems(teamDir, pipeline)
	if err != nil {
		return err
	}
	actions, err := pipelineOutboxActionResolverForScope(teamDir, pipeline)
	if err != nil {
		return err
	}
	return renderOutboxListCommands(w, items, filters, opts, actions, scope)
}

func runPipelineOutboxSummary(w io.Writer, teamDir, pipeline string, filters outboxListFilters, jsonOut bool) error {
	items, err := collectPipelineOutboxItems(teamDir, pipeline)
	if err != nil {
		return err
	}
	return renderOutboxSummaryForItems(w, items, filters, jsonOut)
}

func runPipelineOutboxListWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
	return runOutboxWatch(ctx, w, jsonOut, interval, clear, func() error {
		return runPipelineOutboxList(w, teamDir, pipeline, filters, opts, jsonOut, tmpl)
	})
}

func runPipelineOutboxSummaryWatch(ctx context.Context, w io.Writer, teamDir, pipeline string, filters outboxListFilters, jsonOut bool, interval time.Duration, clear bool) error {
	return runOutboxWatch(ctx, w, jsonOut, interval, clear, func() error {
		return runPipelineOutboxSummary(w, teamDir, pipeline, filters, jsonOut)
	})
}

func runPipelineOutboxRetryAll(w io.Writer, teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := pipelineOutboxRetryAllResults(teamDir, pipeline, filters, opts, dryRun)
	if err != nil {
		return err
	}
	return renderOutboxActionResults(w, results, jsonOut, tmpl)
}

func pipelineOutboxRetryAllResults(teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, dryRun bool) ([]outboxActionResult, error) {
	matches, err := filteredPipelineOutboxItems(teamDir, pipeline, filters, opts)
	if err != nil {
		return nil, err
	}
	return retryOutboxItemMatches(teamDir, matches, dryRun)
}

func runPipelineOutboxDropAll(w io.Writer, teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := pipelineOutboxDropAllResults(teamDir, pipeline, filters, opts, dryRun)
	if err != nil {
		return err
	}
	return renderOutboxActionResults(w, results, jsonOut, tmpl)
}

func pipelineOutboxDropAllResults(teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions, dryRun bool) ([]outboxActionResult, error) {
	matches, err := filteredPipelineOutboxItems(teamDir, pipeline, filters, opts)
	if err != nil {
		return nil, err
	}
	return dropOutboxItemMatches(teamDir, matches, dryRun)
}

func runPipelineOutboxPrune(w io.Writer, teamDir, pipeline string, state string, olderThan time.Duration, now time.Time, dryRun bool, filters outboxListFilters, limit int, jsonOut bool, tmpl *template.Template) error {
	results, err := pipelineOutboxPruneResults(teamDir, pipeline, state, olderThan, now, dryRun, filters, limit)
	if err != nil {
		return err
	}
	return renderOutboxPruneResults(w, results, jsonOut, tmpl)
}

func pipelineOutboxPruneResults(teamDir, pipeline string, state string, olderThan time.Duration, now time.Time, dryRun bool, filters outboxListFilters, limit int) ([]outboxPruneResult, error) {
	items, err := collectPipelineOutboxItems(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	return pruneOutboxItemsFromList(teamDir, items, state, olderThan, now, dryRun, filters, limit)
}

func collectPipelineOutboxQuarantineItems(teamDir, pipeline string, filters outboxListFilters) ([]outboxQuarantineItem, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	items, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = outboxQuarantineItemsForJobs(items, jobs)
	return filterOutboxQuarantineItems(items, filters), nil
}

func pipelineOutboxQuarantineActionResolverForScope(teamDir, pipeline string) (outboxQuarantineActionResolver, error) {
	pipeline = strings.TrimSpace(pipeline)
	if pipeline != "" {
		return scopedOutboxQuarantineActionResolver("", pipeline, ""), nil
	}
	jobs, err := selectedPipelineJobs(teamDir, "")
	if err != nil {
		return nil, err
	}
	return func(item outboxQuarantineItem) []string {
		for _, j := range jobs {
			if j == nil || strings.TrimSpace(j.Pipeline) == "" {
				continue
			}
			if outboxQuarantineItemMatchesJob(item, j) {
				return scopedOutboxQuarantineActionResolver("", j.Pipeline, "")(item)
			}
		}
		return nil
	}, nil
}

func readPipelineOutboxQuarantineItem(teamDir, pipeline, rawPath string) (outboxQuarantineItem, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return outboxQuarantineItem{}, err
	}
	outboxRoot := daemon.OutboxRoot(teamDir)
	rel, err := normalizeOutboxQuarantinePath(rawPath)
	if err != nil {
		return outboxQuarantineItem{}, err
	}
	item, err := inspectOutboxQuarantineFile(outboxRoot, rel)
	if err != nil {
		return outboxQuarantineItem{}, err
	}
	if !outboxQuarantineMatchesAnyJob(item, jobs) {
		return outboxQuarantineItem{}, fmt.Errorf("quarantined outbox file %q is not owned by pipeline %q", item.Path, pipeline)
	}
	return item, nil
}

func outboxQuarantineItemsForJobs(items []outboxQuarantineItem, jobs []*job.Job) []outboxQuarantineItem {
	if len(items) == 0 || len(jobs) == 0 {
		return nil
	}
	out := make([]outboxQuarantineItem, 0, len(items))
	for _, item := range items {
		if outboxQuarantineMatchesAnyJob(item, jobs) {
			out = append(out, item)
		}
	}
	return out
}

func outboxQuarantineMatchesAnyJob(item outboxQuarantineItem, jobs []*job.Job) bool {
	for _, j := range jobs {
		if outboxQuarantineItemMatchesJob(item, j) {
			return true
		}
	}
	return false
}

func filteredPipelineOutboxItems(teamDir, pipeline string, filters outboxListFilters, opts outboxListOptions) ([]*daemon.OutboxItem, error) {
	items, err := collectPipelineOutboxItems(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	return prepareOutboxActionMatches(filterOutboxItems(items, filters), opts), nil
}

func collectPipelineOutboxItems(teamDir, pipeline string) ([]*daemon.OutboxItem, error) {
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		return nil, err
	}
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return nil, err
	}
	return outboxItemsForJobs(items, jobs), nil
}

func readPipelineOutboxItem(cmd *cobra.Command, teamDir, pipeline, id, verb string) (*daemon.OutboxItem, error) {
	item, err := daemon.ReadOutboxItem(teamDir, id)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox %s: outbox item %q not found.\n", verb, id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	jobs, err := selectedPipelineJobs(teamDir, pipeline)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox %s: %v\n", verb, err)
		return nil, exitErr(1)
	}
	if len(outboxItemsForJobs([]*daemon.OutboxItem{item}, jobs)) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline outbox %s: outbox item %q is not owned by pipeline %q.\n", verb, id, pipeline)
		return nil, exitErr(2)
	}
	return item, nil
}
