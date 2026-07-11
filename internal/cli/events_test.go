package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
)

type fakeEventsClient struct {
	body   string
	follow bool
	tail   int
}

func (f *fakeEventsClient) Events(ctx context.Context, follow bool, tailLines int) (io.ReadCloser, error) {
	f.follow = follow
	f.tail = tailLines
	return io.NopCloser(strings.NewReader(f.body)), nil
}

func mustEventFilters(t *testing.T, actions, instances, agents, statuses []string, since string, now func() time.Time) eventFilters {
	t.Helper()
	filters, err := newEventFilters(actions, instances, agents, statuses, since, now)
	if err != nil {
		t.Fatalf("newEventFilters: %v", err)
	}
	return filters
}

func TestEventsTextRendering(t *testing.T) {
	client := &fakeEventsClient{body: `{"ts":"2026-06-17T12:00:00Z","action":"dispatch","instance":"manager","agent":"manager","status":"running","pid":42,"message":"instance dispatched"}` + "\n"}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{Tail: 5}); err != nil {
		t.Fatalf("runEvents: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"2026-06-17T12:00:00Z", "dispatch", "manager", "status=running", "pid=42"} {
		if !strings.Contains(out, want) {
			t.Fatalf("events output missing %q: %s", want, out)
		}
	}
	if client.tail != 5 {
		t.Fatalf("tail = %d, want 5", client.tail)
	}
}

func TestEventsNormalizeMissingStatus(t *testing.T) {
	body := `{"ts":"2026-06-17T12:00:00Z","action":"stop","instance":"manager","agent":"manager"}` + "\n"
	filters := mustEventFilters(t, nil, nil, nil, []string{"unknown"}, "", nil)

	var text bytes.Buffer
	if err := runEvents(context.Background(), &text, &fakeEventsClient{body: body}, eventsOptions{Filters: filters}); err != nil {
		t.Fatalf("runEvents text: %v", err)
	}
	if !strings.Contains(text.String(), "status=unknown") {
		t.Fatalf("text events = %q, want status=unknown", text.String())
	}

	tmpl, err := parseEventFormat(`{{.Action}}:{{.Status}}`)
	if err != nil {
		t.Fatalf("parseEventFormat: %v", err)
	}
	var formatted bytes.Buffer
	if err := runEvents(context.Background(), &formatted, &fakeEventsClient{body: body}, eventsOptions{Format: tmpl, Filters: filters}); err != nil {
		t.Fatalf("runEvents formatted: %v", err)
	}
	if got, want := formatted.String(), "stop:unknown\n"; got != want {
		t.Fatalf("formatted events = %q, want %q", got, want)
	}

	summary, err := collectEventSummary(strings.NewReader(body), filters)
	if err != nil {
		t.Fatalf("collectEventSummary: %v", err)
	}
	if summary.Total != 1 || summary.Statuses["unknown"] != 1 {
		t.Fatalf("summary = %+v, want one unknown status", summary)
	}
}

func TestEventsJSONModeStreamsRawJSONL(t *testing.T) {
	body := `{"action":"stop","instance":"manager"}` + "\n"
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{Follow: true, JSON: true}); err != nil {
		t.Fatalf("runEvents json: %v", err)
	}
	if buf.String() != body {
		t.Fatalf("json output = %q, want raw %q", buf.String(), body)
	}
	if !client.follow {
		t.Fatalf("follow not passed to client")
	}
}

