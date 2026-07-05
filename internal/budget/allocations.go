package budget

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/origin"
	"github.com/jamesaud/agent-team/internal/topology"
)

const (
	AllocationStatusOutstanding = "outstanding"
	AllocationStatusReleased    = "released"
)

var allocationProcessLocks sync.Map

// AllocationRecord is one durable parent -> child token allowance grant.
// Today the live allocation tree is operator/team -> job/step, but parent and
// child are explicit so future nested teams can reuse the same ledger shape.
type AllocationRecord struct {
	ID             string          `json:"id"`
	Team           string          `json:"team"`
	Parent         string          `json:"parent"`
	Child          string          `json:"child"`
	JobID          string          `json:"job_id,omitempty"`
	StepID         string          `json:"step_id,omitempty"`
	Instance       string          `json:"instance,omitempty"`
	Allocation     string          `json:"allocation"`
	Status         string          `json:"status"`
	Tokens         int64           `json:"tokens"`
	ConsumedTokens int64           `json:"consumed_tokens,omitempty"`
	ReleasedTokens int64           `json:"released_tokens,omitempty"`
	Origin         origin.Envelope `json:"origin,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	ReleasedAt     time.Time       `json:"released_at,omitempty"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// GrantRequest asks the ledger to grant a child token allowance.
type GrantRequest struct {
	Team               string
	JobID              string
	StepID             string
	Instance           string
	Tokens             int64
	ClampOversubscribe bool
	GateOversubscribe  bool
	Now                time.Time
	Origin             origin.Envelope
}

// GrantResult is the atomic grant decision.
type GrantResult struct {
	Allowed         bool
	Noop            bool
	Team            string
	RequestedTokens int64
	GrantedTokens   int64
	Clamped         bool
	Status          TeamStatus
	TokenExhausted  bool
	NextTokenRetry  time.Time
	Allocation      *AllocationRecord
}

type ReleaseRequest struct {
	ID             string
	JobID          string
	StepID         string
	Instance       string
	ConsumedTokens int64
	Now            time.Time
}

func allocationRoot(teamDir string) string {
	return filepath.Join(teamDir, "budget", "allocations")
}

func allocationLockPath(teamDir string) string {
	return filepath.Join(teamDir, "budget", "allocations.lock")
}

func allocationPath(teamDir, id string) string {
	return filepath.Join(allocationRoot(teamDir), id+".json")
}

// ListAllocations reads all durable allocation records.
func ListAllocations(teamDir string) ([]*AllocationRecord, error) {
	dir := allocationRoot(teamDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*AllocationRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		rec, err := readAllocation(teamDir, id)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	sortAllocations(out)
	return out, nil
}

// GrantTokens atomically checks parent headroom and writes an outstanding child
// allocation record. Reserve mode gates on consumed + outstanding + requested;
// oversubscribe mode preserves phase-1 consumption gating and clamps the grant
// to consumed headroom when a token cap exists.
func GrantTokens(teamDir string, top *topology.Topology, req GrantRequest) (GrantResult, error) {
	req.Team = strings.TrimSpace(req.Team)
	req.JobID = jobstore.NormalizeID(req.JobID)
	req.StepID = strings.TrimSpace(req.StepID)
	req.Instance = strings.TrimSpace(req.Instance)
	req.Tokens = maxInt64(req.Tokens, 0)
	if req.Now.IsZero() {
		req.Now = time.Now().UTC()
	} else {
		req.Now = req.Now.UTC()
	}
	out := GrantResult{Allowed: true, Noop: true, Team: req.Team, RequestedTokens: req.Tokens, GrantedTokens: req.Tokens}
	if top == nil || req.Team == "" || req.JobID == "" || req.Tokens <= 0 {
		return out, nil
	}
	b := top.FindBudget(req.Team)
	if b == nil {
		return out, nil
	}
	out.Noop = false
	err := withAllocationLock(teamDir, func() error {
		admission, err := AdmissionForTeamWithRequest(teamDir, top, req.Team, req.JobID, req.Tokens, req.Now)
		if err != nil {
			return err
		}
		out.Status = admission.Status
		out.TokenExhausted = admission.TokenExhausted
		out.NextTokenRetry = admission.NextTokenRetry
		gateExhausted := admission.TokenExhausted && (b.Allocation == topology.BudgetAllocationReserve || req.GateOversubscribe)
		if gateExhausted {
			out.Allowed = false
			out.GrantedTokens = 0
			return nil
		}
		granted := req.Tokens
		if req.ClampOversubscribe && b.Allocation == topology.BudgetAllocationOversubscribe && b.TokensPerDay > 0 {
			remaining := admission.Status.TokensRemaining
			if remaining > 0 && granted > remaining {
				granted = remaining
				out.Clamped = true
			}
		}
		out.GrantedTokens = granted
		if granted <= 0 {
			out.Allowed = false
			out.TokenExhausted = true
			return nil
		}
		rec := &AllocationRecord{
			ID:         newAllocationID(req.Now),
			Team:       req.Team,
			Parent:     "team:" + req.Team,
			Child:      allocationChild(req.JobID, req.StepID, req.Instance),
			JobID:      req.JobID,
			StepID:     req.StepID,
			Instance:   req.Instance,
			Allocation: b.Allocation,
			Status:     AllocationStatusOutstanding,
			Tokens:     granted,
			Origin:     req.Origin,
			CreatedAt:  req.Now,
			UpdatedAt:  req.Now,
		}
		if err := writeAllocation(teamDir, rec); err != nil {
			return err
		}
		out.Allocation = rec
		return nil
	})
	return out, err
}

// ReleaseJobInstanceAllocations releases outstanding allocations owned by a
// completed instance and records how much of that allowance was consumed.
func ReleaseJobInstanceAllocations(teamDir string, j *jobstore.Job, instance string, now time.Time) ([]*AllocationRecord, error) {
	if j == nil {
		return nil, nil
	}
	return ReleaseAllocations(teamDir, ReleaseRequest{
		JobID:          j.ID,
		Instance:       instance,
		ConsumedTokens: consumedTokensForInstance(j, instance),
		Now:            now,
	})
}

// ReleaseAllocations marks matching outstanding records as released.
func ReleaseAllocations(teamDir string, req ReleaseRequest) ([]*AllocationRecord, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.JobID = jobstore.NormalizeID(req.JobID)
	req.StepID = strings.TrimSpace(req.StepID)
	req.Instance = strings.TrimSpace(req.Instance)
	if req.Now.IsZero() {
		req.Now = time.Now().UTC()
	} else {
		req.Now = req.Now.UTC()
	}
	if req.ID == "" && req.JobID == "" {
		return nil, nil
	}
	var released []*AllocationRecord
	err := withAllocationLock(teamDir, func() error {
		records, err := ListAllocations(teamDir)
		if err != nil {
			return err
		}
		remainingConsumed := maxInt64(req.ConsumedTokens, 0)
		for _, rec := range records {
			if !allocationMatchesRelease(rec, req) {
				continue
			}
			consume := minInt64(remainingConsumed, rec.Tokens)
			remainingConsumed -= consume
			rec.Status = AllocationStatusReleased
			rec.ConsumedTokens = consume
			rec.ReleasedTokens = maxInt64(rec.Tokens-consume, 0)
			rec.ReleasedAt = req.Now
			rec.UpdatedAt = req.Now
			if err := writeAllocation(teamDir, rec); err != nil {
				return err
			}
			copyRec := *rec
			released = append(released, &copyRec)
		}
		return nil
	})
	return released, err
}

func outstandingTokens(records []*AllocationRecord, team string) int64 {
	team = strings.TrimSpace(team)
	var total int64
	for _, rec := range records {
		if rec == nil || rec.Team != team || rec.Status != AllocationStatusOutstanding {
			continue
		}
		if rec.Tokens > 0 {
			total += rec.Tokens
		}
	}
	return total
}

func consumedTokensForInstance(j *jobstore.Job, instance string) int64 {
	instance = strings.TrimSpace(instance)
	if j == nil || j.Usage == nil {
		return 0
	}
	var total int64
	for _, rec := range j.Usage.Records {
		if instance != "" && strings.TrimSpace(rec.Instance) != instance {
			continue
		}
		if rec.TokensAvailable {
			total += rec.InputTokens + rec.OutputTokens
		}
	}
	return total
}

func allocationMatchesRelease(rec *AllocationRecord, req ReleaseRequest) bool {
	if rec == nil || rec.Status != AllocationStatusOutstanding {
		return false
	}
	if req.ID != "" {
		return rec.ID == req.ID
	}
	if jobstore.NormalizeID(rec.JobID) != req.JobID {
		return false
	}
	if req.StepID != "" && strings.TrimSpace(rec.StepID) != req.StepID {
		return false
	}
	if req.Instance != "" && strings.TrimSpace(rec.Instance) != req.Instance {
		return false
	}
	return true
}

func readAllocation(teamDir, id string) (*AllocationRecord, error) {
	body, err := os.ReadFile(allocationPath(teamDir, id))
	if err != nil {
		return nil, err
	}
	var rec AllocationRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, fmt.Errorf("budget allocation %s: %w", id, err)
	}
	if rec.ID == "" {
		rec.ID = id
	}
	if err := validateAllocation(&rec); err != nil {
		return nil, fmt.Errorf("budget allocation %s: %w", id, err)
	}
	return &rec, nil
}

func writeAllocation(teamDir string, rec *AllocationRecord) error {
	if err := validateAllocation(rec); err != nil {
		return err
	}
	dir := allocationRoot(teamDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("budget allocation: mkdir: %w", err)
	}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("budget allocation: marshal: %w", err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, rec.ID+"-*.json.tmp")
	if err != nil {
		return fmt.Errorf("budget allocation: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("budget allocation: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("budget allocation: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("budget allocation: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), allocationPath(teamDir, rec.ID)); err != nil {
		return fmt.Errorf("budget allocation: rename: %w", err)
	}
	return nil
}

func validateAllocation(rec *AllocationRecord) error {
	if rec == nil {
		return errors.New("budget allocation: nil record")
	}
	if strings.TrimSpace(rec.ID) == "" {
		return errors.New("budget allocation: id is required")
	}
	if strings.ContainsAny(rec.ID, `/\`) || rec.ID == "." || rec.ID == ".." || strings.Contains(rec.ID, "..") {
		return errors.New("budget allocation: id must not contain path segments")
	}
	if strings.TrimSpace(rec.Team) == "" {
		return errors.New("budget allocation: team is required")
	}
	if strings.TrimSpace(rec.Parent) == "" {
		return errors.New("budget allocation: parent is required")
	}
	if strings.TrimSpace(rec.Child) == "" {
		return errors.New("budget allocation: child is required")
	}
	if rec.Tokens <= 0 {
		return errors.New("budget allocation: tokens must be > 0")
	}
	allocation, err := topology.NormalizeBudgetAllocation(rec.Allocation)
	if err != nil {
		return fmt.Errorf("budget allocation: %w", err)
	}
	rec.Allocation = allocation
	switch rec.Status {
	case AllocationStatusOutstanding, AllocationStatusReleased:
	default:
		return fmt.Errorf("budget allocation: unknown status %q", rec.Status)
	}
	if rec.CreatedAt.IsZero() {
		return errors.New("budget allocation: created_at is required")
	}
	if rec.UpdatedAt.IsZero() {
		return errors.New("budget allocation: updated_at is required")
	}
	return nil
}

func withAllocationLock(teamDir string, fn func() error) error {
	if strings.TrimSpace(teamDir) == "" {
		return errors.New("budget allocation: team dir is required")
	}
	lockPath := allocationLockPath(teamDir)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("budget allocation: lock mkdir: %w", err)
	}
	processLockValue, _ := allocationProcessLocks.LoadOrStore(lockPath, &sync.Mutex{})
	processLock := processLockValue.(*sync.Mutex)
	processLock.Lock()
	defer processLock.Unlock()
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("budget allocation: lock open: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("budget allocation: lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func newAllocationID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%d-%s", now.UnixNano(), hex.EncodeToString(b[:]))
}

func allocationChild(jobID, stepID, instance string) string {
	jobID = jobstore.NormalizeID(jobID)
	stepID = strings.TrimSpace(stepID)
	instance = strings.TrimSpace(instance)
	child := "job:" + jobID
	if stepID != "" {
		child += "/step:" + stepID
	}
	if instance != "" {
		child += "/instance:" + instance
	}
	return child
}

func sortAllocations(records []*AllocationRecord) {
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.Before(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
