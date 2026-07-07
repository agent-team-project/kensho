package daemon

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
)

func TestExitReasonReadWrite(t *testing.T) {
	teamDir := t.TempDir()
	recordedAt := time.Date(2026, 7, 7, 12, 30, 0, 0, time.FixedZone("BST", 3600))
	reason := ExitReason{
		Kind:       ExitKindSignal,
		Signal:     "terminated",
		PID:        90535,
		RecordedAt: recordedAt,
		Build: buildinfo.Info{
			Version:  "0.1.0",
			Revision: "abcdef1234567890",
			Time:     "2026-07-07T11:00:00Z",
		},
	}
	if err := WriteExitReason(teamDir, reason); err != nil {
		t.Fatalf("WriteExitReason: %v", err)
	}
	got, err := ReadExitReason(teamDir)
	if err != nil {
		t.Fatalf("ReadExitReason: %v", err)
	}
	if got.Kind != ExitKindSignal || got.Signal != "terminated" || got.PID != 90535 {
		t.Fatalf("reason = %+v, want signal terminated pid 90535", got)
	}
	if got.Reason != "received terminated" {
		t.Fatalf("reason text = %q, want default signal reason", got.Reason)
	}
	if got.RecordedAt.Location() != time.UTC || !got.RecordedAt.Equal(recordedAt.UTC()) {
		t.Fatalf("recorded_at = %s, want UTC %s", got.RecordedAt, recordedAt.UTC())
	}
	if got.Build.ShortRevision() != "abcdef123456" {
		t.Fatalf("build = %+v, want persisted build", got.Build)
	}
	body, err := os.ReadFile(ExitReasonPath(teamDir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(body), "\n") {
		t.Fatalf("exit reason file should end with newline: %q", string(body))
	}
}

func TestExitReasonMissingWrapsNotExist(t *testing.T) {
	_, err := ReadExitReason(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}
