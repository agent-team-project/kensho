package tui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
	tea "github.com/charmbracelet/bubbletea"
)

var sgrPattern = regexp.MustCompile("\\x1b\\[[0-9;]*m")

type soakHeapSample struct {
	elapsed time.Duration
	bytes   uint64
}

func TestOverviewProjectionMatchesCanonicalFixture(t *testing.T) {
	projection := projectOverview(smallFixtureModel(Capabilities{}))
	want := OverviewSummary{
		Instances: 6, Running: 4, Jobs: 12, ActiveJobs: 7, BlockedJobs: 2, FailedJobs: 1,
		ModelTiers: 4, BounceClasses: 4, Pipelines: 4, Budgets: 2, Teams: 3, Schedules: 5,
		Deployments: 2, Deadlines: 3,
	}
	if projection.Summary != want {
		t.Fatalf("summary = %+v, want %+v", projection.Summary, want)
	}
	if len(projection.Org) != 7 {
		t.Fatalf("org role rows = %d, want 7", len(projection.Org))
	}
	if len(projection.Attention) != 9 || projection.Attention[0].Status != "failed" {
		t.Fatalf("attention = %+v", projection.Attention)
	}
}

func TestOverviewTelemetryPrecedenceAndRecentWindow(t *testing.T) {
	snapshot := &daemonclient.Snapshot{Resources: map[string]*daemonclient.Resource{}}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("job-%02d", i)
		jobURI := "agt://dep/job/" + id
		outcomeURI := "agt://dep/outcome/" + id
		snapshot.Jobs = append(snapshot.Jobs, &daemonclient.Job{ID: id, URI: jobURI, OutcomeURI: outcomeURI, UpdatedAt: fixtureTime.Add(-time.Duration(i) * time.Minute)})
		model := "gpt-current"
		if i == 24 {
			model = "gpt-too-old"
		}
		snapshot.Resources[outcomeURI] = testResource(outcomeURI, "outcome", id, map[string]any{"model": model, "tier": "T2"})
	}
	if got := distinctModelTiers(snapshot); got != 1 {
		t.Fatalf("recent-24 model tiers = %d, want 1", got)
	}
	snapshot.Jobs = snapshot.Jobs[:4]
	first := snapshot.Jobs[0]
	snapshot.Resources[first.OutcomeURI] = testResource(first.OutcomeURI, "outcome", first.ID, map[string]any{
		"bounce_classes": map[string]any{"capability": 1},
		"bounces":        map[string]any{"infra": 1},
	})
	snapshot.Resources[first.URI] = testResource(first.URI, "job", first.ID, map[string]any{"bounce_classes": map[string]any{"infra": 1}})
	second := snapshot.Jobs[1]
	snapshot.Resources[second.OutcomeURI] = testResource(second.OutcomeURI, "outcome", second.ID, map[string]any{"bounces": []any{map[string]any{"classes": []any{"scope"}}}})
	snapshot.Resources[second.URI] = testResource(second.URI, "job", second.ID, map[string]any{"bounce_classes": map[string]any{"infra": 1}})
	third := snapshot.Jobs[2]
	snapshot.Resources[third.OutcomeURI] = testResource(third.OutcomeURI, "outcome", third.ID, map[string]any{})
	snapshot.Resources[third.URI] = testResource(third.URI, "job", third.ID, map[string]any{
		"bounce_classes": map[string]any{"infra": 1},
		"kickoff":        "## Review findings (bounce 1)\nSpec ambiguity needs clarification.",
	})
	fourth := snapshot.Jobs[3]
	snapshot.Resources[fourth.OutcomeURI] = testResource(fourth.OutcomeURI, "outcome", fourth.ID, map[string]any{})
	snapshot.Resources[fourth.URI] = testResource(fourth.URI, "job", fourth.ID, map[string]any{"kickoff": "## Review findings (bounce 1)\nSpec ambiguity needs clarification."})

	for _, test := range []struct {
		name string
		job  *daemonclient.Job
		want string
	}{
		{name: "explicit outcome beats legacy and job", job: first, want: "capability"},
		{name: "legacy outcome beats job resource", job: second, want: "scope"},
		{name: "job resource beats kickoff", job: third, want: "infra"},
		{name: "kickoff is final fallback", job: fourth, want: "spec-ambiguity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			classes := bounceClassesForJob(snapshot, test.job)
			if len(classes) != 1 || !classes[test.want] {
				t.Fatalf("classes = %v, want only %s", classes, test.want)
			}
		})
	}
	if got := distinctBounceClasses(snapshot); got != 4 {
		t.Fatalf("bounce classes = %d, want capability/scope/infra/spec-ambiguity", got)
	}
}

