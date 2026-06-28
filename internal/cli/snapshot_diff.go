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

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newSnapshotDiffCmd() *cobra.Command {
	var (
		jsonOut       bool
		exitCode      bool
		output        string
		sections      []string
		actions       []string
		format        string
		limit         int
		sortBy        string
		summary       bool
		currentAfter  bool
		currentBefore bool
		eventLimit    int
		intakeLimit   int
		scheduleLimit int
		timelineTail  string
		noRedact      bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "diff <before.json> <after.json> | <snapshot.json> (--current-after|--current-before)",
		Short: "Compare two saved diagnostic snapshots.",
		Long: "Compare two saved global, team, pipeline, or job diagnostic snapshot JSON files and summarize " +
			"provenance, git, runtime, health, plan, triage, next-action, instance, job, inbox, outbox, queue, schedule, intake, event, timeline, pipeline, ready-advance, and section-error changes. " +
			"Use --current-after or --current-before to compare one saved snapshot against the current repo state for the saved snapshot scope.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if currentAfter && currentBefore {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: choose one of --current-after or --current-before.")
				return exitErr(2)
			}
			currentRequested := currentAfter || currentBefore
			if currentRequested {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: pass exactly one snapshot file with --current-after or --current-before.")
					return exitErr(2)
				}
			} else if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: pass two snapshot files, or one file with --current-after/--current-before.")
				return exitErr(2)
			}
			if !currentRequested && (cmd.Flags().Changed("events") || cmd.Flags().Changed("intake-deliveries") || cmd.Flags().Changed("schedule-limit") || cmd.Flags().Changed("timeline") || cmd.Flags().Changed("no-redact")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: current snapshot options require --current-after or --current-before.")
				return exitErr(2)
			}
			if eventLimit < -1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --events must be >= -1.")
				return exitErr(2)
			}
			if intakeLimit < -1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --intake-deliveries must be >= -1.")
				return exitErr(2)
			}
			if scheduleLimit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --schedule-limit must be >= 0.")
				return exitErr(2)
			}
			timelineEvents, err := parseLogTail(timelineTail)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --timeline must be >= 0 or \"all\".")
				return exitErr(2)
			}
			if jsonOut && output != "" && output != "-" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: choose one of --json or --output.")
				return exitErr(2)
			}
			if format != "" && output != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --format cannot be combined with --output.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --limit must be >= 0.")
				return exitErr(2)
			}
			if summary && limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --summary cannot be combined with --limit.")
				return exitErr(2)
			}
			sortMode, err := parseSnapshotDiffSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team snapshot diff: %v\n", err)
				return exitErr(2)
			}
			if summary && sortMode != snapshotDiffSortSection {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team snapshot diff: --summary cannot be combined with --sort.")
				return exitErr(2)
			}
			formatTemplate, err := parseSnapshotDiffFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team snapshot diff: %v\n", err)
				return exitErr(2)
			}
			sectionSet, err := parseSnapshotDiffSections(sections)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team snapshot diff: %v\n", err)
				return exitErr(2)
			}
			actionSet, actionLabels, err := parseSnapshotDiffActions(actions)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team snapshot diff: %v\n", err)
				return exitErr(2)
			}
			result, err := diffSnapshotInputs(cmd, cwd, args, snapshotDiffOptions{Sections: sectionSet}, snapshotDiffCurrentOptions{
				Events:           eventLimit,
				IntakeDeliveries: intakeLimit,
				ScheduleLimit:    scheduleLimit,
				TimelineTail:     timelineEvents,
				Redact:           !noRedact,
				CurrentAfter:     currentAfter,
				CurrentBefore:    currentBefore,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team snapshot diff: %v\n", err)
				return exitErr(1)
			}
			filterSnapshotDiffResultByActions(result, actionSet, actionLabels)
			if summary {
				summarizeSnapshotDiffResult(result)
			} else {
				sortSnapshotDiffResult(result, sortMode)
				limitSnapshotDiffResult(result, limit)
			}
			if jsonOut || output == "-" {
				if err := writeSnapshotDiffResultJSON(cmd.OutOrStdout(), result); err != nil {
					return err
				}
			} else if output != "" {
				path, err := writeSnapshotDiffFile(output, result)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote snapshot diff to %s\n", path)
			} else if formatTemplate != nil {
				if err := renderSnapshotDiffFormat(cmd.OutOrStdout(), result, formatTemplate); err != nil {
					return err
				}
			} else {
				renderSnapshotDiff(cmd.OutOrStdout(), result)
			}
			if exitCode && result.Summary.TotalChanges > 0 {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit snapshot diff as JSON.")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the JSON snapshot diff to this file. Use '-' for stdout.")
	cmd.Flags().BoolVar(&exitCode, "exit-code", false, "Exit with status 1 when snapshots differ.")
	cmd.Flags().StringSliceVar(&sections, "section", nil, "Only compare sections: provenance, git, runtime, health, plan, triage, next, instances, jobs, job_quarantine, pipelines, inbox, outbox, outbox_quarantine, queue, queue_quarantine, schedules, intake, events, timeline, advance, section_errors, quarantine, or all. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actions, "action", nil, "Only compare change actions: added, removed, or changed. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&format, "format", "", "Render the diff result with a Go template, e.g. '{{.Summary.TotalChanges}} {{len .Changes}}'.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit emitted change detail rows after summarizing all changes; 0 means all.")
	cmd.Flags().StringVar(&sortBy, "sort", "section", "Sort emitted change detail rows by section, action, or id before applying --limit.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Only emit metadata and summary counters; suppress change detail rows.")
	cmd.Flags().BoolVar(&currentAfter, "current-after", false, "Compare the saved snapshot argument against the current repo state for the saved snapshot scope as the after snapshot.")
	cmd.Flags().BoolVar(&currentBefore, "current-before", false, "Compare the current repo state for the saved snapshot scope as the before snapshot against the saved snapshot argument.")
	cmd.Flags().IntVar(&eventLimit, "events", 50, "With --current-after/--current-before, recent lifecycle events to include. Use -1 for all events or 0 to skip events.")
	cmd.Flags().IntVar(&intakeLimit, "intake-deliveries", 50, "With --current-after/--current-before, recent intake deliveries to include. Use -1 for all deliveries or 0 to skip deliveries.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 10, "With --current-after/--current-before, upcoming schedules to include after ordering; 0 means all.")
	cmd.Flags().StringVar(&timelineTail, "timeline", "50", "With --current-after/--current-before on pipeline snapshots, include the last N combined audit/lifecycle timeline rows (0 or all = all).")
	cmd.Flags().BoolVar(&noRedact, "no-redact", false, "With --current-after/--current-before, include raw payload values instead of redacting sensitive keys.")
	return cmd
}

type snapshotDiffCurrentOptions struct {
	Events           int
	IntakeDeliveries int
	ScheduleLimit    int
	TimelineTail     int
	Redact           bool
	CurrentAfter     bool
	CurrentBefore    bool
}

const (
	snapshotDiffSortSection = "section"
	snapshotDiffSortAction  = "action"
	snapshotDiffSortID      = "id"
)

type snapshotDiffInput struct {
	Version          string                         `json:"version,omitempty"`
	CapturedAt       string                         `json:"captured_at,omitempty"`
	Repo             string                         `json:"repo,omitempty"`
	Provenance       *snapshotProvenance            `json:"provenance,omitempty"`
	Git              *snapshotGitInfo               `json:"git,omitempty"`
	Runtime          *runtimeInfo                   `json:"runtime,omitempty"`
	Health           *healthResult                  `json:"health,omitempty"`
	Plan             *planResult                    `json:"plan,omitempty"`
	JobTriage        *jobTriageSnapshot             `json:"job_triage,omitempty"`
	Next             *nextActionResult              `json:"next,omitempty"`
	Actions          []string                       `json:"actions,omitempty"`
	Team             *teamInfo                      `json:"team,omitempty"`
	Pipeline         string                         `json:"pipeline,omitempty"`
	Job              *snapshotDiffDetailedJob       `json:"job,omitempty"`
	Instance         string                         `json:"instance,omitempty"`
	State            *jobSnapshotState              `json:"state,omitempty"`
	Status           *snapshotDiffStatus            `json:"status,omitempty"`
	Log              *jobSnapshotFile               `json:"log,omitempty"`
	LastMessage      *jobSnapshotFile               `json:"last_message,omitempty"`
	Instances        []snapshotDiffInstance         `json:"instances,omitempty"`
	Jobs             []snapshotDiffJob              `json:"jobs,omitempty"`
	JobQuarantine    []jobQuarantineItem            `json:"job_quarantine,omitempty"`
	Inbox            []snapshotDiffInbox            `json:"inbox,omitempty"`
	Outbox           []snapshotDiffOutboxItem       `json:"outbox,omitempty"`
	OutboxQuarantine []snapshotDiffOutboxQuarantine `json:"outbox_quarantine,omitempty"`
	Queue            []snapshotDiffQueueItem        `json:"queue,omitempty"`
	QueueQuarantine  []snapshotDiffQuarantine       `json:"queue_quarantine,omitempty"`
	Schedules        []snapshotDiffSchedule         `json:"schedules,omitempty"`
	ScheduleNext     []snapshotDiffSchedule         `json:"schedule_next,omitempty"`
	Intake           []snapshotDiffIntake           `json:"intake,omitempty"`
	IntakeDuplicates []snapshotDiffIntakeDuplicate  `json:"intake_duplicates,omitempty"`
	Events           []snapshotDiffEvent            `json:"events,omitempty"`
	JobEvents        []snapshotDiffJobEvent         `json:"job_events,omitempty"`
	LifecycleEvents  []snapshotDiffEvent            `json:"lifecycle_events,omitempty"`
	Timeline         []snapshotDiffTimelineEntry    `json:"timeline,omitempty"`
	PipelineStatus   []pipelineStatusRow            `json:"pipeline_status,omitempty"`
	PipelineAdvance  []snapshotDiffAdvance          `json:"pipeline_advance_preview,omitempty"`
	AdvancePreview   []snapshotDiffAdvance          `json:"advance_preview,omitempty"`
	SectionErrors    map[string]string              `json:"section_errors,omitempty"`
}

type snapshotDiffDetailedJob struct {
	ID         string
	Ticket     string
	TicketURL  string
	Target     string
	Instance   string
	Pipeline   string
	Status     string
	Held       bool
	HoldReason string
	Branch     string
	Worktree   string
	PR         string
	LastEvent  string
	LastStatus string
	Steps      []snapshotDiffDetailedJobStep
}

type snapshotDiffDetailedJobStep struct {
	ID          string
	Target      string
	Workspace   string
	Runtime     string
	RuntimeBin  string
	Status      string
	Instance    string
	Gate        string
	Optional    bool
	Attempts    int
	MaxAttempts int
	Skipped     bool
	SkipReason  string
}

type snapshotDiffStatus struct {
	Pipeline           string               `json:"pipeline,omitempty"`
	Jobs               int                  `json:"jobs,omitempty"`
	ReadySteps         int                  `json:"ready_steps,omitempty"`
	QueuedSteps        int                  `json:"queued_steps,omitempty"`
	RunningSteps       int                  `json:"running_steps,omitempty"`
	BlockedSteps       int                  `json:"blocked_steps,omitempty"`
	ManualGates        int                  `json:"manual_gates,omitempty"`
	FailedSteps        int                  `json:"failed_steps,omitempty"`
	DoneSteps          int                  `json:"done_steps,omitempty"`
	Phase              string               `json:"phase,omitempty"`
	Description        string               `json:"description,omitempty"`
	Since              string               `json:"since,omitempty"`
	LastAction         string               `json:"last_action,omitempty"`
	Stale              bool                 `json:"stale,omitempty"`
	Work               *inspectWorkJSON     `json:"work,omitempty"`
	Blocking           *inspectBlockingJSON `json:"blocking,omitempty"`
	ParallelReadySteps int                  `json:"parallel_ready_steps,omitempty"`
	StaleRunningSteps  int                  `json:"stale_running_steps,omitempty"`
	HeldSteps          int                  `json:"held_steps,omitempty"`
	QueuePending       int                  `json:"queue_pending,omitempty"`
	QueueDead          int                  `json:"queue_dead,omitempty"`
	QueueQuarantined   int                  `json:"queue_quarantined,omitempty"`
	QueueRestorable    int                  `json:"queue_restorable,omitempty"`
	QueueUnrestorable  int                  `json:"queue_unrestorable,omitempty"`
	Actions            []string             `json:"actions,omitempty"`
}

func (s *snapshotDiffStatus) pipelineRow(fallbackPipeline string) (pipelineStatusRow, bool) {
	if s == nil {
		return pipelineStatusRow{}, false
	}
	pipeline := strings.TrimSpace(s.Pipeline)
	if pipeline == "" {
		pipeline = strings.TrimSpace(fallbackPipeline)
	}
	if pipeline == "" {
		return pipelineStatusRow{}, false
	}
	if s.Jobs == 0 &&
		s.ReadySteps == 0 &&
		s.QueuedSteps == 0 &&
		s.RunningSteps == 0 &&
		s.BlockedSteps == 0 &&
		s.ManualGates == 0 &&
		s.FailedSteps == 0 &&
		s.DoneSteps == 0 &&
		s.ParallelReadySteps == 0 &&
		s.StaleRunningSteps == 0 &&
		s.HeldSteps == 0 &&
		s.QueuePending == 0 &&
		s.QueueDead == 0 &&
		s.QueueQuarantined == 0 &&
		s.QueueRestorable == 0 &&
		s.QueueUnrestorable == 0 {
		return pipelineStatusRow{}, false
	}
	return pipelineStatusRow{
		Pipeline:           pipeline,
		Jobs:               s.Jobs,
		ReadySteps:         s.ReadySteps,
		ParallelReadySteps: s.ParallelReadySteps,
		QueuedSteps:        s.QueuedSteps,
		RunningSteps:       s.RunningSteps,
		StaleRunningSteps:  s.StaleRunningSteps,
		BlockedSteps:       s.BlockedSteps,
		ManualGates:        s.ManualGates,
		FailedSteps:        s.FailedSteps,
		HeldSteps:          s.HeldSteps,
		DoneSteps:          s.DoneSteps,
		QueuePending:       s.QueuePending,
		QueueDead:          s.QueueDead,
		QueueQuarantined:   s.QueueQuarantined,
		QueueRestorable:    s.QueueRestorable,
		QueueUnrestorable:  s.QueueUnrestorable,
		Actions:            s.Actions,
	}, true
}

type snapshotDiffJob struct {
	ID       string `json:"id"`
	Status   string `json:"status,omitempty"`
	Pipeline string `json:"pipeline,omitempty"`
	Target   string `json:"target,omitempty"`
	Instance string `json:"instance,omitempty"`
}

type snapshotDiffInstance struct {
	Instance string `json:"instance"`
	Agent    string `json:"agent,omitempty"`
	Status   string `json:"status,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Runtime  string `json:"runtime,omitempty"`
	Job      string `json:"job,omitempty"`
	Stale    bool   `json:"stale,omitempty"`
}

type snapshotDiffQueueItem struct {
	ID    string `json:"id"`
	State string `json:"state,omitempty"`
}

type snapshotDiffOutboxItem struct {
	ID        string         `json:"id"`
	State     string         `json:"state,omitempty"`
	Type      string         `json:"type,omitempty"`
	Source    string         `json:"source,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	LastError string         `json:"last_error,omitempty"`
}

type snapshotDiffInbox struct {
	Instance   string `json:"instance"`
	Agent      string `json:"agent,omitempty"`
	Status     string `json:"status,omitempty"`
	Total      int    `json:"total,omitempty"`
	Unread     int    `json:"unread,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
	LatestID   string `json:"latest_id,omitempty"`
	LatestFrom string `json:"latest_from,omitempty"`
	LatestTS   string `json:"latest_ts,omitempty"`
}

type snapshotDiffQuarantine struct {
	Path       string `json:"path"`
	State      string `json:"state,omitempty"`
	ID         string `json:"id,omitempty"`
	EventType  string `json:"event_type,omitempty"`
	Instance   string `json:"instance,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Job        string `json:"job,omitempty"`
	Restorable bool   `json:"restorable"`
	Problem    string `json:"problem,omitempty"`
}

type snapshotDiffOutboxQuarantine struct {
	Path       string `json:"path"`
	State      string `json:"state,omitempty"`
	ID         string `json:"id,omitempty"`
	Type       string `json:"type,omitempty"`
	Source     string `json:"source,omitempty"`
	Job        string `json:"job,omitempty"`
	Instance   string `json:"instance,omitempty"`
	Target     string `json:"target,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Restorable bool   `json:"restorable"`
	Problem    string `json:"problem,omitempty"`
}

type snapshotDiffSchedule struct {
	Name        string `json:"name"`
	Event       string `json:"event,omitempty"`
	Every       string `json:"every,omitempty"`
	RunOnStart  bool   `json:"run_on_start,omitempty"`
	LastFiredAt string `json:"last_fired_at,omitempty"`
	NextRun     string `json:"next_run_at,omitempty"`
	Due         bool   `json:"due,omitempty"`
	DueReason   string `json:"due_reason,omitempty"`
}

type snapshotDiffIntake struct {
	ID           string `json:"id"`
	Time         string `json:"time,omitempty"`
	Provider     string `json:"provider,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	EventType    string `json:"event_type,omitempty"`
	Ticket       string `json:"ticket,omitempty"`
	PR           string `json:"pr,omitempty"`
	JobID        string `json:"job_id,omitempty"`
	Status       string `json:"status,omitempty"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	ReplayStatus string `json:"replay_status,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
}

type snapshotDiffIntakeDuplicate struct {
	Provider  string   `json:"provider,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
	Count     int      `json:"count,omitempty"`
	IDs       []string `json:"ids,omitempty"`
}

type snapshotDiffEvent struct {
	ID       string `json:"id"`
	TS       string `json:"ts,omitempty"`
	Action   string `json:"action,omitempty"`
	Instance string `json:"instance,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Job      string `json:"job,omitempty"`
	Ticket   string `json:"ticket,omitempty"`
	Branch   string `json:"branch,omitempty"`
	PR       string `json:"pr,omitempty"`
	Status   string `json:"status,omitempty"`
	Message  string `json:"message,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

type snapshotDiffJobEvent struct {
	TS       string            `json:"ts,omitempty"`
	JobID    string            `json:"job_id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Status   string            `json:"status,omitempty"`
	Instance string            `json:"instance,omitempty"`
	Message  string            `json:"message,omitempty"`
	Actor    string            `json:"actor,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
}

type snapshotDiffTimelineEntry struct {
	TS       string            `json:"ts,omitempty"`
	Source   string            `json:"source,omitempty"`
	JobID    string            `json:"job_id,omitempty"`
	Kind     string            `json:"kind,omitempty"`
	Status   string            `json:"status,omitempty"`
	Instance string            `json:"instance,omitempty"`
	Actor    string            `json:"actor,omitempty"`
	Agent    string            `json:"agent,omitempty"`
	Ticket   string            `json:"ticket,omitempty"`
	Branch   string            `json:"branch,omitempty"`
	PR       string            `json:"pr,omitempty"`
	PID      int               `json:"pid,omitempty"`
	ExitCode *int              `json:"exit_code,omitempty"`
	Message  string            `json:"message,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
}

type snapshotDiffAdvance struct {
	JobID      string `json:"job_id"`
	Pipeline   string `json:"pipeline,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	Target     string `json:"target,omitempty"`
	StepStatus string `json:"step_status,omitempty"`
	Action     string `json:"action,omitempty"`
}

type snapshotDiffResult struct {
	Before  snapshotDiffMeta     `json:"before"`
	After   snapshotDiffMeta     `json:"after"`
	Summary snapshotDiffSummary  `json:"summary"`
	Changes []snapshotDiffChange `json:"changes,omitempty"`
}

type snapshotDiffMeta struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Scope      string `json:"scope,omitempty"`
	CapturedAt string `json:"captured_at,omitempty"`
	Repo       string `json:"repo,omitempty"`
}

type snapshotDiffSummary struct {
	TotalChanges     int                  `json:"total_changes"`
	ShownChanges     int                  `json:"shown_changes,omitempty"`
	OmittedChanges   int                  `json:"omitted_changes,omitempty"`
	DetailLimit      int                  `json:"detail_limit,omitempty"`
	DetailSort       string               `json:"detail_sort,omitempty"`
	ActionFilter     []string             `json:"action_filter,omitempty"`
	SummaryOnly      bool                 `json:"summary_only,omitempty"`
	Provenance       snapshotDiffCounters `json:"provenance"`
	Git              snapshotDiffCounters `json:"git"`
	Runtime          snapshotDiffCounters `json:"runtime"`
	Health           snapshotDiffCounters `json:"health"`
	Plan             snapshotDiffCounters `json:"plan"`
	Triage           snapshotDiffCounters `json:"triage"`
	Next             snapshotDiffCounters `json:"next"`
	Instances        snapshotDiffCounters `json:"instances"`
	Jobs             snapshotDiffCounters `json:"jobs"`
	JobQuarantine    snapshotDiffCounters `json:"job_quarantine"`
	Pipelines        snapshotDiffCounters `json:"pipelines"`
	Inbox            snapshotDiffCounters `json:"inbox"`
	Outbox           snapshotDiffCounters `json:"outbox"`
	OutboxQuarantine snapshotDiffCounters `json:"outbox_quarantine"`
	Queue            snapshotDiffCounters `json:"queue"`
	QueueQuarantine  snapshotDiffCounters `json:"queue_quarantine"`
	Schedules        snapshotDiffCounters `json:"schedules"`
	Intake           snapshotDiffCounters `json:"intake"`
	Events           snapshotDiffCounters `json:"events"`
	Timeline         snapshotDiffCounters `json:"timeline"`
	Advance          snapshotDiffCounters `json:"advance"`
	SectionErrors    snapshotDiffCounters `json:"section_errors"`
}

type snapshotDiffCounters struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

type snapshotDiffChange struct {
	Section string `json:"section"`
	ID      string `json:"id"`
	Action  string `json:"action"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
}

type snapshotDiffComparable struct {
	Meta             snapshotDiffMeta
	Provenance       map[string]string
	Git              map[string]string
	Runtime          map[string]string
	Health           map[string]string
	Plan             map[string]string
	Triage           map[string]string
	Next             map[string]string
	Instances        map[string]string
	Jobs             map[string]string
	JobQuarantine    map[string]string
	Pipelines        map[string]string
	Inbox            map[string]string
	Outbox           map[string]string
	OutboxQuarantine map[string]string
	Queue            map[string]string
	QueueQuarantine  map[string]string
	Schedules        map[string]string
	Intake           map[string]string
	Events           map[string]string
	Timeline         map[string]string
	Advance          map[string]string
	SectionErrors    map[string]string
}

type snapshotDiffOptions struct {
	Sections map[string]bool
}

func diffSnapshotInputs(cmd *cobra.Command, defaultRepo string, args []string, opts snapshotDiffOptions, current snapshotDiffCurrentOptions) (*snapshotDiffResult, error) {
	if current.CurrentAfter || current.CurrentBefore {
		fileComparable, err := readSnapshotDiffComparable(args[0])
		if err != nil {
			return nil, err
		}
		currentComparable, err := collectCurrentSnapshotDiffComparable(cmd, defaultRepo, current, fileComparable.Meta)
		if err != nil {
			return nil, err
		}
		if current.CurrentBefore {
			return diffSnapshotComparables(currentComparable, fileComparable, opts), nil
		}
		return diffSnapshotComparables(fileComparable, currentComparable, opts), nil
	}
	return diffSnapshotFiles(args[0], args[1], opts)
}

func diffSnapshotFiles(beforePath, afterPath string, opts snapshotDiffOptions) (*snapshotDiffResult, error) {
	before, err := readSnapshotDiffComparable(beforePath)
	if err != nil {
		return nil, err
	}
	after, err := readSnapshotDiffComparable(afterPath)
	if err != nil {
		return nil, err
	}
	return diffSnapshotComparables(before, after, opts), nil
}

func diffSnapshotComparables(before, after snapshotDiffComparable, opts snapshotDiffOptions) *snapshotDiffResult {
	result := &snapshotDiffResult{
		Before: before.Meta,
		After:  after.Meta,
	}
	if snapshotDiffSectionEnabled(opts.Sections, "provenance") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("provenance", before.Provenance, after.Provenance, &result.Summary.Provenance)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "git") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("git", before.Git, after.Git, &result.Summary.Git)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "runtime") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("runtime", before.Runtime, after.Runtime, &result.Summary.Runtime)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "health") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("health", before.Health, after.Health, &result.Summary.Health)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "plan") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("plan", before.Plan, after.Plan, &result.Summary.Plan)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "triage") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("triage", before.Triage, after.Triage, &result.Summary.Triage)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "next") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("next", before.Next, after.Next, &result.Summary.Next)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "instances") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("instances", before.Instances, after.Instances, &result.Summary.Instances)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "jobs") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("jobs", before.Jobs, after.Jobs, &result.Summary.Jobs)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "job_quarantine") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("job_quarantine", before.JobQuarantine, after.JobQuarantine, &result.Summary.JobQuarantine)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "pipelines") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("pipelines", before.Pipelines, after.Pipelines, &result.Summary.Pipelines)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "inbox") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("inbox", before.Inbox, after.Inbox, &result.Summary.Inbox)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "outbox") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("outbox", before.Outbox, after.Outbox, &result.Summary.Outbox)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "outbox_quarantine") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("outbox_quarantine", before.OutboxQuarantine, after.OutboxQuarantine, &result.Summary.OutboxQuarantine)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "queue") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("queue", before.Queue, after.Queue, &result.Summary.Queue)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "queue_quarantine") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("queue_quarantine", before.QueueQuarantine, after.QueueQuarantine, &result.Summary.QueueQuarantine)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "schedules") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("schedules", before.Schedules, after.Schedules, &result.Summary.Schedules)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "intake") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("intake", before.Intake, after.Intake, &result.Summary.Intake)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "events") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("events", before.Events, after.Events, &result.Summary.Events)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "timeline") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("timeline", before.Timeline, after.Timeline, &result.Summary.Timeline)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "advance") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("advance", before.Advance, after.Advance, &result.Summary.Advance)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "section_errors") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("section_errors", before.SectionErrors, after.SectionErrors, &result.Summary.SectionErrors)...)
	}
	result.Summary.TotalChanges = len(result.Changes)
	return result
}

