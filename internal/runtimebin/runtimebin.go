package runtimebin

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type Kind string

const (
	KindClaude Kind = "claude"
	KindCodex  Kind = "codex"

	DefaultBinary  = "claude"
	DefaultRuntime = KindClaude
	EnvRuntime     = "AGENT_TEAM_RUNTIME"
	EnvBinary      = "AGENT_TEAM_RUNTIME_BIN"

	// CodexLastMessageFile is the per-instance sidecar filename used with
	// `codex exec --output-last-message` to capture a clean final response.
	CodexLastMessageFile = "last-message.txt"
)

type Runtime struct {
	Kind   Kind
	Binary string
}

type configFile struct {
	Runtime runtimeConfig `toml:"runtime"`
}

type runtimeConfig struct {
	Kind   string `toml:"kind"`
	Binary string `toml:"binary"`
	Bin    string `toml:"bin"`
}

func Current() (Runtime, error) {
	return currentWithConfig(runtimeConfig{})
}

// CurrentFromConfig resolves the runtime using environment variables first,
// then an optional repo config file containing [runtime].kind and
// [runtime].binary (or [runtime].bin), then built-in defaults.
func CurrentFromConfig(configPath string) (Runtime, error) {
	cfg, err := loadRuntimeConfig(configPath)
	if err != nil {
		return Runtime{}, err
	}
	return currentWithConfig(cfg)
}

func currentWithConfig(cfg runtimeConfig) (Runtime, error) {
	envKind := os.Getenv(EnvRuntime)
	kindRaw := envKind
	if strings.TrimSpace(kindRaw) == "" {
		kindRaw = cfg.Kind
	}
	kind, err := ParseKind(kindRaw)
	if err != nil {
		return Runtime{}, err
	}
	bin := strings.TrimSpace(os.Getenv(EnvBinary))
	if bin == "" && strings.TrimSpace(envKind) == "" {
		bin = strings.TrimSpace(cfg.Binary)
		if bin == "" {
			bin = strings.TrimSpace(cfg.Bin)
		}
	}
	if bin == "" {
		bin = defaultBinary(kind)
	}
	return Runtime{Kind: kind, Binary: bin}, nil
}

func loadRuntimeConfig(configPath string) (runtimeConfig, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return runtimeConfig{}, nil
	}
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return runtimeConfig{}, nil
		}
		return runtimeConfig{}, err
	}
	var cfg configFile
	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return runtimeConfig{}, fmt.Errorf("%s: %w", configPath, err)
	}
	return cfg.Runtime, nil
}

func ParseKind(value string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(KindClaude), "claude-compatible":
		return KindClaude, nil
	case string(KindCodex):
		return KindCodex, nil
	default:
		return "", fmt.Errorf("%s must be %q or %q", EnvRuntime, KindClaude, KindCodex)
	}
}

func Binary() (string, error) {
	rt, err := Current()
	if err != nil {
		return "", err
	}
	return rt.Binary, nil
}

func ClaudeCompatibleBinary() (string, error) {
	rt, err := Current()
	if err != nil {
		return "", err
	}
	if rt.Kind != KindClaude {
		return "", fmt.Errorf("runtime %q is not Claude-compatible; set %s=%s or use direct run mode", rt.Kind, EnvRuntime, KindClaude)
	}
	return rt.Binary, nil
}

func defaultBinary(kind Kind) string {
	if kind == KindCodex {
		return "codex"
	}
	return DefaultBinary
}

// DefaultBinaryForKind returns the built-in binary name for a runtime kind.
func DefaultBinaryForKind(kind Kind) string {
	return defaultBinary(kind)
}

// CodexAgentTeamEnvConfigArgs returns Codex -c overrides that expose the
// daemon/session contract to shell commands without broadly inheriting the
// parent process environment.
func CodexAgentTeamEnvConfigArgs(env []string) []string {
	args := []string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || !strings.HasPrefix(key, "AGENT_TEAM_") || !validEnvKey(key) {
			continue
		}
		args = append(args, "-c", "shell_environment_policy.set."+key+"="+strconv.Quote(value))
	}
	return args
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
