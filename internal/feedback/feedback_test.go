package feedback

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFingerprintNormalizesBody(t *testing.T) {
	left := Fingerprint("  Harness   friction\nIS loud ")
	right := Fingerprint("harness friction is loud")
	if left != right {
		t.Fatalf("Fingerprint normalization mismatch: %s != %s", left, right)
	}
	if left == Fingerprint("different feedback") {
		t.Fatalf("Fingerprint collision for distinct body")
	}
}

func TestSubmitReadGroupAndResolve(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	ctx := Context{
		Instance: "worker-squ-79",
		Agent:    "worker",
		Job:      "squ-79",
		Ticket:   "SQU-79",
		Pipeline: "ticket_to_pr",
		Step:     "implement",
		Runtime:  "codex",
		Build:    "abc123",
	}

	first, err := Submit(teamDir, SubmitInput{
		Body:     "Harness friction is loud",
		Category: CategoryFriction,
		Context:  ctx,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	second, err := Submit(teamDir, SubmitInput{
		Body:     "  harness   friction is loud ",
		Category: CategoryBug,
		Context:  ctx,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("Submit second: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("duplicate submissions got same id %q", first.ID)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("duplicate submissions did not group by fingerprint")
	}

	read, err := Read(teamDir, first.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.Context.Instance != ctx.Instance || read.Context.Build != ctx.Build {
		t.Fatalf("context = %+v, want %+v", read.Context, ctx)
	}

	items, err := List(teamDir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("List len = %d, want 2", len(items))
	}
	groups := GroupItems(items)
	if len(groups) != 1 || groups[0].Count != 2 || groups[0].Fingerprint != first.Fingerprint {
		t.Fatalf("groups = %+v, want one group with count 2", groups)
	}

	resolved, err := Resolve(teamDir, first.ID, ResolveInput{
		Ticket: "SQU-80",
		By:     "triage",
		Now:    now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != StatusTicketed || resolved.Resolution == nil || resolved.Resolution.Ticket != "SQU-80" || resolved.Resolution.By != "triage" {
		t.Fatalf("resolved = %+v", resolved)
	}

	items, err = List(teamDir)
	if err != nil {
		t.Fatalf("List after resolve: %v", err)
	}
	if got := FilterItems(items, string(StatusNew)); len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("new filter = %+v, want only %s", got, second.ID)
	}
	if got := FilterItems(items, StatusAll); len(got) != 2 {
		t.Fatalf("all filter len = %d, want 2", len(got))
	}
}

func TestResolveRequiresOneDisposition(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	item, err := Submit(teamDir, SubmitInput{
		Body:     "one thing happened",
		Category: CategoryIdea,
		Now:      time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := Resolve(teamDir, item.ID, ResolveInput{}); err == nil {
		t.Fatalf("Resolve accepted missing disposition")
	}
	if _, err := Resolve(teamDir, item.ID, ResolveInput{Ticket: "SQU-1", Reason: "no"}); err == nil {
		t.Fatalf("Resolve accepted both dispositions")
	}
}
