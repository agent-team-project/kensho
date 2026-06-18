package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
)

func TestHealthHealthyDeclaredPersistent(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager": {Name: "manager", Agent: "manager"},
		"worker":  {Name: "worker", Agent: "worker", Ephemeral: true},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 10},
	}
	got := buildHealth(true, 123, rows, topo, now)
	if !got.Healthy {
		t.Fatalf("health should be healthy: %+v", got.Issues)
	}
	if got.Declared.Persistent != 1 || got.Declared.Running != 1 || got.Declared.Missing != 0 {
		t.Fatalf("declared summary = %+v", got.Declared)
	}
	if got.Summary.Running != 1 || got.Summary.Total != 1 {
		t.Fatalf("summary = %+v", got.Summary)
	}
}

func TestHealthDaemonDownIsUnhealthy(t *testing.T) {
	got := buildHealth(false, 0, nil, nil, time.Now())
	if got.Healthy {
		t.Fatalf("daemon-down health should be unhealthy")
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != "daemon_not_running" {
		t.Fatalf("issues = %+v, want daemon_not_running", got.Issues)
	}
}

func TestHealthDaemonNotReadyIsUnhealthy(t *testing.T) {
	got := buildHealthWithDaemonStatus(daemonStatusJSON{
		Running: true,
		Ready:   false,
		PID:     123,
		Error:   "daemon socket not found",
	}, nil, nil, time.Now(), healthOptions{})
	if got.Healthy {
		t.Fatalf("daemon-not-ready health should be unhealthy")
	}
	if !got.Daemon.Running || got.Daemon.Ready || got.Daemon.Error == "" {
		t.Fatalf("daemon summary = %+v, want running but not ready with error", got.Daemon)
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != "daemon_not_ready" {
		t.Fatalf("issues = %+v, want daemon_not_ready", got.Issues)
	}
}

func TestHealthReportsCrashedStaleAndMissingDeclared(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager":        {Name: "manager", Agent: "manager"},
		"ticket-manager": {Name: "ticket-manager", Agent: "ticket-manager"},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "implementing", Stale: true, PID: 10},
		{Instance: "worker", Agent: "worker", Lifecycle: string(daemon.StatusCrashed), Phase: "unknown", PID: 11},
	}
	got := buildHealth(true, 123, rows, topo, now)
	if got.Healthy {
		t.Fatalf("health should be unhealthy")
	}
	codes := map[string]bool{}
	for _, issue := range got.Issues {
		codes[issue.Code] = true
	}
	for _, want := range []string{"status_stale", "instance_crashed", "declared_missing"} {
		if !codes[want] {
			t.Fatalf("issues = %+v, missing %s", got.Issues, want)
		}
	}
	if got.Declared.Persistent != 2 || got.Declared.Running != 1 || got.Declared.Missing != 1 {
		t.Fatalf("declared summary = %+v", got.Declared)
	}
}

func TestHealthAgentFilterScopesInstanceAndDeclaredIssues(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager":        {Name: "manager", Agent: "manager"},
		"ticket-manager": {Name: "ticket-manager", Agent: "ticket-manager"},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 10},
		{Instance: "ticket-manager", Agent: "ticket-manager", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked", PID: 11},
		{Instance: "worker", Agent: "worker", Lifecycle: string(daemon.StatusCrashed), Phase: "unknown", PID: 12},
	}
	global := buildHealth(true, 123, rows, topo, now)
	if global.Healthy {
		t.Fatalf("global health should see crashed non-manager rows")
	}

	opts, err := newHealthOptions(nil, []string{"manager"}, nil, false)
	if err != nil {
		t.Fatalf("newHealthOptions: %v", err)
	}
	scoped := buildHealthWithOptions(true, 123, rows, topo, now, opts)
	if !scoped.Healthy {
		t.Fatalf("manager-scoped health should ignore non-manager issues: %+v", scoped.Issues)
	}
	if scoped.Summary.Total != 1 || scoped.Summary.Running != 1 {
		t.Fatalf("summary = %+v, want one running manager row", scoped.Summary)
	}
	if scoped.Declared.Persistent != 1 || scoped.Declared.Running != 1 || scoped.Declared.Missing != 0 {
		t.Fatalf("declared = %+v, want manager declaration only", scoped.Declared)
	}
	if len(scoped.Instances) != 1 || scoped.Instances[0].Instance != "manager" {
		t.Fatalf("instances = %+v, want manager only", scoped.Instances)
	}
}

