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
	"sync"
	"time"

	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/loader"
	teamtemplate "github.com/jamesaud/agent-team/internal/template"
	"github.com/jamesaud/agent-team/internal/topology"
)

// DefaultQueueCap is the maximum number of events queued per declared
// ephemeral instance once its replica capacity is exhausted. Excess events
// are rejected with HTTP 429 by the resolver — the spec calls this out as a
// trade-off (see documentation/topology.md § Open question on replicas).
const DefaultQueueCap = 10

// MaxQueueAttempts is the number of failed spawn attempts before a queued
// event is moved to dead-letter. Initial capacity queueing does not count as
// an attempt; only failed dispatch attempts do.
const MaxQueueAttempts = 3

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
	id         string
	eventType  string
	payload    map[string]any
	queuedAt   time.Time
	uniqueName string
	attempts   int
	lastError  string
	nextRetry  time.Time
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
	_ = r.loadPersistedQueue()
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
	childName, requested, err := childNameForEvent(inst.Name, payload)
	if err != nil {
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	if requested && r.mgr.isRunning(childName) {
		return EventOutcome{
			Instance:   inst.Name,
			Action:     "rejected",
			InstanceID: childName,
			Reason:     fmt.Sprintf("instance %q already running", childName),
		}
	}

	r.mu.Lock()
	tr, ok := r.tracking[inst.Name]
	if !ok {
		tr = &ephTracker{}
		r.tracking[inst.Name] = tr
	}
	if requested && queuedChildName(tr.queue, childName) {
		r.mu.Unlock()
		return EventOutcome{
			Instance:   inst.Name,
			Action:     "rejected",
			InstanceID: childName,
			Reason:     fmt.Sprintf("instance %q already queued", childName),
		}
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
		ev := &queuedEvent{
			id:         newSessionID(),
			eventType:  eventType,
			payload:    payload,
			queuedAt:   time.Now().UTC(),
			uniqueName: childName,
		}
		if err := WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(inst.Name, ev, QueueStatePending)); err != nil {
			r.mu.Unlock()
			return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
		}
		tr.queue = append(tr.queue, ev)
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: childName}
	}
	tr.running++
	r.mu.Unlock()

	meta, err := r.spawn(inst, childName, eventType, payload)
	if err != nil {
		// Spawn failed; release capacity and don't drain queue (no work freed).
		r.mu.Lock()
		tr.running--
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "rejected", Reason: err.Error()}
	}
	return EventOutcome{Instance: inst.Name, Action: "dispatched", InstanceID: meta.Instance}
}

func childNameForEvent(declared string, payload map[string]any) (string, bool, error) {
	requested := payloadString(payload, "name")
	if requested == "" {
		requested = payloadString(payload, "instance")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return uniqueChildName(declared), false, nil
	}
	if err := validateRequestedChildName(declared, requested); err != nil {
		return "", false, err
	}
	return requested, true, nil
}

func queuedChildName(queue []*queuedEvent, name string) bool {
	for _, ev := range queue {
		if ev != nil && ev.uniqueName == name {
			return true
		}
	}
	return false
}

func queuedEventID(queue []*queuedEvent, id string) bool {
	for _, ev := range queue {
		if ev != nil && ev.id == id {
			return true
		}
	}
	return false
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

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
	runtime, err := r.prepareEphemeralRuntime(inst, name)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"event": eventType, "payload": payload})
	prompt := fmt.Sprintf("Topology event for declared instance %q (agent=%s):\n%s",
		inst.Name, inst.Agent, string(body))
	args, err := r.prepareEphemeralAgentArgs(inst.Agent, name, runtime.stateDir, prompt)
	if err != nil {
		return nil, err
	}
	workspace := r.teamDirParent()
	worktreePath := ""
	branch := ""
	cleanupWorkspace := func() {}
	if payloadString(payload, "workspace") == "worktree" || payloadString(payload, "isolation") == "worktree" {
		workspace, branch, cleanupWorkspace, err = r.prepareEphemeralWorktree(name)
		if err != nil {
			return nil, err
		}
		worktreePath = workspace
	}
	meta, err := r.mgr.Dispatch(DispatchInput{
		Agent:     inst.Agent,
		Name:      name,
		Workspace: workspace,
		Args:      args,
		Env:       runtime.env,
	})
	if err != nil {
		cleanupWorkspace()
		return nil, err
	}
	r.attachSpawnOwnership(meta, payload, branch, worktreePath)
	return meta, nil
}

