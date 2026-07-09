package daemon

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/allowance"
	"github.com/agent-team-project/agent-team/internal/budget"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/topology"
)

func applyPipelineStepBudgetToPayload(step *topology.PipelineStep, payload map[string]any) {
	if step == nil || payload == nil {
		return
	}
	if step.TokenBudget > 0 && payloadBudgetTokens(payload) == 0 {
		payload["budget_tokens"] = step.TokenBudget
	}
	if step.TimeBudget > 0 && strings.TrimSpace(payloadString(payload, "budget_time")) == "" {
		payload["budget_time"] = step.TimeBudget.String()
	}
	if len(step.ReminderLevels) > 0 && payload["reminder_levels"] == nil {
		payload["reminder_levels"] = append([]int(nil), step.ReminderLevels...)
	}
	if step.HardBudget && !payloadBudgetHard(payload) {
		payload["budget_hard"] = true
	}
	if step.HardMultiplier > 0 && payloadBudgetHardMultiplier(payload) == 0 {
		payload["budget_hard_multiplier"] = step.HardMultiplier
	}
}

func applyInstanceBudgetDefaultsToPayload(inst *topology.Instance, payload map[string]any) {
	if inst == nil || payload == nil {
		return
	}
	if inst.TokenBudget > 0 && payloadBudgetTokens(payload) == 0 {
		payload["budget_tokens"] = inst.TokenBudget
	}
	if inst.TimeBudget > 0 && strings.TrimSpace(payloadString(payload, "budget_time")) == "" {
		payload["budget_time"] = inst.TimeBudget.String()
	}
	if inst.HardBudget && !payloadBudgetHard(payload) {
		payload["budget_hard"] = true
	}
	if inst.HardMultiplier > 0 && payloadBudgetHardMultiplier(payload) == 0 {
		payload["budget_hard_multiplier"] = inst.HardMultiplier
	}
}

func applyTopologyReminderDefaultsToPayload(top *topology.Topology, payload map[string]any) {
	if top == nil || payload == nil || len(top.ReminderLevels) == 0 || payload["reminder_levels"] != nil {
		return
	}
	if payloadBudgetTokens(payload) <= 0 && strings.TrimSpace(payloadString(payload, "budget_time")) == "" {
		return
	}
	payload["reminder_levels"] = append([]int(nil), top.ReminderLevels...)
}

func payloadBudgetTokens(payload map[string]any) int64 {
	if payload == nil {
		return 0
	}
	tokens, _ := allowance.ParseTokenValue(payload["budget_tokens"], "budget_tokens")
	return tokens
}

func payloadBudgetHard(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	for _, key := range []string{"budget_hard", "hard"} {
		if raw, ok := payload[key]; ok {
			if value, ok := raw.(bool); ok {
				return value
			}
		}
	}
	return false
}

func payloadBudgetHardMultiplier(payload map[string]any) float64 {
	if payload == nil {
		return 0
	}
	for _, key := range []string{"budget_hard_multiplier", "hard_multiplier"} {
		if raw, ok := payload[key]; ok {
			value, err := allowance.ParseHardMultiplierValue(raw, key)
			if err == nil {
				return value
			}
		}
	}
	return 0
}

