package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newOutboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "outbox",
		Aliases: []string{"outboxes"},
		Short:   "Inspect and control sandboxed agent outbox events.",
		Long: "Inspect and control sandboxed agent outbox events under `.agent_team/outbox/`.\n\n" +
			"Agents write outbox events when daemon socket or loopback HTTP transport is unavailable. " +
			"`agent-team tick`, `agent-team drain`, and `agent-team outbox drain` publish pending events through the daemon resolver.",
	}
	cmd.AddCommand(newOutboxLsCmd())
	cmd.AddCommand(newOutboxShowCmd())
	cmd.AddCommand(newOutboxDrainCmd())
	cmd.AddCommand(newOutboxRetryCmd())
	cmd.AddCommand(newOutboxDropCmd())
	return cmd
}

type outboxListFilters struct {
	State   string
	Types   map[string]bool
	Sources map[string]bool
	Jobs    map[string]bool
}

type outboxListOptions struct {
	Sort  string
	Limit int
}

func newOutboxLsCmd() *cobra.Command {
	var (
		target      string
		stateFilter string
		types       []string
		sources     []string
		jobs        []string
		sortBy      string
		limit       int
		summary     bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List sandboxed agent outbox events.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox ls: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if summary && (cmd.Flags().Changed("sort") || cmd.Flags().Changed("limit")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox ls: --sort and --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox ls: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseOutboxSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox ls: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox ls: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseOutboxFilters(stateFilter, types, sources, jobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if summary {
				return renderOutboxSummary(cmd.OutOrStdout(), teamDir, filters, jsonOut)
			}
			return runOutboxList(cmd.OutOrStdout(), teamDir, filters, outboxListOptions{Sort: sortMode, Limit: limit}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by outbox state: pending, processed, or failed.")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&sources, "source", nil, "Filter by source agent/instance; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringVar(&sortBy, "sort", "state", "Sort rows by state, id, type, source, job, created, updated, or error.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate outbox counts instead of rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newOutboxShowCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one outbox event.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			item, err := daemon.ReadOutboxItem(teamDir, args[0])
			if err != nil {
				return outboxReadError(args[0], err)
			}
			return renderOutboxItemResult(cmd.OutOrStdout(), item, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the outbox item as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newOutboxDrainCmd() *cobra.Command {
	var (
		target  string
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drain",
		Short: "Ask the running daemon to publish pending outbox events.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox drain: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxDrainFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox drain: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				if dryRun && errors.Is(err, errDaemonNotRunning) {
					result, previewErr := previewOutboxDrainLocal(teamDir)
					if previewErr != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox drain: %v\n", previewErr)
						return exitErr(1)
					}
					return renderOutboxDrainCommandResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
				}
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox drain: daemon is not running — start it first with `agent-team daemon start`.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox drain: %v\n", err)
				return exitErr(1)
			}
			result, err := dc.OutboxDrain(dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox drain: %v\n", err)
				return exitErr(1)
			}
			return renderOutboxDrainCommandResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview pending outbox events without publishing them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drain result with a Go template, e.g. '{{.WouldPublish}} {{.Pending}}'.")
	return cmd
}

func newOutboxRetryCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
		dryRun  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "retry <id>",
		Aliases: []string{"requeue"},
		Short:   "Move a processed or failed outbox event back to pending.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox retry: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := retryOutboxItem(teamDir, args[0], dryRun)
			if err != nil {
				return err
			}
			return renderOutboxActionResults(cmd.OutOrStdout(), []outboxActionResult{result}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the retry without moving the event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newOutboxDropCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
		dryRun  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <id>",
		Short: "Remove one outbox event.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := dropOutboxItem(teamDir, args[0], dryRun)
			if err != nil {
				return err
			}
			return renderOutboxActionResults(cmd.OutOrStdout(), []outboxActionResult{result}, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the drop without removing the event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

type outboxActionResult struct {
	ID     string             `json:"id"`
	State  string             `json:"state"`
	Type   string             `json:"type"`
	Source string             `json:"source,omitempty"`
	Job    string             `json:"job,omitempty"`
	Action string             `json:"action"`
	Item   *daemon.OutboxItem `json:"item,omitempty"`
	DryRun bool               `json:"dry_run,omitempty"`
}

type outboxSummary struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Processed int `json:"processed"`
	Failed    int `json:"failed"`
	Filtered  int `json:"filtered"`
}

func parseOutboxFilters(state string, types, sources, jobs []string) (outboxListFilters, error) {
	state = strings.TrimSpace(strings.ToLower(state))
	switch state {
	case "", daemon.OutboxStatePending, daemon.OutboxStateProcessed, daemon.OutboxStateFailed:
	default:
		return outboxListFilters{}, fmt.Errorf("--state must be pending, processed, or failed")
	}
	eventTypes, err := stringSetFilter(types, "--type", "event type")
	if err != nil {
		return outboxListFilters{}, err
	}
	sourceSet, err := stringSetFilter(sources, "--source", "source")
	if err != nil {
		return outboxListFilters{}, err
	}
	jobSet, err := jobIDSetFilter(jobs, "--job")
	if err != nil {
		return outboxListFilters{}, err
	}
	filters := outboxListFilters{
		State:   state,
		Types:   eventTypes,
		Sources: sourceSet,
		Jobs:    jobSet,
	}
	return filters, nil
}

func parseOutboxSort(sortBy string) (string, error) {
	sortBy = strings.TrimSpace(strings.ToLower(sortBy))
	if sortBy == "" {
		sortBy = "state"
	}
	switch sortBy {
	case "state", "id", "type", "source", "job", "created", "updated", "error":
		return sortBy, nil
	default:
		return "", fmt.Errorf("--sort must be state, id, type, source, job, created, updated, or error")
	}
}

func parseOutboxFormat(format string) (*template.Template, error) {
	if format == "" {
		return nil, nil
	}
	tmpl, err := template.New("outbox-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseOutboxDrainFormat(format string) (*template.Template, error) {
	if format == "" {
		return nil, nil
	}
	tmpl, err := template.New("outbox-drain-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseOutboxActionFormat(format string) (*template.Template, error) {
	if format == "" {
		return nil, nil
	}
	tmpl, err := template.New("outbox-action-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func runOutboxList(w io.Writer, teamDir string, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template) error {
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return err
	}
	return runOutboxListItems(w, items, filters, opts, jsonOut, tmpl)
}

func runOutboxListItems(w io.Writer, items []*daemon.OutboxItem, filters outboxListFilters, opts outboxListOptions, jsonOut bool, tmpl *template.Template) error {
	filtered := filterOutboxItems(items, filters)
	sortOutboxItems(filtered, opts.Sort)
	filtered = limitOutboxItems(filtered, opts.Limit)
	if jsonOut {
		return json.NewEncoder(w).Encode(filtered)
	}
	if tmpl != nil {
		return renderOutboxItemsFormat(w, filtered, tmpl)
	}
	return renderOutboxItemsTable(w, filtered)
}

func renderOutboxSummary(w io.Writer, teamDir string, filters outboxListFilters, jsonOut bool) error {
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return err
	}
	return renderOutboxSummaryForItems(w, items, filters, jsonOut)
}

func renderOutboxSummaryForItems(w io.Writer, items []*daemon.OutboxItem, filters outboxListFilters, jsonOut bool) error {
	filtered := filterOutboxItems(items, filters)
	summary := summarizeOutboxItems(items)
	summary.Filtered = len(filtered)
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	fmt.Fprintf(w, "outbox: total=%d pending=%d processed=%d failed=%d filtered=%d\n",
		summary.Total, summary.Pending, summary.Processed, summary.Failed, summary.Filtered)
	return nil
}

func summarizeOutboxItems(items []*daemon.OutboxItem) outboxSummary {
	var summary outboxSummary
	for _, item := range items {
		if item == nil {
			continue
		}
		summary.Total++
		switch item.State {
		case daemon.OutboxStatePending:
			summary.Pending++
		case daemon.OutboxStateProcessed:
			summary.Processed++
		case daemon.OutboxStateFailed:
			summary.Failed++
		}
	}
	return summary
}

func filterOutboxItems(items []*daemon.OutboxItem, filters outboxListFilters) []*daemon.OutboxItem {
	out := make([]*daemon.OutboxItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if filters.State != "" && item.State != filters.State {
			continue
		}
		if len(filters.Types) > 0 && !filters.Types[item.Type] {
			continue
		}
		if len(filters.Sources) > 0 && !filters.Sources[item.Source] {
			continue
		}
		if len(filters.Jobs) > 0 {
			job := normalizeOutboxJob(outboxItemJob(item))
			if !filters.Jobs[job] {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func outboxItemsForJobs(items []*daemon.OutboxItem, jobs []*job.Job) []*daemon.OutboxItem {
	if len(items) == 0 || len(jobs) == 0 {
		return nil
	}
	out := make([]*daemon.OutboxItem, 0, len(items))
	for _, item := range items {
		if outboxItemMatchesAnyJob(item, jobs) {
			out = append(out, item)
		}
	}
	return out
}

func sortOutboxItems(items []*daemon.OutboxItem, sortMode string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch sortMode {
		case "id":
			return a.ID < b.ID
		case "type":
			if a.Type != b.Type {
				return a.Type < b.Type
			}
		case "source":
			if a.Source != b.Source {
				return a.Source < b.Source
			}
		case "job":
			if outboxItemJob(a) != outboxItemJob(b) {
				return outboxItemJob(a) < outboxItemJob(b)
			}
		case "created":
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
		case "updated":
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.UpdatedAt.Before(b.UpdatedAt)
			}
		case "error":
			if a.LastError != b.LastError {
				return a.LastError < b.LastError
			}
		default:
			if a.State != b.State {
				return outboxStateRank(a.State) < outboxStateRank(b.State)
			}
		}
		return a.ID < b.ID
	})
}

func limitOutboxItems(items []*daemon.OutboxItem, limit int) []*daemon.OutboxItem {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func renderOutboxItemsFormat(w io.Writer, items []*daemon.OutboxItem, tmpl *template.Template) error {
	for _, item := range items {
		if err := tmpl.Execute(w, item); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func renderOutboxItemsTable(w io.Writer, items []*daemon.OutboxItem) error {
	if len(items) == 0 {
		fmt.Fprintln(w, "(no outbox events)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tTYPE\tSOURCE\tJOB\tCREATED\tUPDATED\tERROR")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.ID,
			item.State,
			item.Type,
			emptyDash(item.Source),
			emptyDash(outboxItemJob(item)),
			outboxTime(item.CreatedAt),
			outboxTime(item.UpdatedAt),
			emptyDash(item.LastError),
		)
	}
	return tw.Flush()
}

func renderOutboxItemResult(w io.Writer, item *daemon.OutboxItem, jsonOut bool, tmpl *template.Template) error {
	if item == nil {
		item = &daemon.OutboxItem{}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(item)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, item); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	fmt.Fprintf(w, "ID:          %s\n", item.ID)
	fmt.Fprintf(w, "State:       %s\n", item.State)
	fmt.Fprintf(w, "Type:        %s\n", item.Type)
	fmt.Fprintf(w, "Source:      %s\n", emptyDash(item.Source))
	fmt.Fprintf(w, "Job:         %s\n", emptyDash(outboxItemJob(item)))
	fmt.Fprintf(w, "Created:     %s\n", outboxTime(item.CreatedAt))
	fmt.Fprintf(w, "Updated:     %s\n", outboxTime(item.UpdatedAt))
	if !item.ProcessedAt.IsZero() {
		fmt.Fprintf(w, "Processed:   %s\n", outboxTime(item.ProcessedAt))
	}
	if !item.FailedAt.IsZero() {
		fmt.Fprintf(w, "Failed:      %s\n", outboxTime(item.FailedAt))
	}
	fmt.Fprintf(w, "Last error:  %s\n", emptyDash(item.LastError))
	body, err := json.MarshalIndent(item.Payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "Payload:")
	fmt.Fprintln(w, string(body))
	return nil
}

func renderOutboxDrainCommandResult(w io.Writer, result *daemon.OutboxDrainResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &daemon.OutboxDrainResult{}
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
	return renderOutboxDrainResult(w, result)
}

func renderOutboxActionResults(w io.Writer, results []outboxActionResult, jsonOut bool, tmpl *template.Template) error {
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
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tTYPE\tJOB\tACTION")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			result.ID, result.State, result.Type, emptyDash(result.Job), result.Action)
	}
	return tw.Flush()
}

func retryOutboxItem(teamDir, id string, dryRun bool) (outboxActionResult, error) {
	item, err := daemon.ReadOutboxItem(teamDir, id)
	if err != nil {
		return outboxActionResult{}, outboxReadError(id, err)
	}
	if item.State == daemon.OutboxStatePending {
		return outboxActionFromItem(item, "already_pending", dryRun), nil
	}
	action := "retried"
	if dryRun {
		action = "would_retry"
	}
	result := outboxActionFromItem(item, action, dryRun)
	if dryRun {
		return result, nil
	}
	if err := daemon.MoveOutboxItem(teamDir, item, daemon.OutboxStatePending); err != nil {
		return outboxActionResult{}, err
	}
	result.State = daemon.OutboxStatePending
	result.Item = item
	return result, nil
}

func dropOutboxItem(teamDir, id string, dryRun bool) (outboxActionResult, error) {
	item, err := daemon.ReadOutboxItem(teamDir, id)
	if err != nil {
		return outboxActionResult{}, outboxReadError(id, err)
	}
	action := "dropped"
	if dryRun {
		action = "would_drop"
	}
	result := outboxActionFromItem(item, action, dryRun)
	if dryRun {
		return result, nil
	}
	if err := daemon.RemoveOutboxItem(teamDir, id); err != nil {
		return outboxActionResult{}, err
	}
	return result, nil
}

func outboxActionFromItem(item *daemon.OutboxItem, action string, dryRun bool) outboxActionResult {
	return outboxActionResult{
		ID:     item.ID,
		State:  item.State,
		Type:   item.Type,
		Source: item.Source,
		Job:    outboxItemJob(item),
		Action: action,
		Item:   item,
		DryRun: dryRun,
	}
}

func previewOutboxDrainLocal(teamDir string) (*daemon.OutboxDrainResult, error) {
	items, err := daemon.ListOutboxItems(teamDir)
	if err != nil {
		return nil, err
	}
	result := &daemon.OutboxDrainResult{DryRun: true, Items: []daemon.OutboxDrainItem{}}
	for _, item := range items {
		if item == nil {
			continue
		}
		switch item.State {
		case daemon.OutboxStatePending:
			result.Pending++
			result.WouldPublish++
			result.Items = append(result.Items, daemon.OutboxDrainItem{
				ID:     item.ID,
				Type:   item.Type,
				Action: "would_publish",
			})
		case daemon.OutboxStateProcessed:
			result.Processed++
		case daemon.OutboxStateFailed:
			result.Failed++
		}
	}
	return result, nil
}

func outboxReadError(id string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("outbox item %q not found", id)
	}
	return err
}

func outboxItemJob(item *daemon.OutboxItem) string {
	if item == nil {
		return ""
	}
	return outboxItemJobFromPayload(item.Payload)
}

func outboxItemJobFromPayload(payload map[string]any) string {
	for _, key := range []string{"job_id", "job", "ticket", "ticket_id"} {
		if value := outboxPayloadString(payload, key); value != "" {
			return value
		}
	}
	return ""
}

func outboxPayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func normalizeOutboxJob(value string) string {
	return job.NormalizeID(value)
}

func outboxTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339)
}

func outboxStateRank(state string) int {
	switch state {
	case daemon.OutboxStatePending:
		return 0
	case daemon.OutboxStateFailed:
		return 1
	case daemon.OutboxStateProcessed:
		return 2
	default:
		return 3
	}
}
