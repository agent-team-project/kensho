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
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

func newQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and control persisted daemon event queue items.",
		Long:  "Inspect and control persisted daemon event queue items under `.agent_team/daemon/queue/`.",
	}
	cmd.AddCommand(newQueueLsCmd())
	cmd.AddCommand(newQueueShowCmd())
	cmd.AddCommand(newQueueRetryCmd())
	cmd.AddCommand(newQueueDropCmd())
	cmd.AddCommand(newQueuePruneCmd())
	return cmd
}

func newQueueLsCmd() *cobra.Command {
	var (
		target      string
		stateFilter string
		instances   []string
		eventTypes  []string
		readyOnly   bool
		watch       bool
		noClear     bool
		summary     bool
		jsonOut     bool
		format      string
		interval    time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List persisted queue items.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue ls: --interval must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue ls: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFilters(stateFilter, instances, eventTypes, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue ls: %v\n", err)
				return exitErr(2)
			}
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
				return runQueueListWatch(ctx, cmd.OutOrStdout(), teamDir, filters, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runQueueSummary(cmd.OutOrStdout(), teamDir, filters, jsonOut)
			}
			return runQueueList(cmd.OutOrStdout(), teamDir, filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "Filter by target instance name; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only show pending queue items whose next retry is due now.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the queue table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate queue counts instead of queue rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	return cmd
}

func newQueueShowCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
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
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue show: %v\n", err)
				return exitErr(2)
			}
			item, err := readQueueItemFromRepo(cmd, target, args[0])
			if err != nil {
				return err
			}
			return renderQueueItemResult(cmd.OutOrStdout(), item, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the queue item as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newQueueDropCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <id>",
		Short: "Drop a pending or dead-letter queue item.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			id := args[0]
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
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"dropped": true, "id": id})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Dropped queue item %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	return cmd
}

func newQueueRetryCmd() *cobra.Command {
	var (
		target      string
		jsonOut     bool
		retryAll    bool
		dryRun      bool
		stateFilter string
		instances   []string
		eventTypes  []string
		readyOnly   bool
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Retry pending or dead-letter queue items.",
		Long:  "Retry one queue item by id, or retry a filtered batch with --all. Batch retries default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if retryAll {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --all cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --limit must be >= 0.")
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFilters(effectiveState, instances, eventTypes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue retry: %v\n", err)
					return exitErr(2)
				}
				return runQueueRetryAll(cmd.OutOrStdout(), teamDir, filters, limit, dryRun, jsonOut)
			}
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: requires one id unless --all is set.")
				return exitErr(2)
			}
			if dryRun || stateFilter != "" || len(instances) > 0 || len(eventTypes) > 0 || readyOnly || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue retry: --dry-run, --state, --instance, --event-type, --ready, and --limit require --all.")
				return exitErr(2)
			}
			id := args[0]
			if dc, err := newDaemonClient(teamDir); err == nil {
				outcome, err := dc.QueueRetry(id)
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
				}
				renderQueueRetryOutcome(cmd.OutOrStdout(), outcome)
				return nil
			} else if !errors.Is(err, errDaemonNotRunning) {
				return err
			}

			item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue retry: queue item %q not found.\n", id)
					return exitErr(2)
				}
				return err
			}
			if err := daemon.ResetQueueItemForRetry(daemon.DaemonRoot(teamDir), item); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(item)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Queue item %s marked pending; start the daemon to dispatch it.\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching queue items without retrying them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&instances, "instance", nil, "With --all, filter by target instance name; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only retry pending queue items whose next retry is due now.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newQueuePruneCmd() *cobra.Command {
	var (
		target    string
		stateFlag string
		olderThan time.Duration
		dryRun    bool
		jsonOut   bool
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune persisted queue items.",
		Long:  "Prune persisted queue items. By default this removes dead-letter items.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueuePruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseQueuePruneState(stateFlag)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			results, err := pruneQueueItems(teamDir, state, olderThan, time.Now().UTC(), dryRun)
			if err != nil {
				return err
			}
			return renderQueuePruneResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&stateFlag, "state", daemon.QueueStateDead, "Queue state to prune: dead, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune items older than this duration based on retry/dead-letter/update time.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview queue items that would be pruned without dropping them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func readQueueItemFromRepo(cmd *cobra.Command, target, id string) (*daemon.QueueItem, error) {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return nil, err
	}
	item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue show: queue item %q not found.\n", id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	return item, nil
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
	state      string
	instances  map[string]bool
	eventTypes map[string]bool
	readyOnly  bool
	now        time.Time
}

func parseQueueListFilters(stateRaw string, instancesRaw, eventTypesRaw []string, readyOnly bool, now time.Time) (queueListFilters, error) {
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
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return queueListFilters{
		state:      state,
		instances:  instances,
		eventTypes: eventTypes,
		readyOnly:  readyOnly,
		now:        now,
	}, nil
}

