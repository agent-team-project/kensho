package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newSnapshotCmd() *cobra.Command {
	var (
		target        string
		output        string
		jsonOut       bool
		eventLimit    int
		scheduleLimit int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture a read-only orchestration diagnostic report.",
		Long: "Capture a read-only diagnostic report with health, plan, instance, job, queue, schedule, " +
			"runtime, and recent lifecycle event state. Use --json for stdout or --output to write a JSON file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if eventLimit < -1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot: --events must be >= -1.")
				return exitErr(2)
			}
			if scheduleLimit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot: --schedule-limit must be >= 0.")
				return exitErr(2)
			}
			if jsonOut && output != "" && output != "-" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot: choose one of --json or --output.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			repoRoot, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			snapshot := collectSnapshot(teamDir, repoRoot, snapshotOptions{
				EventLimit:    eventLimit,
				ScheduleLimit: scheduleLimit,
				Now:           time.Now().UTC(),
			})
			switch {
			case jsonOut || output == "-":
				return writeSnapshotJSON(cmd.OutOrStdout(), snapshot)
			case output != "":
				path, err := writeSnapshotFile(output, snapshot)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote snapshot to %s\n", path)
				return nil
			default:
				renderSnapshotSummary(cmd.OutOrStdout(), snapshot)
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the full JSON snapshot to this file. Use '-' for stdout.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the full snapshot JSON to stdout.")
	cmd.Flags().IntVar(&eventLimit, "events", 50, "Recent lifecycle events to include. Use -1 for all events or 0 to skip events.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 10, "Upcoming schedules to include after ordering; 0 means all.")
	return cmd
}

type snapshotOptions struct {
	EventLimit    int
	ScheduleLimit int
	Now           time.Time
}

type snapshotResult struct {
	Version       string                  `json:"version"`
	CapturedAt    string                  `json:"captured_at"`
	Repo          string                  `json:"repo"`
	TeamDir       string                  `json:"team_dir"`
	Runtime       *runtimeInfo            `json:"runtime,omitempty"`
	Health        *healthResult           `json:"health,omitempty"`
	Plan          *planResult             `json:"plan,omitempty"`
	Instances     []psJSONRow             `json:"instances,omitempty"`
	Jobs          []*job.Job              `json:"jobs,omitempty"`
	Queue         []*daemon.QueueItem     `json:"queue,omitempty"`
	QueueSummary  *queueSummary           `json:"queue_summary,omitempty"`
	Schedules     []scheduleInfo          `json:"schedules,omitempty"`
	ScheduleNext  []scheduleInfo          `json:"schedule_next,omitempty"`
	Events        []daemon.LifecycleEvent `json:"events,omitempty"`
	SectionErrors map[string]string       `json:"section_errors,omitempty"`
}

func collectSnapshot(teamDir, repoRoot string, opts snapshotOptions) *snapshotResult {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := &snapshotResult{
		Version:    Version,
		CapturedAt: now.UTC().Format(time.RFC3339),
		Repo:       filepath.ToSlash(repoRoot),
		TeamDir:    filepath.ToSlash(teamDir),
	}

	if runtime, err := collectRuntimeInfo(); err != nil {
		out.addError("runtime", err)
	} else {
		out.Runtime = &runtime
	}
	if health, err := collectHealth(teamDir, now); err != nil {
		out.addError("health", err)
	} else {
		out.Health = health
	}
	if plan, err := collectPlan(teamDir); err != nil {
		out.addError("plan", err)
	} else {
		out.Plan = plan
	}
	if rows, err := collectPsRows(teamDir, now); err != nil {
		out.addError("instances", err)
	} else {
		out.Instances = psJSONRows(rows)
	}
	if jobs, err := job.List(teamDir); err != nil {
		out.addError("jobs", err)
	} else {
		out.Jobs = jobs
	}
	if queue, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir)); err != nil {
		out.addError("queue", err)
	} else {
		out.Queue = queue
		summary := summarizeQueueItems(queue, now)
		out.QueueSummary = &summary
	}
	if schedules, err := loadScheduleInfos(teamDir); err != nil {
		out.addError("schedules", err)
	} else {
		out.Schedules = schedules
		out.ScheduleNext = nextScheduleRows(schedules, now, opts.ScheduleLimit)
	}
	if events, err := collectSnapshotEvents(teamDir, opts.EventLimit); err != nil {
		out.addError("events", err)
	} else {
		out.Events = events
	}
	return out
}

