package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/jamesaud/agent-team/internal/template"
	"github.com/spf13/cobra"
)

// runConfig is the parsed flags for `agent-team run`.
type runConfig struct {
	target         string
	name           string
	prompt         string
	setStrings     []string
	instanceConfig string
	noDaemon       bool
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
	cmd.Flags().StringArrayVar(&cfg.setStrings, "set", nil, "Override a config value for this spawn, e.g. --set linear.team_id=<x>. Repeatable.")
	cmd.Flags().StringVar(&cfg.instanceConfig, "instance-config", "", "Path to a per-instance TOML config that layers on top of repo config (below --set).")
	cmd.Flags().BoolVar(&cfg.noDaemon, "no-daemon", false, "Bypass the daemon: exec claude directly even if the daemon is running. Useful for debugging.")
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

	// Resolve the config tree: repo `config.toml` ← per-instance ← --set.
	// The merged result is written to <stateDir>/config.toml so the spawned
	// session reads exactly the config its caller intended.
	resolved, err := resolveRunConfig(teamDir, stateDir, cfg)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	if err := writeStateConfig(stateDir, resolved); err != nil {
		return fmt.Errorf("write state config: %w", err)
	}
	// Open question #5 (documentation/templates.md): render any `.tmpl`
	// files in the team tree against the resolved config and drop them
	// under <stateDir>/rendered/ so a `--set` flag is reflected in the
	// per-spawn rendered tree even though the in-tree files were rendered
	// at init time. Skills that need fresh substitution can read from
	// $AGENT_TEAM_STATE_DIR/rendered/<rel-path>.
	if err := rerenderTmplFiles(teamDir, stateDir, resolved); err != nil {
		return fmt.Errorf("re-render .tmpl files: %w", err)
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

	teamEnv := []string{
		"AGENT_TEAM_ROOT=" + teamDir,
		"AGENT_TEAM_INSTANCE=" + instance,
		"AGENT_TEAM_STATE_DIR=" + stateDir,
	}
	env := append(os.Environ(), teamEnv...)

	// Daemon-aware routing: for one-shot dispatches (--prompt given), route
	// through the daemon when one is running. Interactive sessions stay
	// direct — the daemon spawns claude headless against a log file, which
	// is incompatible with an attached terminal.
	if !cfg.noDaemon && cfg.prompt != "" {
		if dc, err := newDaemonClient(teamDir); err == nil {
			// claudeArgs already starts with --agents/--add-dir/.../-p; the
			// daemon prepends `claude --session-id <uuid>` so we strip nothing
			// — the daemon's spawn surface accepts arbitrary trailing argv
			// via DispatchInput.Args.
			disp, derr := dc.Dispatch(dispatchPayload{
				Agent:     agentName,
				Name:      instance,
				Prompt:    cfg.prompt,
				Workspace: target,
				Args:      claudeArgs,
				Env:       teamEnv,
			})
			if derr != nil {
				return fmt.Errorf("daemon dispatch: %w", derr)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"agent-team: dispatched %s via daemon (pid=%d, session=%s)\n",
				disp.InstanceID, disp.PID, disp.SessionID)
			fmt.Fprintf(cmd.OutOrStdout(),
				"  follow: agent-team logs %s --follow\n", disp.InstanceID)
			return nil
		}
		// Daemon not running → fall through to direct exec.
	}

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

// resolveRunConfig builds the resolved instance config from layered sources:
//   1. repo config (`<teamDir>/config.toml`)
//   2. per-instance config — either `--instance-config <path>` if given, or
//      the auto-pickup at `<stateDir>/config.toml` from a previous run/edit
//   3. CLI `--set` flags
//
// The merged tree is the single source of truth for the spawned session's
// skills and bash steps.
func resolveRunConfig(teamDir, stateDir string, cfg runConfig) (template.Tree, error) {
	repoCfg, err := template.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		return nil, fmt.Errorf("repo config: %w", err)
	}
	var instanceCfg template.Tree
	switch {
	case cfg.instanceConfig != "":
		instanceCfg, err = template.LoadTOMLFile(cfg.instanceConfig)
		if err != nil {
			return nil, fmt.Errorf("--instance-config %s: %w", cfg.instanceConfig, err)
		}
	default:
		// Pick up <stateDir>/config.toml if the user has previously committed
		// per-instance settings there. Missing → empty tree.
		instanceCfg, err = template.LoadTOMLFile(filepath.Join(stateDir, "config.toml"))
		if err != nil {
			return nil, fmt.Errorf("instance config: %w", err)
		}
	}
	merged := template.ResolveLayers(repoCfg, instanceCfg)

	sets, err := template.ParseSetSpecs(cfg.setStrings)
	if err != nil {
		return nil, err
	}
	// `run` does not have direct access to the template manifest (the consumer
	// may have edited the resolved config beyond what the manifest declares).
	// We pass nil here, which means string-only coercion for --set values.
	withSets, err := template.ApplySets(merged, sets, nil)
	if err != nil {
		return nil, err
	}
	return withSets, nil
}

// writeStateConfig writes the resolved tree to <stateDir>/config.toml.
// Any pre-existing per-instance config has already been folded in at this
// point, so overwriting is safe.
func writeStateConfig(stateDir string, resolved template.Tree) error {
	body, err := template.EncodeTOML(resolved)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, "config.toml"), body, 0o644)
}

// rerenderTmplFiles walks teamDir for any `.tmpl` files and renders each one
// against `resolved`, writing the output under <stateDir>/rendered/<rel-path>
// with the suffix stripped. Files without `.tmpl` are ignored (re-render is
// purely additive — they were already verbatim-copied at init).
//
// This is option (a) from `documentation/templates.md` § Open questions #5:
// the per-spawn render gives `--set` overrides somewhere to land for skills
// that consume parameter-substituted files.
func rerenderTmplFiles(teamDir, stateDir string, resolved template.Tree) error {
	renderRoot := filepath.Join(stateDir, "rendered")
	// Always recreate so stale renders from a previous spawn don't persist.
	if err := os.RemoveAll(renderRoot); err != nil {
		return err
	}
	hasTmpl := false
	err := filepath.WalkDir(teamDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip the state tree itself to avoid feedback loops.
			if p == filepath.Join(teamDir, "state") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, template.TmplSuffix) {
			return nil
		}
		hasTmpl = true
		rel, err := filepath.Rel(teamDir, p)
		if err != nil {
			return err
		}
		dstRel := strings.TrimSuffix(rel, template.TmplSuffix)
		dst := filepath.Join(renderRoot, dstRel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out, err := template.RenderBytes(rel, body, resolved)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh"+template.TmplSuffix) {
			mode = 0o755
		}
		return os.WriteFile(dst, out, mode)
	})
	if err != nil {
		return err
	}
	if !hasTmpl {
		// No .tmpl files in this repo — leave renderRoot absent.
		_ = os.RemoveAll(renderRoot)
	}
	return nil
}

