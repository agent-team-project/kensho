package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/runtimebin"
)

func TestLifecycleHelpShowsTopLevelStartStop(t *testing.T) {
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{
		"start", "stop", "kill", "restart", "reload", "plan", "sync", "status", "health", "monitor", "watch", "inspect", "rm", "prune", "wait", "stats", "send", "dispatch", "job", "pipeline", "team", "schedule", "queue", "intake", "events", "ps", "logs", "attach",
		"Docker-like shortcuts:", "agent-team up", "agent-team down", "agent-team ls", "agent-team top", "agent-team exec",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("root help missing %q: %s", want, out.String())
		}
	}
}

func TestLifecycleAliasesResolve(t *testing.T) {
	cmd := NewRootCmd()
	cases := map[string]string{
		"up":   "start",
		"down": "stop",
		"ls":   "ps",
		"top":  "stats",
		"exec": "attach",
	}
	for alias, canonical := range cases {
		found, _, err := cmd.Find([]string{alias})
		if err != nil {
			t.Fatalf("find %s: %v", alias, err)
		}
		if found == nil || found.Name() != canonical {
			t.Fatalf("alias %s resolved to %v, want %s", alias, found, canonical)
		}
	}
}

func TestStopTopLevelNoDaemonUsesInstanceDownPath(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"stop", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected stop without daemon to fail")
	}
	if !strings.Contains(errOut.String(), "daemon is not running") {
		t.Errorf("missing daemon hint: %s", errOut.String())
	}
}

func TestStopNegativeTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--timeout must be >= 0") {
		t.Fatalf("stderr = %q, want timeout validation", stderr.String())
	}
}

func TestStopNegativeWaitTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--wait-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected wait-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--wait-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want wait-timeout validation", stderr.String())
	}
}

func TestInstanceDownNegativeWaitTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"instance", "down", "--wait-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected wait-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--wait-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want wait-timeout validation", stderr.String())
	}
}

func TestStartNegativeTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--timeout must be >= 0") {
		t.Fatalf("stderr = %q, want timeout validation", stderr.String())
	}
}

func TestStartNegativeReadyTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--ready-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected ready-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--ready-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want ready-timeout validation", stderr.String())
	}
}

func TestLifecyclePromptFileValidation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.txt")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"start", "--prompt", "hello", "--prompt-file", "task.txt"}, "provide prompt text using only one of --prompt or --prompt-file"},
		{[]string{"restart", "--prompt-file", missing}, "--prompt-file:"},
		{[]string{"instance", "up", "--prompt", "hello", "--prompt-file", "task.txt"}, "provide prompt text using only one of --prompt or --prompt-file"},
		{[]string{"team", "up", "delivery", "--prompt", "hello", "--prompt-file", "task.txt"}, "provide prompt text using only one of --prompt or --prompt-file"},
		{[]string{"team", "restart", "delivery", "--prompt-file", missing}, "--prompt-file:"},
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

func TestStartRejectsInvalidLatestLastBeforeStartingDaemon(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "negative-last", args: []string{"start", "--last", "-1"}, want: "--last must be >= 0"},
		{name: "latest-and-last", args: []string{"start", "--latest", "--last", "2"}, want: "choose one of --latest or --last"},
		{name: "last-with-name", args: []string{"start", "manager", "--last", "2"}, want: "--last cannot be combined with instance names"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			initInto(t, tmp)
			teamDir := filepath.Join(tmp, ".agent_team")

			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			args := append([]string{}, tc.args...)
			args = append(args, "--target", tmp)
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
			if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
				t.Fatalf("invalid selector should not start daemon, pidfile stat err=%v", err)
			}
		})
	}
}

func TestStartRejectsInvalidRuntimeBeforeStartingDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "runtime-with-name", args: []string{"start", "manager", "--runtime", "codex"}, want: "--runtime cannot be combined with instance names"},
		{name: "unknown-runtime", args: []string{"start", "--runtime", "llama"}, want: "unknown --runtime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			args := append([]string{}, tc.args...)
			args = append(args, "--target", tmp)
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
			if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
				t.Fatalf("invalid runtime selector should not start daemon, pidfile stat err=%v", err)
			}
		})
	}
}

func TestStartAndRestartRuntimeDryRunUseLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-stopped", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex", SessionID: "sid-codex", Status: daemon.StatusStopped, StartedAt: now.Add(-time.Minute)},
		{Instance: "claude-stopped", Agent: "manager", Runtime: "claude", Status: daemon.StatusStopped, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	for _, tc := range []struct {
		command string
		action  string
	}{
		{command: "start", action: lifecycleActionUnsupported},
		{command: "restart", action: lifecycleActionUnsupported},
	} {
		t.Run(tc.command, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs([]string{tc.command, "--runtime", "codex", "--dry-run", "--json", "--target", tmp})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s --runtime codex --dry-run: %v\nstderr=%s", tc.command, err, stderr.String())
			}
			var rows []lifecycleActionResult
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
				t.Fatalf("decode %s runtime json: %v\nbody=%s", tc.command, err, out.String())
			}
			if len(rows) != 1 || rows[0].Instance != "codex-stopped" || rows[0].Action != tc.action || !rows[0].DryRun {
				t.Fatalf("%s rows = %+v, want codex-stopped %s dry-run", tc.command, rows, tc.action)
			}
			for _, want := range []string{
				`runtime "codex" does not support managed resume`,
				`agent-team logs codex-stopped --last-message`,
				`codex resume sid-codex`,
			} {
				if !strings.Contains(rows[0].Detail, want) {
					t.Fatalf("%s detail = %q, want %q", tc.command, rows[0].Detail, want)
				}
			}
		})
	}
}

func TestStopAndKillRuntimeDryRunUseLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now().UTC()
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-running", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-time.Minute)},
		{Instance: "codex-stopped", Agent: "worker", Runtime: "codex", Status: daemon.StatusStopped, StartedAt: now},
		{Instance: "claude-running", Agent: "manager", Runtime: "claude", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	for _, tc := range []struct {
		command string
		action  string
	}{
		{command: "stop", action: "stop"},
		{command: "kill", action: "kill"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs([]string{tc.command, "--runtime", "codex", "--dry-run", "--json", "--target", tmp})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s --runtime codex --dry-run: %v\nstderr=%s", tc.command, err, stderr.String())
			}
			var rows []instanceDownResult
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
				t.Fatalf("decode %s runtime json: %v\nbody=%s", tc.command, err, out.String())
			}
			if len(rows) != 1 || rows[0].Instance != "codex-running" || rows[0].Action != tc.action || !rows[0].DryRun {
				t.Fatalf("%s rows = %+v, want codex-running %s dry-run", tc.command, rows, tc.action)
			}
		})
	}

	bad := NewRootCmd()
	bad.SetOut(&bytes.Buffer{})
	var badErr bytes.Buffer
	bad.SetErr(&badErr)
	bad.SetArgs([]string{"stop", "--runtime", "llama", "--dry-run", "--target", tmp})
	if err := bad.Execute(); err == nil {
		t.Fatal("stop accepted unknown runtime")
	}
	if !strings.Contains(badErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badErr.String())
	}
}

func TestStartUnknownStatusFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--status", "paused"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected unknown status validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --status") {
		t.Fatalf("stderr = %q, want status validation", stderr.String())
	}
}

func TestLifecycleActionsRejectUnknownPhase(t *testing.T) {
	for _, args := range [][]string{
		{"start", "--phase", "reviewing"},
		{"stop", "--phase", "reviewing"},
		{"kill", "--phase", "reviewing"},
		{"restart", "--phase", "reviewing"},
		{"instance", "up", "--phase", "reviewing"},
		{"instance", "down", "--phase", "reviewing"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected unknown phase validation error", args)
		}
		if !strings.Contains(stderr.String(), "unknown --phase") {
			t.Fatalf("%v: stderr = %q, want phase validation", args, stderr.String())
		}
	}
}

func TestLifecycleActionsPhaseRejectsExplicitNames(t *testing.T) {
	for _, args := range [][]string{
		{"start", "--phase", "idle", "manager"},
		{"stop", "--phase", "idle", "manager"},
		{"kill", "--phase", "idle", "manager"},
		{"restart", "--phase", "idle", "manager"},
		{"instance", "up", "--phase", "idle", "manager"},
		{"instance", "down", "--phase", "idle", "manager"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected phase/name validation error", args)
		}
		if !strings.Contains(stderr.String(), "--phase cannot be combined") {
			t.Fatalf("%v: stderr = %q, want phase/name validation", args, stderr.String())
		}
	}
}

func TestLifecycleActionsStaleRejectsExplicitNames(t *testing.T) {
	for _, args := range [][]string{
		{"start", "--stale", "manager"},
		{"stop", "--stale", "manager"},
		{"kill", "--stale", "manager"},
		{"restart", "--stale", "manager"},
		{"instance", "up", "--stale", "manager"},
		{"instance", "down", "--stale", "manager"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected stale/name validation error", args)
		}
		if !strings.Contains(stderr.String(), "--stale cannot be combined") {
			t.Fatalf("%v: stderr = %q, want stale/name validation", args, stderr.String())
		}
	}
	for _, args := range [][]string{
		{"start", "--unhealthy", "manager"},
		{"stop", "--unhealthy", "manager"},
		{"kill", "--unhealthy", "manager"},
		{"restart", "--unhealthy", "manager"},
		{"instance", "up", "--unhealthy", "manager"},
		{"instance", "down", "--unhealthy", "manager"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected unhealthy/name validation error", args)
		}
		if !strings.Contains(stderr.String(), "--unhealthy cannot be combined") {
			t.Fatalf("%v: stderr = %q, want unhealthy/name validation", args, stderr.String())
		}
	}
	for _, args := range [][]string{
		{"start", "--runtime-stale", "manager"},
		{"stop", "--runtime-stale", "manager"},
		{"kill", "--runtime-stale", "manager"},
		{"restart", "--runtime-stale", "manager"},
		{"instance", "up", "--runtime-stale", "manager"},
		{"instance", "down", "--runtime-stale", "manager"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected runtime-stale/name validation error", args)
		}
		if !strings.Contains(stderr.String(), "--runtime-stale cannot be combined") {
			t.Fatalf("%v: stderr = %q, want runtime-stale/name validation", args, stderr.String())
		}
	}
}

func TestStartDryRunJSONDoesNotStartDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --dry-run --json: %v\nstderr: %s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want manager and ticket-manager", rows)
	}
	got := map[string]lifecycleActionResult{}
	for _, row := range rows {
		got[row.Instance] = row
	}
	for _, name := range []string{"manager", "ticket-manager"} {
		row, ok := got[name]
		if !ok {
			t.Fatalf("dry-run rows missing %s: %+v", name, rows)
		}
		if row.Action != "start" || row.Status != "unknown" || !row.DryRun || row.Detail != "would start" {
			t.Fatalf("%s row = %+v, want dry-run start preview", name, row)
		}
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
	if _, err := os.Stat(daemon.SocketPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon socket, stat err=%v", err)
	}
}

func TestStartDryRunCommandsPrintsApplyCommand(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--target", tmp, "--dry-run", "--agent", "manager", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --dry-run --commands: %v\nstderr: %s", err, stderr.String())
	}
	want := "agent-team start --target " + tmp + " --agent manager"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("start --dry-run --commands = %q, want %q", got, want)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("commands dry-run should not create daemon pidfile, stat err=%v", err)
	}

	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Runtime:  string(runtimebin.KindClaude),
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	noAction := NewRootCmd()
	noActionOut, noActionErr := &bytes.Buffer{}, &bytes.Buffer{}
	noAction.SetOut(noActionOut)
	noAction.SetErr(noActionErr)
	noAction.SetArgs([]string{"start", "--target", tmp, "--dry-run", "--agent", "manager", "--commands"})
	if err := noAction.Execute(); err != nil {
		t.Fatalf("start --dry-run --commands no actionable rows: %v\nstderr: %s", err, noActionErr.String())
	}
	if got := strings.TrimSpace(noActionOut.String()); got != "" {
		t.Fatalf("start --dry-run --commands with no actionable rows = %q, want empty", got)
	}
}

