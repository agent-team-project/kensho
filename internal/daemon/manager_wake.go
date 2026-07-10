package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
)

const (
	managerWakeSweepInterval = time.Minute

	managerIdleWakeEventType    = "manager.idle_wake"
	managerOverdueWakeEventType = "job.time_budget_exceeded"

	managerIdleWakeLifecycleAction    = "manager_idle_wake"
	managerOverdueWakeLifecycleAction = "job_time_budget_exceeded"

	managerIdleWakeDefaultBackoff = 5 * time.Minute
	managerIdleWakeMaxBackoff     = time.Hour
)

// ManagerWakeSweepResult summarizes one daemon policy sweep for tests and
// operator-facing logs. Normal daemon operation treats it as best-effort.
type ManagerWakeSweepResult struct {
	IdleWakeups []ManagerWakeupResult `json:"idle_wakeups,omitempty"`
	Overdue     []ManagerWakeupResult `json:"overdue,omitempty"`
	DryRun      bool                  `json:"dry_run,omitempty"`
}

// ManagerWakeupResult describes one manager wake attempt or suppression.
type ManagerWakeupResult struct {
	Manager  string `json:"manager"`
	JobID    string `json:"job_id,omitempty"`
	StepID   string `json:"step_id,omitempty"`
	Action   string `json:"action"`
	Reason   string `json:"reason,omitempty"`
	Instance string `json:"instance,omitempty"`
}

type managerIdleWakeState struct {
	Instance                 string    `json:"instance"`
	JobID                    string    `json:"job_id,omitempty"`
	LastWakeAt               time.Time `json:"last_wake_at,omitempty"`
	LastObservedJobUpdatedAt time.Time `json:"last_observed_job_updated_at,omitempty"`
	Backoff                  string    `json:"backoff,omitempty"`
}

type managerOverdueWakeState struct {
	JobID          string    `json:"job_id"`
	StepID         string    `json:"step_id"`
	StartedAt      time.Time `json:"started_at"`
	Timeout        string    `json:"timeout"`
	LastNotifiedAt time.Time `json:"last_notified_at"`
}

// RunManagerWakeSweeps detects missing manager wakeups that topology events
// cannot cover: a stopped manager with unfinished manager-owned work and
// running pipeline steps that have crossed their expected time budget.
func (r *EventResolver) RunManagerWakeSweeps(ctx context.Context) {
	if r == nil {
		return
	}
	r.runManagerWakeSweep(time.Now().UTC())
	ticker := time.NewTicker(managerWakeSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.runManagerWakeSweep(now.UTC())
		}
	}
}

func (r *EventResolver) runManagerWakeSweep(now time.Time) {
	if _, err := r.SweepManagerWakeupsWithResult(now); err != nil {
		r.mu.Lock()
		out := r.logOut
		r.mu.Unlock()
		if out != nil {
			fmt.Fprintf(out, "%s manager wake sweep: %v\n", time.Now().UTC().Format(time.RFC3339), err)
		}
	}
}

// SweepManagerWakeupsWithResult runs one policy sweep. A zero now uses current
// UTC time. It is exported inside the package for deterministic tests.
func (r *EventResolver) SweepManagerWakeupsWithResult(now time.Time) (*ManagerWakeSweepResult, error) {
	return r.managerWakeupsWithResult(now, false)
}

// PreviewManagerWakeupsWithResult evaluates one policy sweep without writing
// mailbox, lifecycle, job, or wake-backoff state and without starting managers.
func (r *EventResolver) PreviewManagerWakeupsWithResult(now time.Time) (*ManagerWakeSweepResult, error) {
	return r.managerWakeupsWithResult(now, true)
}

