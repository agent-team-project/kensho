package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedule",
		Aliases: []string{"schedules"},
		Short:   "Inspect and run declared schedule events.",
		Long:    "Inspect schedules declared in .agent_team/instances.toml and manually publish their schedule events.",
	}
	cmd.AddCommand(newScheduleLsCmd())
	cmd.AddCommand(newScheduleShowCmd())
	cmd.AddCommand(newScheduleDueCmd())
	cmd.AddCommand(newScheduleNextCmd())
	cmd.AddCommand(newScheduleFireCmd())
	cmd.AddCommand(newScheduleRunCmd())
	return cmd
}

func newScheduleLsCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List declared schedules.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseScheduleDueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			schedules, err := loadScheduleInfos(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule ls: %v\n", err)
				return exitErr(1)
			}
			return renderScheduleList(cmd.OutOrStdout(), schedules, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit schedules as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each schedule with a Go template, e.g. '{{.Name}} {{.Every}}'.")
	return cmd
}

func newScheduleShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <schedule>",
		Short: "Show one declared schedule.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseScheduleDueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadScheduleInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule show: %v\n", err)
				return exitErr(1)
			}
			return renderScheduleDetail(cmd.OutOrStdout(), info, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the schedule as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the schedule with a Go template, e.g. '{{.Name}} {{.Every}}'.")
	return cmd
}

func newScheduleDueCmd() *cobra.Command {
	var (
		repo     string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "due",
		Short: "List schedules due now.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule due: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule due: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule due: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseScheduleDueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule due: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			schedules, err := loadScheduleInfos(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule due: %v\n", err)
				return exitErr(1)
			}
			rows := dueScheduleRows(schedules, time.Now().UTC())
			return renderScheduleDueRows(cmd.OutOrStdout(), rows, jsonOut, tmpl, commands)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit due schedules as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only the due schedule preview command, one per line.")
	cmd.Flags().StringVar(&format, "format", "", "Render each due schedule with a Go template, e.g. '{{.Name}} {{.DueReason}}'.")
	return cmd
}

func newScheduleNextCmd() *cobra.Command {
	var (
		repo     string
		jsonOut  bool
		format   string
		limit    int
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "next",
		Short: "List declared schedules ordered by next run.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule next: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule next: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule next: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule next: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseScheduleDueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule next: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			schedules, err := loadScheduleInfos(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule next: %v\n", err)
				return exitErr(1)
			}
			rows := nextScheduleRows(schedules, time.Now().UTC(), limit)
			return renderScheduleNextRows(cmd.OutOrStdout(), rows, jsonOut, tmpl, commands)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit schedule forecast rows as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only due schedule preview commands, one per line.")
	cmd.Flags().StringVar(&format, "format", "", "Render each forecast row with a Go template, e.g. '{{.Name}} {{.Due}} {{.NextRun}}'.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Show at most this many schedules after ordering; 0 means all.")
	return cmd
}

func newScheduleFireCmd() *cobra.Command {
	var (
		repo          string
		dryRun        bool
		previewRoutes bool
		commands      bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "fire",
		Short: "Publish every schedule due now through the daemon.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: --preview-triggers requires --dry-run.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if wait && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || cmd.Flags().Changed("fail-on-failed")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parseScheduleFireFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule fire: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule fire: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if dryRun {
				result, previews, err := previewScheduleFire(teamDir, previewRoutes)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule fire: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderScheduleFireApplyCommand(cmd.OutOrStdout(), result != nil && result.WouldFire > 0, scheduleFireApplyCommandOptions{
						Repo:    repo,
						RepoSet: cmd.Flags().Changed("repo"),
					})
				}
				return renderScheduleFireResultWithPreviews(cmd.OutOrStdout(), result, previews, jsonOut, tmpl)
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule fire: daemon is not running — start it first with `agent-team daemon start`.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule fire: %v\n", err)
				return exitErr(1)
			}
			result, err := dc.ScheduleFire(dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule fire: %v\n", err)
				return exitErr(1)
			}
			var waited []scheduleWaitJob
			if wait {
				waited, err = waitForScheduleOutcomeJobs(cmd, teamDir, scheduleFireJobIDs(result), waitFilters, waitTimeout, waitInterval, "agent-team schedule fire")
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return err
				}
			}
			if err := renderScheduleFireCommandResult(cmd.OutOrStdout(), result, waited, jsonOut, tmpl); err != nil {
				return err
			}
			if failOnFailed && scheduleWaitJobsHaveFailed(waited) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview due schedules without publishing events or writing schedule clocks.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching schedule fire apply command when schedules are due.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After schedules publish pipeline jobs, wait for those jobs to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. pipeline_step, advance_dispatched, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step for every schedule-created job.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any schedule-created job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit fire results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the fire result with a Go template, e.g. '{{.Fired}} {{len .Schedules}}'.")
	return cmd
}

