package cli

import (
	"fmt"
	"strings"
)

const queueRecoveryHintLimit = 10

func queueRetryAllRecoveryAction(base string, dryRun bool, filters ...string) string {
	action := strings.TrimSpace(base)
	if action == "" {
		return ""
	}
	action += " --all"
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter != "" {
			action += " " + filter
		}
	}
	action = appendQueueBatchRecoveryDefaults(action)
	if dryRun {
		action = appendDryRunFlag(action)
	}
	return action
}

func globalQueueRetryAllRecoveryAction(dryRun bool) string {
	return queueRetryAllRecoveryAction("agent-team queue retry", dryRun)
}

func jobQueueRetryAllRecoveryAction(jobID string, dryRun bool) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	return queueRetryAllRecoveryAction(fmt.Sprintf("agent-team job queue retry %s", jobID), dryRun)
}

func pipelineQueueRetryAllRecoveryAction(pipelineName string, dryRun bool) string {
	pipelineName = strings.TrimSpace(pipelineName)
	if pipelineName == "" {
		return ""
	}
	return queueRetryAllRecoveryAction(fmt.Sprintf("agent-team pipeline queue retry %s", pipelineName), dryRun)
}

func teamQueueRetryAllRecoveryAction(teamName string, dryRun bool, filters ...string) string {
	teamName = strings.TrimSpace(teamName)
	if teamName == "" {
		return ""
	}
	return queueRetryAllRecoveryAction(fmt.Sprintf("agent-team team queue retry %s", teamName), dryRun, filters...)
}

func teamJobQueueRetryAllRecoveryAction(teamName, jobID string, dryRun bool) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	return teamQueueRetryAllRecoveryAction(teamName, dryRun, fmt.Sprintf("--job %s", jobID))
}

func queueRetryRecoveryDryRunAction(action string) string {
	action = appendQueueBatchRecoveryDefaults(action)
	return appendDryRunFlag(action)
}

func appendQueueBatchRecoveryDefaults(action string) string {
	action = strings.TrimSpace(action)
	if action == "" || !actionHasFlag(action, "--all") {
		return action
	}
	hadDryRun := actionHasFlag(action, "--dry-run")
	if hadDryRun {
		action = removeActionFlag(action, "--dry-run")
	}
	if !actionHasFlag(action, "--sort") {
		action += " --sort attempts"
	}
	if !actionHasFlag(action, "--limit") {
		action += fmt.Sprintf(" --limit %d", queueRecoveryHintLimit)
	}
	if hadDryRun {
		action = appendDryRunFlag(action)
	}
	return action
}

func actionHasFlag(action, flag string) bool {
	for _, field := range strings.Fields(action) {
		if field == flag || strings.HasPrefix(field, flag+"=") {
			return true
		}
	}
	return false
}

func removeActionFlag(action, flag string) string {
	fields := strings.Fields(action)
	out := fields[:0]
	for _, field := range fields {
		if field == flag || strings.HasPrefix(field, flag+"=") {
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " ")
}

func isQueueRetryAction(action string) bool {
	action = strings.TrimSpace(action)
	return action == "agent-team queue retry" ||
		strings.HasPrefix(action, "agent-team queue retry ") ||
		strings.HasPrefix(action, "agent-team job queue retry ") ||
		strings.HasPrefix(action, "agent-team pipeline queue retry ") ||
		strings.HasPrefix(action, "agent-team team queue retry ")
}
