package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// captureRun replaces the execClaude hook with a recorder for the duration of
// a test. The captured args reference paths inside a tmpdir that runAgent
// cleans up via defer, so the recorder snapshots filesystem state synchronously
// while the dir is still alive.
type runCapture struct {
	args         []string
	env          []string
	cwd          string
	rc           error
	skillsDirOK  bool
	promptBody   string
	addDir       string
	promptFile   string
	agentsJSON   string
}

func captureRun(t *testing.T, rc error) (*runCapture, func()) {
	t.Helper()
	cap := &runCapture{rc: rc}
	prev := execClaude
	execClaude = func(cmd *cobra.Command, args []string, env []string, cwd string) error {
		cap.args = args
		cap.env = env
		cap.cwd = cwd
		// Snapshot filesystem state before runAgent's `defer os.RemoveAll(tmpdir)` fires.
		for i := 0; i+1 < len(args); i++ {
			switch args[i] {
			case "--add-dir":
				cap.addDir = args[i+1]
				if st, err := os.Stat(filepath.Join(cap.addDir, ".claude", "skills")); err == nil && st.IsDir() {
					cap.skillsDirOK = true
				}
			case "--append-system-prompt-file":
				cap.promptFile = args[i+1]
				if b, err := os.ReadFile(cap.promptFile); err == nil {
					cap.promptBody = string(b)
				}
			case "--agents":
				cap.agentsJSON = args[i+1]
			}
		}
		return cap.rc
	}
	return cap, func() { execClaude = prev }
}

// initInto runs `init` against a tmp dir to produce a real .agent_team/ tree.
// Required template parameters are passed via --set so the call doesn't block
// on a prompt — tests that exercise the prompt path build their own init args.
func initInto(t *testing.T, dir string) {
	t.Helper()
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"init", "--target", dir,
		"--set", "linear.team_id=test-team-uuid",
		"--set", "linear.ticket_prefix=TST",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
}

func TestRun_ExecsClaudeWithExpectedArgs(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "kickoff message"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}

	if cap.agentsJSON == "" {
		t.Fatalf("missing --agents in args: %v", cap.args)
	}
	var agents map[string]map[string]string
	if err := json.Unmarshal([]byte(cap.agentsJSON), &agents); err != nil {
		t.Fatalf("invalid --agents JSON: %v", err)
	}
	wantAgents := []string{"manager", "ticket-manager", "worker"}
	got := make([]string, 0, len(agents))
	for k := range agents {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantAgents, ",") {
		t.Errorf("agents JSON keys = %v, want %v", got, wantAgents)
	}
	for _, a := range wantAgents {
		if agents[a]["description"] == "" {
			t.Errorf("agent %s: empty description", a)
		}
		if agents[a]["prompt"] == "" {
			t.Errorf("agent %s: empty prompt", a)
		}
	}

	if cap.addDir == "" {
		t.Fatalf("missing --add-dir: %v", cap.args)
	}
	if !cap.skillsDirOK {
		t.Errorf("skills dir not created at %s/.claude/skills (snapshotted during exec)", cap.addDir)
	}
	if cap.promptFile == "" {
		t.Fatalf("missing --append-system-prompt-file: %v", cap.args)
	}
	if !strings.Contains(cap.promptBody, "You are the `manager` instance of the `manager` agent.") {
		t.Errorf("kickoff missing instance line, got: %s", cap.promptBody)
	}

	// State dir must be created.
	stateDir := filepath.Join(tmp, ".agent_team", "state", "manager")
	if st, err := os.Stat(stateDir); err != nil || !st.IsDir() {
		t.Errorf("state dir not created: %s", stateDir)
	}

	// Env must include AGENT_TEAM_*.
	hasRoot, hasInstance, hasState := false, false, false
	for _, e := range cap.env {
		switch {
		case strings.HasPrefix(e, "AGENT_TEAM_ROOT="):
			hasRoot = true
		case strings.HasPrefix(e, "AGENT_TEAM_INSTANCE=manager"):
			hasInstance = true
		case strings.HasPrefix(e, "AGENT_TEAM_STATE_DIR="):
			hasState = true
		}
	}
	if !hasRoot || !hasInstance || !hasState {
		t.Errorf("missing AGENT_TEAM_* env vars: root=%v instance=%v state=%v", hasRoot, hasInstance, hasState)
	}

	// -p prompt must be forwarded.
	foundPromptFlag := false
	for i := 0; i < len(cap.args)-1; i++ {
		if cap.args[i] == "-p" && cap.args[i+1] == "kickoff message" {
			foundPromptFlag = true
		}
	}
	if !foundPromptFlag {
		t.Errorf("-p prompt not forwarded: %v", cap.args)
	}
}

