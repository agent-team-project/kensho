package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/topology"
)

func TestLoadNotificationConfigDefaultsAndOverrides(t *testing.T) {
	teamDir := t.TempDir()
	cfg, err := loadNotificationConfig(teamDir)
	if err != nil {
		t.Fatalf("load default notification config: %v", err)
	}
	if !cfg.enabled("blocked") || cfg.enabled("idle") || cfg.IdleRenotify != 0 {
		t.Fatalf("default config = %+v, want blocked-only with renotify off", cfg)
	}

	body := []byte(`[notifications]
phase_transitions = ["idle", "blocked"]
idle_renotify = "30m"
`)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), body, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err = loadNotificationConfig(teamDir)
	if err != nil {
		t.Fatalf("load override notification config: %v", err)
	}
	if !cfg.enabled("idle") || !cfg.enabled("blocked") || cfg.IdleRenotify != 30*time.Minute {
		t.Fatalf("override config = %+v, want idle+blocked and 30m renotify", cfg)
	}
}

func TestLoadNotificationConfigRejectsUnknownPhase(t *testing.T) {
	teamDir := t.TempDir()
	body := []byte(`[notifications]
phase_transitions = ["done"]
`)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), body, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadNotificationConfig(teamDir); err == nil {
		t.Fatal("load notification config accepted unsupported phase")
	}
}

