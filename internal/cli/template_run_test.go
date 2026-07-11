package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/runtimebin"
)

// runsRootEnv repoints runsRootDir() at a tempdir so auto-created run dirs
// land somewhere t.TempDir-backed (cleaned up by the test framework). Returns
// the tempdir path; restoring HOME is handled by t.Setenv.
//
// We override XDG_CACHE_HOME (which runsRootDir prefers) rather than HOME so
// the override works on every platform — runsRootDir's HOME branch only fires
// when XDG_CACHE_HOME is unset.
func runsRootEnv(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	return filepath.Join(root, "agent-team", "runs")
}

// TestTemplateRun_TargetUsed verifies that an explicit --target dir is the
// one `init` + `run` operate against, and that it survives the command's
// completion.
func TestTemplateRun_TargetUsed(t *testing.T) {
	target := t.TempDir()
	promptFile := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(promptFile, []byte("hello from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--prompt-file", promptFile,
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}

	teamDir := filepath.Join(target, ".agent_team")
	if st, err := os.Stat(teamDir); err != nil || !st.IsDir() {
		t.Fatalf(".agent_team/ should exist under --target: %v", err)
	}
	stateDir := filepath.Join(teamDir, "state", "manager")
	if st, err := os.Stat(stateDir); err != nil || !st.IsDir() {
		t.Errorf("expected state dir at %s, got %v", stateDir, err)
	}
	// The bundled topology selects Codex for non-Fable seats.
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Errorf("expected codex exec in captured args: %v", cap.args)
	}
	if got, ok := argValue(cap.args, "--model"); !ok || got != "gpt-5.6-sol" {
		t.Errorf("template run model = %q, %v; want gpt-5.6-sol in %v", got, ok, cap.args)
	}
	if !containsArgSubstring(cap.args, `model_reasoning_effort="xhigh"`) {
		t.Errorf("template run args missing xhigh effort: %v", cap.args)
	}
	if !strings.Contains(cap.stdin, "hello from file") {
		t.Errorf("kickoff prompt not forwarded in Codex stdin: %q", cap.stdin)
	}
}

func TestTemplateRun_DefaultAlias(t *testing.T) {
	target := t.TempDir()
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "default", "manager",
		"--target", target,
		"--prompt", "hello default",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run default: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".agent_team", ".template.lock")); err != nil {
		t.Fatalf("template lock missing: %v", err)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Errorf("expected codex exec in captured args: %v", cap.args)
	}
}

// TestTemplateRun_TempdirRemovedOnExit verifies that without --target and
// without --keep, the auto-created tempdir is removed when the command
// returns successfully.
func TestTemplateRun_TempdirRemovedOnExit(t *testing.T) {
	runsRoot := runsRootEnv(t)
	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "worker",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}
	// runsRoot itself may exist, but should be empty (no leftover run dir).
	entries, err := os.ReadDir(runsRoot)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read runs root: %v", err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected runs root empty after exit, found: %v", names)
	}
}

func TestTemplateRun_CodexAutoTempdirAddsSkipGitRepoCheck(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	runsRootEnv(t)
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--prompt", "codex template run",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}
	if !containsString(cap.args, "--skip-git-repo-check") {
		t.Fatalf("codex template run args missing --skip-git-repo-check: %v", cap.args)
	}
}

func TestTemplateRun_RuntimeFlagSelectsCodex(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad-env-runtime")
	t.Setenv(runtimebin.EnvBinary, "claude-env-wrapper")
	runsRootEnv(t)
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--prompt", "codex template run",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run with runtime flag: %v", err)
	}
	if cap.bin != "codex-dev" {
		t.Fatalf("runtime binary = %q, want explicit codex-dev", cap.bin)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Fatalf("codex args = %v, want exec subcommand", cap.args)
	}
	if !containsString(cap.args, "--skip-git-repo-check") {
		t.Fatalf("codex template run args missing --skip-git-repo-check: %v", cap.args)
	}
}

