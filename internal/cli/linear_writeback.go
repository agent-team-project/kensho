package cli

import (
	"context"
	"strings"

	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/pmprovider"
)

func writeLinearDispatchInProgress(teamDir string, j *job.Job) {
	if !linearJobDispatchStarted(j) {
		return
	}
	_ = pmprovider.WriteBack(context.Background(), teamDir, pmprovider.Request{
		Action: pmprovider.ActionDispatchInProgress,
		Job:    j,
		Actor:  "cli",
	})
}

func writeLinearStepDispatchInProgress(teamDir string, j *job.Job, stepID string) {
	if !linearJobDispatchStarted(j) || !linearFirstJobStep(j, stepID) {
		return
	}
	_ = pmprovider.WriteBack(context.Background(), teamDir, pmprovider.Request{
		Action: pmprovider.ActionDispatchInProgress,
		Job:    j,
		Actor:  "cli",
	})
}

func writeLinearBounceBack(teamDir string, j *job.Job, stepID, findings string) {
	_ = pmprovider.WriteBack(context.Background(), teamDir, pmprovider.Request{
		Action:   pmprovider.ActionBounceBack,
		Job:      j,
		StepID:   stepID,
		Findings: findings,
		Actor:    "cli",
	})
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
