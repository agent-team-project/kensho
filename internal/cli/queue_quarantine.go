package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/spf13/cobra"
)

const queueQuarantineDir = "quarantine"

const queueQuarantineSortFlagHelp = "Sort rows by path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size."

type queueQuarantineItem struct {
	Path        string    `json:"path"`
	State       string    `json:"state,omitempty"`
	ID          string    `json:"id,omitempty"`
	EventType   string    `json:"event_type,omitempty"`
	Instance    string    `json:"instance,omitempty"`
	InstanceID  string    `json:"instance_id,omitempty"`
	Job         string    `json:"job,omitempty"`
	RestorePath string    `json:"restore_path,omitempty"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	QueuedAt    time.Time `json:"queued_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	NextRetry   time.Time `json:"next_retry,omitempty"`
	Attempts    int       `json:"attempts,omitempty"`
	Restorable  bool      `json:"restorable"`
	Problem     string    `json:"problem,omitempty"`
}

type queueQuarantineSummary struct {
	Quarantined  int            `json:"quarantined"`
	Restorable   int            `json:"restorable,omitempty"`
	Unrestorable int            `json:"unrestorable,omitempty"`
	States       map[string]int `json:"states,omitempty"`
	Events       map[string]int `json:"events,omitempty"`
	Instances    map[string]int `json:"instances,omitempty"`
	Jobs         map[string]int `json:"jobs,omitempty"`
}

func summarizeQueueQuarantineItems(items []queueQuarantineItem) queueQuarantineSummary {
	summary := queueQuarantineSummary{
		States:    map[string]int{},
		Events:    map[string]int{},
		Instances: map[string]int{},
		Jobs:      map[string]int{},
	}
	for _, item := range items {
		summary.Quarantined++
		if item.Restorable {
			summary.Restorable++
		} else {
			summary.Unrestorable++
		}
		if strings.TrimSpace(item.State) != "" {
			summary.States[item.State]++
		}
		if strings.TrimSpace(item.EventType) != "" {
			summary.Events[item.EventType]++
		}
		if strings.TrimSpace(item.Instance) != "" {
			summary.Instances[item.Instance]++
		}
		if jobID := job.NormalizeID(item.Job); jobID != "" {
			summary.Jobs[jobID]++
		}
	}
	return summary
}

func queueQuarantineSummaryLine(summary queueQuarantineSummary) string {
	return fmt.Sprintf("queue quarantine: quarantined=%d restorable=%d unrestorable=%d",
		summary.Quarantined,
		summary.Restorable,
		summary.Unrestorable)
}

