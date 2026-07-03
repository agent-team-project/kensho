package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "queue",
		Aliases: []string{"queues"},
		Short:   "Inspect and control persisted daemon event queue items.",
		Long:    "Inspect and control persisted daemon event queue items under `.agent_team/daemon/queue/`.",
	}
	cmd.AddCommand(newQueueLsCmd())
	cmd.AddCommand(newQueueShowCmd())
	cmd.AddCommand(newQueueRetryCmd())
	cmd.AddCommand(newQueueDropCmd())
	cmd.AddCommand(newQueueDrainCmd())
	cmd.AddCommand(newQueuePruneCmd())
	cmd.AddCommand(newQueueDoctorCmd())
	cmd.AddCommand(newQueueQuarantineCmd())
	return cmd
}

func newQueueLsCmd() *cobra.Command {
	var (
		target      string
		stateFilter string
		instances   []string
		eventTypes  []string
		jobs        []string
		runtimes    []string
		reasons     []string
		readyOnly   bool
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
		Use:     "ls",
		Aliases: []string{"watch"},
		Short:   "List persisted queue items.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.CalledAs() == "watch" {
				watch = true
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && watch {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --commands cannot be combined with --watch.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --interval must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseQueueListSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue ls: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue ls: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFiltersWithRuntime(stateFilter, instances, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue ls: %v\n", err)
				return exitErr(2)
			}
			reasonFilter, err := stringSetFilter(reasons, "--reason", "reason")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue ls: %v\n", err)
				return exitErr(2)
			}
			filters.reasons = reasonFilter
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runQueueSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runQueueListWatch(ctx, cmd.OutOrStdout(), teamDir, filters, queueListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runQueueSummary(cmd.OutOrStdout(), teamDir, filters, jsonOut)
			}
			if commands {
				return runQueueListCommands(cmd.OutOrStdout(), teamDir, filters, queueListOptions{Sort: sortMode, Limit: limit}, nil, operatorCommandScopeFromCommand(cmd, target, "target"))
			}
			return runQueueList(cmd.OutOrStdout(), teamDir, filters, queueListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "Filter by target instance name; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&reasons, "reason", nil, "Filter by queue reason, such as lock_held. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only show pending queue items whose next retry is due now.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the queue table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate queue counts instead of queue rows.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from the visible queue rows, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	return cmd
}

func newQueueShowCmd() *cobra.Command {
	var (
		target   string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one persisted queue item.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue show: %v\n", err)
				return exitErr(2)
			}
			item, teamDir, err := readQueueItemFromRepo(cmd, target, args[0])
			if err != nil {
				return err
			}
			if commands {
				return renderQueueItemCommands(cmd.OutOrStdout(), item, nil, operatorCommandScopeFromCommand(cmd, target, "target"))
			}
			return renderQueueItemResult(cmd.OutOrStdout(), item, jsonOut, tmpl, queueRuntimeMap(teamDir))
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the queue item as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newQueueDropCmd() *cobra.Command {
	var (
		target      string
		jsonOut     bool
		format      string
		dropAll     bool
		dryRun      bool
		commands    bool
		stateFilter string
		instances   []string
		eventTypes  []string
		jobs        []string
		runtimes    []string
		readyOnly   bool
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <id>",
		Short: "Drop pending or dead-letter queue items.",
		Long:  "Drop one queue item by id, or drop a filtered batch with --all. Batch drops default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			scope := operatorCommandScopeFromCommand(cmd, target, "target")
			if dropAll {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drop: --all cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drop: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseQueueListSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drop: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFiltersWithRuntime(effectiveState, instances, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drop: %v\n", err)
					return exitErr(2)
				}
				if commands {
					results, err := queueDropAllResults(teamDir, filters, sortMode, limit, true)
					if err != nil {
						return err
					}
					return renderQueueApplyCommand(cmd.OutOrStdout(), queueDropResultsHaveDryRunAction(results, "would_drop"), queueApplyCommandOptions{
						BaseArgs:   []string{"agent-team", "queue", "drop"},
						Repo:       scope.Repo,
						RepoSet:    scope.Set,
						All:        true,
						State:      stateFilter,
						StateSet:   cmd.Flags().Changed("state"),
						Instances:  instances,
						EventTypes: eventTypes,
						Jobs:       jobs,
						Runtimes:   runtimes,
						Ready:      readyOnly,
						Sort:       sortBy,
						SortSet:    cmd.Flags().Changed("sort"),
						Limit:      limit,
					})
				}
				return runQueueDropAll(cmd.OutOrStdout(), teamDir, filters, sortMode, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drop: requires one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(instances) > 0 || len(eventTypes) > 0 || len(jobs) > 0 || len(runtimes) > 0 || readyOnly || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drop: --state, --instance, --event-type, --job, --runtime, --ready, --sort, and --limit require --all.")
				return exitErr(2)
			}
			id := args[0]
			var item *daemon.QueueItem
			if dryRun || tmpl != nil || commands {
				item, err = daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drop: queue item %q not found.\n", id)
						return exitErr(2)
					}
					return err
				}
			}
			if commands {
				return renderQueueApplyCommand(cmd.OutOrStdout(), item != nil, queueApplyCommandOptions{
					BaseArgs: []string{"agent-team", "queue", "drop", id},
					Repo:     scope.Repo,
					RepoSet:  scope.Set,
				})
			}
			if dryRun {
				return renderQueueDropResults(cmd.OutOrStdout(), []queueDropResult{{
					ID:         item.ID,
					State:      item.State,
					Instance:   item.Instance,
					InstanceID: item.InstanceID,
					Action:     "would_drop",
					DryRun:     true,
				}}, jsonOut, tmpl)
			}
			if dc, err := newDaemonClient(teamDir); err == nil {
				err = dc.QueueDrop(id)
				if err != nil {
					return err
				}
			} else if errors.Is(err, errDaemonNotRunning) {
				if err := daemon.RemoveQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drop: queue item %q not found.\n", id)
						return exitErr(2)
					}
					return err
				}
			} else {
				return err
			}
			if tmpl != nil {
				return renderQueueDropResults(cmd.OutOrStdout(), []queueDropResult{{
					ID:         item.ID,
					State:      item.State,
					Instance:   item.Instance,
					InstanceID: item.InstanceID,
					Action:     "dropped",
				}}, false, tmpl)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"dropped": true, "id": id})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Dropped queue item %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching queue items without dropping them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching drop command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "With --all, filter by target instance name; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only drop pending queue items whose next retry is due now.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newQueueRetryCmd() *cobra.Command {
	var (
		target      string
		jsonOut     bool
		format      string
		retryAll    bool
		dryRun      bool
		commands    bool
		stateFilter string
		instances   []string
		eventTypes  []string
		jobs        []string
		runtimes    []string
		readyOnly   bool
		sortBy      string
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Retry pending or dead-letter queue items.",
		Long:  "Retry one queue item by id, or retry a filtered batch with --all. Batch retries default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue retry: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			scope := operatorCommandScopeFromCommand(cmd, target, "target")
			if retryAll {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --all cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --limit must be >= 0.")
					return exitErr(2)
				}
				sortMode, err := parseQueueListSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue retry: %v\n", err)
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFiltersWithRuntime(effectiveState, instances, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue retry: %v\n", err)
					return exitErr(2)
				}
				if commands {
					results, err := queueRetryAllResults(teamDir, filters, sortMode, limit, true)
					if err != nil {
						return err
					}
					return renderQueueApplyCommand(cmd.OutOrStdout(), queueRetryResultsHaveDryRunAction(results, "would_retry"), queueApplyCommandOptions{
						BaseArgs:   []string{"agent-team", "queue", "retry"},
						Repo:       scope.Repo,
						RepoSet:    scope.Set,
						All:        true,
						State:      stateFilter,
						StateSet:   cmd.Flags().Changed("state"),
						Instances:  instances,
						EventTypes: eventTypes,
						Jobs:       jobs,
						Runtimes:   runtimes,
						Ready:      readyOnly,
						Sort:       sortBy,
						SortSet:    cmd.Flags().Changed("sort"),
						Limit:      limit,
					})
				}
				return runQueueRetryAll(cmd.OutOrStdout(), teamDir, filters, sortMode, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: requires one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(instances) > 0 || len(eventTypes) > 0 || len(jobs) > 0 || len(runtimes) > 0 || readyOnly || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --state, --instance, --event-type, --job, --runtime, --ready, --sort, and --limit require --all.")
				return exitErr(2)
			}
			id := args[0]
			var item *daemon.QueueItem
			if dryRun || tmpl != nil || commands {
				item, err = daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue retry: queue item %q not found.\n", id)
						return exitErr(2)
					}
					return err
				}
			}
			if commands {
				return renderQueueApplyCommand(cmd.OutOrStdout(), item != nil, queueApplyCommandOptions{
					BaseArgs: []string{"agent-team", "queue", "retry", id},
					Repo:     scope.Repo,
					RepoSet:  scope.Set,
				})
			}
			if dryRun {
				return renderQueueRetryResults(cmd.OutOrStdout(), []queueRetryResult{{
					ID:         item.ID,
					State:      item.State,
					Instance:   item.Instance,
					InstanceID: item.InstanceID,
					Action:     "would_retry",
					DryRun:     true,
				}}, jsonOut, tmpl)
			}
			if dc, err := newDaemonClient(teamDir); err == nil {
				outcome, err := dc.QueueRetry(id)
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
				}
				if tmpl != nil {
					return renderQueueRetryResults(cmd.OutOrStdout(), []queueRetryResult{{
						ID:         item.ID,
						State:      item.State,
						Instance:   outcome.Instance,
						InstanceID: outcome.InstanceID,
						Action:     outcome.Action,
						Reason:     outcome.Reason,
					}}, false, tmpl)
				}
				renderQueueRetryOutcome(cmd.OutOrStdout(), outcome)
				return nil
			} else if !errors.Is(err, errDaemonNotRunning) {
				return err
			}

			if item == nil {
				item, err = daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue retry: queue item %q not found.\n", id)
						return exitErr(2)
					}
					return err
				}
			}
			originalState := item.State
			if err := daemon.ResetQueueItemForRetry(daemon.DaemonRoot(teamDir), item); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(item)
			}
			if tmpl != nil {
				return renderQueueRetryResults(cmd.OutOrStdout(), []queueRetryResult{{
					ID:         item.ID,
					State:      originalState,
					Instance:   item.Instance,
					InstanceID: item.InstanceID,
					Action:     "reset",
				}}, false, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Queue item %s marked pending; start the daemon to dispatch it.\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching queue items without retrying them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching retry command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "With --all, filter by target instance name; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only retry pending queue items whose next retry is due now.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "With --all, sort matching queue items before limiting: state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newQueueDrainCmd() *cobra.Command {
	var (
		target   string
		dryRun   bool
		commands bool
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drain",
		Short: "Ask the running daemon to dispatch ready pending queue items.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drain: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drain: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drain: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drain: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drain: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			scope := operatorCommandScopeFromCommand(cmd, target, "target")
			if dryRun {
				if dc, err := newDaemonClient(teamDir); err == nil {
					result, err := dc.QueueDrain(true)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drain: %v\n", err)
						return exitErr(1)
					}
					if commands {
						return renderQueueDrainApplyCommand(cmd.OutOrStdout(), result, scope)
					}
					return renderQueueDrainResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
				} else if !errors.Is(err, errDaemonNotRunning) {
					return err
				}
				result, err := previewQueueDrainLocal(teamDir)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drain: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderQueueDrainApplyCommand(cmd.OutOrStdout(), result, scope)
				}
				return renderQueueDrainResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue drain: daemon is not running — start it first with `agent-team daemon start`.")
					return exitErr(2)
				}
				return err
			}
			result, err := dc.QueueDrain(dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue drain: %v\n", err)
				return exitErr(1)
			}
			return renderQueueDrainResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview ready queue items without dispatching them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching drain command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drain result with a Go template, e.g. '{{.Dispatched}} {{.Pending}}'.")
	return cmd
}

