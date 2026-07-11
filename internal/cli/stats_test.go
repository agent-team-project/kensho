package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
)

func TestStatsDefaultShowsRunningInstancesOnly(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker-done", Agent: "worker", Status: daemon.StatusExited, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		if pid != 10 {
			t.Fatalf("probe called for pid %d, want only running pid 10", pid)
		}
		return processStats{CPUPercent: 2.5, MemoryPercent: 1.2, RSSKiB: 12_800}, nil
	}

	var buf bytes.Buffer
	if err := runStats(&buf, lister, nil, statsOptions{}, now, probe); err != nil {
		t.Fatalf("runStats: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "manager") || !strings.Contains(out, "2.5") || !strings.Contains(out, "12.5MiB") {
		t.Fatalf("stats output missing running metrics: %q", out)
	}
	if strings.Contains(out, "worker-done") {
		t.Fatalf("default stats should not include exited rows: %q", out)
	}
}

func TestStatsLatestSelectsNewestRunningInstance(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "old-running", Agent: "worker", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new-running", Agent: "worker", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "newer-stopped", Agent: "worker", Status: daemon.StatusStopped, PID: 12, StartedAt: now.Add(-1 * time.Minute)},
	}}}
	probed := map[int]bool{}
	probe := func(pid int) (processStats, error) {
		probed[pid] = true
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}

	rows, err := collectStatsRows(lister, nil, statsOptions{Latest: true}, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Instance != "new-running" {
		t.Fatalf("rows = %+v, want newest running instance", rows)
	}
	if probed[10] || !probed[11] || probed[12] {
		t.Fatalf("probed pids = %+v, want only newest running pid", probed)
	}
}

func TestStatsLastSelectsNewestRunningInstances(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "old-running", Agent: "worker", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid-running", Agent: "worker", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new-running", Agent: "worker", Status: daemon.StatusRunning, PID: 12, StartedAt: now.Add(-1 * time.Hour)},
		{Instance: "newer-stopped", Agent: "worker", Status: daemon.StatusStopped, PID: 13, StartedAt: now.Add(-5 * time.Minute)},
	}}}
	probed := map[int]bool{}
	probe := func(pid int) (processStats, error) {
		probed[pid] = true
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}

	rows, err := collectStatsRows(lister, nil, statsOptions{Limit: 2}, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 2 || rows[0].Instance != "new-running" || rows[1].Instance != "mid-running" {
		t.Fatalf("rows = %+v, want newest two running instances", rows)
	}
	if probed[10] || !probed[11] || !probed[12] || probed[13] {
		t.Fatalf("probed pids = %+v, want only newest two running pids", probed)
	}
}

func TestStatsLatestWithAllCanSelectStoppedInstance(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "running", Agent: "worker", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "stopped", Agent: "worker", Status: daemon.StatusStopped, PID: 11, StartedAt: now.Add(-5 * time.Minute)},
	}}}
	rows, err := collectStatsRows(lister, nil, statsOptions{All: true, Latest: true}, now, func(pid int) (processStats, error) {
		t.Fatalf("probe should not run for selected stopped pid %d", pid)
		return processStats{}, nil
	})
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Instance != "stopped" || rows[0].StatsAvailable {
		t.Fatalf("rows = %+v, want newest stopped instance without metrics", rows)
	}
}

