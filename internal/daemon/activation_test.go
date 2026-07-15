package daemon

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
)

func TestActivationRejectsStaleCLIForScheduledTeamAuthorityBeforeSpawn(t *testing.T) {
	topologyText := `
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "schedule"
match.name = "activation-check"

[schedules.activation-check]
every = "1h"
run_on_start = true

[schedules.activation-check.payload]
kind = "activation"

[teams.platform]
instances = ["worker"]
schedules = ["activation-check"]

[authority]
enforcement = "enforce"

[authority.instances.worker]
allow = ["job.gate.*:team"]
`
	fixture := newProductionActivationFixture(t, topologyText)
	fixture.useCLI(t, fixture.staleCLI)
	top := mustParseCustomTopo(t, topologyText)
	fake := newFakeSpawner(100 * time.Millisecond)
	mgr := NewInstanceManager(DaemonRoot(fixture.teamDir), fake.spawn)
	resolver := NewEventResolver(mgr, fixture.teamDir, top)
	setActivationContextForTest(resolver, fixture.activationContext())
	status := resolver.activationStatus()
	if status.State != ActivationStateNeeded || !strings.Contains(strings.Join(status.Reasons, "\n"), "managed CLI") {
		t.Fatalf("production stale CLI verdict = %+v", status)
	}

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	state := map[string]*ScheduleState{}
	_, err := resolver.fireDueSchedulesWithResult(now, state, false, nil)
	if err == nil || !strings.Contains(err.Error(), "activation needed") || !strings.Contains(err.Error(), "managed CLI") {
		t.Fatalf("scheduled stale tuple error = %v", err)
	}
	if len(state) != 0 {
		t.Fatalf("scheduled stale tuple consumed in-memory clock: %+v", state)
	}
	if _, err := ReadScheduleState(mgr.daemonRoot, "activation-check"); !os.IsNotExist(err) {
		t.Fatalf("scheduled stale tuple persisted a clock, err=%v", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("scheduled stale tuple spawned %d process(es), want 0", got)
	}
	_, err = mgr.Dispatch(DispatchInput{Agent: "worker", Name: "direct-worker", Workspace: filepath.Dir(fixture.teamDir)})
	if err == nil || !strings.Contains(err.Error(), "activation needed") {
		t.Fatalf("direct stale tuple error = %v", err)
	}
	if got := fake.callCount(); got != 0 {
		t.Fatalf("direct stale tuple spawned %d process(es), want 0", got)
	}

	fixture.useCLI(t, fixture.currentCLI)
	fired, err := resolver.fireDueSchedulesWithResult(now, state, false, nil)
	if err != nil {
		t.Fatalf("coherent retry of blocked schedule: %v", err)
	}
	if fired.Fired != 1 || len(state) != 1 || fake.callCount() != 1 {
		t.Fatalf("coherent retry result=%+v state=%+v spawn_calls=%d", fired, state, fake.callCount())
	}
}

func TestActivationBlockDoesNotConsumeQueueAttempt(t *testing.T) {
	topologyText := `
[instances.worker]
agent = "worker"
ephemeral = true
`
	fixture := newProductionActivationFixture(t, topologyText)
	fixture.useCLI(t, fixture.staleCLI)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	item := &QueueItem{
		ID:         "activation-blocked",
		State:      QueueStatePending,
		EventType:  "agent.dispatch",
		Instance:   "worker",
		InstanceID: "worker-activation-blocked",
		Payload: map[string]any{
			"target":    "worker",
			"name":      "worker-activation-blocked",
			"workspace": "repo",
		},
		Attempts:  MaxQueueAttempts - 1,
		QueuedAt:  now,
		UpdatedAt: now,
	}
	root := DaemonRoot(fixture.teamDir)
	if err := WriteQueueItem(root, item); err != nil {
		t.Fatal(err)
	}
	top := mustParseCustomTopo(t, topologyText)
	fake := newFakeSpawner(100 * time.Millisecond)
	mgr := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(mgr, fixture.teamDir, top)
	setActivationContextForTest(resolver, fixture.activationContext())
	if status := resolver.activationStatus(); status.State != ActivationStateNeeded {
		t.Fatalf("production stale queue verdict = %+v", status)
	}

	result, err := resolver.DrainQueuesWithResult()
	if err == nil || !strings.Contains(err.Error(), "activation needed") {
		t.Fatalf("stale queue drain result=%+v err=%v", result, err)
	}
	if result == nil || result.Attempted != 0 || result.Dispatched != 0 || result.Rejected != 0 {
		t.Fatalf("stale queue drain consumed work: %+v", result)
	}
	pending, err := ReadQueueItem(root, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State != QueueStatePending || pending.Attempts != MaxQueueAttempts-1 || pending.LastError != "" {
		t.Fatalf("blocked queue item = %+v, want unchanged pending attempt", pending)
	}
	if running, queued := resolver.QueueDepth("worker"); running != 0 || queued != 1 {
		t.Fatalf("blocked queue depth running=%d queued=%d, want 0/1", running, queued)
	}
	if fake.callCount() != 0 {
		t.Fatalf("blocked queue spawned %d process(es), want 0", fake.callCount())
	}
	if _, err := resolver.RetryQueueItem(item.ID); err == nil || !strings.Contains(err.Error(), "activation needed") {
		t.Fatalf("stale explicit queue retry error = %v", err)
	}
	pending, err = ReadQueueItem(root, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State != QueueStatePending || pending.Attempts != MaxQueueAttempts-1 || pending.LastError != "" {
		t.Fatalf("blocked explicit retry changed queue item: %+v", pending)
	}

	fixture.useCLI(t, fixture.currentCLI)
	result, err = resolver.DrainQueuesWithResult()
	if err != nil {
		t.Fatalf("coherent retry of blocked queue: %v", err)
	}
	if result.Attempted != 1 || result.Dispatched != 1 || result.Rejected != 0 || fake.callCount() != 1 {
		t.Fatalf("coherent queue retry result=%+v spawn_calls=%d", result, fake.callCount())
	}
	if _, err := ReadQueueItem(root, item.ID); !os.IsNotExist(err) {
		t.Fatalf("coherent queue retry did not consume pending work, err=%v", err)
	}
}

func TestActivationAllowsCoherentScheduledAndPersistentLaunches(t *testing.T) {
	t.Run("scheduled", func(t *testing.T) {
		topologyText := `
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "schedule"
match.name = "activation-check"

[schedules.activation-check]
every = "1h"
run_on_start = true

[authority]
enforcement = "enforce"

[authority.instances.worker]
allow = ["job.gate.*:team"]
`
		fixture := newProductionActivationFixture(t, topologyText)
		fixture.useCLI(t, fixture.currentCLI)
		top := mustParseCustomTopo(t, topologyText)
		fake := newFakeSpawner(100 * time.Millisecond)
		mgr := NewInstanceManager(DaemonRoot(fixture.teamDir), fake.spawn)
		resolver := NewEventResolver(mgr, fixture.teamDir, top)
		setActivationContextForTest(resolver, fixture.activationContext())
		if status := resolver.activationStatus(); !status.Coherent() {
			t.Fatalf("production coherent scheduled verdict = %+v", status)
		}

		result, err := resolver.FireDueSchedulesWithResult(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("coherent scheduled launch: %v", err)
		}
		if result.Fired != 1 || fake.callCount() != 1 {
			t.Fatalf("coherent scheduled result=%+v spawn_calls=%d", result, fake.callCount())
		}
	})

	t.Run("persistent", func(t *testing.T) {
		topologyText := `
[instances.manager]
agent = "manager"
ephemeral = false

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["job.gate.*:team", "job.merge:team"]
`
		fixture := newProductionActivationFixture(t, topologyText)
		fixture.useCLI(t, fixture.currentCLI)
		top := mustParseCustomTopo(t, topologyText)
		fake := newFakeSpawner(100 * time.Millisecond)
		mgr := NewInstanceManager(DaemonRoot(fixture.teamDir), fake.spawn)
		resolver := NewEventResolver(mgr, fixture.teamDir, top)
		setActivationContextForTest(resolver, fixture.activationContext())
		if status := resolver.activationStatus(); !status.Coherent() {
			t.Fatalf("production coherent persistent verdict = %+v", status)
		}

		meta, launched, err := launchDeclaredFreshWithPrompt(fixture.teamDir, mgr, top, top.Find("manager"), nil, "coherent control")
		if err != nil {
			t.Fatalf("coherent persistent launch: %v", err)
		}
		if !launched || meta == nil || fake.callCount() != 1 {
			t.Fatalf("coherent persistent meta=%+v launched=%t spawn_calls=%d", meta, launched, fake.callCount())
		}

		if err := os.WriteFile(filepath.Join(fixture.teamDir, "config.toml"), []byte("# changed after activation\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if status := resolver.activationStatus(); status.State != ActivationStateNeeded || !strings.Contains(strings.Join(status.Reasons, "\n"), "assets changed") {
			t.Fatalf("production stale asset verdict = %+v", status)
		}
		_, _, err = launchDeclaredFreshWithPrompt(fixture.teamDir, mgr, top, top.Find("manager"), nil, "stale control")
		if err == nil || !strings.Contains(err.Error(), "activation needed") {
			t.Fatalf("persistent stale tuple error = %v", err)
		}
		if got := fake.callCount(); got != 1 {
			t.Fatalf("persistent stale tuple spawned process; calls=%d", got)
		}
	})

	t.Run("persistent resume regenerates stale bundle", func(t *testing.T) {
		teamDir := fixtureTeamDir(t)
		writeFixtureAgent(t, teamDir, "manager")
		topologyText := `
[instances.manager]
agent = "manager"
ephemeral = false
restart = "on-failure"

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["job.gate.*:team", "job.merge:team"]
`
		if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topologyText), 0o644); err != nil {
			t.Fatal(err)
		}
		top := mustParseCustomTopo(t, topologyText)
		fake := newFakeSpawner(100 * time.Millisecond)
		root := DaemonRoot(teamDir)
		mgr := NewInstanceManager(root, fake.spawn)
		resolver := NewEventResolver(mgr, teamDir, top)
		coherent := coherentActivationForTest(t)
		setActivationForTest(resolver, coherent)
		meta := &Metadata{
			Instance:      "manager",
			Agent:         "manager",
			Runtime:       "codex",
			RuntimeBinary: "codex",
			Workspace:     filepath.Dir(teamDir),
			SessionID:     "old-session",
			Status:        StatusStopped,
			StartedAt:     time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
		}
		if err := WriteMetadata(root, meta); err != nil {
			t.Fatal(err)
		}
		if err := WriteInstanceLaunchEnv(root, "manager", &LaunchEnv{
			Bin:     "codex",
			Args:    []string{"codex", "resume", "old-session"},
			Dir:     filepath.Dir(teamDir),
			Env:     os.Environ(),
			Version: 1,
			Build:   coherent.Daemon,
			Assets:  "stale-assets",
		}); err != nil {
			t.Fatal(err)
		}
		if err := mgr.ensureTracked("manager", meta); err != nil {
			t.Fatal(err)
		}

		status := resolver.activationStatus()
		if status.State != ActivationStateNeeded || len(status.StaleInstances) != 1 || status.StaleInstances[0] != "manager" {
			t.Fatalf("stale persistent status = %+v", status)
		}
		started, err := mgr.Start("manager")
		if err != nil {
			t.Fatalf("fresh fallback from stale resume: %v", err)
		}
		if started == nil || fake.callCount() != 1 {
			t.Fatalf("fresh fallback meta=%+v spawn_calls=%d", started, fake.callCount())
		}
		if args := strings.Join(fake.lastCall(), " "); strings.Contains(args, "old-session") {
			t.Fatalf("stale session was resumed instead of regenerated: %s", args)
		}
		snapshot, err := ReadInstanceLaunchEnv(root, "manager")
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Assets != coherent.LoadedAssets || !buildinfo.SameRevision(snapshot.Build, coherent.Daemon) {
			t.Fatalf("regenerated activation provenance = %+v", snapshot)
		}
	})
}

func TestActivationAllowsRevisionlessSiblingBuilds(t *testing.T) {
	topologyText := `
[instances.manager]
agent = "manager"
ephemeral = false

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "schedule"
match.name = "revisionless-check"

[schedules.revisionless-check]
every = "1h"
run_on_start = true

[authority]
enforcement = "enforce"

[authority.instances.manager]
allow = ["job.gate.*:team", "job.merge:team"]

[authority.instances.worker]
allow = ["job.gate.*:team"]
`
	buildRoot, cliPath, cliBuild, daemonBuild := buildRevisionlessSiblings(t)
	teamDir := filepath.Join(buildRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "manager")
	writeFixtureAgent(t, teamDir, "worker")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(topologyText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("# revisionless sibling fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cliBuild.Revision != "" || daemonBuild.Revision != "" {
		t.Fatalf("revisionless sibling fixture unexpectedly has VCS settings: cli=%+v daemon=%+v", cliBuild, daemonBuild)
	}
	if cliBuild.Cohort == "" || cliBuild.Cohort != daemonBuild.Cohort || cliBuild.BuildID == "" || daemonBuild.BuildID == "" {
		t.Fatalf("revisionless sibling identities are not comparable: cli=%+v daemon=%+v", cliBuild, daemonBuild)
	}
	t.Setenv("PATH", filepath.Dir(cliPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	loadedAssets, err := activationAssetDigest(teamDir)
	if err != nil {
		t.Fatal(err)
	}
	top := mustParseCustomTopo(t, topologyText)
	fake := newFakeSpawner(100 * time.Millisecond)
	mgr := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	t.Cleanup(func() {
		for _, running := range mgr.List() {
			_, _ = mgr.Stop(running.Instance)
			waitForStatusNot(t, mgr, running.Instance, StatusRunning)
		}
	})
	resolver := NewEventResolver(mgr, teamDir, top)
	setActivationContextForTest(resolver, activationContext{
		Build:        daemonBuild,
		LoadedAssets: loadedAssets,
		Inspect:      InspectActivation,
	})
	if status := resolver.activationStatus(); !status.Coherent() {
		t.Fatalf("revisionless sibling activation verdict = %+v, want coherent", status)
	}

	fired, err := resolver.FireDueSchedulesWithResult(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("revisionless scheduled launch: %v", err)
	}
	if fired.Fired != 1 || fake.callCount() != 1 {
		t.Fatalf("revisionless scheduled result=%+v spawn_calls=%d", fired, fake.callCount())
	}
	meta, launched, err := launchDeclaredFreshWithPrompt(teamDir, mgr, top, top.Find("manager"), nil, "revisionless sibling control")
	if err != nil {
		t.Fatalf("revisionless persistent launch: %v", err)
	}
	if !launched || meta == nil || fake.callCount() != 2 {
		t.Fatalf("revisionless persistent meta=%+v launched=%t spawn_calls=%d", meta, launched, fake.callCount())
	}
	buildInfoPath := filepath.Join(buildRoot, "internal", "buildinfo", "buildinfo.go")
	body, err := os.ReadFile(buildInfoPath)
	if err != nil {
		t.Fatal(err)
	}
	drifted := bytes.Replace(body, []byte("activation-coherence-v1"), []byte("activation-coherence-v2"), 1)
	if bytes.Equal(drifted, body) {
		t.Fatal("revisionless fixture cohort marker not found")
	}
	if err := os.WriteFile(buildInfoPath, drifted, 0o644); err != nil {
		t.Fatal(err)
	}
	if status := resolver.activationStatus(); status.State != ActivationStateNeeded || !strings.Contains(strings.Join(status.Reasons, "\n"), "current source tree") {
		t.Fatalf("revisionless stale-source verdict = %+v, want activation_needed", status)
	}
}

func TestBuildHandshakeRejectsStaleMutationsButLeavesStatusReadable(t *testing.T) {
	daemonBuild := buildinfo.Info{Version: "0.2.0", Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"}
	clientBuild := buildinfo.Info{Version: "0.1.0", Revision: "b062047f11111111111111111111111111111111"}
	called := 0
	handler := buildHandshakeHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}), daemonBuild, &bytes.Buffer{})

	blocked := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/v1/dispatch"},
		{method: http.MethodPost, path: "/v1/start"},
		{method: http.MethodPost, path: "/v1/restart"},
		{method: http.MethodPost, path: "/v1/interrupt"},
		{method: http.MethodPost, path: "/v1/reconcile"},
		{method: http.MethodPost, path: "/v1/team/spawn"},
		{method: http.MethodPost, path: "/v1/event"},
		{method: http.MethodPost, path: "/v1/intake/github"},
		{method: http.MethodPost, path: "/v1/outbox/drain"},
		{method: http.MethodPost, path: "/v1/queue/drain"},
		{method: http.MethodPost, path: "/v1/queue/queued-1/retry"},
		{method: http.MethodPost, path: "/v1/schedules/fire"},
		{method: http.MethodPost, path: "/v1/manager-wake/sweep"},
		{method: http.MethodPost, path: "/v1/topology/reload"},
	}
	for _, tc := range blocked {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set(buildinfo.HeaderName, clientBuild.HeaderValue())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "activation needed") {
			t.Fatalf("%s %s body=%s", tc.method, tc.path, rec.Body.String())
		}
	}
	if called != 0 {
		t.Fatalf("blocked launch routes reached downstream %d time(s)", called)
	}

	allowed := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/status"},
		{method: http.MethodPost, path: "/v1/stop"},
		{method: http.MethodPost, path: "/v1/message"},
		{method: http.MethodPost, path: "/v1/queue/queued-1/drop"},
		{method: http.MethodPost, path: "/v1/team/charters/charter-1/reap"},
	}
	for _, tc := range allowed {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set(buildinfo.HeaderName, clientBuild.HeaderValue())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if called != len(allowed) {
		t.Fatalf("non-launch downstream calls=%d, want %d", called, len(allowed))
	}
}

func TestTopologyReloadDurablyAdvancesActivationTuple(t *testing.T) {
	topologyText := `
[instances.manager]
agent = "manager"
ephemeral = false
`
	fixture := newProductionActivationFixture(t, topologyText)
	fixture.useCLI(t, fixture.currentCLI)
	top := mustParseCustomTopo(t, topologyText)
	mgr := NewInstanceManager(DaemonRoot(fixture.teamDir), newFakeSpawner(100*time.Millisecond).spawn)
	resolver := NewEventResolver(mgr, fixture.teamDir, top)
	setActivationContextForTest(resolver, fixture.activationContext())
	if err := WriteLaunchEnv(DaemonRoot(fixture.teamDir), &LaunchEnv{
		Bin:        "agent-teamd",
		RecordedAt: time.Now().UTC(),
		Version:    1,
		Build:      fixture.currentBuild,
		Assets:     fixture.loadedAssets,
	}); err != nil {
		t.Fatal(err)
	}
	if status := resolver.activationStatus(); !status.Coherent() {
		t.Fatalf("initial activation verdict = %+v", status)
	}

	if err := os.WriteFile(filepath.Join(fixture.teamDir, "config.toml"), []byte("# changed before reload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := resolver.activationStatus(); status.State != ActivationStateNeeded {
		t.Fatalf("pre-reload activation verdict = %+v, want activation_needed", status)
	}

	handler := Handler(mgr, nil, resolver, fixture.teamDir, fixture.currentBuild)
	reloadReq := httptest.NewRequest(http.MethodPost, "/v1/topology/reload", nil)
	reloadReq.Header.Set(buildinfo.HeaderName, fixture.currentBuild.HeaderValue())
	reloadRec := httptest.NewRecorder()
	handler.ServeHTTP(reloadRec, reloadReq)
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("topology reload status=%d body=%s", reloadRec.Code, reloadRec.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("daemon status=%d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var live struct {
		Activation *ActivationStatus `json:"activation"`
	}
	if err := decodeJSONResponse(statusRec.Body, &live); err != nil {
		t.Fatal(err)
	}
	durable, err := ReadActivationStatus(fixture.teamDir)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := GenerateInstanceBrief(fixture.teamDir, "manager", BriefOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, status := range map[string]*ActivationStatus{
		"daemon status":  live.Activation,
		"durable tuple":  durable,
		"instance brief": brief.Activation,
	} {
		if status == nil || !status.Coherent() {
			t.Fatalf("%s activation = %+v, want coherent", name, status)
		}
		if status.LoadedAssets == fixture.loadedAssets || status.LoadedAssets != status.CurrentAssets {
			t.Fatalf("%s assets loaded=%q current=%q initial=%q", name, status.LoadedAssets, status.CurrentAssets, fixture.loadedAssets)
		}
	}
	launch, err := ReadLaunchEnv(DaemonRoot(fixture.teamDir))
	if err != nil {
		t.Fatal(err)
	}
	if launch.Assets != durable.LoadedAssets {
		t.Fatalf("launch snapshot assets=%q durable loaded=%q", launch.Assets, durable.LoadedAssets)
	}
}

func decodeJSONResponse(r *bytes.Buffer, target any) error {
	return json.NewDecoder(r).Decode(target)
}

func setActivationForTest(resolver *EventResolver, status ActivationStatus) {
	setActivationContextForTest(resolver, activationContextForTest(status))
}

func setActivationContextForTest(resolver *EventResolver, ctx activationContext) {
	resolver.mu.Lock()
	resolver.activation = ctx
	resolver.mu.Unlock()
	resolver.mgr.setActivationContext(ctx)
}

func activationContextForTest(status ActivationStatus) activationContext {
	return activationContext{
		Build:        status.Daemon,
		LoadedAssets: status.LoadedAssets,
		Inspect: func(string, buildinfo.Info, string) ActivationStatus {
			return status
		},
	}
}

type productionActivationFixture struct {
	teamDir      string
	staleCLI     string
	currentCLI   string
	currentBuild buildinfo.Info
	loadedAssets string
}

func newProductionActivationFixture(t *testing.T, topologyText string) productionActivationFixture {
	t.Helper()
	repoRoot := t.TempDir()
	teamDir := filepath.Join(repoRoot, ".agent_team")
	writeFixtureAgent(t, teamDir, "worker")
	writeFixtureAgent(t, teamDir, "manager")
	for path, body := range map[string]string{
		filepath.Join(teamDir, "instances.toml"): topologyText,
		filepath.Join(teamDir, "config.toml"):    "# activation fixture\n",
		filepath.Join(repoRoot, "go.mod"):        "module example.com/activationfixture\n\ngo 1.22\n",
		filepath.Join(repoRoot, "main.go"):       "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runActivationFixtureCommand(t, repoRoot, "git", "init", "-q")
	runActivationFixtureCommand(t, repoRoot, "git", "config", "user.name", "Activation Fixture")
	runActivationFixtureCommand(t, repoRoot, "git", "config", "user.email", "activation@example.invalid")
	runActivationFixtureCommand(t, repoRoot, "git", "add", ".")
	runActivationFixtureCommand(t, repoRoot, "git", "commit", "-qm", "stale activation revision")

	staleCLI := filepath.Join(t.TempDir(), "agent-team")
	runActivationFixtureCommand(t, repoRoot, "go", "build", "-buildvcs=true", "-o", staleCLI, ".")
	controlPlaneFile := filepath.Join(repoRoot, "internal", "daemon", "revision.txt")
	if err := os.MkdirAll(filepath.Dir(controlPlaneFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(controlPlaneFile, []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runActivationFixtureCommand(t, repoRoot, "git", "add", ".")
	runActivationFixtureCommand(t, repoRoot, "git", "commit", "-qm", "current activation revision")

	currentCLI := filepath.Join(t.TempDir(), "agent-team")
	runActivationFixtureCommand(t, repoRoot, "go", "build", "-buildvcs=true", "-o", currentCLI, ".")
	staleBuild, err := buildinfo.ReadFile(staleCLI)
	if err != nil {
		t.Fatal(err)
	}
	currentBuild, err := buildinfo.ReadFile(currentCLI)
	if err != nil {
		t.Fatal(err)
	}
	if buildinfo.SameRevision(staleBuild, currentBuild) {
		t.Fatalf("fixture binaries unexpectedly share revision: stale=%+v current=%+v", staleBuild, currentBuild)
	}
	loadedAssets, err := activationAssetDigest(teamDir)
	if err != nil {
		t.Fatal(err)
	}
	return productionActivationFixture{
		teamDir:      teamDir,
		staleCLI:     staleCLI,
		currentCLI:   currentCLI,
		currentBuild: currentBuild,
		loadedAssets: loadedAssets,
	}
}

func buildRevisionlessSiblings(t *testing.T) (string, string, buildinfo.Info, buildinfo.Info) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve activation test source")
	}
	sourceRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	buildRoot := filepath.Join(t.TempDir(), "source")
	for _, rel := range []string{"cmd", "internal", "template", "embed.go", "go.mod", "go.sum"} {
		copyRevisionlessBuildPath(t, filepath.Join(sourceRoot, rel), filepath.Join(buildRoot, rel))
	}
	binDir := t.TempDir()
	cliPath := filepath.Join(binDir, "agent-team")
	daemonPath := filepath.Join(binDir, "agent-teamd")
	runActivationFixtureCommand(t, buildRoot, "go", "build", "-buildvcs=true", "-o", cliPath, "./cmd/agent-team")
	runActivationFixtureCommand(t, buildRoot, "go", "build", "-buildvcs=true", "-o", daemonPath, "./cmd/agent-teamd")
	cliBuild, err := buildinfo.ReadFile(cliPath)
	if err != nil {
		t.Fatal(err)
	}
	daemonBuild, err := buildinfo.ReadFile(daemonPath)
	if err != nil {
		t.Fatal(err)
	}
	return buildRoot, cliPath, cliBuild, daemonBuild
}

func copyRevisionlessBuildPath(t *testing.T, src, dst string) {
	t.Helper()
	info, err := os.Lstat(src)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		copyRevisionlessBuildFile(t, src, dst, info.Mode())
		return
	}
	if err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, entryInfo.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
}

func copyRevisionlessBuildFile(t *testing.T, src, dst string, mode fs.FileMode) {
	t.Helper()
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, body, mode.Perm()); err != nil {
		t.Fatal(err)
	}
}

func (f productionActivationFixture) useCLI(t *testing.T, cli string) {
	t.Helper()
	t.Setenv("PATH", filepath.Dir(cli)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (f productionActivationFixture) activationContext() activationContext {
	return activationContext{
		Build:        f.currentBuild,
		LoadedAssets: f.loadedAssets,
		Inspect:      InspectActivation,
	}
}

func runActivationFixtureCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func coherentActivationForTest(t *testing.T) ActivationStatus {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	build, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if build.Revision == "" {
		build.Revision = "3d5921d9c5d8115359ed1519c9d448981cd5abc7"
	}
	return ActivationStatus{
		State:         ActivationStateCoherent,
		CLIPath:       filepath.Clean(exe),
		CLI:           build,
		Daemon:        build,
		LoadedAssets:  "test-assets",
		CurrentAssets: "test-assets",
	}
}

func TestActivationStatusSummaryExposesBuildAndDriftWithoutShimBypass(t *testing.T) {
	status := ActivationStatus{
		State:             ActivationStateNeeded,
		CLI:               buildinfo.Info{Revision: "b062047f11111111111111111111111111111111"},
		Daemon:            buildinfo.Info{Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"},
		WorkspaceRevision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7",
		Reasons:           []string{"build drift"},
		Action:            activationAction,
	}
	got := status.Summary() + "\n" + status.Diagnostic()
	for _, want := range []string{"activation_needed", "cli=", "daemon=", "workspace=", "activation needed", "restart the daemon"} {
		if !strings.Contains(got, want) {
			t.Fatalf("activation diagnostic missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "go run") || strings.Contains(got, "source checkout") {
		t.Fatalf("activation diagnostic teaches shim bypass:\n%s", got)
	}
}

func TestInstanceBriefRendersActivationTupleAndAction(t *testing.T) {
	brief := &InstanceBrief{
		GeneratedAt: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Instance:    "manager",
		StateDir:    "/repo/.agent_team/state/manager",
		DaemonDir:   "/repo/.agent_team/daemon/instances/manager",
		Activation: &ActivationStatus{
			State:         ActivationStateNeeded,
			CLI:           buildinfo.Info{Revision: "b062047f11111111111111111111111111111111"},
			Daemon:        buildinfo.Info{Revision: "3d5921d9c5d8115359ed1519c9d448981cd5abc7"},
			LoadedAssets:  "11111111111111111111111111111111",
			CurrentAssets: "22222222222222222222222222222222",
			Reasons:       []string{"loaded assets differ from current assets"},
			Action:        activationAction,
		},
	}
	text := RenderInstanceBrief(brief)
	for _, want := range []string{"## Activation", "cli=", "daemon=", "loaded-assets=", "current-assets=", "activation needed", "restart the daemon"} {
		if !strings.Contains(text, want) {
			t.Fatalf("brief missing %q:\n%s", want, text)
		}
	}
}