func newQueuePruneCmd() *cobra.Command {
	var (
		target     string
		stateFlag  string
		olderThan  time.Duration
		dryRun     bool
		commands   bool
		jsonOut    bool
		format     string
		instances  []string
		eventTypes []string
		jobs       []string
		runtimes   []string
		readyOnly  bool
		limit      int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune persisted queue items.",
		Long:  "Prune persisted queue items. By default this removes dead-letter items.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue prune: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue prune: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue prune: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue prune: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueuePruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseQueuePruneStateWithReady(stateFlag, readyOnly, cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue prune: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFiltersWithRuntime("", instances, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			scope := operatorCommandScopeFromCommand(cmd, target, "target")
			results, err := pruneQueueItems(teamDir, state, olderThan, time.Now().UTC(), dryRun, filters, limit)
			if err != nil {
				return err
			}
			if commands {
				return renderQueueApplyCommand(cmd.OutOrStdout(), queuePruneResultsHaveDryRunAction(results), queueApplyCommandOptions{
					BaseArgs:     []string{"agent-team", "queue", "prune"},
					Repo:         scope.Repo,
					RepoSet:      scope.Set,
					State:        stateFlag,
					StateSet:     cmd.Flags().Changed("state"),
					Instances:    instances,
					EventTypes:   eventTypes,
					Jobs:         jobs,
					Runtimes:     runtimes,
					Ready:        readyOnly,
					Limit:        limit,
					OlderThan:    olderThan,
					OlderThanSet: cmd.Flags().Changed("older-than"),
				})
			}
			return renderQueuePruneResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&stateFlag, "state", daemon.QueueStateDead, "Queue state to prune: dead, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune items older than this duration based on retry/dead-letter/update time.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "Filter by target instance name before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket before pruning; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Filter by queued dispatch runtime before pruning: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only prune pending queue items whose next retry is due now. Defaults --state to pending when --state is omitted.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Prune at most this many matching queue items; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview queue items that would be pruned without dropping them.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching prune command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func readQueueItemFromRepo(cmd *cobra.Command, target, id string) (*daemon.QueueItem, string, error) {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return nil, "", err
	}
	item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue show: queue item %q not found.\n", id)
			return nil, "", exitErr(2)
		}
		return nil, "", err
	}
	return item, teamDir, nil
}

