package job

import "strings"

// Pipeline-step readiness logic. This is the single source of truth shared by
// the CLI (`agent-team pipeline tick`/`advance`, `job approve`) and the daemon's
// auto-advance path, so both decide "what runs next" identically.

// StepSatisfiesDependency reports whether a step counts as satisfied for the
// purpose of a dependent step's `after` list: it completed, or it failed but was
// declared optional.
func StepSatisfiesDependency(step *Step) bool {
	if step == nil {
		return false
	}
	return step.Status == StatusDone || (step.Optional && step.Status == StatusFailed)
}

// StepManualGatePending reports whether a step is blocked on a manual approval
// gate (released by `agent-team job approve`).
func StepManualGatePending(step *Step) bool {
	return step != nil && step.Status == StatusBlocked && step.Gate == StepGateManual
}

// StepPRGatePending reports whether a step is gated on PR metadata the job does
// not yet carry (released when j.PR is populated).
func StepPRGatePending(j *Job, step *Step) bool {
	return step != nil && step.Gate == StepGatePR && j != nil && strings.TrimSpace(j.PR) == ""
}

// StepGatePending reports whether any gate is currently holding the step back.
func StepGatePending(j *Job, step *Step) bool {
	return StepManualGatePending(step) || StepPRGatePending(j, step)
}

// NextReadyStep returns the next pipeline step that can run: its `after`
// dependencies are satisfied, no gate is pending, and it is not already terminal
// or in-flight. It returns nil when nothing is ready — deps incomplete, a gate is
// pending, or every step is done.
func NextReadyStep(j *Job) *Step {
	if j == nil || j.Held {
		return nil
	}
	done := map[string]bool{}
	for i := range j.Steps {
		if StepSatisfiesDependency(&j.Steps[i]) {
			done[j.Steps[i].ID] = true
		}
	}
	// First pass: fresh (not-yet-queued) steps whose deps are satisfied.
	for i := range j.Steps {
		step := &j.Steps[i]
		if StepGatePending(j, step) {
			continue
		}
		if step.Status == StatusDone || step.Status == StatusFailed || step.Status == StatusRunning || step.Status == StatusQueued {
			continue
		}
		if dependenciesMet(step, done) {
			return step
		}
	}
	// Second pass: already-queued steps that are now unblocked.
	for i := range j.Steps {
		step := &j.Steps[i]
		if step.Status != StatusQueued {
			continue
		}
		if StepGatePending(j, step) {
			continue
		}
		if dependenciesMet(step, done) {
			return step
		}
	}
	return nil
}

func dependenciesMet(step *Step, done map[string]bool) bool {
	for _, dep := range step.After {
		if !done[dep] {
			return false
		}
	}
	return true
}
