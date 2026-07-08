package allowance

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseTokens(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "plain", raw: "40000000", want: 40000000},
		{name: "underscore", raw: "40_000_000", want: 40000000},
		{name: "millions", raw: "40M", want: 40000000},
		{name: "fractional millions", raw: "1.5M", want: 1500000},
		{name: "thousands lowercase", raw: "2k", want: 2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTokens(tt.raw)
			if err != nil {
				t.Fatalf("ParseTokens(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseTokens(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseTokensRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "", want: "non-empty"},
		{raw: "M", want: "include a number"},
		{raw: "-1", want: ">= 0"},
		{raw: "soon", want: "number"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			_, err := ParseTokens(tt.raw)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseTokens(%q) err = %v, want %q", tt.raw, err, tt.want)
			}
		})
	}
}

func TestParseTokenValueAcceptsIntegralJSONNumber(t *testing.T) {
	got, err := ParseTokenValue(float64(60), "budget_tokens")
	if err != nil {
		t.Fatalf("ParseTokenValue(float64): %v", err)
	}
	if got != 60 {
		t.Fatalf("ParseTokenValue(float64) = %d, want 60", got)
	}
	for _, raw := range []any{float64(1.5), float64(-1)} {
		t.Run("reject", func(t *testing.T) {
			if _, err := ParseTokenValue(raw, "budget_tokens"); err == nil {
				t.Fatalf("ParseTokenValue(%v) succeeded, want error", raw)
			}
		})
	}
}

func TestCrossedLevelsSkipsSentAndDefaults(t *testing.T) {
	got := CrossedLevels(85, 100, nil, []int{50})
	if want := []int{80}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CrossedLevels = %v, want %v", got, want)
	}
}

func TestNormalizeReminderLevelsSortsAndDeduplicates(t *testing.T) {
	got, err := NormalizeReminderLevels([]int{80, 50, 80, 100})
	if err != nil {
		t.Fatalf("NormalizeReminderLevels: %v", err)
	}
	if want := []int{50, 80, 100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeReminderLevels = %v, want %v", got, want)
	}
}

func TestParseHardMultiplierValue(t *testing.T) {
	got, err := ParseHardMultiplierValue("1.5", "hard_multiplier")
	if err != nil {
		t.Fatalf("ParseHardMultiplierValue: %v", err)
	}
	if got != 1.5 {
		t.Fatalf("multiplier = %v, want 1.5", got)
	}

	for _, raw := range []any{0.5, -1, "soon"} {
		t.Run("reject", func(t *testing.T) {
			if _, err := ParseHardMultiplierValue(raw, "hard_multiplier"); err == nil {
				t.Fatalf("ParseHardMultiplierValue(%v) succeeded, want error", raw)
			}
		})
	}
}

func TestHardLimit(t *testing.T) {
	if got := HardLimit(100, true, 0); got != 100 {
		t.Fatalf("hard limit = %d, want 100", got)
	}
	if got := HardLimit(101, false, 1.5); got != 152 {
		t.Fatalf("multiplier hard limit = %d, want 152", got)
	}
	if got := HardLimit(100, false, 0); got != 0 {
		t.Fatalf("disabled hard limit = %d, want 0", got)
	}
}
