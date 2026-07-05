package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/agent-team-project/agent-team/internal/allowance"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/jobwrite"
	"github.com/agent-team-project/agent-team/internal/usage"
)

const budgetNoticePollInterval = time.Second

type budgetNotice struct {
	Dimension string
	Level     int
	Used      int64
	Budget    int64
	StepID    string
}

type budgetHardCutoff struct {
	Dimension  string
	Used       int64
	Budget     int64
	HardLimit  int64
	Multiplier float64
	StepID     string
}

func (m *InstanceManager) startBudgetNoticeWatcher(meta Metadata, proc *os.Process, reaped <-chan struct{}) {
	if strings.TrimSpace(meta.Job) == "" || reaped == nil {
		return
	}
	go m.budgetNoticeWatcher(meta, proc, reaped)
}

func (m *InstanceManager) budgetNoticeWatcher(meta Metadata, proc *os.Process, reaped <-chan struct{}) {
	ticker := time.NewTicker(budgetNoticePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-reaped:
			return
		case now := <-ticker.C:
			if !m.budgetNoticeInstanceRunning(meta.Instance, proc) {
				return
			}
			cutoff, _ := m.checkBudgetThresholds(meta, now.UTC())
			if cutoff != nil && m.enforceUsageBudgetCutoff(meta.Instance, proc, reaped, *cutoff) {
				return
			}
		}
	}
}

func (m *InstanceManager) budgetNoticeInstanceRunning(instance string, proc *os.Process) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.instances[instance]
	return ok && t.meta != nil && t.process == proc && t.meta.Status == StatusRunning
}

func (m *InstanceManager) checkBudgetNotices(meta Metadata, now time.Time) error {
	_, err := m.checkBudgetThresholds(meta, now)
	return err
}

func (m *InstanceManager) checkBudgetThresholds(meta Metadata, now time.Time) (*budgetHardCutoff, error) {
	teamDir := filepath.Dir(m.daemonRoot)
	j, err := jobstore.Read(teamDir, meta.Job)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	target := budgetNoticeTargetForInstance(j, meta.Instance)
	if target.tokenBudget <= 0 && target.timeBudget <= 0 {
		return nil, nil
	}
	rec := liveUsageForBudgetNotice(meta, now)
	var notices []budgetNotice
	if target.tokenBudget > 0 && rec.TokensAvailable {
		for _, level := range allowance.CrossedLevels(liveTokenTotal(rec), target.tokenBudget, target.reminderLevels, target.tokenNotices) {
			notices = append(notices, budgetNotice{Dimension: "tokens", Level: level, Used: liveTokenTotal(rec), Budget: target.tokenBudget, StepID: target.stepID})
		}
	}
	if target.timeBudget > 0 {
		used := int64(now.Sub(meta.StartedAt.UTC()).Milliseconds())
		budget := int64(target.timeBudget.Milliseconds())
		for _, level := range allowance.CrossedLevels(used, budget, target.reminderLevels, target.timeNotices) {
			notices = append(notices, budgetNotice{Dimension: "time", Level: level, Used: used, Budget: budget, StepID: target.stepID})
		}
	}
	cutoff := budgetHardCutoffForTarget(target, rec, meta, now)
	if len(notices) == 0 {
		if cutoff != nil {
			recorded, err := recordBudgetHardCutoff(teamDir, j, meta, *cutoff, now)
			if err != nil {
				return nil, err
			}
			if !recorded {
				return cutoff, nil
			}
		}
		return cutoff, nil
	}
	for _, notice := range notices {
		if latest, err := jobstore.Read(teamDir, j.ID); err == nil {
			j = latest
		} else {
			return nil, err
		}
		applyBudgetNoticeToJob(j, notice)
		message := budgetNoticeMessage(j.ID, meta.Instance, notice)
		if !budgetNoticeJobTerminal(j.Status) {
			j.LastEvent = "budget_notice"
			j.LastStatus = message
			j.UpdatedAt = now
		}
		if err := jobwrite.WriteWithAudit(teamDir, j, jobwrite.Options{
			EventType: "budget_notice",
			Actor:     "daemon",
			Message:   message,
			Data:      budgetNoticeEventData(meta, notice),
		}); err != nil {
			return nil, err
		}
		_ = AppendMessage(m.daemonRoot, meta.Instance, &Message{
			From: "agent-teamd",
			To:   meta.Instance,
			Body: budgetNoticeMailboxBody(j.ID, notice),
			TS:   now,
		})
	}
	if cutoff != nil {
		if _, err := recordBudgetHardCutoff(teamDir, j, meta, *cutoff, now); err != nil {
			return nil, err
		}
	}
	return cutoff, nil
}

