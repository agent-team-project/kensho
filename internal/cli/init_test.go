package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesaud/agent-team/internal/loader"
)

func TestInit_DefaultTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, errOut.String())
	}

	expected := []string{
		".agent_team/config.toml",
		".agent_team/config.toml.example",
		".agent_team/agents/ticket-manager/agent.md",
		".agent_team/agents/ticket-manager/config.toml",
		".agent_team/agents/manager/agent.md",
		".agent_team/agents/manager/config.toml",
		".agent_team/agents/manager/skills/assign-worker/SKILL.md",
		".agent_team/agents/worker/agent.md",
		".agent_team/agents/worker/config.toml",
		".agent_team/skills/linear/SKILL.md",
		".agent_team/skills/linear/scripts/linear-graphql.sh",
		".agent_team/skills/pull-request/SKILL.md",
	}
	for _, rel := range expected {
		p := filepath.Join(tmp, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing after init: %s", rel)
		}
	}

	stdout := out.String()
	for _, want := range []string{
		"Vendoring team into",
		"Done. Next steps:",
		"agent-team run",
		"agent-team doctor",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull:\n%s", want, stdout)
		}
	}
}

func TestInit_LoaderReadsBundledTemplate(t *testing.T) {
	// After init, the loader must accept the bundled tree without error.
	// This is a stronger parity check than just file existence.
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"init", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	teamDir := filepath.Join(tmp, ".agent_team")
	agents, err := loader.LoadAllAgents(teamDir)
	if err != nil {
		t.Fatalf("LoadAllAgents on bundled template: %v", err)
	}
	if len(agents) != 3 {
		t.Errorf("expected 3 bundled agents, got %d", len(agents))
	}
	if _, err := loader.UnionSkills(agents); err != nil {
		t.Errorf("UnionSkills: %v", err)
	}
}

func TestInit_EmptyTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"init", "--target", tmp, "--template", "empty"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --template empty: %v", err)
	}

	teamDir := filepath.Join(tmp, ".agent_team")
	for _, sub := range []string{"agents", "skills"} {
		st, err := os.Stat(filepath.Join(teamDir, sub))
		if err != nil || !st.IsDir() {
			t.Errorf("expected %s to be a dir", sub)
		}
	}
	cfg, err := os.ReadFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(cfg), "empty-template stub") {
		t.Errorf("expected EMPTY_CONFIG marker, got: %s", cfg)
	}
}

func TestInit_BadTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp, "--template", "bogus"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for bad template")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "must be `default` or `empty`") {
		t.Errorf("missing error text, got: %s", errOut.String())
	}
}

func TestInit_BadTarget(t *testing.T) {
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", "/this/does/not/exist/anywhere"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing target")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "is not a directory") {
		t.Errorf("missing error text, got: %s", errOut.String())
	}
}

func TestInit_SkipsExistingWithoutForce(t *testing.T) {
	tmp := t.TempDir()
	for _, args := range [][]string{
		{"init", "--target", tmp},
		{"init", "--target", tmp},
	} {
		cmd := NewRootCmd()
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("init: %v", err)
		}
		// On the second run we expect "skip" lines.
		if args[0] == "init" && strings.Contains(out.String(), "skip .agent_team/agents") {
			return // ok on the second pass
		}
	}
	t.Fatal("expected skip output on second init")
}

func TestInit_ForceOverwritesDirs(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"init", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init1: %v", err)
	}

	// Edit a vendored file. Re-init with --force should restore it.
	target := filepath.Join(tmp, ".agent_team", "agents", "worker", "agent.md")
	if err := os.WriteFile(target, []byte("MUTATED"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd2 := NewRootCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs([]string{"init", "--target", tmp, "--force"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("init2: %v", err)
	}

	got, _ := os.ReadFile(target)
	if string(got) == "MUTATED" {
		t.Errorf("--force did not overwrite agent.md")
	}
}
