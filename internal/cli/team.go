package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Inspect declared agent teams.",
		Long:  "Inspect team declarations loaded from .agent_team/instances.toml.",
	}
	cmd.AddCommand(newTeamLsCmd())
	cmd.AddCommand(newTeamShowCmd())
	cmd.AddCommand(newTeamPsCmd())
	cmd.AddCommand(newTeamJobsCmd())
	cmd.AddCommand(newTeamPipelinesCmd())
	cmd.AddCommand(newTeamSchedulesCmd())
	cmd.AddCommand(newTeamHealthCmd())
	cmd.AddCommand(newTeamStatusCmd())
	return cmd
}

func newTeamLsCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List declared teams.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			teams, err := loadTeamInfos(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ls: %v\n", err)
				return exitErr(1)
			}
			return renderTeamList(cmd.OutOrStdout(), teams, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit teams as JSON.")
	return cmd
}

func newTeamShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <team>",
		Short: "Show one declared team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadTeamInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team show: %v\n", err)
				return exitErr(1)
			}
			return renderTeamDetail(cmd.OutOrStdout(), info, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the team as JSON.")
	return cmd
}

func newTeamPsCmd() *cobra.Command {
	var (
		repo     string
		watch    bool
		noClear  bool
		interval time.Duration
		jsonOut  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "ps <team>",
		Aliases: []string{"instances"},
		Short:   "List instances owned by one team.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team ps: --interval must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				return runTeamPsWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], interval, jsonOut, clear)
			}
			rows, err := collectTeamPsRows(teamDir, args[0], time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ps: %v\n", err)
				return exitErr(1)
			}
			return renderTeamPs(cmd.OutOrStdout(), rows, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team instances until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team instances as JSON.")
	return cmd
}

func newTeamJobsCmd() *cobra.Command {
	var (
		repo    string
		status  string
		sortBy  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "jobs <team>",
		Short: "List jobs owned by one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team jobs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseJobSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(2)
			}
			var statusFilter job.Status
			if strings.TrimSpace(status) != "" {
				statusFilter, err = job.ParseStatus(status)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			jobs, err := collectTeamJobs(teamDir, args[0], statusFilter, sortMode)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(1)
			}
			return renderTeamJobs(cmd.OutOrStdout(), jobs, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", "", "Filter by job status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&sortBy, "sort", "id", "Sort jobs by id, status, target, ticket, created, updated, instance, branch, or pr.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team jobs as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newTeamPipelinesCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "pipelines <team>",
		Short: "List pipeline status for one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team pipelines: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineStatusFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team pipelines: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			rows, err := collectTeamPipelineStatus(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team pipelines: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineStatusRows(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team pipeline status as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline with a Go template, e.g. '{{.Pipeline}} {{.ReadySteps}}'.")
	return cmd
}

func newTeamSchedulesCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "schedules <team>",
		Short: "List schedules owned by one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team schedules: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseScheduleDueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team schedules: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			schedules, err := collectTeamSchedules(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team schedules: %v\n", err)
				return exitErr(1)
			}
			return renderTeamSchedules(cmd.OutOrStdout(), schedules, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team schedules as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each schedule with a Go template, e.g. '{{.Name}} {{.Every}}'.")
	return cmd
}

func newTeamHealthCmd() *cobra.Command {
	var (
		repo        string
		includeJobs bool
		quiet       bool
		jsonOut     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "health <team>",
		Short: "Check health for one declared team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team health: choose one of --quiet or --json.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			snapshot, err := collectTeamHealth(teamDir, args[0], time.Now().UTC(), includeJobs)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team health: %v\n", err)
				return exitErr(1)
			}
			if !quiet {
				if err := renderTeamHealth(cmd.OutOrStdout(), snapshot, jsonOut); err != nil {
					return err
				}
			}
			if snapshot.Health != nil && !snapshot.Health.Healthy {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&includeJobs, "jobs", false, "Include team-owned job and pipeline health.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team health as JSON.")
	return cmd
}

