package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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

func TestHealthCrashedIssueSuggestsRuntimeResumePlan(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	rows := []instanceRow{{
		Instance:  "worker-squ-46",
		Agent:     "worker",
		Lifecycle: string(daemon.StatusCrashed),
		Phase:     "unknown",
		Job:       "SQU-46",
	}}
	got := buildHealth(true, 123, rows, nil, now)
	if got.Healthy {
		t.Fatalf("health should be unhealthy")
	}
	var crashed *healthIssue
	for i := range got.Issues {
		if got.Issues[i].Code == "instance_crashed" {
			crashed = &got.Issues[i]
			break
		}
	}
	if crashed == nil {
		t.Fatalf("issues = %+v, missing instance_crashed", got.Issues)
	}
	if crashed.Job != "squ-46" || !containsString(crashed.Actions, "agent-team job resume-plan squ-46 --status crashed") {
		t.Fatalf("crashed issue = %+v", crashed)
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

func TestHealthRuntimeFilterScopesInstanceAndDeclaredIssues(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"codex-worker":   {Name: "codex-worker", Agent: "worker"},
		"claude-manager": {Name: "claude-manager", Agent: "manager"},
	}}
	rows := []instanceRow{
		{Instance: "codex-worker", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex-dev", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked", PID: 10},
		{Instance: "claude-manager", Agent: "manager", Runtime: "claude", RuntimeBinary: "claude-code", Lifecycle: string(daemon.StatusCrashed), Phase: "blocked", PID: 11},
	}
	opts, err := newHealthOptionsWithRuntimeInstancesAndUnhealthy(nil, []string{"codex"}, nil, nil, nil, false, false)
	if err != nil {
		t.Fatalf("newHealthOptionsWithRuntimeInstancesAndUnhealthy: %v", err)
	}
	scoped := buildHealthWithOptions(true, 123, rows, topo, now, opts)
	if scoped.Healthy {
		t.Fatalf("codex-scoped health should see codex crash")
	}
	if scoped.Summary.Total != 1 || scoped.Summary.Crashed != 1 {
		t.Fatalf("summary = %+v, want one crashed codex row", scoped.Summary)
	}
	if scoped.Declared.Persistent != 1 || scoped.Declared.Running != 0 {
		t.Fatalf("declared = %+v, want codex declaration only", scoped.Declared)
	}
	if len(scoped.Instances) != 1 || scoped.Instances[0].Instance != "codex-worker" {
		t.Fatalf("instances = %+v, want codex worker only", scoped.Instances)
	}
	for _, issue := range scoped.Issues {
		if issue.Instance == "claude-manager" {
			t.Fatalf("claude issue should be filtered out: %+v", scoped.Issues)
		}
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
	declaredAction := false
	for _, issue := range got.Issues {
		codes[issue.Code] = true
		if issue.Code == "declared_not_running" && containsString(issue.Actions, "agent-team sync --dry-run") {
			declaredAction = true
		}
		if issue.Instance == "manager" {
			t.Fatalf("manager issue should be filtered out: %+v", got.Issues)
		}
	}
	for _, want := range []string{"instance_crashed", "declared_not_running"} {
		if !codes[want] {
			t.Fatalf("issues = %+v, missing %s", got.Issues, want)
		}
	}
	if !declaredAction {
		t.Fatalf("declared_not_running issue missing sync action: %+v", got.Issues)
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
	if !containsString(got.Issues[0].Actions, "agent-team sync --dry-run") {
		t.Fatalf("declared_missing actions = %+v", got.Issues[0].Actions)
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
		runtimes []string
		phases   []string
		want     string
	}{
		{name: "unknown status", statuses: []string{"paused"}, want: "unknown --status"},
		{name: "empty status", statuses: []string{"  "}, want: "non-empty status"},
		{name: "unknown runtime", runtimes: []string{"llama"}, want: "unknown --runtime"},
		{name: "empty runtime", runtimes: []string{"  "}, want: "non-empty runtime"},
		{name: "unknown phase", phases: []string{"waiting"}, want: "unknown --phase"},
		{name: "empty phase", phases: []string{"  "}, want: "non-empty phase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newHealthOptionsWithRuntimeInstancesAndUnhealthy(tc.statuses, tc.runtimes, nil, tc.phases, nil, false, false)
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
	if !containsString(body.Actions, "agent-team sync --dry-run") {
		t.Fatalf("health json actions = %+v, want sync dry-run", body.Actions)
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
			if !containsString(issue.Actions, "agent-team queue retry --all --sort attempts --limit 10") || !containsString(issue.Actions, "agent-team repair --skip-tick") {
				t.Fatalf("queue issue actions = %+v", issue.Actions)
			}
			sawQueueIssue = true
			break
		}
	}
	if !sawQueueIssue {
		t.Fatalf("issues = %+v, missing queue_dead_letter", body.Issues)
	}
	for _, want := range []string{
		"agent-team sync --dry-run",
		"agent-team queue retry --all --sort attempts --limit 10",
		"agent-team repair --skip-tick",
	} {
		if !containsString(body.Actions, want) {
			t.Fatalf("health json actions missing %q: %+v", want, body.Actions)
		}
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health text succeeded unexpectedly")
	}
	for _, want := range []string{"queue_dead_letter", "action=agent-team queue retry --all --sort attempts --limit 10; agent-team repair --skip-tick"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s", want, textOut.String())
		}
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"health", "--target", tmp, "--commands"})
	err = commands.Execute()
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("health commands err = %v, want exit 1\nstderr=%s", err, commandsErr.String())
	}
	wantCommands := strings.Join(scopedOperatorActions([]string{
		"agent-team sync --dry-run",
		"agent-team queue retry --all --sort attempts --limit 10",
		"agent-team repair --skip-tick",
	}, operatorCommandScope{Repo: tmp, Set: true}), "\n") + "\n"
	if got := commandsOut.String(); got != wantCommands {
		t.Fatalf("health commands = %q, want %q", got, wantCommands)
	}
}

