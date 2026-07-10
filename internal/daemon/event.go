package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/agent-team-project/agent-team/internal/budget"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/jobwrite"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/worktreepolicy"
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
	kickoffMailboxHeading   = "## Unread messages (delivered at dispatch)"
	kickoffMailboxMaxBytes  = 8 * 1024
	budgetDrainPollInterval = time.Second
)

const (
	deliveryContractBranch     = "branch"
	deliveryContractPR         = "pr"
	deliveryContractReport     = "report"
	deliveryContractTicketToPR = "ticket_to_pr"
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

	mu          sync.Mutex
	topo        *topology.Topology
	tracking    map[string]*ephTracker // declared-name → tracker
	locks       map[string]*dispatchLockTracker
	concurrency *concurrencyController
}

type ephTracker struct {
	running     int
	runningLoad map[string]float64
	queue       []*queuedEvent
}

func (tr *ephTracker) trackRunningLoad(instance string, load float64) {
	if tr == nil || strings.TrimSpace(instance) == "" {
		return
	}
	if tr.runningLoad == nil {
		tr.runningLoad = map[string]float64{}
	}
	tr.runningLoad[instance] = normalizeConcurrencyLoad(load)
}

func (tr *ephTracker) releaseRunningLoad(instance string) {
	if tr == nil || tr.runningLoad == nil || strings.TrimSpace(instance) == "" {
		return
	}
	delete(tr.runningLoad, instance)
	if len(tr.runningLoad) == 0 {
		tr.runningLoad = nil
	}
}

func (tr *ephTracker) runningLoadUnits() float64 {
	if tr == nil || tr.running <= 0 {
		return 0
	}
	load := 0.0
	tracked := 0
	for _, weight := range tr.runningLoad {
		load += normalizeConcurrencyLoad(weight)
		tracked++
	}
	if missing := tr.running - tracked; missing > 0 {
		load += float64(missing)
	}
	return load
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
	name    string
	storage string
	scope   string
	team    string
	job     string
	slots   int
	holders map[string]*LockLease // instance name → lease
}

// NewEventResolver installs a reap hook on mgr and returns a resolver bound
// to it. teamDir is the consumer's `.agent_team/` (used to resolve the
// workspace for spawned instances).
func NewEventResolver(mgr *InstanceManager, teamDir string, topo *topology.Topology) *EventResolver {
	r := &EventResolver{
		mgr:         mgr,
		teamDir:     teamDir,
		queueCap:    DefaultQueueCap,
		topo:        topo,
		tracking:    map[string]*ephTracker{},
		locks:       map[string]*dispatchLockTracker{},
		concurrency: newConcurrencyController(concurrencyConfigForTopology(topo)),
		otel:        loadOrchestrationTracer(teamDir, mgr.daemonRoot),
	}
	mgr.SetReapHook(r.onReap)
	mgr.SetTerminalHook(r.onTerminalMetadata)
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
	r.setConcurrencyConfigLocked(t)
	r.recoverLockStateLocked(time.Now().UTC())
}

func concurrencyConfigForTopology(t *topology.Topology) *topology.Concurrency {
	if t == nil {
		return nil
	}
	return t.Concurrency
}

