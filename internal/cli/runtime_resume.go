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
	Pipeline              string `json:"pipeline,omitempty"`
	StepID                string `json:"step_id,omitempty"`
	Agent                 string `json:"agent,omitempty"`
	Runtime               string `json:"runtime"`
	RuntimeBinary         string `json:"runtime_binary"`
	Status                string `json:"status"`
	PID                   int    `json:"pid,omitempty"`
	SessionID             string `json:"session_id,omitempty"`
	Stale                 bool   `json:"stale,omitempty"`
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
	Stale            int            `json:"stale"`
	Unhealthy        int            `json:"unhealthy"`
}

type runtimeResumeCommandOptions struct {
	Target      string
	TargetFlag  string
	TargetSet   bool
	LastMessage bool
}

type runtimeResumeCapabilityFilters struct {
	ManagedOnly    bool
	CanManagedOnly bool
	DirectOnly     bool
}

func newResumePlanCmd() *cobra.Command {
	return newRuntimeResumePlanCommand(runtimeResumePlanCommandConfig{
		Use:       "resume-plan [<instance>...]",
		ErrorName: "agent-team resume-plan",
		Long: "Show runtime resume and fallback commands for daemon metadata without contacting the daemon. " +
			"This is a shorter alias for `agent-team runtime resume-plan`.",
		RepoFlag:    true,
		SummaryHelp: "Summarize matching resume plans by recommended action, runtime, and status.",
	})
}

func newRuntimeResumePlanCmd() *cobra.Command {
	return newRuntimeResumePlanCommand(runtimeResumePlanCommandConfig{
		Use:       "resume-plan [<instance>...]",
		ErrorName: "agent-team runtime resume-plan",
		Long: "Show runtime resume and fallback commands for daemon metadata without contacting the daemon. " +
			"This explains whether an instance can be resumed through agent-team, which direct runtime command is available, and which log commands are safest for runtimes without managed resume.",
		RepoFlag:    false,
		SummaryHelp: "Summarize matching resume plans by recommended action, runtime, and status.",
	})
}

type runtimeResumePlanCommandConfig struct {
	Use         string
	ErrorName   string
	Long        string
	RepoFlag    bool
	SummaryHelp string
}

