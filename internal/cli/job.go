package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage durable work units.",
		Long: "Manage durable work units backed by `.agent_team/jobs/<job-id>.toml`. " +
			"Jobs track ticket ownership, target agent, lifecycle state, instance, branch, worktree, and PR metadata.",
	}
	cmd.AddCommand(newJobCreateCmd())
	cmd.AddCommand(newJobLsCmd())
	cmd.AddCommand(newJobShowCmd())
	cmd.AddCommand(newJobEventsCmd())
	cmd.AddCommand(newJobWaitCmd())
	cmd.AddCommand(newJobStartCmd())
	cmd.AddCommand(newJobDispatchCmd())
	cmd.AddCommand(newJobSendCmd())
	cmd.AddCommand(newJobLogsCmd())
	cmd.AddCommand(newJobAttachCmd())
	cmd.AddCommand(newJobStopCmd())
	cmd.AddCommand(newJobKillCmd())
	cmd.AddCommand(newJobCloseCmd())
	cmd.AddCommand(newJobUpdateCmd())
	cmd.AddCommand(newJobReopenCmd())
	cmd.AddCommand(newJobCleanupCmd())
	cmd.AddCommand(newJobRmCmd())
	cmd.AddCommand(newJobPruneCmd())
	cmd.AddCommand(newJobNextCmd())
	cmd.AddCommand(newJobReadyCmd())
	cmd.AddCommand(newJobTriageCmd())
	cmd.AddCommand(newJobStepCmd())
	cmd.AddCommand(newJobAdvanceCmd())
	cmd.AddCommand(newJobReconcileCmd())
	return cmd
}

func newJobEventsCmd() *cobra.Command {
	var (
		repo     string
		follow   bool
		tail     string
		types    []string
		actors   []string
		since    string
		interval time.Duration
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "events <job-id>",
		Short: "Show a job's durable event history.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job events: --interval must be >= 0.")
				return exitErr(2)
			}
			filters, err := newJobEventFilters(types, actors, since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
				return exitErr(2)
			}
			tailEvents, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobEventFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job events: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if follow {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runJobEventsFollow(ctx, cmd.OutOrStdout(), teamDir, j.ID, tailEvents, interval, filters, jsonOut, tmpl)
			}
			return runJobEvents(cmd.OutOrStdout(), teamDir, j.ID, tailEvents, filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Poll and print new job events until interrupted.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N events before returning or following (0 or all = all).")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Only show job events with this type. Can repeat or comma-separate.")
	cmd.Flags().StringSliceVar(&actors, "actor", nil, "Only show job events from this actor. Can repeat or comma-separate.")
	cmd.Flags().StringVar(&since, "since", "", "Only show job events since this duration ago (for example 10m, 24h) or an RFC3339 timestamp.")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "Polling interval for --follow.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON. With --follow, emit one JSON object per line.")
	cmd.Flags().StringVar(&format, "format", "", "Render each event with a Go template, e.g. '{{.TS}} {{.Type}} {{.Message}}'.")
	return cmd
}

func newJobCreateCmd() *cobra.Command {
	var (
		repo        string
		targetAgent string
		pipeline    string
		id          string
		ticketURL   string
		kickoff     string
		kickoffFile string
		instance    string
		dispatchNow bool
		workspace   string
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "create <ticket> [kickoff...]",
		Short: "Create a durable job for a ticket.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job create: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			ticket := args[0]
			kickoffText, err := dispatchKickoff(ticket, kickoff, kickoffFile, args[1:])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			target := strings.TrimSpace(targetAgent)
			var pipelineDef *topology.Pipeline
			if strings.TrimSpace(pipeline) != "" {
				pipelineDef, err = loadJobCreatePipeline(teamDir, pipeline)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
					return exitErr(2)
				}
				firstTarget := pipelineDef.Steps[0].Target
				if cmd.Flags().Changed("target") && target != firstTarget {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: --target %q does not match first step target %q for pipeline %q.\n", target, firstTarget, pipelineDef.Name)
					return exitErr(2)
				}
				target = firstTarget
			}
			j, err := job.New(ticket, target, kickoffText, time.Now())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: %v\n", err)
				return exitErr(2)
			}
			if pipelineDef != nil {
				j.Pipeline = pipelineDef.Name
				j.Steps = jobStepsFromPipeline(pipelineDef)
			}
			if strings.TrimSpace(id) != "" {
				normalized := job.NormalizeID(id)
				if normalized == "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: --id %q produced an empty normalized id.\n", id)
					return exitErr(2)
				}
				j.ID = normalized
			}
			if strings.TrimSpace(ticketURL) != "" {
				j.TicketURL = strings.TrimSpace(ticketURL)
			}
			if strings.TrimSpace(instance) != "" {
				j.Instance = strings.TrimSpace(instance)
			}
			j.LastEvent = "created"
			j.LastStatus = "created"
			if _, err := os.Stat(job.Path(teamDir, j.ID)); err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job create: job %q already exists.\n", j.ID)
				return exitErr(2)
			}
			data := map[string]string{
				"ticket": j.Ticket,
				"target": j.Target,
			}
			if j.TicketURL != "" {
				data["ticket_url"] = j.TicketURL
			}
			if j.Pipeline != "" {
				data["pipeline"] = j.Pipeline
			}
			if err := writeJobWithAudit(teamDir, j, "created", "cli", "created "+j.Ticket, data); err != nil {
				return err
			}
			if dispatchNow {
				if len(j.Steps) > 0 {
					res, err := advanceJob(cmd, teamDir, j, workspace)
					if err != nil {
						return err
					}
					if jsonOut {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
					}
					if tmpl != nil {
						return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
					}
					return renderJobAdvanceResult(cmd.OutOrStdout(), res)
				}
				res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, "", workspace, "agent-team job create")
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
				}
				if tmpl != nil {
					return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
				}
				renderDispatchOutcome(cmd.OutOrStdout(), res.Job.Target, requestedName, res.Event)
				fmt.Fprintf(cmd.OutOrStdout(), "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
				return nil
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&targetAgent, "target", "worker", "Target agent that should own this job.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Create this job from a declared pipeline in instances.toml.")
	cmd.Flags().StringVar(&id, "id", "", "Override the normalized job id (default: ticket slug).")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Canonical ticket URL to store on the job.")
	cmd.Flags().StringVar(&kickoff, "kickoff", "", "Kickoff text for the target agent.")
	cmd.Flags().StringVar(&kickoffFile, "kickoff-file", "", "Read kickoff text from a file.")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance name that owns the job (default set during dispatch).")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the created job immediately using the running daemon.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func loadJobCreatePipeline(teamDir, name string) (*topology.Pipeline, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("--pipeline requires a non-empty pipeline name")
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, fmt.Errorf("pipeline %q not found", name)
	}
	p := top.Pipelines[name]
	if p == nil {
		return nil, fmt.Errorf("pipeline %q not found", name)
	}
	if len(p.Steps) == 0 {
		return nil, fmt.Errorf("pipeline %q has no steps", name)
	}
	return p, nil
}

func jobStepsFromPipeline(p *topology.Pipeline) []job.Step {
	steps := make([]job.Step, 0, len(p.Steps))
	for i, step := range p.Steps {
		status := job.StatusQueued
		if i > 0 {
			status = job.StatusBlocked
		}
		steps = append(steps, job.Step{
			ID:     step.ID,
			Target: step.Target,
			Status: status,
			After:  append([]string(nil), step.After...),
		})
	}
	return steps
}

func newJobLsCmd() *cobra.Command {
	var (
		repo         string
		statusFilter string
		targetFilter string
		instance     string
		pipeline     string
		ticket       string
		branch       string
		pr           string
		watch        bool
		noClear      bool
		summary      bool
		jsonOut      bool
		format       string
		sortBy       string
		interval     time.Duration
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List durable jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if format != "" && summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --format cannot be combined with --summary.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ls: --interval must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseJobSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
				return exitErr(2)
			}
			filters, err := newJobListFilters(statusFilter, targetFilter, instance, pipeline, ticket, branch, pr)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ls: %v\n", err)
				return exitErr(2)
			}
			filters.Sort = sortMode
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				if summary {
					return runJobSummaryWatch(ctx, cmd.OutOrStdout(), teamDir, filters, jsonOut, interval, !noClear && !jsonOut)
				}
				return runJobListWatch(ctx, cmd.OutOrStdout(), teamDir, filters, jsonOut, tmpl, interval, !noClear && !jsonOut)
			}
			if summary {
				return runJobSummary(cmd.OutOrStdout(), teamDir, filters, jsonOut)
			}
			return runJobList(cmd.OutOrStdout(), teamDir, filters, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&statusFilter, "status", "", "Filter by status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&targetFilter, "target-agent", "", "Filter by target agent.")
	cmd.Flags().StringVar(&instance, "instance", "", "Filter by owning instance.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Filter by pipeline name.")
	cmd.Flags().StringVar(&ticket, "ticket", "", "Filter by ticket id or URL substring.")
	cmd.Flags().StringVar(&branch, "branch", "", "Filter by branch.")
	cmd.Flags().StringVar(&pr, "pr", "", "Filter by PR URL or number substring.")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the job table until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().BoolVar(&summary, "summary", false, "Show aggregate job counts instead of job rows.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	cmd.Flags().StringVar(&sortBy, "sort", "id", "Sort rows by id, status, target, ticket, created, updated, instance, branch, or pr.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	return cmd
}

func newJobShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show one durable job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job show: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job show: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return renderJobShowResult(cmd.OutOrStdout(), teamDir, j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobWaitCmd() *cobra.Command {
	var (
		repo         string
		statuses     []string
		timeout      time.Duration
		interval     time.Duration
		failOnFailed bool
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "wait <job-id>",
		Short: "Wait for a job to reach a lifecycle status.",
		Long: "Wait for a durable job to reach one of the requested lifecycle statuses. " +
			"By default this waits for a terminal status: done or failed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: --interval must be >= 0.")
				return exitErr(2)
			}
			if timeout < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: --timeout must be >= 0.")
				return exitErr(2)
			}
			if quiet && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: choose one of --quiet or --json.")
				return exitErr(2)
			}
			if format != "" && (quiet || jsonOut) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job wait: --format cannot be combined with --quiet or --json.")
				return exitErr(2)
			}
			waitStatuses, err := parseJobWaitStatuses(statuses, !cmd.Flags().Changed("status"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			cancel := func() {}
			if timeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, timeout)
			}
			defer cancel()
			j, err := runJobWait(ctx, teamDir, args[0], waitStatuses, interval)
			if err != nil {
				if timeoutErr, ok := err.(*jobWaitTimeoutError); ok {
					if !quiet {
						status := "unknown"
						if timeoutErr.Job != nil {
							status = string(timeoutErr.Job.Status)
						}
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job wait: timed out waiting for %s to reach %s (current=%s).\n",
							job.NormalizeID(args[0]), jobWaitStatusList(waitStatuses), status)
					}
					return exitErr(1)
				}
				if err == context.Canceled {
					return nil
				}
				return err
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(j); err != nil {
					return err
				}
			} else if tmpl != nil {
				if err := renderJobTemplate(cmd.OutOrStdout(), j, tmpl); err != nil {
					return err
				}
			} else if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "  wait   %-20s %s\n", j.ID, j.Status)
			}
			if failOnFailed && j.Status == job.StatusFailed {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Status to wait for: queued, running, blocked, done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait (0 = no timeout).")
	cmd.Flags().DurationVar(&interval, "interval", 500*time.Millisecond, "Polling interval.")
	cmd.Flags().BoolVar(&failOnFailed, "fail-on-failed", false, "Exit 1 if the job resolves to failed.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the final job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the final job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobStartCmd() *cobra.Command {
	var (
		repo         string
		wait         bool
		timeout      time.Duration
		readyTimeout time.Duration
		dryRun       bool
		quiet        bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "start <job-id>",
		Short: "Start or resume a job's owning instance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job start: %v\n", err)
				return exitErr(2)
			}
			return runJobInstanceUp(cmd, repo, args[0], instanceUpOptions{
				Wait:    wait,
				Timeout: timeout,
				DryRun:  dryRun,
				Quiet:   quiet,
				JSON:    jsonOut,
				Format:  formatTemplate,
			}, readyTimeout)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the owning instance to become healthy after starting or resuming.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait with --wait (0 = no timeout).")
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", defaultDaemonReadyTimeout, "Maximum time to wait for implicit daemon readiness (0 = no timeout).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the start/resume action without changing daemon or job state.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable lifecycle action JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newJobDispatchCmd() *cobra.Command {
	var (
		repo      string
		source    string
		workspace string
		jsonOut   bool
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "dispatch <job-id>",
		Short: "Dispatch a job to its target agent.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job dispatch: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job dispatch: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, source, workspace, "agent-team job dispatch")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return json.NewEncoder(out).Encode(res)
			}
			if tmpl != nil {
				return renderJobTemplate(out, res.Job, tmpl)
			}
			renderDispatchOutcome(out, res.Job.Target, requestedName, res.Event)
			fmt.Fprintf(out, "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&source, "source", "", "Source instance for the dispatch event (default: AGENT_TEAM_INSTANCE or cli).")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for spawned children: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job and daemon event outcome as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobSendCmd() *cobra.Command {
	var (
		repo         string
		from         string
		allowMissing bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "send <job-id> <message...>",
		Short: "Send a mailbox message to a job's owning instance.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job send: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job send: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(j.Instance) == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job send: job %q has no owning instance; dispatch it first.\n", j.ID)
				return exitErr(2)
			}
			client, err := sendClientForTeamDir(teamDir)
			if err != nil {
				return err
			}
			body := strings.Join(args[1:], " ")
			if err := runSendWithClient(io.Discard, cmd.ErrOrStderr(), client, j.Instance, body, sendOptions{
				From:         from,
				AllowMissing: allowMissing,
			}); err != nil {
				return err
			}
			j.LastEvent = "message_sent"
			j.LastStatus = strings.TrimSpace(body)
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"from": from}); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  sent   %-20s job=%s\n", j.Instance, j.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&from, "from", "(cli)", "Sender label recorded with the message.")
	cmd.Flags().BoolVar(&allowMissing, "allow-missing", false, "Allow queueing a message for an instance the daemon does not know yet.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.LastEvent}}'.")
	return cmd
}