func collectCurrentSnapshotDiffComparable(cmd *cobra.Command, defaultRepo string, opts snapshotDiffCurrentOptions, target snapshotDiffMeta) (snapshotDiffComparable, error) {
	teamDir, err := resolveTeamDir(cmd, defaultRepo)
	if err != nil {
		return snapshotDiffComparable{}, err
	}
	repoRoot, err := filepath.Abs(effectiveRepoTarget(cmd, defaultRepo))
	if err != nil {
		return snapshotDiffComparable{}, err
	}
	now := time.Now().UTC()
	scope := strings.TrimSpace(target.Scope)
	switch target.Kind {
	case "job":
		if scope == "" {
			return snapshotDiffComparable{}, fmt.Errorf("current job snapshot requires a saved job scope")
		}
		j, err := job.Read(teamDir, scope)
		if err != nil {
			return snapshotDiffComparable{}, err
		}
		snapshot := collectJobSnapshot(teamDir, repoRoot, j, jobSnapshotOptions{
			EventLimit: opts.Events,
			EventSort:  "oldest",
			Redact:     opts.Redact,
			Now:        now,
		})
		snapshot.Provenance = newSnapshotProvenance(cmd.CommandPath(), "job", j.ID, snapshotProvenanceOptions{
			Events:   intValuePtr(opts.Events),
			Redacted: opts.Redact,
		})
		return snapshotDiffComparableFromValue("<current>", snapshot)
	case "pipeline":
		if scope == "" {
			return snapshotDiffComparable{}, fmt.Errorf("current pipeline snapshot requires a saved pipeline scope")
		}
		snapshot := collectPipelineSnapshot(teamDir, repoRoot, scope, pipelineSnapshotOptions{
			Redact:       opts.Redact,
			Now:          now,
			TimelineTail: opts.TimelineTail,
		})
		snapshot.Provenance = newSnapshotProvenance(cmd.CommandPath(), "pipeline", scope, snapshotProvenanceOptions{
			Timeline: intValuePtr(opts.TimelineTail),
			Redacted: opts.Redact,
		})
		return snapshotDiffComparableFromValue("<current>", snapshot)
	case "team":
		if scope == "" {
			return snapshotDiffComparable{}, fmt.Errorf("current team snapshot requires a saved team scope")
		}
		snapshot, err := collectTeamSnapshot(teamDir, repoRoot, scope, snapshotOptions{
			EventLimit:    opts.Events,
			ScheduleLimit: opts.ScheduleLimit,
			Redact:        opts.Redact,
			Now:           now,
		})
		if err != nil {
			return snapshotDiffComparable{}, err
		}
		setSnapshotProvenance(snapshot, cmd.CommandPath(), "team", scope, snapshotProvenanceOptions{
			Events:        intValuePtr(opts.Events),
			ScheduleLimit: intValuePtr(opts.ScheduleLimit),
			Redacted:      opts.Redact,
		})
		return snapshotDiffComparableFromSnapshot("<current>", snapshot)
	}
	snapshot := collectSnapshot(teamDir, repoRoot, snapshotOptions{
		EventLimit:    opts.Events,
		IntakeLimit:   opts.IntakeDeliveries,
		ScheduleLimit: opts.ScheduleLimit,
		Redact:        opts.Redact,
		Now:           now,
	})
	setSnapshotProvenance(snapshot, cmd.CommandPath(), "global", "", snapshotProvenanceOptions{
		Events:           intValuePtr(opts.Events),
		IntakeDeliveries: intValuePtr(opts.IntakeDeliveries),
		ScheduleLimit:    intValuePtr(opts.ScheduleLimit),
		Redacted:         opts.Redact,
	})
	return snapshotDiffComparableFromSnapshot("<current>", snapshot)
}

