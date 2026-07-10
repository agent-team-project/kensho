package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/origin"
	"github.com/agent-team-project/agent-team/internal/resource"
	"github.com/agent-team-project/agent-team/internal/usage"
)

// Status is an instance's lifecycle state, surfaced via GET /v1/instances.
type Status string

const (
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusExited  Status = "exited"
	StatusCrashed Status = "crashed"
)

// Metadata is the disk-durable record for one instance under
// `.agent_team/daemon/<instance>/meta.json`. It is the source of truth on
// daemon restart — the in-memory map is rebuilt from these files.
type Metadata struct {
	Instance            string          `json:"instance"`
	URI                 string          `json:"uri,omitempty"`
	SpecURI             string          `json:"spec_uri,omitempty"`
	DeploymentURI       string          `json:"deployment_uri,omitempty"`
	DeploymentParentURI string          `json:"deployment_parent_uri,omitempty"`
	Chartered           bool            `json:"chartered,omitempty"`
	CharterURI          string          `json:"charter_uri,omitempty"`
	CapabilityURI       string          `json:"capability_uri,omitempty"`
	Agent               string          `json:"agent"`
	Job                 string          `json:"job,omitempty"`
	JobURI              string          `json:"job_uri,omitempty"`
	Ticket              string          `json:"ticket,omitempty"`
	Branch              string          `json:"branch,omitempty"`
	PR                  string          `json:"pr,omitempty"`
	Origin              origin.Envelope `json:"origin,omitempty"`
	Runtime             string          `json:"runtime,omitempty"`
	RuntimeBinary       string          `json:"runtime_binary,omitempty"`
	Model               string          `json:"model,omitempty"`
	Effort              string          `json:"effort,omitempty"`
	// EffectiveRuntime is the delegated runtime whose logs expose usage data.
	// Empty means Runtime is also the effective runtime.
	EffectiveRuntime string        `json:"effective_runtime,omitempty"`
	Workspace        string        `json:"workspace"`
	WorkspaceURI     string        `json:"workspace_uri,omitempty"`
	StateURI         string        `json:"state_uri,omitempty"`
	PID              int           `json:"pid"`
	SessionID        string        `json:"session_id,omitempty"`
	StartedAt        time.Time     `json:"started_at"`
	RuntimeBudget    string        `json:"runtime_budget,omitempty"`
	RuntimeDeadline  time.Time     `json:"runtime_deadline,omitempty"`
	ResumeCount      int           `json:"resume_count,omitempty"`
	FreshFallback    bool          `json:"fresh_fallback,omitempty"`
	FreshFallbacks   int           `json:"fresh_fallback_count,omitempty"`
	StoppedAt        time.Time     `json:"stopped_at,omitempty"`
	ExitedAt         time.Time     `json:"exited_at,omitempty"`
	Status           Status        `json:"status"`
	LogPath          string        `json:"log_path,omitempty"`
	LogURI           string        `json:"log_uri,omitempty"`
	ExitCode         *int          `json:"exit_code,omitempty"`
	Usage            *usage.Record `json:"usage,omitempty"`
	Adopted          bool          `json:"adopted,omitempty"`
	// RestartBackoffUntil suppresses policy-driven relaunch attempts until the
	// timestamp, preventing tight crash loops across daemon restarts.
	RestartBackoffUntil time.Time `json:"restart_backoff_until,omitempty"`
}

// instanceDir returns the per-instance metadata dir under daemonRoot.
func instanceDir(daemonRoot, instance string) string {
	return filepath.Join(daemonRoot, instance)
}

// metadataPath returns the meta.json path for an instance.
func metadataPath(daemonRoot, instance string) string {
	return filepath.Join(instanceDir(daemonRoot, instance), "meta.json")
}

