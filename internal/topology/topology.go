// Package topology implements the `instances.toml` schema: declared agent
// instances, per-instance config overrides, and the event-trigger table.
//
// See `documentation/topology.md` for the design. The schema is parsed via
// BurntSushi/toml; the match-evaluation DSL is intentionally minimal in v1.2
// (single-value equality, list membership, AND across keys).
package topology

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/template"
)

// FileName is the conventional file name at the template root and at
// `<.agent_team>/instances.toml`.
const FileName = "instances.toml"

// DefaultReplicas is the implicit `replicas = 1` for ephemeral instances when
// the field is omitted. Persistent instances ignore replicas.
const DefaultReplicas = 1

// Event types recognised by the daemon's resolver. Webhook aliases
// (`ticket_webhook`, `pr_webhook`) remain supported for older topology files;
// normalized intake events (`ticket.created`, `pr.merged`, etc.) match those
// aliases with the event suffix exposed to trigger matchers as `event`.
const (
	EventUserInvocation = "user_invocation"
	EventAgentDispatch  = "agent.dispatch"
	EventSchedule       = "schedule"
	EventChannelMessage = "channel.message"
	EventTicketWebhook  = "ticket_webhook"
	EventPRWebhook      = "pr_webhook"
)

// Topology is the parsed + merged set of declared instances for a repo.
type Topology struct {
	// Instances is keyed by the declared instance name (the `[instances.<n>]`
	// table key in the TOML).
	Instances map[string]*Instance
	// Pipelines is keyed by the declared pipeline name (`[pipelines.<n>]`).
	Pipelines map[string]*Pipeline
	// Schedules is keyed by the declared schedule name (`[schedules.<n>]`).
	Schedules map[string]*Schedule
	// Teams is keyed by the declared team name (`[teams.<n>]`).
	Teams map[string]*Team
}

// Instance is one declared instance.
type Instance struct {
	Name        string
	Agent       string
	Ephemeral   bool
	Description string
	// Replicas is meaningful only for ephemeral instances. Defaults to 1.
	Replicas int
	// Config holds per-instance overrides for the resolved config tree —
	// dotted-path keys flattened from `[instances.<name>.config]` in TOML.
	// Empty when no overrides are declared.
	Config template.Tree
	// Triggers is the ordered list of event-matchers declared for this
	// instance. An empty list means the instance is only invokable via an
	// explicit `agent-team run <name>` (i.e. no event-driven dispatch).
	Triggers []*Trigger
}

// Trigger is one entry under `[[instances.<name>.triggers]]`.
type Trigger struct {
	Event string
	// Match is the per-key matcher. Empty map = match any payload of this
	// event type. Multiple keys = AND. Each value is a single-or-list
	// expression (see MatchValue).
	Match map[string]MatchValue
}

// MatchValue is one `match.<key>` entry. Either Single is non-empty (exact
// equality) or List is non-empty (membership). Both empty is invalid and
// rejected during parse.
type MatchValue struct {
	Single string
	List   []string
}

// Pipeline is a declarative multi-step workflow triggered by an event.
type Pipeline struct {
	Name    string
	Trigger *Trigger
	Steps   []*PipelineStep
}

// PipelineStep is one target dispatch in a pipeline.
type PipelineStep struct {
	ID           string
	Label        string
	Description  string
	Instructions string
	Target       string
	Workspace    string
	After        []string
	Gate         string
	Optional     bool
	Timeout      time.Duration
	MaxAttempts  int
}

// Schedule is a periodic source of `schedule` events.
type Schedule struct {
	Name       string
	Every      time.Duration
	RunOnStart bool
	Payload    map[string]any
}

// Team names a group of instances, pipelines, and schedules owned together.
type Team struct {
	Name        string
	Description string
	Instances   []string
	Pipelines   []string
	Schedules   []string
}

// EventPayload returns the payload published for this schedule tick.
func (s *Schedule) EventPayload() map[string]any {
	payload := make(map[string]any, len(s.Payload)+2)
	for k, v := range s.Payload {
		payload[k] = v
	}
	payload["source"] = "schedule"
	payload["name"] = s.Name
	return payload
}

