package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/budget"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/resource"
)

func TestDynamicTeamSpawnCharterLifecycle(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[project]\nid = \"parent-dep\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.manager]
agent = "manager"
ephemeral = false

[instances.worker]
agent = "worker"
ephemeral = true
replicas = 1

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[teams.platform]
instances = ["manager", "worker"]

[budgets.platform]
tokens_per_day = 100
allocation = "reserve"

[authority.agents.manager]
allow = ["team.spawn", "job.show", "inbox.*"]
`)
	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.SpawnTeam(TeamSpawnRequest{
		Name:     "Adapter Port GH155",
		Target:   "worker",
		Template: "adapter-porting-squad",
		Goal: TeamSpawnGoal{
			Summary: "Port adapter tests",
			Success: []string{
				"tests pass",
				"report posted",
			},
		},
		Budget: TeamSpawnBudget{
			Tokens: 60,
			Time:   "30m",
		},
		Authority: TeamSpawnAuthority{
			Verbs:     []string{"job.show", "inbox.send", "instance.remove"},
			Resources: []string{"agt://parent-dep/job/gh155-dynteam"},
		},
		Lifecycle: TeamSpawnLifecycle{TTL: "2h", Reap: "on_goal_complete"},
		Payload: map[string]any{
			"job_id":  "gh155-dynteam",
			"ticket":  "GH155-dynteams-impl",
			"kickoff": "run the child team",
		},
		Origin: origin.Envelope{
			Project:  "parent-dep",
			Team:     "platform",
			Instance: "manager",
			Agent:    "manager",
			Job:      "gh155-dynteam",
		},
	})
	if err != nil {
		t.Fatalf("SpawnTeam: %v", err)
	}
	if !result.Accepted || result.State != TeamCharterStateRunning || result.Outcome.Action != "dispatched" {
		t.Fatalf("spawn result = %+v", result)
	}
	charter := result.Charter
	if charter == nil {
		t.Fatal("result missing charter")
	}
	if charter.ParentDeploymentURI != "agt://parent-dep/project/parent-dep" ||
		!strings.HasPrefix(charter.ChildDeploymentID, "child-adapter-port-gh155-") ||
		charter.ChildDeploymentURI != resource.DeploymentURI(charter.ChildDeploymentID) {
		t.Fatalf("charter deployment edge = %+v", charter)
	}
	if charter.Instance != "worker-adapter-port-gh155" || charter.Creator.InstanceURI != "agt://parent-dep/instance/manager" || charter.Creator.JobURI != "agt://parent-dep/job/gh155-dynteam" {
		t.Fatalf("charter provenance = %+v", charter)
	}
	if charter.Budget.RequestedTokens != 60 || charter.Budget.GrantedTokens != 60 || charter.Budget.AllocationURI == "" || charter.Budget.Team != "platform" {
		t.Fatalf("charter budget = %+v", charter.Budget)
	}
	if !reflect.DeepEqual(charter.Authority.GrantedVerbs, []string{"inbox.send", "job.show"}) {
		t.Fatalf("granted verbs = %#v", charter.Authority.GrantedVerbs)
	}
	if len(charter.Authority.Denied) != 1 || charter.Authority.Denied[0].Verb != "instance.remove" {
		t.Fatalf("denied grants = %+v", charter.Authority.Denied)
	}

	meta, err := ReadMetadata(m.daemonRoot, charter.Instance)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if meta.DeploymentURI != charter.ChildDeploymentURI || meta.DeploymentParentURI != charter.ParentDeploymentURI || meta.Job != "gh155-dynteam" {
		t.Fatalf("metadata deployment/provenance = %+v", meta)
	}
	env := fake.lastEnv()
	for _, want := range []string{
		"AGENT_TEAM_DEPLOYMENT_URI=" + charter.ChildDeploymentURI,
		"AGENT_TEAM_DEPLOYMENT_PARENT_URI=" + charter.ParentDeploymentURI,
		"AGENT_TEAM_CHARTER_URI=" + charter.URI,
		"AGENT_TEAM_CHILD_DEPLOYMENT_URI=" + charter.ChildDeploymentURI,
		"AGENT_TEAM_CAPABILITY_URI=" + charter.Authority.CapabilityURI,
	} {
		if !containsString(env, want) {
			t.Fatalf("env missing %q: %#v", want, env)
		}
	}
	prompt, ok := argValue(fake.lastCall(), "-p")
	if !ok {
		t.Fatalf("spawn call missing -p prompt: %#v", fake.lastCall())
	}
	for _, want := range []string{
		`"charter_uri":"` + charter.URI + `"`,
		`"capability_uri":"` + charter.Authority.CapabilityURI + `"`,
		`"deployment_uri":"` + charter.ChildDeploymentURI + `"`,
		`"deployment_parent_uri":"` + charter.ParentDeploymentURI + `"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}

	read, err := ResolveResourceRead(teamDir, m, nil, resolver, charter.URI)
	if err != nil {
		t.Fatalf("read charter resource: %v", err)
	}
	if read.Kind != resource.KindCharter || read.ID != charter.ID {
		t.Fatalf("charter resource = %+v", read)
	}
	read, err = ResolveResourceRead(teamDir, m, nil, resolver, charter.ChildDeploymentURI)
	if err != nil {
		t.Fatalf("read child deployment resource: %v", err)
	}
	child, ok := read.Data.(*projectResource)
	if !ok || child.ParentURI != charter.ParentDeploymentURI || child.CharterURI != charter.URI || child.State != TeamCharterStateRunning {
		t.Fatalf("child deployment resource = %#v", read.Data)
	}
	read, err = ResolveResourceRead(teamDir, m, nil, resolver, charter.Authority.CapabilityURI)
	if err != nil {
		t.Fatalf("read capability resource: %v", err)
	}
	capability, ok := read.Data.(*capabilityResource)
	if !ok || capability.CharterURI != charter.URI || !reflect.DeepEqual(capability.Authority.GrantedVerbs, charter.Authority.GrantedVerbs) {
		t.Fatalf("capability resource = %#v", read.Data)
	}

	if err := m.WaitForReaper(charter.Instance, 5*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	reaped, err := ReadTeamCharter(m.daemonRoot, charter.ID)
	if err != nil {
		t.Fatalf("read reaped charter: %v", err)
	}
	if reaped.State != TeamCharterStateReaped || reaped.ReapedAt.IsZero() || reaped.Tombstone["reason"] != "instance_reaped" {
		t.Fatalf("reaped charter = %+v", reaped)
	}
	allocations, err := budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	if len(allocations) != 1 || allocations[0].Status != budget.AllocationStatusReleased || allocations[0].ReleasedTokens != 60 {
		t.Fatalf("allocations after reap = %+v", allocations)
	}
}

