package pmprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/intake"
	"github.com/agent-team-project/agent-team/internal/job"
)

const defaultEndpoint = "https://api.linear.app/graphql"

var errNoAPIKey = errors.New("no Linear API key found")

type Action string

const (
	ActionDispatchInProgress Action = "dispatch_in_progress"
	ActionBounceBack         Action = "bounce_back"
	ActionFailureAttention   Action = "failure_attention"
)

type Request struct {
	Action   Action
	Job      *job.Job
	StepID   string
	Findings string
	Message  string
	Actor    string
}

type Result struct {
	Action        Action `json:"action"`
	Issue         string `json:"issue,omitempty"`
	State         string `json:"state,omitempty"`
	Labels        string `json:"labels,omitempty"`
	Project       string `json:"project,omitempty"`
	ProjectStatus string `json:"project_status,omitempty"`
	Comment       bool   `json:"comment,omitempty"`
	Skipped       bool   `json:"skipped,omitempty"`
	Changed       bool   `json:"changed,omitempty"`
	Message       string `json:"message,omitempty"`
	Error         string `json:"error,omitempty"`
	AuditErr      error  `json:"-"`
}

type Client struct {
	Endpoint   string
	APIKey     string
	HTTPClient *http.Client
}

type config struct {
	PM struct {
		Provider string `toml:"provider"`
	} `toml:"pm"`
	Team struct {
		PMTool string `toml:"pm_tool"`
	} `toml:"team"`
	Linear struct {
		TeamID          string   `toml:"team_id"`
		TicketPrefix    string   `toml:"ticket_prefix"`
		InProgressState string   `toml:"in_progress_state"`
		AttentionState  string   `toml:"attention_state"`
		ClosedState     string   `toml:"closed_state"`
		Labels          []string `toml:"labels"`
	} `toml:"linear"`
	GitHub struct {
		Owner              string   `toml:"owner"`
		Repo               string   `toml:"repo"`
		AgentColumn        string   `toml:"agent_column"`
		AgentLogin         string   `toml:"agent_login"`
		AgentID            string   `toml:"agent_id"`
		InProgressState    string   `toml:"in_progress_state"`
		AttentionState     string   `toml:"attention_state"`
		InProgressLabel    string   `toml:"in_progress_label"`
		AttentionLabel     string   `toml:"attention_label"`
		Labels             []string `toml:"labels"`
		ProjectOwner       string   `toml:"project_owner"`
		ProjectNumber      int      `toml:"project_number"`
		ProjectStatusField string   `toml:"project_status_field"`
		InProgressColumn   string   `toml:"in_progress_column"`
		AttentionColumn    string   `toml:"attention_column"`
	} `toml:"github"`
}

func (c *Client) Name() ProviderName {
	return ProviderLinear
}

func (c *Client) NormalizeIntake(body []byte) (*intake.Event, error) {
	return intake.NormalizeLinear(body)
}

func (c *Client) ResolveActorID(teamDir string) (string, error) {
	return intake.ResolveLinearAgentUserID(teamDir)
}

func (c *Client) SelfStatusChangeForActor(ev *intake.Event, actorID string) (bool, string) {
	return intake.LinearSelfStatusChangeForUser(ev, actorID)
}

type linearNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type issueStateLookup struct {
	Issue struct {
		ID         string `json:"id"`
		Identifier string `json:"identifier"`
		URL        string `json:"url"`
		Labels     struct {
			Nodes []linearNode `json:"nodes"`
		} `json:"labels"`
	} `json:"issue"`
	WorkflowStates struct {
		Nodes []linearNode `json:"nodes"`
	} `json:"workflowStates"`
	IssueLabels struct {
		Nodes []linearNode `json:"nodes"`
	} `json:"issueLabels"`
}

func DispatchInProgress(ctx context.Context, teamDir string, j *job.Job) Result {
	return DefaultClient().WriteBack(ctx, teamDir, Request{Action: ActionDispatchInProgress, Job: j, Actor: "daemon"})
}

func BounceBack(ctx context.Context, teamDir string, j *job.Job, stepID, findings, actor string) Result {
	return DefaultClient().WriteBack(ctx, teamDir, Request{
		Action:   ActionBounceBack,
		Job:      j,
		StepID:   stepID,
		Findings: findings,
		Actor:    actor,
	})
}

func FailureAttention(ctx context.Context, teamDir string, j *job.Job, message, actor string) Result {
	return DefaultClient().WriteBack(ctx, teamDir, Request{
		Action:  ActionFailureAttention,
		Job:     j,
		Message: message,
		Actor:   actor,
	})
}

