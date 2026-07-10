package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	templatecfg "github.com/agent-team-project/agent-team/internal/template"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestResearchProgramCompletionPayloadRoutesOnlyToResearchManager(t *testing.T) {
	for _, fixture := range researchRoutingTopologies(t) {
		t.Run(fixture.name, func(t *testing.T) {
			assertResearchCompletionTriggersUsePipeline(t, fixture.top)
			for _, pipeline := range []string{"research_study", "research_slice"} {
				t.Run(pipeline, func(t *testing.T) {
					completedTarget := "research-verifier"
					if pipeline == "research_slice" {
						completedTarget = "research-reviewer"
					}
					for _, event := range []string{topology.EventJobCompleted, topology.EventDeliverableReady} {
						t.Run("literal completed target/"+event, func(t *testing.T) {
							assertResearchTrace(t, fixture.top, event, map[string]any{
								"pipeline": pipeline,
								"target":   completedTarget,
							})
						})
					}

					t.Run("terminal", func(t *testing.T) {
						j, completed := researchCompletionJob(pipeline, completedTarget, jobstore.StatusFailed, false)
						assertResearchCompletionRoute(t, fixture.top, topology.EventJobCompleted, j, completed, jobstore.StatusFailed)
					})

					t.Run("deliverable", func(t *testing.T) {
						j, completed := researchCompletionJob(pipeline, "research-worker", jobstore.StatusDone, true)
						j.ImplementationAgent = "research-worker"
						j.PR = "https://github.com/acme/repo/pull/381"
						assertResearchCompletionRoute(t, fixture.top, topology.EventDeliverableReady, j, completed, jobstore.StatusDone)
					})

					t.Run("manual gate ready", func(t *testing.T) {
						j, completed := researchCompletionJob(pipeline, "research-reviewer", jobstore.StatusDone, true)
						assertResearchCompletionRoute(t, fixture.top, topology.EventJobStepCompleted, j, completed, jobstore.StatusDone)
					})
				})
			}
		})
	}
}

func TestManagerCompletionTargetFallsBackForOrdinaryJobs(t *testing.T) {
	j := &jobstore.Job{Steps: []jobstore.Step{{
		ID:     "approve",
		Target: "manager",
		Status: jobstore.StatusBlocked,
		Gate:   jobstore.StepGateManual,
	}}}
	if got := managerCompletionTarget(j); got != "manager" {
		t.Fatalf("completion target = %q, want manager", got)
	}
	if got := managerCompletionTarget(&jobstore.Job{}); got != "manager" {
		t.Fatalf("ungated completion target = %q, want manager", got)
	}
}

type researchRoutingFixture struct {
	name string
	top  *topology.Topology
}

func researchRoutingTopologies(t *testing.T) []researchRoutingFixture {
	t.Helper()
	selfDogfoodBody, err := os.ReadFile(filepath.Join("..", "..", ".agent_team", "instances.toml"))
	if err != nil {
		t.Fatalf("read self-dogfood topology: %v", err)
	}
	selfDogfood, err := topology.Parse(selfDogfoodBody)
	if err != nil {
		t.Fatalf("parse self-dogfood topology: %v", err)
	}

	templateBody, err := os.ReadFile(filepath.Join("..", "..", "template", "instances.toml.tmpl"))
	if err != nil {
		t.Fatalf("read full-profile topology template: %v", err)
	}
	data := templatecfg.Tree{}
	data.SetDotted("template.profile", "full")
	data.SetDotted("pm.provider", "github")
	data.SetDotted("github.owner", "acme")
	data.SetDotted("github.repo", "research")
	data.SetDotted("github.agent_column", "Ready for Agent")
	renderedBody, err := templatecfg.RenderBytes("instances.toml.tmpl", templateBody, data)
	if err != nil {
		t.Fatalf("render full-profile topology: %v", err)
	}
	rendered, err := topology.Parse(renderedBody)
	if err != nil {
		t.Fatalf("parse rendered full-profile topology: %v", err)
	}

	return []researchRoutingFixture{
		{name: "self-dogfood", top: selfDogfood},
		{name: "rendered full profile", top: rendered},
	}
}

func researchCompletionJob(pipeline, completedTarget string, completedStatus jobstore.Status, gateReady bool) (*jobstore.Job, *jobstore.Step) {
	manualID := "activate"
	if pipeline == "research_slice" {
		manualID = "integrate"
	}
	completed := jobstore.Step{
		ID:     "completed",
		Target: completedTarget,
		Status: completedStatus,
	}
	manual := jobstore.Step{
		ID:     manualID,
		Target: "research-manager",
		Status: jobstore.StatusBlocked,
		Gate:   jobstore.StepGateManual,
	}
	if gateReady {
		manual.After = []string{completed.ID}
	} else {
		manual.After = []string{"review"}
	}
	j := &jobstore.Job{
		ID:       "research-routing",
		Pipeline: pipeline,
		Status:   jobstore.StatusRunning,
		Steps:    []jobstore.Step{completed, manual},
	}
	if completedStatus == jobstore.StatusFailed {
		j.Status = jobstore.StatusFailed
	}
	return j, &j.Steps[0]
}

func assertResearchCompletionRoute(t *testing.T, top *topology.Topology, event string, j *jobstore.Job, completed *jobstore.Step, status jobstore.Status) {
	t.Helper()
	if !managerCompletionShouldWake(j, completed, status) {
		t.Fatalf("producer would not emit %s for pipeline %s", event, j.Pipeline)
	}
	payload := managerCompletionPayload(j, completed, nil, status)
	if got := payload["target"]; got != "research-manager" {
		t.Fatalf("completion payload target = %v, want research-manager: %#v", got, payload)
	}
	assertResearchTrace(t, top, event, payload)
}

func assertResearchTrace(t *testing.T, top *topology.Topology, event string, payload map[string]any) {
	t.Helper()
	trace := top.Trace(event, payload)
	if trace.MatchedRules != 1 {
		t.Fatalf("matched rules = %d, want exactly one: %+v", trace.MatchedRules, trace.Entries)
	}
	if got := trace.MatchedInstanceNames(); !reflect.DeepEqual(got, []string{"research-manager"}) {
		t.Fatalf("matched instances = %v, want only research-manager", got)
	}
	for _, entry := range trace.Entries {
		if entry.Scope == "instances.manager" && entry.Matched {
			t.Fatalf("user-facing manager unexpectedly matched: %+v", entry)
		}
	}
}

func assertResearchCompletionTriggersUsePipeline(t *testing.T, top *topology.Topology) {
	t.Helper()
	instance := top.Instances["research-manager"]
	if instance == nil {
		t.Fatal("research-manager instance missing")
	}
	got := map[string][]string{}
	for _, trigger := range instance.Triggers {
		if trigger.Event != topology.EventJobCompleted && trigger.Event != topology.EventDeliverableReady {
			continue
		}
		if len(trigger.Match) != 1 || trigger.Match["pipeline"].Single == "" {
			t.Fatalf("research completion trigger must match only pipeline identity: %+v", trigger)
		}
		got[trigger.Event] = append(got[trigger.Event], trigger.Match["pipeline"].Single)
	}
	want := []string{"research_slice", "research_study"}
	for _, event := range []string{topology.EventJobCompleted, topology.EventDeliverableReady} {
		pipelines := got[event]
		slices.Sort(pipelines)
		if !reflect.DeepEqual(pipelines, want) {
			t.Fatalf("%s pipeline routes = %v, want %v", event, pipelines, want)
		}
	}
}
