package daemon

import (
	"context"
	"time"

	"github.com/agent-team-project/agent-team/internal/topology"
)

var schedulePollInterval = time.Second

// ScheduleFireResult summarizes one due-schedule evaluation.
type ScheduleFireResult struct {
	DryRun    bool               `json:"dry_run,omitempty"`
	Fired     int                `json:"fired"`
	WouldFire int                `json:"would_fire,omitempty"`
	Schedules []ScheduleFireItem `json:"schedules"`
}

// ScheduleFireItem describes one schedule that fired, or would fire in dry-run.
type ScheduleFireItem struct {
	Name      string         `json:"name"`
	EventType string         `json:"event_type"`
	Payload   map[string]any `json:"payload,omitempty"`
	Reason    string         `json:"reason"`
	Outcomes  []EventOutcome `json:"outcomes,omitempty"`
}

// RunSchedules publishes due topology schedules until ctx is cancelled.
func (r *EventResolver) RunSchedules(ctx context.Context) {
	if r == nil {
		return
	}
	state := r.loadScheduleStates()
	r.fireDueSchedules(time.Now().UTC(), state)
	ticker := time.NewTicker(schedulePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.fireDueSchedules(now.UTC(), state)
		}
	}
}

func (r *EventResolver) loadScheduleStates() map[string]*ScheduleState {
	out := map[string]*ScheduleState{}
	if r == nil || r.mgr == nil {
		return out
	}
	states, err := ListScheduleStates(r.mgr.daemonRoot)
	if err != nil {
		return out
	}
	for _, state := range states {
		out[state.Name] = state
	}
	return out
}

func (r *EventResolver) scheduleStateName(s *topology.Schedule) string {
	if s == nil {
		return ""
	}
	topo := r.Topology()
	team := ""
	if topo != nil {
		team = topo.TeamForSchedule(s.Name)
	}
	return topology.ScopedResourceName(s.Name, s.Scope, team, "")
}

// FireDueSchedulesWithResult publishes every schedule due at now and persists
// schedule clocks. A zero now uses the current UTC time.
func (r *EventResolver) FireDueSchedulesWithResult(now time.Time) (*ScheduleFireResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return r.fireDueSchedulesWithResult(now.UTC(), r.loadScheduleStates(), false, nil)
}

// FireDueSchedulesWithResultForNames publishes due schedules whose names are
// included in names and persists only those schedule clocks.
func (r *EventResolver) FireDueSchedulesWithResultForNames(now time.Time, names []string) (*ScheduleFireResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return r.fireDueSchedulesWithResult(now.UTC(), r.loadScheduleStates(), false, stringAllowSet(names))
}

// PreviewDueSchedulesWithResult reports schedules that are due at now without
// dispatching events or writing schedule clocks.
func (r *EventResolver) PreviewDueSchedulesWithResult(now time.Time) (*ScheduleFireResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return r.fireDueSchedulesWithResult(now.UTC(), r.loadScheduleStates(), true, nil)
}

// PreviewDueSchedulesWithResultForNames reports due schedules whose names are
// included in names without dispatching events or writing schedule clocks.
func (r *EventResolver) PreviewDueSchedulesWithResultForNames(now time.Time, names []string) (*ScheduleFireResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return r.fireDueSchedulesWithResult(now.UTC(), r.loadScheduleStates(), true, stringAllowSet(names))
}

func (r *EventResolver) fireDueSchedules(now time.Time, state map[string]*ScheduleState) []string {
	result, err := r.fireDueSchedulesWithResult(now, state, false, nil)
	if err != nil {
		return nil
	}
	fired := make([]string, 0, len(result.Schedules))
	for _, item := range result.Schedules {
		fired = append(fired, item.Name)
	}
	return fired
}

func (r *EventResolver) fireDueSchedulesWithResult(now time.Time, state map[string]*ScheduleState, dryRun bool, names map[string]bool) (*ScheduleFireResult, error) {
	result := &ScheduleFireResult{DryRun: dryRun, Schedules: []ScheduleFireItem{}}
	scoped := names != nil
	topo := r.Topology()
	if topo == nil || len(topo.Schedules) == 0 {
		if !dryRun && !scoped {
			for name := range state {
				delete(state, name)
				_ = RemoveScheduleState(r.mgr.daemonRoot, name)
			}
		}
		return result, nil
	}
	current := map[string]bool{}
	for _, sched := range topo.SortedSchedules() {
		if scoped && !names[sched.Name] {
			continue
		}
		stateName := r.scheduleStateName(sched)
		current[stateName] = true
		clock, seen := state[stateName]
		reason := ""
		if !seen {
			clock = &ScheduleState{Name: stateName, LastSeenAt: now}
			if !sched.RunOnStart {
				if !dryRun {
					state[stateName] = clock
					_ = WriteScheduleState(r.mgr.daemonRoot, clock)
				}
				continue
			}
			reason = "run_on_start"
		} else if now.Sub(clock.LastSeenAt) < sched.Every {
			continue
		} else {
			reason = "interval"
		}
		payload := sched.EventPayload()
		item := ScheduleFireItem{Name: sched.Name, EventType: topology.EventSchedule, Payload: payload, Reason: reason}
		if dryRun {
			result.WouldFire++
			result.Schedules = append(result.Schedules, item)
			continue
		}
		if !seen {
			state[stateName] = clock
		}
		clock.LastSeenAt = now
		clock.LastFiredAt = now
		eventResult, err := r.EventWithResult(topology.EventSchedule, payload)
		if err != nil {
			return result, err
		}
		item.Outcomes = eventResult.Outcomes
		_ = WriteScheduleState(r.mgr.daemonRoot, clock)
		result.Fired++
		result.Schedules = append(result.Schedules, item)
	}
	if !dryRun && !scoped {
		for name := range state {
			if !current[name] {
				delete(state, name)
				_ = RemoveScheduleState(r.mgr.daemonRoot, name)
			}
		}
	}
	return result, nil
}

func stringAllowSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = true
		}
	}
	return out
}
