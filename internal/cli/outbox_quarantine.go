package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
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
				return renderOutboxQuarantineCommands(cmd.OutOrStdout(), result)
			}
			return renderOutboxQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the quarantined outbox file as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands.")
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
						BaseArgs:  []string{"agent-team", "outbox", "quarantine", "restore"},
						Target:    target,
						TargetSet: cmd.Flags().Changed("target"),
						All:       true,
						Force:     force,
						State:     stateFilter,
						StateSet:  cmd.Flags().Changed("state"),
						Types:     types,
						Sources:   sources,
						Jobs:      jobs,
						Sort:      sortBy,
						SortSet:   cmd.Flags().Changed("sort"),
						Limit:     limit,
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
					BaseArgs:  []string{"agent-team", "outbox", "quarantine", "restore", result.Path},
					Target:    target,
					TargetSet: cmd.Flags().Changed("target"),
					Force:     force,
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
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching restore apply command when the preview has actionable work.")
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
						Target:       target,
						TargetSet:    cmd.Flags().Changed("target"),
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
					BaseArgs:  []string{"agent-team", "outbox", "quarantine", "drop", result.Path},
					Target:    target,
					TargetSet: cmd.Flags().Changed("target"),
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
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching drop apply command when the preview has actionable work.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func listOutboxQuarantine(teamDir string) ([]outboxQuarantineItem, error) {
	outboxRoot := daemon.OutboxRoot(teamDir)
	root := filepath.Join(outboxRoot, outboxQuarantineDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	var items []outboxQuarantineItem
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		rel, err := filepath.Rel(outboxRoot, path)
		if err != nil {
			return err
		}
		item, err := inspectOutboxQuarantineFile(outboxRoot, rel)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Path < items[j].Path
	})
	return items, nil
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
	if !restorableOnly && !unrestorableOnly {
		return items
	}
	out := make([]outboxQuarantineItem, 0, len(items))
	for _, item := range items {
		if restorableOnly && !item.Restorable {
			continue
		}
		if unrestorableOnly && item.Restorable {
			continue
		}
		out = append(out, item)
	}
	return out
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
	outboxRoot := daemon.OutboxRoot(teamDir)
	rel, err := normalizeOutboxQuarantinePath(rawPath)
	if err != nil {
		return outboxQuarantineRestoreResult{}, err
	}
	item, err := inspectOutboxQuarantineFile(outboxRoot, rel)
	if err != nil {
		return outboxQuarantineRestoreResult{}, err
	}
	if !item.Restorable {
		return outboxQuarantineRestoreResult{}, fmt.Errorf("%s is not restorable: %s", item.Path, item.Problem)
	}
	source, err := outboxDoctorSafeOutboxPath(outboxRoot, item.Path)
	if err != nil {
		return outboxQuarantineRestoreResult{}, err
	}
	destination, err := outboxDoctorSafeOutboxPath(outboxRoot, item.RestorePath)
	if err != nil {
		return outboxQuarantineRestoreResult{}, err
	}
	if _, err := os.Stat(destination); err == nil && !force {
		return outboxQuarantineRestoreResult{}, fmt.Errorf("%s already exists; pass --force to overwrite it", item.RestorePath)
	} else if err != nil && !os.IsNotExist(err) {
		return outboxQuarantineRestoreResult{}, err
	}
	result := outboxQuarantineRestoreResult{
		Path:        item.Path,
		Destination: item.RestorePath,
		State:       item.State,
		ID:          item.ID,
		Action:      "would_restore",
		DryRun:      dryRun,
		Overwrite:   force,
	}
	if dryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return result, err
	}
	if force {
		_ = os.Remove(destination)
	}
	if err := os.Rename(source, destination); err != nil {
		return result, err
	}
	result.Action = "restored"
	result.DryRun = false
	pruneEmptyOutboxQuarantineDirs(outboxRoot, filepath.Dir(source))
	return result, nil
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
	items = prepareOutboxQuarantineItems(items, sortMode, limit)
	results := make([]outboxQuarantineRestoreResult, 0, len(items))
	for _, item := range items {
		result, err := restoreOutboxQuarantine(teamDir, item.Path, dryRun, force)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func dropOutboxQuarantine(teamDir, rawPath string, dryRun bool) (outboxQuarantineDropResult, error) {
	outboxRoot := daemon.OutboxRoot(teamDir)
	rel, err := normalizeOutboxQuarantinePath(rawPath)
	if err != nil {
		return outboxQuarantineDropResult{}, err
	}
	item, err := inspectOutboxQuarantineFile(outboxRoot, rel)
	if err != nil {
		return outboxQuarantineDropResult{}, err
	}
	return dropOutboxQuarantineItem(outboxRoot, item, dryRun)
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
	sortOutboxQuarantineItems(items, sortMode)
	matches := make([]outboxQuarantineItem, 0, len(items))
	for _, item := range items {
		if olderThan > 0 && item.ModTime.After(now.Add(-olderThan)) {
			continue
		}
		matches = append(matches, item)
		if limit > 0 && len(matches) >= limit {
			break
		}
	}
	results := make([]outboxQuarantineDropResult, 0, len(matches))
	for _, item := range matches {
		result, err := dropOutboxQuarantineItem(outboxRoot, item, dryRun)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func prepareOutboxQuarantineItems(items []outboxQuarantineItem, sortMode string, limit int) []outboxQuarantineItem {
	sortOutboxQuarantineItems(items, sortMode)
	if limit <= 0 || limit >= len(items) {
		return items
	}
	return items[:limit]
}

func parseOutboxQuarantineSort(raw string) (string, error) {
	sortMode := strings.ToLower(strings.TrimSpace(raw))
	switch sortMode {
	case "", "path", "state", "id", "type", "source", "job", "created", "updated", "modified", "restorable", "size":
		if sortMode == "" {
			return "path", nil
		}
		return sortMode, nil
	default:
		return "", fmt.Errorf("--sort must be path, state, id, type, source, job, created, updated, modified, restorable, or size")
	}
}

func sortOutboxQuarantineItems(items []outboxQuarantineItem, sortMode string) {
	sortMode = strings.ToLower(strings.TrimSpace(sortMode))
	if sortMode == "" {
		sortMode = "path"
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		switch sortMode {
		case "state":
			if left.State != right.State {
				return outboxStateRank(left.State) < outboxStateRank(right.State)
			}
		case "id":
			if left.ID != right.ID {
				return left.ID < right.ID
			}
		case "type":
			if left.Type != right.Type {
				return left.Type < right.Type
			}
		case "source":
			if left.Source != right.Source {
				return left.Source < right.Source
			}
		case "job":
			if left.Job != right.Job {
				return left.Job < right.Job
			}
		case "created":
			if !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.After(right.CreatedAt)
			}
		case "updated":
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.After(right.UpdatedAt)
			}
		case "modified":
			if !left.ModTime.Equal(right.ModTime) {
				return left.ModTime.After(right.ModTime)
			}
		case "restorable":
			if left.Restorable != right.Restorable {
				return left.Restorable && !right.Restorable
			}
		case "size":
			if left.Size != right.Size {
				return left.Size > right.Size
			}
		case "path":
			if left.Path != right.Path {
				return left.Path < right.Path
			}
		}
		return left.Path < right.Path
	})
}