func TestHealthInstanceFilterScopesInstanceAndDeclaredIssues(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager":        {Name: "manager", Agent: "manager"},
		"ticket-manager": {Name: "ticket-manager", Agent: "ticket-manager"},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 10},
		{Instance: "ticket-manager", Agent: "ticket-manager", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked", PID: 11},
		{Instance: "adhoc", Agent: "manager", Lifecycle: string(daemon.StatusCrashed), Phase: "unknown", PID: 12},
	}
	opts, err := newHealthOptionsWithInstances(nil, nil, nil, []string{"manager"}, false)
	if err != nil {
		t.Fatalf("newHealthOptionsWithInstances: %v", err)
	}
	scoped := buildHealthWithOptions(true, 123, rows, topo, now, opts)
	if !scoped.Healthy {
		t.Fatalf("manager-scoped health should ignore other instance issues: %+v", scoped.Issues)
	}
	if scoped.Summary.Total != 1 || scoped.Summary.Running != 1 {
		t.Fatalf("summary = %+v, want one running manager row", scoped.Summary)
	}
	if scoped.Declared.Persistent != 1 || scoped.Declared.Running != 1 || scoped.Declared.Missing != 0 {
		t.Fatalf("declared = %+v, want manager declaration only", scoped.Declared)
	}
	if len(scoped.Instances) != 1 || scoped.Instances[0].Instance != "manager" {
		t.Fatalf("instances = %+v, want manager only", scoped.Instances)
	}
}

