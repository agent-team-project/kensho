package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

type JobRow struct {
	ID, Ticket, Status, Pipeline string
	Model, Tier, Runtime         string
	Bounces                      map[string]int
	Instance                     string
	UpdatedAt                    time.Time
}

type ModelTierRow struct {
	Label                 string
	Jobs, Active, Bounces int
}

type BounceRow struct {
	Class         string
	Bounces, Jobs int
}

type TelemetryProjection struct {
	Models  []ModelTierRow
	Bounces []BounceRow
}

type InstanceRow struct {
	Name, Agent, Status, Phase string
	Model, Tier, Runtime       string
	Job, Ticket, Description   string
	URI                        string
}

type OrgInstanceRow struct {
	Name, Job, Ticket, Description string
	Status, Phase                  string
	Model, Tier, Runtime           string
}

type OrgLaneRow struct {
	Name, Agent, State, Meta  string
	Working, Idle             int
	Queued, Running, Capacity int
	Instances                 []OrgInstanceRow
}

type OrgRoleProjection struct {
	Role                           string
	Working, Idle, Queued, Crashed int
	Lanes                          []OrgLaneRow
}

type DeploymentRow struct {
	URI, ID, Parent, CharterURI, CharterStatus, Status string
	Instances, Jobs                                    int
}

type PipelineRow struct {
	Name, Trigger, Steps string
	Active               int
}

type BudgetRow struct {
	Team, Tokens, Cap, Allocation string
	Active                        int
	Open                          *int
}

type ScheduleRow struct {
	Name, Cadence, LastFired, Team, Payload string
}

type DeadlineRow struct {
	Label, Deadline, State, Source string
}

type TeamRow struct {
	Name                           string
	Instances, Pipelines, Channels int
	Active                         int
}

type TopologyProjection struct {
	Deployments []DeploymentRow
	Pipelines   []PipelineRow
	Budgets     []BudgetRow
	Schedules   []ScheduleRow
	Deadlines   []DeadlineRow
	Teams       []TeamRow
}

