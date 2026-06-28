package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "job",
		Aliases: []string{"jobs"},
		Short:   "Manage durable work units.",
		Long: "Manage durable work units backed by `.agent_team/jobs/<job-id>.toml`. " +
			"Jobs track ticket ownership, target agent, lifecycle state, instance, branch, worktree, and PR metadata.",
	}
	cmd.AddCommand(newJobCreateCmd())
	cmd.AddCommand(newJobLsCmd())
	cmd.AddCommand(newJobShowCmd())
	cmd.AddCommand(newJobDoctorCmd())
	cmd.AddCommand(newJobQuarantineCmd())
	cmd.AddCommand(newJobQueueCmd())
	cmd.AddCommand(newJobOutboxCmd())
	cmd.AddCommand(newJobEventsCmd())
	cmd.AddCommand(newJobWaitCmd())
	cmd.AddCommand(newJobStartCmd())
	cmd.AddCommand(newJobAdoptCmd())
	cmd.AddCommand(newJobDispatchCmd())
	cmd.AddCommand(newJobSendCmd())
	cmd.AddCommand(newJobNoteCmd())
	cmd.AddCommand(newJobBlockCmd())
	cmd.AddCommand(newJobUnblockCmd())
	cmd.AddCommand(newJobLogsCmd())
	cmd.AddCommand(newJobResumePlanCmd())
	cmd.AddCommand(newJobPsCmd())
	cmd.AddCommand(newJobStatsCmd())
	cmd.AddCommand(newJobSnapshotCmd())
	cmd.AddCommand(newJobAttachCmd())
	cmd.AddCommand(newJobStopCmd())
	cmd.AddCommand(newJobKillCmd())
	cmd.AddCommand(newJobCloseCmd())
	cmd.AddCommand(newJobCancelCmd())
	cmd.AddCommand(newJobTimeoutCmd())
	cmd.AddCommand(newJobUpdateCmd())
	cmd.AddCommand(newJobHoldCmd())
	cmd.AddCommand(newJobReleaseCmd())
	cmd.AddCommand(newJobReopenCmd())
	cmd.AddCommand(newJobCleanupCmd())
	cmd.AddCommand(newJobRmCmd())
	cmd.AddCommand(newJobPruneCmd())
	cmd.AddCommand(newJobNextCmd())
	cmd.AddCommand(newJobExplainCmd())
	cmd.AddCommand(newJobReadyCmd())
	cmd.AddCommand(newJobTriageCmd())
	cmd.AddCommand(newJobStepCmd())
	cmd.AddCommand(newJobApproveCmd())
	cmd.AddCommand(newJobRejectCmd())
	cmd.AddCommand(newJobAdvanceCmd())
	cmd.AddCommand(newJobReconcileCmd())
	return cmd
}

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
				return renderQueueItemCommands(cmd.OutOrStdout(), item, actions)
			}
			return renderQueueItemResultWithActions(cmd.OutOrStdout(), item, jsonOut, tmpl, actions, queueRuntimeMap(teamDir))
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the queue item as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands.")
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
				return renderQueueQuarantineCommands(cmd.OutOrStdout(), result)
			}
			return renderQueueQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the quarantined queue file as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands.")
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
		repo     string
		follow   bool
		tail     string
		types    []string
		actors   []string
		since    string
		interval time.Duration
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "events <job-id>",
		Short: "Show a job's durable event history.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --interval must be >= 0.")
				return exitErr(2)
			}
			filters, err := newJobEventFilters(types, actors, since, time.Now)
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
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if follow {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runJobEventsFollow(ctx, cmd.OutOrStdout(), teamDir, j.ID, tailEvents, interval, filters, jsonOut, tmpl)
			}
			return runJobEvents(cmd.OutOrStdout(), teamDir, j.ID, tailEvents, filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Poll and print new job events until interrupted.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N events before returning or following (0 or all = all).")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Only show job events with this type. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actors, "actor", nil, "Only show job events from this actor. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&since, "since", "", "Only show job events since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "Polling interval for --follow.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON. With --follow, emit one JSON object per line.")
	cmd.Flags().StringVar(&format, "format", "", "Render each event with a Go template, e.g. '{{.TS}} {{.Type}} {{.Message}}'.")
	return cmd
}

func newJobCreateCmd() *cobra.Command {
	var (
		repo          string
		targetAgent   string
		pipeline      string
		id            string
		ticketURL     string
		kickoff       string
		kickoffFile   string
		instance      string
		dispatchNow   bool
		workspace     string
		runtimeKind   string
		runtimeBin    string
		dryRun        bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "create <ticket> [kickoff...]",
		Short: "Create a durable job for a ticket.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job create: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job create: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job create: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job create: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job create: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			ticket := args[0]
			kickoffText, err := dispatchKickoff(ticket, kickoff, kickoffFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			target := strings.TrimSpace(targetAgent)
			var pipelineDef *topology.Pipeline
			if strings.TrimSpace(pipeline) != "" {
				pipelineDef, err = loadJobCreatePipeline(teamDir, pipeline)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
					return exitErr(2)
				}
				firstTarget := pipelineDef.Steps[0].Target
				if cmd.Flags().Changed("target") && target != firstTarget {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: --target %q does not match first step target %q for pipeline %q.\n", target, firstTarget, pipelineDef.Name)
					return exitErr(2)
				}
				target = firstTarget
			}
			j, err := job.New(ticket, target, kickoffText, time.Now())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			if pipelineDef != nil {
				j.Pipeline = pipelineDef.Name
				j.Steps = jobStepsFromPipeline(pipelineDef)
			}
			if strings.TrimSpace(id) != "" {
				normalized := job.NormalizeID(id)
				if normalized == "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: --id %q produced an empty normalized id.\n", id)
					return exitErr(2)
				}
				j.ID = normalized
			}
			if strings.TrimSpace(ticketURL) != "" {
				j.TicketURL = strings.TrimSpace(ticketURL)
			}
			if strings.TrimSpace(instance) != "" {
				j.Instance = strings.TrimSpace(instance)
			}
			j.LastEvent = "created"
			j.LastStatus = "created"
			if _, err := os.Stat(job.Path(teamDir, j.ID)); err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: job %q already exists.\n", j.ID)
				return exitErr(2)
			}
			if dryRun {
				if dispatchNow {
					if len(j.Steps) > 0 {
						preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
						if err != nil {
							fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
							return exitErr(1)
						}
						return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, tmpl)
					}
					payload, requestedName, err := buildDispatchEventPayload(j.Target, j.Ticket, j.Kickoff, j.Instance, "", workspace)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
						return exitErr(2)
					}
					payload["job_id"] = j.ID
					payload["job"] = j.ID
					if err := applyDispatchRuntimeSelection(teamDir, payload, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
						return exitErr(2)
					}
					preview, err := previewDispatchPayload(teamDir, j.Target, requestedName, payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
						return exitErr(1)
					}
					return renderJobDispatchPreview(cmd.OutOrStdout(), j, preview, jsonOut, tmpl)
				}
				return renderJobCreatePreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			data := map[string]string{
				"ticket": j.Ticket,
				"target": j.Target,
			}
			if j.TicketURL != "" {
				data["ticket_url"] = j.TicketURL
			}
			if j.Pipeline != "" {
				data["pipeline"] = j.Pipeline
			}
			if err := writeJobWithAudit(teamDir, j, "created", "cli", "created "+j.Ticket, data); err != nil {
				return err
			}
			if dispatchNow {
				if len(j.Steps) > 0 {
					res, err := advanceJob(cmd, teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
					if err != nil {
						return err
					}
					if wait {
						waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job create")
						if err != nil {
							if err == context.Canceled {
								return nil
							}
							return err
						}
						refreshJobAdvanceResultAfterWait(res, waited)
					}
					if jsonOut {
						if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
							return err
						}
						if failOnFailed && res.Job.Status == job.StatusFailed {
							return exitErr(1)
						}
						return nil
					}
					if tmpl != nil {
						if err := renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl); err != nil {
							return err
						}
						if failOnFailed && res.Job.Status == job.StatusFailed {
							return exitErr(1)
						}
						return nil
					}
					if err := renderJobAdvanceResult(cmd.OutOrStdout(), res); err != nil {
						return err
					}
					if failOnFailed && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, "", workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, "agent-team job create")
				if err != nil {
					return err
				}
				if wait {
					waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job create")
					if err != nil {
						if err == context.Canceled {
							return nil
						}
						return err
					}
					res.Job = waited
				}
				if jsonOut {
					if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
						return err
					}
					if failOnFailed && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				if tmpl != nil {
					if err := renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl); err != nil {
						return err
					}
					if failOnFailed && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				renderDispatchOutcome(cmd.OutOrStdout(), res.Job.Target, requestedName, res.Event)
				fmt.Fprintf(cmd.OutOrStdout(), "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
				if failOnFailed && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			if wait {
				waited, err := waitForJobCommand(cmd, teamDir, j.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job create")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
				j = waited
			}
			if err := renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && j.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&targetAgent, "target", "worker", "Target agent that should own this job.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Create this job from a declared pipeline in instances.toml.")
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Canonical ticket URL to store on the job.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the target agent.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file, or '-' for stdin.")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name that owns the job (default set during dispatch).")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the created job immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the job that would be created without writing it.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After creating or dispatching, wait for the job to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. dispatched, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func loadJobCreatePipeline(teamDir, name string) (*topology.Pipeline, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("--pipeline requires a non-empty pipeline name")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, fmt.Errorf("pipeline %q not found", name)
	}
	p := top.Pipelines[name]
	if p == nil {
		return nil, fmt.Errorf("pipeline %q not found", name)
	}
	if len(p.Steps) == 0 {
		return nil, fmt.Errorf("pipeline %q has no steps", name)
	}
	return p, nil
}

func jobStepsFromPipeline(p *topology.Pipeline) []job.Step {
	steps := make([]job.Step, 0, len(p.Steps))
	for i, step := range p.Steps {
		status := job.StatusQueued
		if i > 0 || strings.TrimSpace(step.Gate) != "" {
			status = job.StatusBlocked
		}
		steps = append(steps, job.Step{
			ID:           step.ID,
			Label:        step.Label,
			Description:  step.Description,
			Instructions: step.Instructions,
			Target:       step.Target,
			Workspace:    step.Workspace,
			Runtime:      step.Runtime,
			RuntimeBin:   step.RuntimeBin,
			Status:       status,
			After:        append([]string(nil), step.After...),
			Gate:         step.Gate,
			Optional:     step.Optional,
			Timeout:      formatPipelineStepTimeout(step.Timeout),
			MaxAttempts:  step.MaxAttempts,
		})
	}
	return steps
}

func newJobLsCmd() *cobra.Command {
	var (
		repo           string
		statusFilter   string
		targetFilter   string
		instance       string
		pipeline       string
		ticket         string
		branch         string
		pr             string
		runtimeFilters []string
		held           bool
		unheld         bool
		expiredHold    bool
		activeHold     bool
		limit          int
		watch          bool
		noClear        bool
		summary        bool
		jsonOut        bool
		format         string
		sortBy         string
		interval       time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List durable jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && cmd.Flags().Changed("limit") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --interval must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseJobSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
				return exitErr(2)
			}
			if held && unheld {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --held and --unheld cannot be combined.")
				return exitErr(2)
			}
			if expiredHold && activeHold {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --expired-hold and --active-hold cannot be combined.")
				return exitErr(2)
			}
			filters, err := newJobListFilters(statusFilter, targetFilter, instance, pipeline, ticket, branch, pr, runtimeFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
				return exitErr(2)
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
				return runJobSummary(cmd.OutOrStdout(), teamDir, filters, jsonOut)
			}
			return runJobList(cmd.OutOrStdout(), teamDir, filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&statusFilter, "status", "", "Filter by status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&targetFilter, "target-agent", "", "Filter by target agent.")
	cmd.Flags().StringVar(&instance, "instance", "", "Filter by owning instance.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Filter by pipeline name.")
	cmd.Flags().StringVar(&ticket, "ticket", "", "Filter by ticket id or URL substring.")
	cmd.Flags().StringVar(&branch, "branch", "", "Filter by branch.")
	cmd.Flags().StringVar(&pr, "pr", "", "Filter by PR URL or number substring.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Filter by owning instance runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&held, "held", false, "Only show held jobs.")
	cmd.Flags().BoolVar(&unheld, "unheld", false, "Only show jobs that are not held.")
	cmd.Flags().BoolVar(&expiredHold, "expired-hold", false, "Only show held jobs whose hold_until has passed.")
	cmd.Flags().BoolVar(&activeHold, "active-hold", false, "Only show held jobs whose hold is still active or has no deadline.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the job table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate job counts instead of job rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "id", "Sort rows by id, status, target, ticket, created, updated, instance, branch, or pr.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	return cmd
}

func newJobShowCmd() *cobra.Command {
	var (
		repo       string
		eventsTail string
		jsonOut    bool
		format     string
		commands   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "show <job-id>",
		Aliases: []string{"inspect"},
		Short:   "Show one durable job.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			includeEvents := cmd.Flags().Changed("events")
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && includeEvents {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job show: --commands cannot be combined with --events.")
				return exitErr(2)
			}
			if includeEvents && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job show: --events cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job show: %v\n", err)
				return exitErr(2)
			}
			eventTail := 0
			if includeEvents {
				eventTail, err = parseJobShowEventsTail(eventsTail)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job show: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return renderJobShowResult(cmd.OutOrStdout(), teamDir, j, jsonOut, tmpl, includeEvents, eventTail, commands)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&eventsTail, "events", "5", "Include the last N job events in the detail output, or all.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the job as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobWaitCmd() *cobra.Command {
	var (
		repo         string
		statuses     []string
		events       []string
		nextStates   []string
		step         string
		timeout      time.Duration
		interval     time.Duration
		failOnFailed bool
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "wait <job-id>",
		Short: "Wait for a job to reach a lifecycle status, event, or next step.",
		Long: "Wait for a durable job to reach one of the requested lifecycle statuses, last events, or pipeline next-step states. " +
			"By default this waits for a terminal status: done or failed. When --event, --next-state, or --step is set without --status, any lifecycle status is accepted.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: --interval must be >= 0.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: --timeout must be >= 0.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: --format cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			nextStepFilter := strings.TrimSpace(step)
			nextStateChanged := cmd.Flags().Changed("next-state")
			nextStateFilter := map[string]bool{}
			var err error
			if nextStateChanged {
				nextStateFilter, err = parseJobNextStateFilter(nextStates, false)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: %s\n", strings.ReplaceAll(err.Error(), "--state", "--next-state"))
					return exitErr(2)
				}
			}
			waitEvents := parseJobWaitEvents(events)
			defaultStatus := !cmd.Flags().Changed("status") && len(waitEvents) == 0 && !nextStateChanged && nextStepFilter == ""
			waitStatuses, err := parseJobWaitStatuses(statuses, defaultStatus)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: %v\n", err)
				return exitErr(2)
			}
			if len(waitStatuses) == 0 && len(waitEvents) == 0 && !nextStateChanged && nextStepFilter == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: pass at least one non-empty --status, --event, --next-state, or --step.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			cancel := func() {}
			if timeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, timeout)
			}
			defer cancel()
			j, err := runJobWait(ctx, teamDir, args[0], waitStatuses, waitEvents, nextStateFilter, nextStateChanged, nextStepFilter, interval)
			if err != nil {
				if timeoutErr, ok := err.(*jobWaitTimeoutError); ok {
					if !quiet {
						status := "unknown"
						event := ""
						if timeoutErr.Job != nil {
							status = string(timeoutErr.Job.Status)
							event = strings.TrimSpace(timeoutErr.Job.LastEvent)
						}
						if nextStateChanged || nextStepFilter != "" {
							next := jobNextResult{}
							if timeoutErr.Job != nil {
								next = inspectNextJobStep(timeoutErr.Job)
							}
							fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: timed out waiting for %s to reach %s (current=%s event=%s next_state=%s step=%s).\n",
								job.NormalizeID(args[0]), jobWaitConditionList(waitStatuses, waitEvents, nextStateFilter, nextStateChanged, nextStepFilter), status, emptyDash(event), emptyDash(next.State), jobWaitNextStep(next))
						} else if len(waitEvents) > 0 {
							fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: timed out waiting for %s to reach %s (current=%s event=%s).\n",
								job.NormalizeID(args[0]), jobWaitConditionList(waitStatuses, waitEvents, nil, false, ""), status, emptyDash(event))
						} else {
							fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: timed out waiting for %s to reach %s (current=%s).\n",
								job.NormalizeID(args[0]), jobWaitStatusList(waitStatuses), status)
						}
					}
					return exitErr(1)
				}
				if err == context.Canceled {
					return nil
				}
				return err
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(j); err != nil {
					return err
				}
			} else if tmpl != nil {
				if err := renderJobTemplate(cmd.OutOrStdout(), j, tmpl); err != nil {
					return err
				}
			} else if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "  wait   %-20s %s\n", j.ID, j.Status)
			}
			if failOnFailed && j.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&events, "event", nil, "Last event to wait for, e.g. closed, adopted, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&nextStates, "next-state", nil, "Next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&step, "step", "", "Pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 500*time.Millisecond, "Polling interval.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "Exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the final job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the final job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobStartCmd() *cobra.Command {
	var (
		repo         string
		stepID       string
		wait         bool
		timeout      time.Duration
		readyTimeout time.Duration
		dryRun       bool
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "start <job-id>",
		Short: "Start or resume a job's owning instance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job start: %v\n", err)
				return exitErr(2)
			}
			return runJobInstanceUp(cmd, repo, args[0], stepID, instanceUpOptions{
				Wait:    wait,
				Timeout: timeout,
				DryRun:  dryRun,
				Quiet:   quiet,
				JSON:    jsonOut,
				Format:  formatTemplate,
			}, readyTimeout)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the owning instance to become healthy after starting or resuming.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the start/resume action without changing daemon or job state.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable lifecycle action JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newJobDispatchCmd() *cobra.Command {
	var (
		repo          string
		source        string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		dryRun        bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "dispatch <job-id>",
		Short: "Dispatch a job to its target agent.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job dispatch: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job dispatch: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job dispatch: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job dispatch: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job dispatch: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if dryRun {
				payload, requestedName, err := buildDispatchEventPayload(j.Target, j.Ticket, j.Kickoff, j.Instance, source, workspace)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
					return exitErr(2)
				}
				payload["job_id"] = j.ID
				payload["job"] = j.ID
				if err := applyDispatchRuntimeSelection(teamDir, payload, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
					return exitErr(2)
				}
				preview, err := previewDispatchPayload(teamDir, j.Target, requestedName, payload)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
					return exitErr(1)
				}
				return renderJobDispatchPreview(cmd.OutOrStdout(), j, preview, jsonOut, tmpl)
			}
			res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, source, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, "agent-team job dispatch")
			if err != nil {
				return err
			}
			if wait {
				waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job dispatch")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
				res.Job = waited
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				if err := json.NewEncoder(out).Encode(res); err != nil {
					return err
				}
				if failOnFailed && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			if tmpl != nil {
				if err := renderJobTemplate(out, res.Job, tmpl); err != nil {
					return err
				}
				if failOnFailed && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			renderDispatchOutcome(out, res.Job.Target, requestedName, res.Event)
			fmt.Fprintf(out, "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
			if failOnFailed && res.Job.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&source, "source", "", "Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for spawned children: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for the dispatched instance (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for the dispatched instance. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview topology matches without publishing to the daemon or updating the job.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After dispatching, wait for the job to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. dispatched, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job and daemon event outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or dry-run preview with a Go template.")
	return cmd
}

func newJobSendCmd() *cobra.Command {
	var (
		repo         string
		from         string
		message      string
		messageFile  string
		stepID       string
		allowMissing bool
		dryRun       bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "send <job-id> [message...]",
		Short: "Send a mailbox message to a job's owning instance.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job send: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job send: %v\n", err)
				return exitErr(2)
			}
			body, err := sendMessageBody(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job send: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			selection, err := selectJobOwningInstance(j, stepID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job send: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(selection.Instance) == "" {
				printMissingJobInstanceError(cmd.ErrOrStderr(), "send", j, selection.StepID, "dispatch or adopt it first")
				return exitErr(2)
			}
			instance := strings.TrimSpace(selection.Instance)
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			if dryRun {
				if err := runSendWithClient(io.Discard, cmd.ErrOrStderr(), client, instance, body, sendOptions{
					From:         from,
					AllowMissing: allowMissing,
					DryRun:       true,
				}); err != nil {
					return err
				}
				return renderJobSendPreview(cmd.OutOrStdout(), j, instance, from, body, jsonOut, tmpl)
			}
			if err := runSendWithClient(io.Discard, cmd.ErrOrStderr(), client, instance, body, sendOptions{
				From:         from,
				AllowMissing: allowMissing,
			}); err != nil {
				return err
			}
			j.LastEvent = "message_sent"
			j.LastStatus = strings.TrimSpace(body)
			j.UpdatedAt = time.Now().UTC()
			auditData := map[string]string{
				"from":     from,
				"instance": instance,
			}
			if strings.TrimSpace(selection.StepID) != "" {
				auditData["step"] = strings.TrimSpace(selection.StepID)
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", auditData); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  sent   %-20s job=%s\n", instance, j.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().BoolVar(&allowMissing, "allow-missing", false, "Allow queueing a message for an instance the daemon does not know yet.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the send without appending a mailbox message or updating the job.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or batch rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or dry-run preview with a Go template, e.g. '{{.ID}} {{.LastEvent}}'.")
	return cmd
}

func newJobNoteCmd() *cobra.Command {
	var (
		repo        string
		actor       string
		message     string
		messageFile string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "note <job-id> [message...]",
		Short: "Append an operator note to a job's audit history.",
		Long: "Append an operator note to a durable job without sending a mailbox message or changing ownership. " +
			"The note updates last_event and last_status, then records a durable job event.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job note: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job note: %v\n", err)
				return exitErr(2)
			}
			body, err := sendMessageBody(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job note: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			j.LastEvent = "note"
			j.LastStatus = body
			j.UpdatedAt = time.Now().UTC()
			if dryRun {
				return renderJobActionPreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			noteActor := strings.TrimSpace(actor)
			if noteActor == "" {
				noteActor = "cli"
			}
			if err := writeJobWithAudit(teamDir, j, "note", noteActor, body, nil); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&actor, "actor", "cli", "Actor label recorded in the note audit event.")
	cmd.Flags().StringVar(&message, "message", "", "Note text recorded on the job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read note text from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the note without changing job state or writing an audit event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or dry-run preview as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or dry-run preview with a Go template, e.g. '{{.ID}} {{.LastEvent}}'.")
	return cmd
}

func newJobBlockCmd() *cobra.Command {
	var (
		repo        string
		actor       string
		message     string
		messageFile string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "block <job-id> [reason...]",
		Short: "Mark a job blocked with an operator reason.",
		Long: "Mark a durable job blocked and record an operator reason in the job audit history. " +
			"Use `job hold` instead when work should keep its lifecycle status but automation should stop advancing it.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job block: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job block: %v\n", err)
				return exitErr(2)
			}
			reason, err := jobBlockMessage(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job block: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			j.Status = job.StatusBlocked
			j.LastEvent = "blocked"
			j.LastStatus = reason
			j.UpdatedAt = time.Now().UTC()
			if dryRun {
				return renderJobActionPreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			blockActor := strings.TrimSpace(actor)
			if blockActor == "" {
				blockActor = "cli"
			}
			if err := writeJobWithAudit(teamDir, j, "blocked", blockActor, reason, map[string]string{"status": string(job.StatusBlocked)}); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&actor, "actor", "cli", "Actor label recorded in the blocked audit event.")
	cmd.Flags().StringVar(&message, "message", "", "Blocked reason recorded on the job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read blocked reason from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the blocked job without changing job state or writing an audit event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or dry-run preview as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or dry-run preview with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func jobBlockMessage(message, messageFile string, positional []string) (string, error) {
	if strings.TrimSpace(message) == "" && strings.TrimSpace(messageFile) == "" && len(positional) == 0 {
		return "blocked by operator", nil
	}
	return sendMessageBody(message, messageFile, positional)
}

func newJobUnblockCmd() *cobra.Command {
	var (
		repo         string
		from         string
		message      string
		messageFile  string
		stepID       string
		status       string
		force        bool
		allowMissing bool
		dryRun       bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "unblock <job-id> [message...]",
		Short: "Answer a blocked job and mark it ready to continue.",
		Long: "Send an answer to a blocked job's owning instance, then mark the durable job running or queued. " +
			"Use this when a worker reported blocked and the operator has supplied the missing input.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job unblock: --format cannot be combined with --json.")
				return exitErr(2)
			}
			next, err := parseJobUnblockStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job unblock: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job unblock: %v\n", err)
				return exitErr(2)
			}
			body, err := sendMessageBody(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job unblock: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			statusPreviews, err := statusPreviewsForJob(teamDir, j)
			if err != nil {
				return err
			}
			statusPreview, hasStatusBlock := blockedStatusPreviewForUnblock(statusPreviews)
			if hasStatusBlock {
				applyStatusPreviewOwnership(j, statusPreview)
			}
			selection, err := selectJobUnblockInstance(j, stepID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job unblock: %v\n", err)
				return exitErr(2)
			}
			stepBlocked := selectedJobStepHasStatus(j, selection.StepID, job.StatusBlocked)
			if j.Status != job.StatusBlocked && !stepBlocked && !hasStatusBlock && !force {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job unblock: job %q is %s; pass --force to unblock anyway.\n", j.ID, j.Status)
				return exitErr(2)
			}
			instance := strings.TrimSpace(selection.Instance)
			if instance == "" {
				if strings.TrimSpace(selection.StepID) != "" {
					printMissingJobInstanceError(cmd.ErrOrStderr(), "unblock", j, selection.StepID, "dispatch or adopt it first")
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job unblock: job %q has no owning instance; use `agent-team job retry %s --dispatch` to start a new attempt.\n", j.ID, j.ID)
				}
				return exitErr(2)
			}
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			fromLabel := normalizedJobUnblockSender(from)
			if err := runSendWithClient(io.Discard, cmd.ErrOrStderr(), client, instance, body, sendOptions{
				From:         fromLabel,
				AllowMissing: allowMissing,
				DryRun:       dryRun,
			}); err != nil {
				return err
			}
			now := time.Now().UTC()
			j.Status = next
			if strings.TrimSpace(selection.StepID) != "" {
				applySelectedJobStepStatus(j, selection.StepID, next, now)
			}
			j.LastEvent = "unblocked"
			j.LastStatus = body
			j.UpdatedAt = now
			if dryRun {
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(jobUnblockPreview{
						DryRun:        true,
						Job:           j,
						Instance:      instance,
						StepID:        selection.StepID,
						From:          fromLabel,
						Message:       body,
						StatusPreview: hasStatusBlock,
					})
				}
				if tmpl != nil {
					return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  would-unblock   %-20s job=%s status=%s\n", instance, j.ID, j.Status)
				return nil
			}
			data := map[string]string{
				"from":     fromLabel,
				"instance": instance,
				"status":   string(next),
			}
			if strings.TrimSpace(selection.StepID) != "" {
				data["step"] = strings.TrimSpace(selection.StepID)
			}
			if hasStatusBlock {
				data["status_preview"] = "true"
				data["phase"] = statusPreview.Phase
				data["matched_by"] = statusPreview.MatchedBy
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", data); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  unblocked   %-20s job=%s status=%s\n", instance, j.ID, j.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the unblock message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusRunning), "Status after unblocking: running or queued.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Allow unblocking a job not currently marked blocked.")
	cmd.Flags().BoolVar(&allowMissing, "allow-missing", false, "Allow queueing a message for an owning instance the daemon does not know yet.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the unblock without sending a mailbox message or updating the job.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or dry-run preview as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or dry-run preview with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

type jobUnblockPreview struct {
	DryRun        bool     `json:"dry_run"`
	Job           *job.Job `json:"job"`
	Instance      string   `json:"instance"`
	StepID        string   `json:"step_id,omitempty"`
	From          string   `json:"from"`
	Message       string   `json:"message"`
	StatusPreview bool     `json:"status_preview,omitempty"`
}

func parseJobUnblockStatus(raw string) (job.Status, error) {
	status, err := job.ParseStatus(raw)
	if err != nil {
		return "", err
	}
	switch status {
	case job.StatusRunning, job.StatusQueued:
		return status, nil
	default:
		return "", fmt.Errorf("--status must be running or queued")
	}
}

func normalizedJobUnblockSender(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return "(cli)"
	}
	return from
}

func blockedStatusPreviewForUnblock(previews []jobStatusReconcileResult) (jobStatusReconcileResult, bool) {
	for _, preview := range previews {
		if preview.Changed && preview.After == job.StatusBlocked {
			return preview, true
		}
	}
	return jobStatusReconcileResult{}, false
}

func applyStatusPreviewOwnership(j *job.Job, preview jobStatusReconcileResult) {
	if strings.TrimSpace(j.Instance) == "" && strings.TrimSpace(preview.Instance) != "" {
		j.Instance = strings.TrimSpace(preview.Instance)
	}
	if strings.TrimSpace(j.Branch) == "" && strings.TrimSpace(preview.Branch) != "" {
		j.Branch = strings.TrimSpace(preview.Branch)
	}
	if strings.TrimSpace(j.PR) == "" && strings.TrimSpace(preview.PR) != "" {
		j.PR = strings.TrimSpace(preview.PR)
	}
}

type jobInstanceSelection struct {
	Instance string
	StepID   string
}

func selectJobOwningInstance(j *job.Job, requestedStepID string) (jobInstanceSelection, error) {
	if j == nil {
		return jobInstanceSelection{}, nil
	}
	requestedStepID = strings.TrimSpace(requestedStepID)
	if requestedStepID != "" {
		idx := jobStepIndex(j, requestedStepID)
		if idx < 0 {
			return jobInstanceSelection{}, fmt.Errorf("step %q not found", requestedStepID)
		}
		step := j.Steps[idx]
		return jobInstanceSelection{
			Instance: strings.TrimSpace(step.Instance),
			StepID:   step.ID,
		}, nil
	}
	if step := uniqueRunningJobStepWithInstance(j); step != nil {
		return jobInstanceSelection{
			Instance: strings.TrimSpace(step.Instance),
			StepID:   step.ID,
		}, nil
	}
	return jobInstanceSelection{Instance: strings.TrimSpace(j.Instance)}, nil
}

func selectJobUnblockInstance(j *job.Job, requestedStepID string) (jobInstanceSelection, error) {
	if strings.TrimSpace(requestedStepID) != "" {
		return selectJobOwningInstance(j, requestedStepID)
	}
	if step := uniqueJobStepWithStatusAndInstance(j, job.StatusBlocked); step != nil {
		return jobInstanceSelection{
			Instance: strings.TrimSpace(step.Instance),
			StepID:   step.ID,
		}, nil
	}
	return selectJobOwningInstance(j, "")
}

func uniqueRunningJobStepWithInstance(j *job.Job) *job.Step {
	return uniqueJobStepWithStatusAndInstance(j, job.StatusRunning)
}

func uniqueJobStepWithStatusAndInstance(j *job.Job, status job.Status) *job.Step {
	if j == nil {
		return nil
	}
	var found *job.Step
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != status || strings.TrimSpace(step.Instance) == "" {
			continue
		}
		if found != nil {
			return nil
		}
		found = step
	}
	return found
}

func selectedJobStepHasStatus(j *job.Job, stepID string, status job.Status) bool {
	if j == nil || strings.TrimSpace(stepID) == "" {
		return false
	}
	idx := jobStepIndex(j, strings.TrimSpace(stepID))
	return idx >= 0 && j.Steps[idx].Status == status
}

func printMissingJobInstanceError(w io.Writer, command string, j *job.Job, stepID, hint string) {
	if j == nil {
		return
	}
	stepID = strings.TrimSpace(stepID)
	if stepID != "" {
		fmt.Fprintf(w, "agent-team job %s: job %q step %q has no owning instance; %s.\n", command, j.ID, stepID, hint)
		return
	}
	fmt.Fprintf(w, "agent-team job %s: job %q has no owning instance; %s.\n", command, j.ID, hint)
}

func jobStepCommandFlag(stepID string) string {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return ""
	}
	return " --step " + stepID
}

type jobStatsLister struct {
	instanceLister
	job    *job.Job
	stepID string
}

func (l jobStatsLister) Instances() ([]*daemon.Metadata, error) {
	metas, err := l.instanceLister.Instances()
	if err != nil {
		return nil, err
	}
	byInstance := map[string]*daemon.Metadata{}
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		byInstance[meta.Instance] = meta
	}
	if stepID := strings.TrimSpace(l.stepID); stepID != "" {
		idx := jobStepIndex(l.job, stepID)
		if idx < 0 {
			return nil, fmt.Errorf("step %q not found", stepID)
		}
		instance := strings.TrimSpace(l.job.Steps[idx].Instance)
		if instance == "" {
			return nil, nil
		}
		meta := byInstance[instance]
		if meta == nil {
			return nil, nil
		}
		return []*daemon.Metadata{meta}, nil
	}
	return metadataForResumePlanJob(metas, byInstance, l.job), nil
}

func collectJobOwnedInstanceNames(teamDir string, j *job.Job, stepID string) (map[string]bool, error) {
	names := map[string]bool{}
	if j == nil {
		return names, nil
	}
	if stepID = strings.TrimSpace(stepID); stepID != "" {
		idx := jobStepIndex(j, stepID)
		if idx < 0 {
			return nil, fmt.Errorf("step %q not found", stepID)
		}
		if instance := strings.TrimSpace(j.Steps[idx].Instance); instance != "" {
			names[instance] = true
		}
		return names, nil
	}
	if instance := strings.TrimSpace(j.Instance); instance != "" {
		names[instance] = true
	}
	for _, step := range j.Steps {
		if instance := strings.TrimSpace(step.Instance); instance != "" {
			names[instance] = true
		}
	}
	jobID := job.NormalizeID(j.ID)
	if jobID == "" {
		return names, nil
	}
	metas, err := jobDaemonMetadata(teamDir)
	if err != nil {
		return nil, err
	}
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		if job.NormalizeID(meta.Job) == jobID {
			names[meta.Instance] = true
		}
	}
	return names, nil
}

func jobDaemonMetadata(teamDir string) ([]*daemon.Metadata, error) {
	if dc, err := newDaemonClient(teamDir); err == nil {
		return dc.Instances()
	} else if !errors.Is(err, errDaemonNotRunning) {
		return nil, err
	}
	return daemon.ListMetadata(daemon.DaemonRoot(teamDir))
}

func collectJobPsRows(teamDir string, j *job.Job, stepID string, now time.Time, opts psOptions) ([]instanceRow, error) {
	names, err := collectJobOwnedInstanceNames(teamDir, j, stepID)
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

func runJobPs(w io.Writer, teamDir string, j *job.Job, stepID string, now time.Time, opts psOptions) error {
	rows, err := collectJobPsRows(teamDir, j, stepID, now, opts)
	if err != nil {
		return err
	}
	return renderPsTable(w, rows)
}

func runJobPsJSON(w io.Writer, teamDir string, j *job.Job, stepID string, now time.Time, opts psOptions) error {
	rows, err := collectJobPsRows(teamDir, j, stepID, now, opts)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(psJSONRows(rows))
}

func runJobPsQuiet(w io.Writer, teamDir string, j *job.Job, stepID string, now time.Time, opts psOptions) error {
	rows, err := collectJobPsRows(teamDir, j, stepID, now, opts)
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Fprintln(w, row.Instance)
	}
	return nil
}

func runJobPsFormat(w io.Writer, teamDir string, j *job.Job, stepID string, now time.Time, opts psOptions, tmpl *template.Template) error {
	rows, err := collectJobPsRows(teamDir, j, stepID, now, opts)
	if err != nil {
		return err
	}
	return renderPsFormat(w, rows, tmpl)
}

func runJobPsSummary(w io.Writer, teamDir string, j *job.Job, stepID string, now time.Time, opts psOptions) error {
	rows, err := collectJobPsRows(teamDir, j, stepID, now, opts)
	if err != nil {
		return err
	}
	return renderPsSummary(w, psSummaryRows(rows))
}

func runJobPsSummaryJSON(w io.Writer, teamDir string, j *job.Job, stepID string, now time.Time, opts psOptions) error {
	rows, err := collectJobPsRows(teamDir, j, stepID, now, opts)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(psSummaryRows(rows))
}

