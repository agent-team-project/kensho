package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newEventsCmd() *cobra.Command {
	var (
		target           string
		follow           bool
		tail             int
		latest           bool
		last             int
		jsonOut          bool
		summary          bool
		format           string
		actionFilters    []string
		instanceFilters  []string
		agentFilters     []string
		statusFilters    []string
		runtimeFilters   []string
		phaseFilters     []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthyOnly    bool
		sinceRaw         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Show daemon lifecycle events.",
		Long:  "Show or follow the daemon lifecycle event stream: dispatches, starts, stops, exits, crashes, and removals.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team events: --tail must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team events: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team events: choose one of --latest or --last.")
				return exitErr(2)
			}
			if summary && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team events: --summary cannot be combined with --follow.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team events: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseEventFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team events: %v\n", err)
				return exitErr(2)
			}
			filters, err := newEventFilters(actionFilters, instanceFilters, agentFilters, statusFilters, sinceRaw, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team events: %v\n", err)
				return exitErr(2)
			}
			phases, err := lifecyclePhaseFilterSet(phaseFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team events: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			var daemonClientForFilters *daemonClient
			var client eventsClient
			daemonClientForFilters, err = newDaemonClient(teamDir)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					client = localEventsClient{daemonRoot: daemon.DaemonRoot(teamDir)}
				} else {
					return err
				}
			} else {
				client = daemonClientForFilters
			}
			filters, err = applyEventRuntimeFilter(teamDir, daemonClientForFilters, filters, runtimeFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team events: %v\n", err)
				return exitErr(2)
			}
			if len(phases) > 0 || staleOnly || runtimeStaleOnly || unhealthyOnly {
				filters, err = applyCurrentEventInstanceFilter(teamDir, filters, phases, staleOnly, runtimeStaleOnly, unhealthyOnly, time.Now())
				if err != nil {
					return err
				}
			}
			if latest || last > 0 {
				filters, err = applyLatestEventInstanceFilter(teamDir, daemonClientForFilters, filters, latest, last)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team events: %v\n", err)
					return exitErr(2)
				}
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return runEvents(ctx, cmd.OutOrStdout(), client, eventsOptions{Follow: follow, Tail: tail, JSON: jsonOut, Summary: summary, Format: formatTemplate, Filters: filters})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep streaming new lifecycle events.")
	cmd.Flags().IntVar(&tail, "tail", 0, "Show only the last N events before returning or following (0 = all). With non-following filters, N applies after filtering.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show events for the most recently started daemon-known instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show events for the N most recently started daemon-known instances after other filters (0 = all).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit raw JSONL events.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching events by action, status, agent, and instance.")
	cmd.Flags().StringVar(&format, "format", "", "Render each event with a Go template, e.g. '{{.Action}} {{.Instance}} {{.Status}}'.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only show events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show events for this instance. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show events for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show events with this lifecycle status. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show events for daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show events for instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show events for instances whose status.toml is currently stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Only show events for instances whose recorded runtime PID is currently no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show events for instances that are currently crashed, status-stale, or runtime-stale.")
	cmd.Flags().StringVar(&sinceRaw, "since", "", "Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	return cmd
}

type eventsOptions struct {
	Follow  bool
	Tail    int
	JSON    bool
	Summary bool
	Format  *template.Template
	Filters eventFilters
}

type eventFormatRow struct {
	TS        string
	Timestamp string
	Action    string
	Instance  string
	Agent     string
	Status    string
	PID       int
	Message   string
}

type eventFilters struct {
	actions          map[string]bool
	instances        map[string]bool
	instancePrefixes map[string]bool
	agents           map[string]bool
	statuses         map[string]bool
	since            *time.Time
}

type eventsClient interface {
	Events(ctx context.Context, follow bool, tailLines int) (io.ReadCloser, error)
}

type localEventsClient struct {
	daemonRoot string
}

func (c localEventsClient) Events(ctx context.Context, follow bool, tailLines int) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		err := daemon.StreamLifecycleEvents(ctx, pw, c.daemonRoot, follow, tailLines)
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

