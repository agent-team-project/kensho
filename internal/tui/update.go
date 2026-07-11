package tui

import (
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
)

type Msg interface{ isMsg() }

type Boot struct{}
type Resize struct{ Width, Height int }
type Tick struct {
	At         time.Time
	Generation uint64
}
type ReconnectTick struct {
	At         time.Time
	Generation uint64
}
type RefreshStarted struct{ At time.Time }
type RefreshFinished struct {
	At         time.Time
	AnySuccess bool
	Complete   bool
	CacheUsed  bool
	Error      string
}
type CachedSnapshot struct{ Snapshot *daemonclient.Snapshot }
type SnapshotOK struct {
	Source   daemonclient.SnapshotSource
	Snapshot *daemonclient.Snapshot
	At       time.Time
}
type SnapshotError struct {
	Source daemonclient.SnapshotSource
	Error  string
	At     time.Time
}
type Key struct {
	Name string
	At   time.Time
}
type QueryChanged struct{ Value string }
type QueryCommit struct{}
type OpenOverlay struct{ Overlay Overlay }
type CloseOverlay struct{}
type AttachRequested struct{}
type AttachStarted struct{}
type AttachReturned struct{}
type AttachFailed struct{ Error string }
type QuitRequested struct{}

func (Boot) isMsg()            {}
func (Resize) isMsg()          {}
func (Tick) isMsg()            {}
func (ReconnectTick) isMsg()   {}
func (RefreshStarted) isMsg()  {}
func (RefreshFinished) isMsg() {}
func (CachedSnapshot) isMsg()  {}
func (SnapshotOK) isMsg()      {}
func (SnapshotError) isMsg()   {}
func (Key) isMsg()             {}
func (QueryChanged) isMsg()    {}
func (QueryCommit) isMsg()     {}
func (OpenOverlay) isMsg()     {}
func (CloseOverlay) isMsg()    {}
func (AttachRequested) isMsg() {}
func (AttachStarted) isMsg()   {}
func (AttachReturned) isMsg()  {}
func (AttachFailed) isMsg()    {}
func (QuitRequested) isMsg()   {}

type CommandKind string

const (
	CommandBootstrap CommandKind = "bootstrap"
	CommandRefresh   CommandKind = "refresh"
	CommandTick      CommandKind = "tick"
	CommandAttach    CommandKind = "attach"
	CommandQuit      CommandKind = "quit"
)

type Command struct {
	Kind       CommandKind
	After      time.Duration
	Generation uint64
}

