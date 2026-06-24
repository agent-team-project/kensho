package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

var runtimeProbeRunCommand = defaultRuntimeProbeRunCommand
var runtimeProbeRunExecCommand = defaultRuntimeProbeRunExecCommand
var runtimeProbeStartDaemon = daemonStartDetachedOperation

const defaultRuntimeProbeExecPrompt = "Reply exactly with: agent-team runtime probe ok"

func newRuntimeProbeCmd() *cobra.Command {
	var (
		target         string
		jsonOut        bool
		runtimeKind    string
		runtimeBinary  string
		timeout        time.Duration
		daemonInterval time.Duration
		skipDoctor     bool
		execProbe      bool
		execPrompt     string
		output         string
		requireDaemon  bool
		waitDaemon     bool
		startDaemon    bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Probe runtime, daemon, and Codex environment health.",
		Long: "Probe the selected runtime and repo daemon health. For the Codex runtime, " +
			"the probe also runs `codex doctor --json` so provider reachability, auth, " +
			"and sandbox issues are captured before dispatching work. Pass --exec to also " +
			"run a minimal real Codex `exec -` one-shot and verify last-message capture.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime probe: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime probe: --timeout must be >= 0.")
				return exitErr(2)
			}
			if daemonInterval <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime probe: --daemon-interval must be > 0.")
				return exitErr(2)
			}
			tmpl, err := parseRuntimeProbeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime probe: %v\n", err)
				return exitErr(2)
			}
			result, err := collectRuntimeProbe(cmd, runtimeProbeOptions{
				Target:         target,
				RuntimeKind:    runtimeKind,
				RuntimeBinary:  runtimeBinary,
				Timeout:        timeout,
				DaemonInterval: daemonInterval,
				SkipDoctor:     skipDoctor,
				Exec:           execProbe,
				ExecPrompt:     execPrompt,
				RequireDaemon:  requireDaemon,
				WaitDaemon:     waitDaemon,
				StartDaemon:    startDaemon,
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime probe: %v\n", err)
				return exitErr(2)
			}
			if strings.TrimSpace(output) != "" {
				outputPath, err := writeRuntimeProbeOutput(output, result)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime probe: %v\n", err)
					return exitErr(1)
				}
				result.Output = outputPath
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return err
				}
			} else if tmpl != nil {
				if err := renderRuntimeProbeFormat(cmd.OutOrStdout(), result, tmpl); err != nil {
					return err
				}
			} else {
				renderRuntimeProbe(cmd.OutOrStdout(), result)
			}
			if !result.OK {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root or any path under a repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the probe result with a Go template, e.g. '{{.OK}} {{len .Issues}}'.")
	cmd.Flags().StringVar(&runtimeKind, "runtime", "", "Runtime profile to probe for this invocation (claude or codex). Overrides env and repo config.")
	cmd.Flags().StringVar(&runtimeBinary, "runtime-bin", "", "Runtime binary to probe for this invocation. Overrides env and repo config.")
	cmd.Flags().DurationVar(&timeout, "timeout", 20*time.Second, "Maximum time for daemon wait and external runtime diagnostics such as codex doctor --json.")
	cmd.Flags().DurationVar(&daemonInterval, "daemon-interval", 200*time.Millisecond, "Polling interval for --wait-daemon.")
	cmd.Flags().BoolVar(&skipDoctor, "skip-doctor", false, "Skip runtime-native diagnostics such as codex doctor --json.")
	cmd.Flags().BoolVar(&execProbe, "exec", false, "Run a minimal runtime-native execution probe. Currently supports Codex one-shot execution.")
	cmd.Flags().StringVar(&execPrompt, "exec-prompt", defaultRuntimeProbeExecPrompt, "Prompt sent to the runtime when --exec is set.")
	cmd.Flags().StringVar(&output, "output", "", "Write the full probe result as pretty JSON to this file.")
	cmd.Flags().BoolVar(&requireDaemon, "require-daemon", false, "Fail when the repo daemon is not running and ready.")
	cmd.Flags().BoolVar(&waitDaemon, "wait-daemon", false, "Wait for the repo daemon to become ready before reporting daemon health.")
	cmd.Flags().BoolVar(&startDaemon, "start-daemon", false, "Start the detached repo daemon before reporting daemon health when it is not ready.")
	return cmd
}

