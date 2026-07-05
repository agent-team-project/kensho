package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/agent-team-project/agent-team/internal/loader"
	"github.com/spf13/cobra"
)

func newAgentDoctorCmd() *cobra.Command {
	var (
		repo          string
		all           bool
		strict        bool
		strictRuntime bool
		jsonOut       bool
		commands      bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor [agent]",
		Short: "Validate installed agent definitions.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && all {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team agent doctor: <agent> cannot be combined with --all.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team agent doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team agent doctor: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team agent doctor: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseAgentDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team agent doctor: %v\n", err)
				return exitErr(2)
			}
			if strict {
				strictRuntime = true
			}
			strictActionFlag := scopedDoctorStrictActionFlag(strict, strictRuntime)
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			result := collectAgentDoctor(teamDir, name)
			if strictRuntime {
				promoteAgentDoctorRuntimeWarnings(result)
			}
			return renderAgentDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, commands, strictRuntime, strictActionFlag, tmpl, operatorCommandScopeFromCommand(cmd, repo, "repo"))
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Validate all installed agents. This is the default when no agent is passed.")
	cmd.Flags().BoolVar(&strict, "strict", false, "Fail on all strict agent doctor checks. Currently aliases --strict-runtime.")
	cmd.Flags().BoolVar(&strictRuntime, "strict-runtime", false, "Fail when an agent runtime default cannot be resolved or is not discoverable.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit agent doctor findings as JSON.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&format, "format", "", "Render the agent doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.")
	return cmd
}

type agentDoctorResult struct {
	OK       bool                 `json:"ok"`
	Agents   []agentDoctorAgent   `json:"agents"`
	Actions  []string             `json:"actions,omitempty"`
	Problems []agentDoctorFinding `json:"problems,omitempty"`
	Warnings []agentDoctorFinding `json:"warnings,omitempty"`
}

type agentDoctorAgent struct {
	Name     string               `json:"name"`
	OK       bool                 `json:"ok"`
	Problems []agentDoctorFinding `json:"problems,omitempty"`
	Warnings []agentDoctorFinding `json:"warnings,omitempty"`
}

type agentDoctorFinding struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Agent      string `json:"agent,omitempty"`
	Runtime    string `json:"runtime,omitempty"`
	RuntimeBin string `json:"runtime_bin,omitempty"`
}

func collectAgentDoctor(teamDir, agentName string) *agentDoctorResult {
	result := &agentDoctorResult{OK: true}
	dirs, err := agentDoctorDirs(teamDir, agentName)
	if err != nil {
		result.Problems = append(result.Problems, agentDoctorFinding{
			Code:    "agents_unavailable",
			Message: err.Error(),
		})
		result.OK = false
		return result
	}
	if len(dirs) == 0 {
		result.Warnings = append(result.Warnings, agentDoctorFinding{
			Code:    "no_agents",
			Message: "no agents are installed",
		})
		return result
	}
	for _, dir := range dirs {
		name := filepath.Base(dir)
		report := agentDoctorAgent{Name: name, OK: true}
		agent, err := loader.LoadAgent(dir, teamDir)
		if err != nil {
			report.Problems = append(report.Problems, agentDoctorFinding{
				Code:    "agent_load_failed",
				Message: err.Error(),
				Agent:   name,
			})
		} else {
			report.Name = agent.Name
			report.Warnings = append(report.Warnings, agentRuntimeFindings(teamDir, agent)...)
		}
		report.OK = len(report.Problems) == 0
		result.Agents = append(result.Agents, report)
		result.Problems = append(result.Problems, report.Problems...)
		result.Warnings = append(result.Warnings, report.Warnings...)
	}
	result.OK = len(result.Problems) == 0
	return result
}

