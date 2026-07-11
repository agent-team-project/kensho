package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeTeam builds a minimal `.agent_team`-style tree for tests.
func makeTeam(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	teamDir := filepath.Join(dir, ".agent_team")
	if err := os.MkdirAll(filepath.Join(teamDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(teamDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	return teamDir
}

func TestLoadAgent_Basic(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "alpha")
	writeFile(t, filepath.Join(agentDir, "agent.md"),
		"---\ndescription: alpha agent\n---\nprompt body\n")

	a, err := LoadAgent(agentDir, teamDir)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a.Name != "alpha" {
		t.Errorf("name=%q", a.Name)
	}
	if a.Description != "alpha agent" {
		t.Errorf("description=%q", a.Description)
	}
	if a.Prompt != "prompt body\n" {
		t.Errorf("prompt=%q", a.Prompt)
	}
	if len(a.Skills) != 0 {
		t.Errorf("expected no skills, got %v", a.Skills)
	}
}

func TestLoadAgent_Runtime(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "worker")
	writeFile(t, filepath.Join(agentDir, "agent.md"),
		"---\ndescription: worker agent\nruntime: codex\nruntime_bin: /opt/homebrew/bin/codex\n---\nbody\n")

	a, err := LoadAgent(agentDir, teamDir)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a.Runtime != "codex" {
		t.Errorf("runtime=%q, want codex", a.Runtime)
	}
	if a.RuntimeBin != "/opt/homebrew/bin/codex" {
		t.Errorf("runtime_bin=%q", a.RuntimeBin)
	}
}

func TestLoadAgent_RuntimeOmittedIsEmpty(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "manager")
	writeFile(t, filepath.Join(agentDir, "agent.md"),
		"---\ndescription: manager agent\n---\nbody\n")

	a, err := LoadAgent(agentDir, teamDir)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a.Runtime != "" || a.RuntimeBin != "" {
		t.Errorf("expected empty runtime fields, got runtime=%q bin=%q", a.Runtime, a.RuntimeBin)
	}
}

func TestLoadAgent_MissingFile(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "ghost")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAgent(agentDir, teamDir)
	if err == nil {
		t.Fatal("expected error for missing agent.md")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error message=%q", err.Error())
	}
}

func TestLoadAgent_NoDescription(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "blank")
	writeFile(t, filepath.Join(agentDir, "agent.md"),
		"---\nname: blank\n---\nbody")
	_, err := LoadAgent(agentDir, teamDir)
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Errorf("expected error about missing description, got %v", err)
	}
}

func TestResolveSkills_LocalAndShared(t *testing.T) {
	teamDir := makeTeam(t)

	writeFile(t, filepath.Join(teamDir, "skills", "shared-skill", "SKILL.md"), "shared")

	agentDir := filepath.Join(teamDir, "agents", "alpha")
	writeFile(t, filepath.Join(agentDir, "skills", "local-skill", "SKILL.md"), "local")
	writeFile(t, filepath.Join(agentDir, "config.toml"),
		"[skills]\nextra = [\"shared-skill\"]\n")

	got, err := ResolveSkills(agentDir, teamDir)
	if err != nil {
		t.Fatalf("ResolveSkills: %v", err)
	}
	if _, ok := got["local-skill"]; !ok {
		t.Errorf("expected local-skill, got %v", got)
	}
	if _, ok := got["shared-skill"]; !ok {
		t.Errorf("expected shared-skill, got %v", got)
	}
}

func TestResolveSkills_TeamSkillsApplyToEveryAgent(t *testing.T) {
	teamDir := makeTeam(t)

	writeFile(t, filepath.Join(teamDir, "config.toml"),
		"[skills]\nteam = [\"linear\"]\n")
	writeFile(t, filepath.Join(teamDir, "skills", "linear", "SKILL.md"), "linear")
	for _, name := range []string{"alpha", "bravo"} {
		writeFile(t, filepath.Join(teamDir, "agents", name, "agent.md"),
			"---\ndescription: "+name+"\n---\nbody\n")
	}

	agents, err := LoadAllAgents(teamDir)
	if err != nil {
		t.Fatalf("LoadAllAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("loaded %d agents, want 2", len(agents))
	}
	for _, agent := range agents {
		if _, ok := agent.Skills["linear"]; !ok {
			t.Fatalf("%s missing team skill linear: %+v", agent.Name, agent.Skills)
		}
	}
}

func TestResolveSkills_TeamSkillsSurviveAgentDisable(t *testing.T) {
	teamDir := makeTeam(t)

	writeFile(t, filepath.Join(teamDir, "config.toml"),
		"[skills]\nteam = [\"status\"]\n")
	writeFile(t, filepath.Join(teamDir, "skills", "status", "SKILL.md"), "status")
	agentDir := filepath.Join(teamDir, "agents", "alpha")
	writeFile(t, filepath.Join(agentDir, "config.toml"),
		"[skills]\ndisable = [\"status\"]\n")

	got, err := ResolveSkills(agentDir, teamDir)
	if err != nil {
		t.Fatalf("ResolveSkills: %v", err)
	}
	if _, ok := got["status"]; !ok {
		t.Fatalf("team skill status should not be disabled by agent config: %+v", got)
	}
}

func TestResolveTeamSkills_MissingSharedSkill(t *testing.T) {
	teamDir := makeTeam(t)
	writeFile(t, filepath.Join(teamDir, "config.toml"),
		"[skills]\nteam = [\"missing\"]\n")

	_, err := ResolveTeamSkills(teamDir)
	if err == nil || !strings.Contains(err.Error(), "team skill `missing` not found") {
		t.Fatalf("expected missing team skill error, got %v", err)
	}
}

func TestResolveTeamSkills_RejectsPathSpecs(t *testing.T) {
	teamDir := makeTeam(t)
	writeFile(t, filepath.Join(teamDir, "config.toml"),
		"[skills]\nteam = [\"./local\"]\n")

	_, err := ResolveTeamSkills(teamDir)
	if err == nil || !strings.Contains(err.Error(), "must be a shared skill name") {
		t.Fatalf("expected path-spec error, got %v", err)
	}
}

func TestResolveSkills_PathReferenced(t *testing.T) {
	teamDir := makeTeam(t)

	// A skill at an arbitrary path inside the agent dir.
	agentDir := filepath.Join(teamDir, "agents", "alpha")
	writeFile(t, filepath.Join(agentDir, "vendored", "my-skill", "SKILL.md"), "x")
	writeFile(t, filepath.Join(agentDir, "config.toml"),
		"[skills]\nextra = [\"./vendored/my-skill\"]\n")

	got, err := ResolveSkills(agentDir, teamDir)
	if err != nil {
		t.Fatalf("ResolveSkills: %v", err)
	}
	if _, ok := got["my-skill"]; !ok {
		t.Errorf("expected my-skill, got %v", got)
	}
}

func TestResolveSkills_MissingExtra(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "alpha")
	writeFile(t, filepath.Join(agentDir, "config.toml"),
		"[skills]\nextra = [\"does-not-exist\"]\n")
	_, err := ResolveSkills(agentDir, teamDir)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestResolveSkills_Disable(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "alpha")
	writeFile(t, filepath.Join(agentDir, "skills", "kept", "SKILL.md"), "k")
	writeFile(t, filepath.Join(agentDir, "skills", "removed", "SKILL.md"), "r")
	writeFile(t, filepath.Join(agentDir, "config.toml"),
		"[skills]\nextra = []\ndisable = [\"removed\"]\n")

	got, err := ResolveSkills(agentDir, teamDir)
	if err != nil {
		t.Fatalf("ResolveSkills: %v", err)
	}
	if _, ok := got["kept"]; !ok {
		t.Errorf("expected kept, got %v", got)
	}
	if _, ok := got["removed"]; ok {
		t.Errorf("expected removed to be disabled, got %v", got)
	}
}

