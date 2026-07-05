package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/origin"
)

// Record is one finalized runtime run captured from daemon metadata.
type Record struct {
	Instance              string          `json:"instance,omitempty" toml:"instance,omitempty"`
	Agent                 string          `json:"agent,omitempty" toml:"agent,omitempty"`
	Runtime               string          `json:"runtime,omitempty" toml:"runtime,omitempty"`
	TokensAvailable       bool            `json:"tokens_available" toml:"tokens_available"`
	InputTokens           int64           `json:"input_tokens,omitempty" toml:"input_tokens,omitempty"`
	CachedInputTokens     int64           `json:"cached_input_tokens,omitempty" toml:"cached_input_tokens,omitempty"`
	OutputTokens          int64           `json:"output_tokens,omitempty" toml:"output_tokens,omitempty"`
	ReasoningOutputTokens int64           `json:"reasoning_output_tokens,omitempty" toml:"reasoning_output_tokens,omitempty"`
	Turns                 int             `json:"turns,omitempty" toml:"turns,omitempty"`
	DurationMS            int64           `json:"duration_ms,omitempty" toml:"duration_ms,omitempty"`
	StartedAt             time.Time       `json:"started_at,omitempty" toml:"started_at,omitempty"`
	EndedAt               time.Time       `json:"ended_at,omitempty" toml:"ended_at,omitempty"`
	CapturedAt            time.Time       `json:"captured_at,omitempty" toml:"captured_at,omitempty"`
	Source                string          `json:"source,omitempty" toml:"source,omitempty"`
	Origin                origin.Envelope `json:"origin,omitempty" toml:"origin,omitempty"`
}

// Summary is an aggregate over one or more usage records.
type Summary struct {
	Runs                  int   `json:"runs,omitempty" toml:"runs,omitempty"`
	TokenAvailableRuns    int   `json:"token_available_runs,omitempty" toml:"token_available_runs,omitempty"`
	TokenUnavailableRuns  int   `json:"token_unavailable_runs,omitempty" toml:"token_unavailable_runs,omitempty"`
	InputTokens           int64 `json:"input_tokens,omitempty" toml:"input_tokens,omitempty"`
	CachedInputTokens     int64 `json:"cached_input_tokens,omitempty" toml:"cached_input_tokens,omitempty"`
	OutputTokens          int64 `json:"output_tokens,omitempty" toml:"output_tokens,omitempty"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens,omitempty" toml:"reasoning_output_tokens,omitempty"`
	Turns                 int   `json:"turns,omitempty" toml:"turns,omitempty"`
	DurationMS            int64 `json:"duration_ms,omitempty" toml:"duration_ms,omitempty"`
}

// JobUsage stores both an idempotent per-run ledger and its materialized
// summary so archived job records remain self-contained.
type JobUsage struct {
	Summary Summary  `json:"summary" toml:"summary"`
	Records []Record `json:"records,omitempty" toml:"records,omitempty"`
}

// CaptureInput describes the daemon metadata needed to capture a runtime's
// final usage before its log can be cleaned up.
type CaptureInput struct {
	Instance  string
	Agent     string
	Runtime   string
	LogPath   string
	StartedAt time.Time
	EndedAt   time.Time
	Now       time.Time
}

// Capture reads the runtime log, when useful, and returns a usage record for a
// finalized instance. Missing logs still produce duration/turn metadata with
// tokens unavailable so callers can distinguish "unknown" from zero tokens.
func Capture(in CaptureInput) (*Record, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rec := &Record{
		Instance:   strings.TrimSpace(in.Instance),
		Agent:      strings.TrimSpace(in.Agent),
		Runtime:    strings.TrimSpace(in.Runtime),
		StartedAt:  utcOrZero(in.StartedAt),
		EndedAt:    utcOrZero(in.EndedAt),
		CapturedAt: now.UTC(),
		Source:     strings.TrimSpace(in.LogPath),
	}
	if rec.Runtime == "" {
		rec.Runtime = "claude"
	}
	if !rec.StartedAt.IsZero() && !rec.EndedAt.IsZero() && !rec.EndedAt.Before(rec.StartedAt) {
		rec.DurationMS = rec.EndedAt.Sub(rec.StartedAt).Milliseconds()
	}

	switch strings.ToLower(rec.Runtime) {
	case "codex":
		if err := fillCodexUsageFromJSONL(rec, in.LogPath); err != nil {
			return rec, err
		}
	default:
		// Non-Codex logs do not expose reliable token counts in this daemon
		// surface. Count the finalized runtime invocation as one turn without
		// inventing token values.
		rec.Turns = 1
		rec.TokensAvailable = false
	}
	return rec, nil
}

