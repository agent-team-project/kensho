package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/runtimehooks"
	"github.com/agent-team-project/agent-team/internal/runtimeshim"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

// captureRun replaces the execClaude hook with a recorder for the duration of
// a test. The captured args reference paths inside a tmpdir that runAgent
// cleans up via defer, so the recorder snapshots filesystem state synchronously
// while the dir is still alive.
type runCapture struct {
	bin          string
	args         []string
	env          []string
	cwd          string
	rc           error
	skillsDirOK  bool
	promptBody   string
	stdin        string
	addDir       string
	promptFile   string
	agentsJSON   string
	skills       []string
	settings     string
	settingsBody string
	shims        []string
}

type runtimeCapture struct {
	bin         string
	args        []string
	env         []string
	cwd         string
	stdin       string
	rc          error
	stdout      string
	stderr      string
	lastMessage string
}

func captureRun(t *testing.T, rc error) (*runCapture, func()) {
	t.Helper()
	cap := &runCapture{rc: rc}
	prev := execClaude
	execClaude = func(cmd *cobra.Command, bin string, args []string, env []string, cwd, stdin string) error {
		cap.bin = bin
		cap.args = args
		cap.env = env
		cap.cwd = cwd
		cap.stdin = stdin
		// Snapshot filesystem state before runAgent's `defer os.RemoveAll(tmpdir)` fires.
		for i := 0; i+1 < len(args); i++ {
			switch args[i] {
			case "--add-dir":
				cap.addDir = args[i+1]
				skillsRoot := filepath.Join(cap.addDir, ".claude", "skills")
				if st, err := os.Stat(skillsRoot); err == nil && st.IsDir() {
					cap.skillsDirOK = true
					if entries, err := os.ReadDir(skillsRoot); err == nil {
						for _, entry := range entries {
							cap.skills = append(cap.skills, entry.Name())
						}
						sort.Strings(cap.skills)
					}
				}
				shimRoot := filepath.Join(cap.addDir, "bin")
				if entries, err := os.ReadDir(shimRoot); err == nil {
					for _, entry := range entries {
						cap.shims = append(cap.shims, entry.Name())
					}
					sort.Strings(cap.shims)
				}
			case "--append-system-prompt-file":
				cap.promptFile = args[i+1]
				if b, err := os.ReadFile(cap.promptFile); err == nil {
					cap.promptBody = string(b)
				}
			case "--agents":
				cap.agentsJSON = args[i+1]
			case "--settings":
				cap.settings = args[i+1]
				if b, err := os.ReadFile(cap.settings); err == nil {
					cap.settingsBody = string(b)
				}
			}
		}
		return cap.rc
	}
	return cap, func() { execClaude = prev }
}

func captureRuntime(t *testing.T, rc error) (*runtimeCapture, func()) {
	t.Helper()
	cap := &runtimeCapture{rc: rc}
	prev := execRuntime
	execRuntime = func(cmd *cobra.Command, bin string, args []string, env []string, cwd, stdin string, stdout, stderr io.Writer) error {
		cap.bin = bin
		cap.args = args
		cap.env = env
		cap.cwd = cwd
		cap.stdin = stdin
		if cap.stdout != "" {
			_, _ = io.WriteString(stdout, cap.stdout)
		}
		if cap.stderr != "" {
			_, _ = io.WriteString(stderr, cap.stderr)
		}
		if cap.lastMessage != "" {
			path, ok := argValue(args, "--output-last-message")
			if !ok {
				t.Fatalf("runtime args missing --output-last-message: %v", args)
			}
			if err := os.WriteFile(path, []byte(cap.lastMessage), 0o644); err != nil {
				t.Fatalf("write captured last message: %v", err)
			}
		}
		return cap.rc
	}
	return cap, func() { execRuntime = prev }
}

func startRunTestDaemon(t *testing.T, teamDir string, mgr *daemon.InstanceManager) func() {
	return startRunTestDaemonWithBuild(t, teamDir, mgr, buildinfo.Info{})
}

