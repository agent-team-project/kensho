package pmprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/intake"
	"github.com/agent-team-project/agent-team/internal/job"
)

const (
	defaultGitHubRESTEndpoint    = "https://api.github.com"
	defaultGitHubGraphQLEndpoint = "https://api.github.com/graphql"
)

var errNoGitHubToken = errors.New("no GitHub token found")

type GitHubClient struct {
	RESTEndpoint    string
	GraphQLEndpoint string
	Token           string
	HTTPClient      *http.Client
}

type githubIssueRef struct {
	Owner  string
	Repo   string
	Number int
	URL    string
}

func DefaultGitHubClient() *GitHubClient {
	rest := strings.TrimSpace(os.Getenv("AGENT_TEAM_GITHUB_REST_URL"))
	if rest == "" {
		rest = defaultGitHubRESTEndpoint
	}
	graphql := strings.TrimSpace(os.Getenv("AGENT_TEAM_GITHUB_GRAPHQL_URL"))
	if graphql == "" {
		graphql = defaultGitHubGraphQLEndpoint
	}
	return &GitHubClient{
		RESTEndpoint:    rest,
		GraphQLEndpoint: graphql,
		HTTPClient:      &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *GitHubClient) Name() ProviderName {
	return ProviderGitHub
}

func (c *GitHubClient) NormalizeIntake(body []byte) (*intake.Event, error) {
	return intake.NormalizeGitHub(body)
}

func (c *GitHubClient) ResolveActorID(teamDir string) (string, error) {
	cfg, err := decodeProviderConfig(teamDir)
	if err != nil {
		return "", err
	}
	if login := strings.TrimSpace(cfg.GitHub.AgentLogin); login != "" {
		return login, nil
	}
	if id := strings.TrimSpace(cfg.GitHub.AgentID); id != "" {
		return id, nil
	}
	if actor := cachedGitHubViewerActor(teamDir); actor != "" {
		return actor, nil
	}
	token, err := c.resolveToken(teamDir)
	if err != nil {
		return "", err
	}
	viewer, err := c.lookupViewer(context.Background(), token)
	if err != nil {
		return "", err
	}
	actor := strings.TrimSpace(viewer.Login)
	if actor == "" {
		actor = strings.TrimSpace(viewer.ID)
	}
	if actor == "" {
		return "", errors.New("GitHub viewer query did not return a login or id")
	}
	if err := writeGitHubViewerCache(teamDir, viewer); err != nil {
		return "", err
	}
	return actor, nil
}

func (c *GitHubClient) SelfStatusChangeForActor(ev *intake.Event, actorID string) (bool, string) {
	return intake.GitHubSelfStatusChangeForActor(ev, actorID)
}

func (c *GitHubClient) WriteBack(ctx context.Context, teamDir string, req Request) Result {
	result := Result{Action: req.Action}
	finish := func(result Result) Result {
		if req.Job != nil {
			result.AuditErr = appendProviderAudit(teamDir, req.Job, req, result, string(ProviderGitHub))
		}
		return result
	}
	if req.Job == nil {
		result.Skipped = true
		result.Message = "job is required"
		return result
	}
	cfg, skip, err := loadGitHubConfig(teamDir)
	if err != nil {
		result.Error = err.Error()
		result.Message = "github write-back failed"
		return finish(result)
	}
	if skip != "" {
		result.Skipped = true
		result.Message = skip
		return finish(result)
	}
	issue, ok := githubIssueRefForJob(cfg, req.Job)
	if !ok {
		result.Skipped = true
		result.Message = "job has no GitHub issue reference"
		return finish(result)
	}
	result.Issue = issue.String()
	issueState, commentBody, labelNames, projectStatus, skip := githubRequestIntent(cfg, req)
	if skip != "" {
		result.Skipped = true
		result.Message = skip
		return finish(result)
	}
	result.State = issueState
	result.Labels = strings.Join(labelNames, ",")
	result.Comment = strings.TrimSpace(commentBody) != ""
	if githubProjectConfigured(cfg) && projectStatus != "" {
		result.Project = githubProjectRef(cfg)
		result.ProjectStatus = projectStatus
	}
	token, err := c.resolveToken(teamDir)
	if err != nil {
		if errors.Is(err, errNoGitHubToken) {
			result.Skipped = true
			result.Message = err.Error()
			return finish(result)
		}
		result.Error = err.Error()
		result.Message = "github write-back failed"
		return finish(result)
	}
	ctx, cancel := contextWithTimeout(ctx, 10*time.Second)
	defer cancel()
	if issueState != "" {
		if err := c.updateIssueState(ctx, token, issue, issueState); err != nil {
			result.Error = err.Error()
			result.Message = "github write-back failed"
			return finish(result)
		}
		result.Changed = true
	}
	if len(labelNames) > 0 {
		if err := c.addIssueLabels(ctx, token, issue, labelNames); err != nil {
			result.Error = err.Error()
			result.Message = "github write-back failed"
			return finish(result)
		}
		result.Changed = true
	}
	if projectStatus != "" && githubProjectConfigured(cfg) {
		if err := c.updateProjectStatus(ctx, token, cfg, issue, projectStatus); err != nil {
			result.Error = err.Error()
			result.Message = "github write-back failed"
			return finish(result)
		}
		result.Changed = true
	}
	if strings.TrimSpace(commentBody) != "" {
		commentBody = appendOriginFooter(teamDir, req, commentBody)
		if err := c.createIssueComment(ctx, token, issue, commentBody); err != nil {
			result.Error = err.Error()
			result.Message = "github write-back failed"
			return finish(result)
		}
		result.Changed = true
	}
	result.Message = githubSuccessMessage(result)
	return finish(result)
}

func loadGitHubConfig(teamDir string) (config, string, error) {
	cfg, err := decodeProviderConfig(teamDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, "GitHub not configured for this repo", nil
		}
		return cfg, "", err
	}
	if ConfiguredProviderName(cfg.PM.Provider, cfg.Team.PMTool) != ProviderGitHub {
		return cfg, "GitHub not configured for this repo", nil
	}
	if strings.TrimSpace(cfg.GitHub.Owner) == "" {
		return cfg, "", errors.New("[github].owner is required")
	}
	if strings.TrimSpace(cfg.GitHub.Repo) == "" {
		return cfg, "", errors.New("[github].repo is required")
	}
	return cfg, "", nil
}

