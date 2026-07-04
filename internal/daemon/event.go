package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/jobwrite"
	"github.com/jamesaud/agent-team/internal/loader"
	"github.com/jamesaud/agent-team/internal/origin"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/jamesaud/agent-team/internal/runtimehooks"
	"github.com/jamesaud/agent-team/internal/runtimeotel"
	teamtemplate "github.com/jamesaud/agent-team/internal/template"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/jamesaud/agent-team/internal/worktreecleanup"
	"github.com/jamesaud/agent-team/internal/worktreepolicy"
)

func (r *EventResolver) writeJobWithAudit(j *jobstore.Job, eventType, actor, message string, data map[string]string) error {
	if err := jobwrite.WriteWithAudit(r.teamDir, j, jobwrite.Options{
		EventType: eventType,
		Actor:     actor,
		Message:   message,
		Data:      data,
	}); err != nil {
		return err
	}
	if r.otel != nil {
		_ = r.otel.exportJob(j)
	}
	return nil
}

// DefaultQueueCap is the maximum number of events queued per declared
// ephemeral instance once its replica capacity is exhausted. Excess events
// are rejected with HTTP 429 by the resolver — the spec calls this out as a
// trade-off (see documentation/topology.md § Open question on replicas).
const DefaultQueueCap = 10

// MaxQueueAttempts is the number of failed spawn attempts before a queued
// event is moved to dead-letter. Initial capacity queueing does not count as
// an attempt; only failed dispatch attempts do.
const MaxQueueAttempts = 3

const (
	kickoffMailboxHeading  = "## Unread messages (delivered at dispatch)"
	kickoffMailboxMaxBytes = 8 * 1024
)

// EventResolver routes inbound events to declared instances per the topology.
// Persistent instances receive a JSON-encoded event payload via mailbox
// (the inbox skill drains it on the agent side). Ephemeral instances spawn
// a fresh claude child via the InstanceManager — capacity-limited per
// declared name with a small in-memory queue.
//
// Concurrency: a single mutex protects the per-instance counters and queues.
// The Manager's reap hook decrements the running counter and drains queued
// events; the hook is set on installation.
type EventResolver struct {
	mgr      *InstanceManager
	teamDir  string
	queueCap int
	logOut   io.Writer
	otel     *orchestrationTracer

	mu       sync.Mutex
	topo     *topology.Topology
	tracking map[string]*ephTracker // declared-name → tracker
	locks    map[string]*dispatchLockTracker
}

type ephTracker struct {
	running int
	queue   []*queuedEvent
}

type queuedEvent struct {
	id         string
	eventType  string
	payload    map[string]any
	queuedAt   time.Time
	uniqueName string
	reason     string
	locks      []string
	origin     origin.Envelope
	attempts   int
	lastError  string
	nextRetry  time.Time
}

type dispatchLockTracker struct {
	slots   int
	holders map[string]*LockLease // instance name → lease
}

// NewEventResolver installs a reap hook on mgr and returns a resolver bound
// to it. teamDir is the consumer's `.agent_team/` (used to resolve the
// workspace for spawned instances).
func NewEventResolver(mgr *InstanceManager, teamDir string, topo *topology.Topology) *EventResolver {
	r := &EventResolver{
		mgr:      mgr,
		teamDir:  teamDir,
		queueCap: DefaultQueueCap,
		topo:     topo,
		tracking: map[string]*ephTracker{},
		locks:    map[string]*dispatchLockTracker{},
		otel:     loadOrchestrationTracer(teamDir, mgr.daemonRoot),
	}
	mgr.SetReapHook(r.onReap)
	r.mu.Lock()
	r.recoverLockStateLocked(time.Now().UTC())
	r.mu.Unlock()
	_ = r.loadPersistedQueue()
	return r
}

// SetTopology swaps the live topology pointer (used by /v1/topology/reload).
// In-flight ephemeral spawns and their queue depth are preserved across
// reloads — the running children outlive the topology that spawned them.
func (r *EventResolver) SetTopology(t *topology.Topology) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.topo = t
	r.recoverLockStateLocked(time.Now().UTC())
}

// Topology returns the current topology pointer (for /v1/topology).
func (r *EventResolver) Topology() *topology.Topology {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.topo
}

// SetLogOutput wires daemon log output for resolver warnings. A nil writer
// disables resolver-owned log lines, which keeps focused unit tests quiet.
func (r *EventResolver) SetLogOutput(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logOut = w
}