func TestStatsAllJSONIncludesStoppedWithoutMetrics(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
	}}}
	probeCalls := 0
	probe := func(pid int) (processStats, error) {
		probeCalls++
		return processStats{CPUPercent: 0.5, MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}

	var buf bytes.Buffer
	if err := runStatsJSON(&buf, lister, nil, statsOptions{All: true}, now, probe); err != nil {
		t.Fatalf("runStatsJSON: %v", err)
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("decode stats json: %v\nbody=%s", err, buf.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two", rows)
	}
	if probeCalls != 1 {
		t.Fatalf("probeCalls = %d, want one running probe", probeCalls)
	}
	if rows[0].Instance != "manager" || rows[0].CPUPercent == nil || rows[0].RSSBytes != 1024*1024 {
		t.Fatalf("running row = %+v", rows[0])
	}
	if rows[1].Instance != "worker" || rows[1].CPUPercent != nil || rows[1].RSSBytes != 0 {
		t.Fatalf("stopped row should not include metrics: %+v", rows[1])
	}
}

func TestStatsSortByCPUDescending(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "alpha", Agent: "worker", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "beta", Agent: "manager", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
		{Instance: "gamma", Agent: "worker", Status: daemon.StatusStopped, PID: 12, StartedAt: now.Add(-15 * time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		switch pid {
		case 10:
			return processStats{CPUPercent: 1.0, MemoryPercent: 0.2, RSSKiB: 100}, nil
		case 11:
			return processStats{CPUPercent: 9.0, MemoryPercent: 0.8, RSSKiB: 900}, nil
		default:
			t.Fatalf("probe called for stopped pid %d", pid)
			return processStats{}, nil
		}
	}

	rows, err := collectStatsRows(lister, nil, statsOptions{All: true, Sort: statsSortCPU, SortSet: true}, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	got := []string{rows[0].Instance, rows[1].Instance, rows[2].Instance}
	if strings.Join(got, ",") != "beta,alpha,gamma" {
		t.Fatalf("sorted rows = %v, want beta,alpha,gamma", got)
	}
}

func TestStatsSortByPhase(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "alpha", Agent: "worker", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "beta", Agent: "manager", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
		{Instance: "gamma", Agent: "worker", Status: daemon.StatusRunning, PID: 12, StartedAt: now.Add(-15 * time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1.0, MemoryPercent: 0.2, RSSKiB: 100}, nil
	}

	rows, err := collectStatsRows(lister, nil, statsOptions{
		All:             true,
		Sort:            statsSortPhase,
		SortSet:         true,
		phaseByInstance: map[string]string{"alpha": "idle", "beta": "blocked"},
	}, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	got := []string{rows[0].Instance, rows[1].Instance, rows[2].Instance}
	if strings.Join(got, ",") != "beta,alpha,gamma" {
		t.Fatalf("phase-sorted rows = %v, want beta,alpha,gamma", got)
	}
}

func TestStatsSortByStale(t *testing.T) {
	rows := []statsRow{
		{Instance: "fresh", Status: "running"},
		{Instance: "stale-b", Status: "running", Stale: true},
		{Instance: "stale-a", Status: "running", Stale: true},
	}
	sortStatsRows(rows, statsSortStale)
	if got := rows[0].Instance + "," + rows[1].Instance + "," + rows[2].Instance; got != "stale-a,stale-b,fresh" {
		t.Fatalf("stale-sorted rows = %s, want stale-a,stale-b,fresh", got)
	}
}

func TestStatsSortByRuntimeStale(t *testing.T) {
	sortMode, err := parseStatsSort("runtime-stale")
	if err != nil {
		t.Fatalf("parseStatsSort runtime-stale: %v", err)
	}
	aliasMode, err := parseStatsSortFlag("runtime_stale", "--stats-sort")
	if err != nil {
		t.Fatalf("parseStatsSortFlag runtime_stale: %v", err)
	}
	if aliasMode != sortMode {
		t.Fatalf("runtime_stale alias = %q, want %q", aliasMode, sortMode)
	}
	rows := []statsRow{
		{Instance: "fresh", Status: "running"},
		{Instance: "status-stale", Status: "running", Stale: true},
		{Instance: "runtime-b", Status: "running", RuntimeStale: true},
		{Instance: "runtime-a", Status: "running", RuntimeStale: true},
	}
	sortStatsRows(rows, sortMode)
	if got := rows[0].Instance + "," + rows[1].Instance + "," + rows[2].Instance + "," + rows[3].Instance; got != "runtime-a,runtime-b,fresh,status-stale" {
		t.Fatalf("runtime-stale-sorted rows = %s, want runtime-a,runtime-b,fresh,status-stale", got)
	}
}

func TestStatsSortByUnhealthy(t *testing.T) {
	rows := []statsRow{
		{Instance: "fresh", Status: "running"},
		{Instance: "stale", Status: "running", Stale: true},
		{Instance: "crashed", Status: string(daemon.StatusCrashed)},
	}
	sortStatsRows(rows, statsSortUnhealthy)
	if got := rows[0].Instance + "," + rows[1].Instance + "," + rows[2].Instance; got != "crashed,stale,fresh" {
		t.Fatalf("unhealthy-sorted rows = %s, want crashed,stale,fresh", got)
	}
}

func TestStatsExplicitNamesPreserveOrderUnlessSortSet(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "alpha", Agent: "worker", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "beta", Agent: "manager", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: int64(pid)}, nil
	}

	rows, err := collectStatsRows(lister, []string{"beta", "alpha"}, statsOptions{Sort: statsSortName}, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if got := rows[0].Instance + "," + rows[1].Instance; got != "beta,alpha" {
		t.Fatalf("default explicit order = %s, want beta,alpha", got)
	}

	rows, err = collectStatsRows(lister, []string{"beta", "alpha"}, statsOptions{Sort: statsSortName, SortSet: true}, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows sorted: %v", err)
	}
	if got := rows[0].Instance + "," + rows[1].Instance; got != "alpha,beta" {
		t.Fatalf("explicit sorted order = %s, want alpha,beta", got)
	}
}

func TestStatsSortRejectsUnknownValue(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "--sort", "latency"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected unknown --sort validation error")
	}
	if !strings.Contains(stderr.String(), "unknown --sort") {
		t.Fatalf("stderr = %q, want unknown --sort", stderr.String())
	}
}

func TestStatsLatestRejectsInstanceNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "manager", "--latest"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected latest/name validation error")
	}
	if !strings.Contains(stderr.String(), "--latest cannot be combined with instance names") {
		t.Fatalf("stderr = %q, want latest/name validation", stderr.String())
	}
}

func TestStatsUnhealthyRejectsInstanceNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "manager", "--unhealthy"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected unhealthy/name validation error")
	}
	if !strings.Contains(stderr.String(), "--unhealthy cannot be combined with instance names") {
		t.Fatalf("stderr = %q, want unhealthy/name validation", stderr.String())
	}
}

func TestStatsRejectsInvalidLatestLastOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "negative-last",
			args: []string{"stats", "--last", "-1"},
			want: "--last must be >= 0",
		},
		{
			name: "latest-and-last",
			args: []string{"stats", "--latest", "--last", "2"},
			want: "choose one of --latest or --last",
		},
		{
			name: "last-and-name",
			args: []string{"stats", "manager", "--last", "2"},
			want: "--last cannot be combined with instance names",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			stderr := &bytes.Buffer{}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(stderr)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestStatsRowsNormalizeMissingStatus(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", PID: 10, StartedAt: now.Add(-5 * time.Minute)},
	}}}
	opts, err := newStatsOptions(false, []string{"unknown"}, nil)
	if err != nil {
		t.Fatalf("newStatsOptions: %v", err)
	}

	rows, err := collectStatsRows(lister, nil, opts, now, func(pid int) (processStats, error) {
		t.Fatalf("probe should not run for unknown status, got pid %d", pid)
		return processStats{}, nil
	})
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != "unknown" {
		t.Fatalf("rows = %+v, want unknown status", rows)
	}

	jsonRows := statsJSONRows(rows)
	if len(jsonRows) != 1 || jsonRows[0].Status != "unknown" {
		t.Fatalf("json rows = %+v, want unknown status", jsonRows)
	}
	formatRows := statsFormatRows(rows)
	if len(formatRows) != 1 || formatRows[0].Status != "unknown" {
		t.Fatalf("format rows = %+v, want unknown status", formatRows)
	}
	summary := summarizeStatsRows(rows)
	if summary.Total != 1 || summary.Unknown != 1 || summary.Running != 0 {
		t.Fatalf("summary = %+v, want one unknown row", summary)
	}
	if summary.Phases["unknown"] != 1 {
		t.Fatalf("summary phases = %+v, want one unknown phase", summary.Phases)
	}
}

func TestStatsCommandUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	if err := daemon.WriteMetadata(daemon.DaemonRoot(teamDir), &daemon.Metadata{
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusStopped,
		PID:      123,
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "--all", "--json", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stats local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stats json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || rows[0].Status != "stopped" || rows[0].CPUPercent != nil {
		t.Fatalf("rows = %+v, want stopped manager without metrics", rows)
	}
}

func TestStatsCommandLastJSONLimitsRowsByLatestStarted(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Hour)},
		{Instance: "mid", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-1 * time.Hour)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "--json", "--last", "2", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stats --json --last 2: %v\nstderr=%s", err, stderr.String())
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stats json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "new" || rows[1].Instance != "mid" {
		t.Fatalf("rows = %+v, want newest two instances", rows)
	}
}

func TestStatsFormatRendersRows(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		if pid != 10 {
			t.Fatalf("probe called for pid %d, want only running pid 10", pid)
		}
		return processStats{CPUPercent: 2.5, MemoryPercent: 1.2, RSSKiB: 12_800}, nil
	}
	tmpl, err := parseStatsFormat(`{{.Instance}} {{.Status}} {{.CPUPercent}} {{.RSS}} {{.Measured}}`)
	if err != nil {
		t.Fatalf("parseStatsFormat: %v", err)
	}

	var buf bytes.Buffer
	if err := runStatsFormat(&buf, lister, nil, statsOptions{All: true}, now, probe, tmpl); err != nil {
		t.Fatalf("runStatsFormat: %v", err)
	}
	want := "manager running 2.5 12.5MiB true\nworker stopped   false\n"
	if got := buf.String(); got != want {
		t.Fatalf("formatted stats = %q, want %q", got, want)
	}
}

