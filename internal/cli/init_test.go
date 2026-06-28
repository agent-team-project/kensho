package cli

import (
	"bytes"
	"encoding/json"
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
		".agent_team/.template.lock",
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
	lock, err := os.ReadFile(filepath.Join(tmp, ".agent_team", ".template.lock"))
	if err != nil {
		t.Fatal(err)
	}
	lockBody := string(lock)
	for _, want := range []string{
		`ref = "bundled"`,
		`name = "default"`,
		`version = "1.0.0"`,
		`content_hash = "sha256:`,
	} {
		if !strings.Contains(lockBody, want) {
			t.Errorf(".template.lock missing %q: %s", want, lockBody)
		}
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

func TestInitJSONDefaultTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(append(initArgsWithRequired(tmp), "--json"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --json: %v\nstderr: %s", err, errOut.String())
	}
	var result initResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode init json: %v\nbody=%s", err, out.String())
	}
	resolvedTarget, err := resolveAbsTarget(tmp)
	if err != nil {
		t.Fatal(err)
	}
	teamDir := filepath.ToSlash(filepath.Join(resolvedTarget, ".agent_team"))
	if result.Target != filepath.ToSlash(resolvedTarget) || result.TeamDir != teamDir || result.Kind != "default" || result.Ref != "bundled" || result.TemplateName != "default" || result.TemplateVersion != "1.0.0" || !strings.HasPrefix(result.ContentHash, "sha256:") || result.Empty || result.Force {
		t.Fatalf("unexpected init json result: %+v", result)
	}
	if result.DryRun || result.Action != "initialized" {
		t.Fatalf("unexpected init action fields: %+v", result)
	}
	if result.ConfigPath != filepath.ToSlash(filepath.Join(resolvedTarget, ".agent_team", "config.toml")) || result.LockPath != filepath.ToSlash(filepath.Join(resolvedTarget, ".agent_team", ".template.lock")) {
		t.Fatalf("unexpected init paths: %+v", result)
	}
	if strings.Contains(out.String(), "Vendoring team into") || strings.Contains(out.String(), "Done. Next steps") {
		t.Fatalf("init --json should not include progress text: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", ".template.lock")); err != nil {
		t.Fatalf("template lock missing after init --json: %v", err)
	}
}

func TestInitDryRunJSONDoesNotWrite(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(append(initArgsWithRequired(tmp), "--dry-run", "--json"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --dry-run --json: %v\nstderr: %s", err, errOut.String())
	}
	var result initResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode init dry-run json: %v\nbody=%s", err, out.String())
	}
	if !result.DryRun || result.Action != "would-init" || result.Ref != "bundled" || result.Kind != "default" {
		t.Fatalf("unexpected dry-run init result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create .agent_team, stat err=%v", err)
	}
}

func TestInitDryRunCommands(t *testing.T) {
	tmp := t.TempDir()
	resolvedTarget, err := resolveAbsTarget(tmp)
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"init",
		"--target", tmp,
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
		"--dry-run",
		"--commands",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --dry-run --commands: %v\nstderr: %s", err, errOut.String())
	}
	want := strings.Join(shellQuoteArgs([]string{
		"agent-team", "init",
		"--target", filepath.ToSlash(resolvedTarget),
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
	}), " ")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("init dry-run commands = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team")); !os.IsNotExist(err) {
		t.Fatalf("commands dry-run should not create .agent_team, stat err=%v", err)
	}
}

func TestInitFormatEmptyTemplate(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"init", "--target", tmp, "--template", "empty", "--format", "{{.Kind}} {{.Empty}} {{.TeamDir}} {{.LockPath}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init empty --format: %v\nstderr: %s", err, errOut.String())
	}
	resolvedTarget, err := resolveAbsTarget(tmp)
	if err != nil {
		t.Fatal(err)
	}
	want := "empty true " + filepath.ToSlash(filepath.Join(resolvedTarget, ".agent_team"))
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("init empty format = %q, want %q", got, want)
	}
	if strings.Contains(out.String(), "Vendoring team into") || strings.Contains(out.String(), "Done. Next steps") {
		t.Fatalf("init --format should not include progress text: %s", out.String())
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
	if _, err := os.Stat(filepath.Join(teamDir, ".template.lock")); !os.IsNotExist(err) {
		t.Errorf("empty template should not write .template.lock, got err=%v", err)
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

func TestInitOutputFlagValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format json",
			args: []string{"init", "--json", "--format", "{{.TeamDir}}"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "invalid format",
			args: []string{"init", "--format", "{{"},
			want: "invalid --format template",
		},
		{
			name: "commands without dry-run",
			args: []string{"init", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "commands json",
			args: []string{"init", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "commands format",
			args: []string{"init", "--dry-run", "--commands", "--format", "{{.TeamDir}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "machine output no prompt",
			args: []string{"init", "--json", "--target", t.TempDir()},
			want: "machine-readable output requested but required parameters are missing",
		},
		{
			name: "dry-run no prompt",
			args: []string{"init", "--dry-run", "--target", t.TempDir()},
			want: "--dry-run requested but required parameters are missing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(errOut)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected init validation failure, stdout=%s", out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || int(code) != 2 {
				t.Fatalf("err = %v, want exit 2", err)
			}
			if !strings.Contains(errOut.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tt.want)
			}
		})
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

func TestInit_PreservesTemplateLockWithoutForce(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequired(tmp))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init1: %v", err)
	}

	lockPath := filepath.Join(tmp, ".agent_team", ".template.lock")
	if err := os.WriteFile(lockPath, []byte("consumer lock edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd2 := NewRootCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs(initArgsWithRequired(tmp))
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("init2: %v", err)
	}
	got, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "consumer lock edit" {
		t.Errorf("lock was overwritten without --force: %s", got)
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

func TestInit_ForceOverwritesTemplateLock(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(initArgsWithRequired(tmp))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init1: %v", err)
	}

	lockPath := filepath.Join(tmp, ".agent_team", ".template.lock")
	if err := os.WriteFile(lockPath, []byte("stale lock"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd2 := NewRootCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs(append(initArgsWithRequired(tmp), "--force"))
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("init2: %v", err)
	}
	got, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "stale lock" {
		t.Fatal("--force did not overwrite .template.lock")
	}
	if !strings.Contains(string(got), `ref = "bundled"`) {
		t.Errorf("rewritten lock missing bundled ref: %s", got)
	}
}
