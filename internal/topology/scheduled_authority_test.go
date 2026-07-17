package topology

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const scheduledAuthorityFixture = `
[schedules.observe]
every = "1h"

[schedules.observe.payload]
kind = "observe"

[instances.observer]
agent = "manager"
ephemeral = true
required_verbs = ["ticket.comment", "feedback.ls", "job.ls"]

[[instances.observer.triggers]]
event = "schedule"
match.name = "observe"
match.kind = "observe"

[teams.quality]
instances = ["observer"]
schedules = ["observe"]

[authority]
enforcement = "enforce"

[authority.instances.observer]
allow = ["job.ls"]

[authority.agents.manager]
allow = ["feedback.*"]

[authority.teams.quality]
allow = ["ticket.comment"]
`

const scheduledAuthorityNoRulesFixture = `
[schedules.observe]
every = "1h"

[instances.observer]
agent = "manager"
ephemeral = true
required_verbs = ["job.ls"]

[[instances.observer.triggers]]
event = "schedule"
match.name = "observe"

[teams.quality]
instances = ["observer"]
schedules = ["observe"]

[authority]
enforcement = "enforce"
`

func TestParseScheduledAuthorityUsesEffectiveComposedGrants(t *testing.T) {
	top, err := Parse([]byte(scheduledAuthorityFixture))
	if err != nil {
		t.Fatalf("Parse corrected scheduled authority: %v", err)
	}
	inst := top.Instances["observer"]
	if inst == nil {
		t.Fatal("observer instance is missing")
	}
	wantRequired := []string{"feedback.ls", "job.ls", "ticket.comment"}
	if !reflect.DeepEqual(inst.RequiredVerbs, wantRequired) {
		t.Fatalf("required verbs = %v, want %v", inst.RequiredVerbs, wantRequired)
	}

	for _, tc := range []struct {
		verb   string
		source string
	}{
		{verb: "feedback.ls", source: "authority.agents.manager"},
		{verb: "job.ls", source: "authority.instances.observer"},
		{verb: "ticket.comment", source: "authority.teams.quality"},
	} {
		eval := top.Authority.Evaluate(AuthorityDecision{
			Instance: inst.Name,
			Agent:    inst.Agent,
			Team:     top.TeamForInstance(inst.Name),
			Verb:     tc.verb,
		})
		if !eval.Allowed || !slices.Contains(eval.Sources, tc.source) {
			t.Fatalf("effective authority for %s = %+v, want allowed via %s", tc.verb, eval, tc.source)
		}
	}

	allow := top.AuthorityAllowlistForInstance(inst.Name, inst.Agent)
	for _, want := range []string{"feedback.*", "job.ls", "ticket.comment"} {
		if !slices.Contains(allow, want) {
			t.Fatalf("runtime allowlist = %v, want composed grant %q", allow, want)
		}
	}
}

func TestParseScheduledAuthorityRejectsDeficientEffectiveGrant(t *testing.T) {
	deficient := strings.Replace(scheduledAuthorityFixture, `allow = ["job.ls"]`, `allow = ["daemon.status"]`, 1)
	_, err := Parse([]byte(deficient))
	for _, want := range []string{
		`scheduled instance "observer"`,
		`required verb "job.ls"`,
		`authority.instances.observer`,
		`authority.agents.manager`,
		`authority.teams.quality`,
		`[authority.instances.observer].allow`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Parse error = %v, want %q", err, want)
		}
	}
}

func TestAuthorityEvaluateEnforcedWithoutRulesIsClosedWorld(t *testing.T) {
	authority := &Authority{Enforcement: AuthorityModeEnforce}
	eval := authority.Evaluate(AuthorityDecision{
		Instance: "observer",
		Agent:    "manager",
		Team:     "quality",
		Verb:     "job.ls",
	})
	if eval.Allowed {
		t.Fatalf("Evaluate = %+v, want closed-world denial without grant tables", eval)
	}
	if eval.Decision.Verb != "job.ls" || eval.SourceDescription() != "none" {
		t.Fatalf("Evaluate = %+v, want cleaned decision and no runtime sources", eval)
	}
}

