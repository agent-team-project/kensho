package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

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

func collectJobRuntimeRows(teamDir string, j *job.Job, stepID string, now time.Time, opts teamRuntimeListOptions) ([]teamRuntimeRow, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
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
	var selected []*daemon.Metadata
	if stepID = strings.TrimSpace(stepID); stepID != "" {
		idx := jobStepIndex(j, stepID)
		if idx < 0 {
			return nil, fmt.Errorf("step %q not found", stepID)
		}
		if instance := strings.TrimSpace(j.Steps[idx].Instance); instance != "" {
			if meta := byInstance[instance]; meta != nil {
				selected = append(selected, meta)
			}
		}
	} else {
		selected = metadataForResumePlanJob(metas, byInstance, j)
	}
	rows := make([]teamRuntimeRow, 0, len(selected))
	for _, meta := range selected {
		row := teamRuntimeRowFromMetadata(meta, now)
		enrichJobRuntimeRow(&row, j)
		if !teamRuntimeRowMatches(row, opts) {
			continue
		}
		rows = append(rows, row)
	}
	return filterLimitSortTeamRuntimeRows(rows, opts), nil
}

func enrichJobRuntimeRow(row *teamRuntimeRow, j *job.Job) {
	if row == nil || j == nil {
		return
	}
	if row.Job == "" {
		if id := job.NormalizeID(j.ID); id != "" {
			row.Job = id
		} else {
			row.Job = strings.TrimSpace(j.ID)
		}
	}
	if row.Ticket == "" {
		row.Ticket = strings.TrimSpace(j.Ticket)
	}
	if row.Branch == "" {
		row.Branch = strings.TrimSpace(j.Branch)
	}
	if row.PR == "" {
		row.PR = strings.TrimSpace(j.PR)
	}
	if row.Workspace == "" {
		row.Workspace = filepath.ToSlash(strings.TrimSpace(j.Worktree))
	}
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
		raw    bool
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
				if raw {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job logs: --last-message cannot be combined with --raw.")
					return exitErr(2)
				}
				return streamSelectedLastMessageWithPrefix(cmd, teamDir, logListRow{Instance: instance}, "agent-team job logs")
			}
			if raw && clean {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job logs: --raw cannot be combined with --clean.")
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
				Clean:   clean,
				Raw:     raw,
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
	cmd.Flags().BoolVar(&clean, "clean", false, "Hide known Codex runtime diagnostic noise before printing the owning instance log.")
	cmd.Flags().BoolVar(&raw, "raw", false, "Print the unprocessed owning instance log without Codex JSONL rendering.")
	return cmd
}

func newJobRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect job-owned runtime metadata.",
		Long:  "Inspect raw daemon runtime metadata owned by one durable job.",
	}
	cmd.AddCommand(newJobRuntimeLsCmd())
	return cmd
}

