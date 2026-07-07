package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/budget"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/resource"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestDynamicTeamSpawnCharterLifecycle(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	writeFixtureAgent(t, teamDir, "manager")
	installFakeAgentTeamCLI(t)
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
	instances = ["manager"]

	[teams.delivery]
	instances = ["worker"]

	[budgets.platform]
	tokens_per_day = 100
	allocation = "reserve"

		[budgets.delivery]
		tokens_per_day = 100
		allocation = "reserve"

		[authority]
		enforcement = "enforce"

		[authority.agents.manager]
		allow = ["team.spawn", "job.show", "inbox.check", "channel.publish"]

		[authority.teams.delivery]
		allow = ["job.*", "inbox.*", "channel.*"]
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
			Verbs: []string{
				"job.show",
				"inbox.check",
				"channel.publish",
				"job.merge",
				"inbox.send",
				"channel.delete",
			},
			Resources: []string{
				resource.JobURI("parent-dep", "gh155-dynteam"),
				resource.ChannelURI("parent-dep", "team-platform-supervisor"),
			},
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
	allocations, err := budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	if len(allocations) != 1 {
		t.Fatalf("allocations = %+v", allocations)
	}
	allocation := allocations[0]
	if allocation.Team != "platform" || allocation.Instance != charter.Instance || allocation.Origin.Team != "platform" || allocation.Origin.Instance != "manager" || allocation.Origin.DeploymentURI != charter.ParentDeploymentURI {
		t.Fatalf("allocation provenance = %+v", allocation)
	}
	rows, err := budget.Statuses(teamDir, top, time.Now().UTC())
	if err != nil {
		t.Fatalf("budget statuses: %v", err)
	}
	parentBudget := budgetStatusByTeam(rows, "platform")
	childBudget := budgetStatusByTeam(rows, "delivery")
	if parentBudget == nil || parentBudget.TokensAllocated != 60 || parentBudget.TokensRemaining != 40 {
		t.Fatalf("parent budget = %+v in rows %+v", parentBudget, rows)
	}
	if childBudget == nil || childBudget.TokensAllocated != 0 || childBudget.TokensRemaining != 100 {
		t.Fatalf("child budget = %+v in rows %+v", childBudget, rows)
	}
	if !reflect.DeepEqual(charter.Authority.GrantedVerbs, []string{"channel.publish", "inbox.check", "job.show"}) {
		t.Fatalf("granted verbs = %#v", charter.Authority.GrantedVerbs)
	}
	if !deniedGrantContains(charter.Authority.Denied, "job.merge", "not present in parent capability") ||
		!deniedGrantContains(charter.Authority.Denied, "inbox.send", "not present in parent capability") ||
		!deniedGrantContains(charter.Authority.Denied, "channel.delete", "not present in parent capability") {
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
	if !reflect.DeepEqual(capability.Authority.GrantedResources, charter.Authority.GrantedResources) {
		t.Fatalf("capability resources = %#v, charter resources = %#v", capability.Authority.GrantedResources, charter.Authority.GrantedResources)
	}
	childActor := origin.Envelope{
		Project:  "parent-dep",
		Team:     "delivery",
		Instance: charter.Instance,
		Agent:    "worker",
		Job:      "gh155-dynteam",
	}
	assertCharteredAuditAllows(t, teamDir, top, childActor, "job.show", "job:gh155-dynteam")
	assertCharteredAuditDenies(t, teamDir, top, childActor, "job.show", "job:other")
	assertCharteredAuditAllows(t, teamDir, top, childActor, "channel.publish", "channel:team-platform-supervisor")
	assertCharteredAuditDenies(t, teamDir, top, childActor, "channel.publish", "channel:other")
	assertCharteredAuditDenies(t, teamDir, top, childActor, "inbox.send", "inbox:manager")
	assertCharteredChildShimAllows(t, teamDir, charter.Instance, "job.show")
	assertCharteredChildShimAllows(t, teamDir, charter.Instance, "inbox.check")
	assertCharteredChildShimAllows(t, teamDir, charter.Instance, "channel.publish")
	assertCharteredChildShimDenies(t, teamDir, charter.Instance, "job.merge")
	assertCharteredChildShimDenies(t, teamDir, charter.Instance, "inbox.send")
	assertCharteredChildShimDenies(t, teamDir, charter.Instance, "channel.delete")

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
	allocations, err = budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	if len(allocations) != 1 || allocations[0].Team != "platform" || allocations[0].Status != budget.AllocationStatusReleased || allocations[0].ReleasedTokens != 60 {
		t.Fatalf("allocations after reap = %+v", allocations)
	}
}

func TestDynamicTeamSpawnOverParentBudgetQueuesWithoutChargingChildTeam(t *testing.T) {
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
	instances = ["manager"]

	[teams.delivery]
	instances = ["worker"]

	[budgets.platform]
	tokens_per_day = 50
	allocation = "reserve"

	[budgets.delivery]
	tokens_per_day = 100
	allocation = "reserve"

	[authority.agents.manager]
	allow = ["team.spawn", "job.show"]
	`)
	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.SpawnTeam(TeamSpawnRequest{
		Name:   "Oversized Child",
		Target: "worker",
		Budget: TeamSpawnBudget{Tokens: 60},
		Payload: map[string]any{
			"job_id":  "gh155-over-parent-budget",
			"ticket":  "GH155-dynteams-impl",
			"kickoff": "run the oversized child team",
		},
		Origin: origin.Envelope{
			Project:  "parent-dep",
			Team:     "platform",
			Instance: "manager",
			Agent:    "manager",
			Job:      "gh155-over-parent-budget",
		},
	})
	if err != nil {
		t.Fatalf("SpawnTeam: %v", err)
	}
	if !result.Accepted || result.State != TeamCharterStateQueued || result.Outcome.Action != "queued" || result.Outcome.Reason != QueueReasonBudgetExhausted {
		t.Fatalf("spawn result = %+v", result)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls = %d, want none", fake.callCount())
	}
	if result.Charter == nil || result.Charter.Budget.Team != "platform" || result.Charter.Budget.AllocationURI != "" {
		t.Fatalf("charter budget = %+v", result.Charter)
	}
	allocations, err := budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	if len(allocations) != 0 {
		t.Fatalf("allocations = %+v, want none", allocations)
	}
	rows, err := budget.Statuses(teamDir, top, time.Now().UTC())
	if err != nil {
		t.Fatalf("budget statuses: %v", err)
	}
	parentBudget := budgetStatusByTeam(rows, "platform")
	childBudget := budgetStatusByTeam(rows, "delivery")
	if parentBudget == nil || parentBudget.TokensAllocated != 0 || parentBudget.TokensRemaining != 50 {
		t.Fatalf("parent budget = %+v in rows %+v", parentBudget, rows)
	}
	if childBudget == nil || childBudget.TokensAllocated != 0 || childBudget.TokensRemaining != 100 {
		t.Fatalf("child budget = %+v in rows %+v", childBudget, rows)
	}
	items, err := ListQueueItems(m.daemonRoot)
	if err != nil {
		t.Fatalf("list queue: %v", err)
	}
	if len(items) != 1 || items[0].Reason != QueueReasonBudgetExhausted || items[0].Origin.Team != "platform" || items[0].Origin.Instance != "manager" {
		t.Fatalf("queue items = %+v", items)
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

func budgetStatusByTeam(rows []budget.TeamStatus, team string) *budget.TeamStatus {
	for i := range rows {
		if rows[i].Team == team {
			return &rows[i]
		}
	}
	return nil
}

func deniedGrantContains(denied []TeamCharterDeniedGrant, verb, reason string) bool {
	for _, item := range denied {
		if item.Verb == verb && item.Reason == reason {
			return true
		}
	}
	return false
}

func installFakeAgentTeamCLI(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "agent-team.log")
	agentTeam := filepath.Join(binDir, "agent-team")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"__resolve-verb\" ]; then\n" +
		"  shift\n" +
		"  case \"$1 $2\" in\n" +
		"    'job merge') echo job.merge; exit 0 ;;\n" +
		"    'job show') echo job.show; exit 0 ;;\n" +
		"    'inbox check') echo inbox.check; exit 0 ;;\n" +
		"    'inbox send') echo inbox.send; exit 0 ;;\n" +
		"    'channel publish') echo channel.publish; exit 0 ;;\n" +
		"    'channel delete') echo channel.delete; exit 0 ;;\n" +
		"    *) exit 1 ;;\n" +
		"  esac\n" +
		"fi\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuoteTest(logPath) + "\n"
	if err := os.WriteFile(agentTeam, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake agent-team: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func assertCharteredAuditAllows(t *testing.T, teamDir string, top *topology.Topology, actor origin.Envelope, verb, auditResource string) {
	t.Helper()
	if err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:    teamDir,
		DaemonRoot: DaemonRoot(teamDir),
		Topology:   top,
		Actor:      actor,
		Verb:       verb,
		Resource:   auditResource,
		JobID:      "gh155-dynteam",
		TargetJob:  "gh155-dynteam",
		EventActor: "test",
	}); err != nil {
		t.Fatalf("AuditAuthority(%s, %s) denied unexpectedly: %v", verb, auditResource, err)
	}
}

func assertCharteredAuditDenies(t *testing.T, teamDir string, top *topology.Topology, actor origin.Envelope, verb, auditResource string) {
	t.Helper()
	err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:    teamDir,
		DaemonRoot: DaemonRoot(teamDir),
		Topology:   top,
		Actor:      actor,
		Verb:       verb,
		Resource:   auditResource,
		JobID:      "gh155-dynteam",
		TargetJob:  "gh155-dynteam",
		EventActor: "test",
	})
	if err == nil {
		t.Fatalf("AuditAuthority(%s, %s) allowed unexpectedly", verb, auditResource)
	}
	if !strings.Contains(err.Error(), "authority violation") {
		t.Fatalf("AuditAuthority(%s, %s) error = %v, want authority violation", verb, auditResource, err)
	}
}

func assertCharteredChildShimAllows(t *testing.T, teamDir, instance, verb string) {
	t.Helper()
	parts := strings.Split(verb, ".")
	args := append([]string{}, parts...)
	args = append(args, "gh155-dynteam")
	shim := filepath.Join(teamDir, "state", instance, "runtime", "bin", "agent-team")
	out, err := exec.Command(shim, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("shim denied %s unexpectedly: %v; output=%s", verb, err, out)
	}
}

func assertCharteredChildShimDenies(t *testing.T, teamDir, instance, verb string) {
	t.Helper()
	parts := strings.Split(verb, ".")
	args := append([]string{}, parts...)
	args = append(args, "gh155-dynteam")
	shim := filepath.Join(teamDir, "state", instance, "runtime", "bin", "agent-team")
	out, err := exec.Command(shim, args...).CombinedOutput()
	if err == nil {
		t.Fatalf("shim allowed %s; output=%s", verb, out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("shim %s failed with %T: %v; output=%s", verb, err, err, out)
	}
	if exitErr.ExitCode() != 3 || !strings.Contains(string(out), "denied verb "+verb) {
		t.Fatalf("shim denial for %s = code %d output %q", verb, exitErr.ExitCode(), out)
	}
}

func shellQuoteTest(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