func newTeamStatusCmd() *cobra.Command {
	var (
		repo     string
		watch    bool
		noClear  bool
		interval time.Duration
		jsonOut  bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status <team>",
		Short: "Summarize one team's instances, jobs, and pipelines.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team status: --interval must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				return runTeamStatusWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], interval, jsonOut, clear)
			}
			snapshot, err := collectTeamStatus(teamDir, args[0], time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team status: %v\n", err)
				return exitErr(1)
			}
			return renderTeamStatus(cmd.OutOrStdout(), snapshot, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team status until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team status as JSON.")
	return cmd
}

type teamInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Instances   []string `json:"instances,omitempty"`
	Pipelines   []string `json:"pipelines,omitempty"`
	Schedules   []string `json:"schedules,omitempty"`
}

type teamStatusSnapshot struct {
	Team            teamInfo            `json:"team"`
	CheckedAt       string              `json:"checked_at"`
	InstanceSummary psSummaryJSON       `json:"instance_summary"`
	Instances       []psJSONRow         `json:"instances,omitempty"`
	JobSummary      jobSummary          `json:"job_summary"`
	PipelineStatus  []pipelineStatusRow `json:"pipeline_status,omitempty"`
	Schedules       []scheduleInfo      `json:"schedules,omitempty"`
	Actions         []string            `json:"actions,omitempty"`
}

type teamHealthSnapshot struct {
	Team   teamInfo      `json:"team"`
	Health *healthResult `json:"health"`
}

func loadTeamInfos(teamDir string) ([]teamInfo, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, nil
	}
	infos := make([]teamInfo, 0, len(top.Teams))
	for _, team := range top.SortedTeams() {
		infos = append(infos, teamInfoFromTopology(team))
	}
	return infos, nil
}

func loadTeamInfo(teamDir, name string) (teamInfo, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return teamInfo{}, err
	}
	if top == nil {
		return teamInfo{}, fmt.Errorf("team %q not found", strings.TrimSpace(name))
	}
	return teamInfoFromTopology(team), nil
}

func loadTopologyTeam(teamDir, name string) (*topology.Topology, *topology.Team, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, fmt.Errorf("team name is required")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, nil, err
	}
	if top == nil || top.Teams[name] == nil {
		return top, nil, fmt.Errorf("team %q not found", name)
	}
	return top, top.Teams[name], nil
}

func teamInfoFromTopology(team *topology.Team) teamInfo {
	if team == nil {
		return teamInfo{}
	}
	return teamInfo{
		Name:        team.Name,
		Description: team.Description,
		Instances:   append([]string(nil), team.Instances...),
		Pipelines:   append([]string(nil), team.Pipelines...),
		Schedules:   append([]string(nil), team.Schedules...),
	}
}

func collectTeamStatus(teamDir, name string, now time.Time) (*teamStatusSnapshot, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	instanceRows := teamInstanceRows(top, team, rows)
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	pipelineStatus, err := collectPipelineStatusRows(teamDir, "")
	if err != nil {
		return nil, err
	}
	schedules, err := loadScheduleInfos(teamDir)
	if err != nil {
		return nil, err
	}
	snapshot := &teamStatusSnapshot{
		Team:            teamInfoFromTopology(team),
		CheckedAt:       now.UTC().Format(time.RFC3339),
		InstanceSummary: psSummaryRows(instanceRows),
		Instances:       psJSONRows(instanceRows),
		JobSummary:      summarizeJobs(teamJobs(top, team, jobs)),
		PipelineStatus:  teamPipelineStatus(team, pipelineStatus),
		Schedules:       teamSchedules(team, schedules),
	}
	snapshot.Actions = teamStatusActions(top, team, snapshot)
	return snapshot, nil
}

func collectTeamHealth(teamDir, name string, now time.Time, includeJobs bool) (*teamHealthSnapshot, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	healthRows := teamRuntimeRows(top, team, rows)
	scoped := teamScopedTopology(top, team)
	result := buildHealthWithDaemonStatus(collectDaemonStatus(teamDir), healthRows, scoped, now, healthOptions{})
	if includeJobs {
		if err := addTeamJobHealth(result, teamDir, top, team, now); err != nil {
			return nil, err
		}
	}
	return &teamHealthSnapshot{Team: teamInfoFromTopology(team), Health: result}, nil
}

