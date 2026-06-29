package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

func withRuntimeProbeRunCommand(t *testing.T, fn func(context.Context, string, ...string) runtimeProbeCommandResult) {
	t.Helper()
	old := runtimeProbeRunCommand
	runtimeProbeRunCommand = fn
	t.Cleanup(func() { runtimeProbeRunCommand = old })
}

func withRuntimeProbeRunExecCommand(t *testing.T, fn func(context.Context, string, []string, []string, string, string) runtimeProbeExecCommandResult) {
	t.Helper()
	old := runtimeProbeRunExecCommand
	runtimeProbeRunExecCommand = fn
	t.Cleanup(func() { runtimeProbeRunExecCommand = old })
}

func withRuntimeProbeStartDaemon(t *testing.T, fn func(*cobra.Command, string, time.Duration, string) (daemonLifecycleJSON, error)) {
	t.Helper()
	old := runtimeProbeStartDaemon
	runtimeProbeStartDaemon = fn
	t.Cleanup(func() { runtimeProbeStartDaemon = old })
}

func TestRuntimeProbeDoctorAlias(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "sh" {
			t.Fatalf("look path bin = %q, want sh", bin)
		}
		return "/bin/sh", nil
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "doctor", "--target", tmp, "--runtime", "codex", "--runtime-bin", "sh", "--skip-doctor", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime doctor alias: %v\nstderr=%s", err, stderr.String())
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode runtime doctor json: %v\nbody=%s", err, out.String())
	}
	if result.Runtime.Runtime != "codex" || result.Runtime.Binary != "sh" {
		t.Fatalf("runtime = %+v, want codex sh", result.Runtime)
	}
}

func TestRuntimeProbeCodexDoctorFailureJSON(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-dev")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-dev" {
			t.Fatalf("look path bin = %q, want codex-dev", bin)
		}
		return "/usr/local/bin/codex-dev", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		if binary != "codex-dev" || strings.Join(args, " ") != "doctor --json" {
			t.Fatalf("probe command = %s %v", binary, args)
		}
		return runtimeProbeCommandResult{Stdout: []byte(`{
		  "overallStatus": "fail",
		  "codexVersion": "0.141.0",
		  "checks": {
		    "network.provider_reachability": {
		      "id": "network.provider_reachability",
		      "category": "reachability",
		      "status": "fail",
		      "summary": "one or more required provider endpoints are unreachable over HTTP",
		      "details": {"ChatGPT base URL": "https://chatgpt.com/backend-api/ connect failed (required)"},
		      "remediation": "Check proxy, VPN, firewall, DNS, and custom CA configuration."
		    },
		    "network.websocket_reachability": {
		      "id": "network.websocket_reachability",
		      "category": "websocket",
		      "status": "warning",
		      "summary": "Responses WebSocket failed; HTTPS fallback may still work",
		      "details": {"DNS": "lookup failed"},
		      "remediation": "Check proxy, VPN, firewall, DNS, custom CA, and WebSocket policy support."
		    },
		    "terminal.env": {
		      "id": "terminal.env",
		      "category": "terminal",
		      "status": "fail",
		      "summary": "TERM=dumb - colors and cursor control are disabled",
		      "details": {"TERM": "dumb"},
		      "remediation": "Set TERM to a real value."
		    },
		    "auth.credentials": {
		      "id": "auth.credentials",
		      "category": "auth",
		      "status": "ok",
		      "summary": "auth is configured"
		    }
		  }
		}`)}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe succeeded, want exit 1")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("error = %v, want exit 1", err)
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if result.OK || result.Runtime.Runtime != "codex" || result.CodexDoctor == nil {
		t.Fatalf("result = %+v, want failed codex probe", result)
	}
	if len(result.CodexDoctor.Failures) != 2 || result.CodexDoctor.Failures[0].ID != "network.provider_reachability" || result.CodexDoctor.Failures[1].ID != "terminal.env" {
		t.Fatalf("failures = %+v", result.CodexDoctor.Failures)
	}
	if len(result.CodexDoctor.Warnings) != 1 || result.CodexDoctor.Warnings[0].ID != "network.websocket_reachability" {
		t.Fatalf("warnings = %+v", result.CodexDoctor.Warnings)
	}
	if !containsRuntimeProbeIssue(result.Issues, "fail", "codex_doctor", "network.provider_reachability") {
		t.Fatalf("issues = %+v, want provider reachability failure", result.Issues)
	}
	if !containsRuntimeProbeIssue(result.Issues, "warning", "codex_doctor", "terminal.env") {
		t.Fatalf("issues = %+v, want terminal failure downgraded to warning", result.Issues)
	}
	if !containsString(result.Actions, "codex doctor --summary") {
		t.Fatalf("actions = %+v, want codex doctor hint", result.Actions)
	}
}