func TestBounceClassesUseStableRecent24Window(t *testing.T) {
	snapshot := &daemonclient.Snapshot{Resources: map[string]*daemonclient.Resource{}}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("job-%02d", i)
		uri := "agt://dep/outcome/" + id
		snapshot.Jobs = append(snapshot.Jobs, &daemonclient.Job{ID: id, OutcomeURI: uri, UpdatedAt: fixtureTime.Add(-time.Duration(i) * time.Minute)})
		data := map[string]any{}
		if i == 24 {
			data["bounce_classes"] = map[string]any{"infra": 1}
		}
		snapshot.Resources[uri] = testResource(uri, "outcome", id, data)
	}
	if got := distinctBounceClasses(snapshot); got != 0 {
		t.Fatalf("bounce classes = %d, want 0 because the only class is outside recent 24", got)
	}
}

func TestModelTierUsesCanonicalPrimaryStepRunAndTierFallback(t *testing.T) {
	snapshot := &daemonclient.Snapshot{Resources: map[string]*daemonclient.Resource{}}
	for index := range 2 {
		id := fmt.Sprintf("job-%d", index+1)
		jobURI := "agt://dep/job/" + id
		outcomeURI := "agt://dep/outcome/" + id
		job := &daemonclient.Job{
			ID: id, URI: jobURI, OutcomeURI: outcomeURI, Target: "worker", ImplementationAgent: "worker",
			UpdatedAt: fixtureTime.Add(-time.Duration(index) * time.Minute),
		}
		snapshot.Jobs = append(snapshot.Jobs, job)
		snapshot.Resources[jobURI] = testResource(jobURI, "job", id, map[string]any{"steps": []any{
			map[string]any{"id": "implement", "target": "worker", "status": "done"},
			map[string]any{"id": "review", "target": "reviewer", "status": "done"},
		}})
		runs := []any{
			map[string]any{"id": "review", "target": "reviewer", "model": "claude-opus-4-8", "tier": "T1", "status": "done"},
			map[string]any{"id": "implement", "target": "worker", "model": "claude-sonnet-5", "status": "done"},
		}
		if index == 1 {
			runs = []any{runs[1]}
		}
		snapshot.Resources[outcomeURI] = testResource(outcomeURI, "outcome", id, map[string]any{"step_runs": runs})
	}

	if got := distinctModelTiers(snapshot); got != 1 {
		t.Fatalf("primary implement model/tier groups = %d, want 1", got)
	}
	model, tier := jobModelTier(snapshot, snapshot.Jobs[0])
	if model != "claude-sonnet-5" || tier != "T2" {
		t.Fatalf("primary implement telemetry = %q/%q, want claude-sonnet-5/T2", model, tier)
	}
}

func TestPartialRefreshRendersRetainedSourceTimeAndError(t *testing.T) {
	model := smallFixtureModel(Capabilities{Dumb: true})
	refreshedAt := fixtureTime.Add(5 * time.Second)
	for _, source := range []daemonclient.SnapshotSource{daemonclient.SourceInstances, daemonclient.SourceTopology, daemonclient.SourceResources} {
		model, _ = Update(model, SnapshotOK{Source: source, Snapshot: smallFixtureSnapshot(), At: refreshedAt})
	}
	model, _ = Update(model, SnapshotError{Source: daemonclient.SourceJobs, Error: "daemon: jobs: 503 unavailable", At: refreshedAt})
	model.RefreshInFlight = true
	model, _ = Update(model, RefreshFinished{At: refreshedAt, AnySuccess: true})
	frame := Render(model)
	for _, want := range []string{"PARTIAL", "collections 2/3", "JOBS retained 12:04:05 ERROR: daemon: jobs: 503 unavailable"} {
		if !strings.Contains(frame, want) {
			t.Errorf("partial frame missing %q:\n%s", want, frame)
		}
	}
	if !model.Sources[daemonclient.SourceInstances].FetchedAt.Equal(refreshedAt) || !model.Sources[daemonclient.SourceJobs].FetchedAt.Equal(fixtureTime) {
		t.Fatalf("source times = %+v", model.Sources)
	}
}

