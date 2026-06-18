package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

// captureRun replaces the execClaude hook with a recorder for the duration of
// a test. The captured args reference paths inside a tmpdir that runAgent
// cleans up via defer, so the recorder snapshots filesystem state synchronously
// while the dir is still alive.
type runCapture struct {
	args        []string
	env         []string
	cwd         string
	rc          error
	skillsDirOK bool
	promptBody  string
	addDir      string
	promptFile  string
	agentsJSON  string
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

func startRunTestDaemon(t *testing.T, teamDir string, mgr *daemon.InstanceManager) func() {
	t.Helper()
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatalf("mkdir daemon root: %v", err)
	}
	socket := daemon.SocketPath(teamDir)
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix daemon socket: %v", err)
	}
	var resolver *daemon.EventResolver
	if topo, err := topology.LoadFromTeamDir(teamDir); err == nil {
		resolver = daemon.NewEventResolver(mgr, teamDir, topo)
	}
	srv := &http.Server{Handler: daemon.Handler(mgr, nil, resolver, teamDir)}
	go func() {
		_ = srv.Serve(ln)
	}()
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write daemon pidfile: %v", err)
	}
	return func() {
		_ = srv.Close()
		_ = ln.Close()
		_ = os.Remove(socket)
		_ = os.Remove(daemon.PidPath(teamDir))
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsEnvPrefix(env []string, prefix string) bool {
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
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

func TestRun_CodexRuntimeBuildsDirectExecArgs(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "codex task", "--no-daemon", "--", "--sandbox", "workspace-write"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Fatalf("codex args = %v, want exec subcommand", cap.args)
	}
	for _, forbidden := range []string{"--agents", "--append-system-prompt-file"} {
		if containsString(cap.args, forbidden) {
			t.Fatalf("codex args should not include %s: %v", forbidden, cap.args)
		}
	}
	cwdArg := ""
	for i := 0; i < len(cap.args)-1; i++ {
		if cap.args[i] == "-C" {
			cwdArg = cap.args[i+1]
			break
		}
	}
	if cwdArg == "" || cwdArg != cap.cwd {
		t.Fatalf("codex args missing -C target: %v", cap.args)
	}
	if !containsString(cap.args, "--add-dir") || cap.addDir == "" || !cap.skillsDirOK {
		t.Fatalf("codex args missing add-dir with skills: args=%v addDir=%q skills=%v", cap.args, cap.addDir, cap.skillsDirOK)
	}
	if !containsString(cap.args, "--sandbox") || !containsString(cap.args, "workspace-write") {
		t.Fatalf("forwarded codex args missing: %v", cap.args)
	}
	prompt := cap.args[len(cap.args)-1]
	for _, want := range []string{
		"You are the `manager` instance of the `manager` agent.",
		"This session is running through the Codex adapter.",
		"Available team agents:",
		"codex task",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("codex prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRun_CodexRuntimeRequiresPromptForDaemonDispatch(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp := t.TempDir()
	initInto(t, tmp)

	cases := [][]string{
		{"run", "manager", "--target", tmp, "--detach"},
		{"run", "manager", "--target", tmp, "--attach"},
		{"run", "manager", "--target", tmp, "--json"},
		{"run", "manager", "--target", tmp, "--format", "{{.Instance}}"},
	}
	for _, args := range cases {
		cmd := NewRootCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("run %v succeeded, want daemon-only flag rejection", args)
		}
	}
}

func TestRun_CodexRuntimeCanDetachWithPrompt(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp, err := os.MkdirTemp("/tmp", "agent-team-run-codex-detach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	wantWorkspace, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		wantWorkspace = tmp
	}

	var (
		mu       sync.Mutex
		gotArgs  []string
		gotSpace string
	)
	base := fakeSpawnerForTest(t, 2*time.Second)
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), func(args []string, env []string, workspace, stdoutPath, stderrPath string) (*os.Process, error) {
		mu.Lock()
		gotArgs = append([]string(nil), args...)
		gotSpace = workspace
		mu.Unlock()
		return base(args, env, workspace, stdoutPath, stderrPath)
	})
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	defer func() {
		for _, meta := range mgr.List() {
			if meta.Instance == "manager" {
				stopAndWaitForTest(t, mgr, "manager")
				return
			}
		}
	}()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "codex task", "--detach", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("codex run --detach --json: %v\nstderr: %s", err, stderr.String())
	}
	var body runDispatchJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("json body: %v\nstdout: %s", err, out.String())
	}
	if body.Instance != "manager" || body.Agent != "manager" || body.Runtime != "codex" || body.PID == 0 || body.SessionID != "" || body.Follow == "" {
		t.Fatalf("dispatch body = %+v", body)
	}

	mu.Lock()
	args := append([]string(nil), gotArgs...)
	workspace := gotSpace
	mu.Unlock()
	if workspace != wantWorkspace {
		t.Fatalf("workspace = %q, want %q", workspace, wantWorkspace)
	}
	if len(args) < 2 || args[0] != "codex" || args[1] != "exec" {
		t.Fatalf("codex daemon args = %v, want codex exec", args)
	}
	if containsString(args, "--session-id") || containsString(args, "--agents") || containsString(args, "--append-system-prompt-file") {
		t.Fatalf("codex daemon args include Claude-only flags: %v", args)
	}
	if !containsString(args, "--add-dir") || !strings.Contains(args[len(args)-1], "codex task") {
		t.Fatalf("codex daemon args missing add-dir or task prompt: %v", args)
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

func TestRunJSONRequiresPrompt(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--json"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --json without --prompt to fail")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--json requires --prompt or --detach") {
		t.Fatalf("stderr = %q, want --json requires --prompt or --detach", stderr.String())
	}
}

