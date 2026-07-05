package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/topology"
)

const (
	supervisorChannelName       = "#supervisor"
	phaseTransitionSender       = "agent-teamd"
	defaultPhaseWatcherPoll     = 2 * time.Second
	phaseTransitionEventName    = "instance.phase_transition"
	phaseTransitionBusyToIdle   = "busy_to_idle"
	phaseTransitionBlocked      = "blocked"
	phaseTransitionIdleRenotify = "idle_renotify"
	phaseTransitionUnknown      = "unknown"
	defaultIdleRenotify         = 0
)

type notificationConfig struct {
	PhaseTransitions map[string]bool
	IdleRenotify     time.Duration
}

type notificationsConfigFile struct {
	Notifications notificationsSection `toml:"notifications"`
}

type notificationsSection struct {
	PhaseTransitions []string `toml:"phase_transitions"`
	IdleRenotify     string   `toml:"idle_renotify"`
}

func defaultNotificationConfig() notificationConfig {
	return notificationConfig{
		PhaseTransitions: map[string]bool{
			phaseTransitionBlocked: true,
		},
		IdleRenotify: defaultIdleRenotify,
	}
}

func loadNotificationConfig(teamDir string) (notificationConfig, error) {
	cfg := defaultNotificationConfig()
	path := filepath.Join(teamDir, "config.toml")

	var raw notificationsConfigFile
	md, err := toml.DecodeFile(path, &raw)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	if md.IsDefined("notifications", "phase_transitions") {
		transitions := map[string]bool{}
		for _, value := range raw.Notifications.PhaseTransitions {
			phase := normalizeStatusPhase(value)
			switch phase {
			case phaseTransitionIdle, phaseTransitionBlocked:
				transitions[phase] = true
			case "":
				return cfg, errors.New("notifications.phase_transitions must not contain empty values")
			default:
				return cfg, fmt.Errorf("notifications.phase_transitions contains unsupported phase %q (want idle or blocked)", value)
			}
		}
		cfg.PhaseTransitions = transitions
	}
	if md.IsDefined("notifications", "idle_renotify") {
		rawDuration := strings.TrimSpace(raw.Notifications.IdleRenotify)
		if rawDuration == "" {
			return cfg, errors.New("notifications.idle_renotify must not be empty")
		}
		d, err := time.ParseDuration(rawDuration)
		if err != nil {
			return cfg, fmt.Errorf("notifications.idle_renotify: invalid duration %q: %w", rawDuration, err)
		}
		if d < 0 {
			return cfg, errors.New("notifications.idle_renotify must be >= 0")
		}
		cfg.IdleRenotify = d
	}
	return cfg, nil
}

func (c notificationConfig) enabled(phase string) bool {
	return c.PhaseTransitions[normalizeStatusPhase(phase)]
}

func (c notificationConfig) anyEnabled() bool {
	return len(c.PhaseTransitions) > 0
}

const (
	phaseTransitionPlanning       = "planning"
	phaseTransitionImplementing   = "implementing"
	phaseTransitionAwaitingReview = "awaiting_review"
	phaseTransitionIdle           = "idle"
	phaseTransitionDone           = "done"
)

type phaseTransitionWatcher struct {
	teamDir  string
	topo     *topology.Topology
	channels *ChannelStore
	cfg      notificationConfig
	interval time.Duration
	now      func() time.Time
	logf     func(string, ...any)
	states   map[string]phaseWatchState
}

type phaseWatchState struct {
	Phase              string
	LastIdleNotifiedAt time.Time
}

type phaseStatusFile struct {
	Status   phaseStatusSection    `toml:"status"`
	Work     *phaseWorkSection     `toml:"work,omitempty"`
	Blocking *phaseBlockingSection `toml:"blocking,omitempty"`
}

type phaseStatusSection struct {
	Phase       string `toml:"phase"`
	Description string `toml:"description"`
	LastAction  string `toml:"last_action"`
}

