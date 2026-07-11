// Package topology implements the `instances.toml` schema: declared agent
// instances, per-instance config overrides, and the event-trigger table.
//
// See `documentation/topology.md` for the design. The schema is parsed via
// BurntSushi/toml; the match-evaluation DSL is intentionally minimal in v1.2
// (single-value equality, list membership, AND across keys).
package topology

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/allowance"
	"github.com/agent-team-project/agent-team/internal/mergepolicy"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
	"github.com/agent-team-project/agent-team/internal/template"
	"github.com/agent-team-project/agent-team/internal/worktreepolicy"
)

// FileName is the conventional file name at the template root and at
// `<.agent_team>/instances.toml`.
const FileName = "instances.toml"

// DefaultReplicas is the implicit `replicas = 1` for ephemeral instances when
// the field is omitted. Persistent instances ignore replicas.
const DefaultReplicas = 1

// Restart policy for declared persistent instances.
const (
	RestartNever     = "never"
	RestartOnFailure = "on-failure"
	RestartAlways    = "always"
)

// Resource scopes control how daemon state is keyed for topology resources.
// Machine scope preserves the historical flat namespace. Team and job scope
// add ownership-derived namespace segments where the daemon has an origin.
const (
	ScopeMachine = "machine"
	ScopeTeam    = "team"
	ScopeJob     = "job"
)

// Budget allocation modes control whether child allowances debit parent
// headroom at grant time or are only visible as outstanding promises.
const (
	BudgetAllocationOversubscribe = "oversubscribe"
	BudgetAllocationReserve       = "reserve"
)

// Authority enforcement modes control whether disallowed audited verbs are
// only recorded or actively refused.
const (
	AuthorityModeAudit   = "audit"
	AuthorityModeEnforce = "enforce"
)

// Event types recognised by the daemon's resolver. Intake publishes normalized
// event names such as `ticket.created` and `pr.merged`; topology triggers match
// those names exactly.
const (
	EventUserInvocation   = "user_invocation"
	EventAgentDispatch    = "agent.dispatch"
	EventSchedule         = "schedule"
	EventChannelMessage   = "channel.message"
	EventJobCompleted     = "job.completed"
	EventJobStepCompleted = "job.step_completed"
	EventDeliverableReady = "deliverable.ready"
)

// ManagerCompletionTriggerPayload returns the stable completion-event fields
// that topology triggers may safely constrain. The daemon enriches this base
// with job- and step-specific fields before publishing the event.
func ManagerCompletionTriggerPayload(pipeline, target string, managerGateReady bool) map[string]any {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "manager"
	}
	payload := map[string]any{
		"source": "daemon:completion",
		"target": target,
	}
	if pipeline = strings.TrimSpace(pipeline); pipeline != "" {
		payload["pipeline"] = pipeline
	}
	if managerGateReady {
		payload["manager_gate_ready"] = true
	}
	return payload
}

// Topology is the parsed + merged set of declared instances for a repo.
type Topology struct {
	// ModelPolicy is the shared runtime/model/effort default for declared seats.
	// Instances inherit omitted values from it; pipeline steps inherit omitted
	// values from their resolved target instance while the runtime family stays
	// compatible.
	ModelPolicy *ModelPolicy
	// Instances is keyed by the declared instance name (the `[instances.<n>]`
	// table key in the TOML).
	Instances map[string]*Instance
	// Locks is keyed by the declared lock name (`[locks.<n>]`).
	Locks map[string]*Lock
	// Channels is keyed by the canonical channel name (`#name`).
	Channels map[string]*Channel
	// Pipelines is keyed by the declared pipeline name (`[pipelines.<n>]`).
	Pipelines map[string]*Pipeline
	// Schedules is keyed by the declared schedule name (`[schedules.<n>]`).
	Schedules map[string]*Schedule
	// Teams is keyed by the declared team name (`[teams.<n>]`).
	Teams map[string]*Team
	// Budgets is keyed by the declared team name (`[budgets.<team>]`).
	Budgets map[string]*Budget
	// Concurrency optionally enables daemon-wide adaptive admission control for
	// ephemeral dispatches. Nil preserves existing static replica/budget behavior.
	Concurrency *Concurrency

	// ReminderLevels are the default soft budget notice percentage thresholds
	// declared under `[budgets].reminder_levels`.
	ReminderLevels []int
	// Authority is the optional daemon verb allowlist policy.
	Authority *Authority
}

// ModelPolicy is the shared default declared under `[model_policy]`.
// Explicit instance and pipeline-step fields remain authoritative.
type ModelPolicy struct {
	Runtime string
	Model   string
	Effort  string
}

// ResolveRuntimePolicy applies an explicit runtime/model/effort override to an
// inherited triple. Model and effort are runtime-family-specific: changing the
// runtime clears inherited selectors from the previous family before any
// explicitly supplied selectors for the new runtime are applied.
func ResolveRuntimePolicy(inherited, override ModelPolicy) ModelPolicy {
	resolved := ModelPolicy{
		Runtime: strings.TrimSpace(inherited.Runtime),
		Model:   strings.TrimSpace(inherited.Model),
		Effort:  strings.TrimSpace(inherited.Effort),
	}
	override.Runtime = strings.TrimSpace(override.Runtime)
	override.Model = strings.TrimSpace(override.Model)
	override.Effort = strings.TrimSpace(override.Effort)
	if override.Runtime != "" {
		if resolved.Runtime != "" && !strings.EqualFold(resolved.Runtime, override.Runtime) {
			resolved.Model = ""
			resolved.Effort = ""
		}
		resolved.Runtime = override.Runtime
	}
	if override.Model != "" {
		resolved.Model = override.Model
	}
	if override.Effort != "" {
		resolved.Effort = override.Effort
	}
	return resolved
}

// Instance is one declared instance.
type Instance struct {
	Name        string
	Agent       string
	Ephemeral   bool
	Runtime     string
	RuntimeBin  string
	Model       string
	Effort      string
	Description string
	// Locks names dispatch locks held while this instance's ephemeral child runs.
	Locks []string
	// Replicas is meaningful only for ephemeral instances. Defaults to 1.
	Replicas int
	// ReapWorktree controls opt-in cleanup of job-owned worker worktrees.
	// Defaults to "never".
	ReapWorktree string
	// Restart controls daemon reconcile relaunch behavior for declared
	// persistent instances. Defaults to "never".
	Restart string
	// Brief controls generated catch-up brief injection for recoverable
	// persistent instances. Defaults to true for persistent instances and false
	// for ephemeral instances.
	Brief bool
	// TokenBudget is a soft per-run allowance surfaced to the spawned agent.
	// TimeBudget is also surfaced to the agent and, for ephemeral dispatches,
	// arms a hard wall-clock watchdog cutoff.
	TokenBudget    int64
	TimeBudget     time.Duration
	HardBudget     bool
	HardMultiplier float64
	// EnvAllow is an optional glob allowlist for the launched process
	// environment. nil means unset/no-op; an empty non-nil slice means only the
	// daemon-required AGENT_TEAM_* environment survives.
	EnvAllow []string
	// Config holds per-instance overrides for the resolved config tree —
	// dotted-path keys flattened from `[instances.<name>.config]` in TOML.
	// Empty when no overrides are declared.
	Config template.Tree
	// Triggers is the ordered list of event-matchers declared for this
	// instance. An empty list means the instance is only invokable via an
	// explicit `agent-team run <name>` (i.e. no event-driven dispatch).
	Triggers []*Trigger

	runtimeDeclared bool
	modelDeclared   bool
	effortDeclared  bool
}

// Lock is a named dispatch semaphore. Slots defaults to 1, making the lock a
// mutex unless the topology author declares more capacity.
type Lock struct {
	Name  string
	Slots int
	Scope string
}

// Channel is a declared pub/sub channel. Undeclared channels still work and
// keep machine-scoped behavior; declarations exist for scoped namespaces.
type Channel struct {
	Name  string
	Scope string
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
	// Merge describes the mechanical merge strategy for jobs created by this
	// pipeline plus the final PR landing mode. Nil means no pipeline-specific
	// merge action was declared.
	Merge *PipelineMerge
	// AutoAdvance, when true, lets the daemon dispatch the next ready step as
	// soon as the prior step's instance exits successfully (respecting gates),
	// instead of waiting for an external `agent-team pipeline tick`. Opt-in;
	// defaults false so existing pipelines keep their manual-advance behavior.
	AutoAdvance bool
	// RedispatchOnReentry, when true, lets a matching trigger reopen a terminal
	// job for the same ticket. Defaults false so board re-entry is idempotent.
	RedispatchOnReentry bool
	// ReapWorktree controls opt-in cleanup for jobs created by this pipeline.
	// Defaults to "never".
	ReapWorktree string
	// InfraSignatures maps stable names to regex patterns that classify failed
	// gate signatures as infrastructure failures.
	InfraSignatures map[string]string
}

// PipelineMerge describes `[pipelines.<name>.merge]`.
type PipelineMerge struct {
	Strategy   string
	Script     string
	Land       string
	OwnedPaths []string
}

