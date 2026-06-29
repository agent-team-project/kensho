package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestAgentRuntimeFrontmatterVisible(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	agentDir := filepath.Join(root, ".agent_team", "agents", "codex-worker")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agent.md"), []byte(`---
description: Codex worker
runtime: codex
runtime_bin: /opt/bin/codex-wrapper
---
Run Codex work.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"--repo", root, "agent", "ls", "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("agent ls runtime json: %v\nstderr=%s", err, listErr.String())
	}
	var agents []agentInfo
	if err := json.Unmarshal(listOut.Bytes(), &agents); err != nil {
		t.Fatalf("decode agent list: %v\nbody=%s", err, listOut.String())
	}
	var found *agentInfo
	for i := range agents {
		if agents[i].Name == "codex-worker" {
			found = &agents[i]
			break
		}
	}
	if found == nil || found.Runtime != "codex" || found.RuntimeBin != "/opt/bin/codex-wrapper" {
		t.Fatalf("codex-worker runtime info = %+v", found)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"--repo", root, "agent", "show", "codex-worker"})
	if err := text.Execute(); err != nil {
		t.Fatalf("agent show runtime text: %v\nstderr=%s", err, textErr.String())
	}
	for _, want := range []string{"Runtime:     codex", "Runtime bin: /opt/bin/codex-wrapper"} {
		if !strings.Contains(textOut.String(), want) {
			t.Fatalf("agent show text missing %q:\n%s", want, textOut.String())
		}
	}

	formatted := NewRootCmd()
	formatOut, formatErr := &bytes.Buffer{}, &bytes.Buffer{}
	formatted.SetOut(formatOut)
	formatted.SetErr(formatErr)
	formatted.SetArgs([]string{"--repo", root, "agents", "ls", "--format", "{{.Name}}:{{.Runtime}}:{{.RuntimeBin}}"})
	if err := formatted.Execute(); err != nil {
		t.Fatalf("agents ls runtime format: %v\nstderr=%s", err, formatErr.String())
	}
	if !strings.Contains(formatOut.String(), "codex-worker:codex:/opt/bin/codex-wrapper") {
		t.Fatalf("formatted runtime output missing codex-worker:\n%s", formatOut.String())
	}
}

func TestAgentDoctorWarnsWhenRuntimeUnavailable(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	agentDir := filepath.Join(root, ".agent_team", "agents", "codex-worker")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agent.md"), []byte(`---
description: Codex worker
runtime: codex
runtime_bin: missing-codex
---
Run Codex work.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	withRuntimeLookPath(t, func(bin string) (string, error) {
		if bin != "missing-codex" {
			t.Fatalf("look path bin = %q, want missing-codex", bin)
		}
		return "", exec.ErrNotFound
	})

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"agent", "doctor", "codex-worker", "--repo", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agent doctor json: %v\nstderr=%s", err, stderr.String())
	}
	var result agentDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode agent doctor json: %v\nbody=%s", err, out.String())
	}
	if !result.OK || len(result.Problems) != 0 || len(result.Warnings) != 1 {
		t.Fatalf("agent doctor result = %+v", result)
	}
	got := result.Warnings[0]
	if got.Code != "agent_runtime_unavailable" || got.Agent != "codex-worker" || got.Runtime != "codex" || got.RuntimeBin != "missing-codex" {
		t.Fatalf("runtime warning = %+v", got)
	}

	text := NewRootCmd()
	textOut, textErr := &bytes.Buffer{}, &bytes.Buffer{}
	text.SetOut(textOut)
	text.SetErr(textErr)
	text.SetArgs([]string{"agent", "doctor", "codex-worker", "--repo", root})
	if err := text.Execute(); err != nil {
		t.Fatalf("agent doctor text: %v\nstderr=%s", err, textErr.String())
	}
	if !strings.Contains(textOut.String(), "agent-team agent doctor: OK (codex-worker)") {
		t.Fatalf("agent doctor text stdout = %q", textOut.String())
	}
	if !strings.Contains(textErr.String(), `runtime "codex" with binary "missing-codex"`) {
		t.Fatalf("agent doctor text stderr = %q", textErr.String())
	}

	commands := NewRootCmd()
	commandsOut, commandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	commands.SetOut(commandsOut)
	commands.SetErr(commandsErr)
	commands.SetArgs([]string{"agent", "doctor", "codex-worker", "--repo", root, "--commands"})
	if err := commands.Execute(); err != nil {
		t.Fatalf("agent doctor commands: %v\nstderr=%s", err, commandsErr.String())
	}
	wantCommands := strings.Join(scopedOperatorActions([]string{
		agentDoctorDetailAction("codex-worker", false),
		strings.Join(shellQuoteArgs([]string{"agent-team", "agent", "show", "codex-worker", "--json"}), " "),
	}, operatorCommandScope{Repo: root, Set: true}), "\n") + "\n"
	if got := commandsOut.String(); got != wantCommands {
		t.Fatalf("agent doctor commands output = %q, want %q", got, wantCommands)
	}
	if commandsErr.Len() != 0 {
		t.Fatalf("agent doctor commands stderr = %q", commandsErr.String())
	}

	strict := NewRootCmd()
	strictOut, strictErr := &bytes.Buffer{}, &bytes.Buffer{}
	strict.SetOut(strictOut)
	strict.SetErr(strictErr)
	strict.SetArgs([]string{"agent", "doctor", "codex-worker", "--repo", root, "--strict-runtime", "--json"})
	err := strict.Execute()
	if err == nil {
		t.Fatal("agent doctor strict runtime unexpectedly succeeded")
	}
	var code ExitCode
	if !errors.As(err, &code) || int(code) != 1 {
		t.Fatalf("strict err = %v, want exit 1", err)
	}
	var strictResult agentDoctorResult
	if err := json.Unmarshal(strictOut.Bytes(), &strictResult); err != nil {
		t.Fatalf("decode strict agent doctor json: %v\nbody=%s", err, strictOut.String())
	}
	if strictResult.OK || len(strictResult.Problems) != 1 || len(strictResult.Warnings) != 0 || strictResult.Problems[0].Code != "agent_runtime_unavailable" {
		t.Fatalf("strict agent doctor result = %+v", strictResult)
	}
	if strictErr.Len() != 0 {
		t.Fatalf("strict stderr = %q", strictErr.String())
	}
}

func TestAgentDoctorWarnsWhenRuntimeBinHasNoRuntime(t *testing.T) {
	root := t.TempDir()
	initInto(t, root)
	agentDir := filepath.Join(root, ".agent_team", "agents", "wrapped-worker")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "agent.md"), []byte(`---
description: Wrapped worker
runtime_bin: codex-wrapper
---
Run wrapped work.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"agent", "doctor", "wrapped-worker", "--repo", root, "--strict-runtime", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("agent doctor ignored runtime_bin warning should not fail strict mode: %v\nstderr=%s", err, stderr.String())
	}
	var result agentDoctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode agent doctor ignored runtime_bin json: %v\nbody=%s", err, out.String())
	}
	if !result.OK || len(result.Problems) != 0 || len(result.Warnings) != 1 || result.Warnings[0].Code != "agent_runtime_bin_ignored" {
		t.Fatalf("agent doctor ignored runtime_bin result = %+v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("agent doctor json stderr = %q", stderr.String())
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
