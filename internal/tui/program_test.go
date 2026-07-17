package tui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func TestTeatestKeyboardResizeDisconnectReconnectFlow(t *testing.T) {
	domain := smallFixtureModel(Capabilities{})
	domain.Polling = false
	domain.FocusIndex = 2
	domain = preserveFocus(domain)
	testModel := teatest.NewTestModel(t, NewTestProgramModel(domain), teatest.WithInitialTermSize(80, 24))

	testModel.Send(tea.KeyMsg{Type: tea.KeyTab})
	testModel.Send(tea.KeyMsg{Type: tea.KeyDown})
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	testModel.Send(tea.KeyMsg{Type: tea.KeyCtrlK})
	testModel.Type("work")
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	testModel.Send(tea.KeyMsg{Type: tea.KeyPgDown})
	testModel.Send(tea.KeyMsg{Type: tea.KeyPgUp})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyShiftTab}, {Type: tea.KeyUp}, {Type: tea.KeyDown},
		{Type: tea.KeyLeft}, {Type: tea.KeyRight},
		{Type: tea.KeyRunes, Runes: []rune{'h'}}, {Type: tea.KeyRunes, Runes: []rune{'j'}},
		{Type: tea.KeyRunes, Runes: []rune{'k'}}, {Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeySpace}, {Type: tea.KeyPgUp}, {Type: tea.KeyPgDown},
		{Type: tea.KeyHome}, {Type: tea.KeyEnd},
		{Type: tea.KeyRunes, Runes: []rune{'['}}, {Type: tea.KeyRunes, Runes: []rune{']'}},
		{Type: tea.KeyRunes, Runes: []rune{'p'}}, {Type: tea.KeyRunes, Runes: []rune{'p'}},
	} {
		testModel.Send(key)
	}
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	testModel.Type("status:blocked")
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool {
		return bytes.Contains(output, []byte("status:blocked"))
	}, teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	for i := 0; i < 120; i++ {
		width := 60 + i%120
		height := 16 + i%35
		testModel.Send(tea.WindowSizeMsg{Width: width, Height: height})
	}
	testModel.Send(tea.WindowSizeMsg{Width: 120, Height: 30})

	failedAt := fixtureTime.Add(time.Minute)
	testModel.Send(refreshBatch{messages: []Msg{
		SnapshotError{Source: daemonclient.SourceInstances, Error: "connection refused", At: failedAt},
		SnapshotError{Source: daemonclient.SourceJobs, Error: "connection refused", At: failedAt},
		SnapshotError{Source: daemonclient.SourceTopology, Error: "connection refused", At: failedAt},
		SnapshotError{Source: daemonclient.SourceResources, Error: "connection refused", At: failedAt},
		RefreshFinished{At: failedAt, Error: "connection refused"},
	}})
	reconnectedAt := failedAt.Add(time.Second)
	messages := []Msg{}
	for _, source := range daemonclient.SnapshotSources() {
		messages = append(messages, SnapshotOK{Source: source, Snapshot: smallFixtureSnapshot(), At: reconnectedAt})
	}
	messages = append(messages, RefreshFinished{At: reconnectedAt, AnySuccess: true, Complete: true})
	testModel.Send(refreshBatch{messages: messages})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	testModel.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	final := testModel.FinalModel(t, teatest.WithFinalTimeout(3*time.Second))
	program, ok := final.(ProgramModel)
	if !ok {
		t.Fatalf("final model = %T", final)
	}
	if program.Domain.Connection != ConnectionReconnected || program.Domain.Width != 120 || program.Domain.Height != 30 {
		t.Fatalf("final domain = %+v", program.Domain)
	}
	if program.Domain.Query != "status:blocked" || program.Domain.QueryError != "" {
		t.Fatalf("query = %q error=%q", program.Domain.Query, program.Domain.QueryError)
	}
	output, err := io.ReadAll(testModel.FinalOutput(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output, []byte("agent-team")) || !bytes.Contains(output, []byte("status:blocked")) {
		t.Fatalf("teatest output missing shell/query: %q", output)
	}
}