type runtimeProbeOptions struct {
	Target         string
	RuntimeKind    string
	RuntimeBinary  string
	Timeout        time.Duration
	DaemonInterval time.Duration
	SkipDoctor     bool
	Exec           bool
	ExecPrompt     string
	RequireDaemon  bool
	WaitDaemon     bool
	StartDaemon    bool
}

type runtimeProbeResult struct {
	OK          bool                 `json:"ok"`
	Repo        string               `json:"repo"`
	TeamDir     string               `json:"team_dir,omitempty"`
	Runtime     runtimeInfo          `json:"runtime"`
	Daemon      *daemonStatusJSON    `json:"daemon,omitempty"`
	DaemonStart *daemonLifecycleJSON `json:"daemon_start,omitempty"`
	CodexDoctor *codexDoctorProbe    `json:"codex_doctor,omitempty"`
	ExecProbe   *runtimeExecProbe    `json:"exec_probe,omitempty"`
	Issues      []runtimeProbeIssue  `json:"issues,omitempty"`
	Actions     []string             `json:"actions,omitempty"`
	Output      string               `json:"output,omitempty"`
}

type runtimeProbeIssue struct {
	Severity    string `json:"severity"`
	Source      string `json:"source"`
	ID          string `json:"id,omitempty"`
	Summary     string `json:"summary"`
	Remediation string `json:"remediation,omitempty"`
}

type codexDoctorProbe struct {
	Ran           bool                      `json:"ran"`
	Command       []string                  `json:"command,omitempty"`
	OverallStatus string                    `json:"overall_status,omitempty"`
	CodexVersion  string                    `json:"codex_version,omitempty"`
	Failures      []codexDoctorCheckSummary `json:"failures,omitempty"`
	Warnings      []codexDoctorCheckSummary `json:"warnings,omitempty"`
	Error         string                    `json:"error,omitempty"`
	Stderr        string                    `json:"stderr,omitempty"`
}

type codexDoctorCheckSummary struct {
	ID          string            `json:"id"`
	Category    string            `json:"category,omitempty"`
	Status      string            `json:"status"`
	Summary     string            `json:"summary"`
	Details     map[string]string `json:"details,omitempty"`
	Remediation string            `json:"remediation,omitempty"`
}

type runtimeExecProbe struct {
	Ran                bool     `json:"ran"`
	Runtime            string   `json:"runtime"`
	Command            []string `json:"command,omitempty"`
	Workdir            string   `json:"workdir,omitempty"`
	DurationMillis     int64    `json:"duration_ms,omitempty"`
	ExitCode           int      `json:"exit_code,omitempty"`
	TimedOut           bool     `json:"timed_out,omitempty"`
	LastMessagePresent bool     `json:"last_message_present"`
	LastMessage        string   `json:"last_message,omitempty"`
	Stdout             string   `json:"stdout,omitempty"`
	Stderr             string   `json:"stderr,omitempty"`
	Error              string   `json:"error,omitempty"`
}

type codexDoctorReport struct {
	OverallStatus string                      `json:"overallStatus"`
	CodexVersion  string                      `json:"codexVersion"`
	Checks        map[string]codexDoctorCheck `json:"checks"`
}

type codexDoctorCheck struct {
	ID          string            `json:"id"`
	Category    string            `json:"category"`
	Status      string            `json:"status"`
	Summary     string            `json:"summary"`
	Details     map[string]string `json:"details"`
	Remediation string            `json:"remediation"`
}

type runtimeProbeCommandResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

type runtimeProbeExecCommandResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

