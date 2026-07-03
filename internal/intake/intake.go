package intake

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Event is the normalized event sent into the topology resolver.
type Event struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

func NormalizeLinear(body []byte) (*Event, error) {
	raw, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	data := object(raw["data"])
	if len(data) == 0 {
		data = raw
	}
	action := firstString(raw, "action", "type", "webhookAction")
	eventType := linearEventType(action, raw, data)
	payload := map[string]any{
		"source": "linear",
		"action": action,
	}
	copyIf(payload, "ticket", firstNestedString(data, []string{"identifier"}, []string{"id"}))
	copyIf(payload, "ticket_id", firstNestedString(data, []string{"id"}))
	copyIf(payload, "ticket_url", firstNestedString(data, []string{"url"}, []string{"appUrl"}))
	copyIf(payload, "title", firstNestedString(data, []string{"title"}, []string{"name"}))
	copyIf(payload, "team", firstNestedString(data, []string{"team", "key"}, []string{"team", "name"}))
	copyIf(payload, "project", firstNestedString(data, []string{"project", "name"}, []string{"project", "id"}))
	copyIf(payload, "status", firstNestedString(data, []string{"state", "name"}, []string{"status", "name"}))
	copyIf(payload, "description", firstNestedString(data, []string{"description"}))
	copyIf(payload, "actor_id", firstNestedString(raw,
		[]string{"actor", "id"},
		[]string{"actorId"},
		[]string{"actor_id"},
		[]string{"user", "id"},
		[]string{"updatedBy", "id"},
		[]string{"createdBy", "id"},
	))
	copyIf(payload, "actor_name", firstNestedString(raw,
		[]string{"actor", "name"},
		[]string{"user", "name"},
		[]string{"updatedBy", "name"},
		[]string{"createdBy", "name"},
	))
	copyIf(payload, "actor_email", firstNestedString(raw,
		[]string{"actor", "email"},
		[]string{"user", "email"},
		[]string{"updatedBy", "email"},
		[]string{"createdBy", "email"},
	))
	return &Event{Type: eventType, Payload: payload}, nil
}

func NormalizeGitHub(body []byte) (*Event, error) {
	raw, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	action := firstString(raw, "action")
	pr := object(raw["pull_request"])
	issue := object(raw["issue"])
	issuePR := object(issue["pull_request"])
	repo := object(raw["repository"])
	comment := object(raw["comment"])
	eventType := githubEventType(action, pr, issue, comment)
	payload := map[string]any{
		"source": "github",
		"action": action,
	}
	prNumber := firstNestedString(pr, []string{"number"})
	if prNumber == "" && len(issuePR) > 0 {
		prNumber = firstNestedString(issue, []string{"number"})
	}
	prURL := firstNestedString(pr, []string{"html_url"}, []string{"url"})
	if prURL == "" && len(issuePR) > 0 {
		prURL = firstNestedString(issuePR, []string{"html_url"}, []string{"url"})
	}
	title := firstNestedString(pr, []string{"title"}, []string{"name"})
	if title == "" && len(issuePR) > 0 {
		title = firstNestedString(issue, []string{"title"}, []string{"name"})
	}
	copyIf(payload, "repository", firstNestedString(repo, []string{"full_name"}, []string{"name"}))
	copyIf(payload, "pr", prNumber)
	copyIf(payload, "pr_url", prURL)
	copyIf(payload, "title", title)
	copyIf(payload, "branch", firstNestedString(pr, []string{"head", "ref"}))
	copyIf(payload, "base", firstNestedString(pr, []string{"base", "ref"}))
	copyIf(payload, "comment_url", firstNestedString(comment, []string{"html_url"}, []string{"url"}))
	copyIf(payload, "issue", firstNestedString(issue, []string{"number"}))
	if merged, ok := pr["merged"].(bool); ok {
		payload["merged"] = merged
	}
	return &Event{Type: eventType, Payload: payload}, nil
}

func decodeObject(body []byte) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("intake: invalid JSON: %w", err)
	}
	if raw == nil {
		return nil, errors.New("intake: JSON object is required")
	}
	return raw, nil
}

func linearEventType(action string, raw, data map[string]any) string {
	a := strings.ToLower(action)
	switch {
	case strings.Contains(a, "comment"):
		return "ticket.commented"
	case strings.Contains(a, "status"), strings.Contains(a, "state"):
		return "ticket.status_changed"
	case strings.Contains(a, "create"):
		return "ticket.created"
	case strings.Contains(a, "update"):
		if object(data["state"]) != nil || object(data["status"]) != nil {
			return "ticket.status_changed"
		}
		return "ticket.updated"
	}
	if strings.EqualFold(firstString(raw, "type"), "Issue") {
		return "ticket.updated"
	}
	return "linear." + strings.Trim(strings.ReplaceAll(a, " ", "_"), "_")
}

func githubEventType(action string, pr, issue, comment map[string]any) string {
	a := strings.ToLower(action)
	if len(pr) > 0 {
		if a == "closed" {
			if merged, _ := pr["merged"].(bool); merged {
				return "pr.merged"
			}
			return "pr.closed"
		}
		switch a {
		case "opened", "reopened", "synchronize":
			return "pr." + a
		case "review_requested":
			return "pr.review_requested"
		}
		return "pr." + strings.Trim(strings.ReplaceAll(a, " ", "_"), "_")
	}
	if len(comment) > 0 && len(issue) > 0 {
		if _, ok := issue["pull_request"]; ok {
			return "pr.commented"
		}
		return "issue.commented"
	}
	if a == "" {
		return "github.event"
	}
	return "github." + strings.Trim(strings.ReplaceAll(a, " ", "_"), "_")
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			return stringify(value)
		}
	}
	return ""
}

func firstNestedString(m map[string]any, paths ...[]string) string {
	for _, path := range paths {
		cur := m
		for i, key := range path {
			value, ok := cur[key]
			if !ok {
				cur = nil
				break
			}
			if i == len(path)-1 {
				if got := stringify(value); got != "" {
					return got
				}
				break
			}
			cur = object(value)
			if cur == nil {
				break
			}
		}
	}
	return ""
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprint(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(x)
	}
}

func copyIf(dst map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		dst[key] = value
	}
}