func (r *EventResolver) grantPayloadBudgetLocked(payload map[string]any, eventOrigin origin.Envelope, instance string, now time.Time) (budget.GrantResult, error) {
	result := budget.GrantResult{Allowed: true, Noop: true, Team: strings.TrimSpace(eventOrigin.Team), GrantedTokens: payloadBudgetTokens(payload)}
	if payload == nil || strings.TrimSpace(eventOrigin.Team) == "" {
		return result, nil
	}
	requested := payloadBudgetTokens(payload)
	if requested <= 0 {
		return result, nil
	}
	instance = strings.TrimSpace(instance)
	if instance == "" {
		instance = eventOrigin.Instance
	}
	grant, err := budget.GrantTokens(r.teamDir, r.topo, budget.GrantRequest{
		Team:               eventOrigin.Team,
		JobID:              eventJobID(payload),
		StepID:             payloadString(payload, "pipeline_step"),
		Instance:           instance,
		Tokens:             requested,
		ClampOversubscribe: true,
		GateOversubscribe:  true,
		Now:                now,
		Origin:             eventOrigin,
	})
	if err != nil {
		return grant, err
	}
	if !grant.Allowed {
		return grant, nil
	}
	if grant.GrantedTokens > 0 && grant.GrantedTokens != requested {
		payload["budget_tokens"] = grant.GrantedTokens
	}
	if grant.Clamped {
		r.recordBudgetClampEvent(payload, eventOrigin, requested, grant.GrantedTokens)
	}
	return grant, nil
}

func (r *EventResolver) recordBudgetClampEvent(payload map[string]any, eventOrigin origin.Envelope, requested, clamped int64) {
	jobID := eventJobID(payload)
	if jobID == "" || strings.TrimSpace(r.teamDir) == "" {
		return
	}
	message := fmt.Sprintf("token allowance clamped from %d to %d by team %s headroom", requested, clamped, eventOrigin.Team)
	_ = jobstore.AppendEvent(r.teamDir, &jobstore.Event{
		JobID:   jobID,
		Type:    "budget_clamped",
		Message: message,
		Actor:   "daemon",
		Origin:  eventOrigin,
		Data: map[string]string{
			"team":             eventOrigin.Team,
			"requested_tokens": fmt.Sprint(requested),
			"clamped_tokens":   fmt.Sprint(clamped),
		},
	})
}

// envInstanceMaxRuntime is the daemon-wide default runtime budget for ephemeral
// instances (workers/reviewers/spawned steps). It opts the whole deployment into
// the per-instance watchdog without per-step config — the backstop for codex/
// Claude children that wedge on the model backend and hold a replica slot.
const envInstanceMaxRuntime = "AGENT_TEAM_INSTANCE_MAX_RUNTIME"

// ephemeralRuntimeBudget resolves the wall-clock budget for an ephemeral spawn.
// Any configured wall-clock ceiling can arm the watchdog: time_budget, timeout,
// or the AGENT_TEAM_INSTANCE_MAX_RUNTIME env default. The earliest positive
// ceiling wins so existing timeout/env backstops stay active while time_budget
// becomes a hard budget. Unparseable values are ignored so one bad source never
// accidentally arms or disarms the watchdog; it simply falls through.
func ephemeralRuntimeBudget(payload map[string]any) time.Duration {
	return ephemeralRuntimeBudgetWithInstance(nil, payload)
}

func ephemeralRuntimeBudgetForInstance(inst *topology.Instance, payload map[string]any) time.Duration {
	return ephemeralRuntimeBudgetWithInstance(inst, payload)
}

func ephemeralRuntimeBudgetWithInstance(inst *topology.Instance, payload map[string]any) time.Duration {
	var budget time.Duration
	fallbackMultiplier := 0.0
	if inst != nil {
		fallbackMultiplier = inst.HardMultiplier
	}
	if tb := payloadHardTimeBudget(payload, fallbackMultiplier); tb > 0 {
		budget = minPositiveDuration(budget, tb)
	} else if inst != nil && inst.TimeBudget > 0 {
		tb := hardTimeBudgetDuration(inst.TimeBudget, inst.HardMultiplier)
		budget = minPositiveDuration(budget, tb)
	}
	if ts := strings.TrimSpace(payloadString(payload, "timeout")); ts != "" {
		if d, err := time.ParseDuration(ts); err == nil && d > 0 {
			budget = minPositiveDuration(budget, d)
		}
	}
	if env := strings.TrimSpace(os.Getenv(envInstanceMaxRuntime)); env != "" {
		if d, err := time.ParseDuration(env); err == nil && d > 0 {
			budget = minPositiveDuration(budget, d)
		}
	}
	return budget
}

