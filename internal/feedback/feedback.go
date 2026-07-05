package feedback

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/daemon"
	"github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/runtimebin"
)

type Category string

const (
	CategoryFriction Category = "friction"
	CategoryBug      Category = "bug"
	CategoryIdea     Category = "idea"
	CategoryDocs     Category = "docs"
)

type Status string

const (
	StatusNew       Status = "new"
	StatusTicketed  Status = "ticketed"
	StatusDismissed Status = "dismissed"
)

const StatusAll = "all"

var feedbackIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Item is one agent-submitted feedback report stored under
// `.agent_team/feedback/items/<id>.toml`.
type Item struct {
	ID          string      `toml:"id"`
	TS          time.Time   `toml:"ts"`
	Category    Category    `toml:"category"`
	Body        string      `toml:"body"`
	Status      Status      `toml:"status"`
	Fingerprint string      `toml:"fingerprint"`
	Context     Context     `toml:"context,omitempty"`
	Resolution  *Resolution `toml:"resolution,omitempty"`
}

// Context is captured automatically by the CLI; submitters only provide body
// text and, optionally, a category.
type Context struct {
	Instance string `toml:"instance,omitempty"`
	Agent    string `toml:"agent,omitempty"`
	Job      string `toml:"job,omitempty"`
	Ticket   string `toml:"ticket,omitempty"`
	Pipeline string `toml:"pipeline,omitempty"`
	Step     string `toml:"step,omitempty"`
	Runtime  string `toml:"runtime,omitempty"`
	Build    string `toml:"build,omitempty"`
}

type Resolution struct {
	Ticket string    `toml:"ticket,omitempty"`
	Reason string    `toml:"reason,omitempty"`
	By     string    `toml:"by"`
	TS     time.Time `toml:"ts"`
}

type SubmitInput struct {
	Body     string
	Category Category
	Context  Context
	Now      time.Time
}

type ResolveInput struct {
	Ticket string
	Reason string
	By     string
	Now    time.Time
}

type Group struct {
	Fingerprint string
	Count       int
	FirstSeen   time.Time
	LastSeen    time.Time
	FirstID     string
	LastID      string
	Category    Category
	Body        string
	Statuses    map[Status]int
}

func Directory(teamDir string) string {
	return filepath.Join(teamDir, "feedback", "items")
}

func Path(teamDir, rawID string) (string, error) {
	id, err := CleanID(rawID)
	if err != nil {
		return "", err
	}
	return filepath.Join(Directory(teamDir), id+".toml"), nil
}

func CleanID(raw string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(raw))
	id = strings.TrimSuffix(id, ".toml")
	if id == "" {
		return "", errors.New("feedback id is required")
	}
	if !feedbackIDPattern.MatchString(id) {
		return "", fmt.Errorf("feedback id %q must contain only lowercase letters, digits, '.', '_', or '-'", raw)
	}
	return id, nil
}

func ParseCategory(raw string) (Category, error) {
	category := Category(strings.ToLower(strings.TrimSpace(raw)))
	if category == "" {
		category = CategoryFriction
	}
	if !ValidCategory(category) {
		return "", fmt.Errorf("unknown feedback category %q", raw)
	}
	return category, nil
}

func ValidCategory(category Category) bool {
	switch category {
	case CategoryFriction, CategoryBug, CategoryIdea, CategoryDocs:
		return true
	default:
		return false
	}
}

func ParseStatusFilter(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status == "" {
		status = string(StatusNew)
	}
	if status == StatusAll {
		return StatusAll, nil
	}
	if !ValidStatus(Status(status)) {
		return "", fmt.Errorf("unknown feedback status %q", raw)
	}
	return status, nil
}

func ValidStatus(status Status) bool {
	switch status {
	case StatusNew, StatusTicketed, StatusDismissed:
		return true
	default:
		return false
	}
}

func Fingerprint(body string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(body))), " ")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func NewItem(input SubmitInput) (*Item, error) {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	category := input.Category
	if category == "" {
		category = CategoryFriction
	}
	fp := Fingerprint(input.Body)
	item := &Item{
		ID:          newID(now, fp),
		TS:          now,
		Category:    category,
		Body:        strings.TrimSpace(input.Body),
		Status:      StatusNew,
		Fingerprint: fp,
		Context:     input.Context.clean(),
	}
	if err := Validate(item); err != nil {
		return nil, err
	}
	return item, nil
}

func Submit(teamDir string, input SubmitInput) (*Item, error) {
	item, err := NewItem(input)
	if err != nil {
		return nil, err
	}
	baseID := item.ID
	for attempt := 0; attempt < 100; attempt++ {
		if attempt > 0 {
			item.ID = fmt.Sprintf("%s-%02d", baseID, attempt)
		}
		path, err := Path(teamDir, item.ID)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err := Write(teamDir, item); err != nil {
			return nil, err
		}
		return item, nil
	}
	return nil, fmt.Errorf("feedback id collision for %s", baseID)
}

