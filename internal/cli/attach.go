package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

// newAttachCmd builds `agent-team attach <instance>` — the interactive-resume
// command described in `documentation/orchestrator.md` § Lifecycle model
// (Shape A, transfer-ownership model).
//
// Flow:
//  1. Daemon must be running. Topology lookup rejects ephemeral instances.
//  2. POST /v1/stop {instance} — daemon SIGTERMs the child and (per SQU-28's
//     reaper-with-channel sync) returns only after metadata is persisted.
//  3. Read the persisted session_id from /v1/instances.
//  4. exec the managed-resume command directly with stdin/stdout/stderr
//     wired to the user's terminal — TTY ownership transfers.
//  5. When the user exits: unless --no-resume is set, POST /v1/start to put
//     the instance back under daemon supervision.
//
// Brief downtime is by design (Shape A). Per-instance state files
// (status.toml, channel cursors, mailbox cursor) are untouched throughout.
func newAttachCmd() *cobra.Command {
	var (
		target           string
		noResume         bool
		dryRun           bool
		commands         bool
		all              bool
		latest           bool
		last             int
		noFollow         bool
		statuses         []string
		runtimes         []string
		agents           []string
		phases           []string
		staleOnly        bool
		runtimeStaleOnly bool
		unhealthy        bool
		tail             string
		since            string
		grep             string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "attach <instance>",
		Aliases: []string{"exec"},
		Short:   "Open an interactive runtime session against a daemon-managed persistent instance.",
		Long: "Stop the daemon-managed child for <instance>, then exec " +
			"`<runtime> --resume <session-id>` in your terminal so the conversation " +
			"continues interactively. On exit, the daemon resumes supervision " +
			"automatically — pass --no-resume to leave the instance stopped.\n\n" +
			"There is brief downtime during the handoff (Shape A): the daemon " +
			"child is killed before the runtime resume command reattaches. Channel cursors " +
			"and mailbox state survive the transfer.\n\n" +
			"Compatibility: log-oriented invocations such as --no-follow, --tail, " +
			"--latest, --last, --all, or status/agent/phase filters follow the " +
			"daemon-captured log stream, matching the older attach shortcut. " +
			"`agent-team logs` is the preferred explicit command for log streaming. " +
			"Dry-runs also print unmanaged resume and log commands for runtimes " +
			"that do not support daemon-managed resume.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --commands requires --dry-run.")
				return exitErr(2)
			}
			logMode := attachUsesLogMode(args, all, latest, last, statuses, runtimes, agents, phases, staleOnly, runtimeStaleOnly, unhealthy, noFollow, cmd.Flags().Changed("tail"), since, grep)
			if logMode {
				if dryRun {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --dry-run requires an instance name and cannot be combined with log-follow attach options.")
					return exitErr(2)
				}
				if noResume {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --no-resume cannot be combined with log-follow attach options.")
					return exitErr(2)
				}
				return runAttachLogMode(cmd, target, args, attachLogOptions{
					All:            all,
					Latest:         latest,
					Limit:          last,
					NoFollow:       noFollow,
					StatusFilters:  statuses,
					RuntimeFilters: runtimes,
					AgentFilters:   agents,
					PhaseFilters:   phases,
					Stale:          staleOnly,
					RuntimeStale:   runtimeStaleOnly,
					Unhealthy:      unhealthy,
					Tail:           tail,
					TailSet:        cmd.Flags().Changed("tail"),
					Since:          since,
					Grep:           grep,
				})
			}
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: instance is required.")
				return exitErr(2)
			}
			scope := operatorCommandScopeFromCommand(cmd, target, "target")
			return runAttach(cmd, target, args[0], noResume, dryRun, commands, attachCommandOptions{
				BaseArgs:   []string{"agent-team", "attach"},
				TargetFlag: "--repo",
				Target:     scope.Repo,
				TargetSet:  scope.Set,
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&noResume, "no-resume", false, "Leave the instance in stopped state when the runtime exits (default: re-dispatch via the daemon).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the interactive handoff without stopping or resuming the daemon child.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the matching attach or unmanaged fallback commands. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Log compatibility mode: attach to every daemon-known instance, prefixed by instance name.")
	cmd.Flags().BoolVar(&latest, "latest", false, "Log compatibility mode: attach to the most recently started instance.")
	cmd.Flags().IntVarP(&last, "last", "n", 0, "Log compatibility mode: attach to the N most recently started instances (0 = disabled).")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "Log compatibility mode: print the selected log tail and exit instead of following.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Log compatibility mode: only attach to instances with lifecycle status: running, stopped, exited, crashed, or unknown. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimes, "runtime", nil, "Log compatibility mode: only attach to instances for this runtime: claude, codex, or docker. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&agents, "agent", nil, "Log compatibility mode: only attach to instances for this agent. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&phases, "phase", nil, "Log compatibility mode: only attach to instances in this work phase: planning, implementing, awaiting_review, blocked, idle, done, or unknown. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Log compatibility mode: only attach to instances whose status.toml is stale.")
	cmd.Flags().BoolVar(&runtimeStaleOnly, "runtime-stale", false, "Log compatibility mode: only attach to instances whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthy, "unhealthy", false, "Log compatibility mode: only attach to crashed, status-stale, or runtime-stale instances.")
	cmd.Flags().StringVar(&tail, "tail", "50", "Log compatibility mode: show only the last N lines before following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Log compatibility mode with --no-follow: only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Log compatibility mode with --no-follow: only print log lines matching this regular expression.")
	return cmd
}

type attachLogOptions struct {
	All            bool
	Latest         bool
	Limit          int
	NoFollow       bool
	StatusFilters  []string
	RuntimeFilters []string
	AgentFilters   []string
	PhaseFilters   []string
	Stale          bool
	RuntimeStale   bool
	Unhealthy      bool
	Tail           string
	TailSet        bool
	Since          string
	Grep           string
}

func attachUsesLogMode(args []string, all bool, latest bool, last int, statuses, runtimes, agents, phases []string, stale, runtimeStale, unhealthy, noFollow, tailSet bool, since, grep string) bool {
	return len(args) == 0 ||
		all ||
		latest ||
		last > 0 ||
		len(statuses) > 0 ||
		len(runtimes) > 0 ||
		len(agents) > 0 ||
		len(phases) > 0 ||
		stale ||
		runtimeStale ||
		unhealthy ||
		noFollow ||
		tailSet ||
		since != "" ||
		grep != ""
}

func runAttachLogMode(cmd *cobra.Command, target string, args []string, opts attachLogOptions) error {
	if opts.Limit < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --last must be >= 0.")
		return exitErr(2)
	}
	hasFilters := len(opts.StatusFilters) > 0 || len(opts.RuntimeFilters) > 0 || len(opts.AgentFilters) > 0 || len(opts.PhaseFilters) > 0 || opts.Stale || opts.RuntimeStale || opts.Unhealthy
	if opts.All && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --all cannot be combined with an instance name.")
		return exitErr(2)
	}
	if opts.Latest && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --latest cannot be combined with an instance name.")
		return exitErr(2)
	}
	if opts.Limit > 0 && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --last cannot be combined with an instance name.")
		return exitErr(2)
	}
	if hasFilters && len(args) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --status, --runtime, --agent, --phase, --stale, --runtime-stale, and --unhealthy cannot be combined with an instance name.")
		return exitErr(2)
	}
	if opts.Latest && opts.Limit > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: choose one of --latest or --last.")
		return exitErr(2)
	}
	if opts.Latest && opts.All {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --latest cannot be combined with --all.")
		return exitErr(2)
	}
	if opts.Limit > 0 && opts.All {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --last cannot be combined with --all.")
		return exitErr(2)
	}
	if hasFilters {
		if _, err := newLogListOptionsWithRuntimeAndUnhealthy(opts.StatusFilters, opts.RuntimeFilters, opts.AgentFilters, opts.PhaseFilters, opts.Stale, opts.Unhealthy); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
			return exitErr(2)
		}
	}
	if !opts.Latest && opts.Limit == 0 && !opts.All && !hasFilters && len(args) != 1 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: instance is required unless --all, --latest, --last, --status, --runtime, --agent, --phase, --stale, --runtime-stale, or --unhealthy is set.")
		return exitErr(2)
	}
	tailLines, err := parseLogTail(opts.Tail)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
		return exitErr(2)
	}
	sinceCutoff, err := parseLogSince(opts.Since, time.Now)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
		return exitErr(2)
	}
	grepPattern, err := parseLogGrep(opts.Grep)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
		return exitErr(2)
	}
	if sinceCutoff != nil && !opts.NoFollow {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --since requires --no-follow because captured logs are not timestamped.")
		return exitErr(2)
	}
	if grepPattern != nil && !opts.NoFollow {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team attach: --grep requires --no-follow.")
		return exitErr(2)
	}
	return runLogs(cmd, target, args, logsOptions{
		All:            opts.All,
		Follow:         !opts.NoFollow,
		Latest:         opts.Latest,
		Limit:          opts.Limit,
		StatusFilters:  opts.StatusFilters,
		RuntimeFilters: opts.RuntimeFilters,
		AgentFilters:   opts.AgentFilters,
		PhaseFilters:   opts.PhaseFilters,
		Stale:          opts.Stale,
		RuntimeStale:   opts.RuntimeStale,
		Unhealthy:      opts.Unhealthy,
		Tail:           tailLines,
		TailSet:        opts.TailSet,
		Since:          sinceCutoff,
		Grep:           grepPattern,
	})
}