// Eval returns true iff `payload[key]` matches this expression. The payload
// value is coerced to its string form (json-decoded values arrive as string,
// json.Number, or bool — we stringify each for comparison; this is the small
// DSL the docs commit to).
func (mv MatchValue) Eval(value any) bool {
	got := stringifyMatchValue(value)
	if mv.Single != "" {
		return got == mv.Single
	}
	for _, want := range mv.List {
		if got == want {
			return true
		}
	}
	return false
}

func stringifyMatchValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

// Matches returns true iff every key in Match has a value in payload that
// satisfies the corresponding MatchValue. An empty Match map matches any
// payload.
func (t *Trigger) Matches(payload map[string]any) bool {
	for k, mv := range t.Match {
		v, ok := payload[k]
		if !ok {
			return false
		}
		if !mv.Eval(v) {
			return false
		}
	}
	return true
}

// Resolve returns the declared instances whose triggers match the given event
// type and payload, in the deterministic order produced by sorting by
// instance name. The `dispatch` field on the trigger is not considered here —
// the caller (daemon event resolver) decides how to actuate.
func (t *Topology) Resolve(eventType string, payload map[string]any) []*Instance {
	if t == nil {
		return nil
	}
	var matched []*Instance
	for _, inst := range t.SortedInstances() {
		for _, trig := range inst.Triggers {
			matches, matchPayload := triggerMatchesEvent(trig, eventType, payload)
			if !matches {
				continue
			}
			if trig.Matches(matchPayload) {
				matched = append(matched, inst)
				break
			}
		}
	}
	return matched
}

// ResolvePipelines returns pipelines whose trigger matches the event.
func (t *Topology) ResolvePipelines(eventType string, payload map[string]any) []*Pipeline {
	if t == nil {
		return nil
	}
	var matched []*Pipeline
	for _, p := range t.SortedPipelines() {
		matches, matchPayload := triggerMatchesEvent(p.Trigger, eventType, payload)
		if !matches {
			continue
		}
		if p.Trigger.Matches(matchPayload) {
			matched = append(matched, p)
		}
	}
	return matched
}

func triggerMatchesEvent(trigger *Trigger, eventType string, payload map[string]any) (bool, map[string]any) {
	if trigger == nil {
		return false, payload
	}
	if trigger.Event == eventType {
		return true, payload
	}
	switch trigger.Event {
	case EventTicketWebhook:
		if suffix, ok := normalizedEventSuffix(eventType, "ticket."); ok {
			return true, payloadWithEventSuffix(payload, suffix)
		}
	case EventPRWebhook:
		if suffix, ok := normalizedEventSuffix(eventType, "pr."); ok {
			return true, payloadWithEventSuffix(payload, suffix)
		}
	}
	return false, payload
}

func normalizedEventSuffix(eventType, prefix string) (string, bool) {
	if !strings.HasPrefix(eventType, prefix) {
		return "", false
	}
	suffix := strings.TrimPrefix(eventType, prefix)
	return suffix, suffix != ""
}

func payloadWithEventSuffix(payload map[string]any, suffix string) map[string]any {
	if _, ok := payload["event"]; ok {
		return payload
	}
	out := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		out[key] = value
	}
	out["event"] = suffix
	return out
}

