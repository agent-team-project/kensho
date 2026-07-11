package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/daemonclient"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/outcomes"
	"github.com/agent-team-project/agent-team/internal/resource"
	"github.com/agent-team-project/agent-team/internal/topology"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func TestSeededLiveDaemonDiscoveryAndOverviewParity(t *testing.T) {
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")
	t.Setenv("AGENT_TEAM_DAEMON_SOCKET", "")
	harness := newSeededLiveDaemon(t)
	harness.start(t)

	client, err := daemonclient.New(harness.teamDir, daemonclient.Options{Timeout: 2 * time.Second, KeepAlive: true})
	if err != nil {
		t.Fatalf("zero-environment discovery: %v", err)
	}
	connection := client.Connection()
	if connection.Kind != daemonclient.TransportHTTP || connection.Endpoint != daemon.DaemonHTTPURL(harness.daemon.HTTPAddr()) {
		t.Fatalf("persisted HTTP did not win over live Unix socket: %+v", connection)
	}
	if connection.TokenFile != daemon.OperatorTokenPath(harness.teamDir) {
		t.Fatalf("default token file = %q, want %q", connection.TokenFile, daemon.OperatorTokenPath(harness.teamDir))
	}
	snapshot := client.Snapshot(context.Background(), fixtureTime)
	if !snapshot.Complete() {
		t.Fatalf("authenticated live snapshot errors = %v", snapshot.SourceErrors)
	}
	instances, err := client.Instances()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := client.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	topology, err := client.Topology()
	if err != nil {
		t.Fatal(err)
	}
	model := modelFromSnapshot(snapshot)
	projection := projectOverview(model)
	oracle := canonicalOverviewAPIOracle(t, instances, jobs, topology, snapshot.Resources)
	if projection.Summary != oracle {
		t.Fatalf("typed TUI/API parity mismatch: projection=%+v oracle=%+v", projection.Summary, oracle)
	}
	want := OverviewSummary{
		Instances: 6, Running: 4, Jobs: 12, ActiveJobs: 7, BlockedJobs: 2, FailedJobs: 1,
		ModelTiers: 4, BounceClasses: 4, Pipelines: 4, Budgets: 2, Teams: 3, Schedules: 5,
		Deployments: 2, Deadlines: 3,
	}
	if oracle != want || snapshot.DeploymentID != "tui-small-v1" {
		t.Fatalf("seeded tui-small-v1 oracle = %+v want=%+v deployment=%q", oracle, want, snapshot.DeploymentID)
	}
	child := snapshot.Resources[resource.DeploymentURI("tui-small-child")]
	childData := liveResourceData(t, child)
	if child == nil || childData["charter_uri"] == "" || childData["parent_uri"] != resource.DeploymentURI("tui-small-v1") {
		t.Fatalf("canonical child deployment/charter resource = %+v data=%v", child, childData)
	}

	unauthenticated, err := http.Get(connection.Endpoint + "/v1/jobs")
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401 to prove the discovered token was required", unauthenticated.StatusCode)
	}

	if err := os.Remove(daemon.HTTPAddrPath(harness.teamDir)); err != nil {
		t.Fatal(err)
	}
	unixClient, err := daemonclient.New(harness.teamDir, daemonclient.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Unix fallback discovery: %v", err)
	}
	if got := unixClient.Connection(); got.Kind != daemonclient.TransportUnix || got.Endpoint != daemon.SocketPath(harness.teamDir) {
		t.Fatalf("Unix fallback connection = %+v", got)
	}
	if unixSnapshot := unixClient.Snapshot(context.Background(), fixtureTime); !unixSnapshot.Complete() || len(unixSnapshot.Jobs) != len(jobs) {
		t.Fatalf("Unix snapshot = complete %v jobs %d errors %v", unixSnapshot.Complete(), len(unixSnapshot.Jobs), unixSnapshot.SourceErrors)
	}

	client.CloseIdleConnections()
	unixClient.CloseIdleConnections()
	http.DefaultClient.CloseIdleConnections()
	harness.stop(t)
	if err := os.WriteFile(daemon.PidPath(harness.teamDir), []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := daemon.SetPidLiveCheckForTest(func(int) bool { return false })
	defer restore()
	if _, err := daemonclient.New(harness.teamDir, daemonclient.Options{}); !errors.Is(err, daemonclient.ErrNotRunning) {
		t.Fatalf("stale pidfile discovery error = %v, want ErrNotRunning", err)
	}
}