func runEvents(ctx context.Context, w io.Writer, client eventsClient, opts eventsOptions) error {
	tailLines := opts.Tail
	tailAfterFilters := opts.Tail > 0 && !opts.Follow && !opts.Filters.empty()
	if tailAfterFilters {
		tailLines = 0
	}
	rc, err := client.Events(ctx, opts.Follow, tailLines)
	if err != nil {
		return err
	}
	defer rc.Close()
	if tailAfterFilters {
		events, err := collectFilteredEventLines(rc, opts.Filters)
		if err != nil {
			return err
		}
		events = tailEventLines(events, opts.Tail)
		if opts.Summary {
			return renderEventSummaryLines(w, events, opts.JSON)
		}
		if opts.JSON {
			return renderEventJSONLines(w, events)
		}
		if opts.Format != nil {
			return renderEventFormatLines(w, events, opts.Format)
		}
		return renderEventTextLines(w, events)
	}
	if opts.Summary {
		return renderEventSummary(w, rc, opts.JSON, opts.Filters)
	}
	if opts.JSON && opts.Filters.empty() {
		_, err := io.Copy(w, rc)
		return err
	}
	if opts.Format != nil {
		return renderEventFormatStream(w, rc, opts.Filters, opts.Format)
	}
	return renderEventStream(w, rc, opts.Follow, opts.JSON, opts.Filters)
}

type filteredEventLine struct {
	raw string
	ev  daemon.LifecycleEvent
}

func parseEventFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("events-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func newEventFilters(actions, instances, agents, statuses []string, sinceRaw string, now func() time.Time) (eventFilters, error) {
	var filters eventFilters
	var err error
	if filters.actions, err = stringSetFilter(actions, "--action", "action"); err != nil {
		return filters, err
	}
	if filters.instances, err = stringSetFilter(instances, "--instance", "instance"); err != nil {
		return filters, err
	}
	if filters.agents, err = stringSetFilter(agents, "--agent", "agent"); err != nil {
		return filters, err
	}
	if filters.statuses, err = lifecycleStatusFilterSet(statuses); err != nil {
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

func stringSetFilter(values []string, flagName, noun string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			out[value] = true
		}
	}
	if len(out) == 0 {
		if len(values) > 0 {
			return nil, fmt.Errorf("%s requires at least one non-empty %s", flagName, noun)
		}
		return nil, nil
	}
	return out, nil
}

func (f eventFilters) empty() bool {
	return len(f.actions) == 0 && len(f.instances) == 0 && len(f.instancePrefixes) == 0 && len(f.agents) == 0 && len(f.statuses) == 0 && f.since == nil
}

func applyCurrentEventInstanceFilter(teamDir string, filters eventFilters, phases map[string]bool, staleOnly, runtimeStaleOnly, unhealthyOnly bool, now time.Time) (eventFilters, error) {
	if len(phases) == 0 && !staleOnly && !runtimeStaleOnly && !unhealthyOnly {
		return filters, nil
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return filters, err
	}
	rows = filterPsRows(rows, psOptions{phases: phases, stale: staleOnly, runtimeStale: runtimeStaleOnly, unhealthy: unhealthyOnly})
	instances := make(map[string]bool, len(rows))
	hasScope := eventFiltersHaveInstanceScope(filters)
	for _, row := range rows {
		if hasScope && !eventFilterMatchesInstance(filters, row.Instance) {
			continue
		}
		instances[row.Instance] = true
	}
	if len(instances) == 0 {
		instances[""] = false
	}
	filters.instances = instances
	filters.instancePrefixes = nil
	return filters, nil
}

func applyJobEventInstanceScope(filters eventFilters, jobs []*job.Job, jobsRaw []string, stepRaw string) (eventFilters, error) {
	jobIDs, err := jobIDSetFilter(jobsRaw, "--job")
	if err != nil {
		return filters, err
	}
	step := strings.TrimSpace(stepRaw)
	if len(jobIDs) == 0 && step == "" {
		return filters, nil
	}
	selected := map[string]bool{}
	for _, j := range jobs {
		if !jobMatchesIDFilter(j, jobIDs) {
			continue
		}
		addJobEventInstances(selected, j, step)
	}
	return restrictEventFilterToInstances(filters, selected), nil
}

