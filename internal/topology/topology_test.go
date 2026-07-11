package topology

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const sampleTOML = `
[instances.manager]
agent       = "manager"
ephemeral   = false
description = "User-facing entry point."

[[instances.manager.triggers]]
event = "user_invocation"

[instances.tm-platform]
agent       = "ticket-manager"
ephemeral   = false

[instances.tm-platform.config.linear]
project_id = "3d07030a"

[[instances.tm-platform.triggers]]
event         = "ticket.created"
match.project = "Platform"

[[instances.tm-platform.triggers]]
event         = "ticket.updated"
match.project = "Platform"

[instances.tm-mobile]
agent     = "ticket-manager"
ephemeral = false

[instances.tm-mobile.config.linear]
project_id = "50b6cd55"

[[instances.tm-mobile.triggers]]
event         = "ticket.created"
match.project = "Mobile"

[instances.worker]
agent     = "worker"
ephemeral = true
replicas  = 3

[[instances.worker.triggers]]
event        = "agent.dispatch"
match.target = "worker"
`

func TestParse_Sample(t *testing.T) {
	top, err := Parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := len(top.Instances); got != 4 {
		t.Fatalf("got %d instances, want 4", got)
	}
	mgr := top.Instances["manager"]
	if mgr == nil {
		t.Fatal("manager instance missing")
	}
	if mgr.Agent != "manager" || mgr.Ephemeral || mgr.Replicas != 1 {
		t.Errorf("manager wrong: %+v", mgr)
	}
	if !mgr.Brief {
		t.Errorf("manager brief = false, want default true for persistent instances")
	}
	tmPlat := top.Instances["tm-platform"]
	if tmPlat == nil {
		t.Fatal("tm-platform missing")
	}
	got, ok := tmPlat.Config.GetDotted("linear.project_id")
	if !ok || got != "3d07030a" {
		t.Errorf("tm-platform config: got %v ok=%v", got, ok)
	}
	if len(tmPlat.Triggers) != 2 {
		t.Fatalf("tm-platform triggers: %d", len(tmPlat.Triggers))
	}
	trig := tmPlat.Triggers[0]
	if trig.Event != "ticket.created" {
		t.Errorf("trigger event: %s", trig.Event)
	}
	if trig.Match["project"].Single != "Platform" {
		t.Errorf("project match: %+v", trig.Match["project"])
	}
	trig = tmPlat.Triggers[1]
	if trig.Event != "ticket.updated" {
		t.Errorf("second trigger event: %s", trig.Event)
	}
	if trig.Match["project"].Single != "Platform" {
		t.Errorf("second trigger project match: %+v", trig.Match["project"])
	}
	worker := top.Instances["worker"]
	if !worker.Ephemeral || worker.Replicas != 3 {
		t.Errorf("worker: %+v", worker)
	}
	if worker.Brief {
		t.Errorf("worker brief = true, want default false for ephemeral instances")
	}
	if worker.ReapWorktree != "never" {
		t.Errorf("worker reap_worktree = %q, want never", worker.ReapWorktree)
	}
	if worker.Restart != RestartNever {
		t.Errorf("worker restart = %q, want never", worker.Restart)
	}
}

func TestParse_BriefPolicy(t *testing.T) {
	top, err := Parse([]byte(`
[instances.manager]
agent = "manager"
brief = false

[instances.reviewer]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true
brief = true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if top.Instances["manager"].Brief {
		t.Fatalf("manager brief = true, want explicit false")
	}
	if !top.Instances["reviewer"].Brief {
		t.Fatalf("reviewer brief = false, want persistent default true")
	}
	if !top.Instances["worker"].Brief {
		t.Fatalf("worker brief = false, want explicit true")
	}
}

func TestParse_HardBudgetCutoffs(t *testing.T) {
	top, err := Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true
token_budget = "10M"
time_budget = "45m"
hard_multiplier = 1.5

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
token_budget = "2M"
time_budget = "30m"
hard = true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	worker := top.Instances["worker"]
	if worker == nil || worker.TokenBudget != 10000000 || worker.TimeBudget != 45*time.Minute || worker.HardMultiplier != 1.5 {
		t.Fatalf("worker hard budget fields = %+v", worker)
	}
	step := top.Pipelines["ticket_to_pr"].Steps[0]
	if step.TokenBudget != 2000000 || step.TimeBudget != 30*time.Minute || !step.HardBudget {
		t.Fatalf("step hard budget fields = %+v", step)
	}
}

func TestParse_HardMultiplierRejectsBelowOne(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"
hard_multiplier = 0.5
`))
	if err == nil || !strings.Contains(err.Error(), "hard_multiplier must be >= 1") {
		t.Fatalf("Parse err = %v, want hard multiplier validation", err)
	}
}

func TestParse_EnvAllow(t *testing.T) {
	top, err := Parse([]byte(`
[instances.unset]
agent = "worker"

[instances.worker]
agent = "worker"
ephemeral = true
env_allow = [" PATH ", "HOME", "LC_*"]

[instances.minimal]
agent = "reviewer"
ephemeral = true
env_allow = []
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if top.Instances["unset"].EnvAllow != nil {
		t.Fatalf("unset env_allow = %#v, want nil", top.Instances["unset"].EnvAllow)
	}
	if got, want := top.Instances["worker"].EnvAllow, []string{"PATH", "HOME", "LC_*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("worker env_allow = %#v, want %#v", got, want)
	}
	if got := top.Instances["minimal"].EnvAllow; got == nil || len(got) != 0 {
		t.Fatalf("minimal env_allow = %#v, want configured empty list", got)
	}
}

func TestParse_RejectsInvalidEnvAllow(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"
env_allow = ["["]
`))
	if err == nil || !strings.Contains(err.Error(), "env_allow[0]: invalid glob") {
		t.Fatalf("Parse err = %v, want invalid env_allow glob", err)
	}
}

