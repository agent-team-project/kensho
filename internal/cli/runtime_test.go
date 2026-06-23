package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesaud/agent-team/internal/runtimebin"
)

func withRuntimeLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	old := runtimeLookPath
	runtimeLookPath = fn
	t.Cleanup(func() { runtimeLookPath = old })
}

func TestRuntimeCommand_DefaultText(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "claude" {
			t.Fatalf("look path bin = %q, want claude", bin)
		}
		return "/usr/local/bin/claude", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime failed: %v\nstderr: %s", err, errOut.String())
	}
	for _, want := range []string{
		"runtime:          claude",
		"binary:           claude",
		"path:             /usr/local/bin/claude",
		"daemon_dispatch:  yes",
		"direct_resume:    yes",
		"managed_resume:   yes",
		"resume:           yes",
		"subagents:        yes",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("runtime output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRuntimeCommand_CodexJSON(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --json failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex" || info.Path != "/opt/homebrew/bin/codex" {
		t.Fatalf("info = %+v, want codex path", info)
	}
	if !info.DirectRun || !info.DaemonDispatch || !info.DirectResume || info.ManagedResume || info.Resume || info.Subagents {
		t.Fatalf("codex capabilities = %+v, want direct plus daemon one-shot", info)
	}
	if len(info.Notes) == 0 {
		t.Fatalf("codex info missing limitation notes: %+v", info)
	}
}

func TestRuntimeCommand_Format(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "codex")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/opt/homebrew/bin/codex", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--format", "{{.Runtime}} {{.Binary}} {{.Available}} {{.DirectResume}} {{.ManagedResume}} {{.Resume}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --format failed: %v\nstderr: %s", err, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "codex codex true true false false" {
		t.Fatalf("runtime format = %q", got)
	}
}

func TestRuntimeCommand_FormatRejectsJSON(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--json", "--format", "{{.Runtime}}"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime --json --format succeeded")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), "--format cannot be combined with --json") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func appendRuntimeConfigForRuntimeTest(t *testing.T, root, kind, binary string) {
	t.Helper()
	cfg := filepath.Join(root, ".agent_team", "config.toml")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	if _, err := f.WriteString("\n[runtime]\nkind = \"" + kind + "\"\nbinary = \"" + binary + "\"\n"); err != nil {
		_ = f.Close()
		t.Fatalf("write config: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close config: %v", err)
	}
}

func TestRuntimeCommand_RepoConfigCodexJSON(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-wrapper")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-wrapper" {
			t.Fatalf("look path bin = %q, want codex-wrapper", bin)
		}
		return "/usr/local/bin/codex-wrapper", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", tmp, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --json failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex-wrapper" || info.ConfigPath == "" {
		t.Fatalf("info = %+v, want config-backed codex", info)
	}
}

func TestRuntimeCommand_RuntimeFlagOverridesEnvRuntimeAndBinary(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad-env-runtime")
	t.Setenv(runtimebin.EnvBinary, "claude-env-wrapper")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex" {
			t.Fatalf("look path bin = %q, want codex", bin)
		}
		return "/usr/local/bin/codex", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--runtime", "codex", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --runtime codex failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex" {
		t.Fatalf("info = %+v, want codex default binary from runtime flag", info)
	}
}

func TestRuntimeCommand_RuntimeBinFlagOverridesSelectedRuntimeBinary(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-wrapper")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-dev" {
			t.Fatalf("look path bin = %q, want codex-dev", bin)
		}
		return "/usr/local/bin/codex-dev", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", tmp, "--runtime-bin", "codex-dev", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime --runtime-bin failed: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if info.Runtime != "codex" || info.Binary != "codex-dev" {
		t.Fatalf("info = %+v, want config kind with explicit binary", info)
	}
}

func TestRuntimeCommand_RepoFlagOverridesTarget(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRuntimeTest(t, tmp, "codex", "codex-wrapper")
	badTarget := t.TempDir()
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "codex-wrapper" {
			t.Fatalf("look path bin = %q, want codex-wrapper", bin)
		}
		return "/usr/local/bin/codex-wrapper", nil
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"--repo", tmp, "runtime", "--target", badTarget, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime with --repo override: %v\nstderr: %s", err, errOut.String())
	}
	var info runtimeInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	wantRoot := tmp
	if eval, err := filepath.EvalSymlinks(wantRoot); err == nil {
		wantRoot = eval
	}
	wantConfig := filepath.ToSlash(filepath.Join(wantRoot, ".agent_team", "config.toml"))
	if info.Binary != "codex-wrapper" || info.ConfigPath != wantConfig {
		t.Fatalf("info = %+v, want config from --repo %s", info, wantConfig)
	}
}

func TestRuntimeCommand_MissingBinaryExitsOne(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "missing-runtime")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "missing-runtime" {
			t.Fatalf("look path bin = %q, want missing-runtime", bin)
		}
		return "", exec.ErrNotFound
	})

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir()})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime succeeded with missing binary, want exit 1")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("error = %v, want exit 1", err)
	}
	if !strings.Contains(out.String(), "available:        no") {
		t.Fatalf("missing binary output = %q, want available no", out.String())
	}
}

func TestRuntimeCommand_InvalidRuntimeExitsTwo(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad")

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir()})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime succeeded with invalid env, want exit 2")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), runtimebin.EnvRuntime+" must be") {
		t.Fatalf("stderr = %q, want invalid runtime error", errOut.String())
	}
}

func TestRuntimeCommand_InvalidRuntimeFlagExitsTwo(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"runtime", "--target", t.TempDir(), "--runtime", "bad-runtime"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("runtime succeeded with invalid flag, want exit 2")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Fatalf("error = %v, want exit 2", err)
	}
	if !strings.Contains(errOut.String(), `--runtime must be "claude" or "codex"`) {
		t.Fatalf("stderr = %q, want invalid runtime flag error", errOut.String())
	}
}
