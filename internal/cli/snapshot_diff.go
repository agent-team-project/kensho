package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newSnapshotDiffCmd() *cobra.Command {
	var (
		jsonOut  bool
		exitCode bool
	)
	cmd := &cobra.Command{
		Use:   "diff <before.json> <after.json>",
		Short: "Compare two saved diagnostic snapshots.",
		Long: "Compare two saved global, team, or pipeline diagnostic snapshot JSON files and summarize " +
			"job, queue, pipeline, ready-advance, and section-error changes.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := diffSnapshotFiles(args[0], args[1])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team snapshot diff: %v\n", err)
				return exitErr(1)
			}
			if jsonOut {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return err
				}
			} else {
				renderSnapshotDiff(cmd.OutOrStdout(), result)
			}
			if exitCode && result.Summary.TotalChanges > 0 {
				return exitErr(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit snapshot diff as JSON.")
	cmd.Flags().BoolVar(&exitCode, "exit-code", false, "Exit with status 1 when snapshots differ.")
	return cmd
}

type snapshotDiffInput struct {
	Version         string                  `json:"version,omitempty"`
	CapturedAt      string                  `json:"captured_at,omitempty"`
	Repo            string                  `json:"repo,omitempty"`
	Team            *teamInfo               `json:"team,omitempty"`
	Pipeline        string                  `json:"pipeline,omitempty"`
	Jobs            []snapshotDiffJob       `json:"jobs,omitempty"`
	Queue           []snapshotDiffQueueItem `json:"queue,omitempty"`
	PipelineStatus  []pipelineStatusRow     `json:"pipeline_status,omitempty"`
	Status          *pipelineStatusRow      `json:"status,omitempty"`
	PipelineAdvance []snapshotDiffAdvance   `json:"pipeline_advance_preview,omitempty"`
	AdvancePreview  []snapshotDiffAdvance   `json:"advance_preview,omitempty"`
	SectionErrors   map[string]string       `json:"section_errors,omitempty"`
}

type snapshotDiffJob struct {
	ID       string `json:"id"`
	Status   string `json:"status,omitempty"`
	Pipeline string `json:"pipeline,omitempty"`
	Target   string `json:"target,omitempty"`
	Instance string `json:"instance,omitempty"`
}

type snapshotDiffQueueItem struct {
	ID    string `json:"id"`
	State string `json:"state,omitempty"`
}

type snapshotDiffAdvance struct {
	JobID      string `json:"job_id"`
	Pipeline   string `json:"pipeline,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	Target     string `json:"target,omitempty"`
	StepStatus string `json:"step_status,omitempty"`
	Action     string `json:"action,omitempty"`
}

type snapshotDiffResult struct {
	Before  snapshotDiffMeta     `json:"before"`
	After   snapshotDiffMeta     `json:"after"`
	Summary snapshotDiffSummary  `json:"summary"`
	Changes []snapshotDiffChange `json:"changes,omitempty"`
}

type snapshotDiffMeta struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Scope      string `json:"scope,omitempty"`
	CapturedAt string `json:"captured_at,omitempty"`
	Repo       string `json:"repo,omitempty"`
}

type snapshotDiffSummary struct {
	TotalChanges  int                  `json:"total_changes"`
	Jobs          snapshotDiffCounters `json:"jobs"`
	Pipelines     snapshotDiffCounters `json:"pipelines"`
	Queue         snapshotDiffCounters `json:"queue"`
	Advance       snapshotDiffCounters `json:"advance"`
	SectionErrors snapshotDiffCounters `json:"section_errors"`
}

type snapshotDiffCounters struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

type snapshotDiffChange struct {
	Section string `json:"section"`
	ID      string `json:"id"`
	Action  string `json:"action"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
}

type snapshotDiffComparable struct {
	Meta          snapshotDiffMeta
	Jobs          map[string]string
	Pipelines     map[string]string
	Queue         map[string]string
	Advance       map[string]string
	SectionErrors map[string]string
}

func diffSnapshotFiles(beforePath, afterPath string) (*snapshotDiffResult, error) {
	before, err := readSnapshotDiffComparable(beforePath)
	if err != nil {
		return nil, err
	}
	after, err := readSnapshotDiffComparable(afterPath)
	if err != nil {
		return nil, err
	}
	result := &snapshotDiffResult{
		Before: before.Meta,
		After:  after.Meta,
	}
	result.Changes = append(result.Changes, diffSnapshotStringMaps("jobs", before.Jobs, after.Jobs, &result.Summary.Jobs)...)
	result.Changes = append(result.Changes, diffSnapshotStringMaps("pipelines", before.Pipelines, after.Pipelines, &result.Summary.Pipelines)...)
	result.Changes = append(result.Changes, diffSnapshotStringMaps("queue", before.Queue, after.Queue, &result.Summary.Queue)...)
	result.Changes = append(result.Changes, diffSnapshotStringMaps("advance", before.Advance, after.Advance, &result.Summary.Advance)...)
	result.Changes = append(result.Changes, diffSnapshotStringMaps("section_errors", before.SectionErrors, after.SectionErrors, &result.Summary.SectionErrors)...)
	result.Summary.TotalChanges = len(result.Changes)
	return result, nil
}

func readSnapshotDiffComparable(path string) (snapshotDiffComparable, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return snapshotDiffComparable{}, err
	}
	var input snapshotDiffInput
	if err := json.Unmarshal(body, &input); err != nil {
		return snapshotDiffComparable{}, fmt.Errorf("%s: %w", path, err)
	}
	return snapshotDiffComparableFromInput(path, input), nil
}

