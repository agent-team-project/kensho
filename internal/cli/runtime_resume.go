package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	texttemplate "text/template"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/runtimebin"
	"github.com/spf13/cobra"
)

type runtimeResumePlan struct {
	Instance              string `json:"instance"`
	Job                   string `json:"job,omitempty"`
	Agent                 string `json:"agent,omitempty"`
	Runtime               string `json:"runtime"`
	RuntimeBinary         string `json:"runtime_binary"`
	Status                string `json:"status"`
	PID                   int    `json:"pid,omitempty"`
	SessionID             string `json:"session_id,omitempty"`
	ManagedResume         bool   `json:"managed_resume"`
	CanManagedResume      bool   `json:"can_managed_resume"`
	DirectResume          bool   `json:"direct_resume"`
	RecommendedAction     string `json:"recommended_action,omitempty"`
	RecommendedCommand    string `json:"recommended_command,omitempty"`
	ResumeCommand         string `json:"resume_command,omitempty"`
	StartCommand          string `json:"start_command,omitempty"`
	AttachCommand         string `json:"attach_command,omitempty"`
	LogsCommand           string `json:"logs_command,omitempty"`
	LastMessageCommand    string `json:"last_message_command,omitempty"`
	JobAttachCommand      string `json:"job_attach_command,omitempty"`
	JobLogsCommand        string `json:"job_logs_command,omitempty"`
	JobLastMessageCommand string `json:"job_last_message_command,omitempty"`
	Detail                string `json:"detail,omitempty"`
}

type runtimeResumeSummary struct {
	Total            int            `json:"total"`
	Actions          map[string]int `json:"actions,omitempty"`
	Runtimes         map[string]int `json:"runtimes,omitempty"`
	Statuses         map[string]int `json:"statuses,omitempty"`
	ManagedResume    int            `json:"managed_resume"`
	CanManagedResume int            `json:"can_managed_resume"`
	DirectResume     int            `json:"direct_resume"`
}

func newRuntimeResumePlanCmd() *cobra.Command {
	var (
		target        string
		jobID         string
		statusFilters []string
		runtimeFilter []string
		actionFilters []string
		summary       bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "resume-plan [<instance>...]",
		Short: "Show runtime resume and fallback commands for daemon metadata.",
		Long: "Show runtime resume and fallback commands for daemon metadata without contacting the daemon. " +
			"This explains whether an instance can be resumed through agent-team, which direct runtime command is available, and which log commands are safest for runtimes without managed resume.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime resume-plan: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime resume-plan: --summary cannot be combined with --format.")
				return exitErr(2)
			}
			if strings.TrimSpace(jobID) != "" && len(args) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team runtime resume-plan: --job cannot be combined with instance names.")
				return exitErr(2)
			}
			tmpl, err := parseRuntimeResumePlanFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime resume-plan: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, effectiveRepoTarget(cmd, target))
			if err != nil {
				return err
			}
			plans, err := collectRuntimeResumePlans(teamDir, args, jobID, statusFilters, runtimeFilter, actionFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team runtime resume-plan: %v\n", err)
				return exitErr(1)
			}
			if summary {
				out := summarizeRuntimeResumePlans(plans)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				renderRuntimeResumeSummary(cmd.OutOrStdout(), out)
				return nil
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(plans)
			}
			if tmpl != nil {
				return renderRuntimeResumePlanFormat(cmd.OutOrStdout(), plans, tmpl)
			}
			renderRuntimeResumePlans(cmd.OutOrStdout(), plans)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&jobID, "job", "", "Select the instance recorded on or associated with this job id.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilter, "runtime", nil, "Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching resume plans by recommended action, runtime, and status.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.")
	return cmd
}

