package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/runtimebin"
)

func TestPrepareDockerAgentArgsMountsWorktreeStateAndAuth(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"mode":"chatgpt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "gh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "init")
	worktree := filepath.Join(repoRoot, ".claude", "worktrees", "worker-squ-131-test")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "worktree", "add", "-b", "worker-squ-131-test", worktree, "HEAD")

	instance := "worker-squ-131"
	stateDir := filepath.Join(repoRoot, ".agent_team", "state", instance)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"MAIN_REPO=" + repoRoot,
		"AGENT_TEAM_ROOT=" + filepath.Join(repoRoot, ".agent_team"),
		"AGENT_TEAM_INSTANCE=" + instance,
		"AGENT_TEAM_STATE_DIR=" + stateDir,
		DaemonTokenFileEnv + "=" + filepath.Join(stateDir, "daemon.token"),
		"AGENT_TEAM_DAEMON_URL=http://127.0.0.1:54321",
		"AGENT_TEAM_JOB_ID=squ-131",
		"AGENT_TEAM_TICKET=SQU-131",
	}

	args, err := (&EventResolver{}).prepareDockerAgentArgs(
		runtimebin.Runtime{Kind: runtimebin.KindDocker, Binary: "docker", Image: "agent-team:test"},
		"worker",
		instance,
		stateDir,
		worktree,
		"do a trivial job",
		env,
	)
	if err != nil {
		t.Fatalf("prepareDockerAgentArgs: %v", err)
	}

	containerStateDir := filepath.Join(worktree, ".agent_team", "state", instance)
	gitCommon := gitCommonDir(worktree)
	if gitCommon == "" {
		t.Fatal("git common dir is empty")
	}
	for _, want := range []string{
		"run",
		"--rm",
		"-i",
		"agent-team:test",
		"worker",
		"--runtime",
		string(runtimebin.KindCodex),
		"--target",
		worktree,
		"--prompt-file",
		filepath.Join(containerStateDir, "runtime", "docker-prompt.md"),
		runtimebin.CodexAutonomousExecArgs()[0],
	} {
		if !containsString(args, want) {
			t.Fatalf("docker args missing %q:\n%v", want, args)
		}
	}
	for _, want := range []string{
		dockerVolume(worktree, worktree, false),
		dockerVolume(gitCommon, gitCommon, false),
		dockerVolume(stateDir, containerStateDir, false),
		dockerVolume(filepath.Join(codexHome, "auth.json"), filepath.Join(dockerCodexHome, "auth.json"), true),
		dockerVolume(filepath.Join(codexHome, "config.toml"), filepath.Join(dockerCodexHome, "config.toml"), true),
		dockerVolume(filepath.Join(home, ".config", "gh"), dockerGHConfigDir, true),
		dockerVolume(filepath.Join(home, ".gitconfig"), "/root/.gitconfig", true),
	} {
		if !containsString(args, want) {
			t.Fatalf("docker args missing volume %q:\n%v", want, args)
		}
	}
	for _, want := range []string{
		"AGENT_TEAM_DAEMON_URL=http://host.docker.internal:54321",
		DaemonTokenFileEnv + "=" + filepath.Join(containerStateDir, "daemon.token"),
		"MAIN_REPO=" + repoRoot,
		"AGENT_TEAM_JOB_ID=squ-131",
		"AGENT_TEAM_TICKET=SQU-131",
	} {
		if !containsString(args, want) {
			t.Fatalf("docker args missing env %q:\n%v", want, args)
		}
	}
	for _, forbidden := range []string{
		"AGENT_TEAM_ROOT=" + filepath.Join(repoRoot, ".agent_team"),
		"AGENT_TEAM_STATE_DIR=" + stateDir,
	} {
		if containsString(args, forbidden) {
			t.Fatalf("docker args forwarded host-only env %q:\n%v", forbidden, args)
		}
	}
	body, err := os.ReadFile(filepath.Join(stateDir, "runtime", "docker-prompt.md"))
	if err != nil {
		t.Fatalf("prompt file: %v", err)
	}
	if strings.TrimSpace(string(body)) != "do a trivial job" {
		t.Fatalf("prompt file = %q", string(body))
	}
}

func TestPrepareDockerAgentArgsRequiresDaemonHTTPURL(t *testing.T) {
	stateDir := t.TempDir()
	workspace := t.TempDir()
	_, err := (&EventResolver{}).prepareDockerAgentArgs(
		runtimebin.Runtime{Kind: runtimebin.KindDocker, Binary: "docker", Image: "agent-team:test"},
		"worker",
		"worker-squ-131",
		stateDir,
		workspace,
		"go",
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "AGENT_TEAM_DAEMON_URL is required") {
		t.Fatalf("err = %v, want daemon URL requirement", err)
	}
}
