package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	texttemplate "text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/loader"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/runtimehooks"
	"github.com/agent-team-project/agent-team/internal/runtimeotel"
	"github.com/agent-team-project/agent-team/internal/runtimeshim"
	"github.com/agent-team-project/agent-team/internal/template"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

// runConfig is the parsed flags for `agent-team run`.
type runConfig struct {
	target         string
	name           string
	prompt         string
	promptFile     string
	setStrings     []string
	instanceConfig string
	noDaemon       bool
	detach         bool
	attach         bool
	tail           string
	tailSet        bool
	readyTimeout   time.Duration
	jsonOut        bool
	format         string
	lastMessage    bool
	runtimeKind    string
	runtimeBinary  string
}

func newRunCmd() *cobra.Command {
	var cfg runConfig
	cwd, _ := os.Getwd()

	cmd := &cobra.Command{
		Use:   "run <agent> [-- <runtime-args>...]",
		Short: "Launch an LLM runtime session as the named agent.",
		Long: "Launch an LLM runtime session as the named agent. With the default Claude-compatible " +
			"runtime, the agent's prompt becomes the system prompt and all other agents are " +
			"registered as subagents. Pass `--name` to give the instance a unique identifier " +
			"(state dir: .agent_team/state/<name>/). Forward extra runtime args after `--`.",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: false,
		// Allow unknown trailing args after the agent name to be forwarded to the runtime.
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.tailSet = cmd.Flags().Changed("tail")
			prompt, err := promptTextWithFile(cfg.prompt, cfg.promptFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: %v\n", err)
				return exitErr(2)
			}
			cfg.prompt = prompt
			return runAgent(cmd, cfg, args[0], args[1:])
		},
	}
	cmd.Flags().StringVar(&cfg.target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVarP(&cfg.name, "name", "n", "", "Instance name (defaults to the agent name). State dir: .agent_team/state/<name>/.")
	cmd.Flags().StringVarP(&cfg.prompt, "prompt", "p", "", "Kickoff message. With this, the runtime runs in one-shot mode when supported; without, interactive.")
	cmd.Flags().StringVar(&cfg.promptFile, "prompt-file", "", "Read kickoff message from a file, or '-' for stdin.")
	cmd.Flags().StringArrayVar(&cfg.setStrings, "set", nil, "Override a config value for this spawn, e.g. --set linear.team_id=<x>. Repeatable.")
	cmd.Flags().StringVar(&cfg.instanceConfig, "instance-config", "", "Path to a per-instance TOML config that layers on top of repo config (below --set).")
	cmd.Flags().BoolVar(&cfg.noDaemon, "no-daemon", false, "Bypass the daemon: exec the runtime directly even if the daemon is running. Useful for debugging.")
	cmd.Flags().BoolVarP(&cfg.detach, "detach", "d", false, "Dispatch through agent-teamd and return immediately instead of attaching to the runtime.")
	cmd.Flags().BoolVar(&cfg.attach, "attach", false, "Dispatch through agent-teamd and follow the captured instance log.")
	cmd.Flags().StringVar(&cfg.tail, "tail", "50", "With --attach, show only the last N lines before following (0 or all = all).")
	cmd.Flags().DurationVar(&cfg.readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for daemon readiness with --detach or --attach (0 = no timeout).")
	cmd.Flags().BoolVar(&cfg.jsonOut, "json", false, "Emit daemon dispatch metadata as JSON. Requires --prompt or --detach.")
	cmd.Flags().StringVar(&cfg.format, "format", "", "Render daemon dispatch metadata with a Go template, e.g. '{{.Instance}} {{.PID}}'. Requires --prompt or --detach.")
	cmd.Flags().BoolVar(&cfg.lastMessage, "last-message", false, "With Codex --prompt runs, bypass the daemon and print only the clean final response sidecar.")
	cmd.Flags().StringVar(&cfg.runtimeKind, "runtime", "", "Runtime profile for this invocation (claude, codex, or docker). Overrides env and repo config.")
	cmd.Flags().StringVar(&cfg.runtimeBinary, "runtime-bin", "", "Runtime binary for this invocation. Overrides env and repo config.")
	return cmd
}

func promptTextWithFile(prompt, promptFile string) (string, error) {
	promptSet := strings.TrimSpace(prompt) != ""
	fileSet := strings.TrimSpace(promptFile) != ""
	switch {
	case promptSet && fileSet:
		return "", fmt.Errorf("provide prompt text using only one of --prompt or --prompt-file")
	case fileSet:
		body, err := readMessageFile(promptFile, "--prompt-file")
		if err != nil {
			return "", err
		}
		text := strings.TrimSpace(string(body))
		if text == "" {
			return "", fmt.Errorf("prompt text is required")
		}
		return text, nil
	default:
		return prompt, nil
	}
}

func runAgent(cmd *cobra.Command, cfg runConfig, agentName string, forwarded []string) error {
	if cfg.format != "" && cfg.jsonOut {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --format cannot be combined with --json.")
		return exitErr(2)
	}
	if cfg.attach && cfg.jsonOut {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --attach cannot be combined with --json.")
		return exitErr(2)
	}
	if cfg.format != "" && cfg.attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --format cannot be combined with --attach.")
		return exitErr(2)
	}
	if cfg.jsonOut && cfg.prompt == "" && !cfg.detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --json requires --prompt or --detach so the daemon can dispatch the session.")
		return exitErr(2)
	}
	if cfg.format != "" && cfg.prompt == "" && !cfg.detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --format requires --prompt or --detach so the daemon can dispatch the session.")
		return exitErr(2)
	}
	if cfg.jsonOut && cfg.noDaemon {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --json cannot be combined with --no-daemon.")
		return exitErr(2)
	}
	if cfg.format != "" && cfg.noDaemon {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --format cannot be combined with --no-daemon.")
		return exitErr(2)
	}
	if cfg.lastMessage && cfg.prompt == "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --last-message requires --prompt.")
		return exitErr(2)
	}
	if cfg.lastMessage && cfg.jsonOut {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --last-message cannot be combined with --json.")
		return exitErr(2)
	}
	if cfg.lastMessage && cfg.format != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --last-message cannot be combined with --format.")
		return exitErr(2)
	}
	if cfg.lastMessage && cfg.detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --last-message cannot be combined with --detach.")
		return exitErr(2)
	}
	if cfg.lastMessage && cfg.attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --last-message cannot be combined with --attach.")
		return exitErr(2)
	}
	if cfg.detach && cfg.noDaemon {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --detach cannot be combined with --no-daemon.")
		return exitErr(2)
	}
	if cfg.detach && cfg.attach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: choose one of --detach or --attach.")
		return exitErr(2)
	}
	if cfg.attach && cfg.noDaemon {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --attach cannot be combined with --no-daemon.")
		return exitErr(2)
	}
	if !cfg.attach && cfg.tailSet {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --tail requires --attach.")
		return exitErr(2)
	}
	if cfg.readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	tailLines, err := parseLogTail(cfg.tail)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: %v\n", err)
		return exitErr(2)
	}
	formatTemplate, err := parseRunFormat(cfg.format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: %v\n", err)
		return exitErr(2)
	}

	repoResolved, err := resolvePrimaryRepo(cmd, cfg.target)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return exitErr(2)
	}
	target := repoResolved.RepoRoot
	teamDir := repoResolved.TeamDir
	rt, err := runtimeFromConfigWithOverrides(filepath.Join(teamDir, "config.toml"), runtimeSelection{
		Kind:   cfg.runtimeKind,
		Binary: cfg.runtimeBinary,
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: %v\n", err)
		return exitErr(2)
	}
	if rt.Kind == runtimebin.KindDocker {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: docker runtime is only supported for daemon topology dispatch; use an instance or pipeline runtime override.")
		return exitErr(2)
	}
	if rt.Kind == runtimebin.KindCodex && (cfg.detach || cfg.attach) && strings.TrimSpace(cfg.prompt) == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: codex daemon dispatch requires --prompt so Codex can run with `codex exec`.\n")
		return exitErr(2)
	}
	if cfg.lastMessage {
		if rt.Kind != runtimebin.KindCodex {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: --last-message requires the codex runtime.\n")
			return exitErr(2)
		}
		if hasForwardedRuntimeFlag(forwarded, "--output-last-message") {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: --last-message cannot be combined with forwarded --output-last-message.\n")
			return exitErr(2)
		}
		cfg.noDaemon = true
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
	lastMessagePath := ""
	if rt.Kind == runtimebin.KindCodex && strings.TrimSpace(cfg.prompt) != "" {
		lastMessagePath = filepath.Join(stateDir, runtimebin.CodexLastMessageFile)
		if err := os.Remove(lastMessagePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale Codex last message: %w", err)
		}
	}

	// Resolve the config tree:
	//   repo `config.toml` ← declared overrides (instances.toml)
	//                     ← per-instance state ← --set.
	// The merged result is written to <stateDir>/config.toml so the spawned
	// session reads exactly the config its caller intended.
	resolved, err := resolveRunConfig(teamDir, stateDir, instance, cfg)
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
	var mailboxHook *runtimehooks.MailboxHook
	if runtimehooks.MailboxInjectionEnabled(resolved) {
		hook, err := runtimehooks.PrepareMailboxHook(filepath.Join(stateDir, "runtime"))
		if err != nil {
			return err
		}
		mailboxHook = hook
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
	if brief, err := daemon.InstanceBriefLaunchText(teamDir, instance); err != nil {
		return fmt.Errorf("generate instance brief: %w", err)
	} else if brief != "" {
		kickoff = brief + "\n\n--- runtime kickoff ---\n\n" + kickoff
	}
	promptFile := filepath.Join(tmpdir, "system_prompt.md")
	if err := os.WriteFile(promptFile, []byte(kickoff), 0o644); err != nil {
		return fmt.Errorf("write prompt file: %w", err)
	}

	agentsJSON, err := buildAgentsJSON(agents)
	if err != nil {
		return err
	}

	// Forwarded args may begin with `--` (the cobra delimiter); strip it.
	if len(forwarded) > 0 && forwarded[0] == "--" {
		forwarded = forwarded[1:]
	}
	teamEnv := []string{
		"AGENT_TEAM_ROOT=" + teamDir,
		"AGENT_TEAM_INSTANCE=" + instance,
		"AGENT_TEAM_STATE_DIR=" + stateDir,
		"AGENT_TEAM_DAEMON_SOCKET=" + daemon.SocketPath(teamDir),
	}
	tokenFile, err := daemon.EnsureInstanceToken(teamDir, instance)
	if err != nil {
		return fmt.Errorf("create daemon token: %w", err)
	}
	teamEnv = append(teamEnv, daemon.DaemonTokenFileEnv+"="+tokenFile)
	authorityAllow, authorityEnforce := topologyAuthorityAllowlistForInstance(teamDir, instance, agentName)
	teamEnv = runtimeshim.WithAuthorityAllowlist(teamEnv, authorityAllow)
	if daemonURL := daemonURLForRuntimeEnv(teamDir); daemonURL != "" {
		teamEnv = append(teamEnv, "AGENT_TEAM_DAEMON_URL="+daemonURL)
	}
	otelCfg, err := runtimeotel.FromTree(resolved)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: %v\n", err)
		return exitErr(2)
	}
	otelLaunch, err := runtimeotel.BuildLaunch(otelCfg, rt.Kind, runtimeotel.Context{
		Agent:        agentName,
		Instance:     instance,
		JobID:        os.Getenv("AGENT_TEAM_JOB_ID"),
		Ticket:       os.Getenv("AGENT_TEAM_TICKET"),
		Pipeline:     os.Getenv("AGENT_TEAM_PIPELINE"),
		PipelineStep: os.Getenv("AGENT_TEAM_PIPELINE_STEP"),
		Team:         topologyTeamForInstance(teamDir, instance),
		Runtime:      string(rt.Kind),
		Branch:       os.Getenv("AGENT_TEAM_BRANCH"),
		Worktree:     firstNonEmpty(os.Getenv("AGENT_TEAM_WORKTREE"), target),
		Build:        BuildInfo(),
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: %v\n", err)
		return exitErr(2)
	}
	teamEnv = append(teamEnv, otelLaunch.Env...)

	// Daemon-aware routing: one-shot dispatches (--prompt given) route
	// through the daemon when one is running, and --detach opts into daemon
	// routing explicitly. Plain interactive sessions stay direct because
	// users expect an attached terminal.
	//
	// Channel subscriptions declared in agent frontmatter (`subscribes: ...`)
	// are POSTed to the daemon before the spawn so an agent has its mailbox
	// ready when its runtime process boots. This applies to both daemon and
	// no-daemon paths — but no-daemon mode silently skips since channels
	// only exist with a running daemon.
	var dispatchClient *daemonClient
	if cfg.detach || cfg.attach {
		if err := ensureDaemonReadyWithTimeout(cmd, target, cfg.jsonOut || formatTemplate != nil, cfg.readyTimeout); err != nil {
			return err
		}
	}
	daemonCapable := rt.Kind == runtimebin.KindClaude || rt.Kind == runtimebin.KindCodex
	if !cfg.noDaemon && daemonCapable {
		dc, err := newDaemonClient(teamDir)
		if err == nil {
			dispatchClient = dc
			subscribeAgentChannels(cmd, dc, instance, chosen.Subscribes)
		} else if cfg.jsonOut || formatTemplate != nil {
			if errors.Is(err, errDaemonNotRunning) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team run: daemon is not running — start it with `agent-team start`.")
				return exitErr(2)
			}
			return err
		}
	}
	daemonDispatch := !cfg.noDaemon && daemonCapable && (cfg.prompt != "" || cfg.detach || cfg.attach) && dispatchClient != nil
	shimRoot := tmpdir
	if daemonDispatch {
		shimRoot = stateDir
	}
	shimBinDir, err := runtimeshim.InstallWithOptions(shimRoot, skillPaths, runtimeshim.Options{
		EnforceAuthority:   authorityEnforce,
		AuthorityAllowlist: authorityAllow,
	})
	if err != nil {
		return err
	}

	baseEnv := os.Environ()
	if otelCfg.Configured() {
		baseEnv = runtimeotel.StripOwnedEnv(baseEnv)
	}
	env := runtimeshim.PrependPath(append(baseEnv, teamEnv...), shimBinDir)
	runtimeArgEnv := append([]string(nil), teamEnv...)
	runtimeArgEnv = runtimeshim.PrependPath(append(runtimeArgEnv, "PATH="+os.Getenv("PATH")), shimBinDir)
	runtimeArgs, runtimeStdin, err := buildRuntimeArgs(rt, target, tmpdir, agentsJSON, promptFile, kickoff, cfg.prompt, forwarded, agents, runtimeArgEnv, lastMessagePath, mailboxHook, otelLaunch)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: %v\n", err)
		return exitErr(2)
	}

	if daemonDispatch {
		// runtimeArgs already starts with --agents/--add-dir/.../-p; the
		// daemon prepends the selected runtime binary and session metadata
		// when needed, so we strip nothing. The daemon's spawn surface
		// accepts arbitrary trailing argv via DispatchInput.Args.
		disp, derr := dispatchClient.Dispatch(dispatchPayload{
			Agent:         agentName,
			Name:          instance,
			Prompt:        cfg.prompt,
			Workspace:     target,
			Runtime:       string(rt.Kind),
			RuntimeBinary: rt.Binary,
			Args:          runtimeArgs,
			Env:           runtimeArgEnv,
			Stdin:         runtimeStdin,
		})
		if derr != nil {
			return fmt.Errorf("daemon dispatch: %w", derr)
		}
		row := runDispatchJSON{
			Instance:  disp.InstanceID,
			Agent:     agentName,
			Runtime:   disp.Runtime,
			PID:       disp.PID,
			SessionID: disp.SessionID,
			StartedAt: disp.StartedAt.Format(time.RFC3339),
			Follow:    fmt.Sprintf("agent-team logs %s --follow", disp.InstanceID),
		}
		if cfg.jsonOut {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(row)
		}
		if formatTemplate != nil {
			return renderRunFormat(cmd.OutOrStdout(), row, formatTemplate)
		}
		if cfg.attach {
			out := cmd.OutOrStdout()
			printRunDispatchLine(out, disp)
			fmt.Fprintf(out, "\nattaching to %s (Ctrl-C to detach)\n", disp.InstanceID)
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return followLifecycleLog(ctx, out, dispatchClient, disp.InstanceID, tailLines)
		}
		printRunDispatchLine(cmd.OutOrStdout(), disp)
		fmt.Fprintf(cmd.OutOrStdout(),
			"  follow: agent-team logs %s --follow\n", disp.InstanceID)
		return nil
	}

	if cfg.lastMessage {
		return execRuntimeAndPrintLastMessage(cmd, rt.Binary, runtimeArgs, env, target, runtimeStdin, lastMessagePath)
	}
	return execClaude(cmd, rt.Binary, runtimeArgs, env, target, runtimeStdin)
}

