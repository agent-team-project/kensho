package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/runtimebin"
)

func TestSyncDryRunJSONUsesPlanAndDoesNotStartDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--dry-run", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --json: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode sync dry-run json: %v\nbody=%s", err, out.String())
	}
	if body.Daemon.Running {
		t.Fatalf("daemon should not be running: %+v", body.Daemon)
	}
	if body.Summary.Start != 2 || body.Summary.OnDemand != 7 {
		t.Fatalf("summary = %+v, want two starts and seven on-demand rows", body.Summary)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
	if _, err := os.Stat(daemon.SocketPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon socket, stat err=%v", err)
	}
}

func TestSyncDryRunSummaryJSONUsesPlanAndDoesNotStartDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--dry-run", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --summary --json: %v\nstderr: %s", err, stderr.String())
	}

	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode sync dry-run summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 9 || body.Summary.Actions["start"] != 2 || body.Summary.Actions["on-demand"] != 7 || !body.Summary.DryRun {
		t.Fatalf("summary = %+v, want two starts and two on-demand dry-run rows", body.Summary)
	}
	if body.Summary.Statuses["unknown"] != 9 {
		t.Fatalf("statuses = %+v, want unknown=9", body.Summary.Statuses)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
	if _, err := os.Stat(daemon.SocketPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon socket, stat err=%v", err)
	}
}

func TestSyncDryRunFormatUsesPlanAndDoesNotStartDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--dry-run", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --format: %v\nstderr: %s", err, stderr.String())
	}
	rows := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line != "" {
			rows[line] = true
		}
	}
	for _, want := range []string{
		"manager:start:unknown",
		"ticket-manager:start:unknown",
		"worker:on-demand:unknown",
	} {
		if !rows[want] {
			t.Fatalf("sync --dry-run --format rows missing %q: %q", want, out.String())
		}
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run format should not create daemon pidfile, stat err=%v", err)
	}
}