func TestStatsFormatRejectsInvalidTemplate(t *testing.T) {
	_, err := parseStatsFormat(`{{.Instance`)
	if err == nil || !strings.Contains(err.Error(), "invalid --format template") {
		t.Fatalf("err = %v, want invalid template", err)
	}
}

func TestStatsFormatRejectsConflictingOutputModes(t *testing.T) {
	for _, args := range [][]string{
		{"stats", "--format", "{{.Instance}}", "--json"},
		{"stats", "--format", "{{.Instance}}", "--summary"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", args)
		}
		if !strings.Contains(stderr.String(), "--format cannot be combined") {
			t.Fatalf("%v: stderr = %q, want format conflict", args, stderr.String())
		}
	}
}

func TestStatsSummaryAggregatesMeasuredRows(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
		{Instance: "stopped", Agent: "worker", Status: daemon.StatusStopped, PID: 12, StartedAt: now.Add(-15 * time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		switch pid {
		case 10:
			return processStats{CPUPercent: 1.5, MemoryPercent: 0.5, RSSKiB: 1024}, nil
		case 11:
			return processStats{CPUPercent: 2.0, MemoryPercent: 0.7, RSSKiB: 2048}, nil
		default:
			t.Fatalf("probe called for stopped pid %d", pid)
			return processStats{}, nil
		}
	}

	var buf bytes.Buffer
	opts := statsOptions{
		All: true,
		phaseByInstance: map[string]string{
			"manager": "idle",
			"worker":  "blocked",
			"stopped": "idle",
		},
	}
	if err := runStatsSummary(&buf, lister, nil, opts, now, probe); err != nil {
		t.Fatalf("runStatsSummary: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"total=3", "running=2", "stopped=1", "measured=2", "cpu=3.5%", "mem=1.2%", "rss=3.0MiB", "phases:", "blocked=1", "idle=2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary output missing %q:\n%s", want, out)
		}
	}
}

func TestStatsSummaryJSONAggregatesErrors(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		if pid == 11 {
			return processStats{}, errors.New("missing process")
		}
		return processStats{CPUPercent: 1.5, MemoryPercent: 0.5, RSSKiB: 1024}, nil
	}

	var buf bytes.Buffer
	if err := runStatsSummaryJSON(&buf, lister, nil, statsOptions{}, now, probe); err != nil {
		t.Fatalf("runStatsSummaryJSON: %v", err)
	}
	var summary statsSummaryJSON
	if err := json.Unmarshal(buf.Bytes(), &summary); err != nil {
		t.Fatalf("decode stats summary json: %v\nbody=%s", err, buf.String())
	}
	if summary.Total != 2 || summary.Running != 2 || summary.Measured != 1 || summary.Errors != 1 {
		t.Fatalf("summary counts = %+v, want total=2 running=2 measured=1 errors=1", summary)
	}
	if summary.CPUPercent != 1.5 || summary.MemoryPercent != 0.5 || summary.RSSBytes != 1024*1024 || summary.RSS != "1.0MiB" {
		t.Fatalf("summary metrics = %+v, want one measured row", summary)
	}
	if summary.Phases["unknown"] != 2 {
		t.Fatalf("summary phases = %+v, want two unknown rows", summary.Phases)
	}
}

func TestStatsFiltersByAgent(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "adhoc", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusRunning, PID: 12, StartedAt: now.Add(-2 * time.Minute)},
	}}}
	probed := map[int]bool{}
	probe := func(pid int) (processStats, error) {
		probed[pid] = true
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}
	opts, err := newStatsOptions(false, nil, []string{"manager"})
	if err != nil {
		t.Fatalf("newStatsOptions: %v", err)
	}

	rows, err := collectStatsRows(lister, nil, opts, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 2 || rows[0].Instance != "adhoc" || rows[1].Instance != "manager" {
		t.Fatalf("rows = %+v, want adhoc and manager only", rows)
	}
	if !probed[10] || !probed[11] || probed[12] {
		t.Fatalf("probed pids = %+v, want manager-agent pids only", probed)
	}
}