// Update is the framework-free transition function. It performs no I/O and
// reads neither the wall clock nor terminal state.
func Update(model Model, msg Msg) (Model, []Command) {
	switch value := msg.(type) {
	case Boot:
		if model.Booted {
			return model, nil
		}
		model.Booted = true
		model.RefreshInFlight = true
		model.Connection = ConnectionConnecting
		return model, []Command{{Kind: CommandBootstrap}}
	case Resize:
		model.Width = max(0, value.Width)
		model.Height = max(0, value.Height)
		model.Size = ClassifySize(model.Width, model.Height)
		model.HelpPage = clampHelpPage(model, model.HelpPage)
		model = preserveFocus(model)
		return model, nil
	case CachedSnapshot:
		if value.Snapshot == nil || !value.Snapshot.Usable() {
			return model, nil
		}
		model.Snapshot = cloneSnapshot(value.Snapshot)
		for source, at := range value.Snapshot.SourceTimes {
			model.Sources[source] = SourceState{FetchedAt: at}
		}
		model.LastGoodAt = value.Snapshot.CapturedAt
		model.Connection = ConnectionStale
		model.Feedback = "Loaded last-good snapshot"
		return preserveFocus(model), nil
	case RefreshStarted:
		model.Now = normalizedTime(value.At, model.Now)
		if model.RefreshInFlight {
			return model, nil
		}
		model.RefreshInFlight = true
		if model.Connection == ConnectionDisconnected || model.Connection == ConnectionStale {
			model.Connection = ConnectionReconnecting
		}
		model.Feedback = "Refreshing..."
		return model, nil
	case SnapshotOK:
		model.Now = normalizedTime(value.At, model.Now)
		model = mergeSnapshotSource(model, value.Source, value.Snapshot, value.At)
		return preserveFocus(model), nil
	case SnapshotError:
		model.Now = normalizedTime(value.At, model.Now)
		state := model.Sources[value.Source]
		state.Error = strings.TrimSpace(value.Error)
		model.Sources[value.Source] = state
		if model.Snapshot != nil {
			copy := cloneSnapshot(model.Snapshot)
			copy.SourceErrors[value.Source] = state.Error
			model.Snapshot = copy
		}
		return model, nil
	case RefreshFinished:
		model.Now = normalizedTime(value.At, model.Now)
		model.RefreshInFlight = false
		previous := model.Connection
		switch {
		case value.Complete:
			if previous == ConnectionDisconnected || previous == ConnectionReconnecting || previous == ConnectionStale {
				model.Connection = ConnectionReconnected
				model.Feedback = "Reconnected"
			} else {
				model.Connection = ConnectionConnected
				model.Feedback = "Snapshot refreshed"
			}
			model.LastGoodAt = model.Now
			model.ReconnectAttempts = 0
		case value.AnySuccess:
			model.Connection = ConnectionPartial
			model.Feedback = "Partial refresh; last-good sections retained"
			model.LastGoodAt = latestSourceTime(model.Sources)
		case value.CacheUsed && model.HasSnapshot():
			model.Connection = ConnectionStale
			model.Feedback = "Daemon unavailable; showing last-good snapshot"
			model.ReconnectAttempts++
		default:
			model.Connection = ConnectionDisconnected
			model.Feedback = noDaemonFeedback(value.Error)
			model.ReconnectAttempts++
		}
		return reconcilePoll(model)
	case Tick:
		if !model.PollScheduled || value.Generation != model.PollGeneration {
			return model, nil
		}
		model.PollScheduled = false
		model.PollDelay = 0
		model.Now = normalizedTime(value.At, model.Now)
		if model.Connection == ConnectionReconnected {
			model.Connection = ConnectionConnected
		}
		if !model.Polling {
			return model, nil
		}
		if model.RefreshInFlight {
			return schedulePoll(model)
		}
		model.RefreshInFlight = true
		if model.Connection == ConnectionDisconnected || model.Connection == ConnectionStale {
			model.Connection = ConnectionReconnecting
		}
		model, pollCommands := schedulePoll(model)
		return model, append([]Command{{Kind: CommandRefresh}}, pollCommands...)
	case ReconnectTick:
		if !model.PollScheduled || value.Generation != model.PollGeneration {
			return model, nil
		}
		model.PollScheduled = false
		model.PollDelay = 0
		model.Now = normalizedTime(value.At, model.Now)
		if model.RefreshInFlight {
			return schedulePoll(model)
		}
		if !model.Polling {
			return model, nil
		}
		model.RefreshInFlight = true
		model.Connection = ConnectionReconnecting
		model, pollCommands := schedulePoll(model)
		return model, append([]Command{{Kind: CommandRefresh}}, pollCommands...)
	case QueryChanged:
		model.Query = value.Value
		model.QueryError = validateOverviewQuery(value.Value)
		model = preserveFocus(model)
		return model, nil
	case QueryCommit:
		model.QueryActive = false
		model.QueryError = validateOverviewQuery(model.Query)
		if model.QueryError == "" {
			model.Feedback = "Filter applied"
		}
		return model, nil
	case OpenOverlay:
		model = openOverlay(model, value.Overlay)
		return model, nil
	case CloseOverlay:
		model = closeOverlay(model)
		return model, nil
	case AttachRequested:
		model.Polling = false
		invalidatePoll(&model)
		model.Feedback = "Preparing terminal handoff"
		return model, []Command{{Kind: CommandAttach}}
	case AttachStarted:
		model.Polling = false
		invalidatePoll(&model)
		model.Feedback = "Attached process owns terminal"
		return model, nil
	case AttachReturned:
		model.Polling = true
		model.Feedback = "Attach returned"
		return model, []Command{{Kind: CommandRefresh}}
	case AttachFailed:
		model.Polling = true
		model.Feedback = "Attach failed: " + strings.TrimSpace(value.Error)
		return model, []Command{{Kind: CommandRefresh}}
	case QuitRequested:
		return requestQuit(model)
	case Key:
		return updateKey(model, value)
	default:
		return model, nil
	}
}

