package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/budget"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestReconcile_LiveProcessStaysRunning(t *testing.T) {
	root := t.TempDir()
	// Use the test process's own PID as a guaranteed-alive PID.
	if err := WriteMetadata(root, &Metadata{
		Instance:  "alive",
		Agent:     "x",
		Workspace: "/tmp",
		PID:       os.Getpid(),
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	m := NewInstanceManager(root, nil)
	if err := Reconcile(root, m); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	disk, err := ReadMetadata(root, "alive")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Status != StatusRunning {
		t.Errorf("status: got %s want running", disk.Status)
	}
	got := m.List()
	if len(got) != 1 {
		t.Errorf("manager map: want 1, got %d", len(got))
	}
}

func TestReconcile_PreservesReaperForManagerSpawnedProcess(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(2 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	meta, err := m.Dispatch(DispatchInput{
		Agent:     "worker",
		Name:      "worker-squ-1",
		Prompt:    "hello",
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := Reconcile(root, m); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := m.Stop(meta.Instance); err != nil {
		t.Fatalf("stop after reconcile: %v", err)
	}
	if err := m.WaitForReaper(meta.Instance, 10*time.Second); err != nil {
		t.Fatalf("wait reaper after reconcile: %v", err)
	}
}

func TestReconcile_DeadProcessMarkedExited(t *testing.T) {
	root := t.TempDir()
	// Pick a PID that's almost certainly not in use. PID 1 (init) is alive
	// but we can't kill it; we want one that's gone. Use 999_999_999 — far
	// above any realistic PID.
	if err := WriteMetadata(root, &Metadata{
		Instance:  "dead",
		Agent:     "x",
		Workspace: "/tmp",
		PID:       999_999_999,
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	m := NewInstanceManager(root, nil)
	if err := Reconcile(root, m); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	disk, err := ReadMetadata(root, "dead")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Status != StatusExited {
		t.Errorf("status: got %s want exited", disk.Status)
	}
	if disk.ExitedAt.IsZero() {
		t.Errorf("ExitedAt not set")
	}
}

func TestReconcile_StoppedAndExitedUntouched(t *testing.T) {
	root := t.TempDir()
	for _, st := range []Status{StatusStopped, StatusExited, StatusCrashed} {
		instance := "i-" + string(st)
		if err := WriteMetadata(root, &Metadata{
			Instance:  instance,
			Agent:     "x",
			Workspace: "/tmp",
			PID:       1,
			Status:    st,
			StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	m := NewInstanceManager(root, nil)
	if err := Reconcile(root, m); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, st := range []Status{StatusStopped, StatusExited, StatusCrashed} {
		disk, _ := ReadMetadata(root, "i-"+string(st))
		if disk.Status != st {
			t.Errorf("%s changed to %s", st, disk.Status)
		}
	}
}

func TestReconcile_PidLiveCheckReportsZero(t *testing.T) {
	if PidLiveCheck(0) {
		t.Errorf("PID 0 should not be live")
	}
	if PidLiveCheck(-1) {
		t.Errorf("negative PID should not be live")
	}
	if !PidLiveCheck(os.Getpid()) {
		t.Errorf("self should be live")
	}
}

func TestReconcileWithTopology_RestartPolicyMatrix(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	now := time.Now().UTC()
	exit0 := 0
	exit1 := 1
	for _, meta := range []*Metadata{
		{Instance: "never-crashed", Agent: "manager", Workspace: t.TempDir(), Status: StatusCrashed, SessionID: "sid-never", StartedAt: now},
		{Instance: "failure-crashed", Agent: "manager", Workspace: t.TempDir(), Status: StatusCrashed, SessionID: "sid-failure-crashed", StartedAt: now},
		{Instance: "failure-clean", Agent: "manager", Workspace: t.TempDir(), Status: StatusExited, ExitCode: &exit0, SessionID: "sid-failure-clean", StartedAt: now},
		{Instance: "failure-nonzero", Agent: "manager", Workspace: t.TempDir(), Status: StatusExited, ExitCode: &exit1, SessionID: "sid-failure-nonzero", StartedAt: now},
		{Instance: "always-clean", Agent: "manager", Workspace: t.TempDir(), Status: StatusExited, ExitCode: &exit0, SessionID: "sid-always-clean", StartedAt: now},
	} {
		if err := WriteMetadata(root, meta); err != nil {
			t.Fatalf("write %s: %v", meta.Instance, err)
		}
	}
	topo := mustParseCustomTopo(t, `
[instances.never-crashed]
agent = "manager"
restart = "never"

[instances.failure-crashed]
agent = "manager"
restart = "on-failure"

[instances.failure-clean]
agent = "manager"
restart = "on-failure"

[instances.failure-nonzero]
agent = "manager"
restart = "on-failure"

[instances.always-clean]
agent = "manager"
restart = "always"
`)

	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got, want := fake.callCount(), 3; got != want {
		t.Fatalf("spawn calls = %d, want %d", got, want)
	}
	for _, name := range []string{"failure-crashed", "failure-nonzero", "always-clean"} {
		disk, err := ReadMetadata(root, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if disk.Status != StatusRunning {
			t.Fatalf("%s status = %s, want running", name, disk.Status)
		}
		if !disk.RestartBackoffUntil.IsZero() {
			t.Fatalf("%s restart backoff should be cleared, got %s", name, disk.RestartBackoffUntil)
		}
		_, _ = m.Stop(name)
		_ = m.WaitForReaper(name, 2*time.Second)
	}
	for _, name := range []string{"never-crashed", "failure-clean"} {
		disk, err := ReadMetadata(root, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if disk.Status == StatusRunning {
			t.Fatalf("%s restarted unexpectedly", name)
		}
	}
}

func TestReconcileWithTopology_PersistsCappedRestartBackoff(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	spawnErr := errors.New("spawn failed")
	m := NewInstanceManager(root, func(args []string, env []string, workspace, stdoutPath, stderrPath, stdin string) (*os.Process, error) {
		return nil, spawnErr
	})
	now := time.Now().UTC()
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: t.TempDir(),
		Status:    StatusCrashed,
		SessionID: "sid-manager",
		StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	topo := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
restart = "on-failure"
`)
	oldDelay := restartBackoffDelay
	restartBackoffDelay = 10 * time.Minute
	t.Cleanup(func() { restartBackoffDelay = oldDelay })

	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	disk, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatal(err)
	}
	if !disk.RestartBackoffUntil.After(now) {
		t.Fatalf("restart_backoff_until = %s, want after %s", disk.RestartBackoffUntil, now)
	}
	if disk.RestartBackoffUntil.After(now.Add(restartBackoffCap + time.Second)) {
		t.Fatalf("restart_backoff_until = %s, want capped within %s", disk.RestartBackoffUntil, restartBackoffCap)
	}
}

func TestReconcileWithTopology_SkipsWhileRestartBackoffActive(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	if err := WriteMetadata(root, &Metadata{
		Instance:            "manager",
		Agent:               "manager",
		Workspace:           t.TempDir(),
		Status:              StatusCrashed,
		SessionID:           "sid-manager",
		StartedAt:           time.Now().UTC(),
		RestartBackoffUntil: time.Now().UTC().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	topo := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
restart = "always"
`)
	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("spawn calls = %d, want 0 while backoff active", got)
	}
}

func TestReconcileWithTopology_AdoptedWatcherMarksExitPromptly(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	var live atomic.Bool
	live.Store(true)
	restorePIDLiveCheck := SetPidLiveCheckForTest(func(pid int) bool { return live.Load() })
	oldInterval := adoptedPollInterval
	adoptedPollInterval = 10 * time.Millisecond
	var m *InstanceManager
	t.Cleanup(func() {
		live.Store(false)
		if m != nil {
			_ = m.WaitForReaper("manager", time.Second)
		}
		restorePIDLiveCheck()
		adoptedPollInterval = oldInterval
	})
	started := time.Now().UTC()
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: t.TempDir(),
		PID:       4242,
		Status:    StatusRunning,
		StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	m = NewInstanceManager(root, nil)
	topo := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
restart = "never"
`)
	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	live.Store(false)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		disk, err := ReadMetadata(root, "manager")
		if err != nil {
			t.Fatal(err)
		}
		if disk.Status == StatusExited && !disk.ExitedAt.IsZero() {
			if err := m.WaitForReaper("manager", time.Second); err != nil {
				t.Fatalf("wait watcher: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	disk, _ := ReadMetadata(root, "manager")
	t.Fatalf("status = %s exited_at=%s, want exited promptly", disk.Status, disk.ExitedAt)
}

func TestReconcileWithTopology_ReprobePreventsDuplicateForAdoptedSurvivor(t *testing.T) {
	teamDir := restartFixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	var checks atomic.Int32
	var live atomic.Bool
	live.Store(true)
	restorePIDLiveCheck := SetPidLiveCheckForTest(func(pid int) bool {
		return live.Load() && checks.Add(1) > 1
	})
	oldInterval := adoptedPollInterval
	adoptedPollInterval = 10 * time.Millisecond
	var m *InstanceManager
	var topo *topology.Topology
	t.Cleanup(func() {
		if topo != nil && topo.Instances["manager"] != nil {
			topo.Instances["manager"].Restart = topology.RestartNever
		}
		live.Store(false)
		if m != nil {
			_ = m.WaitForReaper("manager", time.Second)
		}
		restorePIDLiveCheck()
		adoptedPollInterval = oldInterval
	})
	started := time.Now().UTC()
	if err := WriteMetadata(root, &Metadata{
		Instance:  "manager",
		Agent:     "manager",
		Workspace: t.TempDir(),
		PID:       7777,
		Status:    StatusRunning,
		SessionID: "sid-manager",
		StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m = NewInstanceManager(root, fake.spawn)
	topo = mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
restart = "on-failure"
`)
	if err := ReconcileWithTopology(teamDir, m, topo); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("spawn calls = %d, want 0 after live re-probe", got)
	}
	disk, err := ReadMetadata(root, "manager")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Status != StatusRunning || !disk.ExitedAt.IsZero() {
		t.Fatalf("metadata = %+v, want revived running survivor", disk)
	}
	topo.Instances["manager"].Restart = topology.RestartNever
	live.Store(false)
	if err := m.WaitForReaper("manager", time.Second); err != nil {
		t.Fatalf("wait revived watcher: %v", err)
	}
}

func TestReconcileWithTopology_ReleasesReserveAllocationAndDrainsBudgetQueue(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true
replicas = 2

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[teams.delivery]
instances = ["worker"]

[budgets.delivery]
tokens_per_day = 100
allocation = "reserve"
`)
	now := time.Now().UTC()
	firstJob, err := jobstore.New("SQU-510", "worker", "reserve crash", now)
	if err != nil {
		t.Fatalf("job.New first: %v", err)
	}
	firstJob.Status = jobstore.StatusRunning
	firstJob.Instance = "worker-squ-510"
	firstJob.Origin = origin.Envelope{Team: "delivery", Job: firstJob.ID, Instance: firstJob.Instance}
	if err := jobstore.Write(teamDir, firstJob); err != nil {
		t.Fatalf("write first job: %v", err)
	}
	grant, err := budget.GrantTokens(teamDir, top, budget.GrantRequest{
		Team:     "delivery",
		JobID:    firstJob.ID,
		Instance: firstJob.Instance,
		Tokens:   60,
		Now:      now,
		Origin:   firstJob.Origin,
	})
	if err != nil {
		t.Fatalf("grant first reserve: %v", err)
	}
	if !grant.Allowed || grant.Allocation == nil {
		t.Fatalf("grant = %+v, want durable reserve allocation", grant)
	}
	if err := WriteMetadata(root, &Metadata{
		Instance:  firstJob.Instance,
		Agent:     "worker",
		Job:       firstJob.ID,
		Ticket:    firstJob.Ticket,
		Workspace: t.TempDir(),
		PID:       999_999_999,
		Status:    StatusRunning,
		StartedAt: now,
		Origin:    firstJob.Origin,
	}); err != nil {
		t.Fatalf("write first metadata: %v", err)
	}
	secondPayload := map[string]any{
		"target":        "worker",
		"name":          "worker-squ-511",
		"ticket":        "SQU-511",
		"budget_tokens": "60",
	}
	secondOrigin := origin.Envelope{Team: "delivery", Job: "squ-511", Instance: "worker-squ-511"}
	if err := WriteQueueItem(root, queueItemFromEvent("worker", &queuedEvent{
		id:         "reserve-waiter",
		eventType:  topology.EventAgentDispatch,
		payload:    secondPayload,
		queuedAt:   now,
		uniqueName: "worker-squ-511",
		reason:     QueueReasonBudgetExhausted,
		origin:     secondOrigin,
	}, QueueStatePending)); err != nil {
		t.Fatalf("write queued reserve waiter: %v", err)
	}

	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	NewEventResolver(m, teamDir, top)

	if err := ReconcileWithTopology(teamDir, m, top); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := fake.callCount(); got != 1 {
		t.Fatalf("spawn calls = %d, want queued reserve waiter dispatched", got)
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("list queue: %v", err)
	}
	for _, item := range items {
		if item.InstanceID == "worker-squ-511" && item.State == QueueStatePending {
			t.Fatalf("reserve waiter remained queued after reconcile: %+v", item)
		}
	}
	records, err := budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	var firstReleased, secondOutstanding bool
	var detail []string
	for _, rec := range records {
		detail = append(detail, rec.JobID+"/"+rec.Instance+"/"+rec.Status)
		if rec.JobID == firstJob.ID && rec.Instance == firstJob.Instance && rec.Status == budget.AllocationStatusReleased {
			firstReleased = true
		}
		if rec.JobID == "squ-511" && rec.Instance == "worker-squ-511" && rec.Status == budget.AllocationStatusOutstanding {
			secondOutstanding = true
		}
	}
	if !firstReleased || !secondOutstanding {
		t.Fatalf("allocations = %v, want first released and second outstanding", detail)
	}
	updated, err := jobstore.Read(teamDir, firstJob.ID)
	if err != nil {
		t.Fatalf("read first job: %v", err)
	}
	if updated.Status != jobstore.StatusDone {
		t.Fatalf("first job status = %s, want done", updated.Status)
	}

	_, _ = m.Stop("worker-squ-511")
	_ = m.WaitForReaper("worker-squ-511", 2*time.Second)
}

func TestLaunchDeclaredFreshPrependsBrief(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "claude")
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"
restart = "always"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	meta, launched, err := launchDeclaredFresh(teamDir, m, topo, topo.Find("manager"), nil)
	if err != nil {
		t.Fatalf("launch declared fresh: %v", err)
	}
	if !launched || meta == nil {
		t.Fatalf("launch result meta=%+v launched=%t", meta, launched)
	}
	args := fake.lastCall()
	promptFile := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--append-system-prompt-file" {
			promptFile = args[i+1]
			break
		}
	}
	if promptFile == "" {
		t.Fatalf("prompt file arg missing from %v", args)
	}
	body, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("read prompt file: %v", err)
	}
	if !strings.Contains(string(body), "# Instance brief: manager") || !strings.Contains(string(body), "--- runtime kickoff ---") {
		t.Fatalf("prompt missing prepended brief:\n%s", string(body))
	}

	_, _ = m.Stop("manager")
	waitForStatusNot(t, m, "manager", StatusRunning)
}

func TestLaunchDeclaredFreshUsesPersistedLaunchEnvSnapshot(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "claude")
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"
restart = "always"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(teamDir, "state", "manager")
	root := DaemonRoot(teamDir)
	if err := WriteInstanceLaunchEnv(root, "manager", &LaunchEnv{
		Bin:  "claude",
		Args: []string{"claude", "--session-id", "old"},
		Dir:  filepath.Dir(teamDir),
		Env: []string{
			"MARKER=dispatch",
			"OPENAI_API_KEY=must-not-persist",
			"AGENT_TEAM_ROOT=" + teamDir,
			"AGENT_TEAM_INSTANCE=manager",
			"AGENT_TEAM_STATE_DIR=" + stateDir,
		},
		RecordedAt: time.Now().UTC(),
		Version:    1,
	}); err != nil {
		t.Fatalf("write instance launch env: %v", err)
	}
	t.Setenv("MARKER", "current-after-dispatch")
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	meta, launched, err := launchDeclaredFresh(teamDir, m, topo, topo.Find("manager"), nil)
	if err != nil {
		t.Fatalf("launch declared fresh: %v", err)
	}
	if !launched || meta == nil {
		t.Fatalf("launch result meta=%+v launched=%t", meta, launched)
	}
	env := fake.lastEnv()
	if !containsString(env, "MARKER=dispatch") {
		t.Fatalf("relaunch env missing dispatch marker: %+v", env)
	}
	if containsString(env, "MARKER=current-after-dispatch") {
		t.Fatalf("relaunch env used current shell marker instead of snapshot: %+v", env)
	}
	if envHasKey(env, "OPENAI_API_KEY") {
		t.Fatalf("relaunch env included stripped denied key: %+v", env)
	}

	_, _ = m.Stop("manager")
	waitForStatusNot(t, m, "manager", StatusRunning)
}

func TestLaunchDeclaredFreshStripsStaleOTelSnapshotWhenDisabled(t *testing.T) {
	t.Setenv(runtimebin.EnvRuntime, "claude")
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[instances.manager]
agent = "manager"
restart = "always"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`
[otel]
enabled = false
`), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(teamDir, "state", "manager")
	root := DaemonRoot(teamDir)
	if err := WriteInstanceLaunchEnv(root, "manager", &LaunchEnv{
		Bin:  "claude",
		Args: []string{"claude", "--session-id", "old"},
		Dir:  filepath.Dir(teamDir),
		Env: []string{
			"MARKER=dispatch",
			"AGENT_TEAM_ROOT=" + teamDir,
			"AGENT_TEAM_INSTANCE=manager",
			"AGENT_TEAM_STATE_DIR=" + stateDir,
			"CLAUDE_CODE_ENABLE_TELEMETRY=1",
			"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1",
			"OTEL_EXPORTER_OTLP_ENDPOINT=http://old-collector:4318",
			"OTEL_TRACES_EXPORTER=otlp",
			"OTEL_RESOURCE_ATTRIBUTES=old=true",
			"TRACEPARENT=00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01",
			"TRACESTATE=old",
			"AGENTTEAM_OTEL_HEADER_0=old-secret",
		},
		RecordedAt: time.Now().UTC(),
		Version:    1,
	}); err != nil {
		t.Fatalf("write instance launch env: %v", err)
	}
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	meta, launched, err := launchDeclaredFresh(teamDir, m, topo, topo.Find("manager"), nil)
	if err != nil {
		t.Fatalf("launch declared fresh: %v", err)
	}
	if !launched || meta == nil {
		t.Fatalf("launch result meta=%+v launched=%t", meta, launched)
	}
	env := fake.lastEnv()
	if !containsString(env, "MARKER=dispatch") {
		t.Fatalf("relaunch env missing dispatch marker: %+v", env)
	}
	for _, key := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_TRACES_EXPORTER",
		"OTEL_RESOURCE_ATTRIBUTES",
		"TRACEPARENT",
		"TRACESTATE",
		"AGENTTEAM_OTEL_HEADER_0",
	} {
		if envHasKey(env, key) {
			t.Fatalf("relaunch env kept stale %s: %+v", key, env)
		}
	}

	_, _ = m.Stop("manager")
	waitForStatusNot(t, m, "manager", StatusRunning)
}

func restartFixtureTeamDir(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	return teamDir
}
