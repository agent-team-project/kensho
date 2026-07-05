package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/allowance"
	jobstore "github.com/jamesaud/agent-team/internal/job"
	"github.com/jamesaud/agent-team/internal/jobwrite"
	"github.com/jamesaud/agent-team/internal/usage"
)

const budgetNoticePollInterval = time.Second

type budgetNotice struct {
	Dimension string
	Level     int
	Used      int64
	Budget    int64
	StepID    string
}

func (m *InstanceManager) startBudgetNoticeWatcher(meta Metadata, reaped <-chan struct{}) {
	if strings.TrimSpace(meta.Job) == "" || reaped == nil {
		return
	}
	go m.budgetNoticeWatcher(meta, reaped)
}

func (m *InstanceManager) budgetNoticeWatcher(meta Metadata, reaped <-chan struct{}) {
	ticker := time.NewTicker(budgetNoticePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-reaped:
			return
		case now := <-ticker.C:
			_ = m.checkBudgetNotices(meta, now.UTC())
		}
	}
}

func (m *InstanceManager) checkBudgetNotices(meta Metadata, now time.Time) error {
	teamDir := filepath.Dir(m.daemonRoot)
	j, err := jobstore.Read(teamDir, meta.Job)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	target := budgetNoticeTargetForInstance(j, meta.Instance)
	if target.tokenBudget <= 0 && target.timeBudget <= 0 {
		return nil
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
	if len(notices) == 0 {
		return nil
	}
	for _, notice := range notices {
		applyBudgetNoticeToJob(j, notice)
		message := budgetNoticeMessage(j.ID, meta.Instance, notice)
		j.LastEvent = "budget_notice"
		j.LastStatus = message
		j.UpdatedAt = now
		if err := jobwrite.WriteWithAudit(teamDir, j, jobwrite.Options{
			EventType: "budget_notice",
			Actor:     "daemon",
			Message:   message,
			Data:      budgetNoticeEventData(meta, notice),
		}); err != nil {
			return err
		}
		_ = AppendMessage(m.daemonRoot, meta.Instance, &Message{
			From: "agent-teamd",
			To:   meta.Instance,
			Body: budgetNoticeMailboxBody(j.ID, notice),
			TS:   now,
		})
	}
	return nil
}

type budgetNoticeTarget struct {
	stepID         string
	tokenBudget    int64
	timeBudget     time.Duration
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
			reminderLevels: step.ReminderLevels,
			tokenNotices:   step.TokenBudgetNotices,
			timeNotices:    step.TimeBudgetNotices,
		}
	}
	return budgetNoticeTarget{
		tokenBudget:    j.TokenBudget,
		timeBudget:     parseBudgetNoticeDuration(j.TimeBudget),
		reminderLevels: j.ReminderLevels,
		tokenNotices:   j.TokenBudgetNotices,
		timeNotices:    j.TimeBudgetNotices,
	}
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