func snapshotDiffComparableFromSnapshot(path string, snapshot *snapshotResult) (snapshotDiffComparable, error) {
	return snapshotDiffComparableFromValue(path, snapshot)
}

func snapshotDiffComparableFromValue(path string, value any) (snapshotDiffComparable, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return snapshotDiffComparable{}, err
	}
	var input snapshotDiffInput
	if err := json.Unmarshal(body, &input); err != nil {
		return snapshotDiffComparable{}, err
	}
	return snapshotDiffComparableFromInput(path, input), nil
}

func parseSnapshotDiffSort(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "", snapshotDiffSortSection:
		return snapshotDiffSortSection, nil
	case snapshotDiffSortAction:
		return snapshotDiffSortAction, nil
	case snapshotDiffSortID:
		return snapshotDiffSortID, nil
	default:
		return "", fmt.Errorf("--sort must be section, action, or id")
	}
}

func parseSnapshotDiffActions(values []string) (map[string]bool, []string, error) {
	if len(values) == 0 {
		return nil, nil, nil
	}
	valid := map[string]bool{"added": true, "removed": true, "changed": true}
	out := map[string]bool{}
	labels := []string{}
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			name := strings.ToLower(strings.TrimSpace(part))
			if name == "" {
				continue
			}
			if name == "all" {
				return nil, nil, nil
			}
			if !valid[name] {
				return nil, nil, fmt.Errorf("--action must be added, removed, changed, or all")
			}
			if !out[name] {
				labels = append(labels, name)
			}
			out[name] = true
		}
	}
	if len(out) == 0 {
		return nil, nil, fmt.Errorf("--action requires at least one non-empty action")
	}
	sort.Strings(labels)
	return out, labels, nil
}

