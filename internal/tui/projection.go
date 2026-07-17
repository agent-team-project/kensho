package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

type OverviewSummary struct {
	Instances     int
	Running       int
	Jobs          int
	ActiveJobs    int
	BlockedJobs   int
	FailedJobs    int
	ModelTiers    int
	BounceClasses int
	Pipelines     int
	Budgets       int
	Teams         int
	Schedules     int
	Deployments   int
	Deadlines     int
}

type AttentionRow struct {
	ID       string
	Kind     string
	Status   string
	Detail   string
	Role     string
	Ticket   string
	Severity int
}

type OrgRow struct {
	Role     string
	Working  int
	Idle     int
	Crashed  int
	Queued   int
	Running  int
	Capacity int
}

type OverviewProjection struct {
	Summary   OverviewSummary
	Attention []AttentionRow
	Org       []OrgRow
}

func projectOverview(model Model) OverviewProjection {
	var out OverviewProjection
	if model.Snapshot == nil {
		return out
	}
	snapshot := model.Snapshot
	out.Summary.Instances = len(snapshot.Instances)
	for _, instance := range snapshot.Instances {
		if instance == nil {
			continue
		}
		switch instance.Status {
		case daemonclient.InstanceRunning:
			out.Summary.Running++
		case daemonclient.InstanceCrashed:
			out.Attention = append(out.Attention, AttentionRow{
				ID: instance.Instance, Kind: "instance", Status: "crashed", Role: instance.Agent,
				Detail: exitDetail(instance.ExitCode), Severity: 4,
			})
		}
	}
	out.Summary.Jobs = len(snapshot.Jobs)
	for _, job := range snapshot.Jobs {
		if job == nil {
			continue
		}
		active := job.Status == daemonclient.JobQueued || job.Status == daemonclient.JobRunning || job.Status == daemonclient.JobBlocked
		if active {
			out.Summary.ActiveJobs++
		}
		severity := 0
		switch job.Status {
		case daemonclient.JobFailed:
			out.Summary.FailedJobs++
			severity = 5
		case daemonclient.JobBlocked:
			out.Summary.BlockedJobs++
			severity = 4
		case daemonclient.JobRunning:
			severity = 2
		case daemonclient.JobQueued:
			severity = 1
		}
		if severity > 0 {
			out.Attention = append(out.Attention, AttentionRow{
				ID: job.ID, Kind: "job", Status: string(job.Status), Detail: firstText(job.Instance, job.Pipeline, job.Target),
				Role: job.Target, Ticket: job.Ticket, Severity: severity,
			})
		}
	}
	if topology := snapshot.Topology; topology != nil {
		out.Summary.Pipelines = len(topology.Pipelines)
		out.Summary.Budgets = len(topology.Budgets)
		out.Summary.Teams = len(topology.Teams)
		out.Summary.Schedules = len(topology.Schedules)
	}
	out.Summary.ModelTiers = distinctModelTiers(snapshot)
	out.Summary.BounceClasses = distinctBounceClasses(snapshot)
	out.Summary.Deployments = distinctDeployments(snapshot)
	out.Summary.Deadlines = distinctDeadlines(snapshot)
	out.Attention = filterAttention(out.Attention, model.Query)
	sort.SliceStable(out.Attention, func(i, j int) bool {
		if out.Attention[i].Severity != out.Attention[j].Severity {
			return out.Attention[i].Severity > out.Attention[j].Severity
		}
		return out.Attention[i].ID < out.Attention[j].ID
	})
	out.Org = projectOrg(snapshot)
	return out
}

func projectOrg(snapshot *daemonclient.Snapshot) []OrgRow {
	model := NewModel(time.Unix(0, 0).UTC(), Capabilities{})
	model.Snapshot = snapshot
	roles := projectLiveOrg(model)
	out := make([]OrgRow, 0, len(roles))
	for _, role := range roles {
		row := OrgRow{Role: role.Role, Working: role.Working, Idle: role.Idle, Crashed: role.Crashed, Queued: role.Queued}
		for _, lane := range role.Lanes {
			row.Running += lane.Running
			row.Capacity += lane.Capacity
			row.Queued += lane.Queued
		}
		out = append(out, row)
	}
	return out
}

func distinctModelTiers(snapshot *daemonclient.Snapshot) int {
	jobs := recentJobs(snapshot.Jobs, 24)
	groups := map[string]bool{}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		model, tier := jobModelTier(snapshot, job)
		if model == "" && tier == "" {
			groups["not reported"] = true
		} else {
			groups[firstText(model, "not reported")+"/"+firstText(tier, "not reported")] = true
		}
	}
	return len(groups)
}

func jobModelTier(snapshot *daemonclient.Snapshot, job *daemonclient.Job) (string, string) {
	if snapshot == nil || job == nil {
		return "", ""
	}
	jobData := resourceMap(snapshot.Resources[job.URI])
	outcomeData := resourceMap(snapshot.Resources[job.OutcomeURI])
	step := primaryJobStep(job, jobData)
	run := primaryStepRun(job, outcomeData, step)
	return modelTierFromSources(run, outcomeData)
}

