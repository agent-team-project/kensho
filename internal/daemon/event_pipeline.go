package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/worktreepolicy"
)

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
	j.Epic = jobstore.EpicFromInputs(payloadString(payload, "epic"), j.TicketURL, j.Origin.Project)
	j.Kind = payloadJobKind(payload)
	j.Steps = pipelineJobSteps(pipeline)
	j.DeliveryContract = pipelineDeliveryArtifactContract(pipeline)
	if contract, ok := explicitPayloadDeliveryArtifactContract(payload); ok {
		j.DeliveryContract = contract
	} else if contract := payloadDeliveryArtifactContract(payload); contract != "" {
		j.DeliveryContract = contract
	}
	j.Contract = r.compileContractForPayload(j, payload)
	jobstore.SetImplementationAgentFromSteps(j)
	applyProbeProfileToPipelineJob(j)
	if jobIsProbe(j) {
		j.DeliveryContract = ""
		j.Contract = nil
	}
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
	if j.Epic != "" {
		data["epic"] = j.Epic
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
	r.hydratePipelineJob(j, pipeline, payload)
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
	r.hydratePipelineJob(j, pipeline, payload)
	if !jobStatusTerminal(j.Status) {
		return r.pipelineReentryNoop(pipeline, eventType, j, now, "job "+j.ID+" already "+string(j.Status))
	}
	if !pipeline.RedispatchOnReentry {
		return r.pipelineReentryNoop(pipeline, eventType, j, now, "job "+j.ID+" already "+string(j.Status)+"; redispatch_on_reentry=false")
	}
	r.resetPipelineJobForReentry(j, pipeline, eventType, payload, now)
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

func (r *EventResolver) hydratePipelineJob(j *jobstore.Job, pipeline *topology.Pipeline, payload map[string]any) {
	j.Pipeline = pipeline.Name
	if team := r.teamForPipeline(pipeline.Name); team != "" {
		j.Origin.Team = team
	}
	if kind := payloadJobKind(payload); kind != "" {
		j.Kind = kind
	}
	if jobIsProbe(j) {
		j.DeliveryContract = ""
	} else if contract, ok := explicitPayloadDeliveryArtifactContract(payload); ok {
		j.DeliveryContract = contract
	} else if contract := payloadDeliveryArtifactContract(payload); contract != "" {
		j.DeliveryContract = contract
	} else if contract := pipelineDeliveryArtifactContract(pipeline); contract != "" {
		j.DeliveryContract = contract
	}
	if pipeline.ReapWorktree != worktreepolicy.Never && strings.TrimSpace(j.ReapWorktree) == "" {
		j.ReapWorktree = pipeline.ReapWorktree
	}
	if j.Merge == nil {
		j.Merge = jobMergeFromPipeline(pipeline.Merge)
	}
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		j.TicketURL = ticketURL
	}
	j.Epic = jobstore.EpicFromInputs(payloadString(payload, "epic"), j.TicketURL, j.Origin.Project)
	if len(j.Steps) == 0 {
		j.Steps = pipelineJobSteps(pipeline)
	}
	if j.Contract == nil {
		j.Contract = r.compileContractForPayload(j, payload)
	}
	jobstore.SetImplementationAgentFromSteps(j)
	applyProbeProfileToPipelineJob(j)
	if jobIsProbe(j) {
		j.Contract = nil
	}
}