func TestSyncDryRunFiltersPlanRowsAndDoesNotStartDaemon(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--dry-run",
		"--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
		"--agent", "manager",
		"--status", "unknown",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run filtered: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "feedback-triage:on-demand:unknown\nharness-reviewer:on-demand:unknown\nmanager:start:unknown"; got != want {
		t.Fatalf("sync --dry-run filtered = %q, want %q", got, want)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("filtered dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestSyncDryRunFiltersPlanRowsByPhase(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "blocked"
description = "waiting"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "ticket-manager"), `[status]
phase = "idle"
description = "ready"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--dry-run",
		"--format", "{{.Instance}}:{{.Action}}:{{.Phase}}",
		"--phase", "blocked",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --phase blocked: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "manager:start:blocked"; got != want {
		t.Fatalf("sync --dry-run --phase blocked = %q, want %q", got, want)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("phase-filtered dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestSyncDryRunFiltersPlanRowsByRuntime(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Runtime: string(runtimebin.KindClaude), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp},
		{Instance: "ticket-manager", Agent: "ticket-manager", Runtime: string(runtimebin.KindCodex), Status: daemon.StatusRunning, PID: os.Getpid(), Workspace: tmp},
	} {
		if err := daemon.WriteMetadata(daemonRoot, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--dry-run",
		"--format", "{{.Instance}}:{{.Action}}:{{.Runtime}}",
		"--runtime", "codex",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --runtime codex: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "ticket-manager:keep:codex"; got != want {
		t.Fatalf("sync --dry-run --runtime codex = %q, want %q", got, want)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("runtime-filtered dry-run should not create daemon pidfile, stat err=%v", err)
	}

	invalid := NewRootCmd()
	invalidOut, invalidErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalid.SetOut(invalidOut)
	invalid.SetErr(invalidErr)
	invalid.SetArgs([]string{"sync", "--dry-run", "--runtime", "llama", "--target", tmp})
	if err := invalid.Execute(); err == nil {
		t.Fatalf("sync --runtime llama succeeded\nstdout=%s\nstderr=%s", invalidOut.String(), invalidErr.String())
	}
	if !strings.Contains(invalidErr.String(), "unknown --runtime") {
		t.Fatalf("invalid runtime stderr = %q", invalidErr.String())
	}
}

func TestSyncDryRunFiltersPlanRowsByAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--dry-run",
		"--format", "{{.Instance}}:{{.Action}}",
		"--agent", "manager",
		"--action", "start",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --action start: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "manager:start"; got != want {
		t.Fatalf("sync --dry-run --action start = %q, want %q", got, want)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("action-filtered dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestSyncDryRunStopExtrasMarksRunningExtra(t *testing.T) {
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
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--dry-run", "--stop-extras", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --stop-extras --format: %v\nstderr: %s", err, stderr.String())
	}
	rows := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line != "" {
			rows[line] = true
		}
	}
	if !rows["adhoc:stop:running"] {
		t.Fatalf("sync dry-run rows missing adhoc stop action: %q", out.String())
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestSyncDryRunCommandsPrintsApplyCommand(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "adhoc",
		Agent:    "worker",
		Runtime:  string(runtimebin.KindCodex),
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--target", tmp, "--dry-run", "--stop-extras", "--runtime", "codex", "--action", "stop", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --commands: %v\nstderr: %s", err, stderr.String())
	}
	want := "agent-team sync --repo " + tmp + " --stop-extras --runtime codex --action stop"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("sync --dry-run --commands = %q, want %q", got, want)
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("commands dry-run should not create daemon pidfile, stat err=%v", err)
	}

	noAction := NewRootCmd()
	noActionOut, noActionErr := &bytes.Buffer{}, &bytes.Buffer{}
	noAction.SetOut(noActionOut)
	noAction.SetErr(noActionErr)
	noAction.SetArgs([]string{"sync", "--target", tmp, "--dry-run", "--action", "keep", "--commands"})
	if err := noAction.Execute(); err != nil {
		t.Fatalf("sync --dry-run --commands no actionable rows: %v\nstderr: %s", err, noActionErr.String())
	}
	if got := strings.TrimSpace(noActionOut.String()); got != "" {
		t.Fatalf("sync --dry-run --commands with no actionable rows = %q, want empty", got)
	}

	rootScoped := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScoped.SetOut(rootScopedOut)
	rootScoped.SetErr(rootScopedErr)
	rootScoped.SetArgs([]string{"--repo", tmp, "sync", "--dry-run", "--stop-extras", "--runtime", "codex", "--action", "stop", "--commands"})
	if err := rootScoped.Execute(); err != nil {
		t.Fatalf("sync root --repo --dry-run --commands: %v\nstderr: %s", err, rootScopedErr.String())
	}
	if got := strings.TrimSpace(rootScopedOut.String()); got != want {
		t.Fatalf("sync root --repo --dry-run --commands = %q, want %q", got, want)
	}
}

func TestSyncQuietDryRunSuppressesOutput(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--dry-run", "--quiet", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --quiet: %v\nstderr: %s", err, stderr.String())
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet sync dry-run should not write output, stdout=%q stderr=%q", out.String(), stderr.String())
	}
	if _, err := os.Stat(daemon.PidPath(teamDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create daemon pidfile, stat err=%v", err)
	}
}

func TestSyncFormatPrintsActionRows(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-format-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	pid := os.Getpid()
	for _, name := range []string{"manager", "ticket-manager"} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: name,
			Agent:    name,
			Status:   daemon.StatusRunning,
			PID:      pid,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", name, err)
		}
	}
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --format: %v\nstderr: %s", err, stderr.String())
	}
	rows := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line != "" {
			rows[line] = true
		}
	}
	for _, want := range []string{
		"manager:skip:running",
		"ticket-manager:skip:running",
	} {
		if !rows[want] {
			t.Fatalf("sync --format rows missing %q: %q", want, out.String())
		}
	}
}

func TestSyncReportsUnsupportedCodexResumeWithoutCallingDaemonStart(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-codex-unsupported-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:      "manager",
		Agent:         "manager",
		Status:        daemon.StatusStopped,
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: runtimebin.DefaultBinaryForKind(runtimebin.KindCodex),
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance: "ticket-manager",
		Agent:    "ticket-manager",
		Status:   daemon.StatusRunning,
		PID:      os.Getpid(),
	}); err != nil {
		t.Fatalf("write ticket-manager metadata: %v", err)
	}
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--action", "unsupported", "--format", "{{.Instance}}:{{.Action}}:{{.Status}}:{{.Detail}}", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --action unsupported --format: %v\nstderr: %s", err, stderr.String())
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasPrefix(got, "manager:unsupported:stopped:") {
		t.Fatalf("sync output = %q, want manager unsupported row", got)
	}
	if !strings.Contains(got, `supports managed resume but no session id is recorded`) {
		t.Fatalf("sync output = %q, want missing-session Codex limitation", got)
	}
	for _, want := range []string{
		`agent-team logs manager --follow`,
		`agent-team logs manager --last-message`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("sync output = %q, want %q", got, want)
		}
	}
	meta := metadataByInstanceForTest(mgr.List(), "manager")
	if meta == nil || meta.Status != daemon.StatusStopped || meta.PID != 0 {
		t.Fatalf("manager metadata = %+v, want still stopped without daemon start", meta)
	}
}

func TestSyncFormatHonorsAgentFilter(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-filter-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	pid := os.Getpid()
	for _, name := range []string{"manager", "ticket-manager"} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: name,
			Agent:    name,
			Status:   daemon.StatusRunning,
			PID:      pid,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", name, err)
		}
	}
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--agent", "manager",
		"--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --agent manager --format: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "manager:skip:running"; got != want {
		t.Fatalf("sync --agent manager rows = %q, want %q", got, want)
	}
}

func TestSyncFormatHonorsPhaseFilter(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-phase-filter-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	pid := os.Getpid()
	for _, name := range []string{"manager", "ticket-manager"} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: name,
			Agent:    name,
			Status:   daemon.StatusRunning,
			PID:      pid,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", name, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "blocked"
description = "waiting"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "ticket-manager"), `[status]
phase = "idle"
description = "ready"
`, time.Time{})
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--phase", "blocked",
		"--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --phase blocked --format: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "manager:skip:running"; got != want {
		t.Fatalf("sync --phase blocked rows = %q, want %q", got, want)
	}
}

func TestSyncFormatHonorsActionFilter(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-action-filter-")
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
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--action", "keep",
		"--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --action keep --format: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := strings.TrimSpace(out.String()), "manager:skip:running"; got != want {
		t.Fatalf("sync --action keep rows = %q, want %q", got, want)
	}
}

func TestSyncWaitJSONHonorsAgentFilterHealth(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-filter-wait-")
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
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--agent", "manager",
		"--wait",
		"--timeout", "50ms",
		"--json",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --agent manager --wait --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var body lifecycleHealthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode sync filtered wait json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || !body.Health.Healthy {
		t.Fatalf("filtered wait health = %+v, want healthy manager-only health", body.Health)
	}
	if len(body.Actions) != 1 || body.Actions[0].Instance != "manager" || body.Actions[0].Action != "skip" {
		t.Fatalf("filtered wait actions = %+v, want manager skip only", body.Actions)
	}
	if body.Health.Declared.Persistent != 1 || body.Health.Declared.Running != 1 || body.Health.Declared.Missing != 0 {
		t.Fatalf("filtered wait declared health = %+v, want only manager declared", body.Health.Declared)
	}
}

func TestSyncWaitTimeoutReportsUnhealthyFleet(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-wait-timeout-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	for _, spec := range []struct {
		name  string
		agent string
	}{
		{name: "manager", agent: "manager"},
		{name: "ticket-manager", agent: "ticket-manager"},
	} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: spec.name,
			Agent:    spec.agent,
			Status:   daemon.StatusRunning,
			PID:      os.Getpid(),
		}); err != nil {
			t.Fatalf("write metadata %s: %v", spec.name, err)
		}
	}
	old := time.Now().Add(-staleAfter - time.Minute)
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "implementing"
description = "stale work"
`, old)
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--wait",
		"--timeout", "5ms",
		"--json",
		"--target", tmp,
	})
	err = cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var body lifecycleHealthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode sync wait timeout json: %v\nbody=%s", err, out.String())
	}
	if body.Health == nil || body.Health.Healthy {
		t.Fatalf("sync wait health = %+v, want unhealthy timeout snapshot", body.Health)
	}
	if !strings.Contains(stderr.String(), "wait timed out before selected instances became healthy") {
		t.Fatalf("stderr = %q, want lifecycle wait timeout message", stderr.String())
	}
}

