package tui

import (
	"fmt"
	"sort"
	"strings"
)

func renderParityScreen(canvas *cellCanvas, model Model) {
	renderScreenTabs(canvas, model)
	switch model.Screen {
	case ScreenWorkJobs:
		renderJobsScreen(canvas, model)
	case ScreenWorkTelemetry:
		renderTelemetryScreen(canvas, model)
	case ScreenFleetOrg:
		renderLiveOrgScreen(canvas, model)
	case ScreenFleetInstances:
		renderInstancesScreen(canvas, model)
	case ScreenFleetTopology:
		renderTopologyScreen(canvas, model)
	default:
		renderPlaceholder(canvas, model)
	}
}

func renderScreenTabs(canvas *cellCanvas, model Model) {
	var tabs []struct {
		screen Screen
		label  string
	}
	switch model.Route {
	case RouteWork:
		tabs = []struct {
			screen Screen
			label  string
		}{{ScreenWorkJobs, "Jobs"}, {ScreenWorkTelemetry, "Telemetry"}}
	case RouteFleet:
		tabs = []struct {
			screen Screen
			label  string
		}{{ScreenFleetOrg, "Org"}, {ScreenFleetInstances, "Instances"}, {ScreenFleetTopology, "Topology"}}
	default:
		return
	}
	parts := []string{focusMarker(model, "screen", "tabs")}
	for _, tab := range tabs {
		label := tab.label
		if model.Screen == tab.screen {
			label = "[" + label + "]"
		}
		parts = append(parts, label)
	}
	canvas.putClipped(2, 3, strings.Join(parts, " "), canvas.width-4)
	query := "Query: none"
	if model.Query != "" {
		query = "Query: " + model.Query
	}
	if model.QueryActive {
		query = "Query: > " + model.Query
	}
	if model.QueryError != "" {
		query += " ERROR: " + model.QueryError
	}
	canvas.putRight(2, 3, query+" ")
	canvas.horizontal(0, 4, canvas.width)
}

func contentBottom(canvas *cellCanvas, model Model) int {
	return max(5, canvas.height-4-len(failedSourceLines(model)))
}

func renderJobsScreen(canvas *cellCanvas, model Model) {
	rows := projectJobs(model)
	bottom := contentBottom(canvas, model)
	if model.Inspecting && model.Size == SizeCompact {
		renderSelectedJobDetail(canvas, model, rows, 5, bottom, 2, canvas.width-4)
		return
	}
	if model.Size == SizeCompact {
		canvas.put(2, 5, "Jobs")
		canvas.putClipped(2, 6, "  ID                   STATUS         MODEL/TIER", canvas.width-4)
		listEnd := min(bottom-7, 13)
		renderJobRows(canvas, model, rows, 7, listEnd, 2, canvas.width-4, true)
		canvas.separator(listEnd, "Model / tier")
		telemetry := projectTelemetry(model)
		y := listEnd + 1
		for _, row := range telemetry.Models {
			if y >= bottom-2 {
				break
			}
			marker := itemMarker(model, "models", row.Label)
			canvas.putClipped(2, y, fmt.Sprintf("%s %-24s jobs %-3d active %-3d bounces %-3d", marker, row.Label, row.Jobs, row.Active, row.Bounces), canvas.width-4)
			y++
		}
		canvas.separator(max(y, bottom-2), "Bounce classes")
		if bottom-1 < canvas.height {
			canvas.putClipped(2, bottom-1, bounceSummary(telemetry.Bounces), canvas.width-4)
		}
		return
	}
	divider := 70
	if model.Size == SizeWide {
		divider = 106
	}
	canvas.put(2, 5, "Jobs")
	canvas.put(divider+2, 5, "Detail")
	canvas.vertical(divider, 4, min(bottom-1, 15))
	canvas.put(divider, 4, "+")
	canvas.putClipped(2, 6, "  ID                   TICKET   STATUS        PIPELINE          MODEL/TIER", divider-4)
	listEnd := min(bottom-9, 15)
	renderJobRows(canvas, model, rows, 7, listEnd, 2, divider-4, false)
	renderSelectedJobDetail(canvas, model, rows, 6, min(bottom, 15), divider+2, canvas.width-divider-4)
	section := min(16, bottom-7)
	canvas.separator(section, "Model / tier")
	telemetry := projectTelemetry(model)
	y := section + 1
	for _, row := range telemetry.Models {
		if y >= bottom-3 {
			break
		}
		marker := itemMarker(model, "models", row.Label)
		canvas.putClipped(2, y, fmt.Sprintf("%s %-32s jobs %-3d active %-3d bounces %-3d", marker, row.Label, row.Jobs, row.Active, row.Bounces), canvas.width-4)
		y++
	}
	canvas.separator(max(y, bottom-3), "Bounce classes")
	if len(telemetry.Bounces) > 0 {
		if y := max(y+1, bottom-2); y < bottom {
			canvas.putClipped(2, y, bounceSummary(telemetry.Bounces), canvas.width-4)
		}
	}
}

