package pmprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/intake"
)

type ProviderName string

const (
	ProviderNone   ProviderName = "none"
	ProviderLinear ProviderName = "linear"
)

type Provider interface {
	Name() ProviderName
	NormalizeIntake([]byte) (*intake.Event, error)
	ResolveActorID(teamDir string) (string, error)
	SelfStatusChangeForActor(ev *intake.Event, actorID string) (bool, string)
	WriteBack(ctx context.Context, teamDir string, req Request) Result
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
	provider, source := ConfiguredProviderNameWithSource(cfg.PM.Provider, cfg.Team.PMTool)
	return Config{Provider: provider, Source: source}, nil
}

func ConfiguredProviderName(pmProvider, teamPMTool string) ProviderName {
	name, _ := ConfiguredProviderNameWithSource(pmProvider, teamPMTool)
	return name
}

func ConfiguredProviderNameWithSource(pmProvider, teamPMTool string) (ProviderName, string) {
	if raw := strings.TrimSpace(pmProvider); raw != "" {
		return NormalizeProviderName(raw), "pm.provider"
	}
	if raw := strings.TrimSpace(teamPMTool); raw != "" {
		return NormalizeProviderName(raw), "team.pm_tool"
	}
	return ProviderNone, ""
}

func NormalizeProviderName(raw string) ProviderName {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none":
		return ProviderNone
	case "linear":
		return ProviderLinear
	default:
		return ProviderName(strings.ToLower(strings.TrimSpace(raw)))
	}
}

func KnownProvider(name ProviderName) bool {
	switch name {
	case ProviderNone, ProviderLinear:
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
