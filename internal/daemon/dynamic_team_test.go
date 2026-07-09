package daemon

import (
	"errors"
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
	enableDynamicTeamSpawnForTest(t)

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
			"job_id":                "gh155-dynteam",
			"ticket":                "GH155-dynteams-impl",
			"kickoff":               "run the child team",
			"deployment_uri":        "agt://spoofed-dep/project/spoofed-dep",
			"deployment_parent_uri": "agt://spoofed-parent/project/spoofed-parent",
			"charter_uri":           "agt://spoofed-dep/charter/spoofed-charter",
			"child_deployment_uri":  "agt://spoofed-dep/project/spoofed-dep",
			"capability_uri":        "agt://spoofed-dep/capability/spoofed-capability",
			"relationship":          "spoofed",
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
	childDeploymentID := charter.ChildDeploymentID
	wantInstanceURI := resource.InstanceURI(childDeploymentID, charter.Instance)
	wantSpecURI := resource.InstanceURI(childDeploymentID, "worker")
	wantJobURI := resource.JobURI(childDeploymentID, "gh155-dynteam")
	wantWorkspaceURI := resource.WorkspaceURI(childDeploymentID, "repo")
	wantStateURI := resource.StateURI(childDeploymentID, charter.Instance)
	if charter.InstanceURI != wantInstanceURI {
		t.Fatalf("charter instance URI = %q, want %q", charter.InstanceURI, wantInstanceURI)
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
	if !grantContains(charter.Authority.Grants, "job.show", resource.JobURI("parent-dep", "gh155-dynteam")) ||
		!grantContains(charter.Authority.Grants, "channel.publish", resource.ChannelURI("parent-dep", "team-platform-supervisor")) ||
		!grantContains(charter.Authority.Grants, "inbox.check", "") {
		t.Fatalf("authority grants = %+v", charter.Authority.Grants)
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
	if !meta.Chartered || meta.CharterURI != charter.URI || meta.CapabilityURI != charter.Authority.CapabilityURI {
		t.Fatalf("metadata charter marker = %+v, charter = %+v", meta, charter)
	}
	childURIs := map[string]string{
		"metadata uri":       meta.URI,
		"metadata spec_uri":  meta.SpecURI,
		"metadata job_uri":   meta.JobURI,
		"metadata workspace": meta.WorkspaceURI,
		"metadata state":     meta.StateURI,
		"charter instance":   charter.InstanceURI,
	}
	for label, uri := range childURIs {
		if strings.HasPrefix(uri, "agt://parent-dep/") {
			t.Fatalf("%s = %q, want child deployment URI", label, uri)
		}
	}
	if meta.URI != wantInstanceURI ||
		meta.SpecURI != wantSpecURI ||
		meta.JobURI != wantJobURI ||
		meta.WorkspaceURI != wantWorkspaceURI ||
		meta.StateURI != wantStateURI {
		t.Fatalf("metadata child URIs = %+v, want instance=%s spec=%s job=%s workspace=%s state=%s", meta, wantInstanceURI, wantSpecURI, wantJobURI, wantWorkspaceURI, wantStateURI)
	}
	env := fake.lastEnv()
	for _, want := range []string{
		"AGENT_TEAM_DEPLOYMENT_URI=" + charter.ChildDeploymentURI,
		"AGENT_TEAM_DEPLOYMENT_PARENT_URI=" + charter.ParentDeploymentURI,
		"AGENT_TEAM_CHARTER_URI=" + charter.URI,
		"AGENT_TEAM_CHILD_DEPLOYMENT_URI=" + charter.ChildDeploymentURI,
		"AGENT_TEAM_CAPABILITY_URI=" + charter.Authority.CapabilityURI,
		"AGENT_TEAM_INSTANCE_URI=" + wantInstanceURI,
		"AGENT_TEAM_SPEC_URI=" + wantSpecURI,
		"AGENT_TEAM_JOB_URI=" + wantJobURI,
		"AGENT_TEAM_WORKSPACE_URI=" + wantWorkspaceURI,
		"AGENT_TEAM_STATE_URI=" + wantStateURI,
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
		`"instance_uri":"` + wantInstanceURI + `"`,
		`"spec_uri":"` + wantSpecURI + `"`,
		`"job_uri":"` + wantJobURI + `"`,
		`"workspace_uri":"` + wantWorkspaceURI + `"`,
		`"state_uri":"` + wantStateURI + `"`,
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
	if declaredAllow, enforce, strict := resolver.authorityForInstance("worker", charter.Instance); !enforce || strict || !containsString(declaredAllow, "job.*") {
		t.Fatalf("declared worker authority = allow=%#v enforce=%v strict=%v", declaredAllow, enforce, strict)
	}
	if allow, enforce, strict := resolver.runtimeAuthorityForInstance("worker", charter.Instance, map[string]any{
		"charter_uri":    charter.URI,
		"capability_uri": "agt://spoofed-dep/capability/spoofed-capability",
		"deployment_uri": charter.ChildDeploymentURI,
	}); !enforce || !strict || len(allow) != 0 {
		t.Fatalf("spoofed charter authority = allow=%#v enforce=%v strict=%v, want strict empty", allow, enforce, strict)
	}
	if allow, enforce, strict := resolver.runtimeAuthorityForInstance("worker", charter.Instance, map[string]any{
		"charter_uri": "not-a-charter-uri",
	}); !enforce || !strict || len(allow) != 0 {
		t.Fatalf("corrupt charter authority = allow=%#v enforce=%v strict=%v, want strict empty", allow, enforce, strict)
	}
	if allow, enforce, strict := resolver.runtimeAuthorityForInstance("worker", charter.Instance, map[string]any{}); !enforce || !strict || len(allow) != 0 {
		t.Fatalf("missing charter authority = allow=%#v enforce=%v strict=%v, want strict empty", allow, enforce, strict)
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
	allocations, err = budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations: %v", err)
	}
	if len(allocations) != 1 || allocations[0].Team != "platform" || allocations[0].Status != budget.AllocationStatusReleased || allocations[0].ReleasedTokens != 60 {
		t.Fatalf("allocations after reap = %+v", allocations)
	}
	second, err := resolver.SpawnTeam(TeamSpawnRequest{
		Name:   "Adapter Port GH155",
		Target: "worker",
		Budget: TeamSpawnBudget{Tokens: 10},
		Payload: map[string]any{
			"job_id":  "gh155-dynteam-second",
			"ticket":  "GH155-dynteams-impl",
			"kickoff": "run the replacement child team",
		},
		Origin: origin.Envelope{
			Project:  "parent-dep",
			Team:     "platform",
			Instance: "manager",
			Agent:    "manager",
			Job:      "gh155-dynteam-second",
		},
	})
	if err != nil {
		t.Fatalf("second SpawnTeam: %v", err)
	}
	if second.Charter == nil || second.Charter.ID == charter.ID || second.Charter.State != TeamCharterStateRunning {
		t.Fatalf("second charter reused stale state: first=%+v second=%+v", charter, second.Charter)
	}
	active, err := ReadTeamCharterByInstance(m.daemonRoot, charter.Instance)
	if err != nil {
		t.Fatalf("read active charter after respawn: %v", err)
	}
	if active.ID != second.Charter.ID {
		t.Fatalf("active charter = %s, want fresh %s", active.ID, second.Charter.ID)
	}
	read, err = ResolveResourceRead(teamDir, m, nil, resolver, second.Charter.ChildDeploymentURI)
	if err != nil {
		t.Fatalf("read second child deployment resource: %v", err)
	}
	child, ok = read.Data.(*projectResource)
	if !ok || child.CharterURI != second.Charter.URI || child.State != TeamCharterStateRunning {
		t.Fatalf("second child deployment resource = %#v, want fresh charter %s", read.Data, second.Charter.URI)
	}
	read, err = ResolveResourceRead(teamDir, m, nil, resolver, second.Charter.Authority.CapabilityURI)
	if err != nil {
		t.Fatalf("read second capability resource: %v", err)
	}
	capability, ok = read.Data.(*capabilityResource)
	if !ok || capability.CharterURI != second.Charter.URI {
		t.Fatalf("second capability resource = %#v, want fresh charter %s", read.Data, second.Charter.URI)
	}
}

func TestDynamicTeamCharteredMarkerFailsClosedWithoutActiveCharter(t *testing.T) {
	enableDynamicTeamSpawnForTest(t)

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

	[authority]
	enforcement = "enforce"

	[authority.agents.manager]
	allow = ["team.spawn", "job.show"]

	[authority.teams.delivery]
	allow = ["job.*"]
	`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.SpawnTeam(TeamSpawnRequest{
		Name:   "Fail Closed GH155",
		Target: "worker",
		Budget: TeamSpawnBudget{Tokens: 10},
		Authority: TeamSpawnAuthority{
			Verbs:     []string{"job.show"},
			Resources: []string{resource.JobURI("parent-dep", "gh155-fail-closed")},
		},
		Payload: map[string]any{
			"job_id":  "gh155-fail-closed",
			"ticket":  "GH155-dynteams-impl",
			"kickoff": "run the fail-closed child",
		},
		Origin: origin.Envelope{
			Project:  "parent-dep",
			Team:     "platform",
			Instance: "manager",
			Agent:    "manager",
			Job:      "gh155-fail-closed",
		},
	})
	if err != nil {
		t.Fatalf("SpawnTeam: %v", err)
	}
	charter := result.Charter
	if charter == nil || charter.State != TeamCharterStateRunning {
		t.Fatalf("charter = %+v", charter)
	}
	t.Cleanup(func() {
		_, _ = m.Stop(charter.Instance)
		_ = m.WaitForReaper(charter.Instance, 5*time.Second)
	})
	meta, err := ReadMetadata(m.daemonRoot, charter.Instance)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if !meta.Chartered || meta.CharterURI != charter.URI || meta.CapabilityURI != charter.Authority.CapabilityURI {
		t.Fatalf("metadata charter marker = %+v, charter = %+v", meta, charter)
	}
	childActor := origin.Envelope{
		Project:  "parent-dep",
		Team:     "delivery",
		Instance: charter.Instance,
		Agent:    "worker",
		Job:      "gh155-fail-closed",
	}
	assertCharteredAuditAllowsTarget(t, teamDir, top, childActor, "job.show", "job:gh155-fail-closed", "gh155-fail-closed")

	terminal := *charter
	terminal.State = TeamCharterStateReaped
	terminal.ReapedAt = time.Now().UTC()
	terminal.UpdatedAt = terminal.ReapedAt
	if err := WriteTeamCharter(m.daemonRoot, &terminal); err != nil {
		t.Fatalf("write terminal charter: %v", err)
	}
	if allow, enforce, strict := resolver.runtimeAuthorityForInstance("worker", charter.Instance, map[string]any{
		"charter_uri":    charter.URI,
		"capability_uri": charter.Authority.CapabilityURI,
		"deployment_uri": charter.ChildDeploymentURI,
	}); !enforce || !strict || len(allow) != 0 {
		t.Fatalf("terminal charter authority = allow=%#v enforce=%v strict=%v, want strict empty", allow, enforce, strict)
	}
	assertCharteredAuditDeniesTarget(t, teamDir, top, childActor, "job.show", "job:gh155-fail-closed", "gh155-fail-closed")

	if err := os.Remove(teamCharterPath(m.daemonRoot, charter.ID)); err != nil {
		t.Fatalf("remove active charter: %v", err)
	}
	if allow, enforce, strict := resolver.runtimeAuthorityForInstance("worker", charter.Instance, map[string]any{}); !enforce || !strict || len(allow) != 0 {
		t.Fatalf("missing charter authority = allow=%#v enforce=%v strict=%v, want strict empty", allow, enforce, strict)
	}
	assertCharteredAuditDeniesTarget(t, teamDir, top, childActor, "job.show", "job:gh155-fail-closed", "gh155-fail-closed")
}

func TestDynamicTeamAuthorityGrantPreservesOwnScope(t *testing.T) {
	enableDynamicTeamSpawnForTest(t)

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

	[authority]
	enforcement = "enforce"

	[authority.agents.manager]
	allow = ["team.spawn", "job.gate.*:own", "inbox.send"]

	[authority.teams.delivery]
	allow = ["job.*", "inbox.*"]
	`)
	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	result, err := resolver.SpawnTeam(TeamSpawnRequest{
		Name:   "Scoped Gate GH155",
		Target: "worker",
		Budget: TeamSpawnBudget{Tokens: 10},
		Authority: TeamSpawnAuthority{
			Verbs: []string{"job.gate.set", "inbox.send", "job.merge"},
			Resources: []string{
				resource.JobURI("parent-dep", "gh155-scope"),
				resource.JobURI("parent-dep", "other-job"),
				resource.MailboxURI("parent-dep", "manager"),
			},
		},
		Payload: map[string]any{
			"job_id":  "gh155-scope",
			"ticket":  "GH155-dynteams-impl",
			"kickoff": "run the scoped child team",
		},
		Origin: origin.Envelope{
			Project:  "parent-dep",
			Team:     "platform",
			Instance: "manager",
			Agent:    "manager",
			Job:      "gh155-scope",
		},
	})
	if err != nil {
		t.Fatalf("SpawnTeam: %v", err)
	}
	charter := result.Charter
	if charter == nil || charter.State != TeamCharterStateRunning {
		t.Fatalf("charter = %+v", charter)
	}
	if !reflect.DeepEqual(charter.Authority.GrantedVerbs, []string{"inbox.send", "job.gate.set:own"}) {
		t.Fatalf("granted verbs = %#v", charter.Authority.GrantedVerbs)
	}
	if !grantContains(charter.Authority.Grants, "job.gate.set:own", resource.JobURI("parent-dep", "gh155-scope")) {
		t.Fatalf("scoped job grant missing owner resource: %+v", charter.Authority.Grants)
	}
	if grantContains(charter.Authority.Grants, "job.gate.set:own", resource.JobURI("parent-dep", "other-job")) {
		t.Fatalf("scoped job grant widened to other job: %+v", charter.Authority.Grants)
	}
	if !grantContains(charter.Authority.Grants, "inbox.send", resource.MailboxURI("parent-dep", "manager")) {
		t.Fatalf("inbox grant missing requested mailbox: %+v", charter.Authority.Grants)
	}
	if !deniedGrantContains(charter.Authority.Denied, "job.merge", "not present in parent capability") {
		t.Fatalf("denied grants = %+v", charter.Authority.Denied)
	}
	childActor := origin.Envelope{
		Project:  "parent-dep",
		Team:     "delivery",
		Instance: charter.Instance,
		Agent:    "worker",
		Job:      "gh155-scope",
	}
	assertCharteredAuditAllowsTarget(t, teamDir, top, childActor, "job.gate.set", "job:gh155-scope:gate:tests", "gh155-scope")
	assertCharteredAuditDeniesTarget(t, teamDir, top, childActor, "job.gate.set", "job:other-job:gate:tests", "other-job")
	assertCharteredAuditDeniesTarget(t, teamDir, top, childActor, "job.merge", "job:gh155-scope", "gh155-scope")
	assertCharteredChildShimAllows(t, teamDir, charter.Instance, "job.gate.set")
	assertCharteredChildShimAllows(t, teamDir, charter.Instance, "inbox.send")
	assertCharteredChildShimDenies(t, teamDir, charter.Instance, "job.merge")
}

func TestHTTPDynamicTeamCharteredChildSpawnRejectedPendingResourceReconciliation(t *testing.T) {
	enableDynamicTeamSpawnForTest(t)

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
	replicas = 4

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
	allow = ["team.spawn", "job.show", "job.merge"]

	[authority.teams.delivery]
	allow = ["team.spawn", "job.*"]
	`)
	fake := newFakeSpawner(30 * time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	parentJob := resource.JobURI("parent-dep", "gh155-nested-parent")
	forbiddenJob := resource.JobURI("parent-dep", "gh155-forbidden")

	parent, err := resolver.SpawnTeam(TeamSpawnRequest{
		Name:   "Nested Parent GH155",
		Target: "worker",
		Budget: TeamSpawnBudget{Tokens: 20},
		Authority: TeamSpawnAuthority{
			Verbs:     []string{"team.spawn", "job.show"},
			Resources: []string{parentJob},
		},
		Payload: map[string]any{
			"job_id":  "gh155-nested-parent",
			"ticket":  "GH155-dynteams-impl",
			"kickoff": "run the nested parent child team",
		},
		Origin: origin.Envelope{
			Project:  "parent-dep",
			Team:     "platform",
			Instance: "manager",
			Agent:    "manager",
			Job:      "gh155-nested-parent",
		},
	})
	if err != nil {
		t.Fatalf("parent SpawnTeam: %v", err)
	}
	if parent.Charter == nil || parent.Charter.State != TeamCharterStateRunning {
		t.Fatalf("parent charter = %+v", parent.Charter)
	}
	if !grantContains(parent.Charter.Authority.Grants, "team.spawn", parentJob) ||
		!grantContains(parent.Charter.Authority.Grants, "job.show", parentJob) ||
		grantContains(parent.Charter.Authority.Grants, "job.merge", parentJob) {
		t.Fatalf("parent grants = %+v", parent.Charter.Authority.Grants)
	}

	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()
	resp := postJSONWithOrigin(t, srv.URL+"/v1/team/spawn", `{
		"name": "Nested Grandchild GH155",
		"target": "worker",
		"budget": {"tokens": 10},
		"authority": {
			"verbs": ["job.show", "job.merge"],
			"resources": ["`+parentJob+`", "`+forbiddenJob+`"]
		},
		"payload": {
			"job_id": "gh155-nested-parent",
			"ticket": "GH155-dynteams-impl",
			"kickoff": "run the nested grandchild team"
		}
	}`, origin.Envelope{
		Project:  "parent-dep",
		Team:     "delivery",
		Instance: parent.Charter.Instance,
		Agent:    "worker",
		Job:      "gh155-nested-parent",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("nested HTTP spawn status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if body := readBody(t, resp); !strings.Contains(body, "authority violation") || !strings.Contains(body, "team.spawn") {
		t.Fatalf("nested HTTP spawn body = %s", body)
	}
	if fake.callCount() != 1 {
		t.Fatalf("spawn calls = %d, want only the parent child", fake.callCount())
	}
	charters, err := ListTeamCharters(m.daemonRoot)
	if err != nil {
		t.Fatalf("list charters: %v", err)
	}
	if len(charters) != 1 || charters[0].ID != parent.Charter.ID {
		t.Fatalf("charters after rejected nested spawn = %+v", charters)
	}
}

func TestDynamicTeamSpawnOverParentBudgetQueuesWithoutChargingChildTeam(t *testing.T) {
	enableDynamicTeamSpawnForTest(t)

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
	if result.Charter == nil || result.Charter.Budget.Team != "platform" || result.Charter.Budget.AllocationURI != "" || result.Charter.Budget.GrantedTokens != 0 {
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
	top.Budgets["platform"].TokensPerDay = 100
	outcome, err := resolver.RetryQueueItem(items[0].ID)
	if err != nil {
		t.Fatalf("RetryQueueItem: %v", err)
	}
	if outcome.Action != "dispatched" || outcome.InstanceID != result.Charter.Instance {
		t.Fatalf("retry outcome = %+v", outcome)
	}
	updated, err := ReadTeamCharter(m.daemonRoot, result.Charter.ID)
	if err != nil {
		t.Fatalf("read updated charter: %v", err)
	}
	if updated.State != TeamCharterStateRunning || updated.SpawnedAt.IsZero() || updated.InstanceURI == "" || updated.Budget.AllocationURI == "" || updated.Budget.GrantedTokens != 60 {
		t.Fatalf("updated queued charter = %+v", updated)
	}
	allocations, err = budget.ListAllocations(teamDir)
	if err != nil {
		t.Fatalf("list allocations after retry: %v", err)
	}
	if len(allocations) != 1 || allocations[0].Team != "platform" || allocations[0].Instance != result.Charter.Instance || allocations[0].Tokens != 60 {
		t.Fatalf("allocations after retry = %+v", allocations)
	}
}

func TestDynamicTeamSpawnDisabledByDefault(t *testing.T) {
	teamDir := fixtureTeamDir(t)
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
allow = ["*"]
`)
	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)

	_, err := resolver.SpawnTeam(TeamSpawnRequest{
		Name:   "Direct Child",
		Target: "worker",
		Budget: TeamSpawnBudget{Tokens: 10},
		Payload: map[string]any{
			"job_id": "gh155-direct-disabled",
			"ticket": "GH155",
		},
		Origin: origin.Envelope{
			Project:  "parent-dep",
			Team:     "platform",
			Instance: "manager",
			Agent:    "manager",
			Job:      "gh155-direct-disabled",
		},
	})
	if !errors.Is(err, ErrDynamicTeamSpawnDisabled) {
		t.Fatalf("SpawnTeam err = %v, want %v", err, ErrDynamicTeamSpawnDisabled)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls = %d, want none", fake.callCount())
	}
	charters, err := ListTeamCharters(m.daemonRoot)
	if err != nil {
		t.Fatalf("list charters: %v", err)
	}
	if len(charters) != 0 {
		t.Fatalf("charters = %+v, want none", charters)
	}
}