func (r *EventResolver) resetPipelineJobForReentry(j *jobstore.Job, pipeline *topology.Pipeline, eventType string, payload map[string]any, now time.Time) {
	r.hydratePipelineJob(j, pipeline, payload)
	j.Target = pipeline.Steps[0].Target
	j.Kickoff = pipelineKickoff(eventType, payload)
	j.Status = jobstore.StatusQueued
	j.Held = false
	j.HoldReason = ""
	j.HoldUntil = time.Time{}
	j.DeliveryContract = pipelineDeliveryArtifactContract(pipeline)
	if contract, ok := explicitPayloadDeliveryArtifactContract(payload); ok {
		j.DeliveryContract = contract
	} else if contract := payloadDeliveryArtifactContract(payload); contract != "" {
		j.DeliveryContract = contract
	}
	j.Instance = ""
	j.Branch = ""
	j.Worktree = ""
	j.PR = ""
	j.Drift = nil
	j.Steps = pipelineJobSteps(pipeline)
	jobstore.SetImplementationAgentFromSteps(j)
	applyProbeProfileToPipelineJob(j)
	if jobIsProbe(j) {
		j.DeliveryContract = ""
		j.Contract = nil
	} else {
		j.Contract = r.compileContractForPayload(j, payload)
	}
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
	if j != nil && j.Epic != "" {
		data["epic"] = j.Epic
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
	dispatchPayload["attempt"] = jobstore.CurrentAttempt(j)
	dispatchPayload["ticket"] = j.Ticket
	if head := strings.TrimSpace(j.Head); head != "" {
		dispatchPayload["head"] = head
	}
	if !jobIsProbe(j) {
		payloadSetStringIfEmpty(dispatchPayload, "branch", strings.TrimSpace(j.Branch))
		payloadSetStringIfEmpty(dispatchPayload, "worktree", strings.TrimSpace(j.Worktree))
	}
	if kind := strings.TrimSpace(j.Kind); kind != "" {
		dispatchPayload["kind"] = kind
	}
	if strings.EqualFold(strings.TrimSpace(j.DeliveryContract), "none") {
		dispatchPayload["deliverable"] = "none"
	} else if contract := normalizeDeliveryArtifactContract(j.DeliveryContract); contract != "" {
		dispatchPayload["deliverable"] = contract
		if path := deliveryReportArtifactPath(contract); path != "" {
			dispatchPayload["report_path"] = path
		}
	}
	if jobIsProbe(j) || payloadIsProbe(dispatchPayload) {
		dispatchPayload["kind"] = jobstore.KindProbe
		dispatchPayload["workspace"] = "repo"
	}
	if !payloadIsProbe(dispatchPayload) && payloadString(dispatchPayload, "workspace") == "" && strings.TrimSpace(step.Workspace) != "" {
		dispatchPayload["workspace"] = strings.TrimSpace(step.Workspace)
	}
	if runtime := strings.TrimSpace(step.Runtime); runtime != "" {
		dispatchPayload["runtime"] = runtime
	}
	if runtimeBin := strings.TrimSpace(step.RuntimeBin); runtimeBin != "" {
		dispatchPayload["runtime_binary"] = runtimeBin
	}
	if model := strings.TrimSpace(step.Model); model != "" {
		dispatchPayload["model"] = model
	}
	if effort := strings.TrimSpace(step.Effort); effort != "" {
		dispatchPayload["effort"] = effort
	}
	if payloadString(dispatchPayload, "reap_worktree") == "" && pipeline.ReapWorktree != worktreepolicy.Never {
		dispatchPayload["reap_worktree"] = pipeline.ReapWorktree
	}
	// Thread the step's runtime budget through so the ephemeral spawn arms a
	// per-instance watchdog (a hung worker/reviewer otherwise holds a replica
	// slot forever). A zero step timeout leaves it to the env-level default.
	if ts := pipelineStepTimeoutString(step.Timeout); ts != "" {
		dispatchPayload["timeout"] = ts
	}
	applyPipelineStepBudgetToPayload(step, dispatchPayload)
	kickoff := jobstore.StepDispatchKickoffWithContract(j.Kickoff, step.ID, step.Instructions, j.Contract)
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
	if payloadString(payload, "source") == "schedule" {
		delete(dispatchPayload, "name")
	}
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
			Workspace:        step.Workspace,
			Status:           status,
			Runtime:          step.Runtime,
			RuntimeBin:       step.RuntimeBin,
			Model:            step.Model,
			Effort:           step.Effort,
			After:            append([]string(nil), step.After...),
			Gate:             step.Gate,
			ApprovalRequired: step.ApprovalRequired,
			Optional:         step.Optional,
			Timeout:          pipelineStepTimeoutString(step.Timeout),
			TokenBudget:      step.TokenBudget,
			TimeBudget:       pipelineStepTimeoutString(step.TimeBudget),
			HardBudget:       step.HardBudget,
			HardMultiplier:   step.HardMultiplier,
			ReminderLevels:   append([]int(nil), step.ReminderLevels...),
			MaxAttempts:      step.MaxAttempts,
			RetryOnCrash:     step.RetryOnCrash,
		})
	}
	return steps
}

func payloadJobKind(payload map[string]any) string {
	for _, key := range []string{"kind", "profile"} {
		if kind, err := jobstore.NormalizeKind(payloadString(payload, key)); err == nil && kind != "" {
			return kind
		}
	}
	return ""
}

func payloadIsProbe(payload map[string]any) bool {
	return jobstore.IsProbe(payloadJobKind(payload))
}

func jobIsProbe(j *jobstore.Job) bool {
	return j != nil && jobstore.IsProbe(j.Kind)
}

func applyProbeProfileToPipelineJob(j *jobstore.Job) {
	if !jobIsProbe(j) || len(j.Steps) <= 1 {
		return
	}
	now := j.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for i := 1; i < len(j.Steps); i++ {
		step := &j.Steps[i]
		if step.Status == jobstore.StatusDone && step.Skipped {
			continue
		}
		step.Status = jobstore.StatusDone
		step.Skipped = true
		step.SkipReason = jobstore.ProbeSkipReason
		if step.StartedAt.IsZero() {
			step.StartedAt = now
		}
		if step.FinishedAt.IsZero() {
			step.FinishedAt = now
		}
	}
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
		prevStep.InstanceURI = ""
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
	j, err := jobstore.Read(r.teamDir, jobID)
	if err != nil {
		return false
	}
	for _, record := range jobstore.LatestGateRecordsForAttemptHead(records, jobstore.CurrentAttempt(j), j.Head) {
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

func reconcilePipelineStepExit(j *jobstore.Job, instance string, attempt int, head string, status jobstore.Status, now time.Time) (*jobstore.Step, bool) {
	if j == nil || instance == "" {
		return nil, false
	}
	if !jobstore.AttemptHeadMatches(j, attempt, head) {
		return nil, false
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Instance != instance {
			continue
		}
		if step.Status != jobstore.StatusRunning && step.Status != jobstore.StatusQueued {
			// Step-owned commands can persist a terminal result before the runtime
			// exits. Return the owner without mutating that authoritative result so
			// the reaper can derive the outer status from the full graph.
			return step, false
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
		return step, true
	}
	return nil, false
}

func allPipelineStepsDone(j *jobstore.Job) bool {
	if j == nil || len(j.Steps) == 0 {
		return false
	}
	for _, step := range j.Steps {
		if !jobstore.StepSatisfiesDependency(&step) {
			return false
		}
	}
	return true
}

func pipelineStatusFromSteps(j *jobstore.Job) jobstore.Status {
	if j == nil || len(j.Steps) == 0 {
		return jobstore.StatusRunning
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if !step.Optional && step.Status == jobstore.StatusFailed {
			return jobstore.StatusFailed
		}
	}
	if allPipelineStepsDone(j) {
		return jobstore.StatusDone
	}
	return jobstore.StatusRunning
}