func newJobLogsCmd() *cobra.Command {
	var (
		repo   string
		follow bool
		tail   string
		since  string
		grep   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "Show a job's owning instance log.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			instance := strings.TrimSpace(j.Instance)
			if instance == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: job %q has no owning instance; dispatch it first.\n", j.ID)
				return exitErr(2)
			}
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: %v\n", err)
				return exitErr(2)
			}
			sinceCutoff, err := parseLogSince(since, time.Now)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: %v\n", err)
				return exitErr(2)
			}
			grepPattern, err := parseLogGrep(grep)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job logs: %v\n", err)
				return exitErr(2)
			}
			return runLogs(cmd, filepath.Dir(teamDir), []string{instance}, logsOptions{
				Follow:  follow,
				Tail:    tailLines,
				TailSet: cmd.Flags().Changed("tail"),
				Since:   sinceCutoff,
				Grep:    grepPattern,
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Tail the owning instance log; print new bytes as they appear.")
	cmd.Flags().StringVar(&tail, "tail", "0", "Show only the last N lines before returning or following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Only print the log if it was modified since a duration ago (for example 10m, 24h) or RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Only print log lines matching this regular expression. One-shot reads only.")
	return cmd
}

func newJobAttachCmd() *cobra.Command {
	var (
		repo     string
		noResume bool
		noFollow bool
		tail     string
		since    string
		grep     string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "attach <job-id>",
		Short: "Attach to a job's owning instance.",
		Long: "Attach to the instance recorded on a durable job. By default this opens " +
			"the owning instance with the normal interactive attach flow. Passing log " +
			"options such as --tail, --no-follow, --since, or --grep follows the daemon-captured log stream instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			instance := strings.TrimSpace(j.Instance)
			if instance == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job attach: job %q has no owning instance; dispatch it first.\n", j.ID)
				return exitErr(2)
			}
			repoRoot := filepath.Dir(teamDir)
			logMode := noFollow || cmd.Flags().Changed("tail") || strings.TrimSpace(since) != "" || strings.TrimSpace(grep) != ""
			if logMode {
				if noResume {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job attach: --no-resume cannot be combined with log-follow attach options.")
					return exitErr(2)
				}
				return runAttachLogMode(cmd, repoRoot, []string{instance}, attachLogOptions{
					NoFollow: noFollow,
					Tail:     tail,
					TailSet:  cmd.Flags().Changed("tail"),
					Since:    since,
					Grep:     grep,
				})
			}
			return runAttach(cmd, repoRoot, instance, noResume)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&noResume, "no-resume", false, "Leave the owning instance in stopped state when claude exits.")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "Log mode: print the selected log tail and exit instead of following.")
	cmd.Flags().StringVar(&tail, "tail", "50", "Log mode: show only the last N lines before following (0 or all = all).")
	cmd.Flags().StringVar(&since, "since", "", "Log mode with --no-follow: only print the log if it was modified since this duration ago (for example 10m, 24h) or RFC3339 timestamp.")
	cmd.Flags().StringVar(&grep, "grep", "", "Log mode with --no-follow: only print log lines matching this regular expression.")
	return cmd
}

func newJobStopCmd() *cobra.Command {
	var (
		repo        string
		force       bool
		wait        bool
		timeout     time.Duration
		waitTimeout time.Duration
		dryRun      bool
		remove      bool
		quiet       bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "stop <job-id>",
		Short: "Stop a job's owning instance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job stop: %v\n", err)
				return exitErr(2)
			}
			return runJobInstanceDown(cmd, repo, args[0], instanceDownOptions{
				Force:          force,
				Wait:           wait,
				Timeout:        timeout,
				WaitTimeout:    waitTimeout,
				WaitTimeoutSet: cmd.Flags().Changed("wait-timeout"),
				DryRun:         dryRun,
				Remove:         remove,
				Quiet:          quiet,
				JSON:           jsonOut,
				Format:         formatTemplate,
			}, job.StatusBlocked)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Escalate to SIGKILL if the owning instance does not stop within --timeout.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the owning instance to reach a terminal state.")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Grace before --force kills. With --wait and no --wait-timeout, also used as the wait deadline.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the stop action without changing daemon or job state.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after stopping.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable lifecycle action JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newJobKillCmd() *cobra.Command {
	var (
		repo        string
		timeout     time.Duration
		wait        bool
		waitTimeout time.Duration
		dryRun      bool
		remove      bool
		quiet       bool
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "kill <job-id>",
		Short: "Force-stop a job's owning instance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseLifecycleActionFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job kill: %v\n", err)
				return exitErr(2)
			}
			return runJobInstanceDown(cmd, repo, args[0], instanceDownOptions{
				Force:          true,
				Wait:           wait,
				Timeout:        timeout,
				WaitTimeout:    waitTimeout,
				WaitTimeoutSet: cmd.Flags().Changed("wait-timeout"),
				DryRun:         dryRun,
				Remove:         remove,
				Quiet:          quiet,
				Action:         "kill",
				JSON:           jsonOut,
				Format:         formatTemplate,
			}, job.StatusFailed)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Second, "Grace before SIGKILL escalation.")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the owning instance to reach a terminal state.")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 0, "Maximum time to wait for terminal state with --wait.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the kill action without changing daemon or job state.")
	cmd.Flags().BoolVar(&remove, "rm", false, "Remove selected instance state and daemon metadata after killing.")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output and use only the exit code.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable lifecycle action JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the lifecycle action with a Go template, e.g. '{{.Instance}} {{.Action}}'.")
	return cmd
}

func newJobCloseCmd() *cobra.Command {
	var (
		repo    string
		status  string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "close <job-id>",
		Short: "Close a job as done or failed.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job close: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if status != string(job.StatusDone) && status != string(job.StatusFailed) {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job close: --status must be done or failed.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job close: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			j.Status = job.Status(status)
			j.LastEvent = "closed"
			j.LastStatus = status
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusDone), "Close status: done or failed.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobUpdateCmd() *cobra.Command {
	var (
		repo      string
		status    string
		target    string
		ticketURL string
		instance  string
		branch    string
		worktree  string
		pr        string
		message   string
		clear     []string
		jsonOut   bool
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "update <job-id>",
		Short: "Update job metadata.",
		Long:  "Update durable job metadata such as status, owner instance, branch, worktree, and PR URL.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
				return exitErr(2)
			}
			clearSet, err := parseJobUpdateClear(clear)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			changed := map[string]string{}
			if cmd.Flags().Changed("status") {
				next, err := job.ParseStatus(status)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job update: %v\n", err)
					return exitErr(2)
				}
				j.Status = next
				changed["status"] = string(next)
			}
			if cmd.Flags().Changed("target") {
				if strings.TrimSpace(target) == "" {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: --target cannot be empty.")
					return exitErr(2)
				}
				j.Target = strings.TrimSpace(target)
				changed["target"] = j.Target
			}
			if cmd.Flags().Changed("ticket-url") {
				j.TicketURL = strings.TrimSpace(ticketURL)
				changed["ticket_url"] = j.TicketURL
			}
			if cmd.Flags().Changed("instance") {
				j.Instance = strings.TrimSpace(instance)
				changed["instance"] = j.Instance
			}
			if cmd.Flags().Changed("branch") {
				j.Branch = strings.TrimSpace(branch)
				changed["branch"] = j.Branch
			}
			if cmd.Flags().Changed("worktree") {
				j.Worktree = strings.TrimSpace(worktree)
				changed["worktree"] = j.Worktree
			}
			if cmd.Flags().Changed("pr") {
				j.PR = strings.TrimSpace(pr)
				changed["pr"] = j.PR
			}
			applyJobUpdateClears(j, clearSet, changed)
			if len(changed) == 0 && strings.TrimSpace(message) == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job update: pass at least one update flag.")
				return exitErr(2)
			}
			j.LastEvent = "updated"
			if strings.TrimSpace(message) != "" {
				j.LastStatus = strings.TrimSpace(message)
			} else {
				j.LastStatus = "updated " + jobUpdateFieldList(changed)
			}
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", changed); err != nil {
				return err
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", "", "Set lifecycle status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&target, "target", "", "Set target agent.")
	cmd.Flags().StringVar(&ticketURL, "ticket-url", "", "Set ticket URL.")
	cmd.Flags().StringVar(&instance, "instance", "", "Set owning instance.")
	cmd.Flags().StringVar(&branch, "branch", "", "Set branch.")
	cmd.Flags().StringVar(&worktree, "worktree", "", "Set worktree path.")
	cmd.Flags().StringVar(&pr, "pr", "", "Set PR URL or number.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().StringSliceVar(&clear, "clear", nil, "Clear metadata fields: ticket-url, instance, branch, worktree, pr, or pipeline. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobReopenCmd() *cobra.Command {
	var (
		repo        string
		status      string
		message     string
		force       bool
		dispatchNow bool
		source      string
		workspace   string
		jsonOut     bool
		format      string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "reopen <job-id>",
		Aliases: []string{"retry"},
		Short:   "Reopen a durable job for another attempt.",
		Long: "Reopen a durable job by resetting its lifecycle status to queued or blocked. " +
			"Running jobs are refused unless --force is set. Pass --dispatch to immediately send the reopened job to its target.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reopen: --format cannot be combined with --json.")
				return exitErr(2)
			}
			nextStatus, err := parseJobReopenStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if j.Status == job.StatusRunning && !force {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reopen: refusing to reopen running job %q; pass --force to override.\n", j.ID)
				return exitErr(2)
			}
			j.Status = nextStatus
			j.LastEvent = "reopened"
			if strings.TrimSpace(message) != "" {
				j.LastStatus = strings.TrimSpace(message)
			} else {
				j.LastStatus = "reopened as " + string(nextStatus)
			}
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"force": fmt.Sprint(force)}); err != nil {
				return err
			}
			if dispatchNow {
				if len(j.Steps) > 0 {
					res, err := advanceJob(cmd, teamDir, j, workspace)
					if err != nil {
						return err
					}
					if jsonOut {
						return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
					}
					if tmpl != nil {
						return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
					}
					return renderJobAdvanceResult(cmd.OutOrStdout(), res)
				}
				res, requestedName, err := dispatchJobWithPrefix(cmd, teamDir, j, source, workspace, "agent-team job reopen")
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
				}
				if tmpl != nil {
					return renderJobTemplate(cmd.OutOrStdout(), res.Job, tmpl)
				}
				renderDispatchOutcome(cmd.OutOrStdout(), res.Job.Target, requestedName, res.Event)
				fmt.Fprintf(cmd.OutOrStdout(), "Job: %s status=%s instance=%s\n", res.Job.ID, res.Job.Status, res.Job.Instance)
				return nil
			}
			return renderJobResult(cmd.OutOrStdout(), j, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusQueued), "Reopened status: queued or blocked.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Allow reopening a job currently marked running.")
	cmd.Flags().BoolVar(&dispatchNow, "dispatch", false, "Dispatch the reopened job immediately using the running daemon.")
	cmd.Flags().StringVar(&source, "source", "", "Source instance for --dispatch (default: AGENT_TEAM_INSTANCE or cli).")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for --dispatch: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func newJobCleanupCmd() *cobra.Command {
	var (
		repo    string
		merged  bool
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "cleanup <job-id>",
		Short: "Remove a job-owned worker worktree and branch after merge.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if dryRun && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: --format cannot be combined with --dry-run.")
				return exitErr(2)
			}
			if !merged && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job cleanup: pass --merged after confirming the job's PR has merged.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			repoRoot := filepath.Dir(teamDir)
			if dryRun {
				preview, err := previewJobCleanup(repoRoot, j)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
					return exitErr(1)
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(preview)
				}
				renderJobCleanupPreview(cmd.OutOrStdout(), preview)
				return nil
			}
			summary, err := cleanupJobOwnedWorktree(repoRoot, j)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job cleanup: %v\n", err)
				return exitErr(1)
			}
			j.Worktree = ""
			j.Branch = ""
			j.LastEvent = "cleanup"
			j.LastStatus = summary
			j.UpdatedAt = time.Now().UTC()
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), j, tmpl)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Job: %s cleanup complete (%s)\n", j.ID, summary)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&merged, "merged", false, "Confirm the job's PR has merged before removing its worktree and branch.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the job-owned worktree and branch cleanup without removing anything.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the updated job with a Go template, e.g. '{{.ID}} {{.LastStatus}}'.")
	return cmd
}