func jobMatchesIDFilter(j *job.Job, ids map[string]bool) bool {
	if j == nil {
		return false
	}
	if len(ids) == 0 {
		return true
	}
	for _, raw := range []string{j.ID, j.Ticket, j.TicketURL} {
		if id := job.IDFromInput(raw); id != "" && ids[id] {
			return true
		}
	}
	return false
}

func addJobEventInstances(out map[string]bool, j *job.Job, step string) {
	if j == nil {
		return
	}
	if step == "" {
		if instance := strings.TrimSpace(j.Instance); instance != "" {
			out[instance] = true
		}
	}
	for _, s := range j.Steps {
		if step != "" && strings.TrimSpace(s.ID) != step {
			continue
		}
		if instance := strings.TrimSpace(s.Instance); instance != "" {
			out[instance] = true
		}
	}
}

func restrictEventFilterToInstances(filters eventFilters, selected map[string]bool) eventFilters {
	instances := map[string]bool{}
	for instance := range selected {
		if strings.TrimSpace(instance) == "" {
			continue
		}
		if eventFiltersHaveInstanceScope(filters) && !eventFilterMatchesInstance(filters, instance) {
			continue
		}
		instances[instance] = true
	}
	if len(instances) == 0 {
		instances[""] = false
	}
	filters.instances = instances
	filters.instancePrefixes = nil
	return filters
}

func applyLatestEventInstanceFilter(teamDir string, dc *daemonClient, filters eventFilters, latest bool, limit int) (eventFilters, error) {
	if !latest && limit <= 0 {
		return filters, nil
	}
	metas, err := eventFilterMetadata(teamDir, dc)
	if err != nil {
		return filters, err
	}
	selected := latestEventMetadataLimit(metas, filters, eventLatestLimit(latest, limit))
	instances := make(map[string]bool, len(selected))
	for _, meta := range selected {
		instances[meta.Instance] = true
	}
	if len(instances) == 0 {
		instances[""] = false
	}
	filters.instances = instances
	return filters, nil
}

func eventFilterMetadata(teamDir string, dc *daemonClient) ([]*daemon.Metadata, error) {
	if dc != nil {
		return dc.Instances()
	}
	return daemon.ListMetadata(daemon.DaemonRoot(teamDir))
}

func applyEventRuntimeFilter(teamDir string, dc *daemonClient, filters eventFilters, runtimeFilters []string) (eventFilters, error) {
	if len(runtimeFilters) == 0 {
		return filters, nil
	}
	runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
	if err != nil {
		return filters, err
	}
	metas, err := eventFilterMetadata(teamDir, dc)
	if err != nil {
		return filters, err
	}
	instances := make(map[string]bool, len(metas))
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		if eventFiltersHaveInstanceScope(filters) && !eventFilterMatchesInstance(filters, meta.Instance) {
			continue
		}
		if !runtimes[metadataRuntimeKey(meta)] {
			continue
		}
		instances[meta.Instance] = true
	}
	if len(instances) == 0 {
		instances[""] = false
	}
	filters.instances = instances
	filters.instancePrefixes = nil
	return filters, nil
}

func eventLatestLimit(latest bool, limit int) int {
	if latest {
		return 1
	}
	return limit
}

func latestEventMetadataLimit(metas []*daemon.Metadata, filters eventFilters, limit int) []*daemon.Metadata {
	if limit <= 0 || len(metas) == 0 {
		return metas
	}
	filtered := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		if eventFiltersHaveInstanceScope(filters) && !eventFilterMatchesInstance(filters, meta.Instance) {
			continue
		}
		if len(filters.agents) > 0 && !filters.agents[meta.Agent] {
			continue
		}
		if len(filters.statuses) > 0 && !filters.statuses[sendStatusKey(meta)] {
			continue
		}
		filtered = append(filtered, meta)
	}
	return latestMetadataByStartedLimit(filtered, limit)
}