type phaseWorkSection struct {
	Job    string `toml:"job"`
	Ticket string `toml:"ticket"`
	PR     string `toml:"pr"`
	Branch string `toml:"branch"`
}

type phaseBlockingSection struct {
	Reason string `toml:"reason"`
	AskTo  string `toml:"ask_to"`
}

type phaseSnapshot struct {
	Phase       string
	Description string
	LastAction  string
	Job         string
	Ticket      string
	Branch      string
	PR          string
	Reason      string
	AskTo       string
}

type phaseTransitionMessage struct {
	Event         string `json:"event"`
	Instance      string `json:"instance"`
	Agent         string `json:"agent,omitempty"`
	Transition    string `json:"transition"`
	PreviousPhase string `json:"previous_phase"`
	Phase         string `json:"phase"`
	Description   string `json:"description,omitempty"`
	LastAction    string `json:"last_action,omitempty"`
	Job           string `json:"job,omitempty"`
	Ticket        string `json:"ticket,omitempty"`
	Branch        string `json:"branch,omitempty"`
	PR            string `json:"pr,omitempty"`
	Reason        string `json:"reason,omitempty"`
	AskTo         string `json:"ask_to,omitempty"`
	At            string `json:"at"`
}

func newPhaseTransitionWatcher(teamDir string, topo *topology.Topology, channels *ChannelStore, cfg notificationConfig) *phaseTransitionWatcher {
	return &phaseTransitionWatcher{
		teamDir:  teamDir,
		topo:     topo,
		channels: channels,
		cfg:      cfg,
		interval: defaultPhaseWatcherPoll,
		now:      func() time.Time { return time.Now().UTC() },
		states:   map[string]phaseWatchState{},
	}
}

func runPhaseTransitionWatcher(ctx context.Context, teamDir string, topo *topology.Topology, channels *ChannelStore, cfg notificationConfig, logf func(string, ...any)) {
	w := newPhaseTransitionWatcher(teamDir, topo, channels, cfg)
	w.logf = logf
	if !w.active() {
		return
	}
	if err := w.baseline(); err != nil {
		w.log("phase watcher: baseline failed: %v", err)
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.tick(); err != nil {
				w.log("phase watcher: tick failed: %v", err)
			}
		}
	}
}

func (w *phaseTransitionWatcher) active() bool {
	return w != nil && w.topo != nil && w.channels != nil && w.cfg.anyEnabled() && len(w.persistentInstances()) > 0
}

func (w *phaseTransitionWatcher) baseline() error {
	if w.states == nil {
		w.states = map[string]phaseWatchState{}
	}
	for _, inst := range w.persistentInstances() {
		snap, ok := readPhaseStatus(w.teamDir, inst.Name)
		if !ok {
			continue
		}
		w.states[inst.Name] = phaseWatchState{Phase: snap.Phase}
	}
	return nil
}

func (w *phaseTransitionWatcher) tick() error {
	if !w.active() {
		return nil
	}
	if w.states == nil {
		w.states = map[string]phaseWatchState{}
	}
	for _, inst := range w.persistentInstances() {
		snap, ok := readPhaseStatus(w.teamDir, inst.Name)
		if !ok {
			continue
		}
		state, known := w.states[inst.Name]
		if !known {
			if snap.Phase == phaseTransitionBlocked && w.cfg.enabled(phaseTransitionBlocked) {
				if err := w.publish(inst, phaseTransitionUnknown, snap, phaseTransitionBlocked); err != nil {
					return err
				}
			}
			w.states[inst.Name] = phaseWatchState{Phase: snap.Phase}
			continue
		}
		if state.Phase == snap.Phase {
			if w.shouldRenotifyIdle(state, snap) {
				if err := w.publish(inst, snap.Phase, snap, phaseTransitionIdleRenotify); err != nil {
					return err
				}
				state.LastIdleNotifiedAt = w.now().UTC()
				w.states[inst.Name] = state
			}
			continue
		}

		previous := state.Phase
		state.Phase = snap.Phase
		state.LastIdleNotifiedAt = time.Time{}
		switch {
		case snap.Phase == phaseTransitionBlocked && w.cfg.enabled(phaseTransitionBlocked):
			if err := w.publish(inst, previous, snap, phaseTransitionBlocked); err != nil {
				return err
			}
		case snap.Phase == phaseTransitionIdle && phaseIsBusy(previous) && w.cfg.enabled(phaseTransitionIdle):
			if err := w.publish(inst, previous, snap, phaseTransitionBusyToIdle); err != nil {
				return err
			}
			state.LastIdleNotifiedAt = w.now().UTC()
		}
		w.states[inst.Name] = state
	}
	return nil
}