func runJobPsWatch(ctx context.Context, w io.Writer, teamDir string, j *job.Job, stepID string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runJobPsJSON(w, teamDir, j, stepID, now(), opts); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runJobPs(w, teamDir, j, stepID, now(), opts); err != nil {
				return err
			}
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func runJobPsSummaryWatch(ctx context.Context, w io.Writer, teamDir string, j *job.Job, stepID string, interval time.Duration, now func() time.Time, jsonOut bool, opts psOptions, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if jsonOut {
			if err := runJobPsSummaryJSON(w, teamDir, j, stepID, now(), opts); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := runJobPsSummary(w, teamDir, j, stepID, now(), opts); err != nil {
				return err
			}
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func runJobPsFormatWatch(ctx context.Context, w io.Writer, teamDir string, j *job.Job, stepID string, interval time.Duration, now func() time.Time, opts psOptions, tmpl *template.Template) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := runJobPsFormat(w, teamDir, j, stepID, now(), opts, tmpl); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
	}
}

func newJobLogsCmd() *cobra.Command {
	var (
		repo   string
		stepID string
		follow bool
		tail   string
		since  string
		grep   string
		last   bool
		clean  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "Show a job's owning instance log.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			selection, err := selectJobOwningInstance(j, stepID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: %v\n", err)
				return exitErr(2)
			}
			instance := strings.TrimSpace(selection.Instance)
			if instance == "" {
				printMissingJobInstanceError(cmd.ErrOrStderr(), "logs", j, selection.StepID, "dispatch or adopt it first")
				return exitErr(2)
			}
			if last {
				if follow {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job logs: --last-message cannot be combined with --follow.")
					return exitErr(2)
				}
				if cmd.Flags().Changed("tail") {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job logs: --last-message cannot be combined with --tail.")
					return exitErr(2)
				}
				if strings.TrimSpace(since) != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job logs: --last-message cannot be combined with --since.")
					return exitErr(2)
				}
				if strings.TrimSpace(grep) != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job logs: --last-message cannot be combined with --grep.")
					return exitErr(2)
				}
				if clean {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job logs: --last-message cannot be combined with --clean.")
					return exitErr(2)
				}
				return streamSelectedLastMessageWithPrefix(cmd, teamDir, logListRow{Instance: instance}, "agent-team job logs")
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: %v\n", err)
				return exitErr(2)
			}
			sinceCutoff, err := parseLogSince(since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: %v\n", err)
				return exitErr(2)
			}
			grepPattern, err := parseLogGrep(grep)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: %v\n", err)
				return exitErr(2)
			}
			return runLogs(cmd, filepath.Dir(teamDir), []string{instance}, logsOptions{
				Follow:  follow,
				Tail:    tailLines,
				TailSet: cmd.Flags().Changed("tail"),
				Since:   sinceCutoff,
				Grep:    grepPattern,
				Clean:   clean,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail the owning instance log; print new bytes as they appear.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only print the log if it was modified since a duration ago (for example 10m, 24h) or RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	cmd.Flags().BoolVar(&last, "last-message", false, "Show the clean final Codex response sidecar for the owning instance.")
	cmd.Flags().BoolVar(&clean, "clean", false, "Hide known Codex runtime diagnostic noise when printing the raw owning instance log.")
	return cmd
}

func newJobPsCmd() *cobra.Command {
	var (
		repo             string
		stepID           string
		all              bool
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
		Use:   "ps <job-id>",
		Short: "List instances owned by one job.",
		Long: "List daemon-aware instance rows owned by one durable job. " +
			"Pipeline jobs can own several stage instances; pass --step to focus one stage.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ps: --interval must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ps: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ps: choose one of --latest or --last.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ps: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ps: --quiet cannot be combined with --watch.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ps: --quiet cannot be combined with --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ps: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			opts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(statusFilters, runtimeFilters, agentFilters, phaseFilters, nil, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ps: %v\n", err)
				return exitErr(2)
			}
			opts.runtimeStale = runtimeStaleOnly
			sortMode, err := parsePsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ps: %v\n", err)
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
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ps: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(stepID) != "" && jobStepIndex(j, stepID) < 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ps: step %q not found\n", strings.TrimSpace(stepID))
				return exitErr(2)
			}
			if quiet {
				return runJobPsQuiet(cmd.OutOrStdout(), teamDir, j, stepID, time.Now(), opts)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				switch {
				case summary:
					return runJobPsSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, j, stepID, interval, time.Now, jsonOut, opts, clear)
				case formatTemplate != nil:
					return runJobPsFormatWatch(ctx, cmd.OutOrStdout(), teamDir, j, stepID, interval, time.Now, opts, formatTemplate)
				default:
					return runJobPsWatch(ctx, cmd.OutOrStdout(), teamDir, j, stepID, interval, time.Now, jsonOut, opts, clear)
				}
			}
			switch {
			case summary && jsonOut:
				return runJobPsSummaryJSON(cmd.OutOrStdout(), teamDir, j, stepID, time.Now(), opts)
			case summary:
				return runJobPsSummary(cmd.OutOrStdout(), teamDir, j, stepID, time.Now(), opts)
			case jsonOut:
				return runJobPsJSON(cmd.OutOrStdout(), teamDir, j, stepID, time.Now(), opts)
			case formatTemplate != nil:
				return runJobPsFormat(cmd.OutOrStdout(), teamDir, j, stepID, time.Now(), opts, formatTemplate)
			default:
				return runJobPs(cmd.OutOrStdout(), teamDir, j, stepID, time.Now(), opts)
			}
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "List this pipeline step's owning instance.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show all visible job-owned instances. Accepted for Docker compatibility; this is already the default.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh job instance rows until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON array per refresh.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only print matching job-owned instance names.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show lifecycle counts instead of job instance rows.")
	cmd.Flags().BoolVarP(&latest, "latest", "l", false, "Show only the most recently started job-owned instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started job-owned instances after other filters (0 = all).")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show job-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show job-owned running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed, status-stale, or runtime-stale job-owned instances.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Instance}} {{.Status}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort rows by name, status, agent, phase, stale, runtime-stale, unhealthy, started, stopped, or exited.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show job-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show job-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show job-owned instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show job-owned work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	return cmd
}

func newJobStatsCmd() *cobra.Command {
	var (
		repo             string
		stepID           string
		all              bool
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
		agentFilters     []string
		phaseFilters     []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "stats <job-id>",
		Aliases: []string{"top"},
		Short:   "Show CPU and memory usage for a job's instances.",
		Long: "Show a one-shot or watchable resource snapshot for daemon-known instances owned by one durable job. " +
			"Pipeline jobs can own several stage instances; pass --step to focus one stage. With no filters, only running job-owned instances are shown.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job stats: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job stats: choose one of --latest or --last.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job stats: --interval must be >= 0.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job stats: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseStatsFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job stats: %v\n", err)
				return exitErr(2)
			}
			opts, err := newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(all, statusFilters, runtimeFilters, agentFilters, phaseFilters, nil, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job stats: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseStatsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job stats: %v\n", err)
				return exitErr(2)
			}
			opts.Sort = sortMode
			opts.SortSet = cmd.Flags().Changed("sort")
			opts.Latest = latest
			opts.Limit = last
			opts.Stale = staleOnly
			opts.RuntimeStale = runtimeStaleOnly
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(stepID) != "" && jobStepIndex(j, stepID) < 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job stats: step %q not found\n", strings.TrimSpace(stepID))
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
			lister := jobStatsLister{instanceLister: base, job: j, stepID: stepID}
			var renderErr error
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				switch {
				case summary:
					renderErr = runStatsSummaryWatchWithClear(ctx, cmd.OutOrStdout(), lister, nil, opts, interval, time.Now, readProcessStats, jsonOut, clear)
				case formatTemplate != nil:
					renderErr = runStatsFormatWatch(ctx, cmd.OutOrStdout(), lister, nil, opts, interval, time.Now, readProcessStats, formatTemplate)
				default:
					renderErr = runStatsWatchWithClear(ctx, cmd.OutOrStdout(), lister, nil, opts, interval, time.Now, readProcessStats, jsonOut, clear)
				}
			} else {
				switch {
				case summary && jsonOut:
					renderErr = runStatsSummaryJSON(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats)
				case summary:
					renderErr = runStatsSummary(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats)
				case jsonOut:
					renderErr = runStatsJSON(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats)
				case formatTemplate != nil:
					renderErr = runStatsFormat(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats, formatTemplate)
				default:
					renderErr = runStats(cmd.OutOrStdout(), lister, nil, opts, time.Now(), readProcessStats)
				}
			}
			if renderErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job stats: %v\n", renderErr)
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Show stats for this pipeline step's owning instance.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include stopped, exited, and crashed job-owned instances.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show stats for the most recently started job-owned instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show stats for the N most recently started job-owned instances after other filters (0 = all).")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh job stats until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON array per refresh.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate CPU, memory, and RSS totals instead of job instance rows.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Instance}} {{.CPUPercent}} {{.RSS}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort rows by name, cpu, mem, rss, status, agent, phase, stale, runtime-stale, or unhealthy.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show job-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show job-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show job-owned instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show job-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show job-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show job-owned running instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed, status-stale, or runtime-stale job-owned instances.")
	return cmd
}

func newJobAttachCmd() *cobra.Command {
	var (
		repo     string
		stepID   string
		noResume bool
		dryRun   bool
		noFollow bool
		tail     string
		since    string
		grep     string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "attach <job-id>",
		Aliases: []string{"exec"},
		Short:   "Attach to a job's owning instance.",
		Long: "Attach to the instance recorded on a durable job. By default this opens " +
			"the owning instance with the normal interactive attach flow. Passing log " +
			"options such as --tail, --no-follow, --since, or --grep follows the daemon-captured log stream instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			selection, err := selectJobOwningInstance(j, stepID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job attach: %v\n", err)
				return exitErr(2)
			}
			instance := strings.TrimSpace(selection.Instance)
			if instance == "" {
				printMissingJobInstanceError(cmd.ErrOrStderr(), "attach", j, selection.StepID, "dispatch or adopt it first")
				return exitErr(2)
			}
			repoRoot := filepath.Dir(teamDir)
			logMode := noFollow || cmd.Flags().Changed("tail") || strings.TrimSpace(since) != "" || strings.TrimSpace(grep) != ""
			if logMode {
				if dryRun {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job attach: --dry-run cannot be combined with log-follow attach options.")
					return exitErr(2)
				}
				if noResume {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job attach: --no-resume cannot be combined with log-follow attach options.")
					return exitErr(2)
				}
				return runAttachLogMode(cmd, repoRoot, []string{instance}, attachLogOptions{
					NoFollow: noFollow,
					Tail:     tail,
					TailSet:  cmd.Flags().Changed("tail"),
					Since:    since,
					Grep:     grep,
				})
			}
			if err := runAttach(cmd, repoRoot, instance, noResume, dryRun); err != nil {
				return err
			}
			if dryRun {
				renderJobAttachDryRunHints(cmd.OutOrStdout(), teamDir, j, instance, selection.StepID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().BoolVar(&noResume, "no-resume", false, "Leave the owning instance in stopped state when the runtime exits.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the owning instance handoff without stopping or resuming the daemon child.")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "Log mode: print the selected log tail and exit instead of following.")
	cmd.Flags().StringVar(&tail, "tail", "50", "Log mode: show only the last N lines before following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Log mode with --no-follow: only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Log mode with --no-follow: only print log lines matching this regular expression.")
	return cmd
}

func renderJobAttachDryRunHints(w io.Writer, teamDir string, j *job.Job, instance, stepID string) {
	instance = strings.TrimSpace(instance)
	if w == nil || j == nil || instance == "" {
		return
	}
	meta, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), instance)
	if err != nil || meta == nil {
		return
	}
	if lifecycleMetadataSupportsManagedResume(meta) {
		return
	}
	stepFlag := jobStepCommandFlag(stepID)
	fmt.Fprintf(w, "job_logs_command:      agent-team job logs %s%s --follow\n", j.ID, stepFlag)
	if lifecycleMetadataRuntimeKind(meta) == runtimebin.KindCodex {
		fmt.Fprintf(w, "job_last_message_command: agent-team job logs %s%s --last-message\n", j.ID, stepFlag)
	}
}

func newJobStopCmd() *cobra.Command {
	var (
		repo        string
		stepID      string
		force       bool
		wait        bool
		timeout     time.Duration
		waitTimeout time.Duration
		dryRun      bool
		remove      bool
		quiet       bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "stop <job-id>",
		Short: "Stop a job's owning instance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job stop: %v\n", err)
				return exitErr(2)
			}
			return runJobInstanceDown(cmd, repo, args[0], stepID, instanceDownOptions{
				Force:          force,
				Wait:           wait,
				Timeout:        timeout,
				WaitTimeout:    waitTimeout,
				WaitTimeoutSet: cmd.Flags().Changed("wait-timeout"),
				DryRun:         dryRun,
				Remove:         remove,
				Quiet:          quiet,
				JSON:           jsonOut,
				Format:         formatTemplate,
			}, job.StatusBlocked)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if the owning instance does not stop within --timeout.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the owning instance to reach a terminal state.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the stop action without changing daemon or job state.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after stopping.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable lifecycle action JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newJobKillCmd() *cobra.Command {
	var (
		repo        string
		stepID      string
		timeout     time.Duration
		wait        bool
		waitTimeout time.Duration
		dryRun      bool
		remove      bool
		quiet       bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "kill <job-id>",
		Short: "Force-stop a job's owning instance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job kill: %v\n", err)
				return exitErr(2)
			}
			return runJobInstanceDown(cmd, repo, args[0], stepID, instanceDownOptions{
				Force:          true,
				Wait:           wait,
				Timeout:        timeout,
				WaitTimeout:    waitTimeout,
				WaitTimeoutSet: cmd.Flags().Changed("wait-timeout"),
				DryRun:         dryRun,
				Remove:         remove,
				Quiet:          quiet,
				Action:         "kill",
				JSON:           jsonOut,
				Format:         formatTemplate,
			}, job.StatusFailed)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Second, "Grace before SIGKILL escalation.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the owning instance to reach a terminal state.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the kill action without changing daemon or job state.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after killing.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable lifecycle action JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newJobCloseCmd() *cobra.Command {
	var (
		repo        string
		actor       string
		status      string
		message     string
		messageFile string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "close <job-id> [message...]",
		Short: "Close a job as done or failed.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job close: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if status != string(job.StatusDone) && status != string(job.StatusFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job close: --status must be done or failed.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job close: %v\n", err)
				return exitErr(2)
			}
			closeMessage, err := jobCloseMessage(status, message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job close: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			j.Status = job.Status(status)
			j.LastEvent = "closed"
			j.LastStatus = closeMessage
			j.UpdatedAt = time.Now().UTC()
			if dryRun {
				return renderJobActionPreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			closeActor := strings.TrimSpace(actor)
			if closeActor == "" {
				closeActor = "cli"
			}
			if err := writeJobWithAudit(teamDir, j, "closed", closeActor, closeMessage, map[string]string{"status": status}); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&actor, "actor", "cli", "Actor label recorded in the close audit event.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusDone), "Close status: done or failed.")
	cmd.Flags().StringVar(&message, "message", "", "Close message recorded on the job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read close message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the close without changing job state or writing an audit event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func jobCloseMessage(status, message, messageFile string, positional []string) (string, error) {
	if strings.TrimSpace(message) == "" && strings.TrimSpace(messageFile) == "" && len(positional) == 0 {
		return strings.TrimSpace(status), nil
	}
	return sendMessageBody(message, messageFile, positional)
}

func newJobCancelCmd() *cobra.Command {
	var (
		repo           string
		stepID         string
		actor          string
		message        string
		messageFile    string
		stopInstance   bool
		killInstance   bool
		wait           bool
		timeout        time.Duration
		waitTimeout    time.Duration
		remove         bool
		dryRun         bool
		jsonOut        bool
		format         string
		timeoutSet     bool
		waitTimeoutSet bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cancel <job-id> [reason...]",
		Short: "Cancel a job as failed.",
		Long: "Cancel a durable job by marking it failed with a cancelled audit event. " +
			"By default this only updates the job file; pass --stop or --kill to also stop the owning instance.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			timeoutSet = cmd.Flags().Changed("timeout")
			waitTimeoutSet = cmd.Flags().Changed("wait-timeout")
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cancel: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if stopInstance && killInstance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cancel: choose one of --stop or --kill.")
				return exitErr(2)
			}
			if !stopInstance && !killInstance {
				if wait || remove || timeoutSet || waitTimeoutSet {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cancel: --wait, --timeout, --wait-timeout, and --rm require --stop or --kill.")
					return exitErr(2)
				}
			}
			if strings.TrimSpace(stepID) != "" && !stopInstance && !killInstance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cancel: --step requires --stop or --kill.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cancel: --dry-run cannot be combined with --wait.")
				return exitErr(2)
			}
			reason, err := jobCancelMessage(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cancel: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobCancelFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cancel: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			selection := jobInstanceSelection{Instance: strings.TrimSpace(j.Instance)}
			if stopInstance || killInstance {
				var err error
				selection, err = selectJobOwningInstance(j, stepID)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cancel: %v\n", err)
					return exitErr(2)
				}
				if strings.TrimSpace(selection.Instance) == "" {
					printMissingJobInstanceError(cmd.ErrOrStderr(), "cancel", j, selection.StepID, "omit --stop/--kill to cancel only the job file, or dispatch/adopt it first")
					return exitErr(2)
				}
			}
			repoRoot := filepath.Dir(teamDir)
			cancelled := *j
			cancelled.Steps = append([]job.Step(nil), j.Steps...)
			applyJobCancelUpdate(&cancelled, reason)
			if strings.TrimSpace(selection.StepID) != "" {
				applySelectedJobStepStatus(&cancelled, selection.StepID, job.StatusFailed, cancelled.UpdatedAt)
			}
			result := jobCancelResult{
				DryRun:  dryRun,
				Job:     &cancelled,
				Message: reason,
			}
			if stopInstance || killInstance {
				actions, err := runJobCancelInstanceAction(cmd, repoRoot, selection.Instance, jobCancelInstanceOptions{
					Stop:           stopInstance,
					Kill:           killInstance,
					Wait:           wait,
					Timeout:        timeout,
					WaitTimeout:    waitTimeout,
					WaitTimeoutSet: waitTimeoutSet,
					Remove:         remove,
					DryRun:         dryRun,
				})
				if err != nil {
					return err
				}
				result.InstanceActions = actions
			}
			if dryRun {
				return renderJobCancelResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
			}
			*j = cancelled
			data := map[string]string{
				"status":  string(j.Status),
				"message": reason,
			}
			if strings.TrimSpace(selection.Instance) != "" {
				data["instance"] = strings.TrimSpace(selection.Instance)
			} else if strings.TrimSpace(j.Instance) != "" {
				data["instance"] = strings.TrimSpace(j.Instance)
			}
			if strings.TrimSpace(selection.StepID) != "" {
				data["step"] = strings.TrimSpace(selection.StepID)
			}
			if len(result.InstanceActions) > 0 {
				data["instance_action"] = result.InstanceActions[0].Action
			}
			cancelActor := strings.TrimSpace(actor)
			if cancelActor == "" {
				cancelActor = "cli"
			}
			if err := writeJobWithAudit(teamDir, j, "cancelled", cancelActor, reason, data); err != nil {
				return err
			}
			result.Job = j
			return renderJobCancelResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance when combined with --stop or --kill.")
	cmd.Flags().StringVar(&actor, "actor", "cli", "Actor label recorded in the cancellation audit event.")
	cmd.Flags().StringVar(&message, "message", "", "Cancellation reason recorded on the job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read cancellation reason from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&stopInstance, "stop", false, "Gracefully stop the owning instance before recording the cancellation.")
	cmd.Flags().BoolVar(&killInstance, "kill", false, "Force-stop the owning instance before recording the cancellation.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the owning instance to reach a terminal state when --stop or --kill is set.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Grace before --kill escalation, or wait deadline when used with --wait and no --wait-timeout.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after stopping or killing.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the cancellation without changing daemon or job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the cancellation result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the cancellation result with a Go template, e.g. '{{.Job.ID}} {{.Job.Status}}'.")
	return cmd
}

type jobCancelResult struct {
	DryRun          bool                 `json:"dry_run,omitempty"`
	Job             *job.Job             `json:"job"`
	Message         string               `json:"message,omitempty"`
	InstanceActions []instanceDownResult `json:"instance_actions,omitempty"`
}

type jobCancelInstanceOptions struct {
	Stop           bool
	Kill           bool
	Wait           bool
	Timeout        time.Duration
	WaitTimeout    time.Duration
	WaitTimeoutSet bool
	Remove         bool
	DryRun         bool
}

func jobCancelMessage(message, messageFile string, positional []string) (string, error) {
	if strings.TrimSpace(message) == "" && strings.TrimSpace(messageFile) == "" && len(positional) == 0 {
		return "cancelled by operator", nil
	}
	return sendMessageBody(message, messageFile, positional)
}

func applyJobCancelUpdate(j *job.Job, message string) {
	if j == nil {
		return
	}
	j.Status = job.StatusFailed
	j.Held = false
	j.HoldReason = ""
	j.HoldUntil = time.Time{}
	j.LastEvent = "cancelled"
	j.LastStatus = strings.TrimSpace(message)
	j.UpdatedAt = time.Now().UTC()
}

func runJobCancelInstanceAction(cmd *cobra.Command, repoRoot, instance string, opts jobCancelInstanceOptions) ([]instanceDownResult, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" || (!opts.Stop && !opts.Kill) {
		return nil, nil
	}
	action := "stop"
	timeout := opts.Timeout
	force := false
	if opts.Kill {
		action = "kill"
		force = true
		if timeout == 0 {
			timeout = 2 * time.Second
		}
	}
	var buf bytes.Buffer
	downCmd := *cmd
	downCmd.SetOut(&buf)
	downCmd.SetErr(cmd.ErrOrStderr())
	err := runInstanceDownWithOptions(&downCmd, repoRoot, []string{instance}, instanceDownOptions{
		Force:          force,
		Wait:           opts.Wait,
		Timeout:        timeout,
		WaitTimeout:    opts.WaitTimeout,
		WaitTimeoutSet: opts.WaitTimeoutSet,
		DryRun:         opts.DryRun,
		Remove:         opts.Remove,
		Action:         action,
		JSON:           true,
	})
	var rows []instanceDownResult
	if strings.TrimSpace(buf.String()) != "" {
		if decodeErr := json.Unmarshal(buf.Bytes(), &rows); decodeErr != nil && err == nil {
			return nil, fmt.Errorf("decode instance action: %w", decodeErr)
		}
	}
	if err != nil {
		return rows, err
	}
	return rows, nil
}

func renderJobCancelResult(w io.Writer, result jobCancelResult, jsonOut bool, tmpl *template.Template) error {
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
	}
	if result.Job != nil {
		fmt.Fprintf(w, "Job: %s cancelled status=%s\n", result.Job.ID, result.Job.Status)
		if strings.TrimSpace(result.Message) != "" {
			fmt.Fprintf(w, "Reason: %s\n", strings.TrimSpace(result.Message))
		}
		if strings.TrimSpace(result.Job.Instance) != "" && len(result.InstanceActions) == 0 {
			fmt.Fprintf(w, "Instance: %s unchanged\n", result.Job.Instance)
		}
	}
	if len(result.InstanceActions) > 0 {
		fmt.Fprintln(w, "Instance actions:")
		for _, action := range result.InstanceActions {
			detail := strings.TrimSpace(action.Detail)
			if detail == "" {
				detail = action.Status
			}
			fmt.Fprintf(w, "  %s %-20s %s\n", action.Action, action.Instance, detail)
		}
	}
	return nil
}

func newJobTimeoutCmd() *cobra.Command {
	var (
		repo        string
		step        string
		message     string
		messageFile string
		pipeline    string
		targetAgent string
		all         bool
		limit       int
		dryRun      bool
		commands    bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "timeout <job-id>|--all",
		Short: "Mark stale running job work failed.",
		Long: "Mark or preview stale running work for one durable job, or across all jobs with --all. " +
			"Pipeline steps use their step timeout first, then [health].job_stale_after. " +
			"A step-less running job uses [health].job_stale_after.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job timeout: --limit must be >= 0.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job timeout: --all cannot be combined with a job id.")
				return exitErr(2)
			}
			if !all && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job timeout: pass a job id or --all.")
				return exitErr(2)
			}
			if !all && (strings.TrimSpace(pipeline) != "" || strings.TrimSpace(targetAgent) != "") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job timeout: --pipeline and --target-agent require --all.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job timeout: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job timeout: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job timeout: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job timeout: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineTimeoutFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job timeout: %v\n", err)
				return exitErr(2)
			}
			timeoutMessage, err := optionalSendMessageBody(message, messageFile, nil)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job timeout: %v\n", err)
				return exitErr(2)
			}
			results, err := runJobTimeoutCommand(cmd, repo, args, all, step, timeoutMessage, pipeline, targetAgent, limit, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job timeout: %v\n", err)
				return exitErr(1)
			}
			if commands {
				baseArgs := []string{"agent-team", "job", "timeout"}
				if !all {
					baseArgs = append(baseArgs, args[0])
				}
				return renderTimeoutApplyCommands(cmd.OutOrStdout(), results, timeoutCommandOptions{
					BaseArgs:       baseArgs,
					Repo:           repo,
					RepoSet:        cmd.Flags().Changed("repo"),
					All:            all,
					Step:           step,
					StepSet:        cmd.Flags().Changed("step"),
					Pipeline:       pipeline,
					PipelineSet:    cmd.Flags().Changed("pipeline"),
					TargetAgent:    targetAgent,
					TargetAgentSet: cmd.Flags().Changed("target-agent"),
					Limit:          limit,
					Message:        message,
					MessageSet:     cmd.Flags().Changed("message"),
					MessageFile:    messageFile,
					MessageFileSet: cmd.Flags().Changed("message-file"),
				})
			}
			return renderPipelineTimeoutResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&step, "step", "", "Mark only a stale running step with this id failed.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the timed-out job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read timeout message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&all, "all", false, "Mark stale running work across all jobs.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "With --all, mark only stale work owned by this pipeline.")
	cmd.Flags().StringVar(&targetAgent, "target-agent", "", "With --all, mark only stale work targeting this agent.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, mark at most this many stale running jobs or steps failed; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview stale-work failure without writing job state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching timeout apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit timeout results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func runJobTimeoutCommand(cmd *cobra.Command, repo string, args []string, all bool, step, message, pipeline, targetAgent string, limit int, dryRun bool) ([]pipelineTimeoutResult, error) {
	if all {
		teamDir, err := resolveTeamDir(cmd, repo)
		if err != nil {
			return nil, err
		}
		return timeoutAllStaleJobWork(teamDir, step, message, limit, dryRun, jobTimeoutFilters{
			Pipeline:    pipeline,
			TargetAgent: targetAgent,
		})
	}
	teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
	if err != nil {
		return nil, err
	}
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return nil, err
	}
	return timeoutStaleJobWork(teamDir, []*job.Job{j}, step, "", message, 0, dryRun, time.Now().UTC(), staleAfter)
}

type jobTimeoutFilters struct {
	Pipeline    string
	TargetAgent string
}

func timeoutAllStaleJobWork(teamDir, stepFilter, message string, limit int, dryRun bool, filters jobTimeoutFilters) ([]pipelineTimeoutResult, error) {
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	jobs = filterJobTimeoutCandidates(jobs, filters)
	return timeoutStaleJobWork(teamDir, jobs, stepFilter, filters.TargetAgent, message, limit, dryRun, time.Now().UTC(), staleAfter)
}

func filterJobTimeoutCandidates(jobs []*job.Job, filters jobTimeoutFilters) []*job.Job {
	pipeline := strings.TrimSpace(filters.Pipeline)
	target := strings.TrimSpace(filters.TargetAgent)
	if pipeline == "" && target == "" {
		return jobs
	}
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if pipeline != "" && j.Pipeline != pipeline {
			continue
		}
		if target != "" && !jobTargetsAgent(j, target) {
			continue
		}
		out = append(out, j)
	}
	return out
}

func jobTargetsAgent(j *job.Job, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" || j == nil {
		return false
	}
	if j.Target == target {
		return true
	}
	for _, step := range j.Steps {
		if step.Target == target {
			return true
		}
	}
	return false
}

func timeoutStaleJobWork(teamDir string, jobs []*job.Job, stepFilter, targetFilter, message string, limit int, dryRun bool, now time.Time, staleAfter time.Duration) ([]pipelineTimeoutResult, error) {
	results := []pipelineTimeoutResult{}
	stepFilter = strings.TrimSpace(stepFilter)
	targetFilter = strings.TrimSpace(targetFilter)
	for _, j := range jobs {
		if limit > 0 && len(results) >= limit {
			break
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
			break
		}
		if stepFilter != "" {
			continue
		}
		lifecycle, err := timeoutJobLifecycle(teamDir, j, targetFilter, message, dryRun, now, staleAfter)
		if err != nil {
			return nil, err
		}
		results = append(results, lifecycle...)
	}
	return results, nil
}

func timeoutJobLifecycle(teamDir string, j *job.Job, targetFilter, message string, dryRun bool, now time.Time, staleAfter time.Duration) ([]pipelineTimeoutResult, error) {
	if j == nil || len(j.Steps) > 0 || j.Status != job.StatusRunning || staleAfter <= 0 || j.UpdatedAt.IsZero() {
		return nil, nil
	}
	if targetFilter = strings.TrimSpace(targetFilter); targetFilter != "" && j.Target != targetFilter {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	age := now.Sub(j.UpdatedAt)
	if age <= staleAfter {
		return nil, nil
	}
	result := pipelineTimeoutResult{
		JobID:      j.ID,
		Ticket:     j.Ticket,
		Pipeline:   j.Pipeline,
		Target:     j.Target,
		StepStatus: j.Status,
		Instance:   j.Instance,
		Action:     "would_fail",
		DryRun:     dryRun,
		Age:        roundedDurationString(age),
		Timeout:    roundedDurationString(staleAfter),
		Message:    jobTimeoutMessage(j.ID, age, staleAfter, message),
	}
	if dryRun {
		return []pipelineTimeoutResult{result}, nil
	}
	j.Status = job.StatusFailed
	j.LastEvent = "job_timeout"
	j.LastStatus = result.Message
	j.UpdatedAt = now.UTC()
	data := map[string]string{
		"age":     result.Age,
		"timeout": result.Timeout,
	}
	if err := writeJobWithAudit(teamDir, j, "job_timeout", "cli", result.Message, data); err != nil {
		return nil, err
	}
	result.Action = "failed"
	result.DryRun = false
	result.StepStatus = j.Status
	result.Job = j
	return []pipelineTimeoutResult{result}, nil
}

func jobTimeoutMessage(jobID string, age, timeout time.Duration, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	return fmt.Sprintf("timed out job %s after %s (threshold %s)", jobID, roundedDurationString(age), roundedDurationString(timeout))
}

type timeoutCommandOptions struct {
	BaseArgs       []string
	Repo           string
	RepoSet        bool
	All            bool
	IncludeJobs    bool
	Step           string
	StepSet        bool
	Pipeline       string
	PipelineSet    bool
	TargetAgent    string
	TargetAgentSet bool
	Limit          int
	Message        string
	MessageSet     bool
	MessageFile    string
	MessageFileSet bool
}

func renderTimeoutApplyCommands(w io.Writer, results []pipelineTimeoutResult, opts timeoutCommandOptions) error {
	if !timeoutResultsHaveApplyCommand(results) {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(timeoutApplyCommandArgs(opts)), " "))
	return err
}

func timeoutResultsHaveApplyCommand(results []pipelineTimeoutResult) bool {
	for _, result := range results {
		if result.DryRun && strings.TrimSpace(result.Action) == "would_fail" {
			return true
		}
	}
	return false
}

func timeoutApplyCommandArgs(opts timeoutCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.All {
		args = append(args, "--all")
	}
	if opts.IncludeJobs {
		args = append(args, "--jobs")
	}
	if opts.StepSet && strings.TrimSpace(opts.Step) != "" {
		args = append(args, "--step", opts.Step)
	}
	if opts.PipelineSet && strings.TrimSpace(opts.Pipeline) != "" {
		args = append(args, "--pipeline", opts.Pipeline)
	}
	if opts.TargetAgentSet && strings.TrimSpace(opts.TargetAgent) != "" {
		args = append(args, "--target-agent", opts.TargetAgent)
	}
	if opts.Limit > 0 {
		args = append(args, "--limit", fmt.Sprint(opts.Limit))
	}
	if opts.MessageSet && strings.TrimSpace(opts.Message) != "" {
		args = append(args, "--message", opts.Message)
	}
	if opts.MessageFileSet && strings.TrimSpace(opts.MessageFile) != "" {
		args = append(args, "--message-file", opts.MessageFile)
	}
	return args
}

func newJobUpdateCmd() *cobra.Command {
	var (
		repo          string
		status        string
		target        string
		ticketURL     string
		instance      string
		branch        string
		worktree      string
		pr            string
		message       string
		clear         []string
		advance       bool
		workspace     string
		runtimeKind   string
		runtimeBin    string
		dryRun        bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "update <job-id>",
		Short: "Update job metadata.",
		Long:  "Update durable job metadata such as status, owner instance, branch, worktree, and PR URL.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if wait && !advance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: --wait requires --advance.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: wait-related flags require --wait.")
				return exitErr(2)
			}
			var jobTmpl, advanceTmpl *template.Template
			var err error
			if advance {
				advanceTmpl, err = parseJobAdvanceFormat(format)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
					return exitErr(2)
				}
			} else {
				jobTmpl, err = parseJobFormat(format)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
					return exitErr(2)
				}
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
					return exitErr(2)
				}
			}
			clearSet, err := parseJobUpdateClear(clear)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			changed := map[string]string{}
			if cmd.Flags().Changed("status") {
				next, err := job.ParseStatus(status)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
					return exitErr(2)
				}
				j.Status = next
				changed["status"] = string(next)
			}
			if cmd.Flags().Changed("target") {
				if strings.TrimSpace(target) == "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: --target cannot be empty.")
					return exitErr(2)
				}
				j.Target = strings.TrimSpace(target)
				changed["target"] = j.Target
			}
			if cmd.Flags().Changed("ticket-url") {
				j.TicketURL = strings.TrimSpace(ticketURL)
				changed["ticket_url"] = j.TicketURL
			}
			if cmd.Flags().Changed("instance") {
				j.Instance = strings.TrimSpace(instance)
				changed["instance"] = j.Instance
			}
			if cmd.Flags().Changed("branch") {
				j.Branch = strings.TrimSpace(branch)
				changed["branch"] = j.Branch
			}
			if cmd.Flags().Changed("worktree") {
				j.Worktree = strings.TrimSpace(worktree)
				changed["worktree"] = j.Worktree
			}
			if cmd.Flags().Changed("pr") {
				j.PR = strings.TrimSpace(pr)
				changed["pr"] = j.PR
			}
			applyJobUpdateClears(j, clearSet, changed)
			hasUpdate := len(changed) > 0 || strings.TrimSpace(message) != ""
			if !hasUpdate && !advance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: pass at least one update flag.")
				return exitErr(2)
			}
			if hasUpdate {
				j.LastEvent = "updated"
				if strings.TrimSpace(message) != "" {
					j.LastStatus = strings.TrimSpace(message)
				} else {
					j.LastStatus = "updated " + jobUpdateFieldList(changed)
				}
				j.UpdatedAt = time.Now().UTC()
			}
			if dryRun {
				if advance {
					preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
						return exitErr(1)
					}
					return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, advanceTmpl)
				}
				return renderJobUpdatePreview(cmd.OutOrStdout(), j, changed, jsonOut, jobTmpl)
			}
			if hasUpdate {
				if err := writeJobWithAudit(teamDir, j, "", "cli", "", changed); err != nil {
					return err
				}
			}
			if advance {
				res, err := advanceJob(cmd, teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
				if err != nil {
					return err
				}
				if wait && res.Job != nil {
					waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job update")
					if err != nil {
						if err == context.Canceled {
							return nil
						}
						return err
					}
					refreshJobAdvanceResultAfterWait(res, waited)
				}
				if jsonOut {
					if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
						return err
					}
					if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				if advanceTmpl != nil {
					if err := renderJobAdvanceResultFormat(cmd.OutOrStdout(), res, advanceTmpl); err != nil {
						return err
					}
					if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				if err := renderJobAdvanceResult(cmd.OutOrStdout(), res); err != nil {
					return err
				}
				if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, jobTmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&status, "status", "", "Set lifecycle status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&target, "target", "", "Set target agent.")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Set ticket URL.")
	cmd.Flags().StringVar(&instance, "instance", "", "Set owning instance.")
	cmd.Flags().StringVar(&branch, "branch", "", "Set branch.")
	cmd.Flags().StringVar(&worktree, "worktree", "", "Set worktree path.")
	cmd.Flags().StringVar(&pr, "pr", "", "Set PR URL or number.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().StringSliceVar(&clear, "clear", nil, "Clear metadata fields: ticket-url, instance, branch, worktree, pr, or pipeline. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&advance, "advance", false, "After updating metadata, dispatch the next ready pipeline step.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --advance: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --advance dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview metadata updates and optional advance dispatch without writing job or daemon state.")
	cmd.Flags().BoolVar(&wait, "wait", false, "With --advance, wait for the job to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or advance result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.")
	return cmd
}

func newJobHoldCmd() *cobra.Command {
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
		commands    bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "hold <job-id>|--all [reason...]",
		Short: "Hold a job so pipeline automation will not advance it.",
		Long: "Hold a durable job without changing its lifecycle status. " +
			"Held jobs remain visible in status views, but next-step readiness reports held and automatic advance loops skip them until release. Use --all to hold matching jobs in a batch.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job hold: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job hold: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job hold: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job hold: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job hold: --limit must be >= 0.")
				return exitErr(2)
			}
			if limit > 0 && !all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job hold: --limit requires --all.")
				return exitErr(2)
			}
			if cmd.Flags().Changed("state") && !all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job hold: --state requires --all.")
				return exitErr(2)
			}
			var stateFilter map[string]bool
			stateDefault := !cmd.Flags().Changed("state")
			if !stateDefault {
				var err error
				stateFilter, err = parseJobNextStateFilter(states, false)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job hold: %v\n", err)
					return exitErr(2)
				}
			}
			holdUntil, err := parseJobHoldUntil(holdFor, cmd.Flags().Changed("for"), untilRaw, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job hold: %v\n", err)
				return exitErr(2)
			}
			if all {
				tmpl, err := parsePipelineHoldFormat(format)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job hold: %v\n", err)
					return exitErr(2)
				}
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				reason, err := jobActionMessageWithFile(message, messageFile, args, "held")
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job hold: %v\n", err)
					return exitErr(2)
				}
				results, err := holdJobs(teamDir, reason, holdUntil, stateFilter, stateDefault, limit, dryRun)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job hold: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderHoldReleaseApplyCommands(cmd.OutOrStdout(), results, "would_hold", holdReleaseCommandOptions{
						BaseArgs:       []string{"agent-team", "job", "hold"},
						Repo:           repo,
						RepoSet:        cmd.Flags().Changed("repo"),
						All:            true,
						Limit:          limit,
						States:         states,
						StateSet:       cmd.Flags().Changed("state"),
						Message:        message,
						MessageSet:     cmd.Flags().Changed("message"),
						MessageFile:    messageFile,
						MessageFileSet: cmd.Flags().Changed("message-file"),
						HoldFor:        holdFor,
						HoldForSet:     cmd.Flags().Changed("for"),
						Until:          untilRaw,
						UntilSet:       cmd.Flags().Changed("until"),
						PositionalArgs: args,
					})
				}
				return renderPipelineHoldResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
			}
			if len(args) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job hold: pass a job id or --all.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job hold: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			reason, err := jobActionMessageWithFile(message, messageFile, args[1:], "held")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job hold: %v\n", err)
				return exitErr(2)
			}
			heldBefore := j.Held
			holdJobState(j, reason, holdUntil, time.Now().UTC())
			if dryRun {
				if commands {
					return renderHoldReleaseApplyCommands(cmd.OutOrStdout(), []pipelineHoldResult{{
						JobID:      j.ID,
						Ticket:     j.Ticket,
						Pipeline:   j.Pipeline,
						Status:     j.Status,
						NextState:  inspectNextJobStep(j).State,
						Action:     "would_hold",
						Message:    reason,
						HeldBefore: heldBefore,
						HeldAfter:  true,
						HoldUntil:  jobHoldUntilText(j),
						DryRun:     true,
						Job:        j,
					}}, "would_hold", holdReleaseCommandOptions{
						BaseArgs:       []string{"agent-team", "job", "hold", args[0]},
						Repo:           repo,
						RepoSet:        cmd.Flags().Changed("repo"),
						Message:        message,
						MessageSet:     cmd.Flags().Changed("message"),
						MessageFile:    messageFile,
						MessageFileSet: cmd.Flags().Changed("message-file"),
						HoldFor:        holdFor,
						HoldForSet:     cmd.Flags().Changed("for"),
						Until:          untilRaw,
						UntilSet:       cmd.Flags().Changed("until"),
						PositionalArgs: args[1:],
					})
				}
				return renderJobActionPreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			changes := map[string]string{"held": "true"}
			if !j.HoldUntil.IsZero() {
				changes["hold_until"] = jobHoldUntilText(j)
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", changes); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Hold all matching jobs instead of one job.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, hold at most this many matching jobs; 0 means no limit.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "With --all, next-step state to hold: ready, queued, running, blocked, failed, held, done, none, or all. Defaults to active non-held, non-done jobs.")
	cmd.Flags().StringVar(&message, "message", "", "Hold reason recorded on the job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read hold reason from a file, or '-' for stdin.")
	cmd.Flags().DurationVar(&holdFor, "for", 0, "Hold for this duration, for example 30m or 2h.")
	cmd.Flags().StringVar(&untilRaw, "until", "", "Hold until this RFC3339 timestamp.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the hold without writing job state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching hold apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or batch row with a Go template, e.g. '{{.ID}} {{.Held}} {{.HoldReason}}' or '{{.JobID}} {{.Action}}'.")
	return cmd
}

func holdJobState(j *job.Job, reason string, holdUntil time.Time, now time.Time) {
	if j == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	j.Held = true
	j.HoldReason = strings.TrimSpace(reason)
	if j.HoldReason == "" {
		j.HoldReason = "held"
	}
	j.HoldUntil = holdUntil
	j.LastEvent = "held"
	j.LastStatus = j.HoldReason
	j.UpdatedAt = now.UTC()
}

func holdJobs(teamDir, reason string, holdUntil time.Time, stateFilter map[string]bool, stateDefault bool, limit int, dryRun bool) ([]pipelineHoldResult, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	sortJobs(jobs, "id")
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
		if j == nil {
			continue
		}
		next := inspectNextJobStep(j)
		if stateDefault && j.Status == job.StatusDone {
			continue
		}
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
		holdJobState(j, reason, holdUntil, now)
		result.Job = j
		if dryRun {
			results = append(results, result)
			continue
		}
		changes := map[string]string{"held": "true"}
		if strings.TrimSpace(j.Pipeline) != "" {
			changes["pipeline"] = j.Pipeline
		}
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

func newJobReleaseCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		expiredOnly bool
		limit       int
		message     string
		messageFile string
		dryRun      bool
		commands    bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "release <job-id>|--all [message...]",
		Short: "Release a held job so pipeline automation can advance it.",
		Long: "Release a held durable job without changing its lifecycle status. " +
			"After release, ready and advance commands evaluate the job's pipeline steps normally. Use --all to release matching held jobs in a batch.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job release: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job release: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job release: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job release: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job release: --limit must be >= 0.")
				return exitErr(2)
			}
			if expiredOnly && !all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job release: --expired requires --all.")
				return exitErr(2)
			}
			if limit > 0 && !all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job release: --limit requires --all.")
				return exitErr(2)
			}
			if all {
				tmpl, err := parsePipelineHoldFormat(format)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job release: %v\n", err)
					return exitErr(2)
				}
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				statusMessage, err := jobActionMessageWithFile(message, messageFile, args, "released")
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job release: %v\n", err)
					return exitErr(2)
				}
				results, err := releaseJobs(teamDir, statusMessage, limit, expiredOnly, dryRun)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job release: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderHoldReleaseApplyCommands(cmd.OutOrStdout(), results, "would_release", holdReleaseCommandOptions{
						BaseArgs:       []string{"agent-team", "job", "release"},
						Repo:           repo,
						RepoSet:        cmd.Flags().Changed("repo"),
						All:            true,
						Limit:          limit,
						Expired:        expiredOnly,
						Message:        message,
						MessageSet:     cmd.Flags().Changed("message"),
						MessageFile:    messageFile,
						MessageFileSet: cmd.Flags().Changed("message-file"),
						PositionalArgs: args,
					})
				}
				return renderPipelineHoldResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
			}
			if len(args) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job release: pass a job id or --all.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job release: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			statusMessage, err := jobActionMessageWithFile(message, messageFile, args[1:], "released")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job release: %v\n", err)
				return exitErr(2)
			}
			heldBefore := j.Held
			holdUntilBefore := jobHoldUntilText(j)
			releaseJobState(j, statusMessage, time.Now().UTC())
			if dryRun {
				if commands {
					return renderHoldReleaseApplyCommands(cmd.OutOrStdout(), []pipelineHoldResult{{
						JobID:      j.ID,
						Ticket:     j.Ticket,
						Pipeline:   j.Pipeline,
						Status:     j.Status,
						NextState:  inspectNextJobStep(j).State,
						Action:     "would_release",
						Message:    statusMessage,
						HeldBefore: heldBefore,
						HeldAfter:  false,
						HoldUntil:  holdUntilBefore,
						DryRun:     true,
						Job:        j,
					}}, "would_release", holdReleaseCommandOptions{
						BaseArgs:       []string{"agent-team", "job", "release", args[0]},
						Repo:           repo,
						RepoSet:        cmd.Flags().Changed("repo"),
						Message:        message,
						MessageSet:     cmd.Flags().Changed("message"),
						MessageFile:    messageFile,
						MessageFileSet: cmd.Flags().Changed("message-file"),
						PositionalArgs: args[1:],
					})
				}
				return renderJobActionPreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"held": "false", "hold_until": ""}); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Release all matching held jobs instead of one job.")
	cmd.Flags().BoolVar(&expiredOnly, "expired", false, "With --all, only release held jobs whose hold_until has passed.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, release at most this many held jobs; 0 means no limit.")
	cmd.Flags().StringVar(&message, "message", "", "Release message recorded on the job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read release message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the release without writing job state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching release apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or batch row with a Go template, e.g. '{{.ID}} {{.Held}} {{.LastStatus}}' or '{{.JobID}} {{.Action}}'.")
	return cmd
}

