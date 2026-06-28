package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Message is one entry in an instance's mailbox. Stored one-per-line as JSON
// in `<daemon-root>/<instance>/mailbox.jsonl`. Append-only — `inbox ack` does
// not delete; it advances the cursor file.
type Message struct {
	ID   string    `json:"id"`
	From string    `json:"from"`
	To   string    `json:"to"`
	Body string    `json:"body"`
	TS   time.Time `json:"ts"`
}

// mailboxLock serialises appends to a single file from concurrent /v1/message
// requests. POSIX `O_APPEND` is atomic for small writes on local FS, but we
// also want the serialised observable order to match the JSONL invariant —
// one well-formed object per line — even under heavy concurrency. A per-path
// mutex map is overkill for one daemon instance; one process-wide mutex is
// fine.
var mailboxLock sync.Mutex

// MailboxPath returns the JSONL mailbox file path for an instance.
func MailboxPath(daemonRoot, instance string) string {
	return filepath.Join(instanceDir(daemonRoot, instance), "mailbox.jsonl")
}

// MailboxCursorPath returns the ack-cursor file path.
func MailboxCursorPath(daemonRoot, instance string) string {
	return filepath.Join(instanceDir(daemonRoot, instance), "mailbox-cursor.txt")
}

// AppendMessage appends msg to the instance's mailbox. If msg.ID is empty,
// a new UUID-v4 is generated. The instance's daemon dir is created if missing
// — sending a message to an instance that hasn't been dispatched yet is
// allowed (it will be visible whenever that instance is next launched).
func AppendMessage(daemonRoot, instance string, msg *Message) error {
	if instance == "" {
		return errors.New("mailbox: instance is required")
	}
	if msg == nil {
		return errors.New("mailbox: nil message")
	}
	if msg.ID == "" {
		msg.ID = newSessionID()
	}
	if msg.TS.IsZero() {
		msg.TS = time.Now().UTC()
	}
	msg.To = instance

	if err := os.MkdirAll(instanceDir(daemonRoot, instance), 0o755); err != nil {
		return fmt.Errorf("mailbox: mkdir: %w", err)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mailbox: marshal: %w", err)
	}
	body = append(body, '\n')

	mailboxLock.Lock()
	defer mailboxLock.Unlock()
	f, err := os.OpenFile(MailboxPath(daemonRoot, instance), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("mailbox: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("mailbox: write: %w", err)
	}
	return nil
}

// ReadMessages returns every message currently in an instance's mailbox, in
// arrival order. Missing mailbox returns an empty slice.
func ReadMessages(daemonRoot, instance string) ([]*Message, error) {
	f, err := os.Open(MailboxPath(daemonRoot, instance))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []*Message
	sc := bufio.NewScanner(f)
	// Allow large message bodies (default 64KiB token cap is too small for
	// arbitrary user text). 1MiB is generous and bounded.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			// One bad line shouldn't blind every other message. Skip.
			continue
		}
		out = append(out, &m)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mailbox: scan: %w", err)
	}
	return out, nil
}

// RewriteMessages replaces an instance mailbox with messages in arrival order.
// Atomic: writes a temp file and renames over the JSONL mailbox. The same
// process-wide lock as AppendMessage prevents interleaved local rewrites/appends.
func RewriteMessages(daemonRoot, instance string, messages []*Message) error {
	if instance == "" {
		return errors.New("mailbox: instance is required")
	}
	dir := instanceDir(daemonRoot, instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mailbox: mkdir: %w", err)
	}

	mailboxLock.Lock()
	defer mailboxLock.Unlock()

	tmp, err := os.CreateTemp(dir, "mailbox-*.tmp")
	if err != nil {
		return fmt.Errorf("mailbox: temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	enc := json.NewEncoder(tmp)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if err := enc.Encode(msg); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("mailbox: encode: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mailbox: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mailbox: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), MailboxPath(daemonRoot, instance)); err != nil {
		return fmt.Errorf("mailbox: rename: %w", err)
	}
	return nil
}

// ReadUnacked returns every message that follows the cursor. The cursor stores
// the highest-acked message ID; we yield messages whose index is strictly
// greater than the cursor's match. If the cursor points at an ID no longer in
// the file, every message is returned.
func ReadUnacked(daemonRoot, instance string) ([]*Message, error) {
	all, err := ReadMessages(daemonRoot, instance)
	if err != nil {
		return nil, err
	}
	cursor, err := ReadCursor(daemonRoot, instance)
	if err != nil {
		return nil, err
	}
	if cursor == "" {
		return all, nil
	}
	for i, m := range all {
		if m.ID == cursor {
			return all[i+1:], nil
		}
	}
	return all, nil
}

// WriteCursor advances the ack cursor for an instance. Atomic: writes to a
// temp file and renames over.
func WriteCursor(daemonRoot, instance, id string) error {
	if instance == "" {
		return errors.New("mailbox: instance is required")
	}
	if err := os.MkdirAll(instanceDir(daemonRoot, instance), 0o755); err != nil {
		return err
	}
	target := MailboxCursorPath(daemonRoot, instance)
	tmp, err := os.CreateTemp(instanceDir(daemonRoot, instance), "cursor-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(id + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// ReadCursor returns the current ack cursor (empty string if none set).
func ReadCursor(daemonRoot, instance string) (string, error) {
	body, err := os.ReadFile(MailboxCursorPath(daemonRoot, instance))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}
