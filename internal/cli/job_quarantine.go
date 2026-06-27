package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

const jobQuarantineDir = "quarantine"

type jobQuarantineItem struct {
	Path        string     `json:"path"`
	ID          string     `json:"id,omitempty"`
	Ticket      string     `json:"ticket,omitempty"`
	Target      string     `json:"target,omitempty"`
	Status      job.Status `json:"status,omitempty"`
	RestorePath string     `json:"restore_path,omitempty"`
	Size        int64      `json:"size"`
	ModTime     time.Time  `json:"mod_time"`
	Restorable  bool       `json:"restorable"`
	Problem     string     `json:"problem,omitempty"`
}

type jobQuarantineShowResult struct {
	jobQuarantineItem
	Job *job.Job `json:"job,omitempty"`
}

type jobQuarantineRestoreResult struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	ID          string `json:"id,omitempty"`
	Action      string `json:"action"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Overwrite   bool   `json:"overwrite,omitempty"`
}

type jobQuarantineDropResult struct {
	Path       string `json:"path"`
	ID         string `json:"id,omitempty"`
	Restorable bool   `json:"restorable"`
	Action     string `json:"action"`
	DryRun     bool   `json:"dry_run,omitempty"`
	Dropped    bool   `json:"dropped,omitempty"`
}

func newJobQuarantineCmd() *cobra.Command {
	var (
		repo         string
		restorable   bool
		unrestorable bool
		sortBy       string
		limit        int
		jsonOut      bool
		format       string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "quarantine",
		Short: "Inspect, restore, and drop quarantined job files.",
		Long:  "Inspect durable job TOML files moved under `.agent_team/jobs/quarantine/`, restore validated files to the active jobs directory, or explicitly drop preserved files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if restorable && unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job quarantine: --restorable and --unrestorable cannot be combined.")
				return exitErr(2)
			}
			if limit < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job quarantine: --limit must be >= 0.")
				return exitErr(2)
			}
			sortMode, err := parseJobQuarantineSort(sortBy)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job quarantine: %v\n", err)
				return exitErr(2)
			}
			formatTemplate, err := parseJobQuarantineCommandFormat(cmd, "agent-team job quarantine", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			items, err := listJobQuarantine(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job quarantine: %v\n", err)
				return exitErr(1)
			}
			items = filterJobQuarantineRestorable(items, restorable, unrestorable)
			items = prepareJobQuarantineItems(items, sortMode, limit)
			return renderJobQuarantineList(cmd.OutOrStdout(), items, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&restorable, "restorable", false, "Only show quarantined job files that can be restored.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "Only show quarantined job files that cannot be restored.")
	cmd.Flags().StringVar(&sortBy, "sort", "path", "Sort rows by path, id, ticket, target, status, modified, restorable, or size.")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit rows after filtering and sorting; 0 means no limit.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit quarantined job files as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render each quarantined job file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	cmd.AddCommand(newJobQuarantineShowCmd())
	cmd.AddCommand(newJobQuarantineRestoreCmd())
	cmd.AddCommand(newJobQuarantineDropCmd())
	return cmd
}

func newJobQuarantineShowCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "show <quarantine-path>",
		Short: "Show one quarantined job file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseJobQuarantineCommandFormat(cmd, "agent-team job quarantine show", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := showJobQuarantine(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job quarantine show: %v\n", err)
				return exitErr(1)
			}
			return renderJobQuarantineShow(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the quarantined job file as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the quarantined job file with a Go template, e.g. '{{.ID}} {{.Restorable}}'.")
	return cmd
}

func newJobQuarantineRestoreCmd() *cobra.Command {
	var (
		repo    string
		dryRun  bool
		force   bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <quarantine-path>",
		Short: "Restore one validated quarantined job file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseJobQuarantineCommandFormat(cmd, "agent-team job quarantine restore", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := restoreJobQuarantine(teamDir, args[0], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job quarantine restore: %v\n", err)
				return exitErr(1)
			}
			return renderJobQuarantineRestore(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the job file restore without moving it.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active job file with the same id.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the restore result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the restore result with a Go template, e.g. '{{.ID}} {{.Action}}'.")
	return cmd
}

func newJobQuarantineDropCmd() *cobra.Command {
	var (
		repo    string
		dryRun  bool
		jsonOut bool
		format  string
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <quarantine-path>",
		Short: "Drop one quarantined job file after inspection.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			formatTemplate, err := parseJobQuarantineCommandFormat(cmd, "agent-team job quarantine drop", format, jsonOut)
			if err != nil {
				return err
			}
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			result, err := dropJobQuarantine(teamDir, args[0], dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job quarantine drop: %v\n", err)
				return exitErr(1)
			}
			return renderJobQuarantineDrop(cmd.OutOrStdout(), result, jsonOut, formatTemplate)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the quarantined job file drop without deleting it.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the drop result as JSON.")
	cmd.Flags().StringVar(&format, "format", "", "Render the drop result with a Go template, e.g. '{{.Path}} {{.Action}}'.")
	return cmd
}

func listJobQuarantine(teamDir string) ([]jobQuarantineItem, error) {
	jobsRoot := job.Directory(teamDir)
	root := filepath.Join(jobsRoot, jobQuarantineDir)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []jobQuarantineItem
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			return nil
		}
		rel, err := filepath.Rel(jobsRoot, path)
		if err != nil {
			return err
		}
		item, err := inspectJobQuarantineFile(jobsRoot, rel)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortJobQuarantineItems(items, "path")
	return items, nil
}

func inspectJobQuarantineFile(jobsRoot, rel string) (jobQuarantineItem, error) {
	source, err := jobDoctorSafePath(jobsRoot, rel)
	if err != nil {
		return jobQuarantineItem{}, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return jobQuarantineItem{}, err
	}
	item := jobQuarantineItem{
		Path:    filepath.Clean(rel),
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
	var j job.Job
	if _, err := toml.DecodeFile(source, &j); err != nil {
		item.ID = strings.TrimSuffix(filepath.Base(item.Path), ".toml")
		item.Problem = fmt.Sprintf("invalid TOML: %v", err)
		return item, nil
	}
	if j.ID == "" {
		j.ID = strings.TrimSuffix(filepath.Base(item.Path), ".toml")
	}
	item.ID = j.ID
	item.Ticket = j.Ticket
	item.Target = j.Target
	item.Status = j.Status
	if err := job.Validate(&j); err != nil {
		item.Problem = err.Error()
		return item, nil
	}
	item.RestorePath = j.ID + ".toml"
	item.Restorable = true
	return item, nil
}

func showJobQuarantine(teamDir, rawPath string) (jobQuarantineShowResult, error) {
	jobsRoot := job.Directory(teamDir)
	rel, err := normalizeJobQuarantinePath(rawPath)
	if err != nil {
		return jobQuarantineShowResult{}, err
	}
	item, err := inspectJobQuarantineFile(jobsRoot, rel)
	if err != nil {
		return jobQuarantineShowResult{}, err
	}
	result := jobQuarantineShowResult{jobQuarantineItem: item}
	if !item.Restorable {
		return result, nil
	}
	source, err := jobDoctorSafePath(jobsRoot, item.Path)
	if err != nil {
		return result, nil
	}
	var j job.Job
	if _, err := toml.DecodeFile(source, &j); err != nil {
		return result, nil
	}
	if j.ID == "" {
		j.ID = strings.TrimSuffix(filepath.Base(item.Path), ".toml")
	}
	if err := job.Validate(&j); err != nil {
		return result, nil
	}
	result.Job = &j
	return result, nil
}

func restoreJobQuarantine(teamDir, rawPath string, dryRun, force bool) (jobQuarantineRestoreResult, error) {
	jobsRoot := job.Directory(teamDir)
	rel, err := normalizeJobQuarantinePath(rawPath)
	if err != nil {
		return jobQuarantineRestoreResult{}, err
	}
	item, err := inspectJobQuarantineFile(jobsRoot, rel)
	if err != nil {
		return jobQuarantineRestoreResult{}, err
	}
	if !item.Restorable {
		return jobQuarantineRestoreResult{}, fmt.Errorf("%s is not restorable: %s", item.Path, item.Problem)
	}
	source, err := jobDoctorSafePath(jobsRoot, item.Path)
	if err != nil {
		return jobQuarantineRestoreResult{}, err
	}
	destination, err := jobDoctorSafePath(jobsRoot, item.RestorePath)
	if err != nil {
		return jobQuarantineRestoreResult{}, err
	}
	if _, err := os.Stat(destination); err == nil && !force {
		return jobQuarantineRestoreResult{}, fmt.Errorf("%s already exists; pass --force to overwrite it", item.RestorePath)
	} else if err != nil && !os.IsNotExist(err) {
		return jobQuarantineRestoreResult{}, err
	}
	result := jobQuarantineRestoreResult{
		Path:        item.Path,
		Destination: item.RestorePath,
		ID:          item.ID,
		Action:      "would_restore",
		DryRun:      dryRun,
		Overwrite:   force,
	}
	if dryRun {
		return result, nil
	}
	if force {
		_ = os.Remove(destination)
	}
	if err := os.Rename(source, destination); err != nil {
		return result, err
	}
	pruneEmptyJobQuarantineDirs(jobsRoot, filepath.Dir(source))
	result.Action = "restored"
	result.DryRun = false
	return result, nil
}

func dropJobQuarantine(teamDir, rawPath string, dryRun bool) (jobQuarantineDropResult, error) {
	jobsRoot := job.Directory(teamDir)
	rel, err := normalizeJobQuarantinePath(rawPath)
	if err != nil {
		return jobQuarantineDropResult{}, err
	}
	item, err := inspectJobQuarantineFile(jobsRoot, rel)
	if err != nil {
		return jobQuarantineDropResult{}, err
	}
	result := jobQuarantineDropResult{
		Path:       item.Path,
		ID:         item.ID,
		Restorable: item.Restorable,
		Action:     "would_drop",
		DryRun:     dryRun,
	}
	if dryRun {
		return result, nil
	}
	source, err := jobDoctorSafePath(jobsRoot, item.Path)
	if err != nil {
		return result, err
	}
	if err := os.Remove(source); err != nil {
		return result, err
	}
	pruneEmptyJobQuarantineDirs(jobsRoot, filepath.Dir(source))
	result.Action = "dropped"
	result.Dropped = true
	result.DryRun = false
	return result, nil
}

func filterJobQuarantineRestorable(items []jobQuarantineItem, restorableOnly, unrestorableOnly bool) []jobQuarantineItem {
	if !restorableOnly && !unrestorableOnly {
		return items
	}
	out := make([]jobQuarantineItem, 0, len(items))
	for _, item := range items {
		if restorableOnly && !item.Restorable {
			continue
		}
		if unrestorableOnly && item.Restorable {
			continue
		}
		out = append(out, item)
	}
	return out
}

func prepareJobQuarantineItems(items []jobQuarantineItem, sortMode string, limit int) []jobQuarantineItem {
	sortJobQuarantineItems(items, sortMode)
	if limit <= 0 || limit >= len(items) {
		return items
	}
	return items[:limit]
}

func parseJobQuarantineSort(raw string) (string, error) {
	sortMode := strings.ToLower(strings.TrimSpace(raw))
	switch sortMode {
	case "", "path", "id", "ticket", "target", "status", "modified", "restorable", "size":
		if sortMode == "" {
			return "path", nil
		}
		return sortMode, nil
	default:
		return "", fmt.Errorf("--sort must be path, id, ticket, target, status, modified, restorable, or size")
	}
}

func sortJobQuarantineItems(items []jobQuarantineItem, sortMode string) {
	sortMode = strings.ToLower(strings.TrimSpace(sortMode))
	if sortMode == "" {
		sortMode = "path"
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		switch sortMode {
		case "id":
			if left.ID != right.ID {
				return left.ID < right.ID
			}
		case "ticket":
			if left.Ticket != right.Ticket {
				return left.Ticket < right.Ticket
			}
		case "target":
			if left.Target != right.Target {
				return left.Target < right.Target
			}
		case "status":
			if left.Status != right.Status {
				return left.Status < right.Status
			}
		case "modified":
			if !left.ModTime.Equal(right.ModTime) {
				return left.ModTime.After(right.ModTime)
			}
		case "restorable":
			if left.Restorable != right.Restorable {
				return left.Restorable && !right.Restorable
			}
		case "size":
			if left.Size != right.Size {
				return left.Size > right.Size
			}
		case "path":
			if left.Path != right.Path {
				return left.Path < right.Path
			}
		}
		return left.Path < right.Path
	})
}

func normalizeJobQuarantinePath(raw string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(raw))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe quarantine path %q", raw)
	}
	slash := filepath.ToSlash(clean)
	if !strings.HasPrefix(slash, jobQuarantineDir+"/") {
		slash = jobQuarantineDir + "/" + slash
	}
	parts := strings.Split(slash, "/")
	if len(parts) < 3 || parts[0] != jobQuarantineDir {
		return "", fmt.Errorf("quarantine path must look like quarantine/<timestamp>/<file>.toml")
	}
	if !strings.HasSuffix(slash, ".toml") {
		return "", fmt.Errorf("quarantine path must name a .toml file")
	}
	return filepath.FromSlash(slash), nil
}

func pruneEmptyJobQuarantineDirs(jobsRoot, dir string) {
	stop := filepath.Join(jobsRoot, jobQuarantineDir)
	for {
		if dir == "" || dir == "." || dir == stop || !strings.HasPrefix(dir, stop) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func parseJobQuarantineCommandFormat(cmd *cobra.Command, command, format string, jsonOut bool) (*template.Template, error) {
	if format != "" && jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: --format cannot be combined with --json.\n", command)
		return nil, exitErr(2)
	}
	tmpl, err := parseJobQuarantineFormat(format)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", command, err)
		return nil, exitErr(2)
	}
	return tmpl, nil
}

func parseJobQuarantineFormat(format string) (*template.Template, error) {
	if strings.TrimSpace(format) == "" {
		return nil, nil
	}
	tmpl, err := template.New("job-quarantine-format").Parse(format)
	if err != nil {
		return nil, fmt.Errorf("invalid --format template: %w", err)
	}
	return tmpl, nil
}

func renderJobQuarantineList(w io.Writer, items []jobQuarantineItem, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		if items == nil {
			items = []jobQuarantineItem{}
		}
		return json.NewEncoder(w).Encode(items)
	}
	if tmpl != nil {
		for _, item := range items {
			if err := renderJobQuarantineTemplate(w, item, tmpl); err != nil {
				return err
			}
		}
		return nil
	}
	if len(items) == 0 {
		fmt.Fprintln(w, "(no quarantined job files)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PATH\tID\tTICKET\tTARGET\tSTATUS\tRESTORABLE\tPROBLEM")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Path,
			emptyDash(item.ID),
			emptyDash(item.Ticket),
			emptyDash(item.Target),
			emptyDash(string(item.Status)),
			jobQuarantineRestorableText(item.Restorable),
			emptyDash(item.Problem))
	}
	return tw.Flush()
}

func renderJobQuarantineShow(w io.Writer, result jobQuarantineShowResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderJobQuarantineTemplate(w, result, tmpl)
	}
	fmt.Fprintf(w, "Path:       %s\n", result.Path)
	fmt.Fprintf(w, "ID:         %s\n", emptyDash(result.ID))
	fmt.Fprintf(w, "Ticket:     %s\n", emptyDash(result.Ticket))
	fmt.Fprintf(w, "Target:     %s\n", emptyDash(result.Target))
	fmt.Fprintf(w, "Status:     %s\n", emptyDash(string(result.Status)))
	fmt.Fprintf(w, "Restore:    %s\n", emptyDash(result.RestorePath))
	fmt.Fprintf(w, "Restorable: %s\n", jobQuarantineRestorableText(result.Restorable))
	fmt.Fprintf(w, "Size:       %d\n", result.Size)
	if !result.ModTime.IsZero() {
		fmt.Fprintf(w, "Modified:   %s\n", result.ModTime.Format(time.RFC3339))
	}
	if result.Problem != "" {
		fmt.Fprintf(w, "Problem:    %s\n", result.Problem)
	}
	if actions := jobQuarantineShowActions(result); len(actions) > 0 {
		fmt.Fprintln(w, "Actions:")
		for _, action := range actions {
			fmt.Fprintf(w, "  %s\n", action)
		}
	}
	return nil
}

func renderJobQuarantineRestore(w io.Writer, result jobQuarantineRestoreResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderJobQuarantineTemplate(w, result, tmpl)
	}
	switch result.Action {
	case "would_restore":
		fmt.Fprintf(w, "Would restore %s -> %s\n", result.Path, result.Destination)
	default:
		fmt.Fprintf(w, "Restored %s -> %s\n", result.Path, result.Destination)
	}
	return nil
}

func renderJobQuarantineDrop(w io.Writer, result jobQuarantineDropResult, jsonOut bool, tmpl *template.Template) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	if tmpl != nil {
		return renderJobQuarantineTemplate(w, result, tmpl)
	}
	switch result.Action {
	case "would_drop":
		fmt.Fprintf(w, "Would drop %s\n", result.Path)
	default:
		fmt.Fprintf(w, "Dropped %s\n", result.Path)
	}
	return nil
}

func jobQuarantineShowActions(result jobQuarantineShowResult) []string {
	if result.Path == "" {
		return nil
	}
	actions := []string{}
	if result.Restorable {
		actions = append(actions, fmt.Sprintf("agent-team job quarantine restore %s --dry-run", result.Path))
	}
	actions = append(actions, fmt.Sprintf("agent-team job quarantine drop %s --dry-run", result.Path))
	return actions
}

func jobQuarantineRestorableText(restorable bool) string {
	if restorable {
		return "yes"
	}
	return "no"
}

func renderJobQuarantineTemplate(w io.Writer, value any, tmpl *template.Template) error {
	if err := tmpl.Execute(w, value); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
