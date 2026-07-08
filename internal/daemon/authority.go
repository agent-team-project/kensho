package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	resuri "github.com/agent-team-project/agent-team/internal/resource"
	"github.com/agent-team-project/agent-team/internal/topology"
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
	Operator   bool
	Verb       string
	Resource   string
	JobID      string
	TargetJob  string
	EventActor string
}

// AuditAuthority appends authority_violation events for disallowed actions. In
// audit mode it never blocks the caller's mutation; in enforce mode it returns
// an error after recording the violation. Failures to write observability events
// are intentionally ignored.
func AuditAuthority(opts AuthorityAuditOptions) error {
	topo := opts.Topology
	if topo == nil || topo.Authority == nil || !topo.Authority.Configured() {
		return nil
	}
	actor := opts.Actor.Clean()
	operator := opts.Operator
	verb := strings.TrimSpace(opts.Verb)
	resource := strings.TrimSpace(opts.Resource)
	if authorityActorIdentityEmpty(actor) && !operator {
		return nil
	}
	targetJob := strings.TrimSpace(opts.TargetJob)
	if targetJob == "" {
		targetJob = strings.TrimSpace(opts.JobID)
	}
	handled, eval, err := charterAuthorityEvaluation(opts, actor, verb, resource, targetJob)
	if err != nil {
		return err
	}
	if !handled {
		decision := authorityDecisionForActor(topo, actor, operator, verb, targetJob)
		eval = topo.Authority.Evaluate(decision)
	}
	if eval.Allowed {
		return nil
	}
	message := authorityViolationMessage(actor, eval, verb, resource)
	data := map[string]string{
		"verb":             verb,
		"resource":         resource,
		"agent":            actor.Agent,
		"team":             actor.Team,
		"instance":         actor.Instance,
		"allowlist_source": eval.SourceDescription(),
		// The actor's ORIGIN job verbatim — the jobstore event's Origin.Job is
		// backfilled from the target job for completeness, but an absent actor
		// job is signal (it is why :own failed) and must stay observable.
		"actor_job": actor.Job,
	}
	jobID := targetJob
	if jobID == "" {
		jobID = strings.TrimSpace(actor.Job)
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
	if topo.Authority.Enforced() {
		return fmt.Errorf("%s", message)
	}
	return nil
}

func charterAuthorityEvaluation(opts AuthorityAuditOptions, actor origin.Envelope, verb, auditResource, targetJob string) (bool, topology.AuthorityEvaluation, error) {
	if opts.Operator || strings.TrimSpace(opts.DaemonRoot) == "" || strings.TrimSpace(actor.Instance) == "" {
		return false, topology.AuthorityEvaluation{}, nil
	}
	marker, markerErr := readCharteredInstanceMarker(opts.DaemonRoot, actor.Instance)
	if markerErr != nil && !errors.Is(markerErr, fs.ErrNotExist) {
		return true, deniedCharterAuthorityEvaluation(actor, verb, targetJob, "charter.metadata"), fmt.Errorf("authority violation: charter marker lookup for instance %s: %w", actor.Instance, markerErr)
	}
	charter, err := ReadTeamCharterByInstance(opts.DaemonRoot, actor.Instance)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if marker.Chartered {
				return true, deniedCharterAuthorityEvaluation(actor, verb, targetJob, "charter.missing"), nil
			}
			return false, topology.AuthorityEvaluation{}, nil
		}
		return true, topology.AuthorityEvaluation{}, fmt.Errorf("authority violation: charter lookup for instance %s: %w", actor.Instance, err)
	}
	if charter == nil {
		if marker.Chartered {
			return true, deniedCharterAuthorityEvaluation(actor, verb, targetJob, "charter.missing"), nil
		}
		return false, topology.AuthorityEvaluation{}, nil
	}
	if teamCharterTerminal(charter.State) {
		return true, deniedCharterAuthorityEvaluation(actor, verb, targetJob, "charter."+charter.ID+".terminal"), nil
	}
	if marker.Chartered {
		if marker.CharterURI != "" && marker.CharterURI != charter.URI {
			return true, deniedCharterAuthorityEvaluation(actor, verb, targetJob, "charter.marker"), nil
		}
		if marker.CapabilityURI != "" && marker.CapabilityURI != charter.Authority.CapabilityURI {
			return true, deniedCharterAuthorityEvaluation(actor, verb, targetJob, "charter.marker"), nil
		}
	}
	eval := topology.AuthorityEvaluation{
		Allowed: true,
		Decision: topology.AuthorityDecision{
			Instance:  actor.Instance,
			Agent:     actor.Agent,
			Team:      actor.Team,
			Verb:      verb,
			ActorJob:  actor.Job,
			TargetJob: targetJob,
		},
		Sources: []string{"charter." + charter.ID},
	}
	if allowed, source := charterGrantAllows(charter, eval.Decision, auditResource); !allowed {
		eval.Allowed = false
		if source != "" {
			eval.Sources = []string{source}
		}
		return true, eval, nil
	}
	return true, eval, nil
}

