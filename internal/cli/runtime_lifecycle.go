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

func lifecycleStaleUnsupportedResumeDetail(meta *daemon.Metadata) string {
	return "recorded running pid is not live; " + lifecycleUnsupportedResumeDetail(meta)
}

func lifecycleTargetUnsupportedResumeResult(target lifecycleTarget) lifecycleActionResult {
	result := lifecycleActionResult{
		Action:   lifecycleActionUnsupported,
		Instance: target.name,
		Agent:    target.agent,
		Status:   lifecycleTargetStatusKey(target),
		Detail:   lifecycleUnsupportedResumeDetail(target.meta),
	}
	if target.meta != nil {
		result.PID = target.meta.PID
	}
	return result
}