func newRuntimeResumePlanCommand(cfg runtimeResumePlanCommandConfig) *cobra.Command {
	var (
		target         string
		jobID          string
		stepID         string
		statusFilters  []string
		runtimeFilter  []string
		actionFilters  []string
		sortBy         string
		limit          int
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		managedOnly    bool
		canManagedOnly bool
		directOnly     bool
		summary        bool
		commandsOnly   bool
		lastMessage    bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   cfg.Use,
		Short: "Show runtime resume and fallback commands for daemon metadata.",
		Long:  cfg.Long,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", cfg.ErrorName)
				return exitErr(2)
			}
			if commandsOnly && jsonOut {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --commands cannot be combined with --json.\n", cfg.ErrorName)
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --summary cannot be combined with --format.\n", cfg.ErrorName)
				return exitErr(2)
			}
			if summary && commandsOnly {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --summary cannot be combined with --commands.\n", cfg.ErrorName)
				return exitErr(2)
			}
			if commandsOnly && format != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --commands cannot be combined with --format.\n", cfg.ErrorName)
				return exitErr(2)
			}
			if strings.TrimSpace(jobID) != "" && len(args) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --job cannot be combined with instance names.\n", cfg.ErrorName)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --limit must be >= 0.\n", cfg.ErrorName)
				return exitErr(2)
			}
			if summary && limit > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: --limit cannot be combined with --summary.\n", cfg.ErrorName)
				return exitErr(2)
			}
			sortMode, err := parseRuntimeResumeSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", cfg.ErrorName, err)
				return exitErr(2)
			}
			tmpl, err := parseRuntimeResumePlanFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", cfg.ErrorName, err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			capabilityFilters := runtimeResumeCapabilityFilters{
				ManagedOnly:    managedOnly,
				CanManagedOnly: canManagedOnly,
				DirectOnly:     directOnly,
			}
			plans, err := collectRuntimeResumePlans(teamDir, args, jobID, stepID, statusFilters, runtimeFilter, actionFilters, staleOnly || runtimeStale, unhealthyOnly, capabilityFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", cfg.ErrorName, err)
				return exitErr(1)
			}
			sortRuntimeResumePlans(plans, sortMode)
			if lastMessage {
				preferRuntimeResumeLastMessages(plans)
			}
			if summary {
				out := summarizeRuntimeResumePlans(plans)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				renderRuntimeResumeSummary(cmd.OutOrStdout(), out)
				return nil
			}
			plans = limitRuntimeResumePlans(plans, limit)
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(plans)
			}
			if commandsOnly {
				opts := runtimeResumeCommandOptionsFromFlag(cmd, target, runtimeResumePlanRepoFlag(cfg))
				opts.LastMessage = lastMessage
				renderRuntimeResumePlanCommands(cmd.OutOrStdout(), plans, opts)
				return nil
			}
			if tmpl != nil {
				return renderRuntimeResumePlanFormat(cmd.OutOrStdout(), plans, tmpl)
			}
			renderRuntimeResumePlans(cmd.OutOrStdout(), plans)
			return nil
		},
	}
	if cfg.RepoFlag {
		cmd.Flags().StringVar(&target, "repo", cwd, repoFlagHelp)
	} else {
		cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	}
	cmd.Flags().StringVar(&jobID, "job", "", "Select the instance recorded on or associated with this job id.")
	cmd.Flags().StringVar(&stepID, "step", "", "Only include plans for this pipeline step id.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilter, "runtime", nil, "Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&sortBy, "sort", "instance", "Sort plans before rendering by instance, action, runtime, status, stale, job, pipeline, step, or agent.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit plans after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Only include running metadata whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only include crashed or stale running metadata.")
	cmd.Flags().BoolVar(&managedOnly, "managed", false, "Only include runtimes whose adapter supports daemon-managed resume.")
	cmd.Flags().BoolVar(&canManagedOnly, "can-managed", false, "Only include runtimes with enough session metadata for daemon-managed resume.")
	cmd.Flags().BoolVar(&directOnly, "direct", false, "Only include runtimes with a direct runtime resume command.")
	cmd.Flags().BoolVar(&summary, "summary", false, cfg.SummaryHelp)
	cmd.Flags().BoolVar(&commandsOnly, "commands", false, "Print only recommended commands, one per line, after filtering, sorting, and limiting. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&lastMessage, "last-message", false, "For Codex log fallbacks, recommend the clean last-message sidecar instead of following raw logs.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.")
	return cmd
}