func TestParse_ResourceScopesChannelsAndAuthority(t *testing.T) {
	top, err := Parse([]byte(`
[locks.build]
slots = 2
scope = "team"

[channels.supervisor]
scope = "team"

[schedules.nightly]
every = "24h"
scope = "job"

[schedules.nightly.payload]
kind = "audit"

[instances.worker]
agent = "worker"
ephemeral = true
locks = ["build"]

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[authority]
enforcement = "audit"

[authority.instances.manager]
allow = ["*"]

[authority.agents.worker]
allow = ["inbox.*", "job.gate.set:own"]

[authority.agents.manager]
allow = ["job.bounce:team"]

[authority.teams.platform]
verbs = ["job.*", "queue.retry"]

[teams.platform]
instances = ["worker"]
schedules = ["nightly"]
channels = ["supervisor"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if lock := top.Locks["build"]; lock == nil || lock.Scope != ScopeTeam || lock.Slots != 2 {
		t.Fatalf("lock = %+v", lock)
	}
	if channel := top.Channels["#supervisor"]; channel == nil || channel.Scope != ScopeTeam {
		t.Fatalf("channel = %+v", channel)
	}
	if schedule := top.Schedules["nightly"]; schedule == nil || schedule.Scope != ScopeJob {
		t.Fatalf("schedule = %+v", schedule)
	}
	if team := top.Teams["platform"]; team == nil || !reflect.DeepEqual(team.Channels, []string{"#supervisor"}) {
		t.Fatalf("team = %+v", team)
	}
	if top.Authority == nil || top.Authority.Enforcement != AuthorityModeAudit || top.Authority.Enforced() {
		t.Fatalf("authority = %+v", top.Authority)
	}
	if !top.Authority.Allows(AuthorityDecision{Instance: "manager", Verb: "job.merge"}) {
		t.Fatalf("manager instance should be allowed by instance wildcard")
	}
	if !top.Authority.Allows(AuthorityDecision{Agent: "worker", Verb: "inbox.check"}) {
		t.Fatalf("worker inbox.check should be allowed by wildcard")
	}
	if !top.Authority.Allows(AuthorityDecision{Agent: "reviewer", Team: "platform", Verb: "job.merge", ActorJob: "squ-92", TargetJob: "squ-93"}) {
		t.Fatalf("platform job.merge should be allowed by team wildcard")
	}
	if !top.Authority.Allows(AuthorityDecision{Agent: "worker", Verb: "job.gate.set", ActorJob: "squ-92", TargetJob: "squ-92"}) {
		t.Fatalf("worker job.gate.set should be allowed for own job")
	}
	if top.Authority.Allows(AuthorityDecision{Agent: "worker", Verb: "job.gate.set", ActorJob: "squ-92", TargetJob: "squ-93"}) {
		t.Fatalf("worker job.gate.set should not be allowed for another job")
	}
	if !top.Authority.Allows(AuthorityDecision{Agent: "manager", Team: "frontend", Verb: "job.bounce", TargetTeam: "frontend"}) {
		t.Fatalf("manager job.bounce should be allowed for a target job in its team")
	}
	if top.Authority.Allows(AuthorityDecision{Agent: "manager", Team: "frontend", Verb: "job.bounce", TargetTeam: "research"}) {
		t.Fatalf("manager job.bounce should not be allowed for another team's job")
	}
	if top.Authority.Allows(AuthorityDecision{Agent: "worker", Verb: "job.merge"}) {
		t.Fatalf("worker job.merge should not be allowed")
	}
	if !top.Authority.Allows(AuthorityDecision{Operator: true, Verb: "job.merge"}) {
		t.Fatalf("trusted operator should be allowed by operator wildcard")
	}
	if top.Authority.Allows(AuthorityDecision{Agent: "operator", Verb: "job.merge"}) {
		t.Fatalf("spoofed operator agent without trusted operator marker should not be allowed")
	}
	if got := top.TeamForInstance("worker-squ-92"); got != "platform" {
		t.Fatalf("TeamForInstance(worker-squ-92) = %q, want platform", got)
	}
	if inst := top.FindRuntimeInstance("worker-squ-92", "worker"); inst == nil || inst.Name != "worker" {
		t.Fatalf("FindRuntimeInstance(worker-squ-92) = %+v, want worker", inst)
	}
	wantAllow := []string{"inbox.*", "job.*", "job.gate.set:own", "queue.retry"}
	if got := top.AuthorityAllowlistForInstance("worker-squ-92", "worker"); !reflect.DeepEqual(got, wantAllow) {
		t.Fatalf("AuthorityAllowlistForInstance = %#v, want %#v", got, wantAllow)
	}
	if got := top.AuthorityAllowlistForInstance("manager", "manager"); !reflect.DeepEqual(got, []string{"*", "job.bounce:team"}) {
		t.Fatalf("AuthorityAllowlistForInstance(manager) = %#v, want instance and agent grants", got)
	}
	if got := ScopedResourceName("build", ScopeTeam, "platform", "squ-92"); got != "team.platform.build" {
		t.Fatalf("ScopedResourceName = %q", got)
	}
	if got, err := ScopedChannelName("#supervisor", ScopeTeam, "platform", "squ-92"); err != nil || got != "#team-platform-supervisor" {
		t.Fatalf("ScopedChannelName = %q, %v", got, err)
	}
}

func TestParse_PipelineAuthoritySatisfiability(t *testing.T) {
	t.Run("frontend manager valid control", func(t *testing.T) {
		if _, err := Parse([]byte(managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["read"]

[authority.agents.manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team", "job.approve:team", "job.reject:team", "job.merge:team"]
`))); err != nil {
			t.Fatalf("Parse valid frontend topology: %v", err)
		}
	})

	t.Run("non-job duty cannot borrow pipeline team scope", func(t *testing.T) {
		teamScopedPublish := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish:team", "job.*:team"]
`)
		_, err := Parse([]byte(teamScopedPublish))
		assertAuthoritySatisfiabilityError(t, err,
			`pipeline "managed"`,
			`owner "frontend-manager"`,
			`"event.publish"`,
			`[authority.instances.frontend-manager].allow`,
		)

		unscopedPublish := strings.Replace(teamScopedPublish, "event.publish:team", "event.publish", 1)
		if _, err := Parse([]byte(unscopedPublish)); err != nil {
			t.Fatalf("Parse topology with runtime-satisfiable event.publish grant: %v", err)
		}
	})

	t.Run("frontend dead own grants", func(t *testing.T) {
		_, err := Parse([]byte(managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.bounce:own", "job.step:own", "job.gate.*:own", "job.approve:own", "job.reject:own", "job.merge:own"]
`)))
		assertAuthoritySatisfiabilityError(t, err, `pipeline "managed"`, `owner "frontend-manager"`, `"job.bounce:team"`, `[authority.instances.frontend-manager].allow`)
	})

	t.Run("research dead own grants", func(t *testing.T) {
		_, err := Parse([]byte(managedPipelineAuthorityFixture("research-manager", "research", "on_close", `
[authority.instances.research-manager]
allow = ["event.publish", "job.bounce:own", "job.step:own", "job.gate.*:own", "job.approve:own", "job.reject:own", "job.close:own"]
`)))
		assertAuthoritySatisfiabilityError(t, err, `pipeline "managed"`, `owner "research-manager"`, `"job.bounce:team"`, `[authority.instances.research-manager].allow`)
	})

	t.Run("declared merge duty", func(t *testing.T) {
		_, err := Parse([]byte(managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team", "job.approve:team", "job.reject:team"]
`)))
		assertAuthoritySatisfiabilityError(t, err, `"job.merge:team"`)
	})

	t.Run("declared close duty", func(t *testing.T) {
		_, err := Parse([]byte(managedPipelineAuthorityFixture("research-manager", "research", "on_close", `
[authority.instances.research-manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team", "job.approve:team", "job.reject:team"]
`)))
		assertAuthoritySatisfiabilityError(t, err, `"job.close:team"`)
	})

	for _, tc := range []struct {
		name          string
		reap          string
		requiredGrant string
	}{
		{name: "close-only pipeline", reap: "on_close", requiredGrant: "job.close:team"},
		{name: "merge-only reap pipeline", reap: "on_merge", requiredGrant: "job.merge:team"},
	} {
		t.Run(tc.name+" resolves completion owner", func(t *testing.T) {
			body := fmt.Sprintf(`
[instances.release-manager]
agent = "manager"

[[instances.release-manager.triggers]]
event = "job.completed"
match.pipeline = "release"
match.target = "manager"
match.source = "daemon:completion"

[instances.worker]
agent = "worker"
ephemeral = true

[pipelines.release]
trigger.event = "release.ready"
reap_worktree = %q

[[pipelines.release.steps]]
id = "prepare"
target = "worker"

[teams.release]
instances = ["release-manager", "worker"]
pipelines = ["release"]

[authority]
enforcement = "enforce"

[authority.instances.release-manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team"]
`, tc.reap)
			_, err := Parse([]byte(body))
			assertAuthoritySatisfiabilityError(t, err, `pipeline "release"`, `owner "release-manager"`, `"`+tc.requiredGrant+`"`)

			valid := strings.Replace(body, `"job.gate.*:team"]`, `"job.gate.*:team", "`+tc.requiredGrant+`"]`, 1)
			if _, err := Parse([]byte(valid)); err != nil {
				t.Fatalf("Parse valid %s: %v", tc.name, err)
			}
		})
	}

	t.Run("manual owner resolves canonical completion source", func(t *testing.T) {
		body := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.*:team"]
`)
		body = strings.Replace(body, `match.target = "frontend-manager"`, `match.target = "frontend-manager"
match.source = "daemon:completion"`, 1)
		if _, err := Parse([]byte(body)); err != nil {
			t.Fatalf("Parse completion-source-constrained owner: %v", err)
		}
	})

	t.Run("manual and terminal owners are validated independently", func(t *testing.T) {
		body := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team", "job.approve:team", "job.reject:team"]

[authority.instances.terminal-manager]
allow = ["read"]
`)
		body = strings.Replace(body, `
[[instances.frontend-manager.triggers]]
event = "job.completed"
match.pipeline = "managed"
`, "\n", 1)
		body = strings.Replace(body, `[instances.worker]`, `[instances.terminal-manager]
agent = "manager"

[[instances.terminal-manager.triggers]]
event = "job.completed"
match.pipeline = "managed"

[instances.worker]`, 1)
		body = strings.Replace(body, `instances = ["frontend-manager", "worker"]`, `instances = ["frontend-manager", "terminal-manager", "worker"]`, 1)

		_, err := Parse([]byte(body))
		assertAuthoritySatisfiabilityError(t, err, `pipeline "managed"`, `owner "terminal-manager"`, `reap_worktree "on_merge"`, `"job.merge:team"`, `[authority.instances.terminal-manager].allow`)

		valid := strings.Replace(body, `allow = ["read"]`, `allow = ["read", "job.merge:team"]`, 1)
		if _, err := Parse([]byte(valid)); err != nil {
			t.Fatalf("Parse independently authorized manual and terminal owners: %v", err)
		}
	})

	t.Run("manual gate requires approve and reject authority", func(t *testing.T) {
		base := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team", "job.merge:team"]
`)
		_, err := Parse([]byte(base))
		assertAuthoritySatisfiabilityError(t, err, `pipeline "managed"`, `owner "frontend-manager"`, `"job.approve:team"`)

		deadApprove := strings.Replace(base, `"job.merge:team"]`, `"job.approve:own", "job.reject:team", "job.merge:team"]`, 1)
		_, err = Parse([]byte(deadApprove))
		assertAuthoritySatisfiabilityError(t, err, `"job.approve:team"`)

		missingReject := strings.Replace(base, `"job.merge:team"]`, `"job.approve:team", "job.merge:team"]`, 1)
		_, err = Parse([]byte(missingReject))
		assertAuthoritySatisfiabilityError(t, err, `"job.reject:team"`)

		deadReject := strings.Replace(base, `"job.merge:team"]`, `"job.approve:team", "job.reject:own", "job.merge:team"]`, 1)
		_, err = Parse([]byte(deadReject))
		assertAuthoritySatisfiabilityError(t, err, `"job.reject:team"`)
	})

	t.Run("merge-only pipeline owner resolves from completion trigger", func(t *testing.T) {
		body := `
[instances.release-manager]
agent = "manager"

[[instances.release-manager.triggers]]
event = "job.completed"
match.pipeline = "release"

[instances.worker]
agent = "worker"
ephemeral = true

[pipelines.release]
trigger.event = "release.ready"

[pipelines.release.merge]
strategy = "squash"

[[pipelines.release.steps]]
id = "prepare"
target = "worker"

[teams.release]
instances = ["release-manager", "worker"]
pipelines = ["release"]

[authority]
enforcement = "enforce"

[authority.instances.release-manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team", "job.merge:team"]
`
		if _, err := Parse([]byte(body)); err != nil {
			t.Fatalf("Parse valid merge-only pipeline: %v", err)
		}
		withoutOwner := strings.Replace(body, `event = "job.completed"`, `event = "schedule"`, 1)
		_, err := Parse([]byte(withoutOwner))
		assertAuthoritySatisfiabilityError(t, err, `unsupported managing instance for declared merge`, `pipeline "release"`)
	})

	t.Run("dynamic completion trigger is ambiguous", func(t *testing.T) {
		body := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.*:team"]
`)
		body += `
[instances.catch-all-manager]
agent = "manager"

[[instances.catch-all-manager.triggers]]
event = "job.step_completed"

[authority.instances.catch-all-manager]
allow = ["event.publish", "job.*:team"]
`
		_, err := Parse([]byte(body))
		assertAuthoritySatisfiabilityError(t, err, `ambiguous completion owner`, `catch-all-manager, frontend-manager`)
	})

	t.Run("runtime-enriched completion match fields are unsupported", func(t *testing.T) {
		base := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.*:team"]
`)
		for _, tc := range []struct {
			name  string
			event string
			match string
			field string
			audit bool
		}{
			{name: "manual status", event: EventJobStepCompleted, match: `match.status = "done"`, field: "match.status"},
			{name: "manual completed step", event: EventJobStepCompleted, match: `match.completed_step = "implement"`, field: "match.completed_step"},
			{name: "terminal job status in audit", event: EventJobCompleted, match: `match.job_status = "done"`, field: "match.job_status", audit: true},
			{name: "terminal step status", event: EventJobCompleted, match: `match.step_status = "done"`, field: "match.step_status"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body := base
				if tc.audit {
					body = strings.Replace(body, `enforcement = "enforce"`, `enforcement = "audit"`, 1)
				}
				body += fmt.Sprintf(`
[instances.dynamic-manager]
agent = "manager"

[[instances.dynamic-manager.triggers]]
event = %q
match.pipeline = "managed"
%s
`, tc.event, tc.match)
				_, err := Parse([]byte(body))
				assertAuthoritySatisfiabilityError(t, err,
					`pipeline "managed"`,
					`unsupported dynamic completion owner "dynamic-manager"`,
					tc.event,
					tc.field,
					`runtime-enriched completion fields cannot determine manager ownership`,
				)
			})
		}

		unrelated := base + `
[instances.other-pipeline-manager]
agent = "manager"

[[instances.other-pipeline-manager.triggers]]
event = "job.step_completed"
match.pipeline = "other"
match.status = "done"
`
		if _, err := Parse([]byte(unrelated)); err != nil {
			t.Fatalf("Parse unrelated runtime-enriched completion trigger: %v", err)
		}
	})

	t.Run("audit still rejects ambiguous manager routing", func(t *testing.T) {
		body := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["read"]
`)
		body = strings.Replace(body, `enforcement = "enforce"`, `enforcement = "audit"`, 1)
		body += `
[instances.catch-all-manager]
agent = "manager"

[[instances.catch-all-manager.triggers]]
event = "job.step_completed"

[authority.instances.catch-all-manager]
allow = ["read"]
`
		_, err := Parse([]byte(body))
		assertAuthoritySatisfiabilityError(t, err, `pipeline "managed"`, `ambiguous completion owner`, `catch-all-manager, frontend-manager`)
	})

	t.Run("audit still rejects unsupported manager routing", func(t *testing.T) {
		body := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["read"]
`)
		body = strings.Replace(body, `enforcement = "enforce"`, `enforcement = "audit"`, 1)
		body = strings.Replace(body, `event = "job.step_completed"`, `event = "schedule"`, 1)
		_, err := Parse([]byte(body))
		assertAuthoritySatisfiabilityError(t, err, `pipeline "managed"`, `unsupported owner "frontend-manager"`, `no persistent instance trigger matches job.step_completed`)
	})

	t.Run("unsupported completion owner", func(t *testing.T) {
		body := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.*:team"]
`)
		body = strings.Replace(body, `event = "job.step_completed"`, `event = "job.completed"`, 1)
		_, err := Parse([]byte(body))
		assertAuthoritySatisfiabilityError(t, err, `unsupported owner "frontend-manager"`, `no persistent instance trigger matches job.step_completed`)
	})

	t.Run("ambiguous manual owners", func(t *testing.T) {
		body := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.*:team"]
`)
		body += `
[instances.release-manager]
agent = "manager"

[[instances.release-manager.triggers]]
event = "job.step_completed"
match.target = "release-manager"

[[pipelines.managed.steps]]
id = "release"
target = "release-manager"
after = ["decide"]
gate = "manual"

[authority.instances.release-manager]
allow = ["event.publish", "job.*:team"]
`
		_, err := Parse([]byte(body))
		assertAuthoritySatisfiabilityError(t, err, `ambiguous managing instances from manual gates`, `frontend-manager, release-manager`)
	})

	t.Run("grant string mutation cannot fake runtime scope", func(t *testing.T) {
		valid := managedPipelineAuthorityFixture("frontend-manager", "frontend", "on_merge", `
[authority.instances.frontend-manager]
allow = ["event.publish", "job.bounce:team", "job.step:team", "job.gate.*:team", "job.approve:team", "job.reject:team", "job.merge:team"]
`)
		if _, err := Parse([]byte(valid)); err != nil {
			t.Fatalf("Parse valid control: %v", err)
		}
		mutated := strings.ReplaceAll(valid, ":team", ":own")
		_, err := Parse([]byte(mutated))
		assertAuthoritySatisfiabilityError(t, err, `"job.bounce:team"`)
	})
}

