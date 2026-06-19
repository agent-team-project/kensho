package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/jamesaud/agent-team/internal/intake"
	"github.com/spf13/cobra"
)

const (
	intakeDeliveryStatusOK    = "ok"
	intakeDeliveryStatusError = "error"
)

var (
	intakeDeliveryLogMu sync.Mutex
	intakeDeliverySeq   atomic.Uint64
)

type intakeDelivery struct {
	ID         string         `json:"id"`
	Time       time.Time      `json:"time"`
	Provider   string         `json:"provider,omitempty"`
	Method     string         `json:"method,omitempty"`
	Path       string         `json:"path,omitempty"`
	RemoteAddr string         `json:"remote_addr,omitempty"`
	EventType  string         `json:"event_type,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Ticket     string         `json:"ticket,omitempty"`
	PR         string         `json:"pr,omitempty"`
	JobID      string         `json:"job_id,omitempty"`
	Status     string         `json:"status"`
	HTTPStatus int            `json:"http_status"`
	Error      string         `json:"error,omitempty"`
	DryRun     bool           `json:"dry_run,omitempty"`
	Matched    []string       `json:"matched,omitempty"`
	Pipelines  []string       `json:"pipelines,omitempty"`
}

func newIntakeDeliveriesCmd() *cobra.Command {
	var (
		target   string
		provider string
		status   string
		tail     string
		jsonOut  bool
		format   string
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
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake deliveries: --format cannot be combined with --json.")
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
			deliveries = filterIntakeDeliveries(deliveries, provider, status)
			deliveries = tailIntakeDeliveries(deliveries, tailLines)
			return renderIntakeDeliveries(cmd.OutOrStdout(), deliveries, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().StringVar(&provider, "provider", "", "Only show deliveries for a provider: linear or github.")
	cmd.Flags().StringVar(&status, "status", "", "Only show deliveries with a status: ok or error.")
	cmd.Flags().StringVar(&tail, "tail", "20", "Show only the last N deliveries (0 or all = all).")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit deliveries as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each delivery with a Go template, e.g. '{{.Provider}} {{.Status}} {{.EventType}}'.")
	return cmd
}

func newIntakeReplayCmd() *cobra.Command {
	var (
		target        string
		dryRun        bool
		previewRoutes bool
		jsonOut       bool
		format        string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "replay <delivery-id>",
		Short: "Replay a recorded normalized intake delivery.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake replay: --format cannot be combined with --json.")
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
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
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
				return renderIntakeDryRun(cmd.OutOrStdout(), ev, jsonOut, tmpl, nil, nil, triggerPreview)
			}
			return publishIntakeEvent(cmd, target, ev, jsonOut, tmpl)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the normalized delivery without publishing it.")
	cmd.Flags().BoolVar(&previewRoutes, "preview-triggers", false, "With --dry-run, include local topology instance and pipeline matches.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit replay result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the replay result with a Go template, e.g. '{{.Event.Type}}'.")
	return cmd
}

func intakeDeliveryLogPath(teamDir string) string {
	return filepath.Join(teamDir, "daemon", "intake.jsonl")
}

func newIntakeDeliveryRecord(provider string, r *http.Request, now time.Time, dryRun bool) intakeDelivery {
	return intakeDelivery{
		ID:         nextIntakeDeliveryID(now),
		Time:       now.UTC(),
		Provider:   provider,
		Method:     r.Method,
		Path:       r.URL.Path,
		RemoteAddr: r.RemoteAddr,
		DryRun:     dryRun,
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
	path := intakeDeliveryLogPath(teamDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("intake deliveries: mkdir: %w", err)
	}
	intakeDeliveryLogMu.Lock()
	defer intakeDeliveryLogMu.Unlock()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
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

func filterIntakeDeliveries(deliveries []intakeDelivery, provider, status string) []intakeDelivery {
	if provider == "" && status == "" {
		return deliveries
	}
	out := deliveries[:0]
	for _, delivery := range deliveries {
		if provider != "" && delivery.Provider != provider {
			continue
		}
		if status != "" && delivery.Status != status {
			continue
		}
		out = append(out, delivery)
	}
	return out
}

func tailIntakeDeliveries(deliveries []intakeDelivery, tail int) []intakeDelivery {
	if tail <= 0 || tail >= len(deliveries) {
		return deliveries
	}
	return deliveries[len(deliveries)-tail:]
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

func renderIntakeDeliveries(w io.Writer, deliveries []intakeDelivery, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(deliveries)
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
	fmt.Fprintln(tw, "TIME\tID\tPROVIDER\tSTATUS\tHTTP\tEVENT\tTICKET\tPR\tERROR")
	for _, delivery := range deliveries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			delivery.Time.Format(time.RFC3339),
			delivery.ID,
			emptyDash(delivery.Provider),
			delivery.Status,
			delivery.HTTPStatus,
			emptyDash(delivery.EventType),
			emptyDash(delivery.Ticket),
			emptyDash(delivery.PR),
			emptyDash(delivery.Error),
		)
	}
	return tw.Flush()
}