func parseQueueStateFilter(raw string) (string, error) {
	state := strings.ToLower(strings.TrimSpace(raw))
	switch state {
	case "", daemon.QueueStatePending, daemon.QueueStateDead:
		return state, nil
	default:
		return "", fmt.Errorf("--state must be pending or dead")
	}
}

type queueListFilters struct {
	state             string
	instances         map[string]bool
	eventTypes        map[string]bool
	jobs              map[string]bool
	runtimes          map[string]bool
	reasons           map[string]bool
	runtimeByInstance map[string]string
	readyOnly         bool
	now               time.Time
}

type queueListOptions struct {
	Sort  string
	Limit int
}

func parseQueueListFilters(stateRaw string, instancesRaw, eventTypesRaw, jobsRaw []string, readyOnly bool, now time.Time) (queueListFilters, error) {
	state, err := parseQueueStateFilter(stateRaw)
	if err != nil {
		return queueListFilters{}, err
	}
	instances, err := stringSetFilter(instancesRaw, "--instance", "instance")
	if err != nil {
		return queueListFilters{}, err
	}
	eventTypes, err := stringSetFilter(eventTypesRaw, "--event-type", "event type")
	if err != nil {
		return queueListFilters{}, err
	}
	jobs, err := jobIDSetFilter(jobsRaw, "--job")
	if err != nil {
		return queueListFilters{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return queueListFilters{
		state:      state,
		instances:  instances,
		eventTypes: eventTypes,
		jobs:       jobs,
		readyOnly:  readyOnly,
		now:        now,
	}, nil
}

func parseQueueListFiltersWithRuntime(stateRaw string, instancesRaw, eventTypesRaw, jobsRaw, runtimesRaw []string, readyOnly bool, now time.Time) (queueListFilters, error) {
	filters, err := parseQueueListFilters(stateRaw, instancesRaw, eventTypesRaw, jobsRaw, readyOnly, now)
	if err != nil {
		return queueListFilters{}, err
	}
	runtimes, err := lifecycleRuntimeFilterSet(runtimesRaw)
	if err != nil {
		return queueListFilters{}, err
	}
	filters.runtimes = runtimes
	return filters, nil
}

func jobIDSetFilter(values []string, flagName string) (map[string]bool, error) {
	raw, err := stringSetFilter(values, flagName, "job")
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]bool, len(raw))
	for value := range raw {
		id := job.IDFromInput(value)
		if id == "" {
			return nil, fmt.Errorf("%s value %q produced an empty job id", flagName, value)
		}
		out[id] = true
	}
	return out, nil
}

func (f queueListFilters) withNow(now time.Time) queueListFilters {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	f.now = now
	return f
}

func (f queueListFilters) withRuntimeByInstance(runtimeByInstance map[string]string) queueListFilters {
	f.runtimeByInstance = runtimeByInstance
	return f
}

func (f queueListFilters) empty() bool {
	return f.state == "" && len(f.instances) == 0 && len(f.eventTypes) == 0 && len(f.jobs) == 0 && len(f.runtimes) == 0 && len(f.reasons) == 0 && !f.readyOnly
}

func (f queueListFilters) match(item *daemon.QueueItem) bool {
	if f.state != "" && item.State != f.state {
		return false
	}
	if len(f.instances) > 0 && !f.instances[item.Instance] {
		return false
	}
	if len(f.eventTypes) > 0 && !f.eventTypes[item.EventType] {
		return false
	}
	if len(f.jobs) > 0 && !queueItemMatchesJobIDs(item, f.jobs) {
		return false
	}
	if len(f.runtimes) > 0 && !f.runtimes[queueItemRuntimeKey(item, f.runtimeByInstance)] {
		return false
	}
	if len(f.reasons) > 0 && !f.reasons[item.Reason] {
		return false
	}
	if f.readyOnly {
		if item.State != daemon.QueueStatePending {
			return false
		}
		now := f.now
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if !item.NextRetry.IsZero() && item.NextRetry.After(now) {
			return false
		}
	}
	return true
}

func queueItemMatchesJobIDs(item *daemon.QueueItem, ids map[string]bool) bool {
	if item == nil || len(ids) == 0 {
		return true
	}
	for _, key := range []string{"job_id", "job", "ticket"} {
		if id := job.NormalizeID(queuePayloadString(item.Payload, key)); id != "" && ids[id] {
			return true
		}
	}
	return false
}

func filterQueueItems(items []*daemon.QueueItem, filters queueListFilters) []*daemon.QueueItem {
	if filters.empty() {
		return items
	}
	out := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if filters.match(item) {
			out = append(out, item)
		}
	}
	return out
}

func prepareQueueListItems(items []*daemon.QueueItem, opts queueListOptions, runtimeByInstance map[string]string) []*daemon.QueueItem {
	sortQueueItems(items, opts.Sort, runtimeByInstance)
	return limitQueueItems(items, opts.Limit)
}

func parseQueueListSort(raw string) (string, error) {
	sortMode := strings.ToLower(strings.TrimSpace(raw))
	switch sortMode {
	case "", "state", "id", "event", "instance", "job", "runtime", "queued", "updated", "next-retry", "attempts":
		if sortMode == "" {
			return "state", nil
		}
		return sortMode, nil
	default:
		return "", fmt.Errorf("--sort must be state, id, event, instance, job, runtime, queued, updated, next-retry, or attempts")
	}
}

