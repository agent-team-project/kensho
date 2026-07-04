package daemon

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/origin"
)

const authorityViolationAction = "authority_violation"

type authorityAuditor struct {
	mgr     *InstanceManager
	events  *EventResolver
	teamDir string
}

func newAuthorityAuditor(mgr *InstanceManager, events *EventResolver, teamDir string) *authorityAuditor {
	return &authorityAuditor{mgr: mgr, events: events, teamDir: teamDir}
}

func (a *authorityAuditor) audit(r *http.Request, verb, resource string, fallback origin.Envelope) {
	if a == nil || a.events == nil {
		return
	}
	topo := a.events.Topology()
	if topo == nil || !topo.Authority.Configured() {
		return
	}
	actor := a.originForRequest(r, fallback)
	if topo.Authority.Allows(actor.Agent, actor.Team, verb) {
		return
	}
	message := authorityViolationMessage(actor, verb, resource)
	data := map[string]string{
		"verb":     verb,
		"resource": resource,
		"agent":    actor.Agent,
		"team":     actor.Team,
	}
	if a.mgr != nil {
		_ = AppendLifecycleEvent(a.mgr.daemonRoot, &LifecycleEvent{
			Action:   authorityViolationAction,
			Instance: actor.Instance,
			Agent:    actor.Agent,
			Job:      actor.Job,
			Origin:   actor,
			Message:  message,
		})
	}
	if strings.TrimSpace(a.teamDir) != "" && strings.TrimSpace(actor.Job) != "" {
		_ = jobstore.AppendEvent(a.teamDir, &jobstore.Event{
			JobID:    actor.Job,
			Type:     authorityViolationAction,
			Instance: actor.Instance,
			Message:  message,
			Actor:    "daemon",
			Origin:   actor,
			Data:     data,
		})
	}
}

func (a *authorityAuditor) originForRequest(r *http.Request, fallback origin.Envelope) origin.Envelope {
	var fromHeader origin.Envelope
	if r != nil {
		fromHeader, _ = origin.ParseHeaderValue(r.Header.Get(origin.HeaderName))
	}
	actor := origin.Merge(fromHeader, fallback)
	if a != nil && a.mgr != nil && strings.TrimSpace(actor.Instance) != "" {
		if meta, err := ReadMetadata(a.mgr.daemonRoot, actor.Instance); err == nil && meta != nil {
			metaOrigin := meta.Origin
			if metaOrigin.Agent == "" {
				metaOrigin.Agent = meta.Agent
			}
			if metaOrigin.Instance == "" {
				metaOrigin.Instance = meta.Instance
			}
			actor = origin.Merge(actor, metaOrigin)
		}
	}
	if strings.TrimSpace(actor.Project) == "" && a != nil {
		actor.Project = projectIDForTeamDir(a.teamDir)
	}
	if strings.TrimSpace(actor.Build) == "" {
		actor.Build = buildinfo.Current("").Display()
	}
	return actor.Clean()
}

func authorityViolationMessage(actor origin.Envelope, verb, resource string) string {
	agent := strings.TrimSpace(actor.Agent)
	if agent == "" {
		agent = "unknown"
	}
	team := strings.TrimSpace(actor.Team)
	if team == "" {
		team = "unknown"
	}
	return fmt.Sprintf("authority violation: agent=%s team=%s verb=%s resource=%s", agent, team, verb, resource)
}