func releaseJobState(j *job.Job, message string, now time.Time) {
	if j == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	j.Held = false
	j.HoldReason = ""
	j.HoldUntil = time.Time{}
	j.LastEvent = "released"
	j.LastStatus = strings.TrimSpace(message)
	if j.LastStatus == "" {
		j.LastStatus = "released"
	}
	j.UpdatedAt = now.UTC()
}

func releaseJobs(teamDir, message string, limit int, expiredOnly bool, dryRun bool) ([]pipelineHoldResult, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	sortJobs(jobs, "id")
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
		if j == nil || !j.Held {
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
		releaseJobState(j, message, now)
		result.Job = j
		if dryRun {
			results = append(results, result)
			continue
		}
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"held": "false", "hold_until": ""}); err != nil {
			return nil, err
		}
		result.Action = "released"
		result.DryRun = false
		results = append(results, result)
	}
	return results, nil
}

type holdReleaseCommandOptions struct {
	BaseArgs       []string
	Repo           string
	RepoSet        bool
	All            bool
	Limit          int
	States         []string
	StateSet       bool
	HoldFor        time.Duration
	HoldForSet     bool
	Until          string
	UntilSet       bool
	Expired        bool
	Message        string
	MessageSet     bool
	MessageFile    string
	MessageFileSet bool
	PositionalArgs []string
}

func renderHoldReleaseApplyCommands(w io.Writer, results []pipelineHoldResult, action string, opts holdReleaseCommandOptions) error {
	if !holdReleaseResultsHaveApplyCommand(results, action) {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(holdReleaseApplyCommandArgs(opts)), " "))
	return err
}

func holdReleaseResultsHaveApplyCommand(results []pipelineHoldResult, action string) bool {
	action = strings.TrimSpace(action)
	if action == "" {
		return false
	}
	for _, result := range results {
		if !result.DryRun {
			continue
		}
		if strings.TrimSpace(result.Action) == action {
			return true
		}
	}
	return false
}

func holdReleaseApplyCommandArgs(opts holdReleaseCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.All {
		args = append(args, "--all")
	}
	if opts.StateSet {
		args = appendPlanCommandFilterArgs(args, "--state", opts.States)
	}
	if opts.Limit > 0 {
		args = append(args, "--limit", fmt.Sprint(opts.Limit))
	}
	if opts.HoldForSet {
		args = append(args, "--for", opts.HoldFor.String())
	}
	if opts.UntilSet && strings.TrimSpace(opts.Until) != "" {
		args = append(args, "--until", opts.Until)
	}
	if opts.Expired {
		args = append(args, "--expired")
	}
	if opts.MessageSet && strings.TrimSpace(opts.Message) != "" {
		args = append(args, "--message", opts.Message)
	}
	if opts.MessageFileSet && strings.TrimSpace(opts.MessageFile) != "" {
		args = append(args, "--message-file", opts.MessageFile)
	}
	for _, arg := range opts.PositionalArgs {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		args = append(args, arg)
	}
	return args
}

func newJobReopenCmd() *cobra.Command {
	var (
		repo          string
		status        string
		message       string
		force         bool
		dispatchNow   bool
		source        string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		dryRun        bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "reopen <job-id>",
		Aliases: []string{"retry"},
		Short:   "Reopen a durable job for another attempt.",
		Long: "Reopen a durable job by resetting its lifecycle status to queued or blocked. " +
			"Running jobs are refused unless --force is set. Pass --dispatch to immediately send the reopened job to its target, " +
			"and --wait to block until the retried job reaches a status or event.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reopen: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reopen: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reopen: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reopen: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reopen: wait-related flags require --wait.")
				return exitErr(2)
			}
			nextStatus, err := parseJobReopenStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if j.Status == job.StatusRunning && !force {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: refusing to reopen running job %q; pass --force to override.\n", j.ID)
				return exitErr(2)
			}
			j.Status = nextStatus
			j.LastEvent = "reopened"
			if strings.TrimSpace(message) != "" {
				j.LastStatus = strings.TrimSpace(message)
			} else {
				j.LastStatus = "reopened as " + string(nextStatus)
			}
			data := map[string]string{"force": fmt.Sprint(force)}
			if dispatchNow && len(j.Steps) > 0 {
				if stepID := resetFailedPipelineStepForRetry(j); stepID != "" {
					data["step"] = stepID
					if strings.TrimSpace(message) == "" {
						j.LastStatus = "reopened step " + stepID + " for retry"
					}
				}
			}
			j.UpdatedAt = time.Now().UTC()
			if dryRun {
				if dispatchNow {
					if len(j.Steps) > 0 {
						preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
						if err != nil {
							fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
							return exitErr(1)
						}
						return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, tmpl)
					}
					payload, requestedName, err := buildDispatchEventPayload(j.Target, j.Ticket, j.Kickoff, j.Instance, source, workspace)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
						return exitErr(2)
					}
					payload["job_id"] = j.ID
					payload["job"] = j.ID
					if err := applyDispatchRuntimeSelection(teamDir, payload, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
						return exitErr(2)
					}
					preview, err := previewDispatchPayload(teamDir, j.Target, requestedName, payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
						return exitErr(1)
					}
					return renderJobDispatchPreview(cmd.OutOrStdout(), j, preview, jsonOut, tmpl)
				}
				return renderJobReopenPreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", data); err != nil {
				return err
			}
			if dispatchNow {
				if len(j.Steps) > 0 {
					res, err := advanceJob(cmd, teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
					if err != nil {
						return err
					}
					if wait {
						waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job reopen")
						if err != nil {
							if err == context.Canceled {
								return nil
							}
							return err
						}
						refreshJobAdvanceResultAfterWait(res, waited)
					}
					if jsonOut {
						if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
							return err
						}
						if failOnFailed && res.Job.Status == job.StatusFailed {
							return exitErr(1)
						}
						return nil
					}
					if tmpl != nil {
						if err := renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl); err != nil {
							return err
						}
						if failOnFailed && res.Job.Status == job.StatusFailed {
							return exitErr(1)
						}
						return nil
					}
					if err := renderJobAdvanceResult(cmd.OutOrStdout(), res); err != nil {
						return err
					}
					if failOnFailed && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, source, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, "agent-team job reopen")
				if err != nil {
					return err
				}
				if wait {
					waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job reopen")
					if err != nil {
						if err == context.Canceled {
							return nil
						}
						return err
					}
					res.Job = waited
				}
				if jsonOut {
					if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
						return err
					}
					if failOnFailed && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				if tmpl != nil {
					if err := renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl); err != nil {
						return err
					}
					if failOnFailed && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				renderDispatchOutcome(cmd.OutOrStdout(), res.Job.Target, requestedName, res.Event)
				fmt.Fprintf(cmd.OutOrStdout(), "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
				if failOnFailed && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			if wait {
				waited, err := waitForJobCommand(cmd, teamDir, j.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job reopen")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
				j = waited
			}
			if err := renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && j.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&status, "status", string(job.StatusQueued), "Reopened status: queued or blocked.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Allow reopening a job currently marked running.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the reopened job immediately using the running daemon.")
	cmd.Flags().StringVar(&source, "source", "", "Source instance for --dispatch (default: AGENT_TEAM_INSTANCE or cli).")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the reopened job and optional dispatch without writing job or daemon state.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After reopening or dispatching, wait for the job to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or dry-run preview as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or dry-run preview with a Go template.")
	return cmd
}

func newJobCleanupCmd() *cobra.Command {
	var (
		repo        string
		all         bool
		merged      bool
		forceBranch bool
		verifyPR    bool
		dryRun      bool
		commands    bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cleanup <job-id>|--all",
		Short: "Remove a done job's owned worker worktree and branch after merge.",
		Long:  "Preview or remove job-owned worktrees and branches. Applying cleanup requires jobs marked done plus --merged after confirming the matching PR has merged.",
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				if len(args) != 0 {
					return fmt.Errorf("--all cannot be combined with explicit job ids")
				}
				return nil
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if !merged && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: pass --merged after confirming the job's PR has merged.")
				return exitErr(2)
			}
			tmpl, err := parseJobCleanupFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
				return exitErr(2)
			}
			if all {
				teamDir, err := resolveTeamDir(cmd, repo)
				if err != nil {
					return err
				}
				result, err := runJobCleanupAll(teamDir, filepath.Dir(teamDir), dryRun, merged, forceBranch, verifyPR)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
					return exitErr(1)
				}
				if commands {
					if err := renderJobCleanupBatchCommands(cmd.OutOrStdout(), result, jobCleanupCommandOptions{
						BaseArgs:    []string{"agent-team", "job", "cleanup"},
						Repo:        repo,
						RepoSet:     cmd.Flags().Changed("repo"),
						All:         true,
						ForceBranch: forceBranch,
						VerifyPR:    verifyPR,
					}); err != nil {
						return err
					}
				} else if jsonOut {
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
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			repoRoot := filepath.Dir(teamDir)
			if dryRun {
				preview, err := previewJobCleanup(repoRoot, j, forceBranch, verifyPR)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderJobCleanupPreviewCommands(cmd.OutOrStdout(), preview, j, jobCleanupCommandOptions{
						BaseArgs:    []string{"agent-team", "job", "cleanup", args[0]},
						Repo:        repo,
						RepoSet:     cmd.Flags().Changed("repo"),
						ForceBranch: forceBranch,
						VerifyPR:    verifyPR,
					})
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(preview)
				}
				if tmpl != nil {
					return renderJobCleanupFormat(cmd.OutOrStdout(), preview, tmpl)
				}
				renderJobCleanupPreview(cmd.OutOrStdout(), preview)
				return nil
			}
			if err := validateJobCleanupReady(j); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
				return exitErr(2)
			}
			summary, err := cleanupJobOwnedWorktree(repoRoot, j, forceBranch, verifyPR)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
				return exitErr(1)
			}
			j.Worktree = ""
			j.Branch = ""
			j.LastEvent = "cleanup"
			j.LastStatus = summary
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobCleanupFormat(cmd.OutOrStdout(), j, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Job: %s cleanup complete (%s)\n", j.ID, summary)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Clean all done jobs that still own a recorded worktree or branch.")
	cmd.Flags().BoolVar(&merged, "merged", false, "Confirm the job's PR has merged before removing a done job's worktree and branch.")
	cmd.Flags().BoolVar(&forceBranch, "force-branch", false, "With --merged, delete the job branch with git branch -D if it is not locally merged.")
	cmd.Flags().BoolVar(&verifyPR, "verify-pr", false, "Verify the recorded GitHub PR is merged with gh before cleanup.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the job-owned worktree and branch cleanup without removing anything.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching cleanup apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the cleanup result with a Go template, e.g. '{{.ID}} {{.LastStatus}}' or '{{.Total}} {{.Cleaned}}'.")
	return cmd
}

func newJobRmCmd() *cobra.Command {
	var (
		repo    string
		force   bool
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "rm <job-id> [<job-id>...]",
		Aliases: []string{"remove"},
		Short:   "Remove job files and their event logs.",
		Long: "Remove durable job TOML files and their sibling event logs. " +
			"Queued, running, and blocked jobs are refused unless --force is set.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job rm: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobRemoveFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job rm: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results := make([]jobRemoveResult, 0, len(args))
			for _, id := range args {
				j, err := job.Read(teamDir, id)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job rm: %v\n", err)
					return exitErr(1)
				}
				if !force && !jobStatusTerminal(j.Status) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job rm: refusing to remove active job %q with status %s; pass --force to remove it.\n", j.ID, j.Status)
					return exitErr(2)
				}
				result, err := removeJobFiles(teamDir, j, jobRemoveOptions{DryRun: dryRun, Force: force})
				if err != nil {
					return err
				}
				results = append(results, result)
			}
			return renderJobRemoveResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Allow removing queued, running, or blocked jobs.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview removals without deleting files.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit removal results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobPruneCmd() *cobra.Command {
	var (
		repo     string
		statuses []string
		dryRun   bool
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove terminal job files and their event logs.",
		Long:  "Remove jobs in terminal statuses. By default, this removes done and failed jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobRemoveFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job prune: %v\n", err)
				return exitErr(2)
			}
			statusSet, err := parseJobPruneStatuses(statuses, !cmd.Flags().Changed("status"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			jobs, err := job.List(teamDir)
			if err != nil {
				return err
			}
			results := make([]jobRemoveResult, 0, len(jobs))
			for _, j := range jobs {
				if !statusSet[j.Status] {
					continue
				}
				result, err := removeJobFiles(teamDir, j, jobRemoveOptions{DryRun: dryRun})
				if err != nil {
					return err
				}
				results = append(results, result)
			}
			return renderJobRemoveResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Terminal status to prune: done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview removals without deleting files.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit removal results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobNextCmd() *cobra.Command {
	var (
		repo     string
		states   []string
		step     string
		commands bool
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "next <job-id>",
		Short: "Show the next pipeline step for a job without dispatching it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job next: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job next: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job next: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			var (
				stateFilter map[string]bool
				err         error
			)
			if cmd.Flags().Changed("state") {
				stateFilter, err = parseJobNextStateFilter(states, false)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job next: %v\n", err)
					return exitErr(2)
				}
			}
			tmpl, err := parseJobNextFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job next: %v\n", err)
				return exitErr(2)
			}
			j, err := readJobFromRepo(cmd, repo, args[0])
			if err != nil {
				return err
			}
			next := inspectNextJobStep(j)
			if err := filterJobNextResult(next, stateFilter, step); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job next: %v\n", err)
				return exitErr(1)
			}
			return renderJobNextResult(cmd.OutOrStdout(), next, jsonOut, tmpl, commands)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&states, "state", nil, "Only render when the next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&step, "step", "", "Only render when this pipeline step is the next step.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended commands, one per line.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the next-step state as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the next-step state with a Go template, e.g. '{{.State}} {{.Step.ID}}'.")
	return cmd
}

func newJobExplainCmd() *cobra.Command {
	var (
		repo     string
		states   []string
		step     string
		watch    bool
		noClear  bool
		interval time.Duration
		jsonOut  bool
		commands bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "explain <job-id>",
		Aliases: []string{"watch"},
		Short:   "Explain pipeline step readiness for one job.",
		Long: "Explain one job's pipeline state from the durable job file, including every step, " +
			"dependency blockers, gates, ready/running/failed state, and suggested next actions.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.CalledAs() == "watch" {
				watch = true
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job explain: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job explain: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job explain: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job explain: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job explain: --interval must be >= 0.")
				return exitErr(2)
			}
			var (
				stateFilter map[string]bool
				err         error
			)
			if cmd.Flags().Changed("state") {
				stateFilter, err = parseJobNextStateFilter(states, false)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job explain: %v\n", err)
					return exitErr(2)
				}
			}
			tmpl, err := parseJobExplainFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job explain: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runJobExplainWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], stateFilter, step, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if err := runJobExplain(cmd.OutOrStdout(), teamDir, args[0], stateFilter, step, jsonOut, commands, tmpl); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job explain: %v\n", err)
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&states, "state", nil, "Only render when the job's next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&step, "step", "", "Only include details for this pipeline step id.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the job pipeline explanation until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the pipeline explanation as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands, one per line.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline explanation with a Go template, e.g. '{{.State}} {{len .Steps}}'.")
	return cmd
}

func newJobReadyCmd() *cobra.Command {
	var (
		repo     string
		pipeline string
		states   []string
		step     string
		sortBy   string
		limit    int
		watch    bool
		noClear  bool
		interval time.Duration
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ready",
		Short: "List pipeline jobs with ready or selected next-step states.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ready: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ready: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ready: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ready: --format cannot be combined with --json.")
				return exitErr(2)
			}
			stateFilter, err := parseJobNextStateFilter(states, !cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ready: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseJobReadySort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ready: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ready: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ready: --interval must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseJobReadyFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ready: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			opts := jobReadyOptions{
				Pipeline: strings.TrimSpace(pipeline),
				States:   stateFilter,
				Step:     step,
				Sort:     sortMode,
				Limit:    limit,
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runJobReadyWatch(ctx, cmd.OutOrStdout(), teamDir, opts, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			return runJobReady(cmd.OutOrStdout(), teamDir, opts, jsonOut, tmpl, commands)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Filter by pipeline name.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&step, "step", "", "Only include rows whose next step has this id.")
	cmd.Flags().StringVar(&sortBy, "sort", "job", "Sort rows by job, state, step, target, pipeline, updated, ticket, instance, or label.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the ready-step table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit ready rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended commands, one per line.")
	return cmd
}

func newJobTriageCmd() *cobra.Command {
	var (
		repo        string
		staleAfter  time.Duration
		minSeverity string
		reasons     []string
		watch       bool
		noClear     bool
		interval    time.Duration
		jsonOut     bool
		format      string
		commands    bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "triage",
		Short: "Show jobs that need operator attention.",
		Long: "Show a compact work queue triage view from durable jobs, persisted daemon queue items, " +
			"status-file update previews, and ready pipeline steps.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job triage: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job triage: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job triage: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job triage: --interval must be >= 0.")
				return exitErr(2)
			}
			if format != "" && (watch || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job triage: --format cannot be combined with --watch or --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobTriageFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job triage: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseJobTriageFilters(minSeverity, reasons)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job triage: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("stale-after") {
				configured, err := configuredJobTriageStaleAfter(teamDir)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job triage: %v\n", err)
					return exitErr(2)
				}
				staleAfter = configured
			}
			if staleAfter < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job triage: --stale-after must be >= 0.")
				return exitErr(2)
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runJobTriageWatch(ctx, cmd.OutOrStdout(), teamDir, staleAfter, filters, jsonOut, interval, !noClear)
			}
			snapshot, err := collectJobTriage(teamDir, time.Now().UTC(), staleAfter)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job triage: %v\n", err)
				return exitErr(1)
			}
			snapshot = filterJobTriageSnapshot(snapshot, filters)
			return renderJobTriage(cmd.OutOrStdout(), snapshot, jsonOut, tmpl, commands)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().DurationVar(&staleAfter, "stale-after", defaultJobTriageStaleAfter, "Flag queued or running jobs with no update after this duration (default: [health].job_stale_after or 24h; 0 disables stale checks).")
	cmd.Flags().StringVar(&minSeverity, "min-severity", "", "Only show attention rows at least this severe: critical, warning, or info.")
	cmd.Flags().StringSliceVar(&reasons, "reason", nil, "Only show attention rows with this reason. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the triage view until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit triage snapshot as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended commands, one per line.")
	return cmd
}

func newJobStepCmd() *cobra.Command {
	var (
		repo          string
		status        string
		message       string
		instance      string
		pr            string
		branch        string
		worktree      string
		advance       bool
		skip          bool
		force         bool
		workspace     string
		runtimeKind   string
		runtimeBin    string
		dryRun        bool
		commands      bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "step <job-id> <step-id>",
		Short: "Update a pipeline job step status.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseJobStepFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
				return exitErr(2)
			}
			stepStatus, err := job.ParseStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
				return exitErr(2)
			}
			if skip {
				if cmd.Flags().Changed("status") && stepStatus != job.StatusDone {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --skip can only be combined with --status done.")
					return exitErr(2)
				}
				stepStatus = job.StatusDone
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if wait && (!advance || stepStatus != job.StatusDone) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: --wait requires --advance with a done step.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job step: wait-related flags require --wait.")
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if err := validateJobStepRunningOwner(j, args[1], stepStatus, instance, force); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
				return exitErr(2)
			}
			if err := updateJobStep(j, args[1], stepStatus, jobStepUpdate{
				Message:  message,
				Instance: instance,
				PR:       pr,
				Branch:   branch,
				Worktree: worktree,
				Skip:     skip,
			}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
				return exitErr(2)
			}
			if dryRun {
				commandOptions := jobStepApplyCommandOptions{
					JobID:          j.ID,
					Step:           args[1],
					Repo:           repo,
					RepoSet:        cmd.Flags().Changed("repo"),
					Status:         stepStatus,
					StatusSet:      cmd.Flags().Changed("status"),
					Message:        message,
					MessageSet:     cmd.Flags().Changed("message"),
					Instance:       instance,
					InstanceSet:    cmd.Flags().Changed("instance"),
					PR:             pr,
					PRSet:          cmd.Flags().Changed("pr"),
					Branch:         branch,
					BranchSet:      cmd.Flags().Changed("branch"),
					Worktree:       worktree,
					WorktreeSet:    cmd.Flags().Changed("worktree"),
					Advance:        advance,
					Skip:           skip,
					Force:          force,
					Workspace:      workspace,
					WorkspaceSet:   cmd.Flags().Changed("workspace"),
					RuntimeKind:    runtimeKind,
					RuntimeKindSet: cmd.Flags().Changed("runtime"),
					RuntimeBin:     runtimeBin,
					RuntimeBinSet:  cmd.Flags().Changed("runtime-bin"),
				}
				if advance && stepStatus == job.StatusDone {
					preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
						return exitErr(1)
					}
					if commands {
						return renderJobStepApplyCommand(cmd.OutOrStdout(), true, commandOptions)
					}
					return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, tmpl)
				}
				if commands {
					return renderJobStepApplyCommand(cmd.OutOrStdout(), true, commandOptions)
				}
				return renderJobStepPreview(cmd.OutOrStdout(), j, args[1], jsonOut, tmpl)
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": args[1]}); err != nil {
				return err
			}
			if advance && stepStatus == job.StatusDone {
				res, err := advanceJob(cmd, teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
				if err != nil {
					return err
				}
				if wait && res.Job != nil {
					waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job step")
					if err != nil {
						if err == context.Canceled {
							return nil
						}
						return err
					}
					refreshJobAdvanceResultAfterWait(res, waited)
				}
				if jsonOut {
					if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
						return err
					}
					if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				if tmpl != nil {
					if err := renderJobAdvanceResultFormat(cmd.OutOrStdout(), res, tmpl); err != nil {
						return err
					}
					if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				if err := renderJobAdvanceResult(cmd.OutOrStdout(), res); err != nil {
					return err
				}
				if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobStepTemplate(cmd.OutOrStdout(), j, args[1], false, tmpl)
			}
			renderJobDetail(cmd.OutOrStdout(), j)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&status, "status", string(job.StatusDone), "Step status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance that owns or completed this step.")
	cmd.Flags().StringVar(&pr, "pr", "", "PR URL to record on the job.")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch name to record on the job.")
	cmd.Flags().StringVar(&worktree, "worktree", "", "Worktree path to record on the job.")
	cmd.Flags().BoolVar(&advance, "advance", false, "After marking the step done, dispatch the next ready step.")
	cmd.Flags().BoolVar(&skip, "skip", false, "Mark this step as intentionally skipped; stored as done so dependent steps can continue.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Allow marking a step running without an owning instance.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for an advanced step: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --advance dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the step update and optional advance dispatch without writing job or daemon state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job step apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&wait, "wait", false, "With --advance, wait for the job to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or advance result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.")
	return cmd
}

func newJobApproveCmd() *cobra.Command {
	var (
		repo          string
		stepID        string
		message       string
		messageFile   string
		advance       bool
		workspace     string
		runtimeKind   string
		runtimeBin    string
		dryRun        bool
		commands      bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "approve <job-id> [message...]",
		Short: "Approve a blocked manual pipeline gate.",
		Long: "Approve a blocked manual pipeline gate by marking it queued. " +
			"By default this selects the next blocked manual gate for the job; pass --step to approve a specific gate.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseJobStepFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job approve: %v\n", err)
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if wait && !advance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: --wait requires --advance.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job approve: wait-related flags require --wait.")
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job approve: %v\n", err)
					return exitErr(2)
				}
			}
			approvalMessage, err := optionalSendMessageBody(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job approve: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			selectedStep, err := selectManualGateForApproval(j, stepID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job approve: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(approvalMessage) == "" {
				approvalMessage = "approved manual gate " + selectedStep
			}
			if err := updateJobStep(j, selectedStep, job.StatusQueued, jobStepUpdate{Message: approvalMessage}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job approve: %v\n", err)
				return exitErr(2)
			}
			if dryRun {
				commandOptions := jobApproveApplyCommandOptions{
					JobID:             j.ID,
					Repo:              repo,
					RepoSet:           cmd.Flags().Changed("repo"),
					Advance:           advance,
					Workspace:         workspace,
					WorkspaceSet:      cmd.Flags().Changed("workspace"),
					RuntimeKind:       runtimeKind,
					RuntimeKindSet:    cmd.Flags().Changed("runtime"),
					RuntimeBin:        runtimeBin,
					RuntimeBinSet:     cmd.Flags().Changed("runtime-bin"),
					Step:              selectedStep,
					StepSet:           selectedStep != "",
					Message:           message,
					MessageSet:        cmd.Flags().Changed("message"),
					MessageFile:       messageFile,
					MessageFileSet:    cmd.Flags().Changed("message-file"),
					PositionalMessage: args[1:],
				}
				if advance {
					preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job approve: %v\n", err)
						return exitErr(1)
					}
					if commands {
						return renderJobApproveApplyCommand(cmd.OutOrStdout(), true, commandOptions)
					}
					return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, tmpl)
				}
				if commands {
					return renderJobApproveApplyCommand(cmd.OutOrStdout(), true, commandOptions)
				}
				return renderJobStepPreview(cmd.OutOrStdout(), j, selectedStep, jsonOut, tmpl)
			}
			if err := writeJobWithAudit(teamDir, j, "manual_gate_approved", "cli", approvalMessage, map[string]string{"step": selectedStep}); err != nil {
				return err
			}
			if advance {
				res, err := advanceJob(cmd, teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
				if err != nil {
					return err
				}
				if wait && res.Job != nil {
					waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job approve")
					if err != nil {
						if err == context.Canceled {
							return nil
						}
						return err
					}
					refreshJobAdvanceResultAfterWait(res, waited)
				}
				if jsonOut {
					if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
						return err
					}
					if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				if tmpl != nil {
					if err := renderJobAdvanceResultFormat(cmd.OutOrStdout(), res, tmpl); err != nil {
						return err
					}
					if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
						return exitErr(1)
					}
					return nil
				}
				if err := renderJobAdvanceResult(cmd.OutOrStdout(), res); err != nil {
					return err
				}
				if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobStepTemplate(cmd.OutOrStdout(), j, selectedStep, false, tmpl)
			}
			renderJobDetail(cmd.OutOrStdout(), j)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Manual gate step id to approve. Defaults to the next blocked manual gate.")
	cmd.Flags().StringVar(&message, "message", "", "Approval message recorded on the job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read approval message from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&advance, "advance", false, "After approval, dispatch the newly ready step.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for an advanced step: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --advance dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview approval and optional advance dispatch without writing job or daemon state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job approve apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&wait, "wait", false, "With --advance, wait for the job to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or advance result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.")
	return cmd
}

func newJobRejectCmd() *cobra.Command {
	var (
		repo        string
		stepID      string
		message     string
		messageFile string
		dryRun      bool
		commands    bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "reject <job-id> [reason...]",
		Short: "Reject a blocked manual pipeline gate.",
		Long: "Reject a blocked manual pipeline gate by marking the gate step failed. " +
			"By default this selects the next blocked manual gate for the job; pass --step to reject a specific gate.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reject: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reject: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reject: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reject: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseJobStepFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reject: %v\n", err)
				return exitErr(2)
			}
			reason, err := optionalSendMessageBody(message, messageFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reject: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			selectedStep, err := selectManualGateForApproval(j, stepID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reject: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(reason) == "" {
				reason = "rejected manual gate " + selectedStep
			}
			if err := updateJobStep(j, selectedStep, job.StatusFailed, jobStepUpdate{Message: reason}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reject: %v\n", err)
				return exitErr(2)
			}
			j.LastEvent = "manual_gate_rejected"
			if dryRun {
				if commands {
					return renderJobRejectApplyCommand(cmd.OutOrStdout(), true, jobRejectApplyCommandOptions{
						JobID:             j.ID,
						Repo:              repo,
						RepoSet:           cmd.Flags().Changed("repo"),
						Step:              selectedStep,
						StepSet:           selectedStep != "",
						Message:           message,
						MessageSet:        cmd.Flags().Changed("message"),
						MessageFile:       messageFile,
						MessageFileSet:    cmd.Flags().Changed("message-file"),
						PositionalMessage: args[1:],
					})
				}
				return renderJobStepPreview(cmd.OutOrStdout(), j, selectedStep, jsonOut, tmpl)
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": selectedStep}); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobStepTemplate(cmd.OutOrStdout(), j, selectedStep, false, tmpl)
			}
			renderJobDetail(cmd.OutOrStdout(), j)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Manual gate step id to reject. Defaults to the next blocked manual gate.")
	cmd.Flags().StringVar(&message, "message", "", "Rejection reason recorded on the job.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read rejection reason from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview rejection without writing job state.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job reject apply command when the preview has actionable work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobAdvanceCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		dryRun        bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "advance <job-id>",
		Short: "Dispatch the next ready step in a pipeline job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job advance: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job advance: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job advance: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job advance: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job advance: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parseJobAdvanceFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if dryRun {
				preview, err := previewJobAdvanceDispatch(teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
					return exitErr(1)
				}
				return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, tmpl)
			}
			res, err := advanceJob(cmd, teamDir, j, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin})
			if err != nil {
				return err
			}
			if wait && res.Job != nil {
				waited, err := waitForJobCommand(cmd, teamDir, res.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job advance")
				if err != nil {
					if err == context.Canceled {
						return nil
					}
					return err
				}
				refreshJobAdvanceResultAfterWait(res, waited)
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
					return err
				}
				if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			if tmpl != nil {
				if err := renderJobAdvanceResultFormat(cmd.OutOrStdout(), res, tmpl); err != nil {
					return err
				}
				if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			if err := renderJobAdvanceResult(cmd.OutOrStdout(), res); err != nil {
				return err
			}
			if failOnFailed && res.Job != nil && res.Job.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for the advanced step: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for the advanced step dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for the advanced step dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the next ready step dispatch without changing daemon or job state.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After advancing, wait for the job to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job and daemon event outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the advance preview or result with a Go template, e.g. '{{.Job.ID}} {{.Step.ID}}'.")
	return cmd
}

func newJobReconcileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Reconcile external runtime state back into jobs.",
	}
	cmd.AddCommand(newJobReconcileGitHubCmd())
	cmd.AddCommand(newJobReconcileEventsCmd())
	cmd.AddCommand(newJobReconcileQueueCmd())
	cmd.AddCommand(newJobReconcileStatusCmd())
	return cmd
}

