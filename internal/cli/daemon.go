package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
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
			"agent-teamd is the per-repo daemon that owns runtime subprocess lifecycle " +
			"(spawn / track / stop / resume) and serves a small JSON API over " +
			".agent_team/daemon.sock. It is a separate binary; this command group manages it.",
	}
	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonRestartCmd())
	cmd.AddCommand(newDaemonAdoptCmd())
	cmd.AddCommand(newDaemonReconcileCmd())
	cmd.AddCommand(newDaemonStatusCmd())
	cmd.AddCommand(newDaemonLogsCmd())
	cmd.AddCommand(newDaemonEnvCmd())
	return cmd
}

func newDaemonStartCmd() *cobra.Command {
	var (
		target       string
		detach       bool
		readyTimeout time.Duration
		httpAddr     string
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Boot agent-teamd in this repo (detached by default; foreground with --detach=false).",
		Long: "Boot agent-teamd in this repo. By default the daemon is detached so the command " +
			"returns after the socket is ready. Pass --detach=false to run in the foreground for debugging.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if jsonOut && !detach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --json cannot be combined with --detach=false.")
				return exitErr(2)
			}
			if quiet && !detach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --quiet cannot be combined with --detach=false.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && quiet {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --format cannot be combined with --quiet.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && !detach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --format cannot be combined with --detach=false.")
				return exitErr(2)
			}
			formatTemplate, err := parseDaemonLifecycleFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon start: %v\n", err)
				return exitErr(2)
			}
			normalizedHTTPAddr, err := daemon.NormalizeLoopbackHTTPAddr(httpAddr)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon start: %v\n", err)
				return exitErr(2)
			}
			if formatTemplate != nil {
				return runDaemonStartWithFormat(cmd, target, detach, readyTimeout, normalizedHTTPAddr, formatTemplate)
			}
			return runDaemonStartWithJSON(cmd, target, detach, readyTimeout, normalizedHTTPAddr, jsonOut, quiet)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&detach, "detach", true, "Background the daemon (writes log to .agent_team/daemon/agent-teamd.log).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for detached daemon readiness (0 = no timeout).")
	cmd.Flags().StringVar(&httpAddr, "http-addr", "", "Also expose the daemon API on this loopback HTTP address, e.g. 127.0.0.1:0. Empty disables HTTP.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code. Requires detached mode.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON. Requires detached mode.")
	cmd.Flags().StringVar(&format, "format", "", "Render daemon start result with a Go template, e.g. '{{.Action}} {{.PID}}'. Requires detached mode.")
	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	var (
		target  string
		timeout time.Duration
		quiet   bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Gracefully stop the running agent-teamd (SIGTERM, then SIGKILL after timeout).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon stop: --timeout must be >= 0.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon stop: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && quiet {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon stop: --format cannot be combined with --quiet.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon stop: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseDaemonLifecycleFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon stop: %v\n", err)
				return exitErr(2)
			}
			if formatTemplate != nil {
				return runDaemonStopWithTimeoutFormat(cmd, target, timeout, formatTemplate)
			}
			return runDaemonStopWithTimeoutJSON(cmd, target, timeout, jsonOut, quiet)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "Grace period before SIGKILL escalation (0 = force immediately).")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render daemon stop result with a Go template, e.g. '{{.Action}} {{.Changed}}'.")
	return cmd
}

func newDaemonRestartCmd() *cobra.Command {
	var (
		target       string
		detach       bool
		timeout      time.Duration
		readyTimeout time.Duration
		httpAddr     string
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart agent-teamd, reconciling existing instance metadata on boot.",
		Long: "Stop agent-teamd if it is running, then start it again. By default the restarted " +
			"daemon is detached so the command returns after the socket is ready. Pass --detach=false " +
			"to restart in the foreground for debugging.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --timeout must be >= 0.")
				return exitErr(2)
			}
			if readyTimeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --ready-timeout must be >= 0.")
				return exitErr(2)
			}
			if jsonOut && !detach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --json cannot be combined with --detach=false.")
				return exitErr(2)
			}
			if quiet && !detach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --quiet cannot be combined with --detach=false.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && quiet {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --format cannot be combined with --quiet.")
				return exitErr(2)
			}
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && !detach {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --format cannot be combined with --detach=false.")
				return exitErr(2)
			}
			formatTemplate, err := parseDaemonRestartFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon restart: %v\n", err)
				return exitErr(2)
			}
			normalizedHTTPAddr, err := daemon.NormalizeLoopbackHTTPAddr(httpAddr)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon restart: %v\n", err)
				return exitErr(2)
			}
			httpAddrExplicit := cmd.Flags().Changed("http-addr")
			if formatTemplate != nil {
				return runDaemonRestartWithFormat(cmd, target, detach, timeout, readyTimeout, normalizedHTTPAddr, httpAddrExplicit, formatTemplate)
			}
			return runDaemonRestartWithJSON(cmd, target, detach, timeout, readyTimeout, normalizedHTTPAddr, httpAddrExplicit, jsonOut, quiet)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&detach, "detach", true, "Background the restarted daemon (writes log to .agent_team/daemon/agent-teamd.log).")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "Grace period for stopping the old daemon before SIGKILL escalation (0 = force immediately).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for restarted detached daemon readiness (0 = no timeout).")
	cmd.Flags().StringVar(&httpAddr, "http-addr", "", "Also expose the restarted daemon API on this loopback HTTP address, e.g. 127.0.0.1:0. Empty disables HTTP.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code. Requires detached mode.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON. Requires detached mode.")
	cmd.Flags().StringVar(&format, "format", "", "Render daemon restart result with a Go template, e.g. '{{.Action}} {{.Changed}} {{.Status.Ready}}'. Requires detached mode.")
	return cmd
}