func TestStartDryRunSummaryJSONCountsActions(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--dry-run", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --dry-run --summary --json: %v\nstderr: %s", err, stderr.String())
	}

	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 2 || body.Summary.Actions["start"] != 2 || body.Summary.Statuses["unknown"] != 2 || !body.Summary.DryRun {
		t.Fatalf("summary = %+v, want two dry-run starts with unknown status", body.Summary)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestStartDryRunUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
		PID:      123,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "ticket-manager",
		Agent:    "ticket-manager",
		Status:   daemon.StatusRunning,
	}); err != nil {
		t.Fatalf("write ticket-manager metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --dry-run --json: %v\nstderr: %s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	got := map[string]lifecycleActionResult{}
	for _, row := range rows {
		got[row.Instance] = row
	}
	manager := got["manager"]
	if manager.Action != "resume" || manager.Status != "stopped" || manager.PID != 123 || manager.Detail != "would resume" || !manager.DryRun {
		t.Fatalf("manager row = %+v, want local stopped resume preview", manager)
	}
	ticketManager := got["ticket-manager"]
	if ticketManager.Action != "resume" || ticketManager.Status != "running" || ticketManager.Detail != "would resume; recorded running pid is not live" || !ticketManager.DryRun {
		t.Fatalf("ticket-manager row = %+v, want stale running resume preview", ticketManager)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
	if _, err := os.Stat(daemon.SocketPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon socket, stat err=%v", err)
	}
}

func TestStartDryRunPhaseFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "blocked"
description = "needs input"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "ticket-manager"), `[status]
phase = "idle"
description = "waiting"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--phase", "blocked", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --phase --dry-run --json: %v\nstderr: %s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Action != "resume" {
		t.Fatalf("rows = %+v, want blocked manager resume preview", rows)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestStartFilterOnlyDryRunUsesMetadataWithoutTopology(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.Remove(filepath.Join(teamDir, "instances.toml")); err != nil {
		t.Fatalf("remove instances.toml: %v", err)
	}
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "stopped", Agent: "worker", Status: daemon.StatusStopped},
		{Instance: "running", Agent: "worker", Status: daemon.StatusRunning},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--status", "stopped", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --status stopped --dry-run without topology: %v\nstderr=%s", err, stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "stopped" || rows[0].Action != "resume" {
		t.Fatalf("rows = %+v, want stopped metadata resume preview", rows)
	}
}

func TestStartLatestDryRunSelectsNewestStoppedMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "running-newer", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--latest", "--status", "stopped", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --latest dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "new" || rows[0].Action != "resume" {
		t.Fatalf("rows = %+v, want newest stopped metadata resume", rows)
	}
}

func TestStartLastDryRunSelectsNewestStoppedMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Hour)},
		{Instance: "running-newer", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--last", "2", "--status", "stopped", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --last dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two stopped metadata resumes", rows)
	}
}

func TestStartDryRunFormatDoesNotStartDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--dry-run", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}:{{.DryRun}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --dry-run --format: %v\nstderr: %s", err, stderr.String())
	}
	rows := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line != "" {
			rows[line] = true
		}
	}
	for _, want := range []string{
		"manager:start:unknown:true",
		"ticket-manager:start:unknown:true",
	} {
		if !rows[want] {
			t.Fatalf("formatted dry-run rows missing %q: %q", want, out.String())
		}
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestStartQuietDryRunSuppressesOutput(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--dry-run", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --dry-run --quiet: %v\nstderr: %s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet start should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestStartQuietRejectsJSONAndAttach(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"start", "--quiet", "--json"}, "choose one of --quiet or --json"},
		{[]string{"start", "--quiet", "--attach", "manager"}, "--quiet cannot be combined with --attach"},
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

func TestStartFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"start", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined"},
		{[]string{"start", "--format", "{{.Instance}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"start", "--format", "{{.Instance}}", "--attach", "manager"}, "--format cannot be combined"},
		{[]string{"start", "--format", "{{"}, "invalid --format template"},
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

func TestLifecycleCommandsRejectsInvalidRenderModes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "start no dry-run",
			args: []string{"start", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "start json",
			args: []string{"start", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "stop summary",
			args: []string{"stop", "--dry-run", "--commands", "--summary"},
			want: "--commands cannot be combined with --summary",
		},
		{
			name: "kill quiet",
			args: []string{"kill", "--dry-run", "--commands", "--quiet"},
			want: "--commands cannot be combined with --quiet",
		},
		{
			name: "restart format",
			args: []string{"restart", "--dry-run", "--commands", "--format", "{{.Instance}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "restart attach",
			args: []string{"restart", "manager", "--dry-run", "--commands", "--attach"},
			want: "--commands cannot be combined with --attach",
		},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%s succeeded\nstdout=%s\nstderr=%s", tc.name, out.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%s stderr = %q, want %q", tc.name, stderr.String(), tc.want)
		}
	}
}

func TestRestartStaleDryRunTargetsOnlyStaleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: old},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "implementing"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--stale", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --stale --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stale restart json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Action != "restart" || !rows[0].DryRun {
		t.Fatalf("rows = %+v, want stale manager restart dry-run only", rows)
	}
}

func TestStartUnhealthyDryRunTargetsCrashedAndStaleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, PID: 101, StartedAt: old},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusStopped, PID: 202, StartedAt: now},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusCrashed, PID: 303, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "ticket-manager"), `[status]
phase = "idle"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--unhealthy", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --unhealthy --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode unhealthy start json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want stale manager and crashed worker", rows)
	}
	got := map[string]lifecycleActionResult{}
	for _, row := range rows {
		got[row.Instance] = row
	}
	if got["manager"].Action != "resume" || got["manager"].Status != string(daemon.StatusStopped) || !got["manager"].DryRun {
		t.Fatalf("manager row = %+v, want stale manager resume dry-run", got["manager"])
	}
	if got["worker"].Action != "resume" || got["worker"].Status != string(daemon.StatusCrashed) || !got["worker"].DryRun {
		t.Fatalf("worker row = %+v, want crashed worker resume dry-run", got["worker"])
	}
}

func TestStartUnhealthyDryRunTargetsRuntimeStaleInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--unhealthy", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --unhealthy --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode unhealthy start json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || rows[0].Action != lifecycleActionUnsupported || rows[0].Status != string(daemon.StatusRunning) || !rows[0].DryRun {
		t.Fatalf("rows = %+v, want runtime-stale unsupported dry-run only", rows)
	}
	if !strings.Contains(rows[0].Detail, "recorded running pid is not live") {
		t.Fatalf("detail = %q, want stale runtime detail", rows[0].Detail)
	}
}

func TestStartRuntimeStaleDryRunTargetsOnlyRuntimeStaleInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, PID: 0, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--runtime-stale", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --runtime-stale --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode runtime-stale start json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || rows[0].Action != lifecycleActionUnsupported || rows[0].Status != string(daemon.StatusRunning) || !rows[0].DryRun {
		t.Fatalf("rows = %+v, want runtime-stale unsupported dry-run only", rows)
	}
}

func TestRestartUnhealthyDryRunTargetsCrashedAndStaleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: old},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusCrashed, PID: 303, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "ticket-manager"), `[status]
phase = "implementing"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--unhealthy", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --unhealthy --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode unhealthy restart json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want stale manager and crashed worker", rows)
	}
	got := map[string]lifecycleActionResult{}
	for _, row := range rows {
		got[row.Instance] = row
	}
	if got["manager"].Action != "restart" || got["manager"].Status != string(daemon.StatusRunning) || !got["manager"].DryRun {
		t.Fatalf("manager row = %+v, want stale manager restart dry-run", got["manager"])
	}
	if got["worker"].Action != "restart" || got["worker"].Status != string(daemon.StatusCrashed) || !got["worker"].DryRun {
		t.Fatalf("worker row = %+v, want crashed worker restart dry-run", got["worker"])
	}
}

func TestKillStaleDryRunTargetsOnlyStaleRunningInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: old},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "implementing"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--stale", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kill --stale --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stale kill json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Action != "kill" || rows[0].Status != string(daemon.StatusRunning) || !rows[0].DryRun {
		t.Fatalf("rows = %+v, want stale manager kill dry-run only", rows)
	}
}

func TestKillUnhealthyDryRunTargetsCrashedAndStaleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: old},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusCrashed, PID: 303, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "ticket-manager"), `[status]
phase = "implementing"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--unhealthy", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kill --unhealthy --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode unhealthy kill json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want stale manager and crashed worker", rows)
	}
	got := map[string]instanceDownResult{}
	for _, row := range rows {
		got[row.Instance] = row
	}
	if got["manager"].Action != "kill" || got["manager"].Status != string(daemon.StatusRunning) || !got["manager"].DryRun {
		t.Fatalf("manager row = %+v, want stale manager kill dry-run", got["manager"])
	}
	if got["worker"].Action != "skip" || got["worker"].Status != "skipped" || !got["worker"].DryRun {
		t.Fatalf("worker row = %+v, want crashed worker selected and skipped as not running", got["worker"])
	}
}

func TestStopRuntimeStaleDryRunTargetsOnlyRuntimeStaleInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, PID: 0, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--runtime-stale", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --runtime-stale --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode runtime-stale stop json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || rows[0].Action != "stop" || rows[0].Status != string(daemon.StatusRunning) || !rows[0].DryRun {
		t.Fatalf("rows = %+v, want runtime-stale stop dry-run only", rows)
	}
}

func TestKillUnhealthyDryRunTargetsRuntimeStaleInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--unhealthy", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kill --unhealthy --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode unhealthy runtime-stale kill json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || rows[0].Action != "kill" || rows[0].Status != string(daemon.StatusRunning) || !rows[0].DryRun {
		t.Fatalf("rows = %+v, want runtime-stale kill dry-run only", rows)
	}
}

func TestStartWaitFormatPrintsRowsAfterHealthWait(t *testing.T) {
	tmp, _, cleanup := startDeclaredLifecycleInstancesForTest(t)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"start",
		"--wait",
		"--timeout", "2s",
		"--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --wait --format: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	rows := lifecycleFormattedRows(out.String())
	for _, want := range []string{
		"manager:skip:running",
		"ticket-manager:skip:running",
	} {
		if !rows[want] {
			t.Fatalf("formatted start wait rows missing %q: %q", want, out.String())
		}
	}
}

func TestStartWaitJSONHonorsAgentFilterHealth(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-start-filter-wait-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	defer cleanupDaemon()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"start",
		"--agent", "manager",
		"--wait",
		"--timeout", "50ms",
		"--json",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --agent manager --wait --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var body lifecycleHealthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode start filtered wait json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || !body.Health.Healthy {
		t.Fatalf("filtered start wait health = %+v, want healthy manager-only health", body.Health)
	}
	if len(body.Actions) != 1 || body.Actions[0].Instance != "manager" || body.Actions[0].Action != "skip" {
		t.Fatalf("filtered start wait actions = %+v, want manager skip only", body.Actions)
	}
	if body.Health.Declared.Persistent != 1 || body.Health.Declared.Running != 1 || body.Health.Declared.Missing != 0 {
		t.Fatalf("filtered start wait declared health = %+v, want only manager declared", body.Health.Declared)
	}
}

func TestStartWaitTimeoutReportsUnhealthyFleet(t *testing.T) {
	tmp, _, cleanup := startDeclaredLifecycleInstancesForTest(t)
	defer cleanup()
	teamDir := filepath.Join(tmp, ".agent_team")
	old := time.Now().Add(-staleAfter - time.Minute)
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"start",
		"--wait",
		"--timeout", "5ms",
		"--json",
		"--target", tmp,
	})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var body lifecycleHealthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode start wait timeout json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || body.Health.Healthy {
		t.Fatalf("start wait health = %+v, want unhealthy timeout snapshot", body.Health)
	}
	if !strings.Contains(stderr.String(), "wait timed out before selected instances became healthy") {
		t.Fatalf("stderr = %q, want lifecycle wait timeout message", stderr.String())
	}
}

func startDeclaredLifecycleInstancesForTest(t *testing.T) (string, *daemon.InstanceManager, func()) {
	t.Helper()
	tmp, err := os.MkdirTemp("/tmp", "agent-team-lifecycle-format-")
	if err != nil {
		t.Fatal(err)
	}
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	for _, spec := range []struct {
		name  string
		agent string
	}{
		{name: "manager", agent: "manager"},
		{name: "ticket-manager", agent: "ticket-manager"},
	} {
		if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: spec.agent, Name: spec.name, Workspace: tmp}); err != nil {
			cleanupDaemon()
			_ = os.RemoveAll(tmp)
			t.Fatalf("dispatch %s: %v", spec.name, err)
		}
	}
	cleanup := func() {
		for _, meta := range mgr.List() {
			if meta.Status == daemon.StatusRunning {
				stopAndWaitForTest(t, mgr, meta.Instance)
			}
		}
		cleanupDaemon()
		_ = os.RemoveAll(tmp)
	}
	return tmp, mgr, cleanup
}

