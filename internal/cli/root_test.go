package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/feedback"
	"github.com/agent-team-project/agent-team/internal/job"
)

func TestRootRepoFlagSelectsRepoForRepoScopedCommands(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "job", "ls", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job ls with root --repo: %v\nstderr=%s", err, stderr.String())
	}
	var jobs []job.Job
	if err := json.Unmarshal(out.Bytes(), &jobs); err != nil {
		t.Fatalf("decode jobs: %v\nbody=%s", err, out.String())
	}
	if len(jobs) != 0 {
		t.Fatalf("jobs = %+v, want none", jobs)
	}
}

func TestRootRepoFlagDoesNotConflictWithJobTargetAgent(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "job", "create", "SQU-707", "--target", "manager", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job create with root --repo and agent --target: %v\nstderr=%s", err, stderr.String())
	}
	var created job.Job
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("decode created job: %v\nbody=%s", err, out.String())
	}
	if created.ID != "squ-707" || created.Target != "manager" {
		t.Fatalf("created = %+v, want manager-targeted squ-707", created)
	}
}

func TestRootRepoFlagWorksAfterLegacyTargetCommand(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"plan", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan with --repo: %v\nstderr=%s", err, stderr.String())
	}
	var plan planResult
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v\nbody=%s", err, out.String())
	}
	if len(plan.Instances) == 0 {
		t.Fatalf("plan = %+v, want declared instances", plan)
	}
}

func TestRepoScopedCommandsResolveFromAgentTeamRootEnv(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	now := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	if err := job.Write(teamDir, &job.Job{
		ID:        "squ-100",
		Ticket:    "SQU-100",
		Target:    "worker",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("write job: %v", err)
	}
	worktree := filepath.Join(t.TempDir(), "worker")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	chdirForFeedbackTest(t, worktree)
	t.Setenv("AGENT_TEAM_ROOT", teamDir)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "job gate set",
			args: []string{"job", "gate", "set", "squ-100", "tests", "--status", "pass", "--json"},
			want: `"status":"pass"`,
		},
		{
			name: "job show",
			args: []string{"job", "show", "squ-100", "--json"},
			want: `"ID":"squ-100"`,
		},
		{
			name: "send",
			args: []string{"send", "worker", "review is ready", "--json"},
			want: `"delivered":true`,
		},
		{
			name: "feedback submit",
			args: []string{"feedback", "submit", "repo resolver friction"},
			want: "submitted fb-",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, err := runRootResolverCommand(tc.args...)
			if err != nil {
				t.Fatalf("%s failed: %v\nstderr=%s", tc.name, err, stderr)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("%s output missing %q:\n%s", tc.name, tc.want, out)
			}
		})
	}

	items, err := feedback.List(teamDir)
	if err != nil {
		t.Fatalf("list feedback: %v", err)
	}
	if len(items) != 1 || items[0].Body != "repo resolver friction" {
		t.Fatalf("feedback items = %+v, want submitted item in primary repo", items)
	}
}

