package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/resource"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func (r *EventResolver) backfillDispatchPayloadResourceURIs(inst *topology.Instance, instance string, payload map[string]any, branch, workspace string) {
	if payload == nil {
		return
	}
	deployment, _ := resource.DeploymentFromTeamDir(r.teamDir)
	deploymentID := deploymentIDForDispatchPayload(payload, deployment)
	if deploymentID == "" {
		return
	}
	payloadSetStringIfEmpty(payload, "deployment_uri", resource.DeploymentURI(deploymentID))
	payloadSetStringIfEmpty(payload, "deployment_parent_uri", deploymentParentURIForDispatchPayload(deploymentID, deployment))
	payloadSetStringIfEmpty(payload, "instance_uri", resource.InstanceURI(deploymentID, instance))
	declared := instance
	if inst != nil && strings.TrimSpace(inst.Name) != "" {
		declared = inst.Name
	}
	specURI := resource.InstanceURI(deploymentID, declared)
	if jobID := eventJobID(payload); jobID != "" {
		payloadSetStringIfEmpty(payload, "job_uri", resource.JobURI(deploymentID, jobID))
		if stepID := payloadString(payload, "pipeline_step"); stepID != "" {
			specURI = resource.StepURI(deploymentID, jobID, stepID)
		}
	}
	payloadSetStringIfEmpty(payload, "spec_uri", specURI)
	workspaceURI := resource.WorkspaceURIFor(deploymentID, workspace, branch, eventJobID(payload), instance)
	if payloadString(payload, "workspace") == "repo" {
		workspaceURI = resource.WorkspaceURI(deploymentID, "repo")
	}
	payloadSetStringIfEmpty(payload, "workspace_uri", workspaceURI)
	payloadSetStringIfEmpty(payload, "state_uri", resource.StateURI(deploymentID, instance))
}

func deploymentIDForDispatchPayload(payload map[string]any, fallback resource.Deployment) string {
	if parsed, err := resource.Parse(payloadString(payload, "deployment_uri")); err == nil {
		return strings.TrimSpace(parsed.DeploymentID)
	}
	return strings.TrimSpace(fallback.ID)
}

func deploymentParentURIForDispatchPayload(deploymentID string, fallback resource.Deployment) string {
	fallbackID := strings.TrimSpace(fallback.ID)
	if deploymentID == "" || deploymentID == fallbackID {
		return strings.TrimSpace(fallback.ParentURI)
	}
	return strings.TrimSpace(fallback.URI)
}

func payloadSetStringIfEmpty(payload map[string]any, key, value string) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(payloadString(payload, key)) != "" {
		return
	}
	payload[key] = value
}

func (r *EventResolver) teamForInstance(instance string, payload map[string]any) string {
	if team := payloadString(payload, "team"); team != "" {
		return team
	}
	if r == nil || r.topo == nil {
		return ""
	}
	for _, team := range r.topo.SortedTeams() {
		for _, name := range team.Instances {
			if name == instance {
				return team.Name
			}
		}
	}
	return ""
}

func (r *EventResolver) teamForOrigin(instance string, payload map[string]any) string {
	if r == nil || r.topo == nil {
		return ""
	}
	// Origin ownership is topology-derived only. Inbound payloads may carry
	// provider team keys such as Linear's "SQU", but those are trigger context,
	// not agent-team ownership.
	instance = strings.TrimSpace(instance)
	// Pipeline ownership is the durable job authority scope. Prefer it over
	// instance membership because an allowed shared step instance can belong to
	// multiple teams while the validated pipeline has one owning team.
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		if team := r.teamForPipeline(pipeline); team != "" {
			return team
		}
	}
	for _, team := range r.topo.SortedTeams() {
		for _, name := range team.Instances {
			if instance == name || strings.HasPrefix(instance, name+"-") {
				return team.Name
			}
		}
	}
	return ""
}

func (r *EventResolver) teamForPipeline(pipeline string) string {
	pipeline = strings.TrimSpace(pipeline)
	if pipeline == "" || r == nil || r.topo == nil {
		return ""
	}
	return r.topo.TeamForPipeline(pipeline)
}

func (r *EventResolver) originForEvent(inst *topology.Instance, instance, eventType string, payload map[string]any) origin.Envelope {
	declared := instance
	agent := ""
	if inst != nil {
		declared = inst.Name
		// The origin agent is the resolved agent TEMPLATE (e.g. "reviewer"),
		// never the dispatch target alias (e.g. "platform-reviewer") — the
		// shipped authority allowlists are keyed by agent type, and payload
		// fields must not influence identity (same rule as origin team).
		agent = inst.Agent
	}
	if agent == "" {
		agent = firstPayloadString(payload, "agent", "target")
	}
	return origin.Envelope{
		Project:  projectIDForTeamDir(r.teamDir),
		Team:     r.teamForOrigin(declared, payload),
		Instance: instance,
		Agent:    agent,
		Job:      eventJobID(payload),
		Trigger:  origin.TriggerFromEvent(eventType, payload),
		Build:    buildinfo.Current("").Display(),
	}.WithResourceURIs()
}

