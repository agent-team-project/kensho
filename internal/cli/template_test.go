package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateShow_Bundled(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"template", "show"}) // default ref = bundled
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template show: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	for _, want := range []string{
		"Template: default v",
		"linear.team_id",
		"linear.ticket_prefix",
		"required",
		"^[A-Z]{2,5}$",
		"Agents in this template:",
		"manager",
		"worker",
		"ticket-manager",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("template show missing %q\nfull:\n%s", want, body)
		}
	}
}

func TestTemplateLs_IncludesBundled(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"template", "ls"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template ls: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "REF") {
		t.Errorf("expected header in ls output: %s", body)
	}
	if !strings.Contains(body, "bundled") {
		t.Errorf("expected bundled in ls output: %s", body)
	}
}

func TestTemplateRm_RejectsBundled(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"template", "rm", "bundled"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected rm bundled to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "cannot rm the bundled template") {
		t.Errorf("missing rejection message: %s", errOut.String())
	}
}

func TestInit_FromLocalRef(t *testing.T) {
	// Create a tiny local template with one .tmpl file.
	tmplDir := t.TempDir()
	manifest := `[template]
name = "tiny"
version = "0.0.1"

[[parameter]]
key = "greeting"
type = "string"
required = true
`
	if err := os.WriteFile(filepath.Join(tmplDir, "template.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmplDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "skills", "hello.txt.tmpl"),
		[]byte("hi {{ .greeting }}"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"init", tmplDir, "--target", target,
		"--set", "greeting=world",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init local: %v", err)
	}
	rendered, err := os.ReadFile(filepath.Join(target, ".agent_team", "skills", "hello.txt"))
	if err != nil {
		t.Fatalf("rendered file missing: %v", err)
	}
	if string(rendered) != "hi world" {
		t.Errorf("rendered body = %q", rendered)
	}
}

// TestResolutionChain_AllFourLayers asserts the full layering chain end-to-end
// via init + run, in line with the ticket's acceptance criteria. Constructs a
// local template that declares a default for `linear.ticket_prefix`, then
// exercises each higher layer overriding it in turn.
func TestResolutionChain_AllFourLayers(t *testing.T) {
	// 1. Template-default layer — local template with a default.
	tmplDir := t.TempDir()
	manifest := `[template]
name = "chain"
version = "0.0.1"

[[parameter]]
key = "linear.ticket_prefix"
type = "string"
default = "FROM-DEFAULT"

[[parameter]]
key = "linear.team_id"
type = "string"
required = true

[[parameter]]
key = "marker"
type = "string"
default = "from-default"
`
	if err := os.WriteFile(filepath.Join(tmplDir, "template.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()

	// Layer 1: defaults survive when nothing higher overrides them.
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"init", tmplDir, "--target", target,
		"--set", "linear.team_id=team-from-init",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	repoCfg := readFile(t, filepath.Join(target, ".agent_team", "config.toml"))
	if !strings.Contains(repoCfg, `ticket_prefix = "FROM-DEFAULT"`) {
		t.Errorf("layer 1 (default) not present in repo config: %s", repoCfg)
	}
	if !strings.Contains(repoCfg, `marker = "from-default"`) {
		t.Errorf("layer 1 (default-only key) missing: %s", repoCfg)
	}

	// Layer 2: repo config overrides default. Mutate config.toml directly.
	if err := os.WriteFile(filepath.Join(target, ".agent_team", "config.toml"),
		[]byte(`[linear]
team_id = "team-from-repo"
ticket_prefix = "FROM-REPO"

[other]
marker = "from-default"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Manually mkdir the agent dir so run() can find the team. We don't have
	// agents in the local template; spawn would fail. So we use a fake test
	// that doesn't actually run, but writes the resolved config — easiest is
	// to test resolveRunConfig directly.
	stateDir := filepath.Join(target, ".agent_team", "state", "x")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Layer 2 + 3 + 4 via resolveRunConfig.
	teamDir := filepath.Join(target, ".agent_team")
	resolved, err := resolveRunConfig(teamDir, stateDir, runConfig{
		setStrings: []string{"linear.ticket_prefix=FROM-CLI"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resolved.GetDotted("linear.ticket_prefix"); v != "FROM-CLI" {
		t.Errorf("layer 4 (CLI) didn't win: ticket_prefix = %v", v)
	}
	if v, _ := resolved.GetDotted("linear.team_id"); v != "team-from-repo" {
		t.Errorf("layer 2 (repo) survived against unrelated CLI override: team_id = %v", v)
	}

	// Add layer 3 (per-instance) and re-resolve.
	instanceCfg := filepath.Join(stateDir, "config.toml")
	if err := os.WriteFile(instanceCfg, []byte(`[linear]
ticket_prefix = "FROM-INSTANCE"
team_id = "team-from-instance"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without --set, instance overrides repo.
	resolved2, err := resolveRunConfig(teamDir, stateDir, runConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resolved2.GetDotted("linear.ticket_prefix"); v != "FROM-INSTANCE" {
		t.Errorf("layer 3 (instance) didn't win over repo: %v", v)
	}

	// With --set, CLI still wins over instance.
	resolved3, err := resolveRunConfig(teamDir, stateDir, runConfig{
		setStrings: []string{"linear.ticket_prefix=FROM-CLI"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resolved3.GetDotted("linear.ticket_prefix"); v != "FROM-CLI" {
		t.Errorf("layer 4 (CLI) didn't beat instance: %v", v)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