func TestHealthCommandReportsJobScopedQueueDeadLetterAction(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j, err := job.New("SQU-91", "worker", "recover queue failure", now)
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.Status = job.StatusRunning
	j.Instance = "worker-squ-91"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
		ID:             "q-job-health-dead",
		State:          daemon.QueueStateDead,
		EventType:      "agent.dispatch",
		Instance:       "worker",
		InstanceID:     "worker-squ-91",
		Payload:        map[string]any{"target": "worker", "ticket": "SQU-91", "job_id": "squ-91"},
		Attempts:       daemon.MaxQueueAttempts,
		LastError:      "spawn failed",
		QueuedAt:       now.Add(-2 * time.Hour),
		UpdatedAt:      now.Add(-time.Hour),
		DeadLetteredAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("WriteQueueItem: %v", err)
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--json", "--target", tmp})
	err = cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1\nstderr=%s", err, stderr.String())
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode health json: %v\nbody=%s", err, stdout.String())
	}
	var queueIssue *healthIssue
	for i := range body.Issues {
		if body.Issues[i].Code == "queue_dead_letter" {
			queueIssue = &body.Issues[i]
			break
		}
	}
	if queueIssue == nil {
		t.Fatalf("issues = %+v, missing queue_dead_letter", body.Issues)
	}
	if !containsString(queueIssue.Actions, "agent-team job queue retry squ-91 --all --sort attempts --limit 10") || containsString(queueIssue.Actions, "agent-team queue retry --all --sort attempts --limit 10") {
		t.Fatalf("queue issue actions = %+v", queueIssue.Actions)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health text succeeded unexpectedly")
	}
	for _, want := range []string{"queue_dead_letter", "action=agent-team job queue retry squ-91 --all --sort attempts --limit 10; agent-team repair --skip-tick"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestHealthCommandReportsQueueQuarantine(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	writeQuarantinedQueueItem(t, teamDir, "20260619T000000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-health-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-109",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-109"},
		QueuedAt:   now.Add(-time.Minute),
		UpdatedAt:  now,
	})

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
		t.Fatalf("decode health quarantine json: %v\nbody=%s", err, stdout.String())
	}
	if body.Queue.Quarantined != 1 || body.Queue.QuarantineRestorable != 1 || body.Queue.QuarantineUnrestorable != 0 {
		t.Fatalf("queue = %+v, want one quarantined item", body.Queue)
	}
	var sawQuarantineIssue bool
	for _, issue := range body.Issues {
		if issue.Code == "queue_quarantined" {
			if issue.Severity != "warning" || !containsString(issue.Actions, "agent-team queue quarantine ls") || !containsString(issue.Actions, "agent-team queue quarantine ls --restorable") || !containsString(issue.Actions, "agent-team snapshot --json") {
				t.Fatalf("queue quarantine issue = %+v", issue)
			}
			sawQuarantineIssue = true
			break
		}
	}
	if !sawQuarantineIssue {
		t.Fatalf("issues = %+v, missing queue_quarantined", body.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health text succeeded unexpectedly")
	}
	for _, want := range []string{"quarantined=1 restorable=1 unrestorable=0", "queue_quarantined", "agent-team queue quarantine ls --restorable"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s\nstderr=%s", want, textOut.String(), textErr.String())
		}
	}
}

