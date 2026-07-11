package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agent-team-project/agent-team/internal/origin"
)

const (
	SnapshotSchema = "agent-team.tui-snapshot.v1"
	cacheFilename  = "tui-last-good.json"
)

type SnapshotSource string

const (
	SourceInstances SnapshotSource = "instances"
	SourceJobs      SnapshotSource = "jobs"
	SourceTopology  SnapshotSource = "topology"
	SourceResources SnapshotSource = "resources"
)

var snapshotSources = [...]SnapshotSource{SourceInstances, SourceJobs, SourceTopology, SourceResources}

// Snapshot is the shared, typed, read-only projection used by terminal
// frontends. SourceErrors and SourceTimes preserve partial-refresh honesty.
type Snapshot struct {
	Schema             string                       `json:"schema"`
	TeamDir            string                       `json:"team_dir"`
	DeploymentID       string                       `json:"deployment_id"`
	CapturedAt         time.Time                    `json:"captured_at"`
	Connection         Connection                   `json:"connection"`
	Instances          []*Instance                  `json:"instances"`
	Jobs               []*Job                       `json:"jobs"`
	Topology           *Topology                    `json:"topology"`
	Resources          map[string]*Resource         `json:"resources"`
	ResourcesRequested int                          `json:"resources_requested"`
	SourceTimes        map[SnapshotSource]time.Time `json:"source_times"`
	SourceErrors       map[SnapshotSource]string    `json:"source_errors,omitempty"`
}

// Complete reports whether every dashboard-parity source refreshed.
func (s *Snapshot) Complete() bool {
	if s == nil || len(s.SourceErrors) != 0 {
		return false
	}
	for _, source := range snapshotSources {
		if s.SourceTimes[source].IsZero() {
			return false
		}
	}
	return true
}

// Usable reports whether the snapshot contains at least one successful source.
func (s *Snapshot) Usable() bool {
	if s == nil {
		return false
	}
	for _, at := range s.SourceTimes {
		if !at.IsZero() {
			return true
		}
	}
	return false
}

// SnapshotSources returns the stable source order used by views and tests.
func SnapshotSources() []SnapshotSource {
	return append([]SnapshotSource(nil), snapshotSources[:]...)
}