func TestRuntimeProbeSkipDoctorWarningsDoNotFail(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime probe warning-only failed: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{"runtime probe: attention", "runtime: codex", "[warning] not_running", "agent-team daemon start"} {
		if !strings.Contains(body, want) {
			t.Fatalf("probe output missing %q:\n%s", want, body)
		}
	}
}

func TestRuntimeProbeCommands(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime probe commands: %v\nstderr=%s", err, stderr.String())
	}
	want := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "daemon", "start"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp}), " ") + " run manager --runtime codex --prompt \"probe\" --last-message",
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "runtime", "--json"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "runtime", "probe", "--runtime", "codex", "--exec", "--timeout", "2m"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "runtime", "probe", "--runtime", "codex", "--start-daemon", "--daemon-http-addr", "127.0.0.1:0", "--exec-http-check", "--timeout", "2m"}), " "),
		"codex doctor --summary",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Fatalf("runtime probe commands = %q, want %q", got, want)
	}
	if strings.Contains(out.String(), "runtime probe:") || strings.Contains(out.String(), "actions:") {
		t.Fatalf("runtime probe commands included prose:\n%s", out.String())
	}
}

func TestRuntimeProbeCommandsForMissingTeamOnlySuggestInit(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--runtime", "codex", "--skip-doctor", "--commands"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime probe commands succeeded, want missing-team failure")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	resolvedTmp := tmp
	if eval, err := filepath.EvalSymlinks(tmp); err == nil {
		resolvedTmp = eval
	}
	want := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "init", "--target", resolvedTmp}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "runtime", "--json"}), " "),
		"codex doctor --summary",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Fatalf("runtime probe missing-team commands = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("runtime probe missing-team commands should not write stderr: %s", stderr.String())
	}
}

func TestRuntimeProbeActionsPreferHTTPCheckWhenAvailable(t *testing.T) {
	result := &runtimeProbeResult{
		Runtime: runtimeInfo{Runtime: "codex", Available: true},
		Daemon: &daemonStatusJSON{
			Running: true,
			Ready:   true,
			HTTPURL: "http://127.0.0.1:49152",
			Socket:  "/tmp/agent-team.sock",
		},
	}

	actions := runtimeProbeActions(result)
	if !containsString(actions, "agent-team runtime probe --runtime codex --exec-http-check --timeout 2m") {
		t.Fatalf("actions = %+v, want HTTP exec check", actions)
	}
	if containsString(actions, "agent-team runtime probe --runtime codex --exec-socket-check --timeout 2m") {
		t.Fatalf("actions = %+v, did not expect socket exec check when HTTP URL is available", actions)
	}
}

func TestRuntimeProbeActionsUseSocketCheckWithoutHTTP(t *testing.T) {
	result := &runtimeProbeResult{
		Runtime: runtimeInfo{Runtime: "codex", Available: true},
		Daemon: &daemonStatusJSON{
			Running: true,
			Ready:   true,
			Socket:  "/tmp/agent-team.sock",
		},
	}

	actions := runtimeProbeActions(result)
	if !containsString(actions, "agent-team runtime probe --runtime codex --exec-socket-check --timeout 2m") {
		t.Fatalf("actions = %+v, want socket exec check fallback", actions)
	}
	if !containsString(actions, "agent-team runtime probe --runtime codex --start-daemon --daemon-http-addr 127.0.0.1:0 --exec-http-check --timeout 2m") {
		t.Fatalf("actions = %+v, want HTTP daemon-start probe hint", actions)
	}
}

