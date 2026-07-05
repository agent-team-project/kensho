package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

func newInstanceBriefCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "brief <name>",
		Short: "Generate a recoverable catch-up brief for an instance.",
		Long: "Generate a recoverable catch-up brief for an instance from daemon-owned state: " +
			"identity, jobs, mailbox, channel cursors, lifecycle events, and fleet rows. " +
			"The brief is written to .agent_team/state/<name>/brief.md and printed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			brief, err := daemon.GenerateAndWriteInstanceBrief(teamDir, name, daemon.BriefOptions{})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team instance brief: %v\n", err)
				return exitErr(1)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(brief)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), brief.Text)
			return err
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the generated brief as structured JSON.")
	return cmd
}
