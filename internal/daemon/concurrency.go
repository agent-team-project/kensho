package daemon

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/topology"
)

const (
	defaultConcurrencyTargetLoadPerCore = 0.85
	defaultConcurrencyLoadPerDispatch   = 1.0
	defaultConcurrencyCrashWindow       = 10 * time.Minute
	defaultConcurrencyCrashThreshold    = 3
	defaultConcurrencyDecreaseFactor    = 0.5
	defaultConcurrencyStableWindow      = 20 * time.Minute
	defaultConcurrencyIncreaseStep      = 1
	concurrencyDrainPollInterval        = 5 * time.Second
)

type machineLoadSample struct {
	Load1 float64
	Cores int
}

type machineLoadSampler func() (machineLoadSample, error)

type concurrencyConfig struct {
	enabled           bool
	minCeiling        int
	maxCeiling        int
	initialCeiling    int
	targetLoadPerCore float64
	loadPerDispatch   float64
	crashWindow       time.Duration
	crashThreshold    int
	decreaseFactor    float64
	stableWindow      time.Duration
	increaseStep      int
}

type concurrencyController struct {
	cfg            concurrencyConfig
	current        int
	lastEffective  int
	crashes        []time.Time
	lastCrash      time.Time
	lastAdjustment time.Time
	sampler        machineLoadSampler
}

type concurrencyAdmission struct {
	Allowed      bool
	Reason       string
	Ceiling      int
	CeilingLoad  float64
	Running      int
	RunningLoad  float64
	IncomingLoad float64
}

func newConcurrencyController(raw *topology.Concurrency) *concurrencyController {
	cfg, ok := resolveConcurrencyConfig(raw)
	if !ok {
		return nil
	}
	return &concurrencyController{
		cfg:           cfg,
		current:       cfg.initialCeiling,
		lastEffective: -1,
		sampler:       defaultMachineLoadSample,
	}
}

func resolveConcurrencyConfig(raw *topology.Concurrency) (concurrencyConfig, bool) {
	if raw == nil || !raw.Enabled {
		return concurrencyConfig{}, false
	}
	maxCeiling := raw.MaxCeiling
	if maxCeiling <= 0 {
		maxCeiling = runtime.NumCPU()
	}
	if maxCeiling < 1 {
		maxCeiling = 1
	}
	minCeiling := raw.MinCeiling
	if minCeiling <= 0 {
		minCeiling = 1
	}
	if minCeiling > maxCeiling {
		minCeiling = maxCeiling
	}
	initialCeiling := raw.InitialCeiling
	if initialCeiling <= 0 {
		initialCeiling = maxCeiling
	}
	initialCeiling = clampInt(initialCeiling, minCeiling, maxCeiling)
	targetLoadPerCore := raw.TargetLoadPerCore
	if targetLoadPerCore <= 0 {
		targetLoadPerCore = defaultConcurrencyTargetLoadPerCore
	}
	loadPerDispatch := raw.LoadPerDispatch
	if loadPerDispatch <= 0 {
		loadPerDispatch = defaultConcurrencyLoadPerDispatch
	}
	crashWindow := raw.CrashWindow
	if crashWindow <= 0 {
		crashWindow = defaultConcurrencyCrashWindow
	}
	crashThreshold := raw.CrashThreshold
	if crashThreshold <= 0 {
		crashThreshold = defaultConcurrencyCrashThreshold
	}
	decreaseFactor := raw.DecreaseFactor
	if decreaseFactor <= 0 || decreaseFactor >= 1 {
		decreaseFactor = defaultConcurrencyDecreaseFactor
	}
	stableWindow := raw.StableWindow
	if stableWindow <= 0 {
		stableWindow = defaultConcurrencyStableWindow
	}
	increaseStep := raw.IncreaseStep
	if increaseStep <= 0 {
		increaseStep = defaultConcurrencyIncreaseStep
	}
	return concurrencyConfig{
		enabled:           true,
		minCeiling:        minCeiling,
		maxCeiling:        maxCeiling,
		initialCeiling:    initialCeiling,
		targetLoadPerCore: targetLoadPerCore,
		loadPerDispatch:   loadPerDispatch,
		crashWindow:       crashWindow,
		crashThreshold:    crashThreshold,
		decreaseFactor:    decreaseFactor,
		stableWindow:      stableWindow,
		increaseStep:      increaseStep,
	}, true
}

