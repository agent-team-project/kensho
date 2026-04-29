package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/spf13/cobra"
)

func newInstanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instance",
		Short: "Manage agent instance state (.agent_team/state/<instance>/).",
	}
	cmd.AddCommand(newInstanceLsCmd())
	cmd.AddCommand(newInstancePsCmd())
	cmd.AddCommand(newInstanceShowCmd())
	cmd.AddCommand(newInstanceRmCmd())
	return cmd
}

func newInstanceLsCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "ls",
		Short: "List instances (state dirs).",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			stateRoot := filepath.Join(teamDir, "state")
			st, err := os.Stat(stateRoot)
			if err != nil || !st.IsDir() {
				fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
				return nil
			}
			entries, err := os.ReadDir(stateRoot)
			if err != nil {
				return err
			}
			var names []string
			for _, e := range entries {
				if e.IsDir() {
					names = append(names, e.Name())
				}
			}
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no instances)")
				return nil
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return c
}

func newInstanceShowCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "show <name>",
		Short: "Show an instance's state files.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			name := args[0]
			stateDir := filepath.Join(teamDir, "state", name)
			st, err := os.Stat(stateDir)
			if err != nil || !st.IsDir() {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: instance not found: %s\n", stateDir)
				return exitErr(2)
			}
			rel, err := filepath.Rel(filepath.Dir(teamDir), stateDir)
			if err != nil {
				rel = stateDir
			}
			fmt.Fprintf(cmd.OutOrStdout(), "instance: %s\n", name)
			fmt.Fprintf(cmd.OutOrStdout(), "path:     %s/\n\n", filepath.ToSlash(rel))

			printInstanceStatus(cmd.OutOrStdout(), stateDir, time.Now())

			entries, err := os.ReadDir(stateDir)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(empty)")
				return nil
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			fmt.Fprintln(cmd.OutOrStdout(), "files:")
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				if e.IsDir() {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s/  (dir)\n", e.Name())
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s  (%d bytes)\n", e.Name(), info.Size())
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return c
}

func newInstanceRmCmd() *cobra.Command {
	var (
		target string
		force  bool
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an instance's state.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			name := args[0]
			stateDir := filepath.Join(teamDir, "state", name)
			st, err := os.Stat(stateDir)
			if err != nil || !st.IsDir() {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: instance not found: %s\n", stateDir)
				return exitErr(2)
			}

			if !force {
				ok, err := confirm(cmd, fmt.Sprintf("Remove %s?", stateDir))
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(cmd.OutOrStdout(), "(aborted)")
					return nil
				}
			}

			if err := os.RemoveAll(stateDir); err != nil {
				return err
			}
			rel, err := filepath.Rel(filepath.Dir(teamDir), stateDir)
			if err != nil {
				rel = stateDir
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  removed %s\n", filepath.ToSlash(rel))
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation.")
	return c
}

// resolveTeamDir resolves cfg.target into the absolute .agent_team/ path,
// emitting a stderr message and ExitCode(2) if missing — matches the Python
// helper of the same name.
func resolveTeamDir(cmd *cobra.Command, target string) (string, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", exitErr(2)
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	teamDir := filepath.Join(abs, loader.TeamDirName)
	st, err := os.Stat(teamDir)
	if err != nil || !st.IsDir() {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %s not found — run `agent-team init` first.\n", teamDir)
		return "", exitErr(2)
	}
	return teamDir, nil
}

// confirm reads a yes/no answer from cmd.InOrStdin(). Returns true on y/yes
// (case-insensitive), false on n/no/empty/EOF. Default-no.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N]: ", prompt)
	r := bufio.NewReader(cmd.InOrStdin())
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
}
