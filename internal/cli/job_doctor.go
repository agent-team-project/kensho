package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/spf13/cobra"
)

type jobDoctorFinding struct {
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type jobDoctorSummary struct {
	Files   int `json:"files"`
	Jobs    int `json:"jobs"`
	Valid   int `json:"valid"`
	Invalid int `json:"invalid"`
	Ignored int `json:"ignored"`
}

type jobDoctorResult struct {
	OK         bool                       `json:"ok"`
	Root       string                     `json:"root"`
	Summary    jobDoctorSummary           `json:"summary"`
	Problems   []jobDoctorFinding         `json:"problems,omitempty"`
	Warnings   []jobDoctorFinding         `json:"warnings,omitempty"`
	Actions    []string                   `json:"actions,omitempty"`
	Quarantine *jobDoctorQuarantineResult `json:"quarantine,omitempty"`
}

type jobDoctorQuarantineResult struct {
	DryRun     bool                      `json:"dry_run,omitempty"`
	Directory  string                    `json:"directory,omitempty"`
	Candidates int                       `json:"candidates"`
	Moved      int                       `json:"moved"`
	Items      []jobDoctorQuarantineItem `json:"items,omitempty"`
}

type jobDoctorQuarantineItem struct {
	ID          string   `json:"id,omitempty"`
	Path        string   `json:"path"`
	Destination string   `json:"destination,omitempty"`
	Codes       []string `json:"codes,omitempty"`
	Action      string   `json:"action"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

func newJobDoctorCmd() *cobra.Command {
	var (
		repo       string
		jsonOut    bool
		format     string
		commands   bool
		quarantine bool
		dryRun     bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate durable job files.",
		Long:  "Validate durable job TOML files under `.agent_team/jobs/` without relying on normal job listing paths.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "" && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job doctor: --format cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job doctor: --commands cannot be combined with --json.")
				return exitErr(2)
			}
			if commands && format != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job doctor: --commands cannot be combined with --format.")
				return exitErr(2)
			}
			if dryRun && !quarantine {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job doctor: --dry-run requires --quarantine.")
				return exitErr(2)
			}
			if commands && quarantine && !dryRun {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job doctor: --commands with --quarantine requires --dry-run.")
				return exitErr(2)
			}
			tmpl, err := parseJobDoctorFormat(format)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job doctor: %v\n", err)
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := collectJobDoctor(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job doctor: %v\n", err)
				return exitErr(1)
			}
			if quarantine {
				q, err := quarantineJobDoctorProblems(result.Root, result, dryRun, time.Now())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job doctor: quarantine: %v\n", err)
					return exitErr(1)
				}
				result.Quarantine = q
				if !dryRun && q.Moved > 0 {
					refreshed, err := collectJobDoctor(teamDir)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job doctor: %v\n", err)
						return exitErr(1)
					}
					refreshed.Quarantine = q
					result = refreshed
				}
			}
			if err := renderJobDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), result, jsonOut, tmpl, commands, operatorCommandScopeFromCommand(cmd, repo, "repo")); err != nil {
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
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit durable job doctor findings as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the job doctor result with a Go template, e.g. '{{.OK}} {{.Summary.Valid}}'.")
	cmd.Flags().BoolVar(&commands, "commands", false, "Print recommended follow-up commands, or with --quarantine --dry-run print the matching quarantine apply command.")
	cmd.Flags().BoolVar(&quarantine, "quarantine", false, "Move job files with doctor problems out of the active jobs directory.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "With --quarantine, preview files that would be moved.")
	return cmd
}

func collectJobDoctor(teamDir string) (jobDoctorResult, error) {
	root := job.Directory(teamDir)
	result := jobDoctorResult{
		OK:   true,
		Root: root,
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	seen := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".toml") {
			result.Summary.Ignored++
			continue
		}
		result.Summary.Files++
		id := strings.TrimSuffix(name, ".toml")
		path := filepath.Join(root, name)
		fileHasProblem := false

		if strings.TrimSpace(id) == "" {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				Path:    path,
				Code:    "invalid_filename",
				Message: fmt.Sprintf("%s has an empty job id in its filename", path),
			})
			fileHasProblem = true
		} else if normalized := job.NormalizeID(id); normalized != id {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				ID:      id,
				Path:    path,
				Code:    "filename_not_normalized",
				Message: fmt.Sprintf("%s filename id %q must be normalized as %q", path, id, normalized),
			})
			fileHasProblem = true
		}

		var j job.Job
		if _, err := toml.DecodeFile(path, &j); err != nil {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				ID:      id,
				Path:    path,
				Code:    "invalid_toml",
				Message: fmt.Sprintf("%s is not valid job TOML: %v", path, err),
			})
			result.Summary.Invalid++
			continue
		}
		result.Summary.Jobs++
		if j.ID == "" {
			j.ID = id
		}
		if err := job.Validate(&j); err != nil {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				ID:      j.ID,
				Path:    path,
				Code:    "invalid_job",
				Message: fmt.Sprintf("%s has invalid job data: %v", path, err),
			})
			fileHasProblem = true
		}
		if j.ID != "" && j.ID != id {
			jobDoctorAddProblem(&result, jobDoctorFinding{
				ID:      j.ID,
				Path:    path,
				Code:    "id_mismatch",
				Message: fmt.Sprintf("%s filename id %q does not match job id %q", path, id, j.ID),
			})
			fileHasProblem = true
		}
		if j.ID != "" {
			if previous, ok := seen[j.ID]; ok {
				jobDoctorAddProblem(&result, jobDoctorFinding{
					ID:      j.ID,
					Path:    path,
					Code:    "duplicate_id",
					Message: fmt.Sprintf("%s duplicates job id %q already used by %s", path, j.ID, previous),
				})
				fileHasProblem = true
			} else {
				seen[j.ID] = path
			}
		}
		if fileHasProblem {
			result.Summary.Invalid++
		} else {
			result.Summary.Valid++
		}
	}
	result.OK = len(result.Problems) == 0
	result.Actions = jobDoctorActions(result)
	sortJobDoctorFindings(result.Problems)
	sortJobDoctorFindings(result.Warnings)
	return result, nil
}

func jobDoctorAddProblem(result *jobDoctorResult, finding jobDoctorFinding) {
	result.Problems = append(result.Problems, finding)
}

func jobDoctorActions(result jobDoctorResult) []string {
	if len(result.Problems) == 0 {
		return nil
	}
	return []string{"agent-team job doctor --quarantine --dry-run --commands", "agent-team job doctor --json", "agent-team snapshot --json"}
}

func renderJobDoctor(stdout, stderr io.Writer, result jobDoctorResult, jsonOut bool, tmpl *template.Template, commands bool, scope operatorCommandScope) error {
	sortJobDoctorFindings(result.Problems)
	sortJobDoctorFindings(result.Warnings)
	if jsonOut {
		return json.NewEncoder(stdout).Encode(result)
	}
	if commands {
		actions := result.Actions
		if result.Quarantine != nil && result.Quarantine.DryRun {
			actions = jobDoctorQuarantineApplyActions(result)
		}
		return renderOperatorActionCommands(stdout, actions, scope)
	}
	if tmpl != nil {
		return renderJobDoctorFormat(stdout, result, tmpl)
	}
	if result.OK {
		fmt.Fprintln(stdout, "agent-team job doctor: OK")
		renderJobDoctorSummary(stdout, result.Summary)
		renderJobDoctorQuarantine(stdout, result.Quarantine)
		for _, warning := range result.Warnings {
			fmt.Fprintf(stderr, "  warning: %s\n", warning.Message)
		}
		return nil
	}
	fmt.Fprintln(stderr, "agent-team job doctor: problems found:")
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
	renderJobDoctorQuarantine(stdout, result.Quarantine)
	return nil
}

func renderJobDoctorSummary(w io.Writer, summary jobDoctorSummary) {
	fmt.Fprintf(w, "jobs: files=%d valid=%d invalid=%d ignored=%d\n", summary.Files, summary.Valid, summary.Invalid, summary.Ignored)
}

func jobDoctorQuarantineApplyActions(result jobDoctorResult) []string {
	if result.Quarantine == nil || !result.Quarantine.DryRun || result.Quarantine.Candidates == 0 {
		return nil
	}
	return []string{"agent-team job doctor --quarantine"}
}

func quarantineJobDoctorProblems(root string, result jobDoctorResult, dryRun bool, now time.Time) (*jobDoctorQuarantineResult, error) {
	items := jobDoctorQuarantineCandidates(result)
	out := &jobDoctorQuarantineResult{
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
		item.Destination = filepath.Join(out.Directory, filepath.Base(item.Path))
		if dryRun {
			continue
		}
		source, err := jobDoctorSafePath(root, item.Path)
		if err != nil {
			return out, err
		}
		destination, err := jobDoctorSafePath(root, item.Destination)
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

func jobDoctorQuarantineCandidates(result jobDoctorResult) []jobDoctorQuarantineItem {
	byPath := map[string]*jobDoctorQuarantineItem{}
	for _, problem := range result.Problems {
		path := strings.TrimSpace(problem.Path)
		if path == "" {
			continue
		}
		item := byPath[path]
		if item == nil {
			item = &jobDoctorQuarantineItem{
				ID:     problem.ID,
				Path:   path,
				Action: "would_quarantine",
			}
			byPath[path] = item
		}
		if item.ID == "" {
			item.ID = problem.ID
		}
		if problem.Code != "" && !stringSliceContains(item.Codes, problem.Code) {
			item.Codes = append(item.Codes, problem.Code)
		}
	}
	out := make([]jobDoctorQuarantineItem, 0, len(byPath))
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

func jobDoctorSafePath(root, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("empty job doctor path")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootAbs, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, candidate)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("refusing path outside jobs directory: %s", raw)
	}
	return candidate, nil
}

func renderJobDoctorQuarantine(w io.Writer, result *jobDoctorQuarantineResult) {
	if result == nil {
		return
	}
	action := "quarantined"
	if result.DryRun {
		action = "would_quarantine"
	}
	fmt.Fprintf(w, "quarantine: candidates=%d moved=%d action=%s", result.Candidates, result.Moved, action)
	if result.Directory != "" {
		fmt.Fprintf(w, " directory=%s", result.Directory)
	}
	fmt.Fprintln(w)
	for _, item := range result.Items {
		fmt.Fprintf(w, "  - %s -> %s", item.Path, item.Destination)
		if len(item.Codes) > 0 {
			fmt.Fprintf(w, " codes=%s", strings.Join(item.Codes, ","))
		}
		fmt.Fprintf(w, " action=%s\n", item.Action)
	}
}

func parseJobDoctorFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-doctor-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobDoctorFormat(w io.Writer, result jobDoctorResult, tmpl *template.Template) error {
	if err := tmpl.Execute(w, result); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func sortJobDoctorFindings(findings []jobDoctorFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].Code != findings[j].Code {
			return findings[i].Code < findings[j].Code
		}
		return findings[i].ID < findings[j].ID
	})
}