func TestHealthLastScopesRowsAndDeclaredIssuesToNewestInstances(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"old":     {Name: "old", Agent: "worker"},
		"mid":     {Name: "mid", Agent: "manager"},
		"new":     {Name: "new", Agent: "manager"},
		"missing": {Name: "missing", Agent: "manager"},
	}}
	rows := []instanceRow{
		{Instance: "old", Agent: "worker", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked", StartedAt: now.Add(-2 * time.Hour), PID: 10},
		{Instance: "new", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", StartedAt: now.Add(-5 * time.Minute), PID: 11},
		{Instance: "mid", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", StartedAt: now.Add(-30 * time.Minute), PID: 12},
	}
	got := buildHealthWithOptions(true, 123, rows, topo, now, healthOptions{filters: psOptions{Limit: 2}})
	if !got.Healthy {
		t.Fatalf("latest-scoped health should ignore old crash and unrelated missing declaration: %+v", got.Issues)
	}
	if got.Summary.Total != 2 || got.Summary.Running != 2 {
		t.Fatalf("summary = %+v, want two running newest rows", got.Summary)
	}
	if len(got.Instances) != 2 || got.Instances[0].Instance != "new" || got.Instances[1].Instance != "mid" {
		t.Fatalf("instances = %+v, want newest rows new,mid", got.Instances)
	}
	if got.Declared.Persistent != 2 || got.Declared.Running != 2 || got.Declared.Missing != 0 {
		t.Fatalf("declared = %+v, want only selected declarations", got.Declared)
	}
}

func TestHealthLatestWithAgentFilterUsesNewestMatchingInstance(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager-old": {Name: "manager-old", Agent: "manager"},
		"manager-new": {Name: "manager-new", Agent: "manager"},
		"worker-new":  {Name: "worker-new", Agent: "worker"},
	}}
	rows := []instanceRow{
		{Instance: "worker-new", Agent: "worker", Lifecycle: string(daemon.StatusRunning), Phase: "idle", StartedAt: now.Add(-5 * time.Minute), PID: 10},
		{Instance: "manager-old", Agent: "manager", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked", StartedAt: now.Add(-2 * time.Hour), PID: 11},
		{Instance: "manager-new", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", StartedAt: now.Add(-30 * time.Minute), PID: 12},
	}
	opts, err := newHealthOptions(nil, []string{"manager"}, nil, false)
	if err != nil {
		t.Fatalf("newHealthOptions: %v", err)
	}
	opts.filters.Limit = 1
	got := buildHealthWithOptions(true, 123, rows, topo, now, opts)
	if !got.Healthy {
		t.Fatalf("latest manager health should ignore older crashed manager: %+v", got.Issues)
	}
	if len(got.Instances) != 1 || got.Instances[0].Instance != "manager-new" {
		t.Fatalf("instances = %+v, want newest manager only", got.Instances)
	}
}

func TestHealthAgentFilterRejectsEmptyAgent(t *testing.T) {
	_, err := newHealthOptions(nil, []string{"  "}, nil, false)
	if err == nil || !strings.Contains(err.Error(), "non-empty agent") {
		t.Fatalf("err = %v, want non-empty agent validation", err)
	}
}

func TestHealthStatusAndPhaseFiltersScopeInstanceAndDeclaredIssues(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager":        {Name: "manager", Agent: "manager"},
		"ticket-manager": {Name: "ticket-manager", Agent: "ticket-manager"},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 10},
		{Instance: "ticket-manager", Agent: "ticket-manager", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked", PID: 11},
		{Instance: "worker", Agent: "worker", Lifecycle: string(daemon.StatusCrashed), Phase: "unknown", PID: 12},
	}
	opts, err := newHealthOptions([]string{"crashed"}, nil, []string{"blocked"}, false)
	if err != nil {
		t.Fatalf("newHealthOptions: %v", err)
	}
	got := buildHealthWithOptions(true, 123, rows, topo, now, opts)
	if got.Healthy {
		t.Fatalf("crashed blocked health should be unhealthy")
	}
	if got.Summary.Total != 1 || got.Summary.Crashed != 1 {
		t.Fatalf("summary = %+v, want one crashed row", got.Summary)
	}
	if len(got.Instances) != 1 || got.Instances[0].Instance != "ticket-manager" {
		t.Fatalf("instances = %+v, want ticket-manager only", got.Instances)
	}
	if got.Declared.Persistent != 1 || got.Declared.Running != 0 || got.Declared.Missing != 0 {
		t.Fatalf("declared = %+v, want only crashed ticket-manager declaration", got.Declared)
	}
	codes := map[string]bool{}
	for _, issue := range got.Issues {
		codes[issue.Code] = true
		if issue.Instance == "manager" {
			t.Fatalf("manager issue should be filtered out: %+v", got.Issues)
		}
	}
	for _, want := range []string{"instance_crashed", "declared_not_running"} {
		if !codes[want] {
			t.Fatalf("issues = %+v, missing %s", got.Issues, want)
		}
	}
}

func TestHealthUnknownStatusFilterIncludesMissingDeclared(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager": {Name: "manager", Agent: "manager"},
	}}
	opts, err := newHealthOptions([]string{"unknown"}, nil, nil, false)
	if err != nil {
		t.Fatalf("newHealthOptions: %v", err)
	}
	got := buildHealthWithOptions(true, 123, nil, topo, now, opts)
	if got.Healthy {
		t.Fatalf("missing declared instance should be unhealthy")
	}
	if got.Declared.Persistent != 1 || got.Declared.Missing != 1 {
		t.Fatalf("declared = %+v, want missing declared manager", got.Declared)
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != "declared_missing" {
		t.Fatalf("issues = %+v, want declared_missing", got.Issues)
	}
}

func TestHealthStaleFilterScopesRowsAndDeclared(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager":        {Name: "manager", Agent: "manager"},
		"ticket-manager": {Name: "ticket-manager", Agent: "ticket-manager"},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "implementing", Stale: true, PID: 10},
		{Instance: "ticket-manager", Agent: "ticket-manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 11},
	}
	opts, err := newHealthOptions(nil, nil, nil, true)
	if err != nil {
		t.Fatalf("newHealthOptions: %v", err)
	}
	got := buildHealthWithOptions(true, 123, rows, topo, now, opts)
	if got.Healthy {
		t.Fatalf("stale filtered health should be unhealthy")
	}
	if got.Summary.Total != 1 || got.Summary.Stale != 1 {
		t.Fatalf("summary = %+v, want one stale row", got.Summary)
	}
	if got.Declared.Persistent != 1 || got.Declared.Running != 1 {
		t.Fatalf("declared = %+v, want stale manager declaration only", got.Declared)
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != "status_stale" || got.Issues[0].Instance != "manager" {
		t.Fatalf("issues = %+v, want stale manager only", got.Issues)
	}
}