func filterSnapshotDiffResultByActions(result *snapshotDiffResult, actions map[string]bool, labels []string) {
	if result == nil || len(actions) == 0 {
		return
	}
	filtered := make([]snapshotDiffChange, 0, len(result.Changes))
	for _, change := range result.Changes {
		if actions[change.Action] {
			filtered = append(filtered, change)
		}
	}
	result.Changes = filtered
	result.Summary = snapshotDiffSummaryForChanges(filtered)
	result.Summary.ActionFilter = append([]string(nil), labels...)
}

func snapshotDiffSummaryForChanges(changes []snapshotDiffChange) snapshotDiffSummary {
	summary := snapshotDiffSummary{TotalChanges: len(changes)}
	for _, change := range changes {
		snapshotDiffIncrementSummary(&summary, change.Section, change.Action)
	}
	return summary
}

func snapshotDiffIncrementSummary(summary *snapshotDiffSummary, section, action string) {
	counter := snapshotDiffSummaryCounter(summary, section)
	if counter == nil {
		return
	}
	switch action {
	case "added":
		counter.Added++
	case "removed":
		counter.Removed++
	case "changed":
		counter.Changed++
	}
}

func snapshotDiffSummaryCounter(summary *snapshotDiffSummary, section string) *snapshotDiffCounters {
	if summary == nil {
		return nil
	}
	switch section {
	case "provenance":
		return &summary.Provenance
	case "git":
		return &summary.Git
	case "runtime":
		return &summary.Runtime
	case "health":
		return &summary.Health
	case "plan":
		return &summary.Plan
	case "triage":
		return &summary.Triage
	case "next":
		return &summary.Next
	case "instances":
		return &summary.Instances
	case "jobs":
		return &summary.Jobs
	case "job_quarantine":
		return &summary.JobQuarantine
	case "pipelines":
		return &summary.Pipelines
	case "inbox":
		return &summary.Inbox
	case "outbox":
		return &summary.Outbox
	case "outbox_quarantine":
		return &summary.OutboxQuarantine
	case "queue":
		return &summary.Queue
	case "queue_quarantine":
		return &summary.QueueQuarantine
	case "schedules":
		return &summary.Schedules
	case "intake":
		return &summary.Intake
	case "events":
		return &summary.Events
	case "timeline":
		return &summary.Timeline
	case "advance":
		return &summary.Advance
	case "section_errors":
		return &summary.SectionErrors
	default:
		return nil
	}
}

func sortSnapshotDiffResult(result *snapshotDiffResult, sortMode string) {
	if result == nil || sortMode == "" || sortMode == snapshotDiffSortSection {
		return
	}
	result.Summary.DetailSort = sortMode
	switch sortMode {
	case snapshotDiffSortAction:
		sort.SliceStable(result.Changes, func(i, j int) bool {
			left, right := result.Changes[i], result.Changes[j]
			leftRank, rightRank := snapshotDiffActionRank(left.Action), snapshotDiffActionRank(right.Action)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			if left.Action != right.Action {
				return left.Action < right.Action
			}
			return snapshotDiffChangeLess(left, right)
		})
	case snapshotDiffSortID:
		sort.SliceStable(result.Changes, func(i, j int) bool {
			left, right := result.Changes[i], result.Changes[j]
			if left.ID != right.ID {
				return left.ID < right.ID
			}
			if left.Section != right.Section {
				return left.Section < right.Section
			}
			leftRank, rightRank := snapshotDiffActionRank(left.Action), snapshotDiffActionRank(right.Action)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			if left.Action != right.Action {
				return left.Action < right.Action
			}
			if left.Before != right.Before {
				return left.Before < right.Before
			}
			return left.After < right.After
		})
	}
}

func snapshotDiffActionRank(action string) int {
	switch action {
	case "added":
		return 0
	case "removed":
		return 1
	case "changed":
		return 2
	default:
		return 3
	}
}

func snapshotDiffChangeLess(left, right snapshotDiffChange) bool {
	if left.Section != right.Section {
		return left.Section < right.Section
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	if left.Before != right.Before {
		return left.Before < right.Before
	}
	return left.After < right.After
}

func summarizeSnapshotDiffResult(result *snapshotDiffResult) {
	if result == nil {
		return
	}
	total := result.Summary.TotalChanges
	if total == 0 {
		total = len(result.Changes)
		result.Summary.TotalChanges = total
	}
	result.Summary.SummaryOnly = true
	result.Summary.ShownChanges = 0
	result.Summary.OmittedChanges = total
	result.Changes = nil
}

func limitSnapshotDiffResult(result *snapshotDiffResult, limit int) {
	if result == nil || limit <= 0 {
		return
	}
	total := result.Summary.TotalChanges
	if total == 0 {
		total = len(result.Changes)
		result.Summary.TotalChanges = total
	}
	result.Summary.DetailLimit = limit
	if len(result.Changes) <= limit {
		result.Summary.ShownChanges = len(result.Changes)
		return
	}
	result.Changes = append([]snapshotDiffChange(nil), result.Changes[:limit]...)
	result.Summary.ShownChanges = len(result.Changes)
	result.Summary.OmittedChanges = total - len(result.Changes)
}

func parseSnapshotDiffSections(values []string) (map[string]bool, error) {
	if len(values) == 0 {
		return nil, nil
	}
	valid := map[string]bool{
		"provenance":        true,
		"git":               true,
		"runtime":           true,
		"health":            true,
		"plan":              true,
		"triage":            true,
		"next":              true,
		"instances":         true,
		"jobs":              true,
		"job_quarantine":    true,
		"pipelines":         true,
		"inbox":             true,
		"outbox":            true,
		"outbox_quarantine": true,
		"queue":             true,
		"queue_quarantine":  true,
		"schedules":         true,
		"intake":            true,
		"events":            true,
		"timeline":          true,
		"advance":           true,
		"section_errors":    true,
	}
	out := map[string]bool{}
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			name := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(part, "-", "_")))
			if name == "" {
				continue
			}
			if name == "all" {
				return nil, nil
			}
			names := normalizeSnapshotDiffSectionAliases(name)
			for _, name := range names {
				if !valid[name] {
					return nil, fmt.Errorf("--section must be provenance, git, runtime, health, plan, triage, next, instances, jobs, job_quarantine, pipelines, inbox, outbox, outbox_quarantine, queue, queue_quarantine, schedules, intake, events, timeline, advance, section_errors, quarantine, or all")
				}
				out[name] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--section requires at least one non-empty section")
	}
	return out, nil
}

func normalizeSnapshotDiffSectionAliases(name string) []string {
	switch name {
	case "quarantine", "quarantines":
		return []string{"job_quarantine", "outbox_quarantine", "queue_quarantine"}
	case "job_quarantines", "jobs_quarantine", "jobs_quarantines":
		return []string{"job_quarantine"}
	case "outbox_quarantines":
		return []string{"outbox_quarantine"}
	case "queue_quarantines":
		return []string{"queue_quarantine"}
	default:
		return []string{name}
	}
}

func snapshotDiffSectionEnabled(sections map[string]bool, section string) bool {
	return len(sections) == 0 || sections[section]
}

func readSnapshotDiffComparable(path string) (snapshotDiffComparable, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return snapshotDiffComparable{}, err
	}
	var input snapshotDiffInput
	if err := json.Unmarshal(body, &input); err != nil {
		return snapshotDiffComparable{}, fmt.Errorf("%s: %w", path, err)
	}
	return snapshotDiffComparableFromInput(path, input), nil
}

