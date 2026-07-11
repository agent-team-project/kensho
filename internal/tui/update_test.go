package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

func TestClassifySizeBoundaryAndExhaustiveTotality(t *testing.T) {
	cases := []struct {
		width, height int
		want          SizeClass
	}{
		{59, 50, SizeTooSmall}, {60, 15, SizeTooSmall}, {60, 16, SizeCompact},
		{99, 50, SizeCompact}, {100, 26, SizeCompact}, {100, 27, SizeStandard},
		{100, 40, SizeStandard}, {120, 50, SizeStandard}, {144, 40, SizeStandard},
		{145, 27, SizeStandard}, {145, 30, SizeStandard}, {160, 30, SizeStandard},
		{145, 39, SizeStandard}, {145, 40, SizeWide}, {160, 50, SizeWide},
	}
	for _, test := range cases {
		if got := ClassifySize(test.width, test.height); got != test.want {
			t.Errorf("ClassifySize(%d,%d) = %s, want %s", test.width, test.height, got, test.want)
		}
	}
	valid := map[SizeClass]bool{SizeTooSmall: true, SizeCompact: true, SizeStandard: true, SizeWide: true}
	for width := 0; width <= 320; width++ {
		for height := 0; height <= 120; height++ {
			if class := ClassifySize(width, height); !valid[class] {
				t.Fatalf("invalid class at %dx%d: %q", width, height, class)
			}
		}
	}
}

func TestBootEmitsDiscoveryExactlyOnce(t *testing.T) {
	model := NewModel(fixtureTime, Capabilities{})
	model, commands := Update(model, Boot{})
	if len(commands) != 1 || commands[0].Kind != CommandBootstrap || !model.RefreshInFlight || model.Route != RouteOverview {
		t.Fatalf("first boot model=%+v commands=%+v", model, commands)
	}
	_, commands = Update(model, Boot{})
	if len(commands) != 0 {
		t.Fatalf("second boot commands=%+v", commands)
	}
}

func TestSnapshotTransitionsPreserveLastGoodAndReconnect(t *testing.T) {
	snapshot := smallFixtureSnapshot()
	model := NewModel(fixtureTime, Capabilities{})
	model.Booted = true
	model.RefreshInFlight = true
	for _, source := range daemonclient.SnapshotSources() {
		model, _ = Update(model, SnapshotOK{Source: source, Snapshot: snapshot, At: fixtureTime})
	}
	model, commands := Update(model, RefreshFinished{At: fixtureTime, AnySuccess: true, Complete: true})
	if model.Connection != ConnectionConnected || !model.HasSnapshot() || len(commands) != 1 {
		t.Fatalf("connected model=%+v commands=%+v", model, commands)
	}
	lastJobs := model.Snapshot.Jobs

	model.RefreshInFlight = true
	failedAt := fixtureTime.Add(5 * time.Second)
	model, _ = Update(model, SnapshotError{Source: daemonclient.SourceJobs, Error: "503 unavailable", At: failedAt})
	model, _ = Update(model, RefreshFinished{At: failedAt, Error: "down"})
	if model.Connection != ConnectionDisconnected || len(model.Snapshot.Jobs) != len(lastJobs) {
		t.Fatalf("disconnect lost data: state=%s jobs=%d", model.Connection, len(model.Snapshot.Jobs))
	}
	if !model.Sources[daemonclient.SourceJobs].FetchedAt.Equal(fixtureTime) {
		t.Fatalf("failed refresh changed fetched-at: %+v", model.Sources[daemonclient.SourceJobs])
	}

	model.RefreshInFlight = true
	refreshedAt := fixtureTime.Add(10 * time.Second)
	for _, source := range daemonclient.SnapshotSources() {
		model, _ = Update(model, SnapshotOK{Source: source, Snapshot: snapshot, At: refreshedAt})
	}
	model, _ = Update(model, RefreshFinished{At: refreshedAt, AnySuccess: true, Complete: true})
	if model.Connection != ConnectionReconnected || model.Feedback != "Reconnected" {
		t.Fatalf("reconnect state=%s feedback=%q", model.Connection, model.Feedback)
	}
	model, _ = Update(model, Tick{At: refreshedAt.Add(time.Second), Generation: model.PollGeneration})
	if model.Connection != ConnectionConnected {
		t.Fatalf("ordinary tick after reconnect = %s", model.Connection)
	}
}