func newScheduleRunCmd() *cobra.Command {
	var (
		repo          string
		payload       string
		payloadFile   string
		dryRun        bool
		previewRoutes bool
		commands      bool
		wait          bool
		waitStatuses  []string
		waitEvents    []string
		waitNextState []string
		waitStep      string
		waitTimeout   time.Duration
		waitInterval  time.Duration
		failOnFailed  bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "run <schedule>",
		Short: "Publish one declared schedule event now.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --commands requires --dry-run.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --preview-triggers requires --dry-run.")
				return exitErr(2)
			}
			if waitInterval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --wait-interval must be >= 0.")
				return exitErr(2)
			}
			if waitTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --wait-timeout must be >= 0.")
				return exitErr(2)
			}
			if wait && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: --wait cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !wait && (cmd.Flags().Changed("wait-status") || cmd.Flags().Changed("wait-event") || cmd.Flags().Changed("wait-next-state") || cmd.Flags().Changed("wait-step") || cmd.Flags().Changed("wait-timeout") || cmd.Flags().Changed("wait-interval") || cmd.Flags().Changed("fail-on-failed")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team schedule run: wait-related flags require --wait.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
				return exitErr(2)
			}
			waitFilters := jobWaitFilters{}
			if wait {
				waitFilters, err = parseJobCommandWaitFilters(cmd, waitStatuses, waitEvents, waitNextState, waitStep)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadScheduleInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
				return exitErr(1)
			}
			override, label, err := optionalPayloadInput(payload, payloadFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
				return exitErr(2)
			}
			eventPayload, err := scheduleEventPayload(info, override, label)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
				return exitErr(2)
			}
			ev := &intake.Event{Type: topology.EventSchedule, Payload: eventPayload}
			if dryRun {
				var triggerPreview *eventPublishPreview
				if previewRoutes {
					triggerPreview, err = previewEventPublish(teamDir, ev.Type, ev.Payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team schedule run: %v\n", err)
						return exitErr(1)
					}
				}
				if commands {
					return renderScheduleRunApplyCommand(cmd.OutOrStdout(), true, scheduleRunApplyCommandOptions{
						Name:           info.Name,
						Repo:           repo,
						RepoSet:        cmd.Flags().Changed("repo"),
						Payload:        payload,
						PayloadSet:     cmd.Flags().Changed("payload"),
						PayloadFile:    payloadFile,
						PayloadFileSet: cmd.Flags().Changed("payload-file"),
						PayloadRaw:     strings.TrimSpace(string(override)),
					})
				}
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, nil, nil, nil, triggerPreview)
			}
			return publishScheduleEvent(cmd, repo, ev, "agent-team schedule run", wait, waitFilters, waitTimeout, waitInterval, failOnFailed, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&payload, "payload", "", "Additional JSON object merged into the declared schedule payload.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read additional schedule payload JSON from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the schedule event without publishing it.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching schedule run apply command.")
	cmd.Flags().BoolVar(&wait, "wait", false, "After the schedule publishes pipeline jobs, wait for those jobs to reach a lifecycle status, event, or next-step state.")
	cmd.Flags().StringSliceVar(&waitStatuses, "wait-status", nil, "With --wait, status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitEvents, "wait-event", nil, "With --wait, last event to wait for, e.g. pipeline_step, advance_dispatched, closed, or pipeline_done. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&waitNextState, "wait-next-state", nil, "With --wait, next-step state to wait for: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&waitStep, "wait-step", "", "With --wait, pipeline step id that must be the current next step for every schedule-created job.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&waitInterval, "wait-interval", 500*time.Millisecond, "Polling interval with --wait.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "With --wait, exit 1 if any schedule-created job resolves to failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the event and outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the event result with a Go template, e.g. '{{.Event.Type}} {{.DryRun}}'.")
	return cmd
}