func TestEveryFailedSourceRendersRetainedTimeAndCurrentError(t *testing.T) {
	model := smallFixtureModel(Capabilities{Dumb: true})
	for _, source := range daemonclient.SnapshotSources() {
		model, _ = Update(model, SnapshotError{Source: source, Error: string(source) + "-503", At: fixtureTime.Add(time.Second)})
	}
	model.Connection = ConnectionPartial
	frame := Render(model)
	for _, source := range daemonclient.SnapshotSources() {
		want := strings.ToUpper(string(source)) + " retained 12:04:05 ERROR: " + string(source) + "-503"
		if !strings.Contains(frame, want) {
			t.Errorf("frame omits failed source %q:\n%s", want, frame)
		}
	}
}

func TestHelpPagesExposeEveryBindingAtEveryCanonicalGeometry(t *testing.T) {
	for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
		model := smallFixtureModel(Capabilities{Dumb: true})
		model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
		model, _ = Update(model, OpenOverlay{Overlay: OverlayHelp})
		var pages strings.Builder
		for page := 0; page < helpPageCount(model); page++ {
			model.HelpPage = page
			pages.WriteString(Render(model))
		}
		for _, binding := range Bindings() {
			if !strings.Contains(pages.String(), binding.Label) || !strings.Contains(pages.String(), binding.Description) {
				t.Errorf("%dx%d help pages omit binding %+v", geometry[0], geometry[1], binding)
			}
		}
	}
}

func TestEveryFocusTargetHasPlainTextMarker(t *testing.T) {
	model := smallFixtureModel(Capabilities{Dumb: true})
	wants := []string{"> Fleet", "> Work", "> job", "> auditor", "> Filter"}
	for index, want := range wants {
		model.FocusIndex = index
		model = preserveFocus(model)
		if frame := Render(model); !strings.Contains(frame, want) {
			t.Errorf("focus %d frame missing %q", index, want)
		}
	}
}

func TestCanonicalRendersAreExactStableFrames(t *testing.T) {
	geometries := [][2]int{{80, 24}, {120, 30}, {160, 50}}
	modes := []struct {
		name string
		caps Capabilities
	}{
		{"color", Capabilities{Color: true}},
		{"NO_COLOR", Capabilities{}},
		{"TERM=dumb", Capabilities{Dumb: true}},
	}
	for _, geometry := range geometries {
		for _, mode := range modes {
			t.Run(fmt.Sprintf("%dx%d/%s", geometry[0], geometry[1], mode.name), func(t *testing.T) {
				model := smallFixtureModel(mode.caps)
				model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
				first := Render(model)
				second := Render(model)
				if first != second {
					t.Fatal("two consecutive clean renders differ")
				}
				plain := sgrPattern.ReplaceAllString(first, "")
				assertFrameGeometry(t, plain, geometry[0], geometry[1])
				if mode.name == "color" && !strings.Contains(first, "\x1b[") {
					t.Fatal("color mode emitted no SGR styling")
				}
				if mode.name != "color" && strings.Contains(first, "\x1b") {
					t.Fatalf("plain mode emitted escape byte: %q", first)
				}
			})
		}
	}
}

