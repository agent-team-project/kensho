package jobwrite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/pmprovider"
	"github.com/jamesaud/agent-team/internal/usage"
)

type Options struct {
	EventType string
	Actor     string
	Message   string
	Data      map[string]string
}

func WriteWithAudit(teamDir string, j *job.Job, opts Options) error {
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