func scheduleEventPayload(info scheduleInfo, overrideRaw []byte, overrideLabel string) (map[string]any, error) {
	payload := copyMap(info.Payload)
	if overrideLabel == "" {
		overrideLabel = "--payload"
	}
	if strings.TrimSpace(string(overrideRaw)) != "" {
		var extra map[string]any
		if err := json.Unmarshal(overrideRaw, &extra); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON: %w", overrideLabel, err)
		}
		for key, value := range extra {
			payload[key] = value
		}
	}
	payload["source"] = "schedule"
	payload["name"] = info.Name
	return payload, nil
}

func previewScheduleFire(teamDir string, previewRoutes bool) (*daemon.ScheduleFireResult, map[string]*eventPublishPreview, error) {
	schedules, err := loadScheduleInfos(teamDir)
	if err != nil {
		return nil, nil, err
	}
	rows := dueScheduleRows(schedules, time.Now().UTC())
	result := &daemon.ScheduleFireResult{DryRun: true, Schedules: []daemon.ScheduleFireItem{}}
	var previews map[string]*eventPublishPreview
	if previewRoutes {
		previews = map[string]*eventPublishPreview{}
	}
	for _, row := range rows {
		payload, err := scheduleEventPayload(row, nil, "")
		if err != nil {
			return nil, nil, err
		}
		item := daemon.ScheduleFireItem{
			Name:      row.Name,
			EventType: topology.EventSchedule,
			Payload:   payload,
			Reason:    row.DueReason,
		}
		result.WouldFire++
		result.Schedules = append(result.Schedules, item)
		if previewRoutes {
			preview, err := previewEventPublish(teamDir, topology.EventSchedule, payload)
			if err != nil {
				return nil, nil, err
			}
			previews[row.Name] = preview
		}
	}
	return result, previews, nil
}

type scheduleInfo struct {
	Name        string         `json:"name"`
	Event       string         `json:"event"`
	Every       string         `json:"every"`
	RunOnStart  bool           `json:"run_on_start"`
	Payload     map[string]any `json:"payload"`
	LastSeenAt  *time.Time     `json:"last_seen_at,omitempty"`
	LastFiredAt *time.Time     `json:"last_fired_at,omitempty"`
	NextRun     *time.Time     `json:"next_run_at,omitempty"`
	Due         bool           `json:"due,omitempty"`
	DueReason   string         `json:"due_reason,omitempty"`
}

func loadScheduleInfos(teamDir string) ([]scheduleInfo, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, nil
	}
	states, err := loadScheduleStateMap(teamDir)
	if err != nil {
		return nil, err
	}
	infos := make([]scheduleInfo, 0, len(top.Schedules))
	for _, s := range top.SortedSchedules() {
		infos = append(infos, scheduleInfoFromTopology(s, states[s.Name]))
	}
	return infos, nil
}

func loadScheduleInfo(teamDir, name string) (scheduleInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return scheduleInfo{}, fmt.Errorf("schedule name is required")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return scheduleInfo{}, err
	}
	if top == nil || top.Schedules[name] == nil {
		return scheduleInfo{}, fmt.Errorf("schedule %q not found", name)
	}
	states, err := loadScheduleStateMap(teamDir)
	if err != nil {
		return scheduleInfo{}, err
	}
	return scheduleInfoFromTopology(top.Schedules[name], states[name]), nil
}