func (r *EventResolver) setConcurrencyConfigLocked(t *topology.Topology) {
	raw := concurrencyConfigForTopology(t)
	if r.concurrency == nil {
		r.concurrency = newConcurrencyController(raw)
		return
	}
	if !r.concurrency.updateConfig(raw) {
		r.concurrency = nil
	}
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

func (r *EventResolver) concurrencyAdmissionForLoadLocked(now time.Time, incomingLoad float64) concurrencyAdmission {
	if r.concurrency == nil {
		running := r.runningEphemeralLocked()
		return concurrencyAdmission{Allowed: true, Running: running, RunningLoad: r.runningEphemeralLoadLocked(), IncomingLoad: normalizeConcurrencyLoad(incomingLoad)}
	}
	running := r.runningEphemeralLocked()
	admission, ev := r.concurrency.admit(now, running, r.runningEphemeralLoadLocked(), incomingLoad)
	r.appendConcurrencyLifecycleEventLocked(ev)
	return admission
}

func (r *EventResolver) concurrencyAdmissionPreviewLocked(now time.Time, running int, runningLoad, incomingLoad float64) concurrencyAdmission {
	if r.concurrency == nil {
		return concurrencyAdmission{Allowed: true, Running: running, RunningLoad: normalizeConcurrencyRunningLoad(running, runningLoad), IncomingLoad: normalizeConcurrencyLoad(incomingLoad)}
	}
	return r.concurrency.preview(now, running, runningLoad, incomingLoad)
}

func (r *EventResolver) concurrencyConfiguredLocked() bool {
	return r.concurrency != nil
}

func (r *EventResolver) runningEphemeralLocked() int {
	running := 0
	for _, tr := range r.tracking {
		if tr == nil {
			continue
		}
		running += tr.running
	}
	return running
}

func (r *EventResolver) runningEphemeralLoadLocked() float64 {
	load := 0.0
	for _, tr := range r.tracking {
		load += tr.runningLoadUnits()
	}
	return load
}

func (r *EventResolver) observeConcurrencyTerminal(meta *Metadata) {
	if meta == nil || meta.Status != StatusCrashed {
		return
	}
	r.mu.Lock()
	if r.concurrency == nil {
		r.mu.Unlock()
		return
	}
	ev := r.concurrency.observeCrash(time.Now().UTC(), r.runningEphemeralLocked(), r.runningEphemeralLoadLocked())
	r.mu.Unlock()
	r.appendConcurrencyLifecycleEvent(ev)
}

func (r *EventResolver) appendConcurrencyLifecycleEventLocked(ev *LifecycleEvent) {
	if ev == nil || r == nil || r.mgr == nil {
		return
	}
	_ = AppendLifecycleEvent(r.mgr.daemonRoot, ev)
}

func (r *EventResolver) appendConcurrencyLifecycleEvent(ev *LifecycleEvent) {
	if ev == nil || r == nil || r.mgr == nil {
		return
	}
	_ = AppendLifecycleEvent(r.mgr.daemonRoot, ev)
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
	if r.mgr.isRunning(inst.Name) || !persistentEventShouldWake(eventType) {
		return EventOutcome{Instance: inst.Name, Action: "messaged", InstanceID: inst.Name}
	}
	meta, err := r.wakePersistent(inst, eventType, payload)
	if err != nil {
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: inst.Name, Reason: err.Error()}
	}
	return EventOutcome{Instance: inst.Name, Action: "dispatched", InstanceID: meta.Instance}
}

func persistentEventShouldWake(eventType string) bool {
	switch eventType {
	case topology.EventAgentDispatch, topology.EventJobCompleted, topology.EventJobStepCompleted, topology.EventDeliverableReady:
		return true
	default:
		return false
	}
}

func (r *EventResolver) wakePersistent(inst *topology.Instance, eventType string, payload map[string]any) (*Metadata, error) {
	prompt := persistentWakePrompt(eventType, payload)
	meta, err := r.mgr.StartWithOptions(inst.Name, StartOptions{
		AllowFreshWithoutSession: true,
		ResumePrompt:             prompt,
	})
	if err == nil {
		return meta, nil
	}
	if eventType == topology.EventAgentDispatch {
		return nil, err
	}
	r.mu.Lock()
	t := r.topo
	r.mu.Unlock()
	if t == nil || strings.TrimSpace(r.teamDir) == "" {
		return nil, err
	}
	meta, launched, fallbackErr := launchDeclaredFreshWithPrompt(r.teamDir, r.mgr, t, inst, nil, prompt)
	if fallbackErr != nil {
		return nil, fmt.Errorf("%w; fresh launch fallback failed: %v", err, fallbackErr)
	}
	if !launched || meta == nil {
		return nil, fmt.Errorf("%w; fresh launch fallback did not start %q", err, inst.Name)
	}
	return meta, nil
}

