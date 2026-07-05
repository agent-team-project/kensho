package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
)

const DefaultBriefEventLimit = 12

type BriefOptions struct {
	EventLimit int
	Now        time.Time
}

type InstanceBrief struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Instance    string            `json:"instance"`
	Agent       string            `json:"agent,omitempty"`
	Role        string            `json:"role,omitempty"`
	StateDir    string            `json:"state_dir"`
	DaemonDir   string            `json:"daemon_dir"`
	Topology    *BriefTopology    `json:"topology,omitempty"`
	Runtime     *BriefRuntime     `json:"runtime,omitempty"`
	Jobs        []BriefJob        `json:"jobs"`
	Pipelines   []BriefPipeline   `json:"pipelines"`
	Mailbox     []BriefMessage    `json:"mailbox"`
	Channels    []BriefChannel    `json:"channels"`
	Events      []BriefEvent      `json:"events"`
	Fleet       []BriefFleetRow   `json:"fleet"`
	Path        string            `json:"path,omitempty"`
	Text        string            `json:"text,omitempty"`
	Errors      map[string]string `json:"errors,omitempty"`
}

type BriefTopology struct {
	Declared    bool     `json:"declared"`
	Agent       string   `json:"agent,omitempty"`
	Description string   `json:"description,omitempty"`
	Ephemeral   bool     `json:"ephemeral"`
	Restart     string   `json:"restart,omitempty"`
	Brief       bool     `json:"brief"`
	Teams       []string `json:"teams,omitempty"`
}

type BriefRuntime struct {
	Lifecycle     string `json:"lifecycle,omitempty"`
	Runtime       string `json:"runtime,omitempty"`
	RuntimeBinary string `json:"runtime_binary,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	PID           int    `json:"pid,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Job           string `json:"job,omitempty"`
	Ticket        string `json:"ticket,omitempty"`
	Branch        string `json:"branch,omitempty"`
	PR            string `json:"pr,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	StoppedAt     string `json:"stopped_at,omitempty"`
	ExitedAt      string `json:"exited_at,omitempty"`
}

type BriefJob struct {
	ID         string         `json:"id"`
	Ticket     string         `json:"ticket,omitempty"`
	TicketURL  string         `json:"ticket_url,omitempty"`
	Target     string         `json:"target,omitempty"`
	Instance   string         `json:"instance,omitempty"`
	Pipeline   string         `json:"pipeline,omitempty"`
	Status     string         `json:"status"`
	Branch     string         `json:"branch,omitempty"`
	PR         string         `json:"pr,omitempty"`
	LastEvent  string         `json:"last_event,omitempty"`
	LastStatus string         `json:"last_status,omitempty"`
	UpdatedAt  string         `json:"updated_at,omitempty"`
	Steps      []BriefJobStep `json:"steps,omitempty"`
}

type BriefJobStep struct {
	ID               string `json:"id"`
	Target           string `json:"target,omitempty"`
	Instance         string `json:"instance,omitempty"`
	Status           string `json:"status"`
	Gate             string `json:"gate,omitempty"`
	ApprovalRequired bool   `json:"approval_required,omitempty"`
	ApprovalID       string `json:"approval_id,omitempty"`
	ApprovalStatus   string `json:"approval_status,omitempty"`
}

type BriefPipeline struct {
	Name    string `json:"name"`
	Jobs    int    `json:"jobs"`
	Queued  int    `json:"queued"`
	Running int    `json:"running"`
	Blocked int    `json:"blocked"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
}

type BriefMessage struct {
	ID   string `json:"id"`
	From string `json:"from,omitempty"`
	Body string `json:"body"`
	TS   string `json:"ts,omitempty"`
}

type BriefChannel struct {
	Name        string `json:"name"`
	Cursor      int64  `json:"cursor"`
	Unread      int    `json:"unread"`
	LatestSeq   int64  `json:"latest_seq,omitempty"`
	LastMessage string `json:"last_message,omitempty"`
}