// Snapshot collects the three dashboard collections and their resource URI
// enrichment. It never issues a mutating request.
func (c *Client) Snapshot(ctx context.Context, at time.Time) *Snapshot {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	teamDir, _ := filepath.Abs(c.teamDir)
	deploymentID, _ := origin.ProjectID(c.teamDir)
	out := &Snapshot{
		Schema:       SnapshotSchema,
		TeamDir:      filepath.Clean(teamDir),
		DeploymentID: strings.TrimSpace(deploymentID),
		CapturedAt:   at,
		Connection:   c.Connection(),
		Resources:    map[string]*Resource{},
		SourceTimes:  map[SnapshotSource]time.Time{},
		SourceErrors: map[SnapshotSource]string{},
	}

	type collectionResult struct {
		source SnapshotSource
		value  any
		err    error
	}
	results := make(chan collectionResult, 3)
	go func() { value, err := c.Instances(); results <- collectionResult{SourceInstances, value, err} }()
	go func() { value, err := c.Jobs(); results <- collectionResult{SourceJobs, value, err} }()
	go func() { value, err := c.Topology(); results <- collectionResult{SourceTopology, value, err} }()
	for range 3 {
		select {
		case <-ctx.Done():
			for _, source := range []SnapshotSource{SourceInstances, SourceJobs, SourceTopology} {
				if _, ok := out.SourceTimes[source]; !ok {
					out.SourceErrors[source] = ctx.Err().Error()
				}
			}
			return out
		case result := <-results:
			if result.err != nil {
				out.SourceErrors[result.source] = result.err.Error()
				continue
			}
			out.SourceTimes[result.source] = at
			switch result.source {
			case SourceInstances:
				out.Instances, _ = result.value.([]*Instance)
			case SourceJobs:
				out.Jobs, _ = result.value.([]*Job)
			case SourceTopology:
				out.Topology, _ = result.value.(*Topology)
			}
		}
	}

	resourceURIs := SnapshotResourceURIs(out.Instances, out.Jobs)
	out.ResourcesRequested = len(resourceURIs)
	resourceErrors := resourceDiscoveryErrors(out.SourceErrors)
	if len(resourceURIs) == 0 {
		if len(resourceErrors) > 0 {
			out.SourceErrors[SourceResources] = strings.Join(resourceErrors, "; ")
		} else {
			out.SourceTimes[SourceResources] = at
		}
		return out
	}
	type resourceResult struct {
		uri      string
		resource *Resource
		err      error
	}
	jobs := make(chan string)
	resources := make(chan resourceResult, len(resourceURIs))
	workerCount := 16
	if len(resourceURIs) < workerCount {
		workerCount = len(resourceURIs)
	}
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for uri := range jobs {
				resource, err := c.Resource(uri)
				resources <- resourceResult{uri: uri, resource: resource, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, uri := range resourceURIs {
			select {
			case jobs <- uri:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(resources)
	}()
	for result := range resources {
		if result.err != nil {
			resourceErrors = append(resourceErrors, fmt.Sprintf("%s: %v", result.uri, result.err))
			continue
		}
		out.Resources[result.uri] = result.resource
	}
	if err := ctx.Err(); err != nil {
		resourceErrors = append(resourceErrors, err.Error())
	}
	if len(resourceErrors) > 0 {
		sort.Strings(resourceErrors)
		out.SourceErrors[SourceResources] = strings.Join(resourceErrors, "; ")
	} else {
		out.SourceTimes[SourceResources] = at
	}
	return out
}

func resourceDiscoveryErrors(sourceErrors map[SnapshotSource]string) []string {
	var errors []string
	for _, source := range []SnapshotSource{SourceInstances, SourceJobs} {
		if message := strings.TrimSpace(sourceErrors[source]); message != "" {
			errors = append(errors, fmt.Sprintf("resource discovery incomplete because %s failed: %s", source, message))
		}
	}
	return errors
}

// SnapshotResourceURIs returns the stable resource set implied by the current
// instance and job collections. Frontends use the same formula when merging a
// partial refresh with retained failed collections.
func SnapshotResourceURIs(instances []*Instance, jobs []*Job) []string {
	seen := map[string]bool{}
	add := func(values ...string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				seen[value] = true
			}
		}
	}
	for _, instance := range instances {
		if instance != nil {
			add(instance.URI, instance.DeploymentURI, instance.JobURI, instance.WorkspaceURI, instance.StateURI, instance.LogURI)
		}
	}
	for _, job := range jobs {
		if job != nil {
			add(job.URI, job.OutcomeURI, job.DeploymentURI, job.InstanceURI, job.WorkspaceURI)
		}
	}
	out := make([]string, 0, len(seen))
	for uri := range seen {
		out = append(out, uri)
	}
	sort.Strings(out)
	return out
}

// SaveSnapshotCache atomically persists a complete last-good snapshot as
// sensitive local state. Partial snapshots never replace the last good copy.
func SaveSnapshotCache(teamDir string, snapshot *Snapshot) error {
	if snapshot == nil || !snapshot.Complete() {
		return errors.New("daemonclient: refusing to cache incomplete snapshot")
	}
	absTeamDir, err := filepath.Abs(teamDir)
	if err != nil {
		return err
	}
	deploymentID, err := origin.ProjectID(teamDir)
	if err != nil {
		return fmt.Errorf("daemonclient: snapshot identity: %w", err)
	}
	copy := *snapshot
	copy.Schema = SnapshotSchema
	copy.TeamDir = filepath.Clean(absTeamDir)
	copy.DeploymentID = strings.TrimSpace(deploymentID)
	copy.Connection = Connection{}
	body, err := json.MarshalIndent(&copy, "", "  ")
	if err != nil {
		return err
	}
	path := SnapshotCachePath(teamDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// LoadSnapshotCache validates schema, team directory, and deployment identity.
func LoadSnapshotCache(teamDir string) (*Snapshot, error) {
	body, err := os.ReadFile(SnapshotCachePath(teamDir))
	if err != nil {
		return nil, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("daemonclient: decode snapshot cache: %w", err)
	}
	absTeamDir, err := filepath.Abs(teamDir)
	if err != nil {
		return nil, err
	}
	deploymentID, err := origin.ProjectID(teamDir)
	if err != nil {
		return nil, err
	}
	if snapshot.Schema != SnapshotSchema || filepath.Clean(snapshot.TeamDir) != filepath.Clean(absTeamDir) || snapshot.DeploymentID != strings.TrimSpace(deploymentID) {
		return nil, errors.New("daemonclient: snapshot cache identity mismatch")
	}
	if !snapshot.Usable() {
		return nil, errors.New("daemonclient: snapshot cache is empty")
	}
	return &snapshot, nil
}

func SnapshotCachePath(teamDir string) string {
	return filepath.Join(teamDir, "cache", cacheFilename)
}
