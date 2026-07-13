package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/allowance"
	"github.com/agent-team-project/agent-team/internal/budget"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

type jobExtendResult struct {
	Job         *job.Job            `json:"job"`
	Extension   extendCommandResult `json:"extension"`
	StepID      string              `json:"step_id,omitempty"`
	TokensAdded int64               `json:"tokens_added,omitempty"`
	TokenBudget int64               `json:"token_budget,omitempty"`
}

func newJobExtendCmd() *cobra.Command {
	var (
		repo    string
		stepID  string
		by      time.Duration
		tokens  string
		actor   string
		quiet   bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "extend <job-id>",
		Short: "Extend a job's running watchdog deadline.",
		Long: "Extend the armed watchdog deadline for a job's running owning instance. " +
			"Use --step for pipeline jobs when the target step is ambiguous.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tmpl, err := parseJobExtendFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job extend: %v\n", err)
				return exitErr(2)
			}
			if by < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job extend: --by must be >= 0.")
				return exitErr(2)
			}
			tokenDelta := int64(0)
			if strings.TrimSpace(tokens) != "" {
				var err error
				tokenDelta, err = allowance.ParseTokens(tokens)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job extend: --tokens: %v\n", err)
					return exitErr(2)
				}
				if tokenDelta <= 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job extend: --tokens must be > 0.")
					return exitErr(2)
				}
			}
			if by == 0 && tokenDelta == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job extend: pass --by and/or --tokens.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job extend: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job extend: --format cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			selection, err := selectJobOwningInstance(j, stepID)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job extend: %v\n", err)
				return exitErr(2)
			}
			if err := auditCLIJobAuthority(teamDir, j, "job.extend", "job:"+j.ID); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job extend: %v\n", err)
				return exitErr(3)
			}
			var res *runtimeExtensionResponse
			row := extendCommandResult{}
			if by > 0 {
				instance := strings.TrimSpace(selection.Instance)
				if instance == "" {
					printMissingJobInstanceError(cmd.ErrOrStderr(), "extend", j, selection.StepID, "dispatch or adopt it first")
					return exitErr(2)
				}
				res, err = extendInstanceWatchdog(teamDir, instance, by, actor)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job extend: %v\n", err)
					return exitErr(1)
				}
				row = extendCommandResultFromResponse(res, time.Now().UTC())
			}
			now := time.Now().UTC()
			if by > 0 {
				applyJobExtendUpdate(j, selection, by, now)
			}
			newTokenBudget := int64(0)
			tokenGrant := budget.GrantResult{}
			if tokenDelta > 0 {
				grant, err := grantJobTokenExtension(teamDir, j, selection, tokenDelta, now)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job extend: %v\n", err)
					return exitErr(1)
				}
				renderJobExtendDiagnostics(cmd.ErrOrStderr(), grant.Diagnostics)
				if !grant.Allowed {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job extend: token budget exhausted for team %s\n", grant.Team)
					return exitErr(1)
				}
				tokenGrant = grant
				newTokenBudget = applyJobTokenExtendUpdate(j, selection, tokenDelta, now)
			}
			data := extendAuditData(selection, res, by)
			if tokenDelta > 0 {
				data["tokens_added"] = fmt.Sprint(tokenDelta)
				data["token_budget"] = fmt.Sprint(newTokenBudget)
			}
			eventType := "extended"
			if by == 0 && tokenDelta > 0 {
				eventType = "budget_extended"
			}
			if err := writeJobWithAudit(teamDir, j, eventType, actor, j.LastStatus, data); err != nil {
				if tokenGrant.Allocation != nil {
					_, _ = budget.ReleaseAllocations(teamDir, budget.ReleaseRequest{ID: tokenGrant.Allocation.ID, Now: now})
				}
				return err
			}
			result := jobExtendResult{Job: j, Extension: row, StepID: strings.TrimSpace(selection.StepID), TokensAdded: tokenDelta, TokenBudget: newTokenBudget}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if tmpl != nil {
				return renderJobExtendFormat(cmd.OutOrStdout(), result, tmpl)
			}
			if quiet {
				return nil
			}
			renderJobExtendResult(cmd.OutOrStdout(), j, row, selection.StepID, tokenDelta, newTokenBudget)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().DurationVar(&by, "by", 0, "Amount to add to the running watchdog deadline, for example 30m.")
	cmd.Flags().StringVar(&tokens, "tokens", "", "Amount to add to the job's soft token allowance, for example 10M.")
	cmd.Flags().StringVar(&actor, "actor", "cli", "Actor label recorded in the job audit event.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the extension result with a Go template, e.g. '{{.Job.ID}} {{.Extension.NewDeadline}}'.")
	return cmd
}

