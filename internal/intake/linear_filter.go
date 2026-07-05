package intake

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	teamtemplate "github.com/agent-team-project/agent-team/internal/template"
)

const LinearSelfStatusChangeReason = "self-authored Linear status change ignored"
const LinearLoopProtectionUnavailableReason = "Linear column trigger loop protection unavailable; set linear.agent_user_id or ensure the Linear API key can resolve viewer { id }"
const GitHubSelfStatusChangeReason = "self-authored GitHub project status change ignored"
const GitHubLoopProtectionUnavailableReason = "GitHub project trigger loop protection unavailable; set github.agent_login or github.agent_id, or ensure the GitHub token can resolve the viewer"

// LinearSelfStatusChange reports whether a normalized Linear status-change
// event was authored by the configured agent user. The agent user id comes
// from `.agent_team/config.toml` (`linear.agent_user_id`) or a cached viewer
// query at `.agent_team/state/linear/viewer.json`.
func LinearSelfStatusChange(teamDir string, ev *Event) (bool, string) {
	return LinearSelfStatusChangeForUser(ev, LinearAgentUserID(teamDir))
}

// ResolveLinearAgentUserID returns the configured or cached Linear API user id.
// If neither exists, it resolves `viewer { id }` through the bundled Linear
// skill helper and writes `.agent_team/state/linear/viewer.json` for reuse.
func ResolveLinearAgentUserID(teamDir string) (string, error) {
	if id := configuredLinearAgentUserID(teamDir); id != "" {
		return id, nil
	}
	if id := cachedLinearViewerID(teamDir); id != "" {
		return id, nil
	}
	id, err := resolveLinearViewerID(teamDir)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("Linear viewer query did not return an id")
	}
	if err := writeLinearViewerCache(teamDir, id); err != nil {
		return "", err
	}
	return id, nil
}

// LinearSelfStatusChangeForUser is the pure comparison used by tests and
// callers that already resolved the Linear API key's viewer id.
func LinearSelfStatusChangeForUser(ev *Event, agentUserID string) (bool, string) {
	if ev == nil || ev.Type != "ticket.status_changed" {
		return false, ""
	}
	agentUserID = strings.TrimSpace(agentUserID)
	if agentUserID == "" {
		return false, ""
	}
	actorID := strings.TrimSpace(firstString(ev.Payload, "actor_id"))
	if actorID == "" || actorID != agentUserID {
		return false, ""
	}
	return true, LinearSelfStatusChangeReason
}

func GitHubSelfStatusChangeForActor(ev *Event, actorID string) (bool, string) {
	if ev == nil || ev.Type != "ticket.status_changed" {
		return false, ""
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return false, ""
	}
	for _, key := range []string{"actor_login", "actor_id"} {
		if got := strings.TrimSpace(firstString(ev.Payload, key)); got != "" && got == actorID {
			return true, GitHubSelfStatusChangeReason
		}
	}
	return false, ""
}

func LinearAgentUserID(teamDir string) string {
	if id := configuredLinearAgentUserID(teamDir); id != "" {
		return id
	}
	return cachedLinearViewerID(teamDir)
}

func configuredLinearAgentUserID(teamDir string) string {
	teamDir = strings.TrimSpace(teamDir)
	if teamDir == "" {
		return ""
	}
	cfg, err := teamtemplate.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err == nil {
		if value, ok := cfg.GetDotted("linear.agent_user_id"); ok {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func cachedLinearViewerID(teamDir string) string {
	body, err := os.ReadFile(filepath.Join(teamDir, "state", "linear", "viewer.json"))
	if err != nil {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return ""
	}
	return strings.TrimSpace(firstNestedString(raw,
		[]string{"id"},
		[]string{"viewer", "id"},
		[]string{"data", "viewer", "id"},
	))
}

func resolveLinearViewerID(teamDir string) (string, error) {
	teamDir = strings.TrimSpace(teamDir)
	if teamDir == "" {
		return "", fmt.Errorf("team dir is required")
	}
	script := filepath.Join(teamDir, "skills", "linear", "scripts", "linear-graphql.sh")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("Linear helper unavailable: %w", err)
	}
	cmd := exec.Command(script, "query { viewer { id } }")
	cmd.Dir = filepath.Dir(teamDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return "", fmt.Errorf("Linear viewer query failed: %w", err)
		}
		return "", fmt.Errorf("Linear viewer query failed: %w: %s", err, detail)
	}
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return "", fmt.Errorf("decode Linear viewer response: %w", err)
	}
	if errorsValue, ok := raw["errors"]; ok {
		return "", fmt.Errorf("Linear viewer query returned errors: %v", errorsValue)
	}
	return strings.TrimSpace(firstNestedString(raw,
		[]string{"data", "viewer", "id"},
		[]string{"viewer", "id"},
		[]string{"id"},
	)), nil
}

func writeLinearViewerCache(teamDir, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("Linear viewer id is required")
	}
	dir := filepath.Join(teamDir, "state", "linear")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write Linear viewer cache: %w", err)
	}
	body, err := json.MarshalIndent(map[string]any{
		"id":        id,
		"cached_at": time.Now().UTC().Format(time.RFC3339),
		"source":    "linear.viewer",
	}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := filepath.Join(dir, "viewer.json.tmp")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write Linear viewer cache: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "viewer.json")); err != nil {
		return fmt.Errorf("write Linear viewer cache: %w", err)
	}
	return nil
}
