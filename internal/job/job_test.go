package job

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/origin"
)

func TestNormalizeID(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{raw: "SQU-42", want: "squ-42"},
		{raw: " Linear / Ticket 42 ", want: "linear-ticket-42"},
		{raw: "Feature: PR_owner", want: "feature-pr_owner"},
		{raw: "###", want: ""},
	} {
		if got := NormalizeID(tc.raw); got != tc.want {
			t.Fatalf("NormalizeID(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestAttemptHeadMatchesRequiresExactGeneration(t *testing.T) {
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	j := &Job{Attempt: 2, Head: headB}

	for _, tc := range []struct {
		name    string
		attempt int
		head    string
		want    bool
	}{
		{name: "current", attempt: 2, head: headB, want: true},
		{name: "prior attempt", attempt: 1, head: headB},
		{name: "prior head", attempt: 2, head: headA},
		{name: "missing head", attempt: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AttemptHeadMatches(j, tc.attempt, tc.head); got != tc.want {
				t.Fatalf("AttemptHeadMatches(%d, %q) = %t, want %t", tc.attempt, tc.head, got, tc.want)
			}
		})
	}
	if !AttemptHeadMatches(&Job{}, 0, "") {
		t.Fatal("legacy attempt-one/headless generation did not match")
	}
}

func TestTicketIdentity(t *testing.T) {
	for _, tc := range []struct {
		raw       string
		ticket    string
		ticketURL string
		id        string
	}{
		{raw: "SQU-42", ticket: "SQU-42", id: "squ-42"},
		{
			raw:       "https://linear.app/squirtlesquad/issue/SQU-42/status-monitor",
			ticket:    "SQU-42",
			ticketURL: "https://linear.app/squirtlesquad/issue/SQU-42/status-monitor",
			id:        "squ-42",
		},
		{
			raw:       "https://linear.app/squirtlesquad/issue/squ-43/lowercase",
			ticket:    "SQU-43",
			ticketURL: "https://linear.app/squirtlesquad/issue/squ-43/lowercase",
			id:        "squ-43",
		},
	} {
		ticket, ticketURL := TicketIdentity(tc.raw)
		if ticket != tc.ticket || ticketURL != tc.ticketURL {
			t.Fatalf("TicketIdentity(%q) = %q, %q; want %q, %q", tc.raw, ticket, ticketURL, tc.ticket, tc.ticketURL)
		}
		j, err := New(tc.raw, "worker", "kickoff", time.Now())
		if err != nil {
			t.Fatalf("New(%q): %v", tc.raw, err)
		}
		if j.ID != tc.id || j.Ticket != tc.ticket || j.TicketURL != tc.ticketURL {
			t.Fatalf("job from %q = %+v", tc.raw, j)
		}
	}
}

func TestEpicAttributionDerivation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		explicit      string
		ticketURL     string
		originProject string
		want          string
	}{
		{name: "explicit wins", explicit: "resource-governance", ticketURL: "https://github.com/agent-team-project/kensho/issues/202", originProject: "project-1", want: "resource-governance"},
		{name: "github issue URL", ticketURL: "https://github.com/agent-team-project/kensho/issues/202", want: "agent-team-project/kensho#202"},
		{name: "linear issue URL", ticketURL: "https://linear.app/squirtlesquad/issue/squ-42/status-monitor", want: "SQU-42"},
		{name: "generic URL", ticketURL: "https://tracker.example.com/projects/core/epic-1?ignored=true", want: "tracker.example.com/projects/core/epic-1"},
		{name: "origin project fallback", originProject: "project-1", want: "project-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EpicFromInputs(tc.explicit, tc.ticketURL, tc.originProject); got != tc.want {
				t.Fatalf("EpicFromInputs = %q, want %q", got, tc.want)
			}
		})
	}

	j := &Job{
		TicketURL: "https://github.com/agent-team-project/kensho/issues/202",
		Origin:    origin.Envelope{Project: "project-1"},
	}
	if got := EpicForJob(j); got != "agent-team-project/kensho#202" {
		t.Fatalf("EpicForJob = %q, want GitHub issue tag", got)
	}
	j.Epic = "manual"
	if got := EpicForJob(j); got != "manual" {
		t.Fatalf("EpicForJob explicit = %q, want manual", got)
	}
}

