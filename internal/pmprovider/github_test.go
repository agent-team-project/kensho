package pmprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/job"
)

func TestGitHubWriteBackMissingTokenAuditsSkip(t *testing.T) {
	t.Setenv("AGENT_TEAM_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("AGENT_TEAM_GITHUB_LOGIN", "")
	t.Setenv("PATH", t.TempDir())
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

func TestGitHubListOpenCommunityItemsAllowsTokenlessPublicRead(t *testing.T) {
	t.Setenv("AGENT_TEAM_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	teamDir := testGitHubTeamDir(t, ``)
	var gotAuth string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if r.URL.Query().Get("state") != "open" {
			t.Fatalf("query = %s, want state=open", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"number":42,"html_url":"https://github.com/acme/widgets/issues/42","title":"Crash","body":"Steps to reproduce: panic","state":"open","user":{"login":"alice"},"labels":[{"name":"bug"}]},
			{"number":43,"html_url":"https://github.com/acme/widgets/pull/43","title":"Add feature","body":"feature request","state":"open","user":{"login":"bob"},"pull_request":{"html_url":"https://github.com/acme/widgets/pull/43"}}
		]`))
	}))
	defer server.Close()
	client := &GitHubClient{RESTEndpoint: server.URL, HTTPClient: server.Client()}

	items, err := client.ListOpenCommunityItems(context.Background(), teamDir, GitHubCommunityListOptions{Limit: 10, IncludeIssues: true, IncludePullRequests: true})
	if err != nil {
		t.Fatalf("ListOpenCommunityItems: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want empty for tokenless public read", gotAuth)
	}
	if gotPath != "/repos/acme/widgets/issues" {
		t.Fatalf("path = %q, want issues list", gotPath)
	}
	if len(items) != 2 || items[0].Kind != "issue" || items[1].Kind != "pull_request" || items[1].Author != "bob" {
		t.Fatalf("items = %+v", items)
	}
}

func TestGitHubListOpenCommunityItemsUsesStableRawPaginationForFilteredIssues(t *testing.T) {
	t.Setenv("AGENT_TEAM_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	teamDir := testGitHubTeamDir(t, ``)
	records := githubCommunityRecords(10, 50)
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/issues" {
			t.Fatalf("path = %q, want issues list", r.URL.Path)
		}
		perPage, err := strconv.Atoi(r.URL.Query().Get("per_page"))
		if err != nil {
			t.Fatalf("per_page = %q, want integer", r.URL.Query().Get("per_page"))
		}
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			t.Fatalf("page = %q, want integer", r.URL.Query().Get("page"))
		}
		requests = append(requests, fmt.Sprintf("%d:%d", page, perPage))
		start := (page - 1) * perPage
		if start > len(records) {
			start = len(records)
		}
		end := start + perPage
		if end > len(records) {
			end = len(records)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[" + strings.Join(records[start:end], ",") + "]"))
	}))
	defer server.Close()
	client := &GitHubClient{RESTEndpoint: server.URL, HTTPClient: server.Client()}

	items, err := client.ListOpenCommunityItems(context.Background(), teamDir, GitHubCommunityListOptions{Limit: 20, IncludeIssues: true})
	if err != nil {
		t.Fatalf("ListOpenCommunityItems limit 20: %v", err)
	}
	if len(items) != 20 {
		t.Fatalf("len(items) = %d, want 20", len(items))
	}
	for i, item := range items {
		if want := 11 + i; item.Number != want {
			t.Fatalf("items[%d].Number = %d, want %d; items = %+v", i, item.Number, want, items)
		}
	}
	if got := strings.Join(requests, ","); got != "1:20,2:20" {
		t.Fatalf("requests = %s, want stable 20-item raw pages", got)
	}

	requests = nil
	items, err = client.ListOpenCommunityItems(context.Background(), teamDir, GitHubCommunityListOptions{Limit: 60, IncludeIssues: true})
	if err != nil {
		t.Fatalf("ListOpenCommunityItems limit 60: %v", err)
	}
	if len(items) != 50 {
		t.Fatalf("len(items) = %d, want all 50 matching issues", len(items))
	}
	seen := map[int]bool{}
	for _, item := range items {
		if item.Kind != "issue" {
			t.Fatalf("item.Kind = %q, want issue", item.Kind)
		}
		if seen[item.Number] {
			t.Fatalf("duplicate issue number %d in %+v", item.Number, items)
		}
		seen[item.Number] = true
	}
	if len(seen) != 50 || !seen[11] || !seen[60] {
		t.Fatalf("seen issue numbers = %+v, want 11 through 60", seen)
	}
	if got := strings.Join(requests, ","); got != "1:60,2:60" {
		t.Fatalf("requests = %s, want stable 60-item raw pages", got)
	}
}

func TestGitHubAddCommunityItemLabelsRequiresToken(t *testing.T) {
	teamDir := testGitHubTeamDir(t, ``)
	var gotAuth string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.URL.Path != "/repos/acme/widgets/issues/42/labels" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	client := &GitHubClient{RESTEndpoint: server.URL, Token: "gh-key", HTTPClient: server.Client()}

	if err := client.AddCommunityItemLabels(context.Background(), teamDir, "", "", 42, []string{"community-intake", "bug"}); err != nil {
		t.Fatalf("AddCommunityItemLabels: %v", err)
	}
	if gotAuth != "Bearer gh-key" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	rawLabels, _ := gotPayload["labels"].([]any)
	if len(rawLabels) != 2 || rawLabels[0] != "community-intake" || rawLabels[1] != "bug" {
		t.Fatalf("payload = %+v, want labels", gotPayload)
	}
}

func githubCommunityRecords(prs, issues int) []string {
	records := make([]string, 0, prs+issues)
	for i := 1; i <= prs; i++ {
		records = append(records, githubCommunityRecord(i, true))
	}
	for i := prs + 1; i <= prs+issues; i++ {
		records = append(records, githubCommunityRecord(i, false))
	}
	return records
}

func githubCommunityRecord(number int, pullRequest bool) string {
	kindPath := "issues"
	pullRequestJSON := ""
	if pullRequest {
		kindPath = "pull"
		pullRequestJSON = fmt.Sprintf(`,"pull_request":{"html_url":"https://github.com/acme/widgets/pull/%d"}`, number)
	}
	return fmt.Sprintf(`{"number":%d,"html_url":"https://github.com/acme/widgets/%s/%d","title":"Item %d","body":"body","state":"open","user":{"login":"user%d"}%s}`,
		number, kindPath, number, number, number, pullRequestJSON)
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
		{raw: "acme/widgets/issues/42", want: "acme/widgets#42"},
		{raw: "#42", want: "acme/widgets#42"},
		{raw: "42", want: "acme/widgets#42"},
		{raw: "GH-42", want: "acme/widgets#42"},
		{raw: "gh-42", want: "acme/widgets#42"},
		{raw: "gH-00042", want: "acme/widgets#42"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			ref, ok := parseGitHubIssueRef(tt.raw, "acme", "widgets")
			if !ok || ref.String() != tt.want {
				t.Fatalf("parseGitHubIssueRef(%q) = %+v/%v, want %s", tt.raw, ref, ok, tt.want)
			}
		})
	}
}

func TestParseGitHubIssueRefRejectsMalformedGHIdentifiers(t *testing.T) {
	for _, raw := range []string{
		"GH-",
		"GH-0",
		"GH--1",
		"GH-nope",
		"GH-42x",
		"GH-+42",
		"SQU-42",
		"prefix-GH-42",
	} {
		t.Run(raw, func(t *testing.T) {
			if ref, ok := parseGitHubIssueRef(raw, "acme", "widgets"); ok {
				t.Fatalf("parseGitHubIssueRef(%q) = %+v/true, want rejection", raw, ref)
			}
		})
	}
}

func TestGitHubTicketActionsAcceptGHIdentifiers(t *testing.T) {
	teamDir := testGitHubTeamDir(t, ``)
	tests := []struct {
		name       string
		request    TicketRequest
		wantMethod string
		wantPath   string
	}{
		{
			name:       "update uppercase",
			request:    TicketRequest{Action: TicketUpdate, Ticket: "GH-42", Title: "Updated title"},
			wantMethod: http.MethodPatch,
			wantPath:   "/repos/acme/widgets/issues/42",
		},
		{
			name:       "comment lowercase",
			request:    TicketRequest{Action: TicketComment, Ticket: "gh-42", Body: "A comment"},
			wantMethod: http.MethodPost,
			wantPath:   "/repos/acme/widgets/issues/42/comments",
		},
		{
			name:       "close mixed case",
			request:    TicketRequest{Action: TicketClose, Ticket: "Gh-42"},
			wantMethod: http.MethodPatch,
			wantPath:   "/repos/acme/widgets/issues/42",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests []githubRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, githubRequest{Method: r.Method, Path: r.URL.Path})
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"number":42,"html_url":"https://github.com/acme/widgets/issues/42","title":"Issue","state":"closed"}`))
			}))
			defer server.Close()
			t.Setenv("AGENT_TEAM_GITHUB_REST_URL", server.URL)
			t.Setenv("AGENT_TEAM_GITHUB_TOKEN", "gh-key")

			result := ApplyTicket(context.Background(), teamDir, tt.request)
			if result.Error != "" || result.Issue != "acme/widgets#42" {
				t.Fatalf("ApplyTicket(%s, %q) = %+v, want normalized issue", tt.request.Action, tt.request.Ticket, result)
			}
			if len(requests) != 1 || requests[0].Method != tt.wantMethod || requests[0].Path != tt.wantPath {
				t.Fatalf("requests = %+v, want %s %s", requests, tt.wantMethod, tt.wantPath)
			}
		})
	}
}

