package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

func newIntakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "intake",
		Short: "Normalize external events into topology events.",
		Long:  "Normalize external events such as Linear/GitHub webhooks and schedules into topology events handled by the daemon.",
	}
	cmd.AddCommand(newIntakeLinearCmd())
	cmd.AddCommand(newIntakeGitHubCmd())
	cmd.AddCommand(newIntakeScheduleCmd())
	return cmd
}

var intakeInput io.Reader = os.Stdin

func newIntakeLinearCmd() *cobra.Command {
	return newWebhookIntakeCmd("linear", intake.NormalizeLinear)
}

func newIntakeGitHubCmd() *cobra.Command {
	return newWebhookIntakeCmd("github", intake.NormalizeGitHub)
}

func newWebhookIntakeCmd(provider string, normalize func([]byte) (*intake.Event, error)) *cobra.Command {
	var (
		target        string
		payload       string
		payloadFile   string
		dryRun        bool
		previewRoutes bool
		reconcileJob  bool
		cleanupMerged bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   provider,
		Short: "Normalize a " + provider + " webhook payload and publish it.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: --format cannot be combined with --json.\n", provider)
				return exitErr(2)
			}
			if provider == "github" && cleanupMerged && !reconcileJob {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake github: --cleanup-merged requires --reconcile-job.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: --preview-triggers requires --dry-run.\n", provider)
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
			body, err := intakePayload(payload, payloadFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
			ev, err := normalize(body)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
				return exitErr(2)
			}
			var reconcile *job.ReconcileResult
			var cleanupPreview *jobCleanupPreview
			var triggerPreview *eventPublishPreview
			cleanup := ""
			if (provider == "github" && reconcileJob) || previewRoutes {
				teamDir, err := resolveTeamDir(cmd, target)
				if err != nil {
					return err
				}
				if provider == "github" && reconcileJob {
					if dryRun {
						reconcile, cleanupPreview, err = previewGitHubIntakeJob(teamDir, ev, cleanupMerged)
					} else {
						if err := preflightIntakeDaemon(teamDir); err != nil {
							fmt.Fprintln(cmd.ErrOrStderr(), err)
							return exitErr(2)
						}
						reconcile, cleanup, err = reconcileGitHubIntakeJob(teamDir, ev, cleanupMerged)
					}
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake github: %v\n", err)
						return exitErr(1)
					}
				}
				if previewRoutes {
					triggerPreview, err = previewEventPublish(teamDir, ev.Type, ev.Payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake %s: %v\n", provider, err)
						return exitErr(1)
					}
				}
			}
			if dryRun {
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, reconcile, cleanupPreview, triggerPreview)
			}
			return publishIntakeEventWithJob(cmd, target, ev, jsonOut, tmpl, reconcile, cleanup)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&payload, "payload", "", "Webhook JSON object.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read webhook JSON from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Normalize and print the event without publishing to the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	if provider == "github" {
		cmd.Flags().BoolVar(&reconcileJob, "reconcile-job", false, "Also reconcile the normalized PR event into the owning durable job.")
		cmd.Flags().BoolVar(&cleanupMerged, "cleanup-merged", false, "With --reconcile-job, remove the job-owned worktree and branch after a merged PR event.")
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit normalized event and daemon outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the intake result with a Go template, e.g. '{{.Event.Type}}'.")
	return cmd
}

func newIntakeScheduleCmd() *cobra.Command {
	var (
		target        string
		payload       string
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "schedule <name>",
		Short: "Publish a named schedule event.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake schedule: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: %v\n", err)
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake schedule: --preview-triggers requires --dry-run.")
				return exitErr(2)
			}
			body := map[string]any{"source": "schedule", "name": args[0]}
			if strings.TrimSpace(payload) != "" {
				if err := json.Unmarshal([]byte(payload), &body); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: --payload is not valid JSON: %v\n", err)
					return exitErr(2)
				}
				body["source"] = "schedule"
				body["name"] = args[0]
			}
			ev := &intake.Event{Type: "schedule", Payload: body}
			if dryRun {
				var triggerPreview *eventPublishPreview
				if previewRoutes {
					teamDir, err := resolveTeamDir(cmd, target)
					if err != nil {
						return err
					}
					triggerPreview, err = previewEventPublish(teamDir, ev.Type, ev.Payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake schedule: %v\n", err)
						return exitErr(1)
					}
				}
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, nil, nil, triggerPreview)
			}
			return publishIntakeEvent(cmd, target, ev, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&payload, "payload", "", "Additional JSON object merged into the schedule payload.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Normalize and print the event without publishing to the daemon.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit normalized event and daemon outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the intake result with a Go template, e.g. '{{.Event.Type}}'.")
	return cmd
}

