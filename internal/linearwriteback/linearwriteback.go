// Package linearwriteback preserves the pre-provider Linear write-back API.
//
// New code should prefer internal/pmprovider. The aliases here keep existing
// callers and tests source-compatible while Linear moves behind the provider
// seam.
package linearwriteback

import (
	"context"

	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/pmprovider"
)

type Action = pmprovider.Action

const (
	ActionDispatchInProgress = pmprovider.ActionDispatchInProgress
	ActionBounceBack         = pmprovider.ActionBounceBack
	ActionFailureAttention   = pmprovider.ActionFailureAttention
)

type Request = pmprovider.Request
type Result = pmprovider.Result
type Client = pmprovider.Client

func DefaultClient() *Client {
	return pmprovider.DefaultClient()
}

func DispatchInProgress(ctx context.Context, teamDir string, j *job.Job) Result {
	return pmprovider.DefaultClient().WriteBack(ctx, teamDir, pmprovider.Request{Action: pmprovider.ActionDispatchInProgress, Job: j, Actor: "daemon"})
}

func BounceBack(ctx context.Context, teamDir string, j *job.Job, stepID, findings, actor string) Result {
	return pmprovider.DefaultClient().WriteBack(ctx, teamDir, pmprovider.Request{
		Action:   pmprovider.ActionBounceBack,
		Job:      j,
		StepID:   stepID,
		Findings: findings,
		Actor:    actor,
	})
}

func FailureAttention(ctx context.Context, teamDir string, j *job.Job, message, actor string) Result {
	return pmprovider.DefaultClient().WriteBack(ctx, teamDir, pmprovider.Request{
		Action:  pmprovider.ActionFailureAttention,
		Job:     j,
		Message: message,
		Actor:   actor,
	})
}