// PipelineStep is one target dispatch in a pipeline.
type PipelineStep struct {
	ID               string
	Label            string
	Description      string
	Instructions     string
	Target           string
	Locks            []string
	Workspace        string
	Runtime          string
	RuntimeBin       string
	Model            string
	Effort           string
	After            []string
	Gate             string
	ApprovalRequired bool
	Optional         bool
	Timeout          time.Duration
	TokenBudget      int64
	TimeBudget       time.Duration
	HardBudget       bool
	HardMultiplier   float64
	ReminderLevels   []int
	MaxAttempts      int
	RetryOnCrash     bool

	runtimeDeclared bool
	modelDeclared   bool
	effortDeclared  bool
}

// Schedule is a periodic source of `schedule` events.
type Schedule struct {
	Name       string
	Every      time.Duration
	RunOnStart bool
	Scope      string
	Payload    map[string]any
}

// Team names a group of instances, pipelines, and schedules owned together.
type Team struct {
	Name        string
	Description string
	Instances   []string
	Pipelines   []string
	Schedules   []string
	Channels    []string
}

// Budget declares admission-time resource caps for one team. A zero cap is
// disabled for that dimension.
type Budget struct {
	Team         string
	TokensPerDay int64
	JobsInFlight int
	Allocation   string
	LoadWeight   float64
}

// Concurrency declares daemon-wide adaptive admission settings. Zero values
// for numeric and duration fields mean "use the daemon default" when Enabled.
type Concurrency struct {
	Enabled           bool
	MinCeiling        int
	MaxCeiling        int
	InitialCeiling    int
	TargetLoadPerCore float64
	LoadPerDispatch   float64
	CrashWindow       time.Duration
	CrashThreshold    int
	DecreaseFactor    float64
	StableWindow      time.Duration
	IncreaseStep      int
}

// Authority is the audit/enforcement policy declared in topology.
type Authority struct {
	Enforcement string
	Instances   map[string]*AuthorityRule
	Agents      map[string]*AuthorityRule
	Teams       map[string]*AuthorityRule
}

// AuthorityRule is a verb allowlist for one agent or team. Verbs may be exact
// (`job.gate.set`) or prefix wildcards (`job.*`), with optional scope
// qualifiers such as `:own` and `:team`.
type AuthorityRule struct {
	Allow []string
}

// AuthorityDecision is one audited daemon or CLI action.
type AuthorityDecision struct {
	Instance  string
	Agent     string
	Team      string
	Operator  bool
	Verb      string
	ActorJob  string
	TargetJob string
	// TargetTeam is the owning team recorded on the target job. It is kept
	// separate from Team because persistent managers are not attached to the
	// jobs they manage and may act across more than one declared team.
	TargetTeam string
}

// AuthorityEvaluation is the result of checking one audited action against the
// declared topology allowlists.
type AuthorityEvaluation struct {
	Allowed  bool
	Decision AuthorityDecision
	Sources  []string
}

// Configured reports whether at least one allowlist exists.
func (a *Authority) Configured() bool {
	if a == nil {
		return false
	}
	return len(a.Instances) > 0 || len(a.Agents) > 0 || len(a.Teams) > 0
}

// Enforced reports whether disallowed audited verbs should be refused.
func (a *Authority) Enforced() bool {
	if a == nil {
		return false
	}
	return a.Enforcement == AuthorityModeEnforce
}

// Allows reports whether the actor is allowed to perform the decision's verb.
func (a *Authority) Allows(decision AuthorityDecision) bool {
	return a.Evaluate(decision).Allowed
}

// Evaluate reports whether the actor is allowed to perform the decision's verb
// and records which allowlist sources were considered.
func (a *Authority) Evaluate(decision AuthorityDecision) AuthorityEvaluation {
	eval := AuthorityEvaluation{Allowed: true}
	if !a.Configured() {
		return eval
	}
	decision = cleanAuthorityDecision(decision)
	eval = AuthorityEvaluation{Decision: decision}
	if decision.Verb == "" {
		return eval
	}
	if decision.Operator {
		eval.Allowed = true
		eval.Sources = []string{"operator.token"}
		return eval
	}
	for _, candidate := range []struct {
		source string
		rule   *AuthorityRule
	}{
		{source: "authority.instances." + decision.Instance, rule: a.Instances[decision.Instance]},
		{source: "authority.agents." + decision.Agent, rule: a.Agents[decision.Agent]},
		{source: "authority.teams." + decision.Team, rule: a.Teams[decision.Team]},
	} {
		if candidate.rule == nil {
			continue
		}
		eval.Sources = append(eval.Sources, candidate.source)
		if candidate.rule.Allows(decision) {
			eval.Allowed = true
			return eval
		}
	}
	return eval
}

// SourceDescription returns a compact description of the allowlist source for
// error messages and audit output.
func (e AuthorityEvaluation) SourceDescription() string {
	if len(e.Sources) == 0 {
		return "none"
	}
	return strings.Join(e.Sources, ",")
}

// Allows reports whether this rule includes the decision's verb and scope.
func (r *AuthorityRule) Allows(decision AuthorityDecision) bool {
	if r == nil {
		return false
	}
	decision = cleanAuthorityDecision(decision)
	for _, allow := range r.Allow {
		if authorityAllowMatches(allow, decision) {
			return true
		}
	}
	return false
}

func cleanAuthorityDecision(decision AuthorityDecision) AuthorityDecision {
	return AuthorityDecision{
		Instance:   strings.TrimSpace(decision.Instance),
		Agent:      strings.TrimSpace(decision.Agent),
		Team:       strings.TrimSpace(decision.Team),
		Operator:   decision.Operator,
		Verb:       strings.TrimSpace(decision.Verb),
		ActorJob:   strings.TrimSpace(decision.ActorJob),
		TargetJob:  strings.TrimSpace(decision.TargetJob),
		TargetTeam: strings.TrimSpace(decision.TargetTeam),
	}
}

func authorityAllowMatches(allow string, decision AuthorityDecision) bool {
	pattern, qualifier := splitAuthorityAllow(allow)
	if !authorityVerbMatches(pattern, decision.Verb) {
		return false
	}
	switch qualifier {
	case "":
		return true
	case "own":
		return decision.ActorJob != "" && decision.TargetJob != "" && strings.EqualFold(decision.ActorJob, decision.TargetJob)
	case "team":
		return decision.Team != "" && decision.TargetTeam != "" && strings.EqualFold(decision.Team, decision.TargetTeam)
	default:
		return false
	}
}

func splitAuthorityAllow(value string) (string, string) {
	value = strings.TrimSpace(value)
	verb, qualifier, ok := strings.Cut(value, ":")
	if !ok {
		return value, ""
	}
	return strings.TrimSpace(verb), strings.TrimSpace(qualifier)
}

func authorityVerbMatches(pattern, verb string) bool {
	pattern = strings.TrimSpace(pattern)
	verb = strings.TrimSpace(verb)
	if pattern == "" || verb == "" {
		return false
	}
	if pattern == "*" || pattern == verb {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		return strings.HasPrefix(verb, strings.TrimSuffix(pattern, "*"))
	}
	return false
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
	trace := t.Trace(eventType, payload)
	matched := make([]*Instance, 0, len(trace.MatchedInstanceNames()))
	for _, name := range trace.MatchedInstanceNames() {
		if inst := t.Find(name); inst != nil {
			matched = append(matched, inst)
		}
	}
	return matched
}