func startRunTestDaemonWithBuild(t *testing.T, teamDir string, mgr *daemon.InstanceManager, build buildinfo.Info) func() {
	t.Helper()
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatalf("mkdir daemon root: %v", err)
	}
	if build.Empty() {
		build = BuildInfo()
	}
	if err := daemon.WriteLaunchEnv(daemon.DaemonRoot(teamDir), &daemon.LaunchEnv{
		Bin:        "/test/bin/agent-teamd",
		Args:       []string{"/test/bin/agent-teamd", "--target", filepath.Dir(teamDir)},
		Dir:        filepath.Dir(teamDir),
		RecordedAt: time.Now().UTC().Truncate(time.Second),
		PID:        os.Getpid(),
		Version:    1,
		Build:      build,
	}); err != nil {
		t.Fatalf("write launch env: %v", err)
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
	srv := &http.Server{Handler: daemon.Handler(mgr, nil, resolver, teamDir, build)}
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

func argValue(items []string, flag string) (string, bool) {
	for i := 0; i+1 < len(items); i++ {
		if items[i] == flag {
			return items[i+1], true
		}
	}
	return "", false
}

func containsEnvPrefix(env []string, prefix string) bool {
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func TestMailboxHookCommandDrainsUnreadMessages(t *testing.T) {
	repo := t.TempDir()
	teamDir := filepath.Join(repo, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.AppendMessage(root, "worker", &daemon.Message{ID: "msg-1", From: "manager", Body: "first"}); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := daemon.AppendMessage(root, "worker", &daemon.Message{ID: "msg-2", From: "reviewer", Body: "second"}); err != nil {
		t.Fatalf("append second: %v", err)
	}
	hook, err := runtimehooks.PrepareMailboxHook(t.TempDir())
	if err != nil {
		t.Fatalf("prepare hook: %v", err)
	}

	cmd := exec.Command("sh", "-c", hook.Command)
	cmd.Env = append(os.Environ(),
		"AGENT_TEAM_ROOT="+teamDir,
		"AGENT_TEAM_INSTANCE=worker",
	)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"UserPromptSubmit"}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook command: %v\n%s", err, out)
	}
	var body struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &body); err != nil {
		t.Fatalf("decode hook output: %v\n%s", err, out)
	}
	if body.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("hook event = %q", body.HookSpecificOutput.HookEventName)
	}
	for _, want := range []string{"New daemon mailbox messages", "first", "second", "From: manager", "From: reviewer"} {
		if !strings.Contains(body.HookSpecificOutput.AdditionalContext, want) {
			t.Fatalf("hook context missing %q:\n%s", want, body.HookSpecificOutput.AdditionalContext)
		}
	}
	unread, err := daemon.ReadUnacked(root, "worker")
	if err != nil {
		t.Fatalf("read unacked: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("unread after hook = %+v, want none", unread)
	}
	cursor, err := daemon.ReadCursor(root, "worker")
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor != "msg-2" {
		t.Fatalf("cursor = %q, want msg-2", cursor)
	}
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

func appendAuthorityTopology(t *testing.T, path string) {
	t.Helper()
	body := `

[authority.agents.worker]
allow = ["job.gate.*:own"]

[authority.teams.delivery]
allow = ["event.publish"]
`
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

func writeOTelRunConfig(t *testing.T, dir string) {
	t.Helper()
	body := `[team]
pm_tool = "none"

[otel]
enabled = true
endpoint = "http://collector:4318"

[otel.headers]
authorization = "Bearer secret"

[otel.resource]
"deployment.environment" = "test"
`
	if err := os.WriteFile(filepath.Join(dir, ".agent_team", "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write otel config: %v", err)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func assertRuntimeCommandSurface(t *testing.T, addDir string, env []string, shims []string) {
	t.Helper()
	if addDir == "" {
		t.Fatalf("missing runtime add-dir")
	}
	path := envValue(env, "PATH")
	if path == "" {
		t.Fatalf("runtime env missing PATH: %v", env)
	}
	wantBin := filepath.Join(addDir, "bin")
	if got := strings.Split(path, string(os.PathListSeparator))[0]; got != wantBin {
		t.Fatalf("PATH first entry = %q, want runtime shim bin %q; PATH=%q", got, wantBin, path)
	}
	for _, want := range []string{"agent-team", "channel.sh", "inbox"} {
		if !containsString(shims, want) {
			t.Fatalf("runtime shims = %v, want %s", shims, want)
		}
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
	wantAgents := []string{"auditor", "comms", "manager", "reviewer", "ticket-manager", "worker"}
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
	assertRuntimeCommandSurface(t, cap.addDir, cap.env, cap.shims)
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
	hasRoot, hasInstance, hasState, hasSocket := false, false, false, false
	for _, e := range cap.env {
		switch {
		case strings.HasPrefix(e, "AGENT_TEAM_ROOT="):
			hasRoot = true
		case strings.HasPrefix(e, "AGENT_TEAM_INSTANCE=manager"):
			hasInstance = true
		case strings.HasPrefix(e, "AGENT_TEAM_STATE_DIR="):
			hasState = true
		case strings.HasPrefix(e, "AGENT_TEAM_DAEMON_SOCKET="):
			hasSocket = true
		}
	}
	if !hasRoot || !hasInstance || !hasState || !hasSocket {
		t.Errorf("missing AGENT_TEAM_* env vars: root=%v instance=%v state=%v socket=%v", hasRoot, hasInstance, hasState, hasSocket)
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
	if cap.settings == "" {
		t.Fatalf("missing --settings for mailbox hooks: %v", cap.args)
	}
	for _, want := range []string{"UserPromptSubmit", "PreToolUse", "agent-team-mailbox-inject.py"} {
		if !strings.Contains(cap.settingsBody, want) {
			t.Fatalf("mailbox hook settings missing %q:\n%s", want, cap.settingsBody)
		}
	}
}

func TestRun_ExportsAuthorityAllowlistFromTopology(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	appendAuthorityTopology(t, filepath.Join(tmp, ".agent_team", "instances.toml"))

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "worker", "--target", tmp, "--name", "worker-squ-123", "--prompt", "kickoff message", "--no-daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := envValue(cap.env, runtimeshim.EnvAuthorityAllowlist); got != "event.publish,job.gate.*:own" {
		t.Fatalf("%s = %q, want topology allowlist", runtimeshim.EnvAuthorityAllowlist, got)
	}
}

func TestRun_StagesTeamLevelSkills(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "skills", "team-only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "skills", "team-only", "SKILL.md"), []byte("team skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	setTeamSkillsForTest(t, teamDir, "team-only")

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "kickoff message"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !containsString(cap.skills, "team-only") {
		t.Fatalf("staged skills = %v, want team-only", cap.skills)
	}
}

func TestRun_MailboxHookOptOutSuppressesClaudeSettings(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "kickoff message", "--set", "runtime.hooks.mailbox_injection=false", "--no-daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if cap.settings != "" || containsString(cap.args, "--settings") {
		t.Fatalf("mailbox hook opt-out still added settings: settings=%q args=%v", cap.settings, cap.args)
	}
}

func TestRun_ClaudeOTelInjectionFromConfig(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writeOTelRunConfig(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "kickoff message", "--no-daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, want := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318",
		"OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer secret",
	} {
		if !containsString(cap.env, want) {
			t.Fatalf("env missing %q: %#v", want, cap.env)
		}
	}
	if !containsEnvPrefix(cap.env, "TRACEPARENT=00-") {
		t.Fatalf("env missing TRACEPARENT: %#v", cap.env)
	}
	resource := envValue(cap.env, "OTEL_RESOURCE_ATTRIBUTES")
	for _, want := range []string{
		"service.name=agent-team/manager",
		"agent_team.instance=manager",
		"agent_team.team=delivery",
		"agent_team.runtime=claude",
		"deployment.environment=test",
	} {
		if !strings.Contains(resource, want) {
			t.Fatalf("resource attrs missing %q in %q", want, resource)
		}
	}
}

func TestRun_CodexOTelInjectionFromConfig(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp := t.TempDir()
	initInto(t, tmp)
	writeOTelRunConfig(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "codex task", "--no-daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !containsEnvPrefix(cap.env, "TRACEPARENT=00-") {
		t.Fatalf("codex env missing TRACEPARENT: %#v", cap.env)
	}
	if !containsString(cap.env, "AGENTTEAM_OTEL_HEADER_0=Bearer secret") {
		t.Fatalf("codex env missing header indirection: %#v", cap.env)
	}
	joined := strings.Join(cap.args, "\n")
	if strings.Contains(joined, "Bearer secret") {
		t.Fatalf("codex args leaked header secret:\n%s", joined)
	}
	for _, want := range []string{
		"otel.exporter={ otlp-http = { endpoint = \"http://collector:4318\", protocol = \"binary\", headers = { \"authorization\" = \"${AGENTTEAM_OTEL_HEADER_0}\" } } }",
		"otel.trace_exporter=\"otlp-http\"",
		"otel.trace_exporter.\"otlp-http\".endpoint=\"http://collector:4318\"",
		"otel.trace_exporter.\"otlp-http\".protocol=\"binary\"",
		"otel.trace_exporter.\"otlp-http\".headers={ \"authorization\" = \"${AGENTTEAM_OTEL_HEADER_0}\" }",
		"otel.log_user_prompt=false",
		"otel.span_attributes={",
		"\"service.name\" = \"agent-team/manager\"",
		"shell_environment_policy.set.TRACEPARENT=",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("codex args missing %q:\n%s", want, joined)
		}
	}
}

func TestRunPromptFileFromStdin(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	oldInput := sendMessageInput
	sendMessageInput = strings.NewReader("prompt from stdin\n")
	defer func() { sendMessageInput = oldInput }()

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt-file", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run prompt file stdin: %v", err)
	}

	foundPromptFlag := false
	for i := 0; i < len(cap.args)-1; i++ {
		if cap.args[i] == "-p" && cap.args[i+1] == "prompt from stdin" {
			foundPromptFlag = true
		}
	}
	if !foundPromptFlag {
		t.Fatalf("-p prompt from stdin not forwarded: %v", cap.args)
	}
}

func TestRun_RepoFlagOverridesTarget(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	badTarget := t.TempDir()

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--repo", tmp, "run", "manager", "--target", badTarget, "--prompt", "kickoff message", "--no-daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run with --repo override: %v", err)
	}

	wantRoot := tmp
	if eval, err := filepath.EvalSymlinks(wantRoot); err == nil {
		wantRoot = eval
	}
	if cap.cwd != wantRoot {
		t.Fatalf("cwd = %q, want repo root %q", cap.cwd, wantRoot)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".agent_team", "state", "manager")); err != nil {
		t.Fatalf("expected state in --repo target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(badTarget, ".agent_team", "state", "manager")); !os.IsNotExist(err) {
		t.Fatalf("unexpected state in legacy --target: %v", err)
	}
}

