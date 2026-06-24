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
	cmd.AddCommand(newTeamGraphCmd())
	cmd.AddCommand(newTeamDoctorCmd())
	cmd.AddCommand(newTeamOverviewCmd())
	cmd.AddCommand(newTeamNextCmd())
	cmd.AddCommand(newTeamRuntimeCmd())
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
	cmd.AddCommand(newTeamHoldCmd())
	cmd.AddCommand(newTeamReleaseCmd())
	cmd.AddCommand(newTeamAdvanceCmd())
	cmd.AddCommand(newTeamApproveCmd())
	cmd.AddCommand(newTeamRetryCmd())
	cmd.AddCommand(newTeamTimeoutCmd())
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
	cmd.AddCommand(newTeamExplainCmd())
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the team as JSON.")
	return cmd
}

func newTeamGraphCmd() *cobra.Command {
	var (
		repo          string
		graphFormat   string
		includeRoutes bool
		jsonOut       bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "graph <team>",
		Short: "Render a declared team graph.",
		Long:  "Render a read-only graph of one declared team's instances, pipelines, schedules, and step dispatch wiring.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut && cmd.Flags().Changed("format") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team graph: --format cannot be combined with --json.")
				return exitErr(2)
			}
			format, err := parsePipelineGraphFormat(graphFormat)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team graph: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			graph, err := collectTeamGraph(teamDir, args[0], includeRoutes)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team graph: %v\n", err)
				return exitErr(1)
			}
			return renderTeamGraph(cmd.OutOrStdout(), graph, format, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&graphFormat, "format", "text", "Graph output format: text, mermaid, or dot.")
	cmd.Flags().BoolVar(&includeRoutes, "routes", false, "Annotate pipeline steps with matching agent.dispatch routes.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit graph nodes and edges as JSON.")
	return cmd
}

func newTeamDoctorCmd() *cobra.Command {
	var (
		repo    string
		all     bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor <team>|--all",
		Short: "Validate one team's topology wiring.",
		Long: "Validate a declared team's topology wiring: team-owned pipeline workflows must be runnable, " +
			"pipeline step targets must be owned by the team, and team schedules should route back to team-owned instances or pipelines.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if all && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team doctor: --all cannot be combined with a team argument.")
				return exitErr(2)
			}
			if !all && len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team doctor: pass a team name or --all.")
				return exitErr(2)
			}
			tmpl, err := parseTeamDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team doctor: %v\n", err)
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
				return renderAllTeamDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl)
			}
			result, err := collectTeamDoctor(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team doctor: %v\n", err)
				return exitErr(1)
			}
			return renderTeamDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Validate all declared teams.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team doctor findings as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.")
	return cmd
}

func newTeamRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect team-owned runtime metadata.",
		Long:  "Inspect runtime metadata for daemon-known instances owned by one declared team.",
	}
	cmd.AddCommand(newTeamRuntimeResumePlanCmd())
	return cmd
}

func newTeamRuntimeResumePlanCmd() *cobra.Command {
	var (
		repo          string
		statusFilters []string
		runtimeFilter []string
		actionFilters []string
		summary       bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "resume-plan <team>",
		Short: "Show runtime resume and fallback commands for one team.",
		Long: "Show runtime resume and fallback commands for daemon metadata owned by one declared team. " +
			"This is the team-scoped form of `agent-team runtime resume-plan`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team runtime resume-plan: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team runtime resume-plan: --summary cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseRuntimeResumePlanFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team runtime resume-plan: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			plans, err := collectTeamRuntimeResumePlans(teamDir, args[0], statusFilters, runtimeFilter, actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team runtime resume-plan: %v\n", err)
				return exitErr(1)
			}
			if summary {
				out := summarizeRuntimeResumePlans(plans)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				renderRuntimeResumeSummary(cmd.OutOrStdout(), out)
				return nil
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(plans)
			}
			if tmpl != nil {
				return renderRuntimeResumePlanFormat(cmd.OutOrStdout(), plans, tmpl)
			}
			renderRuntimeResumePlans(cmd.OutOrStdout(), plans)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilter, "runtime", nil, "Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching team resume plans by recommended action, runtime, and status.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.")
	return cmd
}

