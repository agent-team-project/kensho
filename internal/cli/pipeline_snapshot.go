package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newPipelineSnapshotCmd() *cobra.Command {
	var (
		repo         string
		output       string
		timelineTail string
		timelineSort string
		jsonOut      bool
		noRedact     bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "snapshot <pipeline>",
		Short: "Capture a read-only diagnostic snapshot for one pipeline.",
		Long: "Capture a compact read-only diagnostic artifact for one pipeline, including status, " +
			"step explanations, owned jobs, inbox summaries, queue ownership, dry-run advance previews, and command provenance.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && output != "" && output != "-" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline snapshot: choose one of --json or --output.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || output != "") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline snapshot: --format cannot be combined with --json or --output.")
				return exitErr(2)
			}
			formatTemplate, err := parseSnapshotFormat("pipeline-snapshot-format", format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team pipeline snapshot: %v\n", err)
				return exitErr(2)
			}
			pipelineName := strings.TrimSpace(args[0])
			if pipelineName == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline snapshot: pipeline name is required.")
				return exitErr(2)
			}
			timelineEvents, err := parseLogTail(timelineTail)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline snapshot: --timeline must be >= 0 or \"all\".")
				return exitErr(2)
			}
			timelineSortMode, err := parseEventSort(timelineSort)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team pipeline snapshot: --timeline-sort must be oldest or newest.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			repoRoot, err := filepath.Abs(effectiveRepoTarget(cmd, repo))
			if err != nil {
				return exitErr(2)
			}
			snapshot := collectPipelineSnapshot(teamDir, repoRoot, pipelineName, pipelineSnapshotOptions{
				Redact:       !noRedact,
				Now:          time.Now().UTC(),
				TimelineTail: timelineEvents,
				TimelineSort: timelineSortMode,
			})
			timelineSortProvenance := ""
			if cmd.Flags().Changed("timeline-sort") {
				timelineSortProvenance = timelineSortMode
			}
			snapshot.Provenance = newSnapshotProvenance(cmd.CommandPath(), "pipeline", pipelineName, snapshotProvenanceOptions{
				Timeline:     intValuePtr(timelineEvents),
				TimelineSort: timelineSortProvenance,
				Redacted:     !noRedact,
			})
			switch {
			case jsonOut || output == "-":
				return writePipelineSnapshotJSON(cmd.OutOrStdout(), snapshot)
			case output != "":
				path, err := writePipelineSnapshotFile(output, snapshot)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote pipeline snapshot to %s\n", path)
				return nil
			case formatTemplate != nil:
				return renderSnapshotFormat(cmd.OutOrStdout(), snapshot, formatTemplate)
			default:
				renderPipelineSnapshotSummary(cmd.OutOrStdout(), snapshot)
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the full JSON pipeline snapshot to this file. Use '-' for stdout.")
	cmd.Flags().StringVar(&timelineTail, "timeline", "50", "Include the last N combined audit/lifecycle timeline rows in the snapshot (0 or all = all).")
	cmd.Flags().StringVar(&timelineSort, "timeline-sort", "oldest", "Sort included combined audit/lifecycle timeline rows by oldest or newest after applying --timeline.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the full pipeline snapshot JSON to stdout.")
	cmd.Flags().BoolVar(&noRedact, "no-redact", false, "Include raw payload values and latest inbox bodies instead of redacting them.")
	cmd.Flags().StringVar(&format, "format", "", "Render the pipeline snapshot with a Go template, e.g. '{{.Pipeline}} {{len .Jobs}}'.")
	return cmd
}

type pipelineSnapshotOptions struct {
	Redact       bool
	Now          time.Time
	TimelineTail int
	TimelineSort string
}