func (f queueListFilters) withNow(now time.Time) queueListFilters {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	f.now = now
	return f
}

func (f queueListFilters) empty() bool {
	return f.state == "" && len(f.instances) == 0 && len(f.eventTypes) == 0 && !f.readyOnly
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

type queueRetryResult struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Instance   string `json:"instance"`
	InstanceID string `json:"instance_id"`
	Action     string `json:"action"`
	Reason     string `json:"reason,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

type queueSummary struct {
	Total     int            `json:"total"`
	Pending   int            `json:"pending"`
	Dead      int            `json:"dead"`
	Delayed   int            `json:"delayed"`
	Attempts  int            `json:"attempts"`
	Instances map[string]int `json:"instances"`
	Events    map[string]int `json:"events"`
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

func pruneQueueItems(teamDir, state string, olderThan time.Duration, now time.Time, dryRun bool) ([]queuePruneResult, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
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
		if !queueItemMatchesPrune(item, state, olderThan, now) {
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

func runQueueRetryAll(w io.Writer, teamDir string, filters queueListFilters, limit int, dryRun, jsonOut bool) error {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	matches := filterQueueItems(items, filters.withNow(time.Now().UTC()))
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	var dc *daemonClient
	if !dryRun {
		client, err := newDaemonClient(teamDir)
		if err == nil {
			dc = client
		} else if !errors.Is(err, errDaemonNotRunning) {
			return err
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
				return err
			}
			result.Action = outcome.Action
			result.Instance = outcome.Instance
			result.InstanceID = outcome.InstanceID
			result.Reason = outcome.Reason
		default:
			if err := daemon.ResetQueueItemForRetry(daemon.DaemonRoot(teamDir), item); err != nil {
				return err
			}
			result.Action = "reset"
		}
		results = append(results, result)
	}
	return renderQueueRetryResults(w, results, jsonOut)
}

func runQueueSummary(w io.Writer, teamDir string, filters queueListFilters, jsonOut bool) error {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	summary := summarizeQueueItems(filterQueueItems(items, filters.withNow(now)), now)
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
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

func summarizeQueueItems(items []*daemon.QueueItem, now time.Time) queueSummary {
	summary := queueSummary{
		Instances: map[string]int{},
		Events:    map[string]int{},
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
	}
	return summary
}

func renderQueueSummary(w io.Writer, summary queueSummary) {
	fmt.Fprintf(w, "queue: total=%d pending=%d dead=%d delayed=%d attempts=%d\n",
		summary.Total, summary.Pending, summary.Dead, summary.Delayed, summary.Attempts)
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
}

func queueItemMatchesPrune(item *daemon.QueueItem, state string, olderThan time.Duration, now time.Time) bool {
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

func renderQueueRetryResults(w io.Writer, results []queueRetryResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
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

func runQueueList(w io.Writer, teamDir string, filters queueListFilters, jsonOut bool, tmpl *template.Template) error {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	filtered := filterQueueItems(items, filters.withNow(time.Now().UTC()))
	if jsonOut {
		return json.NewEncoder(w).Encode(filtered)
	}
	if tmpl != nil {
		return renderQueueItemsFormat(w, filtered, tmpl)
	}
	renderQueueTable(w, filtered)
	return nil
}

func runQueueListWatch(ctx context.Context, w io.Writer, teamDir string, filters queueListFilters, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runQueueList(w, teamDir, filters, jsonOut, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
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

func renderQueueItemResult(w io.Writer, item *daemon.QueueItem, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(item)
	}
	if tmpl != nil {
		return renderQueueItemTemplate(w, item, tmpl)
	}
	renderQueueDetail(w, item)
	return nil
}

func renderQueueItemTemplate(w io.Writer, item *daemon.QueueItem, tmpl *template.Template) error {
	if err := tmpl.Execute(w, item); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderQueueTable(w io.Writer, items []*daemon.QueueItem) {
	if len(items) == 0 {
		fmt.Fprintln(w, "(no queue items)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tINSTANCE\tINSTANCE_ID\tATTEMPTS\tNEXT_RETRY\tLAST_ERROR")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			item.ID, item.State, item.Instance, item.InstanceID, item.Attempts, queueTime(item.NextRetry), emptyDash(item.LastError))
	}
	_ = tw.Flush()
}

func renderQueueDetail(w io.Writer, item *daemon.QueueItem) {
	fmt.Fprintf(w, "ID:          %s\n", item.ID)
	fmt.Fprintf(w, "State:       %s\n", item.State)
	fmt.Fprintf(w, "Event:       %s\n", item.EventType)
	fmt.Fprintf(w, "Instance:    %s\n", item.Instance)
	fmt.Fprintf(w, "Instance ID: %s\n", item.InstanceID)
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
	if len(item.Payload) > 0 {
		body, _ := json.MarshalIndent(item.Payload, "", "  ")
		fmt.Fprintf(w, "Payload:\n%s\n", string(body))
	}
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