func Read(teamDir, rawID string) (*Item, error) {
	path, err := Path(teamDir, rawID)
	if err != nil {
		return nil, err
	}
	var item Item
	if _, err := toml.DecodeFile(path, &item); err != nil {
		return nil, err
	}
	if item.ID == "" {
		item.ID = strings.TrimSuffix(filepath.Base(path), ".toml")
	}
	item.TS = item.TS.UTC()
	if item.Resolution != nil {
		item.Resolution.TS = item.Resolution.TS.UTC()
	}
	if err := Validate(&item); err != nil {
		return nil, fmt.Errorf("feedback %s: %w", item.ID, err)
	}
	return &item, nil
}

func Write(teamDir string, item *Item) error {
	if err := Validate(item); err != nil {
		return err
	}
	dir := Directory(teamDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("feedback: mkdir: %w", err)
	}
	target, err := Path(teamDir, item.ID)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, item.ID+"-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("feedback: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(item); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("feedback: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("feedback: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("feedback: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("feedback: rename: %w", err)
	}
	return nil
}

func List(teamDir string) ([]*Item, error) {
	dir := Directory(teamDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]*Item, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		item, err := Read(teamDir, strings.TrimSuffix(entry.Name(), ".toml"))
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	SortItems(items)
	return items, nil
}

func SortItems(items []*Item) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if !left.TS.Equal(right.TS) {
			return left.TS.After(right.TS)
		}
		return left.ID < right.ID
	})
}

func FilterItems(items []*Item, statusFilter string) []*Item {
	statusFilter = strings.ToLower(strings.TrimSpace(statusFilter))
	if statusFilter == "" {
		statusFilter = string(StatusNew)
	}
	if statusFilter == StatusAll {
		return append([]*Item(nil), items...)
	}
	out := make([]*Item, 0, len(items))
	for _, item := range items {
		if item != nil && string(item.Status) == statusFilter {
			out = append(out, item)
		}
	}
	return out
}

func GroupItems(items []*Item) []Group {
	byFingerprint := map[string]*Group{}
	for _, item := range items {
		if item == nil {
			continue
		}
		group := byFingerprint[item.Fingerprint]
		if group == nil {
			group = &Group{
				Fingerprint: item.Fingerprint,
				FirstSeen:   item.TS,
				LastSeen:    item.TS,
				FirstID:     item.ID,
				LastID:      item.ID,
				Category:    item.Category,
				Body:        item.Body,
				Statuses:    map[Status]int{},
			}
			byFingerprint[item.Fingerprint] = group
		}
		group.Count++
		group.Statuses[item.Status]++
		if item.TS.Before(group.FirstSeen) || (item.TS.Equal(group.FirstSeen) && item.ID < group.FirstID) {
			group.FirstSeen = item.TS
			group.FirstID = item.ID
			group.Category = item.Category
			group.Body = item.Body
		}
		if item.TS.After(group.LastSeen) || (item.TS.Equal(group.LastSeen) && item.ID > group.LastID) {
			group.LastSeen = item.TS
			group.LastID = item.ID
		}
	}
	groups := make([]Group, 0, len(byFingerprint))
	for _, group := range byFingerprint {
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		left, right := groups[i], groups[j]
		if !left.LastSeen.Equal(right.LastSeen) {
			return left.LastSeen.After(right.LastSeen)
		}
		return left.Fingerprint < right.Fingerprint
	})
	return groups
}