func newDaemonStatusCmd() *cobra.Command {
	var (
		target   string
		jsonOut  bool
		quiet    bool
		commands bool
		wait     bool
		down     bool
		timeout  time.Duration
		interval time.Duration
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print whether agent-teamd is running in this repo, and its pid if so.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon status: --timeout must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon status: --interval must be >= 0.")
				return exitErr(2)
			}
			if wait && interval == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon status: --interval must be > 0 with --wait.")
				return exitErr(2)
			}
			if down && !wait {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon status: --down requires --wait.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon status: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if commands && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon status: --commands cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon status: --format cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon status: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			formatTemplate, err := parseDaemonStatusFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon status: %v\n", err)
				return exitErr(2)
			}
			return runDaemonStatus(cmd, target, jsonOut, quiet, commands, operatorCommandScopeFromCommand(cmd, target, "target"), daemonStatusOptions{
				Wait:     wait,
				Down:     down,
				Timeout:  timeout,
				Interval: interval,
			}, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use the exit code as a readiness probe.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print only recommended follow-up commands for the daemon state. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait until agent-teamd is running and ready.")
	cmd.Flags().BoolVar(&down, "down", false, "With --wait, wait until agent-teamd is not running.")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 200*time.Millisecond, "Polling interval for --wait.")
	cmd.Flags().StringVar(&format, "format", "", "Render daemon status with a Go template, e.g. '{{.Ready}} {{.PID}}'.")
	return cmd
}

func newDaemonReconcileCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Refresh daemon instance metadata against the live process table.",
		Long: "Run the daemon's crash-only reconciliation pass without restarting agent-teamd. " +
			"Running records whose PIDs are gone are marked exited; live adopted records stay running.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon reconcile: --format cannot be combined with --json.")
				return exitErr(2)
			}
			formatTemplate, err := parseDaemonReconcileFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon reconcile: %v\n", err)
				return exitErr(2)
			}
			return runDaemonReconcile(cmd, target, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render reconcile result with a Go template, e.g. '{{.Changed}} {{len .Instances}}'.")
	return cmd
}

func newDaemonLogsCmd() *cobra.Command {
	var (
		target string
		follow bool
		tail   string
		since  string
		grep   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the agent-teamd daemon log.",
		Long: "Show or follow the local agent-teamd daemon log at " +
			".agent_team/daemon/agent-teamd.log. This is a discoverable alias " +
			"for `agent-team logs --daemon`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon logs: %v\n", err)
				return exitErr(2)
			}
			sinceCutoff, err := parseLogSince(since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon logs: %v\n", err)
				return exitErr(2)
			}
			grepPattern, err := parseLogGrep(grep)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team daemon logs: %v\n", err)
				return exitErr(2)
			}
			return runDaemonLog(cmd, target, logsOptions{Follow: follow, Tail: tailLines, TailSet: cmd.Flags().Changed("tail"), Since: sinceCutoff, Grep: grepPattern})
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail the daemon log; print new bytes as they appear.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only show the daemon log if it was modified since a duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print daemon log lines matching this regular expression. One-shot reads only.")
	return cmd
}

func newDaemonEnvCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Show the daemon launch environment snapshot with secrets redacted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonEnv(cmd, target, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON with secret values redacted.")
	return cmd
}

type daemonLifecycleJSON struct {
	Action              string           `json:"action"`
	Changed             bool             `json:"changed"`
	PID                 int              `json:"pid,omitempty"`
	PreviousPID         int              `json:"previous_pid,omitempty"`
	Log                 string           `json:"log,omitempty"`
	AlreadyRunning      bool             `json:"already_running,omitempty"`
	Stopped             bool             `json:"stopped,omitempty"`
	Killed              bool             `json:"killed,omitempty"`
	StalePidfileRemoved bool             `json:"stale_pidfile_removed,omitempty"`
	SnapshotMissing     bool             `json:"snapshot_missing,omitempty"`
	RelaunchedBinary    string           `json:"relaunched_binary,omitempty"`
	Message             string           `json:"message,omitempty"`
	Status              daemonStatusJSON `json:"status"`
}

const defaultDaemonReadyTimeout = 3 * time.Second

type daemonRestartJSON struct {
	Action           string              `json:"action"`
	Changed          bool                `json:"changed"`
	Stop             daemonLifecycleJSON `json:"stop"`
	Start            daemonLifecycleJSON `json:"start"`
	Status           daemonStatusJSON    `json:"status"`
	SnapshotMissing  bool                `json:"snapshot_missing,omitempty"`
	RelaunchedBinary string              `json:"relaunched_binary,omitempty"`
}

type daemonEnvJSON struct {
	Recorded   bool           `json:"recorded"`
	Path       string         `json:"path"`
	Bin        string         `json:"bin,omitempty"`
	Args       []string       `json:"args,omitempty"`
	Dir        string         `json:"dir,omitempty"`
	Env        []string       `json:"env,omitempty"`
	Stripped   []string       `json:"stripped,omitempty"`
	RecordedAt time.Time      `json:"recorded_at,omitempty"`
	PID        int            `json:"pid,omitempty"`
	Version    int            `json:"version,omitempty"`
	Build      buildinfo.Info `json:"build,omitempty"`
	Message    string         `json:"message,omitempty"`
}

