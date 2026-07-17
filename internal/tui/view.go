package tui

import (
	"bytes"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agent-team-project/agent-team/internal/daemonclient"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Render deterministically renders one complete frame from model state.
func Render(model Model) string {
	if model.Width <= 0 || model.Height <= 0 {
		return ""
	}
	canvas := newCanvas(model.Width, model.Height, model.Capabilities.Dumb)
	canvas.box(0, 0, model.Width, model.Height)
	if model.Size == SizeTooSmall {
		renderTooSmall(canvas, model)
		if len(model.Overlays) > 0 && model.Width >= 20 && model.Height >= 9 {
			renderOverlay(canvas, model, model.Overlays[len(model.Overlays)-1])
		}
	} else {
		renderShell(canvas, model)
	}
	frame := canvas.string()
	if !model.Capabilities.Color || model.Capabilities.Dumb {
		return frame
	}
	var output bytes.Buffer
	renderer := lipgloss.NewRenderer(&output)
	renderer.SetColorProfile(termenv.ANSI)
	return renderer.NewStyle().Foreground(lipgloss.Color("6")).Render(frame)
}

func renderTooSmall(canvas *cellCanvas, model Model) {
	status := connectionLabel(model)
	canvas.put(1, 0, " agent-team | TERMINAL TOO SMALL ")
	canvas.putRight(2, 0, status+" ")
	message := fmt.Sprintf("Terminal is %dx%d; agent-team ui requires at least 60x16.", model.Width, model.Height)
	canvas.putCentered(max(2, model.Height/2-1), message)
	canvas.putCentered(max(3, model.Height/2), "Resize the terminal, press ? for Help, or q to Quit.")
	if model.Height >= 3 {
		canvas.put(2, model.Height-2, "? Help   q Quit")
	}
}

func renderShell(canvas *cellCanvas, model Model) {
	canvas.put(1, 0, " agent-team | "+strings.ToUpper(screenTitle(model.Screen))+" ")
	canvas.putRight(2, 0, connectionLabel(model)+" "+clockText(model.Now)+" ")
	canvas.put(2, 1, navigationText(model))
	canvas.putRight(2, 1, "? Help ")
	canvas.horizontal(0, 2, canvas.width)

	if !model.HasSnapshot() {
		renderEmpty(canvas, model)
	} else if model.Screen == ScreenOverview {
		switch model.Size {
		case SizeCompact:
			renderCompactOverview(canvas, model)
		case SizeStandard:
			renderStandardOverview(canvas, model)
		case SizeWide:
			renderWideOverview(canvas, model)
		}
	} else {
		renderParityScreen(canvas, model)
	}
	renderStatus(canvas, model)
	renderFooter(canvas, model)
	if len(model.Overlays) > 0 {
		renderOverlay(canvas, model, model.Overlays[len(model.Overlays)-1])
	}
}

func renderEmpty(canvas *cellCanvas, model Model) {
	canvas.put(2, 3, screenTitle(model.Screen))
	if model.Connection == ConnectionConnecting || model.Connection == ConnectionReconnecting {
		canvas.put(4, 5, "Connecting to agent-teamd through shared daemon discovery...")
	} else {
		canvas.put(4, 5, "No daemon snapshot is available.")
		canvas.put(4, 7, "Start the daemon: agent-team daemon start")
		canvas.put(4, 8, "Then press r to refresh. No token or endpoint prompt is required.")
	}
	if model.Feedback != "" {
		canvas.put(4, 10, model.Feedback)
	}
}

func renderPlaceholder(canvas *cellCanvas, model Model) {
	canvas.put(2, 5, screenTitle(model.Screen))
	canvas.put(4, 7, "This route is reserved by the global navigation grammar.")
	canvas.put(4, 8, "Its read-only screen arrives after the dashboard parity cutover.")
	canvas.put(4, 10, "Press g o to return to Overview.")
}

func renderCompactOverview(canvas *cellCanvas, model Model) {
	projection := projectOverview(model)
	summary := projection.Summary
	canvas.putClipped(2, 3, fmt.Sprintf("%s Fleet      %d instances   %d running   %d teams   %d crashed", focusMarker(model, "summary", "fleet"), summary.Instances, summary.Running, summary.Teams, countStatus(projection.Attention, "instance", "crashed")), canvas.width-4)
	canvas.putClipped(2, 4, fmt.Sprintf("%s Work       %d jobs        %d active    %d blocked %d failed", focusMarker(model, "summary", "work"), summary.Jobs, summary.ActiveJobs, summary.BlockedJobs, summary.FailedJobs), canvas.width-4)
	canvas.putClipped(2, 5, fmt.Sprintf("Telemetry  %d model tiers %d bounce classes   %d deployments", summary.ModelTiers, summary.BounceClasses, summary.Deployments), canvas.width-4)
	canvas.putClipped(2, 6, fmt.Sprintf("Capacity   %d pipelines   %d budgets   %d schedules  %d deadlines", summary.Pipelines, summary.Budgets, summary.Schedules, summary.Deadlines), canvas.width-4)
	canvas.separator(7, "Attention")
	renderAttention(canvas, model, projection.Attention, 8, min(12, canvas.height-7), 2, canvas.width-4)
	orgStart := min(13, canvas.height-6)
	canvas.separator(orgStart-1, "Live org")
	renderOrg(canvas, model, projection.Org, orgStart, canvas.height-4, 2, canvas.width-4)
}

func renderStandardOverview(canvas *cellCanvas, model Model) {
	projection := projectOverview(model)
	summary := projection.Summary
	middle := canvas.width / 2
	canvas.separator(3, "Summary")
	canvas.vertical(middle, 3, canvas.height-4)
	canvas.putClipped(2, 4, fmt.Sprintf("%s Fleet       instances %-4d running %-4d teams %-4d", focusMarker(model, "summary", "fleet"), summary.Instances, summary.Running, summary.Teams), middle-4)
	canvas.putClipped(2, 5, fmt.Sprintf("%s Work        jobs %-9d active %-5d blocked %-2d failed %-2d", focusMarker(model, "summary", "work"), summary.Jobs, summary.ActiveJobs, summary.BlockedJobs, summary.FailedJobs), middle-4)
	canvas.putClipped(2, 6, fmt.Sprintf("Telemetry   model/tier %-3d bounce classes %-3d deployments %-3d", summary.ModelTiers, summary.BounceClasses, summary.Deployments), middle-4)
	canvas.putClipped(2, 7, fmt.Sprintf("Topology    pipelines %-4d budgets %-4d schedules %-4d deadlines %-4d", summary.Pipelines, summary.Budgets, summary.Schedules, summary.Deadlines), middle-4)
	canvas.put(middle+2, 3, "Attention")
	renderAttention(canvas, model, projection.Attention, 4, 12, middle+2, canvas.width-middle-4)
	canvas.separator(13, "Live org")
	renderOrg(canvas, model, projection.Org, 14, canvas.height-4, 2, canvas.width-4)
}

func renderWideOverview(canvas *cellCanvas, model Model) {
	projection := projectOverview(model)
	summary := projection.Summary
	rail := 23
	right := 106
	canvas.vertical(rail, 2, canvas.height-4)
	canvas.vertical(right, 2, canvas.height-4)
	canvas.put(2, 3, "Overview")
	canvas.put(2, 5, focusMarker(model, "summary", model.Focus.Control)+" Summary")
	canvas.put(2, 6, focusMarker(model, "attention", "list")+" Attention")
	canvas.put(2, 7, focusMarker(model, "org", "list")+" Live org")
	canvas.put(rail+2, 3, "Fleet and work summary")
	canvas.putClipped(rail+2, 5, fmt.Sprintf("Fleet       %d instances   %d running   %d teams", summary.Instances, summary.Running, summary.Teams), right-rail-4)
	canvas.putClipped(rail+2, 6, fmt.Sprintf("Work        %d jobs        %d active    %d blocked   %d failed", summary.Jobs, summary.ActiveJobs, summary.BlockedJobs, summary.FailedJobs), right-rail-4)
	canvas.putClipped(rail+2, 7, fmt.Sprintf("Telemetry   %d model tiers %d bounce classes  %d deployments", summary.ModelTiers, summary.BounceClasses, summary.Deployments), right-rail-4)
	canvas.putClipped(rail+2, 8, fmt.Sprintf("Topology    %d pipelines   %d budgets   %d schedules   %d deadlines", summary.Pipelines, summary.Budgets, summary.Schedules, summary.Deadlines), right-rail-4)
	canvas.put(right+2, 3, "Connection")
	canvas.put(right+2, 5, connectionDetail(model))
	canvas.put(right+2, 6, fmt.Sprintf("Focus: %s", model.FocusLabel()))
	canvas.put(right+2, 7, fmt.Sprintf("Polling: %s", enabledText(model.Polling)))
	canvas.horizontal(rail, 10, canvas.width-rail)
	canvas.put(rail+2, 10, "Attention")
	renderAttention(canvas, model, projection.Attention, 11, 20, rail+2, right-rail-4)
	canvas.put(right+2, 10, "Live org")
	renderOrg(canvas, model, projection.Org, 11, canvas.height-4, right+2, canvas.width-right-4)
}

func renderAttention(canvas *cellCanvas, model Model, rows []AttentionRow, start, end, x, width int) {
	if len(rows) == 0 {
		canvas.put(x, start, "No active problems. Read-only fleet is calm.")
		return
	}
	limit := min(len(rows), max(0, end-start))
	for i := 0; i < limit; i++ {
		row := rows[i]
		marker := " "
		if model.Focus.Region == "attention" && (model.Focus.ItemID == row.ID || model.Focus.ItemID == "" && i == 0) {
			marker = ">"
		}
		line := fmt.Sprintf("%s %-8s %-24s %-14s %s", marker, row.Kind, row.ID, row.Status, row.Detail)
		canvas.putClipped(x, start+i, line, width)
	}
}

func renderOrg(canvas *cellCanvas, model Model, rows []OrgRow, start, end, x, width int) {
	if len(rows) == 0 {
		canvas.put(x, start, "No runtime instances reported.")
		return
	}
	limit := min(len(rows), max(0, end-start))
	for i := 0; i < limit; i++ {
		row := rows[i]
		capacity := "unbounded"
		if row.Capacity > 0 {
			capacity = fmt.Sprintf("%d/%d running", row.Running, row.Capacity)
		}
		marker := " "
		if model.Focus.Region == "org" && i == 0 {
			marker = ">"
		}
		line := fmt.Sprintf("%s %-16s %d working  %d idle  %d crashed  [%s, %d queued]", marker, row.Role, row.Working, row.Idle, row.Crashed, capacity, row.Queued)
		canvas.putClipped(x, start+i, line, width)
	}
}

func renderStatus(canvas *cellCanvas, model Model) {
	y := canvas.height - 3
	failures := failedSourceLines(model)
	start := y - len(failures)
	canvas.clearRect(1, max(3, start-1), canvas.width-2, y-max(3, start-1)+1)
	canvas.horizontal(0, start-1, canvas.width)
	for i, failure := range failures {
		canvas.putClipped(2, start+i, failure, canvas.width-4)
	}
	query := "none"
	if model.Query != "" {
		query = model.Query
	}
	if model.QueryActive {
		query = "> " + query
	}
	if model.QueryError != "" {
		query += " ERROR: " + model.QueryError
	}
	resources := 0
	requested := 0
	if model.Snapshot != nil {
		resources = len(model.Snapshot.Resources)
		requested = model.Snapshot.ResourcesRequested
		if requested == 0 {
			requested = resources
		}
	}
	line := fmt.Sprintf("%s Filter: %s | %s | collections %d/3 | resources %d/%d | focus %s", focusMarker(model, "status", "refresh"), query, freshnessText(model), successfulCollections(model), resources, requested, model.FocusLabel())
	if model.Feedback != "" {
		line += " | " + model.Feedback
	}
	canvas.putClipped(2, y, line, canvas.width-4)
}

func renderFooter(canvas *cellCanvas, model Model) {
	y := canvas.height - 2
	text := "Tab focus  arrows/hjkl move  Enter inspect  [/] section  / filter  p poll  r refresh  ? help  q quit"
	if model.Size == SizeCompact {
		text = "Tab focus  Enter inspect  [/] section  / filter  r refresh  ? help  q quit"
	}
	if !model.Polling {
		text += "  [polling paused]"
	}
	canvas.putClipped(2, y, text, canvas.width-4)
}

func renderOverlay(canvas *cellCanvas, model Model, overlay Overlay) {
	x := max(2, canvas.width/8)
	y := 4
	w := max(20, canvas.width-2*x)
	h := min(canvas.height-8, 19)
	canvas.clearRect(x, y, w, h)
	canvas.box(x, y, w, h)
	switch overlay {
	case OverlayHelp:
		canvas.put(x+2, y, " Help ")
		bindings := Bindings()
		pageSize := helpPageSize(model)
		page := clampHelpPage(model, model.HelpPage)
		first := page * pageSize
		last := min(len(bindings), first+pageSize)
		row := y + 2
		for _, binding := range bindings[first:last] {
			canvas.putClipped(x+2, row, fmt.Sprintf("%-12s %s", binding.Label, binding.Description), w-4)
			row++
		}
		canvas.putClipped(x+2, y+h-2, fmt.Sprintf("Page %d/%d  PgUp/PgDn pages  ? or Esc closes", page+1, helpPageCount(model)), w-4)
	case OverlayPalette:
		canvas.put(x+2, y, " Command palette ")
		canvas.putClipped(x+2, y+2, "Search: "+model.PaletteQuery, w-4)
		items := filteredPaletteItems(model.PaletteQuery)
		visible := max(1, h-5)
		first := 0
		if model.PaletteIndex >= visible {
			first = model.PaletteIndex - visible + 1
		}
		last := min(len(items), first+visible)
		for i, item := range items[first:last] {
			marker := " "
			if first+i == model.PaletteIndex {
				marker = ">"
			}
			canvas.putClipped(x+2, y+3+i, marker+" "+item.Label, w-4)
		}
		if len(items) == 0 {
			canvas.put(x+2, y+3, "No matching commands")
		}
		canvas.putClipped(x+2, y+h-2, "Type to search; Enter selects; Ctrl+K or Esc closes.", w-4)
	}
}

func navigationText(model Model) string {
	if model.Size == SizeCompact {
		return selectedRoute(model.Route, RouteOverview, "Overview") + " " + selectedRoute(model.Route, RouteWork, "Work") + " " + selectedRoute(model.Route, RouteFleet, "Fleet") + " Activity Logs More..."
	}
	parts := make([]string, 0, len(routeOrder))
	for _, route := range routeOrder {
		parts = append(parts, selectedRoute(model.Route, route, routeTitle(route)))
	}
	return strings.Join(parts, "  ")
}

func selectedRoute(current, route Route, label string) string {
	if current == route {
		return "[" + label + "]"
	}
	return label
}

func connectionLabel(model Model) string {
	return strings.ToUpper(string(model.Connection))
}

func connectionDetail(model Model) string {
	if model.Connection == ConnectionDisconnected || model.Connection == ConnectionStale {
		return "snapshot from " + clockText(model.LastGoodAt)
	}
	if model.RefreshInFlight {
		return "refresh in flight"
	}
	return "snapshot " + clockText(model.LastGoodAt)
}

func freshnessText(model Model) string {
	switch model.Connection {
	case ConnectionDisconnected:
		return "DISCONNECTED - snapshot from " + clockText(model.LastGoodAt)
	case ConnectionStale:
		return "STALE - snapshot from " + clockText(model.LastGoodAt)
	case ConnectionReconnected:
		return "RECONNECTED - refreshed " + clockText(model.LastGoodAt)
	case ConnectionPartial:
		return "PARTIAL - last good " + clockText(model.LastGoodAt)
	default:
		return "Snapshot " + clockText(model.LastGoodAt)
	}
}

func successfulCollections(model Model) int {
	count := 0
	for _, source := range []daemonclient.SnapshotSource{daemonclient.SourceInstances, daemonclient.SourceJobs, daemonclient.SourceTopology} {
		state := model.Sources[source]
		if !state.FetchedAt.IsZero() && strings.TrimSpace(state.Error) == "" {
			count++
		}
	}
	return count
}

func failedSourceLines(model Model) []string {
	lines := []string{}
	for _, source := range daemonclient.SnapshotSources() {
		state := model.Sources[source]
		if strings.TrimSpace(state.Error) == "" {
			continue
		}
		errorText := strings.Join(strings.Fields(state.Error), " ")
		lines = append(lines, fmt.Sprintf("%s retained %s ERROR: %s", strings.ToUpper(string(source)), clockText(state.FetchedAt), errorText))
	}
	return lines
}

func focusMarker(model Model, region, control string) string {
	if model.Focus.Region == region && (control == "" || model.Focus.Control == control) {
		return ">"
	}
	return " "
}

func countStatus(rows []AttentionRow, kind, status string) int {
	count := 0
	for _, row := range rows {
		if row.Kind == kind && row.Status == status {
			count++
		}
	}
	return count
}

func enabledText(value bool) string {
	if value {
		return "enabled"
	}
	return "paused"
}

func clockText(value time.Time) string {
	if value.IsZero() {
		return "--:--:--"
	}
	return value.UTC().Format("15:04:05")
}

type cellCanvas struct {
	width, height int
	rows          [][]rune
	ascii         bool
}

func newCanvas(width, height int, ascii bool) *cellCanvas {
	canvas := &cellCanvas{width: width, height: height, ascii: ascii, rows: make([][]rune, height)}
	for y := range canvas.rows {
		canvas.rows[y] = []rune(strings.Repeat(" ", width))
	}
	return canvas
}

func (c *cellCanvas) put(x, y int, text string) {
	if y < 0 || y >= c.height || x >= c.width {
		return
	}
	for _, char := range text {
		if c.ascii && char > utf8.RuneSelf {
			char = '?'
		}
		if x >= 0 && x < c.width {
			c.rows[y][x] = char
		}
		x++
		if x >= c.width {
			return
		}
	}
}

func (c *cellCanvas) putClipped(x, y int, text string, width int) {
	if width <= 0 {
		return
	}
	runes := []rune(text)
	if len(runes) > width {
		if width > 3 {
			runes = append(runes[:width-3], '.', '.', '.')
		} else {
			runes = runes[:width]
		}
	}
	c.put(x, y, string(runes))
}

func (c *cellCanvas) putRight(padding, y int, text string) {
	c.put(max(padding, c.width-padding-len([]rune(text))), y, text)
}

func (c *cellCanvas) putCentered(y int, text string) {
	c.putClipped(max(1, (c.width-len([]rune(text)))/2), y, text, max(0, c.width-2))
}

func (c *cellCanvas) horizontal(x, y, width int) {
	if y < 0 || y >= c.height {
		return
	}
	for column := max(0, x); column < min(c.width, x+width); column++ {
		c.rows[y][column] = '-'
	}
	if x >= 0 && x < c.width {
		c.rows[y][x] = '+'
	}
	if x+width-1 >= 0 && x+width-1 < c.width {
		c.rows[y][x+width-1] = '+'
	}
}

func (c *cellCanvas) vertical(x, from, to int) {
	if x < 0 || x >= c.width {
		return
	}
	for y := max(0, from); y <= min(c.height-1, to); y++ {
		c.rows[y][x] = '|'
	}
}

func (c *cellCanvas) separator(y int, title string) {
	c.horizontal(0, y, c.width)
	c.put(1, y, " "+title+" ")
}

func (c *cellCanvas) box(x, y, width, height int) {
	if width <= 1 || height <= 1 {
		return
	}
	c.horizontal(x, y, width)
	c.horizontal(x, y+height-1, width)
	c.vertical(x, y, y+height-1)
	c.vertical(x+width-1, y, y+height-1)
	for _, point := range [][2]int{{x, y}, {x + width - 1, y}, {x, y + height - 1}, {x + width - 1, y + height - 1}} {
		if point[0] >= 0 && point[0] < c.width && point[1] >= 0 && point[1] < c.height {
			c.rows[point[1]][point[0]] = '+'
		}
	}
}

func (c *cellCanvas) clearRect(x, y, width, height int) {
	for row := max(0, y); row < min(c.height, y+height); row++ {
		for column := max(0, x); column < min(c.width, x+width); column++ {
			c.rows[row][column] = ' '
		}
	}
}

func (c *cellCanvas) string() string {
	lines := make([]string, c.height)
	for i, row := range c.rows {
		lines[i] = string(row)
	}
	return strings.Join(lines, "\n")
}