func newJobRmCmd() *cobra.Command {
	var (
		repo    string
		force   bool
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:     "rm <job-id> [<job-id>...]",
		Aliases: []string{"remove"},
		Short:   "Remove job files and their event logs.",
		Long: "Remove durable job TOML files and their sibling event logs. " +
			"Queued, running, and blocked jobs are refused unless --force is set.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job rm: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobRemoveFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job rm: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results := make([]jobRemoveResult, 0, len(args))
			for _, id := range args {
				j, err := job.Read(teamDir, id)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job rm: %v\n", err)
					return exitErr(1)
				}
				if !force && !jobStatusTerminal(j.Status) {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job rm: refusing to remove active job %q with status %s; pass --force to remove it.\n", j.ID, j.Status)
					return exitErr(2)
				}
				result, err := removeJobFiles(teamDir, j, jobRemoveOptions{DryRun: dryRun, Force: force})
				if err != nil {
					return err
				}
				results = append(results, result)
			}
			return renderJobRemoveResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Allow removing queued, running, or blocked jobs.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview removals without deleting files.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit removal results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobPruneCmd() *cobra.Command {
	var (
		repo     string
		statuses []string
		dryRun   bool
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove terminal job files and their event logs.",
		Long:  "Remove jobs in terminal statuses. By default, this removes done and failed jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobRemoveFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job prune: %v\n", err)
				return exitErr(2)
			}
			statusSet, err := parseJobPruneStatuses(statuses, !cmd.Flags().Changed("status"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			jobs, err := job.List(teamDir)
			if err != nil {
				return err
			}
			results := make([]jobRemoveResult, 0, len(jobs))
			for _, j := range jobs {
				if !statusSet[j.Status] {
					continue
				}
				result, err := removeJobFiles(teamDir, j, jobRemoveOptions{DryRun: dryRun})
				if err != nil {
					return err
				}
				results = append(results, result)
			}
			return renderJobRemoveResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Terminal status to prune: done, failed, or terminal. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview removals without deleting files.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit removal results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobNextCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "next <job-id>",
		Short: "Show the next pipeline step for a job without dispatching it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job next: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobNextFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job next: %v\n", err)
				return exitErr(2)
			}
			j, err := readJobFromRepo(cmd, repo, args[0])
			if err != nil {
				return err
			}
			return renderJobNextResult(cmd.OutOrStdout(), inspectNextJobStep(j), jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the next-step state as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the next-step state with a Go template, e.g. '{{.State}} {{.Step.ID}}'.")
	return cmd
}

func newJobReadyCmd() *cobra.Command {
	var (
		repo     string
		pipeline string
		states   []string
		jsonOut  bool
		format   string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ready",
		Short: "List pipeline jobs with ready or selected next-step states.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job ready: --format cannot be combined with --json.")
				return exitErr(2)
			}
			stateFilter, err := parseJobNextStateFilter(states, !cmd.Flags().Changed("state"))
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ready: %v\n", err)
				return exitErr(2)
			}
			tmpl, err := parseJobReadyFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job ready: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			return runJobReady(cmd.OutOrStdout(), teamDir, strings.TrimSpace(pipeline), stateFilter, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "Filter by pipeline name.")
	cmd.Flags().StringSliceVar(&states, "state", nil, "Next-step state to include: ready, queued, running, blocked, failed, done, none, or all. Can repeat or comma-separate.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit ready rows as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each row with a Go template, e.g. '{{.JobID}} {{.State}} {{.StepID}}'.")
	return cmd
}

func newJobTriageCmd() *cobra.Command {
	var (
		repo       string
		staleAfter time.Duration
		watch      bool
		noClear    bool
		interval   time.Duration
		jsonOut    bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "triage",
		Short: "Show jobs that need operator attention.",
		Long: "Show a compact work queue triage view from durable jobs, persisted daemon queue items, " +
			"and ready pipeline steps.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if staleAfter < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job triage: --stale-after must be >= 0.")
				return exitErr(2)
			}
			if interval < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job triage: --interval must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			if watch {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runJobTriageWatch(ctx, cmd.OutOrStdout(), teamDir, staleAfter, jsonOut, interval, !noClear)
			}
			snapshot, err := collectJobTriage(teamDir, time.Now().UTC(), staleAfter)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job triage: %v\n", err)
				return exitErr(1)
			}
			return renderJobTriage(cmd.OutOrStdout(), snapshot, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().DurationVar(&staleAfter, "stale-after", defaultJobTriageStaleAfter, "Flag queued or running jobs with no update after this duration (0 disables stale checks).")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh the triage view until interrupted.")
	cmd.Flags().BoolVar(&noClear, "no-clear", false, "With --watch, append snapshots instead of redrawing the terminal.")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Refresh interval for --watch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit triage snapshot as JSON.")
	return cmd
}

func newJobStepCmd() *cobra.Command {
	var (
		repo      string
		status    string
		message   string
		instance  string
		pr        string
		branch    string
		worktree  string
		advance   bool
		workspace string
		jsonOut   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "step <job-id> <step-id>",
		Short: "Update a pipeline job step status.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			stepStatus, err := job.ParseStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			if err := updateJobStep(j, args[1], stepStatus, jobStepUpdate{
				Message:  message,
				Instance: instance,
				PR:       pr,
				Branch:   branch,
				Worktree: worktree,
			}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job step: %v\n", err)
				return exitErr(2)
			}
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": args[1]}); err != nil {
				return err
			}
			if advance && stepStatus == job.StatusDone {
				res, err := advanceJob(cmd, teamDir, j, workspace)
				if err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
				}
				return renderJobAdvanceResult(cmd.OutOrStdout(), res)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(j)
			}
			renderJobDetail(cmd.OutOrStdout(), j)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&status, "status", string(job.StatusDone), "Step status: queued, running, blocked, done, or failed.")
	cmd.Flags().StringVar(&message, "message", "", "Status message recorded on the job.")
	cmd.Flags().StringVar(&instance, "instance", "", "Instance that owns or completed this step.")
	cmd.Flags().StringVar(&pr, "pr", "", "PR URL to record on the job.")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch name to record on the job.")
	cmd.Flags().StringVar(&worktree, "worktree", "", "Worktree path to record on the job.")
	cmd.Flags().BoolVar(&advance, "advance", false, "After marking the step done, dispatch the next ready step.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for an advanced step: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job or advance result as JSON.")
	return cmd
}

func newJobAdvanceCmd() *cobra.Command {
	var (
		repo      string
		workspace string
		jsonOut   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "advance <job-id>",
		Short: "Dispatch the next ready step in a pipeline job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			res, err := advanceJob(cmd, teamDir, j, workspace)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			return renderJobAdvanceResult(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&workspace, "workspace", "auto", "Workspace mode for the advanced step: auto, worktree, or repo.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the updated job and daemon event outcome as JSON.")
	return cmd
}

func newJobReconcileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Reconcile external runtime state back into jobs.",
	}
	cmd.AddCommand(newJobReconcileGitHubCmd())
	cmd.AddCommand(newJobReconcileQueueCmd())
	return cmd
}

func newJobReconcileQueueCmd() *cobra.Command {
	var (
		repo    string
		state   string
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Reconcile persisted queue state back into owning jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile queue: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobQueueReconcileFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile queue: %v\n", err)
				return exitErr(2)
			}
			stateFilter, err := parseJobQueueReconcileState(state)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile queue: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			results, err := reconcileJobsFromQueue(teamDir, stateFilter, dryRun, time.Now().UTC())
			if err != nil {
				return err
			}
			return renderJobQueueReconcileResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&state, "state", queuePruneStateAll, "Queue state to reconcile: pending, dead, or all.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview job updates without writing them.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each result with a Go template, e.g. '{{.JobID}} {{.After}}'.")
	return cmd
}

func newJobReconcileGitHubCmd() *cobra.Command {
	var (
		repo          string
		payload       string
		payloadFile   string
		dryRun        bool
		cleanupMerged bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Reconcile a GitHub PR webhook payload with its owning job.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job reconcile github: --format cannot be combined with --json.")
				return exitErr(2)
			}
			tmpl, err := parseJobFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			body, err := intakePayload(payload, payloadFile)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(2)
			}
			ev, err := intake.NormalizeGitHub(body)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(2)
			}
			input := job.ReconcileInputFromPayload(ev.Type, ev.Payload)
			var result *job.ReconcileResult
			if dryRun {
				result, err = job.PreviewReconcilePR(teamDir, input, time.Now().UTC())
			} else {
				result, err = job.ReconcilePR(teamDir, input, time.Now().UTC())
			}
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
				return exitErr(1)
			}
			cleanupSummary := ""
			var cleanupPreview *jobCleanupPreview
			if cleanupMerged && result.Job.Status == job.StatusDone {
				repoRoot := filepath.Dir(teamDir)
				if dryRun {
					preview, err := previewJobCleanup(repoRoot, result.Job)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
						return exitErr(1)
					}
					cleanupPreview = &preview
				} else {
					cleanupSummary, err = cleanupJobOwnedWorktree(repoRoot, result.Job)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job reconcile github: %v\n", err)
						return exitErr(1)
					}
					result.Job.Worktree = ""
					result.Job.Branch = ""
					result.Job.LastStatus = strings.TrimSpace(result.Job.LastStatus + "; cleanup: " + cleanupSummary)
					result.Job.UpdatedAt = time.Now().UTC()
					if err := writeJobWithAudit(teamDir, result.Job, "cleanup", "cli", cleanupSummary, nil); err != nil {
						return err
					}
				}
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Event          *intake.Event        `json:"event"`
					Result         *job.ReconcileResult `json:"result"`
					Cleanup        string               `json:"cleanup,omitempty"`
					CleanupPreview *jobCleanupPreview   `json:"cleanup_preview,omitempty"`
					DryRun         bool                 `json:"dry_run,omitempty"`
				}{Event: ev, Result: result, Cleanup: cleanupSummary, CleanupPreview: cleanupPreview, DryRun: dryRun})
			}
			if tmpl != nil {
				return renderJobTemplate(cmd.OutOrStdout(), result.Job, tmpl)
			}
			action := "reconciled"
			if dryRun {
				action = "would reconcile"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Job: %s %s by %s status=%s\n", result.Job.ID, action, result.MatchedBy, result.Job.Status)
			if cleanupSummary != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Cleanup: %s\n", cleanupSummary)
			}
			if cleanupPreview != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Cleanup: %s\n", cleanupPreview.Summary)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, "Repo root.")
	cmd.Flags().StringVar(&payload, "payload", "", "GitHub webhook JSON object.")
	cmd.Flags().StringVar(&payloadFile, "payload-file", "", "Read GitHub webhook JSON from a file, or '-' for stdin.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the owning job update without writing it.")
	cmd.Flags().BoolVar(&cleanupMerged, "cleanup-merged", false, "After a merged PR event, remove the job-owned worktree and branch.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the normalized event and reconciled job as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the reconciled job with a Go template, e.g. '{{.ID}} {{.Status}}'.")
	return cmd
}