func TestRuntimeProbeActionsSkipCodexExecutionWhenUnavailable(t *testing.T) {
	result := &runtimeProbeResult{
		Runtime: runtimeInfo{
			Runtime:   "codex",
			Binary:    "missing-codex",
			Available: false,
		},
		Daemon: &daemonStatusJSON{
			Running: false,
			Ready:   false,
		},
	}

	actions := runtimeProbeActions(result)
	for _, want := range []string{"agent-team runtime --json", "agent-team daemon start"} {
		if !containsString(actions, want) {
			t.Fatalf("actions = %+v, missing %q", actions, want)
		}
	}
	for _, disallowed := range []string{"codex doctor", "agent-team run manager", "agent-team runtime probe --runtime codex --exec"} {
		for _, action := range actions {
			if strings.Contains(action, disallowed) {
				t.Fatalf("actions = %+v, should not include unavailable Codex action containing %q", actions, disallowed)
			}
		}
	}
}

func TestRuntimeProbeActionsPreferInitWhenTeamMissing(t *testing.T) {
	repo := "/tmp/runtime probe repo"
	result := &runtimeProbeResult{
		Repo:    repo,
		Runtime: runtimeInfo{Runtime: "codex", Binary: "codex", Available: true},
		Issues: []runtimeProbeIssue{{
			Severity: "fail",
			Source:   "repo",
			ID:       "team_missing",
			Summary:  "missing .agent_team",
		}},
	}

	actions := runtimeProbeActions(result)
	wantInit := strings.Join(shellQuoteArgs([]string{"agent-team", "init", "--target", repo}), " ")
	for _, want := range []string{wantInit, "agent-team runtime --json", "codex doctor --summary"} {
		if !containsString(actions, want) {
			t.Fatalf("actions = %+v, missing %q", actions, want)
		}
	}
	for _, disallowed := range []string{"agent-team daemon start", "agent-team run manager", "agent-team runtime probe --runtime codex --exec"} {
		for _, action := range actions {
			if strings.Contains(action, disallowed) {
				t.Fatalf("actions = %+v, should not include pre-init action containing %q", actions, disallowed)
			}
		}
	}
}

func TestRuntimeProbeFormat(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--format", "{{.OK}} {{.Runtime.Runtime}} {{len .Issues}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime probe format: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "true codex 1\n"; got != want {
		t.Fatalf("runtime probe format = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("runtime probe format stderr = %q", stderr.String())
	}
}

func TestRuntimeProbeFormatValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format with json",
			args: []string{"runtime", "probe", "--format", "{{.OK}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "commands with json",
			args: []string{"runtime", "probe", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "commands with format",
			args: []string{"runtime", "probe", "--commands", "--format", "{{.OK}}"},
			want: "--commands cannot be combined with --format",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("runtime probe accepted invalid output flags")
			}
			var ec ExitCode
			if !errors.As(err, &ec) || int(ec) != 2 {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("stdout = %q", out.String())
			}
		})
	}
}

func TestRuntimeProbeRequireDaemonFailsWhenStopped(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--require-daemon", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe succeeded, want daemon-required failure")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("error = %v, want exit 1", err)
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if result.OK || !containsRuntimeProbeIssue(result.Issues, "fail", "daemon", "not_running") {
		t.Fatalf("result = %+v, want failing daemon issue", result)
	}
}

func TestRuntimeProbeWaitDaemonTimesOut(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--require-daemon", "--wait-daemon", "--timeout", "1ms", "--daemon-interval", "1ms", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe wait daemon succeeded, want timeout failure")
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if result.OK || result.Daemon == nil || !strings.Contains(result.Daemon.Error, "timed out waiting for daemon readiness") {
		t.Fatalf("result = %+v, want daemon timeout", result)
	}
	if !containsRuntimeProbeIssue(result.Issues, "fail", "daemon", "not_running") {
		t.Fatalf("issues = %+v, want daemon not_running failure", result.Issues)
	}
}

func TestRuntimeProbeStartDaemon(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})
	withRuntimeProbeStartDaemon(t, func(cmd *cobra.Command, teamDir string, timeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
		if timeout != 20*time.Second {
			t.Fatalf("timeout = %s, want default 20s", timeout)
		}
		return daemonLifecycleJSON{
			Action:  "start",
			Changed: true,
			PID:     1234,
			Log:     filepath.Join(teamDir, "daemon", "agent-teamd.log"),
			Message: "started",
			Status: daemonStatusJSON{
				Running: true,
				Ready:   true,
				PID:     1234,
				TeamDir: filepath.ToSlash(teamDir),
				Socket:  filepath.Join(teamDir, "daemon.sock"),
				Log:     filepath.Join(teamDir, "daemon", "agent-teamd.log"),
			},
		}, nil
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--start-daemon", "--require-daemon", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime probe start daemon failed: %v\nstderr=%s", err, stderr.String())
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if !result.OK || result.Daemon == nil || !result.Daemon.Ready || result.DaemonStart == nil || !result.DaemonStart.Changed {
		t.Fatalf("result = %+v, want started ready daemon", result)
	}
}

