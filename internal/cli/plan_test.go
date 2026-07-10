package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
)

func TestPlanJSONShowsTopologyAndDaemonMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writePlanShapeTopologyFixture(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		SessionID: "sid-manager",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "adhoc",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		SessionID: "sid-adhoc",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write adhoc metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --json: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode plan json: %v\nbody=%s", err, out.String())
	}
	if body.Daemon.Running {
		t.Fatalf("daemon should be reported down: %+v", body.Daemon)
	}
	if body.Summary.Total != 7 || body.Summary.Start != 1 || body.Summary.Resume != 1 || body.Summary.OnDemand != 4 || body.Summary.Extra != 1 {
		t.Fatalf("summary = %+v, want fixture start/resume/on-demand/extra counts", body.Summary)
	}
	byName := map[string]planRow{}
	for _, row := range body.Instances {
		byName[row.Instance] = row
	}
	if row := byName["manager"]; row.Kind != "persistent" || row.Status != "stopped" || row.Action != "resume" {
		t.Fatalf("manager row = %+v, want stopped persistent resume", row)
	} else if row.Phase != "unknown" {
		t.Fatalf("manager phase = %q, want unknown", row.Phase)
	}
	if row := byName["ticket-manager"]; row.Kind != "persistent" || row.Status != "unknown" || row.Action != "start" {
		t.Fatalf("ticket-manager row = %+v, want unknown persistent start", row)
	}
	if row := byName["worker"]; row.Kind != "ephemeral" || row.Action != "on-demand" {
		t.Fatalf("worker row = %+v, want ephemeral on-demand", row)
	}
	if row := byName["reviewer"]; row.Kind != "ephemeral" || row.Action != "on-demand" {
		t.Fatalf("reviewer row = %+v, want ephemeral on-demand", row)
	}
	if row := byName["adhoc"]; row.Kind != "extra" || row.Action != "extra" || row.Status != "stopped" {
		t.Fatalf("adhoc row = %+v, want stopped extra", row)
	}
}

func TestPlanBundledFullProfileTopologyCanary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --summary --json: %v\nstderr: %s", err, stderr.String())
	}

	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode bundled plan summary json: %v\nbody=%s", err, out.String())
	}
	// This is the one Go canary for the bundled template's full topology.
	// Other exact plan-shape tests overwrite instances.toml with a local
	// fixture so adding a bundled instance only updates this test.
	if body.Summary.Total != 23 || body.Summary.Actions["start"] != 5 || body.Summary.Actions["on-demand"] != 18 || !body.Summary.DryRun {
		t.Fatalf("bundled topology summary = %+v, want current bundled default shape", body.Summary)
	}
	if body.Summary.Statuses["unknown"] != 23 {
		t.Fatalf("bundled topology statuses = %+v, want unknown=23", body.Summary.Statuses)
	}
}

func TestPlanMarksStoppedCodexMetadataUnsupported(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:      "manager",
		Agent:         "manager",
		Status:        daemon.StatusStopped,
		Runtime:       string(runtimebin.KindCodex),
		RuntimeBinary: runtimebin.DefaultBinaryForKind(runtimebin.KindCodex),
		Workspace:     tmp,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--json", "--action", "unsupported", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --json --action unsupported: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Unsupported != 1 || body.Summary.Resume != 0 {
		t.Fatalf("summary = %+v, want one unsupported row", body.Summary)
	}
	if len(body.Instances) != 1 {
		t.Fatalf("instances = %+v, want manager only", body.Instances)
	}
	row := body.Instances[0]
	if row.Instance != "manager" || row.Action != lifecycleActionUnsupported || row.Status != string(daemon.StatusStopped) {
		t.Fatalf("row = %+v, want stopped unsupported manager", row)
	}
	if !strings.Contains(row.Detail, `supports managed resume but no session id is recorded`) {
		t.Fatalf("detail = %q, want missing-session Codex limitation", row.Detail)
	}
	for _, want := range []string{
		`agent-team logs manager --follow`,
		`agent-team logs manager --last-message`,
	} {
		if !strings.Contains(row.Detail, want) {
			t.Fatalf("detail = %q, want %q", row.Detail, want)
		}
	}
}

func TestPlanShowsRestartPolicyAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"
restart = "on-failure"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusCrashed,
	}); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--json", "--action", "restart", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --action restart: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Restart != 1 {
		t.Fatalf("summary = %+v, want one restart row", body.Summary)
	}
	if got := body.Instances[0]; got.Instance != "manager" || got.Action != "restart" || !strings.Contains(got.Detail, `restart policy "on-failure"`) {
		t.Fatalf("row = %+v, want restart policy detail", got)
	}
}

func TestSyncDryRunShowsRestartPolicyAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"
restart = "always"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	exit0 := 0
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusExited,
		ExitCode: &exit0,
	}); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"sync", "--dry-run", "--json", "--action", "restart", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --action restart: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode sync json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Restart != 1 {
		t.Fatalf("summary = %+v, want one restart row", body.Summary)
	}
	if got := body.Instances[0]; got.Instance != "manager" || got.Action != "restart" || !strings.Contains(got.Detail, `restart policy "always"`) {
		t.Fatalf("row = %+v, want restart policy detail", got)
	}
}

func TestPlanFiltersRowsByRuntime(t *testing.T) {
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
	cmd.SetArgs([]string{"plan", "--json", "--runtime", "codex", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --runtime codex: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || len(body.Instances) != 1 || body.Instances[0].Instance != "ticket-manager" {
		t.Fatalf("runtime-filtered plan = %+v", body)
	}
}

func TestPlanSummaryJSONShowsAggregateCounts(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writePlanShapeTopologyFixture(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--summary", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --summary --json: %v\nstderr: %s", err, stderr.String())
	}

	var body lifecycleActionSummaryResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode plan summary json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 6 || body.Summary.Actions["start"] != 2 || body.Summary.Actions["on-demand"] != 4 || !body.Summary.DryRun {
		t.Fatalf("summary = %+v, want fixture start/on-demand previews", body.Summary)
	}
	if body.Summary.Statuses["unknown"] != 6 {
		t.Fatalf("statuses = %+v, want unknown=6", body.Summary.Statuses)
	}
}

func TestPlanStopExtrasMarksOnlyRunningExtras(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writePlanShapeTopologyFixture(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "adhoc-running",
		Agent:     "manager",
		Status:    daemon.StatusRunning,
		PID:       os.Getpid(),
		SessionID: "sid-running",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write running adhoc metadata: %v", err)
	}
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "adhoc-stopped",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		SessionID: "sid-stopped",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write stopped adhoc metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--stop-extras", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --stop-extras --json: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 8 || body.Summary.Stop != 1 || body.Summary.Extra != 1 {
		t.Fatalf("summary = %+v, want one stop preview and one remaining extra", body.Summary)
	}
	byName := map[string]planRow{}
	for _, row := range body.Instances {
		byName[row.Instance] = row
	}
	if row := byName["adhoc-running"]; row.Kind != "extra" || row.Status != "running" || row.Action != "stop" {
		t.Fatalf("running adhoc row = %+v, want extra/running/stop", row)
	}
	if row := byName["adhoc-stopped"]; row.Kind != "extra" || row.Status != "stopped" || row.Action != "extra" {
		t.Fatalf("stopped adhoc row = %+v, want extra/stopped/extra", row)
	}
}

func TestPlanStopExtrasKeepsDeclaredEphemeralChildren(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "worker-abc123",
		Agent:     "worker",
		Status:    daemon.StatusRunning,
		PID:       os.Getpid(),
		SessionID: "sid-worker",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write worker child metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--stop-extras", "--json", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --stop-extras --json: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Keep != 1 || body.Summary.Stop != 0 || body.Summary.Extra != 0 {
		t.Fatalf("summary = %+v, want ephemeral child kept without stop/extra", body.Summary)
	}
	byName := map[string]planRow{}
	for _, row := range body.Instances {
		byName[row.Instance] = row
	}
	if row := byName["worker-abc123"]; row.Kind != "ephemeral" || row.Action != "keep" || !strings.Contains(row.Detail, "worker") {
		t.Fatalf("worker child row = %+v, want ephemeral keep", row)
	}
}