func updateKey(model Model, key Key) (Model, []Command) {
	name := strings.ToLower(strings.TrimSpace(key.Name))
	model.Now = normalizedTime(key.At, model.Now)
	if len(model.Overlays) > 0 {
		return updateOverlayKey(model, name)
	}
	if model.PendingGo {
		model.PendingGo = false
		if !model.GoDeadline.IsZero() && model.Now.After(model.GoDeadline) {
			model.Feedback = "Screen chord timed out"
			return model, nil
		}
		if route, ok := goRoute(name); ok {
			model.Route = route
			model.FocusIndex = 0
			model.Focus = focusRing[0]
			if route != RouteOverview {
				model.Feedback = routeTitle(route) + " arrives in a later read-only slice"
			}
			return model, nil
		}
		model.Feedback = "Unknown screen chord"
		return model, nil
	}
	if model.QueryActive {
		switch name {
		case "esc":
			if model.Query != "" {
				model.Query = ""
				model.QueryError = ""
			} else {
				model.QueryActive = false
			}
		case "enter":
			return Update(model, QueryCommit{})
		case "ctrl+c":
			model.QueryActive = false
		}
		return model, nil
	}
	switch name {
	case "ctrl+c":
		return requestQuit(model)
	case "q":
		return requestQuit(model)
	case "?":
		if model.HasOverlay(OverlayHelp) {
			return Update(model, CloseOverlay{})
		}
		return Update(model, OpenOverlay{Overlay: OverlayHelp})
	case "ctrl+k":
		if model.HasOverlay(OverlayPalette) {
			return Update(model, CloseOverlay{})
		}
		return Update(model, OpenOverlay{Overlay: OverlayPalette})
	case "/":
		model.QueryActive = true
		model.Feedback = "Type to filter Overview"
	case "esc":
		if model.Query != "" {
			model.Query = ""
			model.QueryError = ""
		} else {
			model.Feedback = "Already at Overview"
		}
	case "tab":
		moveFocus(&model, 1)
	case "shift+tab":
		moveFocus(&model, -1)
	case "up", "k":
		moveFocusedItem(&model, -1)
	case "down", "j":
		moveFocusedItem(&model, 1)
	case "left", "h":
		moveFocus(&model, -1)
	case "right", "l":
		moveFocus(&model, 1)
	case "enter":
		model.Feedback = inspectFeedback(model)
	case "space":
		model.Feedback = "No selection toggle on read-only Overview"
	case "pgup", "home":
		moveFocusedItem(&model, -1000)
	case "pgdown", "end":
		moveFocusedItem(&model, 1000)
	case "[", "]":
		model.Feedback = "Overview has no local subsections"
	case "r":
		if model.RefreshInFlight {
			model.Feedback = "Refresh already in flight"
			return model, nil
		}
		model.RefreshInFlight = true
		if model.Connection == ConnectionDisconnected || model.Connection == ConnectionStale {
			model.Connection = ConnectionReconnecting
		}
		model.Feedback = "Refreshing..."
		return model, []Command{{Kind: CommandRefresh}}
	case "p":
		model.Polling = !model.Polling
		if model.Polling {
			model.Feedback = "Auto-refresh resumed"
			return schedulePoll(model)
		} else {
			invalidatePoll(&model)
			model.Feedback = "Auto-refresh paused"
		}
	case "g":
		model.PendingGo = true
		model.GoDeadline = model.Now.Add(time.Second)
		model.Feedback = "Go to: o overview, w work, f fleet, a activity, l logs, s research, r requirements, e release"
	}
	return model, nil
}

func schedulePoll(model Model) (Model, []Command) {
	if !model.Polling || model.PollScheduled {
		return model, nil
	}
	model.PollGeneration++
	model.PollScheduled = true
	model.PollDelay = nextPollDelay(model)
	return model, []Command{{Kind: CommandTick, After: model.PollDelay, Generation: model.PollGeneration}}
}