func TestRun_CodexRuntimeBuildsDirectExecArgs(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp := t.TempDir()
	initInto(t, tmp)
	staleLastMessage := filepath.Join(tmp, ".agent_team", "state", "manager", runtimebin.CodexLastMessageFile)
	if err := os.MkdirAll(filepath.Dir(staleLastMessage), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleLastMessage, []byte("stale response"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	if !containsString(cap.args, "--dangerously-bypass-hook-trust") {
		t.Fatalf("codex args missing hook trust bypass for generated mailbox hook: %v", cap.args)
	}
	if !argsContainSubstring(cap.args, "hooks.UserPromptSubmit") || !argsContainSubstring(cap.args, "hooks.PreToolUse") || !argsContainSubstring(cap.args, "agent-team-mailbox-inject.py") {
		t.Fatalf("codex args missing mailbox hook config: %v", cap.args)
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
	assertRuntimeCommandSurface(t, cap.addDir, cap.env, cap.shims)
	if !containsString(cap.args, "--sandbox") || !containsString(cap.args, "workspace-write") {
		t.Fatalf("forwarded codex args missing: %v", cap.args)
	}
	wantTeamDir := filepath.Join(cap.cwd, ".agent_team")
	wantLastMessage := filepath.Join(wantTeamDir, "state", "manager", runtimebin.CodexLastMessageFile)
	if got, ok := argValue(cap.args, "--output-last-message"); !ok || got != wantLastMessage {
		t.Fatalf("codex args last-message path = %q, %v; want %q in %v", got, ok, wantLastMessage, cap.args)
	}
	if _, err := os.Stat(wantLastMessage); !os.IsNotExist(err) {
		t.Fatalf("stale last message still exists or stat failed: %v", err)
	}
	for _, want := range []string{
		"shell_environment_policy.set.AGENT_TEAM_ROOT=" + strconv.Quote(wantTeamDir),
		"shell_environment_policy.set.AGENT_TEAM_INSTANCE=" + strconv.Quote("manager"),
		"shell_environment_policy.set.AGENT_TEAM_STATE_DIR=" + strconv.Quote(filepath.Join(wantTeamDir, "state", "manager")),
		"shell_environment_policy.set.AGENT_TEAM_DAEMON_SOCKET=" + strconv.Quote(daemon.SocketPath(wantTeamDir)),
		"shell_environment_policy.set.PATH=" + strconv.Quote(envValue(cap.env, "PATH")),
	} {
		if !containsString(cap.args, want) {
			t.Fatalf("codex args missing env config %q: %v", want, cap.args)
		}
	}
	if got := cap.args[len(cap.args)-1]; got != "-" {
		t.Fatalf("codex prompt arg = %q, want stdin marker '-' in %v", got, cap.args)
	}
	for _, want := range []string{
		"You are the `manager` instance of the `manager` agent.",
		"This session is running through the Codex adapter.",
		"Available team agents:",
		"codex task",
	} {
		if !strings.Contains(cap.stdin, want) {
			t.Fatalf("codex stdin prompt missing %q:\n%s", want, cap.stdin)
		}
	}
}

func argsContainSubstring(args []string, want string) bool {
	for _, arg := range args {
		if strings.Contains(arg, want) {
			return true
		}
	}
	return false
}

func TestRun_CodexLastMessagePrintsCleanSidecar(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRuntime(t, nil)
	defer restore()
	cap.stdout = "raw codex stdout\n"
	cap.stderr = "raw codex stderr\n"
	cap.lastMessage = "clean codex answer\n"

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "codex task", "--last-message"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run last-message: %v\nstderr: %s", err, stderr.String())
	}
	if got := stdout.String(); got != "clean codex answer\n" {
		t.Fatalf("stdout = %q, want clean sidecar only", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want raw stderr suppressed on success", got)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Fatalf("codex args = %v, want exec", cap.args)
	}
	wantTeamDir := filepath.Join(cap.cwd, ".agent_team")
	wantLastMessage := filepath.Join(wantTeamDir, "state", "manager", runtimebin.CodexLastMessageFile)
	if got, ok := argValue(cap.args, "--output-last-message"); !ok || got != wantLastMessage {
		t.Fatalf("codex args last-message path = %q, %v; want %q in %v", got, ok, wantLastMessage, cap.args)
	}
	if !strings.Contains(cap.stdin, "codex task") {
		t.Fatalf("codex stdin missing task:\n%s", cap.stdin)
	}
}

func TestRun_CodexLastMessageCanUseRuntimeFlag(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindClaude))
	t.Setenv(runtimebin.EnvBinary, "claude-env-wrapper")
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRuntime(t, nil)
	defer restore()
	cap.lastMessage = "clean flag-selected codex answer\n"

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"run", "manager",
		"--target", tmp,
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--prompt", "codex task",
		"--last-message",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run last-message with runtime flag: %v\nstderr: %s", err, stderr.String())
	}
	if got := stdout.String(); got != "clean flag-selected codex answer\n" {
		t.Fatalf("stdout = %q, want clean sidecar only", got)
	}
	if cap.bin != "codex-dev" {
		t.Fatalf("runtime binary = %q, want explicit codex-dev", cap.bin)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Fatalf("codex args = %v, want exec", cap.args)
	}
}