func TestRepoScopedCommandMissingRepoErrorTeachesResolutionOptions(t *testing.T) {
	outside := t.TempDir()
	chdirForFeedbackTest(t, outside)
	t.Setenv("AGENT_TEAM_ROOT", "")

	_, stderr, err := runRootResolverCommand("job", "show", "squ-100")
	if err == nil {
		t.Fatalf("job show outside repo unexpectedly succeeded")
	}
	for _, want := range []string{"--repo <repo>", ".agent_team", "cwd ancestors", "AGENT_TEAM_ROOT"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func runRootResolverCommand(args ...string) (string, string, error) {
	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func TestRootRepoCommandsEmitRepoScope(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(`
[agents.worker]
runtime = "codex"
runtime_bin = "missing-agent-team-test-runtime"

[pipelines.ticket_to_pr]
trigger.event = "ticket.created"

[[pipelines.ticket_to_pr.steps]]
id = "implement"
target = "worker"
runtime = "codex"
runtime_bin = "missing-agent-team-test-runtime"
`), 0o644); err != nil {
		t.Fatalf("write topology: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"pipeline", "doctor", "--repo", root, "--strict-runtime", "--commands"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("pipeline doctor strict runtime unexpectedly succeeded")
	}
	want := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", root, "pipeline", "doctor", "ticket_to_pr", "--strict-runtime", "--json"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", root, "pipeline", "graph", "ticket_to_pr", "--routes"}), " "),
	}, "\n")
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("pipeline doctor commands = %q, want %q\nstderr=%s", got, want, stderr.String())
	}
	if strings.Contains(out.String(), "--target") {
		t.Fatalf("pipeline doctor commands should canonicalize repo scope, got:\n%s", out.String())
	}

	alias := NewRootCmd()
	aliasOut, aliasErr := &bytes.Buffer{}, &bytes.Buffer{}
	alias.SetOut(aliasOut)
	alias.SetErr(aliasErr)
	alias.SetArgs([]string{"pipeline", "doctor", "--repo", root, "--strict", "--commands"})
	if err := alias.Execute(); err == nil {
		t.Fatalf("pipeline doctor strict alias unexpectedly succeeded")
	}
	wantAlias := strings.Join([]string{
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", root, "pipeline", "doctor", "ticket_to_pr", "--strict", "--json"}), " "),
		strings.Join(shellQuoteArgs([]string{"agent-team", "--repo", root, "pipeline", "graph", "ticket_to_pr", "--routes"}), " "),
	}, "\n")
	if got := strings.TrimSpace(aliasOut.String()); got != wantAlias {
		t.Fatalf("pipeline doctor strict alias commands = %q, want %q\nstderr=%s", got, wantAlias, aliasErr.String())
	}
	if strings.Contains(aliasOut.String(), "--strict-runtime") || strings.Contains(aliasOut.String(), "--target") {
		t.Fatalf("pipeline doctor strict alias commands should preserve --strict and canonicalize repo scope, got:\n%s", aliasOut.String())
	}
}

func TestRootRepoAliasDoesNotConflictWithJobTargetAgent(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "job", "create", "SQU-708", "--target", "manager", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("job create with root --repo and agent --target: %v\nstderr=%s", err, stderr.String())
	}
	var created job.Job
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("decode created job: %v\nbody=%s", err, out.String())
	}
	if created.ID != "squ-708" || created.Target != "manager" {
		t.Fatalf("created = %+v, want manager-targeted squ-708", created)
	}
}

func TestPluralTopLevelAliasesDispatch(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "agent", args: []string{"--repo", root, "agent", "ls", "--json"}},
		{name: "job", args: []string{"--repo", root, "job", "ls", "--json"}},
		{name: "pipeline", args: []string{"--repo", root, "pipeline", "ls", "--json"}},
		{name: "queue", args: []string{"--repo", root, "queue", "ls", "--summary", "--json"}},
		{name: "schedule", args: []string{"--repo", root, "schedule", "ls", "--json"}},
		{name: "team", args: []string{"--repo", root, "team", "ls", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s alias failed: %v\nstderr=%s", tc.name, err, stderr.String())
			}
		})
	}
}