func TestHTTPTeamSpawnCreatesReadableCharter(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[project]\nid = \"parent-dep\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	top := mustParseCustomTopo(t, `
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"
`)
	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := mustPost(t, srv.URL+"/v1/team/spawn", `{
		"name": "HTTP Child",
		"target": "worker",
		"budget": {"tokens": 10},
		"payload": {"job_id": "gh155-http-child", "ticket": "GH155"}
	}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("team spawn: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var result TeamSpawnResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode spawn result: %v", err)
	}
	if !result.Accepted || result.CharterURI == "" || result.ChildDeploymentURI == "" {
		t.Fatalf("result = %+v", result)
	}
	t.Cleanup(func() {
		if result.Charter != nil {
			_, _ = m.Stop(result.Charter.Instance)
			_ = m.WaitForReaper(result.Charter.Instance, 5*time.Second)
		}
	})

	resp = mustGet(t, srv.URL+"/v1/team/charters/"+result.Charter.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("charter get: %d %s", resp.StatusCode, readBody(t, resp))
	}
	var charter TeamCharter
	if err := json.NewDecoder(resp.Body).Decode(&charter); err != nil {
		t.Fatalf("decode charter: %v", err)
	}
	if charter.URI != result.CharterURI || charter.ChildDeploymentURI != result.ChildDeploymentURI {
		t.Fatalf("charter = %+v, result = %+v", charter, result)
	}
}
