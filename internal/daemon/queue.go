package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	QueueStatePending = "pending"
	QueueStateDead    = "dead"
)

// QueueItem is one persisted topology dispatch waiting for capacity or manual retry.
type QueueItem struct {
	ID             string         `json:"id"`
	State          string         `json:"state"`
	EventType      string         `json:"event_type"`
	Instance       string         `json:"instance"`
	InstanceID     string         `json:"instance_id"`
	Payload        map[string]any `json:"payload"`
	Attempts       int            `json:"attempts"`
	LastError      string         `json:"last_error,omitempty"`
	NextRetry      time.Time      `json:"next_retry,omitempty"`
	QueuedAt       time.Time      `json:"queued_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeadLetteredAt time.Time      `json:"dead_lettered_at,omitempty"`
}

// QueueDrainResult describes one explicit queue drain pass.
type QueueDrainResult struct {
	Attempted  int            `json:"attempted"`
	Dispatched int            `json:"dispatched"`
	Rejected   int            `json:"rejected"`
	Pending    int            `json:"pending"`
	Dead       int            `json:"dead"`
	Outcomes   []EventOutcome `json:"outcomes"`
}

func QueueRoot(daemonRoot string) string {
	return filepath.Join(daemonRoot, "queue")
}

func QueuePendingDir(daemonRoot string) string {
	return filepath.Join(QueueRoot(daemonRoot), QueueStatePending)
}

func QueueDeadDir(daemonRoot string) string {
	return filepath.Join(QueueRoot(daemonRoot), QueueStateDead)
}

func QueuePath(daemonRoot, state, id string) string {
	return filepath.Join(queueDirForState(daemonRoot, state), id+".json")
}

func queueDirForState(daemonRoot, state string) string {
	if state == QueueStateDead {
		return QueueDeadDir(daemonRoot)
	}
	return QueuePendingDir(daemonRoot)
}

func WriteQueueItem(daemonRoot string, item *QueueItem) error {
	if err := validateQueueItem(item); err != nil {
		return err
	}
	dir := queueDirForState(daemonRoot, item.State)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("queue: mkdir: %w", err)
	}
	body, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("queue: marshal: %w", err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, item.ID+"-*.json.tmp")
	if err != nil {
		return fmt.Errorf("queue: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("queue: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("queue: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("queue: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), QueuePath(daemonRoot, item.State, item.ID)); err != nil {
		return fmt.Errorf("queue: rename: %w", err)
	}
	return nil
}

func ReadQueueItem(daemonRoot, id string) (*QueueItem, error) {
	for _, state := range []string{QueueStatePending, QueueStateDead} {
		item, err := readQueueItemState(daemonRoot, state, id)
		if err == nil {
			return item, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, fs.ErrNotExist
}

func readQueueItemState(daemonRoot, state, id string) (*QueueItem, error) {
	body, err := os.ReadFile(QueuePath(daemonRoot, state, id))
	if err != nil {
		return nil, err
	}
	var item QueueItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("queue: parse %s/%s: %w", state, id, err)
	}
	if item.ID == "" {
		item.ID = id
	}
	item.State = state
	if err := validateQueueItem(&item); err != nil {
		return nil, fmt.Errorf("queue: %s/%s: %w", state, id, err)
	}
	return &item, nil
}

func ListQueueItems(daemonRoot string) ([]*QueueItem, error) {
	var out []*QueueItem
	for _, state := range []string{QueueStatePending, QueueStateDead} {
		items, err := listQueueState(daemonRoot, state)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return out[i].State < out[j].State
		}
		return out[i].QueuedAt.Before(out[j].QueuedAt)
	})
	return out, nil
}

func listQueueState(daemonRoot, state string) ([]*QueueItem, error) {
	dir := queueDirForState(daemonRoot, state)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*QueueItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		item, err := readQueueItemState(daemonRoot, state, id)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QueuedAt.Before(out[j].QueuedAt) })
	return out, nil
}

func RemoveQueueItem(daemonRoot, id string) error {
	var removed bool
	for _, state := range []string{QueueStatePending, QueueStateDead} {
		err := os.Remove(QueuePath(daemonRoot, state, id))
		switch {
		case err == nil:
			removed = true
		case errors.Is(err, fs.ErrNotExist):
		default:
			return err
		}
	}
	if !removed {
		return fs.ErrNotExist
	}
	return nil
}

func MoveQueueItemToDead(daemonRoot string, item *QueueItem) error {
	if item == nil {
		return errors.New("queue: nil item")
	}
	_ = os.Remove(QueuePath(daemonRoot, QueueStatePending, item.ID))
	now := time.Now().UTC()
	item.State = QueueStateDead
	item.DeadLetteredAt = now
	item.UpdatedAt = now
	return WriteQueueItem(daemonRoot, item)
}

func ResetQueueItemForRetry(daemonRoot string, item *QueueItem) error {
	if item == nil {
		return errors.New("queue: nil item")
	}
	_ = os.Remove(QueuePath(daemonRoot, QueueStateDead, item.ID))
	item.State = QueueStatePending
	item.LastError = ""
	item.NextRetry = time.Time{}
	item.UpdatedAt = time.Now().UTC()
	item.DeadLetteredAt = time.Time{}
	return WriteQueueItem(daemonRoot, item)
}

func validateQueueItem(item *QueueItem) error {
	if item == nil {
		return errors.New("queue: nil item")
	}
	if strings.TrimSpace(item.ID) == "" {
		return errors.New("queue: id is required")
	}
	switch item.State {
	case QueueStatePending, QueueStateDead:
	default:
		return fmt.Errorf("queue: unknown state %q", item.State)
	}
	if strings.TrimSpace(item.EventType) == "" {
		return errors.New("queue: event_type is required")
	}
	if strings.TrimSpace(item.Instance) == "" {
		return errors.New("queue: instance is required")
	}
	if strings.TrimSpace(item.InstanceID) == "" {
		return errors.New("queue: instance_id is required")
	}
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	if item.QueuedAt.IsZero() {
		return errors.New("queue: queued_at is required")
	}
	if item.UpdatedAt.IsZero() {
		return errors.New("queue: updated_at is required")
	}
	return nil
}