func (r *EventResolver) managerWakeupsWithResult(now time.Time, dryRun bool) (*ManagerWakeSweepResult, error) {
	result := &ManagerWakeSweepResult{DryRun: dryRun}
	if r == nil || r.mgr == nil || strings.TrimSpace(r.teamDir) == "" {
		return result, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	r.mu.Lock()
	topo := r.topo
	r.mu.Unlock()
	if topo == nil {
		return result, nil
	}
	jobs, err := jobstore.List(r.teamDir)
	if err != nil {
		return result, err
	}
	metas, err := ListMetadata(r.mgr.daemonRoot)
	if err != nil {
		return result, err
	}
	result.IdleWakeups = r.sweepIdleManagers(now, topo, jobs, metas, dryRun)
	overdue, err := r.sweepOverdueExpectations(now, topo, jobs, dryRun)
	if err != nil {
		return result, err
	}
	result.Overdue = overdue
	return result, nil
}

func (r *EventResolver) sweepIdleManagers(now time.Time, topo *topology.Topology, jobs []*jobstore.Job, metas []*Metadata, dryRun bool) []ManagerWakeupResult {
	var out []ManagerWakeupResult
	managers := managerWakeInstances(topo)
	managerNames := managerWakeInstanceNames(managers)
	for _, inst := range managers {
		if r.mgr.isRunning(inst.Name) {
			continue
		}
		job := managerIdleBacklogJob(inst, jobs, metas, managerNames)
		if job == nil {
			continue
		}
		wakeup := r.wakeIdleManagerOrPreview(now, inst, job, dryRun)
		out = append(out, wakeup)
	}
	return out
}

func (r *EventResolver) sweepOverdueExpectations(now time.Time, topo *topology.Topology, jobs []*jobstore.Job, dryRun bool) ([]ManagerWakeupResult, error) {
	var out []ManagerWakeupResult
	for _, j := range jobs {
		if j == nil || jobTerminal(j.Status) {
			continue
		}
		manager := managerForJob(topo, j)
		if manager == nil {
			continue
		}
		for i := range j.Steps {
			step := &j.Steps[i]
			startedAt, timeout, ok := overdueStepExpectation(step)
			if !ok || !now.After(startedAt.Add(timeout)) {
				continue
			}
			state, _ := readManagerOverdueWakeState(r.mgr.daemonRoot, j.ID, step.ID)
			if state != nil && state.StartedAt.Equal(startedAt) && state.Timeout == timeout.String() {
				continue
			}
			wakeup, err := r.wakeManagerForOverdueStepOrPreview(now, manager, j, step, startedAt, timeout, dryRun)
			if err != nil {
				return out, err
			}
			out = append(out, wakeup)
		}
	}
	return out, nil
}

func managerWakeInstanceNames(instances []*topology.Instance) map[string]struct{} {
	out := make(map[string]struct{}, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		name := strings.TrimSpace(inst.Name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

func managerWakeInstances(topo *topology.Topology) []*topology.Instance {
	if topo == nil {
		return nil
	}
	var out []*topology.Instance
	for _, inst := range topo.SortedInstances() {
		if inst == nil || inst.Ephemeral || strings.TrimSpace(inst.Agent) != "manager" {
			continue
		}
		out = append(out, inst)
	}
	return out
}

func managerIdleBacklogJob(inst *topology.Instance, jobs []*jobstore.Job, metas []*Metadata, managerNames map[string]struct{}) *jobstore.Job {
	if inst == nil {
		return nil
	}
	for _, j := range jobs {
		if !managerOwnsPendingWork(inst, j, managerNames) {
			continue
		}
		if managerHasActiveChildWork(inst.Name, j, jobs, metas) {
			continue
		}
		return j
	}
	return nil
}

func managerOwnsPendingWork(inst *topology.Instance, j *jobstore.Job, managerNames map[string]struct{}) bool {
	return managerOwnsIncompleteJob(inst, j) || managerOwnsCompletedDeliverable(inst, j, managerNames)
}

func managerOwnsIncompleteJob(inst *topology.Instance, j *jobstore.Job) bool {
	if inst == nil || j == nil || jobTerminal(j.Status) {
		return false
	}
	name := strings.TrimSpace(inst.Name)
	if name == "" {
		return false
	}
	if strings.TrimSpace(j.Target) == name || strings.TrimSpace(j.Instance) == name {
		return true
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if strings.TrimSpace(step.Target) == name || strings.TrimSpace(step.Instance) == name {
			return true
		}
	}
	return false
}

func managerOwnsCompletedDeliverable(inst *topology.Instance, j *jobstore.Job, managerNames map[string]struct{}) bool {
	if inst == nil || j == nil || !jobHasPendingManagerDeliverable(j) {
		return false
	}
	name := strings.TrimSpace(inst.Name)
	if name == "" {
		return false
	}
	if strings.TrimSpace(j.Origin.Instance) == name || managerTarget(name, j.Target, j.Instance) {
		return true
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if managerTarget(name, step.Target, step.Instance) {
			return true
		}
	}
	if completedDeliverableTargetsKnownManager(j, managerNames) {
		return false
	}
	if len(managerNames) == 1 {
		_, ok := managerNames[name]
		return ok
	}
	return false
}

func completedDeliverableTargetsKnownManager(j *jobstore.Job, managerNames map[string]struct{}) bool {
	if j == nil || len(managerNames) == 0 {
		return false
	}
	for _, candidate := range []string{j.Origin.Instance, j.Target, j.Instance} {
		if _, ok := managerNames[strings.TrimSpace(candidate)]; ok {
			return true
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		for _, candidate := range []string{step.Target, step.Instance} {
			if _, ok := managerNames[strings.TrimSpace(candidate)]; ok {
				return true
			}
		}
	}
	return false
}

func jobHasPendingManagerDeliverable(j *jobstore.Job) bool {
	if j == nil || j.Status != jobstore.StatusDone {
		return false
	}
	if strings.TrimSpace(j.PR) == "" && strings.TrimSpace(j.Branch) == "" && strings.TrimSpace(j.Worktree) == "" {
		return false
	}
	if jobstore.IsHandledTerminalEvent(j.LastEvent) {
		return false
	}
	return true
}

func managerHasActiveChildWork(manager string, candidate *jobstore.Job, jobs []*jobstore.Job, metas []*Metadata) bool {
	manager = strings.TrimSpace(manager)
	if manager == "" || candidate == nil {
		return false
	}
	for _, meta := range metas {
		if meta == nil || meta.Status != StatusRunning || strings.TrimSpace(meta.Agent) == "manager" {
			continue
		}
		if meta.PID <= 0 || !PidLiveCheck(meta.PID) {
			continue
		}
		if strings.TrimSpace(meta.Job) == candidate.ID || strings.TrimSpace(meta.Origin.Instance) == manager || strings.TrimSpace(meta.Origin.Job) == candidate.ID {
			return true
		}
	}
	if jobHasActiveNonManagerWork(candidate, manager) {
		return true
	}
	candidateEpic := strings.TrimSpace(candidate.Epic)
	for _, j := range jobs {
		if j == nil || j.ID == candidate.ID || !jobActive(j.Status) {
			continue
		}
		if strings.TrimSpace(j.Origin.Instance) != manager && (candidateEpic == "" || strings.TrimSpace(j.Epic) != candidateEpic) {
			continue
		}
		if jobHasActiveNonManagerWork(j, manager) {
			return true
		}
	}
	return false
}

func jobHasActiveNonManagerWork(j *jobstore.Job, manager string) bool {
	if j == nil {
		return false
	}
	if len(j.Steps) == 0 {
		return jobActive(j.Status) && !managerTarget(manager, j.Target, j.Instance)
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if !jobActive(step.Status) {
			continue
		}
		if managerTarget(manager, step.Target, step.Instance) {
			continue
		}
		return true
	}
	return false
}

func managerTarget(manager, target, instance string) bool {
	manager = strings.TrimSpace(manager)
	return manager != "" && (strings.TrimSpace(target) == manager || strings.TrimSpace(instance) == manager)
}

func jobActive(status jobstore.Status) bool {
	return status == jobstore.StatusQueued || status == jobstore.StatusRunning
}

func jobTerminal(status jobstore.Status) bool {
	return status == jobstore.StatusDone || status == jobstore.StatusFailed
}

func (r *EventResolver) wakeIdleManager(now time.Time, inst *topology.Instance, j *jobstore.Job) ManagerWakeupResult {
	return r.wakeIdleManagerOrPreview(now, inst, j, false)
}

func (r *EventResolver) wakeIdleManagerOrPreview(now time.Time, inst *topology.Instance, j *jobstore.Job, dryRun bool) ManagerWakeupResult {
	state, _ := readManagerIdleWakeState(r.mgr.daemonRoot, inst.Name)
	due, nextBackoff, reason := managerIdleWakeDue(now, state, j)
	if !due {
		return ManagerWakeupResult{Manager: inst.Name, JobID: j.ID, Action: "skipped", Reason: reason}
	}
	if dryRun {
		return ManagerWakeupResult{Manager: inst.Name, JobID: j.ID, Action: "would_dispatch", Reason: reason}
	}
	payload := managerIdleWakePayload(inst, j, reason)
	meta, err := r.deliverManagerPolicyWake(now, inst, managerIdleWakeEventType, payload)
	result := ManagerWakeupResult{Manager: inst.Name, JobID: j.ID, Action: "dispatched"}
	if meta != nil {
		result.Instance = meta.Instance
	}
	if err != nil {
		result.Action = "failed"
		result.Reason = err.Error()
	}
	updated := &managerIdleWakeState{
		Instance:                 inst.Name,
		JobID:                    j.ID,
		LastWakeAt:               now,
		LastObservedJobUpdatedAt: j.UpdatedAt,
		Backoff:                  nextBackoff.String(),
	}
	_ = writeManagerIdleWakeState(r.mgr.daemonRoot, updated)
	_ = AppendLifecycleEvent(r.mgr.daemonRoot, &LifecycleEvent{
		Action:   managerIdleWakeLifecycleAction,
		Instance: inst.Name,
		Agent:    inst.Agent,
		Job:      j.ID,
		Ticket:   j.Ticket,
		Status:   StatusRunning,
		Message:  "manager idle with unfinished job " + j.ID,
	})
	return result
}

func managerIdleWakeDue(now time.Time, state *managerIdleWakeState, j *jobstore.Job) (bool, time.Duration, string) {
	if j == nil {
		return false, managerIdleWakeDefaultBackoff, "no job"
	}
	reason := managerIdleWakeReason(j)
	if state == nil || state.JobID != j.ID || j.UpdatedAt.After(state.LastObservedJobUpdatedAt) {
		return true, managerIdleWakeDefaultBackoff, reason
	}
	backoff := parseManagerBackoff(state.Backoff)
	if state.LastWakeAt.IsZero() {
		return true, backoff, reason
	}
	if now.Before(state.LastWakeAt.Add(backoff)) {
		return false, backoff, "backoff until " + state.LastWakeAt.Add(backoff).UTC().Format(time.RFC3339)
	}
	return true, clampManagerBackoff(backoff * 2), reason + " without observed progress"
}

func managerIdleWakeReason(j *jobstore.Job) string {
	if jobHasPendingManagerDeliverable(j) {
		return "completed job has PR or branch awaiting manager action"
	}
	return "unfinished manager-owned job"
}

func parseManagerBackoff(raw string) time.Duration {
	backoff, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || backoff <= 0 {
		return managerIdleWakeDefaultBackoff
	}
	return clampManagerBackoff(backoff)
}

func clampManagerBackoff(backoff time.Duration) time.Duration {
	if backoff < managerIdleWakeDefaultBackoff {
		return managerIdleWakeDefaultBackoff
	}
	if backoff > managerIdleWakeMaxBackoff {
		return managerIdleWakeMaxBackoff
	}
	return backoff
}

func managerIdleWakePayload(inst *topology.Instance, j *jobstore.Job, reason string) map[string]any {
	payload := map[string]any{
		"source":     "daemon:idle_sweep",
		"target":     inst.Name,
		"job_id":     j.ID,
		"job":        j.ID,
		"ticket":     j.Ticket,
		"status":     string(j.Status),
		"job_status": string(j.Status),
		"reason":     reason,
	}
	if j.TicketURL != "" {
		payload["ticket_url"] = j.TicketURL
	}
	if j.Epic != "" {
		payload["epic"] = j.Epic
	}
	if j.Pipeline != "" {
		payload["pipeline"] = j.Pipeline
	}
	return payload
}

func overdueStepExpectation(step *jobstore.Step) (time.Time, time.Duration, bool) {
	if step == nil || step.Status != jobstore.StatusRunning {
		return time.Time{}, 0, false
	}
	startedAt := step.RunningAt
	if startedAt.IsZero() {
		startedAt = step.StartedAt
	}
	if startedAt.IsZero() {
		startedAt = step.QueuedAt
	}
	if startedAt.IsZero() {
		return time.Time{}, 0, false
	}
	for _, raw := range []string{step.TimeBudget, step.Timeout} {
		timeout, err := time.ParseDuration(strings.TrimSpace(raw))
		if err == nil && timeout > 0 {
			return startedAt.UTC(), timeout, true
		}
	}
	return time.Time{}, 0, false
}

func managerForJob(topo *topology.Topology, j *jobstore.Job) *topology.Instance {
	if topo == nil || j == nil {
		return nil
	}
	for _, candidate := range []string{j.Origin.Instance, j.Instance, j.Target} {
		if inst := topo.Find(strings.TrimSpace(candidate)); inst != nil && !inst.Ephemeral && strings.TrimSpace(inst.Agent) == "manager" {
			return inst
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		for _, candidate := range []string{step.Instance, step.Target} {
			if inst := topo.Find(strings.TrimSpace(candidate)); inst != nil && !inst.Ephemeral && strings.TrimSpace(inst.Agent) == "manager" {
				return inst
			}
		}
	}
	managers := managerWakeInstances(topo)
	if len(managers) == 1 {
		return managers[0]
	}
	return nil
}

func (r *EventResolver) wakeManagerForOverdueStep(now time.Time, inst *topology.Instance, j *jobstore.Job, step *jobstore.Step, startedAt time.Time, timeout time.Duration) (ManagerWakeupResult, error) {
	return r.wakeManagerForOverdueStepOrPreview(now, inst, j, step, startedAt, timeout, false)
}

func (r *EventResolver) wakeManagerForOverdueStepOrPreview(now time.Time, inst *topology.Instance, j *jobstore.Job, step *jobstore.Step, startedAt time.Time, timeout time.Duration, dryRun bool) (ManagerWakeupResult, error) {
	age := now.Sub(startedAt)
	message := fmt.Sprintf("pipeline step %s exceeded time budget after %s (threshold %s)", step.ID, roundedManagerWakeDuration(age), roundedManagerWakeDuration(timeout))
	if dryRun {
		return ManagerWakeupResult{Manager: inst.Name, JobID: j.ID, StepID: step.ID, Action: "would_dispatch", Reason: message}, nil
	}
	payload := map[string]any{
		"source":        "daemon:expectation_timeout",
		"target":        inst.Name,
		"job_id":        j.ID,
		"job":           j.ID,
		"ticket":        j.Ticket,
		"status":        string(j.Status),
		"job_status":    string(j.Status),
		"pipeline_step": step.ID,
		"step":          step.ID,
		"step_status":   string(step.Status),
		"timeout":       timeout.String(),
		"age":           age.String(),
	}
	if j.TicketURL != "" {
		payload["ticket_url"] = j.TicketURL
	}
	if j.Epic != "" {
		payload["epic"] = j.Epic
	}
	if j.Pipeline != "" {
		payload["pipeline"] = j.Pipeline
	}
	if step.Instance != "" {
		payload["step_instance"] = step.Instance
	}
	meta, wakeErr := r.deliverManagerPolicyWake(now, inst, managerOverdueWakeEventType, payload)
	j.LastEvent = managerOverdueWakeEventType
	j.LastStatus = message
	j.UpdatedAt = now
	data := map[string]string{
		"step":    step.ID,
		"age":     roundedManagerWakeDuration(age),
		"timeout": roundedManagerWakeDuration(timeout),
	}
	if step.Instance != "" {
		data["instance"] = step.Instance
	}
	if err := r.writeJobWithAudit(j, managerOverdueWakeEventType, "daemon", message, data); err != nil {
		return ManagerWakeupResult{}, err
	}
	_ = writeManagerOverdueWakeState(r.mgr.daemonRoot, &managerOverdueWakeState{
		JobID:          j.ID,
		StepID:         step.ID,
		StartedAt:      startedAt,
		Timeout:        timeout.String(),
		LastNotifiedAt: now,
	})
	_ = AppendLifecycleEvent(r.mgr.daemonRoot, &LifecycleEvent{
		Action:   managerOverdueWakeLifecycleAction,
		Instance: inst.Name,
		Agent:    inst.Agent,
		Job:      j.ID,
		Ticket:   j.Ticket,
		Status:   StatusRunning,
		Message:  message,
	})
	result := ManagerWakeupResult{Manager: inst.Name, JobID: j.ID, StepID: step.ID, Action: "dispatched"}
	if meta != nil {
		result.Instance = meta.Instance
	}
	if wakeErr != nil {
		result.Action = "failed"
		result.Reason = wakeErr.Error()
	}
	return result, nil
}

func (r *EventResolver) deliverManagerPolicyWake(now time.Time, inst *topology.Instance, eventType string, payload map[string]any) (*Metadata, error) {
	body := map[string]any{"event": eventType, "payload": payload}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	msg := &Message{From: "daemon", To: inst.Name, Body: string(encoded), TS: now}
	if err := AppendMessage(r.mgr.daemonRoot, inst.Name, msg); err != nil {
		return nil, err
	}
	return r.wakePersistent(inst, eventType, payload)
}

func readManagerIdleWakeState(daemonRoot, instance string) (*managerIdleWakeState, error) {
	var state managerIdleWakeState
	if err := readManagerWakeState(filepath.Join(managerWakeStateRoot(daemonRoot), "idle", safeManagerWakeName(instance)+".json"), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func writeManagerIdleWakeState(daemonRoot string, state *managerIdleWakeState) error {
	if state == nil {
		return errors.New("manager wake state: nil idle state")
	}
	return writeManagerWakeState(filepath.Join(managerWakeStateRoot(daemonRoot), "idle", safeManagerWakeName(state.Instance)+".json"), state)
}

func readManagerOverdueWakeState(daemonRoot, jobID, stepID string) (*managerOverdueWakeState, error) {
	var state managerOverdueWakeState
	if err := readManagerWakeState(managerOverdueWakeStatePath(daemonRoot, jobID, stepID), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func writeManagerOverdueWakeState(daemonRoot string, state *managerOverdueWakeState) error {
	if state == nil {
		return errors.New("manager wake state: nil overdue state")
	}
	return writeManagerWakeState(managerOverdueWakeStatePath(daemonRoot, state.JobID, state.StepID), state)
}

func managerOverdueWakeStatePath(daemonRoot, jobID, stepID string) string {
	name := safeManagerWakeName(jobstore.NormalizeID(jobID)) + "--" + safeManagerWakeName(stepID) + ".json"
	return filepath.Join(managerWakeStateRoot(daemonRoot), "overdue", name)
}

func managerWakeStateRoot(daemonRoot string) string {
	return filepath.Join(daemonRoot, "manager-wake")
}

func readManagerWakeState(path string, v any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("manager wake state: parse %s: %w", path, err)
	}
	return nil
}

func writeManagerWakeState(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func safeManagerWakeName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func roundedManagerWakeDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= time.Second {
		return d.Round(time.Second).String()
	}
	return d.String()
}