func TestStatsFiltersByRuntime(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "codex-worker", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex-dev", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "claude-manager", Agent: "manager", Runtime: "claude", RuntimeBinary: "claude-code", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "unknown-runtime", Agent: "worker", Status: daemon.StatusRunning, PID: 12, StartedAt: now.Add(-2 * time.Minute)},
	}}}
	probed := map[int]bool{}
	probe := func(pid int) (processStats, error) {
		probed[pid] = true
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}
	opts, err := newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(false, nil, []string{"codex"}, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy: %v", err)
	}

	rows, err := collectStatsRows(lister, nil, opts, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Instance != "codex-worker" || rows[0].Runtime != "codex" || rows[0].RuntimeBinary != "codex-dev" {
		t.Fatalf("rows = %+v, want codex worker only", rows)
	}
	if !probed[10] || probed[11] || probed[12] {
		t.Fatalf("probed pids = %+v, want codex pid only", probed)
	}
	jsonRows := statsJSONRows(rows)
	if len(jsonRows) != 1 || jsonRows[0].Runtime != "codex" || jsonRows[0].RuntimeBinary != "codex-dev" {
		t.Fatalf("json rows = %+v, want codex runtime metadata", jsonRows)
	}
	formatRows := statsFormatRows(rows)
	if len(formatRows) != 1 || formatRows[0].Runtime != "codex" || formatRows[0].RuntimeBinary != "codex-dev" {
		t.Fatalf("format rows = %+v, want codex runtime metadata", formatRows)
	}
}

func TestStatsFiltersByInstance(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "adhoc", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "ticket-manager", Agent: "ticket-manager", Status: daemon.StatusRunning, PID: 12, StartedAt: now.Add(-2 * time.Minute)},
	}}}
	probed := map[int]bool{}
	probe := func(pid int) (processStats, error) {
		probed[pid] = true
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}
	opts, err := newStatsOptionsWithInstances(false, nil, nil, []string{"manager,ticket-manager"})
	if err != nil {
		t.Fatalf("newStatsOptionsWithInstances: %v", err)
	}

	rows, err := collectStatsRows(lister, nil, opts, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 2 || rows[0].Instance != "manager" || rows[1].Instance != "ticket-manager" {
		t.Fatalf("rows = %+v, want manager and ticket-manager only", rows)
	}
	if probed[10] || !probed[11] || !probed[12] {
		t.Fatalf("probed pids = %+v, want selected instance pids only", probed)
	}
}

func TestStatsFiltersByPhase(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-5 * time.Minute)},
	}}}
	probed := map[int]bool{}
	probe := func(pid int) (processStats, error) {
		probed[pid] = true
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}
	opts, err := newStatsOptionsWithInstancesAndPhases(false, nil, nil, []string{"blocked"}, nil)
	if err != nil {
		t.Fatalf("newStatsOptionsWithInstancesAndPhases: %v", err)
	}
	opts.phaseByInstance = map[string]string{"manager": "idle", "worker": "blocked"}

	rows, err := collectStatsRows(lister, nil, opts, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Instance != "worker" || rows[0].Phase != "blocked" {
		t.Fatalf("rows = %+v, want blocked worker only", rows)
	}
	if probed[10] || !probed[11] {
		t.Fatalf("probed pids = %+v, want blocked worker pid only", probed)
	}
	jsonRows := statsJSONRows(rows)
	if len(jsonRows) != 1 || jsonRows[0].Phase != "blocked" {
		t.Fatalf("json rows = %+v, want blocked phase", jsonRows)
	}
	formatRows := statsFormatRows(rows)
	if len(formatRows) != 1 || formatRows[0].Phase != "blocked" {
		t.Fatalf("format rows = %+v, want blocked phase", formatRows)
	}
	var buf bytes.Buffer
	if err := renderStatsTable(&buf, rows, "(no matching instances)"); err != nil {
		t.Fatalf("renderStatsTable: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "PHASE") || !strings.Contains(out, "blocked") {
		t.Fatalf("stats table missing phase column/value: %q", out)
	}
}

func TestStatsFiltersByStale(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-5 * time.Minute)},
	}}}
	probed := map[int]bool{}
	probe := func(pid int) (processStats, error) {
		probed[pid] = true
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}
	opts := statsOptions{
		Stale:           true,
		staleByInstance: map[string]bool{"manager": true},
	}

	rows, err := collectStatsRows(lister, nil, opts, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || !rows[0].Stale {
		t.Fatalf("rows = %+v, want stale manager only", rows)
	}
	if !probed[10] || probed[11] {
		t.Fatalf("probed pids = %+v, want stale manager pid only", probed)
	}
	jsonRows := statsJSONRows(rows)
	if len(jsonRows) != 1 || !jsonRows[0].Stale {
		t.Fatalf("json rows = %+v, want stale=true", jsonRows)
	}
	formatRows := statsFormatRows(rows)
	if len(formatRows) != 1 || !formatRows[0].Stale {
		t.Fatalf("format rows = %+v, want stale=true", formatRows)
	}
	summary := summarizeStatsRows(rows)
	if summary.Stale != 1 {
		t.Fatalf("summary = %+v, want one stale row", summary)
	}
	var buf bytes.Buffer
	if err := renderStatsTable(&buf, rows, "(no matching instances)"); err != nil {
		t.Fatalf("renderStatsTable: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "STALE") || !strings.Contains(out, "yes") {
		t.Fatalf("stats table missing stale column/value: %q", out)
	}
}

func TestStatsFiltersByUnhealthy(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, PID: 11, StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusStopped, PID: 12, StartedAt: now.Add(-10 * time.Minute)},
	}}}
	probed := map[int]bool{}
	probe := func(pid int) (processStats, error) {
		probed[pid] = true
		return processStats{CPUPercent: float64(pid), MemoryPercent: 0.1, RSSKiB: 1024}, nil
	}
	opts := statsOptions{
		Unhealthy:       true,
		staleByInstance: map[string]bool{"stale": true},
	}

	rows, err := collectStatsRows(lister, nil, opts, now, probe)
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 2 || rows[0].Instance != "crashed" || rows[1].Instance != "stale" {
		t.Fatalf("rows = %+v, want crashed and stale only", rows)
	}
	if probed[10] || probed[11] || probed[12] {
		t.Fatalf("probed pids = %+v, want no probes for crashed or stopped rows", probed)
	}
	if rows[0].Status != string(daemon.StatusCrashed) || !rows[1].Stale {
		t.Fatalf("rows = %+v, want crashed row and stale row", rows)
	}
	summary := summarizeStatsRows(rows)
	if summary.Total != 2 || summary.Crashed != 1 || summary.Stale != 1 {
		t.Fatalf("summary = %+v, want one crashed and one stale row", summary)
	}
}