func reconcilePoll(model Model) (Model, []Command) {
	if !model.Polling {
		return model, nil
	}
	desired := nextPollDelay(model)
	if model.PollScheduled && model.PollDelay == desired {
		return model, nil
	}
	if model.PollScheduled {
		invalidatePoll(&model)
	}
	return schedulePoll(model)
}

func invalidatePoll(model *Model) {
	model.PollGeneration++
	model.PollScheduled = false
	model.PollDelay = 0
}

func requestQuit(model Model) (Model, []Command) {
	if len(model.Overlays) > 0 {
		model = closeOverlay(model)
		return model, nil
	}
	if model.QueryActive {
		model.QueryActive = false
		return model, nil
	}
	model.Quit = true
	return model, []Command{{Kind: CommandQuit}}
}

func openOverlay(model Model, overlay Overlay) Model {
	if model.HasOverlay(overlay) {
		return model
	}
	model.Overlays = append(model.Overlays, overlay)
	model.OverlayInvokers = append(model.OverlayInvokers, OverlayInvoker{Focus: model.Focus, FocusIndex: model.FocusIndex})
	switch overlay {
	case OverlayHelp:
		model.HelpPage = 0
	case OverlayPalette:
		model.PaletteQuery = ""
		model.PaletteIndex = 0
	}
	return model
}

func closeOverlay(model Model) Model {
	if len(model.Overlays) == 0 {
		return model
	}
	model.Overlays = model.Overlays[:len(model.Overlays)-1]
	if len(model.OverlayInvokers) > 0 {
		invoker := model.OverlayInvokers[len(model.OverlayInvokers)-1]
		model.OverlayInvokers = model.OverlayInvokers[:len(model.OverlayInvokers)-1]
		model.FocusIndex = invoker.FocusIndex
		model.Focus = invoker.Focus
		model = preserveFocus(model)
	}
	return model
}

func updateOverlayKey(model Model, name string) (Model, []Command) {
	switch model.Overlays[len(model.Overlays)-1] {
	case OverlayHelp:
		switch name {
		case "?", "esc", "ctrl+c":
			model = closeOverlay(model)
		case "tab", "down", "j", "right", "l", "pgdown", "space":
			model.HelpPage = clampHelpPage(model, model.HelpPage+1)
		case "shift+tab", "up", "k", "left", "h", "pgup":
			model.HelpPage = clampHelpPage(model, model.HelpPage-1)
		case "home":
			model.HelpPage = 0
		case "end":
			model.HelpPage = helpPageCount(model) - 1
		default:
			model.Feedback = "Help owns input; use PgUp/PgDn, ? or Esc"
		}
		return model, nil
	case OverlayPalette:
		items := filteredPaletteItems(model.PaletteQuery)
		switch name {
		case "ctrl+k", "esc", "ctrl+c":
			return closeOverlay(model), nil
		case "up", "k":
			if len(items) > 0 {
				model.PaletteIndex = (model.PaletteIndex - 1 + len(items)) % len(items)
			}
		case "down", "j", "tab":
			if len(items) > 0 {
				model.PaletteIndex = (model.PaletteIndex + 1) % len(items)
			}
		case "home":
			model.PaletteIndex = 0
		case "end":
			model.PaletteIndex = max(0, len(items)-1)
		case "backspace", "ctrl+h":
			model.PaletteQuery = trimLastRune(model.PaletteQuery)
			model.PaletteIndex = 0
		case "enter":
			if len(items) == 0 {
				model.Feedback = "No command matches palette search"
				return model, nil
			}
			selected := items[min(model.PaletteIndex, len(items)-1)]
			model = closeOverlay(model)
			if selected.Route != "" {
				model.Route = selected.Route
				model.FocusIndex = 0
				model = preserveFocus(model)
				if selected.Route != RouteOverview {
					model.Feedback = routeTitle(selected.Route) + " arrives in a later read-only slice"
				}
				return model, nil
			}
			var commands []Command
			for _, keyName := range strings.Fields(selected.Key) {
				var next []Command
				model, next = updateKey(model, Key{Name: keyName, At: model.Now})
				commands = append(commands, next...)
			}
			return model, commands
		default:
			if name == "space" {
				model.PaletteQuery += " "
				model.PaletteIndex = 0
			} else if len([]rune(name)) == 1 {
				model.PaletteQuery += name
				model.PaletteIndex = 0
			} else {
				model.Feedback = "Command palette owns input"
			}
		}
		return model, nil
	default:
		return model, nil
	}
}