type BriefEvent struct {
	ID       string `json:"id"`
	TS       string `json:"ts,omitempty"`
	Action   string `json:"action"`
	Instance string `json:"instance,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Job      string `json:"job,omitempty"`
	Ticket   string `json:"ticket,omitempty"`
	Status   string `json:"status,omitempty"`
	Message  string `json:"message,omitempty"`
}

type BriefFleetRow struct {
	Instance  string `json:"instance"`
	Agent     string `json:"agent,omitempty"`
	Lifecycle string `json:"lifecycle,omitempty"`
	Phase     string `json:"phase,omitempty"`
	Job       string `json:"job,omitempty"`
	Ticket    string `json:"ticket,omitempty"`
	Branch    string `json:"branch,omitempty"`
	PR        string `json:"pr,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

type briefStatusFile struct {
	Status struct {
		Phase       string `toml:"phase"`
		Description string `toml:"description"`
		LastAction  string `toml:"last_action"`
	} `toml:"status"`
	Work *struct {
		Job    string `toml:"job"`
		Ticket string `toml:"ticket"`
		PR     string `toml:"pr"`
		Branch string `toml:"branch"`
	} `toml:"work"`
}

func ShouldGenerateInstanceBrief(teamDir, instance string) bool {
	if strings.TrimSpace(teamDir) == "" || strings.TrimSpace(instance) == "" {
		return false
	}
	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil || topo == nil {
		return false
	}
	inst := topo.Find(instance)
	return inst != nil && !inst.Ephemeral && inst.Brief
}

func GenerateAndWriteInstanceBrief(teamDir, instance string, opts BriefOptions) (*InstanceBrief, error) {
	brief, err := GenerateInstanceBrief(teamDir, instance, opts)
	if err != nil {
		return nil, err
	}
	text := RenderInstanceBrief(brief)
	stateDir := filepath.Join(teamDir, "state", instance)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("brief: mkdir state dir: %w", err)
	}
	path := filepath.Join(stateDir, "brief.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return nil, fmt.Errorf("brief: write %s: %w", path, err)
	}
	brief.Path = path
	brief.Text = text
	return brief, nil
}

func InstanceBriefLaunchText(teamDir, instance string) (string, error) {
	if !ShouldGenerateInstanceBrief(teamDir, instance) {
		return "", nil
	}
	brief, err := GenerateAndWriteInstanceBrief(teamDir, instance, BriefOptions{})
	if err != nil {
		return "", err
	}
	return brief.Text, nil
}

func GenerateInstanceBrief(teamDir, instance string, opts BriefOptions) (*InstanceBrief, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return nil, fmt.Errorf("brief: instance is required")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.EventLimit == 0 {
		opts.EventLimit = DefaultBriefEventLimit
	}
	root := DaemonRoot(teamDir)
	brief := &InstanceBrief{
		GeneratedAt: opts.Now,
		Instance:    instance,
		StateDir:    filepath.Join(teamDir, "state", instance),
		DaemonDir:   instanceDir(root, instance),
		Jobs:        []BriefJob{},
		Pipelines:   []BriefPipeline{},
		Mailbox:     []BriefMessage{},
		Channels:    []BriefChannel{},
		Events:      []BriefEvent{},
		Fleet:       []BriefFleetRow{},
		Errors:      map[string]string{},
	}

	topo, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		brief.Errors["topology"] = err.Error()
	}
	var declared *topology.Instance
	if topo != nil {
		declared = topo.Find(instance)
	}
	teams := topologyTeamsForInstance(topo, instance)
	if declared != nil {
		brief.Agent = declared.Agent
		brief.Role = declared.Description
		brief.Topology = &BriefTopology{
			Declared:    true,
			Agent:       declared.Agent,
			Description: declared.Description,
			Ephemeral:   declared.Ephemeral,
			Restart:     declared.Restart,
			Brief:       declared.Brief,
			Teams:       teams,
		}
	}
	if meta, err := ReadMetadata(root, instance); err == nil {
		brief.Runtime = briefRuntimeFromMetadata(meta)
		if brief.Agent == "" {
			brief.Agent = meta.Agent
		}
	} else if !os.IsNotExist(err) {
		brief.Errors["runtime"] = err.Error()
	}

	jobs, err := jobstore.List(teamDir)
	if err != nil {
		brief.Errors["jobs"] = err.Error()
	}
	ownedJobs, ownedIDs := briefOwnedJobs(jobs, instance)
	brief.Jobs = briefJobRows(ownedJobs)
	brief.Pipelines = briefPipelineRows(ownedJobs)

	if messages, err := ReadUnacked(root, instance); err == nil {
		brief.Mailbox = briefMessageRows(messages)
	} else {
		brief.Errors["mailbox"] = err.Error()
	}
	if channels, err := briefChannelRows(root, instance); err == nil {
		brief.Channels = channels
	} else {
		brief.Errors["channels"] = err.Error()
	}
	if events, err := ListLifecycleEvents(root); err == nil {
		brief.Events = briefEventRows(events, instance, ownedIDs, opts.EventLimit)
	} else {
		brief.Errors["events"] = err.Error()
	}
	if fleet, err := briefFleetRows(teamDir, topo, instance, teams); err == nil {
		brief.Fleet = fleet
	} else {
		brief.Errors["fleet"] = err.Error()
	}
	if len(brief.Errors) == 0 {
		brief.Errors = nil
	}
	return brief, nil
}