func TestRun_NamedInstanceUsesCustomStateDir(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "worker", "--target", tmp, "--name", "worker-squ-99"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	stateDir := filepath.Join(tmp, ".agent_team", "state", "worker-squ-99")
	if st, err := os.Stat(stateDir); err != nil || !st.IsDir() {
		t.Errorf("named instance state dir not created: %s", stateDir)
	}
}

func TestRun_AgentNotFound(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"run", "nonexistent", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(errOut.String(), "agent `nonexistent` not found") {
		t.Errorf("missing not-found text: %s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "Available:") {
		t.Errorf("missing available agents: %s", errOut.String())
	}
}

func TestRun_MissingTeamDir(t *testing.T) {
	tmp := t.TempDir() // no .agent_team/

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"run", "manager", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when .agent_team/ missing")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestRun_WritesResolvedConfigToStateDir(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"run", "manager", "--target", tmp,
		"--set", "linear.team_id=run-override",
		"--set", "linear.runtime_only=hello",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}

	stateCfgPath := filepath.Join(tmp, ".agent_team", "state", "manager", "config.toml")
	body, err := os.ReadFile(stateCfgPath)
	if err != nil {
		t.Fatalf("read state config: %v", err)
	}
	cfg := string(body)
	if !strings.Contains(cfg, `team_id = "run-override"`) {
		t.Errorf("--set override missing in state config: %s", cfg)
	}
	if !strings.Contains(cfg, `runtime_only = "hello"`) {
		t.Errorf("--set new key missing in state config: %s", cfg)
	}
	// Repo config values not overridden should still be present in the merge.
	if !strings.Contains(cfg, `ticket_prefix = "TST"`) {
		t.Errorf("repo config value missing in merged state config: %s", cfg)
	}
}

func TestRun_InstanceConfigLayersBelowSet(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	_, restore := captureRun(t, nil)
	defer restore()

	instCfg := filepath.Join(tmp, "instance-config.toml")
	if err := os.WriteFile(instCfg, []byte(`[linear]
team_id = "from-instance-file"
extra = "from-instance-file"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"run", "manager", "--target", tmp,
		"--instance-config", instCfg,
		"--set", "linear.team_id=from-cli",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}

	stateCfgPath := filepath.Join(tmp, ".agent_team", "state", "manager", "config.toml")
	body, _ := os.ReadFile(stateCfgPath)
	cfg := string(body)
	// CLI flag wins over instance file.
	if !strings.Contains(cfg, `team_id = "from-cli"`) {
		t.Errorf("CLI --set should beat --instance-config: %s", cfg)
	}
	// Instance-file-only key should be present.
	if !strings.Contains(cfg, `extra = "from-instance-file"`) {
		t.Errorf("instance-file extra missing: %s", cfg)
	}
}

func TestRun_ReRendersTmplFiles(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	// Drop a .tmpl file inside the team tree so run sees something to render.
	tmplPath := filepath.Join(tmp, ".agent_team", "skills", "linear", "demo.txt.tmpl")
	if err := os.WriteFile(tmplPath, []byte("team={{ .linear.team_id }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"run", "manager", "--target", tmp,
		"--set", "linear.team_id=fresh-from-set",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	rendered := filepath.Join(tmp, ".agent_team", "state", "manager", "rendered", "skills", "linear", "demo.txt")
	body, err := os.ReadFile(rendered)
	if err != nil {
		t.Fatalf("re-rendered file missing: %v", err)
	}
	if string(body) != "team=fresh-from-set\n" {
		t.Errorf("rendered body = %q", body)
	}
}

func TestRun_NoTmplFilesProducesNoRenderDir(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	_, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	renderRoot := filepath.Join(tmp, ".agent_team", "state", "manager", "rendered")
	if _, err := os.Stat(renderRoot); !os.IsNotExist(err) {
		t.Errorf("expected no rendered/ dir when no .tmpl files exist, got err=%v", err)
	}
}

func TestRun_ForwardedArgsAfterDoubleDash(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--", "--dangerously-skip-permissions"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
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
