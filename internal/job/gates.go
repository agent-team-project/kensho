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
	"regexp"
	"sort"
	"strings"
	"time"
)

// GateStatus is the explicit outcome reported by the agent that ran a gate.
type GateStatus string

const (
	GateStatusPass GateStatus = "pass"
	GateStatusFail GateStatus = "fail"
)

const (
	GateClassInfra   = "infra"
	GateClassContent = "content"
)

// GateRecord is one append-only per-job gate result.
type GateRecord struct {
	TS        time.Time  `json:"ts"`
	JobID     string     `json:"job_id"`
	Attempt   int        `json:"attempt,omitempty"`
	Step      string     `json:"step,omitempty"`
	Commit    string     `json:"commit,omitempty"`
	Name      string     `json:"name"`
	Status    GateStatus `json:"status"`
	Signature string     `json:"signature,omitempty"`
	LogRef    string     `json:"log_ref,omitempty"`
	Actor     string     `json:"actor,omitempty"`
}

// GateSignatureMatcher is one compiled pipeline infra signature.
type GateSignatureMatcher struct {
	Name    string
	Pattern string
	Re      *regexp.Regexp
}

// GateClassification is the infra/content classification for a gate result.
type GateClassification struct {
	Class            string
	MatchedSignature string
	MatchedPattern   string
}

// GateSignatureTestResult is one dry-run match result for a log file.
type GateSignatureTestResult struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	Matched bool   `json:"matched"`
	Excerpt string `json:"excerpt,omitempty"`
}

// GatePath returns the JSONL gate-result path for a job id.
func GatePath(teamDir, rawID string) string {
	id := IDFromInput(rawID)
	return filepath.Join(Directory(teamDir), id+".gates.jsonl")
}

// ValidGateStatus reports whether status is a supported gate outcome.
func ValidGateStatus(status GateStatus) bool {
	switch status {
	case GateStatusPass, GateStatusFail:
		return true
	default:
		return false
	}
}

// ParseGateStatus validates a gate status string.
func ParseGateStatus(raw string) (GateStatus, error) {
	status := GateStatus(strings.ToLower(strings.TrimSpace(raw)))
	if !ValidGateStatus(status) {
		return "", fmt.Errorf("unknown gate status %q", raw)
	}
	return status, nil
}

// CompileGateSignatureMatchers compiles named infra signature regexes in a
// stable order so classification is deterministic.
func CompileGateSignatureMatchers(signatures map[string]string) ([]GateSignatureMatcher, error) {
	if len(signatures) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(signatures))
	for name := range signatures {
		names = append(names, name)
	}
	sort.Strings(names)

	matchers := make([]GateSignatureMatcher, 0, len(names))
	for _, name := range names {
		cleanName := strings.TrimSpace(name)
		if cleanName == "" {
			return nil, errors.New("infra_signatures: name must be non-empty")
		}
		pattern := strings.TrimSpace(signatures[name])
		if pattern == "" {
			return nil, fmt.Errorf("infra_signatures.%s: pattern must be non-empty", cleanName)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("infra_signatures.%s: invalid regex: %w", cleanName, err)
		}
		matchers = append(matchers, GateSignatureMatcher{Name: cleanName, Pattern: pattern, Re: re})
	}
	return matchers, nil
}

// ClassifyGateRecord classifies explicit failed gate signatures. Passing gates
// have no class; failed gates with no matching infra signature are content.
func ClassifyGateRecord(matchers []GateSignatureMatcher, record GateRecord) GateClassification {
	if record.Status != GateStatusFail {
		return GateClassification{}
	}
	signature := strings.TrimSpace(record.Signature)
	if signature == "" {
		return GateClassification{Class: GateClassContent}
	}
	for _, matcher := range matchers {
		if matcher.Re != nil && matcher.Re.MatchString(signature) {
			return GateClassification{
				Class:            GateClassInfra,
				MatchedSignature: matcher.Name,
				MatchedPattern:   matcher.Pattern,
			}
		}
	}
	return GateClassification{Class: GateClassContent}
}

// TestGateSignatureMatchers dry-runs infra signatures against a log body.
func TestGateSignatureMatchers(matchers []GateSignatureMatcher, log string) []GateSignatureTestResult {
	if len(matchers) == 0 {
		return nil
	}
	out := make([]GateSignatureTestResult, 0, len(matchers))
	for _, matcher := range matchers {
		result := GateSignatureTestResult{Name: matcher.Name, Pattern: matcher.Pattern}
		if matcher.Re != nil {
			if loc := matcher.Re.FindStringIndex(log); len(loc) == 2 {
				result.Matched = true
				result.Excerpt = gateSignatureExcerpt(log, loc[0], loc[1])
			}
		}
		out = append(out, result)
	}
	return out
}

func gateSignatureExcerpt(log string, start, end int) string {
	if start < 0 || end < start || end > len(log) {
		return ""
	}
	excerpt := strings.TrimSpace(log[start:end])
	const maxExcerpt = 180
	if len(excerpt) <= maxExcerpt {
		return excerpt
	}
	return excerpt[:maxExcerpt] + "..."
}