func collectTeamRuntimeResumePlans(teamDir, name string, statusFilters []string, runtimeFilters []string, actionFilters []string) ([]runtimeResumePlan, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	statusSet, err := parseRuntimeResumeStatusFilter(statusFilters)
	if err != nil {
		return nil, err
	}
	runtimeSet, err := parseRuntimeResumeRuntimeFilter(runtimeFilters)
	if err != nil {
		return nil, err
	}
	actionSet, err := parseRuntimeResumeActionFilter(actionFilters)
	if err != nil {
		return nil, err
	}
	selected := teamMetadata(top, team, metas)
	plans := make([]runtimeResumePlan, 0, len(selected))
	for _, meta := range selected {
		if meta == nil {
			continue
		}
		if len(statusSet) > 0 && !statusSet[strings.ToLower(strings.TrimSpace(string(meta.Status)))] {
			continue
		}
		runtimeKind := lifecycleMetadataRuntimeKind(meta)
		if len(runtimeSet) > 0 && !runtimeSet[string(runtimeKind)] {
			continue
		}
		plan := runtimeResumePlanFromMetadata(meta)
		if len(actionSet) > 0 && !actionSet[plan.RecommendedAction] {
			continue
		}
		plans = append(plans, plan)
	}
	sort.SliceStable(plans, func(i, j int) bool {
		return plans[i].Instance < plans[j].Instance
	})
	return plans, nil
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

func renderTeamDoctor(stdout, stderr io.Writer, result *teamDoctorResult, jsonOut bool, tmpl *template.Template) error {
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
	if tmpl != nil {
		if err := renderTeamDoctorFormat(stdout, result, tmpl); err != nil {
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

func renderAllTeamDoctor(stdout, stderr io.Writer, result *allTeamDoctorResult, jsonOut bool, tmpl *template.Template) error {
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
	if tmpl != nil {
		if err := renderTeamDoctorFormat(stdout, result, tmpl); err != nil {
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

func parseTeamDoctorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("team-doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderTeamDoctorFormat(w io.Writer, result any, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
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
		runtimeKind string
		runtimeBin  string
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
				Runtime:     runtimeSelection{Kind: runtimeKind, Binary: runtimeBin},
				DryRun:      dryRun,
				JSON:        jsonOut,
				Format:      format,
				ErrPrefix:   "agent-team team run",
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Team pipeline to use when the team declares more than one.")
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Canonical ticket URL to store on the job.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the first pipeline step.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the first ready pipeline step immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
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
		repo           string
		watch          bool
		noClear        bool
		interval       time.Duration
		runtimeFilters []string
		jsonOut        bool
		format         string
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
			if format != "" && (watch || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team ps: --format cannot be combined with --watch or --json.")
				return exitErr(2)
			}
			tmpl, err := parsePsFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ps: %v\n", err)
				return exitErr(2)
			}
			opts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(nil, runtimeFilters, nil, nil, nil, false, false)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ps: %v\n", err)
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
				return runTeamPsWatchWithOptions(ctx, cmd.OutOrStdout(), teamDir, args[0], interval, jsonOut, clear, opts)
			}
			rows, err := collectTeamPsRowsWithOptions(teamDir, args[0], time.Now().UTC(), opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team ps: %v\n", err)
				return exitErr(1)
			}
			return renderTeamPs(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team instances until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team instances as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each team instance with a Go template, e.g. '{{.Instance}} {{.Status}}'.")
	return cmd
}

func newTeamJobsCmd() *cobra.Command {
	var (
		repo           string
		status         string
		sortBy         string
		runtimeFilters []string
		held           bool
		unheld         bool
		expiredHold    bool
		activeHold     bool
		summary        bool
		jsonOut        bool
		format         string
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
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team jobs: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(2)
			}
			if held && unheld {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team jobs: --held and --unheld cannot be combined.")
				return exitErr(2)
			}
			if expiredHold && activeHold {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team jobs: --expired-hold and --active-hold cannot be combined.")
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
			runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			jobs, err := collectTeamJobs(teamDir, args[0], statusFilter, sortMode, runtimes, jobHeldFilter(held, unheld), jobHoldExpiredFilter(expiredHold, activeHold))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team jobs: %v\n", err)
				return exitErr(1)
			}
			if summary {
				s := summarizeJobsWithRuntime(teamDir, jobs)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(s)
				}
				renderJobSummary(cmd.OutOrStdout(), s)
				return nil
			}
			return renderTeamJobs(cmd.OutOrStdout(), teamDir, jobs, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&status, "status", "", "Filter by job status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&sortBy, "sort", "id", "Sort jobs by id, status, target, ticket, created, updated, instance, branch, or pr.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show team-owned jobs whose instance metadata has this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&held, "held", false, "Only show held jobs.")
	cmd.Flags().BoolVar(&unheld, "unheld", false, "Only show jobs that are not held.")
	cmd.Flags().BoolVar(&expiredHold, "expired-hold", false, "Only show held jobs whose hold_until has passed.")
	cmd.Flags().BoolVar(&activeHold, "active-hold", false, "Only show held jobs whose hold is still active or has no deadline.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate team job counts instead of job rows.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
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
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "triage <team>",
		Short: "Show team-owned jobs that need operator attention.",
		Long: "Show a compact team-scoped work queue triage view from durable jobs, " +
			"persisted daemon queue items, status-file update previews, and ready pipeline steps.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team triage: --interval must be >= 0.")
				return exitErr(2)
			}
			if format != "" && (watch || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team triage: --format cannot be combined with --watch or --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobTriageFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team triage: %v\n", err)
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
			if !cmd.Flags().Changed("stale-after") {
				configured, err := configuredJobTriageStaleAfter(teamDir)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team triage: %v\n", err)
					return exitErr(2)
				}
				staleAfter = configured
			}
			if staleAfter < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team triage: --stale-after must be >= 0.")
				return exitErr(2)
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
			return renderJobTriage(cmd.OutOrStdout(), snapshot, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().DurationVar(&staleAfter, "stale-after", defaultJobTriageStaleAfter, "Flag queued or running jobs with no update after this duration (default: [health].job_stale_after or 24h; 0 disables stale checks).")
	cmd.Flags().StringVar(&minSeverity, "min-severity", "", "Only show attention rows at least this severe: critical, warning, or info.")
	cmd.Flags().StringSliceVar(&reasons, "reason", nil, "Only show attention rows with this reason. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the team triage view until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team triage snapshot as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team triage snapshot with a Go template, e.g. '{{.Summary.Total}} {{len .Attention}}'.")
	return cmd
}

func newTeamCleanupCmd() *cobra.Command {
	var (
		repo        string
		merged      bool
		forceBranch bool
		verifyPR    bool
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cleanup <team>",
		Short: "Clean up done jobs owned by one team.",
		Long:  "Preview or remove job-owned worktrees and branches for done jobs owned by one declared team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team cleanup: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobCleanupFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team cleanup: %v\n", err)
				return exitErr(2)
			}
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
			result := runJobCleanupJobs(teamDir, filepath.Dir(teamDir), teamJobs(top, team, jobs), dryRun, merged, forceBranch, verifyPR)
			result.Team = team.Name
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return err
				}
			} else if tmpl != nil {
				if err := renderJobCleanupFormat(cmd.OutOrStdout(), result, tmpl); err != nil {
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&merged, "merged", false, "Confirm the team's matching PRs have merged before removing worktrees and branches.")
	cmd.Flags().BoolVar(&forceBranch, "force-branch", false, "With --merged, delete job branches with git branch -D if they are not locally merged.")
	cmd.Flags().BoolVar(&verifyPR, "verify-pr", false, "Verify recorded GitHub PRs are merged with gh before cleanup.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team-owned job cleanup without removing anything.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the cleanup batch as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the cleanup batch with a Go template, e.g. '{{.Team}} {{.Cleaned}} {{.Failed}}'.")
	return cmd
}

func newTeamHoldCmd() *cobra.Command {
	var (
		repo     string
		limit    int
		states   []string
		message  string
		holdFor  time.Duration
		untilRaw string
		dryRun   bool
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "hold <team> [reason...]",
		Short: "Hold pipeline jobs owned by one team.",
		Long:  "Hold matching jobs in pipelines declared on one team without changing their lifecycle status.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team hold: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team hold: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineHoldFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team hold: %v\n", err)
				return exitErr(2)
			}
			var stateFilter map[string]bool
			stateDefault := !cmd.Flags().Changed("state")
			if !stateDefault {
				stateFilter, err = parseJobNextStateFilter(states, false)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team hold: %v\n", err)
					return exitErr(2)
				}
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			_, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team hold: %v\n", err)
				return exitErr(1)
			}
			holdUntil, err := parseJobHoldUntil(holdFor, cmd.Flags().Changed("for"), untilRaw, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team hold: %v\n", err)
				return exitErr(2)
			}
			reason := jobActionMessage(message, args[1:], "held")
			results, err := holdTeamPipelineJobs(teamDir, team, reason, holdUntil, stateFilter, stateDefault, limit, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team hold: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineHoldResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Hold at most this many matching team jobs; 0 means no limit.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to hold: ready, queued, running, blocked, failed, held, done, none, or all. Defaults to active non-held, non-done jobs.")
	cmd.Flags().StringVar(&message, "message", "", "Hold reason recorded on each team job.")
	cmd.Flags().DurationVar(&holdFor, "for", 0, "Hold for this duration, for example 30m or 2h.")
	cmd.Flags().StringVar(&untilRaw, "until", "", "Hold until this RFC3339 timestamp.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview holds without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit hold results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each hold result with a Go template, e.g. '{{.JobID}} {{.Action}}'.")
	return cmd
}

func newTeamReleaseCmd() *cobra.Command {
	var (
		repo        string
		limit       int
		message     string
		expiredOnly bool
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "release <team> [message...]",
		Short: "Release held pipeline jobs owned by one team.",
		Long:  "Release held jobs in pipelines declared on one team without changing their lifecycle status.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team release: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team release: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineHoldFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team release: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			_, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team release: %v\n", err)
				return exitErr(1)
			}
			statusMessage := jobActionMessage(message, args[1:], "released")
			results, err := releaseTeamPipelineJobs(teamDir, team, statusMessage, limit, expiredOnly, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team release: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineHoldResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Release at most this many held team jobs; 0 means no limit.")
	cmd.Flags().StringVar(&message, "message", "", "Release message recorded on each team job.")
	cmd.Flags().BoolVar(&expiredOnly, "expired", false, "Only release held jobs whose hold_until has passed.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview releases without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit release results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each release result with a Go template, e.g. '{{.JobID}} {{.Action}}'.")
	return cmd
}

func newTeamAdvanceCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		limit         int
		allReadySteps bool
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
			results, err := advanceTeamReadyPipelineJobs(cmd, teamDir, team, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, limit, dryRun, previewRoutes, allReadySteps)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced steps: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for advanced step dispatches (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for advanced step dispatches. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready team jobs, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent step for each selected team job.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview ready steps without dispatching them.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include local topology route and dispatch payload previews.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit advance results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newTeamApproveCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		limit         int
		dispatchNow   bool
		step          string
		message       string
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "approve <team>",
		Short: "Approve manual pipeline gates owned by one team.",
		Long: "Approve or preview blocked manual-gate steps for jobs in one team's declared pipelines. " +
			"Pass --step to target one stage, or --dispatch to immediately dispatch each approved step.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team approve: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team approve: --limit must be >= 0.")
				return exitErr(2)
			}
			if previewRoutes && (!dryRun || !dispatchNow) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team approve: --preview-routes requires --dry-run and --dispatch.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineApproveFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team approve: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			_, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team approve: %v\n", err)
				return exitErr(1)
			}
			results, err := approveTeamPipelineManualGates(cmd, teamDir, team, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, step, message, limit, dispatchNow, dryRun, previewRoutes)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team approve: daemon is not running — start it with `agent-team start`, or use --dry-run without --dispatch.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team approve: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineApproveResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for approved dispatches: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Approve at most this many manual gates; 0 means no limit.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch each approved manual gate immediately.")
	cmd.Flags().StringVar(&step, "step", "", "Approve only manual gates whose next blocked step has this id.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on each approved team job.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview manual gate approvals and optional dispatches without writing job or daemon state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run --dispatch, include local topology route and dispatch payload previews.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit approval results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newTeamRetryCmd() *cobra.Command {
	var (
		repo          string
		workspace     string
		runtimeKind   string
		runtimeBin    string
		limit         int
		dispatchNow   bool
		step          string
		message       string
		force         bool
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "retry <team>",
		Short: "Reset failed pipeline steps owned by one team.",
		Long: "Reset or preview failed-step retries for jobs in one team's declared pipelines. " +
			"Pass --step to target one stage, or --dispatch to immediately dispatch each reset retry.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team retry: --limit must be >= 0.")
				return exitErr(2)
			}
			if previewRoutes && (!dryRun || !dispatchNow) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team retry: --preview-routes requires --dry-run and --dispatch.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineRetryFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team retry: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			_, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team retry: %v\n", err)
				return exitErr(1)
			}
			results, err := retryTeamPipelineJobs(cmd, teamDir, team, workspace, runtimeSelection{Kind: runtimeKind, Binary: runtimeBin}, step, message, limit, force, dispatchNow, dryRun, previewRoutes)
			if err != nil {
				if errors.Is(err, errDaemonNotRunning) {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team retry: daemon is not running — start it with `agent-team start`, or use --dry-run without --dispatch.")
					return exitErr(2)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team retry: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineRetryResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for retried dispatches: auto, worktree, or repo.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile for --dispatch (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBin, "runtime-bin", "", "Runtime binary for --dispatch. Overrides env and repo config.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Retry at most this many failed team jobs; 0 means no limit.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch each reset failed step immediately.")
	cmd.Flags().StringVar(&step, "step", "", "Retry only failed team jobs whose next failed step has this id.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on each retried team job.")
	cmd.Flags().BoolVar(&force, "force", false, "Ignore step max_attempts caps for this explicit team retry.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview failed-step resets and optional dispatches without writing job or daemon state.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run --dispatch, include local topology route and dispatch payload previews.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit retry results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newTeamTimeoutCmd() *cobra.Command {
	var (
		repo        string
		limit       int
		step        string
		targetAgent string
		message     string
		includeJobs bool
		dryRun      bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "timeout <team>",
		Short: "Mark stale running work owned by one team failed.",
		Long: "Mark or preview stale running steps for jobs in one team's declared pipelines. " +
			"Add --jobs to include stale step-less jobs whose target instance belongs to the team. " +
			"Timed-out work becomes failed so the normal team retry flow can reopen it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team timeout: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team timeout: --limit must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parsePipelineTimeoutFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team timeout: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			_, team, err := loadTopologyTeam(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team timeout: %v\n", err)
				return exitErr(1)
			}
			results, err := timeoutTeamWork(teamDir, team, step, targetAgent, message, limit, includeJobs, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team timeout: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineTimeoutResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Mark at most this many stale running team jobs or steps failed; 0 means no limit.")
	cmd.Flags().StringVar(&step, "step", "", "Mark only stale running team steps with this id.")
	cmd.Flags().StringVar(&targetAgent, "target-agent", "", "Mark only stale running team work targeting this agent.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on each timed-out team job.")
	cmd.Flags().BoolVar(&includeJobs, "jobs", false, "Include stale step-less jobs whose target instance belongs to the team.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview stale-work failures without writing job state.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit timeout results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.Action}} {{.StepID}}'.")
	return cmd
}

func newTeamQueueCmd() *cobra.Command {
	var (
		repo        string
		stateFilter string
		eventTypes  []string
		jobs        []string
		runtimes    []string
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
			filters, err := parseQueueListFiltersWithRuntime(stateFilter, nil, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "Only show pending queue items whose next retry is due now.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the team queue table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate queue counts instead of queue rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team queue rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.AddCommand(newTeamQueueShowCmd())
	cmd.AddCommand(newTeamQueueQuarantineCmd())
	cmd.AddCommand(newTeamQueueRetryCmd())
	cmd.AddCommand(newTeamQueueDropCmd())
	cmd.AddCommand(newTeamQueuePruneCmd())
	return cmd
}

func newTeamQueueShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <team> <id>",
		Short: "Show one queue item owned by one team.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readTeamQueueItem(cmd, teamDir, args[0], args[1], "show")
			if err != nil {
				return err
			}
			return renderQueueItemResultWithActions(cmd.OutOrStdout(), item, jsonOut, tmpl, teamQueueActionResolver(args[0]), queueRuntimeMap(teamDir))
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the queue item as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the queue item with a Go template, e.g. '{{.ID}} {{.State}}'.")
	return cmd
}

func newTeamQueueQuarantineCmd() *cobra.Command {
	var (
		repo         string
		stateFilter  string
		eventTypes   []string
		jobs         []string
		restorable   bool
		unrestorable bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "quarantine <team>",
		Short: "List quarantined queue files scoped to one team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team team queue quarantine", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			items, err := collectTeamQueueQuarantineItems(teamDir, args[0], filters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine: %v\n", err)
				return exitErr(1)
			}
			items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
			return renderQueueQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFilter, "state", "", "Filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "Filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "Filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined files that cannot be restored.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team-owned quarantined queue files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each team-owned quarantined queue file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	cmd.AddCommand(newTeamQueueQuarantineShowCmd())
	cmd.AddCommand(newTeamQueueQuarantineRestoreCmd())
	cmd.AddCommand(newTeamQueueQuarantineDropCmd())
	return cmd
}

func newTeamQueueQuarantineShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <team> <quarantine-path>",
		Short: "Show one team-owned quarantined queue file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team team queue quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			item, err := readTeamQueueQuarantineItem(teamDir, args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine show: %v\n", err)
				return exitErr(1)
			}
			result, err := showQueueQuarantine(teamDir, item.Path)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine show: %v\n", err)
				return exitErr(1)
			}
			result.Team = args[0]
			return renderQueueQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the team-owned quarantined queue file as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team-owned quarantined queue file with a Go template, e.g. '{{.Team}} {{.ID}}'.")
	return cmd
}