func TestCanonicalGoldenHashes(t *testing.T) {
	want := map[string]string{
		"80x24/color":      "6fc69d96048c2e53be14f721f5634f5e15b0de5c51b0e5ec160342149abb77c1",
		"80x24/NO_COLOR":   "88a1ac3e982bd240da91b54300548b67462c7884415c76ccc7aa09e0b6e65bdd",
		"80x24/TERM=dumb":  "88a1ac3e982bd240da91b54300548b67462c7884415c76ccc7aa09e0b6e65bdd",
		"120x30/color":     "cbbf9ce369e711effe29f74494d8a209b209c158f0cf4aa0d7c7edeb7a2914ed",
		"120x30/NO_COLOR":  "66db77dadb1f588a6fb080f411eab45f10f4ee1903cb44095b01bc8def5d1b32",
		"120x30/TERM=dumb": "66db77dadb1f588a6fb080f411eab45f10f4ee1903cb44095b01bc8def5d1b32",
		"160x50/color":     "d0725c00adddf09fbe1523c9b0fed6174000316c5d0b9bd1029ab37af16c43f4",
		"160x50/NO_COLOR":  "504720ff6b61e308de9ca3e17b720be9e33d0d819c2707feb9e298f38933bc63",
		"160x50/TERM=dumb": "504720ff6b61e308de9ca3e17b720be9e33d0d819c2707feb9e298f38933bc63",
	}
	modes := []struct {
		name string
		caps Capabilities
	}{{"color", Capabilities{Color: true}}, {"NO_COLOR", Capabilities{}}, {"TERM=dumb", Capabilities{Dumb: true}}}
	for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
		for _, mode := range modes {
			model := smallFixtureModel(mode.caps)
			model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
			frame := Render(model)
			key := fmt.Sprintf("%dx%d/%s", geometry[0], geometry[1], mode.name)
			got := fmt.Sprintf("%x", sha256.Sum256([]byte(frame)))
			if got != want[key] {
				t.Errorf("golden %s hash = %s, want %s", key, got, want[key])
			}
		}
	}
}

func TestCanonicalGoldenFiles(t *testing.T) {
	modes := []struct {
		name string
		caps Capabilities
	}{{"color", Capabilities{Color: true}}, {"no_color", Capabilities{}}, {"term_dumb", Capabilities{Dumb: true}}}
	for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
		for _, mode := range modes {
			model := smallFixtureModel(mode.caps)
			model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
			frame := []byte(Render(model))
			path := filepath.Join("testdata", fmt.Sprintf("overview_%dx%d_%s.golden", geometry[0], geometry[1], mode.name))
			if os.Getenv("UPDATE_TUI_GOLDENS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, frame, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(frame, want) {
				t.Errorf("golden mismatch: %s (set UPDATE_TUI_GOLDENS=1 to review an intentional update)", path)
			}
		}
	}
}

func TestTermDumbFramesAreASCIIAndControlFree(t *testing.T) {
	for _, geometry := range [][2]int{{80, 24}, {120, 30}, {160, 50}} {
		model := smallFixtureModel(Capabilities{Dumb: true})
		model, _ = Update(model, Resize{Width: geometry[0], Height: geometry[1]})
		frame := []byte(Render(model))
		for index, value := range frame {
			if value == 0x1b || value == 0x9b || value == 0x9d {
				t.Fatalf("%dx%d forbidden byte %#x at %d", geometry[0], geometry[1], value, index)
			}
			if value >= utf8.RuneSelf {
				t.Fatalf("%dx%d non-ASCII byte %#x at %d", geometry[0], geometry[1], value, index)
			}
		}
		if strings.ContainsAny(string(frame), "┌┐└┘─│├┤┬┴┼") {
			t.Fatalf("%dx%d contains Unicode box drawing", geometry[0], geometry[1])
		}
	}
}

func TestTooSmallFrameIsStableAndUseful(t *testing.T) {
	model := smallFixtureModel(Capabilities{Dumb: true})
	model, _ = Update(model, Resize{Width: 59, Height: 15})
	frame := Render(model)
	assertFrameGeometry(t, frame, 59, 15)
	for _, text := range []string{"TERMINAL TOO SMALL", "59x15", "60x16", "Help", "Quit"} {
		if !strings.Contains(frame, text) {
			t.Errorf("frame missing %q", text)
		}
	}
}