func (r *EventResolver) attachSpawnOwnership(meta *Metadata, payload map[string]any, branch, worktreePath string) {
	if meta == nil {
		return
	}
	meta.Job = eventJobID(payload)
	meta.Ticket = payloadString(payload, "ticket")
	meta.Branch = branch
	meta.PR = payloadString(payload, "pr")
	if err := WriteMetadata(r.mgr.daemonRoot, meta); err != nil {
		return
	}
	if meta.Job == "" {
		return
	}
	j, err := jobstore.Read(r.teamDir, meta.Job)
	if err != nil {
		return
	}
	j.Instance = meta.Instance
	if meta.Ticket != "" {
		j.Ticket = meta.Ticket
	}
	if branch != "" {
		j.Branch = branch
	}
	if worktreePath != "" {
		j.Worktree = worktreePath
	}
	if meta.PR != "" {
		j.PR = meta.PR
	}
	j.Status = jobstore.StatusRunning
	j.LastEvent = "dispatched"
	j.LastStatus = "running"
	j.UpdatedAt = time.Now().UTC()
	_ = jobstore.Write(r.teamDir, j)
}

func eventJobID(payload map[string]any) string {
	id := payloadString(payload, "job_id")
	if id == "" {
		id = payloadString(payload, "job")
	}
	return jobstore.NormalizeID(id)
}

