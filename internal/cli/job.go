package cli

import (
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
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage durable work units.",
		Long: "Manage durable work units backed by `.agent_team/jobs/<job-id>.toml`. " +
			"Jobs track ticket ownership, target agent, lifecycle state, instance, branch, worktree, and PR metadata.",
	}
	cmd.AddCommand(newJobCreateCmd())
	cmd.AddCommand(newJobLsCmd())
	cmd.AddCommand(newJobShowCmd())
	cmd.AddCommand(newJobQueueCmd())
	cmd.AddCommand(newJobEventsCmd())
	cmd.AddCommand(newJobWaitCmd())
	cmd.AddCommand(newJobStartCmd())
	cmd.AddCommand(newJobDispatchCmd())
	cmd.AddCommand(newJobSendCmd())
	cmd.AddCommand(newJobUnblockCmd())
	cmd.AddCommand(newJobLogsCmd())
	cmd.AddCommand(newJobAttachCmd())
	cmd.AddCommand(newJobStopCmd())
	cmd.AddCommand(newJobKillCmd())
	cmd.AddCommand(newJobCloseCmd())
	cmd.AddCommand(newJobUpdateCmd())
	cmd.AddCommand(newJobReopenCmd())
	cmd.AddCommand(newJobCleanupCmd())
	cmd.AddCommand(newJobRmCmd())
	cmd.AddCommand(newJobPruneCmd())
	cmd.AddCommand(newJobNextCmd())
	cmd.AddCommand(newJobReadyCmd())
	cmd.AddCommand(newJobTriageCmd())
	cmd.AddCommand(newJobStepCmd())
	cmd.AddCommand(newJobAdvanceCmd())
	cmd.AddCommand(newJobReconcileCmd())
	return cmd
}

func newJobQueueCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		eventTypes  []string
		readyOnly   bool
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
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, nil, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return runJobQueueList(cmd.OutOrStdout(), teamDir, j, filters, summary, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only show pending queue items whose next retry is due now.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate queue counts instead of queue rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.AddCommand(newJobQueueQuarantineCmd())
	cmd.AddCommand(newJobQueueRetryCmd())
	cmd.AddCommand(newJobQueueDropCmd())
	cmd.AddCommand(newJobQueuePruneCmd())
	return cmd
}

