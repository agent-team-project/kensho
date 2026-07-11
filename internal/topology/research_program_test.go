package topology

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/job"
)

func loadSelfDogfoodResearchTopology(t *testing.T) *Topology {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", ".agent_team", "instances.toml"))
	if err != nil {
		t.Fatalf("read self-dogfood topology: %v", err)
	}
	top, err := Parse(body)
	if err != nil {
		t.Fatalf("parse self-dogfood topology: %v", err)
	}
	return top
}

func TestResearchProgramTopologyContract(t *testing.T) {
	top := loadSelfDogfoodResearchTopology(t)
	for _, name := range []string{"research-manager", "research-worker", "research-verifier", "research-reviewer", "research-auditor"} {
		if top.Instances[name] == nil {
			t.Fatalf("research instance %q missing", name)
		}
	}
	if top.Instances["research-manager"].Ephemeral || top.Instances["research-auditor"].Ephemeral {
		t.Fatal("research manager and auditor must be persistent")
	}
	for _, name := range []string{"research-worker", "research-verifier", "research-reviewer"} {
		if !top.Instances[name].Ephemeral {
			t.Fatalf("pipeline role %q must be ephemeral", name)
		}
	}
	if schedule := top.Schedules["research-reconcile"]; schedule == nil || schedule.Every.String() != "2h0m0s" || !schedule.RunOnStart {
		t.Fatalf("research reconcile schedule = %+v, want 2h and run_on_start", schedule)
	}
	if schedule := top.Schedules["research-evidence-audit"]; schedule == nil || schedule.Every.String() != "84h0m0s" {
		t.Fatalf("research evidence audit schedule = %+v, want 84h", schedule)
	}
	team := top.FindTeam("research")
	if team == nil || !reflect.DeepEqual(team.Pipelines, []string{"research_study", "research_slice"}) {
		t.Fatalf("research team = %+v, want both research pipelines", team)
	}
	budget := top.FindBudget("research")
	if budget == nil || budget.TokensPerDay != 100_000_000_000 || budget.JobsInFlight != 16 {
		t.Fatalf("research budget = %+v, want 100B/day and 16 in flight", budget)
	}
	if top.Authority == nil || !reflect.DeepEqual(top.Authority.Instances["research-auditor"].Allow, []string{"read"}) {
		t.Fatalf("research auditor authority = %+v, want read-only", top.Authority)
	}

	assertResearchPipeline(t, top.Pipelines["research_study"],
		[]string{"preregister", "verify", "review", "activate"}, "activate")
	assertResearchPipeline(t, top.Pipelines["research_slice"],
		[]string{"implement", "verify", "review", "integrate"}, "integrate")
}

func TestResearchProgramTemplateMatchesSelfDogfood(t *testing.T) {
	fragment, err := os.ReadFile(filepath.Join("..", "..", "template", "topology", "instances.toml.tmpl.d", "85_full_research_program.toml.tmpl"))
	if err != nil {
		t.Fatalf("read research template fragment: %v", err)
	}
	selfDogfood, err := os.ReadFile(filepath.Join("..", "..", ".agent_team", "instances.toml"))
	if err != nil {
		t.Fatalf("read self-dogfood topology: %v", err)
	}
	const start = "# Standing empirical software-research organization."
	body := string(selfDogfood)
	startAt := strings.Index(body, start)
	endAt := strings.Index(body, "\n[teams.pr]")
	if startAt < 0 || endAt < 0 || endAt <= startAt {
		t.Fatalf("could not isolate self-dogfood research block")
	}
	if got, want := strings.TrimSpace(body[startAt:endAt]), strings.TrimSpace(string(fragment)); got != want {
		t.Fatalf("bundled and self-dogfood research topology differ")
	}
}

func TestResearchPipelinesClassifyBaseBrokenAsInfra(t *testing.T) {
	top := loadSelfDogfoodResearchTopology(t)
	for _, name := range []string{"research_study", "research_slice"} {
		pipeline := top.Pipelines[name]
		if pipeline == nil {
			t.Fatalf("pipeline %q missing", name)
		}
		matchers, err := job.CompileGateSignatureMatchers(pipeline.InfraSignatures)
		if err != nil {
			t.Fatalf("compile %s infra signatures: %v", name, err)
		}
		classification := job.ClassifyGateRecord(matchers, job.GateRecord{
			Name:      "go-test",
			Status:    job.GateStatusFail,
			Signature: "base-broken",
		})
		if classification.Class != job.GateClassInfra || classification.MatchedSignature != "base_broken" {
			t.Fatalf("%s base-broken classification = %+v, want class=infra matched_signature=base_broken", name, classification)
		}
	}
}

func TestResearchStudyVerifierUsesDeclaredReportGates(t *testing.T) {
	top := loadSelfDogfoodResearchTopology(t)
	pipeline := top.Pipelines["research_study"]
	if pipeline == nil {
		t.Fatal("research_study pipeline missing")
	}
	var verify *PipelineStep
	for _, step := range pipeline.Steps {
		if step.ID == "verify" {
			verify = step
			break
		}
	}
	if verify == nil {
		t.Fatal("research_study verify step missing")
	}
	gates := declaredVerifierGateLines(verify.Instructions)
	if len(gates) != 3 {
		t.Fatalf("declared report gates = %v, want three explicit gates", gates)
	}
	wantNames := []string{"report-artifact", "report-verdict-separation", "report-preregistration-contract"}
	for i, want := range wantNames {
		if !strings.HasPrefix(gates[i], want+" :: ") {
			t.Fatalf("gate[%d] = %q, want %q", i, gates[i], want)
		}
	}
	joined := strings.Join(gates, "\n")
	for _, want := range []string{
		"AGENT_TEAM_REPORT_PATH",
		"report.sha256",
		"product verdict",
		"Kensho|process",
		"hypothes",
		"falsif",
		"hard gate",
		"requirements[- ]graph",
		"activate|revise|stop",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("declared report gates missing %q:\n%s", want, joined)
		}
	}
	for _, fallback := range []string{"gofmt-check", "go-vet", "go-test"} {
		if strings.Contains(joined, fallback) {
			t.Fatalf("research verifier includes repository fallback gate %q:\n%s", fallback, joined)
		}
	}
}

func assertResearchPipeline(t *testing.T, pipeline *Pipeline, wantIDs []string, manualID string) {
	t.Helper()
	if pipeline == nil {
		t.Fatalf("research pipeline missing; want steps %v", wantIDs)
	}
	gotIDs := make([]string, 0, len(pipeline.Steps))
	for _, step := range pipeline.Steps {
		gotIDs = append(gotIDs, step.ID)
		if step.ID == manualID && step.Gate != "manual" {
			t.Fatalf("pipeline %s step %s gate = %q, want manual", pipeline.Name, manualID, step.Gate)
		}
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("pipeline %s steps = %v, want %v", pipeline.Name, gotIDs, wantIDs)
	}
}

func declaredVerifierGateLines(instructions string) []string {
	lines := []string{}
	inBlock := false
	for _, raw := range strings.Split(instructions, "\n") {
		line := strings.TrimSpace(raw)
		if !inBlock && line == "```agent-team-verify-gates" {
			inBlock = true
			continue
		}
		if inBlock && strings.HasPrefix(line, "```") {
			break
		}
		if inBlock && line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}