func lifecycleFormattedRows(body string) map[string]bool {
	rows := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			rows[line] = true
		}
	}
	return rows
}

func TestStartDryRunRejectsWait(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--dry-run", "--wait"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected dry-run wait validation error")
	}
	if !strings.Contains(stderr.String(), "--dry-run cannot be combined with --wait") {
		t.Fatalf("stderr = %q, want dry-run wait validation", stderr.String())
	}
}

func TestStartAttachRejectsJSONBeforeStartingDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "manager", "--attach", "--json", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected attach/json validation error")
	}
	if !strings.Contains(stderr.String(), "--attach cannot be combined with --json") {
		t.Fatalf("stderr = %q, want attach/json validation", stderr.String())
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("invalid attach/json should not start daemon, pidfile stat err=%v", err)
	}
}

func TestStartTailRequiresAttach(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "manager", "--tail", "all"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected tail without attach validation error")
	}
	if !strings.Contains(stderr.String(), "--tail requires --attach") {
		t.Fatalf("stderr = %q, want tail/attach validation", stderr.String())
	}
}

func TestStartAttachRequiresExactlyOneSelectedInstance(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-start-attach-many-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "--attach", "--target", tmp})
	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected multi-target attach validation error")
	}
	if !strings.Contains(stderr.String(), "--attach requires exactly one selected instance") {
		t.Fatalf("stderr = %q, want exactly-one validation", stderr.String())
	}
}

func TestStartAttachStreamsSelectedInstanceLog(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-start-attach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, nil)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "first\nlast\n")
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"start", "manager", "--attach", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("start --attach: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	body := out.String()
	for _, want := range []string{"skip", "attaching to manager", "first\nlast\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("start --attach output missing %q:\n%s", want, body)
		}
	}
}

func TestRestartAttachRejectsJSONBeforeStartingDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "manager", "--attach", "--json", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected attach/json validation error")
	}
	if !strings.Contains(stderr.String(), "--attach cannot be combined with --json") {
		t.Fatalf("stderr = %q, want attach/json validation", stderr.String())
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("invalid attach/json should not start daemon, pidfile stat err=%v", err)
	}
}

func TestRestartTailRequiresAttach(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "manager", "--tail", "all"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected tail without attach validation error")
	}
	if !strings.Contains(stderr.String(), "--tail requires --attach") {
		t.Fatalf("stderr = %q, want tail/attach validation", stderr.String())
	}
}

func TestRestartAttachRequiresExactlyOneSelectedInstance(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-restart-attach-many-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), nil)
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--attach", "--target", tmp})
	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected multi-target attach validation error")
	}
	if !strings.Contains(stderr.String(), "--attach requires exactly one selected instance") {
		t.Fatalf("stderr = %q, want exactly-one validation", stderr.String())
	}
}

func TestRestartAttachStreamsSelectedInstanceLog(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-restart-attach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	defer func() {
		for _, meta := range mgr.List() {
			if meta.Instance == "manager" && meta.Status == daemon.StatusRunning {
				stopAndWaitForTest(t, mgr, "manager")
				return
			}
		}
	}()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "manager", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch manager: %v", err)
	}
	writeChildLogForTest(t, root, "manager", "before restart\n")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "manager", "--attach", "--tail", "all", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --attach: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	body := out.String()
	for _, want := range []string{"restart", "attaching to manager", "before restart\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("restart --attach output missing %q:\n%s", want, body)
		}
	}
}

func TestKillNegativeTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--timeout must be >= 0") {
		t.Fatalf("stderr = %q, want timeout validation", stderr.String())
	}
}

func TestKillNegativeWaitTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--wait-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected wait-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--wait-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want wait-timeout validation", stderr.String())
	}
}

func TestKillTopLevelRequiresDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected kill without daemon to fail")
	}
	if !strings.Contains(stderr.String(), "daemon is not running") {
		t.Errorf("missing daemon hint: %s", stderr.String())
	}
}

func TestStopAgentRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--agent", "manager", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --agent plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--agent cannot be combined") {
		t.Fatalf("stderr = %q, want --agent validation", stderr.String())
	}
}

func TestStopStatusRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--status", "running", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --status plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--status cannot be combined") {
		t.Fatalf("stderr = %q, want --status validation", stderr.String())
	}
}

func TestStopRejectsUnknownStatus(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--status", "paused"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected unknown status validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --status") {
		t.Fatalf("stderr = %q, want status validation", stderr.String())
	}
}

func TestKillAgentRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--agent", "manager", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --agent plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--agent cannot be combined") {
		t.Fatalf("stderr = %q, want --agent validation", stderr.String())
	}
}

func TestKillStatusRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--status", "running", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --status plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--status cannot be combined") {
		t.Fatalf("stderr = %q, want --status validation", stderr.String())
	}
}

func TestKillRejectsUnknownStatus(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--status", "paused"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected unknown status validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --status") {
		t.Fatalf("stderr = %q, want status validation", stderr.String())
	}
}

func TestStopDryRunRejectsWait(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--dry-run", "--wait"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected dry-run wait validation error")
	}
	if !strings.Contains(stderr.String(), "--dry-run cannot be combined with --wait") {
		t.Fatalf("stderr = %q, want dry-run wait validation", stderr.String())
	}
}

func TestKillDryRunRejectsWait(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--dry-run", "--wait"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected dry-run wait validation error")
	}
	if !strings.Contains(stderr.String(), "--dry-run cannot be combined with --wait") {
		t.Fatalf("stderr = %q, want dry-run wait validation", stderr.String())
	}
}

func TestStopDryRunUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
		PID:      123,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "manager", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop manager --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stop dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one manager row", rows)
	}
	row := rows[0]
	if row.Action != "skip" || row.Instance != "manager" || row.Status != "skipped" || row.Detail != "not running" || !row.DryRun {
		t.Fatalf("row = %+v, want stopped local metadata skip preview", row)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestStopKillRestartDryRunCommandsPrintApplyCommands(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Runtime:   string(runtimebin.KindClaude),
		Status:    daemon.StatusRunning,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "stop named remove timeout",
			args: []string{"stop", "manager", "--target", tmp, "--dry-run", "--rm", "--timeout", "10s", "--commands"},
			want: "agent-team stop --target " + tmp + " manager --rm --timeout 10s",
		},
		{
			name: "kill filtered",
			args: []string{"kill", "--target", tmp, "--dry-run", "--all", "--runtime", "claude", "--commands"},
			want: "agent-team kill --target " + tmp + " --all --runtime claude",
		},
		{
			name: "restart filtered force timeout",
			args: []string{"restart", "--target", tmp, "--dry-run", "--agent", "manager", "--force", "--timeout", "5s", "--commands"},
			want: "agent-team restart --target " + tmp + " --agent manager --force --timeout 5s",
		},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: %v\nstderr: %s", tc.name, err, stderr.String())
		}
		if got := strings.TrimSpace(out.String()); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}

	noAction := NewRootCmd()
	noActionOut, noActionErr := &bytes.Buffer{}, &bytes.Buffer{}
	noAction.SetOut(noActionOut)
	noAction.SetErr(noActionErr)
	noAction.SetArgs([]string{"kill", "ticket-manager", "--target", tmp, "--dry-run", "--commands"})
	if err := noAction.Execute(); err != nil {
		t.Fatalf("kill --dry-run --commands no actionable rows: %v\nstderr: %s", err, noActionErr.String())
	}
	if got := strings.TrimSpace(noActionOut.String()); got != "" {
		t.Fatalf("kill --dry-run --commands with no actionable rows = %q, want empty", got)
	}
}

func TestStopDryRunSummaryJSONCountsStopsAndSkips(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid()},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusStopped},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "manager", "ticket-manager", "--dry-run", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop explicit --dry-run --summary --json: %v\nstderr=%s", err, stderr.String())
	}

	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode stop summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 2 || body.Summary.Actions["stop"] != 1 || body.Summary.Actions["skip"] != 1 || !body.Summary.DryRun {
		t.Fatalf("summary = %+v, want one stop and one skip", body.Summary)
	}
	if body.Summary.Statuses["running"] != 1 || body.Summary.Statuses["skipped"] != 1 {
		t.Fatalf("statuses = %+v, want running=1 skipped=1", body.Summary.Statuses)
	}
}

func TestStopDryRunPhaseFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid()},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusRunning, PID: os.Getpid()},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "blocked"
description = "needs input"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "ticket-manager"), `[status]
phase = "idle"
description = "waiting"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--phase", "blocked", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --phase --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stop dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Action != "stop" || rows[0].Status != "running" {
		t.Fatalf("rows = %+v, want blocked manager stop preview", rows)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestKillDryRunUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--all", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kill --all --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}

	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode kill dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one manager row", rows)
	}
	row := rows[0]
	if row.Action != "kill" || row.Instance != "manager" || row.Status != "running" || row.Detail != "would kill" || !row.DryRun {
		t.Fatalf("row = %+v, want live local metadata kill preview", row)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestStopLatestDryRunSelectsNewestMatchingMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "stopped-newer", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--latest", "--status", "running", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --latest dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "new" {
		t.Fatalf("rows = %+v, want newest running metadata target", rows)
	}
}

func TestStopLastDryRunSelectsNewestMatchingMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Hour)},
		{Instance: "stopped-newer", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "--last", "2", "--status", "running", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --last dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two running metadata targets", rows)
	}
}

func TestKillLatestDryRunSelectsNewestMatchingMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--latest", "--status", "running", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kill --latest dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "new" {
		t.Fatalf("rows = %+v, want newest running metadata target", rows)
	}
}

func TestKillLastDryRunSelectsNewestMatchingMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "--last", "2", "--status", "running", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kill --last dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two running metadata targets", rows)
	}
}

func TestStopRmRemovesStateAndDaemonMetadata(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-stop-rm-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "adhoc", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch adhoc: %v", err)
	}
	stateDir := filepath.Join(teamDir, "state", "adhoc")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "adhoc", "--rm", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --rm --json: %v\nstderr: %s", err, stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stop --rm json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	row := rows[0]
	if row.Action != "stop" || row.Status != "stopped" || !row.Removed || !row.StateRemoved || !row.DaemonRemoved {
		t.Fatalf("stop --rm row = %+v, want stopped and removed", row)
	}
	if row.Path != ".agent_team/state/adhoc" {
		t.Fatalf("path = %q, want .agent_team/state/adhoc", row.Path)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir should be removed, stat err=%v", err)
	}
	for _, meta := range mgr.List() {
		if meta.Instance == "adhoc" {
			t.Fatalf("daemon metadata still includes adhoc: %+v", meta)
		}
	}
}

func TestStopDryRunFormatPrintsActionRows(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-stop-format-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "adhoc", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch adhoc: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "adhoc", "--dry-run", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}:{{.DryRun}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --dry-run --format: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := out.String(), "adhoc:stop:running:true\n"; got != want {
		t.Fatalf("formatted stop dry-run = %q, want %q", got, want)
	}
}

func TestKillWaitJSONReportsTerminalStatus(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-kill-wait-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "adhoc", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch adhoc: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"kill", "adhoc", "--wait", "--timeout", "2s", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kill --wait --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var rows []instanceDownResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode kill wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one adhoc row", rows)
	}
	row := rows[0]
	if row.Action != "kill" || row.Instance != "adhoc" || row.Status != "stopped" || row.WaitStatus != "stopped" {
		t.Fatalf("kill wait row = %+v, want kill stopped with wait_status=stopped", row)
	}
}

func TestDownWaitTimeoutFallbackAndOverride(t *testing.T) {
	if got := downWaitTimeout(instanceDownOptions{Timeout: 2 * time.Second}); got != 2*time.Second {
		t.Fatalf("fallback wait timeout = %s, want 2s", got)
	}
	if got := downWaitTimeout(instanceDownOptions{
		Timeout:        2 * time.Second,
		WaitTimeout:    5 * time.Second,
		WaitTimeoutSet: true,
	}); got != 5*time.Second {
		t.Fatalf("explicit wait timeout = %s, want 5s", got)
	}
	if got := downWaitTimeout(instanceDownOptions{
		Timeout:        2 * time.Second,
		WaitTimeout:    0,
		WaitTimeoutSet: true,
	}); got != 0 {
		t.Fatalf("explicit zero wait timeout = %s, want 0", got)
	}
}

func TestStopQuietSuppressesRows(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-stop-quiet-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "adhoc", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch adhoc: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stop", "adhoc", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stop --quiet: %v\nstderr: %s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet stop should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestStopAndKillFormatRejectConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"stop", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined"},
		{[]string{"stop", "--format", "{{.Instance}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"stop", "--format", "{{"}, "invalid --format template"},
		{[]string{"kill", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined"},
		{[]string{"kill", "--format", "{{.Instance}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"kill", "--format", "{{"}, "invalid --format template"},
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

func TestStopAndKillQuietRejectJSON(t *testing.T) {
	for _, args := range [][]string{
		{"stop", "--quiet", "--json"},
		{"kill", "--quiet", "--json"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", args)
		}
		if !strings.Contains(stderr.String(), "choose one of --quiet or --json") {
			t.Fatalf("%v: stderr = %q, want quiet/json validation", args, stderr.String())
		}
	}
}

func TestRestartAllRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--all", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --all plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--all cannot be combined") {
		t.Fatalf("stderr = %q, want --all validation", stderr.String())
	}
}

func TestRestartAgentRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--agent", "manager", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --agent plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--agent cannot be combined") {
		t.Fatalf("stderr = %q, want --agent validation", stderr.String())
	}
}

func TestRestartStatusRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--status", "running", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --status plus name validation error")
	}
	if !strings.Contains(stderr.String(), "--status cannot be combined") {
		t.Fatalf("stderr = %q, want --status validation", stderr.String())
	}
}

func TestRestartUnknownStatusFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--status", "paused"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected unknown status validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --status") {
		t.Fatalf("stderr = %q, want status validation", stderr.String())
	}
}

func TestRestartNegativeTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--timeout must be >= 0") {
		t.Fatalf("stderr = %q, want timeout validation", stderr.String())
	}
}

func TestRestartNegativeReadyTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--ready-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected ready-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--ready-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want ready-timeout validation", stderr.String())
	}
}

func TestRestartNegativeWaitTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--wait-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected wait-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--wait-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want wait-timeout validation", stderr.String())
	}
}

func TestRestartRejectsInvalidLatestLastBeforeStartingDaemon(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "negative-last", args: []string{"restart", "--last", "-1"}, want: "--last must be >= 0"},
		{name: "latest-and-last", args: []string{"restart", "--latest", "--last", "2"}, want: "choose one of --latest or --last"},
		{name: "last-with-name", args: []string{"restart", "manager", "--last", "2"}, want: "--last cannot be combined with instance names"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			initInto(t, tmp)
			teamDir := filepath.Join(tmp, ".agent_team")

			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			args := append([]string{}, tc.args...)
			args = append(args, "--target", tmp)
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
			if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
				t.Fatalf("invalid selector should not start daemon, pidfile stat err=%v", err)
			}
		})
	}
}

func TestRestartDryRunRejectsWait(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--dry-run", "--wait"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected dry-run wait validation error")
	}
	if !strings.Contains(stderr.String(), "--dry-run cannot be combined with --wait") {
		t.Fatalf("stderr = %q, want dry-run wait validation", stderr.String())
	}
}

func TestRestartQuietDryRunSuppressesOutput(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--dry-run", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --dry-run --quiet: %v\nstderr: %s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet restart should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestRestartDryRunFormatPrintsActionRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--dry-run", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}:{{.DryRun}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --dry-run --format: %v\nstderr: %s", err, stderr.String())
	}
	rows := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line != "" {
			rows[line] = true
		}
	}
	for _, want := range []string{
		"manager:start:unknown:true",
		"ticket-manager:start:unknown:true",
	} {
		if !rows[want] {
			t.Fatalf("formatted restart dry-run rows missing %q: %q", want, out.String())
		}
	}
}

func TestRestartForceDryRunAccepted(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--force", "--timeout", "1s", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --force --dry-run --json: %v\nstderr: %s", err, stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode restart force dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) == 0 {
		t.Fatalf("rows = %+v, want dry-run restart targets", rows)
	}
}

func TestRestartDryRunSummaryJSONCountsStartsAndRestarts(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
		PID:      321,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--dry-run", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --dry-run --summary --json: %v\nstderr: %s", err, stderr.String())
	}

	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode restart summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 2 || body.Summary.Actions["restart"] != 1 || body.Summary.Actions["start"] != 1 || !body.Summary.DryRun {
		t.Fatalf("summary = %+v, want one restart and one start", body.Summary)
	}
}

func TestRestartDryRunUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
		PID:      321,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "manager", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart manager --dry-run --json: %v\nstderr: %s", err, stderr.String())
	}

	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode restart dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one manager row", rows)
	}
	row := rows[0]
	if row.Action != "restart" || row.Status != "stopped" || row.PID != 321 || row.Detail != "would restart" || !row.DryRun {
		t.Fatalf("row = %+v, want local stopped restart preview", row)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestRestartReportsUnsupportedCodexResumeWithoutStoppingInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	repo := tmp
	if eval, err := filepath.EvalSymlinks(tmp); err == nil {
		repo = eval
	}
	teamDir := filepath.Join(repo, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, fakeSpawnerForTest(t, 2*time.Second))
	meta, err := mgr.Dispatch(daemon.DispatchInput{
		Agent:         "manager",
		Name:          "manager",
		Workspace:     repo,
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: runtimebin.DefaultBinaryForKind(runtimebin.KindCodex),
		Prompt:        "hello from codex",
	})
	if err != nil {
		t.Fatalf("dispatch manager: %v", err)
	}
	defer stopAndWaitForTest(t, mgr, "manager")
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()
	if _, err := newDaemonClient(teamDir); err != nil {
		t.Fatalf("test daemon client: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	formatTemplate, err := parseLifecycleActionFormat("{{.Instance}}:{{.Action}}:{{.Status}}:{{.PID}}:{{.Detail}}")
	if err != nil {
		t.Fatalf("parse format: %v", err)
	}
	if err := runInstanceRestart(cmd, repo, "", []string{"manager"}, instanceRestartOptions{Format: formatTemplate}); err != nil {
		t.Fatalf("restart manager --format: %v\nstderr: %s", err, stderr.String())
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasPrefix(got, "manager:unsupported:running:") || !strings.Contains(got, `:runtime "codex" does not support managed resume`) {
		t.Fatalf("restart output = %q, want unsupported running Codex row", got)
	}
	for _, want := range []string{
		`runtime "codex" does not support managed resume`,
		`agent-team resume-plan manager`,
		`agent-team logs manager --follow`,
		`agent-team logs manager --last-message`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("restart output = %q, want %q", got, want)
		}
	}
	after := metadataByInstanceForTest(mgr.List(), "manager")
	if after == nil || after.Status != daemon.StatusRunning || after.PID != meta.PID {
		t.Fatalf("manager metadata = %+v, want running metadata unchanged", after)
	}
}

func TestRestartFilterOnlyDryRunUsesMetadataWithoutTopology(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.Remove(filepath.Join(teamDir, "instances.toml")); err != nil {
		t.Fatalf("remove instances.toml: %v", err)
	}
	root := daemon.DaemonRoot(teamDir)
	for _, item := range []struct {
		name  string
		phase string
	}{
		{name: "idle", phase: "idle"},
		{name: "blocked", phase: "blocked"},
	} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: item.name,
			Agent:    "worker",
			Status:   daemon.StatusStopped,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", item.name, err)
		}
		writeStatus(t, filepath.Join(teamDir, "state", item.name), "[status]\nphase = \""+item.phase+"\"\ndescription = \"fixture\"\n", time.Time{})
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--phase", "idle", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --phase idle --dry-run without topology: %v\nstderr=%s", err, stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode restart dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "idle" || rows[0].Action != "restart" {
		t.Fatalf("rows = %+v, want idle metadata restart preview", rows)
	}
}

func TestRestartLatestDryRunSelectsNewestStoppedMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--latest", "--status", "stopped", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --latest dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "new" || rows[0].Action != "restart" {
		t.Fatalf("rows = %+v, want newest stopped metadata restart", rows)
	}
}

func TestRestartLastDryRunSelectsNewestStoppedMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"restart", "--last", "2", "--status", "stopped", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --last dry-run: %v\nstderr=%s", err, stderr.String())
	}
	var rows []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two stopped metadata restarts", rows)
	}
}

func TestRestartQuietRejectsJSONAndAttach(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"restart", "--quiet", "--json"}, "choose one of --quiet or --json"},
		{[]string{"restart", "--quiet", "--attach", "manager"}, "--quiet cannot be combined with --attach"},
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

func TestRestartFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"restart", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined"},
		{[]string{"restart", "--format", "{{.Instance}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"restart", "--format", "{{.Instance}}", "--attach", "manager"}, "--format cannot be combined"},
		{[]string{"restart", "--format", "{{"}, "invalid --format template"},
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

func TestRestartWaitFormatPrintsRowsAfterHealthWait(t *testing.T) {
	tmp, _, cleanup := startDeclaredLifecycleInstancesForTest(t)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"restart",
		"--wait",
		"--timeout", "2s",
		"--wait-timeout", "2s",
		"--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --wait --format: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	rows := lifecycleFormattedRows(out.String())
	for _, want := range []string{
		"manager:restart:running",
		"ticket-manager:restart:running",
	} {
		if !rows[want] {
			t.Fatalf("formatted restart wait rows missing %q: %q", want, out.String())
		}
	}
}

func TestRestartWaitJSONHonorsAgentFilterHealth(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-restart-filter-wait-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	mgr := daemon.NewInstanceManager(daemon.DaemonRoot(teamDir), fakeSpawnerForTest(t, 2*time.Second))
	cleanupDaemon := startRunTestDaemon(t, teamDir, mgr)
	defer cleanupDaemon()
	if _, err := mgr.Dispatch(daemon.DispatchInput{Agent: "manager", Name: "manager", Workspace: tmp}); err != nil {
		t.Fatalf("dispatch manager: %v", err)
	}
	defer stopAndWaitForTest(t, mgr, "manager")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"restart",
		"--agent", "manager",
		"--wait",
		"--wait-timeout", "2s",
		"--json",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("restart --agent manager --wait --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var body lifecycleHealthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode restart filtered wait json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || !body.Health.Healthy {
		t.Fatalf("filtered restart wait health = %+v, want healthy manager-only health", body.Health)
	}
	if len(body.Actions) != 1 || body.Actions[0].Instance != "manager" || body.Actions[0].Action != "restart" {
		t.Fatalf("filtered restart wait actions = %+v, want manager restart only", body.Actions)
	}
	if body.Health.Declared.Persistent != 1 || body.Health.Declared.Running != 1 || body.Health.Declared.Missing != 0 {
		t.Fatalf("filtered restart wait declared health = %+v, want only manager declared", body.Health.Declared)
	}
}

func TestLifecycleActionResultsHaveErrors(t *testing.T) {
	cases := []struct {
		name string
		rows []lifecycleActionResult
		want bool
	}{
		{
			name: "clean",
			rows: []lifecycleActionResult{{Action: "restart", Instance: "manager", Status: "running"}},
		},
		{
			name: "error action",
			rows: []lifecycleActionResult{{Action: "error", Instance: "manager", Status: "error"}},
			want: true,
		},
		{
			name: "error status",
			rows: []lifecycleActionResult{{Action: "restart", Instance: "manager", Status: "error"}},
			want: true,
		},
		{
			name: "error detail",
			rows: []lifecycleActionResult{{Action: "restart", Instance: "manager", Status: "unknown", Error: "boom"}},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lifecycleActionResultsHaveErrors(tc.rows); got != tc.want {
				t.Fatalf("lifecycleActionResultsHaveErrors() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInstanceDownResultsHaveErrors(t *testing.T) {
	cases := []struct {
		name string
		rows []instanceDownResult
		want bool
	}{
		{
			name: "clean",
			rows: []instanceDownResult{{Action: "stop", Instance: "manager", Status: "stopped"}},
		},
		{
			name: "error action",
			rows: []instanceDownResult{{Action: "error", Instance: "manager", Status: "error"}},
			want: true,
		},
		{
			name: "error status",
			rows: []instanceDownResult{{Action: "stop", Instance: "manager", Status: "error"}},
			want: true,
		},
		{
			name: "error detail",
			rows: []instanceDownResult{{Action: "stop", Instance: "manager", Status: "stopped", Error: "boom"}},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := instanceDownResultsHaveErrors(tc.rows); got != tc.want {
				t.Fatalf("instanceDownResultsHaveErrors() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDryRunStartAndRestartResults(t *testing.T) {
	start := dryRunStartResult(lifecycleTarget{
		name:  "manager",
		agent: "manager",
	})
	if start.Action != "start" || start.Status != "unknown" || start.Detail != "would start" || !start.DryRun {
		t.Fatalf("start dry-run = %+v, want fresh start preview", start)
	}

	resume := dryRunStartResult(lifecycleTarget{
		name:  "manager",
		agent: "manager",
		meta:  &daemon.Metadata{Status: daemon.StatusStopped, PID: 123},
	})
	if resume.Action != "resume" || resume.Status != "stopped" || resume.Detail != "would resume" || resume.PID != 123 || !resume.DryRun {
		t.Fatalf("resume dry-run = %+v, want stopped resume preview", resume)
	}

	unsupported := dryRunStartResult(lifecycleTarget{
		name:  "manager",
		agent: "manager",
		meta: &daemon.Metadata{
			Status:        daemon.StatusStopped,
			PID:           321,
			Runtime:       string(runtimebin.KindCodex),
			RuntimeBinary: runtimebin.DefaultBinaryForKind(runtimebin.KindCodex),
			SessionID:     "sid-manager",
		},
	})
	if unsupported.Action != lifecycleActionUnsupported || unsupported.Status != "stopped" || unsupported.PID != 321 || !unsupported.DryRun {
		t.Fatalf("unsupported dry-run = %+v, want stopped Codex unsupported preview", unsupported)
	}
	if !strings.Contains(unsupported.Detail, `runtime "codex" does not support managed resume`) {
		t.Fatalf("unsupported detail = %q, want Codex resume limitation", unsupported.Detail)
	}
	for _, want := range []string{
		`agent-team resume-plan manager`,
		`agent-team logs manager --follow`,
		`agent-team logs manager --last-message`,
		`codex resume sid-manager`,
	} {
		if !strings.Contains(unsupported.Detail, want) {
			t.Fatalf("unsupported detail = %q, want %q", unsupported.Detail, want)
		}
	}

	staleUnsupported := dryRunStartResultWithDaemonState(lifecycleTarget{
		name:  "manager",
		agent: "manager",
		meta: &daemon.Metadata{
			Status:    daemon.StatusRunning,
			Runtime:   string(runtimebin.KindCodex),
			SessionID: "sid-running",
		},
	}, false)
	if staleUnsupported.Action != lifecycleActionUnsupported || staleUnsupported.Status != "running" || !staleUnsupported.DryRun {
		t.Fatalf("stale unsupported dry-run = %+v, want stale running Codex unsupported preview", staleUnsupported)
	}
	if !strings.Contains(staleUnsupported.Detail, "recorded running pid is not live") {
		t.Fatalf("stale unsupported detail = %q, want stale pid context", staleUnsupported.Detail)
	}
	if !strings.Contains(staleUnsupported.Detail, `agent-team resume-plan manager`) || !strings.Contains(staleUnsupported.Detail, `agent-team logs manager --last-message`) || !strings.Contains(staleUnsupported.Detail, `codex resume sid-running`) {
		t.Fatalf("stale unsupported detail = %q, want Codex fallback hints", staleUnsupported.Detail)
	}

	jobUnsupported := dryRunStartResult(lifecycleTarget{
		name:  "worker-squ-42",
		agent: "worker",
		meta: &daemon.Metadata{
			Status:  daemon.StatusStopped,
			Runtime: string(runtimebin.KindCodex),
			Job:     "squ-42",
		},
	})
	if !strings.Contains(jobUnsupported.Detail, `agent-team job resume-plan squ-42`) || strings.Contains(jobUnsupported.Detail, `agent-team resume-plan worker-squ-42`) {
		t.Fatalf("job unsupported detail = %q, want job-scoped resume-plan hint", jobUnsupported.Detail)
	}

	skip := dryRunStartResult(lifecycleTarget{
		name:  "manager",
		agent: "manager",
		meta:  &daemon.Metadata{Status: daemon.StatusRunning, PID: 456},
	})
	if skip.Action != "skip" || skip.Status != "running" || skip.Detail != "already running" || skip.PID != 456 || !skip.DryRun {
		t.Fatalf("running dry-run = %+v, want skip preview", skip)
	}

	restart := dryRunRestartResult(lifecycleTarget{
		name:  "manager",
		agent: "manager",
		meta:  &daemon.Metadata{Status: daemon.StatusRunning, PID: 789},
	})
	if restart.Action != "restart" || restart.Status != "running" || restart.Detail != "would restart" || restart.PID != 789 || !restart.DryRun {
		t.Fatalf("restart dry-run = %+v, want restart preview", restart)
	}
}

func TestDryRunDownResult(t *testing.T) {
	stop := dryRunDownResult("manager", &daemon.Metadata{Status: daemon.StatusRunning}, true, "stop")
	if stop.Action != "stop" || stop.Status != "running" || stop.Detail != "would stop" || !stop.DryRun {
		t.Fatalf("stop dry-run = %+v, want stop preview", stop)
	}

	kill := dryRunDownResult("manager", &daemon.Metadata{Status: daemon.StatusRunning}, true, "kill")
	if kill.Action != "kill" || kill.Status != "running" || kill.Detail != "would kill" || !kill.DryRun {
		t.Fatalf("kill dry-run = %+v, want kill preview", kill)
	}

	skip := dryRunDownResult("manager", &daemon.Metadata{Status: daemon.StatusStopped}, false, "stop")
	if skip.Action != "skip" || skip.Status != "skipped" || skip.Detail != "not running" || !skip.DryRun {
		t.Fatalf("stopped dry-run = %+v, want skip preview", skip)
	}
}

func TestStatusShowsDaemonAndInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	for _, want := range []string{"daemon: not running", "(no instances)"} {
		if !strings.Contains(body, want) {
			t.Errorf("status output missing %q: %s", want, body)
		}
	}
}

func TestStatusSummaryShowsHealthWithoutFailing(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary should not fail on unhealthy fleet: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	for _, want := range []string{"health: unhealthy", "daemon: not running", "declared:", "instances:", "phases:"} {
		if !strings.Contains(body, want) {
			t.Fatalf("status --summary output missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "INSTANCE\tAGENT") {
		t.Fatalf("status --summary should not render the full instance table:\n%s", body)
	}
}

func TestPruneNoLocalMetadataNoops(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"prune", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(out.String()); got != "(nothing to remove)" {
		t.Fatalf("stdout = %q, want nothing to remove", out.String())
	}
}

func TestStatusSummaryJSONShowsHealthWithoutFailing(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --json should not fail on unhealthy fleet: %v\nstderr: %s", err, errOut.String())
	}
	var body healthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status summary json: %v\nbody=%s", err, out.String())
	}
	if body.Healthy || body.Daemon.Running {
		t.Fatalf("status summary json should report unhealthy daemon-down state: %+v", body)
	}
	if body.Declared.Persistent == 0 || body.Declared.Missing == 0 {
		t.Fatalf("status summary json should include declared persistent counts: %+v", body.Declared)
	}
}

func TestStatusSummaryEventsJSONIncludesEventSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, ev := range []daemon.LifecycleEvent{
		{
			TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
			Action:   "stop",
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusStopped,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 2, 0, 0, time.UTC),
			Action:   "dispatch",
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusRunning,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 3, 0, 0, time.UTC),
			Action:   "stop",
			Instance: "manager",
			Agent:    "manager",
			Status:   daemon.StatusStopped,
		},
		{
			TS:       time.Date(2026, 6, 17, 12, 4, 0, 0, time.UTC),
			Action:   "stop",
			Instance: "worker",
			Agent:    "worker",
			Status:   daemon.StatusStopped,
		},
	} {
		if err := daemon.AppendLifecycleEvent(root, &ev); err != nil {
			t.Fatalf("append event %s/%s: %v", ev.Instance, ev.Action, err)
		}
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"status",
		"--summary",
		"--events", "5",
		"--event-action", "stop",
		"--since", "2026-06-17T12:01:00Z",
		"--agent", "manager",
		"--json",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --events --json: %v\nstderr: %s", err, errOut.String())
	}
	var body statusSummarySnapshot
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status summary events json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || body.Health.Declared.Persistent == 0 {
		t.Fatalf("health = %+v, want embedded health summary", body.Health)
	}
	if body.Events == nil {
		t.Fatalf("events summary missing from status summary: %+v", body)
	}
	if body.Events.Total != 1 || body.Events.Actions["stop"] != 1 || body.Events.Statuses["stopped"] != 1 || body.Events.Agents["manager"] != 1 || body.Events.Instances["manager"] != 1 {
		t.Fatalf("events summary = %+v, want one recent manager stop", body.Events)
	}
}

func TestStatusSummaryEventsTextIncludesEventSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.AppendLifecycleEvent(daemon.DaemonRoot(teamDir), &daemon.LifecycleEvent{
		TS:       time.Date(2026, 6, 17, 12, 3, 0, 0, time.UTC),
		Action:   "stop",
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--events", "5", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --events: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	for _, want := range []string{"health:", "events: total=1", "actions: stop=1", "instances: manager=1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("status summary events output missing %q:\n%s", want, body)
		}
	}
}

func TestStatusSummaryResourcesJSONUsesLocalMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "done"
description = "finished"
`, time.Time{})
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--resources", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --resources --json: %v\nstderr: %s", err, errOut.String())
	}
	var body statusSummarySnapshot
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status summary resources json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || body.Health.Summary.Total != 1 {
		t.Fatalf("health summary = %+v, want one manager row", body.Health)
	}
	if body.Resources == nil {
		t.Fatalf("resources summary missing: %+v", body)
	}
	if body.Resources.Total != 1 || body.Resources.Stopped != 1 || body.Resources.Phases["done"] != 1 || body.Resources.Measured != 0 {
		t.Fatalf("resources summary = %+v, want one stopped done manager without process metrics", body.Resources)
	}
}