func TestSyncStopExtrasJSONStopsUndeclaredRunningInstances(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-stop-extras-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	sleepers := map[string]*exec.Cmd{}
	for name, agent := range map[string]string{
		"manager":        "manager",
		"ticket-manager": "ticket-manager",
		"adhoc":          "worker",
		"worker-abc123":  "worker",
	} {
		sleepers[name] = startSleepMetadataForSyncTest(t, root, tmp, name, agent)
	}
	defer func() {
		for _, sleeper := range sleepers {
			_ = sleeper.Process.Kill()
			_ = sleeper.Wait()
		}
	}()
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--stop-extras", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --stop-extras --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var actions []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &actions); err != nil {
		t.Fatalf("decode sync --stop-extras json: %v\nbody=%s", err, out.String())
	}
	rows := map[string]bool{}
	for _, action := range actions {
		rows[action.Instance+":"+action.Action+":"+action.Status] = true
	}
	for _, want := range []string{
		"adhoc:stop:stopped",
		"manager:skip:running",
		"ticket-manager:skip:running",
	} {
		if !rows[want] {
			t.Fatalf("sync --stop-extras actions missing %q: %+v", want, actions)
		}
	}
	for key := range rows {
		if strings.HasPrefix(key, "worker-abc123:") {
			t.Fatalf("sync --stop-extras should not act on declared ephemeral child: %+v", actions)
		}
	}
	_ = sleepers["adhoc"].Wait()
	delete(sleepers, "adhoc")
	meta := metadataByInstanceForTest(mgr.List(), "adhoc")
	if meta == nil || meta.Status != daemon.StatusStopped {
		t.Fatalf("adhoc metadata = %+v, want stopped", meta)
	}
	if manager := metadataByInstanceForTest(mgr.List(), "manager"); manager == nil || manager.Status != daemon.StatusRunning {
		t.Fatalf("manager metadata = %+v, want still running", manager)
	}
	if worker := metadataByInstanceForTest(mgr.List(), "worker-abc123"); worker == nil || worker.Status != daemon.StatusRunning {
		t.Fatalf("worker child metadata = %+v, want still running", worker)
	}
}