func TestRunJSONRejectsNoDaemon(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--prompt", "hello", "--json", "--no-daemon"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --json with --no-daemon to fail")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--json cannot be combined with --no-daemon") {
		t.Fatalf("stderr = %q, want --no-daemon validation", stderr.String())
	}
}

func TestRunDetachRejectsNoDaemon(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--detach", "--no-daemon"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --detach with --no-daemon to fail")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--detach cannot be combined with --no-daemon") {
		t.Fatalf("stderr = %q, want --detach validation", stderr.String())
	}
}

func TestRunNegativeReadyTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--detach", "--ready-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected ready-timeout validation error")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "--ready-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want ready-timeout validation", stderr.String())
	}
}

func TestRunJSONRequiresRunningDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--prompt", "hello", "--json", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --json without daemon to fail")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "daemon is not running") {
		t.Fatalf("stderr = %q, want daemon hint", stderr.String())
	}
}

func TestRunFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "manager", "--format", "{{.Instance}}"}, "--format requires --prompt or --detach"},
		{[]string{"run", "manager", "--detach", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined with --json"},
		{[]string{"run", "manager", "--prompt", "hello", "--format", "{{.Instance}}", "--no-daemon"}, "--format cannot be combined with --no-daemon"},
		{[]string{"run", "manager", "--detach", "--format", "{{"}, "invalid --format template"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestRunAttachRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "manager", "--attach", "--json"}, "--attach cannot be combined with --json"},
		{[]string{"run", "manager", "--attach", "--format", "{{.Instance}}"}, "--format cannot be combined with --attach"},
		{[]string{"run", "manager", "--attach", "--no-daemon"}, "--attach cannot be combined with --no-daemon"},
		{[]string{"run", "manager", "--attach", "--detach"}, "choose one of --detach or --attach"},
		{[]string{"run", "manager", "--tail", "all"}, "--tail requires --attach"},
		{[]string{"run", "manager", "--attach", "--tail", "-1"}, "--tail must be >= 0"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestRunFormatRequiresRunningDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--prompt", "hello", "--format", "{{.Instance}}", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --format without daemon to fail")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), "daemon is not running") {
		t.Fatalf("stderr = %q, want daemon hint", stderr.String())
	}
}