func buildRuntimeArgs(rt runtimebin.Runtime, target, addDir, agentsJSON, promptFile, kickoff, prompt string, forwarded []string, agents []*loader.Agent, env []string, lastMessagePath string, mailboxHook *runtimehooks.MailboxHook, otelLaunch runtimeotel.Launch) ([]string, string, error) {
	switch rt.Kind {
	case runtimebin.KindClaude:
		args := []string{
			"--agents", agentsJSON,
			"--add-dir", addDir,
			"--append-system-prompt-file", promptFile,
		}
		if mailboxHook != nil {
			settingsPath, err := runtimehooks.WriteClaudeSettings(mailboxHook.RuntimeDir, mailboxHook)
			if err != nil {
				return nil, "", err
			}
			args = append(args, "--settings", settingsPath)
		}
		if prompt != "" {
			args = append(args, "-p", prompt)
		}
		return append(args, forwarded...), "", nil
	case runtimebin.KindCodex:
		args := []string{}
		if prompt != "" {
			args = append(args, "exec")
		}
		if mailboxHook != nil {
			if !hasForwardedRuntimeFlag(forwarded, "--dangerously-bypass-hook-trust") {
				args = append(args, "--dangerously-bypass-hook-trust")
			}
			args = append(args, runtimehooks.CodexConfigArgs(mailboxHook)...)
		}
		args = append(args, otelLaunch.CodexArgs...)
		args = append(args, runtimebin.CodexAgentTeamEnvConfigArgs(env)...)
		args = append(args, "-C", target, "--add-dir", addDir)
		args = append(args, forwarded...)
		if prompt != "" && strings.TrimSpace(lastMessagePath) != "" && !hasForwardedRuntimeFlag(forwarded, "--output-last-message") {
			args = append(args, "--output-last-message", lastMessagePath)
		}
		initialPrompt := codexInitialPrompt(kickoff, prompt, agents)
		if prompt != "" {
			args = append(args, "-")
			return args, initialPrompt, nil
		}
		args = append(args, initialPrompt)
		return args, "", nil
	default:
		return nil, "", fmt.Errorf("unsupported runtime %q", rt.Kind)
	}
}

