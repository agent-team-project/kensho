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

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/spf13/cobra"
)

type outboxDoctorFinding struct {
	State   string `json:"state,omitempty"`
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type outboxDoctorSummary struct {
	Files      int `json:"files"`
	Items      int `json:"items"`
	Valid      int `json:"valid"`
	Invalid    int `json:"invalid"`
	Pending    int `json:"pending"`
	Processed  int `json:"processed"`
	Failed     int `json:"failed"`
	Ignored    int `json:"ignored"`
	Duplicates int `json:"duplicates"`
}

type outboxDoctorResult struct {
	OK         bool                          `json:"ok"`
	Root       string                        `json:"root"`
	Summary    outboxDoctorSummary           `json:"summary"`
	Problems   []outboxDoctorFinding         `json:"problems,omitempty"`
	Warnings   []outboxDoctorFinding         `json:"warnings,omitempty"`
	Actions    []string                      `json:"actions,omitempty"`
	Quarantine *outboxDoctorQuarantineResult `json:"quarantine,omitempty"`
}

type outboxDoctorQuarantineResult struct {
	DryRun     bool                         `json:"dry_run,omitempty"`
	Directory  string                       `json:"directory,omitempty"`
	Candidates int                          `json:"candidates"`
	Moved      int                          `json:"moved"`
	Items      []outboxDoctorQuarantineItem `json:"items,omitempty"`
}

type outboxDoctorQuarantineItem struct {
	State       string   `json:"state,omitempty"`
	ID          string   `json:"id,omitempty"`
	Path        string   `json:"path"`
	Destination string   `json:"destination,omitempty"`
	Codes       []string `json:"codes,omitempty"`
	Action      string   `json:"action"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

type outboxDoctorSeen struct {
	State string
	Path  string
}

func newOutboxDoctorCmd() *cobra.Command {
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
		Short: "Validate sandboxed agent outbox files.",
		Long:  "Validate sandboxed agent outbox JSON files without relying on normal outbox listing paths.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox doctor: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox doctor: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if dryRun && !quarantine {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox doctor: --dry-run requires --quarantine.")
				return exitErr(2)
			}
			if commands && quarantine && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team outbox doctor: --commands with --quarantine requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parseOutboxDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox doctor: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := collectOutboxDoctor(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox doctor: %v\n", err)
				return exitErr(1)
			}
			if quarantine {
				q, err := quarantineOutboxDoctorProblems(result.Root, result, dryRun, time.Now())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox doctor: quarantine: %v\n", err)
					return exitErr(1)
				}
				result.Quarantine = q
				if !dryRun && q.Moved > 0 {
					refreshed, err := collectOutboxDoctor(teamDir)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team outbox doctor: %v\n", err)
						return exitErr(1)
					}
					refreshed.Quarantine = q
					result = refreshed
				}
			}
			if err := renderOutboxDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl, commands, operatorCommandScopeFromCommand(cmd, target, "target")); err != nil {
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
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit outbox doctor findings as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the outbox doctor result with a Go template, e.g. '{{.OK}} {{.Summary.Invalid}}'.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, or with --quarantine --dry-run print the matching quarantine apply command.")
	cmd.Flags().BoolVar(&quarantine, "quarantine", false, "Move outbox files with doctor problems out of the active outbox.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "With --quarantine, preview files that would be moved.")
	return cmd
}

func collectOutboxDoctor(teamDir string) (outboxDoctorResult, error) {
	root := daemon.OutboxRoot(teamDir)
	result := outboxDoctorResult{
		OK:   true,
		Root: root,
	}
	seen := map[string]outboxDoctorSeen{}
	for _, state := range []string{daemon.OutboxStatePending, daemon.OutboxStateProcessed, daemon.OutboxStateFailed} {
		if err := inspectOutboxDoctorState(root, state, seen, &result); err != nil {
			return result, err
		}
	}
	result.OK = len(result.Problems) == 0
	result.Actions = outboxDoctorActions(result)
	return result, nil
}

func inspectOutboxDoctorState(root, state string, seen map[string]outboxDoctorSeen, result *outboxDoctorResult) error {
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
			outboxDoctorAddWarning(result, outboxDoctorFinding{
				State:   state,
				Path:    displayPath,
				Code:    "unexpected_directory",
				Message: fmt.Sprintf("%s is a directory; outbox entries must be JSON files", displayPath),
			})
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			result.Summary.Ignored++
			outboxDoctorAddWarning(result, outboxDoctorFinding{
				State:   state,
				Path:    displayPath,
				Code:    "unexpected_file",
				Message: fmt.Sprintf("%s is ignored because it is not a .json file", displayPath),
			})
			continue
		}
		result.Summary.Files++
		idFromPath := strings.TrimSuffix(entry.Name(), ".json")
		problemsBefore := len(result.Problems)
		if err := validateOutboxDoctorID(idFromPath); err != nil {
			outboxDoctorAddProblem(result, outboxDoctorFinding{
				State:   state,
				ID:      idFromPath,
				Path:    displayPath,
				Code:    "invalid_path_id",
				Message: fmt.Sprintf("%s has invalid outbox filename id: %v", displayPath, err),
			})
		}
		body, err := os.ReadFile(path)
		if err != nil {
			result.Summary.Invalid++
			outboxDoctorAddProblem(result, outboxDoctorFinding{
				State:   state,
				ID:      idFromPath,
				Path:    displayPath,
				Code:    "read_failed",
				Message: fmt.Sprintf("cannot read outbox item %s: %v", displayPath, err),
			})
			continue
		}
		var raw daemon.OutboxItem
		if err := json.Unmarshal(body, &raw); err != nil {
			result.Summary.Invalid++
			outboxDoctorAddProblem(result, outboxDoctorFinding{
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
		case daemon.OutboxStatePending:
			result.Summary.Pending++
		case daemon.OutboxStateProcessed:
			result.Summary.Processed++
		case daemon.OutboxStateFailed:
			result.Summary.Failed++
		}
		effectiveID := strings.TrimSpace(raw.ID)
		if effectiveID == "" {
			effectiveID = idFromPath
			outboxDoctorAddWarning(result, outboxDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "missing_id",
				Message: fmt.Sprintf("%s has no id field; runtime falls back to filename %q", displayPath, idFromPath),
			})
		} else if effectiveID != idFromPath {
			outboxDoctorAddProblem(result, outboxDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "id_path_mismatch",
				Message: fmt.Sprintf("%s stores id %q but filename implies %q", displayPath, effectiveID, idFromPath),
			})
		}
		if err := validateOutboxDoctorID(effectiveID); err != nil {
			outboxDoctorAddProblem(result, outboxDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "invalid_id",
				Message: fmt.Sprintf("%s has invalid outbox id %q: %v", displayPath, effectiveID, err),
			})
		}
		if prev, ok := seen[effectiveID]; ok {
			result.Summary.Duplicates++
			outboxDoctorAddProblem(result, outboxDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "duplicate_id",
				Message: fmt.Sprintf("%s duplicates outbox id %q already found at %s/%s", displayPath, effectiveID, prev.State, prev.Path),
			})
		} else {
			seen[effectiveID] = outboxDoctorSeen{State: state, Path: entry.Name()}
		}
		validateOutboxDoctorItem(state, displayPath, raw, effectiveID, result)
		if len(result.Problems) == problemsBefore {
			result.Summary.Valid++
		} else {
			result.Summary.Invalid++
		}
	}
	return nil
}

func validateOutboxDoctorItem(state, displayPath string, raw daemon.OutboxItem, effectiveID string, result *outboxDoctorResult) {
	rawState := strings.TrimSpace(raw.State)
	switch rawState {
	case "":
		outboxDoctorAddWarning(result, outboxDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_state",
			Message: fmt.Sprintf("%s has no state field; runtime uses containing directory %q", displayPath, state),
		})
	case daemon.OutboxStatePending, daemon.OutboxStateProcessed, daemon.OutboxStateFailed:
		if rawState != state {
			outboxDoctorAddProblem(result, outboxDoctorFinding{
				State:   state,
				ID:      effectiveID,
				Path:    displayPath,
				Code:    "state_path_mismatch",
				Message: fmt.Sprintf("%s stores state %q but lives under %q", displayPath, rawState, state),
			})
		}
	default:
		outboxDoctorAddProblem(result, outboxDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "unknown_state",
			Message: fmt.Sprintf("%s stores unknown outbox state %q", displayPath, rawState),
		})
	}
	if strings.TrimSpace(raw.Type) == "" {
		outboxDoctorAddProblem(result, outboxDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_type",
			Message: fmt.Sprintf("%s has no event type", displayPath),
		})
	}
	if raw.CreatedAt.IsZero() {
		outboxDoctorAddProblem(result, outboxDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_created_at",
			Message: fmt.Sprintf("%s has no created_at timestamp", displayPath),
		})
	}
	if raw.UpdatedAt.IsZero() {
		outboxDoctorAddProblem(result, outboxDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_updated_at",
			Message: fmt.Sprintf("%s has no updated_at timestamp", displayPath),
		})
	}
	if raw.Payload == nil {
		outboxDoctorAddWarning(result, outboxDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_payload",
			Message: fmt.Sprintf("%s has no payload object; runtime treats it as empty", displayPath),
		})
	}
	if state == daemon.OutboxStateProcessed && raw.ProcessedAt.IsZero() {
		outboxDoctorAddWarning(result, outboxDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_processed_at",
			Message: fmt.Sprintf("%s is processed but has no processed_at timestamp", displayPath),
		})
	}
	if state == daemon.OutboxStateFailed && raw.FailedAt.IsZero() {
		outboxDoctorAddWarning(result, outboxDoctorFinding{
			State:   state,
			ID:      effectiveID,
			Path:    displayPath,
			Code:    "missing_failed_at",
			Message: fmt.Sprintf("%s is failed but has no failed_at timestamp", displayPath),
		})
	}
}

func outboxDoctorAddProblem(result *outboxDoctorResult, finding outboxDoctorFinding) {
	result.Problems = append(result.Problems, finding)
}

func outboxDoctorAddWarning(result *outboxDoctorResult, finding outboxDoctorFinding) {
	result.Warnings = append(result.Warnings, finding)
}

func outboxDoctorActions(result outboxDoctorResult) []string {
	if len(result.Problems) == 0 {
		return nil
	}
	return []string{"agent-team outbox doctor --quarantine --dry-run --commands", "agent-team outbox doctor --json", "agent-team snapshot --json"}
}

func renderOutboxDoctor(stdout, stderr io.Writer, result outboxDoctorResult, jsonOut bool, tmpl *template.Template, commands bool, scope operatorCommandScope) error {
	sortOutboxDoctorFindings(result.Problems)
	sortOutboxDoctorFindings(result.Warnings)
	if jsonOut {
		return json.NewEncoder(stdout).Encode(result)
	}
	if commands {
		actions := result.Actions
		if result.Quarantine != nil && result.Quarantine.DryRun {
			actions = outboxDoctorQuarantineApplyActions(result)
		}
		return renderOperatorActionCommands(stdout, actions, scope)
	}
	if tmpl != nil {
		return renderOutboxDoctorFormat(stdout, result, tmpl)
	}
	if result.OK {
		fmt.Fprintln(stdout, "agent-team outbox doctor: OK")
		renderOutboxDoctorSummary(stdout, result.Summary)
		renderOutboxDoctorQuarantine(stdout, result.Quarantine)
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
		}
		return nil
	}
	fmt.Fprintln(stderr, "agent-team outbox doctor: problems found:")
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
	renderOutboxDoctorQuarantine(stdout, result.Quarantine)
	return nil
}

func outboxDoctorQuarantineApplyActions(result outboxDoctorResult) []string {
	if result.Quarantine == nil || !result.Quarantine.DryRun || result.Quarantine.Candidates == 0 {
		return nil
	}
	return []string{"agent-team outbox doctor --quarantine"}
}

func parseOutboxDoctorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("outbox-doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderOutboxDoctorFormat(w io.Writer, result outboxDoctorResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func renderOutboxDoctorSummary(w io.Writer, summary outboxDoctorSummary) {
	fmt.Fprintf(w, "outbox files: files=%d items=%d valid=%d invalid=%d pending=%d processed=%d failed=%d ignored=%d duplicates=%d\n",
		summary.Files,
		summary.Items,
		summary.Valid,
		summary.Invalid,
		summary.Pending,
		summary.Processed,
		summary.Failed,
		summary.Ignored,
		summary.Duplicates)
}

func sortOutboxDoctorFindings(findings []outboxDoctorFinding) {
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

func quarantineOutboxDoctorProblems(root string, result outboxDoctorResult, dryRun bool, now time.Time) (*outboxDoctorQuarantineResult, error) {
	items := outboxDoctorQuarantineCandidates(result)
	out := &outboxDoctorQuarantineResult{
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
		source, err := outboxDoctorSafeOutboxPath(root, item.Path)
		if err != nil {
			return out, err
		}
		destination, err := outboxDoctorSafeOutboxPath(root, item.Destination)
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

func outboxDoctorQuarantineCandidates(result outboxDoctorResult) []outboxDoctorQuarantineItem {
	byPath := map[string]*outboxDoctorQuarantineItem{}
	for _, problem := range result.Problems {
		path := strings.TrimSpace(problem.Path)
		if path == "" {
			continue
		}
		item := byPath[path]
		if item == nil {
			item = &outboxDoctorQuarantineItem{
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
	out := make([]outboxDoctorQuarantineItem, 0, len(byPath))
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

func outboxDoctorSafeOutboxPath(root, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe outbox path %q", rel)
	}
	return filepath.Join(root, clean), nil
}

func renderOutboxDoctorQuarantine(w io.Writer, result *outboxDoctorQuarantineResult) {
	if result == nil {
		return
	}
	action := "quarantine"
	if result.DryRun {
		action = "quarantine dry-run"
	}
	fmt.Fprintf(w, "outbox %s: candidates=%d moved=%d", action, result.Candidates, result.Moved)
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

func validateOutboxDoctorID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if id == "." || id == ".." || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("path segments are not allowed")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("only ASCII letters, digits, '.', '_' and '-' are allowed")
	}
	return nil
}