func managedPipelineAuthorityFixture(owner, team, reap, authority string) string {
	return fmt.Sprintf(`
[instances.%[1]s]
agent = "manager"

[[instances.%[1]s.triggers]]
event = "job.step_completed"
match.target = "%[1]s"

[[instances.%[1]s.triggers]]
event = "job.completed"
match.pipeline = "managed"

[instances.worker]
agent = "worker"
ephemeral = true

[pipelines.managed]
trigger.event = "work.ready"
reap_worktree = "%[3]s"

[[pipelines.managed.steps]]
id = "implement"
target = "worker"

[[pipelines.managed.steps]]
id = "decide"
target = "%[1]s"
after = ["implement"]
gate = "manual"

[teams.%[2]s]
instances = ["%[1]s", "worker"]
pipelines = ["managed"]

[authority]
enforcement = "enforce"
%[4]s
`, owner, team, reap, authority)
}

func assertAuthoritySatisfiabilityError(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("Parse succeeded; want authority satisfiability error")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err, want)
		}
	}
}

func TestParse_RejectsInvalidResourceScope(t *testing.T) {
	_, err := Parse([]byte(`
[locks.build]
scope = "repo"
`))
	if err == nil || !strings.Contains(err.Error(), "scope must be machine, team, or job") {
		t.Fatalf("Parse err = %v, want scope validation", err)
	}
}

func TestParse_PipelineStepApprovalRequired(t *testing.T) {
	top, err := Parse([]byte(`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
gate = "manual"
approval_required = true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	step := top.Pipelines["ticket_to_pr"].Steps[0]
	if !step.ApprovalRequired {
		t.Fatalf("ApprovalRequired = false, want true")
	}
}

func TestParse_PipelineStepApprovalRequiredRequiresManualGate(t *testing.T) {
	_, err := Parse([]byte(`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
gate = "pr"
approval_required = true
`))
	if err == nil || !strings.Contains(err.Error(), "approval_required is only valid with gate manual") {
		t.Fatalf("Parse err = %v, want approval_required manual gate error", err)
	}
}

func TestParse_RestartPolicy(t *testing.T) {
	top, err := Parse([]byte(`
[instances.manager]
agent = "manager"
restart = "on-failure"

[instances.reviewer]
agent = "manager"
restart = "always"

[instances.worker]
agent = "worker"
ephemeral = true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := top.Instances["manager"].Restart; got != RestartOnFailure {
		t.Fatalf("manager restart = %q, want on-failure", got)
	}
	if got := top.Instances["reviewer"].Restart; got != RestartAlways {
		t.Fatalf("reviewer restart = %q, want always", got)
	}
	if got := top.Instances["worker"].Restart; got != RestartNever {
		t.Fatalf("worker restart = %q, want default never", got)
	}
}

func TestParse_RejectsInvalidRestartPolicy(t *testing.T) {
	_, err := Parse([]byte(`
[instances.manager]
agent = "manager"
restart = "unless-stopped"
`))
	if err == nil {
		t.Fatal("expected invalid restart policy error")
	}
	if !strings.Contains(err.Error(), "restart must be never, on-failure, or always") {
		t.Fatalf("error = %v", err)
	}
}

func TestParse_ExampleTopologies(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "topologies")
	paths, err := filepath.Glob(filepath.Join(root, "*.toml"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no example topology files found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			top, err := Parse(body)
			if err != nil {
				t.Fatalf("parse example: %v", err)
			}
			if len(top.Instances) == 0 || len(top.Teams) == 0 {
				t.Fatalf("example should declare instances and teams: %+v", top)
			}
		})
	}
}