func TestSyncStopExtrasSummaryJSONCountsActions(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-stop-extras-summary-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	sleepers := map[string]*exec.Cmd{}
	for name, agent := range map[string]string{
		"manager":        "manager",
		"ticket-manager": "ticket-manager",
		"adhoc":          "worker",
	} {
		sleepers[name] = startSleepMetadataForSyncTest(t, root, tmp, name, agent)
	}
	defer func() {
		for _, sleeper := range sleepers {
			_ = sleeper.Process.Kill()
			_ = sleeper.Wait()
		}
	}()
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--stop-extras", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --stop-extras --summary --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode sync --stop-extras summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 3 || body.Summary.Actions["stop"] != 1 || body.Summary.Actions["skip"] != 2 {
		t.Fatalf("summary = %+v, want one stop and two skips", body.Summary)
	}
	if body.Summary.Statuses["stopped"] != 1 || body.Summary.Statuses["running"] != 2 {
		t.Fatalf("statuses = %+v, want stopped=1 running=2", body.Summary.Statuses)
	}
	_ = sleepers["adhoc"].Wait()
	delete(sleepers, "adhoc")
	meta := metadataByInstanceForTest(mgr.List(), "adhoc")
	if meta == nil || meta.Status != daemon.StatusStopped {
		t.Fatalf("adhoc metadata = %+v, want stopped", meta)
	}
}

func TestSyncStopExtrasHonorsAgentFilter(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-stop-extras-filter-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	sleepers := map[string]*exec.Cmd{}
	for name, agent := range map[string]string{
		"manager":        "manager",
		"ticket-manager": "ticket-manager",
		"adhoc-manager":  "manager",
		"adhoc-worker":   "worker",
	} {
		sleepers[name] = startSleepMetadataForSyncTest(t, root, tmp, name, agent)
	}
	defer func() {
		for _, sleeper := range sleepers {
			_ = sleeper.Process.Kill()
			_ = sleeper.Wait()
		}
	}()
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--stop-extras", "--agent", "manager", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --stop-extras --agent manager --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var actions []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &actions); err != nil {
		t.Fatalf("decode sync --stop-extras filtered json: %v\nbody=%s", err, out.String())
	}
	rows := map[string]bool{}
	for _, action := range actions {
		rows[action.Instance+":"+action.Action+":"+action.Status] = true
	}
	for _, want := range []string{
		"adhoc-manager:stop:stopped",
		"manager:skip:running",
	} {
		if !rows[want] {
			t.Fatalf("sync --stop-extras filtered actions missing %q: %+v", want, actions)
		}
	}
	for _, unwanted := range []string{
		"adhoc-worker:stop:stopped",
		"ticket-manager:skip:running",
	} {
		if rows[unwanted] {
			t.Fatalf("sync --stop-extras filtered actions included %q: %+v", unwanted, actions)
		}
	}
	_ = sleepers["adhoc-manager"].Wait()
	delete(sleepers, "adhoc-manager")
	if extra := metadataByInstanceForTest(mgr.List(), "adhoc-manager"); extra == nil || extra.Status != daemon.StatusStopped {
		t.Fatalf("adhoc-manager metadata = %+v, want stopped", extra)
	}
	if worker := metadataByInstanceForTest(mgr.List(), "adhoc-worker"); worker == nil || worker.Status != daemon.StatusRunning {
		t.Fatalf("adhoc-worker metadata = %+v, want still running", worker)
	}
	if ticketManager := metadataByInstanceForTest(mgr.List(), "ticket-manager"); ticketManager == nil || ticketManager.Status != daemon.StatusRunning {
		t.Fatalf("ticket-manager metadata = %+v, want still running", ticketManager)
	}
}