func TestPhaseTransitionWatcherBaselinesWithoutPublishing(t *testing.T) {
	w, channels, _ := newTestPhaseWatcher(t, notificationConfigForTest("idle", "blocked"))
	writePhaseStatus(t, w.teamDir, "manager", "blocked", "waiting")

	if err := w.baseline(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if err := w.tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := supervisorMessages(t, channels); len(got) != 0 {
		t.Fatalf("messages = %+v, want none for startup baseline", got)
	}
}

func TestPhaseTransitionWatcherDefaultPublishesBlockedOnly(t *testing.T) {
	w, channels, _ := newTestPhaseWatcher(t, defaultNotificationConfig())
	writePhaseStatus(t, w.teamDir, "manager", "implementing", "working")
	writePhaseStatus(t, w.teamDir, "worker-squ-1", "blocked", "ephemeral ignored")

	if err := w.baseline(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	writePhaseStatus(t, w.teamDir, "manager", "idle", "done")
	if err := w.tick(); err != nil {
		t.Fatalf("idle tick: %v", err)
	}
	if got := supervisorMessages(t, channels); len(got) != 0 {
		t.Fatalf("messages after idle = %+v, want none by default", got)
	}

	writeBlockedPhaseStatus(t, w.teamDir, "manager", "Need input", "manager")
	if err := w.tick(); err != nil {
		t.Fatalf("blocked tick: %v", err)
	}
	if err := w.tick(); err != nil {
		t.Fatalf("repeat tick: %v", err)
	}

	got := supervisorMessages(t, channels)
	if len(got) != 1 {
		t.Fatalf("messages = %+v, want exactly one blocked transition", got)
	}
	msg := got[0]
	if msg.Transition != phaseTransitionBlocked || msg.PreviousPhase != phaseTransitionIdle || msg.Phase != phaseTransitionBlocked {
		t.Fatalf("blocked message = %+v", msg)
	}
	if msg.Instance != "manager" || msg.Agent != "manager" || msg.Reason != "Need input" || msg.AskTo != "manager" {
		t.Fatalf("blocked ownership/details = %+v", msg)
	}
}

func TestPhaseTransitionWatcherIdleOptInPublishesBusyToIdleOnce(t *testing.T) {
	w, channels, _ := newTestPhaseWatcher(t, notificationConfigForTest("idle"))
	writePhaseStatus(t, w.teamDir, "manager", "idle", "already idle")
	if err := w.baseline(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if err := w.tick(); err != nil {
		t.Fatalf("startup idle tick: %v", err)
	}
	if got := supervisorMessages(t, channels); len(got) != 0 {
		t.Fatalf("startup idle messages = %+v, want none", got)
	}

	writePhaseStatus(t, w.teamDir, "manager", "implementing", "working")
	if err := w.tick(); err != nil {
		t.Fatalf("busy tick: %v", err)
	}
	writePhaseStatus(t, w.teamDir, "manager", "idle", "finished")
	if err := w.tick(); err != nil {
		t.Fatalf("idle tick: %v", err)
	}
	if err := w.tick(); err != nil {
		t.Fatalf("repeat idle tick: %v", err)
	}

	got := supervisorMessages(t, channels)
	if len(got) != 1 {
		t.Fatalf("messages = %+v, want one busy->idle transition", got)
	}
	msg := got[0]
	if msg.Transition != phaseTransitionBusyToIdle || msg.PreviousPhase != phaseTransitionImplementing || msg.Phase != phaseTransitionIdle {
		t.Fatalf("idle message = %+v", msg)
	}

	writePhaseStatus(t, w.teamDir, "manager", "done", "terminal")
	if err := w.tick(); err != nil {
		t.Fatalf("done tick: %v", err)
	}
	writePhaseStatus(t, w.teamDir, "manager", "idle", "idle again")
	if err := w.tick(); err != nil {
		t.Fatalf("done->idle tick: %v", err)
	}
	if got := supervisorMessages(t, channels); len(got) != 1 {
		t.Fatalf("messages after done->idle = %+v, want no extra idle notification", got)
	}
}

func TestPhaseTransitionWatcherFirstObservedBlockedPublishes(t *testing.T) {
	w, channels, _ := newTestPhaseWatcher(t, defaultNotificationConfig())
	if err := w.baseline(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	writePhaseStatus(t, w.teamDir, "manager", "blocked", "blocked immediately")
	if err := w.tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	got := supervisorMessages(t, channels)
	if len(got) != 1 {
		t.Fatalf("messages = %+v, want one first-observed blocked notification", got)
	}
	if got[0].PreviousPhase != phaseTransitionUnknown || got[0].Phase != phaseTransitionBlocked {
		t.Fatalf("first-observed blocked message = %+v", got[0])
	}
}

func TestPhaseTransitionWatcherIdleRenotify(t *testing.T) {
	cfg := notificationConfigForTest("idle")
	cfg.IdleRenotify = 30 * time.Minute
	w, channels, now := newTestPhaseWatcher(t, cfg)
	writePhaseStatus(t, w.teamDir, "manager", "implementing", "working")
	if err := w.baseline(); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	writePhaseStatus(t, w.teamDir, "manager", "idle", "finished")
	if err := w.tick(); err != nil {
		t.Fatalf("idle tick: %v", err)
	}
	*now = (*now).Add(29 * time.Minute)
	if err := w.tick(); err != nil {
		t.Fatalf("early renotify tick: %v", err)
	}
	if got := supervisorMessages(t, channels); len(got) != 1 {
		t.Fatalf("messages before window = %+v, want one transition only", got)
	}

	*now = (*now).Add(time.Minute)
	if err := w.tick(); err != nil {
		t.Fatalf("renotify tick: %v", err)
	}
	got := supervisorMessages(t, channels)
	if len(got) != 2 {
		t.Fatalf("messages after window = %+v, want transition plus renotify", got)
	}
	if got[1].Transition != phaseTransitionIdleRenotify || got[1].PreviousPhase != phaseTransitionIdle || got[1].Phase != phaseTransitionIdle {
		t.Fatalf("renotify message = %+v", got[1])
	}
}

func newTestPhaseWatcher(t *testing.T, cfg notificationConfig) (*phaseTransitionWatcher, *ChannelStore, *time.Time) {
	t.Helper()
	teamDir := t.TempDir()
	topo := &topology.Topology{Instances: map[string]*topology.Instance{
		"manager": {
			Name:      "manager",
			Agent:     "manager",
			Ephemeral: false,
		},
		"worker": {
			Name:      "worker",
			Agent:     "worker",
			Ephemeral: true,
		},
	}}
	channels := NewChannelStore(DaemonRoot(teamDir))
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	w := newPhaseTransitionWatcher(teamDir, topo, channels, cfg)
	w.now = func() time.Time { return now }
	return w, channels, &now
}

func notificationConfigForTest(phases ...string) notificationConfig {
	cfg := notificationConfig{PhaseTransitions: map[string]bool{}}
	for _, phase := range phases {
		cfg.PhaseTransitions[phase] = true
	}
	return cfg
}

func writePhaseStatus(t *testing.T, teamDir, instance, phase, description string) {
	t.Helper()
	dir := filepath.Join(teamDir, "state", instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir status dir: %v", err)
	}
	body := `[status]
phase = "` + phase + `"
description = "` + description + `"

[work]
job = "squ-37"
ticket = "SQU-37"
branch = "squ-37"
pr = "https://github.com/agent-team-project/agent-team/pull/37"
`
	if err := os.WriteFile(filepath.Join(dir, "status.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}
}

func writeBlockedPhaseStatus(t *testing.T, teamDir, instance, reason, askTo string) {
	t.Helper()
	writePhaseStatus(t, teamDir, instance, "blocked", "blocked")
	body := `[status]
phase = "blocked"
description = "blocked"

[blocking]
reason = "` + reason + `"
ask_to = "` + askTo + `"
`
	if err := os.WriteFile(filepath.Join(teamDir, "state", instance, "status.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write blocked status: %v", err)
	}
}

func supervisorMessages(t *testing.T, channels *ChannelStore) []phaseTransitionMessage {
	t.Helper()
	raw, err := readChannelMessagesSince(channels.daemonRoot, supervisorChannelName, 0)
	if err != nil {
		t.Fatalf("read supervisor messages: %v", err)
	}
	out := make([]phaseTransitionMessage, 0, len(raw))
	for _, msg := range raw {
		var decoded phaseTransitionMessage
		if err := json.Unmarshal([]byte(msg.Body), &decoded); err != nil {
			t.Fatalf("decode supervisor message body %q: %v", msg.Body, err)
		}
		out = append(out, decoded)
	}
	return out
}