// ResolvePipelines returns pipelines whose trigger matches the event.
func (t *Topology) ResolvePipelines(eventType string, payload map[string]any) []*Pipeline {
	if t == nil {
		return nil
	}
	trace := t.Trace(eventType, payload)
	matched := make([]*Pipeline, 0, len(trace.MatchedPipelineNames()))
	for _, name := range trace.MatchedPipelineNames() {
		if pipeline := t.Pipelines[name]; pipeline != nil {
			matched = append(matched, pipeline)
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
	return false, payload
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

// SortedLocks returns declared locks ordered by name for deterministic output.
func (t *Topology) SortedLocks() []*Lock {
	if t == nil {
		return nil
	}
	out := make([]*Lock, 0, len(t.Locks))
	for _, lock := range t.Locks {
		out = append(out, lock)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SortedChannels returns declared channels ordered by canonical name.
func (t *Topology) SortedChannels() []*Channel {
	if t == nil {
		return nil
	}
	out := make([]*Channel, 0, len(t.Channels))
	for _, channel := range t.Channels {
		out = append(out, channel)
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

// SortedBudgets returns budgets ordered by team for deterministic output.
func (t *Topology) SortedBudgets() []*Budget {
	if t == nil {
		return nil
	}
	out := make([]*Budget, 0, len(t.Budgets))
	for _, budget := range t.Budgets {
		out = append(out, budget)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Team < out[j].Team })
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

// FindRuntimeInstance returns the declared instance that owns a runtime
// instance name. Exact names win; otherwise ephemeral names such as
// "worker-squ-123" match the longest declared "worker-" prefix.
func (t *Topology) FindRuntimeInstance(name, agent string) *Instance {
	if t == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	agent = strings.TrimSpace(agent)
	if inst := t.Find(name); inst != nil {
		return inst
	}
	var best *Instance
	for _, inst := range t.SortedInstances() {
		if inst == nil || inst.Name == "" {
			continue
		}
		if agent != "" && inst.Agent != agent {
			continue
		}
		if !strings.HasPrefix(name, inst.Name+"-") {
			continue
		}
		if best == nil || len(inst.Name) > len(best.Name) {
			best = inst
		}
	}
	return best
}

// FindTeam returns the declared team by name, or nil if none.
func (t *Topology) FindTeam(name string) *Team {
	if t == nil {
		return nil
	}
	return t.Teams[name]
}

// FindBudget returns the budget declared for team, or nil if none.
func (t *Topology) FindBudget(team string) *Budget {
	if t == nil {
		return nil
	}
	return t.Budgets[strings.TrimSpace(team)]
}

// TeamForInstance returns the owning team declared for a runtime instance, or
// empty. Exact declared instance names win; ephemeral names match the longest
// declared "<instance>-" prefix.
func (t *Topology) TeamForInstance(name string) string {
	if t == nil {
		return ""
	}
	name = strings.TrimSpace(name)
	for _, team := range t.SortedTeams() {
		for _, ref := range team.Instances {
			if ref == name {
				return team.Name
			}
		}
	}
	bestTeam := ""
	bestLen := -1
	for _, team := range t.SortedTeams() {
		for _, ref := range team.Instances {
			if ref == "" || !strings.HasPrefix(name, ref+"-") {
				continue
			}
			if len(ref) > bestLen {
				bestTeam = team.Name
				bestLen = len(ref)
			}
		}
	}
	return bestTeam
}

// TeamForPipeline returns the owning team declared for a pipeline. Topology
// validation rejects managed pipelines with missing or ambiguous ownership, so
// runtime callers receive one stable team for every validated manager path.
func (t *Topology) TeamForPipeline(name string) string {
	if t == nil {
		return ""
	}
	name = strings.TrimSpace(name)
	for _, team := range t.SortedTeams() {
		for _, ref := range team.Pipelines {
			if ref == name {
				return team.Name
			}
		}
	}
	return ""
}

// AuthorityAllowlistForInstance returns the topology-declared authority
// patterns that apply to a runtime instance. Per-agent and per-team rules are
// additive, mirroring Authority.Allows.
func (t *Topology) AuthorityAllowlistForInstance(instance, agent string) []string {
	if t == nil || t.Authority == nil || !t.Authority.Configured() {
		return nil
	}
	instance = strings.TrimSpace(instance)
	agent = strings.TrimSpace(agent)
	if inst := t.FindRuntimeInstance(instance, agent); inst != nil && strings.TrimSpace(inst.Agent) != "" {
		agent = strings.TrimSpace(inst.Agent)
	}
	seen := map[string]bool{}
	var out []string
	addRule := func(rule *AuthorityRule) {
		if rule == nil {
			return
		}
		for _, allow := range rule.Allow {
			allow = strings.TrimSpace(allow)
			if allow == "" || seen[allow] {
				continue
			}
			seen[allow] = true
			out = append(out, allow)
		}
	}
	if agent != "" {
		addRule(t.Authority.Agents[agent])
	}
	if instance != "" {
		addRule(t.Authority.Instances[instance])
	}
	if inst := t.FindRuntimeInstance(instance, agent); inst != nil && strings.TrimSpace(inst.Name) != "" {
		addRule(t.Authority.Instances[strings.TrimSpace(inst.Name)])
	}
	if team := t.TeamForInstance(instance); team != "" {
		addRule(t.Authority.Teams[team])
	}
	sort.Strings(out)
	return out
}

// TeamForSchedule returns the owning team declared for schedule, or empty.
func (t *Topology) TeamForSchedule(name string) string {
	if t == nil {
		return ""
	}
	name = strings.TrimSpace(name)
	for _, team := range t.SortedTeams() {
		for _, ref := range team.Schedules {
			if ref == name {
				return team.Name
			}
		}
	}
	return ""
}

// TeamForChannel returns the owning team declared for channel, or empty.
func (t *Topology) TeamForChannel(name string) string {
	if t == nil {
		return ""
	}
	canonical, err := normalizeChannelName(name)
	if err != nil {
		return ""
	}
	for _, team := range t.SortedTeams() {
		for _, ref := range team.Channels {
			if ref == canonical {
				return team.Name
			}
		}
	}
	return ""
}

// rawTopology mirrors the on-wire TOML schema. We keep parsing in two stages
// so the public Topology can carry validated, normalised values regardless of
// how lenient toml.Decode is.
type rawTopology struct {
	ModelPolicy *rawModelPolicy         `toml:"model_policy"`
	Instances   map[string]*rawInstance `toml:"instances"`
	Locks       map[string]*rawLock     `toml:"locks"`
	Channels    map[string]*rawChannel  `toml:"channels"`
	Pipelines   map[string]*rawPipeline `toml:"pipelines"`
	Schedules   map[string]*rawSchedule `toml:"schedules"`
	Teams       map[string]*rawTeam     `toml:"teams"`
	Budgets     map[string]any          `toml:"budgets"`
	Concurrency *rawConcurrency         `toml:"concurrency"`
	Authority   *rawAuthority           `toml:"authority"`
}

type rawModelPolicy struct {
	Runtime any `toml:"runtime"`
	Model   any `toml:"model"`
	Effort  any `toml:"effort"`
}

type rawInstance struct {
	Agent          string           `toml:"agent"`
	Ephemeral      bool             `toml:"ephemeral"`
	Runtime        any              `toml:"runtime"`
	RuntimeBin     any              `toml:"runtime_bin"`
	Model          any              `toml:"model"`
	Effort         any              `toml:"effort"`
	Description    string           `toml:"description"`
	Locks          []string         `toml:"locks"`
	Replicas       *int             `toml:"replicas"`
	ReapWorktree   string           `toml:"reap_worktree"`
	Restart        string           `toml:"restart"`
	Brief          *bool            `toml:"brief"`
	TokenBudget    any              `toml:"token_budget"`
	TimeBudget     string           `toml:"time_budget"`
	Hard           bool             `toml:"hard"`
	HardMultiplier any              `toml:"hard_multiplier"`
	EnvAllow       []string         `toml:"env_allow"`
	Config         map[string]any   `toml:"config"`
	Triggers       []map[string]any `toml:"triggers"`
}

type rawLock struct {
	Slots *int   `toml:"slots"`
	Scope string `toml:"scope"`
}

type rawChannel struct {
	Scope string `toml:"scope"`
}

type rawPipeline struct {
	Trigger             map[string]any    `toml:"trigger"`
	Steps               []map[string]any  `toml:"steps"`
	Merge               *rawPipelineMerge `toml:"merge"`
	Land                string            `toml:"land"`
	InfraSignatures     map[string]string `toml:"infra_signatures"`
	AutoAdvance         bool              `toml:"auto_advance"`
	RedispatchOnReentry bool              `toml:"redispatch_on_reentry"`
	ReapWorktree        string            `toml:"reap_worktree"`
}

type rawPipelineMerge struct {
	Strategy   string   `toml:"strategy"`
	Script     string   `toml:"script"`
	Land       string   `toml:"land"`
	OwnedPaths []string `toml:"owned_paths"`
	Owns       []string `toml:"owns"`
}

type rawSchedule struct {
	Every      string         `toml:"every"`
	Interval   string         `toml:"interval"`
	RunOnStart bool           `toml:"run_on_start"`
	Scope      string         `toml:"scope"`
	Payload    map[string]any `toml:"payload"`
}

type rawTeam struct {
	Description string   `toml:"description"`
	Instances   []string `toml:"instances"`
	Pipelines   []string `toml:"pipelines"`
	Schedules   []string `toml:"schedules"`
	Channels    []string `toml:"channels"`
}

type rawBudget struct {
	TokensPerDay *int64   `toml:"tokens_per_day"`
	JobsInFlight *int     `toml:"jobs_in_flight"`
	Allocation   string   `toml:"allocation"`
	LoadWeight   *float64 `toml:"load_weight"`
}

type rawConcurrency struct {
	Enabled           bool     `toml:"enabled"`
	MinCeiling        *int     `toml:"min_ceiling"`
	MaxCeiling        *int     `toml:"max_ceiling"`
	InitialCeiling    *int     `toml:"initial_ceiling"`
	TargetLoadPerCore *float64 `toml:"target_load_per_core"`
	LoadPerDispatch   *float64 `toml:"load_per_dispatch"`
	CrashWindow       string   `toml:"crash_window"`
	CrashThreshold    *int     `toml:"crash_threshold"`
	DecreaseFactor    *float64 `toml:"decrease_factor"`
	StableWindow      string   `toml:"stable_window"`
	IncreaseStep      *int     `toml:"increase_step"`
}

type rawAuthority struct {
	Enforcement string                       `toml:"enforcement"`
	Instances   map[string]*rawAuthorityRule `toml:"instances"`
	Agents      map[string]*rawAuthorityRule `toml:"agents"`
	Teams       map[string]*rawAuthorityRule `toml:"teams"`
}

type rawAuthorityRule struct {
	Allow []string `toml:"allow"`
	Verbs []string `toml:"verbs"`
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
	modelPolicy, err := finaliseModelPolicy(raw.ModelPolicy)
	if err != nil {
		return nil, err
	}
	budgets, reminderLevels, err := finaliseBudgets(raw.Budgets)
	if err != nil {
		return nil, err
	}
	concurrency, err := finaliseConcurrency(raw.Concurrency)
	if err != nil {
		return nil, err
	}
	t := &Topology{
		ModelPolicy:    modelPolicy,
		Instances:      make(map[string]*Instance, len(raw.Instances)),
		Locks:          make(map[string]*Lock, len(raw.Locks)),
		Channels:       make(map[string]*Channel, len(raw.Channels)),
		Pipelines:      make(map[string]*Pipeline, len(raw.Pipelines)),
		Schedules:      make(map[string]*Schedule, len(raw.Schedules)),
		Teams:          make(map[string]*Team, len(raw.Teams)),
		Budgets:        budgets,
		Concurrency:    concurrency,
		ReminderLevels: reminderLevels,
	}
	for name, rl := range raw.Locks {
		if rl == nil {
			continue
		}
		lock, err := finaliseLock(name, rl)
		if err != nil {
			return nil, err
		}
		t.Locks[name] = lock
	}
	for name, rc := range raw.Channels {
		if rc == nil {
			continue
		}
		channel, err := finaliseChannel(name, rc)
		if err != nil {
			return nil, err
		}
		t.Channels[channel.Name] = channel
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
		applyPipelineReminderDefaults(p, t.ReminderLevels)
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
	applyModelPolicyDefaults(t)
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
	authority, err := finaliseAuthority(raw.Authority)
	if err != nil {
		return nil, err
	}
	t.Authority = authority
	if validateTeamRefs {
		if err := validateLockReferences(t); err != nil {
			return nil, err
		}
		if err := validateBudgetReferences(t); err != nil {
			return nil, err
		}
		if err := validatePipelineAuthoritySatisfiability(t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func finaliseModelPolicy(raw *rawModelPolicy) (*ModelPolicy, error) {
	if raw == nil {
		return nil, nil
	}
	runtime, err := parseStepRuntime(raw.Runtime)
	if err != nil {
		return nil, fmt.Errorf("model_policy: %w", err)
	}
	model, err := parseOptionalText(raw.Model, "model")
	if err != nil {
		return nil, fmt.Errorf("model_policy: %w", err)
	}
	effort, err := parseOptionalText(raw.Effort, "effort")
	if err != nil {
		return nil, fmt.Errorf("model_policy: %w", err)
	}
	return &ModelPolicy{Runtime: runtime, Model: model, Effort: effort}, nil
}

func applyModelPolicyDefaults(t *Topology) {
	if t == nil || t.ModelPolicy == nil {
		return
	}
	policy := *t.ModelPolicy
	for _, inst := range t.Instances {
		override := ModelPolicy{}
		if inst.runtimeDeclared {
			override.Runtime = inst.Runtime
		}
		if inst.modelDeclared {
			override.Model = inst.Model
		}
		if inst.effortDeclared {
			override.Effort = inst.Effort
		}
		resolved := ResolveRuntimePolicy(policy, override)
		inst.Runtime = resolved.Runtime
		inst.Model = resolved.Model
		inst.Effort = resolved.Effort
	}
	for _, pipeline := range t.Pipelines {
		for _, step := range pipeline.Steps {
			inherited := policy
			target := t.Instances[step.Target]
			if target != nil {
				inherited = ModelPolicy{Runtime: target.Runtime, Model: target.Model, Effort: target.Effort}
			}
			override := ModelPolicy{}
			if step.runtimeDeclared {
				override.Runtime = step.Runtime
			}
			if step.modelDeclared {
				override.Model = step.Model
			}
			if step.effortDeclared {
				override.Effort = step.Effort
			}
			resolved := ResolveRuntimePolicy(inherited, override)
			step.Runtime = resolved.Runtime
			step.Model = resolved.Model
			step.Effort = resolved.Effort
		}
	}
}

func finaliseLock(name string, rl *rawLock) (*Lock, error) {
	name = strings.TrimSpace(name)
	if err := validateLockName(name); err != nil {
		return nil, fmt.Errorf("lock %q: %w", name, err)
	}
	scope, err := normalizeResourceScope(rl.Scope)
	if err != nil {
		return nil, fmt.Errorf("lock %q: %w", name, err)
	}
	slots := 1
	if rl.Slots != nil {
		if *rl.Slots < 1 {
			return nil, fmt.Errorf("lock %q: slots must be >= 1", name)
		}
		slots = *rl.Slots
	}
	return &Lock{Name: name, Slots: slots, Scope: scope}, nil
}

func finaliseChannel(name string, rc *rawChannel) (*Channel, error) {
	canonical, err := normalizeChannelName(name)
	if err != nil {
		return nil, fmt.Errorf("channel %q: %w", name, err)
	}
	scope, err := normalizeResourceScope(rc.Scope)
	if err != nil {
		return nil, fmt.Errorf("channel %q: %w", name, err)
	}
	return &Channel{Name: canonical, Scope: scope}, nil
}

func finaliseInstance(name string, ri *rawInstance) (*Instance, error) {
	if strings.TrimSpace(ri.Agent) == "" {
		return nil, fmt.Errorf("instance %q: `agent` is required", name)
	}
	locks, err := parseLockRefs("instance", name, "locks", ri.Locks)
	if err != nil {
		return nil, err
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
	runtime, err := parseStepRuntime(ri.Runtime)
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	runtimeBin, err := parseStepText(ri.RuntimeBin, "runtime_bin")
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	model, err := parseOptionalText(ri.Model, "model")
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	effort, err := parseOptionalText(ri.Effort, "effort")
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	reapWorktree, err := worktreepolicy.Normalize(ri.ReapWorktree)
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	restart, err := normalizeRestartPolicy(ri.Restart)
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	brief := !ri.Ephemeral
	if ri.Brief != nil {
		brief = *ri.Brief
	}
	tokenBudget, err := allowance.ParseTokenValue(ri.TokenBudget, "token_budget")
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	timeBudget, err := parseOptionalDurationString(ri.TimeBudget, "time_budget")
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	hardMultiplier, err := allowance.ParseHardMultiplierValue(ri.HardMultiplier, "hard_multiplier")
	if err != nil {
		return nil, fmt.Errorf("instance %q: %w", name, err)
	}
	envAllow, err := finaliseEnvAllow(name, ri.EnvAllow)
	if err != nil {
		return nil, err
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
		Name:            name,
		Agent:           ri.Agent,
		Ephemeral:       ri.Ephemeral,
		Runtime:         runtime,
		RuntimeBin:      runtimeBin,
		Model:           model,
		Effort:          effort,
		Description:     ri.Description,
		Locks:           locks,
		Replicas:        replicas,
		ReapWorktree:    reapWorktree,
		Restart:         restart,
		Brief:           brief,
		TokenBudget:     tokenBudget,
		TimeBudget:      timeBudget,
		HardBudget:      ri.Hard,
		HardMultiplier:  hardMultiplier,
		EnvAllow:        envAllow,
		Config:          cfg,
		Triggers:        triggers,
		runtimeDeclared: runtime != "",
		modelDeclared:   model != "",
		effortDeclared:  effort != "",
	}, nil
}

func finaliseEnvAllow(instance string, raw []string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	for i, item := range raw {
		pattern := strings.TrimSpace(item)
		if pattern == "" {
			return nil, fmt.Errorf("instance %q: env_allow[%d]: must be non-empty", instance, i)
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("instance %q: env_allow[%d]: invalid glob: %w", instance, i, err)
		}
		out = append(out, pattern)
	}
	return out, nil
}

func normalizeRestartPolicy(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return RestartNever, nil
	}
	switch value {
	case RestartNever, RestartOnFailure, RestartAlways:
		return value, nil
	default:
		return "", fmt.Errorf("restart must be never, on-failure, or always")
	}
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
	reapWorktree, err := worktreepolicy.Normalize(rp.ReapWorktree)
	if err != nil {
		return nil, fmt.Errorf("pipeline %q: %w", name, err)
	}
	merge, err := parsePipelineMerge(name, rp.Merge, rp.Land)
	if err != nil {
		return nil, err
	}
	infraSignatures, err := parsePipelineInfraSignatures(name, rp.InfraSignatures)
	if err != nil {
		return nil, err
	}
	return &Pipeline{Name: name, Trigger: trigger, Steps: steps, Merge: merge, AutoAdvance: rp.AutoAdvance, RedispatchOnReentry: rp.RedispatchOnReentry, ReapWorktree: reapWorktree, InfraSignatures: infraSignatures}, nil
}

func parsePipelineMerge(name string, raw *rawPipelineMerge, pipelineLand string) (*PipelineMerge, error) {
	if raw == nil {
		if strings.TrimSpace(pipelineLand) == "" {
			return nil, nil
		}
		land, err := mergepolicy.NormalizeLand(pipelineLand)
		if err != nil {
			return nil, fmt.Errorf("pipeline %q: %w", name, err)
		}
		if land != strings.ToLower(strings.TrimSpace(pipelineLand)) {
			return nil, fmt.Errorf("pipeline %q: land %q must be normalized as %q", name, pipelineLand, land)
		}
		return &PipelineMerge{Strategy: mergepolicy.StrategySquash, Land: land}, nil
	}
	strategyRaw := strings.TrimSpace(raw.Strategy)
	strategy := mergepolicy.StrategySquash
	if strategyRaw == "" && strings.TrimSpace(raw.Land) == "" && strings.TrimSpace(pipelineLand) == "" {
		var err error
		strategy, err = mergepolicy.NormalizeStrategy(raw.Strategy)
		if err != nil {
			return nil, fmt.Errorf("pipeline %q merge: %w", name, err)
		}
	} else if strategyRaw != "" {
		var err error
		strategy, err = mergepolicy.NormalizeStrategy(raw.Strategy)
		if err != nil {
			return nil, fmt.Errorf("pipeline %q merge: %w", name, err)
		}
	}
	land, err := parsePipelineLand(name, pipelineLand, raw.Land)
	if err != nil {
		return nil, err
	}
	script := strings.TrimSpace(raw.Script)
	switch strategy {
	case mergepolicy.StrategyScript:
		if script == "" {
			return nil, fmt.Errorf("pipeline %q merge: script is required when strategy is script", name)
		}
	default:
		if script != "" {
			return nil, fmt.Errorf("pipeline %q merge: script is only valid when strategy is script", name)
		}
	}
	ownedPaths, err := parseOwnedMergePaths(name, raw)
	if err != nil {
		return nil, err
	}
	return &PipelineMerge{Strategy: strategy, Script: script, Land: land, OwnedPaths: ownedPaths}, nil
}

func parsePipelineLand(name, pipelineLand, mergeLand string) (string, error) {
	pipelineLand = strings.TrimSpace(pipelineLand)
	mergeLand = strings.TrimSpace(mergeLand)
	if pipelineLand != "" {
		normalized, err := mergepolicy.NormalizeLand(pipelineLand)
		if err != nil {
			return "", fmt.Errorf("pipeline %q: %w", name, err)
		}
		if normalized != strings.ToLower(pipelineLand) {
			return "", fmt.Errorf("pipeline %q: land %q must be normalized as %q", name, pipelineLand, normalized)
		}
		pipelineLand = normalized
	}
	if mergeLand != "" {
		normalized, err := mergepolicy.NormalizeLand(mergeLand)
		if err != nil {
			return "", fmt.Errorf("pipeline %q merge: %w", name, err)
		}
		if normalized != strings.ToLower(mergeLand) {
			return "", fmt.Errorf("pipeline %q merge: land %q must be normalized as %q", name, mergeLand, normalized)
		}
		mergeLand = normalized
	}
	if pipelineLand != "" && mergeLand != "" && pipelineLand != mergeLand {
		return "", fmt.Errorf("pipeline %q merge: land %q conflicts with pipeline land %q", name, mergeLand, pipelineLand)
	}
	if mergeLand != "" {
		return mergeLand, nil
	}
	if pipelineLand != "" {
		return pipelineLand, nil
	}
	return "", nil
}

func parseOwnedMergePaths(name string, raw *rawPipelineMerge) ([]string, error) {
	paths := append([]string(nil), raw.OwnedPaths...)
	paths = append(paths, raw.Owns...)
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for i, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("pipeline %q merge owned_paths[%d]: must be non-empty", name, i)
		}
		if strings.HasPrefix(p, "/") {
			return nil, fmt.Errorf("pipeline %q merge owned_paths[%d]: must be repo-relative", name, i)
		}
		p = strings.TrimPrefix(p, "./")
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func parsePipelineInfraSignatures(name string, raw map[string]string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for key, pattern := range raw {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("pipeline %q infra_signatures: name must be non-empty", name)
		}
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return nil, fmt.Errorf("pipeline %q infra_signatures.%s: pattern must be non-empty", name, key)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return nil, fmt.Errorf("pipeline %q infra_signatures.%s: invalid regex: %w", name, key, err)
		}
		out[key] = pattern
	}
	return out, nil
}

func finaliseSchedule(name string, rs *rawSchedule) (*Schedule, error) {
	name = strings.TrimSpace(name)
	if err := validateLockName(name); err != nil {
		return nil, fmt.Errorf("schedule %q: %w", name, err)
	}
	scope, err := normalizeResourceScope(rs.Scope)
	if err != nil {
		return nil, fmt.Errorf("schedule %q: %w", name, err)
	}
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
	return &Schedule{Name: name, Every: every, RunOnStart: rs.RunOnStart, Scope: scope, Payload: payload}, nil
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
	channels, err := cleanChannelRefs("team", name, "channels", rt.Channels)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 && len(pipelines) == 0 && len(schedules) == 0 && len(channels) == 0 {
		return nil, fmt.Errorf("team %q: at least one of instances, pipelines, schedules, or channels is required", name)
	}
	if !validateRefs {
		return &Team{
			Name:        name,
			Description: strings.TrimSpace(rt.Description),
			Instances:   instances,
			Pipelines:   pipelines,
			Schedules:   schedules,
			Channels:    channels,
		}, nil
	}
	team := &Team{
		Name:        name,
		Description: strings.TrimSpace(rt.Description),
		Instances:   instances,
		Pipelines:   pipelines,
		Schedules:   schedules,
		Channels:    channels,
	}
	if err := validateTeamReferences(t, team); err != nil {
		return nil, err
	}
	return team, nil
}

func finaliseBudgets(raw map[string]any) (map[string]*Budget, []int, error) {
	budgets := make(map[string]*Budget, len(raw))
	var reminderLevels []int
	for name, value := range raw {
		if strings.TrimSpace(name) == "reminder_levels" {
			levels, err := parseStepReminderLevels(value)
			if err != nil {
				return nil, nil, fmt.Errorf("budgets.reminder_levels: %w", err)
			}
			reminderLevels = levels
			continue
		}
		rb, err := rawBudgetFromValue(name, value)
		if err != nil {
			return nil, nil, err
		}
		if rb == nil {
			continue
		}
		budget, err := finaliseBudget(name, rb)
		if err != nil {
			return nil, nil, err
		}
		budgets[budget.Team] = budget
	}
	return budgets, reminderLevels, nil
}

func rawBudgetFromValue(name string, value any) (*rawBudget, error) {
	if value == nil {
		return nil, nil
	}
	if rb, ok := value.(*rawBudget); ok {
		return rb, nil
	}
	if rb, ok := value.(rawBudget); ok {
		return &rb, nil
	}
	values, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("budget %q must be a table", name)
	}
	var rb rawBudget
	if rawTokens, ok := values["tokens_per_day"]; ok {
		tokens, err := rawInt64(rawTokens, fmt.Sprintf("budget %q: tokens_per_day", name))
		if err != nil {
			return nil, err
		}
		rb.TokensPerDay = &tokens
	}
	if rawJobs, ok := values["jobs_in_flight"]; ok {
		jobs, err := rawInt(rawJobs, fmt.Sprintf("budget %q: jobs_in_flight", name))
		if err != nil {
			return nil, err
		}
		rb.JobsInFlight = &jobs
	}
	if rawAllocation, ok := values["allocation"]; ok {
		allocation, ok := rawAllocation.(string)
		if !ok {
			return nil, fmt.Errorf("budget %q: allocation must be a string", name)
		}
		rb.Allocation = allocation
	}
	if rawWeight, ok := values["load_weight"]; ok {
		weight, err := rawFloat64(rawWeight, fmt.Sprintf("budget %q: load_weight", name))
		if err != nil {
			return nil, err
		}
		rb.LoadWeight = &weight
	}
	return &rb, nil
}

func rawInt64(raw any, field string) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%s must be an integer", field)
	}
}

func rawInt(raw any, field string) (int, error) {
	v, err := rawInt64(raw, field)
	if err != nil {
		return 0, err
	}
	out := int(v)
	if int64(out) != v {
		return 0, fmt.Errorf("%s is too large", field)
	}
	return out, nil
}

func rawFloat64(raw any, field string) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("%s must be a number", field)
	}
}

func finaliseConcurrency(raw *rawConcurrency) (*Concurrency, error) {
	if raw == nil {
		return nil, nil
	}
	cfg := &Concurrency{Enabled: raw.Enabled}
	if raw.MinCeiling != nil {
		if *raw.MinCeiling < 0 {
			return nil, fmt.Errorf("concurrency.min_ceiling must be >= 0")
		}
		cfg.MinCeiling = *raw.MinCeiling
	}
	if raw.MaxCeiling != nil {
		if *raw.MaxCeiling < 0 {
			return nil, fmt.Errorf("concurrency.max_ceiling must be >= 0")
		}
		cfg.MaxCeiling = *raw.MaxCeiling
	}
	if raw.InitialCeiling != nil {
		if *raw.InitialCeiling < 0 {
			return nil, fmt.Errorf("concurrency.initial_ceiling must be >= 0")
		}
		cfg.InitialCeiling = *raw.InitialCeiling
	}
	if raw.TargetLoadPerCore != nil {
		if *raw.TargetLoadPerCore <= 0 {
			return nil, fmt.Errorf("concurrency.target_load_per_core must be > 0")
		}
		cfg.TargetLoadPerCore = *raw.TargetLoadPerCore
	}
	if raw.LoadPerDispatch != nil {
		if *raw.LoadPerDispatch <= 0 {
			return nil, fmt.Errorf("concurrency.load_per_dispatch must be > 0")
		}
		cfg.LoadPerDispatch = *raw.LoadPerDispatch
	}
	if raw.CrashWindow != "" {
		window, err := parseOptionalDurationString(raw.CrashWindow, "concurrency.crash_window")
		if err != nil {
			return nil, err
		}
		cfg.CrashWindow = window
	}
	if raw.CrashThreshold != nil {
		if *raw.CrashThreshold < 0 {
			return nil, fmt.Errorf("concurrency.crash_threshold must be >= 0")
		}
		cfg.CrashThreshold = *raw.CrashThreshold
	}
	if raw.DecreaseFactor != nil {
		if *raw.DecreaseFactor <= 0 || *raw.DecreaseFactor >= 1 {
			return nil, fmt.Errorf("concurrency.decrease_factor must be > 0 and < 1")
		}
		cfg.DecreaseFactor = *raw.DecreaseFactor
	}
	if raw.StableWindow != "" {
		window, err := parseOptionalDurationString(raw.StableWindow, "concurrency.stable_window")
		if err != nil {
			return nil, err
		}
		cfg.StableWindow = window
	}
	if raw.IncreaseStep != nil {
		if *raw.IncreaseStep < 0 {
			return nil, fmt.Errorf("concurrency.increase_step must be >= 0")
		}
		cfg.IncreaseStep = *raw.IncreaseStep
	}
	if cfg.MaxCeiling > 0 && cfg.MinCeiling > cfg.MaxCeiling {
		return nil, fmt.Errorf("concurrency.min_ceiling must be <= max_ceiling")
	}
	if cfg.MaxCeiling > 0 && cfg.InitialCeiling > cfg.MaxCeiling {
		return nil, fmt.Errorf("concurrency.initial_ceiling must be <= max_ceiling")
	}
	if cfg.InitialCeiling > 0 && cfg.MinCeiling > cfg.InitialCeiling {
		return nil, fmt.Errorf("concurrency.min_ceiling must be <= initial_ceiling")
	}
	return cfg, nil
}

func finaliseBudget(name string, rb *rawBudget) (*Budget, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("budget team name is required")
	}
	tokensPerDay := int64(0)
	if rb.TokensPerDay != nil {
		if *rb.TokensPerDay < 0 {
			return nil, fmt.Errorf("budget %q: tokens_per_day must be >= 0", name)
		}
		tokensPerDay = *rb.TokensPerDay
	}
	jobsInFlight := 0
	if rb.JobsInFlight != nil {
		if *rb.JobsInFlight < 0 {
			return nil, fmt.Errorf("budget %q: jobs_in_flight must be >= 0", name)
		}
		jobsInFlight = *rb.JobsInFlight
	}
	loadWeight := 1.0
	if rb.LoadWeight != nil {
		if *rb.LoadWeight <= 0 {
			return nil, fmt.Errorf("budget %q: load_weight must be > 0", name)
		}
		loadWeight = *rb.LoadWeight
	}
	allocation, err := NormalizeBudgetAllocation(rb.Allocation)
	if err != nil {
		return nil, fmt.Errorf("budget %q: %w", name, err)
	}
	return &Budget{Team: name, TokensPerDay: tokensPerDay, JobsInFlight: jobsInFlight, Allocation: allocation, LoadWeight: loadWeight}, nil
}

// NormalizeBudgetAllocation validates a budget allocation mode. Empty means
// oversubscribe so existing topology files keep their phase-1 behavior.
func NormalizeBudgetAllocation(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return BudgetAllocationOversubscribe, nil
	}
	switch value {
	case BudgetAllocationOversubscribe, BudgetAllocationReserve:
		return value, nil
	default:
		return "", fmt.Errorf("allocation must be reserve or oversubscribe")
	}
}

func applyPipelineReminderDefaults(p *Pipeline, levels []int) {
	if p == nil || len(levels) == 0 {
		return
	}
	for _, step := range p.Steps {
		if step == nil || len(step.ReminderLevels) > 0 {
			continue
		}
		step.ReminderLevels = append([]int(nil), levels...)
	}
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
	for _, ref := range team.Channels {
		if t.Channels[ref] == nil {
			return fmt.Errorf("team %q: channels references unknown channel %q", team.Name, ref)
		}
	}
	return nil
}

func validateBudgetReferences(t *Topology) error {
	if t == nil {
		return nil
	}
	for _, budget := range t.SortedBudgets() {
		if t.Teams[budget.Team] == nil {
			return fmt.Errorf("budget %q: references unknown team %q", budget.Team, budget.Team)
		}
	}
	return nil
}

// ValidateAuthoritySatisfiability checks that every declaratively managed
// pipeline has unambiguous persistent receivers for its manual-gate and
// terminal completion events. Under enforcement it also checks that each
// receiver can perform its route's duties. It intentionally evaluates concrete
// AuthorityDecisions instead of searching allowlist strings, so wildcard,
// instance/agent/team composition, and scope qualifiers stay identical to the
// runtime authorizer.
func (t *Topology) ValidateAuthoritySatisfiability() error {
	return validatePipelineAuthoritySatisfiability(t)
}

func validatePipelineAuthoritySatisfiability(t *Topology) error {
	if t == nil {
		return nil
	}
	for _, pipeline := range t.SortedPipelines() {
		routes, managed, err := t.resolvePipelineManagers(pipeline)
		if err != nil {
			return err
		}
		if !managed {
			continue
		}
		team, err := t.pipelineOwningTeam(pipeline.Name)
		if err != nil {
			return err
		}
		if t.Authority == nil || !t.Authority.Configured() || !t.Authority.Enforced() {
			continue
		}
		for _, route := range routes {
			for _, verb := range route.verbs {
				decision := t.pipelineManagerAuthorityDecision(route.owner, verb, pipeline.Name, team)
				eval := t.Authority.Evaluate(decision)
				if eval.Allowed {
					continue
				}
				requiredGrant := verb
				if strings.HasPrefix(verb, "job.") && decision.Team != "" && strings.EqualFold(decision.Team, team) {
					requiredGrant += ":team"
				}
				return fmt.Errorf("pipeline %q: owner %q for %s lacks effective authority %q for team %q (runtime sources: %s); add %q to [authority.instances.%s].allow", pipeline.Name, route.owner.Name, route.duty, requiredGrant, team, eval.SourceDescription(), requiredGrant, route.owner.Name)
			}
		}
	}
	return nil
}

// pipelineManagerAuthorityDecision models the target context supplied by the
// runtime path for each mandatory manager duty. Job mutations resolve the
// target job and its durable topology team; non-job actions such as
// event.publish have neither and therefore cannot satisfy :own or :team grants.
func (t *Topology) pipelineManagerAuthorityDecision(owner *Instance, verb, pipeline, pipelineTeam string) AuthorityDecision {
	decision := AuthorityDecision{
		Instance: owner.Name,
		Agent:    owner.Agent,
		Team:     t.TeamForInstance(owner.Name),
		Verb:     verb,
	}
	if strings.HasPrefix(verb, "job.") {
		// The concrete job ID does not exist at topology-load time. A stable,
		// non-empty representative preserves the runtime scope semantics:
		// persistent managers still have no ActorJob, while the target does.
		decision.TargetJob = "pipeline-job:" + pipeline
		decision.TargetTeam = pipelineTeam
	}
	return decision
}

type pipelineManagerRoute struct {
	owner *Instance
	duty  string
	verbs []string
}

func (t *Topology) resolvePipelineManagers(pipeline *Pipeline) ([]pipelineManagerRoute, bool, error) {
	if pipeline == nil {
		return nil, false, nil
	}
	owners := map[string]bool{}
	for _, step := range pipeline.Steps {
		if step == nil || step.Gate != "manual" {
			continue
		}
		if target := strings.TrimSpace(step.Target); target != "" {
			owners[target] = true
		}
	}
	terminalVerbs := pipelineTerminalManagerRequiredVerbs(pipeline)
	if len(owners) == 0 && len(terminalVerbs) == 0 {
		return nil, false, nil
	}

	var routes []pipelineManagerRoute
	terminalTarget := ""
	if len(owners) > 0 {
		names := sortedStringKeys(owners)
		if len(names) != 1 {
			return nil, true, fmt.Errorf("pipeline %q: ambiguous managing instances from manual gates: %s", pipeline.Name, strings.Join(names, ", "))
		}
		owner := t.Instances[names[0]]
		if owner == nil {
			return nil, true, fmt.Errorf("pipeline %q: unsupported managing instance %q from manual gate: instance is not declared", pipeline.Name, names[0])
		}
		if owner.Ephemeral {
			return nil, true, fmt.Errorf("pipeline %q: unsupported managing instance %q from manual gate: owner must be persistent", pipeline.Name, owner.Name)
		}
		for _, managerGateReady := range []bool{false, true} {
			payload := ManagerCompletionTriggerPayload(pipeline.Name, owner.Name, managerGateReady)
			if candidate, fields, ok := t.unsupportedPersistentCompletionOwner(EventJobStepCompleted, payload); ok {
				return nil, true, fmt.Errorf("%v (manager_gate_ready=%t)", unsupportedDynamicCompletionOwnerError(pipeline.Name, EventJobStepCompleted, candidate, fields), managerGateReady)
			}
			candidates := t.persistentCompletionCandidates(EventJobStepCompleted, payload)
			if len(candidates) == 0 {
				return nil, true, fmt.Errorf("pipeline %q: unsupported owner %q: no persistent instance trigger matches job.step_completed for target %q when manager_gate_ready=%t", pipeline.Name, owner.Name, owner.Name, managerGateReady)
			}
			if len(candidates) != 1 || candidates[0] != owner.Name {
				return nil, true, fmt.Errorf("pipeline %q: ambiguous completion owner for job.step_completed target %q when manager_gate_ready=%t: matched %s", pipeline.Name, owner.Name, managerGateReady, strings.Join(candidates, ", "))
			}
		}
		routes = append(routes, pipelineManagerRoute{
			owner: owner,
			duty:  "manual-gate completion",
			verbs: pipelineManualManagerRequiredVerbs(),
		})
		terminalTarget = owner.Name
	}

	if len(terminalVerbs) > 0 {
		terminalOwners := map[string]bool{}
		for _, managerGateReady := range []bool{false, true} {
			payload := ManagerCompletionTriggerPayload(pipeline.Name, terminalTarget, managerGateReady)
			if candidate, fields, ok := t.unsupportedPersistentCompletionOwner(EventJobCompleted, payload); ok {
				return nil, true, fmt.Errorf("%v (manager_gate_ready=%t)", unsupportedDynamicCompletionOwnerError(pipeline.Name, EventJobCompleted, candidate, fields), managerGateReady)
			}
			candidates := t.persistentCompletionCandidates(EventJobCompleted, payload)
			if len(candidates) == 0 {
				return nil, true, fmt.Errorf("pipeline %q: unsupported managing instance for %s: no persistent instance trigger matches job.completed with pipeline %q when manager_gate_ready=%t", pipeline.Name, pipelineCompletionManagerDuty(pipeline), pipeline.Name, managerGateReady)
			}
			if len(candidates) != 1 {
				return nil, true, fmt.Errorf("pipeline %q: ambiguous completion owner for %s when manager_gate_ready=%t: matched %s", pipeline.Name, pipelineCompletionManagerDuty(pipeline), managerGateReady, strings.Join(candidates, ", "))
			}
			terminalOwners[candidates[0]] = true
		}
		verbs := terminalVerbs
		if len(owners) == 0 {
			verbs = append(pipelineCoreManagerRequiredVerbs(), terminalVerbs...)
		}
		for _, name := range sortedStringKeys(terminalOwners) {
			routes = append(routes, pipelineManagerRoute{
				owner: t.Instances[name],
				duty:  pipelineCompletionManagerDuty(pipeline),
				verbs: verbs,
			})
		}
	}
	return routes, true, nil
}

func (t *Topology) unsupportedPersistentCompletionOwner(event string, payload map[string]any) (string, []string, bool) {
	stableKeys := managerCompletionStableMatchKeys()
	for _, candidate := range t.SortedInstances() {
		if candidate == nil || candidate.Ephemeral {
			continue
		}
		for _, trigger := range candidate.Triggers {
			if trigger == nil || trigger.Event != event || !completionTriggerMatchesStablePayload(trigger, payload, stableKeys) {
				continue
			}
			var dynamicKeys []string
			for key := range trigger.Match {
				if !stableKeys[key] {
					dynamicKeys = append(dynamicKeys, key)
				}
			}
			if len(dynamicKeys) == 0 {
				continue
			}
			sort.Strings(dynamicKeys)
			return candidate.Name, dynamicKeys, true
		}
	}
	return "", nil, false
}

func managerCompletionStableMatchKeys() map[string]bool {
	payload := ManagerCompletionTriggerPayload("pipeline", "manager", true)
	keys := make(map[string]bool, len(payload))
	for key := range payload {
		keys[key] = true
	}
	return keys
}

func completionTriggerMatchesStablePayload(trigger *Trigger, payload map[string]any, stableKeys map[string]bool) bool {
	for key, matcher := range trigger.Match {
		if !stableKeys[key] {
			continue
		}
		value, ok := payload[key]
		if !ok || !matcher.Eval(value) {
			return false
		}
	}
	return true
}

func unsupportedDynamicCompletionOwnerError(pipeline, event, owner string, fields []string) error {
	stableKeys := sortedStringKeys(managerCompletionStableMatchKeys())
	return fmt.Errorf(
		"pipeline %q: unsupported dynamic completion owner %q for %s: trigger constrains %s; runtime-enriched completion fields cannot determine manager ownership; use only stable completion fields %s",
		pipeline,
		owner,
		event,
		strings.Join(prefixedMatchKeys(fields), ", "),
		strings.Join(prefixedMatchKeys(stableKeys), ", "),
	)
}

func prefixedMatchKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, "match."+key)
	}
	return out
}