func TestBoundaryShellKeepsHelpAndQuitKeyboardComplete(t *testing.T) {
	model := smallFixtureModel(Capabilities{Dumb: true})
	model, _ = Update(model, Resize{Width: 59, Height: 50})
	closed := Render(model)
	model, _ = Update(model, Key{Name: "?", At: fixtureTime})
	opened := Render(model)
	if opened == closed || !strings.Contains(opened, "Help") || !strings.Contains(opened, "q            quit") {
		t.Fatalf("59x50 help did not visibly own the too-small frame:\n%s", opened)
	}
	model, _ = Update(model, Key{Name: "pgdown", At: fixtureTime})
	if page := Render(model); !strings.Contains(page, "g+key        screen") {
		t.Fatalf("59x50 help paging omitted the final registry binding:\n%s", page)
	}
	model, _ = Update(model, Key{Name: "esc", At: fixtureTime})
	if model.HasOverlay(OverlayHelp) || Render(model) != closed {
		t.Fatal("too-small help did not close back to the invoking shell")
	}

	model, _ = Update(model, Resize{Width: 80, Height: 24})
	frame := Render(model)
	if !strings.Contains(strings.Split(frame, "\n")[22], "q quit") {
		t.Fatalf("80x24 compact footer has no visible quit path:\n%s", frame)
	}
}

func TestLargeFleetFirstPaint(t *testing.T) {
	model := largeFixtureModel()
	model, _ = Update(model, Resize{Width: 160, Height: 50})
	start := time.Now()
	frame := Render(model)
	elapsed := time.Since(start)
	assertFrameGeometry(t, frame, 160, 50)
	if elapsed > 150*time.Millisecond {
		t.Fatalf("first paint = %s, limit 150ms", elapsed)
	}
	if !strings.Contains(frame, "100 instances") || !strings.Contains(frame, "500 jobs") {
		t.Fatalf("large fixture counts missing from frame")
	}
}

