package jobwrite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/origin"
	"github.com/jamesaud/agent-team/internal/pmprovider"
	"github.com/jamesaud/agent-team/internal/topology"
	"github.com/jamesaud/agent-team/internal/usage"
)

type Options struct {
	EventType string
	Actor     string
	Message   string
	Data      map[string]string
}

func WriteWithAudit(teamDir string, j *job.Job, opts Options) error {
	applyJobOrigin(teamDir, j, opts)
	writeAttention := shouldWriteFailureAttention(teamDir, j)
	if writeAttention {
		j.LinearAttentionWritten = true
	} else if j != nil && j.Status != job.StatusFailed {
		j.LinearAttentionWritten = false
	}
	if err := job.Write(teamDir, j); err != nil {
		if writeAttention {
			j.LinearAttentionWritten = false
		}
		return err
	}
	if err := job.AppendSnapshotEvent(teamDir, j, opts.EventType, opts.Actor, opts.Message, opts.Data); err != nil {
		return err
	}
	if writeAttention {
		_ = pmprovider.WriteBack(context.Background(), teamDir, pmprovider.Request{
			Action:  pmprovider.ActionFailureAttention,
			Job:     j,
			Message: attentionMessage(j, opts.Message),
			Actor:   opts.Actor,
		})
	}
	return nil
}

func ReconcilePR(teamDir string, input job.ReconcileInput, now time.Time) (*job.ReconcileResult, error) {
	result, err := job.PreviewReconcilePR(teamDir, input, now)
	if err != nil {
		return nil, err
	}
	actor := strings.TrimSpace(input.Source)
	if actor == "" {
		actor = "reconcile"
	}
	if err := WriteWithAudit(teamDir, result.Job, Options{
		Actor: actor,
		Data:  reconcileEventData(input, result.MatchedBy),
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func RecordUsage(teamDir, rawID string, record usage.Record, opts Options) (*job.Job, bool, error) {
	j, err := job.Read(teamDir, rawID)
	if err != nil {
		return nil, false, err
	}
	record.Origin = origin.Merge(record.Origin, j.Origin)
	merged, changed := usage.MergeRecord(j.Usage, record)
	if !changed {
		return j, false, nil
	}
	j.Usage = merged
	now := record.CapturedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	j.UpdatedAt = now.UTC()
	if opts.EventType == "" {
		opts.EventType = "usage_captured"
	}
	if opts.Actor == "" {
		opts.Actor = "daemon"
	}
	if opts.Message == "" {
		opts.Message = usageCaptureMessage(record)
	}
	if opts.Data == nil {
		opts.Data = usageCaptureData(record)
	}
	if err := WriteWithAudit(teamDir, j, opts); err != nil {
		return nil, false, err
	}
	return j, true, nil
}

func shouldWriteFailureAttention(teamDir string, j *job.Job) bool {
	if j == nil || j.Status != job.StatusFailed || j.LinearAttentionWritten {
		return false
	}
	prior, err := job.Read(teamDir, j.ID)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	if prior.LinearAttentionWritten || prior.Status == job.StatusFailed {
		return false
	}
	return true
}

func usageCaptureMessage(record usage.Record) string {
	instance := strings.TrimSpace(record.Instance)
	if instance == "" {
		instance = "instance"
	}
	return fmt.Sprintf("captured usage for %s", instance)
}

func usageCaptureData(record usage.Record) map[string]string {
	data := map[string]string{
		"tokens_available": fmt.Sprint(record.TokensAvailable),
	}
	if record.Instance != "" {
		data["instance"] = record.Instance
	}
	if record.Agent != "" {
		data["agent"] = record.Agent
	}
	if record.Runtime != "" {
		data["runtime"] = record.Runtime
	}
	if record.Turns > 0 {
		data["turns"] = fmt.Sprint(record.Turns)
	}
	if record.DurationMS > 0 {
		data["duration_ms"] = fmt.Sprint(record.DurationMS)
	}
	if record.TokensAvailable {
		data["input_tokens"] = fmt.Sprint(record.InputTokens)
		data["cached_input_tokens"] = fmt.Sprint(record.CachedInputTokens)
		data["output_tokens"] = fmt.Sprint(record.OutputTokens)
		data["reasoning_output_tokens"] = fmt.Sprint(record.ReasoningOutputTokens)
	}
	return data
}

func attentionMessage(j *job.Job, message string) string {
	if message = strings.TrimSpace(message); message != "" {
		return message
	}
	if j == nil {
		return ""
	}
	return strings.TrimSpace(j.LastStatus)
}

func applyJobOrigin(teamDir string, j *job.Job, opts Options) {
	if j == nil {
		return
	}
	fallback := origin.Envelope{
		Project:  projectID(teamDir),
		Team:     jobTeam(teamDir, j),
		Instance: j.Instance,
		Agent:    j.Target,
		Job:      j.ID,
		Trigger:  originTrigger(opts),
		Build:    buildinfo.Current("").Display(),
	}
	j.Origin = origin.Merge(j.Origin, fallback)
}

func projectID(teamDir string) string {
	id, _ := origin.ProjectID(teamDir)
	return id
}

func originTrigger(opts Options) string {
	for _, key := range []string{"trigger", "event", "source"} {
		if opts.Data != nil {
			if value := strings.TrimSpace(opts.Data[key]); value != "" {
				return value
			}
		}
	}
	return strings.TrimSpace(opts.EventType)
}

func jobTeam(teamDir string, j *job.Job) string {
	if j == nil {
		return ""
	}
	if team := strings.TrimSpace(j.Origin.Team); team != "" {
		return team
	}
	top, err := topology.LoadFromTeamDir(teamDir)
	if err != nil || top == nil {
		return ""
	}
	for _, team := range top.SortedTeams() {
		if j.Pipeline != "" && stringInList(team.Pipelines, j.Pipeline) {
			return team.Name
		}
		if j.Instance != "" && instanceMatchesTeam(j.Instance, team.Instances) {
			return team.Name
		}
		if j.Target != "" && stringInList(team.Instances, j.Target) {
			return team.Name
		}
	}
	return ""
}

func instanceMatchesTeam(instance string, names []string) bool {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return false
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if instance == name || strings.HasPrefix(instance, name+"-") {
			return true
		}
	}
	return false
}

func stringInList(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func reconcileEventData(input job.ReconcileInput, matchedBy string) map[string]string {
	data := map[string]string{"matched_by": matchedBy}
	if input.PR != "" {
		data["pr"] = input.PR
	}
	if input.PRURL != "" {
		data["pr_url"] = input.PRURL
	}
	if input.Branch != "" {
		data["branch"] = input.Branch
	}
	if input.Source != "" {
		data["source"] = input.Source
	}
	return data
}