func primaryJobStep(job *daemonclient.Job, data map[string]any) map[string]any {
	steps := objectSlice(firstMapValue([]map[string]any{data}, "steps", "Steps"))
	if len(steps) == 0 {
		return nil
	}
	primary := strings.ToLower(strings.TrimSpace(job.ImplementationAgent))
	if primary == "" {
		primary = strings.ToLower(strings.TrimSpace(job.Target))
	}
	for _, step := range steps {
		if strings.EqualFold(mapString(step, "id", "ID"), "implement") {
			return step
		}
	}
	for _, step := range steps {
		if primary != "" && strings.EqualFold(firstMapString([]map[string]any{step}, "target", "Target", "agent", "Agent"), primary) {
			return step
		}
	}
	for _, step := range steps {
		if stepProgressed(step) {
			return step
		}
	}
	return steps[0]
}

func primaryStepRun(job *daemonclient.Job, outcomeData, step map[string]any) map[string]any {
	runs := objectSlice(firstMapValue(telemetrySources(outcomeData), "step_runs", "StepRuns"))
	if len(runs) == 0 {
		return nil
	}
	stepID := strings.TrimSpace(mapString(step, "id", "ID"))
	primary := strings.ToLower(strings.TrimSpace(job.ImplementationAgent))
	if primary == "" {
		primary = strings.ToLower(strings.TrimSpace(job.Target))
	}
	for _, run := range runs {
		if stepID != "" && strings.EqualFold(mapString(run, "id", "ID"), stepID) {
			return run
		}
	}
	for _, run := range runs {
		if primary != "" && strings.EqualFold(firstMapString([]map[string]any{run}, "target", "Target", "agent", "Agent"), primary) {
			return run
		}
	}
	for _, run := range runs {
		if stepProgressed(run) {
			return run
		}
	}
	return runs[0]
}

func stepProgressed(value map[string]any) bool {
	if value == nil {
		return false
	}
	if numberPositive(firstMapValue([]map[string]any{value}, "attempts", "Attempts")) || mapString(value, "instance", "Instance") != "" {
		return true
	}
	if firstMapString([]map[string]any{value}, "running_at", "RunningAt", "started_at", "StartedAt", "finished_at", "FinishedAt") != "" {
		return true
	}
	switch strings.ToLower(mapString(value, "status", "Status")) {
	case "running", "done", "failed":
		return true
	default:
		return false
	}
}

func modelTierFromSources(sources ...map[string]any) (string, string) {
	flattened := make([]map[string]any, 0, len(sources)*2)
	for _, source := range sources {
		flattened = append(flattened, telemetrySources(source)...)
	}
	model := firstMapString(flattened, "model", "Model")
	tier := firstMapString(flattened, "tier", "Tier", "model_tier", "ModelTier")
	if tier == "" {
		tier = tierForModel(model)
	}
	return model, tier
}

func tierForModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "claude-fable-5":
		return "T0"
	case "claude-opus-4-8":
		return "T1"
	case "claude-sonnet-5":
		return "T2"
	case "claude-haiku-4-5":
		return "T3"
	default:
		return ""
	}
}

func telemetrySources(value map[string]any) []map[string]any {
	if value == nil {
		return nil
	}
	out := []map[string]any{value}
	for _, name := range []string{"telemetry", "Telemetry", "outcome", "Outcome", "outcome_record", "OutcomeRecord"} {
		if nested, ok := value[name].(map[string]any); ok {
			out = append(out, nested)
		}
	}
	return out
}

func firstMapValue(sources []map[string]any, names ...string) any {
	for _, source := range sources {
		for _, name := range names {
			if value, ok := source[name]; ok && value != nil {
				return value
			}
		}
	}
	return nil
}

func firstMapString(sources []map[string]any, names ...string) string {
	for _, source := range sources {
		if value := mapString(source, names...); value != "" {
			return value
		}
	}
	return ""
}

