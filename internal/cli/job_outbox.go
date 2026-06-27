package cli

import (
	"fmt"
	"io"
	"os"
	"text/template"

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
		summary     bool
		jsonOut     bool
		format      string
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
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate outbox counts instead of rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit job-owned outbox rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job-owned outbox item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.AddCommand(newJobOutboxShowCmd())
	cmd.AddCommand(newJobOutboxRetryCmd())
	cmd.AddCommand(newJobOutboxDropCmd())
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
		repo    string
		jsonOut bool
		format  string
		dryRun  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "retry <job-id> <id>",
		Aliases: []string{"requeue"},
		Short:   "Move one job-owned processed or failed outbox event back to pending.",
		Args:    cobra.ExactArgs(2),
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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the retry without moving the event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobOutboxDropCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
		dryRun  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <job-id> <id>",
		Short: "Remove one job-owned outbox event.",
		Args:  cobra.ExactArgs(2),
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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the drop without removing the event.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
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
