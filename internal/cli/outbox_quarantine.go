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

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

const outboxQuarantineDir = "quarantine"

const outboxQuarantineSortFlagHelp = "Sort rows by path, state, id, type, source, job, created, updated, modified, restorable, or size."

type outboxQuarantineItem struct {
	Path        string    `json:"path"`
	State       string    `json:"state,omitempty"`
	ID          string    `json:"id,omitempty"`
	Type        string    `json:"type,omitempty"`
	Source      string    `json:"source,omitempty"`
	Job         string    `json:"job,omitempty"`
	Instance    string    `json:"instance,omitempty"`
	Target      string    `json:"target,omitempty"`
	Agent       string    `json:"agent,omitempty"`
	RestorePath string    `json:"restore_path,omitempty"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	Restorable  bool      `json:"restorable"`
	Problem     string    `json:"problem,omitempty"`
}

type outboxQuarantineSummary struct {
	Quarantined  int            `json:"quarantined"`
	Restorable   int            `json:"restorable,omitempty"`
	Unrestorable int            `json:"unrestorable,omitempty"`
	States       map[string]int `json:"states,omitempty"`
	Types        map[string]int `json:"types,omitempty"`
	Sources      map[string]int `json:"sources,omitempty"`
	Jobs         map[string]int `json:"jobs,omitempty"`
}

type outboxQuarantineRestoreResult struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	State       string `json:"state,omitempty"`
	ID          string `json:"id,omitempty"`
	Action      string `json:"action"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Overwrite   bool   `json:"overwrite,omitempty"`
}

func summarizeOutboxQuarantineItems(items []outboxQuarantineItem) outboxQuarantineSummary {
	summary := outboxQuarantineSummary{
		States:  map[string]int{},
		Types:   map[string]int{},
		Sources: map[string]int{},
		Jobs:    map[string]int{},
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
		if strings.TrimSpace(item.Type) != "" {
			summary.Types[item.Type]++
		}
		if strings.TrimSpace(item.Source) != "" {
			summary.Sources[item.Source]++
		}
		if job := normalizeOutboxJob(item.Job); job != "" {
			summary.Jobs[job]++
		}
	}
	return summary
}

func outboxQuarantineSummaryLine(summary outboxQuarantineSummary) string {
	return fmt.Sprintf("outbox quarantine: quarantined=%d restorable=%d unrestorable=%d",
		summary.Quarantined,
		summary.Restorable,
		summary.Unrestorable)
}

type outboxQuarantineDropResult struct {
	Path       string `json:"path"`
	State      string `json:"state,omitempty"`
	ID         string `json:"id,omitempty"`
	Restorable bool   `json:"restorable"`
	Action     string `json:"action"`
	DryRun     bool   `json:"dry_run,omitempty"`
	Dropped    bool   `json:"dropped,omitempty"`
}

type outboxQuarantineShowResult struct {
	outboxQuarantineItem
	Team       string             `json:"team,omitempty"`
	Pipeline   string             `json:"pipeline,omitempty"`
	ScopeJob   string             `json:"scope_job,omitempty"`
	OutboxItem *daemon.OutboxItem `json:"outbox_item,omitempty"`
}

var outboxQuarantineSortModes = []string{
	"path",
	"state",
	"id",
	"type",
	"source",
	"job",
	"created",
	"updated",
	"modified",
	"restorable",
	"size",
}

var outboxQuarantineSortLessers = map[string]quarantineSortLess[outboxQuarantineItem]{
	"state":      quarantineRankedStringLess(func(item outboxQuarantineItem) string { return item.State }, outboxStateRank),
	"id":         quarantineStringLess(func(item outboxQuarantineItem) string { return item.ID }),
	"type":       quarantineStringLess(func(item outboxQuarantineItem) string { return item.Type }),
	"source":     quarantineStringLess(func(item outboxQuarantineItem) string { return item.Source }),
	"job":        quarantineStringLess(func(item outboxQuarantineItem) string { return item.Job }),
	"created":    quarantineTimeDescLess(func(item outboxQuarantineItem) time.Time { return item.CreatedAt }),
	"updated":    quarantineTimeDescLess(func(item outboxQuarantineItem) time.Time { return item.UpdatedAt }),
	"modified":   quarantineTimeDescLess(func(item outboxQuarantineItem) time.Time { return item.ModTime }),
	"restorable": quarantineBoolTrueFirstLess(func(item outboxQuarantineItem) bool { return item.Restorable }),
	"size":       quarantineInt64DescLess(func(item outboxQuarantineItem) int64 { return item.Size }),
}