func loadScheduleStateMap(teamDir string) (map[string]*daemon.ScheduleState, error) {
	states, err := daemon.ListScheduleStates(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	out := make(map[string]*daemon.ScheduleState, len(states))
	for _, state := range states {
		out[state.Name] = state
	}
	return out, nil
}

func scheduleInfoFromTopology(s *topology.Schedule, state *daemon.ScheduleState) scheduleInfo {
	info := scheduleInfo{
		Name:       s.Name,
		Event:      topology.EventSchedule,
		Every:      s.Every.String(),
		RunOnStart: s.RunOnStart,
		Payload:    s.EventPayload(),
	}
	if state != nil {
		lastSeen := state.LastSeenAt
		lastFired := state.LastFiredAt
		next := state.LastSeenAt.Add(s.Every)
		info.LastSeenAt = &lastSeen
		if !state.LastFiredAt.IsZero() {
			info.LastFiredAt = &lastFired
		}
		info.NextRun = &next
	}
	return info
}

func renderScheduleList(w io.Writer, schedules []scheduleInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(schedules)
	}
	if tmpl != nil {
		for _, info := range schedules {
			if err := tmpl.Execute(w, info); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(schedules) == 0 {
		fmt.Fprintln(w, "(no schedules declared)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tEVERY\tRUN_ON_START\tLAST_FIRED\tNEXT_RUN\tPAYLOAD")
	for _, info := range schedules {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			info.Name, info.Every, yesNo(info.RunOnStart), scheduleTime(info.LastFiredAt), scheduleTime(info.NextRun), summariseSchedulePayload(info.Payload))
	}
	_ = tw.Flush()
	return nil
}

func renderScheduleDetail(w io.Writer, info scheduleInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(info)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, info); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	fmt.Fprintf(w, "Schedule:     %s\n", info.Name)
	fmt.Fprintf(w, "Event:        %s\n", info.Event)
	fmt.Fprintf(w, "Every:        %s\n", info.Every)
	fmt.Fprintf(w, "Run On Start: %s\n", yesNo(info.RunOnStart))
	if info.LastSeenAt != nil {
		fmt.Fprintf(w, "Last Seen:    %s\n", scheduleTime(info.LastSeenAt))
	}
	if info.LastFiredAt != nil {
		fmt.Fprintf(w, "Last Fired:   %s\n", scheduleTime(info.LastFiredAt))
	}
	if info.NextRun != nil {
		fmt.Fprintf(w, "Next Run:     %s\n", scheduleTime(info.NextRun))
	}
	fmt.Fprintf(w, "Payload:      %s\n", summariseSchedulePayload(info.Payload))
	return nil
}

func dueScheduleRows(schedules []scheduleInfo, now time.Time) []scheduleInfo {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows := make([]scheduleInfo, 0, len(schedules))
	for _, info := range schedules {
		due, reason := scheduleDueState(info, now)
		if !due {
			continue
		}
		info.Due = true
		info.DueReason = reason
		rows = append(rows, info)
	}
	return rows
}

func scheduleDueState(info scheduleInfo, now time.Time) (bool, string) {
	if info.LastSeenAt == nil {
		if info.RunOnStart {
			return true, "run_on_start"
		}
		return false, ""
	}
	if info.NextRun != nil && !info.NextRun.After(now) {
		return true, "interval"
	}
	return false, ""
}

func nextScheduleRows(schedules []scheduleInfo, now time.Time, limit int) []scheduleInfo {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows := make([]scheduleInfo, 0, len(schedules))
	for _, info := range schedules {
		due, reason := scheduleDueState(info, now)
		info.Due = due
		info.DueReason = reason
		if due && (info.NextRun == nil || info.NextRun.Before(now)) {
			next := now
			info.NextRun = &next
		}
		rows = append(rows, info)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Due != rows[j].Due {
			return rows[i].Due
		}
		if rows[i].NextRun == nil && rows[j].NextRun != nil {
			return false
		}
		if rows[i].NextRun != nil && rows[j].NextRun == nil {
			return true
		}
		if rows[i].NextRun != nil && rows[j].NextRun != nil && !rows[i].NextRun.Equal(*rows[j].NextRun) {
			return rows[i].NextRun.Before(*rows[j].NextRun)
		}
		return rows[i].Name < rows[j].Name
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func parseScheduleDueFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("schedule-due-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderScheduleDueRows(w io.Writer, rows []scheduleInfo, jsonOut bool, tmpl *template.Template, commands bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if commands {
		return renderScheduleDueCommands(w, rows)
	}
	if tmpl != nil {
		for _, row := range rows {
			if err := tmpl.Execute(w, row); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no schedules due)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tEVERY\tREASON\tLAST_FIRED\tNEXT_RUN\tPAYLOAD")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Name, row.Every, row.DueReason, scheduleTime(row.LastFiredAt), scheduleTime(row.NextRun), summariseSchedulePayload(row.Payload))
	}
	_ = tw.Flush()
	return nil
}

func renderScheduleNextRows(w io.Writer, rows []scheduleInfo, jsonOut bool, tmpl *template.Template, commands bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if commands {
		return renderScheduleDueCommands(w, rows)
	}
	if tmpl != nil {
		for _, row := range rows {
			if err := tmpl.Execute(w, row); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no schedules declared)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tEVERY\tDUE\tREASON\tNEXT_RUN\tLAST_FIRED\tPAYLOAD")
	for _, row := range rows {
		reason := row.DueReason
		if reason == "" {
			reason = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Name, row.Every, yesNo(row.Due), reason, scheduleTime(row.NextRun), scheduleTime(row.LastFiredAt), summariseSchedulePayload(row.Payload))
	}
	_ = tw.Flush()
	return nil
}

func renderScheduleDueCommands(w io.Writer, rows []scheduleInfo) error {
	for _, row := range rows {
		if !row.Due {
			continue
		}
		_, err := fmt.Fprintln(w, "agent-team schedule fire --dry-run --preview-triggers")
		return err
	}
	return nil
}

type scheduleFireApplyCommandOptions struct {
	Repo    string
	RepoSet bool
}

func renderScheduleFireApplyCommand(w io.Writer, hasAction bool, opts scheduleFireApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	args := []string{"agent-team", "schedule", "fire"}
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		args = append(args, "--repo", opts.Repo)
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(args), " "))
	return err
}

type scheduleRunApplyCommandOptions struct {
	Name           string
	Repo           string
	RepoSet        bool
	Payload        string
	PayloadSet     bool
	PayloadFile    string
	PayloadFileSet bool
	PayloadRaw     string
}

func renderScheduleRunApplyCommand(w io.Writer, hasAction bool, opts scheduleRunApplyCommandOptions) error {
	if !hasAction {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(scheduleRunApplyCommandArgs(opts)), " "))
	return err
}

