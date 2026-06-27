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
	OutboxStatePending   = "pending"
	OutboxStateProcessed = "processed"
	OutboxStateFailed    = "failed"
)

// OutboxItem is an event a sandboxed agent wrote to disk because it could not
// reach the daemon transport directly.
type OutboxItem struct {
	ID          string         `json:"id"`
	State       string         `json:"state"`
	Type        string         `json:"type"`
	Payload     map[string]any `json:"payload"`
	Source      string         `json:"source,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	ProcessedAt time.Time      `json:"processed_at,omitempty"`
	FailedAt    time.Time      `json:"failed_at,omitempty"`
	LastError   string         `json:"last_error,omitempty"`
}

// OutboxDrainResult describes one outbox drain pass.
type OutboxDrainResult struct {
	Attempted    int               `json:"attempted"`
	Published    int               `json:"published"`
	WouldPublish int               `json:"would_publish,omitempty"`
	Rejected     int               `json:"rejected"`
	Pending      int               `json:"pending"`
	Processed    int               `json:"processed"`
	Failed       int               `json:"failed"`
	DryRun       bool              `json:"dry_run,omitempty"`
	Items        []OutboxDrainItem `json:"items"`
}

type OutboxDrainItem struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Action   string         `json:"action"`
	Error    string         `json:"error,omitempty"`
	Outcomes []EventOutcome `json:"outcomes,omitempty"`
}

func OutboxRoot(teamDir string) string {
	return filepath.Join(teamDir, "outbox")
}

func OutboxPendingDir(teamDir string) string {
	return filepath.Join(OutboxRoot(teamDir), OutboxStatePending)
}

func OutboxProcessedDir(teamDir string) string {
	return filepath.Join(OutboxRoot(teamDir), OutboxStateProcessed)
}

func OutboxFailedDir(teamDir string) string {
	return filepath.Join(OutboxRoot(teamDir), OutboxStateFailed)
}

func OutboxPath(teamDir, state, id string) string {
	return filepath.Join(outboxDirForState(teamDir, state), id+".json")
}

func outboxDirForState(teamDir, state string) string {
	switch state {
	case OutboxStateProcessed:
		return OutboxProcessedDir(teamDir)
	case OutboxStateFailed:
		return OutboxFailedDir(teamDir)
	default:
		return OutboxPendingDir(teamDir)
	}
}

func WriteOutboxItem(teamDir string, item *OutboxItem) error {
	if err := validateOutboxItem(item); err != nil {
		return err
	}
	dir := outboxDirForState(teamDir, item.State)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("outbox: mkdir: %w", err)
	}
	body, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("outbox: marshal: %w", err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, item.ID+"-*.json.tmp")
	if err != nil {
		return fmt.Errorf("outbox: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("outbox: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("outbox: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("outbox: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), OutboxPath(teamDir, item.State, item.ID)); err != nil {
		return fmt.Errorf("outbox: rename: %w", err)
	}
	return nil
}

func ReadOutboxItem(teamDir, id string) (*OutboxItem, error) {
	for _, state := range []string{OutboxStatePending, OutboxStateProcessed, OutboxStateFailed} {
		item, err := readOutboxItemState(teamDir, state, id)
		if err == nil {
			return item, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, fs.ErrNotExist
}

func RemoveOutboxItem(teamDir, id string) error {
	if err := validateOutboxID(id); err != nil {
		return err
	}
	var removed bool
	for _, state := range []string{OutboxStatePending, OutboxStateProcessed, OutboxStateFailed} {
		err := os.Remove(OutboxPath(teamDir, state, id))
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

func ListOutboxItems(teamDir string) ([]*OutboxItem, error) {
	var out []*OutboxItem
	for _, state := range []string{OutboxStatePending, OutboxStateProcessed, OutboxStateFailed} {
		items, err := listOutboxState(teamDir, state)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return outboxStateRank(out[i].State) < outboxStateRank(out[j].State)
		}
		return outboxItemSortTime(out[i]).Before(outboxItemSortTime(out[j]))
	})
	return out, nil
}

func MoveOutboxItem(teamDir string, item *OutboxItem, state string) error {
	if item == nil {
		return errors.New("outbox: nil item")
	}
	now := time.Now().UTC()
	previousState := item.State
	item.State = state
	item.UpdatedAt = now
	switch state {
	case OutboxStateProcessed:
		item.ProcessedAt = now
		item.FailedAt = time.Time{}
		item.LastError = ""
	case OutboxStateFailed:
		item.FailedAt = now
	case OutboxStatePending:
		item.ProcessedAt = time.Time{}
		item.FailedAt = time.Time{}
		item.LastError = ""
	}
	if err := WriteOutboxItem(teamDir, item); err != nil {
		item.State = previousState
		return err
	}
	for _, existing := range []string{OutboxStatePending, OutboxStateProcessed, OutboxStateFailed} {
		if existing != state {
			_ = os.Remove(OutboxPath(teamDir, existing, item.ID))
		}
	}
	return nil
}

func (r *EventResolver) DrainOutboxWithResult(dryRun bool) (*OutboxDrainResult, error) {
	result := &OutboxDrainResult{DryRun: dryRun, Items: []OutboxDrainItem{}}
	items, err := listOutboxState(r.teamDir, OutboxStatePending)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		if dryRun {
			result.WouldPublish++
			result.Items = append(result.Items, OutboxDrainItem{
				ID:     item.ID,
				Type:   item.Type,
				Action: "would_publish",
			})
			continue
		}
		result.Attempted++
		eventResult, err := r.EventWithResult(item.Type, item.Payload)
		if err != nil {
			item.LastError = err.Error()
			if moveErr := MoveOutboxItem(r.teamDir, item, OutboxStateFailed); moveErr != nil {
				return result, moveErr
			}
			result.Rejected++
			result.Items = append(result.Items, OutboxDrainItem{
				ID:     item.ID,
				Type:   item.Type,
				Action: "failed",
				Error:  err.Error(),
			})
			continue
		}
		outcomes := []EventOutcome{}
		if eventResult != nil {
			outcomes = eventResult.Outcomes
		}
		if err := MoveOutboxItem(r.teamDir, item, OutboxStateProcessed); err != nil {
			return result, err
		}
		result.Published++
		result.Items = append(result.Items, OutboxDrainItem{
			ID:       item.ID,
			Type:     item.Type,
			Action:   "published",
			Outcomes: outcomes,
		})
	}
	if err := countOutboxStates(result, r.teamDir); err != nil {
		return result, err
	}
	return result, nil
}

func readOutboxItemState(teamDir, state, id string) (*OutboxItem, error) {
	if err := validateOutboxID(id); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(OutboxPath(teamDir, state, id))
	if err != nil {
		return nil, err
	}
	var item OutboxItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("outbox: parse %s/%s: %w", state, id, err)
	}
	if item.ID == "" {
		item.ID = id
	}
	item.State = state
	if err := validateOutboxItem(&item); err != nil {
		return nil, fmt.Errorf("outbox: %s/%s: %w", state, id, err)
	}
	return &item, nil
}

func listOutboxState(teamDir, state string) ([]*OutboxItem, error) {
	dir := outboxDirForState(teamDir, state)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*OutboxItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		item, err := readOutboxItemState(teamDir, state, id)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return outboxItemSortTime(out[i]).Before(outboxItemSortTime(out[j]))
	})
	return out, nil
}

func countOutboxStates(result *OutboxDrainResult, teamDir string) error {
	if result == nil {
		return nil
	}
	items, err := ListOutboxItems(teamDir)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		switch item.State {
		case OutboxStatePending:
			result.Pending++
		case OutboxStateProcessed:
			result.Processed++
		case OutboxStateFailed:
			result.Failed++
		}
	}
	return nil
}

func outboxItemSortTime(item *OutboxItem) time.Time {
	if item == nil {
		return time.Time{}
	}
	if !item.CreatedAt.IsZero() {
		return item.CreatedAt
	}
	return item.UpdatedAt
}

func outboxStateRank(state string) int {
	switch state {
	case OutboxStatePending:
		return 0
	case OutboxStateFailed:
		return 1
	case OutboxStateProcessed:
		return 2
	default:
		return 3
	}
}

func validateOutboxItem(item *OutboxItem) error {
	if item == nil {
		return errors.New("outbox: nil item")
	}
	if err := validateOutboxID(item.ID); err != nil {
		return err
	}
	switch item.State {
	case OutboxStatePending, OutboxStateProcessed, OutboxStateFailed:
	default:
		return fmt.Errorf("outbox: unknown state %q", item.State)
	}
	if strings.TrimSpace(item.Type) == "" {
		return errors.New("outbox: type is required")
	}
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	if item.CreatedAt.IsZero() {
		return errors.New("outbox: created_at is required")
	}
	if item.UpdatedAt.IsZero() {
		return errors.New("outbox: updated_at is required")
	}
	return nil
}

func validateOutboxID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("outbox: id is required")
	}
	if id == "." || id == ".." || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("outbox: id %q invalid: path segments are not allowed", id)
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("outbox: id %q invalid: only ASCII letters, digits, '.', '_' and '-' are allowed", id)
	}
	return nil
}
