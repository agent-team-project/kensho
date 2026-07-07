package pmprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type linearTicketRecord struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	State      struct {
		Name string `json:"name"`
	} `json:"state"`
	Labels struct {
		Nodes []linearNode `json:"nodes"`
	} `json:"labels"`
}

func (c *Client) ApplyTicket(ctx context.Context, teamDir string, req TicketRequest) TicketResult {
	result := TicketResult{Provider: ProviderLinear, Action: req.Action, Labels: cleanStrings(req.Labels)}
	cfg, skip, err := loadConfig(teamDir)
	if err != nil {
		result.Error = err.Error()
		result.Message = "linear ticket action failed"
		return result
	}
	if skip != "" {
		result.Skipped = true
		result.Message = skip
		return result
	}
	apiKey, err := c.resolveAPIKey(teamDir)
	if err != nil {
		if errors.Is(err, errNoAPIKey) {
			result.Skipped = true
			result.Message = err.Error()
			return result
		}
		result.Error = err.Error()
		result.Message = "linear ticket action failed"
		return result
	}
	ctx, cancel := contextWithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch req.Action {
	case TicketCreate:
		return c.applyLinearTicketCreate(ctx, apiKey, cfg, req, result)
	case TicketUpdate:
		return c.applyLinearTicketUpdate(ctx, apiKey, cfg, req, result)
	case TicketComment:
		return c.applyLinearTicketComment(ctx, apiKey, cfg, req, result)
	case TicketClose:
		return c.applyLinearTicketClose(ctx, apiKey, cfg, req, result)
	default:
		result.Skipped = true
		result.Message = "linear ticket action is not supported"
		return result
	}
}

func (c *Client) applyLinearTicketCreate(ctx context.Context, apiKey string, cfg config, req TicketRequest, result TicketResult) TicketResult {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		result.Error = "ticket title is required"
		result.Message = "linear ticket create failed"
		return result
	}
	input := map[string]any{
		"teamId": strings.TrimSpace(cfg.Linear.TeamID),
		"title":  title,
	}
	if body := strings.TrimSpace(req.Body); body != "" {
		input["description"] = body
	}
	if state := strings.TrimSpace(req.State); state != "" || len(result.Labels) > 0 {
		lookup, err := c.lookupTeamStatesAndLabels(ctx, apiKey, cfg.Linear.TeamID)
		if err != nil {
			result.Error = err.Error()
			result.Message = "linear ticket create failed"
			return result
		}
		if state != "" {
			stateID := workflowStateID(lookup, state)
			if stateID == "" {
				result.Error = fmt.Sprintf("Linear workflow state %q not found", state)
				result.Message = "linear ticket create failed"
				return result
			}
			input["stateId"] = stateID
			result.State = state
		}
		labelIDs := workflowLabelIDs(lookup, result.Labels)
		if len(labelIDs) != len(result.Labels) {
			result.Error = fmt.Sprintf("Linear labels not found: %s", strings.Join(result.Labels, ", "))
			result.Message = "linear ticket create failed"
			return result
		}
		if len(labelIDs) > 0 {
			input["labelIds"] = labelIDs
		}
	}
	record, err := c.createLinearTicket(ctx, apiKey, input)
	if err != nil {
		result.Error = err.Error()
		result.Message = "linear ticket create failed"
		return result
	}
	return finishLinearTicketResult(result, record, true, false, "linear ticket created")
}