func snapshotDiffComparableFromInput(path string, input snapshotDiffInput) snapshotDiffComparable {
	scope, kind := snapshotDiffScope(input)
	out := snapshotDiffComparable{
		Meta: snapshotDiffMeta{
			Path:       path,
			Kind:       kind,
			Scope:      scope,
			CapturedAt: input.CapturedAt,
			Repo:       input.Repo,
		},
		Jobs:          map[string]string{},
		Pipelines:     map[string]string{},
		Queue:         map[string]string{},
		Advance:       map[string]string{},
		SectionErrors: map[string]string{},
	}
	for _, j := range input.Jobs {
		id := strings.TrimSpace(j.ID)
		if id == "" {
			continue
		}
		out.Jobs[id] = compactSnapshotDiffValue(j.Status, j.Pipeline, j.Target, j.Instance)
	}
	for _, q := range input.Queue {
		id := strings.TrimSpace(q.ID)
		if id == "" {
			continue
		}
		out.Queue[id] = emptyDash(q.State)
	}
	for _, row := range input.PipelineStatus {
		addSnapshotDiffPipelineMetrics(out.Pipelines, row, input.Pipeline)
	}
	if input.Status != nil {
		addSnapshotDiffPipelineMetrics(out.Pipelines, *input.Status, input.Pipeline)
	}
	for _, advance := range input.PipelineAdvance {
		addSnapshotDiffAdvance(out.Advance, advance)
	}
	for _, advance := range input.AdvancePreview {
		addSnapshotDiffAdvance(out.Advance, advance)
	}
	for key, value := range input.SectionErrors {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out.SectionErrors[key] = strings.TrimSpace(value)
	}
	return out
}

func snapshotDiffScope(input snapshotDiffInput) (string, string) {
	if strings.TrimSpace(input.Pipeline) != "" {
		return strings.TrimSpace(input.Pipeline), "pipeline"
	}
	if input.Team != nil && strings.TrimSpace(input.Team.Name) != "" {
		return strings.TrimSpace(input.Team.Name), "team"
	}
	if strings.TrimSpace(input.Repo) != "" {
		return strings.TrimSpace(input.Repo), "repo"
	}
	return "", "snapshot"
}