func (t *Topology) persistentCompletionCandidates(event string, payload map[string]any) []string {
	var candidates []string
	for _, candidate := range t.Resolve(event, payload) {
		if candidate != nil && !candidate.Ephemeral {
			candidates = append(candidates, candidate.Name)
		}
	}
	return candidates
}

func pipelineCompletionManagerDuty(pipeline *Pipeline) string {
	if pipeline == nil {
		return "completion duty"
	}
	if pipeline.Merge != nil {
		return "declared merge"
	}
	return fmt.Sprintf("reap_worktree %q", pipeline.ReapWorktree)
}

func (t *Topology) pipelineOwningTeam(pipeline string) (string, error) {
	var owners []string
	for _, team := range t.SortedTeams() {
		for _, ref := range team.Pipelines {
			if ref == pipeline {
				owners = append(owners, team.Name)
			}
		}
	}
	switch len(owners) {
	case 0:
		return "", fmt.Errorf("pipeline %q: unsupported authority scope: managed pipeline has no owning [teams.*].pipelines declaration", pipeline)
	case 1:
		return owners[0], nil
	default:
		return "", fmt.Errorf("pipeline %q: ambiguous authority scope: declared by teams %s", pipeline, strings.Join(owners, ", "))
	}
}

func pipelineManualManagerRequiredVerbs() []string {
	return append(pipelineCoreManagerRequiredVerbs(), "job.approve", "job.reject")
}

