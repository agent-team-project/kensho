package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

const managerWakeTestTopology = `
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 2

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`

func managerWakeFixture(t *testing.T) (string, string, *topology.Topology, *fakeSpawner, *EventResolver) {
	t.Helper()
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(managerWakeTestTopology), 0o644); err != nil {
		t.Fatalf("write instances.toml: %v", err)
	}
	top := mustParseCustomTopo(t, managerWakeTestTopology)
	root := DaemonRoot(teamDir)
	seedStoppedCodexManager(t, root, teamDir, "manager")
	fake := newFakeSpawner(30 * time.Second)
	mgr := NewInstanceManager(root, fake.spawn)
	resolver := NewEventResolver(mgr, teamDir, top)
	return teamDir, root, top, fake, resolver
}

func writeManagerWakeJob(t *testing.T, teamDir string, j *jobstore.Job) {
	t.Helper()
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
}

func newManagerGoalJob(t *testing.T, now time.Time) *jobstore.Job {
	t.Helper()
	j, err := jobstore.New("GH-264", "manager", "complete the backlog", now)
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Status = jobstore.StatusRunning
	j.Instance = "manager"
	j.UpdatedAt = now
	return j
}

func TestManagerWakeSweepWakesStoppedManagerWithUnfinishedGoal(t *testing.T) {
	teamDir, root, _, fake, resolver := managerWakeFixture(t)
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	j := newManagerGoalJob(t, now.Add(-time.Hour))
	writeManagerWakeJob(t, teamDir, j)

	result, err := resolver.SweepManagerWakeupsWithResult(now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls = %d, want manager resume", fake.callCount())
	}
	if len(result.IdleWakeups) != 1 || result.IdleWakeups[0].Action != "dispatched" || result.IdleWakeups[0].JobID != j.ID {
		t.Fatalf("idle wakeups = %+v, want dispatched job %s", result.IdleWakeups, j.ID)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, managerIdleWakeEventType) || !strings.Contains(messages[0].Body, `"job_id":"`+j.ID+`"`) {
		t.Fatalf("messages = %+v, want idle wake for job %s", messages, j.ID)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !lifecycleEventsContain(events, managerIdleWakeLifecycleAction, "manager") {
		t.Fatalf("events = %+v, want %s", events, managerIdleWakeLifecycleAction)
	}

	_, _ = resolver.mgr.Stop("manager")
	waitForStatusNot(t, resolver.mgr, "manager", StatusRunning)
}

func TestManagerWakeSweepSuppressesWhenChildWorkActive(t *testing.T) {
	teamDir, root, _, fake, resolver := managerWakeFixture(t)
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	j := newManagerGoalJob(t, now.Add(-time.Hour))
	writeManagerWakeJob(t, teamDir, j)
	if err := WriteMetadata(root, &Metadata{
		Instance:  "worker-gh-264",
		Agent:     "worker",
		Job:       j.ID,
		Workspace: filepath.Dir(teamDir),
		PID:       os.Getpid(),
		StartedAt: now,
		Status:    StatusRunning,
	}); err != nil {
		t.Fatalf("write worker metadata: %v", err)
	}

	result, err := resolver.SweepManagerWakeupsWithResult(now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls = %d, want no wake while worker active", fake.callCount())
	}
	if len(result.IdleWakeups) != 0 {
		t.Fatalf("idle wakeups = %+v, want none", result.IdleWakeups)
	}
}

func TestManagerWakeSweepBacksOffWithoutProgress(t *testing.T) {
	teamDir, _, _, fake, resolver := managerWakeFixture(t)
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	j := newManagerGoalJob(t, now.Add(-time.Hour))
	writeManagerWakeJob(t, teamDir, j)

	first, err := resolver.SweepManagerWakeupsWithResult(now)
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if len(first.IdleWakeups) != 1 || first.IdleWakeups[0].Action != "dispatched" {
		t.Fatalf("first wakeups = %+v, want dispatched", first.IdleWakeups)
	}
	_, _ = resolver.mgr.Stop("manager")
	waitForStatusNot(t, resolver.mgr, "manager", StatusRunning)

	second, err := resolver.SweepManagerWakeupsWithResult(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls = %d, want no second resume during backoff", fake.callCount())
	}
	if len(second.IdleWakeups) != 1 || second.IdleWakeups[0].Action != "skipped" || !strings.Contains(second.IdleWakeups[0].Reason, "backoff") {
		t.Fatalf("second wakeups = %+v, want backoff skip", second.IdleWakeups)
	}

	third, err := resolver.SweepManagerWakeupsWithResult(now.Add(managerIdleWakeDefaultBackoff + time.Second))
	if err != nil {
		t.Fatalf("third sweep: %v", err)
	}
	if fake.callCount() != 2 {
		t.Fatalf("spawn calls = %d, want second resume after backoff", fake.callCount())
	}
	if len(third.IdleWakeups) != 1 || third.IdleWakeups[0].Action != "dispatched" {
		t.Fatalf("third wakeups = %+v, want dispatched", third.IdleWakeups)
	}
	_, _ = resolver.mgr.Stop("manager")
	waitForStatusNot(t, resolver.mgr, "manager", StatusRunning)
}

func TestManagerWakeSweepRecordsOverdueRunningStepAndWakesManager(t *testing.T) {
	teamDir, root, _, fake, resolver := managerWakeFixture(t)
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	j, err := jobstore.New("GH-265", "worker", "review-like overdue work", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Status = jobstore.StatusRunning
	j.Origin = origin.Envelope{Instance: "manager", Agent: "manager"}
	j.Steps = []jobstore.Step{{
		ID:         "review",
		Target:     "reviewer",
		Status:     jobstore.StatusRunning,
		Instance:   "reviewer-gh-265",
		StartedAt:  now.Add(-31 * time.Minute),
		RunningAt:  now.Add(-31 * time.Minute),
		TimeBudget: "30m",
	}}
	j.UpdatedAt = now.Add(-31 * time.Minute)
	writeManagerWakeJob(t, teamDir, j)

	result, err := resolver.SweepManagerWakeupsWithResult(now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls = %d, want manager wake for overdue step", fake.callCount())
	}
	if len(result.Overdue) != 1 || result.Overdue[0].Action != "dispatched" || result.Overdue[0].StepID != "review" {
		t.Fatalf("overdue = %+v, want dispatched review wake", result.Overdue)
	}
	updated, err := jobstore.Read(teamDir, j.ID)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.LastEvent != managerOverdueWakeEventType || !strings.Contains(updated.LastStatus, "exceeded time budget") {
		t.Fatalf("job last event/status = %q/%q, want overdue marker", updated.LastEvent, updated.LastStatus)
	}
	messages, err := ReadMessages(root, "manager")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0].Body, managerOverdueWakeEventType) || !strings.Contains(messages[0].Body, `"pipeline_step":"review"`) {
		t.Fatalf("messages = %+v, want overdue review wake", messages)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !lifecycleEventsContain(events, managerOverdueWakeLifecycleAction, "manager") {
		t.Fatalf("events = %+v, want %s", events, managerOverdueWakeLifecycleAction)
	}

	_, _ = resolver.mgr.Stop("manager")
	waitForStatusNot(t, resolver.mgr, "manager", StatusRunning)
}