func RenderInstanceBrief(brief *InstanceBrief) string {
	if brief == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Instance brief: %s\n\n", brief.Instance)
	fmt.Fprintf(&b, "Generated: %s\n\n", brief.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "## Identity\n\n")
	fmt.Fprintf(&b, "- Instance: %s\n", brief.Instance)
	if brief.Agent != "" {
		fmt.Fprintf(&b, "- Agent: %s\n", brief.Agent)
	}
	if brief.Role != "" {
		fmt.Fprintf(&b, "- Role: %s\n", brief.Role)
	}
	fmt.Fprintf(&b, "- State dir: %s\n", filepath.ToSlash(brief.StateDir))
	fmt.Fprintf(&b, "- Daemon metadata: %s\n", filepath.ToSlash(brief.DaemonDir))
	if brief.Topology != nil {
		fmt.Fprintf(&b, "- Declared: yes (ephemeral=%t, restart=%s, brief=%t)\n", brief.Topology.Ephemeral, emptyBriefDash(brief.Topology.Restart), brief.Topology.Brief)
		if len(brief.Topology.Teams) > 0 {
			fmt.Fprintf(&b, "- Teams: %s\n", strings.Join(brief.Topology.Teams, ", "))
		}
	} else {
		fmt.Fprintf(&b, "- Declared: no\n")
	}
	if brief.Runtime != nil {
		fmt.Fprintf(&b, "\n## Runtime\n\n")
		fmt.Fprintf(&b, "- Lifecycle: %s\n", emptyBriefDash(brief.Runtime.Lifecycle))
		fmt.Fprintf(&b, "- Runtime: %s\n", emptyBriefDash(brief.Runtime.Runtime))
		fmt.Fprintf(&b, "- Workspace: %s\n", emptyBriefDash(filepath.ToSlash(brief.Runtime.Workspace)))
		if brief.Runtime.PID > 0 {
			fmt.Fprintf(&b, "- PID: %d\n", brief.Runtime.PID)
		}
		if brief.Runtime.SessionID != "" {
			fmt.Fprintf(&b, "- Session: %s\n", brief.Runtime.SessionID)
		}
	}
	renderBriefJobs(&b, brief.Jobs)
	renderBriefPipelines(&b, brief.Pipelines)
	renderBriefMessages(&b, "Unread Mailbox", brief.Mailbox)
	renderBriefChannels(&b, brief.Channels)
	renderBriefEvents(&b, brief.Events)
	renderBriefFleet(&b, brief.Fleet)
	if len(brief.Errors) > 0 {
		fmt.Fprintf(&b, "\n## Collection Errors\n\n")
		keys := make([]string, 0, len(brief.Errors))
		for key := range brief.Errors {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "- %s: %s\n", key, brief.Errors[key])
		}
	}
	return b.String()
}

