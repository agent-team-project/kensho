package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

type queueDoctorFinding struct {
	State   string `json:"state,omitempty"`
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type queueDoctorSummary struct {
	Files      int `json:"files"`
	Items      int `json:"items"`
	Valid      int `json:"valid"`
	Invalid    int `json:"invalid"`
	Pending    int `json:"pending"`
	Dead       int `json:"dead"`
	Ignored    int `json:"ignored"`
	Duplicates int `json:"duplicates"`
}

type queueDoctorResult struct {
	OK       bool                 `json:"ok"`
	Root     string               `json:"root"`
	Summary  queueDoctorSummary   `json:"summary"`
	Problems []queueDoctorFinding `json:"problems,omitempty"`
	Warnings []queueDoctorFinding `json:"warnings,omitempty"`
	Actions  []string             `json:"actions,omitempty"`
}

type queueDoctorSeen struct {
	State string
	Path  string
}

func newQueueDoctorCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate persisted queue files.",
		Long:  "Validate persisted daemon queue files without relying on normal queue listing paths.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := collectQueueDoctor(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue doctor: %v\n", err)
				return exitErr(1)
			}
			if err := renderQueueDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut); err != nil {
				return err
			}
			if !result.OK {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit queue doctor findings as JSON.")
	return cmd
}

func collectQueueDoctor(teamDir string) (queueDoctorResult, error) {
	root := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	result := queueDoctorResult{
		OK:   true,
		Root: root,
	}
	seen := map[string]queueDoctorSeen{}
	for _, state := range []string{daemon.QueueStatePending, daemon.QueueStateDead} {
		if err := inspectQueueDoctorState(root, state, seen, &result); err != nil {
			return result, err
		}
	}
	result.OK = len(result.Problems) == 0
	result.Actions = queueDoctorActions(result)
	return result, nil
}

func inspectQueueDoctorState(root, state string, seen map[string]queueDoctorSeen, result *queueDoctorResult) error {
	dir := filepath.Join(root, state)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		displayPath := filepath.Join(state, entry.Name())
		if entry.IsDir() {
			result.Summary.Ignored++
			queueDoctorAddWarning(result, queueDoctorFinding{
				State:   state,
				Path:    displayPath,
				Code:    "unexpected_directory",
				Message: fmt.Sprintf("%s is a directory; queue entries must be JSON files", displayPath),
			})
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			result.Summary.Ignored++
			queueDoctorAddWarning(result, queueDoctorFinding{
				State:   state,
				Path:    displayPath,
				Code:    "unexpected_file",
				Message: fmt.Sprintf("%s is ignored because it is not a .json file", displayPath),
			})
			continue
		}
		result.Summary.Files++
		idFromPath := strings.TrimSuffix(entry.Name(), ".json")
		body, err := os.ReadFile(path)
		if err != nil {
			result.Summary.Invalid++
			queueDoctorAddProblem(result, queueDoctorFinding{
				State:   state,
				ID:      idFromPath,
				Path:    displayPath,
				Code:    "read_failed",
				Message: fmt.Sprintf("cannot read queue item %s: %v", displayPath, err),
			})
			continue
		}
		var raw daemon.QueueItem
		if err := json.Unmarshal(body, &raw); err != nil {
			result.Summary.Invalid++
			queueDoctorAddProblem(result, queueDoctorFinding{
				State:   state,
				ID:      idFromPath,
				Path:    displayPath,
				Code:    "invalid_json",
				Message: fmt.Sprintf("%s is not valid JSON: %v", displayPath, err),
			})
			continue
		}
		result.Summary.Items++
		switch state {
		case daemon.QueueStatePending:
			result.Summary.Pending++
		case daemon.QueueStateDead:
			result.Summary.Dead++
		}
		problemsBefore := len(result.Problems)
		effectiveID := strings.TrimSpace(raw.ID)
		if effectiveID == "" {
			effectiveID = idFromPath
			queueDoctorAddWarning(result, queueDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "missing_id",
				Message: fmt.Sprintf("%s has no id field; runtime falls back to filename %q", displayPath, idFromPath),
			})
		} else if effectiveID != idFromPath {
			queueDoctorAddProblem(result, queueDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "id_path_mismatch",
				Message: fmt.Sprintf("%s stores id %q but filename implies %q", displayPath, effectiveID, idFromPath),
			})
		}
		if prev, ok := seen[effectiveID]; ok {
			result.Summary.Duplicates++
			queueDoctorAddProblem(result, queueDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "duplicate_id",
				Message: fmt.Sprintf("%s duplicates queue id %q already found at %s/%s", displayPath, effectiveID, prev.State, prev.Path),
			})
		} else {
			seen[effectiveID] = queueDoctorSeen{State: state, Path: entry.Name()}
		}
		validateQueueDoctorItem(state, idFromPath, displayPath, raw, effectiveID, result)
		if len(result.Problems) == problemsBefore {
			result.Summary.Valid++
		} else {
			result.Summary.Invalid++
		}
	}
	return nil
}

