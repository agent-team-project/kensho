package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/jamesaud/agent-team/internal/usage"
	"github.com/spf13/cobra"
)

type usageGroupBy string

const (
	usageByJob      usageGroupBy = "job"
	usageByInstance usageGroupBy = "instance"
	usageByAgent    usageGroupBy = "agent"
	usageByRuntime  usageGroupBy = "runtime"
	usageByTeam     usageGroupBy = "team"
)

func newUsageCmd() *cobra.Command {
	var (
		target  string
		since   string
		by      string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show runtime token usage rollups.",
		Long:  "Show runtime usage rollups captured from finalized daemon-managed instances and persisted onto durable jobs.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			group, err := parseUsageGroupBy(by)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team usage: %v\n", err)
				return exitErr(2)
			}
			var sinceAt *time.Time
			if strings.TrimSpace(since) != "" {
				ts, err := parseUsageSince(since, time.Now)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "agent-team usage: %v\n", err)
					return exitErr(2)
				}
				sinceAt = &ts
			}
			teamDir, err := resolveTeamDir(cmd, target)
			if err != nil {
				return err
			}
			records, err := collectUsageRecords(teamDir, sinceAt)
			if err != nil {
				return err
			}
			rows := rollupUsageRecords(records, group)
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			renderUsageRollups(cmd.OutOrStdout(), rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", cwd, legacyRepoTargetFlagHelp)
	cmd.Flags().StringVar(&since, "since", "", "Only include usage captured since a duration ago (for example 7d, 24h) or an RFC3339 timestamp.")
	cmd.Flags().StringVar(&by, "by", string(usageByJob), "Group usage by job, instance, agent, runtime, or team.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit usage rollups as JSON.")
	return cmd
}

func parseUsageSince(raw string, now func() time.Time) (time.Time, error) {
	value := strings.TrimSpace(raw)
	lower := strings.ToLower(value)
	if strings.HasSuffix(lower, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(lower, "d"))
		if err != nil || days < 0 {
			return time.Time{}, fmt.Errorf("--since duration must be >= 0")
		}
		if now == nil {
			now = time.Now
		}
		return now().Add(-time.Duration(days) * 24 * time.Hour), nil
	}
	return parseEventSince(value, now)
}

func parseUsageGroupBy(raw string) (usageGroupBy, error) {
	switch usageGroupBy(strings.ToLower(strings.TrimSpace(raw))) {
	case "", usageByJob:
		return usageByJob, nil
	case usageByInstance:
		return usageByInstance, nil
	case usageByAgent:
		return usageByAgent, nil
	case usageByRuntime:
		return usageByRuntime, nil
	case usageByTeam:
		return usageByTeam, nil
	default:
		return "", fmt.Errorf("--by must be job, instance, agent, runtime, or team")
	}
}

type usageAttributedRecord struct {
	JobID  string
	Ticket string
	Team   string
	Record usage.Record
}

type usageRollupRow struct {
	By       usageGroupBy  `json:"by"`
	Key      string        `json:"key"`
	JobID    string        `json:"job_id,omitempty"`
	Ticket   string        `json:"ticket,omitempty"`
	Instance string        `json:"instance,omitempty"`
	Agent    string        `json:"agent,omitempty"`
	Runtime  string        `json:"runtime,omitempty"`
	Team     string        `json:"team,omitempty"`
	Usage    usage.Summary `json:"usage"`
}

func collectUsageRecords(teamDir string, since *time.Time) ([]usageAttributedRecord, error) {
	jobs, err := job.List(teamDir)
	if err != nil {
		return nil, err
	}
	archived, err := job.ListArchived(teamDir)
	if err != nil {
		return nil, err
	}
	jobs = append(jobs, archived...)
	top, _ := topology.LoadFromTeamDir(teamDir)
	records := make([]usageAttributedRecord, 0)
	seen := map[string]bool{}
	for _, j := range jobs {
		if j == nil || j.Usage == nil {
			continue
		}
		for _, rec := range j.Usage.Records {
			if !usageRecordSince(rec, since) {
				continue
			}
			key := usageDedupKey(j.ID, rec)
			if seen[key] {
				continue
			}
			seen[key] = true
			records = append(records, usageAttributedRecord{JobID: j.ID, Ticket: j.Ticket, Team: usageTeamForJob(top, j, rec), Record: rec})
		}
	}
	metas, err := daemon.ListMetadata(daemon.DaemonRoot(teamDir))
	if err != nil {
		return nil, err
	}
	for _, meta := range metas {
		if meta == nil || meta.Usage == nil || !usageRecordSince(*meta.Usage, since) {
			continue
		}
		jobID := job.IDFromInput(meta.Job)
		key := usageDedupKey(jobID, *meta.Usage)
		if seen[key] {
			continue
		}
		seen[key] = true
		records = append(records, usageAttributedRecord{JobID: jobID, Ticket: meta.Ticket, Team: usageTeamForMetadata(top, meta), Record: *meta.Usage})
	}
	return records, nil
}

func usageDedupKey(jobID string, rec usage.Record) string {
	return strings.TrimSpace(jobID) + "|" + usage.RecordKey(rec)
}

func usageRecordSince(rec usage.Record, since *time.Time) bool {
	if since == nil {
		return true
	}
	ts := usageRecordTime(rec)
	return ts.IsZero() || !ts.Before(since.UTC())
}