func parseDaemonLifecycleFormat(format string) (*template.Template, error) {
	return parseDaemonFormat("daemon-lifecycle-format", format)
}

func parseDaemonRestartFormat(format string) (*template.Template, error) {
	return parseDaemonFormat("daemon-restart-format", format)
}

func parseDaemonReconcileFormat(format string) (*template.Template, error) {
	return parseDaemonFormat("daemon-reconcile-format", format)
}

func parseDaemonFormat(name, format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New(name).Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderDaemonLifecycleFormat(w io.Writer, result daemonLifecycleJSON, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderDaemonRestartFormat(w io.Writer, result daemonRestartJSON, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderDaemonReconcileFormat(w io.Writer, result *daemonReconcileResponse, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func runDaemonStart(cmd *cobra.Command, target string, detach bool) error {
	return runDaemonStartWithJSON(cmd, target, detach, defaultDaemonReadyTimeout, "", false, false)
}

func runDaemonStartWithJSON(cmd *cobra.Command, target string, detach bool, readyTimeout time.Duration, httpAddr string, jsonOut bool, quiet bool) error {
	if readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	if jsonOut && !detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --json cannot be combined with --detach=false.")
		return exitErr(2)
	}
	if quiet && !detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --quiet cannot be combined with --detach=false.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}

	if !detach {
		// Already running? Don't double-spawn.
		if pid, alive := daemonAlive(teamDir); alive {
			result := daemonLifecycleJSON{
				Action:         "start",
				Changed:        false,
				PID:            pid,
				Log:            daemon.LogPath(teamDir),
				AlreadyRunning: true,
				Message:        "already running",
				Status:         collectDaemonStatus(teamDir),
			}
			renderDaemonStartResult(cmd.OutOrStdout(), result)
			return nil
		}
		bin, err := locateAgentTeamd(cmd)
		if err != nil {
			return err
		}
		// Foreground: re-exec the daemon directly so the user sees its logs.
		args := []string{"--target", filepath.Dir(teamDir)}
		if strings.TrimSpace(httpAddr) != "" {
			args = append(args, "--http-addr", httpAddr)
		}
		c := exec.Command(bin, args...)
		c.Stdin = os.Stdin
		c.Stdout = cmd.OutOrStdout()
		c.Stderr = cmd.ErrOrStderr()
		return c.Run()
	}

	result, err := daemonStartDetachedOperation(cmd, teamDir, readyTimeout, httpAddr)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	if quiet {
		return nil
	}
	renderDaemonStartResult(cmd.OutOrStdout(), result)
	return nil
}

func runDaemonStartWithFormat(cmd *cobra.Command, target string, detach bool, readyTimeout time.Duration, httpAddr string, tmpl *template.Template) error {
	if readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	if !detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon start: --format cannot be combined with --detach=false.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	result, err := daemonStartDetachedOperation(cmd, teamDir, readyTimeout, httpAddr)
	if err != nil {
		return err
	}
	return renderDaemonLifecycleFormat(cmd.OutOrStdout(), result, tmpl)
}

func daemonStartDetachedOperation(cmd *cobra.Command, teamDir string, readyTimeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
	if pid, alive := daemonAlive(teamDir); alive {
		result := daemonLifecycleJSON{
			Action:         "start",
			Changed:        false,
			PID:            pid,
			Log:            daemon.LogPath(teamDir),
			AlreadyRunning: true,
			Message:        "already running",
			Status:         collectDaemonStatus(teamDir),
		}
		return result, nil
	}
	bin, err := locateAgentTeamd(cmd)
	if err != nil {
		return daemonLifecycleJSON{}, err
	}
	return daemonStartDetached(teamDir, bin, readyTimeout, httpAddr)
}

type daemonDetachedLaunch struct {
	Bin  string
	Args []string
	Dir  string
	Env  []string
}

var daemonStartDetachedLaunch = startDaemonDetachedLaunch

func daemonStartDetached(teamDir, bin string, readyTimeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
	args := []string{bin, "--target", filepath.Dir(teamDir)}
	if strings.TrimSpace(httpAddr) != "" {
		args = append(args, "--http-addr", httpAddr)
	}
	return daemonStartDetachedLaunch(teamDir, daemonDetachedLaunch{
		Bin:  bin,
		Args: args,
		Dir:  filepath.Dir(teamDir),
		Env:  os.Environ(),
	}, readyTimeout)
}

var errDaemonReadyTimeout = errors.New("daemon readiness timeout")

type daemonReadyTimeoutError struct {
	message string
}

func (e daemonReadyTimeoutError) Error() string {
	return e.message
}

func (e daemonReadyTimeoutError) Is(target error) bool {
	return target == errDaemonReadyTimeout
}

func startDaemonDetachedLaunch(teamDir string, launch daemonDetachedLaunch, readyTimeout time.Duration) (daemonLifecycleJSON, error) {
	// Detached: open the daemon log file and start the child with new SID.
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		return daemonLifecycleJSON{}, fmt.Errorf("mkdir daemon root: %w", err)
	}
	logPath := daemon.LogPath(teamDir)
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return daemonLifecycleJSON{}, fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return daemonLifecycleJSON{}, fmt.Errorf("open /dev/null: %w", err)
	}
	defer devnull.Close()

	bin := strings.TrimSpace(launch.Bin)
	if bin == "" && len(launch.Args) > 0 {
		bin = launch.Args[0]
	}
	if bin == "" {
		return daemonLifecycleJSON{}, errors.New("spawn agent-teamd: missing binary path")
	}
	args := append([]string(nil), launch.Args...)
	if len(args) == 0 {
		args = []string{bin}
	}
	dir := strings.TrimSpace(launch.Dir)
	if dir == "" {
		dir = filepath.Dir(teamDir)
	}
	env := append([]string(nil), launch.Env...)
	proc, err := os.StartProcess(bin, args, &os.ProcAttr{
		Dir:   dir,
		Env:   env,
		Files: []*os.File{devnull, logFile, logFile},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return daemonLifecycleJSON{}, fmt.Errorf("spawn agent-teamd: %w", err)
	}
	// Detach from the child — we don't want to be its reaper.
	if err := proc.Release(); err != nil {
		return daemonLifecycleJSON{}, fmt.Errorf("detach: %w", err)
	}

	// Wait briefly for the pidfile to appear so the user gets immediate
	// feedback. Then wait for the socket to be ready so commands that start
	// the daemon and immediately dispatch work don't race startup.
	var deadline time.Time
	if readyTimeout > 0 {
		deadline = time.Now().Add(readyTimeout)
	}
	for deadline.IsZero() || time.Now().Before(deadline) {
		if pid, alive := daemonAlive(teamDir); alive {
			if _, err := newDaemonClient(teamDir); err == nil {
				return daemonLifecycleJSON{
					Action:  "start",
					Changed: true,
					PID:     pid,
					Log:     logPath,
					Message: "started",
					Status:  collectDaemonStatus(teamDir),
				}, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return daemonLifecycleJSON{}, daemonReadyTimeoutError{
		message: fmt.Sprintf("agent-teamd did not become ready within %s — check %s", readyTimeout, logPath),
	}
}

func renderDaemonStartResult(w fmtWriter, result daemonLifecycleJSON) {
	if result.AlreadyRunning {
		fmt.Fprintf(w, "agent-teamd already running (pid=%d).\n", result.PID)
		return
	}
	fmt.Fprintf(w, "agent-teamd started (pid=%d).\nlog: %s\n", result.PID, result.Log)
	if result.RelaunchedBinary != "" {
		fmt.Fprintf(w, "binary: %s\n", result.RelaunchedBinary)
	}
}

func daemonRelaunchFromSnapshot(cmd *cobra.Command, teamDir string, readyTimeout time.Duration, httpAddr string, httpAddrExplicit bool) (daemonLifecycleJSON, error) {
	le, err := daemon.ReadLaunchEnv(daemon.DaemonRoot(teamDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			result, startErr := daemonStartDetachedOperation(cmd, teamDir, readyTimeout, httpAddr)
			result.SnapshotMissing = true
			if result.Message == "started" {
				result.Message = "started with current environment because launch-env snapshot is missing"
			}
			return result, startErr
		}
		return daemonLifecycleJSON{}, err
	}
	launch, err := daemonDetachedLaunchFromSnapshot(teamDir, le, httpAddr, httpAddrExplicit)
	if err != nil {
		return daemonLifecycleJSON{}, err
	}
	result, err := daemonStartDetachedLaunch(teamDir, launch, readyTimeout)
	if err != nil {
		if errors.Is(err, errDaemonReadyTimeout) {
			return daemonLifecycleJSON{}, fmt.Errorf("old daemon stopped, new daemon not ready — launch-env snapshot preserved at %s: %w", daemon.LaunchEnvPath(teamDir), err)
		}
		return daemonLifecycleJSON{}, err
	}
	result.RelaunchedBinary = launch.Bin
	return result, nil
}

func daemonRelaunchForegroundFromSnapshot(cmd *cobra.Command, teamDir string, httpAddr string, httpAddrExplicit bool) error {
	le, err := daemon.ReadLaunchEnv(daemon.DaemonRoot(teamDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			renderLaunchEnvSnapshotWarning(cmd.OutOrStdout())
			return runDaemonStartWithJSON(cmd, filepath.Dir(teamDir), false, defaultDaemonReadyTimeout, httpAddr, false, false)
		}
		return err
	}
	launch, err := daemonDetachedLaunchFromSnapshot(teamDir, le, httpAddr, httpAddrExplicit)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "relaunching agent-teamd from snapshot binary: %s\n", launch.Bin)
	args := append([]string(nil), launch.Args...)
	if len(args) > 0 {
		args = args[1:]
	}
	c := exec.Command(launch.Bin, args...)
	c.Dir = launch.Dir
	c.Env = launch.Env
	c.Stdin = os.Stdin
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

func daemonDetachedLaunchFromSnapshot(teamDir string, le *daemon.LaunchEnv, httpAddr string, httpAddrExplicit bool) (daemonDetachedLaunch, error) {
	if le == nil {
		return daemonDetachedLaunch{}, errors.New("launch-env: nil snapshot")
	}
	bin := strings.TrimSpace(le.Bin)
	if bin == "" && len(le.Args) > 0 {
		bin = strings.TrimSpace(le.Args[0])
	}
	if bin == "" {
		return daemonDetachedLaunch{}, errors.New("launch-env: missing daemon binary path")
	}
	args := append([]string(nil), le.Args...)
	if len(args) == 0 {
		args = []string{bin}
	} else {
		args[0] = bin
	}
	args = reconcileHTTPAddrArgs(args, httpAddr, httpAddrExplicit)
	dir := strings.TrimSpace(le.Dir)
	if dir == "" {
		dir = filepath.Dir(teamDir)
	}
	return daemonDetachedLaunch{
		Bin:  bin,
		Args: args,
		Dir:  dir,
		Env:  daemon.StripEnv(le.Env, daemon.DefaultStrippedEnvKeys),
	}, nil
}

func reconcileHTTPAddrArgs(args []string, httpAddr string, explicit bool) []string {
	if !explicit {
		return args
	}
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--http-addr":
			if i+1 < len(args) {
				i++
			}
			continue
		case strings.HasPrefix(arg, "--http-addr="):
			continue
		default:
			out = append(out, arg)
		}
	}
	if strings.TrimSpace(httpAddr) != "" {
		out = append(out, "--http-addr", httpAddr)
	}
	return out
}

func renderLaunchEnvSnapshotWarning(w fmtWriter) {
	fmt.Fprintln(w, "warning: no launch env recorded (daemon may predate this feature); restarted with current environment.")
}

func runDaemonStop(cmd *cobra.Command, target string) error {
	return runDaemonStopWithTimeout(cmd, target, 5*time.Second)
}

func runDaemonStopWithTimeout(cmd *cobra.Command, target string, timeout time.Duration) error {
	return runDaemonStopWithTimeoutJSON(cmd, target, timeout, false, false)
}

func runDaemonStopWithTimeoutJSON(cmd *cobra.Command, target string, timeout time.Duration, jsonOut bool, quiet bool) error {
	if timeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon stop: --timeout must be >= 0.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	result, err := daemonStopOperation(teamDir, timeout)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	if quiet {
		return nil
	}
	renderDaemonStopResult(cmd.OutOrStdout(), result)
	return nil
}

func runDaemonStopWithTimeoutFormat(cmd *cobra.Command, target string, timeout time.Duration, tmpl *template.Template) error {
	if timeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon stop: --timeout must be >= 0.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	result, err := daemonStopOperation(teamDir, timeout)
	if err != nil {
		return err
	}
	return renderDaemonLifecycleFormat(cmd.OutOrStdout(), result, tmpl)
}

func daemonStopOperation(teamDir string, timeout time.Duration) (daemonLifecycleJSON, error) {
	pid, err := daemon.ReadPidfile(daemon.PidPath(teamDir))
	if err != nil {
		return daemonLifecycleJSON{}, fmt.Errorf("read pidfile: %w", err)
	}
	if pid == 0 {
		return daemonLifecycleJSON{
			Action:  "stop",
			Changed: false,
			Message: "not running",
			Status:  collectDaemonStatus(teamDir),
		}, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return daemonLifecycleJSON{}, fmt.Errorf("find pid %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			// Already gone — clean up the stale pidfile.
			_ = os.Remove(daemon.PidPath(teamDir))
			return daemonLifecycleJSON{
				Action:              "stop",
				Changed:             true,
				PreviousPID:         pid,
				StalePidfileRemoved: true,
				Message:             "stale pidfile removed",
				Status:              collectDaemonStatus(teamDir),
			}, nil
		}
		return daemonLifecycleJSON{}, fmt.Errorf("SIGTERM pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !daemon.PidLiveCheck(pid) {
			return daemonLifecycleJSON{
				Action:      "stop",
				Changed:     true,
				PreviousPID: pid,
				Stopped:     true,
				Message:     "stopped",
				Status:      collectDaemonStatus(teamDir),
			}, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Force.
	_ = proc.Signal(syscall.SIGKILL)
	return daemonLifecycleJSON{
		Action:      "stop",
		Changed:     true,
		PreviousPID: pid,
		Stopped:     true,
		Killed:      true,
		Message:     "sent SIGKILL after timeout",
		Status:      collectDaemonStatus(teamDir),
	}, nil
}

func renderDaemonStopResult(w fmtWriter, result daemonLifecycleJSON) {
	switch {
	case result.StalePidfileRemoved:
		fmt.Fprintln(w, "agent-teamd was not running (stale pidfile removed).")
	case result.Killed:
		fmt.Fprintf(w, "agent-teamd did not exit on SIGTERM; sent SIGKILL (pid was %d).\n", result.PreviousPID)
	case result.Stopped:
		fmt.Fprintf(w, "agent-teamd stopped (pid was %d).\n", result.PreviousPID)
	default:
		fmt.Fprintln(w, "agent-teamd not running.")
	}
}

func runDaemonRestart(cmd *cobra.Command, target string, detach bool, timeout time.Duration) error {
	return runDaemonRestartWithJSON(cmd, target, detach, timeout, defaultDaemonReadyTimeout, "", false, false, false)
}

func runDaemonRestartWithJSON(cmd *cobra.Command, target string, detach bool, timeout, readyTimeout time.Duration, httpAddr string, httpAddrExplicit bool, jsonOut bool, quiet bool) error {
	if timeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --timeout must be >= 0.")
		return exitErr(2)
	}
	if readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	if jsonOut && !detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --json cannot be combined with --detach=false.")
		return exitErr(2)
	}
	if quiet && !detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --quiet cannot be combined with --detach=false.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	stopResult, err := daemonStopOperation(teamDir, timeout)
	if err != nil {
		return err
	}
	if !detach {
		if !quiet && !jsonOut {
			renderDaemonStopResult(cmd.OutOrStdout(), stopResult)
		}
		return daemonRelaunchForegroundFromSnapshot(cmd, teamDir, httpAddr, httpAddrExplicit)
	}
	if !jsonOut {
		startResult, err := daemonRelaunchFromSnapshot(cmd, teamDir, readyTimeout, httpAddr, httpAddrExplicit)
		if err != nil {
			return err
		}
		if quiet {
			return nil
		}
		renderDaemonStopResult(cmd.OutOrStdout(), stopResult)
		if startResult.SnapshotMissing {
			renderLaunchEnvSnapshotWarning(cmd.OutOrStdout())
		}
		renderDaemonStartResult(cmd.OutOrStdout(), startResult)
		return nil
	}
	startResult, err := daemonRelaunchFromSnapshot(cmd, teamDir, readyTimeout, httpAddr, httpAddrExplicit)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(daemonRestartJSON{
		Action:           "restart",
		Changed:          stopResult.Changed || startResult.Changed,
		Stop:             stopResult,
		Start:            startResult,
		Status:           startResult.Status,
		SnapshotMissing:  startResult.SnapshotMissing,
		RelaunchedBinary: startResult.RelaunchedBinary,
	})
}

func runDaemonRestartWithFormat(cmd *cobra.Command, target string, detach bool, timeout, readyTimeout time.Duration, httpAddr string, httpAddrExplicit bool, tmpl *template.Template) error {
	if timeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --timeout must be >= 0.")
		return exitErr(2)
	}
	if readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	if !detach {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon restart: --format cannot be combined with --detach=false.")
		return exitErr(2)
	}
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	stopResult, err := daemonStopOperation(teamDir, timeout)
	if err != nil {
		return err
	}
	startResult, err := daemonRelaunchFromSnapshot(cmd, teamDir, readyTimeout, httpAddr, httpAddrExplicit)
	if err != nil {
		return err
	}
	return renderDaemonRestartFormat(cmd.OutOrStdout(), daemonRestartJSON{
		Action:           "restart",
		Changed:          stopResult.Changed || startResult.Changed,
		Stop:             stopResult,
		Start:            startResult,
		Status:           startResult.Status,
		SnapshotMissing:  startResult.SnapshotMissing,
		RelaunchedBinary: startResult.RelaunchedBinary,
	}, tmpl)
}

func runDaemonReconcile(cmd *cobra.Command, target string, jsonOut bool, tmpl *template.Template) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			fmt.Fprintln(cmd.ErrOrStderr(), "agent-team daemon reconcile: daemon is not running.")
			return exitErr(1)
		}
		return err
	}
	resp, err := dc.Reconcile()
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
	}
	if tmpl != nil {
		return renderDaemonReconcileFormat(cmd.OutOrStdout(), resp, tmpl)
	}
	return renderDaemonReconcile(cmd.OutOrStdout(), resp)
}