func agentDoctorDirs(teamDir, agentName string) ([]string, error) {
	agentsDir := filepath.Join(teamDir, "agents")
	agentName = strings.TrimSpace(agentName)
	if agentName != "" {
		return []string{filepath.Join(agentsDir, agentName)}, nil
	}
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil, fmt.Errorf("%s not found", agentsDir)
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(agentsDir, entry.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func agentRuntimeFindings(teamDir string, agent *loader.Agent) []agentDoctorFinding {
	if agent == nil {
		return nil
	}
	runtime := strings.TrimSpace(agent.Runtime)
	runtimeBin := strings.TrimSpace(agent.RuntimeBin)
	if runtime == "" && runtimeBin == "" {
		return nil
	}
	base := agentDoctorFinding{
		Agent:      agent.Name,
		Runtime:    runtime,
		RuntimeBin: runtimeBin,
	}
	if runtime == "" {
		base.Code = "agent_runtime_bin_ignored"
		base.Message = fmt.Sprintf("agent %q declares runtime_bin %q without runtime; agent-level runtime_bin is ignored until runtime is set", agent.Name, runtimeBin)
		return []agentDoctorFinding{base}
	}
	info, err := collectRuntimeInfoForConfigWithSelection(filepath.Join(teamDir, "config.toml"), runtimeSelection{Kind: runtime, Binary: runtimeBin})
	if err != nil {
		base.Code = "agent_runtime_invalid"
		base.Message = fmt.Sprintf("agent %q runtime default could not be resolved: %v", agent.Name, err)
		return []agentDoctorFinding{base}
	}
	if !info.Available {
		base.Code = "agent_runtime_unavailable"
		base.Message = fmt.Sprintf("agent %q defaults to runtime %q with binary %q, but that binary was not found in PATH", agent.Name, info.Runtime, info.Binary)
		base.Runtime = info.Runtime
		base.RuntimeBin = info.Binary
		return []agentDoctorFinding{base}
	}
	return nil
}

func promoteAgentDoctorRuntimeWarnings(result *agentDoctorResult) {
	if result == nil {
		return
	}
	result.Problems, result.Warnings = promoteAgentRuntimeFindings(result.Problems, result.Warnings)
	for i := range result.Agents {
		agent := &result.Agents[i]
		agent.Problems, agent.Warnings = promoteAgentRuntimeFindings(agent.Problems, agent.Warnings)
		agent.OK = len(agent.Problems) == 0
	}
	result.OK = len(result.Problems) == 0
}

func promoteAgentRuntimeFindings(problems, warnings []agentDoctorFinding) ([]agentDoctorFinding, []agentDoctorFinding) {
	if len(warnings) == 0 {
		return problems, warnings
	}
	nextProblems := append([]agentDoctorFinding(nil), problems...)
	nextWarnings := make([]agentDoctorFinding, 0, len(warnings))
	for _, warning := range warnings {
		if agentRuntimeFindingIsStrict(warning) {
			nextProblems = append(nextProblems, warning)
			continue
		}
		nextWarnings = append(nextWarnings, warning)
	}
	return nextProblems, nextWarnings
}

func agentRuntimeFindingIsStrict(finding agentDoctorFinding) bool {
	switch strings.TrimSpace(finding.Code) {
	case "agent_runtime_invalid", "agent_runtime_unavailable":
		return true
	default:
		return false
	}
}

func scopedDoctorStrictActionFlag(strict, strictRuntime bool) string {
	if strict {
		return "--strict"
	}
	return strictRuntimeActionFlag(strictRuntime)
}

func strictRuntimeActionFlag(strictRuntime bool) string {
	if strictRuntime {
		return "--strict-runtime"
	}
	return ""
}

func renderAgentDoctor(stdout, stderr io.Writer, result *agentDoctorResult, jsonOut, commands, strictRuntime bool, strictActionFlag string, tmpl *template.Template, scope operatorCommandScope) error {
	if result == nil {
		result = &agentDoctorResult{OK: true}
	}
	result.Actions = agentDoctorActionsWithFlag(result, strictActionFlag)
	if jsonOut {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	if commands {
		if err := renderOperatorActionCommands(stdout, result.Actions, scope); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	if tmpl != nil {
		if err := renderAgentDoctorFormat(stdout, result, tmpl); err != nil {
			return err
		}
		if !result.OK {
			return exitErr(1)
		}
		return nil
	}
	label := "agents"
	if len(result.Agents) == 1 {
		label = result.Agents[0].Name
	}
	if result.OK {
		if len(result.Agents) == 1 {
			fmt.Fprintf(stdout, "agent-team agent doctor: OK (%s)\n", label)
		} else {
			fmt.Fprintf(stdout, "agent-team agent doctor: OK (%d agents)\n", len(result.Agents))
		}
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
		}
		return nil
	}
	if len(result.Agents) == 1 {
		fmt.Fprintf(stderr, "agent-team agent doctor: problems found for %s:\n", label)
	} else {
		fmt.Fprintln(stderr, "agent-team agent doctor: problems found:")
	}
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "  - %s\n", problem.Message)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
	}
	return exitErr(1)
}

func agentDoctorActions(result *agentDoctorResult, strictRuntime bool) []string {
	return agentDoctorActionsWithFlag(result, strictRuntimeActionFlag(strictRuntime))
}

func agentDoctorActionsWithFlag(result *agentDoctorResult, strictActionFlag string) []string {
	if result == nil {
		return nil
	}
	seen := map[string]struct{}{}
	add := func(agent string) {
		agent = strings.TrimSpace(agent)
		if agent != "" {
			seen[agent] = struct{}{}
		}
	}
	for _, report := range result.Agents {
		if !report.OK || len(report.Problems) > 0 || len(report.Warnings) > 0 {
			add(report.Name)
		}
	}
	for _, finding := range result.Problems {
		add(finding.Agent)
	}
	for _, finding := range result.Warnings {
		add(finding.Agent)
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	actions := make([]string, 0, len(names)*2)
	for _, name := range names {
		actions = append(actions, agentDoctorDetailActionWithFlag(name, strictActionFlag), strings.Join(shellQuoteArgs([]string{"agent-team", "agent", "show", name, "--json"}), " "))
	}
	if len(actions) == 0 && len(result.Warnings) > 0 {
		actions = append(actions, strings.Join(shellQuoteArgs([]string{"agent-team", "agent", "ls"}), " "))
	}
	return actions
}

func agentDoctorDetailAction(name string, strictRuntime bool) string {
	return agentDoctorDetailActionWithFlag(name, strictRuntimeActionFlag(strictRuntime))
}

func agentDoctorDetailActionWithFlag(name, strictActionFlag string) string {
	args := []string{"agent-team", "agent", "doctor"}
	name = strings.TrimSpace(name)
	if name == "" {
		args = append(args, "--all")
	} else {
		args = append(args, name)
	}
	if strictActionFlag != "" {
		args = append(args, strictActionFlag)
	}
	args = append(args, "--json")
	return strings.Join(shellQuoteArgs(args), " ")
}

func parseAgentDoctorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("agent-doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderAgentDoctorFormat(w io.Writer, result *agentDoctorResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