func newJobReconcileEventsCmd() *cobra.Command {
	var (
		repo    string
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Reconcile terminal daemon instance metadata back into owning jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile events: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobEventReconcileFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile events: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := reconcileJobsFromEvents(teamDir, dryRun, time.Now().UTC())
			if err != nil {
				return err
			}
			return renderJobEventReconcileResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job updates without writing them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.")
	return cmd
}

func newJobReconcileStatusCmd() *cobra.Command {
	var (
		repo    string
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Reconcile instance status.toml files back into owning jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile status: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobStatusReconcileFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile status: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := reconcileJobsFromStatus(teamDir, dryRun, time.Now().UTC())
			if err != nil {
				return err
			}
			return renderJobStatusReconcileResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job updates without writing them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.")
	return cmd
}

func newJobReconcileQueueCmd() *cobra.Command {
	var (
		repo    string
		state   string
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Reconcile persisted queue state back into owning jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile queue: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobQueueReconcileFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile queue: %v\n", err)
				return exitErr(2)
			}
			stateFilter, err := parseJobQueueReconcileState(state)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile queue: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := reconcileJobsFromQueue(teamDir, stateFilter, dryRun, time.Now().UTC())
			if err != nil {
				return err
			}
			return renderJobQueueReconcileResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&state, "state", queuePruneStateAll, "Queue state to reconcile: pending, dead, or all.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job updates without writing them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.")
	return cmd
}

func newJobReconcileGitHubCmd() *cobra.Command {
	var (
		repo          string
		payload       string
		payloadFile   string
		dryRun        bool
		cleanupMerged bool
		verifyPR      bool
		advance       bool
		workspace     string
		runtimeKind   string
		runtimeBin    string
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Reconcile a GitHub PR webhook payload with its owning job.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile github: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if verifyPR && !cleanupMerged {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile github: --verify-pr requires --cleanup-merged.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile github: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile github: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile github: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if wait && !advance {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile github: --wait requires --advance.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || failOnFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile github: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			body, err := intakePayload(payload, payloadFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(2)
			}
			ev, err := intake.NormalizeGitHub(body)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(2)
			}
			input := job.ReconcileInputFromPayload(ev.Type, ev.Payload)
			var result *job.ReconcileResult
			if dryRun {
				result, err = job.PreviewReconcilePR(teamDir, input, time.Now().UTC())
			} else {
				result, err = job.ReconcilePR(teamDir, input, time.Now().UTC())
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(1)
			}
			var advanceResult *jobAdvanceResult
			var advancePreview *jobAdvancePreview
			cleanupSummary := ""
			var cleanupPreview *jobCleanupPreview
			if cleanupMerged && result.Job.Status == job.StatusDone {
				repoRoot := filepath.Dir(teamDir)
				if dryRun {
					preview, err := previewJobCleanup(repoRoot, result.Job, false, verifyPR)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
						return exitErr(1)
					}
					cleanupPreview = &preview
				} else {
					cleanupSummary, err = cleanupJobOwnedWorktree(repoRoot, result.Job, false, verifyPR)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
						return exitErr(1)
					}
					result.Job.Worktree = ""
					result.Job.Branch = ""
					result.Job.LastStatus = strings.TrimSpace(result.Job.LastStatus + "; cleanup: " + cleanupSummary)
					result.Job.UpdatedAt = time.Now().UTC()
					if err := writeJobWithAudit(teamDir, result.Job, "cleanup", "cli", cleanupSummary, nil); err != nil {
						return err
					}
				}
			}
			if advance {
				selection := runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}
				if dryRun {
					advancePreview, err = previewJobAdvanceDispatch(teamDir, result.Job, workspace, selection)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
						return exitErr(1)
					}
				} else {
					advanceResult, err = advanceJob(cmd, teamDir, result.Job, workspace, selection)
					if err != nil {
						return err
					}
					if wait && advanceResult != nil && advanceResult.Job != nil {
						waited, err := waitForJobCommand(cmd, teamDir, advanceResult.Job.ID, waitFilters.statuses, waitFilters.events, waitFilters.nextStates, waitFilters.nextStateSet, waitFilters.step, waitTimeout, waitInterval, "agent-team job reconcile github")
						if err != nil {
							if err == context.Canceled {
								return nil
							}
							return err
						}
						refreshJobAdvanceResultAfterWait(advanceResult, waited)
					}
					if advanceResult != nil && advanceResult.Job != nil {
						result.Job = advanceResult.Job
					}
				}
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Event          *intake.Event        `json:"event"`
					Result         *job.ReconcileResult `json:"result"`
					Cleanup        string               `json:"cleanup,omitempty"`
					CleanupPreview *jobCleanupPreview   `json:"cleanup_preview,omitempty"`
					Advance        *jobAdvanceResult    `json:"advance,omitempty"`
					AdvancePreview *jobAdvancePreview   `json:"advance_preview,omitempty"`
					DryRun         bool                 `json:"dry_run,omitempty"`
				}{Event: ev, Result: result, Cleanup: cleanupSummary, CleanupPreview: cleanupPreview, Advance: advanceResult, AdvancePreview: advancePreview, DryRun: dryRun}); err != nil {
					return err
				}
				if failOnFailed && result.Job != nil && result.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			if tmpl != nil {
				if err := renderJobTemplate(cmd.OutOrStdout(), result.Job, tmpl); err != nil {
					return err
				}
				if failOnFailed && result.Job != nil && result.Job.Status == job.StatusFailed {
					return exitErr(1)
				}
				return nil
			}
			action := "reconciled"
			if dryRun {
				action = "would reconcile"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Job: %s %s by %s status=%s\n", result.Job.ID, action, result.MatchedBy, result.Job.Status)
			if cleanupSummary != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Cleanup: %s\n", cleanupSummary)
			}
			if cleanupPreview != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Cleanup: %s\n", cleanupPreview.Summary)
			}
			if advancePreview != nil {
				if advancePreview.Message != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Advance: %s\n", advancePreview.Message)
				} else if advancePreview.Step != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Advance: would dispatch step %s\n", advancePreview.Step.ID)
				}
			}
			if advanceResult != nil {
				if advanceResult.Message != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Advance: %s\n", advanceResult.Message)
				} else if advanceResult.Step != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Advance: dispatched step %s status=%s\n", advanceResult.Step.ID, advanceResult.Step.Status)
				}
			}
			if failOnFailed && result.Job != nil && result.Job.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&payload, "payload", "", "GitHub webhook JSON object.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read GitHub webhook JSON from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the owning job update without writing it.")
	cmd.Flags().BoolVar(&cleanupMerged, "cleanup-merged", false, "After a merged PR event, remove the job-owned worktree and branch.")
	cmd.Flags().BoolVar(&verifyPR, "verify-pr", false, "With --cleanup-merged, verify the recorded GitHub PR is merged with gh before cleanup.")
	cmd.Flags().BoolVar(&advance, "advance", false, "After reconciling PR metadata, dispatch the next ready pipeline step.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --advance dispatch: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --advance dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --advance dispatch. Overrides env and repo config.")
	cmd.Flags().BoolVar(&wait, "wait", false, "With --advance, wait for the job to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. advance_dispatched, advance_queued, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the normalized event and reconciled job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the reconciled job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func readJobFromRepo(cmd *cobra.Command, repo, id string) (*job.Job, error) {
	_, j, err := readJobAndTeamDir(cmd, repo, id)
	return j, err
}

func readJobAndTeamDir(cmd *cobra.Command, repo, id string) (string, *job.Job, error) {
	teamDir, err := resolveTeamDir(cmd, repo)
	if err != nil {
		return "", nil, err
	}
	j, err := job.Read(teamDir, id)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job: %v\n", err)
		return "", nil, exitErr(1)
	}
	return teamDir, j, nil
}

func writeJobWithAudit(teamDir string, j *job.Job, eventType, actor, message string, data map[string]string) error {
	if err := job.Write(teamDir, j); err != nil {
		return err
	}
	return job.AppendSnapshotEvent(teamDir, j, eventType, actor, message, data)
}

func runJobEvents(w io.Writer, teamDir, id string, tail int, filters jobEventFilters, jsonOut bool, tmpl *template.Template) error {
	events, err := job.ListEvents(teamDir, id)
	if err != nil {
		return err
	}
	events = filterJobEvents(events, filters)
	events = job.TailEvents(events, tail)
	return renderJobEvents(w, events, jsonOut, tmpl)
}

func runJobEventsFollow(ctx context.Context, w io.Writer, teamDir, id string, tail int, interval time.Duration, filters jobEventFilters, jsonOut bool, tmpl *template.Template) error {
	if interval <= 0 {
		interval = time.Second
	}
	events, err := job.ListEvents(teamDir, id)
	if err != nil {
		return err
	}
	index := len(events)
	headerWritten := false
	initial := job.TailEvents(filterJobEvents(events, filters), tail)
	if len(initial) > 0 {
		if err := renderJobEventsFollowBatch(w, initial, jsonOut, tmpl, true); err != nil {
			return err
		}
		headerWritten = !jsonOut && tmpl == nil
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		events, err := job.ListEvents(teamDir, id)
		if err != nil {
			return err
		}
		if len(events) < index {
			index = 0
			headerWritten = false
		}
		if len(events) == index {
			continue
		}
		next := filterJobEvents(events[index:], filters)
		index = len(events)
		if len(next) == 0 {
			continue
		}
		if err := renderJobEventsFollowBatch(w, next, jsonOut, tmpl, !headerWritten); err != nil {
			return err
		}
		if !jsonOut && tmpl == nil {
			headerWritten = true
		}
	}
}

type jobEventFilters struct {
	types  map[string]bool
	actors map[string]bool
	since  *time.Time
}

func newJobEventFilters(types, actors []string, sinceRaw string, now func() time.Time) (jobEventFilters, error) {
	var filters jobEventFilters
	var err error
	if filters.types, err = stringSetFilter(types, "--type", "event type"); err != nil {
		return filters, err
	}
	if filters.actors, err = stringSetFilter(actors, "--actor", "actor"); err != nil {
		return filters, err
	}
	sinceRaw = strings.TrimSpace(sinceRaw)
	if sinceRaw == "" {
		return filters, nil
	}
	since, err := parseEventSince(sinceRaw, now)
	if err != nil {
		return filters, err
	}
	filters.since = &since
	return filters, nil
}

func filterJobEvents(events []job.Event, filters jobEventFilters) []job.Event {
	if filters.empty() {
		return events
	}
	out := make([]job.Event, 0, len(events))
	for _, ev := range events {
		if filters.match(ev) {
			out = append(out, ev)
		}
	}
	return out
}

func (f jobEventFilters) empty() bool {
	return len(f.types) == 0 && len(f.actors) == 0 && f.since == nil
}

func (f jobEventFilters) match(ev job.Event) bool {
	if f.since != nil && ev.TS.Before(*f.since) {
		return false
	}
	if len(f.types) > 0 && !f.types[ev.Type] {
		return false
	}
	if len(f.actors) > 0 && !f.actors[ev.Actor] {
		return false
	}
	return true
}

