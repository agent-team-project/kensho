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

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newTeamOutboxCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
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
		Use:   "outbox <team>",
		Short: "List or control outbox events scoped to one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox: --interval must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runTeamOutboxSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runTeamOutboxListWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runTeamOutboxSummary(cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut)
			}
			if commands {
				return runTeamOutboxListCommands(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return runTeamOutboxList(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the team outbox table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate outbox counts instead of rows.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from the visible team-owned outbox rows, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team-owned outbox rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each team-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.AddCommand(newTeamOutboxShowCmd())
	cmd.AddCommand(newTeamOutboxRetryCmd())
	cmd.AddCommand(newTeamOutboxDropCmd())
	cmd.AddCommand(newTeamOutboxPruneCmd())
	cmd.AddCommand(newTeamOutboxQuarantineCmd())
	return cmd
}

func newTeamOutboxShowCmd() *cobra.Command {
	var (
		repo     string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <team> <id>",
		Short: "Show one outbox event owned by one team.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readTeamOutboxItem(cmd, teamDir, args[0], args[1], "show")
			if err != nil {
				return err
			}
			actions := teamOutboxActionResolver(args[0])
			if commands {
				return renderOutboxItemCommands(cmd.OutOrStdout(), item, actions, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderOutboxItemResultWithActions(cmd.OutOrStdout(), item, jsonOut, tmpl, actions)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the team-owned outbox item as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newTeamOutboxRetryCmd() *cobra.Command {
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
		Use:     "retry <team> [id]",
		Aliases: []string{"requeue"},
		Short:   "Retry outbox events owned by one team.",
		Long:    "Move one team-owned processed or failed outbox event back to pending by id, or retry a filtered team-owned batch with --all. Batch retries default to failed events.",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox retry: %v\n", err)
				return exitErr(2)
			}
			if retryAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: --all requires exactly one team and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox retry: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.OutboxStateFailed
				}
				filters, err := parseOutboxFilters(effectiveState, types, sources, jobs)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox retry: %v\n", err)
					return exitErr(2)
				}
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				if commands {
					results, err := teamOutboxRetryAllResults(teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, true)
					if err != nil {
						return err
					}
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxActionResultsHaveDryRunAction(results, "would_retry"), outboxApplyCommandOptions{
						BaseArgs: []string{"agent-team", "team", "outbox", "retry", args[0]},
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
				return runTeamOutboxRetryAll(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: requires <team> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(types) > 0 || len(sources) > 0 || len(jobs) > 0 || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox retry: --state, --type, --source, --job, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if _, err := readTeamOutboxItem(cmd, teamDir, args[0], args[1], "retry"); err != nil {
				return err
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), true, outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "team", "outbox", "retry", args[0], args[1]},
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
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching team-owned outbox events instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the retry without moving the event.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching team outbox retry apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newTeamOutboxDropCmd() *cobra.Command {
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
		Use:   "drop <team> [id]",
		Short: "Drop outbox events owned by one team.",
		Long:  "Remove one team-owned outbox event by id, or drop a filtered team-owned batch with --all. Batch drops default to failed events.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox drop: %v\n", err)
				return exitErr(2)
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: --all requires exactly one team and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox drop: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.OutboxStateFailed
				}
				filters, err := parseOutboxFilters(effectiveState, types, sources, jobs)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox drop: %v\n", err)
					return exitErr(2)
				}
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				if commands {
					results, err := teamOutboxDropAllResults(teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, true)
					if err != nil {
						return err
					}
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxActionResultsHaveDryRunAction(results, "would_drop"), outboxApplyCommandOptions{
						BaseArgs: []string{"agent-team", "team", "outbox", "drop", args[0]},
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
				return runTeamOutboxDropAll(cmd.OutOrStdout(), teamDir, args[0], filters, outboxListOptions{Sort: sortMode, Limit: limit}, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: requires <team> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(types) > 0 || len(sources) > 0 || len(jobs) > 0 || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox drop: --state, --type, --source, --job, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if _, err := readTeamOutboxItem(cmd, teamDir, args[0], args[1], "drop"); err != nil {
				return err
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), true, outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "team", "outbox", "drop", args[0], args[1]},
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
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching team-owned outbox events instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the drop without removing the event.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching team outbox drop apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newTeamOutboxPruneCmd() *cobra.Command {
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
		Use:   "prune <team>",
		Short: "Prune old outbox events owned by one team.",
		Long:  "Prune old sandboxed agent outbox events owned by one team. By default this removes processed events; pass --state failed, pending, or all for explicit cleanup.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox prune: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox prune: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox prune: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox prune: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxPruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseOutboxPruneState(stateFlag)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox prune: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters("", types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if commands {
				results, err := teamOutboxPruneResults(teamDir, args[0], state, olderThan, time.Now().UTC(), true, filters, limit)
				if err != nil {
					return err
				}
				return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxPruneResultsHaveDryRunAction(results), outboxApplyCommandOptions{
					BaseArgs:     []string{"agent-team", "team", "outbox", "prune", args[0]},
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
			return runTeamOutboxPrune(cmd.OutOrStdout(), teamDir, args[0], state, olderThan, time.Now().UTC(), dryRun, filters, limit, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFlag, "state", daemon.OutboxStateProcessed, "Outbox state to prune: processed, failed, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune items older than this duration based on processed/failed/update/create time.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket before pruning; repeat or comma-separate values.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Prune at most this many matching team-owned outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team-owned outbox events that would be pruned without dropping them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching team outbox prune apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each prune result with a Go template, e.g. '{{.ID}} {{.Dropped}}'.")
	return cmd
}

func newTeamOutboxQuarantineCmd() *cobra.Command {
	var (
		repo         string
		stateFilter  string
		types        []string
		sources      []string
		jobs         []string
		restorable   bool
		unrestorable bool
		sortBy       string
		limit        int
		summary      bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "quarantine <team>",
		Short: "List team-owned quarantined outbox files.",
		Long:  "List quarantined sandboxed agent outbox files owned by one declared team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine: --limit must be >= 0.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || limit > 0) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxQuarantineSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team team outbox quarantine", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			items, err := collectTeamOutboxQuarantineItems(teamDir, args[0], filters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine: %v\n", err)
				return exitErr(1)
			}
			items = filterOutboxQuarantineRestorable(items, restorable, unrestorable)
			if summary {
				return renderOutboxQuarantineSummary(cmd.OutOrStdout(), summarizeOutboxQuarantineItems(items), jsonOut)
			}
			items = prepareOutboxQuarantineItems(items, sortMode, limit)
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
	cmd.Flags().StringVar(&sortBy, "sort", "path", outboxQuarantineSortFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate team-owned quarantined outbox-file counts instead of rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team-owned quarantined outbox files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each team-owned quarantined outbox file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	cmd.AddCommand(newTeamOutboxQuarantineShowCmd())
	cmd.AddCommand(newTeamOutboxQuarantineRestoreCmd())
	cmd.AddCommand(newTeamOutboxQuarantineDropCmd())
	return cmd
}

func newTeamOutboxQuarantineShowCmd() *cobra.Command {
	var (
		repo     string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <team> <quarantine-path>",
		Short: "Show one team-owned quarantined outbox file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team team outbox quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readTeamOutboxQuarantineItem(teamDir, args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine show: %v\n", err)
				return exitErr(1)
			}
			result, err := showOutboxQuarantine(teamDir, item.Path)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine show: %v\n", err)
				return exitErr(1)
			}
			result.Team = args[0]
			if commands {
				return renderOutboxQuarantineCommands(cmd.OutOrStdout(), result, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderOutboxQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the team-owned quarantined outbox file as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team-owned quarantined outbox file with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newTeamOutboxQuarantineRestoreCmd() *cobra.Command {
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
		Use:   "restore <team> [quarantine-path]",
		Short: "Restore team-owned quarantined outbox files.",
		Long:  "Restore one team-owned quarantined outbox file by path, or restore a filtered team-owned batch of restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team team outbox quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: --limit must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if restoreAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: --all requires exactly one team and cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: %v\n", err)
					return exitErr(2)
				}
				items, err := collectTeamOutboxQuarantineItems(teamDir, args[0], filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: %v\n", err)
					return exitErr(1)
				}
				items = filterOutboxQuarantineRestorable(items, true, false)
				results, err := restoreOutboxQuarantineItems(teamDir, items, dryRun, force, sortMode, limit)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxQuarantineRestoreResultsHaveDryRunAction(results, "would_restore"), outboxApplyCommandOptions{
						BaseArgs: []string{"agent-team", "team", "outbox", "quarantine", "restore", args[0]},
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
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: requires <team> and one path unless --all is set.")
				return exitErr(2)
			}
			if !outboxQuarantineFiltersEmpty(filters) || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			if _, err := readTeamOutboxQuarantineItem(teamDir, args[0], args[1]); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: %v\n", err)
				return exitErr(1)
			}
			result, err := restoreOutboxQuarantine(teamDir, args[1], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine restore: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_restore", outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "team", "outbox", "quarantine", "restore", args[0], result.Path},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
					Force:    force,
				})
			}
			return renderOutboxQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching team-owned restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active outbox file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching team-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, restore at most this many matching team-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching team outbox quarantine restore apply command when the preview has actionable work.")
	cmd.Flags().StringVar(&format, "format", "", "Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newTeamOutboxQuarantineDropCmd() *cobra.Command {
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
		Use:   "drop <team> [quarantine-path]",
		Short: "Drop team-owned quarantined outbox files after inspection.",
		Long:  "Drop one team-owned quarantined outbox file by path, or drop a filtered team-owned batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: --limit must be >= 0.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team team outbox quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: --all requires exactly one team and cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: %v\n", err)
					return exitErr(2)
				}
				items, err := collectTeamOutboxQuarantineItems(teamDir, args[0], filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: %v\n", err)
					return exitErr(1)
				}
				items = filterOutboxQuarantineRestorable(items, restorable, unrestorable)
				results, err := dropOutboxQuarantineItems(teamDir, items, dryRun, olderThan, sortMode, limit, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxQuarantineDropResultsHaveDryRunAction(results, "would_drop"), outboxApplyCommandOptions{
						BaseArgs:     []string{"agent-team", "team", "outbox", "quarantine", "drop", args[0]},
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
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: requires <team> and one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !outboxQuarantineFiltersEmpty(filters) || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			item, err := readTeamOutboxQuarantineItem(teamDir, args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: %v\n", err)
				return exitErr(1)
			}
			result, err := dropOutboxQuarantineItem(daemon.OutboxRoot(teamDir), item, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox quarantine drop: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_drop", outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "team", "outbox", "quarantine", "drop", args[0], result.Path},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
				})
			}
			return renderOutboxQuarantineDrop(cmd.OutOrStdout(), []outboxQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching team-owned quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching team-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching team-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching team outbox quarantine drop apply command when the preview has actionable work.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func runTeamOutboxList(w io.Writer, teamDir, name string, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template) error {
	items, err := collectTeamOutboxItems(teamDir, name)
	if err != nil {
		return err
	}
	return runOutboxListItems(w, items, filters, opts, jsonOut, tmpl)
}

func runTeamOutboxListCommands(w io.Writer, teamDir, name string, filters outboxListFilters, opts outboxListOptions, scope operatorCommandScope) error {
	items, err := collectTeamOutboxItems(teamDir, name)
	if err != nil {
		return err
	}
	return renderOutboxListCommands(w, items, filters, opts, teamOutboxActionResolver(name), scope)
}

func runTeamOutboxSummary(w io.Writer, teamDir, name string, filters outboxListFilters, jsonOut bool) error {
	items, err := collectTeamOutboxItems(teamDir, name)
	if err != nil {
		return err
	}
	return renderOutboxSummaryForItems(w, items, filters, jsonOut)
}

func runTeamOutboxListWatch(ctx context.Context, w io.Writer, teamDir, name string, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
	return runOutboxWatch(ctx, w, jsonOut, interval, clear, func() error {
		return runTeamOutboxList(w, teamDir, name, filters, opts, jsonOut, tmpl)
	})
}

func runTeamOutboxSummaryWatch(ctx context.Context, w io.Writer, teamDir, name string, filters outboxListFilters, jsonOut bool, interval time.Duration, clear bool) error {
	return runOutboxWatch(ctx, w, jsonOut, interval, clear, func() error {
		return runTeamOutboxSummary(w, teamDir, name, filters, jsonOut)
	})
}

func runTeamOutboxRetryAll(w io.Writer, teamDir, name string, filters outboxListFilters, opts outboxListOptions, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := teamOutboxRetryAllResults(teamDir, name, filters, opts, dryRun)
	if err != nil {
		return err
	}
	return renderOutboxActionResults(w, results, jsonOut, tmpl)
}

func teamOutboxRetryAllResults(teamDir, name string, filters outboxListFilters, opts outboxListOptions, dryRun bool) ([]outboxActionResult, error) {
	matches, err := filteredTeamOutboxItems(teamDir, name, filters, opts)
	if err != nil {
		return nil, err
	}
	return retryOutboxItemMatches(teamDir, matches, dryRun)
}

func runTeamOutboxDropAll(w io.Writer, teamDir, name string, filters outboxListFilters, opts outboxListOptions, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := teamOutboxDropAllResults(teamDir, name, filters, opts, dryRun)
	if err != nil {
		return err
	}
	return renderOutboxActionResults(w, results, jsonOut, tmpl)
}

func teamOutboxDropAllResults(teamDir, name string, filters outboxListFilters, opts outboxListOptions, dryRun bool) ([]outboxActionResult, error) {
	matches, err := filteredTeamOutboxItems(teamDir, name, filters, opts)
	if err != nil {
		return nil, err
	}
	return dropOutboxItemMatches(teamDir, matches, dryRun)
}

func runTeamOutboxPrune(w io.Writer, teamDir, name string, state string, olderThan time.Duration, now time.Time, dryRun bool, filters outboxListFilters, limit int, jsonOut bool, tmpl *template.Template) error {
	results, err := teamOutboxPruneResults(teamDir, name, state, olderThan, now, dryRun, filters, limit)
	if err != nil {
		return err
	}
	return renderOutboxPruneResults(w, results, jsonOut, tmpl)
}

func teamOutboxPruneResults(teamDir, name string, state string, olderThan time.Duration, now time.Time, dryRun bool, filters outboxListFilters, limit int) ([]outboxPruneResult, error) {
	items, err := collectTeamOutboxItems(teamDir, name)
	if err != nil {
		return nil, err
	}
	return pruneOutboxItemsFromList(teamDir, items, state, olderThan, now, dryRun, filters, limit)
}

func filteredTeamOutboxItems(teamDir, name string, filters outboxListFilters, opts outboxListOptions) ([]*daemon.OutboxItem, error) {
	items, err := collectTeamOutboxItems(teamDir, name)
	if err != nil {
		return nil, err
	}
	return prepareOutboxActionMatches(filterOutboxItems(items, filters), opts), nil
}

func collectTeamOutboxItems(teamDir, name string) ([]*daemon.OutboxItem, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return nil, err
	}
	return teamOutboxItems(top, team, teamJobs(top, team, jobs), items), nil
}

func collectTeamOutboxQuarantineItems(teamDir, name string, filters outboxListFilters) ([]outboxQuarantineItem, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	items, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = teamOutboxQuarantineItems(top, team, teamJobs(top, team, jobs), items)
	return filterOutboxQuarantineItems(items, filters), nil
}

func readTeamOutboxQuarantineItem(teamDir, name, rawPath string) (outboxQuarantineItem, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return outboxQuarantineItem{}, err
	}
	jobs, err := job.List(teamDir)
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
	if len(teamOutboxQuarantineItems(top, team, teamJobs(top, team, jobs), []outboxQuarantineItem{item})) == 0 {
		return outboxQuarantineItem{}, fmt.Errorf("quarantined outbox file %q is not owned by team %q", item.Path, name)
	}
	return item, nil
}

func readTeamOutboxItem(cmd *cobra.Command, teamDir, name, id, verb string) (*daemon.OutboxItem, error) {
	item, err := daemon.ReadOutboxItem(teamDir, id)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox %s: outbox item %q not found.\n", verb, id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox %s: %v\n", verb, err)
		return nil, exitErr(1)
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	if len(teamOutboxItems(top, team, teamJobs(top, team, jobs), []*daemon.OutboxItem{item})) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team outbox %s: outbox item %q is not owned by team %q.\n", verb, id, name)
		return nil, exitErr(2)
	}
	return item, nil
}