type pipelineSnapshotResult struct {
	Version                 string                   `json:"version"`
	CapturedAt              string                   `json:"captured_at"`
	Repo                    string                   `json:"repo"`
	TeamDir                 string                   `json:"team_dir"`
	Provenance              *snapshotProvenance      `json:"provenance,omitempty"`
	Pipeline                string                   `json:"pipeline"`
	Git                     *snapshotGitInfo         `json:"git,omitempty"`
	Redacted                bool                     `json:"redacted"`
	Status                  *pipelineStatusRow       `json:"status,omitempty"`
	Explain                 *pipelineExplainRow      `json:"explain,omitempty"`
	Jobs                    []*job.Job               `json:"jobs,omitempty"`
	Inbox                   []inboxSummaryRow        `json:"inbox,omitempty"`
	InboxSummary            *overviewInboxSummary    `json:"inbox_summary,omitempty"`
	Queue                   []*daemon.QueueItem      `json:"queue,omitempty"`
	QueueSummary            *queueSummary            `json:"queue_summary,omitempty"`
	QueueQuarantine         []queueQuarantineItem    `json:"queue_quarantine,omitempty"`
	Outbox                  []*daemon.OutboxItem     `json:"outbox,omitempty"`
	OutboxSummary           *outboxSummary           `json:"outbox_summary,omitempty"`
	OutboxQuarantine        []outboxQuarantineItem   `json:"outbox_quarantine,omitempty"`
	OutboxQuarantineSummary *outboxQuarantineSummary `json:"outbox_quarantine_summary,omitempty"`
	Timeline                []jobTimelineEntry       `json:"timeline,omitempty"`
	AdvancePreview          []pipelineAdvanceResult  `json:"advance_preview,omitempty"`
	SectionErrors           map[string]string        `json:"section_errors,omitempty"`
}

func collectPipelineSnapshot(teamDir, repoRoot, pipeline string, opts pipelineSnapshotOptions) *pipelineSnapshotResult {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	pipeline = strings.TrimSpace(pipeline)
	out := &pipelineSnapshotResult{
		Version:    Version,
		CapturedAt: now.Format(time.RFC3339),
		Repo:       filepath.ToSlash(repoRoot),
		TeamDir:    filepath.ToSlash(teamDir),
		Pipeline:   pipeline,
	}
	out.Git = collectSnapshotGitInfo(repoRoot)

	if status, err := collectPipelineStatusRows(teamDir, pipeline); err != nil {
		out.addError("status", err)
	} else if len(status) > 0 {
		row := status[0]
		out.Status = &row
	}
	if explain, err := collectPipelineExplainRows(teamDir, pipeline, 0, nil, "", "updated"); err != nil {
		out.addError("explain", err)
	} else if len(explain) > 0 {
		row := explain[0]
		out.Explain = &row
	}
	if jobs, err := job.List(teamDir); err != nil {
		out.addError("jobs", err)
	} else {
		out.Jobs = filterJobsByPipeline(jobs, pipeline)
		sortJobs(out.Jobs, "updated")
	}
	timelineSort := opts.TimelineSort
	if timelineSort == "" {
		timelineSort = "oldest"
	}
	if timeline, err := collectJobTimelineForJobs(teamDir, out.Jobs, "all", nil, opts.TimelineTail, timelineSort); err != nil {
		out.addError("timeline", err)
	} else {
		out.Timeline = timeline
	}
	if queue, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir)); err != nil {
		out.addError("queue", err)
	} else {
		out.Queue = queueItemsForJobs(queue, out.Jobs)
		summary := summarizeQueueItems(out.Queue, now)
		out.QueueSummary = &summary
	}
	if outbox, err := daemon.ListOutboxItems(teamDir); err != nil {
		out.addError("outbox", err)
	} else {
		out.Outbox = outboxItemsForJobs(outbox, out.Jobs)
		summary := summarizeOutboxItems(out.Outbox)
		out.OutboxSummary = &summary
	}
	if quarantine, err := listOutboxQuarantine(teamDir); err != nil {
		out.addError("outbox_quarantine", err)
	} else {
		out.OutboxQuarantine = outboxQuarantineItemsForJobs(quarantine, out.Jobs)
		summary := summarizeOutboxQuarantineItems(out.OutboxQuarantine)
		out.OutboxQuarantineSummary = &summary
	}
	if inbox, summary, err := collectPipelineSnapshotInbox(teamDir, out.Jobs); err != nil {
		out.addError("inbox", err)
	} else if len(inbox) > 0 {
		out.Inbox = inbox
		out.InboxSummary = &summary
	}
	if quarantine, err := listQueueQuarantine(teamDir); err != nil {
		out.addError("queue_quarantine", err)
	} else {
		out.QueueQuarantine = queueQuarantineItemsForJobs(quarantine, out.Jobs)
		applyQueueQuarantineSummary(ensurePipelineSnapshotQueueSummary(out, now), out.QueueQuarantine)
	}
	if advance, err := advanceReadyPipelineJobs(nil, teamDir, pipeline, "auto", runtimeSelection{}, 0, true, true, false); err != nil {
		out.addError("advance_preview", err)
	} else {
		out.AdvancePreview = advance
	}
	if opts.Redact {
		redactPipelineSnapshotResult(out)
	}
	return out
}