func TestPTYInducesRealDaemonDisconnectAndRecovery(t *testing.T) {
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")
	t.Setenv("AGENT_TEAM_DAEMON_SOCKET", "")
	harness := newSeededLiveDaemon(t)
	harness.start(t)

	clockAt := fixtureTime
	runtime := &commandRuntime{ctx: context.Background(), teamDir: harness.teamDir, clock: func() time.Time { return clockAt }}
	domain := NewModel(clockAt, Capabilities{})
	domain.Polling = false
	testModel := teatest.NewTestModel(t, newProgramModel(domain, runtime), teatest.WithInitialTermSize(80, 24))
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool { return strings.Contains(string(output), "CONNECTED") }, teatest.WithDuration(5*time.Second))

	harness.stop(t)
	clockAt = clockAt.Add(time.Second)
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool { return strings.Contains(string(output), "DISCONNECTED") }, teatest.WithDuration(5*time.Second))

	harness.start(t)
	clockAt = clockAt.Add(time.Second)
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool { return strings.Contains(string(output), "RECONNECTED") }, teatest.WithDuration(5*time.Second))
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	final := testModel.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(ProgramModel)
	if final.Domain.Connection != ConnectionReconnected {
		t.Fatalf("final connection = %s, want reconnected", final.Domain.Connection)
	}
}

func modelFromSnapshot(snapshot *daemonclient.Snapshot) Model {
	model := NewModel(fixtureTime, Capabilities{})
	model.Booted = true
	model.RefreshInFlight = true
	for _, source := range daemonclient.SnapshotSources() {
		model, _ = Update(model, SnapshotOK{Source: source, Snapshot: snapshot, At: snapshot.SourceTimes[source]})
	}
	model, _ = Update(model, RefreshFinished{At: snapshot.CapturedAt, AnySuccess: true, Complete: snapshot.Complete()})
	return model
}