func runDaemonEnv(cmd *cobra.Command, target string, jsonOut bool) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	path := daemon.LaunchEnvPath(teamDir)
	le, err := daemon.ReadLaunchEnv(daemon.DaemonRoot(teamDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			result := daemonEnvJSON{
				Recorded: false,
				Path:     path,
				Message:  "no launch env recorded (daemon may predate this feature)",
			}
			if jsonOut {
				if encErr := json.NewEncoder(cmd.OutOrStdout()).Encode(result); encErr != nil {
					return encErr
				}
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), result.Message)
			}
			return exitErr(1)
		}
		return err
	}
	result := redactedDaemonEnv(path, le)
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	renderDaemonEnv(cmd.OutOrStdout(), result)
	return nil
}

func redactedDaemonEnv(path string, le *daemon.LaunchEnv) daemonEnvJSON {
	env := append([]string(nil), le.Env...)
	sort.Slice(env, func(i, j int) bool {
		return envKey(env[i]) < envKey(env[j])
	})
	for i, item := range env {
		key, value, hasValue := strings.Cut(item, "=")
		if !hasValue {
			key = item
			value = ""
		}
		if daemonEnvKeyLooksSecret(key) {
			env[i] = key + "=<redacted>"
			continue
		}
		if hasValue {
			env[i] = key + "=" + value
		} else {
			env[i] = key
		}
	}
	stripped := append([]string(nil), le.Stripped...)
	sort.Strings(stripped)
	return daemonEnvJSON{
		Recorded:   true,
		Path:       path,
		Bin:        le.Bin,
		Args:       append([]string(nil), le.Args...),
		Dir:        le.Dir,
		Env:        env,
		Stripped:   stripped,
		RecordedAt: le.RecordedAt,
		PID:        le.PID,
		Version:    le.Version,
		Build:      le.Build,
	}
}

