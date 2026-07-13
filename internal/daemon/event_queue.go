package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/budget"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/worktreecleanup"
	"github.com/agent-team-project/agent-team/internal/worktreepolicy"
)

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
	tr.releaseRunningLoad(spawned)
	freedLocks := r.releaseLocksForInstanceLocked(spawned)
	r.mu.Unlock()
	if meta, err := ReadMetadata(r.mgr.daemonRoot, spawned); err == nil {
		r.observeConcurrencyTerminal(meta)
		r.reconcileEphemeralJobExit(meta)
	}
	_, _ = r.markTeamCharterReaped(spawned, map[string]string{"reason": "instance_reaped"})
	var next *queuedEvent
	r.mu.Lock()
	tr = r.tracking[declared.Name]
	if tr != nil && tr.running < declared.Replicas && len(tr.queue) > 0 {
		next = r.popReadyQueuedEventLocked(declared, tr, time.Now().UTC(), nil)
	}
	r.mu.Unlock()
	drainQueues := freedLocks > 0
	if r.budgetsConfigured() {
		drainQueues = true
	}
	r.mu.Lock()
	if r.concurrencyConfiguredLocked() {
		drainQueues = true
	}
	r.mu.Unlock()
	// Freed lock slots may unblock lock_held waiters queued under other
	// declared instances; the same-instance pop above cannot reach them, so
	// run the shared drain pass (the same path as `agent-team queue drain`).
	if drainQueues {
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
		tr.releaseRunningLoad(next.uniqueName)
		r.mu.Unlock()
		return
	}
	outcome := EventOutcome{Instance: declared.Name, Action: "dispatched", InstanceID: next.uniqueName}
	if meta, err := ReadMetadata(r.mgr.daemonRoot, next.uniqueName); err == nil {
		r.updateLockLeasePID(meta.Instance, meta.PID)
	}
	_, _ = r.markTeamCharterSpawned(next.uniqueName, outcome)
	_ = RemoveQueueItem(r.mgr.daemonRoot, next.id)
}

