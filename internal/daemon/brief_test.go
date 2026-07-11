package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOneLineTruncationPreservesUTF8AndByteBound(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "exact multibyte bound",
			value: strings.Repeat("é", briefOneLineMaxBytes/2),
			want:  strings.Repeat("é", briefOneLineMaxBytes/2),
		},
		{
			name:  "ascii truncation fills bound",
			value: strings.Repeat("a", briefOneLineMaxBytes+1),
			want:  strings.Repeat("a", briefOneLineMaxBytes-len(briefEllipsis)) + briefEllipsis,
		},
		{
			name:  "multibyte rune crossing truncation boundary",
			value: strings.Repeat("a", briefOneLineMaxBytes-len(briefEllipsis)-1) + "—" + strings.Repeat("b", 20),
			want:  strings.Repeat("a", briefOneLineMaxBytes-len(briefEllipsis)-1) + briefEllipsis,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oneLine(tt.value)
			if got != tt.want {
				t.Fatalf("oneLine() = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("oneLine() returned invalid UTF-8: %q", got)
			}
			if len(got) > briefOneLineMaxBytes {
				t.Fatalf("oneLine() length = %d, want <= %d", len(got), briefOneLineMaxBytes)
			}
			if again := oneLine(tt.value); again != got {
				t.Fatalf("oneLine() is not deterministic: first %q, second %q", got, again)
			}
		})
	}
}

func TestValidateInstanceBriefTextRejectsInvalidUTF8(t *testing.T) {
	if err := validateInstanceBriefText("valid — brief"); err != nil {
		t.Fatalf("valid brief rejected: %v", err)
	}
	if err := validateInstanceBriefText("invalid \xff brief"); err == nil {
		t.Fatal("invalid UTF-8 brief was accepted")
	}
}

func TestGenerateAndWriteInstanceBriefRejectsInvalidUTF8BeforeWrite(t *testing.T) {
	teamDir := t.TempDir()
	instance := "manager-\xff"
	brief, err := GenerateAndWriteInstanceBrief(teamDir, instance, BriefOptions{})
	if err == nil || err.Error() != "brief: rendered text is not valid UTF-8" {
		t.Fatalf("GenerateAndWriteInstanceBrief() error = %v, want invalid UTF-8 error", err)
	}
	if brief != nil {
		t.Fatalf("GenerateAndWriteInstanceBrief() brief = %+v, want nil", brief)
	}
	stateDir := filepath.Join(teamDir, "state", instance)
	if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
		t.Fatalf("brief state dir created before UTF-8 validation: stat error = %v", statErr)
	}
}