type paletteItem struct {
	Label string
	Key   string
	Route Route
}

func paletteItems() []paletteItem {
	items := make([]paletteItem, 0, len(routeOrder)+len(Bindings()))
	for _, route := range routeOrder {
		items = append(items, paletteItem{Label: routeTitle(route) + " route", Route: route})
	}
	for _, binding := range Bindings() {
		key := ""
		if len(binding.Keys) > 0 {
			key = binding.Keys[0]
		}
		items = append(items, paletteItem{Label: binding.Label + " — " + binding.Description, Key: key})
	}
	return items
}

func filteredPaletteItems(query string) []paletteItem {
	query = strings.ToLower(strings.TrimSpace(query))
	items := paletteItems()
	if query == "" {
		return items
	}
	filtered := items[:0]
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Label), query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func helpPageSize(model Model) int {
	height := min(model.Height-8, 19)
	return max(1, height-4)
}

func helpPageCount(model Model) int {
	return max(1, (len(Bindings())+helpPageSize(model)-1)/helpPageSize(model))
}

func clampHelpPage(model Model, page int) int {
	return max(0, min(helpPageCount(model)-1, page))
}

func mergeSnapshotSource(model Model, source daemonclient.SnapshotSource, snapshot *daemonclient.Snapshot, at time.Time) Model {
	if snapshot == nil {
		return model
	}
	if model.Snapshot == nil {
		model.Snapshot = &daemonclient.Snapshot{
			Schema:       daemonclient.SnapshotSchema,
			Resources:    map[string]*daemonclient.Resource{},
			SourceTimes:  map[daemonclient.SnapshotSource]time.Time{},
			SourceErrors: map[daemonclient.SnapshotSource]string{},
		}
	}
	copy := cloneSnapshot(model.Snapshot)
	copy.CapturedAt = snapshot.CapturedAt
	copy.Connection = snapshot.Connection
	copy.TeamDir = snapshot.TeamDir
	copy.DeploymentID = snapshot.DeploymentID
	switch source {
	case daemonclient.SourceInstances:
		copy.Instances = append([]*daemonclient.Instance(nil), snapshot.Instances...)
	case daemonclient.SourceJobs:
		copy.Jobs = append([]*daemonclient.Job(nil), snapshot.Jobs...)
	case daemonclient.SourceTopology:
		copy.Topology = snapshot.Topology
	case daemonclient.SourceResources:
		currentURIs := daemonclient.SnapshotResourceURIs(copy.Instances, copy.Jobs)
		resources := make(map[string]*daemonclient.Resource, len(currentURIs))
		for _, uri := range currentURIs {
			if resource, ok := snapshot.Resources[uri]; ok {
				resources[uri] = resource
			} else if resource, ok := copy.Resources[uri]; ok {
				resources[uri] = resource
			}
		}
		copy.Resources = resources
		copy.ResourcesRequested = len(currentURIs)
	}
	if at.IsZero() {
		at = snapshot.SourceTimes[source]
	}
	if at.IsZero() {
		at = copy.SourceTimes[source]
	} else {
		copy.SourceTimes[source] = at
	}
	delete(copy.SourceErrors, source)
	model.Snapshot = copy
	model.Sources[source] = SourceState{FetchedAt: at}
	return model
}

func cloneSnapshot(snapshot *daemonclient.Snapshot) *daemonclient.Snapshot {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	copy.Instances = append([]*daemonclient.Instance(nil), snapshot.Instances...)
	copy.Jobs = append([]*daemonclient.Job(nil), snapshot.Jobs...)
	copy.Resources = make(map[string]*daemonclient.Resource, len(snapshot.Resources))
	for uri, resource := range snapshot.Resources {
		copy.Resources[uri] = resource
	}
	copy.SourceTimes = make(map[daemonclient.SnapshotSource]time.Time, len(snapshot.SourceTimes))
	for source, at := range snapshot.SourceTimes {
		copy.SourceTimes[source] = at
	}
	copy.SourceErrors = make(map[daemonclient.SnapshotSource]string, len(snapshot.SourceErrors))
	for source, message := range snapshot.SourceErrors {
		copy.SourceErrors[source] = message
	}
	return &copy
}

