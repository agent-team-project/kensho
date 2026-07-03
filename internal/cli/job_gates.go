package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/spf13/cobra"
)

const (
	jobGateClassInfra   = "infra"
	jobGateClassContent = "content"
)

type jobGateResult struct {
	TS               time.Time      `json:"ts"`
	JobID            string         `json:"job_id"`
	Name             string         `json:"name"`
	Status           job.GateStatus `json:"status"`
	Class            string         `json:"class,omitempty"`
	Signature        string         `json:"signature,omitempty"`
	MatchedSignature string         `json:"matched_signature,omitempty"`
	LogRef           string         `json:"log_ref,omitempty"`
	Actor            string         `json:"actor,omitempty"`
}

type jobGateSignatureMatcher struct {
	Name string
	Re   *regexp.Regexp
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
			result := gateResultFromRecord(*record, "", "")
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
	fmt.Fprintln(tw, "GATE\tSTATUS\tCLASS\tSIGNATURE\tLOG_REF\tACTOR\tUPDATED")
	for _, gate := range gates {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			gate.Name,
			gate.Status,
			emptyDash(gate.Class),
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
		class, matched := classifyJobGateRecord(matchers, record)
		out = append(out, gateResultFromRecord(record, class, matched))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func jobGateSignatureMatchers(teamDir string, j *job.Job) ([]jobGateSignatureMatcher, error) {
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
	names := make([]string, 0, len(pipeline.InfraSignatures))
	for name := range pipeline.InfraSignatures {
		names = append(names, name)
	}
	sort.Strings(names)
	matchers := make([]jobGateSignatureMatcher, 0, len(names))
	for _, name := range names {
		re, err := regexp.Compile(pipeline.InfraSignatures[name])
		if err != nil {
			return nil, fmt.Errorf("pipeline %q infra_signatures.%s: invalid regex: %w", pipeline.Name, name, err)
		}
		matchers = append(matchers, jobGateSignatureMatcher{Name: name, Re: re})
	}
	return matchers, nil
}

func classifyJobGateRecord(matchers []jobGateSignatureMatcher, record job.GateRecord) (class, matched string) {
	if record.Status != job.GateStatusFail {
		return "", ""
	}
	signature := strings.TrimSpace(record.Signature)
	if signature == "" {
		return jobGateClassContent, ""
	}
	for _, matcher := range matchers {
		if matcher.Re.MatchString(signature) {
			return jobGateClassInfra, matcher.Name
		}
	}
	return jobGateClassContent, ""
}

func gateResultFromRecord(record job.GateRecord, class, matched string) jobGateResult {
	return jobGateResult{
		TS:               record.TS,
		JobID:            record.JobID,
		Name:             record.Name,
		Status:           record.Status,
		Class:            class,
		Signature:        record.Signature,
		MatchedSignature: matched,
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
	return strings.Join(parts, ",")
}