func (r *EventResolver) originForPayload(instance string, payload map[string]any) origin.Envelope {
	trigger := payloadString(payload, "trigger")
	if trigger == "" {
		trigger = origin.TriggerFromEvent("", payload)
	}
	return origin.Envelope{
		Project:  projectIDForTeamDir(r.teamDir),
		Team:     r.teamForOrigin(instance, payload),
		Instance: instance,
		Agent:    firstPayloadString(payload, "target", "agent"),
		Job:      eventJobID(payload),
		Trigger:  trigger,
		Build:    buildinfo.Current("").Display(),
	}.WithResourceURIs()
}

func projectIDForTeamDir(teamDir string) string {
	id, _ := origin.ProjectID(teamDir)
	return id
}

func originContextEnv(env origin.Envelope) []string {
	env = env.Clean()
	out := []string{}
	if env.Project != "" {
		out = append(out, "AGENT_TEAM_PROJECT="+env.Project)
	}
	if env.DeploymentURI != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_DEPLOYMENT_URI="+env.DeploymentURI)
	}
	if env.Team != "" {
		out = append(out, "AGENT_TEAM_TEAM="+env.Team)
	}
	if env.InstanceURI != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_INSTANCE_URI="+env.InstanceURI)
	}
	if env.JobURI != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_JOB_URI="+env.JobURI)
	}
	if env.Trigger != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_TRIGGER="+env.Trigger)
	}
	if env.Build != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_BUILD="+env.Build)
	}
	if env.Agent != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_AGENT="+env.Agent)
	}
	if env.Job != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_JOB="+env.Job)
	}
	return out
}

func dispatchContextEnv(repoRoot string, payload map[string]any, branch, worktreePath string) []string {
	env := []string{}
	if repoRoot = strings.TrimSpace(repoRoot); repoRoot != "" {
		env = append(env, "MAIN_REPO="+repoRoot)
	}
	if id := eventJobID(payload); id != "" {
		env = append(env, "AGENT_TEAM_JOB_ID="+id)
	}
	if uri := payloadString(payload, "job_uri"); uri != "" {
		env = append(env, "AGENT_TEAM_JOB_URI="+uri)
	}
	if kind := payloadJobKind(payload); kind != "" {
		env = append(env, "AGENT_TEAM_JOB_KIND="+kind)
	}
	if deliverable := normalizeDeliveryArtifactContract(firstPayloadString(payload, "deliverable", "delivery_contract")); deliverable != "" {
		env = append(env, "AGENT_TEAM_DELIVERABLE="+deliverable)
		if path := deliveryReportArtifactPath(deliverable); path != "" {
			env = append(env, "AGENT_TEAM_REPORT_PATH="+path)
		}
	}
	if ticket := payloadString(payload, "ticket"); ticket != "" {
		env = append(env, "AGENT_TEAM_TICKET="+ticket)
	}
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		env = append(env, "AGENT_TEAM_TICKET_URL="+ticketURL)
	}
	if epic := payloadString(payload, "epic"); epic != "" {
		env = append(env, "AGENT_TEAM_EPIC="+epic)
	}
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		env = append(env, "AGENT_TEAM_PIPELINE="+pipeline)
	}
	if step := payloadString(payload, "pipeline_step"); step != "" {
		env = append(env, "AGENT_TEAM_PIPELINE_STEP="+step)
	}
	if attempt := payloadAttempt(payload); attempt > 0 {
		env = append(env, fmt.Sprintf("AGENT_TEAM_ATTEMPT=%d", attempt))
	}
	if head := payloadString(payload, "head"); head != "" {
		env = append(env, "AGENT_TEAM_HEAD="+head)
	}
	if tokens := payloadBudgetTokens(payload); tokens > 0 {
		env = append(env, fmt.Sprintf("AGENT_TEAM_BUDGET_TOKENS=%d", tokens))
	}
	if timeBudget := payloadString(payload, "budget_time"); timeBudget != "" {
		env = append(env, "AGENT_TEAM_BUDGET_TIME="+timeBudget)
	}
	if pr := firstPayloadString(payload, "pr_url", "pr"); pr != "" {
		env = append(env, "AGENT_TEAM_PR="+pr)
	}
	if branch == "" {
		branch = payloadString(payload, "branch")
	}
	if worktreePath == "" {
		worktreePath = payloadString(payload, "worktree")
	}
	if branch != "" {
		env = append(env, "AGENT_TEAM_BRANCH="+branch)
	}
	if worktreePath != "" {
		env = append(env, "AGENT_TEAM_WORKTREE="+worktreePath)
	}
	for _, item := range []struct {
		key string
		env string
	}{
		{"deployment_uri", "AGENT_TEAM_DEPLOYMENT_URI"},
		{"deployment_parent_uri", "AGENT_TEAM_DEPLOYMENT_PARENT_URI"},
		{"charter_uri", "AGENT_TEAM_CHARTER_URI"},
		{"child_deployment_uri", "AGENT_TEAM_CHILD_DEPLOYMENT_URI"},
		{"capability_uri", "AGENT_TEAM_CAPABILITY_URI"},
		{"instance_uri", "AGENT_TEAM_INSTANCE_URI"},
		{"spec_uri", "AGENT_TEAM_SPEC_URI"},
		{"workspace_uri", "AGENT_TEAM_WORKSPACE_URI"},
		{"state_uri", "AGENT_TEAM_STATE_URI"},
	} {
		if value := payloadString(payload, item.key); value != "" {
			env = append(env, item.env+"="+value)
		}
	}
	return env
}