func sortQueueItems(items []*daemon.QueueItem, sortMode string, runtimeByInstance map[string]string) {
	sortMode = strings.ToLower(strings.TrimSpace(sortMode))
	if sortMode == "" {
		sortMode = "state"
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		switch sortMode {
		case "id":
			if left.ID != right.ID {
				return left.ID < right.ID
			}
		case "event":
			if left.EventType != right.EventType {
				return left.EventType < right.EventType
			}
		case "instance":
			if left.Instance != right.Instance {
				return left.Instance < right.Instance
			}
		case "job":
			if leftJob, rightJob := queueItemSortJobID(left), queueItemSortJobID(right); leftJob != rightJob {
				return leftJob < rightJob
			}
		case "runtime":
			if leftRuntime, rightRuntime := queueItemRuntimeKey(left, runtimeByInstance), queueItemRuntimeKey(right, runtimeByInstance); leftRuntime != rightRuntime {
				return leftRuntime < rightRuntime
			}
		case "queued":
			if !left.QueuedAt.Equal(right.QueuedAt) {
				return left.QueuedAt.After(right.QueuedAt)
			}
		case "updated":
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.After(right.UpdatedAt)
			}
		case "next-retry":
			if !left.NextRetry.Equal(right.NextRetry) {
				if left.NextRetry.IsZero() {
					return false
				}
				if right.NextRetry.IsZero() {
					return true
				}
				return left.NextRetry.Before(right.NextRetry)
			}
		case "attempts":
			if left.Attempts != right.Attempts {
				return left.Attempts > right.Attempts
			}
		case "state":
			if left.State != right.State {
				return left.State < right.State
			}
			if !left.QueuedAt.Equal(right.QueuedAt) {
				return left.QueuedAt.Before(right.QueuedAt)
			}
		}
		return left.ID < right.ID
	})
}

func limitQueueItems(items []*daemon.QueueItem, limit int) []*daemon.QueueItem {
	if limit <= 0 || limit >= len(items) {
		return items
	}
	return items[:limit]
}

func prepareQueueActionMatches(items []*daemon.QueueItem, sortMode string, limit int, runtimeByInstance map[string]string) []*daemon.QueueItem {
	sortQueueItems(items, sortMode, runtimeByInstance)
	return limitQueueItems(items, limit)
}

func queueItemSortJobID(item *daemon.QueueItem) string {
	if item == nil {
		return ""
	}
	for _, key := range []string{"job_id", "job", "ticket"} {
		if id := job.NormalizeID(queuePayloadString(item.Payload, key)); id != "" {
			return id
		}
	}
	return ""
}

func previewQueueDrainLocal(teamDir string) (*daemon.QueueDrainResult, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	return previewQueueDrainItems(top, items, time.Now().UTC()), nil
}

func previewQueueDrainItems(top *topology.Topology, items []*daemon.QueueItem, now time.Time) *daemon.QueueDrainResult {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := &daemon.QueueDrainResult{DryRun: true, Outcomes: []daemon.EventOutcome{}}
	capacityByInstance := map[string]int{}
	for _, item := range items {
		if item == nil {
			continue
		}
		switch item.State {
		case daemon.QueueStatePending:
			result.Pending++
		case daemon.QueueStateDead:
			result.Dead++
			continue
		default:
			continue
		}
		if !item.NextRetry.IsZero() && item.NextRetry.After(now) {
			continue
		}
		if top == nil {
			continue
		}
		inst := top.Find(item.Instance)
		if inst == nil || !inst.Ephemeral {
			continue
		}
		capacity, ok := capacityByInstance[item.Instance]
		if !ok {
			capacity = inst.Replicas
		}
		if capacity <= 0 {
			capacityByInstance[item.Instance] = capacity
			continue
		}
		result.WouldDispatch++
		result.Outcomes = append(result.Outcomes, daemon.EventOutcome{
			Instance:   item.Instance,
			Action:     "would_dispatch",
			InstanceID: item.InstanceID,
		})
		capacityByInstance[item.Instance] = capacity - 1
	}
	return result
}

const queuePruneStateAll = "all"

type queuePruneResult struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	Instance   string    `json:"instance"`
	InstanceID string    `json:"instance_id"`
	QueuedAt   time.Time `json:"queued_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Reference  time.Time `json:"reference_time"`
	DryRun     bool      `json:"dry_run,omitempty"`
	Dropped    bool      `json:"dropped"`
}

type queueDropResult struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Instance   string `json:"instance"`
	InstanceID string `json:"instance_id"`
	Action     string `json:"action"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