func deniedCharterAuthorityEvaluation(actor origin.Envelope, verb, targetJob, source string) topology.AuthorityEvaluation {
	return topology.AuthorityEvaluation{
		Allowed: false,
		Decision: topology.AuthorityDecision{
			Instance:  actor.Instance,
			Agent:     actor.Agent,
			Team:      actor.Team,
			Verb:      verb,
			ActorJob:  actor.Job,
			TargetJob: targetJob,
		},
		Sources: []string{source},
	}
}

func charterGrantAllows(charter *TeamCharter, decision topology.AuthorityDecision, auditResource string) (bool, string) {
	if charter == nil {
		return false, "charter"
	}
	verbMatched := false
	for _, grant := range effectiveTeamCharterGrants(charter.Authority) {
		if !authorityAllowedByPatterns([]string{grant.Verb}, decision, true) {
			continue
		}
		verbMatched = true
		if strings.TrimSpace(auditResource) == "" || charteredResourceAllowedByGrant(grant, auditResource) {
			return true, ""
		}
	}
	if verbMatched {
		return false, "charter." + charter.ID + ".resources"
	}
	return false, "charter." + charter.ID
}

func effectiveTeamCharterGrants(authority TeamCharterAuthority) []TeamCharterGrant {
	if len(authority.Grants) > 0 {
		return cleanTeamCharterGrants(authority.Grants)
	}
	resources := cleanStringSet(authority.GrantedResources)
	var grants []TeamCharterGrant
	for _, verb := range cleanStringSet(authority.GrantedVerbs) {
		grants = append(grants, TeamCharterGrant{Verb: verb, Resources: resources})
	}
	return grants
}

func charteredResourceAllowedByGrant(grant TeamCharterGrant, auditResource string) bool {
	auditResource = strings.TrimSpace(auditResource)
	if auditResource == "" {
		return true
	}
	for _, resource := range cleanStringSet(grant.Resources) {
		if charteredResourceGrantMatches(resource, auditResource) {
			return true
		}
	}
	return false
}

func charteredResourceGrantMatches(grant, auditResource string) bool {
	grant = strings.TrimSpace(grant)
	auditResource = strings.TrimSpace(auditResource)
	if grant == "" || auditResource == "" {
		return false
	}
	if grant == auditResource {
		return true
	}
	parsedGrant, err := resuri.Parse(grant)
	if err != nil {
		return false
	}
	if parsedAudit, err := resuri.Parse(auditResource); err == nil {
		return parsedGrant.DeploymentID == parsedAudit.DeploymentID &&
			parsedGrant.Kind == parsedAudit.Kind &&
			parsedGrant.ID == parsedAudit.ID &&
			parsedGrant.Fragment == parsedAudit.Fragment
	}
	return localAuditResourceMatchesGrant(parsedGrant, auditResource)
}

func localAuditResourceMatchesGrant(grant resuri.Parsed, auditResource string) bool {
	switch grant.Kind {
	case resuri.KindJob:
		return auditResource == "job:"+grant.ID || strings.HasPrefix(auditResource, "job:"+grant.ID+":")
	case resuri.KindChannel:
		return auditResource == "channel:"+grant.ID
	case resuri.KindMailbox:
		return auditResource == "inbox:"+grant.ID || auditResource == "mailbox:"+grant.ID
	case resuri.KindInstance:
		return auditResource == "instance:"+grant.ID
	case resuri.KindCharter:
		return auditResource == "charter:"+grant.ID
	case resuri.KindQueue:
		return auditResource == "queue:"+grant.ID
	case resuri.KindOutbox:
		return auditResource == "outbox:"+grant.ID
	case resuri.KindLock:
		return auditResource == "lock:"+grant.ID
	case resuri.KindProject:
		return auditResource == "project:"+grant.ID || auditResource == "deployment:"+grant.ID
	default:
		return false
	}
}

func (a *authorityAuditor) audit(r *http.Request, verb, resource string, fallback origin.Envelope) error {
	if a == nil || a.events == nil {
		return nil
	}
	topo := a.events.Topology()
	if topo == nil || !topo.Authority.Configured() {
		return nil
	}
	actor := a.originForRequest(r, fallback)
	daemonRoot := ""
	if a.mgr != nil {
		daemonRoot = a.mgr.daemonRoot
	}
	return AuditAuthority(AuthorityAuditOptions{
		TeamDir:    a.teamDir,
		DaemonRoot: daemonRoot,
		Topology:   topo,
		Actor:      actor,
		Operator:   trustedBearerOperatorFromRequest(r),
		Verb:       verb,
		Resource:   resource,
		EventActor: "daemon",
	})
}