// authorityForInstance resolves the closed-world verb allowlist baked into the
// generated shim. enforce is true only when topology explicitly opts into
// authority enforcement; audit mode keeps the shim pass-through.
func (r *EventResolver) authorityForInstance(agentName, instance string) (allow []string, enforce bool, strict bool) {
	r.mu.Lock()
	topo := r.topo
	r.mu.Unlock()
	if topo == nil || topo.Authority == nil || !topo.Authority.Enforced() {
		return nil, false, false
	}
	return topo.AuthorityAllowlistForInstance(instance, strings.TrimSpace(agentName)), true, false
}

func (r *EventResolver) runtimeAuthorityForInstance(agentName, instance string, payload map[string]any) (allow []string, enforce bool, strict bool) {
	if allow, ok := r.charterAuthorityForPayload(instance, payload); ok {
		return allow, true, true
	}
	if r.payloadRequiresCharterAuthority(instance, payload) {
		return nil, true, true
	}
	return r.authorityForInstance(agentName, instance)
}

func (r *EventResolver) charterAuthorityForPayload(instance string, payload map[string]any) ([]string, bool) {
	if payload == nil || r == nil || r.mgr == nil {
		return nil, false
	}
	charterID := charterIDFromURI(payloadString(payload, "charter_uri"))
	if charterID == "" {
		return nil, false
	}
	charter, err := ReadTeamCharter(r.mgr.daemonRoot, charterID)
	if err != nil || charter == nil {
		return nil, false
	}
	if teamCharterTerminal(charter.State) {
		return nil, false
	}
	if strings.TrimSpace(charter.Instance) != strings.TrimSpace(instance) {
		return nil, false
	}
	active, err := ReadTeamCharterByInstance(r.mgr.daemonRoot, instance)
	if err != nil || active == nil || active.ID != charter.ID {
		return nil, false
	}
	if capabilityURI := payloadString(payload, "capability_uri"); capabilityURI != "" && capabilityURI != charter.Authority.CapabilityURI {
		return nil, false
	}
	if deploymentURI := payloadString(payload, "deployment_uri"); deploymentURI != "" && deploymentURI != charter.ChildDeploymentURI {
		return nil, false
	}
	return grantedVerbsFromAuthority(charter.Authority), true
}

func (r *EventResolver) payloadRequiresCharterAuthority(instance string, payload map[string]any) bool {
	if payloadReferencesTeamCharter(payload) {
		return true
	}
	if r == nil || r.mgr == nil || strings.TrimSpace(instance) == "" {
		return false
	}
	if marker, err := readCharteredInstanceMarker(r.mgr.daemonRoot, instance); err == nil && marker.Chartered {
		return true
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if _, err := ReadTeamCharterByInstance(r.mgr.daemonRoot, instance); err == nil {
		return true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return false
}

func payloadReferencesTeamCharter(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	if payloadString(payload, "charter_uri") != "" ||
		payloadString(payload, "capability_uri") != "" ||
		payloadString(payload, "child_deployment_uri") != "" {
		return true
	}
	return payloadString(payload, "relationship") == childDeploymentRelationshipEphemeralTeam
}

func charterIDFromURI(uri string) string {
	parsed, err := resource.Parse(strings.TrimSpace(uri))
	if err != nil || parsed.Kind != resource.KindCharter {
		return ""
	}
	return parsed.ID
}