var outboxQuarantineListColumns = []quarantineListColumn[outboxQuarantineItem]{
	{Header: "PATH", Value: func(item outboxQuarantineItem) string { return item.Path }},
	{Header: "STATE", Value: func(item outboxQuarantineItem) string { return emptyDash(item.State) }},
	{Header: "ID", Value: func(item outboxQuarantineItem) string { return emptyDash(item.ID) }},
	{Header: "TYPE", Value: func(item outboxQuarantineItem) string { return emptyDash(item.Type) }},
	{Header: "SOURCE", Value: func(item outboxQuarantineItem) string { return emptyDash(item.Source) }},
	{Header: "JOB", Value: func(item outboxQuarantineItem) string { return emptyDash(item.Job) }},
	{Header: "RESTORABLE", Value: func(item outboxQuarantineItem) string { return outboxQuarantineRestorableText(item.Restorable) }},
	{Header: "PROBLEM", Value: func(item outboxQuarantineItem) string { return emptyDash(item.Problem) }},
}

var outboxQuarantineRestoreConfig = quarantineRestoreConfig[outboxQuarantineItem, outboxQuarantineRestoreResult]{
	Root:        daemon.OutboxRoot,
	Normalize:   normalizeOutboxQuarantinePath,
	Inspect:     inspectOutboxQuarantineFile,
	SafePath:    outboxDoctorSafeOutboxPath,
	Path:        func(item outboxQuarantineItem) string { return item.Path },
	RestorePath: func(item outboxQuarantineItem) string { return item.RestorePath },
	Restorable:  func(item outboxQuarantineItem) bool { return item.Restorable },
	Problem:     func(item outboxQuarantineItem) string { return item.Problem },
	NewResult: func(item outboxQuarantineItem, dryRun, force bool) outboxQuarantineRestoreResult {
		return outboxQuarantineRestoreResult{
			Path:        item.Path,
			Destination: item.RestorePath,
			State:       item.State,
			ID:          item.ID,
			Action:      "would_restore",
			DryRun:      dryRun,
			Overwrite:   force,
		}
	},
	MarkRestored: func(result *outboxQuarantineRestoreResult) {
		result.Action = "restored"
		result.DryRun = false
	},
	PruneAfterRestore: true,
	Prune:             pruneEmptyOutboxQuarantineDirs,
}

var outboxQuarantineDropConfig = quarantineDropConfig[outboxQuarantineItem, outboxQuarantineDropResult]{
	Root:      daemon.OutboxRoot,
	Normalize: normalizeOutboxQuarantinePath,
	Inspect:   inspectOutboxQuarantineFile,
	SafePath:  outboxDoctorSafeOutboxPath,
	Path:      func(item outboxQuarantineItem) string { return item.Path },
	NewResult: func(item outboxQuarantineItem, dryRun bool) outboxQuarantineDropResult {
		return outboxQuarantineDropResult{
			Path:       item.Path,
			State:      item.State,
			ID:         item.ID,
			Restorable: item.Restorable,
			Action:     "would_drop",
			DryRun:     dryRun,
		}
	},
	MarkDropped: func(result *outboxQuarantineDropResult) {
		result.Action = "dropped"
		result.Dropped = true
		result.DryRun = false
	},
	Prune: pruneEmptyOutboxQuarantineDirs,
}

func newOutboxQuarantineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quarantine",
		Short: "Inspect, restore, and drop quarantined outbox files.",
		Long:  "Inspect outbox files moved under `.agent_team/outbox/quarantine/`, restore validated entries to the active outbox, or explicitly drop preserved files.",
	}
	cmd.AddCommand(newOutboxQuarantineLsCmd())
	cmd.AddCommand(newOutboxQuarantineShowCmd())
	cmd.AddCommand(newOutboxQuarantineRestoreCmd())
	cmd.AddCommand(newOutboxQuarantineDropCmd())
	return cmd
}

