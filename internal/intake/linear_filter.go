package intake

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	teamtemplate "github.com/jamesaud/agent-team/internal/template"
)

const LinearSelfStatusChangeReason = "self-authored Linear status change ignored"

// LinearSelfStatusChange reports whether a normalized Linear status-change
// event was authored by the configured agent user. The agent user id comes
// from `.agent_team/config.toml` (`linear.agent_user_id`) or a cached viewer
// query at `.agent_team/state/linear/viewer.json`.
func LinearSelfStatusChange(teamDir string, ev *Event) (bool, string) {
	return LinearSelfStatusChangeForUser(ev, LinearAgentUserID(teamDir))
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

func LinearAgentUserID(teamDir string) string {
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
	return cachedLinearViewerID(teamDir)
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