func TestPollingGenerationCoalescesManualRefreshAndRejectsStaleTicks(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	model.RefreshInFlight = true
	model, commands := Update(model, RefreshFinished{At: fixtureTime, AnySuccess: true, Complete: true})
	if len(commands) != 1 || commands[0].Kind != CommandTick || !model.PollScheduled {
		t.Fatalf("initial poll schedule model=%+v commands=%+v", model, commands)
	}
	firstGeneration := model.PollGeneration
	for index := range 10 {
		model, commands = Update(model, Key{Name: "r", At: fixtureTime.Add(time.Duration(index+1) * time.Millisecond)})
		if len(commands) != 1 || commands[0].Kind != CommandRefresh {
			t.Fatalf("manual refresh %d commands=%+v", index, commands)
		}
		model, commands = Update(model, RefreshFinished{At: fixtureTime.Add(time.Duration(index+1) * time.Millisecond), AnySuccess: true, Complete: true})
		if len(commands) != 0 || !model.PollScheduled || model.PollGeneration != firstGeneration {
			t.Fatalf("manual finish %d multiplied schedule: model=%+v commands=%+v", index, model, commands)
		}
	}

	model, commands = Update(model, Tick{At: fixtureTime.Add(5 * time.Second), Generation: firstGeneration})
	if model.PollScheduled || !model.RefreshInFlight || len(commands) != 1 || commands[0].Kind != CommandRefresh {
		t.Fatalf("current tick model=%+v commands=%+v", model, commands)
	}
	model, commands = Update(model, RefreshFinished{At: fixtureTime.Add(5 * time.Second), AnySuccess: true, Complete: true})
	if !model.PollScheduled || model.PollGeneration == firstGeneration || len(commands) != 1 || commands[0].Kind != CommandTick {
		t.Fatalf("next generation model=%+v commands=%+v", model, commands)
	}
	currentGeneration := model.PollGeneration
	model, commands = Update(model, Tick{At: fixtureTime.Add(6 * time.Second), Generation: firstGeneration})
	if !model.PollScheduled || model.PollGeneration != currentGeneration || len(commands) != 0 || model.RefreshInFlight {
		t.Fatalf("stale tick changed schedule: model=%+v commands=%+v", model, commands)
	}
	model, commands = Update(model, Key{Name: "p", At: fixtureTime.Add(7 * time.Second)})
	if model.Polling || model.PollScheduled || len(commands) != 0 {
		t.Fatalf("pause did not invalidate scheduler: model=%+v commands=%+v", model, commands)
	}
	pausedGeneration := model.PollGeneration
	model, commands = Update(model, Tick{At: fixtureTime.Add(8 * time.Second), Generation: currentGeneration})
	if model.PollGeneration != pausedGeneration || len(commands) != 0 || model.RefreshInFlight {
		t.Fatalf("paused stale tick changed scheduler: model=%+v commands=%+v", model, commands)
	}
	model, commands = Update(model, Key{Name: "p", At: fixtureTime.Add(9 * time.Second)})
	if !model.Polling || !model.PollScheduled || model.PollGeneration == pausedGeneration || len(commands) != 1 || commands[0].Kind != CommandTick {
		t.Fatalf("resume did not create one fresh scheduler: model=%+v commands=%+v", model, commands)
	}
}

func TestCachedStartupIsExplicitlyStale(t *testing.T) {
	model := NewModel(fixtureTime, Capabilities{})
	model, _ = Update(model, CachedSnapshot{Snapshot: smallFixtureSnapshot()})
	if model.Connection != ConnectionStale || !model.HasSnapshot() {
		t.Fatalf("cached model = %+v", model)
	}
	model.RefreshInFlight = true
	model, _ = Update(model, RefreshFinished{At: fixtureTime.Add(time.Second), CacheUsed: true, Error: "daemon: not running"})
	if model.Connection != ConnectionStale || model.Feedback == "" {
		t.Fatalf("cached failure model = %+v", model)
	}
}