func TestOneHourSoak(t *testing.T) {
	if os.Getenv("AGENT_TEAM_TUI_SOAK") != "1" {
		t.Skip("set AGENT_TEAM_TUI_SOAK=1 for the one-hour acceptance soak")
	}
	t.Setenv("AGENT_TEAM_DAEMON_URL", "")
	t.Setenv("AGENT_TEAM_DAEMON_TOKEN_FILE", "")
	t.Setenv("AGENT_TEAM_DAEMON_SOCKET", "")
	duration := time.Hour
	if override := os.Getenv("AGENT_TEAM_TUI_SOAK_DURATION"); override != "" {
		parsed, err := time.ParseDuration(override)
		if err != nil || parsed < 15*time.Second {
			t.Fatalf("AGENT_TEAM_TUI_SOAK_DURATION must be at least 15s: %q (%v)", override, err)
		}
		duration = parsed
	}
	harness := newSeededLiveDaemon(t)
	harness.start(t)
	clockAt := fixtureTime
	commandRuntime := &commandRuntime{ctx: context.Background(), teamDir: harness.teamDir, clock: func() time.Time { return clockAt }}
	program := newProgramModel(NewModel(clockAt, Capabilities{Dumb: true}), commandRuntime)
	var scheduledAt time.Time
	var scheduledDelay time.Duration
	var schedulePending bool
	schedules := 0
	program.tick = func(delay time.Duration, message func(time.Time) tea.Msg) tea.Cmd {
		if schedulePending {
			t.Fatal("production polling scheduler created a second pending timer")
		}
		schedulePending = true
		scheduledAt = time.Now()
		scheduledDelay = delay
		schedules++
		return tea.Tick(delay, message)
	}
	initial := runSoakCommand(t, program.Init())
	updated, pending := program.Update(initial)
	program = updated.(ProgramModel)
	updated, _ = program.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	program = updated.(ProgramModel)
	if !program.Domain.HasSnapshot() || program.Domain.Connection != ConnectionConnected || !program.Domain.PollScheduled || pending == nil {
		t.Fatalf("soak initial domain = connection %s scheduled %v errors=%v", program.Domain.Connection, program.Domain.PollScheduled, program.Domain.Snapshot.SourceErrors)
	}
	startGoroutines := runtime.NumGoroutine()
	startFDs, err := openFDCount()
	if err != nil {
		t.Fatalf("file-descriptor metric unavailable: %v", err)
	}
	started := time.Now()
	deadline := started.Add(duration)
	cadence := 5 * time.Second
	disconnectAt := started.Add(duration / 3)
	reconnectAt := disconnectAt.Add(30 * time.Second)
	if duration < 5*time.Minute {
		reconnectAt = disconnectAt.Add(min(2*cadence, duration/3))
	}
	finalWindow := 30 * time.Minute
	if duration < time.Hour {
		finalWindow = duration / 2
	}
	baselineReady := false
	var samples []soakHeapSample
	refreshes, filterChanges, navigations := 0, 0, 0
	disconnected, reconnected := false, false
	cadenceChecks := 0
	nextHeapSample := started.Add(time.Minute)
	if duration < 10*time.Minute {
		nextHeapSample = started.Add(cadence)
	}
	for time.Now().Before(deadline) {
		now := time.Now()
		if !disconnected && !now.Before(disconnectAt) {
			harness.stop(t)
			disconnected = true
		}
		if disconnected && !reconnected && !now.Before(reconnectAt) {
			harness.start(t)
			reconnected = true
		}
		if !schedulePending || pending == nil {
			t.Fatal("production polling scheduler lost its sole pending timer")
		}
		wantDelay := nextPollDelay(program.Domain)
		if scheduledDelay != wantDelay {
			t.Fatalf("scheduled delay = %s, want model cadence %s in %s", scheduledDelay, wantDelay, program.Domain.Connection)
		}
		schedulePending = false
		tickMessage := runSoakCommand(t, pending)
		now = time.Now()
		observedDelay := now.Sub(scheduledAt)
		if observedDelay < scheduledDelay-time.Second || observedDelay > scheduledDelay+time.Second {
			t.Fatalf("production polling cadence drifted: got %s, want %s±1s", observedDelay, scheduledDelay)
		}
		cadenceChecks++
		if now.After(deadline) {
			break
		}
		clockAt = fixtureTime.Add(now.Sub(started))
		updated, refreshCommand := program.Update(tickMessage)
		program = updated.(ProgramModel)
		if refreshCommand == nil || !program.Domain.RefreshInFlight || program.Domain.PollScheduled {
			t.Fatalf("tick did not consume one schedule and start one refresh: %+v", program.Domain)
		}
		refreshMessage := runSoakCommand(t, refreshCommand)
		updated, pending = program.Update(refreshMessage)
		program = updated.(ProgramModel)
		refreshes++
		if !schedulePending || !program.Domain.PollScheduled || pending == nil {
			t.Fatal("refresh completion did not leave exactly one production timer")
		}
		if disconnected && !reconnected && program.Domain.Connection != ConnectionDisconnected {
			t.Fatalf("real transport loss rendered %s, want disconnected", program.Domain.Connection)
		}
		if reconnected && program.Domain.Connection != ConnectionReconnected && program.Domain.Connection != ConnectionConnected {
			t.Fatalf("real transport recovery rendered %s", program.Domain.Connection)
		}
		navigation := []struct {
			key   string
			route Route
		}{
			{"o", RouteOverview}, {"w", RouteWork}, {"f", RouteFleet}, {"a", RouteActivity},
			{"l", RouteLogs}, {"s", RouteResearch}, {"r", RouteRequirements}, {"e", RouteRelease},
		}[navigations%len(routeOrder)]
		program.Domain, _ = Update(program.Domain, Key{Name: "g", At: clockAt})
		program.Domain, _ = Update(program.Domain, Key{Name: navigation.key, At: clockAt.Add(100 * time.Millisecond)})
		if program.Domain.Route != navigation.route {
			t.Fatalf("soak navigation %d route = %s, want %s", navigations, program.Domain.Route, navigation.route)
		}
		navigations++
		if refreshes%3 == 0 {
			before := len(projectOverview(program.Domain).Attention)
			query := "status:failed"
			if program.Domain.Query != "" {
				query = ""
			}
			program.Domain, _ = Update(program.Domain, QueryChanged{Value: query})
			program.Domain, _ = Update(program.Domain, QueryCommit{})
			if len(projectOverview(program.Domain).Attention) != before {
				filterChanges++
			}
		}
		frame := program.View()
		assertFrameGeometry(t, frame, 160, 50)
		if strings.ContainsRune(frame, '\x00') || strings.ContainsRune(frame, '\x1b') {
			t.Fatal("soak observed a corrupt/control-bearing TERM=dumb frame")
		}
		remaining := deadline.Sub(now)
		if !baselineReady && soakBaselineReady(remaining, finalWindow, disconnected, reconnected) {
			baselineReady = true
		}
		if !now.Before(nextHeapSample) {
			runtime.GC()
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			if remaining <= finalWindow {
				samples = append(samples, soakHeapSample{elapsed: now.Sub(started), bytes: memory.HeapAlloc})
			}
			nextHeapSample = now.Add(time.Minute)
			if duration < 10*time.Minute {
				nextHeapSample = now.Add(cadence)
			}
		}
	}
	if !disconnected || !reconnected {
		t.Fatalf("soak did not execute real disconnect/reconnect: disconnected=%v reconnected=%v", disconnected, reconnected)
	}
	minimumRefreshes := max(1, int(duration/cadence)-6)
	if refreshes < minimumRefreshes || filterChanges == 0 || navigations != refreshes || schedules != refreshes+1 || cadenceChecks < refreshes {
		t.Fatalf("soak coverage refreshes=%d want>=%d schedules=%d cadence_checks=%d filter_changes=%d navigations=%d", refreshes, minimumRefreshes, schedules, cadenceChecks, filterChanges, navigations)
	}
	if !baselineReady {
		t.Fatal("soak never captured a post-warm-up heap baseline")
	}
	runtime.GC()
	var finalMemory runtime.MemStats
	runtime.ReadMemStats(&finalMemory)
	samples = append(samples, soakHeapSample{elapsed: duration, bytes: finalMemory.HeapAlloc})
	if runtime.NumGoroutine() > startGoroutines+2 {
		t.Fatalf("goroutines grew from %d to %d", startGoroutines, runtime.NumGoroutine())
	}
	finalFDs, err := openFDCount()
	if err != nil {
		t.Fatalf("final file-descriptor metric unavailable: %v", err)
	}
	if finalFDs > startFDs {
		t.Fatalf("file descriptors grew from %d to %d", startFDs, finalFDs)
	}
	window := min(5, len(samples)/2)
	if window == 0 {
		t.Fatal("soak did not collect enough final-window heap samples")
	}
	baseline, retainedFinal, limit, retained := heapRetentionWithinTenPercent(samples, window)
	if !retained {
		t.Fatalf("retained heap median grew from %d to %d (limit %d, exact 10%%; window=%d)", baseline, retainedFinal, limit, window)
	}
	slope := heapSlopeBytesPerHour(samples)
	if slope > 1024*1024 {
		t.Fatalf("final-window retained-heap slope = %.0f bytes/hour, limit 1048576", slope)
	}
	t.Logf("SOAK EVIDENCE duration=%s scheduler=ProgramModel/tea.Tick cadence=%s schedules=%d cadence_checks=%d refreshes=%d filters=%d navigations=%d routes=%d real_disconnect=true real_reconnect=true final_window=%s heap_slope_bytes_per_hour=%.0f retained_window_samples=%d retained_baseline_median=%d retained_final_median=%d retained_limit=%d goroutines=%d->%d fds=%d->%d", duration, cadence, schedules, cadenceChecks, refreshes, filterChanges, navigations, len(routeOrder), finalWindow, slope, window, baseline, retainedFinal, limit, startGoroutines, runtime.NumGoroutine(), startFDs, finalFDs)
}