func TestRuntimeProbeStartDaemonFailure(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})
	withRuntimeProbeStartDaemon(t, func(cmd *cobra.Command, teamDir string, timeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
		return daemonLifecycleJSON{}, errors.New("spawn failed")
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--start-daemon", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe start daemon succeeded, want failure")
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if result.OK || !containsRuntimeProbeIssue(result.Issues, "fail", "daemon", "start_failed") {
		t.Fatalf("result = %+v, want start_failed issue", result)
	}
}

func TestRuntimeProbeRejectsInvalidWaitFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "negative timeout", args: []string{"runtime", "probe", "--timeout", "-1s"}, want: "--timeout must be >= 0"},
		{name: "zero daemon interval", args: []string{"runtime", "probe", "--daemon-interval", "0s"}, want: "--daemon-interval must be > 0"},
		{name: "exec prompt conflict", args: []string{"runtime", "probe", "--exec-prompt", "hello", "--exec-prompt-file", "prompt.txt"}, want: "provide exec prompt using only one of --exec-prompt or --exec-prompt-file"},
		{name: "socket check prompt conflict", args: []string{"runtime", "probe", "--exec-socket-check", "--exec-prompt", "hello"}, want: "--exec-socket-check cannot be combined"},
		{name: "missing exec prompt file", args: []string{"runtime", "probe", "--exec-prompt-file", filepath.Join(t.TempDir(), "missing.txt")}, want: "--exec-prompt-file:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("runtime probe succeeded, want validation error")
			}
			var ec ExitCode
			if !errors.As(err, &ec) || int(ec) != 2 {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestRuntimeProbeOutputWritesDiagnosticFile(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})
	outPath := filepath.Join(tmp, "diagnostics", "runtime-probe.json")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--output", outPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime probe output failed: %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "output: "+filepath.ToSlash(outPath)) {
		t.Fatalf("text output missing diagnostic path:\n%s", out.String())
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode output: %v\nbody=%s", err, string(body))
	}
	if result.Output != filepath.ToSlash(outPath) || result.Runtime.Runtime != "codex" || !result.Runtime.Available {
		t.Fatalf("result = %+v", result)
	}
	if !containsRuntimeProbeIssue(result.Issues, "warning", "daemon", "not_running") {
		t.Fatalf("issues = %+v, want daemon warning", result.Issues)
	}
}