func TestParseScheduledAuthorityRejectsEnforcedWithoutRules(t *testing.T) {
	_, err := Parse([]byte(scheduledAuthorityNoRulesFixture))
	for _, want := range []string{
		`scheduled instance "observer"`,
		`required verb "job.ls"`,
		`runtime sources: none`,
		`[authority.instances.observer].allow`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Parse error = %v, want %q", err, want)
		}
	}
}

func TestLoadLayeredRejectsDeficientScheduledAuthority(t *testing.T) {
	dir := t.TempDir()
	correctedPath := filepath.Join(dir, "corrected.toml")
	if err := os.WriteFile(correctedPath, []byte(scheduledAuthorityFixture), 0o644); err != nil {
		t.Fatalf("write corrected topology: %v", err)
	}
	if _, err := LoadLayered(correctedPath, ""); err != nil {
		t.Fatalf("LoadLayered corrected scheduled authority: %v", err)
	}

	deficient := strings.Replace(scheduledAuthorityFixture, `allow = ["job.ls"]`, `allow = ["daemon.status"]`, 1)
	deficientPath := filepath.Join(dir, "deficient.toml")
	if err := os.WriteFile(deficientPath, []byte(deficient), 0o644); err != nil {
		t.Fatalf("write deficient topology: %v", err)
	}
	_, err := LoadLayered(deficientPath, "")
	if err == nil || !strings.Contains(err.Error(), `scheduled instance "observer": required verb "job.ls"`) {
		t.Fatalf("LoadLayered error = %v, want scheduled authority rejection", err)
	}
}

func TestParseScheduledAuthorityRejectsDeadScopedGrant(t *testing.T) {
	deficient := strings.Replace(scheduledAuthorityFixture, `allow = ["job.ls"]`, `allow = ["job.ls:team"]`, 1)
	_, err := Parse([]byte(deficient))
	if err == nil || !strings.Contains(err.Error(), `required verb "job.ls"`) {
		t.Fatalf("Parse error = %v, want scoped job.ls rejection", err)
	}
}

func TestParseScheduledAuthorityAuditKeepsGrantMismatchNonBlocking(t *testing.T) {
	audit := strings.Replace(scheduledAuthorityFixture, `enforcement = "enforce"`, `enforcement = "audit"`, 1)
	audit = strings.Replace(audit, `allow = ["job.ls"]`, `allow = ["daemon.status"]`, 1)
	if _, err := Parse([]byte(audit)); err != nil {
		t.Fatalf("Parse audit topology: %v", err)
	}
}

func TestParseScheduledAuthorityRequiresScheduleTrigger(t *testing.T) {
	withoutTrigger := strings.Replace(scheduledAuthorityFixture, `
[[instances.observer.triggers]]
event = "schedule"
match.name = "observe"
match.kind = "observe"
`, "\n", 1)
	_, err := Parse([]byte(withoutTrigger))
	if err == nil || !strings.Contains(err.Error(), `required_verbs is only valid for an instance with a schedule trigger`) {
		t.Fatalf("Parse error = %v, want schedule-trigger requirement", err)
	}
}

func TestParseScheduledAuthorityRequiresCanonicalVerbs(t *testing.T) {
	for _, tc := range []struct {
		name string
		verb string
		want string
	}{
		{name: "wildcard", verb: "job.*", want: "wildcards are not valid"},
		{name: "scope", verb: "job.ls:team", want: "scope qualifiers are not valid"},
		{name: "empty", verb: "", want: "must be non-empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(scheduledAuthorityFixture,
				`required_verbs = ["ticket.comment", "feedback.ls", "job.ls"]`,
				`required_verbs = ["`+tc.verb+`"]`, 1)
			_, err := Parse([]byte(body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want %q", err, tc.want)
			}
		})
	}
}
