package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ChannelMessage is one entry in a channel's append log.
type ChannelMessage struct {
	Seq    int64     `json:"seq"`
	Sender string    `json:"sender"`
	Body   string    `json:"body"`
	TS     time.Time `json:"ts"`
}

// ChannelInfo is the public summary returned by GET /v1/channels.
type ChannelInfo struct {
	Name          string    `json:"name"`
	Subscribers   int       `json:"subscribers"`
	MessageCount  int64     `json:"message_count"`
	LastMessageTS time.Time `json:"last_message_ts,omitempty"`
}

// channelNameRE accepts `#` followed by 1-64 chars of [a-z0-9-], starting
// with [a-z0-9]. Documented in PR body and surfaced by both the daemon and
// CLI. Rejected names cannot reach the filesystem.
var channelNameRE = regexp.MustCompile(`^#[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateChannelName returns nil if the name is well-formed.
func ValidateChannelName(name string) error {
	if !channelNameRE.MatchString(name) {
		return fmt.Errorf("channel name %q invalid: must match %s", name, channelNameRE)
	}
	return nil
}

// channelDir returns the on-disk directory for a channel. The leading `#` is
// stripped from the path component (filesystem-friendly); callers always pass
// the canonical `#name` form.
func channelDir(daemonRoot, name string) string {
	return filepath.Join(daemonRoot, "channels", strings.TrimPrefix(name, "#"))
}

// channelMessagesPath / channelSubscriptionsPath: on-disk contract.
func channelMessagesPath(daemonRoot, name string) string {
	return filepath.Join(channelDir(daemonRoot, name), "messages.jsonl")
}

func channelSubscriptionsPath(daemonRoot, name string) string {
	return filepath.Join(channelDir(daemonRoot, name), "subscriptions.jsonl")
}

// subscriptionEvent is one line in subscriptions.jsonl. The materialised
// per-instance state is the *latest* event for that instance: subscribe means
// active (with the recorded cursor as a starting point), unsubscribe means
// inactive. Cursor advancement happens via Ack which writes a fresh
// subscribe-with-new-cursor entry.
type subscriptionEvent struct {
	Event    string    `json:"event"` // "subscribe" | "unsubscribe" | "ack"
	Instance string    `json:"instance"`
	Cursor   int64     `json:"cursor"`
	TS       time.Time `json:"ts"`
}

// Subscription is the materialised view of one instance's subscription.
type Subscription struct {
	Instance     string
	Cursor       int64
	SubscribedAt time.Time
}

// channelState holds in-memory caches for one channel: the per-channel mutex
// and a wake channel for long-pollers. The high-water seq is computed lazily
// on first publish from the on-disk file (recovers correctly across daemon
// restarts; we don't trust an in-memory counter that could drift from disk).
type channelState struct {
	mu       sync.Mutex
	maxSeq   int64
	seqKnown bool
	// notifyMu guards swaps of `notify`. Every Publish closes the current
	// channel and installs a fresh one; long-pollers grab the snapshot under
	// notifyMu before releasing the per-channel `mu`.
	notifyMu sync.Mutex
	notify   chan struct{}
}

// ChannelStore owns every channel in a daemon root. Per-channel state is
// lazily allocated on first reference.
type ChannelStore struct {
	daemonRoot string

	mu       sync.RWMutex
	channels map[string]*channelState
}

// NewChannelStore constructs a store rooted at daemonRoot
// (`.agent_team/daemon/` in production). Channel data lives at
// `<daemonRoot>/channels/<sanitised-name>/`.
func NewChannelStore(daemonRoot string) *ChannelStore {
	return &ChannelStore{
		daemonRoot: daemonRoot,
		channels:   make(map[string]*channelState),
	}
}

// state returns (and creates if needed) the per-channel struct.
func (s *ChannelStore) state(name string) *channelState {
	s.mu.RLock()
	st, ok := s.channels[name]
	s.mu.RUnlock()
	if ok {
		return st
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.channels[name]; ok {
		return st
	}
	st = &channelState{notify: make(chan struct{})}
	s.channels[name] = st
	return st
}

// PublishResult is what Publish returns to the caller.
type PublishResult struct {
	Seq int64
	TS  time.Time
}

// Publish appends a message to the channel's log under the per-channel lock.
// Seq is monotonic per channel. The notify channel is closed-and-swapped so
// any waiters in Drain wake up.
func (s *ChannelStore) Publish(name, sender, body string) (*PublishResult, error) {
	if err := ValidateChannelName(name); err != nil {
		return nil, err
	}
	st := s.state(name)
	st.mu.Lock()
	defer st.mu.Unlock()

	if !st.seqKnown {
		seq, err := scanMaxSeq(channelMessagesPath(s.daemonRoot, name))
		if err != nil {
			return nil, fmt.Errorf("channel %s: load seq: %w", name, err)
		}
		st.maxSeq = seq
		st.seqKnown = true
	}
	st.maxSeq++
	msg := ChannelMessage{
		Seq:    st.maxSeq,
		Sender: sender,
		Body:   body,
		TS:     time.Now().UTC(),
	}
	if err := appendChannelMessage(s.daemonRoot, name, &msg); err != nil {
		// Roll back the in-memory counter so a retry doesn't skip a seq.
		st.maxSeq--
		return nil, err
	}

	st.notifyMu.Lock()
	old := st.notify
	st.notify = make(chan struct{})
	st.notifyMu.Unlock()
	close(old)

	return &PublishResult{Seq: msg.Seq, TS: msg.TS}, nil
}

// Subscribe records an instance as a subscriber. Idempotent: re-subscribing
// returns the existing cursor without writing a duplicate event. Returns
// (cursor, fresh) where fresh=true means this call added a new subscription
// (vs. a no-op re-subscribe).
func (s *ChannelStore) Subscribe(name, instance string) (int64, bool, error) {
	if err := ValidateChannelName(name); err != nil {
		return 0, false, err
	}
	if instance == "" {
		return 0, false, errors.New("subscribe: instance is required")
	}
	st := s.state(name)
	st.mu.Lock()
	defer st.mu.Unlock()

	subs, err := loadSubscriptions(s.daemonRoot, name)
	if err != nil {
		return 0, false, err
	}
	if existing, ok := subs[instance]; ok {
		// Already subscribed → no-op, return the cursor we have.
		return existing.Cursor, false, nil
	}
	// New subscriber: cursor starts at the current head — they don't replay
	// history that arrived before they subscribed.
	if !st.seqKnown {
		seq, err := scanMaxSeq(channelMessagesPath(s.daemonRoot, name))
		if err != nil {
			return 0, false, err
		}
		st.maxSeq = seq
		st.seqKnown = true
	}
	now := time.Now().UTC()
	if err := appendSubscriptionEvent(s.daemonRoot, name, &subscriptionEvent{
		Event:    "subscribe",
		Instance: instance,
		Cursor:   st.maxSeq,
		TS:       now,
	}); err != nil {
		return 0, false, err
	}
	return st.maxSeq, true, nil
}

// Unsubscribe removes a subscription. Idempotent; not-subscribed returns
// (false, nil).
func (s *ChannelStore) Unsubscribe(name, instance string) (bool, error) {
	if err := ValidateChannelName(name); err != nil {
		return false, err
	}
	if instance == "" {
		return false, errors.New("unsubscribe: instance is required")
	}
	st := s.state(name)
	st.mu.Lock()
	defer st.mu.Unlock()

	subs, err := loadSubscriptions(s.daemonRoot, name)
	if err != nil {
		return false, err
	}
	if _, ok := subs[instance]; !ok {
		return false, nil
	}
	now := time.Now().UTC()
	if err := appendSubscriptionEvent(s.daemonRoot, name, &subscriptionEvent{
		Event:    "unsubscribe",
		Instance: instance,
		TS:       now,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// Ack advances a subscription cursor. The new cursor must be >= the existing
// one (cursor monotonicity); otherwise an error is returned.
func (s *ChannelStore) Ack(name, instance string, cursor int64) error {
	if err := ValidateChannelName(name); err != nil {
		return err
	}
	if instance == "" {
		return errors.New("ack: instance is required")
	}
	st := s.state(name)
	st.mu.Lock()
	defer st.mu.Unlock()

	subs, err := loadSubscriptions(s.daemonRoot, name)
	if err != nil {
		return err
	}
	cur, ok := subs[instance]
	if !ok {
		return fmt.Errorf("ack: %s is not subscribed to %s", instance, name)
	}
	if cursor < cur.Cursor {
		return fmt.Errorf("ack: cursor %d below current %d for %s on %s", cursor, cur.Cursor, instance, name)
	}
	if cursor == cur.Cursor {
		return nil
	}
	now := time.Now().UTC()
	return appendSubscriptionEvent(s.daemonRoot, name, &subscriptionEvent{
		Event:    "ack",
		Instance: instance,
		Cursor:   cursor,
		TS:       now,
	})
}

// DrainResult bundles the messages and the post-drain cursor for the caller.
type DrainResult struct {
	Messages []*ChannelMessage
	Cursor   int64
}

// Drain returns every message for `instance` after its cursor (or after
// `since` if non-nil). With `wait > 0`, blocks up to that duration if the
// initial drain is empty, returning whatever is present at deadline.
//
// `since` overrides the stored cursor; useful for re-reads / debugging.
// Without `since`, the instance must be subscribed (we need a cursor).
func (s *ChannelStore) Drain(ctx context.Context, name, instance string, since *int64, wait time.Duration) (*DrainResult, error) {
	if err := ValidateChannelName(name); err != nil {
		return nil, err
	}
	if instance == "" {
		return nil, errors.New("drain: instance is required")
	}
	st := s.state(name)

	read := func() (*DrainResult, <-chan struct{}, error) {
		st.mu.Lock()
		defer st.mu.Unlock()

		var cursor int64
		if since != nil {
			cursor = *since
		} else {
			subs, err := loadSubscriptions(s.daemonRoot, name)
			if err != nil {
				return nil, nil, err
			}
			cur, ok := subs[instance]
			if !ok {
				return nil, nil, fmt.Errorf("drain: %s is not subscribed to %s", instance, name)
			}
			cursor = cur.Cursor
		}

		all, err := readChannelMessagesSince(s.daemonRoot, name, cursor)
		if err != nil {
			return nil, nil, err
		}
		out := &DrainResult{Messages: all, Cursor: cursor}
		if len(all) > 0 {
			out.Cursor = all[len(all)-1].Seq
		}

		st.notifyMu.Lock()
		notify := st.notify
		st.notifyMu.Unlock()
		return out, notify, nil
	}

	res, notify, err := read()
	if err != nil {
		return nil, err
	}
	if len(res.Messages) > 0 || wait <= 0 {
		return res, nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return res, nil
	case <-timer.C:
		return res, nil
	case <-notify:
		// Re-read from current state.
		out, _, err := read()
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}

// List walks the channels dir and summarises each channel: subscriber count,
// message count, last-message timestamp.
func (s *ChannelStore) List() ([]*ChannelInfo, error) {
	root := filepath.Join(s.daemonRoot, "channels")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*ChannelInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Reconstitute the canonical `#name` form from the directory name.
		canon := "#" + e.Name()
		if err := ValidateChannelName(canon); err != nil {
			continue
		}
		info, err := s.summarise(canon)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// summarise builds a ChannelInfo for one channel without altering its state.
func (s *ChannelStore) summarise(name string) (*ChannelInfo, error) {
	subs, err := loadSubscriptions(s.daemonRoot, name)
	if err != nil {
		return nil, err
	}
	count, lastTS, err := scanMessagesSummary(channelMessagesPath(s.daemonRoot, name))
	if err != nil {
		return nil, err
	}
	return &ChannelInfo{
		Name:          name,
		Subscribers:   len(subs),
		MessageCount:  count,
		LastMessageTS: lastTS,
	}, nil
}

// Delete removes a channel's storage entirely. Returns (false, nil) if the
// channel didn't exist.
func (s *ChannelStore) Delete(name string) (bool, error) {
	if err := ValidateChannelName(name); err != nil {
		return false, err
	}
	dir := channelDir(s.daemonRoot, name)
	st, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !st.IsDir() {
		return false, fmt.Errorf("channel storage at %s is not a directory", dir)
	}

	cs := s.state(name)
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	// Reset the in-memory state so a subsequent publish starts at seq 1.
	cs.maxSeq = 0
	cs.seqKnown = false
	// Don't bother swapping `notify`: any current waiters would just see no
	// messages, return empty, and that's correct.
	return true, nil
}

// Subscriptions returns the current subscription map for tests / introspection.
func (s *ChannelStore) Subscriptions(name string) (map[string]Subscription, error) {
	if err := ValidateChannelName(name); err != nil {
		return nil, err
	}
	st := s.state(name)
	st.mu.Lock()
	defer st.mu.Unlock()
	return loadSubscriptions(s.daemonRoot, name)
}

// --- File I/O helpers ----------------------------------------------------

// appendChannelMessage writes msg as one JSON line to messages.jsonl. Caller
// must hold the per-channel mutex.
func appendChannelMessage(daemonRoot, name string, msg *ChannelMessage) error {
	if err := os.MkdirAll(channelDir(daemonRoot, name), 0o755); err != nil {
		return fmt.Errorf("channel %s: mkdir: %w", name, err)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("channel %s: marshal: %w", name, err)
	}
	body = append(body, '\n')
	f, err := os.OpenFile(channelMessagesPath(daemonRoot, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("channel %s: open: %w", name, err)
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("channel %s: write: %w", name, err)
	}
	return nil
}

// appendSubscriptionEvent writes one event line to subscriptions.jsonl.
func appendSubscriptionEvent(daemonRoot, name string, ev *subscriptionEvent) error {
	if err := os.MkdirAll(channelDir(daemonRoot, name), 0o755); err != nil {
		return fmt.Errorf("channel %s: mkdir: %w", name, err)
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("channel %s: marshal sub: %w", name, err)
	}
	body = append(body, '\n')
	f, err := os.OpenFile(channelSubscriptionsPath(daemonRoot, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("channel %s: open subs: %w", name, err)
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("channel %s: write subs: %w", name, err)
	}
	return nil
}

// loadSubscriptions reads subscriptions.jsonl and materialises the current
// state map. Replays events in order: subscribe → record / refresh; ack →
// bump cursor (only if currently subscribed); unsubscribe → remove.
func loadSubscriptions(daemonRoot, name string) (map[string]Subscription, error) {
	out := map[string]Subscription{}
	f, err := os.Open(channelSubscriptionsPath(daemonRoot, name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev subscriptionEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev.Event {
		case "subscribe":
			if _, ok := out[ev.Instance]; !ok {
				out[ev.Instance] = Subscription{
					Instance:     ev.Instance,
					Cursor:       ev.Cursor,
					SubscribedAt: ev.TS,
				}
			}
		case "ack":
			if cur, ok := out[ev.Instance]; ok && ev.Cursor > cur.Cursor {
				cur.Cursor = ev.Cursor
				out[ev.Instance] = cur
			}
		case "unsubscribe":
			delete(out, ev.Instance)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("channel %s: scan subs: %w", name, err)
	}
	return out, nil
}

// readChannelMessagesSince streams messages.jsonl and returns those with
// seq > since.
func readChannelMessagesSince(daemonRoot, name string, since int64) ([]*ChannelMessage, error) {
	f, err := os.Open(channelMessagesPath(daemonRoot, name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []*ChannelMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m ChannelMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m.Seq > since {
			out = append(out, &m)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("channel %s: scan msgs: %w", name, err)
	}
	return out, nil
}

// scanMaxSeq returns the highest seq currently persisted for the channel,
// or 0 if the file does not exist or is empty. Used for cold-start recovery.
func scanMaxSeq(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	var max int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m ChannelMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m.Seq > max {
			max = m.Seq
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return max, nil
}

// scanMessagesSummary returns (count, last_ts) without loading every message
// body. Used by ChannelStore.List.
func scanMessagesSummary(path string) (int64, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	defer f.Close()
	var count int64
	var lastTS time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		count++
		var m ChannelMessage
		if err := json.Unmarshal([]byte(line), &m); err == nil && m.TS.After(lastTS) {
			lastTS = m.TS
		}
	}
	if err := sc.Err(); err != nil {
		return 0, time.Time{}, err
	}
	return count, lastTS, nil
}