func TestStatsFiltersByUnhealthyIncludesRuntimeStale(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Minute)},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-10 * time.Minute)},
	}}}
	opts := statsOptions{Unhealthy: true}

	rows, err := collectStatsRows(lister, nil, opts, now, func(pid int) (processStats, error) {
		if pid == os.Getpid() {
			return processStats{CPUPercent: 1, MemoryPercent: 0.1, RSSKiB: 1024}, nil
		}
		return processStats{}, os.ErrProcessDone
	})
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || !rows[0].RuntimeStale || !statsRowUnhealthy(rows[0]) || rows[0].Stale {
		t.Fatalf("rows = %+v, want one runtime-stale unhealthy row", rows)
	}
	summary := summarizeStatsRows(rows)
	if summary.Total != 1 || summary.RuntimeStale != 1 || summary.Unhealthy != 1 || summary.Stale != 0 {
		t.Fatalf("summary = %+v, want one runtime-stale unhealthy row", summary)
	}
}

func TestStatsFiltersByRuntimeStaleOnly(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, PID: 99999998, StartedAt: now.Add(-12 * time.Minute)},
		{Instance: "status-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-11 * time.Minute)},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-10 * time.Minute)},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Minute)},
	}}}
	opts := statsOptions{
		RuntimeStale:    true,
		staleByInstance: map[string]bool{"status-stale": true},
	}

	rows, err := collectStatsRows(lister, nil, opts, now, func(pid int) (processStats, error) {
		if pid == os.Getpid() {
			return processStats{CPUPercent: 1, MemoryPercent: 0.1, RSSKiB: 1024}, nil
		}
		return processStats{}, os.ErrProcessDone
	})
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Instance != "runtime-stale" || !rows[0].RuntimeStale || !statsRowUnhealthy(rows[0]) || rows[0].Stale {
		t.Fatalf("rows = %+v, want one runtime-stale row only", rows)
	}
}

func TestStatsCommandRuntimeFilterUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "codex-worker", Agent: "worker", Runtime: "codex", RuntimeBinary: "codex-dev", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "claude-manager", Agent: "manager", Runtime: "claude", RuntimeBinary: "claude-code", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "--json", "--runtime", "codex", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stats --runtime local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("decode stats json: %v\nbody=%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0].Instance != "codex-worker" || rows[0].Runtime != "codex" || rows[0].RuntimeBinary != "codex-dev" {
		t.Fatalf("rows = %+v, want codex worker only", rows)
	}
}

func TestStatsCommandPhaseFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now.Add(-3 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "idle"
description = "waiting"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `
[status]
phase = "blocked"
description = "needs input"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "--json", "--phase", "blocked", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stats --phase local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stats json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "worker" || rows[0].Phase != "blocked" {
		t.Fatalf("rows = %+v, want blocked worker only", rows)
	}
}

func TestStatsCommandStaleFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: old},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "manager"), `
[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "worker"), `
[status]
phase = "implementing"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "--json", "--stale", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stats --stale local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stats json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Instance != "manager" || !rows[0].Stale || rows[0].Phase != "implementing" {
		t.Fatalf("rows = %+v, want stale manager only", rows)
	}
}

func TestStatsCommandUnhealthyFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusStopped, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale"), `
[status]
phase = "implementing"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh"), `
[status]
phase = "idle"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "--json", "--unhealthy", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stats --unhealthy local metadata: %v\nstderr=%s", err, stderr.String())
	}
	var rows []statsJSONRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode stats json: %v\nbody=%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Instance != "crashed" || rows[1].Instance != "stale" {
		t.Fatalf("rows = %+v, want crashed and stale only", rows)
	}
	if rows[0].Status != string(daemon.StatusCrashed) || !rows[1].Stale || rows[1].Phase != "implementing" {
		t.Fatalf("rows = %+v, want crashed row and stale implementing row", rows)
	}
}

func TestStatsStatusFilterIncludesStoppedWithoutAll(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, PID: 11, StartedAt: now.Add(-10 * time.Minute)},
	}}}
	opts, err := newStatsOptions(false, []string{"stopped"}, nil)
	if err != nil {
		t.Fatalf("newStatsOptions: %v", err)
	}

	rows, err := collectStatsRows(lister, nil, opts, now, func(pid int) (processStats, error) {
		t.Fatalf("probe should not run for stopped-only filter, got pid %d", pid)
		return processStats{}, nil
	})
	if err != nil {
		t.Fatalf("collectStatsRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Instance != "worker" || rows[0].StatsAvailable {
		t.Fatalf("rows = %+v, want stopped worker without metrics", rows)
	}
}

