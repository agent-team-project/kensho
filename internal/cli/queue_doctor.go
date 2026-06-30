package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
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
	OK         bool                         `json:"ok"`
	Root       string                       `json:"root"`
	Summary    queueDoctorSummary           `json:"summary"`
	Problems   []queueDoctorFinding         `json:"problems,omitempty"`
	Warnings   []queueDoctorFinding         `json:"warnings,omitempty"`
	Actions    []string                     `json:"actions,omitempty"`
	Quarantine *queueDoctorQuarantineResult `json:"quarantine,omitempty"`
}

type queueDoctorQuarantineResult struct {
	DryRun     bool                        `json:"dry_run,omitempty"`
	Directory  string                      `json:"directory,omitempty"`
	Candidates int                         `json:"candidates"`
	Moved      int                         `json:"moved"`
	Items      []queueDoctorQuarantineItem `json:"items,omitempty"`
}

type queueDoctorQuarantineItem struct {
	State       string   `json:"state,omitempty"`
	ID          string   `json:"id,omitempty"`
	Path        string   `json:"path"`
	Destination string   `json:"destination,omitempty"`
	Codes       []string `json:"codes,omitempty"`
	Action      string   `json:"action"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

type queueDoctorSeen struct {
	State string
	Path  string
}

func newQueueDoctorCmd() *cobra.Command {
	var (
		target     string
		jsonOut    bool
		format     string
		commands   bool
		quarantine bool
		dryRun     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate persisted queue files.",
		Long:  "Validate persisted daemon queue files without relying on normal queue listing paths.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue doctor: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue doctor: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if dryRun && !quarantine {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue doctor: --dry-run requires --quarantine.")
				return exitErr(2)
			}
			if commands && quarantine && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue doctor: --commands with --quarantine requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parseQueueDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue doctor: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := collectQueueDoctor(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue doctor: %v\n", err)
				return exitErr(1)
			}
			if quarantine {
				q, err := quarantineQueueDoctorProblems(result.Root, result, dryRun, time.Now())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue doctor: quarantine: %v\n", err)
					return exitErr(1)
				}
				result.Quarantine = q
				if !dryRun && q.Moved > 0 {
					refreshed, err := collectQueueDoctor(teamDir)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue doctor: %v\n", err)
						return exitErr(1)
					}
					refreshed.Quarantine = q
					result = refreshed
				}
			}
			if err := renderQueueDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl, commands, operatorCommandScopeFromCommand(cmd, target, "target")); err != nil {
				return err
			}
			if !result.OK && !quarantine {
				return exitErr(1)
			}
			if !result.OK && quarantine && !dryRun {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit queue doctor findings as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the queue doctor result with a Go template, e.g. '{{.OK}} {{.Summary.Invalid}}'.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, or with --quarantine --dry-run print the matching quarantine apply command.")
	cmd.Flags().BoolVar(&quarantine, "quarantine", false, "Move queue files with doctor problems out of the active queue.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "With --quarantine, preview files that would be moved.")
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
	return []string{"agent-team queue doctor --quarantine --dry-run --commands", "agent-team queue doctor --json", "agent-team snapshot --json"}
}

func renderQueueDoctor(stdout, stderr io.Writer, result queueDoctorResult, jsonOut bool, tmpl *template.Template, commands bool, scope operatorCommandScope) error {
	sortQueueDoctorFindings(result.Problems)
	sortQueueDoctorFindings(result.Warnings)
	if jsonOut {
		return json.NewEncoder(stdout).Encode(result)
	}
	if commands {
		actions := result.Actions
		if result.Quarantine != nil && result.Quarantine.DryRun {
			actions = queueDoctorQuarantineApplyActions(result)
		}
		return renderOperatorActionCommands(stdout, actions, scope)
	}
	if tmpl != nil {
		return renderQueueDoctorFormat(stdout, result, tmpl)
	}
	if result.OK {
		fmt.Fprintln(stdout, "agent-team queue doctor: OK")
		renderQueueDoctorSummary(stdout, result.Summary)
		renderQueueDoctorQuarantine(stdout, result.Quarantine)
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
	if len(result.Actions) > 0 {
		fmt.Fprintln(stderr, "next actions:")
		for _, action := range result.Actions {
			fmt.Fprintf(stderr, "  - %s\n", action)
		}
	}
	renderQueueDoctorQuarantine(stdout, result.Quarantine)
	return nil
}

func queueDoctorQuarantineApplyActions(result queueDoctorResult) []string {
	if result.Quarantine == nil || !result.Quarantine.DryRun || result.Quarantine.Candidates == 0 {
		return nil
	}
	return []string{"agent-team queue doctor --quarantine"}
}

func parseQueueDoctorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("queue-doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderQueueDoctorFormat(w io.Writer, result queueDoctorResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
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

func quarantineQueueDoctorProblems(root string, result queueDoctorResult, dryRun bool, now time.Time) (*queueDoctorQuarantineResult, error) {
	items := queueDoctorQuarantineCandidates(result)
	out := &queueDoctorQuarantineResult{
		DryRun:     dryRun,
		Candidates: len(items),
		Items:      items,
	}
	if len(items) == 0 {
		return out, nil
	}
	out.Directory = filepath.Join("quarantine", now.UTC().Format("20060102T150405.000000000Z"))
	for i := range out.Items {
		item := &out.Items[i]
		item.DryRun = dryRun
		item.Action = "would_quarantine"
		item.Destination = filepath.Join(out.Directory, item.State, filepath.Base(item.Path))
		if dryRun {
			continue
		}
		source, err := queueDoctorSafeQueuePath(root, item.Path)
		if err != nil {
			return out, err
		}
		destination, err := queueDoctorSafeQueuePath(root, item.Destination)
		if err != nil {
			return out, err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return out, err
		}
		if err := os.Rename(source, destination); err != nil {
			return out, err
		}
		item.Action = "quarantined"
		item.DryRun = false
		out.Moved++
	}
	return out, nil
}

func queueDoctorQuarantineCandidates(result queueDoctorResult) []queueDoctorQuarantineItem {
	byPath := map[string]*queueDoctorQuarantineItem{}
	for _, problem := range result.Problems {
		path := strings.TrimSpace(problem.Path)
		if path == "" {
			continue
		}
		item := byPath[path]
		if item == nil {
			item = &queueDoctorQuarantineItem{
				State:  problem.State,
				ID:     problem.ID,
				Path:   path,
				Action: "would_quarantine",
			}
			byPath[path] = item
		}
		if item.ID == "" {
			item.ID = problem.ID
		}
		if item.State == "" {
			item.State = problem.State
		}
		if problem.Code != "" && !stringSliceContains(item.Codes, problem.Code) {
			item.Codes = append(item.Codes, problem.Code)
		}
	}
	out := make([]queueDoctorQuarantineItem, 0, len(byPath))
	for _, item := range byPath {
		sort.Strings(item.Codes)
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func queueDoctorSafeQueuePath(root, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe queue path %q", rel)
	}
	return filepath.Join(root, clean), nil
}

func renderQueueDoctorQuarantine(w io.Writer, result *queueDoctorQuarantineResult) {
	if result == nil {
		return
	}
	action := "quarantine"
	if result.DryRun {
		action = "quarantine dry-run"
	}
	fmt.Fprintf(w, "queue %s: candidates=%d moved=%d", action, result.Candidates, result.Moved)
	if result.Directory != "" {
		fmt.Fprintf(w, " directory=%s", result.Directory)
	}
	fmt.Fprintln(w)
	for _, item := range result.Items {
		if result.DryRun {
			fmt.Fprintf(w, "  - would quarantine %s -> %s", item.Path, item.Destination)
		} else {
			fmt.Fprintf(w, "  - quarantined %s -> %s", item.Path, item.Destination)
		}
		if len(item.Codes) > 0 {
			fmt.Fprintf(w, " (%s)", strings.Join(item.Codes, ","))
		}
		fmt.Fprintln(w)
	}
}