type attachPlan struct {
	teamDir string
	meta    *daemon.Metadata
	bin     string
}

type attachCommandOptions struct {
	BaseArgs   []string
	TargetFlag string
	Target     string
	TargetSet  bool
}

func prepareAttach(cmd *cobra.Command, target, instance string, allowUnsupportedPreview bool) (*attachPlan, *daemonClient, error) {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return nil, nil, err
	}

	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: daemon is not running — start it first with `agent-team daemon start`.")
			return nil, nil, exitErr(2)
		}
		return nil, nil, err
	}

	// Reject ephemeral instances: they spawn-and-exit by design and have no
	// stable session to attach to. Send the user to logs --follow instead.
	if topo, terr := topology.LoadFromTeamDir(teamDir); terr == nil && topo != nil {
		if decl := topo.Find(instance); decl != nil && decl.Ephemeral {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"agent-team: %q is declared ephemeral; cannot attach. Use `agent-team logs %s --follow` to watch its output.\n",
				instance, instance)
			return nil, nil, exitErr(2)
		}
	}

	meta, err := lookupInstanceMeta(dc, instance)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: %v\n", err)
		return nil, nil, exitErr(2)
	}
	if meta.SessionID == "" {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"agent-team: %q has no session id recorded; cannot attach. Has it been dispatched yet?\n",
			instance)
		return nil, nil, exitErr(2)
	}

	bin, err := resolveAttachRuntimeBinary(cmd, teamDir, meta, allowUnsupportedPreview)
	if err != nil {
		return nil, nil, err
	}
	return &attachPlan{teamDir: teamDir, meta: meta, bin: bin}, dc, nil
}