func renderJobEventsFollowBatch(w io.Writer, events []job.Event, jsonOut bool, tmpl *template.Template, header bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		for _, ev := range events {
			if err := enc.Encode(ev); err != nil {
				return err
			}
		}
		return nil
	}
	if tmpl != nil {
		for _, ev := range events {
			if err := renderJobEventTemplate(w, ev, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobEventTable(w, events, header)
	return nil
}

func renderJobEvents(w io.Writer, events []job.Event, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(events)
	}
	if tmpl != nil {
		for _, ev := range events {
			if err := renderJobEventTemplate(w, ev, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobEventTable(w, events, true)
	return nil
}

func renderJobEventTemplate(w io.Writer, ev job.Event, tmpl *template.Template) error {
	if err := tmpl.Execute(w, ev); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderJobEventTable(w io.Writer, events []job.Event, header bool) {
	if len(events) == 0 {
		if header {
			fmt.Fprintln(w, "(no job events)")
		}
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if header {
		fmt.Fprintln(tw, "TIME\tTYPE\tSTATUS\tINSTANCE\tACTOR\tMESSAGE")
	}
	for _, ev := range events {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			ev.TS.Format(time.RFC3339), ev.Type, emptyDash(string(ev.Status)), emptyDash(ev.Instance), emptyDash(ev.Actor), emptyDash(ev.Message))
	}
	_ = tw.Flush()
}

type jobListFilters struct {
	Status        job.Status
	Target        string
	Instance      string
	Pipeline      string
	PipelineOwned bool
	Ticket        string
	Branch        string
	PR            string
	Sort          string
	Limit         int
	Runtimes      map[string]bool
	Held          *bool
	HoldExpired   *bool
	Now           time.Time
}

type jobRemoveOptions struct {
	DryRun bool
	Force  bool
}

type jobRemoveResult struct {
	ID            string     `json:"id"`
	Ticket        string     `json:"ticket"`
	Status        job.Status `json:"status"`
	Action        string     `json:"action"`
	DryRun        bool       `json:"dry_run,omitempty"`
	Forced        bool       `json:"forced,omitempty"`
	Removed       bool       `json:"removed"`
	JobFile       bool       `json:"job_file"`
	EventLog      bool       `json:"event_log"`
	JobPath       string     `json:"job_path"`
	EventPath     string     `json:"event_path"`
	EventsRemoved bool       `json:"events_removed"`
}

type jobCleanupPreview struct {
	JobID               string                  `json:"job_id"`
	Worktree            string                  `json:"worktree,omitempty"`
	Branch              string                  `json:"branch,omitempty"`
	ForceBranch         bool                    `json:"force_branch,omitempty"`
	VerifyPR            bool                    `json:"verify_pr,omitempty"`
	PRVerification      *jobPRMergeVerification `json:"pr_verification,omitempty"`
	BranchDeleteMode    string                  `json:"branch_delete_mode,omitempty"`
	WorktreeExists      bool                    `json:"worktree_exists"`
	BranchExists        bool                    `json:"branch_exists"`
	WouldRemoveWorktree bool                    `json:"would_remove_worktree"`
	WouldRemoveBranch   bool                    `json:"would_remove_branch"`
	Summary             string                  `json:"summary"`
	DryRun              bool                    `json:"dry_run"`
}

type jobCleanupBatchResult struct {
	Team        string                `json:"team,omitempty"`
	Pipeline    string                `json:"pipeline,omitempty"`
	DryRun      bool                  `json:"dry_run"`
	Merged      bool                  `json:"merged,omitempty"`
	ForceBranch bool                  `json:"force_branch,omitempty"`
	VerifyPR    bool                  `json:"verify_pr,omitempty"`
	Total       int                   `json:"total"`
	Previewed   int                   `json:"previewed,omitempty"`
	Cleaned     int                   `json:"cleaned,omitempty"`
	Failed      int                   `json:"failed,omitempty"`
	Items       []jobCleanupBatchItem `json:"items"`
}

type jobCleanupBatchItem struct {
	JobID    string             `json:"job_id"`
	Status   job.Status         `json:"status"`
	Worktree string             `json:"worktree,omitempty"`
	Branch   string             `json:"branch,omitempty"`
	Summary  string             `json:"summary,omitempty"`
	Error    string             `json:"error,omitempty"`
	Preview  *jobCleanupPreview `json:"preview,omitempty"`
	Job      *job.Job           `json:"job,omitempty"`
}

type jobPRMergeVerification struct {
	URL         string `json:"url"`
	Verified    bool   `json:"verified"`
	State       string `json:"state,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
	Source      string `json:"source"`
}

type jobCleanupCommandOptions struct {
	BaseArgs    []string
	Repo        string
	RepoSet     bool
	All         bool
	ForceBranch bool
	VerifyPR    bool
}

type jobSummary struct {
	Total        int            `json:"total"`
	Queued       int            `json:"queued"`
	Running      int            `json:"running"`
	Blocked      int            `json:"blocked"`
	Done         int            `json:"done"`
	Failed       int            `json:"failed"`
	Held         int            `json:"held,omitempty"`
	ExpiredHeld  int            `json:"expired_held,omitempty"`
	Targets      map[string]int `json:"targets"`
	Pipelines    map[string]int `json:"pipelines"`
	Runtimes     map[string]int `json:"runtimes,omitempty"`
	WithInstance int            `json:"with_instance"`
	WithBranch   int            `json:"with_branch"`
	WithWorktree int            `json:"with_worktree"`
	WithPR       int            `json:"with_pr"`
}

type jobTriageSnapshot struct {
	CheckedAt        time.Time                  `json:"checked_at"`
	Summary          jobSummary                 `json:"summary"`
	Queue            queueSummary               `json:"queue"`
	OutboxQuarantine outboxQuarantineSummary    `json:"outbox_quarantine"`
	StatusPreviews   []jobStatusReconcileResult `json:"status_previews,omitempty"`
	Attention        []jobTriageItem            `json:"attention"`
	ReadySteps       []jobReadyRow              `json:"ready_steps,omitempty"`
}

type jobTriageItem struct {
	JobID                           string     `json:"job_id"`
	Ticket                          string     `json:"ticket"`
	Status                          job.Status `json:"status"`
	Severity                        string     `json:"severity"`
	Reasons                         []string   `json:"reasons"`
	Actions                         []string   `json:"actions,omitempty"`
	Message                         string     `json:"message,omitempty"`
	Target                          string     `json:"target,omitempty"`
	Instance                        string     `json:"instance,omitempty"`
	Pipeline                        string     `json:"pipeline,omitempty"`
	UpdatedAt                       time.Time  `json:"updated_at"`
	StepID                          string     `json:"step_id,omitempty"`
	StepState                       string     `json:"step_state,omitempty"`
	StepTarget                      string     `json:"step_target,omitempty"`
	QueuePending                    int        `json:"queue_pending,omitempty"`
	QueueDead                       int        `json:"queue_dead,omitempty"`
	QueueDelayed                    int        `json:"queue_delayed,omitempty"`
	QueueIDs                        []string   `json:"queue_ids,omitempty"`
	QueueQuarantined                int        `json:"queue_quarantined,omitempty"`
	QueueQuarantineRestorable       int        `json:"queue_quarantine_restorable,omitempty"`
	QueueQuarantineUnrestorable     int        `json:"queue_quarantine_unrestorable,omitempty"`
	QueueQuarantinePaths            []string   `json:"queue_quarantine_paths,omitempty"`
	QueueQuarantineRestorablePaths  []string   `json:"queue_quarantine_restorable_paths,omitempty"`
	OutboxQuarantined               int        `json:"outbox_quarantined,omitempty"`
	OutboxQuarantineRestorable      int        `json:"outbox_quarantine_restorable,omitempty"`
	OutboxQuarantineUnrestorable    int        `json:"outbox_quarantine_unrestorable,omitempty"`
	OutboxQuarantinePaths           []string   `json:"outbox_quarantine_paths,omitempty"`
	OutboxQuarantineRestorablePaths []string   `json:"outbox_quarantine_restorable_paths,omitempty"`
}

type jobTriageQueueStats struct {
	Pending                   int
	Dead                      int
	Delayed                   int
	IDs                       []string
	Quarantined               int
	QuarantineRestorable      int
	QuarantineUnrestorable    int
	QuarantinePaths           []string
	QuarantineRestorablePaths []string
}

type jobTriageOutboxQuarantineStats struct {
	Quarantined               int
	QuarantineRestorable      int
	QuarantineUnrestorable    int
	QuarantinePaths           []string
	QuarantineRestorablePaths []string
}

type jobTriageFilters struct {
	MinSeverity string
	Reasons     map[string]bool
}

const defaultJobTriageStaleAfter = 24 * time.Hour

func parseJobTriageFilters(minSeverity string, reasons []string) (jobTriageFilters, error) {
	var filters jobTriageFilters
	severity := strings.ToLower(strings.TrimSpace(minSeverity))
	if severity != "" {
		switch severity {
		case "critical", "warning", "info":
			filters.MinSeverity = severity
		default:
			return filters, fmt.Errorf("--min-severity must be critical, warning, or info")
		}
	}
	reasonSet, err := stringSetFilter(reasons, "--reason", "reason")
	if err != nil {
		return filters, err
	}
	filters.Reasons = reasonSet
	return filters, nil
}

func filterJobTriageSnapshot(snapshot jobTriageSnapshot, filters jobTriageFilters) jobTriageSnapshot {
	if filters.empty() {
		return snapshot
	}
	out := snapshot
	out.Attention = filterJobTriageAttention(snapshot.Attention, filters)
	return out
}

func filterJobTriageAttention(items []jobTriageItem, filters jobTriageFilters) []jobTriageItem {
	if filters.empty() {
		return items
	}
	out := make([]jobTriageItem, 0, len(items))
	for _, item := range items {
		if filters.match(item) {
			out = append(out, item)
		}
	}
	return out
}

func (f jobTriageFilters) empty() bool {
	return f.MinSeverity == "" && len(f.Reasons) == 0
}

func (f jobTriageFilters) match(item jobTriageItem) bool {
	if f.MinSeverity != "" && jobTriageSeverityRank(item.Severity) > jobTriageSeverityRank(f.MinSeverity) {
		return false
	}
	if len(f.Reasons) > 0 {
		found := false
		for _, reason := range item.Reasons {
			if f.Reasons[reason] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func newJobListFilters(status, target, instance, pipeline, ticket, branch, pr string, runtimeFilters []string) (jobListFilters, error) {
	f := jobListFilters{
		Target:   strings.TrimSpace(target),
		Instance: strings.TrimSpace(instance),
		Pipeline: strings.TrimSpace(pipeline),
		Ticket:   strings.TrimSpace(ticket),
		Branch:   strings.TrimSpace(branch),
		PR:       strings.TrimSpace(pr),
		Now:      time.Now().UTC(),
	}
	if strings.TrimSpace(status) != "" {
		parsed, err := job.ParseStatus(status)
		if err != nil {
			return f, err
		}
		f.Status = parsed
	}
	runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
	if err != nil {
		return f, err
	}
	f.Runtimes = runtimes
	return f, nil
}

func jobHeldFilter(held, unheld bool) *bool {
	switch {
	case held:
		value := true
		return &value
	case unheld:
		value := false
		return &value
	default:
		return nil
	}
}

func jobHoldExpiredFilter(expiredHold, activeHold bool) *bool {
	switch {
	case expiredHold:
		value := true
		return &value
	case activeHold:
		value := false
		return &value
	default:
		return nil
	}
}

func parseJobHoldUntil(holdFor time.Duration, holdForSet bool, untilRaw string, now time.Time) (time.Time, error) {
	untilRaw = strings.TrimSpace(untilRaw)
	if holdForSet && untilRaw != "" {
		return time.Time{}, fmt.Errorf("--for cannot be combined with --until")
	}
	if holdForSet {
		if holdFor < 0 {
			return time.Time{}, fmt.Errorf("--for must be >= 0")
		}
		if now.IsZero() {
			now = time.Now().UTC()
		}
		return now.UTC().Add(holdFor).UTC(), nil
	}
	if untilRaw == "" {
		return time.Time{}, nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, untilRaw); err == nil {
		return ts.UTC(), nil
	}
	if ts, err := time.Parse(time.RFC3339, untilRaw); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("--until must be an RFC3339 timestamp")
}

func jobHoldExpired(j *job.Job, now time.Time) bool {
	if j == nil || !j.Held || j.HoldUntil.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !j.HoldUntil.After(now.UTC())
}

func jobHoldExpirationMatches(j *job.Job, expired bool, now time.Time) bool {
	if expired {
		return jobHoldExpired(j, now)
	}
	return j != nil && j.Held && !jobHoldExpired(j, now)
}

func jobHoldUntilText(j *job.Job) string {
	if j == nil || j.HoldUntil.IsZero() {
		return ""
	}
	return j.HoldUntil.UTC().Format(time.RFC3339)
}

func parseJobSort(raw string) (string, error) {
	sortMode := strings.ToLower(strings.TrimSpace(raw))
	switch sortMode {
	case "", "id", "status", "target", "ticket", "created", "updated", "instance", "branch", "pr":
		if sortMode == "" {
			return "id", nil
		}
		return sortMode, nil
	default:
		return "", fmt.Errorf("--sort must be id, status, target, ticket, created, updated, instance, branch, or pr")
	}
}

func sortJobs(jobs []*job.Job, sortMode string) {
	if sortMode == "" {
		sortMode = "id"
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		left, right := jobs[i], jobs[j]
		switch sortMode {
		case "status":
			if rankLeft, rankRight := jobStatusSortRank(left.Status), jobStatusSortRank(right.Status); rankLeft != rankRight {
				return rankLeft < rankRight
			}
		case "target":
			if left.Target != right.Target {
				return left.Target < right.Target
			}
		case "ticket":
			if left.Ticket != right.Ticket {
				return left.Ticket < right.Ticket
			}
		case "created":
			if !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.After(right.CreatedAt)
			}
		case "updated":
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.After(right.UpdatedAt)
			}
		case "instance":
			if left.Instance != right.Instance {
				return left.Instance < right.Instance
			}
		case "branch":
			if left.Branch != right.Branch {
				return left.Branch < right.Branch
			}
		case "pr":
			if left.PR != right.PR {
				return left.PR < right.PR
			}
		}
		return left.ID < right.ID
	})
}

func jobStatusSortRank(status job.Status) int {
	switch status {
	case job.StatusQueued:
		return 0
	case job.StatusRunning:
		return 1
	case job.StatusBlocked:
		return 2
	case job.StatusDone:
		return 3
	case job.StatusFailed:
		return 4
	default:
		return 5
	}
}

func runJobSummary(w io.Writer, teamDir string, filters jobListFilters, jsonOut bool) error {
	filtered, err := filteredJobs(teamDir, filters)
	if err != nil {
		return err
	}
	summary := summarizeJobsWithRuntime(teamDir, filtered)
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	renderJobSummary(w, summary)
	return nil
}

func runJobSummaryWatch(ctx context.Context, w io.Writer, teamDir string, filters jobListFilters, jsonOut bool, interval time.Duration, clear bool) error {
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
		if err := runJobSummary(w, teamDir, filters, jsonOut); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func summarizeJobs(jobs []*job.Job) jobSummary {
	summary := jobSummary{
		Targets:   map[string]int{},
		Pipelines: map[string]int{},
	}
	now := time.Now().UTC()
	for _, j := range jobs {
		summary.Total++
		switch j.Status {
		case job.StatusQueued:
			summary.Queued++
		case job.StatusRunning:
			summary.Running++
		case job.StatusBlocked:
			summary.Blocked++
		case job.StatusDone:
			summary.Done++
		case job.StatusFailed:
			summary.Failed++
		}
		if j.Held {
			summary.Held++
			if jobHoldExpired(j, now) {
				summary.ExpiredHeld++
			}
		}
		if target := strings.TrimSpace(j.Target); target != "" {
			summary.Targets[target]++
		}
		if pipeline := strings.TrimSpace(j.Pipeline); pipeline != "" {
			summary.Pipelines[pipeline]++
		}
		if strings.TrimSpace(j.Instance) != "" {
			summary.WithInstance++
		}
		if strings.TrimSpace(j.Branch) != "" {
			summary.WithBranch++
		}
		if strings.TrimSpace(j.Worktree) != "" {
			summary.WithWorktree++
		}
		if strings.TrimSpace(j.PR) != "" {
			summary.WithPR++
		}
	}
	return summary
}

func summarizeJobsWithRuntime(teamDir string, jobs []*job.Job) jobSummary {
	summary := summarizeJobs(jobs)
	summary.Runtimes = summarizeJobRuntimeCounts(teamDir, jobs)
	return summary
}

func summarizeJobRuntimeCounts(teamDir string, jobs []*job.Job) map[string]int {
	if strings.TrimSpace(teamDir) == "" || len(jobs) == 0 {
		return nil
	}
	runtimeByInstance := jobRuntimeMap(teamDir)
	if len(runtimeByInstance) == 0 {
		return nil
	}
	counts := map[string]int{}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		seen := map[string]bool{}
		add := func(instance string) {
			instance = strings.TrimSpace(instance)
			if instance == "" {
				return
			}
			runtime, ok := runtimeByInstance[instance]
			if !ok || runtime == "" {
				return
			}
			seen[runtime] = true
		}
		add(j.Instance)
		for _, step := range j.Steps {
			add(step.Instance)
		}
		for runtime := range seen {
			counts[runtime]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func jobRuntimeMap(teamDir string) map[string]string {
	if strings.TrimSpace(teamDir) == "" {
		return nil
	}
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(metas))
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		out[meta.Instance] = metadataRuntimeKey(meta)
	}
	return out
}

func collectJobTriage(teamDir string, now time.Time, staleAfter time.Duration) (jobTriageSnapshot, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	jobs, err := job.List(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	quarantineItems, err := listQueueQuarantine(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	outboxQuarantineItems, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	queueByJob := queueStatsByJob(jobs, queueItems, now)
	addQueueQuarantineStatsByJob(queueByJob, jobs, quarantineItems)
	outboxQuarantineByJob := outboxQuarantineStatsByJob(jobs, outboxQuarantineItems)
	attention := make([]jobTriageItem, 0, len(jobs))
	for _, j := range jobs {
		if item, ok := triageJob(j, inspectNextJobStep(j), queueByJob[j.ID], outboxQuarantineByJob[j.ID], now, staleAfter); ok {
			attention = append(attention, item)
		}
	}
	statusPreviews, err := reconcileJobsFromStatus(teamDir, true, now)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	attention = addStatusPreviewsToJobTriage(attention, jobs, statusPreviews, now)
	sortJobTriageItems(attention)
	readySteps, err := collectJobReadyRows(teamDir, "", map[string]bool{"ready": true, "queued": true})
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	readySteps = filterJobReadyRowsByAdvanceable(readySteps)
	queueSummary := summarizeQueueItems(queueItems, now)
	applyQueueQuarantineSummary(&queueSummary, quarantineItems)
	outboxQuarantineSummary := summarizeOutboxQuarantineItems(outboxQuarantineItems)
	return jobTriageSnapshot{
		CheckedAt:        now,
		Summary:          summarizeJobs(jobs),
		Queue:            queueSummary,
		OutboxQuarantine: outboxQuarantineSummary,
		StatusPreviews:   statusPreviews,
		Attention:        attention,
		ReadySteps:       readySteps,
	}, nil
}

func runJobTriageWatch(ctx context.Context, w io.Writer, teamDir string, staleAfter time.Duration, filters jobTriageFilters, jsonOut bool, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectJobTriage(teamDir, time.Now().UTC(), staleAfter)
		if err != nil {
			return err
		}
		snapshot = filterJobTriageSnapshot(snapshot, filters)
		if jsonOut {
			if err := json.NewEncoder(w).Encode(snapshot); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := renderJobTriage(w, snapshot, false, nil, false); err != nil {
				return err
			}
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func queueStatsByJob(jobs []*job.Job, items []*daemon.QueueItem, now time.Time) map[string]jobTriageQueueStats {
	out := make(map[string]jobTriageQueueStats, len(jobs))
	for _, j := range jobs {
		var stats jobTriageQueueStats
		for _, item := range items {
			if !queueItemMatchesJob(item, j) {
				continue
			}
			stats.IDs = append(stats.IDs, item.ID)
			switch item.State {
			case daemon.QueueStatePending:
				stats.Pending++
				if !item.NextRetry.IsZero() && item.NextRetry.After(now) {
					stats.Delayed++
				}
			case daemon.QueueStateDead:
				stats.Dead++
			}
		}
		if stats.Pending > 0 || stats.Dead > 0 {
			sort.Strings(stats.IDs)
			out[j.ID] = stats
		}
	}
	return out
}

func addQueueQuarantineStatsByJob(out map[string]jobTriageQueueStats, jobs []*job.Job, items []queueQuarantineItem) {
	if out == nil {
		return
	}
	for _, j := range jobs {
		var stats jobTriageQueueStats
		if existing, ok := out[j.ID]; ok {
			stats = existing
		}
		for _, item := range items {
			if !queueQuarantineItemMatchesJob(item, j) {
				continue
			}
			addQueueQuarantineItemToStats(&stats, item)
		}
		if stats.Pending > 0 || stats.Dead > 0 || stats.Quarantined > 0 {
			sort.Strings(stats.QuarantinePaths)
			sort.Strings(stats.QuarantineRestorablePaths)
			out[j.ID] = stats
		}
	}
}

func addQueueQuarantineItemToStats(stats *jobTriageQueueStats, item queueQuarantineItem) {
	if stats == nil {
		return
	}
	stats.Quarantined++
	stats.QuarantinePaths = append(stats.QuarantinePaths, item.Path)
	if item.Restorable {
		stats.QuarantineRestorable++
		stats.QuarantineRestorablePaths = append(stats.QuarantineRestorablePaths, item.Path)
	} else {
		stats.QuarantineUnrestorable++
	}
}

func outboxQuarantineStatsByJob(jobs []*job.Job, items []outboxQuarantineItem) map[string]jobTriageOutboxQuarantineStats {
	out := make(map[string]jobTriageOutboxQuarantineStats, len(jobs))
	for _, j := range jobs {
		var stats jobTriageOutboxQuarantineStats
		for _, item := range items {
			if !outboxQuarantineItemMatchesJob(item, j) {
				continue
			}
			addOutboxQuarantineItemToTriageStats(&stats, item)
		}
		if stats.Quarantined > 0 {
			sort.Strings(stats.QuarantinePaths)
			sort.Strings(stats.QuarantineRestorablePaths)
			out[j.ID] = stats
		}
	}
	return out
}

func addOutboxQuarantineItemToTriageStats(stats *jobTriageOutboxQuarantineStats, item outboxQuarantineItem) {
	if stats == nil {
		return
	}
	stats.Quarantined++
	stats.QuarantinePaths = append(stats.QuarantinePaths, item.Path)
	if item.Restorable {
		stats.QuarantineRestorable++
		stats.QuarantineRestorablePaths = append(stats.QuarantineRestorablePaths, item.Path)
	} else {
		stats.QuarantineUnrestorable++
	}
}

func triageJob(j *job.Job, next jobNextResult, queueStats jobTriageQueueStats, outboxQuarantineStats jobTriageOutboxQuarantineStats, now time.Time, staleAfter time.Duration) (jobTriageItem, bool) {
	item := jobTriageItem{
		JobID:                           j.ID,
		Ticket:                          j.Ticket,
		Status:                          j.Status,
		Severity:                        "info",
		Target:                          j.Target,
		Instance:                        j.Instance,
		Pipeline:                        j.Pipeline,
		UpdatedAt:                       j.UpdatedAt,
		QueuePending:                    queueStats.Pending,
		QueueDead:                       queueStats.Dead,
		QueueDelayed:                    queueStats.Delayed,
		QueueIDs:                        append([]string(nil), queueStats.IDs...),
		QueueQuarantined:                queueStats.Quarantined,
		QueueQuarantineRestorable:       queueStats.QuarantineRestorable,
		QueueQuarantineUnrestorable:     queueStats.QuarantineUnrestorable,
		QueueQuarantinePaths:            append([]string(nil), queueStats.QuarantinePaths...),
		QueueQuarantineRestorablePaths:  append([]string(nil), queueStats.QuarantineRestorablePaths...),
		OutboxQuarantined:               outboxQuarantineStats.Quarantined,
		OutboxQuarantineRestorable:      outboxQuarantineStats.QuarantineRestorable,
		OutboxQuarantineUnrestorable:    outboxQuarantineStats.QuarantineUnrestorable,
		OutboxQuarantinePaths:           append([]string(nil), outboxQuarantineStats.QuarantinePaths...),
		OutboxQuarantineRestorablePaths: append([]string(nil), outboxQuarantineStats.QuarantineRestorablePaths...),
	}
	if next.Step != nil {
		item.StepID = next.Step.ID
		item.StepTarget = next.Step.Target
	}
	item.Instance = activeJobInstanceForTriage(j, next)
	item.StepState = next.State
	addTriageReason := func(reason, severity string) {
		for _, existing := range item.Reasons {
			if existing == reason {
				item.Severity = maxJobTriageSeverity(item.Severity, severity)
				return
			}
		}
		item.Reasons = append(item.Reasons, reason)
		item.Severity = maxJobTriageSeverity(item.Severity, severity)
	}
	switch j.Status {
	case job.StatusFailed:
		addTriageReason("failed", "critical")
	case job.StatusBlocked:
		addTriageReason("blocked", "warning")
	case job.StatusDone:
		if jobNeedsCleanup(j) {
			addTriageReason("cleanup_ready", "info")
		}
	case job.StatusRunning:
		if strings.TrimSpace(item.Instance) == "" {
			addTriageReason("running_without_instance", "warning")
		}
		if staleAfter > 0 && !j.UpdatedAt.IsZero() && j.UpdatedAt.Before(now.Add(-staleAfter)) {
			addTriageReason("stale_running", "warning")
		}
	case job.StatusQueued:
		if staleAfter > 0 && !j.UpdatedAt.IsZero() && j.UpdatedAt.Before(now.Add(-staleAfter)) && queueStats.Pending == 0 && queueStats.Dead == 0 && queueStats.Quarantined == 0 && outboxQuarantineStats.Quarantined == 0 {
			addTriageReason("stale_queued", "warning")
		}
	}
	if queueStats.Dead > 0 {
		addTriageReason("queue_dead", "critical")
	}
	if queueStats.Quarantined > 0 {
		addTriageReason("queue_quarantined", "warning")
	}
	if outboxQuarantineStats.Quarantined > 0 {
		addTriageReason("outbox_quarantined", "warning")
	}
	switch next.State {
	case "failed":
		addTriageReason("failed_step", "critical")
	case "blocked":
		addTriageReason("blocked_step", "warning")
	case "held":
		addTriageReason("held", "info")
		if jobHoldExpired(j, now) {
			addTriageReason("expired_hold", "warning")
		}
	}
	if len(item.Reasons) == 0 {
		return jobTriageItem{}, false
	}
	if stringSliceContains(item.Reasons, "expired_hold") {
		item.Message = heldJobMessage(j)
	} else if strings.TrimSpace(j.LastStatus) != "" {
		item.Message = j.LastStatus
	} else if strings.TrimSpace(next.Message) != "" {
		item.Message = next.Message
	} else {
		item.Message = strings.Join(item.Reasons, ",")
	}
	item.Actions = actionsForJobTriageItem(item)
	return item, true
}

func activeJobInstanceForTriage(j *job.Job, next jobNextResult) string {
	if j == nil {
		return ""
	}
	if instance := strings.TrimSpace(j.Instance); instance != "" {
		return instance
	}
	if next.Step != nil && next.Step.Status == job.StatusRunning {
		return strings.TrimSpace(next.Step.Instance)
	}
	if step := firstJobStepWithStatus(j, job.StatusRunning); step != nil {
		return strings.TrimSpace(step.Instance)
	}
	return ""
}

func maxJobTriageSeverity(left, right string) string {
	if jobTriageSeverityRank(right) < jobTriageSeverityRank(left) {
		return right
	}
	return left
}

func jobTriageSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "warning":
		return 1
	case "info":
		return 2
	default:
		return 3
	}
}

func sortJobTriageItems(items []jobTriageItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if li, ri := jobTriageSeverityRank(left.Severity), jobTriageSeverityRank(right.Severity); li != ri {
			return li < ri
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.Before(right.UpdatedAt)
		}
		return left.JobID < right.JobID
	})
}

func parseJobTriageFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-triage-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobTriage(w io.Writer, snapshot jobTriageSnapshot, jsonOut bool, tmpl *template.Template, commands bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	if commands {
		return renderJobTriageCommands(w, snapshot)
	}
	if tmpl != nil {
		return renderJobTriageFormat(w, snapshot, tmpl)
	}
	renderJobSummary(w, snapshot.Summary)
	renderQueueSummary(w, snapshot.Queue)
	if snapshot.OutboxQuarantine.Quarantined > 0 {
		fmt.Fprintln(w, outboxQuarantineSummaryLine(snapshot.OutboxQuarantine))
	}
	if len(snapshot.StatusPreviews) > 0 {
		fmt.Fprintf(w, "job status: previews=%d changes=%d blocked=%d\n",
			len(snapshot.StatusPreviews),
			countChangedJobStatusPreviews(snapshot.StatusPreviews),
			countJobStatusPreviewsByAfter(snapshot.StatusPreviews, job.StatusBlocked),
		)
	}
	fmt.Fprintln(w)
	renderJobTriageAttention(w, snapshot.Attention)
	if len(snapshot.ReadySteps) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Ready pipeline steps:")
		renderJobReadyTable(w, snapshot.ReadySteps)
	}
	return nil
}

func renderJobTriageCommands(w io.Writer, snapshot jobTriageSnapshot) error {
	for _, item := range snapshot.Attention {
		for _, action := range item.Actions {
			if strings.TrimSpace(action) == "" {
				continue
			}
			if _, err := fmt.Fprintln(w, action); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderJobTriageFormat(w io.Writer, snapshot jobTriageSnapshot, tmpl *template.Template) error {
	if err := tmpl.Execute(w, snapshot); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderJobTriageAttention(w io.Writer, items []jobTriageItem) {
	if len(items) == 0 {
		fmt.Fprintln(w, "(no jobs need attention)")
		return
	}
	fmt.Fprintln(w, "Attention:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tSEVERITY\tSTATUS\tREASONS\tTARGET\tINSTANCE\tQUEUE\tUPDATED\tACTION\tMESSAGE")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.JobID,
			item.Severity,
			item.Status,
			strings.Join(item.Reasons, ","),
			emptyDash(item.Target),
			emptyDash(item.Instance),
			jobTriageQueueSummary(item),
			item.UpdatedAt.Format(time.RFC3339),
			emptyDash(strings.Join(item.Actions, "; ")),
			emptyDash(item.Message),
		)
	}
	_ = tw.Flush()
}

func actionsForJobTriageItem(item jobTriageItem) []string {
	var actions []string
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" {
			return
		}
		for _, existing := range actions {
			if existing == action {
				return
			}
		}
		actions = append(actions, action)
	}
	if stringSliceContains(item.Reasons, "queue_dead") {
		if len(item.QueueIDs) == 1 {
			add(fmt.Sprintf("agent-team job queue retry %s %s", item.JobID, item.QueueIDs[0]))
		} else {
			add(jobQueueRetryAllRecoveryAction(item.JobID, false))
		}
	}
	if stringSliceContains(item.Reasons, "queue_quarantined") {
		add(fmt.Sprintf("agent-team job queue quarantine %s", item.JobID))
		if item.QueueQuarantineRestorable == 1 && len(item.QueueQuarantineRestorablePaths) == 1 {
			add(fmt.Sprintf("agent-team job queue quarantine restore %s %s --dry-run", item.JobID, item.QueueQuarantineRestorablePaths[0]))
		} else if item.QueueQuarantineRestorable > 1 {
			add(fmt.Sprintf("agent-team job queue quarantine restore %s --all --limit %d --dry-run", item.JobID, queueRecoveryHintLimit))
		}
		if item.QueueQuarantineUnrestorable > 0 {
			add(fmt.Sprintf("agent-team job queue quarantine drop %s --all --unrestorable --limit %d --dry-run", item.JobID, queueRecoveryHintLimit))
		}
	}
	if stringSliceContains(item.Reasons, "outbox_quarantined") {
		add(fmt.Sprintf("agent-team job outbox quarantine %s", item.JobID))
		if item.OutboxQuarantineRestorable == 1 && len(item.OutboxQuarantineRestorablePaths) == 1 {
			add(fmt.Sprintf("agent-team job outbox quarantine restore %s %s --dry-run", item.JobID, item.OutboxQuarantineRestorablePaths[0]))
		} else if item.OutboxQuarantineRestorable > 1 {
			add(fmt.Sprintf("agent-team job outbox quarantine restore %s --all --limit %d --dry-run", item.JobID, queueRecoveryHintLimit))
		}
		if item.OutboxQuarantineUnrestorable > 0 {
			add(fmt.Sprintf("agent-team job outbox quarantine drop %s --all --unrestorable --limit %d --dry-run", item.JobID, queueRecoveryHintLimit))
		}
	}
	if stringSliceContains(item.Reasons, "failed") || stringSliceContains(item.Reasons, "failed_step") {
		add(fmt.Sprintf("agent-team job retry %s --dispatch", item.JobID))
	}
	if stringSliceContains(item.Reasons, "blocked") || stringSliceContains(item.Reasons, "blocked_step") || stringSliceContains(item.Reasons, "status_file_blocked") {
		add(jobUnblockAction(item.JobID, item.StepID))
	}
	if stringSliceContains(item.Reasons, "stale_queued") {
		add(fmt.Sprintf("agent-team job dispatch %s", item.JobID))
	}
	if stringSliceContains(item.Reasons, "stale_running") || stringSliceContains(item.Reasons, "running_without_instance") {
		add("agent-team job reconcile status")
	}
	if stringSliceContains(item.Reasons, "stale_running") {
		add(fmt.Sprintf("agent-team job timeout %s --dry-run", item.JobID))
	}
	if stringSliceContains(item.Reasons, "running_without_instance") {
		if stepID := strings.TrimSpace(item.StepID); stepID != "" {
			add(fmt.Sprintf("agent-team job adopt %s --step %s --pid <pid> --dry-run", item.JobID, stepID))
		} else {
			add(fmt.Sprintf("agent-team job adopt %s --pid <pid> --dry-run", item.JobID))
		}
	}
	if stringSliceContains(item.Reasons, "cleanup_ready") {
		add(fmt.Sprintf("agent-team job cleanup %s --dry-run", item.JobID))
	}
	if stringSliceContains(item.Reasons, "held") || stringSliceContains(item.Reasons, "expired_hold") {
		add(fmt.Sprintf("agent-team job release %s", item.JobID))
	}
	if strings.TrimSpace(item.Pipeline) != "" {
		add(fmt.Sprintf("agent-team job explain %s", item.JobID))
	}
	return actions
}

func jobNeedsCleanup(j *job.Job) bool {
	if j == nil {
		return false
	}
	return strings.TrimSpace(j.Worktree) != "" || strings.TrimSpace(j.Branch) != ""
}

func stringSliceContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func jobTriageQueueSummary(item jobTriageItem) string {
	parts := []string{}
	if item.QueueDead > 0 {
		parts = append(parts, fmt.Sprintf("dead=%d", item.QueueDead))
	}
	if item.QueuePending > 0 {
		parts = append(parts, fmt.Sprintf("pending=%d", item.QueuePending))
	}
	if item.QueueDelayed > 0 {
		parts = append(parts, fmt.Sprintf("delayed=%d", item.QueueDelayed))
	}
	if item.QueueQuarantined > 0 {
		parts = append(parts, fmt.Sprintf("quarantined=%d", item.QueueQuarantined))
		parts = append(parts, fmt.Sprintf("restorable=%d", item.QueueQuarantineRestorable))
		parts = append(parts, fmt.Sprintf("unrestorable=%d", item.QueueQuarantineUnrestorable))
	}
	if item.OutboxQuarantined > 0 {
		parts = append(parts, fmt.Sprintf("outbox_quarantined=%d", item.OutboxQuarantined))
		parts = append(parts, fmt.Sprintf("outbox_restorable=%d", item.OutboxQuarantineRestorable))
		parts = append(parts, fmt.Sprintf("outbox_unrestorable=%d", item.OutboxQuarantineUnrestorable))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func addStatusPreviewsToJobTriage(items []jobTriageItem, jobs []*job.Job, previews []jobStatusReconcileResult, now time.Time) []jobTriageItem {
	if len(previews) == 0 {
		return items
	}
	jobsByID := make(map[string]*job.Job, len(jobs))
	for _, j := range jobs {
		jobsByID[j.ID] = j
	}
	itemIndexes := make(map[string]int, len(items))
	for idx, item := range items {
		itemIndexes[item.JobID] = idx
	}
	for _, preview := range previews {
		if !preview.Changed || preview.After != job.StatusBlocked {
			continue
		}
		if idx, ok := itemIndexes[preview.JobID]; ok {
			items[idx].Status = preview.After
			items[idx].Reasons = appendStringOnce(items[idx].Reasons, "status_file_blocked")
			items[idx].Severity = maxJobTriageSeverity(items[idx].Severity, "warning")
			if strings.TrimSpace(preview.Message) != "" {
				items[idx].Message = preview.Message
			}
			if strings.TrimSpace(items[idx].Instance) == "" {
				items[idx].Instance = preview.Instance
			}
			items[idx].Actions = actionsForJobTriageItem(items[idx])
			continue
		}
		j := jobsByID[preview.JobID]
		item := jobTriageItem{
			JobID:     preview.JobID,
			Status:    preview.After,
			Severity:  "warning",
			Reasons:   []string{"status_file_blocked"},
			Actions:   []string{jobUnblockAction(preview.JobID, "")},
			Message:   preview.Message,
			Instance:  preview.Instance,
			UpdatedAt: now,
		}
		if j != nil {
			item.Ticket = j.Ticket
			item.Target = j.Target
			item.Pipeline = j.Pipeline
			item.UpdatedAt = j.UpdatedAt
			if strings.TrimSpace(item.Instance) == "" {
				item.Instance = j.Instance
			}
		}
		items = append(items, item)
		itemIndexes[item.JobID] = len(items) - 1
	}
	return items
}

func appendStringOnce(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func renderJobSummary(w io.Writer, summary jobSummary) {
	fmt.Fprintf(w, "jobs: total=%d queued=%d running=%d blocked=%d done=%d failed=%d held=%d expired_held=%d\n",
		summary.Total, summary.Queued, summary.Running, summary.Blocked, summary.Done, summary.Failed, summary.Held, summary.ExpiredHeld)
	if len(summary.Targets) > 0 {
		fmt.Fprint(w, "targets:")
		for _, key := range sortedCountKeys(summary.Targets) {
			fmt.Fprintf(w, " %s=%d", key, summary.Targets[key])
		}
		fmt.Fprintln(w)
	}
	if len(summary.Pipelines) > 0 {
		fmt.Fprint(w, "pipelines:")
		for _, key := range sortedCountKeys(summary.Pipelines) {
			fmt.Fprintf(w, " %s=%d", key, summary.Pipelines[key])
		}
		fmt.Fprintln(w)
	}
	if len(summary.Runtimes) > 0 {
		fmt.Fprint(w, "runtimes:")
		for _, key := range sortedCountKeys(summary.Runtimes) {
			fmt.Fprintf(w, " %s=%d", key, summary.Runtimes[key])
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "ownership: instance=%d branch=%d worktree=%d pr=%d\n",
		summary.WithInstance, summary.WithBranch, summary.WithWorktree, summary.WithPR)
}

func parseJobPruneStatuses(raw []string, useDefault bool) (map[job.Status]bool, error) {
	if useDefault {
		return map[job.Status]bool{job.StatusDone: true, job.StatusFailed: true}, nil
	}
	statuses := map[job.Status]bool{}
	for _, value := range splitFilterValues(raw) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		switch value {
		case "terminal", "finished":
			statuses[job.StatusDone] = true
			statuses[job.StatusFailed] = true
		case string(job.StatusDone), string(job.StatusFailed):
			parsed, _ := job.ParseStatus(value)
			statuses[parsed] = true
		default:
			return nil, fmt.Errorf("--status must be done, failed, or terminal")
		}
	}
	if len(statuses) == 0 {
		return nil, fmt.Errorf("--status requires at least one non-empty status")
	}
	return statuses, nil
}

func parseJobNextStateFilter(raw []string, useDefault bool) (map[string]bool, error) {
	if useDefault {
		return map[string]bool{"ready": true, "queued": true}, nil
	}
	states := map[string]bool{}
	for _, value := range splitFilterValues(raw) {
		state := strings.ToLower(strings.TrimSpace(value))
		if state == "" {
			continue
		}
		switch state {
		case "all":
			return nil, nil
		case "ready", "queued", "running", "blocked", "failed", "held", "done", "none":
			states[state] = true
		default:
			return nil, fmt.Errorf("--state must be ready, queued, running, blocked, failed, held, done, none, or all")
		}
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("--state requires at least one non-empty state")
	}
	return states, nil
}

func filterJobNextResult(next jobNextResult, stateFilter map[string]bool, step string) error {
	if len(stateFilter) > 0 && !stateFilter[next.State] {
		return fmt.Errorf("job %q next-step state is %q; does not match --state", next.JobID, next.State)
	}
	stepFilter := strings.TrimSpace(step)
	if stepFilter == "" {
		return nil
	}
	currentStep := "none"
	if next.Step != nil && strings.TrimSpace(next.Step.ID) != "" {
		currentStep = next.Step.ID
	}
	if currentStep != stepFilter {
		return fmt.Errorf("job %q next step is %q; does not match --step", next.JobID, currentStep)
	}
	return nil
}

func jobStatusTerminal(status job.Status) bool {
	return status == job.StatusDone || status == job.StatusFailed
}

func parseJobUpdateClear(raw []string) (map[string]bool, error) {
	fields := map[string]bool{}
	for _, value := range splitFilterValues(raw) {
		field := strings.ToLower(strings.TrimSpace(value))
		if field == "" {
			continue
		}
		switch field {
		case "ticket-url", "ticket_url":
			fields["ticket_url"] = true
		case "instance", "branch", "worktree", "pr", "pipeline":
			fields[field] = true
		default:
			return nil, fmt.Errorf("--clear accepts ticket-url, instance, branch, worktree, pr, or pipeline")
		}
	}
	return fields, nil
}

func applyJobUpdateClears(j *job.Job, clearSet map[string]bool, changed map[string]string) {
	for field := range clearSet {
		switch field {
		case "ticket_url":
			j.TicketURL = ""
		case "instance":
			j.Instance = ""
		case "branch":
			j.Branch = ""
		case "worktree":
			j.Worktree = ""
		case "pr":
			j.PR = ""
		case "pipeline":
			j.Pipeline = ""
		}
		changed[field] = ""
	}
}

func jobUpdateFieldList(changed map[string]string) string {
	counts := map[string]int{}
	for field := range changed {
		counts[field] = 1
	}
	return strings.Join(sortedCountKeys(counts), ",")
}

func removeJobFiles(teamDir string, j *job.Job, opts jobRemoveOptions) (jobRemoveResult, error) {
	result := jobRemoveResult{
		ID:        j.ID,
		Ticket:    j.Ticket,
		Status:    j.Status,
		Action:    "removed",
		DryRun:    opts.DryRun,
		Forced:    opts.Force,
		JobPath:   job.Path(teamDir, j.ID),
		EventPath: job.EventPath(teamDir, j.ID),
	}
	if opts.DryRun {
		result.Action = "would_remove"
	}
	jobExists, err := pathExists(result.JobPath)
	if err != nil {
		return result, err
	}
	eventExists, err := pathExists(result.EventPath)
	if err != nil {
		return result, err
	}
	result.JobFile = jobExists
	result.EventLog = eventExists
	if opts.DryRun {
		return result, nil
	}
	if jobExists {
		if err := os.Remove(result.JobPath); err != nil {
			return result, err
		}
		result.Removed = true
	}
	if eventExists {
		if err := os.Remove(result.EventPath); err != nil {
			return result, err
		}
		result.EventsRemoved = true
	}
	if result.Removed || result.EventsRemoved {
		result.Removed = true
	}
	return result, nil
}

func pathExists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

func renderJobRemoveResults(w io.Writer, results []jobRemoveResult, jsonOut bool, tmpl *template.Template) error {
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
	renderJobRemoveTable(w, results)
	return nil
}

func renderJobRemoveTable(w io.Writer, results []jobRemoveResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no jobs removed)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tACTION\tJOB\tEVENTS")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			result.ID, result.Status, result.Action, yesNo(result.JobFile), yesNo(result.EventLog))
	}
	_ = tw.Flush()
}

type jobReadyOptions struct {
	Pipeline string
	States   map[string]bool
	Step     string
	Sort     string
	Limit    int
}

func runJobReady(w io.Writer, teamDir string, opts jobReadyOptions, jsonOut bool, tmpl *template.Template, commands bool) error {
	rows, err := collectJobReadyRows(teamDir, opts.Pipeline, opts.States)
	if err != nil {
		return err
	}
	rows = prepareJobReadyRows(rows, opts)
	return renderJobReadyRows(w, rows, jsonOut, tmpl, commands)
}

func renderJobReadyRows(w io.Writer, rows []jobReadyRow, jsonOut bool, tmpl *template.Template, commands bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if commands {
		return renderJobReadyCommands(w, rows)
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
	renderJobReadyTable(w, rows)
	return nil
}

func renderJobReadyCommands(w io.Writer, rows []jobReadyRow) error {
	for _, row := range rows {
		for _, action := range row.Actions {
			if strings.TrimSpace(action) == "" {
				continue
			}
			if _, err := fmt.Fprintln(w, action); err != nil {
				return err
			}
		}
	}
	return nil
}

func runJobReadyWatch(ctx context.Context, w io.Writer, teamDir string, opts jobReadyOptions, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runJobReady(w, teamDir, opts, jsonOut, tmpl, false); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func prepareJobReadyRows(rows []jobReadyRow, opts jobReadyOptions) []jobReadyRow {
	rows = filterJobReadyRowsByStep(rows, opts.Step)
	sortJobReadyRows(rows, opts.Sort)
	rows = limitJobReadyRows(rows, opts.Limit)
	return rows
}

func collectJobReadyRows(teamDir, pipeline string, states map[string]bool) ([]jobReadyRow, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	rows := make([]jobReadyRow, 0, len(jobs))
	for _, j := range jobs {
		if len(j.Steps) == 0 {
			continue
		}
		if pipeline != "" && j.Pipeline != pipeline {
			continue
		}
		next := inspectNextJobStep(j)
		if len(states) > 0 && !states[next.State] {
			continue
		}
		rows = append(rows, jobReadyRowFromJob(j, next))
	}
	return rows, nil
}

func filterJobReadyRowsByStep(rows []jobReadyRow, step string) []jobReadyRow {
	step = strings.TrimSpace(step)
	if step == "" {
		return rows
	}
	filtered := rows[:0]
	for _, row := range rows {
		if row.StepID == step {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func sortJobReadyRows(rows []jobReadyRow, sortMode string) {
	sortMode = strings.ToLower(strings.TrimSpace(sortMode))
	if sortMode == "" {
		sortMode = "job"
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		switch sortMode {
		case "state":
			if rankLeft, rankRight := jobReadyStateSortRank(left.State), jobReadyStateSortRank(right.State); rankLeft != rankRight {
				return rankLeft < rankRight
			}
		case "step":
			if left.StepID != right.StepID {
				return left.StepID < right.StepID
			}
		case "target":
			if left.Target != right.Target {
				return left.Target < right.Target
			}
		case "pipeline":
			if left.Pipeline != right.Pipeline {
				return left.Pipeline < right.Pipeline
			}
		case "updated":
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.After(right.UpdatedAt)
			}
		case "ticket":
			if left.Ticket != right.Ticket {
				return left.Ticket < right.Ticket
			}
		case "instance":
			if left.Instance != right.Instance {
				return left.Instance < right.Instance
			}
		case "label":
			if left.Label != right.Label {
				return left.Label < right.Label
			}
		}
		return left.JobID < right.JobID
	})
}

func limitJobReadyRows(rows []jobReadyRow, limit int) []jobReadyRow {
	if limit <= 0 || limit >= len(rows) {
		return rows
	}
	return rows[:limit]
}

func jobReadyStateSortRank(state string) int {
	switch state {
	case "ready":
		return 0
	case "queued":
		return 1
	case "running":
		return 2
	case "blocked":
		return 3
	case "failed":
		return 4
	case "held":
		return 5
	case "none":
		return 6
	case "done":
		return 7
	default:
		return 8
	}
}

func jobReadyRowFromJob(j *job.Job, next jobNextResult) jobReadyRow {
	row := jobReadyRow{
		JobID:      j.ID,
		Ticket:     j.Ticket,
		Pipeline:   j.Pipeline,
		JobStatus:  j.Status,
		State:      next.State,
		WaitingFor: next.WaitingFor,
		UpdatedAt:  j.UpdatedAt,
		Message:    next.Message,
	}
	if steps := advanceableJobSteps(j); len(steps) > 1 {
		row.ParallelReadySteps = len(steps)
	}
	if next.Step != nil {
		row.StepID = next.Step.ID
		row.Label = next.Step.Label
		row.Description = next.Step.Description
		row.Instructions = next.Step.Instructions
		row.Target = next.Step.Target
		row.Workspace = next.Step.Workspace
		row.Runtime = next.Step.Runtime
		row.RuntimeBin = next.Step.RuntimeBin
		row.StepStatus = next.Step.Status
		row.Instance = next.Step.Instance
		row.Gate = next.Step.Gate
		row.Optional = next.Step.Optional
		row.Attempts = next.Step.Attempts
		row.MaxAttempts = next.Step.MaxAttempts
	}
	row.Actions = actionsForJobReadyRow(row)
	return row
}

func actionsForJobReadyRow(row jobReadyRow) []string {
	switch row.State {
	case "ready":
		return appendParallelReadyAction([]string{fmt.Sprintf("agent-team job advance %s", row.JobID)}, row)
	case "queued":
		if jobReadyRowIsAdvanceable(row) {
			return appendParallelReadyAction([]string{fmt.Sprintf("agent-team job advance %s", row.JobID)}, row)
		}
		return []string{"agent-team tick"}
	case "failed":
		return []string{fmt.Sprintf("agent-team job retry %s --dispatch", row.JobID)}
	case "blocked":
		if row.Gate == job.StepGateManual {
			if len(row.WaitingFor) == 0 && strings.TrimSpace(row.StepID) != "" {
				return manualGateDecisionActions(row.JobID, row.StepID)
			}
			return nil
		}
		if row.Gate == job.StepGatePR {
			return prGateRecoveryActions(row.JobID)
		}
		return []string{jobUnblockAction(row.JobID, row.StepID)}
	case "held":
		return []string{fmt.Sprintf("agent-team job release %s", row.JobID)}
	default:
		return nil
	}
}

func filterJobReadyRowsByAdvanceable(rows []jobReadyRow) []jobReadyRow {
	if len(rows) == 0 {
		return nil
	}
	out := make([]jobReadyRow, 0, len(rows))
	for _, row := range rows {
		if jobReadyRowIsAdvanceable(row) {
			out = append(out, row)
		}
	}
	return out
}

func jobReadyRowIsAdvanceable(row jobReadyRow) bool {
	return row.State == "ready" ||
		(row.State == "queued" && len(row.WaitingFor) == 0 && strings.TrimSpace(row.Instance) == "")
}

func jobNextResultIsAdvanceable(next jobNextResult) bool {
	if next.State == "ready" {
		return true
	}
	if next.State != "queued" || len(next.WaitingFor) > 0 || next.Step == nil {
		return false
	}
	return strings.TrimSpace(next.Step.Instance) == ""
}

func appendParallelReadyAction(actions []string, row jobReadyRow) []string {
	if row.ParallelReadySteps <= 1 || strings.TrimSpace(row.Pipeline) == "" {
		return actions
	}
	return append(actions, fmt.Sprintf("agent-team pipeline advance %s --all-ready-steps --dry-run --preview-routes", row.Pipeline))
}

func renderJobReadyTable(w io.Writer, rows []jobReadyRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no ready pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tSTATE\tSTEP\tLABEL\tTARGET\tWORKSPACE\tRUNTIME\tPIPELINE\tOPTIONAL\tWAITING_FOR\tUPDATED\tACTION")
	for _, row := range rows {
		waiting := "-"
		if len(row.WaitingFor) > 0 {
			waiting = strings.Join(row.WaitingFor, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.JobID, row.State, emptyDash(row.StepID), emptyDash(row.Label), emptyDash(row.Target), emptyDash(row.Workspace), emptyDash(formatStepRuntime(row.Runtime, row.RuntimeBin)), emptyDash(row.Pipeline), yesNo(row.Optional), waiting, row.UpdatedAt.Format(time.RFC3339), emptyDash(strings.Join(row.Actions, "; ")))
	}
	_ = tw.Flush()
}

func runJobList(w io.Writer, teamDir string, filters jobListFilters, jsonOut bool, tmpl *template.Template) error {
	filtered, err := filteredJobs(teamDir, filters)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(filtered)
	}
	if tmpl != nil {
		for _, j := range filtered {
			if err := renderJobTemplate(w, j, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobTableWithRuntime(w, filtered, jobRuntimeMap(teamDir))
	return nil
}

func runJobListWatch(ctx context.Context, w io.Writer, teamDir string, filters jobListFilters, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runJobList(w, teamDir, filters, jsonOut, tmpl); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

type jobWaitTimeoutError struct {
	Job *job.Job
}

func (e *jobWaitTimeoutError) Error() string {
	return "job wait timed out"
}

func parseJobWaitStatuses(raw []string, useDefault bool) (map[job.Status]bool, error) {
	if useDefault {
		return map[job.Status]bool{
			job.StatusDone:   true,
			job.StatusFailed: true,
		}, nil
	}
	statuses := map[job.Status]bool{}
	for _, value := range splitFilterValues(raw) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		switch value {
		case "terminal", "finished":
			statuses[job.StatusDone] = true
			statuses[job.StatusFailed] = true
		default:
			status, err := job.ParseStatus(value)
			if err != nil {
				return nil, err
			}
			statuses[status] = true
		}
	}
	if len(statuses) == 0 && len(raw) > 0 {
		return nil, fmt.Errorf("--status requires at least one non-empty status")
	}
	return statuses, nil
}

func parseJobWaitEvents(raw []string) map[string]bool {
	events := map[string]bool{}
	for _, value := range splitFilterValues(raw) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		events[value] = true
	}
	return events
}

func runJobWait(ctx context.Context, teamDir, id string, statuses map[job.Status]bool, events map[string]bool, nextStates map[string]bool, nextStateSet bool, step string, interval time.Duration) (*job.Job, error) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	step = strings.TrimSpace(step)
	var last *job.Job
	for {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		last = j
		statusMatched := len(statuses) == 0 || statuses[j.Status]
		eventMatched := len(events) == 0 || events[strings.TrimSpace(j.LastEvent)]
		nextMatched := jobWaitNextMatched(j, nextStates, nextStateSet, step)
		if statusMatched && eventMatched && nextMatched {
			return j, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if ctx.Err() == context.DeadlineExceeded {
				return last, &jobWaitTimeoutError{Job: last}
			}
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func jobWaitNextMatched(j *job.Job, nextStates map[string]bool, nextStateSet bool, step string) bool {
	if !nextStateSet && strings.TrimSpace(step) == "" {
		return true
	}
	next := inspectNextJobStep(j)
	if nextStateSet && len(nextStates) > 0 && !nextStates[next.State] {
		return false
	}
	step = strings.TrimSpace(step)
	if step == "" {
		return true
	}
	return jobWaitNextStep(next) == step
}

func waitForJobCommand(cmd *cobra.Command, teamDir, id string, statuses map[job.Status]bool, events map[string]bool, nextStates map[string]bool, nextStateSet bool, step string, timeout, interval time.Duration, prefix string) (*job.Job, error) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	waited, err := runJobWait(ctx, teamDir, id, statuses, events, nextStates, nextStateSet, step, interval)
	if err == nil {
		return waited, nil
	}
	if timeoutErr, ok := err.(*jobWaitTimeoutError); ok {
		status := "unknown"
		event := ""
		nextState := ""
		nextStep := ""
		if timeoutErr.Job != nil {
			status = string(timeoutErr.Job.Status)
			event = strings.TrimSpace(timeoutErr.Job.LastEvent)
			next := inspectNextJobStep(timeoutErr.Job)
			nextState = next.State
			nextStep = jobWaitNextStep(next)
		}
		if nextStateSet || strings.TrimSpace(step) != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: timed out waiting for %s to reach %s (current=%s event=%s next_state=%s step=%s).\n",
				prefix, id, jobWaitConditionList(statuses, events, nextStates, nextStateSet, step), status, emptyDash(event), emptyDash(nextState), emptyDash(nextStep))
		} else if len(events) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: timed out waiting for %s to reach %s (current=%s event=%s).\n",
				prefix, id, jobWaitConditionList(statuses, events, nextStates, nextStateSet, step), status, emptyDash(event))
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: timed out waiting for %s to reach %s (current=%s).\n",
				prefix, id, jobWaitStatusList(statuses), status)
		}
		return nil, exitErr(1)
	}
	return nil, err
}

func refreshJobAdvanceResultAfterWait(res *jobAdvanceResult, waited *job.Job) {
	if res == nil || waited == nil {
		return
	}
	stepID := ""
	if res.Step != nil {
		stepID = res.Step.ID
	}
	res.Job = waited
	if stepID == "" {
		return
	}
	idx := jobStepIndex(waited, stepID)
	if idx == -1 {
		res.Step = nil
		return
	}
	res.Step = &waited.Steps[idx]
}

func jobWaitConditionList(statuses map[job.Status]bool, events map[string]bool, nextStates map[string]bool, nextStateSet bool, step string) string {
	parts := make([]string, 0, 4)
	if len(statuses) > 0 {
		parts = append(parts, "status="+jobWaitStatusList(statuses))
	}
	if len(events) > 0 {
		parts = append(parts, "event="+jobWaitEventList(events))
	}
	if nextStateSet {
		parts = append(parts, "next-state="+jobNextStateList(nextStates))
	}
	step = strings.TrimSpace(step)
	if step != "" {
		parts = append(parts, "step="+step)
	}
	return strings.Join(parts, " ")
}

func jobWaitStatusList(statuses map[job.Status]bool) string {
	order := []job.Status{job.StatusQueued, job.StatusRunning, job.StatusBlocked, job.StatusDone, job.StatusFailed}
	out := make([]string, 0, len(statuses))
	for _, status := range order {
		if statuses[status] {
			out = append(out, string(status))
		}
	}
	return strings.Join(out, "|")
}

func jobNextStateList(states map[string]bool) string {
	if len(states) == 0 {
		return "all"
	}
	order := []string{"ready", "queued", "running", "blocked", "failed", "held", "done", "none"}
	out := make([]string, 0, len(states))
	for _, state := range order {
		if states[state] {
			out = append(out, state)
		}
	}
	return strings.Join(out, "|")
}

func jobWaitNextStep(next jobNextResult) string {
	if next.Step == nil || strings.TrimSpace(next.Step.ID) == "" {
		return "none"
	}
	return next.Step.ID
}

func jobWaitEventList(events map[string]bool) string {
	out := make([]string, 0, len(events))
	for event := range events {
		out = append(out, event)
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

func parseJobReopenStatus(raw string) (job.Status, error) {
	status, err := job.ParseStatus(raw)
	if err != nil {
		return "", err
	}
	switch status {
	case job.StatusQueued, job.StatusBlocked:
		return status, nil
	default:
		return "", fmt.Errorf("--status must be queued or blocked")
	}
}

func runJobInstanceUp(cmd *cobra.Command, repo, id, stepID string, opts instanceUpOptions, readyTimeout time.Duration) error {
	if readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job start: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	teamDir, j, err := readJobAndTeamDir(cmd, repo, id)
	if err != nil {
		return err
	}
	selection, err := selectJobOwningInstance(j, stepID)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job start: %v\n", err)
		return exitErr(2)
	}
	instance := strings.TrimSpace(selection.Instance)
	if instance == "" {
		printMissingJobInstanceError(cmd.ErrOrStderr(), "start", j, selection.StepID, "dispatch or adopt it first")
		return exitErr(2)
	}
	repoRoot := filepath.Dir(teamDir)
	if !opts.DryRun {
		if err := ensureDaemonReadyWithTimeout(cmd, repoRoot, opts.JSON || opts.Quiet || opts.Summary || opts.Format != nil, readyTimeout); err != nil {
			return err
		}
	}
	if err := runInstanceUpWithOptions(cmd, repoRoot, "", []string{instance}, opts); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}
	applyJobInstanceUpUpdate(j, selection)
	return writeJobWithAudit(teamDir, j, "", "cli", "", jobInstanceSelectionAuditData(selection))
}

func applyJobInstanceUpUpdate(j *job.Job, selection jobInstanceSelection) {
	now := time.Now().UTC()
	if j.Status != job.StatusDone {
		j.Status = job.StatusRunning
	}
	if strings.TrimSpace(selection.StepID) != "" {
		applySelectedJobStepStatus(j, selection.StepID, job.StatusRunning, now)
	}
	j.LastEvent = "instance_start"
	if strings.TrimSpace(selection.Instance) != "" {
		j.LastStatus = "start " + strings.TrimSpace(selection.Instance)
	} else if strings.TrimSpace(j.Instance) != "" {
		j.LastStatus = "start " + strings.TrimSpace(j.Instance)
	} else {
		j.LastStatus = "start"
	}
	j.UpdatedAt = now
}

func runJobInstanceDown(cmd *cobra.Command, repo, id, stepID string, opts instanceDownOptions, nextStatus job.Status) error {
	teamDir, j, err := readJobAndTeamDir(cmd, repo, id)
	if err != nil {
		return err
	}
	selection, err := selectJobOwningInstance(j, stepID)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job %s: %v\n", downAction(opts), err)
		return exitErr(2)
	}
	instance := strings.TrimSpace(selection.Instance)
	if instance == "" {
		printMissingJobInstanceError(cmd.ErrOrStderr(), downAction(opts), j, selection.StepID, "dispatch or adopt it first")
		return exitErr(2)
	}
	if err := runInstanceDownWithOptions(cmd, filepath.Dir(teamDir), []string{instance}, opts); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}
	applyJobInstanceDownUpdate(j, selection, downAction(opts), nextStatus)
	return writeJobWithAudit(teamDir, j, "", "cli", "", jobInstanceSelectionAuditData(selection))
}

func applyJobInstanceDownUpdate(j *job.Job, selection jobInstanceSelection, action string, nextStatus job.Status) {
	now := time.Now().UTC()
	if nextStatus == job.StatusFailed {
		if j.Status != job.StatusDone {
			j.Status = job.StatusFailed
		}
	} else if nextStatus != "" {
		switch j.Status {
		case job.StatusQueued, job.StatusRunning:
			j.Status = nextStatus
		}
	}
	if strings.TrimSpace(selection.StepID) != "" {
		applySelectedJobStepStatus(j, selection.StepID, nextStatus, now)
	}
	j.LastEvent = "instance_" + action
	if strings.TrimSpace(selection.Instance) != "" {
		j.LastStatus = action + " " + strings.TrimSpace(selection.Instance)
	} else if strings.TrimSpace(j.Instance) != "" {
		j.LastStatus = action + " " + strings.TrimSpace(j.Instance)
	} else {
		j.LastStatus = action
	}
	j.UpdatedAt = now
}

func applySelectedJobStepStatus(j *job.Job, stepID string, status job.Status, now time.Time) {
	if j == nil {
		return
	}
	idx := jobStepIndex(j, strings.TrimSpace(stepID))
	if idx < 0 {
		return
	}
	step := &j.Steps[idx]
	switch status {
	case job.StatusRunning, job.StatusQueued:
		if step.Status != job.StatusDone {
			step.Status = status
			if step.StartedAt.IsZero() {
				step.StartedAt = now
			}
			step.FinishedAt = time.Time{}
		}
	case job.StatusBlocked:
		switch step.Status {
		case job.StatusQueued, job.StatusRunning:
			step.Status = job.StatusBlocked
		}
	case job.StatusFailed:
		if step.Status != job.StatusDone {
			step.Status = job.StatusFailed
			if step.StartedAt.IsZero() {
				step.StartedAt = now
			}
			step.FinishedAt = now
		}
	}
}

func jobInstanceSelectionAuditData(selection jobInstanceSelection) map[string]string {
	data := map[string]string{}
	if instance := strings.TrimSpace(selection.Instance); instance != "" {
		data["instance"] = instance
	}
	if stepID := strings.TrimSpace(selection.StepID); stepID != "" {
		data["step"] = stepID
	}
	if len(data) == 0 {
		return nil
	}
	return data
}

func filteredJobs(teamDir string, filters jobListFilters) ([]*job.Job, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	runtimeByInstance, err := jobRuntimeIndex(teamDir, filters)
	if err != nil {
		return nil, err
	}
	filtered := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if jobMatchesFilters(j, filters) && jobMatchesRuntimeFilter(j, filters, runtimeByInstance) {
			filtered = append(filtered, j)
		}
	}
	sortJobs(filtered, filters.Sort)
	return limitJobRows(filtered, filters.Limit), nil
}

func limitJobRows(jobs []*job.Job, limit int) []*job.Job {
	if limit <= 0 || limit >= len(jobs) {
		return jobs
	}
	return jobs[:limit]
}

func jobRuntimeIndex(teamDir string, filters jobListFilters) (map[string]string, error) {
	if len(filters.Runtimes) == 0 {
		return nil, nil
	}
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(metas))
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		out[meta.Instance] = metadataRuntimeKey(meta)
	}
	return out, nil
}

func jobMatchesFilters(j *job.Job, filters jobListFilters) bool {
	if j == nil {
		return false
	}
	if filters.Status != "" && j.Status != filters.Status {
		return false
	}
	if filters.Held != nil && j.Held != *filters.Held {
		return false
	}
	if filters.HoldExpired != nil && !jobHoldExpirationMatches(j, *filters.HoldExpired, filters.Now) {
		return false
	}
	if filters.Target != "" && j.Target != filters.Target {
		return false
	}
	if filters.Instance != "" && j.Instance != filters.Instance {
		return false
	}
	if filters.Pipeline != "" && j.Pipeline != filters.Pipeline {
		return false
	}
	if filters.PipelineOwned && strings.TrimSpace(j.Pipeline) == "" {
		return false
	}
	if filters.Ticket != "" && !containsFold(j.Ticket, filters.Ticket) && !containsFold(j.TicketURL, filters.Ticket) {
		return false
	}
	if filters.Branch != "" && j.Branch != filters.Branch {
		return false
	}
	if filters.PR != "" && !containsFold(j.PR, filters.PR) {
		return false
	}
	return true
}

func jobMatchesRuntimeFilter(j *job.Job, filters jobListFilters, runtimeByInstance map[string]string) bool {
	if len(filters.Runtimes) == 0 {
		return true
	}
	if j == nil {
		return false
	}
	if jobInstanceMatchesRuntime(j.Instance, filters.Runtimes, runtimeByInstance) {
		return true
	}
	for _, step := range j.Steps {
		if jobInstanceMatchesRuntime(step.Instance, filters.Runtimes, runtimeByInstance) {
			return true
		}
	}
	return false
}

func jobInstanceMatchesRuntime(instance string, runtimes map[string]bool, runtimeByInstance map[string]string) bool {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return false
	}
	return runtimes[runtimeByInstance[instance]]
}

func dispatchJobWithPrefix(cmd *cobra.Command, teamDir string, j *job.Job, source, workspace string, selection runtimeSelection, prefix string) (*jobDispatchResult, string, error) {
	payload, requestedName, err := buildDispatchEventPayload(j.Target, j.Ticket, j.Kickoff, j.Instance, source, workspace)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return nil, "", exitErr(2)
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	if err := applyDispatchRuntimeSelection(teamDir, payload, selection); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return nil, "", exitErr(2)
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: daemon is not running — start it with `agent-team start`.\n", prefix)
		return nil, "", exitErr(2)
	}
	res, err := dc.PublishEvent("agent.dispatch", payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return nil, "", exitErr(1)
	}
	if latest, err := job.Read(teamDir, j.ID); err == nil {
		j = latest
	}
	applyDispatchResponseToJob(j, requestedName, res)
	if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{
		"target":             j.Target,
		"requested_instance": requestedName,
	}); err != nil {
		return nil, "", err
	}
	return &jobDispatchResult{Job: j, Event: res}, requestedName, nil
}

func containsFold(value, substr string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(substr))
}

func queueItemsForJob(teamDir string, j *job.Job) ([]*daemon.QueueItem, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	matches := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if queueItemMatchesJob(item, j) {
			matches = append(matches, item)
		}
	}
	return matches, nil
}

func runJobQueueList(w io.Writer, teamDir string, j *job.Job, filters queueListFilters, opts queueListOptions, summary, jsonOut bool, tmpl *template.Template) error {
	items, err := queueItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	runtimeByInstance := queueRuntimeMap(teamDir)
	filtered := filterQueueItems(items, filters.withNow(now).withRuntimeByInstance(runtimeByInstance))
	if summary {
		queueSummary := summarizeQueueItems(filtered, now, runtimeByInstance)
		if jsonOut {
			return json.NewEncoder(w).Encode(queueSummary)
		}
		renderQueueSummary(w, queueSummary)
		return nil
	}
	filtered = prepareQueueListItems(filtered, opts, runtimeByInstance)
	if jsonOut {
		return json.NewEncoder(w).Encode(filtered)
	}
	if tmpl != nil {
		return renderQueueItemsFormat(w, filtered, tmpl)
	}
	renderQueueTableWithActions(w, filtered, runtimeByInstance, jobQueueActionResolver(j.ID))
	return nil
}

func runJobQueueListWatch(ctx context.Context, w io.Writer, teamDir string, j *job.Job, filters queueListFilters, opts queueListOptions, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runJobQueueList(w, teamDir, j, filters, opts, false, jsonOut, tmpl); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func runJobQueueSummaryWatch(ctx context.Context, w io.Writer, teamDir string, j *job.Job, filters queueListFilters, jsonOut bool, interval time.Duration, clear bool) error {
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
		if err := runJobQueueList(w, teamDir, j, filters, queueListOptions{}, true, jsonOut, nil); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func jobQueueActionResolver(jobID string) queueActionResolver {
	return func(item *daemon.QueueItem, now time.Time) []string {
		return jobQueueItemActions(jobID, item, now)
	}
}

func jobQueueItemActions(jobID string, item *daemon.QueueItem, now time.Time) []string {
	if item == nil {
		return nil
	}
	id := queueItemActionJobID(item)
	if id == "" {
		id = job.NormalizeID(jobID)
	}
	if id == "" {
		return queueItemActions(item, now)
	}
	queueCommand := func(verb string) string {
		return fmt.Sprintf("agent-team job queue %s %s %s", verb, id, item.ID)
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

func collectJobQueueQuarantineItems(teamDir string, j *job.Job, filters queueListFilters) ([]queueQuarantineItem, error) {
	items, err := listQueueQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = jobQueueQuarantineItems(j, items)
	return filterQueueQuarantineItems(items, filters), nil
}

func readJobQueueQuarantineItem(teamDir string, j *job.Job, rawPath string) (queueQuarantineItem, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	rel, err := normalizeQueueQuarantinePath(rawPath)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	item, err := inspectQueueQuarantineFile(queueRoot, rel)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	if !queueQuarantineItemMatchesJob(item, j) {
		id := ""
		if j != nil {
			id = j.ID
		}
		return queueQuarantineItem{}, fmt.Errorf("quarantined queue file %q is not owned by job %q", item.Path, id)
	}
	return item, nil
}

func jobQueueQuarantineItems(j *job.Job, items []queueQuarantineItem) []queueQuarantineItem {
	if j == nil {
		return nil
	}
	out := make([]queueQuarantineItem, 0, len(items))
	for _, item := range items {
		if queueQuarantineItemMatchesJob(item, j) {
			out = append(out, item)
		}
	}
	return out
}

func queueQuarantineItemMatchesJob(item queueQuarantineItem, j *job.Job) bool {
	if j == nil {
		return false
	}
	if id := job.NormalizeID(item.Job); id != "" && id == j.ID {
		return true
	}
	if strings.TrimSpace(j.Instance) != "" && item.InstanceID == j.Instance {
		return true
	}
	return false
}

func filteredQueueItemsForJob(teamDir string, j *job.Job, filters queueListFilters, sortMode string, limit int, now time.Time) ([]*daemon.QueueItem, error) {
	items, err := queueItemsForJob(teamDir, j)
	if err != nil {
		return nil, err
	}
	runtimeByInstance := queueRuntimeMap(teamDir)
	matches := filterQueueItems(items, filters.withNow(now).withRuntimeByInstance(runtimeByInstance))
	matches = prepareQueueActionMatches(matches, sortMode, limit, runtimeByInstance)
	return matches, nil
}

func runJobQueueRetryAll(w io.Writer, teamDir string, j *job.Job, filters queueListFilters, sortMode string, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := jobQueueRetryAllResults(teamDir, j, filters, sortMode, limit, dryRun)
	if err != nil {
		return err
	}
	return renderQueueRetryResults(w, results, jsonOut, tmpl)
}

func jobQueueRetryAllResults(teamDir string, j *job.Job, filters queueListFilters, sortMode string, limit int, dryRun bool) ([]queueRetryResult, error) {
	matches, err := filteredQueueItemsForJob(teamDir, j, filters, sortMode, limit, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return retryQueueItemMatches(teamDir, matches, dryRun)
}

func runJobQueueDropAll(w io.Writer, teamDir string, j *job.Job, filters queueListFilters, sortMode string, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := jobQueueDropAllResults(teamDir, j, filters, sortMode, limit, dryRun)
	if err != nil {
		return err
	}
	return renderQueueDropResults(w, results, jsonOut, tmpl)
}

func jobQueueDropAllResults(teamDir string, j *job.Job, filters queueListFilters, sortMode string, limit int, dryRun bool) ([]queueDropResult, error) {
	matches, err := filteredQueueItemsForJob(teamDir, j, filters, sortMode, limit, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return dropQueueItemMatches(teamDir, matches, dryRun)
}

func runJobQueuePrune(w io.Writer, teamDir string, j *job.Job, state string, olderThan time.Duration, filters queueListFilters, limit int, now time.Time, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := jobQueuePruneResults(teamDir, j, state, olderThan, filters, limit, now, dryRun)
	if err != nil {
		return err
	}
	return renderQueuePruneResults(w, results, jsonOut, tmpl)
}

func jobQueuePruneResults(teamDir string, j *job.Job, state string, olderThan time.Duration, filters queueListFilters, limit int, now time.Time, dryRun bool) ([]queuePruneResult, error) {
	items, err := queueItemsForJob(teamDir, j)
	if err != nil {
		return nil, err
	}
	runtimeByInstance := queueRuntimeMap(teamDir)
	queueFilters := filters.withNow(now).withRuntimeByInstance(runtimeByInstance)
	matches := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if queueFilters.match(item) && queueItemMatchesPrune(item, state, olderThan, now) {
			matches = append(matches, item)
		}
	}
	matches = prepareQueuePruneMatches(matches, limit)
	return pruneQueueItemMatches(teamDir, matches, dryRun)
}

func readJobQueueItem(cmdErr io.Writer, teamDir string, j *job.Job, id, verb string) (*daemon.QueueItem, error) {
	item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(cmdErr, "agent-team job queue %s: queue item %q not found.\n", verb, id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	if !queueItemMatchesJob(item, j) {
		fmt.Fprintf(cmdErr, "agent-team job queue %s: queue item %q is not owned by job %q.\n", verb, id, j.ID)
		return nil, exitErr(2)
	}
	return item, nil
}

func runJobQueueRetryOne(w, cmdErr io.Writer, teamDir string, j *job.Job, id string, dryRun, jsonOut bool, tmpl *template.Template) error {
	item, err := readJobQueueItem(cmdErr, teamDir, j, id, "retry")
	if err != nil {
		return err
	}
	results, err := retryQueueItemMatches(teamDir, []*daemon.QueueItem{item}, dryRun)
	if err != nil {
		return err
	}
	return renderQueueRetryResults(w, results, jsonOut, tmpl)
}

func runJobQueueDropOne(w, cmdErr io.Writer, teamDir string, j *job.Job, id string, dryRun, jsonOut bool, tmpl *template.Template) error {
	item, err := readJobQueueItem(cmdErr, teamDir, j, id, "drop")
	if err != nil {
		return err
	}
	results, err := dropQueueItemMatches(teamDir, []*daemon.QueueItem{item}, dryRun)
	if err != nil {
		return err
	}
	return renderQueueDropResults(w, results, jsonOut, tmpl)
}

func statusPreviewsForJob(teamDir string, j *job.Job) ([]jobStatusReconcileResult, error) {
	previews, err := reconcileJobsFromStatus(teamDir, true, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	matches := make([]jobStatusReconcileResult, 0, len(previews))
	for _, preview := range previews {
		if preview.JobID == j.ID {
			matches = append(matches, preview)
		}
	}
	return matches, nil
}

func queueItemMatchesJob(item *daemon.QueueItem, j *job.Job) bool {
	if item == nil || j == nil {
		return false
	}
	for _, key := range []string{"job_id", "job"} {
		if id := job.NormalizeID(queuePayloadString(item.Payload, key)); id != "" && id == j.ID {
			return true
		}
	}
	if ticket := queuePayloadString(item.Payload, "ticket"); ticket != "" {
		if job.NormalizeID(ticket) == j.ID || strings.EqualFold(strings.TrimSpace(ticket), strings.TrimSpace(j.Ticket)) {
			return true
		}
	}
	if strings.TrimSpace(j.Instance) != "" && item.InstanceID == j.Instance {
		return true
	}
	return false
}

func queuePayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func parseJobQueueReconcileState(raw string) (string, error) {
	state := strings.ToLower(strings.TrimSpace(raw))
	switch state {
	case "", queuePruneStateAll:
		return queuePruneStateAll, nil
	case daemon.QueueStatePending, daemon.QueueStateDead:
		return state, nil
	default:
		return "", fmt.Errorf("--state must be pending, dead, or all")
	}
}

func reconcileJobsFromQueue(teamDir, state string, dryRun bool, now time.Time) ([]jobQueueReconcileResult, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	results := make([]jobQueueReconcileResult, 0)
	for _, item := range items {
		if state != queuePruneStateAll && item.State != state {
			continue
		}
		j := jobForQueueItem(jobs, item)
		if j == nil {
			continue
		}
		result := reconcileJobFromQueueItem(j, item, dryRun, now)
		if result.Changed && !dryRun {
			if err := writeJobWithAudit(teamDir, j, "queue_reconcile", "cli", result.Message, map[string]string{
				"queue_id":    item.ID,
				"queue_state": item.State,
				"instance":    item.Instance,
				"instance_id": item.InstanceID,
			}); err != nil {
				return nil, err
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func reconcileJobsFromStatus(teamDir string, dryRun bool, now time.Time) ([]jobStatusReconcileResult, error) {
	rows := loadInstanceRows(teamDir, loadAgentNames(teamDir), now)
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	results := make([]jobStatusReconcileResult, 0)
	for _, row := range rows {
		if !row.HasFile || strings.TrimSpace(row.Phase) == "" || row.Phase == "—" || row.Phase == "?" {
			continue
		}
		j, matchedBy := jobForStatusRow(jobs, row)
		if j == nil {
			continue
		}
		if statusRowSupersededByUnblock(j, row) {
			continue
		}
		result := reconcileJobFromStatusRow(j, row, matchedBy, dryRun, now)
		if result.Changed && !dryRun {
			data := map[string]string{
				"instance":   row.Instance,
				"phase":      row.Phase,
				"matched_by": matchedBy,
			}
			if row.Job != "" {
				data["status_job"] = row.Job
			}
			if row.Ticket != "" {
				data["ticket"] = row.Ticket
			}
			if row.Branch != "" {
				data["branch"] = row.Branch
			}
			if row.PR != "" {
				data["pr"] = row.PR
			}
			if err := writeJobWithAudit(teamDir, j, "status_reconcile", "cli", result.Message, data); err != nil {
				return nil, err
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func reconcileJobsFromEvents(teamDir string, dryRun bool, now time.Time) ([]jobEventReconcileResult, error) {
	daemonRoot := daemon.DaemonRoot(teamDir)
	metas, err := daemon.ListMetadata(daemonRoot)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	results := make([]jobEventReconcileResult, 0)
	reconciledInstances := make(map[string]bool)
	for _, meta := range metas {
		if !daemonMetadataTerminal(meta) {
			continue
		}
		j, matchedBy := jobForDaemonMetadata(jobs, meta)
		if j == nil {
			continue
		}
		result := reconcileJobFromDaemonMetadata(j, meta, matchedBy, dryRun, now)
		if result.Changed && !dryRun {
			if err := writeJobWithAudit(teamDir, j, result.Event, "cli", result.Message, jobEventReconcileData(meta, matchedBy)); err != nil {
				return nil, err
			}
		}
		results = append(results, result)
		reconciledInstances[strings.TrimSpace(meta.Instance)] = true
	}
	lifecycleResults, err := reconcileJobsFromLifecycleEvents(teamDir, daemonRoot, jobs, reconciledInstances, dryRun, now)
	if err != nil {
		return nil, err
	}
	results = append(results, lifecycleResults...)
	return results, nil
}

func daemonMetadataTerminal(meta *daemon.Metadata) bool {
	if meta == nil {
		return false
	}
	switch meta.Status {
	case daemon.StatusExited, daemon.StatusCrashed:
		return strings.TrimSpace(meta.Instance) != ""
	default:
		return false
	}
}

func reconcileJobsFromLifecycleEvents(teamDir, daemonRoot string, jobs []*job.Job, reconciledInstances map[string]bool, dryRun bool, now time.Time) ([]jobEventReconcileResult, error) {
	events, err := daemon.ListLifecycleEvents(daemonRoot)
	if err != nil {
		return nil, err
	}
	latestByInstance := make(map[string]*daemon.LifecycleEvent)
	for _, ev := range events {
		if !daemonLifecycleEventTerminal(ev) {
			continue
		}
		instance := strings.TrimSpace(ev.Instance)
		if reconciledInstances[instance] {
			continue
		}
		if job.IDFromInput(ev.Job) == "" {
			continue
		}
		latestByInstance[instance] = ev
	}
	instances := make([]string, 0, len(latestByInstance))
	for instance := range latestByInstance {
		instances = append(instances, instance)
	}
	sort.Strings(instances)
	results := make([]jobEventReconcileResult, 0, len(instances))
	for _, instance := range instances {
		ev := latestByInstance[instance]
		meta := daemonMetadataFromLifecycleEvent(ev)
		j, matchedBy := jobForDaemonMetadata(jobs, meta)
		if j == nil {
			continue
		}
		result := reconcileJobFromDaemonMetadata(j, meta, matchedBy, dryRun, now)
		if result.Changed && !dryRun {
			data := jobEventReconcileDataWithSource(meta, matchedBy, "lifecycle_event")
			if ev.ID != "" {
				data["lifecycle_event_id"] = ev.ID
			}
			if err := writeJobWithAudit(teamDir, j, result.Event, "cli", result.Message, data); err != nil {
				return nil, err
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func daemonLifecycleEventTerminal(ev *daemon.LifecycleEvent) bool {
	if ev == nil || strings.TrimSpace(ev.Instance) == "" {
		return false
	}
	switch ev.Status {
	case daemon.StatusExited, daemon.StatusCrashed:
		return true
	default:
		return false
	}
}

func daemonMetadataFromLifecycleEvent(ev *daemon.LifecycleEvent) *daemon.Metadata {
	if ev == nil {
		return nil
	}
	meta := &daemon.Metadata{
		Instance: ev.Instance,
		Agent:    ev.Agent,
		Job:      ev.Job,
		Ticket:   ev.Ticket,
		Branch:   ev.Branch,
		PR:       ev.PR,
		Status:   ev.Status,
		PID:      ev.PID,
		ExitCode: ev.ExitCode,
	}
	if !ev.TS.IsZero() {
		meta.ExitedAt = ev.TS
	}
	return meta
}

func jobForDaemonMetadata(jobs []*job.Job, meta *daemon.Metadata) (*job.Job, string) {
	if meta == nil {
		return nil, ""
	}
	instance := strings.TrimSpace(meta.Instance)
	if id := job.IDFromInput(meta.Job); id != "" {
		for _, j := range jobs {
			if j.ID != id {
				continue
			}
			if len(j.Steps) > 0 && instance != "" && strings.TrimSpace(j.Instance) == "" && !jobHasStepInstance(j, instance) && jobHasAnyStepInstance(j) {
				return nil, ""
			}
			if instance == "" || strings.TrimSpace(j.Instance) == "" || strings.TrimSpace(j.Instance) == instance || jobHasStepInstance(j, instance) {
				return j, "job"
			}
			return nil, ""
		}
	}
	if instance == "" {
		return nil, ""
	}
	for _, j := range jobs {
		if strings.TrimSpace(j.Instance) == instance {
			return j, "instance"
		}
	}
	for _, j := range jobs {
		if jobHasStepInstance(j, instance) {
			return j, "step_instance"
		}
	}
	return nil, ""
}

func jobHasStepInstance(j *job.Job, instance string) bool {
	if j == nil || strings.TrimSpace(instance) == "" {
		return false
	}
	instance = strings.TrimSpace(instance)
	for _, step := range j.Steps {
		if strings.TrimSpace(step.Instance) == instance {
			return true
		}
	}
	return false
}

func jobHasAnyStepInstance(j *job.Job) bool {
	if j == nil {
		return false
	}
	for _, step := range j.Steps {
		if strings.TrimSpace(step.Instance) != "" {
			return true
		}
	}
	return false
}

func reconcileJobFromDaemonMetadata(j *job.Job, meta *daemon.Metadata, matchedBy string, dryRun bool, now time.Time) jobEventReconcileResult {
	before := cloneJobForEventReconcile(j)
	target := j
	if dryRun {
		target = cloneJobForEventReconcile(j)
	}
	outcome, eventType, message := daemonMetadataJobOutcome(meta)
	stepID, stepMatched, stepActive := reconcileJobStepFromDaemonMetadata(target, meta.Instance, outcome, now)
	if stepMatched && !stepActive {
		result := jobEventReconcileResult{
			JobID:     target.ID,
			Instance:  meta.Instance,
			Lifecycle: string(meta.Status),
			MatchedBy: matchedBy,
			StepID:    stepID,
			Event:     eventType,
			Before:    before.Status,
			After:     target.Status,
			Branch:    target.Branch,
			PR:        target.PR,
			Message:   target.LastStatus,
			ExitCode:  meta.ExitCode,
			DryRun:    dryRun,
		}
		result.Changed = jobEventReconcileChanged(before, target)
		return result
	}
	if strings.TrimSpace(meta.Instance) != "" {
		target.Instance = strings.TrimSpace(meta.Instance)
	}
	if strings.TrimSpace(meta.Ticket) != "" {
		target.Ticket = strings.TrimSpace(meta.Ticket)
	}
	if strings.TrimSpace(meta.Branch) != "" {
		target.Branch = strings.TrimSpace(meta.Branch)
	}
	if strings.TrimSpace(meta.PR) != "" {
		target.PR = strings.TrimSpace(meta.PR)
	}
	if stepMatched {
		if outcome == job.StatusDone && !allJobStepsDone(target) {
			target.Status = job.StatusRunning
			message = "completed pipeline step"
		} else {
			target.Status = outcome
		}
	} else {
		target.Status = outcome
	}
	target.LastEvent = eventType
	target.LastStatus = message
	target.UpdatedAt = now.UTC()

	result := jobEventReconcileResult{
		JobID:     target.ID,
		Instance:  meta.Instance,
		Lifecycle: string(meta.Status),
		MatchedBy: matchedBy,
		StepID:    stepID,
		Event:     eventType,
		Before:    before.Status,
		After:     target.Status,
		Branch:    target.Branch,
		PR:        target.PR,
		Message:   message,
		ExitCode:  meta.ExitCode,
		DryRun:    dryRun,
	}
	result.Changed = jobEventReconcileChanged(before, target)
	return result
}

func daemonMetadataJobOutcome(meta *daemon.Metadata) (job.Status, string, string) {
	status := job.StatusDone
	eventType := "instance_exited"
	message := "instance exited successfully"
	if meta == nil {
		return status, eventType, message
	}
	if meta.Status == daemon.StatusCrashed || (meta.ExitCode != nil && *meta.ExitCode != 0) {
		status = job.StatusFailed
		eventType = "instance_crashed"
		message = "instance crashed"
		if meta.ExitCode != nil {
			message = fmt.Sprintf("instance exited with code %d", *meta.ExitCode)
		}
	}
	return status, eventType, message
}

func reconcileJobStepFromDaemonMetadata(j *job.Job, instance string, status job.Status, now time.Time) (string, bool, bool) {
	if j == nil || strings.TrimSpace(instance) == "" {
		return "", false, false
	}
	instance = strings.TrimSpace(instance)
	for i := range j.Steps {
		step := &j.Steps[i]
		if strings.TrimSpace(step.Instance) != instance {
			continue
		}
		active := step.Status == job.StatusRunning || step.Status == job.StatusQueued
		if !active {
			return step.ID, true, false
		}
		step.Status = status
		if step.StartedAt.IsZero() {
			step.StartedAt = now.UTC()
		}
		step.FinishedAt = now.UTC()
		return step.ID, true, true
	}
	return "", false, false
}

func cloneJobForEventReconcile(j *job.Job) *job.Job {
	if j == nil {
		return nil
	}
	cloned := *j
	if len(j.Steps) > 0 {
		cloned.Steps = make([]job.Step, len(j.Steps))
		copy(cloned.Steps, j.Steps)
		for i := range cloned.Steps {
			if len(j.Steps[i].After) > 0 {
				cloned.Steps[i].After = append([]string(nil), j.Steps[i].After...)
			}
		}
	}
	return &cloned
}

func jobEventReconcileChanged(before, after *job.Job) bool {
	if before == nil || after == nil {
		return before != after
	}
	if before.Status != after.Status ||
		strings.TrimSpace(before.Instance) != strings.TrimSpace(after.Instance) ||
		strings.TrimSpace(before.Ticket) != strings.TrimSpace(after.Ticket) ||
		strings.TrimSpace(before.Branch) != strings.TrimSpace(after.Branch) ||
		strings.TrimSpace(before.PR) != strings.TrimSpace(after.PR) ||
		strings.TrimSpace(before.LastEvent) != strings.TrimSpace(after.LastEvent) ||
		strings.TrimSpace(before.LastStatus) != strings.TrimSpace(after.LastStatus) {
		return true
	}
	if len(before.Steps) != len(after.Steps) {
		return true
	}
	for i := range before.Steps {
		if before.Steps[i].Status != after.Steps[i].Status ||
			strings.TrimSpace(before.Steps[i].Instance) != strings.TrimSpace(after.Steps[i].Instance) ||
			before.Steps[i].Skipped != after.Steps[i].Skipped ||
			strings.TrimSpace(before.Steps[i].SkipReason) != strings.TrimSpace(after.Steps[i].SkipReason) ||
			!before.Steps[i].StartedAt.Equal(after.Steps[i].StartedAt) ||
			!before.Steps[i].FinishedAt.Equal(after.Steps[i].FinishedAt) {
			return true
		}
	}
	return false
}

func jobEventReconcileData(meta *daemon.Metadata, matchedBy string) map[string]string {
	return jobEventReconcileDataWithSource(meta, matchedBy, "daemon_metadata")
}

func jobEventReconcileDataWithSource(meta *daemon.Metadata, matchedBy, source string) map[string]string {
	if source == "" {
		source = "daemon_metadata"
	}
	data := map[string]string{
		"source":     source,
		"matched_by": matchedBy,
	}
	if meta == nil {
		return data
	}
	if meta.Instance != "" {
		data["instance"] = meta.Instance
	}
	if meta.Status != "" {
		data["lifecycle"] = string(meta.Status)
	}
	if meta.Job != "" {
		data["metadata_job"] = meta.Job
	}
	if meta.Ticket != "" {
		data["ticket"] = meta.Ticket
	}
	if meta.Branch != "" {
		data["branch"] = meta.Branch
	}
	if meta.PR != "" {
		data["pr"] = meta.PR
	}
	if meta.ExitCode != nil {
		data["exit_code"] = fmt.Sprint(*meta.ExitCode)
	}
	return data
}

func statusRowSupersededByUnblock(j *job.Job, row instanceRow) bool {
	if j == nil || strings.ToLower(strings.TrimSpace(row.Phase)) != "blocked" || j.LastEvent != "unblocked" {
		return false
	}
	if j.UpdatedAt.IsZero() || row.StatusAt.IsZero() {
		return false
	}
	return !row.StatusAt.After(j.UpdatedAt)
}

func jobForStatusRow(jobs []*job.Job, row instanceRow) (*job.Job, string) {
	if id := job.IDFromInput(row.Job); id != "" {
		for _, j := range jobs {
			if j.ID == id {
				return j, "job"
			}
		}
	}
	if id := job.IDFromInput(row.Ticket); id != "" {
		for _, j := range jobs {
			if j.ID == id {
				return j, "ticket"
			}
		}
	}
	ticket := strings.TrimSpace(row.Ticket)
	if ticket == "" {
		return nil, ""
	}
	for _, j := range jobs {
		if strings.EqualFold(strings.TrimSpace(j.Ticket), ticket) || strings.EqualFold(strings.TrimSpace(j.TicketURL), ticket) {
			return j, "ticket"
		}
	}
	return nil, ""
}

func reconcileJobFromStatusRow(j *job.Job, row instanceRow, matchedBy string, dryRun bool, now time.Time) jobStatusReconcileResult {
	before := j.Status
	after := statusReconciledJobState(j.Status, row.Phase)
	message := strings.TrimSpace(row.Summary)
	if message == "" {
		message = "status " + strings.TrimSpace(row.Phase)
	}
	result := jobStatusReconcileResult{
		JobID:     j.ID,
		Instance:  row.Instance,
		Phase:     row.Phase,
		MatchedBy: matchedBy,
		Before:    before,
		After:     after,
		Branch:    row.Branch,
		PR:        row.PR,
		Message:   message,
		DryRun:    dryRun,
	}
	if j.Status != after ||
		strings.TrimSpace(j.Instance) != strings.TrimSpace(row.Instance) ||
		(row.Branch != "" && strings.TrimSpace(j.Branch) != strings.TrimSpace(row.Branch)) ||
		(row.PR != "" && strings.TrimSpace(j.PR) != strings.TrimSpace(row.PR)) ||
		strings.TrimSpace(j.LastStatus) != message {
		result.Changed = true
	}
	if dryRun || !result.Changed {
		return result
	}
	j.Status = after
	if row.Instance != "" {
		j.Instance = row.Instance
	}
	if row.Branch != "" {
		j.Branch = row.Branch
	}
	if row.PR != "" {
		j.PR = row.PR
	}
	if row.Ticket != "" && strings.TrimSpace(j.Ticket) == "" {
		j.Ticket = row.Ticket
	}
	j.LastEvent = "status_reconcile"
	j.LastStatus = message
	j.UpdatedAt = now.UTC()
	return result
}

func statusReconciledJobState(current job.Status, phase string) job.Status {
	phase = strings.ToLower(strings.TrimSpace(phase))
	if current == job.StatusDone {
		return current
	}
	if current == job.StatusFailed && phase != "done" {
		return current
	}
	switch phase {
	case "planning", "implementing", "awaiting_review":
		return job.StatusRunning
	case "blocked":
		return job.StatusBlocked
	case "done":
		return job.StatusDone
	default:
		return current
	}
}

func jobForQueueItem(jobs []*job.Job, item *daemon.QueueItem) *job.Job {
	for _, j := range jobs {
		if queueItemMatchesJob(item, j) {
			return j
		}
	}
	return nil
}

func reconcileJobFromQueueItem(j *job.Job, item *daemon.QueueItem, dryRun bool, now time.Time) jobQueueReconcileResult {
	before := j.Status
	after, event, status := queueReconciledJobState(j, item, now)
	result := jobQueueReconcileResult{
		JobID:      j.ID,
		QueueID:    item.ID,
		QueueState: item.State,
		Before:     before,
		After:      after,
		Instance:   item.InstanceID,
		Message:    status,
		DryRun:     dryRun,
	}
	if j.Status == job.StatusDone {
		return result
	}
	if j.Status != after || j.LastEvent != event || j.LastStatus != status || (item.InstanceID != "" && j.Instance != item.InstanceID) {
		result.Changed = true
	}
	if dryRun || !result.Changed {
		return result
	}
	j.Status = after
	j.LastEvent = event
	j.LastStatus = status
	if item.InstanceID != "" {
		j.Instance = item.InstanceID
	}
	if item.Instance != "" {
		j.Target = item.Instance
	}
	j.UpdatedAt = now.UTC()
	return result
}

func queueReconciledJobState(j *job.Job, item *daemon.QueueItem, now time.Time) (job.Status, string, string) {
	if j.Status == job.StatusDone {
		return j.Status, j.LastEvent, j.LastStatus
	}
	switch item.State {
	case daemon.QueueStateDead:
		status := strings.TrimSpace(item.LastError)
		if status == "" {
			status = "dead-lettered queue item " + item.ID
		}
		return job.StatusFailed, "queue_dead", status
	case daemon.QueueStatePending:
		status := "queued"
		if !item.NextRetry.IsZero() {
			if item.NextRetry.After(now.UTC()) {
				status = "retry at " + item.NextRetry.Format(time.RFC3339)
			} else {
				status = "ready to retry"
			}
		}
		return job.StatusQueued, "queue_pending", status
	default:
		return j.Status, j.LastEvent, j.LastStatus
	}
}

func applyDispatchResponseToJob(j *job.Job, requestedName string, res *eventResponse) {
	now := time.Now().UTC()
	j.UpdatedAt = now
	if strings.TrimSpace(j.Instance) == "" {
		j.Instance = requestedName
	}
	for _, d := range res.Dispatched {
		if id, _ := d["instance_id"].(string); strings.TrimSpace(id) != "" {
			j.Instance = id
			j.Status = job.StatusRunning
			j.LastEvent = "dispatched"
			j.LastStatus = "running"
			return
		}
	}
	if len(res.Queued) > 0 {
		if queued := strings.TrimSpace(res.Queued[0]); queued != "" {
			j.Instance = queued
		} else if strings.TrimSpace(j.Instance) == "" {
			j.Instance = requestedName
		}
		j.Status = job.StatusQueued
		j.LastEvent = "queued"
		j.LastStatus = "queued"
		return
	}
	if len(res.Messaged) > 0 {
		j.Instance = res.Messaged[0]
		j.Status = job.StatusRunning
		j.LastEvent = "messaged"
		j.LastStatus = "running"
		return
	}
	for _, r := range res.Rejected {
		reason, _ := r["reason"].(string)
		if id, _ := r["instance_id"].(string); strings.TrimSpace(id) != "" {
			j.Instance = id
		}
		if strings.Contains(reason, "already running") {
			j.Status = job.StatusRunning
			j.LastEvent = "already_running"
			j.LastStatus = reason
			return
		}
		if strings.Contains(reason, "already queued") {
			j.Status = job.StatusQueued
			j.LastEvent = "already_queued"
			j.LastStatus = reason
			return
		}
		j.Status = job.StatusFailed
		j.LastEvent = "dispatch_rejected"
		j.LastStatus = reason
		return
	}
	if len(res.Matched) == 0 {
		j.Status = job.StatusFailed
		j.LastEvent = "dispatch_no_match"
		j.LastStatus = "no triggers matched"
	}
}

type jobStepUpdate struct {
	Message      string
	Instance     string
	PR           string
	Branch       string
	Worktree     string
	Skip         bool
	CountAttempt bool
}

type jobAdvanceResult struct {
	Job     *job.Job       `json:"job"`
	Step    *job.Step      `json:"step,omitempty"`
	Event   *eventResponse `json:"event,omitempty"`
	Message string         `json:"message,omitempty"`
}

type jobDispatchResult struct {
	Job   *job.Job       `json:"job"`
	Event *eventResponse `json:"event"`
}

type jobQueueReconcileResult struct {
	JobID      string     `json:"job_id"`
	QueueID    string     `json:"queue_id"`
	QueueState string     `json:"queue_state"`
	Before     job.Status `json:"before"`
	After      job.Status `json:"after"`
	Instance   string     `json:"instance,omitempty"`
	Message    string     `json:"message,omitempty"`
	Changed    bool       `json:"changed"`
	DryRun     bool       `json:"dry_run,omitempty"`
}

func validateJobStepRunningOwner(j *job.Job, stepID string, status job.Status, instance string, force bool) error {
	if status != job.StatusRunning || force {
		return nil
	}
	idx := jobStepIndex(j, stepID)
	if idx == -1 {
		return nil
	}
	if strings.TrimSpace(instance) != "" || strings.TrimSpace(j.Steps[idx].Instance) != "" {
		return nil
	}
	return fmt.Errorf("status running requires --instance for step %q; pass --force to record an ownerless running step", stepID)
}

type jobEventReconcileResult struct {
	JobID     string     `json:"job_id"`
	Instance  string     `json:"instance"`
	Lifecycle string     `json:"lifecycle"`
	MatchedBy string     `json:"matched_by"`
	StepID    string     `json:"step_id,omitempty"`
	Event     string     `json:"event"`
	Before    job.Status `json:"before"`
	After     job.Status `json:"after"`
	Branch    string     `json:"branch,omitempty"`
	PR        string     `json:"pr,omitempty"`
	Message   string     `json:"message,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Changed   bool       `json:"changed"`
	DryRun    bool       `json:"dry_run,omitempty"`
}

type jobStatusReconcileResult struct {
	JobID     string     `json:"job_id"`
	Instance  string     `json:"instance"`
	Phase     string     `json:"phase"`
	MatchedBy string     `json:"matched_by"`
	Before    job.Status `json:"before"`
	After     job.Status `json:"after"`
	Branch    string     `json:"branch,omitempty"`
	PR        string     `json:"pr,omitempty"`
	Message   string     `json:"message,omitempty"`
	Changed   bool       `json:"changed"`
	DryRun    bool       `json:"dry_run,omitempty"`
}

type jobNextResult struct {
	JobID      string     `json:"job_id"`
	Ticket     string     `json:"ticket"`
	Pipeline   string     `json:"pipeline,omitempty"`
	JobStatus  job.Status `json:"job_status"`
	State      string     `json:"state"`
	Step       *job.Step  `json:"step,omitempty"`
	WaitingFor []string   `json:"waiting_for,omitempty"`
	Actions    []string   `json:"actions,omitempty"`
	Message    string     `json:"message"`
}

type jobExplainResult struct {
	JobID     string           `json:"job_id"`
	Ticket    string           `json:"ticket"`
	Pipeline  string           `json:"pipeline,omitempty"`
	JobStatus job.Status       `json:"job_status"`
	State     string           `json:"state"`
	Message   string           `json:"message"`
	Next      jobExplainNext   `json:"next"`
	Steps     []jobExplainStep `json:"steps,omitempty"`
	Actions   []string         `json:"actions,omitempty"`
}

type jobExplainNext struct {
	State        string     `json:"state"`
	StepID       string     `json:"step_id,omitempty"`
	Label        string     `json:"label,omitempty"`
	Description  string     `json:"description,omitempty"`
	Instructions string     `json:"instructions,omitempty"`
	Target       string     `json:"target,omitempty"`
	Workspace    string     `json:"workspace,omitempty"`
	Runtime      string     `json:"runtime,omitempty"`
	RuntimeBin   string     `json:"runtime_bin,omitempty"`
	Status       job.Status `json:"status,omitempty"`
	Instance     string     `json:"instance,omitempty"`
	WaitingFor   []string   `json:"waiting_for,omitempty"`
	Actions      []string   `json:"actions,omitempty"`
	Message      string     `json:"message"`
}

type jobExplainStep struct {
	ID           string     `json:"id"`
	Label        string     `json:"label,omitempty"`
	Description  string     `json:"description,omitempty"`
	Instructions string     `json:"instructions,omitempty"`
	Target       string     `json:"target"`
	Workspace    string     `json:"workspace,omitempty"`
	Runtime      string     `json:"runtime,omitempty"`
	RuntimeBin   string     `json:"runtime_bin,omitempty"`
	Status       job.Status `json:"status"`
	State        string     `json:"state"`
	Ready        bool       `json:"ready,omitempty"`
	Instance     string     `json:"instance,omitempty"`
	After        []string   `json:"after,omitempty"`
	Gate         string     `json:"gate,omitempty"`
	Optional     bool       `json:"optional,omitempty"`
	Timeout      string     `json:"timeout,omitempty"`
	Attempts     int        `json:"attempts,omitempty"`
	MaxAttempts  int        `json:"max_attempts,omitempty"`
	WaitingFor   []string   `json:"waiting_for,omitempty"`
	Actions      []string   `json:"actions,omitempty"`
	Skipped      bool       `json:"skipped,omitempty"`
	SkipReason   string     `json:"skip_reason,omitempty"`
	StartedAt    string     `json:"started_at,omitempty"`
	FinishedAt   string     `json:"finished_at,omitempty"`
	Message      string     `json:"message"`
}

type jobReadyRow struct {
	JobID              string     `json:"job_id"`
	Ticket             string     `json:"ticket"`
	Pipeline           string     `json:"pipeline,omitempty"`
	JobStatus          job.Status `json:"job_status"`
	State              string     `json:"state"`
	Actions            []string   `json:"actions,omitempty"`
	StepID             string     `json:"step_id,omitempty"`
	Label              string     `json:"label,omitempty"`
	Description        string     `json:"description,omitempty"`
	Instructions       string     `json:"instructions,omitempty"`
	Target             string     `json:"target,omitempty"`
	Workspace          string     `json:"workspace,omitempty"`
	Runtime            string     `json:"runtime,omitempty"`
	RuntimeBin         string     `json:"runtime_bin,omitempty"`
	StepStatus         job.Status `json:"step_status,omitempty"`
	Instance           string     `json:"instance,omitempty"`
	Gate               string     `json:"gate,omitempty"`
	Optional           bool       `json:"optional,omitempty"`
	Attempts           int        `json:"attempts,omitempty"`
	MaxAttempts        int        `json:"max_attempts,omitempty"`
	WaitingFor         []string   `json:"waiting_for,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at"`
	Message            string     `json:"message"`
	ParallelReadySteps int        `json:"parallel_ready_steps,omitempty"`
}

func updateJobStep(j *job.Job, stepID string, status job.Status, update jobStepUpdate) error {
	idx := jobStepIndex(j, stepID)
	if idx == -1 {
		return fmt.Errorf("step %q not found", stepID)
	}
	now := time.Now().UTC()
	step := &j.Steps[idx]
	step.Status = status
	if strings.TrimSpace(update.Instance) != "" {
		step.Instance = strings.TrimSpace(update.Instance)
	}
	if update.CountAttempt && (status == job.StatusRunning || status == job.StatusQueued) {
		step.Attempts++
	}
	if (status == job.StatusRunning || status == job.StatusQueued) && step.StartedAt.IsZero() {
		step.StartedAt = now
	}
	if status == job.StatusDone || status == job.StatusFailed {
		if step.StartedAt.IsZero() {
			step.StartedAt = now
		}
		step.FinishedAt = now
	}
	if update.PR != "" {
		j.PR = update.PR
	}
	if update.Branch != "" {
		j.Branch = update.Branch
	}
	if update.Worktree != "" {
		j.Worktree = update.Worktree
	}
	message := strings.TrimSpace(update.Message)
	if status == job.StatusDone && update.Skip {
		step.Skipped = true
		step.SkipReason = message
	} else {
		step.Skipped = false
		step.SkipReason = ""
	}
	if status == job.StatusDone && update.Skip {
		j.LastEvent = "step_skipped"
	} else {
		j.LastEvent = "step_" + string(status)
	}
	if message != "" {
		j.LastStatus = message
	} else if status == job.StatusDone && update.Skip {
		j.LastStatus = stepID + " skipped"
	} else {
		j.LastStatus = stepID + " " + string(status)
	}
	j.UpdatedAt = now
	switch status {
	case job.StatusFailed:
		if step.Optional {
			if allJobStepsDone(j) {
				j.Status = job.StatusDone
				j.LastEvent = "pipeline_done"
				j.LastStatus = jobStepsCompleteMessage(j)
			} else {
				j.Status = job.StatusRunning
			}
		} else {
			j.Status = job.StatusFailed
		}
	case job.StatusBlocked:
		j.Status = job.StatusBlocked
	case job.StatusDone:
		if allJobStepsDone(j) {
			j.Status = job.StatusDone
			j.LastEvent = "pipeline_done"
			j.LastStatus = jobStepsCompleteMessage(j)
		} else {
			j.Status = job.StatusRunning
		}
	default:
		j.Status = status
	}
	return nil
}

func advanceJob(cmd *cobra.Command, teamDir string, j *job.Job, workspace string, selection runtimeSelection) (*jobAdvanceResult, error) {
	if j != nil && j.Held {
		return &jobAdvanceResult{Job: j, Message: heldJobMessage(j)}, nil
	}
	step := nextReadyJobStep(j)
	if step == nil {
		now := time.Now().UTC()
		if allJobStepsDone(j) {
			j.Status = job.StatusDone
			j.LastEvent = "pipeline_done"
			j.LastStatus = jobStepsCompleteMessage(j)
			j.UpdatedAt = now
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
				return nil, err
			}
			return &jobAdvanceResult{Job: j, Message: jobStepsCompleteMessage(j)}, nil
		}
		j.Status = job.StatusBlocked
		j.LastEvent = "advance_blocked"
		j.LastStatus = "no ready steps"
		j.UpdatedAt = now
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
			return nil, err
		}
		return &jobAdvanceResult{Job: j, Message: "no ready steps"}, nil
	}
	return advanceJobStep(cmd, teamDir, j, step, workspace, selection)
}

func advanceJobStep(cmd *cobra.Command, teamDir string, j *job.Job, step *job.Step, workspace string, selection runtimeSelection) (*jobAdvanceResult, error) {
	if step == nil {
		return &jobAdvanceResult{Job: j, Message: "no ready steps"}, nil
	}
	stepID := step.ID
	name := step.Instance
	if strings.TrimSpace(name) == "" {
		name = step.Target + "-" + j.ID + "-" + job.NormalizeID(stepID)
	}
	payload, requestedName, err := buildDispatchEventPayload(step.Target, j.Ticket, job.StepDispatchKickoff(j.Kickoff, step.ID, step.Instructions), name, "job:"+j.ID, workspaceForJobStep(step, workspace))
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
		return nil, exitErr(2)
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	if j.Pipeline != "" {
		payload["pipeline"] = j.Pipeline
	}
	payload["pipeline_step"] = stepID
	if err := applyDispatchRuntimeSelection(teamDir, payload, runtimeSelectionForJobStep(step, selection)); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
		return nil, exitErr(2)
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job advance: daemon is not running — start it with `agent-team start`.")
		return nil, exitErr(2)
	}
	res, err := dc.PublishEvent("agent.dispatch", payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
		return nil, exitErr(1)
	}
	if latest, err := job.Read(teamDir, j.ID); err == nil {
		j = latest
	}
	applyAdvanceResponseToJobStep(j, stepID, requestedName, res)
	if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": stepID}); err != nil {
		return nil, err
	}
	if idx := jobStepIndex(j, stepID); idx >= 0 {
		return &jobAdvanceResult{Job: j, Step: &j.Steps[idx], Event: res}, nil
	}
	return &jobAdvanceResult{Job: j, Event: res}, nil
}

func applyAdvanceResponseToJobStep(j *job.Job, stepID, requestedName string, res *eventResponse) {
	status := job.StatusFailed
	instance := requestedName
	lastEvent := "advance_rejected"
	lastStatus := "dispatch rejected"
	for _, d := range res.Dispatched {
		if id, _ := d["instance_id"].(string); strings.TrimSpace(id) != "" {
			status = job.StatusRunning
			instance = id
			lastEvent = "advance_dispatched"
			lastStatus = "running " + stepID
			goto done
		}
	}
	if len(res.Queued) > 0 {
		status = job.StatusQueued
		if queued := strings.TrimSpace(res.Queued[0]); queued != "" {
			instance = queued
		}
		lastEvent = "advance_queued"
		lastStatus = "queued " + stepID
		goto done
	}
	if len(res.Messaged) > 0 {
		status = job.StatusRunning
		instance = res.Messaged[0]
		lastEvent = "advance_messaged"
		lastStatus = "running " + stepID
		goto done
	}
	for _, r := range res.Rejected {
		reason, _ := r["reason"].(string)
		if id, _ := r["instance_id"].(string); strings.TrimSpace(id) != "" {
			instance = id
		}
		if strings.Contains(reason, "already running") {
			status = job.StatusRunning
			lastEvent = "advance_already_running"
			lastStatus = reason
			goto done
		}
		if strings.Contains(reason, "already queued") {
			status = job.StatusQueued
			lastEvent = "advance_already_queued"
			lastStatus = reason
			goto done
		}
		lastStatus = reason
		break
	}
	if len(res.Matched) == 0 {
		lastEvent = "advance_no_match"
		lastStatus = "no triggers matched"
	}
done:
	_ = updateJobStep(j, stepID, status, jobStepUpdate{Instance: instance, Message: lastStatus, CountAttempt: pipelineStepAttemptCounted(lastEvent)})
	j.LastEvent = lastEvent
	j.LastStatus = lastStatus
}

func pipelineStepAttemptCounted(event string) bool {
	switch event {
	case "advance_dispatched", "advance_queued", "advance_messaged":
		return true
	default:
		return false
	}
}

func inspectNextJobStep(j *job.Job) (res jobNextResult) {
	res = jobNextResult{
		JobID:     j.ID,
		Ticket:    j.Ticket,
		Pipeline:  j.Pipeline,
		JobStatus: j.Status,
	}
	defer func() {
		res.Actions = actionsForJobNextResult(j, res)
	}()
	if j.Held {
		res.State = "held"
		res.Message = heldJobMessage(j)
		return res
	}
	if len(j.Steps) == 0 {
		res.State = "none"
		res.Message = "job has no pipeline steps"
		return res
	}
	if step := nextReadyJobStep(j); step != nil {
		res.Step = cloneJobStep(step)
		res.State = "ready"
		if step.Status == job.StatusQueued {
			res.State = "queued"
			res.Message = "step " + step.ID + " is queued and ready"
		} else {
			res.Message = "step " + step.ID + " is ready"
		}
		return res
	}
	if step := firstJobStepWithStatus(j, job.StatusRunning); step != nil {
		res.State = "running"
		res.Step = cloneJobStep(step)
		res.Message = "step " + step.ID + " is running"
		return res
	}
	if step := firstJobStepWithStatus(j, job.StatusQueued); step != nil {
		res.State = "queued"
		res.Step = cloneJobStep(step)
		res.WaitingFor = jobStepWaitingFor(j, step)
		res.Message = "step " + step.ID + " is queued"
		return res
	}
	if allJobStepsDone(j) {
		res.State = "done"
		res.Message = jobStepsCompleteMessage(j)
		return res
	}
	if step := firstRequiredJobStepWithStatus(j, job.StatusFailed); step != nil {
		res.State = "failed"
		res.Step = cloneJobStep(step)
		res.Message = "step " + step.ID + " failed"
		return res
	}
	if step := firstJobStepWithStatus(j, job.StatusBlocked); step != nil {
		res.State = "blocked"
		res.Step = cloneJobStep(step)
		res.WaitingFor = jobStepWaitingFor(j, step)
		switch {
		case step.Gate == job.StepGateManual && len(res.WaitingFor) > 0:
			res.Message = "step " + step.ID + " is waiting for " + strings.Join(res.WaitingFor, ",") + " before manual approval"
		case step.Gate == job.StepGateManual:
			res.Message = "step " + step.ID + " is waiting for manual approval"
		case step.Gate == job.StepGatePR && len(res.WaitingFor) > 0:
			res.Message = "step " + step.ID + " is waiting for " + strings.Join(res.WaitingFor, ",")
		case step.Gate == job.StepGatePR:
			res.Message = "step " + step.ID + " is waiting for PR metadata"
		case len(res.WaitingFor) > 0:
			res.Message = "step " + step.ID + " is waiting for " + strings.Join(res.WaitingFor, ",")
		default:
			res.Message = "step " + step.ID + " is blocked"
		}
		return res
	}
	res.State = "blocked"
	res.Message = "no ready steps"
	return res
}

func actionsForJobNextResult(j *job.Job, res jobNextResult) []string {
	if j == nil || len(j.Steps) == 0 {
		return nil
	}
	row := jobReadyRowFromJob(j, res)
	return row.Actions
}

func explainJobPipeline(j *job.Job) jobExplainResult {
	if j == nil {
		return jobExplainResult{
			State:   "missing",
			Message: "job is missing",
		}
	}
	next := inspectNextJobStep(j)
	res := jobExplainResult{
		JobID:     next.JobID,
		Ticket:    next.Ticket,
		Pipeline:  next.Pipeline,
		JobStatus: next.JobStatus,
		State:     next.State,
		Message:   next.Message,
		Next:      jobExplainNextFromResult(next),
		Actions:   append([]string(nil), next.Actions...),
	}
	ready := map[string]bool{}
	for _, step := range advanceableJobSteps(j) {
		if step != nil {
			ready[step.ID] = true
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		waiting := jobStepWaitingFor(j, step)
		row := jobExplainStep{
			ID:           step.ID,
			Label:        step.Label,
			Description:  step.Description,
			Instructions: step.Instructions,
			Target:       step.Target,
			Workspace:    step.Workspace,
			Runtime:      step.Runtime,
			RuntimeBin:   step.RuntimeBin,
			Status:       step.Status,
			Ready:        ready[step.ID],
			Instance:     step.Instance,
			After:        append([]string(nil), step.After...),
			Gate:         step.Gate,
			Optional:     step.Optional,
			Timeout:      step.Timeout,
			Attempts:     step.Attempts,
			MaxAttempts:  step.MaxAttempts,
			WaitingFor:   waiting,
			Skipped:      step.Skipped,
			SkipReason:   step.SkipReason,
			StartedAt:    jobExplainTime(step.StartedAt),
			FinishedAt:   jobExplainTime(step.FinishedAt),
		}
		row.State = explainJobStepState(j, step, row.Ready, waiting)
		row.Message = explainJobStepMessage(j, step, row.State, waiting)
		row.Actions = actionsForJobExplainStep(j, step, row.State)
		res.Steps = append(res.Steps, row)
	}
	return res
}

func jobExplainNextFromResult(next jobNextResult) jobExplainNext {
	out := jobExplainNext{
		State:      next.State,
		WaitingFor: append([]string(nil), next.WaitingFor...),
		Actions:    append([]string(nil), next.Actions...),
		Message:    next.Message,
	}
	if next.Step != nil {
		out.StepID = next.Step.ID
		out.Label = next.Step.Label
		out.Description = next.Step.Description
		out.Instructions = next.Step.Instructions
		out.Target = next.Step.Target
		out.Workspace = next.Step.Workspace
		out.Runtime = next.Step.Runtime
		out.RuntimeBin = next.Step.RuntimeBin
		out.Status = next.Step.Status
		out.Instance = next.Step.Instance
	}
	return out
}

func explainJobStepState(j *job.Job, step *job.Step, ready bool, waiting []string) string {
	if step == nil {
		return "unknown"
	}
	if ready {
		return "ready"
	}
	if step.Skipped {
		return "skipped"
	}
	switch step.Status {
	case job.StatusRunning:
		return "running"
	case job.StatusDone:
		return "done"
	case job.StatusFailed:
		return "failed"
	case job.StatusQueued:
		if len(waiting) > 0 || stepGatePending(j, step) {
			return "waiting"
		}
		return "queued"
	case job.StatusBlocked:
		if len(waiting) > 0 || stepGatePending(j, step) {
			return "waiting"
		}
		return "blocked"
	default:
		return string(step.Status)
	}
}

func explainJobStepMessage(j *job.Job, step *job.Step, state string, waiting []string) string {
	if step == nil {
		return "step is unavailable"
	}
	switch state {
	case "ready":
		return "ready to advance"
	case "running":
		if strings.TrimSpace(step.Instance) != "" {
			return "running in " + step.Instance
		}
		return "running"
	case "skipped":
		if strings.TrimSpace(step.SkipReason) != "" {
			return "skipped: " + step.SkipReason
		}
		return "skipped"
	case "done":
		return "done"
	case "failed":
		if step.Optional {
			return "optional failed"
		}
		return "failed"
	case "waiting":
		switch {
		case step.Gate == job.StepGateManual && len(waiting) > 0:
			return "waiting for " + strings.Join(waiting, ",") + " before manual approval"
		case step.Gate == job.StepGateManual:
			return "waiting for manual approval"
		case step.Gate == job.StepGatePR && len(waiting) > 0:
			return "waiting for " + strings.Join(waiting, ",")
		case step.Gate == job.StepGatePR:
			return "waiting for PR metadata"
		case len(waiting) > 0:
			return "waiting for " + strings.Join(waiting, ",")
		default:
			return "waiting"
		}
	case "queued":
		return "queued"
	case "blocked":
		return "blocked"
	default:
		return state
	}
}

func actionsForJobExplainStep(j *job.Job, step *job.Step, state string) []string {
	if j == nil || step == nil {
		return nil
	}
	switch {
	case state == "ready":
		return []string{fmt.Sprintf("agent-team job advance %s", j.ID)}
	case state == "waiting" && step.Gate == job.StepGateManual:
		return manualGateDecisionActions(j.ID, step.ID)
	case state == "waiting" && step.Gate == job.StepGatePR:
		return prGateRecoveryActions(j.ID)
	case state == "failed":
		return []string{fmt.Sprintf("agent-team job retry %s --dry-run --dispatch", j.ID)}
	default:
		return nil
	}
}

func manualGateDecisionActions(jobID, stepID string) []string {
	return []string{
		fmt.Sprintf("agent-team job approve %s --step %s", jobID, stepID),
		fmt.Sprintf("agent-team job reject %s --step %s", jobID, stepID),
	}
}

func prGateRecoveryActions(jobID string) []string {
	return []string{
		fmt.Sprintf("agent-team job update %s --pr <url> --advance --dry-run", jobID),
		"agent-team intake github --payload-file github-webhook.json --reconcile-job --advance --dry-run",
	}
}

func jobExplainTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func cloneJobStep(step *job.Step) *job.Step {
	if step == nil {
		return nil
	}
	cloned := *step
	return &cloned
}

func firstJobStepWithStatus(j *job.Job, status job.Status) *job.Step {
	for i := range j.Steps {
		if j.Steps[i].Status == status {
			return &j.Steps[i]
		}
	}
	return nil
}

func firstRequiredJobStepWithStatus(j *job.Job, status job.Status) *job.Step {
	for i := range j.Steps {
		if j.Steps[i].Optional {
			continue
		}
		if j.Steps[i].Status == status {
			return &j.Steps[i]
		}
	}
	return nil
}

func unmetJobStepDependencies(j *job.Job, step *job.Step) []string {
	if step == nil || len(step.After) == 0 {
		return nil
	}
	done := map[string]bool{}
	for _, candidate := range j.Steps {
		if jobStepSatisfiesDependency(&candidate) {
			done[candidate.ID] = true
		}
	}
	var waiting []string
	for _, dep := range step.After {
		if !done[dep] {
			waiting = append(waiting, dep)
		}
	}
	return waiting
}

func jobStepWaitingFor(j *job.Job, step *job.Step) []string {
	waiting := append([]string(nil), unmetJobStepDependencies(j, step)...)
	if stepPRGatePending(j, step) {
		waiting = append(waiting, "pr")
	}
	return waiting
}

func nextReadyJobStep(j *job.Job) *job.Step {
	if j == nil || j.Held {
		return nil
	}
	done := map[string]bool{}
	for _, step := range j.Steps {
		if jobStepSatisfiesDependency(&step) {
			done[step.ID] = true
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if stepGatePending(j, step) {
			continue
		}
		if step.Status == job.StatusDone || step.Status == job.StatusFailed || step.Status == job.StatusRunning || step.Status == job.StatusQueued {
			continue
		}
		ready := true
		for _, dep := range step.After {
			if !done[dep] {
				ready = false
				break
			}
		}
		if ready {
			return step
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != job.StatusQueued {
			continue
		}
		if stepGatePending(j, step) {
			continue
		}
		ready := true
		for _, dep := range step.After {
			if !done[dep] {
				ready = false
				break
			}
		}
		if ready {
			return step
		}
	}
	return nil
}

func advanceableJobSteps(j *job.Job) []*job.Step {
	if j == nil || j.Held {
		return nil
	}
	done := map[string]bool{}
	for _, step := range j.Steps {
		if jobStepSatisfiesDependency(&step) {
			done[step.ID] = true
		}
	}
	isReady := func(step *job.Step) bool {
		if stepGatePending(j, step) {
			return false
		}
		for _, dep := range step.After {
			if !done[dep] {
				return false
			}
		}
		return true
	}
	var ready []*job.Step
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status == job.StatusDone || step.Status == job.StatusFailed || step.Status == job.StatusRunning {
			continue
		}
		if step.Status == job.StatusQueued && strings.TrimSpace(step.Instance) != "" {
			continue
		}
		if isReady(step) {
			ready = append(ready, step)
		}
	}
	return ready
}

func stepManualGatePending(step *job.Step) bool {
	return step != nil && step.Status == job.StatusBlocked && step.Gate == job.StepGateManual
}

func selectManualGateForApproval(j *job.Job, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if j == nil || len(j.Steps) == 0 {
		return "", fmt.Errorf("job has no pipeline steps")
	}
	if requested != "" {
		idx := jobStepIndex(j, requested)
		if idx == -1 {
			return "", fmt.Errorf("step %q not found", requested)
		}
		step := &j.Steps[idx]
		if step.Gate != job.StepGateManual {
			return "", fmt.Errorf("step %q is not a manual gate", requested)
		}
		if step.Status != job.StatusBlocked {
			return "", fmt.Errorf("step %q is %s, not blocked", requested, step.Status)
		}
		return step.ID, nil
	}
	next := inspectNextJobStep(j)
	if next.Step != nil && next.Step.Gate == job.StepGateManual && next.Step.Status == job.StatusBlocked {
		return next.Step.ID, nil
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Gate == job.StepGateManual && step.Status == job.StatusBlocked {
			return step.ID, nil
		}
	}
	return "", fmt.Errorf("job has no blocked manual gate")
}

func optionalSendMessageBody(flagValue, fileValue string, positional []string) (string, error) {
	if strings.TrimSpace(flagValue) == "" && strings.TrimSpace(fileValue) == "" && len(positional) == 0 {
		return "", nil
	}
	return sendMessageBody(flagValue, fileValue, positional)
}

func stepPRGatePending(j *job.Job, step *job.Step) bool {
	return step != nil && step.Gate == job.StepGatePR && strings.TrimSpace(j.PR) == ""
}

func stepGatePending(j *job.Job, step *job.Step) bool {
	return stepManualGatePending(step) || stepPRGatePending(j, step)
}

func jobStepSatisfiesDependency(step *job.Step) bool {
	if step == nil {
		return false
	}
	return step.Status == job.StatusDone || (step.Optional && step.Status == job.StatusFailed)
}

func jobHasOptionalFailedStep(j *job.Job) bool {
	if j == nil {
		return false
	}
	for i := range j.Steps {
		if j.Steps[i].Optional && j.Steps[i].Status == job.StatusFailed {
			return true
		}
	}
	return false
}

func jobStepsCompleteMessage(j *job.Job) string {
	if jobHasOptionalFailedStep(j) {
		return "all required steps done"
	}
	return "all steps done"
}

func heldJobMessage(j *job.Job) string {
	if j == nil {
		return "job is held"
	}
	msg := "job is held"
	if until := jobHoldUntilText(j); until != "" {
		if jobHoldExpired(j, time.Now().UTC()) {
			msg = "job hold expired at " + until
		} else {
			msg += " until " + until
		}
	}
	if strings.TrimSpace(j.HoldReason) != "" {
		return msg + ": " + strings.TrimSpace(j.HoldReason)
	}
	return msg
}

func resetFailedPipelineStepForRetry(j *job.Job) string {
	return resetFailedPipelineStepForRetryByID(j, "")
}

func resetFailedPipelineStepForRetryByID(j *job.Job, stepID string) string {
	result := resetFailedPipelineStepForRetryByIDWithReason(j, stepID, false)
	return result.StepID
}

type pipelineStepRetryReset struct {
	StepID      string
	Reason      string
	Attempts    int
	MaxAttempts int
}

func resetFailedPipelineStepForRetryByIDWithReason(j *job.Job, stepID string, force bool) pipelineStepRetryReset {
	stepID = strings.TrimSpace(stepID)
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != job.StatusFailed {
			continue
		}
		if stepID != "" && step.ID != stepID {
			continue
		}
		if len(unmetJobStepDependencies(j, step)) > 0 {
			continue
		}
		attempts := effectiveJobStepAttempts(step)
		if attempts, reason := jobStepRetryLimitReason(step); reason != "" && !force {
			return pipelineStepRetryReset{Reason: reason, Attempts: attempts, MaxAttempts: step.MaxAttempts}
		}
		step.Status = job.StatusBlocked
		step.Instance = ""
		step.StartedAt = time.Time{}
		step.FinishedAt = time.Time{}
		return pipelineStepRetryReset{StepID: step.ID, Attempts: attempts, MaxAttempts: step.MaxAttempts}
	}
	return pipelineStepRetryReset{Reason: "no retryable failed step"}
}

func jobStepRetryLimitReason(step *job.Step) (int, string) {
	if step == nil || step.MaxAttempts <= 0 {
		return 0, ""
	}
	attempts := effectiveJobStepAttempts(step)
	if attempts < step.MaxAttempts {
		return attempts, ""
	}
	return attempts, fmt.Sprintf("max attempts reached (%d/%d)", attempts, step.MaxAttempts)
}

func effectiveJobStepAttempts(step *job.Step) int {
	if step == nil {
		return 0
	}
	if step.Attempts > 0 {
		return step.Attempts
	}
	if !step.StartedAt.IsZero() || !step.FinishedAt.IsZero() || strings.TrimSpace(step.Instance) != "" {
		return 1
	}
	switch step.Status {
	case job.StatusRunning, job.StatusQueued, job.StatusDone, job.StatusFailed:
		return 1
	default:
		return 0
	}
}

func allJobStepsDone(j *job.Job) bool {
	if len(j.Steps) == 0 {
		return false
	}
	for _, step := range j.Steps {
		if !jobStepSatisfiesDependency(&step) {
			return false
		}
	}
	return true
}

func jobStepIndex(j *job.Job, stepID string) int {
	for i, step := range j.Steps {
		if step.ID == stepID {
			return i
		}
	}
	return -1
}

func renderJobAdvanceResult(w io.Writer, res *jobAdvanceResult) error {
	if res.Message != "" {
		fmt.Fprintf(w, "Job: %s %s\n", res.Job.ID, res.Message)
		return nil
	}
	if res.Step != nil {
		fmt.Fprintf(w, "Job: %s advanced step=%s status=%s instance=%s\n",
			res.Job.ID, res.Step.ID, res.Step.Status, emptyDash(res.Step.Instance))
	}
	if res.Event != nil {
		renderDispatchOutcome(w, "", "", res.Event)
	}
	return nil
}

func renderJobAdvanceResultFormat(w io.Writer, res *jobAdvanceResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, res); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderJobNextResult(w io.Writer, res jobNextResult, jsonOut bool, tmpl *template.Template, commandsOnly bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(res)
	}
	if commandsOnly {
		for _, action := range res.Actions {
			fmt.Fprintln(w, action)
		}
		return nil
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, res); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if res.Step == nil {
		fmt.Fprintf(w, "Job: %s state=%s message=%q\n", res.JobID, res.State, res.Message)
		return nil
	}
	after := "-"
	if len(res.Step.After) > 0 {
		after = strings.Join(res.Step.After, ",")
	}
	gate := "-"
	if res.Step.Gate != "" {
		gate = res.Step.Gate
	}
	optional := ""
	if res.Step.Optional {
		optional = " optional=true"
	}
	waiting := "-"
	if len(res.WaitingFor) > 0 {
		waiting = strings.Join(res.WaitingFor, ",")
	}
	actions := "-"
	if len(res.Actions) > 0 {
		actions = strings.Join(res.Actions, "; ")
	}
	attempts := formatJobStepAttempts(res.Step.Attempts, res.Step.MaxAttempts)
	fmt.Fprintf(w, "Job: %s next step=%s state=%s status=%s target=%s workspace=%s runtime=%s instance=%s after=%s gate=%s%s attempts=%s waiting_for=%s actions=%s\n",
		res.JobID, res.Step.ID, res.State, res.Step.Status, res.Step.Target, emptyDash(res.Step.Workspace), emptyDash(formatStepRuntime(res.Step.Runtime, res.Step.RuntimeBin)), emptyDash(res.Step.Instance), after, gate, optional, attempts, waiting, actions)
	return nil
}

func formatJobStepAttempts(attempts, maxAttempts int) string {
	if maxAttempts > 0 {
		return fmt.Sprintf("%d/%d", attempts, maxAttempts)
	}
	if attempts > 0 {
		return fmt.Sprintf("%d", attempts)
	}
	return "-"
}

func renderJobExplainResult(w io.Writer, res jobExplainResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(res)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, res); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	pipeline := emptyDash(res.Pipeline)
	fmt.Fprintf(w, "Job: %s ticket=%s pipeline=%s status=%s state=%s\n",
		res.JobID, res.Ticket, pipeline, res.JobStatus, res.State)
	if res.Message != "" {
		fmt.Fprintf(w, "Message: %s\n", res.Message)
	}
	if len(res.Steps) == 0 {
		fmt.Fprintln(w, "Steps: none")
	} else {
		fmt.Fprintln(w, "Steps:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tLABEL\tTARGET\tWORKSPACE\tRUNTIME\tSTATUS\tSTATE\tINSTANCE\tAFTER\tGATE\tOPTIONAL\tTIMEOUT\tATTEMPTS\tWAITING_FOR\tACTION")
		for _, step := range res.Steps {
			action := "-"
			if len(step.Actions) > 0 {
				action = strings.Join(step.Actions, "; ")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				step.ID,
				emptyDash(step.Label),
				emptyDash(step.Target),
				emptyDash(step.Workspace),
				emptyDash(formatStepRuntime(step.Runtime, step.RuntimeBin)),
				step.Status,
				emptyDash(step.State),
				emptyDash(step.Instance),
				listDash(step.After),
				emptyDash(step.Gate),
				yesNo(step.Optional),
				emptyDash(step.Timeout),
				formatJobStepAttempts(step.Attempts, step.MaxAttempts),
				listDash(step.WaitingFor),
				action)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(res.Actions) > 0 {
		fmt.Fprintln(w, "Actions:")
		for _, action := range res.Actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	return nil
}

func jobExplainCommandActions(res jobExplainResult) []string {
	actions := make([]string, 0, len(res.Actions)+len(res.Next.Actions))
	actions = append(actions, res.Actions...)
	actions = append(actions, res.Next.Actions...)
	for _, step := range res.Steps {
		actions = append(actions, step.Actions...)
	}
	return commandActionsOnly(actions)
}

func renderJobExplainCommands(w io.Writer, res jobExplainResult) error {
	return renderActionCommands(w, jobExplainCommandActions(res))
}

func runJobExplain(w io.Writer, teamDir, id string, stateFilter map[string]bool, step string, jsonOut, commands bool, tmpl *template.Template) error {
	j, err := job.Read(teamDir, id)
	if err != nil {
		return err
	}
	explained := explainJobPipeline(j)
	stepFilter := strings.TrimSpace(step)
	if stepFilter != "" {
		var ok bool
		explained, ok = filterJobExplainResultByStep(explained, stepFilter)
		if !ok {
			return fmt.Errorf("step %q not found in job %q", stepFilter, explained.JobID)
		}
	}
	if len(stateFilter) > 0 && !stateFilter[explained.State] {
		return fmt.Errorf("job %q next-step state is %q; does not match --state", explained.JobID, explained.State)
	}
	if commands {
		return renderJobExplainCommands(w, explained)
	}
	return renderJobExplainResult(w, explained, jsonOut, tmpl)
}

func runJobExplainWatch(ctx context.Context, w io.Writer, teamDir, id string, stateFilter map[string]bool, step string, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runJobExplain(w, teamDir, id, stateFilter, step, jsonOut, false, tmpl); err != nil {
			return err
		}
		if !waitForWatchTick(ctx, ticker.C) {
			return nil
		}
		if !jsonOut && !clear {
			fmt.Fprintln(w)
		}
	}
}

func validateJobCleanupReady(j *job.Job) error {
	if j == nil {
		return fmt.Errorf("job is required")
	}
	if j.Status != job.StatusDone {
		return fmt.Errorf("job %q is %s; close or reconcile it as done before cleanup", j.ID, j.Status)
	}
	if !jobNeedsCleanup(j) {
		return fmt.Errorf("job %q has no worktree or branch to clean", j.ID)
	}
	return nil
}

func previewJobCleanup(repoRoot string, j *job.Job, forceBranch bool, verifyPR bool) (jobCleanupPreview, error) {
	preview := jobCleanupPreview{
		JobID:       j.ID,
		Worktree:    strings.TrimSpace(j.Worktree),
		Branch:      strings.TrimSpace(j.Branch),
		ForceBranch: forceBranch,
		VerifyPR:    verifyPR,
		DryRun:      true,
	}
	if verifyPR {
		verification, err := verifyJobPRMerged(repoRoot, j)
		if err != nil {
			return preview, err
		}
		preview.PRVerification = &verification
	}
	if preview.Worktree != "" {
		if err := validateJobOwnedWorktree(repoRoot, preview.Worktree); err != nil {
			return preview, err
		}
		exists, err := pathExists(preview.Worktree)
		if err != nil {
			return preview, err
		}
		preview.WorktreeExists = exists
		preview.WouldRemoveWorktree = exists
	}
	if preview.Branch != "" {
		preview.BranchDeleteMode = jobCleanupBranchDeleteMode(forceBranch)
		exists, err := gitBranchExists(repoRoot, preview.Branch)
		if err != nil {
			return preview, err
		}
		preview.BranchExists = exists
		preview.WouldRemoveBranch = exists
	}
	preview.Summary = jobCleanupPreviewSummary(preview)
	return preview, nil
}

func jobCleanupPreviewSummary(preview jobCleanupPreview) string {
	wouldRemove := []string{}
	if preview.WouldRemoveWorktree {
		wouldRemove = append(wouldRemove, "worktree")
	}
	if preview.WouldRemoveBranch {
		wouldRemove = append(wouldRemove, jobCleanupRemovedBranchSummary(preview.ForceBranch))
	}
	if len(wouldRemove) == 0 {
		return "nothing to clean"
	}
	return "would remove " + strings.Join(wouldRemove, " and ")
}

func jobCleanupBranchDeleteMode(force bool) string {
	if force {
		return "force_delete"
	}
	return "safe_delete"
}

func jobCleanupRemovedBranchSummary(force bool) string {
	if force {
		return "branch (force)"
	}
	return "branch"
}

func renderJobCleanupPreview(w io.Writer, preview jobCleanupPreview) {
	fmt.Fprintf(w, "Job: %s cleanup dry-run (%s)\n", preview.JobID, preview.Summary)
	if preview.Worktree != "" {
		fmt.Fprintf(w, "Worktree: %s exists=%s remove=%s\n", preview.Worktree, yesNo(preview.WorktreeExists), yesNo(preview.WouldRemoveWorktree))
	}
	if preview.Branch != "" {
		mode := emptyDash(preview.BranchDeleteMode)
		fmt.Fprintf(w, "Branch:   %s exists=%s remove=%s mode=%s\n", preview.Branch, yesNo(preview.BranchExists), yesNo(preview.WouldRemoveBranch), mode)
	}
	if preview.PRVerification != nil {
		verify := preview.PRVerification
		fmt.Fprintf(w, "PR:       %s merged=%s state=%s source=%s\n", verify.URL, yesNo(verify.Verified), emptyDash(verify.State), verify.Source)
	}
}

func parseJobCleanupFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-cleanup-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobCleanupFormat(w io.Writer, value any, tmpl *template.Template) error {
	if err := tmpl.Execute(w, value); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func runJobCleanupAll(teamDir, repoRoot string, dryRun, merged, forceBranch bool, verifyPR bool) (jobCleanupBatchResult, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return jobCleanupBatchResult{}, err
	}
	return runJobCleanupJobs(teamDir, repoRoot, jobs, dryRun, merged, forceBranch, verifyPR), nil
}

func runJobCleanupJobs(teamDir, repoRoot string, jobs []*job.Job, dryRun, merged, forceBranch bool, verifyPR bool) jobCleanupBatchResult {
	candidates := cleanupReadyJobs(jobs)
	result := jobCleanupBatchResult{
		DryRun:      dryRun,
		Merged:      merged,
		ForceBranch: forceBranch,
		VerifyPR:    verifyPR,
		Total:       len(candidates),
		Items:       make([]jobCleanupBatchItem, 0, len(candidates)),
	}
	for _, j := range candidates {
		item := jobCleanupBatchItem{
			JobID:    j.ID,
			Status:   j.Status,
			Worktree: strings.TrimSpace(j.Worktree),
			Branch:   strings.TrimSpace(j.Branch),
		}
		if dryRun {
			preview, err := previewJobCleanup(repoRoot, j, forceBranch, verifyPR)
			if err != nil {
				item.Error = err.Error()
				result.Failed++
			} else {
				item.Summary = preview.Summary
				item.Preview = &preview
				result.Previewed++
			}
			result.Items = append(result.Items, item)
			continue
		}
		summary, err := cleanupJobOwnedWorktree(repoRoot, j, forceBranch, verifyPR)
		if err != nil {
			item.Error = err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		j.Worktree = ""
		j.Branch = ""
		j.LastEvent = "cleanup"
		j.LastStatus = summary
		j.UpdatedAt = time.Now().UTC()
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
			item.Error = err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		item.Summary = summary
		item.Job = j
		result.Cleaned++
		result.Items = append(result.Items, item)
	}
	return result
}

func cleanupReadyJobs(jobs []*job.Job) []*job.Job {
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j == nil || j.Status != job.StatusDone || !jobNeedsCleanup(j) {
			continue
		}
		out = append(out, j)
	}
	return out
}

func renderJobCleanupBatch(w io.Writer, result jobCleanupBatchResult) {
	if result.Team != "" {
		fmt.Fprintf(w, "Team: %s\n", result.Team)
	}
	if result.Pipeline != "" {
		fmt.Fprintf(w, "Pipeline: %s\n", result.Pipeline)
	}
	if result.Total == 0 {
		fmt.Fprintln(w, "No cleanup-ready jobs.")
		return
	}
	if result.DryRun {
		fmt.Fprintf(w, "Job cleanup dry-run: cleanup-ready jobs=%d previewed=%d failed=%d\n", result.Total, result.Previewed, result.Failed)
	} else {
		fmt.Fprintf(w, "Job cleanup: cleanup-ready jobs=%d cleaned=%d failed=%d\n", result.Total, result.Cleaned, result.Failed)
	}
	for _, item := range result.Items {
		if item.Error != "" {
			fmt.Fprintf(w, "Job: %s cleanup failed (%s)\n", item.JobID, item.Error)
			continue
		}
		if result.DryRun && item.Preview != nil {
			renderJobCleanupPreview(w, *item.Preview)
			continue
		}
		fmt.Fprintf(w, "Job: %s cleanup complete (%s)\n", item.JobID, item.Summary)
	}
}

func renderJobCleanupPreviewCommands(w io.Writer, preview jobCleanupPreview, j *job.Job, opts jobCleanupCommandOptions) error {
	if !jobCleanupPreviewHasApplyCommand(preview) || j == nil || j.Status != job.StatusDone {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(jobCleanupApplyCommandArgs(opts)), " "))
	return err
}

func renderJobCleanupBatchCommands(w io.Writer, result jobCleanupBatchResult, opts jobCleanupCommandOptions) error {
	if !jobCleanupBatchHasApplyCommand(result) {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(jobCleanupApplyCommandArgs(opts)), " "))
	return err
}

