package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

type intakeDoctorFinding struct {
	Line    int      `json:"line,omitempty"`
	ID      string   `json:"id,omitempty"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Actions []string `json:"actions,omitempty"`
}

type intakeDoctorResult struct {
	OK         bool                  `json:"ok"`
	Path       string                `json:"path"`
	Exists     bool                  `json:"exists"`
	Deliveries int                   `json:"deliveries"`
	Summary    intakeSummaryResult   `json:"summary"`
	Problems   []intakeDoctorFinding `json:"problems,omitempty"`
	Warnings   []intakeDoctorFinding `json:"warnings,omitempty"`
}

func newIntakeDoctorCmd() *cobra.Command {
	var (
		target   string
		jsonOut  bool
		format   string
		commands bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate the recorded intake delivery ledger.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake doctor: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team intake doctor: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			tmpl, err := parseIntakeDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake doctor: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := collectIntakeDoctor(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team intake doctor: %v\n", err)
				return exitErr(1)
			}
			if err := renderIntakeDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl, commands, operatorCommandScopeFromCommand(cmd, target, "target")); err != nil {
				return err
			}
			if !result.OK {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit ledger doctor findings as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the intake doctor result with a Go template, e.g. '{{.OK}} {{len .Problems}}'.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, one per line.")
	return cmd
}

func collectIntakeDoctor(teamDir string) (intakeDoctorResult, error) {
	path := intakeDeliveryLogPath(teamDir)
	result := intakeDoctorResult{
		OK:   true,
		Path: path,
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.Summary = summarizeIntakeDeliveries(nil)
			return result, nil
		}
		return result, err
	}
	defer f.Close()
	result.Exists = true
	deliveries, problems, warnings := inspectIntakeDeliveryLedger(f)
	result.Deliveries = len(deliveries)
	result.Summary = summarizeIntakeDeliveries(deliveries)
	result.Problems = problems
	result.Warnings = warnings
	result.OK = len(problems) == 0
	return result, nil
}

func inspectIntakeDeliveryLedger(r io.Reader) ([]intakeDelivery, []intakeDoctorFinding, []intakeDoctorFinding) {
	var deliveries []intakeDelivery
	var problems []intakeDoctorFinding
	var warnings []intakeDoctorFinding
	seenIDs := map[string]int{}
	seenRequestIDs := map[string]int{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var delivery intakeDelivery
		if err := json.Unmarshal([]byte(text), &delivery); err != nil {
			problems = append(problems, intakeDoctorFinding{
				Line:    line,
				Code:    "invalid_json",
				Message: fmt.Sprintf("line %d is not valid JSON: %v", line, err),
			})
			continue
		}
		validateIntakeDeliveryRecord(line, delivery, seenIDs, seenRequestIDs, &problems, &warnings)
		deliveries = append(deliveries, delivery)
	}
	if err := scanner.Err(); err != nil {
		problems = append(problems, intakeDoctorFinding{
			Code:    "read_failed",
			Message: fmt.Sprintf("read intake ledger: %v", err),
		})
	}
	return deliveries, problems, warnings
}

func validateIntakeDeliveryRecord(line int, delivery intakeDelivery, seenIDs map[string]int, seenRequestIDs map[string]int, problems, warnings *[]intakeDoctorFinding) {
	id := strings.TrimSpace(delivery.ID)
	if id == "" {
		*problems = append(*problems, intakeDoctorFinding{
			Line:    line,
			Code:    "missing_id",
			Message: fmt.Sprintf("line %d has no delivery id", line),
		})
	} else if firstLine, ok := seenIDs[id]; ok {
		*problems = append(*problems, intakeDoctorFinding{
			Line:    line,
			ID:      id,
			Code:    "duplicate_id",
			Message: fmt.Sprintf("line %d duplicates delivery id %q from line %d", line, id, firstLine),
		})
	} else {
		seenIDs[id] = line
	}
	provider := strings.ToLower(strings.TrimSpace(delivery.Provider))
	requestID := strings.TrimSpace(delivery.RequestID)
	if provider != "" && requestID != "" {
		key := provider + "\x00" + requestID
		if firstLine, ok := seenRequestIDs[key]; ok {
			*warnings = append(*warnings, intakeDoctorFinding{
				Line:    line,
				ID:      id,
				Code:    "duplicate_request_id",
				Message: fmt.Sprintf("line %d duplicates %s request id %q from line %d", line, provider, requestID, firstLine),
				Actions: []string{fmt.Sprintf(
					"agent-team intake duplicates --provider %s --request-id %s",
					shellQuote(provider),
					shellQuote(requestID),
				)},
			})
		} else {
			seenRequestIDs[key] = line
		}
	}
	if delivery.Time.IsZero() {
		*warnings = append(*warnings, intakeDoctorFinding{
			Line:    line,
			ID:      id,
			Code:    "missing_time",
			Message: fmt.Sprintf("line %d delivery %s has no timestamp", line, emptyDash(id)),
		})
	}
	switch delivery.Status {
	case intakeDeliveryStatusOK, intakeDeliveryStatusError:
	case "":
		*problems = append(*problems, intakeDoctorFinding{
			Line:    line,
			ID:      id,
			Code:    "missing_status",
			Message: fmt.Sprintf("line %d delivery %s has no status", line, emptyDash(id)),
		})
	default:
		*problems = append(*problems, intakeDoctorFinding{
			Line:    line,
			ID:      id,
			Code:    "unknown_status",
			Message: fmt.Sprintf("line %d delivery %s has unknown status %q", line, emptyDash(id), delivery.Status),
		})
	}
	switch delivery.ReplayStatus {
	case "", intakeDeliveryReplayStatusOK, intakeDeliveryReplayStatusError:
	default:
		*problems = append(*problems, intakeDoctorFinding{
			Line:    line,
			ID:      id,
			Code:    "unknown_replay_status",
			Message: fmt.Sprintf("line %d delivery %s has unknown replay status %q", line, emptyDash(id), delivery.ReplayStatus),
		})
	}
	if delivery.Status != intakeDeliveryStatusError && strings.TrimSpace(delivery.ReplayStatus) != "" {
		*warnings = append(*warnings, intakeDoctorFinding{
			Line:    line,
			ID:      id,
			Code:    "unexpected_replay_status",
			Message: fmt.Sprintf("line %d delivery %s has replay status on a non-error delivery", line, emptyDash(id)),
		})
	}
	if intakeDeliveryNeedsReplay(delivery) && (strings.TrimSpace(delivery.EventType) == "" || len(delivery.Payload) == 0) {
		*warnings = append(*warnings, intakeDoctorFinding{
			Line:    line,
			ID:      id,
			Code:    "not_replayable",
			Message: fmt.Sprintf("line %d delivery %s cannot be replayed because it has no normalized event payload", line, emptyDash(id)),
		})
	}
}

func renderIntakeDoctor(stdout, stderr io.Writer, result intakeDoctorResult, jsonOut bool, tmpl *template.Template, commands bool, scope operatorCommandScope) error {
	if jsonOut {
		return json.NewEncoder(stdout).Encode(result)
	}
	if commands {
		return renderOperatorActionCommands(stdout, intakeDoctorActions(result), scope)
	}
	if tmpl != nil {
		return renderIntakeDoctorFormat(stdout, result, tmpl)
	}
	if result.OK {
		fmt.Fprintln(stdout, "agent-team intake doctor: OK")
		fmt.Fprintf(stdout, "intake: deliveries=%d ok=%d failed=%d unresolved=%d recovered=%d replayable=%d replay_failed=%d\n",
			result.Summary.Deliveries,
			result.Summary.OK,
			result.Summary.Failed,
			result.Summary.Unresolved,
			result.Summary.Recovered,
			result.Summary.Replayable,
			result.Summary.ReplayFailed)
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
			for _, action := range warning.Actions {
				fmt.Fprintf(stderr, "    action: %s\n", action)
			}
		}
		return nil
	}
	fmt.Fprintln(stderr, "agent-team intake doctor: problems found:")
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "  - %s\n", problem.Message)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
		for _, action := range warning.Actions {
			fmt.Fprintf(stderr, "    action: %s\n", action)
		}
	}
	return nil
}

func intakeDoctorActions(result intakeDoctorResult) []string {
	var actions []string
	for _, problem := range result.Problems {
		actions = append(actions, problem.Actions...)
	}
	for _, warning := range result.Warnings {
		actions = append(actions, warning.Actions...)
	}
	return actions
}

func intakeDoctorFindingMessage(finding intakeDoctorFinding) string {
	if len(finding.Actions) == 0 {
		return finding.Message
	}
	return finding.Message + " (action: " + strings.Join(finding.Actions, "; ") + ")"
}

func parseIntakeDoctorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("intake-doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderIntakeDoctorFormat(w io.Writer, result intakeDoctorResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