func TestRuntimeProbeCodexExecProbeSuccess(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-dev")
	execPromptFile := filepath.Join(tmp, "exec-prompt.txt")
	if err := os.WriteFile(execPromptFile, []byte("Reply exactly with: custom runtime probe ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedTmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-dev" {
			t.Fatalf("look path bin = %q, want codex-dev", bin)
		}
		return "/usr/local/bin/codex-dev", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})
	withRuntimeProbeRunExecCommand(t, func(ctx context.Context, binary string, args []string, env []string, cwd, stdin string) runtimeProbeExecCommandResult {
		if binary != "codex-dev" {
			t.Fatalf("exec binary = %q, want codex-dev", binary)
		}
		if cwd != resolvedTmp {
			t.Fatalf("exec cwd = %q, want %q", cwd, resolvedTmp)
		}
		if !strings.Contains(stdin, "custom runtime probe ok") {
			t.Fatalf("exec stdin = %q, want probe prompt", stdin)
		}
		if len(args) == 0 || args[0] != "exec" || args[len(args)-1] != "-" {
			t.Fatalf("exec args = %#v, want codex exec ... -", args)
		}
		if !containsString(args, "-C") || !containsString(args, "--skip-git-repo-check") || !containsString(args, "--output-last-message") {
			t.Fatalf("exec args = %#v, want repo and last-message flags", args)
		}
		if !containsString(env, "AGENT_TEAM_INSTANCE=runtime-probe") {
			t.Fatalf("exec env = %#v, want runtime-probe instance", env)
		}
		lastMessage := ""
		for i := range args {
			if args[i] == "--output-last-message" && i+1 < len(args) {
				lastMessage = args[i+1]
				break
			}
		}
		if lastMessage == "" {
			t.Fatalf("exec args = %#v, missing last-message path", args)
		}
		if err := os.WriteFile(lastMessage, []byte("custom runtime probe ok\n"), 0o644); err != nil {
			t.Fatalf("write last-message: %v", err)
		}
		return runtimeProbeExecCommandResult{
			Stdout: []byte("raw runtime output\n"),
			Stderr: []byte("diagnostic warning\n"),
		}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--exec", "--exec-prompt-file", execPromptFile, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime probe exec failed: %v\nstderr=%s", err, stderr.String())
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if !result.OK || result.ExecProbe == nil || !result.ExecProbe.LastMessagePresent {
		t.Fatalf("result = %+v, want successful exec probe", result)
	}
	if got := result.ExecProbe.LastMessage; got != "custom runtime probe ok" {
		t.Fatalf("last message = %q", got)
	}
	if !containsString(result.Actions, "codex doctor --summary") {
		t.Fatalf("actions = %+v, want codex doctor hint", result.Actions)
	}
}

func TestRuntimeProbeCodexExecSocketCheckSuccess(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-dev")
	socketPath := filepath.Join(tmp, ".agent_team", "daemon.sock")
	resolvedTmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-dev" {
			t.Fatalf("look path bin = %q, want codex-dev", bin)
		}
		return "/usr/local/bin/codex-dev", nil
	})
	withRuntimeProbeRunCommand(t, func(ctx context.Context, binary string, args ...string) runtimeProbeCommandResult {
		t.Fatalf("codex doctor should be skipped")
		return runtimeProbeCommandResult{}
	})
	withRuntimeProbeStartDaemon(t, func(cmd *cobra.Command, teamDir string, timeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
		return daemonLifecycleJSON{
			Action:  "start",
			Changed: true,
			PID:     1234,
			Status: daemonStatusJSON{
				Running: true,
				Ready:   true,
				PID:     1234,
				TeamDir: filepath.ToSlash(teamDir),
				Socket:  socketPath,
			},
		}, nil
	})
	withRuntimeProbeRunExecCommand(t, func(ctx context.Context, binary string, args []string, env []string, cwd, stdin string) runtimeProbeExecCommandResult {
		if binary != "codex-dev" {
			t.Fatalf("exec binary = %q, want codex-dev", binary)
		}
		if cwd != resolvedTmp {
			t.Fatalf("exec cwd = %q, want %q", cwd, resolvedTmp)
		}
		for _, want := range []string{"AGENT_TEAM_DAEMON_SOCKET", "/v1/instances", runtimeProbeSocketCheckSuccessReply} {
			if !strings.Contains(stdin, want) {
				t.Fatalf("socket-check stdin missing %q:\n%s", want, stdin)
			}
		}
		if !containsString(env, "AGENT_TEAM_DAEMON_SOCKET="+socketPath) {
			t.Fatalf("exec env = %#v, want daemon socket", env)
		}
		lastMessage := ""
		for i := range args {
			if args[i] == "--output-last-message" && i+1 < len(args) {
				lastMessage = args[i+1]
				break
			}
		}
		if lastMessage == "" {
			t.Fatalf("exec args = %#v, missing last-message path", args)
		}
		if err := os.WriteFile(lastMessage, []byte(runtimeProbeSocketCheckSuccessReply+"\n"), 0o644); err != nil {
			t.Fatalf("write last-message: %v", err)
		}
		return runtimeProbeExecCommandResult{}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--start-daemon", "--exec-socket-check", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime probe exec socket check failed: %v\nstderr=%s", err, stderr.String())
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if !result.OK || result.ExecProbe == nil || !result.ExecProbe.SocketCheck || result.ExecProbe.DaemonSocket != socketPath {
		t.Fatalf("result = %+v, want successful socket exec probe", result)
	}
	if got := result.ExecProbe.LastMessage; got != runtimeProbeSocketCheckSuccessReply {
		t.Fatalf("last message = %q", got)
	}
}

