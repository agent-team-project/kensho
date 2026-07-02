package cli

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	teamtemplate "github.com/jamesaud/agent-team/internal/template"
)

const defaultStatusStaleAfter = 10 * time.Minute

// staleAfter is retained as the historical default used throughout tests.
const staleAfter = defaultStatusStaleAfter

type healthPolicy struct {
	StatusStaleAfter  time.Duration
	JobStaleAfter     time.Duration
	TerminalRetention time.Duration
}

func defaultHealthPolicy() healthPolicy {
	return healthPolicy{
		StatusStaleAfter:  defaultStatusStaleAfter,
		JobStaleAfter:     defaultJobTriageStaleAfter,
		TerminalRetention: 0,
	}
}

func loadHealthPolicy(teamDir string) (healthPolicy, error) {
	policy := defaultHealthPolicy()
	cfg, err := teamtemplate.LoadTOMLFile(filepath.Join(teamDir, "config.toml"))
	if err != nil {
		return policy, err
	}
	if v, ok := cfg.GetDotted("health.status_stale_after"); ok {
		d, err := parseHealthPolicyDuration("health.status_stale_after", v)
		if err != nil {
			return policy, err
		}
		policy.StatusStaleAfter = d
	}
	if v, ok := cfg.GetDotted("health.job_stale_after"); ok {
		d, err := parseHealthPolicyDuration("health.job_stale_after", v)
		if err != nil {
			return policy, err
		}
		policy.JobStaleAfter = d
	}
	if v, ok := cfg.GetDotted("health.terminal_retention"); ok {
		d, err := parseHealthPolicyDuration("health.terminal_retention", v)
		if err != nil {
			return policy, err
		}
		policy.TerminalRetention = d
	}
	return policy, nil
}

func configuredJobTriageStaleAfter(teamDir string) (time.Duration, error) {
	policy, err := loadHealthPolicy(teamDir)
	if err != nil {
		return 0, err
	}
	return policy.JobStaleAfter, nil
}

func collectJobTriageWithPolicy(teamDir string, now time.Time) (jobTriageSnapshot, error) {
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	return collectJobTriage(teamDir, now, staleAfter)
}

func collectTeamTriageWithPolicy(teamDir, name string, now time.Time, filters jobTriageFilters) (jobTriageSnapshot, error) {
	staleAfter, err := configuredJobTriageStaleAfter(teamDir)
	if err != nil {
		return jobTriageSnapshot{}, err
	}
	return collectTeamTriage(teamDir, name, now, staleAfter, filters)
}

func parseHealthPolicyDuration(key string, value any) (time.Duration, error) {
	raw, ok := value.(string)
	if !ok {
		return 0, fmt.Errorf("%s must be a duration string like \"30m\" or \"24h\"", key)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("%s must not be empty", key)
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		d, err = parseDayDuration(raw)
		if err != nil {
			return 0, fmt.Errorf("%s: invalid duration %q: %w", key, raw, err)
		}
	}
	if d < 0 {
		return 0, fmt.Errorf("%s must be >= 0", key)
	}
	return d, nil
}

func parseDayDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasSuffix(raw, "d") {
		return 0, fmt.Errorf("unsupported duration unit")
	}
	daysRaw := strings.TrimSpace(strings.TrimSuffix(raw, "d"))
	if daysRaw == "" {
		return 0, fmt.Errorf("missing day count")
	}
	days, err := strconv.ParseFloat(daysRaw, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(days) || math.IsInf(days, 0) {
		return 0, fmt.Errorf("invalid day count")
	}
	return time.Duration(days * float64(24*time.Hour)), nil
}