func topologyTeamsForInstance(topo *topology.Topology, instance string) []string {
	if topo == nil {
		return nil
	}
	var teams []string
	for name, team := range topo.Teams {
		if team == nil {
			continue
		}
		for _, member := range team.Instances {
			if member == instance {
				teams = append(teams, name)
				break
			}
		}
	}
	sort.Strings(teams)
	return teams
}

func briefRuntimeFromMetadata(meta *Metadata) *BriefRuntime {
	out := &BriefRuntime{
		Lifecycle:     string(meta.Status),
		Runtime:       meta.Runtime,
		RuntimeBinary: meta.RuntimeBinary,
		Workspace:     meta.Workspace,
		PID:           meta.PID,
		SessionID:     meta.SessionID,
		Job:           meta.Job,
		Ticket:        meta.Ticket,
		Branch:        meta.Branch,
		PR:            meta.PR,
	}
	if !meta.StartedAt.IsZero() {
		out.StartedAt = meta.StartedAt.Format(time.RFC3339)
	}
	if !meta.StoppedAt.IsZero() {
		out.StoppedAt = meta.StoppedAt.Format(time.RFC3339)
	}
	if !meta.ExitedAt.IsZero() {
		out.ExitedAt = meta.ExitedAt.Format(time.RFC3339)
	}
	return out
}

