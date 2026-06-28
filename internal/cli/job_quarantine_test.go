package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesaud/agent-team/internal/job"
)

func writeQuarantinedJobFile(t *testing.T, teamDir, stamp, name string, body []byte) string {
	t.Helper()
	dir := filepath.Join(job.Directory(teamDir), "quarantine", stamp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join("quarantine", stamp, name)
}

func TestJobQuarantineListShowRestoreDrop(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")

	restorableRel := writeQuarantinedJobFile(t, teamDir, "20260627T120000.000000000Z", "squ-402-restored.toml", []byte(`id = "squ-402"
ticket = "SQU-402"
target = "worker"
kickoff = "restore this job"
status = "queued"
created_at = 2026-06-27T12:00:00Z
updated_at = 2026-06-27T12:00:00Z
`))
	brokenRel := writeQuarantinedJobFile(t, teamDir, "20260627T120000.000000000Z", "broken.toml", []byte("id = [\n"))
	restorablePath := filepath.Join(job.Directory(teamDir), restorableRel)
	brokenPath := filepath.Join(job.Directory(teamDir), brokenRel)

	list := NewRootCmd()
	listOut, listErr := &bytes.Buffer{}, &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetErr(listErr)
	list.SetArgs([]string{"job", "quarantine", "--repo", tmp, "--sort", "restorable", "--json"})
	if err := list.Execute(); err != nil {
		t.Fatalf("job quarantine list: %v\nstderr=%s", err, listErr.String())
	}
	var listed []jobQuarantineItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("decode job quarantine list: %v\nbody=%s", err, listOut.String())
	}
	if len(listed) != 2 || listed[0].ID != "squ-402" || !listed[0].Restorable || listed[1].Restorable {
		t.Fatalf("listed job quarantine items = %+v", listed)
	}

	listCommands := NewRootCmd()
	listCommandsOut, listCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	listCommands.SetOut(listCommandsOut)
	listCommands.SetErr(listCommandsErr)
	listCommands.SetArgs([]string{"job", "quarantine", "--repo", tmp, "--sort", "restorable", "--commands"})
	if err := listCommands.Execute(); err != nil {
		t.Fatalf("job quarantine list --commands: %v\nstderr=%s", err, listCommandsErr.String())
	}
	wantListCommands := strings.Join(scopedOperatorActions([]string{
		"agent-team job quarantine restore " + restorableRel + " --dry-run",
		"agent-team job quarantine drop " + restorableRel + " --dry-run",
		"agent-team job quarantine drop " + brokenRel + " --dry-run",
	}, operatorCommandScope{Repo: tmp, Set: true}), "\n") + "\n"
	if got := listCommandsOut.String(); got != wantListCommands {
		t.Fatalf("job quarantine list --commands = %q, want %q", got, wantListCommands)
	}
	if strings.Contains(listCommandsOut.String(), "PATH") || strings.Contains(listCommandsOut.String(), "RESTORABLE") {
		t.Fatalf("job quarantine list --commands included table text:\n%s", listCommandsOut.String())
	}

	summaryCmd := NewRootCmd()
	summaryOut, summaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryCmd.SetOut(summaryOut)
	summaryCmd.SetErr(summaryErr)
	summaryCmd.SetArgs([]string{"job", "quarantine", "--repo", tmp, "--summary", "--json"})
	if err := summaryCmd.Execute(); err != nil {
		t.Fatalf("job quarantine summary json: %v\nstderr=%s", err, summaryErr.String())
	}
	var summary jobQuarantineSummary
	if err := json.Unmarshal(summaryOut.Bytes(), &summary); err != nil {
		t.Fatalf("decode job quarantine summary: %v\nbody=%s", err, summaryOut.String())
	}
	if summary.Quarantined != 2 || summary.Restorable != 1 || summary.Unrestorable != 1 || summary.Jobs["squ-402"] != 1 {
		t.Fatalf("job quarantine summary = %+v", summary)
	}

	summaryText := NewRootCmd()
	summaryTextOut, summaryTextErr := &bytes.Buffer{}, &bytes.Buffer{}
	summaryText.SetOut(summaryTextOut)
	summaryText.SetErr(summaryTextErr)
	summaryText.SetArgs([]string{"job", "quarantine", "--repo", tmp, "--restorable", "--summary"})
	if err := summaryText.Execute(); err != nil {
		t.Fatalf("job quarantine restorable summary text: %v\nstderr=%s", err, summaryTextErr.String())
	}
	if got, want := summaryTextOut.String(), "job quarantine: quarantined=1 restorable=1 unrestorable=0\n"; got != want {
		t.Fatalf("job quarantine restorable summary text = %q, want %q", got, want)
	}

	invalidSummary := NewRootCmd()
	invalidSummaryOut, invalidSummaryErr := &bytes.Buffer{}, &bytes.Buffer{}
	invalidSummary.SetOut(invalidSummaryOut)
	invalidSummary.SetErr(invalidSummaryErr)
	invalidSummary.SetArgs([]string{"job", "quarantine", "--repo", tmp, "--summary", "--limit", "1"})
	if err := invalidSummary.Execute(); err == nil {
		t.Fatalf("job quarantine summary accepted --limit; stdout=%s stderr=%s", invalidSummaryOut.String(), invalidSummaryErr.String())
	}
	if !strings.Contains(invalidSummaryErr.String(), "--sort and --limit cannot be combined with --summary") {
		t.Fatalf("job quarantine summary invalid stderr = %q", invalidSummaryErr.String())
	}

	filtered := NewRootCmd()
	filteredOut, filteredErr := &bytes.Buffer{}, &bytes.Buffer{}
	filtered.SetOut(filteredOut)
	filtered.SetErr(filteredErr)
	filtered.SetArgs([]string{"job", "quarantine", "--repo", tmp, "--unrestorable", "--format", "{{.Path}} {{.Restorable}}"})
	if err := filtered.Execute(); err != nil {
		t.Fatalf("job quarantine unrestorable format: %v\nstderr=%s", err, filteredErr.String())
	}
	if got, want := filteredOut.String(), brokenRel+" false\n"; got != want {
		t.Fatalf("job quarantine unrestorable format = %q, want %q", got, want)
	}

	show := NewRootCmd()
	showOut, showErr := &bytes.Buffer{}, &bytes.Buffer{}
	show.SetOut(showOut)
	show.SetErr(showErr)
	show.SetArgs([]string{"job", "quarantine", "show", brokenRel, "--repo", tmp})
	if err := show.Execute(); err != nil {
		t.Fatalf("job quarantine show: %v\nstderr=%s", err, showErr.String())
	}
	for _, want := range []string{"broken", "Restorable: no", "Problem:", "agent-team job quarantine drop"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("job quarantine show missing %q:\n%s", want, showOut.String())
		}
	}
	if strings.Contains(showOut.String(), "agent-team job quarantine restore") {
		t.Fatalf("unrestorable job quarantine show included restore action:\n%s", showOut.String())
	}

	showCommands := NewRootCmd()
	showCommandsOut, showCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	showCommands.SetOut(showCommandsOut)
	showCommands.SetErr(showCommandsErr)
	showCommands.SetArgs([]string{"job", "quarantine", "show", brokenRel, "--repo", tmp, "--commands"})
	if err := showCommands.Execute(); err != nil {
		t.Fatalf("job quarantine show --commands: %v\nstderr=%s", err, showCommandsErr.String())
	}
	wantCommand := scopedOperatorAction("agent-team job quarantine drop "+brokenRel+" --dry-run", operatorCommandScope{Repo: tmp, Set: true}) + "\n"
	if got, want := showCommandsOut.String(), wantCommand; got != want {
		t.Fatalf("job quarantine show --commands = %q, want %q", got, want)
	}

	restoreDry := NewRootCmd()
	restoreDryOut, restoreDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreDry.SetOut(restoreDryOut)
	restoreDry.SetErr(restoreDryErr)
	restoreDry.SetArgs([]string{"job", "quarantine", "restore", restorableRel, "--repo", tmp, "--dry-run", "--format", "{{.ID}} {{.Action}} {{.Destination}} {{.DryRun}}"})
	if err := restoreDry.Execute(); err != nil {
		t.Fatalf("job quarantine restore dry-run: %v\nstderr=%s", err, restoreDryErr.String())
	}
	if got, want := restoreDryOut.String(), "squ-402 would_restore squ-402.toml true\n"; got != want {
		t.Fatalf("job quarantine restore dry-run = %q, want %q", got, want)
	}
	if _, err := os.Stat(restorablePath); err != nil {
		t.Fatalf("dry-run should leave quarantined file in place: %v", err)
	}

	restoreCommands := NewRootCmd()
	restoreCommandsOut, restoreCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	restoreCommands.SetOut(restoreCommandsOut)
	restoreCommands.SetErr(restoreCommandsErr)
	restoreCommands.SetArgs([]string{"job", "quarantine", "restore", restorableRel, "--repo", tmp, "--dry-run", "--commands"})
	if err := restoreCommands.Execute(); err != nil {
		t.Fatalf("job quarantine restore commands: %v\nstderr=%s", err, restoreCommandsErr.String())
	}
	wantRestoreCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "quarantine", "restore", restorableRel, "--repo", tmp}), " ")
	if got := strings.TrimSpace(restoreCommandsOut.String()); got != wantRestoreCommand {
		t.Fatalf("job quarantine restore commands = %q, want %q", got, wantRestoreCommand)
	}

	restore := NewRootCmd()
	restoreOut, restoreErr := &bytes.Buffer{}, &bytes.Buffer{}
	restore.SetOut(restoreOut)
	restore.SetErr(restoreErr)
	restore.SetArgs([]string{"job", "quarantine", "restore", restorableRel, "--repo", tmp, "--json"})
	if err := restore.Execute(); err != nil {
		t.Fatalf("job quarantine restore: %v\nstderr=%s", err, restoreErr.String())
	}
	var restored jobQuarantineRestoreResult
	if err := json.Unmarshal(restoreOut.Bytes(), &restored); err != nil {
		t.Fatalf("decode job quarantine restore: %v\nbody=%s", err, restoreOut.String())
	}
	if restored.ID != "squ-402" || restored.Action != "restored" || restored.Destination != "squ-402.toml" {
		t.Fatalf("restore result = %+v", restored)
	}
	if _, err := os.Stat(restorablePath); !os.IsNotExist(err) {
		t.Fatalf("quarantined restorable file still exists: %v", err)
	}
	if restoredJob, err := job.Read(teamDir, "SQU-402"); err != nil || restoredJob.ID != "squ-402" {
		t.Fatalf("restored active job = %+v err=%v", restoredJob, err)
	}

	dropDry := NewRootCmd()
	dropDryOut, dropDryErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropDry.SetOut(dropDryOut)
	dropDry.SetErr(dropDryErr)
	dropDry.SetArgs([]string{"job", "quarantine", "drop", brokenRel, "--repo", tmp, "--dry-run", "--format", "{{.Path}} {{.Action}} {{.DryRun}}"})
	if err := dropDry.Execute(); err != nil {
		t.Fatalf("job quarantine drop dry-run: %v\nstderr=%s", err, dropDryErr.String())
	}
	if got, want := dropDryOut.String(), brokenRel+" would_drop true\n"; got != want {
		t.Fatalf("job quarantine drop dry-run = %q, want %q", got, want)
	}
	if _, err := os.Stat(brokenPath); err != nil {
		t.Fatalf("dry-run should leave broken file in place: %v", err)
	}

	dropCommands := NewRootCmd()
	dropCommandsOut, dropCommandsErr := &bytes.Buffer{}, &bytes.Buffer{}
	dropCommands.SetOut(dropCommandsOut)
	dropCommands.SetErr(dropCommandsErr)
	dropCommands.SetArgs([]string{"job", "quarantine", "drop", brokenRel, "--repo", tmp, "--dry-run", "--commands"})
	if err := dropCommands.Execute(); err != nil {
		t.Fatalf("job quarantine drop commands: %v\nstderr=%s", err, dropCommandsErr.String())
	}
	wantDropCommand := strings.Join(shellQuoteArgs([]string{"agent-team", "job", "quarantine", "drop", brokenRel, "--repo", tmp}), " ")
	if got := strings.TrimSpace(dropCommandsOut.String()); got != wantDropCommand {
		t.Fatalf("job quarantine drop commands = %q, want %q", got, wantDropCommand)
	}

	drop := NewRootCmd()
	dropOut, dropErr := &bytes.Buffer{}, &bytes.Buffer{}
	drop.SetOut(dropOut)
	drop.SetErr(dropErr)
	drop.SetArgs([]string{"job", "quarantine", "drop", brokenRel, "--repo", tmp, "--json"})
	if err := drop.Execute(); err != nil {
		t.Fatalf("job quarantine drop: %v\nstderr=%s", err, dropErr.String())
	}
	var dropped jobQuarantineDropResult
	if err := json.Unmarshal(dropOut.Bytes(), &dropped); err != nil {
		t.Fatalf("decode job quarantine drop: %v\nbody=%s", err, dropOut.String())
	}
	if dropped.Path != brokenRel || dropped.Action != "dropped" || !dropped.Dropped {
		t.Fatalf("drop result = %+v", dropped)
	}
	if _, err := os.Stat(brokenPath); !os.IsNotExist(err) {
		t.Fatalf("broken quarantined file still exists: %v", err)
	}

	doctor := NewRootCmd()
	doctorOut, doctorErr := &bytes.Buffer{}, &bytes.Buffer{}
	doctor.SetOut(doctorOut)
	doctor.SetErr(doctorErr)
	doctor.SetArgs([]string{"job", "doctor", "--repo", tmp, "--format", "{{.OK}} {{.Summary.Valid}} {{.Summary.Invalid}}"})
	if err := doctor.Execute(); err != nil {
		t.Fatalf("job doctor after quarantine restore/drop: %v\nstderr=%s", err, doctorErr.String())
	}
	if got, want := doctorOut.String(), "true 1 0\n"; got != want {
		t.Fatalf("job doctor after quarantine restore/drop = %q, want %q", got, want)
	}
}