func persistentWakePrompt(eventType string, payload map[string]any) string {
	body, _ := json.Marshal(map[string]any{"event": eventType, "payload": payload})
	return "Re-invoked by agent-teamd after a topology event matched this persistent instance. Run `inbox check`, acknowledge handled messages, then continue autonomous management.\n\nMatched event:\n" + string(body)
}

func (r *EventResolver) actuateEphemeral(inst *topology.Instance, eventType string, payload map[string]any) EventOutcome {
	return r.actuateEphemeralWithBudgetOrigin(inst, eventType, payload, origin.Envelope{})
}

func (r *EventResolver) actuateEphemeralWithBudgetOrigin(inst *topology.Instance, eventType string, payload map[string]any, budgetOrigin origin.Envelope) EventOutcome {
	payload = copyPayload(payloadWithInstanceReapPolicy(inst, payload))
	applyInstanceBudgetDefaultsToPayload(inst, payload)
	r.mu.Lock()
	top := r.topo
	r.mu.Unlock()
	applyTopologyReminderDefaultsToPayload(top, payload)
	childName, requested, err := childNameForEvent(inst.Name, payload)
	if err != nil {
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	eventOrigin := r.originForEvent(inst, childName, eventType, payload)
	if !budgetOrigin.Empty() {
		eventOrigin = origin.Merge(budgetOrigin, eventOrigin)
	}
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
	admission, err := r.budgetAdmissionLocked(eventOrigin.Team, payload, now)
	if err != nil {
		r.mu.Unlock()
		r.upsertDispatchJob(payload, childName, jobstore.StatusFailed, "budget_error", err.Error(), "", "")
		return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: err.Error()}
	}
	if !admission.Allowed {
		ev := &queuedEvent{
			id:         newSessionID(),
			eventType:  eventType,
			payload:    payload,
			queuedAt:   now,
			uniqueName: childName,
			reason:     QueueReasonBudgetExhausted,
			origin:     eventOrigin,
			nextRetry:  admission.NextTokenRetry,
		}
		if err := WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending)); err != nil {
			r.mu.Unlock()
			return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: err.Error()}
		}
		tr.queue = append(tr.queue, ev)
		r.mu.Unlock()
		r.upsertDispatchJob(payload, childName, jobstore.StatusQueued, QueueReasonBudgetExhausted, QueueReasonBudgetExhausted, "", "")
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: childName, Reason: QueueReasonBudgetExhausted}
	}
	loadWeight := r.dispatchLoadWeightLocked(eventOrigin.Team)
	concurrencyAdmission := r.concurrencyAdmissionForLoadLocked(now, loadWeight)
	if !concurrencyAdmission.Allowed {
		if len(tr.queue) >= r.queueCap {
			r.mu.Unlock()
			reason := fmt.Sprintf("concurrency ceiling reached and queue is full (%d)", r.queueCap)
			r.upsertDispatchJob(payload, childName, jobstore.StatusFailed, "queue_full", reason, "", "")
			return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: reason}
		}
		ev := &queuedEvent{
			id:         newSessionID(),
			eventType:  eventType,
			payload:    payload,
			queuedAt:   now,
			uniqueName: childName,
			reason:     QueueReasonConcurrencyCeiling,
			locks:      r.dispatchLocksLocked(inst, payload),
			origin:     eventOrigin,
		}
		if err := WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending)); err != nil {
			r.mu.Unlock()
			return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: err.Error()}
		}
		tr.queue = append(tr.queue, ev)
		r.mu.Unlock()
		reason := concurrencyQueueMessage(concurrencyAdmission)
		r.upsertDispatchJob(payload, childName, jobstore.StatusQueued, QueueReasonConcurrencyCeiling, reason, "", "")
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: childName, Reason: QueueReasonConcurrencyCeiling}
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
	grant, err := r.grantPayloadBudgetLocked(payload, eventOrigin, childName, now)
	if err != nil {
		r.releaseLocksForInstanceLocked(childName)
		r.mu.Unlock()
		r.upsertDispatchJob(payload, childName, jobstore.StatusFailed, "budget_error", err.Error(), "", "")
		return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: err.Error()}
	}
	if !grant.Allowed {
		r.releaseLocksForInstanceLocked(childName)
		if len(tr.queue) >= r.queueCap {
			r.mu.Unlock()
			reason := fmt.Sprintf("budget exhausted and queue is full (%d)", r.queueCap)
			r.upsertDispatchJob(payload, childName, jobstore.StatusFailed, "queue_full", reason, "", "")
			return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: reason}
		}
		ev := &queuedEvent{
			id:         newSessionID(),
			eventType:  eventType,
			payload:    payload,
			queuedAt:   now,
			uniqueName: childName,
			reason:     QueueReasonBudgetExhausted,
			origin:     eventOrigin,
			nextRetry:  grant.NextTokenRetry,
		}
		if err := WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending)); err != nil {
			r.mu.Unlock()
			return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: childName, Reason: err.Error()}
		}
		tr.queue = append(tr.queue, ev)
		r.mu.Unlock()
		r.upsertDispatchJob(payload, childName, jobstore.StatusQueued, QueueReasonBudgetExhausted, QueueReasonBudgetExhausted, "", "")
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: childName, Reason: QueueReasonBudgetExhausted}
	}
	tr.running++
	tr.trackRunningLoad(childName, loadWeight)
	r.mu.Unlock()

	meta, err := r.spawn(inst, childName, eventType, payload)
	if err != nil {
		// Spawn failed; release capacity and don't drain queue (no work freed).
		r.mu.Lock()
		r.releaseLocksForInstanceLocked(childName)
		tr.running--
		tr.releaseRunningLoad(childName)
		r.mu.Unlock()
		_, _ = budget.ReleaseAllocations(r.teamDir, budget.ReleaseRequest{JobID: eventJobID(payload), Instance: childName, Now: time.Now().UTC()})
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

func concurrencyQueueMessage(admission concurrencyAdmission) string {
	reason := strings.TrimSpace(admission.Reason)
	if reason == "" {
		reason = "adaptive admission"
	}
	ceiling := formatConcurrencyLoad(admission.CeilingLoad)
	if admission.CeilingLoad == 0 && admission.Ceiling > 0 {
		ceiling = fmt.Sprint(admission.Ceiling)
	}
	loadDetail := ""
	if formatConcurrencyLoad(admission.RunningLoad) != fmt.Sprint(admission.Running) || formatConcurrencyLoad(admission.IncomingLoad) != "1" {
		loadDetail = fmt.Sprintf("; load=%s incoming=%s", formatConcurrencyLoad(admission.RunningLoad), formatConcurrencyLoad(admission.IncomingLoad))
	}
	return fmt.Sprintf("concurrency ceiling %s reached (%s; running=%d%s)", ceiling, reason, admission.Running, loadDetail)
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

func (r *EventResolver) missingDeliveryArtifactReason(j *jobstore.Job, meta *Metadata) string {
	if !jobRequiresDeliveryArtifact(j) {
		return ""
	}
	if r.jobHasDeliveryArtifact(j, meta) {
		return ""
	}
	contract := deliveryArtifactContract(j)
	if path := deliveryReportArtifactPath(contract); path != "" {
		return "delivery artifact missing: expected non-empty report artifact at " + path + " before accepting done"
	}
	if contract == deliveryContractReport {
		return "delivery artifact missing: expected report:<path> deliverable contract before accepting done"
	}
	return "delivery artifact missing: expected an open PR, pushed branch, or non-empty committed diff before accepting done"
}

func MissingDeliveryArtifactReason(teamDir string, j *jobstore.Job, meta *Metadata) string {
	r := &EventResolver{teamDir: teamDir}
	return r.missingDeliveryArtifactReason(j, meta)
}

func DeliveryArtifactContract(j *jobstore.Job) string {
	return deliveryArtifactContract(j)
}

func jobRequiresDeliveryArtifact(j *jobstore.Job) bool {
	if j == nil || jobIsProbe(j) {
		return false
	}
	if deliveryArtifactContract(j) != "" {
		return true
	}
	return false
}

func deliveryArtifactContract(j *jobstore.Job) string {
	if j == nil {
		return ""
	}
	if jobIsProbe(j) {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(j.DeliveryContract), "none") {
		return ""
	}
	if contract := normalizeDeliveryArtifactContract(j.DeliveryContract); contract != "" {
		return contract
	}
	if jobstore.IsReport(j.Kind) {
		return deliveryContractReport
	}
	if pipelineNameIsTicketToPR(j.Pipeline) {
		return deliveryContractTicketToPR
	}
	for _, step := range j.Steps {
		if strings.EqualFold(strings.TrimSpace(step.Workspace), "worktree") {
			return deliveryContractBranch
		}
	}
	return ""
}

func (r *EventResolver) jobHasDeliveryArtifact(j *jobstore.Job, meta *Metadata) bool {
	if j == nil {
		return false
	}
	contract := deliveryArtifactContract(j)
	repoRoot := r.teamDirParent()
	branch := strings.TrimSpace(j.Branch)
	worktree := strings.TrimSpace(j.Worktree)
	pr := strings.TrimSpace(j.PR)
	if meta != nil {
		if pr == "" {
			pr = strings.TrimSpace(meta.PR)
		}
		if branch == "" {
			branch = strings.TrimSpace(meta.Branch)
		}
		if worktree == "" {
			worktree = strings.TrimSpace(meta.Workspace)
		}
	}
	if deliveryReportArtifactPath(contract) != "" || contract == deliveryContractReport {
		return reportDeliveryArtifactExists(repoRoot, r.teamDir, worktree, deliveryReportArtifactPath(contract))
	}
	if openPullRequestExists(repoRoot, worktree, pr) {
		return true
	}
	if pushedBranchExists(repoRoot, worktree, branch) {
		return true
	}
	if committedBranchDiffExists(repoRoot, worktree, branch) {
		return true
	}
	return openTicketPullRequestWithRecentCommitExists(repoRoot, worktree, j, deliveryArtifactDispatchCutoff(j, meta))
}

func normalizeDeliveryArtifactContract(raw string) string {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, deliveryContractReport+":") {
		path := strings.TrimSpace(trimmed[len(deliveryContractReport)+1:])
		if path == "" {
			return ""
		}
		return deliveryContractReport + ":" + path
	}
	switch lower {
	case "", "none":
		return ""
	case deliveryContractBranch:
		return deliveryContractBranch
	case deliveryContractPR:
		return deliveryContractPR
	case deliveryContractReport:
		return deliveryContractReport
	case deliveryContractTicketToPR:
		return deliveryContractTicketToPR
	default:
		return ""
	}
}

func payloadDeliveryArtifactContract(payload map[string]any) string {
	if payload == nil || payloadIsProbe(payload) {
		return ""
	}
	if contract, ok := explicitPayloadDeliveryArtifactContract(payload); ok {
		return contract
	}
	if jobstore.IsReport(payloadJobKind(payload)) {
		if path := strings.TrimSpace(firstPayloadString(payload, "report_path", "report")); path != "" {
			return deliveryContractReport + ":" + path
		}
		return deliveryContractReport
	}
	if pipelineNameIsTicketToPR(payloadString(payload, "pipeline")) {
		return deliveryContractTicketToPR
	}
	if firstPayloadString(payload, "pr_url", "pr") != "" {
		return deliveryContractPR
	}
	if strings.EqualFold(payloadString(payload, "workspace"), "worktree") || strings.EqualFold(payloadString(payload, "isolation"), "worktree") {
		return deliveryContractBranch
	}
	return ""
}

func explicitPayloadDeliveryArtifactContract(payload map[string]any) (string, bool) {
	if payload == nil {
		return "", false
	}
	for _, key := range []string{"deliverable", "delivery_contract"} {
		if _, ok := payload[key]; ok {
			raw := strings.TrimSpace(payloadString(payload, key))
			if strings.EqualFold(raw, "none") {
				return "none", true
			}
			return normalizeDeliveryArtifactContract(raw), true
		}
	}
	return "", false
}

func NormalizeDeliveryArtifactContract(raw string) string {
	return normalizeDeliveryArtifactContract(raw)
}

func deliveryReportArtifactPath(contract string) string {
	contract = normalizeDeliveryArtifactContract(contract)
	lower := strings.ToLower(contract)
	if !strings.HasPrefix(lower, deliveryContractReport+":") {
		return ""
	}
	return strings.TrimSpace(contract[len(deliveryContractReport)+1:])
}

func reportDeliveryArtifactExists(repoRoot, teamDir, worktree, reportPath string) bool {
	reportPath = strings.TrimSpace(reportPath)
	if reportPath == "" {
		return false
	}
	for _, candidate := range reportArtifactCandidates(repoRoot, teamDir, worktree, reportPath) {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func reportArtifactCandidates(repoRoot, teamDir, worktree, reportPath string) []string {
	if filepath.IsAbs(reportPath) {
		return []string{filepath.Clean(reportPath)}
	}
	bases := []string{repoRoot, worktree, teamDir}
	seen := map[string]bool{}
	var out []string
	for _, base := range bases {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		candidate := filepath.Clean(filepath.Join(base, reportPath))
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	return out
}

func pipelineDeliveryArtifactContract(pipeline *topology.Pipeline) string {
	if pipeline == nil {
		return ""
	}
	if pipelineNameIsTicketToPR(pipeline.Name) {
		return deliveryContractTicketToPR
	}
	for _, step := range pipeline.Steps {
		if strings.EqualFold(strings.TrimSpace(step.Workspace), "worktree") {
			return deliveryContractBranch
		}
	}
	return ""
}

func pipelineNameIsTicketToPR(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == deliveryContractTicketToPR || strings.HasSuffix(lower, "_"+deliveryContractTicketToPR)
}

func openPullRequestExists(repoRoot, worktree, pr string) bool {
	pr = strings.TrimSpace(pr)
	if pr == "" {
		return false
	}
	dir := deliveryGitDir(repoRoot, worktree)
	if dir == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", pr, "--json", "state", "--jq", ".state")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(out)), "OPEN")
}

func pushedBranchExists(repoRoot, worktree, branch string) bool {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return false
	}
	dir := deliveryGitDir(repoRoot, worktree)
	if dir == "" {
		return false
	}
	out, ok := gitOutput(dir, "ls-remote", "--heads", "origin", "refs/heads/"+branch)
	return ok && strings.TrimSpace(out) != ""
}

func committedBranchDiffExists(repoRoot, worktree, branch string) bool {
	dir := deliveryGitDir(repoRoot, worktree)
	if dir == "" {
		return false
	}
	head := "HEAD"
	if strings.TrimSpace(branch) != "" {
		ref := "refs/heads/" + strings.TrimSpace(branch)
		if _, ok := gitOutput(dir, "rev-parse", "--verify", ref+"^{commit}"); ok {
			head = ref
		}
	}
	for _, base := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, ok := gitOutput(dir, "rev-parse", "--verify", base+"^{commit}"); !ok {
			continue
		}
		mergeBase, ok := gitOutput(dir, "merge-base", base, head)
		if !ok {
			continue
		}
		if gitDiffHasChanges(dir, strings.TrimSpace(mergeBase), head) {
			return true
		}
	}
	return false
}

type ticketPullRequestRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type pullRequestCommitView struct {
	State   string `json:"state"`
	Commits []struct {
		CommittedDate string `json:"committedDate"`
	} `json:"commits"`
}

func openTicketPullRequestWithRecentCommitExists(repoRoot, worktree string, j *jobstore.Job, cutoff time.Time) bool {
	ticket := deliveryTicketIdentifier(j)
	if ticket == "" || cutoff.IsZero() {
		return false
	}
	dir := deliveryGitDir(repoRoot, worktree)
	if dir == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	query := fmt.Sprintf("%q in:title,body", ticket)
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--state", "open", "--search", query, "--json", "number,url", "--limit", "20")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var refs []ticketPullRequestRef
	if err := json.Unmarshal(out, &refs); err != nil {
		return false
	}
	for _, ref := range refs {
		pr := strings.TrimSpace(ref.URL)
		if pr == "" && ref.Number > 0 {
			pr = fmt.Sprint(ref.Number)
		}
		if pr == "" {
			continue
		}
		if openPullRequestHasCommitAtOrAfter(dir, pr, cutoff) {
			return true
		}
	}
	return false
}

func openPullRequestHasCommitAtOrAfter(dir, pr string, cutoff time.Time) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", pr, "--json", "state,commits")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var view pullRequestCommitView
	if err := json.Unmarshal(out, &view); err != nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(view.State), "OPEN") {
		return false
	}
	cutoff = cutoff.UTC().Truncate(time.Second)
	for _, commit := range view.Commits {
		committedAt, ok := parseGitHubTimestamp(commit.CommittedDate)
		if ok && !committedAt.Before(cutoff) {
			return true
		}
	}
	return false
}