func collectRuntimeProbe(cmd *cobra.Command, opts runtimeProbeOptions) (*runtimeProbeResult, error) {
	target := effectiveRepoTarget(cmd, opts.Target)
	repo, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	if eval, err := filepath.EvalSymlinks(repo); err == nil {
		repo = eval
	}
	info, err := collectRuntimeInfoForTargetWithSelection(repo, runtimeSelection{
		Kind:   opts.RuntimeKind,
		Binary: opts.RuntimeBinary,
	})
	if err != nil {
		return nil, err
	}
	result := &runtimeProbeResult{
		OK:      true,
		Repo:    filepath.ToSlash(repo),
		Runtime: info,
	}
	if !info.Available {
		result.addIssue("fail", "runtime", "binary_missing", fmt.Sprintf("runtime binary %q for %s was not found in PATH", info.Binary, info.Runtime), "")
	}

	teamDir := filepath.Join(repo, loader.TeamDirName)
	teamResolved := false
	if resolved, err := resolveTeamDir(cmd, repo); err == nil {
		teamResolved = true
		teamDir = resolved
		status := collectDaemonStatus(teamDir)
		if opts.StartDaemon && !status.Ready {
			startResult, err := runtimeProbeStartDaemon(cmd, teamDir, opts.Timeout)
			if err != nil {
				result.addIssue("fail", "daemon", "start_failed", err.Error(), "Run `agent-team daemon start` and inspect the daemon log.")
			} else {
				result.DaemonStart = &startResult
				status = startResult.Status
				if !status.Ready {
					status = collectDaemonStatus(teamDir)
				}
			}
		}
		if opts.WaitDaemon {
			var timedOut bool
			status, timedOut = waitForDaemonReady(teamDir, opts.Timeout, opts.DaemonInterval)
			if timedOut {
				status.Error = appendStatusError(status.Error, "timed out waiting for daemon readiness")
			}
		}
		result.Daemon = &status
		if !status.Running {
			severity := "warning"
			if opts.RequireDaemon {
				severity = "fail"
			}
			result.addIssue(severity, "daemon", "not_running", "agent-teamd is not running; daemon-backed dispatch, mailbox, and channel flows are unavailable", "Run `agent-team daemon start`.")
		} else if !status.Ready {
			result.addIssue("fail", "daemon", "not_ready", "agent-teamd is running but not ready: "+emptyDash(status.Error), "Run `agent-team daemon status --wait` or inspect the daemon log.")
		}
	} else {
		result.addIssue("fail", "repo", "team_missing", fmt.Sprintf("%s not found", filepath.ToSlash(teamDir)), "Run `agent-team init` first.")
	}
	result.TeamDir = filepath.ToSlash(teamDir)

	if info.Runtime == string(runtimebin.KindCodex) && !opts.SkipDoctor && info.Available {
		probe := runCodexDoctorProbe(cmd.Context(), info.Binary, opts.Timeout)
		result.CodexDoctor = probe
		for _, failure := range probe.Failures {
			severity := "fail"
			if codexDoctorFailureIsNonBlocking(failure) {
				severity = "warning"
			}
			result.addIssue(severity, "codex_doctor", failure.ID, failure.Summary, failure.Remediation)
		}
		for _, warning := range probe.Warnings {
			result.addIssue("warning", "codex_doctor", warning.ID, warning.Summary, warning.Remediation)
		}
		if probe.Error != "" {
			result.addIssue("fail", "codex_doctor", "doctor_failed", probe.Error, "Run `codex doctor --json` directly for the full diagnostic output.")
		}
	}
	if opts.Exec {
		switch {
		case info.Runtime != string(runtimebin.KindCodex):
			result.addIssue("fail", "exec_probe", "unsupported_runtime", "runtime exec probe currently supports Codex only", "Run `agent-team runtime probe --runtime codex --exec`.")
		case !info.Available:
			// binary_missing already records the actionable failure.
		case !teamResolved:
			// team_missing already records the actionable failure.
		default:
			probe := runCodexExecProbe(cmd.Context(), repo, teamDir, result.Daemon, info.Binary, opts.Timeout, opts.ExecPrompt)
			result.ExecProbe = probe
			if probe.Error != "" {
				issueID := runtimeExecProbeIssueID(probe)
				result.addIssue("fail", "exec_probe", issueID, runtimeExecProbeIssueSummary(probe, issueID), runtimeExecProbeRemediation(issueID))
			}
		}
	}
	result.Actions = runtimeProbeActions(result)
	result.OK = runtimeProbeOK(result.Issues)
	return result, nil
}