func TestEventsFiltersTextOutput(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T12:00:00Z","action":"dispatch","instance":"manager","agent":"manager","status":"running","pid":42,"message":"instance dispatched"}`,
		`{"ts":"2026-06-17T12:00:01Z","action":"stop","instance":"worker","agent":"worker","status":"stopped","pid":43,"message":"instance stopped"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Filters: mustEventFilters(t, []string{"dispatch"}, []string{"manager"}, []string{"manager"}, []string{"running"}, "", nil),
	}); err != nil {
		t.Fatalf("runEvents filtered: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "dispatch") || !strings.Contains(out, "manager") {
		t.Fatalf("filtered events missing manager dispatch: %s", out)
	}
	if strings.Contains(out, "worker") || strings.Contains(out, "stop") {
		t.Fatalf("filtered events should omit worker stop: %s", out)
	}
}

func TestEventsFiltersByAgent(t *testing.T) {
	body := strings.Join([]string{
		`{"action":"dispatch","instance":"adhoc","agent":"manager","status":"running"}`,
		`{"action":"dispatch","instance":"ticket-manager","agent":"ticket-manager","status":"running"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		JSON:    true,
		Filters: mustEventFilters(t, nil, nil, []string{"manager"}, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents agent filtered json: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if !strings.Contains(got, `"instance":"adhoc"`) || strings.Contains(got, "ticket-manager") {
		t.Fatalf("agent-filtered json = %q", got)
	}
}

func TestEventsFormatExposesOwnershipMetadata(t *testing.T) {
	body := `{"action":"dispatch","instance":"worker-squ-42","agent":"worker","job":"squ-42","ticket":"SQU-42","branch":"worker-squ-42","pr":"https://github.test/acme/repo/pull/42","status":"running","message":"owned"}` + "\n"
	tmpl, err := parseEventFormat(`{{.Job}} {{.Ticket}} {{.Branch}} {{.PR}}`)
	if err != nil {
		t.Fatalf("parseEventFormat: %v", err)
	}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, &fakeEventsClient{body: body}, eventsOptions{Format: tmpl}); err != nil {
		t.Fatalf("runEvents formatted: %v", err)
	}
	want := "squ-42 SQU-42 worker-squ-42 https://github.test/acme/repo/pull/42\n"
	if got := buf.String(); got != want {
		t.Fatalf("formatted event = %q, want %q", got, want)
	}
}

func TestEventsLatestInstanceFilterUsesNewestMetadata(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	filters := mustEventFilters(t, nil, nil, []string{"manager"}, []string{"running"}, "", nil)
	selected := latestEventMetadataLimit([]*daemon.Metadata{
		{Instance: "worker-new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "manager-old", Agent: "manager", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "manager-new", Agent: "manager", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
		{Instance: "manager-stopped", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Minute)},
	}, filters, 1)
	if len(selected) != 1 || selected[0].Instance != "manager-new" {
		t.Fatalf("selected = %+v, want newest running manager", selected)
	}
}

func TestEventsLastInstanceFilterUsesNewestMetadata(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	selected := latestEventMetadataLimit([]*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "missing", Agent: "worker", Status: daemon.StatusRunning},
		{Instance: "new", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
		{Instance: "mid", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-30 * time.Minute)},
	}, eventFilters{}, 2)
	if len(selected) != 2 || selected[0].Instance != "new" || selected[1].Instance != "mid" {
		t.Fatalf("selected = %+v, want newest two new,mid", selected)
	}
}

