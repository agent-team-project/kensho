package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAgentLsJSONListsBundledAgents(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "agent", "ls", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agent ls --json: %v\nstderr=%s", err, stderr.String())
	}

	var agents []agentInfo
	if err := json.Unmarshal(out.Bytes(), &agents); err != nil {
		t.Fatalf("decode agents: %v\nbody=%s", err, out.String())
	}
	if len(agents) < 3 {
		t.Fatalf("agents len = %d, want at least bundled core agents: %+v", len(agents), agents)
	}
	byName := map[string]agentInfo{}
	for _, agent := range agents {
		byName[agent.Name] = agent
		if strings.TrimSpace(agent.Description) == "" {
			t.Fatalf("agent %q has empty description", agent.Name)
		}
		if agent.Prompt != "" {
			t.Fatalf("agent ls included prompt for %q", agent.Name)
		}
	}
	for _, name := range []string{"manager", "ticket-manager", "worker"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing bundled agent %q in %+v", name, agents)
		}
	}
	if got := byName["worker"].Summary; !strings.Contains(got, "Executes Linear tickets") {
		t.Fatalf("worker summary = %q, want frontmatter summary", got)
	}
}

func TestAgentShowJSONIncludesPromptAndSkills(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "agent", "show", "worker", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agent show --json: %v\nstderr=%s", err, stderr.String())
	}

	var agent agentInfo
	if err := json.Unmarshal(out.Bytes(), &agent); err != nil {
		t.Fatalf("decode agent: %v\nbody=%s", err, out.String())
	}
	if agent.Name != "worker" {
		t.Fatalf("agent name = %q, want worker", agent.Name)
	}
	if !strings.Contains(agent.Prompt, "You are an engineering agent") {
		t.Fatalf("worker prompt missing expected body: %.120q", agent.Prompt)
	}
	if len(agent.Skills) == 0 {
		t.Fatalf("worker skills empty")
	}
}

func TestAgentsAliasSupportsFormattedList(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "agents", "ls", "--format", "{{.Name}}:{{len .Skills}}"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agents ls --format: %v\nstderr=%s", err, stderr.String())
	}
	body := out.String()
	for _, want := range []string{"manager:", "ticket-manager:", "worker:"} {
		if !strings.Contains(body, want) {
			t.Fatalf("formatted agents output missing %q\nbody=%s", want, body)
		}
	}
}

func TestAgentShowMissingAgent(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--repo", root, "agent", "show", "missing"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("agent show missing succeeded unexpectedly; stdout=%s", out.String())
	}
	var code ExitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("error = %v, want exit code 1", err)
	}
	if !strings.Contains(stderr.String(), `agent "missing" not found`) {
		t.Fatalf("stderr = %q, want missing agent message", stderr.String())
	}
}

func TestAgentFormatConflictsAndValidation(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "format-json-conflict",
			args: []string{"--repo", root, "agent", "ls", "--format", "{{.Name}}", "--json"},
			want: "--format cannot be combined with --json",
		},
		{
			name: "invalid-template",
			args: []string{"--repo", root, "agent", "show", "worker", "--format", "{{"},
			want: "invalid --format template",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("%s succeeded unexpectedly; stdout=%s", tc.name, out.String())
			}
			var code ExitCode
			if !errors.As(err, &code) || code != 2 {
				t.Fatalf("error = %v, want exit code 2", err)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}