func scheduleRunApplyCommandArgs(opts scheduleRunApplyCommandOptions) []string {
	args := []string{"agent-team", "schedule", "run", opts.Name}
	if opts.RepoSet && strings.TrimSpace(opts.Repo) != "" {
		args = append(args, "--repo", opts.Repo)
	}
	if opts.PayloadSet && strings.TrimSpace(opts.Payload) != "" {
		args = append(args, "--payload", opts.Payload)
	}
	payloadFile := strings.TrimSpace(opts.PayloadFile)
	if opts.PayloadFileSet && payloadFile != "" && payloadFile != "-" {
		args = append(args, "--payload-file", opts.PayloadFile)
	}
	if opts.PayloadFileSet && payloadFile == "-" && strings.TrimSpace(opts.PayloadRaw) != "" {
		args = append(args, "--payload", opts.PayloadRaw)
	}
	return args
}

func parseScheduleFireFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("schedule-fire-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderScheduleFireResult(w io.Writer, result *daemon.ScheduleFireResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &daemon.ScheduleFireResult{Schedules: []daemon.ScheduleFireItem{}}
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
	if result.DryRun {
		fmt.Fprintf(w, "schedule fire dry-run: would_fire=%d\n", result.WouldFire)
	} else {
		fmt.Fprintf(w, "schedule fire: fired=%d\n", result.Fired)
	}
	if len(result.Schedules) == 0 {
		fmt.Fprintln(w, "(no schedules due)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCHEDULE\tREASON\tEVENT\tOUTCOMES\tPAYLOAD")
	for _, item := range result.Schedules {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			item.Name, item.Reason, item.EventType, summariseScheduleFireOutcomes(item.Outcomes), summariseSchedulePayload(item.Payload))
	}
	return tw.Flush()
}

