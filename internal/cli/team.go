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
	cmd.AddCommand(newTeamDoctorCmd())
	cmd.AddCommand(newTeamOverviewCmd())
	cmd.AddCommand(newTeamRunCmd())
	cmd.AddCommand(newTeamUpCmd())
	cmd.AddCommand(newTeamDownCmd())
	cmd.AddCommand(newTeamRestartCmd())
	cmd.AddCommand(newTeamSyncCmd())
	cmd.AddCommand(newTeamPlanCmd())
	cmd.AddCommand(newTeamPsCmd())
	cmd.AddCommand(newTeamJobsCmd())
	cmd.AddCommand(newTeamReadyCmd())
	cmd.AddCommand(newTeamTriageCmd())
	cmd.AddCommand(newTeamCleanupCmd())
	cmd.AddCommand(newTeamAdvanceCmd())
	cmd.AddCommand(newTeamQueueCmd())
	cmd.AddCommand(newTeamLogsCmd())
	cmd.AddCommand(newTeamEventsCmd())
	cmd.AddCommand(newTeamSendCmd())
	cmd.AddCommand(newTeamWaitCmd())
	cmd.AddCommand(newTeamPruneCmd())
	cmd.AddCommand(newTeamStatsCmd())
	cmd.AddCommand(newTeamSnapshotCmd())
	cmd.AddCommand(newTeamTickCmd())
	cmd.AddCommand(newTeamDrainCmd())
	cmd.AddCommand(newTeamRepairCmd())
	cmd.AddCommand(newTeamPipelinesCmd())
	cmd.AddCommand(newTeamSchedulesCmd())
	cmd.AddCommand(newTeamHealthCmd())
	cmd.AddCommand(newTeamMonitorCmd())
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

func newTeamDoctorCmd() *cobra.Command {
	var (
		repo    string
		all     bool
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor <team>|--all",
		Short: "Validate one team's topology wiring.",
		Long: "Validate a declared team's topology wiring: team-owned pipeline workflows must be runnable, " +
			"pipeline step targets must be owned by the team, and team schedules should route back to team-owned instances or pipelines.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team doctor: --all cannot be combined with a team argument.")
				return exitErr(2)
			}
			if !all && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team doctor: pass a team name or --all.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if all {
				result, err := collectAllTeamDoctor(teamDir)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team doctor: %v\n", err)
					return exitErr(1)
				}
				return renderAllTeamDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut)
			}
			result, err := collectTeamDoctor(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team doctor: %v\n", err)
				return exitErr(1)
			}
			return renderTeamDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&all, "all", false, "Validate all declared teams.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team doctor findings as JSON.")
	return cmd
}

type teamDoctorResult struct {
	Team     teamInfo            `json:"team"`
	OK       bool                `json:"ok"`
	Problems []teamDoctorFinding `json:"problems,omitempty"`
	Warnings []teamDoctorFinding `json:"warnings,omitempty"`
}

type allTeamDoctorResult struct {
	OK       bool                `json:"ok"`
	Teams    []teamDoctorResult  `json:"teams"`
	Problems []teamDoctorFinding `json:"problems,omitempty"`
	Warnings []teamDoctorFinding `json:"warnings,omitempty"`
}