type ephemeralRuntime struct {
	stateDir string
	env      []string
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
	return &ephemeralRuntime{
		stateDir: stateDir,
		env: []string{
			"AGENT_TEAM_ROOT=" + r.teamDir,
			"AGENT_TEAM_INSTANCE=" + name,
			"AGENT_TEAM_STATE_DIR=" + stateDir,
		},
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

func (r *EventResolver) prepareEphemeralAgentArgs(agentName, instance, stateDir, prompt string) ([]string, error) {
	agents, err := loader.LoadAllAgents(r.teamDir)
	if err != nil {
		return nil, fmt.Errorf("event runtime: load agents: %w", err)
	}
	var chosen *loader.Agent
	for _, agent := range agents {
		if agent.Name == agentName {
			chosen = agent
			break
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("event runtime: agent %q not found", agentName)
	}
	skillPaths, err := loader.UnionSkills(agents)
	if err != nil {
		return nil, fmt.Errorf("event runtime: resolve skills: %w", err)
	}

	runtimeDir := filepath.Join(stateDir, "runtime")
	if err := os.RemoveAll(runtimeDir); err != nil {
		return nil, fmt.Errorf("event runtime: reset runtime dir: %w", err)
	}
	skillsRoot := filepath.Join(runtimeDir, ".claude", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("event runtime: create skills root: %w", err)
	}
	for name, path := range skillPaths {
		if err := os.Symlink(path, filepath.Join(skillsRoot, name)); err != nil {
			return nil, fmt.Errorf("event runtime: symlink skill %s: %w", name, err)
		}
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
	promptFile := filepath.Join(runtimeDir, "system_prompt.md")
	if err := os.WriteFile(promptFile, []byte(kickoff), 0o644); err != nil {
		return nil, fmt.Errorf("event runtime: write prompt file: %w", err)
	}
	agentsJSON, err := buildAgentsJSON(agents)
	if err != nil {
		return nil, err
	}
	return []string{
		"--agents", agentsJSON,
		"--add-dir", runtimeDir,
		"--append-system-prompt-file", promptFile,
		"-p", prompt,
	}, nil
}

func (r *EventResolver) prepareEphemeralWorktree(instance string) (string, string, func(), error) {
	repoRoot := r.teamDirParent()
	if repoRoot == "" {
		return "", "", nil, errors.New("event worktree: repo root is required")
	}
	tag := newSessionID()[0:8]
	branch := "worktree-" + instance + "-" + tag
	worktreePath := filepath.Join(repoRoot, ".claude", "worktrees", instance+"-"+tag)
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", "", nil, fmt.Errorf("event worktree: create parent: %w", err)
	}
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "-b", branch, worktreePath, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", nil, fmt.Errorf("event worktree: git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	cleanup := func() {
		_ = exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", worktreePath).Run()
		_ = exec.Command("git", "-C", repoRoot, "branch", "-D", branch).Run()
	}
	return worktreePath, branch, cleanup, nil
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
		next, tr.queue = popReadyQueuedEvent(tr.queue, time.Now().UTC())
		if next != nil {
			tr.running++
		}
	}
	r.mu.Unlock()
	r.cleanupEphemeralSpawn(spawned)
	if next == nil {
		return
	}
	// Re-spawn from the queue. Failures are dropped to the daemon log; no
	// retry. (A full retry-and-dead-letter design is out of scope for v1.2.)
	if _, err := r.spawn(declared, next.uniqueName, next.eventType, next.payload); err != nil {
		r.recordQueueFailure(declared.Name, next, err)
		r.mu.Lock()
		tr.running--
		r.mu.Unlock()
		return
	}
	_ = RemoveQueueItem(r.mgr.daemonRoot, next.id)
}

func popReadyQueuedEvent(queue []*queuedEvent, now time.Time) (*queuedEvent, []*queuedEvent) {
	for i, ev := range queue {
		if ev == nil {
			continue
		}
		if !ev.nextRetry.IsZero() && ev.nextRetry.After(now) {
			continue
		}
		out := append(queue[:i:i], queue[i+1:]...)
		return ev, out
	}
	return nil, queue
}

func (r *EventResolver) recordQueueFailure(declared string, ev *queuedEvent, spawnErr error) {
	if ev == nil {
		return
	}
	ev.attempts++
	ev.lastError = spawnErr.Error()
	if ev.attempts >= MaxQueueAttempts {
		_ = MoveQueueItemToDead(r.mgr.daemonRoot, queueItemFromEvent(declared, ev, QueueStateDead))
		return
	}
	ev.nextRetry = time.Now().UTC().Add(time.Duration(ev.attempts) * time.Second)
	_ = WriteQueueItem(r.mgr.daemonRoot, queueItemFromEvent(declared, ev, QueueStatePending))
	r.mu.Lock()
	tr, ok := r.tracking[declared]
	if ok && !queuedEventID(tr.queue, ev.id) {
		tr.queue = append(tr.queue, ev)
	}
	r.mu.Unlock()
}

func (r *EventResolver) loadPersistedQueue() error {
	items, err := ListQueueItems(r.mgr.daemonRoot)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range items {
		if item.State != QueueStatePending {
			continue
		}
		if r.topo == nil || r.topo.Find(item.Instance) == nil {
			continue
		}
		tr, ok := r.tracking[item.Instance]
		if !ok {
			tr = &ephTracker{}
			r.tracking[item.Instance] = tr
		}
		if queuedEventID(tr.queue, item.ID) {
			continue
		}
		tr.queue = append(tr.queue, queuedEventFromItem(item))
	}
	return nil
}

// RecoverQueueState rebuilds ephemeral running counters from current daemon
// metadata and reloads persisted pending queue files. Daemon.Run calls this
// after process reconciliation so queued dispatches survive daemon restart.
func (r *EventResolver) RecoverQueueState() {
	r.mu.Lock()
	r.tracking = map[string]*ephTracker{}
	if r.topo != nil {
		for _, meta := range r.mgr.List() {
			inst := r.declaredOwnerOfLocked(meta.Instance)
			if inst == nil || !inst.Ephemeral || meta.Status != StatusRunning {
				continue
			}
			tr, ok := r.tracking[inst.Name]
			if !ok {
				tr = &ephTracker{}
				r.tracking[inst.Name] = tr
			}
			tr.running++
		}
	}
	r.mu.Unlock()
	_ = r.loadPersistedQueue()
	r.DrainQueues()
}

// DrainQueues attempts ready queued items while replica capacity is available.
func (r *EventResolver) DrainQueues() {
	for {
		declared, ev := r.nextDrainableQueuedEvent()
		if declared == nil || ev == nil {
			return
		}
		if _, err := r.spawn(declared, ev.uniqueName, ev.eventType, ev.payload); err != nil {
			r.recordQueueFailure(declared.Name, ev, err)
			r.mu.Lock()
			if tr := r.tracking[declared.Name]; tr != nil && tr.running > 0 {
				tr.running--
			}
			r.mu.Unlock()
			continue
		}
		_ = RemoveQueueItem(r.mgr.daemonRoot, ev.id)
	}
}

func (r *EventResolver) nextDrainableQueuedEvent() (*topology.Instance, *queuedEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.topo == nil {
		return nil, nil
	}
	now := time.Now().UTC()
	for _, inst := range r.topo.SortedInstances() {
		if !inst.Ephemeral {
			continue
		}
		tr, ok := r.tracking[inst.Name]
		if !ok || tr.running >= inst.Replicas {
			continue
		}
		ev, rest := popReadyQueuedEvent(tr.queue, now)
		if ev == nil {
			continue
		}
		tr.queue = rest
		tr.running++
		return inst, ev
	}
	return nil, nil
}

// DropQueueItem removes a queued item from memory and disk.
func (r *EventResolver) DropQueueItem(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("queue: id is required")
	}
	r.mu.Lock()
	for _, tr := range r.tracking {
		filtered := tr.queue[:0]
		for _, ev := range tr.queue {
			if ev == nil || ev.id == id {
				continue
			}
			filtered = append(filtered, ev)
		}
		tr.queue = filtered
	}
	r.mu.Unlock()
	return RemoveQueueItem(r.mgr.daemonRoot, id)
}

// RetryQueueItem retries a pending or dead-letter queue item immediately.
func (r *EventResolver) RetryQueueItem(id string) (EventOutcome, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return EventOutcome{}, errors.New("queue: id is required")
	}
	item, err := ReadQueueItem(r.mgr.daemonRoot, id)
	if err != nil {
		return EventOutcome{}, err
	}
	if err := ResetQueueItemForRetry(r.mgr.daemonRoot, item); err != nil {
		return EventOutcome{}, err
	}
	ev := queuedEventFromItem(item)
	r.mu.Lock()
	inst := (*topology.Instance)(nil)
	if r.topo != nil {
		inst = r.topo.Find(item.Instance)
	}
	if inst == nil || !inst.Ephemeral {
		r.mu.Unlock()
		return EventOutcome{}, fmt.Errorf("queue: instance %q is not a declared ephemeral instance", item.Instance)
	}
	tr, ok := r.tracking[inst.Name]
	if !ok {
		tr = &ephTracker{}
		r.tracking[inst.Name] = tr
	}
	tr.queue = removeQueuedEventByID(tr.queue, id)
	if tr.running >= inst.Replicas {
		tr.queue = append(tr.queue, ev)
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "queued", InstanceID: ev.uniqueName}, nil
	}
	tr.running++
	r.mu.Unlock()

	if _, err := r.spawn(inst, ev.uniqueName, ev.eventType, ev.payload); err != nil {
		r.recordQueueFailure(inst.Name, ev, err)
		r.mu.Lock()
		if tr.running > 0 {
			tr.running--
		}
		r.mu.Unlock()
		return EventOutcome{Instance: inst.Name, Action: "rejected", InstanceID: ev.uniqueName, Reason: err.Error()}, nil
	}
	_ = RemoveQueueItem(r.mgr.daemonRoot, ev.id)
	return EventOutcome{Instance: inst.Name, Action: "dispatched", InstanceID: ev.uniqueName}, nil
}