func TestRunAttachDispatchesThroughDaemonAndFollowsLog(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-run-attach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	base := fakeSpawnerForTest(t, 2*time.Second)
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), func(args []string, env []string, workspace, stdoutPath, stderrPath string) (*os.Process, error) {
		if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(stdoutPath, []byte("attach log\n"), 0o644); err != nil {
			return nil, err
		}
		return base(args, env, workspace, stdoutPath, stderrPath)
	})
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	defer func() {
		for _, meta := range mgr.List() {
			if meta.Instance == "manager-attach" && meta.Status == daemon.StatusRunning {
				stopAndWaitForTest(t, mgr, "manager-attach")
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--name", "manager-attach", "--target", tmp, "--attach", "--tail", "all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run --attach: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	body := out.String()
	for _, want := range []string{"dispatched manager-attach", "attaching to manager-attach", "attach log\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("run --attach output missing %q:\n%s", want, body)
		}
	}
}

func TestRunDetachDispatchesThroughDaemonWithoutPrompt(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-run-detach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	wantWorkspace := tmp
	if eval, err := filepath.EvalSymlinks(tmp); err == nil {
		wantWorkspace = eval
	}
	teamDir := filepath.Join(tmp, ".agent_team")

	base := fakeSpawnerForTest(t, 2*time.Second)
	var (
		mu       sync.Mutex
		gotArgs  []string
		gotEnv   []string
		gotSpace string
	)
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), func(args []string, env []string, workspace, stdoutPath, stderrPath string) (*os.Process, error) {
		mu.Lock()
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		gotSpace = workspace
		mu.Unlock()
		return base(args, env, workspace, stdoutPath, stderrPath)
	})
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	defer func() {
		for _, meta := range mgr.List() {
			if meta.Instance == "manager" {
				stopAndWaitForTest(t, mgr, "manager")
				return
			}
		}
	}()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--detach", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run --detach --json: %v\nstderr: %s", err, stderr.String())
	}

	var body runDispatchJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("json body: %v\nstdout: %s", err, out.String())
	}
	if body.Instance != "manager" || body.Agent != "manager" || body.PID == 0 || body.SessionID == "" || body.Follow == "" {
		t.Fatalf("dispatch body = %+v", body)
	}

	mu.Lock()
	args := append([]string(nil), gotArgs...)
	env := append([]string(nil), gotEnv...)
	workspace := gotSpace
	mu.Unlock()
	if workspace != wantWorkspace {
		t.Fatalf("workspace = %q, want %q", workspace, wantWorkspace)
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-p" {
			t.Fatalf("detached no-prompt dispatch should not add -p, args=%v", args)
		}
	}
	for _, want := range []string{"--agents", "--add-dir", "--append-system-prompt-file"} {
		if !containsString(args, want) {
			t.Fatalf("detached dispatch args missing %s: %v", want, args)
		}
	}
	if !containsEnvPrefix(env, "AGENT_TEAM_INSTANCE=manager") {
		t.Fatalf("detached dispatch env missing AGENT_TEAM_INSTANCE: %v", env)
	}
}

func TestRunDetachFormatPrintsDispatchMetadata(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-run-format-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	defer func() {
		for _, meta := range mgr.List() {
			if meta.Instance == "manager-format" {
				stopAndWaitForTest(t, mgr, "manager-format")
				return
			}
		}
	}()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--name", "manager-format", "--target", tmp, "--detach", "--format", "{{.Instance}}:{{.Agent}}:{{.PID}}:{{.Follow}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run --detach --format: %v\nstderr: %s", err, stderr.String())
	}
	parts := strings.SplitN(strings.TrimSpace(out.String()), ":", 4)
	if len(parts) != 4 {
		t.Fatalf("formatted run output = %q, want four fields", out.String())
	}
	if parts[0] != "manager-format" || parts[1] != "manager" || parts[2] == "0" || parts[3] == "" {
		t.Fatalf("formatted run output = %q, want populated dispatch metadata", out.String())
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

func TestSubscribeAgentChannels_PostsToDaemon(t *testing.T) {
	env := newChannelTestEnv(t)
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	subscribeAgentChannels(cmd, env.client, "manager-billing", []string{"#blocked", "#review-requests", "  ", ""})

	infos, err := env.client.ChannelList()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("channels: got %d want 2 (%+v)", len(infos), infos)
	}
	got := []string{infos[0].Name, infos[1].Name}
	want := []string{"#blocked", "#review-requests"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("at %d: got %q want %q", i, got[i], w)
		}
	}
	for _, info := range infos {
		if info.Subscribers != 1 {
			t.Errorf("%s subscribers: got %d want 1", info.Name, info.Subscribers)
		}
	}
}

func TestSubscribeAgentChannels_ErrorIsLoggedNotFatal(t *testing.T) {
	env := newChannelTestEnv(t)
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	stderr := &bytes.Buffer{}
	cmd.SetErr(stderr)
	// Invalid name (uppercase) → daemon rejects with 400.
	subscribeAgentChannels(cmd, env.client, "x", []string{"#BAD"})
	if !strings.Contains(stderr.String(), "failed to pre-subscribe") {
		t.Errorf("stderr should warn about failed subscribe: %q", stderr.String())
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