func TestHealthCommandSuggestsJobScopedQueueQuarantine(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := mustNewJob(t, "SQU-92", "worker")
	j.Instance = "worker-squ-92"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	writeQuarantinedQueueItem(t, teamDir, "20260619T010000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
		ID:         "q-health-job-quarantined",
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-squ-92",
		Payload:    map[string]any{"target": "worker", "ticket": "SQU-92", "job_id": "squ-92"},
		QueuedAt:   now.Add(-time.Minute),
		UpdatedAt:  now,
	})

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
		t.Fatalf("decode health quarantine json: %v\nbody=%s", err, stdout.String())
	}
	var queueIssue *healthIssue
	for i := range body.Issues {
		if body.Issues[i].Code == "queue_quarantined" {
			queueIssue = &body.Issues[i]
			break
		}
	}
	if queueIssue == nil {
		t.Fatalf("issues = %+v, missing queue_quarantined", body.Issues)
	}
	for _, want := range []string{
		"agent-team job queue quarantine squ-92",
		"agent-team job queue quarantine squ-92 --restorable",
		"agent-team job show squ-92",
	} {
		if !containsString(queueIssue.Actions, want) {
			t.Fatalf("queue issue actions missing %q: %+v", want, queueIssue.Actions)
		}
	}
	if containsString(queueIssue.Actions, "agent-team queue quarantine ls") {
		t.Fatalf("queue issue should use job-scoped quarantine actions: %+v", queueIssue.Actions)
	}
}

func TestHealthCommandSuggestsPipelineScopedQueueRecovery(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		mustNewJob(t, "SQU-93", "worker"),
		mustNewJob(t, "SQU-94", "worker"),
	} {
		j.Pipeline = "ticket_to_pr"
		j.Instance = "worker-" + j.ID
		j.Status = job.StatusRunning
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("job.Write: %v", err)
		}
		if err := daemon.WriteQueueItem(daemon.DaemonRoot(teamDir), &daemon.QueueItem{
			ID:             "q-health-" + j.ID + "-dead",
			State:          daemon.QueueStateDead,
			EventType:      "agent.dispatch",
			Instance:       "worker",
			InstanceID:     j.Instance,
			Payload:        map[string]any{"target": "worker", "ticket": j.Ticket, "job_id": j.ID},
			Attempts:       daemon.MaxQueueAttempts,
			LastError:      "spawn failed",
			QueuedAt:       now.Add(-2 * time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
			DeadLetteredAt: now.Add(-time.Hour),
		}); err != nil {
			t.Fatalf("WriteQueueItem: %v", err)
		}
		writeQuarantinedQueueItem(t, teamDir, "20260619T020000.000000000Z", daemon.QueueStatePending, &daemon.QueueItem{
			ID:         "q-health-" + j.ID + "-quarantined",
			EventType:  "agent.dispatch",
			Instance:   "worker",
			InstanceID: j.Instance,
			Payload:    map[string]any{"target": "worker", "ticket": j.Ticket, "job_id": j.ID},
			QueuedAt:   now.Add(-time.Minute),
			UpdatedAt:  now,
		})
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
	var deadIssue, quarantineIssue *healthIssue
	for i := range body.Issues {
		switch body.Issues[i].Code {
		case "queue_dead_letter":
			deadIssue = &body.Issues[i]
		case "queue_quarantined":
			quarantineIssue = &body.Issues[i]
		}
	}
	if deadIssue == nil || quarantineIssue == nil {
		t.Fatalf("issues = %+v, want queue dead and quarantine issues", body.Issues)
	}
	if !containsString(deadIssue.Actions, "agent-team pipeline queue retry ticket_to_pr --all --sort attempts --limit 10") || containsString(deadIssue.Actions, "agent-team queue retry --all --sort attempts --limit 10") || containsString(deadIssue.Actions, "agent-team job queue retry") {
		t.Fatalf("dead issue actions = %+v", deadIssue.Actions)
	}
	for _, want := range []string{
		"agent-team pipeline queue quarantine ticket_to_pr",
		"agent-team pipeline queue quarantine ticket_to_pr --restorable",
		"agent-team pipeline snapshot ticket_to_pr --json",
	} {
		if !containsString(quarantineIssue.Actions, want) {
			t.Fatalf("quarantine issue actions missing %q: %+v", want, quarantineIssue.Actions)
		}
	}
	if containsString(quarantineIssue.Actions, "agent-team queue quarantine ls") || containsString(quarantineIssue.Actions, "agent-team job queue quarantine") {
		t.Fatalf("quarantine issue should use pipeline-scoped actions: %+v", quarantineIssue.Actions)
	}
}

