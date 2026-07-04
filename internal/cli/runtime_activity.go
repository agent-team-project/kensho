package cli

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/daemon"
)

type runtimeActivityInfo struct {
	LastActivityAt time.Time
	Activity       string
}

func runtimeActivityForInstance(teamDir, instance string, meta *daemon.Metadata, now time.Time) runtimeActivityInfo {
	instance = strings.TrimSpace(instance)
	if instance == "" && meta != nil {
		instance = meta.Instance
	}
	if instance == "" {
		return runtimeActivityInfo{}
	}
	var latest time.Time
	for _, path := range []string{
		activityLogPath(teamDir, instance, meta),
		filepath.Join(teamDir, "state", instance, "status.toml"),
	} {
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return runtimeActivityFromTime(latest, now)
}

func activityLogPath(teamDir, instance string, meta *daemon.Metadata) string {
	if meta != nil {
		return logPathForMetadata(teamDir, meta)
	}
	if strings.TrimSpace(instance) == "" {
		return ""
	}
	return filepath.Join(daemon.DaemonRoot(teamDir), instance, "child.log")
}

func runtimeActivityFromTime(last time.Time, now time.Time) runtimeActivityInfo {
	if last.IsZero() {
		return runtimeActivityInfo{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	last = last.UTC()
	age := now.Sub(last)
	if age < 0 {
		age = 0
	}
	activity := "progressing"
	if age >= time.Minute {
		activity = "quiet " + formatAge(age)
	}
	return runtimeActivityInfo{
		LastActivityAt: last,
		Activity:       activity,
	}
}

func formatOptionalRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