func dropOutboxQuarantineItem(outboxRoot string, item outboxQuarantineItem, dryRun bool) (outboxQuarantineDropResult, error) {
	result := outboxQuarantineDropResult{
		Path:       item.Path,
		State:      item.State,
		ID:         item.ID,
		Restorable: item.Restorable,
		Action:     "would_drop",
		DryRun:     dryRun,
	}
	if dryRun {
		return result, nil
	}
	source, err := outboxDoctorSafeOutboxPath(outboxRoot, item.Path)
	if err != nil {
		return result, err
	}
	if err := os.Remove(source); err != nil {
		return result, err
	}
	pruneEmptyOutboxQuarantineDirs(outboxRoot, filepath.Dir(source))
	result.Action = "dropped"
	result.Dropped = true
	result.DryRun = false
	return result, nil
}

func pruneEmptyOutboxQuarantineDirs(outboxRoot, dir string) {
	stop := filepath.Join(outboxRoot, outboxQuarantineDir)
	for {
		if dir == "" || dir == "." || dir == stop || !strings.HasPrefix(dir, stop) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func normalizeOutboxQuarantinePath(raw string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(raw))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe quarantine path %q", raw)
	}
	slash := filepath.ToSlash(clean)
	if !strings.HasPrefix(slash, outboxQuarantineDir+"/") {
		slash = outboxQuarantineDir + "/" + slash
	}
	if outboxQuarantineState(filepath.FromSlash(slash)) == "" {
		return "", fmt.Errorf("quarantine path must look like quarantine/<timestamp>/pending/<file>.json, quarantine/<timestamp>/processed/<file>.json, or quarantine/<timestamp>/failed/<file>.json")
	}
	if !strings.HasSuffix(slash, ".json") {
		return "", fmt.Errorf("quarantine path must name a .json file")
	}
	return filepath.FromSlash(slash), nil
}