func jobCleanupBatchHasApplyCommand(result jobCleanupBatchResult) bool {
	if !result.DryRun || result.Failed > 0 {
		return false
	}
	for _, item := range result.Items {
		if item.Preview != nil && jobCleanupPreviewHasApplyCommand(*item.Preview) {
			return true
		}
	}
	return false
}

func jobCleanupPreviewHasApplyCommand(preview jobCleanupPreview) bool {
	return preview.DryRun && (preview.WouldRemoveWorktree || preview.WouldRemoveBranch)
}

func jobCleanupApplyCommandArgs(opts jobCleanupCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.All {
		args = append(args, "--all")
	}
	args = append(args, "--merged")
	if opts.ForceBranch {
		args = append(args, "--force-branch")
	}
	if opts.VerifyPR {
		args = append(args, "--verify-pr")
	}
	return args
}

func cleanupJobOwnedWorktree(repoRoot string, j *job.Job, forceBranch bool, verifyPR bool) (string, error) {
	if strings.TrimSpace(j.Worktree) == "" && strings.TrimSpace(j.Branch) == "" {
		return "nothing to clean", nil
	}
	if verifyPR {
		if _, err := verifyJobPRMerged(repoRoot, j); err != nil {
			return "", err
		}
	}
	removed := make([]string, 0, 2)
	if strings.TrimSpace(j.Worktree) != "" {
		if err := validateJobOwnedWorktree(repoRoot, j.Worktree); err != nil {
			return "", err
		}
		if _, err := os.Stat(j.Worktree); err == nil {
			if out, err := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", j.Worktree).CombinedOutput(); err != nil {
				return "", fmt.Errorf("remove worktree %s: %w: %s", j.Worktree, err, strings.TrimSpace(string(out)))
			}
			removed = append(removed, "worktree")
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if strings.TrimSpace(j.Branch) != "" {
		exists, err := gitBranchExists(repoRoot, j.Branch)
		if err != nil {
			return "", err
		}
		if exists {
			deleteFlag := "-d"
			if forceBranch {
				deleteFlag = "-D"
			}
			if out, err := exec.Command("git", "-C", repoRoot, "branch", deleteFlag, j.Branch).CombinedOutput(); err != nil {
				return "", fmt.Errorf("remove branch %s: %w: %s", j.Branch, err, strings.TrimSpace(string(out)))
			}
			removed = append(removed, jobCleanupRemovedBranchSummary(forceBranch))
		}
	}
	if len(removed) == 0 {
		return "nothing to clean", nil
	}
	return "removed " + strings.Join(removed, " and "), nil
}

func verifyJobPRMerged(repoRoot string, j *job.Job) (jobPRMergeVerification, error) {
	if j == nil {
		return jobPRMergeVerification{}, fmt.Errorf("job is required")
	}
	prURL := strings.TrimSpace(j.PR)
	if prURL == "" {
		return jobPRMergeVerification{}, fmt.Errorf("job %q has no recorded PR URL to verify", j.ID)
	}
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return jobPRMergeVerification{}, fmt.Errorf("verify PR merge for job %q: gh CLI not found in PATH", j.ID)
	}
	cmd := exec.Command(ghPath, "pr", "view", prURL, "--json", "merged,state,mergeCommit")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return jobPRMergeVerification{}, fmt.Errorf("verify PR merge for job %q: %w: %s", j.ID, err, strings.TrimSpace(string(out)))
	}
	var view struct {
		Merged      bool   `json:"merged"`
		State       string `json:"state"`
		MergeCommit *struct {
			OID string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal(out, &view); err != nil {
		return jobPRMergeVerification{}, fmt.Errorf("verify PR merge for job %q: decode gh output: %w", j.ID, err)
	}
	verification := jobPRMergeVerification{
		URL:      prURL,
		Verified: view.Merged || strings.EqualFold(view.State, "MERGED"),
		State:    view.State,
		Source:   "gh",
	}
	if view.MergeCommit != nil {
		verification.MergeCommit = strings.TrimSpace(view.MergeCommit.OID)
	}
	if !verification.Verified {
		state := emptyDash(verification.State)
		return verification, fmt.Errorf("verify PR merge for job %q: PR is not merged (state=%s)", j.ID, state)
	}
	return verification, nil
}

func validateJobOwnedWorktree(repoRoot, worktreePath string) error {
	rawRoot, err := filepath.Abs(filepath.Join(repoRoot, ".claude", "worktrees"))
	if err != nil {
		return err
	}
	rawPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return err
	}
	if pathInsideDir(rawRoot, rawPath) {
		return nil
	}
	root := resolvePathWithExistingPrefix(rawRoot)
	path := resolvePathWithExistingPrefix(rawPath)
	if pathInsideDir(root, path) {
		return nil
	}
	return fmt.Errorf("refusing to remove worktree outside %s: %s", root, path)
}

