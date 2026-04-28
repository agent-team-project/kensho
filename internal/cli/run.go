package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/spf13/cobra"
)

// runConfig is the parsed flags for `agent-team run`.
type runConfig struct {
	target string
	name   string
	prompt string
}

func newRunCmd() *cobra.Command {
	var cfg runConfig
	cwd, _ := os.Getwd()

	cmd := &cobra.Command{
		Use:   "run <agent> [-- <claude-args>...]",
		Short: "Launch a Claude Code session as the named agent.",
		Long: "Launch a Claude Code session as the named agent. The agent's prompt becomes " +
			"the system prompt; all other agents are still registered as subagents so this " +
			"agent can dispatch them. Pass `--name` to give the instance a unique identifier " +
			"(state dir: .agent_team/state/<name>/). Forward extra args to claude after `--`.",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: false,
		// Allow unknown trailing args after the agent name to be forwarded to claude.
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(cmd, cfg, args[0], args[1:])
		},
	}
	cmd.Flags().StringVar(&cfg.target, "target", cwd, "Repo root.")
	cmd.Flags().StringVarP(&cfg.name, "name", "n", "", "Instance name (defaults to the agent name). State dir: .agent_team/state/<name>/.")
	cmd.Flags().StringVarP(&cfg.prompt, "prompt", "p", "", "Kickoff message. With this, claude runs in one-shot mode; without, interactive.")
	return cmd
}

func runAgent(cmd *cobra.Command, cfg runConfig, agentName string, forwarded []string) error {
	target, err := filepath.Abs(cfg.target)
	if err != nil {
		return exitErr(2)
	}
	if eval, err := filepath.EvalSymlinks(target); err == nil {
		target = eval
	}
	teamDir := filepath.Join(target, loader.TeamDirName)
	if st, err := os.Stat(teamDir); err != nil || !st.IsDir() {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %s not found — run `agent-team init` first.\n", teamDir)
		return exitErr(2)
	}

	agents, err := loader.LoadAllAgents(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %s\n", err)
		return exitErr(1)
	}

	var chosen *loader.Agent
	for _, a := range agents {
		if a.Name == agentName {
			chosen = a
			break
		}
	}
	if chosen == nil {
		available := agentNames(agents)
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: agent `%s` not found. Available: %s\n", agentName, available)
		return exitErr(2)
	}

	skillPaths, err := loader.UnionSkills(agents)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %s\n", err)
		return exitErr(1)
	}

	instance := cfg.name
	if instance == "" {
		instance = agentName
	}
	stateDir := filepath.Join(teamDir, "state", instance)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	tmpdir, err := os.MkdirTemp("", "agent-team-")
	if err != nil {
		return fmt.Errorf("create tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpdir)

	skillsRoot := filepath.Join(tmpdir, ".claude", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return fmt.Errorf("create skills root: %w", err)
	}
	for sname, spath := range skillPaths {
		if err := os.Symlink(spath, filepath.Join(skillsRoot, sname)); err != nil {
			return fmt.Errorf("symlink skill %s: %w", sname, err)
		}
	}

	stateRel, err := filepath.Rel(target, stateDir)
	if err != nil {
		stateRel = stateDir
	}
	kickoff := fmt.Sprintf(
		"You are the `%s` instance of the `%s` agent.\n"+
			"Your state dir is `%s` (absolute: `%s`).\n\n"+
			"--- agent prompt ---\n\n%s",
		instance, agentName, filepath.ToSlash(stateRel), stateDir, chosen.Prompt,
	)
	promptFile := filepath.Join(tmpdir, "system_prompt.md")
	if err := os.WriteFile(promptFile, []byte(kickoff), 0o644); err != nil {
		return fmt.Errorf("write prompt file: %w", err)
	}

	agentsJSON, err := buildAgentsJSON(agents)
	if err != nil {
		return err
	}

	claudeArgs := []string{
		"--agents", agentsJSON,
		"--add-dir", tmpdir,
		"--append-system-prompt-file", promptFile,
	}
	if cfg.prompt != "" {
		claudeArgs = append(claudeArgs, "-p", cfg.prompt)
	}
	// Forwarded args may begin with `--` (the cobra delimiter); strip it.
	if len(forwarded) > 0 && forwarded[0] == "--" {
		forwarded = forwarded[1:]
	}
	claudeArgs = append(claudeArgs, forwarded...)

	env := append(os.Environ(),
		"AGENT_TEAM_ROOT="+teamDir,
		"AGENT_TEAM_INSTANCE="+instance,
		"AGENT_TEAM_STATE_DIR="+stateDir,
	)

	return execClaude(cmd, claudeArgs, env, target)
}

// execClaude is split out so tests can intercept the exec.
var execClaude = func(cmd *cobra.Command, args []string, env []string, cwd string) error {
	c := exec.Command("claude", args...)
	c.Env = env
	c.Dir = cwd
	c.Stdin = os.Stdin
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: `claude` CLI not found in PATH. Install Claude Code first.")
			return exitErr(127)
		}
		var exitErrTyped *exec.ExitError
		if errors.As(err, &exitErrTyped) {
			return exitErr(exitErrTyped.ExitCode())
		}
		return err
	}
	return nil
}

// buildAgentsJSON serialises {name: {description, prompt}, …} compactly.
func buildAgentsJSON(agents []*loader.Agent) (string, error) {
	type agentEntry struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	m := make(map[string]agentEntry, len(agents))
	for _, a := range agents {
		m[a.Name] = agentEntry{Description: a.Description, Prompt: a.Prompt}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encode agents JSON: %w", err)
	}
	return string(b), nil
}

func agentNames(agents []*loader.Agent) string {
	if len(agents) == 0 {
		return "(none)"
	}
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}

