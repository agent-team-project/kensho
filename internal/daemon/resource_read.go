package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/budget"
	jobstore "github.com/agent-team-project/agent-team/internal/job"
	"github.com/agent-team-project/agent-team/internal/outcomes"
	"github.com/agent-team-project/agent-team/internal/resource"
)

var (
	ErrResourceDeploymentUnavailable = errors.New("resource deployment identity unavailable")
	ErrResourceWrongDeployment       = errors.New("resource belongs to another deployment")
	ErrResourceNotFound              = errors.New("resource not found")
	ErrResourceUnsupported           = errors.New("resource kind unsupported")
)

// ResourceRead is the daemon's URI-addressed read envelope. Data is a
// resource-specific JSON value read through the daemon-owned stores.
type ResourceRead struct {
	URI      string `json:"uri"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Fragment string `json:"fragment,omitempty"`
	Data     any    `json:"data"`
}

type projectResource struct {
	ID           string    `json:"id"`
	URI          string    `json:"uri"`
	ParentURI    string    `json:"parent_uri,omitempty"`
	Ready        bool      `json:"ready"`
	PID          int       `json:"pid,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	PathScope    string    `json:"path_scope,omitempty"`
	TeamDir      string    `json:"team_dir,omitempty"`
	DaemonRoot   string    `json:"daemon_root,omitempty"`
	TopologyURI  string    `json:"topology_uri,omitempty"`
	Relationship string    `json:"relationship,omitempty"`
	State        string    `json:"state,omitempty"`
	CharterURI   string    `json:"charter_uri,omitempty"`
}

