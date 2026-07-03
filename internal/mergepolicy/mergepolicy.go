package mergepolicy

import (
	"fmt"
	"strings"
)

const (
	StrategySquash = "squash"
	StrategyRebase = "rebase"
	StrategyScript = "script"
)

const (
	LandSquash = "squash"
	LandMerge  = "merge"
	LandRebase = "rebase"
)

const (
	DriftClean        = "clean"
	DriftReconcilable = "reconcilable"
	DriftUnclassified = "unclassified"
)

func NormalizeStrategy(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case StrategySquash, StrategyRebase, StrategyScript:
		return value, nil
	default:
		return "", fmt.Errorf("merge strategy must be squash, rebase, or script")
	}
}

func ValidStrategy(strategy string) bool {
	if strings.TrimSpace(strategy) == "" {
		return true
	}
	_, err := NormalizeStrategy(strategy)
	return err == nil
}

func NormalizeLand(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return LandSquash, nil
	}
	switch value {
	case LandSquash, LandMerge, LandRebase:
		return value, nil
	default:
		return "", fmt.Errorf("merge land must be squash, merge, or rebase")
	}
}

func ValidLand(land string) bool {
	_, err := NormalizeLand(land)
	return err == nil
}

func ValidDrift(classification string) bool {
	switch strings.TrimSpace(classification) {
	case "", DriftClean, DriftReconcilable, DriftUnclassified:
		return true
	default:
		return false
	}
}