func readJobFromRepo(cmd *cobra.Command, repo, id string) (*job.Job, error) {
	_, j, err := readJobAndTeamDir(cmd, repo, id)
	return j, err
}

func readJobAndTeamDir(cmd *cobra.Command, repo, id string) (string, *job.Job, error) {
	teamDir, err := resolveTeamDir(cmd, repo)
	if err != nil {
		return "", nil, err
	}
	j, err := job.Read(teamDir, id)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job: %v\n", err)
		return "", nil, exitErr(1)
	}
	return teamDir, j, nil
}

func writeJobWithAudit(teamDir string, j *job.Job, eventType, actor, message string, data map[string]string) error {
	if err := job.Write(teamDir, j); err != nil {
		return err
	}
	return job.AppendSnapshotEvent(teamDir, j, eventType, actor, message, data)
}

func runJobEvents(w io.Writer, teamDir, id string, tail int, filters jobEventFilters, jsonOut bool, tmpl *template.Template) error {
	events, err := job.ListEvents(teamDir, id)
	if err != nil {
		return err
	}
	events = filterJobEvents(events, filters)
	events = job.TailEvents(events, tail)
	return renderJobEvents(w, events, jsonOut, tmpl)
}

func runJobEventsFollow(ctx context.Context, w io.Writer, teamDir, id string, tail int, interval time.Duration, filters jobEventFilters, jsonOut bool, tmpl *template.Template) error {
	if interval <= 0 {
		interval = time.Second
	}
	events, err := job.ListEvents(teamDir, id)
	if err != nil {
		return err
	}
	index := len(events)
	headerWritten := false
	initial := job.TailEvents(filterJobEvents(events, filters), tail)
	if len(initial) > 0 {
		if err := renderJobEventsFollowBatch(w, initial, jsonOut, tmpl, true); err != nil {
			return err
		}
		headerWritten = !jsonOut && tmpl == nil
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		events, err := job.ListEvents(teamDir, id)
		if err != nil {
			return err
		}
		if len(events) < index {
			index = 0
			headerWritten = false
		}
		if len(events) == index {
			continue
		}
		next := filterJobEvents(events[index:], filters)
		index = len(events)
		if len(next) == 0 {
			continue
		}
		if err := renderJobEventsFollowBatch(w, next, jsonOut, tmpl, !headerWritten); err != nil {
			return err
		}
		if !jsonOut && tmpl == nil {
			headerWritten = true
		}
	}
}

type jobEventFilters struct {
	types  map[string]bool
	actors map[string]bool
	since  *time.Time
}

func newJobEventFilters(types, actors []string, sinceRaw string, now func() time.Time) (jobEventFilters, error) {
	var filters jobEventFilters
	var err error
	if filters.types, err = stringSetFilter(types, "--type", "event type"); err != nil {
		return filters, err
	}
	if filters.actors, err = stringSetFilter(actors, "--actor", "actor"); err != nil {
		return filters, err
	}
	sinceRaw = strings.TrimSpace(sinceRaw)
	if sinceRaw == "" {
		return filters, nil
	}
	since, err := parseEventSince(sinceRaw, now)
	if err != nil {
		return filters, err
	}
	filters.since = &since
	return filters, nil
}

func filterJobEvents(events []job.Event, filters jobEventFilters) []job.Event {
	if filters.empty() {
		return events
	}
	out := make([]job.Event, 0, len(events))
	for _, ev := range events {
		if filters.match(ev) {
			out = append(out, ev)
		}
	}
	return out
}

func (f jobEventFilters) empty() bool {
	return len(f.types) == 0 && len(f.actors) == 0 && f.since == nil
}

func (f jobEventFilters) match(ev job.Event) bool {
	if f.since != nil && ev.TS.Before(*f.since) {
		return false
	}
	if len(f.types) > 0 && !f.types[ev.Type] {
		return false
	}
	if len(f.actors) > 0 && !f.actors[ev.Actor] {
		return false
	}
	return true
}