func TestHealthCommandReportsOutboxQuarantine(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	writeQuarantinedOutboxFile(t, teamDir, "20260627T220000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-health-quarantined",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"target": "worker", "ticket": "SQU-220"},
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now,
	})

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
		t.Fatalf("decode health outbox quarantine json: %v\nbody=%s", err, stdout.String())
	}
	if body.OutboxQuarantine.Quarantined != 1 || body.OutboxQuarantine.Restorable != 1 || body.OutboxQuarantine.Unrestorable != 0 {
		t.Fatalf("outbox quarantine = %+v, want one restorable item", body.OutboxQuarantine)
	}
	var sawQuarantineIssue bool
	for _, issue := range body.Issues {
		if issue.Code == "outbox_quarantined" {
			if issue.Severity != "warning" || !containsString(issue.Actions, "agent-team outbox quarantine ls") || !containsString(issue.Actions, "agent-team outbox quarantine ls --restorable") || !containsString(issue.Actions, "agent-team snapshot --json") {
				t.Fatalf("outbox quarantine issue = %+v", issue)
			}
			sawQuarantineIssue = true
			break
		}
	}
	if !sawQuarantineIssue {
		t.Fatalf("issues = %+v, missing outbox_quarantined", body.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health text succeeded unexpectedly")
	}
	for _, want := range []string{"outbox quarantine: quarantined=1 restorable=1 unrestorable=0", "outbox_quarantined", "agent-team outbox quarantine ls --restorable"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s\nstderr=%s", want, textOut.String(), textErr.String())
		}
	}
}

func TestHealthCommandReportsJobQuarantine(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	writeQuarantinedJobFile(t, teamDir, "20260627T230000.000000000Z", "squ-230.toml", []byte(`id = "squ-230"
ticket = "SQU-230"
target = "worker"
status = "queued"
created_at = 2026-06-27T23:00:00Z
updated_at = 2026-06-27T23:00:00Z
`))
	writeQuarantinedJobFile(t, teamDir, "20260627T230000.000000000Z", "broken.toml", []byte("id = [\n"))

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
		t.Fatalf("decode health job quarantine json: %v\nbody=%s", err, stdout.String())
	}
	if body.JobQuarantine.Quarantined != 2 || body.JobQuarantine.Restorable != 1 || body.JobQuarantine.Unrestorable != 1 {
		t.Fatalf("job quarantine = %+v, want mixed restorable/unrestorable", body.JobQuarantine)
	}
	var sawQuarantineIssue bool
	for _, issue := range body.Issues {
		if issue.Code == "job_quarantined" {
			if issue.Severity != "warning" || !containsString(issue.Actions, "agent-team job quarantine") || !containsString(issue.Actions, "agent-team job quarantine --restorable") || !containsString(issue.Actions, "agent-team job quarantine --unrestorable") || !containsString(issue.Actions, "agent-team snapshot --json") {
				t.Fatalf("job quarantine issue = %+v", issue)
			}
			sawQuarantineIssue = true
			break
		}
	}
	if !sawQuarantineIssue {
		t.Fatalf("issues = %+v, missing job_quarantined", body.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health text succeeded unexpectedly")
	}
	for _, want := range []string{"job quarantine: quarantined=2 restorable=1 unrestorable=1", "job_quarantined", "agent-team job quarantine --restorable", "agent-team job quarantine --unrestorable"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s\nstderr=%s", want, textOut.String(), textErr.String())
		}
	}
}