func usageRecordTime(rec usage.Record) time.Time {
	for _, ts := range []time.Time{rec.EndedAt, rec.CapturedAt, rec.StartedAt} {
		if !ts.IsZero() {
			return ts.UTC()
		}
	}
	return time.Time{}
}

func rollupUsageRecords(records []usageAttributedRecord, by usageGroupBy) []usageRollupRow {
	byKey := map[string]*usageRollupRow{}
	recordSets := map[string][]usage.Record{}
	for _, attributed := range records {
		key := usageRollupKey(attributed, by)
		if key == "" {
			key = "-"
		}
		row := byKey[key]
		if row == nil {
			row = &usageRollupRow{By: by, Key: key}
			applyUsageRollupIdentity(row, attributed, by)
			byKey[key] = row
		}
		recordSets[key] = append(recordSets[key], attributed.Record)
	}
	rows := make([]usageRollupRow, 0, len(byKey))
	for key, row := range byKey {
		row.Usage = usage.Summarize(recordSets[key])
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Usage.InputTokens != rows[j].Usage.InputTokens {
			return rows[i].Usage.InputTokens > rows[j].Usage.InputTokens
		}
		if rows[i].Usage.OutputTokens != rows[j].Usage.OutputTokens {
			return rows[i].Usage.OutputTokens > rows[j].Usage.OutputTokens
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

func usageRollupKey(attributed usageAttributedRecord, by usageGroupBy) string {
	rec := attributed.Record
	switch by {
	case usageByJob:
		return strings.TrimSpace(attributed.JobID)
	case usageByInstance:
		return strings.TrimSpace(rec.Instance)
	case usageByAgent:
		return strings.TrimSpace(rec.Agent)
	case usageByRuntime:
		return strings.TrimSpace(rec.Runtime)
	case usageByTeam:
		return strings.TrimSpace(attributed.Team)
	default:
		return strings.TrimSpace(attributed.JobID)
	}
}

func applyUsageRollupIdentity(row *usageRollupRow, attributed usageAttributedRecord, by usageGroupBy) {
	rec := attributed.Record
	switch by {
	case usageByJob:
		row.JobID = attributed.JobID
		row.Ticket = attributed.Ticket
	case usageByInstance:
		row.Instance = rec.Instance
		row.JobID = attributed.JobID
		row.Ticket = attributed.Ticket
		row.Agent = rec.Agent
		row.Runtime = rec.Runtime
	case usageByAgent:
		row.Agent = rec.Agent
	case usageByRuntime:
		row.Runtime = rec.Runtime
	case usageByTeam:
		row.Team = attributed.Team
	}
}

func usageTeamForJob(top *topology.Topology, j *job.Job, rec usage.Record) string {
	if team := strings.TrimSpace(rec.Origin.Team); team != "" {
		return team
	}
	if j == nil {
		return ""
	}
	if team := strings.TrimSpace(j.Origin.Team); team != "" {
		return team
	}
	if team := topologyTeamForPipeline(top, j.Pipeline); team != "" {
		return team
	}
	if team := topologyTeamForInstanceRow(top, rec.Instance); team != "" {
		return team
	}
	return topologyTeamForInstanceRow(top, j.Instance)
}

func usageTeamForMetadata(top *topology.Topology, meta *daemon.Metadata) string {
	if meta == nil {
		return ""
	}
	if meta.Usage != nil {
		if team := strings.TrimSpace(meta.Usage.Origin.Team); team != "" {
			return team
		}
	}
	if team := strings.TrimSpace(meta.Origin.Team); team != "" {
		return team
	}
	return topologyTeamForInstanceRow(top, meta.Instance)
}

func topologyTeamForPipeline(top *topology.Topology, pipeline string) string {
	pipeline = strings.TrimSpace(pipeline)
	if top == nil || pipeline == "" {
		return ""
	}
	for _, team := range top.SortedTeams() {
		for _, name := range team.Pipelines {
			if strings.TrimSpace(name) == pipeline {
				return team.Name
			}
		}
	}
	return ""
}

func renderUsageRollups(w io.Writer, rows []usageRollupRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no usage records)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tRUNS\tTOKEN_RUNS\tTURNS\tDURATION\tINPUT\tCACHED\tOUTPUT\tREASONING")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d/%d\t%d\t%s\t%d\t%d\t%d\t%d\n",
			emptyDash(row.Key),
			row.Usage.Runs,
			row.Usage.TokenAvailableRuns,
			row.Usage.Runs,
			row.Usage.Turns,
			formatUsageDuration(row.Usage.DurationMS),
			row.Usage.InputTokens,
			row.Usage.CachedInputTokens,
			row.Usage.OutputTokens,
			row.Usage.ReasoningOutputTokens)
	}
	_ = tw.Flush()
}

func renderJobUsageSummary(w io.Writer, label string, u *usage.JobUsage) {
	if u == nil || u.Summary.Runs == 0 {
		return
	}
	if strings.TrimSpace(label) == "" {
		label = "Usage"
	}
	s := u.Summary
	fmt.Fprintf(w, "%s:       runs=%d token_runs=%d/%d turns=%d duration=%s input=%d cached=%d output=%d reasoning=%d\n",
		label,
		s.Runs,
		s.TokenAvailableRuns,
		s.Runs,
		s.Turns,
		formatUsageDuration(s.DurationMS),
		s.InputTokens,
		s.CachedInputTokens,
		s.OutputTokens,
		s.ReasoningOutputTokens)
}

func formatUsageDuration(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	d := time.Duration(ms) * time.Millisecond
	return d.Truncate(time.Millisecond).String()
}