func TestGitHubTicketActionsRejectMalformedGHIdentifiers(t *testing.T) {
	teamDir := testGitHubTeamDir(t, ``)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	t.Setenv("AGENT_TEAM_GITHUB_REST_URL", server.URL)
	t.Setenv("AGENT_TEAM_GITHUB_TOKEN", "gh-key")
	actions := []TicketRequest{
		{Action: TicketUpdate, Title: "Updated title"},
		{Action: TicketComment, Body: "A comment"},
		{Action: TicketClose},
	}
	for _, action := range actions {
		for _, raw := range []string{"GH-", "GH-0", "GH--1", "GH-nope", "GH-+42", "SQU-42"} {
			t.Run(string(action.Action)+"/"+raw, func(t *testing.T) {
				action.Ticket = raw
				result := ApplyTicket(context.Background(), teamDir, action)
				if !strings.Contains(result.Error, "GitHub issue reference is required") || !strings.Contains(result.Error, "accepted forms") {
					t.Fatalf("ApplyTicket(%s, %q).Error = %q, want clear reference error with accepted forms", action.Action, raw, result.Error)
				}
			})
		}
	}
	if requests != 0 {
		t.Fatalf("malformed references made %d HTTP requests, want zero", requests)
	}
}

func TestDecodeProviderConfigMissingFile(t *testing.T) {
	_, err := decodeProviderConfig(filepath.Join(t.TempDir(), ".agent_team"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decodeProviderConfig err = %v, want os.ErrNotExist", err)
	}
}