func TestHealthCommandSuggestsJobScopedOutboxQuarantine(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	now := time.Now().UTC()
	j := mustNewJob(t, "SQU-221", "worker")
	j.Instance = "worker-squ-221"
	if err := job.Write(teamDir, j); err != nil {
		t.Fatalf("job.Write: %v", err)
	}
	writeQuarantinedOutboxFile(t, teamDir, "20260627T221000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
		ID:        "outbox-health-job-quarantined",
		State:     daemon.OutboxStatePending,
		Type:      "agent.dispatch",
		Source:    "manager",
		Payload:   map[string]any{"target": "worker", "ticket": "SQU-221", "job_id": "squ-221"},
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now,
	})

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
		t.Fatalf("decode health outbox quarantine json: %v\nbody=%s", err, stdout.String())
	}
	var outboxIssue *healthIssue
	for i := range body.Issues {
		if body.Issues[i].Code == "outbox_quarantined" {
			outboxIssue = &body.Issues[i]
			break
		}
	}
	if outboxIssue == nil {
		t.Fatalf("issues = %+v, missing outbox_quarantined", body.Issues)
	}
	for _, want := range []string{
		"agent-team job outbox quarantine squ-221",
		"agent-team job outbox quarantine squ-221 --restorable",
		"agent-team job snapshot squ-221 --json",
	} {
		if !containsString(outboxIssue.Actions, want) {
			t.Fatalf("outbox issue actions missing %q: %+v", want, outboxIssue.Actions)
		}
	}
	if containsString(outboxIssue.Actions, "agent-team outbox quarantine ls") {
		t.Fatalf("outbox issue should use job-scoped quarantine actions: %+v", outboxIssue.Actions)
	}
}

func TestHealthCommandSuggestsPipelineScopedOutboxQuarantine(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		mustNewJob(t, "SQU-222", "worker"),
		mustNewJob(t, "SQU-223", "worker"),
	} {
		j.Pipeline = "ticket_to_pr"
		j.Instance = "worker-" + j.ID
		j.Status = job.StatusRunning
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("job.Write: %v", err)
		}
		writeQuarantinedOutboxFile(t, teamDir, "20260627T222000.000000000Z", daemon.OutboxStatePending, &daemon.OutboxItem{
			ID:        "outbox-health-" + j.ID + "-quarantined",
			State:     daemon.OutboxStatePending,
			Type:      "agent.dispatch",
			Source:    "manager",
			Payload:   map[string]any{"target": "worker", "ticket": j.Ticket, "job_id": j.ID},
			CreatedAt: now.Add(-time.Minute),
			UpdatedAt: now,
		})
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
		t.Fatalf("decode health outbox quarantine json: %v\nbody=%s", err, stdout.String())
	}
	var outboxIssue *healthIssue
	for i := range body.Issues {
		if body.Issues[i].Code == "outbox_quarantined" {
			outboxIssue = &body.Issues[i]
			break
		}
	}
	if outboxIssue == nil {
		t.Fatalf("issues = %+v, missing outbox_quarantined", body.Issues)
	}
	for _, want := range []string{
		"agent-team pipeline outbox quarantine ticket_to_pr",
		"agent-team pipeline outbox quarantine ticket_to_pr --restorable",
		"agent-team pipeline snapshot ticket_to_pr --json",
	} {
		if !containsString(outboxIssue.Actions, want) {
			t.Fatalf("outbox issue actions missing %q: %+v", want, outboxIssue.Actions)
		}
	}
	if containsString(outboxIssue.Actions, "agent-team outbox quarantine ls") || containsString(outboxIssue.Actions, "agent-team job outbox quarantine") {
		t.Fatalf("outbox issue should use pipeline-scoped actions: %+v", outboxIssue.Actions)
	}

	next := NewRootCmd()
	nextOut, nextErr := &bytes.Buffer{}, &bytes.Buffer{}
	next.SetOut(nextOut)
	next.SetErr(nextErr)
	next.SetArgs([]string{"next", "--target", tmp, "--source", "outbox", "--reason", "quarantined", "--json"})
	if err := next.Execute(); err != nil {
		t.Fatalf("next outbox quarantine json: %v\nstderr=%s", err, nextErr.String())
	}
	var nextResult nextActionResult
	if err := json.Unmarshal(nextOut.Bytes(), &nextResult); err != nil {
		t.Fatalf("decode next outbox quarantine: %v\nbody=%s", err, nextOut.String())
	}
	for _, want := range []string{
		"agent-team pipeline outbox quarantine ticket_to_pr",
		"agent-team pipeline outbox quarantine ticket_to_pr --restorable",
		"agent-team pipeline snapshot ticket_to_pr --json",
	} {
		if !containsString(nextResult.Actions, want) {
			t.Fatalf("next outbox quarantine actions missing %q: %+v", want, nextResult)
		}
	}
	if nextResult.TotalActions != len(nextResult.Actions) || nextResult.TotalActions != 3 {
		t.Fatalf("next outbox quarantine actions = %+v", nextResult)
	}
}