func resolvePathWithExistingPrefix(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	missing := []string{}
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathInsideDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func gitBranchExists(repoRoot, branch string) (bool, error) {
	out, err := exec.Command("git", "-C", repoRoot, "branch", "--list", branch, "--format", "%(refname:short)").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("list branch %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == branch {
			return true, nil
		}
	}
	return false, nil
}

func parseJobFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobCancelFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-cancel-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobEventFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-event-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobNextFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-next-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobExplainFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-explain-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobReadyFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-ready-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobReadySort(raw string) (string, error) {
	sortMode := strings.ToLower(strings.TrimSpace(raw))
	switch sortMode {
	case "", "job", "state", "step", "target", "pipeline", "updated", "ticket", "instance", "label":
		if sortMode == "" {
			return "job", nil
		}
		return sortMode, nil
	default:
		return "", fmt.Errorf("--sort must be job, state, step, target, pipeline, updated, ticket, instance, or label")
	}
}

func parseJobAdvanceFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-advance-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobStepFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-step-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobRemoveFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-remove-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobQueueReconcileFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-queue-reconcile-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobEventReconcileFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-event-reconcile-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobStatusReconcileFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-status-reconcile-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobResult(w io.Writer, j *job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(j)
	}
	if tmpl != nil {
		return renderJobTemplate(w, j, tmpl)
	}
	renderJobDetail(w, j)
	return nil
}