func (c *Client) applyLinearTicketUpdate(ctx context.Context, apiKey string, cfg config, req TicketRequest, result TicketResult) TicketResult {
	issue := strings.TrimSpace(req.Ticket)
	if issue == "" {
		result.Error = "ticket identifier is required"
		result.Message = "linear ticket update failed"
		return result
	}
	lookup, err := c.lookupIssueAndStates(ctx, apiKey, cfg.Linear.TeamID, issue)
	if err != nil {
		result.Error = err.Error()
		result.Message = "linear ticket update failed"
		return result
	}
	issueID := strings.TrimSpace(lookup.Issue.ID)
	if issueID == "" {
		result.Error = "Linear issue not found"
		result.Message = "linear ticket update failed"
		return result
	}
	input, stateName, err := linearTicketUpdateInput(lookup, req, result.Labels)
	if err != nil {
		result.Error = err.Error()
		result.Message = "linear ticket update failed"
		return result
	}
	if len(input) == 0 {
		result.Error = "no ticket update fields provided"
		result.Message = "linear ticket update failed"
		return result
	}
	record, err := c.updateLinearTicket(ctx, apiKey, issueID, input)
	if err != nil {
		result.Error = err.Error()
		result.Message = "linear ticket update failed"
		return result
	}
	if stateName != "" {
		result.State = stateName
	}
	return finishLinearTicketResult(result, record, true, false, "linear ticket updated")
}

func (c *Client) applyLinearTicketComment(ctx context.Context, apiKey string, cfg config, req TicketRequest, result TicketResult) TicketResult {
	issue := strings.TrimSpace(req.Ticket)
	if issue == "" {
		result.Error = "ticket identifier is required"
		result.Message = "linear ticket comment failed"
		return result
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		result.Error = "comment body is required"
		result.Message = "linear ticket comment failed"
		return result
	}
	lookup, err := c.lookupIssueAndStates(ctx, apiKey, cfg.Linear.TeamID, issue)
	if err != nil {
		result.Error = err.Error()
		result.Message = "linear ticket comment failed"
		return result
	}
	issueID := strings.TrimSpace(lookup.Issue.ID)
	if issueID == "" {
		result.Error = "Linear issue not found"
		result.Message = "linear ticket comment failed"
		return result
	}
	if err := c.createComment(ctx, apiKey, issueID, body); err != nil {
		result.Error = err.Error()
		result.Message = "linear ticket comment failed"
		return result
	}
	result.Issue = strings.TrimSpace(lookup.Issue.Identifier)
	result.Comment = true
	result.Changed = true
	result.Message = "linear ticket commented"
	return result
}

func (c *Client) applyLinearTicketClose(ctx context.Context, apiKey string, cfg config, req TicketRequest, result TicketResult) TicketResult {
	state := strings.TrimSpace(req.State)
	if state == "" {
		state = strings.TrimSpace(cfg.Linear.ClosedState)
	}
	if state == "" {
		result.Error = "linear ticket close requires --state or [linear].closed_state"
		result.Message = "linear ticket close failed"
		return result
	}
	updateReq := req
	updateReq.Action = TicketUpdate
	updateReq.State = state
	updateReq.Title = ""
	updateReq.Body = ""
	updateReq.Labels = nil
	updateResult := c.applyLinearTicketUpdate(ctx, apiKey, cfg, updateReq, result)
	updateResult.Action = TicketClose
	if updateResult.Error != "" || updateResult.Skipped {
		updateResult.Message = "linear ticket close failed"
		return updateResult
	}
	if body := strings.TrimSpace(req.Body); body != "" {
		commentReq := TicketRequest{Action: TicketComment, Ticket: req.Ticket, Body: body}
		commentResult := c.applyLinearTicketComment(ctx, apiKey, cfg, commentReq, TicketResult{Provider: ProviderLinear, Action: TicketClose})
		if commentResult.Error != "" || commentResult.Skipped {
			commentResult.Action = TicketClose
			commentResult.Issue = updateResult.Issue
			commentResult.State = updateResult.State
			commentResult.Message = "linear ticket close failed"
			return commentResult
		}
		updateResult.Comment = true
	}
	updateResult.State = state
	updateResult.Message = "linear ticket closed"
	return updateResult
}