func codexDoctorFailureIsNonBlocking(check codexDoctorCheckSummary) bool {
	id := strings.ToLower(strings.TrimSpace(check.ID))
	category := strings.ToLower(strings.TrimSpace(check.Category))
	return strings.HasPrefix(id, "terminal.") || category == "terminal"
}

func runCodexDoctorProbe(parent context.Context, binary string, timeout time.Duration) *codexDoctorProbe {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := []string{binary, "doctor", "--json"}
	probe := &codexDoctorProbe{Ran: true, Command: command}
	res := runtimeProbeRunCommand(ctx, binary, "doctor", "--json")
	probe.Stderr = strings.TrimSpace(string(res.Stderr))
	if ctx.Err() != nil {
		probe.Error = ctx.Err().Error()
		return probe
	}
	var report codexDoctorReport
	if len(bytes.TrimSpace(res.Stdout)) == 0 {
		if res.Err != nil {
			probe.Error = res.Err.Error()
		} else {
			probe.Error = "codex doctor returned no JSON output"
		}
		return probe
	}
	if err := json.Unmarshal(res.Stdout, &report); err != nil {
		probe.Error = "decode codex doctor JSON: " + err.Error()
		if res.Err != nil {
			probe.Error = appendRuntimeProbeError(probe.Error, res.Err.Error())
		}
		return probe
	}
	probe.OverallStatus = report.OverallStatus
	probe.CodexVersion = report.CodexVersion
	for _, check := range report.Checks {
		summary := codexDoctorCheckSummary{
			ID:          check.ID,
			Category:    check.Category,
			Status:      check.Status,
			Summary:     check.Summary,
			Details:     check.Details,
			Remediation: check.Remediation,
		}
		switch strings.ToLower(strings.TrimSpace(check.Status)) {
		case "fail", "failed", "error":
			probe.Failures = append(probe.Failures, summary)
		case "warning", "warn":
			probe.Warnings = append(probe.Warnings, summary)
		}
	}
	sort.Slice(probe.Failures, func(i, j int) bool { return probe.Failures[i].ID < probe.Failures[j].ID })
	sort.Slice(probe.Warnings, func(i, j int) bool { return probe.Warnings[i].ID < probe.Warnings[j].ID })
	if strings.EqualFold(report.OverallStatus, "fail") && len(probe.Failures) == 0 {
		probe.Failures = append(probe.Failures, codexDoctorCheckSummary{
			ID:      "overall",
			Status:  report.OverallStatus,
			Summary: "codex doctor reported overall failure",
		})
	}
	return probe
}

func defaultRuntimeProbeRunCommand(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
	c := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return runtimeProbeCommandResult{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
		Err:    err,
	}
}

func defaultRuntimeProbeRunExecCommand(ctx context.Context, binary string, args []string, env []string, cwd, stdin string) runtimeProbeExecCommandResult {
	c := exec.CommandContext(ctx, binary, args...)
	c.Env = append(os.Environ(), env...)
	c.Dir = cwd
	var cleanupStdin func()
	if stdin != "" {
		stdinFile, cleanup, err := openRuntimeStdin(stdin)
		if err != nil {
			return runtimeProbeExecCommandResult{Err: err}
		}
		c.Stdin = stdinFile
		cleanupStdin = cleanup
	}
	if cleanupStdin != nil {
		defer cleanupStdin()
	}
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return runtimeProbeExecCommandResult{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
		Err:    err,
	}
}