func collectRuntimeResumePlans(teamDir string, instances []string, jobID string, statusFilters []string, runtimeFilters []string, actionFilters []string) ([]runtimeResumePlan, error) {
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	byInstance := map[string]*daemon.Metadata{}
	for _, meta := range metas {
		if meta == nil || strings.TrimSpace(meta.Instance) == "" {
			continue
		}
		byInstance[meta.Instance] = meta
	}

	var selected []*daemon.Metadata
	selectedJobID := ""
	if id := strings.TrimSpace(jobID); id != "" {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		selectedJobID = j.ID
		selected = metadataForResumePlanJob(metas, byInstance, j)
		if len(selected) == 0 {
			return nil, fmt.Errorf("job %q has no daemon metadata; dispatch or adopt it first", j.ID)
		}
	} else if len(instances) > 0 {
		for _, instance := range instances {
			name := strings.TrimSpace(instance)
			if name == "" {
				continue
			}
			meta := byInstance[name]
			if meta == nil {
				return nil, fmt.Errorf("instance %q has no daemon metadata", name)
			}
			selected = append(selected, meta)
		}
	} else {
		selected = append(selected, metas...)
	}

	statusSet, err := parseRuntimeResumeStatusFilter(statusFilters)
	if err != nil {
		return nil, err
	}
	runtimeSet, err := parseRuntimeResumeRuntimeFilter(runtimeFilters)
	if err != nil {
		return nil, err
	}
	actionSet, err := parseRuntimeResumeActionFilter(actionFilters)
	if err != nil {
		return nil, err
	}

	plans := make([]runtimeResumePlan, 0, len(selected))
	for _, meta := range selected {
		if meta == nil {
			continue
		}
		if len(statusSet) > 0 && !statusSet[strings.ToLower(strings.TrimSpace(string(meta.Status)))] {
			continue
		}
		runtimeKind := lifecycleMetadataRuntimeKind(meta)
		if len(runtimeSet) > 0 && !runtimeSet[string(runtimeKind)] {
			continue
		}
		plan := runtimeResumePlanFromMetadata(meta)
		if selectedJobID != "" && strings.TrimSpace(plan.Job) == "" {
			plan = runtimeResumePlanWithJobCommands(plan, selectedJobID)
		}
		if len(actionSet) > 0 && !actionSet[plan.RecommendedAction] {
			continue
		}
		plans = append(plans, plan)
	}
	sort.SliceStable(plans, func(i, j int) bool {
		return plans[i].Instance < plans[j].Instance
	})
	return plans, nil
}

func metadataForResumePlanJob(metas []*daemon.Metadata, byInstance map[string]*daemon.Metadata, j *job.Job) []*daemon.Metadata {
	if j == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []*daemon.Metadata
	if instance := strings.TrimSpace(j.Instance); instance != "" {
		if meta := byInstance[instance]; meta != nil {
			out = append(out, meta)
			seen[meta.Instance] = true
		}
	}
	id := job.NormalizeID(j.ID)
	for _, meta := range metas {
		if meta == nil || seen[meta.Instance] || job.NormalizeID(meta.Job) != id {
			continue
		}
		out = append(out, meta)
		seen[meta.Instance] = true
	}
	return out
}

func runtimeResumePlanFromMetadata(meta *daemon.Metadata) runtimeResumePlan {
	runtimeKind := lifecycleMetadataRuntimeKind(meta)
	bin := attachRuntimeBinaryFromMetadata(meta)
	sessionID := strings.TrimSpace(meta.SessionID)
	managedResume := lifecycleMetadataSupportsManagedResume(meta)
	canManagedResume := managedResume && sessionID != ""
	directResume := sessionID != ""
	instance := strings.TrimSpace(meta.Instance)

	plan := runtimeResumePlan{
		Instance:         instance,
		Job:              strings.TrimSpace(meta.Job),
		Agent:            strings.TrimSpace(meta.Agent),
		Runtime:          string(runtimeKind),
		RuntimeBinary:    bin,
		Status:           string(meta.Status),
		PID:              meta.PID,
		SessionID:        sessionID,
		ManagedResume:    managedResume,
		CanManagedResume: canManagedResume,
		DirectResume:     directResume,
	}
	if directResume {
		plan.ResumeCommand = attachResumeCommand(meta, bin)
	}
	if instance != "" {
		plan.AttachCommand = "agent-team attach " + instance + " --dry-run"
		plan.LogsCommand = "agent-team logs " + instance + " --follow"
		if runtimeKind == runtimebin.KindCodex {
			plan.LastMessageCommand = "agent-team logs " + instance + " --last-message"
		}
	}
	if canManagedResume && meta.Status != daemon.StatusRunning {
		plan.StartCommand = "agent-team start " + instance
	}
	if plan.Job != "" {
		plan = runtimeResumePlanWithJobCommands(plan, plan.Job)
	}
	plan.RecommendedCommand, plan.RecommendedAction = runtimeResumeRecommendation(meta, plan)
	plan.Detail = runtimeResumePlanDetail(meta, plan)
	return plan
}

func runtimeResumePlanWithJobCommands(plan runtimeResumePlan, jobID string) runtimeResumePlan {
	id := job.NormalizeID(jobID)
	if id == "" {
		return plan
	}
	plan.Job = id
	plan.JobAttachCommand = "agent-team job attach " + id + " --dry-run"
	plan.JobLogsCommand = "agent-team job logs " + id + " --follow"
	if plan.Runtime == string(runtimebin.KindCodex) {
		plan.JobLastMessageCommand = "agent-team job logs " + id + " --last-message"
	}
	return plan
}