func collectTeamPsRows(teamDir, name string, now time.Time) ([]instanceRow, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	return teamInstanceRows(top, team, rows), nil
}

func addTeamJobHealth(result *healthResult, teamDir string, top *topology.Topology, team *topology.Team, now time.Time) error {
	if result == nil {
		return nil
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return err
	}
	ownedJobs := teamJobs(top, team, jobs)
	ownedIDs := jobIDSet(ownedJobs)
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	teamQueue := queueItemsForJobs(queueItems, ownedJobs)
	result.Queue = summarizeQueueItems(teamQueue, now.UTC())
	if result.Queue.Dead > 0 {
		result.addIssueWithSeverityAndActions(
			"queue_dead_letter",
			"error",
			"",
			"",
			"",
			"",
			fmt.Sprintf("team %q queue has %d dead-letter item(s)", team.Name, result.Queue.Dead),
			teamQueueActions(ownedJobs, teamQueue),
		)
	}
	triage, err := collectJobTriage(teamDir, now.UTC(), defaultJobTriageStaleAfter)
	if err != nil {
		return err
	}
	triage.Summary = summarizeJobs(ownedJobs)
	triage.Queue = result.Queue
	triage.Attention = filterJobTriageItemsByJobIDs(triage.Attention, ownedIDs)
	triage.ReadySteps = filterJobReadyRowsByJobIDs(triage.ReadySteps, ownedIDs)
	triage.StatusPreviews = filterJobStatusPreviewsByJobIDs(triage.StatusPreviews, ownedIDs)
	result.Jobs = &triage
	result.JobStatus = triage.StatusPreviews
	for _, item := range triage.Attention {
		result.addJobIssue(item)
	}
	for _, preview := range triage.StatusPreviews {
		if !preview.Changed || preview.After != job.StatusBlocked {
			continue
		}
		message := fmt.Sprintf("job %q status file reports blocked", preview.JobID)
		if strings.TrimSpace(preview.Message) != "" {
			message += ": " + strings.TrimSpace(preview.Message)
		}
		result.addIssueWithSeverityAndActions("job_status_blocked", "error", preview.Instance, preview.JobID, string(preview.After), preview.Phase, message, []string{
			fmt.Sprintf("agent-team job unblock %s <answer...>", preview.JobID),
		})
	}
	pipelineStatus, err := collectTeamPipelineStatus(teamDir, team.Name)
	if err != nil {
		return err
	}
	result.PipelineStatus = pipelineStatus
	for _, row := range pipelineStatus {
		if row.FailedSteps > 0 {
			result.addIssueWithSeverityAndActions(
				"pipeline_failed_step",
				"error",
				"",
				"",
				"",
				"",
				fmt.Sprintf("pipeline %q has %d failed step(s)", row.Pipeline, row.FailedSteps),
				row.Actions,
			)
		}
		if row.BlockedSteps > 0 {
			result.addIssueWithSeverityAndActions(
				"pipeline_blocked_step",
				"warning",
				"",
				"",
				"",
				"",
				fmt.Sprintf("pipeline %q has %d blocked step(s)", row.Pipeline, row.BlockedSteps),
				row.Actions,
			)
		}
	}
	return nil
}

func collectTeamJobs(teamDir, name string, status job.Status, sortMode string) ([]*job.Job, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	owned := teamJobs(top, team, jobs)
	if status != "" {
		filtered := owned[:0]
		for _, j := range owned {
			if j.Status == status {
				filtered = append(filtered, j)
			}
		}
		owned = filtered
	}
	sortJobs(owned, sortMode)
	return owned, nil
}

func collectTeamPipelineStatus(teamDir, name string) ([]pipelineStatusRow, error) {
	_, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPipelineStatusRows(teamDir, "")
	if err != nil {
		return nil, err
	}
	return teamPipelineStatus(team, rows), nil
}