func preserveFocus(model Model) Model {
	itemID := model.Focus.ItemID
	if model.FocusIndex < 0 || model.FocusIndex >= len(focusRing) {
		model.FocusIndex = 0
	}
	model.Focus = focusRing[model.FocusIndex]
	model.Focus.ItemID = itemID
	if model.Focus.Region == "attention" {
		rows := projectOverview(model).Attention
		if len(rows) == 0 {
			model.Focus.ItemID = ""
		} else if !containsAttention(rows, model.Focus.ItemID) {
			model.Focus.ItemID = rows[0].ID
		}
	}
	return model
}

func moveFocus(model *Model, delta int) {
	model.FocusIndex = (model.FocusIndex + delta + len(focusRing)) % len(focusRing)
	*model = preserveFocus(*model)
}

func moveFocusedItem(model *Model, delta int) {
	if model.Focus.Region != "attention" {
		model.Feedback = "Move applies within the focused list"
		return
	}
	rows := projectOverview(*model).Attention
	if len(rows) == 0 {
		model.Feedback = "No attention items"
		return
	}
	index := 0
	for i, row := range rows {
		if row.ID == model.Focus.ItemID {
			index = i
			break
		}
	}
	if delta < -len(rows) {
		index = 0
	} else if delta > len(rows) {
		index = len(rows) - 1
	} else {
		index = max(0, min(len(rows)-1, index+delta))
	}
	model.Focus.ItemID = rows[index].ID
}

func containsAttention(rows []AttentionRow, id string) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

func inspectFeedback(model Model) string {
	switch model.Focus.Region {
	case "attention":
		if model.Focus.ItemID == "" {
			return "No attention item to inspect"
		}
		return "Focused " + model.Focus.ItemID + "; full detail arrives in a later read-only slice"
	case "summary":
		return "Use g w for Work or g f for Fleet"
	case "org":
		return "Use g f for Fleet"
	default:
		return "Read-only Overview"
	}
}

func validateOverviewQuery(query string) string {
	allowed := map[string]bool{"id": true, "status": true, "type": true, "role": true, "ticket": true}
	for _, term := range strings.Fields(query) {
		if i := strings.IndexByte(term, ':'); i > 0 && !allowed[strings.ToLower(term[:i])] {
			return "unknown filter field: " + term[:i]
		}
	}
	return ""
}

func goRoute(key string) (Route, bool) {
	switch key {
	case "o":
		return RouteOverview, true
	case "w":
		return RouteWork, true
	case "f":
		return RouteFleet, true
	case "a":
		return RouteActivity, true
	case "l":
		return RouteLogs, true
	case "s":
		return RouteResearch, true
	case "r":
		return RouteRequirements, true
	case "e":
		return RouteRelease, true
	default:
		return "", false
	}
}

func routeTitle(route Route) string {
	if route == RouteOverview {
		return "Overview"
	}
	return strings.ToUpper(string(route[:1])) + string(route[1:])
}

func noDaemonFeedback(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "Daemon not running; start it with: agent-team daemon start"
	}
	return "Daemon unavailable (" + message + "); run: agent-team daemon start"
}

func normalizedTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value.UTC()
}

func latestSourceTime(states map[daemonclient.SnapshotSource]SourceState) time.Time {
	var latest time.Time
	for _, state := range states {
		if state.FetchedAt.After(latest) {
			latest = state.FetchedAt
		}
	}
	return latest
}

func nextPollDelay(model Model) time.Duration {
	if model.Connection != ConnectionDisconnected && model.Connection != ConnectionStale && model.Connection != ConnectionReconnecting {
		return 5 * time.Second
	}
	delay := time.Second << min(model.ReconnectAttempts, 5)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	if model.ReconnectJitter && model.Now.Nanosecond() != 0 {
		window := delay / 4
		if window > 0 {
			delay += time.Duration(model.Now.UnixNano() % int64(window))
		}
	}
	return delay
}
