package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newSnapshotDiffCmd() *cobra.Command {
	var (
		jsonOut  bool
		exitCode bool
		sections []string
	)
	cmd := &cobra.Command{
		Use:   "diff <before.json> <after.json>",
		Short: "Compare two saved diagnostic snapshots.",
		Long: "Compare two saved global, team, or pipeline diagnostic snapshot JSON files and summarize " +
			"provenance, git, runtime, health, plan, next-action, instance, job, inbox, queue, schedule, intake, event, pipeline, ready-advance, and section-error changes.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sectionSet, err := parseSnapshotDiffSections(sections)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team snapshot diff: %v\n", err)
				return exitErr(2)
			}
			result, err := diffSnapshotFiles(args[0], args[1], snapshotDiffOptions{Sections: sectionSet})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team snapshot diff: %v\n", err)
				return exitErr(1)
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
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
	cmd.Flags().BoolVar(&exitCode, "exit-code", false, "Exit with status 1 when snapshots differ.")
	cmd.Flags().StringSliceVar(&sections, "section", nil, "Only compare sections: provenance, git, runtime, health, plan, next, instances, jobs, pipelines, inbox, queue, queue_quarantine, schedules, intake, events, advance, section_errors, or all. Can repeat or comma-separate.")
	return cmd
}

type snapshotDiffInput struct {
	Version          string                        `json:"version,omitempty"`
	CapturedAt       string                        `json:"captured_at,omitempty"`
	Repo             string                        `json:"repo,omitempty"`
	Provenance       *snapshotProvenance           `json:"provenance,omitempty"`
	Git              *snapshotGitInfo              `json:"git,omitempty"`
	Runtime          *runtimeInfo                  `json:"runtime,omitempty"`
	Health           *healthResult                 `json:"health,omitempty"`
	Plan             *planResult                   `json:"plan,omitempty"`
	Next             *nextActionResult             `json:"next,omitempty"`
	Team             *teamInfo                     `json:"team,omitempty"`
	Pipeline         string                        `json:"pipeline,omitempty"`
	Instances        []snapshotDiffInstance        `json:"instances,omitempty"`
	Jobs             []snapshotDiffJob             `json:"jobs,omitempty"`
	Inbox            []snapshotDiffInbox           `json:"inbox,omitempty"`
	Queue            []snapshotDiffQueueItem       `json:"queue,omitempty"`
	QueueQuarantine  []snapshotDiffQuarantine      `json:"queue_quarantine,omitempty"`
	Schedules        []snapshotDiffSchedule        `json:"schedules,omitempty"`
	ScheduleNext     []snapshotDiffSchedule        `json:"schedule_next,omitempty"`
	Intake           []snapshotDiffIntake          `json:"intake,omitempty"`
	IntakeDuplicates []snapshotDiffIntakeDuplicate `json:"intake_duplicates,omitempty"`
	Events           []snapshotDiffEvent           `json:"events,omitempty"`
	PipelineStatus   []pipelineStatusRow           `json:"pipeline_status,omitempty"`
	Status           *pipelineStatusRow            `json:"status,omitempty"`
	PipelineAdvance  []snapshotDiffAdvance         `json:"pipeline_advance_preview,omitempty"`
	AdvancePreview   []snapshotDiffAdvance         `json:"advance_preview,omitempty"`
	SectionErrors    map[string]string             `json:"section_errors,omitempty"`
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
	TotalChanges    int                  `json:"total_changes"`
	Provenance      snapshotDiffCounters `json:"provenance"`
	Git             snapshotDiffCounters `json:"git"`
	Runtime         snapshotDiffCounters `json:"runtime"`
	Health          snapshotDiffCounters `json:"health"`
	Plan            snapshotDiffCounters `json:"plan"`
	Next            snapshotDiffCounters `json:"next"`
	Instances       snapshotDiffCounters `json:"instances"`
	Jobs            snapshotDiffCounters `json:"jobs"`
	Pipelines       snapshotDiffCounters `json:"pipelines"`
	Inbox           snapshotDiffCounters `json:"inbox"`
	Queue           snapshotDiffCounters `json:"queue"`
	QueueQuarantine snapshotDiffCounters `json:"queue_quarantine"`
	Schedules       snapshotDiffCounters `json:"schedules"`
	Intake          snapshotDiffCounters `json:"intake"`
	Events          snapshotDiffCounters `json:"events"`
	Advance         snapshotDiffCounters `json:"advance"`
	SectionErrors   snapshotDiffCounters `json:"section_errors"`
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
	Meta            snapshotDiffMeta
	Provenance      map[string]string
	Git             map[string]string
	Runtime         map[string]string
	Health          map[string]string
	Plan            map[string]string
	Next            map[string]string
	Instances       map[string]string
	Jobs            map[string]string
	Pipelines       map[string]string
	Inbox           map[string]string
	Queue           map[string]string
	QueueQuarantine map[string]string
	Schedules       map[string]string
	Intake          map[string]string
	Events          map[string]string
	Advance         map[string]string
	SectionErrors   map[string]string
}

