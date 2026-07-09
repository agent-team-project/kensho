package daemon

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestConcurrencyControllerMachineLoadAdmission(t *testing.T) {
	c := newConcurrencyController(&topology.Concurrency{
		Enabled:           true,
		MaxCeiling:        10,
		InitialCeiling:    10,
		TargetLoadPerCore: 0.5,
		LoadPerDispatch:   1,
	})
	c.sampler = func() (machineLoadSample, error) {
		return machineLoadSample{Load1: 4, Cores: 4}, nil
	}

	admission, ev := c.admit(time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC), 0, 0, 1)
	if admission.Allowed || admission.Ceiling != 0 || admission.Running != 0 {
		t.Fatalf("admission = %+v, want blocked at ceiling 0", admission)
	}
	if ev == nil || ev.Action != "concurrency_ceiling_adjusted" || !strings.Contains(ev.Message, "concurrency ceiling adjusted to 0 (load average 4.00/4 cores)") {
		t.Fatalf("event = %+v", ev)
	}
}

func TestConcurrencyControllerCrashBackoffAndStableIncrease(t *testing.T) {
	c := newConcurrencyController(&topology.Concurrency{
		Enabled:        true,
		MinCeiling:     1,
		MaxCeiling:     8,
		InitialCeiling: 8,
		CrashWindow:    10 * time.Minute,
		CrashThreshold: 2,
		DecreaseFactor: 0.5,
		StableWindow:   time.Second,
		IncreaseStep:   1,
	})
	c.sampler = func() (machineLoadSample, error) {
		return machineLoadSample{Load1: 0, Cores: 100}, nil
	}

	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	if ev := c.observeCrash(now, 0, 0); ev != nil {
		t.Fatalf("first crash event = %+v, want nil before threshold", ev)
	}
	ev := c.observeCrash(now.Add(time.Second), 0, 0)
	if ev == nil || !strings.Contains(ev.Message, "concurrency ceiling adjusted to 4 (AIMD decrease after 2 crashes in 10m0s)") {
		t.Fatalf("decrease event = %+v", ev)
	}
	if c.current != 4 {
		t.Fatalf("current ceiling = %d, want 4", c.current)
	}

	admission, ev := c.admit(now.Add(3*time.Second), 0, 0, 1)
	if admission.Ceiling != 5 || !admission.Allowed {
		t.Fatalf("admission after stable window = %+v, want ceiling 5 allowed", admission)
	}
	if ev == nil || !strings.Contains(ev.Message, "concurrency ceiling adjusted to 5 (AIMD increase after 1s stable)") {
		t.Fatalf("increase event = %+v", ev)
	}
}