func newJobRuntimeLsCmd() *cobra.Command {
	var (
		repo             string
		stepID           string
		statusFilters    []string
		runtimeFilters   []string
		agentFilters     []string
		instanceFilters  []string
		runtimeStaleOnly bool
		unhealthyOnly    bool
		latest           bool
		last             int
		sortBy           string
		summary          bool
		jsonOut          bool
		format           string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls <job-id>",
		Short: "List daemon runtime metadata owned by one job.",
		Long: "List raw daemon runtime metadata owned by one durable job. " +
			"Pipeline jobs can own several stage instances; pass --step to focus one stage.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job runtime ls: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job runtime ls: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job runtime ls: choose one of --latest or --last.")
				return exitErr(2)
			}
			tmpl, err := parseTeamRuntimeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job runtime ls: %v\n", err)
				return exitErr(2)
			}
			opts, err := newTeamRuntimeListOptions(statusFilters, runtimeFilters, agentFilters, instanceFilters, runtimeStaleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job runtime ls: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseTeamRuntimeSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job runtime ls: %v\n", err)
				return exitErr(2)
			}
			opts.Sort = sortMode
			opts.SortSet = cmd.Flags().Changed("sort")
			opts.Limit = last
			if latest {
				opts.Limit = 1
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(stepID) != "" && jobStepIndex(j, stepID) < 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job runtime ls: step %q not found\n", strings.TrimSpace(stepID))
				return exitErr(2)
			}
			rows, err := collectJobRuntimeRows(teamDir, j, stepID, time.Now().UTC(), opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job runtime ls: %v\n", err)
				return exitErr(1)
			}
			if summary {
				out := summarizeTeamRuntimeRows(rows)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				return renderTeamRuntimeSummary(cmd.OutOrStdout(), out)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			if tmpl != nil {
				return renderTeamRuntimeFormat(cmd.OutOrStdout(), rows, tmpl)
			}
			return renderTeamRuntimeRows(cmd.OutOrStdout(), rows)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Only show metadata for this pipeline step's owning instance.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show job-owned runtime status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show job-owned metadata for this runtime: claude, codex, or docker. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show job-owned metadata for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show job-owned metadata with this instance name. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show job-owned running metadata whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed or runtime-stale job-owned metadata.")
	cmd.Flags().BoolVarP(&latest, "latest", "l", false, "Show only the most recently started job-owned runtime record after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started job-owned runtime records after other filters (0 = all).")
	cmd.Flags().StringVar(&sortBy, "sort", "instance", "Sort job runtime rows by instance, status, runtime, agent, stale, unhealthy, job, started, stopped, or exited.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching job-owned runtime metadata by status, runtime, and agent.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit job runtime metadata as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job runtime row with a Go template, e.g. '{{.Instance}} {{.Runtime}} {{.Status}}'.")
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
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show job-owned instances for this runtime: claude, codex, or docker. Can repeat or comma-separate.")
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
		Use:   "stats <job-id>",
		Short: "Show CPU and memory usage for a job's instances.",
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
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show job-owned instances for this runtime: claude, codex, or docker. Can repeat or comma-separate.")
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
		commands bool
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
			if err := validateCommandsMode(cmd, commandsModeValidation{
				Command:       "agent-team job attach",
				Commands:      commands,
				RequireDryRun: true,
				DryRun:        dryRun,
			}); err != nil {
				return err
			}
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
			if commands {
				plan, _, err := prepareAttach(cmd, repoRoot, instance, true)
				if err != nil {
					return err
				}
				return renderJobAttachDryRunCommands(cmd.OutOrStdout(), plan, j, selection.StepID, noResume, attachCommandOptions{
					BaseArgs:   []string{"agent-team", "job", "attach"},
					TargetFlag: "--repo",
					Target:     repo,
					TargetSet:  cmd.Flags().Changed("repo"),
				})
			}
			if err := runAttach(cmd, repoRoot, instance, noResume, dryRun, false, attachCommandOptions{}); err != nil {
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
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job attach or unmanaged fallback commands.")
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

func renderJobAttachDryRunCommands(w fmtWriter, plan *attachPlan, j *job.Job, stepID string, noResume bool, opts attachCommandOptions) error {
	if plan == nil || plan.meta == nil || j == nil {
		return nil
	}
	if lifecycleMetadataSupportsManagedResume(plan.meta) {
		args := jobAttachApplyCommandArgs(j.ID, stepID, noResume, opts)
		_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(args), " "))
		return err
	}
	if command := attachResumeCommand(plan.meta, plan.bin); command != "" {
		if _, err := fmt.Fprintln(w, command); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(jobAttachLogsCommandArgs(j.ID, stepID, "--follow", opts)), " ")); err != nil {
		return err
	}
	if lifecycleMetadataRuntimeKind(plan.meta) == runtimebin.KindCodex {
		if _, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(jobAttachLogsCommandArgs(j.ID, stepID, "--last-message", opts)), " ")); err != nil {
			return err
		}
	}
	return nil
}

func jobAttachApplyCommandArgs(jobID, stepID string, noResume bool, opts attachCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	if opts.TargetSet && strings.TrimSpace(opts.Target) != "" {
		args = append(args, attachCommandTargetFlag(opts), opts.Target)
	}
	args = append(args, jobID)
	if stepID = strings.TrimSpace(stepID); stepID != "" {
		args = append(args, "--step", stepID)
	}
	if noResume {
		args = append(args, "--no-resume")
	}
	return args
}

func jobAttachLogsCommandArgs(jobID, stepID, logFlag string, opts attachCommandOptions) []string {
	args := []string{"agent-team", "job", "logs"}
	if opts.TargetSet && strings.TrimSpace(opts.Target) != "" {
		args = append(args, attachCommandTargetFlag(opts), opts.Target)
	}
	args = append(args, jobID)
	if stepID = strings.TrimSpace(stepID); stepID != "" {
		args = append(args, "--step", stepID)
	}
	args = append(args, logFlag)
	return args
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
		commands    bool
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
				Commands:       commands,
				Quiet:          quiet,
				JSON:           jsonOut,
				Format:         formatTemplate,
				Command: jobLifecycleCommandOptions(cmd, repo, []string{"agent-team", "job", "stop"}, args[0], stepID, lifecycleCommandOptions{
					Force:      force,
					Remove:     remove,
					Timeout:    timeout,
					TimeoutSet: cmd.Flags().Changed("timeout"),
				}),
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
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job stop command when the preview has actionable work.")
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
		commands    bool
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
				Commands:       commands,
				Action:         "kill",
				JSON:           jsonOut,
				Format:         formatTemplate,
				Command: jobLifecycleCommandOptions(cmd, repo, []string{"agent-team", "job", "kill"}, args[0], stepID, lifecycleCommandOptions{
					Remove:     remove,
					Timeout:    timeout,
					TimeoutSet: cmd.Flags().Changed("timeout"),
				}),
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
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching job kill command when the preview has actionable work.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable lifecycle action JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func jobLifecycleCommandOptions(cmd *cobra.Command, repo string, baseArgs []string, jobID, stepID string, opts lifecycleCommandOptions) lifecycleCommandOptions {
	scope := operatorCommandScopeFromCommand(cmd, repo, "repo")
	opts.BaseArgs = append([]string{}, baseArgs...)
	opts.TargetFlag = "--repo"
	opts.Target = scope.Repo
	opts.TargetSet = scope.Set
	opts.Names = []string{strings.TrimSpace(jobID)}
	opts.Step = stepID
	return opts
}