func runSoakCommand(t *testing.T, command tea.Cmd) tea.Msg {
	t.Helper()
	if command == nil {
		t.Fatal("soak production command is nil")
	}
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		return message
	}
	if len(batch) != 1 {
		t.Fatalf("soak expected one production command, got batch of %d", len(batch))
	}
	return runSoakCommand(t, batch[0])
}

func soakBaselineReady(remaining, finalWindow time.Duration, disconnected, reconnected bool) bool {
	return disconnected && reconnected && remaining <= finalWindow
}

func TestSoakRetainedHeapBaselineStartsAfterRecoveryInFinalWindow(t *testing.T) {
	finalWindow := 30 * time.Minute
	for _, test := range []struct {
		name                      string
		remaining                 time.Duration
		disconnected, reconnected bool
		want                      bool
	}{
		{name: "before final window", remaining: finalWindow + time.Second, disconnected: true, reconnected: true},
		{name: "before disconnect", remaining: finalWindow, reconnected: true},
		{name: "before reconnect", remaining: finalWindow, disconnected: true},
		{name: "post recovery final window", remaining: finalWindow, disconnected: true, reconnected: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := soakBaselineReady(test.remaining, finalWindow, test.disconnected, test.reconnected); got != test.want {
				t.Fatalf("soakBaselineReady() = %v, want %v", got, test.want)
			}
		})
	}
}