func TestTemplateRun_CodexLastMessagePrintsCleanSidecar(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindClaude))
	t.Setenv(runtimebin.EnvBinary, "claude-env-wrapper")
	target := t.TempDir()

	cap, restore := captureRuntime(t, nil)
	defer restore()
	cap.stdout = "raw codex stdout\n"
	cap.stderr = "raw codex stderr\n"
	cap.lastMessage = "clean template codex answer\n"

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--prompt", "codex template run",
		"--last-message",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run last-message: %v\nstderr: %s", err, stderr.String())
	}
	if got := stdout.String(); !strings.HasSuffix(got, "clean template codex answer\n") || strings.Contains(got, cap.stdout) {
		t.Fatalf("stdout = %q, want init output followed by clean sidecar only", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want raw stderr suppressed on success", got)
	}
	if cap.bin != "codex-dev" {
		t.Fatalf("runtime binary = %q, want explicit codex-dev", cap.bin)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Fatalf("codex args = %v, want exec", cap.args)
	}
}

func TestTemplateRun_CodexAutoTempdirDoesNotDuplicateSkipGitRepoCheck(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	runsRootEnv(t)
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--prompt", "codex template run",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
		"--",
		"--skip-git-repo-check",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}
	count := 0
	for _, arg := range cap.args {
		if arg == "--skip-git-repo-check" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("codex template run args include %d skip flags, want one: %v", count, cap.args)
	}
}

func TestTemplateRun_CodexExplicitTargetDoesNotAddSkipGitRepoCheck(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	target := t.TempDir()
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--prompt", "codex template run",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}
	if containsString(cap.args, "--skip-git-repo-check") {
		t.Fatalf("explicit target should not receive implicit skip-git flag: %v", cap.args)
	}
}

// TestTemplateRun_KeepPreservesTempdir verifies --keep leaves the
// auto-created run dir on disk.
func TestTemplateRun_KeepPreservesTempdir(t *testing.T) {
	runsRoot := runsRootEnv(t)
	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "worker",
		"--keep",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		t.Fatalf("read runs root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one preserved run dir, got %d", len(entries))
	}
	preserved := filepath.Join(runsRoot, entries[0].Name())
	if st, err := os.Stat(filepath.Join(preserved, ".agent_team")); err != nil || !st.IsDir() {
		t.Errorf(".agent_team/ should exist under preserved tempdir: %v", err)
	}
}

// TestTemplateRun_SetFlowsToConfig verifies that --set values land in the
// rendered repo config (init layer) AND in the resolved instance state config
// (run layer). This is the acceptance criterion: --set linear.team_id=...
// flows through correctly.
func TestTemplateRun_SetFlowsToConfig(t *testing.T) {
	target := t.TempDir()
	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--set", "linear.team_id=injected-team",
		"--set", "linear.ticket_prefix=INJ",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}
	repoCfg, err := os.ReadFile(filepath.Join(target, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatalf("read repo config: %v", err)
	}
	if !strings.Contains(string(repoCfg), `team_id = "injected-team"`) {
		t.Errorf("repo config missing --set value:\n%s", repoCfg)
	}
	stateCfg, err := os.ReadFile(filepath.Join(target, ".agent_team", "state", "manager", "config.toml"))
	if err != nil {
		t.Fatalf("read state config: %v", err)
	}
	if !strings.Contains(string(stateCfg), `team_id = "injected-team"`) {
		t.Errorf("state config missing --set value:\n%s", stateCfg)
	}
}

func TestTemplateRun_NoInputTicketlessSucceeds(t *testing.T) {
	target := t.TempDir()
	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--no-input",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run ticketless: %v\nstdout=%s\nstderr=%s", err, out.String(), errOut.String())
	}
	repoCfg, err := os.ReadFile(filepath.Join(target, ".agent_team", "config.toml"))
	if err != nil {
		t.Fatalf("read repo config: %v", err)
	}
	if !strings.Contains(string(repoCfg), `provider = "none"`) {
		t.Fatalf("ticketless template run config = %s", repoCfg)
	}
}

// TestTemplateRun_LinearNoInputFailsListingMissing verifies --no-input +
// explicit Linear mode with missing params fails clearly with exit 2 and a list
// of missing keys.
func TestTemplateRun_LinearNoInputFailsListingMissing(t *testing.T) {
	target := t.TempDir()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--set", "pm.provider=linear",
		"--no-input",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --no-input + missing required params")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "required parameters are missing") {
		t.Errorf("expected missing-params message, got: %s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "linear.team_id") {
		t.Errorf("expected linear.team_id listed, got: %s", errOut.String())
	}
}

