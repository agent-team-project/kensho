package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesaud/agent-team/internal/runtimebin"
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
		"Content hash: sha256:",
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

func TestTemplateShow_DefaultAlias(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"template", "show", "default"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template show default: %v\nstderr: %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "Ref: bundled") || !strings.Contains(out.String(), "Template: default v") {
		t.Fatalf("default alias output = %s", out.String())
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

func TestTemplateRm_RejectsDefaultAlias(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"template", "rm", "default"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected rm default to fail")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "cannot rm the bundled template") {
		t.Errorf("missing rejection message: %s", errOut.String())
	}
}

func TestTemplatePull_DefaultAliasNoop(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"template", "pull", "default"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template pull default: %v\nstderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "bundled template needs no pull") {
		t.Fatalf("pull default output = %s", out.String())
	}
}

func TestTemplateSmokeBundledJSONCleansTempRepo(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"template", "smoke",
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template smoke: %v\nstdout=%s\nstderr=%s", err, out.String(), errOut.String())
	}
	var result templateSmokeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode smoke result: %v\nbody=%s", err, out.String())
	}
	if !result.OK || result.Ref != "bundled" || result.Kept || len(result.Steps) != 4 {
		t.Fatalf("smoke result = %+v", result)
	}
	if result.Doctor == nil || !result.Doctor.OK || result.PipelineDoctor == nil || !result.PipelineDoctor.OK || result.TeamDoctor == nil || !result.TeamDoctor.OK {
		t.Fatalf("smoke validation summaries = doctor:%+v pipeline:%+v team:%+v", result.Doctor, result.PipelineDoctor, result.TeamDoctor)
	}
	if _, err := os.Stat(filepath.FromSlash(result.Target)); !os.IsNotExist(err) {
		t.Fatalf("target should be removed after smoke, stat err=%v target=%s", err, result.Target)
	}
}

func TestTemplateSmokeKeepPreservesTempRepo(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"template", "smoke",
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
		"--keep",
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template smoke --keep: %v\nstdout=%s\nstderr=%s", err, out.String(), errOut.String())
	}
	var result templateSmokeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode smoke keep result: %v\nbody=%s", err, out.String())
	}
	if !result.OK || !result.Kept {
		t.Fatalf("smoke keep result = %+v", result)
	}
	target := filepath.FromSlash(result.Target)
	defer os.RemoveAll(target)
	if st, err := os.Stat(filepath.Join(target, ".agent_team", "config.toml")); err != nil || st.IsDir() {
		t.Fatalf("kept target missing config.toml: st=%v err=%v target=%s", st, err, target)
	}
}

func TestTemplateSmokeMissingRequiredParameters(t *testing.T) {
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"template", "smoke", "--json"})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("err = %v, want exit 1\nstdout=%s\nstderr=%s", err, out.String(), errOut.String())
	}
	var result templateSmokeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode failed smoke result: %v\nbody=%s", err, out.String())
	}
	if result.OK || len(result.Steps) != 1 || result.Steps[0].OK || !strings.Contains(result.Steps[0].Error, "required parameters are missing") {
		t.Fatalf("failed smoke result = %+v", result)
	}
	if _, err := os.Stat(filepath.FromSlash(result.Target)); !os.IsNotExist(err) {
		t.Fatalf("failed smoke target should be removed, stat err=%v target=%s", err, result.Target)
	}
}

