package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

// newDaemonCmd builds the `agent-team daemon` command group: start, stop,
// status hooks for the agent-teamd binary. The CLI itself is a thin wrapper
// around the daemon's pidfile and socket — the real lifecycle work lives in
// `internal/daemon`.
func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the agent-teamd orchestrator daemon for this repo.",
		Long: "Manage the agent-teamd orchestrator daemon for this repo.\n\n" +
			"agent-teamd is the per-repo daemon that owns claude-subprocess lifecycle " +
			"(spawn / track / stop / resume) and serves a small JSON API over " +
			".agent_team/daemon.sock. It is a separate binary; this command group manages it.",
	}
	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonStatusCmd())
	return cmd
}

func newDaemonStartCmd() *cobra.Command {
	var (
		target string
		detach bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Boot agent-teamd in this repo (background by default with --detach; foreground without).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStart(cmd, target, detach)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&detach, "detach", false, "Background the daemon (writes log to .agent_team/daemon/agent-teamd.log).")
	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Gracefully stop the running agent-teamd (SIGTERM, then SIGKILL after timeout).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStop(cmd, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return cmd
}

func newDaemonStatusCmd() *cobra.Command {
	var target string
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print whether agent-teamd is running in this repo, and its pid if so.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStatus(cmd, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	return cmd
}

func runDaemonStart(cmd *cobra.Command, target string, detach bool) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}

	// Already running? Don't double-spawn.
	if pid, alive := daemonAlive(teamDir); alive {
		fmt.Fprintf(cmd.OutOrStdout(), "agent-teamd already running (pid=%d).\n", pid)
		return nil
	}

	bin, err := locateAgentTeamd(cmd)
	if err != nil {
		return err
	}

	if !detach {
		// Foreground: re-exec the daemon directly so the user sees its logs.
		c := exec.Command(bin, "--target", filepath.Dir(teamDir))
		c.Stdin = os.Stdin
		c.Stdout = cmd.OutOrStdout()
		c.Stderr = cmd.ErrOrStderr()
		return c.Run()
	}

	// Detached: open the daemon log file and start the child with new SID.
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		return fmt.Errorf("mkdir daemon root: %w", err)
	}
	logPath := daemon.LogPath(teamDir)
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer devnull.Close()

	proc, err := os.StartProcess(bin, []string{bin, "--target", filepath.Dir(teamDir)}, &os.ProcAttr{
		Dir:   filepath.Dir(teamDir),
		Env:   os.Environ(),
		Files: []*os.File{devnull, logFile, logFile},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return fmt.Errorf("spawn agent-teamd: %w", err)
	}
	// Detach from the child — we don't want to be its reaper.
	if err := proc.Release(); err != nil {
		return fmt.Errorf("detach: %w", err)
	}

	// Wait briefly for the pidfile to appear so the user gets immediate
	// feedback. If it doesn't show, it likely crashed at startup —
	// surface the log path.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pid, alive := daemonAlive(teamDir); alive {
			fmt.Fprintf(cmd.OutOrStdout(), "agent-teamd started (pid=%d).\nlog: %s\n", pid, logPath)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("agent-teamd did not write %s within 3s — check %s", daemon.PidPath(teamDir), logPath)
}

func runDaemonStop(cmd *cobra.Command, target string) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	pid, err := daemon.ReadPidfile(daemon.PidPath(teamDir))
	if err != nil {
		return fmt.Errorf("read pidfile: %w", err)
	}
	if pid == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "agent-teamd not running.")
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			// Already gone — clean up the stale pidfile.
			_ = os.Remove(daemon.PidPath(teamDir))
			fmt.Fprintln(cmd.OutOrStdout(), "agent-teamd was not running (stale pidfile removed).")
			return nil
		}
		return fmt.Errorf("SIGTERM pid %d: %w", pid, err)
	}

	// Wait up to 5s for clean shutdown.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !daemon.PidLiveCheck(pid) {
			fmt.Fprintf(cmd.OutOrStdout(), "agent-teamd stopped (pid was %d).\n", pid)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Force.
	_ = proc.Signal(syscall.SIGKILL)
	fmt.Fprintf(cmd.OutOrStdout(), "agent-teamd did not exit on SIGTERM; sent SIGKILL (pid was %d).\n", pid)
	return nil
}

func runDaemonStatus(cmd *cobra.Command, target string) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	pid, alive := daemonAlive(teamDir)
	if !alive {
		fmt.Fprintln(cmd.OutOrStdout(), "agent-teamd: not running")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "agent-teamd: running (pid=%d)\n", pid)
	fmt.Fprintf(cmd.OutOrStdout(), "socket: %s\n", daemon.SocketPath(teamDir))
	return nil
}

// daemonAlive reads the pidfile and probes the PID. Returns (pid, alive).
// A stale pidfile (pid present but process gone) returns (0, false).
func daemonAlive(teamDir string) (int, bool) {
	pid, err := daemon.ReadPidfile(daemon.PidPath(teamDir))
	if err != nil || pid == 0 {
		return 0, false
	}
	if !daemon.PidLiveCheck(pid) {
		return 0, false
	}
	return pid, true
}

// locateAgentTeamd finds the agent-teamd binary. Search order:
//   1. PATH (`exec.LookPath`).
//   2. The same directory as the running agent-team binary (so a `go install`
//      that puts both binaries in the same `$GOBIN` works without a separate
//      PATH entry, and so a release tarball that ships them side-by-side works
//      too).
func locateAgentTeamd(cmd *cobra.Command) (string, error) {
	if path, err := exec.LookPath("agent-teamd"); err == nil {
		return path, nil
	}
	exe, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "agent-teamd")
		if st, err := os.Stat(sibling); err == nil && !st.IsDir() {
			return sibling, nil
		}
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: agent-teamd binary not found.")
	fmt.Fprintln(cmd.ErrOrStderr(), "  Install it alongside agent-team — `go install ./cmd/agent-teamd` if you're building from source.")
	return "", exitErr(127)
}

