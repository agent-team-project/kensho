package linearwriteback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
)

func TestWriteBackSkipsWhenLinearDisabled(t *testing.T) {
	teamDir := testTeamDir(t, `[team]
pm_tool = "none"
`)
	j := testJob()
	client := &Client{APIKey: "unused"}
	result := client.WriteBack(context.Background(), teamDir, Request{Action: ActionDispatchInProgress, Job: j, Actor: "test"})
	if !result.Skipped || result.Error != "" {
		t.Fatalf("result = %+v, want skipped without error", result)
	}
	events, err := job.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "linear_writeback_skipped" || events[0].Message != "Linear not configured for this repo" || events[0].Data["action"] != string(ActionDispatchInProgress) {
		t.Fatalf("events = %+v, want skipped audit for disabled Linear", events)
	}
}

func TestWriteBackMissingAPIKeyAuditsSkip(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("LINEAR_USER_API_KEY", "")
	teamDir := testLinearTeamDir(t, `in_progress_state = "In Progress"`)
	j := testJob()
	client := &Client{}

	result := client.WriteBack(context.Background(), teamDir, Request{Action: ActionDispatchInProgress, Job: j, Actor: "test"})
	if !result.Skipped || result.Message != errNoAPIKey.Error() || result.State != "In Progress" || result.AuditErr != nil {
		t.Fatalf("result = %+v, want skipped missing-key audit", result)
	}
	events, err := job.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "linear_writeback_skipped" || events[0].Message != errNoAPIKey.Error() || events[0].Data["action"] != string(ActionDispatchInProgress) || events[0].Data["state"] != "In Progress" {
		t.Fatalf("events = %+v, want skipped missing-key audit", events)
	}
}

func TestWriteBackDispatchMovesToInProgress(t *testing.T) {
	teamDir := testLinearTeamDir(t, `in_progress_state = "In Progress"`)
	server, requests := linearTestServer(t)
	defer server.Close()
	j := testJob()
	client := &Client{Endpoint: server.URL, APIKey: "linear-key", HTTPClient: server.Client()}

	result := client.WriteBack(context.Background(), teamDir, Request{Action: ActionDispatchInProgress, Job: j, Actor: "test"})
	if result.Error != "" || !result.Changed || result.State != "In Progress" || result.Comment {
		t.Fatalf("result = %+v, want state update only", result)
	}
	if got := len(*requests); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	if auth := (*requests)[0].Authorization; auth != "linear-key" {
		t.Fatalf("authorization header = %q", auth)
	}
	events, err := job.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "linear_writeback" || events[0].Data["state"] != "In Progress" {
		t.Fatalf("events = %+v", events)
	}
}

func TestWriteBackBounceMovesAndCommentsWithFindings(t *testing.T) {
	teamDir := testLinearTeamDir(t, `in_progress_state = "In Progress"`)
	server, requests := linearTestServer(t)
	defer server.Close()
	j := testJob()
	client := &Client{Endpoint: server.URL, APIKey: "linear-key", HTTPClient: server.Client()}

	result := client.WriteBack(context.Background(), teamDir, Request{
		Action:   ActionBounceBack,
		Job:      j,
		StepID:   "implement",
		Findings: "missing test coverage",
		Actor:    "test",
	})
	if result.Error != "" || !result.Changed || result.State != "In Progress" || !result.Comment {
		t.Fatalf("result = %+v, want state update and comment", result)
	}
	comment := commentBodyFromRequests(t, *requests)
	for _, want := range []string{"Job squ-68 was bounced back", "implement", "missing test coverage"} {
		if !strings.Contains(comment, want) {
			t.Fatalf("comment = %q, missing %q", comment, want)
		}
	}
}

func TestWriteBackFailureMovesToAttentionAndComments(t *testing.T) {
	teamDir := testLinearTeamDir(t, `attention_state = "Todo"`)
	server, requests := linearTestServer(t)
	defer server.Close()
	j := testJob()
	j.Status = job.StatusFailed
	j.LastStatus = "instance crashed"
	client := &Client{Endpoint: server.URL, APIKey: "linear-key", HTTPClient: server.Client()}

	result := client.WriteBack(context.Background(), teamDir, Request{
		Action:  ActionFailureAttention,
		Job:     j,
		Message: "instance crashed",
		Actor:   "test",
	})
	if result.Error != "" || !result.Changed || result.State != "Todo" || !result.Comment {
		t.Fatalf("result = %+v, want attention state and comment", result)
	}
	comment := commentBodyFromRequests(t, *requests)
	if !strings.Contains(comment, "Job squ-68 failed and needs attention: instance crashed") {
		t.Fatalf("comment = %q", comment)
	}
}