func runCodexExecProbe(parent context.Context, repo, teamDir string, status *daemonStatusJSON, binary string, timeout time.Duration, prompt string) *runtimeExecProbe {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = defaultRuntimeProbeExecPrompt
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	start := time.Now()
	probe := &runtimeExecProbe{
		Ran:     true,
		Runtime: string(runtimebin.KindCodex),
		Workdir: filepath.ToSlash(repo),
	}
	stateDir, err := os.MkdirTemp("", "agent-team-runtime-probe-")
	if err != nil {
		probe.Error = "create exec probe state dir: " + err.Error()
		return probe
	}
	defer os.RemoveAll(stateDir)

	socket := ""
	if status != nil {
		socket = status.Socket
	}
	teamEnv := []string{
		"AGENT_TEAM_ROOT=" + teamDir,
		"AGENT_TEAM_INSTANCE=runtime-probe",
		"AGENT_TEAM_STATE_DIR=" + stateDir,
		"AGENT_TEAM_DAEMON_SOCKET=" + socket,
	}
	lastMessagePath := filepath.Join(stateDir, runtimebin.CodexLastMessageFile)
	args := []string{"exec"}
	args = append(args, runtimebin.CodexAgentTeamEnvConfigArgs(teamEnv)...)
	args = append(args, "-C", repo, "--output-last-message", lastMessagePath, "-")
	probe.Command = append([]string{binary}, args...)

	res := runtimeProbeRunExecCommand(ctx, binary, args, teamEnv, repo, prompt)
	probe.DurationMillis = time.Since(start).Milliseconds()
	probe.Stdout = runtimeProbeExcerpt(res.Stdout)
	probe.Stderr = runtimeProbeExcerpt(res.Stderr)
	if ctx.Err() != nil {
		probe.TimedOut = true
		probe.Error = ctx.Err().Error()
		return probe
	}
	if res.Err != nil {
		probe.ExitCode = runtimeProbeExitCode(res.Err)
		probe.Error = res.Err.Error()
		return probe
	}
	lastMessage, err := os.ReadFile(lastMessagePath)
	if err != nil {
		probe.Error = "Codex exec exited successfully but did not write the expected last-message sidecar"
		return probe
	}
	probe.LastMessagePresent = true
	probe.LastMessage = runtimeProbeExcerpt(bytes.TrimSpace(lastMessage))
	if strings.TrimSpace(probe.LastMessage) == "" {
		probe.Error = "Codex exec wrote an empty last-message sidecar"
	}
	return probe
}

func runtimeExecProbeIssueID(probe *runtimeExecProbe) string {
	if probe == nil {
		return "exec_failed"
	}
	if probe.TimedOut {
		return "exec_timeout"
	}
	execText := strings.ToLower(strings.Join([]string{probe.Error, probe.Stderr, probe.Stdout}, "\n"))
	switch {
	case strings.Contains(execText, "could not resolve host"),
		strings.Contains(execText, "failed to lookup"),
		strings.Contains(execText, "provider unavailable"),
		strings.Contains(execText, "provider endpoints are unreachable"),
		strings.Contains(execText, "network error"),
		strings.Contains(execText, "error sending request"),
		strings.Contains(execText, "connection refused"),
		strings.Contains(execText, "connect failed"):
		return "provider_unreachable"
	case strings.Contains(execText, "unauthorized"),
		strings.Contains(execText, "forbidden"),
		strings.Contains(execText, "401"),
		strings.Contains(execText, "403"),
		strings.Contains(execText, "authentication"),
		strings.Contains(execText, "not logged in"),
		strings.Contains(execText, "login required"):
		return "auth_failed"
	case strings.Contains(execText, "operation not permitted"),
		strings.Contains(execText, "permission denied"),
		strings.Contains(execText, "sandbox"):
		return "sandbox_blocked"
	}
	if probe.LastMessagePresent && strings.TrimSpace(probe.LastMessage) == "" {
		return "last_message_empty"
	}
	if !probe.LastMessagePresent && probe.ExitCode == 0 {
		return "last_message_missing"
	}
	return "exec_failed"
}

