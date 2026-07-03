package daemon

import (
	"context"
	"strings"

	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/linearwriteback"
)

func (r *EventResolver) writeLinearPipelineDispatch(j *jobstore.Job, stepID string) {
	if !linearDispatchStarted(j) || !jobFirstStep(j, stepID) {
		return
	}
	_ = linearwriteback.DispatchInProgress(context.Background(), r.teamDir, j)
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
