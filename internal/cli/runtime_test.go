package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
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
	cmd.SetArgs([]string{"runtime"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("runtime failed: %v\nstderr: %s", err, errOut.String())
	}
	for _, want := range []string{
		"runtime:          claude",
		"binary:           claude",
		"path:             /usr/local/bin/claude",
		"daemon_dispatch:  yes",
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
	cmd.SetArgs([]string{"runtime", "--json"})
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
	if !info.DirectRun || info.DaemonDispatch || info.Resume || info.Subagents {
		t.Fatalf("codex capabilities = %+v, want direct-only", info)
	}
	if len(info.Notes) == 0 {
		t.Fatalf("codex info missing limitation notes: %+v", info)
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
	cmd.SetArgs([]string{"runtime"})
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
	cmd.SetArgs([]string{"runtime"})
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