func TestEventConcurrencyQueuesWhenMachineLoadIsSaturated(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	top := mustParseCustomTopo(t, `
[concurrency]
enabled = true
max_ceiling = 10
initial_ceiling = 10
target_load_per_core = 0.5
load_per_dispatch = 1

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 10

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`)
	resolver := NewEventResolver(m, fixtureTeamDir(t), top)
	resolver.concurrency.sampler = func() (machineLoadSample, error) {
		return machineLoadSample{Load1: 4, Cores: 4}, nil
	}

	result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-gh202-load",
	})
	if err != nil {
		t.Fatalf("EventWithResult: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "queued" || result.Outcomes[0].Reason != QueueReasonConcurrencyCeiling {
		t.Fatalf("outcomes = %+v, want concurrency queued", result.Outcomes)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls=%d, want 0", fake.callCount())
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 1 || items[0].Reason != QueueReasonConcurrencyCeiling || items[0].InstanceID != "worker-gh202-load" {
		t.Fatalf("queue items = %+v", items)
	}
	events, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("ListLifecycleEvents: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Message, "concurrency ceiling adjusted to 0") {
		t.Fatalf("events = %+v", events)
	}
}

func TestEventConcurrencyLoadWeightConsumesGovernorHeadroom(t *testing.T) {
	tests := []struct {
		name             string
		budget           string
		wantThirdAction  string
		wantSpawnedCount int
	}{
		{
			name:             "default weight",
			budget:           "",
			wantThirdAction:  "dispatched",
			wantSpawnedCount: 3,
		},
		{
			name:             "heavy team weight",
			budget:           "load_weight = 2.5",
			wantThirdAction:  "queued",
			wantSpawnedCount: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			fake := newFakeSpawner(30 * time.Second)
			m := NewInstanceManager(root, fake.spawn)
			top := mustParseCustomTopo(t, fmt.Sprintf(`
	[concurrency]
	enabled = true
	max_ceiling = 5
	initial_ceiling = 5
	target_load_per_core = 100
	load_per_dispatch = 1

	[instances.worker]
	agent = "worker"
	ephemeral = true
	replicas = 10

	[[instances.worker.triggers]]
	event = "agent.dispatch"
	match.target = "worker"

	[teams.delivery]
	instances = ["worker"]

	[budgets.delivery]
	%s
	`, tt.budget))
			resolver := NewEventResolver(m, fixtureTeamDir(t), top)
			resolver.concurrency.sampler = func() (machineLoadSample, error) {
				return machineLoadSample{Load1: 0, Cores: 4}, nil
			}

			var outcomes []EventOutcome
			for i := 1; i <= 3; i++ {
				result, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
					"target": "worker",
					"name":   fmt.Sprintf("worker-gh285-%d", i),
				})
				if err != nil {
					t.Fatalf("dispatch %d: %v", i, err)
				}
				if len(result.Outcomes) != 1 {
					t.Fatalf("dispatch %d outcomes = %+v, want one outcome", i, result.Outcomes)
				}
				outcomes = append(outcomes, result.Outcomes[0])
			}
			if outcomes[2].Action != tt.wantThirdAction {
				t.Fatalf("third action = %q, want %q; outcomes=%+v", outcomes[2].Action, tt.wantThirdAction, outcomes)
			}
			if outcomes[2].Action == "queued" && outcomes[2].Reason != QueueReasonConcurrencyCeiling {
				t.Fatalf("third reason = %q, want %q", outcomes[2].Reason, QueueReasonConcurrencyCeiling)
			}
			if fake.callCount() != tt.wantSpawnedCount {
				t.Fatalf("spawn calls = %d, want %d", fake.callCount(), tt.wantSpawnedCount)
			}

			for i := 1; i <= tt.wantSpawnedCount; i++ {
				name := fmt.Sprintf("worker-gh285-%d", i)
				_, _ = m.Stop(name)
				_ = m.WaitForReaper(name, 5*time.Second)
			}
		})
	}
}

func TestRetryQueueItemRespectsConcurrencyCeiling(t *testing.T) {
	root := t.TempDir()
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(root, fake.spawn)
	top := mustParseCustomTopo(t, `
[concurrency]
enabled = true
max_ceiling = 1
initial_ceiling = 1
target_load_per_core = 100
load_per_dispatch = 1

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 10

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`)
	resolver := NewEventResolver(m, fixtureTeamDir(t), top)
	resolver.concurrency.sampler = func() (machineLoadSample, error) {
		return machineLoadSample{Load1: 0, Cores: 4}, nil
	}

	first, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-gh202-running",
	})
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if len(first.Outcomes) != 1 || first.Outcomes[0].Action != "dispatched" {
		t.Fatalf("first outcomes = %+v, want dispatched", first.Outcomes)
	}
	second, err := resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
		"target": "worker",
		"name":   "worker-gh202-retry",
	})
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if len(second.Outcomes) != 1 || second.Outcomes[0].Action != "queued" || second.Outcomes[0].Reason != QueueReasonConcurrencyCeiling {
		t.Fatalf("second outcomes = %+v, want concurrency queued", second.Outcomes)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, want only the running instance", fake.callCount())
	}
	items, err := ListQueueItems(root)
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 1 || items[0].Reason != QueueReasonConcurrencyCeiling || items[0].InstanceID != "worker-gh202-retry" {
		t.Fatalf("queue items = %+v, want one concurrency-held retry candidate", items)
	}

	outcome, err := resolver.RetryQueueItem(items[0].ID)
	if err != nil {
		t.Fatalf("RetryQueueItem: %v", err)
	}
	if outcome.Action != "queued" || outcome.InstanceID != "worker-gh202-retry" || outcome.Reason != QueueReasonConcurrencyCeiling {
		t.Fatalf("retry outcome = %+v, want still queued by concurrency ceiling", outcome)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls=%d, retry should not exceed ceiling", fake.callCount())
	}
	running, queued := resolver.QueueDepth("worker")
	if running != 1 || queued != 1 {
		t.Fatalf("queue depth = running:%d queued:%d, want 1/1", running, queued)
	}
	retried, err := ReadQueueItem(root, items[0].ID)
	if err != nil {
		t.Fatalf("ReadQueueItem: %v", err)
	}
	if retried.Reason != QueueReasonConcurrencyCeiling || !strings.Contains(retried.LastError, "concurrency ceiling 1 reached") {
		t.Fatalf("retried queue item = %+v, want concurrency-held pending item", retried)
	}

	_, _ = m.Stop("worker-gh202-running")
	_ = m.WaitForReaper("worker-gh202-running", 5*time.Second)
	if fake.callCount() > 1 {
		_, _ = m.Stop("worker-gh202-retry")
		_ = m.WaitForReaper("worker-gh202-retry", 5*time.Second)
	}
}