func collectTeamSchedules(teamDir, name string) ([]scheduleInfo, error) {
	_, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	schedules, err := loadScheduleInfos(teamDir)
	if err != nil {
		return nil, err
	}
	return teamSchedules(team, schedules), nil
}

func runTeamPsWatch(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, jsonOut bool, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		rows, err := collectTeamPsRows(teamDir, name, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := renderTeamPsWithClear(w, rows, jsonOut, clear); err != nil {
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

func runTeamStatusWatch(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, jsonOut bool, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectTeamStatus(teamDir, name, time.Now().UTC())
		if err != nil {
			return err
		}
		if jsonOut {
			if err := json.NewEncoder(w).Encode(snapshot); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			if err := renderTeamStatus(w, snapshot, false); err != nil {
				return err
			}
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

func teamInstanceRows(top *topology.Topology, team *topology.Team, rows []instanceRow) []instanceRow {
	if team == nil {
		return nil
	}
	rowByName := map[string]instanceRow{}
	rowsByAgent := map[string][]instanceRow{}
	for _, row := range rows {
		rowByName[row.Instance] = row
		rowsByAgent[row.Agent] = append(rowsByAgent[row.Agent], row)
	}
	var out []instanceRow
	seen := map[string]bool{}
	for _, name := range team.Instances {
		inst := top.Instances[name]
		if inst == nil {
			continue
		}
		if inst.Ephemeral {
			addedLive := false
			for _, row := range rowsByAgent[inst.Agent] {
				if seen[row.Instance] {
					continue
				}
				out = append(out, row)
				seen[row.Instance] = true
				addedLive = true
			}
			if !addedLive && !seen[name] {
				out = append(out, declaredTeamInstanceRow(name, inst.Agent))
				seen[name] = true
			}
			continue
		}
		if row, ok := rowByName[name]; ok {
			out = append(out, row)
		} else {
			out = append(out, declaredTeamInstanceRow(name, inst.Agent))
		}
		seen[name] = true
	}
	sortPsRows(out, psSortName)
	return out
}

func teamRuntimeRows(top *topology.Topology, team *topology.Team, rows []instanceRow) []instanceRow {
	if team == nil {
		return nil
	}
	instanceNames := stringSliceSet(team.Instances)
	ephemeralAgents := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			ephemeralAgents[inst.Agent] = true
		}
	}
	out := make([]instanceRow, 0, len(rows))
	for _, row := range rows {
		if instanceNames[row.Instance] || ephemeralAgents[row.Agent] {
			out = append(out, row)
		}
	}
	sortPsRows(out, psSortName)
	return out
}

func teamScopedTopology(top *topology.Topology, team *topology.Team) *topology.Topology {
	scoped := &topology.Topology{
		Instances: map[string]*topology.Instance{},
		Pipelines: map[string]*topology.Pipeline{},
		Schedules: map[string]*topology.Schedule{},
		Teams:     map[string]*topology.Team{},
	}
	if top == nil || team == nil {
		return scoped
	}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil {
			scoped.Instances[name] = inst
		}
	}
	for _, name := range team.Pipelines {
		if pipeline := top.Pipelines[name]; pipeline != nil {
			scoped.Pipelines[name] = pipeline
		}
	}
	for _, name := range team.Schedules {
		if schedule := top.Schedules[name]; schedule != nil {
			scoped.Schedules[name] = schedule
		}
	}
	scoped.Teams[team.Name] = team
	return scoped
}

func declaredTeamInstanceRow(name, agent string) instanceRow {
	return instanceRow{
		Instance: name,
		Agent:    agent,
		Phase:    "—",
		Age:      "—",
	}
}

func teamJobs(top *topology.Topology, team *topology.Team, jobs []*job.Job) []*job.Job {
	if team == nil {
		return nil
	}
	pipelines := stringSliceSet(team.Pipelines)
	targets := stringSliceSet(team.Instances)
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil {
			targets[inst.Agent] = true
		}
	}
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if pipelines[j.Pipeline] || targets[j.Target] {
			out = append(out, j)
		}
	}
	return out
}