func snapshotDiffComparableFromInput(path string, input snapshotDiffInput) snapshotDiffComparable {
	scope, kind := snapshotDiffScope(input)
	out := snapshotDiffComparable{
		Meta: snapshotDiffMeta{
			Path:       path,
			Kind:       kind,
			Scope:      scope,
			CapturedAt: input.CapturedAt,
			Repo:       input.Repo,
		},
		Provenance:       snapshotDiffProvenanceMap(input.Provenance),
		Git:              snapshotDiffGitMap(input.Git),
		Runtime:          snapshotDiffRuntimeMap(input.Runtime),
		Health:           snapshotDiffHealthMap(input.Health),
		Plan:             snapshotDiffPlanMap(input.Plan),
		Triage:           snapshotDiffTriageMap(input.JobTriage),
		Next:             snapshotDiffNextMap(input.Next, input.Actions),
		Instances:        map[string]string{},
		Jobs:             map[string]string{},
		JobQuarantine:    map[string]string{},
		Pipelines:        map[string]string{},
		Inbox:            map[string]string{},
		Outbox:           map[string]string{},
		OutboxQuarantine: map[string]string{},
		Queue:            map[string]string{},
		QueueQuarantine:  map[string]string{},
		Schedules:        map[string]string{},
		Intake:           map[string]string{},
		Events:           map[string]string{},
		Timeline:         map[string]string{},
		Advance:          map[string]string{},
		SectionErrors:    map[string]string{},
	}
	for _, inst := range input.Instances {
		id := strings.TrimSpace(inst.Instance)
		if id == "" {
			continue
		}
		stale := ""
		if inst.Stale {
			stale = "stale"
		}
		out.Instances[id] = compactSnapshotDiffValue(inst.Status, inst.Phase, inst.Agent, inst.Runtime, inst.Job, stale)
	}
	for _, j := range input.Jobs {
		id := strings.TrimSpace(j.ID)
		if id == "" {
			continue
		}
		out.Jobs[id] = compactSnapshotDiffValue(j.Status, j.Pipeline, j.Target, j.Instance)
	}
	addSnapshotDiffJobSnapshot(out.Jobs, input)
	for _, item := range input.JobQuarantine {
		id := strings.TrimSpace(item.Path)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			id = compactSnapshotDiffValue(item.Ticket, item.Target, string(item.Status))
		}
		if id == "" || id == "-" {
			continue
		}
		out.JobQuarantine[id] = compactSnapshotDiffValue(
			item.ID,
			item.Ticket,
			item.Target,
			string(item.Status),
			boolSnapshotDiffValue("restorable", item.Restorable),
			item.Problem,
		)
	}
	for _, inbox := range input.Inbox {
		id := strings.TrimSpace(inbox.Instance)
		if id == "" {
			continue
		}
		out.Inbox[id] = compactSnapshotDiffValue(
			inbox.Agent,
			inbox.Status,
			intSnapshotDiffValue("total", inbox.Total),
			intSnapshotDiffValue("unread", inbox.Unread),
			inbox.Cursor,
			inbox.LatestID,
			inbox.LatestFrom,
			inbox.LatestTS,
		)
	}
	for _, q := range input.Queue {
		id := strings.TrimSpace(q.ID)
		if id == "" {
			continue
		}
		out.Queue[id] = emptyDash(q.State)
	}
	for _, item := range input.Outbox {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = compactSnapshotDiffValue(item.Type, item.Source, outboxPayloadString(item.Payload, "target"), outboxItemJobFromPayload(item.Payload))
		}
		if id == "" || id == "-" {
			continue
		}
		out.Outbox[id] = compactSnapshotDiffValue(
			item.State,
			item.Type,
			item.Source,
			normalizeOutboxJob(outboxItemJobFromPayload(item.Payload)),
			outboxPayloadString(item.Payload, "target"),
			outboxPayloadString(item.Payload, "name"),
			item.LastError,
		)
	}
	for _, item := range input.OutboxQuarantine {
		id := strings.TrimSpace(item.Path)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			id = compactSnapshotDiffValue(item.State, item.Type, item.Source, item.Job, item.Target, item.Instance)
		}
		if id == "" || id == "-" {
			continue
		}
		out.OutboxQuarantine[id] = compactSnapshotDiffValue(
			item.State,
			item.ID,
			item.Type,
			item.Source,
			normalizeOutboxJob(item.Job),
			item.Target,
			item.Instance,
			item.Agent,
			boolSnapshotDiffValue("restorable", item.Restorable),
			item.Problem,
		)
	}
	for _, item := range input.QueueQuarantine {
		id := strings.TrimSpace(item.Path)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			id = compactSnapshotDiffValue(item.State, item.EventType, item.Instance, item.InstanceID, item.Job)
		}
		if id == "" || id == "-" {
			continue
		}
		out.QueueQuarantine[id] = compactSnapshotDiffValue(item.State, item.ID, item.EventType, item.Instance, item.InstanceID, item.Job, boolSnapshotDiffValue("restorable", item.Restorable), item.Problem)
	}
	for _, sched := range input.Schedules {
		addSnapshotDiffSchedule(out.Schedules, "declared", sched)
	}
	for _, sched := range input.ScheduleNext {
		addSnapshotDiffSchedule(out.Schedules, "next", sched)
	}
	for _, delivery := range input.Intake {
		id := strings.TrimSpace(delivery.ID)
		if id == "" {
			id = compactSnapshotDiffValue(delivery.Provider, delivery.RequestID, delivery.EventType, delivery.Time)
		}
		if id == "" || id == "-" {
			continue
		}
		out.Intake[id] = compactSnapshotDiffValue(delivery.Provider, delivery.Status, intSnapshotDiffValue("http", delivery.HTTPStatus), delivery.ReplayStatus, delivery.EventType, delivery.Ticket, delivery.PR, delivery.JobID, boolSnapshotDiffValue("dry_run", delivery.DryRun))
	}
	for _, duplicate := range input.IntakeDuplicates {
		addSnapshotDiffIntakeDuplicate(out.Intake, duplicate)
	}
	for _, ev := range input.Events {
		addSnapshotDiffLifecycleEvent(out.Events, "", ev)
	}
	for _, ev := range input.LifecycleEvents {
		addSnapshotDiffLifecycleEvent(out.Events, "lifecycle/", ev)
	}
	for _, ev := range input.JobEvents {
		addSnapshotDiffJobEvent(out.Events, ev)
	}
	for _, entry := range input.Timeline {
		addSnapshotDiffTimelineEntry(out.Timeline, entry)
	}
	for _, row := range input.PipelineStatus {
		addSnapshotDiffPipelineMetrics(out.Pipelines, row, input.Pipeline)
	}
	if row, ok := input.Status.pipelineRow(input.Pipeline); ok {
		addSnapshotDiffPipelineMetrics(out.Pipelines, row, input.Pipeline)
	}
	for _, advance := range input.PipelineAdvance {
		addSnapshotDiffAdvance(out.Advance, advance)
	}
	for _, advance := range input.AdvancePreview {
		addSnapshotDiffAdvance(out.Advance, advance)
	}
	for key, value := range input.SectionErrors {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out.SectionErrors[key] = strings.TrimSpace(value)
	}
	return out
}

func snapshotDiffHealthMap(health *healthResult) map[string]string {
	out := map[string]string{}
	if health == nil {
		return out
	}
	setSnapshotDiffBool(out, "healthy", health.Healthy)
	setSnapshotDiffBool(out, "daemon.running", health.Daemon.Running)
	setSnapshotDiffBool(out, "daemon.ready", health.Daemon.Ready)
	addSnapshotDiffPSSummary(out, "instances", health.Summary)
	addSnapshotDiffQueueSummary(out, "queue", health.Queue)
	addSnapshotDiffJobQuarantineSummary(out, "job_quarantine", health.JobQuarantine)
	addSnapshotDiffOutboxQuarantineSummary(out, "outbox_quarantine", health.OutboxQuarantine)
	addSnapshotDiffIntakeSummary(out, "intake", health.Intake)
	if health.Jobs != nil {
		addSnapshotDiffJobSummary(out, "jobs", health.Jobs.Summary)
		setSnapshotDiffInt(out, "jobs.attention", len(health.Jobs.Attention))
		setSnapshotDiffInt(out, "jobs.ready_steps", len(health.Jobs.ReadySteps))
		setSnapshotDiffInt(out, "jobs.status_previews", len(health.Jobs.StatusPreviews))
	}
	setSnapshotDiffInt(out, "declared.persistent", health.Declared.Persistent)
	setSnapshotDiffInt(out, "declared.running", health.Declared.Running)
	setSnapshotDiffInt(out, "declared.missing", health.Declared.Missing)
	issueCodes := map[string]int{}
	issueSeverities := map[string]int{}
	for _, issue := range health.Issues {
		if code := strings.TrimSpace(issue.Code); code != "" {
			issueCodes[code]++
		}
		if severity := strings.TrimSpace(issue.Severity); severity != "" {
			issueSeverities[severity]++
		}
	}
	addSnapshotDiffCountMap(out, "issues.code", issueCodes)
	addSnapshotDiffCountMap(out, "issues.severity", issueSeverities)
	return out
}

func snapshotDiffPlanMap(plan *planResult) map[string]string {
	out := map[string]string{}
	if plan == nil {
		return out
	}
	setSnapshotDiffBool(out, "daemon.running", plan.Daemon.Running)
	setSnapshotDiffInt(out, "summary.total", plan.Summary.Total)
	setSnapshotDiffInt(out, "summary.start", plan.Summary.Start)
	setSnapshotDiffInt(out, "summary.resume", plan.Summary.Resume)
	setSnapshotDiffInt(out, "summary.keep", plan.Summary.Keep)
	setSnapshotDiffInt(out, "summary.unsupported", plan.Summary.Unsupported)
	setSnapshotDiffInt(out, "summary.on_demand", plan.Summary.OnDemand)
	setSnapshotDiffInt(out, "summary.stop", plan.Summary.Stop)
	setSnapshotDiffInt(out, "summary.extra", plan.Summary.Extra)
	for _, row := range plan.Instances {
		id := strings.TrimSpace(row.Instance)
		if id == "" {
			continue
		}
		out["instance."+id] = compactSnapshotDiffValue(row.Agent, row.Kind, row.Status, row.Phase, row.Action, row.Detail)
	}
	return out
}

func snapshotDiffTriageMap(triage *jobTriageSnapshot) map[string]string {
	out := map[string]string{}
	if triage == nil {
		return out
	}
	addSnapshotDiffJobSummary(out, "summary", triage.Summary)
	addSnapshotDiffQueueSummary(out, "queue", triage.Queue)
	setSnapshotDiffInt(out, "attention.total", len(triage.Attention))
	setSnapshotDiffInt(out, "ready_steps.total", len(triage.ReadySteps))
	setSnapshotDiffInt(out, "status_previews.total", len(triage.StatusPreviews))
	severities := map[string]int{}
	reasons := map[string]int{}
	for _, item := range triage.Attention {
		if severity := strings.TrimSpace(item.Severity); severity != "" {
			severities[severity]++
		}
		for _, reason := range item.Reasons {
			reason = strings.TrimSpace(reason)
			if reason != "" {
				reasons[reason]++
			}
		}
		addSnapshotDiffTriageAttention(out, item)
	}
	addSnapshotDiffCountMap(out, "attention.severity", severities)
	addSnapshotDiffCountMap(out, "attention.reason", reasons)
	for _, row := range triage.ReadySteps {
		addSnapshotDiffTriageReadyStep(out, row)
	}
	for _, preview := range triage.StatusPreviews {
		addSnapshotDiffTriageStatusPreview(out, preview)
	}
	return out
}