func (a *authorityAuditor) originForRequest(r *http.Request, fallback origin.Envelope) origin.Envelope {
	var fromHeader origin.Envelope
	var trusted origin.Envelope
	if r != nil {
		fromHeader, _ = origin.ParseHeaderValue(r.Header.Get(origin.HeaderName))
		trusted, _ = trustedBearerOriginFromRequest(r)
	}
	actor := origin.Merge(trusted, origin.Merge(fromHeader, fallback))
	if a != nil && a.events != nil {
		actor = authorityOriginFromTopology(a.events.Topology(), actor)
	}
	if a != nil && a.mgr != nil && strings.TrimSpace(actor.Instance) != "" {
		if meta, err := ReadMetadata(a.mgr.daemonRoot, actor.Instance); err == nil && meta != nil {
			actor = origin.Merge(authorityOriginFromMetadata(meta, a.events), actor)
		}
	}
	if a != nil && a.events != nil {
		actor = authorityOriginFromTopology(a.events.Topology(), actor)
	}
	if strings.TrimSpace(actor.Project) == "" && a != nil {
		actor.Project = projectIDForTeamDir(a.teamDir)
	}
	if strings.TrimSpace(actor.Build) == "" {
		actor.Build = buildinfo.Current("").Display()
	}
	return actor.Clean()
}

func authorityActorIdentityEmpty(actor origin.Envelope) bool {
	actor = actor.Clean()
	return actor.Instance == "" && actor.Agent == "" && actor.Team == ""
}

func authorityDecisionForActor(topo *topology.Topology, actor origin.Envelope, operator bool, verb, targetJob string) topology.AuthorityDecision {
	actor = actor.Clean()
	instance := actor.Instance
	agent := actor.Agent
	team := actor.Team
	if topo != nil && instance != "" {
		if inst := topo.FindRuntimeInstance(instance, ""); inst != nil {
			instance = inst.Name
			if strings.TrimSpace(inst.Agent) != "" {
				agent = strings.TrimSpace(inst.Agent)
			}
		} else if inst := topo.FindRuntimeInstance(instance, agent); inst != nil {
			instance = inst.Name
			if strings.TrimSpace(inst.Agent) != "" {
				agent = strings.TrimSpace(inst.Agent)
			}
		}
		if resolvedTeam := topo.TeamForInstance(actor.Instance); resolvedTeam != "" {
			team = resolvedTeam
		}
	}
	return topology.AuthorityDecision{
		Instance:  instance,
		Agent:     agent,
		Team:      team,
		Operator:  operator,
		Verb:      verb,
		ActorJob:  actor.Job,
		TargetJob: targetJob,
	}
}

func authorityOriginFromTopology(topo *topology.Topology, actor origin.Envelope) origin.Envelope {
	actor = actor.Clean()
	if topo == nil || actor.Instance == "" {
		return actor
	}
	if inst := topo.FindRuntimeInstance(actor.Instance, ""); inst != nil {
		if strings.TrimSpace(inst.Agent) != "" {
			actor.Agent = strings.TrimSpace(inst.Agent)
		}
	} else if inst := topo.FindRuntimeInstance(actor.Instance, actor.Agent); inst != nil {
		if strings.TrimSpace(inst.Agent) != "" {
			actor.Agent = strings.TrimSpace(inst.Agent)
		}
	}
	if team := topo.TeamForInstance(actor.Instance); team != "" {
		actor.Team = team
	}
	return actor.Clean()
}

func authorityOriginFromMetadata(meta *Metadata, events *EventResolver) origin.Envelope {
	if meta == nil {
		return origin.Envelope{}
	}
	out := meta.Origin
	if out.Instance == "" {
		out.Instance = meta.Instance
	}
	if out.Agent == "" {
		out.Agent = meta.Agent
	}
	if out.Job == "" {
		out.Job = meta.Job
	}
	if events != nil {
		if topo := events.Topology(); topo != nil {
			if inst := topo.FindRuntimeInstance(out.Instance, ""); inst != nil && strings.TrimSpace(inst.Agent) != "" {
				out.Agent = strings.TrimSpace(inst.Agent)
			} else if inst := topo.FindRuntimeInstance(out.Instance, out.Agent); inst != nil && strings.TrimSpace(inst.Agent) != "" {
				out.Agent = strings.TrimSpace(inst.Agent)
			}
			if team := topo.TeamForInstance(out.Instance); team != "" {
				out.Team = team
			}
		}
	}
	return out.Clean()
}

func authorityViolationMessage(actor origin.Envelope, eval topology.AuthorityEvaluation, verb, resource string) string {
	agent := strings.TrimSpace(actor.Agent)
	if agent == "" {
		agent = "unknown"
	}
	team := strings.TrimSpace(actor.Team)
	if team == "" {
		team = "unknown"
	}
	instance := strings.TrimSpace(actor.Instance)
	if instance == "" {
		instance = "unknown"
	}
	return fmt.Sprintf("authority violation: verb=%s actor=%s/%s instance=%s resource=%s allowlist_source=%s", verb, team, agent, instance, resource, eval.SourceDescription())
}
