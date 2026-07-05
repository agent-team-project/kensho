package job

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/jamesaud/agent-team/internal/allowance"
	"github.com/jamesaud/agent-team/internal/mergepolicy"
	"github.com/jamesaud/agent-team/internal/origin"
	"github.com/jamesaud/agent-team/internal/usage"
	"github.com/jamesaud/agent-team/internal/worktreepolicy"
)

var ticketIdentifierPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([a-z][a-z0-9]{1,9}-[0-9]+)(?:$|[^a-z0-9])`)

// Status is the durable lifecycle state of a work unit.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Supported job kinds. Empty kind is the normal delivery workflow.
const (
	KindProbe = "probe"
)

// ProbeSkipReason is recorded on pipeline steps intentionally bypassed for
// report-only probe jobs.
const ProbeSkipReason = "probe profile skips delivery review and gates"

// Supported pipeline step gates.
const (
	StepGateManual = "manual"
	StepGatePR     = "pr"
)

// Job is one durable work unit under `.agent_team/jobs/<id>.toml`.
type Job struct {
	ID                     string          `toml:"id"`
	Ticket                 string          `toml:"ticket"`
	TicketURL              string          `toml:"ticket_url,omitempty"`
	Target                 string          `toml:"target"`
	Kickoff                string          `toml:"kickoff,omitempty"`
	Kind                   string          `toml:"kind,omitempty"`
	Instance               string          `toml:"instance,omitempty"`
	Pipeline               string          `toml:"pipeline,omitempty"`
	Status                 Status          `toml:"status"`
	Held                   bool            `toml:"held,omitempty"`
	HoldReason             string          `toml:"hold_reason,omitempty"`
	HoldUntil              time.Time       `toml:"hold_until,omitempty"`
	Branch                 string          `toml:"branch,omitempty"`
	Worktree               string          `toml:"worktree,omitempty"`
	ReapWorktree           string          `toml:"reap_worktree,omitempty"`
	PR                     string          `toml:"pr,omitempty"`
	Origin                 origin.Envelope `toml:"origin,omitempty"`
	LinearAttentionWritten bool            `toml:"linear_attention_written,omitempty"`
	Merge                  *Merge          `toml:"merge,omitempty"`
	Drift                  *Drift          `toml:"drift,omitempty"`
	LastEvent              string          `toml:"last_event,omitempty"`
	LastStatus             string          `toml:"last_status,omitempty"`
	CreatedAt              time.Time       `toml:"created_at"`
	UpdatedAt              time.Time       `toml:"updated_at"`
	Usage                  *usage.JobUsage `toml:"usage,omitempty"`
	TokenBudget            int64           `toml:"token_budget,omitempty"`
	TimeBudget             string          `toml:"time_budget,omitempty"`
	ReminderLevels         []int           `toml:"reminder_levels,omitempty"`
	TokenBudgetNotices     []int           `toml:"token_budget_notices,omitempty"`
	TimeBudgetNotices      []int           `toml:"time_budget_notices,omitempty"`
	Steps                  []Step          `toml:"steps,omitempty"`
}

// Merge stores the mechanical merge strategy and final PR landing mode captured
// for a durable job.
type Merge struct {
	Strategy   string   `toml:"strategy"`
	Script     string   `toml:"script,omitempty"`
	Land       string   `toml:"land,omitempty"`
	OwnedPaths []string `toml:"owned_paths,omitempty"`
}

// Drift stores the last base-drift classification computed for a job.
type Drift struct {
	Classification string    `toml:"classification"`
	Base           string    `toml:"base,omitempty"`
	Head           string    `toml:"head,omitempty"`
	Files          []string  `toml:"files,omitempty"`
	UpdatedAt      time.Time `toml:"updated_at,omitempty"`
}

// Step is a pipeline step snapshot recorded on a job.
type Step struct {
	ID                 string         `toml:"id"`
	Label              string         `toml:"label,omitempty"`
	Description        string         `toml:"description,omitempty"`
	Instructions       string         `toml:"instructions,omitempty"`
	Target             string         `toml:"target"`
	Workspace          string         `toml:"workspace,omitempty"`
	Runtime            string         `toml:"runtime,omitempty"`
	RuntimeBin         string         `toml:"runtime_bin,omitempty"`
	Status             Status         `toml:"status"`
	Instance           string         `toml:"instance,omitempty"`
	After              []string       `toml:"after,omitempty"`
	Gate               string         `toml:"gate,omitempty"`
	ApprovalRequired   bool           `toml:"approval_required,omitempty"`
	ApprovalID         string         `toml:"approval_id,omitempty"`
	ApprovalStatus     ApprovalStatus `toml:"approval_status,omitempty"`
	Optional           bool           `toml:"optional,omitempty"`
	Timeout            string         `toml:"timeout,omitempty"`
	Attempts           int            `toml:"attempts,omitempty"`
	MaxAttempts        int            `toml:"max_attempts,omitempty"`
	RetryOnCrash       bool           `toml:"retry_on_crash,omitempty"`
	Skipped            bool           `toml:"skipped,omitempty"`
	SkipReason         string         `toml:"skip_reason,omitempty"`
	QueueReason        string         `toml:"queue_reason,omitempty"`
	QueuedAt           time.Time      `toml:"queued_at,omitempty"`
	RunningAt          time.Time      `toml:"running_at,omitempty"`
	StartedAt          time.Time      `toml:"started_at,omitempty"`
	FinishedAt         time.Time      `toml:"finished_at,omitempty"`
	TokenBudget        int64          `toml:"token_budget,omitempty"`
	TimeBudget         string         `toml:"time_budget,omitempty"`
	ReminderLevels     []int          `toml:"reminder_levels,omitempty"`
	TokenBudgetNotices []int          `toml:"token_budget_notices,omitempty"`
	TimeBudgetNotices  []int          `toml:"time_budget_notices,omitempty"`
}

// StepDispatchKickoff combines a job-level kickoff with optional step-specific
// instructions for the payload sent to the target runtime.
func StepDispatchKickoff(jobKickoff, stepID, instructions string) string {
	jobKickoff = strings.TrimSpace(jobKickoff)
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return jobKickoff
	}
	heading := "--- pipeline step instructions"
	if stepID = strings.TrimSpace(stepID); stepID != "" {
		heading += " (" + stepID + ")"
	}
	heading += " ---"
	if jobKickoff == "" {
		return heading + "\n\n" + instructions
	}
	return jobKickoff + "\n\n" + heading + "\n\n" + instructions
}

// Directory returns the jobs directory for a team root.
func Directory(teamDir string) string {
	return filepath.Join(teamDir, "jobs")
}

// Path returns the TOML path for id. The caller should pass a normalized id.
func Path(teamDir, id string) string {
	return filepath.Join(Directory(teamDir), id+".toml")
}

// NormalizeID turns a ticket or user-supplied id into the canonical filename id.
func NormalizeID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// IDFromInput returns the normalized durable job id implied by raw input.
// It accepts either a plain job/ticket id or a ticket URL containing an id.
func IDFromInput(raw string) string {
	ticket, _ := TicketIdentity(raw)
	return NormalizeID(ticket)
}

// TicketIdentity returns the display ticket and canonical URL implied by raw.
// Plain ticket identifiers are returned unchanged. URL input keeps the URL in
// ticket_url and, when possible, extracts identifiers like SQU-42 for the
// durable ticket/id fields.
func TicketIdentity(raw string) (ticket, ticketURL string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if !looksLikeTicketURL(raw) {
		return raw, ""
	}
	ticketURL = raw
	if id := ExtractTicketIdentifier(raw); id != "" {
		return id, ticketURL
	}
	return raw, ticketURL
}

// ExtractTicketIdentifier finds ticket identifiers such as SQU-42 in free text.
func ExtractTicketIdentifier(raw string) string {
	matches := ticketIdentifierPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(matches) < 2 {
		return ""
	}
	return strings.ToUpper(matches[1])
}

func looksLikeTicketURL(raw string) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// ValidStatus reports whether s is a supported job lifecycle state.
func ValidStatus(s Status) bool {
	switch s {
	case StatusQueued, StatusRunning, StatusBlocked, StatusDone, StatusFailed:
		return true
	default:
		return false
	}
}

// ParseStatus validates a status string.
func ParseStatus(raw string) (Status, error) {
	s := Status(strings.ToLower(strings.TrimSpace(raw)))
	if !ValidStatus(s) {
		return "", fmt.Errorf("unknown job status %q", raw)
	}
	return s, nil
}

// NormalizeKind validates a job kind/profile and returns the persisted value.
func NormalizeKind(raw string) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(raw))
	switch kind {
	case "", "default":
		return "", nil
	case KindProbe:
		return KindProbe, nil
	default:
		return "", fmt.Errorf("unknown job profile %q", raw)
	}
}

// IsProbe reports whether kind selects the reduced report-only probe contract.
func IsProbe(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), KindProbe)
}

// New builds a queued job with normalized defaults.
func New(ticket, target, kickoff string, now time.Time) (*Job, error) {
	ticket, ticketURL := TicketIdentity(ticket)
	target = strings.TrimSpace(target)
	kickoff = strings.TrimSpace(kickoff)
	if ticket == "" {
		return nil, errors.New("ticket is required")
	}
	if target == "" {
		return nil, errors.New("target is required")
	}
	id := IDFromInput(ticket)
	if id == "" {
		return nil, fmt.Errorf("ticket %q produced an empty job id", ticket)
	}
	now = now.UTC()
	return &Job{
		ID:        id,
		Ticket:    ticket,
		TicketURL: ticketURL,
		Target:    target,
		Kickoff:   kickoff,
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Validate checks the persisted job invariants.
func Validate(j *Job) error {
	if j == nil {
		return errors.New("job is nil")
	}
	if strings.TrimSpace(j.ID) == "" {
		return errors.New("job id is required")
	}
	if normalized := NormalizeID(j.ID); normalized != j.ID {
		return fmt.Errorf("job id %q must be normalized as %q", j.ID, normalized)
	}
	if strings.TrimSpace(j.Ticket) == "" {
		return errors.New("ticket is required")
	}
	if strings.TrimSpace(j.Target) == "" {
		return errors.New("target is required")
	}
	kind, err := NormalizeKind(j.Kind)
	if err != nil {
		return err
	}
	if j.Kind != kind {
		return fmt.Errorf("job kind %q must be normalized as %q", j.Kind, kind)
	}
	if !ValidStatus(j.Status) {
		return fmt.Errorf("unknown job status %q", j.Status)
	}
	if !worktreepolicy.Valid(j.ReapWorktree) {
		return fmt.Errorf("reap_worktree must be on_close, on_merge, or never")
	}
	if err := validateMerge(j.Merge); err != nil {
		return err
	}
	if err := validateDrift(j.Drift); err != nil {
		return err
	}
	if err := usage.ValidateJobUsage(j.Usage); err != nil {
		return err
	}
	if err := validateBudgetFields("job", j.TokenBudget, j.TimeBudget, j.ReminderLevels, j.TokenBudgetNotices, j.TimeBudgetNotices); err != nil {
		return err
	}
	if j.CreatedAt.IsZero() {
		return errors.New("created_at is required")
	}
	if j.UpdatedAt.IsZero() {
		return errors.New("updated_at is required")
	}
	seenSteps := map[string]bool{}
	for i, step := range j.Steps {
		if strings.TrimSpace(step.ID) == "" {
			return fmt.Errorf("steps[%d]: id is required", i)
		}
		if seenSteps[step.ID] {
			return fmt.Errorf("steps[%d]: duplicate id %q", i, step.ID)
		}
		seenSteps[step.ID] = true
		if strings.TrimSpace(step.Target) == "" {
			return fmt.Errorf("steps[%d]: target is required", i)
		}
		if !ValidStepWorkspace(step.Workspace) {
			return fmt.Errorf("steps[%d]: workspace must be auto, worktree, or repo", i)
		}
		if !ValidStepRuntime(step.Runtime) {
			return fmt.Errorf("steps[%d]: runtime must be claude or codex", i)
		}
		if !ValidStatus(step.Status) {
			return fmt.Errorf("steps[%d]: unknown status %q", i, step.Status)
		}
		if !ValidStepGate(step.Gate) {
			return fmt.Errorf("steps[%d]: unknown gate %q", i, step.Gate)
		}
		if step.ApprovalRequired && step.Gate != StepGateManual {
			return fmt.Errorf("steps[%d]: approval_required is only valid with gate %q", i, StepGateManual)
		}
		if approvalID := strings.TrimSpace(step.ApprovalID); approvalID != "" {
			if !step.ApprovalRequired {
				return fmt.Errorf("steps[%d]: approval_id requires approval_required", i)
			}
			if normalized := NormalizeApprovalID(approvalID); normalized != approvalID {
				return fmt.Errorf("steps[%d]: approval_id %q must be normalized as %q", i, approvalID, normalized)
			}
		}
		if step.ApprovalStatus != "" {
			if !step.ApprovalRequired {
				return fmt.Errorf("steps[%d]: approval_status requires approval_required", i)
			}
			if !ValidApprovalStatus(step.ApprovalStatus) {
				return fmt.Errorf("steps[%d]: unknown approval_status %q", i, step.ApprovalStatus)
			}
			if strings.TrimSpace(step.ApprovalID) == "" {
				return fmt.Errorf("steps[%d]: approval_status requires approval_id", i)
			}
		}
		if timeout := strings.TrimSpace(step.Timeout); timeout != "" {
			duration, err := time.ParseDuration(timeout)
			if err != nil {
				return fmt.Errorf("steps[%d]: invalid timeout %q: %w", i, step.Timeout, err)
			}
			if duration <= 0 {
				return fmt.Errorf("steps[%d]: timeout must be greater than zero", i)
			}
		}
		if step.Attempts < 0 {
			return fmt.Errorf("steps[%d]: attempts must be >= 0", i)
		}
		if step.MaxAttempts < 0 {
			return fmt.Errorf("steps[%d]: max_attempts must be >= 0", i)
		}
		if step.Skipped && step.Status != StatusDone {
			return fmt.Errorf("steps[%d]: skipped steps must have status %q", i, StatusDone)
		}
		if err := validateBudgetFields(fmt.Sprintf("steps[%d]", i), step.TokenBudget, step.TimeBudget, step.ReminderLevels, step.TokenBudgetNotices, step.TimeBudgetNotices); err != nil {
			return err
		}
	}
	return nil
}

func validateBudgetFields(prefix string, tokenBudget int64, timeBudget string, reminderLevels, tokenBudgetNotices, timeBudgetNotices []int) error {
	if tokenBudget < 0 {
		return fmt.Errorf("%s: token_budget must be >= 0", prefix)
	}
	if strings.TrimSpace(timeBudget) != "" {
		duration, err := time.ParseDuration(timeBudget)
		if err != nil {
			return fmt.Errorf("%s: invalid time_budget %q: %w", prefix, timeBudget, err)
		}
		if duration <= 0 {
			return fmt.Errorf("%s: time_budget must be greater than zero", prefix)
		}
	}
	if len(reminderLevels) > 0 {
		if _, err := allowance.NormalizeReminderLevels(reminderLevels); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	if len(tokenBudgetNotices) > 0 {
		if _, err := allowance.NormalizeReminderLevels(tokenBudgetNotices); err != nil {
			return fmt.Errorf("%s: token_budget_notices %w", prefix, err)
		}
	}
	if len(timeBudgetNotices) > 0 {
		if _, err := allowance.NormalizeReminderLevels(timeBudgetNotices); err != nil {
			return fmt.Errorf("%s: time_budget_notices %w", prefix, err)
		}
	}
	return nil
}

func validateMerge(merge *Merge) error {
	if merge == nil {
		return nil
	}
	strategy, err := mergepolicy.NormalizeStrategy(merge.Strategy)
	if err != nil {
		return fmt.Errorf("merge: %w", err)
	}
	if strategy != merge.Strategy {
		return fmt.Errorf("merge: strategy %q must be normalized as %q", merge.Strategy, strategy)
	}
	land, err := mergepolicy.NormalizeLand(merge.Land)
	if err != nil {
		return fmt.Errorf("merge: %w", err)
	}
	if strings.TrimSpace(merge.Land) != "" && land != merge.Land {
		return fmt.Errorf("merge: land %q must be normalized as %q", merge.Land, land)
	}
	script := strings.TrimSpace(merge.Script)
	switch strategy {
	case mergepolicy.StrategyScript:
		if script == "" {
			return errors.New("merge: script is required when strategy is script")
		}
	default:
		if script != "" {
			return errors.New("merge: script is only valid when strategy is script")
		}
	}
	for i, p := range merge.OwnedPaths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("merge: owned_paths[%d] must be non-empty", i)
		}
		if strings.HasPrefix(strings.TrimSpace(p), "/") {
			return fmt.Errorf("merge: owned_paths[%d] must be repo-relative", i)
		}
	}
	return nil
}

func validateDrift(drift *Drift) error {
	if drift == nil {
		return nil
	}
	if !mergepolicy.ValidDrift(drift.Classification) {
		return fmt.Errorf("drift: unknown classification %q", drift.Classification)
	}
	if strings.TrimSpace(drift.Classification) == "" {
		return errors.New("drift: classification is required")
	}
	return nil
}

// ValidStepRuntime reports whether a step runtime override is supported.
func ValidStepRuntime(runtime string) bool {
	switch strings.TrimSpace(runtime) {
	case "", "claude", "codex":
		return true
	default:
		return false
	}
}

// ValidStepWorkspace reports whether a step workspace override is supported.
func ValidStepWorkspace(workspace string) bool {
	switch strings.TrimSpace(workspace) {
	case "", "auto", "worktree", "repo":
		return true
	default:
		return false
	}
}

// ValidStepGate reports whether gate is one of the supported pipeline gates.
func ValidStepGate(gate string) bool {
	switch strings.TrimSpace(gate) {
	case "", StepGateManual, StepGatePR:
		return true
	default:
		return false
	}
}

// Read loads a single job by normalized or raw id.
func Read(teamDir, rawID string) (*Job, error) {
	id := IDFromInput(rawID)
	if id == "" {
		return nil, fmt.Errorf("job id %q produced an empty normalized id", rawID)
	}
	var j Job
	if _, err := toml.DecodeFile(Path(teamDir, id), &j); err != nil {
		return nil, err
	}
	if j.ID == "" {
		j.ID = id
	}
	if err := Validate(&j); err != nil {
		return nil, fmt.Errorf("job %s: %w", id, err)
	}
	return &j, nil
}

// Write stores a job atomically.
func Write(teamDir string, j *Job) error {
	if err := Validate(j); err != nil {
		return err
	}
	dir := Directory(teamDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("job: mkdir: %w", err)
	}
	target := Path(teamDir, j.ID)
	tmp, err := os.CreateTemp(dir, j.ID+"-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("job: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(j); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("job: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("job: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("job: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("job: rename: %w", err)
	}
	return nil
}

// List loads all valid job files in deterministic id order.
func List(teamDir string) ([]*Job, error) {
	dir := Directory(teamDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*Job, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".toml")
		j, err := Read(teamDir, id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}
