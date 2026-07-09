package job

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContractReadWriteRoundTrip(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	j, err := New("https://github.com/agent-team-project/kensho/issues/324", "worker", "implement GH324", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.DeliveryContract = "pr"
	j.Contract = &Contract{
		Schema:      ContractSchemaV1,
		WorkItem:    j.TicketURL,
		Deliverable: "pr",
		Trailer:     "Advances #324",
		Gates:       "smoke",
		Scope:       []string{"internal/daemon", "template/agents/reviewer"},
		Criteria: []ContractCriterion{
			{ID: "AC1", Text: "The durable job record persists a contract block.", Verify: "go test ./internal/job"},
			{ID: "AC2", Text: "Worker kickoff includes clause ids.", Verify: "review"},
		},
	}
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(teamDir, j.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Contract == nil {
		t.Fatalf("contract missing after round trip")
	}
	if got.Contract.Schema != ContractSchemaV1 ||
		got.Contract.WorkItem != j.TicketURL ||
		got.Contract.Deliverable != "pr" ||
		got.Contract.Trailer != "Advances #324" ||
		got.Contract.Gates != "smoke" {
		t.Fatalf("contract fields = %+v", got.Contract)
	}
	if len(got.Contract.Scope) != 2 || got.Contract.Scope[0] != "internal/daemon" || got.Contract.Scope[1] != "template/agents/reviewer" {
		t.Fatalf("scope = %+v", got.Contract.Scope)
	}
	if len(got.Contract.Criteria) != 2 || got.Contract.Criteria[0].ID != "AC1" || got.Contract.Criteria[0].Verify != "go test ./internal/job" {
		t.Fatalf("criteria = %+v", got.Contract.Criteria)
	}
}

func TestCompileContractExtractsCriteriaTrailerAndScope(t *testing.T) {
	j, err := New("GH324-agent-contracts", "platform-worker", "implement", time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.TicketURL = "https://github.com/agent-team-project/kensho/issues/324"
	j.Epic = "agent-team-project/kensho#324"
	j.DeliveryContract = "pr"
	kickoff := `GH324-agent-contracts

Required PR trailer: ` + "`Advances #324`" + `

Likely scope:

- internal/daemon job record / dispatch persistence
- template/agents/reviewer

## Contract

AC1. The durable job record can persist a contract block. (verify: go test ./internal/job)
AC2. Worker kickoff rendering includes a fixed contract section.
`
	contract := CompileContract(j, ContractCompileOptions{Text: kickoff})
	if contract == nil {
		t.Fatalf("CompileContract returned nil")
	}
	if contract.WorkItem != j.TicketURL || contract.Deliverable != "pr" || contract.Trailer != "Advances #324" || contract.Gates != "smoke" {
		t.Fatalf("contract = %+v", contract)
	}
	if len(contract.Scope) != 2 || contract.Scope[0] != "internal/daemon job record / dispatch persistence" {
		t.Fatalf("scope = %+v", contract.Scope)
	}
	if len(contract.Criteria) != 2 {
		t.Fatalf("criteria len=%d: %+v", len(contract.Criteria), contract.Criteria)
	}
	if contract.Criteria[0].ID != "AC1" || contract.Criteria[0].Verify != "go test ./internal/job" {
		t.Fatalf("first criterion = %+v", contract.Criteria[0])
	}
	if contract.Criteria[1].ID != "AC2" || contract.Criteria[1].Verify != "review" {
		t.Fatalf("second criterion = %+v", contract.Criteria[1])
	}
}

func TestCompileContractFallbackRecordsDeliverableTrailerFloor(t *testing.T) {
	j, err := New("https://github.com/agent-team-project/kensho/issues/42", "worker", "mechanical fix", time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Epic = EpicFromInputs("", j.TicketURL, "")
	j.DeliveryContract = "ticket_to_pr"
	contract := CompileContract(j, ContractCompileOptions{Text: "mechanical fix"})
	if contract == nil {
		t.Fatalf("CompileContract returned nil")
	}
	if contract.Deliverable != "pr" || contract.Trailer != "Closes #42" {
		t.Fatalf("contract floor = %+v", contract)
	}
	if len(contract.Criteria) != 0 {
		t.Fatalf("fallback criteria = %+v, want none", contract.Criteria)
	}
}

func TestCompileContractExplicitEpicRequiresAdvances(t *testing.T) {
	j, err := New("https://github.com/agent-team-project/kensho/issues/324", "worker", "epic slice", time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Epic = EpicFromInputs("agent-team-project/kensho#324", j.TicketURL, "")
	j.DeliveryContract = "ticket_to_pr"
	contract := CompileContract(j, ContractCompileOptions{
		Text:         "epic slice",
		ExplicitEpic: "agent-team-project/kensho#324",
	})
	if contract == nil {
		t.Fatalf("CompileContract returned nil")
	}
	if contract.Trailer != "Advances #324" {
		t.Fatalf("contract trailer = %q, want Advances #324", contract.Trailer)
	}
}

func TestCompileContractIgnoresNumberedProseOutsideCriteriaHeading(t *testing.T) {
	j, err := New("https://github.com/agent-team-project/kensho/issues/42", "worker", "mechanical fix", time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.DeliveryContract = "ticket_to_pr"
	contract := CompileContract(j, ContractCompileOptions{Text: `Implementation notes:

1. Touch internal/daemon.
2. Run go test ./...
`})
	if contract == nil {
		t.Fatalf("CompileContract returned nil")
	}
	if len(contract.Criteria) != 0 {
		t.Fatalf("criteria = %+v, want no criteria from generic numbered prose", contract.Criteria)
	}
}

func TestCompileContractIgnoresTicketPrefixedKickoffOutsideCriteriaHeading(t *testing.T) {
	j, err := New("SQU-14", "worker", "mechanical fix", time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.DeliveryContract = "ticket_to_pr"
	contract := CompileContract(j, ContractCompileOptions{Text: "SQU-14: Fix the durable dispatcher."})
	if contract == nil {
		t.Fatalf("CompileContract returned nil")
	}
	if len(contract.Criteria) != 0 {
		t.Fatalf("criteria = %+v, want no criteria from normal ticket-prefix kickoff", contract.Criteria)
	}
}

func TestCompileContractParsesIntentionalCriteriaSources(t *testing.T) {
	j, err := New("https://github.com/agent-team-project/kensho/issues/42", "worker", "mechanical fix", time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.DeliveryContract = "ticket_to_pr"
	contract := CompileContract(j, ContractCompileOptions{Text: `Implementation notes:

AC7. The dispatch contract survives durable job round-trip. (verify: go test ./internal/job)

## Acceptance criteria

1. Worker kickoff rendering includes clause ids.
`})
	if contract == nil {
		t.Fatalf("CompileContract returned nil")
	}
	if len(contract.Criteria) != 2 {
		t.Fatalf("criteria len=%d: %+v", len(contract.Criteria), contract.Criteria)
	}
	if contract.Criteria[0].ID != "AC1" || contract.Criteria[0].Text != "Worker kickoff rendering includes clause ids." {
		t.Fatalf("numbered criterion = %+v", contract.Criteria[0])
	}
	if contract.Criteria[1].ID != "AC7" || contract.Criteria[1].Verify != "go test ./internal/job" {
		t.Fatalf("stable criterion = %+v", contract.Criteria[1])
	}
}

func TestRenderKickoffWithContractReplacesExistingSection(t *testing.T) {
	contract := &Contract{
		Schema:      ContractSchemaV1,
		WorkItem:    "https://github.com/agent-team-project/kensho/issues/324",
		Deliverable: "pr",
		Trailer:     "Advances #324",
		Gates:       "smoke",
		Criteria:    []ContractCriterion{{ID: "AC1", Text: "Worker kickoff includes clause ids.", Verify: "review"}},
	}
	got := RenderKickoffWithContract("Intro\n\n## Contract\n\nold prose\n\n## Notes\n\nkeep this", contract)
	for _, want := range []string{"## Contract", "Required PR trailer: Advances #324", "- AC1: Worker kickoff includes clause ids.", "## Notes", "keep this"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered kickoff missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old prose") {
		t.Fatalf("rendered kickoff kept stale contract section:\n%s", got)
	}
}

func TestRenderKickoffWithContractReplacesNestedExistingSection(t *testing.T) {
	contract := &Contract{
		Schema:      ContractSchemaV1,
		WorkItem:    "SQU-999",
		Deliverable: "pr",
		Trailer:     "Closes #999",
		Gates:       "smoke",
		Criteria:    []ContractCriterion{{ID: "AC1", Text: "Worker kickoff rendering includes a fixed contract section.", Verify: "review"}},
	}
	got := RenderKickoffWithContract("Intro\n\n## Contract\n\n### Acceptance criteria\n\nAC9. stale old criterion\n\n## Notes\n\nkeep", contract)
	for _, want := range []string{"Intro", "## Contract", "- AC1: Worker kickoff rendering includes a fixed contract section.", "## Notes", "keep"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered kickoff missing %q:\n%s", want, got)
		}
	}
	for _, stale := range []string{"### Acceptance criteria", "AC9. stale old criterion"} {
		if strings.Contains(got, stale) {
			t.Fatalf("rendered kickoff kept stale nested contract content %q:\n%s", stale, got)
		}
	}
}