type scheduleFireResultWithPreviews struct {
	*daemon.ScheduleFireResult
	Previews map[string]*eventPublishPreview `json:"previews,omitempty"`
}

type scheduleFireCommandResult struct {
	*daemon.ScheduleFireResult
	WaitedJobs []scheduleWaitJob `json:"waited_jobs,omitempty"`
}

type scheduleWaitJob struct {
	ID         string     `json:"id"`
	Ticket     string     `json:"ticket,omitempty"`
	Pipeline   string     `json:"pipeline,omitempty"`
	Status     job.Status `json:"status"`
	LastEvent  string     `json:"last_event,omitempty"`
	LastStatus string     `json:"last_status,omitempty"`
	NextState  string     `json:"next_state,omitempty"`
	NextStep   string     `json:"next_step,omitempty"`
}

func renderScheduleFireResultWithPreviews(w io.Writer, result *daemon.ScheduleFireResult, previews map[string]*eventPublishPreview, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &daemon.ScheduleFireResult{Schedules: []daemon.ScheduleFireItem{}}
	}
	if len(previews) == 0 {
		return renderScheduleFireResult(w, result, jsonOut, tmpl)
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(scheduleFireResultWithPreviews{ScheduleFireResult: result, Previews: previews})
	}
	if tmpl != nil {
		return renderScheduleFireResult(w, result, false, tmpl)
	}
	if err := renderScheduleFireResult(w, result, false, nil); err != nil {
		return err
	}
	for _, item := range result.Schedules {
		preview := previews[item.Name]
		if preview == nil {
			continue
		}
		fmt.Fprintf(w, "Routes for %s:\n", item.Name)
		if !eventPublishPreviewHasRoutes(preview) {
			fmt.Fprintln(w, "  none")
			continue
		}
		if err := renderEventPublishRoutePreview(w, preview); err != nil {
			return err
		}
	}
	return nil
}

func renderScheduleFireCommandResult(w io.Writer, result *daemon.ScheduleFireResult, waited []scheduleWaitJob, jsonOut bool, tmpl *template.Template) error {
	if len(waited) == 0 {
		return renderScheduleFireResult(w, result, jsonOut, tmpl)
	}
	wrapped := scheduleFireCommandResult{ScheduleFireResult: result, WaitedJobs: waited}
	if jsonOut {
		return json.NewEncoder(w).Encode(wrapped)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, wrapped); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if err := renderScheduleFireResult(w, result, false, nil); err != nil {
		return err
	}
	renderScheduleWaitJobs(w, waited)
	return nil
}

func summariseScheduleFireOutcomes(outcomes []daemon.EventOutcome) string {
	if len(outcomes) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		target := outcome.Instance
		if outcome.InstanceID != "" && outcome.InstanceID != outcome.Instance {
			target = target + "/" + outcome.InstanceID
		}
		if target == "" {
			target = "-"
		}
		action := outcome.Action
		if action == "" {
			action = "unknown"
		}
		part := action + "=" + target
		if outcome.Reason != "" {
			part += "(" + outcome.Reason + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}

func publishScheduleEvent(cmd *cobra.Command, target string, ev *intake.Event, prefix string, wait bool, waitFilters jobWaitFilters, waitTimeout, waitInterval time.Duration, failOnFailed bool, jsonOut bool, tmpl *template.Template) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: daemon is not running — start it first with `agent-team daemon start`.\n", prefix)
		return exitErr(2)
	}
	res, err := dc.PublishEvent(ev.Type, ev.Payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return exitErr(1)
	}
	var waited []scheduleWaitJob
	if wait {
		waited, err = waitForScheduleOutcomeJobs(cmd, teamDir, eventResponseJobIDs(res), waitFilters, waitTimeout, waitInterval, prefix)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
	result := intakePublishResult{Event: ev, Outcome: res, WaitedJobs: waited}
	if jsonOut {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return err
		}
		if failOnFailed && scheduleWaitJobsHaveFailed(waited) {
			return exitErr(1)
		}
		return nil
	}
	if tmpl != nil {
		if err := renderIntakeTemplate(cmd.OutOrStdout(), result, tmpl); err != nil {
			return err
		}
		if failOnFailed && scheduleWaitJobsHaveFailed(waited) {
			return exitErr(1)
		}
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Event: %s\n", ev.Type)
	if err := renderIntakeOutcome(cmd.OutOrStdout(), res); err != nil {
		return err
	}
	if len(waited) > 0 {
		renderScheduleWaitJobs(cmd.OutOrStdout(), waited)
	}
	if failOnFailed && scheduleWaitJobsHaveFailed(waited) {
		return exitErr(1)
	}
	return nil
}

