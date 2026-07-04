package pmprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
)

func TestGitHubWriteBackMissingTokenAuditsSkip(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	teamDir := testGitHubTeamDir(t, `in_progress_state = "open"`)
	j := testGitHubJob()
	client := &GitHubClient{}

	result := client.WriteBack(context.Background(), teamDir, Request{Action: ActionDispatchInProgress, Job: j, Actor: "test"})
	if !result.Skipped || result.Message != errNoGitHubToken.Error() || result.Issue != "acme/widgets#42" || result.AuditErr != nil {
		t.Fatalf("result = %+v, want skipped missing-token audit", result)
	}
	events, err := job.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "github_writeback_skipped" || events[0].Message != errNoGitHubToken.Error() || events[0].Data["action"] != string(ActionDispatchInProgress) {
		t.Fatalf("events = %+v, want skipped GitHub audit", events)
	}
}

func TestGitHubWriteBackDispatchMovesIssueAndProject(t *testing.T) {
	teamDir := testGitHubTeamDir(t, `in_progress_state = "open"
in_progress_label = "agent-work"
project_owner = "acme"
project_number = 7
project_status_field = "Status"
in_progress_column = "In Progress"
`)
	server, requests := githubTestServer(t)
	defer server.Close()
	j := testGitHubJob()
	client := &GitHubClient{
		RESTEndpoint:    server.URL,
		GraphQLEndpoint: server.URL + "/graphql",
		Token:           "gh-key",
		HTTPClient:      server.Client(),
	}

	result := client.WriteBack(context.Background(), teamDir, Request{Action: ActionDispatchInProgress, Job: j, Actor: "test"})
	if result.Error != "" || !result.Changed || result.State != "open" || result.Labels != "agent-work" || result.Project != "acme#7" || result.ProjectStatus != "In Progress" || result.Comment {
		t.Fatalf("result = %+v, want issue state, label, and project update", result)
	}
	if !githubRequestSeen(*requests, http.MethodPatch, "/repos/acme/widgets/issues/42", "open") {
		t.Fatalf("requests = %+v, missing issue state update", *requests)
	}
	if !githubRequestSeen(*requests, http.MethodPost, "/repos/acme/widgets/issues/42/labels", "agent-work") {
		t.Fatalf("requests = %+v, missing label update", *requests)
	}
	if !githubGraphQLSeen(*requests, "updateProjectV2ItemFieldValue", "option-progress") {
		t.Fatalf("requests = %+v, missing project status update", *requests)
	}
	events, err := job.ListEvents(teamDir, j.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Type != "github_writeback" || events[0].Data["project_status"] != "In Progress" || events[0].Data["labels"] != "agent-work" {
		t.Fatalf("events = %+v", events)
	}
}

func TestGitHubWriteBackBounceComments(t *testing.T) {
	teamDir := testGitHubTeamDir(t, `in_progress_state = "open"`)
	server, requests := githubTestServer(t)
	defer server.Close()
	j := testGitHubJob()
	client := &GitHubClient{RESTEndpoint: server.URL, Token: "gh-key", HTTPClient: server.Client()}

	result := client.WriteBack(context.Background(), teamDir, Request{
		Action:   ActionBounceBack,
		Job:      j,
		StepID:   "review",
		Findings: "missing project status test",
		Actor:    "test",
	})
	if result.Error != "" || !result.Changed || result.State != "open" || !result.Comment {
		t.Fatalf("result = %+v, want state update and comment", result)
	}
	if !githubRequestSeen(*requests, http.MethodPost, "/repos/acme/widgets/issues/42/comments", "missing project status test") {
		t.Fatalf("requests = %+v, missing bounce comment", *requests)
	}
}

func testGitHubTeamDir(t *testing.T, extra string) string {
	t.Helper()
	return testTeamDir(t, `[pm]
provider = "github"

[github]
owner = "acme"
repo = "widgets"
`+extra+"\n")
}

func testGitHubJob() *job.Job {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	return &job.Job{
		ID:        "github-42",
		Ticket:    "https://github.com/acme/widgets/issues/42",
		TicketURL: "https://github.com/acme/widgets/issues/42",
		Target:    "worker",
		Status:    job.StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

type githubRequest struct {
	Method        string
	Path          string
	Query         string
	Variables     map[string]any
	Body          string
	Authorization string
}

func githubTestServer(t *testing.T) (*httptest.Server, *[]githubRequest) {
	t.Helper()
	var requests []githubRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer gh-key" {
			t.Fatalf("authorization header = %q, want bearer token", auth)
		}
		if r.URL.Path == "/graphql" {
			var body struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql request: %v", err)
			}
			requests = append(requests, githubRequest{
				Method:        r.Method,
				Path:          r.URL.Path,
				Query:         body.Query,
				Variables:     body.Variables,
				Authorization: r.Header.Get("Authorization"),
			})
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(body.Query, "repository(owner"):
				_, _ = w.Write([]byte(`{"data":{"repository":{"issue":{"id":"issue-node","projectItems":{"nodes":[{"id":"item-1","project":{"id":"project-1"}}]}}},"organization":{"projectV2":{"id":"project-1","fields":{"nodes":[{"__typename":"ProjectV2SingleSelectField","id":"field-status","name":"Status","options":[{"id":"option-progress","name":"In Progress"}]}]}}},"user":{"projectV2":null}}}`))
			case strings.Contains(body.Query, "updateProjectV2ItemFieldValue"):
				if body.Variables["optionId"] != "option-progress" {
					t.Fatalf("graphql variables = %+v, want option-progress", body.Variables)
				}
				_, _ = w.Write([]byte(`{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"item-1"}}}}`))
			default:
				t.Fatalf("unexpected graphql query: %s", body.Query)
			}
			return
		}
		var raw map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&raw)
		}
		body, _ := json.Marshal(raw)
		requests = append(requests, githubRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Body:          string(body),
			Authorization: r.Header.Get("Authorization"),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	return server, &requests
}

func githubRequestSeen(requests []githubRequest, method, path, contains string) bool {
	for _, req := range requests {
		if req.Method == method && req.Path == path && strings.Contains(req.Body, contains) {
			return true
		}
	}
	return false
}

func githubGraphQLSeen(requests []githubRequest, queryContains, variableValue string) bool {
	for _, req := range requests {
		if req.Path != "/graphql" || !strings.Contains(req.Query, queryContains) {
			continue
		}
		for _, value := range req.Variables {
			if value == variableValue {
				return true
			}
		}
	}
	return false
}

func TestParseGitHubIssueRef(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "https://github.com/acme/widgets/issues/42", want: "acme/widgets#42"},
		{raw: "https://api.github.com/repos/acme/widgets/issues/42", want: "acme/widgets#42"},
		{raw: "acme/widgets#42", want: "acme/widgets#42"},
		{raw: "#42", want: "acme/widgets#42"},
	}
	for _, tt := range tests {
		ref, ok := parseGitHubIssueRef(tt.raw, "acme", "widgets")
		if !ok || ref.String() != tt.want {
			t.Fatalf("parseGitHubIssueRef(%q) = %+v/%v, want %s", tt.raw, ref, ok, tt.want)
		}
	}
}

func TestDecodeProviderConfigMissingFile(t *testing.T) {
	_, err := decodeProviderConfig(filepath.Join(t.TempDir(), ".agent_team"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decodeProviderConfig err = %v, want os.ErrNotExist", err)
	}
}