// EventOutcome is the per-instance result of an Event call, returned in the
// HTTP response so callers know what was actuated.
type EventOutcome struct {
	Instance   string `json:"instance"`
	Action     string `json:"action"` // "dispatched" | "queued" | "messaged" | "blocked" | "rejected"
	InstanceID string `json:"instance_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	Pipeline   string `json:"pipeline,omitempty"`
	Step       string `json:"step,omitempty"`
}

// EventResult is the full resolver outcome, including side-effect metadata
// such as a durable job reconciled from a PR webhook.
type EventResult struct {
	Outcomes  []EventOutcome            `json:"outcomes"`
	Reconcile *jobstore.ReconcileResult `json:"reconcile,omitempty"`
	Trace     *topology.EventTrace      `json:"trace,omitempty"`
}

type pipelineStepDispatchResult struct {
	jobOutcomes   []EventOutcome
	eventOutcomes []EventOutcome
}

// Event resolves an inbound event against the topology and actuates each
// matched instance. Returns one outcome per matched instance; an empty slice
// means no triggers matched.
//
// payload is the inbound event payload; eventType is one of the known event
// types. Callers should pass the eventType through unchanged — webhook event
// types are passed through to triggers as-is.
func (r *EventResolver) Event(eventType string, payload map[string]any) ([]EventOutcome, error) {
	result, err := r.EventWithResult(eventType, payload)
	if err != nil {
		return nil, err
	}
	return result.Outcomes, nil
}

// EventWithResult is Event plus side-effect metadata for API callers that need
// to report more than matched instance outcomes.
func (r *EventResolver) EventWithResult(eventType string, payload map[string]any) (*EventResult, error) {
	if strings.TrimSpace(eventType) == "" {
		return nil, errors.New("event: type is required")
	}
	reconciled := r.reconcilePRJob(eventType, payload)
	r.mu.Lock()
	t := r.topo
	r.mu.Unlock()
	trace := traceForTopology(t, eventType, payload)
	if trace.MatchedRules == 0 {
		r.logZeroMatch(trace)
	}
	if t == nil {
		return &EventResult{Reconcile: reconciled, Trace: trace}, nil
	}
	matched := make([]*topology.Instance, 0, len(trace.MatchedInstanceNames()))
	for _, name := range trace.MatchedInstanceNames() {
		if inst := t.Find(name); inst != nil {
			matched = append(matched, inst)
		}
	}
	out := make([]EventOutcome, 0, len(matched))
	directOutcomes := make(map[string]EventOutcome, len(matched))
	for _, inst := range matched {
		outcome := r.actuate(inst, eventType, payload)
		directOutcomes[inst.Name] = outcome
		out = append(out, outcome)
	}
	for _, name := range trace.MatchedPipelineNames() {
		if pipeline := t.Pipelines[name]; pipeline != nil {
			out = append(out, r.actuatePipeline(pipeline, eventType, payload, directOutcomes)...)
		}
	}
	return &EventResult{Outcomes: out, Reconcile: reconciled, Trace: trace}, nil
}

func traceForTopology(t *topology.Topology, eventType string, payload map[string]any) *topology.EventTrace {
	if t != nil {
		trace := t.Trace(eventType, payload)
		return &trace
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return &topology.EventTrace{Type: eventType, Payload: payload, Entries: []topology.EventTraceEntry{}}
}

func (r *EventResolver) logZeroMatch(trace *topology.EventTrace) {
	r.mu.Lock()
	out := r.logOut
	r.mu.Unlock()
	if out == nil || trace == nil {
		return
	}
	payload, _ := json.Marshal(trace.Payload)
	fmt.Fprintf(out, "%s WARNING event %q matched 0 rules payload=%s\n", time.Now().UTC().Format(time.RFC3339), trace.Type, string(payload))
}

func (r *EventResolver) reconcilePRJob(eventType string, payload map[string]any) *jobstore.ReconcileResult {
	if !strings.HasPrefix(eventType, "pr.") {
		return nil
	}
	if strings.TrimSpace(r.teamDir) == "" {
		return nil
	}
	result, err := jobwrite.ReconcilePR(r.teamDir, jobstore.ReconcileInputFromPayload(eventType, payload), time.Now().UTC())
	if err == nil || errors.Is(err, jobstore.ErrNoReconcileMatch) || errors.Is(err, jobstore.ErrAmbiguousReconcileMatch) {
		if err == nil && result != nil && result.Job != nil {
			if r.otel != nil {
				_ = r.otel.exportJob(result.Job)
			}
			r.autoReapJob(result.Job.ID, worktreepolicy.OnMerge)
		}
		return result
	}
	return nil
}

func (r *EventResolver) actuate(inst *topology.Instance, eventType string, payload map[string]any) EventOutcome {
	var outcome EventOutcome
	if inst.Ephemeral {
		outcome = r.actuateEphemeral(inst, eventType, payload)
	} else {
		outcome = r.actuatePersistent(inst, eventType, payload)
	}
	return annotateOutcomeFromPayload(outcome, payload)
}

func (r *EventResolver) actuatePipeline(pipeline *topology.Pipeline, eventType string, payload map[string]any, directOutcomes map[string]EventOutcome) []EventOutcome {
	if pipeline == nil || len(pipeline.Steps) == 0 {
		return nil
	}
	now := time.Now().UTC()
	ticket := pipelineTicket(pipeline.Name, payload)
	kickoff := pipelineKickoff(eventType, payload)
	j, err := jobstore.New(ticket, pipeline.Steps[0].Target, kickoff, now)
	if err != nil {
		return []EventOutcome{{Instance: "pipeline:" + pipeline.Name, Action: "rejected", Reason: err.Error(), Pipeline: pipeline.Name}}
	}
	j.Pipeline = pipeline.Name
	j.Origin = origin.Envelope{
		Project: projectIDForTeamDir(r.teamDir),
		Team:    r.teamForPipeline(pipeline.Name),
		Agent:   pipeline.Steps[0].Target,
		Job:     j.ID,
		Trigger: origin.TriggerFromEvent(eventType, payload),
		Build:   buildinfo.Current("").Display(),
	}
	if pipeline.ReapWorktree != worktreepolicy.Never {
		j.ReapWorktree = pipeline.ReapWorktree
	}
	j.Merge = jobMergeFromPipeline(pipeline.Merge)
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		j.TicketURL = ticketURL
	}
	j.Steps = pipelineJobSteps(pipeline)
	pipelineEvent := "pipeline_created"
	if existing, err := jobstore.Read(r.teamDir, j.ID); err == nil {
		if canAdoptDirectPipelineDispatch(pipeline, existing, directOutcomes) {
			return r.adoptDirectPipelineDispatch(pipeline, eventType, payload, directOutcomes, existing)
		}
		return r.actuatePipelineReentry(pipeline, eventType, payload, directOutcomes, existing, now)
	} else if !os.IsNotExist(err) {
		return []EventOutcome{{Instance: "pipeline:" + pipeline.Name, Action: "rejected", Reason: err.Error(), JobID: j.ID, Pipeline: pipeline.Name}}
	}
	j.LastEvent = pipelineEvent
	j.LastStatus = "pipeline " + pipeline.Name
	data := map[string]string{
		"pipeline": pipeline.Name,
		"event":    eventType,
	}
	if j.TicketURL != "" {
		data["ticket_url"] = j.TicketURL
	}
	if err := r.writeJobWithAudit(j, pipelineEvent, "daemon", "", data); err != nil {
		return []EventOutcome{{Instance: "pipeline:" + pipeline.Name, Action: "rejected", Reason: err.Error(), JobID: j.ID, Pipeline: pipeline.Name}}
	}
	step := firstRunnablePipelineStep(pipeline)
	if step == nil {
		if blocked := firstBlockedInitialPipelineStep(pipeline); blocked != nil {
			return []EventOutcome{{
				Instance: "pipeline:" + pipeline.Name,
				Action:   "blocked",
				Reason:   pipelineStepGateBlockedReason(blocked),
				JobID:    j.ID,
				Pipeline: pipeline.Name,
				Step:     blocked.ID,
			}}
		}
		return []EventOutcome{{Instance: "pipeline:" + pipeline.Name, Action: "rejected", Reason: "no runnable step", JobID: j.ID, Pipeline: pipeline.Name}}
	}
	dispatch := r.dispatchPipelineStepWithDirectOutcomes(pipeline, step, j, payload, directOutcomes)
	outcomes := dispatch.jobOutcomes
	if latest, err := jobstore.Read(r.teamDir, j.ID); err == nil {
		j = latest
	}
	applyPipelineStepOutcome(j, step.ID, outcomes)
	_ = r.writeJobWithAudit(j, "", "daemon", "", map[string]string{
		"pipeline": pipeline.Name,
		"step":     step.ID,
	})
	return dispatch.eventOutcomes
}

func canAdoptDirectPipelineDispatch(pipeline *topology.Pipeline, j *jobstore.Job, directOutcomes map[string]EventOutcome) bool {
	if pipeline == nil || j == nil || strings.TrimSpace(j.Pipeline) != "" || len(directOutcomes) == 0 {
		return false
	}
	step := firstRunnablePipelineStep(pipeline)
	if step == nil {
		return false
	}
	outcome, ok := directOutcomes[step.Target]
	return ok && outcome.Action != "rejected"
}

func (r *EventResolver) adoptDirectPipelineDispatch(pipeline *topology.Pipeline, eventType string, payload map[string]any, directOutcomes map[string]EventOutcome, j *jobstore.Job) []EventOutcome {
	hydratePipelineJob(j, pipeline, payload)
	j.LastEvent = "pipeline_created"
	j.LastStatus = "pipeline " + pipeline.Name
	if err := r.writeJobWithAudit(j, "pipeline_created", "daemon", "", pipelineAuditData(pipeline.Name, eventType, j, "")); err != nil {
		return []EventOutcome{{Instance: "pipeline:" + pipeline.Name, Action: "rejected", Reason: err.Error(), JobID: j.ID, Pipeline: pipeline.Name}}
	}
	step := firstRunnablePipelineStep(pipeline)
	if step == nil {
		return nil
	}
	dispatch := r.dispatchPipelineStepWithDirectOutcomes(pipeline, step, j, payload, directOutcomes)
	outcomes := dispatch.jobOutcomes
	if latest, err := jobstore.Read(r.teamDir, j.ID); err == nil {
		j = latest
	}
	applyPipelineStepOutcome(j, step.ID, outcomes)
	_ = r.writeJobWithAudit(j, "", "daemon", "", map[string]string{
		"pipeline": pipeline.Name,
		"step":     step.ID,
	})
	return dispatch.eventOutcomes
}

func (r *EventResolver) actuatePipelineReentry(pipeline *topology.Pipeline, eventType string, payload map[string]any, directOutcomes map[string]EventOutcome, j *jobstore.Job, now time.Time) []EventOutcome {
	hydratePipelineJob(j, pipeline, payload)
	if !jobStatusTerminal(j.Status) {
		return r.pipelineReentryNoop(pipeline, eventType, j, now, "job "+j.ID+" already "+string(j.Status))
	}
	if !pipeline.RedispatchOnReentry {
		return r.pipelineReentryNoop(pipeline, eventType, j, now, "job "+j.ID+" already "+string(j.Status)+"; redispatch_on_reentry=false")
	}
	resetPipelineJobForReentry(j, pipeline, eventType, payload, now)
	if err := r.writeJobWithAudit(j, "reopened", "daemon", j.LastStatus, pipelineAuditData(pipeline.Name, eventType, j, "reopen")); err != nil {
		return []EventOutcome{{Instance: "pipeline:" + pipeline.Name, Action: "rejected", Reason: err.Error(), JobID: j.ID, Pipeline: pipeline.Name}}
	}
	step := firstRunnablePipelineStep(pipeline)
	if step == nil {
		if blocked := firstBlockedInitialPipelineStep(pipeline); blocked != nil {
			return []EventOutcome{{
				Instance: "pipeline:" + pipeline.Name,
				Action:   "blocked",
				Reason:   pipelineStepGateBlockedReason(blocked),
				JobID:    j.ID,
				Pipeline: pipeline.Name,
				Step:     blocked.ID,
			}}
		}
		return []EventOutcome{{Instance: "pipeline:" + pipeline.Name, Action: "rejected", Reason: "no runnable step", JobID: j.ID, Pipeline: pipeline.Name}}
	}
	dispatch := r.dispatchPipelineStepWithDirectOutcomes(pipeline, step, j, payload, directOutcomes)
	outcomes := dispatch.jobOutcomes
	if latest, err := jobstore.Read(r.teamDir, j.ID); err == nil {
		j = latest
	}
	applyPipelineStepOutcome(j, step.ID, outcomes)
	_ = r.writeJobWithAudit(j, "", "daemon", "", map[string]string{
		"pipeline": pipeline.Name,
		"step":     step.ID,
	})
	return dispatch.eventOutcomes
}

func (r *EventResolver) pipelineReentryNoop(pipeline *topology.Pipeline, eventType string, j *jobstore.Job, now time.Time, reason string) []EventOutcome {
	j.LastEvent = "pipeline_reentry_noop"
	j.LastStatus = reason
	j.UpdatedAt = now
	if err := r.writeJobWithAudit(j, "pipeline_reentry_noop", "daemon", reason, pipelineAuditData(pipeline.Name, eventType, j, "noop")); err != nil {
		return []EventOutcome{{Instance: "pipeline:" + pipeline.Name, Action: "rejected", Reason: err.Error(), JobID: j.ID, Pipeline: pipeline.Name}}
	}
	return []EventOutcome{{
		Instance: "pipeline:" + pipeline.Name,
		Action:   "noop",
		Reason:   reason,
		JobID:    j.ID,
		Pipeline: pipeline.Name,
	}}
}

func hydratePipelineJob(j *jobstore.Job, pipeline *topology.Pipeline, payload map[string]any) {
	j.Pipeline = pipeline.Name
	if pipeline.ReapWorktree != worktreepolicy.Never && strings.TrimSpace(j.ReapWorktree) == "" {
		j.ReapWorktree = pipeline.ReapWorktree
	}
	if j.Merge == nil {
		j.Merge = jobMergeFromPipeline(pipeline.Merge)
	}
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		j.TicketURL = ticketURL
	}
	if len(j.Steps) == 0 {
		j.Steps = pipelineJobSteps(pipeline)
	}
}

func resetPipelineJobForReentry(j *jobstore.Job, pipeline *topology.Pipeline, eventType string, payload map[string]any, now time.Time) {
	hydratePipelineJob(j, pipeline, payload)
	j.Target = pipeline.Steps[0].Target
	j.Kickoff = pipelineKickoff(eventType, payload)
	j.Status = jobstore.StatusQueued
	j.Held = false
	j.HoldReason = ""
	j.HoldUntil = time.Time{}
	j.Instance = ""
	j.Branch = ""
	j.Worktree = ""
	j.PR = ""
	j.Drift = nil
	j.Steps = pipelineJobSteps(pipeline)
	j.LastEvent = "reopened"
	j.LastStatus = "reopened by pipeline re-entry"
	j.UpdatedAt = now
}

func jobStatusTerminal(status jobstore.Status) bool {
	return status == jobstore.StatusDone || status == jobstore.StatusFailed
}

func pipelineAuditData(pipelineName, eventType string, j *jobstore.Job, reentry string) map[string]string {
	data := map[string]string{
		"pipeline": pipelineName,
		"event":    eventType,
	}
	if reentry != "" {
		data["reentry"] = reentry
	}
	if j != nil && j.TicketURL != "" {
		data["ticket_url"] = j.TicketURL
	}
	return data
}

func (r *EventResolver) dispatchPipelineStep(pipeline *topology.Pipeline, step *topology.PipelineStep, j *jobstore.Job, payload map[string]any) []EventOutcome {
	return r.dispatchPipelineStepWithDirectOutcomes(pipeline, step, j, payload, nil).jobOutcomes
}

func (r *EventResolver) dispatchPipelineStepWithDirectOutcomes(pipeline *topology.Pipeline, step *topology.PipelineStep, j *jobstore.Job, payload map[string]any, directOutcomes map[string]EventOutcome) pipelineStepDispatchResult {
	dispatchPayload := copyPayload(payload)
	dispatchPayload["source"] = "pipeline:" + pipeline.Name
	dispatchPayload["target"] = step.Target
	dispatchPayload["job_id"] = j.ID
	dispatchPayload["job"] = j.ID
	dispatchPayload["pipeline"] = pipeline.Name
	dispatchPayload["pipeline_step"] = step.ID
	dispatchPayload["ticket"] = j.Ticket
	if payloadString(dispatchPayload, "reap_worktree") == "" && pipeline.ReapWorktree != worktreepolicy.Never {
		dispatchPayload["reap_worktree"] = pipeline.ReapWorktree
	}
	// Thread the step's runtime budget through so the ephemeral spawn arms a
	// per-instance watchdog (a hung worker/reviewer otherwise holds a replica
	// slot forever). A zero step timeout leaves it to the env-level default.
	if ts := pipelineStepTimeoutString(step.Timeout); ts != "" {
		dispatchPayload["timeout"] = ts
	}
	kickoff := jobstore.StepDispatchKickoff(j.Kickoff, step.ID, step.Instructions)
	// Best-effort context passing: if the caller threaded the prior step's final
	// output through the payload (auto-advance does), hand it to this step so e.g.
	// a reviewer sees the worker's "opened PR #N" report. Never required.
	if prev := strings.TrimSpace(payloadString(payload, "previous_step_output")); prev != "" {
		heading := "## Output from previous step"
		if prevID := strings.TrimSpace(payloadString(payload, "previous_step_id")); prevID != "" {
			heading += " (" + prevID + ")"
		}
		kickoff = kickoff + "\n\n" + heading + ":\n" + prev
		delete(dispatchPayload, "previous_step_output")
		delete(dispatchPayload, "previous_step_id")
	}
	dispatchPayload["kickoff"] = kickoff
	if payloadString(dispatchPayload, "name") == "" {
		dispatchPayload["name"] = step.Target + "-" + j.ID
	}
	if payloadString(dispatchPayload, "workspace") == "" && step.Target == "worker" {
		dispatchPayload["workspace"] = "worktree"
	}

	r.mu.Lock()
	t := r.topo
	r.mu.Unlock()
	if t == nil {
		outcome := EventOutcome{Instance: step.Target, Action: "rejected", Reason: "topology not configured"}
		return pipelineStepDispatchResult{
			jobOutcomes:   []EventOutcome{outcome},
			eventOutcomes: []EventOutcome{outcome},
		}
	}
	matched := t.Resolve(topology.EventAgentDispatch, dispatchPayload)
	if len(matched) == 0 {
		if inst := t.Find(step.Target); inst != nil {
			matched = []*topology.Instance{inst}
		}
	}
	if len(matched) == 0 {
		outcome := EventOutcome{Instance: step.Target, Action: "rejected", Reason: "no agent.dispatch trigger matched pipeline step"}
		return pipelineStepDispatchResult{
			jobOutcomes:   []EventOutcome{outcome},
			eventOutcomes: []EventOutcome{outcome},
		}
	}
	result := pipelineStepDispatchResult{
		jobOutcomes:   make([]EventOutcome, 0, len(matched)),
		eventOutcomes: make([]EventOutcome, 0, len(matched)),
	}
	for _, inst := range matched {
		if prior, ok := directOutcomes[inst.Name]; ok {
			result.jobOutcomes = append(result.jobOutcomes, annotateOutcomeFromPayload(prior, dispatchPayload))
			continue
		}
		outcome := r.actuate(inst, topology.EventAgentDispatch, dispatchPayload)
		result.jobOutcomes = append(result.jobOutcomes, outcome)
		result.eventOutcomes = append(result.eventOutcomes, outcome)
	}
	return result
}

func copyPayload(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload)+8)
	for k, v := range payload {
		out[k] = v
	}
	return out
}

func annotateOutcomeFromPayload(outcome EventOutcome, payload map[string]any) EventOutcome {
	if outcome.JobID == "" {
		outcome.JobID = firstPayloadString(payload, "job_id", "job")
	}
	if outcome.Pipeline == "" {
		outcome.Pipeline = payloadString(payload, "pipeline")
	}
	if outcome.Step == "" {
		outcome.Step = payloadString(payload, "pipeline_step")
	}
	return outcome
}

func pipelineTicket(pipeline string, payload map[string]any) string {
	for _, key := range []string{"ticket", "ticket_id", "id"} {
		if v := payloadString(payload, key); v != "" {
			return v
		}
	}
	return pipeline + "-" + newSessionID()[0:8]
}

func pipelineKickoff(eventType string, payload map[string]any) string {
	if kickoff := payloadString(payload, "kickoff"); kickoff != "" {
		return kickoff
	}
	body, _ := json.Marshal(map[string]any{"event": eventType, "payload": payload})
	return string(body)
}

func pipelineJobSteps(pipeline *topology.Pipeline) []jobstore.Step {
	steps := make([]jobstore.Step, 0, len(pipeline.Steps))
	for i, step := range pipeline.Steps {
		status := jobstore.StatusQueued
		if i > 0 || strings.TrimSpace(step.Gate) != "" {
			status = jobstore.StatusBlocked
		}
		steps = append(steps, jobstore.Step{
			ID:               step.ID,
			Label:            step.Label,
			Description:      step.Description,
			Instructions:     step.Instructions,
			Target:           step.Target,
			Status:           status,
			After:            append([]string(nil), step.After...),
			Gate:             step.Gate,
			ApprovalRequired: step.ApprovalRequired,
			Optional:         step.Optional,
			Timeout:          pipelineStepTimeoutString(step.Timeout),
			MaxAttempts:      step.MaxAttempts,
			RetryOnCrash:     step.RetryOnCrash,
		})
	}
	return steps
}

func jobMergeFromPipeline(merge *topology.PipelineMerge) *jobstore.Merge {
	if merge == nil {
		return nil
	}
	return &jobstore.Merge{
		Strategy:   merge.Strategy,
		Script:     merge.Script,
		Land:       merge.Land,
		OwnedPaths: append([]string(nil), merge.OwnedPaths...),
	}
}

func pipelineStepTimeoutString(timeout time.Duration) string {
	if timeout <= 0 {
		return ""
	}
	return timeout.String()
}

// envInstanceMaxRuntime is the daemon-wide default runtime budget for ephemeral
// instances (workers/reviewers/spawned steps). It opts the whole deployment into
// the per-instance watchdog without per-step config — the backstop for codex/
// Claude children that wedge on the model backend and hold a replica slot.
const envInstanceMaxRuntime = "AGENT_TEAM_INSTANCE_MAX_RUNTIME"

// ephemeralRuntimeBudget resolves the wall-clock budget for an ephemeral spawn,
// in precedence order: the per-step `timeout` threaded through the payload, then
// the AGENT_TEAM_INSTANCE_MAX_RUNTIME env default, else 0 (watchdog disabled).
// Unparseable values are ignored so a bad config never accidentally arms — or,
// worse, never disarms by erroring — the watchdog; it simply falls through.
func ephemeralRuntimeBudget(payload map[string]any) time.Duration {
	if ts := strings.TrimSpace(payloadString(payload, "timeout")); ts != "" {
		if d, err := time.ParseDuration(ts); err == nil && d > 0 {
			return d
		}
	}
	if env := strings.TrimSpace(os.Getenv(envInstanceMaxRuntime)); env != "" {
		if d, err := time.ParseDuration(env); err == nil && d > 0 {
			return d
		}
	}
	return 0
}

func firstRunnablePipelineStep(pipeline *topology.Pipeline) *topology.PipelineStep {
	for _, step := range pipeline.Steps {
		if len(step.After) == 0 && strings.TrimSpace(step.Gate) == "" {
			return step
		}
	}
	for _, step := range pipeline.Steps {
		if len(step.After) == 0 && strings.TrimSpace(step.Gate) != "" {
			return nil
		}
	}
	return pipeline.Steps[0]
}

func firstBlockedInitialPipelineStep(pipeline *topology.Pipeline) *topology.PipelineStep {
	if pipeline == nil {
		return nil
	}
	for _, step := range pipeline.Steps {
		if len(step.After) == 0 && strings.TrimSpace(step.Gate) != "" {
			return step
		}
	}
	return nil
}

func pipelineStepGateBlockedReason(step *topology.PipelineStep) string {
	if step == nil {
		return "pipeline step is waiting"
	}
	switch strings.TrimSpace(step.Gate) {
	case jobstore.StepGateManual:
		return "initial step " + step.ID + " is waiting for manual approval"
	case jobstore.StepGatePR:
		return "initial step " + step.ID + " is waiting for PR metadata"
	default:
		return "initial step " + step.ID + " is waiting for gate " + step.Gate
	}
}

func applyPipelineStepOutcome(j *jobstore.Job, stepID string, outcomes []EventOutcome) {
	now := time.Now().UTC()
	j.UpdatedAt = now
	j.LastEvent = "pipeline_step"
	for _, oc := range outcomes {
		if oc.Action == "dispatched" || oc.Action == "messaged" {
			markPipelineStep(j, stepID, jobstore.StatusRunning, oc.InstanceID, oc.Reason, now)
			j.Status = jobstore.StatusRunning
			j.LastStatus = "running " + stepID
			return
		}
		if oc.Action == "queued" {
			markPipelineStep(j, stepID, jobstore.StatusQueued, oc.InstanceID, oc.Reason, now)
			j.Status = jobstore.StatusQueued
			j.LastStatus = "queued " + stepID
			return
		}
	}
	reason := "step rejected"
	if len(outcomes) > 0 && outcomes[0].Reason != "" {
		reason = outcomes[0].Reason
	}
	markPipelineStep(j, stepID, jobstore.StatusFailed, "", reason, now)
	j.Status = jobstore.StatusFailed
	j.LastStatus = reason
}

func markPipelineStep(j *jobstore.Job, stepID string, status jobstore.Status, instance, reason string, now time.Time) {
	for i := range j.Steps {
		if j.Steps[i].ID != stepID {
			continue
		}
		incrementAttempt := status == jobstore.StatusRunning || status == jobstore.StatusQueued
		if status == jobstore.StatusRunning {
			if j.Steps[i].Status == jobstore.StatusRunning && j.Steps[i].Instance == instance && !j.Steps[i].RunningAt.IsZero() {
				incrementAttempt = false
			}
			if j.Steps[i].Status == jobstore.StatusQueued && !j.Steps[i].QueuedAt.IsZero() && j.Steps[i].Attempts > 0 {
				incrementAttempt = false
			}
		}
		if status == jobstore.StatusQueued && j.Steps[i].Status == jobstore.StatusQueued && j.Steps[i].Instance == instance && !j.Steps[i].QueuedAt.IsZero() {
			incrementAttempt = false
		}
		j.Steps[i].Status = status
		if instance != "" {
			j.Steps[i].Instance = instance
		}
		if status == jobstore.StatusQueued {
			j.Steps[i].QueueReason = strings.TrimSpace(reason)
		}
		if incrementAttempt {
			j.Steps[i].Attempts++
		}
		if j.Steps[i].StartedAt.IsZero() {
			j.Steps[i].StartedAt = now
		}
		if status == jobstore.StatusQueued && j.Steps[i].QueuedAt.IsZero() {
			j.Steps[i].QueuedAt = now
		}
		if status == jobstore.StatusRunning {
			if j.Steps[i].QueuedAt.IsZero() {
				j.Steps[i].QueuedAt = j.Steps[i].StartedAt
			}
			if j.Steps[i].RunningAt.IsZero() {
				j.Steps[i].RunningAt = now
			}
		}
		if status == jobstore.StatusDone || status == jobstore.StatusFailed {
			if j.Steps[i].RunningAt.IsZero() {
				j.Steps[i].RunningAt = j.Steps[i].StartedAt
			}
			j.Steps[i].FinishedAt = now
		}
		return
	}
}

func (r *EventResolver) actuatePersistent(inst *topology.Instance, eventType string, payload map[string]any) EventOutcome {
	body := map[string]any{"event": eventType, "payload": payload}
	encoded, err := json.Marshal(body)
	if err != nil {
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	msg := &Message{From: "topology", To: inst.Name, Body: string(encoded), TS: time.Now().UTC()}
	if err := AppendMessage(r.mgr.daemonRoot, inst.Name, msg); err != nil {
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	if eventType == topology.EventAgentDispatch && !r.mgr.isRunning(inst.Name) {
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: inst.Name}
	}
	return EventOutcome{Instance: inst.Name, Action: "messaged", InstanceID: inst.Name}
}

func (r *EventResolver) actuateEphemeral(inst *topology.Instance, eventType string, payload map[string]any) EventOutcome {
	payload = payloadWithInstanceReapPolicy(inst, payload)
	childName, requested, err := childNameForEvent(inst.Name, payload)
	if err != nil {
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	eventOrigin := r.originForEvent(inst, childName, eventType, payload)
	now := time.Now().UTC()
	if requested && r.mgr.isRunning(childName) {
		reason := fmt.Sprintf("instance %q already running", childName)
		r.upsertDispatchJob(payload, childName, jobstore.StatusRunning, "already_running", reason, "", "")
		return EventOutcome{
			Instance:   inst.Name,
			Action:     "rejected",
			InstanceID: childName,
			Reason:     reason,
		}
	}

	r.mu.Lock()
	tr, ok := r.tracking[inst.Name]
	if !ok {
		tr = &ephTracker{}
		r.tracking[inst.Name] = tr
	}
	if requested && queuedChildName(tr.queue, childName) {
		r.mu.Unlock()
		reason := fmt.Sprintf("instance %q already queued", childName)
		r.upsertDispatchJob(payload, childName, jobstore.StatusQueued, "already_queued", reason, "", "")
		return EventOutcome{
			Instance:   inst.Name,
			Action:     "rejected",
			InstanceID: childName,
			Reason:     reason,
		}
	}
	if tr.running >= inst.Replicas {
		if len(tr.queue) >= r.queueCap {
			r.mu.Unlock()
			reason := fmt.Sprintf("at replica capacity (%d) and queue is full (%d)", inst.Replicas, r.queueCap)
			r.upsertDispatchJob(payload, childName, jobstore.StatusFailed, "queue_full", reason, "", "")
			return EventOutcome{
				Instance: inst.Name,
				Action:   "rejected",
				Reason:   reason,
			}
		}
		ev := &queuedEvent{
			id:         newSessionID(),
			eventType:  eventType,
			payload:    payload,
			queuedAt:   now,
			uniqueName: childName,
			reason:     QueueReasonReplicaCapacity,
			locks:      r.dispatchLocksLocked(inst, payload),
			origin:     eventOrigin,
		}
		if err := WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending)); err != nil {
			r.mu.Unlock()
			return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
		}
		tr.queue = append(tr.queue, ev)
		r.mu.Unlock()
		r.upsertDispatchJob(payload, childName, jobstore.StatusQueued, "queued", "queued", "", "")
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: childName}
	}
	locks := r.dispatchLocksLocked(inst, payload)
	acquired, err := r.acquireLocksLocked(locks, childName, eventOrigin, now)
	if err != nil {
		r.mu.Unlock()
		r.upsertDispatchJob(payload, childName, jobstore.StatusFailed, "lock_error", err.Error(), "", "")
		return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: err.Error()}
	}
	if !acquired {
		if len(tr.queue) >= r.queueCap {
			r.mu.Unlock()
			reason := fmt.Sprintf("lock held and queue is full (%d)", r.queueCap)
			r.upsertDispatchJob(payload, childName, jobstore.StatusFailed, "queue_full", reason, "", "")
			return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: reason}
		}
		ev := &queuedEvent{
			id:         newSessionID(),
			eventType:  eventType,
			payload:    payload,
			queuedAt:   now,
			uniqueName: childName,
			reason:     QueueReasonLockHeld,
			locks:      locks,
			origin:     eventOrigin,
		}
		if err := WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending)); err != nil {
			r.mu.Unlock()
			return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: err.Error()}
		}
		tr.queue = append(tr.queue, ev)
		r.mu.Unlock()
		r.upsertDispatchJob(payload, childName, jobstore.StatusQueued, QueueReasonLockHeld, QueueReasonLockHeld, "", "")
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: childName, Reason: QueueReasonLockHeld}
	}
	tr.running++
	r.mu.Unlock()

	meta, err := r.spawn(inst, childName, eventType, payload)
	if err != nil {
		// Spawn failed; release capacity and don't drain queue (no work freed).
		r.mu.Lock()
		r.releaseLocksForInstanceLocked(childName)
		tr.running--
		r.mu.Unlock()
		r.upsertDispatchJob(payload, childName, jobstore.StatusFailed, "dispatch_failed", err.Error(), "", "")
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	r.updateLockLeasePID(meta.Instance, meta.PID)
	return EventOutcome{Instance: inst.Name, Action: "dispatched", InstanceID: meta.Instance}
}

func childNameForEvent(declared string, payload map[string]any) (string, bool, error) {
	requested := payloadString(payload, "name")
	// Schedule events carry the SCHEDULE's identity in "name" (that is what
	// trigger match.name matches on) — it is never an instance-name request.
	if payloadString(payload, "source") == "schedule" {
		requested = ""
	}
	if requested == "" {
		requested = payloadString(payload, "instance")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return uniqueChildName(declared), false, nil
	}
	if err := validateRequestedChildName(declared, requested); err != nil {
		return "", false, err
	}
	return requested, true, nil
}

func queuedChildName(queue []*queuedEvent, name string) bool {
	for _, ev := range queue {
		if ev != nil && ev.uniqueName == name {
			return true
		}
	}
	return false
}

func queuedEventID(queue []*queuedEvent, id string) bool {
	for _, ev := range queue {
		if ev != nil && ev.id == id {
			return true
		}
	}
	return false
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func payloadWithInstanceReapPolicy(inst *topology.Instance, payload map[string]any) map[string]any {
	if inst == nil || payloadString(payload, "reap_worktree") != "" || inst.ReapWorktree == worktreepolicy.Never {
		return payload
	}
	out := copyPayload(payload)
	out["reap_worktree"] = inst.ReapWorktree
	return out
}

func (r *EventResolver) dispatchLocksLocked(inst *topology.Instance, payload map[string]any) []string {
	names := []string{}
	if inst != nil {
		names = append(names, inst.Locks...)
	}
	if r.topo != nil {
		pipelineName := payloadString(payload, "pipeline")
		stepID := payloadString(payload, "pipeline_step")
		if pipelineName != "" && stepID != "" {
			if pipeline := r.topo.Pipelines[pipelineName]; pipeline != nil {
				for _, step := range pipeline.Steps {
					if step.ID == stepID {
						names = append(names, step.Locks...)
						break
					}
				}
			}
		}
	}
	return normalizeLockNames(names)
}

func normalizeLockNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *EventResolver) acquireLocksLocked(names []string, instance string, env origin.Envelope, now time.Time) (bool, error) {
	names = normalizeLockNames(names)
	if len(names) == 0 {
		return true, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, name := range names {
		tr := r.lockTrackerLocked(name)
		if tr == nil {
			return false, fmt.Errorf("lock %q is not declared", name)
		}
		if tr.holders[instance] != nil {
			continue
		}
		if len(tr.holders) >= tr.slots {
			return false, nil
		}
	}
	acquired := []string{}
	for _, name := range names {
		tr := r.lockTrackerLocked(name)
		if tr.holders[instance] != nil {
			continue
		}
		lease := &LockLease{
			Lock:       name,
			Instance:   instance,
			AcquiredAt: now,
			UpdatedAt:  now,
			Origin:     env,
		}
		if err := WriteLockLease(r.mgr.daemonRoot, lease); err != nil {
			for _, held := range acquired {
				delete(r.locks[held].holders, instance)
				_ = RemoveLockLease(r.mgr.daemonRoot, held, instance)
			}
			return false, err
		}
		tr.holders[instance] = lease
		acquired = append(acquired, name)
	}
	return true, nil
}

func (r *EventResolver) lockTrackerLocked(name string) *dispatchLockTracker {
	if r.locks == nil {
		r.locks = map[string]*dispatchLockTracker{}
	}
	if tr := r.locks[name]; tr != nil {
		return tr
	}
	if r.topo == nil || r.topo.Locks[name] == nil {
		return nil
	}
	tr := &dispatchLockTracker{slots: r.topo.Locks[name].Slots, holders: map[string]*LockLease{}}
	r.locks[name] = tr
	return tr
}

func (r *EventResolver) updateLockLeasePID(instance string, pid int) {
	if strings.TrimSpace(instance) == "" || pid <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	for name, tr := range r.locks {
		if tr == nil || tr.holders == nil {
			continue
		}
		lease := tr.holders[instance]
		if lease == nil {
			continue
		}
		lease.PID = pid
		lease.UpdatedAt = now
		_ = WriteLockLease(r.mgr.daemonRoot, lease)
		r.locks[name].holders[instance] = lease
	}
}

// releaseLocksForInstanceLocked frees every lock slot held by instance and
// reports how many were released so callers can kick waiters: lock_held queue
// items may belong to OTHER declared instances, which the per-instance reap
// queue pop never retries (SQU-76).
func (r *EventResolver) releaseLocksForInstanceLocked(instance string) int {
	if strings.TrimSpace(instance) == "" {
		return 0
	}
	released := 0
	for name, tr := range r.locks {
		if tr == nil || tr.holders == nil {
			continue
		}
		if tr.holders[instance] == nil {
			continue
		}
		delete(tr.holders, instance)
		_ = RemoveLockLease(r.mgr.daemonRoot, name, instance)
		released++
	}
	return released
}

func (r *EventResolver) recoverLockStateLocked(now time.Time) {
	r.locks = map[string]*dispatchLockTracker{}
	if r.topo != nil {
		for _, lock := range r.topo.SortedLocks() {
			r.locks[lock.Name] = &dispatchLockTracker{slots: lock.Slots, holders: map[string]*LockLease{}}
		}
	}
	if r.mgr == nil {
		return
	}
	leases, err := ListLockLeases(r.mgr.daemonRoot)
	if err != nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, lease := range leases {
		tr := r.locks[lease.Lock]
		if tr == nil {
			_ = RemoveLockLease(r.mgr.daemonRoot, lease.Lock, lease.Instance)
			continue
		}
		live, recoveredPID := r.lockLeaseLivePID(lease)
		if !live {
			_ = RemoveLockLease(r.mgr.daemonRoot, lease.Lock, lease.Instance)
			continue
		}
		if recoveredPID > 0 && lease.PID != recoveredPID {
			lease.PID = recoveredPID
			lease.UpdatedAt = now
			_ = WriteLockLease(r.mgr.daemonRoot, lease)
		}
		tr.holders[lease.Instance] = lease
	}
}

func (r *EventResolver) lockLeaseLivePID(lease *LockLease) (bool, int) {
	if lease == nil {
		return false, 0
	}
	if lease.PID > 0 && PidLiveCheck(lease.PID) {
		return true, lease.PID
	}
	if r.mgr == nil {
		return false, 0
	}
	meta, err := ReadMetadata(r.mgr.daemonRoot, lease.Instance)
	if err != nil || meta == nil || meta.Status != StatusRunning || meta.PID <= 0 {
		return false, 0
	}
	if !PidLiveCheck(meta.PID) {
		return false, 0
	}
	return true, meta.PID
}

// RecoverLockState rebuilds in-memory dispatch lock holders from the durable
// ledger, dropping entries whose instances no longer have a live PID.
func (r *EventResolver) RecoverLockState() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recoverLockStateLocked(time.Now().UTC())
}

// LockSnapshots returns declared lock utilization from current resolver state.
func (r *EventResolver) LockSnapshots() []LockSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recoverLockStateLocked(time.Now().UTC())
	out := make([]LockSnapshot, 0, len(r.locks))
	names := make([]string, 0, len(r.locks))
	for name := range r.locks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tr := r.locks[name]
		if tr == nil {
			continue
		}
		holders := make([]LockHolder, 0, len(tr.holders))
		holderNames := make([]string, 0, len(tr.holders))
		for instance := range tr.holders {
			holderNames = append(holderNames, instance)
		}
		sort.Strings(holderNames)
		for _, instance := range holderNames {
			lease := tr.holders[instance]
			holders = append(holders, LockHolder{
				Instance:   lease.Instance,
				PID:        lease.PID,
				AcquiredAt: lease.AcquiredAt,
				UpdatedAt:  lease.UpdatedAt,
			})
		}
		available := tr.slots - len(holders)
		if available < 0 {
			available = 0
		}
		out = append(out, LockSnapshot{
			Name:      name,
			Slots:     tr.slots,
			Used:      len(holders),
			Available: available,
			Holders:   holders,
		})
	}
	return out
}

func validateRequestedChildName(declared, name string) error {
	if len(name) > 128 {
		return fmt.Errorf("requested instance name %q invalid: max length is 128", name)
	}
	if name == "." || name == ".." || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("requested instance name %q invalid: path segments are not allowed", name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("requested instance name %q invalid: only ASCII letters, digits, '.', '_' and '-' are allowed", name)
	}
	prefix := declared + "-"
	if !strings.HasPrefix(name, prefix) {
		return fmt.Errorf("requested instance name %q invalid: must start with %q", name, prefix)
	}
	return nil
}

// spawn issues a Dispatch for an ephemeral declared instance. The daemon still
// mirrors the CLI's full `--agents` / `--add-dir` launcher, plus the run path's
// per-instance runtime contract: state dir, resolved config, and AGENT_TEAM_* env vars.
// The caller's payload is JSON-encoded into the prompt so the spawned child has
// full event context to work from.
func (r *EventResolver) spawn(inst *topology.Instance, name, eventType string, payload map[string]any) (*Metadata, error) {
	runtime, err := r.prepareEphemeralRuntime(inst, name)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"event": eventType, "payload": payload})
	prompt := fmt.Sprintf("Topology event for declared instance %q (agent=%s):\n%s",
		inst.Name, inst.Agent, string(body))
	workspace := r.teamDirParent()
	worktreePath := ""
	branch := ""
	cleanupWorkspace := func() {}
	if payloadString(payload, "workspace") == "worktree" || payloadString(payload, "isolation") == "worktree" {
		workspace, branch, cleanupWorkspace, err = r.prepareEphemeralWorktree(name, payloadString(payload, "ticket"))
		if err != nil {
			return nil, err
		}
		worktreePath = workspace
	}
	prompt, err = r.appendUnreadMailboxToPrompt(name, inst.Agent, prompt, payload, branch)
	if err != nil {
		cleanupWorkspace()
		return nil, err
	}
	eventOrigin := r.originForEvent(inst, name, eventType, payload)
	env := append([]string(nil), runtime.env...)
	env = append(env, dispatchContextEnv(payload, branch, worktreePath)...)
	env = append(env, originContextEnv(eventOrigin)...)
	otelCtx := runtimeotel.Context{
		Agent:        inst.Agent,
		Instance:     name,
		JobID:        eventJobID(payload),
		Ticket:       payloadString(payload, "ticket"),
		Pipeline:     payloadString(payload, "pipeline"),
		PipelineStep: payloadString(payload, "pipeline_step"),
		Team:         r.teamForInstance(inst.Name, payload),
		Branch:       branch,
		Worktree:     worktreePath,
		Build:        buildinfo.Current(""),
	}
	traceparent := ""
	if r.otel != nil && payloadString(payload, "pipeline_step") != "" {
		traceparent, err = r.otel.traceparentForStep(eventJobID(payload), payloadString(payload, "pipeline_step"))
		if err != nil {
			cleanupWorkspace()
			return nil, fmt.Errorf("event runtime: otel trace context: %w", err)
		}
	}
	args, stdin, rt, env, err := r.prepareEphemeralAgentArgs(inst.Agent, name, runtime.stateDir, workspace, prompt, env, runtime.mailboxInjection, payload, runtime.otelConfig, otelCtx, traceparent)
	if err != nil {
		cleanupWorkspace()
		return nil, err
	}
	meta, err := r.mgr.Dispatch(DispatchInput{
		Agent:         inst.Agent,
		Name:          name,
		Job:           eventJobID(payload),
		Ticket:        payloadString(payload, "ticket"),
		Branch:        branch,
		PR:            firstPayloadString(payload, "pr_url", "pr"),
		Origin:        eventOrigin,
		Workspace:     workspace,
		Runtime:       string(rt.Kind),
		RuntimeBinary: rt.Binary,
		Args:          args,
		Env:           env,
		StripOTelEnv:  runtime.otelConfig.Configured(),
		Stdin:         stdin,
		Budget:        ephemeralRuntimeBudget(payload),
	})
	if err != nil {
		cleanupWorkspace()
		return nil, err
	}
	r.attachSpawnOwnership(meta, payload, branch, worktreePath)
	return meta, nil
}

func (r *EventResolver) appendUnreadMailboxToPrompt(instance, agent, prompt string, payload map[string]any, branch string) (string, error) {
	unread, err := ReadUnacked(r.mgr.daemonRoot, instance)
	if err != nil {
		return "", fmt.Errorf("event runtime: read kickoff mailbox: %w", err)
	}
	section, delivered, truncated, cursor := formatKickoffMailbox(unread, kickoffMailboxMaxBytes)
	if delivered == 0 {
		return prompt, nil
	}
	if err := WriteCursor(r.mgr.daemonRoot, instance, cursor); err != nil {
		return "", fmt.Errorf("event runtime: advance kickoff mailbox cursor: %w", err)
	}
	r.recordKickoffMailDelivered(instance, agent, payload, branch, delivered, len(section), truncated)
	return prompt + "\n\n" + section, nil
}

func formatKickoffMailbox(messages []*Message, maxBytes int) (section string, delivered int, truncated bool, cursor string) {
	if len(messages) == 0 || maxBytes <= 0 {
		return "", 0, false, ""
	}
	var full strings.Builder
	full.WriteString(kickoffMailboxHeading)
	full.WriteString("\n\n")
	for i, msg := range messages {
		if msg == nil {
			continue
		}
		delivered++
		cursor = msg.ID
		if delivered > 1 {
			full.WriteString("\n")
		}
		from := strings.TrimSpace(msg.From)
		if from == "" {
			from = "unknown"
		}
		at := ""
		if !msg.TS.IsZero() {
			at = msg.TS.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(&full, "%d. From: %s", delivered, from)
		if at != "" {
			fmt.Fprintf(&full, " at %s", at)
		}
		if strings.TrimSpace(msg.ID) != "" {
			fmt.Fprintf(&full, " (id: %s)", msg.ID)
		}
		full.WriteString("\n")
		full.WriteString(indentMailboxBody(msg.Body))
		if i < len(messages)-1 {
			full.WriteString("\n")
		}
	}
	if delivered == 0 {
		return "", 0, false, ""
	}
	text := full.String()
	if len(text) <= maxBytes {
		return text, delivered, false, cursor
	}
	note := fmt.Sprintf("\n\n[truncated: unread mailbox delivery capped at %d bytes; %d message(s) were marked delivered at dispatch]", maxBytes, delivered)
	limit := maxBytes - len(note)
	if limit < 0 {
		return truncateUTF8(note, maxBytes), delivered, true, cursor
	}
	return truncateUTF8(text, limit) + note, delivered, true, cursor
}

func indentMailboxBody(body string) string {
	body = strings.TrimRight(strings.ToValidUTF8(body, "\uFFFD"), "\n")
	if body == "" {
		return "   (empty)"
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = "   " + line
	}
	return strings.Join(lines, "\n")
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.ValidString(s[:maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func (r *EventResolver) recordKickoffMailDelivered(instance, agent string, payload map[string]any, branch string, delivered, bytes int, truncated bool) {
	if delivered <= 0 {
		return
	}
	jobID := eventJobID(payload)
	ticket := payloadString(payload, "ticket")
	message := fmt.Sprintf("delivered %d unread mailbox message(s) in dispatch kickoff", delivered)
	if truncated {
		message += " (truncated)"
	}
	_ = AppendLifecycleEvent(r.mgr.daemonRoot, &LifecycleEvent{
		Action:   "kickoff_mail_delivered",
		Instance: instance,
		Agent:    agent,
		Job:      jobID,
		Ticket:   ticket,
		Branch:   branch,
		Origin:   r.originForPayload(instance, payload),
		Message:  message,
	})
	if jobID == "" || strings.TrimSpace(r.teamDir) == "" {
		return
	}
	data := map[string]string{
		"messages": fmt.Sprint(delivered),
		"bytes":    fmt.Sprint(bytes),
	}
	if truncated {
		data["truncated"] = "true"
	}
	_ = jobstore.AppendEvent(r.teamDir, &jobstore.Event{
		JobID:    jobID,
		Type:     "kickoff_mail_delivered",
		Instance: instance,
		Message:  message,
		Actor:    "daemon",
		Origin:   r.originForPayload(instance, payload),
		Data:     data,
	})
}

func (r *EventResolver) attachSpawnOwnership(meta *Metadata, payload map[string]any, branch, worktreePath string) {
	if meta == nil {
		return
	}
	meta.Job = eventJobID(payload)
	meta.Ticket = payloadString(payload, "ticket")
	meta.Branch = branch
	meta.PR = firstPayloadString(payload, "pr_url", "pr")
	meta.Origin = origin.Merge(meta.Origin, r.originForPayload(meta.Instance, payload))
	j := r.upsertDispatchJob(payload, meta.Instance, jobstore.StatusRunning, "dispatched", "running", branch, worktreePath)
	if j != nil {
		meta.Job = j.ID
		meta.Ticket = j.Ticket
		meta.PR = j.PR
		if stepID, ok := linearDispatchStepFromPayload(payload); ok {
			r.writeLinearDispatchInProgress(j, stepID)
		}
	}
	if err := WriteMetadata(r.mgr.daemonRoot, meta); err != nil {
		return
	}
}

func (r *EventResolver) upsertDispatchJob(payload map[string]any, instance string, status jobstore.Status, lastEvent, lastStatus, branch, worktreePath string) *jobstore.Job {
	id := eventJobID(payload)
	ticket := payloadString(payload, "ticket")
	if id == "" && ticket == "" {
		return nil
	}
	if id == "" {
		id = jobstore.NormalizeID(ticket)
	}
	if ticket == "" {
		ticket = id
	}
	if id == "" || strings.TrimSpace(r.teamDir) == "" {
		return nil
	}
	now := time.Now().UTC()
	j, err := jobstore.Read(r.teamDir, id)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil
		}
		target := firstPayloadString(payload, "target", "agent")
		if target == "" {
			target = "worker"
		}
		j, err = jobstore.New(ticket, target, payloadString(payload, "kickoff"), now)
		if err != nil {
			return nil
		}
		j.ID = id
	}
	if target := firstPayloadString(payload, "target", "agent"); target != "" {
		j.Target = target
	}
	j.Origin = origin.Merge(j.Origin, r.originForPayload(instance, payload))
	if kickoff := payloadString(payload, "kickoff"); kickoff != "" && j.Kickoff == "" {
		j.Kickoff = kickoff
	}
	if instance != "" {
		j.Instance = instance
	}
	if ticket != "" {
		j.Ticket = ticket
	}
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		j.TicketURL = ticketURL
	}
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		j.Pipeline = pipeline
	}
	if policy := payloadString(payload, "reap_worktree"); policy != "" {
		if normalized, err := worktreepolicy.Normalize(policy); err == nil {
			j.ReapWorktree = normalized
		}
	}
	if branch != "" {
		j.Branch = branch
	}
	if worktreePath != "" {
		j.Worktree = worktreePath
	}
	if pr := firstPayloadString(payload, "pr_url", "pr"); pr != "" {
		j.PR = pr
	}
	if status != "" {
		j.Status = status
	}
	if lastEvent != "" {
		j.LastEvent = lastEvent
	}
	if lastStatus != "" {
		j.LastStatus = lastStatus
	}
	if status == jobstore.StatusRunning {
		if stepID := payloadString(payload, "pipeline_step"); stepID != "" {
			markPipelineStep(j, stepID, jobstore.StatusRunning, instance, lastStatus, now)
		}
	}
	j.UpdatedAt = now
	if err := r.writeJobWithAudit(j, "", "daemon", "", dispatchJobEventData(payload, branch, worktreePath)); err != nil {
		return nil
	}
	return j
}

func dispatchJobEventData(payload map[string]any, branch, worktreePath string) map[string]string {
	data := map[string]string{}
	for _, key := range []string{"target", "agent", "pipeline", "pipeline_step", "ticket", "ticket_url", "team", "runtime", "runtime_binary"} {
		if value := payloadString(payload, key); value != "" {
			data[key] = value
		}
	}
	if trigger := origin.TriggerFromEvent("", payload); trigger != "" {
		data["trigger"] = trigger
	}
	if branch != "" {
		data["branch"] = branch
	}
	if worktreePath != "" {
		data["worktree"] = worktreePath
	}
	if len(data) == 0 {
		return nil
	}
	return data
}

func eventJobID(payload map[string]any) string {
	for _, key := range []string{"job_id", "job", "ticket"} {
		if id := jobstore.NormalizeID(payloadString(payload, key)); id != "" {
			return id
		}
	}
	return ""
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := payloadString(payload, key); value != "" {
			return value
		}
	}
	return ""
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
	for _, team := range r.topo.SortedTeams() {
		for _, name := range team.Instances {
			if instance == name || strings.HasPrefix(instance, name+"-") {
				return team.Name
			}
		}
	}
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		if team := r.teamForPipeline(pipeline); team != "" {
			return team
		}
	}
	return ""
}

func (r *EventResolver) teamForPipeline(pipeline string) string {
	pipeline = strings.TrimSpace(pipeline)
	if pipeline == "" || r == nil || r.topo == nil {
		return ""
	}
	for _, team := range r.topo.SortedTeams() {
		for _, name := range team.Pipelines {
			if name == pipeline {
				return team.Name
			}
		}
	}
	return ""
}

func (r *EventResolver) originForEvent(inst *topology.Instance, instance, eventType string, payload map[string]any) origin.Envelope {
	declared := instance
	agent := firstPayloadString(payload, "target", "agent")
	if inst != nil {
		declared = inst.Name
		if agent == "" {
			agent = inst.Agent
		}
	}
	if agent == "" {
		agent = payloadString(payload, "agent")
	}
	return origin.Envelope{
		Project:  projectIDForTeamDir(r.teamDir),
		Team:     r.teamForOrigin(declared, payload),
		Instance: instance,
		Agent:    agent,
		Job:      eventJobID(payload),
		Trigger:  origin.TriggerFromEvent(eventType, payload),
		Build:    buildinfo.Current("").Display(),
	}
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
	}
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
	if env.Team != "" {
		out = append(out, "AGENT_TEAM_TEAM="+env.Team)
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

func dispatchContextEnv(payload map[string]any, branch, worktreePath string) []string {
	env := []string{}
	if id := eventJobID(payload); id != "" {
		env = append(env, "AGENT_TEAM_JOB_ID="+id)
	}
	if ticket := payloadString(payload, "ticket"); ticket != "" {
		env = append(env, "AGENT_TEAM_TICKET="+ticket)
	}
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		env = append(env, "AGENT_TEAM_TICKET_URL="+ticketURL)
	}
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		env = append(env, "AGENT_TEAM_PIPELINE="+pipeline)
	}
	if step := payloadString(payload, "pipeline_step"); step != "" {
		env = append(env, "AGENT_TEAM_PIPELINE_STEP="+step)
	}
	if pr := firstPayloadString(payload, "pr_url", "pr"); pr != "" {
		env = append(env, "AGENT_TEAM_PR="+pr)
	}
	if branch != "" {
		env = append(env, "AGENT_TEAM_BRANCH="+branch)
	}
	if worktreePath != "" {
		env = append(env, "AGENT_TEAM_WORKTREE="+worktreePath)
	}
	return env
}

type ephemeralRuntime struct {
	stateDir         string
	env              []string
	mailboxInjection bool
	otelConfig       runtimeotel.Config
}

func (r *EventResolver) prepareEphemeralRuntime(inst *topology.Instance, name string) (*ephemeralRuntime, error) {
	if strings.TrimSpace(r.teamDir) == "" {
		return nil, errors.New("event runtime: team dir is required")
	}
	stateDir := filepath.Join(r.teamDir, "state", name)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("event runtime: create state dir: %w", err)
	}
	repoConfig, err := teamtemplate.LoadTOMLFile(filepath.Join(r.teamDir, "config.toml"))
	if err != nil {
		return nil, fmt.Errorf("event runtime: repo config: %w", err)
	}
	declaredConfig := teamtemplate.Tree{}
	if inst != nil && inst.Config != nil {
		declaredConfig = teamtemplate.Tree(inst.Config)
	}
	resolved := teamtemplate.ResolveLayers(repoConfig, declaredConfig)
	body, err := teamtemplate.EncodeTOML(resolved)
	if err != nil {
		return nil, fmt.Errorf("event runtime: encode config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "config.toml"), body, 0o644); err != nil {
		return nil, fmt.Errorf("event runtime: write config: %w", err)
	}
	if err := r.rerenderTmplFiles(stateDir, resolved); err != nil {
		return nil, fmt.Errorf("event runtime: re-render .tmpl files: %w", err)
	}
	otelCfg, err := runtimeotel.FromTree(resolved)
	if err != nil {
		return nil, fmt.Errorf("event runtime: %w", err)
	}
	env := []string{
		"AGENT_TEAM_ROOT=" + r.teamDir,
		"AGENT_TEAM_INSTANCE=" + name,
		"AGENT_TEAM_STATE_DIR=" + stateDir,
		"AGENT_TEAM_DAEMON_SOCKET=" + SocketPath(r.teamDir),
	}
	if httpAddr, err := ReadHTTPAddr(r.teamDir); err == nil && strings.TrimSpace(httpAddr) != "" {
		env = append(env, "AGENT_TEAM_DAEMON_URL="+DaemonHTTPURL(httpAddr))
	}
	return &ephemeralRuntime{
		stateDir:         stateDir,
		env:              env,
		mailboxInjection: runtimehooks.MailboxInjectionEnabled(resolved),
		otelConfig:       otelCfg,
	}, nil
}

func (r *EventResolver) rerenderTmplFiles(stateDir string, resolved teamtemplate.Tree) error {
	renderRoot := filepath.Join(stateDir, "rendered")
	if err := os.RemoveAll(renderRoot); err != nil {
		return err
	}
	hasTmpl := false
	err := filepath.WalkDir(r.teamDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == filepath.Join(r.teamDir, "state") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, teamtemplate.TmplSuffix) {
			return nil
		}
		hasTmpl = true
		rel, err := filepath.Rel(r.teamDir, path)
		if err != nil {
			return err
		}
		dstRel := strings.TrimSuffix(rel, teamtemplate.TmplSuffix)
		dst := filepath.Join(renderRoot, dstRel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := teamtemplate.RenderBytes(rel, body, resolved)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh"+teamtemplate.TmplSuffix) {
			mode = 0o755
		}
		return os.WriteFile(dst, out, mode)
	})
	if err != nil {
		return err
	}
	if !hasTmpl {
		_ = os.RemoveAll(renderRoot)
	}
	return nil
}

func (r *EventResolver) prepareEphemeralAgentArgs(agentName, instance, stateDir, cwd, prompt string, env []string, mailboxInjection bool, payload map[string]any, otelCfg runtimeotel.Config, otelCtx runtimeotel.Context, traceparent string) ([]string, string, runtimebin.Runtime, []string, error) {
	agents, err := loader.LoadAllAgents(r.teamDir)
	if err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: load agents: %w", err)
	}
	var chosen *loader.Agent
	for _, agent := range agents {
		if agent.Name == agentName {
			chosen = agent
			break
		}
	}
	if chosen == nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: agent %q not found", agentName)
	}
	rt, err := r.runtimeForAgent(chosen, payload)
	if err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: %w", err)
	}
	if otelCfg.Configured() {
		env = runtimeotel.StripOwnedEnv(env)
	}
	skillPaths, err := loader.UnionSkills(agents)
	if err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: resolve skills: %w", err)
	}
	otelCtx.Runtime = string(rt.Kind)
	var otelLaunch runtimeotel.Launch
	if strings.TrimSpace(traceparent) != "" {
		otelLaunch, err = runtimeotel.BuildLaunchWithTraceparent(otelCfg, rt.Kind, otelCtx, traceparent)
	} else {
		otelLaunch, err = runtimeotel.BuildLaunch(otelCfg, rt.Kind, otelCtx)
	}
	if err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: %w", err)
	}
	env = append(env, otelLaunch.Env...)

	runtimeDir := filepath.Join(stateDir, "runtime")
	if err := os.RemoveAll(runtimeDir); err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: reset runtime dir: %w", err)
	}
	skillsRoot := filepath.Join(runtimeDir, ".claude", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: create skills root: %w", err)
	}
	for name, path := range skillPaths {
		if err := os.Symlink(path, filepath.Join(skillsRoot, name)); err != nil {
			return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: symlink skill %s: %w", name, err)
		}
	}
	var mailboxHook *runtimehooks.MailboxHook
	if mailboxInjection {
		hook, err := runtimehooks.PrepareMailboxHook(runtimeDir)
		if err != nil {
			return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: prepare mailbox hook: %w", err)
		}
		mailboxHook = hook
	}

	workspace := r.teamDirParent()
	stateRel, err := filepath.Rel(workspace, stateDir)
	if err != nil {
		stateRel = stateDir
	}
	kickoff := fmt.Sprintf(
		"You are the `%s` instance of the `%s` agent.\n"+
			"Your state dir is `%s` (absolute: `%s`).\n\n"+
			"--- agent prompt ---\n\n%s",
		instance, agentName, filepath.ToSlash(stateRel), stateDir, chosen.Prompt,
	)
	if brief, err := InstanceBriefLaunchText(r.teamDir, instance); err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: generate instance brief: %w", err)
	} else if brief != "" {
		kickoff = brief + "\n\n--- runtime kickoff ---\n\n" + kickoff
	}
	promptFile := filepath.Join(runtimeDir, "system_prompt.md")
	if err := os.WriteFile(promptFile, []byte(kickoff), 0o644); err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: write prompt file: %w", err)
	}
	agentsJSON, err := buildAgentsJSON(agents)
	if err != nil {
		return nil, "", runtimebin.Runtime{}, nil, err
	}
	switch rt.Kind {
	case runtimebin.KindClaude:
		args := []string{
			"--agents", agentsJSON,
			"--add-dir", runtimeDir,
			"--append-system-prompt-file", promptFile,
		}
		if mailboxHook != nil {
			settingsPath, err := runtimehooks.WriteClaudeSettings(runtimeDir, mailboxHook)
			if err != nil {
				return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: %w", err)
			}
			args = append(args, "--settings", settingsPath)
		}
		args = append(args, "-p", prompt)
		return args, "", rt, env, nil
	case runtimebin.KindCodex:
		lastMessagePath := filepath.Join(stateDir, runtimebin.CodexLastMessageFile)
		if err := os.Remove(lastMessagePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: remove stale Codex last message: %w", err)
		}
		// Run codex IN the dispatch workspace — the per-worker git worktree when
		// isolation was requested — so its file edits, branch, and commits stay
		// isolated. Falling back to teamDirParent (the repo root) would make a
		// worktree-isolated worker operate on the main checkout instead, which
		// breaks isolation and collides with other workers / the operator.
		codexCwd := strings.TrimSpace(cwd)
		if codexCwd == "" {
			codexCwd = r.teamDirParent()
		}
		args := []string{"exec"}
		if mailboxHook != nil {
			args = append(args, "--dangerously-bypass-hook-trust")
			args = append(args, runtimehooks.CodexConfigArgs(mailboxHook)...)
		}
		args = append(args, otelLaunch.CodexArgs...)
		args = append(args, runtimebin.CodexAutonomousExecArgs()...)
		args = append(args, runtimebin.CodexAgentTeamEnvConfigArgs(env)...)
		args = append(args,
			"-C", codexCwd,
			"--add-dir", runtimeDir,
			"--output-last-message", lastMessagePath,
			"-",
		)
		return args, codexEventPrompt(kickoff, prompt, agents), rt, env, nil
	default:
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("unsupported runtime %q", rt.Kind)
	}
}

// runtimeForAgent resolves an ephemeral instance's runtime with the same
// precedence as the dispatch path: explicit payload runtime > AGENT_TEAM_RUNTIME
// env override > the agent's frontmatter `runtime:`/`runtime_bin:` > repo
// [runtime] config > default.
func (r *EventResolver) runtimeForAgent(agent *loader.Agent, payload map[string]any) (runtimebin.Runtime, error) {
	kindRaw := firstPayloadString(payload, "runtime")
	binRaw := firstPayloadString(payload, "runtime_binary", "runtime_bin")
	if rt, ok, err := runtimebin.FromFields(kindRaw, binRaw); err != nil {
		return runtimebin.Runtime{}, fmt.Errorf("runtime must be %q or %q", runtimebin.KindClaude, runtimebin.KindCodex)
	} else if ok {
		return rt, nil
	}
	// A deliberate env override outranks a static per-agent default; when no
	// env override is set, the agent's declared runtime wins over repo config.
	if agent != nil && strings.TrimSpace(os.Getenv(runtimebin.EnvRuntime)) == "" {
		if rt, ok, err := runtimebin.FromFields(agent.Runtime, agent.RuntimeBin); err != nil {
			return runtimebin.Runtime{}, fmt.Errorf("agent %q runtime: %w", agent.Name, err)
		} else if ok {
			return rt, nil
		}
	}
	rt, err := runtimebin.CurrentFromConfig(filepath.Join(r.teamDir, "config.toml"))
	if err != nil {
		return runtimebin.Runtime{}, err
	}
	if bin := strings.TrimSpace(binRaw); bin != "" {
		rt.Binary = bin
	}
	if strings.TrimSpace(rt.Binary) == "" {
		rt.Binary = runtimebin.DefaultBinaryForKind(rt.Kind)
	}
	return rt, nil
}

func codexEventPrompt(kickoff, prompt string, agents []*loader.Agent) string {
	var b strings.Builder
	b.WriteString(kickoff)
	b.WriteString("\n\n--- agent-team runtime ---\n\n")
	b.WriteString("This session is running through the Codex adapter. The current agent prompt is included above. Other team agents are listed for coordination context, but this adapter does not register them as native subagents.\n")
	if len(agents) > 0 {
		b.WriteString("\nAvailable team agents:\n")
		for _, agent := range agents {
			b.WriteString("- ")
			b.WriteString(agent.Name)
			if agent.Description != "" {
				b.WriteString(": ")
				b.WriteString(agent.Description)
			}
			b.WriteByte('\n')
		}
	}
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("\n--- task ---\n\n")
		b.WriteString(prompt)
	}
	return b.String()
}

func (r *EventResolver) prepareEphemeralWorktree(instance, ticket string) (string, string, func(), error) {
	repoRoot := r.teamDirParent()
	if repoRoot == "" {
		return "", "", nil, errors.New("event worktree: repo root is required")
	}
	tag := newSessionID()[0:8]
	branch := ephemeralWorktreeBranch(instance, ticket, tag)
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", instance+"-"+tag)
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", "", nil, fmt.Errorf("event worktree: create parent: %w", err)
	}
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "-b", branch, worktreePath, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", nil, fmt.Errorf("event worktree: git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Keep the worker's private scratch dir (.worker_agent/) out of git for this
	// worktree, so a `git add -A` never sweeps it into the PR. A linked worktree
	// has its own git dir, so the main repo's .git/info/exclude does not apply
	// here — seed this worktree's own exclude. Best-effort: never fail the spawn.
	excludeWorktreeWorkerScratch(worktreePath)
	cleanup := func() {
		_ = exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", worktreePath).Run()
		_ = exec.Command("git", "-C", repoRoot, "branch", "-D", branch).Run()
	}
	return worktreePath, branch, cleanup, nil
}

func ephemeralWorktreeBranch(instance, ticket, tag string) string {
	if slug := jobstore.IDFromInput(ticket); slug != "" {
		return slug + "-" + tag
	}
	return "worktree-" + instance + "-" + tag
}

// excludeWorktreeWorkerScratch appends `.worker_agent/` to the worktree's own
// git exclude file so the per-worker scratch directory is never staged into a
// PR. Best-effort and idempotent; failures are intentionally ignored.
func excludeWorktreeWorkerScratch(worktreePath string) {
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return
	}
	gitDir := strings.TrimSpace(string(out))
	if gitDir == "" {
		return
	}
	excludePath := filepath.Join(gitDir, "info", "exclude")
	if existing, rerr := os.ReadFile(excludePath); rerr == nil && strings.Contains(string(existing), ".worker_agent/") {
		return
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(excludePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("\n# agent-team: keep per-worker scratch out of commits\n.worker_agent/\n")
}

func buildAgentsJSON(agents []*loader.Agent) (string, error) {
	type agentEntry struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	m := make(map[string]agentEntry, len(agents))
	for _, agent := range agents {
		m[agent.Name] = agentEntry{Description: agent.Description, Prompt: agent.Prompt}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("event runtime: encode agents JSON: %w", err)
	}
	return string(b), nil
}

func (r *EventResolver) teamDirParent() string {
	// teamDir ends in `.agent_team/`; the workspace for spawned children is
	// the repo root (one level up). When teamDir is empty (early bootstrap),
	// return "" — Dispatch will reject with a clearer error.
	if r.teamDir == "" {
		return ""
	}
	if filepath.Base(r.teamDir) == ".agent_team" {
		return filepath.Dir(r.teamDir)
	}
	return strings.TrimSuffix(r.teamDir, "/.agent_team")
}

// onReap is the hook installed on the InstanceManager. For each ephemeral
// declared instance whose spawn just completed, decrement the running count
// and drain a queued event if any.
func (r *EventResolver) onReap(spawned string) {
	declared, ok := r.declaredOwnerOf(spawned)
	if !ok {
		return
	}
	r.mu.Lock()
	tr, ok := r.tracking[declared.Name]
	if !ok {
		r.mu.Unlock()
		return
	}
	if tr.running > 0 {
		tr.running--
	}
	freedLocks := r.releaseLocksForInstanceLocked(spawned)
	var next *queuedEvent
	if len(tr.queue) > 0 {
		next = r.popReadyQueuedEventLocked(declared, tr, time.Now().UTC(), nil)
	}
	r.mu.Unlock()
	if meta, err := ReadMetadata(r.mgr.daemonRoot, spawned); err == nil {
		r.reconcileEphemeralJobExit(meta)
	}
	// Freed lock slots may unblock lock_held waiters queued under other
	// declared instances; the same-instance pop above cannot reach them, so
	// run the shared drain pass (the same path as `agent-team queue drain`).
	if freedLocks > 0 {
		defer r.DrainQueues()
	}
	if next == nil {
		return
	}
	// Re-spawn from the queue. Failures are persisted with retry metadata and
	// move to dead-letter after MaxQueueAttempts.
	if _, err := r.spawn(declared, next.uniqueName, next.eventType, next.payload); err != nil {
		r.recordQueueFailure(declared.Name, next, err)
		r.mu.Lock()
		r.releaseLocksForInstanceLocked(next.uniqueName)
		tr.running--
		r.mu.Unlock()
		return
	}
	if meta, err := ReadMetadata(r.mgr.daemonRoot, next.uniqueName); err == nil {
		r.updateLockLeasePID(meta.Instance, meta.PID)
	}
	_ = RemoveQueueItem(r.mgr.daemonRoot, next.id)
}

func (r *EventResolver) reconcileEphemeralJobExit(meta *Metadata) {
	if meta == nil || strings.TrimSpace(r.teamDir) == "" || strings.TrimSpace(meta.Job) == "" {
		return
	}
	switch meta.Status {
	case StatusExited, StatusCrashed:
	default:
		return
	}
	j, err := jobstore.Read(r.teamDir, meta.Job)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	status := jobstore.StatusDone
	eventType := "instance_exited"
	message := "instance exited successfully"
	if meta.Status == StatusCrashed || (meta.ExitCode != nil && *meta.ExitCode != 0) {
		status = jobstore.StatusFailed
		eventType = "instance_crashed"
		message = "instance crashed"
		if meta.ExitCode != nil {
			message = fmt.Sprintf("instance exited with code %d", *meta.ExitCode)
		}
	}
	if meta.Instance != "" {
		j.Instance = meta.Instance
	}
	if meta.Ticket != "" {
		j.Ticket = meta.Ticket
	}
	if meta.Branch != "" {
		j.Branch = meta.Branch
		if meta.Workspace != "" {
			j.Worktree = meta.Workspace
		}
	}
	if meta.PR != "" {
		j.PR = meta.PR
	}
	if reconcilePipelineStepExit(j, meta.Instance, status, now) {
		if status == jobstore.StatusDone && !allPipelineStepsDone(j) {
			j.Status = jobstore.StatusRunning
			message = "completed pipeline step"
		} else {
			j.Status = status
		}
	} else {
		j.Status = status
	}
	j.LastEvent = eventType
	j.LastStatus = message
	j.UpdatedAt = now
	data := map[string]string{"instance": meta.Instance}
	if meta.Branch != "" {
		data["branch"] = meta.Branch
	}
	if meta.PR != "" {
		data["pr"] = meta.PR
	}
	if meta.ExitCode != nil {
		data["exit_code"] = fmt.Sprint(*meta.ExitCode)
	}
	if err := r.writeJobWithAudit(j, eventType, "daemon", message, data); err != nil {
		return
	}

	// Opt-in: dispatch the next ready step without waiting for a manual
	// `agent-team pipeline tick`. Safe to call here — onReap has already released
	// r.mu, and this reuses the normal dispatch path (which does its own locking).
	r.tryAutoAdvancePipeline(j, meta, status)
	r.autoReapJob(meta.Job, worktreepolicy.OnClose)
}

func (r *EventResolver) autoReapJob(id, trigger string) {
	id = strings.TrimSpace(id)
	if id == "" || strings.TrimSpace(r.teamDir) == "" {
		return
	}
	j, err := jobstore.Read(r.teamDir, id)
	if err != nil || j == nil {
		return
	}
	if j.Status != jobstore.StatusDone && j.Status != jobstore.StatusFailed {
		return
	}
	policy := r.reapWorktreePolicyForJob(j)
	if !worktreepolicy.ShouldReap(policy, trigger) {
		return
	}
	if strings.TrimSpace(j.Worktree) == "" && strings.TrimSpace(j.Branch) == "" {
		return
	}
	summary, err := worktreecleanup.CleanupJobOwnedWorktree(r.teamDirParent(), j, worktreecleanup.Options{ForceBranch: true})
	if err != nil {
		_ = jobstore.AppendSnapshotEvent(r.teamDir, j, "cleanup_skipped", "daemon", err.Error(), map[string]string{
			"trigger":       trigger,
			"reap_worktree": policy,
		})
		return
	}
	j.Worktree = ""
	j.Branch = ""
	j.LastEvent = "cleanup"
	j.LastStatus = summary
	j.UpdatedAt = time.Now().UTC()
	_ = r.writeJobWithAudit(j, "cleanup", "daemon", summary, map[string]string{
		"trigger":       trigger,
		"reap_worktree": policy,
	})
}

func (r *EventResolver) reapWorktreePolicyForJob(j *jobstore.Job) string {
	if j == nil {
		return worktreepolicy.Never
	}
	if policy := strings.TrimSpace(j.ReapWorktree); policy != "" {
		if normalized, err := worktreepolicy.Normalize(policy); err == nil {
			return normalized
		}
	}
	r.mu.Lock()
	t := r.topo
	r.mu.Unlock()
	if t == nil {
		return worktreepolicy.Never
	}
	if pipeline := t.Pipelines[strings.TrimSpace(j.Pipeline)]; pipeline != nil {
		return pipeline.ReapWorktree
	}
	if inst := t.Find(strings.TrimSpace(j.Target)); inst != nil {
		return inst.ReapWorktree
	}
	return worktreepolicy.Never
}

// tryAutoAdvancePipeline dispatches the next ready step of a pipeline job once a
// step's instance exits, when the pipeline opted in via `auto_advance`. It mirrors
// `agent-team pipeline tick`, but driven by the reap hook.
//
// MUST be called WITHOUT r.mu held: it runs from onReap after the lock is
// released and reuses dispatchPipelineStep → actuate, which manages its own
// locking and running-count bookkeeping (so no manual tr.running changes here).
// Best-effort: any miss (feature off, gate pending, nothing ready, retries
// exhausted) simply leaves the job for manual advancement.
func (r *EventResolver) tryAutoAdvancePipeline(j *jobstore.Job, meta *Metadata, prevStatus jobstore.Status) {
	if j == nil || strings.TrimSpace(j.Pipeline) == "" {
		return
	}
	r.mu.Lock()
	t := r.topo
	r.mu.Unlock()
	if t == nil {
		return
	}
	pipeline := t.Pipelines[j.Pipeline]
	if pipeline == nil || !pipeline.AutoAdvance {
		return
	}

	// Locate the step that just exited (for retry + prior-output context).
	prevInstance := ""
	if meta != nil {
		prevInstance = meta.Instance
	}
	var prevStep *jobstore.Step
	for i := range j.Steps {
		if j.Steps[i].Instance == prevInstance {
			prevStep = &j.Steps[i]
			break
		}
	}

	// On failure: only retry opt-in crash-without-verdict steps. This deliberately
	// does not use max_attempts as a generic auto-retry policy: implementation
	// steps can fail after opening a PR, and retrying them risks duplicate PRs.
	if prevStatus == jobstore.StatusFailed {
		if !r.shouldAutoRetryCrashedStep(j, prevStep, meta) {
			return
		}
		topoStep := pipelineTopologyStep(pipeline, prevStep.ID)
		if topoStep == nil {
			return
		}
		prevStep.Instance = ""
		prevStep.QueueReason = ""
		prevStep.QueuedAt = time.Time{}
		prevStep.RunningAt = time.Time{}
		prevStep.StartedAt = time.Time{}
		prevStep.FinishedAt = time.Time{}
		r.dispatchAndRecord(pipeline, topoStep, j, nil, "auto-retried crashed step "+topoStep.ID)
		return
	}

	// On success: advance to the next ready step. Gates (manual / pr) are honored
	// by NextReadyStep, so auto-advance naturally parks at a manual approval gate.
	next := jobstore.NextReadyStep(j)
	if next == nil {
		return
	}
	topoStep := pipelineTopologyStep(pipeline, next.ID)
	if topoStep == nil {
		return
	}
	var payload map[string]any
	if out := r.readInstanceFinalMessage(prevInstance); out != "" {
		payload = map[string]any{"previous_step_output": out}
		if prevStep != nil {
			payload["previous_step_id"] = prevStep.ID
		}
	}
	r.dispatchAndRecord(pipeline, topoStep, j, payload, "auto-advanced to step "+topoStep.ID)
}

func (r *EventResolver) shouldAutoRetryCrashedStep(j *jobstore.Job, step *jobstore.Step, meta *Metadata) bool {
	if j == nil || step == nil || meta == nil {
		return false
	}
	if !step.RetryOnCrash {
		return false
	}
	if !metadataFinalizedAsCrash(meta) {
		return false
	}
	if effectiveStepAttempts(step) != 1 {
		return false
	}
	return !r.instanceHasRecordedOutputOrGate(j.ID, meta.Instance)
}

func metadataFinalizedAsCrash(meta *Metadata) bool {
	if meta == nil {
		return false
	}
	if meta.Status == StatusCrashed {
		return true
	}
	return meta.ExitCode != nil && *meta.ExitCode != 0
}

func effectiveStepAttempts(step *jobstore.Step) int {
	if step == nil {
		return 0
	}
	if step.Attempts > 0 {
		return step.Attempts
	}
	if !step.StartedAt.IsZero() || !step.FinishedAt.IsZero() || strings.TrimSpace(step.Instance) != "" {
		return 1
	}
	switch step.Status {
	case jobstore.StatusRunning, jobstore.StatusQueued, jobstore.StatusDone, jobstore.StatusFailed:
		return 1
	default:
		return 0
	}
}

func (r *EventResolver) jobHasGateFromInstance(jobID, instance string) bool {
	instance = strings.TrimSpace(instance)
	if strings.TrimSpace(jobID) == "" || instance == "" || strings.TrimSpace(r.teamDir) == "" {
		return false
	}
	records, err := jobstore.ListGateRecords(r.teamDir, jobID)
	if err != nil {
		return false
	}
	for _, record := range records {
		if strings.TrimSpace(record.Actor) == instance {
			return true
		}
	}
	return false
}

func (r *EventResolver) instanceHasRecordedOutputOrGate(jobID, instance string) bool {
	if r.jobHasGateFromInstance(jobID, instance) {
		return true
	}
	return strings.TrimSpace(r.readInstanceFinalMessage(instance)) != ""
}

// dispatchAndRecord dispatches one pipeline step, records the outcome on the job,
// persists it, and appends a snapshot event.
func (r *EventResolver) dispatchAndRecord(pipeline *topology.Pipeline, step *topology.PipelineStep, j *jobstore.Job, payload map[string]any, message string) {
	outcomes := r.dispatchPipelineStep(pipeline, step, j, payload)
	applyPipelineStepOutcome(j, step.ID, outcomes)
	j.UpdatedAt = time.Now().UTC()
	_ = r.writeJobWithAudit(j, "pipeline_advanced", "daemon", message, map[string]string{"step": step.ID})
}

func pipelineTopologyStep(pipeline *topology.Pipeline, id string) *topology.PipelineStep {
	if pipeline == nil {
		return nil
	}
	for _, step := range pipeline.Steps {
		if step.ID == id {
			return step
		}
	}
	return nil
}

// readInstanceFinalMessage returns the Codex `last-message.txt` sidecar for an
// exited instance, if present — the clean final response. Best-effort: empty on
// any miss (Claude steps have no sidecar; the file may be absent).
func (r *EventResolver) readInstanceFinalMessage(instance string) string {
	if strings.TrimSpace(instance) == "" || strings.TrimSpace(r.teamDir) == "" {
		return ""
	}
	path := filepath.Join(r.teamDir, "state", instance, runtimebin.CodexLastMessageFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func reconcilePipelineStepExit(j *jobstore.Job, instance string, status jobstore.Status, now time.Time) bool {
	if j == nil || instance == "" {
		return false
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Instance != instance {
			continue
		}
		if step.Status != jobstore.StatusRunning && step.Status != jobstore.StatusQueued {
			continue
		}
		step.Status = status
		if step.StartedAt.IsZero() {
			step.StartedAt = now
		}
		if step.QueuedAt.IsZero() {
			step.QueuedAt = step.StartedAt
		}
		if step.RunningAt.IsZero() {
			step.RunningAt = step.StartedAt
		}
		step.FinishedAt = now
		return true
	}
	return false
}

func allPipelineStepsDone(j *jobstore.Job) bool {
	if j == nil || len(j.Steps) == 0 {
		return false
	}
	for _, step := range j.Steps {
		if step.Status != jobstore.StatusDone {
			return false
		}
	}
	return true
}

func popReadyQueuedEvent(queue []*queuedEvent, now time.Time, ids map[string]bool) (*queuedEvent, []*queuedEvent) {
	for i, ev := range queue {
		if ev == nil {
			continue
		}
		if !queueDrainIDAllowed(ids, ev.id) {
			continue
		}
		if !ev.nextRetry.IsZero() && ev.nextRetry.After(now) {
			continue
		}
		out := append(queue[:i:i], queue[i+1:]...)
		return ev, out
	}
	return nil, queue
}

func (r *EventResolver) popReadyQueuedEventLocked(inst *topology.Instance, tr *ephTracker, now time.Time, ids map[string]bool) *queuedEvent {
	if inst == nil || tr == nil {
		return nil
	}
	for i, ev := range tr.queue {
		if ev == nil {
			continue
		}
		if !queueDrainIDAllowed(ids, ev.id) {
			continue
		}
		if !ev.nextRetry.IsZero() && ev.nextRetry.After(now) {
			continue
		}
		if len(ev.locks) == 0 {
			ev.locks = r.dispatchLocksLocked(inst, ev.payload)
		}
		acquired, err := r.acquireLocksLocked(ev.locks, ev.uniqueName, ev.origin, now)
		if err != nil {
			ev.lastError = err.Error()
			ev.reason = "lock_error"
			_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
			continue
		}
		if !acquired {
			ev.reason = QueueReasonLockHeld
			_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
			continue
		}
		tr.queue = append(tr.queue[:i:i], tr.queue[i+1:]...)
		tr.running++
		return ev
	}
	return nil
}

func (r *EventResolver) recordQueueFailure(declared string, ev *queuedEvent, spawnErr error) {
	if ev == nil {
		return
	}
	r.mu.Lock()
	r.releaseLocksForInstanceLocked(ev.uniqueName)
	r.mu.Unlock()
	ev.attempts++
	ev.lastError = spawnErr.Error()
	if ev.attempts >= MaxQueueAttempts {
		_ = MoveQueueItemToDead(r.mgr.daemonRoot, queueItemFromEvent(declared, ev, QueueStateDead))
		return
	}
	ev.nextRetry = time.Now().UTC().Add(time.Duration(ev.attempts) * time.Second)
	_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(declared, ev, QueueStatePending))
	r.mu.Lock()
	tr, ok := r.tracking[declared]
	if ok && !queuedEventID(tr.queue, ev.id) {
		tr.queue = append(tr.queue, ev)
	}
	r.mu.Unlock()
}

func (r *EventResolver) loadPersistedQueue() error {
	items, err := ListQueueItems(r.mgr.daemonRoot)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range items {
		if item.State != QueueStatePending {
			continue
		}
		if r.topo == nil || r.topo.Find(item.Instance) == nil {
			continue
		}
		tr, ok := r.tracking[item.Instance]
		if !ok {
			tr = &ephTracker{}
			r.tracking[item.Instance] = tr
		}
		if queuedEventID(tr.queue, item.ID) {
			continue
		}
		tr.queue = append(tr.queue, queuedEventFromItem(item))
	}
	return nil
}

// RecoverQueueState rebuilds ephemeral running counters from current daemon
// metadata and reloads persisted pending queue files. Daemon.Run calls this
// after process reconciliation so queued dispatches survive daemon restart.
func (r *EventResolver) RecoverQueueState() {
	r.mu.Lock()
	r.tracking = map[string]*ephTracker{}
	if r.topo != nil {
		for _, meta := range r.mgr.List() {
			inst := r.declaredOwnerOfLocked(meta.Instance)
			if inst == nil || !inst.Ephemeral || meta.Status != StatusRunning {
				continue
			}
			tr, ok := r.tracking[inst.Name]
			if !ok {
				tr = &ephTracker{}
				r.tracking[inst.Name] = tr
			}
			tr.running++
		}
	}
	r.recoverLockStateLocked(time.Now().UTC())
	r.mu.Unlock()
	_ = r.loadPersistedQueue()
	r.DrainQueues()
}

// DrainQueues attempts ready queued items while replica capacity is available.
func (r *EventResolver) DrainQueues() {
	_, _ = r.DrainQueuesWithResult()
}

// DrainQueuesWithResult attempts ready queued items while replica capacity is
// available and returns a summary suitable for operator-facing APIs.
func (r *EventResolver) DrainQueuesWithResult() (*QueueDrainResult, error) {
	return r.drainQueuesWithResult(nil)
}

// DrainQueuesWithResultForIDs attempts ready queued items whose persisted IDs
// are included in ids while replica capacity is available.
func (r *EventResolver) DrainQueuesWithResultForIDs(ids []string) (*QueueDrainResult, error) {
	return r.drainQueuesWithResult(stringAllowSet(ids))
}

func (r *EventResolver) drainQueuesWithResult(ids map[string]bool) (*QueueDrainResult, error) {
	if err := r.loadPersistedQueue(); err != nil {
		return nil, err
	}
	result := &QueueDrainResult{Outcomes: []EventOutcome{}}
	for {
		declared, ev := r.nextDrainableQueuedEvent(ids)
		if declared == nil || ev == nil {
			break
		}
		result.Attempted++
		meta, err := r.spawn(declared, ev.uniqueName, ev.eventType, ev.payload)
		if err != nil {
			r.recordQueueFailure(declared.Name, ev, err)
			r.mu.Lock()
			if tr := r.tracking[declared.Name]; tr != nil && tr.running > 0 {
				tr.running--
			}
			r.mu.Unlock()
			result.Rejected++
			result.Outcomes = append(result.Outcomes, EventOutcome{Instance: declared.Name, Action: "rejected", InstanceID: ev.uniqueName, Reason: err.Error()})
			continue
		}
		r.updateLockLeasePID(meta.Instance, meta.PID)
		_ = RemoveQueueItem(r.mgr.daemonRoot, ev.id)
		result.Dispatched++
		result.Outcomes = append(result.Outcomes, EventOutcome{Instance: declared.Name, Action: "dispatched", InstanceID: ev.uniqueName})
	}
	items, err := ListQueueItems(r.mgr.daemonRoot)
	if err != nil {
		return result, err
	}
	applyQueueDrainCounts(result, items, ids)
	return result, nil
}

// PreviewDrainQueuesWithResult reports which ready queue items would be
// dispatched by DrainQueuesWithResult without spawning processes or removing
// queue files.
func (r *EventResolver) PreviewDrainQueuesWithResult() (*QueueDrainResult, error) {
	return r.previewDrainQueuesWithResult(nil)
}

// PreviewDrainQueuesWithResultForIDs reports which selected ready queue items
// would be dispatched without spawning processes or removing queue files.
func (r *EventResolver) PreviewDrainQueuesWithResultForIDs(ids []string) (*QueueDrainResult, error) {
	return r.previewDrainQueuesWithResult(stringAllowSet(ids))
}

func (r *EventResolver) previewDrainQueuesWithResult(ids map[string]bool) (*QueueDrainResult, error) {
	if err := r.loadPersistedQueue(); err != nil {
		return nil, err
	}
	result := &QueueDrainResult{DryRun: true, Outcomes: []EventOutcome{}}
	r.mu.Lock()
	r.recoverLockStateLocked(time.Now().UTC())
	if r.topo != nil {
		now := time.Now().UTC()
		lockUsage, lockSlots := r.previewLockCountsLocked()
		for _, inst := range r.topo.SortedInstances() {
			if !inst.Ephemeral {
				continue
			}
			tr := r.tracking[inst.Name]
			running := 0
			var queue []*queuedEvent
			if tr != nil {
				running = tr.running
				queue = tr.queue
			}
			capacity := inst.Replicas - running
			for _, ev := range queue {
				if capacity <= 0 {
					break
				}
				if ev == nil {
					continue
				}
				if !queueDrainIDAllowed(ids, ev.id) {
					continue
				}
				if !ev.nextRetry.IsZero() && ev.nextRetry.After(now) {
					continue
				}
				locks := ev.locks
				if len(locks) == 0 {
					locks = r.dispatchLocksLocked(inst, ev.payload)
				}
				if !previewLocksAvailable(locks, ev.uniqueName, lockUsage, lockSlots) {
					continue
				}
				previewReserveLocks(locks, ev.uniqueName, lockUsage)
				result.WouldDispatch++
				result.Outcomes = append(result.Outcomes, EventOutcome{Instance: inst.Name, Action: "would_dispatch", InstanceID: ev.uniqueName})
				capacity--
			}
		}
	}
	r.mu.Unlock()
	items, err := ListQueueItems(r.mgr.daemonRoot)
	if err != nil {
		return result, err
	}
	applyQueueDrainCounts(result, items, ids)
	return result, nil
}

func applyQueueDrainCounts(result *QueueDrainResult, items []*QueueItem, ids map[string]bool) {
	if result == nil {
		return
	}
	for _, item := range items {
		if item == nil || !queueDrainIDAllowed(ids, item.ID) {
			continue
		}
		switch item.State {
		case QueueStatePending:
			result.Pending++
		case QueueStateDead:
			result.Dead++
		}
	}
}

func queueDrainIDAllowed(ids map[string]bool, id string) bool {
	return ids == nil || ids[id]
}

func (r *EventResolver) nextDrainableQueuedEvent(ids map[string]bool) (*topology.Instance, *queuedEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.topo == nil {
		return nil, nil
	}
	now := time.Now().UTC()
	for _, inst := range r.topo.SortedInstances() {
		if !inst.Ephemeral {
			continue
		}
		tr, ok := r.tracking[inst.Name]
		if !ok || tr.running >= inst.Replicas {
			continue
		}
		ev := r.popReadyQueuedEventLocked(inst, tr, now, ids)
		if ev == nil {
			continue
		}
		return inst, ev
	}
	return nil, nil
}

func (r *EventResolver) previewLockCountsLocked() (map[string]map[string]bool, map[string]int) {
	usage := map[string]map[string]bool{}
	slots := map[string]int{}
	for name, tr := range r.locks {
		if tr == nil {
			continue
		}
		slots[name] = tr.slots
		usage[name] = map[string]bool{}
		for instance := range tr.holders {
			usage[name][instance] = true
		}
	}
	return usage, slots
}

func previewLocksAvailable(locks []string, instance string, usage map[string]map[string]bool, slots map[string]int) bool {
	for _, name := range locks {
		holders := usage[name]
		if holders == nil {
			return false
		}
		if holders[instance] {
			continue
		}
		if len(holders) >= slots[name] {
			return false
		}
	}
	return true
}

func previewReserveLocks(locks []string, instance string, usage map[string]map[string]bool) {
	for _, name := range locks {
		if usage[name] == nil {
			usage[name] = map[string]bool{}
		}
		usage[name][instance] = true
	}
}

// DropQueueItem removes a queued item from memory and disk.
func (r *EventResolver) DropQueueItem(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("queue: id is required")
	}
	r.mu.Lock()
	for _, tr := range r.tracking {
		filtered := tr.queue[:0]
		for _, ev := range tr.queue {
			if ev == nil || ev.id == id {
				continue
			}
			filtered = append(filtered, ev)
		}
		tr.queue = filtered
	}
	r.mu.Unlock()
	return RemoveQueueItem(r.mgr.daemonRoot, id)
}

// RetryQueueItem retries a pending or dead-letter queue item immediately.
func (r *EventResolver) RetryQueueItem(id string) (EventOutcome, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return EventOutcome{}, errors.New("queue: id is required")
	}
	item, err := ReadQueueItem(r.mgr.daemonRoot, id)
	if err != nil {
		return EventOutcome{}, err
	}
	if err := ResetQueueItemForRetry(r.mgr.daemonRoot, item); err != nil {
		return EventOutcome{}, err
	}
	ev := queuedEventFromItem(item)
	r.mu.Lock()
	inst := (*topology.Instance)(nil)
	if r.topo != nil {
		inst = r.topo.Find(item.Instance)
	}
	if inst == nil || !inst.Ephemeral {
		r.mu.Unlock()
		return EventOutcome{}, fmt.Errorf("queue: instance %q is not a declared ephemeral instance", item.Instance)
	}
	tr, ok := r.tracking[inst.Name]
	if !ok {
		tr = &ephTracker{}
		r.tracking[inst.Name] = tr
	}
	tr.queue = removeQueuedEventByID(tr.queue, id)
	if tr.running >= inst.Replicas {
		ev.reason = QueueReasonReplicaCapacity
		tr.queue = append(tr.queue, ev)
		_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: ev.uniqueName, Reason: ev.reason}, nil
	}
	if len(ev.locks) == 0 {
		ev.locks = r.dispatchLocksLocked(inst, ev.payload)
	}
	acquired, err := r.acquireLocksLocked(ev.locks, ev.uniqueName, ev.origin, time.Now().UTC())
	if err != nil {
		r.mu.Unlock()
		return EventOutcome{}, err
	}
	if !acquired {
		ev.reason = QueueReasonLockHeld
		tr.queue = append(tr.queue, ev)
		_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: ev.uniqueName, Reason: QueueReasonLockHeld}, nil
	}
	tr.running++
	r.mu.Unlock()

	meta, err := r.spawn(inst, ev.uniqueName, ev.eventType, ev.payload)
	if err != nil {
		r.recordQueueFailure(inst.Name, ev, err)
		r.mu.Lock()
		if tr.running > 0 {
			tr.running--
		}
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: ev.uniqueName, Reason: err.Error()}, nil
	}
	r.updateLockLeasePID(meta.Instance, meta.PID)
	_ = RemoveQueueItem(r.mgr.daemonRoot, ev.id)
	return EventOutcome{Instance: inst.Name, Action: "dispatched", InstanceID: ev.uniqueName}, nil
}

func removeQueuedEventByID(queue []*queuedEvent, id string) []*queuedEvent {
	filtered := queue[:0]
	for _, ev := range queue {
		if ev == nil || ev.id == id {
			continue
		}
		filtered = append(filtered, ev)
	}
	return filtered
}

func queueItemFromEvent(declared string, ev *queuedEvent, state string) *QueueItem {
	now := time.Now().UTC()
	updated := now
	if ev.queuedAt.IsZero() {
		ev.queuedAt = now
	}
	return &QueueItem{
		ID:         ev.id,
		State:      state,
		EventType:  ev.eventType,
		Instance:   declared,
		InstanceID: ev.uniqueName,
		Reason:     ev.reason,
		Locks:      append([]string(nil), ev.locks...),
		Payload:    ev.payload,
		Origin:     ev.origin,
		Attempts:   ev.attempts,
		LastError:  ev.lastError,
		NextRetry:  ev.nextRetry,
		QueuedAt:   ev.queuedAt,
		UpdatedAt:  updated,
	}
}

func queuedEventFromItem(item *QueueItem) *queuedEvent {
	return &queuedEvent{
		id:         item.ID,
		eventType:  item.EventType,
		payload:    item.Payload,
		queuedAt:   item.QueuedAt,
		uniqueName: item.InstanceID,
		reason:     item.Reason,
		locks:      append([]string(nil), item.Locks...),
		origin:     item.Origin,
		attempts:   item.Attempts,
		lastError:  item.LastError,
		nextRetry:  item.NextRetry,
	}
}

// declaredOwnerOf identifies which declared ephemeral instance a unique-named
// spawn belongs to. Names produced by uniqueChildName have the shape
// `<declared>-<short-hex>`; we reverse the prefix lookup against the current
// topology.
func (r *EventResolver) declaredOwnerOf(spawned string) (*topology.Instance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst := r.declaredOwnerOfLocked(spawned)
	return inst, inst != nil
}

func (r *EventResolver) declaredOwnerOfLocked(spawned string) *topology.Instance {
	if r.topo == nil {
		return nil
	}
	for _, inst := range r.topo.Instances {
		if !inst.Ephemeral {
			continue
		}
		if spawned == inst.Name || strings.HasPrefix(spawned, inst.Name+"-") {
			return inst
		}
	}
	return nil
}

// uniqueChildName builds a per-spawn name from the declared name plus a short
// random hex tag. We avoid name collisions across concurrent spawns of the
// same declared ephemeral instance.
func uniqueChildName(declared string) string {
	tag := newSessionID()
	// First 8 hex chars are sufficient — collision on a per-repo daemon's
	// running set is vanishingly unlikely.
	return declared + "-" + tag[0:8]
}

// SetQueueCap overrides the per-declared-instance queue capacity. Useful for
// tests; production uses DefaultQueueCap.
func (r *EventResolver) SetQueueCap(cap int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cap < 0 {
		cap = 0
	}
	r.queueCap = cap
}

// QueueDepth reports the current queued+running counts for a declared
// instance. Exposed for tests and `/v1/topology` enrichment.
func (r *EventResolver) QueueDepth(name string) (running, queued int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tr, ok := r.tracking[name]
	if !ok {
		return 0, 0
	}
	return tr.running, len(tr.queue)
}
