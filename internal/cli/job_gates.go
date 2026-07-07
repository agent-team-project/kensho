package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

const (
	jobGateClassInfra   = job.GateClassInfra
	jobGateClassContent = job.GateClassContent
)

type jobGateResult struct {
	TS               time.Time      `json:"ts"`
	JobID            string         `json:"job_id"`
	Name             string         `json:"name"`
	Status           job.GateStatus `json:"status"`
	Class            string         `json:"class,omitempty"`
	Signature        string         `json:"signature,omitempty"`
	MatchedSignature string         `json:"matched_signature,omitempty"`
	MatchedPattern   string         `json:"matched_pattern,omitempty"`
	LogRef           string         `json:"log_ref,omitempty"`
	Actor            string         `json:"actor,omitempty"`
}

func newJobGateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate",
		Short: "Write durable per-job gate results.",
		Long:  "Write durable per-job gate results to the append-only gate log under `.agent_team/jobs/`.",
	}
	cmd.AddCommand(newJobGateSetCmd())
	return cmd
}

func newJobGateSetCmd() *cobra.Command {
	var (
		repo      string
		statusRaw string
		signature string
		logRef    string
		actor     string
		jsonOut   bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "set <job-id> <gate-name>",
		Short: "Append one gate result to a job.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("status") {
				fmt.Fprintln(cmd.ErrOrStderr(), "agent-team job gate set: --status is required.")
				return exitErr(2)
			}
			status, err := job.ParseGateStatus(statusRaw)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job gate set: %v\n", err)
				return exitErr(2)
			}
			teamDir, j, err := readJobAndTeamDir(cmd, repo, args[0])
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			record := &job.GateRecord{
				TS:        now,
				JobID:     j.ID,
				Name:      args[1],
				Status:    status,
				Signature: signature,
				LogRef:    logRef,
				Actor:     defaultJobGateActor(actor),
			}
			if err := auditCLIJobAuthority(teamDir, j, "job.gate.set", "job:"+j.ID+":gate:"+record.Name); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job gate set: %v\n", err)
				return exitErr(3)
			}
			if err := job.AppendGateRecord(teamDir, record); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job gate set: %v\n", err)
				return exitErr(1)
			}
			j.UpdatedAt = now
			j.LastEvent = "gate.updated"
			j.LastStatus = jobGateStatusMessage(record)
			if err := writeJobWithAudit(teamDir, j, "gate.updated", record.Actor, j.LastStatus, jobGateEventData(record)); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job gate set: %v\n", err)
				return exitErr(1)
			}
			results, err := classifyJobGateRecords(teamDir, j, []job.GateRecord{*record})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job gate set: %v\n", err)
				return exitErr(1)
			}
			result := gateResultFromRecord(*record, job.GateClassification{})
			if len(results) == 1 {
				result = results[0]
			}
			return renderJobGateSetResult(cmd.OutOrStdout(), result, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().StringVar(&statusRaw, "status", "", "Gate status: pass or fail.")
	cmd.Flags().StringVar(&signature, "signature", "", "Failure signature or short failure text used for infra/content classification.")
	cmd.Flags().StringVar(&logRef, "log-ref", "", "Path or URL with supporting gate output.")
	cmd.Flags().StringVar(&actor, "actor", "", "Actor recorded on the gate result; defaults to AGENT_TEAM_INSTANCE or cli.")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit the recorded gate result as JSON.")
	return cmd
}

func newJobGatesCmd() *cobra.Command {
	var (
		repo    string
		jsonOut bool
	)
	cwd, _ := os.Getwd()
	cmd := &cobra.Command{
		Use:   "gates <job-id>",
		Short: "Show latest gate results for one job.",
		Long:  "Show latest per-name gate results from a job's append-only gate log. Failed gates are classified as infra or content using the job pipeline's infra_signatures.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamDir, err := resolveTeamDir(cmd, repo)
			if err != nil {
				return err
			}
			j, err := job.ReadLiveOrArchive(teamDir, args[0])
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job gates: %v\n", err)
				return exitErr(1)
			}
			gates, err := latestClassifiedJobGateResults(teamDir, j)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "agent-team job gates: %v\n", err)
				return exitErr(1)
			}
			return renderJobGateResults(cmd.OutOrStdout(), gates, jsonOut)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", cwd, repoFlagHelp)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit gate results as JSON.")
	return cmd
}

func defaultJobGateActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor != "" {
		return actor
	}
	if instance := strings.TrimSpace(os.Getenv("AGENT_TEAM_INSTANCE")); instance != "" {
		return instance
	}
	return "cli"
}

func jobGateStatusMessage(record *job.GateRecord) string {
	if record == nil {
		return "gate updated"
	}
	return fmt.Sprintf("gate %s %s", strings.TrimSpace(record.Name), record.Status)
}

func jobGateEventData(record *job.GateRecord) map[string]string {
	data := map[string]string{}
	if record == nil {
		return data
	}
	data["gate"] = strings.TrimSpace(record.Name)
	data["status"] = string(record.Status)
	if signature := strings.TrimSpace(record.Signature); signature != "" {
		data["signature"] = signature
	}
	if logRef := strings.TrimSpace(record.LogRef); logRef != "" {
		data["log_ref"] = logRef
	}
	return data
}

func renderJobGateSetResult(w io.Writer, result jobGateResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(result)
	}
	fmt.Fprintf(w, "recorded gate %s for %s: %s", result.Name, result.JobID, result.Status)
	if result.Class != "" {
		fmt.Fprintf(w, " class=%s", result.Class)
	}
	if result.MatchedSignature != "" {
		fmt.Fprintf(w, " matched_signature=%s", result.MatchedSignature)
	}
	if result.MatchedPattern != "" {
		fmt.Fprintf(w, " matched_pattern=%q", result.MatchedPattern)
	}
	if result.Signature != "" {
		fmt.Fprintf(w, " signature=%q", result.Signature)
	}
	if result.LogRef != "" {
		fmt.Fprintf(w, " log_ref=%s", result.LogRef)
	}
	fmt.Fprintln(w)
	return nil
}

func renderJobGateResults(w io.Writer, gates []jobGateResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(w).Encode(gates)
	}
	if len(gates) == 0 {
		fmt.Fprintln(w, "(no gate results)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GATE\tSTATUS\tCLASS\tMATCHED_SIGNATURE\tMATCHED_PATTERN\tSIGNATURE\tLOG_REF\tACTOR\tUPDATED")
	for _, gate := range gates {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			gate.Name,
			gate.Status,
			emptyDash(gate.Class),
			emptyDash(gate.MatchedSignature),
			emptyDash(gate.MatchedPattern),
			emptyDash(gate.Signature),
			emptyDash(gate.LogRef),
			emptyDash(gate.Actor),
			gate.TS.Format(time.RFC3339),
		)
	}
	return tw.Flush()
}

func latestClassifiedJobGateResults(teamDir string, j *job.Job) ([]jobGateResult, error) {
	if j == nil {
		return nil, nil
	}
	records, err := job.ListGateRecords(teamDir, j.ID)
	if err != nil {
		return nil, err
	}
	return classifyJobGateRecords(teamDir, j, job.LatestGateRecords(records))
}