type teamDoctorFinding struct {
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	Team         string   `json:"team,omitempty"`
	Pipeline     string   `json:"pipeline,omitempty"`
	Step         string   `json:"step,omitempty"`
	Target       string   `json:"target,omitempty"`
	Routes       []string `json:"routes,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Cycle        []string `json:"cycle,omitempty"`
	Schedule     string   `json:"schedule,omitempty"`
}

func collectTeamDoctor(teamDir, name string) (*teamDoctorResult, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	return doctorTeam(top, team), nil
}

func collectAllTeamDoctor(teamDir string) (*allTeamDoctorResult, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	result := &allTeamDoctorResult{OK: true}
	if top == nil || len(top.Teams) == 0 {
		result.Warnings = append(result.Warnings, teamDoctorFinding{
			Code:    "no_teams",
			Message: "no teams are declared",
		})
		return result, nil
	}
	for _, team := range top.SortedTeams() {
		if team == nil {
			continue
		}
		teamResult := doctorTeam(top, team)
		result.Teams = append(result.Teams, *teamResult)
		result.Problems = append(result.Problems, teamResult.Problems...)
		result.Warnings = append(result.Warnings, teamResult.Warnings...)
	}
	result.OK = len(result.Problems) == 0
	return result, nil
}

func doctorTeam(top *topology.Topology, team *topology.Team) *teamDoctorResult {
	result := &teamDoctorResult{Team: teamInfoFromTopology(team)}
	if top == nil || team == nil {
		result.OK = false
		result.Problems = append(result.Problems, teamDoctorFinding{
			Code:    "team_missing",
			Message: "team declaration is missing",
		})
		return result
	}
	teamName := team.Name
	targets := teamTargetSet(top, team)
	if len(team.Instances) == 0 {
		result.Warnings = append(result.Warnings, teamDoctorFinding{
			Code:    "no_instances",
			Team:    teamName,
			Message: fmt.Sprintf("team %q declares no instances", team.Name),
		})
	}
	if len(team.Pipelines) == 0 {
		result.Warnings = append(result.Warnings, teamDoctorFinding{
			Code:    "no_pipelines",
			Team:    teamName,
			Message: fmt.Sprintf("team %q declares no pipelines; team run is unavailable", team.Name),
		})
	}
	for _, name := range team.Pipelines {
		pipeline := top.Pipelines[name]
		if pipeline == nil {
			continue
		}
		pipelineReport := doctorPipeline(top, pipeline)
		for _, problem := range pipelineReport.Problems {
			result.Problems = append(result.Problems, teamDoctorFindingFromPipeline(teamName, problem))
		}
		for _, warning := range pipelineReport.Warnings {
			result.Warnings = append(result.Warnings, teamDoctorFindingFromPipeline(teamName, warning))
		}
		if len(pipeline.Steps) == 0 {
			continue
		}
		for _, step := range pipeline.Steps {
			if step == nil {
				continue
			}
			target := strings.TrimSpace(step.Target)
			if target == "" || targets[target] {
				continue
			}
			result.Problems = append(result.Problems, teamDoctorFinding{
				Code:     "pipeline_target_outside_team",
				Team:     teamName,
				Message:  fmt.Sprintf("pipeline %q step %q targets %q, which is not owned by team %q", pipeline.Name, step.ID, target, team.Name),
				Pipeline: pipeline.Name,
				Step:     step.ID,
				Target:   target,
			})
		}
	}
	for _, name := range team.Schedules {
		schedule := top.Schedules[name]
		if schedule == nil {
			continue
		}
		teamRoutes, outsideRoutes := teamScheduleRoutes(top, team, schedule)
		if outsideRoutes > 0 {
			result.Warnings = append(result.Warnings, teamDoctorFinding{
				Code:     "schedule_routes_outside_team",
				Team:     teamName,
				Message:  fmt.Sprintf("schedule %q also matches %d pipeline or instance route(s) outside team %q", schedule.Name, outsideRoutes, team.Name),
				Schedule: schedule.Name,
			})
		} else if teamRoutes == 0 {
			result.Warnings = append(result.Warnings, teamDoctorFinding{
				Code:     "schedule_no_team_route",
				Team:     teamName,
				Message:  fmt.Sprintf("schedule %q does not currently match any team-owned pipeline or instance", schedule.Name),
				Schedule: schedule.Name,
			})
		}
	}
	result.OK = len(result.Problems) == 0
	return result
}

func teamDoctorFindingFromPipeline(teamName string, finding pipelineDoctorFinding) teamDoctorFinding {
	out := teamDoctorFinding{
		Code:         finding.Code,
		Team:         teamName,
		Message:      finding.Message,
		Pipeline:     finding.Pipeline,
		Step:         finding.Step,
		Target:       finding.Target,
		Routes:       append([]string(nil), finding.Routes...),
		Dependencies: append([]string(nil), finding.Dependencies...),
		Cycle:        append([]string(nil), finding.Cycle...),
	}
	return out
}

func teamTargetSet(top *topology.Topology, team *topology.Team) map[string]bool {
	targets := map[string]bool{}
	if top == nil || team == nil {
		return targets
	}
	for _, name := range team.Instances {
		targets[name] = true
		if inst := top.Instances[name]; inst != nil && strings.TrimSpace(inst.Agent) != "" {
			targets[inst.Agent] = true
		}
	}
	return targets
}

func teamScheduleRoutes(top *topology.Topology, team *topology.Team, schedule *topology.Schedule) (teamRoutes, outsideRoutes int) {
	if top == nil || team == nil || schedule == nil {
		return 0, 0
	}
	payload := schedule.EventPayload()
	teamInstances := stringSliceSet(team.Instances)
	teamPipelines := stringSliceSet(team.Pipelines)
	for _, inst := range top.Resolve(topology.EventSchedule, payload) {
		if inst == nil {
			continue
		}
		if teamInstances[inst.Name] {
			teamRoutes++
		} else {
			outsideRoutes++
		}
	}
	for _, pipeline := range top.ResolvePipelines(topology.EventSchedule, payload) {
		if pipeline == nil {
			continue
		}
		if teamPipelines[pipeline.Name] {
			teamRoutes++
		} else {
			outsideRoutes++
		}
	}
	return teamRoutes, outsideRoutes
}

func renderTeamDoctor(stdout, stderr io.Writer, result *teamDoctorResult, jsonOut bool) error {
	if result == nil {
		result = &teamDoctorResult{}
	}
	if jsonOut {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	if result.OK {
		fmt.Fprintf(stdout, "agent-team team doctor: OK (%s)\n", result.Team.Name)
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
		}
		return nil
	}
	fmt.Fprintf(stderr, "agent-team team doctor: problems found for %s:\n", result.Team.Name)
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "  - %s\n", problem.Message)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
	}
	return exitErr(1)
}

func renderAllTeamDoctor(stdout, stderr io.Writer, result *allTeamDoctorResult, jsonOut bool) error {
	if result == nil {
		result = &allTeamDoctorResult{OK: true}
	}
	if jsonOut {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	if result.OK {
		fmt.Fprintf(stdout, "agent-team team doctor: OK (%d teams)\n", len(result.Teams))
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s%s\n", teamDoctorFindingPrefix(warning), warning.Message)
		}
		return nil
	}
	fmt.Fprintln(stderr, "agent-team team doctor: problems found:")
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "  - %s%s\n", teamDoctorFindingPrefix(problem), problem.Message)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "  warning: %s%s\n", teamDoctorFindingPrefix(warning), warning.Message)
	}
	return exitErr(1)
}

func teamDoctorFindingPrefix(finding teamDoctorFinding) string {
	if strings.TrimSpace(finding.Team) == "" {
		return ""
	}
	return fmt.Sprintf("team %q: ", finding.Team)
}

func newTeamRunCmd() *cobra.Command {
	var (
		repo        string
		pipeline    string
		id          string
		ticketURL   string
		kickoff     string
		kickoffFile string
		dispatchNow bool
		workspace   string
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "run <team> <ticket> [kickoff...]",
		Short: "Create a durable job through a team's pipeline.",
		Long: "Create a durable job using one of the team's declared pipelines. " +
			"If the team declares exactly one pipeline, it is selected automatically; otherwise pass --pipeline.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			_, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team run: %v\n", err)
				return exitErr(1)
			}
			pipelineName, err := selectTeamRunPipeline(team, pipeline)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team run: %v\n", err)
				return exitErr(2)
			}
			return runPipelineJobCreate(cmd, teamDir, pipelineName, args[1], args[2:], pipelineRunOptions{
				ID:          id,
				TicketURL:   ticketURL,
				Kickoff:     kickoff,
				KickoffFile: kickoffFile,
				DispatchNow: dispatchNow,
				Workspace:   workspace,
				DryRun:      dryRun,
				JSON:        jsonOut,
				Format:      format,
				ErrPrefix:   "agent-team team run",
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Team pipeline to use when the team declares more than one.")
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Canonical ticket URL to store on the job.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the first pipeline step.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the first ready pipeline step immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the pipeline job that would be created without writing it.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the created job or advance result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the created or advanced job with a Go template, e.g. '{{.ID}} {{.Pipeline}}'.")
	return cmd
}

func selectTeamRunPipeline(team *topology.Team, override string) (string, error) {
	if team == nil {
		return "", fmt.Errorf("team is required")
	}
	override = strings.TrimSpace(override)
	if override != "" {
		if stringSliceSet(team.Pipelines)[override] {
			return override, nil
		}
		return "", fmt.Errorf("pipeline %q is not declared on team %q", override, team.Name)
	}
	switch len(team.Pipelines) {
	case 0:
		return "", fmt.Errorf("team %q has no declared pipelines", team.Name)
	case 1:
		return team.Pipelines[0], nil
	default:
		return "", fmt.Errorf("team %q has multiple pipelines; choose one with --pipeline", team.Name)
	}
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

func newTeamReadyCmd() *cobra.Command {
	var (
		repo    string
		states  []string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ready <team>",
		Short: "List ready pipeline jobs owned by one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team ready: --format cannot be combined with --json.")
				return exitErr(2)
			}
			stateFilter, err := parseJobNextStateFilter(states, !cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ready: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobReadyFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ready: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			rows, err := collectTeamReadyRows(teamDir, args[0], stateFilter)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ready: %v\n", err)
				return exitErr(1)
			}
			return renderTeamReadyRows(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team ready rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.")
	return cmd
}

func newTeamTriageCmd() *cobra.Command {
	var (
		repo        string
		staleAfter  time.Duration
		minSeverity string
		reasons     []string
		watch       bool
		noClear     bool
		interval    time.Duration
		jsonOut     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "triage <team>",
		Short: "Show team-owned jobs that need operator attention.",
		Long: "Show a compact team-scoped work queue triage view from durable jobs, " +
			"persisted daemon queue items, status-file update previews, and ready pipeline steps.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if staleAfter < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team triage: --stale-after must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team triage: --interval must be >= 0.")
				return exitErr(2)
			}
			filters, err := parseJobTriageFilters(minSeverity, reasons)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team triage: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runTeamTriageWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], staleAfter, filters, jsonOut, interval, !noClear)
			}
			snapshot, err := collectTeamTriage(teamDir, args[0], time.Now().UTC(), staleAfter, filters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team triage: %v\n", err)
				return exitErr(1)
			}
			return renderJobTriage(cmd.OutOrStdout(), snapshot, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().DurationVar(&staleAfter, "stale-after", defaultJobTriageStaleAfter, "Flag queued or running jobs with no update after this duration (0 disables stale checks).")
	cmd.Flags().StringVar(&minSeverity, "min-severity", "", "Only show attention rows at least this severe: critical, warning, or info.")
	cmd.Flags().StringSliceVar(&reasons, "reason", nil, "Only show attention rows with this reason. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the team triage view until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team triage snapshot as JSON.")
	return cmd
}

func newTeamCleanupCmd() *cobra.Command {
	var (
		repo        string
		merged      bool
		forceBranch bool
		dryRun      bool
		jsonOut     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cleanup <team>",
		Short: "Clean up done jobs owned by one team.",
		Long:  "Preview or remove job-owned worktrees and branches for done jobs owned by one declared team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !merged && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team cleanup: pass --merged after confirming the team's PRs have merged.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			top, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team cleanup: %v\n", err)
				return exitErr(1)
			}
			jobs, err := job.List(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team cleanup: %v\n", err)
				return exitErr(1)
			}
			result := runJobCleanupJobs(teamDir, filepath.Dir(teamDir), teamJobs(top, team, jobs), dryRun, merged, forceBranch)
			result.Team = team.Name
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return err
				}
			} else {
				renderJobCleanupBatch(cmd.OutOrStdout(), result)
			}
			if result.Failed > 0 {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&merged, "merged", false, "Confirm the team's matching PRs have merged before removing worktrees and branches.")
	cmd.Flags().BoolVar(&forceBranch, "force-branch", false, "With --merged, delete job branches with git branch -D if they are not locally merged.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team-owned job cleanup without removing anything.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the cleanup batch as JSON.")
	return cmd
}

func newTeamAdvanceCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		limit         int
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "advance <team>",
		Short: "Dispatch ready pipeline steps owned by one team.",
		Long:  "Dispatch or preview ready next steps for jobs in one team's declared pipelines.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team advance: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team advance: --limit must be >= 0.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team advance: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineAdvanceFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team advance: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			_, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team advance: %v\n", err)
				return exitErr(1)
			}
			results, err := advanceTeamReadyPipelineJobs(cmd, teamDir, team, workspace, limit, dryRun, previewRoutes)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team advance: daemon is not running — start it with `agent-team start`, or use --dry-run.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team advance: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineAdvanceResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready team jobs; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview ready steps without dispatching them.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include local topology route and dispatch payload previews.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit advance results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newTeamQueueCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		eventTypes  []string
		jobs        []string
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
		Use:   "queue <team>",
		Short: "List or control queue items scoped to one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue: --interval must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, jobs, readyOnly, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runTeamQueueSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runTeamQueueListWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runTeamQueueSummary(cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut)
			}
			return runTeamQueueList(cmd.OutOrStdout(), teamDir, args[0], filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only show pending queue items whose next retry is due now.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the team queue table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate queue counts instead of queue rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team queue rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.AddCommand(newTeamQueueRetryCmd())
	cmd.AddCommand(newTeamQueueDropCmd())
	return cmd
}

func newTeamQueueRetryCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		retryAll    bool
		dryRun      bool
		stateFilter string
		eventTypes  []string
		jobs        []string
		readyOnly   bool
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <team> [id]",
		Short: "Retry team-owned queue items.",
		Long:  "Retry one team-owned queue item by id, or retry a filtered team-owned batch with --all. Batch retries default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if retryAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue retry: --all requires exactly one team and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue retry: --limit must be >= 0.")
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFilters(effectiveState, nil, eventTypes, jobs, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue retry: %v\n", err)
					return exitErr(2)
				}
				return runTeamQueueRetryAll(cmd.OutOrStdout(), teamDir, args[0], filters, limit, dryRun, jsonOut)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue retry: requires <team> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || len(jobs) > 0 || readyOnly || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue retry: --state, --event-type, --job, --ready, and --limit require --all.")
				return exitErr(2)
			}
			teamName, id := args[0], args[1]
			item, err := readTeamQueueItem(cmd, teamDir, teamName, id, "retry")
			if err != nil {
				return err
			}
			if dryRun {
				return renderQueueRetryResults(cmd.OutOrStdout(), []queueRetryResult{{
					ID:         item.ID,
					State:      item.State,
					Instance:   item.Instance,
					InstanceID: item.InstanceID,
					Action:     "would_retry",
					DryRun:     true,
				}}, jsonOut)
			}
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
			if err := daemon.ResetQueueItemForRetry(daemon.DaemonRoot(teamDir), item); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(item)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Team queue item %s marked pending; start the daemon to dispatch it.\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching team-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching team-owned queue items without retrying them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only retry pending queue items whose next retry is due now.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newTeamQueueDropCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		dropAll     bool
		dryRun      bool
		stateFilter string
		eventTypes  []string
		jobs        []string
		readyOnly   bool
		limit       int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <team> [id]",
		Short: "Drop team-owned queue items.",
		Long:  "Drop one team-owned queue item by id, or drop a filtered team-owned batch with --all. Batch drops default to dead-letter items.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue drop: --all requires exactly one team and cannot be combined with an id.")
					return exitErr(2)
				}
				if limit < 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue drop: --limit must be >= 0.")
					return exitErr(2)
				}
				effectiveState := strings.TrimSpace(stateFilter)
				if effectiveState == "" {
					effectiveState = daemon.QueueStateDead
					if readyOnly {
						effectiveState = daemon.QueueStatePending
					}
				}
				filters, err := parseQueueListFilters(effectiveState, nil, eventTypes, jobs, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue drop: %v\n", err)
					return exitErr(2)
				}
				return runTeamQueueDropAll(cmd.OutOrStdout(), teamDir, args[0], filters, limit, dryRun, jsonOut)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue drop: requires <team> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || len(jobs) > 0 || readyOnly || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue drop: --state, --event-type, --job, --ready, and --limit require --all.")
				return exitErr(2)
			}
			teamName, id := args[0], args[1]
			item, err := readTeamQueueItem(cmd, teamDir, teamName, id, "drop")
			if err != nil {
				return err
			}
			if dryRun {
				return renderQueueDropResults(cmd.OutOrStdout(), []queueDropResult{{
					ID:         item.ID,
					State:      item.State,
					Instance:   item.Instance,
					InstanceID: item.InstanceID,
					Action:     "would_drop",
					DryRun:     true,
				}}, jsonOut)
			}
			if dc, err := newDaemonClient(teamDir); err == nil {
				if err := dc.QueueDrop(id); err != nil {
					return err
				}
			} else if errors.Is(err, errDaemonNotRunning) {
				if err := daemon.RemoveQueueItem(daemon.DaemonRoot(teamDir), id); err != nil {
					if os.IsNotExist(err) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue drop: queue item %q not found.\n", id)
						return exitErr(2)
					}
					return err
				}
			} else {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"dropped": true, "id": id, "team": teamName})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Dropped team queue item %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching team-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching team-owned queue items without dropping them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only drop pending queue items whose next retry is due now.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newTeamLogsCmd() *cobra.Command {
	var (
		repo      string
		follow    bool
		latest    bool
		last      int
		list      bool
		jsonOut   bool
		noPrefix  bool
		statuses  []string
		phases    []string
		staleOnly bool
		unhealthy bool
		tail      string
		since     string
		grep      string
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "logs <team>",
		Short: "Show daemon-captured logs for one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last must be >= 0.")
				return exitErr(2)
			}
			if jsonOut && !list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --json requires --list.")
				return exitErr(2)
			}
			if format != "" && !list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --format requires --list.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if list && (follow || cmd.Flags().Changed("tail")) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --list cannot be combined with --follow or --tail.")
				return exitErr(2)
			}
			formatTemplate, err := parseLogListFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			sinceCutoff, err := parseLogSince(since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			grepPattern, err := parseLogGrep(grep)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			if sinceCutoff != nil && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --since cannot be combined with --follow because captured logs are not timestamped.")
				return exitErr(2)
			}
			if grepPattern != nil && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --grep cannot be combined with --follow.")
				return exitErr(2)
			}
			if grepPattern != nil && list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --grep cannot be combined with --list.")
				return exitErr(2)
			}
			listOpts, err := newLogListOptionsWithUnhealthy(statuses, nil, phases, staleOnly, unhealthy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			opts := logsOptions{
				Follow:    follow,
				Latest:    latest,
				Limit:     last,
				List:      list,
				JSON:      jsonOut,
				NoPrefix:  noPrefix,
				Tail:      tailLines,
				TailSet:   cmd.Flags().Changed("tail"),
				Since:     sinceCutoff,
				Grep:      grepPattern,
				Format:    formatTemplate,
				Unhealthy: unhealthy,
			}
			return runTeamLogs(cmd, teamDir, args[0], opts, listOpts)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail selected team logs.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show the most recently started team instance log.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show logs for the N most recently started team instances (0 = all).")
	cmd.Flags().BoolVar(&list, "list", false, "List team log streams instead of printing log content.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON with --list.")
	cmd.Flags().BoolVar(&noPrefix, "no-prefix", false, "Do not prefix lines when streaming multiple team logs.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Only show logs for work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show logs for team instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthy, "unhealthy", false, "Only show logs for crashed or stale team instances.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	cmd.Flags().StringVar(&format, "format", "", "With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.")
	return cmd
}

func newTeamEventsCmd() *cobra.Command {
	var (
		repo          string
		follow        bool
		tail          int
		jsonOut       bool
		summary       bool
		format        string
		actionFilters []string
		statusFilters []string
		sinceRaw      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "events <team>",
		Short: "Show lifecycle events scoped to one team.",
		Long:  "Show or follow daemon lifecycle events for one declared team, including ephemeral children owned by that team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team events: --tail must be >= 0.")
				return exitErr(2)
			}
			if summary && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team events: --summary cannot be combined with --follow.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team events: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseEventFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team events: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			filters, err := teamEventFilters(teamDir, args[0], actionFilters, statusFilters, sinceRaw, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team events: %v\n", err)
				return exitErr(2)
			}
			var client eventsClient
			if dc, err := newDaemonClient(teamDir); err == nil {
				client = dc
			} else if errors.Is(err, errDaemonNotRunning) {
				client = localEventsClient{daemonRoot: daemon.DaemonRoot(teamDir)}
			} else {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return runEvents(ctx, cmd.OutOrStdout(), client, eventsOptions{Follow: follow, Tail: tail, JSON: jsonOut, Summary: summary, Format: formatTemplate, Filters: filters})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep streaming new lifecycle events.")
	cmd.Flags().IntVar(&tail, "tail", 0, "Show only the last N matching team events before returning or following (0 = all).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit raw JSONL events.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching team events by action, status, agent, and instance.")
	cmd.Flags().StringVar(&format, "format", "", "Render each event with a Go template, e.g. '{{.Action}} {{.Instance}} {{.Status}}'.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only show events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show events with this lifecycle status. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&sinceRaw, "since", "", "Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	return cmd
}

func newTeamSendCmd() *cobra.Command {
	var (
		repo          string
		from          string
		allStatuses   bool
		latest        bool
		last          int
		statusFilters []string
		phaseFilters  []string
		staleOnly     bool
		unhealthyOnly bool
		dryRun        bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "send <team> <message...>",
		Short: "Send a mailbox message to team-owned instances.",
		Long: "Send a mailbox message to running daemon-known instances owned by one declared team. " +
			"Use --all to include every lifecycle status, or combine selectors such as --status, --phase, --latest, --last, --stale, and --unhealthy.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team send: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team send: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team send: choose one of --latest or --last.")
				return exitErr(2)
			}
			formatTemplate, err := parseSendFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team send: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			top, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team send: %v\n", err)
				return exitErr(1)
			}
			baseClient, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			effectiveStatuses := append([]string(nil), statusFilters...)
			if !allStatuses && len(effectiveStatuses) == 0 && !staleOnly && !unhealthyOnly {
				effectiveStatuses = []string{string(daemon.StatusRunning)}
			}
			opts := sendOptions{
				From:            from,
				All:             true,
				Latest:          latest,
				Limit:           last,
				StatusFilters:   effectiveStatuses,
				PhaseFilters:    phaseFilters,
				Stale:           staleOnly,
				Unhealthy:       unhealthyOnly,
				StaleByInstance: staleInstanceSet(teamDir, time.Now()),
				DryRun:          dryRun,
				JSON:            jsonOut,
				Format:          formatTemplate,
			}
			if len(phaseFilters) > 0 {
				opts.PhaseByInstance = sendPhaseByInstance(teamDir, time.Now())
			}
			body := strings.Join(args[1:], " ")
			client := teamSendClient{sendClient: baseClient, top: top, team: team}
			return runSendSelectionWithClient(cmd.OutOrStdout(), cmd.ErrOrStderr(), client, body, opts)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().BoolVar(&allStatuses, "all", false, "Send to every daemon-known team instance regardless of lifecycle status.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Send to the most recently started team-owned daemon-known instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Send to the N most recently started team-owned daemon-known instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Send to team-owned instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Send to team-owned instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Send to team-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Send to team-owned instances that are crashed or stale.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching recipients without appending mailbox messages.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each send result with a Go template, e.g. '{{.To}} {{.ID}}'.")
	return cmd
}

func newTeamWaitCmd() *cobra.Command {
	var (
		repo          string
		latest        bool
		last          int
		statusFilters []string
		phaseFilters  []string
		staleOnly     bool
		unhealthyOnly bool
		untilPhases   []string
		untilRaw      string
		timeout       time.Duration
		interval      time.Duration
		dryRun        bool
		failOnCrash   bool
		jsonOut       bool
		quiet         bool
		summary       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "wait <team> [<instance>...]",
		Short: "Wait for team-owned instances to reach a lifecycle condition.",
		Long: "Wait until each selected team-owned instance reaches a lifecycle condition. " +
			"With no instance names or selectors, this waits for declared persistent team members and live team-owned ephemeral children to be running. " +
			"Use --until to wait for terminal, stopped, exited, crashed, removed, or running. " +
			"Use --until-phase to wait for a reported work phase such as idle, blocked, or done; when combined with --until, both conditions must match.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := append([]string(nil), args[1:]...)
			if latest && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --latest cannot be combined with instance names.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last > 0 && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --last cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(statusFilters) > 0 && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --status cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(phaseFilters) > 0 && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --phase cannot be combined with instance names.")
				return exitErr(2)
			}
			if staleOnly && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --stale cannot be combined with instance names.")
				return exitErr(2)
			}
			if unhealthyOnly && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --unhealthy cannot be combined with instance names.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --timeout must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --interval must be >= 0.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseWaitFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: %v\n", err)
				return exitErr(2)
			}
			until, err := parseWaitUntil(untilRaw)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecycleStatusFilterSet(statusFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: %v\n", err)
				return exitErr(2)
			}
			if _, err := lifecyclePhaseFilterSet(phaseFilters); len(phaseFilters) > 0 && err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: %v\n", err)
				return exitErr(2)
			}
			untilPhaseSet := map[string]bool(nil)
			if len(untilPhases) > 0 {
				untilPhaseSet, err = lifecyclePhaseFilterSetForFlag("--until-phase", untilPhases)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: %v\n", err)
					return exitErr(2)
				}
				if !cmd.Flags().Changed("until") {
					until = waitUntilAny
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			top, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: %v\n", err)
				return exitErr(1)
			}
			var base instanceLister
			base, err = newDaemonClient(teamDir)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					base = localInstanceLister{daemonRoot: daemon.DaemonRoot(teamDir)}
					err = nil
				} else {
					return err
				}
			}
			lister := teamWaitLister{instanceLister: base, top: top, team: team}
			var phaseByInstance map[string]string
			var staleInstances map[string]bool
			if len(phaseFilters) > 0 || staleOnly || unhealthyOnly {
				now := time.Now()
				if len(phaseFilters) > 0 {
					phaseByInstance = waitPhaseByInstance(teamDir, now)
				}
				if staleOnly || unhealthyOnly {
					staleInstances = staleInstanceSet(teamDir, now)
				}
			}
			if latest {
				names, err = waitLatestInstanceNamesWithPhasesStaleAndUnhealthy(lister, nil, statusFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, unhealthyOnly)
			} else if last > 0 {
				names, err = waitLatestInstanceNamesLimitWithPhasesStaleAndUnhealthy(lister, nil, statusFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, unhealthyOnly, last)
			} else if len(statusFilters) > 0 || len(phaseFilters) > 0 || staleOnly || unhealthyOnly {
				names, err = waitFilteredInstanceNamesWithPhasesStaleAndUnhealthy(lister, nil, statusFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, unhealthyOnly)
			} else if len(names) == 0 {
				names, err = waitAllInstanceNames(lister)
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: %v\n", err)
				return exitErr(2)
			}
			if len(names) == 0 {
				if summary {
					body := waitSummaryResult{Summary: summarizeWaitResults(nil, waitConditionString(until, untilPhaseSet))}
					if jsonOut {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(body)
					}
					renderWaitSummary(cmd.OutOrStdout(), body.Summary)
					return nil
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode([]waitResult{})
				}
				if !quiet && formatTemplate == nil {
					fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
				}
				return nil
			}
			var phaseSource waitPhaseSource
			if len(untilPhaseSet) > 0 || len(phaseFilters) > 0 || staleOnly || unhealthyOnly || summary || dryRun {
				phaseSource = func() map[string]string {
					return waitPhaseByInstance(teamDir, time.Now())
				}
			}
			if dryRun {
				results, err := waitSnapshotForInstances(lister, phaseSource, names)
				if err != nil {
					var unknownErr *waitUnknownError
					if errors.As(err, &unknownErr) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: instance %q is not known to team %q.\n", unknownErr.Instance, team.Name)
						return exitErr(2)
					}
					return err
				}
				if err := renderWaitCommandResults(cmd, results, summary, jsonOut, quiet, formatTemplate, waitConditionString(until, untilPhaseSet), len(untilPhaseSet) > 0); err != nil {
					return err
				}
				if failOnCrash && waitResultsHaveStatus(results, string(daemon.StatusCrashed)) {
					return exitErr(1)
				}
				return nil
			}
			ctx := cmd.Context()
			cancel := func() {}
			if timeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, timeout)
			}
			defer cancel()
			results, err := waitForInstancesUntilWithPhases(ctx, lister, phaseSource, names, interval, until, untilPhaseSet)
			if err != nil {
				var timeoutErr *waitTimeoutError
				if errors.As(err, &timeoutErr) {
					if !quiet {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: timed out waiting for %s: %s\n", waitConditionString(until, untilPhaseSet), strings.Join(timeoutErr.PendingNames(), ", "))
					}
					return exitErr(1)
				}
				var unknownErr *waitUnknownError
				if errors.As(err, &unknownErr) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team wait: instance %q is not known to team %q.\n", unknownErr.Instance, team.Name)
					return exitErr(2)
				}
				return err
			}
			if err := renderWaitCommandResults(cmd, results, summary, jsonOut, quiet, formatTemplate, waitConditionString(until, untilPhaseSet), len(untilPhaseSet) > 0); err != nil {
				return err
			}
			if failOnCrash && waitResultsHaveStatus(results, string(daemon.StatusCrashed)) {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Wait for the most recently started team-owned instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Wait for the N most recently started team-owned instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Wait for team-owned instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Wait for team-owned instances currently in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Wait for team-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Wait for team-owned instances that are crashed or stale.")
	cmd.Flags().StringVar(&untilRaw, "until", string(waitUntilRunning), "Lifecycle condition to wait for: running, terminal, stopped, exited, crashed, or removed.")
	cmd.Flags().StringSliceVar(&untilPhases, "until-phase", nil, "Work phase condition to wait for: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 500*time.Millisecond, "Polling interval.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview selected team instances and current state without waiting.")
	cmd.Flags().BoolVar(&failOnCrash, "fail-on-crash", false, "Exit 1 if any selected instance resolves to crashed.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate final status and phase counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each wait result with a Go template, e.g. '{{.Instance}} {{.Status}} {{.Phase}}'.")
	return cmd
}

func newTeamPruneCmd() *cobra.Command {
	var (
		repo          string
		statusFilters []string
		phaseFilters  []string
		staleOnly     bool
		unhealthyOnly bool
		dryRun        bool
		olderThan     time.Duration
		quiet         bool
		jsonOut       bool
		summary       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune <team>",
		Short: "Remove finished team-owned instances.",
		Long: "Remove daemon-known exited or crashed instances owned by one declared team. " +
			"Running and stopped instances are intentionally left alone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team prune: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team prune: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			olderThanSet := cmd.Flags().Changed("older-than")
			if olderThanSet && olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			if err := validatePruneStatusFilters(statusFilters); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team prune: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseRmFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			names, err := collectTeamPruneTargets(teamDir, args[0], teamPruneTargetOptions{
				StatusFilters: statusFilters,
				PhaseFilters:  phaseFilters,
				Stale:         staleOnly,
				Unhealthy:     unhealthyOnly,
				OlderThan:     olderThan,
				OlderThanSet:  olderThanSet,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team prune: %v\n", err)
				return exitErr(1)
			}
			if len(names) == 0 {
				return renderTeamPruneNoTargets(cmd.OutOrStdout(), dryRun, quiet, jsonOut, summary, formatTemplate)
			}
			return runInstanceRmWithOptions(cmd, repo, names, instanceRmOptions{
				Force:   true,
				DryRun:  dryRun,
				Quiet:   quiet,
				JSON:    jsonOut,
				Summary: summary,
				Format:  formatTemplate,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only remove finished team-owned instances in this lifecycle status: exited or crashed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only remove finished team-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only remove finished team-owned instances whose non-idle work phase has stale status telemetry.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only remove finished team-owned instances that are crashed or stale.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview finished team-owned instances that would be pruned without deleting state or daemon metadata.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune finished team-owned instances whose terminal timestamp is older than this duration (for example 24h).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate removal counts instead of per-instance rows.")
	cmd.Flags().StringVar(&format, "format", "", "Render each removal result with a Go template, e.g. '{{.Instance}} {{.Path}}'.")
	return cmd
}

func newTeamStatsCmd() *cobra.Command {
	var (
		repo          string
		all           bool
		latest        bool
		last          int
		watch         bool
		jsonOut       bool
		summary       bool
		noClear       bool
		format        string
		sortBy        string
		interval      time.Duration
		statusFilters []string
		phaseFilters  []string
		staleOnly     bool
		unhealthyOnly bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "stats <team> [<instance>...]",
		Short: "Show CPU and memory usage for team-owned instances.",
		Long: "Show a one-shot or watchable resource snapshot for instances owned by one declared team. " +
			"With no names, only running team-owned instances are shown. Use --all to include stopped, exited, crashed, and missing persistent team members.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := append([]string(nil), args[1:]...)
			if all && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: --all cannot be combined with instance names.")
				return exitErr(2)
			}
			if latest && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: --latest cannot be combined with instance names.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: choose one of --latest or --last.")
				return exitErr(2)
			}
			if last > 0 && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: --last cannot be combined with instance names.")
				return exitErr(2)
			}
			if len(names) > 0 && (len(statusFilters) > 0 || len(phaseFilters) > 0 || staleOnly || unhealthyOnly) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: --status, --phase, --stale, and --unhealthy cannot be combined with instance names.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: --interval must be >= 0.")
				return exitErr(2)
			}
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parseStatsFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team stats: %v\n", err)
				return exitErr(2)
			}
			opts, err := newStatsOptionsWithInstancesPhasesAndUnhealthy(all, statusFilters, nil, phaseFilters, nil, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team stats: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parseStatsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team stats: %v\n", err)
				return exitErr(2)
			}
			opts.Sort = sortMode
			opts.SortSet = cmd.Flags().Changed("sort")
			opts.Latest = latest
			opts.Limit = last
			opts.Stale = staleOnly
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			top, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team stats: %v\n", err)
				return exitErr(1)
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
			lister := teamWaitLister{instanceLister: base, top: top, team: team}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				clear := !noClear && !jsonOut
				var watchErr error
				if summary {
					watchErr = runStatsSummaryWatchWithClear(ctx, cmd.OutOrStdout(), lister, names, opts, interval, time.Now, readProcessStats, jsonOut, clear)
				} else if formatTemplate != nil {
					watchErr = runStatsFormatWatch(ctx, cmd.OutOrStdout(), lister, names, opts, interval, time.Now, readProcessStats, formatTemplate)
				} else {
					watchErr = runStatsWatchWithClear(ctx, cmd.OutOrStdout(), lister, names, opts, interval, time.Now, readProcessStats, jsonOut, clear)
				}
				if watchErr != nil {
					var unknownErr *statsUnknownError
					if errors.As(watchErr, &unknownErr) {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team stats: instance %q is not known to team %q.\n", unknownErr.Instance, team.Name)
						return exitErr(2)
					}
				}
				return watchErr
			}
			var renderErr error
			switch {
			case summary && jsonOut:
				renderErr = runStatsSummaryJSON(cmd.OutOrStdout(), lister, names, opts, time.Now(), readProcessStats)
			case summary:
				renderErr = runStatsSummary(cmd.OutOrStdout(), lister, names, opts, time.Now(), readProcessStats)
			case jsonOut:
				renderErr = runStatsJSON(cmd.OutOrStdout(), lister, names, opts, time.Now(), readProcessStats)
			case formatTemplate != nil:
				renderErr = runStatsFormat(cmd.OutOrStdout(), lister, names, opts, time.Now(), readProcessStats, formatTemplate)
			default:
				renderErr = runStats(cmd.OutOrStdout(), lister, names, opts, time.Now(), readProcessStats)
			}
			if renderErr != nil {
				var unknownErr *statsUnknownError
				if errors.As(renderErr, &unknownErr) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team stats: instance %q is not known to team %q.\n", unknownErr.Instance, team.Name)
					return exitErr(2)
				}
				return renderErr
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include stopped, exited, crashed, and missing persistent team-owned instances.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show stats for the most recently started team-owned instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show stats for the N most recently started team-owned instances after other filters (0 = all).")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team stats until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON array per refresh.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate CPU, memory, and RSS totals instead of team instance rows.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.Instance}} {{.CPUPercent}} {{.RSS}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort rows by name, cpu, mem, rss, status, agent, phase, stale, or unhealthy.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show team-owned lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show team-owned instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show team-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed or stale team-owned instances.")
	return cmd
}

func newTeamSnapshotCmd() *cobra.Command {
	var (
		repo          string
		output        string
		jsonOut       bool
		noRedact      bool
		eventLimit    int
		scheduleLimit int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "snapshot <team>",
		Short: "Capture a team-scoped diagnostic report.",
		Long: "Capture a read-only diagnostic report scoped to one declared team. " +
			"It includes team health, plan, instances, jobs, job status preview, queue, schedule, runtime, and lifecycle event state.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if eventLimit < -1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team snapshot: --events must be >= -1.")
				return exitErr(2)
			}
			if scheduleLimit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team snapshot: --schedule-limit must be >= 0.")
				return exitErr(2)
			}
			if jsonOut && output != "" && output != "-" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team snapshot: choose one of --json or --output.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			repoRoot, err := filepath.Abs(repo)
			if err != nil {
				return err
			}
			snapshot, err := collectTeamSnapshot(teamDir, repoRoot, args[0], snapshotOptions{
				EventLimit:    eventLimit,
				ScheduleLimit: scheduleLimit,
				Redact:        !noRedact,
				Now:           time.Now().UTC(),
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team snapshot: %v\n", err)
				return exitErr(1)
			}
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
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the full JSON snapshot to this file. Use '-' for stdout.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the full snapshot JSON to stdout.")
	cmd.Flags().BoolVar(&noRedact, "no-redact", false, "Include raw payload values instead of redacting sensitive keys.")
	cmd.Flags().IntVar(&eventLimit, "events", 50, "Recent matching team lifecycle events to include. Use -1 for all matching events or 0 to skip events.")
	cmd.Flags().IntVar(&scheduleLimit, "schedule-limit", 10, "Upcoming team schedules to include after ordering; 0 means all.")
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

func newTeamTickCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		limit         int
		skipSchedules bool
		skipDrain     bool
		skipAdvance   bool
		dryRun        bool
		previewRoutes bool
		watch         bool
		untilIdle     bool
		jsonOut       bool
		format        string
		interval      time.Duration
		maxCycles     int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "tick <team>",
		Short: "Run one team's orchestration maintenance work.",
		Long:  "Run or preview one team's due schedules, drainable queue items, and ready pipeline steps.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: --interval must be >= 0.")
				return exitErr(2)
			}
			if watch && untilIdle {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: choose one of --watch or --until-idle.")
				return exitErr(2)
			}
			if untilIdle && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: --until-idle cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			if maxCycles <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: --max-cycles must be > 0.")
				return exitErr(2)
			}
			if cmd.Flags().Changed("max-cycles") && !untilIdle {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: --max-cycles requires --until-idle.")
				return exitErr(2)
			}
			tmpl, err := parseTeamTickFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team tick: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			opts := tickOptions{
				SkipSchedules: skipSchedules,
				SkipDrain:     skipDrain,
				SkipAdvance:   skipAdvance,
				DryRun:        dryRun,
				PreviewRoutes: previewRoutes,
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if err := runTeamTickLoop(ctx, cmd, teamDir, args[0], workspace, limit, opts, jsonOut, tmpl, interval); err != nil {
					if errors.Is(err, errDaemonNotRunning) {
						fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: daemon is not running — start it with `agent-team start`, or use --dry-run.")
						return exitErr(2)
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team tick: %v\n", err)
					return exitErr(1)
				}
				return nil
			}
			if untilIdle {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				result, err := runTeamTickUntilIdle(ctx, cmd, teamDir, args[0], workspace, limit, opts, maxCycles, interval)
				if err != nil {
					if errors.Is(err, errDaemonNotRunning) {
						fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: daemon is not running — start it with `agent-team start`, or use --dry-run.")
						return exitErr(2)
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team tick: %v\n", err)
					return exitErr(1)
				}
				return renderTeamTickUntilIdleResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
			}
			result, err := runTeamTick(cmd, teamDir, args[0], workspace, limit, opts)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team tick: daemon is not running — start it with `agent-team start`, or use --dry-run.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team tick: %v\n", err)
				return exitErr(1)
			}
			return renderTeamTickResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs; 0 means no limit.")
	cmd.Flags().BoolVar(&skipSchedules, "skip-schedules", false, "Skip due schedule work.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip queue drain work.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement work.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team-owned maintenance work without mutating state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include route and dispatch payload previews for ready pipeline steps.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Run the team tick repeatedly until interrupted.")
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Run team tick cycles until no immediate team schedule, queue, or pipeline work remains.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team tick result with a Go template, e.g. '{{.Team.Name}} {{.Tick.Queue.WouldDispatch}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch, or delay between --until-idle cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "With --until-idle, stop after this many cycles if work keeps appearing.")
	return cmd
}

func newTeamDrainCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		limit         int
		skipSchedules bool
		skipDrain     bool
		skipAdvance   bool
		jsonOut       bool
		format        string
		interval      time.Duration
		maxCycles     int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drain <team>",
		Short: "Run one team's maintenance loop until idle.",
		Long:  "Run scoped team ticks until no immediate team schedule, queue, or pipeline work remains.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team drain: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team drain: --limit must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team drain: --interval must be >= 0.")
				return exitErr(2)
			}
			if maxCycles <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team drain: --max-cycles must be > 0.")
				return exitErr(2)
			}
			tmpl, err := parseTeamTickFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team drain: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			result, err := runTeamTickUntilIdle(ctx, cmd, teamDir, args[0], workspace, limit, tickOptions{
				SkipSchedules: skipSchedules,
				SkipDrain:     skipDrain,
				SkipAdvance:   skipAdvance,
			}, maxCycles, interval)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team drain: daemon is not running — start it with `agent-team start`, or use `agent-team team tick --dry-run` to preview.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team drain: %v\n", err)
				return exitErr(1)
			}
			return renderTeamTickUntilIdleResult(cmd.OutOrStdout(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs per cycle; 0 means no limit.")
	cmd.Flags().BoolVar(&skipSchedules, "skip-schedules", false, "Skip due schedule work.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip queue drain work.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement work.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drain result with a Go template, e.g. '{{.Team.Name}} {{.CyclesRun}} {{.Idle}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Delay between drain cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "Stop after this many cycles if work keeps appearing.")
	return cmd
}

func newTeamRepairCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		limit         int
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		skipDaemon    bool
		skipQueue     bool
		skipTick      bool
		includeJobs   bool
		untilIdle     bool
		readyTimeout  time.Duration
		interval      time.Duration
		maxCycles     int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "repair <team>",
		Short: "Recover unhealthy orchestration state for one team.",
		Long: "Recover unhealthy orchestration state scoped to one team: ensure the daemon is ready, retry team-owned dead-letter queue items, " +
			"and run a scoped team tick. Use --dry-run to preview.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --limit must be >= 0.")
				return exitErr(2)
			}
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --interval must be >= 0.")
				return exitErr(2)
			}
			if maxCycles <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --max-cycles must be > 0.")
				return exitErr(2)
			}
			if cmd.Flags().Changed("max-cycles") && !untilIdle {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --max-cycles requires --until-idle.")
				return exitErr(2)
			}
			if untilIdle && dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --until-idle cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if untilIdle && skipTick {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --until-idle cannot be combined with --skip-tick.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --preview-routes requires --dry-run.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := runTeamRepair(cmd, repo, teamDir, args[0], teamRepairOptions{
				Workspace:     workspace,
				Limit:         limit,
				DryRun:        dryRun,
				PreviewRoutes: previewRoutes,
				SkipDaemon:    skipDaemon,
				SkipQueue:     skipQueue,
				SkipTick:      skipTick,
				IncludeJobs:   includeJobs,
				UntilIdle:     untilIdle,
				ReadyTimeout:  readyTimeout,
				Interval:      interval,
				MaxCycles:     maxCycles,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team repair: %v\n", err)
				return exitErr(1)
			}
			return renderTeamRepairResult(cmd.OutOrStdout(), result, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for pipeline steps during the scoped team tick: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Retry at most this many team dead-letter queue items and advance at most this many ready team pipeline jobs; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team repair actions without mutating state or starting the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include route and dispatch payload previews for ready team pipeline steps.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVar(&skipDaemon, "skip-daemon", false, "Do not start or reconcile the daemon.")
	cmd.Flags().BoolVar(&skipQueue, "skip-queue", false, "Do not retry team-owned dead-letter queue items.")
	cmd.Flags().BoolVar(&skipTick, "skip-tick", false, "Do not run a scoped team tick after queue retry.")
	cmd.Flags().BoolVar(&includeJobs, "jobs", false, "Include team-owned durable job and pipeline health.")
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Run scoped team ticks until no immediate team queue, schedule, or pipeline work remains.")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Delay between --until-idle scoped team tick cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "With --until-idle, stop after this many cycles if work keeps appearing.")
	return cmd
}

func newTeamUpCmd() *cobra.Command {
	var (
		repo         string
		prompt       string
		wait         bool
		timeout      time.Duration
		readyTimeout time.Duration
		dryRun       bool
		summary      bool
		attach       bool
		tail         string
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "up <team>",
		Aliases: []string{"start"},
		Short:   "Start or resume a team's declared persistent instances.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tailLines, formatTemplate, err := validateTeamUpOptions(cmd, "agent-team team up", teamLifecycleUpOptions{
				Wait:          wait,
				Timeout:       timeout,
				ReadyTimeout:  readyTimeout,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTailSet: cmd.Flags().Changed("tail"),
				Tail:          tail,
				Quiet:         quiet,
				JSON:          jsonOut,
				Format:        format,
			})
			if err != nil {
				return err
			}
			_, names, err := loadTeamPersistentLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team up", err)
			}
			if len(names) == 0 {
				return writeEmptyTeamLifecycleStart(cmd, args[0], "up", dryRun, wait, summary, quiet, jsonOut, formatTemplate)
			}
			if !dryRun {
				if err := ensureDaemonReadyWithTimeout(cmd, repo, jsonOut || quiet, readyTimeout); err != nil {
					return err
				}
			}
			return runInstanceUpWithOptions(cmd, repo, prompt, names, instanceUpOptions{
				Wait:          wait,
				Timeout:       timeout,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTail:    tailLines,
				AttachTailSet: cmd.Flags().Changed("tail"),
				Quiet:         quiet,
				JSON:          jsonOut,
				Format:        formatTemplate,
				Health:        teamLifecycleHealthOptions(names),
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after starting.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned start/resume actions without changing daemon state.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&attach, "attach", false, "Follow the selected instance log after starting or resuming. Requires exactly one selected instance.")
	cmd.Flags().StringVar(&tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamDownCmd() *cobra.Command {
	var (
		repo        string
		force       bool
		wait        bool
		timeout     time.Duration
		waitTimeout time.Duration
		dryRun      bool
		remove      bool
		summary     bool
		quiet       bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "down <team>",
		Aliases: []string{"stop"},
		Short:   "Stop a team's persistent instances and active ephemeral children.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := validateTeamDownOptions(cmd, "agent-team team down", teamLifecycleDownOptions{
				Wait:        wait,
				Timeout:     timeout,
				WaitTimeout: waitTimeout,
				DryRun:      dryRun,
				Summary:     summary,
				Quiet:       quiet,
				JSON:        jsonOut,
				Format:      format,
			})
			if err != nil {
				return err
			}
			_, names, err := loadTeamStopLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team down", err)
			}
			if len(names) == 0 {
				return writeEmptyTeamLifecycleDown(cmd, args[0], "stop", dryRun, summary, quiet, jsonOut, formatTemplate)
			}
			return runInstanceDownWithOptions(cmd, repo, names, instanceDownOptions{
				Force:          force,
				Wait:           wait,
				Timeout:        timeout,
				WaitTimeout:    waitTimeout,
				WaitTimeoutSet: cmd.Flags().Changed("wait-timeout"),
				DryRun:         dryRun,
				Remove:         remove,
				Summary:        summary,
				Quiet:          quiet,
				JSON:           jsonOut,
				Format:         formatTemplate,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if an instance does not stop within --timeout.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for stopped instances to reach a terminal state.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned stop actions without changing daemon state.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after stopping.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamRestartCmd() *cobra.Command {
	var (
		repo         string
		prompt       string
		timeout      time.Duration
		readyTimeout time.Duration
		wait         bool
		waitTimeout  time.Duration
		force        bool
		dryRun       bool
		summary      bool
		attach       bool
		tail         string
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restart <team>",
		Short: "Restart or resume a team's declared persistent instances.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tailLines, formatTemplate, err := validateTeamRestartOptions(cmd, "agent-team team restart", teamLifecycleRestartOptions{
				Timeout:       timeout,
				ReadyTimeout:  readyTimeout,
				Wait:          wait,
				WaitTimeout:   waitTimeout,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTailSet: cmd.Flags().Changed("tail"),
				Tail:          tail,
				Quiet:         quiet,
				JSON:          jsonOut,
				Format:        format,
			})
			if err != nil {
				return err
			}
			_, names, err := loadTeamPersistentLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team restart", err)
			}
			if len(names) == 0 {
				return writeEmptyTeamLifecycleStart(cmd, args[0], "restart", dryRun, wait, summary, quiet, jsonOut, formatTemplate)
			}
			if !dryRun {
				if err := ensureDaemonReadyWithTimeout(cmd, repo, jsonOut || quiet, readyTimeout); err != nil {
					return err
				}
			}
			return runInstanceRestart(cmd, repo, prompt, names, instanceRestartOptions{
				Timeout:       timeout,
				Wait:          wait,
				WaitTimeout:   waitTimeout,
				Force:         force,
				DryRun:        dryRun,
				Summary:       summary,
				Attach:        attach,
				AttachTail:    tailLines,
				AttachTailSet: cmd.Flags().Changed("tail"),
				Quiet:         quiet,
				JSON:          jsonOut,
				Format:        formatTemplate,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt for instances started fresh.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait for each running instance to stop before resuming (0 = daemon default).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after restarting.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for health with --wait (0 = no timeout).")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if a running instance does not stop within --timeout before restarting.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned restart/resume actions without changing daemon state.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&attach, "attach", false, "Follow the selected instance log after restarting or resuming. Requires exactly one selected instance.")
	cmd.Flags().StringVar(&tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamSyncCmd() *cobra.Command {
	var (
		repo         string
		dryRun       bool
		wait         bool
		stopExtras   bool
		timeout      time.Duration
		readyTimeout time.Duration
		summary      bool
		quiet        bool
		jsonOut      bool
		format       string
		actions      []string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "sync <team>",
		Short: "Sync one team's declared persistent instances.",
		Long: "Reload topology, reconcile daemon metadata, then start or resume the selected team's " +
			"declared persistent instances. With --stop-extras, running daemon-known extras for the team's agents are stopped.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team sync: --timeout must be >= 0.")
				return exitErr(2)
			}
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team sync: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if dryRun && wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team sync: --dry-run cannot be combined with --wait.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team sync: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if quiet && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team sync: choose one of --quiet or --summary.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team sync: --format cannot be combined with --quiet, --json, or --summary.")
				return exitErr(2)
			}
			actionFilters, err := planActionFilterSet(actions)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: %v\n", err)
				return exitErr(2)
			}
			return runTeamSync(cmd, repo, args[0], syncOptions{
				DryRun:       dryRun,
				Wait:         wait,
				StopExtras:   stopExtras,
				Timeout:      timeout,
				ReadyTimeout: readyTimeout,
				Summary:      summary,
				Quiet:        quiet,
				JSON:         jsonOut,
				Format:       formatTemplate,
				Actions:      actionFilters,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team topology convergence without starting the daemon or instances.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected team instances to become healthy after syncing.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "Also stop running daemon-known extras for this team's agents.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each sync action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	cmd.Flags().StringSliceVar(&actions, "action", nil, "Only sync plan rows with this action: start, resume, keep, on-demand, stop, or extra. Can repeat or comma-separate.")
	return cmd
}

func newTeamPlanCmd() *cobra.Command {
	var (
		repo          string
		jsonOut       bool
		summary       bool
		stopExtras    bool
		actionFilters []string
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "plan <team>",
		Short: "Preview desired lifecycle state for one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && (jsonOut || summary) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team plan: --format cannot be combined with --json or --summary.")
				return exitErr(2)
			}
			formatTemplate, err := parsePlanFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team plan: %v\n", err)
				return exitErr(2)
			}
			actions, err := planActionFilterSet(actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team plan: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			snapshot, err := collectTeamPlan(teamDir, args[0], stopExtras, actions)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team plan: %v\n", err)
				return exitErr(1)
			}
			if jsonOut {
				if summary {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(lifecycleActionSummaryResult{
						Summary: summarizeLifecycleActions(planRowsToLifecycleActionResults(snapshot.Plan.Instances, true), true),
					})
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot)
			}
			if formatTemplate != nil {
				return renderPlanFormat(cmd.OutOrStdout(), snapshot.Plan.Instances, formatTemplate)
			}
			if summary {
				renderLifecycleActionSummary(cmd.OutOrStdout(), summarizeLifecycleActions(planRowsToLifecycleActionResults(snapshot.Plan.Instances, true), true))
				return nil
			}
			renderTeamPlan(cmd.OutOrStdout(), snapshot)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team plan as JSON.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "Preview running team-agent topology extras as stop actions.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only show plan rows with this action: start, resume, keep, on-demand, stop, or extra. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan row with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
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

func newTeamMonitorCmd() *cobra.Command {
	var (
		repo            string
		all             bool
		watch           bool
		plan            bool
		jobs            bool
		schedules       bool
		stopExtras      bool
		jsonOut         bool
		noClear         bool
		latest          bool
		last            int
		format          string
		sortBy          string
		statsSortBy     string
		staleOnly       bool
		unhealthyOnly   bool
		eventTail       int
		eventSince      string
		interval        time.Duration
		statusFilters   []string
		agentFilters    []string
		phaseFilters    []string
		instanceFilters []string
		actionFilters   []string
		eventActions    []string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "monitor <team>",
		Short: "Show a combined operator snapshot for one team.",
		Long: "Show a Docker-style operator snapshot scoped to one declared team, combining team health, " +
			"instance rows, daemon-managed process stats, and optional plan, job, schedule, and lifecycle event sections.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: --interval must be >= 0.")
				return exitErr(2)
			}
			if eventTail < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: --events must be >= 0.")
				return exitErr(2)
			}
			if last < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: --last must be >= 0.")
				return exitErr(2)
			}
			if latest && last > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: choose one of --latest or --last.")
				return exitErr(2)
			}
			if stopExtras && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: --stop-extras requires --plan.")
				return exitErr(2)
			}
			if strings.TrimSpace(eventSince) != "" && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: --since requires --events.")
				return exitErr(2)
			}
			if len(eventActions) > 0 && eventTail == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: --event-action requires --events.")
				return exitErr(2)
			}
			if len(actionFilters) > 0 && !plan {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: --action requires --plan.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team monitor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseMonitorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team monitor: %v\n", err)
				return exitErr(2)
			}
			opts, err := newMonitorOptionsWithInstancesPhasesStaleAndUnhealthy(all, statusFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team monitor: %v\n", err)
				return exitErr(2)
			}
			sortMode, err := parsePsSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team monitor: %v\n", err)
				return exitErr(2)
			}
			statsSortMode, err := parseStatsSortFlag(statsSortBy, "--stats-sort")
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team monitor: %v\n", err)
				return exitErr(2)
			}
			planActions, err := planActionFilterSet(actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team monitor: %v\n", err)
				return exitErr(2)
			}
			eventFilters, err := newMonitorEventFilters(eventActions, eventSince, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team monitor: %v\n", err)
				return exitErr(2)
			}
			opts.PS.Sort = sortMode
			opts.PS.SortSet = cmd.Flags().Changed("sort")
			opts.Stats.Sort = statsSortMode
			opts.Stats.SortSet = cmd.Flags().Changed("stats-sort")
			opts.PS.Limit = last
			opts.Stats.Limit = last
			if latest {
				opts.PS.Limit = 1
				opts.Stats.Limit = 1
			}
			opts.IncludePlan = plan
			opts.IncludeJobs = jobs
			opts.IncludeSchedules = schedules
			opts.StopExtras = stopExtras
			opts.PlanActions = planActions
			opts.EventTail = eventTail
			opts.EventFilters = eventFilters
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if formatTemplate != nil {
					return runTeamMonitorFormatWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], interval, time.Now, readProcessStats, opts, formatTemplate)
				}
				clear := !noClear && !jsonOut
				return runTeamMonitorWatch(ctx, cmd.OutOrStdout(), teamDir, args[0], interval, time.Now, readProcessStats, jsonOut, opts, clear)
			}
			snapshot, err := collectTeamMonitorSnapshot(teamDir, args[0], time.Now().UTC(), readProcessStats, opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team monitor: %v\n", err)
				return exitErr(1)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot)
			}
			if formatTemplate != nil {
				return renderMonitorFormat(cmd.OutOrStdout(), snapshot, formatTemplate)
			}
			return renderMonitor(cmd.OutOrStdout(), snapshot)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include stopped, exited, crashed, and missing team-owned instances in the stats section.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the team monitor snapshot until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&plan, "plan", false, "Include team-scoped desired-state actions from instances.toml and daemon metadata.")
	cmd.Flags().BoolVar(&jobs, "jobs", false, "Include team-owned durable job summary, attention, ready-step state, and status-file previews.")
	cmd.Flags().BoolVar(&schedules, "schedules", false, "Include due and upcoming team schedules.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "With --plan, preview running team-agent extras as stop actions.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit JSON. With --watch, writes one JSON object per refresh.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show only the most recently started team-owned instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show only the N most recently started team-owned instances after other filters (0 = all).")
	cmd.Flags().StringVar(&format, "format", "", "Render team monitor snapshots with a Go template, e.g. '{{.Team.Name}} {{len .Instances}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "name", "Sort instance rows by name, status, agent, phase, stale, unhealthy, started, stopped, or exited.")
	cmd.Flags().StringVar(&statsSortBy, "stats-sort", "name", "Sort stats rows by name, cpu, mem, rss, status, agent, phase, stale, or unhealthy.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show team-owned instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only show crashed or stale team-owned instances.")
	cmd.Flags().IntVar(&eventTail, "events", 0, "Include the last N matching team lifecycle events in the full monitor (0 = omit).")
	cmd.Flags().StringSliceVar(&eventActions, "event-action", nil, "With --events, only show lifecycle events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&eventSince, "since", "", "With --events, only show lifecycle events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show team-owned lifecycle status in instance, stats, and plan sections: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show team-owned instances, stats, and plan rows for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show team-owned instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show team-owned instances with this name. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "With --plan, only show plan rows with this action: start, resume, keep, on-demand, stop, or extra. Can repeat or comma-separate.")
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
	Queue           queueSummary        `json:"queue"`
	PipelineStatus  []pipelineStatusRow `json:"pipeline_status,omitempty"`
	Schedules       []scheduleInfo      `json:"schedules,omitempty"`
	Actions         []string            `json:"actions,omitempty"`
}

type teamPlanSnapshot struct {
	Team teamInfo    `json:"team"`
	Plan *planResult `json:"plan"`
}

type teamHealthSnapshot struct {
	Team   teamInfo      `json:"team"`
	Health *healthResult `json:"health"`
}

type teamTickResult struct {
	Team      teamInfo   `json:"team"`
	CheckedAt string     `json:"checked_at"`
	Tick      tickResult `json:"tick"`
}

type teamTickUntilIdleResult struct {
	Team      teamInfo          `json:"team"`
	CyclesRun int               `json:"cycles_run"`
	Idle      bool              `json:"idle"`
	HitLimit  bool              `json:"hit_limit,omitempty"`
	Cycles    []*teamTickResult `json:"cycles"`
}

type teamRepairOptions struct {
	Workspace     string
	Limit         int
	DryRun        bool
	PreviewRoutes bool
	SkipDaemon    bool
	SkipQueue     bool
	SkipTick      bool
	IncludeJobs   bool
	UntilIdle     bool
	ReadyTimeout  time.Duration
	Interval      time.Duration
	MaxCycles     int
}

type teamRepairResult struct {
	Team         teamInfo           `json:"team"`
	DryRun       bool               `json:"dry_run,omitempty"`
	HealthBefore *healthResult      `json:"health_before,omitempty"`
	Daemon       repairStepResult   `json:"daemon"`
	Queue        repairQueueStep    `json:"queue"`
	Tick         teamRepairTickStep `json:"tick"`
	HealthAfter  *healthResult      `json:"health_after,omitempty"`
}

type teamRepairTickStep struct {
	Action    string                   `json:"action"`
	Reason    string                   `json:"reason,omitempty"`
	Result    *teamTickResult          `json:"result,omitempty"`
	UntilIdle *teamTickUntilIdleResult `json:"until_idle,omitempty"`
}

type teamPruneTargetOptions struct {
	StatusFilters []string
	PhaseFilters  []string
	Stale         bool
	Unhealthy     bool
	OlderThan     time.Duration
	OlderThanSet  bool
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
	ownedJobs := teamJobs(top, team, jobs)
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	teamQueue := teamQueueItems(top, team, ownedJobs, queueItems)
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
		JobSummary:      summarizeJobs(ownedJobs),
		Queue:           summarizeQueueItems(teamQueue, now.UTC()),
		PipelineStatus:  teamPipelineStatus(team, pipelineStatus),
		Schedules:       teamSchedules(team, schedules),
	}
	snapshot.Actions = teamStatusActions(top, team, snapshot)
	return snapshot, nil
}

func collectTeamPlan(teamDir, name string, stopExtras bool, actions map[string]bool) (*teamPlanSnapshot, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	result, err := collectPlan(teamDir)
	if err != nil {
		return nil, err
	}
	if stopExtras {
		markPlanStopExtras(result)
	}
	result.Instances = teamPlanRows(top, team, result.Instances, stopExtras)
	result.Instances = filterPlanRowsWithActions(result.Instances, psOptions{}, actions)
	result.Summary = summarizePlanRows(result.Instances)
	return &teamPlanSnapshot{
		Team: teamInfoFromTopology(team),
		Plan: result,
	}, nil
}

func runTeamSync(cmd *cobra.Command, repo, name string, opts syncOptions) error {
	teamDir, err := resolveTeamDir(cmd, repo)
	if err != nil {
		return err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: %v\n", err)
		return exitErr(1)
	}
	if opts.DryRun {
		snapshot, err := collectTeamPlan(teamDir, name, opts.StopExtras, opts.Actions)
		if err != nil {
			return err
		}
		return renderTeamSyncDryRun(cmd.OutOrStdout(), snapshot, opts)
	}
	if err := ensureDaemonReadyWithTimeout(cmd, repo, opts.JSON || opts.Quiet, opts.ReadyTimeout); err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team sync: daemon is not running — start it with `agent-team start`.")
			return exitErr(1)
		}
		return err
	}
	if _, err := dc.TopologyReload(); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: reload: %v\n", err)
		return exitErr(1)
	}
	if _, err := dc.Reconcile(); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: reconcile: %v\n", err)
		return exitErr(1)
	}
	if opts.StopExtras {
		return runTeamSyncWithStopExtras(cmd, repo, teamDir, dc, top, team, opts)
	}
	names, err := teamSyncTargetNamesFromCurrentPlan(teamDir, top, team, opts.Actions)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: %v\n", err)
		return exitErr(1)
	}
	if len(names) == 0 {
		return renderSyncNoActions(cmd.OutOrStdout(), opts)
	}
	return runInstanceUpWithOptions(cmd, repo, "", names, instanceUpOptions{
		Wait:    opts.Wait,
		Timeout: opts.Timeout,
		Summary: opts.Summary,
		Quiet:   opts.Quiet,
		JSON:    opts.JSON,
		Format:  opts.Format,
		Health:  teamLifecycleHealthOptions(names),
	})
}

func renderTeamSyncDryRun(w io.Writer, snapshot *teamPlanSnapshot, opts syncOptions) error {
	if snapshot == nil || snapshot.Plan == nil {
		return renderSyncNoActions(w, opts)
	}
	rows := snapshot.Plan.Instances
	if opts.JSON {
		if opts.Summary {
			return json.NewEncoder(w).Encode(lifecycleActionSummaryResult{
				Summary: summarizeLifecycleActions(planRowsToLifecycleActionResults(rows, true), true),
			})
		}
		return json.NewEncoder(w).Encode(snapshot)
	}
	if opts.Quiet {
		return nil
	}
	if opts.Format != nil {
		return renderPlanFormat(w, rows, opts.Format)
	}
	if opts.Summary {
		renderLifecycleActionSummary(w, summarizeLifecycleActions(planRowsToLifecycleActionResults(rows, true), true))
		return nil
	}
	renderTeamPlan(w, snapshot)
	return nil
}

func teamSyncTargetNamesFromCurrentPlan(teamDir string, top *topology.Topology, team *topology.Team, actions map[string]bool) ([]string, error) {
	result, err := collectPlan(teamDir)
	if err != nil {
		return nil, err
	}
	rows := teamPlanRows(top, team, result.Instances, false)
	rows = filterPlanRowsWithActions(rows, psOptions{}, actions)
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		switch row.Action {
		case "start", "resume", "keep":
			if row.Kind == "persistent" {
				names = append(names, row.Instance)
			}
		}
	}
	return names, nil
}

func runTeamSyncWithStopExtras(cmd *cobra.Command, repo, teamDir string, dc *daemonClient, top *topology.Topology, team *topology.Team, opts syncOptions) error {
	metas, err := dc.Instances()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: %v\n", err)
		return exitErr(1)
	}
	out := cmd.OutOrStdout()
	results := teamSyncStopExtraResults(out, dc, top, team, metas, opts)
	metas, err = dc.Instances()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: %v\n", err)
		return exitErr(1)
	}
	names, err := teamSyncTargetNamesFromCurrentPlan(teamDir, top, team, opts.Actions)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: %v\n", err)
		return exitErr(1)
	}
	if len(names) == 0 {
		if len(results) == 0 {
			return renderSyncNoActions(cmd.OutOrStdout(), opts)
		}
		return renderSyncActionResults(cmd, teamDir, dc, results, opts)
	}
	targets, err := selectLifecycleTargets(top, metas, names)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team sync: %v\n", err)
		return exitErr(2)
	}
	for _, lt := range targets {
		if lt.running() {
			result := lifecycleActionResult{
				Action:   "skip",
				Instance: lt.name,
				Agent:    lt.agent,
				Status:   string(daemon.StatusRunning),
				Detail:   "already running",
			}
			if lt.meta != nil {
				result.PID = lt.meta.PID
			}
			results = append(results, result)
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  skip   %-20s already running\n", lt.name)
			}
			continue
		}
		if lt.meta != nil {
			if err := dc.StartInstance(lt.name); err != nil {
				results = append(results, lifecycleActionResult{Action: "error", Instance: lt.name, Agent: lt.agent, Status: "error", Error: err.Error()})
				if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
					fmt.Fprintf(out, "  error  %-20s %v\n", lt.name, err)
				}
				continue
			}
			results = append(results, lifecycleActionResult{Action: "resume", Instance: lt.name, Agent: lt.agent})
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  resume %-20s %s\n", lt.name, lt.agent)
			}
			continue
		}
		kickoff := fmt.Sprintf("Team sync: you are %q, an instance of %q.", lt.name, lt.agent)
		runErr := runMaybeSuppressStdout(cmd, opts.JSON || opts.Quiet || opts.Format != nil || opts.Summary, func() error {
			return upOne(cmd, repo, lt.declared, kickoff)
		})
		if runErr != nil {
			results = append(results, lifecycleActionResult{Action: "error", Instance: lt.name, Agent: lt.agent, Status: "error", Error: runErr.Error()})
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(out, "  error  %-20s %v\n", lt.name, runErr)
			}
			continue
		}
		results = append(results, lifecycleActionResult{Action: "start", Instance: lt.name, Agent: lt.agent})
		if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
			fmt.Fprintf(out, "  start  %-20s %s\n", lt.name, lt.agent)
		}
	}
	return renderSyncActionResults(cmd, teamDir, dc, results, opts)
}

func teamSyncStopExtraResults(w io.Writer, dc *daemonClient, top *topology.Topology, team *topology.Team, metas []*daemon.Metadata, opts syncOptions) []lifecycleActionResult {
	if len(opts.Actions) > 0 && !opts.Actions["stop"] {
		return nil
	}
	agents := teamAgentSet(top, team)
	declared := map[string]bool{}
	if top != nil {
		for _, inst := range top.SortedInstances() {
			declared[inst.Name] = true
		}
	}
	extras := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if meta == nil || meta.Status != daemon.StatusRunning || declared[meta.Instance] {
			continue
		}
		if _, ok := declaredEphemeralOwner(top, meta.Instance, meta.Agent); ok {
			continue
		}
		if !agents[meta.Agent] {
			continue
		}
		extras = append(extras, meta)
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i].Instance < extras[j].Instance })
	results := make([]lifecycleActionResult, 0, len(extras))
	for _, meta := range extras {
		result := lifecycleActionResult{
			Action:   "stop",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   string(daemon.StatusStopped),
			PID:      meta.PID,
			Detail:   "team-agent extra",
		}
		if err := dc.StopInstanceWithOptions(meta.Instance, false, 0); err != nil {
			result.Action = "error"
			result.Status = "error"
			result.Error = err.Error()
			if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
				fmt.Fprintf(w, "  error  %-20s %v\n", meta.Instance, err)
			}
		} else if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
			fmt.Fprintf(w, "  stop   %-20s team-agent extra\n", meta.Instance)
		}
		results = append(results, result)
	}
	return results
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

type teamLifecycleUpOptions struct {
	Wait          bool
	Timeout       time.Duration
	ReadyTimeout  time.Duration
	DryRun        bool
	Summary       bool
	Attach        bool
	AttachTailSet bool
	Tail          string
	Quiet         bool
	JSON          bool
	Format        string
}

type teamLifecycleDownOptions struct {
	Wait        bool
	Timeout     time.Duration
	WaitTimeout time.Duration
	DryRun      bool
	Summary     bool
	Quiet       bool
	JSON        bool
	Format      string
}

type teamLifecycleRestartOptions struct {
	Timeout       time.Duration
	ReadyTimeout  time.Duration
	Wait          bool
	WaitTimeout   time.Duration
	DryRun        bool
	Summary       bool
	Attach        bool
	AttachTailSet bool
	Tail          string
	Quiet         bool
	JSON          bool
	Format        string
}

func loadTeamPersistentLifecycleInstances(cmd *cobra.Command, repo, name string) (string, []string, error) {
	teamDir, err := resolveTeamDir(cmd, repo)
	if err != nil {
		return "", nil, err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return "", nil, err
	}
	return teamDir, teamPersistentLifecycleInstanceNames(top, team), nil
}

func loadTeamStopLifecycleInstances(cmd *cobra.Command, repo, name string) (string, []string, error) {
	teamDir, err := resolveTeamDir(cmd, repo)
	if err != nil {
		return "", nil, err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return "", nil, err
	}
	names := teamPersistentLifecycleInstanceNames(top, team)
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return "", nil, err
	}
	names = append(names, teamEphemeralChildLifecycleInstanceNames(top, team, metas)...)
	return teamDir, names, nil
}

func teamPersistentLifecycleInstanceNames(top *topology.Topology, team *topology.Team) []string {
	if top == nil || team == nil {
		return nil
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(team.Instances))
	for _, name := range team.Instances {
		if seen[name] {
			continue
		}
		inst := top.Instances[name]
		if inst == nil || inst.Ephemeral {
			continue
		}
		names = append(names, name)
		seen[name] = true
	}
	return names
}

func teamEphemeralChildLifecycleInstanceNames(top *topology.Topology, team *topology.Team, metas []*daemon.Metadata) []string {
	if top == nil || team == nil {
		return nil
	}
	owners := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			owners[inst.Name] = true
		}
	}
	if len(owners) == 0 {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, meta := range metas {
		if meta == nil || seen[meta.Instance] {
			continue
		}
		owner, ok := declaredEphemeralOwner(top, meta.Instance, meta.Agent)
		if !ok || !owners[owner.Name] {
			continue
		}
		names = append(names, meta.Instance)
		seen[meta.Instance] = true
	}
	sort.Strings(names)
	return names
}

func reportTeamLifecycleLoadError(cmd *cobra.Command, prefix string, err error) error {
	var code ExitCode
	if errors.As(err, &code) {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
	return exitErr(1)
}

func validateTeamUpOptions(cmd *cobra.Command, prefix string, opts teamLifecycleUpOptions) (int, *template.Template, error) {
	if opts.Timeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--timeout must be >= 0")
	}
	if opts.ReadyTimeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--ready-timeout must be >= 0")
	}
	if opts.DryRun && opts.Wait {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--dry-run cannot be combined with --wait")
	}
	if opts.Attach && opts.JSON {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--attach cannot be combined with --json")
	}
	if opts.Quiet && opts.JSON {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --json")
	}
	if opts.Quiet && opts.Summary {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --summary")
	}
	if opts.Summary && opts.Attach {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--summary cannot be combined with --attach")
	}
	if opts.Format != "" && (opts.Quiet || opts.JSON || opts.Attach || opts.Summary) {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--format cannot be combined with --quiet, --json, --attach, or --summary")
	}
	if opts.Quiet && opts.Attach {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--quiet cannot be combined with --attach")
	}
	if opts.Attach && opts.DryRun {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--attach cannot be combined with --dry-run")
	}
	if opts.Attach && opts.Wait {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --attach or --wait")
	}
	if !opts.Attach && opts.AttachTailSet {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--tail requires --attach")
	}
	tailLines, err := parseLogTail(opts.Tail)
	if err != nil {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	formatTemplate, err := parseLifecycleActionFormat(opts.Format)
	if err != nil {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	return tailLines, formatTemplate, nil
}

func validateTeamDownOptions(cmd *cobra.Command, prefix string, opts teamLifecycleDownOptions) (*template.Template, error) {
	if opts.Timeout < 0 {
		return nil, teamLifecycleUsageError(cmd, prefix, "--timeout must be >= 0")
	}
	if opts.WaitTimeout < 0 {
		return nil, teamLifecycleUsageError(cmd, prefix, "--wait-timeout must be >= 0")
	}
	if opts.DryRun && opts.Wait {
		return nil, teamLifecycleUsageError(cmd, prefix, "--dry-run cannot be combined with --wait")
	}
	if opts.Quiet && opts.JSON {
		return nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --json")
	}
	if opts.Quiet && opts.Summary {
		return nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --summary")
	}
	if opts.Format != "" && (opts.Quiet || opts.JSON || opts.Summary) {
		return nil, teamLifecycleUsageError(cmd, prefix, "--format cannot be combined with --quiet, --json, or --summary")
	}
	formatTemplate, err := parseLifecycleActionFormat(opts.Format)
	if err != nil {
		return nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	return formatTemplate, nil
}

func validateTeamRestartOptions(cmd *cobra.Command, prefix string, opts teamLifecycleRestartOptions) (int, *template.Template, error) {
	if opts.Timeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--timeout must be >= 0")
	}
	if opts.ReadyTimeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--ready-timeout must be >= 0")
	}
	if opts.WaitTimeout < 0 {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--wait-timeout must be >= 0")
	}
	if opts.DryRun && opts.Wait {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--dry-run cannot be combined with --wait")
	}
	if opts.Attach && opts.JSON {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--attach cannot be combined with --json")
	}
	if opts.Quiet && opts.JSON {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --json")
	}
	if opts.Quiet && opts.Summary {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --quiet or --summary")
	}
	if opts.Summary && opts.Attach {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--summary cannot be combined with --attach")
	}
	if opts.Format != "" && (opts.Quiet || opts.JSON || opts.Attach || opts.Summary) {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--format cannot be combined with --quiet, --json, --attach, or --summary")
	}
	if opts.Quiet && opts.Attach {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--quiet cannot be combined with --attach")
	}
	if opts.Attach && opts.DryRun {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--attach cannot be combined with --dry-run")
	}
	if opts.Attach && opts.Wait {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "choose one of --attach or --wait")
	}
	if !opts.Attach && opts.AttachTailSet {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, "--tail requires --attach")
	}
	tailLines, err := parseLogTail(opts.Tail)
	if err != nil {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	formatTemplate, err := parseLifecycleActionFormat(opts.Format)
	if err != nil {
		return 0, nil, teamLifecycleUsageError(cmd, prefix, err.Error())
	}
	return tailLines, formatTemplate, nil
}

func teamLifecycleUsageError(cmd *cobra.Command, prefix, message string) error {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s.\n", prefix, strings.TrimSuffix(message, "."))
	return exitErr(2)
}

func teamLifecycleHealthOptions(names []string) healthOptions {
	instances := map[string]bool{}
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			instances[name] = true
		}
	}
	if len(instances) == 0 {
		return healthOptions{}
	}
	return healthOptions{filters: psOptions{instances: instances}}
}

func writeEmptyTeamLifecycleStart(cmd *cobra.Command, teamName, verb string, dryRun, wait, summary, quiet, jsonOut bool, formatTemplate *template.Template) error {
	out := cmd.OutOrStdout()
	if jsonOut {
		if summary {
			return json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
				Summary: summarizeLifecycleActions(nil, dryRun),
			})
		}
		if wait {
			return json.NewEncoder(out).Encode(lifecycleHealthResult{Actions: []lifecycleActionResult{}})
		}
		return json.NewEncoder(out).Encode([]lifecycleActionResult{})
	}
	if quiet || formatTemplate != nil {
		return nil
	}
	if summary {
		renderLifecycleActionSummary(out, summarizeLifecycleActions(nil, dryRun))
		return nil
	}
	fmt.Fprintf(out, "(no persistent instances to %s for team %q)\n", verb, strings.TrimSpace(teamName))
	return nil
}

func writeEmptyTeamLifecycleDown(cmd *cobra.Command, teamName, verb string, dryRun, summary, quiet, jsonOut bool, formatTemplate *template.Template) error {
	out := cmd.OutOrStdout()
	if jsonOut {
		if summary {
			return json.NewEncoder(out).Encode(lifecycleActionSummaryResult{
				Summary: summarizeInstanceDownActions(nil, dryRun),
			})
		}
		return json.NewEncoder(out).Encode([]instanceDownResult{})
	}
	if quiet || formatTemplate != nil {
		return nil
	}
	if summary {
		renderLifecycleActionSummary(out, summarizeInstanceDownActions(nil, dryRun))
		return nil
	}
	fmt.Fprintf(out, "(nothing to %s for team %q)\n", verb, strings.TrimSpace(teamName))
	return nil
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
	teamQueue := teamQueueItems(top, team, ownedJobs, queueItems)
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
			teamQueueActions(team.Name, ownedJobs, teamQueue),
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

func collectTeamReadyRows(teamDir, name string, states map[string]bool) ([]jobReadyRow, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	owned := teamJobs(top, team, jobs)
	rows := make([]jobReadyRow, 0, len(owned))
	for _, j := range owned {
		if j == nil || len(j.Steps) == 0 {
			continue
		}
		next := inspectNextJobStep(j)
		if len(states) > 0 && !states[next.State] {
			continue
		}
		rows = append(rows, jobReadyRowFromJob(j, next))
	}
	return rows, nil
}

func renderTeamReadyRows(w io.Writer, rows []jobReadyRow, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
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
	renderJobReadyTable(w, rows)
	return nil
}

func collectTeamTriage(teamDir, name string, now time.Time, staleAfter time.Duration, filters jobTriageFilters) (jobTriageSnapshot, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	ownedJobs := teamJobs(top, team, jobs)
	ownedIDs := jobIDSet(ownedJobs)
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	teamQueue := teamQueueItems(top, team, ownedJobs, queueItems)
	snapshot, err := collectJobTriage(teamDir, now, staleAfter)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	snapshot.Summary = summarizeJobs(ownedJobs)
	snapshot.Queue = summarizeQueueItems(teamQueue, now)
	snapshot.Attention = filterJobTriageItemsByJobIDs(snapshot.Attention, ownedIDs)
	snapshot.ReadySteps = filterJobReadyRowsByJobIDs(snapshot.ReadySteps, ownedIDs)
	snapshot.StatusPreviews = filterJobStatusPreviewsByJobIDs(snapshot.StatusPreviews, ownedIDs)
	return filterJobTriageSnapshot(snapshot, filters), nil
}

func runTeamTriageWatch(ctx context.Context, w io.Writer, teamDir, name string, staleAfter time.Duration, filters jobTriageFilters, jsonOut bool, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectTeamTriage(teamDir, name, time.Now().UTC(), staleAfter, filters)
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
			renderJobTriage(w, snapshot, false)
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

func collectTeamQueueItems(teamDir, name string, filters queueListFilters, now time.Time) ([]*daemon.QueueItem, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	owned := teamQueueItems(top, team, teamJobs(top, team, jobs), items)
	return filterQueueItems(owned, filters.withNow(now)), nil
}

func readTeamQueueItem(cmd *cobra.Command, teamDir, name, id, verb string) (*daemon.QueueItem, error) {
	item, err := daemon.ReadQueueItem(daemon.DaemonRoot(teamDir), id)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue %s: queue item %q not found.\n", verb, id)
			return nil, exitErr(2)
		}
		return nil, err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue %s: %v\n", verb, err)
		return nil, exitErr(1)
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	if len(teamQueueItems(top, team, teamJobs(top, team, jobs), []*daemon.QueueItem{item})) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue %s: queue item %q is not owned by team %q.\n", verb, id, name)
		return nil, exitErr(2)
	}
	return item, nil
}

func runTeamQueueDropAll(w io.Writer, teamDir, name string, filters queueListFilters, limit int, dryRun, jsonOut bool) error {
	matches, err := collectTeamQueueItems(teamDir, name, filters, time.Now().UTC())
	if err != nil {
		return err
	}
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
	results := make([]queueDropResult, 0, len(matches))
	for _, item := range matches {
		result := queueDropResult{
			ID:         item.ID,
			State:      item.State,
			Instance:   item.Instance,
			InstanceID: item.InstanceID,
		}
		if dryRun {
			result.Action = "would_drop"
			result.DryRun = true
		} else {
			if dc != nil {
				if err := dc.QueueDrop(item.ID); err != nil {
					return err
				}
			} else if err := daemon.RemoveQueueItem(daemon.DaemonRoot(teamDir), item.ID); err != nil {
				return err
			}
			result.Action = "dropped"
		}
		results = append(results, result)
	}
	return renderQueueDropResults(w, results, jsonOut)
}

func runTeamQueueRetryAll(w io.Writer, teamDir, name string, filters queueListFilters, limit int, dryRun, jsonOut bool) error {
	results, err := teamQueueRetryResults(teamDir, name, filters, limit, dryRun)
	if err != nil {
		return err
	}
	return renderQueueRetryResults(w, results, jsonOut)
}

func teamQueueRetryResults(teamDir, name string, filters queueListFilters, limit int, dryRun bool) ([]queueRetryResult, error) {
	matches, err := collectTeamQueueItems(teamDir, name, filters, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
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
				return nil, err
			}
			result.Action = outcome.Action
			result.Instance = outcome.Instance
			result.InstanceID = outcome.InstanceID
			result.Reason = outcome.Reason
		default:
			if err := daemon.ResetQueueItemForRetry(daemon.DaemonRoot(teamDir), item); err != nil {
				return nil, err
			}
			result.Action = "reset"
		}
		results = append(results, result)
	}
	return results, nil
}

func runTeamQueueList(w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool, tmpl *template.Template) error {
	items, err := collectTeamQueueItems(teamDir, name, filters, time.Now().UTC())
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(items)
	}
	if tmpl != nil {
		return renderQueueItemsFormat(w, items, tmpl)
	}
	renderQueueTable(w, items)
	return nil
}

func runTeamQueueSummary(w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool) error {
	now := time.Now().UTC()
	items, err := collectTeamQueueItems(teamDir, name, filters, now)
	if err != nil {
		return err
	}
	summary := summarizeQueueItems(items, now)
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	renderQueueSummary(w, summary)
	return nil
}

func runTeamQueueListWatch(ctx context.Context, w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
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
		if err := runTeamQueueList(w, teamDir, name, filters, jsonOut, tmpl); err != nil {
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

func runTeamQueueSummaryWatch(ctx context.Context, w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool, interval time.Duration, clear bool) error {
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
		if err := runTeamQueueSummary(w, teamDir, name, filters, jsonOut); err != nil {
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

func runTeamLogs(cmd *cobra.Command, teamDir, name string, opts logsOptions, listOpts logListOptions) error {
	rows, err := collectTeamLogRows(teamDir, name, listOpts, opts.Since, opts.Limit)
	if err != nil {
		return err
	}
	if opts.Latest {
		rows = latestLogListRowsLimit(rows, 1)
	}
	if opts.List {
		if opts.JSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
		}
		if opts.Format != nil {
			return renderLogListFormat(cmd.OutOrStdout(), rows, opts.Format)
		}
		renderLogList(cmd.OutOrStdout(), rows)
		return nil
	}
	if len(rows) == 0 {
		if opts.Since != nil || opts.Grep != nil {
			fmt.Fprintln(cmd.OutOrStdout(), "(no matching logs)")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
		return nil
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()
	if len(rows) == 1 {
		if opts.Follow {
			if err := streamLocalLog(ctx, cmd.OutOrStdout(), rows[0].path, true, opts.Tail, nil); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: log not found at %s.\n", rows[0].LogPath)
					return exitErr(1)
				}
				return err
			}
			return nil
		}
		if err := streamLogRowOnce(ctx, cmd.OutOrStdout(), rows[0], opts); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: log not found at %s.\n", rows[0].LogPath)
				return exitErr(1)
			}
			return err
		}
		return nil
	}
	if opts.Follow {
		return streamLocalLogRowsFollow(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix)
	}
	return streamLocalLogRowsOnce(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Grep)
}

func collectTeamLogRows(teamDir, name string, opts logListOptions, since *time.Time, limit int) ([]logListRow, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectLocalLogListRows(teamDir)
	if err != nil {
		return nil, err
	}
	rows = teamLogRows(top, team, rows)
	rows = filterLogListRows(rows, opts)
	rows = filterLogListRowsSince(rows, since)
	rows = latestLogListRowsLimit(rows, limit)
	if rows == nil {
		return []logListRow{}, nil
	}
	return rows, nil
}

func teamEventFilters(teamDir, name string, actionFilters, statusFilters []string, sinceRaw string, now func() time.Time) (eventFilters, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return eventFilters{}, err
	}
	filters, err := newEventFilters(actionFilters, nil, nil, statusFilters, sinceRaw, now)
	if err != nil {
		return eventFilters{}, err
	}
	instances := map[string]bool{}
	prefixes := map[string]bool{}
	for _, name := range team.Instances {
		inst := top.Instances[name]
		if inst == nil {
			continue
		}
		instances[inst.Name] = true
		if inst.Ephemeral {
			prefixes[inst.Name+"-"] = true
		}
	}
	if len(instances) == 0 && len(prefixes) == 0 {
		instances[""] = false
	}
	filters.instances = instances
	filters.instancePrefixes = prefixes
	return filters, nil
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

func parseTeamTickFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("team-tick-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func runTeamTick(cmd *cobra.Command, teamDir, name, workspace string, limit int, opts tickOptions) (*teamTickResult, error) {
	now := time.Now().UTC()
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	result := &teamTickResult{
		Team:      teamInfoFromTopology(team),
		CheckedAt: now.Format(time.RFC3339),
		Tick: tickResult{
			DryRun: opts.DryRun,
		},
	}
	var dc *daemonClient
	daemonClient := func() (*daemonClient, error) {
		if dc != nil {
			return dc, nil
		}
		client, err := newDaemonClient(teamDir)
		if err != nil {
			return nil, err
		}
		dc = client
		return dc, nil
	}
	if !opts.SkipSchedules {
		if opts.DryRun {
			schedule, err := previewTeamScheduleFire(teamDir, team, now)
			if err != nil {
				return nil, err
			}
			result.Tick.Schedule = schedule
		} else if len(team.Schedules) == 0 {
			result.Tick.Schedule = &daemon.ScheduleFireResult{Schedules: []daemon.ScheduleFireItem{}}
		} else {
			client, err := daemonClient()
			if err != nil {
				return nil, err
			}
			schedule, err := client.ScheduleFireScoped(false, team.Schedules)
			if err != nil {
				return nil, err
			}
			result.Tick.Schedule = schedule
		}
	}
	if !opts.SkipDrain {
		items, err := collectTeamQueueItemsForTopology(teamDir, top, team)
		if err != nil {
			return nil, err
		}
		if opts.DryRun {
			result.Tick.Queue = previewQueueDrainItems(top, items, now)
		} else {
			ids := queueItemIDsFromPointers(items)
			if len(ids) == 0 {
				result.Tick.Queue = &daemon.QueueDrainResult{Outcomes: []daemon.EventOutcome{}}
			} else {
				client, err := daemonClient()
				if err != nil {
					return nil, err
				}
				queue, err := client.QueueDrainScoped(false, ids)
				if err != nil {
					return nil, err
				}
				result.Tick.Queue = queue
			}
		}
	}
	if !opts.SkipAdvance {
		advanced, err := advanceTeamReadyPipelineJobs(cmd, teamDir, team, workspace, limit, opts.DryRun, opts.PreviewRoutes)
		if err != nil {
			return nil, err
		}
		result.Tick.Advance = advanced
	}
	return result, nil
}

func runTeamTickLoop(ctx context.Context, cmd *cobra.Command, teamDir, name, workspace string, limit int, opts tickOptions, jsonOut bool, tmpl *template.Template, interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	first := true
	for {
		result, err := runTeamTick(cmd, teamDir, name, workspace, limit, opts)
		if err != nil {
			return err
		}
		if !first && !jsonOut {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		if err := renderTeamTickResult(cmd.OutOrStdout(), result, jsonOut, tmpl); err != nil {
			return err
		}
		first = false
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func runTeamTickUntilIdle(ctx context.Context, cmd *cobra.Command, teamDir, name, workspace string, limit int, opts tickOptions, maxCycles int, interval time.Duration) (*teamTickUntilIdleResult, error) {
	if maxCycles <= 0 {
		maxCycles = 1
	}
	result := &teamTickUntilIdleResult{Cycles: []*teamTickResult{}}
	for cycle := 0; cycle < maxCycles; cycle++ {
		if cycle > 0 && interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				result.CyclesRun = len(result.Cycles)
				return result, nil
			case <-timer.C:
			}
		}
		tick, err := runTeamTick(cmd, teamDir, name, workspace, limit, opts)
		if err != nil {
			result.CyclesRun = len(result.Cycles)
			return result, err
		}
		if result.Team.Name == "" {
			result.Team = tick.Team
		}
		result.Cycles = append(result.Cycles, tick)
		if teamTickResultIsIdle(tick) {
			result.Idle = true
			break
		}
	}
	result.CyclesRun = len(result.Cycles)
	result.HitLimit = !result.Idle && result.CyclesRun >= maxCycles
	return result, nil
}

func teamTickResultIsIdle(result *teamTickResult) bool {
	if result == nil {
		return true
	}
	return tickResultIsIdle(&result.Tick)
}

func runTeamRepair(cmd *cobra.Command, repo, teamDir, name string, opts teamRepairOptions) (*teamRepairResult, error) {
	if opts.MaxCycles <= 0 {
		opts.MaxCycles = 1
	}
	_, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	result := &teamRepairResult{
		Team:   teamInfoFromTopology(team),
		DryRun: opts.DryRun,
	}
	before, err := collectTeamHealth(teamDir, name, time.Now().UTC(), opts.IncludeJobs)
	if err != nil {
		return nil, err
	}
	result.HealthBefore = before.Health

	beforeDaemon := collectDaemonStatus(teamDir)
	result.Daemon = repairDaemonStepResult(beforeDaemon, repairOptions{
		DryRun:     opts.DryRun,
		SkipDaemon: opts.SkipDaemon,
	})
	if !opts.SkipDaemon && !opts.DryRun {
		if err := ensureDaemonReadyWithTimeout(cmd, repo, true, opts.ReadyTimeout); err != nil {
			return nil, err
		}
		dc, err := newDaemonClient(teamDir)
		if err != nil {
			return nil, err
		}
		if _, err := dc.TopologyReload(); err != nil {
			return nil, fmt.Errorf("reload topology: %w", err)
		}
		rec, err := dc.Reconcile()
		if err != nil {
			return nil, err
		}
		afterDaemon := collectDaemonStatus(teamDir)
		result.Daemon.Action = "reconciled"
		if !beforeDaemon.Running {
			result.Daemon.Action = "started"
		}
		result.Daemon.Running = afterDaemon.Running
		result.Daemon.Ready = afterDaemon.Ready
		result.Daemon.PID = afterDaemon.PID
		result.Daemon.Reconcile = rec
	}

	if opts.SkipQueue {
		result.Queue = repairQueueStep{Action: "skipped", Reason: "--skip-queue set"}
	} else {
		filters, err := parseQueueListFilters(daemon.QueueStateDead, nil, nil, nil, false, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		retries, err := teamQueueRetryResults(teamDir, name, filters, opts.Limit, opts.DryRun)
		if err != nil {
			return nil, err
		}
		result.Queue = repairQueueStep{Action: "retried", Results: retries}
		if opts.DryRun {
			result.Queue.Action = "would_retry"
		}
		if len(retries) == 0 {
			result.Queue.Action = "none"
		}
	}

	result.Tick = runTeamRepairTickStep(cmd, teamDir, name, opts)
	if result.Tick.Action == "error" {
		return nil, fmt.Errorf("tick: %s", result.Tick.Reason)
	}

	if !opts.DryRun {
		after, err := collectTeamHealth(teamDir, name, time.Now().UTC(), opts.IncludeJobs)
		if err != nil {
			return nil, err
		}
		result.HealthAfter = after.Health
	}
	return result, nil
}

func runTeamRepairTickStep(cmd *cobra.Command, teamDir, name string, opts teamRepairOptions) teamRepairTickStep {
	if opts.SkipTick {
		return teamRepairTickStep{Action: "skipped", Reason: "--skip-tick set"}
	}
	status := collectDaemonStatus(teamDir)
	if !opts.DryRun && (!status.Running || !status.Ready) {
		return teamRepairTickStep{Action: "skipped", Reason: "daemon is not running"}
	}
	if opts.UntilIdle {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		until, err := runTeamTickUntilIdle(ctx, cmd, teamDir, name, opts.Workspace, opts.Limit, tickOptions{}, opts.MaxCycles, opts.Interval)
		if err != nil {
			return teamRepairTickStep{Action: "error", Reason: err.Error()}
		}
		action := "until_idle"
		if until.HitLimit {
			action = "hit_limit"
		}
		return teamRepairTickStep{Action: action, UntilIdle: until}
	}
	tick, err := runTeamTick(cmd, teamDir, name, opts.Workspace, opts.Limit, tickOptions{DryRun: opts.DryRun, PreviewRoutes: opts.PreviewRoutes})
	if err != nil {
		return teamRepairTickStep{Action: "error", Reason: err.Error()}
	}
	action := "tick"
	if opts.DryRun {
		action = "would_tick"
	}
	return teamRepairTickStep{Action: action, Result: tick}
}

func previewTeamScheduleFire(teamDir string, team *topology.Team, now time.Time) (*daemon.ScheduleFireResult, error) {
	schedules, err := loadScheduleInfos(teamDir)
	if err != nil {
		return nil, err
	}
	rows := dueScheduleRows(teamSchedules(team, schedules), now)
	result := &daemon.ScheduleFireResult{DryRun: true, Schedules: []daemon.ScheduleFireItem{}}
	for _, row := range rows {
		payload, err := scheduleEventPayload(row, "")
		if err != nil {
			return nil, err
		}
		result.WouldFire++
		result.Schedules = append(result.Schedules, daemon.ScheduleFireItem{
			Name:      row.Name,
			EventType: topology.EventSchedule,
			Payload:   payload,
			Reason:    row.DueReason,
		})
	}
	return result, nil
}

func previewTeamQueueDrain(teamDir string, top *topology.Topology, team *topology.Team, now time.Time) (*daemon.QueueDrainResult, error) {
	ownedItems, err := collectTeamQueueItemsForTopology(teamDir, top, team)
	if err != nil {
		return nil, err
	}
	return previewQueueDrainItems(top, ownedItems, now), nil
}

func collectTeamQueueItemsForTopology(teamDir string, top *topology.Topology, team *topology.Team) ([]*daemon.QueueItem, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	ownedJobs := teamJobs(top, team, jobs)
	ownedItems := teamQueueItems(top, team, ownedJobs, items)
	return ownedItems, nil
}

func queueItemIDsFromPointers(items []*daemon.QueueItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil && strings.TrimSpace(item.ID) != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func advanceTeamReadyPipelineJobs(cmd *cobra.Command, teamDir string, team *topology.Team, workspace string, limit int, dryRun bool, previewRoutes bool) ([]pipelineAdvanceResult, error) {
	if team == nil || len(team.Pipelines) == 0 {
		return []pipelineAdvanceResult{}, nil
	}
	results := []pipelineAdvanceResult{}
	remaining := limit
	for _, pipeline := range team.Pipelines {
		if limit > 0 && remaining <= 0 {
			break
		}
		batchLimit := 0
		if limit > 0 {
			batchLimit = remaining
		}
		advanced, err := advanceReadyPipelineJobs(cmd, teamDir, pipeline, workspace, batchLimit, dryRun, previewRoutes)
		if err != nil {
			return nil, err
		}
		results = append(results, advanced...)
		if limit > 0 {
			remaining -= len(advanced)
		}
	}
	return results, nil
}

func renderTeamTickResult(w io.Writer, result *teamTickResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &teamTickResult{Tick: tickResult{DryRun: true}}
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
	fmt.Fprintf(w, "Team: %s\n", result.Team.Name)
	if result.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", result.Team.Description)
	}
	if result.CheckedAt != "" {
		fmt.Fprintf(w, "Checked: %s\n", result.CheckedAt)
	}
	fmt.Fprintf(w, "Dry run: %t\n", result.Tick.DryRun)
	fmt.Fprintln(w)
	if result.Tick.Schedule != nil {
		fmt.Fprintln(w, "Schedules:")
		if err := renderScheduleFireResult(w, result.Tick.Schedule, false, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Schedules: skipped")
	}
	fmt.Fprintln(w)
	if result.Tick.Queue != nil {
		fmt.Fprintln(w, "Queue:")
		if err := renderQueueDrainResult(w, result.Tick.Queue, false, nil); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "Queue: skipped")
	}
	fmt.Fprintln(w)
	if result.Tick.Advance != nil {
		fmt.Fprintln(w, "Pipeline advance:")
		return renderPipelineAdvanceResults(w, result.Tick.Advance, false, nil)
	}
	fmt.Fprintln(w, "Pipeline advance: skipped")
	return nil
}

func renderTeamTickUntilIdleResult(w io.Writer, result *teamTickUntilIdleResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &teamTickUntilIdleResult{Cycles: []*teamTickResult{}}
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
	for i, cycle := range result.Cycles {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "Cycle %d:\n", i+1)
		if err := renderTeamTickResult(w, cycle, false, nil); err != nil {
			return err
		}
	}
	if len(result.Cycles) > 0 {
		fmt.Fprintln(w)
	}
	if result.Idle {
		fmt.Fprintf(w, "team tick: idle after %d cycle(s)\n", result.CyclesRun)
	} else if result.HitLimit {
		fmt.Fprintf(w, "team tick: hit max cycles (%d) before idle\n", result.CyclesRun)
	} else {
		fmt.Fprintf(w, "team tick: stopped after %d cycle(s)\n", result.CyclesRun)
	}
	return nil
}

func renderTeamRepairResult(w io.Writer, result *teamRepairResult, jsonOut bool) error {
	if result == nil {
		result = &teamRepairResult{}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	fmt.Fprintf(w, "Team: %s\n", result.Team.Name)
	if result.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", result.Team.Description)
	}
	if result.DryRun {
		fmt.Fprintln(w, "Repair dry-run: true")
	} else {
		fmt.Fprintln(w, "Repair dry-run: false")
	}
	if result.HealthBefore != nil {
		fmt.Fprintf(w, "Health before: %s\n", repairHealthState(result.HealthBefore))
		renderRepairHealthActions(w, result.HealthBefore)
	}
	renderRepairDaemonStep(w, result.Daemon)
	fmt.Fprintln(w)
	renderRepairQueueStep(w, result.Queue)
	fmt.Fprintln(w)
	if err := renderTeamRepairTickStep(w, result.Tick); err != nil {
		return err
	}
	if result.HealthAfter != nil {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Health after: %s\n", repairHealthState(result.HealthAfter))
	}
	return nil
}

func renderTeamRepairTickStep(w io.Writer, step teamRepairTickStep) error {
	fmt.Fprintf(w, "Tick: %s", emptyDash(step.Action))
	if step.Reason != "" {
		fmt.Fprintf(w, " (%s)", step.Reason)
	}
	fmt.Fprintln(w)
	if step.Result != nil {
		return renderTeamTickResult(w, step.Result, false, nil)
	}
	if step.UntilIdle != nil {
		return renderTeamTickUntilIdleResult(w, step.UntilIdle, false, nil)
	}
	return nil
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

type teamSendClient struct {
	sendClient
	top  *topology.Topology
	team *topology.Team
}

func (c teamSendClient) Instances() ([]*daemon.Metadata, error) {
	metas, err := c.sendClient.Instances()
	if err != nil {
		return nil, err
	}
	return teamMetadata(c.top, c.team, metas), nil
}

type teamWaitLister struct {
	instanceLister
	top  *topology.Topology
	team *topology.Team
}

func (l teamWaitLister) Instances() ([]*daemon.Metadata, error) {
	metas, err := l.instanceLister.Instances()
	if err != nil {
		return nil, err
	}
	scoped := teamMetadata(l.top, l.team, metas)
	if l.top == nil || l.team == nil {
		return scoped, nil
	}
	seen := map[string]bool{}
	for _, meta := range scoped {
		if meta != nil {
			seen[meta.Instance] = true
		}
	}
	for _, name := range l.team.Instances {
		inst := l.top.Instances[name]
		if inst == nil || inst.Ephemeral || seen[name] {
			continue
		}
		scoped = append(scoped, &daemon.Metadata{
			Instance: name,
			Agent:    inst.Agent,
			Status:   "",
		})
		seen[name] = true
	}
	sort.Slice(scoped, func(i, j int) bool { return scoped[i].Instance < scoped[j].Instance })
	return scoped, nil
}

func collectTeamPruneTargets(teamDir, name string, opts teamPruneTargetOptions) ([]string, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	statuses, err := lifecycleStatusFilterSet(opts.StatusFilters)
	if err != nil {
		return nil, err
	}
	phases, err := lifecyclePhaseFilterSet(opts.PhaseFilters)
	if err != nil {
		return nil, err
	}
	var metas []*daemon.Metadata
	if dc, err := newDaemonClient(teamDir); err == nil {
		metas, err = dc.Instances()
		if err != nil {
			return nil, err
		}
	} else if errors.Is(err, errDaemonNotRunning) {
		metas, err = daemon.ListMetadata(daemon.DaemonRoot(teamDir))
		if err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	teamMetas := teamMetadata(top, team, metas)
	daemonByName := make(map[string]daemonInstanceInfo, len(teamMetas))
	for _, meta := range teamMetas {
		if meta == nil {
			continue
		}
		daemonByName[meta.Instance] = daemonInstanceInfo{
			status:     string(meta.Status),
			agent:      meta.Agent,
			pid:        meta.PID,
			startedAt:  meta.StartedAt,
			finishedAt: daemonMetadataFinishedAt(meta),
		}
	}
	var phaseByInstance map[string]string
	var staleInstances map[string]bool
	if len(phases) > 0 || opts.Stale || opts.Unhealthy {
		now := time.Now()
		if len(phases) > 0 {
			phaseByInstance = waitPhaseByInstance(teamDir, now)
		}
		if opts.Stale || opts.Unhealthy {
			staleInstances = staleInstanceSet(teamDir, now)
		}
	}
	names := selectRmTargetsWithUnhealthy(daemonByName, nil, statuses, phases, phaseByInstance, true, opts.Stale, opts.Unhealthy, staleInstances)
	if opts.OlderThanSet {
		names = filterRmTargetsOlderThan(names, daemonByName, opts.OlderThan, time.Now())
	}
	return names, nil
}

func renderTeamPruneNoTargets(w io.Writer, dryRun, quiet, jsonOut, summary bool, formatTemplate *template.Template) error {
	if jsonOut {
		if summary {
			return json.NewEncoder(w).Encode(lifecycleActionSummaryResult{Summary: summarizeInstanceRmResults(nil, dryRun)})
		}
		return json.NewEncoder(w).Encode([]instanceRmResult{})
	}
	if quiet || formatTemplate != nil {
		return nil
	}
	if summary {
		renderLifecycleActionSummary(w, summarizeInstanceRmResults(nil, dryRun))
		return nil
	}
	fmt.Fprintln(w, "(nothing to remove)")
	return nil
}

func teamMetadata(top *topology.Topology, team *topology.Team, metas []*daemon.Metadata) []*daemon.Metadata {
	if top == nil || team == nil {
		return nil
	}
	instanceNames := stringSliceSet(team.Instances)
	ephemeralOwners := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			ephemeralOwners[inst.Name] = true
		}
	}
	out := make([]*daemon.Metadata, 0, len(metas))
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		if instanceNames[meta.Instance] {
			out = append(out, meta)
			continue
		}
		if owner, ok := declaredEphemeralOwner(top, meta.Instance, meta.Agent); ok && ephemeralOwners[owner.Name] {
			out = append(out, meta)
		}
	}
	return out
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

func teamPlanRows(top *topology.Topology, team *topology.Team, rows []planRow, includeExtras bool) []planRow {
	if top == nil || team == nil {
		return nil
	}
	instances := stringSliceSet(team.Instances)
	agents := teamAgentSet(top, team)
	ephemeralOwners := map[string]bool{}
	for _, name := range team.Instances {
		inst := top.Instances[name]
		if inst == nil {
			continue
		}
		if inst.Ephemeral {
			ephemeralOwners[inst.Name] = true
		}
	}
	out := make([]planRow, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Instance] {
			continue
		}
		if instances[row.Instance] {
			out = append(out, row)
			seen[row.Instance] = true
			continue
		}
		if owner, ok := declaredEphemeralOwner(top, row.Instance, row.Agent); ok && ephemeralOwners[owner.Name] {
			out = append(out, row)
			seen[row.Instance] = true
			continue
		}
		if includeExtras && row.Kind == "extra" && agents[row.Agent] {
			out = append(out, row)
			seen[row.Instance] = true
		}
	}
	return out
}

func teamLogRows(top *topology.Topology, team *topology.Team, rows []logListRow) []logListRow {
	if top == nil || team == nil {
		return nil
	}
	instanceNames := stringSliceSet(team.Instances)
	ephemeralOwners := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			ephemeralOwners[inst.Name] = true
		}
	}
	out := make([]logListRow, 0, len(rows))
	for _, row := range rows {
		if instanceNames[row.Instance] {
			out = append(out, row)
			continue
		}
		if owner, ok := declaredEphemeralOwner(top, row.Instance, row.Agent); ok && ephemeralOwners[owner.Name] {
			out = append(out, row)
		}
	}
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

func teamAgentSet(top *topology.Topology, team *topology.Team) map[string]bool {
	agents := map[string]bool{}
	if top == nil || team == nil {
		return agents
	}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && strings.TrimSpace(inst.Agent) != "" {
			agents[inst.Agent] = true
		}
	}
	return agents
}

func teamQueueItems(top *topology.Topology, team *topology.Team, jobs []*job.Job, items []*daemon.QueueItem) []*daemon.QueueItem {
	if team == nil {
		return nil
	}
	instanceNames := stringSliceSet(team.Instances)
	agents := teamAgentSet(top, team)
	out := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if queueItemMatchesAnyJob(item, jobs) || queueItemMatchesTeamTarget(item, instanceNames, agents) {
			out = append(out, item)
		}
	}
	return out
}

func queueItemMatchesAnyJob(item *daemon.QueueItem, jobs []*job.Job) bool {
	for _, j := range jobs {
		if queueItemMatchesJob(item, j) {
			return true
		}
	}
	return false
}

func queueItemMatchesTeamTarget(item *daemon.QueueItem, instances, agents map[string]bool) bool {
	if item == nil {
		return false
	}
	for _, value := range []string{
		item.Instance,
		queuePayloadString(item.Payload, "target"),
		queuePayloadString(item.Payload, "instance"),
		queuePayloadString(item.Payload, "agent"),
	} {
		value = strings.TrimSpace(value)
		if value != "" && (instances[value] || agents[value]) {
			return true
		}
	}
	return false
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

func teamQueueActions(teamName string, jobs []*job.Job, items []*daemon.QueueItem) []string {
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
			return []string{fmt.Sprintf("agent-team team queue retry %s --all --job %s", teamName, id)}
		}
	}
	return []string{fmt.Sprintf("agent-team team queue retry %s --all", teamName)}
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
		add(fmt.Sprintf("agent-team team sync %s --wait", team.Name))
	}
	if snapshot.Queue.Dead > 0 {
		add(fmt.Sprintf("agent-team team queue retry %s --all", team.Name))
	}
	if snapshot.Queue.Pending > 0 {
		add(fmt.Sprintf("agent-team team queue %s --state pending", team.Name))
	}
	if snapshot.InstanceSummary.Crashed > 0 || snapshot.InstanceSummary.Stale > 0 {
		add(fmt.Sprintf("agent-team team events %s --tail 20", team.Name))
		add(fmt.Sprintf("agent-team team logs %s --latest", team.Name))
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
	fmt.Fprintf(w, "queue: total=%d pending=%d dead=%d delayed=%d attempts=%d\n",
		snapshot.Queue.Total,
		snapshot.Queue.Pending,
		snapshot.Queue.Dead,
		snapshot.Queue.Delayed,
		snapshot.Queue.Attempts,
	)
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

func renderTeamPlan(w io.Writer, snapshot *teamPlanSnapshot) {
	if snapshot == nil || snapshot.Plan == nil {
		fmt.Fprintln(w, "(no plan)")
		return
	}
	fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
	if snapshot.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
	}
	renderPlan(w, snapshot.Plan)
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