func mapString(source map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := source[name].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func objectSlice(value any) []map[string]any {
	values, _ := value.([]any)
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func recentJobs(source []*daemonclient.Job, limit int) []*daemonclient.Job {
	jobs := append([]*daemonclient.Job(nil), source...)
	sort.SliceStable(jobs, func(i, j int) bool {
		if jobs[i] == nil || jobs[j] == nil {
			return jobs[j] == nil
		}
		if !jobs[i].UpdatedAt.Equal(jobs[j].UpdatedAt) {
			return jobs[i].UpdatedAt.After(jobs[j].UpdatedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	if limit >= 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs
}

func distinctBounceClasses(snapshot *daemonclient.Snapshot) int {
	classes := map[string]bool{}
	for _, job := range recentJobs(snapshot.Jobs, 24) {
		if job == nil {
			continue
		}
		for class := range bounceClassesForJob(snapshot, job) {
			classes[class] = true
		}
	}
	return len(classes)
}

func bounceClassesForJob(snapshot *daemonclient.Snapshot, job *daemonclient.Job) map[string]bool {
	classes := map[string]bool{}
	for class := range bounceCountsForJob(snapshot, job) {
		classes[class] = true
	}
	return classes
}

func distinctDeployments(snapshot *daemonclient.Snapshot) int {
	deployments := map[string]bool{}
	for _, instance := range snapshot.Instances {
		if instance != nil && instance.DeploymentURI != "" {
			deployments[instance.DeploymentURI] = true
		}
	}
	for _, job := range snapshot.Jobs {
		if job != nil && job.DeploymentURI != "" {
			deployments[job.DeploymentURI] = true
		}
	}
	for _, resource := range snapshot.Resources {
		collectStringsByKey(resourceMap(resource), "deployment_uri", deployments)
	}
	return len(deployments)
}

func distinctDeadlines(snapshot *daemonclient.Snapshot) int {
	deadlines := map[string]bool{}
	representedResources := map[string]bool{}
	for _, job := range snapshot.Jobs {
		if job == nil {
			continue
		}
		resource := snapshot.Resources[job.URI]
		data := resourceMap(resource)
		rememberRepresentedResource(representedResources, job.URI, resource, data)
		if deadlineValue(data) != "" {
			deadlines[firstText(resourceIdentity(data), job.URI, "job:"+job.ID)] = true
		}
	}
	for _, instance := range snapshot.Instances {
		if instance == nil {
			continue
		}
		resource := snapshot.Resources[instance.URI]
		data := resourceMap(resource)
		rememberRepresentedResource(representedResources, instance.URI, resource, data)
		if deadlineValue(data) != "" || !instance.RuntimeDeadline.IsZero() {
			deadlines[firstText(resourceIdentity(data), instance.URI, "instance:"+instance.Instance)] = true
		}
	}
	for uri, resource := range snapshot.Resources {
		if representedResources[uri] {
			continue
		}
		data := resourceMap(resource)
		if deadlineValue(data) != "" {
			deadlines[firstText(resourceIdentity(data), uri)] = true
		}
	}
	return len(deadlines)
}

func deadlineValue(value map[string]any) string {
	for _, name := range []string{"deadline", "Deadline", "runtime_deadline", "RuntimeDeadline"} {
		if text, ok := value[name].(string); ok && validDeadlineText(text) {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func validDeadlineText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil && parsed.IsZero() {
		return false
	}
	return true
}

func resourceIdentity(value map[string]any) string {
	for _, name := range []string{"uri", "URI", "job_uri", "JobURI", "instance_uri", "InstanceURI"} {
		if text, ok := value[name].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func rememberRepresentedResource(seen map[string]bool, fallback string, resource *daemonclient.Resource, data map[string]any) {
	for _, uri := range []string{fallback, resourceIdentity(data)} {
		if strings.TrimSpace(uri) != "" {
			seen[strings.TrimSpace(uri)] = true
		}
	}
	if resource != nil && strings.TrimSpace(resource.URI) != "" {
		seen[strings.TrimSpace(resource.URI)] = true
	}
}

func collectStringsByKey(value any, wanted string, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(key, wanted) {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					out[text] = true
				}
			}
			collectStringsByKey(child, wanted, out)
		}
	case []any:
		for _, child := range typed {
			collectStringsByKey(child, wanted, out)
		}
	}
}

func resourceMap(resource *daemonclient.Resource) map[string]any {
	if resource == nil || len(resource.Data) == 0 {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(resource.Data, &out) != nil {
		return nil
	}
	return out
}

func recursiveString(value any, wanted string) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(key, wanted) {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					return text
				}
			}
		}
		for _, child := range typed {
			if found := recursiveString(child, wanted); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := recursiveString(child, wanted); found != "" {
				return found
			}
		}
	}
	return ""
}

func filterAttention(rows []AttentionRow, query string) []AttentionRow {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" || validateOverviewQuery(query) != "" {
		return rows
	}
	plain := []string{}
	fields := map[string][]string{}
	for _, term := range strings.Fields(query) {
		if i := strings.IndexByte(term, ':'); i > 0 {
			fields[term[:i]] = append(fields[term[:i]], term[i+1:])
		} else {
			plain = append(plain, term)
		}
	}
	out := make([]AttentionRow, 0, len(rows))
	for _, row := range rows {
		all := strings.ToLower(strings.Join([]string{row.ID, row.Kind, row.Status, row.Detail, row.Role, row.Ticket}, " "))
		match := true
		for _, term := range plain {
			match = match && strings.Contains(all, term)
		}
		values := map[string]string{"id": row.ID, "status": row.Status, "type": row.Kind, "role": row.Role, "ticket": row.Ticket}
		for field, wants := range fields {
			fieldMatch := false
			for _, want := range wants {
				fieldMatch = fieldMatch || strings.Contains(strings.ToLower(values[field]), want)
			}
			match = match && fieldMatch
		}
		if match {
			out = append(out, row)
		}
	}
	return out
}

func exitDetail(code *int) string {
	if code == nil {
		return "process exited unexpectedly"
	}
	return fmt.Sprintf("exit %d", *code)
}

func numberPositive(value any) bool {
	switch number := value.(type) {
	case float64:
		return number > 0
	case int:
		return number > 0
	case json.Number:
		parsed, _ := number.Float64()
		return parsed > 0
	default:
		return value != nil
	}
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "-"
}