func TestStepDispatchKickoff(t *testing.T) {
	got := StepDispatchKickoff("Implement SQU-42", "review", "Review the branch and prepare PR feedback.")
	for _, want := range []string{
		"Implement SQU-42",
		"--- pipeline step instructions (review) ---",
		"Review the branch and prepare PR feedback.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("StepDispatchKickoff missing %q in:\n%s", want, got)
		}
	}
	if got := StepDispatchKickoff("Implement SQU-42", "review", " "); got != "Implement SQU-42" {
		t.Fatalf("empty instructions kickoff = %q", got)
	}
	if got := StepDispatchKickoff("", "", "Only step instructions"); got != "--- pipeline step instructions ---\n\nOnly step instructions" {
		t.Fatalf("instruction-only kickoff = %q", got)
	}
}

func TestJobReadWriteList(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	first, err := New("SQU-42", "worker", "SQU-42: fix it", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first.Instance = "worker-squ-42"
	first.Status = StatusRunning
	first.LastEvent = "dispatched"
	if err := Write(teamDir, first); err != nil {
		t.Fatalf("Write first: %v", err)
	}
	second, err := New("SQU-41", "manager", "SQU-41", now)
	if err != nil {
		t.Fatalf("New second: %v", err)
	}
	if err := Write(teamDir, second); err != nil {
		t.Fatalf("Write second: %v", err)
	}

	got, err := Read(teamDir, "SQU-42")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ID != "squ-42" || got.Status != StatusRunning || got.Instance != "worker-squ-42" {
		t.Fatalf("Read job = %+v", got)
	}
	jobs, err := List(teamDir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != "squ-41" || jobs[1].ID != "squ-42" {
		t.Fatalf("List = %+v, want sorted ids", jobs)
	}
}

func TestJobResourceURIBackfill(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte("[project]\nid = \"dep\"\nparent_uri = \"agt://parent/project/parent\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	j, err := New("SQU-156", "worker", "add URIs", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Instance = "worker-squ-156"
	j.Branch = "squ-156-b347bce8"
	j.Worktree = "/repo/.claude/worktrees/worker-squ-156-b347bce8"
	j.Steps = []Step{{ID: "implement", Target: "worker", Status: StatusRunning, Instance: j.Instance, Workspace: "worktree"}}
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(teamDir, "SQU-156")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.URI != "agt://dep/job/squ-156" ||
		got.DeploymentURI != "agt://dep/project/dep" ||
		got.DeploymentParentURI != "agt://parent/project/parent" ||
		got.InstanceURI != "agt://dep/instance/worker-squ-156" ||
		got.WorkspaceURI != "agt://dep/workspace/branch:squ-156-b347bce8" {
		t.Fatalf("job URIs = %+v", got)
	}
	if len(got.Steps) != 1 ||
		got.Steps[0].URI != "agt://dep/job/squ-156#step=implement" ||
		got.Steps[0].JobURI != got.URI ||
		got.Steps[0].InstanceURI != got.InstanceURI ||
		got.Steps[0].WorkspaceURI != got.WorkspaceURI {
		t.Fatalf("step URIs = %+v", got.Steps)
	}
}

func TestJobReadAndEventsAcceptTicketURL(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	ticketURL := "https://linear.app/squirtlesquad/issue/SQU-44/from-url"
	j, err := New(ticketURL, "worker", "from url", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.LastEvent = "created"
	j.LastStatus = "created"
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := AppendSnapshotEvent(teamDir, j, "", "test", "", nil); err != nil {
		t.Fatalf("AppendSnapshotEvent: %v", err)
	}
	read, err := Read(teamDir, ticketURL)
	if err != nil {
		t.Fatalf("Read URL: %v", err)
	}
	if read.ID != "squ-44" || read.Ticket != "SQU-44" || read.TicketURL != ticketURL {
		t.Fatalf("read = %+v", read)
	}
	events, err := ListEvents(teamDir, ticketURL)
	if err != nil {
		t.Fatalf("ListEvents URL: %v", err)
	}
	if len(events) != 1 || events[0].JobID != "squ-44" {
		t.Fatalf("events = %+v", events)
	}
}

func TestJobValidation(t *testing.T) {
	now := time.Now().UTC()
	j := &Job{
		ID:        "SQU-42",
		Ticket:    "SQU-42",
		Target:    "worker",
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted unnormalized id")
	}
	j.ID = "squ-42"
	j.Status = Status("paused")
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid status")
	}
	j.Status = StatusQueued
	j.Steps = []Step{{ID: "review", Target: "manager", Status: StatusBlocked, Gate: StepGatePR}}
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected PR gate: %v", err)
	}
	j.Steps[0].Gate = "robot"
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid gate")
	}
	j.Steps[0].Gate = ""
	j.Steps[0].Skipped = true
	j.Steps[0].Status = StatusBlocked
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted skipped non-done step")
	}
	j.Steps[0].Status = StatusDone
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected skipped done step: %v", err)
	}
	j.Steps = []Step{{ID: "implement", Target: "worker", Status: StatusQueued, Timeout: "soon"}}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid step timeout")
	}
	j.Steps[0].Timeout = "0s"
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted zero step timeout")
	}
	j.Steps[0].Timeout = "15m"
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected valid step timeout: %v", err)
	}
	j.Steps[0].Attempts = -1
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted negative step attempts")
	}
	j.Steps[0].Attempts = 1
	j.Steps[0].MaxAttempts = -1
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted negative step max_attempts")
	}
	j.Steps[0].MaxAttempts = 2
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected valid attempt metadata: %v", err)
	}
	j.Steps[0].Workspace = "scratch"
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid step workspace")
	}
	j.Steps[0].Workspace = "repo"
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected valid step workspace: %v", err)
	}
	j.Steps[0].Runtime = "llama"
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid step runtime")
	}
	j.Steps[0].Runtime = "codex"
	j.Steps[0].RuntimeBin = "codex-dev"
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected valid step runtime: %v", err)
	}
	j.Merge = &Merge{Strategy: "merge-commit"}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid merge strategy")
	}
	j.Merge = &Merge{Strategy: "script"}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted script merge without script")
	}
	j.Merge = &Merge{Strategy: "squash", Script: "merge.sh"}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted script path for squash merge")
	}
	j.Merge = &Merge{Strategy: "squash", Land: "ff-only"}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid merge land")
	}
	j.Merge = &Merge{Strategy: "script", Script: ".agent_team/scripts/merge.sh", Land: "merge", OwnedPaths: []string{"coverage/baselines"}}
	j.Drift = &Drift{Classification: "mystery"}
	if err := Validate(j); err == nil {
		t.Fatalf("Validate accepted invalid drift classification")
	}
	j.Drift = &Drift{Classification: "reconcilable", Base: "main", Head: "feature", Files: []string{"coverage/baselines/a.json"}}
	if err := Validate(j); err != nil {
		t.Fatalf("Validate rejected valid merge and drift metadata: %v", err)
	}
}