func newOutboxQuarantineLsCmd() *cobra.Command {
	var (
		target       string
		stateFilter  string
		types        []string
		sources      []string
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
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List quarantined outbox files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: --limit must be >= 0.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: --commands cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || limit > 0) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxQuarantineSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team outbox quarantine ls", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			items, err := listOutboxQuarantine(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine ls: %v\n", err)
				return exitErr(1)
			}
			items = filterOutboxQuarantineItems(items, filters)
			items = filterOutboxQuarantineRestorable(items, restorable, unrestorable)
			if summary {
				return renderOutboxQuarantineSummary(cmd.OutOrStdout(), summarizeOutboxQuarantineItems(items), jsonOut)
			}
			items = prepareOutboxQuarantineItems(items, sortMode, limit)
			if commands {
				return renderOutboxQuarantineListCommands(cmd.OutOrStdout(), items, nil, operatorCommandScopeFromCommand(cmd, target, "target"))
			}
			return renderOutboxQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined files that cannot be restored.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", outboxQuarantineSortFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate quarantined outbox-file counts instead of rows.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from the visible quarantined outbox files, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit quarantined outbox files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each quarantined outbox file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	return cmd
}

func newOutboxQuarantineShowCmd() *cobra.Command {
	var (
		target   string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <quarantine-path>",
		Short: "Show one quarantined outbox file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine show: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine show: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team outbox quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := showOutboxQuarantine(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine show: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderOutboxQuarantineCommands(cmd.OutOrStdout(), result, operatorCommandScopeFromCommand(cmd, target, "target"))
			}
			return renderOutboxQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the quarantined outbox file as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the quarantined outbox file with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newOutboxQuarantineRestoreCmd() *cobra.Command {
	var (
		target      string
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
		Use:   "restore [quarantine-path]",
		Short: "Restore validated quarantined outbox files.",
		Long:  "Restore one validated quarantined outbox file by path, or restore a filtered batch of restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team outbox quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: --limit must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			scope := operatorCommandScopeFromCommand(cmd, target, "target")
			if restoreAll {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: --all cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: %v\n", err)
					return exitErr(2)
				}
				results, err := restoreOutboxQuarantineAll(teamDir, dryRun, force, filters, sortMode, limit)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxQuarantineRestoreResultsHaveDryRunAction(results, "would_restore"), outboxApplyCommandOptions{
						BaseArgs: []string{"agent-team", "outbox", "quarantine", "restore"},
						Repo:     scope.Repo,
						RepoSet:  scope.Set,
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
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: requires one path unless --all is set.")
				return exitErr(2)
			}
			if !outboxQuarantineFiltersEmpty(filters) || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			result, err := restoreOutboxQuarantine(teamDir, args[0], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine restore: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_restore", outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "outbox", "quarantine", "restore", result.Path},
					Repo:     scope.Repo,
					RepoSet:  scope.Set,
					Force:    force,
				})
			}
			return renderOutboxQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active outbox file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, restore at most this many matching quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching restore apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newOutboxQuarantineDropCmd() *cobra.Command {
	var (
		target       string
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
		Use:   "drop [quarantine-path]",
		Short: "Drop quarantined outbox files after inspection.",
		Long:  "Drop one quarantined outbox file by path, or drop a filtered batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: --limit must be >= 0.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseOutboxQuarantineCommandFormat(cmd, "agent-team outbox quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			scope := operatorCommandScopeFromCommand(cmd, target, "target")
			if dropAll {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: --all cannot be combined with a path.")
					return exitErr(2)
				}
				sortMode, err := parseOutboxQuarantineSort(sortBy)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: %v\n", err)
					return exitErr(2)
				}
				results, err := dropOutboxQuarantineAll(teamDir, dryRun, olderThan, restorable, unrestorable, filters, sortMode, limit, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderOutboxApplyCommand(cmd.OutOrStdout(), outboxQuarantineDropResultsHaveDryRunAction(results, "would_drop"), outboxApplyCommandOptions{
						BaseArgs:     []string{"agent-team", "outbox", "quarantine", "drop"},
						Repo:         scope.Repo,
						RepoSet:      scope.Set,
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
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: requires one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !outboxQuarantineFiltersEmpty(filters) || cmd.Flags().Changed("sort") || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: filters require --all; --sort requires --all; --limit requires --all.")
				return exitErr(2)
			}
			result, err := dropOutboxQuarantine(teamDir, args[0], dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox quarantine drop: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderOutboxApplyCommand(cmd.OutOrStdout(), result.DryRun && result.Action == "would_drop", outboxApplyCommandOptions{
					BaseArgs: []string{"agent-team", "outbox", "quarantine", "drop", result.Path},
					Repo:     scope.Repo,
					RepoSet:  scope.Set,
				})
			}
			return renderOutboxQuarantineDrop(cmd.OutOrStdout(), []outboxQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "With --all, filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "With --all, sort matching quarantined files before limiting: path, state, id, type, source, job, created, updated, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching quarantined files; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching drop apply command when the preview has actionable work. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func listOutboxQuarantine(teamDir string) ([]outboxQuarantineItem, error) {
	outboxRoot := daemon.OutboxRoot(teamDir)
	return listQuarantineItems(outboxRoot, outboxQuarantineDir, ".json", inspectOutboxQuarantineFile, func(item outboxQuarantineItem) string {
		return item.Path
	})
}

func inspectOutboxQuarantineFile(outboxRoot, rel string) (outboxQuarantineItem, error) {
	source, err := outboxDoctorSafeOutboxPath(outboxRoot, rel)
	if err != nil {
		return outboxQuarantineItem{}, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return outboxQuarantineItem{}, err
	}
	item := outboxQuarantineItem{
		Path:    filepath.Clean(rel),
		State:   outboxQuarantineState(rel),
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
	var raw daemon.OutboxItem
	if err := json.Unmarshal(body, &raw); err != nil {
		item.Problem = fmt.Sprintf("invalid JSON: %v", err)
		return item, nil
	}
	idFromPath := strings.TrimSuffix(filepath.Base(item.Path), ".json")
	if strings.TrimSpace(raw.ID) == "" {
		raw.ID = idFromPath
	}
	item.ID = raw.ID
	item.Type = raw.Type
	item.Source = raw.Source
	item.Job = outboxItemJobFromPayload(raw.Payload)
	item.Instance = outboxPayloadString(raw.Payload, "name")
	if item.Instance == "" {
		item.Instance = outboxPayloadString(raw.Payload, "instance")
	}
	item.Target = outboxPayloadString(raw.Payload, "target")
	item.Agent = outboxPayloadString(raw.Payload, "agent")
	item.CreatedAt = raw.CreatedAt.UTC()
	item.UpdatedAt = raw.UpdatedAt.UTC()
	if err := validateOutboxQuarantineRestore(raw, item.State, idFromPath); err != nil {
		item.Problem = err.Error()
		return item, nil
	}
	item.Restorable = true
	return item, nil
}

func outboxQuarantineState(rel string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) < 4 || parts[0] != outboxQuarantineDir {
		return ""
	}
	switch parts[2] {
	case daemon.OutboxStatePending, daemon.OutboxStateProcessed, daemon.OutboxStateFailed:
		return parts[2]
	default:
		return ""
	}
}

func filterOutboxQuarantineItems(items []outboxQuarantineItem, filters outboxListFilters) []outboxQuarantineItem {
	if outboxQuarantineFiltersEmpty(filters) {
		return items
	}
	out := make([]outboxQuarantineItem, 0, len(items))
	for _, item := range items {
		if filters.State != "" && item.State != filters.State {
			continue
		}
		if len(filters.Types) > 0 && !filters.Types[item.Type] {
			continue
		}
		if len(filters.Sources) > 0 && !filters.Sources[item.Source] {
			continue
		}
		if len(filters.Jobs) > 0 && !filters.Jobs[normalizeOutboxJob(item.Job)] {
			continue
		}
		out = append(out, item)
	}
	return out
}

func outboxQuarantineFiltersEmpty(filters outboxListFilters) bool {
	return filters.State == "" && len(filters.Types) == 0 && len(filters.Sources) == 0 && len(filters.Jobs) == 0
}

func filterOutboxQuarantineRestorable(items []outboxQuarantineItem, restorableOnly, unrestorableOnly bool) []outboxQuarantineItem {
	return filterQuarantineRestorable(items, restorableOnly, unrestorableOnly, func(item outboxQuarantineItem) bool {
		return item.Restorable
	})
}

func validateOutboxQuarantineRestore(item daemon.OutboxItem, state, idFromPath string) error {
	switch state {
	case daemon.OutboxStatePending, daemon.OutboxStateProcessed, daemon.OutboxStateFailed:
	default:
		return fmt.Errorf("outbox state is required in quarantine path")
	}
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if err := validateOutboxDoctorID(item.ID); err != nil {
		return fmt.Errorf("id %q invalid: %v", item.ID, err)
	}
	if item.ID != idFromPath {
		return fmt.Errorf("id %q does not match filename id %q", item.ID, idFromPath)
	}
	storedState := strings.TrimSpace(item.State)
	if storedState != "" && storedState != state {
		return fmt.Errorf("stored state %q does not match quarantine path state %q", storedState, state)
	}
	if strings.TrimSpace(item.Type) == "" {
		return fmt.Errorf("type is required")
	}
	if item.CreatedAt.IsZero() {
		return fmt.Errorf("created_at is required")
	}
	if item.UpdatedAt.IsZero() {
		return fmt.Errorf("updated_at is required")
	}
	return nil
}

func showOutboxQuarantine(teamDir, rawPath string) (outboxQuarantineShowResult, error) {
	outboxRoot := daemon.OutboxRoot(teamDir)
	rel, err := normalizeOutboxQuarantinePath(rawPath)
	if err != nil {
		return outboxQuarantineShowResult{}, err
	}
	item, err := inspectOutboxQuarantineFile(outboxRoot, rel)
	if err != nil {
		return outboxQuarantineShowResult{}, err
	}
	result := outboxQuarantineShowResult{outboxQuarantineItem: item}
	source, err := outboxDoctorSafeOutboxPath(outboxRoot, item.Path)
	if err != nil {
		return result, nil
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return result, nil
	}
	var raw daemon.OutboxItem
	if err := json.Unmarshal(body, &raw); err != nil {
		return result, nil
	}
	if strings.TrimSpace(raw.ID) == "" {
		raw.ID = strings.TrimSuffix(filepath.Base(item.Path), ".json")
	}
	raw.State = item.State
	result.OutboxItem = &raw
	return result, nil
}

func restoreOutboxQuarantine(teamDir, rawPath string, dryRun, force bool) (outboxQuarantineRestoreResult, error) {
	return restoreQuarantineItem(teamDir, rawPath, dryRun, force, outboxQuarantineRestoreConfig)
}

func restoreOutboxQuarantineAll(teamDir string, dryRun, force bool, filters outboxListFilters, sortMode string, limit int) ([]outboxQuarantineRestoreResult, error) {
	items, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = filterOutboxQuarantineItems(items, filters)
	items = filterOutboxQuarantineRestorable(items, true, false)
	return restoreOutboxQuarantineItems(teamDir, items, dryRun, force, sortMode, limit)
}

func restoreOutboxQuarantineItems(teamDir string, items []outboxQuarantineItem, dryRun, force bool, sortMode string, limit int) ([]outboxQuarantineRestoreResult, error) {
	return restoreQuarantineItemBatch(teamDir, items, dryRun, force, sortMode, limit, prepareOutboxQuarantineItems, func(item outboxQuarantineItem) string {
		return item.Path
	}, restoreOutboxQuarantine)
}

func dropOutboxQuarantine(teamDir, rawPath string, dryRun bool) (outboxQuarantineDropResult, error) {
	return dropQuarantinePath(teamDir, rawPath, dryRun, outboxQuarantineDropConfig)
}

func dropOutboxQuarantineAll(teamDir string, dryRun bool, olderThan time.Duration, restorable, unrestorable bool, filters outboxListFilters, sortMode string, limit int, now time.Time) ([]outboxQuarantineDropResult, error) {
	items, err := listOutboxQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	items = filterOutboxQuarantineItems(items, filters)
	items = filterOutboxQuarantineRestorable(items, restorable, unrestorable)
	return dropOutboxQuarantineItems(teamDir, items, dryRun, olderThan, sortMode, limit, now)
}

func dropOutboxQuarantineItems(teamDir string, items []outboxQuarantineItem, dryRun bool, olderThan time.Duration, sortMode string, limit int, now time.Time) ([]outboxQuarantineDropResult, error) {
	outboxRoot := daemon.OutboxRoot(teamDir)
	return dropQuarantineItemBatch(outboxRoot, items, dryRun, olderThan, sortMode, limit, now, sortOutboxQuarantineItems, func(item outboxQuarantineItem) time.Time {
		return item.ModTime
	}, dropOutboxQuarantineItem)
}

func prepareOutboxQuarantineItems(items []outboxQuarantineItem, sortMode string, limit int) []outboxQuarantineItem {
	return prepareQuarantineItems(items, sortMode, limit, sortOutboxQuarantineItems)
}

func parseOutboxQuarantineSort(raw string) (string, error) {
	return parseQuarantineSort(raw, outboxQuarantineSortModes, "--sort must be path, state, id, type, source, job, created, updated, modified, restorable, or size")
}

func sortOutboxQuarantineItems(items []outboxQuarantineItem, sortMode string) {
	sortQuarantineItems(items, sortMode, func(item outboxQuarantineItem) string {
		return item.Path
	}, outboxQuarantineSortLessers)
}

func dropOutboxQuarantineItem(outboxRoot string, item outboxQuarantineItem, dryRun bool) (outboxQuarantineDropResult, error) {
	return dropQuarantineItem(outboxRoot, item, dryRun, outboxQuarantineDropConfig)
}

func pruneEmptyOutboxQuarantineDirs(outboxRoot, dir string) {
	pruneEmptyQuarantineDirs(outboxRoot, dir, outboxQuarantineDir)
}

func normalizeOutboxQuarantinePath(raw string) (string, error) {
	return normalizeQuarantinePath(raw, outboxQuarantineDir, ".json", "quarantine path must look like quarantine/<timestamp>/pending/<file>.json, quarantine/<timestamp>/processed/<file>.json, or quarantine/<timestamp>/failed/<file>.json", outboxQuarantineState)
}

func parseOutboxQuarantineCommandFormat(cmd *cobra.Command, command, format string, jsonOut bool) (*template.Template, error) {
	return parseQuarantineCommandFormat(cmd, command, format, "outbox-quarantine-format", jsonOut)
}

func parseOutboxQuarantineFormat(format string) (*template.Template, error) {
	return parseQuarantineFormat(format, "outbox-quarantine-format")
}

func renderOutboxQuarantineList(w io.Writer, items []outboxQuarantineItem, jsonOut bool, tmpl *template.Template) error {
	return renderQuarantineList(w, items, jsonOut, tmpl, "(no quarantined outbox files)", outboxQuarantineListColumns)
}

func renderOutboxQuarantineSummary(w io.Writer, summary outboxQuarantineSummary, jsonOut bool) error {
	return renderQuarantineSummary(w, summary, jsonOut, outboxQuarantineSummaryLine)
}

func outboxQuarantineRestorableText(restorable bool) string {
	return quarantineRestorableText(restorable)
}

func renderOutboxQuarantineRestore(w io.Writer, result outboxQuarantineRestoreResult, jsonOut bool, tmpl *template.Template) error {
	return renderQuarantineResult(w, result, jsonOut, tmpl, renderOutboxQuarantineRestoreLine)
}

func renderOutboxQuarantineRestoreMany(w io.Writer, results []outboxQuarantineRestoreResult, jsonOut bool, tmpl *template.Template) error {
	return renderQuarantineResults(w, results, jsonOut, tmpl, "(no restorable quarantined outbox files matched)", renderOutboxQuarantineRestoreLine)
}

func renderOutboxQuarantineRestoreLine(w io.Writer, result outboxQuarantineRestoreResult) {
	switch result.Action {
	case "would_restore":
		fmt.Fprintf(w, "Would restore %s -> %s\n", result.Path, result.Destination)
	default:
		fmt.Fprintf(w, "Restored %s -> %s\n", result.Path, result.Destination)
	}
}

func outboxQuarantineRestoreResultsHaveDryRunAction(results []outboxQuarantineRestoreResult, action string) bool {
	return quarantineResultsHaveDryRunAction(results, action, func(result outboxQuarantineRestoreResult, action string) bool {
		return result.DryRun && result.Action == action
	})
}

func renderOutboxQuarantineShow(w io.Writer, result outboxQuarantineShowResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderQuarantineTemplate(w, result, tmpl)
	}
	fmt.Fprintf(w, "Path:        %s\n", result.Path)
	fmt.Fprintf(w, "State:       %s\n", emptyDash(result.State))
	fmt.Fprintf(w, "ID:          %s\n", emptyDash(result.ID))
	fmt.Fprintf(w, "Type:        %s\n", emptyDash(result.Type))
	fmt.Fprintf(w, "Source:      %s\n", emptyDash(result.Source))
	fmt.Fprintf(w, "Job:         %s\n", emptyDash(result.Job))
	fmt.Fprintf(w, "Restore:     %s\n", emptyDash(result.RestorePath))
	fmt.Fprintf(w, "Restorable:  %s\n", outboxQuarantineRestorableText(result.Restorable))
	fmt.Fprintf(w, "Size:        %d\n", result.Size)
	if !result.ModTime.IsZero() {
		fmt.Fprintf(w, "Modified:    %s\n", result.ModTime.Format(time.RFC3339))
	}
	if result.Problem != "" {
		fmt.Fprintf(w, "Problem:     %s\n", result.Problem)
	}
	if actions := outboxQuarantineShowActions(result); len(actions) > 0 {
		fmt.Fprintln(w, "Actions:")
		for _, action := range actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	if result.OutboxItem != nil && len(result.OutboxItem.Payload) > 0 {
		body, _ := json.MarshalIndent(result.OutboxItem.Payload, "", "  ")
		fmt.Fprintf(w, "Payload:\n%s\n", string(body))
	}
	return nil
}

func renderOutboxQuarantineCommands(w io.Writer, result outboxQuarantineShowResult, scope operatorCommandScope) error {
	return renderOperatorActionCommands(w, outboxQuarantineShowActions(result), scope)
}

type outboxQuarantineActionResolver func(outboxQuarantineItem) []string

func renderOutboxQuarantineListCommands(w io.Writer, items []outboxQuarantineItem, actions outboxQuarantineActionResolver, scope operatorCommandScope) error {
	return renderQuarantineListCommands(w, items, actions, outboxQuarantineItemActions, scope)
}

func outboxQuarantineItemActions(item outboxQuarantineItem) []string {
	return outboxQuarantineShowActions(outboxQuarantineShowResult{outboxQuarantineItem: item})
}

func scopedOutboxQuarantineActionResolver(scopeJob, pipeline, team string) outboxQuarantineActionResolver {
	return func(item outboxQuarantineItem) []string {
		return outboxQuarantineShowActions(outboxQuarantineShowResult{
			outboxQuarantineItem: item,
			ScopeJob:             strings.TrimSpace(scopeJob),
			Pipeline:             strings.TrimSpace(pipeline),
			Team:                 strings.TrimSpace(team),
		})
	}
}

func outboxQuarantineShowActions(result outboxQuarantineShowResult) []string {
	return quarantineShowActions("outbox", result.Path, result.Restorable, result.ScopeJob, result.Pipeline, result.Team)
}

func outboxQuarantineDropResultsHaveDryRunAction(results []outboxQuarantineDropResult, action string) bool {
	return quarantineResultsHaveDryRunAction(results, action, func(result outboxQuarantineDropResult, action string) bool {
		return result.DryRun && result.Action == action
	})
}

func renderOutboxQuarantineDrop(w io.Writer, results []outboxQuarantineDropResult, jsonOut bool, tmpl *template.Template) error {
	return renderQuarantineResults(w, results, jsonOut, tmpl, "(no quarantined outbox files matched)", func(w io.Writer, result outboxQuarantineDropResult) {
		switch result.Action {
		case "would_drop":
			fmt.Fprintf(w, "Would drop %s\n", result.Path)
		default:
			fmt.Fprintf(w, "Dropped %s\n", result.Path)
		}
	})
}
