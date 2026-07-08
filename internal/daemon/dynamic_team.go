package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/budget"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/resource"
	"github.com/agent-team-project/agent-team/internal/topology"
)

const (
	TeamCharterStateChartered = "chartered"
	TeamCharterStateRunning   = "running"
	TeamCharterStateQueued    = "queued"
	TeamCharterStateFailed    = "failed"
	TeamCharterStateReaped    = "reaped"

	childDeploymentRelationshipEphemeralTeam = "ephemeral_team"

	dynamicTeamSpawnFeatureEnv      = "AGENT_TEAM_DYNAMIC_TEAM_SPAWN_ENABLED"
	dynamicTeamSpawnDisabledMessage = "dynamic-team spawn is not enabled in v1 (foundation only; secure-spawn tracked separately)"
)

var ErrDynamicTeamSpawnDisabled = errors.New(dynamicTeamSpawnDisabledMessage)

// TeamSpawnRequest is the narrow first dynamic-team primitive: one parent
// deployment charters one ephemeral child deployment backed by one declared
// ephemeral bootstrap instance.
type TeamSpawnRequest struct {
	Name       string             `json:"name"`
	Target     string             `json:"target"`
	Instance   string             `json:"instance,omitempty"`
	Template   string             `json:"template,omitempty"`
	Goal       TeamSpawnGoal      `json:"goal,omitempty"`
	Parameters map[string]any     `json:"parameters,omitempty"`
	Budget     TeamSpawnBudget    `json:"budget,omitempty"`
	Authority  TeamSpawnAuthority `json:"authority,omitempty"`
	Lifecycle  TeamSpawnLifecycle `json:"lifecycle,omitempty"`
	Payload    map[string]any     `json:"payload,omitempty"`
	Origin     origin.Envelope    `json:"origin,omitempty"`
}

type TeamSpawnGoal struct {
	Summary string   `json:"summary,omitempty"`
	Success []string `json:"success,omitempty"`
}

type TeamSpawnBudget struct {
	Tokens int64  `json:"tokens,omitempty"`
	Time   string `json:"time,omitempty"`
}

type TeamSpawnAuthority struct {
	Verbs     []string `json:"verbs,omitempty"`
	Resources []string `json:"resources,omitempty"`
}

type TeamSpawnLifecycle struct {
	TTL  string `json:"ttl,omitempty"`
	Reap string `json:"reap,omitempty"`
}

type TeamSpawnResult struct {
	Accepted            bool         `json:"accepted"`
	CharterURI          string       `json:"charter_uri"`
	ChildDeploymentURI  string       `json:"child_deployment_uri"`
	CapabilityURI       string       `json:"capability_uri,omitempty"`
	BudgetAllocationURI string       `json:"budget_allocation_uri,omitempty"`
	State               string       `json:"state"`
	Outcome             EventOutcome `json:"outcome"`
	Charter             *TeamCharter `json:"charter,omitempty"`
}