func TestResizePreservesSemanticAttentionFocus(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	model.FocusIndex = 2
	model.Focus = focusRing[2]
	model.Focus.ItemID = "release-2026-07"
	for _, size := range [][2]int{{80, 24}, {160, 50}, {120, 30}, {60, 16}} {
		model, _ = Update(model, Resize{Width: size[0], Height: size[1]})
		if model.Focus.ItemID != "release-2026-07" {
			t.Fatalf("focus after %dx%d = %+v", size[0], size[1], model.Focus)
		}
	}
	model, _ = Update(model, QueryChanged{Value: "id:gh383"})
	if model.Focus.ItemID != "gh383-tui-spec" {
		t.Fatalf("filtered fallback focus = %+v", model.Focus)
	}
}

func TestQueryUnknownFieldLeavesResultsUnchanged(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	before := len(projectOverview(model).Attention)
	model, _ = Update(model, QueryChanged{Value: "mystery:value"})
	if model.QueryError == "" || len(projectOverview(model).Attention) != before {
		t.Fatalf("query error=%q results=%d want=%d", model.QueryError, len(projectOverview(model).Attention), before)
	}
}

func TestAdvertisedBindingRegistryDispatches(t *testing.T) {
	for _, binding := range Bindings() {
		binding := binding
		for _, key := range binding.Keys {
			key := key
			t.Run(binding.ID+"/"+strings.ReplaceAll(key, " ", "+"), func(t *testing.T) {
				model := bindingTestModel(binding.ID, key)
				before := model
				updated, commands := applyBindingKey(model, key)
				assertBindingEffect(t, binding.ID, key, before, updated, commands, true)
			})
		}
	}
}

func bindingTestModel(bindingID, key string) Model {
	model := smallFixtureModel(Capabilities{})
	model.RefreshInFlight = false
	if bindingID == "move" || bindingID == "page" {
		model.FocusIndex = 2
		model = preserveFocus(model)
		rows := projectOverview(model).Attention
		model.Focus.ItemID = rows[min(1, len(rows)-1)].ID
	}
	if bindingID == "escape" {
		model.Query = "status:blocked"
	}
	if bindingID == "go" && key == "g o" {
		model.Route = RouteWork
	}
	return model
}

func applyBindingKey(model Model, key string) (Model, []Command) {
	commands := []Command{}
	for index, part := range strings.Fields(key) {
		var next []Command
		model, next = Update(model, Key{Name: part, At: fixtureTime.Add(time.Duration(index) * 100 * time.Millisecond)})
		commands = append(commands, next...)
	}
	return model, commands
}

func assertBindingEffect(t *testing.T, bindingID, key string, before, updated Model, commands []Command, requireCommand bool) {
	t.Helper()
	switch bindingID {
	case "quit", "cancel":
		if !updated.Quit || requireCommand && (len(commands) != 1 || commands[0].Kind != CommandQuit) {
			t.Fatalf("%s transition = %+v commands=%+v", key, updated, commands)
		}
	case "help":
		if !updated.HasOverlay(OverlayHelp) {
			t.Fatalf("%s did not open help", key)
		}
	case "palette":
		if !updated.HasOverlay(OverlayPalette) {
			t.Fatalf("%s did not open palette", key)
		}
	case "query":
		if !updated.QueryActive {
			t.Fatalf("%s did not activate query", key)
		}
	case "escape":
		if updated.Query != "" {
			t.Fatalf("%s left query %q", key, updated.Query)
		}
	case "next-focus", "previous-focus":
		if updated.FocusIndex == before.FocusIndex {
			t.Fatalf("%s did not move focus", key)
		}
	case "move":
		if key == "up" || key == "down" || key == "j" || key == "k" {
			if updated.Focus.ItemID == before.Focus.ItemID {
				t.Fatalf("%s did not move the focused item", key)
			}
		} else if updated.FocusIndex == before.FocusIndex {
			t.Fatalf("%s did not move focus", key)
		}
	case "page":
		if updated.Focus.ItemID == before.Focus.ItemID {
			t.Fatalf("%s did not page the focused item", key)
		}
	case "inspect", "toggle", "section":
		if updated.Feedback == before.Feedback || updated.Feedback == "" {
			t.Fatalf("%s did not produce intended feedback: %q", key, updated.Feedback)
		}
	case "refresh":
		if !updated.RefreshInFlight || requireCommand && (len(commands) != 1 || commands[0].Kind != CommandRefresh) {
			t.Fatalf("%s commands = %+v model=%+v", key, commands, updated)
		}
	case "poll":
		if updated.Polling == before.Polling {
			t.Fatalf("%s did not toggle polling", key)
		}
	case "go":
		parts := strings.Fields(key)
		want, ok := goRoute(parts[len(parts)-1])
		if !ok || updated.Route != want {
			t.Fatalf("%s route = %s, want %s", key, updated.Route, want)
		}
	default:
		t.Fatalf("binding %q has no behavior assertion", bindingID)
	}
}