func DefaultClient() *Client {
	endpoint := strings.TrimSpace(os.Getenv("AGENT_TEAM_LINEAR_GRAPHQL_URL"))
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	return &Client{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) WriteBack(ctx context.Context, teamDir string, req Request) Result {
	result := Result{Action: req.Action}
	finish := func(result Result) Result {
		if req.Job != nil {
			result.AuditErr = appendAudit(teamDir, req.Job, req, result)
		}
		return result
	}
	if req.Job == nil {
		result.Skipped = true
		result.Message = "job is required"
		return result
	}
	issue := issueIdentifier(req.Job)
	result.Issue = issue
	if issue == "" {
		result.Skipped = true
		result.Message = "job has no Linear ticket identifier"
		return finish(result)
	}
	cfg, skip, err := loadConfig(teamDir)
	if err != nil {
		result.Error = err.Error()
		result.Message = "linear write-back failed"
		return finish(result)
	}
	if skip != "" {
		result.Skipped = true
		result.Message = skip
		return finish(result)
	}
	stateName, commentBody, labelNames, skip := requestIntent(cfg, req)
	if skip != "" {
		result.Skipped = true
		result.Message = skip
		return finish(result)
	}
	result.State = stateName
	result.Comment = strings.TrimSpace(commentBody) != ""
	apiKey, err := c.resolveAPIKey(teamDir)
	if err != nil {
		if errors.Is(err, errNoAPIKey) {
			result.Skipped = true
			result.Message = err.Error()
			return finish(result)
		}
		result.Error = err.Error()
		result.Message = "linear write-back failed"
		return finish(result)
	}
	ctx, cancel := contextWithTimeout(ctx, 10*time.Second)
	defer cancel()
	lookup, err := c.lookupIssueAndStates(ctx, apiKey, cfg.Linear.TeamID, issue)
	if err != nil {
		result.Error = err.Error()
		result.Message = "linear write-back failed"
		return finish(result)
	}
	issueID := strings.TrimSpace(lookup.Issue.ID)
	if issueID == "" {
		result.Error = "Linear issue not found"
		result.Message = "linear write-back failed"
		return finish(result)
	}
	if stateName != "" {
		stateID := workflowStateID(lookup, stateName)
		if stateID == "" {
			result.Error = fmt.Sprintf("Linear workflow state %q not found", stateName)
			result.Message = "linear write-back failed"
			return finish(result)
		}
		if err := c.updateIssueState(ctx, apiKey, issueID, stateID); err != nil {
			result.Error = err.Error()
			result.Message = "linear write-back failed"
			return finish(result)
		}
		result.Changed = true
	}
	if len(labelNames) > 0 {
		labelIDs := workflowLabelIDs(lookup, labelNames)
		if len(labelIDs) == 0 {
			result.Error = fmt.Sprintf("Linear labels not found: %s", strings.Join(labelNames, ", "))
			result.Message = "linear write-back failed"
			return finish(result)
		}
		if err := c.addIssueLabels(ctx, apiKey, issueID, existingIssueLabelIDs(lookup), labelIDs); err != nil {
			result.Error = err.Error()
			result.Message = "linear write-back failed"
			return finish(result)
		}
		result.Labels = strings.Join(labelNames, ",")
		result.Changed = true
	}
	if strings.TrimSpace(commentBody) != "" {
		commentBody = appendOriginFooter(teamDir, req, commentBody)
		if err := c.createComment(ctx, apiKey, issueID, commentBody); err != nil {
			result.Error = err.Error()
			result.Message = "linear write-back failed"
			return finish(result)
		}
		result.Changed = true
	}
	result.Message = successMessage(result)
	return finish(result)
}

func loadConfig(teamDir string) (config, string, error) {
	var cfg config
	path := filepath.Join(teamDir, "config.toml")
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, "Linear not configured for this repo", nil
		}
		return cfg, "", err
	}
	if ConfiguredProviderName(cfg.PM.Provider, cfg.Team.PMTool) != ProviderLinear {
		return cfg, "Linear not configured for this repo", nil
	}
	if strings.TrimSpace(cfg.Linear.TeamID) == "" {
		return cfg, "", errors.New("[linear].team_id is required")
	}
	return cfg, "", nil
}

