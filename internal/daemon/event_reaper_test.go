package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	eventReaperWaitTimeout = 15 * time.Second
	eventReaperTailBytes   = 4096
	eventShortFakeRuntime  = 100 * time.Millisecond
)

func waitForEventReaper(t testing.TB, m *InstanceManager, instance string) error {
	t.Helper()
	if err := m.WaitForReaper(instance, eventReaperWaitTimeout); err != nil {
		return fmt.Errorf("%w\n%s", err, eventReaperDebugContext(t, m, instance))
	}
	return nil
}

func eventReaperDebugContext(t testing.TB, m *InstanceManager, instance string) string {
	t.Helper()
	var b strings.Builder
	if m == nil {
		return "reaper context: nil instance manager"
	}
	root := m.daemonRoot
	fmt.Fprintf(&b, "reaper context for %q:", instance)

	logPath := childLogPath(root, instance)
	meta, err := ReadMetadata(root, instance)
	if err != nil {
		fmt.Fprintf(&b, "\nmetadata: %v", err)
	} else {
		if meta.LogPath != "" {
			logPath = meta.LogPath
		}
		body, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			fmt.Fprintf(&b, "\nmetadata: marshal: %v", err)
		} else {
			fmt.Fprintf(&b, "\nmetadata:\n%s", body)
		}
	}

	appendFileTail(&b, "child log", logPath)
	appendLifecycleContext(&b, root, instance)
	return b.String()
}

func appendFileTail(b *strings.Builder, label, path string) {
	body, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(b, "\n%s %s: %v", label, path, err)
		return
	}
	truncated := ""
	if len(body) > eventReaperTailBytes {
		body = body[len(body)-eventReaperTailBytes:]
		truncated = " tail"
	}
	text := strings.TrimRight(string(body), "\n")
	if text == "" {
		text = "<empty>"
	}
	fmt.Fprintf(b, "\n%s%s %s:\n%s", label, truncated, path, text)
}

func appendLifecycleContext(b *strings.Builder, root, instance string) {
	events, err := ListLifecycleEvents(root)
	if err != nil {
		fmt.Fprintf(b, "\nlifecycle events: %v", err)
		return
	}
	matched := 0
	for _, ev := range events {
		if ev == nil || ev.Instance != instance {
			continue
		}
		body, err := json.Marshal(ev)
		if err != nil {
			fmt.Fprintf(b, "\nlifecycle event: marshal: %v", err)
			continue
		}
		if matched == 0 {
			fmt.Fprint(b, "\nlifecycle events:")
		}
		matched++
		fmt.Fprintf(b, "\n%s", body)
	}
	if matched == 0 {
		fmt.Fprint(b, "\nlifecycle events: <none>")
	}
}