func runtimeExecProbeIssueSummary(probe *runtimeExecProbe, issueID string) string {
	if probe == nil {
		return "runtime exec probe failed"
	}
	base := strings.TrimSpace(probe.Error)
	switch issueID {
	case "provider_unreachable":
		return appendRuntimeProbeError(base, "stderr indicates provider or network reachability failure")
	case "auth_failed":
		return appendRuntimeProbeError(base, "stderr indicates runtime authentication failure")
	case "sandbox_blocked":
		return appendRuntimeProbeError(base, "stderr indicates sandbox or filesystem/socket permission failure")
	case "exec_timeout":
		if base != "" {
			return base
		}
		return "runtime exec probe timed out"
	case "last_message_empty":
		return "runtime exec wrote an empty last-message sidecar"
	case "last_message_missing":
		return "runtime exec exited successfully but did not write the expected last-message sidecar"
	default:
		if base != "" {
			return base
		}
		return "runtime exec probe failed"
	}
}

func runtimeExecProbeRemediation(issueID string) string {
	switch issueID {
	case "provider_unreachable":
		return "Fix DNS, proxy, VPN, firewall, or provider reachability, then rerun `agent-team runtime probe --runtime codex --exec --timeout 2m`."
	case "auth_failed":
		return "Run `codex login` or refresh the selected runtime credentials, then rerun the exec probe."
	case "sandbox_blocked":
		return "Inspect Codex sandbox and filesystem/socket policy for this repo, then retry with the same `agent-team runtime probe --runtime codex --exec` command."
	case "exec_timeout":
		return "Increase `--timeout` only after checking provider reachability with `codex doctor --json`."
	case "last_message_empty", "last_message_missing":
		return "Run `agent-team run manager --runtime codex --prompt \"probe\" --last-message` and inspect raw Codex output if the sidecar is still missing."
	default:
		return "Run `agent-team run manager --runtime codex --prompt \"probe\" --last-message` and inspect raw Codex output if it still fails."
	}
}

func runtimeProbeExitCode(err error) int {
	if err == nil {
		return 0
	}
	var code ExitCode
	if errors.As(err, &code) {
		return int(code)
	}
	var exitErrTyped *exec.ExitError
	if errors.As(err, &exitErrTyped) {
		return exitErrTyped.ExitCode()
	}
	return -1
}

func runtimeProbeExcerpt(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	const max = 2000
	text := string(body)
	if len(text) <= max {
		return text
	}
	return text[:max] + "...(truncated)"
}

func writeRuntimeProbeOutput(path string, result *runtimeProbeResult) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if path == "-" {
		return "", errors.New("--output must be a file path, not -")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("--output: %w", err)
	}
	outputPath := filepath.ToSlash(abs)
	if result != nil {
		result.Output = outputPath
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("--output: encode probe result: %w", err)
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("--output: mkdir: %w", err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		return "", fmt.Errorf("--output: write: %w", err)
	}
	return outputPath, nil
}

func (r *runtimeProbeResult) addIssue(severity, source, id, summary, remediation string) {
	if r == nil {
		return
	}
	severity = strings.ToLower(strings.TrimSpace(severity))
	if severity == "" {
		severity = "warning"
	}
	r.Issues = append(r.Issues, runtimeProbeIssue{
		Severity:    severity,
		Source:      strings.TrimSpace(source),
		ID:          strings.TrimSpace(id),
		Summary:     strings.TrimSpace(summary),
		Remediation: strings.TrimSpace(remediation),
	})
}

func runtimeProbeOK(issues []runtimeProbeIssue) bool {
	for _, issue := range issues {
		if issue.Severity == "fail" || issue.Severity == "error" {
			return false
		}
	}
	return true
}

func runtimeProbeActions(result *runtimeProbeResult) []string {
	if result == nil {
		return nil
	}
	added := map[string]bool{}
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" || added[action] {
			return
		}
		added[action] = true
	}
	add("agent-team runtime --json")
	if result.Daemon == nil || !result.Daemon.Running {
		add("agent-team daemon start")
	} else if !result.Daemon.Ready {
		add("agent-team daemon status --wait")
	}
	if result.Runtime.Runtime == string(runtimebin.KindCodex) {
		add("codex doctor --summary")
		if result.ExecProbe == nil {
			add("agent-team runtime probe --runtime codex --exec --timeout 2m")
		}
		add("agent-team run manager --runtime codex --prompt \"probe\" --last-message")
	}
	actions := make([]string, 0, len(added))
	for action := range added {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}