func collectPipelineSnapshotInbox(teamDir string, jobs []*job.Job) ([]inboxSummaryRow, overviewInboxSummary, error) {
	if len(jobs) == 0 {
		return nil, overviewInboxSummary{}, nil
	}
	daemonRoot := daemon.DaemonRoot(teamDir)
	instances := map[string]bool{}
	metaByInstance := map[string]*daemon.Metadata{}
	addInstance := func(instance string) {
		instance = strings.TrimSpace(instance)
		if instance != "" {
			instances[instance] = true
		}
	}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		addInstance(j.Instance)
		for _, step := range j.Steps {
			addInstance(step.Instance)
		}
	}
	metas, err := daemon.ListMetadata(daemonRoot)
	if err != nil {
		return nil, overviewInboxSummary{}, err
	}
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		metaByInstance[meta.Instance] = meta
		for _, j := range jobs {
			if jobSnapshotMetadataMatches(meta, j) {
				addInstance(meta.Instance)
				break
			}
		}
	}
	names := sortedInboxInstances(instances)
	if len(names) == 0 {
		return nil, overviewInboxSummary{}, nil
	}
	rows, err := collectInboxSummaryRows(daemonRoot, names, metaByInstance, false)
	if err != nil {
		return nil, overviewInboxSummary{}, err
	}
	return rows, overviewInboxFromRows(rows), nil
}

func ensurePipelineSnapshotQueueSummary(snapshot *pipelineSnapshotResult, now time.Time) *queueSummary {
	if snapshot.QueueSummary == nil {
		summary := summarizeQueueItems(snapshot.Queue, now)
		snapshot.QueueSummary = &summary
	}
	return snapshot.QueueSummary
}

func filterJobsByPipeline(jobs []*job.Job, pipeline string) []*job.Job {
	pipeline = strings.TrimSpace(pipeline)
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j == nil || strings.TrimSpace(j.Pipeline) != pipeline {
			continue
		}
		out = append(out, j)
	}
	return out
}

func queueQuarantineItemsForJobs(items []queueQuarantineItem, jobs []*job.Job) []queueQuarantineItem {
	if len(items) == 0 || len(jobs) == 0 {
		return nil
	}
	out := make([]queueQuarantineItem, 0, len(items))
	for _, item := range items {
		if queueQuarantineMatchesAnyJob(item, jobs) {
			out = append(out, item)
		}
	}
	return out
}

func (r *pipelineSnapshotResult) addError(section string, err error) {
	if r == nil || err == nil {
		return
	}
	if r.SectionErrors == nil {
		r.SectionErrors = map[string]string{}
	}
	r.SectionErrors[section] = err.Error()
}

func redactPipelineSnapshotResult(snapshot *pipelineSnapshotResult) {
	if snapshot == nil {
		return
	}
	snapshot.Redacted = true
	for _, item := range snapshot.Queue {
		if item == nil {
			continue
		}
		item.Payload = redactSnapshotMap(item.Payload)
	}
	for _, item := range snapshot.Outbox {
		if item == nil {
			continue
		}
		item.Payload = redactSnapshotMap(item.Payload)
	}
	for i := range snapshot.Inbox {
		if snapshot.Inbox[i].LatestBody != "" {
			snapshot.Inbox[i].LatestBody = snapshotRedactedValue
		}
	}
	for i := range snapshot.Timeline {
		snapshot.Timeline[i].Data = redactSnapshotStringMap(snapshot.Timeline[i].Data)
	}
	for i := range snapshot.AdvancePreview {
		redactSnapshotPipelineAdvance(&snapshot.AdvancePreview[i])
	}
}

func redactSnapshotStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if snapshotSensitiveKey(key) {
			out[key] = snapshotRedactedValue
			continue
		}
		out[key] = value
	}
	return out
}

