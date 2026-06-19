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
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/spf13/cobra"
)

const queueQuarantineDir = "quarantine"

type queueQuarantineItem struct {
	Path        string    `json:"path"`
	State       string    `json:"state,omitempty"`
	ID          string    `json:"id,omitempty"`
	EventType   string    `json:"event_type,omitempty"`
	Instance    string    `json:"instance,omitempty"`
	InstanceID  string    `json:"instance_id,omitempty"`
	Job         string    `json:"job,omitempty"`
	RestorePath string    `json:"restore_path,omitempty"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time"`
	Restorable  bool      `json:"restorable"`
	Problem     string    `json:"problem,omitempty"`
}

type queueQuarantineRestoreResult struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	State       string `json:"state,omitempty"`
	ID          string `json:"id,omitempty"`
	Action      string `json:"action"`
	DryRun      bool   `json:"dry_run,omitempty"`
	Overwrite   bool   `json:"overwrite,omitempty"`
}

type queueQuarantineDropResult struct {
	Path       string `json:"path"`
	State      string `json:"state,omitempty"`
	ID         string `json:"id,omitempty"`
	Restorable bool   `json:"restorable"`
	Action     string `json:"action"`
	DryRun     bool   `json:"dry_run,omitempty"`
	Dropped    bool   `json:"dropped,omitempty"`
}

func newQueueQuarantineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quarantine",
		Short: "Inspect, restore, and drop quarantined queue files.",
		Long:  "Inspect queue files moved under `.agent_team/daemon/queue/quarantine/`, restore validated entries to the active queue, or explicitly drop preserved files.",
	}
	cmd.AddCommand(newQueueQuarantineLsCmd())
	cmd.AddCommand(newQueueQuarantineRestoreCmd())
	cmd.AddCommand(newQueueQuarantineDropCmd())
	return cmd
}

func newQueueQuarantineLsCmd() *cobra.Command {
	var (
		target  string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List quarantined queue files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			items, err := listQueueQuarantine(teamDir)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine ls: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineList(cmd.OutOrStdout(), items, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit quarantined queue files as JSON.")
	return cmd
}

func newQueueQuarantineRestoreCmd() *cobra.Command {
	var (
		target  string
		dryRun  bool
		force   bool
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "restore <quarantine-path>",
		Short: "Restore one validated quarantined queue file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			result, err := restoreQueueQuarantine(teamDir, args[0], dryRun, force)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine restore: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineRestore(cmd.OutOrStdout(), result, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the restore without moving files.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing active queue file with the same restore path.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit restore result as JSON.")
	return cmd
}

func newQueueQuarantineDropCmd() *cobra.Command {
	var (
		target       string
		dropAll      bool
		dryRun       bool
		unrestorable bool
		olderThan    time.Duration
		jsonOut      bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "drop <quarantine-path>",
		Short: "Drop quarantined queue files after inspection.",
		Long:  "Drop one quarantined queue file by path, or drop a filtered batch with --all.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if olderThan < 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --older-than must be >= 0.")
				return exitErr(2)
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			if dropAll {
				if len(args) != 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --all cannot be combined with a path.")
					return exitErr(2)
				}
				results, err := dropQueueQuarantineAll(teamDir, dryRun, olderThan, unrestorable, time.Now().UTC())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine drop: %v\n", err)
					return exitErr(1)
				}
				return renderQueueQuarantineDrop(cmd.OutOrStdout(), results, jsonOut)
			}
			if len(args) != 1 {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: requires one path unless --all is set.")
				return exitErr(2)
			}
			if olderThan > 0 || unrestorable {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team queue quarantine drop: --older-than and --unrestorable require --all.")
				return exitErr(2)
			}
			result, err := dropQueueQuarantine(teamDir, args[0], dryRun)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team queue quarantine drop: %v\n", err)
				return exitErr(1)
			}
			return renderQueueQuarantineDrop(cmd.OutOrStdout(), []queueQuarantineDropResult{result}, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, "Repo root.")
	cmd.Flags().BoolVar(&dropAll, "all", false, "Drop all matching quarantined files instead of one path.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview quarantined files that would be dropped.")
	cmd.Flags().BoolVar(&unrestorable, "unrestorable", false, "With --all, only drop quarantined files that cannot be restored.")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "With --all, only drop files older than this duration based on file mtime.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit drop results as JSON.")
	return cmd
}

func listQueueQuarantine(teamDir string) ([]queueQuarantineItem, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	root := filepath.Join(queueRoot, queueQuarantineDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	var items []queueQuarantineItem
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		rel, err := filepath.Rel(queueRoot, path)
		if err != nil {
			return err
		}
		item, err := inspectQueueQuarantineFile(queueRoot, rel)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Path < items[j].Path
	})
	return items, nil
}

