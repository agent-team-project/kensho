package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	jobstore "github.com/agent-team-project/agent-team/internal/job"
)

const (
	helperBudgetUsageEnv        = "AGENTTEAM_HELPER_BUDGET_USAGE"
	helperBudgetUsageLineEnv    = "AGENTTEAM_HELPER_BUDGET_USAGE_LINE"
	helperBudgetUsageDelayEnv   = "AGENTTEAM_HELPER_BUDGET_USAGE_DELAY"
	helperBudgetUsageReleaseEnv = "AGENTTEAM_HELPER_BUDGET_USAGE_RELEASE"
	helperBudgetUsageSleepEnv   = "AGENTTEAM_HELPER_BUDGET_USAGE_SLEEP"
)

func TestHelperProcessBudgetUsageSleeper(t *testing.T) {
	if os.Getenv(helperBudgetUsageEnv) != "1" {
		return
	}
	if delay, err := time.ParseDuration(os.Getenv(helperBudgetUsageDelayEnv)); err == nil && delay > 0 {
		time.Sleep(delay)
	}
	if line := os.Getenv(helperBudgetUsageLineEnv); line != "" {
		_, _ = os.Stdout.WriteString(line + "\n")
	}
	if releasePath := os.Getenv(helperBudgetUsageReleaseEnv); releasePath != "" {
		deadline := time.Now().Add(30 * time.Second)
		for {
			if _, err := os.Stat(releasePath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if sleepFor, err := time.ParseDuration(os.Getenv(helperBudgetUsageSleepEnv)); err == nil && sleepFor > 0 {
		time.Sleep(sleepFor)
	}
}

func codexUsageSleeperSpawner(line string, delay, sleepFor time.Duration) Spawner {
	return func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
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
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessBudgetUsageSleeper")
		cmd.Env = append(append([]string(nil), env...),
			helperBudgetUsageEnv+"=1",
			helperBudgetUsageLineEnv+"="+line,
			helperBudgetUsageDelayEnv+"="+delay.String(),
			helperBudgetUsageSleepEnv+"="+sleepFor.String(),
		)
		cmd.Dir = workspace
		cmd.Stdin = stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		err = cmd.Start()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		if err != nil {
			return nil, err
		}
		return cmd.Process, nil
	}
}

func codexUsageReleaseSpawner(line, releasePath string) Spawner {
	return func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
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
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessBudgetUsageSleeper")
		cmd.Env = append(append([]string(nil), env...),
			helperBudgetUsageEnv+"=1",
			helperBudgetUsageLineEnv+"="+line,
			helperBudgetUsageDelayEnv+"=0s",
			helperBudgetUsageReleaseEnv+"="+releasePath,
			helperBudgetUsageSleepEnv+"=0s",
		)
		cmd.Dir = workspace
		cmd.Stdin = stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		err = cmd.Start()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		if err != nil {
			return nil, err
		}
		return cmd.Process, nil
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

func waitForJobStatusAndTokenNotices(t *testing.T, teamDir, id string, wantStatus jobstore.Status, wantNotices []int, timeout time.Duration) *jobstore.Job {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last *jobstore.Job
	var lastErr error
	for {
		j, err := jobstore.Read(teamDir, id)
		if err == nil {
			last = j
			lastErr = nil
			if j.Status == wantStatus && reflect.DeepEqual(j.TokenBudgetNotices, wantNotices) {
				return j
			}
			if jobStatusTerminal(j.Status) && j.Status != wantStatus {
				t.Fatalf("job %s status = %s, want %s; job=%+v", id, j.Status, wantStatus, j)
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if last != nil {
		t.Fatalf("job %s status/notices = %s/%v, want %s/%v within %s; job=%+v", id, last.Status, last.TokenBudgetNotices, wantStatus, wantNotices, timeout, last)
	}
	t.Fatalf("job %s did not reach status/notices %s/%v within %s: last read: %v", id, wantStatus, wantNotices, timeout, lastErr)
	return nil
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
	releasePath := filepath.Join(t.TempDir(), "release")
	m := NewInstanceManager(root, codexUsageReleaseSpawner(`{"type":"turn.completed","usage":{"input_tokens":900,"output_tokens":100}}`, releasePath))
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
	if err := os.WriteFile(releasePath, []byte("ok"), 0o644); err != nil {
		t.Fatalf("release child: %v", err)
	}
	if err := m.WaitForReaper(instance, 5*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}

	updated := waitForJobStatusAndTokenNotices(t, teamDir, "squ-104", jobstore.StatusDone, []int{50, 80, 100}, 10*time.Second)
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

func TestBudgetHardCutoffKillsCodexTokenCrossing(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	writeFixtureRuntimeCommandSkills(t, teamDir, "worker")
	now := time.Now().UTC()
	j, err := jobstore.New("SQU-105", "worker", "cross hard budget", now)
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.TokenBudget = 100
	j.HardBudget = true
	j.ReminderLevels = []int{50, 80, 100}
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	m := NewInstanceManager(root, codexUsageSleeperSpawner(`{"type":"turn.completed","usage":{"input_tokens":90,"output_tokens":20}}`, 0, 10*time.Second))
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult("agent.dispatch", map[string]any{
		"target":         "worker",
		"ticket":         "SQU-105",
		"job_id":         "squ-105",
		"kickoff":        "cross hard budget",
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
	if err := m.WaitForReaper(instance, 8*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	meta, err := ReadMetadata(root, instance)
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Status != StatusCrashed {
		t.Fatalf("metadata status = %s, want crashed", meta.Status)
	}
	updated, err := jobstore.Read(teamDir, "squ-105")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != jobstore.StatusFailed {
		t.Fatalf("job status = %s, want failed; job=%+v", updated.Status, updated)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-105")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	hardEvent := false
	for _, ev := range events {
		if ev.Type == "budget_exceeded_hard" && ev.Data["dimension"] == "tokens" && ev.Data["used"] == "110" && ev.Data["hard_limit"] == "100" {
			hardEvent = true
		}
	}
	if !hardEvent {
		t.Fatalf("events missing hard cutoff: %+v", events)
	}
	lifecycle, err := ListLifecycleEvents(root)
	if err != nil {
		t.Fatalf("lifecycle events: %v", err)
	}
	if !lifecycleEventsContain(lifecycle, "watchdog", instance) {
		t.Fatalf("lifecycle events missing watchdog kill: %+v", lifecycle)
	}
}

func TestBudgetHardCutoffUsesExtendedTokenBudget(t *testing.T) {
	teamDir := fixtureTeamDir(t)
	root := DaemonRoot(teamDir)
	writeFixtureRuntimeCommandSkills(t, teamDir, "worker")
	now := time.Now().UTC()
	j, err := jobstore.New("SQU-106", "worker", "extend before hard budget", now)
	if err != nil {
		t.Fatalf("job.New: %v", err)
	}
	j.TokenBudget = 100
	j.HardBudget = true
	if err := jobstore.Write(teamDir, j); err != nil {
		t.Fatalf("write job: %v", err)
	}
	m := NewInstanceManager(root, codexUsageSleeperSpawner(`{"type":"turn.completed","usage":{"input_tokens":125,"output_tokens":0}}`, 750*time.Millisecond, 100*time.Millisecond))
	resolver := NewEventResolver(m, teamDir, mustParseTopo(t))

	result, err := resolver.EventWithResult("agent.dispatch", map[string]any{
		"target":         "worker",
		"ticket":         "SQU-106",
		"job_id":         "squ-106",
		"kickoff":        "extend before hard budget",
		"runtime":        "codex",
		"runtime_binary": "codex",
		"workspace":      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("dispatch event: %v", err)
	}
	running, err := jobstore.Read(teamDir, "squ-106")
	if err != nil {
		t.Fatalf("read running job: %v", err)
	}
	running.TokenBudget += 100
	running.TokenBudgetNotices = nil
	running.LastEvent = "budget_extended"
	running.LastStatus = "extended token budget by 100"
	running.UpdatedAt = time.Now().UTC()
	if err := jobstore.Write(teamDir, running); err != nil {
		t.Fatalf("extend token budget: %v", err)
	}
	instance := result.Outcomes[0].InstanceID
	if err := m.WaitForReaper(instance, 8*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
	meta, err := ReadMetadata(root, instance)
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta.Status != StatusExited {
		t.Fatalf("metadata status = %s, want exited", meta.Status)
	}
	updated, err := jobstore.Read(teamDir, "squ-106")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if updated.Status != jobstore.StatusDone || updated.TokenBudget != 200 {
		t.Fatalf("job after extended budget = %+v, want done with token budget 200", updated)
	}
	events, err := jobstore.ListEvents(teamDir, "squ-106")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, ev := range events {
		if ev.Type == "budget_exceeded_hard" {
			t.Fatalf("unexpected hard cutoff event after extension: %+v", events)
		}
	}
}