func addSnapshotDiffTriageAttention(out map[string]string, item jobTriageItem) {
	id := strings.TrimSpace(item.JobID)
	if id == "" {
		return
	}
	if step := strings.TrimSpace(item.StepID); step != "" {
		id += "/step/" + step
	}
	out["attention/"+id] = compactSnapshotDiffValue(
		string(item.Status),
		item.Severity,
		snapshotDiffSortedListValue(item.Reasons),
		item.Target,
		item.Instance,
		item.Pipeline,
		item.StepState,
		item.StepTarget,
		intSnapshotDiffValue("queue_pending", item.QueuePending),
		intSnapshotDiffValue("queue_dead", item.QueueDead),
		intSnapshotDiffValue("queue_delayed", item.QueueDelayed),
		intSnapshotDiffValue("queue_quarantined", item.QueueQuarantined),
		intSnapshotDiffValue("queue_restorable", item.QueueQuarantineRestorable),
		intSnapshotDiffValue("queue_unrestorable", item.QueueQuarantineUnrestorable),
		snapshotDiffSortedListValue(item.QueueIDs),
		snapshotDiffSortedListValue(item.QueueQuarantinePaths),
		snapshotDiffSortedListValue(item.QueueQuarantineRestorablePaths),
		item.Message,
		snapshotDiffSortedListValue(item.Actions),
	)
}

func addSnapshotDiffTriageReadyStep(out map[string]string, row jobReadyRow) {
	id := strings.TrimSpace(row.JobID)
	if id == "" {
		return
	}
	if step := strings.TrimSpace(row.StepID); step != "" {
		id += "/step/" + step
	}
	out["ready/"+id] = compactSnapshotDiffValue(
		string(row.JobStatus),
		row.State,
		row.Target,
		row.Instance,
		row.Pipeline,
		string(row.StepStatus),
		row.Gate,
		row.Workspace,
		row.Runtime,
		row.RuntimeBin,
		boolSnapshotDiffValue("optional", row.Optional),
		intSnapshotDiffValue("attempts", row.Attempts),
		intSnapshotDiffValue("max_attempts", row.MaxAttempts),
		intSnapshotDiffValue("parallel_ready", row.ParallelReadySteps),
		snapshotDiffSortedListValue(row.WaitingFor),
		snapshotDiffSortedListValue(row.Actions),
		row.Message,
	)
}

func addSnapshotDiffTriageStatusPreview(out map[string]string, preview jobStatusReconcileResult) {
	id := strings.TrimSpace(preview.JobID)
	if id == "" {
		return
	}
	if instance := strings.TrimSpace(preview.Instance); instance != "" {
		id += "/" + instance
	}
	transition := ""
	if preview.Before != "" || preview.After != "" {
		transition = string(preview.Before) + "->" + string(preview.After)
	}
	out["status_preview/"+id] = compactSnapshotDiffValue(
		preview.Phase,
		preview.MatchedBy,
		transition,
		preview.Branch,
		preview.PR,
		boolSnapshotDiffValue("changed", preview.Changed),
		preview.Message,
	)
}

func snapshotDiffNextMap(next *nextActionResult, actions []string) map[string]string {
	out := map[string]string{}
	if next == nil && len(actions) == 0 {
		return out
	}
	detailsByCommand := map[string]operatorActionHint{}
	if next != nil {
		setSnapshotDiffBool(out, "ok", next.OK)
		if state := strings.TrimSpace(next.State); state != "" {
			out["state"] = state
		}
		if next.Team != nil {
			if team := strings.TrimSpace(next.Team.Name); team != "" {
				out["team"] = team
			}
		}
		setSnapshotDiffInt(out, "total_actions", next.TotalActions)
		setSnapshotDiffInt(out, "hidden_actions", next.HiddenActions)
		actions = append(actions, next.Actions...)
		for _, detail := range next.ActionDetails {
			command := strings.TrimSpace(detail.Command)
			if command == "" {
				continue
			}
			if _, exists := detailsByCommand[command]; !exists {
				detailsByCommand[command] = detail
			}
		}
	}
	seen := map[string]bool{}
	for _, action := range actions {
		command := strings.TrimSpace(action)
		if command == "" || seen[command] {
			continue
		}
		seen[command] = true
		detail := detailsByCommand[command]
		out["action/"+command] = compactSnapshotDiffValue(detail.Source, detail.Reason, detail.Team)
	}
	for command, detail := range detailsByCommand {
		if seen[command] {
			continue
		}
		out["action/"+command] = compactSnapshotDiffValue(detail.Source, detail.Reason, detail.Team)
	}
	return out
}

func addSnapshotDiffJobSnapshot(out map[string]string, input snapshotDiffInput) {
	if input.Job == nil {
		return
	}
	j := input.Job
	id := strings.TrimSpace(j.ID)
	if id == "" {
		id = strings.TrimSpace(j.Ticket)
	}
	if id == "" {
		return
	}
	out[id] = compactSnapshotDiffValue(
		j.Status,
		j.Pipeline,
		j.Target,
		j.Instance,
		j.Ticket,
		j.Branch,
		j.Worktree,
		j.PR,
		boolSnapshotDiffValue("held", j.Held),
		j.HoldReason,
		j.LastEvent,
		j.LastStatus,
	)
	for _, step := range j.Steps {
		stepID := strings.TrimSpace(step.ID)
		if stepID == "" {
			continue
		}
		out[id+"/step/"+stepID] = compactSnapshotDiffValue(
			step.Status,
			step.Target,
			step.Instance,
			step.Workspace,
			step.Runtime,
			step.RuntimeBin,
			step.Gate,
			boolSnapshotDiffValue("optional", step.Optional),
			intSnapshotDiffValue("attempts", step.Attempts),
			intSnapshotDiffValue("max_attempts", step.MaxAttempts),
			boolSnapshotDiffValue("skipped", step.Skipped),
			step.SkipReason,
		)
	}
	if instance := strings.TrimSpace(input.Instance); instance != "" && instance != strings.TrimSpace(j.Instance) {
		out[id+"/snapshot.instance"] = instance
	}
	if input.State != nil {
		out[id+"/state"] = compactSnapshotDiffValue(
			boolSnapshotDiffValue("exists", input.State.Exists),
			input.State.Path,
		)
	}
	if input.Status != nil {
		status := input.Status
		work := ""
		if status.Work != nil {
			work = compactSnapshotDiffValue(status.Work.Job, status.Work.Ticket, status.Work.Branch, status.Work.PR)
		}
		blocking := ""
		if status.Blocking != nil {
			blocking = compactSnapshotDiffValue(status.Blocking.Reason, status.Blocking.AskTo)
		}
		out[id+"/status"] = compactSnapshotDiffValue(
			status.Phase,
			status.Description,
			status.Since,
			status.LastAction,
			boolSnapshotDiffValue("stale", status.Stale),
			work,
			blocking,
		)
	}
	addSnapshotDiffJobSnapshotFile(out, id+"/log", input.Log)
	addSnapshotDiffJobSnapshotFile(out, id+"/last_message", input.LastMessage)
}

func addSnapshotDiffJobSnapshotFile(out map[string]string, key string, file *jobSnapshotFile) {
	if file == nil {
		return
	}
	size := ""
	if file.Size != 0 {
		size = fmt.Sprintf("size=%d", file.Size)
	}
	out[key] = compactSnapshotDiffValue(
		boolSnapshotDiffValue("exists", file.Exists),
		size,
		file.ModTime,
		file.Path,
	)
}

func addSnapshotDiffPSSummary(out map[string]string, prefix string, summary psSummaryJSON) {
	setSnapshotDiffInt(out, prefix+".total", summary.Total)
	setSnapshotDiffInt(out, prefix+".running", summary.Running)
	setSnapshotDiffInt(out, prefix+".stopped", summary.Stopped)
	setSnapshotDiffInt(out, prefix+".exited", summary.Exited)
	setSnapshotDiffInt(out, prefix+".crashed", summary.Crashed)
	setSnapshotDiffInt(out, prefix+".unknown", summary.Unknown)
	setSnapshotDiffInt(out, prefix+".stale", summary.Stale)
	setSnapshotDiffInt(out, prefix+".runtime_stale", summary.RuntimeStale)
	setSnapshotDiffInt(out, prefix+".unhealthy", summary.Unhealthy)
	setSnapshotDiffInt(out, prefix+".has_status", summary.HasStatus)
	addSnapshotDiffCountMap(out, prefix+".phase", summary.Phases)
	addSnapshotDiffCountMap(out, prefix+".runtime", summary.Runtimes)
}

func addSnapshotDiffQueueSummary(out map[string]string, prefix string, summary queueSummary) {
	setSnapshotDiffInt(out, prefix+".total", summary.Total)
	setSnapshotDiffInt(out, prefix+".pending", summary.Pending)
	setSnapshotDiffInt(out, prefix+".dead", summary.Dead)
	setSnapshotDiffInt(out, prefix+".delayed", summary.Delayed)
	setSnapshotDiffInt(out, prefix+".attempts", summary.Attempts)
	setSnapshotDiffInt(out, prefix+".quarantined", summary.Quarantined)
	setSnapshotDiffInt(out, prefix+".quarantine_restorable", summary.QuarantineRestorable)
	setSnapshotDiffInt(out, prefix+".quarantine_unrestorable", summary.QuarantineUnrestorable)
	addSnapshotDiffCountMap(out, prefix+".instance", summary.Instances)
	addSnapshotDiffCountMap(out, prefix+".event", summary.Events)
	addSnapshotDiffCountMap(out, prefix+".runtime", summary.Runtimes)
}

func addSnapshotDiffJobQuarantineSummary(out map[string]string, prefix string, summary jobQuarantineSummary) {
	setSnapshotDiffInt(out, prefix+".quarantined", summary.Quarantined)
	setSnapshotDiffInt(out, prefix+".restorable", summary.Restorable)
	setSnapshotDiffInt(out, prefix+".unrestorable", summary.Unrestorable)
	addSnapshotDiffCountMap(out, prefix+".job", summary.Jobs)
}

func addSnapshotDiffOutboxQuarantineSummary(out map[string]string, prefix string, summary outboxQuarantineSummary) {
	setSnapshotDiffInt(out, prefix+".quarantined", summary.Quarantined)
	setSnapshotDiffInt(out, prefix+".restorable", summary.Restorable)
	setSnapshotDiffInt(out, prefix+".unrestorable", summary.Unrestorable)
	addSnapshotDiffCountMap(out, prefix+".state", summary.States)
	addSnapshotDiffCountMap(out, prefix+".type", summary.Types)
	addSnapshotDiffCountMap(out, prefix+".source", summary.Sources)
	addSnapshotDiffCountMap(out, prefix+".job", summary.Jobs)
}

