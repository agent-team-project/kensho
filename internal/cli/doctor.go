package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Sanity-check the vendored team.",
		Long: "Sanity-check the vendored team: .agent_team/ layout, config.toml validity, " +
			"each agent's frontmatter, and skill resolution across all agents.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return cmd
}

func runDoctor(cmd *cobra.Command, target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return exitErr(2)
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	teamDir := filepath.Join(abs, loader.TeamDirName)

	var problems []string

	if st, err := os.Stat(teamDir); err != nil || !st.IsDir() {
		problems = append(problems, fmt.Sprintf("%s not found — run `agent-team init` first.", teamDir))
		return reportDoctor(cmd, problems)
	}

	cfgPath := filepath.Join(teamDir, "config.toml")
	if st, err := os.Stat(cfgPath); err != nil || st.IsDir() {
		problems = append(problems, fmt.Sprintf("%s missing — copy config.toml.example and fill it in.", cfgPath))
	} else {
		var cfg map[string]any
		if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
			problems = append(problems, fmt.Sprintf("%s is not valid TOML: %v", cfgPath, err))
		} else {
			team, _ := cfg["team"].(map[string]any)
			if pmTool, _ := team["pm_tool"].(string); pmTool == "linear" {
				linear, _ := cfg["linear"].(map[string]any)
				for _, k := range []string{"team_id", "ticket_prefix"} {
					v, _ := linear[k].(string)
					if v == "" {
						problems = append(problems, fmt.Sprintf("[linear].%s missing/empty in %s", k, cfgPath))
					}
				}
			}
		}
	}

	agentsDir := filepath.Join(teamDir, "agents")
	if st, err := os.Stat(agentsDir); err != nil || !st.IsDir() {
		problems = append(problems, fmt.Sprintf("%s missing — re-run `agent-team init`.", agentsDir))
	} else {
		entries, _ := os.ReadDir(agentsDir)
		var dirs []string
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(agentsDir, e.Name()))
			}
		}
		if len(dirs) == 0 {
			problems = append(problems, fmt.Sprintf("no agents under %s — `agent-team agent create <name>` to scaffold one.", agentsDir))
		} else {
			sort.Strings(dirs)
			loaded := make([]*loader.Agent, 0, len(dirs))
			for _, d := range dirs {
				a, err := loader.LoadAgent(d, teamDir)
				if err != nil {
					problems = append(problems, err.Error())
					continue
				}
				loaded = append(loaded, a)
			}
			if len(loaded) > 0 {
				if _, err := loader.UnionSkills(loaded); err != nil {
					problems = append(problems, err.Error())
				}
			}
		}
	}

	return reportDoctor(cmd, problems)
}

func reportDoctor(cmd *cobra.Command, problems []string) error {
	if len(problems) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "agent-team doctor: OK")
		return nil
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "agent-team doctor: problems found:")
	for _, p := range problems {
		fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", p)
	}
	return exitErr(1)
}