func openFDCount() (int, error) {
	var pathErrors []string
	for _, path := range []string{"/proc/self/fd", "/dev/fd"} {
		entries, err := os.ReadDir(path)
		if err == nil {
			return len(entries), nil
		}
		pathErrors = append(pathErrors, path+": "+err.Error())
	}
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return 0, fmt.Errorf("%s; lsof: %w", strings.Join(pathErrors, "; "), err)
	}
	body, err := exec.Command(lsof, "-a", "-p", strconv.Itoa(os.Getpid()), "-Fn").Output()
	if err != nil {
		return 0, fmt.Errorf("%s; lsof: %w", strings.Join(pathErrors, "; "), err)
	}
	count := 0
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "f") {
			count++
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("%s; lsof returned no descriptor records", strings.Join(pathErrors, "; "))
	}
	return count, nil
}

func TestOpenFDCountAvailable(t *testing.T) {
	count, err := openFDCount()
	if err != nil || count <= 0 {
		t.Fatalf("openFDCount() = %d, %v", count, err)
	}
}

func heapSlopeBytesPerHour(samples []soakHeapSample) float64 {
	if len(samples) < 2 {
		return 0
	}
	var sumX, sumY float64
	for _, sample := range samples {
		sumX += sample.elapsed.Hours()
		sumY += float64(sample.bytes)
	}
	meanX := sumX / float64(len(samples))
	meanY := sumY / float64(len(samples))
	var numerator, denominator float64
	for _, sample := range samples {
		x := sample.elapsed.Hours() - meanX
		numerator += x * (float64(sample.bytes) - meanY)
		denominator += x * x
	}
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func heapWindowMedian(samples []soakHeapSample) uint64 {
	values := make([]uint64, len(samples))
	for index, sample := range samples {
		values[index] = sample.bytes
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return 0
	}
	return values[len(values)/2]
}

func heapRetentionWithinTenPercent(samples []soakHeapSample, window int) (baseline, final, limit uint64, ok bool) {
	if window <= 0 || len(samples) < 2*window {
		return 0, 0, 0, false
	}
	baseline = heapWindowMedian(samples[:window])
	final = heapWindowMedian(samples[len(samples)-window:])
	limit = baseline + baseline/10
	return baseline, final, limit, final <= limit
}

func TestHeapWindowMedianIsOrderIndependent(t *testing.T) {
	samples := []soakHeapSample{{bytes: 9}, {bytes: 3}, {bytes: 7}, {bytes: 1}, {bytes: 5}}
	if got := heapWindowMedian(samples); got != 5 {
		t.Fatalf("heapWindowMedian() = %d, want 5", got)
	}
}

func TestHeapRetentionUsesExactTenPercentMedianCeiling(t *testing.T) {
	for _, test := range []struct {
		name       string
		final      uint64
		wantWithin bool
	}{
		{name: "exact ceiling passes", final: 110, wantWithin: true},
		{name: "one byte over fails", final: 111, wantWithin: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			samples := []soakHeapSample{{bytes: 100}, {bytes: 100}, {bytes: 100}, {bytes: test.final}, {bytes: test.final}, {bytes: test.final}}
			baseline, final, limit, within := heapRetentionWithinTenPercent(samples, 3)
			if baseline != 100 || final != test.final || limit != 110 || within != test.wantWithin {
				t.Fatalf("retention = baseline %d final %d limit %d within %v", baseline, final, limit, within)
			}
		})
	}
}

func assertFrameGeometry(t *testing.T, frame string, width, height int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != height {
		t.Fatalf("frame rows = %d, want %d", len(lines), height)
	}
	for row, line := range lines {
		if got := utf8.RuneCountInString(line); got != width {
			t.Fatalf("row %d cells = %d, want %d: %q", row, got, width, line)
		}
	}
}