func runAttach(cmd *cobra.Command, target, instance string, noResume bool, dryRun bool, commands bool, commandOpts attachCommandOptions) error {
	plan, dc, err := prepareAttach(cmd, target, instance, dryRun)
	if err != nil {
		return err
	}
	meta := plan.meta

	if dryRun {
		if commands {
			return renderAttachDryRunCommands(cmd.OutOrStdout(), instance, meta, plan.bin, noResume, commandOpts)
		}
		renderAttachDryRun(cmd.OutOrStdout(), instance, meta, plan.bin, noResume)
		return nil
	}

	// Stop the running child (if any). An already-stopped instance is a no-op
	// on the daemon side — we proceed straight to runtime resume.
	if meta.Status == daemon.StatusRunning {
		if err := dc.StopInstance(instance); err != nil {
			return fmt.Errorf("agent-team: stop %s: %w", instance, err)
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"agent-team: attaching to %s (session=%s)...\n", instance, meta.SessionID)

	resumeErr := execClaudeAttach(cmd, plan.bin, attachResumeRuntimeArgs(meta), target)

	if noResume {
		fmt.Fprintf(cmd.OutOrStdout(),
			"agent-team: %s left in stopped state. Run `agent-team start %s` to resume under the daemon.\n",
			instance, instance)
		return resumeErr
	}

	if startErr := dc.StartInstance(instance); startErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"agent-team: runtime session ended but daemon `start` failed: %v\n  Run `agent-team start %s` manually.\n",
			startErr, instance)
		if resumeErr != nil {
			return resumeErr
		}
		return exitErr(1)
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"agent-team: %s resumed under daemon.\n", instance)
	return resumeErr
}