func TestSyncStopExtrasHonorsPhaseFilter(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-stop-extras-phase-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	sleepers := map[string]*exec.Cmd{}
	for name, agent := range map[string]string{
		"manager":       "manager",
		"adhoc-blocked": "worker",
		"adhoc-idle":    "worker",
	} {
		sleepers[name] = startSleepMetadataForSyncTest(t, root, tmp, name, agent)
	}
	defer func() {
		for _, sleeper := range sleepers {
			_ = sleeper.Process.Kill()
			_ = sleeper.Wait()
		}
	}()
	writeStatus(t, filepath.Join(teamDir, "state", "adhoc-blocked"), `[status]
phase = "blocked"
description = "waiting"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "adhoc-idle"), `[status]
phase = "idle"
description = "ready"
`, time.Time{})
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--stop-extras", "--phase", "blocked", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --stop-extras --phase blocked --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var actions []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &actions); err != nil {
		t.Fatalf("decode sync --stop-extras phase json: %v\nbody=%s", err, out.String())
	}
	rows := map[string]bool{}
	for _, action := range actions {
		rows[action.Instance+":"+action.Action+":"+action.Status] = true
	}
	if !rows["adhoc-blocked:stop:stopped"] {
		t.Fatalf("sync --stop-extras --phase actions missing blocked stop: %+v", actions)
	}
	if rows["adhoc-idle:stop:stopped"] || rows["manager:skip:running"] {
		t.Fatalf("sync --stop-extras --phase included non-blocked rows: %+v", actions)
	}
	_ = sleepers["adhoc-blocked"].Wait()
	delete(sleepers, "adhoc-blocked")
	if blocked := metadataByInstanceForTest(mgr.List(), "adhoc-blocked"); blocked == nil || blocked.Status != daemon.StatusStopped {
		t.Fatalf("adhoc-blocked metadata = %+v, want stopped", blocked)
	}
	if idle := metadataByInstanceForTest(mgr.List(), "adhoc-idle"); idle == nil || idle.Status != daemon.StatusRunning {
		t.Fatalf("adhoc-idle metadata = %+v, want still running", idle)
	}
}

func TestSyncStopExtrasHonorsActionFilter(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-stop-extras-action-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	sleepers := map[string]*exec.Cmd{}
	for name, agent := range map[string]string{
		"manager": "manager",
		"adhoc":   "worker",
	} {
		sleepers[name] = startSleepMetadataForSyncTest(t, root, tmp, name, agent)
	}
	defer func() {
		for _, sleeper := range sleepers {
			_ = sleeper.Process.Kill()
			_ = sleeper.Wait()
		}
	}()
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--stop-extras", "--action", "keep", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --stop-extras --action keep --json: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	var actions []lifecycleActionResult
	if err := json.Unmarshal(out.Bytes(), &actions); err != nil {
		t.Fatalf("decode sync --stop-extras action json: %v\nbody=%s", err, out.String())
	}
	rows := map[string]bool{}
	for _, action := range actions {
		rows[action.Instance+":"+action.Action+":"+action.Status] = true
	}
	if !rows["manager:skip:running"] {
		t.Fatalf("sync --stop-extras --action keep missing manager keep: %+v", actions)
	}
	if rows["adhoc:stop:stopped"] {
		t.Fatalf("sync --stop-extras --action keep stopped adhoc: %+v", actions)
	}
	if extra := metadataByInstanceForTest(mgr.List(), "adhoc"); extra == nil || extra.Status != daemon.StatusRunning {
		t.Fatalf("adhoc metadata = %+v, want still running", extra)
	}
}

func TestSyncWaitFormatPrintsActionRowsAfterHealthWait(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "agent-team-sync-wait-format-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	pid := os.Getpid()
	for _, name := range []string{"manager", "ticket-manager"} {
		if err := daemon.WriteMetadata(root, &daemon.Metadata{
			Instance: name,
			Agent:    name,
			Status:   daemon.StatusRunning,
			PID:      pid,
		}); err != nil {
			t.Fatalf("write metadata %s: %v", name, err)
		}
	}
	mgr := daemon.NewInstanceManager(root, nil)
	if err := mgr.LoadFromDisk(); err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	cleanup := startRunTestDaemon(t, teamDir, mgr)
	defer cleanup()

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"sync",
		"--wait",
		"--timeout", "2s",
		"--format", "{{.Instance}}:{{.Action}}:{{.Status}}",
		"--target", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --wait --format: %v\nstdout=%s\nstderr=%s", err, out.String(), stderr.String())
	}
	rows := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line != "" {
			rows[line] = true
		}
	}
	for _, want := range []string{
		"manager:skip:running",
		"ticket-manager:skip:running",
	} {
		if !rows[want] {
			t.Fatalf("sync --wait --format rows missing %q: %q", want, out.String())
		}
	}
}

func TestSyncFormatRejectsConflictingModes(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"sync", "--quiet", "--json"}, "choose one of --quiet or --json"},
		{[]string{"sync", "--quiet", "--summary"}, "choose one of --quiet or --summary"},
		{[]string{"sync", "--format", "{{.Instance}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"sync", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined"},
		{[]string{"sync", "--format", "{{.Instance}}", "--summary"}, "--format cannot be combined"},
		{[]string{"sync", "--format", "{{"}, "invalid --format template"},
		{[]string{"sync", "--commands"}, wantCommandsModeRequiresDryRun()},
		{[]string{"sync", "--dry-run", "--commands", "--json"}, wantCommandsModeConflict("--json")},
		{[]string{"sync", "--dry-run", "--commands", "--summary"}, wantCommandsModeConflict("--summary")},
		{[]string{"sync", "--dry-run", "--commands", "--quiet"}, wantCommandsModeConflict("--quiet")},
		{[]string{"sync", "--dry-run", "--commands", "--format", "{{.Instance}}"}, wantCommandsModeConflict("--format")},
		{[]string{"sync", "--status", "paused"}, "unknown --status"},
		{[]string{"sync", "--status", "  "}, "non-empty status"},
		{[]string{"sync", "--phase", "reviewing"}, "unknown --phase"},
		{[]string{"sync", "--phase", "  "}, "non-empty phase"},
		{[]string{"sync", "--action", "pause"}, "unknown --action"},
		{[]string{"sync", "--action", "  "}, "non-empty action"},
		{[]string{"sync", "--agent", "  "}, "non-empty agent"},
		{[]string{"sync", "--instance", "  "}, "non-empty instance"},
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

func TestSyncDryRunRejectsWait(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--dry-run", "--wait"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected dry-run wait validation error")
	}
	if !strings.Contains(stderr.String(), "--dry-run cannot be combined with --wait") {
		t.Fatalf("stderr = %q, want dry-run wait validation", stderr.String())
	}
}

func TestSyncNegativeTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--timeout must be >= 0") {
		t.Fatalf("stderr = %q, want timeout validation", stderr.String())
	}
}

func TestSyncNegativeReadyTimeoutFailsFast(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--ready-timeout", "-1s"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected ready-timeout validation error")
	}
	if !strings.Contains(stderr.String(), "--ready-timeout must be >= 0") {
		t.Fatalf("stderr = %q, want ready-timeout validation", stderr.String())
	}
}

func metadataByInstanceForTest(metas []*daemon.Metadata, name string) *daemon.Metadata {
	for _, meta := range metas {
		if meta.Instance == name {
			return meta
		}
	}
	return nil
}

func startSleepMetadataForSyncTest(t *testing.T, root, workspace, name, agent string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.Dir = workspace
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep for %s: %v", name, err)
	}
	if err := daemon.WriteMetadata(root, &daemon.Metadata{
		Instance:  name,
		Agent:     agent,
		Workspace: workspace,
		Status:    daemon.StatusRunning,
		PID:       cmd.Process.Pid,
	}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("write metadata %s: %v", name, err)
	}
	return cmd
}
