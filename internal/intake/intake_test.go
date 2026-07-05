package intake

import "testing"

func TestNormalizeLinearIssueCreated(t *testing.T) {
	ev, err := NormalizeLinear([]byte(`{
  "action": "Issue created",
  "data": {
    "id": "issue-id",
    "identifier": "SQU-100",
    "title": "Add intake",
    "url": "https://linear.app/squirtlesquad/issue/SQU-100/add-intake",
    "team": {"key": "SQU"},
    "project": {"name": "Agent Team"},
    "state": {"name": "Todo"}
  }
}`))
	if err != nil {
		t.Fatalf("NormalizeLinear: %v", err)
	}
	if ev.Type != "ticket.created" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Payload["ticket"] != "SQU-100" || ev.Payload["team"] != "SQU" || ev.Payload["status"] != "Todo" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
}

func TestNormalizeLinearStatusChangedIncludesActor(t *testing.T) {
	ev, err := NormalizeLinear([]byte(`{
  "action": "Issue updated",
  "actor": {"id": "agent-user", "name": "Agent User", "email": "agent@example.com"},
  "data": {
    "id": "issue-id",
    "identifier": "SQU-101",
    "title": "Dispatch me",
    "state": {"name": "Ready for Agent"}
  }
}`))
	if err != nil {
		t.Fatalf("NormalizeLinear: %v", err)
	}
	if ev.Type != "ticket.status_changed" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Payload["status"] != "Ready for Agent" || ev.Payload["actor_id"] != "agent-user" || ev.Payload["actor_name"] != "Agent User" || ev.Payload["actor_email"] != "agent@example.com" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
}

func TestLinearSelfStatusChangeForUser(t *testing.T) {
	ev := &Event{Type: "ticket.status_changed", Payload: map[string]any{"actor_id": "agent-user"}}
	if ignored, reason := LinearSelfStatusChangeForUser(ev, "agent-user"); !ignored || reason != LinearSelfStatusChangeReason {
		t.Fatalf("self status change = %v %q, want ignored reason", ignored, reason)
	}
	if ignored, reason := LinearSelfStatusChangeForUser(ev, "other-user"); ignored || reason != "" {
		t.Fatalf("other actor = %v %q, want not ignored", ignored, reason)
	}
	if ignored, reason := LinearSelfStatusChangeForUser(&Event{Type: "ticket.updated", Payload: ev.Payload}, "agent-user"); ignored || reason != "" {
		t.Fatalf("non-status event = %v %q, want not ignored", ignored, reason)
	}
}

func TestNormalizeGitHubProjectStatusChanged(t *testing.T) {
	ev, err := NormalizeGitHub([]byte(`{
  "action": "edited",
  "sender": {"id": 1234, "login": "human-user"},
  "repository": {"full_name": "acme/widgets"},
  "projects_v2_item": {
    "content_url": "https://api.github.com/repos/acme/widgets/issues/42",
    "content": {"number": 42, "title": "Dispatch me", "html_url": "https://github.com/acme/widgets/issues/42"},
    "project": {"title": "Delivery"}
  },
  "changes": {
    "field_value": {
      "field_name": "Status",
      "from": {"name": "Todo"},
      "to": {"name": "Ready for Agent"}
    }
  }
}`))
	if err != nil {
		t.Fatalf("NormalizeGitHub: %v", err)
	}
	if ev.Type != "ticket.status_changed" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Payload["source"] != "github" || ev.Payload["status"] != "Ready for Agent" || ev.Payload["previous_status"] != "Todo" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
	if ev.Payload["ticket"] != "42" || ev.Payload["ticket_url"] != "https://github.com/acme/widgets/issues/42" || ev.Payload["repository"] != "acme/widgets" || ev.Payload["actor_login"] != "human-user" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
}

func TestGitHubSelfStatusChangeForActor(t *testing.T) {
	ev := &Event{Type: "ticket.status_changed", Payload: map[string]any{"actor_login": "agent-bot", "actor_id": "1234"}}
	if ignored, reason := GitHubSelfStatusChangeForActor(ev, "agent-bot"); !ignored || reason != GitHubSelfStatusChangeReason {
		t.Fatalf("self login = %v %q, want ignored reason", ignored, reason)
	}
	if ignored, reason := GitHubSelfStatusChangeForActor(ev, "1234"); !ignored || reason != GitHubSelfStatusChangeReason {
		t.Fatalf("self id = %v %q, want ignored reason", ignored, reason)
	}
	if ignored, reason := GitHubSelfStatusChangeForActor(ev, "human-user"); ignored || reason != "" {
		t.Fatalf("other actor = %v %q, want not ignored", ignored, reason)
	}
}

func TestNormalizeGitHubPRMerged(t *testing.T) {
	ev, err := NormalizeGitHub([]byte(`{
  "action": "closed",
  "repository": {"full_name": "agent-team-project/agent-team"},
  "pull_request": {
    "number": 42,
    "title": "Add queue",
    "html_url": "https://github.com/agent-team-project/agent-team/pull/42",
    "merged": true,
    "head": {"ref": "worktree-worker-squ-42"},
    "base": {"ref": "main"}
  }
}`))
	if err != nil {
		t.Fatalf("NormalizeGitHub: %v", err)
	}
	if ev.Type != "pr.merged" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Payload["pr"] != "42" || ev.Payload["repository"] != "agent-team-project/agent-team" || ev.Payload["merged"] != true {
		t.Fatalf("payload = %+v", ev.Payload)
	}
}

func TestNormalizeGitHubPRComment(t *testing.T) {
	ev, err := NormalizeGitHub([]byte(`{
  "action": "created",
  "repository": {"full_name": "acme/repo"},
  "issue": {
    "number": 109,
    "title": "Review implementation",
    "pull_request": {
      "html_url": "https://github.com/acme/repo/pull/109",
      "url": "https://api.github.com/repos/acme/repo/pulls/109"
    }
  },
  "comment": {
    "html_url": "https://github.com/acme/repo/pull/109#issuecomment-1"
  }
}`))
	if err != nil {
		t.Fatalf("NormalizeGitHub: %v", err)
	}
	if ev.Type != "pr.commented" {
		t.Fatalf("type = %q", ev.Type)
	}
	if ev.Payload["pr"] != "109" || ev.Payload["pr_url"] != "https://github.com/acme/repo/pull/109" || ev.Payload["issue"] != "109" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
	if ev.Payload["title"] != "Review implementation" || ev.Payload["comment_url"] != "https://github.com/acme/repo/pull/109#issuecomment-1" {
		t.Fatalf("payload = %+v", ev.Payload)
	}
}