func TestHTTPTeamSpawnDisabledByDefault(t *testing.T) {
	teamDir := fixtureTeamDir(t)
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
allow = ["*"]
`)
	fake := newFakeSpawner(time.Second)
	m := NewInstanceManager(DaemonRoot(teamDir), fake.spawn)
	resolver := NewEventResolver(m, teamDir, top)
	srv := httptest.NewServer(Handler(m, nil, resolver, teamDir))
	defer srv.Close()

	resp := postJSONWithOrigin(t, srv.URL+"/v1/team/spawn", `{
		"name": "HTTP Child",
		"target": "worker",
		"budget": {"tokens": 10},
		"payload": {"job_id": "gh155-http-child", "ticket": "GH155"}
	}`, origin.Envelope{
		Project:  "parent-dep",
		Team:     "platform",
		Instance: "manager",
		Agent:    "manager",
		Job:      "gh155-http-child",
	})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("team spawn status = %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, dynamicTeamSpawnDisabledMessage) {
		t.Fatalf("team spawn body = %s, want disabled message", body)
	}
	if fake.callCount() != 0 {
		t.Fatalf("spawn calls = %d, want none", fake.callCount())
	}
	charters, err := ListTeamCharters(m.daemonRoot)
	if err != nil {
		t.Fatalf("list charters: %v", err)
	}
	if len(charters) != 0 {
		t.Fatalf("charters = %+v, want none", charters)
	}
}

func TestTeamCharterReadRejectsPathSegments(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	m := NewInstanceManager(DaemonRoot(teamDir), newFakeSpawner(time.Second).spawn)
	resolver := NewEventResolver(m, teamDir, mustParseCustomTopo(t, ``))

	for _, id := range []string{"../worker/meta", `..\worker\meta`, "..%2Fworker%2Fmeta"} {
		if _, err := ReadTeamCharter(m.daemonRoot, id); err == nil {
			t.Fatalf("ReadTeamCharter(%q) succeeded, want path validation error", id)
		}
		if _, err := resolver.ReapTeamCharter(id); err == nil {
			t.Fatalf("ReapTeamCharter(%q) succeeded, want path validation error", id)
		}
	}
}

func postJSONWithOrigin(t *testing.T, url, body string, actor origin.Envelope) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(origin.HeaderName, origin.HeaderValue(actor))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func enableDynamicTeamSpawnForTest(t *testing.T) {
	t.Helper()
	t.Setenv(dynamicTeamSpawnFeatureEnv, "1")
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

func grantContains(grants []TeamCharterGrant, verb, resource string) bool {
	for _, grant := range grants {
		if grant.Verb != verb {
			continue
		}
		if resource == "" {
			return len(grant.Resources) == 0
		}
		for _, item := range grant.Resources {
			if item == resource {
				return true
			}
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
		"    'job gate') [ \"$3\" = set ] && echo job.gate.set && exit 0; exit 1 ;;\n" +
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
	assertCharteredAuditAllowsTarget(t, teamDir, top, actor, verb, auditResource, "gh155-dynteam")
}

func assertCharteredAuditAllowsTarget(t *testing.T, teamDir string, top *topology.Topology, actor origin.Envelope, verb, auditResource, targetJob string) {
	t.Helper()
	if err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:    teamDir,
		DaemonRoot: DaemonRoot(teamDir),
		Topology:   top,
		Actor:      actor,
		Verb:       verb,
		Resource:   auditResource,
		JobID:      targetJob,
		TargetJob:  targetJob,
		EventActor: "test",
	}); err != nil {
		t.Fatalf("AuditAuthority(%s, %s) denied unexpectedly: %v", verb, auditResource, err)
	}
}

func assertCharteredAuditDenies(t *testing.T, teamDir string, top *topology.Topology, actor origin.Envelope, verb, auditResource string) {
	t.Helper()
	assertCharteredAuditDeniesTarget(t, teamDir, top, actor, verb, auditResource, "gh155-dynteam")
}

func assertCharteredAuditDeniesTarget(t *testing.T, teamDir string, top *topology.Topology, actor origin.Envelope, verb, auditResource, targetJob string) {
	t.Helper()
	err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:    teamDir,
		DaemonRoot: DaemonRoot(teamDir),
		Topology:   top,
		Actor:      actor,
		Verb:       verb,
		Resource:   auditResource,
		JobID:      targetJob,
		TargetJob:  targetJob,
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
	shim := charteredChildShimPath(t, teamDir, instance)
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
	shim := charteredChildShimPath(t, teamDir, instance)
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

func charteredChildShimPath(t *testing.T, teamDir, instance string) string {
	t.Helper()
	if snapshot, err := ReadInstanceLaunchEnv(DaemonRoot(teamDir), instance); err == nil {
		if path := lastEnvValue(snapshot.Env, "PATH"); path != "" {
			first := strings.Split(path, string(os.PathListSeparator))[0]
			shim := filepath.Join(first, "agent-team")
			if _, err := os.Stat(shim); err == nil {
				return shim
			}
		}
	}
	return filepath.Join(teamDir, "state", instance, "runtime", "bin", "agent-team")
}

func shellQuoteTest(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
