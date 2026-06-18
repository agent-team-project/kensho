package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	teamtemplate "github.com/jamesaud/agent-team/internal/template"
	"github.com/jamesaud/agent-team/internal/topology"
)

// DefaultQueueCap is the maximum number of events queued per declared
// ephemeral instance once its replica capacity is exhausted. Excess events
// are rejected with HTTP 429 by the resolver — the spec calls this out as a
// trade-off (see documentation/topology.md § Open question on replicas).
const DefaultQueueCap = 10

// EventResolver routes inbound events to declared instances per the topology.
// Persistent instances receive a JSON-encoded event payload via mailbox
// (the inbox skill drains it on the agent side). Ephemeral instances spawn
// a fresh claude child via the InstanceManager — capacity-limited per
// declared name with a small in-memory queue.
//
// Concurrency: a single mutex protects the per-instance counters and queues.
// The Manager's reap hook decrements the running counter and drains queued
// events; the hook is set on installation.
type EventResolver struct {
	mgr      *InstanceManager
	teamDir  string
	queueCap int

	mu       sync.Mutex
	topo     *topology.Topology
	tracking map[string]*ephTracker // declared-name → tracker
}

type ephTracker struct {
	running int
	queue   []*queuedEvent
}

type queuedEvent struct {
	eventType  string
	payload    map[string]any
	queuedAt   time.Time
	uniqueName string
}

// NewEventResolver installs a reap hook on mgr and returns a resolver bound
// to it. teamDir is the consumer's `.agent_team/` (used to resolve the
// workspace for spawned instances).
func NewEventResolver(mgr *InstanceManager, teamDir string, topo *topology.Topology) *EventResolver {
	r := &EventResolver{
		mgr:      mgr,
		teamDir:  teamDir,
		queueCap: DefaultQueueCap,
		topo:     topo,
		tracking: map[string]*ephTracker{},
	}
	mgr.SetReapHook(r.onReap)
	return r
}

// SetTopology swaps the live topology pointer (used by /v1/topology/reload).
// In-flight ephemeral spawns and their queue depth are preserved across
// reloads — the running children outlive the topology that spawned them.
func (r *EventResolver) SetTopology(t *topology.Topology) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.topo = t
}

// Topology returns the current topology pointer (for /v1/topology).
func (r *EventResolver) Topology() *topology.Topology {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.topo
}

