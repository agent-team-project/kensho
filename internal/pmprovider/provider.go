package pmprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/intake"
	"github.com/agent-team-project/agent-team/internal/origin"
)

type ProviderName string

const (
	ProviderNone   ProviderName = "none"
	ProviderLinear ProviderName = "linear"
	ProviderGitHub ProviderName = "github"
)

type Provider interface {
	Name() ProviderName
	NormalizeIntake([]byte) (*intake.Event, error)
	ResolveActorID(teamDir string) (string, error)
	SelfStatusChangeForActor(ev *intake.Event, actorID string) (bool, string)
	WriteBack(ctx context.Context, teamDir string, req Request) Result
	ApplyTicket(ctx context.Context, teamDir string, req TicketRequest) TicketResult
}

type Config struct {
	Provider ProviderName
	Source   string
}

type NoneProvider struct{}

func LoadConfig(teamDir string) (Config, error) {
	var cfg config
	path := filepath.Join(teamDir, "config.toml")
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if os.IsNotExist(err) {
			return Config{Provider: ProviderNone}, nil
		}
		return Config{}, err
	}
	provider := NormalizeProviderName(cfg.PM.Provider)
	source := ""
	if strings.TrimSpace(cfg.PM.Provider) != "" {
		source = "pm.provider"
	}
	return Config{Provider: provider, Source: source}, nil
}

func NormalizeProviderName(raw string) ProviderName {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none":
		return ProviderNone
	case "linear":
		return ProviderLinear
	case "github":
		return ProviderGitHub
	default:
		return ProviderName(strings.ToLower(strings.TrimSpace(raw)))
	}
}

func KnownProvider(name ProviderName) bool {
	switch name {
	case ProviderNone, ProviderLinear, ProviderGitHub:
		return true
	default:
		return false
	}
}

func ForTeamDir(teamDir string) (Provider, error) {
	cfg, err := LoadConfig(teamDir)
	if err != nil {
		return nil, err
	}
	return ForName(cfg.Provider)
}

func ForName(name ProviderName) (Provider, error) {
	switch name {
	case ProviderNone, "":
		return NoneProvider{}, nil
	case ProviderLinear:
		return DefaultClient(), nil
	case ProviderGitHub:
		return DefaultGitHubClient(), nil
	default:
		return nil, fmt.Errorf("unknown PM provider %q", name)
	}
}

func WriteBack(ctx context.Context, teamDir string, req Request) Result {
	provider, err := ForTeamDir(teamDir)
	if err != nil {
		return writeBackConfigError(teamDir, req, err)
	}
	return provider.WriteBack(ctx, teamDir, req)
}

type TicketAction string

const (
	TicketCreate  TicketAction = "create"
	TicketUpdate  TicketAction = "update"
	TicketComment TicketAction = "comment"
	TicketClose   TicketAction = "close"
)

type TicketRequest struct {
	Action TicketAction
	Ticket string
	Title  string
	Body   string
	State  string
	Labels []string
	Actor  string
}

type TicketResult struct {
	Provider ProviderName `json:"provider"`
	Action   TicketAction `json:"action"`
	Issue    string       `json:"issue,omitempty"`
	URL      string       `json:"url,omitempty"`
	Title    string       `json:"title,omitempty"`
	State    string       `json:"state,omitempty"`
	Labels   []string     `json:"labels,omitempty"`
	Comment  bool         `json:"comment,omitempty"`
	Skipped  bool         `json:"skipped,omitempty"`
	Changed  bool         `json:"changed,omitempty"`
	Message  string       `json:"message,omitempty"`
	Error    string       `json:"error,omitempty"`
}

func ApplyTicket(ctx context.Context, teamDir string, req TicketRequest) TicketResult {
	provider, err := ForTeamDir(teamDir)
	if err != nil {
		return TicketResult{
			Action:  req.Action,
			Error:   err.Error(),
			Message: "PM provider ticket action failed",
		}
	}
	req = appendTicketRequestOrigin(teamDir, req)
	return provider.ApplyTicket(ctx, teamDir, req)
}