func TestOverlaysOwnInputSearchSelectAndRestoreInvoker(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	model.FocusIndex = 3
	model = preserveFocus(model)
	invoker := model.Focus
	model, _ = Update(model, Key{Name: "ctrl+k", At: fixtureTime})
	model, _ = Update(model, Key{Name: "q", At: fixtureTime})
	if model.Quit || model.PaletteQuery != "q" || !model.HasOverlay(OverlayPalette) {
		t.Fatalf("palette did not own q: %+v", model)
	}
	model, _ = Update(model, Key{Name: "backspace", At: fixtureTime})
	for _, key := range []string{"w", "o", "r", "k"} {
		model, _ = Update(model, Key{Name: key, At: fixtureTime})
	}
	model, _ = Update(model, Key{Name: "enter", At: fixtureTime})
	if model.HasOverlay(OverlayPalette) || model.Route != RouteWork {
		t.Fatalf("palette selection = %+v", model)
	}

	model.Route = RouteOverview
	model.Focus = invoker
	model.FocusIndex = 3
	model, _ = Update(model, Key{Name: "?", At: fixtureTime})
	model, _ = Update(model, Key{Name: "enter", At: fixtureTime})
	if !model.HasOverlay(OverlayHelp) || model.Feedback != "Help owns input; use PgUp/PgDn, ? or Esc" {
		t.Fatalf("help did not own Enter: %+v", model)
	}
	model, _ = Update(model, Key{Name: "esc", At: fixtureTime})
	if len(model.Overlays) != 0 || model.Focus != invoker || model.FocusIndex != 3 {
		t.Fatalf("overlay invoker was not restored: %+v", model)
	}
}

func TestQuitClosesModalBeforeProgram(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	model, _ = Update(model, OpenOverlay{Overlay: OverlayHelp})
	model, commands := Update(model, QuitRequested{})
	if model.Quit || len(model.Overlays) != 0 || len(commands) != 0 {
		t.Fatalf("modal quit model=%+v commands=%+v", model, commands)
	}
	model, commands = Update(model, QuitRequested{})
	if !model.Quit || len(commands) != 1 || commands[0].Kind != CommandQuit {
		t.Fatalf("global quit model=%+v commands=%+v", model, commands)
	}
}

func TestAttachTransitionContractPreservesSnapshot(t *testing.T) {
	model := smallFixtureModel(Capabilities{})
	snapshot := model.Snapshot
	model, commands := Update(model, AttachRequested{})
	if model.Polling || len(commands) != 1 || commands[0].Kind != CommandAttach {
		t.Fatalf("attach requested model=%+v commands=%+v", model, commands)
	}
	model, _ = Update(model, AttachStarted{})
	model, commands = Update(model, AttachFailed{Error: "child failed"})
	if !model.Polling || model.Snapshot != snapshot || len(commands) != 1 || commands[0].Kind != CommandRefresh {
		t.Fatalf("attach failed model=%+v commands=%+v", model, commands)
	}
	model.Polling = false
	model, commands = Update(model, AttachReturned{})
	if !model.Polling || model.Snapshot != snapshot || len(commands) != 1 || commands[0].Kind != CommandRefresh {
		t.Fatalf("attach returned model=%+v commands=%+v", model, commands)
	}
}