func newTeamQueueQuarantineRestoreCmd() *cobra.Command {
	var (
		repo        string
		restoreAll  bool
		dryRun      bool
		force       bool
		stateFilter string
		eventTypes  []string
		jobs        []string
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <team> <quarantine-path>",
		Short: "Restore team-owned quarantined queue files.",
		Long:  "Restore one team-owned quarantined queue file by path, or restore a filtered team-owned batch of restorable files with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team team queue quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine restore: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if restoreAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine restore: --all requires exactly one team and cannot be combined with a path.")
					return exitErr(2)
				}
				items, err := collectTeamQueueQuarantineItems(teamDir, args[0], filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				items = filterQueueQuarantineRestorable(items, true, false)
				results, err := restoreQueueQuarantineItems(teamDir, items, dryRun, force)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine restore: %v\n", err)
					return exitErr(1)
				}
				return renderQueueQuarantineRestoreMany(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine restore: requires <team> and one path unless --all is set.")
				return exitErr(2)
			}
			if !filters.empty() {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine restore: filters require --all.")
				return exitErr(2)
			}
			if _, err := readTeamQueueQuarantineItem(teamDir, args[0], args[1]); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			result, err := restoreQueueQuarantine(teamDir, args[1], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&restoreAll, "all", false, "Restore all matching team-owned restorable quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active queue file with the same restore path.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newTeamQueueQuarantineDropCmd() *cobra.Command {
	var (
		repo         string
		dropAll      bool
		dryRun       bool
		stateFilter  string
		eventTypes   []string
		jobs         []string
		restorable   bool
		unrestorable bool
		olderThan    time.Duration
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <team> [quarantine-path]",
		Short: "Drop team-owned quarantined queue files after inspection.",
		Long:  "Drop one team-owned quarantined queue file by path, or drop a filtered team-owned batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			formatTemplate, err := parseQueueQuarantineCommandFormat(cmd, "agent-team team queue quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			filters, err := parseQueueListFilters(stateFilter, nil, eventTypes, jobs, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if dropAll {
				if len(args) != 1 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: --all requires exactly one team and cannot be combined with a path.")
					return exitErr(2)
				}
				items, err := collectTeamQueueQuarantineItems(teamDir, args[0], filters)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				items = filterQueueQuarantineRestorable(items, restorable, unrestorable)
				results, err := dropQueueQuarantineItems(teamDir, items, dryRun, olderThan, unrestorable, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				return renderQueueQuarantineDrop(cmd.OutOrStdout(), results, jsonOut, formatTemplate)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: requires <team> and one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || restorable || unrestorable || !filters.empty() {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: filters require --all.")
				return exitErr(2)
			}
			item, err := readTeamQueueQuarantineItem(teamDir, args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: %v\n", err)
				return exitErr(1)
			}
			result, err := dropQueueQuarantineItem(daemon.QueueRoot(daemon.DaemonRoot(teamDir)), item, dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue quarantine drop: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineDrop(cmd.OutOrStdout(), []queueQuarantineDropResult{result}, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching team-owned quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().BoolVar(&restorable, "restorable", false, "With --all, only drop quarantined files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newTeamQueueRetryCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		retryAll    bool
		dryRun      bool
		stateFilter string
		eventTypes  []string
		jobs        []string
		runtimes    []string
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
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue retry: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue retry: %v\n", err)
				return exitErr(2)
			}
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
				filters, err := parseQueueListFiltersWithRuntime(effectiveState, nil, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue retry: %v\n", err)
					return exitErr(2)
				}
				return runTeamQueueRetryAll(cmd.OutOrStdout(), teamDir, args[0], filters, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue retry: requires <team> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || len(jobs) > 0 || len(runtimes) > 0 || readyOnly || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue retry: --state, --event-type, --job, --runtime, --ready, and --limit require --all.")
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
				}}, jsonOut, tmpl)
			}
			if dc, err := newDaemonClient(teamDir); err == nil {
				outcome, err := dc.QueueRetry(id)
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
				}
				if tmpl != nil {
					return renderQueueRetryResults(cmd.OutOrStdout(), []queueRetryResult{{
						ID:         item.ID,
						State:      item.State,
						Instance:   outcome.Instance,
						InstanceID: outcome.InstanceID,
						Action:     outcome.Action,
						Reason:     outcome.Reason,
					}}, false, tmpl)
				}
				renderQueueRetryOutcome(cmd.OutOrStdout(), outcome)
				return nil
			} else if !errors.Is(err, errDaemonNotRunning) {
				return err
			}
			originalState := item.State
			if err := daemon.ResetQueueItemForRetry(daemon.DaemonRoot(teamDir), item); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(item)
			}
			if tmpl != nil {
				return renderQueueRetryResults(cmd.OutOrStdout(), []queueRetryResult{{
					ID:         item.ID,
					State:      originalState,
					Instance:   item.Instance,
					InstanceID: item.InstanceID,
					Action:     "reset",
				}}, false, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Team queue item %s marked pending; start the daemon to dispatch it.\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each retry result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&retryAll, "all", false, "Retry all matching team-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching team-owned queue items without retrying them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only retry pending queue items whose next retry is due now.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, retry at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newTeamQueueDropCmd() *cobra.Command {
	var (
		repo        string
		jsonOut     bool
		format      string
		dropAll     bool
		dryRun      bool
		stateFilter string
		eventTypes  []string
		jobs        []string
		runtimes    []string
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
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue drop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseQueueFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue drop: %v\n", err)
				return exitErr(2)
			}
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
				filters, err := parseQueueListFiltersWithRuntime(effectiveState, nil, eventTypes, jobs, runtimes, readyOnly, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue drop: %v\n", err)
					return exitErr(2)
				}
				return runTeamQueueDropAll(cmd.OutOrStdout(), teamDir, args[0], filters, limit, dryRun, jsonOut, tmpl)
			}
			if len(args) != 2 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue drop: requires <team> and one id unless --all is set.")
				return exitErr(2)
			}
			if stateFilter != "" || len(eventTypes) > 0 || len(jobs) > 0 || len(runtimes) > 0 || readyOnly || limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue drop: --state, --event-type, --job, --runtime, --ready, and --limit require --all.")
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
				}}, jsonOut, tmpl)
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
			if tmpl != nil {
				return renderQueueDropResults(cmd.OutOrStdout(), []queueDropResult{{
					ID:         item.ID,
					State:      item.State,
					Instance:   item.Instance,
					InstanceID: item.InstanceID,
					Action:     "dropped",
				}}, false, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Dropped team queue item %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each drop result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching team-owned queue items instead of one id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching team-owned queue items without dropping them.")
	cmd.Flags().StringVar(&stateFilter, "state", "", "With --all, filter by queue state: pending or dead. Defaults to dead, or pending with --ready.")
	cmd.Flags().StringSliceVar(&eventTypes, "event-type", nil, "With --all, filter by event type; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&jobs, "job", nil, "With --all, filter by job id or ticket; repeat or comma-separate values.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "With --all, filter by queued dispatch runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&readyOnly, "ready", false, "With --all, only drop pending queue items whose next retry is due now.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, drop at most this many matching queue items; 0 means no limit.")
	return cmd
}

func newTeamQueuePruneCmd() *cobra.Command {
	var (
		repo      string
		stateFlag string
		olderThan time.Duration
		dryRun    bool
		jsonOut   bool
		format    string
		runtimes  []string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune <team>",
		Short: "Prune team-owned queue items.",
		Long:  "Prune team-owned queue items. By default this removes dead-letter items owned by the selected team.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team queue prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			tmpl, err := parseQueuePruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue prune: %v\n", err)
				return exitErr(2)
			}
			state, err := parseQueuePruneState(stateFlag)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue prune: %v\n", err)
				return exitErr(2)
			}
			filters, err := parseQueueListFiltersWithRuntime("", nil, nil, nil, runtimes, false, time.Now().UTC())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team queue prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			return runTeamQueuePrune(cmd.OutOrStdout(), teamDir, args[0], state, olderThan, filters, time.Now().UTC(), dryRun, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stateFlag, "state", daemon.QueueStateDead, "Queue state to prune: dead, pending, or all.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune team-owned items older than this duration based on retry/dead-letter/update time.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Filter by queued dispatch runtime before pruning: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team-owned queue items that would be pruned without dropping them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each prune result with a Go template, e.g. '{{.ID}} {{.State}}'.")
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
		runtimes  []string
		phases    []string
		staleOnly bool
		unhealthy bool
		lastMsg   bool
		clean     bool
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
			if lastMsg {
				if follow {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last-message cannot be combined with --follow.")
					return exitErr(2)
				}
				if list {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last-message cannot be combined with --list.")
					return exitErr(2)
				}
				if jsonOut {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last-message cannot be combined with --json.")
					return exitErr(2)
				}
				if format != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last-message cannot be combined with --format.")
					return exitErr(2)
				}
				if cmd.Flags().Changed("tail") {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last-message cannot be combined with --tail.")
					return exitErr(2)
				}
				if strings.TrimSpace(since) != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last-message cannot be combined with --since.")
					return exitErr(2)
				}
				if strings.TrimSpace(grep) != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last-message cannot be combined with --grep.")
					return exitErr(2)
				}
				if clean {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --last-message cannot be combined with --clean.")
					return exitErr(2)
				}
			}
			if clean && list {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team logs: --clean cannot be combined with --list.")
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
			listOpts, err := newLogListOptionsWithRuntimeAndUnhealthy(statuses, runtimes, nil, phases, staleOnly, unhealthy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team logs: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			opts := logsOptions{
				Follow:      follow,
				Latest:      latest,
				Limit:       last,
				List:        list,
				JSON:        jsonOut,
				NoPrefix:    noPrefix,
				Tail:        tailLines,
				TailSet:     cmd.Flags().Changed("tail"),
				Since:       sinceCutoff,
				Grep:        grepPattern,
				Format:      formatTemplate,
				Unhealthy:   unhealthy,
				LastMessage: lastMsg,
				Clean:       clean,
			}
			return runTeamLogs(cmd, teamDir, args[0], opts, listOpts)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail selected team logs.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show the most recently started team instance log.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Show logs for the N most recently started team instances (0 = all).")
	cmd.Flags().BoolVar(&list, "list", false, "List team log streams instead of printing log content.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON with --list.")
	cmd.Flags().BoolVar(&noPrefix, "no-prefix", false, "Do not prefix lines when streaming multiple team logs.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only show logs for lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Only show logs for team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Only show logs for work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only show logs for team instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&unhealthy, "unhealthy", false, "Only show logs for crashed or stale team instances.")
	cmd.Flags().BoolVar(&lastMsg, "last-message", false, "Show clean final Codex response sidecars instead of raw runtime logs.")
	cmd.Flags().BoolVar(&clean, "clean", false, "Hide known Codex runtime diagnostic noise when printing raw team logs.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only include log streams modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	cmd.Flags().StringVar(&format, "format", "", "With --list, render each log stream with a Go template, e.g. '{{.Instance}} {{.LogPath}}'.")
	return cmd
}

func newTeamEventsCmd() *cobra.Command {
	var (
		repo           string
		follow         bool
		tail           int
		jsonOut        bool
		summary        bool
		format         string
		actionFilters  []string
		statusFilters  []string
		runtimeFilters []string
		sinceRaw       string
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
			filters, err = teamEventRuntimeFilter(teamDir, args[0], filters, runtimeFilters)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep streaming new lifecycle events.")
	cmd.Flags().IntVar(&tail, "tail", 0, "Show only the last N matching team events before returning or following (0 = all).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit raw JSONL events.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching team events by action, status, agent, and instance.")
	cmd.Flags().StringVar(&format, "format", "", "Render each event with a Go template, e.g. '{{.Action}} {{.Instance}} {{.Status}}'.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only show events with this action. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only show events with this lifecycle status. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show team events for daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&sinceRaw, "since", "", "Only show events since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	return cmd
}

func newTeamSendCmd() *cobra.Command {
	var (
		repo           string
		from           string
		message        string
		messageFile    string
		allStatuses    bool
		latest         bool
		last           int
		statusFilters  []string
		runtimeFilters []string
		phaseFilters   []string
		staleOnly      bool
		unhealthyOnly  bool
		dryRun         bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "send <team> [message...]",
		Short: "Send a mailbox message to team-owned instances.",
		Long: "Send a mailbox message to running daemon-known instances owned by one declared team. " +
			"Use --all to include every lifecycle status, or combine selectors such as --status, --runtime, --phase, --latest, --last, --stale, and --unhealthy.",
		Args: cobra.MinimumNArgs(1),
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
			body, err := sendMessageBody(message, messageFile, args[1:])
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
				RuntimeFilters:  runtimeFilters,
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
			client := teamSendClient{sendClient: baseClient, top: top, team: team}
			return runSendSelectionWithClient(cmd.OutOrStdout(), cmd.ErrOrStderr(), client, body, opts)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().StringVar(&message, "message", "", "Message text to send.")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read message text from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&allStatuses, "all", false, "Send to every daemon-known team instance regardless of lifecycle status.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Send to the most recently started team-owned daemon-known instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Send to the N most recently started team-owned daemon-known instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Send to team-owned instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Send to team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
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
		repo           string
		latest         bool
		last           int
		statusFilters  []string
		runtimeFilters []string
		phaseFilters   []string
		staleOnly      bool
		unhealthyOnly  bool
		untilPhases    []string
		untilRaw       string
		timeout        time.Duration
		interval       time.Duration
		dryRun         bool
		failOnCrash    bool
		jsonOut        bool
		quiet          bool
		summary        bool
		format         string
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
			if len(runtimeFilters) > 0 && len(names) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team wait: --runtime cannot be combined with instance names.")
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
			if _, err := lifecycleRuntimeFilterSet(runtimeFilters); err != nil {
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
				names, err = waitLatestInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister, nil, statusFilters, runtimeFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, unhealthyOnly)
			} else if last > 0 {
				names, err = waitLatestInstanceNamesLimitWithPhasesStaleRuntimeAndUnhealthy(lister, nil, statusFilters, runtimeFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, unhealthyOnly, last)
			} else if len(statusFilters) > 0 || len(runtimeFilters) > 0 || len(phaseFilters) > 0 || staleOnly || unhealthyOnly {
				names, err = waitFilteredInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister, nil, statusFilters, runtimeFilters, phaseFilters, phaseByInstance, staleInstances, staleOnly, unhealthyOnly)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&latest, "latest", false, "Wait for the most recently started team-owned instance after other filters.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Wait for the N most recently started team-owned instances after other filters (0 = all).")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Wait for team-owned instances currently in this lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Wait for team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
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
		repo           string
		statusFilters  []string
		runtimeFilters []string
		phaseFilters   []string
		staleOnly      bool
		unhealthyOnly  bool
		dryRun         bool
		olderThan      time.Duration
		quiet          bool
		jsonOut        bool
		summary        bool
		format         string
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
				StatusFilters:  statusFilters,
				RuntimeFilters: runtimeFilters,
				PhaseFilters:   phaseFilters,
				Stale:          staleOnly,
				Unhealthy:      unhealthyOnly,
				OlderThan:      olderThan,
				OlderThanSet:   olderThanSet,
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only remove finished team-owned instances in this lifecycle status: exited or crashed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only remove finished team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
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
		repo           string
		all            bool
		latest         bool
		last           int
		watch          bool
		jsonOut        bool
		summary        bool
		noClear        bool
		format         string
		sortBy         string
		interval       time.Duration
		statusFilters  []string
		runtimeFilters []string
		phaseFilters   []string
		staleOnly      bool
		unhealthyOnly  bool
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
			if len(names) > 0 && (len(statusFilters) > 0 || len(runtimeFilters) > 0 || len(phaseFilters) > 0 || staleOnly || unhealthyOnly) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team stats: --status, --runtime, --phase, --stale, and --unhealthy cannot be combined with instance names.")
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
			opts, err := newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(all, statusFilters, runtimeFilters, nil, phaseFilters, nil, unhealthyOnly)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
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
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show team-owned instances for this runtime: claude or codex. Can repeat or comma-separate.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team pipeline status as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline with a Go template, e.g. '{{.Pipeline}} {{.ReadySteps}}'.")
	return cmd
}

func newTeamExplainCmd() *cobra.Command {
	var (
		repo    string
		limit   int
		states  []string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "explain <team>",
		Short: "Explain pipeline jobs owned by one team.",
		Long: "Explain team-owned pipeline state from durable jobs, expanding each matching job with step readiness, " +
			"dependency blockers, gates, active instances, and suggested next actions.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team explain: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team explain: --limit must be >= 0.")
				return exitErr(2)
			}
			var stateFilter map[string]bool
			if cmd.Flags().Changed("state") {
				var err error
				stateFilter, err = parseJobNextStateFilter(states, false)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team explain: %v\n", err)
					return exitErr(2)
				}
			}
			tmpl, err := parsePipelineExplainFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team explain: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			rows, err := collectTeamPipelineExplain(teamDir, args[0], limit, stateFilter)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team explain: %v\n", err)
				return exitErr(1)
			}
			return renderPipelineExplainRows(cmd.OutOrStdout(), rows, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit job explanations per team-owned pipeline; 0 means no limit.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Only explain jobs whose next-step state matches: ready, queued, running, blocked, failed, held, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team pipeline explanations as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each pipeline explanation with a Go template, e.g. '{{.Pipeline}} {{len .Jobs}}'.")
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
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
		allReadySteps bool
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
				AllReadySteps: allReadySteps,
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&skipSchedules, "skip-schedules", false, "Skip due schedule work.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip queue drain work.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement work.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent team pipeline step in this tick.")
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
		allReadySteps bool
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
				AllReadySteps: allReadySteps,
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for advanced pipeline steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Advance at most this many ready pipeline jobs per cycle, or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&skipSchedules, "skip-schedules", false, "Skip due schedule work.")
	cmd.Flags().BoolVar(&skipDrain, "skip-drain", false, "Skip queue drain work.")
	cmd.Flags().BoolVar(&skipAdvance, "skip-advance", false, "Skip pipeline advancement work.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent team pipeline step in each drain cycle.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drain result with a Go template, e.g. '{{.Team.Name}} {{.CyclesRun}} {{.Idle}}'.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Delay between drain cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "Stop after this many cycles if work keeps appearing.")
	return cmd
}

func newTeamRepairCmd() *cobra.Command {
	var (
		repo             string
		workspace        string
		limit            int
		dryRun           bool
		previewRoutes    bool
		jsonOut          bool
		format           string
		skipDaemon       bool
		skipQueue        bool
		skipTick         bool
		includeJobs      bool
		timeoutJobs      bool
		timeoutPipelines bool
		retryPipelines   bool
		allReadySteps    bool
		timeoutStep      string
		timeoutMessage   string
		timeoutPipeline  string
		timeoutTarget    string
		retryStep        string
		retryMessage     string
		retryForce       bool
		untilIdle        bool
		readyTimeout     time.Duration
		interval         time.Duration
		maxCycles        int
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "repair <team>",
		Short: "Recover unhealthy orchestration state for one team.",
		Long: "Recover unhealthy orchestration state scoped to one team: ensure the daemon is ready, retry team-owned dead-letter queue items, " +
			"optionally time out stale team work, retry failed team pipeline steps, and run a scoped team tick. Use --dry-run to preview.",
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
			if retryPipelines && skipDaemon && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --retry-pipelines requires daemon access unless --dry-run is set.")
				return exitErr(2)
			}
			if timeoutJobs && timeoutPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --timeout-jobs cannot be combined with --timeout-pipelines.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutMessage) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --timeout-message requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutStep) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --timeout-step requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutPipeline) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --timeout-pipeline requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(timeoutTarget) != "" && !timeoutPipelines && !timeoutJobs {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --timeout-target-agent requires --timeout-pipelines or --timeout-jobs.")
				return exitErr(2)
			}
			if strings.TrimSpace(retryMessage) != "" && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --retry-message requires --retry-pipelines.")
				return exitErr(2)
			}
			if strings.TrimSpace(retryStep) != "" && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --retry-step requires --retry-pipelines.")
				return exitErr(2)
			}
			if retryForce && !retryPipelines {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --retry-force requires --retry-pipelines.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team repair: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseTeamRepairFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team repair: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := runTeamRepair(cmd, repo, teamDir, args[0], teamRepairOptions{
				Workspace:        workspace,
				Limit:            limit,
				DryRun:           dryRun,
				PreviewRoutes:    previewRoutes,
				SkipDaemon:       skipDaemon,
				SkipQueue:        skipQueue,
				SkipTick:         skipTick,
				IncludeJobs:      includeJobs,
				TimeoutJobs:      timeoutJobs,
				TimeoutPipelines: timeoutPipelines,
				RetryPipelines:   retryPipelines,
				AllReadySteps:    allReadySteps,
				TimeoutStep:      timeoutStep,
				TimeoutMessage:   timeoutMessage,
				TimeoutPipeline:  timeoutPipeline,
				TimeoutTarget:    timeoutTarget,
				RetryStep:        retryStep,
				RetryMessage:     retryMessage,
				RetryForce:       retryForce,
				UntilIdle:        untilIdle,
				ReadyTimeout:     readyTimeout,
				Interval:         interval,
				MaxCycles:        maxCycles,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team repair: %v\n", err)
				return exitErr(1)
			}
			return renderTeamRepairResult(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for retried or advanced team pipeline steps: auto, worktree, or repo.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Retry at most this many team dead-letter queue items or failed team pipeline jobs, and advance at most this many ready team pipeline jobs or ready steps with --all-ready-steps; 0 means no limit.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team repair actions without mutating state or starting the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-routes", false, "With --dry-run, include route and dispatch payload previews for retried or ready team pipeline steps.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the team repair result with a Go template, e.g. '{{.Team.Name}} {{.Queue.Action}}'.")
	cmd.Flags().BoolVar(&skipDaemon, "skip-daemon", false, "Do not start or reconcile the daemon.")
	cmd.Flags().BoolVar(&skipQueue, "skip-queue", false, "Do not retry team-owned dead-letter queue items.")
	cmd.Flags().BoolVar(&skipTick, "skip-tick", false, "Do not run a scoped team tick after queue retry.")
	cmd.Flags().BoolVar(&includeJobs, "jobs", false, "Include team-owned durable job and pipeline health.")
	cmd.Flags().BoolVar(&timeoutJobs, "timeout-jobs", false, "Mark stale running team job work failed before retrying failed pipeline steps.")
	cmd.Flags().BoolVar(&timeoutPipelines, "timeout-pipelines", false, "Mark stale running team pipeline steps failed before retrying failed pipeline steps.")
	cmd.Flags().BoolVar(&retryPipelines, "retry-pipelines", false, "Reset failed team pipeline steps and dispatch them before the scoped team tick.")
	cmd.Flags().BoolVar(&allReadySteps, "all-ready-steps", false, "Advance every currently ready independent team pipeline step during the scoped repair tick.")
	cmd.Flags().StringVar(&timeoutStep, "timeout-step", "", "With --timeout-jobs or --timeout-pipelines, mark only stale running team steps with this id failed.")
	cmd.Flags().StringVar(&timeoutMessage, "timeout-message", "", "Audit message to record when team timeout repair marks stale work failed.")
	cmd.Flags().StringVar(&timeoutPipeline, "timeout-pipeline", "", "With --timeout-jobs or --timeout-pipelines, mark only stale team work owned by this pipeline.")
	cmd.Flags().StringVar(&timeoutTarget, "timeout-target-agent", "", "With --timeout-jobs or --timeout-pipelines, mark only stale team work targeting this agent.")
	cmd.Flags().StringVar(&retryStep, "retry-step", "", "With --retry-pipelines, retry only failed team jobs whose next failed step has this id.")
	cmd.Flags().StringVar(&retryMessage, "retry-message", "", "Audit message to record when --retry-pipelines resets failed team steps.")
	cmd.Flags().BoolVar(&retryForce, "retry-force", false, "With --retry-pipelines, ignore step max_attempts caps for explicit team repair retry.")
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Run scoped team ticks until no immediate team queue, schedule, or pipeline work remains.")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Delay between --until-idle scoped team tick cycles.")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 20, "With --until-idle, stop after this many cycles if work keeps appearing.")
	return cmd
}