func codexInitialPrompt(kickoff, prompt string, agents []*loader.Agent) string {
	var b strings.Builder
	b.WriteString(kickoff)
	b.WriteString("\n\n--- agent-team runtime ---\n\n")
	b.WriteString("This session is running through the Codex adapter. The current agent prompt is included above. Other team agents are listed for coordination context, but this adapter does not register them as native subagents.\n")
	if len(agents) > 0 {
		b.WriteString("\nAvailable team agents:\n")
		for _, agent := range agents {
			b.WriteString("- ")
			b.WriteString(agent.Name)
			if agent.Description != "" {
				b.WriteString(": ")
				b.WriteString(agent.Description)
			}
			b.WriteByte('\n')
		}
	}
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("\n--- task ---\n\n")
		b.WriteString(prompt)
	}
	return b.String()
}

func parseRunFormat(format string) (*texttemplate.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := texttemplate.New("run-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderRunFormat(w fmtWriter, row runDispatchJSON, tmpl *texttemplate.Template) error {
	if err := tmpl.Execute(w, row); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

type runDispatchJSON struct {
	Instance  string `json:"instance"`
	Agent     string `json:"agent"`
	Runtime   string `json:"runtime,omitempty"`
	PID       int    `json:"pid"`
	SessionID string `json:"session_id,omitempty"`
	StartedAt string `json:"started_at"`
	Follow    string `json:"follow"`
}

func printRunDispatchLine(w fmtWriter, disp *dispatchResponse) {
	runtime := disp.Runtime
	if runtime == "" {
		runtime = string(runtimebin.KindClaude)
	}
	if disp.SessionID != "" {
		fmt.Fprintf(w,
			"agent-team: dispatched %s via daemon (runtime=%s, pid=%d, session=%s)\n",
			disp.InstanceID, runtime, disp.PID, disp.SessionID)
		return
	}
	fmt.Fprintf(w,
		"agent-team: dispatched %s via daemon (runtime=%s, pid=%d)\n",
		disp.InstanceID, runtime, disp.PID)
}

func execRuntimeAndPrintLastMessage(cmd *cobra.Command, bin string, args []string, env []string, cwd, stdin, lastMessagePath string) error {
	var stdout, stderr bytes.Buffer
	err := execRuntime(cmd, bin, args, env, cwd, stdin, &stdout, &stderr)
	if err != nil {
		_, _ = stdout.WriteTo(cmd.OutOrStdout())
		_, _ = stderr.WriteTo(cmd.ErrOrStderr())
		return err
	}
	if err := writeLastMessageFile(cmd.OutOrStdout(), lastMessagePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team run: Codex last message not found at %s.\n", lastMessagePath)
			fmt.Fprintln(cmd.ErrOrStderr(), "  The runtime exited successfully but did not write the expected --output-last-message sidecar.")
			return exitErr(1)
		}
		return err
	}
	return nil
}

// execClaude is split out so tests can intercept the exec.
var execClaude = func(cmd *cobra.Command, bin string, args []string, env []string, cwd, stdin string) error {
	return execRuntime(cmd, bin, args, env, cwd, stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

var execRuntime = func(cmd *cobra.Command, bin string, args []string, env []string, cwd, stdin string, stdout, stderr io.Writer) error {
	bin = strings.TrimSpace(bin)
	if bin == "" {
		fmt.Fprintln(stderr, "agent-team: runtime binary is empty.")
		return exitErr(2)
	}
	c := exec.Command(bin, args...)
	c.Env = env
	c.Dir = cwd
	var cleanupStdin func()
	if stdin != "" {
		stdinFile, cleanup, err := openRuntimeStdin(stdin)
		if err != nil {
			return err
		}
		cleanupStdin = cleanup
		c.Stdin = stdinFile
	} else if len(args) == 0 || args[0] != "exec" {
		c.Stdin = os.Stdin
	}
	c.Stdout = stdout
	c.Stderr = stderr
	var runErr error
	if cleanupStdin != nil {
		runErr = c.Start()
		cleanupStdin()
		if runErr == nil {
			runErr = c.Wait()
		}
	} else {
		runErr = c.Run()
	}
	if runErr != nil {
		var execErr *exec.Error
		if errors.As(runErr, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			fmt.Fprintf(stderr, "agent-team: runtime CLI %q not found in PATH. Install it first or set %s.\n", bin, runtimebin.EnvBinary)
			return exitErr(127)
		}
		var exitErrTyped *exec.ExitError
		if errors.As(runErr, &exitErrTyped) {
			return exitErr(exitErrTyped.ExitCode())
		}
		return runErr
	}
	return nil
}

func openRuntimeStdin(content string) (*os.File, func(), error) {
	f, err := os.CreateTemp("", "agent-team-stdin-")
	if err != nil {
		return nil, nil, fmt.Errorf("create runtime stdin temp file: %w", err)
	}
	cleanup := func() {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
	}
	if _, err := f.WriteString(content); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("write runtime stdin temp file: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("rewind runtime stdin temp file: %w", err)
	}
	return f, cleanup, nil
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

// resolveRunConfig builds the resolved instance config from the five-layer
// chain in `documentation/topology.md` § Layered config resolution chain:
//  1. repo config (`<teamDir>/config.toml`)
//  2. per-instance declared overrides (`instances.toml [instances.<name>.config]`)
//  3. per-instance state file — either `--instance-config <path>` or the
//     auto-pickup at `<stateDir>/config.toml`
//  4. CLI `--set` flags
//
// The merged tree is the single source of truth for the spawned session's
// skills and bash steps.
func resolveRunConfig(teamDir, stateDir, instance string, cfg runConfig) (template.Tree, error) {
	repoCfg, err := template.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		return nil, fmt.Errorf("repo config: %w", err)
	}
	declared, err := loadDeclaredOverrides(teamDir, instance)
	if err != nil {
		return nil, fmt.Errorf("instances.toml: %w", err)
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
	merged := template.ResolveLayers(repoCfg, declared, instanceCfg)

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

// loadDeclaredOverrides reads instances.toml and returns the [instances.<name>.config]
// tree for `instance`, or an empty tree if no declaration matches. Missing
// instances.toml is non-fatal — topology declaration is opt-in.
func loadDeclaredOverrides(teamDir, instance string) (template.Tree, error) {
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if topo == nil {
		return template.Tree{}, nil
	}
	decl := topo.Find(instance)
	if decl == nil || decl.Config == nil {
		return template.Tree{}, nil
	}
	return decl.Config, nil
}

func topologyTeamForInstance(teamDir, instance string) string {
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil || topo == nil {
		return ""
	}
	return topo.TeamForInstance(instance)
}

func topologyAuthorityAllowlistForInstance(teamDir, instance, agent string) (allow []string, enforce bool) {
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil || topo == nil {
		return nil, false
	}
	// Enforce whenever the topology configures authority at all — an instance
	// whose resolved allowlist is empty under a configured authority section
	// is deny-all-beyond-read-only, distinct from no-authority pass-through.
	enforce = topo.Authority != nil && topo.Authority.Configured()
	return topo.AuthorityAllowlistForInstance(instance, agent), enforce
}

func daemonURLForRuntimeEnv(teamDir string) string {
	inherited := strings.TrimRight(strings.TrimSpace(os.Getenv("AGENT_TEAM_DAEMON_URL")), "/")
	if preferInheritedDaemonURL(inherited) {
		return inherited
	}
	if httpAddr, err := daemon.ReadHTTPAddr(teamDir); err == nil && strings.TrimSpace(httpAddr) != "" {
		return daemon.DaemonHTTPURL(httpAddr)
	}
	return inherited
}

func preferInheritedDaemonURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "host.docker.internal")
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

// subscribeAgentChannels POSTs /v1/channel/<name>/subscribe for each declared
// channel before the agent's runtime process is spawned. Errors are logged to
// stderr and ignored (the agent can re-subscribe at runtime via the channel
// skill if needed) — a flaky `subscribe` shouldn't gate the dispatch.
func subscribeAgentChannels(cmd *cobra.Command, dc *daemonClient, instance string, channels []string) {
	for _, ch := range channels {
		ch = strings.TrimSpace(ch)
		if ch == "" {
			continue
		}
		if _, err := dc.ChannelSubscribe(ch, instance); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"agent-team: failed to pre-subscribe %s to %s: %v\n", instance, ch, err)
		}
	}
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