// AppendGateRecord appends one JSONL gate result for a job.
func AppendGateRecord(teamDir string, record *GateRecord) error {
	if record == nil {
		return errors.New("job gate record is nil")
	}
	record.JobID = IDFromInput(record.JobID)
	if record.JobID == "" {
		return errors.New("job gate record job_id is required")
	}
	record.Name = strings.TrimSpace(record.Name)
	if record.Name == "" {
		return errors.New("job gate record name is required")
	}
	if !ValidGateStatus(record.Status) {
		return fmt.Errorf("job gate record status %q is invalid", record.Status)
	}
	if record.Attempt < 0 {
		return errors.New("job gate record attempt must be >= 0")
	}
	if record.TS.IsZero() {
		record.TS = time.Now().UTC()
	} else {
		record.TS = record.TS.UTC()
	}
	record.Signature = strings.TrimSpace(record.Signature)
	record.LogRef = strings.TrimSpace(record.LogRef)
	record.Actor = strings.TrimSpace(record.Actor)
	record.Step = strings.TrimSpace(record.Step)
	record.Commit = strings.TrimSpace(record.Commit)

	dir := Directory(teamDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("job gate: mkdir: %w", err)
	}
	f, err := os.OpenFile(GatePath(teamDir, record.JobID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("job gate: open: %w", err)
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(record); err != nil {
		_ = f.Close()
		return fmt.Errorf("job gate: encode: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("job gate: close: %w", err)
	}
	return nil
}

// ListGateRecords reads a job gate log. A missing log returns an empty slice.
func ListGateRecords(teamDir, rawID string) ([]GateRecord, error) {
	records, err := listLiveGateRecords(teamDir, rawID)
	if err == nil {
		return records, nil
	}
	if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		return nil, err
	}
	return ArchivedGates(teamDir, rawID)
}

func listLiveGateRecords(teamDir, rawID string) ([]GateRecord, error) {
	id := IDFromInput(rawID)
	if id == "" {
		return nil, fmt.Errorf("job id %q produced an empty normalized id", rawID)
	}
	f, err := os.Open(GatePath(teamDir, id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	records, err := readGateRecords(f)
	if err != nil {
		return nil, fmt.Errorf("job gates %s: %w", id, err)
	}
	return records, nil
}

func readGateRecords(r io.Reader) ([]GateRecord, error) {
	scanner := bufio.NewScanner(r)
	var records []GateRecord
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var record GateRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		record.JobID = IDFromInput(record.JobID)
		record.Name = strings.TrimSpace(record.Name)
		record.Step = strings.TrimSpace(record.Step)
		record.Commit = strings.TrimSpace(record.Commit)
		record.Signature = strings.TrimSpace(record.Signature)
		record.LogRef = strings.TrimSpace(record.LogRef)
		record.Actor = strings.TrimSpace(record.Actor)
		if record.JobID == "" {
			return nil, fmt.Errorf("line %d: job_id is required", line)
		}
		if record.Name == "" {
			return nil, fmt.Errorf("line %d: name is required", line)
		}
		if !ValidGateStatus(record.Status) {
			return nil, fmt.Errorf("line %d: invalid status %q", line, record.Status)
		}
		if record.Attempt < 0 {
			return nil, fmt.Errorf("line %d: attempt must be >= 0", line)
		}
		if !record.TS.IsZero() {
			record.TS = record.TS.UTC()
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// LatestGateRecords folds a gate history so the latest record per gate name wins.
func LatestGateRecords(records []GateRecord) []GateRecord {
	if len(records) == 0 {
		return nil
	}
	latest := map[string]GateRecord{}
	for _, record := range records {
		if strings.TrimSpace(record.Name) == "" {
			continue
		}
		latest[record.Name] = record
	}
	out := make([]GateRecord, 0, len(latest))
	for _, record := range latest {
		out = append(out, record)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// LatestGateRecordsForAttempt returns the latest record per gate name from one
// implementation generation. Legacy attempt-zero records belong to attempt one.
func LatestGateRecordsForAttempt(records []GateRecord, attempt int) []GateRecord {
	if attempt <= 0 {
		attempt = 1
	}
	filtered := make([]GateRecord, 0, len(records))
	for _, record := range records {
		recordAttempt := record.Attempt
		if recordAttempt <= 0 {
			recordAttempt = 1
		}
		if recordAttempt == attempt {
			filtered = append(filtered, record)
		}
	}
	return LatestGateRecords(filtered)
}

// LatestGateRecordsForAttemptHead isolates evidence to one implementation
// generation before folding it. Gate names are step-scoped because verifier and
// reviewer steps may report gates with the same name. A blank head selects all
// records for the attempt for compatibility with headless non-git jobs.
func LatestGateRecordsForAttemptHead(records []GateRecord, attempt int, head string) []GateRecord {
	if attempt <= 0 {
		attempt = 1
	}
	head = strings.TrimSpace(head)
	type gateIdentity struct {
		step string
		name string
	}
	latest := make(map[gateIdentity]GateRecord)
	for _, record := range records {
		name := strings.TrimSpace(record.Name)
		if name == "" {
			continue
		}
		recordAttempt := record.Attempt
		if recordAttempt <= 0 {
			recordAttempt = 1
		}
		if recordAttempt != attempt {
			continue
		}
		if head != "" && strings.TrimSpace(record.Commit) != head {
			continue
		}
		latest[gateIdentity{step: strings.TrimSpace(record.Step), name: name}] = record
	}
	out := make([]GateRecord, 0, len(latest))
	for _, record := range latest {
		out = append(out, record)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Step < out[j].Step
		}
		return out[i].Name < out[j].Name
	})
	return out
}