func Resolve(teamDir, rawID string, input ResolveInput) (*Item, error) {
	item, err := Read(teamDir, rawID)
	if err != nil {
		return nil, err
	}
	ticket := strings.TrimSpace(input.Ticket)
	reason := strings.TrimSpace(input.Reason)
	if (ticket == "") == (reason == "") {
		return nil, errors.New("exactly one of ticket or reason is required")
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	by := strings.TrimSpace(input.By)
	if by == "" {
		by = defaultActor()
	}
	item.Resolution = &Resolution{
		Ticket: ticket,
		Reason: reason,
		By:     by,
		TS:     now,
	}
	if ticket != "" {
		item.Status = StatusTicketed
	} else {
		item.Status = StatusDismissed
	}
	if err := Write(teamDir, item); err != nil {
		return nil, err
	}
	return item, nil
}

func Validate(item *Item) error {
	if item == nil {
		return errors.New("feedback item is nil")
	}
	id, err := CleanID(item.ID)
	if err != nil {
		return err
	}
	if id != item.ID {
		return fmt.Errorf("feedback id %q must be normalized as %q", item.ID, id)
	}
	if item.TS.IsZero() {
		return errors.New("ts is required")
	}
	if !ValidCategory(item.Category) {
		return fmt.Errorf("unknown feedback category %q", item.Category)
	}
	if strings.TrimSpace(item.Body) == "" {
		return errors.New("body is required")
	}
	if !ValidStatus(item.Status) {
		return fmt.Errorf("unknown feedback status %q", item.Status)
	}
	if strings.TrimSpace(item.Fingerprint) == "" {
		return errors.New("fingerprint is required")
	}
	wantFingerprint := Fingerprint(item.Body)
	if item.Fingerprint != wantFingerprint {
		return fmt.Errorf("fingerprint %q does not match body fingerprint %q", item.Fingerprint, wantFingerprint)
	}
	if item.Status == StatusNew {
		if item.Resolution != nil {
			return errors.New("new feedback cannot have a resolution")
		}
		return nil
	}
	if item.Resolution == nil {
		return fmt.Errorf("%s feedback requires a resolution", item.Status)
	}
	return validateResolution(item.Status, item.Resolution)
}

func CaptureContext(teamDir string, info buildinfo.Info) Context {
	ctx := Context{
		Instance: strings.TrimSpace(os.Getenv("AGENT_TEAM_INSTANCE")),
		Job:      strings.TrimSpace(os.Getenv("AGENT_TEAM_JOB_ID")),
		Ticket:   strings.TrimSpace(os.Getenv("AGENT_TEAM_TICKET")),
		Pipeline: strings.TrimSpace(os.Getenv("AGENT_TEAM_PIPELINE")),
		Step:     strings.TrimSpace(os.Getenv("AGENT_TEAM_PIPELINE_STEP")),
		Runtime:  strings.TrimSpace(os.Getenv(runtimebin.EnvRuntime)),
		Build:    buildLabel(info),
	}
	if ctx.Instance != "" && strings.TrimSpace(teamDir) != "" {
		if meta, err := daemon.ReadMetadata(daemon.DaemonRoot(teamDir), ctx.Instance); err == nil && meta != nil {
			ctx.Agent = firstNonEmpty(ctx.Agent, meta.Agent)
			ctx.Job = firstNonEmpty(ctx.Job, meta.Job)
			ctx.Ticket = firstNonEmpty(ctx.Ticket, meta.Ticket)
			ctx.Runtime = firstNonEmpty(ctx.Runtime, meta.Runtime)
		}
	}
	if ctx.Job != "" && strings.TrimSpace(teamDir) != "" {
		if j, err := job.Read(teamDir, ctx.Job); err == nil && j != nil {
			ctx.Ticket = firstNonEmpty(ctx.Ticket, j.Ticket)
			ctx.Pipeline = firstNonEmpty(ctx.Pipeline, j.Pipeline)
			if ctx.Step == "" {
				ctx.Step = inferStep(j, ctx.Instance)
			}
			if ctx.Runtime == "" && ctx.Step != "" {
				ctx.Runtime = stepRuntime(j, ctx.Step)
			}
		}
	}
	if ctx.Runtime == "" && strings.TrimSpace(teamDir) != "" {
		if rt, err := runtimebin.CurrentFromConfig(filepath.Join(teamDir, "config.toml")); err == nil {
			ctx.Runtime = string(rt.Kind)
		}
	}
	return ctx.clean()
}

func newID(ts time.Time, fingerprint string) string {
	prefix := strings.TrimSpace(fingerprint)
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return "fb-" + ts.UTC().Format("20060102t150405.000000000z") + "-" + prefix
}

func validateResolution(status Status, resolution *Resolution) error {
	if resolution.TS.IsZero() {
		return errors.New("resolution.ts is required")
	}
	if strings.TrimSpace(resolution.By) == "" {
		return errors.New("resolution.by is required")
	}
	ticket := strings.TrimSpace(resolution.Ticket)
	reason := strings.TrimSpace(resolution.Reason)
	switch status {
	case StatusTicketed:
		if ticket == "" {
			return errors.New("ticketed feedback requires resolution.ticket")
		}
		if reason != "" {
			return errors.New("ticketed feedback cannot have resolution.reason")
		}
	case StatusDismissed:
		if reason == "" {
			return errors.New("dismissed feedback requires resolution.reason")
		}
		if ticket != "" {
			return errors.New("dismissed feedback cannot have resolution.ticket")
		}
	}
	return nil
}

func (ctx Context) clean() Context {
	return Context{
		Instance: strings.TrimSpace(ctx.Instance),
		Agent:    strings.TrimSpace(ctx.Agent),
		Job:      strings.TrimSpace(ctx.Job),
		Ticket:   strings.TrimSpace(ctx.Ticket),
		Pipeline: strings.TrimSpace(ctx.Pipeline),
		Step:     strings.TrimSpace(ctx.Step),
		Runtime:  strings.TrimSpace(ctx.Runtime),
		Build:    strings.TrimSpace(ctx.Build),
	}
}

func buildLabel(info buildinfo.Info) string {
	if rev := info.ShortRevision(); strings.TrimSpace(rev) != "" {
		return rev
	}
	if !info.Empty() {
		return info.Display()
	}
	return ""
}

func inferStep(j *job.Job, instance string) string {
	instance = strings.TrimSpace(instance)
	for _, step := range j.Steps {
		if instance != "" && strings.TrimSpace(step.Instance) == instance {
			return step.ID
		}
	}
	for _, step := range j.Steps {
		if step.Status == job.StatusRunning {
			return step.ID
		}
	}
	return ""
}

func stepRuntime(j *job.Job, id string) string {
	for _, step := range j.Steps {
		if step.ID == id {
			return strings.TrimSpace(step.Runtime)
		}
	}
	return ""
}

func defaultActor() string {
	if instance := strings.TrimSpace(os.Getenv("AGENT_TEAM_INSTANCE")); instance != "" {
		return instance
	}
	return "cli"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