func (w *phaseTransitionWatcher) shouldRenotifyIdle(state phaseWatchState, snap phaseSnapshot) bool {
	return snap.Phase == phaseTransitionIdle &&
		w.cfg.enabled(phaseTransitionIdle) &&
		w.cfg.IdleRenotify > 0 &&
		!state.LastIdleNotifiedAt.IsZero() &&
		!w.now().Before(state.LastIdleNotifiedAt.Add(w.cfg.IdleRenotify))
}

func (w *phaseTransitionWatcher) publish(inst *topology.Instance, previous string, snap phaseSnapshot, transition string) error {
	if w.channels == nil {
		return errors.New("phase watcher: channel store is required")
	}
	msg := phaseTransitionMessage{
		Event:         phaseTransitionEventName,
		Instance:      inst.Name,
		Agent:         inst.Agent,
		Transition:    transition,
		PreviousPhase: normalizeStatusPhase(previous),
		Phase:         snap.Phase,
		Description:   snap.Description,
		LastAction:    snap.LastAction,
		Job:           snap.Job,
		Ticket:        snap.Ticket,
		Branch:        snap.Branch,
		PR:            snap.PR,
		Reason:        snap.Reason,
		AskTo:         snap.AskTo,
		At:            w.now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := w.channels.Publish(supervisorChannelName, phaseTransitionSender, string(body)); err != nil {
		return err
	}
	return nil
}

func (w *phaseTransitionWatcher) persistentInstances() []*topology.Instance {
	if w == nil || w.topo == nil {
		return nil
	}
	instances := w.topo.SortedInstances()
	out := instances[:0]
	for _, inst := range instances {
		if inst == nil || inst.Ephemeral {
			continue
		}
		out = append(out, inst)
	}
	return out
}

func (w *phaseTransitionWatcher) log(format string, args ...any) {
	if w.logf != nil {
		w.logf(format, args...)
	}
}

func readPhaseStatus(teamDir, instance string) (phaseSnapshot, bool) {
	path := filepath.Join(teamDir, "state", instance, "status.toml")
	var sf phaseStatusFile
	if _, err := toml.DecodeFile(path, &sf); err != nil {
		return phaseSnapshot{}, false
	}
	phase := normalizeStatusPhase(sf.Status.Phase)
	if phase == "" {
		return phaseSnapshot{}, false
	}
	snap := phaseSnapshot{
		Phase:       phase,
		Description: sf.Status.Description,
		LastAction:  sf.Status.LastAction,
	}
	if sf.Work != nil {
		snap.Job = sf.Work.Job
		snap.Ticket = sf.Work.Ticket
		snap.Branch = sf.Work.Branch
		snap.PR = sf.Work.PR
	}
	if sf.Blocking != nil {
		snap.Reason = sf.Blocking.Reason
		snap.AskTo = sf.Blocking.AskTo
	}
	return snap, true
}

func normalizeStatusPhase(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func phaseIsBusy(phase string) bool {
	switch normalizeStatusPhase(phase) {
	case phaseTransitionPlanning, phaseTransitionImplementing, phaseTransitionAwaitingReview, phaseTransitionBlocked:
		return true
	default:
		return false
	}
}
