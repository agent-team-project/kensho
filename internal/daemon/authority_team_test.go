package daemon

import (
	"strings"
	"testing"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestAuditAuthorityTeamScopeUsesTargetJobOrigin(t *testing.T) {
	top, err := topology.Parse([]byte(`
[instances.frontend-manager]
agent = "manager"

[teams.frontend]
instances = ["frontend-manager"]

[authority]
enforcement = "enforce"

[authority.instances.frontend-manager]
allow = ["job.bounce:team"]
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	teamDir := t.TempDir()
	writeAuthorityTargetJob(t, teamDir, "GH-403", "frontend")
	writeAuthorityTargetJob(t, teamDir, "GH-398", "research")
	actor := origin.Envelope{Instance: "frontend-manager", Agent: "manager", Team: "frontend"}

	if err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     actor,
		Verb:      "job.bounce",
		TargetJob: "gh-403",
		Resource:  "job:gh-403",
	}); err != nil {
		t.Fatalf("team-owned job denied: %v", err)
	}

	err = AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     actor,
		Verb:      "job.bounce",
		TargetJob: "gh-398",
		Resource:  "job:gh-398",
	})
	if err == nil || !strings.Contains(err.Error(), "authority violation") {
		t.Fatalf("cross-team job error = %v, want authority violation", err)
	}
}

func TestAuditAuthorityPersistentManagerOwnScopeRemainsUnsatisfied(t *testing.T) {
	top, err := topology.Parse([]byte(`
[instances.frontend-manager]
agent = "manager"

[teams.frontend]
instances = ["frontend-manager"]

[authority]
enforcement = "enforce"

[authority.instances.frontend-manager]
allow = ["job.bounce:own"]
`))
	if err != nil {
		t.Fatalf("parse topology: %v", err)
	}
	teamDir := t.TempDir()
	writeAuthorityTargetJob(t, teamDir, "GH-403", "frontend")
	err = AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     origin.Envelope{Instance: "frontend-manager", Agent: "manager", Team: "frontend"},
		Verb:      "job.bounce",
		TargetJob: "gh-403",
		Resource:  "job:gh-403",
	})
	if err == nil || !strings.Contains(err.Error(), "authority violation") {
		t.Fatalf("persistent manager :own error = %v, want authority violation", err)
	}
}

func TestGH403ReviewAuditModeRemainsNonBlocking(t *testing.T) {
	body := `
[instances.frontend-manager]
agent = "manager"

[[instances.frontend-manager.triggers]]
event = "job.step_completed"
match.target = "frontend-manager"

[[instances.frontend-manager.triggers]]
event = "job.completed"
match.pipeline = "managed"

[instances.worker]
agent = "worker"
ephemeral = true

[pipelines.managed]
trigger.event = "work.ready"
reap_worktree = "on_merge"

[[pipelines.managed.steps]]
id = "implement"
target = "worker"

[[pipelines.managed.steps]]
id = "decide"
target = "frontend-manager"
after = ["implement"]
gate = "manual"

[teams.frontend]
instances = ["frontend-manager", "worker"]
pipelines = ["managed"]

[authority]
enforcement = "audit"

[authority.instances.frontend-manager]
allow = ["read"]
`
	top, err := topology.Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse audit topology: %v", err)
	}
	teamDir := t.TempDir()
	writeAuthorityTargetJob(t, teamDir, "GH-403", "frontend")
	if err := AuditAuthority(AuthorityAuditOptions{
		TeamDir:   teamDir,
		Topology:  top,
		Actor:     origin.Envelope{Instance: "frontend-manager", Agent: "manager", Team: "frontend"},
		Verb:      "job.bounce",
		TargetJob: "gh-403",
		Resource:  "job:gh-403",
	}); err != nil {
		t.Fatalf("audit mode blocked denied manager action: %v", err)
	}
	events, err := jobstore.ListEvents(teamDir, "gh-403")
	if err != nil {
		t.Fatalf("list authority events: %v", err)
	}
	if len(events) != 1 || events[0].Type != authorityViolationAction || events[0].Data["verb"] != "job.bounce" || events[0].Data["target_team"] != "frontend" {
		t.Fatalf("audit events = %+v, want one job.bounce authority_violation for frontend", events)
	}

	enforced := strings.Replace(body, `enforcement = "audit"`, `enforcement = "enforce"`, 1)
	_, err = topology.Parse([]byte(enforced))
	if err == nil || !strings.Contains(err.Error(), `pipeline "managed"`) || !strings.Contains(err.Error(), `"job.bounce:team"`) {
		t.Fatalf("parse enforced topology error = %v, want missing job.bounce:team satisfiability error", err)
	}
}

func writeAuthorityTargetJob(t *testing.T, teamDir, ticket, team string) {
	t.Helper()
	j, err := jobstore.New(ticket, "worker", "authority scope test", time.Now().UTC())
	if err != nil {
		t.Fatalf("new job: %v", err)
	}
	j.Origin = origin.Envelope{Team: team, Job: j.ID}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
}
