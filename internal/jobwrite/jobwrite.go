package jobwrite

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/linearwriteback"
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
		_ = linearwriteback.FailureAttention(context.Background(), teamDir, j, attentionMessage(j, opts.Message), opts.Actor)
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
