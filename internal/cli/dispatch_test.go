package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
)

func TestDispatchPayloadDefaults(t *testing.T) {
	payload, name, err := buildDispatchEventPayload("worker", "SQU-42", "SQU-42: fix it", "", "", "auto")
	if err != nil {
		t.Fatalf("buildDispatchEventPayload: %v", err)
	}
	if name != "worker-squ-42" {
		t.Fatalf("name = %q, want worker-squ-42", name)
	}
	for key, want := range map[string]any{
		"source":    "cli",
		"target":    "worker",
		"name":      "worker-squ-42",
		"ticket":    "SQU-42",
		"kickoff":   "SQU-42: fix it",
		"workspace": "worktree",
	} {
		if got := payload[key]; got != want {
			t.Fatalf("payload[%s] = %#v, want %#v", key, got, want)
		}
	}
}

func TestDispatchKickoffSources(t *testing.T) {
	got, err := dispatchKickoff("SQU-42", "", "", nil)
	if err != nil || got != "SQU-42" {
		t.Fatalf("default kickoff = %q err=%v, want ticket", got, err)
	}
	got, err = dispatchKickoff("SQU-42", "", "", []string{"fix", "it"})
	if err != nil || got != "SQU-42: fix it" {
		t.Fatalf("positional kickoff = %q err=%v, want ticket prefix", got, err)
	}
	got, err = dispatchKickoff("SQU-42", "SQU-42: already included", "", nil)
	if err != nil || got != "SQU-42: already included" {
		t.Fatalf("flag kickoff = %q err=%v, want unchanged", got, err)
	}
	kickoffFile := filepath.Join(t.TempDir(), "kickoff.txt")
	if err := os.WriteFile(kickoffFile, []byte("file kickoff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = dispatchKickoff("SQU-42", "", kickoffFile, nil)
	if err != nil || got != "SQU-42: file kickoff" {
		t.Fatalf("file kickoff = %q err=%v, want ticket prefix", got, err)
	}
	oldInput := sendMessageInput
	sendMessageInput = strings.NewReader("stdin kickoff\n")
	defer func() { sendMessageInput = oldInput }()
	got, err = dispatchKickoff("SQU-42", "", "-", nil)
	if err != nil || got != "SQU-42: stdin kickoff" {
		t.Fatalf("stdin kickoff = %q err=%v, want ticket prefix", got, err)
	}
	_, err = dispatchKickoff("SQU-42", "", filepath.Join(t.TempDir(), "missing.txt"), nil)
	if err == nil || !strings.Contains(err.Error(), "--kickoff-file:") {
		t.Fatalf("missing kickoff file err=%v, want --kickoff-file prefix", err)
	}
	_, err = dispatchKickoff("SQU-42", "flag", "", []string{"positional"})
	if err == nil || !strings.Contains(err.Error(), "only one") {
		t.Fatalf("conflicting kickoff sources err=%v, want conflict", err)
	}
}

func TestDispatchCommandJSON(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"dispatch", "worker", "SQU-42", "fix", "the", "thing",
		"--workspace", "repo",
		"--json",
		"--target", target,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dispatch --json: %v\nstderr=%s", err, stderr.String())
	}
	var body eventResponse
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode dispatch json: %v\nbody=%s", err, out.String())
	}
	if len(body.Dispatched) != 1 {
		t.Fatalf("dispatched = %+v, want one", body.Dispatched)
	}
	if id, _ := body.Dispatched[0]["instance_id"].(string); id != "worker-squ-42" {
		t.Fatalf("instance_id = %q, want worker-squ-42", id)
	}
	meta, err := daemon.ReadMetadata(daemon.DaemonRoot(filepath.Join(target, ".agent_team")), "worker-squ-42")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Workspace != target {
		t.Fatalf("workspace = %q, want repo root %q", meta.Workspace, target)
	}
	stopAndWaitForTest(t, mgr, "worker-squ-42")
}

