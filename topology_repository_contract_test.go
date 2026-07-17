package agentteam

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/job"
	templatecfg "github.com/agent-team-project/agent-team/internal/template"
	"github.com/agent-team-project/agent-team/internal/topology"
)

const repositoryFrontendEvidenceContract = "Every metric the issue or SPEC declares for an evidence artifact must actually be measured; if a metric cannot be measured on this host, fail the gate loudly with the reason — a sentinel value (-1, null, or 'skipped') recorded in evidence is a failing gate, never a pass."

func TestTopologyRepositoryContractPackageIsolation(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("internal", "topology", "*_test.go"))
	if err != nil {
		t.Fatalf("glob topology tests: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no internal topology tests found")
	}

	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if topologyPackageTestReadsMutableDeployment(string(body)) {
			t.Errorf("%s reads mutable .agent_team/instances.toml; use a bounded fixture or a repository-contract test", path)
		}
	}
}

func TestTopologyRepositoryContractPackageIsolationGuardSensitivity(t *testing.T) {
	for _, source := range []string{
		`os.ReadFile(filepath.Join("..", "..", ".agent_team", "instances.toml"))`,
		`const teamDir = ".agent_team"; const deployment = "instances.toml"; os.ReadFile(filepath.Join(teamDir, deployment))`,
	} {
		if !topologyPackageTestReadsMutableDeployment(source) {
			t.Fatalf("guard accepted mutable-deployment counterfeit: %s", source)
		}
	}
	if topologyPackageTestReadsMutableDeployment(`os.ReadFile(filepath.Join("testdata", "instances.toml"))`) {
		t.Fatal("guard rejected a bounded testdata fixture")
	}
}

func TestTopologyRepositoryContractResearchProgram(t *testing.T) {
	for _, fixture := range topologyRepositoryFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			top := fixture.top
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
			if schedule := top.Schedules["research-reconcile"]; schedule == nil || schedule.Every != 2*time.Hour || !schedule.RunOnStart {
				t.Fatalf("research reconcile schedule = %+v, want 2h and run_on_start", schedule)
			}
			if schedule := top.Schedules["research-evidence-audit"]; schedule == nil || schedule.Every != 84*time.Hour {
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
			owner := top.Instances["research-manager"]
			for _, verb := range []string{"job.approve", "job.reject"} {
				eval := top.Authority.Evaluate(topology.AuthorityDecision{
					Instance: owner.Name, Agent: owner.Agent, Team: top.TeamForInstance(owner.Name),
					Verb: verb, TargetTeam: "research",
				})
				if !eval.Allowed {
					t.Fatalf("research manager denied %s: %+v", verb, eval)
				}
			}

			assertRepositoryResearchPipeline(t, top.Pipelines["research_study"], []string{"preregister", "verify", "review", "activate"}, "activate")
			assertRepositoryResearchPipeline(t, top.Pipelines["research_slice"], []string{"implement", "verify", "review", "integrate"}, "integrate")

			for _, name := range []string{"research_study", "research_slice"} {
				pipeline := top.Pipelines[name]
				matchers, err := job.CompileGateSignatureMatchers(pipeline.InfraSignatures)
				if err != nil {
					t.Fatalf("compile %s infra signatures: %v", name, err)
				}
				classification := job.ClassifyGateRecord(matchers, job.GateRecord{Name: "go-test", Status: job.GateStatusFail, Signature: "base-broken"})
				if classification.Class != job.GateClassInfra || classification.MatchedSignature != "base_broken" {
					t.Fatalf("%s base-broken classification = %+v", name, classification)
				}
			}

			verify := repositoryPipelineStep(top.Pipelines["research_study"], "verify")
			if verify == nil {
				t.Fatal("research_study verify step missing")
			}
			gates := repositoryDeclaredVerifierGateLines(verify.Instructions)
			if len(gates) != 3 {
				t.Fatalf("declared report gates = %v, want three explicit gates", gates)
			}
			for i, want := range []string{"report-artifact", "report-verdict-separation", "report-preregistration-contract"} {
				if !strings.HasPrefix(gates[i], want+" :: ") {
					t.Fatalf("gate[%d] = %q, want %q", i, gates[i], want)
				}
			}
			joined := strings.Join(gates, "\n")
			for _, want := range []string{
				"AGENT_TEAM_REPORT_PATH", "report.sha256", "product verdict", "Kensho|process",
				"hypothes", "falsif", "hard gate", "requirements[- ]graph", "activate|revise|stop",
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
		})
	}
}