func newJobResumePlanCmd() *cobra.Command {
	var (
		repo           string
		stepID         string
		statusFilters  []string
		runtimeFilter  []string
		actionFilters  []string
		sortBy         string
		limit          int
		staleOnly      bool
		runtimeStale   bool
		unhealthyOnly  bool
		managedOnly    bool
		canManagedOnly bool
		directOnly     bool
		summary        bool
		commandsOnly   bool
		lastMessage    bool
		jsonOut        bool
		format         string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "resume-plan <job-id>",
		Short: "Show runtime resume and fallback commands for one job.",
		Long: "Show runtime resume and fallback commands for daemon metadata owned by one durable job. " +
			"This is the job-scoped form of `agent-team runtime resume-plan --job <job-id>`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job resume-plan: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commandsOnly && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job resume-plan: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if summary && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job resume-plan: --summary cannot be combined with --format.")
				return exitErr(2)
			}
			if summary && commandsOnly {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job resume-plan: --summary cannot be combined with --commands.")
				return exitErr(2)
			}
			if commandsOnly && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job resume-plan: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job resume-plan: --limit must be >= 0.")
				return exitErr(2)
			}
			if summary && limit > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job resume-plan: --limit cannot be combined with --summary.")
				return exitErr(2)
			}
			sortMode, err := parseRuntimeResumeSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job resume-plan: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseRuntimeResumePlanFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job resume-plan: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			capabilityFilters := runtimeResumeCapabilityFilters{
				ManagedOnly:    managedOnly,
				CanManagedOnly: canManagedOnly,
				DirectOnly:     directOnly,
			}
			plans, err := collectRuntimeResumePlans(teamDir, nil, args[0], stepID, statusFilters, runtimeFilter, actionFilters, staleOnly || runtimeStale, unhealthyOnly, capabilityFilters)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job resume-plan: %v\n", err)
				return exitErr(1)
			}
			sortRuntimeResumePlans(plans, sortMode)
			if lastMessage {
				preferRuntimeResumeLastMessages(plans)
			}
			if summary {
				out := summarizeRuntimeResumePlans(plans)
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
				}
				renderRuntimeResumeSummary(cmd.OutOrStdout(), out)
				return nil
			}
			plans = limitRuntimeResumePlans(plans, limit)
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(plans)
			}
			if commandsOnly {
				opts := runtimeResumeCommandOptionsFromFlag(cmd, repo, "repo")
				opts.LastMessage = lastMessage
				renderRuntimeResumePlanCommands(cmd.OutOrStdout(), plans, opts)
				return nil
			}
			if tmpl != nil {
				return renderRuntimeResumePlanFormat(cmd.OutOrStdout(), plans, tmpl)
			}
			renderRuntimeResumePlans(cmd.OutOrStdout(), plans)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Only include plans for this pipeline step id.")
	cmd.Flags().StringSliceVar(&statusFilters, "status", nil, "Only include metadata with this status: running, stopped, exited, or crashed. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&runtimeFilter, "runtime", nil, "Only include metadata for this runtime: claude or codex. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actionFilters, "action", nil, "Only include plans whose recommended action is start, attach, resume, or logs. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&sortBy, "sort", "instance", "Sort plans before rendering by instance, action, runtime, status, stale, job, pipeline, step, or agent.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit plans after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "Only include running metadata whose recorded runtime PID is no longer live. Compatibility alias for --runtime-stale.")
	cmd.Flags().BoolVar(&runtimeStale, "runtime-stale", false, "Only include running metadata whose recorded runtime PID is no longer live.")
	cmd.Flags().BoolVar(&unhealthyOnly, "unhealthy", false, "Only include crashed or stale running metadata.")
	cmd.Flags().BoolVar(&managedOnly, "managed", false, "Only include runtimes whose adapter supports daemon-managed resume.")
	cmd.Flags().BoolVar(&canManagedOnly, "can-managed", false, "Only include runtimes with enough session metadata for daemon-managed resume.")
	cmd.Flags().BoolVar(&directOnly, "direct", false, "Only include runtimes with a direct runtime resume command.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Summarize matching resume plans by recommended action, runtime, and status.")
	cmd.Flags().BoolVar(&commandsOnly, "commands", false, "Print only recommended commands, one per line, after filtering, sorting, and limiting. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&lastMessage, "last-message", false, "For Codex log fallbacks, recommend the clean last-message sidecar instead of following raw logs.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each plan with a Go template, e.g. '{{.Instance}} {{.RecommendedAction}} {{.RecommendedCommand}}'.")
	return cmd
}

func collectRuntimeResumePlans(teamDir string, instances []string, jobID string, stepFilter string, statusFilters []string, runtimeFilters []string, actionFilters []string, staleOnly bool, unhealthyOnly bool, capabilityFilters runtimeResumeCapabilityFilters) ([]runtimeResumePlan, error) {
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
	var selectedJob *job.Job
	if id := strings.TrimSpace(jobID); id != "" {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		selectedJob = j
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
	jobCache := map[string]*job.Job{}
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
		if selectedJob != nil {
			plan = runtimeResumePlanWithJobContext(plan, selectedJob)
		} else {
			plan = runtimeResumePlanWithJobContextFromDisk(teamDir, plan, jobCache)
		}
		plan.Detail = runtimeResumePlanDetail(meta, plan)
		if len(actionSet) > 0 && !actionSet[plan.RecommendedAction] {
			continue
		}
		if !runtimeResumePlanMatchesStep(plan, stepFilter) {
			continue
		}
		if staleOnly && !plan.Stale {
			continue
		}
		if unhealthyOnly && !runtimeResumePlanUnhealthy(plan) {
			continue
		}
		if !capabilityFilters.Matches(plan) {
			continue
		}
		plans = append(plans, plan)
	}
	sort.SliceStable(plans, func(i, j int) bool {
		return plans[i].Instance < plans[j].Instance
	})
	return plans, nil
}

func (filters runtimeResumeCapabilityFilters) Matches(plan runtimeResumePlan) bool {
	if filters.ManagedOnly && !plan.ManagedResume {
		return false
	}
	if filters.CanManagedOnly && !plan.CanManagedResume {
		return false
	}
	if filters.DirectOnly && !plan.DirectResume {
		return false
	}
	return true
}

