// Package allowance contains shared parsing and threshold helpers for soft
// per-job and per-agent resource allowances.
package allowance

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// DefaultReminderLevels are the soft percentage thresholds used when a job or
// topology declaration does not opt into a custom list.
var DefaultReminderLevels = []int{50, 80, 100}

// ParseTokens parses token counts such as "40000000", "40M", or "1.5M".
// Suffixes are decimal, not binary: K=1e3, M=1e6, G=1e9, T=1e12.
func ParseTokens(raw string) (int64, error) {
	value := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), "_", ""))
	if value == "" {
		return 0, fmt.Errorf("token budget must be non-empty")
	}
	multiplier := float64(1)
	switch suffix := value[len(value)-1:]; suffix {
	case "K", "M", "G", "T":
		value = strings.TrimSpace(value[:len(value)-1])
		switch suffix {
		case "K":
			multiplier = 1_000
		case "M":
			multiplier = 1_000_000
		case "G":
			multiplier = 1_000_000_000
		case "T":
			multiplier = 1_000_000_000_000
		}
	}
	if value == "" {
		return 0, fmt.Errorf("token budget must include a number")
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("token budget must be a number with optional K/M/G/T suffix: %w", err)
	}
	if amount < 0 {
		return 0, fmt.Errorf("token budget must be >= 0")
	}
	if amount*multiplier > math.MaxInt64 {
		return 0, fmt.Errorf("token budget is too large")
	}
	return int64(amount * multiplier), nil
}

// ParseTokenValue parses a TOML-decoded token value. Integers are accepted as
// raw token counts; strings may use ParseTokens suffixes.
func ParseTokenValue(raw any, field string) (int64, error) {
	if raw == nil {
		return 0, nil
	}
	switch v := raw.(type) {
	case int:
		if v < 0 {
			return 0, fmt.Errorf("%s must be >= 0", field)
		}
		return int64(v), nil
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("%s must be >= 0", field)
		}
		return v, nil
	case string:
		n, err := ParseTokens(v)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", field, err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%s must be an integer or token string", field)
	}
}

// NormalizeReminderLevels validates percentage thresholds and returns them
// sorted and de-duplicated. Nil/empty means the default reminder levels.
func NormalizeReminderLevels(levels []int) ([]int, error) {
	if len(levels) == 0 {
		return append([]int(nil), DefaultReminderLevels...), nil
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(levels))
	for _, level := range levels {
		if level <= 0 || level > 100 {
			return nil, fmt.Errorf("reminder levels must be between 1 and 100")
		}
		if seen[level] {
			continue
		}
		seen[level] = true
		out = append(out, level)
	}
	sort.Ints(out)
	return out, nil
}

// CrossedLevels returns reminder levels crossed by used/limit that are not in
// alreadySent.
func CrossedLevels(used, limit int64, levels, alreadySent []int) []int {
	if limit <= 0 || used < 0 {
		return nil
	}
	normalized, err := NormalizeReminderLevels(levels)
	if err != nil {
		return nil
	}
	sent := map[int]bool{}
	for _, level := range alreadySent {
		sent[level] = true
	}
	var crossed []int
	for _, level := range normalized {
		if sent[level] {
			continue
		}
		if used*100 >= int64(level)*limit {
			crossed = append(crossed, level)
		}
	}
	return crossed
}

// MergeSentLevels returns a sorted de-duplicated union.
func MergeSentLevels(current []int, add ...int) []int {
	seen := map[int]bool{}
	for _, level := range current {
		if level > 0 {
			seen[level] = true
		}
	}
	for _, level := range add {
		if level > 0 {
			seen[level] = true
		}
	}
	out := make([]int, 0, len(seen))
	for level := range seen {
		out = append(out, level)
	}
	sort.Ints(out)
	return out
}

// Percent returns integer percentage used/limit, capped only by int range.
func Percent(used, limit int64) int {
	if limit <= 0 || used <= 0 {
		return 0
	}
	return int(used * 100 / limit)
}