func renderJobRows(canvas *cellCanvas, model Model, rows []JobRow, start, end, x, width int, compact bool) {
	if len(rows) == 0 {
		canvas.putClipped(x, start, "No jobs match the current query.", width)
		return
	}
	visible := max(1, end-start)
	first := selectedWindow(jobIDs(rows), model.Focus.ItemID, visible)
	for index := 0; index < visible && first+index < len(rows); index++ {
		row := rows[first+index]
		label := modelTierLabel(row.Model, row.Tier, "not reported")
		var line string
		if compact {
			line = fmt.Sprintf("%s %-20s %-14s %s", itemMarker(model, "jobs", row.ID), row.ID, row.Status, label)
		} else {
			line = fmt.Sprintf("%s %-20s %-8s %-13s %-17s %s", itemMarker(model, "jobs", row.ID), row.ID, firstText(row.Ticket, "-"), row.Status, firstText(row.Pipeline, "-"), label)
		}
		canvas.putClipped(x, start+index, line, width)
	}
}

func renderSelectedJobDetail(canvas *cellCanvas, model Model, rows []JobRow, start, end, x, width int) {
	row, ok := selectedJob(rows, model.Focus.ItemID)
	if !ok {
		canvas.putClipped(x, start, "Select a job to inspect complete telemetry.", width)
		return
	}
	lines := []string{
		row.ID,
		"ticket " + firstText(row.Ticket, "-"),
		"status " + firstText(row.Status, "-"),
		"pipeline " + firstText(row.Pipeline, "-"),
		"instance " + firstText(row.Instance, "-"),
		"model/tier " + modelTierLabel(row.Model, row.Tier, "not reported"),
		"runtime " + firstText(row.Runtime, "not reported"),
		"bounces " + bounceMapSummary(row.Bounces),
	}
	for index, line := range lines {
		if start+index >= end {
			break
		}
		canvas.putClipped(x, start+index, line, width)
	}
}

func renderTelemetryScreen(canvas *cellCanvas, model Model) {
	projection := projectTelemetry(model)
	bottom := contentBottom(canvas, model)
	canvas.put(2, 5, "Recent 24 jobs - reported model/tier")
	canvas.putClipped(2, 6, "  MODEL / TIER                       JOBS  ACTIVE  BOUNCES", canvas.width-4)
	mid := min(bottom-7, max(11, 7+len(projection.Models)))
	for index, row := range projection.Models {
		if 7+index >= mid {
			break
		}
		canvas.putClipped(2, 7+index, fmt.Sprintf("%s %-34s %-5d %-7d %d", itemMarker(model, "models", row.Label), row.Label, row.Jobs, row.Active, row.Bounces), canvas.width-4)
	}
	if len(projection.Models) == 0 {
		canvas.put(2, 7, "No model/tier telemetry reported.")
	}
	canvas.separator(mid, "Bounce classes")
	canvas.putClipped(2, mid+1, "  CLASS                         BOUNCES  AFFECTED JOBS", canvas.width-4)
	for index, row := range projection.Bounces {
		if mid+2+index >= bottom {
			break
		}
		canvas.putClipped(2, mid+2+index, fmt.Sprintf("%s %-29s %-8d %d", itemMarker(model, "bounces", row.Class), row.Class, row.Bounces, row.Jobs), canvas.width-4)
	}
	if len(projection.Bounces) == 0 {
		canvas.put(2, mid+2, "No classified review bounces in the recent window.")
	}
}