func addSnapshotDiffIntakeSummary(out map[string]string, prefix string, summary overviewIntakeSummary) {
	setSnapshotDiffInt(out, prefix+".deliveries", summary.Deliveries)
	setSnapshotDiffInt(out, prefix+".errors", summary.Errors)
	setSnapshotDiffInt(out, prefix+".recovered", summary.Recovered)
	setSnapshotDiffInt(out, prefix+".replayable", summary.Replayable)
	setSnapshotDiffInt(out, prefix+".duplicate_request_ids", summary.DuplicateRequestIDs)
	if value := strings.TrimSpace(summary.LatestErrorID); value != "" {
		out[prefix+".latest_error_id"] = value
	}
}

func addSnapshotDiffJobSummary(out map[string]string, prefix string, summary jobSummary) {
	setSnapshotDiffInt(out, prefix+".total", summary.Total)
	setSnapshotDiffInt(out, prefix+".queued", summary.Queued)
	setSnapshotDiffInt(out, prefix+".running", summary.Running)
	setSnapshotDiffInt(out, prefix+".blocked", summary.Blocked)
	setSnapshotDiffInt(out, prefix+".done", summary.Done)
	setSnapshotDiffInt(out, prefix+".failed", summary.Failed)
	setSnapshotDiffInt(out, prefix+".held", summary.Held)
	setSnapshotDiffInt(out, prefix+".expired_held", summary.ExpiredHeld)
	setSnapshotDiffInt(out, prefix+".with_instance", summary.WithInstance)
	setSnapshotDiffInt(out, prefix+".with_branch", summary.WithBranch)
	setSnapshotDiffInt(out, prefix+".with_worktree", summary.WithWorktree)
	setSnapshotDiffInt(out, prefix+".with_pr", summary.WithPR)
	addSnapshotDiffCountMap(out, prefix+".target", summary.Targets)
	addSnapshotDiffCountMap(out, prefix+".pipeline", summary.Pipelines)
	addSnapshotDiffCountMap(out, prefix+".runtime", summary.Runtimes)
}

func addSnapshotDiffCountMap(out map[string]string, prefix string, counts map[string]int) {
	for key, value := range counts {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		setSnapshotDiffInt(out, prefix+"."+key, value)
	}
}

func setSnapshotDiffBool(out map[string]string, key string, value bool) {
	out[key] = fmt.Sprintf("%t", value)
}

func setSnapshotDiffInt(out map[string]string, key string, value int) {
	out[key] = fmt.Sprintf("%d", value)
}

func snapshotDiffRuntimeMap(runtime *runtimeInfo) map[string]string {
	out := map[string]string{}
	if runtime == nil {
		return out
	}
	if value := strings.TrimSpace(runtime.Runtime); value != "" {
		out["runtime"] = value
	}
	if value := strings.TrimSpace(runtime.Lifecycle); value != "" {
		out["lifecycle"] = value
	}
	if value := strings.TrimSpace(runtime.Agent); value != "" {
		out["agent"] = value
	}
	if value := strings.TrimSpace(runtime.Binary); value != "" {
		out["binary"] = value
	}
	if value := strings.TrimSpace(runtime.RuntimeBinary); value != "" {
		out["runtime_binary"] = value
	}
	if value := strings.TrimSpace(runtime.Path); value != "" {
		out["path"] = value
	}
	if value := strings.TrimSpace(runtime.EnvRuntime); value != "" {
		out["env_runtime"] = value
	}
	if value := strings.TrimSpace(runtime.EnvBinary); value != "" {
		out["env_binary"] = value
	}
	if value := strings.TrimSpace(runtime.ConfigPath); value != "" {
		out["config_path"] = value
	}
	if value := strings.TrimSpace(runtime.Job); value != "" {
		out["job"] = value
	}
	if value := strings.TrimSpace(runtime.Ticket); value != "" {
		out["ticket"] = value
	}
	if value := strings.TrimSpace(runtime.Branch); value != "" {
		out["branch"] = value
	}
	if value := strings.TrimSpace(runtime.PR); value != "" {
		out["pr"] = value
	}
	if runtime.PID != 0 {
		out["pid"] = fmt.Sprintf("%d", runtime.PID)
	}
	if value := strings.TrimSpace(runtime.Workspace); value != "" {
		out["workspace"] = value
	}
	if value := strings.TrimSpace(runtime.SessionID); value != "" {
		out["session_id"] = value
	}
	if value := strings.TrimSpace(runtime.StartedAt); value != "" {
		out["started_at"] = value
	}
	if value := strings.TrimSpace(runtime.StoppedAt); value != "" {
		out["stopped_at"] = value
	}
	if value := strings.TrimSpace(runtime.ExitedAt); value != "" {
		out["exited_at"] = value
	}
	if runtime.ExitCode != nil {
		out["exit_code"] = fmt.Sprintf("%d", *runtime.ExitCode)
	}
	if value := strings.TrimSpace(runtime.LogPath); value != "" {
		out["log_path"] = value
	}
	if runtime.Adopted {
		out["adopted"] = "true"
	}
	if snapshotDiffRuntimeHasProfile(runtime) {
		out["selected"] = fmt.Sprintf("%t", runtime.Selected)
		out["available"] = fmt.Sprintf("%t", runtime.Available)
		out["direct_run"] = fmt.Sprintf("%t", runtime.DirectRun)
		out["daemon_dispatch"] = fmt.Sprintf("%t", runtime.DaemonDispatch)
		out["direct_resume"] = fmt.Sprintf("%t", runtime.DirectResume)
		out["managed_resume"] = fmt.Sprintf("%t", runtime.ManagedResume)
		out["resume"] = fmt.Sprintf("%t", runtime.Resume)
		out["subagents"] = fmt.Sprintf("%t", runtime.Subagents)
	}
	return out
}

func snapshotDiffRuntimeHasProfile(runtime *runtimeInfo) bool {
	if runtime == nil {
		return false
	}
	return runtime.Selected ||
		runtime.Available ||
		runtime.DirectRun ||
		runtime.DaemonDispatch ||
		runtime.DirectResume ||
		runtime.ManagedResume ||
		runtime.Resume ||
		runtime.Subagents ||
		strings.TrimSpace(runtime.Binary) != "" ||
		strings.TrimSpace(runtime.Path) != "" ||
		strings.TrimSpace(runtime.EnvRuntime) != "" ||
		strings.TrimSpace(runtime.EnvBinary) != "" ||
		strings.TrimSpace(runtime.ConfigPath) != ""
}

func snapshotDiffGitMap(git *snapshotGitInfo) map[string]string {
	out := map[string]string{}
	if git == nil {
		return out
	}
	if value := strings.TrimSpace(git.Branch); value != "" {
		out["branch"] = value
	}
	if value := strings.TrimSpace(git.Commit); value != "" {
		out["commit"] = value
	}
	if value := strings.TrimSpace(git.Upstream); value != "" {
		out["upstream"] = value
	}
	out["dirty"] = fmt.Sprintf("%t", git.Dirty)
	out["changes"] = fmt.Sprintf("%d", git.Changes)
	out["ahead"] = fmt.Sprintf("%d", git.Ahead)
	out["behind"] = fmt.Sprintf("%d", git.Behind)
	return out
}

func snapshotDiffProvenanceMap(provenance *snapshotProvenance) map[string]string {
	out := map[string]string{}
	if provenance == nil {
		return out
	}
	if value := strings.TrimSpace(provenance.Command); value != "" {
		out["command"] = value
	}
	if value := strings.TrimSpace(provenance.Scope); value != "" {
		out["scope"] = value
	}
	if value := strings.TrimSpace(provenance.Subject); value != "" {
		out["subject"] = value
	}
	out["redacted"] = fmt.Sprintf("%t", provenance.Options.Redacted)
	if provenance.Options.Events != nil {
		out["events"] = fmt.Sprintf("%d", *provenance.Options.Events)
	}
	if provenance.Options.IntakeDeliveries != nil {
		out["intake_deliveries"] = fmt.Sprintf("%d", *provenance.Options.IntakeDeliveries)
	}
	if provenance.Options.ScheduleLimit != nil {
		out["schedule_limit"] = fmt.Sprintf("%d", *provenance.Options.ScheduleLimit)
	}
	if provenance.Options.Timeline != nil {
		out["timeline"] = fmt.Sprintf("%d", *provenance.Options.Timeline)
	}
	if provenance.Options.Tail != nil {
		out["tail"] = fmt.Sprintf("%d", *provenance.Options.Tail)
	}
	return out
}

func snapshotDiffScope(input snapshotDiffInput) (string, string) {
	if input.Provenance != nil && input.Provenance.Scope == "job" {
		if subject := strings.TrimSpace(input.Provenance.Subject); subject != "" {
			return subject, "job"
		}
	}
	if input.Job != nil {
		if id := strings.TrimSpace(input.Job.ID); id != "" {
			return id, "job"
		}
	}
	if strings.TrimSpace(input.Pipeline) != "" {
		return strings.TrimSpace(input.Pipeline), "pipeline"
	}
	if input.Team != nil && strings.TrimSpace(input.Team.Name) != "" {
		return strings.TrimSpace(input.Team.Name), "team"
	}
	if strings.TrimSpace(input.Repo) != "" {
		return strings.TrimSpace(input.Repo), "repo"
	}
	return "", "snapshot"
}

func addSnapshotDiffPipelineMetrics(out map[string]string, row pipelineStatusRow, fallbackPipeline string) {
	pipeline := strings.TrimSpace(row.Pipeline)
	if pipeline == "" {
		pipeline = strings.TrimSpace(fallbackPipeline)
	}
	if pipeline == "" {
		return
	}
	metrics := map[string]int{
		"jobs":          row.Jobs,
		"ready_steps":   row.ReadySteps,
		"manual_gates":  row.ManualGates,
		"failed_steps":  row.FailedSteps,
		"blocked_steps": row.BlockedSteps,
		"queued_steps":  row.QueuedSteps,
		"running_steps": row.RunningSteps,
		"done_steps":    row.DoneSteps,
	}
	keys := make([]string, 0, len(metrics))
	for key := range metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out[pipeline+"."+key] = fmt.Sprintf("%d", metrics[key])
	}
}

