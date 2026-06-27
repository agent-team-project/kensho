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

func newJobOutboxCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		types       []string
		sources     []string
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
		Use:   "outbox <job-id>",
		Short: "List or control outbox events owned by one job.",
		Long:  "List sandboxed agent outbox events owned by one durable job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox: --interval must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runJobOutboxSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, j, filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runJobOutboxListWatch(ctx, cmd.OutOrStdout(), teamDir, j, filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runJobOutboxSummary(cmd.OutOrStdout(), teamDir, j, filters, jsonOut)
			}
			return runJobOutboxList(cmd.OutOrStdout(), teamDir, j, filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the job outbox table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate outbox counts instead of rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit job-owned outbox rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.AddCommand(newJobOutboxShowCmd())
	cmd.AddCommand(newJobOutboxRetryCmd())
	cmd.AddCommand(newJobOutboxDropCmd())
	cmd.AddCommand(newJobOutboxPruneCmd())
	cmd.AddCommand(newJobOutboxQuarantineCmd())
	return cmd
}

func newJobOutboxShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <job-id> <id>",
		Short: "Show one outbox event owned by one job.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox show: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			item, err := readJobOutboxItem(cmd.ErrOrStderr(), teamDir, j, args[1], "show")
			if err != nil {
				return err
			}
			return renderOutboxItemResult(cmd.OutOrStdout(), item, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the job-owned outbox item as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newJobOutboxRetryCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		retryAll    bool
		dryRun      bool
		stateFilter string
		types       []string
		sources     []string
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "retry <job-id> [id]",
		Aliases: []string{"requeue"},
		Short:   "Retry outbox events owned by one job.",
		Long:    "Move one job-owned processed or failed outbox event back to pending by id, or retry a filtered job-owned batch with --all. Batch retries default to failed events.",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox retry: %v\n", err)
				return exitErr(2)
			}
			if retryAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox retry: --all requires exactly one job and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox retry: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox retry: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.OutboxStateFailed
				}
				filters, err := parseOutboxFilters(effectiveState, types, sources, nil)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox retry: %v\n", err)
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				return runJobOutboxRetryAll(cmd.OutOrStdout(), teamDir, j, filters, outboxListOptions{Sort: sortMode, Limit: limit}, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox retry: requires <job-id> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(types) > 0 || len(sources) > 0 || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox retry: --state, --type, --source, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if _, err := readJobOutboxItem(cmd.ErrOrStderr(), teamDir, j, args[1], "retry"); err != nil {
				return err
			}
			result, err := retryOutboxItem(teamDir, args[1], dryRun)
			if err != nil {
				return err
			}
			return renderOutboxActionResults(cmd.OutOrStdout(), []outboxActionResult{result}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching job-owned outbox events instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the retry without moving the event.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobOutboxDropCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		dropAll     bool
		dryRun      bool
		stateFilter string
		types       []string
		sources     []string
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <job-id> [id]",
		Short: "Drop outbox events owned by one job.",
		Long:  "Remove one job-owned outbox event by id, or drop a filtered job-owned batch with --all. Batch drops default to failed events.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox drop: %v\n", err)
				return exitErr(2)
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox drop: --all requires exactly one job and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox drop: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox drop: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.OutboxStateFailed
				}
				filters, err := parseOutboxFilters(effectiveState, types, sources, nil)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox drop: %v\n", err)
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				return runJobOutboxDropAll(cmd.OutOrStdout(), teamDir, j, filters, outboxListOptions{Sort: sortMode, Limit: limit}, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox drop: requires <job-id> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(types) > 0 || len(sources) > 0 || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox drop: --state, --type, --source, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if _, err := readJobOutboxItem(cmd.ErrOrStderr(), teamDir, j, args[1], "drop"); err != nil {
				return err
			}
			result, err := dropOutboxItem(teamDir, args[1], dryRun)
			if err != nil {
				return err
			}
			return renderOutboxActionResults(cmd.OutOrStdout(), []outboxActionResult{result}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching job-owned outbox events instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the drop without removing the event.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed. Defaults to failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching outbox events before limiting: state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobOutboxPruneCmd() *cobra.Command {
	var (
		repo      string
		stateFlag string
		olderThan time.Duration
		dryRun    bool
		jsonOut   bool
		format    string
		types     []string
		sources   []string
		limit     int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune <job-id>",
		Short: "Prune old outbox events owned by one job.",
		Long:  "Prune old sandboxed agent outbox events owned by one durable job. By default this removes processed events; pass --state failed, pending, or all for explicit cleanup.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox prune: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxPruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseOutboxPruneState(stateFlag)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox prune: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters("", types, sources, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return runJobOutboxPrune(cmd.OutOrStdout(), teamDir, j, state, olderThan, time.Now().UTC(), dryRun, filters, limit, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFlag, "state", daemon.OutboxStateProcessed, "Outbox state to prune: processed, failed, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune items older than this duration based on processed/failed/update/create time.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance before pruning; repeat or comma-separate values.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Prune at most this many matching job-owned outbox events; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job-owned outbox events that would be pruned without dropping them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each prune result with a Go template, e.g. '{{.ID}} {{.Dropped}}'.")
	return cmd
}

func newJobOutboxQuarantineCmd() *cobra.Command {
	var (
		repo         string
		stateFilter  string
		types        []string
		sources      []string
		restorable   bool
		unrestorable bool
		sortBy       string
		limit        int
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "quarantine <job-id>",
		Short: "List quarantined outbox files owned by one job.",
		Long:  "List quarantined sandboxed agent outbox files owned by one durable job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxQuarantineSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team job outbox quarantine", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			items, err := collectJobOutboxQuarantineItems(teamDir, j, filters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine: %v\n", err)
				return exitErr(1)
			}
			items = filterOutboxQuarantineRestorable(items, restorable, unrestorable)
			items = prepareOutboxQuarantineItems(items, sortMode, limit)
			return renderOutboxQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined files that cannot be restored.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", outboxQuarantineSortFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit quarantined outbox files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each quarantined outbox file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	cmd.AddCommand(newJobOutboxQuarantineShowCmd())
	cmd.AddCommand(newJobOutboxQuarantineRestoreCmd())
	cmd.AddCommand(newJobOutboxQuarantineDropCmd())
	return cmd
}

func newJobOutboxQuarantineShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <job-id> <quarantine-path>",
		Short: "Show one job-owned quarantined outbox file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team job outbox quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			item, err := readJobOutboxQuarantineItem(teamDir, j, args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine show: %v\n", err)
				return exitErr(1)
			}
			result, err := showOutboxQuarantine(teamDir, item.Path)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine show: %v\n", err)
				return exitErr(1)
			}
			return renderOutboxQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the quarantined outbox file as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the quarantined outbox file with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newJobOutboxQuarantineRestoreCmd() *cobra.Command {
	var (
		repo        string
		restoreAll  bool
		dryRun      bool
		force       bool
		stateFilter string
		types       []string
		sources     []string
		sortBy      string
		limit       int
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <job-id> [quarantine-path]",
		Short: "Restore job-owned quarantined outbox files.",
		Long:  "Restore one job-owned quarantined outbox file by path, or restore a filtered batch of job-owned restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team job outbox quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: --limit must be >= 0.")
				return exitErr(2)
			}
			if restoreAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: --all requires exactly one job and cannot be combined with a path.")
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				sortMode, err := parseOutboxQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: %v\n", err)
					return exitErr(2)
				}
				items, err := collectJobOutboxQuarantineItems(teamDir, j, filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: %v\n", err)
					return exitErr(1)
				}
				items = filterOutboxQuarantineRestorable(items, true, false)
				results, err := restoreOutboxQuarantineItems(teamDir, items, dryRun, force, sortMode, limit)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: %v\n", err)
					return exitErr(1)
				}
				return renderOutboxQuarantineRestoreMany(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: requires <job-id> and one path unless --all is set.")
				return exitErr(2)
			}
			if !outboxQuarantineFiltersEmpty(filters) || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if _, err := readJobOutboxQuarantineItem(teamDir, j, args[1]); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: %v\n", err)
				return exitErr(1)
			}
			result, err := restoreOutboxQuarantine(teamDir, args[1], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine restore: %v\n", err)
				return exitErr(1)
			}
			return renderOutboxQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching job-owned restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active outbox file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching job-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, restore at most this many matching job-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobOutboxQuarantineDropCmd() *cobra.Command {
	var (
		repo         string
		dropAll      bool
		dryRun       bool
		stateFilter  string
		types        []string
		sources      []string
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
		Use:   "drop <job-id> [quarantine-path]",
		Short: "Drop job-owned quarantined outbox files after inspection.",
		Long:  "Drop one job-owned quarantined outbox file by path, or drop a filtered job-owned batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: --limit must be >= 0.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team job outbox quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: %v\n", err)
				return exitErr(2)
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: --all requires exactly one job and cannot be combined with a path.")
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				sortMode, err := parseOutboxQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: %v\n", err)
					return exitErr(2)
				}
				items, err := collectJobOutboxQuarantineItems(teamDir, j, filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: %v\n", err)
					return exitErr(1)
				}
				items = filterOutboxQuarantineRestorable(items, restorable, unrestorable)
				results, err := dropOutboxQuarantineItems(teamDir, items, dryRun, olderThan, sortMode, limit, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: %v\n", err)
					return exitErr(1)
				}
				return renderOutboxQuarantineDrop(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: requires <job-id> and one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !outboxQuarantineFiltersEmpty(filters) || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			item, err := readJobOutboxQuarantineItem(teamDir, j, args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: %v\n", err)
				return exitErr(1)
			}
			result, err := dropOutboxQuarantineItem(daemon.OutboxRoot(teamDir), item, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job outbox quarantine drop: %v\n", err)
				return exitErr(1)
			}
			return renderOutboxQuarantineDrop(cmd.OutOrStdout(), []outboxQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching job-owned quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching job-owned quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching job-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func runJobOutboxList(w io.Writer, teamDir string, j *job.Job, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template) error {
	items, err := outboxItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	return runOutboxListItems(w, items, filters, opts, jsonOut, tmpl)
}

func runJobOutboxSummary(w io.Writer, teamDir string, j *job.Job, filters outboxListFilters, jsonOut bool) error {
	items, err := outboxItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	return renderOutboxSummaryForItems(w, items, filters, jsonOut)
}

func runJobOutboxListWatch(ctx context.Context, w io.Writer, teamDir string, j *job.Job, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
	return runOutboxWatch(ctx, w, jsonOut, interval, clear, func() error {
		return runJobOutboxList(w, teamDir, j, filters, opts, jsonOut, tmpl)
	})
}

func runJobOutboxSummaryWatch(ctx context.Context, w io.Writer, teamDir string, j *job.Job, filters outboxListFilters, jsonOut bool, interval time.Duration, clear bool) error {
	return runOutboxWatch(ctx, w, jsonOut, interval, clear, func() error {
		return runJobOutboxSummary(w, teamDir, j, filters, jsonOut)
	})
}

func runJobOutboxRetryAll(w io.Writer, teamDir string, j *job.Job, filters outboxListFilters, opts outboxListOptions, dryRun, jsonOut bool, tmpl *template.Template) error {
	matches, err := filteredOutboxItemsForJob(teamDir, j, filters, opts)
	if err != nil {
		return err
	}
	results, err := retryOutboxItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderOutboxActionResults(w, results, jsonOut, tmpl)
}

func runJobOutboxDropAll(w io.Writer, teamDir string, j *job.Job, filters outboxListFilters, opts outboxListOptions, dryRun, jsonOut bool, tmpl *template.Template) error {
	matches, err := filteredOutboxItemsForJob(teamDir, j, filters, opts)
	if err != nil {
		return err
	}
	results, err := dropOutboxItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderOutboxActionResults(w, results, jsonOut, tmpl)
}

func runJobOutboxPrune(w io.Writer, teamDir string, j *job.Job, state string, olderThan time.Duration, now time.Time, dryRun bool, filters outboxListFilters, limit int, jsonOut bool, tmpl *template.Template) error {
	items, err := outboxItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	results, err := pruneOutboxItemsFromList(teamDir, items, state, olderThan, now, dryRun, filters, limit)
	if err != nil {
		return err
	}
	return renderOutboxPruneResults(w, results, jsonOut, tmpl)
}

func collectJobOutboxQuarantineItems(teamDir string, j *job.Job, filters outboxListFilters) ([]outboxQuarantineItem, error) {
	items, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = jobOutboxQuarantineItems(j, items)
	return filterOutboxQuarantineItems(items, filters), nil
}

func readJobOutboxQuarantineItem(teamDir string, j *job.Job, rawPath string) (outboxQuarantineItem, error) {
	outboxRoot := daemon.OutboxRoot(teamDir)
	rel, err := normalizeOutboxQuarantinePath(rawPath)
	if err != nil {
		return outboxQuarantineItem{}, err
	}
	item, err := inspectOutboxQuarantineFile(outboxRoot, rel)
	if err != nil {
		return outboxQuarantineItem{}, err
	}
	if !outboxQuarantineItemMatchesJob(item, j) {
		id := ""
		if j != nil {
			id = j.ID
		}
		return outboxQuarantineItem{}, fmt.Errorf("quarantined outbox file %q is not owned by job %q", item.Path, id)
	}
	return item, nil
}

func jobOutboxQuarantineItems(j *job.Job, items []outboxQuarantineItem) []outboxQuarantineItem {
	if j == nil {
		return nil
	}
	out := make([]outboxQuarantineItem, 0, len(items))
	for _, item := range items {
		if outboxQuarantineItemMatchesJob(item, j) {
			out = append(out, item)
		}
	}
	return out
}

func outboxQuarantineItemMatchesJob(item outboxQuarantineItem, j *job.Job) bool {
	if j == nil {
		return false
	}
	if id := normalizeOutboxJob(item.Job); id != "" && id == j.ID {
		return true
	}
	if strings.TrimSpace(j.Instance) != "" && item.Instance == j.Instance {
		return true
	}
	return false
}

func filteredOutboxItemsForJob(teamDir string, j *job.Job, filters outboxListFilters, opts outboxListOptions) ([]*daemon.OutboxItem, error) {
	items, err := outboxItemsForJob(teamDir, j)
	if err != nil {
		return nil, err
	}
	filtered := filterOutboxItems(items, filters)
	return prepareOutboxActionMatches(filtered, opts), nil
}

func outboxItemsForJob(teamDir string, j *job.Job) ([]*daemon.OutboxItem, error) {
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return nil, err
	}
	return outboxItemsForJobs(items, []*job.Job{j}), nil
}

func readJobOutboxItem(cmdErr io.Writer, teamDir string, j *job.Job, id, verb string) (*daemon.OutboxItem, error) {
	item, err := daemon.ReadOutboxItem(teamDir, id)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(cmdErr, "agent-team job outbox %s: outbox item %q not found.\n", verb, id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	if len(outboxItemsForJobs([]*daemon.OutboxItem{item}, []*job.Job{j})) == 0 {
		fmt.Fprintf(cmdErr, "agent-team job outbox %s: outbox item %q is not owned by job %q.\n", verb, id, j.ID)
		return nil, exitErr(2)
	}
	return item, nil
}

func retryOutboxItemMatches(teamDir string, items []*daemon.OutboxItem, dryRun bool) ([]outboxActionResult, error) {
	results := make([]outboxActionResult, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result, err := retryOutboxItem(teamDir, item.ID, dryRun)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func dropOutboxItemMatches(teamDir string, items []*daemon.OutboxItem, dryRun bool) ([]outboxActionResult, error) {
	results := make([]outboxActionResult, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result, err := dropOutboxItem(teamDir, item.ID, dryRun)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func prepareOutboxActionMatches(items []*daemon.OutboxItem, opts outboxListOptions) []*daemon.OutboxItem {
	sortOutboxItems(items, opts.Sort)
	return limitOutboxItems(items, opts.Limit)
}