func TestTeatestParityNavigationFilterInspectAndResize(t *testing.T) {
	domain := smallFixtureModel(Capabilities{Dumb: true})
	domain.Polling = false
	var plain bytes.Buffer
	program := NewTestProgramModel(domain)
	program.plainOutput = &plain
	testModel := teatest.NewTestModel(t, program, teatest.WithInitialTermSize(80, 24))

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'g'}}, {Type: tea.KeyRunes, Runes: []rune{'w'}},
		{Type: tea.KeyTab}, {Type: tea.KeyDown}, {Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'/'}},
	} {
		testModel.Send(key)
	}
	testModel.Type("status:blocked")
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool {
		return bytes.Contains(output, []byte("WORK / JOBS")) && bytes.Contains(output, []byte("release-2026-07")) && bytes.Contains(output, []byte("status:blocked"))
	}, teatest.WithDuration(2*time.Second))

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{']'}},
		{Type: tea.KeyRunes, Runes: []rune{'g'}}, {Type: tea.KeyRunes, Runes: []rune{'f'}},
		{Type: tea.KeyRunes, Runes: []rune{']'}}, {Type: tea.KeyRunes, Runes: []rune{']'}},
	} {
		testModel.Send(key)
	}
	testModel.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	testModel.Send(tea.WindowSizeMsg{Width: 160, Height: 50})
	testModel.Send(tea.KeyMsg{Type: tea.KeyTab})
	testModel.Send(tea.KeyMsg{Type: tea.KeyTab})
	testModel.Send(tea.KeyMsg{Type: tea.KeyDown})
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})

	final := testModel.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(ProgramModel)
	if final.Domain.Screen != ScreenFleetTopology || final.Domain.Width != 160 || final.Domain.Height != 50 || final.Domain.Focus.ItemID == "" || !final.Domain.Inspecting {
		t.Fatalf("final parity navigation domain = %+v", final.Domain)
	}
	for _, text := range []string{"WORK / TELEMETRY", "FLEET / LIVE ORG", "FLEET / INSTANCES", "FLEET / TOPOLOGY", "Inspecting"} {
		if !strings.Contains(plain.String(), text) {
			t.Errorf("PTY plain capture missing %q", text)
		}
	}
}

func TestTeatestTermDumbKeyboardCaptureHasNoControlBytes(t *testing.T) {
	domain := smallFixtureModel(Capabilities{Dumb: true})
	domain.Polling = false
	var plain bytes.Buffer
	program := NewTestProgramModel(domain)
	program.plainOutput = &plain
	testModel := teatest.NewTestModel(t, program, teatest.WithInitialTermSize(80, 24))
	sequence := []tea.KeyMsg{
		{Type: tea.KeyTab},
		{Type: tea.KeyDown},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'/'}},
	}
	for _, key := range sequence {
		testModel.Send(key)
	}
	testModel.Type("status:active")
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'r'}},
		{Type: tea.KeyRunes, Runes: []rune{'?'}},
		{Type: tea.KeyRunes, Runes: []rune{'?'}},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	} {
		testModel.Send(key)
	}
	testModel.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	bytes := plain.Bytes()
	for index, value := range bytes {
		if value == 0x1b || value == 0x9b || value == 0x9d {
			t.Fatalf("plain TERM=dumb capture has control byte %#x at %d", value, index)
		}
	}
	for _, text := range []string{"agent-team", "status:active", "Help"} {
		if !strings.Contains(plain.String(), text) {
			t.Errorf("plain capture missing %q", text)
		}
	}
	lines := strings.Split(strings.TrimSuffix(plain.String(), "\n"), "\n")
	if len(lines)%24 != 0 {
		t.Fatalf("plain capture contains an interleaved/partial frame: %d lines is not a multiple of 24", len(lines))
	}
}

func TestPTYBindingRegistrySweepChangesIntendedState(t *testing.T) {
	for _, binding := range Bindings() {
		binding := binding
		for _, key := range binding.Keys {
			key := key
			t.Run(binding.ID+"/"+strings.ReplaceAll(key, " ", "+"), func(t *testing.T) {
				domain := bindingTestModel(binding.ID, key)
				domain.Capabilities.Dumb = true
				before := domain
				messages := teaMessages(key)
				acknowledged := make(chan struct{}, len(messages))
				programModel := bindingAckModel{ProgramModel: NewTestProgramModel(domain), acknowledged: acknowledged}
				testModel := teatest.NewTestModel(t, programModel, teatest.WithInitialTermSize(80, 24))
				for _, message := range messages {
					testModel.Send(message)
				}
				for range messages {
					select {
					case <-acknowledged:
					case <-time.After(3 * time.Second):
						t.Fatal("timed out waiting for exact key transition acknowledgement")
					}
				}
				if binding.ID == "quit" || binding.ID == "cancel" {
					testModel.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
				} else if err := testModel.Quit(); err != nil {
					t.Fatal(err)
				}
				program, ok := waitFinalProgramModel(t, testModel, 3*time.Second)
				if !ok {
					t.Fatal("final model unavailable or not tui.ProgramModel")
				}
				assertBindingEffect(t, binding.ID, key, before, program.Domain, nil, false)
			})
		}
	}
}