func appendRuntimeProbeError(current, next string) string {
	if strings.TrimSpace(current) == "" {
		return next
	}
	if strings.TrimSpace(next) == "" {
		return current
	}
	return current + "; " + next
}

func parseRuntimeProbeFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("runtime-probe-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderRuntimeProbeFormat(w io.Writer, result *runtimeProbeResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderRuntimeProbe(w io.Writer, result *runtimeProbeResult) {
	if result == nil {
		fmt.Fprintln(w, "runtime probe: unavailable")
		return
	}
	state := "ok"
	if len(result.Issues) > 0 {
		state = "attention"
	}
	if !result.OK {
		state = "failed"
	}
	fmt.Fprintf(w, "runtime probe: %s\n", state)
	fmt.Fprintf(w, "repo: %s\n", result.Repo)
	if result.TeamDir != "" {
		fmt.Fprintf(w, "team_dir: %s\n", result.TeamDir)
	}
	if result.Output != "" {
		fmt.Fprintf(w, "output: %s\n", result.Output)
	}
	fmt.Fprintf(w, "runtime: %s binary=%s available=%s\n", result.Runtime.Runtime, result.Runtime.Binary, yesNo(result.Runtime.Available))
	if result.Runtime.Path != "" {
		fmt.Fprintf(w, "runtime_path: %s\n", result.Runtime.Path)
	}
	if result.Daemon != nil {
		fmt.Fprintf(w, "daemon: running=%s ready=%s socket=%s\n", yesNo(result.Daemon.Running), yesNo(result.Daemon.Ready), result.Daemon.Socket)
		if result.Daemon.Error != "" {
			fmt.Fprintf(w, "daemon_error: %s\n", result.Daemon.Error)
		}
	}
	if result.CodexDoctor != nil {
		fmt.Fprintf(w, "codex_doctor: status=%s version=%s failures=%d warnings=%d\n",
			emptyDash(result.CodexDoctor.OverallStatus),
			emptyDash(result.CodexDoctor.CodexVersion),
			len(result.CodexDoctor.Failures),
			len(result.CodexDoctor.Warnings),
		)
		if result.CodexDoctor.Error != "" {
			fmt.Fprintf(w, "codex_doctor_error: %s\n", result.CodexDoctor.Error)
		}
	}
	if result.ExecProbe != nil {
		state := "ok"
		if result.ExecProbe.TimedOut {
			state = "timed_out"
		} else if result.ExecProbe.Error != "" {
			state = "failed"
		}
		fmt.Fprintf(w, "exec_probe: status=%s runtime=%s exit=%d last_message=%s duration=%dms\n",
			state,
			result.ExecProbe.Runtime,
			result.ExecProbe.ExitCode,
			yesNo(result.ExecProbe.LastMessagePresent),
			result.ExecProbe.DurationMillis,
		)
		if result.ExecProbe.Error != "" {
			fmt.Fprintf(w, "exec_probe_error: %s\n", result.ExecProbe.Error)
		}
		if result.ExecProbe.LastMessage != "" {
			fmt.Fprintf(w, "exec_probe_last_message: %s\n", result.ExecProbe.LastMessage)
		}
		if result.ExecProbe.Stdout != "" {
			fmt.Fprintf(w, "exec_probe_stdout: %s\n", result.ExecProbe.Stdout)
		}
		if result.ExecProbe.Stderr != "" {
			fmt.Fprintf(w, "exec_probe_stderr: %s\n", result.ExecProbe.Stderr)
		}
	}
	if len(result.Issues) > 0 {
		fmt.Fprintln(w, "issues:")
		for _, issue := range result.Issues {
			id := issue.ID
			if id == "" {
				id = issue.Source
			}
			fmt.Fprintf(w, "  [%s] %s: %s\n", issue.Severity, id, issue.Summary)
			if issue.Remediation != "" {
				fmt.Fprintf(w, "    %s\n", issue.Remediation)
			}
		}
	}
	if len(result.Actions) > 0 {
		fmt.Fprintln(w, "actions:")
		for _, action := range result.Actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
}