func classifyJobGateRecords(teamDir string, j *job.Job, records []job.GateRecord) ([]jobGateResult, error) {
	if len(records) == 0 {
		return nil, nil
	}
	matchers, err := jobGateSignatureMatchers(teamDir, j)
	if err != nil {
		return nil, err
	}
	out := make([]jobGateResult, 0, len(records))
	for _, record := range records {
		out = append(out, gateResultFromRecord(record, job.ClassifyGateRecord(matchers, record)))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func jobGateSignatureMatchers(teamDir string, j *job.Job) ([]job.GateSignatureMatcher, error) {
	if j == nil || strings.TrimSpace(j.Pipeline) == "" {
		return nil, nil
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, nil
	}
	pipeline := top.Pipelines[strings.TrimSpace(j.Pipeline)]
	if pipeline == nil || len(pipeline.InfraSignatures) == 0 {
		return nil, nil
	}
	matchers, err := job.CompileGateSignatureMatchers(pipeline.InfraSignatures)
	if err != nil {
		return nil, fmt.Errorf("pipeline %q %w", pipeline.Name, err)
	}
	return matchers, nil
}

func gateResultFromRecord(record job.GateRecord, classification job.GateClassification) jobGateResult {
	return jobGateResult{
		TS:               record.TS,
		JobID:            record.JobID,
		Name:             record.Name,
		Status:           record.Status,
		Class:            classification.Class,
		Signature:        record.Signature,
		MatchedSignature: classification.MatchedSignature,
		MatchedPattern:   classification.MatchedPattern,
		LogRef:           record.LogRef,
		Actor:            record.Actor,
	}
}

func failedJobGateResults(gates []jobGateResult) []jobGateResult {
	out := make([]jobGateResult, 0)
	for _, gate := range gates {
		if gate.Status == job.GateStatusFail {
			out = append(out, gate)
		}
	}
	return out
}

func jobGateFailuresByJob(teamDir string, jobs []*job.Job) (map[string][]jobGateResult, error) {
	out := make(map[string][]jobGateResult, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		gates, err := latestClassifiedJobGateResults(teamDir, j)
		if err != nil {
			return nil, err
		}
		failures := failedJobGateResults(gates)
		if len(failures) > 0 {
			out[j.ID] = failures
		}
	}
	return out, nil
}

func jobGateClassCounts(gates []jobGateResult) (infra, content int) {
	for _, gate := range gates {
		switch gate.Class {
		case jobGateClassInfra:
			infra++
		case jobGateClassContent:
			content++
		}
	}
	return infra, content
}

func jobGateTriageMessage(gates []jobGateResult) string {
	if len(gates) == 0 {
		return ""
	}
	parts := make([]string, 0, len(gates))
	for _, gate := range gates {
		label := strings.TrimSpace(gate.Class)
		if label == "" {
			label = string(gate.Status)
		}
		if match := jobGateMatchedSignatureText(gate); match != "" {
			label = label + "(" + match + ")"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", gate.Name, label))
	}
	sort.Strings(parts)
	return "failed gates: " + strings.Join(parts, ",")
}

func jobGateSummaryText(gates []jobGateResult) string {
	if len(gates) == 0 {
		return "-"
	}
	failed := 0
	for _, gate := range gates {
		if gate.Status == job.GateStatusFail {
			failed++
		}
	}
	infra, content := jobGateClassCounts(gates)
	parts := []string{fmt.Sprintf("total=%d", len(gates))}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", failed))
	}
	if infra > 0 {
		parts = append(parts, fmt.Sprintf("infra=%d", infra))
	}
	if content > 0 {
		parts = append(parts, fmt.Sprintf("content=%d", content))
	}
	if matches := jobGateMatchedSignatureSummaries(gates); len(matches) > 0 {
		parts = append(parts, "matches="+strings.Join(matches, "|"))
	}
	return strings.Join(parts, ",")
}

func jobGateMatchedSignatureSummaries(gates []jobGateResult) []string {
	var out []string
	for _, gate := range gates {
		match := jobGateMatchedSignatureText(gate)
		if match == "" {
			continue
		}
		out = append(out, gate.Name+":"+match)
	}
	sort.Strings(out)
	return out
}

func jobGateMatchedSignatureText(gate jobGateResult) string {
	name := strings.TrimSpace(gate.MatchedSignature)
	if name == "" {
		return ""
	}
	pattern := strings.TrimSpace(gate.MatchedPattern)
	if pattern == "" {
		return name
	}
	return name + "=" + pattern
}