func TestHealthUnhealthyFilterScopesRowsAndDeclared(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager":        {Name: "manager", Agent: "manager"},
		"worker":         {Name: "worker", Agent: "worker"},
		"ticket-manager": {Name: "ticket-manager", Agent: "ticket-manager"},
		"missing":        {Name: "missing", Agent: "manager"},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "implementing", Stale: true, PID: 10},
		{Instance: "worker", Agent: "worker", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked", PID: 11},
		{Instance: "ticket-manager", Agent: "ticket-manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 12},
	}
	opts, err := newHealthOptionsWithInstancesAndUnhealthy(nil, nil, nil, nil, false, true)
	if err != nil {
		t.Fatalf("newHealthOptionsWithInstancesAndUnhealthy: %v", err)
	}
	got := buildHealthWithOptions(true, 123, rows, topo, now, opts)
	if got.Healthy {
		t.Fatalf("unhealthy-filtered health should be unhealthy")
	}
	if got.Summary.Total != 2 || got.Summary.Crashed != 1 || got.Summary.Stale != 1 {
		t.Fatalf("summary = %+v, want one crashed and one stale row", got.Summary)
	}
	if got.Declared.Persistent != 2 || got.Declared.Running != 1 || got.Declared.Missing != 0 {
		t.Fatalf("declared = %+v, want only unhealthy declared rows", got.Declared)
	}
	if len(got.Instances) != 2 || got.Instances[0].Instance != "manager" || got.Instances[1].Instance != "worker" {
		t.Fatalf("instances = %+v, want manager and worker only", got.Instances)
	}
	codes := map[string]bool{}
	for _, issue := range got.Issues {
		codes[issue.Code+"."+issue.Instance] = true
		if issue.Instance == "ticket-manager" || issue.Instance == "missing" {
			t.Fatalf("healthy and missing declared instances should be filtered out: %+v", got.Issues)
		}
	}
	for _, want := range []string{"status_stale.manager", "instance_crashed.worker", "declared_not_running.worker"} {
		if !codes[want] {
			t.Fatalf("issues = %+v, missing %s", got.Issues, want)
		}
	}
}

func TestHealthStrictTopologyReportsRunningExtra(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager": {Name: "manager", Agent: "manager"},
		"worker":  {Name: "worker", Agent: "worker", Ephemeral: true},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 10},
		{Instance: "worker", Agent: "worker", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 11},
		{Instance: "worker-abc123", Agent: "worker", Lifecycle: string(daemon.StatusRunning), Phase: "implementing", PID: 14},
		{Instance: "adhoc", Agent: "worker", Lifecycle: string(daemon.StatusRunning), Phase: "implementing", PID: 12},
		{Instance: "old", Agent: "worker", Lifecycle: string(daemon.StatusStopped), Phase: "done", PID: 13},
	}

	got := buildHealthWithOptions(true, 123, rows, topo, now, healthOptions{strictTopology: true})
	if got.Healthy {
		t.Fatalf("strict topology health should be unhealthy")
	}
	var extra *healthIssue
	for i := range got.Issues {
		if got.Issues[i].Code == "topology_extra_running" {
			extra = &got.Issues[i]
		}
		if got.Issues[i].Instance == "worker" || got.Issues[i].Instance == "worker-abc123" || got.Issues[i].Instance == "old" {
			t.Fatalf("declared ephemeral, ephemeral child, and stopped extra instances should not be strict issues: %+v", got.Issues)
		}
	}
	if extra == nil || extra.Instance != "adhoc" || extra.Status != "running" || extra.Phase != "implementing" {
		t.Fatalf("strict topology issue = %+v, want running adhoc issue", got.Issues)
	}
	if got.Declared.Persistent != 1 || got.Declared.Running != 1 || got.Declared.Missing != 0 {
		t.Fatalf("declared summary = %+v, want one running persistent declaration", got.Declared)
	}
}

