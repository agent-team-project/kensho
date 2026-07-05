package runtimebin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBinaryDefaultAndEnvOverride(t *testing.T) {
	t.Setenv(EnvBinary, "")
	got, err := Binary()
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultBinary {
		t.Fatalf("default Binary() = %q, want %q", got, DefaultBinary)
	}
	t.Setenv(EnvBinary, "  codex  ")
	got, err = Binary()
	if err != nil {
		t.Fatal(err)
	}
	if got != "codex" {
		t.Fatalf("env Binary() = %q, want codex", got)
	}
}

func TestCurrentCodexRuntimeDefaultsBinary(t *testing.T) {
	t.Setenv(EnvRuntime, "codex")
	t.Setenv(EnvBinary, "")

	rt, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if rt.Kind != KindCodex || rt.Binary != "codex" {
		t.Fatalf("Current() = %+v, want codex runtime and binary", rt)
	}
}

func TestCurrentFromConfigUsesRepoRuntime(t *testing.T) {
	t.Setenv(EnvRuntime, "")
	t.Setenv(EnvBinary, "")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[runtime]\nkind = \"codex\"\nbinary = \"codex-wrapper\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := CurrentFromConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Kind != KindCodex || rt.Binary != "codex-wrapper" {
		t.Fatalf("CurrentFromConfig() = %+v, want codex/codex-wrapper", rt)
	}
}

func TestCurrentFromConfigEnvOverridesRepoRuntime(t *testing.T) {
	t.Setenv(EnvRuntime, "claude")
	t.Setenv(EnvBinary, "claude-wrapper")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[runtime]\nkind = \"codex\"\nbinary = \"codex-wrapper\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := CurrentFromConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Kind != KindClaude || rt.Binary != "claude-wrapper" {
		t.Fatalf("CurrentFromConfig() = %+v, want env override claude/claude-wrapper", rt)
	}
}

func TestCurrentRejectsUnknownRuntime(t *testing.T) {
	t.Setenv(EnvRuntime, "llama")

	if _, err := Current(); err == nil {
		t.Fatal("Current() error = nil, want invalid runtime error")
	}
}

func TestClaudeCompatibleBinaryRejectsCodex(t *testing.T) {
	t.Setenv(EnvRuntime, "codex")

	if _, err := ClaudeCompatibleBinary(); err == nil {
		t.Fatal("ClaudeCompatibleBinary() error = nil, want unsupported runtime error")
	}
}

func TestCodexAgentTeamEnvConfigArgsSetsAgentTeamVarsAndPATH(t *testing.T) {
	args := CodexAgentTeamEnvConfigArgs([]string{
		"AGENT_TEAM_ROOT=/tmp/team",
		"AGENT_TEAM_INSTANCE=worker-1",
		"PATH=/tmp/runtime/bin:/bin",
		"BAD-KEY=value",
		"AGENT_TEAM_STATE_DIR=/tmp/team/state/worker 1",
	})

	want := []string{
		"-c", `shell_environment_policy.set.AGENT_TEAM_ROOT="/tmp/team"`,
		"-c", `shell_environment_policy.set.AGENT_TEAM_INSTANCE="worker-1"`,
		"-c", `shell_environment_policy.set.PATH="/tmp/runtime/bin:/bin"`,
		"-c", `shell_environment_policy.set.AGENT_TEAM_STATE_DIR="/tmp/team/state/worker 1"`,
	}
	if len(args) != len(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q; all=%#v", i, args[i], want[i], args)
		}
	}
}