func renderAttachDryRunCommands(w fmtWriter, instance string, meta *daemon.Metadata, bin string, noResume bool, opts attachCommandOptions) error {
	if lifecycleMetadataSupportsManagedResume(meta) {
		args := attachApplyCommandArgs(instance, noResume, opts)
		_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(args), " "))
		return err
	}
	if command := attachResumeCommand(meta, bin); command != "" {
		if _, err := fmt.Fprintln(w, command); err != nil {
			return err
		}
	}
	if strings.TrimSpace(instance) == "" {
		return nil
	}
	logsArgs := attachLogsCommandArgs(instance, "--follow", opts)
	if _, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(logsArgs), " ")); err != nil {
		return err
	}
	if lifecycleMetadataRuntimeKind(meta) == runtimebin.KindCodex {
		lastMessageArgs := attachLogsCommandArgs(instance, "--last-message", opts)
		if _, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(lastMessageArgs), " ")); err != nil {
			return err
		}
	}
	return nil
}

func attachApplyCommandArgs(instance string, noResume bool, opts attachCommandOptions) []string {
	args := append([]string{}, opts.BaseArgs...)
	if opts.TargetSet && strings.TrimSpace(opts.Target) != "" {
		args = append(args, attachCommandTargetFlag(opts), opts.Target)
	}
	args = append(args, instance)
	if noResume {
		args = append(args, "--no-resume")
	}
	return args
}

func attachLogsCommandArgs(instance, logFlag string, opts attachCommandOptions) []string {
	args := []string{"agent-team", "logs"}
	if opts.TargetSet && strings.TrimSpace(opts.Target) != "" {
		args = append(args, attachCommandTargetFlag(opts), opts.Target)
	}
	args = append(args, instance, logFlag)
	return args
}

func attachCommandTargetFlag(opts attachCommandOptions) string {
	if flag := strings.TrimSpace(opts.TargetFlag); flag != "" {
		return flag
	}
	return "--target"
}

func resolveAttachRuntimeBinary(cmd *cobra.Command, teamDir string, meta *daemon.Metadata, allowUnsupportedPreview bool) (string, error) {
	if !lifecycleMetadataSupportsManagedResume(meta) {
		if allowUnsupportedPreview {
			return attachRuntimeBinaryFromMetadata(meta), nil
		}
		writeAttachUnsupportedResumeHint(cmd.ErrOrStderr(), meta, lifecycleUnsupportedResumeDetail(meta))
		return "", exitErr(2)
	}
	if bin := strings.TrimSpace(meta.RuntimeBinary); bin != "" {
		return bin, nil
	}
	if meta != nil && strings.TrimSpace(meta.Runtime) != "" {
		return runtimebin.DefaultBinaryForKind(lifecycleMetadataRuntimeKind(meta)), nil
	}
	rt, err := runtimebin.CurrentFromConfig(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: %v\n", err)
		return "", exitErr(2)
	}
	if !runtimeKindSupportsManagedResume(rt.Kind) {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team attach: runtime %q does not support managed resume; create a new run instead\n", rt.Kind)
		return "", exitErr(2)
	}
	return rt.Binary, nil
}

func writeAttachUnsupportedResumeHint(w fmtWriter, meta *daemon.Metadata, detail string) {
	fmt.Fprintf(w, "agent-team attach: %s\n", detail)
	var instance, sessionID string
	if meta != nil {
		instance = strings.TrimSpace(meta.Instance)
		sessionID = strings.TrimSpace(meta.SessionID)
	}
	if instance != "" {
		fmt.Fprintf(w, "  Follow captured logs with `agent-team logs %s --follow`.\n", instance)
		if lifecycleMetadataRuntimeKind(meta) == runtimebin.KindCodex {
			fmt.Fprintf(w, "  Read the clean Codex final message with `agent-team logs %s --last-message`.\n", instance)
		}
	}
	if lifecycleMetadataRuntimeKind(meta) == runtimebin.KindCodex && sessionID != "" {
		fmt.Fprintf(w, "  For unmanaged Codex resume outside daemon ownership, run `%s`.\n", attachResumeCommand(meta, attachRuntimeBinaryFromMetadata(meta)))
	}
}