func runtimeResumeRecommendation(meta *daemon.Metadata, plan runtimeResumePlan) (string, string) {
	if !plan.DirectResume {
		return plan.LogsCommand, "logs"
	}
	if plan.CanManagedResume {
		if strings.TrimSpace(plan.StartCommand) != "" {
			return plan.StartCommand, "start"
		}
		if strings.TrimSpace(plan.AttachCommand) != "" {
			return plan.AttachCommand, "attach"
		}
	}
	if lifecycleMetadataRuntimeKind(meta) == runtimebin.KindCodex && strings.TrimSpace(plan.ResumeCommand) != "" {
		return plan.ResumeCommand, "resume"
	}
	if strings.TrimSpace(plan.LogsCommand) != "" {
		return plan.LogsCommand, "logs"
	}
	if strings.TrimSpace(plan.ResumeCommand) != "" {
		return plan.ResumeCommand, "resume"
	}
	return "", ""
}

func runtimeResumePlanDetail(meta *daemon.Metadata, plan runtimeResumePlan) string {
	if !plan.DirectResume {
		return "no session id recorded; follow logs or create a new run"
	}
	if !plan.ManagedResume {
		return lifecycleUnsupportedResumeDetailForInstance(meta, plan.Instance)
	}
	if meta.Status == daemon.StatusRunning {
		return "managed attach can stop the daemon child, open the session, and resume daemon ownership afterward"
	}
	return "managed start can resume the recorded runtime session under daemon ownership"
}

func parseRuntimeResumeStatusFilter(raw []string) (map[string]bool, error) {
	values := splitRuntimeResumeCSVValues(raw)
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		switch daemon.Status(key) {
		case daemon.StatusRunning, daemon.StatusStopped, daemon.StatusExited, daemon.StatusCrashed:
			out[key] = true
		default:
			return nil, fmt.Errorf("--status accepts running, stopped, exited, or crashed, got %q", value)
		}
	}
	return out, nil
}

func parseRuntimeResumeRuntimeFilter(raw []string) (map[string]bool, error) {
	values := splitRuntimeResumeCSVValues(raw)
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	for _, value := range values {
		kind, err := runtimebin.ParseKind(value)
		if err != nil {
			return nil, fmt.Errorf("--runtime must be %q or %q", runtimebin.KindClaude, runtimebin.KindCodex)
		}
		out[string(kind)] = true
	}
	return out, nil
}

func parseRuntimeResumeActionFilter(raw []string) (map[string]bool, error) {
	values := splitRuntimeResumeCSVValues(raw)
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		switch key {
		case "all":
			return nil, nil
		case "log":
			key = "logs"
		}
		switch key {
		case "start", "attach", "resume", "logs":
			out[key] = true
		default:
			return nil, fmt.Errorf("--action accepts start, attach, resume, logs, or all, got %q", value)
		}
	}
	return out, nil
}