func TestWriteBackFailureLabelsWhenAttentionStateMissing(t *testing.T) {
	teamDir := testLinearTeamDir(t, `labels = ["needs-attention"]`)
	server, requests := linearTestServer(t)
	defer server.Close()
	j := testJob()
	j.Status = job.StatusFailed
	j.LastStatus = "worker crashed"
	client := &Client{Endpoint: server.URL, APIKey: "linear-key", HTTPClient: server.Client()}

	result := client.WriteBack(context.Background(), teamDir, Request{
		Action:  ActionFailureAttention,
		Job:     j,
		Message: "worker crashed",
		Actor:   "test",
	})
	if result.Error != "" || !result.Changed || result.State != "" || result.Labels != "needs-attention" || !result.Comment {
		t.Fatalf("result = %+v, want label fallback and comment", result)
	}
	if !requestAddedLabel(t, *requests, "label-attention") {
		t.Fatalf("requests = %+v, missing label fallback", *requests)
	}
	comment := commentBodyFromRequests(t, *requests)
	if !strings.Contains(comment, "Job squ-68 failed and needs attention: worker crashed") {
		t.Fatalf("comment = %q", comment)
	}
}

func testTeamDir(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return teamDir
}

func testLinearTeamDir(t *testing.T, extra string) string {
	t.Helper()
	return testTeamDir(t, `[team]
pm_tool = "linear"

[linear]
team_id = "team-1"
ticket_prefix = "SQU"
`+extra+"\n")
}

func testJob() *job.Job {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	return &job.Job{
		ID:        "squ-68",
		Ticket:    "SQU-68",
		Target:    "worker",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

type linearRequest struct {
	Query         string
	Variables     map[string]any
	Authorization string
}

func linearTestServer(t *testing.T) (*httptest.Server, *[]linearRequest) {
	t.Helper()
	var requests []linearRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, linearRequest{
			Query:         body.Query,
			Variables:     body.Variables,
			Authorization: r.Header.Get("Authorization"),
		})
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "workflowStates"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"id":"issue-1","identifier":"SQU-68","url":"https://linear.app/squirtlesquad/issue/SQU-68/test","labels":{"nodes":[{"id":"label-agent","name":"agent-work"}]}},"workflowStates":{"nodes":[{"id":"state-progress","name":"In Progress"},{"id":"state-todo","name":"Todo"}]},"issueLabels":{"nodes":[{"id":"label-agent","name":"agent-work"},{"id":"label-attention","name":"needs-attention"}]}}}`))
		case strings.Contains(body.Query, "issueUpdate"):
			input, _ := body.Variables["input"].(map[string]any)
			if input["stateId"] == "" && input["labelIds"] == nil {
				t.Fatalf("issueUpdate missing stateId or labelIds: %+v", body.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true,"issue":{"identifier":"SQU-68","state":{"name":"ok"}}}}}`))
		case strings.Contains(body.Query, "commentCreate"):
			input, _ := body.Variables["input"].(map[string]any)
			if strings.TrimSpace(input["body"].(string)) == "" {
				t.Fatalf("commentCreate missing body: %+v", body.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"commentCreate":{"success":true,"comment":{"id":"comment-1","url":"https://linear.app/comment"}}}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	return server, &requests
}

func commentBodyFromRequests(t *testing.T, requests []linearRequest) string {
	t.Helper()
	for _, req := range requests {
		if !strings.Contains(req.Query, "commentCreate") {
			continue
		}
		input, ok := req.Variables["input"].(map[string]any)
		if !ok {
			t.Fatalf("comment variables = %+v, missing input", req.Variables)
		}
		body, ok := input["body"].(string)
		if !ok {
			t.Fatalf("comment input = %+v, missing body", input)
		}
		return body
	}
	t.Fatalf("commentCreate request not found in %+v", requests)
	return ""
}

func requestAddedLabel(t *testing.T, requests []linearRequest, labelID string) bool {
	t.Helper()
	for _, req := range requests {
		if !strings.Contains(req.Query, "issueUpdate") {
			continue
		}
		input, ok := req.Variables["input"].(map[string]any)
		if !ok {
			t.Fatalf("issueUpdate variables = %+v, missing input", req.Variables)
		}
		values, ok := input["labelIds"].([]any)
		if !ok {
			continue
		}
		for _, value := range values {
			if value == labelID {
				return true
			}
		}
	}
	return false
}