func decodeProviderConfig(teamDir string) (config, error) {
	var cfg config
	path := filepath.Join(teamDir, "config.toml")
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func githubRequestIntent(cfg config, req Request) (issueState, commentBody string, labelNames []string, projectStatus, skip string) {
	switch req.Action {
	case ActionDispatchInProgress:
		issueState = strings.TrimSpace(cfg.GitHub.InProgressState)
		projectStatus = strings.TrimSpace(cfg.GitHub.InProgressColumn)
		labelNames = append(labelNames, strings.TrimSpace(cfg.GitHub.InProgressLabel))
	case ActionBounceBack:
		issueState = strings.TrimSpace(cfg.GitHub.InProgressState)
		projectStatus = strings.TrimSpace(cfg.GitHub.InProgressColumn)
		labelNames = append(labelNames, strings.TrimSpace(cfg.GitHub.InProgressLabel))
		commentBody = bounceComment(req)
	case ActionFailureAttention:
		issueState = strings.TrimSpace(cfg.GitHub.AttentionState)
		projectStatus = strings.TrimSpace(cfg.GitHub.AttentionColumn)
		labelNames = append(labelNames, strings.TrimSpace(cfg.GitHub.AttentionLabel))
		commentBody = failureComment(req)
	default:
		return "", "", nil, "", "github write-back action is not supported"
	}
	labelNames = cleanStrings(append(cleanStrings(cfg.GitHub.Labels), labelNames...))
	if issueState == "" && projectStatus == "" && len(labelNames) == 0 && strings.TrimSpace(commentBody) == "" {
		return "", "", nil, "", "github write-back has no configured state, label, project column, or comment"
	}
	return issueState, commentBody, labelNames, projectStatus, ""
}

func (r githubIssueRef) String() string {
	return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
}

func githubIssueRefForJob(cfg config, j *job.Job) (githubIssueRef, bool) {
	if j == nil {
		return githubIssueRef{}, false
	}
	for _, raw := range []string{j.TicketURL, j.Ticket, j.ID} {
		if ref, ok := parseGitHubIssueRef(raw, cfg.GitHub.Owner, cfg.GitHub.Repo); ok {
			return ref, true
		}
	}
	return githubIssueRef{}, false
}

func parseGitHubIssueRef(raw, defaultOwner, defaultRepo string) (githubIssueRef, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return githubIssueRef{}, false
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		parts := cleanPathParts(u.Path)
		switch {
		case len(parts) >= 5 && parts[0] == "repos" && parts[3] == "issues":
			return issueRefFromParts(parts[1], parts[2], parts[4], raw)
		case len(parts) >= 4 && parts[2] == "issues":
			return issueRefFromParts(parts[0], parts[1], parts[3], raw)
		}
	}
	if ownerRepo, number, ok := strings.Cut(raw, "#"); ok {
		parts := strings.Split(ownerRepo, "/")
		if len(parts) == 2 {
			return issueRefFromParts(parts[0], parts[1], number, "")
		}
	}
	parts := cleanPathParts(raw)
	if len(parts) >= 4 && parts[2] == "issues" {
		return issueRefFromParts(parts[0], parts[1], parts[3], "")
	}
	number := strings.TrimPrefix(raw, "#")
	if _, err := strconv.Atoi(number); err == nil && strings.TrimSpace(defaultOwner) != "" && strings.TrimSpace(defaultRepo) != "" {
		return issueRefFromParts(defaultOwner, defaultRepo, number, "")
	}
	return githubIssueRef{}, false
}