func writePipelineSnapshotFile(path string, snapshot *pipelineSnapshotResult) (string, error) {
	path = filepath.Clean(path)
	body, err := pipelineSnapshotJSON(snapshot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}

func writePipelineSnapshotJSON(w io.Writer, snapshot *pipelineSnapshotResult) error {
	body, err := pipelineSnapshotJSON(snapshot)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func pipelineSnapshotJSON(snapshot *pipelineSnapshotResult) ([]byte, error) {
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func renderPipelineSnapshotSummary(w io.Writer, snapshot *pipelineSnapshotResult) {
	if snapshot == nil {
		fmt.Fprintln(w, "pipeline snapshot: unavailable")
		return
	}
	fmt.Fprintf(w, "pipeline snapshot: %s\n", snapshot.CapturedAt)
	fmt.Fprintf(w, "pipeline: %s\n", snapshot.Pipeline)
	fmt.Fprintf(w, "repo: %s\n", snapshot.Repo)
	if snapshot.Provenance != nil {
		renderSnapshotProvenanceSummary(w, snapshot.Provenance)
	}
	if snapshot.Git != nil {
		branch := snapshot.Git.Branch
		if branch == "" {
			branch = "unknown"
		}
		commit := snapshot.Git.Commit
		if commit == "" {
			commit = "unknown"
		}
		fmt.Fprintf(w, "git: branch=%s commit=%s dirty=%s changes=%d ahead=%d behind=%d\n",
			branch,
			commit,
			yesNo(snapshot.Git.Dirty),
			snapshot.Git.Changes,
			snapshot.Git.Ahead,
			snapshot.Git.Behind)
	}
	fmt.Fprintf(w, "redacted: %s\n", yesNo(snapshot.Redacted))
	if snapshot.Status != nil {
		fmt.Fprintf(w, "status: jobs=%d ready_steps=%d manual_gates=%d stale_running_steps=%d failed_steps=%d blocked_steps=%d\n",
			snapshot.Status.Jobs,
			snapshot.Status.ReadySteps,
			snapshot.Status.ManualGates,
			snapshot.Status.StaleRunningSteps,
			snapshot.Status.FailedSteps,
			snapshot.Status.BlockedSteps)
	}
	if snapshot.Explain != nil {
		fmt.Fprintf(w, "explain: jobs=%d steps=%d failed_steps=%d blocked_steps=%d\n",
			snapshot.Explain.ExplainedJobs,
			countPipelineExplainSteps([]pipelineExplainRow{*snapshot.Explain}),
			countPipelineExplainStateSteps([]pipelineExplainRow{*snapshot.Explain}, "failed"),
			countPipelineExplainStateSteps([]pipelineExplainRow{*snapshot.Explain}, "blocked"))
	}
	renderSnapshotJobSummary(w, snapshot.Jobs)
	if snapshot.InboxSummary != nil {
		fmt.Fprintf(w, "inbox: instances=%d total=%d unread=%d unread_instances=%d\n",
			snapshot.InboxSummary.Instances,
			snapshot.InboxSummary.Total,
			snapshot.InboxSummary.Unread,
			snapshot.InboxSummary.UnreadInstances)
	}
	if snapshot.QueueSummary != nil {
		fmt.Fprintln(w, queueSummaryLine(*snapshot.QueueSummary))
	}
	if snapshot.OutboxSummary != nil {
		fmt.Fprintf(w, "outbox: total=%d pending=%d failed=%d processed=%d\n",
			snapshot.OutboxSummary.Total,
			snapshot.OutboxSummary.Pending,
			snapshot.OutboxSummary.Failed,
			snapshot.OutboxSummary.Processed)
	}
	if snapshot.OutboxQuarantineSummary != nil && snapshot.OutboxQuarantineSummary.Quarantined > 0 {
		fmt.Fprintln(w, outboxQuarantineSummaryLine(*snapshot.OutboxQuarantineSummary))
	}
	if snapshot.Timeline != nil {
		jobRows, lifecycleRows := countJobTimelineSources(snapshot.Timeline)
		fmt.Fprintf(w, "timeline: events=%d job=%d lifecycle=%d\n", len(snapshot.Timeline), jobRows, lifecycleRows)
	}
	if snapshot.AdvancePreview != nil {
		fmt.Fprintf(w, "advance: ready=%d route_previews=%d\n",
			len(snapshot.AdvancePreview),
			countPipelineAdvanceRoutePreviews(snapshot.AdvancePreview))
	}
	if len(snapshot.SectionErrors) > 0 {
		fmt.Fprintln(w, "section errors:")
		keys := make([]string, 0, len(snapshot.SectionErrors))
		for key := range snapshot.SectionErrors {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(w, "  %s: %s\n", key, snapshot.SectionErrors[key])
		}
	}
}

func countJobTimelineSources(entries []jobTimelineEntry) (jobRows, lifecycleRows int) {
	for _, entry := range entries {
		switch entry.Source {
		case "job":
			jobRows++
		case "lifecycle":
			lifecycleRows++
		}
	}
	return jobRows, lifecycleRows
}