// WriteMetadata writes m atomically to its meta.json. The temp+rename pattern
// keeps readers from observing a half-written file.
func WriteMetadata(daemonRoot string, m *Metadata) error {
	if m.Instance == "" {
		return errors.New("metadata: instance is required")
	}
	backfillMetadataResourceURIs(daemonRoot, m)
	if err := os.MkdirAll(instanceDir(daemonRoot, m.Instance), 0o755); err != nil {
		return fmt.Errorf("metadata: mkdir: %w", err)
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("metadata: marshal: %w", err)
	}
	body = append(body, '\n')
	target := metadataPath(daemonRoot, m.Instance)
	tmp, err := os.CreateTemp(instanceDir(daemonRoot, m.Instance), "meta-*.json.tmp")
	if err != nil {
		return fmt.Errorf("metadata: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("metadata: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("metadata: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("metadata: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("metadata: rename: %w", err)
	}
	return nil
}

// ReadMetadata loads one instance's record. Missing file returns (nil, fs.ErrNotExist).
func ReadMetadata(daemonRoot, instance string) (*Metadata, error) {
	body, err := os.ReadFile(metadataPath(daemonRoot, instance))
	if err != nil {
		return nil, err
	}
	var m Metadata
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("metadata: parse %s: %w", instance, err)
	}
	backfillMetadataResourceURIs(daemonRoot, &m)
	return &m, nil
}

// ListMetadata reads every instance record under daemonRoot. Directories
// without a meta.json are skipped silently — they may be mid-write or stale
// debris.
func ListMetadata(daemonRoot string) ([]*Metadata, error) {
	entries, err := os.ReadDir(daemonRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Metadata
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := ReadMetadata(daemonRoot, e.Name())
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out, nil
}

// RemoveInstance deletes all metadata for a given instance. Used on terminal
// removal — `instance rm` style flows live in the CLI, not here, but the
// daemon needs this for ephemeral cleanup.
func RemoveInstance(daemonRoot, instance string) error {
	return os.RemoveAll(instanceDir(daemonRoot, instance))
}

func backfillMetadataResourceURIs(daemonRoot string, m *Metadata) {
	if m == nil {
		return
	}
	deployment, _ := resource.DeploymentFromTeamDir(filepath.Dir(daemonRoot))
	deploymentID := strings.TrimSpace(deployment.ID)
	if deploymentID == "" {
		return
	}
	if m.DeploymentURI == "" {
		m.DeploymentURI = deployment.URI
	}
	if m.DeploymentParentURI == "" {
		m.DeploymentParentURI = deployment.ParentURI
	}
	if m.URI == "" {
		m.URI = resource.InstanceURI(deploymentID, m.Instance)
	}
	if m.SpecURI == "" {
		m.SpecURI = m.URI
	}
	if m.Job != "" && m.JobURI == "" {
		m.JobURI = resource.JobURI(deploymentID, m.Job)
	}
	if m.WorkspaceURI == "" {
		m.WorkspaceURI = resource.WorkspaceURIFor(deploymentID, m.Workspace, m.Branch, m.Job, m.Instance)
	}
	if m.StateURI == "" {
		m.StateURI = resource.StateURI(deploymentID, m.Instance)
	}
	if m.LogPath != "" && m.LogURI == "" {
		m.LogURI = resource.LogURI(deploymentID, m.Instance)
	}
	m.Origin = m.Origin.WithResourceURIs()
	if m.Usage != nil {
		if m.Usage.DeploymentURI == "" {
			m.Usage.DeploymentURI = m.DeploymentURI
		}
		if m.Usage.DeploymentParentURI == "" {
			m.Usage.DeploymentParentURI = m.DeploymentParentURI
		}
		if m.Usage.InstanceURI == "" {
			m.Usage.InstanceURI = m.URI
		}
		if m.Usage.JobURI == "" {
			m.Usage.JobURI = m.JobURI
		}
		if m.Usage.WorkspaceURI == "" {
			m.Usage.WorkspaceURI = m.WorkspaceURI
		}
		if m.Usage.SourceURI == "" {
			m.Usage.SourceURI = m.LogURI
		}
		if m.Usage.URI == "" {
			m.Usage.URI = resource.UsageURI(deploymentID, m.Usage.Instance, m.Usage.StartedAt)
		}
		m.Usage.Origin = m.Usage.Origin.WithResourceURIs()
	}
}