// EventOutcome is the per-instance result of an Event call, returned in the
// HTTP response so callers know what was actuated.
type EventOutcome struct {
	Instance   string `json:"instance"`
	Action     string `json:"action"` // "dispatched" | "queued" | "messaged" | "rejected"
	InstanceID string `json:"instance_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Event resolves an inbound event against the topology and actuates each
// matched instance. Returns one outcome per matched instance; an empty slice
// means no triggers matched.
//
// payload is the inbound event payload; eventType is one of the known event
// types. Callers should pass the eventType through unchanged — webhook event
// types are passed through to triggers as-is.
func (r *EventResolver) Event(eventType string, payload map[string]any) ([]EventOutcome, error) {
	if strings.TrimSpace(eventType) == "" {
		return nil, errors.New("event: type is required")
	}
	r.mu.Lock()
	t := r.topo
	r.mu.Unlock()
	if t == nil {
		return nil, nil
	}
	matched := t.Resolve(eventType, payload)
	out := make([]EventOutcome, 0, len(matched))
	for _, inst := range matched {
		out = append(out, r.actuate(inst, eventType, payload))
	}
	return out, nil
}

func (r *EventResolver) actuate(inst *topology.Instance, eventType string, payload map[string]any) EventOutcome {
	if inst.Ephemeral {
		return r.actuateEphemeral(inst, eventType, payload)
	}
	return r.actuatePersistent(inst, eventType, payload)
}

func (r *EventResolver) actuatePersistent(inst *topology.Instance, eventType string, payload map[string]any) EventOutcome {
	body := map[string]any{"event": eventType, "payload": payload}
	encoded, err := json.Marshal(body)
	if err != nil {
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	msg := &Message{From: "topology", To: inst.Name, Body: string(encoded), TS: time.Now().UTC()}
	if err := AppendMessage(r.mgr.daemonRoot, inst.Name, msg); err != nil {
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	return EventOutcome{Instance: inst.Name, Action: "messaged", InstanceID: inst.Name}
}

func (r *EventResolver) actuateEphemeral(inst *topology.Instance, eventType string, payload map[string]any) EventOutcome {
	r.mu.Lock()
	tr, ok := r.tracking[inst.Name]
	if !ok {
		tr = &ephTracker{}
		r.tracking[inst.Name] = tr
	}
	if tr.running >= inst.Replicas {
		if len(tr.queue) >= r.queueCap {
			r.mu.Unlock()
			return EventOutcome{
				Instance: inst.Name,
				Action:   "rejected",
				Reason:   fmt.Sprintf("at replica capacity (%d) and queue is full (%d)", inst.Replicas, r.queueCap),
			}
		}
		uniq := uniqueChildName(inst.Name)
		tr.queue = append(tr.queue, &queuedEvent{
			eventType:  eventType,
			payload:    payload,
			queuedAt:   time.Now().UTC(),
			uniqueName: uniq,
		})
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: uniq}
	}
	tr.running++
	uniq := uniqueChildName(inst.Name)
	r.mu.Unlock()

	meta, err := r.spawn(inst, uniq, eventType, payload)
	if err != nil {
		// Spawn failed; release capacity and don't drain queue (no work freed).
		r.mu.Lock()
		tr.running--
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	return EventOutcome{Instance: inst.Name, Action: "dispatched", InstanceID: meta.Instance}
}

// spawn issues a Dispatch for an ephemeral declared instance. The daemon still
// uses the minimal `claude -p <prompt>` argv shape rather than the CLI's full
// `--agents` / `--add-dir` launcher, but it mirrors the run path's per-instance
// runtime contract: state dir, resolved config, and AGENT_TEAM_* env vars.
// The caller's payload is JSON-encoded into the prompt so the spawned child has
// full event context to work from.
func (r *EventResolver) spawn(inst *topology.Instance, name, eventType string, payload map[string]any) (*Metadata, error) {
	env, err := r.prepareEphemeralRuntime(inst, name)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"event": eventType, "payload": payload})
	prompt := fmt.Sprintf("Topology event for declared instance %q (agent=%s):\n%s",
		inst.Name, inst.Agent, string(body))
	return r.mgr.Dispatch(DispatchInput{
		Agent:     inst.Agent,
		Name:      name,
		Prompt:    prompt,
		Workspace: r.teamDirParent(),
		Env:       env,
	})
}

func (r *EventResolver) prepareEphemeralRuntime(inst *topology.Instance, name string) ([]string, error) {
	if strings.TrimSpace(r.teamDir) == "" {
		return nil, nil
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
	return []string{
		"AGENT_TEAM_ROOT=" + r.teamDir,
		"AGENT_TEAM_INSTANCE=" + name,
		"AGENT_TEAM_STATE_DIR=" + stateDir,
	}, nil
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
func (r *EventResolver) onReap(spawned string) {
	declared, ok := r.declaredOwnerOf(spawned)
	if !ok {
		return
	}
	r.mu.Lock()
	tr, ok := r.tracking[declared.Name]
	if !ok {
		r.mu.Unlock()
		return
	}
	if tr.running > 0 {
		tr.running--
	}
	var next *queuedEvent
	if len(tr.queue) > 0 {
		next = tr.queue[0]
		tr.queue = tr.queue[1:]
		tr.running++
	}
	r.mu.Unlock()
	r.cleanupEphemeralSpawn(spawned)
	if next == nil {
		return
	}
	// Re-spawn from the queue. Failures are dropped to the daemon log; no
	// retry. (A full retry-and-dead-letter design is out of scope for v1.2.)
	if _, err := r.spawn(declared, next.uniqueName, next.eventType, next.payload); err != nil {
		r.mu.Lock()
		tr.running--
		r.mu.Unlock()
	}
}

func (r *EventResolver) cleanupEphemeralSpawn(spawned string) {
	if r.teamDir != "" {
		_ = os.RemoveAll(filepath.Join(r.teamDir, "state", spawned))
	}
	_ = r.mgr.Remove(spawned, true, 0)
}

// declaredOwnerOf identifies which declared ephemeral instance a unique-named
// spawn belongs to. Names produced by uniqueChildName have the shape
// `<declared>-<short-hex>`; we reverse the prefix lookup against the current
// topology.
func (r *EventResolver) declaredOwnerOf(spawned string) (*topology.Instance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.topo == nil {
		return nil, false
	}
	for _, inst := range r.topo.Instances {
		if !inst.Ephemeral {
			continue
		}
		if spawned == inst.Name || strings.HasPrefix(spawned, inst.Name+"-") {
			return inst, true
		}
	}
	return nil, false
}

// uniqueChildName builds a per-spawn name from the declared name plus a short
// random hex tag. We avoid name collisions across concurrent spawns of the
// same declared ephemeral instance.
func uniqueChildName(declared string) string {
	tag := newSessionID()
	// First 8 hex chars are sufficient — collision on a per-repo daemon's
	// running set is vanishingly unlikely.
	return declared + "-" + tag[0:8]
}

// SetQueueCap overrides the per-declared-instance queue capacity. Useful for
// tests; production uses DefaultQueueCap.
func (r *EventResolver) SetQueueCap(cap int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cap < 0 {
		cap = 0
	}
	r.queueCap = cap
}

// QueueDepth reports the current queued+running counts for a declared
// instance. Exposed for tests and `/v1/topology` enrichment.
func (r *EventResolver) QueueDepth(name string) (running, queued int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tr, ok := r.tracking[name]
	if !ok {
		return 0, 0
	}
	return tr.running, len(tr.queue)
}