func requestIntent(cfg config, req Request) (stateName, commentBody string, labelNames []string, skip string) {
	switch req.Action {
	case ActionDispatchInProgress:
		stateName = strings.TrimSpace(cfg.Linear.InProgressState)
		if stateName == "" {
			return "", "", nil, "[linear].in_progress_state is not configured"
		}
	case ActionBounceBack:
		stateName = strings.TrimSpace(cfg.Linear.InProgressState)
		if stateName == "" {
			return "", "", nil, "[linear].in_progress_state is not configured"
		}
		commentBody = bounceComment(req)
	case ActionFailureAttention:
		stateName = strings.TrimSpace(cfg.Linear.AttentionState)
		commentBody = failureComment(req)
		if stateName == "" {
			labelNames = cleanStrings(cfg.Linear.Labels)
		}
		if stateName == "" && strings.TrimSpace(commentBody) == "" {
			return "", "", nil, "[linear].attention_state is not configured"
		}
	default:
		return "", "", nil, "linear write-back action is not supported"
	}
	return stateName, commentBody, labelNames, ""
}

func issueIdentifier(j *job.Job) string {
	if j == nil {
		return ""
	}
	for _, raw := range []string{j.Ticket, j.TicketURL, j.ID} {
		if id := job.ExtractTicketIdentifier(raw); id != "" {
			return id
		}
	}
	return ""
}

func bounceComment(req Request) string {
	step := strings.TrimSpace(req.StepID)
	findings := strings.TrimSpace(req.Findings)
	var b strings.Builder
	fmt.Fprintf(&b, "Job %s was bounced back to implementation", req.Job.ID)
	if step != "" {
		fmt.Fprintf(&b, " for step `%s`", step)
	}
	b.WriteString(".")
	if findings != "" {
		b.WriteString("\n\nReview findings:\n")
		b.WriteString(findings)
	}
	return b.String()
}

func failureComment(req Request) string {
	message := strings.TrimSpace(req.Message)
	if message == "" && req.Job != nil {
		message = strings.TrimSpace(req.Job.LastStatus)
	}
	if req.Job == nil {
		return message
	}
	if message == "" {
		return fmt.Sprintf("Job %s failed and needs attention.", req.Job.ID)
	}
	return fmt.Sprintf("Job %s failed and needs attention: %s", req.Job.ID, message)
}

func successMessage(result Result) string {
	parts := make([]string, 0, 3)
	if result.State != "" {
		parts = append(parts, "moved issue to "+result.State)
	}
	if result.Labels != "" {
		parts = append(parts, "labeled issue")
	}
	if result.Comment {
		parts = append(parts, "commented")
	}
	if len(parts) == 0 {
		return "linear write-back completed"
	}
	return "linear write-back " + strings.Join(parts, " and ")
}

func workflowStateID(lookup issueStateLookup, name string) string {
	name = strings.TrimSpace(name)
	for _, state := range lookup.WorkflowStates.Nodes {
		if strings.EqualFold(strings.TrimSpace(state.Name), name) {
			return strings.TrimSpace(state.ID)
		}
	}
	return ""
}

func workflowLabelIDs(lookup issueStateLookup, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, label := range lookup.IssueLabels.Nodes {
			if strings.EqualFold(strings.TrimSpace(label.Name), name) {
				if id := strings.TrimSpace(label.ID); id != "" {
					out = append(out, id)
				}
				break
			}
		}
	}
	return out
}

