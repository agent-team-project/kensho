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

// initArgsWithRequired is the canonical "init the bundled template into tmp,
// non-interactively" arg list. Most tests in this file use this — the prompt
// path has its own dedicated tests.
func initArgsWithRequired(target string) []string {
	return []string{
		"init", "--target", target,
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
	}
}

func TestInit_DefaultTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(initArgsWithRequired(tmp))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, errOut.String())
	}

	expected := []string{
		".agent_team/config.toml",
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

	// The resolved config.toml must contain the supplied --set values.
	cfg, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(cfg)
	if !strings.Contains(body, `team_id = "test-team-uuid"`) {
		t.Errorf("config.toml missing team_id: %s", body)
	}
	if !strings.Contains(body, `ticket_prefix = "TST"`) {
		t.Errorf("config.toml missing ticket_prefix: %s", body)
	}

	// template.toml itself must NOT land in the consumer's tree.
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", "template.toml")); !os.IsNotExist(err) {
		t.Errorf("template.toml leaked into consumer tree (err=%v)", err)
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
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequired(tmp))
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

func TestInit_NoInputFailsListingMissing(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp, "--no-input"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error: required params missing under --no-input")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	combined := errOut.String()
	for _, want := range []string{
		"--no-input given but required parameters are missing:",
		"linear.team_id",
		"linear.ticket_prefix",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("error output missing %q\nfull:\n%s", want, combined)
		}
	}
}

func TestInit_PatternViolationFails(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"init", "--target", tmp,
		"--set", "linear.team_id=abc",
		"--set", "linear.ticket_prefix=lowercase-bad",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected pattern-violation error")
	}
	if !strings.Contains(errOut.String(), "does not match pattern") {
		t.Errorf("missing pattern error: %s", errOut.String())
	}
}

func TestInit_PromptFlow(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	// Two required params; supply each on its own input line.
	cmd.SetIn(strings.NewReader("uuid-from-prompt\nABC\n"))
	cmd.SetArgs([]string{"init", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\nstderr: %s", err, errOut.String())
	}
	cfg, err := os.ReadFile(filepath.Join(tmp, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(cfg)
	if !strings.Contains(body, `team_id = "uuid-from-prompt"`) {
		t.Errorf("missing team_id from prompt: %s", body)
	}
	if !strings.Contains(body, `ticket_prefix = "ABC"`) {
		t.Errorf("missing ticket_prefix from prompt: %s", body)
	}
	// stdout should show the prompts.
	if !strings.Contains(out.String(), "This template requires the following parameters") {
		t.Errorf("missing prompt header: %s", out.String())
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
	for i := 0; i < 2; i++ {
		cmd := NewRootCmd()
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(initArgsWithRequired(tmp))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("init pass %d: %v", i, err)
		}
		if i == 1 && !strings.Contains(out.String(), "skip .agent_team/agents") {
			t.Fatalf("expected skip output on second init, got:\n%s", out.String())
		}
	}
}

func TestInit_ForceOverwritesDirs(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequired(tmp))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init1: %v", err)
	}

	target := filepath.Join(tmp, ".agent_team", "agents", "worker", "agent.md")
	if err := os.WriteFile(target, []byte("MUTATED"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd2 := NewRootCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	args := append(initArgsWithRequired(tmp), "--force")
	cmd2.SetArgs(args)
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("init2: %v", err)
	}

	got, _ := os.ReadFile(target)
	if string(got) == "MUTATED" {
		t.Errorf("--force did not overwrite agent.md")
	}
}
