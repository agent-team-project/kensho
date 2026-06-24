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

[[instances.worker.triggers]]
event = "agent.dispatch"
match.target = "worker"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"
trigger.match.project = "Core"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"

[[pipelines.ticket_to_pr.steps]]
id = "review"
target = "manager"
after = ["implement"]
gate = "pr"
optional = true
timeout = "30m"
max_attempts = 2
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
	if len(p.Steps) != 2 || p.Steps[1].After[0] != "implement" || p.Steps[1].Gate != "pr" || !p.Steps[1].Optional || p.Steps[1].Timeout != 30*time.Minute || p.Steps[1].MaxAttempts != 2 {
		t.Fatalf("steps = %+v", p.Steps)
	}
	matched := top.ResolvePipelines("ticket.created", map[string]any{"project": "Core"})
	if len(matched) != 1 || matched[0].Name != "ticket_to_pr" {
		t.Fatalf("matched = %+v", matched)
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