func TestHealthCommandReportsIntakeFailures(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	replayedAt := time.Date(2026, 6, 19, 12, 10, 0, 0, time.UTC)
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:         "intake-health",
		Time:       time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		Provider:   "linear",
		Status:     intakeDeliveryStatusError,
		HTTPStatus: 503,
		EventType:  "ticket.created",
		Payload:    map[string]any{"source": "linear", "ticket": "SQU-220", "title": "Health intake"},
		Ticket:     "SQU-220",
		Error:      "daemon is not running",
	}); err != nil {
		t.Fatalf("append intake delivery: %v", err)
	}
	if err := appendIntakeDelivery(teamDir, intakeDelivery{
		ID:           "intake-recovered",
		Time:         time.Date(2026, 6, 19, 11, 0, 0, 0, time.UTC),
		Provider:     "linear",
		Status:       intakeDeliveryStatusError,
		ReplayStatus: intakeDeliveryReplayStatusOK,
		ReplayedAt:   &replayedAt,
		HTTPStatus:   503,
		EventType:    "ticket.created",
		Payload:      map[string]any{"source": "linear", "ticket": "SQU-221", "title": "Recovered intake"},
		Ticket:       "SQU-221",
		Error:        "daemon is not running",
	}); err != nil {
		t.Fatalf("append recovered intake delivery: %v", err)
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
	if body.Intake.Deliveries != 2 || body.Intake.Errors != 1 || body.Intake.Recovered != 1 || body.Intake.Replayable != 1 || body.Intake.LatestErrorID != "intake-health" {
		t.Fatalf("intake summary = %+v", body.Intake)
	}
	var sawIntakeIssue bool
	for _, issue := range body.Issues {
		if issue.Code == "intake_unresolved" {
			if !containsString(issue.Actions, "agent-team intake deliveries --unresolved") || !containsString(issue.Actions, "agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers") {
				t.Fatalf("intake issue actions = %+v", issue.Actions)
			}
			sawIntakeIssue = true
			break
		}
	}
	if !sawIntakeIssue {
		t.Fatalf("issues = %+v, missing intake_unresolved", body.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health text succeeded unexpectedly")
	}
	for _, want := range []string{"intake: deliveries=2 errors=1 recovered=1 replayable=1", "intake_unresolved", "agent-team intake deliveries --unresolved"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s", want, textOut.String())
		}
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
			if !containsString(issue.Actions, "agent-team job retry squ-91 --dispatch") {
				t.Fatalf("job issue actions = %+v", issue.Actions)
			}
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
	for _, want := range []string{"jobs: total=1", "attention=1", "job_attention", "job=squ-91", "action=agent-team job retry squ-91 --dispatch"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestHealthCommandJobsIncludesPipelineStatus(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topoFixture+`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
after = ["review"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, j := range []*job.Job{
		{
			ID:        "squ-93",
			Ticket:    "SQU-93",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusFailed,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusFailed},
			},
		},
		{
			ID:        "squ-94",
			Ticket:    "SQU-94",
			Target:    "worker",
			Pipeline:  "ticket_to_pr",
			Status:    job.StatusBlocked,
			CreatedAt: now,
			UpdatedAt: now,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusBlocked, After: []string{"review"}},
				{ID: "review", Target: "manager", Status: job.StatusBlocked, After: []string{"implement"}},
			},
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write %s: %v", j.ID, err)
		}
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
		t.Fatalf("decode health pipeline json: %v\nbody=%s stderr=%s", err, out.String(), stderr.String())
	}
	if len(body.PipelineStatus) != 1 || body.PipelineStatus[0].Pipeline != "ticket_to_pr" || body.PipelineStatus[0].FailedSteps != 1 || body.PipelineStatus[0].BlockedSteps != 1 {
		t.Fatalf("pipeline status = %+v", body.PipelineStatus)
	}
	var sawFailed, sawBlocked bool
	for _, issue := range body.Issues {
		switch issue.Code {
		case "pipeline_failed_step":
			sawFailed = containsString(issue.Actions, "agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes") &&
				containsString(issue.Actions, "agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes") &&
				containsString(issue.Actions, "agent-team repair --retry-pipelines --dry-run --preview-routes") &&
				containsString(issue.Actions, "agent-team pipeline explain ticket_to_pr --state failed") &&
				containsString(issue.Actions, "agent-team pipeline ready ticket_to_pr --state failed")
		case "pipeline_blocked_step":
			sawBlocked = containsString(issue.Actions, "agent-team pipeline explain ticket_to_pr --state blocked") &&
				containsString(issue.Actions, "agent-team pipeline ready ticket_to_pr --state blocked")
		}
	}
	if !sawFailed || !sawBlocked {
		t.Fatalf("issues = %+v, missing pipeline issues", body.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--jobs", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health --jobs text succeeded unexpectedly")
	}
	for _, want := range []string{"pipeline status: pipelines=1 jobs=2 ready_steps=0 manual_gates=0 stale_running_steps=0 failed_steps=1", "pipeline_failed_step", "pipeline_blocked_step", "agent-team pipeline retry ticket_to_pr --dry-run --dispatch --preview-routes", "agent-team pipeline repair ticket_to_pr --retry-pipelines --dry-run --preview-routes", "agent-team repair --retry-pipelines --dry-run --preview-routes", "agent-team pipeline explain ticket_to_pr --state failed", "agent-team pipeline ready ticket_to_pr --state failed", "agent-team pipeline explain ticket_to_pr --state blocked", "agent-team pipeline ready ticket_to_pr --state blocked"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("health text missing %q:\n%s", want, textOut.String())
		}
	}
}

func TestHealthCommandIncludesPipelineDoctorProblems(t *testing.T) {
	tmp := t.TempDir()
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
after = ["review"]

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "worker"
after = ["implement"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--json", "--target", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("health succeeded unexpectedly")
	}
	var ec ExitCode
	if !errors.As(err, &ec) || int(ec) != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	var body healthResult
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("decode health pipeline doctor json: %v\nbody=%s stderr=%s", err, out.String(), stderr.String())
	}
	if body.PipelineDoctor == nil || len(body.PipelineDoctor.Problems) != 1 || body.PipelineDoctor.Problems[0].Code != "dependency_cycle" {
		t.Fatalf("pipeline doctor = %+v", body.PipelineDoctor)
	}
	var sawPipelineIssue bool
	for _, issue := range body.Issues {
		if issue.Code == "pipeline_dependency_cycle" && containsString(issue.Actions, "agent-team pipeline doctor ticket_to_pr") {
			sawPipelineIssue = true
		}
	}
	if !sawPipelineIssue {
		t.Fatalf("issues = %+v, missing pipeline_dependency_cycle", body.Issues)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"health", "--target", tmp})
	if err := text.Execute(); err == nil {
		t.Fatal("health text succeeded unexpectedly")
	}
	for _, want := range []string{"pipeline doctor: pipelines=1 problems=1 warnings=1", "pipeline_dependency_cycle", "agent-team pipeline doctor ticket_to_pr"} {
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
			if !containsString(issue.Actions, "agent-team job unblock squ-92 <answer...>") {
				t.Fatalf("blocked status issue actions = %+v", issue.Actions)
			}
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
	for _, want := range []string{"job status: previews=1 changes=1 blocked=1", "job_status_blocked", "job=squ-92", "action=agent-team job unblock squ-92 <answer...>"} {
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

func TestHealthCommandUnhealthyIncludesRuntimeStaleIssue(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Job: "SQU-88", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

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
	if len(body.Instances) != 1 || body.Instances[0].Instance != "runtime-stale" || !body.Instances[0].RuntimeStale || !body.Instances[0].Unhealthy {
		t.Fatalf("instances = %+v, want one runtime-stale unhealthy row", body.Instances)
	}
	if body.Summary.Total != 1 || body.Summary.RuntimeStale != 1 || body.Summary.Unhealthy != 1 || body.Summary.Stale != 0 {
		t.Fatalf("summary = %+v, want one runtime-stale unhealthy row", body.Summary)
	}
	for _, issue := range body.Issues {
		if issue.Code == "runtime_stale" && issue.Instance == "runtime-stale" {
			if !containsString(issue.Actions, "agent-team job resume-plan squ-88 --runtime-stale") {
				t.Fatalf("runtime stale actions = %+v, want job resume-plan action", issue.Actions)
			}
			return
		}
	}
	t.Fatalf("issues = %+v, missing runtime_stale issue", body.Issues)
}

func TestHealthCommandLastMessageRewritesRuntimeRecoveryActions(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Job: "SQU-88", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--unhealthy", "--last-message", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1 from runtime-stale health\nstderr=%s", err, stderr.String())
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode health json: %v\nbody=%s", err, stdout.String())
	}
	var sawLastMessage bool
	for _, issue := range body.Issues {
		if issue.Code == "runtime_stale" && issue.Instance == "runtime-stale" {
			sawLastMessage = containsString(issue.Actions, "agent-team job resume-plan squ-88 --runtime-stale --last-message")
		}
	}
	if !sawLastMessage {
		t.Fatalf("runtime stale last-message action missing: %+v", body.Issues)
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"health", "--unhealthy", "--last-message", "--commands", "--target", tmp})
	if err := commands.Execute(); !errors.As(err, &code) || code != 1 {
		t.Fatalf("health commands err = %v, want exit 1\nstderr=%s", err, commandsErr.String())
	}
	wantCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", tmp, "job", "resume-plan", "squ-88", "--runtime-stale", "--last-message"}), " ")
	if !strings.Contains(commandsOut.String(), wantCommand) {
		t.Fatalf("health last-message commands missing %q:\n%s", wantCommand, commandsOut.String())
	}
}

func TestHealthCommandRuntimeStaleFilterOnlyIncludesRuntimeStaleInstances(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, PID: 99999998, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"health", "--runtime-stale", "--json", "--target", tmp})
	err := cmd.Execute()
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("err = %v, want exit 1 from runtime-stale health\nstderr=%s", err, stderr.String())
	}
	var body healthResult
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode health json: %v\nbody=%s", err, stdout.String())
	}
	if len(body.Instances) != 1 || body.Instances[0].Instance != "runtime-stale" || !body.Instances[0].RuntimeStale || !body.Instances[0].Unhealthy {
		t.Fatalf("instances = %+v, want one runtime-stale row only", body.Instances)
	}
	for _, issue := range body.Issues {
		if issue.Instance == "crashed" || issue.Instance == "fresh" {
			t.Fatalf("issues = %+v, want non-runtime-stale instances filtered out", body.Issues)
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
		{[]string{"health", "--commands", "--json"}, "--commands cannot be combined with --json"},
		{[]string{"health", "--commands", "--format", "{{.Healthy}}"}, "--commands cannot be combined with --format"},
		{[]string{"health", "--commands", "--quiet"}, "--commands cannot be combined with --quiet"},
		{[]string{"health", "--commands", "--watch"}, "--commands cannot be combined with --watch"},
		{[]string{"health", "--commands", "--wait"}, "--commands cannot be combined with --wait"},
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

func TestHealthWatchJSONIncludesActions(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := tmp + "/.agent_team"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runHealthWatchWithClear(ctx, &buf, teamDir, time.Millisecond, time.Now, true, healthOptions{}, false); err != nil {
		t.Fatalf("runHealthWatchWithClear json: %v", err)
	}
	first := strings.Split(strings.TrimSpace(buf.String()), "\n")[0]
	var body healthResult
	if err := json.Unmarshal([]byte(first), &body); err != nil {
		t.Fatalf("decode first health watch json: %v\nbody=%s", err, first)
	}
	if !containsString(body.Actions, "agent-team sync --dry-run") {
		t.Fatalf("health watch json actions = %+v, want sync dry-run", body.Actions)
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
