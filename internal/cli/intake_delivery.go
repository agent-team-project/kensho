package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/agent-team-project/agent-team/internal/intake"
	"github.com/spf13/cobra"
)

const (
	intakeDeliveryStatusOK    = "ok"
	intakeDeliveryStatusError = "error"

	intakeDeliveryReplayStatusOK    = "ok"
	intakeDeliveryReplayStatusError = "error"
)

var (
	intakeDeliveryLogMu sync.Mutex
	intakeDeliverySeq   atomic.Uint64
)

type intakeDelivery struct {
	ID           string         `json:"id"`
	Time         time.Time      `json:"time"`
	Provider     string         `json:"provider,omitempty"`
	Method       string         `json:"method,omitempty"`
	Path         string         `json:"path,omitempty"`
	RemoteAddr   string         `json:"remote_addr,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
	EventType    string         `json:"event_type,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
	Ticket       string         `json:"ticket,omitempty"`
	PR           string         `json:"pr,omitempty"`
	JobID        string         `json:"job_id,omitempty"`
	Status       string         `json:"status"`
	HTTPStatus   int            `json:"http_status"`
	Error        string         `json:"error,omitempty"`
	ReplayStatus string         `json:"replay_status,omitempty"`
	ReplayedAt   *time.Time     `json:"replayed_at,omitempty"`
	ReplayError  string         `json:"replay_error,omitempty"`
	DryRun       bool           `json:"dry_run,omitempty"`
	Matched      []string       `json:"matched,omitempty"`
	Pipelines    []string       `json:"pipelines,omitempty"`
	Actions      []string       `json:"actions,omitempty"`
}