type budgetNoticeTarget struct {
	stepID         string
	tokenBudget    int64
	timeBudget     time.Duration
	hardBudget     bool
	hardMultiplier float64
	reminderLevels []int
	tokenNotices   []int
	timeNotices    []int
}

func budgetNoticeTargetForInstance(j *jobstore.Job, instance string) budgetNoticeTarget {
	if j == nil {
		return budgetNoticeTarget{}
	}
	for i := range j.Steps {
		step := &j.Steps[i]
		if strings.TrimSpace(step.Instance) != strings.TrimSpace(instance) || step.Status != jobstore.StatusRunning {
			continue
		}
		return budgetNoticeTarget{
			stepID:         step.ID,
			tokenBudget:    step.TokenBudget,
			timeBudget:     parseBudgetNoticeDuration(step.TimeBudget),
			hardBudget:     step.HardBudget,
			hardMultiplier: step.HardMultiplier,
			reminderLevels: step.ReminderLevels,
			tokenNotices:   step.TokenBudgetNotices,
			timeNotices:    step.TimeBudgetNotices,
		}
	}
	return budgetNoticeTarget{
		tokenBudget:    j.TokenBudget,
		timeBudget:     parseBudgetNoticeDuration(j.TimeBudget),
		hardBudget:     j.HardBudget,
		hardMultiplier: j.HardMultiplier,
		reminderLevels: j.ReminderLevels,
		tokenNotices:   j.TokenBudgetNotices,
		timeNotices:    j.TimeBudgetNotices,
	}
}

func budgetHardCutoffForTarget(target budgetNoticeTarget, rec usage.Record, meta Metadata, now time.Time) *budgetHardCutoff {
	if !target.hardBudget && target.hardMultiplier <= 0 {
		return nil
	}
	if limit := allowance.HardLimit(target.tokenBudget, target.hardBudget, target.hardMultiplier); limit > 0 && rec.TokensAvailable {
		used := liveTokenTotal(rec)
		if used >= limit {
			return &budgetHardCutoff{Dimension: "tokens", Used: used, Budget: target.tokenBudget, HardLimit: limit, Multiplier: target.hardMultiplier, StepID: target.stepID}
		}
	}
	if limit := allowance.HardLimit(int64(target.timeBudget.Milliseconds()), target.hardBudget, target.hardMultiplier); limit > 0 {
		used := int64(now.Sub(meta.StartedAt.UTC()).Milliseconds())
		if used >= limit {
			return &budgetHardCutoff{Dimension: "time", Used: used, Budget: int64(target.timeBudget.Milliseconds()), HardLimit: limit, Multiplier: target.hardMultiplier, StepID: target.stepID}
		}
	}
	return nil
}