func renderAttachDryRun(w fmtWriter, instance string, meta *daemon.Metadata, bin string, noResume bool) {
	runtimeKind := lifecycleMetadataRuntimeKind(meta)
	managedResume := lifecycleMetadataSupportsManagedResume(meta)
	wouldStop := managedResume && meta.Status == daemon.StatusRunning
	wouldResumeDaemon := managedResume && !noResume
	fmt.Fprintf(w, "instance:             %s\n", instance)
	fmt.Fprintf(w, "runtime:              %s\n", runtimeKind)
	fmt.Fprintf(w, "runtime_binary:       %s\n", bin)
	fmt.Fprintf(w, "status:               %s\n", meta.Status)
	fmt.Fprintf(w, "session_id:           %s\n", meta.SessionID)
	fmt.Fprintf(w, "managed_resume:       %s\n", attachYesNo(managedResume))
	fmt.Fprintf(w, "would_stop:           %s\n", attachYesNo(wouldStop))
	fmt.Fprintf(w, "command:              %s\n", attachResumeCommand(meta, bin))
	fmt.Fprintf(w, "would_resume_daemon:  %s\n", attachYesNo(wouldResumeDaemon))
	if !managedResume {
		fmt.Fprintf(w, "managed_detail:       %s\n", lifecycleUnsupportedResumeDetail(meta))
		if strings.TrimSpace(instance) != "" {
			fmt.Fprintf(w, "logs_command:         agent-team logs %s --follow\n", instance)
			if runtimeKind == runtimebin.KindCodex {
				fmt.Fprintf(w, "last_message_command: agent-team logs %s --last-message\n", instance)
			}
		}
	}
}

func attachYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func attachRuntimeBinaryFromMetadata(meta *daemon.Metadata) string {
	if meta != nil {
		if bin := strings.TrimSpace(meta.RuntimeBinary); bin != "" {
			return bin
		}
	}
	return runtimebin.DefaultBinaryForKind(lifecycleMetadataRuntimeKind(meta))
}

func attachResumeCommand(meta *daemon.Metadata, bin string) string {
	args := attachResumeCommandArgs(meta, bin)
	if len(args) == 0 {
		return ""
	}
	return strings.Join(shellQuoteArgs(args), " ")
}

func attachResumeCommandArgs(meta *daemon.Metadata, bin string) []string {
	sessionID := ""
	if meta != nil {
		sessionID = strings.TrimSpace(meta.SessionID)
	}
	if sessionID == "" {
		return nil
	}
	if lifecycleMetadataRuntimeKind(meta) == runtimebin.KindCodex {
		return []string{bin, "resume", sessionID}
	}
	return []string{bin, "--resume", sessionID}
}

func attachResumeRuntimeArgs(meta *daemon.Metadata) []string {
	sessionID := ""
	if meta != nil {
		sessionID = strings.TrimSpace(meta.SessionID)
	}
	if lifecycleMetadataRuntimeKind(meta) == runtimebin.KindCodex {
		return []string{"resume", sessionID}
	}
	return []string{"--resume", sessionID}
}

// lookupInstanceMeta fetches the daemon's metadata for one instance. Returns a
// helpful error when the daemon doesn't know the instance, so the CLI message
// is friendlier than a raw "not found".
func lookupInstanceMeta(dc *daemonClient, instance string) (*daemon.Metadata, error) {
	insts, err := dc.Instances()
	if err != nil {
		return nil, fmt.Errorf("query daemon: %w", err)
	}
	for _, m := range insts {
		if m.Instance == instance {
			return m, nil
		}
	}
	return nil, fmt.Errorf("instance %q not known to the daemon", instance)
}

// execClaudeAttach is split out so tests can intercept the exec without
// requiring a real Claude-compatible binary. The default wires stdin/stdout/stderr
// to the user's terminal so the runtime TUI is fully interactive.
var execClaudeAttach = func(cmd *cobra.Command, bin string, args []string, cwd string) error {
	c := exec.Command(bin, args...)
	c.Dir = cwd
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound) {
			fmt.Fprintf(cmd.ErrOrStderr(), "agent-team: runtime CLI %q not found in PATH. Install it first or set %s.\n", bin, runtimebin.EnvBinary)
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