func newTeamUpCmd() *cobra.Command {
	var (
		repo           string
		prompt         string
		wait           bool
		timeout        time.Duration
		readyTimeout   time.Duration
		dryRun         bool
		summary        bool
		attach         bool
		tail           string
		runtimeFilters []string
		quiet          bool
		jsonOut        bool
		format         string
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
			runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
			if err != nil {
				return teamLifecycleUsageError(cmd, "agent-team team up", err.Error())
			}
			teamDir, names, err := loadTeamPersistentLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team up", err)
			}
			names, err = filterTeamLifecycleNamesByRuntime(teamDir, names, runtimes)
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team up", err)
			}
			if len(names) == 0 {
				return writeEmptyTeamLifecycleStart(cmd, args[0], "up", dryRun, wait, summary, quiet, jsonOut, formatTemplate)
			}
			if !dryRun {
				if err := ensureDaemonReadyWithTimeout(cmd, repo, jsonOut || quiet || summary || formatTemplate != nil, readyTimeout); err != nil {
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected instances to become healthy after starting.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned start/resume actions without changing daemon state.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&attach, "attach", false, "Follow the selected instance log after starting or resuming. Requires exactly one selected instance.")
	cmd.Flags().StringVar(&tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only target team-owned daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamDownCmd() *cobra.Command {
	var (
		repo           string
		force          bool
		wait           bool
		timeout        time.Duration
		waitTimeout    time.Duration
		dryRun         bool
		remove         bool
		summary        bool
		runtimeFilters []string
		quiet          bool
		jsonOut        bool
		format         string
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
			runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
			if err != nil {
				return teamLifecycleUsageError(cmd, "agent-team team down", err.Error())
			}
			teamDir, names, err := loadTeamStopLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team down", err)
			}
			names, err = filterTeamLifecycleNamesByRuntime(teamDir, names, runtimes)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if an instance does not stop within --timeout.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for stopped instances to reach a terminal state.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline (0 = no wait deadline; force defaults to 10s).")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait. Defaults to --timeout when unset; set 0 explicitly for no wait timeout.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned stop actions without changing daemon state.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after stopping.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only target team-owned daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamRestartCmd() *cobra.Command {
	var (
		repo           string
		prompt         string
		timeout        time.Duration
		readyTimeout   time.Duration
		wait           bool
		waitTimeout    time.Duration
		force          bool
		dryRun         bool
		summary        bool
		attach         bool
		tail           string
		runtimeFilters []string
		quiet          bool
		jsonOut        bool
		format         string
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
			runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
			if err != nil {
				return teamLifecycleUsageError(cmd, "agent-team team restart", err.Error())
			}
			teamDir, names, err := loadTeamPersistentLifecycleInstances(cmd, repo, args[0])
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team restart", err)
			}
			names, err = filterTeamLifecycleNamesByRuntime(teamDir, names, runtimes)
			if err != nil {
				return reportTeamLifecycleLoadError(cmd, "agent-team team restart", err)
			}
			if len(names) == 0 {
				return writeEmptyTeamLifecycleStart(cmd, args[0], "restart", dryRun, wait, summary, quiet, jsonOut, formatTemplate)
			}
			if !dryRun {
				if err := ensureDaemonReadyWithTimeout(cmd, repo, jsonOut || quiet || summary || formatTemplate != nil, readyTimeout); err != nil {
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
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
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only target team-owned daemon-known instances for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each action result with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamSyncCmd() *cobra.Command {
	var (
		repo           string
		dryRun         bool
		wait           bool
		stopExtras     bool
		timeout        time.Duration
		readyTimeout   time.Duration
		summary        bool
		quiet          bool
		jsonOut        bool
		format         string
		runtimeFilters []string
		actions        []string
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
			filters, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(nil, runtimeFilters, nil, nil, nil, false, false)
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
				Filters:      filters,
				Actions:      actionFilters,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview team topology convergence without starting the daemon or instances.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for selected team instances to become healthy after syncing.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "Also stop running daemon-known extras for this team's agents.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each sync action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only sync team-owned daemon-known plan rows for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actions, "action", nil, "Only sync plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.")
	return cmd
}

func newTeamPlanCmd() *cobra.Command {
	var (
		repo           string
		jsonOut        bool
		summary        bool
		stopExtras     bool
		runtimeFilters []string
		actionFilters  []string
		format         string
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
			filters, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(nil, runtimeFilters, nil, nil, nil, false, false)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team plan: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			snapshot, err := collectTeamPlan(teamDir, args[0], stopExtras, filters, actions)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team plan as JSON.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate action counts instead of per-instance rows.")
	cmd.Flags().BoolVar(&stopExtras, "stop-extras", false, "Preview running team-agent topology extras as stop actions.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show team-owned daemon-known plan rows for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan row with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newTeamHealthCmd() *cobra.Command {
	var (
		repo           string
		includeJobs    bool
		quiet          bool
		jsonOut        bool
		format         string
		runtimeFilters []string
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
			if format != "" && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team health: --format cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			tmpl, err := parseTeamHealthFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team health: %v\n", err)
				return exitErr(2)
			}
			healthOpts, err := newHealthOptionsWithRuntimeInstancesAndUnhealthy(nil, runtimeFilters, nil, nil, nil, false, false)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team health: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			snapshot, err := collectTeamHealthWithOptions(teamDir, args[0], time.Now().UTC(), includeJobs, healthOpts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team health: %v\n", err)
				return exitErr(1)
			}
			if !quiet {
				if err := renderTeamHealth(cmd.OutOrStdout(), snapshot, jsonOut, tmpl); err != nil {
					return err
				}
			}
			if snapshot.Health != nil && !snapshot.Health.Healthy {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&includeJobs, "jobs", false, "Include team-owned job and pipeline health.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team health as JSON.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only check team-owned daemon-known instances for this runtime: claude or codex. Daemon, queue, and job health remain team-scoped. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&format, "format", "", "Render team health with a Go template, e.g. '{{.Team.Name}} {{.Health.Healthy}}'.")
	return cmd
}

func newTeamStatusCmd() *cobra.Command {
	var (
		repo           string
		watch          bool
		noClear        bool
		interval       time.Duration
		jsonOut        bool
		format         string
		runtimeFilters []string
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
			if format != "" && (watch || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team team status: --format cannot be combined with --watch or --json.")
				return exitErr(2)
			}
			tmpl, err := parseTeamStatusFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team status: %v\n", err)
				return exitErr(2)
			}
			opts, err := newPsOptionsWithRuntimeInstancesAndUnhealthy(nil, runtimeFilters, nil, nil, nil, false, false)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team status: %v\n", err)
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
				return runTeamStatusWatchWithOptions(ctx, cmd.OutOrStdout(), teamDir, args[0], interval, jsonOut, clear, opts)
			}
			snapshot, err := collectTeamStatusWithOptions(teamDir, args[0], time.Now().UTC(), opts)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team team status: %v\n", err)
				return exitErr(1)
			}
			return renderTeamStatus(cmd.OutOrStdout(), snapshot, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh team status until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit team status as JSON.")
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only summarize team-owned instances for this runtime: claude or codex. Jobs, queue, pipelines, and schedules remain team-scoped. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&format, "format", "", "Render team status with a Go template, e.g. '{{.Team.Name}} {{.InstanceSummary.Total}}'.")
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
		runtimeFilters  []string
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
			opts, err := newMonitorOptionsWithRuntimeInstancesPhasesStaleAndUnhealthy(all, statusFilters, runtimeFilters, agentFilters, phaseFilters, instanceFilters, staleOnly, unhealthyOnly)
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
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
	cmd.Flags().StringSliceVar(&runtimeFilters, "runtime", nil, "Only show team-owned instances for this runtime in instance, stats, and plan sections: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agentFilters, "agent", nil, "Only show team-owned instances, stats, and plan rows for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phaseFilters, "phase", nil, "Only show team-owned instances and stats in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&instanceFilters, "instance", nil, "Only show team-owned instances with this name. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "With --plan, only show plan rows with this action: start, resume, keep, unsupported, on-demand, stop, or extra. Can repeat or comma-separate.")
	return cmd
}

type teamInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Instances   []string `json:"instances,omitempty"`
	Pipelines   []string `json:"pipelines,omitempty"`
	Schedules   []string `json:"schedules,omitempty"`
}

type teamGraph struct {
	Team      teamInfo            `json:"team"`
	Instances []teamGraphInstance `json:"instances,omitempty"`
	Pipelines []pipelineGraph     `json:"pipelines,omitempty"`
	Schedules []teamGraphSchedule `json:"schedules,omitempty"`
	Edges     []teamGraphEdge     `json:"edges,omitempty"`
}

type teamGraphInstance struct {
	Name      string `json:"name"`
	Agent     string `json:"agent,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
}

type teamGraphSchedule struct {
	Name       string `json:"name"`
	Every      string `json:"every,omitempty"`
	RunOnStart bool   `json:"run_on_start,omitempty"`
	Missing    bool   `json:"missing,omitempty"`
}

type teamGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind,omitempty"`
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
	ownedJobs       []*job.Job
	queueItems      []*daemon.QueueItem
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
	Workspace        string
	Limit            int
	DryRun           bool
	PreviewRoutes    bool
	SkipDaemon       bool
	SkipQueue        bool
	SkipTick         bool
	IncludeJobs      bool
	TimeoutJobs      bool
	TimeoutPipelines bool
	RetryPipelines   bool
	AllReadySteps    bool
	TimeoutStep      string
	TimeoutMessage   string
	TimeoutPipeline  string
	TimeoutTarget    string
	RetryStep        string
	RetryMessage     string
	RetryForce       bool
	UntilIdle        bool
	ReadyTimeout     time.Duration
	Interval         time.Duration
	MaxCycles        int
}

type teamRepairResult struct {
	Team            teamInfo                  `json:"team"`
	DryRun          bool                      `json:"dry_run,omitempty"`
	HealthBefore    *healthResult             `json:"health_before,omitempty"`
	Daemon          repairStepResult          `json:"daemon"`
	Queue           repairQueueStep           `json:"queue"`
	JobTimeout      repairPipelineTimeoutStep `json:"job_timeout"`
	PipelineTimeout repairPipelineTimeoutStep `json:"pipeline_timeout"`
	PipelineRetry   repairPipelineRetryStep   `json:"pipeline_retry"`
	Tick            teamRepairTickStep        `json:"tick"`
	HealthAfter     *healthResult             `json:"health_after,omitempty"`
}

type teamRepairTickStep struct {
	Action    string                   `json:"action"`
	Reason    string                   `json:"reason,omitempty"`
	Result    *teamTickResult          `json:"result,omitempty"`
	UntilIdle *teamTickUntilIdleResult `json:"until_idle,omitempty"`
}

type teamPruneTargetOptions struct {
	StatusFilters  []string
	RuntimeFilters []string
	PhaseFilters   []string
	Stale          bool
	Unhealthy      bool
	OlderThan      time.Duration
	OlderThanSet   bool
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

func collectTeamGraph(teamDir, name string, includeRoutes bool) (teamGraph, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return teamGraph{}, err
	}
	graph := teamGraph{Team: teamInfoFromTopology(team)}
	teamNode := "team:" + team.Name
	for _, name := range team.Instances {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		inst := top.Instances[name]
		node := teamGraphInstance{Name: name}
		if inst == nil {
			node.Missing = true
		} else {
			node.Agent = strings.TrimSpace(inst.Agent)
			node.Ephemeral = inst.Ephemeral
		}
		graph.Instances = append(graph.Instances, node)
		graph.Edges = append(graph.Edges, teamGraphEdge{From: teamNode, To: "instance:" + name, Kind: "owns_instance"})
	}
	for _, name := range team.Pipelines {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		pipeline := top.Pipelines[name]
		if pipeline == nil {
			graph.Pipelines = append(graph.Pipelines, pipelineGraph{Name: name})
			graph.Edges = append(graph.Edges, teamGraphEdge{From: teamNode, To: "pipeline:" + name, Kind: "owns_pipeline"})
			continue
		}
		pg := pipelineGraphFromTopology(top, pipeline, includeRoutes)
		graph.Pipelines = append(graph.Pipelines, pg)
		graph.Edges = append(graph.Edges, teamGraphEdge{From: teamNode, To: "pipeline:" + name, Kind: "owns_pipeline"})
		graph.Edges = append(graph.Edges, teamGraphEdge{From: "pipeline:" + name, To: "pipeline:" + name + ":trigger", Kind: "has_trigger"})
		for _, edge := range pg.Edges {
			graph.Edges = append(graph.Edges, teamGraphEdge{
				From: namespacedPipelineGraphNode(name, edge.From),
				To:   namespacedPipelineGraphNode(name, edge.To),
				Kind: "pipeline_dependency",
			})
		}
		for _, node := range pg.Nodes {
			if node.Missing {
				continue
			}
			targets := node.Routes
			if len(targets) == 0 && strings.TrimSpace(node.Target) != "" {
				targets = []string{strings.TrimSpace(node.Target)}
			}
			for _, target := range targets {
				target = strings.TrimSpace(target)
				if target == "" {
					continue
				}
				graph.Edges = append(graph.Edges, teamGraphEdge{
					From: "pipeline:" + name + ":step:" + node.ID,
					To:   "instance:" + target,
					Kind: "dispatches_to",
				})
			}
		}
	}
	for _, name := range team.Schedules {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		schedule := top.Schedules[name]
		node := teamGraphSchedule{Name: name}
		if schedule == nil {
			node.Missing = true
		} else {
			node.Every = schedule.Every.String()
			node.RunOnStart = schedule.RunOnStart
		}
		graph.Schedules = append(graph.Schedules, node)
		graph.Edges = append(graph.Edges, teamGraphEdge{From: teamNode, To: "schedule:" + name, Kind: "owns_schedule"})
		if schedule == nil {
			continue
		}
		payload := schedule.EventPayload()
		for _, pipelineName := range team.Pipelines {
			pipeline := top.Pipelines[pipelineName]
			if pipeline == nil || pipeline.Trigger == nil || pipeline.Trigger.Event != topology.EventSchedule || !pipeline.Trigger.Matches(payload) {
				continue
			}
			graph.Edges = append(graph.Edges, teamGraphEdge{From: "schedule:" + name, To: "pipeline:" + pipeline.Name, Kind: "triggers_pipeline"})
		}
	}
	return graph, nil
}

func namespacedPipelineGraphNode(pipeline, node string) string {
	if node == "<trigger>" {
		return "pipeline:" + pipeline + ":trigger"
	}
	return "pipeline:" + pipeline + ":step:" + node
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
	return collectTeamStatusWithOptions(teamDir, name, now, psOptions{})
}

func collectTeamStatusWithOptions(teamDir, name string, now time.Time, opts psOptions) (*teamStatusSnapshot, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	instanceRows := filterPsRows(teamInstanceRows(top, team, rows), opts)
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
	queueSummary := summarizeQueueItems(teamQueue, now.UTC())
	quarantine, err := collectTeamQueueQuarantine(teamDir, top, team, ownedJobs)
	if err != nil {
		return nil, err
	}
	applyQueueQuarantineSummary(&queueSummary, quarantine)
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
		JobSummary:      summarizeJobsWithRuntime(teamDir, ownedJobs),
		Queue:           queueSummary,
		PipelineStatus:  teamPipelineStatus(team, pipelineStatus),
		Schedules:       teamSchedules(team, schedules),
		ownedJobs:       ownedJobs,
		queueItems:      teamQueue,
	}
	snapshot.Actions = teamStatusActionsWithOptions(top, team, snapshot, opts)
	return snapshot, nil
}

func collectTeamPlan(teamDir, name string, stopExtras bool, filters psOptions, actions map[string]bool) (*teamPlanSnapshot, error) {
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
	result.Instances = filterPlanRowsWithActions(result.Instances, filters, actions)
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
		snapshot, err := collectTeamPlan(teamDir, name, opts.StopExtras, opts.Filters, opts.Actions)
		if err != nil {
			return err
		}
		return renderTeamSyncDryRun(cmd.OutOrStdout(), snapshot, opts)
	}
	if err := ensureDaemonReadyWithTimeout(cmd, repo, opts.JSON || opts.Quiet || opts.Summary || opts.Format != nil, opts.ReadyTimeout); err != nil {
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
	names, err := teamSyncTargetNamesFromCurrentPlan(teamDir, top, team, opts.Filters, opts.Actions)
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

func teamSyncTargetNamesFromCurrentPlan(teamDir string, top *topology.Topology, team *topology.Team, filters psOptions, actions map[string]bool) ([]string, error) {
	result, err := collectPlan(teamDir)
	if err != nil {
		return nil, err
	}
	rows := teamPlanRows(top, team, result.Instances, false)
	rows = filterPlanRowsWithActions(rows, filters, actions)
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		switch row.Action {
		case "start", "resume", "keep", lifecycleActionUnsupported:
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
	names, err := teamSyncTargetNamesFromCurrentPlan(teamDir, top, team, opts.Filters, opts.Actions)
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
			if !lifecycleMetadataSupportsManagedResume(lt.meta) {
				result := lifecycleTargetUnsupportedResumeResult(lt)
				results = append(results, result)
				if !opts.JSON && !opts.Quiet && opts.Format == nil && !opts.Summary {
					fmt.Fprintf(out, "  %-7s %-20s %s\n", result.Action, lt.name, result.Detail)
				}
				continue
			}
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
		if !syncMetadataMatchesFilters(meta, opts.Filters, nil) {
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
	return collectTeamHealthWithOptions(teamDir, name, now, includeJobs, healthOptions{})
}

func collectTeamHealthWithOptions(teamDir, name string, now time.Time, includeJobs bool, opts healthOptions) (*teamHealthSnapshot, error) {
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
	result := buildHealthWithDaemonStatus(collectDaemonStatus(teamDir), healthRows, scoped, now, opts)
	scopeTeamHealthIssueActions(result, team.Name)
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	ownedJobs := teamJobs(top, team, jobs)
	if err := addTeamQueueHealth(result, teamDir, top, team, ownedJobs, now); err != nil {
		return nil, err
	}
	if includeJobs {
		if err := addTeamJobHealth(result, teamDir, top, team, ownedJobs, now); err != nil {
			return nil, err
		}
	}
	return &teamHealthSnapshot{Team: teamInfoFromTopology(team), Health: result}, nil
}

func scopeTeamHealthIssueActions(result *healthResult, teamName string) {
	if result == nil || strings.TrimSpace(teamName) == "" {
		return
	}
	teamName = strings.TrimSpace(teamName)
	scopedSync := fmt.Sprintf("agent-team team sync %s --dry-run", teamName)
	scopedRuntimeResume := fmt.Sprintf("agent-team team runtime resume-plan %s --status crashed", teamName)
	for i := range result.Issues {
		for j, action := range result.Issues[i].Actions {
			action = strings.TrimSpace(action)
			switch {
			case action == "agent-team sync --dry-run":
				result.Issues[i].Actions[j] = scopedSync
			case teamHealthActionIsInstanceRuntimeResumePlan(action):
				result.Issues[i].Actions[j] = scopedRuntimeResume
			}
		}
	}
}

func teamHealthActionIsInstanceRuntimeResumePlan(action string) bool {
	action = strings.TrimSpace(action)
	return strings.HasPrefix(action, "agent-team runtime resume-plan ") &&
		!strings.Contains(action, " --job ") &&
		strings.HasSuffix(action, " --status crashed")
}

func collectTeamPsRows(teamDir, name string, now time.Time) ([]instanceRow, error) {
	return collectTeamPsRowsWithOptions(teamDir, name, now, psOptions{})
}

func collectTeamPsRowsWithOptions(teamDir, name string, now time.Time, opts psOptions) ([]instanceRow, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPsRows(teamDir, now)
	if err != nil {
		return nil, err
	}
	return filterPsRows(teamInstanceRows(top, team, rows), opts), nil
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

func filterTeamLifecycleNamesByRuntime(teamDir string, names []string, runtimes map[string]bool) ([]string, error) {
	if len(runtimes) == 0 {
		return names, nil
	}
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	runtimeByName := make(map[string]string, len(metas))
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		runtimeByName[meta.Instance] = metadataRuntimeKey(meta)
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if runtimes[runtimeByName[name]] {
			filtered = append(filtered, name)
		}
	}
	return filtered, nil
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

func addTeamQueueHealth(result *healthResult, teamDir string, top *topology.Topology, team *topology.Team, ownedJobs []*job.Job, now time.Time) error {
	if result == nil {
		return nil
	}
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return err
	}
	teamQueue := teamQueueItems(top, team, ownedJobs, queueItems)
	result.Queue = summarizeQueueItems(teamQueue, now.UTC())
	quarantine, err := collectTeamQueueQuarantine(teamDir, top, team, ownedJobs)
	if err != nil {
		return err
	}
	applyQueueQuarantineSummary(&result.Queue, quarantine)
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
	if result.Queue.Quarantined > 0 {
		result.addIssueWithSeverityAndActions(
			"queue_quarantined",
			"warning",
			"",
			"",
			"",
			"",
			fmt.Sprintf("team %q queue has %d quarantined file(s) (%d restorable, %d unrestorable)", team.Name, result.Queue.Quarantined, result.Queue.QuarantineRestorable, result.Queue.QuarantineUnrestorable),
			queueQuarantineHealthActions(result.Queue, team.Name, ""),
		)
	}
	return nil
}

func addTeamJobHealth(result *healthResult, teamDir string, top *topology.Topology, team *topology.Team, ownedJobs []*job.Job, now time.Time) error {
	if result == nil {
		return nil
	}
	ownedIDs := jobIDSet(ownedJobs)
	triage, err := collectJobTriageWithPolicy(teamDir, now.UTC())
	if err != nil {
		return err
	}
	triage.Summary = summarizeJobsWithRuntime(teamDir, ownedJobs)
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
		if row.StaleRunningSteps > 0 {
			result.addIssueWithSeverityAndActions(
				"pipeline_stale_running_step",
				"warning",
				"",
				"",
				"",
				"",
				fmt.Sprintf("pipeline %q has %d stale running step(s)", row.Pipeline, row.StaleRunningSteps),
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

func collectTeamJobs(teamDir, name string, status job.Status, sortMode string, runtimes map[string]bool, heldFilter *bool, holdExpiredFilter *bool) ([]*job.Job, error) {
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
	if heldFilter != nil {
		filtered := owned[:0]
		for _, j := range owned {
			if j.Held == *heldFilter {
				filtered = append(filtered, j)
			}
		}
		owned = filtered
	}
	if holdExpiredFilter != nil {
		filtered := owned[:0]
		now := time.Now().UTC()
		for _, j := range owned {
			if jobHoldExpirationMatches(j, *holdExpiredFilter, now) {
				filtered = append(filtered, j)
			}
		}
		owned = filtered
	}
	if len(runtimes) > 0 {
		runtimeByInstance, err := jobRuntimeIndex(teamDir, jobListFilters{Runtimes: runtimes})
		if err != nil {
			return nil, err
		}
		filtered := owned[:0]
		for _, j := range owned {
			if jobMatchesRuntimeFilter(j, jobListFilters{Runtimes: runtimes}, runtimeByInstance) {
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
		row := jobReadyRowFromJob(j, next)
		row.Actions = teamReadyRowActions(name, row)
		rows = append(rows, row)
	}
	return rows, nil
}

func teamReadyRowActions(teamName string, row jobReadyRow) []string {
	if row.State == "ready" || (row.State == "queued" && len(row.WaitingFor) == 0 && strings.TrimSpace(row.Instance) == "") {
		actions := []string{fmt.Sprintf("agent-team team advance %s --dry-run --preview-routes", teamName)}
		if row.ParallelReadySteps > 1 {
			actions = append(actions, fmt.Sprintf("agent-team team advance %s --all-ready-steps --dry-run --preview-routes", teamName))
		}
		return actions
	}
	if row.State == "queued" {
		return []string{fmt.Sprintf("agent-team team tick %s", teamName)}
	}
	return row.Actions
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
	quarantineItems, err := listQueueQuarantine(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	teamQuarantine := teamQueueQuarantineItems(top, team, ownedJobs, quarantineItems)
	snapshot, err := collectJobTriage(teamDir, now, staleAfter)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	snapshot.Summary = summarizeJobsWithRuntime(teamDir, ownedJobs)
	snapshot.Queue = summarizeQueueItems(teamQueue, now)
	applyQueueQuarantineSummary(&snapshot.Queue, teamQuarantine)
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
			if err := renderJobTriage(w, snapshot, false, nil); err != nil {
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
	return filterQueueItems(owned, filters.withNow(now).withRuntimeByInstance(queueRuntimeMap(teamDir))), nil
}

func collectTeamQueueQuarantine(teamDir string, top *topology.Topology, team *topology.Team, ownedJobs []*job.Job) ([]queueQuarantineItem, error) {
	items, err := listQueueQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	return teamQueueQuarantineItems(top, team, ownedJobs, items), nil
}

func collectTeamQueueQuarantineItems(teamDir, name string, filters queueListFilters) ([]queueQuarantineItem, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	items, err := collectTeamQueueQuarantine(teamDir, top, team, teamJobs(top, team, jobs))
	if err != nil {
		return nil, err
	}
	return filterQueueQuarantineItems(items, filters), nil
}

func readTeamQueueQuarantineItem(teamDir, name, rawPath string) (queueQuarantineItem, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	rel, err := normalizeQueueQuarantinePath(rawPath)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	item, err := inspectQueueQuarantineFile(queueRoot, rel)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	matches := teamQueueQuarantineItems(top, team, teamJobs(top, team, jobs), []queueQuarantineItem{item})
	if len(matches) == 0 {
		return queueQuarantineItem{}, fmt.Errorf("quarantined queue file %q is not owned by team %q", item.Path, name)
	}
	return item, nil
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

func runTeamQueueDropAll(w io.Writer, teamDir, name string, filters queueListFilters, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
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
	return renderQueueDropResults(w, results, jsonOut, tmpl)
}

func runTeamQueuePrune(w io.Writer, teamDir, name, state string, olderThan time.Duration, filters queueListFilters, now time.Time, dryRun, jsonOut bool, tmpl *template.Template) error {
	items, err := collectTeamQueueItems(teamDir, name, filters, now)
	if err != nil {
		return err
	}
	matches := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if queueItemMatchesPrune(item, state, olderThan, now) {
			matches = append(matches, item)
		}
	}
	results, err := pruneQueueItemMatches(teamDir, matches, dryRun)
	if err != nil {
		return err
	}
	return renderQueuePruneResults(w, results, jsonOut, tmpl)
}

func runTeamQueueRetryAll(w io.Writer, teamDir, name string, filters queueListFilters, limit int, dryRun, jsonOut bool, tmpl *template.Template) error {
	results, err := teamQueueRetryResults(teamDir, name, filters, limit, dryRun)
	if err != nil {
		return err
	}
	return renderQueueRetryResults(w, results, jsonOut, tmpl)
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
	renderQueueTableWithActions(w, items, queueRuntimeMap(teamDir), teamQueueActionResolver(name))
	return nil
}

func teamQueueActionResolver(name string) queueActionResolver {
	return func(item *daemon.QueueItem, now time.Time) []string {
		return teamQueueItemActions(name, item, now)
	}
}

func teamQueueItemActions(name string, item *daemon.QueueItem, now time.Time) []string {
	if queueItemActionJobID(item) != "" {
		return queueItemActions(item, now)
	}
	queueCommand := func(verb string) string {
		return fmt.Sprintf("agent-team team queue %s %s %s", verb, name, item.ID)
	}
	switch item.State {
	case daemon.QueueStateDead:
		return []string{
			queueCommand("retry"),
			queueCommand("drop"),
		}
	case daemon.QueueStatePending:
		if !item.NextRetry.IsZero() && item.NextRetry.After(now.UTC()) {
			return []string{
				queueCommand("show"),
				queueCommand("drop"),
			}
		}
		return []string{
			fmt.Sprintf("agent-team team drain %s", name),
			queueCommand("drop"),
		}
	default:
		return nil
	}
}

func runTeamQueueSummary(w io.Writer, teamDir, name string, filters queueListFilters, jsonOut bool) error {
	now := time.Now().UTC()
	summary, err := collectTeamQueueSummary(teamDir, name, filters, now)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	renderQueueSummary(w, summary)
	return nil
}

func collectTeamQueueSummary(teamDir, name string, filters queueListFilters, now time.Time) (queueSummary, error) {
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return queueSummary{}, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return queueSummary{}, err
	}
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return queueSummary{}, err
	}
	ownedJobs := teamJobs(top, team, jobs)
	runtimeByInstance := queueRuntimeMap(teamDir)
	filtered := filterQueueItems(teamQueueItems(top, team, ownedJobs, items), filters.withNow(now).withRuntimeByInstance(runtimeByInstance))
	summary := summarizeQueueItems(filtered, now, runtimeByInstance)
	quarantine, err := collectTeamQueueQuarantine(teamDir, top, team, ownedJobs)
	if err != nil {
		return queueSummary{}, err
	}
	applyQueueQuarantineSummary(&summary, filterQueueQuarantineItems(quarantine, filters.withNow(now)))
	return summary, nil
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
	if opts.LastMessage {
		if len(rows) == 1 {
			return streamSelectedLastMessageWithPrefix(cmd, teamDir, rows[0], "agent-team team logs")
		}
		return streamLastMessageRows(cmd.OutOrStdout(), teamDir, rows, !opts.NoPrefix)
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()
	if len(rows) == 1 {
		if opts.Follow {
			if err := streamLocalLog(ctx, cmd.OutOrStdout(), rows[0].path, true, opts.Tail, nil, opts.Clean); err != nil {
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
		return streamLocalLogRowsFollow(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Clean)
	}
	return streamLocalLogRowsOnce(ctx, cmd.OutOrStdout(), rows, opts.Tail, !opts.NoPrefix, opts.Grep, opts.Clean)
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

func teamEventRuntimeFilter(teamDir, name string, filters eventFilters, runtimeFilters []string) (eventFilters, error) {
	if len(runtimeFilters) == 0 {
		return filters, nil
	}
	runtimes, err := lifecycleRuntimeFilterSet(runtimeFilters)
	if err != nil {
		return filters, err
	}
	top, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return filters, err
	}
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return filters, err
	}
	selected := map[string]bool{}
	for _, meta := range teamMetadata(top, team, metas) {
		if meta == nil {
			continue
		}
		if runtimes[metadataRuntimeKey(meta)] {
			selected[meta.Instance] = true
		}
	}
	if len(selected) == 0 {
		selected[""] = false
	}
	filters.instances = selected
	filters.instancePrefixes = nil
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

func collectTeamPipelineExplain(teamDir, name string, limit int, stateFilter map[string]bool) ([]pipelineExplainRow, error) {
	_, team, err := loadTopologyTeam(teamDir, name)
	if err != nil {
		return nil, err
	}
	rows, err := collectPipelineExplainRows(teamDir, "", limit, stateFilter)
	if err != nil {
		return nil, err
	}
	return teamPipelineExplain(team, rows), nil
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
		advanced, err := advanceTeamReadyPipelineJobs(cmd, teamDir, team, workspace, runtimeSelection{}, limit, opts.DryRun, opts.PreviewRoutes, opts.AllReadySteps)
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

	jobTimeout, err := runTeamRepairJobTimeoutStep(teamDir, team, opts)
	if err != nil {
		return nil, err
	}
	result.JobTimeout = jobTimeout

	pipelineTimeout, err := runTeamRepairPipelineTimeoutStep(teamDir, team, opts)
	if err != nil {
		return nil, err
	}
	result.PipelineTimeout = pipelineTimeout

	pipelineRetry, err := runTeamRepairPipelineRetryStep(cmd, teamDir, team, opts)
	if err != nil {
		return nil, err
	}
	result.PipelineRetry = pipelineRetry

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

func runTeamRepairPipelineRetryStep(cmd *cobra.Command, teamDir string, team *topology.Team, opts teamRepairOptions) (repairPipelineRetryStep, error) {
	if !opts.RetryPipelines {
		return repairPipelineRetryStep{Action: "skipped", Reason: "--retry-pipelines not set"}, nil
	}
	message := strings.TrimSpace(opts.RetryMessage)
	if message == "" {
		message = "team repair retry failed pipeline step"
	}
	results, err := retryTeamPipelineJobs(cmd, teamDir, team, opts.Workspace, runtimeSelection{}, opts.RetryStep, message, opts.Limit, opts.RetryForce, true, opts.DryRun, opts.PreviewRoutes)
	if err != nil {
		return repairPipelineRetryStep{Action: "error", Reason: err.Error()}, err
	}
	action := "retried"
	if opts.DryRun {
		action = "would_dispatch"
	}
	if len(results) == 0 {
		action = "none"
	}
	return repairPipelineRetryStep{Action: action, Results: results}, nil
}

func runTeamRepairPipelineTimeoutStep(teamDir string, team *topology.Team, opts teamRepairOptions) (repairPipelineTimeoutStep, error) {
	if !opts.TimeoutPipelines {
		return repairPipelineTimeoutStep{Action: "skipped", Reason: "--timeout-pipelines not set"}, nil
	}
	message := strings.TrimSpace(opts.TimeoutMessage)
	if message == "" {
		message = "team repair timed out stale pipeline step"
	}
	results, err := timeoutTeamPipelineJobs(teamDir, team, opts.TimeoutPipeline, opts.TimeoutStep, opts.TimeoutTarget, message, opts.Limit, opts.DryRun)
	if err != nil {
		return repairPipelineTimeoutStep{Action: "error", Reason: err.Error()}, err
	}
	action := "timed_out"
	if opts.DryRun {
		action = "would_fail"
	}
	if len(results) == 0 {
		action = "none"
	}
	return repairPipelineTimeoutStep{Action: action, Results: results}, nil
}

func runTeamRepairJobTimeoutStep(teamDir string, team *topology.Team, opts teamRepairOptions) (repairPipelineTimeoutStep, error) {
	if !opts.TimeoutJobs {
		return repairPipelineTimeoutStep{Action: "skipped", Reason: "--timeout-jobs not set"}, nil
	}
	message := strings.TrimSpace(opts.TimeoutMessage)
	if message == "" {
		message = "team repair timed out stale job work"
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return repairPipelineTimeoutStep{Action: "error", Reason: err.Error()}, err
	}
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return repairPipelineTimeoutStep{Action: "error", Reason: err.Error()}, err
	}
	candidates := filterJobTimeoutCandidates(teamTimeoutJobCandidates(team, jobs), jobTimeoutFilters{
		Pipeline:    opts.TimeoutPipeline,
		TargetAgent: opts.TimeoutTarget,
	})
	results, err := timeoutStaleJobWork(teamDir, candidates, opts.TimeoutStep, opts.TimeoutTarget, message, opts.Limit, opts.DryRun, time.Now().UTC(), staleAfter)
	if err != nil {
		return repairPipelineTimeoutStep{Action: "error", Reason: err.Error()}, err
	}
	action := "timed_out"
	if opts.DryRun {
		action = "would_fail"
	}
	if len(results) == 0 {
		action = "none"
	}
	return repairPipelineTimeoutStep{Action: action, Results: results}, nil
}

func teamTimeoutJobCandidates(team *topology.Team, jobs []*job.Job) []*job.Job {
	if team == nil {
		return nil
	}
	pipelines := stringSliceSet(team.Pipelines)
	targets := stringSliceSet(team.Instances)
	out := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if strings.TrimSpace(j.Pipeline) != "" {
			if pipelines[j.Pipeline] {
				out = append(out, j)
			}
			continue
		}
		if targets[j.Target] {
			out = append(out, j)
		}
	}
	return out
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
		until, err := runTeamTickUntilIdle(ctx, cmd, teamDir, name, opts.Workspace, opts.Limit, tickOptions{AllReadySteps: opts.AllReadySteps}, opts.MaxCycles, opts.Interval)
		if err != nil {
			return teamRepairTickStep{Action: "error", Reason: err.Error()}
		}
		action := "until_idle"
		if until.HitLimit {
			action = "hit_limit"
		}
		return teamRepairTickStep{Action: action, UntilIdle: until}
	}
	tick, err := runTeamTick(cmd, teamDir, name, opts.Workspace, opts.Limit, tickOptions{DryRun: opts.DryRun, PreviewRoutes: opts.PreviewRoutes, AllReadySteps: opts.AllReadySteps})
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
		payload, err := scheduleEventPayload(row, nil, "")
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

func advanceTeamReadyPipelineJobs(cmd *cobra.Command, teamDir string, team *topology.Team, workspace string, selection runtimeSelection, limit int, dryRun bool, previewRoutes bool, allReadySteps bool) ([]pipelineAdvanceResult, error) {
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
		advanced, err := advanceReadyPipelineJobs(cmd, teamDir, pipeline, workspace, selection, batchLimit, dryRun, previewRoutes, allReadySteps)
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

func holdTeamPipelineJobs(teamDir string, team *topology.Team, reason string, holdUntil time.Time, stateFilter map[string]bool, stateDefault bool, limit int, dryRun bool) ([]pipelineHoldResult, error) {
	if team == nil || len(team.Pipelines) == 0 {
		return []pipelineHoldResult{}, nil
	}
	results := []pipelineHoldResult{}
	remaining := limit
	for _, pipeline := range team.Pipelines {
		if limit > 0 && remaining <= 0 {
			break
		}
		batchLimit := 0
		if limit > 0 {
			batchLimit = remaining
		}
		held, err := holdPipelineJobs(teamDir, pipeline, reason, holdUntil, stateFilter, stateDefault, batchLimit, dryRun)
		if err != nil {
			return nil, err
		}
		results = append(results, held...)
		if limit > 0 {
			remaining -= len(held)
		}
	}
	return results, nil
}

func releaseTeamPipelineJobs(teamDir string, team *topology.Team, message string, limit int, expiredOnly bool, dryRun bool) ([]pipelineHoldResult, error) {
	if team == nil || len(team.Pipelines) == 0 {
		return []pipelineHoldResult{}, nil
	}
	results := []pipelineHoldResult{}
	remaining := limit
	for _, pipeline := range team.Pipelines {
		if limit > 0 && remaining <= 0 {
			break
		}
		batchLimit := 0
		if limit > 0 {
			batchLimit = remaining
		}
		released, err := releasePipelineJobs(teamDir, pipeline, message, batchLimit, expiredOnly, dryRun)
		if err != nil {
			return nil, err
		}
		results = append(results, released...)
		if limit > 0 {
			remaining -= len(released)
		}
	}
	return results, nil
}

func approveTeamPipelineManualGates(cmd *cobra.Command, teamDir string, team *topology.Team, workspace string, selection runtimeSelection, stepFilter string, message string, limit int, dispatchNow bool, dryRun bool, previewRoutes bool) ([]pipelineApproveResult, error) {
	if team == nil || len(team.Pipelines) == 0 {
		return []pipelineApproveResult{}, nil
	}
	results := []pipelineApproveResult{}
	remaining := limit
	for _, pipeline := range team.Pipelines {
		if limit > 0 && remaining <= 0 {
			break
		}
		batchLimit := 0
		if limit > 0 {
			batchLimit = remaining
		}
		approved, err := approvePipelineManualGates(cmd, teamDir, pipeline, workspace, selection, stepFilter, message, batchLimit, dispatchNow, dryRun, previewRoutes)
		if err != nil {
			return nil, err
		}
		results = append(results, approved...)
		if limit > 0 {
			remaining -= len(approved)
		}
	}
	return results, nil
}

func retryTeamPipelineJobs(cmd *cobra.Command, teamDir string, team *topology.Team, workspace string, selection runtimeSelection, stepFilter string, message string, limit int, force bool, dispatchNow bool, dryRun bool, previewRoutes bool) ([]pipelineRetryResult, error) {
	if team == nil || len(team.Pipelines) == 0 {
		return []pipelineRetryResult{}, nil
	}
	results := []pipelineRetryResult{}
	remaining := limit
	for _, pipeline := range team.Pipelines {
		if limit > 0 && remaining <= 0 {
			break
		}
		batchLimit := 0
		if limit > 0 {
			batchLimit = remaining
		}
		retried, err := retryPipelineJobs(cmd, teamDir, pipeline, workspace, selection, stepFilter, message, batchLimit, force, dispatchNow, dryRun, previewRoutes)
		if err != nil {
			return nil, err
		}
		results = append(results, retried...)
		if limit > 0 {
			remaining -= len(retried)
		}
	}
	return results, nil
}

func timeoutTeamPipelineJobs(teamDir string, team *topology.Team, pipelineFilter string, stepFilter string, targetFilter string, message string, limit int, dryRun bool) ([]pipelineTimeoutResult, error) {
	if team == nil || len(team.Pipelines) == 0 {
		return []pipelineTimeoutResult{}, nil
	}
	pipelineFilter = strings.TrimSpace(pipelineFilter)
	results := []pipelineTimeoutResult{}
	remaining := limit
	for _, pipeline := range team.Pipelines {
		if pipelineFilter != "" && pipeline != pipelineFilter {
			continue
		}
		if limit > 0 && remaining <= 0 {
			break
		}
		batchLimit := 0
		if limit > 0 {
			batchLimit = remaining
		}
		timedOut, err := timeoutPipelineJobs(teamDir, pipeline, stepFilter, targetFilter, message, batchLimit, dryRun)
		if err != nil {
			return nil, err
		}
		results = append(results, timedOut...)
		if limit > 0 {
			remaining -= len(timedOut)
		}
	}
	return results, nil
}

func timeoutTeamWork(teamDir string, team *topology.Team, stepFilter string, targetFilter string, message string, limit int, includeJobs bool, dryRun bool) ([]pipelineTimeoutResult, error) {
	if !includeJobs {
		return timeoutTeamPipelineJobs(teamDir, team, "", stepFilter, targetFilter, message, limit, dryRun)
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return nil, err
	}
	candidates := filterJobTimeoutCandidates(teamTimeoutJobCandidates(team, jobs), jobTimeoutFilters{TargetAgent: targetFilter})
	return timeoutStaleJobWork(teamDir, candidates, stepFilter, targetFilter, message, limit, dryRun, time.Now().UTC(), staleAfter)
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

func parseTeamRepairFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("team-repair-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderTeamRepairResult(w io.Writer, result *teamRepairResult, jsonOut bool, tmpl *template.Template) error {
	if result == nil {
		result = &teamRepairResult{}
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderTeamRepairFormat(w, result, tmpl)
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
	renderRepairJobTimeoutStep(w, result.JobTimeout)
	fmt.Fprintln(w)
	renderRepairPipelineTimeoutStep(w, result.PipelineTimeout)
	fmt.Fprintln(w)
	if err := renderRepairPipelineRetryStep(w, result.PipelineRetry); err != nil {
		return err
	}
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

func renderTeamRepairFormat(w io.Writer, result *teamRepairResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
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
	return runTeamPsWatchWithOptions(ctx, w, teamDir, name, interval, jsonOut, clear, psOptions{})
}

func runTeamPsWatchWithOptions(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, jsonOut bool, clear bool, opts psOptions) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		rows, err := collectTeamPsRowsWithOptions(teamDir, name, time.Now().UTC(), opts)
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
	return runTeamStatusWatchWithOptions(ctx, w, teamDir, name, interval, jsonOut, clear, psOptions{})
}

func runTeamStatusWatchWithOptions(ctx context.Context, w io.Writer, teamDir, name string, interval time.Duration, jsonOut bool, clear bool, opts psOptions) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectTeamStatusWithOptions(teamDir, name, time.Now().UTC(), opts)
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
			if err := renderTeamStatus(w, snapshot, false, nil); err != nil {
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
	runtimes, err := lifecycleRuntimeFilterSet(opts.RuntimeFilters)
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
		if len(runtimes) > 0 && !runtimes[metadataRuntimeKey(meta)] {
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
	for _, row := range rows {
		rowByName[row.Instance] = row
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
			for _, row := range rows {
				if seen[row.Instance] {
					continue
				}
				owner, ok := declaredEphemeralOwner(top, row.Instance, row.Agent)
				if !ok || owner.Name != inst.Name {
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
	ephemeralOwners := map[string]bool{}
	for _, name := range team.Instances {
		if inst := top.Instances[name]; inst != nil && inst.Ephemeral {
			ephemeralOwners[inst.Name] = true
		}
	}
	out := make([]instanceRow, 0, len(rows))
	for _, row := range rows {
		if instanceNames[row.Instance] {
			out = append(out, row)
			continue
		}
		if owner, ok := declaredEphemeralOwner(top, row.Instance, row.Agent); ok && ephemeralOwners[owner.Name] {
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
			scoped := row
			scoped.Actions = teamPipelineActions(team.Name, row)
			out = append(out, scoped)
		}
	}
	return out
}

func teamPipelineExplain(team *topology.Team, rows []pipelineExplainRow) []pipelineExplainRow {
	if team == nil || len(team.Pipelines) == 0 {
		return nil
	}
	pipelines := stringSliceSet(team.Pipelines)
	out := make([]pipelineExplainRow, 0, len(rows))
	for _, row := range rows {
		if pipelines[row.Pipeline] {
			scoped := row
			scoped.Actions = teamPipelineActions(team.Name, row.Status)
			scoped.Status.Actions = append([]string(nil), scoped.Actions...)
			out = append(out, scoped)
		}
	}
	return out
}

func teamPipelineActions(teamName string, row pipelineStatusRow) []string {
	actions := []string{}
	if row.ReadySteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team team advance %s --dry-run --preview-routes", teamName))
	}
	if row.ParallelReadySteps > 1 {
		actions = append(actions, fmt.Sprintf("agent-team team advance %s --all-ready-steps --dry-run --preview-routes", teamName))
	}
	if row.FailedSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team team retry %s --dry-run --dispatch --preview-routes", teamName))
		actions = append(actions, fmt.Sprintf("agent-team team repair %s --retry-pipelines --dry-run --preview-routes", teamName))
		actions = append(actions, fmt.Sprintf("agent-team team explain %s --state failed", teamName))
		actions = append(actions, fmt.Sprintf("agent-team team ready %s --state failed", teamName))
	}
	if row.StaleRunningSteps > 0 {
		actions = append(actions, "agent-team job reconcile events --dry-run")
		actions = append(actions, fmt.Sprintf("agent-team team timeout %s --dry-run", teamName))
		actions = append(actions, fmt.Sprintf("agent-team team repair %s --timeout-jobs --dry-run", teamName))
		actions = append(actions, fmt.Sprintf("agent-team team explain %s --state running", teamName))
		actions = append(actions, fmt.Sprintf("agent-team team ready %s --state running", teamName))
	}
	if row.ManualGates > 0 {
		actions = append(actions, fmt.Sprintf("agent-team team approve %s --dry-run --dispatch --preview-routes", teamName))
	}
	if row.BlockedSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team team explain %s --state blocked", teamName))
		actions = append(actions, fmt.Sprintf("agent-team team ready %s --state blocked", teamName))
	}
	if row.HeldSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team team explain %s --state held", teamName))
		actions = append(actions, fmt.Sprintf("agent-team team ready %s --state held", teamName))
	}
	if row.QueuedSteps > 0 {
		actions = append(actions, fmt.Sprintf("agent-team team tick %s", teamName))
	}
	return actions
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
	return teamStatusActionsWithOptions(top, team, snapshot, psOptions{})
}

func teamStatusActionsWithOptions(top *topology.Topology, team *topology.Team, snapshot *teamStatusSnapshot, opts psOptions) []string {
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
	if !psOptionsHasSelectionFilters(opts) {
		for _, name := range team.Instances {
			inst := top.Instances[name]
			if inst == nil || inst.Ephemeral {
				continue
			}
			if rowsByName[name].Status != "running" {
				missingPersistent = append(missingPersistent, name)
			}
		}
	}
	if len(missingPersistent) > 0 {
		sort.Strings(missingPersistent)
		add(fmt.Sprintf("agent-team team sync %s --wait", team.Name))
	}
	if snapshot.Queue.Dead > 0 {
		for _, action := range teamQueueActions(team.Name, snapshot.ownedJobs, snapshot.queueItems) {
			add(appendDryRunFlag(action))
		}
	}
	if snapshot.Queue.Quarantined > 0 {
		for _, action := range queueQuarantineHealthActions(snapshot.Queue, team.Name, "") {
			add(action)
		}
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

func psOptionsHasSelectionFilters(opts psOptions) bool {
	return len(opts.statuses) > 0 || len(opts.runtimes) > 0 || len(opts.agents) > 0 || len(opts.phases) > 0 || len(opts.instances) > 0 || opts.stale || opts.unhealthy
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

func renderTeamGraph(w io.Writer, graph teamGraph, format pipelineGraphFormat, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(graph)
	}
	switch format {
	case pipelineGraphMermaid:
		renderTeamGraphMermaid(w, graph)
	case pipelineGraphDOT:
		renderTeamGraphDOT(w, graph)
	default:
		renderTeamGraphText(w, graph)
	}
	return nil
}

func renderTeamGraphText(w io.Writer, graph teamGraph) {
	fmt.Fprintf(w, "Team: %s\n", graph.Team.Name)
	if graph.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", graph.Team.Description)
	}
	if len(graph.Instances) == 0 {
		fmt.Fprintln(w, "Instances: -")
	} else {
		fmt.Fprintln(w, "Instances:")
		for _, inst := range graph.Instances {
			missing := ""
			if inst.Missing {
				missing = " missing=true"
			}
			fmt.Fprintf(w, "  %s agent=%s ephemeral=%t%s\n", inst.Name, emptyDash(inst.Agent), inst.Ephemeral, missing)
		}
	}
	if len(graph.Pipelines) == 0 {
		fmt.Fprintln(w, "Pipelines: -")
	} else {
		fmt.Fprintln(w, "Pipelines:")
		for _, pipeline := range graph.Pipelines {
			fmt.Fprintf(w, "  %s trigger=%s steps=%d\n", pipeline.Name, emptyDash(pipeline.Summary), len(pipeline.Nodes))
			for _, node := range pipeline.Nodes {
				after := "-"
				if len(node.After) > 0 {
					after = strings.Join(node.After, ",")
				}
				routes := ""
				if len(node.Routes) > 0 {
					routes = " routes=" + strings.Join(node.Routes, ",")
				}
				gate := ""
				if node.Gate != "" {
					gate = " gate=" + node.Gate
				}
				optional := ""
				if node.Optional {
					optional = " optional=true"
				}
				timeout := ""
				if node.Timeout != "" {
					timeout = " timeout=" + node.Timeout
				}
				maxAttempts := ""
				if node.MaxAttempts > 0 {
					maxAttempts = fmt.Sprintf(" max_attempts=%d", node.MaxAttempts)
				}
				missing := ""
				if node.Missing {
					missing = " missing=true"
				}
				fmt.Fprintf(w, "    %s target=%s after=%s%s%s%s%s%s%s\n", node.ID, emptyDash(node.Target), after, gate, optional, timeout, maxAttempts, routes, missing)
			}
		}
	}
	if len(graph.Schedules) == 0 {
		fmt.Fprintln(w, "Schedules: -")
	} else {
		fmt.Fprintln(w, "Schedules:")
		for _, schedule := range graph.Schedules {
			missing := ""
			if schedule.Missing {
				missing = " missing=true"
			}
			fmt.Fprintf(w, "  %s every=%s run_on_start=%t%s\n", schedule.Name, emptyDash(schedule.Every), schedule.RunOnStart, missing)
		}
	}
	if len(graph.Edges) == 0 {
		return
	}
	fmt.Fprintln(w, "Edges:")
	for _, edge := range graph.Edges {
		kind := ""
		if edge.Kind != "" {
			kind = " kind=" + edge.Kind
		}
		fmt.Fprintf(w, "  %s -> %s%s\n", edge.From, edge.To, kind)
	}
}

func renderTeamGraphMermaid(w io.Writer, graph teamGraph) {
	fmt.Fprintln(w, "flowchart TD")
	for _, node := range teamGraphNodeLabels(graph) {
		label := strings.ReplaceAll(node.Label, "\n", "<br/>")
		fmt.Fprintf(w, "  %s[%q]\n", pipelineGraphMermaidID(node.ID), pipelineMermaidLabel(label))
	}
	for _, edge := range graph.Edges {
		fmt.Fprintf(w, "  %s --> %s\n", pipelineGraphMermaidID(edge.From), pipelineGraphMermaidID(edge.To))
	}
}

func renderTeamGraphDOT(w io.Writer, graph teamGraph) {
	name := graph.Team.Name
	if name == "" {
		name = "team"
	}
	fmt.Fprintf(w, "digraph %q {\n", name)
	fmt.Fprintln(w, "  rankdir=TB;")
	for _, node := range teamGraphNodeLabels(graph) {
		fmt.Fprintf(w, "  %q [label=%q];\n", node.ID, node.Label)
	}
	for _, edge := range graph.Edges {
		fmt.Fprintf(w, "  %q -> %q", edge.From, edge.To)
		if edge.Kind != "" {
			fmt.Fprintf(w, " [label=%q]", edge.Kind)
		}
		fmt.Fprintln(w, ";")
	}
	fmt.Fprintln(w, "}")
}

type teamGraphLabel struct {
	ID    string
	Label string
}

func teamGraphNodeLabels(graph teamGraph) []teamGraphLabel {
	labels := []teamGraphLabel{{ID: "team:" + graph.Team.Name, Label: "team: " + graph.Team.Name}}
	for _, inst := range graph.Instances {
		parts := []string{"instance: " + inst.Name}
		if inst.Agent != "" {
			parts = append(parts, "agent: "+inst.Agent)
		}
		if inst.Ephemeral {
			parts = append(parts, "ephemeral")
		}
		if inst.Missing {
			parts = append(parts, "missing")
		}
		labels = append(labels, teamGraphLabel{ID: "instance:" + inst.Name, Label: strings.Join(parts, "\n")})
	}
	for _, pipeline := range graph.Pipelines {
		labels = append(labels, teamGraphLabel{ID: "pipeline:" + pipeline.Name, Label: "pipeline: " + pipeline.Name})
		labels = append(labels, teamGraphLabel{ID: "pipeline:" + pipeline.Name + ":trigger", Label: "trigger: " + emptyDash(pipeline.Summary)})
		for _, node := range pipeline.Nodes {
			labels = append(labels, teamGraphLabel{
				ID:    "pipeline:" + pipeline.Name + ":step:" + node.ID,
				Label: pipelineGraphNodeLabel(node, "\n"),
			})
		}
	}
	for _, schedule := range graph.Schedules {
		parts := []string{"schedule: " + schedule.Name}
		if schedule.Every != "" {
			parts = append(parts, "every: "+schedule.Every)
		}
		if schedule.RunOnStart {
			parts = append(parts, "run_on_start")
		}
		if schedule.Missing {
			parts = append(parts, "missing")
		}
		labels = append(labels, teamGraphLabel{ID: "schedule:" + schedule.Name, Label: strings.Join(parts, "\n")})
	}
	return labels
}

func parseTeamStatusFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("team-status-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderTeamStatus(w io.Writer, snapshot *teamStatusSnapshot, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	if tmpl != nil {
		return renderTeamStatusFormat(w, snapshot, tmpl)
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
	fmt.Fprintln(w, queueSummaryLine(snapshot.Queue))
	if snapshot.PipelineStatus != nil {
		fmt.Fprintf(w, "pipeline status: pipelines=%d jobs=%d ready_steps=%d manual_gates=%d stale_running_steps=%d failed_steps=%d\n",
			len(snapshot.PipelineStatus),
			countPipelineStatusJobs(snapshot.PipelineStatus),
			countPipelineStatusReadySteps(snapshot.PipelineStatus),
			countPipelineStatusManualGates(snapshot.PipelineStatus),
			countPipelineStatusStaleRunningSteps(snapshot.PipelineStatus),
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

func renderTeamStatusFormat(w io.Writer, snapshot *teamStatusSnapshot, tmpl *template.Template) error {
	if err := tmpl.Execute(w, snapshot); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderTeamHealth(w io.Writer, snapshot *teamHealthSnapshot, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	if tmpl != nil {
		return renderTeamHealthFormat(w, snapshot, tmpl)
	}
	fmt.Fprintf(w, "Team: %s\n", snapshot.Team.Name)
	if snapshot.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", snapshot.Team.Description)
	}
	renderHealth(w, snapshot.Health)
	return nil
}

func parseTeamHealthFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("team-health-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderTeamHealthFormat(w io.Writer, snapshot *teamHealthSnapshot, tmpl *template.Template) error {
	if err := tmpl.Execute(w, snapshot); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
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

func renderTeamPs(w io.Writer, rows []instanceRow, jsonOut bool, tmpl *template.Template) error {
	if tmpl != nil {
		return renderPsFormat(w, rows, tmpl)
	}
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

func renderTeamJobs(w io.Writer, teamDir string, jobs []*job.Job, jsonOut bool, tmpl *template.Template) error {
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
	renderJobTableWithRuntime(w, jobs, jobRuntimeMap(teamDir))
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
	return renderScheduleList(w, schedules, false, nil)
}