// canonicalOverviewAPIOracle independently normalizes the typed daemon API
// fixture. It intentionally does not call the TUI projection helpers under
// test, so parity fails when either the fixture or projection drifts.
func canonicalOverviewAPIOracle(t *testing.T, instances []*daemonclient.Instance, jobs []*daemonclient.Job, topology *daemonclient.Topology, resources map[string]*daemonclient.Resource) OverviewSummary {
	t.Helper()
	oracle := OverviewSummary{Instances: len(instances), Jobs: len(jobs)}
	for _, instance := range instances {
		if instance != nil && instance.Status == daemonclient.InstanceRunning {
			oracle.Running++
		}
	}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		switch job.Status {
		case daemonclient.JobQueued, daemonclient.JobRunning, daemonclient.JobBlocked:
			oracle.ActiveJobs++
		}
		if job.Status == daemonclient.JobBlocked {
			oracle.BlockedJobs++
		}
		if job.Status == daemonclient.JobFailed {
			oracle.FailedJobs++
		}
	}
	if topology != nil {
		oracle.Pipelines = len(topology.Pipelines)
		oracle.Budgets = len(topology.Budgets)
		oracle.Teams = len(topology.Teams)
		oracle.Schedules = len(topology.Schedules)
	}

	recent := append([]*daemonclient.Job(nil), jobs...)
	sort.SliceStable(recent, func(i, j int) bool {
		if recent[i] == nil || recent[j] == nil {
			return recent[j] == nil
		}
		if !recent[i].UpdatedAt.Equal(recent[j].UpdatedAt) {
			return recent[i].UpdatedAt.After(recent[j].UpdatedAt)
		}
		return recent[i].ID < recent[j].ID
	})
	if len(recent) > 24 {
		recent = recent[:24]
	}
	modelTiers := map[string]bool{}
	bounceClasses := map[string]bool{}
	for _, job := range recent {
		if job == nil {
			continue
		}
		model, tier := liveJobModelTier(t, job, resources)
		if model == "" && tier == "" {
			modelTiers["not reported"] = true
		} else {
			modelTiers[model+"/"+tier] = true
		}
		for class := range liveBounceClassesForJob(t, job, resources) {
			bounceClasses[class] = true
		}
	}
	oracle.ModelTiers = len(modelTiers)
	oracle.BounceClasses = len(bounceClasses)

	deployments := map[string]bool{}
	for _, instance := range instances {
		if instance != nil && instance.DeploymentURI != "" {
			deployments[instance.DeploymentURI] = true
		}
	}
	for _, job := range jobs {
		if job != nil && job.DeploymentURI != "" {
			deployments[job.DeploymentURI] = true
		}
	}
	for _, envelope := range resources {
		liveCollectStringsByKey(liveResourceData(t, envelope), "deployment_uri", deployments)
	}
	oracle.Deployments = len(deployments)

	deadlines := map[string]bool{}
	represented := map[string]bool{}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		represented[job.URI] = true
		if liveDeadline(liveResourceData(t, resources[job.URI])) != "" {
			deadlines[job.URI] = true
		}
	}
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		represented[instance.URI] = true
		if liveDeadline(liveResourceData(t, resources[instance.URI])) != "" || !instance.RuntimeDeadline.IsZero() {
			deadlines[instance.URI] = true
		}
	}
	for uri, envelope := range resources {
		if !represented[uri] && liveDeadline(liveResourceData(t, envelope)) != "" {
			deadlines[uri] = true
		}
	}
	oracle.Deadlines = len(deadlines)
	return oracle
}

func liveJobModelTier(t *testing.T, job *daemonclient.Job, resources map[string]*daemonclient.Resource) (string, string) {
	t.Helper()
	jobData := liveResourceData(t, resources[job.URI])
	outcomeData := liveResourceData(t, resources[job.OutcomeURI])
	steps := liveObjectSlice(liveFirstValue([]map[string]any{jobData}, "steps", "Steps"))
	primary := strings.ToLower(strings.TrimSpace(job.ImplementationAgent))
	if primary == "" {
		primary = strings.ToLower(strings.TrimSpace(job.Target))
	}
	step := livePrimaryRecord(steps, "implement", primary, "")
	runs := liveObjectSlice(liveFirstValue(liveTelemetrySources(outcomeData), "step_runs", "StepRuns"))
	stepID := liveMapString(step, "id", "ID")
	run := livePrimaryRecord(runs, stepID, primary, "")
	sources := append(liveTelemetrySources(run), liveTelemetrySources(outcomeData)...)
	model := liveFirstString(sources, "model", "Model")
	tier := liveFirstString(sources, "tier", "Tier", "model_tier", "ModelTier")
	if tier == "" {
		switch strings.ToLower(model) {
		case "claude-fable-5":
			tier = "T0"
		case "claude-opus-4-8":
			tier = "T1"
		case "claude-sonnet-5":
			tier = "T2"
		case "claude-haiku-4-5":
			tier = "T3"
		}
	}
	return model, tier
}

func livePrimaryRecord(records []map[string]any, preferredID, primary, fallback string) map[string]any {
	if len(records) == 0 {
		return nil
	}
	for _, record := range records {
		if preferredID != "" && strings.EqualFold(liveMapString(record, "id", "ID"), preferredID) {
			return record
		}
	}
	for _, record := range records {
		if primary != "" && strings.EqualFold(liveFirstString([]map[string]any{record}, "target", "Target", "agent", "Agent"), primary) {
			return record
		}
	}
	for _, record := range records {
		if liveRecordProgressed(record) {
			return record
		}
	}
	if fallback != "" {
		for _, record := range records {
			if strings.EqualFold(liveMapString(record, "id", "ID"), fallback) {
				return record
			}
		}
	}
	return records[0]
}