func addSnapshotDiffPipelineMetrics(out map[string]string, row pipelineStatusRow, fallbackPipeline string) {
	pipeline := strings.TrimSpace(row.Pipeline)
	if pipeline == "" {
		pipeline = strings.TrimSpace(fallbackPipeline)
	}
	if pipeline == "" {
		return
	}
	metrics := map[string]int{
		"jobs":          row.Jobs,
		"ready_steps":   row.ReadySteps,
		"manual_gates":  row.ManualGates,
		"failed_steps":  row.FailedSteps,
		"blocked_steps": row.BlockedSteps,
		"queued_steps":  row.QueuedSteps,
		"running_steps": row.RunningSteps,
		"done_steps":    row.DoneSteps,
	}
	keys := make([]string, 0, len(metrics))
	for key := range metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out[pipeline+"."+key] = fmt.Sprintf("%d", metrics[key])
	}
}

func addSnapshotDiffAdvance(out map[string]string, advance snapshotDiffAdvance) {
	jobID := strings.TrimSpace(advance.JobID)
	if jobID == "" {
		return
	}
	id := jobID
	if step := strings.TrimSpace(advance.StepID); step != "" {
		id += ":" + step
	}
	out[id] = compactSnapshotDiffValue(advance.Action, advance.Pipeline, advance.Target, advance.StepStatus)
}

func compactSnapshotDiffValue(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return "-"
	}
	return strings.Join(clean, "|")
}

func diffSnapshotStringMaps(section string, before, after map[string]string, counters *snapshotDiffCounters) []snapshotDiffChange {
	keys := make([]string, 0, len(before)+len(after))
	seen := map[string]bool{}
	for key := range before {
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	for key := range after {
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Strings(keys)
	changes := []snapshotDiffChange{}
	for _, key := range keys {
		beforeValue, beforeOK := before[key]
		afterValue, afterOK := after[key]
		switch {
		case !beforeOK && afterOK:
			counters.Added++
			changes = append(changes, snapshotDiffChange{Section: section, ID: key, Action: "added", After: afterValue})
		case beforeOK && !afterOK:
			counters.Removed++
			changes = append(changes, snapshotDiffChange{Section: section, ID: key, Action: "removed", Before: beforeValue})
		case beforeOK && afterOK && beforeValue != afterValue:
			counters.Changed++
			changes = append(changes, snapshotDiffChange{Section: section, ID: key, Action: "changed", Before: beforeValue, After: afterValue})
		}
	}
	return changes
}

func renderSnapshotDiff(w io.Writer, result *snapshotDiffResult) {
	if result == nil {
		fmt.Fprintln(w, "snapshot diff: unavailable")
		return
	}
	fmt.Fprintf(w, "snapshot diff: %s -> %s\n", result.Before.Path, result.After.Path)
	fmt.Fprintf(w, "before: kind=%s scope=%s captured_at=%s\n", result.Before.Kind, emptyDash(result.Before.Scope), emptyDash(result.Before.CapturedAt))
	fmt.Fprintf(w, "after: kind=%s scope=%s captured_at=%s\n", result.After.Kind, emptyDash(result.After.Scope), emptyDash(result.After.CapturedAt))
	fmt.Fprintf(w, "changes: total=%d\n", result.Summary.TotalChanges)
	renderSnapshotDiffCounterLine(w, "jobs", result.Summary.Jobs)
	renderSnapshotDiffCounterLine(w, "pipelines", result.Summary.Pipelines)
	renderSnapshotDiffCounterLine(w, "queue", result.Summary.Queue)
	renderSnapshotDiffCounterLine(w, "advance", result.Summary.Advance)
	renderSnapshotDiffCounterLine(w, "section_errors", result.Summary.SectionErrors)
	if len(result.Changes) == 0 {
		fmt.Fprintln(w, "details: none")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SECTION\tID\tACTION\tBEFORE\tAFTER")
	for _, change := range result.Changes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			change.Section,
			change.ID,
			change.Action,
			emptyDash(change.Before),
			emptyDash(change.After))
	}
	_ = tw.Flush()
}

func renderSnapshotDiffCounterLine(w io.Writer, label string, counters snapshotDiffCounters) {
	fmt.Fprintf(w, "%s: added=%d removed=%d changed=%d\n", label, counters.Added, counters.Removed, counters.Changed)
}