func pipelineCoreManagerRequiredVerbs() []string {
	return []string{"job.bounce", "job.step", "job.gate.set", "event.publish"}
}

func pipelineTerminalManagerRequiredVerbs(pipeline *Pipeline) []string {
	if pipeline == nil {
		return nil
	}
	var verbs []string
	if pipeline.Merge != nil || pipeline.ReapWorktree == worktreepolicy.OnMerge {
		verbs = append(verbs, "job.merge")
	}
	if pipeline.ReapWorktree == worktreepolicy.OnClose {
		verbs = append(verbs, "job.close")
	}
	return verbs
}

func sortedStringKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func finaliseAuthority(raw *rawAuthority) (*Authority, error) {
	if raw == nil {
		return nil, nil
	}
	enforcement, err := normalizeAuthorityMode(raw.Enforcement)
	if err != nil {
		return nil, err
	}
	instances, err := finaliseAuthorityRules("authority.instances", raw.Instances)
	if err != nil {
		return nil, err
	}
	agents, err := finaliseAuthorityRules("authority.agents", raw.Agents)
	if err != nil {
		return nil, err
	}
	teams, err := finaliseAuthorityRules("authority.teams", raw.Teams)
	if err != nil {
		return nil, err
	}
	return &Authority{Enforcement: enforcement, Instances: instances, Agents: agents, Teams: teams}, nil
}

