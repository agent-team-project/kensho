package cli

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newJobQueueCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		eventTypes  []string
		runtimes    []string
		readyOnly   bool
		sortBy      string
		limit       int
		watch       bool
		noClear     bool
		interval    time.Duration
		summary     bool
		commands    bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "queue <job-id>",
		Short: "List queue items owned by one job.",
		Long:  "List persisted daemon queue items owned by one durable job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue: --interval must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseQueueListSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFiltersWithRuntime(stateFilter, nil, eventTypes, nil, runtimes, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue: %v\n", err)
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
					return runJobQueueSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, j, filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runJobQueueListWatch(ctx, cmd.OutOrStdout(), teamDir, j, filters, queueListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if commands {
				return runJobQueueListCommands(cmd.OutOrStdout(), teamDir, j, filters, queueListOptions{Sort: sortMode, Limit: limit}, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return runJobQueueList(cmd.OutOrStdout(), teamDir, j, filters, queueListOptions{Sort: sortMode, Limit: limit}, summary, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only show pending queue items whose next retry is due now.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the job queue table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate queue counts instead of queue rows.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from the visible job queue rows, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.AddCommand(newJobQueueShowCmd())
	cmd.AddCommand(newJobQueueQuarantineCmd())
	cmd.AddCommand(newJobQueueRetryCmd())
	cmd.AddCommand(newJobQueueDropCmd())
	cmd.AddCommand(newJobQueuePruneCmd())
	return cmd
}

func newJobQueueShowCmd() *cobra.Command {
	var (
		repo     string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <job-id> <id>",
		Short: "Show one queue item owned by one job.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue show: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			item, err := readJobQueueItem(cmd.ErrOrStderr(), teamDir, j, args[1], "show")
			if err != nil {
				return err
			}
			actions := jobQueueActionResolver(j.ID)
			if commands {
				return renderQueueItemCommands(cmd.OutOrStdout(), item, actions, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderQueueItemResultWithActions(cmd.OutOrStdout(), item, jsonOut, tmpl, actions, queueRuntimeMap(teamDir))
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the queue item as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newJobQueueQuarantineCmd() *cobra.Command {
	var (
		repo         string
		stateFilter  string
		eventTypes   []string
		restorable   bool
		unrestorable bool
		sortBy       string
		limit        int
		summary      bool
		commands     bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "quarantine <job-id>",
		Short: "List quarantined queue files owned by one job.",
		Long:  "List quarantined queue files owned by one durable job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine: --limit must be >= 0.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || limit > 0) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			sortMode, err := parseQueueQuarantineSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team job queue quarantine", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, nil, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			items, err := collectJobQueueQuarantineItems(teamDir, j, filters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine: %v\n", err)
				return exitErr(1)
			}
			items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
			if summary {
				return renderQueueQuarantineSummary(cmd.OutOrStdout(), summarizeQueueQuarantineItems(items), jsonOut)
			}
			items = prepareQueueQuarantineItems(items, sortMode, limit)
			if commands {
				return renderQueueQuarantineListCommands(cmd.OutOrStdout(), items, scopedQueueQuarantineActionResolver(j.ID, "", ""), operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderQueueQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined files that cannot be restored.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", queueQuarantineSortFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate job-owned quarantined queue-file counts instead of rows.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from the visible job-owned quarantined queue files, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit quarantined queue files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	cmd.AddCommand(newJobQueueQuarantineShowCmd())
	cmd.AddCommand(newJobQueueQuarantineRestoreCmd())
	cmd.AddCommand(newJobQueueQuarantineDropCmd())
	return cmd
}

func newJobQueueQuarantineShowCmd() *cobra.Command {
	var (
		repo     string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <job-id> <quarantine-path>",
		Short: "Show one job-owned quarantined queue file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team job queue quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			item, err := readJobQueueQuarantineItem(teamDir, j, args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine show: %v\n", err)
				return exitErr(1)
			}
			result, err := showQueueQuarantine(teamDir, item.Path)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine show: %v\n", err)
				return exitErr(1)
			}
			result.ScopeJob = j.ID
			if commands {
				return renderQueueQuarantineCommands(cmd.OutOrStdout(), result, operatorCommandScopeFromCommand(cmd, repo, "repo"))
			}
			return renderQueueQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the quarantined queue file as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the quarantined queue file with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newJobQueueQuarantineRestoreCmd() *cobra.Command {
	var (
		repo        string
		restoreAll  bool
		dryRun      bool
		force       bool
		stateFilter string
		eventTypes  []string
		sortBy      string
		limit       int
		jsonOut     bool
		format      string
		commands    bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <job-id> [quarantine-path]",
		Short: "Restore job-owned quarantined queue files.",
		Long:  "Restore one job-owned quarantined queue file by path, or restore a filtered batch of job-owned restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team job queue quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, nil, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: --limit must be >= 0.")
				return exitErr(2)
			}
			if restoreAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: --all requires exactly one job and cannot be combined with a path.")
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				sortMode, err := parseQueueQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
					return exitErr(2)
				}
				items, err := collectJobQueueQuarantineItems(teamDir, j, filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				items = filterQueueQuarantineRestorable(items, true, false)
				results, err := restoreQueueQuarantineItems(teamDir, items, dryRun, force, sortMode, limit)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderQueueApplyCommand(cmd.OutOrStdout(), queueQuarantineRestoreResultsHaveDryRunAction(results, "would_restore"), queueApplyCommandOptions{
						BaseArgs:   []string{"agent-team", "job", "queue", "quarantine", "restore", j.ID},
						Repo:       repo,
						RepoSet:    cmd.Flags().Changed("repo"),
						All:        true,
						Force:      force,
						State:      stateFilter,
						StateSet:   cmd.Flags().Changed("state"),
						EventTypes: eventTypes,
						Sort:       sortBy,
						SortSet:    cmd.Flags().Changed("sort"),
						Limit:      limit,
					})
				}
				return renderQueueQuarantineRestoreMany(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: requires <job-id> and one path unless --all is set.")
				return exitErr(2)
			}
			if !filters.empty() || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if _, err := readJobQueueQuarantineItem(teamDir, j, args[1]); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			result, err := restoreQueueQuarantine(teamDir, args[1], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderQueueApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_restore", queueApplyCommandOptions{
					BaseArgs: []string{"agent-team", "job", "queue", "quarantine", "restore", j.ID, result.Path},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
					Force:    force,
				})
			}
			return renderQueueQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching job-owned restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active queue file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching job-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, restore at most this many matching job-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job queue quarantine restore apply command when the preview has actionable work.")
	cmd.Flags().StringVar(&format, "format", "", "Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobQueueQuarantineDropCmd() *cobra.Command {
	var (
		repo         string
		dropAll      bool
		dryRun       bool
		stateFilter  string
		eventTypes   []string
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
		Use:   "drop <job-id> [quarantine-path]",
		Short: "Drop job-owned quarantined queue files after inspection.",
		Long:  "Drop one job-owned quarantined queue file by path, or drop a filtered job-owned batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: --limit must be >= 0.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team job queue quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, nil, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: %v\n", err)
				return exitErr(2)
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: --all requires exactly one job and cannot be combined with a path.")
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				sortMode, err := parseQueueQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: %v\n", err)
					return exitErr(2)
				}
				items, err := collectJobQueueQuarantineItems(teamDir, j, filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
				results, err := dropQueueQuarantineItems(teamDir, items, dryRun, olderThan, unrestorable, sortMode, limit, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderQueueApplyCommand(cmd.OutOrStdout(), queueQuarantineDropResultsHaveDryRunAction(results, "would_drop"), queueApplyCommandOptions{
						BaseArgs:     []string{"agent-team", "job", "queue", "quarantine", "drop", j.ID},
						Repo:         repo,
						RepoSet:      cmd.Flags().Changed("repo"),
						All:          true,
						State:        stateFilter,
						StateSet:     cmd.Flags().Changed("state"),
						EventTypes:   eventTypes,
						Restorable:   restorable,
						Unrestorable: unrestorable,
						Sort:         sortBy,
						SortSet:      cmd.Flags().Changed("sort"),
						Limit:        limit,
						OlderThan:    olderThan,
						OlderThanSet: cmd.Flags().Changed("older-than"),
					})
				}
				return renderQueueQuarantineDrop(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: requires <job-id> and one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !filters.empty() || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			item, err := readJobQueueQuarantineItem(teamDir, j, args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: %v\n", err)
				return exitErr(1)
			}
			result, err := dropQueueQuarantineItem(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), item, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderQueueApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_drop", queueApplyCommandOptions{
					BaseArgs: []string{"agent-team", "job", "queue", "quarantine", "drop", j.ID, result.Path},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
				})
			}
			return renderQueueQuarantineDrop(cmd.OutOrStdout(), []queueQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching job-owned quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching job-owned quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching job-owned quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job queue quarantine drop apply command when the preview has actionable work.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobQueueRetryCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		retryAll    bool
		dryRun      bool
		commands    bool
		stateFilter string
		eventTypes  []string
		runtimes    []string
		readyOnly   bool
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <job-id> [id]",
		Short: "Retry queue items owned by one job.",
		Long:  "Retry one job-owned queue item by id, or retry a filtered job-owned batch with --all. Batch retries default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue retry: %v\n", err)
				return exitErr(2)
			}
			if retryAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: --all requires exactly one job and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseQueueListSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue retry: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFiltersWithRuntime(effectiveState, nil, eventTypes, nil, runtimes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue retry: %v\n", err)
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				if commands {
					results, err := jobQueueRetryAllResults(teamDir, j, filters, sortMode, limit, true)
					if err != nil {
						return err
					}
					return renderQueueApplyCommand(cmd.OutOrStdout(), queueRetryResultsHaveDryRunAction(results, "would_retry"), queueApplyCommandOptions{
						BaseArgs:   []string{"agent-team", "job", "queue", "retry", args[0]},
						Repo:       repo,
						RepoSet:    cmd.Flags().Changed("repo"),
						All:        true,
						State:      stateFilter,
						StateSet:   cmd.Flags().Changed("state"),
						EventTypes: eventTypes,
						Runtimes:   runtimes,
						Ready:      readyOnly,
						Sort:       sortBy,
						SortSet:    cmd.Flags().Changed("sort"),
						Limit:      limit,
					})
				}
				return runJobQueueRetryAll(cmd.OutOrStdout(), teamDir, j, filters, sortMode, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: requires <job-id> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || len(runtimes) > 0 || readyOnly || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: --state, --event-type, --runtime, --ready, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if commands {
				item, err := readJobQueueItem(cmd.ErrOrStderr(), teamDir, j, args[1], "retry")
				if err != nil {
					return err
				}
				return renderQueueApplyCommand(cmd.OutOrStdout(), item != nil, queueApplyCommandOptions{
					BaseArgs: []string{"agent-team", "job", "queue", "retry", args[0], args[1]},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
				})
			}
			return runJobQueueRetryOne(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, j, args[1], dryRun, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching job-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching job-owned queue items without retrying them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job queue retry command when the preview has actionable work.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only retry pending queue items whose next retry is due now.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newJobQueueDropCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		dropAll     bool
		dryRun      bool
		commands    bool
		stateFilter string
		eventTypes  []string
		runtimes    []string
		readyOnly   bool
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <job-id> [id]",
		Short: "Drop queue items owned by one job.",
		Long:  "Drop one job-owned queue item by id, or drop a filtered job-owned batch with --all. Batch drops default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue drop: %v\n", err)
				return exitErr(2)
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: --all requires exactly one job and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseQueueListSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue drop: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFiltersWithRuntime(effectiveState, nil, eventTypes, nil, runtimes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue drop: %v\n", err)
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				if commands {
					results, err := jobQueueDropAllResults(teamDir, j, filters, sortMode, limit, true)
					if err != nil {
						return err
					}
					return renderQueueApplyCommand(cmd.OutOrStdout(), queueDropResultsHaveDryRunAction(results, "would_drop"), queueApplyCommandOptions{
						BaseArgs:   []string{"agent-team", "job", "queue", "drop", args[0]},
						Repo:       repo,
						RepoSet:    cmd.Flags().Changed("repo"),
						All:        true,
						State:      stateFilter,
						StateSet:   cmd.Flags().Changed("state"),
						EventTypes: eventTypes,
						Runtimes:   runtimes,
						Ready:      readyOnly,
						Sort:       sortBy,
						SortSet:    cmd.Flags().Changed("sort"),
						Limit:      limit,
					})
				}
				return runJobQueueDropAll(cmd.OutOrStdout(), teamDir, j, filters, sortMode, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: requires <job-id> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || len(runtimes) > 0 || readyOnly || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: --state, --event-type, --runtime, --ready, --sort, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if commands {
				item, err := readJobQueueItem(cmd.ErrOrStderr(), teamDir, j, args[1], "drop")
				if err != nil {
					return err
				}
				return renderQueueApplyCommand(cmd.OutOrStdout(), item != nil, queueApplyCommandOptions{
					BaseArgs: []string{"agent-team", "job", "queue", "drop", args[0], args[1]},
					Repo:     repo,
					RepoSet:  cmd.Flags().Changed("repo"),
				})
			}
			return runJobQueueDropOne(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, j, args[1], dryRun, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching job-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching job-owned queue items without dropping them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job queue drop command when the preview has actionable work.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only drop pending queue items whose next retry is due now.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newJobQueuePruneCmd() *cobra.Command {
	var (
		repo       string
		stateFlag  string
		olderThan  time.Duration
		dryRun     bool
		commands   bool
		jsonOut    bool
		format     string
		eventTypes []string
		runtimes   []string
		readyOnly  bool
		limit      int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune <job-id>",
		Short: "Prune queue items owned by one job.",
		Long:  "Prune queue items owned by one durable job. By default this removes dead-letter items.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue prune: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue prune: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue prune: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue prune: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueuePruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseQueuePruneStateWithReady(stateFlag, readyOnly, cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue prune: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFiltersWithRuntime("", nil, eventTypes, nil, runtimes, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if commands {
				results, err := jobQueuePruneResults(teamDir, j, state, olderThan, filters, limit, time.Now().UTC(), true)
				if err != nil {
					return err
				}
				return renderQueueApplyCommand(cmd.OutOrStdout(), queuePruneResultsHaveDryRunAction(results), queueApplyCommandOptions{
					BaseArgs:     []string{"agent-team", "job", "queue", "prune", args[0]},
					Repo:         repo,
					RepoSet:      cmd.Flags().Changed("repo"),
					State:        stateFlag,
					StateSet:     cmd.Flags().Changed("state"),
					EventTypes:   eventTypes,
					Runtimes:     runtimes,
					Ready:        readyOnly,
					Limit:        limit,
					OlderThan:    olderThan,
					OlderThanSet: cmd.Flags().Changed("older-than"),
				})
			}
			return runJobQueuePrune(cmd.OutOrStdout(), teamDir, j, state, olderThan, filters, limit, time.Now().UTC(), dryRun, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFlag, "state", daemon.QueueStateDead, "Queue state to prune: dead, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune job-owned items older than this duration based on retry/dead-letter/update time.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Filter by queued dispatch runtime before pruning: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only prune pending queue items whose next retry is due now. Defaults --state to pending when --state is omitted.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Prune at most this many matching job-owned queue items; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job-owned queue items that would be pruned without dropping them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job queue prune command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each prune result with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newJobEventsCmd() *cobra.Command {
	var (
		repo      string
		all       bool
		follow    bool
		tail      string
		types     []string
		actors    []string
		statuses  []string
		instances []string
		since     string
		interval  time.Duration
		jsonOut   bool
		summary   bool
		sortBy    string
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "events [<job-id>|--all]",
		Short: "Show a job's durable event history.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --all cannot be combined with a job id.")
				return exitErr(2)
			}
			if !all && len(args) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: job id is required.")
				return exitErr(2)
			}
			if len(args) > 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: pass at most one job id.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if summary && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --summary cannot be combined with --follow.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --summary cannot be combined with --format.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --interval must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseEventSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
				return exitErr(2)
			}
			if follow && sortMode == "newest" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --sort newest cannot be combined with --follow.")
				return exitErr(2)
			}
			filters, err := newJobEventFilters(types, actors, statuses, instances, since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
				return exitErr(2)
			}
			tailEvents, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobEventFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if all {
				loadJobs := func() ([]*job.Job, error) {
					return job.List(teamDir)
				}
				if follow {
					ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
					defer stop()
					return runScopedJobEventsFollow(ctx, cmd.OutOrStdout(), teamDir, loadJobs, tailEvents, interval, filters, jsonOut, tmpl)
				}
				jobs, err := loadJobs()
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
					return exitErr(1)
				}
				events, err := collectJobEventsForJobs(teamDir, jobs, filters, tailEvents, sortMode)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
					return exitErr(1)
				}
				return renderScopedJobEvents(cmd.OutOrStdout(), "jobs", events, jsonOut, summary, tmpl)
			}
			j, err := job.Read(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
				return exitErr(1)
			}
			if follow {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runJobEventsFollow(ctx, cmd.OutOrStdout(), teamDir, j.ID, tailEvents, interval, filters, jsonOut, tmpl)
			}
			return runJobEvents(cmd.OutOrStdout(), teamDir, j.ID, tailEvents, filters, sortMode, jsonOut, summary, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Show durable events across all jobs.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Poll and print new job events until interrupted.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N events before returning or following (0 or all = all).")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Only show job events with this type. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actors, "actor", nil, "Only show job events from this actor. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only show job events with this status: queued, running, blocked, done, or failed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "Only show job events for this owning instance. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&since, "since", "", "Only show job events since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "Polling interval for --follow.")
	cmd.Flags().StringVar(&sortBy, "sort", "oldest", "Sort returned events by oldest or newest. Follow mode always streams oldest first.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON. With --follow, emit one JSON object per line.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching job events by type, status, actor, and instance.")
	cmd.Flags().StringVar(&format, "format", "", "Render each event with a Go template, e.g. '{{.TS}} {{.Type}} {{.Message}}'.")
	return cmd
}