func TestTopologyRepositoryContractResearchTemplateMatchesSelfDogfood(t *testing.T) {
	fragment := readTopologyRepositoryFile(t, filepath.Join("template", "topology", "instances.toml.tmpl.d", "85_full_research_program.toml.tmpl"))
	selfDogfood := readTopologyRepositoryFile(t, filepath.Join(".agent_team", "instances.toml"))
	const start = "# Standing empirical software-research organization."
	startAt := strings.Index(string(selfDogfood), start)
	endAt := strings.Index(string(selfDogfood), "\n# Dedicated terminal-interface unit.")
	if startAt < 0 || endAt < 0 || endAt <= startAt {
		t.Fatal("could not isolate self-dogfood research block")
	}
	const guardSensitivityInstructions = `
For every guard test added or strengthened, prove it fails against the
pre-change behavior. Identify the failing test and assertion and record the
evidence before handoff. For a forbidden behavior class, also prove the obvious
counterfeit mutation fails, using production-shaped fixtures.
`
	templateWithoutGuardDuty := strings.Replace(string(fragment), guardSensitivityInstructions, "", 1)
	if templateWithoutGuardDuty == string(fragment) {
		t.Fatal("research template fragment missing guard-sensitivity instructions")
	}
	if got, want := strings.TrimSpace(string(selfDogfood)[startAt:endAt]), strings.TrimSpace(templateWithoutGuardDuty); got != want {
		t.Fatal("bundled and self-dogfood research topology differ")
	}
}

func TestTopologyRepositoryContractFrontendAndScheduledAuthority(t *testing.T) {
	wantRequired := map[string][]string{
		"feedback-triage": {
			"daemon.status", "feedback.ls", "feedback.resolve", "feedback.show", "instance.brief",
			"job.events", "job.explain", "job.ls", "job.show", "job.triage", "ticket.comment", "ticket.create",
		},
		"debt-auditor": {"feedback.submit", "ticket.comment", "ticket.create"},
		"harness-reviewer": {
			"daemon.status", "feedback.ls", "instance.brief", "job.events", "job.explain",
			"job.ls", "job.show", "job.triage", "ticket.comment", "ticket.create",
		},
		"org-review": {
			"budget.status", "daemon.status", "feedback.ls", "feedback.show", "instance.brief", "job.ls", "job.show",
			"job.triage", "outcomes.report", "queue.ls", "schedule.ls", "team.ps", "ticket.comment", "ticket.create",
		},
		"sentinel":         {"daemon.status", "feedback.submit", "instance.brief"},
		"product-verifier": {"daemon.status", "feedback.submit", "instance.brief", "job.ls", "ps", "topology.show"},
	}

	for _, fixture := range topologyRepositoryFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			top := fixture.top
			pipeline := top.Pipelines["frontend_ticket_to_pr"]
			if pipeline == nil || pipeline.ReapWorktree != "on_merge" {
				t.Fatalf("frontend pipeline = %+v", pipeline)
			}
			for _, stepID := range []string{"implement", "verify"} {
				step := repositoryPipelineStep(pipeline, stepID)
				if step == nil {
					t.Fatalf("frontend %s step is missing", stepID)
				}
				normalized := strings.Join(strings.Fields(step.Instructions), " ")
				if !strings.Contains(normalized, repositoryFrontendEvidenceContract) {
					t.Fatalf("frontend %s step is missing the declared-evidence contract", stepID)
				}

				original := step.Instructions
				step.Instructions = strings.Replace(normalized, repositoryFrontendEvidenceContract, "", 1)
				if missing := repositoryMissingFrontendEvidenceContract(top); len(missing) != 1 || missing[0] != stepID {
					t.Fatalf("deleting %s contract reports missing steps %v, want [%s]", stepID, missing, stepID)
				}
				step.Instructions = strings.Replace(normalized, "never a pass.", "may still pass.", 1)
				if step.Instructions == normalized {
					t.Fatal("fail-open counterfeit mutation did not change instructions")
				}
				if missing := repositoryMissingFrontendEvidenceContract(top); len(missing) != 1 || missing[0] != stepID {
					t.Fatalf("counterfeiting %s contract reports missing steps %v, want [%s]", stepID, missing, stepID)
				}
				step.Instructions = original
			}
			for _, want := range []struct {
				id         string
				timeout    time.Duration
				timeBudget time.Duration
			}{{"implement", 3 * time.Hour, 3 * time.Hour}, {"verify", 2 * time.Hour, 2 * time.Hour}} {
				step := repositoryPipelineStep(pipeline, want.id)
				if step == nil || step.Timeout != want.timeout || step.TimeBudget != want.timeBudget {
					t.Fatalf("frontend %s timeout/time budget = %+v, want %s/%s", want.id, step, want.timeout, want.timeBudget)
				}
			}
			owner := top.Instances["frontend-manager"]
			team := top.Teams["frontend"]
			if owner == nil || owner.Ephemeral || owner.Agent != "manager" || team == nil || !repositoryContains(team.Instances, owner.Name) || !repositoryContains(team.Pipelines, pipeline.Name) {
				t.Fatalf("frontend ownership contract invalid: owner=%+v team=%+v", owner, team)
			}
			if !repositoryFrontendManagerReceivesGateReady(owner) {
				t.Fatal("frontend-manager lacks exact job.step_completed target trigger")
			}
			for _, verb := range []string{"job.bounce", "job.step", "job.gate.set", "job.approve", "job.reject", "job.merge"} {
				eval := top.Authority.Evaluate(topology.AuthorityDecision{Instance: owner.Name, Agent: owner.Agent, Team: top.TeamForInstance(owner.Name), Verb: verb, TargetTeam: "frontend"})
				if !eval.Allowed {
					t.Fatalf("frontend manager denied %s: %+v", verb, eval)
				}
			}

			for name, required := range wantRequired {
				inst := top.Instances[name]
				if inst == nil || !reflect.DeepEqual(inst.RequiredVerbs, required) {
					t.Fatalf("%s required verbs = %v, want %v", name, inst.RequiredVerbs, required)
				}
				for _, verb := range required {
					eval := top.Authority.Evaluate(topology.AuthorityDecision{Instance: inst.Name, Agent: inst.Agent, Team: top.TeamForInstance(inst.Name), Verb: verb})
					if !eval.Allowed {
						t.Fatalf("%s required verb %s denied: %+v", name, verb, eval)
					}
				}
				for _, denied := range []string{"event.publish", "instance.up", "job.bounce", "job.merge", "topology.reload"} {
					eval := top.Authority.Evaluate(topology.AuthorityDecision{Instance: inst.Name, Agent: inst.Agent, Team: top.TeamForInstance(inst.Name), Verb: denied})
					if eval.Allowed {
						t.Fatalf("%s unexpectedly granted %s via %v", name, denied, eval.Sources)
					}
				}
			}
		})
	}
}