func TestStatusSummaryResourcesTextIncludesResourceSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--resources", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --resources: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	for _, want := range []string{"health:", "resources:", "instances: total=1", "measured=0"} {
		if !strings.Contains(body, want) {
			t.Fatalf("status summary resources output missing %q:\n%s", want, body)
		}
	}
}

func TestStatusSummaryPlanJSONIncludesPlanSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--plan", "--agent", "manager", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --plan --json: %v\nstderr: %s", err, errOut.String())
	}
	var body statusSummarySnapshot
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status summary plan json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || body.Health.Declared.Persistent == 0 {
		t.Fatalf("health = %+v, want embedded health summary", body.Health)
	}
	if body.Plan == nil {
		t.Fatalf("plan summary missing: %+v", body)
	}
	if body.Plan.Summary.Total != 1 || body.Plan.Summary.Actions["start"] != 1 || !body.Plan.Summary.DryRun {
		t.Fatalf("plan summary = %+v, want one dry-run manager start", body.Plan.Summary)
	}
}

func TestStatusSummaryPlanTextIncludesPlanSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--plan", "--agent", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --plan: %v\nstderr: %s", err, errOut.String())
	}
	body := out.String()
	for _, want := range []string{"health:", "plan:", "summary: total=1 dry_run=true start=1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("status summary plan output missing %q:\n%s", want, body)
		}
	}
}

func TestStatusSummaryLatestJSONScopesHealthRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--latest", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --latest --json should not fail: %v\nstderr: %s", err, errOut.String())
	}
	var body healthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status summary latest json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Stopped != 1 {
		t.Fatalf("summary = %+v, want one stopped latest row", body.Summary)
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "new" {
		t.Fatalf("instances = %+v, want only newest row", body.Instances)
	}
}