func newJobQueueQuarantineCmd() *cobra.Command {
	var (
		repo         string
		stateFilter  string
		eventTypes   []string
		restorable   bool
		unrestorable bool
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
			return renderQueueQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined files that cannot be restored.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit quarantined queue files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	cmd.AddCommand(newJobQueueQuarantineShowCmd())
	cmd.AddCommand(newJobQueueQuarantineRestoreCmd())
	cmd.AddCommand(newJobQueueQuarantineDropCmd())
	return cmd
}

func newJobQueueQuarantineShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <job-id> <quarantine-path>",
		Short: "Show one job-owned quarantined queue file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return renderQueueQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the quarantined queue file as JSON.")
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
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <job-id> [quarantine-path]",
		Short: "Restore job-owned quarantined queue files.",
		Long:  "Restore one job-owned quarantined queue file by path, or restore a filtered batch of job-owned restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team job queue quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, nil, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
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
				items, err := collectJobQueueQuarantineItems(teamDir, j, filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				items = filterQueueQuarantineRestorable(items, true, false)
				results, err := restoreQueueQuarantineItems(teamDir, items, dryRun, force)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				return renderQueueQuarantineRestoreMany(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: requires <job-id> and one path unless --all is set.")
				return exitErr(2)
			}
			if !filters.empty() {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine restore: filters require --all.")
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
			return renderQueueQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching job-owned restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active queue file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
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
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <job-id> [quarantine-path]",
		Short: "Drop job-owned quarantined queue files after inspection.",
		Long:  "Drop one job-owned quarantined queue file by path, or drop a filtered job-owned batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: --older-than must be >= 0.")
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
				items, err := collectJobQueueQuarantineItems(teamDir, j, filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
				results, err := dropQueueQuarantineItems(teamDir, items, dryRun, olderThan, unrestorable, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				return renderQueueQuarantineDrop(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: requires <job-id> and one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !filters.empty() {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue quarantine drop: filters require --all.")
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
			return renderQueueQuarantineDrop(cmd.OutOrStdout(), []queueQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching job-owned quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
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
		stateFilter string
		eventTypes  []string
		readyOnly   bool
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <job-id> [id]",
		Short: "Retry queue items owned by one job.",
		Long:  "Retry one job-owned queue item by id, or retry a filtered job-owned batch with --all. Batch retries default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFilters(effectiveState, nil, eventTypes, nil, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue retry: %v\n", err)
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				return runJobQueueRetryAll(cmd.OutOrStdout(), teamDir, j, filters, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: requires <job-id> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || readyOnly || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue retry: --state, --event-type, --ready, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return runJobQueueRetryOne(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, j, args[1], dryRun, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching job-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching job-owned queue items without retrying them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only retry pending queue items whose next retry is due now.")
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
		stateFilter string
		eventTypes  []string
		readyOnly   bool
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <job-id> [id]",
		Short: "Drop queue items owned by one job.",
		Long:  "Drop one job-owned queue item by id, or drop a filtered job-owned batch with --all. Batch drops default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFilters(effectiveState, nil, eventTypes, nil, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue drop: %v\n", err)
					return exitErr(2)
				}
				teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
				if err != nil {
					return err
				}
				return runJobQueueDropAll(cmd.OutOrStdout(), teamDir, j, filters, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: requires <job-id> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || readyOnly || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue drop: --state, --event-type, --ready, and --limit require --all.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return runJobQueueDropOne(cmd.OutOrStdout(), cmd.ErrOrStderr(), teamDir, j, args[1], dryRun, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching job-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching job-owned queue items without dropping them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only drop pending queue items whose next retry is due now.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newJobQueuePruneCmd() *cobra.Command {
	var (
		repo      string
		stateFlag string
		olderThan time.Duration
		dryRun    bool
		jsonOut   bool
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune <job-id>",
		Short: "Prune queue items owned by one job.",
		Long:  "Prune queue items owned by one durable job. By default this removes dead-letter items.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job queue prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueuePruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseQueuePruneState(stateFlag)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job queue prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return runJobQueuePrune(cmd.OutOrStdout(), teamDir, j, state, olderThan, time.Now().UTC(), dryRun, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&stateFlag, "state", daemon.QueueStateDead, "Queue state to prune: dead, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune job-owned items older than this duration based on retry/dead-letter/update time.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job-owned queue items that would be pruned without dropping them.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
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
		repo        string
		targetAgent string
		pipeline    string
		id          string
		ticketURL   string
		kickoff     string
		kickoffFile string
		instance    string
		dispatchNow bool
		workspace   string
		dryRun      bool
		jsonOut     bool
		format      string
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
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
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
						preview, err := previewJobAdvanceDispatch(teamDir, j, workspace)
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
					res, err := advanceJob(cmd, teamDir, j, workspace)
					if err != nil {
						return err
					}
					if jsonOut {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
					}
					if tmpl != nil {
						return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
					}
					return renderJobAdvanceResult(cmd.OutOrStdout(), res)
				}
				res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, "", workspace, "agent-team job create")
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
				}
				if tmpl != nil {
					return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
				}
				renderDispatchOutcome(cmd.OutOrStdout(), res.Job.Target, requestedName, res.Event)
				fmt.Fprintf(cmd.OutOrStdout(), "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
				return nil
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&targetAgent, "target", "worker", "Target agent that should own this job.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Create this job from a declared pipeline in instances.toml.")
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Canonical ticket URL to store on the job.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the target agent.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name that owns the job (default set during dispatch).")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the created job immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the job that would be created without writing it.")
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
			ID:     step.ID,
			Target: step.Target,
			Status: status,
			After:  append([]string(nil), step.After...),
			Gate:   step.Gate,
		})
	}
	return steps
}

func newJobLsCmd() *cobra.Command {
	var (
		repo         string
		statusFilter string
		targetFilter string
		instance     string
		pipeline     string
		ticket       string
		branch       string
		pr           string
		watch        bool
		noClear      bool
		summary      bool
		jsonOut      bool
		format       string
		sortBy       string
		interval     time.Duration
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
			filters, err := newJobListFilters(statusFilter, targetFilter, instance, pipeline, ticket, branch, pr)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
				return exitErr(2)
			}
			filters.Sort = sortMode
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&statusFilter, "status", "", "Filter by status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&targetFilter, "target-agent", "", "Filter by target agent.")
	cmd.Flags().StringVar(&instance, "instance", "", "Filter by owning instance.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Filter by pipeline name.")
	cmd.Flags().StringVar(&ticket, "ticket", "", "Filter by ticket id or URL substring.")
	cmd.Flags().StringVar(&branch, "branch", "", "Filter by branch.")
	cmd.Flags().StringVar(&pr, "pr", "", "Filter by PR URL or number substring.")
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
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show one durable job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			includeEvents := cmd.Flags().Changed("events")
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job show: --format cannot be combined with --json.")
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
			return renderJobShowResult(cmd.OutOrStdout(), teamDir, j, jsonOut, tmpl, includeEvents, eventTail)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&eventsTail, "events", "5", "Include the last N job events in the detail output, or all.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobWaitCmd() *cobra.Command {
	var (
		repo         string
		statuses     []string
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
		Short: "Wait for a job to reach a lifecycle status.",
		Long: "Wait for a durable job to reach one of the requested lifecycle statuses. " +
			"By default this waits for a terminal status: done or failed.",
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
			waitStatuses, err := parseJobWaitStatuses(statuses, !cmd.Flags().Changed("status"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: %v\n", err)
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
			j, err := runJobWait(ctx, teamDir, args[0], waitStatuses, interval)
			if err != nil {
				if timeoutErr, ok := err.(*jobWaitTimeoutError); ok {
					if !quiet {
						status := "unknown"
						if timeoutErr.Job != nil {
							status = string(timeoutErr.Job.Status)
						}
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: timed out waiting for %s to reach %s (current=%s).\n",
							job.NormalizeID(args[0]), jobWaitStatusList(waitStatuses), status)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
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
			return runJobInstanceUp(cmd, repo, args[0], instanceUpOptions{
				Wait:    wait,
				Timeout: timeout,
				DryRun:  dryRun,
				Quiet:   quiet,
				JSON:    jsonOut,
				Format:  formatTemplate,
			}, readyTimeout)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
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
		repo      string
		source    string
		workspace string
		dryRun    bool
		jsonOut   bool
		format    string
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
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
				return exitErr(2)
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
				preview, err := previewDispatchPayload(teamDir, j.Target, requestedName, payload)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
					return exitErr(1)
				}
				return renderJobDispatchPreview(cmd.OutOrStdout(), j, preview, jsonOut, tmpl)
			}
			res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, source, workspace, "agent-team job dispatch")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return json.NewEncoder(out).Encode(res)
			}
			if tmpl != nil {
				return renderJobTemplate(out, res.Job, tmpl)
			}
			renderDispatchOutcome(out, res.Job.Target, requestedName, res.Event)
			fmt.Fprintf(out, "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&source, "source", "", "Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for spawned children: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview topology matches without publishing to the daemon or updating the job.")
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
		allowMissing bool
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
			if strings.TrimSpace(j.Instance) == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job send: job %q has no owning instance; dispatch it first.\n", j.ID)
				return exitErr(2)
			}
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			if err := runSendWithClient(io.Discard, cmd.ErrOrStderr(), client, j.Instance, body, sendOptions{
				From:         from,
				AllowMissing: allowMissing,
			}); err != nil {
				return err
			}
			j.LastEvent = "message_sent"
			j.LastStatus = strings.TrimSpace(body)
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"from": from}); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  sent   %-20s job=%s\n", j.Instance, j.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&allowMissing, "allow-missing", false, "Allow queueing a message for an instance the daemon does not know yet.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.LastEvent}}'.")
	return cmd
}