func jobIDSet(jobs []*job.Job) map[string]bool {
	out := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		if j != nil {
			out[j.ID] = true
		}
	}
	return out
}

func filterJobTriageItemsByJobIDs(items []jobTriageItem, ids map[string]bool) []jobTriageItem {
	if len(ids) == 0 {
		return nil
	}
	out := make([]jobTriageItem, 0, len(items))
	for _, item := range items {
		if ids[item.JobID] {
			out = append(out, item)
		}
	}
	return out
}

func filterJobReadyRowsByJobIDs(rows []jobReadyRow, ids map[string]bool) []jobReadyRow {
	if len(ids) == 0 {
		return nil
	}
	out := make([]jobReadyRow, 0, len(rows))
	for _, row := range rows {
		if ids[row.JobID] {
			out = append(out, row)
		}
	}
	return out
}

func filterJobStatusPreviewsByJobIDs(previews []jobStatusReconcileResult, ids map[string]bool) []jobStatusReconcileResult {
	if len(ids) == 0 {
		return nil
	}
	out := make([]jobStatusReconcileResult, 0, len(previews))
	for _, preview := range previews {
		if ids[preview.JobID] {
			out = append(out, preview)
		}
	}
	return out
}

func queueItemsForJobs(items []*daemon.QueueItem, jobs []*job.Job) []*daemon.QueueItem {
	if len(items) == 0 || len(jobs) == 0 {
		return nil
	}
	out := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		for _, j := range jobs {
			if queueItemMatchesJob(item, j) {
				out = append(out, item)
				break
			}
		}
	}
	return out
}

func teamQueueActions(jobs []*job.Job, items []*daemon.QueueItem) []string {
	ids := map[string]bool{}
	for _, item := range items {
		if item == nil || item.State != daemon.QueueStateDead {
			continue
		}
		for _, j := range jobs {
			if queueItemMatchesJob(item, j) {
				ids[j.ID] = true
			}
		}
	}
	if len(ids) == 1 {
		for id := range ids {
			return []string{fmt.Sprintf("agent-team queue retry --all --job %s", id)}
		}
	}
	return []string{"agent-team queue retry --all"}
}

func teamPipelineStatus(team *topology.Team, rows []pipelineStatusRow) []pipelineStatusRow {
	if team == nil || len(team.Pipelines) == 0 {
		return nil
	}
	pipelines := stringSliceSet(team.Pipelines)
	out := make([]pipelineStatusRow, 0, len(rows))
	for _, row := range rows {
		if pipelines[row.Pipeline] {
			out = append(out, row)
		}
	}
	return out
}

func teamSchedules(team *topology.Team, schedules []scheduleInfo) []scheduleInfo {
	if team == nil || len(team.Schedules) == 0 {
		return nil
	}
	names := stringSliceSet(team.Schedules)
	out := make([]scheduleInfo, 0, len(schedules))
	for _, schedule := range schedules {
		if names[schedule.Name] {
			out = append(out, schedule)
		}
	}
	return out
}

func teamStatusActions(top *topology.Topology, team *topology.Team, snapshot *teamStatusSnapshot) []string {
	if top == nil || team == nil || snapshot == nil {
		return nil
	}
	actions := []string{}
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" {
			return
		}
		for _, existing := range actions {
			if existing == action {
				return
			}
		}
		actions = append(actions, action)
	}
	rowsByName := map[string]psJSONRow{}
	for _, row := range snapshot.Instances {
		rowsByName[row.Instance] = row
	}
	var missingPersistent []string
	for _, name := range team.Instances {
		inst := top.Instances[name]
		if inst == nil || inst.Ephemeral {
			continue
		}
		if rowsByName[name].Status != "running" {
			missingPersistent = append(missingPersistent, name)
		}
	}
	if len(missingPersistent) > 0 {
		sort.Strings(missingPersistent)
		add("agent-team start " + strings.Join(missingPersistent, " "))
	}
	for _, row := range snapshot.PipelineStatus {
		add("agent-team pipeline status " + row.Pipeline)
		for _, action := range row.Actions {
			add(action)
		}
	}
	return actions
}

func stringSliceSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			out[item] = true
		}
	}
	return out
}

func renderTeamList(w io.Writer, teams []teamInfo, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(teams)
	}
	if len(teams) == 0 {
		fmt.Fprintln(w, "(no teams declared)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TEAM\tINSTANCES\tPIPELINES\tSCHEDULES\tDESCRIPTION")
	for _, team := range teams {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n",
			team.Name,
			len(team.Instances),
			len(team.Pipelines),
			len(team.Schedules),
			emptyDash(team.Description),
		)
	}
	return tw.Flush()
}

func renderTeamDetail(w io.Writer, team teamInfo, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(team)
	}
	fmt.Fprintf(w, "Team:        %s\n", team.Name)
	fmt.Fprintf(w, "Description: %s\n", emptyDash(team.Description))
	fmt.Fprintf(w, "Instances:   %s\n", emptyDash(strings.Join(team.Instances, ", ")))
	fmt.Fprintf(w, "Pipelines:   %s\n", emptyDash(strings.Join(team.Pipelines, ", ")))
	fmt.Fprintf(w, "Schedules:   %s\n", emptyDash(strings.Join(team.Schedules, ", ")))
	return nil
}

func renderTeamStatus(w io.Writer, snapshot *teamStatusSnapshot, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
	if snapshot.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
	}
	fmt.Fprintf(w, "instances: total=%d running=%d stopped=%d exited=%d crashed=%d unknown=%d stale=%d\n",
		snapshot.InstanceSummary.Total,
		snapshot.InstanceSummary.Running,
		snapshot.InstanceSummary.Stopped,
		snapshot.InstanceSummary.Exited,
		snapshot.InstanceSummary.Crashed,
		snapshot.InstanceSummary.Unknown,
		snapshot.InstanceSummary.Stale,
	)
	renderJobSummary(w, snapshot.JobSummary)
	if snapshot.PipelineStatus != nil {
		fmt.Fprintf(w, "pipeline status: pipelines=%d jobs=%d ready_steps=%d failed_steps=%d\n",
			len(snapshot.PipelineStatus),
			countPipelineStatusJobs(snapshot.PipelineStatus),
			countPipelineStatusReadySteps(snapshot.PipelineStatus),
			countPipelineStatusFailedSteps(snapshot.PipelineStatus),
		)
	}
	if len(snapshot.Schedules) > 0 {
		fmt.Fprintf(w, "schedules: %d\n", len(snapshot.Schedules))
	}
	if len(snapshot.Actions) == 0 {
		return nil
	}
	fmt.Fprintln(w, "Actions:")
	for _, action := range snapshot.Actions {
		fmt.Fprintf(w, "  %s\n", action)
	}
	return nil
}

func renderTeamHealth(w io.Writer, snapshot *teamHealthSnapshot, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
	if snapshot.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
	}
	renderHealth(w, snapshot.Health)
	return nil
}

func renderTeamPs(w io.Writer, rows []instanceRow, jsonOut bool) error {
	return renderTeamPsWithClear(w, rows, jsonOut, false)
}

func renderTeamPsWithClear(w io.Writer, rows []instanceRow, jsonOut bool, clear bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(psJSONRows(rows))
	}
	if err := writeWatchClear(w, clear); err != nil {
		return err
	}
	return renderPsTable(w, rows)
}

func renderTeamJobs(w io.Writer, jobs []*job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(jobs)
	}
	if tmpl != nil {
		for _, j := range jobs {
			if err := renderJobTemplate(w, j, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobTable(w, jobs)
	return nil
}

func renderTeamSchedules(w io.Writer, schedules []scheduleInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(schedules)
	}
	if tmpl != nil {
		for _, schedule := range schedules {
			if err := tmpl.Execute(w, schedule); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	return renderScheduleList(w, schedules, false)
}
