package daemon

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ScheduleState is the persisted scheduler clock for one declared schedule.
type ScheduleState struct {
	Name        string    `json:"name"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	LastFiredAt time.Time `json:"last_fired_at,omitempty"`
}

func ScheduleStateDir(daemonRoot string) string {
	return filepath.Join(daemonRoot, "schedules")
}

func ScheduleStatePath(daemonRoot, name string) string {
	return filepath.Join(ScheduleStateDir(daemonRoot), url.PathEscape(name)+".json")
}

func WriteScheduleState(daemonRoot string, state *ScheduleState) error {
	if err := validateScheduleState(state); err != nil {
		return err
	}
	dir := ScheduleStateDir(daemonRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("schedule state: mkdir: %w", err)
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("schedule state: marshal: %w", err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, url.PathEscape(state.Name)+"-*.json.tmp")
	if err != nil {
		return fmt.Errorf("schedule state: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("schedule state: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("schedule state: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("schedule state: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), ScheduleStatePath(daemonRoot, state.Name)); err != nil {
		return fmt.Errorf("schedule state: rename: %w", err)
	}
	return nil
}

func ReadScheduleState(daemonRoot, name string) (*ScheduleState, error) {
	body, err := os.ReadFile(ScheduleStatePath(daemonRoot, name))
	if err != nil {
		return nil, err
	}
	var state ScheduleState
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("schedule state: parse %s: %w", name, err)
	}
	if state.Name == "" {
		state.Name = name
	}
	if err := validateScheduleState(&state); err != nil {
		return nil, fmt.Errorf("schedule state: %s: %w", name, err)
	}
	return &state, nil
}

func ListScheduleStates(daemonRoot string) ([]*ScheduleState, error) {
	dir := ScheduleStateDir(daemonRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*ScheduleState, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name, err := url.PathUnescape(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, fmt.Errorf("schedule state: unescape %s: %w", entry.Name(), err)
		}
		state, err := ReadScheduleState(daemonRoot, name)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func RemoveScheduleState(daemonRoot, name string) error {
	err := os.Remove(ScheduleStatePath(daemonRoot, name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validateScheduleState(state *ScheduleState) error {
	if state == nil {
		return fmt.Errorf("nil state")
	}
	if strings.TrimSpace(state.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if state.LastSeenAt.IsZero() {
		return fmt.Errorf("last_seen_at is required")
	}
	return nil
}