func renderDaemonEnv(w fmtWriter, result daemonEnvJSON) {
	fmt.Fprintf(w, "launch env: %s\n", result.Path)
	fmt.Fprintf(w, "bin: %s\n", result.Bin)
	argsJSON, _ := json.Marshal(result.Args)
	fmt.Fprintf(w, "args: %s\n", string(argsJSON))
	fmt.Fprintf(w, "dir: %s\n", result.Dir)
	if !result.RecordedAt.IsZero() {
		fmt.Fprintf(w, "recorded_at: %s\n", result.RecordedAt.Format(time.RFC3339))
	}
	if result.PID != 0 {
		fmt.Fprintf(w, "pid: %d\n", result.PID)
	}
	if result.Version != 0 {
		fmt.Fprintf(w, "version: %d\n", result.Version)
	}
	if !result.Build.Empty() {
		fmt.Fprintf(w, "build: %s\n", result.Build.Display())
	}
	if len(result.Stripped) > 0 {
		fmt.Fprintln(w, "stripped:")
		for _, key := range result.Stripped {
			fmt.Fprintf(w, "  %s\n", key)
		}
	}
	fmt.Fprintln(w, "env:")
	for _, item := range result.Env {
		fmt.Fprintf(w, "  %s\n", item)
	}
}