func addSnapshotDiffSchedule(out map[string]string, kind string, sched snapshotDiffSchedule) {
	name := strings.TrimSpace(sched.Name)
	if name == "" {
		return
	}
	out[kind+"/"+name] = compactSnapshotDiffValue(
		sched.Event,
		sched.Every,
		boolSnapshotDiffValue("run_on_start", sched.RunOnStart),
		sched.LastFiredAt,
		sched.NextRun,
		boolSnapshotDiffValue("due", sched.Due),
		sched.DueReason,
	)
}

func addSnapshotDiffAdvance(out map[string]string, advance snapshotDiffAdvance) {
	jobID := strings.TrimSpace(advance.JobID)
	if jobID == "" {
		return
	}
	id := jobID
	if step := strings.TrimSpace(advance.StepID); step != "" {
		id += ":" + step
	}
	out[id] = compactSnapshotDiffValue(advance.Action, advance.Pipeline, advance.Target, advance.StepStatus)
}

func addSnapshotDiffLifecycleEvent(out map[string]string, prefix string, ev snapshotDiffEvent) {
	id := strings.TrimSpace(ev.ID)
	if id == "" {
		id = compactSnapshotDiffValue(ev.TS, ev.Action, ev.Instance, ev.Job)
	}
	if id == "" || id == "-" {
		return
	}
	exitCode := ""
	if ev.ExitCode != nil {
		exitCode = fmt.Sprintf("exit_code=%d", *ev.ExitCode)
	}
	out[prefix+id] = compactSnapshotDiffValue(ev.Action, ev.Instance, ev.Agent, ev.Job, ev.Ticket, ev.Status, ev.Branch, ev.PR, exitCode, ev.Message)
}

func addSnapshotDiffJobEvent(out map[string]string, ev snapshotDiffJobEvent) {
	id := compactSnapshotDiffValue(ev.TS, ev.Type, ev.Actor, ev.Instance)
	if id == "" || id == "-" {
		return
	}
	out["job/"+id] = compactSnapshotDiffValue(
		ev.JobID,
		ev.Type,
		ev.Status,
		ev.Instance,
		ev.Actor,
		ev.Message,
		snapshotDiffStringMapValue(ev.Data),
	)
}

func addSnapshotDiffTimelineEntry(out map[string]string, entry snapshotDiffTimelineEntry) {
	id := compactSnapshotDiffValue(entry.Source, entry.JobID, entry.TS, entry.Kind, entry.Instance)
	if id == "" || id == "-" {
		return
	}
	exitCode := ""
	if entry.ExitCode != nil {
		exitCode = fmt.Sprintf("exit_code=%d", *entry.ExitCode)
	}
	out[id] = compactSnapshotDiffValue(
		entry.Source,
		entry.JobID,
		entry.Kind,
		entry.Status,
		entry.Instance,
		entry.Actor,
		entry.Agent,
		entry.Ticket,
		entry.Branch,
		entry.PR,
		exitCode,
		entry.Message,
		snapshotDiffStringMapValue(entry.Data),
	)
}

func addSnapshotDiffIntakeDuplicate(out map[string]string, duplicate snapshotDiffIntakeDuplicate) {
	provider := strings.ToLower(strings.TrimSpace(duplicate.Provider))
	requestID := strings.TrimSpace(duplicate.RequestID)
	if provider == "" || requestID == "" {
		return
	}
	ids := append([]string(nil), duplicate.IDs...)
	sort.Strings(ids)
	out["duplicate/"+provider+"/"+requestID] = compactSnapshotDiffValue(
		fmt.Sprintf("count=%d", duplicate.Count),
		strings.Join(ids, ","),
	)
}

func boolSnapshotDiffValue(name string, value bool) string {
	return fmt.Sprintf("%s=%t", name, value)
}

func intSnapshotDiffValue(name string, value int) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%s=%d", name, value)
}

func snapshotDiffStringMapValue(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(values[key])
		if value == "" {
			continue
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func snapshotDiffSortedListValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			clean = append(clean, value)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	sort.Strings(clean)
	return strings.Join(clean, ",")
}

func compactSnapshotDiffValue(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return "-"
	}
	return strings.Join(clean, "|")
}

func diffSnapshotStringMaps(section string, before, after map[string]string, counters *snapshotDiffCounters) []snapshotDiffChange {
	keys := make([]string, 0, len(before)+len(after))
	seen := map[string]bool{}
	for key := range before {
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	for key := range after {
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Strings(keys)
	changes := []snapshotDiffChange{}
	for _, key := range keys {
		beforeValue, beforeOK := before[key]
		afterValue, afterOK := after[key]
		switch {
		case !beforeOK && afterOK:
			counters.Added++
			changes = append(changes, snapshotDiffChange{Section: section, ID: key, Action: "added", After: afterValue})
		case beforeOK && !afterOK:
			counters.Removed++
			changes = append(changes, snapshotDiffChange{Section: section, ID: key, Action: "removed", Before: beforeValue})
		case beforeOK && afterOK && beforeValue != afterValue:
			counters.Changed++
			changes = append(changes, snapshotDiffChange{Section: section, ID: key, Action: "changed", Before: beforeValue, After: afterValue})
		}
	}
	return changes
}

func renderSnapshotDiff(w io.Writer, result *snapshotDiffResult) {
	if result == nil {
		fmt.Fprintln(w, "snapshot diff: unavailable")
		return
	}
	fmt.Fprintf(w, "snapshot diff: %s -> %s\n", result.Before.Path, result.After.Path)
	fmt.Fprintf(w, "before: kind=%s scope=%s captured_at=%s\n", result.Before.Kind, emptyDash(result.Before.Scope), emptyDash(result.Before.CapturedAt))
	fmt.Fprintf(w, "after: kind=%s scope=%s captured_at=%s\n", result.After.Kind, emptyDash(result.After.Scope), emptyDash(result.After.CapturedAt))
	fmt.Fprintf(w, "changes: total=%d\n", result.Summary.TotalChanges)
	renderSnapshotDiffCounterLine(w, "provenance", result.Summary.Provenance)
	renderSnapshotDiffCounterLine(w, "git", result.Summary.Git)
	renderSnapshotDiffCounterLine(w, "runtime", result.Summary.Runtime)
	renderSnapshotDiffCounterLine(w, "health", result.Summary.Health)
	renderSnapshotDiffCounterLine(w, "plan", result.Summary.Plan)
	renderSnapshotDiffCounterLine(w, "triage", result.Summary.Triage)
	renderSnapshotDiffCounterLine(w, "next", result.Summary.Next)
	renderSnapshotDiffCounterLine(w, "instances", result.Summary.Instances)
	renderSnapshotDiffCounterLine(w, "jobs", result.Summary.Jobs)
	renderSnapshotDiffCounterLine(w, "job_quarantine", result.Summary.JobQuarantine)
	renderSnapshotDiffCounterLine(w, "pipelines", result.Summary.Pipelines)
	renderSnapshotDiffCounterLine(w, "inbox", result.Summary.Inbox)
	renderSnapshotDiffCounterLine(w, "outbox", result.Summary.Outbox)
	renderSnapshotDiffCounterLine(w, "outbox_quarantine", result.Summary.OutboxQuarantine)
	renderSnapshotDiffCounterLine(w, "queue", result.Summary.Queue)
	renderSnapshotDiffCounterLine(w, "queue_quarantine", result.Summary.QueueQuarantine)
	renderSnapshotDiffCounterLine(w, "schedules", result.Summary.Schedules)
	renderSnapshotDiffCounterLine(w, "intake", result.Summary.Intake)
	renderSnapshotDiffCounterLine(w, "events", result.Summary.Events)
	renderSnapshotDiffCounterLine(w, "timeline", result.Summary.Timeline)
	renderSnapshotDiffCounterLine(w, "advance", result.Summary.Advance)
	renderSnapshotDiffCounterLine(w, "section_errors", result.Summary.SectionErrors)
	if len(result.Summary.ActionFilter) > 0 {
		fmt.Fprintf(w, "filter: actions=%s\n", strings.Join(result.Summary.ActionFilter, ","))
	}
	if result.Summary.SummaryOnly {
		fmt.Fprintf(w, "details: summary only (omitted=%d)\n", result.Summary.OmittedChanges)
		return
	}
	if len(result.Changes) == 0 {
		if result.Summary.OmittedChanges > 0 {
			fmt.Fprintf(w, "details: none shown (omitted=%d limit=%d)\n", result.Summary.OmittedChanges, result.Summary.DetailLimit)
		} else if result.Summary.DetailSort != "" {
			fmt.Fprintf(w, "details: none (sort=%s)\n", result.Summary.DetailSort)
		} else {
			fmt.Fprintln(w, "details: none")
		}
		return
	}
	if result.Summary.OmittedChanges > 0 {
		if result.Summary.DetailSort != "" {
			fmt.Fprintf(w, "details: sort=%s showing=%d omitted=%d limit=%d\n", result.Summary.DetailSort, len(result.Changes), result.Summary.OmittedChanges, result.Summary.DetailLimit)
		} else {
			fmt.Fprintf(w, "details: showing=%d omitted=%d limit=%d\n", len(result.Changes), result.Summary.OmittedChanges, result.Summary.DetailLimit)
		}
	} else if result.Summary.DetailSort != "" {
		fmt.Fprintf(w, "details: sort=%s\n", result.Summary.DetailSort)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SECTION\tID\tACTION\tBEFORE\tAFTER")
	for _, change := range result.Changes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			change.Section,
			change.ID,
			change.Action,
			emptyDash(change.Before),
			emptyDash(change.After))
	}
	_ = tw.Flush()
}

func parseSnapshotDiffFormat(format string) (*template.Template, error) {
	if format == "" {
		return nil, nil
	}
	tmpl, err := template.New("snapshot-diff-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func writeSnapshotDiffFile(path string, result *snapshotDiffResult) (string, error) {
	path = filepath.Clean(path)
	body, err := snapshotDiffJSON(result)
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

func writeSnapshotDiffResultJSON(w io.Writer, result *snapshotDiffResult) error {
	body, err := snapshotDiffJSON(result)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func snapshotDiffJSON(result *snapshotDiffResult) ([]byte, error) {
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func renderSnapshotDiffFormat(w io.Writer, result *snapshotDiffResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderSnapshotDiffCounterLine(w io.Writer, label string, counters snapshotDiffCounters) {
	fmt.Fprintf(w, "%s: added=%d removed=%d changed=%d\n", label, counters.Added, counters.Removed, counters.Changed)
}