func removeQueuedEventByID(queue []*queuedEvent, id string) []*queuedEvent {
	filtered := queue[:0]
	for _, ev := range queue {
		if ev == nil || ev.id == id {
			continue
		}
		filtered = append(filtered, ev)
	}
	return filtered
}

func queueItemFromEvent(declared string, ev *queuedEvent, state string) *QueueItem {
	now := time.Now().UTC()
	updated := now
	if ev.queuedAt.IsZero() {
		ev.queuedAt = now
	}
	return &QueueItem{
		ID:         ev.id,
		State:      state,
		EventType:  ev.eventType,
		Instance:   declared,
		InstanceID: ev.uniqueName,
		Payload:    ev.payload,
		Attempts:   ev.attempts,
		LastError:  ev.lastError,
		NextRetry:  ev.nextRetry,
		QueuedAt:   ev.queuedAt,
		UpdatedAt:  updated,
	}
}

func queuedEventFromItem(item *QueueItem) *queuedEvent {
	return &queuedEvent{
		id:         item.ID,
		eventType:  item.EventType,
		payload:    item.Payload,
		queuedAt:   item.QueuedAt,
		uniqueName: item.InstanceID,
		attempts:   item.Attempts,
		lastError:  item.LastError,
		nextRetry:  item.NextRetry,
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
	inst := r.declaredOwnerOfLocked(spawned)
	return inst, inst != nil
}

func (r *EventResolver) declaredOwnerOfLocked(spawned string) *topology.Instance {
	if r.topo == nil {
		return nil
	}
	for _, inst := range r.topo.Instances {
		if !inst.Ephemeral {
			continue
		}
		if spawned == inst.Name || strings.HasPrefix(spawned, inst.Name+"-") {
			return inst
		}
	}
	return nil
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
