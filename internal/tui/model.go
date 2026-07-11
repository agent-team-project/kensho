// Package tui implements the read-only terminal frontend for agent-teamd.
package tui

import (
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

type SizeClass string

const (
	SizeTooSmall SizeClass = "too-small"
	SizeCompact  SizeClass = "compact"
	SizeStandard SizeClass = "standard"
	SizeWide     SizeClass = "wide"
)

func ClassifySize(width, height int) SizeClass {
	if width < 60 || height < 16 {
		return SizeTooSmall
	}
	if width < 100 || height < 27 {
		return SizeCompact
	}
	if width < 145 || height < 40 {
		return SizeStandard
	}
	return SizeWide
}

type ConnectionState string

const (
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionPartial      ConnectionState = "partial"
	ConnectionStale        ConnectionState = "stale"
	ConnectionDisconnected ConnectionState = "disconnected"
	ConnectionReconnecting ConnectionState = "reconnecting"
	ConnectionReconnected  ConnectionState = "reconnected"
)

type Route string

const (
	RouteOverview     Route = "overview"
	RouteWork         Route = "work"
	RouteFleet        Route = "fleet"
	RouteActivity     Route = "activity"
	RouteLogs         Route = "logs"
	RouteResearch     Route = "research"
	RouteRequirements Route = "requirements"
	RouteRelease      Route = "release"
)

var routeOrder = []Route{RouteOverview, RouteWork, RouteFleet, RouteActivity, RouteLogs, RouteResearch, RouteRequirements, RouteRelease}

type Focus struct {
	Region  string
	ItemID  string
	Control string
}

type Overlay string

const (
	OverlayHelp    Overlay = "help"
	OverlayPalette Overlay = "palette"
)

type OverlayInvoker struct {
	Focus      Focus
	FocusIndex int
}

type Capabilities struct {
	Color bool
	Dumb  bool
}

type SourceState struct {
	FetchedAt time.Time
	Error     string
}

// Model is the single framework-free state owner. All time, terminal, and I/O
// facts arrive through messages.
type Model struct {
	Width             int
	Height            int
	Size              SizeClass
	Capabilities      Capabilities
	Route             Route
	Focus             Focus
	FocusIndex        int
	Connection        ConnectionState
	Snapshot          *daemonclient.Snapshot
	Sources           map[daemonclient.SnapshotSource]SourceState
	Now               time.Time
	LastGoodAt        time.Time
	RefreshInFlight   bool
	Polling           bool
	PollScheduled     bool
	PollGeneration    uint64
	PollDelay         time.Duration
	ReconnectAttempts int
	ReconnectJitter   bool
	Query             string
	QueryActive       bool
	QueryError        string
	Overlays          []Overlay
	OverlayInvokers   []OverlayInvoker
	HelpPage          int
	PaletteQuery      string
	PaletteIndex      int
	Feedback          string
	PendingGo         bool
	GoDeadline        time.Time
	Quit              bool
	Booted            bool
}

func NewModel(now time.Time, capabilities Capabilities) Model {
	if now.IsZero() {
		now = time.Unix(0, 0).UTC()
	}
	return Model{
		Width:        80,
		Height:       24,
		Size:         SizeCompact,
		Capabilities: capabilities,
		Route:        RouteOverview,
		Focus:        focusRing[0],
		Connection:   ConnectionConnecting,
		Sources:      map[daemonclient.SnapshotSource]SourceState{},
		Now:          now.UTC(),
		Polling:      true,
	}
}

var focusRing = []Focus{
	{Region: "summary", Control: "fleet"},
	{Region: "summary", Control: "work"},
	{Region: "attention", Control: "list"},
	{Region: "org", Control: "list"},
	{Region: "status", Control: "refresh"},
}

func (m Model) HasOverlay(overlay Overlay) bool {
	for _, current := range m.Overlays {
		if current == overlay {
			return true
		}
	}
	return false
}

func (m Model) HasSnapshot() bool {
	return m.Snapshot != nil && m.Snapshot.Usable()
}

func (m Model) FocusLabel() string {
	if m.QueryActive {
		return "query"
	}
	if len(m.Overlays) > 0 {
		return string(m.Overlays[len(m.Overlays)-1])
	}
	return m.Focus.Region + "/" + m.Focus.Control
}