func splitRuntimeResumeCSVValues(raw []string) []string {
	var out []string
	for _, chunk := range raw {
		for _, value := range strings.Split(chunk, ",") {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

func renderRuntimeResumePlans(w fmtWriter, plans []runtimeResumePlan) {
	if len(plans) == 0 {
		fmt.Fprintln(w, "(no runtime metadata)")
		return
	}
	for i, plan := range plans {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "instance:                 %s\n", plan.Instance)
		if plan.Job != "" {
			fmt.Fprintf(w, "job:                      %s\n", plan.Job)
		}
		if plan.Agent != "" {
			fmt.Fprintf(w, "agent:                    %s\n", plan.Agent)
		}
		fmt.Fprintf(w, "runtime:                  %s\n", plan.Runtime)
		fmt.Fprintf(w, "runtime_binary:           %s\n", plan.RuntimeBinary)
		fmt.Fprintf(w, "status:                   %s\n", plan.Status)
		if plan.PID != 0 {
			fmt.Fprintf(w, "pid:                      %d\n", plan.PID)
		}
		if plan.SessionID != "" {
			fmt.Fprintf(w, "session_id:               %s\n", plan.SessionID)
		}
		fmt.Fprintf(w, "managed_resume:           %s\n", runtimeYesNo(plan.ManagedResume))
		fmt.Fprintf(w, "can_managed_resume:       %s\n", runtimeYesNo(plan.CanManagedResume))
		fmt.Fprintf(w, "direct_resume:            %s\n", runtimeYesNo(plan.DirectResume))
		if plan.RecommendedAction != "" {
			fmt.Fprintf(w, "recommended_action:       %s\n", plan.RecommendedAction)
		}
		if plan.RecommendedCommand != "" {
			fmt.Fprintf(w, "recommended_command:      %s\n", plan.RecommendedCommand)
		}
		if plan.ResumeCommand != "" {
			fmt.Fprintf(w, "resume_command:           %s\n", plan.ResumeCommand)
		}
		if plan.StartCommand != "" {
			fmt.Fprintf(w, "start_command:            %s\n", plan.StartCommand)
		}
		if plan.AttachCommand != "" {
			fmt.Fprintf(w, "attach_command:           %s\n", plan.AttachCommand)
		}
		if plan.LogsCommand != "" {
			fmt.Fprintf(w, "logs_command:             %s\n", plan.LogsCommand)
		}
		if plan.LastMessageCommand != "" {
			fmt.Fprintf(w, "last_message_command:     %s\n", plan.LastMessageCommand)
		}
		if plan.JobAttachCommand != "" {
			fmt.Fprintf(w, "job_attach_command:       %s\n", plan.JobAttachCommand)
		}
		if plan.JobLogsCommand != "" {
			fmt.Fprintf(w, "job_logs_command:         %s\n", plan.JobLogsCommand)
		}
		if plan.JobLastMessageCommand != "" {
			fmt.Fprintf(w, "job_last_message_command: %s\n", plan.JobLastMessageCommand)
		}
		if plan.Detail != "" {
			fmt.Fprintf(w, "detail:                   %s\n", plan.Detail)
		}
	}
}

func summarizeRuntimeResumePlans(plans []runtimeResumePlan) runtimeResumeSummary {
	out := runtimeResumeSummary{
		Total:    len(plans),
		Actions:  map[string]int{},
		Runtimes: map[string]int{},
		Statuses: map[string]int{},
	}
	for _, plan := range plans {
		if action := strings.TrimSpace(plan.RecommendedAction); action != "" {
			out.Actions[action]++
		}
		if runtime := strings.TrimSpace(plan.Runtime); runtime != "" {
			out.Runtimes[runtime]++
		}
		if status := strings.TrimSpace(plan.Status); status != "" {
			out.Statuses[status]++
		}
		if plan.ManagedResume {
			out.ManagedResume++
		}
		if plan.CanManagedResume {
			out.CanManagedResume++
		}
		if plan.DirectResume {
			out.DirectResume++
		}
	}
	return out
}

func renderRuntimeResumeSummary(w fmtWriter, summary runtimeResumeSummary) {
	fmt.Fprintf(w, "total:              %d\n", summary.Total)
	fmt.Fprintf(w, "actions:            %s\n", runtimeResumeCountMapText(summary.Actions, []string{"start", "attach", "resume", "logs"}))
	fmt.Fprintf(w, "runtimes:           %s\n", runtimeResumeCountMapText(summary.Runtimes, []string{string(runtimebin.KindClaude), string(runtimebin.KindCodex)}))
	fmt.Fprintf(w, "statuses:           %s\n", runtimeResumeCountMapText(summary.Statuses, []string{string(daemon.StatusRunning), string(daemon.StatusStopped), string(daemon.StatusExited), string(daemon.StatusCrashed)}))
	fmt.Fprintf(w, "managed_resume:     %d\n", summary.ManagedResume)
	fmt.Fprintf(w, "can_managed_resume: %d\n", summary.CanManagedResume)
	fmt.Fprintf(w, "direct_resume:      %d\n", summary.DirectResume)
}

func runtimeResumeCountMapText(counts map[string]int, preferred []string) string {
	if len(counts) == 0 {
		return "-"
	}
	seen := map[string]bool{}
	parts := []string{}
	for _, key := range preferred {
		if count := counts[key]; count > 0 {
			parts = append(parts, key+"="+strconv.Itoa(count))
			seen[key] = true
		}
	}
	extras := make([]string, 0, len(counts))
	for key, count := range counts {
		if count <= 0 || seen[key] {
			continue
		}
		extras = append(extras, key)
	}
	sort.Strings(extras)
	for _, key := range extras {
		parts = append(parts, key+"="+strconv.Itoa(counts[key]))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func parseRuntimeResumePlanFormat(format string) (*texttemplate.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := texttemplate.New("runtime-resume-plan-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderRuntimeResumePlanFormat(w fmtWriter, plans []runtimeResumePlan, tmpl *texttemplate.Template) error {
	for _, plan := range plans {
		if err := tmpl.Execute(w, plan); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}