func deliveryTicketIdentifier(j *jobstore.Job) string {
	if j == nil {
		return ""
	}
	for _, raw := range []string{j.Ticket, j.TicketURL} {
		if id := jobstore.ExtractTicketIdentifier(raw); id != "" {
			return id
		}
	}
	return ""
}

func deliveryArtifactDispatchCutoff(j *jobstore.Job, meta *Metadata) time.Time {
	if j != nil {
		var earliestStep time.Time
		for _, step := range j.Steps {
			started := step.RunningAt
			if started.IsZero() {
				started = step.StartedAt
			}
			if started.IsZero() {
				continue
			}
			if earliestStep.IsZero() || started.Before(earliestStep) {
				earliestStep = started
			}
		}
		if !earliestStep.IsZero() {
			return earliestStep.UTC()
		}
	}
	if meta != nil && !meta.StartedAt.IsZero() {
		return meta.StartedAt.UTC()
	}
	if j != nil {
		return j.CreatedAt.UTC()
	}
	return time.Time{}
}

func parseGitHubTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		t, err := time.Parse(layout, raw)
		if err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func deliveryGitDir(repoRoot, worktree string) string {
	for _, dir := range []string{strings.TrimSpace(worktree), strings.TrimSpace(repoRoot)} {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	return ""
}

func gitOutput(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func gitDiffHasChanges(dir, base, head string) bool {
	if strings.TrimSpace(base) == "" || strings.TrimSpace(head) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--quiet", base, head)
	err := cmd.Run()
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}

func (r *EventResolver) notifyManagerMissingDeliveryArtifact(j *jobstore.Job, meta *Metadata, reason string) {
	if r == nil || r.mgr == nil || j == nil {
		return
	}
	instance := ""
	if meta != nil {
		instance = strings.TrimSpace(meta.Instance)
	}
	NotifyManagerMissingDeliveryArtifact(r.mgr.daemonRoot, j, instance, reason)
}

func NotifyManagerMissingDeliveryArtifact(daemonRoot string, j *jobstore.Job, instance, reason string) {
	if j == nil || strings.TrimSpace(daemonRoot) == "" {
		return
	}
	instance = strings.TrimSpace(instance)
	if instance == "" {
		instance = "the instance"
	}
	body := fmt.Sprintf("Job %s was marked failed after %s exited cleanly: %s. %s",
		j.ID, instance, reason, deliveryArtifactExpectation(j))
	_ = AppendMessage(daemonRoot, "manager", &Message{From: "daemon", Body: body})
}

func deliveryArtifactExpectation(j *jobstore.Job) string {
	contract := deliveryArtifactContract(j)
	if path := deliveryReportArtifactPath(contract); path != "" {
		return "Expected a non-empty report artifact at " + path + " before accepting done."
	}
	switch contract {
	case deliveryContractReport:
		return "Expected a report:<path> deliverable contract before accepting done."
	case deliveryContractBranch:
		return "Expected a pushed branch or committed diff before accepting done."
	case deliveryContractPR, deliveryContractTicketToPR:
		return "Expected an open PR, pushed branch, or committed diff before accepting done."
	default:
		return "Expected an open PR, pushed branch, or committed diff before accepting done."
	}
}
