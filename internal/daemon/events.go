package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/origin"
)

// LifecycleEvent is one append-only daemon lifecycle record. Stored as JSONL
// at `.agent_team/daemon/events.jsonl` and streamed by GET /v1/events.
type LifecycleEvent struct {
	ID       string          `json:"id"`
	TS       time.Time       `json:"ts"`
	Action   string          `json:"action"`
	Instance string          `json:"instance,omitempty"`
	Agent    string          `json:"agent,omitempty"`
	Job      string          `json:"job,omitempty"`
	Attempt  int             `json:"attempt,omitempty"`
	Ticket   string          `json:"ticket,omitempty"`
	Branch   string          `json:"branch,omitempty"`
	PR       string          `json:"pr,omitempty"`
	Head     string          `json:"head,omitempty"`
	Origin   origin.Envelope `json:"origin,omitempty"`
	Status   Status          `json:"status,omitempty"`
	PID      int             `json:"pid,omitempty"`
	ExitCode *int            `json:"exit_code,omitempty"`
	Message  string          `json:"message,omitempty"`
}

var lifecycleEventLock sync.Mutex

func lifecycleEventsPath(daemonRoot string) string {
	return filepath.Join(daemonRoot, "events.jsonl")
}

// AppendLifecycleEvent appends one lifecycle event. It is intentionally
// independent from instance metadata writes: callers should treat failures as
// best-effort observability failures, not as lifecycle failures.
func AppendLifecycleEvent(daemonRoot string, ev *LifecycleEvent) error {
	if ev == nil {
		return errors.New("events: nil event")
	}
	if ev.Action == "" {
		return errors.New("events: action is required")
	}
	if ev.ID == "" {
		ev.ID = newSessionID()
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	ev.Origin = origin.Merge(ev.Origin, lifecycleOriginFallback(daemonRoot, ev))
	if err := os.MkdirAll(daemonRoot, 0o755); err != nil {
		return fmt.Errorf("events: mkdir: %w", err)
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("events: marshal: %w", err)
	}
	body = append(body, '\n')

	lifecycleEventLock.Lock()
	defer lifecycleEventLock.Unlock()
	f, err := os.OpenFile(lifecycleEventsPath(daemonRoot), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("events: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("events: write: %w", err)
	}
	return nil
}

func lifecycleOriginFallback(daemonRoot string, ev *LifecycleEvent) origin.Envelope {
	if ev == nil {
		return origin.Envelope{}
	}
	teamDir := filepath.Dir(daemonRoot)
	id, _ := origin.ProjectID(teamDir)
	return origin.Envelope{
		Project:  id,
		Instance: ev.Instance,
		Agent:    ev.Agent,
		Job:      ev.Job,
		Trigger:  ev.Action,
		Build:    buildinfo.Current("").Display(),
	}
}

// ListLifecycleEvents reads lifecycle JSONL into memory. Missing files return
// an empty slice, mirroring StreamLifecycleEvents.
func ListLifecycleEvents(daemonRoot string) ([]*LifecycleEvent, error) {
	f, err := os.Open(lifecycleEventsPath(daemonRoot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("events: open: %w", err)
	}
	defer f.Close()

	var out []*LifecycleEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev LifecycleEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("events: parse line %d: %w", lineNo, err)
		}
		out = append(out, &ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("events: read: %w", err)
	}
	return out, nil
}

// StreamLifecycleEvents writes lifecycle JSONL. Missing files stream as empty
// output. With follow=true, the file is created if missing and tailed until
// ctx is cancelled.
func StreamLifecycleEvents(ctx context.Context, w io.Writer, daemonRoot string, follow bool, tailLines int) error {
	if tailLines < 0 {
		return errors.New("events: tail must be >= 0")
	}
	if err := os.MkdirAll(daemonRoot, 0o755); err != nil {
		return fmt.Errorf("events: mkdir: %w", err)
	}
	f, err := os.OpenFile(lifecycleEventsPath(daemonRoot), os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("events: open: %w", err)
	}
	defer f.Close()

	flusher, _ := w.(http.Flusher)
	if tailLines > 0 {
		if err := copyTailLines(w, f, tailLines); err != nil {
			return err
		}
	} else if _, err := io.Copy(w, f); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	if !follow {
		return nil
	}

	buf := make([]byte, 32*1024)
	ticker := time.NewTicker(logTailInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return werr
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) {
					break
				}
				return rerr
			}
		}
	}
}