func TestStatusSummaryStrictTopologyReportsExtra(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "adhoc",
		Agent:    "worker",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--summary", "--strict-topology", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --summary --strict-topology --json should not fail: %v\nstderr: %s", err, errOut.String())
	}
	var body healthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status strict summary json: %v\nbody=%s", err, out.String())
	}
	var found bool
	for _, issue := range body.Issues {
		if issue.Code == "topology_extra_running" && issue.Instance == "adhoc" {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %+v, want adhoc topology_extra_running", body.Issues)
	}
}

func TestStatusJSONShowsDaemonAndInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --json: %v\nstderr: %s", err, errOut.String())
	}
	var body statusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status json: %v\nbody=%s", err, out.String())
	}
	if body.Daemon.Running {
		t.Fatalf("daemon should not be running: %+v", body.Daemon)
	}
	if body.Daemon.PID != 0 {
		t.Fatalf("daemon pid should be omitted/zero when not running: %+v", body.Daemon)
	}
	if len(body.Instances) != 0 {
		t.Fatalf("instances = %+v, want empty", body.Instances)
	}
}

func TestStatusJSONReportsDaemonNotReady(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(daemon.DaemonRoot(teamDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.PidPath(teamDir), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"status", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --json: %v\nstderr: %s", err, errOut.String())
	}
	var body statusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status json: %v\nbody=%s", err, out.String())
	}
	if !body.Daemon.Running || body.Daemon.Ready || body.Daemon.PID != os.Getpid() {
		t.Fatalf("daemon should be running but not ready: %+v", body.Daemon)
	}
	if !strings.Contains(body.Daemon.Error, "socket") {
		t.Fatalf("daemon readiness error = %q, want socket detail", body.Daemon.Error)
	}
}

func TestStatusJSONFiltersInstanceRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	for _, name := range []string{"manager", "worker-1"} {
		stateDir := filepath.Join(teamDir, "state", name)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(`[status]
phase = "idle"
description = "waiting"
`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts, err := newPsOptions(nil, []string{"manager"}, nil, false)
	if err != nil {
		t.Fatalf("newPsOptions: %v", err)
	}

	var out bytes.Buffer
	if err := runStatusJSONWithOptions(&out, teamDir, time.Now(), opts); err != nil {
		t.Fatalf("runStatusJSONWithOptions: %v", err)
	}
	var body statusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status json: %v\nbody=%s", err, out.String())
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "manager" {
		t.Fatalf("instances = %+v, want only manager", body.Instances)
	}
}

func TestStatusRuntimeFilterUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-worker", Agent: "worker", Runtime: string(runtimebin.KindCodex), RuntimeBinary: "codex-dev", Status: daemon.StatusRunning, PID: 321},
		{Instance: "claude-manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), RuntimeBinary: "claude-code", Status: daemon.StatusRunning, PID: 654},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"status", "--runtime", "codex", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --runtime --json: %v\nstderr=%s", err, stderr.String())
	}
	var body statusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status json: %v\nbody=%s", err, out.String())
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "codex-worker" || body.Instances[0].Runtime != "codex" || body.Instances[0].RuntimeBinary != "codex-dev" {
		t.Fatalf("instances = %+v, want only Codex worker with runtime metadata", body.Instances)
	}
}

func TestStatusUnhealthyJSONShowsCrashedAndStaleRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusRunning, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "idle"
description = "waiting"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"status", "--unhealthy", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --unhealthy --json: %v\nstderr=%s", err, stderr.String())
	}
	var body statusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status unhealthy json: %v\nbody=%s", err, out.String())
	}
	if got := strings.Join(rowInstances(body.Instances), ","); got != "crashed,stale" {
		t.Fatalf("instances = %v, want crashed,stale", rowInstances(body.Instances))
	}
	if body.Instances[0].Status != "crashed" || !body.Instances[1].Stale {
		t.Fatalf("instances = %+v, want crashed row and stale row", body.Instances)
	}
}

func TestStatusLatestJSONShowsNewestInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"status", "--latest", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --latest --json: %v\nstderr=%s", err, stderr.String())
	}
	var body statusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status json: %v\nbody=%s", err, out.String())
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "new" {
		t.Fatalf("instances = %+v, want newest instance", body.Instances)
	}
}

func TestStatusLastJSONShowsNewestInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"status", "--last", "2", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --last --json: %v\nstderr=%s", err, stderr.String())
	}
	var body statusJSON
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode status json: %v\nbody=%s", err, out.String())
	}
	got := rowInstances(body.Instances)
	want := []string{"new", "mid"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instances = %v, want %v", got, want)
	}
}

func TestStatusFormatPrintsFilteredRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	for _, name := range []string{"manager", "worker-1"} {
		stateDir := filepath.Join(teamDir, "state", name)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(`[status]
phase = "idle"
description = "waiting"
`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"status",
		"--format", "{{.Instance}}:{{.Agent}}:{{.Status}}:{{.Phase}}",
		"--agent", "manager",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --format: %v\nstderr: %s", err, errOut.String())
	}
	if got, want := out.String(), "manager:manager:unknown:idle\n"; got != want {
		t.Fatalf("status --format output = %q, want %q", got, want)
	}
}

func TestStatusFormatRejectsConflictingStructuredModes(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json",
			args: []string{"status", "--format", "{{.Instance}}", "--json", "--target", tmp},
			want: "--format cannot be combined",
		},
		{
			name: "summary",
			args: []string{"status", "--format", "{{.Instance}}", "--summary", "--target", tmp},
			want: "--format cannot be combined",
		},
		{
			name: "invalid template",
			args: []string{"status", "--format", "{{", "--target", tmp},
			want: "invalid --format template",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(errOut)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected status --format validation failure, stdout=%s", out.String())
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tc.want)
			}
		})
	}
}

func TestStatusWatchStopsOnContextCancel(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatusWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, false, psOptions{}); err != nil {
		t.Fatalf("runStatusWatch: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "daemon: not running") || !strings.Contains(body, "(no instances)") {
		t.Fatalf("watch output missing status snapshot: %q", body)
	}
}

func TestStatusWatchTextClearsWhenRequested(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatusWatchWithClear(ctx, &buf, teamDir, time.Millisecond, time.Now, false, psOptions{}, true); err != nil {
		t.Fatalf("runStatusWatchWithClear: %v", err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, watchClearSequence) {
		t.Fatalf("status watch should start with clear sequence, got %q", body[:min(len(body), len(watchClearSequence)+20)])
	}
	if !strings.Contains(body, "daemon: not running") || !strings.Contains(body, "(no instances)") {
		t.Fatalf("status watch clear output missing snapshot: %q", body)
	}
}

func TestStatusWatchJSONEmitsSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatusWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, true, psOptions{}); err != nil {
		t.Fatalf("runStatusWatch json: %v", err)
	}
	body := strings.TrimSpace(buf.String())
	if body == "" {
		t.Fatalf("watch json output empty")
	}
	first := strings.Split(body, "\n")[0]
	var snapshot statusJSON
	if err := json.Unmarshal([]byte(first), &snapshot); err != nil {
		t.Fatalf("first snapshot is not json: %v\nbody=%s", err, body)
	}
	if snapshot.Daemon.Running {
		t.Fatalf("daemon should not be running: %+v", snapshot.Daemon)
	}
}

