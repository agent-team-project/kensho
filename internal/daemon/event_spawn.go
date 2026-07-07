package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/loader"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/resource"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/runtimehooks"
	"github.com/agent-team-project/agent-team/internal/runtimeotel"
	"github.com/agent-team-project/agent-team/internal/runtimeshim"
	teamtemplate "github.com/agent-team-project/agent-team/internal/template"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/agent-team-project/agent-team/internal/worktreepolicy"
)

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
	payload = copyPayload(payload)
	applyInstanceBudgetDefaultsToPayload(inst, payload)
	r.mu.Lock()
	top := r.topo
	r.mu.Unlock()
	applyTopologyReminderDefaultsToPayload(top, payload)
	runtime, err := r.prepareEphemeralRuntime(inst, name)
	if err != nil {
		return nil, err
	}
	workspace := r.teamDirParent()
	worktreePath := ""
	branch := ""
	cleanupWorkspace := func() {}
	if !payloadIsProbe(payload) && (payloadString(payload, "workspace") == "worktree" || payloadString(payload, "isolation") == "worktree") {
		workspace, branch, cleanupWorkspace, err = r.prepareEphemeralWorktree(name, payload)
		if err != nil {
			return nil, err
		}
		worktreePath = workspace
	}
	r.backfillDispatchPayloadResourceURIs(inst, name, payload, branch, workspace)
	eventOrigin := r.originForEvent(inst, name, eventType, payload)
	body, _ := json.Marshal(map[string]any{"event": eventType, "payload": payload})
	prompt := fmt.Sprintf("Topology event for declared instance %q (agent=%s):\n%s",
		inst.Name, inst.Agent, string(body))
	prompt = prependProbeKickoffPreamble(prompt, payload)
	prompt, err = r.appendUnreadMailboxToPrompt(name, inst.Agent, prompt, payload, branch)
	if err != nil {
		cleanupWorkspace()
		return nil, err
	}
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
	args, stdin, rt, env, err := r.prepareEphemeralAgentArgs(inst, inst.Agent, name, runtime.stateDir, workspace, prompt, env, inst.EnvAllow, runtime.mailboxInjection, payload, runtime.otelConfig, otelCtx, traceparent)
	if err != nil {
		cleanupWorkspace()
		return nil, err
	}
	meta, err := r.mgr.Dispatch(DispatchInput{
		Agent:               inst.Agent,
		Name:                name,
		URI:                 payloadString(payload, "instance_uri"),
		SpecURI:             payloadString(payload, "spec_uri"),
		DeploymentURI:       payloadString(payload, "deployment_uri"),
		DeploymentParentURI: payloadString(payload, "deployment_parent_uri"),
		Job:                 eventJobID(payload),
		JobURI:              payloadString(payload, "job_uri"),
		Ticket:              payloadString(payload, "ticket"),
		Branch:              branch,
		PR:                  firstPayloadString(payload, "pr_url", "pr"),
		Origin:              eventOrigin,
		Workspace:           workspace,
		WorkspaceURI:        payloadString(payload, "workspace_uri"),
		StateURI:            payloadString(payload, "state_uri"),
		Runtime:             string(rt.Kind),
		RuntimeBinary:       rt.Binary,
		Args:                args,
		Env:                 env,
		EnvAllow:            inst.EnvAllow,
		StripOTelEnv:        runtime.otelConfig.Configured(),
		Stdin:               stdin,
		Budget:              ephemeralRuntimeBudget(payload),
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

func prependProbeKickoffPreamble(prompt string, payload map[string]any) string {
	if !payloadIsProbe(payload) {
		return prompt
	}
	return "## Probe job\n\n" +
		"This dispatch is kind=probe. Use the reduced contract: report findings to the state summary and inbox, do not open a PR, do not transition the ticket, do not create or use a branch, and do not run delivery gates or reviewer handoff.\n\n" +
		prompt
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
	applyOwnership := func(dst *Metadata) {
		if dst == nil {
			return
		}
		dst.Job = eventJobID(payload)
		dst.Ticket = payloadString(payload, "ticket")
		dst.Branch = branch
		dst.PR = firstPayloadString(payload, "pr_url", "pr")
		dst.Origin = origin.Merge(dst.Origin, r.originForPayload(dst.Instance, payload))
		if uri := payloadString(payload, "deployment_uri"); uri != "" {
			dst.DeploymentURI = uri
		}
		if uri := payloadString(payload, "deployment_parent_uri"); uri != "" {
			dst.DeploymentParentURI = uri
		}
		if uri := payloadString(payload, "instance_uri"); uri != "" {
			dst.URI = uri
		}
		if uri := payloadString(payload, "spec_uri"); uri != "" {
			dst.SpecURI = uri
		}
		if uri := payloadString(payload, "job_uri"); uri != "" {
			dst.JobURI = uri
		}
		if uri := payloadString(payload, "workspace_uri"); uri != "" {
			dst.WorkspaceURI = uri
		}
		if uri := payloadString(payload, "state_uri"); uri != "" {
			dst.StateURI = uri
		}
	}
	current := *meta
	if latest, err := ReadMetadata(r.mgr.daemonRoot, meta.Instance); err == nil && latest.PID == meta.PID {
		current = *latest
	}
	applyOwnership(&current)
	j := r.upsertDispatchJob(payload, current.Instance, jobstore.StatusRunning, "dispatched", "running", branch, worktreePath)
	if j != nil {
		current.Job = j.ID
		current.Ticket = j.Ticket
		current.PR = j.PR
		if stepID, ok := linearDispatchStepFromPayload(payload); ok && !jobIsProbe(j) {
			r.writeLinearDispatchInProgress(j, stepID)
		}
	}
	r.mgr.mu.Lock()
	if t, ok := r.mgr.instances[meta.Instance]; ok && t.meta != nil && t.meta.PID == meta.PID {
		applyOwnership(t.meta)
		if j != nil {
			t.meta.Job = j.ID
			t.meta.Ticket = j.Ticket
			t.meta.PR = j.PR
		}
		current = *t.meta
	}
	err := WriteMetadata(r.mgr.daemonRoot, &current)
	r.mgr.mu.Unlock()
	if err != nil {
		return
	}
	*meta = current
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
	for _, key := range []string{"target", "agent", "pipeline", "pipeline_step", "ticket", "ticket_url", "epic", "kind", "profile", "team", "runtime", "runtime_binary", "deployment_uri", "deployment_parent_uri", "charter_uri", "child_deployment_uri", "capability_uri", "instance_uri", "spec_uri", "job_uri", "workspace_uri", "state_uri"} {
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

func (r *EventResolver) backfillDispatchPayloadResourceURIs(inst *topology.Instance, instance string, payload map[string]any, branch, workspace string) {
	if payload == nil {
		return
	}
	deployment, _ := resource.DeploymentFromTeamDir(r.teamDir)
	deploymentID := strings.TrimSpace(deployment.ID)
	if deploymentID == "" {
		return
	}
	payloadSetStringIfEmpty(payload, "deployment_uri", deployment.URI)
	payloadSetStringIfEmpty(payload, "deployment_parent_uri", deployment.ParentURI)
	payloadSetStringIfEmpty(payload, "instance_uri", resource.InstanceURI(deploymentID, instance))
	declared := instance
	if inst != nil && strings.TrimSpace(inst.Name) != "" {
		declared = inst.Name
	}
	specURI := resource.InstanceURI(deploymentID, declared)
	if jobID := eventJobID(payload); jobID != "" {
		payloadSetStringIfEmpty(payload, "job_uri", resource.JobURI(deploymentID, jobID))
		if stepID := payloadString(payload, "pipeline_step"); stepID != "" {
			specURI = resource.StepURI(deploymentID, jobID, stepID)
		}
	}
	payloadSetStringIfEmpty(payload, "spec_uri", specURI)
	workspaceURI := resource.WorkspaceURIFor(deploymentID, workspace, branch, eventJobID(payload), instance)
	if payloadString(payload, "workspace") == "repo" {
		workspaceURI = resource.WorkspaceURI(deploymentID, "repo")
	}
	payloadSetStringIfEmpty(payload, "workspace_uri", workspaceURI)
	payloadSetStringIfEmpty(payload, "state_uri", resource.StateURI(deploymentID, instance))
}

func payloadSetStringIfEmpty(payload map[string]any, key, value string) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(payloadString(payload, key)) != "" {
		return
	}
	payload[key] = value
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
	agent := ""
	if inst != nil {
		declared = inst.Name
		// The origin agent is the resolved agent TEMPLATE (e.g. "reviewer"),
		// never the dispatch target alias (e.g. "platform-reviewer") — the
		// shipped authority allowlists are keyed by agent type, and payload
		// fields must not influence identity (same rule as origin team).
		agent = inst.Agent
	}
	if agent == "" {
		agent = firstPayloadString(payload, "agent", "target")
	}
	return origin.Envelope{
		Project:  projectIDForTeamDir(r.teamDir),
		Team:     r.teamForOrigin(declared, payload),
		Instance: instance,
		Agent:    agent,
		Job:      eventJobID(payload),
		Trigger:  origin.TriggerFromEvent(eventType, payload),
		Build:    buildinfo.Current("").Display(),
	}.WithResourceURIs()
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
	}.WithResourceURIs()
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
	if env.DeploymentURI != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_DEPLOYMENT_URI="+env.DeploymentURI)
	}
	if env.Team != "" {
		out = append(out, "AGENT_TEAM_TEAM="+env.Team)
	}
	if env.InstanceURI != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_INSTANCE_URI="+env.InstanceURI)
	}
	if env.JobURI != "" {
		out = append(out, "AGENT_TEAM_ORIGIN_JOB_URI="+env.JobURI)
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
	if uri := payloadString(payload, "job_uri"); uri != "" {
		env = append(env, "AGENT_TEAM_JOB_URI="+uri)
	}
	if kind := payloadJobKind(payload); kind != "" {
		env = append(env, "AGENT_TEAM_JOB_KIND="+kind)
	}
	if deliverable := normalizeDeliveryArtifactContract(firstPayloadString(payload, "deliverable", "delivery_contract")); deliverable != "" {
		env = append(env, "AGENT_TEAM_DELIVERABLE="+deliverable)
		if path := deliveryReportArtifactPath(deliverable); path != "" {
			env = append(env, "AGENT_TEAM_REPORT_PATH="+path)
		}
	}
	if ticket := payloadString(payload, "ticket"); ticket != "" {
		env = append(env, "AGENT_TEAM_TICKET="+ticket)
	}
	if ticketURL := payloadString(payload, "ticket_url"); ticketURL != "" {
		env = append(env, "AGENT_TEAM_TICKET_URL="+ticketURL)
	}
	if epic := payloadString(payload, "epic"); epic != "" {
		env = append(env, "AGENT_TEAM_EPIC="+epic)
	}
	if pipeline := payloadString(payload, "pipeline"); pipeline != "" {
		env = append(env, "AGENT_TEAM_PIPELINE="+pipeline)
	}
	if step := payloadString(payload, "pipeline_step"); step != "" {
		env = append(env, "AGENT_TEAM_PIPELINE_STEP="+step)
	}
	if tokens := payloadBudgetTokens(payload); tokens > 0 {
		env = append(env, fmt.Sprintf("AGENT_TEAM_BUDGET_TOKENS=%d", tokens))
	}
	if timeBudget := payloadString(payload, "budget_time"); timeBudget != "" {
		env = append(env, "AGENT_TEAM_BUDGET_TIME="+timeBudget)
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
	for _, item := range []struct {
		key string
		env string
	}{
		{"deployment_uri", "AGENT_TEAM_DEPLOYMENT_URI"},
		{"deployment_parent_uri", "AGENT_TEAM_DEPLOYMENT_PARENT_URI"},
		{"charter_uri", "AGENT_TEAM_CHARTER_URI"},
		{"child_deployment_uri", "AGENT_TEAM_CHILD_DEPLOYMENT_URI"},
		{"capability_uri", "AGENT_TEAM_CAPABILITY_URI"},
		{"instance_uri", "AGENT_TEAM_INSTANCE_URI"},
		{"spec_uri", "AGENT_TEAM_SPEC_URI"},
		{"workspace_uri", "AGENT_TEAM_WORKSPACE_URI"},
		{"state_uri", "AGENT_TEAM_STATE_URI"},
	} {
		if value := payloadString(payload, item.key); value != "" {
			env = append(env, item.env+"="+value)
		}
	}
	return env
}

type ephemeralRuntime struct {
	stateDir         string
	env              []string
	mailboxInjection bool
	otelConfig       runtimeotel.Config
}

// authorityForInstance resolves the closed-world verb allowlist baked into the
// generated shim. enforce is true only when topology explicitly opts into
// authority enforcement; audit mode keeps the shim pass-through.
func (r *EventResolver) authorityForInstance(agentName, instance string) (allow []string, enforce bool) {
	r.mu.Lock()
	topo := r.topo
	r.mu.Unlock()
	if topo == nil || topo.Authority == nil || !topo.Authority.Enforced() {
		return nil, false
	}
	return topo.AuthorityAllowlistForInstance(instance, strings.TrimSpace(agentName)), true
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
	tokenFile, err := EnsureInstanceToken(r.teamDir, name)
	if err != nil {
		return nil, fmt.Errorf("event runtime: daemon token: %w", err)
	}
	env = append(env, DaemonTokenFileEnv+"="+tokenFile)
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

func (r *EventResolver) prepareEphemeralAgentArgs(inst *topology.Instance, agentName, instance, stateDir, cwd, prompt string, env []string, envAllow []string, mailboxInjection bool, payload map[string]any, otelCfg runtimeotel.Config, otelCtx runtimeotel.Context, traceparent string) ([]string, string, runtimebin.Runtime, []string, error) {
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
	rt, err := r.runtimeForAgent(chosen, inst, payload)
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
	if rt.Kind != runtimebin.KindDocker {
		if strings.TrimSpace(traceparent) != "" {
			otelLaunch, err = runtimeotel.BuildLaunchWithTraceparent(otelCfg, rt.Kind, otelCtx, traceparent)
		} else {
			otelLaunch, err = runtimeotel.BuildLaunch(otelCfg, rt.Kind, otelCtx)
		}
		if err != nil {
			return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: %w", err)
		}
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
	authorityAllow, authorityEnforce := r.authorityForInstance(agentName, instance)
	shimBinDir, err := runtimeshim.InstallWithOptions(runtimeDir, skillPaths, runtimeshim.Options{
		EnforceAuthority:   authorityEnforce,
		AuthorityAllowlist: authorityAllow,
	})
	if err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: %w", err)
	}
	env = runtimeshim.PrependPath(env, shimBinDir)
	env, err = filterEnvAllow(env, envAllow)
	if err != nil {
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("event runtime: %w", err)
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
	case runtimebin.KindDocker:
		args, err := r.prepareDockerAgentArgs(rt, agentName, instance, stateDir, cwd, prompt, env)
		if err != nil {
			return nil, "", runtimebin.Runtime{}, nil, err
		}
		return args, "", rt, env, nil
	default:
		return nil, "", runtimebin.Runtime{}, nil, fmt.Errorf("unsupported runtime %q", rt.Kind)
	}
}

// runtimeForAgent resolves an ephemeral instance's runtime with the same
// precedence as the dispatch path: explicit payload runtime > AGENT_TEAM_RUNTIME
// env override > declared instance `runtime:`/`runtime_bin:` > agent
// frontmatter `runtime:`/`runtime_bin:` > repo [runtime] config > default.
func (r *EventResolver) runtimeForAgent(agent *loader.Agent, inst *topology.Instance, payload map[string]any) (runtimebin.Runtime, error) {
	opts := runtimebin.ResolveOptions{
		Explicit: runtimebin.Fields{
			Kind:   firstPayloadString(payload, "runtime"),
			Binary: firstPayloadString(payload, "runtime_binary", "runtime_bin"),
		},
		ConfigPath: filepath.Join(r.teamDir, "config.toml"),
	}
	if inst != nil {
		opts.Instance = runtimebin.Fields{Name: inst.Name, Kind: inst.Runtime, Binary: inst.RuntimeBin}
	}
	if agent != nil {
		opts.Agent = runtimebin.Fields{Name: agent.Name, Kind: agent.Runtime, Binary: agent.RuntimeBin}
	}
	return runtimebin.Resolve(opts)
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

func (r *EventResolver) prepareEphemeralWorktree(instance string, payload map[string]any) (string, string, func(), error) {
	repoRoot := r.teamDirParent()
	if repoRoot == "" {
		return "", "", nil, errors.New("event worktree: repo root is required")
	}
	tag := newSessionID()[0:8]
	ticket := payloadString(payload, "ticket")
	branch := ephemeralWorktreeBranch(instance, ticket, tag)
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", instance+"-"+tag)
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", "", nil, fmt.Errorf("event worktree: create parent: %w", err)
	}
	baseRef := r.ephemeralWorktreeBaseRef(repoRoot, payload)
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "-b", branch, worktreePath, baseRef)
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

func (r *EventResolver) ephemeralWorktreeBaseRef(repoRoot string, payload map[string]any) string {
	const fallback = "HEAD"
	jobID := eventJobID(payload)
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(r.teamDir) == "" {
		return fallback
	}
	j, err := jobstore.Read(r.teamDir, jobID)
	if err != nil || j == nil {
		return fallback
	}
	if ref := existingJobBranchRef(repoRoot, j.Branch); ref != "" {
		return ref
	}
	if ref := existingWorktreeHead(j.Worktree); ref != "" {
		return ref
	}
	return fallback
}

func existingJobBranchRef(repoRoot, branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return ""
	}
	for _, ref := range []string{branch, "origin/" + branch} {
		if gitCommitRefExists(repoRoot, ref) {
			return ref
		}
	}
	return ""
}

func gitCommitRefExists(repoRoot, ref string) bool {
	if strings.TrimSpace(repoRoot) == "" || strings.TrimSpace(ref) == "" {
		return false
	}
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", ref+"^{commit}")
	return cmd.Run() == nil
}

func existingWorktreeHead(worktree string) string {
	worktree = strings.TrimSpace(worktree)
	if worktree == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "--verify", "HEAD^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