func inspectQueueQuarantineFile(queueRoot, rel string) (queueQuarantineItem, error) {
	source, err := queueDoctorSafeQueuePath(queueRoot, rel)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return queueQuarantineItem{}, err
	}
	item := queueQuarantineItem{
		Path:    filepath.Clean(rel),
		State:   queueQuarantineState(rel),
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
	if item.State != "" {
		item.RestorePath = filepath.Join(item.State, filepath.Base(item.Path))
	}
	body, err := os.ReadFile(source)
	if err != nil {
		item.Problem = err.Error()
		return item, nil
	}
	var raw daemon.QueueItem
	if err := json.Unmarshal(body, &raw); err != nil {
		item.Problem = fmt.Sprintf("invalid JSON: %v", err)
		return item, nil
	}
	idFromPath := strings.TrimSuffix(filepath.Base(item.Path), ".json")
	if strings.TrimSpace(raw.ID) == "" {
		raw.ID = idFromPath
	}
	raw.State = item.State
	item.ID = raw.ID
	item.EventType = raw.EventType
	item.Instance = raw.Instance
	item.InstanceID = raw.InstanceID
	item.Job = queueQuarantineJob(raw.Payload)
	if err := validateQueueQuarantineRestore(raw); err != nil {
		item.Problem = err.Error()
		return item, nil
	}
	item.Restorable = true
	return item, nil
}

func queueQuarantineState(rel string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) < 4 || parts[0] != queueQuarantineDir {
		return ""
	}
	switch parts[2] {
	case daemon.QueueStatePending, daemon.QueueStateDead:
		return parts[2]
	default:
		return ""
	}
}

func filterQueueQuarantineItems(items []queueQuarantineItem, filters queueListFilters) []queueQuarantineItem {
	if filters.empty() {
		return items
	}
	out := make([]queueQuarantineItem, 0, len(items))
	for _, item := range items {
		if filters.state != "" && item.State != filters.state {
			continue
		}
		if len(filters.instances) > 0 && !filters.instances[item.Instance] {
			continue
		}
		if len(filters.eventTypes) > 0 && !filters.eventTypes[item.EventType] {
			continue
		}
		if len(filters.jobs) > 0 && !filters.jobs[job.NormalizeID(item.Job)] {
			continue
		}
		if filters.readyOnly && item.State != daemon.QueueStatePending {
			continue
		}
		out = append(out, item)
	}
	return out
}

func queueQuarantineJob(payload map[string]any) string {
	for _, key := range []string{"job_id", "job", "ticket"} {
		if id := job.NormalizeID(queuePayloadString(payload, key)); id != "" {
			return id
		}
	}
	return ""
}

func validateQueueQuarantineRestore(item daemon.QueueItem) error {
	switch item.State {
	case daemon.QueueStatePending, daemon.QueueStateDead:
	default:
		return fmt.Errorf("queue state is required in quarantine path")
	}
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(item.EventType) == "" {
		return fmt.Errorf("event_type is required")
	}
	if strings.TrimSpace(item.Instance) == "" {
		return fmt.Errorf("instance is required")
	}
	if strings.TrimSpace(item.InstanceID) == "" {
		return fmt.Errorf("instance_id is required")
	}
	if item.QueuedAt.IsZero() {
		return fmt.Errorf("queued_at is required")
	}
	if item.UpdatedAt.IsZero() {
		return fmt.Errorf("updated_at is required")
	}
	return nil
}

func restoreQueueQuarantine(teamDir, rawPath string, dryRun, force bool) (queueQuarantineRestoreResult, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	rel, err := normalizeQueueQuarantinePath(rawPath)
	if err != nil {
		return queueQuarantineRestoreResult{}, err
	}
	item, err := inspectQueueQuarantineFile(queueRoot, rel)
	if err != nil {
		return queueQuarantineRestoreResult{}, err
	}
	if !item.Restorable {
		return queueQuarantineRestoreResult{}, fmt.Errorf("%s is not restorable: %s", item.Path, item.Problem)
	}
	source, err := queueDoctorSafeQueuePath(queueRoot, item.Path)
	if err != nil {
		return queueQuarantineRestoreResult{}, err
	}
	destination, err := queueDoctorSafeQueuePath(queueRoot, item.RestorePath)
	if err != nil {
		return queueQuarantineRestoreResult{}, err
	}
	if _, err := os.Stat(destination); err == nil && !force {
		return queueQuarantineRestoreResult{}, fmt.Errorf("%s already exists; pass --force to overwrite it", item.RestorePath)
	} else if err != nil && !os.IsNotExist(err) {
		return queueQuarantineRestoreResult{}, err
	}
	result := queueQuarantineRestoreResult{
		Path:        item.Path,
		Destination: item.RestorePath,
		State:       item.State,
		ID:          item.ID,
		Action:      "would_restore",
		DryRun:      dryRun,
		Overwrite:   force,
	}
	if dryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return result, err
	}
	if force {
		_ = os.Remove(destination)
	}
	if err := os.Rename(source, destination); err != nil {
		return result, err
	}
	result.Action = "restored"
	result.DryRun = false
	return result, nil
}