func TestStatusWatchFormatEmitsRowsWithoutClear(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	stateDir := filepath.Join(teamDir, "state", "manager")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "status.toml"), []byte(`[status]
phase = "idle"
description = "waiting"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetContext(ctx)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{
		"status",
		"--watch",
		"--format", "{{.Instance}}:{{.Phase}}",
		"--interval", "1ms",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --watch --format: %v\nstderr: %s", err, errOut.String())
	}
	first := strings.Split(strings.TrimSpace(out.String()), "\n")[0]
	if first != "manager:idle" {
		t.Fatalf("first status format watch row = %q, want manager:idle\nbody=%s", first, out.String())
	}
	if strings.Contains(out.String(), watchClearSequence) {
		t.Fatalf("status --watch --format should not emit clear sequence: %q", out.String())
	}
	if strings.Contains(out.String(), "\n\n") {
		t.Fatalf("status --watch --format should not insert blank snapshot separators: %q", out.String())
	}
}

func TestStatusSummaryWatchStopsOnContextCancel(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatusSummaryWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, false, psOptions{}); err != nil {
		t.Fatalf("runStatusSummaryWatch: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "health: unhealthy") || !strings.Contains(body, "daemon: not running") {
		t.Fatalf("summary watch output missing health snapshot: %q", body)
	}
}

func TestStatusSummaryWatchTextClearsWhenRequested(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatusSummaryWatchWithClear(ctx, &buf, teamDir, time.Millisecond, time.Now, false, psOptions{}, true); err != nil {
		t.Fatalf("runStatusSummaryWatchWithClear: %v", err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, watchClearSequence) {
		t.Fatalf("status summary watch should start with clear sequence, got %q", body[:min(len(body), len(watchClearSequence)+20)])
	}
	if !strings.Contains(body, "health: unhealthy") || !strings.Contains(body, "daemon: not running") {
		t.Fatalf("status summary watch clear output missing snapshot: %q", body)
	}
}

func TestStatusRejectsUnknownFilter(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"status", "--status", "paused"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected status filter validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --status") {
		t.Fatalf("stderr = %q, want status validation", stderr.String())
	}
}

func TestStatusRejectsUnknownRuntime(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"status", "--runtime", "llama"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected runtime filter validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --runtime") {
		t.Fatalf("stderr = %q, want runtime validation", stderr.String())
	}
}

func TestStatusLatestLastValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"status", "--latest", "--last", "2"}, "choose one of --latest or --last"},
	} {
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

func TestStatusStrictTopologyRequiresSummary(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"status", "--strict-topology"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --strict-topology validation error")
	}
	if !strings.Contains(stderr.String(), "--strict-topology requires --summary") {
		t.Fatalf("stderr = %q, want strict topology validation", stderr.String())
	}
}

func TestStatusEventFlagsValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--events", "-1"}, "--events must be >= 0"},
		{[]string{"status", "--events", "5"}, "--events requires --summary"},
		{[]string{"status", "--resources"}, "--resources requires --summary"},
		{[]string{"status", "--plan"}, "--plan requires --summary"},
		{[]string{"status", "--stop-extras"}, "--stop-extras requires --plan"},
		{[]string{"status", "--action", "start"}, "--action requires --plan"},
		{[]string{"status", "--since", "10m"}, "--since requires --events"},
		{[]string{"status", "--event-action", "stop"}, "--event-action requires --events"},
		{[]string{"status", "--summary", "--events", "5", "--since", "recently"}, "--since must be a duration"},
		{[]string{"status", "--summary", "--events", "5", "--event-action", ","}, "--event-action requires at least one non-empty action"},
		{[]string{"status", "--summary", "--plan", "--action", "pause"}, "unknown --action"},
	} {
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

func TestStatusNegativeIntervalFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"status", "--watch", "--interval", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected interval validation error")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q, want interval validation", stderr.String())
	}
}

type fakeInstanceLister struct {
	snapshots [][]*daemon.Metadata
	calls     int
}

func (f *fakeInstanceLister) Instances() ([]*daemon.Metadata, error) {
	if len(f.snapshots) == 0 {
		return nil, nil
	}
	idx := f.calls
	if idx >= len(f.snapshots) {
		idx = len(f.snapshots) - 1
	}
	f.calls++
	return f.snapshots[idx], nil
}

func phaseSnapshotSource(snapshots ...map[string]string) waitPhaseSource {
	calls := 0
	return func() map[string]string {
		if len(snapshots) == 0 {
			return nil
		}
		idx := calls
		if idx >= len(snapshots) {
			idx = len(snapshots) - 1
		}
		calls++
		return snapshots[idx]
	}
}

func TestWaitForInstancesImmediateTerminal(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusStopped}},
	}}
	results, err := waitForInstances(context.Background(), lister, []string{"mgr"}, time.Millisecond)
	if err != nil {
		t.Fatalf("waitForInstances: %v", err)
	}
	if len(results) != 1 || results[0].Instance != "mgr" || results[0].Status != "stopped" {
		t.Fatalf("results = %+v", results)
	}
}

func TestWaitForInstancesPollsUntilTerminal(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusRunning, PID: 10}},
		{{Instance: "mgr", Status: daemon.StatusExited}},
	}}
	results, err := waitForInstances(context.Background(), lister, []string{"mgr"}, time.Millisecond)
	if err != nil {
		t.Fatalf("waitForInstances: %v", err)
	}
	if lister.calls < 2 {
		t.Fatalf("expected at least two polls, got %d", lister.calls)
	}
	if len(results) != 1 || results[0].Status != "exited" {
		t.Fatalf("results = %+v", results)
	}
}

func TestWaitForInstancesUntilRunning(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusStopped}},
		{{Instance: "mgr", Status: daemon.StatusRunning, PID: 10}},
	}}
	results, err := waitForInstancesUntil(context.Background(), lister, []string{"mgr"}, time.Millisecond, waitUntilRunning)
	if err != nil {
		t.Fatalf("waitForInstancesUntil: %v", err)
	}
	if lister.calls < 2 {
		t.Fatalf("expected at least two polls, got %d", lister.calls)
	}
	if len(results) != 1 || results[0].Status != "running" || results[0].PID != 10 {
		t.Fatalf("results = %+v", results)
	}
}

func TestWaitForInstancesUntilPhase(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusRunning, PID: 10}},
	}}
	phases := phaseSnapshotSource(
		map[string]string{"mgr": "implementing"},
		map[string]string{"mgr": "done"},
	)
	results, err := waitForInstancesUntilWithPhases(context.Background(), lister, phases, []string{"mgr"}, time.Millisecond, waitUntilAny, map[string]bool{"done": true})
	if err != nil {
		t.Fatalf("waitForInstancesUntilWithPhases: %v", err)
	}
	if len(results) != 1 || results[0].Status != "running" || results[0].Phase != "done" {
		t.Fatalf("results = %+v, want running/done", results)
	}
}

func TestWaitForInstancesUntilLifecycleAndPhase(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusStopped}},
		{{Instance: "mgr", Status: daemon.StatusRunning, PID: 10}},
	}}
	phases := phaseSnapshotSource(
		map[string]string{"mgr": "done"},
		map[string]string{"mgr": "done"},
	)
	results, err := waitForInstancesUntilWithPhases(context.Background(), lister, phases, []string{"mgr"}, time.Millisecond, waitUntilRunning, map[string]bool{"done": true})
	if err != nil {
		t.Fatalf("waitForInstancesUntilWithPhases: %v", err)
	}
	if lister.calls < 2 {
		t.Fatalf("expected wait to require lifecycle and phase, got %d calls", lister.calls)
	}
	if len(results) != 1 || results[0].Status != "running" || results[0].Phase != "done" {
		t.Fatalf("results = %+v, want running/done", results)
	}
}

func TestWaitForInstancesUntilRemoved(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusRunning, PID: 10}},
		{},
	}}
	results, err := waitForInstancesUntil(context.Background(), lister, []string{"mgr"}, time.Millisecond, waitUntilRemoved)
	if err != nil {
		t.Fatalf("waitForInstancesUntil: %v", err)
	}
	if len(results) != 1 || results[0].Status != "removed" {
		t.Fatalf("results = %+v", results)
	}
}

func TestWaitForInstancesUntilRunningDoesNotTreatRemovedAsSuccess(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusStopped}},
		{},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := waitForInstancesUntil(ctx, lister, []string{"mgr"}, time.Millisecond, waitUntilRunning)
	var timeoutErr *waitTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected timeout after removal before running, got %v", err)
	}
	if len(timeoutErr.Pending) != 1 || timeoutErr.Pending[0].Status != "removed" {
		t.Fatalf("pending = %+v, want removed pending row", timeoutErr.Pending)
	}
}

func TestWaitForInstancesStoppedWaitsForProcessExit(t *testing.T) {
	prev := daemon.PidLiveCheck
	daemon.PidLiveCheck = func(pid int) bool { return pid == 99 }
	t.Cleanup(func() { daemon.PidLiveCheck = prev })

	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusStopped, PID: 99}},
		{{Instance: "mgr", Status: daemon.StatusStopped, PID: 99, ExitedAt: time.Now()}},
	}}
	results, err := waitForInstances(context.Background(), lister, []string{"mgr"}, time.Millisecond)
	if err != nil {
		t.Fatalf("waitForInstances: %v", err)
	}
	if lister.calls < 2 {
		t.Fatalf("expected wait to poll past stopped-with-live-pid, got %d calls", lister.calls)
	}
	if len(results) != 1 || results[0].Status != "stopped" {
		t.Fatalf("results = %+v", results)
	}
}

func TestWaitForInstancesStoppedDeadPIDIsTerminal(t *testing.T) {
	prev := daemon.PidLiveCheck
	daemon.PidLiveCheck = func(pid int) bool { return false }
	t.Cleanup(func() { daemon.PidLiveCheck = prev })

	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusStopped, PID: 99}},
	}}
	results, err := waitForInstances(context.Background(), lister, []string{"mgr"}, time.Millisecond)
	if err != nil {
		t.Fatalf("waitForInstances: %v", err)
	}
	if len(results) != 1 || results[0].Status != "stopped" {
		t.Fatalf("results = %+v", results)
	}
}

func TestWaitForInstancesPreservesArgumentOrder(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "worker", Status: daemon.StatusStopped},
			{Instance: "manager", Status: daemon.StatusExited},
		},
	}}
	results, err := waitForInstances(context.Background(), lister, []string{"manager", "worker"}, time.Millisecond)
	if err != nil {
		t.Fatalf("waitForInstances: %v", err)
	}
	if got := []string{results[0].Instance, results[1].Instance}; got[0] != "manager" || got[1] != "worker" {
		t.Fatalf("order = %v, want [manager worker]", got)
	}
}

func TestWaitAllInstanceNamesSorted(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "worker", Status: daemon.StatusStopped},
			{Instance: "manager", Status: daemon.StatusStopped},
		},
	}}
	names, err := waitAllInstanceNames(lister)
	if err != nil {
		t.Fatalf("waitAllInstanceNames: %v", err)
	}
	if strings.Join(names, ",") != "manager,worker" {
		t.Fatalf("names = %v, want sorted manager,worker", names)
	}
}

func TestWaitAgentInstanceNamesSortedAndFiltered(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "manager-b", Agent: "manager", Status: daemon.StatusStopped},
			{Instance: "manager-a", Agent: "manager", Status: daemon.StatusExited},
		},
	}}
	names, err := waitAgentInstanceNames(lister, []string{"manager"})
	if err != nil {
		t.Fatalf("waitAgentInstanceNames: %v", err)
	}
	if strings.Join(names, ",") != "manager-a,manager-b" {
		t.Fatalf("names = %v, want sorted manager-a,manager-b", names)
	}
}

func TestWaitFilteredInstanceNamesByStatus(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "manager-b", Agent: "manager", Status: daemon.StatusStopped},
			{Instance: "manager-a", Agent: "manager", Status: daemon.StatusExited},
			{Instance: "unknown", Agent: "manager"},
		},
	}}
	names, err := waitFilteredInstanceNames(lister, nil, []string{"stopped,exited"})
	if err != nil {
		t.Fatalf("waitFilteredInstanceNames: %v", err)
	}
	if strings.Join(names, ",") != "manager-a,manager-b" {
		t.Fatalf("names = %v, want sorted stopped/exited manager-a,manager-b", names)
	}

	names, err = waitFilteredInstanceNames(lister, nil, []string{"unknown"})
	if err != nil {
		t.Fatalf("waitFilteredInstanceNames unknown: %v", err)
	}
	if strings.Join(names, ",") != "unknown" {
		t.Fatalf("unknown names = %v, want [unknown]", names)
	}
}

func TestWaitFilteredInstanceNamesByRuntime(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "codex-worker", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning},
			{Instance: "claude-manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning},
			{Instance: "unknown-runtime", Agent: "worker", Status: daemon.StatusRunning},
		},
	}}
	names, err := waitFilteredInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister, nil, nil, []string{"codex"}, nil, nil, nil, false, false, false)
	if err != nil {
		t.Fatalf("waitFilteredInstanceNamesWithPhasesStaleRuntimeAndUnhealthy: %v", err)
	}
	if strings.Join(names, ",") != "codex-worker" {
		t.Fatalf("names = %v, want codex-worker only", names)
	}

	names, err = waitFilteredInstanceNamesWithPhasesStaleRuntimeAndUnhealthy(lister, nil, nil, []string{"unknown"}, nil, nil, nil, false, false, false)
	if err == nil || !strings.Contains(err.Error(), "unknown --runtime") {
		t.Fatalf("unknown runtime error = %v, want unknown --runtime", err)
	}
}

func TestWaitFilteredInstanceNamesByAgentAndStatus(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "manager-running", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "manager-stopped", Agent: "manager", Status: daemon.StatusStopped},
		},
	}}
	names, err := waitFilteredInstanceNames(lister, []string{"manager"}, []string{"running"})
	if err != nil {
		t.Fatalf("waitFilteredInstanceNames: %v", err)
	}
	if strings.Join(names, ",") != "manager-running" {
		t.Fatalf("names = %v, want manager-running only", names)
	}
}

func TestWaitFilteredInstanceNamesByPhase(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "manager-blocked", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "manager-idle", Agent: "manager", Status: daemon.StatusRunning},
		},
	}}
	phases := map[string]string{
		"manager-blocked": "blocked",
		"manager-idle":    "idle",
	}
	names, err := waitFilteredInstanceNamesWithPhases(lister, []string{"manager"}, nil, []string{"blocked"}, phases)
	if err != nil {
		t.Fatalf("waitFilteredInstanceNamesWithPhases: %v", err)
	}
	if strings.Join(names, ",") != "manager-blocked" {
		t.Fatalf("names = %v, want manager-blocked only", names)
	}
}

func TestWaitFilteredInstanceNamesByStale(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "manager-stale", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "manager-fresh", Agent: "manager", Status: daemon.StatusRunning},
			{Instance: "worker-stale", Agent: "worker", Status: daemon.StatusRunning},
		},
	}}
	names, err := waitFilteredInstanceNamesWithPhasesAndStale(lister, []string{"manager"}, nil, nil, nil, map[string]bool{
		"manager-stale": true,
		"worker-stale":  true,
	})
	if err != nil {
		t.Fatalf("waitFilteredInstanceNamesWithPhasesAndStale: %v", err)
	}
	if strings.Join(names, ",") != "manager-stale" {
		t.Fatalf("names = %v, want manager-stale only", names)
	}
}

func TestWaitFilteredInstanceNamesByUnhealthy(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed},
			{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning},
			{Instance: "stale", Agent: "manager", Status: daemon.StatusRunning},
		},
	}}
	names, err := waitFilteredInstanceNamesWithPhasesStaleAndUnhealthy(lister, nil, nil, nil, nil, map[string]bool{
		"stale": true,
	}, false, true)
	if err != nil {
		t.Fatalf("waitFilteredInstanceNamesWithPhasesStaleAndUnhealthy: %v", err)
	}
	if strings.Join(names, ",") != "crashed,stale" {
		t.Fatalf("names = %v, want crashed and stale", names)
	}
}

func TestWaitLatestInstanceNamesLimitUsesNewestStartedAt(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "old-worker", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-3 * time.Hour)},
			{Instance: "new-manager", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Hour)},
			{Instance: "mid-worker", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
			{Instance: "newer-running-worker", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
		},
	}}
	names, err := waitLatestInstanceNamesLimit(lister, []string{"worker"}, []string{"stopped"}, 2)
	if err != nil {
		t.Fatalf("waitLatestInstanceNamesLimit: %v", err)
	}
	if strings.Join(names, ",") != "mid-worker,old-worker" {
		t.Fatalf("names = %v, want newest two stopped workers", names)
	}
}

func TestWaitLatestInstanceNamesFiltersStaleBeforeLimit(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "old-stale", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-3 * time.Hour)},
			{Instance: "new-stale", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
			{Instance: "fresh-newer", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Hour)},
		},
	}}
	names, err := waitLatestInstanceNamesLimitWithPhasesAndStale(lister, []string{"worker"}, nil, nil, nil, map[string]bool{
		"old-stale": true,
		"new-stale": true,
	}, 1)
	if err != nil {
		t.Fatalf("waitLatestInstanceNamesLimitWithPhasesAndStale: %v", err)
	}
	if strings.Join(names, ",") != "new-stale" {
		t.Fatalf("names = %v, want newest stale worker", names)
	}
}

func TestWaitLatestInstanceNamesFiltersUnhealthyBeforeLimit(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "crashed-old", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: now.Add(-3 * time.Hour)},
			{Instance: "stale-new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
			{Instance: "fresh-newer", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Hour)},
		},
	}}
	names, err := waitLatestInstanceNamesLimitWithPhasesStaleAndUnhealthy(lister, []string{"worker"}, nil, nil, nil, map[string]bool{
		"stale-new": true,
	}, false, true, 1)
	if err != nil {
		t.Fatalf("waitLatestInstanceNamesLimitWithPhasesStaleAndUnhealthy: %v", err)
	}
	if strings.Join(names, ",") != "stale-new" {
		t.Fatalf("names = %v, want newest unhealthy worker", names)
	}
}

func TestWaitLatestInstanceNamesFiltersRuntimeBeforeLimit(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{
			{Instance: "codex-old", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, StartedAt: now.Add(-3 * time.Hour)},
			{Instance: "codex-new", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
			{Instance: "claude-newer", Agent: "worker", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, StartedAt: now.Add(-1 * time.Hour)},
		},
	}}
	names, err := waitLatestInstanceNamesLimitWithPhasesStaleRuntimeAndUnhealthy(lister, nil, nil, []string{"codex"}, nil, nil, nil, false, false, false, 1)
	if err != nil {
		t.Fatalf("waitLatestInstanceNamesLimitWithPhasesStaleRuntimeAndUnhealthy: %v", err)
	}
	if strings.Join(names, ",") != "codex-new" {
		t.Fatalf("names = %v, want newest Codex worker", names)
	}
}

func TestLifecycleAgentFilterSetSplitsCommaSeparatedValues(t *testing.T) {
	got := lifecycleAgentFilterSet([]string{"manager, worker", "ticket-manager"})
	for _, want := range []string{"manager", "worker", "ticket-manager"} {
		if !got[want] {
			t.Fatalf("filter set = %+v, missing %s", got, want)
		}
	}
	if got[""] {
		t.Fatalf("filter set should not include empty values: %+v", got)
	}
}

func TestWaitAgentRequiresNonEmptyFilter(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{}}}
	_, err := waitAgentInstanceNames(lister, []string{"  "})
	if err == nil || !strings.Contains(err.Error(), "non-empty agent") {
		t.Fatalf("err = %v, want non-empty agent validation", err)
	}
}

func TestWaitRequiresNamesUnlessAll(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "instance is required unless --all") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitQuietSuppressesSuccessfulRows(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-wait-quiet-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, nil)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --quiet: %v", err)
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet wait should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestWaitFormatPrintsResultRows(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-wait-format-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, nil)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
		PID:      321,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--format", "{{.Instance}}:{{.Status}}:{{.PID}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --format: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := out.String(), "manager:stopped:321\n"; got != want {
		t.Fatalf("wait --format output = %q, want %q", got, want)
	}
}

func TestWaitSummaryPrintsStatusAndPhaseCounts(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, PID: 321},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, PID: 654},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "done"
description = "finished"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--all", "--until", "stopped", "--summary", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --summary: %v\nstderr=%s", err, stderr.String())
	}
	got := out.String()
	for _, want := range []string{
		`summary: total=2 condition="stopped"`,
		"statuses: stopped=2",
		"phases: done=1 unknown=1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("wait summary output missing %q:\n%s", want, got)
		}
	}
}

func TestWaitSummaryJSONUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, PID: 321},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, PID: 654},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "done"
description = "finished"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--all", "--until", "stopped", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --summary --json: %v\nstderr=%s", err, stderr.String())
	}
	var body waitSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode wait summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 2 || body.Summary.Condition != "stopped" {
		t.Fatalf("summary = %+v, want total=2 condition=stopped", body.Summary)
	}
	if body.Summary.Statuses["stopped"] != 2 {
		t.Fatalf("statuses = %+v, want stopped=2", body.Summary.Statuses)
	}
	if body.Summary.Phases["done"] != 1 || body.Summary.Phases["unknown"] != 1 {
		t.Fatalf("phases = %+v, want done=1 unknown=1", body.Summary.Phases)
	}
}

func TestWaitUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
		PID:      321,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--until", "stopped", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Status != "stopped" || rows[0].PID != 321 {
		t.Fatalf("rows = %+v, want stopped manager", rows)
	}
}

func TestWaitRuntimeFilterUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-worker", Agent: "worker", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: 321},
		{Instance: "claude-manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: 654},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--runtime", "codex", "--until", "running", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --runtime local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "codex-worker" || rows[0].Status != "running" || rows[0].PID != 321 {
		t.Fatalf("rows = %+v, want running Codex worker only", rows)
	}
}

func TestWaitLatestUsesNewestLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--latest", "--until", "stopped", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --latest local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "new" || rows[0].Status != "stopped" {
		t.Fatalf("rows = %+v, want newest stopped metadata", rows)
	}
}

func TestWaitLastUsesNewestLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--last", "2", "--until", "stopped", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --last local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two stopped metadata rows", rows)
	}
}

func TestWaitUntilPhaseUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      321,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "done"
description = "finished"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--until-phase", "done", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --until-phase local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Status != "running" || rows[0].Phase != "done" {
		t.Fatalf("rows = %+v, want running manager in done phase", rows)
	}
}

func TestWaitPhaseFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 321},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: 654},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "blocked"
description = "needs input"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "idle"
description = "waiting"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--phase", "blocked", "--until", "running", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --phase local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Phase != "blocked" {
		t.Fatalf("rows = %+v, want blocked manager only", rows)
	}
}

func TestWaitStaleFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 321, StartedAt: old},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: 654, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "implementing"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--stale", "--until", "running", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --stale local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait --stale json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Phase != "implementing" {
		t.Fatalf("rows = %+v, want stale manager only", rows)
	}
}

func TestWaitUnhealthyDryRunUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, PID: 654, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusRunning, PID: 321, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "idle"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--unhealthy", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --unhealthy --dry-run local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait --unhealthy json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "crashed" || rows[1].Instance != "stale" {
		t.Fatalf("rows = %+v, want crashed and stale", rows)
	}
	if rows[0].Status != string(daemon.StatusCrashed) || rows[1].Phase != "implementing" {
		t.Fatalf("rows = %+v, want crashed status and stale phase", rows)
	}
}

func TestWaitRuntimeStaleDryRunUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: old},
		{Instance: "status-stale", Agent: "manager", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: old},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "status-stale"), `[status]
phase = "implementing"
description = "old status"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "idle"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--runtime-stale", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --runtime-stale --dry-run local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait --runtime-stale json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || rows[0].Status != string(daemon.StatusRunning) {
		t.Fatalf("rows = %+v, want runtime-stale only", rows)
	}
}

