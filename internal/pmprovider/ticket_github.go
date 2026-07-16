package pmprovider

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"
)

const githubIssueReferenceError = "GitHub issue reference is required (accepted forms: GH-N, #N, N, owner/repo#N, owner/repo/issues/N, or a canonical GitHub issue URL)"

type githubTicketRecord struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (c *GitHubClient) ApplyTicket(ctx context.Context, teamDir string, req TicketRequest) TicketResult {
	result := TicketResult{Provider: ProviderGitHub, Action: req.Action, Labels: cleanStrings(req.Labels)}
	cfg, skip, err := loadGitHubConfig(teamDir)
	if err != nil {
		result.Error = err.Error()
		result.Message = "github ticket action failed"
		return result
	}
	if skip != "" {
		result.Skipped = true
		result.Message = skip
		return result
	}
	token, err := c.resolveToken(teamDir)
	if err != nil {
		if errors.Is(err, errNoGitHubToken) {
			result.Skipped = true
			result.Message = err.Error()
			return result
		}
		result.Error = err.Error()
		result.Message = "github ticket action failed"
		return result
	}
	ctx, cancel := contextWithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch req.Action {
	case TicketCreate:
		return c.applyGitHubTicketCreate(ctx, token, cfg, req, result)
	case TicketUpdate:
		return c.applyGitHubTicketUpdate(ctx, token, cfg, req, result)
	case TicketComment:
		return c.applyGitHubTicketComment(ctx, token, cfg, req, result)
	case TicketClose:
		return c.applyGitHubTicketClose(ctx, token, cfg, req, result)
	default:
		result.Skipped = true
		result.Message = "github ticket action is not supported"
		return result
	}
}

func (c *GitHubClient) applyGitHubTicketCreate(ctx context.Context, token string, cfg config, req TicketRequest, result TicketResult) TicketResult {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		result.Error = "ticket title is required"
		result.Message = "github ticket create failed"
		return result
	}
	payload := map[string]any{"title": title}
	if body := strings.TrimSpace(req.Body); body != "" {
		payload["body"] = body
	}
	if len(result.Labels) > 0 {
		payload["labels"] = result.Labels
	}
	var record githubTicketRecord
	if err := c.rest(ctx, token, http.MethodPost, path.Join("repos", cfg.GitHub.Owner, cfg.GitHub.Repo, "issues"), payload, &record); err != nil {
		result.Error = err.Error()
		result.Message = "github ticket create failed"
		return result
	}
	if state := strings.TrimSpace(req.State); state != "" && !strings.EqualFold(state, record.State) {
		issue := githubIssueRef{Owner: cfg.GitHub.Owner, Repo: cfg.GitHub.Repo, Number: record.Number}
		if err := c.updateIssueState(ctx, token, issue, state); err != nil {
			result.Error = err.Error()
			result.Message = "github ticket create failed"
			return result
		}
		record.State = state
	}
	return finishGitHubTicketResult(result, cfg, record, true, false, "github ticket created")
}

func (c *GitHubClient) applyGitHubTicketUpdate(ctx context.Context, token string, cfg config, req TicketRequest, result TicketResult) TicketResult {
	issue, ok := parseGitHubIssueRef(req.Ticket, cfg.GitHub.Owner, cfg.GitHub.Repo)
	if !ok {
		result.Error = githubIssueReferenceError
		result.Message = "github ticket update failed"
		return result
	}
	payload := map[string]any{}
	if title := strings.TrimSpace(req.Title); title != "" {
		payload["title"] = title
	}
	if body := strings.TrimSpace(req.Body); body != "" {
		payload["body"] = body
	}
	if state := strings.TrimSpace(req.State); state != "" {
		payload["state"] = state
		result.State = state
	}
	if len(payload) == 0 && len(result.Labels) == 0 {
		result.Error = "no ticket update fields provided"
		result.Message = "github ticket update failed"
		return result
	}
	var record githubTicketRecord
	if len(payload) > 0 {
		if err := c.rest(ctx, token, http.MethodPatch, issuePath(issue), payload, &record); err != nil {
			result.Error = err.Error()
			result.Message = "github ticket update failed"
			return result
		}
	}
	if len(result.Labels) > 0 {
		if err := c.addIssueLabels(ctx, token, issue, result.Labels); err != nil {
			result.Error = err.Error()
			result.Message = "github ticket update failed"
			return result
		}
	}
	if record.Number == 0 {
		record.Number = issue.Number
		record.State = result.State
	}
	return finishGitHubTicketResult(result, cfg, record, true, false, "github ticket updated")
}

func (c *GitHubClient) applyGitHubTicketComment(ctx context.Context, token string, cfg config, req TicketRequest, result TicketResult) TicketResult {
	issue, ok := parseGitHubIssueRef(req.Ticket, cfg.GitHub.Owner, cfg.GitHub.Repo)
	if !ok {
		result.Error = githubIssueReferenceError
		result.Message = "github ticket comment failed"
		return result
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		result.Error = "comment body is required"
		result.Message = "github ticket comment failed"
		return result
	}
	if err := c.createIssueComment(ctx, token, issue, body); err != nil {
		result.Error = err.Error()
		result.Message = "github ticket comment failed"
		return result
	}
	result.Issue = issue.String()
	result.Comment = true
	result.Changed = true
	result.Message = "github ticket commented"
	return result
}

func (c *GitHubClient) applyGitHubTicketClose(ctx context.Context, token string, cfg config, req TicketRequest, result TicketResult) TicketResult {
	issue, ok := parseGitHubIssueRef(req.Ticket, cfg.GitHub.Owner, cfg.GitHub.Repo)
	if !ok {
		result.Error = githubIssueReferenceError
		result.Message = "github ticket close failed"
		return result
	}
	var record githubTicketRecord
	if err := c.rest(ctx, token, http.MethodPatch, issuePath(issue), map[string]any{"state": "closed"}, &record); err != nil {
		result.Error = err.Error()
		result.Message = "github ticket close failed"
		return result
	}
	result = finishGitHubTicketResult(result, cfg, record, true, false, "github ticket closed")
	result.State = "closed"
	if body := strings.TrimSpace(req.Body); body != "" {
		if err := c.createIssueComment(ctx, token, issue, body); err != nil {
			result.Error = err.Error()
			result.Message = "github ticket close failed"
			return result
		}
		result.Comment = true
	}
	return result
}

func finishGitHubTicketResult(result TicketResult, cfg config, record githubTicketRecord, changed, comment bool, message string) TicketResult {
	if record.Number > 0 {
		result.Issue = githubIssueRef{Owner: cfg.GitHub.Owner, Repo: cfg.GitHub.Repo, Number: record.Number}.String()
	}
	result.URL = strings.TrimSpace(record.HTMLURL)
	result.Title = strings.TrimSpace(record.Title)
	if state := strings.TrimSpace(record.State); state != "" {
		result.State = state
	}
	if labels := githubRecordLabels(record); len(labels) > 0 {
		result.Labels = labels
	}
	result.Changed = changed
	result.Comment = comment
	result.Message = message
	return result
}

func githubRecordLabels(record githubTicketRecord) []string {
	labels := make([]string, 0, len(record.Labels))
	for _, label := range record.Labels {
		if name := strings.TrimSpace(label.Name); name != "" {
			labels = append(labels, name)
		}
	}
	return labels
}
