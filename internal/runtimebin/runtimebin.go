package runtimebin

import (
	"fmt"
	"os"
	"strings"
)

type Kind string

const (
	KindClaude Kind = "claude"
	KindCodex  Kind = "codex"

	DefaultBinary  = "claude"
	DefaultRuntime = KindClaude
	EnvRuntime     = "AGENT_TEAM_RUNTIME"
	EnvBinary      = "AGENT_TEAM_RUNTIME_BIN"
)

type Runtime struct {
	Kind   Kind
	Binary string
}

func Current() (Runtime, error) {
	kind, err := ParseKind(os.Getenv(EnvRuntime))
	if err != nil {
		return Runtime{}, err
	}
	bin := strings.TrimSpace(os.Getenv(EnvBinary))
	if bin == "" {
		bin = defaultBinary(kind)
	}
	return Runtime{Kind: kind, Binary: bin}, nil
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