func projectJobs(model Model) []JobRow {
	if model.Snapshot == nil {
		return nil
	}
	rows := make([]JobRow, 0, len(model.Snapshot.Jobs))
	for _, job := range model.Snapshot.Jobs {
		if job == nil {
			continue
		}
		modelName, tier, runtime := jobTelemetry(model.Snapshot, job)
		row := JobRow{
			ID: job.ID, Ticket: job.Ticket, Status: string(job.Status), Pipeline: job.Pipeline,
			Model: modelName, Tier: tier, Runtime: runtime, Bounces: bounceCountsForJob(model.Snapshot, job),
			Instance: job.Instance, UpdatedAt: job.UpdatedAt,
		}
		if queryMatches(model.Query, jobQueryValues(row), allowedQueryFields(ScreenWorkJobs)) {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

func projectTelemetry(model Model) TelemetryProjection {
	var out TelemetryProjection
	if model.Snapshot == nil {
		return out
	}
	type modelCount struct{ jobs, active, bounces int }
	type bounceCount struct{ bounces, jobs int }
	models := map[string]*modelCount{}
	bounces := map[string]*bounceCount{}
	for _, job := range recentJobs(model.Snapshot.Jobs, 24) {
		if job == nil {
			continue
		}
		modelName, tier, _ := jobTelemetry(model.Snapshot, job)
		label := modelTierLabel(modelName, tier, "not reported")
		counts := bounceCountsForJob(model.Snapshot, job)
		entry := models[label]
		if entry == nil {
			entry = &modelCount{}
			models[label] = entry
		}
		entry.jobs++
		if activeJobStatus(job.Status) {
			entry.active++
		}
		for class, count := range counts {
			entry.bounces += count
			classEntry := bounces[class]
			if classEntry == nil {
				classEntry = &bounceCount{}
				bounces[class] = classEntry
			}
			classEntry.bounces += count
			classEntry.jobs++
		}
	}
	for label, value := range models {
		out.Models = append(out.Models, ModelTierRow{Label: label, Jobs: value.jobs, Active: value.active, Bounces: value.bounces})
	}
	for class, value := range bounces {
		out.Bounces = append(out.Bounces, BounceRow{Class: class, Bounces: value.bounces, Jobs: value.jobs})
	}
	sort.Slice(out.Models, func(i, j int) bool {
		return out.Models[i].Jobs > out.Models[j].Jobs || out.Models[i].Jobs == out.Models[j].Jobs && out.Models[i].Label < out.Models[j].Label
	})
	sort.Slice(out.Bounces, func(i, j int) bool {
		return out.Bounces[i].Bounces > out.Bounces[j].Bounces || out.Bounces[i].Bounces == out.Bounces[j].Bounces && out.Bounces[i].Class < out.Bounces[j].Class
	})
	return out
}

func jobTelemetry(snapshot *daemonclient.Snapshot, job *daemonclient.Job) (modelName, tier, runtime string) {
	if snapshot == nil || job == nil {
		return "", "", ""
	}
	jobData := resourceMap(snapshot.Resources[job.URI])
	outcome := resourceMap(snapshot.Resources[job.OutcomeURI])
	step := primaryJobStep(job, jobData)
	run := primaryStepRun(job, outcome, step)
	modelName, tier = modelTierFromSources(run, outcome)
	runtime = firstMapString(telemetrySources(run), "effective_runtime", "EffectiveRuntime", "runtime", "Runtime")
	if runtime == "" {
		runtime = firstMapString(telemetrySources(outcome), "effective_runtime", "EffectiveRuntime", "runtime", "Runtime")
	}
	instance := matchingInstance(snapshot, job, step)
	instanceData := map[string]any(nil)
	if instance != nil {
		instanceData = resourceMap(snapshot.Resources[instance.URI])
	}
	if runtime == "" {
		runtime = firstMapString(telemetrySources(jobData), "effective_runtime", "EffectiveRuntime", "runtime", "Runtime")
	}
	if runtime == "" && instance != nil {
		runtime = firstText(firstMapString(telemetrySources(instanceData), "effective_runtime", "EffectiveRuntime", "runtime", "Runtime"), instance.EffectiveRuntime, instance.Runtime)
		if runtime == "-" {
			runtime = ""
		}
	}
	return modelName, tier, runtime
}

func matchingInstance(snapshot *daemonclient.Snapshot, job *daemonclient.Job, step map[string]any) *daemonclient.Instance {
	names := []string{mapString(step, "instance", "Instance"), job.Instance}
	for _, name := range names {
		for _, instance := range snapshot.Instances {
			if instance != nil && name != "" && instance.Instance == name {
				return instance
			}
		}
	}
	for _, instance := range snapshot.Instances {
		if instance != nil && instance.Job == job.ID {
			return instance
		}
	}
	return nil
}

func modelTierLabel(modelName, tier, fallback string) string {
	if modelName != "" && tier != "" {
		return modelName + " / " + tier
	}
	if modelName != "" {
		return modelName
	}
	if tier != "" {
		return tier
	}
	return fallback
}

func activeJobStatus(status daemonclient.JobStatus) bool {
	return status == daemonclient.JobQueued || status == daemonclient.JobRunning || status == daemonclient.JobBlocked
}

func bounceCountsForJob(snapshot *daemonclient.Snapshot, job *daemonclient.Job) map[string]int {
	if snapshot == nil || job == nil {
		return map[string]int{}
	}
	outcome := resourceMap(snapshot.Resources[job.OutcomeURI])
	if counts := firstBounceCounts(telemetrySources(outcome), "bounce_classes", "BounceClasses", "bounceClasses"); len(counts) > 0 {
		return counts
	}
	if counts := firstBounceCounts(telemetrySources(outcome), "bounces", "Bounces"); len(counts) > 0 {
		return counts
	}
	data := resourceMap(snapshot.Resources[job.URI])
	if counts := firstBounceCounts(telemetrySources(data), "bounce_classes", "BounceClasses", "bounceClasses"); len(counts) > 0 {
		return counts
	}
	return parseBounceCounts(recursiveString(data, "kickoff"))
}

func firstBounceCounts(sources []map[string]any, names ...string) map[string]int {
	for _, source := range sources {
		for _, name := range names {
			if value, ok := source[name]; ok {
				if counts := bounceCountsFromValue(value); len(counts) > 0 {
					return counts
				}
			}
		}
	}
	return map[string]int{}
}

func bounceCountsFromValue(value any) map[string]int {
	out := map[string]int{}
	add := func(class string, count int) {
		if class = strings.TrimSpace(class); class != "" && count > 0 {
			out[class] += count
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		for class, raw := range typed {
			add(class, numberCount(raw))
		}
	case []any:
		for _, raw := range typed {
			switch item := raw.(type) {
			case string:
				add(item, 1)
			case map[string]any:
				for _, name := range []string{"classes", "Classes"} {
					if values, ok := item[name].([]any); ok {
						for _, value := range values {
							if class, ok := value.(string); ok {
								add(class, 1)
							}
						}
					}
				}
			}
		}
	}
	return out
}

func numberCount(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := strconv.Atoi(string(typed))
		return parsed
	default:
		return 0
	}
}

var bounceHeading = regexp.MustCompile(`(?mi)^## Review findings \(bounce [0-9]+\)\s*$`)

func parseBounceCounts(kickoff string) map[string]int {
	out := map[string]int{}
	matches := bounceHeading.FindAllStringIndex(kickoff, -1)
	for index, match := range matches {
		end := len(kickoff)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		for _, class := range classifyBounce(kickoff[match[1]:end]) {
			out[class]++
		}
	}
	return out
}

func classifyBounce(body string) []string {
	lower := strings.ToLower(body)
	known := []string{"capability", "spec-ambiguity", "scope", "infra"}
	explicit := []string{}
	for _, line := range strings.Split(lower, "\n") {
		if !strings.Contains(strings.TrimSpace(line), "class") {
			continue
		}
		for _, class := range known {
			if strings.Contains(line, class) || class == "spec-ambiguity" && strings.Contains(line, "spec ambiguity") {
				explicit = appendUnique(explicit, class)
			}
		}
	}
	if len(explicit) > 0 {
		return explicit
	}
	rules := []struct {
		class string
		words []string
	}{
		{"infra", []string{"infra", "flake", "flaky", "timeout", "rate limit", "credential", "auth", "network", "no space", "ci unavailable", "base drift", "runner", "environment"}},
		{"spec-ambiguity", []string{"spec ambiguity", "spec-ambiguity", "ambiguous", "ambiguity", "intent", "clarify", "not what was meant", "underspecified", "under-specified", "vague", "question"}},
		{"scope", []string{"scope", "sprawl", "drive-by", "unrelated", "oversized", "split the ticket", "split ticket", "multiple concerns", "too broad", "out of scope", "owned path"}},
		{"capability", []string{"capability", "logic error", "edge case", "misapplied", "missed", "missing test", "shallow test", "didn't understand", "did not understand", "incorrect", "wrong", "bug", "regression", "behavior", "requirement", "acceptance", "failed to", "doesn't", "does not"}},
	}
	classes := []string{}
	for _, rule := range rules {
		for _, word := range rule.words {
			if strings.Contains(lower, word) {
				classes = append(classes, rule.class)
				break
			}
		}
	}
	if len(classes) == 0 {
		return []string{"unknown"}
	}
	return classes
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func projectInstances(model Model) []InstanceRow {
	if model.Snapshot == nil {
		return nil
	}
	rows := make([]InstanceRow, 0, len(model.Snapshot.Instances))
	for _, instance := range dedupeRuntimeInstances(model.Snapshot.Instances) {
		declared := declaredInstance(model.Snapshot, instance)
		modelName, tier, runtime := instanceTelemetry(model.Snapshot, instance, declared)
		row := InstanceRow{
			Name: instance.Instance, Agent: instance.Agent, Status: string(instance.Status), Phase: instancePhase(model.Snapshot, instance),
			Model: modelName, Tier: tier, Runtime: runtime, Job: instance.Job, Ticket: instance.Ticket,
			Description: instanceDescription(model.Snapshot, instance), URI: instance.URI,
		}
		if queryMatches(model.Query, instanceQueryValues(row), allowedQueryFields(ScreenFleetInstances)) {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func instanceTelemetry(snapshot *daemonclient.Snapshot, instance *daemonclient.Instance, declared *daemonclient.TopologyInstance) (modelName, tier, runtime string) {
	data := resourceMap(snapshot.Resources[instance.URI])
	modelName = firstMapString(telemetrySources(data), "model", "Model")
	if modelName == "" {
		modelName = instance.Model
	}
	if modelName == "" && declared != nil {
		modelName = mapString(declared.Config, "model", "Model")
	}
	tier = firstMapString(telemetrySources(data), "tier", "Tier", "model_tier", "ModelTier")
	if tier == "" && declared != nil {
		tier = mapString(declared.Config, "tier", "Tier", "model_tier", "ModelTier")
	}
	if tier == "" {
		tier = tierForModel(modelName)
	}
	runtime = firstMapString(telemetrySources(data), "effective_runtime", "EffectiveRuntime", "runtime", "Runtime")
	if runtime == "" {
		runtime = firstText(instance.EffectiveRuntime, instance.Runtime)
		if runtime == "-" {
			runtime = ""
		}
	}
	if runtime == "" && declared != nil {
		runtime = mapString(declared.Config, "runtime", "Runtime")
	}
	return modelName, tier, runtime
}

func instancePhase(snapshot *daemonclient.Snapshot, instance *daemonclient.Instance) string {
	data := resourceMap(snapshot.Resources[instance.StateURI])
	if status, ok := data["status"].(map[string]any); ok {
		if phase := mapString(status, "phase", "Phase"); phase != "" {
			return phase
		}
	}
	return mapString(data, "phase", "Phase")
}

func instanceDescription(snapshot *daemonclient.Snapshot, instance *daemonclient.Instance) string {
	data := resourceMap(snapshot.Resources[instance.StateURI])
	if status, ok := data["status"].(map[string]any); ok {
		if description := mapString(status, "description", "Description", "last_action", "LastAction"); description != "" {
			return description
		}
	}
	return firstMapString([]map[string]any{data}, "description", "Description", "last_action", "LastAction")
}

func declaredInstance(snapshot *daemonclient.Snapshot, instance *daemonclient.Instance) *daemonclient.TopologyInstance {
	if snapshot == nil || snapshot.Topology == nil || instance == nil {
		return nil
	}
	name := declaredInstanceName(instance.Instance, snapshot.Topology.Instances)
	for index := range snapshot.Topology.Instances {
		if snapshot.Topology.Instances[index].Name == name {
			return &snapshot.Topology.Instances[index]
		}
	}
	return nil
}

func declaredInstanceName(runtime string, declared []daemonclient.TopologyInstance) string {
	best := ""
	for _, item := range declared {
		if runtime == item.Name {
			return item.Name
		}
		if strings.HasPrefix(runtime, item.Name+"-") && len(item.Name) > len(best) {
			best = item.Name
		}
	}
	return best
}

func dedupeRuntimeInstances(instances []*daemonclient.Instance) []*daemonclient.Instance {
	byID := map[string]*daemonclient.Instance{}
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		id := firstText(instance.URI, instance.Instance)
		if id == "-" {
			continue
		}
		if existing := byID[id]; existing == nil || instanceActivity(instance).After(instanceActivity(existing)) || instanceActivity(instance).Equal(instanceActivity(existing)) {
			byID[id] = instance
		}
	}
	out := make([]*daemonclient.Instance, 0, len(byID))
	for _, instance := range byID {
		out = append(out, instance)
	}
	return out
}

func instanceActivity(instance *daemonclient.Instance) time.Time {
	latest := instance.StartedAt
	for _, value := range []time.Time{instance.StoppedAt, instance.ExitedAt} {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

type orgLane struct {
	name, agent string
	declared    *daemonclient.TopologyInstance
	active      []*daemonclient.Instance
	latest      *daemonclient.Instance
	schedules   []daemonclient.TopologySchedule
}

func projectLiveOrg(model Model) []OrgRoleProjection {
	if model.Snapshot == nil {
		return nil
	}
	snapshot := model.Snapshot
	lanes := map[string]*orgLane{}
	if snapshot.Topology != nil {
		for index := range snapshot.Topology.Instances {
			declared := &snapshot.Topology.Instances[index]
			lanes[declared.Name] = &orgLane{name: declared.Name, agent: firstText(declared.Agent, "unknown"), declared: declared}
		}
	}
	for _, instance := range dedupeRuntimeInstances(snapshot.Instances) {
		name := ""
		if snapshot.Topology != nil {
			name = declaredInstanceName(instance.Instance, snapshot.Topology.Instances)
		}
		if name == "" {
			name = instance.Instance
		}
		lane := lanes[name]
		if lane == nil {
			lane = &orgLane{name: name, agent: firstText(instance.Agent, "unknown")}
			lanes[name] = lane
		}
		if lane.agent == "" || lane.agent == "-" {
			lane.agent = firstText(instance.Agent, "unknown")
		}
		if instance.Status == daemonclient.InstanceRunning {
			lane.active = append(lane.active, instance)
		} else if lane.latest == nil || instanceActivity(instance).After(instanceActivity(lane.latest)) {
			lane.latest = instance
		}
	}
	if snapshot.Topology != nil {
		for _, lane := range lanes {
			if lane.declared == nil {
				continue
			}
			for _, schedule := range snapshot.Topology.Schedules {
				if laneScheduleMatches(lane.declared, schedule) {
					lane.schedules = append(lane.schedules, schedule)
				}
			}
		}
	}
	byRole := map[string]*OrgRoleProjection{}
	for _, lane := range lanes {
		row := projectOrgLane(snapshot, lane)
		if !queryMatches(model.Query, orgQueryValues(row), allowedQueryFields(ScreenFleetOrg)) {
			continue
		}
		role := byRole[row.Agent]
		if role == nil {
			role = &OrgRoleProjection{Role: row.Agent}
			byRole[row.Agent] = role
		}
		switch row.State {
		case "working":
			role.Working++
		case "queued":
			role.Queued++
		case "crashed":
			role.Crashed++
		default:
			role.Idle++
		}
		role.Lanes = append(role.Lanes, row)
	}
	roles := make([]OrgRoleProjection, 0, len(byRole))
	for _, role := range byRole {
		sort.Slice(role.Lanes, func(i, j int) bool { return role.Lanes[i].Name < role.Lanes[j].Name })
		roles = append(roles, *role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Role < roles[j].Role })
	return roles
}

func projectOrgLane(snapshot *daemonclient.Snapshot, lane *orgLane) OrgLaneRow {
	visible := append([]*daemonclient.Instance(nil), lane.active...)
	sort.Slice(visible, func(i, j int) bool { return instanceActivity(visible[i]).After(instanceActivity(visible[j])) })
	if len(visible) == 0 && lane.latest != nil {
		visible = append(visible, lane.latest)
	}
	row := OrgLaneRow{Name: lane.name, Agent: lane.agent, State: laneState(snapshot, lane, visible)}
	if lane.declared != nil {
		row.Queued, row.Running, row.Capacity = lane.declared.Queued, lane.declared.Running, lane.declared.Replicas
	}
	row.Meta = laneMeta(lane)
	for _, instance := range visible {
		declared := lane.declared
		modelName, tier, runtime := instanceTelemetry(snapshot, instance, declared)
		item := OrgInstanceRow{
			Name: instance.Instance, Job: instance.Job, Ticket: instance.Ticket, Description: instanceDescription(snapshot, instance),
			Status: string(instance.Status), Phase: instancePhase(snapshot, instance), Model: modelName, Tier: tier, Runtime: runtime,
		}
		row.Instances = append(row.Instances, item)
		if instance.Status == daemonclient.InstanceRunning && runningPhase(item.Phase) {
			row.Working++
		} else {
			row.Idle++
		}
	}
	return row
}

func laneState(snapshot *daemonclient.Snapshot, lane *orgLane, visible []*daemonclient.Instance) string {
	for _, instance := range visible {
		if instance.Status == daemonclient.InstanceCrashed {
			return "crashed"
		}
	}
	for _, instance := range visible {
		if instance.Status == daemonclient.InstanceRunning && runningPhase(instancePhase(snapshot, instance)) {
			return "working"
		}
	}
	for _, instance := range visible {
		if instance.Status == daemonclient.InstanceRunning {
			return "idle"
		}
	}
	if lane.declared != nil && lane.declared.Queued > 0 {
		return "queued"
	}
	return "idle"
}

func runningPhase(phase string) bool {
	phase = strings.ToLower(strings.TrimSpace(phase))
	return phase == "" || phase != "idle" && phase != "done"
}

func laneMeta(lane *orgLane) string {
	parts := []string{}
	if lane.declared != nil {
		if lane.declared.Replicas > 0 {
			parts = append(parts, fmt.Sprintf("%d/%d running", lane.declared.Running, lane.declared.Replicas))
		} else if lane.declared.Running > 0 {
			parts = append(parts, fmt.Sprintf("%d running", lane.declared.Running))
		}
		if lane.declared.Queued > 0 {
			parts = append(parts, fmt.Sprintf("%d queued", lane.declared.Queued))
		}
		if !lane.declared.Ephemeral {
			parts = append(parts, "persistent")
		}
	}
	for index, schedule := range lane.schedules {
		if index == 2 {
			break
		}
		parts = append(parts, schedule.Name+" "+firstText(schedule.Every, "scheduled"))
	}
	return strings.Join(parts, " / ")
}

func laneScheduleMatches(declared *daemonclient.TopologyInstance, schedule daemonclient.TopologySchedule) bool {
	kind := mapString(schedule.Payload, "kind", "Kind")
	for _, trigger := range declared.Triggers {
		if trigger.Event != "schedule" {
			continue
		}
		if mapString(trigger.Match, "name", "Name") == schedule.Name || kind != "" && mapString(trigger.Match, "kind", "Kind") == kind {
			return true
		}
	}
	return false
}

func projectTopology(model Model) TopologyProjection {
	var out TopologyProjection
	if model.Snapshot == nil {
		return out
	}
	out.Deployments = projectDeployments(model.Snapshot)
	out.Deadlines = projectDeadlines(model.Snapshot)
	if topology := model.Snapshot.Topology; topology != nil {
		out.Pipelines = projectPipelines(topology, model.Snapshot.Jobs)
		out.Budgets = projectBudgets(topology, model.Snapshot.Jobs)
		out.Schedules = projectSchedules(topology)
		out.Teams = projectTeams(topology, model.Snapshot.Jobs)
	}
	filter := func(section, name, team, status, trigger string) bool {
		return queryMatches(model.Query, map[string]string{"section": section, "name": name, "team": team, "status": status, "trigger": trigger}, allowedQueryFields(ScreenFleetTopology))
	}
	out.Deployments = keepDeployments(out.Deployments, func(row DeploymentRow) bool { return filter("deployments", row.ID+" "+row.URI, "", row.Status, "") })
	out.Pipelines = keepPipelines(out.Pipelines, func(row PipelineRow) bool {
		return filter("pipelines", row.Name, "", activeLabel(row.Active), row.Trigger)
	})
	out.Budgets = keepBudgets(out.Budgets, func(row BudgetRow) bool { return filter("budgets", row.Team, row.Team, activeLabel(row.Active), "") })
	out.Schedules = keepSchedules(out.Schedules, func(row ScheduleRow) bool { return filter("schedules", row.Name, row.Team, row.LastFired, row.Payload) })
	out.Deadlines = keepDeadlines(out.Deadlines, func(row DeadlineRow) bool { return filter("deadlines", row.Label, "", row.State, row.Source) })
	out.Teams = keepTeams(out.Teams, func(row TeamRow) bool { return filter("teams", row.Name, row.Name, activeLabel(row.Active), "") })
	return out
}

func projectDeployments(snapshot *daemonclient.Snapshot) []DeploymentRow {
	type mutable struct {
		DeploymentRow
		instances, jobs, statuses map[string]bool
		ready                     bool
	}
	rows := map[string]*mutable{}
	ensure := func(uri string) *mutable {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			return nil
		}
		if rows[uri] == nil {
			rows[uri] = &mutable{DeploymentRow: DeploymentRow{URI: uri, ID: deploymentID(uri)}, instances: map[string]bool{}, jobs: map[string]bool{}, statuses: map[string]bool{}}
		}
		return rows[uri]
	}
	absorb := func(row *mutable, source map[string]any) {
		if row == nil {
			return
		}
		if row.CharterURI == "" {
			row.CharterURI = mapString(source, "charter_uri", "CharterURI")
		}
		if row.CharterStatus == "" {
			row.CharterStatus = mapString(source, "state", "State", "charter_status", "CharterStatus", "charter_state", "CharterState")
		}
	}
	for _, instance := range snapshot.Instances {
		if instance == nil {
			continue
		}
		row := ensure(instance.DeploymentURI)
		if row == nil {
			continue
		}
		if row.Parent == "" {
			row.Parent = instance.DeploymentParentURI
		}
		row.instances[instance.Instance] = true
		row.statuses[string(instance.Status)] = true
		if row.CharterURI == "" {
			row.CharterURI = instance.CharterURI
		}
		absorb(row, resourceMap(snapshot.Resources[instance.URI]))
	}
	for _, job := range snapshot.Jobs {
		if job == nil {
			continue
		}
		row := ensure(job.DeploymentURI)
		if row == nil {
			continue
		}
		if row.Parent == "" {
			row.Parent = job.DeploymentParentURI
		}
		row.jobs[job.ID] = true
		row.statuses[string(job.Status)] = true
		absorb(row, resourceMap(snapshot.Resources[job.URI]))
	}
	for _, resource := range snapshot.Resources {
		if resource == nil || resource.Kind != "project" {
			continue
		}
		data := resourceMap(resource)
		uri := firstText(mapString(data, "uri", "URI", "deployment_uri", "DeploymentURI", "parent_uri", "ParentURI"), resource.URI)
		if uri == "-" {
			continue
		}
		row := ensure(uri)
		if id := mapString(data, "id", "ID"); id != "" {
			row.ID = id
		}
		if row.Parent == "" {
			row.Parent = mapString(data, "parent_uri", "ParentURI")
		}
		row.ready = truthy(data["ready"]) || truthy(data["Ready"])
		absorb(row, data)
	}
	out := make([]DeploymentRow, 0, len(rows))
	for _, row := range rows {
		row.Instances, row.Jobs = len(row.instances), len(row.jobs)
		switch {
		case row.CharterStatus != "":
			row.Status = row.CharterStatus
		case row.statuses["running"]:
			row.Status = "running"
		case row.ready:
			row.Status = "ready"
		default:
			statuses := make([]string, 0, len(row.statuses))
			for status := range row.statuses {
				if status != "" {
					statuses = append(statuses, status)
				}
			}
			sort.Strings(statuses)
			row.Status = strings.Join(statuses, ", ")
			if row.Status == "" {
				row.Status = "observed"
			}
		}
		out = append(out, row.DeploymentRow)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func projectPipelines(topology *daemonclient.Topology, jobs []*daemonclient.Job) []PipelineRow {
	rows := make([]PipelineRow, 0, len(topology.Pipelines))
	for _, pipeline := range topology.Pipelines {
		active := 0
		for _, job := range jobs {
			if job != nil && job.Pipeline == pipeline.Name && activeJobStatus(job.Status) {
				active++
			}
		}
		rows = append(rows, PipelineRow{Name: pipeline.Name, Trigger: triggerSummary(pipeline.Trigger), Steps: stepSummary(pipeline.Steps), Active: active})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func projectBudgets(topology *daemonclient.Topology, jobs []*daemonclient.Job) []BudgetRow {
	byPipeline := map[string]string{}
	for _, team := range topology.Teams {
		for _, pipeline := range team.Pipelines {
			byPipeline[pipeline] = team.Name
		}
	}
	rows := make([]BudgetRow, 0, len(topology.Budgets))
	for _, budget := range topology.Budgets {
		active := 0
		for _, job := range jobs {
			if job != nil && activeJobStatus(job.Status) && byPipeline[job.Pipeline] == budget.Team {
				active++
			}
		}
		capText := "unbounded"
		var open *int
		if budget.JobsInFlight > 0 {
			capText = strconv.Itoa(budget.JobsInFlight)
			available := max(0, budget.JobsInFlight-active)
			open = &available
		}
		allocation := budget.Allocation
		if allocation == "" {
			allocation = "unconfigured"
		}
		rows = append(rows, BudgetRow{Team: budget.Team, Tokens: compactInt(budget.TokensPerDay), Cap: capText, Allocation: allocation, Active: active, Open: open})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Team < rows[j].Team })
	return rows
}

func projectSchedules(topology *daemonclient.Topology) []ScheduleRow {
	rows := make([]ScheduleRow, 0, len(topology.Schedules))
	for _, schedule := range topology.Schedules {
		fired := "pending"
		if schedule.RunOnStart {
			fired = "on start"
		}
		if schedule.LastFiredAt != nil {
			fired = schedule.LastFiredAt.UTC().Format("2006-01-02 15:04:05 UTC")
		}
		rows = append(rows, ScheduleRow{Name: schedule.Name, Cadence: schedule.Every, LastFired: fired, Team: firstText(schedule.Team, "-"), Payload: payloadSummary(schedule.Payload)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func projectTeams(topology *daemonclient.Topology, jobs []*daemonclient.Job) []TeamRow {
	rows := make([]TeamRow, 0, len(topology.Teams))
	for _, team := range topology.Teams {
		pipelines := map[string]bool{}
		for _, pipeline := range team.Pipelines {
			pipelines[pipeline] = true
		}
		active := 0
		for _, job := range jobs {
			if job != nil && pipelines[job.Pipeline] && activeJobStatus(job.Status) {
				active++
			}
		}
		rows = append(rows, TeamRow{Name: team.Name, Instances: len(team.Instances), Pipelines: len(team.Pipelines), Channels: len(team.Channels), Active: active})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func projectDeadlines(snapshot *daemonclient.Snapshot) []DeadlineRow {
	rows := []DeadlineRow{}
	seen := map[string]bool{}
	represented := map[string]bool{}
	add := func(identity, label, value, state, source string) {
		if value == "" || seen[identity] {
			return
		}
		seen[identity] = true
		rows = append(rows, DeadlineRow{Label: label, Deadline: formatDeadline(value), State: state, Source: source})
	}
	for _, job := range snapshot.Jobs {
		if job == nil {
			continue
		}
		resource := snapshot.Resources[job.URI]
		data := resourceMap(resource)
		rememberResource(represented, job.URI, resource, data)
		value, runtime := deadlineText(data)
		if value != "" {
			state := firstText(mapString(data, "deadline_state", "DeadlineState"), conditional(runtime, "runtime", "set"))
			add(firstText(resourceIdentity(data), job.URI, "job:"+job.ID), job.ID, value, state, firstText(mapString(data, "deadline_source", "DeadlineSource"), "job resource"))
		}
	}
	for _, instance := range snapshot.Instances {
		if instance == nil {
			continue
		}
		resource := snapshot.Resources[instance.URI]
		data := resourceMap(resource)
		rememberResource(represented, instance.URI, resource, data)
		value, runtime := deadlineText(data)
		source := "instance resource"
		if value == "" && !instance.RuntimeDeadline.IsZero() {
			value, runtime, source = instance.RuntimeDeadline.UTC().Format(time.RFC3339), true, "runtime watchdog"
		}
		if value != "" {
			state := firstText(mapString(data, "deadline_state", "DeadlineState"), conditional(runtime, "runtime", "set"))
			add(firstText(resourceIdentity(data), instance.URI, "instance:"+instance.Instance), instance.Instance, value, state, firstText(mapString(data, "deadline_source", "DeadlineSource"), source))
		}
	}
	for uri, resource := range snapshot.Resources {
		data := resourceMap(resource)
		if represented[uri] || represented[resourceIdentity(data)] {
			continue
		}
		value, runtime := deadlineText(data)
		if value == "" {
			continue
		}
		kind := "resource"
		if resource != nil && resource.Kind != "" {
			kind = resource.Kind
		}
		label := shortURI(uri)
		state := firstText(mapString(data, "deadline_state", "DeadlineState"), conditional(runtime, "runtime", "set"))
		add(firstText(resourceIdentity(data), uri, kind+":"+label), label, value, state, firstText(mapString(data, "deadline_source", "DeadlineSource"), kind+" resource"))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Label < rows[j].Label })
	return rows
}

func deadlineText(data map[string]any) (string, bool) {
	if durable := mapString(data, "deadline", "Deadline"); validDeadlineText(durable) {
		return durable, false
	}
	if runtime := mapString(data, "runtime_deadline", "RuntimeDeadline"); validDeadlineText(runtime) {
		return runtime, true
	}
	return "", false
}

func rememberResource(seen map[string]bool, fallback string, resource *daemonclient.Resource, data map[string]any) {
	for _, value := range []string{fallback, resourceIdentity(data)} {
		if value != "" {
			seen[value] = true
		}
	}
	if resource != nil && resource.URI != "" {
		seen[resource.URI] = true
	}
}

func formatDeadline(value string) string {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	return value
}

func triggerSummary(trigger *daemonclient.TopologyTrigger) string {
	if trigger == nil {
		return "-"
	}
	parts := []string{trigger.Event}
	keys := make([]string, 0, len(trigger.Match))
	for key := range trigger.Match {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+renderValue(trigger.Match[key]))
	}
	return strings.Join(parts, " ")
}

func stepSummary(steps []daemonclient.TopologyPipelineStep) string {
	parts := make([]string, 0, len(steps))
	for _, step := range steps {
		name := firstText(step.Label, step.ID)
		budget := []string{}
		if step.TokenBudget > 0 {
			budget = append(budget, compactInt(step.TokenBudget))
		}
		if value := firstText(step.TimeBudget, step.Timeout); value != "-" {
			budget = append(budget, value)
		}
		label := name + " -> " + firstText(step.Target, "-")
		if len(budget) > 0 {
			label += " (" + strings.Join(budget, " / ") + ")"
		}
		parts = append(parts, label)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "; ")
}

func payloadSummary(payload map[string]any) string {
	if len(payload) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 3 {
		keys = keys[:3]
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+renderValue(payload[key]))
	}
	return strings.Join(parts, " ")
}

func renderValue(value any) string {
	switch typed := value.(type) {
	case []string:
		return strings.Join(typed, "|")
	case []any:
		parts := make([]string, len(typed))
		for index, item := range typed {
			parts[index] = fmt.Sprint(item)
		}
		return strings.Join(parts, "|")
	default:
		return fmt.Sprint(value)
	}
}

func deploymentID(uri string) string {
	if strings.HasPrefix(uri, "agt://") {
		if rest := strings.TrimPrefix(uri, "agt://"); rest != "" {
			if slash := strings.IndexByte(rest, '/'); slash >= 0 {
				return rest[:slash]
			}
		}
	}
	return uri
}

func shortURI(uri string) string {
	raw := strings.TrimSpace(uri)
	if !strings.HasPrefix(raw, "agt://") {
		return firstText(raw, "-")
	}
	parts := strings.SplitN(strings.TrimPrefix(raw, "agt://"), "/", 3)
	if len(parts) != 3 {
		return raw
	}
	id, _ := url.PathUnescape(parts[2])
	return parts[1] + "/" + id
}

func compactInt(value int64) string {
	if value == 0 {
		return "-"
	}
	abs := value
	if abs < 0 {
		abs = -abs
	}
	if abs < 100000 {
		return strconv.FormatInt(value, 10)
	}
	for _, unit := range []struct {
		value  int64
		suffix string
	}{{1_000_000_000, "B"}, {1_000_000, "M"}, {1_000, "K"}} {
		if abs >= unit.value {
			return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(value)/float64(unit.value)), ".0") + unit.suffix
		}
	}
	return strconv.FormatInt(value, 10)
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(typed)
		return parsed
	case float64:
		return typed != 0
	default:
		return false
	}
}

func conditional(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}

func activeLabel(active int) string {
	if active > 0 {
		return fmt.Sprintf("%d active", active)
	}
	return "idle"
}

func queryMatches(query string, values map[string]string, allowed map[string]bool) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || validateQuery(query, allowed) != "" {
		return true
	}
	plain := []string{}
	fields := map[string][]string{}
	for _, term := range strings.Fields(query) {
		if separator := strings.IndexByte(term, ':'); separator > 0 {
			fields[term[:separator]] = append(fields[term[:separator]], term[separator+1:])
		} else {
			plain = append(plain, term)
		}
	}
	all := []string{}
	for _, value := range values {
		all = append(all, value)
	}
	haystack := strings.ToLower(strings.Join(all, " "))
	for _, term := range plain {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	for field, wants := range fields {
		matched := false
		value := strings.ToLower(values[field])
		for _, want := range wants {
			matched = matched || strings.Contains(value, want)
		}
		if !matched {
			return false
		}
	}
	return true
}

func validateQuery(query string, allowed map[string]bool) string {
	for _, term := range strings.Fields(query) {
		if index := strings.IndexByte(term, ':'); index > 0 && !allowed[strings.ToLower(term[:index])] {
			return "unknown filter field: " + term[:index]
		}
	}
	return ""
}

func allowedQueryFields(screen Screen) map[string]bool {
	fields := map[Screen][]string{
		ScreenOverview:       {"id", "status", "type", "role", "ticket"},
		ScreenWorkJobs:       {"id", "ticket", "status", "pipeline", "model", "tier", "bounce", "instance"},
		ScreenWorkTelemetry:  {"model", "tier", "bounce", "status"},
		ScreenFleetInstances: {"name", "agent", "status", "phase", "model", "tier", "job"},
		ScreenFleetOrg:       {"role", "lane", "state", "schedule", "job", "ticket"},
		ScreenFleetTopology:  {"section", "name", "team", "status", "trigger"},
	}[screen]
	out := map[string]bool{}
	for _, field := range fields {
		out[field] = true
	}
	return out
}

func jobQueryValues(row JobRow) map[string]string {
	classes := make([]string, 0, len(row.Bounces))
	for class := range row.Bounces {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	return map[string]string{"id": row.ID, "ticket": row.Ticket, "status": row.Status, "pipeline": row.Pipeline, "model": row.Model, "tier": row.Tier, "bounce": strings.Join(classes, " "), "instance": row.Instance}
}

func instanceQueryValues(row InstanceRow) map[string]string {
	return map[string]string{"name": row.Name, "agent": row.Agent, "status": row.Status, "phase": row.Phase, "model": row.Model, "tier": row.Tier, "job": row.Job}
}

func orgQueryValues(row OrgLaneRow) map[string]string {
	jobs, tickets := []string{}, []string{}
	for _, instance := range row.Instances {
		jobs = append(jobs, instance.Job)
		tickets = append(tickets, instance.Ticket)
	}
	return map[string]string{"role": row.Agent, "lane": row.Name, "state": row.State, "schedule": row.Meta, "job": strings.Join(jobs, " "), "ticket": strings.Join(tickets, " ")}
}

func keepDeployments(rows []DeploymentRow, keep func(DeploymentRow) bool) []DeploymentRow {
	out := rows[:0]
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	return out
}
func keepPipelines(rows []PipelineRow, keep func(PipelineRow) bool) []PipelineRow {
	out := rows[:0]
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	return out
}
func keepBudgets(rows []BudgetRow, keep func(BudgetRow) bool) []BudgetRow {
	out := rows[:0]
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	return out
}
func keepSchedules(rows []ScheduleRow, keep func(ScheduleRow) bool) []ScheduleRow {
	out := rows[:0]
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	return out
}
func keepDeadlines(rows []DeadlineRow, keep func(DeadlineRow) bool) []DeadlineRow {
	out := rows[:0]
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	return out
}
func keepTeams(rows []TeamRow, keep func(TeamRow) bool) []TeamRow {
	out := rows[:0]
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	return out
}
