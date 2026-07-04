package topology

import (
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
event         = "ticket_webhook"
match.project = "Platform"
match.event   = ["created", "updated"]

[instances.tm-mobile]
agent     = "ticket-manager"
ephemeral = false

[instances.tm-mobile.config.linear]
project_id = "50b6cd55"

[[instances.tm-mobile.triggers]]
event         = "ticket_webhook"
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
	if len(tmPlat.Triggers) != 1 {
		t.Fatalf("tm-platform triggers: %d", len(tmPlat.Triggers))
	}
	trig := tmPlat.Triggers[0]
	if trig.Event != "ticket_webhook" {
		t.Errorf("trigger event: %s", trig.Event)
	}
	if trig.Match["project"].Single != "Platform" {
		t.Errorf("project match: %+v", trig.Match["project"])
	}
	want := []string{"created", "updated"}
	if !reflect.DeepEqual(trig.Match["event"].List, want) {
		t.Errorf("event list match: got %v want %v", trig.Match["event"].List, want)
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
enforce = false

[authority.agents.worker]
allow = ["inbox.*", "job.gate.set"]

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
	if top.Authority == nil || top.Authority.Enforce {
		t.Fatalf("authority = %+v", top.Authority)
	}
	if !top.Authority.Allows("worker", "", "inbox.check") {
		t.Fatalf("worker inbox.check should be allowed by wildcard")
	}
	if !top.Authority.Allows("reviewer", "platform", "job.merge") {
		t.Fatalf("platform job.merge should be allowed by team wildcard")
	}
	if top.Authority.Allows("worker", "", "job.merge") {
		t.Fatalf("worker job.merge should not be allowed")
	}
	if got := ScopedResourceName("build", ScopeTeam, "platform", "squ-92"); got != "team.platform.build" {
		t.Fatalf("ScopedResourceName = %q", got)
	}
	if got, err := ScopedChannelName("#supervisor", ScopeTeam, "platform", "squ-92"); err != nil || got != "#team-platform-supervisor" {
		t.Fatalf("ScopedChannelName = %q, %v", got, err)
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
after = ["implement"]
gate = "pr"
optional = true
timeout = "30m"
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
	if len(p.Steps) != 2 || p.Steps[1].Label != "Manager review" || p.Steps[1].Description != "Review implementation and prepare PR handoff." || p.Steps[1].Instructions != "Review the worker branch and decide whether PR follow-up is ready." || p.Steps[1].Workspace != "repo" || p.Steps[1].Runtime != "codex" || p.Steps[1].RuntimeBin != "codex-dev" || p.Steps[1].After[0] != "implement" || p.Steps[1].Gate != "pr" || !p.Steps[1].Optional || p.Steps[1].Timeout != 30*time.Minute || p.Steps[1].MaxAttempts != 2 || !p.Steps[1].RetryOnCrash {
		t.Fatalf("steps = %+v", p.Steps)
	}
	if worker := top.Instances["worker"]; worker == nil || worker.ReapWorktree != "on_close" {
		t.Fatalf("worker ReapWorktree = %+v, want on_close", worker)
	}
	matched := top.ResolvePipelines("ticket.created", map[string]any{"project": "Core"})
	if len(matched) != 1 || matched[0].Name != "ticket_to_pr" {
		t.Fatalf("matched = %+v", matched)
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
	if !strings.Contains(err.Error(), "runtime must be claude or codex") {
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
event         = "ticket_webhook"
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

func TestResolve_TicketWebhookRouting(t *testing.T) {
	top, err := Parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	matched := top.Resolve("ticket_webhook", map[string]any{
		"project": "Platform",
		"event":   "created",
	})
	if len(matched) != 1 || matched[0].Name != "tm-platform" {
		t.Fatalf("expected only tm-platform, got %v", names(matched))
	}
	matched = top.Resolve("ticket_webhook", map[string]any{
		"project": "Mobile",
	})
	if len(matched) != 1 || matched[0].Name != "tm-mobile" {
		t.Fatalf("expected only tm-mobile, got %v", names(matched))
	}
	matched = top.Resolve("ticket_webhook", map[string]any{
		"project": "Platform",
		"event":   "deleted",
	})
	if len(matched) != 0 {
		t.Errorf("event=deleted should not match (list miss): %v", names(matched))
	}
	matched = top.Resolve("agent.dispatch", map[string]any{"target": "worker"})
	if len(matched) != 1 || matched[0].Name != "worker" {
		t.Errorf("worker dispatch: %v", names(matched))
	}
	// Missing payload key → no match.
	matched = top.Resolve("ticket_webhook", map[string]any{})
	if len(matched) != 0 {
		t.Errorf("empty payload should match nothing for keyed triggers: %v", names(matched))
	}
}

func TestResolve_TicketWebhookAliasMatchesNormalizedIntakeEvents(t *testing.T) {
	top, err := Parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	matched := top.Resolve("ticket.created", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 1 || matched[0].Name != "tm-platform" {
		t.Fatalf("expected ticket.created to match tm-platform legacy trigger, got %v", names(matched))
	}
	matched = top.Resolve("ticket.updated", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 1 || matched[0].Name != "tm-platform" {
		t.Fatalf("expected ticket.updated to match tm-platform legacy trigger, got %v", names(matched))
	}
	matched = top.Resolve("ticket.commented", map[string]any{
		"project": "Platform",
	})
	if len(matched) != 0 {
		t.Fatalf("ticket.commented should miss legacy event filter, got %v", names(matched))
	}
	matched = top.Resolve("ticket.created", map[string]any{
		"project": "Mobile",
	})
	if len(matched) != 1 || matched[0].Name != "tm-mobile" {
		t.Fatalf("expected ticket.created to match tm-mobile legacy trigger, got %v", names(matched))
	}
}

func TestResolve_PRWebhookAliasMatchesNormalizedIntakeEvents(t *testing.T) {
	top, err := Parse([]byte(`
[instances.pr-reviewer]
agent = "manager"

[[instances.pr-reviewer.triggers]]
event = "pr_webhook"
match.repository = "jamesaud/agent-team"
match.event = ["opened", "merged"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	matched := top.Resolve("pr.opened", map[string]any{
		"repository": "jamesaud/agent-team",
	})
	if len(matched) != 1 || matched[0].Name != "pr-reviewer" {
		t.Fatalf("expected pr.opened to match legacy trigger, got %v", names(matched))
	}
	matched = top.Resolve("pr.merged", map[string]any{
		"repository": "jamesaud/agent-team",
	})
	if len(matched) != 1 || matched[0].Name != "pr-reviewer" {
		t.Fatalf("expected pr.merged to match legacy trigger, got %v", names(matched))
	}
	matched = top.Resolve("pr.closed", map[string]any{
		"repository": "jamesaud/agent-team",
	})
	if len(matched) != 0 {
		t.Fatalf("pr.closed should miss legacy event filter, got %v", names(matched))
	}
}

func TestResolvePipelines_WebhookAliasMatchesNormalizedIntakeEvents(t *testing.T) {
	top, err := Parse([]byte(`
[pipelines.ticket_to_pr]
trigger.event = "ticket_webhook"
trigger.match.project = "Core"
trigger.match.event = "created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	matched := top.ResolvePipelines("ticket.created", map[string]any{"project": "Core"})
	if len(matched) != 1 || matched[0].Name != "ticket_to_pr" {
		t.Fatalf("expected normalized ticket event to match legacy pipeline trigger, got %+v", matched)
	}
	matched = top.ResolvePipelines("ticket.updated", map[string]any{"project": "Core"})
	if len(matched) != 0 {
		t.Fatalf("ticket.updated should miss legacy pipeline event filter, got %+v", matched)
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

func TestTrace_ExplainsWebhookAliasPayloadPredicates(t *testing.T) {
	top, err := Parse([]byte(`
[instances.tm]
agent = "ticket-manager"

[[instances.tm.triggers]]
event = "ticket_webhook"
match.event = ["created", "updated"]
match.project = "Core"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	deleted := top.Trace("ticket.deleted", map[string]any{"project": "Core"})
	deletedEntry := findTraceEntry(t, deleted, "instances.tm")
	if deletedEntry.Matched || deletedEntry.Matcher != "match.event=[created, updated]" || deletedEntry.Reason != "payload event=deleted not in [created, updated]" {
		t.Fatalf("deleted trace = %+v", deletedEntry)
	}
	missing := top.Trace("ticket.created", map[string]any{})
	missingEntry := findTraceEntry(t, missing, "instances.tm")
	if missingEntry.Matched || missingEntry.Matcher != "match.project=Core" || missingEntry.Reason != "payload project missing" {
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
