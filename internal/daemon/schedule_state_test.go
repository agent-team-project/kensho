package daemon

import (
	"errors"
	"io/fs"
	"testing"
	"time"
)

func TestScheduleStateReadWriteListAndRemove(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	state := &ScheduleState{
		Name:        "nightly/build",
		LastSeenAt:  now,
		LastFiredAt: now.Add(-time.Hour),
	}
	if err := WriteScheduleState(root, state); err != nil {
		t.Fatalf("WriteScheduleState: %v", err)
	}
	got, err := ReadScheduleState(root, "nightly/build")
	if err != nil {
		t.Fatalf("ReadScheduleState: %v", err)
	}
	if got.Name != state.Name || !got.LastSeenAt.Equal(state.LastSeenAt) || !got.LastFiredAt.Equal(state.LastFiredAt) {
		t.Fatalf("state = %+v, want %+v", got, state)
	}
	all, err := ListScheduleStates(root)
	if err != nil {
		t.Fatalf("ListScheduleStates: %v", err)
	}
	if len(all) != 1 || all[0].Name != "nightly/build" {
		t.Fatalf("states = %+v", all)
	}
	if err := RemoveScheduleState(root, "nightly/build"); err != nil {
		t.Fatalf("RemoveScheduleState: %v", err)
	}
	if _, err := ReadScheduleState(root, "nightly/build"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadScheduleState after remove err = %v, want not exist", err)
	}
}