func intakePayload(payload, payloadFile string) ([]byte, error) {
	hasPayload := strings.TrimSpace(payload) != ""
	hasFile := strings.TrimSpace(payloadFile) != ""
	if hasPayload == hasFile {
		return nil, fmt.Errorf("provide exactly one of --payload or --payload-file")
	}
	if hasPayload {
		return []byte(payload), nil
	}
	return readPayloadFile(payloadFile)
}

func readPayloadFile(payloadFile string) ([]byte, error) {
	if strings.TrimSpace(payloadFile) == "-" {
		body, err := io.ReadAll(intakeInput)
		if err != nil {
			return nil, fmt.Errorf("--payload-file -: %w", err)
		}
		return body, nil
	}
	body, err := os.ReadFile(filepath.Clean(payloadFile))
	if err != nil {
		return nil, fmt.Errorf("--payload-file: %w", err)
	}
	return body, nil
}

type intakePublishResult struct {
	Event          *intake.Event        `json:"event"`
	Outcome        *eventResponse       `json:"outcome"`
	Reconcile      *job.ReconcileResult `json:"reconcile,omitempty"`
	Cleanup        string               `json:"cleanup,omitempty"`
	CleanupPreview *jobCleanupPreview   `json:"cleanup_preview,omitempty"`
	Preview        *eventPublishPreview `json:"preview,omitempty"`
	DryRun         bool                 `json:"dry_run,omitempty"`
}

func parseIntakeFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("intake-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderIntakeDryRun(w io.Writer, ev *intake.Event, jsonOut bool, tmpl *template.Template, reconcile *job.ReconcileResult, cleanupPreview *jobCleanupPreview, triggerPreview *eventPublishPreview) error {
	result := intakePublishResult{Event: ev, Reconcile: reconcile, CleanupPreview: cleanupPreview, Preview: triggerPreview, DryRun: true}
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderIntakeTemplate(w, result, tmpl)
	}
	fmt.Fprintf(w, "Event: %s\n", ev.Type)
	if len(ev.Payload) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE")
	keys := make([]string, 0, len(ev.Payload))
	for key := range ev.Payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(tw, "%s\t%v\n", key, ev.Payload[key])
	}
	_ = tw.Flush()
	if reconcile != nil && reconcile.Job != nil {
		fmt.Fprintf(w, "Job: %s would reconcile by %s status=%s\n", reconcile.Job.ID, reconcile.MatchedBy, reconcile.Job.Status)
	}
	if cleanupPreview != nil {
		fmt.Fprintf(w, "Cleanup: %s\n", cleanupPreview.Summary)
	}
	if triggerPreview != nil {
		if !eventPublishPreviewHasRoutes(triggerPreview) {
			fmt.Fprintln(w, "Triggers: none")
		} else {
			return renderEventPublishRoutePreview(w, triggerPreview)
		}
	}
	return nil
}

func preflightIntakeDaemon(teamDir string) error {
	if _, err := newDaemonClient(teamDir); err != nil {
		if errors.Is(err, errDaemonNotRunning) {
			return errors.New("agent-team intake: daemon is not running — start it first with `agent-team daemon start`.")
		}
		return err
	}
	return nil
}

func publishIntakeEvent(cmd *cobra.Command, target string, ev *intake.Event, jsonOut bool, tmpl *template.Template) error {
	return publishIntakeEventWithJob(cmd, target, ev, jsonOut, tmpl, nil, "")
}