func TestRootGraphShortcutRendersGraphScopes(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "topology",
			args: []string{"graph", "--repo", root},
			want: []string{"Topology", "Teams:", "delivery", "ticket_to_pr"},
		},
		{
			name: "team",
			args: []string{"graph", "--repo", root, "--team", "delivery"},
			want: []string{"Team: delivery", "Pipelines:", "ticket_to_pr"},
		},
		{
			name: "positional team",
			args: []string{"graph", "--repo", root, "delivery"},
			want: []string{"Team: delivery", "Pipelines:", "ticket_to_pr"},
		},
		{
			name: "pipeline",
			args: []string{"graph", "--repo", root, "--pipeline", "ticket_to_pr"},
			want: []string{"Pipeline: ticket_to_pr", "Trigger:  ticket.status_changed(status)", "implement target=worker"},
		},
		{
			name: "positional pipeline",
			args: []string{"graph", "--repo", root, "ticket_to_pr"},
			want: []string{"Pipeline: ticket_to_pr", "Trigger:  ticket.status_changed(status)", "implement target=worker"},
		},
		{
			name: "mermaid",
			args: []string{"graph", "--repo", root, "--pipeline", "ticket_to_pr", "--format", "mermaid"},
			want: []string{"flowchart TD", "implement", "review", "approve"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("graph shortcut failed: %v\nstderr=%s", err, stderr.String())
			}
			body := out.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Fatalf("graph shortcut output missing %q\nbody:\n%s", want, body)
				}
			}
		})
	}

	jsonCmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	jsonCmd.SetOut(out)
	jsonCmd.SetErr(stderr)
	jsonCmd.SetArgs([]string{"graph", "--repo", root, "--pipeline", "ticket_to_pr", "--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("graph shortcut json failed: %v\nstderr=%s", err, stderr.String())
	}
	var graph pipelineGraph
	if err := json.Unmarshal(out.Bytes(), &graph); err != nil {
		t.Fatalf("decode graph json: %v\nbody=%s", err, out.String())
	}
	if graph.Name != "ticket_to_pr" || len(graph.Nodes) != 4 {
		t.Fatalf("graph = %+v, want ticket_to_pr with four nodes", graph)
	}
}

func TestRootGraphShortcutRejectsConflictingFlags(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "team and pipeline",
			args: []string{"graph", "--repo", root, "--team", "delivery", "--pipeline", "ticket_to_pr"},
			want: "choose at most one of --team or --pipeline",
		},
		{
			name: "selector and team flag",
			args: []string{"graph", "--repo", root, "ticket_to_pr", "--team", "delivery"},
			want: "positional selector cannot be combined with --team or --pipeline",
		},
		{
			name: "unknown selector",
			args: []string{"graph", "--repo", root, "missing"},
			want: `selector "missing" is not a declared team or pipeline`,
		},
		{
			name: "format and json",
			args: []string{"graph", "--repo", root, "--json", "--format", "dot"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "commands and json",
			args: []string{"graph", "--repo", root, "--commands", "--json"},
			want: wantCommandsModeConflict("--json"),
		},
		{
			name: "commands and format",
			args: []string{"graph", "--repo", root, "--commands", "--format", "dot"},
			want: wantCommandsModeConflict("--format"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("graph shortcut unexpectedly succeeded\nstdout=%s", out.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("graph shortcut stderr missing %q\nstderr=%s", tc.want, stderr.String())
			}
		})
	}
}

func TestRootGraphShortcutRejectsAmbiguousSelector(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	path := filepath.Join(root, ".agent_team", "instances.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read topology: %v", err)
	}
	body = append(body, []byte(`

[pipelines.delivery]
trigger.event = "ticket.created"

[[pipelines.delivery.steps]]
id = "implement"
target = "worker"
`)...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write topology: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"graph", "--repo", root, "delivery"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("graph shortcut ambiguous selector unexpectedly succeeded\nstdout=%s", out.String())
	}
	want := `selector "delivery" matches both a team and pipeline; use --team or --pipeline`
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("graph shortcut ambiguous stderr missing %q\nstderr=%s", want, stderr.String())
	}
}

func TestRepoHelpDistinguishesGlobalRepoFromAgentTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "global repo selector",
			args: []string{"dispatch", "--help"},
			want: []string{"--repo string", "Repo root containing .agent_team for commands that read repo state"},
		},
		{
			name: "job target agent",
			args: []string{"job", "create", "--help"},
			want: []string{"--repo string", repoFlagHelp, "--target string", "Target agent that should own this job."},
		},
		{
			name: "adopt shortcut repo",
			args: []string{"adopt", "--help"},
			want: []string{"--repo string", repoFlagHelp},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%v: %v\nstderr=%s", tc.args, err, stderr.String())
			}
			body := out.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Fatalf("help for %v missing %q\nbody:\n%s", tc.args, want, body)
				}
			}
		})
	}
}