func existingIssueLabelIDs(lookup issueStateLookup) []string {
	out := make([]string, 0, len(lookup.Issue.Labels.Nodes))
	for _, label := range lookup.Issue.Labels.Nodes {
		if id := strings.TrimSpace(label.ID); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mergeIDs(existing, add []string) []string {
	out := make([]string, 0, len(existing)+len(add))
	seen := map[string]struct{}{}
	for _, list := range [][]string{existing, add} {
		for _, id := range list {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func (c *Client) resolveAPIKey(teamDir string) (string, error) {
	if key := strings.TrimSpace(c.APIKey); key != "" {
		return key, nil
	}
	for _, name := range []string{"LINEAR_API_KEY", "LINEAR_USER_API_KEY"} {
		if key := strings.TrimSpace(os.Getenv(name)); key != "" {
			return key, nil
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
		for _, name := range []string{"LINEAR_API_KEY", "LINEAR_USER_API_KEY"} {
			if key := strings.TrimSpace(values[name]); key != "" {
				return key, nil
			}
		}
	}
	return "", errNoAPIKey
}

func candidateEnvFiles(teamDir string) []string {
	var files []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, existing := range files {
			if existing == path {
				return
			}
		}
		files = append(files, path)
	}
	repoDir := filepath.Dir(teamDir)
	add(filepath.Join(repoDir, ".env"))
	if main := mainWorktreePath(repoDir); main != "" {
		add(filepath.Join(main, ".env"))
	}
	return files
}

func mainWorktreePath(repoDir string) string {
	out, err := exec.Command("git", "-C", repoDir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			return strings.TrimSpace(path)
		}
	}
	return ""
}

func readDotEnv(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			out[key] = value
		}
	}
	return out, nil
}

func contextWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func (c *Client) lookupIssueAndStates(ctx context.Context, apiKey, teamID, issue string) (issueStateLookup, error) {
	const query = `query($issue: String!, $teamId: ID!) {
  issue(id: $issue) { id identifier url labels { nodes { id name } } }
  workflowStates(filter: { team: { id: { eq: $teamId } } }) { nodes { id name } }
  issueLabels(filter: { team: { id: { eq: $teamId } } }) { nodes { id name } }
}`
	var out struct {
		Issue struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
			URL        string `json:"url"`
			Labels     struct {
				Nodes []linearNode `json:"nodes"`
			} `json:"labels"`
		} `json:"issue"`
		WorkflowStates struct {
			Nodes []linearNode `json:"nodes"`
		} `json:"workflowStates"`
		IssueLabels struct {
			Nodes []linearNode `json:"nodes"`
		} `json:"issueLabels"`
	}
	if err := c.graphql(ctx, apiKey, query, map[string]any{"issue": issue, "teamId": teamID}, &out); err != nil {
		return issueStateLookup{}, err
	}
	return issueStateLookup{Issue: out.Issue, WorkflowStates: out.WorkflowStates, IssueLabels: out.IssueLabels}, nil
}

func (c *Client) updateIssueState(ctx context.Context, apiKey, issueID, stateID string) error {
	const query = `mutation($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) { success issue { identifier state { name } } }
}`
	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	err := c.graphql(ctx, apiKey, query, map[string]any{
		"id":    issueID,
		"input": map[string]any{"stateId": stateID},
	}, &out)
	if err != nil {
		return err
	}
	if !out.IssueUpdate.Success {
		return errors.New("Linear issueUpdate returned success=false")
	}
	return nil
}

func (c *Client) addIssueLabels(ctx context.Context, apiKey, issueID string, existingIDs, addIDs []string) error {
	ids := mergeIDs(existingIDs, addIDs)
	if len(ids) == 0 {
		return nil
	}
	const query = `mutation($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) { success issue { identifier labels { nodes { name } } } }
}`
	var out struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	err := c.graphql(ctx, apiKey, query, map[string]any{
		"id":    issueID,
		"input": map[string]any{"labelIds": ids},
	}, &out)
	if err != nil {
		return err
	}
	if !out.IssueUpdate.Success {
		return errors.New("Linear issueUpdate returned success=false")
	}
	return nil
}

func (c *Client) createComment(ctx context.Context, apiKey, issueID, body string) error {
	const query = `mutation($input: CommentCreateInput!) {
  commentCreate(input: $input) { success comment { id url } }
}`
	var out struct {
		CommentCreate struct {
			Success bool `json:"success"`
		} `json:"commentCreate"`
	}
	err := c.graphql(ctx, apiKey, query, map[string]any{
		"input": map[string]any{"issueId": issueID, "body": body},
	}, &out)
	if err != nil {
		return err
	}
	if !out.CommentCreate.Success {
		return errors.New("Linear commentCreate returned success=false")
	}
	return nil
}

func (c *Client) graphql(ctx context.Context, apiKey, query string, variables map[string]any, out any) error {
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Linear GraphQL HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
		return errors.New("Linear GraphQL response missing data")
	}
	return json.Unmarshal(envelope.Data, out)
}

func appendAudit(teamDir string, j *job.Job, req Request, result Result) error {
	return appendProviderAudit(teamDir, j, req, result, "linear")
}

func appendProviderAudit(teamDir string, j *job.Job, req Request, result Result, provider string) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "pm"
	}
	eventType := provider + "_writeback"
	if result.Skipped {
		eventType = provider + "_writeback_skipped"
	} else if result.Error != "" {
		eventType = provider + "_writeback_failed"
	}
	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		actor = "daemon"
	}
	data := map[string]string{
		"action": string(req.Action),
	}
	if result.Issue != "" {
		data["issue"] = result.Issue
	}
	if result.State != "" {
		data["state"] = result.State
	}
	if result.Comment {
		data["comment"] = "true"
	}
	if result.Labels != "" {
		data["labels"] = result.Labels
	}
	if result.Project != "" {
		data["project"] = result.Project
	}
	if result.ProjectStatus != "" {
		data["project_status"] = result.ProjectStatus
	}
	if result.Error != "" {
		data["error"] = result.Error
	}
	message := strings.TrimSpace(result.Message)
	if message == "" {
		message = eventType
	}
	return job.AppendSnapshotEvent(teamDir, j, eventType, actor, message, data)
}