func renderJobEventsFollowBatch(w io.Writer, events []job.Event, jsonOut bool, tmpl *template.Template, header bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		for _, ev := range events {
			if err := enc.Encode(ev); err != nil {
				return err
			}
		}
		return nil
	}
	if tmpl != nil {
		for _, ev := range events {
			if err := renderJobEventTemplate(w, ev, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobEventTable(w, events, header)
	return nil
}

func renderJobEvents(w io.Writer, events []job.Event, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(events)
	}
	if tmpl != nil {
		for _, ev := range events {
			if err := renderJobEventTemplate(w, ev, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobEventTable(w, events, true)
	return nil
}

func renderJobEventTemplate(w io.Writer, ev job.Event, tmpl *template.Template) error {
	if err := tmpl.Execute(w, ev); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderJobEventTable(w io.Writer, events []job.Event, header bool) {
	if len(events) == 0 {
		if header {
			fmt.Fprintln(w, "(no job events)")
		}
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if header {
		fmt.Fprintln(tw, "TIME\tTYPE\tSTATUS\tINSTANCE\tACTOR\tMESSAGE")
	}
	for _, ev := range events {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			ev.TS.Format(time.RFC3339), ev.Type, emptyDash(string(ev.Status)), emptyDash(ev.Instance), emptyDash(ev.Actor), emptyDash(ev.Message))
	}
	_ = tw.Flush()
}

type jobListFilters struct {
	Status   job.Status
	Target   string
	Instance string
	Pipeline string
	Ticket   string
	Branch   string
	PR       string
	Sort     string
}

type jobRemoveOptions struct {
	DryRun bool
	Force  bool
}

type jobRemoveResult struct {
	ID            string     `json:"id"`
	Ticket        string     `json:"ticket"`
	Status        job.Status `json:"status"`
	Action        string     `json:"action"`
	DryRun        bool       `json:"dry_run,omitempty"`
	Forced        bool       `json:"forced,omitempty"`
	Removed       bool       `json:"removed"`
	JobFile       bool       `json:"job_file"`
	EventLog      bool       `json:"event_log"`
	JobPath       string     `json:"job_path"`
	EventPath     string     `json:"event_path"`
	EventsRemoved bool       `json:"events_removed"`
}

type jobCleanupPreview struct {
	JobID               string `json:"job_id"`
	Worktree            string `json:"worktree,omitempty"`
	Branch              string `json:"branch,omitempty"`
	WorktreeExists      bool   `json:"worktree_exists"`
	BranchExists        bool   `json:"branch_exists"`
	WouldRemoveWorktree bool   `json:"would_remove_worktree"`
	WouldRemoveBranch   bool   `json:"would_remove_branch"`
	Summary             string `json:"summary"`
	DryRun              bool   `json:"dry_run"`
}

type jobSummary struct {
	Total        int            `json:"total"`
	Queued       int            `json:"queued"`
	Running      int            `json:"running"`
	Blocked      int            `json:"blocked"`
	Done         int            `json:"done"`
	Failed       int            `json:"failed"`
	Targets      map[string]int `json:"targets"`
	Pipelines    map[string]int `json:"pipelines"`
	WithInstance int            `json:"with_instance"`
	WithBranch   int            `json:"with_branch"`
	WithWorktree int            `json:"with_worktree"`
	WithPR       int            `json:"with_pr"`
}

type jobTriageSnapshot struct {
	CheckedAt  time.Time       `json:"checked_at"`
	Summary    jobSummary      `json:"summary"`
	Queue      queueSummary    `json:"queue"`
	Attention  []jobTriageItem `json:"attention"`
	ReadySteps []jobReadyRow   `json:"ready_steps,omitempty"`
}

type jobTriageItem struct {
	JobID        string     `json:"job_id"`
	Ticket       string     `json:"ticket"`
	Status       job.Status `json:"status"`
	Severity     string     `json:"severity"`
	Reasons      []string   `json:"reasons"`
	Message      string     `json:"message,omitempty"`
	Target       string     `json:"target,omitempty"`
	Instance     string     `json:"instance,omitempty"`
	Pipeline     string     `json:"pipeline,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
	StepID       string     `json:"step_id,omitempty"`
	StepState    string     `json:"step_state,omitempty"`
	StepTarget   string     `json:"step_target,omitempty"`
	QueuePending int        `json:"queue_pending,omitempty"`
	QueueDead    int        `json:"queue_dead,omitempty"`
	QueueDelayed int        `json:"queue_delayed,omitempty"`
	QueueIDs     []string   `json:"queue_ids,omitempty"`
}

type jobTriageQueueStats struct {
	Pending int
	Dead    int
	Delayed int
	IDs     []string
}

const defaultJobTriageStaleAfter = 24 * time.Hour

func newJobListFilters(status, target, instance, pipeline, ticket, branch, pr string) (jobListFilters, error) {
	f := jobListFilters{
		Target:   strings.TrimSpace(target),
		Instance: strings.TrimSpace(instance),
		Pipeline: strings.TrimSpace(pipeline),
		Ticket:   strings.TrimSpace(ticket),
		Branch:   strings.TrimSpace(branch),
		PR:       strings.TrimSpace(pr),
	}
	if strings.TrimSpace(status) != "" {
		parsed, err := job.ParseStatus(status)
		if err != nil {
			return f, err
		}
		f.Status = parsed
	}
	return f, nil
}

func parseJobSort(raw string) (string, error) {
	sortMode := strings.ToLower(strings.TrimSpace(raw))
	switch sortMode {
	case "", "id", "status", "target", "ticket", "created", "updated", "instance", "branch", "pr":
		if sortMode == "" {
			return "id", nil
		}
		return sortMode, nil
	default:
		return "", fmt.Errorf("--sort must be id, status, target, ticket, created, updated, instance, branch, or pr")
	}
}

func sortJobs(jobs []*job.Job, sortMode string) {
	if sortMode == "" {
		sortMode = "id"
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		left, right := jobs[i], jobs[j]
		switch sortMode {
		case "status":
			if rankLeft, rankRight := jobStatusSortRank(left.Status), jobStatusSortRank(right.Status); rankLeft != rankRight {
				return rankLeft < rankRight
			}
		case "target":
			if left.Target != right.Target {
				return left.Target < right.Target
			}
		case "ticket":
			if left.Ticket != right.Ticket {
				return left.Ticket < right.Ticket
			}
		case "created":
			if !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.After(right.CreatedAt)
			}
		case "updated":
			if !left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.UpdatedAt.After(right.UpdatedAt)
			}
		case "instance":
			if left.Instance != right.Instance {
				return left.Instance < right.Instance
			}
		case "branch":
			if left.Branch != right.Branch {
				return left.Branch < right.Branch
			}
		case "pr":
			if left.PR != right.PR {
				return left.PR < right.PR
			}
		}
		return left.ID < right.ID
	})
}

func jobStatusSortRank(status job.Status) int {
	switch status {
	case job.StatusQueued:
		return 0
	case job.StatusRunning:
		return 1
	case job.StatusBlocked:
		return 2
	case job.StatusDone:
		return 3
	case job.StatusFailed:
		return 4
	default:
		return 5
	}
}

func runJobSummary(w io.Writer, teamDir string, filters jobListFilters, jsonOut bool) error {
	filtered, err := filteredJobs(teamDir, filters)
	if err != nil {
		return err
	}
	summary := summarizeJobs(filtered)
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	renderJobSummary(w, summary)
	return nil
}

func runJobSummaryWatch(ctx context.Context, w io.Writer, teamDir string, filters jobListFilters, jsonOut bool, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !jsonOut {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
		}
		if err := runJobSummary(w, teamDir, filters, jsonOut); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

func summarizeJobs(jobs []*job.Job) jobSummary {
	summary := jobSummary{
		Targets:   map[string]int{},
		Pipelines: map[string]int{},
	}
	for _, j := range jobs {
		summary.Total++
		switch j.Status {
		case job.StatusQueued:
			summary.Queued++
		case job.StatusRunning:
			summary.Running++
		case job.StatusBlocked:
			summary.Blocked++
		case job.StatusDone:
			summary.Done++
		case job.StatusFailed:
			summary.Failed++
		}
		if target := strings.TrimSpace(j.Target); target != "" {
			summary.Targets[target]++
		}
		if pipeline := strings.TrimSpace(j.Pipeline); pipeline != "" {
			summary.Pipelines[pipeline]++
		}
		if strings.TrimSpace(j.Instance) != "" {
			summary.WithInstance++
		}
		if strings.TrimSpace(j.Branch) != "" {
			summary.WithBranch++
		}
		if strings.TrimSpace(j.Worktree) != "" {
			summary.WithWorktree++
		}
		if strings.TrimSpace(j.PR) != "" {
			summary.WithPR++
		}
	}
	return summary
}

func collectJobTriage(teamDir string, now time.Time, staleAfter time.Duration) (jobTriageSnapshot, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	jobs, err := job.List(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	queueItems, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	queueByJob := queueStatsByJob(jobs, queueItems, now)
	attention := make([]jobTriageItem, 0, len(jobs))
	for _, j := range jobs {
		if item, ok := triageJob(j, inspectNextJobStep(j), queueByJob[j.ID], now, staleAfter); ok {
			attention = append(attention, item)
		}
	}
	sortJobTriageItems(attention)
	readySteps, err := collectJobReadyRows(teamDir, "", map[string]bool{"ready": true})
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	return jobTriageSnapshot{
		CheckedAt:  now,
		Summary:    summarizeJobs(jobs),
		Queue:      summarizeQueueItems(queueItems, now),
		Attention:  attention,
		ReadySteps: readySteps,
	}, nil
}

func runJobTriageWatch(ctx context.Context, w io.Writer, teamDir string, staleAfter time.Duration, jsonOut bool, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		snapshot, err := collectJobTriage(teamDir, time.Now().UTC(), staleAfter)
		if err != nil {
			return err
		}
		if jsonOut {
			if err := json.NewEncoder(w).Encode(snapshot); err != nil {
				return err
			}
		} else {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
			renderJobTriage(w, snapshot, false)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

func queueStatsByJob(jobs []*job.Job, items []*daemon.QueueItem, now time.Time) map[string]jobTriageQueueStats {
	out := make(map[string]jobTriageQueueStats, len(jobs))
	for _, j := range jobs {
		var stats jobTriageQueueStats
		for _, item := range items {
			if !queueItemMatchesJob(item, j) {
				continue
			}
			stats.IDs = append(stats.IDs, item.ID)
			switch item.State {
			case daemon.QueueStatePending:
				stats.Pending++
				if !item.NextRetry.IsZero() && item.NextRetry.After(now) {
					stats.Delayed++
				}
			case daemon.QueueStateDead:
				stats.Dead++
			}
		}
		if stats.Pending > 0 || stats.Dead > 0 {
			sort.Strings(stats.IDs)
			out[j.ID] = stats
		}
	}
	return out
}

func triageJob(j *job.Job, next jobNextResult, queueStats jobTriageQueueStats, now time.Time, staleAfter time.Duration) (jobTriageItem, bool) {
	item := jobTriageItem{
		JobID:        j.ID,
		Ticket:       j.Ticket,
		Status:       j.Status,
		Severity:     "info",
		Target:       j.Target,
		Instance:     j.Instance,
		Pipeline:     j.Pipeline,
		UpdatedAt:    j.UpdatedAt,
		QueuePending: queueStats.Pending,
		QueueDead:    queueStats.Dead,
		QueueDelayed: queueStats.Delayed,
		QueueIDs:     append([]string(nil), queueStats.IDs...),
	}
	if next.Step != nil {
		item.StepID = next.Step.ID
		item.StepTarget = next.Step.Target
	}
	item.StepState = next.State
	addTriageReason := func(reason, severity string) {
		for _, existing := range item.Reasons {
			if existing == reason {
				item.Severity = maxJobTriageSeverity(item.Severity, severity)
				return
			}
		}
		item.Reasons = append(item.Reasons, reason)
		item.Severity = maxJobTriageSeverity(item.Severity, severity)
	}
	switch j.Status {
	case job.StatusFailed:
		addTriageReason("failed", "critical")
	case job.StatusBlocked:
		addTriageReason("blocked", "warning")
	case job.StatusRunning:
		if strings.TrimSpace(j.Instance) == "" {
			addTriageReason("running_without_instance", "warning")
		}
		if staleAfter > 0 && !j.UpdatedAt.IsZero() && j.UpdatedAt.Before(now.Add(-staleAfter)) {
			addTriageReason("stale_running", "warning")
		}
	case job.StatusQueued:
		if staleAfter > 0 && !j.UpdatedAt.IsZero() && j.UpdatedAt.Before(now.Add(-staleAfter)) && queueStats.Pending == 0 && queueStats.Dead == 0 {
			addTriageReason("stale_queued", "warning")
		}
	}
	if queueStats.Dead > 0 {
		addTriageReason("queue_dead", "critical")
	}
	switch next.State {
	case "failed":
		addTriageReason("failed_step", "critical")
	case "blocked":
		addTriageReason("blocked_step", "warning")
	}
	if len(item.Reasons) == 0 {
		return jobTriageItem{}, false
	}
	if strings.TrimSpace(j.LastStatus) != "" {
		item.Message = j.LastStatus
	} else if strings.TrimSpace(next.Message) != "" {
		item.Message = next.Message
	} else {
		item.Message = strings.Join(item.Reasons, ",")
	}
	return item, true
}

func maxJobTriageSeverity(left, right string) string {
	if jobTriageSeverityRank(right) < jobTriageSeverityRank(left) {
		return right
	}
	return left
}

func jobTriageSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "warning":
		return 1
	case "info":
		return 2
	default:
		return 3
	}
}

func sortJobTriageItems(items []jobTriageItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if li, ri := jobTriageSeverityRank(left.Severity), jobTriageSeverityRank(right.Severity); li != ri {
			return li < ri
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.Before(right.UpdatedAt)
		}
		return left.JobID < right.JobID
	})
}

func renderJobTriage(w io.Writer, snapshot jobTriageSnapshot, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(snapshot)
	}
	renderJobSummary(w, snapshot.Summary)
	renderQueueSummary(w, snapshot.Queue)
	fmt.Fprintln(w)
	renderJobTriageAttention(w, snapshot.Attention)
	if len(snapshot.ReadySteps) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Ready pipeline steps:")
		renderJobReadyTable(w, snapshot.ReadySteps)
	}
	return nil
}

func renderJobTriageAttention(w io.Writer, items []jobTriageItem) {
	if len(items) == 0 {
		fmt.Fprintln(w, "(no jobs need attention)")
		return
	}
	fmt.Fprintln(w, "Attention:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tSEVERITY\tSTATUS\tREASONS\tTARGET\tINSTANCE\tQUEUE\tUPDATED\tMESSAGE")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.JobID,
			item.Severity,
			item.Status,
			strings.Join(item.Reasons, ","),
			emptyDash(item.Target),
			emptyDash(item.Instance),
			jobTriageQueueSummary(item),
			item.UpdatedAt.Format(time.RFC3339),
			emptyDash(item.Message),
		)
	}
	_ = tw.Flush()
}

func jobTriageQueueSummary(item jobTriageItem) string {
	parts := []string{}
	if item.QueueDead > 0 {
		parts = append(parts, fmt.Sprintf("dead=%d", item.QueueDead))
	}
	if item.QueuePending > 0 {
		parts = append(parts, fmt.Sprintf("pending=%d", item.QueuePending))
	}
	if item.QueueDelayed > 0 {
		parts = append(parts, fmt.Sprintf("delayed=%d", item.QueueDelayed))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func renderJobSummary(w io.Writer, summary jobSummary) {
	fmt.Fprintf(w, "jobs: total=%d queued=%d running=%d blocked=%d done=%d failed=%d\n",
		summary.Total, summary.Queued, summary.Running, summary.Blocked, summary.Done, summary.Failed)
	if len(summary.Targets) > 0 {
		fmt.Fprint(w, "targets:")
		for _, key := range sortedCountKeys(summary.Targets) {
			fmt.Fprintf(w, " %s=%d", key, summary.Targets[key])
		}
		fmt.Fprintln(w)
	}
	if len(summary.Pipelines) > 0 {
		fmt.Fprint(w, "pipelines:")
		for _, key := range sortedCountKeys(summary.Pipelines) {
			fmt.Fprintf(w, " %s=%d", key, summary.Pipelines[key])
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "ownership: instance=%d branch=%d worktree=%d pr=%d\n",
		summary.WithInstance, summary.WithBranch, summary.WithWorktree, summary.WithPR)
}

func parseJobPruneStatuses(raw []string, useDefault bool) (map[job.Status]bool, error) {
	if useDefault {
		return map[job.Status]bool{job.StatusDone: true, job.StatusFailed: true}, nil
	}
	statuses := map[job.Status]bool{}
	for _, value := range splitFilterValues(raw) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		switch value {
		case "terminal", "finished":
			statuses[job.StatusDone] = true
			statuses[job.StatusFailed] = true
		case string(job.StatusDone), string(job.StatusFailed):
			parsed, _ := job.ParseStatus(value)
			statuses[parsed] = true
		default:
			return nil, fmt.Errorf("--status must be done, failed, or terminal")
		}
	}
	if len(statuses) == 0 {
		return nil, fmt.Errorf("--status requires at least one non-empty status")
	}
	return statuses, nil
}

func parseJobNextStateFilter(raw []string, useDefault bool) (map[string]bool, error) {
	if useDefault {
		return map[string]bool{"ready": true, "queued": true}, nil
	}
	states := map[string]bool{}
	for _, value := range splitFilterValues(raw) {
		state := strings.ToLower(strings.TrimSpace(value))
		if state == "" {
			continue
		}
		switch state {
		case "all":
			return nil, nil
		case "ready", "queued", "running", "blocked", "failed", "done", "none":
			states[state] = true
		default:
			return nil, fmt.Errorf("--state must be ready, queued, running, blocked, failed, done, none, or all")
		}
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("--state requires at least one non-empty state")
	}
	return states, nil
}

func jobStatusTerminal(status job.Status) bool {
	return status == job.StatusDone || status == job.StatusFailed
}

func parseJobUpdateClear(raw []string) (map[string]bool, error) {
	fields := map[string]bool{}
	for _, value := range splitFilterValues(raw) {
		field := strings.ToLower(strings.TrimSpace(value))
		if field == "" {
			continue
		}
		switch field {
		case "ticket-url", "ticket_url":
			fields["ticket_url"] = true
		case "instance", "branch", "worktree", "pr", "pipeline":
			fields[field] = true
		default:
			return nil, fmt.Errorf("--clear accepts ticket-url, instance, branch, worktree, pr, or pipeline")
		}
	}
	return fields, nil
}

func applyJobUpdateClears(j *job.Job, clearSet map[string]bool, changed map[string]string) {
	for field := range clearSet {
		switch field {
		case "ticket_url":
			j.TicketURL = ""
		case "instance":
			j.Instance = ""
		case "branch":
			j.Branch = ""
		case "worktree":
			j.Worktree = ""
		case "pr":
			j.PR = ""
		case "pipeline":
			j.Pipeline = ""
		}
		changed[field] = ""
	}
}

func jobUpdateFieldList(changed map[string]string) string {
	counts := map[string]int{}
	for field := range changed {
		counts[field] = 1
	}
	return strings.Join(sortedCountKeys(counts), ",")
}

func removeJobFiles(teamDir string, j *job.Job, opts jobRemoveOptions) (jobRemoveResult, error) {
	result := jobRemoveResult{
		ID:        j.ID,
		Ticket:    j.Ticket,
		Status:    j.Status,
		Action:    "removed",
		DryRun:    opts.DryRun,
		Forced:    opts.Force,
		JobPath:   job.Path(teamDir, j.ID),
		EventPath: job.EventPath(teamDir, j.ID),
	}
	if opts.DryRun {
		result.Action = "would_remove"
	}
	jobExists, err := pathExists(result.JobPath)
	if err != nil {
		return result, err
	}
	eventExists, err := pathExists(result.EventPath)
	if err != nil {
		return result, err
	}
	result.JobFile = jobExists
	result.EventLog = eventExists
	if opts.DryRun {
		return result, nil
	}
	if jobExists {
		if err := os.Remove(result.JobPath); err != nil {
			return result, err
		}
		result.Removed = true
	}
	if eventExists {
		if err := os.Remove(result.EventPath); err != nil {
			return result, err
		}
		result.EventsRemoved = true
	}
	if result.Removed || result.EventsRemoved {
		result.Removed = true
	}
	return result, nil
}

func pathExists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

func renderJobRemoveResults(w io.Writer, results []jobRemoveResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobRemoveTable(w, results)
	return nil
}

func renderJobRemoveTable(w io.Writer, results []jobRemoveResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "(no jobs removed)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tACTION\tJOB\tEVENTS")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			result.ID, result.Status, result.Action, yesNo(result.JobFile), yesNo(result.EventLog))
	}
	_ = tw.Flush()
}

func runJobReady(w io.Writer, teamDir, pipeline string, states map[string]bool, jsonOut bool, tmpl *template.Template) error {
	rows, err := collectJobReadyRows(teamDir, pipeline, states)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if tmpl != nil {
		for _, row := range rows {
			if err := tmpl.Execute(w, row); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobReadyTable(w, rows)
	return nil
}

func collectJobReadyRows(teamDir, pipeline string, states map[string]bool) ([]jobReadyRow, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	rows := make([]jobReadyRow, 0, len(jobs))
	for _, j := range jobs {
		if len(j.Steps) == 0 {
			continue
		}
		if pipeline != "" && j.Pipeline != pipeline {
			continue
		}
		next := inspectNextJobStep(j)
		if len(states) > 0 && !states[next.State] {
			continue
		}
		rows = append(rows, jobReadyRowFromJob(j, next))
	}
	return rows, nil
}

func jobReadyRowFromJob(j *job.Job, next jobNextResult) jobReadyRow {
	row := jobReadyRow{
		JobID:      j.ID,
		Ticket:     j.Ticket,
		Pipeline:   j.Pipeline,
		JobStatus:  j.Status,
		State:      next.State,
		WaitingFor: next.WaitingFor,
		UpdatedAt:  j.UpdatedAt,
		Message:    next.Message,
	}
	if next.Step != nil {
		row.StepID = next.Step.ID
		row.Target = next.Step.Target
		row.StepStatus = next.Step.Status
		row.Instance = next.Step.Instance
	}
	return row
}

func renderJobReadyTable(w io.Writer, rows []jobReadyRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no ready pipeline jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tSTATE\tSTEP\tTARGET\tPIPELINE\tWAITING_FOR\tUPDATED")
	for _, row := range rows {
		waiting := "-"
		if len(row.WaitingFor) > 0 {
			waiting = strings.Join(row.WaitingFor, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.JobID, row.State, emptyDash(row.StepID), emptyDash(row.Target), emptyDash(row.Pipeline), waiting, row.UpdatedAt.Format(time.RFC3339))
	}
	_ = tw.Flush()
}

func runJobList(w io.Writer, teamDir string, filters jobListFilters, jsonOut bool, tmpl *template.Template) error {
	filtered, err := filteredJobs(teamDir, filters)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(filtered)
	}
	if tmpl != nil {
		for _, j := range filtered {
			if err := renderJobTemplate(w, j, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	renderJobTable(w, filtered)
	return nil
}

func runJobListWatch(ctx context.Context, w io.Writer, teamDir string, filters jobListFilters, jsonOut bool, tmpl *template.Template, interval time.Duration, clear bool) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !jsonOut {
			if err := writeWatchClear(w, clear); err != nil {
				return err
			}
		}
		if err := runJobList(w, teamDir, filters, jsonOut, tmpl); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !jsonOut && !clear {
				fmt.Fprintln(w)
			}
		}
	}
}

type jobWaitTimeoutError struct {
	Job *job.Job
}

func (e *jobWaitTimeoutError) Error() string {
	return "job wait timed out"
}

func parseJobWaitStatuses(raw []string, useDefault bool) (map[job.Status]bool, error) {
	if useDefault {
		return map[job.Status]bool{
			job.StatusDone:   true,
			job.StatusFailed: true,
		}, nil
	}
	statuses := map[job.Status]bool{}
	for _, value := range splitFilterValues(raw) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		switch value {
		case "terminal", "finished":
			statuses[job.StatusDone] = true
			statuses[job.StatusFailed] = true
		default:
			status, err := job.ParseStatus(value)
			if err != nil {
				return nil, err
			}
			statuses[status] = true
		}
	}
	if len(statuses) == 0 {
		return nil, fmt.Errorf("--status requires at least one non-empty status")
	}
	return statuses, nil
}

func runJobWait(ctx context.Context, teamDir, id string, statuses map[job.Status]bool, interval time.Duration) (*job.Job, error) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	var last *job.Job
	for {
		j, err := job.Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		last = j
		if statuses[j.Status] {
			return j, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if ctx.Err() == context.DeadlineExceeded {
				return last, &jobWaitTimeoutError{Job: last}
			}
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func jobWaitStatusList(statuses map[job.Status]bool) string {
	order := []job.Status{job.StatusQueued, job.StatusRunning, job.StatusBlocked, job.StatusDone, job.StatusFailed}
	out := make([]string, 0, len(statuses))
	for _, status := range order {
		if statuses[status] {
			out = append(out, string(status))
		}
	}
	return strings.Join(out, "|")
}

func parseJobReopenStatus(raw string) (job.Status, error) {
	status, err := job.ParseStatus(raw)
	if err != nil {
		return "", err
	}
	switch status {
	case job.StatusQueued, job.StatusBlocked:
		return status, nil
	default:
		return "", fmt.Errorf("--status must be queued or blocked")
	}
}

func runJobInstanceUp(cmd *cobra.Command, repo, id string, opts instanceUpOptions, readyTimeout time.Duration) error {
	if readyTimeout < 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job start: --ready-timeout must be >= 0.")
		return exitErr(2)
	}
	teamDir, j, err := readJobAndTeamDir(cmd, repo, id)
	if err != nil {
		return err
	}
	instance := strings.TrimSpace(j.Instance)
	if instance == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job start: job %q has no owning instance; dispatch it first.\n", j.ID)
		return exitErr(2)
	}
	repoRoot := filepath.Dir(teamDir)
	if !opts.DryRun {
		if err := ensureDaemonReadyWithTimeout(cmd, repoRoot, opts.JSON || opts.Quiet, readyTimeout); err != nil {
			return err
		}
	}
	if err := runInstanceUpWithOptions(cmd, repoRoot, "", []string{instance}, opts); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}
	applyJobInstanceUpUpdate(j)
	return writeJobWithAudit(teamDir, j, "", "cli", "", nil)
}

func applyJobInstanceUpUpdate(j *job.Job) {
	now := time.Now().UTC()
	if j.Status != job.StatusDone {
		j.Status = job.StatusRunning
	}
	j.LastEvent = "instance_start"
	if strings.TrimSpace(j.Instance) != "" {
		j.LastStatus = "start " + j.Instance
	} else {
		j.LastStatus = "start"
	}
	j.UpdatedAt = now
}

func runJobInstanceDown(cmd *cobra.Command, repo, id string, opts instanceDownOptions, nextStatus job.Status) error {
	teamDir, j, err := readJobAndTeamDir(cmd, repo, id)
	if err != nil {
		return err
	}
	instance := strings.TrimSpace(j.Instance)
	if instance == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job %s: job %q has no owning instance; dispatch it first.\n", downAction(opts), j.ID)
		return exitErr(2)
	}
	if err := runInstanceDownWithOptions(cmd, filepath.Dir(teamDir), []string{instance}, opts); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}
	applyJobInstanceDownUpdate(j, downAction(opts), nextStatus)
	return writeJobWithAudit(teamDir, j, "", "cli", "", nil)
}

func applyJobInstanceDownUpdate(j *job.Job, action string, nextStatus job.Status) {
	now := time.Now().UTC()
	if nextStatus == job.StatusFailed {
		if j.Status != job.StatusDone {
			j.Status = job.StatusFailed
		}
	} else if nextStatus != "" {
		switch j.Status {
		case job.StatusQueued, job.StatusRunning:
			j.Status = nextStatus
		}
	}
	j.LastEvent = "instance_" + action
	if strings.TrimSpace(j.Instance) != "" {
		j.LastStatus = action + " " + j.Instance
	} else {
		j.LastStatus = action
	}
	j.UpdatedAt = now
}

func filteredJobs(teamDir string, filters jobListFilters) ([]*job.Job, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	filtered := make([]*job.Job, 0, len(jobs))
	for _, j := range jobs {
		if jobMatchesFilters(j, filters) {
			filtered = append(filtered, j)
		}
	}
	sortJobs(filtered, filters.Sort)
	return filtered, nil
}

func jobMatchesFilters(j *job.Job, filters jobListFilters) bool {
	if filters.Status != "" && j.Status != filters.Status {
		return false
	}
	if filters.Target != "" && j.Target != filters.Target {
		return false
	}
	if filters.Instance != "" && j.Instance != filters.Instance {
		return false
	}
	if filters.Pipeline != "" && j.Pipeline != filters.Pipeline {
		return false
	}
	if filters.Ticket != "" && !containsFold(j.Ticket, filters.Ticket) && !containsFold(j.TicketURL, filters.Ticket) {
		return false
	}
	if filters.Branch != "" && j.Branch != filters.Branch {
		return false
	}
	if filters.PR != "" && !containsFold(j.PR, filters.PR) {
		return false
	}
	return true
}

func dispatchJobWithPrefix(cmd *cobra.Command, teamDir string, j *job.Job, source, workspace, prefix string) (*jobDispatchResult, string, error) {
	payload, requestedName, err := buildDispatchEventPayload(j.Target, j.Ticket, j.Kickoff, j.Instance, source, workspace)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return nil, "", exitErr(2)
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: daemon is not running — start it with `agent-team start`.\n", prefix)
		return nil, "", exitErr(2)
	}
	res, err := dc.PublishEvent("agent.dispatch", payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", prefix, err)
		return nil, "", exitErr(1)
	}
	if latest, err := job.Read(teamDir, j.ID); err == nil {
		j = latest
	}
	applyDispatchResponseToJob(j, requestedName, res)
	if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{
		"target":             j.Target,
		"requested_instance": requestedName,
	}); err != nil {
		return nil, "", err
	}
	return &jobDispatchResult{Job: j, Event: res}, requestedName, nil
}

func containsFold(value, substr string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(substr))
}

func queueItemsForJob(teamDir string, j *job.Job) ([]*daemon.QueueItem, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	matches := make([]*daemon.QueueItem, 0, len(items))
	for _, item := range items {
		if queueItemMatchesJob(item, j) {
			matches = append(matches, item)
		}
	}
	return matches, nil
}

func queueItemMatchesJob(item *daemon.QueueItem, j *job.Job) bool {
	if item == nil || j == nil {
		return false
	}
	for _, key := range []string{"job_id", "job"} {
		if id := job.NormalizeID(queuePayloadString(item.Payload, key)); id != "" && id == j.ID {
			return true
		}
	}
	if ticket := queuePayloadString(item.Payload, "ticket"); ticket != "" {
		if job.NormalizeID(ticket) == j.ID || strings.EqualFold(strings.TrimSpace(ticket), strings.TrimSpace(j.Ticket)) {
			return true
		}
	}
	if strings.TrimSpace(j.Instance) != "" && item.InstanceID == j.Instance {
		return true
	}
	return false
}

func queuePayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func parseJobQueueReconcileState(raw string) (string, error) {
	state := strings.ToLower(strings.TrimSpace(raw))
	switch state {
	case "", queuePruneStateAll:
		return queuePruneStateAll, nil
	case daemon.QueueStatePending, daemon.QueueStateDead:
		return state, nil
	default:
		return "", fmt.Errorf("--state must be pending, dead, or all")
	}
}

func reconcileJobsFromQueue(teamDir, state string, dryRun bool, now time.Time) ([]jobQueueReconcileResult, error) {
	items, err := daemon.ListQueueItems(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	results := make([]jobQueueReconcileResult, 0)
	for _, item := range items {
		if state != queuePruneStateAll && item.State != state {
			continue
		}
		j := jobForQueueItem(jobs, item)
		if j == nil {
			continue
		}
		result := reconcileJobFromQueueItem(j, item, dryRun, now)
		if result.Changed && !dryRun {
			if err := writeJobWithAudit(teamDir, j, "queue_reconcile", "cli", result.Message, map[string]string{
				"queue_id":    item.ID,
				"queue_state": item.State,
				"instance":    item.Instance,
				"instance_id": item.InstanceID,
			}); err != nil {
				return nil, err
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func jobForQueueItem(jobs []*job.Job, item *daemon.QueueItem) *job.Job {
	for _, j := range jobs {
		if queueItemMatchesJob(item, j) {
			return j
		}
	}
	return nil
}

func reconcileJobFromQueueItem(j *job.Job, item *daemon.QueueItem, dryRun bool, now time.Time) jobQueueReconcileResult {
	before := j.Status
	after, event, status := queueReconciledJobState(j, item, now)
	result := jobQueueReconcileResult{
		JobID:      j.ID,
		QueueID:    item.ID,
		QueueState: item.State,
		Before:     before,
		After:      after,
		Instance:   item.InstanceID,
		Message:    status,
		DryRun:     dryRun,
	}
	if j.Status == job.StatusDone {
		return result
	}
	if j.Status != after || j.LastEvent != event || j.LastStatus != status || (item.InstanceID != "" && j.Instance != item.InstanceID) {
		result.Changed = true
	}
	if dryRun || !result.Changed {
		return result
	}
	j.Status = after
	j.LastEvent = event
	j.LastStatus = status
	if item.InstanceID != "" {
		j.Instance = item.InstanceID
	}
	if item.Instance != "" {
		j.Target = item.Instance
	}
	j.UpdatedAt = now.UTC()
	return result
}

func queueReconciledJobState(j *job.Job, item *daemon.QueueItem, now time.Time) (job.Status, string, string) {
	if j.Status == job.StatusDone {
		return j.Status, j.LastEvent, j.LastStatus
	}
	switch item.State {
	case daemon.QueueStateDead:
		status := strings.TrimSpace(item.LastError)
		if status == "" {
			status = "dead-lettered queue item " + item.ID
		}
		return job.StatusFailed, "queue_dead", status
	case daemon.QueueStatePending:
		status := "queued"
		if !item.NextRetry.IsZero() {
			if item.NextRetry.After(now.UTC()) {
				status = "retry at " + item.NextRetry.Format(time.RFC3339)
			} else {
				status = "ready to retry"
			}
		}
		return job.StatusQueued, "queue_pending", status
	default:
		return j.Status, j.LastEvent, j.LastStatus
	}
}

func applyDispatchResponseToJob(j *job.Job, requestedName string, res *eventResponse) {
	now := time.Now().UTC()
	j.UpdatedAt = now
	if strings.TrimSpace(j.Instance) == "" {
		j.Instance = requestedName
	}
	for _, d := range res.Dispatched {
		if id, _ := d["instance_id"].(string); strings.TrimSpace(id) != "" {
			j.Instance = id
			j.Status = job.StatusRunning
			j.LastEvent = "dispatched"
			j.LastStatus = "running"
			return
		}
	}
	if len(res.Queued) > 0 {
		if strings.TrimSpace(j.Instance) == "" {
			j.Instance = requestedName
		}
		j.Status = job.StatusQueued
		j.LastEvent = "queued"
		j.LastStatus = "queued"
		return
	}
	if len(res.Messaged) > 0 {
		j.Instance = res.Messaged[0]
		j.Status = job.StatusRunning
		j.LastEvent = "messaged"
		j.LastStatus = "running"
		return
	}
	for _, r := range res.Rejected {
		reason, _ := r["reason"].(string)
		if id, _ := r["instance_id"].(string); strings.TrimSpace(id) != "" {
			j.Instance = id
		}
		if strings.Contains(reason, "already running") {
			j.Status = job.StatusRunning
			j.LastEvent = "already_running"
			j.LastStatus = reason
			return
		}
		if strings.Contains(reason, "already queued") {
			j.Status = job.StatusQueued
			j.LastEvent = "already_queued"
			j.LastStatus = reason
			return
		}
		j.Status = job.StatusFailed
		j.LastEvent = "dispatch_rejected"
		j.LastStatus = reason
		return
	}
	if len(res.Matched) == 0 {
		j.Status = job.StatusFailed
		j.LastEvent = "dispatch_no_match"
		j.LastStatus = "no triggers matched"
	}
}

type jobStepUpdate struct {
	Message  string
	Instance string
	PR       string
	Branch   string
	Worktree string
}

type jobAdvanceResult struct {
	Job     *job.Job       `json:"job"`
	Step    *job.Step      `json:"step,omitempty"`
	Event   *eventResponse `json:"event,omitempty"`
	Message string         `json:"message,omitempty"`
}

type jobDispatchResult struct {
	Job   *job.Job       `json:"job"`
	Event *eventResponse `json:"event"`
}

type jobQueueReconcileResult struct {
	JobID      string     `json:"job_id"`
	QueueID    string     `json:"queue_id"`
	QueueState string     `json:"queue_state"`
	Before     job.Status `json:"before"`
	After      job.Status `json:"after"`
	Instance   string     `json:"instance,omitempty"`
	Message    string     `json:"message,omitempty"`
	Changed    bool       `json:"changed"`
	DryRun     bool       `json:"dry_run,omitempty"`
}

type jobNextResult struct {
	JobID      string     `json:"job_id"`
	Ticket     string     `json:"ticket"`
	Pipeline   string     `json:"pipeline,omitempty"`
	JobStatus  job.Status `json:"job_status"`
	State      string     `json:"state"`
	Step       *job.Step  `json:"step,omitempty"`
	WaitingFor []string   `json:"waiting_for,omitempty"`
	Message    string     `json:"message"`
}

type jobReadyRow struct {
	JobID      string     `json:"job_id"`
	Ticket     string     `json:"ticket"`
	Pipeline   string     `json:"pipeline,omitempty"`
	JobStatus  job.Status `json:"job_status"`
	State      string     `json:"state"`
	StepID     string     `json:"step_id,omitempty"`
	Target     string     `json:"target,omitempty"`
	StepStatus job.Status `json:"step_status,omitempty"`
	Instance   string     `json:"instance,omitempty"`
	WaitingFor []string   `json:"waiting_for,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Message    string     `json:"message"`
}

func updateJobStep(j *job.Job, stepID string, status job.Status, update jobStepUpdate) error {
	idx := jobStepIndex(j, stepID)
	if idx == -1 {
		return fmt.Errorf("step %q not found", stepID)
	}
	now := time.Now().UTC()
	step := &j.Steps[idx]
	step.Status = status
	if strings.TrimSpace(update.Instance) != "" {
		step.Instance = strings.TrimSpace(update.Instance)
	}
	if (status == job.StatusRunning || status == job.StatusQueued) && step.StartedAt.IsZero() {
		step.StartedAt = now
	}
	if status == job.StatusDone || status == job.StatusFailed {
		if step.StartedAt.IsZero() {
			step.StartedAt = now
		}
		step.FinishedAt = now
	}
	if update.PR != "" {
		j.PR = update.PR
	}
	if update.Branch != "" {
		j.Branch = update.Branch
	}
	if update.Worktree != "" {
		j.Worktree = update.Worktree
	}
	j.LastEvent = "step_" + string(status)
	if strings.TrimSpace(update.Message) != "" {
		j.LastStatus = strings.TrimSpace(update.Message)
	} else {
		j.LastStatus = stepID + " " + string(status)
	}
	j.UpdatedAt = now
	switch status {
	case job.StatusFailed:
		j.Status = job.StatusFailed
	case job.StatusBlocked:
		j.Status = job.StatusBlocked
	case job.StatusDone:
		if allJobStepsDone(j) {
			j.Status = job.StatusDone
			j.LastEvent = "pipeline_done"
			j.LastStatus = "all steps done"
		} else {
			j.Status = job.StatusRunning
		}
	default:
		j.Status = status
	}
	return nil
}

func advanceJob(cmd *cobra.Command, teamDir string, j *job.Job, workspace string) (*jobAdvanceResult, error) {
	step := nextReadyJobStep(j)
	if step == nil {
		now := time.Now().UTC()
		if allJobStepsDone(j) {
			j.Status = job.StatusDone
			j.LastEvent = "pipeline_done"
			j.LastStatus = "all steps done"
			j.UpdatedAt = now
			if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
				return nil, err
			}
			return &jobAdvanceResult{Job: j, Message: "all steps done"}, nil
		}
		j.Status = job.StatusBlocked
		j.LastEvent = "advance_blocked"
		j.LastStatus = "no ready steps"
		j.UpdatedAt = now
		if err := writeJobWithAudit(teamDir, j, "", "cli", "", nil); err != nil {
			return nil, err
		}
		return &jobAdvanceResult{Job: j, Message: "no ready steps"}, nil
	}
	name := step.Instance
	if strings.TrimSpace(name) == "" {
		name = step.Target + "-" + j.ID + "-" + job.NormalizeID(step.ID)
	}
	payload, requestedName, err := buildDispatchEventPayload(step.Target, j.Ticket, j.Kickoff, name, "job:"+j.ID, workspace)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
		return nil, exitErr(2)
	}
	payload["job_id"] = j.ID
	payload["job"] = j.ID
	if j.Pipeline != "" {
		payload["pipeline"] = j.Pipeline
	}
	payload["pipeline_step"] = step.ID
	dc, err := newDaemonClient(teamDir)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job advance: daemon is not running — start it with `agent-team start`.")
		return nil, exitErr(2)
	}
	res, err := dc.PublishEvent("agent.dispatch", payload)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job advance: %v\n", err)
		return nil, exitErr(1)
	}
	if latest, err := job.Read(teamDir, j.ID); err == nil {
		j = latest
	}
	applyAdvanceResponseToJobStep(j, step.ID, requestedName, res)
	if err := writeJobWithAudit(teamDir, j, "", "cli", "", map[string]string{"step": step.ID}); err != nil {
		return nil, err
	}
	if idx := jobStepIndex(j, step.ID); idx >= 0 {
		return &jobAdvanceResult{Job: j, Step: &j.Steps[idx], Event: res}, nil
	}
	return &jobAdvanceResult{Job: j, Event: res}, nil
}

func applyAdvanceResponseToJobStep(j *job.Job, stepID, requestedName string, res *eventResponse) {
	status := job.StatusFailed
	instance := requestedName
	lastEvent := "advance_rejected"
	lastStatus := "dispatch rejected"
	for _, d := range res.Dispatched {
		if id, _ := d["instance_id"].(string); strings.TrimSpace(id) != "" {
			status = job.StatusRunning
			instance = id
			lastEvent = "advance_dispatched"
			lastStatus = "running " + stepID
			goto done
		}
	}
	if len(res.Queued) > 0 {
		status = job.StatusQueued
		lastEvent = "advance_queued"
		lastStatus = "queued " + stepID
		goto done
	}
	if len(res.Messaged) > 0 {
		status = job.StatusRunning
		instance = res.Messaged[0]
		lastEvent = "advance_messaged"
		lastStatus = "running " + stepID
		goto done
	}
	for _, r := range res.Rejected {
		reason, _ := r["reason"].(string)
		if id, _ := r["instance_id"].(string); strings.TrimSpace(id) != "" {
			instance = id
		}
		if strings.Contains(reason, "already running") {
			status = job.StatusRunning
			lastEvent = "advance_already_running"
			lastStatus = reason
			goto done
		}
		if strings.Contains(reason, "already queued") {
			status = job.StatusQueued
			lastEvent = "advance_already_queued"
			lastStatus = reason
			goto done
		}
		lastStatus = reason
		break
	}
	if len(res.Matched) == 0 {
		lastEvent = "advance_no_match"
		lastStatus = "no triggers matched"
	}
done:
	_ = updateJobStep(j, stepID, status, jobStepUpdate{Instance: instance, Message: lastStatus})
	j.LastEvent = lastEvent
	j.LastStatus = lastStatus
}

func inspectNextJobStep(j *job.Job) jobNextResult {
	res := jobNextResult{
		JobID:     j.ID,
		Ticket:    j.Ticket,
		Pipeline:  j.Pipeline,
		JobStatus: j.Status,
	}
	if len(j.Steps) == 0 {
		res.State = "none"
		res.Message = "job has no pipeline steps"
		return res
	}
	if step := nextReadyJobStep(j); step != nil {
		res.Step = cloneJobStep(step)
		res.State = "ready"
		if step.Status == job.StatusQueued {
			res.State = "queued"
			res.Message = "step " + step.ID + " is queued and ready"
		} else {
			res.Message = "step " + step.ID + " is ready"
		}
		return res
	}
	if step := firstJobStepWithStatus(j, job.StatusRunning); step != nil {
		res.State = "running"
		res.Step = cloneJobStep(step)
		res.Message = "step " + step.ID + " is running"
		return res
	}
	if step := firstJobStepWithStatus(j, job.StatusQueued); step != nil {
		res.State = "queued"
		res.Step = cloneJobStep(step)
		res.WaitingFor = unmetJobStepDependencies(j, step)
		res.Message = "step " + step.ID + " is queued"
		return res
	}
	if allJobStepsDone(j) {
		res.State = "done"
		res.Message = "all steps done"
		return res
	}
	if step := firstJobStepWithStatus(j, job.StatusFailed); step != nil {
		res.State = "failed"
		res.Step = cloneJobStep(step)
		res.Message = "step " + step.ID + " failed"
		return res
	}
	if step := firstJobStepWithStatus(j, job.StatusBlocked); step != nil {
		res.State = "blocked"
		res.Step = cloneJobStep(step)
		res.WaitingFor = unmetJobStepDependencies(j, step)
		if len(res.WaitingFor) > 0 {
			res.Message = "step " + step.ID + " is waiting for " + strings.Join(res.WaitingFor, ",")
		} else {
			res.Message = "step " + step.ID + " is blocked"
		}
		return res
	}
	res.State = "blocked"
	res.Message = "no ready steps"
	return res
}

func cloneJobStep(step *job.Step) *job.Step {
	if step == nil {
		return nil
	}
	cloned := *step
	return &cloned
}

func firstJobStepWithStatus(j *job.Job, status job.Status) *job.Step {
	for i := range j.Steps {
		if j.Steps[i].Status == status {
			return &j.Steps[i]
		}
	}
	return nil
}

func unmetJobStepDependencies(j *job.Job, step *job.Step) []string {
	if step == nil || len(step.After) == 0 {
		return nil
	}
	done := map[string]bool{}
	for _, candidate := range j.Steps {
		if candidate.Status == job.StatusDone {
			done[candidate.ID] = true
		}
	}
	var waiting []string
	for _, dep := range step.After {
		if !done[dep] {
			waiting = append(waiting, dep)
		}
	}
	return waiting
}

func nextReadyJobStep(j *job.Job) *job.Step {
	done := map[string]bool{}
	for _, step := range j.Steps {
		if step.Status == job.StatusDone {
			done[step.ID] = true
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status == job.StatusDone || step.Status == job.StatusFailed || step.Status == job.StatusRunning || step.Status == job.StatusQueued {
			continue
		}
		ready := true
		for _, dep := range step.After {
			if !done[dep] {
				ready = false
				break
			}
		}
		if ready {
			return step
		}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != job.StatusQueued {
			continue
		}
		ready := true
		for _, dep := range step.After {
			if !done[dep] {
				ready = false
				break
			}
		}
		if ready {
			return step
		}
	}
	return nil
}

func allJobStepsDone(j *job.Job) bool {
	if len(j.Steps) == 0 {
		return false
	}
	for _, step := range j.Steps {
		if step.Status != job.StatusDone {
			return false
		}
	}
	return true
}

func jobStepIndex(j *job.Job, stepID string) int {
	for i, step := range j.Steps {
		if step.ID == stepID {
			return i
		}
	}
	return -1
}

func renderJobAdvanceResult(w io.Writer, res *jobAdvanceResult) error {
	if res.Message != "" {
		fmt.Fprintf(w, "Job: %s %s\n", res.Job.ID, res.Message)
		return nil
	}
	if res.Step != nil {
		fmt.Fprintf(w, "Job: %s advanced step=%s status=%s instance=%s\n",
			res.Job.ID, res.Step.ID, res.Step.Status, emptyDash(res.Step.Instance))
	}
	if res.Event != nil {
		renderDispatchOutcome(w, "", "", res.Event)
	}
	return nil
}

func renderJobNextResult(w io.Writer, res jobNextResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(res)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, res); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	if res.Step == nil {
		fmt.Fprintf(w, "Job: %s state=%s message=%q\n", res.JobID, res.State, res.Message)
		return nil
	}
	after := "-"
	if len(res.Step.After) > 0 {
		after = strings.Join(res.Step.After, ",")
	}
	waiting := "-"
	if len(res.WaitingFor) > 0 {
		waiting = strings.Join(res.WaitingFor, ",")
	}
	fmt.Fprintf(w, "Job: %s next step=%s state=%s status=%s target=%s instance=%s after=%s waiting_for=%s\n",
		res.JobID, res.Step.ID, res.State, res.Step.Status, res.Step.Target, emptyDash(res.Step.Instance), after, waiting)
	return nil
}

func previewJobCleanup(repoRoot string, j *job.Job) (jobCleanupPreview, error) {
	preview := jobCleanupPreview{
		JobID:    j.ID,
		Worktree: strings.TrimSpace(j.Worktree),
		Branch:   strings.TrimSpace(j.Branch),
		DryRun:   true,
	}
	if preview.Worktree != "" {
		if err := validateJobOwnedWorktree(repoRoot, preview.Worktree); err != nil {
			return preview, err
		}
		exists, err := pathExists(preview.Worktree)
		if err != nil {
			return preview, err
		}
		preview.WorktreeExists = exists
		preview.WouldRemoveWorktree = exists
	}
	if preview.Branch != "" {
		exists, err := gitBranchExists(repoRoot, preview.Branch)
		if err != nil {
			return preview, err
		}
		preview.BranchExists = exists
		preview.WouldRemoveBranch = exists
	}
	preview.Summary = jobCleanupPreviewSummary(preview)
	return preview, nil
}

func jobCleanupPreviewSummary(preview jobCleanupPreview) string {
	wouldRemove := []string{}
	if preview.WouldRemoveWorktree {
		wouldRemove = append(wouldRemove, "worktree")
	}
	if preview.WouldRemoveBranch {
		wouldRemove = append(wouldRemove, "branch")
	}
	if len(wouldRemove) == 0 {
		return "nothing to clean"
	}
	return "would remove " + strings.Join(wouldRemove, " and ")
}

func renderJobCleanupPreview(w io.Writer, preview jobCleanupPreview) {
	fmt.Fprintf(w, "Job: %s cleanup dry-run (%s)\n", preview.JobID, preview.Summary)
	if preview.Worktree != "" {
		fmt.Fprintf(w, "Worktree: %s exists=%s remove=%s\n", preview.Worktree, yesNo(preview.WorktreeExists), yesNo(preview.WouldRemoveWorktree))
	}
	if preview.Branch != "" {
		fmt.Fprintf(w, "Branch:   %s exists=%s remove=%s\n", preview.Branch, yesNo(preview.BranchExists), yesNo(preview.WouldRemoveBranch))
	}
}

func cleanupJobOwnedWorktree(repoRoot string, j *job.Job) (string, error) {
	if strings.TrimSpace(j.Worktree) == "" && strings.TrimSpace(j.Branch) == "" {
		return "nothing to clean", nil
	}
	removed := make([]string, 0, 2)
	if strings.TrimSpace(j.Worktree) != "" {
		if err := validateJobOwnedWorktree(repoRoot, j.Worktree); err != nil {
			return "", err
		}
		if _, err := os.Stat(j.Worktree); err == nil {
			if out, err := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", j.Worktree).CombinedOutput(); err != nil {
				return "", fmt.Errorf("remove worktree %s: %w: %s", j.Worktree, err, strings.TrimSpace(string(out)))
			}
			removed = append(removed, "worktree")
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if strings.TrimSpace(j.Branch) != "" {
		exists, err := gitBranchExists(repoRoot, j.Branch)
		if err != nil {
			return "", err
		}
		if exists {
			if out, err := exec.Command("git", "-C", repoRoot, "branch", "-d", j.Branch).CombinedOutput(); err != nil {
				return "", fmt.Errorf("remove branch %s: %w: %s", j.Branch, err, strings.TrimSpace(string(out)))
			}
			removed = append(removed, "branch")
		}
	}
	if len(removed) == 0 {
		return "nothing to clean", nil
	}
	return "removed " + strings.Join(removed, " and "), nil
}

func validateJobOwnedWorktree(repoRoot, worktreePath string) error {
	root, err := filepath.Abs(filepath.Join(repoRoot, ".claude", "worktrees"))
	if err != nil {
		return err
	}
	if resolvedRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = resolvedRoot
	}
	path, err := filepath.Abs(worktreePath)
	if err != nil {
		return err
	}
	if resolvedPath, err := filepath.EvalSymlinks(path); err == nil {
		path = resolvedPath
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to remove worktree outside %s: %s", root, path)
	}
	return nil
}

func gitBranchExists(repoRoot, branch string) (bool, error) {
	out, err := exec.Command("git", "-C", repoRoot, "branch", "--list", branch, "--format", "%(refname:short)").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("list branch %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == branch {
			return true, nil
		}
	}
	return false, nil
}

func parseJobFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobEventFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-event-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobNextFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-next-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobReadyFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-ready-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobRemoveFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-remove-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseJobQueueReconcileFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-queue-reconcile-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobResult(w io.Writer, j *job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(j)
	}
	if tmpl != nil {
		return renderJobTemplate(w, j, tmpl)
	}
	renderJobDetail(w, j)
	return nil
}

func renderJobShowResult(w io.Writer, teamDir string, j *job.Job, jsonOut bool, tmpl *template.Template) error {
	if jsonOut || tmpl != nil {
		return renderJobResult(w, j, jsonOut, tmpl)
	}
	queueItems, err := queueItemsForJob(teamDir, j)
	if err != nil {
		return err
	}
	renderJobDetailWithQueue(w, j, queueItems)
	return nil
}

func renderJobTemplate(w io.Writer, j *job.Job, tmpl *template.Template) error {
	if err := tmpl.Execute(w, j); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderJobTable(w io.Writer, jobs []*job.Job) {
	if len(jobs) == 0 {
		fmt.Fprintln(w, "(no jobs)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tTARGET\tINSTANCE\tPIPELINE\tTICKET\tUPDATED")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Status, j.Target, emptyDash(j.Instance), emptyDash(j.Pipeline), j.Ticket, j.UpdatedAt.Format(time.RFC3339))
	}
	_ = tw.Flush()
}