func renderInstancesScreen(canvas *cellCanvas, model Model) {
	rows := projectInstances(model)
	bottom := contentBottom(canvas, model)
	if model.Inspecting && model.Size == SizeCompact {
		renderSelectedInstanceDetail(canvas, model, rows, 5, bottom, 2, canvas.width-4)
		return
	}
	divider := canvas.width
	if model.Size == SizeStandard {
		divider = 70
	} else if model.Size == SizeWide {
		divider = 106
	}
	canvas.put(2, 5, "Instances")
	canvas.putClipped(2, 6, "  NAME                     AGENT          STATUS       PHASE       MODEL/TIER", divider-4)
	renderInstanceRows(canvas, model, rows, 7, bottom, 2, divider-4, model.Size == SizeCompact)
	if divider < canvas.width {
		canvas.vertical(divider, 4, bottom-1)
		canvas.put(divider, 4, "+")
		canvas.put(divider+2, 5, "Detail")
		renderSelectedInstanceDetail(canvas, model, rows, 6, bottom, divider+2, canvas.width-divider-4)
	}
}

func renderInstanceRows(canvas *cellCanvas, model Model, rows []InstanceRow, start, end, x, width int, compact bool) {
	if len(rows) == 0 {
		canvas.putClipped(x, start, "No runtime instances match the current query.", width)
		return
	}
	visible := max(1, end-start)
	first := selectedWindow(instanceIDs(rows), model.Focus.ItemID, visible)
	for index := 0; index < visible && first+index < len(rows); index++ {
		row := rows[first+index]
		line := fmt.Sprintf("%s %-24s %-14s %-12s %-11s %s", itemMarker(model, "instances", row.Name), row.Name, row.Agent, row.Status, firstText(row.Phase, "-"), modelTierLabel(row.Model, row.Tier, "not reported"))
		if compact {
			line = fmt.Sprintf("%s %-24s %-12s %s", itemMarker(model, "instances", row.Name), row.Name, row.Status, firstText(row.Job, "-"))
		}
		canvas.putClipped(x, start+index, line, width)
	}
}

func renderSelectedInstanceDetail(canvas *cellCanvas, model Model, rows []InstanceRow, start, end, x, width int) {
	row, ok := selectedInstance(rows, model.Focus.ItemID)
	if !ok {
		canvas.putClipped(x, start, "Select an instance to inspect complete state.", width)
		return
	}
	lines := []string{row.Name, "agent " + firstText(row.Agent, "-"), "lifecycle " + firstText(row.Status, "-"), "phase " + firstText(row.Phase, "-"), "job " + firstText(row.Job, "-"), "ticket " + firstText(row.Ticket, "-"), "model/tier " + modelTierLabel(row.Model, row.Tier, "not reported"), "runtime " + firstText(row.Runtime, "not reported"), "description " + firstText(row.Description, "-")}
	for index, line := range lines {
		if start+index >= end {
			break
		}
		canvas.putClipped(x, start+index, line, width)
	}
}

func renderLiveOrgScreen(canvas *cellCanvas, model Model) {
	roles := projectLiveOrg(model)
	bottom := contentBottom(canvas, model)
	lanes := flattenOrgLanes(roles)
	if model.Inspecting && model.Size == SizeCompact {
		renderSelectedLaneDetail(canvas, model, lanes, 5, bottom, 2, canvas.width-4)
		return
	}
	divider := canvas.width
	if model.Size == SizeStandard {
		divider = 78
	} else if model.Size == SizeWide {
		divider = 106
	}
	canvas.put(2, 5, "Live org")
	y := 6
	for _, role := range roles {
		if y >= bottom {
			break
		}
		canvas.putClipped(2, y, fmt.Sprintf("%s: %d working  %d idle  %d queued  %d crashed", titleCase(role.Role), role.Working, role.Idle, role.Queued, role.Crashed), divider-4)
		y++
		for _, lane := range role.Lanes {
			if y >= bottom {
				break
			}
			canvas.putClipped(2, y, fmt.Sprintf("%s %-24s %-10s %s", itemMarker(model, "org", lane.Name), lane.Name, lane.State, lane.Meta), divider-4)
			y++
		}
	}
	if len(lanes) == 0 {
		canvas.put(2, 7, "No declared or runtime lanes match the current query.")
	}
	if divider < canvas.width {
		canvas.vertical(divider, 4, bottom-1)
		canvas.put(divider, 4, "+")
		canvas.put(divider+2, 5, "Context")
		renderSelectedLaneDetail(canvas, model, lanes, 6, bottom, divider+2, canvas.width-divider-4)
	}
}