func (c *concurrencyController) updateConfig(raw *topology.Concurrency) bool {
	cfg, ok := resolveConcurrencyConfig(raw)
	if !ok {
		return false
	}
	c.cfg = cfg
	c.current = clampInt(c.current, cfg.minCeiling, cfg.maxCeiling)
	if c.current == 0 {
		c.current = cfg.initialCeiling
	}
	return true
}

func (c *concurrencyController) admit(now time.Time, running int, runningLoad, incomingLoad float64) (concurrencyAdmission, *LifecycleEvent) {
	if c == nil || !c.cfg.enabled {
		runningLoad = normalizeConcurrencyRunningLoad(running, runningLoad)
		incomingLoad = normalizeConcurrencyLoad(incomingLoad)
		return concurrencyAdmission{Allowed: true, Ceiling: math.MaxInt, CeilingLoad: math.MaxFloat64, Running: running, RunningLoad: runningLoad, IncomingLoad: incomingLoad}, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	increaseReason := c.maybeIncrease(now)
	admission := c.admissionForEffective(running, runningLoad, incomingLoad, increaseReason)
	return admission, c.recordEffectiveChange(now, admission.Ceiling, admission.Reason)
}

func (c *concurrencyController) preview(now time.Time, running int, runningLoad, incomingLoad float64) concurrencyAdmission {
	if c == nil || !c.cfg.enabled {
		runningLoad = normalizeConcurrencyRunningLoad(running, runningLoad)
		incomingLoad = normalizeConcurrencyLoad(incomingLoad)
		return concurrencyAdmission{Allowed: true, Ceiling: math.MaxInt, CeilingLoad: math.MaxFloat64, Running: running, RunningLoad: runningLoad, IncomingLoad: incomingLoad}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return c.admissionForEffective(running, runningLoad, incomingLoad, "")
}

func (c *concurrencyController) observeCrash(now time.Time, running int, runningLoad float64) *LifecycleEvent {
	if c == nil || !c.cfg.enabled || c.cfg.crashThreshold <= 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	c.lastCrash = now
	c.crashes = appendRecentTimes(c.crashes, now, c.cfg.crashWindow)
	c.crashes = append(c.crashes, now)
	if len(c.crashes) < c.cfg.crashThreshold {
		return nil
	}
	old := c.current
	next := int(math.Floor(float64(c.current) * c.cfg.decreaseFactor))
	if next >= c.current {
		next = c.current - 1
	}
	next = clampInt(next, c.cfg.minCeiling, c.cfg.maxCeiling)
	c.crashes = nil
	c.lastAdjustment = now
	if next == old {
		return nil
	}
	c.current = next
	reason := fmt.Sprintf("AIMD decrease after %d crashes in %s", c.cfg.crashThreshold, c.cfg.crashWindow)
	admission := c.admissionForEffective(running, runningLoad, 0, reason)
	return c.recordEffectiveChange(now, admission.Ceiling, reason)
}

func (c *concurrencyController) maybeIncrease(now time.Time) string {
	if c.current >= c.cfg.maxCeiling || c.cfg.increaseStep <= 0 || c.cfg.stableWindow <= 0 || c.lastCrash.IsZero() {
		return ""
	}
	if now.Sub(c.lastCrash) < c.cfg.stableWindow {
		return ""
	}
	if !c.lastAdjustment.IsZero() && now.Sub(c.lastAdjustment) < c.cfg.stableWindow {
		return ""
	}
	old := c.current
	c.current = clampInt(c.current+c.cfg.increaseStep, c.cfg.minCeiling, c.cfg.maxCeiling)
	c.lastAdjustment = now
	if c.current == old {
		return ""
	}
	return fmt.Sprintf("AIMD increase after %s stable", c.cfg.stableWindow)
}

func (c *concurrencyController) admissionForEffective(running int, runningLoad, incomingLoad float64, preferredReason string) concurrencyAdmission {
	runningLoad = normalizeConcurrencyRunningLoad(running, runningLoad)
	incomingLoad = normalizeConcurrencyLoad(incomingLoad)
	ceilingLoad, reason := c.effectiveCeiling(runningLoad)
	if strings.TrimSpace(preferredReason) != "" && math.Abs(ceilingLoad-float64(c.current)) < 0.000001 {
		reason = preferredReason
	}
	return concurrencyAdmission{
		Allowed:      runningLoad+incomingLoad <= ceilingLoad+0.000001,
		Reason:       reason,
		Ceiling:      int(math.Floor(ceilingLoad)),
		CeilingLoad:  ceilingLoad,
		Running:      running,
		RunningLoad:  runningLoad,
		IncomingLoad: incomingLoad,
	}
}

func (c *concurrencyController) effectiveCeiling(runningLoad float64) (float64, string) {
	machineCeiling, reason, ok := c.machineCeiling(runningLoad)
	if !ok {
		return float64(c.current), "AIMD ceiling"
	}
	if machineCeiling < float64(c.current) {
		return machineCeiling, reason
	}
	return float64(c.current), "AIMD ceiling"
}

func (c *concurrencyController) machineCeiling(runningLoad float64) (float64, string, bool) {
	if c.sampler == nil {
		return 0, "", false
	}
	sample, err := c.sampler()
	if err != nil || sample.Cores <= 0 || sample.Load1 < 0 {
		return 0, "", false
	}
	targetLoad := c.cfg.targetLoadPerCore * float64(sample.Cores)
	headroom := targetLoad - sample.Load1
	ceiling := runningLoad
	if headroom > 0 {
		ceiling = runningLoad + headroom/c.cfg.loadPerDispatch
	}
	ceiling = math.Max(0, ceiling)
	if ceiling > float64(c.cfg.maxCeiling) {
		ceiling = float64(c.cfg.maxCeiling)
	}
	reason := fmt.Sprintf("load average %.2f/%d cores", sample.Load1, sample.Cores)
	return ceiling, reason, true
}

func (c *concurrencyController) recordEffectiveChange(now time.Time, ceiling int, reason string) *LifecycleEvent {
	if c == nil {
		return nil
	}
	if c.lastEffective == ceiling {
		return nil
	}
	c.lastEffective = ceiling
	return &LifecycleEvent{
		Action:  "concurrency_ceiling_adjusted",
		TS:      now,
		Message: fmt.Sprintf("concurrency ceiling adjusted to %d (%s)", ceiling, reason),
	}
}

func appendRecentTimes(times []time.Time, now time.Time, window time.Duration) []time.Time {
	if window <= 0 {
		return nil
	}
	cutoff := now.Add(-window)
	out := times[:0]
	for _, ts := range times {
		if ts.After(cutoff) || ts.Equal(cutoff) {
			out = append(out, ts)
		}
	}
	return out
}

func normalizeConcurrencyRunningLoad(running int, load float64) float64 {
	if load > 0 {
		return load
	}
	if running <= 0 {
		return 0
	}
	return float64(running)
}

func normalizeConcurrencyLoad(load float64) float64 {
	if load > 0 {
		return load
	}
	return 1
}

func formatConcurrencyLoad(load float64) string {
	if math.Abs(load-math.Round(load)) < 0.000001 {
		return fmt.Sprintf("%.0f", load)
	}
	return fmt.Sprintf("%.2f", load)
}

func defaultMachineLoadSample() (machineLoadSample, error) {
	load, err := readProcLoadavg()
	if err != nil {
		load, err = readSysctlLoadavg()
	}
	if err != nil {
		return machineLoadSample{}, err
	}
	cores := runtime.NumCPU()
	if cores < 1 {
		cores = 1
	}
	return machineLoadSample{Load1: load, Cores: cores}, nil
}

func readProcLoadavg() (float64, error) {
	body, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return 0, fmt.Errorf("/proc/loadavg: missing load fields")
	}
	return strconv.ParseFloat(fields[0], 64)
}

func readSysctlLoadavg() (float64, error) {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0, err
	}
	cleaned := strings.NewReplacer("{", " ", "}", " ", ",", " ").Replace(string(out))
	fields := strings.Fields(cleaned)
	if len(fields) == 0 {
		return 0, fmt.Errorf("sysctl vm.loadavg: missing load fields")
	}
	return strconv.ParseFloat(fields[0], 64)
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