func TestTemplateSmokeStrictRuntimePromotesNestedDoctorWarnings(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	withRuntimeLookPath(t, func(bin string) (string, error) {
		switch bin {
		case "claude":
			return "/usr/local/bin/claude", nil
		case "missing-codex":
			return "", exec.ErrNotFound
		default:
			t.Fatalf("unexpected runtime lookup for %q", bin)
			return "", exec.ErrNotFound
		}
	})
	oldFind := findAgentTeamd
	findAgentTeamd = func() (string, error) {
		return "/usr/local/bin/agent-teamd", nil
	}
	defer func() { findAgentTeamd = oldFind }()

	tmplDir := t.TempDir()
	writeTinyTemplateFiles(t, tmplDir, "runtime-smoke", "0.0.1", map[string]string{
		"config.toml": `[team]
pm_tool = "none"
`,
		"agents/worker/agent.md": `---
description: Worker.
---

Worker prompt.
`,
		"instances.toml": `
[instances.worker]
agent = "worker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
runtime = "codex"
runtime_bin = "missing-codex"

[teams.delivery]
instances = ["worker"]
pipelines = ["ticket_to_pr"]
`,
	})

	nonStrict := NewRootCmd()
	nonStrictOut, nonStrictErr := &bytes.Buffer{}, &bytes.Buffer{}
	nonStrict.SetOut(nonStrictOut)
	nonStrict.SetErr(nonStrictErr)
	nonStrict.SetArgs([]string{"template", "smoke", tmplDir, "--json"})
	if err := nonStrict.Execute(); err != nil {
		t.Fatalf("template smoke warning-only runtime defaults should not fail: %v\nstdout=%s\nstderr=%s", err, nonStrictOut.String(), nonStrictErr.String())
	}
	var nonStrictResult templateSmokeResult
	if err := json.Unmarshal(nonStrictOut.Bytes(), &nonStrictResult); err != nil {
		t.Fatalf("decode non-strict smoke json: %v\nbody=%s", err, nonStrictOut.String())
	}
	if !nonStrictResult.OK || nonStrictResult.PipelineDoctor == nil || !nonStrictResult.PipelineDoctor.OK || nonStrictResult.TeamDoctor == nil || !nonStrictResult.TeamDoctor.OK {
		t.Fatalf("non-strict smoke result = %+v", nonStrictResult)
	}
	if !hasPipelineDoctorFinding(nonStrictResult.PipelineDoctor.Warnings, "step_runtime_unavailable") ||
		!hasTeamDoctorFinding(nonStrictResult.TeamDoctor.Warnings, "step_runtime_unavailable") {
		t.Fatalf("non-strict smoke did not preserve runtime warnings: pipeline=%+v team=%+v", nonStrictResult.PipelineDoctor, nonStrictResult.TeamDoctor)
	}
	if nonStrictErr.Len() != 0 {
		t.Fatalf("template smoke --json should not write warnings to stderr: %s", nonStrictErr.String())
	}

	strict := NewRootCmd()
	strictOut, strictErr := &bytes.Buffer{}, &bytes.Buffer{}
	strict.SetOut(strictOut)
	strict.SetErr(strictErr)
	strict.SetArgs([]string{"template", "smoke", tmplDir, "--strict-runtime", "--json"})
	err := strict.Execute()
	if err == nil {
		t.Fatal("expected strict template smoke to fail on missing step runtime")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("strict smoke err = %v, want exit 1", err)
	}
	var strictResult templateSmokeResult
	if err := json.Unmarshal(strictOut.Bytes(), &strictResult); err != nil {
		t.Fatalf("decode strict smoke json: %v\nbody=%s", err, strictOut.String())
	}
	if strictResult.OK || strictResult.PipelineDoctor == nil || strictResult.PipelineDoctor.OK || strictResult.TeamDoctor == nil || strictResult.TeamDoctor.OK {
		t.Fatalf("strict smoke result = %+v", strictResult)
	}
	if !hasPipelineDoctorFinding(strictResult.PipelineDoctor.Problems, "step_runtime_unavailable") ||
		!hasTeamDoctorFinding(strictResult.TeamDoctor.Problems, "step_runtime_unavailable") {
		t.Fatalf("strict smoke did not promote nested runtime warnings: pipeline=%+v team=%+v", strictResult.PipelineDoctor, strictResult.TeamDoctor)
	}
	if hasPipelineDoctorFinding(strictResult.PipelineDoctor.Warnings, "step_runtime_unavailable") ||
		hasTeamDoctorFinding(strictResult.TeamDoctor.Warnings, "step_runtime_unavailable") {
		t.Fatalf("strict smoke left nested runtime warnings unpromoted: pipeline=%+v team=%+v", strictResult.PipelineDoctor, strictResult.TeamDoctor)
	}
	if strictErr.Len() != 0 {
		t.Fatalf("template smoke --json should not write strict problems to stderr: %s", strictErr.String())
	}
}

func TestParseGitTemplateRef(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		cloneURL string
		revision string
		cacheKey string
	}{
		{
			name:     "github shorthand",
			ref:      "github.com/acme/eng-team@v1.0.0",
			cloneURL: "https://github.com/acme/eng-team",
			revision: "v1.0.0",
			cacheKey: "github.com/acme/eng-team@v1.0.0",
		},
		{
			name:     "https",
			ref:      "https://github.com/acme/eng-team.git@v1.0.0",
			cloneURL: "https://github.com/acme/eng-team.git",
			revision: "v1.0.0",
			cacheKey: "github.com/acme/eng-team@v1.0.0",
		},
		{
			name:     "scp",
			ref:      "git@github.com:acme/eng-team.git@main",
			cloneURL: "git@github.com:acme/eng-team.git",
			revision: "main",
			cacheKey: "github.com/acme/eng-team@main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := parseGitTemplateRef(tt.ref)
			if err != nil {
				t.Fatalf("parseGitTemplateRef: %v", err)
			}
			if !ok {
				t.Fatal("parseGitTemplateRef ok=false")
			}
			if got.CloneURL != tt.cloneURL || got.Revision != tt.revision || got.CacheKey != tt.cacheKey {
				t.Fatalf("parsed = %+v", got)
			}
		})
	}
}

