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
)

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
	Denied             []TeamCharterDeniedGrant `json:"denied,omitempty"`
}

type TeamCharterDeniedGrant struct {
	Verb     string `json:"verb,omitempty"`
	Resource string `json:"resource,omitempty"`
	Reason   string `json:"reason"`
}

func (r *EventResolver) SpawnTeam(req TeamSpawnRequest) (*TeamSpawnResult, error) {
	if r == nil || r.mgr == nil {
		return nil, errors.New("team spawn: event resolver is required")
	}
	if strings.TrimSpace(r.teamDir) == "" {
		return nil, errors.New("team spawn: team dir is required")
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
			GrantedTokens:   req.Budget.Tokens,
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
	payloadSetStringIfEmpty(payload, "deployment_uri", charter.ChildDeploymentURI)
	payloadSetStringIfEmpty(payload, "deployment_parent_uri", charter.ParentDeploymentURI)
	payloadSetStringIfEmpty(payload, "charter_uri", charter.URI)
	payloadSetStringIfEmpty(payload, "child_deployment_uri", charter.ChildDeploymentURI)
	payloadSetStringIfEmpty(payload, "capability_uri", charter.Authority.CapabilityURI)
	payloadSetStringIfEmpty(payload, "relationship", charter.Relationship)
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
		charter.State = TeamCharterStateRunning
		charter.SpawnedAt = charter.UpdatedAt
		if meta, err := ReadMetadata(r.mgr.daemonRoot, instanceName); err == nil && meta != nil {
			charter.InstanceURI = meta.URI
		}
		r.attachTeamCharterAllocation(charter)
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
	if strings.TrimSpace(charter.Instance) != "" {
		_, _ = r.mgr.StopWithOptions(charter.Instance, StopOptions{Force: true, Timeout: time.Second})
	}
	return r.markTeamCharterReaped(charter.Instance, map[string]string{"reason": "manual"})
}

func (r *EventResolver) markTeamCharterReaped(instance string, tombstone map[string]string) (*TeamCharter, error) {
	charter, err := ReadTeamCharterByInstance(r.mgr.daemonRoot, instance)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	charter.State = TeamCharterStateReaped
	charter.ReapedAt = now
	charter.UpdatedAt = now
	if len(tombstone) > 0 {
		charter.Tombstone = copyStringMap(tombstone)
	}
	if meta, err := ReadMetadata(r.mgr.daemonRoot, instance); err == nil && meta != nil {
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
	authorityConfigured := false
	if r != nil && r.topo != nil && r.topo.Authority != nil && r.topo.Authority.Configured() && charter != nil {
		authorityConfigured = true
		parentAllow = r.topo.AuthorityAllowlistForInstance(charter.Creator.Instance, charter.Creator.Agent)
		childAgent := ""
		if inst := r.topo.FindRuntimeInstance(charter.Instance, ""); inst != nil {
			childAgent = inst.Agent
		}
		childAllow = r.topo.AuthorityAllowlistForInstance(charter.Instance, childAgent)
	}
	if len(requestedVerbs) == 0 {
		out.GrantedVerbs = intersectAuthorityAllowlists(parentAllow, childAllow, authorityConfigured)
	} else {
		for _, verb := range requestedVerbs {
			decision := topology.AuthorityDecision{
				Instance:  charter.Creator.Instance,
				Agent:     charter.Creator.Agent,
				Team:      charter.Creator.Team,
				Verb:      verb,
				ActorJob:  charter.Creator.Job,
				TargetJob: charter.Creator.Job,
			}
			if authorityAllowedByPatterns(parentAllow, decision, authorityConfigured) && authorityAllowedByPatterns(childAllow, decision, authorityConfigured) {
				out.GrantedVerbs = append(out.GrantedVerbs, verb)
			} else {
				reason := "not present in parent capability"
				if authorityAllowedByPatterns(parentAllow, decision, authorityConfigured) {
					reason = "not present in child capability"
				}
				out.Denied = append(out.Denied, TeamCharterDeniedGrant{Verb: verb, Reason: reason})
			}
		}
	}
	if len(requestedResources) == 0 {
		if charter != nil {
			out.GrantedResources = []string{charter.ChildDeploymentURI, charter.URI}
		}
	} else {
		for _, requested := range requestedResources {
			parsed, err := resource.Parse(requested)
			if err != nil {
				out.Denied = append(out.Denied, TeamCharterDeniedGrant{Resource: requested, Reason: "invalid resource URI"})
				continue
			}
			if charter != nil && (parsed.DeploymentID == charter.Creator.Project || parsed.DeploymentID == charter.ChildDeploymentID) {
				out.GrantedResources = append(out.GrantedResources, requested)
				continue
			}
			out.Denied = append(out.Denied, TeamCharterDeniedGrant{Resource: requested, Reason: "outside parent deployment scope"})
		}
	}
	sort.Strings(out.GrantedVerbs)
	sort.Strings(out.GrantedResources)
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

func intersectAuthorityAllowlists(parentAllow, childAllow []string, configured bool) []string {
	if !configured {
		return nil
	}
	seen := map[string]bool{}
	var out []string
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
	if strings.ContainsAny(charter.ID, `/\`) || charter.ID == "." || charter.ID == ".." || strings.Contains(charter.ID, "..") {
		return errors.New("team charter: id must not contain path segments")
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
	body, err := os.ReadFile(teamCharterPath(daemonRoot, strings.TrimSpace(id)))
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
		if charter != nil && charter.Instance == instance {
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