func liveRecordProgressed(record map[string]any) bool {
	if attempts, ok := liveFirstValue([]map[string]any{record}, "attempts", "Attempts").(float64); ok && attempts != 0 {
		return true
	}
	if liveMapString(record, "instance", "Instance") != "" || liveFirstString([]map[string]any{record}, "running_at", "RunningAt", "started_at", "StartedAt", "finished_at", "FinishedAt") != "" {
		return true
	}
	switch strings.ToLower(liveMapString(record, "status", "Status")) {
	case "running", "done", "failed":
		return true
	default:
		return false
	}
}

func liveTelemetrySources(value map[string]any) []map[string]any {
	if value == nil {
		return nil
	}
	out := []map[string]any{value}
	for _, key := range []string{"telemetry", "Telemetry", "outcome", "Outcome", "outcome_record", "OutcomeRecord"} {
		if nested, ok := value[key].(map[string]any); ok {
			out = append(out, nested)
		}
	}
	return out
}

func liveFirstValue(sources []map[string]any, names ...string) any {
	for _, source := range sources {
		for _, name := range names {
			if value, ok := source[name]; ok && value != nil {
				return value
			}
		}
	}
	return nil
}

func liveFirstString(sources []map[string]any, names ...string) string {
	for _, source := range sources {
		if value := liveMapString(source, names...); value != "" {
			return value
		}
	}
	return ""
}