func runtimeResumePlanUnhealthy(plan runtimeResumePlan) bool {
	return plan.Stale || strings.EqualFold(strings.TrimSpace(plan.Status), string(daemon.StatusCrashed))
}

func metadataForResumePlanJob(metas []*daemon.Metadata, byInstance map[string]*daemon.Metadata, j *job.Job) []*daemon.Metadata {
	if j == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []*daemon.Metadata
	addInstance := func(instance string) {
		instance = strings.TrimSpace(instance)
		if instance == "" {
			return
		}
		meta := byInstance[instance]
		if meta == nil || seen[meta.Instance] {
			return
		}
		out = append(out, meta)
		seen[meta.Instance] = true
	}
	if instance := strings.TrimSpace(j.Instance); instance != "" {
		addInstance(instance)
	}
	for _, step := range j.Steps {
		addInstance(step.Instance)
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
	stale := runtimeResumeMetadataIsStale(meta)

	plan := runtimeResumePlan{
		Instance:         instance,
		Job:              strings.TrimSpace(meta.Job),
		Agent:            strings.TrimSpace(meta.Agent),
		Runtime:          string(runtimeKind),
		RuntimeBinary:    bin,
		Status:           string(meta.Status),
		PID:              meta.PID,
		SessionID:        sessionID,
		Stale:            stale,
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
	if canManagedResume && (meta.Status != daemon.StatusRunning || stale) {
		plan.StartCommand = "agent-team start " + instance
	}
	if plan.Job != "" {
		plan = runtimeResumePlanWithJobCommands(plan, plan.Job)
	}
	plan = runtimeResumePlanWithRecommendation(plan)
	plan.Detail = runtimeResumePlanDetail(meta, plan)
	return plan
}

func runtimeResumeMetadataIsStale(meta *daemon.Metadata) bool {
	if meta == nil || meta.Status != daemon.StatusRunning {
		return false
	}
	if strings.TrimSpace(meta.Runtime) == "" && strings.TrimSpace(meta.RuntimeBinary) == "" {
		return false
	}
	return meta.PID > 0 && !daemon.PidLiveCheck(meta.PID)
}

func runtimeResumePlanWithJobCommands(plan runtimeResumePlan, jobID string) runtimeResumePlan {
	id := job.NormalizeID(jobID)
	if id == "" {
		return plan
	}
	stepFlag := jobStepCommandFlag(plan.StepID)
	plan.Job = id
	plan.JobAttachCommand = "agent-team job attach " + id + stepFlag + " --dry-run"
	plan.JobLogsCommand = "agent-team job logs " + id + stepFlag + " --follow"
	if plan.Runtime == string(runtimebin.KindCodex) {
		plan.JobLastMessageCommand = "agent-team job logs " + id + stepFlag + " --last-message"
	}
	return plan
}

func runtimeResumePlanWithJobContext(plan runtimeResumePlan, j *job.Job) runtimeResumePlan {
	if j == nil {
		return plan
	}
	if pipeline := strings.TrimSpace(j.Pipeline); pipeline != "" {
		plan.Pipeline = pipeline
	}
	if step := jobStepForRuntimeResumePlan(j, plan.Instance); step != nil {
		plan.StepID = strings.TrimSpace(step.ID)
		if strings.TrimSpace(plan.Agent) == "" {
			plan.Agent = strings.TrimSpace(step.Target)
		}
	}
	if id := job.NormalizeID(j.ID); id != "" {
		plan = runtimeResumePlanWithJobCommands(plan, id)
	}
	plan = runtimeResumePlanWithRecommendation(plan)
	return plan
}

func runtimeResumePlanWithJobContextFromDisk(teamDir string, plan runtimeResumePlan, cache map[string]*job.Job) runtimeResumePlan {
	id := job.IDFromInput(plan.Job)
	if id == "" {
		return plan
	}
	if cache == nil {
		cache = map[string]*job.Job{}
	}
	j, ok := cache[id]
	if !ok {
		var err error
		j, err = job.Read(teamDir, id)
		if err != nil {
			cache[id] = nil
			return plan
		}
		cache[id] = j
	}
	return runtimeResumePlanWithJobContext(plan, j)
}

func jobStepForRuntimeResumePlan(j *job.Job, instance string) *job.Step {
	if j == nil {
		return nil
	}
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return nil
	}
	for i := range j.Steps {
		if strings.TrimSpace(j.Steps[i].Instance) == instance {
			return &j.Steps[i]
		}
	}
	if strings.TrimSpace(j.Instance) != instance {
		return nil
	}
	var running *job.Step
	for i := range j.Steps {
		if j.Steps[i].Status != job.StatusRunning {
			continue
		}
		if running != nil {
			return nil
		}
		running = &j.Steps[i]
	}
	return running
}

func runtimeResumePlanMatchesStep(plan runtimeResumePlan, stepFilter string) bool {
	stepFilter = strings.TrimSpace(stepFilter)
	if stepFilter == "" {
		return true
	}
	return strings.TrimSpace(plan.StepID) == stepFilter
}

func runtimeResumePlanWithRecommendation(plan runtimeResumePlan) runtimeResumePlan {
	plan.RecommendedCommand, plan.RecommendedAction = runtimeResumeRecommendation(plan)
	return plan
}

func runtimeResumeRecommendation(plan runtimeResumePlan) (string, string) {
	if !plan.DirectResume {
		if strings.TrimSpace(plan.JobLogsCommand) != "" {
			return plan.JobLogsCommand, "logs"
		}
		return plan.LogsCommand, "logs"
	}
	if plan.CanManagedResume {
		if strings.TrimSpace(plan.StartCommand) != "" {
			return plan.StartCommand, "start"
		}
		if strings.TrimSpace(plan.JobAttachCommand) != "" {
			return plan.JobAttachCommand, "attach"
		}
		if strings.TrimSpace(plan.AttachCommand) != "" {
			return plan.AttachCommand, "attach"
		}
	}
	if plan.Runtime == string(runtimebin.KindCodex) && strings.TrimSpace(plan.ResumeCommand) != "" {
		return plan.ResumeCommand, "resume"
	}
	if strings.TrimSpace(plan.JobLogsCommand) != "" {
		return plan.JobLogsCommand, "logs"
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
		if plan.Stale {
			return "recorded running pid is not live; no session id recorded; follow logs or create a new run"
		}
		return "no session id recorded; follow logs or create a new run"
	}
	if !plan.ManagedResume {
		if plan.Stale {
			return "recorded running pid is not live; " + runtimeResumeUnsupportedResumeDetail(meta, plan)
		}
		return runtimeResumeUnsupportedResumeDetail(meta, plan)
	}
	if plan.Stale {
		return "recorded running pid is not live; managed start can reconcile and resume the recorded runtime session under daemon ownership"
	}
	if meta.Status == daemon.StatusRunning {
		return "managed attach can stop the daemon child, open the session, and resume daemon ownership afterward"
	}
	return "managed start can resume the recorded runtime session under daemon ownership"
}

func runtimeResumeUnsupportedResumeDetail(meta *daemon.Metadata, plan runtimeResumePlan) string {
	detail := lifecycleUnsupportedResumeDetail(meta)
	hints := runtimeResumeUnsupportedResumeActionHints(plan)
	if len(hints) == 0 {
		return detail
	}
	return detail + "; " + strings.Join(hints, "; ")
}

func runtimeResumeUnsupportedResumeActionHints(plan runtimeResumePlan) []string {
	var hints []string
	if command := runtimeResumePlanPlanCommand(plan); command != "" {
		hints = append(hints, "plan: "+command)
	}
	if command := strings.TrimSpace(plan.JobLogsCommand); command != "" {
		hints = append(hints, "logs: "+command)
	} else if command := strings.TrimSpace(plan.LogsCommand); command != "" {
		hints = append(hints, "logs: "+command)
	}
	if command := strings.TrimSpace(plan.JobLastMessageCommand); command != "" {
		hints = append(hints, "last message: "+command)
	} else if command := strings.TrimSpace(plan.LastMessageCommand); command != "" {
		hints = append(hints, "last message: "+command)
	}
	if command := strings.TrimSpace(plan.ResumeCommand); command != "" {
		hints = append(hints, "unmanaged resume: "+command)
	}
	return hints
}

func runtimeResumePlanPlanCommand(plan runtimeResumePlan) string {
	if id := job.NormalizeID(plan.Job); id != "" {
		return "agent-team job resume-plan " + id + jobStepCommandFlag(plan.StepID)
	}
	if instance := strings.TrimSpace(plan.Instance); instance != "" {
		return "agent-team resume-plan " + instance
	}
	return ""
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

func parseRuntimeResumeSort(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "instance":
		return "instance", nil
	case "action", "runtime", "status", "stale", "job", "pipeline", "step", "agent":
		return value, nil
	default:
		return "", fmt.Errorf("--sort must be instance, action, runtime, status, stale, job, pipeline, step, or agent")
	}
}

func sortRuntimeResumePlans(plans []runtimeResumePlan, sortMode string) {
	sortMode, err := parseRuntimeResumeSort(sortMode)
	if err != nil || len(plans) < 2 {
		return
	}
	sort.SliceStable(plans, func(i, j int) bool {
		left := plans[i]
		right := plans[j]
		switch sortMode {
		case "action":
			return runtimeResumePlanSortLess(left.RecommendedAction, right.RecommendedAction, left.Instance, right.Instance)
		case "runtime":
			return runtimeResumePlanSortLess(left.Runtime, right.Runtime, left.Instance, right.Instance)
		case "status":
			return runtimeResumePlanSortLess(left.Status, right.Status, left.Instance, right.Instance)
		case "stale":
			if left.Stale != right.Stale {
				return left.Stale
			}
			return runtimeResumePlanSortLess(left.Instance, right.Instance, "", "")
		case "job":
			return runtimeResumePlanSortLess(left.Job, right.Job, left.Instance, right.Instance)
		case "pipeline":
			return runtimeResumePlanSortLess(left.Pipeline, right.Pipeline, left.Instance, right.Instance)
		case "step":
			return runtimeResumePlanSortLess(left.StepID, right.StepID, left.Instance, right.Instance)
		case "agent":
			return runtimeResumePlanSortLess(left.Agent, right.Agent, left.Instance, right.Instance)
		default:
			return runtimeResumePlanSortLess(left.Instance, right.Instance, "", "")
		}
	})
}

func runtimeResumePlanSortLess(primaryLeft, primaryRight, fallbackLeft, fallbackRight string) bool {
	left := strings.ToLower(strings.TrimSpace(primaryLeft))
	right := strings.ToLower(strings.TrimSpace(primaryRight))
	if left != right {
		return left < right
	}
	left = strings.ToLower(strings.TrimSpace(fallbackLeft))
	right = strings.ToLower(strings.TrimSpace(fallbackRight))
	return left < right
}

func limitRuntimeResumePlans(plans []runtimeResumePlan, limit int) []runtimeResumePlan {
	if limit <= 0 || len(plans) <= limit {
		return plans
	}
	return plans[:limit]
}

func preferRuntimeResumeLastMessages(plans []runtimeResumePlan) {
	for i := range plans {
		plans[i] = runtimeResumePlanPreferLastMessage(plans[i])
	}
}

func runtimeResumePlanPreferLastMessage(plan runtimeResumePlan) runtimeResumePlan {
	if strings.TrimSpace(plan.RecommendedAction) != "logs" {
		return plan
	}
	if command := strings.TrimSpace(plan.JobLastMessageCommand); command != "" {
		plan.RecommendedCommand = command
	} else if command := strings.TrimSpace(plan.LastMessageCommand); command != "" {
		plan.RecommendedCommand = command
	}
	return plan
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
		if plan.Pipeline != "" {
			fmt.Fprintf(w, "pipeline:                 %s\n", plan.Pipeline)
		}
		if plan.StepID != "" {
			fmt.Fprintf(w, "step:                     %s\n", plan.StepID)
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
		if plan.Stale {
			fmt.Fprintf(w, "stale:                    yes\n")
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

func renderRuntimeResumePlanCommands(w fmtWriter, plans []runtimeResumePlan, opts runtimeResumeCommandOptions) {
	for _, plan := range plans {
		command := runtimeResumePlanCommand(plan, opts)
		if command == "" {
			continue
		}
		fmt.Fprintln(w, command)
	}
}

func runtimeResumePlanCommand(plan runtimeResumePlan, opts runtimeResumeCommandOptions) string {
	var args []string
	instance := strings.TrimSpace(plan.Instance)
	jobID := strings.TrimSpace(plan.Job)
	switch strings.TrimSpace(plan.RecommendedAction) {
	case "start":
		if instance == "" {
			return strings.TrimSpace(plan.RecommendedCommand)
		}
		args = []string{"agent-team", "start"}
		args = appendRuntimeResumeCommandTargetArgs(args, opts)
		args = append(args, instance)
	case "attach":
		if jobID != "" && strings.TrimSpace(plan.JobAttachCommand) != "" {
			args = []string{"agent-team", "job", "attach"}
			args = appendRuntimeResumeCommandTargetArgs(args, opts)
			args = append(args, jobID)
			if step := strings.TrimSpace(plan.StepID); step != "" {
				args = append(args, "--step", step)
			}
			args = append(args, "--dry-run")
			break
		}
		if instance == "" {
			return strings.TrimSpace(plan.RecommendedCommand)
		}
		args = []string{"agent-team", "attach"}
		args = appendRuntimeResumeCommandTargetArgs(args, opts)
		args = append(args, instance)
		args = append(args, "--dry-run")
	case "logs":
		if opts.LastMessage && jobID != "" && strings.TrimSpace(plan.JobLastMessageCommand) != "" {
			args = []string{"agent-team", "job", "logs"}
			args = appendRuntimeResumeCommandTargetArgs(args, opts)
			args = append(args, jobID)
			if step := strings.TrimSpace(plan.StepID); step != "" {
				args = append(args, "--step", step)
			}
			args = append(args, "--last-message")
			break
		}
		if opts.LastMessage && instance != "" && strings.TrimSpace(plan.LastMessageCommand) != "" {
			args = []string{"agent-team", "logs"}
			args = appendRuntimeResumeCommandTargetArgs(args, opts)
			args = append(args, instance)
			args = append(args, "--last-message")
			break
		}
		if jobID != "" && strings.TrimSpace(plan.JobLogsCommand) != "" {
			args = []string{"agent-team", "job", "logs"}
			args = appendRuntimeResumeCommandTargetArgs(args, opts)
			args = append(args, jobID)
			if step := strings.TrimSpace(plan.StepID); step != "" {
				args = append(args, "--step", step)
			}
			args = append(args, "--follow")
			break
		}
		if instance == "" {
			return strings.TrimSpace(plan.RecommendedCommand)
		}
		args = []string{"agent-team", "logs"}
		args = appendRuntimeResumeCommandTargetArgs(args, opts)
		args = append(args, instance)
		args = append(args, "--follow")
	}
	if len(args) > 0 {
		return strings.Join(shellQuoteArgs(args), " ")
	}
	return strings.TrimSpace(plan.RecommendedCommand)
}

func appendRuntimeResumeCommandTargetArgs(args []string, opts runtimeResumeCommandOptions) []string {
	if !opts.TargetSet || strings.TrimSpace(opts.Target) == "" {
		return args
	}
	flag := strings.TrimSpace(opts.TargetFlag)
	if flag == "" {
		flag = "--target"
	}
	return append(args, flag, opts.Target)
}

func runtimeResumeCommandOptionsFromFlag(cmd *cobra.Command, target string, localFlag string) runtimeResumeCommandOptions {
	if cmd != nil {
		if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
			if value := strings.TrimSpace(flag.Value.String()); value != "" {
				return runtimeResumeCommandOptions{
					Target:     value,
					TargetFlag: "--" + rootRepoFlagName,
					TargetSet:  true,
				}
			}
		}
		if flagName := strings.TrimSpace(localFlag); flagName != "" && cmd.Flags().Changed(flagName) {
			return runtimeResumeCommandOptions{
				Target:     target,
				TargetFlag: "--" + rootRepoFlagName,
				TargetSet:  true,
			}
		}
	}
	return runtimeResumeCommandOptions{}
}

func runtimeResumePlanRepoFlag(cfg runtimeResumePlanCommandConfig) string {
	if cfg.RepoFlag {
		return "repo"
	}
	return "target"
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
		if plan.Stale {
			out.Stale++
		}
		if runtimeResumePlanUnhealthy(plan) {
			out.Unhealthy++
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
	fmt.Fprintf(w, "stale:              %d\n", summary.Stale)
	fmt.Fprintf(w, "unhealthy:          %d\n", summary.Unhealthy)
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
