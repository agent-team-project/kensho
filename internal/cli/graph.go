package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newGraphCmd() *cobra.Command {
	var (
		repo          string
		graphFormat   string
		includeRoutes bool
		jsonOut       bool
		jobID         string
		commands      bool
		teamName      string
		pipelineName  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "graph [team-or-pipeline]",
		Short: "Render the automation graph.",
		Long: "Render a read-only graph of the repo automation model. By default this shows the full topology; " +
			"pass a team or pipeline name, or use --team or --pipeline, to narrow to one declared workflow owner.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if teamName != "" && pipelineName != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team graph: choose at most one of --team or --pipeline.")
				return exitErr(2)
			}
			selector := ""
			if len(args) == 1 {
				selector = strings.TrimSpace(args[0])
				if selector == "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team graph: selector cannot be blank.")
					return exitErr(2)
				}
				if teamName != "" || pipelineName != "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team graph: positional selector cannot be combined with --team or --pipeline.")
					return exitErr(2)
				}
			}
			if jsonOut && cmd.Flags().Changed("format") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team graph: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team graph: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && cmd.Flags().Changed("format") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team graph: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			format, err := parsePipelineGraphFormat(graphFormat)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team graph: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if selector != "" {
				inferredTeam, inferredPipeline, err := inferGraphSelector(teamDir, selector)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team graph: %v\n", err)
					return exitErr(2)
				}
				teamName = inferredTeam
				pipelineName = inferredPipeline
			}
			scope := operatorCommandScopeFromCommand(cmd, repo, "repo")
			switch {
			case teamName != "":
				graph, err := collectTeamGraph(teamDir, teamName, includeRoutes, jobID)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team graph: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderTeamGraphCommands(cmd.OutOrStdout(), graph, scope)
				}
				return renderTeamGraph(cmd.OutOrStdout(), graph, format, jsonOut)
			case pipelineName != "":
				graph, err := collectPipelineGraph(teamDir, pipelineName, includeRoutes, jobID)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team graph: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderPipelineGraphCommands(cmd.OutOrStdout(), graph, scope)
				}
				return renderPipelineGraph(cmd.OutOrStdout(), graph, format, jsonOut)
			default:
				graph, err := collectTopologyGraph(teamDir, includeRoutes, jobID)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team graph: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderTopologyGraphCommands(cmd.OutOrStdout(), graph, scope)
				}
				return renderTopologyGraph(cmd.OutOrStdout(), graph, format, jsonOut)
			}
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&graphFormat, "format", "text", "Graph output format: text, mermaid, or dot.")
	cmd.Flags().BoolVar(&includeRoutes, "routes", false, "Annotate pipeline steps with matching agent.dispatch route instances.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit graph nodes and edges as JSON.")
	cmd.Flags().StringVar(&jobID, "job", "", "Overlay durable job step state on declared pipeline graphs.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended commands from graph action hints, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().StringVar(&teamName, "team", "", "Render one declared team graph instead of the full topology graph.")
	cmd.Flags().StringVar(&pipelineName, "pipeline", "", "Render one declared pipeline graph instead of the full topology graph.")
	return cmd
}

func inferGraphSelector(teamDir, selector string) (string, string, error) {
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return "", "", err
	}
	_, isTeam := top.Teams[selector]
	_, isPipeline := top.Pipelines[selector]
	switch {
	case isTeam && isPipeline:
		return "", "", fmt.Errorf("selector %q matches both a team and pipeline; use --team or --pipeline", selector)
	case isTeam:
		return selector, "", nil
	case isPipeline:
		return "", selector, nil
	default:
		return "", "", fmt.Errorf("selector %q is not a declared team or pipeline", selector)
	}
}
