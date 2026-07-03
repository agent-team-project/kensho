package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

type jobExtendResult struct {
	Job       *job.Job            `json:"job"`
	Extension extendCommandResult `json:"extension"`
	StepID    string              `json:"step_id,omitempty"`
}

func newJobExtendCmd() *cobra.Command {
	var (
		repo    string
		stepID  string
		by      time.Duration
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
			if by <= 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job extend: --by must be > 0.")
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
			instance := strings.TrimSpace(selection.Instance)
			if instance == "" {
				printMissingJobInstanceError(cmd.ErrOrStderr(), "extend", j, selection.StepID, "dispatch or adopt it first")
				return exitErr(2)
			}
			res, err := extendInstanceWatchdog(teamDir, instance, by, actor)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job extend: %v\n", err)
				return exitErr(1)
			}
			now := time.Now().UTC()
			row := extendCommandResultFromResponse(res, now)
			applyJobExtendUpdate(j, selection, by, now)
			if err := writeJobWithAudit(teamDir, j, "extended", actor, j.LastStatus, extendAuditData(selection, res, by)); err != nil {
				return err
			}
			result := jobExtendResult{Job: j, Extension: row, StepID: strings.TrimSpace(selection.StepID)}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			if tmpl != nil {
				return renderJobExtendFormat(cmd.OutOrStdout(), result, tmpl)
			}
			if quiet {
				return nil
			}
			renderJobExtendResult(cmd.OutOrStdout(), j, row, selection.StepID)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&stepID, "step", "", "Use this pipeline step's owning instance.")
	cmd.Flags().DurationVar(&by, "by", 0, "Amount to add to the running watchdog deadline, for example 30m.")
	cmd.Flags().StringVar(&actor, "actor", "cli", "Actor label recorded in the job audit event.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the extension result with a Go template, e.g. '{{.Job.ID}} {{.Extension.NewDeadline}}'.")
	_ = cmd.MarkFlagRequired("by")
	return cmd
}

func applyJobExtendUpdate(j *job.Job, selection jobInstanceSelection, by time.Duration, now time.Time) {
	if j == nil {
		return
	}
	instance := strings.TrimSpace(selection.Instance)
	j.LastEvent = "extended"
	if instance != "" {
		j.LastStatus = fmt.Sprintf("extended %s by %s", instance, by)
	} else {
		j.LastStatus = fmt.Sprintf("extended by %s", by)
	}
	j.UpdatedAt = now
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

func renderJobExtendResult(w fmtWriter, j *job.Job, row extendCommandResult, stepID string) {
	fmt.Fprintf(w, "Job: %s extended %s by %s", j.ID, row.Instance, row.By)
	if stepID = strings.TrimSpace(stepID); stepID != "" {
		fmt.Fprintf(w, " step=%s", stepID)
	}
	if row.NewDeadline != "" {
		fmt.Fprintf(w, " deadline=%s", row.NewDeadline)
	}
	if row.RuntimeRemaining != "" {
		fmt.Fprintf(w, " remaining=%s", row.RuntimeRemaining)
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