type bindingAckModel struct {
	ProgramModel
	acknowledged chan struct{}
}

func (model bindingAckModel) Init() tea.Cmd { return model.ProgramModel.Init() }

func (model bindingAckModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	updated, command := model.ProgramModel.Update(message)
	program, ok := updated.(ProgramModel)
	if !ok {
		return updated, command
	}
	model.ProgramModel = program
	if _, ok := message.(tea.KeyMsg); ok {
		model.acknowledged <- struct{}{}
	}
	return model, command
}

func (model bindingAckModel) View() string { return model.ProgramModel.View() }

func waitFinalProgramModel(t *testing.T, testModel *teatest.TestModel, timeout time.Duration) (ProgramModel, bool) {
	t.Helper()
	final := testModel.FinalModel(t, teatest.WithFinalTimeout(timeout))
	deadline := time.Now().Add(timeout)
	for final == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		final = testModel.FinalModel(t)
	}
	switch model := final.(type) {
	case ProgramModel:
		return model, true
	case bindingAckModel:
		return model.ProgramModel, true
	default:
		return ProgramModel{}, false
	}
}

func TestProgramManualRefreshDoesNotMultiplyPollingSchedules(t *testing.T) {
	domain := smallFixtureModel(Capabilities{})
	domain.RefreshInFlight = true
	program := NewTestProgramModel(domain)
	type scheduledTick struct {
		delay time.Duration
	}
	var schedules []scheduledTick
	program.tick = func(delay time.Duration, _ func(time.Time) tea.Msg) tea.Cmd {
		schedules = append(schedules, scheduledTick{delay: delay})
		return func() tea.Msg { return nil }
	}

	updated, _ := program.Update(refreshBatch{messages: []Msg{RefreshFinished{At: fixtureTime, AnySuccess: true, Complete: true}}})
	program = updated.(ProgramModel)
	if len(schedules) != 1 || schedules[0].delay != 5*time.Second {
		t.Fatalf("initial schedules = %+v", schedules)
	}
	firstGeneration := program.Domain.PollGeneration
	for index := range 20 {
		updated, _ = program.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		program = updated.(ProgramModel)
		updated, _ = program.Update(refreshBatch{messages: []Msg{RefreshFinished{At: fixtureTime.Add(time.Duration(index+1) * time.Millisecond), AnySuccess: true, Complete: true}}})
		program = updated.(ProgramModel)
	}
	if len(schedules) != 1 || program.Domain.PollGeneration != firstGeneration || !program.Domain.PollScheduled {
		t.Fatalf("manual refreshes multiplied schedules: count=%d domain=%+v", len(schedules), program.Domain)
	}

	updated, _ = program.Update(Tick{At: fixtureTime.Add(5 * time.Second), Generation: firstGeneration})
	program = updated.(ProgramModel)
	updated, _ = program.Update(refreshBatch{messages: []Msg{RefreshFinished{At: fixtureTime.Add(5 * time.Second), AnySuccess: true, Complete: true}}})
	program = updated.(ProgramModel)
	if len(schedules) != 2 || !program.Domain.PollScheduled || program.Domain.PollGeneration == firstGeneration {
		t.Fatalf("next cadence schedule = count %d domain=%+v", len(schedules), program.Domain)
	}
	currentGeneration := program.Domain.PollGeneration
	updated, _ = program.Update(Tick{At: fixtureTime.Add(6 * time.Second), Generation: firstGeneration})
	program = updated.(ProgramModel)
	if len(schedules) != 2 || program.Domain.PollGeneration != currentGeneration || !program.Domain.PollScheduled {
		t.Fatalf("stale program tick changed scheduler: count=%d domain=%+v", len(schedules), program.Domain)
	}
}

func teaMessages(keyName string) []tea.Msg {
	if strings.Contains(keyName, " ") {
		parts := strings.Fields(keyName)
		messages := make([]tea.Msg, 0, len(parts))
		for _, part := range parts {
			messages = append(messages, teaMessages(part)...)
		}
		return messages
	}
	types := map[string]tea.KeyType{
		"tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab, "up": tea.KeyUp, "down": tea.KeyDown,
		"left": tea.KeyLeft, "right": tea.KeyRight, "enter": tea.KeyEnter, "space": tea.KeySpace,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
		"esc": tea.KeyEsc, "ctrl+c": tea.KeyCtrlC,
	}
	if keyType, ok := types[keyName]; ok {
		return []tea.Msg{tea.KeyMsg{Type: keyType}}
	}
	return []tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keyName)}}
}