func linearTicketUpdateInput(lookup issueStateLookup, req TicketRequest, labels []string) (map[string]any, string, error) {
	input := map[string]any{}
	if title := strings.TrimSpace(req.Title); title != "" {
		input["title"] = title
	}
	if body := strings.TrimSpace(req.Body); body != "" {
		input["description"] = body
	}
	state := strings.TrimSpace(req.State)
	if state != "" {
		stateID := workflowStateID(lookup, state)
		if stateID == "" {
			return nil, "", fmt.Errorf("Linear workflow state %q not found", state)
		}
		input["stateId"] = stateID
	}
	labelIDs := workflowLabelIDs(lookup, labels)
	if len(labelIDs) != len(labels) {
		return nil, "", fmt.Errorf("Linear labels not found: %s", strings.Join(labels, ", "))
	}
	if len(labelIDs) > 0 {
		input["labelIds"] = mergeIDs(existingIssueLabelIDs(lookup), labelIDs)
	}
	return input, state, nil
}

func (c *Client) lookupTeamStatesAndLabels(ctx context.Context, apiKey, teamID string) (issueStateLookup, error) {
	const query = `query($teamId: ID!) {
  workflowStates(filter: { team: { id: { eq: $teamId } } }) { nodes { id name } }
  issueLabels(filter: { team: { id: { eq: $teamId } } }) { nodes { id name } }
}`
	var out struct {
		WorkflowStates struct {
			Nodes []linearNode `json:"nodes"`
		} `json:"workflowStates"`
		IssueLabels struct {
			Nodes []linearNode `json:"nodes"`
		} `json:"issueLabels"`
	}
	if err := c.graphql(ctx, apiKey, query, map[string]any{"teamId": teamID}, &out); err != nil {
		return issueStateLookup{}, err
	}
	return issueStateLookup{WorkflowStates: out.WorkflowStates, IssueLabels: out.IssueLabels}, nil
}

func (c *Client) createLinearTicket(ctx context.Context, apiKey string, input map[string]any) (linearTicketRecord, error) {
	const query = `mutation($input: IssueCreateInput!) {
  issueCreate(input: $input) { success issue { id identifier url title state { name } labels { nodes { id name } } } }
}`
	var out struct {
		IssueCreate struct {
			Success bool               `json:"success"`
			Issue   linearTicketRecord `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := c.graphql(ctx, apiKey, query, map[string]any{"input": input}, &out); err != nil {
		return linearTicketRecord{}, err
	}
	if !out.IssueCreate.Success {
		return linearTicketRecord{}, errors.New("Linear issueCreate returned success=false")
	}
	return out.IssueCreate.Issue, nil
}

func (c *Client) updateLinearTicket(ctx context.Context, apiKey, issueID string, input map[string]any) (linearTicketRecord, error) {
	const query = `mutation($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) { success issue { id identifier url title state { name } labels { nodes { id name } } } }
}`
	var out struct {
		IssueUpdate struct {
			Success bool               `json:"success"`
			Issue   linearTicketRecord `json:"issue"`
		} `json:"issueUpdate"`
	}
	if err := c.graphql(ctx, apiKey, query, map[string]any{"id": issueID, "input": input}, &out); err != nil {
		return linearTicketRecord{}, err
	}
	if !out.IssueUpdate.Success {
		return linearTicketRecord{}, errors.New("Linear issueUpdate returned success=false")
	}
	return out.IssueUpdate.Issue, nil
}

func finishLinearTicketResult(result TicketResult, record linearTicketRecord, changed, comment bool, message string) TicketResult {
	result.Issue = strings.TrimSpace(record.Identifier)
	result.URL = strings.TrimSpace(record.URL)
	result.Title = strings.TrimSpace(record.Title)
	if state := strings.TrimSpace(record.State.Name); state != "" {
		result.State = state
	}
	if labels := linearRecordLabels(record); len(labels) > 0 {
		result.Labels = labels
	}
	result.Changed = changed
	result.Comment = comment
	result.Message = message
	return result
}

func linearRecordLabels(record linearTicketRecord) []string {
	labels := make([]string, 0, len(record.Labels.Nodes))
	for _, label := range record.Labels.Nodes {
		if name := strings.TrimSpace(label.Name); name != "" {
			labels = append(labels, name)
		}
	}
	return labels
}