func TestTopologyRepositoryContractAuthorityMutationsAreRejected(t *testing.T) {
	for _, fixture := range topologyRepositoryFixtures(t) {
		if fixture.name != "self-dogfood" {
			continue
		}
		t.Run(fixture.name, func(t *testing.T) {
			deadOwn := strings.Replace(string(fixture.body),
				`[authority.instances.frontend-manager]
allow = ["event.publish", "job.events", "job.gate.*:team", "job.step:team", "job.bounce:team", "job.approve:team", "job.reject:team", "job.close:team", "job.merge:team", "read", "ticket.create", "ticket.comment", "ticket.update"]`,
				`[authority.instances.frontend-manager]
allow = ["event.publish", "job.events", "job.gate.*:own", "job.step:own", "job.bounce:own", "job.approve:own", "job.reject:own", "job.close:own", "job.merge:own", "read", "ticket.create", "ticket.comment", "ticket.update"]`, 1)
			if deadOwn == string(fixture.body) {
				t.Fatal("frontend authority mutation did not change fixture")
			}
			if _, err := topology.Parse([]byte(deadOwn)); err == nil || !strings.Contains(err.Error(), `lacks effective authority "job.bounce:team"`) {
				t.Fatalf("dead-own authority error = %v", err)
			}

			missingGrant := removeRepositoryAuthorityGrant(t, string(fixture.body), "feedback-triage", "job.ls")
			if _, err := topology.Parse([]byte(missingGrant)); err == nil || !strings.Contains(err.Error(), `scheduled instance "feedback-triage": required verb "job.ls"`) {
				t.Fatalf("missing scheduled grant error = %v", err)
			}
		})
	}
}

