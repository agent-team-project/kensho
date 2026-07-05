package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	jobstore "github.com/jamesaud/agent-team/internal/job"
)

func fastCodexUsageSpawner(line string) Spawner {
	return func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		bin, err := exec.LookPath("sh")
		if err != nil {
			return nil, err
		}
		stdin, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		stdout, err := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		stderr, err := os.OpenFile(stderrPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, err
		}
		defer stdin.Close()
		defer stdout.Close()
		defer stderr.Close()
		childEnv := append(append([]string(nil), env...), "AGENTTEAM_TEST_CODEX_USAGE="+line)
		return os.StartProcess(bin, []string{"sh", "-c", `printf '%s\n' "$AGENTTEAM_TEST_CODEX_USAGE"`}, &os.ProcAttr{
			Dir:   workspace,
			Env:   childEnv,
			Files: []*os.File{stdin, stdout, stderr},
		})
	}
}

func TestBudgetNoticeWritesEventsAndMailboxForCodexTokenCrossing(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Now().UTC()
	j := &jobstore.Job{
		ID:        "squ-104",
		Ticket:    "SQU-104",
		Target:    "worker",
		Status:    jobstore.StatusRunning,
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Minute),
		Steps: []jobstore.Step{
			{
				ID:             "implement",
				Target:         "worker",
				Status:         jobstore.StatusRunning,
				Instance:       "worker-squ-104",
				TokenBudget:    100,
				TimeBudget:     "10m",
				ReminderLevels: []int{50, 80, 100},
			},
		},
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "codex.jsonl")
	if err := os.WriteFile(logPath, []byte(`{"type":"turn.completed","usage":{"input_tokens":90,"output_tokens":20}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write codex log: %v", err)
	}
	m := NewInstanceManager(DaemonRoot(teamDir), nil)
	meta := Metadata{
		Instance:  "worker-squ-104",
		Agent:     "worker",
		Job:       "squ-104",
		Runtime:   "codex",
		Workspace: t.TempDir(),
		Status:    StatusRunning,
		StartedAt: now.Add(-1 * time.Minute),
		LogPath:   logPath,
	}

	if err := m.checkBudgetNotices(meta, now); err != nil {
		t.Fatalf("checkBudgetNotices: %v", err)
	}
	updated, err := jobstore.Read(teamDir, "squ-104")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if got, want := updated.Steps[0].TokenBudgetNotices, []int{50, 80, 100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("token notices = %v, want %v", got, want)
	}
	if len(updated.Steps[0].TimeBudgetNotices) != 0 {
		t.Fatalf("time notices = %v, want none", updated.Steps[0].TimeBudgetNotices)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-104")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 || events[0].Type != "budget_notice" || events[1].Type != "budget_notice" || events[2].Type != "budget_notice" {
		t.Fatalf("events = %+v, want three budget_notice events", events)
	}
	if events[0].Data["dimension"] != "tokens" || events[0].Data["level"] != "50" || events[1].Data["level"] != "80" || events[2].Data["level"] != "100" || events[0].Data["step"] != "implement" {
		t.Fatalf("event data = %+v / %+v / %+v", events[0].Data, events[1].Data, events[2].Data)
	}
	messages, err := ReadMessages(DaemonRoot(teamDir), "worker-squ-104")
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 3 || !strings.Contains(messages[0].Body, "budget_notice") || !strings.Contains(messages[0].Body, "agent-team budget status --self") {
		t.Fatalf("messages = %+v", messages)
	}
	if !strings.Contains(messages[2].Body, "over allowance") || !strings.Contains(messages[2].Body, "warning-only") {
		t.Fatalf("100%% message = %q, want over-allowance warning", messages[2].Body)
	}

	if err := m.checkBudgetNotices(meta, now.Add(time.Second)); err != nil {
		t.Fatalf("second checkBudgetNotices: %v", err)
	}
	events, err = jobstore.ListEvents(teamDir, "squ-104")
	if err != nil {
		t.Fatalf("list events after second check: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events after second check = %+v, want no duplicate notices", events)
	}
}

func TestBudgetNoticeForClaudeRuntimeUsesTimeOnly(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	now := time.Now().UTC()
	j := &jobstore.Job{
		ID:             "squ-105",
		Ticket:         "SQU-105",
		Target:         "worker",
		Instance:       "worker-squ-105",
		Status:         jobstore.StatusRunning,
		TokenBudget:    100,
		TimeBudget:     "1m",
		ReminderLevels: []int{50},
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
	}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	m := NewInstanceManager(DaemonRoot(teamDir), nil)
	meta := Metadata{
		Instance:  "worker-squ-105",
		Agent:     "worker",
		Job:       "squ-105",
		Runtime:   "claude",
		Workspace: t.TempDir(),
		Status:    StatusRunning,
		StartedAt: now.Add(-40 * time.Second),
	}

	if err := m.checkBudgetNotices(meta, now); err != nil {
		t.Fatalf("checkBudgetNotices: %v", err)
	}
	updated, err := jobstore.Read(teamDir, "squ-105")
	if err != nil {
		t.Fatalf("read updated job: %v", err)
	}
	if len(updated.TokenBudgetNotices) != 0 {
		t.Fatalf("token notices = %v, want none for claude runtime", updated.TokenBudgetNotices)
	}
	if got, want := updated.TimeBudgetNotices, []int{50}; !reflect.DeepEqual(got, want) {
		t.Fatalf("time notices = %v, want %v", got, want)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-105")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Data["dimension"] != "time" || events[0].Data["runtime"] != "claude" {
		t.Fatalf("events = %+v", events)
	}
}

func TestBudgetNoticeFinalReapSweepForFastCodexRuntime(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	writeFixtureRuntimeCommandSkills(t, teamDir, "worker")
	now := time.Now().UTC()
	j, err := jobstore.New("SQU-104", "worker", "finish quickly", now)
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.TokenBudget = 1000
	j.ReminderLevels = []int{50, 80, 100}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	m := NewInstanceManager(root, fastCodexUsageSpawner(`{"type":"turn.completed","usage":{"input_tokens":900,"output_tokens":100}}`))
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult("agent.dispatch", map[string]any{
		"target":         "worker",
		"ticket":         "SQU-104",
		"job_id":         "squ-104",
		"kickoff":        "finish quickly",
		"runtime":        "codex",
		"runtime_binary": "codex",
		"workspace":      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("dispatch event: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Action != "dispatched" {
		t.Fatalf("dispatch result = %+v", result)
	}
	instance := result.Outcomes[0].InstanceID
	if err := m.WaitForReaper(instance, 5*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}

	updated, err := jobstore.Read(teamDir, "squ-104")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != jobstore.StatusDone {
		t.Fatalf("job status = %s, want done; job=%+v", updated.Status, updated)
	}
	if got, want := updated.TokenBudgetNotices, []int{50, 80, 100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("token notices = %v, want %v", got, want)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-104")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var levels []string
	for _, ev := range events {
		if ev.Type == "budget_notice" && ev.Data["dimension"] == "tokens" {
			levels = append(levels, ev.Data["level"])
		}
	}
	if got, want := strings.Join(levels, ","), "50,80,100"; got != want {
		t.Fatalf("budget notice levels = %s, want %s; events=%+v", got, want, events)
	}
	messages, err := ReadMessages(root, instance)
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if len(messages) != 3 || !strings.Contains(messages[2].Body, "over allowance") {
		t.Fatalf("messages = %+v, want three durable budget notices with 100%% warning", messages)
	}
}