func (r *EventResolver) reconcileEphemeralJobExit(meta *Metadata) {
	if meta == nil || strings.TrimSpace(r.teamDir) == "" || strings.TrimSpace(meta.Job) == "" {
		return
	}
	switch meta.Status {
	case StatusStopped, StatusExited, StatusCrashed:
	default:
		return
	}
	j, err := jobstore.Read(r.teamDir, meta.Job)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if meta.Status == StatusStopped || !jobstore.AttemptHeadMatches(j, meta.Attempt, meta.Head) {
		_, _ = budget.ReleaseJobInstanceAllocations(r.teamDir, j, meta.Instance, now)
		return
	}
	priorLastStatus := j.LastStatus
	status := jobstore.StatusDone
	eventType := "instance_exited"
	message := "instance exited successfully"
	if meta.Status == StatusCrashed || (meta.ExitCode != nil && *meta.ExitCode != 0) {
		status = jobstore.StatusFailed
		eventType = "instance_crashed"
		message = "instance crashed"
		if meta.ExitCode != nil && *meta.ExitCode != 0 {
			message = fmt.Sprintf("instance exited with code %d", *meta.ExitCode)
		}
	}
	if stalePipelineInstanceExit(j, meta.Instance) {
		_, _ = budget.ReleaseJobInstanceAllocations(r.teamDir, j, meta.Instance, now)
		return
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
	if status == jobstore.StatusDone && jobIsProbe(j) {
		message = "probe completed; report stored in last-message"
	}
	completedStep, stepTransitioned := reconcilePipelineStepExit(j, meta.Instance, meta.Attempt, meta.Head, status, now)
	if completedStep != nil {
		j.Status = pipelineStatusFromSteps(j)
		switch j.Status {
		case jobstore.StatusFailed:
			// A verifier/reviewer can record its step failure before the runtime
			// process exits cleanly. The durable step graph outranks process exit:
			// never turn that failure into a successful outer job completion.
			status = jobstore.StatusFailed
			if !stepTransitioned {
				eventType = "pipeline_step_failed"
				message = strings.TrimSpace(priorLastStatus)
				if message == "" {
					message = completedStep.ID + " failed before instance exit"
				}
			}
		case jobstore.StatusRunning:
			if status == jobstore.StatusDone && stepTransitioned {
				message = "completed pipeline step"
			}
		case jobstore.StatusDone:
			status = jobstore.StatusDone
		}
	} else if len(j.Steps) > 0 {
		// Even an unmatched process exit cannot close a pipeline independently of
		// its durable graph. This covers stale or partially rematerialized runtime
		// metadata without manufacturing a terminal success.
		j.Status = pipelineStatusFromSteps(j)
		switch j.Status {
		case jobstore.StatusFailed:
			status = jobstore.StatusFailed
			eventType = "pipeline_step_failed"
			message = strings.TrimSpace(priorLastStatus)
			if message == "" {
				message = "pipeline has a failed required step"
			}
		case jobstore.StatusDone:
			status = jobstore.StatusDone
		case jobstore.StatusRunning:
			if status == jobstore.StatusDone {
				message = "instance exited without completing the pipeline"
			}
		}
	} else {
		j.Status = status
	}
	deliverableMissing := false
	if j.Status == jobstore.StatusDone {
		if reason := r.missingDeliveryArtifactReason(j, meta); reason != "" {
			status = jobstore.StatusFailed
			j.Status = jobstore.StatusFailed
			if completedStep != nil {
				completedStep.Status = jobstore.StatusFailed
			}
			eventType = "deliverable_missing"
			message = reason
			deliverableMissing = true
		}
	}
	var timeoutData map[string]string
	if status == jobstore.StatusFailed {
		if exceeded, timeoutMessage, data := jobTimeBudgetExceeded(meta, j, completedStep, now); exceeded {
			eventType = managerOverdueWakeEventType
			message = timeoutMessage
			timeoutData = data
		}
	}
	j.LastEvent = eventType
	j.LastStatus = message
	j.UpdatedAt = now
	data := map[string]string{"instance": meta.Instance}
	for k, v := range timeoutData {
		data[k] = v
	}
	if meta.Branch != "" {
		data["branch"] = meta.Branch
	}
	if meta.PR != "" {
		data["pr"] = meta.PR
	}
	if meta.ExitCode != nil {
		data["exit_code"] = fmt.Sprint(*meta.ExitCode)
	}
	if deliverableMissing {
		data["deliverable_contract"] = deliveryArtifactContract(j)
	}
	if released, err := budget.ReleaseJobInstanceAllocations(r.teamDir, j, meta.Instance, now); err != nil {
		data["budget_release_error"] = err.Error()
	} else if len(released) > 0 {
		var tokens, consumed, unspent int64
		for _, rec := range released {
			tokens += rec.Tokens
			consumed += rec.ConsumedTokens
			unspent += rec.ReleasedTokens
		}
		data["budget_allocations_released"] = fmt.Sprint(len(released))
		data["budget_tokens_allocated"] = fmt.Sprint(tokens)
		data["budget_tokens_consumed"] = fmt.Sprint(consumed)
		data["budget_tokens_released"] = fmt.Sprint(unspent)
	}
	if err := r.writeJobWithAudit(j, eventType, "daemon", message, data); err != nil {
		return
	}
	if deliverableMissing {
		r.notifyManagerMissingDeliveryArtifact(j, meta, message)
	}

	// Opt-in: dispatch the next ready step without waiting for a manual
	// `agent-team pipeline tick`. Safe to call here — onReap has already released
	// r.mu, and this reuses the normal dispatch path (which does its own locking).
	r.tryAutoAdvancePipeline(j, meta, status)
	r.publishManagerCompletionEvents(j, completedStep, meta, status, stepTransitioned)
	r.autoReapJob(meta.Job, worktreepolicy.OnClose)
}

func jobTimeBudgetExceeded(meta *Metadata, j *jobstore.Job, step *jobstore.Step, now time.Time) (bool, string, map[string]string) {
	if meta == nil || j == nil || meta.Status != StatusCrashed || meta.StartedAt.IsZero() || meta.RuntimeDeadline.IsZero() {
		return false, "", nil
	}
	finishedAt := now.UTC()
	if !meta.ExitedAt.IsZero() {
		finishedAt = meta.ExitedAt.UTC()
	}
	startedAt := meta.StartedAt.UTC()
	if finishedAt.Before(startedAt) {
		return false, "", nil
	}
	budget, multiplier, stepID := jobTimeBudgetTarget(j, step)
	limit := hardTimeBudgetDuration(budget, multiplier)
	if limit <= 0 {
		return false, "", nil
	}
	elapsed := finishedAt.Sub(startedAt)
	if elapsed < limit {
		return false, "", nil
	}
	subject := "job " + j.ID
	if stepID != "" {
		subject = "pipeline step " + stepID
	}
	message := fmt.Sprintf("%s exceeded time budget after %s (threshold %s); instance killed", subject, roundedManagerWakeDuration(elapsed), roundedManagerWakeDuration(limit))
	data := map[string]string{
		"instance":         meta.Instance,
		"age":              roundedManagerWakeDuration(elapsed),
		"timeout":          roundedManagerWakeDuration(limit),
		"runtime_budget":   meta.RuntimeBudget,
		"runtime_deadline": meta.RuntimeDeadline.UTC().Format(time.RFC3339),
	}
	if stepID != "" {
		data["step"] = stepID
	}
	if multiplier > 0 {
		data["hard_multiplier"] = fmt.Sprintf("%g", multiplier)
	}
	return true, message, data
}

func jobTimeBudgetTarget(j *jobstore.Job, step *jobstore.Step) (time.Duration, float64, string) {
	if step != nil {
		if budget := parseBudgetNoticeDuration(step.TimeBudget); budget > 0 {
			return budget, step.HardMultiplier, step.ID
		}
	}
	if j == nil {
		return 0, 0, ""
	}
	return parseBudgetNoticeDuration(j.TimeBudget), j.HardMultiplier, ""
}

func (r *EventResolver) publishManagerCompletionEvents(j *jobstore.Job, completedStep *jobstore.Step, meta *Metadata, status jobstore.Status, stepTransitioned bool) {
	if !managerCompletionShouldWake(j, completedStep, status) {
		return
	}
	payload := managerCompletionPayload(j, completedStep, meta, status)
	if completedStep != nil && stepTransitioned {
		r.publishCompletionEventIfMatched(topology.EventJobStepCompleted, payload)
		if completionDeliverableReady(j, completedStep, status) {
			r.publishCompletionEventIfMatched(topology.EventDeliverableReady, payload)
		}
	}
	if jobStatusTerminal(j.Status) {
		r.publishCompletionEventIfMatched(topology.EventJobCompleted, payload)
	}
}

func (r *EventResolver) publishCompletionEventIfMatched(eventType string, payload map[string]any) {
	r.mu.Lock()
	t := r.topo
	r.mu.Unlock()
	if traceForTopology(t, eventType, payload).MatchedRules == 0 {
		return
	}
	_, _ = r.EventWithResult(eventType, payload)
}

func managerCompletionShouldWake(j *jobstore.Job, completedStep *jobstore.Step, status jobstore.Status) bool {
	if j == nil {
		return false
	}
	if status == jobstore.StatusFailed || jobStatusTerminal(j.Status) {
		return true
	}
	if completionDeliverableReady(j, completedStep, status) {
		return true
	}
	return managerGateReady(j)
}

func managerCompletionPayload(j *jobstore.Job, completedStep *jobstore.Step, meta *Metadata, status jobstore.Status) map[string]any {
	payload := topology.ManagerCompletionTriggerPayload(j.Pipeline, managerCompletionTarget(j), managerGateReady(j))
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	payload["ticket"] = j.Ticket
	payload["status"] = string(status)
	payload["job_status"] = string(j.Status)
	if j.TicketURL != "" {
		payload["ticket_url"] = j.TicketURL
	}
	if j.Epic != "" {
		payload["epic"] = j.Epic
	}
	if j.Branch != "" {
		payload["branch"] = j.Branch
	}
	if j.Worktree != "" {
		payload["worktree"] = j.Worktree
	}
	if j.PR != "" {
		payload["pr"] = j.PR
	}
	if completedStep != nil {
		payload["pipeline_step"] = completedStep.ID
		payload["completed_step"] = completedStep.ID
		payload["completed_target"] = completedStep.Target
		payload["step_status"] = string(completedStep.Status)
		if completedStep.Instance != "" {
			payload["step_instance"] = completedStep.Instance
		}
	}
	if meta != nil {
		payload["instance"] = meta.Instance
		payload["agent"] = meta.Agent
		if meta.ExitCode != nil {
			payload["exit_code"] = *meta.ExitCode
		}
	}
	if completionDeliverableReady(j, completedStep, status) {
		payload["deliverable_ready"] = true
	}
	return payload
}

func completionDeliverableReady(j *jobstore.Job, completedStep *jobstore.Step, status jobstore.Status) bool {
	if j == nil || completedStep == nil || status != jobstore.StatusDone || strings.TrimSpace(j.PR) == "" {
		return false
	}
	implementationTarget := strings.TrimSpace(j.ImplementationAgent)
	if implementationTarget == "" {
		implementationTarget = strings.TrimSpace(j.Target)
	}
	return implementationTarget == "" || strings.TrimSpace(completedStep.Target) == implementationTarget
}

func managerGateReady(j *jobstore.Job) bool {
	if j == nil {
		return false
	}
	done := map[string]bool{}
	for i := range j.Steps {
		if jobstore.StepSatisfiesDependency(&j.Steps[i]) {
			done[j.Steps[i].ID] = true
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Gate != jobstore.StepGateManual || !jobstore.StepGatePending(j, step) {
			continue
		}
		if stepDependenciesMet(step, done) {
			return true
		}
	}
	return false
}

func managerCompletionTarget(j *jobstore.Job) string {
	const fallback = "manager"
	if j == nil {
		return fallback
	}

	done := map[string]bool{}
	for i := range j.Steps {
		if jobstore.StepSatisfiesDependency(&j.Steps[i]) {
			done[j.Steps[i].ID] = true
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Gate != jobstore.StepGateManual || !jobstore.StepGatePending(j, step) || !stepDependenciesMet(step, done) {
			continue
		}
		if target := strings.TrimSpace(step.Target); target != "" {
			return target
		}
	}

	// Failed jobs can terminate before their decision gate becomes ready. A
	// single declared manual owner still identifies who must reconcile that
	// terminal result. Multiple different owners are ambiguous, so retain the
	// historical user-facing manager fallback.
	owner := ""
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Gate != jobstore.StepGateManual {
			continue
		}
		target := strings.TrimSpace(step.Target)
		if target == "" {
			continue
		}
		if owner != "" && owner != target {
			return fallback
		}
		owner = target
	}
	if owner != "" {
		return owner
	}
	return fallback
}

func stepDependenciesMet(step *jobstore.Step, done map[string]bool) bool {
	if step == nil {
		return false
	}
	for _, dep := range step.After {
		if !done[dep] {
			return false
		}
	}
	return true
}

func stalePipelineInstanceExit(j *jobstore.Job, instance string) bool {
	if j == nil || len(j.Steps) == 0 || strings.TrimSpace(instance) == "" {
		return false
	}
	active := false
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != jobstore.StatusRunning && step.Status != jobstore.StatusQueued {
			continue
		}
		active = true
		if strings.TrimSpace(step.Instance) == strings.TrimSpace(instance) {
			return false
		}
	}
	return active
}

func (r *EventResolver) onTerminalMetadata(meta *Metadata) {
	if r == nil || meta == nil {
		return
	}
	r.recoverQueueStateNoDrain()
	r.observeConcurrencyTerminal(meta)
	r.mu.Lock()
	r.releaseLocksForInstanceLocked(meta.Instance)
	r.mu.Unlock()
	r.reconcileEphemeralJobExit(meta)
	r.DrainQueues()
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
	// Failed work must remain rematerializable: keep its source worktree/branch
	// until an explicit retry, close, or cleanup decision. Successful terminal
	// jobs retain the configured automatic reap behavior.
	if j.Status != jobstore.StatusDone {
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
		if r.queuedEventGenerationStale(ev) {
			tr.queue = append(tr.queue[:i:i], tr.queue[i+1:]...)
			_ = RemoveQueueItem(r.mgr.daemonRoot, ev.id)
			return r.popReadyQueuedEventLocked(inst, tr, now, ids)
		}
		if !ev.nextRetry.IsZero() && ev.nextRetry.After(now) {
			continue
		}
		admission, err := r.budgetAdmissionLocked(ev.origin.Team, ev.payload, now)
		if err != nil {
			ev.lastError = err.Error()
			_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
			continue
		}
		if !admission.Allowed {
			ev.reason = QueueReasonBudgetExhausted
			ev.nextRetry = admission.NextTokenRetry
			_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
			continue
		}
		if ev.reason == QueueReasonBudgetExhausted {
			ev.nextRetry = time.Time{}
		}
		loadWeight := r.dispatchLoadWeightLocked(ev.origin.Team)
		concurrencyAdmission := r.concurrencyAdmissionForLoadLocked(now, loadWeight)
		if !concurrencyAdmission.Allowed {
			ev.reason = QueueReasonConcurrencyCeiling
			ev.lastError = concurrencyQueueMessage(concurrencyAdmission)
			_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
			continue
		}
		if ev.reason == QueueReasonConcurrencyCeiling {
			ev.lastError = ""
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
		grant, err := r.grantPayloadBudgetLocked(ev.payload, ev.origin, ev.uniqueName, now)
		if err != nil {
			ev.lastError = err.Error()
			ev.reason = "budget_error"
			r.releaseLocksForInstanceLocked(ev.uniqueName)
			_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
			continue
		}
		if !grant.Allowed {
			ev.reason = QueueReasonBudgetExhausted
			ev.nextRetry = grant.NextTokenRetry
			r.releaseLocksForInstanceLocked(ev.uniqueName)
			_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
			continue
		}
		tr.queue = append(tr.queue[:i:i], tr.queue[i+1:]...)
		tr.running++
		tr.trackRunningLoad(ev.uniqueName, loadWeight)
		return ev
	}
	return nil
}

func (r *EventResolver) queuedEventGenerationStale(ev *queuedEvent) bool {
	if r == nil || ev == nil || strings.TrimSpace(r.teamDir) == "" {
		return false
	}
	id := eventJobID(ev.payload)
	if id == "" {
		return false
	}
	j, err := jobstore.Read(r.teamDir, id)
	return err == nil && !jobstore.AttemptHeadMatches(j, payloadAttempt(ev.payload), payloadString(ev.payload, "head"))
}

func (r *EventResolver) recordQueueFailure(declared string, ev *queuedEvent, spawnErr error) {
	if ev == nil {
		return
	}
	_, _ = budget.ReleaseAllocations(r.teamDir, budget.ReleaseRequest{JobID: eventJobID(ev.payload), Instance: ev.uniqueName, Now: time.Now().UTC()})
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
	r.recoverQueueStateNoDrain()
	r.DrainQueues()
}

func (r *EventResolver) recoverQueueStateNoDrain() {
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
			tr.trackRunningLoad(meta.Instance, r.dispatchLoadWeightLocked(meta.Origin.Team))
		}
	}
	r.recoverLockStateLocked(time.Now().UTC())
	r.mu.Unlock()
	_ = r.loadPersistedQueue()
}

