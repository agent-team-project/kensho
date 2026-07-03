package cli

import (
	"context"
	"strings"

	"github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/linearwriteback"
)

func writeLinearDispatchInProgress(teamDir string, j *job.Job) {
	if !linearJobDispatchStarted(j) {
		return
	}
	_ = linearwriteback.DefaultClient().WriteBack(context.Background(), teamDir, linearwriteback.Request{
		Action: linearwriteback.ActionDispatchInProgress,
		Job:    j,
		Actor:  "cli",
	})
}

func writeLinearStepDispatchInProgress(teamDir string, j *job.Job, stepID string) {
	if !linearJobDispatchStarted(j) || !linearFirstJobStep(j, stepID) {
		return
	}
	_ = linearwriteback.DefaultClient().WriteBack(context.Background(), teamDir, linearwriteback.Request{
		Action: linearwriteback.ActionDispatchInProgress,
		Job:    j,
		Actor:  "cli",
	})
}

func writeLinearBounceBack(teamDir string, j *job.Job, stepID, findings string) {
	_ = linearwriteback.BounceBack(context.Background(), teamDir, j, stepID, findings, "cli")
}

func linearJobDispatchStarted(j *job.Job) bool {
	return j != nil && (j.Status == job.StatusRunning || j.Status == job.StatusQueued)
}

func linearFirstJobStep(j *job.Job, stepID string) bool {
	stepID = strings.TrimSpace(stepID)
	if j == nil || stepID == "" || len(j.Steps) == 0 {
		return false
	}
	return j.Steps[0].ID == stepID
}
