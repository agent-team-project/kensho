package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

// runsRootDir is a stable, predictable location (rather than os.TempDir())
// so users who pass --keep can find their preserved runs without hunting
// through platform-specific tmp paths.
func runsRootDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "agent-team", "runs")
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".agent-team", "runs")
	}
	return filepath.Join(os.TempDir(), "agent-team-runs")
}

type templateRunConfig struct {
	target     string
	keep       bool
	force      bool
	prompt     string
	name       string
	setStrings []string
	noInput    bool
}

func newTemplateRunCmd() *cobra.Command {
	var cfg templateRunConfig
	cmd := &cobra.Command{
		Use:   "run <ref> <agent> [-- <runtime-args>...]",
		Short: "One-shot: instantiate a template into a tempdir and spawn an agent.",
		Long: "Instantiate a template (bundled, local path, or cached ref) into a target directory " +
			"and immediately spawn the named agent against it. Returns when the selected runtime " +
			"session exits. Without --target, a tempdir under " +
			"$XDG_CACHE_HOME/agent-team/runs (or ~/.agent-team/runs) is created and removed on " +
			"exit unless --keep is passed. With --target, the directory is preserved.\n\n" +
			"This is for ephemeral try-out / CI / sandbox use cases. The daemon is bypassed; " +
			"the selected runtime is exec'd directly. For long-lived setups, use `init` + `run` separately.",
		Args:               cobra.MinimumNArgs(2),
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			agent := args[1]
			forwarded := args[2:]
			return runTemplateRun(cmd, cfg, ref, agent, forwarded)
		},
	}
	cmd.Flags().StringVar(&cfg.target, "target", "", "Target directory (must not already contain .agent_team/ unless --force). Defaults to a tempdir.")
	cmd.Flags().BoolVar(&cfg.keep, "keep", false, "Keep the auto-created tempdir on exit (no-op when --target is set).")
	cmd.Flags().BoolVar(&cfg.force, "force", false, "Overwrite an existing .agent_team/ at --target.")
	cmd.Flags().StringVarP(&cfg.prompt, "prompt", "p", "", "Kickoff message for the agent (one-shot mode if set, interactive otherwise).")
	cmd.Flags().StringVarP(&cfg.name, "name", "n", "", "Instance name (defaults to the agent name).")
	cmd.Flags().StringArrayVar(&cfg.setStrings, "set", nil, "Set a template parameter, e.g. --set linear.team_id=<uuid>. Repeatable.")
	cmd.Flags().BoolVar(&cfg.noInput, "no-input", false, "Fail if required parameters are missing instead of prompting.")
	return cmd
}

func runTemplateRun(cmd *cobra.Command, cfg templateRunConfig, ref, agent string, forwarded []string) error {
	target, autoCreated, err := prepareTemplateRunTarget(cmd, cfg, agent)
	if err != nil {
		return err
	}

	// Guard so the signal handler and the deferred path don't race to remove
	// the same dir twice.
	var cleaned int32
	cleanup := func() {
		if !autoCreated || cfg.keep {
			return
		}
		if atomic.CompareAndSwapInt32(&cleaned, 0, 1) {
			_ = os.RemoveAll(target)
		}
	}
	defer cleanup()

	stopSignals := installSignalCleanup(cleanup)
	defer stopSignals()

	if err := runInit(cmd, initConfig{
		target:     target,
		force:      cfg.force,
		kind:       "default",
		ref:        ref,
		setStrings: cfg.setStrings,
		noInput:    cfg.noInput,
	}); err != nil {
		return err
	}

	forwarded, err = templateRunRuntimeArgs(target, autoCreated, forwarded)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team template run: %v\n", err)
		return exitErr(2)
	}

	// Daemon bypass is intentional — see documentation/templates.md
	// § Worked example step 8. A tempdir-scoped daemon would just add
	// lifecycle churn for a one-shot run.
	return runAgent(cmd, runConfig{
		target:     target,
		name:       cfg.name,
		prompt:     cfg.prompt,
		setStrings: cfg.setStrings,
		noDaemon:   true,
	}, agent, forwarded)
}

func templateRunRuntimeArgs(target string, autoCreated bool, forwarded []string) ([]string, error) {
	if !autoCreated {
		return forwarded, nil
	}
	rt, err := runtimebin.CurrentFromConfig(filepath.Join(target, teamDirName, "config.toml"))
	if err != nil {
		return nil, err
	}
	if rt.Kind != runtimebin.KindCodex || hasForwardedRuntimeFlag(forwarded, "--skip-git-repo-check") {
		return forwarded, nil
	}
	out := append([]string{}, forwarded...)
	return append(out, "--skip-git-repo-check"), nil
}

func hasForwardedRuntimeFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

// prepareTemplateRunTarget returns the absolute target dir and whether we
// auto-created it (callers use the flag to scope cleanup to dirs we own).
func prepareTemplateRunTarget(cmd *cobra.Command, cfg templateRunConfig, agent string) (string, bool, error) {
	if cfg.target != "" {
		abs, err := filepath.Abs(cfg.target)
		if err != nil {
			return "", false, err
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", false, fmt.Errorf("create target: %w", err)
		}
		teamDir := filepath.Join(abs, teamDirName)
		if !cfg.force {
			if st, err := os.Stat(teamDir); err == nil && st.IsDir() {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"agent-team: %s already exists; pass --force to overwrite\n", teamDir)
				return "", false, exitErr(2)
			}
		}
		return abs, false, nil
	}

	root := runsRootDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", false, fmt.Errorf("create runs root: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	prefix := fmt.Sprintf("%s-%s-", stamp, agent)
	dir, err := os.MkdirTemp(root, prefix)
	if err != nil {
		return "", false, fmt.Errorf("create tempdir: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Using tempdir %s%s\n", dir, keepHint(cfg.keep))
	return dir, true, nil
}

func keepHint(keep bool) string {
	if keep {
		return " (kept; remove manually when done)"
	}
	return " (removed on exit; pass --keep to preserve)"
}

// installSignalCleanup runs `fn` on SIGINT/SIGTERM then re-raises the signal
// so the parent shell sees the conventional signal-style exit status. The
// returned teardown must be deferred — it stops the goroutine on normal exit.
func installSignalCleanup(fn func()) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			fn()
			signal.Stop(ch)
			// Re-raise so the parent shell sees the signal-style exit.
			if p, err := os.FindProcess(os.Getpid()); err == nil {
				_ = p.Signal(sig)
			}
		case <-done:
			return
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