type queueRetryResult struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Instance   string `json:"instance"`
	InstanceID string `json:"instance_id"`
	Action     string `json:"action"`
	Reason     string `json:"reason,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

type queueApplyCommandOptions struct {
	BaseArgs     []string
	Repo         string
	RepoSet      bool
	Target       string
	TargetSet    bool
	All          bool
	State        string
	StateSet     bool
	Instances    []string
	EventTypes   []string
	Jobs         []string
	Runtimes     []string
	Ready        bool
	Force        bool
	Restorable   bool
	Unrestorable bool
	Sort         string
	SortSet      bool
	Limit        int
	OlderThan    time.Duration
	OlderThanSet bool
}

type queueActionResolver func(*daemon.QueueItem, time.Time) []string

type queueSummary struct {
	Total                  int            `json:"total"`
	Pending                int            `json:"pending"`
	Dead                   int            `json:"dead"`
	Delayed                int            `json:"delayed"`
	Attempts               int            `json:"attempts"`
	Quarantined            int            `json:"quarantined,omitempty"`
	QuarantineRestorable   int            `json:"quarantine_restorable,omitempty"`
	QuarantineUnrestorable int            `json:"quarantine_unrestorable,omitempty"`
	Instances              map[string]int `json:"instances"`
	Events                 map[string]int `json:"events"`
	Runtimes               map[string]int `json:"runtimes"`
	Reasons                map[string]int `json:"reasons"`
}

func (s queueSummary) MarshalJSON() ([]byte, error) {
	type queueSummaryJSON queueSummary
	if s.Instances == nil {
		s.Instances = map[string]int{}
	}
	if s.Events == nil {
		s.Events = map[string]int{}
	}
	if s.Runtimes == nil {
		s.Runtimes = map[string]int{}
	}
	if s.Reasons == nil {
		s.Reasons = map[string]int{}
	}
	return json.Marshal(queueSummaryJSON(s))
}

func parseQueuePruneState(raw string) (string, error) {
	state := strings.ToLower(strings.TrimSpace(raw))
	switch state {
	case "", daemon.QueueStateDead:
		return daemon.QueueStateDead, nil
	case daemon.QueueStatePending, queuePruneStateAll:
		return state, nil
	default:
		return "", fmt.Errorf("--state must be dead, pending, or all")
	}
}

func parseQueuePruneStateWithReady(raw string, readyOnly, stateChanged bool) (string, error) {
	state, err := parseQueuePruneState(raw)
	if err != nil {
		return "", err
	}
	if readyOnly && !stateChanged {
		return daemon.QueueStatePending, nil
	}
	return state, nil
}

func pruneQueueItems(teamDir, state string, olderThan time.Duration, now time.Time, dryRun bool, filters queueListFilters, limit int) ([]queuePruneResult, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
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

func prepareQueuePruneMatches(items []*daemon.QueueItem, limit int) []*daemon.QueueItem {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		leftRef, rightRef := queuePruneReferenceTime(left), queuePruneReferenceTime(right)
		if !leftRef.Equal(rightRef) {
			if leftRef.IsZero() {
				return false
			}
			if rightRef.IsZero() {
				return true
			}
			return leftRef.Before(rightRef)
		}
		if left != nil && right != nil && !left.QueuedAt.Equal(right.QueuedAt) {
			return left.QueuedAt.Before(right.QueuedAt)
		}
		if left == nil || right == nil {
			return right != nil
		}
		return left.ID < right.ID
	})
	return limitQueueItems(items, limit)
}

func pruneQueueItemMatches(teamDir string, items []*daemon.QueueItem, dryRun bool) ([]queuePruneResult, error) {
	var dc *daemonClient
	if !dryRun {
		client, err := newDaemonClient(teamDir)
		if err == nil {
			dc = client
		} else if !errors.Is(err, errDaemonNotRunning) {
			return nil, err
		}
	}
	results := make([]queuePruneResult, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result := queuePruneResult{
			ID:         item.ID,
			State:      item.State,
			Instance:   item.Instance,
			InstanceID: item.InstanceID,
			QueuedAt:   item.QueuedAt,
			UpdatedAt:  item.UpdatedAt,
			Reference:  queuePruneReferenceTime(item),
			DryRun:     dryRun,
		}
		if !dryRun {
			if dc != nil {
				if err := dc.QueueDrop(item.ID); err != nil {
					return nil, err
				}
			} else if err := daemon.RemoveQueueItem(daemon.DaemonRoot(teamDir), item.ID); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					return nil, err
				}
			}
			result.Dropped = true
		}
		results = append(results, result)
	}
	return results, nil
}

func runQueueDropAll(w io.Writer, teamDir string, filters queueListFilters, sortMode string, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := queueDropAllResults(teamDir, filters, sortMode, limit, dryRun)
	if err != nil {
		return err
	}
	return renderQueueDropResults(w, results, jsonOut, tmpl)
}

func queueDropAllResults(teamDir string, filters queueListFilters, sortMode string, limit int, dryRun bool) ([]queueDropResult, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	runtimeByInstance := queueRuntimeMap(teamDir)
	matches := filterQueueItems(items, filters.withNow(time.Now().UTC()).withRuntimeByInstance(runtimeByInstance))
	matches = prepareQueueActionMatches(matches, sortMode, limit, runtimeByInstance)
	return dropQueueItemMatches(teamDir, matches, dryRun)
}

func renderQueueApplyCommand(w io.Writer, hasAction bool, opts queueApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(queueApplyCommandArgs(opts)), " "))
	return err
}

func renderQueueDrainApplyCommand(w io.Writer, result *daemon.QueueDrainResult, scope operatorCommandScope) error {
	if result == nil || !result.DryRun || result.WouldDispatch == 0 {
		return nil
	}
	return renderQueueApplyCommand(w, true, queueApplyCommandOptions{
		BaseArgs: []string{"agent-team", "queue", "drain"},
		Repo:     scope.Repo,
		RepoSet:  scope.Set,
	})
}

func queueApplyCommandArgs(opts queueApplyCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	if opts.RepoSet {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.TargetSet {
		args = append(args, "--target", opts.Target)
	}
	if opts.All {
		args = append(args, "--all")
	}
	if opts.Force {
		args = append(args, "--force")
	}
	if opts.StateSet && strings.TrimSpace(opts.State) != "" {
		args = append(args, "--state", opts.State)
	}
	args = appendRepeatedQueueFlag(args, "--instance", opts.Instances)
	args = appendRepeatedQueueFlag(args, "--event-type", opts.EventTypes)
	args = appendRepeatedQueueFlag(args, "--job", opts.Jobs)
	args = appendRepeatedQueueFlag(args, "--runtime", opts.Runtimes)
	if opts.Ready {
		args = append(args, "--ready")
	}
	if opts.Restorable {
		args = append(args, "--restorable")
	}
	if opts.Unrestorable {
		args = append(args, "--unrestorable")
	}
	if opts.SortSet && strings.TrimSpace(opts.Sort) != "" {
		args = append(args, "--sort", opts.Sort)
	}
	if opts.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(opts.Limit))
	}
	if opts.OlderThanSet {
		args = append(args, "--older-than", opts.OlderThan.String())
	}
	return args
}

func appendRepeatedQueueFlag(args []string, flag string, values []string) []string {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		args = append(args, flag, value)
	}
	return args
}

func queueDropResultsHaveDryRunAction(results []queueDropResult, action string) bool {
	for _, result := range results {
		if result.DryRun && result.Action == action {
			return true
		}
	}
	return false
}

func queueRetryResultsHaveDryRunAction(results []queueRetryResult, action string) bool {
	for _, result := range results {
		if result.DryRun && result.Action == action {
			return true
		}
	}
	return false
}

func queuePruneResultsHaveDryRunAction(results []queuePruneResult) bool {
	for _, result := range results {
		if result.DryRun && !result.Dropped {
			return true
		}
	}
	return false
}

func dropQueueItemMatches(teamDir string, matches []*daemon.QueueItem, dryRun bool) ([]queueDropResult, error) {
	var dc *daemonClient
	if !dryRun {
		client, err := newDaemonClient(teamDir)
		if err == nil {
			dc = client
		} else if !errors.Is(err, errDaemonNotRunning) {
			return nil, err
		}
	}
	results := make([]queueDropResult, 0, len(matches))
	for _, item := range matches {
		result := queueDropResult{
			ID:         item.ID,
			State:      item.State,
			Instance:   item.Instance,
			InstanceID: item.InstanceID,
		}
		if dryRun {
			result.Action = "would_drop"
			result.DryRun = true
		} else {
			if dc != nil {
				if err := dc.QueueDrop(item.ID); err != nil {
					return nil, err
				}
			} else if err := daemon.RemoveQueueItem(daemon.DaemonRoot(teamDir), item.ID); err != nil {
				return nil, err
			}
			result.Action = "dropped"
		}
		results = append(results, result)
	}
	return results, nil
}

func runQueueRetryAll(w io.Writer, teamDir string, filters queueListFilters, sortMode string, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := queueRetryAllResults(teamDir, filters, sortMode, limit, dryRun)
	if err != nil {
		return err
	}
	return renderQueueRetryResults(w, results, jsonOut, tmpl)
}

func queueRetryAllResults(teamDir string, filters queueListFilters, sortMode string, limit int, dryRun bool) ([]queueRetryResult, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	runtimeByInstance := queueRuntimeMap(teamDir)
	matches := filterQueueItems(items, filters.withNow(time.Now().UTC()).withRuntimeByInstance(runtimeByInstance))
	matches = prepareQueueActionMatches(matches, sortMode, limit, runtimeByInstance)
	return retryQueueItemMatches(teamDir, matches, dryRun)
}

func retryQueueItemMatches(teamDir string, matches []*daemon.QueueItem, dryRun bool) ([]queueRetryResult, error) {
	var dc *daemonClient
	if !dryRun {
		client, err := newDaemonClient(teamDir)
		if err == nil {
			dc = client
		} else if !errors.Is(err, errDaemonNotRunning) {
			return nil, err
		}
	}
	results := make([]queueRetryResult, 0, len(matches))
	for _, item := range matches {
		result := queueRetryResult{
			ID:         item.ID,
			State:      item.State,
			Instance:   item.Instance,
			InstanceID: item.InstanceID,
		}
		switch {
		case dryRun:
			result.Action = "would_retry"
			result.DryRun = true
		case dc != nil:
			outcome, err := dc.QueueRetry(item.ID)
			if err != nil {
				return nil, err
			}
			result.Action = outcome.Action
			result.Instance = outcome.Instance
			result.InstanceID = outcome.InstanceID
			result.Reason = outcome.Reason
		default:
			if err := daemon.ResetQueueItemForRetry(daemon.DaemonRoot(teamDir), item); err != nil {
				return nil, err
			}
			result.Action = "reset"
		}
		results = append(results, result)
	}
	return results, nil
}

func runQueueSummary(w io.Writer, teamDir string, filters queueListFilters, jsonOut bool) error {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	runtimeByInstance := queueRuntimeMap(teamDir)
	filtered := filters.withNow(now).withRuntimeByInstance(runtimeByInstance)
	summary := summarizeQueueItems(filterQueueItems(items, filtered), now, runtimeByInstance)
	quarantine, err := listQueueQuarantine(teamDir)
	if err != nil {
		return err
	}
	applyQueueQuarantineSummary(&summary, filterQueueQuarantineItems(quarantine, filtered))
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	renderQueueSummary(w, summary)
	return nil
}

func runQueueSummaryWatch(ctx context.Context, w io.Writer, teamDir string, filters queueListFilters, jsonOut bool, interval time.Duration, clear bool) error {
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
		if err := runQueueSummary(w, teamDir, filters, jsonOut); err != nil {
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

func summarizeQueueItems(items []*daemon.QueueItem, now time.Time, runtimeByInstanceOpt ...map[string]string) queueSummary {
	var runtimeByInstance map[string]string
	if len(runtimeByInstanceOpt) > 0 {
		runtimeByInstance = runtimeByInstanceOpt[0]
	}
	summary := queueSummary{
		Instances: map[string]int{},
		Events:    map[string]int{},
		Runtimes:  map[string]int{},
		Reasons:   map[string]int{},
	}
	for _, item := range items {
		summary.Total++
		switch item.State {
		case daemon.QueueStatePending:
			summary.Pending++
		case daemon.QueueStateDead:
			summary.Dead++
		}
		if !item.NextRetry.IsZero() && item.NextRetry.After(now) {
			summary.Delayed++
		}
		summary.Attempts += item.Attempts
		if strings.TrimSpace(item.Instance) != "" {
			summary.Instances[item.Instance]++
		}
		if strings.TrimSpace(item.EventType) != "" {
			summary.Events[item.EventType]++
		}
		summary.Runtimes[queueItemRuntimeKey(item, runtimeByInstance)]++
		if strings.TrimSpace(item.Reason) != "" {
			summary.Reasons[item.Reason]++
		}
	}
	return summary
}

func applyQueueQuarantineSummary(summary *queueSummary, items []queueQuarantineItem) {
	if summary == nil {
		return
	}
	summary.Quarantined = len(items)
	summary.QuarantineRestorable = 0
	summary.QuarantineUnrestorable = 0
	for _, item := range items {
		if item.Restorable {
			summary.QuarantineRestorable++
		} else {
			summary.QuarantineUnrestorable++
		}
	}
}

func queueSummaryLine(summary queueSummary) string {
	return fmt.Sprintf("queue: total=%d pending=%d dead=%d delayed=%d attempts=%d%s",
		summary.Total,
		summary.Pending,
		summary.Dead,
		summary.Delayed,
		summary.Attempts,
		queueQuarantineSummaryText(summary))
}

func queueQuarantineSummaryText(summary queueSummary) string {
	out := fmt.Sprintf(" quarantined=%d", summary.Quarantined)
	if summary.Quarantined > 0 {
		out += fmt.Sprintf(" restorable=%d unrestorable=%d", summary.QuarantineRestorable, summary.QuarantineUnrestorable)
	}
	return out
}

func renderQueueSummary(w io.Writer, summary queueSummary) {
	fmt.Fprintln(w, queueSummaryLine(summary))
	if len(summary.Instances) > 0 {
		fmt.Fprint(w, "instances:")
		for _, key := range sortedCountKeys(summary.Instances) {
			fmt.Fprintf(w, " %s=%d", key, summary.Instances[key])
		}
		fmt.Fprintln(w)
	}
	if len(summary.Events) > 0 {
		fmt.Fprint(w, "events:")
		for _, key := range sortedCountKeys(summary.Events) {
			fmt.Fprintf(w, " %s=%d", key, summary.Events[key])
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
	if len(summary.Reasons) > 0 {
		fmt.Fprint(w, "reasons:")
		for _, key := range sortedCountKeys(summary.Reasons) {
			fmt.Fprintf(w, " %s=%d", key, summary.Reasons[key])
		}
		fmt.Fprintln(w)
	}
}

func queueItemMatchesPrune(item *daemon.QueueItem, state string, olderThan time.Duration, now time.Time) bool {
	if item == nil {
		return false
	}
	if state != queuePruneStateAll && item.State != state {
		return false
	}
	if olderThan <= 0 {
		return true
	}
	ref := queuePruneReferenceTime(item)
	if ref.IsZero() {
		return false
	}
	return !ref.After(now.Add(-olderThan))
}

func queuePruneReferenceTime(item *daemon.QueueItem) time.Time {
	if item == nil {
		return time.Time{}
	}
	if !item.DeadLetteredAt.IsZero() {
		return item.DeadLetteredAt
	}
	if !item.NextRetry.IsZero() {
		return item.NextRetry
	}
	if !item.UpdatedAt.IsZero() {
		return item.UpdatedAt
	}
	return item.QueuedAt
}

func renderQueuePruneResults(w io.Writer, results []queuePruneResult, jsonOut bool, tmpl *template.Template) error {
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
	renderQueuePruneTable(w, results)
	return nil
}

func renderQueueDropResults(w io.Writer, results []queueDropResult, jsonOut bool, tmpl *template.Template) error {
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
		fmt.Fprintln(w, "(no queue items dropped)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tINSTANCE\tINSTANCE_ID\tACTION")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			result.ID, result.State, result.Instance, result.InstanceID, result.Action)
	}
	return tw.Flush()
}

func renderQueueRetryResults(w io.Writer, results []queueRetryResult, jsonOut bool, tmpl *template.Template) error {
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
		fmt.Fprintln(w, "(no queue items retried)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tINSTANCE\tINSTANCE_ID\tACTION\tREASON")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			result.ID, result.State, result.Instance, result.InstanceID, result.Action, emptyDash(result.Reason))
	}
	return tw.Flush()
}

func renderQueueDrainResult(w io.Writer, result *daemon.QueueDrainResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &daemon.QueueDrainResult{}
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
		fmt.Fprintf(w, "queue drain dry-run: would_dispatch=%d pending=%d dead=%d\n",
			result.WouldDispatch, result.Pending, result.Dead)
	} else {
		fmt.Fprintf(w, "queue drain: attempted=%d dispatched=%d rejected=%d pending=%d dead=%d\n",
			result.Attempted, result.Dispatched, result.Rejected, result.Pending, result.Dead)
	}
	if len(result.Outcomes) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tINSTANCE_ID\tACTION\tREASON")
	for _, outcome := range result.Outcomes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			outcome.Instance, outcome.InstanceID, outcome.Action, emptyDash(outcome.Reason))
	}
	return tw.Flush()
}

func renderQueuePruneTable(w io.Writer, results []queuePruneResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no queue items pruned)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tINSTANCE\tINSTANCE_ID\tACTION\tREFERENCE")
	for _, result := range results {
		action := "dropped"
		if result.DryRun {
			action = "would_drop"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			result.ID, result.State, result.Instance, result.InstanceID, action, queueTime(result.Reference))
	}
	_ = tw.Flush()
}

func runQueueList(w io.Writer, teamDir string, filters queueListFilters, opts queueListOptions, jsonOut bool, tmpl *template.Template) error {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	runtimeByInstance := queueRuntimeMap(teamDir)
	filtered := filterQueueItems(items, filters.withNow(time.Now().UTC()).withRuntimeByInstance(runtimeByInstance))
	filtered = prepareQueueListItems(filtered, opts, runtimeByInstance)
	if jsonOut {
		if filtered == nil {
			filtered = []*daemon.QueueItem{}
		}
		return json.NewEncoder(w).Encode(filtered)
	}
	if tmpl != nil {
		return renderQueueItemsFormat(w, filtered, tmpl)
	}
	renderQueueTable(w, filtered, runtimeByInstance)
	return nil
}

func runQueueListCommands(w io.Writer, teamDir string, filters queueListFilters, opts queueListOptions, actions queueActionResolver, scope operatorCommandScope) error {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	runtimeByInstance := queueRuntimeMap(teamDir)
	filtered := filterQueueItems(items, filters.withNow(time.Now().UTC()).withRuntimeByInstance(runtimeByInstance))
	filtered = prepareQueueListItems(filtered, opts, runtimeByInstance)
	return renderQueueItemsCommands(w, filtered, actions, scope)
}

func runQueueListWatch(ctx context.Context, w io.Writer, teamDir string, filters queueListFilters, opts queueListOptions, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runQueueList(w, teamDir, filters, opts, jsonOut, tmpl); err != nil {
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

func parseQueueFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("queue-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseQueuePruneFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("queue-prune-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderQueueItemsFormat(w io.Writer, items []*daemon.QueueItem, tmpl *template.Template) error {
	for _, item := range items {
		if err := renderQueueItemTemplate(w, item, tmpl); err != nil {
			return err
		}
	}
	return nil
}

func renderQueueItemResult(w io.Writer, item *daemon.QueueItem, jsonOut bool, tmpl *template.Template, runtimeByInstanceOpt ...map[string]string) error {
	return renderQueueItemResultWithActions(w, item, jsonOut, tmpl, nil, runtimeByInstanceOpt...)
}

func renderQueueItemResultWithActions(w io.Writer, item *daemon.QueueItem, jsonOut bool, tmpl *template.Template, actions queueActionResolver, runtimeByInstanceOpt ...map[string]string) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(item)
	}
	if tmpl != nil {
		return renderQueueItemTemplate(w, item, tmpl)
	}
	var runtimeByInstance map[string]string
	if len(runtimeByInstanceOpt) > 0 {
		runtimeByInstance = runtimeByInstanceOpt[0]
	}
	renderQueueDetailWithActions(w, item, runtimeByInstance, actions)
	return nil
}

func renderQueueItemCommands(w io.Writer, item *daemon.QueueItem, actions queueActionResolver, scope operatorCommandScope) error {
	return renderOperatorActionCommands(w, queueItemResolvedActions(item, time.Now().UTC(), actions), scope)
}

func renderQueueItemsCommands(w io.Writer, items []*daemon.QueueItem, actions queueActionResolver, scope operatorCommandScope) error {
	now := time.Now().UTC()
	out := make([]string, 0, len(items)*2)
	for _, item := range items {
		out = append(out, queueItemResolvedActions(item, now, actions)...)
	}
	return renderOperatorActionCommands(w, out, scope)
}

func renderQueueItemTemplate(w io.Writer, item *daemon.QueueItem, tmpl *template.Template) error {
	if err := tmpl.Execute(w, item); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderQueueTable(w io.Writer, items []*daemon.QueueItem, runtimeByInstanceOpt ...map[string]string) {
	var runtimeByInstance map[string]string
	if len(runtimeByInstanceOpt) > 0 {
		runtimeByInstance = runtimeByInstanceOpt[0]
	}
	renderQueueTableWithActions(w, items, runtimeByInstance, nil)
}

func renderQueueTableWithActions(w io.Writer, items []*daemon.QueueItem, runtimeByInstance map[string]string, actions queueActionResolver) {
	if len(items) == 0 {
		fmt.Fprintln(w, "(no queue items)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tINSTANCE\tINSTANCE_ID\tRUNTIME\tREASON\tATTEMPTS\tNEXT_RETRY\tACTION\tLAST_ERROR")
	now := time.Now().UTC()
	for _, item := range items {
		itemActions := queueItemResolvedActions(item, now, actions)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			item.ID, item.State, item.Instance, item.InstanceID, queueItemRuntimeLabel(item, runtimeByInstance), emptyDash(item.Reason), item.Attempts, queueTime(item.NextRetry), emptyDash(strings.Join(itemActions, "; ")), emptyDash(item.LastError))
	}
	_ = tw.Flush()
}

func queueRuntimeMap(teamDir string) map[string]string {
	return jobRuntimeMap(teamDir)
}

func queueItemRuntimeKey(item *daemon.QueueItem, runtimeByInstance map[string]string) string {
	if item == nil {
		return "unknown"
	}
	if runtime := strings.ToLower(strings.TrimSpace(queuePayloadString(item.Payload, "runtime"))); runtime != "" {
		return runtime
	}
	for _, instance := range []string{item.InstanceID, item.Instance} {
		instance = strings.TrimSpace(instance)
		if instance == "" {
			continue
		}
		if runtime := strings.ToLower(strings.TrimSpace(runtimeByInstance[instance])); runtime != "" {
			return runtime
		}
	}
	return "unknown"
}

func queueItemRuntimeLabel(item *daemon.QueueItem, runtimeByInstance map[string]string) string {
	runtime := queueItemRuntimeKey(item, runtimeByInstance)
	if runtime == "" || runtime == "unknown" {
		return "-"
	}
	return runtime
}

func renderQueueDetail(w io.Writer, item *daemon.QueueItem, runtimeByInstanceOpt ...map[string]string) {
	var runtimeByInstance map[string]string
	if len(runtimeByInstanceOpt) > 0 {
		runtimeByInstance = runtimeByInstanceOpt[0]
	}
	renderQueueDetailWithActions(w, item, runtimeByInstance, nil)
}

func renderQueueDetailWithActions(w io.Writer, item *daemon.QueueItem, runtimeByInstance map[string]string, actions queueActionResolver) {
	fmt.Fprintf(w, "ID:          %s\n", item.ID)
	fmt.Fprintf(w, "State:       %s\n", item.State)
	fmt.Fprintf(w, "Event:       %s\n", item.EventType)
	fmt.Fprintf(w, "Instance:    %s\n", item.Instance)
	fmt.Fprintf(w, "Instance ID: %s\n", item.InstanceID)
	fmt.Fprintf(w, "Runtime:     %s\n", queueItemRuntimeLabel(item, runtimeByInstance))
	if item.Reason != "" {
		fmt.Fprintf(w, "Reason:      %s\n", item.Reason)
	}
	if len(item.Locks) > 0 {
		fmt.Fprintf(w, "Locks:       %s\n", strings.Join(item.Locks, ","))
	}
	fmt.Fprintf(w, "Attempts:    %d\n", item.Attempts)
	if !item.NextRetry.IsZero() {
		fmt.Fprintf(w, "Next Retry:  %s\n", item.NextRetry.Format(time.RFC3339))
	}
	if item.LastError != "" {
		fmt.Fprintf(w, "Last Error:  %s\n", item.LastError)
	}
	fmt.Fprintf(w, "Queued:      %s\n", item.QueuedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Updated:     %s\n", item.UpdatedAt.Format(time.RFC3339))
	if !item.DeadLetteredAt.IsZero() {
		fmt.Fprintf(w, "Dead:        %s\n", item.DeadLetteredAt.Format(time.RFC3339))
	}
	now := time.Now().UTC()
	itemActions := queueItemResolvedActions(item, now, actions)
	if len(itemActions) > 0 {
		fmt.Fprintln(w, "Actions:")
		for _, action := range itemActions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	if len(item.Payload) > 0 {
		body, _ := json.MarshalIndent(item.Payload, "", "  ")
		fmt.Fprintf(w, "Payload:\n%s\n", string(body))
	}
}

func queueItemResolvedActions(item *daemon.QueueItem, now time.Time, actions queueActionResolver) []string {
	itemActions := queueItemActions(item, now)
	if actions != nil {
		itemActions = actions(item, now)
	}
	return itemActions
}

func queueItemActions(item *daemon.QueueItem, now time.Time) []string {
	if item == nil {
		return nil
	}
	jobID := queueItemActionJobID(item)
	queueCommand := func(verb string) string {
		if jobID != "" {
			return fmt.Sprintf("agent-team job queue %s %s %s", verb, jobID, item.ID)
		}
		return fmt.Sprintf("agent-team queue %s %s", verb, item.ID)
	}
	switch item.State {
	case daemon.QueueStateDead:
		return []string{
			queueCommand("retry"),
			queueCommand("drop"),
		}
	case daemon.QueueStatePending:
		if !item.NextRetry.IsZero() && item.NextRetry.After(now.UTC()) {
			showAction := fmt.Sprintf("agent-team queue show %s", item.ID)
			if jobID != "" {
				showAction = fmt.Sprintf("agent-team job queue show %s %s", jobID, item.ID)
			}
			return []string{
				showAction,
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

func queueItemActionJobID(item *daemon.QueueItem) string {
	if item == nil {
		return ""
	}
	for _, key := range []string{"job_id", "job"} {
		if id := job.NormalizeID(queuePayloadString(item.Payload, key)); id != "" {
			return id
		}
	}
	return ""
}

func renderQueueRetryOutcome(w io.Writer, outcome *daemon.EventOutcome) {
	switch outcome.Action {
	case "dispatched":
		fmt.Fprintf(w, "Retried %s as %s\n", outcome.Instance, outcome.InstanceID)
	case "queued":
		fmt.Fprintf(w, "Queued %s as %s\n", outcome.Instance, outcome.InstanceID)
	case "rejected":
		fmt.Fprintf(w, "Rejected %s as %s: %s\n", outcome.Instance, outcome.InstanceID, outcome.Reason)
	default:
		fmt.Fprintf(w, "%s %s as %s\n", outcome.Action, outcome.Instance, outcome.InstanceID)
	}
}

func queueTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}