func (f eventFilters) match(ev daemon.LifecycleEvent) bool {
	if f.since != nil && ev.TS.Before(*f.since) {
		return false
	}
	if len(f.actions) > 0 && !f.actions[ev.Action] {
		return false
	}
	if eventFiltersHaveInstanceScope(f) && !eventFilterMatchesInstance(f, ev.Instance) {
		return false
	}
	if len(f.agents) > 0 && !f.agents[ev.Agent] {
		return false
	}
	if len(f.statuses) > 0 && !f.statuses[eventStatusKey(ev)] {
		return false
	}
	return true
}

func eventFiltersHaveInstanceScope(filters eventFilters) bool {
	return len(filters.instances) > 0 || len(filters.instancePrefixes) > 0
}

func eventFilterMatchesInstance(filters eventFilters, instance string) bool {
	if filters.instances[instance] {
		return true
	}
	for prefix := range filters.instancePrefixes {
		if strings.HasPrefix(instance, prefix) {
			return true
		}
	}
	return false
}

func parseEventSince(raw string, now func() time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			return time.Time{}, fmt.Errorf("--since duration must be >= 0")
		}
		if now == nil {
			now = time.Now
		}
		return now().Add(-d), nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts, nil
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("--since must be a duration like 10m or an RFC3339 timestamp")
}

func renderEventStream(w io.Writer, r io.Reader, follow bool, jsonOut bool, filters eventFilters) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	count := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev daemon.LifecycleEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			if !jsonOut && filters.empty() {
				fmt.Fprintf(w, "invalid-event %s\n", line)
				count++
			}
			continue
		}
		if !filters.match(ev) {
			continue
		}
		if jsonOut {
			fmt.Fprintln(w, line)
		} else {
			renderEventLine(w, ev)
		}
		count++
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	}
	if count == 0 && !follow {
		if !jsonOut {
			fmt.Fprintln(w, "(no events)")
		}
	}
	return nil
}

func renderEventLine(w io.Writer, ev daemon.LifecycleEvent) {
	ts := "—"
	if !ev.TS.IsZero() {
		ts = ev.TS.Format(time.RFC3339)
	}
	instance := ev.Instance
	if instance == "" {
		instance = "—"
	}
	agent := ev.Agent
	if agent == "" {
		agent = "—"
	}
	status := eventStatusKey(ev)
	pid := "—"
	if ev.PID > 0 {
		pid = strconv.Itoa(ev.PID)
	}
	msg := ev.Message
	if msg == "" {
		msg = "—"
	}
	fmt.Fprintf(w, "%s  %-9s %-20s agent=%-14s status=%-8s pid=%-6s %s\n",
		ts, ev.Action, instance, agent, status, pid, msg)
}

func collectFilteredEventLines(r io.Reader, filters eventFilters) ([]filteredEventLine, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	events := []filteredEventLine{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev daemon.LifecycleEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if !filters.match(ev) {
			continue
		}
		events = append(events, filteredEventLine{raw: line, ev: ev})
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return events, nil
		}
		return events, err
	}
	return events, nil
}

func tailEventLines(events []filteredEventLine, tail int) []filteredEventLine {
	if tail <= 0 || len(events) <= tail {
		return events
	}
	return events[len(events)-tail:]
}

func renderEventTextLines(w io.Writer, events []filteredEventLine) error {
	if len(events) == 0 {
		_, err := fmt.Fprintln(w, "(no events)")
		return err
	}
	for _, line := range events {
		renderEventLine(w, line.ev)
	}
	return nil
}

func renderEventJSONLines(w io.Writer, events []filteredEventLine) error {
	for _, line := range events {
		if _, err := fmt.Fprintln(w, line.raw); err != nil {
			return err
		}
	}
	return nil
}

func renderEventFormatLines(w io.Writer, events []filteredEventLine, tmpl *template.Template) error {
	for _, line := range events {
		if err := tmpl.Execute(w, eventFormatRowFromEvent(line.ev)); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func renderEventFormatStream(w io.Writer, r io.Reader, filters eventFilters, tmpl *template.Template) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev daemon.LifecycleEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if !filters.match(ev) {
			continue
		}
		if err := tmpl.Execute(w, eventFormatRowFromEvent(ev)); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	}
	return nil
}