func renderJobExtendDiagnostics(w fmtWriter, diagnostics []budget.InputDiagnostic) {
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(w, "agent-team job extend: warning: isolated unrelated budget record %s: %s; inspect with `agent-team job doctor --json`.\n", diagnostic.Record, diagnostic.Error)
	}
}

func applyJobExtendUpdate(j *job.Job, selection jobInstanceSelection, by time.Duration, now time.Time) {
	if j == nil {
		return
	}
	extendJobTimeBudget(j, selection, by)
	instance := strings.TrimSpace(selection.Instance)
	j.LastEvent = "extended"
	if instance != "" {
		j.LastStatus = fmt.Sprintf("extended %s by %s", instance, by)
	} else {
		j.LastStatus = fmt.Sprintf("extended by %s", by)
	}
	j.UpdatedAt = now
}

func extendJobTimeBudget(j *job.Job, selection jobInstanceSelection, by time.Duration) {
	if j == nil || by <= 0 {
		return
	}
	if stepID := strings.TrimSpace(selection.StepID); stepID != "" {
		if idx := jobStepIndex(j, stepID); idx >= 0 {
			if extended, ok := extendDurationString(j.Steps[idx].TimeBudget, by); ok {
				j.Steps[idx].TimeBudget = extended
				j.Steps[idx].TimeBudgetNotices = nil
			}
			return
		}
	}
	if extended, ok := extendDurationString(j.TimeBudget, by); ok {
		j.TimeBudget = extended
		j.TimeBudgetNotices = nil
	}
}

func extendDurationString(raw string, by time.Duration) (string, bool) {
	current, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || current <= 0 {
		return "", false
	}
	return (current + by).String(), true
}

func applyJobTokenExtendUpdate(j *job.Job, selection jobInstanceSelection, tokens int64, now time.Time) int64 {
	if j == nil || tokens <= 0 {
		return 0
	}
	if stepID := strings.TrimSpace(selection.StepID); stepID != "" {
		if idx := jobStepIndex(j, stepID); idx >= 0 {
			j.Steps[idx].TokenBudget += tokens
			j.Steps[idx].TokenBudgetNotices = nil
			j.LastEvent = "budget_extended"
			j.LastStatus = fmt.Sprintf("extended token budget for step %s by %d", stepID, tokens)
			j.UpdatedAt = now
			return j.Steps[idx].TokenBudget
		}
	}
	j.TokenBudget += tokens
	j.TokenBudgetNotices = nil
	j.LastEvent = "budget_extended"
	j.LastStatus = fmt.Sprintf("extended token budget by %d", tokens)
	j.UpdatedAt = now
	return j.TokenBudget
}

func grantJobTokenExtension(teamDir string, j *job.Job, selection jobInstanceSelection, tokens int64, now time.Time) (budget.GrantResult, error) {
	result := budget.GrantResult{Allowed: true, Noop: true, GrantedTokens: tokens, RequestedTokens: tokens}
	if j == nil || tokens <= 0 {
		return result, nil
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return budget.GrantResult{}, err
	}
	team := jobBudgetTeamForExtension(top, j, selection)
	if team == "" {
		return result, nil
	}
	env := j.Origin
	if env.Team == "" {
		env.Team = team
	}
	env.Job = j.ID
	env.Instance = tokenExtensionInstance(j, selection)
	env.Trigger = "job.extend"
	return budget.GrantTokens(teamDir, top, budget.GrantRequest{
		Team:               team,
		JobID:              j.ID,
		StepID:             strings.TrimSpace(selection.StepID),
		Instance:           env.Instance,
		Tokens:             tokens,
		IsolateInvalidJobs: true,
		Now:                now,
		Origin:             env,
	})
}

