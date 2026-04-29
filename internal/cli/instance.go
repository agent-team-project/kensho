package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/jamesaud/agent-team/internal/topology"
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
	cmd.AddCommand(newInstanceUpCmd())
	cmd.AddCommand(newInstanceDownCmd())
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
			printDeclaredTopologyEntry(cmd.OutOrStdout(), teamDir, name)

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

// newInstanceUpCmd implements `agent-team instance up [<name>...]`. With no
// args, it brings up every non-ephemeral declared instance from
// `instances.toml`. Idempotent — already-running instances are reported and
// skipped. Requires the daemon to be running.
func newInstanceUpCmd() *cobra.Command {
	var (
		target string
		prompt string
	)
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "up [<name>...]",
		Short: "Start declared persistent instances (idempotent). Requires the daemon.",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			topo, err := topology.LoadFromTeamDir(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(1)
			}
			if topo == nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: no instances.toml — nothing to bring up.")
				return exitErr(2)
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running — start it first with `agent-team daemon start`.")
				return exitErr(2)
			}
			targets, err := selectUpTargets(topo, args)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(2)
			}
			running, err := runningInstanceSet(dc)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(1)
			}
			out := cmd.OutOrStdout()
			for _, inst := range targets {
				if running[inst.Name] {
					fmt.Fprintf(out, "  skip   %-20s already running\n", inst.Name)
					continue
				}
				kickoff := prompt
				if kickoff == "" {
					kickoff = fmt.Sprintf("Topology bring-up: you are %q, an instance of %q.", inst.Name, inst.Agent)
				}
				if err := upOne(cmd, target, inst, kickoff); err != nil {
					fmt.Fprintf(out, "  error  %-20s %v\n", inst.Name, err)
					continue
				}
				fmt.Fprintf(out, "  start  %-20s %s\n", inst.Name, inst.Agent)
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	c.Flags().StringVar(&prompt, "prompt", "", "Override the default kickoff prompt.")
	return c
}

// selectUpTargets resolves CLI args to declared persistent instances. With
// no args, every non-ephemeral declared instance is selected.
func selectUpTargets(topo *topology.Topology, names []string) ([]*topology.Instance, error) {
	if len(names) == 0 {
		var out []*topology.Instance
		for _, inst := range topo.SortedInstances() {
			if !inst.Ephemeral {
				out = append(out, inst)
			}
		}
		if len(out) == 0 {
			return nil, errors.New("no non-ephemeral instances declared in instances.toml")
		}
		return out, nil
	}
	out := make([]*topology.Instance, 0, len(names))
	for _, n := range names {
		inst := topo.Find(n)
		if inst == nil {
			return nil, fmt.Errorf("instance %q is not declared in instances.toml", n)
		}
		if inst.Ephemeral {
			return nil, fmt.Errorf("instance %q is ephemeral — `instance up` only manages persistent instances", n)
		}
		out = append(out, inst)
	}
	return out, nil
}

// runningInstanceSet returns the set of instance names whose daemon-tracked
// status is StatusRunning.
func runningInstanceSet(dc *daemonClient) (map[string]bool, error) {
	list, err := dc.Instances()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, m := range list {
		if string(m.Status) == "running" {
			out[m.Instance] = true
		}
	}
	return out, nil
}

// upOne dispatches one declared instance. It reuses runAgent so the spawn
// path mirrors `agent-team run` exactly — same skill resolution, kickoff,
// declared-overrides folding, etc. We construct a minimal runConfig with
// --prompt set so runAgent routes through /v1/dispatch instead of fronting
// an interactive claude.
func upOne(cmd *cobra.Command, target string, inst *topology.Instance, kickoff string) error {
	if target == "" {
		cwd, _ := os.Getwd()
		target = cwd
	}
	cfg := runConfig{
		target: target,
		name:   inst.Name,
		prompt: kickoff,
	}
	return runAgent(cmd, cfg, inst.Agent, nil)
}