func cleanPathParts(raw string) []string {
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func issueRefFromParts(owner, repo, number, rawURL string) (githubIssueRef, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(number))
	if err != nil || n <= 0 {
		return githubIssueRef{}, false
	}
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return githubIssueRef{}, false
	}
	return githubIssueRef{Owner: owner, Repo: repo, Number: n, URL: strings.TrimSpace(rawURL)}, true
}

func githubProjectConfigured(cfg config) bool {
	return cfg.GitHub.ProjectNumber > 0 && strings.TrimSpace(githubProjectOwner(cfg)) != ""
}

func githubProjectOwner(cfg config) string {
	if owner := strings.TrimSpace(cfg.GitHub.ProjectOwner); owner != "" {
		return owner
	}
	return strings.TrimSpace(cfg.GitHub.Owner)
}

func githubProjectStatusField(cfg config) string {
	if field := strings.TrimSpace(cfg.GitHub.ProjectStatusField); field != "" {
		return field
	}
	return "Status"
}

func githubProjectRef(cfg config) string {
	if !githubProjectConfigured(cfg) {
		return ""
	}
	return fmt.Sprintf("%s#%d", githubProjectOwner(cfg), cfg.GitHub.ProjectNumber)
}

func githubSuccessMessage(result Result) string {
	parts := make([]string, 0, 4)
	if result.State != "" {
		parts = append(parts, "set issue state to "+result.State)
	}
	if result.Labels != "" {
		parts = append(parts, "labeled issue")
	}
	if result.ProjectStatus != "" {
		parts = append(parts, "moved project item to "+result.ProjectStatus)
	}
	if result.Comment {
		parts = append(parts, "commented")
	}
	if len(parts) == 0 {
		return "github write-back completed"
	}
	return "github write-back " + strings.Join(parts, " and ")
}