func TestHealthStrictTopologyRespectsFilters(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager": {Name: "manager", Agent: "manager"},
	}}
	rows := []instanceRow{
		{Instance: "manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 10},
		{Instance: "adhoc-manager", Agent: "manager", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 11},
		{Instance: "adhoc-worker", Agent: "worker", Lifecycle: string(daemon.StatusRunning), Phase: "idle", PID: 12},
	}
	opts, err := newHealthOptions(nil, []string{"worker"}, nil, false)
	if err != nil {
		t.Fatalf("newHealthOptions: %v", err)
	}
	opts.strictTopology = true

	got := buildHealthWithOptions(true, 123, rows, topo, now, opts)
	if got.Healthy {
		t.Fatalf("worker-scoped strict topology health should be unhealthy")
	}
	if got.Summary.Total != 1 || got.Summary.Running != 1 {
		t.Fatalf("summary = %+v, want one matching worker row", got.Summary)
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != "topology_extra_running" || got.Issues[0].Instance != "adhoc-worker" {
		t.Fatalf("issues = %+v, want only matching worker extra", got.Issues)
	}
}

func TestHealthFilterValidation(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		phases   []string
		want     string
	}{
		{name: "unknown status", statuses: []string{"paused"}, want: "unknown --status"},
		{name: "empty status", statuses: []string{"  "}, want: "non-empty status"},
		{name: "unknown phase", phases: []string{"waiting"}, want: "unknown --phase"},
		{name: "empty phase", phases: []string{"  "}, want: "non-empty phase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newHealthOptions(tc.statuses, nil, tc.phases, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRenderHealthShowsIssues(t *testing.T) {
	result := buildHealth(false, 0, []instanceRow{
		{Instance: "w", Agent: "worker", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked"},
	}, nil, time.Now())
	result.Queue = queueSummary{Total: 1, Dead: 1, Attempts: 3}
	result.addIssue("queue_dead_letter", "", "", "", "queue has 1 dead-letter item(s)")
	var buf bytes.Buffer
	renderHealth(&buf, result)
	out := buf.String()
	for _, want := range []string{"health: unhealthy", "daemon: not running", "queue: total=1 pending=0 dead=1 delayed=0 attempts=3", "phases:", "blocked=1", "instance_crashed", "queue_dead_letter", "w"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered health missing %q:\n%s", want, out)
		}
	}
}

func TestHealthCommandJSONExitsUnhealthy(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"health", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode health json: %v\nbody=%s", err, stdout.String())
	}
	if body.Healthy || body.Daemon.Running {
		t.Fatalf("health json should be unhealthy with daemon down: %+v", body)
	}
}

func TestHealthCommandReportsDeadQueueItems(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	item := &daemon.QueueItem{
		ID:             "q-health-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-108",
		Payload:        map[string]any{"target": "worker", "ticket": "SQU-108"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-time.Hour),
		UpdatedAt:      now,
		DeadLetteredAt: now,
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), item); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1\nstderr=%s", err, stderr.String())
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode health json: %v\nbody=%s", err, stdout.String())
	}
	if body.Queue.Total != 1 || body.Queue.Dead != 1 || body.Queue.Attempts != daemon.MaxQueueAttempts {
		t.Fatalf("queue = %+v, want one dead item", body.Queue)
	}
	var sawQueueIssue bool
	for _, issue := range body.Issues {
		if issue.Code == "queue_dead_letter" {
			sawQueueIssue = true
			break
		}
	}
	if !sawQueueIssue {
		t.Fatalf("issues = %+v, missing queue_dead_letter", body.Issues)
	}
}

