package topology

import (
	"fmt"
	"sort"
	"strings"
)

const (
	EventTraceKindInstance = "instance"
	EventTraceKindPipeline = "pipeline"

	EventTraceReasonMatched = "matched"
)

// EventTrace explains how one event matched, or failed to match, every
// declared instance and pipeline trigger.
type EventTrace struct {
	Type         string            `json:"type"`
	Payload      map[string]any    `json:"payload"`
	MatchedRules int               `json:"matched_rules"`
	Entries      []EventTraceEntry `json:"entries"`
}

// EventTraceEntry is one per declared trigger. Instances can have many
// triggers, pipelines have one trigger.
type EventTraceEntry struct {
	Scope        string                  `json:"scope"`
	Kind         string                  `json:"kind"`
	Name         string                  `json:"name"`
	Agent        string                  `json:"agent,omitempty"`
	TriggerIndex *int                    `json:"trigger_index,omitempty"`
	TriggerEvent string                  `json:"trigger_event"`
	Matcher      string                  `json:"matcher,omitempty"`
	Matched      bool                    `json:"matched"`
	Reason       string                  `json:"reason"`
	FirstStep    *EventTracePipelineStep `json:"first_step,omitempty"`
}

// EventTracePipelineStep describes the first dispatch step for a pipeline
// trigger. It keeps CLI and daemon trace output useful without coupling
// topology to the job store.
type EventTracePipelineStep struct {
	ID     string `json:"id"`
	Target string `json:"target"`
}

// Trace evaluates all declared trigger rules against an event and records the
// first reason each rule matched or rejected the event.
func (t *Topology) Trace(eventType string, payload map[string]any) EventTrace {
	payload = normalizeTracePayload(payload)
	trace := EventTrace{
		Type:    eventType,
		Payload: payload,
		Entries: []EventTraceEntry{},
	}
	if t == nil {
		return trace
	}
	for _, inst := range t.SortedInstances() {
		for idx, trig := range inst.Triggers {
			entry := traceTrigger(EventTraceKindInstance, inst.Name, inst.Agent, intPtr(idx), trig, eventType, payload)
			trace.addEntry(entry)
		}
	}
	for _, pipeline := range t.SortedPipelines() {
		entry := traceTrigger(EventTraceKindPipeline, pipeline.Name, "", nil, pipeline.Trigger, eventType, payload)
		if len(pipeline.Steps) > 0 {
			entry.FirstStep = &EventTracePipelineStep{
				ID:     pipeline.Steps[0].ID,
				Target: pipeline.Steps[0].Target,
			}
		}
		trace.addEntry(entry)
	}
	return trace
}

func (tr *EventTrace) addEntry(entry EventTraceEntry) {
	tr.Entries = append(tr.Entries, entry)
	if entry.Matched {
		tr.MatchedRules++
	}
}

// MatchedInstanceNames returns the unique declared instance names with at
// least one matching trigger, preserving topology trace order.
func (tr EventTrace) MatchedInstanceNames() []string {
	return traceMatchedNames(tr.Entries, EventTraceKindInstance)
}

// MatchedPipelineNames returns the unique declared pipeline names with a
// matching trigger, preserving topology trace order.
func (tr EventTrace) MatchedPipelineNames() []string {
	return traceMatchedNames(tr.Entries, EventTraceKindPipeline)
}

func traceMatchedNames(entries []EventTraceEntry, kind string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, entry := range entries {
		if entry.Kind != kind || !entry.Matched || seen[entry.Name] {
			continue
		}
		seen[entry.Name] = true
		out = append(out, entry.Name)
	}
	return out
}

func traceTrigger(kind, name, agent string, triggerIndex *int, trigger *Trigger, eventType string, payload map[string]any) EventTraceEntry {
	entry := EventTraceEntry{
		Scope:        traceScope(kind, name),
		Kind:         kind,
		Name:         name,
		Agent:        agent,
		TriggerIndex: triggerIndex,
		Reason:       "trigger missing",
	}
	if trigger == nil {
		return entry
	}
	entry.TriggerEvent = trigger.Event
	matchesEvent, matchPayload := triggerMatchesEvent(trigger, eventType, payload)
	if !matchesEvent {
		entry.Reason = "event type mismatch"
		return entry
	}
	matched, matcher, reason := traceTriggerMatch(trigger.Match, matchPayload)
	entry.Matcher = matcher
	entry.Matched = matched
	entry.Reason = reason
	return entry
}

func traceTriggerMatch(match map[string]MatchValue, payload map[string]any) (bool, string, string) {
	if len(match) == 0 {
		return true, "", EventTraceReasonMatched
	}
	keys := make([]string, 0, len(match))
	for key := range match {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	matchers := make([]string, 0, len(keys))
	for _, key := range keys {
		mv := match[key]
		matcher := "match." + key + "=" + traceMatchValue(mv)
		matchers = append(matchers, matcher)
		value, ok := payload[key]
		if !ok {
			return false, matcher, "payload " + key + " missing"
		}
		if mv.Eval(value) {
			continue
		}
		got := stringifyMatchValue(value)
		if mv.Single != "" {
			return false, matcher, fmt.Sprintf("payload %s=%s != %s", key, got, mv.Single)
		}
		return false, matcher, fmt.Sprintf("payload %s=%s not in %s", key, got, traceMatchValue(mv))
	}
	return true, strings.Join(matchers, " "), EventTraceReasonMatched
}

func traceMatchValue(mv MatchValue) string {
	if mv.Single != "" {
		return mv.Single
	}
	return "[" + strings.Join(mv.List, ", ") + "]"
}

func traceScope(kind, name string) string {
	switch kind {
	case EventTraceKindInstance:
		return "instances." + name
	case EventTraceKindPipeline:
		return "pipelines." + name
	default:
		return kind + "." + name
	}
}

func normalizeTracePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	return payload
}

func intPtr(v int) *int {
	out := new(int)
	*out = v
	return out
}