func TestRun_CodexLastMessageReplaysRawOutputOnFailure(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRuntime(t, exitErr(17))
	defer restore()
	cap.stdout = "raw stdout before failure\n"
	cap.stderr = "raw stderr before failure\n"

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "codex task", "--last-message"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected runtime failure")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 17 {
		t.Fatalf("err = %v, want exit 17", err)
	}
	if got := stdout.String(); got != cap.stdout {
		t.Fatalf("stdout = %q, want replayed raw stdout", got)
	}
	if got := stderr.String(); got != cap.stderr {
		t.Fatalf("stderr = %q, want replayed raw stderr", got)
	}
}

func TestRun_CodexLastMessageMissingSidecarFails(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp := t.TempDir()
	initInto(t, tmp)

	_, restore := captureRuntime(t, nil)
	defer restore()

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "codex task", "--last-message"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected missing last-message sidecar to fail")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(stderr.String(), "Codex last message not found") {
		t.Fatalf("stderr = %q, want missing last-message hint", stderr.String())
	}
}

func TestRun_CodexRuntimeCanComeFromRepoConfig(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "")
	t.Setenv(runtimebin.EnvBinary, "")
	tmp := t.TempDir()
	initInto(t, tmp)
	appendRuntimeConfigForRunTest(t, tmp, "codex", "codex-wrapper")

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "codex task", "--no-daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Fatalf("codex args = %v, want exec subcommand from repo config", cap.args)
	}
	if cap.bin != "codex-wrapper" {
		t.Fatalf("runtime binary = %q, want repo-configured codex-wrapper", cap.bin)
	}
	if containsString(cap.args, "--agents") || containsString(cap.args, "--append-system-prompt-file") {
		t.Fatalf("config-backed codex args include Claude flags: %v", cap.args)
	}
}

