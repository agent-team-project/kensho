package job

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrNoReconcileMatch means no stored job matched a PR event.
	ErrNoReconcileMatch = errors.New("job reconcile: no matching job")
	// ErrAmbiguousReconcileMatch means multiple jobs matched the same PR event.
	ErrAmbiguousReconcileMatch = errors.New("job reconcile: ambiguous matching jobs")
)

// ReconcileInput is the normalized PR metadata used to update a durable job.
type ReconcileInput struct {
	EventType string `json:"event_type"`
	Source    string `json:"source,omitempty"`
	Action    string `json:"action,omitempty"`
	PR        string `json:"pr,omitempty"`
	PRURL     string `json:"pr_url,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Merged    *bool  `json:"merged,omitempty"`
}

// ReconcileResult describes the job updated from a PR event.
type ReconcileResult struct {
	Job       *Job   `json:"job"`
	MatchedBy string `json:"matched_by"`
	Message   string `json:"message"`
}

// ReconcileInputFromPayload converts a normalized external event payload into
// PR reconciliation input. Unknown value types are ignored except simple
// strings and booleans.
func ReconcileInputFromPayload(eventType string, payload map[string]any) ReconcileInput {
	input := ReconcileInput{
		EventType: strings.TrimSpace(eventType),
		Source:    payloadValueString(payload, "source"),
		Action:    payloadValueString(payload, "action"),
		PR:        payloadValueString(payload, "pr"),
		PRURL:     payloadValueString(payload, "pr_url"),
		Branch:    payloadValueString(payload, "branch"),
	}
	if merged, ok := payloadValueBool(payload, "merged"); ok {
		input.Merged = &merged
	}
	return input
}

// ReconcilePR finds the job that owns a PR/branch and updates its lifecycle.
func ReconcilePR(teamDir string, input ReconcileInput, now time.Time) (*ReconcileResult, error) {
	match, err := MatchPRJob(teamDir, input)
	if err != nil {
		return nil, err
	}
	j := match.Job
	if strings.TrimSpace(input.PRURL) != "" {
		j.PR = strings.TrimSpace(input.PRURL)
	} else if strings.TrimSpace(j.PR) == "" && strings.TrimSpace(input.PR) != "" {
		j.PR = strings.TrimSpace(input.PR)
	}
	if strings.TrimSpace(input.Branch) != "" {
		j.Branch = strings.TrimSpace(input.Branch)
	}
	eventType := strings.TrimSpace(input.EventType)
	if eventType == "" {
		eventType = "pr.reconciled"
	}
	j.LastEvent = eventType
	j.LastStatus = reconcileMessage(input, eventType)
	now = now.UTC()
	j.UpdatedAt = now
	switch {
	case input.IsMerged() || eventType == "pr.merged":
		j.Status = StatusDone
	case eventType == "pr.closed":
		j.Status = StatusFailed
	case strings.HasPrefix(eventType, "pr.") && j.Status == StatusQueued:
		j.Status = StatusRunning
	}
	if err := Write(teamDir, j); err != nil {
		return nil, err
	}
	return &ReconcileResult{Job: j, MatchedBy: match.MatchedBy, Message: j.LastStatus}, nil
}

// IsMerged reports whether the input explicitly represents a merged PR.
func (in ReconcileInput) IsMerged() bool {
	if in.Merged != nil && *in.Merged {
		return true
	}
	return strings.TrimSpace(in.EventType) == "pr.merged"
}

// MatchPRJob returns the unique stored job matching PR URL/number or branch.
func MatchPRJob(teamDir string, input ReconcileInput) (*ReconcileResult, error) {
	jobs, err := List(teamDir)
	if err != nil {
		return nil, err
	}
	var best *Job
	bestScore := 0
	bestBy := ""
	ambiguous := []string{}
	for _, j := range jobs {
		score, by := matchPRJob(j, input)
		if score == 0 {
			continue
		}
		if score > bestScore {
			best = j
			bestScore = score
			bestBy = by
			ambiguous = ambiguous[:0]
			continue
		}
		if score == bestScore && best != nil && j.ID != best.ID {
			ambiguous = append(ambiguous, j.ID)
		}
	}
	if best == nil {
		return nil, ErrNoReconcileMatch
	}
	if len(ambiguous) > 0 {
		ids := append([]string{best.ID}, ambiguous...)
		return nil, fmt.Errorf("%w: %s", ErrAmbiguousReconcileMatch, strings.Join(ids, ", "))
	}
	return &ReconcileResult{Job: best, MatchedBy: bestBy}, nil
}

func matchPRJob(j *Job, input ReconcileInput) (int, string) {
	jobPR := strings.TrimSpace(j.PR)
	prURL := strings.TrimSpace(input.PRURL)
	if jobPR != "" && prURL != "" && canonicalPRURL(jobPR) == canonicalPRURL(prURL) {
		return 4, "pr_url"
	}
	inputPR := reconcilePRNumber(input)
	if jobPR != "" && inputPR != "" {
		if jobPRNumber := canonicalPRNumber(jobPR); jobPRNumber == inputPR {
			return 3, "pr"
		}
		if strings.EqualFold(strings.TrimSpace(jobPR), strings.TrimSpace(input.PR)) {
			return 3, "pr"
		}
	}
	if branch := strings.TrimSpace(input.Branch); branch != "" && strings.TrimSpace(j.Branch) == branch {
		return 2, "branch"
	}
	return 0, ""
}

func reconcileMessage(input ReconcileInput, eventType string) string {
	switch {
	case input.IsMerged() || eventType == "pr.merged":
		return "pull request merged"
	case eventType == "pr.closed":
		return "pull request closed without merge"
	case eventType == "pr.opened":
		return "pull request opened"
	case eventType == "pr.review_requested":
		return "pull request review requested"
	case eventType == "pr.commented":
		return "pull request commented"
	case eventType == "pr.synchronize":
		return "pull request synchronized"
	}
	if input.Action != "" {
		return "pull request " + strings.TrimSpace(input.Action)
	}
	return "pull request event " + eventType
}

func reconcilePRNumber(input ReconcileInput) string {
	if n := canonicalPRNumber(input.PR); n != "" {
		return n
	}
	return canonicalPRNumber(input.PRURL)
}

func canonicalPRURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, "/")
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

func canonicalPRNumber(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if allDigits(raw) {
		return raw
	}
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == "pull" && allDigits(parts[i+1]) {
				return parts[i+1]
			}
		}
	}
	needle := "/pull/"
	if idx := strings.LastIndex(raw, needle); idx >= 0 {
		candidate := strings.Trim(raw[idx+len(needle):], "/")
		if slash := strings.Index(candidate, "/"); slash >= 0 {
			candidate = candidate[:slash]
		}
		if allDigits(candidate) {
			return candidate
		}
	}
	return ""
}

func allDigits(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func payloadValueString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	switch v := payload[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func payloadValueBool(payload map[string]any, key string) (bool, bool) {
	if payload == nil {
		return false, false
	}
	switch v := payload[key].(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}
