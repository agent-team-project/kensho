package daemon

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/origin"
	"github.com/jamesaud/agent-team/internal/topology"
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

// AuthorityAuditOptions describes one audited daemon or CLI action.
type AuthorityAuditOptions struct {
	TeamDir    string
	DaemonRoot string
	Topology   *topology.Topology
	Actor      origin.Envelope
	Verb       string
	Resource   string
	JobID      string
	EventActor string
}

// AuditAuthority appends audit-only authority_violation events for disallowed
// actions. It never blocks the caller's mutation; failures to write observability
// events are intentionally ignored.
func AuditAuthority(opts AuthorityAuditOptions) {
	topo := opts.Topology
	if topo == nil || topo.Authority == nil || !topo.Authority.Configured() {
		return
	}
	actor := opts.Actor.Clean()
	verb := strings.TrimSpace(opts.Verb)
	resource := strings.TrimSpace(opts.Resource)
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
	jobID := strings.TrimSpace(actor.Job)
	if jobID == "" {
		jobID = strings.TrimSpace(opts.JobID)
	}
	daemonRoot := strings.TrimSpace(opts.DaemonRoot)
	if daemonRoot != "" {
		_ = AppendLifecycleEvent(daemonRoot, &LifecycleEvent{
			Action:   authorityViolationAction,
			Instance: actor.Instance,
			Agent:    actor.Agent,
			Job:      jobID,
			Origin:   actor,
			Message:  message,
		})
	}
	eventActor := strings.TrimSpace(opts.EventActor)
	if eventActor == "" {
		eventActor = "daemon"
	}
	if strings.TrimSpace(opts.TeamDir) != "" && jobID != "" {
		_ = jobstore.AppendEvent(opts.TeamDir, &jobstore.Event{
			JobID:    jobID,
			Type:     authorityViolationAction,
			Instance: actor.Instance,
			Message:  message,
			Actor:    eventActor,
			Origin:   actor,
			Data:     data,
		})
	}
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
	daemonRoot := ""
	if a.mgr != nil {
		daemonRoot = a.mgr.daemonRoot
	}
	AuditAuthority(AuthorityAuditOptions{
		TeamDir:    a.teamDir,
		DaemonRoot: daemonRoot,
		Topology:   topo,
		Actor:      actor,
		Verb:       verb,
		Resource:   resource,
		EventActor: "daemon",
	})
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