func TestParse_Pipelines(t *testing.T) {
	top, err := Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true
reap_worktree = "on_close"
runtime = "codex"
runtime_bin = "codex-dev"
model = "claude-fable-5"
effort = "max"
token_budget = "40M"
time_budget = "45m"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
trigger.match.project = "Core"
reap_worktree = "on_merge"

[pipelines.ticket_to_pr.merge]
strategy = "script"
script = ".agent_team/scripts/union-merge.sh"
owned_paths = ["coverage/baselines", "coverage/counts.json"]

[pipelines.ticket_to_pr.infra_signatures]
disk_exhaustion = "No space left on device"
missing_binary = "error: test binary .* not found"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
label = "Manager review"
description = "Review implementation and prepare PR handoff."
instructions = "Review the worker branch and decide whether PR follow-up is ready."
target = "manager"
workspace = "repo"
runtime = "codex"
runtime_bin = "codex-dev"
model = "claude-sonnet-5"
effort = "high"
after = ["implement"]
gate = "pr"
optional = true
timeout = "30m"
token_budget = "10M"
time_budget = "20m"
reminder_levels = [50, 75, 100]
max_attempts = 2
retry_on_crash = true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := top.Pipelines["ticket_to_pr"]
	if p == nil {
		t.Fatal("pipeline missing")
	}
	if p.Trigger.Event != "ticket.created" || p.Trigger.Match["project"].Single != "Core" {
		t.Fatalf("trigger = %+v", p.Trigger)
	}
	if p.AutoAdvance {
		t.Fatalf("AutoAdvance should default to false, got true")
	}
	if p.RedispatchOnReentry {
		t.Fatalf("RedispatchOnReentry should default to false, got true")
	}
	if p.ReapWorktree != "on_merge" {
		t.Fatalf("pipeline ReapWorktree = %q, want on_merge", p.ReapWorktree)
	}
	if p.Merge == nil || p.Merge.Strategy != "script" || p.Merge.Script != ".agent_team/scripts/union-merge.sh" || strings.Join(p.Merge.OwnedPaths, ",") != "coverage/baselines,coverage/counts.json" {
		t.Fatalf("pipeline merge = %+v", p.Merge)
	}
	if p.InfraSignatures["disk_exhaustion"] != "No space left on device" || p.InfraSignatures["missing_binary"] != "error: test binary .* not found" {
		t.Fatalf("pipeline infra signatures = %+v", p.InfraSignatures)
	}
	if len(p.Steps) != 2 || p.Steps[0].Model != "" || p.Steps[0].Effort != "" || p.Steps[1].Label != "Manager review" || p.Steps[1].Description != "Review implementation and prepare PR handoff." || p.Steps[1].Instructions != "Review the worker branch and decide whether PR follow-up is ready." || p.Steps[1].Workspace != "repo" || p.Steps[1].Runtime != "codex" || p.Steps[1].RuntimeBin != "codex-dev" || p.Steps[1].Model != "claude-sonnet-5" || p.Steps[1].Effort != "high" || p.Steps[1].After[0] != "implement" || p.Steps[1].Gate != "pr" || !p.Steps[1].Optional || p.Steps[1].Timeout != 30*time.Minute || p.Steps[1].TokenBudget != 10000000 || p.Steps[1].TimeBudget != 20*time.Minute || !reflect.DeepEqual(p.Steps[1].ReminderLevels, []int{50, 75, 100}) || p.Steps[1].MaxAttempts != 2 || !p.Steps[1].RetryOnCrash {
		t.Fatalf("steps = %+v", p.Steps)
	}
	if worker := top.Instances["worker"]; worker == nil || worker.ReapWorktree != "on_close" || worker.Runtime != "codex" || worker.RuntimeBin != "codex-dev" || worker.Model != "claude-fable-5" || worker.Effort != "max" || worker.TokenBudget != 40000000 || worker.TimeBudget != 45*time.Minute {
		t.Fatalf("worker = %+v, want reap policy plus budgets", worker)
	}
	matched := top.ResolvePipelines("ticket.created", map[string]any{"project": "Core"})
	if len(matched) != 1 || matched[0].Name != "ticket_to_pr" {
		t.Fatalf("matched = %+v", matched)
	}
}