func publishIntakeEventWithJob(cmd *cobra.Command, target string, ev *intake.Event, jsonOut bool, tmpl *template.Template, reconcile *job.ReconcileResult, cleanup string) error {
	teamDir, err := resolveTeamDir(cmd, target)
	if err != nil {
		return err
	}
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake: daemon is not running — start it first with `agent-team daemon start`.")
		return exitErr(2)
	}
	res, err := dc.PublishEvent(ev.Type, ev.Payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake: %v\n", err)
		return exitErr(1)
	}
	out := cmd.OutOrStdout()
	result := intakePublishResult{Event: ev, Outcome: res, Reconcile: reconcile, Cleanup: cleanup}
	if jsonOut {
		return json.NewEncoder(out).Encode(result)
	}
	if tmpl != nil {
		return renderIntakeTemplate(out, result, tmpl)
	}
	fmt.Fprintf(out, "Event: %s\n", ev.Type)
	if err := renderIntakeOutcome(out, res); err != nil {
		return err
	}
	if reconcile != nil && reconcile.Job != nil {
		fmt.Fprintf(out, "Job: %s reconciled by %s status=%s\n", reconcile.Job.ID, reconcile.MatchedBy, reconcile.Job.Status)
	}
	if cleanup != "" {
		fmt.Fprintf(out, "Cleanup: %s\n", cleanup)
	}
	return nil
}

func reconcileGitHubIntakeJob(teamDir string, ev *intake.Event, cleanupMerged bool) (*job.ReconcileResult, string, error) {
	result, err := job.ReconcilePR(teamDir, job.ReconcileInputFromPayload(ev.Type, ev.Payload), time.Now().UTC())
	if err != nil {
		return nil, "", err
	}
	cleanup := ""
	if cleanupMerged && result.Job.Status == job.StatusDone {
		repoRoot := filepath.Dir(teamDir)
		cleanup, err = cleanupJobOwnedWorktree(repoRoot, result.Job)
		if err != nil {
			return nil, "", err
		}
		result.Job.Worktree = ""
		result.Job.Branch = ""
		result.Job.LastStatus = strings.TrimSpace(result.Job.LastStatus + "; cleanup: " + cleanup)
		result.Job.UpdatedAt = time.Now().UTC()
		if err := writeJobWithAudit(teamDir, result.Job, "cleanup", "cli", cleanup, nil); err != nil {
			return nil, "", err
		}
	}
	return result, cleanup, nil
}

func previewGitHubIntakeJob(teamDir string, ev *intake.Event, cleanupMerged bool) (*job.ReconcileResult, *jobCleanupPreview, error) {
	result, err := job.PreviewReconcilePR(teamDir, job.ReconcileInputFromPayload(ev.Type, ev.Payload), time.Now().UTC())
	if err != nil {
		return nil, nil, err
	}
	var cleanupPreview *jobCleanupPreview
	if cleanupMerged && result.Job.Status == job.StatusDone {
		preview, err := previewJobCleanup(filepath.Dir(teamDir), result.Job)
		if err != nil {
			return nil, nil, err
		}
		cleanupPreview = &preview
	}
	return result, cleanupPreview, nil
}

func renderIntakeTemplate(w io.Writer, result intakePublishResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderIntakeOutcome(w io.Writer, res *eventResponse) error {
	if len(res.Matched) == 0 {
		_, err := fmt.Fprintln(w, "(no triggers matched)")
		return err
	}
	fmt.Fprintf(w, "Matched: %s\n", strings.Join(res.Matched, ", "))
	for _, d := range res.Dispatched {
		name, _ := d["instance"].(string)
		id, _ := d["instance_id"].(string)
		fmt.Fprintf(w, "  dispatched %s as %s\n", name, id)
	}
	for _, n := range res.Queued {
		fmt.Fprintf(w, "  queued %s (at replica capacity)\n", n)
	}
	for _, n := range res.Messaged {
		fmt.Fprintf(w, "  messaged %s\n", n)
	}
	for _, r := range res.Rejected {
		name, _ := r["instance"].(string)
		reason, _ := r["reason"].(string)
		fmt.Fprintf(w, "  rejected %s: %s\n", name, reason)
	}
	return nil
}