func TestJobQuarantineRejectsCommandsFormatCombinations(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "list commands with json",
			args: []string{"job", "quarantine", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "list commands with format",
			args: []string{"job", "quarantine", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "list commands with summary",
			args: []string{"job", "quarantine", "--commands", "--summary"},
			want: "--commands cannot be combined with --summary",
		},
		{
			name: "commands with json",
			args: []string{"job", "quarantine", "show", "quarantine/20260627T120000.000000000Z/broken.toml", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "commands with format",
			args: []string{"job", "quarantine", "show", "quarantine/20260627T120000.000000000Z/broken.toml", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "restore commands without dry run",
			args: []string{"job", "quarantine", "restore", "quarantine/20260627T120000.000000000Z/squ-402.toml", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "restore commands with json",
			args: []string{"job", "quarantine", "restore", "quarantine/20260627T120000.000000000Z/squ-402.toml", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "restore commands with format",
			args: []string{"job", "quarantine", "restore", "quarantine/20260627T120000.000000000Z/squ-402.toml", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
		{
			name: "drop commands without dry run",
			args: []string{"job", "quarantine", "drop", "quarantine/20260627T120000.000000000Z/broken.toml", "--commands"},
			want: "--commands requires --dry-run",
		},
		{
			name: "drop commands with json",
			args: []string{"job", "quarantine", "drop", "quarantine/20260627T120000.000000000Z/broken.toml", "--dry-run", "--commands", "--json"},
			want: "--commands cannot be combined with --json",
		},
		{
			name: "drop commands with format",
			args: []string{"job", "quarantine", "drop", "quarantine/20260627T120000.000000000Z/broken.toml", "--dry-run", "--commands", "--format", "{{.ID}}"},
			want: "--commands cannot be combined with --format",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("expected error for %v; stdout=%s stderr=%s", tc.args, out.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}