func liveMapString(source map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := source[name].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func liveObjectSlice(value any) []map[string]any {
	values, _ := value.([]any)
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func liveResourceData(t *testing.T, envelope *daemonclient.Resource) map[string]any {
	t.Helper()
	if envelope == nil || len(envelope.Data) == 0 {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode live resource %q: %v", envelope.URI, err)
	}
	return data
}

func liveBounceClassesForJob(t *testing.T, job *daemonclient.Job, resources map[string]*daemonclient.Resource) map[string]bool {
	t.Helper()
	outcome := liveResourceData(t, resources[job.OutcomeURI])
	if classes := liveClassSet(outcome["bounce_classes"]); len(classes) > 0 {
		return classes
	}
	if classes := liveClassSet(outcome["bounces"]); len(classes) > 0 {
		return classes
	}
	jobData := liveResourceData(t, resources[job.URI])
	if classes := liveClassSet(jobData["bounce_classes"]); len(classes) > 0 {
		return classes
	}
	classes := map[string]bool{}
	kickoff, _ := jobData["kickoff"].(string)
	lower := strings.ToLower(kickoff)
	if strings.Contains(lower, "review findings (bounce") {
		for class, phrase := range map[string]string{
			"capability": "capability", "scope": "scope", "infra": "infra", "spec-ambiguity": "spec-ambiguity",
		} {
			if strings.Contains(lower, phrase) {
				classes[class] = true
			}
		}
	}
	return classes
}

func liveClassSet(value any) map[string]bool {
	classes := map[string]bool{}
	switch typed := value.(type) {
	case map[string]any:
		for class, count := range typed {
			if number, ok := count.(float64); ok && number > 0 {
				classes[class] = true
			}
		}
	case []any:
		for _, item := range typed {
			if class, ok := item.(string); ok && class != "" {
				classes[class] = true
				continue
			}
			entry, _ := item.(map[string]any)
			values, _ := entry["classes"].([]any)
			for _, value := range values {
				if class, ok := value.(string); ok && class != "" {
					classes[class] = true
				}
			}
		}
	}
	return classes
}

func liveRecursiveString(value any, wanted string) string {
	switch typed := value.(type) {
	case map[string]any:
		if text, ok := typed[wanted].(string); ok && text != "" {
			return text
		}
		for _, child := range typed {
			if text := liveRecursiveString(child, wanted); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range typed {
			if text := liveRecursiveString(child, wanted); text != "" {
				return text
			}
		}
	}
	return ""
}

func liveCollectStringsByKey(value any, wanted string, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(key, wanted) {
				if text, ok := child.(string); ok && text != "" {
					out[text] = true
				}
			}
			liveCollectStringsByKey(child, wanted, out)
		}
	case []any:
		for _, child := range typed {
			liveCollectStringsByKey(child, wanted, out)
		}
	}
}

func liveDeadline(data map[string]any) string {
	for _, key := range []string{"deadline", "runtime_deadline"} {
		if text, ok := data[key].(string); ok && validDeadlineText(text) {
			return text
		}
	}
	return ""
}

type seededLiveDaemon struct {
	root    string
	teamDir string
	daemon  *daemon.Daemon
	cancel  context.CancelFunc
}

func newSeededLiveDaemon(t *testing.T) *seededLiveDaemon {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "agt-tui-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLiveFile(t, filepath.Join(teamDir, "config.toml"), "[project]\nid = \"tui-small-v1\"\n")
	writeLiveFile(t, filepath.Join(teamDir, "instances.toml"), `[instances.frontend-worker]
agent = "worker"
ephemeral = true
replicas = 2

[instances.platform-worker]
agent = "worker"
ephemeral = true
replicas = 2

[instances.reviewer]
agent = "reviewer"
ephemeral = true
replicas = 2

[instances.verifier]
agent = "verifier"
ephemeral = true
replicas = 2

[instances.manager]
agent = "manager"

[[instances.manager.triggers]]
event = "job.completed"

[instances.comms]
agent = "comms"

[instances.auditor]
agent = "auditor"

[instances.ticket-manager]
agent = "ticket-manager"

[pipelines.frontend_ticket_to_pr]
auto_advance = true
reap_worktree = "on_merge"
[pipelines.frontend_ticket_to_pr.trigger]
event = "agent.dispatch"

[[pipelines.frontend_ticket_to_pr.steps]]
id = "implement"
target = "frontend-worker"

[pipelines.platform]
reap_worktree = "on_merge"
[pipelines.platform.trigger]
event = "agent.dispatch"
[[pipelines.platform.steps]]
id = "implement"
target = "platform-worker"

[pipelines.release]
reap_worktree = "on_merge"
[pipelines.release.trigger]
event = "agent.dispatch"
[[pipelines.release.steps]]
id = "coordinate"
target = "manager"

[pipelines.quality]
reap_worktree = "on_merge"
[pipelines.quality.trigger]
event = "agent.dispatch"
[[pipelines.quality.steps]]
id = "review"
target = "reviewer"

[teams.frontend]
instances = ["frontend-worker", "reviewer"]
pipelines = ["frontend_ticket_to_pr"]

[teams.platform]
instances = ["platform-worker", "verifier", "manager"]
pipelines = ["platform"]

[teams.quality]
instances = ["comms", "auditor", "ticket-manager"]
pipelines = ["release", "quality"]

[budgets.frontend]
tokens_per_day = 40000000
jobs_in_flight = 2

[budgets.platform]
tokens_per_day = 80000000
jobs_in_flight = 2

[schedules.product-verify]
every = "24h"

[schedules.debt-sweep]
every = "24h"

[schedules.docs-freshness]
every = "24h"

[schedules.release]
every = "24h"

[schedules.feedback]
every = "24h"
`)
	parentDeploymentURI := resource.DeploymentURI("tui-small-v1")
	childDeploymentURI := resource.DeploymentURI("tui-small-child")
	charterID := "tui-small-child-charter"
	if err := daemon.WriteTeamCharter(daemon.DaemonRoot(teamDir), &daemon.TeamCharter{
		ID: charterID, URI: resource.CharterURI("tui-small-v1", charterID), Name: "secondary", Target: "comms",
		ParentDeploymentURI: parentDeploymentURI, ChildDeploymentID: "tui-small-child", ChildDeploymentURI: childDeploymentURI,
		Relationship: "child", State: daemon.TeamCharterStateRunning, CreatedAt: fixtureTime, UpdatedAt: fixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	exitCode := 1
	metadata := []*daemon.Metadata{
		{Instance: "frontend-worker-1", Agent: "worker", Job: "gh383-tui-spec", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: fixtureTime, Workspace: root, RuntimeDeadline: fixtureTime.Add(time.Hour), DeploymentURI: parentDeploymentURI},
		{Instance: "platform-worker-2", Agent: "worker", Job: "job-6", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: fixtureTime, Workspace: root, RuntimeDeadline: fixtureTime.Add(2 * time.Hour), DeploymentURI: parentDeploymentURI},
		{Instance: "reviewer-gh382", Agent: "reviewer", Job: "job-11", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: fixtureTime, Workspace: root, RuntimeDeadline: fixtureTime.Add(3 * time.Hour), DeploymentURI: parentDeploymentURI},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: fixtureTime, Workspace: root, DeploymentURI: parentDeploymentURI},
		{Instance: "verifier-2", Agent: "verifier", Status: daemon.StatusCrashed, StartedAt: fixtureTime, Workspace: root, ExitCode: &exitCode, DeploymentURI: parentDeploymentURI},
		{Instance: "comms", Agent: "comms", Status: daemon.StatusStopped, StartedAt: fixtureTime, Workspace: root, DeploymentURI: childDeploymentURI, DeploymentParentURI: parentDeploymentURI, Chartered: true, CharterURI: resource.CharterURI("tui-small-v1", charterID)},
	}
	for _, instance := range metadata {
		if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), instance); err != nil {
			t.Fatal(err)
		}
	}
	statuses := []jobstore.Status{
		jobstore.StatusRunning, jobstore.StatusBlocked, jobstore.StatusFailed, jobstore.StatusQueued,
		jobstore.StatusDone, jobstore.StatusRunning, jobstore.StatusDone, jobstore.StatusBlocked,
		jobstore.StatusQueued, jobstore.StatusDone, jobstore.StatusRunning, jobstore.StatusDone,
	}
	for i, status := range statuses {
		id := fmtJobID(i)
		job, err := jobstore.New("GH-"+fmtInt(380+i), "worker", "seeded tui-small-v1 job", fixtureTime.Add(-time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		job.ID = id
		job.Pipeline = "frontend_ticket_to_pr"
		job.Status = status
		job.Worktree = root
		job.UpdatedAt = fixtureTime.Add(-time.Duration(i) * time.Minute)
		if i == 0 {
			job.Steps = []jobstore.Step{
				{ID: "review", Target: "reviewer", Status: jobstore.StatusDone},
				{ID: "implement", Target: "worker", Status: jobstore.StatusDone},
			}
			jobstore.SetImplementationAgentFromSteps(job)
		}
		if i == 3 {
			job.Kickoff = "## Review findings (bounce 1)\nClass: spec-ambiguity\nThe contract needs clarification."
		}
		if i == 10 {
			job.DeploymentURI = childDeploymentURI
			job.DeploymentParentURI = parentDeploymentURI
		}
		if err := jobstore.Write(teamDir, job); err != nil {
			t.Fatal(err)
		}
		record := &outcomes.Record{JobID: id, Status: string(status), RecordedAt: fixtureTime}
		if i > 0 && i < 9 {
			record.Model = []string{"gpt-5.6", "gpt-5.5", "gpt-5.6"}[i%3]
			record.Tier = []string{"T2", "T1", "T3"}[i%3]
		}
		if i == 0 {
			record.StepRuns = []outcomes.StepRunRecord{
				{ID: "review", Target: "reviewer", Agent: "reviewer", Model: "claude-opus-4-8", Tier: "T1", Status: "done"},
				{ID: "implement", Target: "worker", Agent: "worker", Model: "gpt-5.6", Tier: "T2", Status: "done"},
			}
		}
		switch i {
		case 0:
			record.BounceClasses = map[string]int{"capability": 1}
			record.Bounces = []outcomes.BounceRecord{{Number: 1, Classes: []string{"infra"}}}
		case 1:
			record.Bounces = []outcomes.BounceRecord{{Number: 1, Classes: []string{"scope"}}}
		case 2:
			record.BounceClasses = map[string]int{"infra": 1}
		}
		if err := outcomes.WriteRecord(teamDir, record); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := topology.LoadFromTeamDir(teamDir); err != nil {
		t.Fatalf("load seeded tui-small-v1 topology: %v", err)
	}
	return &seededLiveDaemon{root: root, teamDir: teamDir}
}

func (h *seededLiveDaemon) start(t *testing.T) {
	t.Helper()
	if h.daemon != nil {
		t.Fatal("seeded daemon already running")
	}
	h.reseedRunningInstances(t)
	d, err := daemon.New(daemon.Config{TeamDir: h.teamDir, LogOut: io.Discard, HTTPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.daemon, h.cancel = d, cancel
	go func() { _ = d.Run(ctx) }()
	t.Cleanup(func() {
		if h.daemon != nil {
			h.stop(t)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		addr, _ := daemon.ReadHTTPAddr(h.teamDir)
		if addr != "" {
			client := daemonclient.NewHTTP(daemon.DaemonHTTPURL(addr), daemon.OperatorTokenPath(h.teamDir), daemonclient.Options{Timeout: time.Second})
			if _, err := client.Status(); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("seeded daemon did not become ready: %s", h.teamDir)
}

func (h *seededLiveDaemon) stop(t *testing.T) {
	t.Helper()
	if h.daemon == nil {
		return
	}
	// The fixture uses the test process PID to expose four canonical running
	// rows without launching agents. Let adopted-process watchers retire before
	// daemon restart so the soak measures a stable goroutine population.
	restorePIDCheck := daemon.SetPidLiveCheckForTest(func(int) bool { return false })
	watchersDone := time.Now().Add(3 * time.Second)
	retired := false
	for time.Now().Before(watchersDone) {
		allRetired := true
		for _, name := range seededRunningInstances {
			metadata, err := daemon.ReadMetadata(daemon.DaemonRoot(h.teamDir), name)
			if err == nil && metadata.Status == daemon.StatusRunning {
				allRetired = false
				break
			}
		}
		if allRetired {
			retired = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	restorePIDCheck()
	if !retired {
		t.Fatal("seeded daemon adopted-process watchers did not retire")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.daemon.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("shutdown seeded daemon: %v", err)
	}
	h.cancel()
	h.daemon = nil
	h.cancel = nil
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(daemon.SocketPath(h.teamDir)); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("seeded daemon socket remained after shutdown: %s", daemon.SocketPath(h.teamDir))
}

var seededRunningInstances = []string{"frontend-worker-1", "platform-worker-2", "reviewer-gh382", "manager"}

func (h *seededLiveDaemon) reseedRunningInstances(t *testing.T) {
	t.Helper()
	for _, name := range seededRunningInstances {
		metadata, err := daemon.ReadMetadata(daemon.DaemonRoot(h.teamDir), name)
		if err != nil {
			t.Fatal(err)
		}
		metadata.Status = daemon.StatusRunning
		metadata.PID = os.Getpid()
		metadata.Adopted = false
		metadata.ExitedAt = time.Time{}
		metadata.ExitCode = nil
		if err := daemon.WriteMetadata(daemon.DaemonRoot(h.teamDir), metadata); err != nil {
			t.Fatal(err)
		}
	}
}

func writeLiveFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fmtJobID(index int) string {
	if index == 0 {
		return "gh383-tui-spec"
	}
	if index == 1 {
		return "release-2026-07"
	}
	return "job-" + fmtInt(index+1)
}

func fmtInt(value int) string {
	return strconv.Itoa(value)
}