func (r *snapshotResult) addError(section string, err error) {
	if err == nil {
		return
	}
	if r.SectionErrors == nil {
		r.SectionErrors = map[string]string{}
	}
	r.SectionErrors[section] = err.Error()
}

func collectSnapshotEvents(teamDir string, limit int) ([]daemon.LifecycleEvent, error) {
	if limit == 0 {
		return nil, nil
	}
	tail := limit
	if limit < 0 {
		tail = 0
	}
	var buf bytes.Buffer
	if err := daemon.StreamLifecycleEvents(context.Background(), &buf, daemon.DaemonRoot(teamDir), false, tail); err != nil {
		return nil, err
	}
	lines, err := collectFilteredEventLines(&buf, eventFilters{})
	if err != nil {
		return nil, err
	}
	out := make([]daemon.LifecycleEvent, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.ev)
	}
	return out, nil
}

func writeSnapshotFile(path string, snapshot *snapshotResult) (string, error) {
	path = filepath.Clean(path)
	body, err := snapshotJSON(snapshot)
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

func writeSnapshotJSON(w io.Writer, snapshot *snapshotResult) error {
	body, err := snapshotJSON(snapshot)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func snapshotJSON(snapshot *snapshotResult) ([]byte, error) {
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func renderSnapshotSummary(w io.Writer, snapshot *snapshotResult) {
	if snapshot == nil {
		fmt.Fprintln(w, "snapshot: unavailable")
		return
	}
	fmt.Fprintf(w, "snapshot: %s\n", snapshot.CapturedAt)
	fmt.Fprintf(w, "repo: %s\n", snapshot.Repo)
	if snapshot.Health != nil {
		fmt.Fprintf(w, "health: %s\n", repairHealthState(snapshot.Health))
	}
	if snapshot.Plan != nil {
		fmt.Fprintf(w, "plan: total=%d start=%d resume=%d keep=%d on_demand=%d extra=%d\n",
			snapshot.Plan.Summary.Total,
			snapshot.Plan.Summary.Start,
			snapshot.Plan.Summary.Resume,
			snapshot.Plan.Summary.Keep,
			snapshot.Plan.Summary.OnDemand,
			snapshot.Plan.Summary.Extra)
	}
	fmt.Fprintf(w, "instances: %d\n", len(snapshot.Instances))
	renderSnapshotJobSummary(w, snapshot.Jobs)
	if snapshot.QueueSummary != nil {
		fmt.Fprintf(w, "queue: total=%d pending=%d dead=%d delayed=%d attempts=%d\n",
			snapshot.QueueSummary.Total,
			snapshot.QueueSummary.Pending,
			snapshot.QueueSummary.Dead,
			snapshot.QueueSummary.Delayed,
			snapshot.QueueSummary.Attempts)
	}
	fmt.Fprintf(w, "schedules: declared=%d upcoming=%d\n", len(snapshot.Schedules), len(snapshot.ScheduleNext))
	fmt.Fprintf(w, "events: %d\n", len(snapshot.Events))
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

func renderSnapshotJobSummary(w io.Writer, jobs []*job.Job) {
	counts := map[job.Status]int{}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		counts[j.Status]++
	}
	fmt.Fprintf(w, "jobs: total=%d queued=%d running=%d blocked=%d done=%d failed=%d\n",
		len(jobs),
		counts[job.StatusQueued],
		counts[job.StatusRunning],
		counts[job.StatusBlocked],
		counts[job.StatusDone],
		counts[job.StatusFailed])
}