func TestPlanJSONFiltersByPhase(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `[status]
phase = "blocked"
description = "waiting"
`, time.Time{})
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `[status]
phase = "idle"
description = "ready"
`, time.Time{})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--json", "--phase", "blocked", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --json --phase blocked: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode filtered plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Start != 1 {
		t.Fatalf("summary = %+v, want only blocked manager start row", body.Summary)
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "manager" || body.Instances[0].Phase != "blocked" {
		t.Fatalf("instances = %+v, want blocked manager only", body.Instances)
	}
}

func TestPlanJSONFiltersByAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		SessionID: "sid-manager",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--json", "--action", "start", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --json --action start: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode action-filtered plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 4 || body.Summary.Start != 4 {
		t.Fatalf("summary = %+v, want four start rows", body.Summary)
	}
	if len(body.Instances) != 4 || body.Instances[0].Instance != "advisor" || body.Instances[1].Instance != "research-auditor" || body.Instances[2].Instance != "research-manager" || body.Instances[3].Instance != "ticket-manager" {
		t.Fatalf("instances = %+v, want all stopped persistent starts", body.Instances)
	}
}

func TestPlanStopExtrasFiltersByAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "adhoc",
		Agent:     "worker",
		Status:    daemon.StatusRunning,
		PID:       os.Getpid(),
		SessionID: "sid-adhoc",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write adhoc metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--stop-extras", "--json", "--action", "stop", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --stop-extras --json --action stop: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode stop-action plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Stop != 1 {
		t.Fatalf("summary = %+v, want one stop row", body.Summary)
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "adhoc" || body.Instances[0].Action != "stop" {
		t.Fatalf("instances = %+v, want adhoc stop only", body.Instances)
	}
}

func TestPlanJSONFiltersByAgentAndStatus(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		SessionID: "sid-manager",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "adhoc",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		SessionID: "sid-adhoc",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write adhoc metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--json", "--agent", "manager", "--status", "stopped", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --json with filters: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode filtered plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 2 || body.Summary.Resume != 1 || body.Summary.Extra != 1 || body.Summary.Start != 0 || body.Summary.OnDemand != 0 {
		t.Fatalf("summary = %+v, want only stopped manager rows", body.Summary)
	}
	byName := map[string]planRow{}
	for _, row := range body.Instances {
		byName[row.Instance] = row
	}
	if _, ok := byName["manager"]; !ok {
		t.Fatalf("filtered plan missing manager row: %+v", body.Instances)
	}
	if _, ok := byName["adhoc"]; !ok {
		t.Fatalf("filtered plan missing adhoc row: %+v", body.Instances)
	}
	if _, ok := byName["ticket-manager"]; ok {
		t.Fatalf("filtered plan included ticket-manager row: %+v", body.Instances)
	}
	if _, ok := byName["worker"]; ok {
		t.Fatalf("filtered plan included worker row: %+v", body.Instances)
	}
}

func TestPlanJSONFiltersByInstance(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		SessionID: "sid-manager",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--json", "--instance", "manager", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --json --instance: %v\nstderr: %s", err, stderr.String())
	}

	var body planResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode filtered plan json: %v\nbody=%s", err, out.String())
	}
	if body.Summary.Total != 1 || body.Summary.Resume != 1 {
		t.Fatalf("summary = %+v, want only manager resume", body.Summary)
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "manager" {
		t.Fatalf("instances = %+v, want manager only", body.Instances)
	}
}

func TestPlanFormatPrintsFilteredRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	daemonRoot := daemon.DaemonRoot(teamDir)
	if err := daemon.WriteMetadata(daemonRoot, &daemon.Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Status:    daemon.StatusStopped,
		SessionID: "sid-manager",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write manager metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--format", "{{.Instance}}:{{.Agent}}:{{.Status}}:{{.Action}}", "--agent", "manager", "--status", "stopped", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --format: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := out.String(), "manager:manager:stopped:resume\n"; got != want {
		t.Fatalf("formatted plan = %q, want %q", got, want)
	}
}