func TestRun_RuntimeFlagOverridesEnvRuntimeAndBinary(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "bad-env-runtime")
	t.Setenv(runtimebin.EnvBinary, "claude-env-wrapper")
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--runtime", "codex", "--prompt", "codex task", "--no-daemon"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if cap.bin != "codex" {
		t.Fatalf("runtime binary = %q, want codex default instead of env binary", cap.bin)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Fatalf("codex args = %v, want exec subcommand from runtime flag", cap.args)
	}
}

func TestRun_RuntimeBinFlagOverridesSelectedRuntimeBinary(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindClaude))
	t.Setenv(runtimebin.EnvBinary, "claude-env-wrapper")
	tmp := t.TempDir()
	initInto(t, tmp)

	cap, restore := captureRun(t, nil)
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"run", "manager",
		"--target", tmp,
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--prompt", "codex task",
		"--no-daemon",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if cap.bin != "codex-dev" {
		t.Fatalf("runtime binary = %q, want explicit codex-dev", cap.bin)
	}
	if len(cap.args) == 0 || cap.args[0] != "exec" {
		t.Fatalf("codex args = %v, want exec subcommand from runtime flag", cap.args)
	}
}

func TestRun_InvalidRuntimeFlagExitsTwo(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--runtime", "bad-runtime", "--prompt", "hello", "--no-daemon"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected invalid runtime flag to fail")
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(stderr.String(), `--runtime must be "claude", "codex", or "docker"`) {
		t.Fatalf("stderr = %q, want runtime flag validation", stderr.String())
	}
}