func TestEventsFiltersJSONOutput(t *testing.T) {
	body := strings.Join([]string{
		`{"action":"dispatch","instance":"manager","status":"running"}`,
		`{"action":"stop","instance":"worker","status":"stopped"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		JSON:    true,
		Filters: mustEventFilters(t, []string{"dispatch,stop"}, []string{"manager"}, nil, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents filtered json: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got != `{"action":"dispatch","instance":"manager","status":"running"}` {
		t.Fatalf("filtered json = %q", got)
	}
}

func TestEventsTailAppliesAfterFiltersWhenNotFollowing(t *testing.T) {
	body := strings.Join([]string{
		`{"action":"dispatch","instance":"manager-old","agent":"manager","status":"running"}`,
		`{"action":"dispatch","instance":"worker-new","agent":"worker","status":"running"}`,
		`{"action":"stop","instance":"manager-new","agent":"manager","status":"stopped"}`,
		`{"action":"stop","instance":"worker-newer","agent":"worker","status":"stopped"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	tmpl, err := parseEventFormat(`{{.Instance}}:{{.Status}}`)
	if err != nil {
		t.Fatalf("parseEventFormat: %v", err)
	}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Tail:    1,
		Format:  tmpl,
		Filters: mustEventFilters(t, nil, nil, []string{"manager"}, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents filtered tail: %v", err)
	}
	if client.tail != 0 {
		t.Fatalf("client tail = %d, want full history so CLI can tail after filters", client.tail)
	}
	if got, want := buf.String(), "manager-new:stopped\n"; got != want {
		t.Fatalf("filtered tail output = %q, want %q", got, want)
	}
}

func TestEventsSortNewestSnapshots(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T12:00:00Z","action":"start","instance":"manager","agent":"manager","status":"running"}`,
		`{"ts":"2026-06-17T12:02:00Z","action":"stop","instance":"worker","agent":"worker","status":"stopped"}`,
		`{"ts":"2026-06-17T12:01:00Z","action":"dispatch","instance":"reviewer","agent":"manager","status":"running"}`,
		"",
	}, "\n")
	tmpl, err := parseEventFormat(`{{.Instance}} {{.Action}}`)
	if err != nil {
		t.Fatalf("parseEventFormat: %v", err)
	}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, &fakeEventsClient{body: body}, eventsOptions{
		Sort:   "newest",
		Format: tmpl,
	}); err != nil {
		t.Fatalf("runEvents newest: %v", err)
	}
	if got, want := buf.String(), "worker stop\nreviewer dispatch\nmanager start\n"; got != want {
		t.Fatalf("newest event output = %q, want %q", got, want)
	}
}

