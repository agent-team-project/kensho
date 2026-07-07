package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-team-project/agent-team/internal/pmprovider"
)

func TestTicketCreateRoutesToLinearProvider(t *testing.T) {
	clearTicketOriginEnv(t)
	root := writeTicketCommandConfig(t, `[pm]
provider = "linear"

[linear]
team_id = "team-1"

[project]
id = "project-1"
`)
	var gotAuth string
	var gotInput map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode linear payload: %v", err)
		}
		if !strings.Contains(payload.Query, "issueCreate") {
			t.Fatalf("linear query = %q, want issueCreate", payload.Query)
		}
		input, ok := payload.Variables["input"].(map[string]any)
		if !ok {
			t.Fatalf("linear variables missing input: %+v", payload.Variables)
		}
		gotInput = input
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"issueCreate":{"success":true,"issue":{"id":"lin-1","identifier":"SQU-1","url":"https://linear.app/squirtlesquad/issue/SQU-1/test","title":"Linear title","state":{"name":"Todo"},"labels":{"nodes":[]}}}}}`)
	}))
	defer server.Close()
	t.Setenv("AGENT_TEAM_LINEAR_GRAPHQL_URL", server.URL)
	t.Setenv("LINEAR_API_KEY", "linear-token")
	t.Setenv("AGENT_TEAM_TEAM", "platform")
	t.Setenv("AGENT_TEAM_INSTANCE", "feedback-triage")
	t.Setenv("AGENT_TEAM_ORIGIN_AGENT", "manager")
	t.Setenv("AGENT_TEAM_JOB_ID", "feedback-sweep")
	t.Setenv("AGENT_TEAM_ORIGIN_TRIGGER", "schedule:feedback-triage")

	out, stderr, err := runRootResolverCommand("--repo", root, "ticket", "create", "--title", "Linear title", "--body", "Linear body", "--json")
	if err != nil {
		t.Fatalf("ticket create linear: %v\nstderr=%s", err, stderr)
	}
	var result pmprovider.TicketResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out)
	}
	if result.Provider != pmprovider.ProviderLinear || result.Issue != "SQU-1" {
		t.Fatalf("result = %+v, want linear SQU-1", result)
	}
	if gotAuth != "linear-token" {
		t.Fatalf("Authorization = %q, want linear token", gotAuth)
	}
	if gotInput["teamId"] != "team-1" || gotInput["title"] != "Linear title" {
		t.Fatalf("linear input = %+v", gotInput)
	}
	description, _ := gotInput["description"].(string)
	for _, want := range []string{
		"Linear body",
		"agent-team-origin:",
		"project=project-1",
		"team=platform",
		"instance=feedback-triage",
		"agent=manager",
		"job=feedback-sweep",
		"trigger=schedule:feedback-triage",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("linear description missing %q:\n%s", want, description)
		}
	}
}

func TestTicketCreateRoutesToGitHubProvider(t *testing.T) {
	clearTicketOriginEnv(t)
	root := writeTicketCommandConfig(t, `[pm]
provider = "github"

[github]
owner = "acme"
repo = "widgets"
`)
	var gotAuth string
	var gotPath string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode github payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number":42,"html_url":"https://github.com/acme/widgets/issues/42","title":"GitHub title","state":"open","labels":[{"name":"harness"}]}`)
	}))
	defer server.Close()
	t.Setenv("AGENT_TEAM_GITHUB_REST_URL", server.URL)
	t.Setenv("GITHUB_TOKEN", "github-token")

	out, stderr, err := runRootResolverCommand("--repo", root, "ticket", "create", "--title", "GitHub title", "--body", "GitHub body", "--label", "harness", "--json")
	if err != nil {
		t.Fatalf("ticket create github: %v\nstderr=%s", err, stderr)
	}
	var result pmprovider.TicketResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v\nbody=%s", err, out)
	}
	if result.Provider != pmprovider.ProviderGitHub || result.Issue != "acme/widgets#42" {
		t.Fatalf("result = %+v, want github issue 42", result)
	}
	if gotAuth != "Bearer github-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotPath != "/repos/acme/widgets/issues" {
		t.Fatalf("path = %q, want GitHub issues endpoint", gotPath)
	}
	if gotPayload["title"] != "GitHub title" || gotPayload["body"] != "GitHub body" {
		t.Fatalf("github payload = %+v", gotPayload)
	}
	labels, ok := gotPayload["labels"].([]any)
	if !ok || len(labels) != 1 || labels[0] != "harness" {
		t.Fatalf("github labels = %#v, want harness", gotPayload["labels"])
	}
}

func writeTicketCommandConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir team dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

func clearTicketOriginEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AGENT_TEAM_PROJECT",
		"AGENT_TEAM_DEPLOYMENT_URI",
		"AGENT_TEAM_TEAM",
		"AGENT_TEAM_INSTANCE",
		"AGENT_TEAM_ORIGIN_INSTANCE",
		"AGENT_TEAM_ORIGIN_INSTANCE_URI",
		"AGENT_TEAM_ORIGIN_AGENT",
		"AGENT_TEAM_JOB_ID",
		"AGENT_TEAM_ORIGIN_JOB",
		"AGENT_TEAM_ORIGIN_JOB_URI",
		"AGENT_TEAM_ORIGIN_TRIGGER",
		"AGENT_TEAM_ORIGIN_BUILD",
	} {
		t.Setenv(key, "")
	}
}