type workspaceResource struct {
	ID        string   `json:"id"`
	URI       string   `json:"uri"`
	Kind      string   `json:"kind,omitempty"`
	Path      string   `json:"path,omitempty"`
	PathScope string   `json:"path_scope,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Job       string   `json:"job,omitempty"`
	Instance  string   `json:"instance,omitempty"`
	Status    Status   `json:"status,omitempty"`
	Owners    []string `json:"owners,omitempty"`
}

type stateResource struct {
	Instance  string         `json:"instance"`
	URI       string         `json:"uri"`
	Path      string         `json:"path,omitempty"`
	PathScope string         `json:"path_scope,omitempty"`
	Exists    bool           `json:"exists"`
	Status    map[string]any `json:"status,omitempty"`
}

type logResource struct {
	Instance  string    `json:"instance"`
	URI       string    `json:"uri"`
	Path      string    `json:"path,omitempty"`
	PathScope string    `json:"path_scope,omitempty"`
	Exists    bool      `json:"exists"`
	Size      int64     `json:"size,omitempty"`
	ModTime   time.Time `json:"mod_time,omitempty"`
}

type mailboxResource struct {
	Instance    string     `json:"instance"`
	URI         string     `json:"uri"`
	Cursor      string     `json:"cursor,omitempty"`
	UnreadCount int        `json:"unread_count"`
	Messages    []*Message `json:"messages"`
}

type channelResource struct {
	Name          string                        `json:"name"`
	URI           string                        `json:"uri"`
	Info          *ChannelInfo                  `json:"info,omitempty"`
	Subscriptions []channelSubscriptionResource `json:"subscriptions,omitempty"`
}

type channelSubscriptionResource struct {
	Instance     string    `json:"instance"`
	Cursor       int64     `json:"cursor"`
	SubscribedAt time.Time `json:"subscribed_at"`
}

type lockResource struct {
	ID        string         `json:"id"`
	URI       string         `json:"uri"`
	Snapshots []LockSnapshot `json:"snapshots,omitempty"`
	Leases    []*LockLease   `json:"leases,omitempty"`
}

type capabilityResource struct {
	ID                 string               `json:"id"`
	URI                string               `json:"uri"`
	CharterURI         string               `json:"charter_uri"`
	ChildDeploymentURI string               `json:"child_deployment_uri"`
	Authority          TeamCharterAuthority `json:"authority"`
}

// ResolveResourceRead resolves a canonical agt:// URI against the daemon's
// current stores. It is intentionally read-only: callers get structured data
// without receiving storage paths they must dereference themselves.
func ResolveResourceRead(teamDir string, m *InstanceManager, channels *ChannelStore, events *EventResolver, rawURI string) (*ResourceRead, error) {
	if m == nil {
		return nil, errors.New("resource read: instance manager is required")
	}
	parsed, err := resource.Parse(rawURI)
	if err != nil {
		return nil, err
	}
	deployment, err := resource.DeploymentFromTeamDir(teamDir)
	if err != nil {
		return nil, err
	}
	deploymentID := strings.TrimSpace(deployment.ID)
	if deploymentID == "" {
		return nil, ErrResourceDeploymentUnavailable
	}
	canonical := resource.URIWithFragment(parsed.DeploymentID, parsed.Kind, parsed.ID, parsed.Fragment)
	if parsed.DeploymentID != deploymentID {
		if data, ok, err := resolveChildDeploymentRead(m.daemonRoot, canonical, parsed); ok || err != nil {
			if err != nil {
				return nil, err
			}
			return &ResourceRead{
				URI:      canonical,
				Kind:     parsed.Kind,
				ID:       parsed.ID,
				Fragment: parsed.Fragment,
				Data:     data,
			}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrResourceWrongDeployment, parsed.DeploymentID)
	}
	out := &ResourceRead{
		URI:      canonical,
		Kind:     parsed.Kind,
		ID:       parsed.ID,
		Fragment: parsed.Fragment,
	}
	switch parsed.Kind {
	case resource.KindProject:
		data, err := resolveProjectResource(teamDir, m.daemonRoot, deployment, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindInstance:
		data, err := resolveInstanceResource(m, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindJob:
		data, err := resolveJobResource(teamDir, parsed.ID, parsed.Fragment)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindOutcome:
		data, err := resolveOutcomeResource(teamDir, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindWorkspace:
		data, err := resolveWorkspaceResource(teamDir, m, canonical, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindState:
		data, err := resolveStateResource(teamDir, m, canonical, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindLog:
		data, err := resolveLogResource(m, canonical, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindUsage:
		data, err := resolveUsageResource(teamDir, m, canonical, parsed.ID, parsed.Fragment)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindMailbox:
		data, err := resolveMailboxResource(m, events, canonical, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindChannel:
		if channels == nil {
			channels = NewChannelStore(m.daemonRoot)
		}
		data, err := resolveChannelResource(channels, canonical, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindQueue:
		data, err := ReadQueueItem(m.daemonRoot, parsed.ID)
		if err != nil {
			return nil, resourceReadNotFound(err)
		}
		out.Data = data
	case resource.KindOutbox:
		data, err := ReadOutboxItem(teamDir, parsed.ID)
		if err != nil {
			return nil, resourceReadNotFound(err)
		}
		out.Data = data
	case resource.KindLock:
		data, err := resolveLockResource(m.daemonRoot, events, canonical, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	case resource.KindTopology:
		if parsed.ID != "current" {
			return nil, ErrResourceNotFound
		}
		if events == nil {
			out.Data = marshalTopology(nil, events)
			break
		}
		topo := events.Topology()
		out.Data = marshalTopology(topo, events)
	case resource.KindCharter:
		data, err := ReadTeamCharter(m.daemonRoot, parsed.ID)
		if err != nil {
			return nil, resourceReadNotFound(err)
		}
		out.Data = data
	case resource.KindAllocation:
		data, err := resolveAllocationResource(teamDir, parsed.ID)
		if err != nil {
			return nil, err
		}
		out.Data = data
	default:
		return nil, fmt.Errorf("%w: %s", ErrResourceUnsupported, parsed.Kind)
	}
	return out, nil
}

func resolveProjectResource(teamDir, daemonRoot string, deployment resource.Deployment, id string) (*projectResource, error) {
	if id != deployment.ID {
		return nil, ErrResourceNotFound
	}
	pathScope := ""
	if teamDir != "" || daemonRoot != "" {
		pathScope = "host-local"
	}
	return &projectResource{
		ID:          deployment.ID,
		URI:         deployment.URI,
		ParentURI:   deployment.ParentURI,
		Ready:       true,
		PID:         os.Getpid(),
		StartedAt:   daemonStartedAt(teamDir),
		PathScope:   pathScope,
		TeamDir:     slashPath(teamDir),
		DaemonRoot:  slashPath(daemonRoot),
		TopologyURI: resource.TopologyURI(deployment.ID),
	}, nil
}

func resolveChildDeploymentRead(daemonRoot, canonical string, parsed resource.Parsed) (any, bool, error) {
	charters, err := ListTeamCharters(daemonRoot)
	if err != nil {
		return nil, true, err
	}
	charter := childDeploymentReadCharter(charters, canonical, parsed)
	if charter == nil {
		if childDeploymentReadKnown(charters, parsed.DeploymentID) {
			return nil, true, ErrResourceNotFound
		}
		return nil, false, nil
	}
	switch parsed.Kind {
	case resource.KindProject:
		if parsed.ID != parsed.DeploymentID {
			return nil, true, ErrResourceNotFound
		}
		return &projectResource{
			ID:           charter.ChildDeploymentID,
			URI:          charter.ChildDeploymentURI,
			ParentURI:    charter.ParentDeploymentURI,
			Ready:        charter.State == TeamCharterStateRunning,
			TopologyURI:  resource.TopologyURI(charter.ChildDeploymentID),
			Relationship: charter.Relationship,
			State:        charter.State,
			CharterURI:   charter.URI,
		}, true, nil
	case resource.KindCapability:
		if charter.Authority.CapabilityURI != canonical {
			return nil, true, ErrResourceNotFound
		}
		return &capabilityResource{
			ID:                 parsed.ID,
			URI:                canonical,
			CharterURI:         charter.URI,
			ChildDeploymentURI: charter.ChildDeploymentURI,
			Authority:          charter.Authority,
		}, true, nil
	default:
		return nil, true, fmt.Errorf("%w: %s", ErrResourceUnsupported, parsed.Kind)
	}
}

func childDeploymentReadCharter(charters []*TeamCharter, canonical string, parsed resource.Parsed) *TeamCharter {
	if parsed.Kind == resource.KindCapability {
		for i := len(charters) - 1; i >= 0; i-- {
			charter := charters[i]
			if charter != nil &&
				charter.ChildDeploymentID == parsed.DeploymentID &&
				charter.Authority.CapabilityURI == canonical {
				return charter
			}
		}
		return nil
	}
	for i := len(charters) - 1; i >= 0; i-- {
		charter := charters[i]
		if charter != nil &&
			charter.ChildDeploymentID == parsed.DeploymentID &&
			!teamCharterTerminal(charter.State) {
			return charter
		}
	}
	for i := len(charters) - 1; i >= 0; i-- {
		charter := charters[i]
		if charter != nil && charter.ChildDeploymentID == parsed.DeploymentID {
			return charter
		}
	}
	return nil
}

func childDeploymentReadKnown(charters []*TeamCharter, deploymentID string) bool {
	for _, charter := range charters {
		if charter != nil && charter.ChildDeploymentID == deploymentID {
			return true
		}
	}
	return false
}

func resolveAllocationResource(teamDir, id string) (*budget.AllocationRecord, error) {
	allocations, err := budget.ListAllocations(teamDir)
	if err != nil {
		return nil, err
	}
	for _, rec := range allocations {
		if rec != nil && rec.ID == id {
			return rec, nil
		}
	}
	return nil, ErrResourceNotFound
}

func resolveInstanceResource(m *InstanceManager, id string) (*Metadata, error) {
	for _, meta := range m.List() {
		if meta != nil && meta.Instance == id {
			return meta, nil
		}
	}
	meta, err := ReadMetadata(m.daemonRoot, id)
	if err != nil {
		return nil, resourceReadNotFound(err)
	}
	return meta, nil
}

func resolveJobResource(teamDir, id, fragment string) (any, error) {
	j, err := jobstore.Read(teamDir, id)
	if err != nil {
		return nil, resourceReadNotFound(err)
	}
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return tomlTaggedJSON(j), nil
	}
	const stepPrefix = "step="
	if !strings.HasPrefix(fragment, stepPrefix) {
		return nil, fmt.Errorf("%w: job fragment %q", ErrResourceUnsupported, fragment)
	}
	stepID := strings.TrimSpace(strings.TrimPrefix(fragment, stepPrefix))
	for i := range j.Steps {
		if j.Steps[i].ID == stepID {
			return tomlTaggedJSON(j.Steps[i]), nil
		}
	}
	return nil, ErrResourceNotFound
}

func resolveOutcomeResource(teamDir, id string) (*outcomes.Record, error) {
	rec, err := outcomes.ReadRecord(teamDir, id)
	if err != nil {
		return nil, resourceReadNotFound(err)
	}
	return rec, nil
}

func resolveWorkspaceResource(teamDir string, m *InstanceManager, uri, id string) (*workspaceResource, error) {
	if id == "repo" {
		return &workspaceResource{
			ID:        id,
			URI:       uri,
			Kind:      "repo",
			Path:      slashPath(filepath.Dir(teamDir)),
			PathScope: "host-local",
		}, nil
	}
	for _, meta := range m.List() {
		if meta == nil {
			continue
		}
		if meta.WorkspaceURI == uri || resource.WorkspaceID(meta.Workspace, meta.Branch, meta.Job, meta.Instance) == id {
			return &workspaceResource{
				ID:        id,
				URI:       firstNonEmpty(meta.WorkspaceURI, uri),
				Kind:      "worktree",
				Path:      slashPath(meta.Workspace),
				PathScope: "host-local",
				Branch:    meta.Branch,
				Job:       meta.Job,
				Instance:  meta.Instance,
				Status:    meta.Status,
				Owners:    []string{meta.Instance},
			}, nil
		}
	}
	jobs, err := jobstore.List(teamDir)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j == nil {
			continue
		}
		if j.WorkspaceURI == uri || resource.WorkspaceID(j.Worktree, j.Branch, j.ID, j.Instance) == id {
			owners := []string{}
			if j.Instance != "" {
				owners = append(owners, j.Instance)
			}
			return &workspaceResource{
				ID:        id,
				URI:       firstNonEmpty(j.WorkspaceURI, uri),
				Kind:      "worktree",
				Path:      slashPath(j.Worktree),
				PathScope: "host-local",
				Branch:    j.Branch,
				Job:       j.ID,
				Instance:  j.Instance,
				Owners:    owners,
			}, nil
		}
	}
	return nil, ErrResourceNotFound
}

func resolveStateResource(teamDir string, m *InstanceManager, uri, instance string) (*stateResource, error) {
	path := filepath.Join(teamDir, "state", instance)
	exists := false
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		exists = true
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if !exists && !instanceKnown(m, instance) {
		return nil, ErrResourceNotFound
	}
	out := &stateResource{
		Instance:  instance,
		URI:       uri,
		Path:      slashPath(path),
		PathScope: "host-local",
		Exists:    exists,
	}
	if snap, ok := readPhaseStatus(teamDir, instance); ok {
		out.Status = map[string]any{
			"phase":       snap.Phase,
			"description": snap.Description,
			"last_action": snap.LastAction,
			"job":         snap.Job,
			"ticket":      snap.Ticket,
			"branch":      snap.Branch,
			"pr":          snap.PR,
			"reason":      snap.Reason,
			"ask_to":      snap.AskTo,
		}
	}
	return out, nil
}

func resolveLogResource(m *InstanceManager, uri, instance string) (*logResource, error) {
	path := childLogPath(m.daemonRoot, instance)
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if !instanceKnown(m, instance) {
				return nil, ErrResourceNotFound
			}
			return &logResource{Instance: instance, URI: uri, Path: slashPath(path), PathScope: "host-local"}, nil
		}
		return nil, err
	}
	return &logResource{
		Instance:  instance,
		URI:       uri,
		Path:      slashPath(path),
		PathScope: "host-local",
		Exists:    true,
		Size:      st.Size(),
		ModTime:   st.ModTime(),
	}, nil
}

func resolveUsageResource(teamDir string, m *InstanceManager, uri, instance, fragment string) (any, error) {
	for _, meta := range m.List() {
		if meta == nil || meta.Usage == nil {
			continue
		}
		if meta.Usage.URI == uri || (meta.Usage.Instance == instance && usageFragmentMatches(meta.Usage.StartedAt, fragment)) {
			return meta.Usage, nil
		}
	}
	jobs, err := jobstore.List(teamDir)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j == nil || j.Usage == nil {
			continue
		}
		for i := range j.Usage.Records {
			rec := j.Usage.Records[i]
			if rec.URI == uri || (rec.Instance == instance && usageFragmentMatches(rec.StartedAt, fragment)) {
				return rec, nil
			}
		}
	}
	return nil, ErrResourceNotFound
}

func resolveMailboxResource(m *InstanceManager, events *EventResolver, uri, instance string) (*mailboxResource, error) {
	messages, err := ReadMessages(m.daemonRoot, instance)
	if err != nil {
		return nil, err
	}
	cursor, err := ReadCursor(m.daemonRoot, instance)
	if err != nil {
		return nil, err
	}
	unread, err := ReadUnacked(m.daemonRoot, instance)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 && cursor == "" && !instanceKnownOrDeclared(m, events, instance) {
		return nil, ErrResourceNotFound
	}
	return &mailboxResource{
		Instance:    instance,
		URI:         uri,
		Cursor:      cursor,
		UnreadCount: len(unread),
		Messages:    messages,
	}, nil
}

func resolveChannelResource(channels *ChannelStore, uri, name string) (*channelResource, error) {
	if err := ValidateChannelName(name); err != nil {
		return nil, err
	}
	list, err := channels.List()
	if err != nil {
		return nil, err
	}
	var info *ChannelInfo
	for _, item := range list {
		if item != nil && item.Name == name {
			copy := *item
			info = &copy
			break
		}
	}
	subs, err := channels.Subscriptions(name)
	if err != nil {
		return nil, err
	}
	if info == nil && len(subs) == 0 {
		return nil, ErrResourceNotFound
	}
	out := &channelResource{Name: name, URI: uri, Info: info}
	names := make([]string, 0, len(subs))
	for instance := range subs {
		names = append(names, instance)
	}
	sort.Strings(names)
	for _, instance := range names {
		sub := subs[instance]
		out.Subscriptions = append(out.Subscriptions, channelSubscriptionResource{
			Instance:     sub.Instance,
			Cursor:       sub.Cursor,
			SubscribedAt: sub.SubscribedAt,
		})
	}
	return out, nil
}

func resolveLockResource(daemonRoot string, events *EventResolver, uri, id string) (*lockResource, error) {
	out := &lockResource{ID: id, URI: uri}
	if events != nil {
		for _, snap := range events.LockSnapshots() {
			if snap.Name == id || snap.Storage == id {
				out.Snapshots = append(out.Snapshots, snap)
			}
		}
	}
	leases, err := ListLockLeases(daemonRoot)
	if err != nil {
		return nil, err
	}
	for _, lease := range leases {
		if lease != nil && (lease.Lock == id || lease.Name == id) {
			out.Leases = append(out.Leases, lease)
		}
	}
	if len(out.Snapshots) == 0 && len(out.Leases) == 0 {
		return nil, ErrResourceNotFound
	}
	return out, nil
}

func resourceReadNotFound(err error) error {
	if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
		return ErrResourceNotFound
	}
	return err
}

func instanceKnown(m *InstanceManager, instance string) bool {
	if m == nil {
		return false
	}
	for _, meta := range m.List() {
		if meta != nil && meta.Instance == instance {
			return true
		}
	}
	if _, err := ReadMetadata(m.daemonRoot, instance); err == nil {
		return true
	}
	return false
}

func instanceKnownOrDeclared(m *InstanceManager, events *EventResolver, instance string) bool {
	if instanceKnown(m, instance) {
		return true
	}
	return events != nil && events.Topology() != nil && events.Topology().Find(instance) != nil
}

func usageFragmentMatches(startedAt time.Time, fragment string) bool {
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return true
	}
	const prefix = "started_at="
	if !strings.HasPrefix(fragment, prefix) {
		return false
	}
	want := strings.TrimPrefix(fragment, prefix)
	if startedAt.IsZero() {
		return false
	}
	return startedAt.UTC().Format(time.RFC3339Nano) == want
}

func slashPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.ToSlash(path)
}

func tomlTaggedJSON(value any) any {
	return tomlTaggedJSONValue(reflect.ValueOf(value))
}

func tomlTaggedJSONValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Type() == reflect.TypeOf(time.Time{}) {
		return v.Interface()
	}
	switch v.Kind() {
	case reflect.Struct:
		out := map[string]any{}
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name, omitEmpty, ok := taggedJSONName(field)
			if !ok {
				continue
			}
			fv := v.Field(i)
			if omitEmpty && fv.IsZero() {
				continue
			}
			out[name] = tomlTaggedJSONValue(fv)
		}
		return out
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			out = append(out, tomlTaggedJSONValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := map[string]any{}
		iter := v.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			out[key] = tomlTaggedJSONValue(iter.Value())
		}
		return out
	default:
		return v.Interface()
	}
}

func taggedJSONName(field reflect.StructField) (string, bool, bool) {
	for _, key := range []string{"toml", "json"} {
		tag := field.Tag.Get(key)
		if tag == "-" {
			return "", false, false
		}
		if tag == "" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		omitEmpty := false
		for _, opt := range parts[1:] {
			if strings.TrimSpace(opt) == "omitempty" {
				omitEmpty = true
				break
			}
		}
		return name, omitEmpty, true
	}
	return lowerCamelName(field.Name), false, true
}

func lowerCamelName(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToLower(name[:1]) + name[1:]
}
