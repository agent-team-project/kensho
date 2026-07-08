package daemon

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/agent-team-project/agent-team/internal/runtimebin"
)

const (
	dockerHostGatewayName = "host.docker.internal"
	dockerCodexHome       = "/root/.codex"
	dockerGHConfigDir     = "/root/.config/gh"
)

func (r *EventResolver) prepareDockerAgentArgs(rt runtimebin.Runtime, agentName, instance, hostStateDir, workspace, prompt string, env []string) ([]string, error) {
	image := strings.TrimSpace(rt.Image)
	if image == "" {
		image = runtimebin.DefaultImageForKind(runtimebin.KindDocker)
	}
	if image == "" {
		return nil, errors.New("docker runtime: image is required")
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("docker runtime: workspace is required")
	}
	hostStateDir = strings.TrimSpace(hostStateDir)
	if hostStateDir == "" {
		return nil, errors.New("docker runtime: state dir is required")
	}
	containerStateDir := dockerContainerStateDir(workspace, instance)
	if err := os.MkdirAll(containerStateDir, 0o755); err != nil {
		return nil, fmt.Errorf("docker runtime: create state mount point: %w", err)
	}
	promptFile := filepath.Join(hostStateDir, "runtime", "docker-prompt.md")
	if err := os.MkdirAll(filepath.Dir(promptFile), 0o755); err != nil {
		return nil, fmt.Errorf("docker runtime: create prompt dir: %w", err)
	}
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		return nil, fmt.Errorf("docker runtime: write prompt file: %w", err)
	}
	containerPromptFile := dockerStatePath(promptFile, hostStateDir, containerStateDir)
	daemonURL, err := dockerDaemonURL(env)
	if err != nil {
		return nil, err
	}

	args := []string{
		"run",
		"--rm",
		"-i",
		"--name", dockerContainerName(instance),
		"--add-host", dockerHostGatewayName + ":host-gateway",
		"--workdir", workspace,
		"--volume", dockerVolume(workspace, workspace, false),
		"--volume", dockerVolume(hostStateDir, containerStateDir, false),
	}
	if gitDir := gitCommonDir(workspace); gitDir != "" && !pathWithin(gitDir, workspace) {
		args = append(args, "--volume", dockerVolume(gitDir, gitDir, false))
	}
	args = append(args, dockerAuthMountArgs()...)
	args = append(args,
		"--env", "HOME=/root",
		"--env", "CODEX_HOME="+dockerCodexHome,
		"--env", "AGENT_TEAM_DAEMON_URL="+daemonURL,
		"--env", DaemonTokenFileEnv+"="+filepath.Join(containerStateDir, "daemon.token"),
	)
	for _, entry := range dockerForwardedEnv(env) {
		args = append(args, "--env", entry)
	}
	args = append(args,
		image,
		"run", agentName,
		"--no-daemon",
		"--runtime", string(runtimebin.KindCodex),
		"--name", instance,
		"--target", workspace,
		"--prompt-file", containerPromptFile,
		"--",
	)
	args = append(args, runtimebin.CodexAutonomousExecArgs()...)
	return args, nil
}

func dockerContainerStateDir(workspace, instance string) string {
	return filepath.Join(workspace, ".agent_team", "state", instance)
}

func dockerStatePath(path, hostStateDir, containerStateDir string) string {
	rel, err := filepath.Rel(hostStateDir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return filepath.Join(containerStateDir, rel)
}

func dockerDaemonURL(env []string) (string, error) {
	raw := dockerEnvValue(env, "AGENT_TEAM_DAEMON_URL")
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("docker runtime: AGENT_TEAM_DAEMON_URL is required; restart agent-teamd with loopback HTTP enabled")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("docker runtime: parse daemon URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("docker runtime: daemon URL %q has no host", raw)
	}
	if isLoopbackHost(host) {
		port := u.Port()
		if port == "" {
			return "", fmt.Errorf("docker runtime: daemon URL %q has no port", raw)
		}
		u.Host = net.JoinHostPort(dockerHostGatewayName, port)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.EqualFold(host, dockerHostGatewayName) {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func dockerForwardedEnv(env []string) []string {
	out := []string{}
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || (key != requiredLaunchEnvMainRepo && !strings.HasPrefix(key, "AGENT_TEAM_")) {
			continue
		}
		switch key {
		case "AGENT_TEAM_ROOT", "AGENT_TEAM_INSTANCE", "AGENT_TEAM_STATE_DIR", "AGENT_TEAM_DAEMON_SOCKET", "AGENT_TEAM_DAEMON_URL", DaemonTokenFileEnv:
			continue
		default:
			out = append(out, entry)
		}
	}
	return out
}

func dockerEnvValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

func dockerAuthMountArgs() []string {
	args := []string{}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if codexHome == "" {
			codexHome = filepath.Join(home, ".codex")
		}
		for _, name := range []string{"auth.json", "config.toml"} {
			hostPath := filepath.Join(codexHome, name)
			if regularFile(hostPath) {
				args = append(args, "--volume", dockerVolume(hostPath, filepath.Join(dockerCodexHome, name), true))
			}
		}
		ghConfig := filepath.Join(home, ".config", "gh")
		if directory(ghConfig) {
			args = append(args, "--volume", dockerVolume(ghConfig, dockerGHConfigDir, true))
		}
		gitConfig := filepath.Join(home, ".gitconfig")
		if regularFile(gitConfig) {
			args = append(args, "--volume", dockerVolume(gitConfig, "/root/.gitconfig", true))
		}
	}
	return args
}

func dockerVolume(source, target string, readonly bool) string {
	value := source + ":" + target
	if readonly {
		value += ":ro"
	}
	return value
}

func gitCommonDir(workspace string) string {
	out, err := exec.Command("git", "-C", workspace, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func regularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func directory(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func dockerContainerName(instance string) string {
	var b strings.Builder
	b.WriteString("agent-team-")
	for _, r := range instance {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 120 {
		name = name[:120]
	}
	if name == "" {
		return "agent-team-worker"
	}
	return name
}