func validateQueueDoctorItem(state, idFromPath, displayPath string, raw daemon.QueueItem, effectiveID string, result *queueDoctorResult) {
	rawState := strings.TrimSpace(raw.State)
	switch rawState {
	case "":
		queueDoctorAddWarning(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_state",
			Message: fmt.Sprintf("%s has no state field; runtime uses containing directory %q", displayPath, state),
		})
	case daemon.QueueStatePending, daemon.QueueStateDead:
		if rawState != state {
			queueDoctorAddProblem(result, queueDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "state_path_mismatch",
				Message: fmt.Sprintf("%s stores state %q but lives under %q", displayPath, rawState, state),
			})
		}
	default:
		queueDoctorAddProblem(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "unknown_state",
			Message: fmt.Sprintf("%s stores unknown queue state %q", displayPath, rawState),
		})
	}
	if strings.TrimSpace(effectiveID) == "" {
		queueDoctorAddProblem(result, queueDoctorFinding{
			State:   state,
			ID:      idFromPath,
			Path:    displayPath,
			Code:    "empty_id",
			Message: fmt.Sprintf("%s has no usable queue id", displayPath),
		})
	}
	if strings.TrimSpace(raw.EventType) == "" {
		queueDoctorAddProblem(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_event_type",
			Message: fmt.Sprintf("%s has no event_type", displayPath),
		})
	}
	if strings.TrimSpace(raw.Instance) == "" {
		queueDoctorAddProblem(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_instance",
			Message: fmt.Sprintf("%s has no target instance", displayPath),
		})
	}
	if strings.TrimSpace(raw.InstanceID) == "" {
		queueDoctorAddProblem(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_instance_id",
			Message: fmt.Sprintf("%s has no instance_id", displayPath),
		})
	}
	if raw.QueuedAt.IsZero() {
		queueDoctorAddProblem(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_queued_at",
			Message: fmt.Sprintf("%s has no queued_at timestamp", displayPath),
		})
	}
	if raw.UpdatedAt.IsZero() {
		queueDoctorAddProblem(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_updated_at",
			Message: fmt.Sprintf("%s has no updated_at timestamp", displayPath),
		})
	}
	if raw.Attempts < 0 {
		queueDoctorAddProblem(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "negative_attempts",
			Message: fmt.Sprintf("%s has negative attempts %d", displayPath, raw.Attempts),
		})
	}
	if raw.Payload == nil {
		queueDoctorAddWarning(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_payload",
			Message: fmt.Sprintf("%s has no payload object; runtime treats it as empty", displayPath),
		})
	}
	if state == daemon.QueueStateDead && raw.DeadLetteredAt.IsZero() {
		queueDoctorAddWarning(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_dead_lettered_at",
			Message: fmt.Sprintf("%s is dead-lettered but has no dead_lettered_at timestamp", displayPath),
		})
	}
	if state == daemon.QueueStatePending && !raw.DeadLetteredAt.IsZero() {
		queueDoctorAddWarning(result, queueDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "unexpected_dead_lettered_at",
			Message: fmt.Sprintf("%s is pending but still has dead_lettered_at=%s", displayPath, raw.DeadLetteredAt.Format(time.RFC3339)),
		})
	}
}

func queueDoctorAddProblem(result *queueDoctorResult, finding queueDoctorFinding) {
	result.Problems = append(result.Problems, finding)
}

func queueDoctorAddWarning(result *queueDoctorResult, finding queueDoctorFinding) {
	result.Warnings = append(result.Warnings, finding)
}

func queueDoctorActions(result queueDoctorResult) []string {
	if len(result.Problems) == 0 {
		return nil
	}
	return []string{"agent-team queue doctor --json", "agent-team snapshot --json"}
}

func renderQueueDoctor(stdout, stderr io.Writer, result queueDoctorResult, jsonOut bool) error {
	sortQueueDoctorFindings(result.Problems)
	sortQueueDoctorFindings(result.Warnings)
	if jsonOut {
		return json.NewEncoder(stdout).Encode(result)
	}
	if result.OK {
		fmt.Fprintln(stdout, "agent-team queue doctor: OK")
		renderQueueDoctorSummary(stdout, result.Summary)
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
		}
		return nil
	}
	fmt.Fprintln(stderr, "agent-team queue doctor: problems found:")
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "  - %s\n", problem.Message)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
	}
	return nil
}

func renderQueueDoctorSummary(w io.Writer, summary queueDoctorSummary) {
	fmt.Fprintf(w, "queue files: files=%d items=%d valid=%d invalid=%d pending=%d dead=%d ignored=%d duplicates=%d\n",
		summary.Files,
		summary.Items,
		summary.Valid,
		summary.Invalid,
		summary.Pending,
		summary.Dead,
		summary.Ignored,
		summary.Duplicates)
}

func sortQueueDoctorFindings(findings []queueDoctorFinding) {
	sort.Slice(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}