func jobBudgetTeamForExtension(top *topology.Topology, j *job.Job, selection jobInstanceSelection) string {
	if j == nil {
		return ""
	}
	if team := strings.TrimSpace(j.Origin.Team); team != "" {
		return team
	}
	if top == nil {
		return ""
	}
	if pipeline := strings.TrimSpace(j.Pipeline); pipeline != "" {
		for _, team := range top.SortedTeams() {
			if stringSliceContains(team.Pipelines, pipeline) {
				return team.Name
			}
		}
	}
	instance := tokenExtensionInstance(j, selection)
	for _, team := range top.SortedTeams() {
		if instanceMatchesAny(instance, team.Instances) || stringSliceContains(team.Instances, j.Target) {
			return team.Name
		}
	}
	return ""
}

func tokenExtensionInstance(j *job.Job, selection jobInstanceSelection) string {
	if instance := strings.TrimSpace(selection.Instance); instance != "" {
		return instance
	}
	if j == nil {
		return ""
	}
	if stepID := strings.TrimSpace(selection.StepID); stepID != "" {
		for _, step := range j.Steps {
			if step.ID == stepID {
				return strings.TrimSpace(step.Instance)
			}
		}
	}
	return strings.TrimSpace(j.Instance)
}

func instanceMatchesAny(instance string, names []string) bool {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return false
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if instance == name || strings.HasPrefix(instance, name+"-") {
			return true
		}
	}
	return false
}

func writeExtendAuditForMetadata(teamDir string, res *runtimeExtensionResponse, by time.Duration, actor string, now time.Time) error {
	if res == nil || res.Metadata == nil || strings.TrimSpace(res.Metadata.Job) == "" {
		return nil
	}
	j, err := job.Read(teamDir, res.Metadata.Job)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	selection := jobInstanceSelectionForMetadata(j, res.Metadata.Instance)
	applyJobExtendUpdate(j, selection, by, now)
	return writeJobWithAudit(teamDir, j, "extended", actor, j.LastStatus, extendAuditData(selection, res, by))
}

func jobInstanceSelectionForMetadata(j *job.Job, instance string) jobInstanceSelection {
	selection := jobInstanceSelection{Instance: strings.TrimSpace(instance)}
	if j == nil || selection.Instance == "" {
		return selection
	}
	for _, step := range j.Steps {
		if strings.TrimSpace(step.Instance) != selection.Instance {
			continue
		}
		if selection.StepID != "" {
			selection.StepID = ""
			return selection
		}
		selection.StepID = step.ID
	}
	return selection
}

func renderJobExtendResult(w fmtWriter, j *job.Job, row extendCommandResult, stepID string, tokensAdded, tokenBudget int64) {
	fmt.Fprintf(w, "Job: %s", j.ID)
	if row.By != "" {
		fmt.Fprintf(w, " extended %s by %s", row.Instance, row.By)
	}
	if stepID = strings.TrimSpace(stepID); stepID != "" {
		fmt.Fprintf(w, " step=%s", stepID)
	}
	if row.NewDeadline != "" {
		fmt.Fprintf(w, " deadline=%s", row.NewDeadline)
	}
	if row.RuntimeRemaining != "" {
		fmt.Fprintf(w, " remaining=%s", row.RuntimeRemaining)
	}
	if tokensAdded > 0 {
		fmt.Fprintf(w, " tokens_added=%d token_budget=%d", tokensAdded, tokenBudget)
	}
	fmt.Fprintln(w)
}

func parseJobExtendFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-extend-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobExtendFormat(w fmtWriter, result jobExtendResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