func TestParse_ModelPolicyResolvesInstancesAndPipelineTargets(t *testing.T) {
	top, err := Parse([]byte(`
[model_policy]
runtime = "codex"
model = "gpt-5.6-sol"
effort = "xhigh"

[instances.worker]
agent = "worker"
ephemeral = true

[instances.advisor]
agent = "advisor"
runtime = "claude"
model = "claude-fable-5"
effort = "max"

[pipelines.delivery]
trigger.event = "ticket.created"

[[pipelines.delivery.steps]]
id = "implement"
target = "worker"

[[pipelines.delivery.steps]]
id = "consult"
target = "advisor"
after = ["implement"]

[[pipelines.delivery.steps]]
id = "override"
target = "worker"
runtime = "docker"
model = "special-model"
effort = "low"
after = ["consult"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := top.ModelPolicy; got == nil || got.Runtime != "codex" || got.Model != "gpt-5.6-sol" || got.Effort != "xhigh" {
		t.Fatalf("model policy = %+v", got)
	}
	if got := top.Instances["worker"]; got.Runtime != "codex" || got.Model != "gpt-5.6-sol" || got.Effort != "xhigh" {
		t.Fatalf("worker policy = %q/%q/%q", got.Runtime, got.Model, got.Effort)
	}
	if got := top.Instances["advisor"]; got.Runtime != "claude" || got.Model != "claude-fable-5" || got.Effort != "max" {
		t.Fatalf("advisor override = %q/%q/%q", got.Runtime, got.Model, got.Effort)
	}
	steps := top.Pipelines["delivery"].Steps
	for _, want := range []struct {
		index                  int
		runtime, model, effort string
	}{
		{0, "codex", "gpt-5.6-sol", "xhigh"},
		{1, "claude", "claude-fable-5", "max"},
		{2, "docker", "special-model", "low"},
	} {
		got := steps[want.index]
		if got.Runtime != want.runtime || got.Model != want.model || got.Effort != want.effort {
			t.Fatalf("step %s policy = %q/%q/%q, want %q/%q/%q", got.ID, got.Runtime, got.Model, got.Effort, want.runtime, want.model, want.effort)
		}
	}
}

func TestResolveRuntimePolicyKeepsSelectorsWithinRuntimeFamily(t *testing.T) {
	tests := []struct {
		name      string
		inherited ModelPolicy
		override  ModelPolicy
		want      ModelPolicy
	}{
		{
			name:      "same family inherits selectors",
			inherited: ModelPolicy{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "xhigh"},
			override:  ModelPolicy{Runtime: "codex"},
			want:      ModelPolicy{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "xhigh"},
		},
		{
			name:      "non-Fable to Claude clears selectors",
			inherited: ModelPolicy{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "xhigh"},
			override:  ModelPolicy{Runtime: "claude"},
			want:      ModelPolicy{Runtime: "claude"},
		},
		{
			name:      "Fable to Codex clears selectors",
			inherited: ModelPolicy{Runtime: "claude", Model: "claude-fable-5", Effort: "max"},
			override:  ModelPolicy{Runtime: "codex"},
			want:      ModelPolicy{Runtime: "codex"},
		},
		{
			name:      "new family explicit selectors are authoritative",
			inherited: ModelPolicy{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "xhigh"},
			override:  ModelPolicy{Runtime: "claude", Model: "claude-fable-5", Effort: "max"},
			want:      ModelPolicy{Runtime: "claude", Model: "claude-fable-5", Effort: "max"},
		},
		{
			name:      "partial new family selector does not retain omitted field",
			inherited: ModelPolicy{Runtime: "claude", Model: "claude-fable-5", Effort: "max"},
			override:  ModelPolicy{Runtime: "codex", Model: "gpt-5.6-sol"},
			want:      ModelPolicy{Runtime: "codex", Model: "gpt-5.6-sol"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveRuntimePolicy(tt.inherited, tt.override); got != tt.want {
				t.Fatalf("ResolveRuntimePolicy() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParse_ModelPolicyClearsSelectorsForRuntimeOnlyInstanceOverrides(t *testing.T) {
	tests := []struct {
		name                                     string
		policyRuntime, policyModel, policyEffort string
		instanceRuntime                          string
	}{
		{"non-Fable to Claude", "codex", "gpt-5.6-sol", "xhigh", "claude"},
		{"Fable to Codex", "claude", "claude-fable-5", "max", "codex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top, err := Parse([]byte(fmt.Sprintf(`
[model_policy]
runtime = %q
model = %q
effort = %q

[instances.override]
agent = "worker"
runtime = %q
`, tt.policyRuntime, tt.policyModel, tt.policyEffort, tt.instanceRuntime)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := top.Instances["override"]
			if got.Runtime != tt.instanceRuntime || got.Model != "" || got.Effort != "" {
				t.Fatalf("runtime-only instance override = %q/%q/%q, want %q with empty selectors", got.Runtime, got.Model, got.Effort, tt.instanceRuntime)
			}
		})
	}
}

func TestParse_ModelPolicyClearsSelectorsForRuntimeOnlyPipelineOverrides(t *testing.T) {
	top, err := Parse([]byte(`
[model_policy]
runtime = "codex"
model = "gpt-5.6-sol"
effort = "xhigh"

[instances.worker]
agent = "worker"

[instances.advisor]
agent = "advisor"
runtime = "claude"
model = "claude-fable-5"
effort = "max"

[pipelines.compatibility]
trigger.event = "agent.dispatch"

[[pipelines.compatibility.steps]]
id = "non-fable-to-claude"
target = "worker"
runtime = "claude"

[[pipelines.compatibility.steps]]
id = "fable-to-codex"
target = "advisor"
runtime = "codex"
after = ["non-fable-to-claude"]

[[pipelines.compatibility.steps]]
id = "explicit-new-family-selectors"
target = "worker"
runtime = "claude"
model = "claude-fable-5"
effort = "max"
after = ["fable-to-codex"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	steps := top.Pipelines["compatibility"].Steps
	for _, index := range []int{0, 1} {
		if got := steps[index]; got.Model != "" || got.Effort != "" {
			t.Fatalf("runtime-only step %s = %q/%q/%q, want empty selectors", got.ID, got.Runtime, got.Model, got.Effort)
		}
	}
	if got := steps[2]; got.Runtime != "claude" || got.Model != "claude-fable-5" || got.Effort != "max" {
		t.Fatalf("explicit new-family step = %q/%q/%q, want claude/claude-fable-5/max", got.Runtime, got.Model, got.Effort)
	}
}

func TestParse_ModelPolicyValidatesFieldTypes(t *testing.T) {
	_, err := Parse([]byte(`
[model_policy]
runtime = "codex"
model = 56
`))
	if err == nil || !strings.Contains(err.Error(), "model_policy: model must be a string") {
		t.Fatalf("Parse error = %v, want model policy type error", err)
	}
}

func TestParse_DockerRuntime(t *testing.T) {
	top, err := Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true
runtime = "docker"

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
runtime = "docker"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if top.Instances["worker"].Runtime != "docker" {
		t.Fatalf("instance runtime = %q, want docker", top.Instances["worker"].Runtime)
	}
	if got := top.Pipelines["ticket_to_pr"].Steps[0].Runtime; got != "docker" {
		t.Fatalf("step runtime = %q, want docker", got)
	}
}

func TestParse_PipelineStepModelRequiresString(t *testing.T) {
	_, err := Parse([]byte(`
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
model = 42
`))
	if err == nil || !strings.Contains(err.Error(), "pipeline \"ticket_to_pr\" step[0]: model must be a string") {
		t.Fatalf("Parse error = %v, want model type error", err)
	}
}

func TestParse_PipelineStepEffortRequiresString(t *testing.T) {
	_, err := Parse([]byte(`
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
effort = 42
`))
	if err == nil || !strings.Contains(err.Error(), "pipeline \"ticket_to_pr\" step[0]: effort must be a string") {
		t.Fatalf("Parse error = %v, want effort type error", err)
	}
}

func TestParse_InstanceEffortRequiresString(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"
effort = 42
`))
	if err == nil || !strings.Contains(err.Error(), "instance \"worker\": effort must be a string") {
		t.Fatalf("Parse error = %v, want effort type error", err)
	}
}

func TestParse_FableMaxEffortExamples(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".agent_team", "instances.toml"))
	if err != nil {
		t.Fatalf("read self-dogfood topology: %v", err)
	}
	top, err := Parse(body)
	if err != nil {
		t.Fatalf("parse self-dogfood topology: %v", err)
	}
	if got := top.ModelPolicy; got == nil || got.Runtime != "codex" || got.Model != "gpt-5.6-sol" || got.Effort != "xhigh" {
		t.Fatalf("self-dogfood model policy = %+v", got)
	}
	fable := make([]string, 0, 3)
	for name, inst := range top.Instances {
		if inst.Model == "claude-fable-5" {
			fable = append(fable, name)
			if inst.Runtime != "claude" || inst.Effort != "max" {
				t.Fatalf("self-dogfood Fable seat %s = %q/%q/%q", name, inst.Runtime, inst.Model, inst.Effort)
			}
			continue
		}
		if inst.Runtime != "codex" || inst.Model != "gpt-5.6-sol" || inst.Effort != "xhigh" {
			t.Fatalf("self-dogfood non-Fable seat %s = %q/%q/%q", name, inst.Runtime, inst.Model, inst.Effort)
		}
	}
	sort.Strings(fable)
	if want := []string{"advisor", "harness-reviewer", "org-review"}; !reflect.DeepEqual(fable, want) {
		t.Fatalf("self-dogfood Fable seats = %v, want %v", fable, want)
	}
	for _, name := range fable {
		inst := top.Instances[name]
		if inst.Runtime != "claude" || inst.Model != "claude-fable-5" || inst.Effort != "max" {
			t.Fatalf("self-dogfood %s runtime/model/effort = %q/%q/%q", name, inst.Runtime, inst.Model, inst.Effort)
		}
	}
	for pipelineName, pipeline := range top.Pipelines {
		for _, step := range pipeline.Steps {
			target := top.Instances[step.Target]
			if target == nil || step.Runtime != target.Runtime || step.Model != target.Model || step.Effort != target.Effort {
				t.Fatalf("self-dogfood pipeline %s step %s policy = %q/%q/%q, target=%+v", pipelineName, step.ID, step.Runtime, step.Model, step.Effort, target)
			}
		}
	}

	body, err = os.ReadFile(filepath.Join("..", "..", "template", "topology", "instances.toml.tmpl.d", "50_full_quality_loops.toml.tmpl"))
	if err != nil {
		t.Fatalf("read template quality loops: %v", err)
	}
	top, err = Parse(body)
	if err != nil {
		t.Fatalf("parse template quality loops: %v", err)
	}
	for _, name := range []string{"advisor", "harness-reviewer", "org-review"} {
		inst := top.Instances[name]
		if inst == nil {
			t.Fatalf("template instance %q missing", name)
		}
		if inst.Runtime != "claude" || inst.Model != "claude-fable-5" || inst.Effort != "max" {
			t.Fatalf("template %s runtime/model/effort = %q/%q/%q", name, inst.Runtime, inst.Model, inst.Effort)
		}
	}
}

func TestParse_PipelineInfraSignaturesValidation(t *testing.T) {
	_, err := Parse([]byte(`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[pipelines.ticket_to_pr.infra_signatures]
disk_exhaustion = "["

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`))
	if err == nil {
		t.Fatal("expected invalid regex error")
	}
	if !strings.Contains(err.Error(), "infra_signatures.disk_exhaustion") || !strings.Contains(err.Error(), "invalid regex") {
		t.Fatalf("error = %v", err)
	}
}

func TestParse_PipelineMergeValidation(t *testing.T) {
	base := `
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "invalid strategy",
			body: `
[pipelines.ticket_to_pr.merge]
strategy = "merge-commit"
`,
			want: "merge strategy must be squash, rebase, or script",
		},
		{
			name: "script requires script path",
			body: `
[pipelines.ticket_to_pr.merge]
strategy = "script"
`,
			want: "script is required",
		},
		{
			name: "script rejected for squash",
			body: `
[pipelines.ticket_to_pr.merge]
strategy = "squash"
script = "merge.sh"
`,
			want: "script is only valid",
		},
		{
			name: "owned paths repo relative",
			body: `
[pipelines.ticket_to_pr.merge]
strategy = "squash"
owned_paths = ["/absolute"]
`,
			want: "must be repo-relative",
		},
		{
			name: "invalid land",
			body: `
[pipelines.ticket_to_pr.merge]
strategy = "squash"
land = "ff-only"
`,
			want: "merge land must be squash, merge, or rebase",
		},
		{
			name: "conflicting land declarations",
			body: `
[pipelines.fork_sync]
trigger.event = "ticket.created"
land = "merge"

[pipelines.fork_sync.merge]
strategy = "squash"
land = "rebase"

[[pipelines.fork_sync.steps]]
id = "implement"
target = "worker"
`,
			want: "conflicts with pipeline land",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(base + tc.body))
			if err == nil {
				t.Fatal("expected parse error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParse_PipelineMergeLand(t *testing.T) {
	top, err := Parse([]byte(`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[pipelines.ticket_to_pr.merge]
strategy = "squash"
land = "merge"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[pipelines.fork_sync]
trigger.event = "ticket.created"
land = "rebase"

[[pipelines.fork_sync.steps]]
id = "implement"
target = "worker"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := top.Pipelines["ticket_to_pr"].Merge.Land; got != "merge" {
		t.Fatalf("ticket_to_pr land = %q, want merge", got)
	}
	if merge := top.Pipelines["fork_sync"].Merge; merge == nil || merge.Strategy != "squash" || merge.Land != "rebase" {
		t.Fatalf("fork_sync merge = %+v, want implicit squash strategy with rebase land", merge)
	}
}

func TestParse_PipelineAutoAdvance(t *testing.T) {
	top, err := Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
auto_advance = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := top.Pipelines["ticket_to_pr"]
	if p == nil {
		t.Fatal("pipeline missing")
	}
	if !p.AutoAdvance {
		t.Fatalf("AutoAdvance = false, want true when auto_advance = true")
	}
}

func TestParse_PipelineRedispatchOnReentry(t *testing.T) {
	top, err := Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.status_changed"
trigger.match.status = "Ready for Agent"
redispatch_on_reentry = true

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := top.Pipelines["ticket_to_pr"]
	if p == nil {
		t.Fatal("pipeline missing")
	}
	if !p.RedispatchOnReentry {
		t.Fatalf("RedispatchOnReentry = false, want true")
	}
	matched := top.ResolvePipelines("ticket.status_changed", map[string]any{"status": "Ready for Agent"})
	if len(matched) != 1 || matched[0].Name != "ticket_to_pr" {
		t.Fatalf("matched = %+v", matched)
	}
}

func TestParse_RejectsInvalidReapWorktree(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "instance",
			body: `
[instances.worker]
agent = "worker"
reap_worktree = "always"
`,
		},
		{
			name: "pipeline",
			body: `
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
reap_worktree = "always"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.body))
			if err == nil {
				t.Fatal("expected invalid reap_worktree error")
			}
			if !strings.Contains(err.Error(), "reap_worktree must be on_close, on_merge, or never") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParse_PipelineRejectsInvalidStepText(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "empty label", line: `label = " "`, want: "label must be a non-empty string"},
		{name: "non-string label", line: "label = 10", want: "label must be a non-empty string"},
		{name: "empty description", line: `description = ""`, want: "description must be a non-empty string"},
		{name: "non-string description", line: "description = true", want: "description must be a non-empty string"},
		{name: "empty instructions", line: `instructions = ""`, want: "instructions must be a non-empty string"},
		{name: "non-string instructions", line: "instructions = 10", want: "instructions must be a non-empty string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(`
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
` + tt.line + `
`))
			if err == nil {
				t.Fatal("expected step text error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParse_PipelineRejectsInvalidWorkspace(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
workspace = "scratch"
`))
	if err == nil {
		t.Fatal("expected invalid workspace error")
	}
	if !strings.Contains(err.Error(), "workspace must be auto, worktree, or repo") {
		t.Fatalf("error = %v", err)
	}
}

func TestParse_PipelineRejectsInvalidRuntime(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
runtime = "llama"
`))
	if err == nil {
		t.Fatal("expected invalid runtime error")
	}
	if !strings.Contains(err.Error(), "runtime must be claude, codex, or docker") {
		t.Fatalf("error = %v", err)
	}
}

func TestParse_InstanceRejectsInvalidRuntime(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"
runtime = "llama"
`))
	if err == nil {
		t.Fatal("expected invalid runtime error")
	}
	if !strings.Contains(err.Error(), "runtime must be claude, codex, or docker") {
		t.Fatalf("error = %v", err)
	}
}

func TestParse_PipelineRejectsNonBooleanOptional(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
optional = "yes"
`))
	if err == nil {
		t.Fatal("expected optional type error")
	}
	if !strings.Contains(err.Error(), "optional must be a boolean") {
		t.Fatalf("error = %v", err)
	}
}

func TestParse_PipelineRejectsInvalidMaxAttempts(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
max_attempts = 0
`))
	if err == nil || !strings.Contains(err.Error(), "max_attempts must be greater than zero") {
		t.Fatalf("err = %v", err)
	}
}

