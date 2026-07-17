package tui

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

var fixtureTime = time.Date(2026, 7, 10, 12, 4, 5, 0, time.UTC)

func smallFixtureModel(capabilities Capabilities) Model {
	snapshot := smallFixtureSnapshot()
	model := NewModel(fixtureTime, capabilities)
	model.Booted = true
	model.Connection = ConnectionConnected
	model.Snapshot = snapshot
	model.LastGoodAt = fixtureTime
	for source, at := range snapshot.SourceTimes {
		model.Sources[source] = SourceState{FetchedAt: at}
	}
	model = preserveFocus(model)
	return model
}

func smallFixtureSnapshot() *daemonclient.Snapshot {
	exit := 1
	instances := []*daemonclient.Instance{
		{Instance: "frontend-worker-1", Agent: "worker", Job: "gh383-tui-spec", Ticket: "GH-383", Status: daemonclient.InstanceRunning, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/frontend-worker-1", StateURI: "agt://root/state/frontend-worker-1", Runtime: "codex", RuntimeDeadline: fixtureTime.Add(time.Hour), StartedAt: fixtureTime.Add(-time.Hour)},
		{Instance: "platform-worker-2", Agent: "worker", Job: "gh381-research", Ticket: "GH-381", Status: daemonclient.InstanceRunning, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/platform-worker-2", StateURI: "agt://root/state/platform-worker-2", Runtime: "codex", StartedAt: fixtureTime.Add(-2 * time.Hour)},
		{Instance: "reviewer-gh382", Agent: "reviewer", Job: "gh382-discord", Ticket: "GH-382", Status: daemonclient.InstanceRunning, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/reviewer-gh382", StateURI: "agt://root/state/reviewer-gh382", Runtime: "codex", StartedAt: fixtureTime.Add(-time.Hour)},
		{Instance: "manager", Agent: "manager", Status: daemonclient.InstanceRunning, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/manager", StateURI: "agt://root/state/manager", Runtime: "codex", StartedAt: fixtureTime.Add(-24 * time.Hour)},
		{Instance: "verifier-2", Agent: "verifier", Status: daemonclient.InstanceCrashed, ExitCode: &exit, DeploymentURI: "agt://child/project/child", DeploymentParentURI: "agt://root/project/root", URI: "agt://child/instance/verifier-2", StateURI: "agt://child/state/verifier-2", Runtime: "codex", StartedAt: fixtureTime.Add(-3 * time.Hour), ExitedAt: fixtureTime.Add(-time.Minute)},
		{Instance: "comms", Agent: "comms", Status: daemonclient.InstanceStopped, DeploymentURI: "agt://root/project/root", URI: "agt://root/instance/comms", StateURI: "agt://root/state/comms", Runtime: "codex", StartedAt: fixtureTime.Add(-4 * time.Hour), StoppedAt: fixtureTime.Add(-2 * time.Hour)},
	}
	statuses := []daemonclient.JobStatus{
		daemonclient.JobRunning, daemonclient.JobBlocked, daemonclient.JobFailed, daemonclient.JobQueued,
		daemonclient.JobDone, daemonclient.JobRunning, daemonclient.JobDone, daemonclient.JobBlocked,
		daemonclient.JobQueued, daemonclient.JobDone, daemonclient.JobRunning, daemonclient.JobDone,
	}
	jobs := make([]*daemonclient.Job, 12)
	resources := map[string]*daemonclient.Resource{}
	classes := []string{"capability", "scope", "infra", "spec-ambiguity"}
	pipelineNames := []string{"frontend_ticket_to_pr", "platform_ticket_to_pr", "release", "quality"}
	for i := range jobs {
		id := fmt.Sprintf("job-%02d", i+1)
		if i == 0 {
			id = "gh383-tui-spec"
		}
		if i == 1 {
			id = "release-2026-07"
		}
		uri := "agt://root/job/" + id
		outcomeURI := "agt://root/outcome/" + id
		deployment := "agt://root/project/root"
		if i == 10 {
			deployment = "agt://child/project/child"
		}
		jobs[i] = &daemonclient.Job{
			ID: id, URI: uri, OutcomeURI: outcomeURI, DeploymentURI: deployment,
			Ticket: fmt.Sprintf("GH-%d", 380+i), Target: "worker", Pipeline: pipelineNames[i%len(pipelineNames)],
			Status: statuses[i], UpdatedAt: fixtureTime.Add(-time.Duration(i) * time.Minute), CreatedAt: fixtureTime.Add(-time.Hour),
		}
		data := map[string]any{}
		if i < 9 {
			data["step_runs"] = []any{map[string]any{"id": "implement", "model": []string{"gpt-5.6", "gpt-5.5", "gpt-5.6"}[i%3], "tier": []string{"T2", "T1", "T3"}[i%3]}}
		}
		if i < 4 {
			data["bounce_classes"] = map[string]any{classes[i]: 1}
		}
		if i == 2 {
			data["deadline"] = fixtureTime.Add(2 * time.Hour).Format(time.RFC3339)
		}
		if i == 7 {
			data["runtime_deadline"] = fixtureTime.Add(3 * time.Hour).Format(time.RFC3339)
		}
		resources[outcomeURI] = testResource(outcomeURI, "outcome", id, data)
		resources[uri] = testResource(uri, "job", id, map[string]any{"status": statuses[i]})
	}
	for _, instance := range instances {
		resources[instance.URI] = testResource(instance.URI, "instance", instance.Instance, map[string]any{"uri": instance.URI, "agent": instance.Agent, "runtime": instance.Runtime})
		phase := "idle"
		description := "waiting"
		if instance.Status == daemonclient.InstanceRunning && instance.Job != "" {
			phase = "implementing"
			description = "working on " + instance.Job
		}
		resources[instance.StateURI] = testResource(instance.StateURI, "state", instance.Instance, map[string]any{"status": map[string]any{"phase": phase, "description": description}})
	}
	resources["agt://root/project/root"] = testResource("agt://root/project/root", "project", "root", map[string]any{"uri": "agt://root/project/root", "id": "root", "ready": true, "charter_uri": "agt://root/charter/root", "charter_status": "active"})
	resources["agt://child/project/child"] = testResource("agt://child/project/child", "project", "child", map[string]any{"uri": "agt://child/project/child", "id": "child", "parent_uri": "agt://root/project/root", "charter_uri": "agt://child/charter/child", "charter_status": "observed"})
	topology := &daemonclient.Topology{
		Instances: []daemonclient.TopologyInstance{
			{Name: "frontend-worker", Agent: "worker", Ephemeral: true, Replicas: 2, Running: 2, Queued: 1},
			{Name: "platform-worker", Agent: "worker", Ephemeral: true, Replicas: 2, Running: 1},
			{Name: "reviewer", Agent: "reviewer", Ephemeral: true, Replicas: 2, Running: 1},
			{Name: "verifier", Agent: "verifier", Ephemeral: true, Replicas: 2},
			{Name: "manager", Agent: "manager", Ephemeral: false, Replicas: 1, Running: 1},
			{Name: "comms", Agent: "comms", Ephemeral: false, Replicas: 1},
			{Name: "auditor", Agent: "auditor", Ephemeral: true, Replicas: 1},
			{Name: "ticket-manager", Agent: "ticket-manager", Ephemeral: true, Replicas: 1},
		},
		Pipelines: []daemonclient.TopologyPipeline{
			{Name: "frontend_ticket_to_pr", Trigger: &daemonclient.TopologyTrigger{Event: "agent.dispatch", Match: map[string]any{"kind": "frontend"}}, Steps: []daemonclient.TopologyPipelineStep{{ID: "implement", Target: "worker", TokenBudget: 60_000_000, TimeBudget: "1h"}, {ID: "review", Target: "reviewer", TokenBudget: 20_000_000, TimeBudget: "30m"}}},
			{Name: "platform_ticket_to_pr", Trigger: &daemonclient.TopologyTrigger{Event: "agent.dispatch", Match: map[string]any{"kind": "platform"}}, Steps: []daemonclient.TopologyPipelineStep{{ID: "implement", Target: "worker"}}},
			{Name: "release", Trigger: &daemonclient.TopologyTrigger{Event: "release.ready"}, Steps: []daemonclient.TopologyPipelineStep{{ID: "verify", Target: "verifier"}}},
			{Name: "quality", Trigger: &daemonclient.TopologyTrigger{Event: "schedule"}, Steps: []daemonclient.TopologyPipelineStep{{ID: "audit", Target: "auditor"}}},
		},
		Teams: []daemonclient.TopologyTeam{
			{Name: "frontend", Instances: []string{"frontend-worker", "reviewer"}, Pipelines: []string{"frontend_ticket_to_pr"}, Channels: []string{"frontend"}},
			{Name: "platform", Instances: []string{"platform-worker", "reviewer"}, Pipelines: []string{"platform_ticket_to_pr"}, Channels: []string{"platform", "blocked"}},
			{Name: "quality", Instances: []string{"verifier", "auditor"}, Pipelines: []string{"quality", "release"}, Channels: []string{"quality"}},
		},
		Budgets: []daemonclient.TopologyBudget{{Team: "frontend", TokensPerDay: 40_000_000, JobsInFlight: 2, Allocation: "weighted"}, {Team: "platform", TokensPerDay: 80_000_000, JobsInFlight: 2, Allocation: "weighted"}},
		Schedules: []daemonclient.TopologySchedule{
			{Name: "product-verify", Every: "24h", Team: "quality", Payload: map[string]any{"kind": "product-verify"}, LastFiredAt: ptrTime(fixtureTime.Add(-3 * time.Hour))},
			{Name: "debt-sweep", Every: "24h", Team: "platform", Payload: map[string]any{"kind": "debt-sweep"}},
			{Name: "docs-freshness", Every: "24h", Team: "quality", Payload: map[string]any{"kind": "docs-freshness"}},
			{Name: "release", Every: "168h", Team: "quality", Payload: map[string]any{"kind": "release"}},
			{Name: "feedback", Every: "4h", Team: "platform", Payload: map[string]any{"kind": "feedback"}},
		},
	}
	sourceTimes := map[daemonclient.SnapshotSource]time.Time{}
	for _, source := range daemonclient.SnapshotSources() {
		sourceTimes[source] = fixtureTime
	}
	return &daemonclient.Snapshot{
		Schema: daemonclient.SnapshotSchema, TeamDir: "/fixture/.agent_team", DeploymentID: "root", CapturedAt: fixtureTime,
		Instances: instances, Jobs: jobs, Topology: topology, Resources: resources, ResourcesRequested: len(resources),
		SourceTimes: sourceTimes, SourceErrors: map[daemonclient.SnapshotSource]string{},
	}
}

func largeFixtureModel() Model {
	model := smallFixtureModel(Capabilities{})
	snapshot := cloneSnapshot(model.Snapshot)
	snapshot.Instances = make([]*daemonclient.Instance, 100)
	for i := range snapshot.Instances {
		snapshot.Instances[i] = &daemonclient.Instance{Instance: fmt.Sprintf("worker-%03d", i), Agent: "worker", Status: daemonclient.InstanceRunning}
	}
	snapshot.Jobs = make([]*daemonclient.Job, 500)
	for i := range snapshot.Jobs {
		status := daemonclient.JobDone
		if i%5 == 0 {
			status = daemonclient.JobRunning
		}
		snapshot.Jobs[i] = &daemonclient.Job{ID: fmt.Sprintf("job-%03d", i), Ticket: fmt.Sprintf("GH-%d", i), Target: "worker", Status: status, UpdatedAt: fixtureTime.Add(-time.Duration(i) * time.Second)}
	}
	model.Snapshot = snapshot
	model = preserveFocus(model)
	return model
}

func testResource(uri, kind, id string, data map[string]any) *daemonclient.Resource {
	body, _ := json.Marshal(data)
	return &daemonclient.Resource{URI: uri, Kind: kind, ID: id, Data: body}
}

func ptrTime(value time.Time) *time.Time { return &value }
