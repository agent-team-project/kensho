package cli

import (
	"fmt"
	"strings"

	"github.com/jamesaud/agent-team/internal/daemon"
	"github.com/jamesaud/agent-team/internal/runtimebin"
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
	return lifecycleMetadataRuntimeKind(meta) == runtimebin.KindClaude
}

func lifecycleUnsupportedResumeDetail(meta *daemon.Metadata) string {
	return fmt.Sprintf("runtime %q does not support managed resume; create a new run instead", lifecycleMetadataRuntimeKind(meta))
}

func lifecycleUnsupportedResumeDetailForInstance(meta *daemon.Metadata, instance string) string {
	detail := lifecycleUnsupportedResumeDetail(meta)
	hints := lifecycleUnsupportedResumeActionHints(meta, instance)
	if len(hints) == 0 {
		return detail
	}
	return detail + "; " + strings.Join(hints, "; ")
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