func TestResolveSkills_LocalSkillCollision(t *testing.T) {
	// Local skill `foo` exists. `extra = ["./other-foo"]` resolves to a
	// different path with basename `foo` — should error.
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "alpha")
	writeFile(t, filepath.Join(agentDir, "skills", "foo", "SKILL.md"), "local")
	writeFile(t, filepath.Join(agentDir, "elsewhere", "foo", "SKILL.md"), "extra")
	writeFile(t, filepath.Join(agentDir, "config.toml"),
		"[skills]\nextra = [\"./elsewhere/foo\"]\n")

	_, err := ResolveSkills(agentDir, teamDir)
	if err == nil || !strings.Contains(err.Error(), "already a local skill") {
		t.Errorf("expected collision error, got %v", err)
	}
}

func TestLoadAllAgents_SortedAndAggregates(t *testing.T) {
	teamDir := makeTeam(t)
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		writeFile(t, filepath.Join(teamDir, "agents", name, "agent.md"),
			"---\ndescription: "+name+"\n---\nbody "+name)
	}
	all, err := LoadAllAgents(teamDir)
	if err != nil {
		t.Fatalf("LoadAllAgents: %v", err)
	}
	got := []string{all[0].Name, all[1].Name, all[2].Name}
	want := []string{"alpha", "bravo", "charlie"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("at %d got %q want %q", i, got[i], want[i])
		}
	}
}

func TestLoadAllAgents_MissingAgentsDir(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadAllAgents(filepath.Join(dir, "nope"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestUnionSkills_Collision(t *testing.T) {
	a1 := &Agent{Name: "a", Skills: map[string]string{"foo": "/path/one"}}
	a2 := &Agent{Name: "b", Skills: map[string]string{"foo": "/path/two"}}
	_, err := UnionSkills([]*Agent{a1, a2})
	if err == nil || !strings.Contains(err.Error(), "two different paths") {
		t.Errorf("expected collision, got %v", err)
	}
}

func TestLoadAgent_SubscribesFromFrontmatter(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "subbed")
	writeFile(t, filepath.Join(agentDir, "agent.md"),
		"---\ndescription: a subbed agent\nsubscribes:\n  - \"#deploys\"\n  - \"#blocked\"\n---\nbody\n")

	a, err := LoadAgent(agentDir, teamDir)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	want := []string{"#deploys", "#blocked"}
	if len(a.Subscribes) != len(want) {
		t.Fatalf("subscribes: got %v want %v", a.Subscribes, want)
	}
	for i, s := range a.Subscribes {
		if s != want[i] {
			t.Errorf("subscribe %d: got %q want %q", i, s, want[i])
		}
	}
}

func TestLoadAgent_NoSubscribes(t *testing.T) {
	teamDir := makeTeam(t)
	agentDir := filepath.Join(teamDir, "agents", "plain")
	writeFile(t, filepath.Join(agentDir, "agent.md"),
		"---\ndescription: plain\n---\nbody\n")
	a, err := LoadAgent(agentDir, teamDir)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if len(a.Subscribes) != 0 {
		t.Errorf("expected no subscribes, got %v", a.Subscribes)
	}
}

func TestUnionSkills_SamePathOK(t *testing.T) {
	a1 := &Agent{Name: "a", Skills: map[string]string{"foo": "/p"}}
	a2 := &Agent{Name: "b", Skills: map[string]string{"foo": "/p"}}
	got, err := UnionSkills([]*Agent{a1, a2})
	if err != nil {
		t.Fatalf("UnionSkills: %v", err)
	}
	if got["foo"] != "/p" {
		t.Errorf("foo=%q", got["foo"])
	}
}