func TestTemplatePullGitRefCachesAndShow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "template.toml"), []byte(`[template]
name = "git-template"
version = "1.0.0"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForTemplateTest(t, repo, "init")
	runGitForTemplateTest(t, repo, "config", "user.email", "test@example.com")
	runGitForTemplateTest(t, repo, "config", "user.name", "Test User")
	runGitForTemplateTest(t, repo, "add", "template.toml")
	runGitForTemplateTest(t, repo, "commit", "-m", "init")
	runGitForTemplateTest(t, repo, "tag", "v1.0.0")

	gitURL := (&url.URL{Scheme: "file", Path: repo}).String() + "@v1.0.0"
	cacheRef := "github.com/acme/git-template@v1.0.0"
	pull := NewRootCmd()
	pullOut, pullErr := &bytes.Buffer{}, &bytes.Buffer{}
	pull.SetOut(pullOut)
	pull.SetErr(pullErr)
	pull.SetArgs([]string{"template", "pull", gitURL, "--as", cacheRef})
	if err := pull.Execute(); err != nil {
		t.Fatalf("template pull git ref: %v\nstdout=%s\nstderr=%s", err, pullOut.String(), pullErr.String())
	}
	cached := filepath.Join(home, ".agent-team", "cache", filepath.FromSlash(cacheRef))
	if _, err := os.Stat(filepath.Join(cached, "template.toml")); err != nil {
		t.Fatalf("template.toml not cached: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cached, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git should not be retained in template cache, err=%v", err)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"template", "show", cacheRef})
	if err := show.Execute(); err != nil {
		t.Fatalf("template show cached git ref: %v\nstdout=%s\nstderr=%s", err, showOut.String(), showErr.String())
	}
	if !strings.Contains(showOut.String(), "Template: git-template v1.0.0") {
		t.Fatalf("show output missing cached template:\n%s", showOut.String())
	}
}

func runGitForTemplateTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
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
	resolved, err := resolveRunConfig(teamDir, stateDir, "x", runConfig{
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
	resolved2, err := resolveRunConfig(teamDir, stateDir, "x", runConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resolved2.GetDotted("linear.ticket_prefix"); v != "FROM-INSTANCE" {
		t.Errorf("layer 3 (instance) didn't win over repo: %v", v)
	}

	// With --set, CLI still wins over instance.
	resolved3, err := resolveRunConfig(teamDir, stateDir, "x", runConfig{
		setStrings: []string{"linear.ticket_prefix=FROM-CLI"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := resolved3.GetDotted("linear.ticket_prefix"); v != "FROM-CLI" {
		t.Errorf("layer 4 (CLI) didn't beat instance: %v", v)
	}
}

// TestRun_DeclaredOverridesFlowThrough verifies the new layer 3 from
// documentation/topology.md: per-instance overrides declared in
// instances.toml are folded between repo config and per-instance state file.
// Two declared instances of the same agent with different config land with
// different resolved values.
func TestRun_DeclaredOverridesFlowThrough(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`
[linear]
team_id       = "team-shared"
ticket_prefix = "PREFIX"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.tm-platform]
agent = "ticket-manager"

[instances.tm-platform.config.linear]
project_id = "project-platform-uuid"

[instances.tm-mobile]
agent = "ticket-manager"

[instances.tm-mobile.config.linear]
project_id = "project-mobile-uuid"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Resolve as the platform instance.
	stateP := filepath.Join(teamDir, "state", "tm-platform")
	if err := os.MkdirAll(stateP, 0o755); err != nil {
		t.Fatal(err)
	}
	plat, err := resolveRunConfig(teamDir, stateP, "tm-platform", runConfig{})
	if err != nil {
		t.Fatalf("resolve platform: %v", err)
	}
	if v, _ := plat.GetDotted("linear.project_id"); v != "project-platform-uuid" {
		t.Errorf("platform project_id: %v want project-platform-uuid", v)
	}
	if v, _ := plat.GetDotted("linear.team_id"); v != "team-shared" {
		t.Errorf("platform team_id should inherit repo: %v", v)
	}

	// Resolve as the mobile instance — different declared overrides.
	stateM := filepath.Join(teamDir, "state", "tm-mobile")
	if err := os.MkdirAll(stateM, 0o755); err != nil {
		t.Fatal(err)
	}
	mob, err := resolveRunConfig(teamDir, stateM, "tm-mobile", runConfig{})
	if err != nil {
		t.Fatalf("resolve mobile: %v", err)
	}
	if v, _ := mob.GetDotted("linear.project_id"); v != "project-mobile-uuid" {
		t.Errorf("mobile project_id: %v want project-mobile-uuid", v)
	}

	// Per-instance state file should still beat declared overrides.
	if err := os.WriteFile(filepath.Join(stateM, "config.toml"), []byte(`
[linear]
project_id = "from-state-file"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mob2, err := resolveRunConfig(teamDir, stateM, "tm-mobile", runConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := mob2.GetDotted("linear.project_id"); v != "from-state-file" {
		t.Errorf("state file should beat declared: got %v", v)
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