func payloadHardTimeBudget(payload map[string]any, fallbackMultiplier float64) time.Duration {
	if payload == nil {
		return 0
	}
	raw := strings.TrimSpace(payloadString(payload, "budget_time"))
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	multiplier := payloadBudgetHardMultiplier(payload)
	if multiplier <= 0 {
		multiplier = fallbackMultiplier
	}
	return hardTimeBudgetDuration(d, multiplier)
}

func hardTimeBudgetDuration(budget time.Duration, multiplier float64) time.Duration {
	if budget <= 0 {
		return 0
	}
	if multiplier <= 0 {
		return budget
	}
	limit := allowance.HardLimit(int64(budget), true, multiplier)
	if limit <= 0 {
		return 0
	}
	return time.Duration(limit)
}

func minPositiveDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a <= b {
		return a
	}
	return b
}

func applyPayloadBudgetToJob(j *jobstore.Job, payload map[string]any) {
	if j == nil || payload == nil {
		return
	}
	tokens := payloadBudgetTokens(payload)
	timeBudget := strings.TrimSpace(payloadString(payload, "budget_time"))
	hardBudget := payloadBudgetHard(payload)
	hardMultiplier := payloadBudgetHardMultiplier(payload)
	levels := payloadReminderLevels(payload)
	stepID := payloadString(payload, "pipeline_step")
	if stepID != "" {
		for i := range j.Steps {
			if j.Steps[i].ID != stepID {
				continue
			}
			if tokens > 0 {
				j.Steps[i].TokenBudget = tokens
			}
			if timeBudget != "" {
				j.Steps[i].TimeBudget = timeBudget
			}
			if hardBudget {
				j.Steps[i].HardBudget = true
			}
			if hardMultiplier > 0 {
				j.Steps[i].HardMultiplier = hardMultiplier
			}
			if len(levels) > 0 {
				j.Steps[i].ReminderLevels = levels
			}
			return
		}
	}
	if tokens > 0 {
		j.TokenBudget = tokens
	}
	if timeBudget != "" {
		j.TimeBudget = timeBudget
	}
	if hardBudget {
		j.HardBudget = true
	}
	if hardMultiplier > 0 {
		j.HardMultiplier = hardMultiplier
	}
	if len(levels) > 0 {
		j.ReminderLevels = levels
	}
}

func payloadReminderLevels(payload map[string]any) []int {
	if payload == nil {
		return nil
	}
	raw := payload["reminder_levels"]
	if raw == nil {
		return nil
	}
	var levels []int
	switch values := raw.(type) {
	case []int:
		levels = append([]int(nil), values...)
	case []int64:
		for _, v := range values {
			if int64(int(v)) == v {
				levels = append(levels, int(v))
			}
		}
	case []any:
		for _, value := range values {
			switch v := value.(type) {
			case int:
				levels = append(levels, v)
			case int64:
				if int64(int(v)) == v {
					levels = append(levels, int(v))
				}
			case float64:
				if v == math.Trunc(v) && v <= float64(int(^uint(0)>>1)) {
					levels = append(levels, int(v))
				}
			}
		}
	}
	normalized, err := allowance.NormalizeReminderLevels(levels)
	if err != nil {
		return nil
	}
	return normalized
}

func (r *EventResolver) budgetAdmissionLocked(team string, payload map[string]any, now time.Time) (budget.Admission, error) {
	if r == nil {
		return budget.Admission{Allowed: true, Noop: true, Team: strings.TrimSpace(team)}, nil
	}
	return budget.AdmissionForTeamWithRequest(r.teamDir, r.topo, team, eventJobID(payload), payloadBudgetTokens(payload), now)
}

func (r *EventResolver) budgetsConfigured() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.topo != nil && len(r.topo.Budgets) > 0
}