func parseOutboxQuarantineCommandFormat(cmd *cobra.Command, command, format string, jsonOut bool) (*template.Template, error) {
	if format != "" && jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", command)
		return nil, exitErr(2)
	}
	tmpl, err := parseOutboxQuarantineFormat(format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", command, err)
		return nil, exitErr(2)
	}
	return tmpl, nil
}

func parseOutboxQuarantineFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("outbox-quarantine-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderOutboxQuarantineList(w io.Writer, items []outboxQuarantineItem, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		if items == nil {
			items = []outboxQuarantineItem{}
		}
		return json.NewEncoder(w).Encode(items)
	}
	if tmpl != nil {
		for _, item := range items {
			if err := renderOutboxQuarantineTemplate(w, item, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(items) == 0 {
		fmt.Fprintln(w, "(no quarantined outbox files)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PATH\tSTATE\tID\tTYPE\tSOURCE\tJOB\tRESTORABLE\tPROBLEM")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Path,
			emptyDash(item.State),
			emptyDash(item.ID),
			emptyDash(item.Type),
			emptyDash(item.Source),
			emptyDash(item.Job),
			outboxQuarantineRestorableText(item.Restorable),
			emptyDash(item.Problem))
	}
	return tw.Flush()
}

func renderOutboxQuarantineSummary(w io.Writer, summary outboxQuarantineSummary, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	fmt.Fprintln(w, outboxQuarantineSummaryLine(summary))
	return nil
}

func outboxQuarantineRestorableText(restorable bool) string {
	if restorable {
		return "yes"
	}
	return "no"
}

func renderOutboxQuarantineRestore(w io.Writer, result outboxQuarantineRestoreResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderOutboxQuarantineTemplate(w, result, tmpl)
	}
	renderOutboxQuarantineRestoreLine(w, result)
	return nil
}

func renderOutboxQuarantineRestoreMany(w io.Writer, results []outboxQuarantineRestoreResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := renderOutboxQuarantineTemplate(w, result, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "(no restorable quarantined outbox files matched)")
		return nil
	}
	for _, result := range results {
		renderOutboxQuarantineRestoreLine(w, result)
	}
	return nil
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
	for _, result := range results {
		if result.DryRun && result.Action == action {
			return true
		}
	}
	return false
}

func renderOutboxQuarantineShow(w io.Writer, result outboxQuarantineShowResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderOutboxQuarantineTemplate(w, result, tmpl)
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

func renderOutboxQuarantineCommands(w io.Writer, result outboxQuarantineShowResult) error {
	return renderActionCommands(w, commandActionsOnly(outboxQuarantineShowActions(result)))
}

func outboxQuarantineShowActions(result outboxQuarantineShowResult) []string {
	if result.Path == "" {
		return nil
	}
	var prefix string
	if result.ScopeJob != "" {
		prefix = fmt.Sprintf("agent-team job outbox quarantine %%s %s %s", result.ScopeJob, result.Path)
	} else if result.Pipeline != "" {
		prefix = fmt.Sprintf("agent-team pipeline outbox quarantine %%s %s %s", result.Pipeline, result.Path)
	} else if result.Team != "" {
		prefix = fmt.Sprintf("agent-team team outbox quarantine %%s %s %s", result.Team, result.Path)
	} else {
		prefix = fmt.Sprintf("agent-team outbox quarantine %%s %s", result.Path)
	}
	actions := []string{}
	if result.Restorable {
		actions = append(actions, fmt.Sprintf(prefix, "restore"))
	}
	actions = append(actions, fmt.Sprintf(prefix, "drop"))
	return actions
}

func outboxQuarantineDropResultsHaveDryRunAction(results []outboxQuarantineDropResult, action string) bool {
	for _, result := range results {
		if result.DryRun && result.Action == action {
			return true
		}
	}
	return false
}

func renderOutboxQuarantineDrop(w io.Writer, results []outboxQuarantineDropResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := renderOutboxQuarantineTemplate(w, result, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "(no quarantined outbox files matched)")
		return nil
	}
	for _, result := range results {
		switch result.Action {
		case "would_drop":
			fmt.Fprintf(w, "Would drop %s\n", result.Path)
		default:
			fmt.Fprintf(w, "Dropped %s\n", result.Path)
		}
	}
	return nil
}

func renderOutboxQuarantineTemplate(w io.Writer, value any, tmpl *template.Template) error {
	if err := tmpl.Execute(w, value); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