func eventFormatRowFromEvent(ev daemon.LifecycleEvent) eventFormatRow {
	ts := ""
	if !ev.TS.IsZero() {
		ts = ev.TS.Format(time.RFC3339)
	}
	return eventFormatRow{
		TS:        ts,
		Timestamp: ts,
		Action:    ev.Action,
		Instance:  ev.Instance,
		Agent:     ev.Agent,
		Status:    eventStatusKey(ev),
		PID:       ev.PID,
		Message:   ev.Message,
	}
}

type eventSummaryJSON struct {
	Total     int            `json:"total"`
	FirstTS   string         `json:"first_ts,omitempty"`
	LastTS    string         `json:"last_ts,omitempty"`
	Actions   map[string]int `json:"actions,omitempty"`
	Statuses  map[string]int `json:"statuses,omitempty"`
	Agents    map[string]int `json:"agents,omitempty"`
	Instances map[string]int `json:"instances,omitempty"`

	first time.Time
	last  time.Time
}

func renderEventSummary(w io.Writer, r io.Reader, jsonOut bool, filters eventFilters) error {
	summary, err := collectEventSummary(r, filters)
	if err != nil {
		return err
	}
	return renderEventSummaryResult(w, summary, jsonOut)
}

func renderEventSummaryLines(w io.Writer, events []filteredEventLine, jsonOut bool) error {
	summary := eventSummaryJSON{}
	for _, line := range events {
		summary.add(line.ev)
	}
	summary.finalize()
	return renderEventSummaryResult(w, summary, jsonOut)
}

func renderEventSummaryResult(w io.Writer, summary eventSummaryJSON, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	fmt.Fprintf(w, "events: total=%d", summary.Total)
	if summary.FirstTS != "" {
		fmt.Fprintf(w, " first=%s", summary.FirstTS)
	}
	if summary.LastTS != "" {
		fmt.Fprintf(w, " last=%s", summary.LastTS)
	}
	fmt.Fprintln(w)
	renderEventCountLine(w, "actions", summary.Actions)
	renderEventCountLine(w, "statuses", summary.Statuses)
	renderEventCountLine(w, "agents", summary.Agents)
	renderEventCountLine(w, "instances", summary.Instances)
	return nil
}

func collectEventSummary(r io.Reader, filters eventFilters) (eventSummaryJSON, error) {
	summary := eventSummaryJSON{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev daemon.LifecycleEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if !filters.match(ev) {
			continue
		}
		summary.add(ev)
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return summary, nil
		}
		return summary, err
	}
	summary.finalize()
	return summary, nil
}

func (s *eventSummaryJSON) add(ev daemon.LifecycleEvent) {
	s.Total++
	if !ev.TS.IsZero() {
		if s.first.IsZero() || ev.TS.Before(s.first) {
			s.first = ev.TS
		}
		if s.last.IsZero() || ev.TS.After(s.last) {
			s.last = ev.TS
		}
	}
	addEventCount(&s.Actions, ev.Action)
	addEventCount(&s.Statuses, eventStatusKey(ev))
	addEventCount(&s.Agents, ev.Agent)
	addEventCount(&s.Instances, ev.Instance)
}

func eventStatusKey(ev daemon.LifecycleEvent) string {
	if ev.Status == "" {
		return "unknown"
	}
	return string(ev.Status)
}

func (s *eventSummaryJSON) finalize() {
	if !s.first.IsZero() {
		s.FirstTS = s.first.Format(time.RFC3339)
	}
	if !s.last.IsZero() {
		s.LastTS = s.last.Format(time.RFC3339)
	}
}

func addEventCount(counts *map[string]int, key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if *counts == nil {
		*counts = map[string]int{}
	}
	(*counts)[key]++
}

func renderEventCountLine(w io.Writer, label string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	fmt.Fprintf(w, "%s: %s\n", label, formatEventCounts(counts))
}

func formatEventCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}