func daemonEnvKeyLooksSecret(key string) bool {
	key = strings.ToUpper(key)
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "_PW", "CREDENTIAL"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func envKey(item string) string {
	key, _, _ := strings.Cut(item, "=")
	return key
}

func renderDaemonReconcile(w fmtWriter, resp *daemonReconcileResponse) error {
	instanceCount := 0
	if resp != nil {
		instanceCount = len(resp.Instances)
	}
	changed := 0
	if resp != nil {
		changed = resp.Changed
	}
	fmt.Fprintf(w, "reconciled %d instances (%d changed)\n", instanceCount, changed)
	if resp == nil || len(resp.Changes) == 0 {
		fmt.Fprintln(w, "no status changes")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT\tSTATUS\tPID")
	for _, change := range resp.Changes {
		agent := change.Agent
		if agent == "" {
			agent = "—"
		}
		pid := "—"
		if change.PID != 0 {
			pid = fmt.Sprintf("%d", change.PID)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s -> %s\t%s\n", change.Instance, agent, change.Before, change.After, pid)
	}
	return tw.Flush()
}

type daemonStatusJSON struct {
	Running        bool           `json:"running"`
	Ready          bool           `json:"ready"`
	PID            int            `json:"pid,omitempty"`
	Instances      int            `json:"instances"`
	TeamDir        string         `json:"team_dir"`
	StartedAt      time.Time      `json:"started_at,omitempty"`
	Build          buildinfo.Info `json:"build,omitempty"`
	Socket         string         `json:"socket"`
	SocketExists   bool           `json:"socket_exists"`
	HTTPAddr       string         `json:"http_addr,omitempty"`
	HTTPURL        string         `json:"http_url,omitempty"`
	HTTPAddrFile   string         `json:"http_addr_file,omitempty"`
	HTTPAddrExists bool           `json:"http_addr_exists,omitempty"`
	Pidfile        string         `json:"pidfile"`
	StalePidfile   bool           `json:"stale_pidfile,omitempty"`
	Log            string         `json:"log"`
	Error          string         `json:"error,omitempty"`
	Warnings       []string       `json:"warnings,omitempty"`
	Actions        []string       `json:"actions,omitempty"`
}

type daemonStatusOptions struct {
	Wait     bool
	Down     bool
	Timeout  time.Duration
	Interval time.Duration
}

func runDaemonStatus(cmd *cobra.Command, target string, jsonOut, quiet, commands bool, scope operatorCommandScope, opts daemonStatusOptions, tmpl *template.Template) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	status := collectDaemonStatus(teamDir)
	timedOut := false
	if opts.Wait {
		if opts.Down {
			status, timedOut = waitForDaemonDown(teamDir, opts.Timeout, opts.Interval)
			if timedOut {
				status.Error = appendStatusError(status.Error, "timed out waiting for daemon shutdown")
			}
		} else {
			status, timedOut = waitForDaemonReady(teamDir, opts.Timeout, opts.Interval)
			if timedOut {
				status.Error = appendStatusError(status.Error, "timed out waiting for daemon readiness")
			}
		}
	}
	status = daemonStatusWithActions(status)
	status.Warnings = daemonStatusWarnings(status)
	if jsonOut {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(status); err != nil {
			return err
		}
		if timedOut {
			return exitErr(1)
		}
		return nil
	}
	if commands {
		if err := renderDaemonStatusCommands(cmd.OutOrStdout(), status, scope); err != nil {
			return err
		}
		if timedOut {
			return exitErr(1)
		}
		return nil
	}
	if quiet {
		if timedOut {
			return exitErr(1)
		}
		if opts.Down {
			if status.Running {
				return exitErr(1)
			}
			return nil
		}
		if !status.Ready {
			return exitErr(1)
		}
		return nil
	}
	if tmpl != nil {
		if err := renderDaemonStatusFormat(cmd.OutOrStdout(), status, tmpl); err != nil {
			return err
		}
		if timedOut {
			return exitErr(1)
		}
		return nil
	}
	renderDaemonStatus(cmd.OutOrStdout(), status)
	if timedOut {
		return exitErr(1)
	}
	return nil
}

func renderDaemonStatusCommands(w io.Writer, status daemonStatusJSON, scope operatorCommandScope) error {
	return renderOperatorActionCommands(w, daemonStatusActions(status), scope)
}

func daemonStatusCommandActions(status daemonStatusJSON) []string {
	if !status.Running {
		return []string{"agent-team daemon start"}
	}
	if !status.Ready {
		return []string{
			"agent-team daemon restart",
			"agent-team daemon logs --tail 80",
		}
	}
	if len(daemonBuildMismatchWarnings(status)) > 0 {
		return []string{"agent-team daemon restart"}
	}
	return []string{
		"agent-team ps",
		"agent-team monitor",
	}
}

func daemonStatusWithActions(status daemonStatusJSON) daemonStatusJSON {
	status.Actions = daemonStatusCommandActions(status)
	return status
}

func daemonStatusActions(status daemonStatusJSON) []string {
	if len(status.Actions) > 0 {
		return status.Actions
	}
	return daemonStatusCommandActions(status)
}

func parseDaemonStatusFormat(format string) (*template.Template, error) {
	return parseDaemonFormat("daemon-status-format", format)
}

func renderDaemonStatusFormat(w io.Writer, status daemonStatusJSON, tmpl *template.Template) error {
	if err := tmpl.Execute(w, status); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func waitForDaemonReady(teamDir string, timeout, interval time.Duration) (daemonStatusJSON, bool) {
	return waitForDaemonStatus(teamDir, timeout, interval, func(status daemonStatusJSON) bool {
		return status.Ready
	})
}

func waitForDaemonDown(teamDir string, timeout, interval time.Duration) (daemonStatusJSON, bool) {
	return waitForDaemonStatus(teamDir, timeout, interval, func(status daemonStatusJSON) bool {
		return !status.Running
	})
}

func waitForDaemonStatus(teamDir string, timeout, interval time.Duration, done func(daemonStatusJSON) bool) (daemonStatusJSON, bool) {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		status := collectDaemonStatus(teamDir)
		if done(status) {
			return status, false
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return status, true
		}
		sleep := interval
		if !deadline.IsZero() {
			if remaining := time.Until(deadline); remaining < sleep {
				sleep = remaining
			}
			if sleep <= 0 {
				return status, true
			}
		}
		time.Sleep(sleep)
	}
}

func renderDaemonStatus(w fmtWriter, status daemonStatusJSON) {
	if !status.Running {
		fmt.Fprintln(w, "agent-teamd: not running")
		if status.StalePidfile {
			fmt.Fprintf(w, "stale pidfile: %s\n", status.Pidfile)
		}
		if status.Error != "" {
			fmt.Fprintf(w, "error: %s\n", status.Error)
		}
		for _, warning := range status.Warnings {
			fmt.Fprintf(w, "warning: %s\n", warning)
		}
		return
	}
	fmt.Fprintf(w, "agent-teamd: running (pid=%d)\n", status.PID)
	fmt.Fprintf(w, "ready: %s\n", yesNo(status.Ready))
	if status.Ready {
		fmt.Fprintf(w, "instances: %d\n", status.Instances)
		if !status.Build.Empty() {
			fmt.Fprintf(w, "build: %s\n", status.Build.Display())
		}
	}
	fmt.Fprintf(w, "socket: %s\n", status.Socket)
	if status.HTTPURL != "" {
		fmt.Fprintf(w, "http: %s\n", status.HTTPURL)
	}
	if status.Error != "" {
		fmt.Fprintf(w, "error: %s\n", status.Error)
	}
	for _, warning := range status.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
}

func appendStatusError(current, next string) string {
	if strings.TrimSpace(current) == "" {
		return next
	}
	return current + "; " + next
}

func collectDaemonStatus(teamDir string) daemonStatusJSON {
	status := daemonStatusJSON{
		TeamDir:      teamDir,
		Socket:       daemon.SocketPath(teamDir),
		HTTPAddrFile: daemon.HTTPAddrPath(teamDir),
		Pidfile:      daemon.PidPath(teamDir),
		Log:          daemon.LogPath(teamDir),
	}
	if _, err := os.Stat(status.Socket); err == nil {
		status.SocketExists = true
	}
	if httpAddr, err := daemon.ReadHTTPAddr(teamDir); err == nil && strings.TrimSpace(httpAddr) != "" {
		status.HTTPAddr = httpAddr
		status.HTTPURL = daemon.DaemonHTTPURL(httpAddr)
		status.HTTPAddrExists = true
	}
	pid, err := daemon.ReadPidfile(status.Pidfile)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	if pid == 0 {
		return status
	}
	status.PID = pid
	if !daemon.PidLiveCheck(pid) {
		status.PID = 0
		status.StalePidfile = true
		return status
	}
	status.Running = true
	if !status.SocketExists {
		status.Error = "daemon socket not found"
		return status
	}
	client, err := newDaemonClientWithTimeout(teamDir, 500*time.Millisecond)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	apiStatus, err := client.Status()
	if err == nil {
		status.Ready = apiStatus.Ready
		if apiStatus.PID != 0 {
			status.PID = apiStatus.PID
		}
		status.Instances = apiStatus.Instances
		status.StartedAt = apiStatus.StartedAt
		status.Build = apiStatus.Build
		return status
	}
	instances, instancesErr := client.Instances()
	if instancesErr != nil {
		status.Error = err.Error()
		return status
	}
	status.Ready = true
	status.Instances = len(instances)
	return status
}

func daemonStatusWarnings(status daemonStatusJSON) []string {
	return daemonBuildMismatchWarnings(status)
}

func daemonBuildMismatchWarnings(status daemonStatusJSON) []string {
	if !status.Running || !status.Ready {
		return nil
	}
	cliBuild := BuildInfo()
	if buildinfo.Equivalent(status.Build, cliBuild) {
		return nil
	}
	started := "unknown start time"
	if !status.StartedAt.IsZero() {
		started = status.StartedAt.Format(time.RFC3339)
	}
	return []string{fmt.Sprintf("daemon runs %s (started %s), CLI is %s — restart the daemon to pick up the new binary",
		status.Build.Display(), started, cliBuild.Display())}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
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

var findAgentTeamd = defaultFindAgentTeamd

// locateAgentTeamd finds the agent-teamd binary. Search order:
//  1. PATH (`exec.LookPath`).
//  2. The same directory as the running agent-team binary (so a `go install`
//     that puts both binaries in the same `$GOBIN` works without a separate
//     PATH entry, and so a release tarball that ships them side-by-side works
//     too).
func locateAgentTeamd(cmd *cobra.Command) (string, error) {
	path, err := findAgentTeamd()
	if err == nil {
		return path, nil
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "agent-team: agent-teamd binary not found.")
	fmt.Fprintln(cmd.ErrOrStderr(), "  Install it alongside agent-team — `go install ./cmd/agent-teamd` if you're building from source.")
	return "", exitErr(127)
}

func defaultFindAgentTeamd() (string, error) {
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
	return "", errors.New("agent-teamd binary not found")
}