func TestEventsSortNewestTailAfterFilters(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T12:00:00Z","action":"start","instance":"manager-old","agent":"manager","status":"running"}`,
		`{"ts":"2026-06-17T12:01:00Z","action":"stop","instance":"worker","agent":"worker","status":"stopped"}`,
		`{"ts":"2026-06-17T12:02:00Z","action":"dispatch","instance":"manager-mid","agent":"manager","status":"running"}`,
		`{"ts":"2026-06-17T12:03:00Z","action":"stop","instance":"manager-new","agent":"manager","status":"stopped"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	tmpl, err := parseEventFormat(`{{.Instance}} {{.Action}}`)
	if err != nil {
		t.Fatalf("parseEventFormat: %v", err)
	}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Tail:    2,
		Sort:    "newest",
		Format:  tmpl,
		Filters: mustEventFilters(t, nil, nil, []string{"manager"}, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents filtered newest tail: %v", err)
	}
	if client.tail != 0 {
		t.Fatalf("client tail = %d, want full history so CLI can tail after filters", client.tail)
	}
	if got, want := buf.String(), "manager-new stop\nmanager-mid dispatch\n"; got != want {
		t.Fatalf("filtered newest tail output = %q, want %q", got, want)
	}
}

func TestEventsTailAfterFiltersPreservesRawJSON(t *testing.T) {
	body := strings.Join([]string{
		`{"action":"dispatch","instance":"manager-old","agent":"manager"}`,
		`{"action":"stop","instance":"worker","agent":"worker"}`,
		`{"action":"stop","instance":"manager-new","agent":"manager"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Tail:    1,
		JSON:    true,
		Filters: mustEventFilters(t, nil, nil, []string{"manager"}, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents filtered tail json: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	want := `{"action":"stop","instance":"manager-new","agent":"manager"}`
	if got != want {
		t.Fatalf("filtered tail json = %q, want raw line %q", got, want)
	}
}

func TestEventsTailAfterFiltersSummary(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T12:00:00Z","action":"dispatch","instance":"manager-old","agent":"manager","status":"running"}`,
		`{"ts":"2026-06-17T12:01:00Z","action":"stop","instance":"worker","agent":"worker","status":"stopped"}`,
		`{"ts":"2026-06-17T12:02:00Z","action":"stop","instance":"manager-new","agent":"manager","status":"stopped"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Tail:    1,
		Summary: true,
		Filters: mustEventFilters(t, nil, nil, []string{"manager"}, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents filtered tail summary: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"events: total=1",
		"first=2026-06-17T12:02:00Z",
		"last=2026-06-17T12:02:00Z",
		"actions: stop=1",
		"statuses: stopped=1",
		"instances: manager-new=1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary output missing %q: %s", want, out)
		}
	}
}

func TestEventsTailWithFollowKeepsRawDaemonTail(t *testing.T) {
	client := &fakeEventsClient{body: `{"action":"dispatch","instance":"manager","agent":"manager"}` + "\n"}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Follow:  true,
		Tail:    5,
		Filters: mustEventFilters(t, nil, nil, []string{"manager"}, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents follow filtered tail: %v", err)
	}
	if client.tail != 5 {
		t.Fatalf("client tail = %d, want raw daemon tail for follow", client.tail)
	}
}

func TestEventsFormatOutput(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T12:00:00Z","action":"dispatch","instance":"manager","agent":"manager","status":"running","pid":42}`,
		`{"ts":"2026-06-17T12:00:01Z","action":"stop","instance":"worker","agent":"worker","status":"stopped","pid":43}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	tmpl, err := parseEventFormat(`{{.TS}} {{.Action}} {{.Instance}} {{.Status}} {{.PID}}`)
	if err != nil {
		t.Fatalf("parseEventFormat: %v", err)
	}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Format:  tmpl,
		Filters: mustEventFilters(t, []string{"dispatch"}, nil, []string{"manager"}, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents formatted: %v", err)
	}
	want := "2026-06-17T12:00:00Z dispatch manager running 42\n"
	if got := buf.String(); got != want {
		t.Fatalf("formatted events = %q, want %q", got, want)
	}
}

func TestEventsFormatRejectsInvalidTemplate(t *testing.T) {
	_, err := parseEventFormat(`{{.Action`)
	if err == nil || !strings.Contains(err.Error(), "invalid --format template") {
		t.Fatalf("err = %v, want invalid template", err)
	}
}

func TestEventsFormatRejectsConflictingOutputModes(t *testing.T) {
	for _, args := range [][]string{
		{"events", "--format", "{{.Action}}", "--json"},
		{"events", "--format", "{{.Action}}", "--summary"},
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

func TestEventsRejectNewestSortWithFollow(t *testing.T) {
	cmd := NewRootCmd()
	stderr := &bytes.Buffer{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"events", "--follow", "--sort", "newest"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("events accepted newest sort with follow")
	}
	if !strings.Contains(stderr.String(), "--sort newest cannot be combined with --follow") {
		t.Fatalf("stderr = %q, want newest follow validation", stderr.String())
	}
}

func TestEventsFiltersNoTextMatches(t *testing.T) {
	client := &fakeEventsClient{body: `{"action":"stop","instance":"worker","status":"stopped"}` + "\n"}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Filters: mustEventFilters(t, nil, []string{"manager"}, nil, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents filtered no matches: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "(no events)" {
		t.Fatalf("output = %q, want no events", buf.String())
	}
}

func TestEventsSinceDurationFilter(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T11:00:00Z","action":"dispatch","instance":"old","status":"running"}`,
		`{"ts":"2026-06-17T11:59:00Z","action":"dispatch","instance":"new","status":"running"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	now := func() time.Time { return time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC) }
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		JSON:    true,
		Filters: mustEventFilters(t, nil, nil, nil, nil, "10m", now),
	}); err != nil {
		t.Fatalf("runEvents since duration: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if !strings.Contains(got, `"instance":"new"`) || strings.Contains(got, `"instance":"old"`) {
		t.Fatalf("since-filtered json = %q", got)
	}
}

func TestEventsSinceTimestampFilter(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T11:00:00Z","action":"dispatch","instance":"old","status":"running"}`,
		`{"ts":"2026-06-17T12:00:00Z","action":"dispatch","instance":"new","status":"running"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Filters: mustEventFilters(t, nil, nil, nil, nil, "2026-06-17T12:00:00Z", nil),
	}); err != nil {
		t.Fatalf("runEvents since timestamp: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "new") || strings.Contains(out, "old") {
		t.Fatalf("since-filtered text = %q", out)
	}
}

func TestEventsSinceRejectsInvalidValue(t *testing.T) {
	_, err := newEventFilters(nil, nil, nil, nil, "recently", time.Now)
	if err == nil || !strings.Contains(err.Error(), "--since") {
		t.Fatalf("err = %v, want --since validation", err)
	}
	_, err = newEventFilters(nil, nil, nil, nil, "-1m", time.Now)
	if err == nil || !strings.Contains(err.Error(), "duration must be >= 0") {
		t.Fatalf("err = %v, want negative duration validation", err)
	}
}

func TestEventsRejectEmptyFilters(t *testing.T) {
	cases := []struct {
		name      string
		actions   []string
		instances []string
		agents    []string
		statuses  []string
		want      string
	}{
		{name: "action", actions: []string{"  "}, want: "non-empty action"},
		{name: "instance", instances: []string{"  "}, want: "non-empty instance"},
		{name: "agent", agents: []string{"  "}, want: "non-empty agent"},
		{name: "status", statuses: []string{","}, want: "non-empty status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newEventFilters(tc.actions, tc.instances, tc.agents, tc.statuses, "", time.Now)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEventsRejectUnknownStatusFilter(t *testing.T) {
	_, err := newEventFilters(nil, nil, nil, []string{"paused"}, "", time.Now)
	if err == nil || !strings.Contains(err.Error(), "unknown --status") {
		t.Fatalf("err = %v, want unknown status validation", err)
	}
}

func TestEventsNoEventsMessage(t *testing.T) {
	client := &fakeEventsClient{}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{}); err != nil {
		t.Fatalf("runEvents empty: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "(no events)" {
		t.Fatalf("output = %q, want no events", buf.String())
	}
}

func TestEventsSummaryTextAggregatesFilteredEvents(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T12:00:00Z","action":"dispatch","instance":"manager","agent":"manager","status":"running"}`,
		`{"ts":"2026-06-17T12:01:00Z","action":"stop","instance":"worker","agent":"worker","job":"squ-42","ticket":"SQU-42","branch":"worker-squ-42","pr":"https://github.test/acme/repo/pull/42","status":"stopped"}`,
		`{"ts":"2026-06-17T12:02:00Z","action":"crash","instance":"worker","agent":"worker","job":"squ-42","ticket":"SQU-42","branch":"worker-squ-42","pr":"https://github.test/acme/repo/pull/42","status":"crashed"}`,
		`not json`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{
		Summary: true,
		Filters: mustEventFilters(t, nil, nil, []string{"worker"}, nil, "", nil),
	}); err != nil {
		t.Fatalf("runEvents summary: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"events: total=2",
		"first=2026-06-17T12:01:00Z",
		"last=2026-06-17T12:02:00Z",
		"actions: crash=1 stop=1",
		"statuses: crashed=1 stopped=1",
		"agents: worker=2",
		"instances: worker=2",
		"jobs: squ-42=2",
		"tickets: SQU-42=2",
		"branches: worker-squ-42=2",
		"prs: https://github.test/acme/repo/pull/42=2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary output missing %q: %s", want, out)
		}
	}
}

func TestEventsSummaryJSONAggregatesEvents(t *testing.T) {
	body := strings.Join([]string{
		`{"ts":"2026-06-17T12:00:00Z","action":"dispatch","instance":"manager","agent":"manager","job":"squ-42","ticket":"SQU-42","branch":"manager-squ-42","status":"running"}`,
		`{"ts":"2026-06-17T12:01:00Z","action":"dispatch","instance":"worker","agent":"worker","job":"squ-42","ticket":"SQU-42","branch":"worker-squ-42","pr":"https://github.test/acme/repo/pull/42","status":"running"}`,
		"",
	}, "\n")
	client := &fakeEventsClient{body: body}
	var buf bytes.Buffer
	if err := runEvents(context.Background(), &buf, client, eventsOptions{Tail: 3, JSON: true, Summary: true}); err != nil {
		t.Fatalf("runEvents summary json: %v", err)
	}
	var summary eventSummaryJSON
	if err := json.Unmarshal(buf.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v\nbody=%s", err, buf.String())
	}
	if summary.Total != 2 || summary.Actions["dispatch"] != 2 || summary.Statuses["running"] != 2 {
		t.Fatalf("summary = %+v, want dispatch/running totals", summary)
	}
	if summary.Agents["manager"] != 1 || summary.Agents["worker"] != 1 {
		t.Fatalf("summary agents = %+v", summary.Agents)
	}
	if summary.Jobs["squ-42"] != 2 || summary.Tickets["SQU-42"] != 2 || summary.Branches["manager-squ-42"] != 1 || summary.Branches["worker-squ-42"] != 1 || summary.PRs["https://github.test/acme/repo/pull/42"] != 1 {
		t.Fatalf("summary ownership = jobs %+v tickets %+v branches %+v prs %+v", summary.Jobs, summary.Tickets, summary.Branches, summary.PRs)
	}
	if summary.FirstTS != "2026-06-17T12:00:00Z" || summary.LastTS != "2026-06-17T12:01:00Z" {
		t.Fatalf("summary timestamps = first %q last %q", summary.FirstTS, summary.LastTS)
	}
	if client.tail != 3 {
		t.Fatalf("tail = %d, want 3", client.tail)
	}
}

func TestEventsCommandFallsBackToLocalEventLogWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
		TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
		Action:   "dispatch",
		Instance: "manager",
		Agent:    "manager",
		Status:   daemon.StatusRunning,
		PID:      42,
		Message:  "instance dispatched",
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--tail", "1",
		"--format", "{{.Action}}:{{.Instance}}:{{.Status}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "dispatch:manager:running\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestEventsCommandFiltersByJobAndStepWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	base := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, j := range []*job.Job{
		{
			ID:        "squ-701",
			Ticket:    "SQU-701",
			Target:    "worker",
			Kickoff:   "global event filters",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-701",
			CreatedAt: base,
			UpdatedAt: base,
			Steps: []job.Step{
				{ID: "implement", Target: "worker", Status: job.StatusRunning, Instance: "worker-squ-701"},
				{ID: "review", Target: "manager", Status: job.StatusRunning, Instance: "manager-squ-701", After: []string{"implement"}},
			},
		},
		{
			ID:        "squ-702",
			Ticket:    "SQU-702",
			Target:    "worker",
			Kickoff:   "foreign",
			Status:    job.StatusRunning,
			Instance:  "worker-squ-702",
			CreatedAt: base,
			UpdatedAt: base,
		},
	} {
		if err := job.Write(teamDir, j); err != nil {
			t.Fatalf("write job %s: %v", j.ID, err)
		}
	}
	for _, ev := range []*daemon.LifecycleEvent{
		{TS: base, Action: "dispatch", Instance: "manager-squ-701", Agent: "manager", Status: daemon.StatusRunning, Message: "review"},
		{TS: base.Add(time.Minute), Action: "dispatch", Instance: "worker-squ-701", Agent: "worker", Status: daemon.StatusRunning, Message: "implement"},
		{TS: base.Add(2 * time.Minute), Action: "dispatch", Instance: "worker-squ-702", Agent: "worker", Status: daemon.StatusRunning, Message: "foreign"},
		{TS: base.Add(3 * time.Minute), Action: "start", Instance: "manager", Agent: "manager", Status: daemon.StatusRunning, Message: "persistent"},
	} {
		if err := daemon.AppendLifecycleEvent(root, ev); err != nil {
			t.Fatalf("append event %s: %v", ev.Instance, err)
		}
	}

	byJob := NewRootCmd()
	var jobOut, jobErr bytes.Buffer
	byJob.SetOut(&jobOut)
	byJob.SetErr(&jobErr)
	byJob.SetArgs([]string{
		"events",
		"--job", "https://linear.app/squirtlesquad/issue/SQU-701/global-events",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := byJob.Execute(); err != nil {
		t.Fatalf("events --job fallback: %v\nstderr=%s", err, jobErr.String())
	}
	if got, want := jobOut.String(), "manager-squ-701\nworker-squ-701\n"; got != want {
		t.Fatalf("job-filtered stdout = %q, want %q", got, want)
	}

	byStep := NewRootCmd()
	var stepOut, stepErr bytes.Buffer
	byStep.SetOut(&stepOut)
	byStep.SetErr(&stepErr)
	byStep.SetArgs([]string{
		"events",
		"--step", "implement",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := byStep.Execute(); err != nil {
		t.Fatalf("events --step fallback: %v\nstderr=%s", err, stepErr.String())
	}
	if got, want := stepOut.String(), "worker-squ-701\n"; got != want {
		t.Fatalf("step-filtered stdout = %q, want %q", got, want)
	}
}

func TestEventsLatestUsesLocalNewestMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now.Add(time.Duration(len(meta.Instance)) * time.Second),
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--latest",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --latest fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "new\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsRuntimeFilterUsesLocalMetadataWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	for _, meta := range []*daemon.Metadata{
		{Instance: "worker-old", Agent: "worker", Runtime: "codex", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "manager-new", Agent: "manager", Runtime: "claude", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Minute)},
		{Instance: "worker-new", Agent: "worker", Runtime: "codex", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now.Add(time.Duration(len(meta.Instance)) * time.Second),
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--runtime", "codex",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --runtime fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "worker-old\nworker-new\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	latest := NewRootCmd()
	var latestOut, latestErr bytes.Buffer
	latest.SetOut(&latestOut)
	latest.SetErr(&latestErr)
	latest.SetArgs([]string{
		"events",
		"--runtime", "codex",
		"--latest",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := latest.Execute(); err != nil {
		t.Fatalf("events --runtime --latest fallback: %v\nstderr=%s", err, latestErr.String())
	}
	if got, want := latestOut.String(), "worker-new\n"; got != want {
		t.Fatalf("latest stdout = %q, want %q", got, want)
	}

	bad := NewRootCmd()
	bad.SetOut(&bytes.Buffer{})
	var badErr bytes.Buffer
	bad.SetErr(&badErr)
	bad.SetArgs([]string{"events", "--runtime", "llama", "--repo", tmp})
	if err := bad.Execute(); err == nil {
		t.Fatal("events accepted unknown runtime")
	}
	if !strings.Contains(badErr.String(), "unknown --runtime") {
		t.Fatalf("bad runtime stderr = %q", badErr.String())
	}
}

func TestEventsPhaseFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-1 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
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
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--phase", "blocked",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --phase fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "worker\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsStaleFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "manager", Agent: "manager", Status: daemon.StatusStopped, StartedAt: old},
		{Instance: "worker", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
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
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--stale",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --stale fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "manager\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsUnhealthyFilterUsesLocalStatusWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: old},
		{Instance: "fresh", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now},
		{Instance: "stale", Agent: "manager", Status: daemon.StatusStopped, StartedAt: old},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
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
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--unhealthy",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --unhealthy fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "crashed\nstale\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsUnhealthyFilterIncludesRuntimeStaleWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, StartedAt: now},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--unhealthy",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --unhealthy runtime-stale fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "runtime-stale\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsRuntimeStaleFilterIncludesOnlyCurrentRuntimeStaleWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed", Agent: "worker", Runtime: "codex", Status: daemon.StatusCrashed, PID: 99999998, StartedAt: now.Add(-2 * time.Minute)},
		{Instance: "runtime-stale", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-time.Minute)},
		{Instance: "fresh", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: os.Getpid(), StartedAt: now},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--runtime-stale",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --runtime-stale fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "runtime-stale\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsLatestHonorsPhaseFilterWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "blocked-old", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "blocked-new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
		{Instance: "idle-newer", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "blocked-old"), `
[status]
phase = "blocked"
description = "old"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "blocked-new"), `
[status]
phase = "blocked"
description = "new"
`, now)
	writeStatus(t, filepath.Join(teamDir, "state", "idle-newer"), `
[status]
phase = "idle"
description = "newer"
`, now)

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--latest",
		"--phase", "blocked",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --latest --phase fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "blocked-new\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsLatestHonorsUnhealthyFilterWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	old := now.Add(-staleAfter - time.Minute)
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed-old", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "stale-new", Agent: "worker", Status: daemon.StatusStopped, StartedAt: now.Add(-30 * time.Minute)},
		{Instance: "fresh-newer", Agent: "worker", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}
	writeStatus(t, filepath.Join(teamDir, "state", "stale-new"), `
[status]
phase = "blocked"
description = "stuck"
`, old)
	writeStatus(t, filepath.Join(teamDir, "state", "fresh-newer"), `
[status]
phase = "idle"
description = "fresh"
`, now)

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--latest",
		"--unhealthy",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --latest --unhealthy fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "stale-new\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsLatestHonorsRuntimeStaleUnhealthyFilterWhenDaemonStopped(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	teamDir := filepath.Join(tmp, ".agent_team")
	root := daemon.DaemonRoot(teamDir)
	now := time.Now()
	for _, meta := range []*daemon.Metadata{
		{Instance: "crashed-old", Agent: "worker", Status: daemon.StatusCrashed, StartedAt: now.Add(-2 * time.Hour)},
		{Instance: "runtime-stale-new", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, PID: 99999999, StartedAt: now.Add(-30 * time.Minute)},
		{Instance: "fresh-newer", Agent: "worker", Runtime: "codex", Status: daemon.StatusRunning, StartedAt: now.Add(-5 * time.Minute)},
	} {
		if err := daemon.WriteMetadata(root, meta); err != nil {
			t.Fatalf("write metadata %s: %v", meta.Instance, err)
		}
		if err := daemon.AppendLifecycleEvent(root, &daemon.LifecycleEvent{
			TS:       now,
			Action:   "dispatch",
			Instance: meta.Instance,
			Agent:    meta.Agent,
			Status:   meta.Status,
			Message:  meta.Instance,
		}); err != nil {
			t.Fatalf("append event %s: %v", meta.Instance, err)
		}
	}

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"events",
		"--latest",
		"--unhealthy",
		"--format", "{{.Instance}}",
		"--repo", tmp,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events --latest --unhealthy runtime-stale fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got, want := stdout.String(), "runtime-stale-new\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventsCommandLocalFallbackHandlesMissingEventLog(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"events", "--repo", tmp})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("events empty fallback: %v\nstderr=%s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "(no events)" {
		t.Fatalf("stdout = %q, want no events", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestEventsSummaryRejectsFollow(t *testing.T) {
	tmp := t.TempDir()
	cmd := NewRootCmd()
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"events", "--summary", "--follow", "--repo", tmp})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --summary/--follow validation error")
	}
	if !strings.Contains(stderr.String(), "--summary cannot be combined with --follow") {
		t.Fatalf("stderr = %q, want summary/follow validation", stderr.String())
	}
}

func TestEventsRejectsInvalidPhaseFilter(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"events", "--phase", "reviewing"}, want: "unknown --phase"},
		{args: []string{"events", "--phase", ","}, want: "non-empty phase"},
	} {
		cmd := NewRootCmd()
		var stderr bytes.Buffer
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&stderr)
		cmd.SetArgs(append(tc.args, "--repo", tmp))
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestEventsLatestLastValidation(t *testing.T) {
	tmp := t.TempDir()
	initInto(t, tmp)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"events", "--last", "-1"}, "--last must be >= 0"},
		{[]string{"events", "--latest", "--last", "2"}, "choose one of --latest or --last"},
	}
	for _, tc := range cases {
		cmd := NewRootCmd()
		var stderr bytes.Buffer
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&stderr)
		cmd.SetArgs(append(tc.args, "--repo", tmp))
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%v: expected validation error", tc.args)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Fatalf("%v: stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestRenderEventLineZeroValues(t *testing.T) {
	var buf bytes.Buffer
	renderEventLine(&buf, daemon.LifecycleEvent{
		TS:       time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC),
		Action:   "remove",
		Instance: "mgr",
	})
	out := buf.String()
	if !strings.Contains(out, "remove") || !strings.Contains(out, "mgr") {
		t.Fatalf("line = %q", out)
	}
}