func fillCodexUsageFromJSONL(rec *Record, logPath string) error {
	if strings.TrimSpace(logPath) == "" {
		return nil
	}
	f, err := os.Open(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	return ParseCodexJSONL(rec, f)
}

// ParseCodexJSONL adds all Codex turn.completed usage events in r to rec.
func ParseCodexJSONL(rec *Record, r io.Reader) error {
	if rec == nil {
		return errors.New("usage: record is nil")
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event codexEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type != "turn.completed" {
			continue
		}
		rec.Turns++
		if event.Usage == nil {
			continue
		}
		rec.TokensAvailable = true
		rec.InputTokens += int64Value(event.Usage, "input_tokens", "input")
		rec.CachedInputTokens += int64Value(event.Usage, "cached_input_tokens", "cached_tokens", "cache_read_input_tokens")
		rec.OutputTokens += int64Value(event.Usage, "output_tokens", "output")
		rec.ReasoningOutputTokens += int64Value(event.Usage, "reasoning_output_tokens", "reasoning_tokens", "reasoning")
	}
	return scanner.Err()
}

type codexEvent struct {
	Type  string         `json:"type"`
	Usage map[string]any `json:"usage"`
}

func int64Value(values map[string]any, keys ...string) int64 {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		case json.Number:
			n, _ := v.Int64()
			return n
		}
	}
	return 0
}

// MergeRecord adds or replaces a record by its stable run key, then recomputes
// the materialized summary. It returns true when the job usage changed.
func MergeRecord(current *JobUsage, rec Record) (*JobUsage, bool) {
	if !RecordUseful(rec) {
		return current, false
	}
	if current == nil {
		current = &JobUsage{}
	}
	before := current.clone()
	key := RecordKey(rec)
	replaced := false
	for i := range current.Records {
		if RecordKey(current.Records[i]) == key {
			current.Records[i] = rec
			replaced = true
			break
		}
	}
	if !replaced {
		current.Records = append(current.Records, rec)
	}
	sort.SliceStable(current.Records, func(i, j int) bool {
		return recordLess(current.Records[i], current.Records[j])
	})
	current.Summary = Summarize(current.Records)
	return current, !jobUsageEqual(before, current)
}

func (u *JobUsage) clone() *JobUsage {
	if u == nil {
		return nil
	}
	out := *u
	out.Records = append([]Record(nil), u.Records...)
	return &out
}

// RecordUseful reports whether a record contains enough data to persist.
func RecordUseful(rec Record) bool {
	return strings.TrimSpace(rec.Instance) != "" ||
		rec.Turns > 0 ||
		rec.DurationMS > 0 ||
		rec.TokensAvailable
}

// RecordKey identifies the same finalized run across repeated reconciliation
// attempts.
func RecordKey(rec Record) string {
	instance := strings.TrimSpace(rec.Instance)
	if instance == "" {
		instance = "-"
	}
	if !rec.StartedAt.IsZero() {
		return instance + "|" + rec.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	return instance
}

func recordLess(a, b Record) bool {
	if !a.StartedAt.Equal(b.StartedAt) {
		return a.StartedAt.Before(b.StartedAt)
	}
	return RecordKey(a) < RecordKey(b)
}

// Summarize aggregates records.
func Summarize(records []Record) Summary {
	var out Summary
	for _, rec := range records {
		if !RecordUseful(rec) {
			continue
		}
		out.Runs++
		if rec.TokensAvailable {
			out.TokenAvailableRuns++
			out.InputTokens += rec.InputTokens
			out.CachedInputTokens += rec.CachedInputTokens
			out.OutputTokens += rec.OutputTokens
			out.ReasoningOutputTokens += rec.ReasoningOutputTokens
		} else {
			out.TokenUnavailableRuns++
		}
		out.Turns += rec.Turns
		out.DurationMS += rec.DurationMS
	}
	return out
}

// ValidateRecord checks persisted usage invariants.
func ValidateRecord(rec Record) error {
	if rec.InputTokens < 0 ||
		rec.CachedInputTokens < 0 ||
		rec.OutputTokens < 0 ||
		rec.ReasoningOutputTokens < 0 ||
		rec.Turns < 0 ||
		rec.DurationMS < 0 {
		return errors.New("usage values must be non-negative")
	}
	if !rec.StartedAt.IsZero() && !rec.EndedAt.IsZero() && rec.EndedAt.Before(rec.StartedAt) {
		return errors.New("usage ended_at must not be before started_at")
	}
	return nil
}

// ValidateJobUsage checks a job usage ledger and summary.
func ValidateJobUsage(u *JobUsage) error {
	if u == nil {
		return nil
	}
	seen := map[string]bool{}
	for i, rec := range u.Records {
		if err := ValidateRecord(rec); err != nil {
			return fmt.Errorf("usage.records[%d]: %w", i, err)
		}
		key := RecordKey(rec)
		if seen[key] {
			return fmt.Errorf("usage.records[%d]: duplicate record key %q", i, key)
		}
		seen[key] = true
	}
	want := Summarize(u.Records)
	if u.Summary != want {
		return fmt.Errorf("usage.summary does not match records")
	}
	return nil
}

func jobUsageEqual(a, b *JobUsage) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Summary != b.Summary || len(a.Records) != len(b.Records) {
		return false
	}
	for i := range a.Records {
		if a.Records[i] != b.Records[i] {
			return false
		}
	}
	return true
}

func utcOrZero(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}