// TestTemplateRun_TargetWithExistingTeamDirRequiresForce verifies the safety
// check: a pre-existing .agent_team/ in --target is rejected unless --force
// is passed.
func TestTemplateRun_TargetWithExistingTeamDirRequiresForce(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ".agent_team"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--no-input",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --target has existing .agent_team/ without --force")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "already exists") {
		t.Errorf("expected already-exists message, got: %s", errOut.String())
	}
}

// TestTemplateRun_ForceOverridesExistingTarget verifies --force lets us
// reuse a target that already has a (possibly stale) .agent_team/.
func TestTemplateRun_ForceOverridesExistingTarget(t *testing.T) {
	target := t.TempDir()
	// Pre-create an .agent_team/ with junk so we can prove --force overwrites.
	junk := filepath.Join(target, ".agent_team", "agents", "junk", "agent.md")
	if err := os.MkdirAll(filepath.Dir(junk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(junk, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--force",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run --force: %v", err)
	}
	// Bundled template's `agents/manager/` should now exist; junk should be
	// gone (init.go RemoveAll's the entry on --force).
	mgr := filepath.Join(target, ".agent_team", "agents", "manager", "agent.md")
	if _, err := os.Stat(mgr); err != nil {
		t.Errorf("expected manager agent.md after --force: %v", err)
	}
	if _, err := os.Stat(junk); !os.IsNotExist(err) {
		t.Errorf("--force should have removed stale junk file, got err=%v", err)
	}
}

// TestTemplateRun_LocalRefSpawn covers the full path with a tiny local
// template that has an `agents/` subtree we can spawn against.
func TestTemplateRun_LocalRefSpawn(t *testing.T) {
	tmplDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmplDir, "template.toml"), []byte(`[template]
name = "tiny"
version = "0.0.1"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(tmplDir, "agents", "tinybot")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agent.md"), []byte(`---
description: A tiny test agent.
---

You are tinybot.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", tmplDir, "tinybot",
		"--target", target,
		"--prompt", "say hi",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run local: %v", err)
	}
	if !strings.Contains(cap.agentsJSON, `"tinybot"`) {
		t.Errorf("agents JSON missing tinybot: %s", cap.agentsJSON)
	}
	if !strings.Contains(cap.promptBody, "You are tinybot.") {
		t.Errorf("kickoff missing tinybot body: %s", cap.promptBody)
	}
}

// TestTemplateRun_ForwardsClaudeArgs verifies the `--` passthrough surface.
func TestTemplateRun_ForwardsClaudeArgs(t *testing.T) {
	target := t.TempDir()
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
		"--",
		"--dangerously-skip-permissions",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}
	found := false
	for _, a := range cap.args {
		if a == "--dangerously-skip-permissions" {
			found = true
		}
	}
	if !found {
		t.Errorf("forwarded arg not present in claude args: %v", cap.args)
	}
}

// TestTemplateRun_BypassesDaemon checks that `template run` always exec's
// the selected runtime directly and never attempts to dispatch via the daemon. We verify
// this by observing that captureRun's hook fires (it only runs when
// runAgent reaches execClaude, not the daemon dispatch path).
func TestTemplateRun_BypassesDaemon(t *testing.T) {
	target := t.TempDir()
	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"template", "run", "bundled", "manager",
		"--target", target,
		"--prompt", "hello",
		"--set", "linear.team_id=tt-team",
		"--set", "linear.ticket_prefix=TT",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("template run: %v", err)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Errorf("direct runtime hook was not invoked — daemon bypass appears broken: %v", cap.args)
	}
}

// TestRunsRootDir_HonorsXDG checks the XDG_CACHE_HOME branch.
func TestRunsRootDir_HonorsXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/some/cache")
	got := runsRootDir()
	want := filepath.Join("/some/cache", "agent-team", "runs")
	if got != want {
		t.Errorf("runsRootDir() = %q, want %q", got, want)
	}
}

// TestRunsRootDir_FallsBackToHome checks the HOME-based fallback when
// XDG_CACHE_HOME is unset.
func TestRunsRootDir_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/test/home")
	got := runsRootDir()
	want := filepath.Join("/test/home", ".agent-team", "runs")
	if got != want {
		t.Errorf("runsRootDir() = %q, want %q", got, want)
	}
}