func TestStatsOptionsRejectUnknownStatus(t *testing.T) {
	_, err := newStatsOptions(false, []string{"paused"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown --status") {
		t.Fatalf("err = %v, want unknown status", err)
	}
}

func TestStatsOptionsRejectUnknownRuntime(t *testing.T) {
	_, err := newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(false, nil, []string{"llama"}, nil, nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "unknown --runtime") {
		t.Fatalf("err = %v, want unknown runtime", err)
	}
}

func TestStatsOptionsRejectEmptyFilters(t *testing.T) {
	cases := []struct {
		name      string
		statuses  []string
		runtimes  []string
		agents    []string
		phases    []string
		instances []string
		want      string
	}{
		{name: "status", statuses: []string{"  "}, want: "non-empty status"},
		{name: "runtime", runtimes: []string{"  "}, want: "non-empty runtime"},
		{name: "agent", agents: []string{"  "}, want: "non-empty agent"},
		{name: "phase", phases: []string{"  "}, want: "non-empty phase"},
		{name: "instance", instances: []string{"  "}, want: "non-empty instance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newStatsOptionsWithRuntimeInstancesPhasesAndUnhealthy(false, tc.statuses, tc.runtimes, tc.agents, tc.phases, tc.instances, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStatsOptionsRejectUnknownPhase(t *testing.T) {
	_, err := newStatsOptionsWithInstancesAndPhases(false, nil, nil, []string{"reviewing"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown --phase") {
		t.Fatalf("err = %v, want unknown phase", err)
	}
}

func TestStatsUnknownInstance(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{}}}
	_, err := collectStatsRows(lister, []string{"missing"}, statsOptions{}, time.Now(), nil)
	var unknown *statsUnknownError
	if !errors.As(err, &unknown) || unknown.Instance != "missing" {
		t.Fatalf("err = %v, want statsUnknownError for missing", err)
	}
}

func TestStatsWatchJSONEmitsSnapshots(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: time.Now().Add(-time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1, MemoryPercent: 0.2, RSSKiB: 2048}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatsWatch(ctx, &buf, lister, nil, statsOptions{}, time.Millisecond, time.Now, probe, true); err != nil {
		t.Fatalf("runStatsWatch json: %v", err)
	}
	body := strings.TrimSpace(buf.String())
	if body == "" {
		t.Fatalf("watch stats json output empty")
	}
	first := strings.Split(body, "\n")[0]
	var rows []statsJSONRow
	if err := json.Unmarshal([]byte(first), &rows); err != nil {
		t.Fatalf("first stats snapshot is not json: %v\nbody=%s", err, body)
	}
	if len(rows) != 1 || rows[0].Instance != "manager" {
		t.Fatalf("rows = %+v, want manager snapshot", rows)
	}
}

func TestStatsWatchTextClearsWhenRequested(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: time.Now().Add(-time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1, MemoryPercent: 0.2, RSSKiB: 2048}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatsWatchWithClear(ctx, &buf, lister, nil, statsOptions{}, time.Millisecond, time.Now, probe, false, true); err != nil {
		t.Fatalf("runStatsWatchWithClear: %v", err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, watchClearSequence) {
		t.Fatalf("stats watch should start with clear sequence, got %q", body[:min(len(body), len(watchClearSequence)+20)])
	}
	if !strings.Contains(body, "INSTANCE") || !strings.Contains(body, "manager") {
		t.Fatalf("stats watch clear output missing table: %q", body)
	}
}

func TestStatsFormatWatchEmitsRowsWithoutClear(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: time.Now().Add(-time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1, MemoryPercent: 0.2, RSSKiB: 2048}, nil
	}
	tmpl, err := parseStatsFormat(`{{.Instance}}:{{.CPUPercent}}:{{.RSS}}`)
	if err != nil {
		t.Fatalf("parseStatsFormat: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatsFormatWatch(ctx, &buf, lister, nil, statsOptions{}, time.Millisecond, time.Now, probe, tmpl); err != nil {
		t.Fatalf("runStatsFormatWatch: %v", err)
	}
	first := strings.Split(strings.TrimSpace(buf.String()), "\n")[0]
	if first != "manager:1.0:2.0MiB" {
		t.Fatalf("first stats format watch row = %q, want manager metrics\nbody=%s", first, buf.String())
	}
	if strings.Contains(buf.String(), watchClearSequence) {
		t.Fatalf("stats format watch should not emit clear sequence: %q", buf.String())
	}
	if strings.Contains(buf.String(), "\n\n") {
		t.Fatalf("stats format watch should not insert blank snapshot separators: %q", buf.String())
	}
}

func TestStatsSummaryWatchJSONEmitsSnapshots(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: time.Now().Add(-time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1, MemoryPercent: 0.2, RSSKiB: 2048}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatsSummaryWatch(ctx, &buf, lister, nil, statsOptions{}, time.Millisecond, time.Now, probe, true); err != nil {
		t.Fatalf("runStatsSummaryWatch json: %v", err)
	}
	body := strings.TrimSpace(buf.String())
	if body == "" {
		t.Fatalf("watch stats summary json output empty")
	}
	first := strings.Split(body, "\n")[0]
	var summary statsSummaryJSON
	if err := json.Unmarshal([]byte(first), &summary); err != nil {
		t.Fatalf("first stats summary snapshot is not json: %v\nbody=%s", err, body)
	}
	if summary.Total != 1 || summary.Measured != 1 || summary.RSSBytes != 2048*1024 {
		t.Fatalf("summary = %+v, want one measured manager snapshot", summary)
	}
}

func TestStatsSummaryWatchTextClearsWhenRequested(t *testing.T) {
	lister := &fakeInstanceLister{snapshots: [][]*daemon.Metadata{{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, PID: 10, StartedAt: time.Now().Add(-time.Minute)},
	}}}
	probe := func(pid int) (processStats, error) {
		return processStats{CPUPercent: 1, MemoryPercent: 0.2, RSSKiB: 2048}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var buf bytes.Buffer
	if err := runStatsSummaryWatchWithClear(ctx, &buf, lister, nil, statsOptions{}, time.Millisecond, time.Now, probe, false, true); err != nil {
		t.Fatalf("runStatsSummaryWatchWithClear: %v", err)
	}
	body := buf.String()
	if !strings.HasPrefix(body, watchClearSequence) {
		t.Fatalf("stats summary watch should start with clear sequence, got %q", body[:min(len(body), len(watchClearSequence)+20)])
	}
	if !strings.Contains(body, "total") || !strings.Contains(body, "running") {
		t.Fatalf("stats summary watch clear output missing summary: %q", body)
	}
}

func TestStatsAllRejectsExplicitNames(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"stats", "--all", "manager"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(stderr.String(), "--all cannot be combined") {
		t.Fatalf("stderr = %q, want --all validation", stderr.String())
	}
}

func TestStatsFiltersRejectExplicitNames(t *testing.T) {
	for _, args := range [][]string{
		{"stats", "--instance", "manager", "manager"},
		{"stats", "--runtime", "codex", "manager"},
		{"stats", "--stale", "manager"},
	} {
		cmd := NewRootCmd()
		stderr := &bytes.Buffer{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", args)
		}
		if !strings.Contains(stderr.String(), "--status, --runtime, --agent, --phase, --instance, --stale, --runtime-stale, and --unhealthy cannot be combined") {
			t.Fatalf("%v: stderr = %q, want filter/name validation", args, stderr.String())
		}
	}
}