// RunBudgetQueueDrains wakes budget_exhausted queue items whose token-window
// retry time is due. Job-slot budget queues are kicked by onReap when a running
// job finishes.
func (r *EventResolver) RunBudgetQueueDrains(ctx context.Context) {
	if r == nil {
		return
	}
	r.drainReadyBudgetQueues(time.Now().UTC())
	ticker := time.NewTicker(budgetDrainPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.drainReadyBudgetQueues(now.UTC())
		}
	}
}

// RunConcurrencyQueueDrains retries concurrency-held queue items periodically.
// Unlike replica capacity, machine load can drop because external non-daemon
// work finishes, so no daemon reap event may arrive to kick the queue.
func (r *EventResolver) RunConcurrencyQueueDrains(ctx context.Context) {
	if r == nil {
		return
	}
	r.drainConcurrencyQueues()
	ticker := time.NewTicker(concurrencyDrainPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.drainConcurrencyQueues()
		}
	}
}

func (r *EventResolver) drainReadyBudgetQueues(now time.Time) {
	if r == nil || r.mgr == nil || !r.budgetsConfigured() || !r.hasReadyBudgetQueueItem(now) {
		return
	}
	r.DrainQueues()
}

func (r *EventResolver) drainConcurrencyQueues() {
	if r == nil || r.mgr == nil || !r.concurrencyConfigured() || !r.hasConcurrencyQueueItem() {
		return
	}
	r.DrainQueues()
}