type intakeDuplicateRequest struct {
	Provider  string    `json:"provider"`
	RequestID string    `json:"request_id"`
	Count     int       `json:"count"`
	IDs       []string  `json:"ids"`
	FirstID   string    `json:"first_id,omitempty"`
	LastID    string    `json:"last_id,omitempty"`
	FirstSeen time.Time `json:"first_seen,omitempty"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
	Actions   []string  `json:"actions,omitempty"`
}

func newIntakeDeliveriesCmd() *cobra.Command {
	var (
		target       string
		provider     string
		status       string
		replayStatus string
		requestID    string
		unresolved   bool
		tail         string
		commands     bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "deliveries",
		Short: "List recent intake server deliveries.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tailLines, err := parseLogTail(tail)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake deliveries: --tail must be >= 0 or \"all\".")
				return exitErr(2)
			}
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider != "" && provider != "linear" && provider != "github" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake deliveries: --provider must be linear or github.")
				return exitErr(2)
			}
			status = strings.ToLower(strings.TrimSpace(status))
			if status != "" && status != intakeDeliveryStatusOK && status != intakeDeliveryStatusError {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake deliveries: --status must be ok or error.")
				return exitErr(2)
			}
			replayStatus, err = parseIntakeReplayStatusFilter(replayStatus)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake deliveries: %v\n", err)
				return exitErr(2)
			}
			requestID = strings.TrimSpace(requestID)
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake deliveries: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake deliveries: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake deliveries: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeDeliveryFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake deliveries: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			deliveries, err := listIntakeDeliveries(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake deliveries: %v\n", err)
				return exitErr(1)
			}
			deliveries = filterIntakeDeliveries(deliveries, intakeDeliveryFilter{
				Provider:     provider,
				Status:       status,
				ReplayStatus: replayStatus,
				RequestID:    requestID,
				Unresolved:   unresolved,
			})
			deliveries = tailIntakeDeliveries(deliveries, tailLines)
			deliveries = withIntakeDeliveryActions(deliveries)
			return renderIntakeDeliveries(cmd.OutOrStdout(), deliveries, jsonOut, tmpl, commands, operatorCommandScopeFromCommand(cmd, target, "target"))
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&provider, "provider", "", "Only show deliveries for a provider: linear or github.")
	cmd.Flags().StringVar(&status, "status", "", "Only show deliveries with a status: ok or error.")
	cmd.Flags().StringVar(&replayStatus, "replay-status", "", "Only show deliveries with replay status: ok, error, none, or any.")
	cmd.Flags().StringVar(&requestID, "request-id", "", "Only show deliveries with this provider request id, such as X-GitHub-Delivery.")
	cmd.Flags().BoolVar(&unresolved, "unresolved", false, "Only show failed deliveries that still need replay.")
	cmd.Flags().StringVar(&tail, "tail", "20", "Show only the last N deliveries (0 or all = all).")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, one per line.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit deliveries as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each delivery with a Go template, e.g. '{{.Provider}} {{.Status}} {{.EventType}}'.")
	return cmd
}

func newIntakeDuplicatesCmd() *cobra.Command {
	var (
		target    string
		provider  string
		requestID string
		commands  bool
		jsonOut   bool
		format    string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "duplicates",
		Short: "List duplicate provider request ids in the delivery ledger.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider != "" && provider != "linear" && provider != "github" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake duplicates: --provider must be linear or github.")
				return exitErr(2)
			}
			requestID = strings.TrimSpace(requestID)
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake duplicates: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake duplicates: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake duplicates: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeDuplicateFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake duplicates: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			deliveries, err := listIntakeDeliveries(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake duplicates: %v\n", err)
				return exitErr(1)
			}
			rows := duplicateIntakeRequestIDs(deliveries, provider, requestID)
			return renderIntakeDuplicates(cmd.OutOrStdout(), rows, jsonOut, tmpl, commands, operatorCommandScopeFromCommand(cmd, target, "target"))
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&provider, "provider", "", "Only show duplicate request ids for a provider: linear or github.")
	cmd.Flags().StringVar(&requestID, "request-id", "", "Only show this provider request id.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, one per line.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit duplicate request id groups as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each duplicate group with a Go template, e.g. '{{.Provider}} {{.RequestID}} {{.Count}}'.")
	return cmd
}

func newIntakeReplayCmd() *cobra.Command {
	var (
		target        string
		all           bool
		provider      string
		status        string
		limit         int
		dedupeRequest bool
		dryRun        bool
		previewRoutes bool
		commands      bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "replay [delivery-id]",
		Short: "Replay a recorded normalized intake delivery.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --commands requires --dry-run.")
				return exitErr(2)
			}
			if previewRoutes && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --preview-triggers requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake replay: %v\n", err)
				return exitErr(2)
			}
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider != "" && provider != "linear" && provider != "github" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --provider must be linear or github.")
				return exitErr(2)
			}
			status, err := parseIntakeReplayStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake replay: %v\n", err)
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --limit must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if all {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --all does not accept a delivery id.")
					return exitErr(2)
				}
				batch, err := replayAllIntakeDeliveries(teamDir, provider, status, limit, dedupeRequest, dryRun, previewRoutes)
				if err != nil {
					if errors.Is(err, errDaemonNotRunning) {
						fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: daemon is not running; start it first with `agent-team daemon start`.")
						return exitErr(2)
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake replay: %v\n", err)
					return exitErr(1)
				}
				if commands {
					return renderIntakeReplayAllApplyCommand(cmd.OutOrStdout(), batch, intakeReplayAllApplyCommandOptions{
						Repo:          intakeCommandRepo(cmd, target),
						RepoSet:       intakeCommandRepoSet(cmd),
						RepoFlag:      intakeCommandRepoFlag(cmd),
						Provider:      provider,
						ProviderSet:   cmd.Flags().Changed("provider"),
						Status:        status,
						StatusSet:     cmd.Flags().Changed("status"),
						Limit:         limit,
						LimitSet:      cmd.Flags().Changed("limit"),
						DedupeRequest: dedupeRequest,
					})
				}
				if err := renderIntakeReplayBatch(cmd.OutOrStdout(), batch, jsonOut, tmpl); err != nil {
					return err
				}
				if batch.Failed > 0 {
					return exitErr(1)
				}
				return nil
			}
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: expected one delivery id, or use --all.")
				return exitErr(2)
			}
			delivery, ok, err := findIntakeDelivery(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake replay: %v\n", err)
				return exitErr(1)
			}
			if !ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake replay: delivery %q not found.\n", args[0])
				return exitErr(1)
			}
			ev, err := eventFromIntakeDelivery(delivery)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake replay: %v\n", err)
				return exitErr(1)
			}
			if dryRun {
				var triggerPreview *eventPublishPreview
				if previewRoutes {
					triggerPreview, err = previewEventPublish(teamDir, ev.Type, ev.Payload)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake replay: %v\n", err)
						return exitErr(1)
					}
				}
				if commands {
					return renderIntakeReplayApplyCommand(cmd.OutOrStdout(), delivery.ID, intakeReplayApplyCommandOptions{
						Repo:     intakeCommandRepo(cmd, target),
						RepoSet:  intakeCommandRepoSet(cmd),
						RepoFlag: intakeCommandRepoFlag(cmd),
					})
				}
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, nil, nil, nil, triggerPreview)
			}
			err = publishIntakeEvent(cmd, target, ev, jsonOut, tmpl)
			replayErr := ""
			if err != nil {
				replayErr = err.Error()
			}
			if markErr := markIntakeDeliveryReplays(teamDir, []intakeReplayResult{{
				DeliveryID: delivery.ID,
				OK:         err == nil,
				Error:      replayErr,
			}}, time.Now().UTC()); markErr != nil && err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake replay: record replay: %v\n", markErr)
				return exitErr(1)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&all, "all", false, "Replay all matching recorded deliveries.")
	cmd.Flags().StringVar(&provider, "provider", "", "With --all, only replay deliveries for a provider: linear or github.")
	cmd.Flags().StringVar(&status, "status", intakeDeliveryStatusError, "With --all, delivery status to replay: ok, error, or all. error skips recovered replays.")
	cmd.Flags().IntVar(&limit, "limit", 0, "With --all, replay at most this many matching deliveries (0 = all).")
	cmd.Flags().BoolVar(&dedupeRequest, "dedupe-request-id", false, "With --all, skip later deliveries with the same provider request id.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the normalized delivery without publishing it.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the apply command, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit replay result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the replay result with a Go template, e.g. '{{.Event.Type}}'.")
	return cmd
}

type intakeReplayResult struct {
	DeliveryID string               `json:"delivery_id"`
	Provider   string               `json:"provider,omitempty"`
	Status     string               `json:"status,omitempty"`
	HTTPStatus int                  `json:"http_status,omitempty"`
	Event      *intake.Event        `json:"event,omitempty"`
	Outcome    *eventResponse       `json:"outcome,omitempty"`
	Preview    *eventPublishPreview `json:"preview,omitempty"`
	DryRun     bool                 `json:"dry_run,omitempty"`
	OK         bool                 `json:"ok"`
	Error      string               `json:"error,omitempty"`
}

type intakeReplayBatchResult struct {
	DryRun                     bool                 `json:"dry_run,omitempty"`
	Total                      int                  `json:"total"`
	Succeeded                  int                  `json:"succeeded"`
	Failed                     int                  `json:"failed"`
	SkippedDuplicateRequestIDs int                  `json:"skipped_duplicate_request_ids,omitempty"`
	Results                    []intakeReplayResult `json:"results"`
}

type intakePruneResult struct {
	ID           string    `json:"id"`
	Time         time.Time `json:"time"`
	Provider     string    `json:"provider,omitempty"`
	Status       string    `json:"status"`
	ReplayStatus string    `json:"replay_status,omitempty"`
	HTTPStatus   int       `json:"http_status"`
	EventType    string    `json:"event_type,omitempty"`
	Ticket       string    `json:"ticket,omitempty"`
	PR           string    `json:"pr,omitempty"`
	DryRun       bool      `json:"dry_run,omitempty"`
	Dropped      bool      `json:"dropped"`
}

type intakeSummaryResult struct {
	Deliveries          int                     `json:"deliveries"`
	OK                  int                     `json:"ok"`
	Failed              int                     `json:"failed"`
	Unresolved          int                     `json:"unresolved"`
	Recovered           int                     `json:"recovered"`
	Replayable          int                     `json:"replayable"`
	ReplayFailed        int                     `json:"replay_failed"`
	DuplicateRequestIDs int                     `json:"duplicate_request_ids,omitempty"`
	LatestErrorID       string                  `json:"latest_error_id,omitempty"`
	LatestError         string                  `json:"latest_error,omitempty"`
	Providers           []intakeProviderSummary `json:"providers,omitempty"`
	Actions             []string                `json:"actions,omitempty"`
}

type intakeProviderSummary struct {
	Provider     string `json:"provider,omitempty"`
	Deliveries   int    `json:"deliveries"`
	OK           int    `json:"ok"`
	Failed       int    `json:"failed"`
	Unresolved   int    `json:"unresolved"`
	Recovered    int    `json:"recovered"`
	Replayable   int    `json:"replayable"`
	ReplayFailed int    `json:"replay_failed"`
}

func newIntakeSummaryCmd() *cobra.Command {
	var (
		target       string
		provider     string
		status       string
		replayStatus string
		requestID    string
		unresolved   bool
		commands     bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Summarize recorded intake deliveries.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider != "" && provider != "linear" && provider != "github" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake summary: --provider must be linear or github.")
				return exitErr(2)
			}
			status = strings.ToLower(strings.TrimSpace(status))
			if status != "" && status != intakeDeliveryStatusOK && status != intakeDeliveryStatusError {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake summary: --status must be ok or error.")
				return exitErr(2)
			}
			var err error
			replayStatus, err = parseIntakeReplayStatusFilter(replayStatus)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake summary: %v\n", err)
				return exitErr(2)
			}
			requestID = strings.TrimSpace(requestID)
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake summary: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake summary: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake summary: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeSummaryFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake summary: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			deliveries, err := listIntakeDeliveries(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake summary: %v\n", err)
				return exitErr(1)
			}
			deliveries = filterIntakeDeliveries(deliveries, intakeDeliveryFilter{
				Provider:     provider,
				Status:       status,
				ReplayStatus: replayStatus,
				RequestID:    requestID,
				Unresolved:   unresolved,
			})
			summary := summarizeIntakeDeliveries(deliveries)
			return renderIntakeSummary(cmd.OutOrStdout(), summary, jsonOut, tmpl, commands, operatorCommandScopeFromCommand(cmd, target, "target"))
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&provider, "provider", "", "Only summarize deliveries for a provider: linear or github.")
	cmd.Flags().StringVar(&status, "status", "", "Only summarize deliveries with a status: ok or error.")
	cmd.Flags().StringVar(&replayStatus, "replay-status", "", "Only summarize deliveries with replay status: ok, error, none, or any.")
	cmd.Flags().StringVar(&requestID, "request-id", "", "Only summarize deliveries with this provider request id, such as X-GitHub-Delivery.")
	cmd.Flags().BoolVar(&unresolved, "unresolved", false, "Only summarize failed deliveries that still need replay.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, one per line.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit summary as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the summary with a Go template, e.g. '{{.Unresolved}} {{.Replayable}}'.")
	return cmd
}

func newIntakePruneCmd() *cobra.Command {
	var (
		target       string
		status       string
		replayStatus string
		olderThan    time.Duration
		dryRun       bool
		commands     bool
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune recorded intake deliveries.",
		Long:  "Prune recorded intake deliveries. By default this removes successful deliveries and keeps failures for recovery.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake prune: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake prune: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake prune: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if commands && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake prune: --commands requires --dry-run.")
				return exitErr(2)
			}
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake prune: --older-than must be >= 0.")
				return exitErr(2)
			}
			status, err := parseIntakePruneStatus(status)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake prune: %v\n", err)
				return exitErr(2)
			}
			replayStatus, err = parseIntakeReplayStatusFilter(replayStatus)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake prune: %v\n", err)
				return exitErr(2)
			}
			if replayStatus != "any" && !cmd.Flags().Changed("status") {
				status = "all"
			}
			tmpl, err := parseIntakePruneFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake prune: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			results, err := pruneIntakeDeliveries(teamDir, intakeDeliveryFilter{
				Status:       status,
				ReplayStatus: replayStatus,
			}, olderThan, time.Now().UTC(), dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake prune: %v\n", err)
				return exitErr(1)
			}
			if commands {
				return renderIntakePruneApplyCommand(cmd.OutOrStdout(), results, intakePruneApplyCommandOptions{
					Repo:            intakeCommandRepo(cmd, target),
					RepoSet:         intakeCommandRepoSet(cmd),
					RepoFlag:        intakeCommandRepoFlag(cmd),
					Status:          status,
					StatusSet:       cmd.Flags().Changed("status"),
					ReplayStatus:    replayStatus,
					ReplayStatusSet: cmd.Flags().Changed("replay-status"),
					OlderThan:       olderThan,
					OlderThanSet:    cmd.Flags().Changed("older-than"),
				})
			}
			return renderIntakePruneResults(cmd.OutOrStdout(), results, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&status, "status", intakeDeliveryStatusOK, "Delivery status to prune: ok, error, or all.")
	cmd.Flags().StringVar(&replayStatus, "replay-status", "", "Only prune deliveries with replay status: ok, error, none, or any. Defaults --status to all when set.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only prune deliveries older than this duration.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview deliveries that would be pruned without rewriting the ledger.")
	cmd.Flags().BoolVar(&commands, "commands", false, "With --dry-run, print the apply command, one per line. agent-team follow-ups preserve the selected repo scope.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit prune results as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each prune result with a Go template, e.g. '{{.ID}} {{.Status}} {{.Dropped}}'.")
	return cmd
}

func intakeDeliveryLogPath(teamDir string) string {
	return filepath.Join(teamDir, "daemon", "intake.jsonl")
}

func intakeDeliveryLockPath(teamDir string) string {
	return filepath.Join(teamDir, "daemon", "intake.lock")
}

func withIntakeDeliveryExclusiveLock(teamDir string, fn func() error) error {
	path := intakeDeliveryLogPath(teamDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("intake deliveries: mkdir: %w", err)
	}
	intakeDeliveryLogMu.Lock()
	defer intakeDeliveryLogMu.Unlock()
	lockFile, err := os.OpenFile(intakeDeliveryLockPath(teamDir), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("intake deliveries: lock open: %w", err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("intake deliveries: lock: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	return fn()
}

func newIntakeDeliveryRecord(provider string, r *http.Request, now time.Time, dryRun bool) intakeDelivery {
	return intakeDelivery{
		ID:         nextIntakeDeliveryID(now),
		Time:       now.UTC(),
		Provider:   provider,
		Method:     r.Method,
		Path:       r.URL.Path,
		RemoteAddr: r.RemoteAddr,
		RequestID:  providerWebhookRequestID(provider, r.Header),
		DryRun:     dryRun,
	}
}

func providerWebhookRequestID(provider string, header http.Header) string {
	switch provider {
	case "github":
		return strings.TrimSpace(header.Get("X-GitHub-Delivery"))
	default:
		return ""
	}
}

func cloneIntakePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		out[key] = value
	}
	return out
}

func nextIntakeDeliveryID(now time.Time) string {
	return fmt.Sprintf("%s-%06d", now.UTC().Format("20060102T150405.000000000Z"), intakeDeliverySeq.Add(1))
}

func appendIntakeDelivery(teamDir string, delivery intakeDelivery) error {
	if strings.TrimSpace(delivery.ID) == "" {
		delivery.ID = nextIntakeDeliveryID(time.Now())
	}
	if strings.TrimSpace(delivery.Status) == "" {
		delivery.Status = intakeDeliveryStatusOK
	}
	return withIntakeDeliveryExclusiveLock(teamDir, func() error {
		f, err := os.OpenFile(intakeDeliveryLogPath(teamDir), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("intake deliveries: open: %w", err)
		}
		encErr := json.NewEncoder(f).Encode(delivery)
		closeErr := f.Close()
		if encErr != nil {
			return fmt.Errorf("intake deliveries: encode: %w", encErr)
		}
		if closeErr != nil {
			return fmt.Errorf("intake deliveries: close: %w", closeErr)
		}
		return nil
	})
}

func listIntakeDeliveries(teamDir string) ([]intakeDelivery, error) {
	f, err := os.Open(intakeDeliveryLogPath(teamDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var deliveries []intakeDelivery
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var delivery intakeDelivery
		if err := json.Unmarshal([]byte(text), &delivery); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(deliveries, func(i, j int) bool {
		return deliveries[i].Time.Before(deliveries[j].Time)
	})
	return deliveries, nil
}

func writeIntakeDeliveries(teamDir string, deliveries []intakeDelivery) error {
	path := intakeDeliveryLogPath(teamDir)
	if len(deliveries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("intake deliveries: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".intake-*.jsonl")
	if err != nil {
		return fmt.Errorf("intake deliveries: temp: %w", err)
	}
	tmpPath := tmp.Name()
	enc := json.NewEncoder(tmp)
	for _, delivery := range deliveries {
		if err := enc.Encode(delivery); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("intake deliveries: encode: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("intake deliveries: close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("intake deliveries: replace: %w", err)
	}
	return nil
}

func findIntakeDelivery(teamDir, id string) (intakeDelivery, bool, error) {
	id = strings.TrimSpace(id)
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		return intakeDelivery{}, false, err
	}
	for i := len(deliveries) - 1; i >= 0; i-- {
		if deliveries[i].ID == id {
			return deliveries[i], true, nil
		}
	}
	return intakeDelivery{}, false, nil
}

func eventFromIntakeDelivery(delivery intakeDelivery) (*intake.Event, error) {
	if strings.TrimSpace(delivery.EventType) == "" || len(delivery.Payload) == 0 {
		return nil, fmt.Errorf("delivery %q has no recorded normalized event payload", delivery.ID)
	}
	return &intake.Event{
		Type:    delivery.EventType,
		Payload: delivery.Payload,
	}, nil
}

type intakeDeliveryFilter struct {
	Provider     string
	Status       string
	ReplayStatus string
	RequestID    string
	Unresolved   bool
}

func filterIntakeDeliveries(deliveries []intakeDelivery, filter intakeDeliveryFilter) []intakeDelivery {
	if filter.Provider == "" && filter.Status == "" && filter.ReplayStatus == "any" && filter.RequestID == "" && !filter.Unresolved {
		return deliveries
	}
	out := deliveries[:0]
	for _, delivery := range deliveries {
		if !intakeDeliveryMatchesFilter(delivery, filter) {
			continue
		}
		out = append(out, delivery)
	}
	return out
}

func duplicateIntakeRequestIDs(deliveries []intakeDelivery, providerFilter, requestIDFilter string) []intakeDuplicateRequest {
	type key struct {
		provider  string
		requestID string
	}
	groups := map[key]*intakeDuplicateRequest{}
	for _, delivery := range deliveries {
		provider := strings.ToLower(strings.TrimSpace(delivery.Provider))
		requestID := strings.TrimSpace(delivery.RequestID)
		if provider == "" || requestID == "" {
			continue
		}
		if providerFilter != "" && provider != providerFilter {
			continue
		}
		if requestIDFilter != "" && requestID != requestIDFilter {
			continue
		}
		k := key{provider: provider, requestID: requestID}
		row := groups[k]
		if row == nil {
			row = &intakeDuplicateRequest{
				Provider:  provider,
				RequestID: requestID,
				FirstID:   delivery.ID,
				FirstSeen: delivery.Time,
			}
			groups[k] = row
		}
		row.Count++
		row.IDs = append(row.IDs, delivery.ID)
		row.LastID = delivery.ID
		row.LastSeen = delivery.Time
	}
	out := make([]intakeDuplicateRequest, 0, len(groups))
	for _, row := range groups {
		if row.Count < 2 {
			continue
		}
		row.Actions = []string{fmt.Sprintf(
			"agent-team intake deliveries --provider %s --request-id %s",
			shellQuote(row.Provider),
			shellQuote(row.RequestID),
		)}
		out = append(out, *row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].RequestID < out[j].RequestID
	})
	return out
}

func intakeDeliveryMatchesFilter(delivery intakeDelivery, filter intakeDeliveryFilter) bool {
	if filter.Provider != "" && delivery.Provider != filter.Provider {
		return false
	}
	if filter.RequestID != "" && delivery.RequestID != filter.RequestID {
		return false
	}
	if filter.Status != "" && filter.Status != "all" && delivery.Status != filter.Status {
		return false
	}
	if filter.Unresolved && !intakeDeliveryNeedsReplay(delivery) {
		return false
	}
	return intakeDeliveryReplayStatusMatches(delivery, filter.ReplayStatus)
}

func intakeDeliveryReplayStatusMatches(delivery intakeDelivery, replayStatus string) bool {
	switch replayStatus {
	case "", "any":
		return true
	case "none":
		return strings.TrimSpace(delivery.ReplayStatus) == ""
	default:
		return delivery.ReplayStatus == replayStatus
	}
}

func tailIntakeDeliveries(deliveries []intakeDelivery, tail int) []intakeDelivery {
	if tail <= 0 || tail >= len(deliveries) {
		return deliveries
	}
	return deliveries[len(deliveries)-tail:]
}

func parseIntakeReplayStatusFilter(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "", "any":
		return "any", nil
	case intakeDeliveryReplayStatusOK, intakeDeliveryReplayStatusError, "none":
		return status, nil
	default:
		return "", fmt.Errorf("--replay-status must be ok, error, none, or any")
	}
}

func parseIntakeReplayStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "", intakeDeliveryStatusError:
		return intakeDeliveryStatusError, nil
	case intakeDeliveryStatusOK, "all":
		return status, nil
	default:
		return "", fmt.Errorf("--status must be ok, error, or all")
	}
}

func parseIntakePruneStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "", intakeDeliveryStatusOK:
		return intakeDeliveryStatusOK, nil
	case intakeDeliveryStatusError, "all":
		return status, nil
	default:
		return "", fmt.Errorf("--status must be ok, error, or all")
	}
}

func replayAllIntakeDeliveries(teamDir, provider, status string, limit int, dedupeRequest bool, dryRun, previewRoutes bool) (intakeReplayBatchResult, error) {
	deliveries, err := listIntakeDeliveries(teamDir)
	if err != nil {
		return intakeReplayBatchResult{}, err
	}
	deliveries, skippedDuplicates := selectIntakeReplayDeliveries(deliveries, provider, status, limit, dedupeRequest)
	batch := intakeReplayBatchResult{
		DryRun:                     dryRun,
		Total:                      len(deliveries),
		SkippedDuplicateRequestIDs: skippedDuplicates,
		Results:                    make([]intakeReplayResult, 0, len(deliveries)),
	}
	var dc *daemonClient
	if !dryRun && len(deliveries) > 0 {
		dc, err = newDaemonClient(teamDir)
		if err != nil {
			return batch, err
		}
	}
	for _, delivery := range deliveries {
		result := replayOneIntakeDelivery(teamDir, dc, delivery, dryRun, previewRoutes)
		if result.OK {
			batch.Succeeded++
		} else {
			batch.Failed++
		}
		batch.Results = append(batch.Results, result)
	}
	if !dryRun && len(batch.Results) > 0 {
		if err := markIntakeDeliveryReplays(teamDir, batch.Results, time.Now().UTC()); err != nil {
			return batch, err
		}
	}
	return batch, nil
}

func selectIntakeReplayDeliveries(deliveries []intakeDelivery, provider, status string, limit int, dedupeRequest bool) ([]intakeDelivery, int) {
	out := make([]intakeDelivery, 0, len(deliveries))
	seenRequests := map[string]bool{}
	skippedDuplicates := 0
	for _, delivery := range deliveries {
		if provider != "" && delivery.Provider != provider {
			continue
		}
		switch status {
		case intakeDeliveryStatusError:
			if !intakeDeliveryNeedsReplay(delivery) {
				continue
			}
		case intakeDeliveryStatusOK:
			if delivery.Status != intakeDeliveryStatusOK {
				continue
			}
		}
		if dedupeRequest {
			key := intakeDeliveryRequestKey(delivery)
			if key != "" {
				if seenRequests[key] {
					skippedDuplicates++
					continue
				}
				seenRequests[key] = true
			}
		}
		out = append(out, delivery)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, skippedDuplicates
}

func intakeDeliveryRequestKey(delivery intakeDelivery) string {
	provider := strings.ToLower(strings.TrimSpace(delivery.Provider))
	requestID := strings.TrimSpace(delivery.RequestID)
	if provider == "" || requestID == "" {
		return ""
	}
	return provider + "\x00" + requestID
}

func replayOneIntakeDelivery(teamDir string, dc *daemonClient, delivery intakeDelivery, dryRun, previewRoutes bool) intakeReplayResult {
	result := intakeReplayResult{
		DeliveryID: delivery.ID,
		Provider:   delivery.Provider,
		Status:     delivery.Status,
		HTTPStatus: delivery.HTTPStatus,
		DryRun:     dryRun,
	}
	ev, err := eventFromIntakeDelivery(delivery)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Event = ev
	if dryRun {
		if previewRoutes {
			preview, err := previewEventPublish(teamDir, ev.Type, ev.Payload)
			if err != nil {
				result.Error = err.Error()
				return result
			}
			result.Preview = preview
		}
		result.OK = true
		return result
	}
	outcome, err := dc.PublishEvent(ev.Type, ev.Payload)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Outcome = outcome
	result.OK = true
	return result
}

func markIntakeDeliveryReplays(teamDir string, results []intakeReplayResult, now time.Time) error {
	if len(results) == 0 {
		return nil
	}
	byID := make(map[string]intakeReplayResult, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.DeliveryID) == "" {
			continue
		}
		byID[result.DeliveryID] = result
	}
	if len(byID) == 0 {
		return nil
	}
	now = now.UTC()
	return withIntakeDeliveryExclusiveLock(teamDir, func() error {
		deliveries, err := listIntakeDeliveries(teamDir)
		if err != nil {
			return err
		}
		changed := false
		for i := range deliveries {
			result, ok := byID[deliveries[i].ID]
			if !ok {
				continue
			}
			deliveries[i].ReplayStatus = intakeDeliveryReplayStatusError
			deliveries[i].ReplayError = strings.TrimSpace(result.Error)
			if result.OK {
				deliveries[i].ReplayStatus = intakeDeliveryReplayStatusOK
				deliveries[i].ReplayError = ""
			} else if deliveries[i].ReplayError == "" {
				deliveries[i].ReplayError = "replay failed"
			}
			replayedAt := now
			deliveries[i].ReplayedAt = &replayedAt
			changed = true
		}
		if !changed {
			return nil
		}
		return writeIntakeDeliveries(teamDir, deliveries)
	})
}

func pruneIntakeDeliveries(teamDir string, filter intakeDeliveryFilter, olderThan time.Duration, now time.Time, dryRun bool) ([]intakePruneResult, error) {
	results := []intakePruneResult{}
	err := withIntakeDeliveryExclusiveLock(teamDir, func() error {
		deliveries, err := listIntakeDeliveries(teamDir)
		if err != nil {
			return err
		}
		retained := make([]intakeDelivery, 0, len(deliveries))
		for _, delivery := range deliveries {
			if !intakeDeliveryPruneMatch(delivery, filter, olderThan, now) {
				retained = append(retained, delivery)
				continue
			}
			results = append(results, intakePruneResult{
				ID:           delivery.ID,
				Time:         delivery.Time,
				Provider:     delivery.Provider,
				Status:       delivery.Status,
				ReplayStatus: delivery.ReplayStatus,
				HTTPStatus:   delivery.HTTPStatus,
				EventType:    delivery.EventType,
				Ticket:       delivery.Ticket,
				PR:           delivery.PR,
				DryRun:       dryRun,
				Dropped:      !dryRun,
			})
			if dryRun {
				retained = append(retained, delivery)
			}
		}
		if dryRun || len(results) == 0 {
			return nil
		}
		return writeIntakeDeliveries(teamDir, retained)
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func intakeDeliveryPruneMatch(delivery intakeDelivery, filter intakeDeliveryFilter, olderThan time.Duration, now time.Time) bool {
	if !intakeDeliveryMatchesFilter(delivery, filter) {
		return false
	}
	if olderThan <= 0 {
		return true
	}
	if delivery.Time.IsZero() {
		return false
	}
	return !delivery.Time.After(now.UTC().Add(-olderThan))
}

func withIntakeDeliveryActions(deliveries []intakeDelivery) []intakeDelivery {
	out := make([]intakeDelivery, len(deliveries))
	copy(out, deliveries)
	for i := range out {
		out[i].Actions = intakeDeliveryActions(out[i])
	}
	return out
}

func intakeDeliveryActions(delivery intakeDelivery) []string {
	if !intakeDeliveryNeedsReplay(delivery) {
		return nil
	}
	if strings.TrimSpace(delivery.EventType) != "" && len(delivery.Payload) > 0 {
		return []string{
			fmt.Sprintf("agent-team intake replay %s --dry-run --preview-triggers", delivery.ID),
			fmt.Sprintf("agent-team intake replay %s", delivery.ID),
		}
	}
	return []string{"inspect webhook source; no normalized event payload was recorded"}
}

func intakeDeliveryNeedsReplay(delivery intakeDelivery) bool {
	return delivery.Status == intakeDeliveryStatusError && delivery.ReplayStatus != intakeDeliveryReplayStatusOK
}

func summarizeIntakeDeliveries(deliveries []intakeDelivery) intakeSummaryResult {
	out := intakeSummaryResult{Deliveries: len(deliveries)}
	byProvider := map[string]*intakeProviderSummary{}
	var latest time.Time
	for _, delivery := range deliveries {
		provider := strings.TrimSpace(delivery.Provider)
		providerSummary := byProvider[provider]
		if providerSummary == nil {
			providerSummary = &intakeProviderSummary{Provider: provider}
			byProvider[provider] = providerSummary
		}
		out.addDelivery(delivery, &latest)
		providerSummary.addDelivery(delivery)
	}
	providers := make([]intakeProviderSummary, 0, len(byProvider))
	for _, provider := range byProvider {
		providers = append(providers, *provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Provider < providers[j].Provider
	})
	out.Providers = providers
	out.DuplicateRequestIDs = len(duplicateIntakeRequestIDs(deliveries, "", ""))
	out.Actions = intakeSummaryActions(out)
	return out
}

func (s *intakeSummaryResult) addDelivery(delivery intakeDelivery, latest *time.Time) {
	switch delivery.Status {
	case intakeDeliveryStatusOK:
		s.OK++
	case intakeDeliveryStatusError:
		s.Failed++
		if delivery.ReplayStatus == intakeDeliveryReplayStatusOK {
			s.Recovered++
			return
		}
		s.Unresolved++
		if delivery.ReplayStatus == intakeDeliveryReplayStatusError {
			s.ReplayFailed++
		}
		if strings.TrimSpace(delivery.EventType) != "" && len(delivery.Payload) > 0 {
			s.Replayable++
		}
		if latest != nil && (s.LatestErrorID == "" || delivery.Time.After(*latest)) {
			*latest = delivery.Time
			s.LatestErrorID = delivery.ID
			s.LatestError = delivery.Error
		}
	}
}

func (s *intakeProviderSummary) addDelivery(delivery intakeDelivery) {
	s.Deliveries++
	switch delivery.Status {
	case intakeDeliveryStatusOK:
		s.OK++
	case intakeDeliveryStatusError:
		s.Failed++
		if delivery.ReplayStatus == intakeDeliveryReplayStatusOK {
			s.Recovered++
			return
		}
		s.Unresolved++
		if delivery.ReplayStatus == intakeDeliveryReplayStatusError {
			s.ReplayFailed++
		}
		if strings.TrimSpace(delivery.EventType) != "" && len(delivery.Payload) > 0 {
			s.Replayable++
		}
	}
}

func intakeSummaryActions(summary intakeSummaryResult) []string {
	var actions []string
	if summary.Unresolved > 0 {
		actions = append(actions, "agent-team intake deliveries --unresolved")
	}
	if summary.Replayable > 0 {
		actions = append(actions,
			intakeReplayAllDryRunAction(),
			intakeReplayAllAction(),
		)
	}
	if summary.Recovered > 0 {
		actions = append(actions, "agent-team intake prune --replay-status ok --dry-run")
	}
	if summary.DuplicateRequestIDs > 0 {
		actions = append(actions, "agent-team intake duplicates")
	}
	return actions
}

func intakeReplayAllDryRunAction() string {
	return "agent-team intake replay --all --dedupe-request-id --dry-run --preview-triggers"
}

func intakeReplayAllAction() string {
	return "agent-team intake replay --all --dedupe-request-id"
}

func parseIntakeDeliveryFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("intake-delivery-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseIntakeDuplicateFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("intake-duplicate-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseIntakeSummaryFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("intake-summary-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func parseIntakePruneFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("intake-prune-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderIntakeDeliveries(w io.Writer, deliveries []intakeDelivery, jsonOut bool, tmpl *template.Template, commands bool, scope operatorCommandScope) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(deliveries)
	}
	if commands {
		return renderOperatorActionCommands(w, intakeDeliveryCommandActions(deliveries), scope)
	}
	if tmpl != nil {
		for _, delivery := range deliveries {
			if err := tmpl.Execute(w, delivery); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(deliveries) == 0 {
		_, err := fmt.Fprintln(w, "(no intake deliveries)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tID\tREQUEST_ID\tPROVIDER\tSTATUS\tREPLAY\tHTTP\tEVENT\tTICKET\tPR\tACTIONS\tERROR")
	for _, delivery := range deliveries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			delivery.Time.Format(time.RFC3339),
			delivery.ID,
			emptyDash(delivery.RequestID),
			emptyDash(delivery.Provider),
			delivery.Status,
			emptyDash(delivery.ReplayStatus),
			delivery.HTTPStatus,
			emptyDash(delivery.EventType),
			emptyDash(delivery.Ticket),
			emptyDash(delivery.PR),
			emptyDash(strings.Join(delivery.Actions, "; ")),
			emptyDash(delivery.Error),
		)
	}
	return tw.Flush()
}

func renderIntakeDuplicates(w io.Writer, rows []intakeDuplicateRequest, jsonOut bool, tmpl *template.Template, commands bool, scope operatorCommandScope) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(rows)
	}
	if commands {
		return renderOperatorActionCommands(w, intakeDuplicateCommandActions(rows), scope)
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
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no duplicate provider request ids)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tREQUEST_ID\tCOUNT\tFIRST\tLAST\tIDS\tACTION")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			emptyDash(row.Provider),
			emptyDash(row.RequestID),
			row.Count,
			intakeDuplicateTime(row.FirstSeen),
			intakeDuplicateTime(row.LastSeen),
			emptyDash(strings.Join(row.IDs, ",")),
			emptyDash(strings.Join(row.Actions, "; ")),
		)
	}
	return tw.Flush()
}

func intakeDeliveryCommandActions(deliveries []intakeDelivery) []string {
	var actions []string
	for _, delivery := range deliveries {
		actions = append(actions, commandActionsOnly(delivery.Actions)...)
	}
	return actions
}

func intakeDuplicateCommandActions(rows []intakeDuplicateRequest) []string {
	var actions []string
	for _, row := range rows {
		actions = append(actions, commandActionsOnly(row.Actions)...)
	}
	return actions
}

func intakeDuplicateTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func renderIntakeSummary(w io.Writer, summary intakeSummaryResult, jsonOut bool, tmpl *template.Template, commands bool, scope operatorCommandScope) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(summary)
	}
	if commands {
		return renderOperatorActionCommands(w, summary.Actions, scope)
	}
	if tmpl != nil {
		if err := tmpl.Execute(w, summary); err != nil {
			return err
		}
		_, err := fmt.Fprintln(w)
		return err
	}
	fmt.Fprintf(w, "intake: deliveries=%d ok=%d failed=%d unresolved=%d recovered=%d replayable=%d replay_failed=%d latest_error=%s duplicate_request_ids=%d\n",
		summary.Deliveries,
		summary.OK,
		summary.Failed,
		summary.Unresolved,
		summary.Recovered,
		summary.Replayable,
		summary.ReplayFailed,
		emptyDash(summary.LatestErrorID),
		summary.DuplicateRequestIDs)
	if len(summary.Providers) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PROVIDER\tDELIVERIES\tOK\tFAILED\tUNRESOLVED\tRECOVERED\tREPLAYABLE\tREPLAY_FAILED")
		for _, provider := range summary.Providers {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
				emptyDash(provider.Provider),
				provider.Deliveries,
				provider.OK,
				provider.Failed,
				provider.Unresolved,
				provider.Recovered,
				provider.Replayable,
				provider.ReplayFailed)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(summary.Actions) == 0 {
		_, err := fmt.Fprintln(w, "actions: none")
		return err
	}
	fmt.Fprintln(w, "actions:")
	for _, action := range summary.Actions {
		fmt.Fprintf(w, "  %s\n", action)
	}
	return nil
}

type intakeReplayApplyCommandOptions struct {
	Repo     string
	RepoSet  bool
	RepoFlag string
}

func renderIntakeReplayApplyCommand(w io.Writer, deliveryID string, opts intakeReplayApplyCommandOptions) error {
	deliveryID = strings.TrimSpace(deliveryID)
	if deliveryID == "" {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(intakeReplayApplyCommandArgs(deliveryID, opts)), " "))
	return err
}

func intakeReplayApplyCommandArgs(deliveryID string, opts intakeReplayApplyCommandOptions) []string {
	args := []string{"agent-team", "intake", "replay", deliveryID}
	return appendIntakeRepoArgs(args, opts.RepoFlag, opts.Repo, opts.RepoSet)
}

type intakeReplayAllApplyCommandOptions struct {
	Repo          string
	RepoSet       bool
	RepoFlag      string
	Provider      string
	ProviderSet   bool
	Status        string
	StatusSet     bool
	Limit         int
	LimitSet      bool
	DedupeRequest bool
}

func renderIntakeReplayAllApplyCommand(w io.Writer, batch intakeReplayBatchResult, opts intakeReplayAllApplyCommandOptions) error {
	if len(batch.Results) == 0 {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(intakeReplayAllApplyCommandArgs(opts)), " "))
	return err
}

func intakeReplayAllApplyCommandArgs(opts intakeReplayAllApplyCommandOptions) []string {
	args := []string{"agent-team", "intake", "replay", "--all"}
	args = appendIntakeRepoArgs(args, opts.RepoFlag, opts.Repo, opts.RepoSet)
	if opts.ProviderSet && strings.TrimSpace(opts.Provider) != "" {
		args = append(args, "--provider", opts.Provider)
	}
	if opts.StatusSet && strings.TrimSpace(opts.Status) != "" {
		args = append(args, "--status", opts.Status)
	}
	if opts.LimitSet {
		args = append(args, "--limit", fmt.Sprint(opts.Limit))
	}
	if opts.DedupeRequest {
		args = append(args, "--dedupe-request-id")
	}
	return args
}

type intakePruneApplyCommandOptions struct {
	Repo            string
	RepoSet         bool
	RepoFlag        string
	Status          string
	StatusSet       bool
	ReplayStatus    string
	ReplayStatusSet bool
	OlderThan       time.Duration
	OlderThanSet    bool
}

func renderIntakePruneApplyCommand(w io.Writer, results []intakePruneResult, opts intakePruneApplyCommandOptions) error {
	if len(results) == 0 {
		return nil
	}
	_, err := fmt.Fprintln(w, strings.Join(shellQuoteArgs(intakePruneApplyCommandArgs(opts)), " "))
	return err
}

func intakePruneApplyCommandArgs(opts intakePruneApplyCommandOptions) []string {
	args := []string{"agent-team", "intake", "prune"}
	args = appendIntakeRepoArgs(args, opts.RepoFlag, opts.Repo, opts.RepoSet)
	if opts.StatusSet && strings.TrimSpace(opts.Status) != "" {
		args = append(args, "--status", opts.Status)
	}
	if opts.ReplayStatusSet && strings.TrimSpace(opts.ReplayStatus) != "" {
		args = append(args, "--replay-status", opts.ReplayStatus)
	}
	if opts.OlderThanSet {
		args = append(args, "--older-than", opts.OlderThan.String())
	}
	return args
}

func appendIntakeRepoArgs(args []string, repoFlag, repo string, repoSet bool) []string {
	if !repoSet || strings.TrimSpace(repo) == "" {
		return args
	}
	flag := strings.TrimSpace(repoFlag)
	if flag == "" {
		flag = "target"
	}
	return append(args, "--"+flag, repo)
}

func intakeCommandRepoSet(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
		return true
	}
	return cmd.Flags().Changed("target")
}

func intakeCommandRepoFlag(cmd *cobra.Command) string {
	return rootRepoFlagName
}

func intakeCommandRepo(cmd *cobra.Command, target string) string {
	if cmd != nil {
		if flag := cmd.Root().PersistentFlags().Lookup(rootRepoFlagName); flag != nil && flag.Changed {
			if value := strings.TrimSpace(flag.Value.String()); value != "" {
				return value
			}
		}
	}
	return target
}

func renderIntakeReplayBatch(w io.Writer, batch intakeReplayBatchResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(batch)
	}
	if tmpl != nil {
		for _, result := range batch.Results {
			if err := tmpl.Execute(w, result); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		return nil
	}
	if len(batch.Results) == 0 {
		_, err := fmt.Fprintln(w, "(no intake deliveries matched)")
		return err
	}
	fmt.Fprintf(w, "replay: total=%d succeeded=%d failed=%d skipped_duplicate_request_ids=%d\n",
		batch.Total,
		batch.Succeeded,
		batch.Failed,
		batch.SkippedDuplicateRequestIDs)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROVIDER\tSTATUS\tHTTP\tEVENT\tDRY_RUN\tOK\tMATCHED\tERROR")
	for _, result := range batch.Results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			result.DeliveryID,
			emptyDash(result.Provider),
			emptyDash(result.Status),
			result.HTTPStatus,
			intakeReplayResultEventType(result),
			yesNo(result.DryRun),
			yesNo(result.OK),
			emptyDash(strings.Join(intakeReplayResultMatched(result), ", ")),
			emptyDash(result.Error),
		)
	}
	return tw.Flush()
}

func intakeReplayResultEventType(result intakeReplayResult) string {
	if result.Event == nil {
		return "-"
	}
	return emptyDash(result.Event.Type)
}

func intakeReplayResultMatched(result intakeReplayResult) []string {
	if result.Outcome != nil {
		return result.Outcome.Matched
	}
	if result.Preview != nil {
		return result.Preview.Matched
	}
	return nil
}

func renderIntakePruneResults(w io.Writer, results []intakePruneResult, jsonOut bool, tmpl *template.Template) error {
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
		_, err := fmt.Fprintln(w, "(no intake deliveries matched)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROVIDER\tSTATUS\tREPLAY\tHTTP\tEVENT\tTICKET\tDROPPED")
	for _, result := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			result.ID,
			emptyDash(result.Provider),
			result.Status,
			emptyDash(result.ReplayStatus),
			result.HTTPStatus,
			emptyDash(result.EventType),
			emptyDash(result.Ticket),
			yesNo(result.Dropped),
		)
	}
	return tw.Flush()
}
