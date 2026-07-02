package worktreepolicy

import (
	"fmt"
	"strings"
)

const (
	Never   = "never"
	OnClose = "on_close"
	OnMerge = "on_merge"
)

// Normalize returns the canonical worktree reap policy. Empty input defaults to
// Never so older jobs and topology files remain opt-in.
func Normalize(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return Never, nil
	}
	switch value {
	case Never, OnClose, OnMerge:
		return value, nil
	default:
		return "", fmt.Errorf("reap_worktree must be %s, %s, or %s", OnClose, OnMerge, Never)
	}
}

func Valid(raw string) bool {
	_, err := Normalize(raw)
	return err == nil
}

func ShouldReap(policy, trigger string) bool {
	policy, err := Normalize(policy)
	if err != nil {
		return false
	}
	trigger = strings.ToLower(strings.TrimSpace(trigger))
	switch policy {
	case OnClose:
		return trigger == OnClose || trigger == OnMerge
	case OnMerge:
		return trigger == OnMerge
	default:
		return false
	}
}