// newInstanceDownCmd implements `agent-team instance down [<name>...]`. With
// no args, stops every running declared persistent instance. Ephemerals are
// left alone (they exit on their own work-completion).
func newInstanceDownCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	c := &cobra.Command{
		Use:   "down [<name>...]",
		Short: "Stop declared persistent instances. With no args, stops all running.",
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			dc, err := newDaemonClient(teamDir)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running.")
				return exitErr(2)
			}
			running, err := runningInstanceSet(dc)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
				return exitErr(1)
			}
			var targets []string
			if len(args) == 0 {
				topo, err := topology.LoadFromTeamDir(teamDir)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
					return exitErr(1)
				}
				declared := map[string]bool{}
				if topo != nil {
					for _, inst := range topo.SortedInstances() {
						if !inst.Ephemeral {
							declared[inst.Name] = true
						}
					}
				}
				for name := range running {
					if declared[name] {
						targets = append(targets, name)
					}
				}
				sort.Strings(targets)
			} else {
				targets = args
			}
			out := cmd.OutOrStdout()
			if len(targets) == 0 {
				fmt.Fprintln(out, "(nothing to stop)")
				return nil
			}
			for _, name := range targets {
				if !running[name] {
					fmt.Fprintf(out, "  skip   %-20s not running\n", name)
					continue
				}
				if err := dc.StopInstance(name); err != nil {
					fmt.Fprintf(out, "  error  %-20s %v\n", name, err)
					continue
				}
				fmt.Fprintf(out, "  stop   %-20s\n", name)
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return c
}

// printDeclaredTopologyEntry prints the topology declaration for the named
// instance, if any. Helps `instance show <name>` reveal config overrides and
// triggers without a separate `topology show`.
func printDeclaredTopologyEntry(w fmtWriter, teamDir, name string) {
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil || topo == nil {
		return
	}
	inst := topo.Find(name)
	if inst == nil {
		return
	}
	fmt.Fprintln(w, "topology:")
	fmt.Fprintf(w, "  agent:     %s\n", inst.Agent)
	fmt.Fprintf(w, "  ephemeral: %v\n", inst.Ephemeral)
	if inst.Ephemeral {
		fmt.Fprintf(w, "  replicas:  %d\n", inst.Replicas)
	}
	if inst.Description != "" {
		fmt.Fprintf(w, "  description: %s\n", inst.Description)
	}
	if len(inst.Config) > 0 {
		fmt.Fprintln(w, "  config overrides:")
		for k, v := range flattenForPrint(inst.Config, "") {
			fmt.Fprintf(w, "    %s = %v\n", k, v)
		}
	}
	if len(inst.Triggers) > 0 {
		fmt.Fprintln(w, "  triggers:")
		for _, t := range inst.Triggers {
			matchSummary := summariseLocalMatch(t.Match)
			fmt.Fprintf(w, "    - %s%s\n", t.Event, matchSummary)
		}
	}
	fmt.Fprintln(w)
}

func flattenForPrint(t map[string]any, prefix string) map[string]any {
	out := map[string]any{}
	for k, v := range t {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if m, ok := v.(map[string]any); ok {
			for kk, vv := range flattenForPrint(m, key) {
				out[kk] = vv
			}
			continue
		}
		out[key] = v
	}
	return out
}

func summariseLocalMatch(match map[string]topology.MatchValue) string {
	if len(match) == 0 {
		return ""
	}
	parts := make([]string, 0, len(match))
	keys := make([]string, 0, len(match))
	for k := range match {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		mv := match[k]
		if mv.Single != "" {
			parts = append(parts, fmt.Sprintf("%s=%q", k, mv.Single))
		} else if len(mv.List) > 0 {
			parts = append(parts, fmt.Sprintf("%s∈%v", k, mv.List))
		}
	}
	return " (" + strings.Join(parts, ", ") + ")"
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
