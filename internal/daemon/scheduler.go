package daemon

import (
	"context"
	"time"

	"github.com/jamesaud/agent-team/internal/topology"
)

var schedulePollInterval = time.Second

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

func (r *EventResolver) fireDueSchedules(now time.Time, state map[string]*ScheduleState) []string {
	topo := r.Topology()
	if topo == nil || len(topo.Schedules) == 0 {
		for name := range state {
			delete(state, name)
			_ = RemoveScheduleState(r.mgr.daemonRoot, name)
		}
		return nil
	}
	current := map[string]bool{}
	fired := []string{}
	for _, sched := range topo.SortedSchedules() {
		current[sched.Name] = true
		clock, seen := state[sched.Name]
		if !seen {
			clock = &ScheduleState{Name: sched.Name, LastSeenAt: now}
			state[sched.Name] = clock
			if !sched.RunOnStart {
				_ = WriteScheduleState(r.mgr.daemonRoot, clock)
				continue
			}
		} else if now.Sub(clock.LastSeenAt) < sched.Every {
			continue
		}
		clock.LastSeenAt = now
		clock.LastFiredAt = now
		payload := sched.EventPayload()
		_, _ = r.Event(topology.EventSchedule, payload)
		_ = WriteScheduleState(r.mgr.daemonRoot, clock)
		fired = append(fired, sched.Name)
	}
	for name := range state {
		if !current[name] {
			delete(state, name)
			_ = RemoveScheduleState(r.mgr.daemonRoot, name)
		}
	}
	return fired
}