func TestTopologyRepositoryContractRuntimePolicyExamples(t *testing.T) {
	for _, fixture := range topologyRepositoryFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			top := fixture.top
			if got := top.ModelPolicy; got == nil || got.Runtime != "codex" || got.Model != "gpt-5.6-sol" || got.Effort != "xhigh" {
				t.Fatalf("model policy = %+v", got)
			}
			fable := make([]string, 0, 3)
			for name, inst := range top.Instances {
				if inst.Model == "claude-fable-5" {
					fable = append(fable, name)
					if inst.Runtime != "claude" || inst.Effort != "max" {
						t.Fatalf("Fable seat %s = %q/%q/%q", name, inst.Runtime, inst.Model, inst.Effort)
					}
					continue
				}
				if inst.Runtime != "codex" || inst.Model != "gpt-5.6-sol" || inst.Effort != "xhigh" {
					t.Fatalf("non-Fable seat %s = %q/%q/%q", name, inst.Runtime, inst.Model, inst.Effort)
				}
			}
			sort.Strings(fable)
			if want := []string{"advisor", "harness-reviewer", "org-review"}; !reflect.DeepEqual(fable, want) {
				t.Fatalf("Fable seats = %v, want %v", fable, want)
			}
			for pipelineName, pipeline := range top.Pipelines {
				for _, step := range pipeline.Steps {
					target := top.Instances[step.Target]
					if target == nil || step.Runtime != target.Runtime || step.Model != target.Model || step.Effort != target.Effort {
						t.Fatalf("pipeline %s step %s policy = %q/%q/%q, target=%+v", pipelineName, step.ID, step.Runtime, step.Model, step.Effort, target)
					}
				}
			}
		})
	}
}

type topologyRepositoryFixture struct {
	name string
	body []byte
	top  *topology.Topology
}

func topologyRepositoryFixtures(t *testing.T) []topologyRepositoryFixture {
	t.Helper()
	selfDogfood := readTopologyRepositoryFile(t, filepath.Join(".agent_team", "instances.toml"))
	templateBody := readTopologyRepositoryFile(t, filepath.Join("template", "instances.toml.tmpl"))
	data := templatecfg.Tree{}
	data.SetDotted("template.profile", "full")
	data.SetDotted("pm.provider", "github")
	data.SetDotted("github.owner", "acme")
	data.SetDotted("github.repo", "frontend")
	data.SetDotted("github.agent_column", "Ready for Agent")
	rendered, err := templatecfg.RenderBytes("instances.toml.tmpl", templateBody, data)
	if err != nil {
		t.Fatalf("render bundled full topology: %v", err)
	}

	fixtures := []topologyRepositoryFixture{{name: "self-dogfood", body: selfDogfood}, {name: "bundled-full-template", body: rendered}}
	for i := range fixtures {
		fixtures[i].top, err = topology.Parse(fixtures[i].body)
		if err != nil {
			t.Fatalf("parse %s topology: %v", fixtures[i].name, err)
		}
	}
	return fixtures
}

func topologyPackageTestReadsMutableDeployment(source string) bool {
	return strings.Contains(source, ".agent_team") && strings.Contains(source, "instances.toml")
}

func readTopologyRepositoryFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}

func assertRepositoryResearchPipeline(t *testing.T, pipeline *topology.Pipeline, wantIDs []string, manualID string) {
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

func repositoryPipelineStep(pipeline *topology.Pipeline, id string) *topology.PipelineStep {
	if pipeline == nil {
		return nil
	}
	for _, step := range pipeline.Steps {
		if step.ID == id {
			return step
		}
	}
	return nil
}

func repositoryMissingFrontendEvidenceContract(top *topology.Topology) []string {
	missing := make([]string, 0, 2)
	for _, stepID := range []string{"implement", "verify"} {
		step := repositoryPipelineStep(top.Pipelines["frontend_ticket_to_pr"], stepID)
		if step == nil || !strings.Contains(strings.Join(strings.Fields(step.Instructions), " "), repositoryFrontendEvidenceContract) {
			missing = append(missing, stepID)
		}
	}
	return missing
}

func repositoryFrontendManagerReceivesGateReady(owner *topology.Instance) bool {
	if owner == nil {
		return false
	}
	for _, trigger := range owner.Triggers {
		if trigger == nil || trigger.Event != topology.EventJobStepCompleted {
			continue
		}
		if target := trigger.Match["target"]; target.Single == owner.Name {
			return true
		}
	}
	return false
}

func repositoryDeclaredVerifierGateLines(instructions string) []string {
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

func repositoryContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func removeRepositoryAuthorityGrant(t *testing.T, body, instance, grant string) string {
	t.Helper()
	startMarker := "[authority.instances." + instance + "]"
	start := strings.Index(body, startMarker)
	if start < 0 {
		t.Fatalf("authority section %q is missing", startMarker)
	}
	end := strings.Index(body[start+len(startMarker):], "\n[")
	if end < 0 {
		end = len(body)
	} else {
		end += start + len(startMarker)
	}
	section := body[start:end]
	quoted := `"` + grant + `"`
	updated := strings.Replace(section, quoted+", ", "", 1)
	if updated == section {
		updated = strings.Replace(section, ", "+quoted, "", 1)
	}
	if updated == section {
		t.Fatalf("grant %q is missing from %s", grant, startMarker)
	}
	return body[:start] + updated + body[end:]
}