func renderJobQueueReconcileResults(w io.Writer, results []jobQueueReconcileResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if tmpl != nil {
		for _, result := range results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "(no queue-backed jobs reconciled)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "JOB\tQUEUE\tSTATE\tBEFORE\tAFTER\tINSTANCE\tACTION\tMESSAGE")
	for _, result := range results {
		action := "unchanged"
		if result.Changed {
			action = "updated"
			if result.DryRun {
				action = "would_update"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			result.JobID, result.QueueID, result.QueueState, result.Before, result.After, emptyDash(result.Instance), action, emptyDash(result.Message))
	}
	return tw.Flush()
}

func renderJobDetail(w io.Writer, j *job.Job) {
	renderJobDetailWithQueue(w, j, nil)
}

func renderJobDetailWithQueue(w io.Writer, j *job.Job, queueItems []*daemon.QueueItem) {
	fmt.Fprintf(w, "ID:          %s\n", j.ID)
	fmt.Fprintf(w, "Status:      %s\n", j.Status)
	fmt.Fprintf(w, "Ticket:      %s\n", j.Ticket)
	if j.TicketURL != "" {
		fmt.Fprintf(w, "Ticket URL:  %s\n", j.TicketURL)
	}
	fmt.Fprintf(w, "Target:      %s\n", j.Target)
	if j.Instance != "" {
		fmt.Fprintf(w, "Instance:    %s\n", j.Instance)
	}
	if j.Pipeline != "" {
		fmt.Fprintf(w, "Pipeline:    %s\n", j.Pipeline)
	}
	if j.Branch != "" {
		fmt.Fprintf(w, "Branch:      %s\n", j.Branch)
	}
	if j.Worktree != "" {
		fmt.Fprintf(w, "Worktree:    %s\n", j.Worktree)
	}
	if j.PR != "" {
		fmt.Fprintf(w, "PR:          %s\n", j.PR)
	}
	if j.LastEvent != "" {
		fmt.Fprintf(w, "Last Event:  %s\n", j.LastEvent)
	}
	if j.LastStatus != "" {
		fmt.Fprintf(w, "Last Status: %s\n", j.LastStatus)
	}
	if j.Kickoff != "" {
		fmt.Fprintf(w, "Kickoff:     %s\n", j.Kickoff)
	}
	if len(j.Steps) > 0 {
		fmt.Fprintln(w, "Steps:")
		for _, step := range j.Steps {
			instance := step.Instance
			if instance == "" {
				instance = "-"
			}
			after := "-"
			if len(step.After) > 0 {
				after = strings.Join(step.After, ",")
			}
			fmt.Fprintf(w, "  %s  target=%s status=%s instance=%s after=%s\n",
				step.ID, step.Target, step.Status, instance, after)
		}
	}
	if len(queueItems) > 0 {
		fmt.Fprintln(w, "Queue:")
		for _, item := range queueItems {
			fmt.Fprintf(w, "  %s  state=%s instance=%s instance_id=%s attempts=%d next_retry=%s\n",
				item.ID, item.State, item.Instance, item.InstanceID, item.Attempts, queueTime(item.NextRetry))
		}
	}
	fmt.Fprintf(w, "Created:     %s\n", j.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Updated:     %s\n", j.UpdatedAt.Format(time.RFC3339))
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
