package cli

import (
	"bytes"
	"encoding/json"
	"errors"
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

func newJobSnapshotCmd() *cobra.Command {
	var (
		repo       string
		output     string
		jsonOut    bool
		noRedact   bool
		eventLimit int
		logTail    int
		format     string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "snapshot <job-id>",
		Short: "Capture a job-scoped diagnostic snapshot.",
		Long: "Capture a read-only diagnostic snapshot for one durable job, including job state, audit events, " +
			"daemon lifecycle rows, queue/outbox ownership, inbox summaries, runtime metadata, state files, optional log tail content, and command provenance.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if eventLimit < -1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job snapshot: --events must be >= -1.")
				return exitErr(2)
			}
			if logTail < -1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job snapshot: --tail must be >= -1.")
				return exitErr(2)
			}
			if jsonOut && output != "" && output != "-" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job snapshot: choose one of --json or --output.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || output != "") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job snapshot: --format cannot be combined with --json or --output.")
				return exitErr(2)
			}
			formatTemplate, err := parseSnapshotFormat("job-snapshot-format", format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job snapshot: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			repoRoot, err := filepath.Abs(effectiveRepoTarget(cmd, repo))
			if err != nil {
				return err
			}
			snapshot := collectJobSnapshot(teamDir, repoRoot, j, jobSnapshotOptions{
				EventLimit: eventLimit,
				LogTail:    logTail,
				Redact:     !noRedact,
				Now:        time.Now().UTC(),
			})
			snapshot.Provenance = newSnapshotProvenance(cmd.CommandPath(), "job", j.ID, snapshotProvenanceOptions{
				Events:   intValuePtr(eventLimit),
				Tail:     intValuePtr(logTail),
				Redacted: !noRedact,
			})
			switch {
			case jsonOut || output == "-":
				return writeJobSnapshotJSON(cmd.OutOrStdout(), snapshot)
			case output != "":
				path, err := writeJobSnapshotFile(output, snapshot)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote job snapshot to %s\n", path)
				return nil
			case formatTemplate != nil:
				return renderSnapshotFormat(cmd.OutOrStdout(), snapshot, formatTemplate)
			default:
				renderJobSnapshotSummary(cmd.OutOrStdout(), snapshot)
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the full JSON snapshot to this file. Use '-' for stdout.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the full job snapshot JSON to stdout.")
	cmd.Flags().BoolVar(&noRedact, "no-redact", false, "Include raw queue/outbox payload values and latest inbox bodies instead of redacting them.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job snapshot with a Go template, e.g. '{{.Job.ID}} {{.Job.Status}}'.")
	cmd.Flags().IntVar(&eventLimit, "events", 20, "Recent job and lifecycle events to include. Use -1 for all events or 0 to skip events.")
	cmd.Flags().IntVar(&logTail, "tail", 0, "Include the last N log lines in JSON output. Use -1 for the full log or 0 to omit log content.")
	return cmd
}

type jobSnapshotOptions struct {
	EventLimit int
	LogTail    int
	Redact     bool
	Now        time.Time
}

type jobSnapshotResult struct {
	Version         string                  `json:"version"`
	CapturedAt      string                  `json:"captured_at"`
	Repo            string                  `json:"repo"`
	TeamDir         string                  `json:"team_dir"`
	Provenance      *snapshotProvenance     `json:"provenance,omitempty"`
	Redacted        bool                    `json:"redacted"`
	Job             *job.Job                `json:"job"`
	Instance        string                  `json:"instance,omitempty"`
	Runtime         *inspectRuntimeJSON     `json:"runtime,omitempty"`
	State           *jobSnapshotState       `json:"state,omitempty"`
	Status          *inspectStatusJSON      `json:"status,omitempty"`
	StatusError     string                  `json:"status_error,omitempty"`
	Files           []inspectFileJSON       `json:"files,omitempty"`
	Log             *jobSnapshotFile        `json:"log,omitempty"`
	LastMessage     *jobSnapshotFile        `json:"last_message,omitempty"`
	JobEvents       []job.Event             `json:"job_events,omitempty"`
	LifecycleEvents []daemon.LifecycleEvent `json:"lifecycle_events,omitempty"`
	Queue           []*daemon.QueueItem     `json:"queue,omitempty"`
	QueueSummary    *queueSummary           `json:"queue_summary,omitempty"`
	QueueQuarantine []queueQuarantineItem   `json:"queue_quarantine,omitempty"`
	Outbox          []*daemon.OutboxItem    `json:"outbox,omitempty"`
	OutboxSummary   *outboxSummary          `json:"outbox_summary,omitempty"`
	Inbox           []inboxSummaryRow       `json:"inbox,omitempty"`
	InboxSummary    *overviewInboxSummary   `json:"inbox_summary,omitempty"`
	Actions         []string                `json:"actions,omitempty"`
	SectionErrors   map[string]string       `json:"section_errors,omitempty"`
}

type jobSnapshotState struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type jobSnapshotFile struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"mod_time,omitempty"`
	Tail    string `json:"tail,omitempty"`
}

func collectJobSnapshot(teamDir, repoRoot string, j *job.Job, opts jobSnapshotOptions) *jobSnapshotResult {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := &jobSnapshotResult{
		Version:    Version,
		CapturedAt: now.UTC().Format(time.RFC3339),
		Repo:       filepath.ToSlash(repoRoot),
		TeamDir:    filepath.ToSlash(teamDir),
		Redacted:   opts.Redact,
		Job:        j,
	}
	if j == nil {
		out.addError("job", errors.New("job is nil"))
		return out
	}
	meta, metaErr := collectJobSnapshotMetadata(teamDir, j)
	if metaErr != nil {
		out.addError("runtime", metaErr)
	}
	if meta != nil {
		out.Runtime = inspectRuntimeJSONFromMeta(teamDir, meta)
	}
	instance := jobSnapshotInstance(j, meta)
	out.Instance = instance
	if instance != "" {
		state := collectJobSnapshotState(teamDir, instance, now)
		out.State = &state.State
		out.Status = state.Status
		out.StatusError = state.StatusError
		out.Files = state.Files
		logPath := logPathForMetadata(teamDir, meta)
		out.Log = collectJobSnapshotFile(teamDir, logPath, opts.LogTail)
		out.LastMessage = collectJobSnapshotFile(teamDir, lastMessagePathForInstance(teamDir, instance), opts.LogTail)
	}
	if opts.EventLimit != 0 {
		if events, err := job.ListEvents(teamDir, j.ID); err != nil {
			out.addError("job_events", err)
		} else {
			out.JobEvents = tailJobSnapshotEvents(events, opts.EventLimit)
		}
		if events, err := collectJobSnapshotLifecycleEvents(teamDir, j, instance, opts.EventLimit); err != nil {
			out.addError("lifecycle_events", err)
		} else {
			out.LifecycleEvents = events
		}
	}
	if queue, err := queueItemsForJob(teamDir, j); err != nil {
		out.addError("queue", err)
	} else {
		if opts.Redact {
			queue = redactJobSnapshotQueue(queue)
		}
		out.Queue = queue
		summary := summarizeQueueItems(queue, now, queueRuntimeMap(teamDir))
		out.QueueSummary = &summary
	}
	if quarantine, err := collectJobQueueQuarantineItems(teamDir, j, queueListFilters{}); err != nil {
		out.addError("queue_quarantine", err)
	} else {
		out.QueueQuarantine = quarantine
		applyQueueQuarantineSummary(ensureJobSnapshotQueueSummary(out, now), quarantine)
	}
	if outbox, err := outboxItemsForJob(teamDir, j); err != nil {
		out.addError("outbox", err)
	} else {
		if opts.Redact {
			outbox = redactJobSnapshotOutbox(outbox)
		}
		out.Outbox = outbox
		summary := summarizeOutboxItems(outbox)
		out.OutboxSummary = &summary
	}
	if inbox, summary, err := collectJobSnapshotInbox(teamDir, j, meta); err != nil {
		out.addError("inbox", err)
	} else if len(inbox) > 0 {
		if opts.Redact {
			inbox = redactJobSnapshotInbox(inbox)
		}
		out.Inbox = inbox
		out.InboxSummary = &summary
	}
	out.Actions = jobSnapshotActions(j, out, instance)
	return out
}

type collectedJobSnapshotState struct {
	State       jobSnapshotState
	Status      *inspectStatusJSON
	StatusError string
	Files       []inspectFileJSON
}

func collectJobSnapshotState(teamDir, instance string, now time.Time) collectedJobSnapshotState {
	stateDir := filepath.Join(teamDir, "state", instance)
	out := collectedJobSnapshotState{
		State: jobSnapshotState{
			Path: displayPathFromTeamDir(teamDir, stateDir),
		},
	}
	st, err := os.Stat(stateDir)
	if err != nil || !st.IsDir() {
		return out
	}
	out.State.Exists = true
	out.Status, out.StatusError = inspectStatusJSONFor(teamDir, stateDir, now)
	out.Files, _ = inspectFiles(stateDir)
	return out
}

func collectJobSnapshotMetadata(teamDir string, j *job.Job) (*daemon.Metadata, error) {
	root := daemon.DaemonRoot(teamDir)
	if j != nil && strings.TrimSpace(j.Instance) != "" {
		meta, err := daemon.ReadMetadata(root, j.Instance)
		if err == nil || !os.IsNotExist(err) {
			return meta, err
		}
	}
	all, err := daemon.ListMetadata(root)
	if err != nil {
		return nil, err
	}
	var matches []*daemon.Metadata
	for _, meta := range all {
		if jobSnapshotMetadataMatches(meta, j) {
			matches = append(matches, meta)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(i, k int) bool {
		return jobSnapshotMetadataTime(matches[i]).After(jobSnapshotMetadataTime(matches[k]))
	})
	return matches[0], nil
}

func jobSnapshotMetadataMatches(meta *daemon.Metadata, j *job.Job) bool {
	if meta == nil || j == nil {
		return false
	}
	if strings.TrimSpace(meta.Job) != "" && job.NormalizeID(meta.Job) == j.ID {
		return true
	}
	if strings.TrimSpace(j.Instance) != "" && meta.Instance == j.Instance {
		return true
	}
	if strings.TrimSpace(meta.Ticket) != "" {
		return job.NormalizeID(meta.Ticket) == j.ID || strings.EqualFold(strings.TrimSpace(meta.Ticket), strings.TrimSpace(j.Ticket))
	}
	return false
}

func jobSnapshotMetadataTime(meta *daemon.Metadata) time.Time {
	if meta == nil {
		return time.Time{}
	}
	for _, ts := range []time.Time{meta.ExitedAt, meta.StoppedAt, meta.StartedAt} {
		if !ts.IsZero() {
			return ts
		}
	}
	return time.Time{}
}

func jobSnapshotInstance(j *job.Job, meta *daemon.Metadata) string {
	if j != nil && strings.TrimSpace(j.Instance) != "" {
		return strings.TrimSpace(j.Instance)
	}
	if meta != nil {
		return strings.TrimSpace(meta.Instance)
	}
	return ""
}

func collectJobSnapshotFile(teamDir, path string, tailLines int) *jobSnapshotFile {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	out := &jobSnapshotFile{Path: displayPathFromTeamDir(teamDir, path)}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return out
	}
	out.Exists = true
	out.Size = st.Size()
	out.ModTime = st.ModTime().UTC().Format(time.RFC3339)
	if tailLines != 0 {
		if tail, err := readJobSnapshotTail(path, tailLines); err == nil {
			out.Tail = tail
		}
	}
	return out
}

func readJobSnapshotTail(path string, lines int) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if lines < 0 {
		return string(body), nil
	}
	if lines == 0 {
		return "", nil
	}
	parts := bytes.SplitAfter(body, []byte("\n"))
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	if lines < len(parts) {
		parts = parts[len(parts)-lines:]
	}
	return string(bytes.Join(parts, nil)), nil
}

func tailJobSnapshotEvents(events []job.Event, limit int) []job.Event {
	if limit < 0 {
		return events
	}
	return job.TailEvents(events, limit)
}

func collectJobSnapshotLifecycleEvents(teamDir string, j *job.Job, instance string, limit int) ([]daemon.LifecycleEvent, error) {
	events, err := daemon.ListLifecycleEvents(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	matches := make([]daemon.LifecycleEvent, 0, len(events))
	for _, ev := range events {
		if jobSnapshotLifecycleEventMatches(ev, j, instance) {
			matches = append(matches, *ev)
		}
	}
	if limit < 0 || limit >= len(matches) {
		return matches, nil
	}
	return matches[len(matches)-limit:], nil
}

func jobSnapshotLifecycleEventMatches(ev *daemon.LifecycleEvent, j *job.Job, instance string) bool {
	if ev == nil || j == nil {
		return false
	}
	if strings.TrimSpace(ev.Job) != "" && job.NormalizeID(ev.Job) == j.ID {
		return true
	}
	if instance != "" && ev.Instance == instance {
		return true
	}
	if strings.TrimSpace(ev.Ticket) != "" {
		return job.NormalizeID(ev.Ticket) == j.ID || strings.EqualFold(strings.TrimSpace(ev.Ticket), strings.TrimSpace(j.Ticket))
	}
	return false
}

func redactJobSnapshotQueue(items []*daemon.QueueItem) []*daemon.QueueItem {
	out := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		clone := *item
		clone.Payload = redactSnapshotMap(item.Payload)
		out = append(out, &clone)
	}
	return out
}

func redactJobSnapshotOutbox(items []*daemon.OutboxItem) []*daemon.OutboxItem {
	out := make([]*daemon.OutboxItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		clone := *item
		clone.Payload = redactSnapshotMap(item.Payload)
		out = append(out, &clone)
	}
	return out
}

func collectJobSnapshotInbox(teamDir string, j *job.Job, primaryMeta *daemon.Metadata) ([]inboxSummaryRow, overviewInboxSummary, error) {
	if j == nil {
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
	addInstance(j.Instance)
	for _, step := range j.Steps {
		addInstance(step.Instance)
	}
	if primaryMeta != nil {
		addInstance(primaryMeta.Instance)
		if strings.TrimSpace(primaryMeta.Instance) != "" {
			metaByInstance[primaryMeta.Instance] = primaryMeta
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
		if jobSnapshotMetadataMatches(meta, j) {
			addInstance(meta.Instance)
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

func redactJobSnapshotInbox(rows []inboxSummaryRow) []inboxSummaryRow {
	out := append([]inboxSummaryRow(nil), rows...)
	for i := range out {
		if out[i].LatestBody != "" {
			out[i].LatestBody = snapshotRedactedValue
		}
	}
	return out
}

func ensureJobSnapshotQueueSummary(snapshot *jobSnapshotResult, now time.Time) *queueSummary {
	if snapshot.QueueSummary == nil {
		summary := summarizeQueueItems(snapshot.Queue, now)
		snapshot.QueueSummary = &summary
	}
	return snapshot.QueueSummary
}

func jobSnapshotActions(j *job.Job, snapshot *jobSnapshotResult, instance string) []string {
	if j == nil {
		return nil
	}
	added := map[string]bool{}
	add := func(action string) {
		if strings.TrimSpace(action) == "" || added[action] {
			return
		}
		added[action] = true
	}
	add(fmt.Sprintf("agent-team job show %s --events all", j.ID))
	if instance != "" {
		add(fmt.Sprintf("agent-team inspect %s", instance))
		if snapshot != nil && snapshot.Log != nil && snapshot.Log.Exists {
			add(fmt.Sprintf("agent-team job logs %s --tail 100", j.ID))
		}
		if snapshot != nil && snapshot.LastMessage != nil && snapshot.LastMessage.Exists {
			add(fmt.Sprintf("agent-team job logs %s --last-message", j.ID))
		}
	}
	if snapshot != nil {
		if len(snapshot.Queue) > 0 {
			add(fmt.Sprintf("agent-team job queue %s --summary", j.ID))
		}
		if len(snapshot.QueueQuarantine) > 0 {
			add(fmt.Sprintf("agent-team job queue quarantine %s", j.ID))
		}
		if len(snapshot.Outbox) > 0 {
			add(fmt.Sprintf("agent-team job outbox %s --summary", j.ID))
		}
		for _, row := range snapshot.Inbox {
			if row.Unread > 0 {
				add(fmt.Sprintf("agent-team inbox show %s --unread", row.Instance))
			}
		}
	}
	actions := make([]string, 0, len(added))
	for action := range added {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}

func (r *jobSnapshotResult) addError(section string, err error) {
	if err == nil {
		return
	}
	if r.SectionErrors == nil {
		r.SectionErrors = map[string]string{}
	}
	r.SectionErrors[section] = err.Error()
}

func writeJobSnapshotFile(path string, snapshot *jobSnapshotResult) (string, error) {
	path = filepath.Clean(path)
	body, err := jobSnapshotJSON(snapshot)
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

func writeJobSnapshotJSON(w io.Writer, snapshot *jobSnapshotResult) error {
	body, err := jobSnapshotJSON(snapshot)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func jobSnapshotJSON(snapshot *jobSnapshotResult) ([]byte, error) {
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func renderJobSnapshotSummary(w io.Writer, snapshot *jobSnapshotResult) {
	if snapshot == nil || snapshot.Job == nil {
		fmt.Fprintln(w, "job snapshot: unavailable")
		return
	}
	j := snapshot.Job
	fmt.Fprintf(w, "job snapshot: %s\n", snapshot.CapturedAt)
	if snapshot.Provenance != nil {
		renderSnapshotProvenanceSummary(w, snapshot.Provenance)
	}
	fmt.Fprintf(w, "job: %s ticket=%s status=%s target=%s\n", j.ID, emptyDash(j.Ticket), j.Status, emptyDash(j.Target))
	if j.Instance != "" {
		fmt.Fprintf(w, "instance: %s\n", j.Instance)
	} else if snapshot.Instance != "" {
		fmt.Fprintf(w, "instance: %s\n", snapshot.Instance)
	}
	if snapshot.Runtime != nil {
		fmt.Fprintf(w, "runtime: lifecycle=%s agent=%s runtime=%s pid=%d\n",
			emptyDash(snapshot.Runtime.Lifecycle),
			emptyDash(snapshot.Runtime.Agent),
			emptyDash(snapshot.Runtime.Runtime),
			snapshot.Runtime.PID)
	}
	if snapshot.State != nil {
		fmt.Fprintf(w, "state: exists=%s path=%s\n", yesNo(snapshot.State.Exists), snapshot.State.Path)
	}
	if snapshot.Log != nil {
		fmt.Fprintf(w, "log: exists=%s size=%d path=%s\n", yesNo(snapshot.Log.Exists), snapshot.Log.Size, snapshot.Log.Path)
	}
	if snapshot.LastMessage != nil {
		fmt.Fprintf(w, "last_message: exists=%s size=%d path=%s\n", yesNo(snapshot.LastMessage.Exists), snapshot.LastMessage.Size, snapshot.LastMessage.Path)
	}
	fmt.Fprintf(w, "events: job=%d lifecycle=%d\n", len(snapshot.JobEvents), len(snapshot.LifecycleEvents))
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
	if snapshot.InboxSummary != nil {
		fmt.Fprintf(w, "inbox: instances=%d total=%d unread=%d unread_instances=%d\n",
			snapshot.InboxSummary.Instances,
			snapshot.InboxSummary.Total,
			snapshot.InboxSummary.Unread,
			snapshot.InboxSummary.UnreadInstances)
	}
	if len(snapshot.Actions) > 0 {
		fmt.Fprintln(w, "actions:")
		for _, action := range snapshot.Actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
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