func TestWaitDryRunReportsCurrentStateWithoutWaiting(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      321,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --dry-run --json: %v\nstderr=%s", err, stderr.String())
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait dry-run json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Status != "running" || rows[0].PID != 321 {
		t.Fatalf("rows = %+v, want current running manager", rows)
	}
}

func TestWaitDryRunSummaryCountsCurrentStatusAndPhase(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 321},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, PID: 654},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "working"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "done"
description = "finished"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--all", "--dry-run", "--summary", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wait --dry-run --summary: %v\nstderr=%s", err, stderr.String())
	}
	got := out.String()
	for _, want := range []string{
		`summary: total=2 condition="terminal"`,
		"statuses: running=1 stopped=1",
		"phases: implementing=1 done=1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("wait dry-run summary output missing %q:\n%s", want, got)
		}
	}
}

func TestWaitFailOnCrashExitsAfterRenderingResult(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "worker",
		Agent:    "worker",
		Status:   daemon.StatusCrashed,
		PID:      987,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "worker", "--fail-on-crash", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	var rows []waitResult
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode wait fail-on-crash json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "worker" || rows[0].Status != "crashed" {
		t.Fatalf("rows = %+v, want crashed worker row", rows)
	}
	if stderr.Len() != 0 {
		t.Fatalf("wait --fail-on-crash should not write stderr on rendered crash result: %q", stderr.String())
	}
}

func TestLifecycleLatestRejectsExplicitNames(t *testing.T) {
	for _, tc := range []struct {
		args []string
	}{
		{[]string{"start", "manager", "--latest"}},
		{[]string{"stop", "manager", "--latest"}},
		{[]string{"kill", "manager", "--latest"}},
		{[]string{"restart", "manager", "--latest"}},
		{[]string{"wait", "manager", "--latest"}},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), "--latest cannot be combined with instance names") {
			t.Fatalf("%v: stderr = %q, want latest/name validation", tc.args, stderr.String())
		}
	}
}

func TestLifecycleActionRejectsInvalidLatestLastOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"start", "--dry-run", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"start", "--dry-run", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"start", "manager", "--dry-run", "--last", "2"}, "--last cannot be combined with instance names"},
		{[]string{"stop", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"stop", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"stop", "manager", "--last", "2"}, "--last cannot be combined with instance names"},
		{[]string{"kill", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"kill", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"kill", "manager", "--last", "2"}, "--last cannot be combined with instance names"},
		{[]string{"restart", "--dry-run", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"restart", "--dry-run", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"restart", "manager", "--dry-run", "--last", "2"}, "--last cannot be combined with instance names"},
	} {
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

func TestWaitRejectsInvalidLatestLastOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"wait", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"wait", "--latest", "--last", "2"}, "choose one of --latest or --last"},
		{[]string{"wait", "manager", "--last", "2"}, "--last cannot be combined with instance names"},
	} {
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

func TestWaitQuietSuppressesTimeoutMessage(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-wait-quiet-timeout-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	mgr := daemon.NewInstanceManager(root, nil)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      123,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--quiet", "--timeout", "5ms", "--interval", "1ms", "--target", tmp})
	err = cmd.Execute()
	var code ExitCode
	if err == nil || !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want timeout exit 1", err)
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet wait timeout should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestWaitFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"wait", "manager", "--quiet", "--json"}, "choose one of --quiet or --json"},
		{[]string{"wait", "manager", "--quiet", "--summary"}, "choose one of --quiet or --summary"},
		{[]string{"wait", "manager", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined"},
		{[]string{"wait", "manager", "--format", "{{.Instance}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"wait", "manager", "--format", "{{.Instance}}", "--summary"}, "--format cannot be combined"},
		{[]string{"wait", "manager", "--format", "{{"}, "invalid --format template"},
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

func TestWaitAllRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--all", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--all cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitAgentRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--agent", "manager", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--agent cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitStatusRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--status", "running", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--status cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitRuntimeRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--runtime", "codex", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--runtime cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitPhaseRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--phase", "blocked", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--phase cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitStaleRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--stale", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--stale cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitRuntimeStaleRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--runtime-stale", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--runtime-stale cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitUnhealthyRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--unhealthy", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--unhealthy cannot be combined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitRejectsUnknownStatus(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--status", "paused"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --status") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitRejectsUnknownRuntime(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--runtime", "llama"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --runtime") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitRejectsUnknownPhase(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "--phase", "reviewing"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --phase") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitRejectsUnknownUntilPhase(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--until-phase", "reviewing"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --until-phase") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitRejectsUnknownUntil(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--until", "paused"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --until") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestParseWaitUntilAliases(t *testing.T) {
	for _, raw := range []string{"", "terminal", "finished"} {
		got, err := parseWaitUntil(raw)
		if err != nil {
			t.Fatalf("parseWaitUntil(%q): %v", raw, err)
		}
		if got != waitUntilTerminal {
			t.Fatalf("parseWaitUntil(%q) = %q, want terminal", raw, got)
		}
	}
}

func TestWaitNegativeTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--timeout must be >= 0") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitNegativeIntervalFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"wait", "manager", "--interval", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--interval must be >= 0") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWaitForInstancesUnknown(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{}}}
	_, err := waitForInstances(context.Background(), lister, []string{"ghost"}, time.Millisecond)
	var unknown *waitUnknownError
	if !errors.As(err, &unknown) || unknown.Instance != "ghost" {
		t.Fatalf("expected waitUnknownError, got %v", err)
	}
}

func TestWaitForInstancesTimeout(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{
		{{Instance: "mgr", Status: daemon.StatusRunning, PID: 10}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := waitForInstances(ctx, lister, []string{"mgr"}, time.Millisecond)
	var timeoutErr *waitTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected waitTimeoutError, got %v", err)
	}
	if len(timeoutErr.Pending) != 1 || timeoutErr.Pending[0].Instance != "mgr" {
		t.Fatalf("pending = %+v", timeoutErr.Pending)
	}
}
