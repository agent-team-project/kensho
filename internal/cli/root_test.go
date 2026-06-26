package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jamesaud/agent-team/internal/job"
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

func TestPluralTopLevelAliasesDispatch(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "jobs", args: []string{"--repo", root, "jobs", "ls", "--json"}},
		{name: "pipelines", args: []string{"--repo", root, "pipelines", "ls", "--json"}},
		{name: "queues", args: []string{"--repo", root, "queues", "ls", "--summary", "--json"}},
		{name: "schedules", args: []string{"--repo", root, "schedules", "ls", "--json"}},
		{name: "teams", args: []string{"--repo", root, "teams", "ls", "--json"}},
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

func TestRepoHelpDistinguishesLegacyTargetFromAgentTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "legacy repo target",
			args: []string{"dispatch", "--help"},
			want: []string{"--repo string", "Repo root containing .agent_team for commands that read repo state", "--target string", legacyRepoTargetFlagHelp},
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
