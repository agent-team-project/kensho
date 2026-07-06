package job

import "strings"

// ImplementationAgentForJob returns the stable agent attribution for outcome
// reporting. Job.Target is mutable during pipeline execution; this value is not.
func ImplementationAgentForJob(j *Job) string {
	if j == nil {
		return ""
	}
	if agent := strings.TrimSpace(j.ImplementationAgent); agent != "" {
		return agent
	}
	if agent := ImplementationAgentFromSteps(j.Steps); agent != "" {
		return agent
	}
	return strings.TrimSpace(j.Target)
}

// SetImplementationAgentFromSteps records the implementation owner captured by
// a pipeline snapshot. Prefer a step explicitly named "implement", then the
// first step for nonstandard pipelines.
func SetImplementationAgentFromSteps(j *Job) {
	if j == nil {
		return
	}
	if agent := ImplementationAgentFromSteps(j.Steps); agent != "" {
		j.ImplementationAgent = agent
	}
}

func ImplementationAgentFromSteps(steps []Step) string {
	for _, step := range steps {
		if strings.EqualFold(strings.TrimSpace(step.ID), "implement") {
			return strings.TrimSpace(step.Target)
		}
	}
	if len(steps) == 0 {
		return ""
	}
	return strings.TrimSpace(steps[0].Target)
}