// SortedInstances returns the instances ordered by name for deterministic
// iteration in tests, CLI output, and HTTP responses.
func (t *Topology) SortedInstances() []*Instance {
	if t == nil {
		return nil
	}
	out := make([]*Instance, 0, len(t.Instances))
	for _, i := range t.Instances {
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SortedPipelines returns pipelines ordered by name for deterministic execution.
func (t *Topology) SortedPipelines() []*Pipeline {
	if t == nil {
		return nil
	}
	out := make([]*Pipeline, 0, len(t.Pipelines))
	for _, p := range t.Pipelines {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SortedSchedules returns schedules ordered by name for deterministic execution.
func (t *Topology) SortedSchedules() []*Schedule {
	if t == nil {
		return nil
	}
	out := make([]*Schedule, 0, len(t.Schedules))
	for _, s := range t.Schedules {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SortedTeams returns teams ordered by name for deterministic output.
func (t *Topology) SortedTeams() []*Team {
	if t == nil {
		return nil
	}
	out := make([]*Team, 0, len(t.Teams))
	for _, team := range t.Teams {
		out = append(out, team)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PersistentNames returns the names of declared non-ephemeral instances, in
// sorted order. `instance up` brings these up by default.
func (t *Topology) PersistentNames() []string {
	if t == nil {
		return nil
	}
	var names []string
	for _, inst := range t.SortedInstances() {
		if !inst.Ephemeral {
			names = append(names, inst.Name)
		}
	}
	return names
}

// Find returns the declared instance by name, or nil if none.
func (t *Topology) Find(name string) *Instance {
	if t == nil {
		return nil
	}
	return t.Instances[name]
}

// FindTeam returns the declared team by name, or nil if none.
func (t *Topology) FindTeam(name string) *Team {
	if t == nil {
		return nil
	}
	return t.Teams[name]
}

// rawTopology mirrors the on-wire TOML schema. We keep parsing in two stages
// so the public Topology can carry validated, normalised values regardless of
// how lenient toml.Decode is.
type rawTopology struct {
	Instances map[string]*rawInstance `toml:"instances"`
	Pipelines map[string]*rawPipeline `toml:"pipelines"`
	Schedules map[string]*rawSchedule `toml:"schedules"`
	Teams     map[string]*rawTeam     `toml:"teams"`
}

type rawInstance struct {
	Agent       string           `toml:"agent"`
	Ephemeral   bool             `toml:"ephemeral"`
	Description string           `toml:"description"`
	Replicas    *int             `toml:"replicas"`
	Config      map[string]any   `toml:"config"`
	Triggers    []map[string]any `toml:"triggers"`
}

type rawPipeline struct {
	Trigger map[string]any   `toml:"trigger"`
	Steps   []map[string]any `toml:"steps"`
}

type rawSchedule struct {
	Every      string         `toml:"every"`
	Interval   string         `toml:"interval"`
	RunOnStart bool           `toml:"run_on_start"`
	Payload    map[string]any `toml:"payload"`
}

type rawTeam struct {
	Description string   `toml:"description"`
	Instances   []string `toml:"instances"`
	Pipelines   []string `toml:"pipelines"`
	Schedules   []string `toml:"schedules"`
}

// Parse decodes a single `instances.toml` body. Used by Load and tests.
func Parse(body []byte) (*Topology, error) {
	return parseWithTeamValidation(body, true)
}

func parseWithTeamValidation(body []byte, validateTeamRefs bool) (*Topology, error) {
	var raw rawTopology
	if _, err := toml.Decode(string(body), &raw); err != nil {
		return nil, fmt.Errorf("instances.toml: %w", err)
	}
	return finalise(&raw, validateTeamRefs)
}

func finalise(raw *rawTopology, validateTeamRefs bool) (*Topology, error) {
	t := &Topology{
		Instances: make(map[string]*Instance, len(raw.Instances)),
		Pipelines: make(map[string]*Pipeline, len(raw.Pipelines)),
		Schedules: make(map[string]*Schedule, len(raw.Schedules)),
		Teams:     make(map[string]*Team, len(raw.Teams)),
	}
	for name, ri := range raw.Instances {
		if ri == nil {
			continue
		}
		inst, err := finaliseInstance(name, ri)
		if err != nil {
			return nil, err
		}
		t.Instances[name] = inst
	}
	for name, rp := range raw.Pipelines {
		if rp == nil {
			continue
		}
		p, err := finalisePipeline(name, rp)
		if err != nil {
			return nil, err
		}
		t.Pipelines[name] = p
	}
	for name, rs := range raw.Schedules {
		if rs == nil {
			continue
		}
		s, err := finaliseSchedule(name, rs)
		if err != nil {
			return nil, err
		}
		t.Schedules[name] = s
	}
	for name, rt := range raw.Teams {
		if rt == nil {
			continue
		}
		team, err := finaliseTeam(name, rt, t, validateTeamRefs)
		if err != nil {
			return nil, err
		}
		t.Teams[name] = team
	}
	return t, nil
}

func finaliseInstance(name string, ri *rawInstance) (*Instance, error) {
	if strings.TrimSpace(ri.Agent) == "" {
		return nil, fmt.Errorf("instance %q: `agent` is required", name)
	}
	replicas := DefaultReplicas
	if ri.Replicas != nil {
		if *ri.Replicas < 1 {
			return nil, fmt.Errorf("instance %q: replicas must be >= 1", name)
		}
		replicas = *ri.Replicas
	}
	if !ri.Ephemeral && ri.Replicas != nil && *ri.Replicas != 1 {
		// Persistent instances are implicitly singletons. Warn-via-error so the
		// author either fixes the config or marks the instance ephemeral.
		return nil, fmt.Errorf("instance %q: replicas only valid on ephemeral instances", name)
	}
	cfg := template.Tree{}
	if len(ri.Config) > 0 {
		// `config` arrives as a free-form map[string]any from BurntSushi/toml.
		// We accept arbitrary nested tables — the resolution chain merges them
		// with the same MergeOver helper used for `config.toml`.
		flatten(ri.Config, "", cfg)
	}
	triggers, err := parseTriggers(name, ri.Triggers)
	if err != nil {
		return nil, err
	}
	return &Instance{
		Name:        name,
		Agent:       ri.Agent,
		Ephemeral:   ri.Ephemeral,
		Description: ri.Description,
		Replicas:    replicas,
		Config:      cfg,
		Triggers:    triggers,
	}, nil
}

func finalisePipeline(name string, rp *rawPipeline) (*Pipeline, error) {
	trigger, err := parsePipelineTrigger(name, rp.Trigger)
	if err != nil {
		return nil, err
	}
	steps, err := parsePipelineSteps(name, rp.Steps)
	if err != nil {
		return nil, err
	}
	return &Pipeline{Name: name, Trigger: trigger, Steps: steps}, nil
}

func finaliseSchedule(name string, rs *rawSchedule) (*Schedule, error) {
	everyRaw := strings.TrimSpace(rs.Every)
	if everyRaw == "" {
		everyRaw = strings.TrimSpace(rs.Interval)
	}
	if everyRaw == "" {
		return nil, fmt.Errorf("schedule %q: every is required", name)
	}
	every, err := time.ParseDuration(everyRaw)
	if err != nil {
		return nil, fmt.Errorf("schedule %q: every: %w", name, err)
	}
	if every <= 0 {
		return nil, fmt.Errorf("schedule %q: every must be > 0", name)
	}
	payload := map[string]any{}
	for k, v := range rs.Payload {
		payload[k] = v
	}
	return &Schedule{Name: name, Every: every, RunOnStart: rs.RunOnStart, Payload: payload}, nil
}

func finaliseTeam(name string, rt *rawTeam, t *Topology, validateRefs bool) (*Team, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("team name is required")
	}
	instances, err := cleanTeamRefs("team", name, "instances", rt.Instances)
	if err != nil {
		return nil, err
	}
	pipelines, err := cleanTeamRefs("team", name, "pipelines", rt.Pipelines)
	if err != nil {
		return nil, err
	}
	schedules, err := cleanTeamRefs("team", name, "schedules", rt.Schedules)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 && len(pipelines) == 0 && len(schedules) == 0 {
		return nil, fmt.Errorf("team %q: at least one of instances, pipelines, or schedules is required", name)
	}
	if !validateRefs {
		return &Team{
			Name:        name,
			Description: strings.TrimSpace(rt.Description),
			Instances:   instances,
			Pipelines:   pipelines,
			Schedules:   schedules,
		}, nil
	}
	team := &Team{
		Name:        name,
		Description: strings.TrimSpace(rt.Description),
		Instances:   instances,
		Pipelines:   pipelines,
		Schedules:   schedules,
	}
	if err := validateTeamReferences(t, team); err != nil {
		return nil, err
	}
	return team, nil
}

func validateTopologyTeams(t *Topology) error {
	if t == nil {
		return nil
	}
	for _, team := range t.SortedTeams() {
		if err := validateTeamReferences(t, team); err != nil {
			return err
		}
	}
	return nil
}

func validateTeamReferences(t *Topology, team *Team) error {
	if t == nil || team == nil {
		return nil
	}
	for _, ref := range team.Instances {
		if t.Instances[ref] == nil {
			return fmt.Errorf("team %q: instances references unknown instance %q", team.Name, ref)
		}
	}
	for _, ref := range team.Pipelines {
		if t.Pipelines[ref] == nil {
			return fmt.Errorf("team %q: pipelines references unknown pipeline %q", team.Name, ref)
		}
	}
	for _, ref := range team.Schedules {
		if t.Schedules[ref] == nil {
			return fmt.Errorf("team %q: schedules references unknown schedule %q", team.Name, ref)
		}
	}
	return nil
}

func cleanTeamRefs(kind, name, field string, refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for i, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return nil, fmt.Errorf("%s %q: %s[%d] must be non-empty", kind, name, field, i)
		}
		if seen[ref] {
			return nil, fmt.Errorf("%s %q: %s contains duplicate %q", kind, name, field, ref)
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out, nil
}

// flatten copies src into dst, preserving the dotted-key shape that
// `template.Tree` already uses. Keys whose values are themselves maps are
// recursed into so that the resulting tree mirrors what
// `template.LoadTOMLFile` would produce.
func flatten(src map[string]any, prefix string, dst template.Tree) {
	for k, v := range src {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch x := v.(type) {
		case map[string]any:
			flatten(x, key, dst)
		default:
			dst.SetDotted(key, v)
		}
	}
}

func parseTriggers(instanceName string, raw []map[string]any) ([]*Trigger, error) {
	out := make([]*Trigger, 0, len(raw))
	for i, t := range raw {
		evRaw, ok := t["event"]
		if !ok {
			return nil, fmt.Errorf("instance %q trigger[%d]: `event` is required", instanceName, i)
		}
		ev, ok := evRaw.(string)
		if !ok || strings.TrimSpace(ev) == "" {
			return nil, fmt.Errorf("instance %q trigger[%d]: `event` must be a non-empty string", instanceName, i)
		}
		trig := &Trigger{Event: ev, Match: map[string]MatchValue{}}
		// match.<key> arrives under a nested `match` map produced by TOML
		// dotted keys (e.g. `match.project = "Platform"`).
		if mraw, ok := t["match"]; ok {
			mm, ok := mraw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("instance %q trigger[%d]: `match` must be a table", instanceName, i)
			}
			for k, v := range mm {
				mv, err := parseMatchValue(v)
				if err != nil {
					return nil, fmt.Errorf("instance %q trigger[%d] match.%s: %w", instanceName, i, k, err)
				}
				trig.Match[k] = mv
			}
		}
		out = append(out, trig)
	}
	return out, nil
}

func parsePipelineTrigger(name string, raw map[string]any) (*Trigger, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("pipeline %q: trigger.event is required", name)
	}
	triggers, err := parseTriggers("pipeline "+name, []map[string]any{raw})
	if err != nil {
		return nil, err
	}
	return triggers[0], nil
}

func parsePipelineSteps(name string, raw []map[string]any) ([]*PipelineStep, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("pipeline %q: at least one step is required", name)
	}
	seen := map[string]bool{}
	steps := make([]*PipelineStep, 0, len(raw))
	for i, body := range raw {
		id, ok := body["id"].(string)
		id = strings.TrimSpace(id)
		if !ok || id == "" {
			return nil, fmt.Errorf("pipeline %q step[%d]: id is required", name, i)
		}
		if seen[id] {
			return nil, fmt.Errorf("pipeline %q step[%d]: duplicate id %q", name, i, id)
		}
		seen[id] = true
		target, ok := body["target"].(string)
		target = strings.TrimSpace(target)
		if !ok || target == "" {
			return nil, fmt.Errorf("pipeline %q step[%d]: target is required", name, i)
		}
		workspace, err := parseStepWorkspace(body["workspace"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		label, err := parseStepText(body["label"], "label")
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		description, err := parseStepText(body["description"], "description")
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		instructions, err := parseStepText(body["instructions"], "instructions")
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		after, err := parseStepAfter(body["after"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		gate, err := parseStepGate(body["gate"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		optional, err := parseStepOptional(body["optional"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		timeout, err := parseStepTimeout(body["timeout"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		maxAttempts, err := parseStepMaxAttempts(body["max_attempts"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		steps = append(steps, &PipelineStep{ID: id, Label: label, Description: description, Instructions: instructions, Target: target, Workspace: workspace, After: after, Gate: gate, Optional: optional, Timeout: timeout, MaxAttempts: maxAttempts})
	}
	for _, step := range steps {
		for _, dep := range step.After {
			if !seen[dep] {
				return nil, fmt.Errorf("pipeline %q step %q: after references unknown step %q", name, step.ID, dep)
			}
		}
	}
	return steps, nil
}

func parseStepText(raw any, field string) (string, error) {
	if raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", fmt.Errorf("%s must be a non-empty string", field)
	}
	return value, nil
}

func parseStepWorkspace(raw any) (string, error) {
	if raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	value = strings.ToLower(strings.TrimSpace(value))
	if !ok || value == "" {
		return "", fmt.Errorf("workspace must be a non-empty string")
	}
	switch value {
	case "auto", "worktree", "repo":
		return value, nil
	default:
		return "", fmt.Errorf("workspace must be auto, worktree, or repo")
	}
}

func parseStepMaxAttempts(raw any) (int, error) {
	if raw == nil {
		return 0, nil
	}
	var value int
	switch v := raw.(type) {
	case int:
		value = v
	case int64:
		value = int(v)
		if int64(value) != v {
			return 0, fmt.Errorf("max_attempts is too large")
		}
	default:
		return 0, fmt.Errorf("max_attempts must be an integer")
	}
	if value <= 0 {
		return 0, fmt.Errorf("max_attempts must be greater than zero")
	}
	return value, nil
}

func parseStepOptional(raw any) (bool, error) {
	if raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("optional must be a boolean")
	}
	return value, nil
}

func parseStepTimeout(raw any) (time.Duration, error) {
	if raw == nil {
		return 0, nil
	}
	value, ok := raw.(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return 0, fmt.Errorf("timeout must be a non-empty duration string")
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("timeout must be a valid duration: %w", err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("timeout must be greater than zero")
	}
	return timeout, nil
}

func parseStepGate(raw any) (string, error) {
	if raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	value = strings.ToLower(strings.TrimSpace(value))
	if !ok || value == "" {
		return "", fmt.Errorf("gate must be a non-empty string")
	}
	switch value {
	case "manual", "pr":
		return value, nil
	default:
		return "", fmt.Errorf("gate must be manual or pr")
	}
}

func parseStepAfter(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, fmt.Errorf("after cannot be empty")
		}
		return []string{v}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			s = strings.TrimSpace(s)
			if !ok || s == "" {
				return nil, fmt.Errorf("after values must be non-empty strings")
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("after must be a string or list of strings")
	}
}

func parseMatchValue(v any) (MatchValue, error) {
	switch x := v.(type) {
	case string:
		if x == "" {
			return MatchValue{}, fmt.Errorf("empty string is not a valid match value")
		}
		return MatchValue{Single: x}, nil
	case []any:
		if len(x) == 0 {
			return MatchValue{}, fmt.Errorf("empty list is not a valid match value")
		}
		out := make([]string, 0, len(x))
		for _, el := range x {
			s, ok := el.(string)
			if !ok {
				return MatchValue{}, fmt.Errorf("list values must be strings; got %T", el)
			}
			if s == "" {
				return MatchValue{}, fmt.Errorf("empty string is not a valid match value")
			}
			out = append(out, s)
		}
		return MatchValue{List: out}, nil
	}
	return MatchValue{}, fmt.Errorf("must be a string or list of strings; got %T", v)
}