func TestPlanCommandsPrintsFilteredSyncPreview(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance:  "adhoc",
		Agent:     "worker",
		Runtime:   string(runtimebin.KindCodex),
		Status:    daemon.StatusRunning,
		PID:       os.Getpid(),
		SessionID: "sid-adhoc",
		Workspace: tmp,
	}); err != nil {
		t.Fatalf("write adhoc metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--target", tmp, "--stop-extras", "--runtime", "codex", "--action", "stop", "--commands"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --commands: %v\nstderr: %s", err, stderr.String())
	}
	want := "agent-team sync --repo " + tmp + " --dry-run --stop-extras --runtime codex --action stop"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("plan --commands = %q, want %q", got, want)
	}

	rootScoped := NewRootCmd()
	rootScopedOut, rootScopedErr := &bytes.Buffer{}, &bytes.Buffer{}
	rootScoped.SetOut(rootScopedOut)
	rootScoped.SetErr(rootScopedErr)
	rootScoped.SetArgs([]string{"--repo", tmp, "plan", "--stop-extras", "--runtime", "codex", "--action", "stop", "--commands"})
	if err := rootScoped.Execute(); err != nil {
		t.Fatalf("plan root --repo --commands: %v\nstderr: %s", err, rootScopedErr.String())
	}
	if got := strings.TrimSpace(rootScopedOut.String()); got != want {
		t.Fatalf("plan root --repo --commands = %q, want %q", got, want)
	}

	noAction := NewRootCmd()
	noActionOut, noActionErr := &bytes.Buffer{}, &bytes.Buffer{}
	noAction.SetOut(noActionOut)
	noAction.SetErr(noActionErr)
	noAction.SetArgs([]string{"plan", "--target", tmp, "--action", "keep", "--commands"})
	if err := noAction.Execute(); err != nil {
		t.Fatalf("plan --commands no actionable rows: %v\nstderr: %s", err, noActionErr.String())
	}
	if got := strings.TrimSpace(noActionOut.String()); got != "" {
		t.Fatalf("plan --commands with no actionable rows = %q, want empty", got)
	}
}

func TestPlanFormatRejectsJSONAndInvalidTemplate(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"plan", "--format", "{{.Instance}}", "--json"}, "--format cannot be combined"},
		{[]string{"plan", "--format", "{{.Instance}}", "--summary"}, "--format cannot be combined"},
		{[]string{"plan", "--format", "{{"}, "invalid --format template"},
		{[]string{"plan", "--commands", "--json"}, wantCommandsModeConflict("--json")},
		{[]string{"plan", "--commands", "--summary"}, wantCommandsModeConflict("--summary")},
		{[]string{"plan", "--commands", "--format", "{{.Instance}}"}, wantCommandsModeConflict("--format")},
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

func TestPlanTextRendersTableAndSummary(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	writePlanShapeTopologyFixture(t, tmp)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--target", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan: %v\nstderr: %s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{
		"daemon: not running",
		"INSTANCE",
		"manager",
		"ticket-manager",
		"worker",
		"start",
		"on-demand",
		"summary: total=6 start=2 resume=0 keep=0 on-demand=4 extra=0",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("plan output missing %q:\n%s", want, body)
		}
	}
}

func TestPlanRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"plan", "--status", "paused"}, `unknown --status "paused"`},
		{[]string{"plan", "--phase", "reviewing"}, `unknown --phase "reviewing"`},
		{[]string{"plan", "--phase", "  "}, "non-empty phase"},
		{[]string{"plan", "--action", "pause"}, `unknown --action "pause"`},
		{[]string{"plan", "--action", "  "}, "non-empty action"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(stderr)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("%v succeeded unexpectedly; stdout=%s stderr=%s", tc.args, out.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}