func scheduleFireJobIDs(result *daemon.ScheduleFireResult) []string {
	if result == nil {
		return nil
	}
	ids := make([]string, 0)
	for _, item := range result.Schedules {
		ids = append(ids, eventOutcomeJobIDs(item.Outcomes)...)
	}
	return uniqueNonEmptyStrings(ids)
}

func eventResponseJobIDs(res *eventResponse) []string {
	if res == nil {
		return nil
	}
	return eventOutcomeJobIDs(res.Outcomes)
}

func eventOutcomeJobIDs(outcomes []daemon.EventOutcome) []string {
	ids := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if id := job.NormalizeID(outcome.JobID); id != "" {
			ids = append(ids, id)
		}
	}
	return uniqueNonEmptyStrings(ids)
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func waitForScheduleOutcomeJobs(cmd *cobra.Command, teamDir string, ids []string, filters jobWaitFilters, timeout, interval time.Duration, prefix string) ([]scheduleWaitJob, error) {
	ids = uniqueNonEmptyStrings(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	jobs := make([]*job.Job, 0, len(ids))
	for _, id := range ids {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	ctx := cmd.Context()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	waited, err := runPipelineWait(ctx, teamDir, jobs, filters.statuses, filters.events, filters.nextStates, filters.nextStateSet, filters.step, interval)
	if err != nil {
		var timeoutErr *pipelineWaitTimeoutError
		if errors.As(err, &timeoutErr) {
			return nil, fmt.Errorf("%s: timed out waiting for %s; pending: %s", prefix, jobWaitConditionList(filters.statuses, filters.events, filters.nextStates, filters.nextStateSet, filters.step), pipelineWaitPendingSummaryWithNext(timeoutErr.Pending, filters.nextStateSet || strings.TrimSpace(filters.step) != ""))
		}
		return nil, err
	}
	return scheduleWaitJobsFromJobs(waited), nil
}

func scheduleWaitJobsFromJobs(jobs []*job.Job) []scheduleWaitJob {
	rows := make([]scheduleWaitJob, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		next := inspectNextJobStep(j)
		rows = append(rows, scheduleWaitJob{
			ID:         j.ID,
			Ticket:     j.Ticket,
			Pipeline:   j.Pipeline,
			Status:     j.Status,
			LastEvent:  j.LastEvent,
			LastStatus: j.LastStatus,
			NextState:  next.State,
			NextStep:   jobWaitNextStep(next),
		})
	}
	return rows
}

func renderScheduleWaitJobs(w io.Writer, rows []scheduleWaitJob) {
	if len(rows) == 0 {
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "WAITED JOB\tSTATUS\tPIPELINE\tNEXT\tLAST EVENT")
	for _, row := range rows {
		next := row.NextState
		if row.NextStep != "" {
			next = next + ":" + row.NextStep
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", row.ID, row.Status, emptyDash(row.Pipeline), emptyDash(next), emptyDash(row.LastEvent))
	}
	_ = tw.Flush()
}

func scheduleWaitJobsHaveFailed(rows []scheduleWaitJob) bool {
	for _, row := range rows {
		if row.Status == job.StatusFailed {
			return true
		}
	}
	return false
}

func summariseSchedulePayload(payload map[string]any) string {
	if len(payload) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, payload[key]))
	}
	return strings.Join(parts, ",")
}

func scheduleTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