func briefOwnedJobs(jobs []*jobstore.Job, instance string) ([]*jobstore.Job, map[string]bool) {
	var out []*jobstore.Job
	ids := map[string]bool{}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if strings.TrimSpace(j.Instance) == instance || jobStepsIncludeInstance(j, instance) {
			out = append(out, j)
			ids[j.ID] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, ids
}

func jobStepsIncludeInstance(j *jobstore.Job, instance string) bool {
	for _, step := range j.Steps {
		if strings.TrimSpace(step.Instance) == instance {
			return true
		}
	}
	return false
}

func briefJobRows(jobs []*jobstore.Job) []BriefJob {
	rows := make([]BriefJob, 0, len(jobs))
	for _, j := range jobs {
		row := BriefJob{
			ID:         j.ID,
			Ticket:     j.Ticket,
			TicketURL:  j.TicketURL,
			Target:     j.Target,
			Instance:   j.Instance,
			Pipeline:   j.Pipeline,
			Status:     string(j.Status),
			Branch:     j.Branch,
			PR:         j.PR,
			LastEvent:  j.LastEvent,
			LastStatus: j.LastStatus,
		}
		if !j.UpdatedAt.IsZero() {
			row.UpdatedAt = j.UpdatedAt.Format(time.RFC3339)
		}
		for _, step := range j.Steps {
			row.Steps = append(row.Steps, BriefJobStep{
				ID:               step.ID,
				Target:           step.Target,
				Instance:         step.Instance,
				Status:           string(step.Status),
				Gate:             step.Gate,
				ApprovalRequired: step.ApprovalRequired,
				ApprovalID:       step.ApprovalID,
				ApprovalStatus:   string(step.ApprovalStatus),
			})
		}
		rows = append(rows, row)
	}
	return rows
}

func briefPipelineRows(jobs []*jobstore.Job) []BriefPipeline {
	byName := map[string]*BriefPipeline{}
	for _, j := range jobs {
		name := strings.TrimSpace(j.Pipeline)
		if name == "" {
			continue
		}
		row := byName[name]
		if row == nil {
			row = &BriefPipeline{Name: name}
			byName[name] = row
		}
		row.Jobs++
		switch j.Status {
		case jobstore.StatusQueued:
			row.Queued++
		case jobstore.StatusRunning:
			row.Running++
		case jobstore.StatusBlocked:
			row.Blocked++
		case jobstore.StatusDone:
			row.Done++
		case jobstore.StatusFailed:
			row.Failed++
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]BriefPipeline, 0, len(names))
	for _, name := range names {
		rows = append(rows, *byName[name])
	}
	return rows
}

func briefMessageRows(messages []*Message) []BriefMessage {
	rows := make([]BriefMessage, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		row := BriefMessage{ID: msg.ID, From: msg.From, Body: msg.Body}
		if !msg.TS.IsZero() {
			row.TS = msg.TS.Format(time.RFC3339)
		}
		rows = append(rows, row)
	}
	return rows
}

func briefChannelRows(root, instance string) ([]BriefChannel, error) {
	store := NewChannelStore(root)
	infos, err := store.List()
	if err != nil {
		return nil, err
	}
	var rows []BriefChannel
	for _, info := range infos {
		subs, err := store.Subscriptions(info.Name)
		if err != nil {
			return nil, err
		}
		sub, ok := subs[instance]
		if !ok {
			continue
		}
		drain, err := store.Drain(context.Background(), info.Name, instance, nil, 0)
		if err != nil {
			return nil, err
		}
		row := BriefChannel{Name: info.Name, Cursor: sub.Cursor, Unread: len(drain.Messages)}
		if len(drain.Messages) > 0 {
			last := drain.Messages[len(drain.Messages)-1]
			row.LatestSeq = last.Seq
			row.LastMessage = last.Body
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

func briefEventRows(events []*LifecycleEvent, instance string, ownedJobs map[string]bool, limit int) []BriefEvent {
	rows := []BriefEvent{}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil {
			continue
		}
		if ev.Instance != instance && !ownedJobs[ev.Job] {
			continue
		}
		row := BriefEvent{
			ID:       ev.ID,
			Action:   ev.Action,
			Instance: ev.Instance,
			Agent:    ev.Agent,
			Job:      ev.Job,
			Ticket:   ev.Ticket,
			Status:   string(ev.Status),
			Message:  ev.Message,
		}
		if !ev.TS.IsZero() {
			row.TS = ev.TS.Format(time.RFC3339)
		}
		rows = append(rows, row)
		if limit > 0 && len(rows) >= limit {
			break
		}
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

func briefFleetRows(teamDir string, topo *topology.Topology, instance string, teams []string) ([]BriefFleetRow, error) {
	scope := map[string]bool{instance: true}
	if topo != nil {
		for _, teamName := range teams {
			team := topo.FindTeam(teamName)
			if team == nil {
				continue
			}
			for _, member := range team.Instances {
				scope[member] = true
			}
		}
	}
	root := DaemonRoot(teamDir)
	metas, err := ListMetadata(root)
	if err != nil {
		return nil, err
	}
	metaByName := map[string]*Metadata{}
	for _, meta := range metas {
		if scope[meta.Instance] {
			metaByName[meta.Instance] = meta
		}
	}
	statuses := briefStatusRows(teamDir, scope)
	for name := range statuses {
		scope[name] = true
	}
	names := make([]string, 0, len(scope))
	for name := range scope {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]BriefFleetRow, 0, len(names))
	for _, name := range names {
		meta := metaByName[name]
		status := statuses[name]
		row := BriefFleetRow{Instance: name}
		if meta != nil {
			row.Agent = meta.Agent
			row.Lifecycle = string(meta.Status)
			row.Job = meta.Job
			row.Ticket = meta.Ticket
			row.Branch = meta.Branch
			row.PR = meta.PR
			row.PID = meta.PID
		}
		if status != nil {
			row.Phase = status.Status.Phase
			if status.Status.Description != "" {
				row.Summary = status.Status.Description
			} else {
				row.Summary = status.Status.LastAction
			}
			if status.Work != nil {
				row.Job = firstNonEmpty(row.Job, status.Work.Job)
				row.Ticket = firstNonEmpty(row.Ticket, status.Work.Ticket)
				row.Branch = firstNonEmpty(row.Branch, status.Work.Branch)
				row.PR = firstNonEmpty(row.PR, status.Work.PR)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func briefStatusRows(teamDir string, scope map[string]bool) map[string]*briefStatusFile {
	out := map[string]*briefStatusFile{}
	stateRoot := filepath.Join(teamDir, "state")
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() || !scope[entry.Name()] {
			continue
		}
		var sf briefStatusFile
		if _, err := toml.DecodeFile(filepath.Join(stateRoot, entry.Name(), "status.toml"), &sf); err != nil {
			continue
		}
		out[entry.Name()] = &sf
	}
	return out
}

func renderBriefJobs(b *strings.Builder, jobs []BriefJob) {
	fmt.Fprintf(b, "\n## Owned Jobs\n\n")
	if len(jobs) == 0 {
		fmt.Fprintf(b, "(none)\n")
		return
	}
	for _, job := range jobs {
		fmt.Fprintf(b, "- %s: status=%s target=%s instance=%s branch=%s pr=%s\n",
			job.ID, job.Status, emptyBriefDash(job.Target), emptyBriefDash(job.Instance), emptyBriefDash(job.Branch), emptyBriefDash(job.PR))
		if job.LastStatus != "" {
			fmt.Fprintf(b, "  Last status: %s\n", job.LastStatus)
		}
		for _, step := range job.Steps {
			fmt.Fprintf(b, "  - step %s: status=%s target=%s instance=%s\n", step.ID, step.Status, emptyBriefDash(step.Target), emptyBriefDash(step.Instance))
		}
	}
}

func renderBriefPipelines(b *strings.Builder, pipelines []BriefPipeline) {
	fmt.Fprintf(b, "\n## Pipeline State\n\n")
	if len(pipelines) == 0 {
		fmt.Fprintf(b, "(none)\n")
		return
	}
	for _, p := range pipelines {
		fmt.Fprintf(b, "- %s: jobs=%d queued=%d running=%d blocked=%d done=%d failed=%d\n", p.Name, p.Jobs, p.Queued, p.Running, p.Blocked, p.Done, p.Failed)
	}
}

func renderBriefMessages(b *strings.Builder, title string, messages []BriefMessage) {
	fmt.Fprintf(b, "\n## %s\n\n", title)
	if len(messages) == 0 {
		fmt.Fprintf(b, "(none)\n")
		return
	}
	for _, msg := range messages {
		fmt.Fprintf(b, "- %s from %s at %s: %s\n", msg.ID, emptyBriefDash(msg.From), emptyBriefDash(msg.TS), oneLine(msg.Body))
	}
}

func renderBriefChannels(b *strings.Builder, channels []BriefChannel) {
	fmt.Fprintf(b, "\n## Channel Cursors\n\n")
	if len(channels) == 0 {
		fmt.Fprintf(b, "(none)\n")
		return
	}
	for _, ch := range channels {
		fmt.Fprintf(b, "- %s: cursor=%d unread=%d latest=%d", ch.Name, ch.Cursor, ch.Unread, ch.LatestSeq)
		if ch.LastMessage != "" {
			fmt.Fprintf(b, " last=%q", oneLine(ch.LastMessage))
		}
		fmt.Fprintln(b)
	}
}

func renderBriefEvents(b *strings.Builder, events []BriefEvent) {
	fmt.Fprintf(b, "\n## Recent Lifecycle Events\n\n")
	if len(events) == 0 {
		fmt.Fprintf(b, "(none)\n")
		return
	}
	for _, ev := range events {
		fmt.Fprintf(b, "- %s %s instance=%s job=%s status=%s: %s\n", emptyBriefDash(ev.TS), ev.Action, emptyBriefDash(ev.Instance), emptyBriefDash(ev.Job), emptyBriefDash(ev.Status), oneLine(ev.Message))
	}
}

func renderBriefFleet(b *strings.Builder, rows []BriefFleetRow) {
	fmt.Fprintf(b, "\n## Fleet Snapshot\n\n")
	if len(rows) == 0 {
		fmt.Fprintf(b, "(none)\n")
		return
	}
	for _, row := range rows {
		fmt.Fprintf(b, "- %s: agent=%s lifecycle=%s phase=%s job=%s pid=%d %s\n", row.Instance, emptyBriefDash(row.Agent), emptyBriefDash(row.Lifecycle), emptyBriefDash(row.Phase), emptyBriefDash(row.Job), row.PID, oneLine(row.Summary))
	}
}

func emptyBriefDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func oneLine(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 240 {
		return value[:237] + "..."
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