type TeamCharter struct {
	ID                  string               `json:"id"`
	URI                 string               `json:"uri"`
	Name                string               `json:"name"`
	Target              string               `json:"target"`
	Instance            string               `json:"instance,omitempty"`
	InstanceURI         string               `json:"instance_uri,omitempty"`
	Template            string               `json:"template,omitempty"`
	Parameters          map[string]any       `json:"parameters,omitempty"`
	Goal                TeamSpawnGoal        `json:"goal,omitempty"`
	ParentDeploymentURI string               `json:"parent_deployment_uri"`
	ChildDeploymentID   string               `json:"child_deployment_id"`
	ChildDeploymentURI  string               `json:"child_deployment_uri"`
	Relationship        string               `json:"relationship"`
	State               string               `json:"state"`
	Creator             origin.Envelope      `json:"creator"`
	Budget              TeamCharterBudget    `json:"budget,omitempty"`
	Authority           TeamCharterAuthority `json:"authority,omitempty"`
	Lifecycle           TeamSpawnLifecycle   `json:"lifecycle,omitempty"`
	Outcome             *EventOutcome        `json:"outcome,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
	SpawnedAt           time.Time            `json:"spawned_at,omitempty"`
	ReapedAt            time.Time            `json:"reaped_at,omitempty"`
	UpdatedAt           time.Time            `json:"updated_at"`
	Tombstone           map[string]string    `json:"tombstone,omitempty"`
}

type TeamCharterBudget struct {
	RequestedTokens int64  `json:"requested_tokens,omitempty"`
	GrantedTokens   int64  `json:"granted_tokens,omitempty"`
	Time            string `json:"time,omitempty"`
	AllocationID    string `json:"allocation_id,omitempty"`
	AllocationURI   string `json:"allocation_uri,omitempty"`
	Team            string `json:"team,omitempty"`
}

type TeamCharterAuthority struct {
	CapabilityURI      string                   `json:"capability_uri,omitempty"`
	RequestedVerbs     []string                 `json:"requested_verbs,omitempty"`
	GrantedVerbs       []string                 `json:"granted_verbs,omitempty"`
	RequestedResources []string                 `json:"requested_resources,omitempty"`
	GrantedResources   []string                 `json:"granted_resources,omitempty"`
	Grants             []TeamCharterGrant       `json:"grants,omitempty"`
	Denied             []TeamCharterDeniedGrant `json:"denied,omitempty"`
}

type TeamCharterGrant struct {
	Verb      string   `json:"verb"`
	Resources []string `json:"resources,omitempty"`
}

type TeamCharterDeniedGrant struct {
	Verb     string `json:"verb,omitempty"`
	Resource string `json:"resource,omitempty"`
	Reason   string `json:"reason"`
}

type charteredInstanceMarker struct {
	Chartered     bool
	CharterURI    string
	CapabilityURI string
}

func readCharteredInstanceMarker(daemonRoot, instance string) (charteredInstanceMarker, error) {
	meta, err := ReadMetadata(daemonRoot, instance)
	if err != nil {
		return charteredInstanceMarker{}, err
	}
	return charteredInstanceMarker{
		Chartered:     meta.Chartered || meta.CharterURI != "" || meta.CapabilityURI != "",
		CharterURI:    strings.TrimSpace(meta.CharterURI),
		CapabilityURI: strings.TrimSpace(meta.CapabilityURI),
	}, nil
}

func dynamicTeamSpawnEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(dynamicTeamSpawnFeatureEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (r *EventResolver) SpawnTeam(req TeamSpawnRequest) (*TeamSpawnResult, error) {
	if r == nil || r.mgr == nil {
		return nil, errors.New("team spawn: event resolver is required")
	}
	if strings.TrimSpace(r.teamDir) == "" {
		return nil, errors.New("team spawn: team dir is required")
	}
	if !dynamicTeamSpawnEnabled() {
		return nil, ErrDynamicTeamSpawnDisabled
	}
	r.mu.Lock()
	top := r.topo
	r.mu.Unlock()
	if top == nil {
		return nil, errors.New("team spawn: topology is required")
	}
	parent, err := resource.DeploymentFromTeamDir(r.teamDir)
	if err != nil {
		return nil, fmt.Errorf("team spawn: deployment: %w", err)
	}
	if strings.TrimSpace(parent.ID) == "" {
		return nil, errors.New("team spawn: deployment identity unavailable")
	}
	target := strings.TrimSpace(firstNonEmpty(req.Target, payloadString(req.Payload, "target")))
	if target == "" {
		return nil, errors.New("team spawn: target is required")
	}
	inst := top.Find(target)
	if inst == nil {
		return nil, fmt.Errorf("team spawn: target instance %q not declared", target)
	}
	if !inst.Ephemeral {
		return nil, fmt.Errorf("team spawn: target instance %q is not ephemeral", target)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("team spawn: name is required")
	}
	instanceName, err := childTeamInstanceName(target, name, req.Instance)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	charterID := newTeamCharterID(name, now)
	childDeploymentID := resource.ChildDeploymentID(parent.ID, name)
	creator := r.creatorOrigin(req.Origin, target, instanceName).WithResourceURIs()
	charter := &TeamCharter{
		ID:                  charterID,
		URI:                 resource.CharterURI(parent.ID, charterID),
		Name:                name,
		Target:              target,
		Instance:            instanceName,
		Template:            strings.TrimSpace(req.Template),
		Parameters:          copyAnyMap(req.Parameters),
		Goal:                cleanTeamSpawnGoal(req.Goal),
		ParentDeploymentURI: parent.URI,
		ChildDeploymentID:   childDeploymentID,
		ChildDeploymentURI:  resource.DeploymentURI(childDeploymentID),
		Relationship:        childDeploymentRelationshipEphemeralTeam,
		State:               TeamCharterStateChartered,
		Creator:             creator,
		Budget: TeamCharterBudget{
			RequestedTokens: req.Budget.Tokens,
			Time:            strings.TrimSpace(req.Budget.Time),
			Team:            creator.Team,
		},
		Lifecycle: cleanTeamSpawnLifecycle(req.Lifecycle),
		CreatedAt: now,
		UpdatedAt: now,
	}
	charter.Authority = r.attenuateTeamAuthority(charter, req.Authority)
	if err := WriteTeamCharter(r.mgr.daemonRoot, charter); err != nil {
		return nil, err
	}

	payload := copyPayload(req.Payload)
	payload["target"] = target
	payload["name"] = instanceName
	payload["deployment_uri"] = charter.ChildDeploymentURI
	payload["deployment_parent_uri"] = charter.ParentDeploymentURI
	payload["charter_uri"] = charter.URI
	payload["child_deployment_uri"] = charter.ChildDeploymentURI
	payload["capability_uri"] = charter.Authority.CapabilityURI
	payload["relationship"] = charter.Relationship
	if req.Budget.Tokens > 0 {
		payload["budget_tokens"] = req.Budget.Tokens
	}
	if strings.TrimSpace(req.Budget.Time) != "" {
		payload["budget_time"] = strings.TrimSpace(req.Budget.Time)
	}
	if payloadString(payload, "workspace") == "" {
		payload["workspace"] = "repo"
	}
	if payloadString(payload, "source") == "" {
		payload["source"] = "team.spawn"
	}
	if payloadString(payload, "job_id") == "" && charter.Creator.Job != "" {
		payload["job_id"] = charter.Creator.Job
	}
	outcome := r.actuateEphemeralWithBudgetOrigin(inst, topology.EventAgentDispatch, payload, charter.Creator)
	charter.Outcome = &outcome
	charter.UpdatedAt = time.Now().UTC()
	switch outcome.Action {
	case "dispatched":
		if updated, err := r.markTeamCharterSpawned(instanceName, outcome); err != nil {
			return nil, err
		} else if updated != nil {
			charter = updated
		}
	case "queued":
		charter.State = TeamCharterStateQueued
	case "rejected", "blocked":
		charter.State = TeamCharterStateFailed
	}
	if err := WriteTeamCharter(r.mgr.daemonRoot, charter); err != nil {
		return nil, err
	}
	return &TeamSpawnResult{
		Accepted:            outcome.Action == "dispatched" || outcome.Action == "queued",
		CharterURI:          charter.URI,
		ChildDeploymentURI:  charter.ChildDeploymentURI,
		CapabilityURI:       charter.Authority.CapabilityURI,
		BudgetAllocationURI: charter.Budget.AllocationURI,
		State:               charter.State,
		Outcome:             outcome,
		Charter:             charter,
	}, nil
}

func (r *EventResolver) ReapTeamCharter(id string) (*TeamCharter, error) {
	charter, err := ReadTeamCharter(r.mgr.daemonRoot, id)
	if err != nil {
		return nil, err
	}
	if teamCharterTerminal(charter.State) {
		return charter, nil
	}
	if strings.TrimSpace(charter.Instance) != "" {
		_, _ = r.mgr.StopWithOptions(charter.Instance, StopOptions{Force: true, Timeout: time.Second})
	}
	return r.markTeamCharterRecordReaped(charter, map[string]string{"reason": "manual"})
}

func (r *EventResolver) markTeamCharterReaped(instance string, tombstone map[string]string) (*TeamCharter, error) {
	charter, err := ReadTeamCharterByInstance(r.mgr.daemonRoot, instance)
	if err != nil {
		return nil, err
	}
	return r.markTeamCharterRecordReaped(charter, tombstone)
}

func (r *EventResolver) markTeamCharterRecordReaped(charter *TeamCharter, tombstone map[string]string) (*TeamCharter, error) {
	if charter == nil {
		return nil, errors.New("team charter: nil record")
	}
	now := time.Now().UTC()
	charter.State = TeamCharterStateReaped
	charter.ReapedAt = now
	charter.UpdatedAt = now
	if len(tombstone) > 0 {
		charter.Tombstone = copyStringMap(tombstone)
	}
	if meta, err := ReadMetadata(r.mgr.daemonRoot, charter.Instance); err == nil && meta != nil {
		if charter.InstanceURI == "" {
			charter.InstanceURI = meta.URI
		}
		charter.Tombstone = mergeStringMap(charter.Tombstone, map[string]string{
			"instance_status": string(meta.Status),
			"job":             meta.Job,
			"deployment_uri":  meta.DeploymentURI,
		})
	}
	if err := WriteTeamCharter(r.mgr.daemonRoot, charter); err != nil {
		return nil, err
	}
	return charter, nil
}

func (r *EventResolver) markTeamCharterSpawned(instance string, outcome EventOutcome) (*TeamCharter, error) {
	charter, err := ReadTeamCharterByInstance(r.mgr.daemonRoot, instance)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now().UTC()
	charter.State = TeamCharterStateRunning
	if charter.SpawnedAt.IsZero() {
		charter.SpawnedAt = now
	}
	charter.UpdatedAt = now
	charter.Outcome = &outcome
	if meta, err := ReadMetadata(r.mgr.daemonRoot, instance); err == nil && meta != nil {
		charter.InstanceURI = meta.URI
	}
	r.attachTeamCharterAllocation(charter)
	if err := WriteTeamCharter(r.mgr.daemonRoot, charter); err != nil {
		return nil, err
	}
	return charter, nil
}

func (r *EventResolver) creatorOrigin(input origin.Envelope, target, instance string) origin.Envelope {
	out := input.Clean()
	if out.Project == "" {
		out.Project = projectIDForTeamDir(r.teamDir)
	}
	if out.DeploymentURI == "" {
		out.DeploymentURI = resource.DeploymentURI(out.Project)
	}
	if out.Team == "" {
		out.Team = r.teamForOrigin(target, nil)
	}
	if out.Instance == "" {
		out.Instance = instance
	}
	if out.Agent == "" {
		if r.topo != nil {
			if inst := r.topo.Find(target); inst != nil {
				out.Agent = inst.Agent
			}
		}
	}
	return out.WithResourceURIs()
}

func (r *EventResolver) attenuateTeamAuthority(charter *TeamCharter, req TeamSpawnAuthority) TeamCharterAuthority {
	requestedVerbs := cleanStringSet(req.Verbs)
	requestedResources := cleanStringSet(req.Resources)
	childDeploymentID := ""
	if charter != nil {
		childDeploymentID = charter.ChildDeploymentID
	}
	out := TeamCharterAuthority{
		CapabilityURI:      resource.CapabilityURI(childDeploymentID, "cap-"+charter.ID),
		RequestedVerbs:     requestedVerbs,
		RequestedResources: requestedResources,
	}
	parentAllow := []string{}
	childAllow := []string{}
	parentCharterGrants := []TeamCharterGrant{}
	parentChartered := false
	authorityConfigured := false
	if r != nil && r.topo != nil && r.topo.Authority != nil && r.topo.Authority.Configured() && charter != nil {
		authorityConfigured = true
		parentAllow, parentCharterGrants, parentChartered = r.effectiveAuthorityAllowlistForCharterCreator(charter)
		childAgent := ""
		if inst := r.topo.FindRuntimeInstance(charter.Instance, ""); inst != nil {
			childAgent = inst.Agent
		}
		childAllow = r.topo.AuthorityAllowlistForInstance(charter.Instance, childAgent)
	}
	grantedPatterns := r.attenuatedAuthorityPatterns(parentAllow, childAllow, requestedVerbs, authorityConfigured)
	if len(requestedVerbs) > 0 {
		for _, verb := range requestedVerbs {
			if authorityPatternCoveredByGrant(verb, grantedPatterns) {
				continue
			}
			reason := "not present in parent capability"
			if authorityPatternCoveredByGrant(verb, r.attenuatedAuthorityPatterns(parentAllow, []string{verb}, []string{verb}, authorityConfigured)) {
				reason = "not present in child capability"
			}
			out.Denied = append(out.Denied, TeamCharterDeniedGrant{Verb: verb, Reason: reason})
		}
	}
	validResources, deniedResources := validateTeamAuthorityResources(requestedResources, charter)
	out.Denied = append(out.Denied, deniedResources...)
	for _, pattern := range grantedPatterns {
		resources := authorityGrantResources(pattern, validResources, charter)
		if parentChartered {
			resources = authorityGrantResourcesWithinParentGrant(pattern, resources, validResources, parentCharterGrants)
			if authorityGrantVerbResourceKind(pattern) != "" && len(resources) == 0 {
				continue
			}
		}
		out.Grants = append(out.Grants, TeamCharterGrant{Verb: pattern, Resources: resources})
	}
	out.Grants = cleanTeamCharterGrants(out.Grants)
	out.GrantedVerbs = grantedVerbsFromAuthority(out)
	out.GrantedResources = grantedResourcesFromAuthority(out)
	return out
}

func (r *EventResolver) effectiveAuthorityAllowlistForCharterCreator(charter *TeamCharter) ([]string, []TeamCharterGrant, bool) {
	if r == nil || charter == nil {
		return nil, nil, false
	}
	if r.mgr != nil && strings.TrimSpace(charter.Creator.Instance) != "" {
		parentCharter, err := ReadTeamCharterByInstance(r.mgr.daemonRoot, charter.Creator.Instance)
		if err == nil && parentCharter != nil {
			grants := effectiveTeamCharterGrants(parentCharter.Authority)
			return authorityPatternsFromCharterGrants(grants), grants, true
		}
	}
	if r.topo == nil {
		return nil, nil, false
	}
	return r.topo.AuthorityAllowlistForInstance(charter.Creator.Instance, charter.Creator.Agent), nil, false
}

func authorityPatternsFromCharterGrants(grants []TeamCharterGrant) []string {
	seen := map[string]bool{}
	var out []string
	for _, grant := range cleanTeamCharterGrants(grants) {
		verb := strings.TrimSpace(grant.Verb)
		if verb == "" || seen[verb] {
			continue
		}
		seen[verb] = true
		out = append(out, verb)
	}
	sort.Strings(out)
	return out
}

func (r *EventResolver) attenuatedAuthorityPatterns(parentAllow, childAllow, requested []string, configured bool) []string {
	if !configured {
		return cleanStringSet(requested)
	}
	seen := map[string]bool{}
	var out []string
	if len(requested) == 0 {
		for _, parent := range parentAllow {
			for _, child := range childAllow {
				if grant, ok := intersectAuthorityAllow(parent, child); ok && !seen[grant] {
					seen[grant] = true
					out = append(out, grant)
				}
			}
		}
		sort.Strings(out)
		return out
	}
	for _, req := range requested {
		for _, parent := range parentAllow {
			parentGrant, ok := intersectAuthorityAllow(parent, req)
			if !ok {
				continue
			}
			for _, child := range childAllow {
				childGrant, ok := intersectAuthorityAllow(child, req)
				if !ok {
					continue
				}
				grant, ok := intersectAuthorityAllow(parentGrant, childGrant)
				if !ok || seen[grant] {
					continue
				}
				seen[grant] = true
				out = append(out, grant)
			}
		}
	}
	sort.Strings(out)
	return out
}

func authorityAllowedByPatterns(patterns []string, decision topology.AuthorityDecision, configured bool) bool {
	if len(patterns) == 0 {
		return !configured
	}
	for _, pattern := range patterns {
		if (&topology.AuthorityRule{Allow: []string{pattern}}).Allows(decision) {
			return true
		}
	}
	return false
}

func authorityPatternCoveredByGrant(requested string, grants []string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return false
	}
	for _, grant := range grants {
		if _, ok := intersectAuthorityAllow(grant, requested); ok {
			return true
		}
	}
	return false
}

func intersectAuthorityAllow(left, right string) (string, bool) {
	leftVerb, leftQualifier := splitAuthorityAllowLocal(left)
	rightVerb, rightQualifier := splitAuthorityAllowLocal(right)
	verb, ok := intersectAuthorityVerb(leftVerb, rightVerb)
	if !ok {
		return "", false
	}
	qualifier, ok := intersectAuthorityQualifier(leftQualifier, rightQualifier)
	if !ok {
		return "", false
	}
	if qualifier == "" {
		return verb, true
	}
	return verb + ":" + qualifier, true
}

func splitAuthorityAllowLocal(value string) (string, string) {
	value = strings.TrimSpace(value)
	verb, qualifier, ok := strings.Cut(value, ":")
	if !ok {
		return value, ""
	}
	return strings.TrimSpace(verb), strings.TrimSpace(qualifier)
}

func intersectAuthorityVerb(left, right string) (string, bool) {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return "", false
	}
	if left == "*" {
		return right, true
	}
	if right == "*" {
		return left, true
	}
	if left == right {
		return left, true
	}
	if authorityVerbPatternContains(left, right) {
		return right, true
	}
	if authorityVerbPatternContains(right, left) {
		return left, true
	}
	return "", false
}

func authorityVerbPatternContains(pattern, candidate string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == candidate {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		return strings.HasPrefix(candidate, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func intersectAuthorityQualifier(left, right string) (string, bool) {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch {
	case left == "":
		return right, true
	case right == "":
		return left, true
	case left == right:
		return left, true
	default:
		return "", false
	}
}

func validateTeamAuthorityResources(requested []string, charter *TeamCharter) ([]string, []TeamCharterDeniedGrant) {
	if charter == nil {
		return nil, nil
	}
	if len(requested) == 0 {
		return nil, nil
	}
	var valid []string
	var denied []TeamCharterDeniedGrant
	for _, raw := range requested {
		parsed, err := resource.Parse(raw)
		if err != nil {
			denied = append(denied, TeamCharterDeniedGrant{Resource: raw, Reason: "invalid resource URI"})
			continue
		}
		if parsed.DeploymentID != charter.Creator.Project && parsed.DeploymentID != charter.ChildDeploymentID {
			denied = append(denied, TeamCharterDeniedGrant{Resource: raw, Reason: "outside parent deployment scope"})
			continue
		}
		valid = append(valid, raw)
	}
	return cleanStringSet(valid), denied
}

func authorityGrantResources(grant string, requested []string, charter *TeamCharter) []string {
	if charter == nil {
		return nil
	}
	if len(requested) == 0 {
		return defaultResourcesForAuthorityGrant(grant, charter)
	}
	var resources []string
	for _, raw := range requested {
		parsed, err := resource.Parse(raw)
		if err != nil {
			continue
		}
		if authorityGrantMatchesResource(grant, parsed, charter) {
			resources = append(resources, raw)
		}
	}
	return cleanStringSet(resources)
}

func authorityGrantResourcesWithinParentGrant(grant string, resources, requested []string, parentGrants []TeamCharterGrant) []string {
	kind := authorityGrantVerbResourceKind(grant)
	if kind == "" || len(parentGrants) == 0 {
		return cleanStringSet(resources)
	}
	var out []string
	for _, parentGrant := range cleanTeamCharterGrants(parentGrants) {
		if _, ok := intersectAuthorityAllow(parentGrant.Verb, grant); !ok {
			continue
		}
		candidates := resources
		if len(requested) == 0 {
			candidates = cleanStringSet(parentGrant.Resources)
		}
		for _, candidate := range candidates {
			if authorityGrantResourceKind(candidate) != kind {
				continue
			}
			if charteredResourceAllowedByGrant(parentGrant, candidate) {
				out = append(out, candidate)
			}
		}
	}
	return cleanStringSet(out)
}

func authorityGrantResourceKind(value string) string {
	parsed, err := resource.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return parsed.Kind
}

func defaultResourcesForAuthorityGrant(grant string, charter *TeamCharter) []string {
	if charter == nil {
		return nil
	}
	var resources []string
	if authorityGrantOwnScoped(grant) && authorityGrantVerbResourceKind(grant) == resource.KindJob && strings.TrimSpace(charter.Creator.Job) != "" {
		resources = append(resources, resource.JobURI(charter.Creator.Project, charter.Creator.Job))
	}
	resources = append(resources, charter.ChildDeploymentURI, charter.URI)
	return cleanStringSet(resources)
}

func authorityGrantMatchesResource(grant string, parsed resource.Parsed, charter *TeamCharter) bool {
	kind := authorityGrantVerbResourceKind(grant)
	if kind != "" && parsed.Kind != kind {
		return false
	}
	if authorityGrantOwnScoped(grant) {
		return parsed.Kind == resource.KindJob &&
			strings.TrimSpace(charter.Creator.Job) != "" &&
			parsed.DeploymentID == charter.Creator.Project &&
			parsed.ID == charter.Creator.Job
	}
	return true
}

func authorityGrantOwnScoped(grant string) bool {
	_, qualifier := splitAuthorityAllowLocal(grant)
	return qualifier == "own"
}

func authorityGrantVerbResourceKind(grant string) string {
	verb, _ := splitAuthorityAllowLocal(grant)
	verb = strings.TrimSuffix(strings.TrimSpace(verb), ".*")
	switch {
	case verb == "job" || strings.HasPrefix(verb, "job."):
		return resource.KindJob
	case verb == "channel" || strings.HasPrefix(verb, "channel."):
		return resource.KindChannel
	case verb == "inbox" || strings.HasPrefix(verb, "inbox."):
		return resource.KindMailbox
	case verb == "instance" || strings.HasPrefix(verb, "instance."):
		return resource.KindInstance
	case verb == "queue" || strings.HasPrefix(verb, "queue."):
		return resource.KindQueue
	case verb == "outbox" || strings.HasPrefix(verb, "outbox."):
		return resource.KindOutbox
	case verb == "lock" || strings.HasPrefix(verb, "lock."):
		return resource.KindLock
	}
	return ""
}

func cleanTeamCharterGrants(values []TeamCharterGrant) []TeamCharterGrant {
	seen := map[string]bool{}
	out := make([]TeamCharterGrant, 0, len(values))
	for _, grant := range values {
		verb := strings.TrimSpace(grant.Verb)
		if verb == "" {
			continue
		}
		resources := cleanStringSet(grant.Resources)
		key := verb + "\x00" + strings.Join(resources, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, TeamCharterGrant{Verb: verb, Resources: resources})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Verb == out[j].Verb {
			return strings.Join(out[i].Resources, "\x00") < strings.Join(out[j].Resources, "\x00")
		}
		return out[i].Verb < out[j].Verb
	})
	return out
}

func grantedVerbsFromAuthority(authority TeamCharterAuthority) []string {
	seen := map[string]bool{}
	var verbs []string
	grants := authority.Grants
	if len(grants) == 0 {
		return cleanStringSet(authority.GrantedVerbs)
	}
	for _, grant := range grants {
		verb := strings.TrimSpace(grant.Verb)
		if verb == "" || seen[verb] {
			continue
		}
		seen[verb] = true
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)
	return verbs
}

func grantedResourcesFromAuthority(authority TeamCharterAuthority) []string {
	seen := map[string]bool{}
	var resources []string
	grants := authority.Grants
	if len(grants) == 0 {
		return cleanStringSet(authority.GrantedResources)
	}
	for _, grant := range grants {
		for _, resource := range grant.Resources {
			resource = strings.TrimSpace(resource)
			if resource == "" || seen[resource] {
				continue
			}
			seen[resource] = true
			resources = append(resources, resource)
		}
	}
	sort.Strings(resources)
	return resources
}

func (r *EventResolver) attachTeamCharterAllocation(charter *TeamCharter) {
	if charter == nil || strings.TrimSpace(r.teamDir) == "" || strings.TrimSpace(charter.Instance) == "" {
		return
	}
	allocations, err := budget.ListAllocations(r.teamDir)
	if err != nil {
		return
	}
	for i := len(allocations) - 1; i >= 0; i-- {
		rec := allocations[i]
		if rec == nil || rec.Instance != charter.Instance || rec.Status != budget.AllocationStatusOutstanding {
			continue
		}
		charter.Budget.AllocationID = rec.ID
		charter.Budget.AllocationURI = resource.AllocationURI(projectIDForTeamDir(r.teamDir), rec.ID)
		charter.Budget.GrantedTokens = rec.Tokens
		charter.Budget.Team = rec.Team
		return
	}
}

func WriteTeamCharter(daemonRoot string, charter *TeamCharter) error {
	if charter == nil {
		return errors.New("team charter: nil record")
	}
	if strings.TrimSpace(charter.ID) == "" {
		return errors.New("team charter: id is required")
	}
	if err := validateTeamCharterID(charter.ID); err != nil {
		return err
	}
	dir := teamCharterRoot(daemonRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("team charter: mkdir: %w", err)
	}
	body, err := json.MarshalIndent(charter, "", "  ")
	if err != nil {
		return fmt.Errorf("team charter: marshal: %w", err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, charter.ID+"-*.json.tmp")
	if err != nil {
		return fmt.Errorf("team charter: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("team charter: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("team charter: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("team charter: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), teamCharterPath(daemonRoot, charter.ID)); err != nil {
		return fmt.Errorf("team charter: rename: %w", err)
	}
	return nil
}

func ReadTeamCharter(daemonRoot, id string) (*TeamCharter, error) {
	id = strings.TrimSpace(id)
	if err := validateTeamCharterID(id); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(teamCharterPath(daemonRoot, id))
	if err != nil {
		return nil, err
	}
	var charter TeamCharter
	if err := json.Unmarshal(body, &charter); err != nil {
		return nil, fmt.Errorf("team charter %s: %w", id, err)
	}
	return &charter, nil
}

func ReadTeamCharterByInstance(daemonRoot, instance string) (*TeamCharter, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return nil, fs.ErrNotExist
	}
	charters, err := ListTeamCharters(daemonRoot)
	if err != nil {
		return nil, err
	}
	for _, charter := range charters {
		if charter != nil && charter.Instance == instance && !teamCharterTerminal(charter.State) {
			return charter, nil
		}
	}
	return nil, fs.ErrNotExist
}

func ListTeamCharters(daemonRoot string) ([]*TeamCharter, error) {
	entries, err := os.ReadDir(teamCharterRoot(daemonRoot))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*TeamCharter, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		charter, err := ReadTeamCharter(daemonRoot, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, charter)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func teamCharterRoot(daemonRoot string) string {
	return filepath.Join(daemonRoot, "charters")
}

func teamCharterPath(daemonRoot, id string) string {
	return filepath.Join(teamCharterRoot(daemonRoot), id+".json")
}

func validateTeamCharterID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("team charter: id is required")
	}
	if strings.ContainsAny(id, `/\`) || id == "." || id == ".." || strings.Contains(id, "..") {
		return errors.New("team charter: id must not contain path segments")
	}
	return nil
}

func teamCharterTerminal(state string) bool {
	switch strings.TrimSpace(state) {
	case TeamCharterStateFailed, TeamCharterStateReaped:
		return true
	default:
		return false
	}
}

func childTeamInstanceName(target, name, requested string) (string, error) {
	target = strings.TrimSpace(target)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = strings.TrimSpace(name)
	}
	if requested != "" && strings.HasPrefix(requested, target+"-") {
		return requested, validateRequestedChildName(target, requested)
	}
	slug := childTeamSlug(requested)
	if slug == "" {
		slug = "child"
	}
	out := target + "-" + slug
	return out, validateRequestedChildName(target, out)
}

func childTeamSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '-' || r == '_' || r == '.' || r == ' ' {
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 96 {
		out = strings.Trim(out[:96], "-")
	}
	return out
}

func newTeamCharterID(name string, now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	slug := childTeamSlug(name)
	if slug == "" {
		slug = "child"
	}
	return fmt.Sprintf("charter-%s-%d-%s", slug, now.UnixNano(), hex.EncodeToString(b[:]))
}

func cleanTeamSpawnGoal(goal TeamSpawnGoal) TeamSpawnGoal {
	return TeamSpawnGoal{
		Summary: strings.TrimSpace(goal.Summary),
		Success: cleanStringSet(goal.Success),
	}
}

func cleanTeamSpawnLifecycle(lifecycle TeamSpawnLifecycle) TeamSpawnLifecycle {
	return TeamSpawnLifecycle{
		TTL:  strings.TrimSpace(lifecycle.TTL),
		Reap: strings.TrimSpace(lifecycle.Reap),
	}
}

func cleanStringSet(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func copyAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

func mergeStringMap(primary, fallback map[string]string) map[string]string {
	out := copyStringMap(primary)
	if out == nil {
		out = map[string]string{}
	}
	for k, v := range fallback {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
