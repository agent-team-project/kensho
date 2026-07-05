package cli

import (
	"fmt"
	"strings"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
)

const lifecycleActionUnsupported = "unsupported"

func lifecycleMetadataRuntimeKind(meta *daemon.Metadata) runtimebin.Kind {
	if meta == nil || strings.TrimSpace(meta.Runtime) == "" {
		return runtimebin.KindClaude
	}
	kind, err := runtimebin.ParseKind(meta.Runtime)
	if err != nil {
		return runtimebin.KindClaude
	}
	return kind
}

func lifecycleMetadataSupportsManagedResume(meta *daemon.Metadata) bool {
	return runtimeKindSupportsManagedResume(lifecycleMetadataRuntimeKind(meta))
}

func runtimeKindSupportsManagedResume(kind runtimebin.Kind) bool {
	return kind == runtimebin.KindClaude || kind == runtimebin.KindCodex
}

func lifecycleMetadataCanManagedResume(meta *daemon.Metadata) bool {
	if !lifecycleMetadataSupportsManagedResume(meta) {
		return false
	}
	if lifecycleMetadataRuntimeKind(meta) == runtimebin.KindCodex {
		return strings.TrimSpace(meta.SessionID) != ""
	}
	return true
}

func lifecycleUnsupportedResumeDetail(meta *daemon.Metadata) string {
	return fmt.Sprintf("runtime %q does not support managed resume; create a new run instead", lifecycleMetadataRuntimeKind(meta))
}

func lifecycleUnsupportedResumeDetailForInstance(meta *daemon.Metadata, instance string) string {
	detail := lifecycleManagedResumeUnavailableDetail(meta)
	hints := lifecycleUnsupportedResumeActionHints(meta, instance)
	if len(hints) == 0 {
		return detail
	}
	return detail + "; " + strings.Join(hints, "; ")
}

func lifecycleManagedResumeUnavailableDetail(meta *daemon.Metadata) string {
	if !lifecycleMetadataSupportsManagedResume(meta) {
		return lifecycleUnsupportedResumeDetail(meta)
	}
	return fmt.Sprintf("runtime %q supports managed resume but no session id is recorded; follow logs or create a new run", lifecycleMetadataRuntimeKind(meta))
}

func lifecycleUnsupportedResumeActionHints(meta *daemon.Metadata, instance string) []string {
	if meta != nil && strings.TrimSpace(instance) == "" {
		instance = meta.Instance
	}
	instance = strings.TrimSpace(instance)
	if lifecycleMetadataRuntimeKind(meta) != runtimebin.KindCodex {
		return nil
	}
	hints := []string{}
	if instance != "" {
		if meta != nil && strings.TrimSpace(meta.Job) != "" {
			hints = append(hints, fmt.Sprintf("plan: agent-team job resume-plan %s", strings.TrimSpace(meta.Job)))
		} else {
			hints = append(hints, fmt.Sprintf("plan: agent-team resume-plan %s", instance))
		}
		hints = append(hints,
			fmt.Sprintf("logs: agent-team logs %s --follow", instance),
			fmt.Sprintf("last message: agent-team logs %s --last-message", instance),
		)
	}
	if meta != nil && strings.TrimSpace(meta.SessionID) != "" {
		bin := strings.TrimSpace(meta.RuntimeBinary)
		if bin == "" {
			bin = runtimebin.DefaultBinaryForKind(runtimebin.KindCodex)
		}
		hints = append(hints, fmt.Sprintf("unmanaged resume: %s resume %s", bin, strings.TrimSpace(meta.SessionID)))
	}
	return hints
}

func lifecycleStaleUnsupportedResumeDetailForInstance(meta *daemon.Metadata, instance string) string {
	return "recorded running pid is not live; " + lifecycleUnsupportedResumeDetailForInstance(meta, instance)
}

func lifecycleTargetUnsupportedResumeResult(target lifecycleTarget) lifecycleActionResult {
	result := lifecycleActionResult{
		Action:   lifecycleActionUnsupported,
		Instance: target.name,
		Agent:    target.agent,
		Status:   lifecycleTargetStatusKey(target),
		Detail:   lifecycleUnsupportedResumeDetailForInstance(target.meta, target.name),
	}
	if target.meta != nil {
		result.PID = target.meta.PID
	}
	return result
}