func TestParse_PipelineRejectsInvalidRetryOnCrash(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "reviewer"
retry_on_crash = "yes"
`))
	if err == nil || !strings.Contains(err.Error(), "retry_on_crash must be a boolean") {
		t.Fatalf("err = %v", err)
	}
}

func TestParse_PipelineRejectsUnknownGate(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
gate = "robot"
`))
	if err == nil {
		t.Fatal("expected unknown gate error")
	}
	if !strings.Contains(err.Error(), "gate must be manual or pr") {
		t.Fatalf("error = %v", err)
	}
}

func TestParse_PipelineRejectsInvalidTimeout(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "non-string", line: "timeout = 10", want: "timeout must be a non-empty duration string"},
		{name: "invalid", line: `timeout = "soon"`, want: "timeout must be a valid duration"},
		{name: "zero", line: `timeout = "0s"`, want: "timeout must be greater than zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(`
[instances.worker]
agent = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
` + tt.line + `
`))
			if err == nil {
				t.Fatal("expected timeout error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParse_Teams(t *testing.T) {
	top, err := Parse([]byte(`
[instances.manager]
agent = "manager"

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

[schedules.nightly]
every = "24h"

[teams.delivery]
description = "Default delivery team."
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
schedules = ["nightly"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	team := top.Teams["delivery"]
	if team == nil {
		t.Fatal("team missing")
	}
	if team.Description != "Default delivery team." {
		t.Fatalf("description = %q", team.Description)
	}
	if !reflect.DeepEqual(team.Instances, []string{"manager", "worker"}) {
		t.Fatalf("instances = %+v", team.Instances)
	}
	if !reflect.DeepEqual(team.Pipelines, []string{"ticket_to_pr"}) {
		t.Fatalf("pipelines = %+v", team.Pipelines)
	}
	if !reflect.DeepEqual(team.Schedules, []string{"nightly"}) {
		t.Fatalf("schedules = %+v", team.Schedules)
	}
	if got := top.SortedTeams(); len(got) != 1 || got[0].Name != "delivery" {
		t.Fatalf("SortedTeams = %+v", got)
	}
	if top.FindTeam("delivery") != team {
		t.Fatalf("FindTeam did not return team")
	}
}

func TestParse_Budgets(t *testing.T) {
	top, err := Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true

[teams.delivery]
instances = ["worker"]

[budgets]
reminder_levels = [25, 75, 100]

[budgets.delivery]
tokens_per_day = 200_000_000
jobs_in_flight = 4
allocation = "reserve"
load_weight = 2.5

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
token_budget = "40M"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	budget := top.FindBudget("delivery")
	if budget == nil {
		t.Fatal("budget missing")
	}
	if budget.Team != "delivery" || budget.TokensPerDay != 200_000_000 || budget.JobsInFlight != 4 || budget.Allocation != BudgetAllocationReserve || budget.LoadWeight != 2.5 {
		t.Fatalf("budget = %+v", budget)
	}
	if got := top.SortedBudgets(); len(got) != 1 || got[0] != budget {
		t.Fatalf("SortedBudgets = %+v", got)
	}
	if !reflect.DeepEqual(top.ReminderLevels, []int{25, 75, 100}) {
		t.Fatalf("reminder levels = %+v", top.ReminderLevels)
	}
	step := top.Pipelines["ticket_to_pr"].Steps[0]
	if !reflect.DeepEqual(step.ReminderLevels, []int{25, 75, 100}) {
		t.Fatalf("step reminder levels = %+v", step.ReminderLevels)
	}
}

func TestParse_Concurrency(t *testing.T) {
	top, err := Parse([]byte(`
[concurrency]
enabled = true
min_ceiling = 3
max_ceiling = 20
initial_ceiling = 7
target_load_per_core = 0.85
load_per_dispatch = 1.25
crash_window = "10m"
crash_threshold = 3
decrease_factor = 0.5
stable_window = "20m"
increase_step = 2

[instances.worker]
agent = "worker"
ephemeral = true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cfg := top.Concurrency
	if cfg == nil || !cfg.Enabled {
		t.Fatalf("concurrency = %+v, want enabled config", cfg)
	}
	if cfg.MinCeiling != 3 || cfg.MaxCeiling != 20 || cfg.InitialCeiling != 7 || cfg.TargetLoadPerCore != 0.85 || cfg.LoadPerDispatch != 1.25 || cfg.CrashWindow != 10*time.Minute || cfg.CrashThreshold != 3 || cfg.DecreaseFactor != 0.5 || cfg.StableWindow != 20*time.Minute || cfg.IncreaseStep != 2 {
		t.Fatalf("concurrency = %+v", cfg)
	}
}

func TestParse_ConcurrencyValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "initial above max",
			body: `
[concurrency]
enabled = true
max_ceiling = 5
initial_ceiling = 6
`,
			want: "initial_ceiling must be <= max_ceiling",
		},
		{
			name: "bad load target",
			body: `
[concurrency]
enabled = true
target_load_per_core = 0
`,
			want: "target_load_per_core must be > 0",
		},
		{
			name: "bad decrease factor",
			body: `
[concurrency]
enabled = true
decrease_factor = 1.0
`,
			want: "decrease_factor must be > 0 and < 1",
		},
		{
			name: "bad stable window",
			body: `
[concurrency]
enabled = true
stable_window = "0s"
`,
			want: "stable_window must be greater than zero",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParse_BudgetValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "negative tokens",
			body: `
[instances.worker]
agent = "worker"

[teams.delivery]
instances = ["worker"]

[budgets.delivery]
tokens_per_day = -1
`,
			want: "tokens_per_day must be >= 0",
		},
		{
			name: "negative jobs",
			body: `
[instances.worker]
agent = "worker"

[teams.delivery]
instances = ["worker"]

[budgets.delivery]
jobs_in_flight = -1
`,
			want: "jobs_in_flight must be >= 0",
		},
		{
			name: "invalid allocation",
			body: `
	[instances.worker]
agent = "worker"

[teams.delivery]
instances = ["worker"]

[budgets.delivery]
allocation = "strict"
	`,
			want: "allocation must be reserve or oversubscribe",
		},
		{
			name: "bad load weight",
			body: `
	[instances.worker]
	agent = "worker"

	[teams.delivery]
	instances = ["worker"]

	[budgets.delivery]
	load_weight = 0
	`,
			want: "load_weight must be > 0",
		},
		{
			name: "unknown team",
			body: `
[instances.worker]
agent = "worker"

[teams.delivery]
instances = ["worker"]

[budgets.platform]
jobs_in_flight = 1
`,
			want: `references unknown team "platform"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.body))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParse_Locks(t *testing.T) {
	top, err := Parse([]byte(`
[locks.cargo]
slots = 2

[locks.db]

[instances.worker]
agent = "worker"
ephemeral = true
locks = ["cargo"]

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
locks = ["db"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := top.Locks["cargo"]; got == nil || got.Slots != 2 {
		t.Fatalf("cargo lock = %+v", got)
	}
	if got := top.Locks["db"]; got == nil || got.Slots != 1 {
		t.Fatalf("db lock = %+v", got)
	}
	if !reflect.DeepEqual(top.Instances["worker"].Locks, []string{"cargo"}) {
		t.Fatalf("instance locks = %+v", top.Instances["worker"].Locks)
	}
	step := top.Pipelines["ticket_to_pr"].Steps[0]
	if !reflect.DeepEqual(step.Locks, []string{"db"}) {
		t.Fatalf("step locks = %+v", step.Locks)
	}
	if got := top.SortedLocks(); len(got) != 2 || got[0].Name != "cargo" || got[1].Name != "db" {
		t.Fatalf("SortedLocks = %+v", got)
	}
}

func TestParse_LocksRejectBadReferences(t *testing.T) {
	_, err := Parse([]byte(`
[instances.worker]
agent = "worker"
ephemeral = true
locks = ["missing"]
`))
	if err == nil {
		t.Fatal("expected unknown instance lock error")
	}
	if !strings.Contains(err.Error(), `instance "worker": locks references unknown lock "missing"`) {
		t.Fatalf("err = %v", err)
	}

	_, err = Parse([]byte(`
[locks.cargo]
slots = 1

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
locks = ["missing"]
`))
	if err == nil {
		t.Fatal("expected unknown step lock error")
	}
	if !strings.Contains(err.Error(), `pipeline "ticket_to_pr" step "implement": locks references unknown lock "missing"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestParse_LocksRejectBadSlots(t *testing.T) {
	_, err := Parse([]byte(`
[locks.cargo]
slots = 0
`))
	if err == nil {
		t.Fatal("expected slots error")
	}
	if !strings.Contains(err.Error(), `lock "cargo": slots must be >= 1`) {
		t.Fatalf("err = %v", err)
	}
}

func TestParse_TeamRejectsUnknownReference(t *testing.T) {
	_, err := Parse([]byte(`
[instances.manager]
agent = "manager"

[teams.delivery]
instances = ["manager"]
pipelines = ["missing"]
`))
	if err == nil {
		t.Fatal("expected unknown pipeline error")
	}
	if !strings.Contains(err.Error(), `team "delivery": pipelines references unknown pipeline "missing"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestParse_PipelineRejectsUnknownAfter(t *testing.T) {
	_, err := Parse([]byte(`
[pipelines.bad]
trigger.event = "ticket.created"

[[pipelines.bad.steps]]
id = "review"
target = "manager"
after = ["implement"]
`))
	if err == nil {
		t.Fatal("expected unknown after error")
	}
}

func TestParse_Schedules(t *testing.T) {
	top, err := Parse([]byte(`
[instances.manager]
agent = "manager"

[schedules.nightly]
every = "1h"
run_on_start = true
payload.workspace = "repo"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s := top.Schedules["nightly"]
	if s == nil {
		t.Fatal("schedule missing")
	}
	if s.Every.String() != "1h0m0s" || !s.RunOnStart || s.Payload["workspace"] != "repo" {
		t.Fatalf("schedule = %+v", s)
	}
	payload := s.EventPayload()
	if payload["source"] != "schedule" || payload["name"] != "nightly" || payload["workspace"] != "repo" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestParse_RejectsMissingAgent(t *testing.T) {
	_, err := Parse([]byte(`
[instances.broken]
ephemeral = false
`))
	if err == nil {
		t.Fatal("expected error on missing agent")
	}
}

func TestParse_RejectsBadReplicasOnPersistent(t *testing.T) {
	_, err := Parse([]byte(`
[instances.broken]
agent     = "manager"
ephemeral = false
replicas  = 2
`))
	if err == nil {
		t.Fatal("expected error: replicas only valid on ephemeral")
	}
}

func TestParse_AllowsEmptyMatch(t *testing.T) {
	top, err := Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "user_invocation"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	mgr := top.Instances["manager"]
	if got := len(mgr.Triggers[0].Match); got != 0 {
		t.Errorf("expected empty match, got %d entries", got)
	}
}

func TestParse_RejectsEmptyMatchValue(t *testing.T) {
	_, err := Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event         = "ticket.created"
match.project = ""
`))
	if err == nil {
		t.Fatal("expected error on empty match value")
	}
}

func TestMatch_SingleAndList(t *testing.T) {
	mv := MatchValue{Single: "Platform"}
	if !mv.Eval("Platform") {
		t.Error("single equality failed")
	}
	if mv.Eval("Mobile") {
		t.Error("single mismatch should fail")
	}
	mv2 := MatchValue{List: []string{"created", "updated"}}
	if !mv2.Eval("created") || !mv2.Eval("updated") {
		t.Error("list membership failed")
	}
	if mv2.Eval("deleted") {
		t.Error("list non-member should fail")
	}
}

func TestResolve_TicketRouting(t *testing.T) {
	top, err := Parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	matched := top.Resolve("ticket.created", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 1 || matched[0].Name != "tm-platform" {
		t.Fatalf("expected only tm-platform, got %v", names(matched))
	}
	matched = top.Resolve("ticket.created", map[string]any{
		"project": "Mobile",
	})
	if len(matched) != 1 || matched[0].Name != "tm-mobile" {
		t.Fatalf("expected only tm-mobile, got %v", names(matched))
	}
	matched = top.Resolve("ticket.updated", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 1 || matched[0].Name != "tm-platform" {
		t.Fatalf("expected ticket.updated to match tm-platform, got %v", names(matched))
	}
	matched = top.Resolve("ticket.deleted", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 0 {
		t.Errorf("ticket.deleted should not match exact ticket triggers: %v", names(matched))
	}
	matched = top.Resolve("agent.dispatch", map[string]any{"target": "worker"})
	if len(matched) != 1 || matched[0].Name != "worker" {
		t.Errorf("worker dispatch: %v", names(matched))
	}
	// Missing payload key → no match.
	matched = top.Resolve("ticket.created", map[string]any{})
	if len(matched) != 0 {
		t.Errorf("empty payload should match nothing for keyed triggers: %v", names(matched))
	}
}

func TestResolve_TicketEventsMatchExactly(t *testing.T) {
	top, err := Parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	matched := top.Resolve("ticket.created", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 1 || matched[0].Name != "tm-platform" {
		t.Fatalf("expected ticket.created to match tm-platform trigger, got %v", names(matched))
	}
	matched = top.Resolve("ticket.updated", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 1 || matched[0].Name != "tm-platform" {
		t.Fatalf("expected ticket.updated to match tm-platform trigger, got %v", names(matched))
	}
	matched = top.Resolve("ticket.commented", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 0 {
		t.Fatalf("ticket.commented should miss exact ticket triggers, got %v", names(matched))
	}
	matched = top.Resolve("ticket.created", map[string]any{
		"project": "Mobile",
	})
	if len(matched) != 1 || matched[0].Name != "tm-mobile" {
		t.Fatalf("expected ticket.created to match tm-mobile trigger, got %v", names(matched))
	}
}

func TestResolve_PREventsMatchExactly(t *testing.T) {
	top, err := Parse([]byte(`
[instances.pr-reviewer]
agent = "manager"

[[instances.pr-reviewer.triggers]]
event = "pr.opened"
match.repository = "agent-team-project/agent-team"

[[instances.pr-reviewer.triggers]]
event = "pr.merged"
match.repository = "agent-team-project/agent-team"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	matched := top.Resolve("pr.opened", map[string]any{
		"repository": "agent-team-project/agent-team",
	})
	if len(matched) != 1 || matched[0].Name != "pr-reviewer" {
		t.Fatalf("expected pr.opened to match PR trigger, got %v", names(matched))
	}
	matched = top.Resolve("pr.merged", map[string]any{
		"repository": "agent-team-project/agent-team",
	})
	if len(matched) != 1 || matched[0].Name != "pr-reviewer" {
		t.Fatalf("expected pr.merged to match PR trigger, got %v", names(matched))
	}
	matched = top.Resolve("pr.closed", map[string]any{
		"repository": "agent-team-project/agent-team",
	})
	if len(matched) != 0 {
		t.Fatalf("pr.closed should miss exact PR triggers, got %v", names(matched))
	}
}

func TestResolvePipelines_TicketEventsMatchExactly(t *testing.T) {
	top, err := Parse([]byte(`
[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
trigger.match.project = "Core"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	matched := top.ResolvePipelines("ticket.created", map[string]any{"project": "Core"})
	if len(matched) != 1 || matched[0].Name != "ticket_to_pr" {
		t.Fatalf("expected ticket.created to match pipeline trigger, got %+v", matched)
	}
	matched = top.ResolvePipelines("ticket.updated", map[string]any{"project": "Core"})
	if len(matched) != 0 {
		t.Fatalf("ticket.updated should miss exact pipeline trigger, got %+v", matched)
	}
}

func TestResolve_UserInvocationMatchesAny(t *testing.T) {
	top, err := Parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Empty match → matches any payload of that event type.
	matched := top.Resolve("user_invocation", map[string]any{"name": "manager"})
	if len(matched) != 1 || matched[0].Name != "manager" {
		t.Errorf("user_invocation should match manager: %v", names(matched))
	}
}

func TestTrace_ExplainsInstanceAndPipelineTriggerDecisions(t *testing.T) {
	top, err := Parse([]byte(`
[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "user_invocation"

[instances.worker]
agent = "worker"
ephemeral = true

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.core]
trigger.event = "ticket.created"
trigger.match.project = "Core"

[[pipelines.core.steps]]
id = "implement"
target = "worker"

[pipelines.graphql]
trigger.event = "ticket.created"
trigger.match.project = "GraphQL"

[[pipelines.graphql.steps]]
id = "implement"
target = "worker"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	trace := top.Trace("ticket.created", map[string]any{"project": "GraphQL"})
	if trace.MatchedRules != 1 {
		t.Fatalf("matched rules = %d, want 1: %+v", trace.MatchedRules, trace.Entries)
	}
	manager := findTraceEntry(t, trace, "instances.manager")
	if manager.Matched || manager.Reason != "event type mismatch" {
		t.Fatalf("manager trace = %+v", manager)
	}
	core := findTraceEntry(t, trace, "pipelines.core")
	if core.Matched || core.Matcher != "match.project=Core" || core.Reason != "payload project=GraphQL != Core" {
		t.Fatalf("core trace = %+v", core)
	}
	graphql := findTraceEntry(t, trace, "pipelines.graphql")
	if !graphql.Matched || graphql.Reason != EventTraceReasonMatched || graphql.FirstStep == nil || graphql.FirstStep.ID != "implement" || graphql.FirstStep.Target != "worker" {
		t.Fatalf("graphql trace = %+v", graphql)
	}
	if got := trace.MatchedPipelineNames(); !reflect.DeepEqual(got, []string{"graphql"}) {
		t.Fatalf("matched pipelines = %v", got)
	}
}

func TestTrace_ExplainsNormalizedEventPayloadPredicates(t *testing.T) {
	top, err := Parse([]byte(`
[instances.tm]
agent = "ticket-manager"

[[instances.tm.triggers]]
event = "ticket.created"
match.kind = ["bug", "feature"]
match.project = "Core"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	chore := top.Trace("ticket.created", map[string]any{"project": "Core", "kind": "chore"})
	choreEntry := findTraceEntry(t, chore, "instances.tm")
	if choreEntry.Matched || choreEntry.Matcher != "match.kind=[bug, feature]" || choreEntry.Reason != "payload kind=chore not in [bug, feature]" {
		t.Fatalf("chore trace = %+v", choreEntry)
	}
	missing := top.Trace("ticket.created", map[string]any{})
	missingEntry := findTraceEntry(t, missing, "instances.tm")
	if missingEntry.Matched || missingEntry.Matcher != "match.kind=[bug, feature]" || missingEntry.Reason != "payload kind missing" {
		t.Fatalf("missing trace = %+v", missingEntry)
	}
}

func TestPersistentNames(t *testing.T) {
	top, err := Parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := top.PersistentNames()
	want := []string{"manager", "tm-mobile", "tm-platform"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("persistent names: got %v want %v", got, want)
	}
}

func TestLoadLayered_RepoOverrides(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.toml")
	repo := filepath.Join(t.TempDir(), "repo.toml")
	if err := os.WriteFile(tmpl, []byte(`
[instances.manager]
agent = "manager"
description = "from template"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repo, []byte(`
[instances.manager]
agent = "manager"
description = "from repo"

[instances.tm-extra]
agent = "ticket-manager"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	top, err := LoadLayered(tmpl, repo)
	if err != nil {
		t.Fatalf("LoadLayered: %v", err)
	}
	if top.Instances["manager"].Description != "from repo" {
		t.Errorf("repo override missing: %q", top.Instances["manager"].Description)
	}
	if top.Instances["tm-extra"] == nil {
		t.Error("repo-only entry missing")
	}
}

func TestLoadLayered_TeamCanReferenceMergedTopology(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.toml")
	repo := filepath.Join(t.TempDir(), "repo.toml")
	if err := os.WriteFile(tmpl, []byte(`
[instances.manager]
agent = "manager"

[instances.worker]
agent = "worker"
ephemeral = true

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repo, []byte(`
[teams.delivery]
instances = ["manager", "worker"]
pipelines = ["ticket_to_pr"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	top, err := LoadLayered(tmpl, repo)
	if err != nil {
		t.Fatalf("LoadLayered: %v", err)
	}
	team := top.Teams["delivery"]
	if team == nil {
		t.Fatal("team missing")
	}
	if !reflect.DeepEqual(team.Instances, []string{"manager", "worker"}) || !reflect.DeepEqual(team.Pipelines, []string{"ticket_to_pr"}) {
		t.Fatalf("team = %+v", team)
	}
}

func TestLoadLayered_LockRefsValidateAfterMerge(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.toml")
	repo := filepath.Join(t.TempDir(), "repo.toml")
	if err := os.WriteFile(tmpl, []byte(`
[instances.worker]
agent = "worker"
ephemeral = true
locks = ["build"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repo, []byte(`
[locks.build]
slots = 1
`), 0o644); err != nil {
		t.Fatal(err)
	}
	top, err := LoadLayered(tmpl, repo)
	if err != nil {
		t.Fatalf("LoadLayered: %v", err)
	}
	if top.Locks["build"] == nil || !reflect.DeepEqual(top.Instances["worker"].Locks, []string{"build"}) {
		t.Fatalf("topology = %+v", top)
	}
}

func TestLoadFromTeamDir_Absent(t *testing.T) {
	top, err := LoadFromTeamDir(t.TempDir())
	if err != nil {
		t.Fatalf("absent should not error: %v", err)
	}
	if top != nil {
		t.Errorf("absent should return nil topology, got %+v", top)
	}
}

func names(insts []*Instance) []string {
	out := make([]string, len(insts))
	for i, x := range insts {
		out[i] = x.Name
	}
	sort.Strings(out)
	return out
}

func findTraceEntry(t *testing.T, trace EventTrace, scope string) EventTraceEntry {
	t.Helper()
	for _, entry := range trace.Entries {
		if entry.Scope == scope {
			return entry
		}
	}
	t.Fatalf("trace entry %q missing: %+v", scope, trace.Entries)
	return EventTraceEntry{}
}