func (c *GitHubClient) resolveToken(teamDir string) (string, error) {
	if token := strings.TrimSpace(c.Token); token != "" {
		return token, nil
	}
	for _, name := range []string{"AGENT_TEAM_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, nil
		}
	}
	for _, envPath := range candidateEnvFiles(teamDir) {
		values, err := readDotEnv(envPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		for _, name := range []string{"AGENT_TEAM_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
			if token := strings.TrimSpace(values[name]); token != "" {
				return token, nil
			}
		}
	}
	return "", errNoGitHubToken
}

func (c *GitHubClient) updateIssueState(ctx context.Context, token string, issue githubIssueRef, state string) error {
	state = strings.TrimSpace(state)
	if state == "" {
		return nil
	}
	return c.rest(ctx, token, http.MethodPatch, issuePath(issue), map[string]any{"state": state}, nil)
}

func (c *GitHubClient) addIssueLabels(ctx context.Context, token string, issue githubIssueRef, labels []string) error {
	labels = cleanStrings(labels)
	if len(labels) == 0 {
		return nil
	}
	return c.rest(ctx, token, http.MethodPost, path.Join(issuePath(issue), "labels"), map[string]any{"labels": labels}, nil)
}

func (c *GitHubClient) createIssueComment(ctx context.Context, token string, issue githubIssueRef, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	return c.rest(ctx, token, http.MethodPost, path.Join(issuePath(issue), "comments"), map[string]any{"body": body}, nil)
}

func issuePath(issue githubIssueRef) string {
	return path.Join(
		"repos",
		url.PathEscape(issue.Owner),
		url.PathEscape(issue.Repo),
		"issues",
		strconv.Itoa(issue.Number),
	)
}

func (c *GitHubClient) rest(ctx context.Context, token, method, apiPath string, payload any, out any) error {
	endpoint := strings.TrimRight(strings.TrimSpace(c.RESTEndpoint), "/")
	if endpoint == "" {
		endpoint = defaultGitHubRESTEndpoint
	}
	var body io.Reader = http.NoBody
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint+"/"+strings.TrimLeft(apiPath, "/"), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub REST %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	if out == nil || len(bodyBytes) == 0 {
		return nil
	}
	return json.Unmarshal(bodyBytes, out)
}