func renderSelectedLaneDetail(canvas *cellCanvas, model Model, lanes []OrgLaneRow, start, end, x, width int) {
	lane, ok := selectedLane(lanes, model.Focus.ItemID)
	if !ok {
		canvas.putClipped(x, start, "Select a declared/runtime lane.", width)
		return
	}
	lines := []string{lane.Name, "role " + lane.Agent, "state " + lane.State, "capacity " + firstText(lane.Meta, "-")}
	for _, instance := range lane.Instances {
		lines = append(lines, instance.Name+" | "+firstText(instance.Job, "no active job"), "  "+firstText(instance.Phase, "-")+" / "+instance.Status+" / "+modelTierLabel(instance.Model, instance.Tier, firstText(instance.Runtime, "not reported")))
	}
	if len(lane.Instances) == 0 {
		lines = append(lines, "idle - no runtime instance")
	}
	for index, line := range lines {
		if start+index >= end {
			break
		}
		canvas.putClipped(x, start+index, line, width)
	}
}

func renderTopologyScreen(canvas *cellCanvas, model Model) {
	projection := projectTopology(model)
	bottom := contentBottom(canvas, model)
	sectionParts := []string{focusMarker(model, "section", "tabs")}
	for _, section := range topologySections {
		label := string(section)
		if section == model.TopologySection {
			label = "[" + label + "]"
		}
		sectionParts = append(sectionParts, label)
	}
	canvas.putClipped(2, 5, strings.Join(sectionParts, " "), canvas.width-4)
	canvas.horizontal(0, 6, canvas.width)
	if model.Inspecting && model.Size == SizeCompact {
		renderTopologyDetail(canvas, model, projection, 7, bottom, 2, canvas.width-4)
		return
	}
	divider := canvas.width
	if model.Size == SizeStandard {
		divider = 76
	} else if model.Size == SizeWide {
		divider = 106
	}
	renderTopologyRows(canvas, model, projection, 7, bottom, 2, divider-4)
	if divider < canvas.width {
		canvas.vertical(divider, 6, bottom-1)
		canvas.put(divider, 6, "+")
		canvas.put(divider+2, 7, "Detail")
		renderTopologyDetail(canvas, model, projection, 8, bottom, divider+2, canvas.width-divider-4)
	}
}

func renderTopologyRows(canvas *cellCanvas, model Model, projection TopologyProjection, start, end, x, width int) {
	lines := topologyLines(model, projection)
	if len(lines) == 0 {
		canvas.putClipped(x, start, "No "+string(model.TopologySection)+" match the current query.", width)
		return
	}
	ids := make([]string, len(lines))
	for index := range lines {
		ids[index] = lines[index][0]
	}
	first := selectedWindow(ids, model.Focus.ItemID, max(1, end-start))
	for index := 0; start+index < end && first+index < len(lines); index++ {
		line := lines[first+index]
		canvas.putClipped(x, start+index, itemMarker(model, "topology", line[0])+" "+line[1], width)
	}
}

func topologyLines(model Model, projection TopologyProjection) [][2]string {
	lines := [][2]string{}
	switch model.TopologySection {
	case TopologyDeployments:
		for _, row := range projection.Deployments {
			parent := "root"
			if row.Parent != "" {
				parent = shortURI(row.Parent)
			}
			lines = append(lines, [2]string{row.URI, fmt.Sprintf("%-28s parent %-18s %d inst / %d jobs  %s", shortURI(row.URI), parent, row.Instances, row.Jobs, row.Status)})
		}
	case TopologyPipelines:
		for _, row := range projection.Pipelines {
			lines = append(lines, [2]string{row.Name, fmt.Sprintf("%-28s %-28s %s", row.Name, row.Trigger, activeLabel(row.Active))})
		}
	case TopologyBudgets:
		for _, row := range projection.Budgets {
			open := "-"
			if row.Open != nil {
				open = fmt.Sprintf("%d open", *row.Open)
			}
			lines = append(lines, [2]string{row.Team, fmt.Sprintf("%-20s %s/day cap %s  %s  %s", row.Team, row.Tokens, row.Cap, activeLabel(row.Active), open)})
		}
	case TopologySchedules:
		for _, row := range projection.Schedules {
			lines = append(lines, [2]string{row.Name, fmt.Sprintf("%-24s %-10s %-24s team %s", row.Name, row.Cadence, row.LastFired, row.Team)})
		}
	case TopologyDeadlines:
		for _, row := range projection.Deadlines {
			lines = append(lines, [2]string{row.Label, fmt.Sprintf("%-28s %-24s %-10s %s", row.Label, row.Deadline, row.State, row.Source)})
		}
	case TopologyTeams:
		for _, row := range projection.Teams {
			lines = append(lines, [2]string{row.Name, fmt.Sprintf("%-20s %d instances  %d pipelines  %d channels  %s", row.Name, row.Instances, row.Pipelines, row.Channels, activeLabel(row.Active))})
		}
	}
	return lines
}

