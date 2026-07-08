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
	freedLocks := r.releaseLocksForInstanceLocked(spawned)
	var next *queuedEvent
	if len(tr.queue) > 0 {
		next = r.popReadyQueuedEventLocked(declared, tr, time.Now().UTC(), nil)
	}
	r.mu.Unlock()
	if meta, err := ReadMetadata(r.mgr.daemonRoot, spawned); err == nil {
		r.reconcileEphemeralJobExit(meta)
	}
	_, _ = r.markTeamCharterReaped(spawned, map[string]string{"reason": "instance_reaped"})
	drainQueues := freedLocks > 0
	if r.budgetsConfigured() {
		drainQueues = true
	}
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
	completedStep := reconcilePipelineStepExit(j, meta.Instance, status, now)
	if completedStep != nil {
		if status == jobstore.StatusDone && !allPipelineStepsDone(j) {
			j.Status = jobstore.StatusRunning
			message = "completed pipeline step"
		} else {
			j.Status = status
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
	r.autoReapJob(meta.Job, worktreepolicy.OnClose)
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
		return ev
	}
	return nil
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

func (r *EventResolver) drainReadyBudgetQueues(now time.Time) {
	if r == nil || r.mgr == nil || !r.budgetsConfigured() || !r.hasReadyBudgetQueueItem(now) {
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
	admission, err := r.budgetAdmissionLocked(ev.origin.Team, ev.payload, time.Now().UTC())
	if err != nil {
		r.mu.Unlock()
		return EventOutcome{}, err
	}
	if !admission.Allowed {
		ev.reason = QueueReasonBudgetExhausted
		ev.nextRetry = admission.NextTokenRetry
		tr.queue = append(tr.queue, ev)
		_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: ev.uniqueName, Reason: QueueReasonBudgetExhausted}, nil
	}
	if ev.reason == QueueReasonBudgetExhausted {
		ev.nextRetry = time.Time{}
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
	grant, err := r.grantPayloadBudgetLocked(ev.payload, ev.origin, ev.uniqueName, time.Now().UTC())
	if err != nil {
		r.releaseLocksForInstanceLocked(ev.uniqueName)
		r.mu.Unlock()
		return EventOutcome{}, err
	}
	if !grant.Allowed {
		r.releaseLocksForInstanceLocked(ev.uniqueName)
		ev.reason = QueueReasonBudgetExhausted
		ev.nextRetry = grant.NextTokenRetry
		tr.queue = append(tr.queue, ev)
		_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending))
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: ev.uniqueName, Reason: QueueReasonBudgetExhausted}, nil
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