func (c *GitHubClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

type githubViewer struct {
	Login string `json:"login"`
	ID    string `json:"id"`
}

func (c *GitHubClient) lookupViewer(ctx context.Context, token string) (githubViewer, error) {
	var raw struct {
		Login string `json:"login"`
		ID    any    `json:"id"`
	}
	if err := c.rest(ctx, token, http.MethodGet, "user", nil, &raw); err != nil {
		return githubViewer{}, err
	}
	return githubViewer{Login: strings.TrimSpace(raw.Login), ID: stringifyGitHubValue(raw.ID)}, nil
}

func cachedGitHubViewerActor(teamDir string) string {
	body, err := os.ReadFile(filepath.Join(teamDir, "state", "github", "viewer.json"))
	if err != nil {
		return ""
	}
	var raw struct {
		Login  string `json:"login"`
		ID     any    `json:"id"`
		Viewer struct {
			Login string `json:"login"`
			ID    any    `json:"id"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ""
	}
	if login := strings.TrimSpace(raw.Login); login != "" {
		return login
	}
	if login := strings.TrimSpace(raw.Viewer.Login); login != "" {
		return login
	}
	if id := stringifyGitHubValue(raw.ID); id != "" {
		return id
	}
	return stringifyGitHubValue(raw.Viewer.ID)
}

func writeGitHubViewerCache(teamDir string, viewer githubViewer) error {
	dir := filepath.Join(teamDir, "state", "github")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write GitHub viewer cache: %w", err)
	}
	body, err := json.MarshalIndent(map[string]any{
		"login":     strings.TrimSpace(viewer.Login),
		"id":        strings.TrimSpace(viewer.ID),
		"cached_at": time.Now().UTC().Format(time.RFC3339),
		"source":    "github.viewer",
	}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := filepath.Join(dir, "viewer.json.tmp")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write GitHub viewer cache: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "viewer.json")); err != nil {
		return fmt.Errorf("write GitHub viewer cache: %w", err)
	}
	return nil
}

type githubProjectLookup struct {
	IssueID  string
	Project  githubProject
	ItemID   string
	FieldID  string
	OptionID string
}

type githubProject struct {
	ID string `json:"id"`
}

type githubProjectField struct {
	Typename string                `json:"__typename"`
	ID       string                `json:"id"`
	Name     string                `json:"name"`
	Options  []githubProjectOption `json:"options"`
}

type githubProjectOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *GitHubClient) updateProjectStatus(ctx context.Context, token string, cfg config, issue githubIssueRef, status string) error {
	status = strings.TrimSpace(status)
	if status == "" || !githubProjectConfigured(cfg) {
		return nil
	}
	lookup, err := c.lookupProjectStatus(ctx, token, cfg, issue, status)
	if err != nil {
		return err
	}
	itemID := strings.TrimSpace(lookup.ItemID)
	if itemID == "" {
		itemID, err = c.addProjectItem(ctx, token, lookup.Project.ID, lookup.IssueID)
		if err != nil {
			return err
		}
	}
	return c.updateProjectItemStatus(ctx, token, lookup.Project.ID, itemID, lookup.FieldID, lookup.OptionID)
}

func (c *GitHubClient) lookupProjectStatus(ctx context.Context, token string, cfg config, issue githubIssueRef, status string) (githubProjectLookup, error) {
	const query = `query($owner: String!, $repo: String!, $number: Int!, $projectOwner: String!, $projectNumber: Int!) {
  repository(owner: $owner, name: $repo) {
    issue(number: $number) {
      id
      projectItems(first: 100) {
        nodes { id project { id } }
      }
    }
  }
  organization(login: $projectOwner) { projectV2(number: $projectNumber) { ...ProjectFields } }
  user(login: $projectOwner) { projectV2(number: $projectNumber) { ...ProjectFields } }
}
fragment ProjectFields on ProjectV2 {
  id
  fields(first: 100) {
    nodes {
      __typename
      ... on ProjectV2SingleSelectField { id name options { id name } }
    }
  }
}`
	var out struct {
		Repository struct {
			Issue struct {
				ID           string `json:"id"`
				ProjectItems struct {
					Nodes []struct {
						ID      string        `json:"id"`
						Project githubProject `json:"project"`
					} `json:"nodes"`
				} `json:"projectItems"`
			} `json:"issue"`
		} `json:"repository"`
		Organization struct {
			ProjectV2 githubProjectWithFields `json:"projectV2"`
		} `json:"organization"`
		User struct {
			ProjectV2 githubProjectWithFields `json:"projectV2"`
		} `json:"user"`
	}
	err := c.graphql(ctx, token, query, map[string]any{
		"owner":         issue.Owner,
		"repo":          issue.Repo,
		"number":        issue.Number,
		"projectOwner":  githubProjectOwner(cfg),
		"projectNumber": cfg.GitHub.ProjectNumber,
	}, &out)
	if err != nil {
		return githubProjectLookup{}, err
	}
	issueID := strings.TrimSpace(out.Repository.Issue.ID)
	if issueID == "" {
		return githubProjectLookup{}, errors.New("GitHub issue not found")
	}
	project := out.Organization.ProjectV2
	if strings.TrimSpace(project.ID) == "" {
		project = out.User.ProjectV2
	}
	if strings.TrimSpace(project.ID) == "" {
		return githubProjectLookup{}, fmt.Errorf("GitHub project %s not found", githubProjectRef(cfg))
	}
	fieldID, optionID := githubProjectFieldOption(project.Fields.Nodes, githubProjectStatusField(cfg), status)
	if fieldID == "" {
		return githubProjectLookup{}, fmt.Errorf("GitHub project field %q not found", githubProjectStatusField(cfg))
	}
	if optionID == "" {
		return githubProjectLookup{}, fmt.Errorf("GitHub project status option %q not found", status)
	}
	itemID := ""
	for _, node := range out.Repository.Issue.ProjectItems.Nodes {
		if node.Project.ID == project.ID {
			itemID = strings.TrimSpace(node.ID)
			break
		}
	}
	return githubProjectLookup{
		IssueID:  issueID,
		Project:  githubProject{ID: project.ID},
		ItemID:   itemID,
		FieldID:  fieldID,
		OptionID: optionID,
	}, nil
}

type githubProjectWithFields struct {
	ID     string `json:"id"`
	Fields struct {
		Nodes []githubProjectField `json:"nodes"`
	} `json:"fields"`
}

func githubProjectFieldOption(fields []githubProjectField, fieldName, optionName string) (fieldID, optionID string) {
	fieldName = strings.TrimSpace(fieldName)
	optionName = strings.TrimSpace(optionName)
	for _, field := range fields {
		if !strings.EqualFold(strings.TrimSpace(field.Name), fieldName) {
			continue
		}
		fieldID = strings.TrimSpace(field.ID)
		for _, option := range field.Options {
			if strings.EqualFold(strings.TrimSpace(option.Name), optionName) {
				optionID = strings.TrimSpace(option.ID)
				break
			}
		}
		return fieldID, optionID
	}
	return "", ""
}

func (c *GitHubClient) addProjectItem(ctx context.Context, token, projectID, contentID string) (string, error) {
	const query = `mutation($projectId: ID!, $contentId: ID!) {
  addProjectV2ItemById(input: {projectId: $projectId, contentId: $contentId}) { item { id } }
}`
	var out struct {
		AddProjectV2ItemByID struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"addProjectV2ItemById"`
	}
	if err := c.graphql(ctx, token, query, map[string]any{"projectId": projectID, "contentId": contentID}, &out); err != nil {
		return "", err
	}
	itemID := strings.TrimSpace(out.AddProjectV2ItemByID.Item.ID)
	if itemID == "" {
		return "", errors.New("GitHub addProjectV2ItemById returned no item id")
	}
	return itemID, nil
}

func (c *GitHubClient) updateProjectItemStatus(ctx context.Context, token, projectID, itemID, fieldID, optionID string) error {
	const query = `mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $optionId: String!) {
  updateProjectV2ItemFieldValue(input: {
    projectId: $projectId,
    itemId: $itemId,
    fieldId: $fieldId,
    value: { singleSelectOptionId: $optionId }
  }) { projectV2Item { id } }
}`
	var out struct {
		UpdateProjectV2ItemFieldValue struct {
			ProjectV2Item struct {
				ID string `json:"id"`
			} `json:"projectV2Item"`
		} `json:"updateProjectV2ItemFieldValue"`
	}
	if err := c.graphql(ctx, token, query, map[string]any{
		"projectId": projectID,
		"itemId":    itemID,
		"fieldId":   fieldID,
		"optionId":  optionID,
	}, &out); err != nil {
		return err
	}
	if strings.TrimSpace(out.UpdateProjectV2ItemFieldValue.ProjectV2Item.ID) == "" {
		return errors.New("GitHub updateProjectV2ItemFieldValue returned no item id")
	}
	return nil
}

func (c *GitHubClient) graphql(ctx context.Context, token, query string, variables map[string]any, out any) error {
	endpoint := strings.TrimSpace(c.GraphQLEndpoint)
	if endpoint == "" {
		endpoint = defaultGitHubGraphQLEndpoint
	}
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub GraphQL HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return errors.New(envelope.Errors[0].Message)
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("GitHub GraphQL response missing data")
	}
	return json.Unmarshal(envelope.Data, out)
}

func stringifyGitHubValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprint(x)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}