func (r *EventResolver) hasReadyBudgetQueueItem(now time.Time) bool {
	items, err := ListQueueItems(r.mgr.daemonRoot)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item == nil || item.State != QueueStatePending || item.Reason != QueueReasonBudgetExhausted {
			continue
		}
		if item.NextRetry.IsZero() {
			continue
		}
		if !item.NextRetry.After(now) {
			return true
		}
	}
	return false
}

func (r *EventResolver) concurrencyConfigured() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.concurrencyConfiguredLocked()
}

func (r *EventResolver) hasConcurrencyQueueItem() bool {
	items, err := ListQueueItems(r.mgr.daemonRoot)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item != nil && item.State == QueueStatePending && item.Reason == QueueReasonConcurrencyCeiling {
			return true
		}
	}
	return false
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
				tr.releaseRunningLoad(ev.uniqueName)
			}
			r.mu.Unlock()
			result.Rejected++
			result.Outcomes = append(result.Outcomes, EventOutcome{Instance: declared.Name, Action: "rejected", InstanceID: ev.uniqueName, Reason: err.Error()})
			continue
		}
		r.updateLockLeasePID(meta.Instance, meta.PID)
		outcome := EventOutcome{Instance: declared.Name, Action: "dispatched", InstanceID: ev.uniqueName}
		_, _ = r.markTeamCharterSpawned(ev.uniqueName, outcome)
		_ = RemoveQueueItem(r.mgr.daemonRoot, ev.id)
		result.Dispatched++
		result.Outcomes = append(result.Outcomes, outcome)
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
		globalRunning := r.runningEphemeralLocked()
		globalRunningLoad := r.runningEphemeralLoadLocked()
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
				admission, err := r.budgetAdmissionLocked(ev.origin.Team, ev.payload, now)
				if err != nil || !admission.Allowed {
					continue
				}
				loadWeight := r.dispatchLoadWeightLocked(ev.origin.Team)
				if concurrencyAdmission := r.concurrencyAdmissionPreviewLocked(now, globalRunning, globalRunningLoad, loadWeight); !concurrencyAdmission.Allowed {
					continue
				}
				locks := ev.locks
				if len(locks) == 0 {
					locks = r.dispatchLocksLocked(inst, ev.payload)
				}
				r.ensurePreviewLocksLocked(locks, ev.origin, lockUsage, lockSlots)
				scopedLocks := r.scopedLockNamesLocked(locks, ev.origin)
				if !previewLocksAvailable(scopedLocks, ev.uniqueName, lockUsage, lockSlots) {
					continue
				}
				previewReserveLocks(scopedLocks, ev.uniqueName, lockUsage)
				result.WouldDispatch++
				result.Outcomes = append(result.Outcomes, EventOutcome{Instance: inst.Name, Action: "would_dispatch", InstanceID: ev.uniqueName})
				capacity--
				globalRunning++
				globalRunningLoad += loadWeight
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
	queued := ev
	tr.queue = append(tr.queue, ev)
	ev = r.popReadyQueuedEventLocked(inst, tr, time.Now().UTC(), map[string]bool{id: true})
	if ev == nil {
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: item.InstanceID, Reason: queued.reason}, nil
	}
	r.mu.Unlock()

	meta, err := r.spawn(inst, ev.uniqueName, ev.eventType, ev.payload)
	if err != nil {
		r.recordQueueFailure(inst.Name, ev, err)
		r.mu.Lock()
		if tr.running > 0 {
			tr.running--
			tr.releaseRunningLoad(ev.uniqueName)
		}
		r.mu.Unlock()
		_, _ = budget.ReleaseAllocations(r.teamDir, budget.ReleaseRequest{JobID: eventJobID(ev.payload), Instance: ev.uniqueName, Now: time.Now().UTC()})
		return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: ev.uniqueName, Reason: err.Error()}, nil
	}
	r.updateLockLeasePID(meta.Instance, meta.PID)
	outcome := EventOutcome{Instance: inst.Name, Action: "dispatched", InstanceID: ev.uniqueName}
	_, _ = r.markTeamCharterSpawned(ev.uniqueName, outcome)
	_ = RemoveQueueItem(r.mgr.daemonRoot, ev.id)
	return outcome, nil
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
