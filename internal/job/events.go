package job

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/buildinfo"
	"github.com/jamesaud/agent-team/internal/origin"
)

// Event is one durable audit record for a job.
type Event struct {
	TS       time.Time         `json:"ts"`
	JobID    string            `json:"job_id"`
	Type     string            `json:"type"`
	Status   Status            `json:"status,omitempty"`
	Instance string            `json:"instance,omitempty"`
	Message  string            `json:"message,omitempty"`
	Actor    string            `json:"actor,omitempty"`
	Origin   origin.Envelope   `json:"origin,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
}

// EventPath returns the JSONL event log path for a job id.
func EventPath(teamDir, rawID string) string {
	id := IDFromInput(rawID)
	return filepath.Join(Directory(teamDir), id+".events.jsonl")
}

// AppendEvent appends one JSONL audit event for a job.
func AppendEvent(teamDir string, ev *Event) error {
	if ev == nil {
		return errors.New("job event is nil")
	}
	ev.JobID = IDFromInput(ev.JobID)
	if ev.JobID == "" {
		return errors.New("job event job_id is required")
	}
	ev.Type = strings.TrimSpace(ev.Type)
	if ev.Type == "" {
		return errors.New("job event type is required")
	}
	if ev.Status != "" && !ValidStatus(ev.Status) {
		return fmt.Errorf("job event status %q is invalid", ev.Status)
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	} else {
		ev.TS = ev.TS.UTC()
	}
	ev.Instance = strings.TrimSpace(ev.Instance)
	ev.Message = strings.TrimSpace(ev.Message)
	ev.Actor = strings.TrimSpace(ev.Actor)
	ev.Origin = origin.Merge(ev.Origin, eventOriginFallback(teamDir, ev))

	dir := Directory(teamDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("job event: mkdir: %w", err)
	}
	f, err := os.OpenFile(EventPath(teamDir, ev.JobID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("job event: open: %w", err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(ev); err != nil {
		_ = f.Close()
		return fmt.Errorf("job event: encode: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("job event: close: %w", err)
	}
	return nil
}

func eventOriginFallback(teamDir string, ev *Event) origin.Envelope {
	if ev == nil {
		return origin.Envelope{}
	}
	fallback := origin.Envelope{
		Project:  projectID(teamDir),
		Instance: ev.Instance,
		Job:      ev.JobID,
		Trigger:  strings.TrimSpace(ev.Type),
		Build:    buildinfo.Current("").Display(),
	}
	if j, err := Read(teamDir, ev.JobID); err == nil && j != nil {
		fallback = origin.Merge(fallback, j.Origin)
	}
	return fallback
}

func projectID(teamDir string) string {
	id, _ := origin.ProjectID(teamDir)
	return id
}

// AppendSnapshotEvent appends an audit event using the current job fields.
func AppendSnapshotEvent(teamDir string, j *Job, eventType, actor, message string, data map[string]string) error {
	if j == nil {
		return nil
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		eventType = strings.TrimSpace(j.LastEvent)
	}
	if eventType == "" {
		eventType = "updated"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = strings.TrimSpace(j.LastStatus)
	}
	return AppendEvent(teamDir, &Event{
		JobID:    j.ID,
		Type:     eventType,
		Status:   j.Status,
		Instance: j.Instance,
		Message:  message,
		Actor:    actor,
		Origin:   j.Origin,
		Data:     data,
	})
}

// ListEvents reads a job event log. A missing log returns an empty slice.
func ListEvents(teamDir, rawID string) ([]Event, error) {
	events, err := listLiveEvents(teamDir, rawID)
	if err == nil {
		return events, nil
	}
	if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		return nil, err
	}
	return ArchivedEvents(teamDir, rawID)
}

func listLiveEvents(teamDir, rawID string) ([]Event, error) {
	id := IDFromInput(rawID)
	if id == "" {
		return nil, fmt.Errorf("job id %q produced an empty normalized id", rawID)
	}
	f, err := os.Open(EventPath(teamDir, id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	events, err := readEvents(f)
	if err != nil {
		return nil, fmt.Errorf("job events %s: %w", id, err)
	}
	return events, nil
}

func readEvents(r io.Reader) ([]Event, error) {
	scanner := bufio.NewScanner(r)
	var events []Event
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if ev.JobID == "" {
			return nil, fmt.Errorf("line %d: job_id is required", line)
		}
		if ev.Type == "" {
			return nil, fmt.Errorf("line %d: type is required", line)
		}
		if ev.Status != "" && !ValidStatus(ev.Status) {
			return nil, fmt.Errorf("line %d: invalid status %q", line, ev.Status)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// TailEvents returns at most the final n events. n <= 0 returns all events.
func TailEvents(events []Event, n int) []Event {
	if n <= 0 || n >= len(events) {
		return events
	}
	return events[len(events)-n:]
}