func parseBudgetNoticeDuration(raw string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

func liveUsageForBudgetNotice(meta Metadata, now time.Time) usage.Record {
	rec := usage.Record{
		Instance:   meta.Instance,
		Agent:      meta.Agent,
		Runtime:    meta.Runtime,
		StartedAt:  meta.StartedAt.UTC(),
		EndedAt:    now.UTC(),
		CapturedAt: now.UTC(),
		Source:     meta.LogPath,
		Origin:     meta.Origin,
	}
	if !rec.StartedAt.IsZero() && !rec.EndedAt.Before(rec.StartedAt) {
		rec.DurationMS = rec.EndedAt.Sub(rec.StartedAt).Milliseconds()
	}
	if !strings.EqualFold(meta.Runtime, "codex") {
		rec.TokensAvailable = false
		return rec
	}
	f, err := os.Open(meta.LogPath)
	if err != nil {
		return rec
	}
	defer f.Close()
	_ = usage.ParseCodexJSONL(&rec, f)
	return rec
}

func liveTokenTotal(rec usage.Record) int64 {
	return rec.InputTokens + rec.OutputTokens
}

func applyBudgetNoticeToJob(j *jobstore.Job, notice budgetNotice) {
	if j == nil {
		return
	}
	if notice.StepID != "" {
		for i := range j.Steps {
			if j.Steps[i].ID != notice.StepID {
				continue
			}
			if notice.Dimension == "tokens" {
				j.Steps[i].TokenBudgetNotices = allowance.MergeSentLevels(j.Steps[i].TokenBudgetNotices, notice.Level)
			} else {
				j.Steps[i].TimeBudgetNotices = allowance.MergeSentLevels(j.Steps[i].TimeBudgetNotices, notice.Level)
			}
			return
		}
	}
	if notice.Dimension == "tokens" {
		j.TokenBudgetNotices = allowance.MergeSentLevels(j.TokenBudgetNotices, notice.Level)
	} else {
		j.TimeBudgetNotices = allowance.MergeSentLevels(j.TimeBudgetNotices, notice.Level)
	}
}

func recordBudgetHardCutoff(teamDir string, j *jobstore.Job, meta Metadata, cutoff budgetHardCutoff, now time.Time) (bool, error) {
	if j == nil {
		return false, nil
	}
	if latest, err := jobstore.Read(teamDir, j.ID); err == nil {
		j = latest
	} else {
		return false, err
	}
	if budgetHardCutoffAlreadyRecorded(teamDir, j.ID, cutoff) {
		return false, nil
	}
	message := budgetHardCutoffMessage(j.ID, meta.Instance, cutoff)
	if !budgetNoticeJobTerminal(j.Status) {
		j.LastEvent = "budget_exceeded_hard"
		j.LastStatus = message
		j.UpdatedAt = now
	}
	if err := jobwrite.WriteWithAudit(teamDir, j, jobwrite.Options{
		EventType: "budget_exceeded_hard",
		Actor:     "daemon",
		Message:   message,
		Data:      budgetHardCutoffEventData(meta, cutoff),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func budgetNoticeJobTerminal(status jobstore.Status) bool {
	return status == jobstore.StatusDone || status == jobstore.StatusFailed
}

func budgetHardCutoffAlreadyRecorded(teamDir, jobID string, cutoff budgetHardCutoff) bool {
	events, err := jobstore.ListEvents(teamDir, jobID)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event.Type != "budget_exceeded_hard" {
			continue
		}
		if event.Data["dimension"] != cutoff.Dimension {
			continue
		}
		if cutoff.StepID != "" && event.Data["step"] != cutoff.StepID {
			continue
		}
		if cutoff.StepID == "" && event.Data["step"] != "" {
			continue
		}
		return true
	}
	return false
}

func budgetNoticeMessage(jobID, instance string, notice budgetNotice) string {
	subject := strings.TrimSpace(jobID)
	if notice.StepID != "" {
		subject += " step " + notice.StepID
	}
	if subject == "" {
		subject = strings.TrimSpace(instance)
	}
	return fmt.Sprintf("%s reached %d%% of %s budget (%s/%s)", subject, notice.Level, notice.Dimension, formatBudgetNoticeAmount(notice.Dimension, notice.Used), formatBudgetNoticeAmount(notice.Dimension, notice.Budget))
}

func budgetNoticeMailboxBody(jobID string, notice budgetNotice) string {
	if notice.Level >= 100 {
		return fmt.Sprintf("budget_notice: %s. This job is over allowance; budget_exceeded is warning-only and nothing has been stopped. Check `agent-team budget status --self`; request more token headroom with `agent-team job extend %s --tokens <amount>` when appropriate.", budgetNoticeMessage(jobID, "", notice), jobID)
	}
	return fmt.Sprintf("budget_notice: %s. Check `agent-team budget status --self`; request more token headroom with `agent-team job extend %s --tokens <amount>` when appropriate.", budgetNoticeMessage(jobID, "", notice), jobID)
}

func budgetNoticeEventData(meta Metadata, notice budgetNotice) map[string]string {
	data := map[string]string{
		"dimension": notice.Dimension,
		"level":     fmt.Sprint(notice.Level),
		"used":      fmt.Sprint(notice.Used),
		"budget":    fmt.Sprint(notice.Budget),
		"instance":  meta.Instance,
		"runtime":   meta.Runtime,
	}
	if notice.StepID != "" {
		data["step"] = notice.StepID
	}
	return data
}

func formatBudgetNoticeAmount(dimension string, value int64) string {
	if dimension == "time" {
		return (time.Duration(value) * time.Millisecond).String()
	}
	return fmt.Sprintf("%d tokens", value)
}

func budgetHardCutoffMessage(jobID, instance string, cutoff budgetHardCutoff) string {
	subject := strings.TrimSpace(jobID)
	if cutoff.StepID != "" {
		subject += " step " + cutoff.StepID
	}
	if subject == "" {
		subject = strings.TrimSpace(instance)
	}
	return fmt.Sprintf("%s exceeded hard %s budget (%s/%s hard line; allowance %s)", subject, cutoff.Dimension, formatBudgetNoticeAmount(cutoff.Dimension, cutoff.Used), formatBudgetNoticeAmount(cutoff.Dimension, cutoff.HardLimit), formatBudgetNoticeAmount(cutoff.Dimension, cutoff.Budget))
}

func budgetHardCutoffEventData(meta Metadata, cutoff budgetHardCutoff) map[string]string {
	data := map[string]string{
		"dimension":  cutoff.Dimension,
		"used":       fmt.Sprint(cutoff.Used),
		"budget":     fmt.Sprint(cutoff.Budget),
		"hard_limit": fmt.Sprint(cutoff.HardLimit),
		"instance":   meta.Instance,
		"runtime":    meta.Runtime,
	}
	if cutoff.Multiplier > 0 {
		data["hard_multiplier"] = fmt.Sprintf("%g", cutoff.Multiplier)
	} else {
		data["hard"] = "true"
	}
	if cutoff.StepID != "" {
		data["step"] = cutoff.StepID
	}
	return data
}

func (m *InstanceManager) enforceUsageBudgetCutoff(instance string, proc *os.Process, reaped <-chan struct{}, cutoff budgetHardCutoff) bool {
	if proc == nil {
		return false
	}
	out, pid, ok := m.markInstanceCrashedForBudgetCutoff(instance, proc, cutoff, false)
	if !ok {
		return false
	}
	m.recordEvent("watchdog", &out, budgetHardCutoffMessage(out.Job, out.Instance, cutoff)+"; killing")
	_ = signalProcessGroupOrProcess(proc, pid, syscall.SIGTERM)
	if waitForProcessExit(pid, reaped, stopKillWaitTimeout) {
		return true
	}
	_ = signalProcessGroupOrProcess(proc, pid, syscall.SIGKILL)
	return true
}

func (m *InstanceManager) markReapedInstanceCrashedForBudgetCutoff(instance string, proc *os.Process, cutoff budgetHardCutoff) (*Metadata, bool) {
	out, _, ok := m.markInstanceCrashedForBudgetCutoff(instance, proc, cutoff, true)
	if !ok {
		return nil, false
	}
	return &out, true
}

func (m *InstanceManager) markInstanceCrashedForBudgetCutoff(instance string, proc *os.Process, cutoff budgetHardCutoff, allowExited bool) (Metadata, int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.instances[instance]
	if !ok || t.meta == nil || t.process != proc {
		return Metadata{}, 0, false
	}
	switch t.meta.Status {
	case StatusRunning:
	case StatusExited:
		if !allowExited {
			return Metadata{}, 0, false
		}
	case StatusCrashed:
		out := *t.meta
		return out, t.meta.PID, true
	default:
		return Metadata{}, 0, false
	}
	t.meta.Status = StatusCrashed
	out := *t.meta
	if err := WriteMetadata(m.daemonRoot, t.meta); err != nil {
		_ = err
	}
	return out, t.meta.PID, true
}
