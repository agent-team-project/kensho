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

	"github.com/jamesaud/agent-team/internal/runtimebin"
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

func TestRuntimeProbeCodexExecProbeSuccess(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-dev")
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
		if !strings.Contains(stdin, "agent-team runtime probe ok") {
			t.Fatalf("exec stdin = %q, want probe prompt", stdin)
		}
		if len(args) == 0 || args[0] != "exec" || args[len(args)-1] != "-" {
			t.Fatalf("exec args = %#v, want codex exec ... -", args)
		}
		if !containsString(args, "-C") || !containsString(args, "--output-last-message") {
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
		if err := os.WriteFile(lastMessage, []byte("agent-team runtime probe ok\n"), 0o644); err != nil {
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
	cmd.SetArgs([]string{"runtime", "probe", "--target", tmp, "--skip-doctor", "--exec", "--json"})
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
	if got := result.ExecProbe.LastMessage; got != "agent-team runtime probe ok" {
		t.Fatalf("last message = %q", got)
	}
	if !containsString(result.Actions, "codex doctor --summary") {
		t.Fatalf("actions = %+v, want codex doctor hint", result.Actions)
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
	if !containsRuntimeProbeIssue(result.Issues, "fail", "exec_probe", "exec_failed") {
		t.Fatalf("issues = %+v, want exec failure", result.Issues)
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