func newJobUnblockCmd() *cobra.Command {
	var (
		repo         string
		from         string
		message      string
		messageFile  string
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
			if j.Status != job.StatusBlocked && !hasStatusBlock && !force {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job unblock: job %q is %s; pass --force to unblock anyway.\n", j.ID, j.Status)
				return exitErr(2)
			}
			if hasStatusBlock {
				applyStatusPreviewOwnership(j, statusPreview)
			}
			if strings.TrimSpace(j.Instance) == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job unblock: job %q has no owning instance; use `agent-team job retry %s --dispatch` to start a new attempt.\n", j.ID, j.ID)
				return exitErr(2)
			}
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			fromLabel := normalizedJobUnblockSender(from)
			if err := runSendWithClient(io.Discard, cmd.ErrOrStderr(), client, j.Instance, body, sendOptions{
				From:         fromLabel,
				AllowMissing: allowMissing,
				DryRun:       dryRun,
			}); err != nil {
				return err
			}
			j.Status = next
			j.LastEvent = "unblocked"
			j.LastStatus = body
			j.UpdatedAt = time.Now().UTC()
			if dryRun {
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(jobUnblockPreview{
						DryRun:        true,
						Job:           j,
						Instance:      j.Instance,
						From:          fromLabel,
						Message:       body,
						StatusPreview: hasStatusBlock,
					})
				}
				if tmpl != nil {
					return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  would-unblock   %-20s job=%s status=%s\n", j.Instance, j.ID, j.Status)
				return nil
			}
			data := map[string]string{
				"from":     fromLabel,
				"instance": j.Instance,
				"status":   string(next),
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
			fmt.Fprintf(cmd.OutOrStdout(), "  unblocked   %-20s job=%s status=%s\n", j.Instance, j.ID, j.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the unblock message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusRunning), "Status after unblocking: running or queued.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Allow unblocking a job not currently marked blocked.")
	cmd.Flags().BoolVar(&allowMissing, "allow-missing", false, "Allow queueing a message for an owning instance the daemon does not know yet.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the unblock without sending a mailbox message or updating the job.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

type jobUnblockPreview struct {
	DryRun        bool     `json:"dry_run"`
	Job           *job.Job `json:"job"`
	Instance      string   `json:"instance"`
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

func newJobLogsCmd() *cobra.Command {
	var (
		repo   string
		follow bool
		tail   string
		since  string
		grep   string
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
			instance := strings.TrimSpace(j.Instance)
			if instance == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: job %q has no owning instance; dispatch it first.\n", j.ID)
				return exitErr(2)
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
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail the owning instance log; print new bytes as they appear.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only print the log if it was modified since a duration ago (for example 10m, 24h) or RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	return cmd
}

func newJobAttachCmd() *cobra.Command {
	var (
		repo     string
		noResume bool
		dryRun   bool
		noFollow bool
		tail     string
		since    string
		grep     string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "attach <job-id>",
		Short: "Attach to a job's owning instance.",
		Long: "Attach to the instance recorded on a durable job. By default this opens " +
			"the owning instance with the normal interactive attach flow. Passing log " +
			"options such as --tail, --no-follow, --since, or --grep follows the daemon-captured log stream instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			instance := strings.TrimSpace(j.Instance)
			if instance == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job attach: job %q has no owning instance; dispatch it first.\n", j.ID)
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
			return runAttach(cmd, repoRoot, instance, noResume, dryRun)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&noResume, "no-resume", false, "Leave the owning instance in stopped state when claude exits.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the owning instance handoff without stopping or resuming the daemon child.")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "Log mode: print the selected log tail and exit instead of following.")
	cmd.Flags().StringVar(&tail, "tail", "50", "Log mode: show only the last N lines before following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Log mode with --no-follow: only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Log mode with --no-follow: only print log lines matching this regular expression.")
	return cmd
}

func newJobStopCmd() *cobra.Command {
	var (
		repo        string
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
			return runJobInstanceDown(cmd, repo, args[0], instanceDownOptions{
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
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
			return runJobInstanceDown(cmd, repo, args[0], instanceDownOptions{
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
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
		repo    string
		status  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "close <job-id>",
		Short: "Close a job as done or failed.",
		Args:  cobra.ExactArgs(1),
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
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			j.Status = job.Status(status)
			j.LastEvent = "closed"
			j.LastStatus = status
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusDone), "Close status: done or failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobUpdateCmd() *cobra.Command {
	var (
		repo      string
		status    string
		target    string
		ticketURL string
		instance  string
		branch    string
		worktree  string
		pr        string
		message   string
		clear     []string
		jsonOut   bool
		format    string
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
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
				return exitErr(2)
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
			if len(changed) == 0 && strings.TrimSpace(message) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: pass at least one update flag.")
				return exitErr(2)
			}
			j.LastEvent = "updated"
			if strings.TrimSpace(message) != "" {
				j.LastStatus = strings.TrimSpace(message)
			} else {
				j.LastStatus = "updated " + jobUpdateFieldList(changed)
			}
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", changed); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", "", "Set lifecycle status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&target, "target", "", "Set target agent.")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Set ticket URL.")
	cmd.Flags().StringVar(&instance, "instance", "", "Set owning instance.")
	cmd.Flags().StringVar(&branch, "branch", "", "Set branch.")
	cmd.Flags().StringVar(&worktree, "worktree", "", "Set worktree path.")
	cmd.Flags().StringVar(&pr, "pr", "", "Set PR URL or number.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().StringSliceVar(&clear, "clear", nil, "Clear metadata fields: ticket-url, instance, branch, worktree, pr, or pipeline. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobReopenCmd() *cobra.Command {
	var (
		repo        string
		status      string
		message     string
		force       bool
		dispatchNow bool
		source      string
		workspace   string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "reopen <job-id>",
		Aliases: []string{"retry"},
		Short:   "Reopen a durable job for another attempt.",
		Long: "Reopen a durable job by resetting its lifecycle status to queued or blocked. " +
			"Running jobs are refused unless --force is set. Pass --dispatch to immediately send the reopened job to its target.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reopen: --format cannot be combined with --json.")
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
						preview, err := previewJobAdvanceDispatch(teamDir, j, workspace)
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
					res, err := advanceJob(cmd, teamDir, j, workspace)
					if err != nil {
						return err
					}
					if jsonOut {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
					}
					if tmpl != nil {
						return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
					}
					return renderJobAdvanceResult(cmd.OutOrStdout(), res)
				}
				res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, source, workspace, "agent-team job reopen")
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
				}
				if tmpl != nil {
					return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
				}
				renderDispatchOutcome(cmd.OutOrStdout(), res.Job.Target, requestedName, res.Event)
				fmt.Fprintf(cmd.OutOrStdout(), "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
				return nil
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusQueued), "Reopened status: queued or blocked.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Allow reopening a job currently marked running.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the reopened job immediately using the running daemon.")
	cmd.Flags().StringVar(&source, "source", "", "Source instance for --dispatch (default: AGENT_TEAM_INSTANCE or cli).")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the reopened job and optional dispatch without writing job or daemon state.")
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
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cleanup <job-id>|--all",
		Short: "Remove a job-owned worker worktree and branch after merge.",
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&all, "all", false, "Clean all done jobs that still own a recorded worktree or branch.")
	cmd.Flags().BoolVar(&merged, "merged", false, "Confirm the job's PR has merged before removing its worktree and branch.")
	cmd.Flags().BoolVar(&forceBranch, "force-branch", false, "With --merged, delete the job branch with git branch -D if it is not locally merged.")
	cmd.Flags().BoolVar(&verifyPR, "verify-pr", false, "Verify the recorded GitHub PR is merged with gh before cleanup.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the job-owned worktree and branch cleanup without removing anything.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Terminal status to prune: done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview removals without deleting files.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit removal results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobNextCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
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
			tmpl, err := parseJobNextFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job next: %v\n", err)
				return exitErr(2)
			}
			j, err := readJobFromRepo(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return renderJobNextResult(cmd.OutOrStdout(), inspectNextJobStep(j), jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the next-step state as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the next-step state with a Go template, e.g. '{{.State}} {{.Step.ID}}'.")
	return cmd
}

func newJobReadyCmd() *cobra.Command {
	var (
		repo     string
		pipeline string
		states   []string
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ready",
		Short: "List pipeline jobs with ready or selected next-step states.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ready: --format cannot be combined with --json.")
				return exitErr(2)
			}
			stateFilter, err := parseJobNextStateFilter(states, !cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ready: %v\n", err)
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
			return runJobReady(cmd.OutOrStdout(), teamDir, strings.TrimSpace(pipeline), stateFilter, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Filter by pipeline name.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit ready rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.")
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
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "triage",
		Short: "Show jobs that need operator attention.",
		Long: "Show a compact work queue triage view from durable jobs, persisted daemon queue items, " +
			"status-file update previews, and ready pipeline steps.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return renderJobTriage(cmd.OutOrStdout(), snapshot, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().DurationVar(&staleAfter, "stale-after", defaultJobTriageStaleAfter, "Flag queued or running jobs with no update after this duration (default: [health].job_stale_after or 24h; 0 disables stale checks).")
	cmd.Flags().StringVar(&minSeverity, "min-severity", "", "Only show attention rows at least this severe: critical, warning, or info.")
	cmd.Flags().StringSliceVar(&reasons, "reason", nil, "Only show attention rows with this reason. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the triage view until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit triage snapshot as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.")
	return cmd
}

func newJobStepCmd() *cobra.Command {
	var (
		repo      string
		status    string
		message   string
		instance  string
		pr        string
		branch    string
		worktree  string
		advance   bool
		skip      bool
		workspace string
		dryRun    bool
		jsonOut   bool
		format    string
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
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
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
				if advance && stepStatus == job.StatusDone {
					preview, err := previewJobAdvanceDispatch(teamDir, j, workspace)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
						return exitErr(1)
					}
					return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, tmpl)
				}
				return renderJobStepPreview(cmd.OutOrStdout(), j, jsonOut, tmpl)
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": args[1]}); err != nil {
				return err
			}
			if advance && stepStatus == job.StatusDone {
				res, err := advanceJob(cmd, teamDir, j, workspace)
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
				}
				if tmpl != nil {
					return renderJobAdvanceResultFormat(cmd.OutOrStdout(), res, tmpl)
				}
				return renderJobAdvanceResult(cmd.OutOrStdout(), res)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
			}
			renderJobDetail(cmd.OutOrStdout(), j)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusDone), "Step status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance that owns or completed this step.")
	cmd.Flags().StringVar(&pr, "pr", "", "PR URL to record on the job.")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch name to record on the job.")
	cmd.Flags().StringVar(&worktree, "worktree", "", "Worktree path to record on the job.")
	cmd.Flags().BoolVar(&advance, "advance", false, "After marking the step done, dispatch the next ready step.")
	cmd.Flags().BoolVar(&skip, "skip", false, "Mark this step as intentionally skipped; stored as done so dependent steps can continue.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for an advanced step: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the step update and optional advance dispatch without writing job or daemon state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or advance result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job or advance result with a Go template, e.g. '{{.ID}} {{.Status}}' or '{{.Job.ID}} {{.Step.ID}}'.")
	return cmd
}

func newJobAdvanceCmd() *cobra.Command {
	var (
		repo      string
		workspace string
		dryRun    bool
		jsonOut   bool
		format    string
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
			tmpl, err := parseJobAdvanceFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if dryRun {
				preview, err := previewJobAdvanceDispatch(teamDir, j, workspace)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
					return exitErr(1)
				}
				return renderJobAdvancePreview(cmd.OutOrStdout(), preview, jsonOut, tmpl)
			}
			res, err := advanceJob(cmd, teamDir, j, workspace)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			if tmpl != nil {
				return renderJobAdvanceResultFormat(cmd.OutOrStdout(), res, tmpl)
			}
			return renderJobAdvanceResult(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for the advanced step: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the next ready step dispatch without changing daemon or job state.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
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
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(2)
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
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Event          *intake.Event        `json:"event"`
					Result         *job.ReconcileResult `json:"result"`
					Cleanup        string               `json:"cleanup,omitempty"`
					CleanupPreview *jobCleanupPreview   `json:"cleanup_preview,omitempty"`
					DryRun         bool                 `json:"dry_run,omitempty"`
				}{Event: ev, Result: result, Cleanup: cleanupSummary, CleanupPreview: cleanupPreview, DryRun: dryRun})
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), result.Job, tmpl)
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
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&payload, "payload", "", "GitHub webhook JSON object.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read GitHub webhook JSON from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the owning job update without writing it.")
	cmd.Flags().BoolVar(&cleanupMerged, "cleanup-merged", false, "After a merged PR event, remove the job-owned worktree and branch.")
	cmd.Flags().BoolVar(&verifyPR, "verify-pr", false, "With --cleanup-merged, verify the recorded GitHub PR is merged with gh before cleanup.")
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
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
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
	Status   job.Status
	Target   string
	Instance string
	Pipeline string
	Ticket   string
	Branch   string
	PR       string
	Sort     string
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