func TestDispatchCommandDryRunPreviewsRoutesWithoutDaemon(t *testing.T) {
	target := t.TempDir()
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"dispatch", "worker", "SQU-242", "preview", "dispatch",
		"--target", target,
		"--dry-run",
		"--json",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dispatch --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}
	var preview dispatchRoutePreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatalf("decode dispatch dry-run json: %v\nbody=%s", err, out.String())
	}
	if !preview.DryRun || preview.Target != "worker" || preview.RequestedName != "worker-squ-242" {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Preview == nil || preview.Preview.Type != "agent.dispatch" || len(preview.Preview.Matched) != 1 || preview.Preview.Matched[0] != "worker" {
		t.Fatalf("event preview = %+v", preview.Preview)
	}
	if preview.Preview.Payload["workspace"] != "worktree" ||
		preview.Preview.Payload["kickoff"] != "SQU-242: preview dispatch" ||
		preview.Preview.Payload["runtime"] != "codex" ||
		preview.Preview.Payload["runtime_binary"] != "codex-dev" {
		t.Fatalf("payload = %+v", preview.Preview.Payload)
	}
	if _, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), "worker-squ-242"); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote daemon metadata, err=%v", err)
	}

	textCmd := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	textCmd.SetOut(textOut)
	textCmd.SetErr(textErr)
	textCmd.SetArgs([]string{"dispatch", "worker", "SQU-243", "--target", target, "--dry-run"})
	if err := textCmd.Execute(); err != nil {
		t.Fatalf("dispatch --dry-run text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Dispatch: worker instance=worker-squ-243", "Dry run: true", "Matched: worker"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("dry-run text missing %q:\n%s", want, textOut.String())
		}
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{
		"dispatch", "worker", "SQU-244",
		"--target", target,
		"--source", "manager",
		"--workspace", "repo",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--kickoff", "direct kickoff",
		"--dry-run",
		"--commands",
	})
	if err := commands.Execute(); err != nil {
		t.Fatalf("dispatch --dry-run --commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team", "dispatch", "worker", "SQU-244",
		"--repo", target,
		"--source", "manager",
		"--workspace", "repo",
		"--runtime", "codex",
		"--runtime-bin", "codex-dev",
		"--kickoff", "direct kickoff",
	}), " ")
	if got := strings.TrimSpace(commandsOut.String()); got != wantCommand {
		t.Fatalf("dispatch --dry-run --commands = %q, want %q", got, wantCommand)
	}

	rootScopedCommands := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScopedCommands.SetOut(rootScopedOut)
	rootScopedCommands.SetErr(rootScopedErr)
	rootScopedCommands.SetArgs([]string{
		"--repo", target,
		"dispatch", "worker", "SQU-245", "root", "scoped",
		"--dry-run",
		"--commands",
	})
	if err := rootScopedCommands.Execute(); err != nil {
		t.Fatalf("dispatch root --repo --dry-run --commands: %v\nstderr=%s", err, rootScopedErr.String())
	}
	wantRootScopedCommand := strings.Join(shellQuoteArgs([]string{
		"agent-team", "dispatch", "worker", "SQU-245",
		"--repo", target,
		"root", "scoped",
	}), " ")
	if got := strings.TrimSpace(rootScopedOut.String()); got != wantRootScopedCommand {
		t.Fatalf("dispatch root --repo --dry-run --commands = %q, want %q", got, wantRootScopedCommand)
	}

	noRoute := NewRootCmd()
	noRouteOut, noRouteErr := &bytes.Buffer{}, &bytes.Buffer{}
	noRoute.SetOut(noRouteOut)
	noRoute.SetErr(noRouteErr)
	noRoute.SetArgs([]string{"dispatch", "reviewer", "SQU-246", "--target", target, "--dry-run", "--commands"})
	if err := noRoute.Execute(); err != nil {
		t.Fatalf("dispatch no-route --dry-run --commands: %v\nstderr=%s", err, noRouteErr.String())
	}
	if got := strings.TrimSpace(noRouteOut.String()); got != "" {
		t.Fatalf("dispatch no-route --dry-run --commands = %q, want empty", got)
	}
}

func TestDispatchCommandCommandsValidation(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"dispatch", "worker", "SQU-246", "--commands"}, "dispatch: --commands requires --dry-run"},
		{[]string{"dispatch", "worker", "SQU-246", "--dry-run", "--commands", "--json"}, "dispatch: --commands cannot be combined with --json"},
		{[]string{"dispatch", "worker", "SQU-246", "--dry-run", "--commands", "--format", "{{.Target}}"}, "dispatch: --commands cannot be combined with --format"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		var code ExitCode
		if !errors.As(err, &code) || int(code) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", tc.args, err)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%v: validation wrote stdout: %q", tc.args, out.String())
		}
	}
}

func TestDispatchCommandDuplicateSuggestsSend(t *testing.T) {
	target, mgr, cleanup := setupDispatchCommandRepo(t)
	defer cleanup()

	first := NewRootCmd()
	firstOut, firstErr := &bytes.Buffer{}, &bytes.Buffer{}
	first.SetOut(firstOut)
	first.SetErr(firstErr)
	first.SetArgs([]string{
		"dispatch", "worker", "SQU-42", "first",
		"--workspace", "repo",
		"--target", target,
	})
	if err := first.Execute(); err != nil {
		t.Fatalf("first dispatch: %v\nstderr=%s", err, firstErr.String())
	}

	second := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	second.SetOut(out)
	second.SetErr(stderr)
	second.SetArgs([]string{
		"dispatch", "worker", "SQU-42", "follow-up",
		"--workspace", "repo",
		"--target", target,
	})
	if err := second.Execute(); err != nil {
		t.Fatalf("second dispatch: %v\nstderr=%s", err, stderr.String())
	}
	got := out.String()
	for _, want := range []string{"rejected worker", "already running", "follow-up: agent-team send worker-squ-42 <message>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("duplicate output missing %q:\n%s", want, got)
		}
	}
	stopAndWaitForTest(t, mgr, "worker-squ-42")
}

func setupDispatchCommandRepo(t *testing.T) (target string, mgr *daemon.InstanceManager, cleanup func()) {
	t.Helper()
	target, err := os.MkdirTemp("/tmp", "agent-team-dispatch-")
	if err != nil {
		t.Fatal(err)
	}
	teamDir := filepath.Join(target, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "agents", "worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := "---\ndescription: test worker\n---\n\nYou are a test worker.\n"
	if err := os.WriteFile(filepath.Join(teamDir, "agents", "worker", "agent.md"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr = daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	return target, mgr, func() {
		cleanupDaemon()
		_ = os.RemoveAll(target)
	}
}