func renderTopologyDetail(canvas *cellCanvas, model Model, projection TopologyProjection, start, end, x, width int) {
	lines := []string{"Select a topology row for complete detail."}
	id := model.Focus.ItemID
	switch model.TopologySection {
	case TopologyDeployments:
		for _, row := range projection.Deployments {
			if row.URI == id {
				lines = []string{row.URI, "parent " + firstText(row.Parent, "root"), fmt.Sprintf("children %d instances / %d jobs", row.Instances, row.Jobs), "charter " + firstText(row.CharterURI, "not reported"), "charter state " + firstText(row.CharterStatus, "not reported"), "status " + row.Status}
			}
		}
	case TopologyPipelines:
		for _, row := range projection.Pipelines {
			if row.Name == id {
				lines = []string{row.Name, "trigger " + row.Trigger, "steps " + row.Steps, activeLabel(row.Active)}
			}
		}
	case TopologyBudgets:
		for _, row := range projection.Budgets {
			if row.Team == id {
				lines = []string{row.Team, "tokens/day " + row.Tokens, "jobs cap " + row.Cap, activeLabel(row.Active), "allocation " + row.Allocation}
			}
		}
	case TopologySchedules:
		for _, row := range projection.Schedules {
			if row.Name == id {
				lines = []string{row.Name, "cadence " + row.Cadence, "last fired " + row.LastFired, "team " + row.Team, "payload " + row.Payload}
			}
		}
	case TopologyDeadlines:
		for _, row := range projection.Deadlines {
			if row.Label == id {
				lines = []string{row.Label, "deadline " + row.Deadline, "state " + row.State, "source " + row.Source}
			}
		}
	case TopologyTeams:
		for _, row := range projection.Teams {
			if row.Name == id {
				lines = []string{row.Name, fmt.Sprintf("instances %d", row.Instances), fmt.Sprintf("pipelines %d", row.Pipelines), fmt.Sprintf("channels %d", row.Channels), activeLabel(row.Active)}
			}
		}
	}
	for index, line := range lines {
		if start+index >= end {
			break
		}
		canvas.putClipped(x, start+index, line, width)
	}
}

func itemMarker(model Model, region, id string) string {
	if model.Focus.Region == region && model.Focus.ItemID == id {
		return ">"
	}
	return " "
}

func selectedWindow(ids []string, selected string, visible int) int {
	if visible <= 0 || len(ids) <= visible {
		return 0
	}
	index := 0
	for current, id := range ids {
		if id == selected {
			index = current
			break
		}
	}
	first := index - visible/2
	return max(0, min(len(ids)-visible, first))
}

func jobIDs(rows []JobRow) []string {
	out := make([]string, len(rows))
	for i := range rows {
		out[i] = rows[i].ID
	}
	return out
}
func instanceIDs(rows []InstanceRow) []string {
	out := make([]string, len(rows))
	for i := range rows {
		out[i] = rows[i].Name
	}
	return out
}

func selectedJob(rows []JobRow, id string) (JobRow, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	if len(rows) > 0 {
		return rows[0], true
	}
	return JobRow{}, false
}

func selectedInstance(rows []InstanceRow, id string) (InstanceRow, bool) {
	for _, row := range rows {
		if row.Name == id {
			return row, true
		}
	}
	if len(rows) > 0 {
		return rows[0], true
	}
	return InstanceRow{}, false
}

func flattenOrgLanes(roles []OrgRoleProjection) []OrgLaneRow {
	rows := []OrgLaneRow{}
	for _, role := range roles {
		rows = append(rows, role.Lanes...)
	}
	return rows
}

func selectedLane(rows []OrgLaneRow, id string) (OrgLaneRow, bool) {
	for _, row := range rows {
		if row.Name == id {
			return row, true
		}
	}
	if len(rows) > 0 {
		return rows[0], true
	}
	return OrgLaneRow{}, false
}

func bounceMapSummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return counts[keys[i]] > counts[keys[j]] || counts[keys[i]] == counts[keys[j]] && keys[i] < keys[j]
	})
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", key, counts[key]))
	}
	return strings.Join(parts, "  ")
}

func bounceSummary(rows []BounceRow) string {
	if len(rows) == 0 {
		return "No classified review bounces."
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("%s %d/%d jobs", row.Class, row.Bounces, row.Jobs))
	}
	return strings.Join(parts, "   ")
}

func titleCase(value string) string {
	if value == "" {
		return "Unknown"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