func TestReadMissingJob(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), ".agent_team"), "squ-404")
	if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		t.Fatalf("Read missing err=%v, want not exist", err)
	}
}

func TestJobEventsAppendListTail(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.FixedZone("local", 3600))
	if err := AppendEvent(teamDir, &Event{
		TS:       now,
		JobID:    "SQU-42",
		Type:     "created",
		Status:   StatusQueued,
		Instance: " worker-squ-42 ",
		Message:  " created ",
		Actor:    " cli ",
		Data:     map[string]string{"ticket": "SQU-42"},
	}); err != nil {
		t.Fatalf("AppendEvent first: %v", err)
	}
	if err := AppendEvent(teamDir, &Event{
		JobID:  "squ-42",
		Type:   "closed",
		Status: StatusDone,
	}); err != nil {
		t.Fatalf("AppendEvent second: %v", err)
	}
	events, err := ListEvents(teamDir, "SQU-42")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len=%d, want 2: %+v", len(events), events)
	}
	first := events[0]
	if first.JobID != "squ-42" || first.Type != "created" || first.Status != StatusQueued || first.TS.Location() != time.UTC {
		t.Fatalf("first event = %+v", first)
	}
	if first.Instance != "worker-squ-42" || first.Message != "created" || first.Actor != "cli" || first.Data["ticket"] != "SQU-42" {
		t.Fatalf("first event fields = %+v", first)
	}
	tail := TailEvents(events, 1)
	if len(tail) != 1 || tail[0].Type != "closed" {
		t.Fatalf("tail = %+v", tail)
	}
}