func normalizeAuthorityMode(raw string) (string, error) {
	switch mode := strings.TrimSpace(raw); mode {
	case "", AuthorityModeAudit:
		return AuthorityModeAudit, nil
	case AuthorityModeEnforce:
		return AuthorityModeEnforce, nil
	default:
		return "", fmt.Errorf("authority.enforcement must be %q or %q", AuthorityModeAudit, AuthorityModeEnforce)
	}
}

func finaliseAuthorityRules(kind string, raw map[string]*rawAuthorityRule) (map[string]*AuthorityRule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]*AuthorityRule, len(raw))
	for name, rule := range raw {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("%s: name must be non-empty", kind)
		}
		allow, err := finaliseAuthorityAllowList(kind, name, rule)
		if err != nil {
			return nil, err
		}
		out[name] = &AuthorityRule{Allow: allow}
	}
	return out, nil
}

func finaliseAuthorityAllowList(kind, name string, rule *rawAuthorityRule) ([]string, error) {
	if rule == nil {
		return nil, fmt.Errorf("%s.%s: allow must be non-empty", kind, name)
	}
	values := append([]string(nil), rule.Allow...)
	values = append(values, rule.Verbs...)
	if len(values) == 0 {
		return nil, fmt.Errorf("%s.%s: allow must be non-empty", kind, name)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for i, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s.%s allow[%d]: must be non-empty", kind, name, i)
		}
		if err := validateAuthorityVerb(value); err != nil {
			return nil, fmt.Errorf("%s.%s allow[%d]: %w", kind, name, i, err)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func validateAuthorityVerb(value string) error {
	value = strings.TrimSpace(value)
	verb, qualifier, hasQualifier := strings.Cut(value, ":")
	if hasQualifier {
		if strings.Contains(qualifier, ":") {
			return fmt.Errorf("authority scope qualifier must be :own or :team")
		}
		switch strings.TrimSpace(qualifier) {
		case "own", "team":
		default:
			return fmt.Errorf("authority scope qualifier must be :own or :team")
		}
		value = strings.TrimSpace(verb)
	}
	if value == "*" {
		return nil
	}
	if value == "" {
		return fmt.Errorf("verb must be non-empty")
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("verb must not contain empty path segments")
	}
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		case r == '*' && i == len(value)-1 && strings.HasSuffix(value, ".*"):
		default:
			return fmt.Errorf("verb may only contain lowercase ASCII letters, digits, '.', '_' and '-' plus a trailing .* wildcard")
		}
	}
	if strings.Contains(value, "*") && !strings.HasSuffix(value, ".*") {
		return fmt.Errorf("wildcard must be '*' or a trailing .*")
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

func cleanChannelRefs(kind, name, field string, refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for i, ref := range refs {
		canonical, err := normalizeChannelName(ref)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %s[%d]: %w", kind, name, field, i, err)
		}
		if seen[canonical] {
			return nil, fmt.Errorf("%s %q: %s contains duplicate %q", kind, name, field, canonical)
		}
		seen[canonical] = true
		out = append(out, canonical)
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
		runtime, err := parseStepRuntime(body["runtime"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		runtimeBin, err := parseStepText(body["runtime_bin"], "runtime_bin")
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		model, err := parseOptionalText(body["model"], "model")
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		effort, err := parseOptionalText(body["effort"], "effort")
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
		approvalRequired, err := parseStepApprovalRequired(body["approval_required"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		if approvalRequired && gate != "manual" {
			return nil, fmt.Errorf("pipeline %q step[%d]: approval_required is only valid with gate manual", name, i)
		}
		optional, err := parseStepOptional(body["optional"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		timeout, err := parseStepTimeout(body["timeout"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		tokenBudget, err := allowance.ParseTokenValue(body["token_budget"], "token_budget")
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		timeBudget, err := parseStepTimeBudget(body["time_budget"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		hardBudget, err := parseStepHard(body["hard"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		hardMultiplier, err := allowance.ParseHardMultiplierValue(body["hard_multiplier"], "hard_multiplier")
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		reminderLevels, err := parseStepReminderLevels(body["reminder_levels"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		maxAttempts, err := parseStepMaxAttempts(body["max_attempts"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		retryOnCrash, err := parseStepRetryOnCrash(body["retry_on_crash"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		locks, err := parsePipelineStepLocks(name, id, body["locks"])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q step[%d]: %w", name, i, err)
		}
		steps = append(steps, &PipelineStep{ID: id, Label: label, Description: description, Instructions: instructions, Target: target, Locks: locks, Workspace: workspace, Runtime: runtime, RuntimeBin: runtimeBin, Model: model, Effort: effort, After: after, Gate: gate, ApprovalRequired: approvalRequired, Optional: optional, Timeout: timeout, TokenBudget: tokenBudget, TimeBudget: timeBudget, HardBudget: hardBudget, HardMultiplier: hardMultiplier, ReminderLevels: reminderLevels, MaxAttempts: maxAttempts, RetryOnCrash: retryOnCrash, runtimeDeclared: runtime != "", modelDeclared: model != "", effortDeclared: effort != ""})
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

func parsePipelineStepLocks(pipeline, step string, raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	values, err := parseStringList(raw, "locks")
	if err != nil {
		return nil, err
	}
	return parseLockRefs("pipeline "+pipeline+" step", step, "locks", values)
}

func parseStringList(raw any, field string) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s values must be strings", field)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be a list of strings", field)
	}
}

func parseLockRefs(kind, name, field string, refs []string) ([]string, error) {
	out, err := cleanTeamRefs(kind, name, field, refs)
	if err != nil {
		return nil, err
	}
	for _, ref := range out {
		if err := validateLockName(ref); err != nil {
			return nil, fmt.Errorf("%s %q: %s references invalid lock %q: %w", kind, name, field, ref, err)
		}
	}
	return out, nil
}

func normalizeResourceScope(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ScopeMachine, nil
	}
	switch value {
	case ScopeMachine, ScopeTeam, ScopeJob:
		return value, nil
	default:
		return "", fmt.Errorf("scope must be machine, team, or job")
	}
}

// ScopedResourceName returns the storage key for a declared resource under the
// requested scope. The unscoped name is returned for machine scope.
func ScopedResourceName(name, scope, team, job string) string {
	name = strings.TrimSpace(name)
	scope, err := normalizeResourceScope(scope)
	if err != nil {
		scope = ScopeMachine
	}
	switch scope {
	case ScopeTeam:
		return "team." + safeResourceSegment(team, "unknown") + "." + name
	case ScopeJob:
		return "job." + safeResourceSegment(job, "unknown") + "." + name
	default:
		return name
	}
}

// CanonicalChannelName normalizes topology channel names to the runtime form.
func CanonicalChannelName(name string) (string, error) {
	return normalizeChannelName(name)
}

// ScopedChannelName returns the storage channel name for a declared channel.
func ScopedChannelName(name, scope, team, job string) (string, error) {
	canonical, err := normalizeChannelName(name)
	if err != nil {
		return "", err
	}
	base := strings.TrimPrefix(canonical, "#")
	scope, err = normalizeResourceScope(scope)
	if err != nil {
		scope = ScopeMachine
	}
	switch scope {
	case ScopeTeam:
		return normalizeChannelName("team-" + safeResourceSegment(team, "unknown") + "-" + base)
	case ScopeJob:
		return normalizeChannelName("job-" + safeResourceSegment(job, "unknown") + "-" + base)
	default:
		return canonical, nil
	}
}

func safeResourceSegment(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	var b strings.Builder
	lastSep := false
	for _, r := range strings.ToLower(value) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastSep = false
			continue
		}
		if !lastSep {
			b.WriteByte('-')
			lastSep = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return fallback
	}
	return out
}

func validateLockName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name must be non-empty")
	}
	if name == "." || name == ".." || strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("name must not contain path segments")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("name may only contain ASCII letters, digits, '.', '_' and '-'")
	}
	return nil
}

var channelNameRE = regexp.MustCompile(`^#[a-z0-9][a-z0-9-]{0,63}$`)

func normalizeChannelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name must be non-empty")
	}
	if !strings.HasPrefix(name, "#") {
		name = "#" + name
	}
	if !channelNameRE.MatchString(name) {
		return "", fmt.Errorf("name must match %s", channelNameRE)
	}
	return name, nil
}

func validateLockReferences(t *Topology) error {
	if t == nil {
		return nil
	}
	for _, inst := range t.SortedInstances() {
		for _, lock := range inst.Locks {
			if t.Locks[lock] == nil {
				return fmt.Errorf("instance %q: locks references unknown lock %q", inst.Name, lock)
			}
		}
	}
	for _, pipeline := range t.SortedPipelines() {
		for _, step := range pipeline.Steps {
			for _, lock := range step.Locks {
				if t.Locks[lock] == nil {
					return fmt.Errorf("pipeline %q step %q: locks references unknown lock %q", pipeline.Name, step.ID, lock)
				}
			}
		}
	}
	return nil
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

func parseOptionalText(raw any, field string) (string, error) {
	if raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return strings.TrimSpace(value), nil
}

func parseStepRuntime(raw any) (string, error) {
	if raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	value = strings.ToLower(strings.TrimSpace(value))
	if !ok || value == "" {
		return "", fmt.Errorf("runtime must be a non-empty string")
	}
	kind, err := runtimebin.ParseKind(value)
	if err != nil {
		return "", fmt.Errorf("runtime must be claude, codex, or docker")
	}
	return string(kind), nil
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

func parseStepHard(raw any) (bool, error) {
	if raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("hard must be a boolean")
	}
	return value, nil
}

func parseStepRetryOnCrash(raw any) (bool, error) {
	if raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("retry_on_crash must be a boolean")
	}
	return value, nil
}

func parseStepApprovalRequired(raw any) (bool, error) {
	if raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("approval_required must be a boolean")
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

func parseStepTimeBudget(raw any) (time.Duration, error) {
	if raw == nil {
		return 0, nil
	}
	value, ok := raw.(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return 0, fmt.Errorf("time_budget must be a non-empty duration string")
	}
	return parseOptionalDurationString(value, "time_budget")
}

func parseOptionalDurationString(raw, field string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", field, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", field)
	}
	return duration, nil
}

func parseStepReminderLevels(raw any) ([]int, error) {
	if raw == nil {
		return nil, nil
	}
	var levels []int
	switch values := raw.(type) {
	case []any:
		levels = make([]int, 0, len(values))
		for _, rawLevel := range values {
			switch v := rawLevel.(type) {
			case int:
				levels = append(levels, v)
			case int64:
				if int64(int(v)) != v {
					return nil, fmt.Errorf("reminder_levels contains an integer that is too large")
				}
				levels = append(levels, int(v))
			default:
				return nil, fmt.Errorf("reminder_levels must be an array of integers")
			}
		}
	case []int64:
		levels = make([]int, 0, len(values))
		for _, v := range values {
			if int64(int(v)) != v {
				return nil, fmt.Errorf("reminder_levels contains an integer that is too large")
			}
			levels = append(levels, int(v))
		}
	case []int:
		levels = append([]int(nil), values...)
	default:
		return nil, fmt.Errorf("reminder_levels must be an array of integers")
	}
	return allowance.NormalizeReminderLevels(levels)
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