type jobSummary struct {
	Total        int            `json:"total"`
	Queued       int            `json:"queued"`
	Running      int            `json:"running"`
	Blocked      int            `json:"blocked"`
	Done         int            `json:"done"`
	Failed       int            `json:"failed"`
	Targets      map[string]int `json:"targets"`
	Pipelines    map[string]int `json:"pipelines"`
	WithInstance int            `json:"with_instance"`
	WithBranch   int            `json:"with_branch"`
	WithWorktree int            `json:"with_worktree"`
	WithPR       int            `json:"with_pr"`
}

type jobTriageSnapshot struct {
	CheckedAt      time.Time                  `json:"checked_at"`
	Summary        jobSummary                 `json:"summary"`
	Queue          queueSummary               `json:"queue"`
	StatusPreviews []jobStatusReconcileResult `json:"status_previews,omitempty"`
	Attention      []jobTriageItem            `json:"attention"`
	ReadySteps     []jobReadyRow              `json:"ready_steps,omitempty"`
}

type jobTriageItem struct {
	JobID                          string     `json:"job_id"`
	Ticket                         string     `json:"ticket"`
	Status                         job.Status `json:"status"`
	Severity                       string     `json:"severity"`
	Reasons                        []string   `json:"reasons"`
	Actions                        []string   `json:"actions,omitempty"`
	Message                        string     `json:"message,omitempty"`
	Target                         string     `json:"target,omitempty"`
	Instance                       string     `json:"instance,omitempty"`
	Pipeline                       string     `json:"pipeline,omitempty"`
	UpdatedAt                      time.Time  `json:"updated_at"`
	StepID                         string     `json:"step_id,omitempty"`
	StepState                      string     `json:"step_state,omitempty"`
	StepTarget                     string     `json:"step_target,omitempty"`
	QueuePending                   int        `json:"queue_pending,omitempty"`
	QueueDead                      int        `json:"queue_dead,omitempty"`
	QueueDelayed                   int        `json:"queue_delayed,omitempty"`
	QueueIDs                       []string   `json:"queue_ids,omitempty"`
	QueueQuarantined               int        `json:"queue_quarantined,omitempty"`
	QueueQuarantineRestorable      int        `json:"queue_quarantine_restorable,omitempty"`
	QueueQuarantineUnrestorable    int        `json:"queue_quarantine_unrestorable,omitempty"`
	QueueQuarantinePaths           []string   `json:"queue_quarantine_paths,omitempty"`
	QueueQuarantineRestorablePaths []string   `json:"queue_quarantine_restorable_paths,omitempty"`
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

func newJobListFilters(status, target, instance, pipeline, ticket, branch, pr string) (jobListFilters, error) {
	f := jobListFilters{
		Target:   strings.TrimSpace(target),
		Instance: strings.TrimSpace(instance),
		Pipeline: strings.TrimSpace(pipeline),
		Ticket:   strings.TrimSpace(ticket),
		Branch:   strings.TrimSpace(branch),
		PR:       strings.TrimSpace(pr),
	}
	if strings.TrimSpace(status) != "" {
		parsed, err := job.ParseStatus(status)
		if err != nil {
			return f, err
		}
		f.Status = parsed
	}
	return f, nil
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
	summary := summarizeJobs(filtered)
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

func summarizeJobs(jobs []*job.Job) jobSummary {
	summary := jobSummary{
		Targets:   map[string]int{},
		Pipelines: map[string]int{},
	}
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
	queueByJob := queueStatsByJob(jobs, queueItems, now)
	addQueueQuarantineStatsByJob(queueByJob, jobs, quarantineItems)
	attention := make([]jobTriageItem, 0, len(jobs))
	for _, j := range jobs {
		if item, ok := triageJob(j, inspectNextJobStep(j), queueByJob[j.ID], now, staleAfter); ok {
			attention = append(attention, item)
		}
	}
	statusPreviews, err := reconcileJobsFromStatus(teamDir, true, now)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	attention = addStatusPreviewsToJobTriage(attention, jobs, statusPreviews, now)
	sortJobTriageItems(attention)
	readySteps, err := collectJobReadyRows(teamDir, "", map[string]bool{"ready": true})
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	queueSummary := summarizeQueueItems(queueItems, now)
	applyQueueQuarantineSummary(&queueSummary, quarantineItems)
	return jobTriageSnapshot{
		CheckedAt:      now,
		Summary:        summarizeJobs(jobs),
		Queue:          queueSummary,
		StatusPreviews: statusPreviews,
		Attention:      attention,
		ReadySteps:     readySteps,
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

func triageJob(j *job.Job, next jobNextResult, queueStats jobTriageQueueStats, now time.Time, staleAfter time.Duration) (jobTriageItem, bool) {
	item := jobTriageItem{
		JobID:                          j.ID,
		Ticket:                         j.Ticket,
		Status:                         j.Status,
		Severity:                       "info",
		Target:                         j.Target,
		Instance:                       j.Instance,
		Pipeline:                       j.Pipeline,
		UpdatedAt:                      j.UpdatedAt,
		QueuePending:                   queueStats.Pending,
		QueueDead:                      queueStats.Dead,
		QueueDelayed:                   queueStats.Delayed,
		QueueIDs:                       append([]string(nil), queueStats.IDs...),
		QueueQuarantined:               queueStats.Quarantined,
		QueueQuarantineRestorable:      queueStats.QuarantineRestorable,
		QueueQuarantineUnrestorable:    queueStats.QuarantineUnrestorable,
		QueueQuarantinePaths:           append([]string(nil), queueStats.QuarantinePaths...),
		QueueQuarantineRestorablePaths: append([]string(nil), queueStats.QuarantineRestorablePaths...),
	}
	if next.Step != nil {
		item.StepID = next.Step.ID
		item.StepTarget = next.Step.Target
	}
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
		if strings.TrimSpace(j.Instance) == "" {
			addTriageReason("running_without_instance", "warning")
		}
		if staleAfter > 0 && !j.UpdatedAt.IsZero() && j.UpdatedAt.Before(now.Add(-staleAfter)) {
			addTriageReason("stale_running", "warning")
		}
	case job.StatusQueued:
		if staleAfter > 0 && !j.UpdatedAt.IsZero() && j.UpdatedAt.Before(now.Add(-staleAfter)) && queueStats.Pending == 0 && queueStats.Dead == 0 && queueStats.Quarantined == 0 {
			addTriageReason("stale_queued", "warning")
		}
	}
	if queueStats.Dead > 0 {
		addTriageReason("queue_dead", "critical")
	}
	if queueStats.Quarantined > 0 {
		addTriageReason("queue_quarantined", "warning")
	}
	switch next.State {
	case "failed":
		addTriageReason("failed_step", "critical")
	case "blocked":
		addTriageReason("blocked_step", "warning")
	}
	if len(item.Reasons) == 0 {
		return jobTriageItem{}, false
	}
	if strings.TrimSpace(j.LastStatus) != "" {
		item.Message = j.LastStatus
	} else if strings.TrimSpace(next.Message) != "" {
		item.Message = next.Message
	} else {
		item.Message = strings.Join(item.Reasons, ",")
	}
	item.Actions = actionsForJobTriageItem(item)
	return item, true
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

func renderJobTriage(w io.Writer, snapshot jobTriageSnapshot, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	if tmpl != nil {
		return renderJobTriageFormat(w, snapshot, tmpl)
	}
	renderJobSummary(w, snapshot.Summary)
	renderQueueSummary(w, snapshot.Queue)
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
			add(fmt.Sprintf("agent-team job queue retry %s --all", item.JobID))
		}
	}
	if stringSliceContains(item.Reasons, "queue_quarantined") {
		add(fmt.Sprintf("agent-team job queue quarantine %s", item.JobID))
		if item.QueueQuarantineRestorable == 1 && len(item.QueueQuarantineRestorablePaths) == 1 {
			add(fmt.Sprintf("agent-team job queue quarantine restore %s %s --dry-run", item.JobID, item.QueueQuarantineRestorablePaths[0]))
		} else if item.QueueQuarantineRestorable > 1 {
			add(fmt.Sprintf("agent-team job queue quarantine restore %s --all --dry-run", item.JobID))
		}
		if item.QueueQuarantineUnrestorable > 0 {
			add(fmt.Sprintf("agent-team job queue quarantine drop %s --all --unrestorable --dry-run", item.JobID))
		}
	}
	if stringSliceContains(item.Reasons, "failed") || stringSliceContains(item.Reasons, "failed_step") {
		add(fmt.Sprintf("agent-team job retry %s --dispatch", item.JobID))
	}
	if stringSliceContains(item.Reasons, "blocked") || stringSliceContains(item.Reasons, "blocked_step") || stringSliceContains(item.Reasons, "status_file_blocked") {
		add(fmt.Sprintf("agent-team job unblock %s <answer...>", item.JobID))
	}
	if stringSliceContains(item.Reasons, "stale_queued") {
		add(fmt.Sprintf("agent-team job dispatch %s", item.JobID))
	}
	if stringSliceContains(item.Reasons, "stale_running") || stringSliceContains(item.Reasons, "running_without_instance") {
		add("agent-team job reconcile status")
	}
	if stringSliceContains(item.Reasons, "cleanup_ready") {
		add(fmt.Sprintf("agent-team job cleanup %s --dry-run", item.JobID))
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
			Actions:   []string{fmt.Sprintf("agent-team job unblock %s <answer...>", preview.JobID)},
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
	fmt.Fprintf(w, "jobs: total=%d queued=%d running=%d blocked=%d done=%d failed=%d\n",
		summary.Total, summary.Queued, summary.Running, summary.Blocked, summary.Done, summary.Failed)
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
		case "ready", "queued", "running", "blocked", "failed", "done", "none":
			states[state] = true
		default:
			return nil, fmt.Errorf("--state must be ready, queued, running, blocked, failed, done, none, or all")
		}
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("--state requires at least one non-empty state")
	}
	return states, nil
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

func runJobReady(w io.Writer, teamDir, pipeline string, states map[string]bool, jsonOut bool, tmpl *template.Template) error {
	rows, err := collectJobReadyRows(teamDir, pipeline, states)
	if err != nil {
		return err
	}
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
	renderJobReadyTable(w, rows)
	return nil
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
	if next.Step != nil {
		row.StepID = next.Step.ID
		row.Target = next.Step.Target
		row.StepStatus = next.Step.Status
		row.Instance = next.Step.Instance
		row.Gate = next.Step.Gate
	}
	row.Actions = actionsForJobReadyRow(row)
	return row
}

func actionsForJobReadyRow(row jobReadyRow) []string {
	switch row.State {
	case "ready":
		return []string{fmt.Sprintf("agent-team job advance %s", row.JobID)}
	case "queued":
		if len(row.WaitingFor) == 0 && strings.TrimSpace(row.Instance) == "" {
			return []string{fmt.Sprintf("agent-team job advance %s", row.JobID)}
		}
		return []string{"agent-team tick"}
	case "failed":
		return []string{fmt.Sprintf("agent-team job retry %s --dispatch", row.JobID)}
	case "blocked":
		if row.Gate == job.StepGateManual {
			if len(row.WaitingFor) == 0 && strings.TrimSpace(row.StepID) != "" {
				return []string{fmt.Sprintf("agent-team job step %s %s --status queued", row.JobID, row.StepID)}
			}
			return nil
		}
		if row.Gate == job.StepGatePR {
			return []string{fmt.Sprintf("agent-team job update %s --pr <url>", row.JobID)}
		}
		return []string{fmt.Sprintf("agent-team job unblock %s <answer...>", row.JobID)}
	default:
		return nil
	}
}

func renderJobReadyTable(w io.Writer, rows []jobReadyRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no ready pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tSTATE\tSTEP\tTARGET\tPIPELINE\tWAITING_FOR\tUPDATED\tACTION")
	for _, row := range rows {
		waiting := "-"
		if len(row.WaitingFor) > 0 {
			waiting = strings.Join(row.WaitingFor, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.JobID, row.State, emptyDash(row.StepID), emptyDash(row.Target), emptyDash(row.Pipeline), waiting, row.UpdatedAt.Format(time.RFC3339), emptyDash(strings.Join(row.Actions, "; ")))
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
	renderJobTable(w, filtered)
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
	if len(statuses) == 0 {
		return nil, fmt.Errorf("--status requires at least one non-empty status")
	}
	return statuses, nil
}

func runJobWait(ctx context.Context, teamDir, id string, statuses map[job.Status]bool, interval time.Duration) (*job.Job, error) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	var last *job.Job
	for {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		last = j
		if statuses[j.Status] {
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

func runJobInstanceUp(cmd *cobra.Command, repo, id string, opts instanceUpOptions, readyTimeout time.Duration) error {
	if readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job start: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	teamDir, j, err := readJobAndTeamDir(cmd, repo, id)
	if err != nil {
		return err
	}
	instance := strings.TrimSpace(j.Instance)
	if instance == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job start: job %q has no owning instance; dispatch it first.\n", j.ID)
		return exitErr(2)
	}
	repoRoot := filepath.Dir(teamDir)
	if !opts.DryRun {
		if err := ensureDaemonReadyWithTimeout(cmd, repoRoot, opts.JSON || opts.Quiet, readyTimeout); err != nil {
			return err
		}
	}
	if err := runInstanceUpWithOptions(cmd, repoRoot, "", []string{instance}, opts); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}
	applyJobInstanceUpUpdate(j)
	return writeJobWithAudit(teamDir, j, "", "cli", "", nil)
}

func applyJobInstanceUpUpdate(j *job.Job) {
	now := time.Now().UTC()
	if j.Status != job.StatusDone {
		j.Status = job.StatusRunning
	}
	j.LastEvent = "instance_start"
	if strings.TrimSpace(j.Instance) != "" {
		j.LastStatus = "start " + j.Instance
	} else {
		j.LastStatus = "start"
	}
	j.UpdatedAt = now
}

func runJobInstanceDown(cmd *cobra.Command, repo, id string, opts instanceDownOptions, nextStatus job.Status) error {
	teamDir, j, err := readJobAndTeamDir(cmd, repo, id)
	if err != nil {
		return err
	}
	instance := strings.TrimSpace(j.Instance)
	if instance == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job %s: job %q has no owning instance; dispatch it first.\n", downAction(opts), j.ID)
		return exitErr(2)
	}
	if err := runInstanceDownWithOptions(cmd, filepath.Dir(teamDir), []string{instance}, opts); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}
	applyJobInstanceDownUpdate(j, downAction(opts), nextStatus)
	return writeJobWithAudit(teamDir, j, "", "cli", "", nil)
}

func applyJobInstanceDownUpdate(j *job.Job, action string, nextStatus job.Status) {
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
	j.LastEvent = "instance_" + action
	if strings.TrimSpace(j.Instance) != "" {
		j.LastStatus = action + " " + j.Instance
	} else {
		j.LastStatus = action
	}
	j.UpdatedAt = now
}

func filteredJobs(teamDir string, filters jobListFilters) ([]*job.Job, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	filtered := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if jobMatchesFilters(j, filters) {
			filtered = append(filtered, j)
		}
	}
	sortJobs(filtered, filters.Sort)
	return filtered, nil
}

func jobMatchesFilters(j *job.Job, filters jobListFilters) bool {
	if filters.Status != "" && j.Status != filters.Status {
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

func dispatchJobWithPrefix(cmd *cobra.Command, teamDir string, j *job.Job, source, workspace, prefix string) (*jobDispatchResult, string, error) {
	payload, requestedName, err := buildDispatchEventPayload(j.Target, j.Ticket, j.Kickoff, j.Instance, source, workspace)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return nil, "", exitErr(2)
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
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

func runJobQueueList(w io.Writer, teamDir string, j *job.Job, filters queueListFilters, summary, jsonOut bool, tmpl *template.Template) error {
	items, err := queueItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	filtered := filterQueueItems(items, filters.withNow(now))
	if summary {
		queueSummary := summarizeQueueItems(filtered, now)
		if jsonOut {
			return json.NewEncoder(w).Encode(queueSummary)
		}
		renderQueueSummary(w, queueSummary)
		return nil
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(filtered)
	}
	if tmpl != nil {
		return renderQueueItemsFormat(w, filtered, tmpl)
	}
	renderQueueTable(w, filtered)
	return nil
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

func filteredQueueItemsForJob(teamDir string, j *job.Job, filters queueListFilters, limit int, now time.Time) ([]*daemon.QueueItem, error) {
	items, err := queueItemsForJob(teamDir, j)
	if err != nil {
		return nil, err
	}
	matches := filterQueueItems(items, filters.withNow(now))
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func runJobQueueRetryAll(w io.Writer, teamDir string, j *job.Job, filters queueListFilters, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	matches, err := filteredQueueItemsForJob(teamDir, j, filters, limit, time.Now().UTC())
	if err != nil {
		return err
	}
	results, err := retryQueueItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderQueueRetryResults(w, results, jsonOut, tmpl)
}

func runJobQueueDropAll(w io.Writer, teamDir string, j *job.Job, filters queueListFilters, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	matches, err := filteredQueueItemsForJob(teamDir, j, filters, limit, time.Now().UTC())
	if err != nil {
		return err
	}
	results, err := dropQueueItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderQueueDropResults(w, results, jsonOut, tmpl)
}

func runJobQueuePrune(w io.Writer, teamDir string, j *job.Job, state string, olderThan time.Duration, now time.Time, dryRun, jsonOut bool, tmpl *template.Template) error {
	items, err := queueItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	matches := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if queueItemMatchesPrune(item, state, olderThan, now) {
			matches = append(matches, item)
		}
	}
	results, err := pruneQueueItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderQueuePruneResults(w, results, jsonOut, tmpl)
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
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	results := make([]jobEventReconcileResult, 0)
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
	}
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
	data := map[string]string{
		"source":     "daemon_metadata",
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
	Message  string
	Instance string
	PR       string
	Branch   string
	Worktree string
	Skip     bool
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
	Message    string     `json:"message"`
}

type jobReadyRow struct {
	JobID      string     `json:"job_id"`
	Ticket     string     `json:"ticket"`
	Pipeline   string     `json:"pipeline,omitempty"`
	JobStatus  job.Status `json:"job_status"`
	State      string     `json:"state"`
	Actions    []string   `json:"actions,omitempty"`
	StepID     string     `json:"step_id,omitempty"`
	Target     string     `json:"target,omitempty"`
	StepStatus job.Status `json:"step_status,omitempty"`
	Instance   string     `json:"instance,omitempty"`
	Gate       string     `json:"gate,omitempty"`
	WaitingFor []string   `json:"waiting_for,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Message    string     `json:"message"`
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
		j.Status = job.StatusFailed
	case job.StatusBlocked:
		j.Status = job.StatusBlocked
	case job.StatusDone:
		if allJobStepsDone(j) {
			j.Status = job.StatusDone
			j.LastEvent = "pipeline_done"
			j.LastStatus = "all steps done"
		} else {
			j.Status = job.StatusRunning
		}
	default:
		j.Status = status
	}
	return nil
}

func advanceJob(cmd *cobra.Command, teamDir string, j *job.Job, workspace string) (*jobAdvanceResult, error) {
	step := nextReadyJobStep(j)
	if step == nil {
		now := time.Now().UTC()
		if allJobStepsDone(j) {
			j.Status = job.StatusDone
			j.LastEvent = "pipeline_done"
			j.LastStatus = "all steps done"
			j.UpdatedAt = now
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
				return nil, err
			}
			return &jobAdvanceResult{Job: j, Message: "all steps done"}, nil
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
	name := step.Instance
	if strings.TrimSpace(name) == "" {
		name = step.Target + "-" + j.ID + "-" + job.NormalizeID(step.ID)
	}
	payload, requestedName, err := buildDispatchEventPayload(step.Target, j.Ticket, j.Kickoff, name, "job:"+j.ID, workspace)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
		return nil, exitErr(2)
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	if j.Pipeline != "" {
		payload["pipeline"] = j.Pipeline
	}
	payload["pipeline_step"] = step.ID
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
	applyAdvanceResponseToJobStep(j, step.ID, requestedName, res)
	if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": step.ID}); err != nil {
		return nil, err
	}
	if idx := jobStepIndex(j, step.ID); idx >= 0 {
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
	_ = updateJobStep(j, stepID, status, jobStepUpdate{Instance: instance, Message: lastStatus})
	j.LastEvent = lastEvent
	j.LastStatus = lastStatus
}

func inspectNextJobStep(j *job.Job) jobNextResult {
	res := jobNextResult{
		JobID:     j.ID,
		Ticket:    j.Ticket,
		Pipeline:  j.Pipeline,
		JobStatus: j.Status,
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
		res.Message = "all steps done"
		return res
	}
	if step := firstJobStepWithStatus(j, job.StatusFailed); step != nil {
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

func unmetJobStepDependencies(j *job.Job, step *job.Step) []string {
	if step == nil || len(step.After) == 0 {
		return nil
	}
	done := map[string]bool{}
	for _, candidate := range j.Steps {
		if candidate.Status == job.StatusDone {
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
	done := map[string]bool{}
	for _, step := range j.Steps {
		if step.Status == job.StatusDone {
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

func stepManualGatePending(step *job.Step) bool {
	return step != nil && step.Status == job.StatusBlocked && step.Gate == job.StepGateManual
}

func stepPRGatePending(j *job.Job, step *job.Step) bool {
	return step != nil && step.Gate == job.StepGatePR && strings.TrimSpace(j.PR) == ""
}

func stepGatePending(j *job.Job, step *job.Step) bool {
	return stepManualGatePending(step) || stepPRGatePending(j, step)
}

func resetFailedPipelineStepForRetry(j *job.Job) string {
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != job.StatusFailed {
			continue
		}
		if len(unmetJobStepDependencies(j, step)) > 0 {
			continue
		}
		step.Status = job.StatusBlocked
		step.Instance = ""
		step.StartedAt = time.Time{}
		step.FinishedAt = time.Time{}
		return step.ID
	}
	return ""
}

func allJobStepsDone(j *job.Job) bool {
	if len(j.Steps) == 0 {
		return false
	}
	for _, step := range j.Steps {
		if step.Status != job.StatusDone {
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

func renderJobNextResult(w io.Writer, res jobNextResult, jsonOut bool, tmpl *template.Template) error {
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
	waiting := "-"
	if len(res.WaitingFor) > 0 {
		waiting = strings.Join(res.WaitingFor, ",")
	}
	fmt.Fprintf(w, "Job: %s next step=%s state=%s status=%s target=%s instance=%s after=%s gate=%s waiting_for=%s\n",
		res.JobID, res.Step.ID, res.State, res.Step.Status, res.Step.Target, emptyDash(res.Step.Instance), after, gate, waiting)
	return nil
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

type jobAdvancePreview struct {
	Job      *job.Job              `json:"job"`
	Step     *job.Step             `json:"step,omitempty"`
	Dispatch *dispatchRoutePreview `json:"dispatch,omitempty"`
	Message  string                `json:"message,omitempty"`
	DryRun   bool                  `json:"dry_run"`
}

func previewJobAdvanceDispatch(teamDir string, j *job.Job, workspace string) (*jobAdvancePreview, error) {
	step := nextReadyJobStep(j)
	if step == nil {
		message := "no ready steps"
		if allJobStepsDone(j) {
			message = "all steps done"
		}
		return &jobAdvancePreview{Job: j, Message: message, DryRun: true}, nil
	}
	name := step.Instance
	if strings.TrimSpace(name) == "" {
		name = step.Target + "-" + j.ID + "-" + job.NormalizeID(step.ID)
	}
	payload, requestedName, err := buildDispatchEventPayload(step.Target, j.Ticket, j.Kickoff, name, "job:"+j.ID, workspace)
	if err != nil {
		return nil, err
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	if j.Pipeline != "" {
		payload["pipeline"] = j.Pipeline
	}
	payload["pipeline_step"] = step.ID
	dispatch, err := previewDispatchPayload(teamDir, step.Target, requestedName, payload)
	if err != nil {
		return nil, err
	}
	return &jobAdvancePreview{Job: j, Step: step, Dispatch: dispatch, DryRun: true}, nil
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

type jobReopenPreview struct {
	Job    *job.Job `json:"job"`
	DryRun bool     `json:"dry_run"`
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

func renderJobStepPreview(w io.Writer, j *job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(jobStepPreview{Job: j, DryRun: true})
	}
	if tmpl != nil {
		return renderJobTemplate(w, j, tmpl)
	}
	fmt.Fprintln(w, "Dry run: true")
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

func renderJobShowResult(w io.Writer, teamDir string, j *job.Job, jsonOut bool, tmpl *template.Template, includeEvents bool, eventTail int) error {
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
	statusPreviews, err := statusPreviewsForJob(teamDir, j)
	if err != nil {
		return err
	}
	renderJobDetailWithRuntime(w, j, queueItems, statusPreviews, quarantineItems)
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
	fmt.Fprintln(tw, "ID\tSTATUS\tTARGET\tINSTANCE\tPIPELINE\tTICKET\tUPDATED")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Status, j.Target, emptyDash(j.Instance), emptyDash(j.Pipeline), j.Ticket, j.UpdatedAt.Format(time.RFC3339))
	}
	_ = tw.Flush()
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
	renderJobDetailWithRuntime(w, j, nil, nil, nil)
}

func renderJobDetailWithQueue(w io.Writer, j *job.Job, queueItems []*daemon.QueueItem) {
	renderJobDetailWithRuntime(w, j, queueItems, nil, nil)
}

func renderJobRecentEvents(w io.Writer, events []job.Event) {
	fmt.Fprintln(w, "Recent Events:")
	renderJobEventTable(w, events, true)
}

func renderJobDetailWithRuntime(w io.Writer, j *job.Job, queueItems []*daemon.QueueItem, statusPreviews []jobStatusReconcileResult, quarantineItems []queueQuarantineItem) {
	actions := jobDetailActions(j, queueItems, statusPreviews, quarantineItems, time.Now().UTC())
	fmt.Fprintf(w, "ID:          %s\n", j.ID)
	fmt.Fprintf(w, "Status:      %s\n", j.Status)
	fmt.Fprintf(w, "Ticket:      %s\n", j.Ticket)
	if j.TicketURL != "" {
		fmt.Fprintf(w, "Ticket URL:  %s\n", j.TicketURL)
	}
	fmt.Fprintf(w, "Target:      %s\n", j.Target)
	if j.Instance != "" {
		fmt.Fprintf(w, "Instance:    %s\n", j.Instance)
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
			if step.Gate != "" {
				parts = append(parts, "gate="+step.Gate)
			}
			if step.Skipped {
				parts = append(parts, "skipped=true")
				if strings.TrimSpace(step.SkipReason) != "" {
					parts = append(parts, "skip_reason="+step.SkipReason)
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

func jobDetailActions(j *job.Job, queueItems []*daemon.QueueItem, statusPreviews []jobStatusReconcileResult, quarantineItems []queueQuarantineItem, now time.Time) []string {
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
	if triage, ok := triageJob(j, inspectNextJobStep(j), stats, now, 0); ok {
		for _, action := range triage.Actions {
			add(action)
		}
	}
	for _, preview := range statusPreviews {
		if preview.Changed && preview.After == job.StatusBlocked {
			add(fmt.Sprintf("agent-team job unblock %s <answer...>", j.ID))
		}
	}
	if len(j.Steps) > 0 {
		for _, action := range actionsForJobReadyRow(jobReadyRowFromJob(j, inspectNextJobStep(j))) {
			add(action)
		}
	} else if j.Status == job.StatusQueued {
		add(fmt.Sprintf("agent-team job dispatch %s", j.ID))
	}
	return actions
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