func TestJobEventsMissingAndInvalid(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	events, err := ListEvents(teamDir, "missing")
	if err != nil {
		t.Fatalf("ListEvents missing: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("missing events = %+v", events)
	}
	if err := AppendEvent(teamDir, &Event{JobID: "SQU-42"}); err == nil {
		t.Fatalf("AppendEvent accepted missing type")
	}
	if err := os.MkdirAll(Directory(teamDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(EventPath(teamDir, "squ-43"), []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatalf("write bad event log: %v", err)
	}
	_, err = ListEvents(teamDir, "squ-43")
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("ListEvents invalid err=%v, want line number", err)
	}
}

func TestJobGateRecordsAppendListLatest(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.FixedZone("local", 3600))
	if err := AppendGateRecord(teamDir, &GateRecord{
		TS:        now,
		JobID:     "SQU-42",
		Name:      " rust-checks ",
		Status:    GateStatusFail,
		Signature: " missing-binary:coral-app/grpc ",
		LogRef:    " logs/rust.txt ",
		Actor:     " worker-squ-42 ",
	}); err != nil {
		t.Fatalf("AppendGateRecord first: %v", err)
	}
	if err := AppendGateRecord(teamDir, &GateRecord{
		JobID:  "squ-42",
		Name:   "rust-checks",
		Status: GateStatusPass,
	}); err != nil {
		t.Fatalf("AppendGateRecord second: %v", err)
	}
	if err := AppendGateRecord(teamDir, &GateRecord{
		JobID:  "squ-42",
		Name:   "lint",
		Status: GateStatusFail,
	}); err != nil {
		t.Fatalf("AppendGateRecord third: %v", err)
	}
	records, err := ListGateRecords(teamDir, "SQU-42")
	if err != nil {
		t.Fatalf("ListGateRecords: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("records len=%d, want 3: %+v", len(records), records)
	}
	first := records[0]
	if first.JobID != "squ-42" || first.Name != "rust-checks" || first.Status != GateStatusFail || first.TS.Location() != time.UTC {
		t.Fatalf("first record = %+v", first)
	}
	if first.Signature != "missing-binary:coral-app/grpc" || first.LogRef != "logs/rust.txt" || first.Actor != "worker-squ-42" {
		t.Fatalf("first record fields = %+v", first)
	}
	latest := LatestGateRecords(records)
	if len(latest) != 2 || latest[0].Name != "lint" || latest[1].Name != "rust-checks" || latest[1].Status != GateStatusPass {
		t.Fatalf("latest = %+v", latest)
	}
}

func TestLatestGateRecordsForAttemptHeadRejectsPriorAttemptAndHead(t *testing.T) {
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	records := []GateRecord{
		{Attempt: 1, Step: "verify", Commit: headA, Name: "tests", Status: GateStatusPass},
		{Attempt: 2, Step: "verify", Commit: headA, Name: "tests", Status: GateStatusPass},
		{Attempt: 2, Step: "verify", Commit: headB, Name: "tests", Status: GateStatusFail},
		{Attempt: 2, Step: "review", Commit: headB, Name: "review", Status: GateStatusPass},
	}
	latest := LatestGateRecordsForAttemptHead(records, 2, headB)
	if len(latest) != 2 || latest[0].Name != "review" || latest[0].Commit != headB || latest[1].Name != "tests" || latest[1].Status != GateStatusFail || latest[1].Commit != headB {
		t.Fatalf("latest = %+v", latest)
	}
	if got := LatestGateRecordsForAttemptHead(records, 2, headA); len(got) != 1 || got[0].Name != "tests" || got[0].Status != GateStatusPass || got[0].Commit != headA {
		t.Fatalf("head-A latest = %+v", got)
	}
}

func TestLatestGateRecordsForAttemptHeadScopesNamesByStep(t *testing.T) {
	head := strings.Repeat("b", 40)
	records := []GateRecord{
		{Attempt: 2, Step: "verify", Commit: head, Name: "shared", Status: GateStatusFail},
		{Attempt: 2, Step: "verify", Commit: head, Name: "gofmt", Status: GateStatusPass},
		{Attempt: 2, Step: "review", Commit: head, Name: "shared", Status: GateStatusPass},
	}

	latest := LatestGateRecordsForAttemptHead(records, 2, head)
	if len(latest) != 3 {
		t.Fatalf("latest len=%d, want 3: %+v", len(latest), latest)
	}
	byStepAndName := make(map[string]GateStatus, len(latest))
	for _, record := range latest {
		byStepAndName[record.Step+":"+record.Name] = record.Status
	}
	want := map[string]GateStatus{
		"verify:shared": GateStatusFail,
		"verify:gofmt":  GateStatusPass,
		"review:shared": GateStatusPass,
	}
	if len(byStepAndName) != len(want) {
		t.Fatalf("latest by step/name = %+v, want %+v", byStepAndName, want)
	}
	for key, status := range want {
		if byStepAndName[key] != status {
			t.Fatalf("latest by step/name = %+v, want %+v", byStepAndName, want)
		}
	}
}

func TestJobGateRecordsMissingAndInvalid(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	records, err := ListGateRecords(teamDir, "missing")
	if err != nil {
		t.Fatalf("ListGateRecords missing: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("missing records = %+v", records)
	}
	if err := AppendGateRecord(teamDir, &GateRecord{JobID: "SQU-42", Status: GateStatusPass}); err == nil {
		t.Fatalf("AppendGateRecord accepted missing name")
	}
	if err := os.MkdirAll(Directory(teamDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(GatePath(teamDir, "squ-43"), []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatalf("write bad gate log: %v", err)
	}
	_, err = ListGateRecords(teamDir, "squ-43")
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("ListGateRecords invalid err=%v, want line number", err)
	}
}

func TestPreviewReconcilePRDoesNotWrite(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 6, 18, 13, 0, 0, 0, time.UTC)
	j, err := New("SQU-78", "worker", "preview the change", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	j.Status = StatusRunning
	j.PR = "https://github.com/acme/repo/pull/78"
	j.Branch = "worktree-worker-squ-78"
	if err := Write(teamDir, j); err != nil {
		t.Fatalf("Write: %v", err)
	}
	merged := true
	result, err := PreviewReconcilePR(teamDir, ReconcileInput{
		EventType: "pr.merged",
		Action:    "closed",
		PRURL:     "https://github.com/acme/repo/pull/78",
		Branch:    "worker-squ-78",
		Merged:    &merged,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("PreviewReconcilePR: %v", err)
	}
	if result.MatchedBy != "pr_url" || result.Job.Status != StatusDone || result.Job.Branch != "worker-squ-78" {
		t.Fatalf("preview result = %+v", result)
	}
	unchanged, err := Read(teamDir, "squ-78")
	if err != nil {
		t.Fatalf("Read unchanged: %v", err)
	}
	if unchanged.Status != StatusRunning || unchanged.LastEvent != "" || unchanged.Branch != "worktree-worker-squ-78" {
		t.Fatalf("preview mutated persisted job = %+v", unchanged)
	}
	events, err := ListEvents(teamDir, "squ-78")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("preview wrote events = %+v", events)
	}
}