func TestHealthCommandJobsReportsJobAttention(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC().Add(-48 * time.Hour)
	failed := &job.Job{
		ID:         "squ-91",
		Ticket:     "SQU-91",
		Target:     "worker",
		Status:     job.StatusFailed,
		LastStatus: "tests failed",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := job.Write(teamDir, failed); err != nil {
		t.Fatalf("write failed job: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--jobs", "--json", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("health --jobs succeeded unexpectedly")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	var body healthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode health jobs json: %v\nbody=%s stderr=%s", err, out.String(), stderr.String())
	}
	if body.Jobs == nil || body.Jobs.Summary.Total != 1 || len(body.Jobs.Attention) != 1 {
		t.Fatalf("jobs = %+v", body.Jobs)
	}
	var sawJobIssue bool
	for _, issue := range body.Issues {
		if issue.Code == "job_attention" && issue.Job == "squ-91" && issue.Status == string(job.StatusFailed) {
			sawJobIssue = true
		}
	}
	if !sawJobIssue {
		t.Fatalf("issues = %+v, missing job_attention", body.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--jobs", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health --jobs text succeeded unexpectedly")
	}
	for _, want := range []string{"jobs: total=1", "attention=1", "job_attention", "job=squ-91"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestHealthCommandJobsReportsBlockedStatusPreview(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := &job.Job{
		ID:        "squ-92",
		Ticket:    "SQU-92",
		Target:    "worker",
		Status:    job.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	writeStatus(t, filepath.Join(teamDir, "state", "worker-squ-92"), `[status]
phase = "blocked"
description = "needs credentials"
since = "2026-06-18T12:00:00Z"

[work]
job = "squ-92"
ticket = "SQU-92"
branch = "worker-squ-92"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--jobs", "--json", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("health --jobs succeeded unexpectedly")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	var body healthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode health jobs json: %v\nbody=%s stderr=%s", err, out.String(), stderr.String())
	}
	if len(body.JobStatus) != 1 || body.JobStatus[0].JobID != "squ-92" || body.JobStatus[0].After != job.StatusBlocked || !body.JobStatus[0].Changed {
		t.Fatalf("job status preview = %+v", body.JobStatus)
	}
	var sawIssue bool
	for _, issue := range body.Issues {
		if issue.Code == "job_status_blocked" && issue.Job == "squ-92" && issue.Phase == "blocked" {
			sawIssue = true
			break
		}
	}
	if !sawIssue {
		t.Fatalf("issues = %+v, missing job_status_blocked", body.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--jobs", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health --jobs text succeeded unexpectedly")
	}
	for _, want := range []string{"job status: previews=1 changes=1 blocked=1", "job_status_blocked", "job=squ-92"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestHealthCommandLatestUsesLocalNewestMatchingMetadata(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "manager-old", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "manager-new", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--latest", "--agent", "manager", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1 from daemon-down health\nstderr=%s", err, stderr.String())
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode health json: %v\nbody=%s", err, stdout.String())
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "manager-new" {
		t.Fatalf("instances = %+v, want newest manager metadata", body.Instances)
	}
	if body.Summary.Total != 1 || body.Summary.Stopped != 1 {
		t.Fatalf("summary = %+v, want one stopped manager", body.Summary)
	}
}

func TestHealthCommandUnhealthyUsesLocalMetadataAndStatus(t *testing.T) {
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
description = "stale work"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `[status]
phase = "idle"
description = "fresh work"
`, now)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--unhealthy", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1 from daemon-down health\nstderr=%s", err, stderr.String())
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode health json: %v\nbody=%s", err, stdout.String())
	}
	if got := strings.Join(healthInstanceNames(body.Instances), ","); got != "crashed,stale" {
		t.Fatalf("instances = %+v, want crashed and stale rows", body.Instances)
	}
	if body.Summary.Total != 2 || body.Summary.Crashed != 1 || body.Summary.Stale != 1 {
		t.Fatalf("summary = %+v, want one crashed and one stale row", body.Summary)
	}
	codes := map[string]bool{}
	for _, issue := range body.Issues {
		codes[issue.Code+"."+issue.Instance] = true
		if issue.Instance == "fresh" {
			t.Fatalf("fresh instance should be filtered out: %+v", body.Issues)
		}
	}
	for _, want := range []string{"daemon_not_running.", "instance_crashed.crashed", "status_stale.stale"} {
		if !codes[want] {
			t.Fatalf("issues = %+v, missing %s", body.Issues, want)
		}
	}
}

func healthInstanceNames(rows []healthInstance) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Instance)
	}
	return out
}

func TestHealthCommandFormatExitsUnhealthy(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"health", "--format", "{{.Healthy}}:{{.Daemon.Running}}:{{.Summary.Total}}:{{len .Issues}}", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if got, want := stdout.String(), "false:false:0:3\n"; got != want {
		t.Fatalf("health --format output = %q, want %q", got, want)
	}
}

func TestHealthQuietExitsUnhealthyWithoutOutput(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--quiet", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet health should not write output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestHealthQuietWaitExitsUnhealthyWithoutOutput(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--quiet", "--wait", "--timeout", "5ms", "--interval", "1ms", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet wait health should not write output, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestHealthQuietRejectsOutputConflicts(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"health", "--quiet", "--json"}, "--quiet"},
		{[]string{"health", "--quiet", "--watch"}, "--quiet"},
		{[]string{"health", "--format", "{{.Healthy}}", "--json"}, "--format cannot be combined"},
		{[]string{"health", "--format", "{{.Healthy}}", "--quiet"}, "--format cannot be combined"},
		{[]string{"health", "--format", "{{"}, "invalid --format template"},
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

func TestHealthLatestLastValidation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"health", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"health", "--latest", "--last", "2"}, "choose one of --latest or --last"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(append(tc.args, "--target", tmp))
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestHealthWaitJSONTimesOutWithFinalSnapshot(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--wait", "--json", "--timeout", "5ms", "--interval", "1ms", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode health wait json: %v\nbody=%s", err, stdout.String())
	}
	if body.Healthy || body.Daemon.Running {
		t.Fatalf("health wait json should report unhealthy daemon-down state: %+v", body)
	}
	if !strings.Contains(stderr.String(), "wait timed out before the fleet became healthy") {
		t.Fatalf("stderr = %q, want explicit timeout message", stderr.String())
	}
}

func TestHealthWaitFormatTimesOutWithFinalSnapshot(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"health", "--wait", "--format", "{{.Healthy}}:{{len .Issues}}", "--timeout", "5ms", "--interval", "1ms", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if got, want := stdout.String(), "false:3\n"; got != want {
		t.Fatalf("health --wait --format output = %q, want %q", got, want)
	}
}

func TestHealthWatchTextClearsWhenRequested(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runHealthWatchWithClear(ctx, &buf, teamDir, time.Millisecond, time.Now, false, healthOptions{}, true); err != nil {
		t.Fatalf("runHealthWatchWithClear: %v", err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, watchClearSequence) {
		t.Fatalf("health watch should start with clear sequence, got %q", body[:min(len(body), len(watchClearSequence)+20)])
	}
	if !strings.Contains(body, "health: unhealthy") || !strings.Contains(body, "daemon: not running") {
		t.Fatalf("health watch clear output missing snapshot: %q", body)
	}
}

func TestHealthWatchTextNoClearAppendsSnapshots(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runHealthWatchWithClear(ctx, &buf, teamDir, time.Millisecond, time.Now, false, healthOptions{}, false); err != nil {
		t.Fatalf("runHealthWatchWithClear no clear: %v", err)
	}
	body := buf.String()
	if strings.Contains(body, watchClearSequence) {
		t.Fatalf("health watch no-clear should not emit clear sequence: %q", body)
	}
	if !strings.Contains(body, "health: unhealthy") || !strings.Contains(body, "daemon: not running") {
		t.Fatalf("health watch no-clear output missing snapshot: %q", body)
	}
}

func TestHealthFormatWatchEmitsRows(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"
	tmpl, err := parseHealthFormat("{{.Healthy}}:{{.Daemon.Running}}")
	if err != nil {
		t.Fatalf("parseHealthFormat: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runHealthFormatWatch(ctx, &buf, teamDir, time.Millisecond, time.Now, healthOptions{}, tmpl); err != nil {
		t.Fatalf("runHealthFormatWatch: %v", err)
	}
	first := strings.Split(strings.TrimSpace(buf.String()), "\n")[0]
	if first != "false:false" {
		t.Fatalf("first health format watch row = %q, want false:false\nbody=%s", first, buf.String())
	}
	if strings.Contains(buf.String(), watchClearSequence) {
		t.Fatalf("health format watch should not emit clear sequence: %q", buf.String())
	}
}

func TestHealthWatchAndWaitConflict(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--watch", "--wait"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --watch/--wait conflict")
	}
	if !strings.Contains(stderr.String(), "choose one of --watch or --wait") {
		t.Fatalf("stderr = %q, want conflict validation", stderr.String())
	}
}