func TestEventReapConcurrentDispatchDoesNotExceedReplicas(t *testing.T) {
	for i := 0; i < 32; i++ {
		root := t.TempDir()
		fake := newFakeSpawner(30 * time.Second)
		m := NewInstanceManager(root, fake.spawn)
		top := mustParseCustomTopo(t, `
	[instances.worker]
	agent = "worker"
	ephemeral = true
	replicas = 1

	[[instances.worker.triggers]]
	event = "agent.dispatch"
	match.target = "worker"
	`)
		resolver := NewEventResolver(m, fixtureTeamDir(t), top)
		queuedName := fmt.Sprintf("worker-gh202-reap-queued-%d", i)
		freshName := fmt.Sprintf("worker-gh202-reap-fresh-%d", i)
		queued := &queuedEvent{
			id:         fmt.Sprintf("queued-gh202-reap-%d", i),
			eventType:  topology.EventAgentDispatch,
			payload:    map[string]any{"target": "worker", "name": queuedName},
			queuedAt:   time.Now().UTC(),
			uniqueName: queuedName,
			reason:     QueueReasonReplicaCapacity,
		}
		if err := WriteQueueItem(root, queueItemFromEvent("worker", queued, QueueStatePending)); err != nil {
			t.Fatalf("WriteQueueItem: %v", err)
		}
		resolver.mu.Lock()
		resolver.tracking["worker"] = &ephTracker{
			running: 1,
			queue:   []*queuedEvent{queued},
		}
		resolver.mu.Unlock()

		var wg sync.WaitGroup
		start := make(chan struct{})
		var dispatchErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			resolver.onReap("worker-gh202-reap-running")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, dispatchErr = resolver.EventWithResult(topology.EventAgentDispatch, map[string]any{
				"target": "worker",
				"name":   freshName,
			})
		}()
		close(start)
		wg.Wait()
		if dispatchErr != nil {
			t.Fatalf("EventWithResult: %v", dispatchErr)
		}

		running, queuedDepth := resolver.QueueDepth("worker")
		if running > 1 {
			t.Fatalf("iteration %d: running=%d queued=%d, want running <= replicas", i, running, queuedDepth)
		}
		if calls := fake.callCount(); calls > 1 {
			t.Fatalf("iteration %d: spawn calls=%d, concurrent reap+dispatch exceeded replicas", i, calls)
		}

		for pass := 0; pass < 2; pass++ {
			for _, name := range []string{freshName, queuedName} {
				if _, err := m.Stop(name); err == nil {
					_ = m.WaitForReaper(name, time.Second)
				}
			}
		}
	}
}
