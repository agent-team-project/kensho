package daemon

import (
	"context"
	"strings"

	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/linearwriteback"
)

func (r *EventResolver) writeLinearDispatchInProgress(j *jobstore.Job, stepID string) {
	if !linearDispatchStarted(j) {
		return
	}
	if stepID != "" && !jobFirstStep(j, stepID) {
		return
	}
	_ = linearwriteback.DispatchInProgress(context.Background(), r.teamDir, j)
}

// linearDispatchStepFromPayload extracts the pipeline step for the dispatch
// write-back. A dispatch that attaches a job without a pipeline_step (direct
// `agent.dispatch` with job_id/ticket) still attempts the write-back — the
// non-first-step suppression is handled by jobFirstStep when a step is named.
func linearDispatchStepFromPayload(payload map[string]any) (string, bool) {
	return payloadString(payload, "pipeline_step"), true
}

func linearDispatchStarted(j *jobstore.Job) bool {
	return j != nil && (j.Status == jobstore.StatusRunning || j.Status == jobstore.StatusQueued)
}

func jobFirstStep(j *jobstore.Job, stepID string) bool {
	stepID = strings.TrimSpace(stepID)
	if j == nil || stepID == "" || len(j.Steps) == 0 {
		return false
	}
	return j.Steps[0].ID == stepID
}
