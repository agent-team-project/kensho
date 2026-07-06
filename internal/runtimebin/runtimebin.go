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

// Fields is a runtime kind/binary pair from a higher-level declaration, such
// as a dispatch payload, declared topology instance, or agent frontmatter.
type Fields struct {
	Kind   string
	Binary string
	Name   string
}

// ResolveOptions describes the full runtime precedence stack:
//
//	explicit runtime fields
//	  > AGENT_TEAM_RUNTIME env override
//	  > declared instance runtime fields
//	  > agent frontmatter runtime fields
//	  > repo [runtime] config
//	  > built-in default
//
// Explicit.Binary also acts as a binary-only override for the fallback
// env/config/default runtime, matching the CLI --runtime-bin behavior.
type ResolveOptions struct {
	Explicit   Fields
	Instance   Fields
	Agent      Fields
	ConfigPath string
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

// Resolve applies the topology-aware runtime precedence used by dispatch,
// reconcile, and lifecycle start paths.
func Resolve(opts ResolveOptions) (Runtime, error) {
	kindRaw := opts.Explicit.Kind
	binRaw := opts.Explicit.Binary
	if rt, ok, err := FromFields(kindRaw, binRaw); err != nil {
		return Runtime{}, fmt.Errorf("runtime must be %q or %q", KindClaude, KindCodex)
	} else if ok {
		return rt, nil
	}

	// A deliberate runtime env override outranks static topology/agent
	// defaults. Binary-only env overrides are handled by CurrentFromConfig
	// when resolution falls through to repo/default runtime selection.
	if strings.TrimSpace(os.Getenv(EnvRuntime)) == "" {
		if rt, ok, err := FromFields(opts.Instance.Kind, opts.Instance.Binary); err != nil {
			return Runtime{}, fmt.Errorf("instance %q runtime: %w", opts.Instance.Name, err)
		} else if ok {
			return rt, nil
		}
		if rt, ok, err := FromFields(opts.Agent.Kind, opts.Agent.Binary); err != nil {
			return Runtime{}, fmt.Errorf("agent %q runtime: %w", opts.Agent.Name, err)
		} else if ok {
			return rt, nil
		}
	}

	rt, err := CurrentFromConfig(opts.ConfigPath)
	if err != nil {
		return Runtime{}, err
	}
	if bin := strings.TrimSpace(binRaw); bin != "" {
		rt.Binary = bin
	}
	if strings.TrimSpace(rt.Binary) == "" {
		rt.Binary = defaultBinary(rt.Kind)
	}
	return rt, nil
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

// FromFields builds a Runtime from explicit kind/binary strings — for example
// an agent's frontmatter `runtime:`/`runtime_bin:`, a pipeline step, or a
// dispatch payload. ok is false when kindRaw is blank, letting callers fall
// through to the next resolution source. A non-nil error means kindRaw was set
// but not a recognised runtime.
func FromFields(kindRaw, binRaw string) (Runtime, bool, error) {
	if strings.TrimSpace(kindRaw) == "" {
		return Runtime{}, false, nil
	}
	kind, err := ParseKind(kindRaw)
	if err != nil {
		return Runtime{}, false, err
	}
	bin := strings.TrimSpace(binRaw)
	if bin == "" {
		bin = defaultBinary(kind)
	}
	return Runtime{Kind: kind, Binary: bin}, true, nil
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

// CodexAutonomousExecArgs are the `codex exec` flags that let a daemon-spawned
// worker actually do its job. `codex exec` defaults to a read-only,
// network-disabled sandbox, which makes an autonomous worker a no-op — it
// cannot write files, reach the network (Linear / GitHub), build (write
// ~/.cargo), or push. The daemon exists to run autonomous agents on a trusted,
// operator-controlled machine, so it bypasses the in-process sandbox;
// isolation comes from the per-worker git worktree, not from Codex's sandbox.
func CodexAutonomousExecArgs() []string {
	return []string{"--dangerously-bypass-approvals-and-sandbox"}
}

// CodexAgentTeamEnvConfigArgs returns Codex -c overrides that expose the
// daemon/session contract to shell commands without broadly inheriting the
// parent process environment. PATH is also allowed so launchers can make
// generated per-session command shims visible to Codex shell commands.
func CodexAgentTeamEnvConfigArgs(env []string) []string {
	args := []string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || !codexAllowedEnvKey(key) {
			continue
		}
		args = append(args, "-c", "shell_environment_policy.set."+key+"="+strconv.Quote(value))
	}
	return args
}

func codexAllowedEnvKey(key string) bool {
	return (key == "PATH" || strings.HasPrefix(key, "AGENT_TEAM_")) && validEnvKey(key)
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
