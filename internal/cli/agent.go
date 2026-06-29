package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"

	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/spf13/cobra"
)

type agentInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Summary     string   `json:"summary"`
	Runtime     string   `json:"runtime,omitempty"`
	RuntimeBin  string   `json:"runtime_bin,omitempty"`
	Skills      []string `json:"skills,omitempty"`
	Subscribes  []string `json:"subscribes,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
}

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent",
		Aliases: []string{"agents"},
		Short:   "List and inspect runnable agent definitions.",
		Long:    "List and inspect runnable agent definitions loaded from .agent_team/agents.",
	}
	cmd.AddCommand(newAgentLsCmd())
	cmd.AddCommand(newAgentShowCmd())
	return cmd
}

func newAgentLsCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List runnable agent definitions.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team agent ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseAgentFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team agent ls: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			infos, err := loadAgentInfos(teamDir, false)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team agent ls: %v\n", err)
				return exitErr(1)
			}
			return renderAgentList(cmd.OutOrStdout(), infos, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit agents as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each agent with a Go template, e.g. '{{.Name}} {{len .Skills}}'.")
	return cmd
}

func newAgentShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <agent>",
		Short: "Show one runnable agent definition.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team agent show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseAgentFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team agent show: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			info, err := loadAgentInfo(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team agent show: %v\n", err)
				return exitErr(1)
			}
			return renderAgentDetail(cmd.OutOrStdout(), info, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the agent as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the agent with a Go template, e.g. '{{.Name}} {{.Summary}}'.")
	return cmd
}

func loadAgentInfos(teamDir string, includePrompt bool) ([]agentInfo, error) {
	agents, err := loader.LoadAllAgents(teamDir)
	if err != nil {
		return nil, err
	}
	infos := make([]agentInfo, 0, len(agents))
	for _, agent := range agents {
		infos = append(infos, agentInfoFromLoaded(agent, includePrompt))
	}
	return infos, nil
}

func loadAgentInfo(teamDir, name string) (agentInfo, error) {
	infos, err := loadAgentInfos(teamDir, true)
	if err != nil {
		return agentInfo{}, err
	}
	for _, info := range infos {
		if info.Name == name {
			return info, nil
		}
	}
	return agentInfo{}, fmt.Errorf("agent %q not found", name)
}

func agentInfoFromLoaded(agent *loader.Agent, includePrompt bool) agentInfo {
	skills := make([]string, 0, len(agent.Skills))
	for name := range agent.Skills {
		skills = append(skills, name)
	}
	sort.Strings(skills)

	subscribes := append([]string(nil), agent.Subscribes...)
	sort.Strings(subscribes)

	info := agentInfo{
		Name:        agent.Name,
		Description: strings.TrimSpace(agent.Description),
		Summary:     agentSummary(agent.Description),
		Runtime:     strings.TrimSpace(agent.Runtime),
		RuntimeBin:  strings.TrimSpace(agent.RuntimeBin),
		Skills:      skills,
		Subscribes:  subscribes,
	}
	if includePrompt {
		info.Prompt = strings.TrimSpace(agent.Prompt)
	}
	return info
}

func renderAgentList(w io.Writer, agents []agentInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(agents)
	}
	if tmpl != nil {
		for _, agent := range agents {
			if err := renderAgentFormat(w, agent, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(agents) == 0 {
		fmt.Fprintln(w, "(no agents installed)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tDESCRIPTION\tRUNTIME\tSKILLS")
	for _, agent := range agents {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", agent.Name, agent.Summary, agentRuntimeSummary(agent), strings.Join(agent.Skills, ", "))
	}
	_ = tw.Flush()
	return nil
}

func renderAgentDetail(w io.Writer, agent agentInfo, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(agent)
	}
	if tmpl != nil {
		return renderAgentFormat(w, agent, tmpl)
	}
	fmt.Fprintf(w, "Agent:       %s\n", agent.Name)
	fmt.Fprintf(w, "Description: %s\n", agent.Summary)
	fmt.Fprintf(w, "Runtime:     %s\n", emptyDash(agent.Runtime))
	fmt.Fprintf(w, "Runtime bin: %s\n", emptyDash(agent.RuntimeBin))
	fmt.Fprintf(w, "Skills:      %s\n", summariseStringList(agent.Skills))
	fmt.Fprintf(w, "Subscribes:  %s\n", summariseStringList(agent.Subscribes))
	if strings.TrimSpace(agent.Prompt) != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, agent.Prompt)
	}
	return nil
}

func parseAgentFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("agent-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderAgentFormat(w io.Writer, agent agentInfo, tmpl *template.Template) error {
	if err := tmpl.Execute(w, agent); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func agentSummary(description string) string {
	description = strings.ReplaceAll(description, "\r\n", "\n")
	paragraphs := strings.Split(description, "\n\n")
	for _, paragraph := range paragraphs {
		line := strings.TrimSpace(strings.Join(strings.Fields(paragraph), " "))
		if line != "" {
			return line
		}
	}
	return ""
}

func agentRuntimeSummary(agent agentInfo) string {
	runtime := strings.TrimSpace(agent.Runtime)
	if runtime == "" {
		return "-"
	}
	runtimeBin := strings.TrimSpace(agent.RuntimeBin)
	if runtimeBin == "" {
		return runtime
	}
	return runtime + " (" + runtimeBin + ")"
}

func summariseStringList(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}