type jobCreatePreview struct {
	Job    *job.Job `json:"job"`
	DryRun bool     `json:"dry_run"`
}

func renderJobCreatePreview(w io.Writer, j *job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(jobCreatePreview{Job: j, DryRun: true})
	}
	if tmpl != nil {
		return renderJobTemplate(w, j, tmpl)
	}
	fmt.Fprintln(w, "Dry run: true")
	renderJobDetail(w, j)
	return nil
}

type jobDispatchPreview struct {
	Job      *job.Job              `json:"job"`
	Dispatch *dispatchRoutePreview `json:"dispatch"`
	DryRun   bool                  `json:"dry_run"`
}

func renderJobDispatchPreview(w io.Writer, j *job.Job, dispatch *dispatchRoutePreview, jsonOut bool, tmpl *template.Template) error {
	result := jobDispatchPreview{Job: j, Dispatch: dispatch, DryRun: true}
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
	requestedName := ""
	if dispatch != nil {
		requestedName = dispatch.RequestedName
	}
	fmt.Fprintf(w, "Job: %s dry-run dispatch target=%s instance=%s\n", j.ID, j.Target, requestedName)
	if dispatch == nil || dispatch.Preview == nil || !eventPublishPreviewHasRoutes(dispatch.Preview) {
		fmt.Fprintln(w, "(no triggers matched)")
		return nil
	}
	return renderEventPublishRoutePreview(w, dispatch.Preview)
}

type jobSendPreview struct {
	ID       string   `json:"id"`
	Job      *job.Job `json:"job"`
	DryRun   bool     `json:"dry_run"`
	Instance string   `json:"instance"`
	From     string   `json:"from"`
	Message  string   `json:"message"`
}

func renderJobSendPreview(w io.Writer, j *job.Job, instance, from, message string, jsonOut bool, tmpl *template.Template) error {
	from = strings.TrimSpace(from)
	if from == "" {
		from = "(cli)"
	}
	result := jobSendPreview{
		ID:       j.ID,
		Job:      j,
		DryRun:   true,
		Instance: strings.TrimSpace(instance),
		From:     from,
		Message:  strings.TrimSpace(message),
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
	fmt.Fprintf(w, "  would-send   %-20s job=%s\n", result.Instance, j.ID)
	return nil
}

type jobAdvancePreview struct {
	Job      *job.Job              `json:"job"`
	Step     *job.Step             `json:"step,omitempty"`
	Dispatch *dispatchRoutePreview `json:"dispatch,omitempty"`
	Message  string                `json:"message,omitempty"`
	DryRun   bool                  `json:"dry_run"`
}

func previewJobAdvanceDispatch(teamDir string, j *job.Job, workspace string, selection runtimeSelection) (*jobAdvancePreview, error) {
	if j != nil && j.Held {
		return &jobAdvancePreview{Job: j, Message: heldJobMessage(j), DryRun: true}, nil
	}
	step := nextReadyJobStep(j)
	if step == nil {
		message := "no ready steps"
		if allJobStepsDone(j) {
			message = jobStepsCompleteMessage(j)
		}
		return &jobAdvancePreview{Job: j, Message: message, DryRun: true}, nil
	}
	return previewJobStepDispatch(teamDir, j, step, workspace, selection)
}

func previewJobStepDispatch(teamDir string, j *job.Job, step *job.Step, workspace string, selection runtimeSelection) (*jobAdvancePreview, error) {
	if step == nil {
		return &jobAdvancePreview{Job: j, Message: "no ready steps", DryRun: true}, nil
	}
	name := step.Instance
	if strings.TrimSpace(name) == "" {
		name = step.Target + "-" + j.ID + "-" + job.NormalizeID(step.ID)
	}
	payload, requestedName, err := buildDispatchEventPayload(step.Target, j.Ticket, job.StepDispatchKickoff(j.Kickoff, step.ID, step.Instructions), name, "job:"+j.ID, workspaceForJobStep(step, workspace))
	if err != nil {
		return nil, err
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	if j.Pipeline != "" {
		payload["pipeline"] = j.Pipeline
	}
	payload["pipeline_step"] = step.ID
	if err := applyDispatchRuntimeSelection(teamDir, payload, runtimeSelectionForJobStep(step, selection)); err != nil {
		return nil, err
	}
	dispatch, err := previewDispatchPayload(teamDir, step.Target, requestedName, payload)
	if err != nil {
		return nil, err
	}
	return &jobAdvancePreview{Job: j, Step: step, Dispatch: dispatch, DryRun: true}, nil
}

func workspaceForJobStep(step *job.Step, requested string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" && requested != "auto" {
		return requested
	}
	if step != nil {
		if workspace := strings.TrimSpace(step.Workspace); workspace != "" {
			return workspace
		}
	}
	return requested
}

func runtimeSelectionForJobStep(step *job.Step, requested runtimeSelection) runtimeSelection {
	if strings.TrimSpace(requested.Kind) != "" || strings.TrimSpace(requested.Binary) != "" {
		return requested
	}
	if step == nil {
		return requested
	}
	return runtimeSelection{Kind: strings.TrimSpace(step.Runtime), Binary: strings.TrimSpace(step.RuntimeBin)}
}

func formatStepRuntime(kind, binary string) string {
	kind = strings.TrimSpace(kind)
	binary = strings.TrimSpace(binary)
	if kind == "" {
		return ""
	}
	if binary != "" {
		return kind + ":" + binary
	}
	return kind
}

func renderJobAdvancePreview(w io.Writer, preview *jobAdvancePreview, jsonOut bool, tmpl *template.Template) error {
	if preview == nil {
		preview = &jobAdvancePreview{DryRun: true}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(preview)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, preview); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if preview.Message != "" {
		fmt.Fprintf(w, "Job: %s dry-run advance %s\n", preview.Job.ID, preview.Message)
		return nil
	}
	requestedName := ""
	if preview.Dispatch != nil {
		requestedName = preview.Dispatch.RequestedName
	}
	stepID := ""
	target := ""
	if preview.Step != nil {
		stepID = preview.Step.ID
		target = preview.Step.Target
	}
	fmt.Fprintf(w, "Job: %s dry-run advance step=%s target=%s instance=%s\n", preview.Job.ID, stepID, target, requestedName)
	if preview.Dispatch == nil || preview.Dispatch.Preview == nil || !eventPublishPreviewHasRoutes(preview.Dispatch.Preview) {
		fmt.Fprintln(w, "(no triggers matched)")
		return nil
	}
	return renderEventPublishRoutePreview(w, preview.Dispatch.Preview)
}

type jobApproveApplyCommandOptions struct {
	JobID             string
	Repo              string
	RepoSet           bool
	Advance           bool
	Workspace         string
	WorkspaceSet      bool
	RuntimeKind       string
	RuntimeKindSet    bool
	RuntimeBin        string
	RuntimeBinSet     bool
	Step              string
	StepSet           bool
	Message           string
	MessageSet        bool
	MessageFile       string
	MessageFileSet    bool
	PositionalMessage []string
}

type jobStepApplyCommandOptions struct {
	JobID          string
	Step           string
	Repo           string
	RepoSet        bool
	Status         job.Status
	StatusSet      bool
	Message        string
	MessageSet     bool
	Instance       string
	InstanceSet    bool
	PR             string
	PRSet          bool
	Branch         string
	BranchSet      bool
	Worktree       string
	WorktreeSet    bool
	Advance        bool
	Skip           bool
	Force          bool
	Workspace      string
	WorkspaceSet   bool
	RuntimeKind    string
	RuntimeKindSet bool
	RuntimeBin     string
	RuntimeBinSet  bool
}

func renderJobStepApplyCommand(w io.Writer, hasAction bool, opts jobStepApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(jobStepApplyCommandArgs(opts)), " "))
	return err
}

func jobStepApplyCommandArgs(opts jobStepApplyCommandOptions) []string {
	args := []string{"agent-team", "job", "step", opts.JobID, opts.Step}
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.StatusSet {
		args = append(args, "--status", string(opts.Status))
	}
	if opts.MessageSet {
		args = append(args, "--message", opts.Message)
	}
	if opts.InstanceSet && strings.TrimSpace(opts.Instance) != "" {
		args = append(args, "--instance", opts.Instance)
	}
	if opts.PRSet && strings.TrimSpace(opts.PR) != "" {
		args = append(args, "--pr", opts.PR)
	}
	if opts.BranchSet && strings.TrimSpace(opts.Branch) != "" {
		args = append(args, "--branch", opts.Branch)
	}
	if opts.WorktreeSet && strings.TrimSpace(opts.Worktree) != "" {
		args = append(args, "--worktree", opts.Worktree)
	}
	if opts.Skip {
		args = append(args, "--skip")
	}
	if opts.Advance {
		args = append(args, "--advance")
	}
	if opts.WorkspaceSet && strings.TrimSpace(opts.Workspace) != "" {
		args = append(args, "--workspace", opts.Workspace)
	}
	if opts.RuntimeKindSet && strings.TrimSpace(opts.RuntimeKind) != "" {
		args = append(args, "--runtime", opts.RuntimeKind)
	}
	if opts.RuntimeBinSet && strings.TrimSpace(opts.RuntimeBin) != "" {
		args = append(args, "--runtime-bin", opts.RuntimeBin)
	}
	if opts.Force {
		args = append(args, "--force")
	}
	return args
}

func renderJobApproveApplyCommand(w io.Writer, hasAction bool, opts jobApproveApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(jobApproveApplyCommandArgs(opts)), " "))
	return err
}

func jobApproveApplyCommandArgs(opts jobApproveApplyCommandOptions) []string {
	args := []string{"agent-team", "job", "approve", opts.JobID}
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.Advance {
		args = append(args, "--advance")
	}
	if opts.WorkspaceSet && strings.TrimSpace(opts.Workspace) != "" {
		args = append(args, "--workspace", opts.Workspace)
	}
	if opts.RuntimeKindSet && strings.TrimSpace(opts.RuntimeKind) != "" {
		args = append(args, "--runtime", opts.RuntimeKind)
	}
	if opts.RuntimeBinSet && strings.TrimSpace(opts.RuntimeBin) != "" {
		args = append(args, "--runtime-bin", opts.RuntimeBin)
	}
	if opts.StepSet && strings.TrimSpace(opts.Step) != "" {
		args = append(args, "--step", opts.Step)
	}
	if opts.MessageSet {
		args = append(args, "--message", opts.Message)
	}
	if opts.MessageFileSet && strings.TrimSpace(opts.MessageFile) != "" {
		args = append(args, "--message-file", opts.MessageFile)
	}
	if !opts.MessageSet && !opts.MessageFileSet && len(opts.PositionalMessage) > 0 {
		args = append(args, opts.PositionalMessage...)
	}
	return args
}

type jobRejectApplyCommandOptions struct {
	JobID             string
	Repo              string
	RepoSet           bool
	Step              string
	StepSet           bool
	Message           string
	MessageSet        bool
	MessageFile       string
	MessageFileSet    bool
	PositionalMessage []string
}

func renderJobRejectApplyCommand(w io.Writer, hasAction bool, opts jobRejectApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(jobRejectApplyCommandArgs(opts)), " "))
	return err
}

func jobRejectApplyCommandArgs(opts jobRejectApplyCommandOptions) []string {
	args := []string{"agent-team", "job", "reject", opts.JobID}
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.StepSet && strings.TrimSpace(opts.Step) != "" {
		args = append(args, "--step", opts.Step)
	}
	if opts.MessageSet {
		args = append(args, "--message", opts.Message)
	}
	if opts.MessageFileSet && strings.TrimSpace(opts.MessageFile) != "" {
		args = append(args, "--message-file", opts.MessageFile)
	}
	if !opts.MessageSet && !opts.MessageFileSet && len(opts.PositionalMessage) > 0 {
		args = append(args, opts.PositionalMessage...)
	}
	return args
}

type jobReopenPreview struct {
	Job    *job.Job `json:"job"`
	DryRun bool     `json:"dry_run"`
}

func jobActionMessage(flag string, args []string, fallback string) string {
	if msg := strings.TrimSpace(flag); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(strings.Join(args, " ")); msg != "" {
		return msg
	}
	return fallback
}

func jobActionMessageWithFile(flag string, file string, args []string, fallback string) (string, error) {
	msg, err := optionalSendMessageBody(flag, file, args)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(msg) == "" {
		return fallback, nil
	}
	return msg, nil
}

type jobActionPreview struct {
	Job    *job.Job `json:"job"`
	DryRun bool     `json:"dry_run"`
}

func renderJobActionPreview(w io.Writer, j *job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(jobActionPreview{Job: j, DryRun: true})
	}
	if tmpl != nil {
		return renderJobTemplate(w, j, tmpl)
	}
	fmt.Fprintln(w, "Dry run: true")
	renderJobDetail(w, j)
	return nil
}

func renderJobReopenPreview(w io.Writer, j *job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(jobReopenPreview{Job: j, DryRun: true})
	}
	if tmpl != nil {
		return renderJobTemplate(w, j, tmpl)
	}
	fmt.Fprintln(w, "Dry run: true")
	renderJobDetail(w, j)
	return nil
}

type jobStepPreview struct {
	Job    *job.Job `json:"job"`
	DryRun bool     `json:"dry_run"`
}

type jobStepTemplateContext struct {
	*job.Job
	Step   *job.Step `json:"step,omitempty"`
	DryRun bool      `json:"dry_run,omitempty"`
}

func renderJobStepTemplate(w io.Writer, j *job.Job, stepID string, dryRun bool, tmpl *template.Template) error {
	if tmpl == nil {
		return nil
	}
	ctx := jobStepTemplateContext{
		Job:    j,
		Step:   jobStepForTemplate(j, stepID),
		DryRun: dryRun,
	}
	if err := tmpl.Execute(w, ctx); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func jobStepForTemplate(j *job.Job, stepID string) *job.Step {
	if j == nil {
		return nil
	}
	idx := jobStepIndex(j, stepID)
	if idx == -1 {
		return nil
	}
	return &j.Steps[idx]
}

func renderJobStepPreview(w io.Writer, j *job.Job, stepID string, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(jobStepPreview{Job: j, DryRun: true})
	}
	if tmpl != nil {
		return renderJobStepTemplate(w, j, stepID, true, tmpl)
	}
	fmt.Fprintln(w, "Dry run: true")
	renderJobDetail(w, j)
	return nil
}

type jobUpdatePreview struct {
	Job     *job.Job          `json:"job"`
	Changed map[string]string `json:"changed,omitempty"`
	DryRun  bool              `json:"dry_run"`
}

func renderJobUpdatePreview(w io.Writer, j *job.Job, changed map[string]string, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(jobUpdatePreview{Job: j, Changed: changed, DryRun: true})
	}
	if tmpl != nil {
		return renderJobTemplate(w, j, tmpl)
	}
	fmt.Fprintln(w, "Dry run: true")
	if len(changed) > 0 {
		fmt.Fprintf(w, "Changed: %s\n", jobUpdateFieldList(changed))
	}
	renderJobDetail(w, j)
	return nil
}

func parseJobShowEventsTail(raw string) (int, error) {
	tail, err := parseLogTail(raw)
	if err != nil {
		return 0, fmt.Errorf("--events must be >= 0 or \"all\"")
	}
	return tail, nil
}

type jobShowResult struct {
	Job    *job.Job    `json:"job"`
	Events []job.Event `json:"events"`
}

func renderJobShowResult(w io.Writer, teamDir string, j *job.Job, jsonOut bool, tmpl *template.Template, includeEvents bool, eventTail int, commandsOut bool) error {
	if jsonOut || tmpl != nil {
		if jsonOut && includeEvents {
			events, err := job.ListEvents(teamDir, j.ID)
			if err != nil {
				return err
			}
			return json.NewEncoder(w).Encode(jobShowResult{
				Job:    j,
				Events: job.TailEvents(events, eventTail),
			})
		}
		return renderJobResult(w, j, jsonOut, tmpl)
	}
	queueItems, err := queueItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	quarantineItems, err := collectJobQueueQuarantineItems(teamDir, j, queueListFilters{})
	if err != nil {
		return err
	}
	outboxItems, err := outboxItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	outboxQuarantineItems, err := collectJobOutboxQuarantineItems(teamDir, j, outboxListFilters{})
	if err != nil {
		return err
	}
	statusPreviews, err := statusPreviewsForJob(teamDir, j)
	if err != nil {
		return err
	}
	if commandsOut {
		actions := jobDetailActions(j, teamDir, queueItems, outboxItems, statusPreviews, quarantineItems, outboxQuarantineItems, time.Now().UTC())
		return renderActionCommands(w, commandActionsOnly(actions))
	}
	renderJobDetailWithRuntime(w, teamDir, j, queueItems, outboxItems, statusPreviews, quarantineItems, outboxQuarantineItems)
	if includeEvents {
		events, err := job.ListEvents(teamDir, j.ID)
		if err != nil {
			return err
		}
		renderJobRecentEvents(w, job.TailEvents(events, eventTail))
	}
	return nil
}

func renderJobTemplate(w io.Writer, j *job.Job, tmpl *template.Template) error {
	if err := tmpl.Execute(w, j); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderJobTable(w io.Writer, jobs []*job.Job) {
	if len(jobs) == 0 {
		fmt.Fprintln(w, "(no jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tHELD\tHOLD_UNTIL\tTARGET\tINSTANCE\tPIPELINE\tTICKET\tUPDATED")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Status, yesNo(j.Held), emptyDash(jobHoldUntilText(j)), j.Target, emptyDash(j.Instance), emptyDash(j.Pipeline), j.Ticket, j.UpdatedAt.Format(time.RFC3339))
	}
	_ = tw.Flush()
}

func renderJobTableWithRuntime(w io.Writer, jobs []*job.Job, runtimeByInstance map[string]string) {
	if len(jobs) == 0 {
		fmt.Fprintln(w, "(no jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tHELD\tHOLD_UNTIL\tTARGET\tINSTANCE\tRUNTIME\tPIPELINE\tTICKET\tUPDATED")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Status, yesNo(j.Held), emptyDash(jobHoldUntilText(j)), j.Target, emptyDash(j.Instance), jobRuntimeLabel(j, runtimeByInstance), emptyDash(j.Pipeline), j.Ticket, j.UpdatedAt.Format(time.RFC3339))
	}
	_ = tw.Flush()
}

func jobRuntimeLabel(j *job.Job, runtimeByInstance map[string]string) string {
	if j == nil || len(runtimeByInstance) == 0 {
		return "-"
	}
	runtimes := map[string]bool{}
	add := func(instance string) {
		instance = strings.TrimSpace(instance)
		if instance == "" {
			return
		}
		runtime := strings.TrimSpace(runtimeByInstance[instance])
		if runtime == "" {
			return
		}
		runtimes[runtime] = true
	}
	add(j.Instance)
	for _, step := range j.Steps {
		add(step.Instance)
	}
	if len(runtimes) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(runtimes))
	for runtime := range runtimes {
		keys = append(keys, runtime)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func renderJobQueueReconcileResults(w io.Writer, results []jobQueueReconcileResult, jsonOut bool, tmpl *template.Template) error {
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
	if len(results) == 0 {
		fmt.Fprintln(w, "(no queue-backed jobs reconciled)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tQUEUE\tSTATE\tBEFORE\tAFTER\tINSTANCE\tACTION\tMESSAGE")
	for _, result := range results {
		action := "unchanged"
		if result.Changed {
			action = "updated"
			if result.DryRun {
				action = "would_update"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID, result.QueueID, result.QueueState, result.Before, result.After, emptyDash(result.Instance), action, emptyDash(result.Message))
	}
	return tw.Flush()
}

func renderJobEventReconcileResults(w io.Writer, results []jobEventReconcileResult, jsonOut bool, tmpl *template.Template) error {
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
	if len(results) == 0 {
		fmt.Fprintln(w, "(no event-backed jobs reconciled)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tINSTANCE\tLIFECYCLE\tEVENT\tBEFORE\tAFTER\tMATCH\tACTION\tMESSAGE")
	for _, result := range results {
		action := "unchanged"
		if result.Changed {
			action = "updated"
			if result.DryRun {
				action = "would_update"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID, result.Instance, result.Lifecycle, result.Event, result.Before, result.After, result.MatchedBy, action, emptyDash(result.Message))
	}
	return tw.Flush()
}

func renderJobStatusReconcileResults(w io.Writer, results []jobStatusReconcileResult, jsonOut bool, tmpl *template.Template) error {
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
	if len(results) == 0 {
		fmt.Fprintln(w, "(no status-backed jobs reconciled)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tINSTANCE\tPHASE\tBEFORE\tAFTER\tMATCH\tACTION\tMESSAGE")
	for _, result := range results {
		action := "unchanged"
		if result.Changed {
			action = "updated"
			if result.DryRun {
				action = "would_update"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID, result.Instance, result.Phase, result.Before, result.After, result.MatchedBy, action, emptyDash(result.Message))
	}
	return tw.Flush()
}

func renderJobDetail(w io.Writer, j *job.Job) {
	renderJobDetailWithRuntime(w, "", j, nil, nil, nil, nil, nil)
}

func renderJobDetailWithQueue(w io.Writer, j *job.Job, queueItems []*daemon.QueueItem) {
	renderJobDetailWithRuntime(w, "", j, queueItems, nil, nil, nil, nil)
}

func renderJobRecentEvents(w io.Writer, events []job.Event) {
	fmt.Fprintln(w, "Recent Events:")
	renderJobEventTable(w, events, true)
}

func renderJobDetailWithRuntime(w io.Writer, teamDir string, j *job.Job, queueItems []*daemon.QueueItem, outboxItems []*daemon.OutboxItem, statusPreviews []jobStatusReconcileResult, quarantineItems []queueQuarantineItem, outboxQuarantineItems []outboxQuarantineItem) {
	actions := jobDetailActions(j, teamDir, queueItems, outboxItems, statusPreviews, quarantineItems, outboxQuarantineItems, time.Now().UTC())
	runtimeMeta := jobRuntimeMetadataForDetail(teamDir, j)
	fmt.Fprintf(w, "ID:          %s\n", j.ID)
	fmt.Fprintf(w, "Status:      %s\n", j.Status)
	if j.Held {
		fmt.Fprintln(w, "Held:        yes")
		if strings.TrimSpace(j.HoldReason) != "" {
			fmt.Fprintf(w, "Hold Reason: %s\n", j.HoldReason)
		}
		if until := jobHoldUntilText(j); until != "" {
			fmt.Fprintf(w, "Hold Until:  %s\n", until)
		}
	}
	fmt.Fprintf(w, "Ticket:      %s\n", j.Ticket)
	if j.TicketURL != "" {
		fmt.Fprintf(w, "Ticket URL:  %s\n", j.TicketURL)
	}
	fmt.Fprintf(w, "Target:      %s\n", j.Target)
	if j.Instance != "" {
		fmt.Fprintf(w, "Instance:    %s\n", j.Instance)
		if meta := runtimeMeta[j.Instance]; meta != nil {
			if runtime := metadataRuntimeKey(meta); runtime != "unknown" {
				fmt.Fprintf(w, "Runtime:     %s\n", runtime)
			}
			if strings.TrimSpace(meta.RuntimeBinary) != "" {
				fmt.Fprintf(w, "Runtime Bin: %s\n", meta.RuntimeBinary)
			}
		}
	}
	if j.Pipeline != "" {
		fmt.Fprintf(w, "Pipeline:    %s\n", j.Pipeline)
	}
	if j.Branch != "" {
		fmt.Fprintf(w, "Branch:      %s\n", j.Branch)
	}
	if j.Worktree != "" {
		fmt.Fprintf(w, "Worktree:    %s\n", j.Worktree)
	}
	if j.PR != "" {
		fmt.Fprintf(w, "PR:          %s\n", j.PR)
	}
	if j.LastEvent != "" {
		fmt.Fprintf(w, "Last Event:  %s\n", j.LastEvent)
	}
	if j.LastStatus != "" {
		fmt.Fprintf(w, "Last Status: %s\n", j.LastStatus)
	}
	if j.Kickoff != "" {
		fmt.Fprintf(w, "Kickoff:     %s\n", j.Kickoff)
	}
	if len(j.Steps) > 0 {
		fmt.Fprintln(w, "Steps:")
		for _, step := range j.Steps {
			instance := step.Instance
			if instance == "" {
				instance = "-"
			}
			after := "-"
			if len(step.After) > 0 {
				after = strings.Join(step.After, ",")
			}
			parts := []string{
				"target=" + step.Target,
				"status=" + string(step.Status),
				"instance=" + instance,
				"after=" + after,
			}
			if strings.TrimSpace(step.Workspace) != "" {
				parts = append(parts, "workspace="+strings.TrimSpace(step.Workspace))
			}
			if runtime := formatStepRuntime(step.Runtime, step.RuntimeBin); runtime != "" {
				parts = append(parts, "runtime_default="+runtime)
			}
			if strings.TrimSpace(step.Label) != "" {
				parts = append(parts, fmt.Sprintf("label=%q", strings.TrimSpace(step.Label)))
			}
			if strings.TrimSpace(step.Description) != "" {
				parts = append(parts, fmt.Sprintf("description=%q", strings.TrimSpace(step.Description)))
			}
			if strings.TrimSpace(step.Instructions) != "" {
				parts = append(parts, fmt.Sprintf("instructions=%q", strings.TrimSpace(step.Instructions)))
			}
			if step.Gate != "" {
				parts = append(parts, "gate="+step.Gate)
			}
			if strings.TrimSpace(step.Timeout) != "" {
				parts = append(parts, "timeout="+strings.TrimSpace(step.Timeout))
			}
			if attempts := formatJobStepAttempts(step.Attempts, step.MaxAttempts); attempts != "-" {
				parts = append(parts, "attempts="+attempts)
			}
			if step.Skipped {
				parts = append(parts, "skipped=true")
				if strings.TrimSpace(step.SkipReason) != "" {
					parts = append(parts, "skip_reason="+step.SkipReason)
				}
			}
			if meta := runtimeMeta[step.Instance]; meta != nil {
				if runtime := metadataRuntimeKey(meta); runtime != "unknown" {
					parts = append(parts, "runtime="+runtime)
				}
				if strings.TrimSpace(meta.RuntimeBinary) != "" {
					parts = append(parts, "runtime_bin="+meta.RuntimeBinary)
				}
			}
			fmt.Fprintf(w, "  %s  %s\n", step.ID, strings.Join(parts, " "))
		}
	}
	if len(queueItems) > 0 {
		fmt.Fprintln(w, "Queue:")
		for _, item := range queueItems {
			fmt.Fprintf(w, "  %s  state=%s instance=%s instance_id=%s attempts=%d next_retry=%s\n",
				item.ID, item.State, item.Instance, item.InstanceID, item.Attempts, queueTime(item.NextRetry))
		}
	}
	if len(quarantineItems) > 0 {
		fmt.Fprintln(w, "Queue Quarantine:")
		for _, item := range quarantineItems {
			fmt.Fprintf(w, "  %s  state=%s id=%s instance=%s instance_id=%s restorable=%s problem=%s\n",
				item.Path,
				emptyDash(item.State),
				emptyDash(item.ID),
				emptyDash(item.Instance),
				emptyDash(item.InstanceID),
				queueQuarantineRestorableText(item.Restorable),
				emptyDash(item.Problem))
		}
	}
	if len(outboxItems) > 0 {
		fmt.Fprintln(w, "Outbox:")
		for _, item := range outboxItems {
			if item == nil {
				continue
			}
			fmt.Fprintf(w, "  %s  state=%s type=%s source=%s job=%s updated=%s error=%s\n",
				item.ID,
				item.State,
				item.Type,
				emptyDash(item.Source),
				emptyDash(outboxItemJob(item)),
				outboxTime(item.UpdatedAt),
				emptyDash(item.LastError))
		}
	}
	if len(outboxQuarantineItems) > 0 {
		fmt.Fprintln(w, "Outbox Quarantine:")
		for _, item := range outboxQuarantineItems {
			fmt.Fprintf(w, "  %s  state=%s id=%s type=%s source=%s restorable=%s problem=%s\n",
				item.Path,
				emptyDash(item.State),
				emptyDash(item.ID),
				emptyDash(item.Type),
				emptyDash(item.Source),
				yesNo(item.Restorable),
				emptyDash(item.Problem))
		}
	}
	if len(statusPreviews) > 0 {
		fmt.Fprintln(w, "Status Preview:")
		for _, preview := range statusPreviews {
			action := "unchanged"
			if preview.Changed {
				action = "would_update"
			}
			fmt.Fprintf(w, "  %s  phase=%s before=%s after=%s action=%s message=%s\n",
				preview.Instance, preview.Phase, preview.Before, preview.After, action, emptyDash(preview.Message))
		}
	}
	if len(actions) > 0 {
		fmt.Fprintln(w, "Actions:")
		for _, action := range actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	fmt.Fprintf(w, "Created:     %s\n", j.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Updated:     %s\n", j.UpdatedAt.Format(time.RFC3339))
}

func jobRuntimeMetadataForDetail(teamDir string, j *job.Job) map[string]*daemon.Metadata {
	if strings.TrimSpace(teamDir) == "" || j == nil {
		return nil
	}
	names := map[string]bool{}
	if strings.TrimSpace(j.Instance) != "" {
		names[j.Instance] = true
	}
	for _, step := range j.Steps {
		if strings.TrimSpace(step.Instance) != "" {
			names[step.Instance] = true
		}
	}
	jobID := job.NormalizeID(j.ID)
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil
	}
	out := map[string]*daemon.Metadata{}
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		matchesInstance := names[meta.Instance]
		matchesJob := jobID != "" && job.NormalizeID(meta.Job) == jobID
		if matchesInstance || matchesJob {
			out[meta.Instance] = meta
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func jobDetailActions(j *job.Job, teamDir string, queueItems []*daemon.QueueItem, outboxItems []*daemon.OutboxItem, statusPreviews []jobStatusReconcileResult, quarantineItems []queueQuarantineItem, outboxQuarantineItems []outboxQuarantineItem, now time.Time) []string {
	if j == nil {
		return nil
	}
	var actions []string
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" {
			return
		}
		actions = appendStringOnce(actions, action)
	}
	stats := jobTriageQueueStats{IDs: make([]string, 0, len(queueItems))}
	for _, item := range queueItems {
		if item == nil {
			continue
		}
		stats.IDs = append(stats.IDs, item.ID)
		switch item.State {
		case daemon.QueueStatePending:
			stats.Pending++
			if !item.NextRetry.IsZero() && item.NextRetry.After(now) {
				stats.Delayed++
			}
		case daemon.QueueStateDead:
			stats.Dead++
		}
	}
	for _, item := range quarantineItems {
		addQueueQuarantineItemToStats(&stats, item)
	}
	sort.Strings(stats.IDs)
	sort.Strings(stats.QuarantinePaths)
	sort.Strings(stats.QuarantineRestorablePaths)
	var outboxQuarantineStats jobTriageOutboxQuarantineStats
	for _, item := range outboxQuarantineItems {
		addOutboxQuarantineItemToTriageStats(&outboxQuarantineStats, item)
	}
	sort.Strings(outboxQuarantineStats.QuarantinePaths)
	sort.Strings(outboxQuarantineStats.QuarantineRestorablePaths)
	if triage, ok := triageJob(j, inspectNextJobStep(j), stats, outboxQuarantineStats, now, 0); ok {
		for _, action := range triage.Actions {
			add(action)
		}
	}
	for _, item := range outboxItems {
		for _, action := range jobOutboxItemActions(j.ID, item) {
			add(action)
		}
	}
	if strings.TrimSpace(teamDir) != "" && strings.TrimSpace(j.Instance) != "" {
		if st, err := os.Stat(lastMessagePathForInstance(teamDir, j.Instance)); err == nil && !st.IsDir() {
			add(fmt.Sprintf("agent-team job logs %s --last-message", j.ID))
		}
	}
	if jobHasCrashedRuntimeMetadata(teamDir, j) {
		add(fmt.Sprintf("agent-team job resume-plan %s --status crashed", j.ID))
	}
	for _, preview := range statusPreviews {
		if preview.Changed && preview.After == job.StatusBlocked {
			add(jobUnblockAction(j.ID, ""))
		}
	}
	if j.Held {
		add(fmt.Sprintf("agent-team job release %s", j.ID))
	} else if len(j.Steps) > 0 {
		add(fmt.Sprintf("agent-team job explain %s", j.ID))
		for _, action := range actionsForJobReadyRow(jobReadyRowFromJob(j, inspectNextJobStep(j))) {
			add(action)
		}
	} else if j.Status == job.StatusQueued {
		add(fmt.Sprintf("agent-team job dispatch %s", j.ID))
	}
	return actions
}

func jobOutboxItemActions(jobID string, item *daemon.OutboxItem) []string {
	if item == nil {
		return nil
	}
	id := job.NormalizeID(jobID)
	if id == "" {
		id = normalizeOutboxJob(outboxItemJob(item))
	}
	if id == "" {
		return nil
	}
	switch item.State {
	case daemon.OutboxStateFailed:
		return []string{
			fmt.Sprintf("agent-team job outbox retry %s %s", id, item.ID),
			fmt.Sprintf("agent-team job outbox drop %s %s --dry-run", id, item.ID),
		}
	case daemon.OutboxStatePending:
		return []string{
			fmt.Sprintf("agent-team job outbox %s --state pending", id),
			"agent-team outbox drain --dry-run",
		}
	default:
		return nil
	}
}

func jobUnblockAction(jobID, stepID string) string {
	jobID = strings.TrimSpace(jobID)
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return fmt.Sprintf("agent-team job unblock %s <answer...>", jobID)
	}
	return fmt.Sprintf("agent-team job unblock %s --step %s <answer...>", jobID, stepID)
}

func jobHasCrashedRuntimeMetadata(teamDir string, j *job.Job) bool {
	for _, meta := range jobRuntimeMetadataForDetail(teamDir, j) {
		if meta != nil && meta.Status == daemon.StatusCrashed {
			return true
		}
	}
	return false
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func listDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	var cleaned []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return "-"
	}
	return strings.Join(cleaned, ",")
}