type queueQuarantineRestoreResult struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	State       string `json:"state,omitempty"`
	ID          string `json:"id,omitempty"`
	Action      string `json:"action"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Overwrite   bool   `json:"overwrite,omitempty"`
}

type queueQuarantineDropResult struct {
	Path       string `json:"path"`
	State      string `json:"state,omitempty"`
	ID         string `json:"id,omitempty"`
	Restorable bool   `json:"restorable"`
	Action     string `json:"action"`
	DryRun     bool   `json:"dry_run,omitempty"`
	Dropped    bool   `json:"dropped,omitempty"`
}

type queueQuarantineShowResult struct {
	queueQuarantineItem
	Team      string            `json:"team,omitempty"`
	Pipeline  string            `json:"pipeline,omitempty"`
	ScopeJob  string            `json:"scope_job,omitempty"`
	QueueItem *daemon.QueueItem `json:"queue_item,omitempty"`
}

var queueQuarantineSortModes = []string{
	"path",
	"state",
	"id",
	"event",
	"instance",
	"job",
	"queued",
	"updated",
	"modified",
	"attempts",
	"restorable",
	"size",
}

var queueQuarantineSortLessers = map[string]quarantineSortLess[queueQuarantineItem]{
	"state":      quarantineStringLess(func(item queueQuarantineItem) string { return item.State }),
	"id":         quarantineStringLess(func(item queueQuarantineItem) string { return item.ID }),
	"event":      quarantineStringLess(func(item queueQuarantineItem) string { return item.EventType }),
	"instance":   quarantineStringLess(func(item queueQuarantineItem) string { return item.Instance }),
	"job":        quarantineStringLess(func(item queueQuarantineItem) string { return item.Job }),
	"queued":     quarantineTimeDescLess(func(item queueQuarantineItem) time.Time { return item.QueuedAt }),
	"updated":    quarantineTimeDescLess(func(item queueQuarantineItem) time.Time { return item.UpdatedAt }),
	"modified":   quarantineTimeDescLess(func(item queueQuarantineItem) time.Time { return item.ModTime }),
	"attempts":   quarantineIntDescLess(func(item queueQuarantineItem) int { return item.Attempts }),
	"restorable": quarantineBoolTrueFirstLess(func(item queueQuarantineItem) bool { return item.Restorable }),
	"size":       quarantineInt64DescLess(func(item queueQuarantineItem) int64 { return item.Size }),
}

var queueQuarantineListColumns = []quarantineListColumn[queueQuarantineItem]{
	{Header: "PATH", Value: func(item queueQuarantineItem) string { return item.Path }},
	{Header: "STATE", Value: func(item queueQuarantineItem) string { return emptyDash(item.State) }},
	{Header: "ID", Value: func(item queueQuarantineItem) string { return emptyDash(item.ID) }},
	{Header: "INSTANCE", Value: func(item queueQuarantineItem) string { return emptyDash(item.Instance) }},
	{Header: "EVENT", Value: func(item queueQuarantineItem) string { return emptyDash(item.EventType) }},
	{Header: "JOB", Value: func(item queueQuarantineItem) string { return emptyDash(item.Job) }},
	{Header: "RESTORABLE", Value: func(item queueQuarantineItem) string { return queueQuarantineRestorableText(item.Restorable) }},
	{Header: "PROBLEM", Value: func(item queueQuarantineItem) string { return emptyDash(item.Problem) }},
}

var queueQuarantineRestoreConfig = quarantineRestoreConfig[queueQuarantineItem, queueQuarantineRestoreResult]{
	Root: func(teamDir string) string {
		return daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	},
	Normalize:   normalizeQueueQuarantinePath,
	Inspect:     inspectQueueQuarantineFile,
	SafePath:    queueDoctorSafeQueuePath,
	Path:        func(item queueQuarantineItem) string { return item.Path },
	RestorePath: func(item queueQuarantineItem) string { return item.RestorePath },
	Restorable:  func(item queueQuarantineItem) bool { return item.Restorable },
	Problem:     func(item queueQuarantineItem) string { return item.Problem },
	NewResult: func(item queueQuarantineItem, dryRun, force bool) queueQuarantineRestoreResult {
		return queueQuarantineRestoreResult{
			Path:        item.Path,
			Destination: item.RestorePath,
			State:       item.State,
			ID:          item.ID,
			Action:      "would_restore",
			DryRun:      dryRun,
			Overwrite:   force,
		}
	},
	MarkRestored: func(result *queueQuarantineRestoreResult) {
		result.Action = "restored"
		result.DryRun = false
	},
}

var queueQuarantineDropConfig = quarantineDropConfig[queueQuarantineItem, queueQuarantineDropResult]{
	Root: func(teamDir string) string {
		return daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	},
	Normalize: normalizeQueueQuarantinePath,
	Inspect:   inspectQueueQuarantineFile,
	SafePath:  queueDoctorSafeQueuePath,
	Path:      func(item queueQuarantineItem) string { return item.Path },
	NewResult: func(item queueQuarantineItem, dryRun bool) queueQuarantineDropResult {
		return queueQuarantineDropResult{
			Path:       item.Path,
			State:      item.State,
			ID:         item.ID,
			Restorable: item.Restorable,
			Action:     "would_drop",
			DryRun:     dryRun,
		}
	},
	MarkDropped: func(result *queueQuarantineDropResult) {
		result.Action = "dropped"
		result.Dropped = true
		result.DryRun = false
	},
	Prune: pruneEmptyQueueQuarantineDirs,
}

func newQueueQuarantineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quarantine",
		Short: "Inspect, restore, and drop quarantined queue files.",
		Long:  "Inspect queue files moved under `.agent_team/daemon/queue/quarantine/`, restore validated entries to the active queue, or explicitly drop preserved files.",
	}
	cmd.AddCommand(newQueueQuarantineLsCmd())
	cmd.AddCommand(newQueueQuarantineShowCmd())
	cmd.AddCommand(newQueueQuarantineRestoreCmd())
	cmd.AddCommand(newQueueQuarantineDropCmd())
	return cmd
}

func newQueueQuarantineLsCmd() *cobra.Command {
	var (
		target       string
		stateFilter  string
		instances    []string
		eventTypes   []string
		jobs         []string
		restorable   bool
		unrestorable bool
		sortBy       string
		limit        int
		summary      bool
		commands     bool
		jsonOut      bool
		format       string
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List quarantined queue files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine ls: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine ls: --limit must be >= 0.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine ls: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine ls: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine ls: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine ls: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || limit > 0) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine ls: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			sortMode, err := parseQueueQuarantineSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine ls: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team queue quarantine ls", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, instances, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			items, err := listQueueQuarantine(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine ls: %v\n", err)
				return exitErr(1)
			}
			items = filterQueueQuarantineItems(items, filters)
			items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
			if summary {
				return renderQueueQuarantineSummary(cmd.OutOrStdout(), summarizeQueueQuarantineItems(items), jsonOut)
			}
			items = prepareQueueQuarantineItems(items, sortMode, limit)
			if commands {
				return renderQueueQuarantineListCommands(cmd.OutOrStdout(), items, nil, operatorCommandScopeFromCommand(cmd, target, rootRepoFlagName))
			}
			return renderQueueQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "Filter by target instance name; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined files that cannot be restored.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", queueQuarantineSortFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate quarantined queue-file counts instead of rows.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from the visible quarantined queue files, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit quarantined queue files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	return cmd
}

func newQueueQuarantineShowCmd() *cobra.Command {
	var (
		target   string
		jsonOut  bool
		format   string
		commands bool
	)
	cmd := &cobra.Command{
		Use:   "show <quarantine-path>",
		Short: "Show one quarantined queue file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team queue quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := showQueueQuarantine(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine show: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderQueueQuarantineCommands(cmd.OutOrStdout(), result, operatorCommandScopeFromCommand(cmd, target, rootRepoFlagName))
			}
			return renderQueueQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the quarantined queue file as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the quarantined queue file with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newQueueQuarantineRestoreCmd() *cobra.Command {
	var (
		target      string
		restoreAll  bool
		dryRun      bool
		force       bool
		stateFilter string
		instances   []string
		eventTypes  []string
		jobs        []string
		sortBy      string
		limit       int
		jsonOut     bool
		format      string
		commands    bool
	)
	cmd := &cobra.Command{
		Use:   "restore [quarantine-path]",
		Short: "Restore validated quarantined queue files.",
		Long:  "Restore one validated quarantined queue file by path, or restore a filtered batch of restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine restore: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine restore: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine restore: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team queue quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, instances, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine restore: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine restore: --limit must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			scope := operatorCommandScopeFromCommand(cmd, target, rootRepoFlagName)
			if restoreAll {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine restore: --all cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseQueueQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine restore: %v\n", err)
					return exitErr(2)
				}
				results, err := restoreQueueQuarantineAll(teamDir, dryRun, force, filters, sortMode, limit)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderQueueApplyCommand(cmd.OutOrStdout(), queueQuarantineRestoreResultsHaveDryRunAction(results, "would_restore"), queueApplyCommandOptions{
						BaseArgs:   []string{"agent-team", "queue", "quarantine", "restore"},
						Repo:       scope.Repo,
						RepoSet:    scope.Set,
						All:        true,
						Force:      force,
						State:      stateFilter,
						StateSet:   cmd.Flags().Changed("state"),
						Instances:  instances,
						EventTypes: eventTypes,
						Jobs:       jobs,
						Sort:       sortBy,
						SortSet:    cmd.Flags().Changed("sort"),
						Limit:      limit,
					})
				}
				return renderQueueQuarantineRestoreMany(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine restore: requires one path unless --all is set.")
				return exitErr(2)
			}
			if !filters.empty() || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine restore: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			result, err := restoreQueueQuarantine(teamDir, args[0], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderQueueApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_restore", queueApplyCommandOptions{
					BaseArgs: []string{"agent-team", "queue", "quarantine", "restore", result.Path},
					Repo:     scope.Repo,
					RepoSet:  scope.Set,
					Force:    force,
				})
			}
			return renderQueueQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active queue file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "With --all, filter by target instance name; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, restore at most this many matching quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching restore apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newQueueQuarantineDropCmd() *cobra.Command {
	var (
		target       string
		dropAll      bool
		dryRun       bool
		stateFilter  string
		instances    []string
		eventTypes   []string
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
	cmd := &cobra.Command{
		Use:   "drop [quarantine-path]",
		Short: "Drop quarantined queue files after inspection.",
		Long:  "Drop one quarantined queue file by path, or drop a filtered batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --limit must be >= 0.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team queue quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, instances, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			scope := operatorCommandScopeFromCommand(cmd, target, rootRepoFlagName)
			if dropAll {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --all cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseQueueQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine drop: %v\n", err)
					return exitErr(2)
				}
				results, err := dropQueueQuarantineAll(teamDir, dryRun, olderThan, restorable, unrestorable, filters, sortMode, limit, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderQueueApplyCommand(cmd.OutOrStdout(), queueQuarantineDropResultsHaveDryRunAction(results, "would_drop"), queueApplyCommandOptions{
						BaseArgs:     []string{"agent-team", "queue", "quarantine", "drop"},
						Repo:         scope.Repo,
						RepoSet:      scope.Set,
						All:          true,
						State:        stateFilter,
						StateSet:     cmd.Flags().Changed("state"),
						Instances:    instances,
						EventTypes:   eventTypes,
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
				return renderQueueQuarantineDrop(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: requires one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !filters.empty() || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			result, err := dropQueueQuarantine(teamDir, args[0], dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine drop: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderQueueApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_drop", queueApplyCommandOptions{
					BaseArgs: []string{"agent-team", "queue", "quarantine", "drop", result.Path},
					Repo:     scope.Repo,
					RepoSet:  scope.Set,
				})
			}
			return renderQueueQuarantineDrop(cmd.OutOrStdout(), []queueQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "With --all, filter by target instance name; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching quarantined files before limiting: path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching drop apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func listQueueQuarantine(teamDir string) ([]queueQuarantineItem, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	return listQuarantineItems(queueRoot, queueQuarantineDir, ".json", inspectQueueQuarantineFile, func(item queueQuarantineItem) string {
		return item.Path
	})
}

func inspectQueueQuarantineFile(queueRoot, rel string) (queueQuarantineItem, error) {
	source, err := queueDoctorSafeQueuePath(queueRoot, rel)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	item := queueQuarantineItem{
		Path:    filepath.Clean(rel),
		State:   queueQuarantineState(rel),
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
	if item.State != "" {
		item.RestorePath = filepath.Join(item.State, filepath.Base(item.Path))
	}
	body, err := os.ReadFile(source)
	if err != nil {
		item.Problem = err.Error()
		return item, nil
	}
	var raw daemon.QueueItem
	if err := json.Unmarshal(body, &raw); err != nil {
		item.Problem = fmt.Sprintf("invalid JSON: %v", err)
		return item, nil
	}
	idFromPath := strings.TrimSuffix(filepath.Base(item.Path), ".json")
	if strings.TrimSpace(raw.ID) == "" {
		raw.ID = idFromPath
	}
	raw.State = item.State
	item.ID = raw.ID
	item.EventType = raw.EventType
	item.Instance = raw.Instance
	item.InstanceID = raw.InstanceID
	item.Job = queueQuarantineJob(raw.Payload)
	item.QueuedAt = raw.QueuedAt.UTC()
	item.UpdatedAt = raw.UpdatedAt.UTC()
	item.NextRetry = raw.NextRetry.UTC()
	item.Attempts = raw.Attempts
	if err := validateQueueQuarantineRestore(raw); err != nil {
		item.Problem = err.Error()
		return item, nil
	}
	item.Restorable = true
	return item, nil
}

func queueQuarantineState(rel string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) < 4 || parts[0] != queueQuarantineDir {
		return ""
	}
	switch parts[2] {
	case daemon.QueueStatePending, daemon.QueueStateDead:
		return parts[2]
	default:
		return ""
	}
}

func filterQueueQuarantineItems(items []queueQuarantineItem, filters queueListFilters) []queueQuarantineItem {
	if filters.empty() {
		return items
	}
	out := make([]queueQuarantineItem, 0, len(items))
	for _, item := range items {
		if filters.state != "" && item.State != filters.state {
			continue
		}
		if len(filters.instances) > 0 && !filters.instances[item.Instance] {
			continue
		}
		if len(filters.eventTypes) > 0 && !filters.eventTypes[item.EventType] {
			continue
		}
		if len(filters.jobs) > 0 && !filters.jobs[job.NormalizeID(item.Job)] {
			continue
		}
		if len(filters.runtimes) > 0 {
			continue
		}
		if filters.readyOnly && item.State != daemon.QueueStatePending {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterQueueQuarantineRestorable(items []queueQuarantineItem, restorableOnly, unrestorableOnly bool) []queueQuarantineItem {
	return filterQuarantineRestorable(items, restorableOnly, unrestorableOnly, func(item queueQuarantineItem) bool {
		return item.Restorable
	})
}

func queueQuarantineJob(payload map[string]any) string {
	for _, key := range []string{"job_id", "job", "ticket"} {
		if id := job.NormalizeID(queuePayloadString(payload, key)); id != "" {
			return id
		}
	}
	return ""
}

func validateQueueQuarantineRestore(item daemon.QueueItem) error {
	switch item.State {
	case daemon.QueueStatePending, daemon.QueueStateDead:
	default:
		return fmt.Errorf("queue state is required in quarantine path")
	}
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(item.EventType) == "" {
		return fmt.Errorf("event_type is required")
	}
	if strings.TrimSpace(item.Instance) == "" {
		return fmt.Errorf("instance is required")
	}
	if strings.TrimSpace(item.InstanceID) == "" {
		return fmt.Errorf("instance_id is required")
	}
	if item.QueuedAt.IsZero() {
		return fmt.Errorf("queued_at is required")
	}
	if item.UpdatedAt.IsZero() {
		return fmt.Errorf("updated_at is required")
	}
	return nil
}

func showQueueQuarantine(teamDir, rawPath string) (queueQuarantineShowResult, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	rel, err := normalizeQueueQuarantinePath(rawPath)
	if err != nil {
		return queueQuarantineShowResult{}, err
	}
	item, err := inspectQueueQuarantineFile(queueRoot, rel)
	if err != nil {
		return queueQuarantineShowResult{}, err
	}
	result := queueQuarantineShowResult{queueQuarantineItem: item}
	source, err := queueDoctorSafeQueuePath(queueRoot, item.Path)
	if err != nil {
		return result, nil
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return result, nil
	}
	var raw daemon.QueueItem
	if err := json.Unmarshal(body, &raw); err != nil {
		return result, nil
	}
	if strings.TrimSpace(raw.ID) == "" {
		raw.ID = strings.TrimSuffix(filepath.Base(item.Path), ".json")
	}
	raw.State = item.State
	result.QueueItem = &raw
	return result, nil
}

func restoreQueueQuarantine(teamDir, rawPath string, dryRun, force bool) (queueQuarantineRestoreResult, error) {
	return restoreQuarantineItem(teamDir, rawPath, dryRun, force, queueQuarantineRestoreConfig)
}

func restoreQueueQuarantineAll(teamDir string, dryRun, force bool, filters queueListFilters, sortMode string, limit int) ([]queueQuarantineRestoreResult, error) {
	items, err := listQueueQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = filterQueueQuarantineItems(items, filters.withNow(time.Now().UTC()))
	items = filterQueueQuarantineRestorable(items, true, false)
	return restoreQueueQuarantineItems(teamDir, items, dryRun, force, sortMode, limit)
}

func restoreQueueQuarantineItems(teamDir string, items []queueQuarantineItem, dryRun, force bool, sortMode string, limit int) ([]queueQuarantineRestoreResult, error) {
	return restoreQuarantineItemBatch(teamDir, items, dryRun, force, sortMode, limit, prepareQueueQuarantineItems, func(item queueQuarantineItem) string {
		return item.Path
	}, restoreQueueQuarantine)
}

func dropQueueQuarantine(teamDir, rawPath string, dryRun bool) (queueQuarantineDropResult, error) {
	return dropQuarantinePath(teamDir, rawPath, dryRun, queueQuarantineDropConfig)
}

func dropQueueQuarantineAll(teamDir string, dryRun bool, olderThan time.Duration, restorable, unrestorable bool, filters queueListFilters, sortMode string, limit int, now time.Time) ([]queueQuarantineDropResult, error) {
	items, err := listQueueQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = filterQueueQuarantineItems(items, filters.withNow(now))
	items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
	return dropQueueQuarantineItems(teamDir, items, dryRun, olderThan, unrestorable, sortMode, limit, now)
}

func dropQueueQuarantineItems(teamDir string, items []queueQuarantineItem, dryRun bool, olderThan time.Duration, unrestorable bool, sortMode string, limit int, now time.Time) ([]queueQuarantineDropResult, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	if unrestorable {
		items = filterQueueQuarantineRestorable(items, false, true)
	}
	return dropQuarantineItemBatch(queueRoot, items, dryRun, olderThan, sortMode, limit, now, sortQueueQuarantineItems, func(item queueQuarantineItem) time.Time {
		return item.ModTime
	}, dropQueueQuarantineItem)
}

func prepareQueueQuarantineItems(items []queueQuarantineItem, sortMode string, limit int) []queueQuarantineItem {
	return prepareQuarantineItems(items, sortMode, limit, sortQueueQuarantineItems)
}

func parseQueueQuarantineSort(raw string) (string, error) {
	return parseQuarantineSort(raw, queueQuarantineSortModes, "--sort must be path, state, id, event, instance, job, queued, updated, modified, attempts, restorable, or size")
}

func sortQueueQuarantineItems(items []queueQuarantineItem, sortMode string) {
	sortQuarantineItems(items, sortMode, func(item queueQuarantineItem) string {
		return item.Path
	}, queueQuarantineSortLessers)
}

func dropQueueQuarantineItem(queueRoot string, item queueQuarantineItem, dryRun bool) (queueQuarantineDropResult, error) {
	return dropQuarantineItem(queueRoot, item, dryRun, queueQuarantineDropConfig)
}

func pruneEmptyQueueQuarantineDirs(queueRoot, dir string) {
	pruneEmptyQuarantineDirs(queueRoot, dir, queueQuarantineDir)
}

func normalizeQueueQuarantinePath(raw string) (string, error) {
	return normalizeQuarantinePath(raw, queueQuarantineDir, ".json", "quarantine path must look like quarantine/<timestamp>/pending/<file>.json or quarantine/<timestamp>/dead/<file>.json", queueQuarantineState)
}

func parseQueueQuarantineCommandFormat(cmd *cobra.Command, command, format string, jsonOut bool) (*template.Template, error) {
	return parseQuarantineCommandFormat(cmd, command, format, "queue-quarantine-format", jsonOut)
}

func parseQueueQuarantineFormat(format string) (*template.Template, error) {
	return parseQuarantineFormat(format, "queue-quarantine-format")
}

func renderQueueQuarantineList(w io.Writer, items []queueQuarantineItem, jsonOut bool, tmpl *template.Template) error {
	return renderQuarantineList(w, items, jsonOut, tmpl, "(no quarantined queue files)", queueQuarantineListColumns)
}

func renderQueueQuarantineSummary(w io.Writer, summary queueQuarantineSummary, jsonOut bool) error {
	return renderQuarantineSummary(w, summary, jsonOut, queueQuarantineSummaryLine)
}

func queueQuarantineRestorableText(restorable bool) string {
	return quarantineRestorableText(restorable)
}

func renderQueueQuarantineRestore(w io.Writer, result queueQuarantineRestoreResult, jsonOut bool, tmpl *template.Template) error {
	return renderQuarantineResult(w, result, jsonOut, tmpl, renderQueueQuarantineRestoreLine)
}

func renderQueueQuarantineRestoreMany(w io.Writer, results []queueQuarantineRestoreResult, jsonOut bool, tmpl *template.Template) error {
	return renderQuarantineResults(w, results, jsonOut, tmpl, "(no restorable quarantined queue files matched)", renderQueueQuarantineRestoreLine)
}

func renderQueueQuarantineRestoreLine(w io.Writer, result queueQuarantineRestoreResult) {
	switch result.Action {
	case "would_restore":
		fmt.Fprintf(w, "Would restore %s -> %s\n", result.Path, result.Destination)
	default:
		fmt.Fprintf(w, "Restored %s -> %s\n", result.Path, result.Destination)
	}
}

func queueQuarantineRestoreResultsHaveDryRunAction(results []queueQuarantineRestoreResult, action string) bool {
	return quarantineResultsHaveDryRunAction(results, action, func(result queueQuarantineRestoreResult, action string) bool {
		return result.DryRun && result.Action == action
	})
}

func renderQueueQuarantineShow(w io.Writer, result queueQuarantineShowResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderQuarantineTemplate(w, result, tmpl)
	}
	fmt.Fprintf(w, "Path:        %s\n", result.Path)
	fmt.Fprintf(w, "State:       %s\n", emptyDash(result.State))
	fmt.Fprintf(w, "ID:          %s\n", emptyDash(result.ID))
	fmt.Fprintf(w, "Event:       %s\n", emptyDash(result.EventType))
	fmt.Fprintf(w, "Instance:    %s\n", emptyDash(result.Instance))
	fmt.Fprintf(w, "Instance ID: %s\n", emptyDash(result.InstanceID))
	fmt.Fprintf(w, "Job:         %s\n", emptyDash(result.Job))
	fmt.Fprintf(w, "Restore:     %s\n", emptyDash(result.RestorePath))
	fmt.Fprintf(w, "Restorable:  %s\n", queueQuarantineRestorableText(result.Restorable))
	fmt.Fprintf(w, "Size:        %d\n", result.Size)
	if !result.ModTime.IsZero() {
		fmt.Fprintf(w, "Modified:    %s\n", result.ModTime.Format(time.RFC3339))
	}
	if result.Problem != "" {
		fmt.Fprintf(w, "Problem:     %s\n", result.Problem)
	}
	if actions := queueQuarantineShowActions(result); len(actions) > 0 {
		fmt.Fprintln(w, "Actions:")
		for _, action := range actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	if result.QueueItem != nil && len(result.QueueItem.Payload) > 0 {
		body, _ := json.MarshalIndent(result.QueueItem.Payload, "", "  ")
		fmt.Fprintf(w, "Payload:\n%s\n", string(body))
	}
	return nil
}

func renderQueueQuarantineCommands(w io.Writer, result queueQuarantineShowResult, scope operatorCommandScope) error {
	return renderOperatorActionCommands(w, queueQuarantineShowActions(result), scope)
}

type queueQuarantineActionResolver func(queueQuarantineItem) []string

func renderQueueQuarantineListCommands(w io.Writer, items []queueQuarantineItem, actions queueQuarantineActionResolver, scope operatorCommandScope) error {
	return renderQuarantineListCommands(w, items, actions, queueQuarantineItemActions, scope)
}

func queueQuarantineItemActions(item queueQuarantineItem) []string {
	return queueQuarantineShowActions(queueQuarantineShowResult{queueQuarantineItem: item})
}

func scopedQueueQuarantineActionResolver(scopeJob, pipeline, team string) queueQuarantineActionResolver {
	return func(item queueQuarantineItem) []string {
		return queueQuarantineShowActions(queueQuarantineShowResult{
			queueQuarantineItem: item,
			ScopeJob:            strings.TrimSpace(scopeJob),
			Pipeline:            strings.TrimSpace(pipeline),
			Team:                strings.TrimSpace(team),
		})
	}
}

func queueQuarantineShowActions(result queueQuarantineShowResult) []string {
	return quarantineShowActions("queue", result.Path, result.Restorable, result.ScopeJob, result.Pipeline, result.Team)
}

func queueQuarantineDropResultsHaveDryRunAction(results []queueQuarantineDropResult, action string) bool {
	return quarantineResultsHaveDryRunAction(results, action, func(result queueQuarantineDropResult, action string) bool {
		return result.DryRun && result.Action == action
	})
}

func renderQueueQuarantineDrop(w io.Writer, results []queueQuarantineDropResult, jsonOut bool, tmpl *template.Template) error {
	return renderQuarantineResults(w, results, jsonOut, tmpl, "(no quarantined queue files matched)", func(w io.Writer, result queueQuarantineDropResult) {
		switch result.Action {
		case "would_drop":
			fmt.Fprintf(w, "Would drop %s\n", result.Path)
		default:
			fmt.Fprintf(w, "Dropped %s\n", result.Path)
		}
	})
}