func dropQueueQuarantine(teamDir, rawPath string, dryRun bool) (queueQuarantineDropResult, error) {
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	rel, err := normalizeQueueQuarantinePath(rawPath)
	if err != nil {
		return queueQuarantineDropResult{}, err
	}
	item, err := inspectQueueQuarantineFile(queueRoot, rel)
	if err != nil {
		return queueQuarantineDropResult{}, err
	}
	return dropQueueQuarantineItem(queueRoot, item, dryRun)
}

func dropQueueQuarantineAll(teamDir string, dryRun bool, olderThan time.Duration, unrestorable bool, now time.Time) ([]queueQuarantineDropResult, error) {
	items, err := listQueueQuarantine(teamDir)
	if err != nil {
		return nil, err
	}
	queueRoot := daemon.QueueRoot(daemon.DaemonRoot(teamDir))
	results := make([]queueQuarantineDropResult, 0, len(items))
	for _, item := range items {
		if unrestorable && item.Restorable {
			continue
		}
		if olderThan > 0 && item.ModTime.After(now.Add(-olderThan)) {
			continue
		}
		result, err := dropQueueQuarantineItem(queueRoot, item, dryRun)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func dropQueueQuarantineItem(queueRoot string, item queueQuarantineItem, dryRun bool) (queueQuarantineDropResult, error) {
	result := queueQuarantineDropResult{
		Path:       item.Path,
		State:      item.State,
		ID:         item.ID,
		Restorable: item.Restorable,
		Action:     "would_drop",
		DryRun:     dryRun,
	}
	if dryRun {
		return result, nil
	}
	source, err := queueDoctorSafeQueuePath(queueRoot, item.Path)
	if err != nil {
		return result, err
	}
	if err := os.Remove(source); err != nil {
		return result, err
	}
	pruneEmptyQueueQuarantineDirs(queueRoot, filepath.Dir(source))
	result.Action = "dropped"
	result.Dropped = true
	result.DryRun = false
	return result, nil
}

func pruneEmptyQueueQuarantineDirs(queueRoot, dir string) {
	stop := filepath.Join(queueRoot, queueQuarantineDir)
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

func normalizeQueueQuarantinePath(raw string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(raw))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe quarantine path %q", raw)
	}
	slash := filepath.ToSlash(clean)
	if !strings.HasPrefix(slash, queueQuarantineDir+"/") {
		slash = queueQuarantineDir + "/" + slash
	}
	if queueQuarantineState(filepath.FromSlash(slash)) == "" {
		return "", fmt.Errorf("quarantine path must look like quarantine/<timestamp>/pending/<file>.json or quarantine/<timestamp>/dead/<file>.json")
	}
	if !strings.HasSuffix(slash, ".json") {
		return "", fmt.Errorf("quarantine path must name a .json file")
	}
	return filepath.FromSlash(slash), nil
}

func renderQueueQuarantineList(w io.Writer, items []queueQuarantineItem, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(items)
	}
	if len(items) == 0 {
		fmt.Fprintln(w, "(no quarantined queue files)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PATH\tSTATE\tID\tINSTANCE\tEVENT\tJOB\tRESTORABLE\tPROBLEM")
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Path,
			emptyDash(item.State),
			emptyDash(item.ID),
			emptyDash(item.Instance),
			emptyDash(item.EventType),
			emptyDash(item.Job),
			queueQuarantineRestorableText(item.Restorable),
			emptyDash(item.Problem))
	}
	return tw.Flush()
}

func queueQuarantineRestorableText(restorable bool) string {
	if restorable {
		return "yes"
	}
	return "no"
}

func renderQueueQuarantineRestore(w io.Writer, result queueQuarantineRestoreResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	switch result.Action {
	case "would_restore":
		fmt.Fprintf(w, "Would restore %s -> %s\n", result.Path, result.Destination)
	default:
		fmt.Fprintf(w, "Restored %s -> %s\n", result.Path, result.Destination)
	}
	return nil
}

func renderQueueQuarantineDrop(w io.Writer, results []queueQuarantineDropResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(results)
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "(no quarantined queue files matched)")
		return nil
	}
	for _, result := range results {
		switch result.Action {
		case "would_drop":
			fmt.Fprintf(w, "Would drop %s\n", result.Path)
		default:
			fmt.Fprintf(w, "Dropped %s\n", result.Path)
		}
	}
	return nil
}
