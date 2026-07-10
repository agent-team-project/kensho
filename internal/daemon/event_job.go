package daemon

import (
	"os"
	"strings"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/worktreepolicy"
)

func (r *EventResolver) contractFromPayload(payload map[string]any) *jobstore.Contract {
	if payload == nil || payloadIsProbe(payload) {
		return nil
	}
	if existing := r.existingPayloadContract(payload); existing != nil {
		return existing
	}
	ticket := payloadString(payload, "ticket")
	id := eventJobID(payload)
	if ticket == "" {
		ticket = id
	}
	if ticket == "" {
		return nil
	}
	target := firstPayloadString(payload, "target", "agent")
	if target == "" {
		target = "worker"
	}
	j, err := jobstore.New(ticket, target, payloadString(payload, "kickoff"), time.Now().UTC())
	if err != nil {
		return nil
	}
	if id != "" {
		j.ID = id
	}
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		j.TicketURL = ticketURL
	}
	j.Epic = jobstore.EpicFromInputs(payloadString(payload, "epic"), j.TicketURL, j.Origin.Project)
	if kind := payloadJobKind(payload); kind != "" {
		j.Kind = kind
	}
	if contract, ok := explicitPayloadDeliveryArtifactContract(payload); ok {
		j.DeliveryContract = contract
	} else if contract := payloadDeliveryArtifactContract(payload); contract != "" {
		j.DeliveryContract = contract
	}
	return r.compileContractForPayload(j, payload)
}

func (r *EventResolver) existingPayloadContract(payload map[string]any) *jobstore.Contract {
	if strings.TrimSpace(r.teamDir) == "" {
		return nil
	}
	id := eventJobID(payload)
	if id == "" {
		return nil
	}
	j, err := jobstore.Read(r.teamDir, id)
	if err != nil || j == nil || j.Contract == nil {
		return nil
	}
	return j.Contract
}

func (r *EventResolver) compileContractForPayload(j *jobstore.Job, payload map[string]any) *jobstore.Contract {
	if j == nil || payloadIsProbe(payload) {
		return nil
	}
	return jobstore.CompileContract(j, jobstore.ContractCompileOptions{
		Text:            payloadString(payload, "kickoff"),
		RequiredTrailer: firstPayloadString(payload, "required_trailer", "trailer"),
		ExplicitEpic:    payloadString(payload, "epic"),
		Gates:           firstPayloadString(payload, "contract_gates", "gates"),
	})
}

func (r *EventResolver) upsertDispatchJob(payload map[string]any, instance string, status jobstore.Status, lastEvent, lastStatus, branch, worktreePath string) *jobstore.Job {
	return r.upsertDispatchJobWithContract(payload, instance, status, lastEvent, lastStatus, branch, worktreePath, nil)
}

func (r *EventResolver) upsertDispatchJobWithContract(payload map[string]any, instance string, status jobstore.Status, lastEvent, lastStatus, branch, worktreePath string, compiledContract *jobstore.Contract) *jobstore.Job {
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
	if status != "" && !jobStatusTerminal(status) && jobStatusTerminal(j.Status) {
		return j
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
	if uri := payloadString(payload, "job_uri"); uri != "" {
		j.URI = uri
	}
	if uri := payloadString(payload, "deployment_uri"); uri != "" {
		j.DeploymentURI = uri
	}
	if uri := payloadString(payload, "deployment_parent_uri"); uri != "" {
		j.DeploymentParentURI = uri
	}
	if uri := payloadString(payload, "instance_uri"); uri != "" {
		j.InstanceURI = uri
	}
	if ticket != "" {
		j.Ticket = ticket
	}
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		j.TicketURL = ticketURL
	}
	j.Epic = jobstore.EpicFromInputs(payloadString(payload, "epic"), j.TicketURL, j.Origin.Project)
	if kind := payloadJobKind(payload); kind != "" {
		j.Kind = kind
	}
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		j.Pipeline = pipeline
	}
	if payloadIsProbe(payload) {
		j.DeliveryContract = ""
	} else if contract, ok := explicitPayloadDeliveryArtifactContract(payload); ok {
		j.DeliveryContract = contract
	} else if contract := payloadDeliveryArtifactContract(payload); contract != "" {
		j.DeliveryContract = contract
	}
	if j.Contract == nil {
		if compiledContract != nil {
			j.Contract = compiledContract
		} else {
			j.Contract = r.compileContractForPayload(j, payload)
		}
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
	if uri := payloadString(payload, "workspace_uri"); uri != "" {
		j.WorkspaceURI = uri
	}
	if pr := firstPayloadString(payload, "pr_url", "pr"); pr != "" {
		j.PR = pr
	}
	applyPayloadBudgetToJob(j, payload)
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
	applyProbeProfileToPipelineJob(j)
	j.UpdatedAt = now
	if status != "" && !jobStatusTerminal(status) {
		if latest, err := jobstore.Read(r.teamDir, j.ID); err == nil && jobStatusTerminal(latest.Status) {
			return latest
		}
	}
	if err := r.writeJobWithAudit(j, "", "daemon", "", dispatchJobEventData(payload, branch, worktreePath)); err != nil {
		return nil
	}
	return j
}

func dispatchJobEventData(payload map[string]any, branch, worktreePath string) map[string]string {
	data := map[string]string{}
	for _, key := range []string{"target", "agent", "pipeline", "pipeline_step", "ticket", "ticket_url", "epic", "kind", "profile", "team", "runtime", "runtime_binary", "model", "deployment_uri", "deployment_parent_uri", "charter_uri", "child_deployment_uri", "capability_uri", "instance_uri", "spec_uri", "job_uri", "workspace_uri", "state_uri"} {
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