type snapshotDiffOptions struct {
	Sections map[string]bool
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
	if snapshotDiffSectionEnabled(opts.Sections, "next") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("next", before.Next, after.Next, &result.Summary.Next)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "instances") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("instances", before.Instances, after.Instances, &result.Summary.Instances)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "jobs") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("jobs", before.Jobs, after.Jobs, &result.Summary.Jobs)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "pipelines") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("pipelines", before.Pipelines, after.Pipelines, &result.Summary.Pipelines)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "inbox") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("inbox", before.Inbox, after.Inbox, &result.Summary.Inbox)...)
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
	if snapshotDiffSectionEnabled(opts.Sections, "advance") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("advance", before.Advance, after.Advance, &result.Summary.Advance)...)
	}
	if snapshotDiffSectionEnabled(opts.Sections, "section_errors") {
		result.Changes = append(result.Changes, diffSnapshotStringMaps("section_errors", before.SectionErrors, after.SectionErrors, &result.Summary.SectionErrors)...)
	}
	result.Summary.TotalChanges = len(result.Changes)
	return result, nil
}

func parseSnapshotDiffSections(values []string) (map[string]bool, error) {
	if len(values) == 0 {
		return nil, nil
	}
	valid := map[string]bool{
		"provenance":       true,
		"git":              true,
		"runtime":          true,
		"health":           true,
		"plan":             true,
		"next":             true,
		"instances":        true,
		"jobs":             true,
		"pipelines":        true,
		"inbox":            true,
		"queue":            true,
		"queue_quarantine": true,
		"schedules":        true,
		"intake":           true,
		"events":           true,
		"advance":          true,
		"section_errors":   true,
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
			if name == "quarantine" {
				name = "queue_quarantine"
			}
			if !valid[name] {
				return nil, fmt.Errorf("--section must be provenance, git, runtime, health, plan, next, instances, jobs, pipelines, inbox, queue, queue_quarantine, schedules, intake, events, advance, section_errors, or all")
			}
			out[name] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--section requires at least one non-empty section")
	}
	return out, nil
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
		Provenance:      snapshotDiffProvenanceMap(input.Provenance),
		Git:             snapshotDiffGitMap(input.Git),
		Runtime:         snapshotDiffRuntimeMap(input.Runtime),
		Health:          snapshotDiffHealthMap(input.Health),
		Plan:            snapshotDiffPlanMap(input.Plan),
		Next:            snapshotDiffNextMap(input.Next),
		Instances:       map[string]string{},
		Jobs:            map[string]string{},
		Pipelines:       map[string]string{},
		Inbox:           map[string]string{},
		Queue:           map[string]string{},
		QueueQuarantine: map[string]string{},
		Schedules:       map[string]string{},
		Intake:          map[string]string{},
		Events:          map[string]string{},
		Advance:         map[string]string{},
		SectionErrors:   map[string]string{},
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
		id := strings.TrimSpace(ev.ID)
		if id == "" {
			id = compactSnapshotDiffValue(ev.TS, ev.Action, ev.Instance, ev.Job)
		}
		if id == "" || id == "-" {
			continue
		}
		exitCode := ""
		if ev.ExitCode != nil {
			exitCode = fmt.Sprintf("exit_code=%d", *ev.ExitCode)
		}
		out.Events[id] = compactSnapshotDiffValue(ev.Action, ev.Instance, ev.Agent, ev.Job, ev.Ticket, ev.Status, ev.Branch, ev.PR, exitCode, ev.Message)
	}
	for _, row := range input.PipelineStatus {
		addSnapshotDiffPipelineMetrics(out.Pipelines, row, input.Pipeline)
	}
	if input.Status != nil {
		addSnapshotDiffPipelineMetrics(out.Pipelines, *input.Status, input.Pipeline)
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

func snapshotDiffNextMap(next *nextActionResult) map[string]string {
	out := map[string]string{}
	if next == nil {
		return out
	}
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
	detailsByCommand := map[string]operatorActionHint{}
	for _, detail := range next.ActionDetails {
		command := strings.TrimSpace(detail.Command)
		if command == "" {
			continue
		}
		if _, exists := detailsByCommand[command]; !exists {
			detailsByCommand[command] = detail
		}
	}
	seen := map[string]bool{}
	for _, action := range next.Actions {
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
	if value := strings.TrimSpace(runtime.Binary); value != "" {
		out["binary"] = value
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
	out["selected"] = fmt.Sprintf("%t", runtime.Selected)
	out["available"] = fmt.Sprintf("%t", runtime.Available)
	out["direct_run"] = fmt.Sprintf("%t", runtime.DirectRun)
	out["daemon_dispatch"] = fmt.Sprintf("%t", runtime.DaemonDispatch)
	out["direct_resume"] = fmt.Sprintf("%t", runtime.DirectResume)
	out["managed_resume"] = fmt.Sprintf("%t", runtime.ManagedResume)
	out["resume"] = fmt.Sprintf("%t", runtime.Resume)
	out["subagents"] = fmt.Sprintf("%t", runtime.Subagents)
	return out
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
	if provenance.Options.Tail != nil {
		out["tail"] = fmt.Sprintf("%d", *provenance.Options.Tail)
	}
	return out
}

func snapshotDiffScope(input snapshotDiffInput) (string, string) {
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
	renderSnapshotDiffCounterLine(w, "next", result.Summary.Next)
	renderSnapshotDiffCounterLine(w, "instances", result.Summary.Instances)
	renderSnapshotDiffCounterLine(w, "jobs", result.Summary.Jobs)
	renderSnapshotDiffCounterLine(w, "pipelines", result.Summary.Pipelines)
	renderSnapshotDiffCounterLine(w, "inbox", result.Summary.Inbox)
	renderSnapshotDiffCounterLine(w, "queue", result.Summary.Queue)
	renderSnapshotDiffCounterLine(w, "queue_quarantine", result.Summary.QueueQuarantine)
	renderSnapshotDiffCounterLine(w, "schedules", result.Summary.Schedules)
	renderSnapshotDiffCounterLine(w, "intake", result.Summary.Intake)
	renderSnapshotDiffCounterLine(w, "events", result.Summary.Events)
	renderSnapshotDiffCounterLine(w, "advance", result.Summary.Advance)
	renderSnapshotDiffCounterLine(w, "section_errors", result.Summary.SectionErrors)
	if len(result.Changes) == 0 {
		fmt.Fprintln(w, "details: none")
		return
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

func renderSnapshotDiffCounterLine(w io.Writer, label string, counters snapshotDiffCounters) {
	fmt.Fprintf(w, "%s: added=%d removed=%d changed=%d\n", label, counters.Added, counters.Removed, counters.Changed)
}