func TestDaemonURLForRuntimeEnvPrefersDockerHostGateway(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.HTTPAddrPath(teamDir), []byte("127.0.0.1:54321\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_TEAM_DAEMON_URL", "http://host.docker.internal:54321/")

	if got, want := daemonURLForRuntimeEnv(teamDir), "http://host.docker.internal:54321"; got != want {
		t.Fatalf("daemonURLForRuntimeEnv() = %q, want %q", got, want)
	}
}

func TestDaemonURLForRuntimeEnvPrefersRepoLoopbackOverInheritedLoopback(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.HTTPAddrPath(teamDir), []byte("127.0.0.1:54321\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_TEAM_DAEMON_URL", "http://127.0.0.1:11111")

	if got, want := daemonURLForRuntimeEnv(teamDir), "http://127.0.0.1:54321"; got != want {
		t.Fatalf("daemonURLForRuntimeEnv() = %q, want %q", got, want)
	}
}

func TestDaemonURLForRuntimeEnvFallsBackToInheritedWhenNoRepoHTTPAddr(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	t.Setenv("AGENT_TEAM_DAEMON_URL", "http://127.0.0.1:11111/")

	if got, want := daemonURLForRuntimeEnv(teamDir), "http://127.0.0.1:11111"; got != want {
		t.Fatalf("daemonURLForRuntimeEnv() = %q, want %q", got, want)
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

func appendRuntimeConfigForRunTest(t *testing.T, repo, kind, binary string) {
	t.Helper()
	cfg := filepath.Join(repo, ".agent_team", "config.toml")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[runtime]\nkind = %q\n", kind); err != nil {
		t.Fatalf("write runtime kind: %v", err)
	}
	if binary != "" {
		if _, err := fmt.Fprintf(f, "binary = %q\n", binary); err != nil {
			t.Fatalf("write runtime binary: %v", err)
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
		gotEnv   []string
		gotSpace string
		gotStdin string
	)
	base := fakeSpawnerForTest(t, 2*time.Second)
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		mu.Lock()
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		gotSpace = workspace
		gotStdin = stdinContent
		mu.Unlock()
		return base(args, env, workspace, stdoutPath, stderrPath, stdinContent)
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
	env := append([]string(nil), gotEnv...)
	workspace := gotSpace
	stdin := gotStdin
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
	if !containsString(args, "--add-dir") || args[len(args)-1] != "-" {
		t.Fatalf("codex daemon args missing add-dir or stdin marker: %v", args)
	}
	if !strings.Contains(stdin, "codex task") || !strings.Contains(stdin, "This session is running through the Codex adapter.") {
		t.Fatalf("codex daemon stdin missing task or adapter prompt:\n%s", stdin)
	}
	wantTeamDir := filepath.Join(workspace, ".agent_team")
	wantLastMessage := filepath.Join(wantTeamDir, "state", "manager", runtimebin.CodexLastMessageFile)
	if got, ok := argValue(args, "--output-last-message"); !ok || got != wantLastMessage {
		t.Fatalf("codex daemon args last-message path = %q, %v; want %q in %v", got, ok, wantLastMessage, args)
	}
	wantShimBin := filepath.Join(wantTeamDir, "state", "manager", "bin")
	path := envValue(env, "PATH")
	if path == "" {
		t.Fatalf("codex daemon env missing PATH: %v", env)
	}
	if got := strings.Split(path, string(os.PathListSeparator))[0]; got != wantShimBin {
		t.Fatalf("codex daemon PATH first entry = %q, want durable shim bin %q; PATH=%q", got, wantShimBin, path)
	}
	for _, name := range []string{"channel.sh", "inbox"} {
		if st, err := os.Stat(filepath.Join(wantShimBin, name)); err != nil {
			t.Fatalf("durable runtime shim %s missing after dispatch: %v", name, err)
		} else if st.Mode().Perm()&0o111 == 0 {
			t.Fatalf("durable runtime shim %s is not executable: mode=%s", name, st.Mode())
		}
	}
	for _, want := range []string{
		"shell_environment_policy.set.AGENT_TEAM_ROOT=" + strconv.Quote(wantTeamDir),
		"shell_environment_policy.set.AGENT_TEAM_INSTANCE=" + strconv.Quote("manager"),
		"shell_environment_policy.set.AGENT_TEAM_STATE_DIR=" + strconv.Quote(filepath.Join(wantTeamDir, "state", "manager")),
		"shell_environment_policy.set.AGENT_TEAM_DAEMON_SOCKET=" + strconv.Quote(daemon.SocketPath(wantTeamDir)),
		"shell_environment_policy.set.PATH=" + strconv.Quote(path),
	} {
		if !containsString(args, want) {
			t.Fatalf("codex daemon args missing env config %q: %v", want, args)
		}
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

func TestRunPromptFileValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "manager", "--prompt", "hello", "--prompt-file", "task.txt"}, "provide prompt text using only one of --prompt or --prompt-file"},
		{[]string{"run", "manager", "--prompt-file", filepath.Join(t.TempDir(), "missing.txt")}, "--prompt-file:"},
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

func TestRunLastMessageRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "manager", "--last-message"}, "--last-message requires --prompt"},
		{[]string{"run", "manager", "--prompt", "hello", "--last-message", "--json"}, "--last-message cannot be combined with --json"},
		{[]string{"run", "manager", "--prompt", "hello", "--last-message", "--format", "{{.Instance}}"}, "--last-message cannot be combined with --format"},
		{[]string{"run", "manager", "--prompt", "hello", "--last-message", "--detach"}, "--last-message cannot be combined with --detach"},
		{[]string{"run", "manager", "--prompt", "hello", "--last-message", "--attach"}, "--last-message cannot be combined with --attach"},
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

func TestRunLastMessageRequiresCodexRuntime(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindClaude))
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"run", "manager", "--target", tmp, "--prompt", "hello", "--last-message"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --last-message with claude runtime to fail")
	}
	if !strings.Contains(stderr.String(), "--last-message requires the codex runtime") {
		t.Fatalf("stderr = %q, want codex runtime validation", stderr.String())
	}
}

func TestRunLastMessageRejectsForwardedOutputLastMessage(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, string(runtimebin.KindCodex))
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"run", "manager",
		"--target", tmp,
		"--prompt", "hello",
		"--last-message",
		"--",
		"--output-last-message", filepath.Join(tmp, "other.txt"),
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected forwarded --output-last-message conflict")
	}
	if !strings.Contains(stderr.String(), "--last-message cannot be combined with forwarded --output-last-message") {
		t.Fatalf("stderr = %q, want forwarded flag validation", stderr.String())
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
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(stdoutPath, []byte("attach log\n"), 0o644); err != nil {
			return nil, err
		}
		return base(args, env, workspace, stdoutPath, stderrPath, stdinContent)
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
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		mu.Lock()
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		gotSpace = workspace
		mu.Unlock()
		return base(args, env, workspace, stdoutPath, stderrPath, stdinContent)
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