func (NoneProvider) Name() ProviderName {
	return ProviderNone
}

func (NoneProvider) NormalizeIntake([]byte) (*intake.Event, error) {
	return nil, fmt.Errorf("PM provider %q does not support intake", ProviderNone)
}

func (NoneProvider) ResolveActorID(string) (string, error) {
	return "", nil
}

func (NoneProvider) SelfStatusChangeForActor(*intake.Event, string) (bool, string) {
	return false, ""
}

func (NoneProvider) WriteBack(_ context.Context, teamDir string, req Request) Result {
	result := Result{Action: req.Action}
	finish := func(result Result) Result {
		if req.Job != nil {
			// Preserve phase-a behavior: previous callers always attempted a
			// Linear write-back and audited a Linear skip when PM was disabled.
			result.AuditErr = appendAudit(teamDir, req.Job, req, result)
		}
		return result
	}
	if req.Job == nil {
		result.Skipped = true
		result.Message = "job is required"
		return result
	}
	result.Issue = issueIdentifier(req.Job)
	if result.Issue == "" {
		result.Skipped = true
		result.Message = "job has no Linear ticket identifier"
		return finish(result)
	}
	result.Skipped = true
	result.Message = "Linear not configured for this repo"
	return finish(result)
}

func (NoneProvider) ApplyTicket(_ context.Context, _ string, req TicketRequest) TicketResult {
	return TicketResult{
		Provider: ProviderNone,
		Action:   req.Action,
		Skipped:  true,
		Message:  "PM provider is not configured for this repo",
	}
}

func writeBackConfigError(teamDir string, req Request, err error) Result {
	result := Result{
		Action:  req.Action,
		Error:   err.Error(),
		Message: "PM provider write-back failed",
	}
	if req.Job != nil {
		result.Issue = issueIdentifier(req.Job)
		result.AuditErr = appendAudit(teamDir, req.Job, req, result)
	}
	return result
}

func appendOriginFooter(teamDir string, req Request, body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}
	env := ticketOriginEnvelope(teamDir)
	if req.Job != nil {
		env = req.Job.Origin
		projectID, _ := origin.ProjectID(teamDir)
		env = origin.Merge(env, origin.Envelope{
			Project:  projectID,
			Instance: req.Job.Instance,
			Agent:    req.Job.Target,
			Job:      req.Job.ID,
		})
	}
	return origin.AppendFooter(body, env)
}

func appendTicketRequestOrigin(teamDir string, req TicketRequest) TicketRequest {
	if strings.TrimSpace(req.Body) == "" {
		return req
	}
	switch req.Action {
	case TicketCreate, TicketComment, TicketClose:
		req.Body = origin.AppendFooter(req.Body, ticketOriginEnvelope(teamDir))
	}
	return req
}

func ticketOriginEnvelope(teamDir string) origin.Envelope {
	projectID, _ := origin.ProjectID(teamDir)
	return origin.Merge(origin.Envelope{
		Project:       os.Getenv("AGENT_TEAM_PROJECT"),
		DeploymentURI: os.Getenv("AGENT_TEAM_DEPLOYMENT_URI"),
		Team:          os.Getenv("AGENT_TEAM_TEAM"),
		Instance:      firstNonEmpty(os.Getenv("AGENT_TEAM_ORIGIN_INSTANCE"), os.Getenv("AGENT_TEAM_INSTANCE")),
		InstanceURI:   os.Getenv("AGENT_TEAM_ORIGIN_INSTANCE_URI"),
		Agent:         os.Getenv("AGENT_TEAM_ORIGIN_AGENT"),
		Job:           firstNonEmpty(os.Getenv("AGENT_TEAM_ORIGIN_JOB"), os.Getenv("AGENT_TEAM_JOB_ID")),
		JobURI:        os.Getenv("AGENT_TEAM_ORIGIN_JOB_URI"),
		Trigger:       os.Getenv("AGENT_TEAM_ORIGIN_TRIGGER"),
		Build:         os.Getenv("AGENT_TEAM_ORIGIN_BUILD"),
	}, origin.Envelope{Project: projectID})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
