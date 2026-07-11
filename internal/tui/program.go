package tui

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type RunOptions struct {
	TeamDir      string
	Build        buildinfo.Info
	Capabilities Capabilities
	Input        io.Reader
	Output       io.Writer
	Clock        func() time.Time
}

type ProgramModel struct {
	Domain      Model
	runtime     *commandRuntime
	query       textinput.Model
	initial     []Command
	plainOutput io.Writer
	tick        func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
}

func newProgramModel(domain Model, runtime *commandRuntime) ProgramModel {
	query := textinput.New()
	query.Prompt = ""
	query.CharLimit = 160
	query.Width = 60
	updated, commands := Update(domain, Boot{})
	return ProgramModel{Domain: updated, runtime: runtime, query: query, initial: commands}
}

// NewTestProgramModel exposes the Bubble Tea adapter without starting I/O.
// Tests normally provide a booted domain model and inject messages with teatest.
func NewTestProgramModel(domain Model) ProgramModel {
	query := textinput.New()
	query.Prompt = ""
	query.CharLimit = 160
	query.Width = 60
	return ProgramModel{Domain: domain, query: query}
}

func (model ProgramModel) Init() tea.Cmd {
	commands := model.commands(model.initial)
	if model.plainOutput != nil {
		commands = append(commands, model.renderPlain())
	}
	return tea.Batch(commands...)
}

func (model ProgramModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []Command
	switch value := message.(type) {
	case tea.WindowSizeMsg:
		model.Domain, commands = Update(model.Domain, Resize{Width: value.Width, Height: value.Height})
	case tea.KeyMsg:
		name := value.String()
		if value.Type == tea.KeySpace {
			name = "space"
		}
		cancel := key.NewBinding(key.WithKeys("ctrl+c"))
		if key.Matches(value, cancel) {
			name = "ctrl+c"
		}
		if model.Domain.QueryActive && name != "esc" && name != "enter" && name != "ctrl+c" {
			before := model.query.Value()
			var cmd tea.Cmd
			model.query, cmd = model.query.Update(value)
			if model.query.Value() != before {
				model.Domain, commands = Update(model.Domain, QueryChanged{Value: model.query.Value()})
			}
			return model, tea.Batch(cmd, model.batch(commands))
		}
		at := model.Domain.Now
		if model.runtime != nil {
			at = model.runtime.now()
		}
		model.Domain, commands = Update(model.Domain, Key{Name: name, At: at})
		if model.Domain.QueryActive {
			model.query.Focus()
		} else {
			model.query.Blur()
			if model.query.Value() != model.Domain.Query {
				model.query.SetValue(model.Domain.Query)
			}
		}
	case refreshBatch:
		for _, domainMessage := range value.messages {
			var next []Command
			model.Domain, next = Update(model.Domain, domainMessage)
			commands = append(commands, next...)
		}
	case Tick:
		model.Domain, commands = Update(model.Domain, value)
	case plainFrame:
		if model.plainOutput != nil {
			_, _ = fmt.Fprintln(model.plainOutput, value.frame)
		}
		return model, nil
	default:
		return model, nil
	}
	return model, model.batch(commands)
}

func (model ProgramModel) View() string {
	return Render(model.Domain)
}

func (model ProgramModel) batch(commands []Command) tea.Cmd {
	cmds := model.commands(commands)
	if model.plainOutput != nil {
		cmds = append(cmds, model.renderPlain())
	}
	return tea.Batch(cmds...)
}

func (model ProgramModel) commands(commands []Command) []tea.Cmd {
	out := make([]tea.Cmd, 0, len(commands))
	for _, command := range commands {
		switch command.Kind {
		case CommandBootstrap:
			if model.runtime != nil {
				out = append(out, func() tea.Msg { return model.runtime.load(true) })
			}
		case CommandRefresh:
			if model.runtime != nil {
				out = append(out, func() tea.Msg { return model.runtime.load(false) })
			}
		case CommandTick:
			delay := command.After
			if delay <= 0 {
				delay = 5 * time.Second
			}
			generation := command.Generation
			out = append(out, model.tickCommand(delay, func(at time.Time) tea.Msg { return Tick{At: at.UTC(), Generation: generation} }))
		case CommandQuit:
			out = append(out, tea.Quit)
		}
	}
	return out
}

func (model ProgramModel) tickCommand(delay time.Duration, message func(time.Time) tea.Msg) tea.Cmd {
	if model.tick != nil {
		return model.tick(delay, message)
	}
	return tea.Tick(delay, message)
}

type plainFrame struct{ frame string }

func (model ProgramModel) renderPlain() tea.Cmd {
	frame := Render(model.Domain)
	return func() tea.Msg {
		return plainFrame{frame: frame}
	}
}

func Run(ctx context.Context, options RunOptions) error {
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	runtime := &commandRuntime{ctx: ctx, teamDir: options.TeamDir, build: options.Build, clock: options.Clock}
	domain := NewModel(options.Clock(), options.Capabilities)
	domain.ReconnectJitter = true
	model := newProgramModel(domain, runtime)
	programOptions := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithInput(options.Input),
		tea.WithOutput(options.Output),
	}
	if options.Capabilities.Dumb {
		model.plainOutput = options.Output
		programOptions = append(programOptions, tea.WithoutRenderer())
	} else {
		programOptions = append(programOptions, tea.WithAltScreen())
	}
	_, err := tea.NewProgram(model, programOptions...).Run()
	return err
}

// RunOnce performs one read-only load and returns the stable plain Overview
// frame plus the process exit code defined by the TUI contract.
func RunOnce(ctx context.Context, options RunOptions) (string, int) {
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	options.Capabilities = Capabilities{Color: false, Dumb: true}
	model := NewModel(options.Clock(), options.Capabilities)
	model.Booted = true
	model.RefreshInFlight = true
	model, _ = Update(model, Resize{Width: 120, Height: 30})
	runtime := &commandRuntime{ctx: ctx, teamDir: options.TeamDir, build: options.Build, clock: options.Clock}
	batch := runtime.load(true)
	for _, message := range batch.messages {
		model, _ = Update(model, message)
	}
	code := 0
	if !model.HasSnapshot() {
		code = 1
	}
	return Render(model) + "\n", code
}