func TestRuntimeProbeCodexExecHTTPCheckSuccess(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	socketPath := filepath.Join(tmp, ".agent_team", "daemon.sock")
	httpURL := "http://127.0.0.1:49152"
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeStartDaemon(t, func(cmd *cobra.Command, teamDir string, timeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
		if httpAddr != "127.0.0.1:0" {
			t.Fatalf("http addr = %q, want 127.0.0.1:0", httpAddr)
		}
		return daemonLifecycleJSON{
			Action: "start",
			PID:    1234,
			Status: daemonStatusJSON{
				Running:  true,
				Ready:    true,
				PID:      1234,
				TeamDir:  filepath.ToSlash(teamDir),
				Socket:   socketPath,
				HTTPAddr: "127.0.0.1:49152",
				HTTPURL:  httpURL,
			},
		}, nil
	})
	withRuntimeProbeRunExecCommand(t, func(ctx context.Context, binary string, args []string, env []string, cwd, stdin string) runtimeProbeExecCommandResult {
		for _, want := range []string{"AGENT_TEAM_DAEMON_URL", "/v1/instances", runtimeProbeHTTPCheckSuccessReply} {
			if !strings.Contains(stdin, want) {
				t.Fatalf("HTTP-check stdin missing %q:\n%s", want, stdin)
			}
		}
		if !containsString(env, "AGENT_TEAM_DAEMON_URL="+httpURL) {
			t.Fatalf("exec env = %#v, want daemon URL", env)
		}
		lastMessage := ""
		for i := range args {
			if args[i] == "--output-last-message" && i+1 < len(args) {
				lastMessage = args[i+1]
				break
			}
		}
		if lastMessage == "" {
			t.Fatalf("exec args = %#v, missing last-message path", args)
		}
		if err := os.WriteFile(lastMessage, []byte(runtimeProbeHTTPCheckSuccessReply+"\n"), 0o644); err != nil {
			t.Fatalf("write last-message: %v", err)
		}
		return runtimeProbeExecCommandResult{}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--start-daemon", "--daemon-http-addr", "127.0.0.1:0", "--exec-http-check", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime probe exec HTTP check failed: %v\nstderr=%s", err, stderr.String())
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if !result.OK || result.ExecProbe == nil || !result.ExecProbe.HTTPCheck || result.ExecProbe.DaemonURL != httpURL {
		t.Fatalf("result = %+v, want successful HTTP exec probe", result)
	}
	if got := result.ExecProbe.LastMessage; got != runtimeProbeHTTPCheckSuccessReply {
		t.Fatalf("last message = %q", got)
	}
}

func TestRuntimeProbeCodexExecHTTPCheckRequiresHTTPURL(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeStartDaemon(t, func(cmd *cobra.Command, teamDir string, timeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
		return daemonLifecycleJSON{
			Action: "start",
			PID:    1234,
			Status: daemonStatusJSON{
				Running: true,
				Ready:   true,
				PID:     1234,
				TeamDir: filepath.ToSlash(teamDir),
				Socket:  filepath.Join(tmp, ".agent_team", "daemon.sock"),
			},
		}, nil
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--start-daemon", "--exec-http-check", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe HTTP check succeeded unexpectedly")
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s stderr=%s", err, out.String(), stderr.String())
	}
	if !containsRuntimeProbeIssue(result.Issues, "fail", "exec_probe", "daemon_http_not_enabled") {
		t.Fatalf("issues = %+v, want daemon_http_not_enabled", result.Issues)
	}
}

func TestRuntimeProbeCodexExecSocketCheckFailure(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	socketPath := filepath.Join(tmp, ".agent_team", "daemon.sock")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeStartDaemon(t, func(cmd *cobra.Command, teamDir string, timeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
		return daemonLifecycleJSON{
			Action: "start",
			PID:    1234,
			Status: daemonStatusJSON{
				Running: true,
				Ready:   true,
				PID:     1234,
				TeamDir: filepath.ToSlash(teamDir),
				Socket:  socketPath,
			},
		}, nil
	})
	withRuntimeProbeRunExecCommand(t, func(ctx context.Context, binary string, args []string, env []string, cwd, stdin string) runtimeProbeExecCommandResult {
		lastMessage := ""
		for i := range args {
			if args[i] == "--output-last-message" && i+1 < len(args) {
				lastMessage = args[i+1]
				break
			}
		}
		if lastMessage == "" {
			t.Fatalf("exec args = %#v, missing last-message path", args)
		}
		if err := os.WriteFile(lastMessage, []byte("daemon returned HTTP 503\n"), 0o644); err != nil {
			t.Fatalf("write last-message: %v", err)
		}
		return runtimeProbeExecCommandResult{Stdout: []byte("raw output")}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--start-daemon", "--exec-socket-check", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe socket check succeeded unexpectedly")
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if result.OK || result.ExecProbe == nil || !result.ExecProbe.SocketCheck {
		t.Fatalf("result = %+v, want failed socket check", result)
	}
	if !containsRuntimeProbeIssue(result.Issues, "fail", "exec_probe", "socket_check_failed") {
		t.Fatalf("issues = %+v, want socket_check_failed", result.Issues)
	}
}

func TestRuntimeProbeCodexExecSocketCheckPermissionFailure(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	socketPath := filepath.Join(tmp, ".agent_team", "daemon.sock")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeStartDaemon(t, func(cmd *cobra.Command, teamDir string, timeout time.Duration, httpAddr string) (daemonLifecycleJSON, error) {
		return daemonLifecycleJSON{
			Action: "start",
			PID:    1234,
			Status: daemonStatusJSON{
				Running: true,
				Ready:   true,
				PID:     1234,
				TeamDir: filepath.ToSlash(teamDir),
				Socket:  socketPath,
			},
		}, nil
	})
	withRuntimeProbeRunExecCommand(t, func(ctx context.Context, binary string, args []string, env []string, cwd, stdin string) runtimeProbeExecCommandResult {
		lastMessage := ""
		for i := range args {
			if args[i] == "--output-last-message" && i+1 < len(args) {
				lastMessage = args[i+1]
				break
			}
		}
		if lastMessage == "" {
			t.Fatalf("exec args = %#v, missing last-message path", args)
		}
		msg := "Failure: `daemon socket check failed: [Errno 1] Operation not permitted`\n"
		if err := os.WriteFile(lastMessage, []byte(msg), 0o644); err != nil {
			t.Fatalf("write last-message: %v", err)
		}
		return runtimeProbeExecCommandResult{Stdout: []byte(msg)}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--start-daemon", "--exec-socket-check", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe socket check succeeded unexpectedly")
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if result.OK || result.ExecProbe == nil || !result.ExecProbe.SocketCheck {
		t.Fatalf("result = %+v, want failed socket check", result)
	}
	if !containsRuntimeProbeIssue(result.Issues, "fail", "exec_probe", "sandbox_blocked") {
		t.Fatalf("issues = %+v, want sandbox_blocked", result.Issues)
	}
}

func TestRuntimeProbeCodexExecProbeFailure(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})
	withRuntimeProbeRunExecCommand(t, func(ctx context.Context, binary string, args []string, env []string, cwd, stdin string) runtimeProbeExecCommandResult {
		return runtimeProbeExecCommandResult{Stderr: []byte("provider unavailable"), Err: ExitCode(42)}
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--exec", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe exec succeeded, want exit 1")
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if result.OK || result.ExecProbe == nil || result.ExecProbe.ExitCode != 42 {
		t.Fatalf("result = %+v, want failed exec probe exit 42", result)
	}
	if !containsRuntimeProbeIssue(result.Issues, "fail", "exec_probe", "provider_unreachable") {
		t.Fatalf("issues = %+v, want provider reachability failure", result.Issues)
	}
}

func TestRuntimeExecProbeClassifiesFailures(t *testing.T) {
	tests := []struct {
		name  string
		probe *runtimeExecProbe
		want  string
	}{
		{
			name:  "provider dns",
			probe: &runtimeExecProbe{Error: "exit status 1", Stderr: "fatal: unable to access 'https://github.com/openai/plugins.git/': Could not resolve host: github.com"},
			want:  "provider_unreachable",
		},
		{
			name:  "auth",
			probe: &runtimeExecProbe{Error: "exit status 1", Stderr: "401 unauthorized: login required"},
			want:  "auth_failed",
		},
		{
			name:  "nonfatal plugin warm auth warning",
			probe: &runtimeExecProbe{Error: "exit status 1", ExitCode: 1, Stderr: `WARN codex_core_plugins::manager: failed to warm featured plugin ids cache error=remote featured plugin request failed with status 401 Unauthorized: {"detail":"Unauthorized"}`},
			want:  "exec_failed",
		},
		{
			name:  "sandbox",
			probe: &runtimeExecProbe{Error: "exit status 1", Stderr: "operation not permitted while opening daemon socket"},
			want:  "sandbox_blocked",
		},
		{
			name:  "socket check sandbox last message",
			probe: &runtimeExecProbe{SocketCheck: true, LastMessagePresent: true, LastMessage: "Failure: `daemon socket check failed: [Errno 1] Operation not permitted`", Error: `Codex exec socket check did not confirm daemon socket access; expected final message "agent-team daemon socket ok"`},
			want:  "sandbox_blocked",
		},
		{
			name:  "HTTP check sandbox last message",
			probe: &runtimeExecProbe{HTTPCheck: true, LastMessagePresent: true, LastMessage: "Failed: `daemon HTTP check failed: <urlopen error [Errno 1] Operation not permitted>`", Stderr: `WARN codex_core_plugins::manager: failed to warm featured plugin ids cache error=remote featured plugin request failed with status 401 Unauthorized: {"detail":"Unauthorized"}`, Error: `Codex exec HTTP check did not confirm daemon HTTP access; expected final message "agent-team daemon http ok"`},
			want:  "sandbox_blocked",
		},
		{
			name:  "generic socket check failure",
			probe: &runtimeExecProbe{SocketCheck: true, LastMessagePresent: true, LastMessage: "daemon returned HTTP 503", Error: `Codex exec socket check did not confirm daemon socket access; expected final message "agent-team daemon socket ok"`},
			want:  "socket_check_failed",
		},
		{
			name:  "generic HTTP check failure",
			probe: &runtimeExecProbe{HTTPCheck: true, LastMessagePresent: true, LastMessage: "daemon returned HTTP 503", Error: `Codex exec HTTP check did not confirm daemon HTTP access; expected final message "agent-team daemon http ok"`},
			want:  "http_check_failed",
		},
		{
			name:  "codex banner sandbox line",
			probe: &runtimeExecProbe{Error: "exit status 1", ExitCode: 1, Stderr: "OpenAI Codex v0.142.2\nsandbox: read-only\nuser\nprobe"},
			want:  "exec_failed",
		},
		{
			name:  "prompt mentions sandbox",
			probe: &runtimeExecProbe{Error: "exit status 1", ExitCode: 1, Stderr: "user\nverify the Codex sandbox can reach a daemon socket"},
			want:  "exec_failed",
		},
		{
			name:  "timeout",
			probe: &runtimeExecProbe{TimedOut: true},
			want:  "exec_timeout",
		},
		{
			name:  "empty last message",
			probe: &runtimeExecProbe{LastMessagePresent: true, LastMessage: " \n"},
			want:  "last_message_empty",
		},
		{
			name:  "missing last message",
			probe: &runtimeExecProbe{ExitCode: 0},
			want:  "last_message_missing",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeExecProbeIssueID(tc.probe); got != tc.want {
				t.Fatalf("issue id = %q, want %q", got, tc.want)
			}
			if summary := runtimeExecProbeIssueSummary(tc.probe, tc.want); strings.TrimSpace(summary) == "" {
				t.Fatalf("empty summary for %s", tc.want)
			}
			if remediation := runtimeExecProbeRemediation(tc.probe, tc.want); strings.TrimSpace(remediation) == "" {
				t.Fatalf("empty remediation for %s", tc.want)
			}
			if tc.name == "socket check sandbox last message" || tc.name == "HTTP check sandbox last message" {
				remediation := runtimeExecProbeRemediation(tc.probe, tc.want)
				wantFlag := "--exec-socket-check"
				if tc.probe.HTTPCheck {
					wantFlag = "--exec-http-check"
				}
				if !strings.Contains(remediation, wantFlag) {
					t.Fatalf("remediation = %q, want %s command", remediation, wantFlag)
				}
			}
		})
	}
}

func TestRuntimeProbeMissingBinaryFails(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "", exec.ErrNotFound
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("runtime probe missing binary succeeded")
	}
	var result runtimeProbeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out.String())
	}
	if result.OK || !containsRuntimeProbeIssue(result.Issues, "fail", "runtime", "binary_missing") {
		t.Fatalf("result = %+v, want missing binary failure", result)
	}
	if result.CodexDoctor != nil {
		t.Fatalf("codex doctor should not run without binary: %+v", result.CodexDoctor)
	}
}

func containsRuntimeProbeIssue(issues []runtimeProbeIssue, severity, source, id string) bool {
	for _, issue := range issues {
		if issue.Severity == severity && issue.Source == source && issue.ID == id {
			return true
		}
	}
	return false
}
