// Package resource renders stable agent-team resource identifiers.
package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const Scheme = "agt"

const (
	KindProject    = "project"
	KindInstance   = "instance"
	KindJob        = "job"
	KindOutcome    = "outcome"
	KindWorkspace  = "workspace"
	KindState      = "state"
	KindLog        = "log"
	KindUsage      = "usage"
	KindMailbox    = "mailbox"
	KindChannel    = "channel"
	KindQueue      = "queue"
	KindOutbox     = "outbox"
	KindLock       = "lock"
	KindTopology   = "topology"
	KindCharter    = "charter"
	KindCapability = "capability"
	KindAllocation = "allocation"
)

// Deployment is the local control-plane deployment identity from
// `.agent_team/config.toml`.
type Deployment struct {
	ID        string `json:"id,omitempty" toml:"id,omitempty"`
	URI       string `json:"uri,omitempty" toml:"uri,omitempty"`
	ParentURI string `json:"parent_uri,omitempty" toml:"parent_uri,omitempty"`
}

// Parsed is a decoded agt:// resource URI.
type Parsed struct {
	DeploymentID string
	Kind         string
	ID           string
	Fragment     string
}

// URI renders `agt://<deployment-id>/<kind>/<id>`.
func URI(deploymentID, kind, id string) string {
	deploymentID = strings.TrimSpace(deploymentID)
	kind = strings.Trim(strings.TrimSpace(kind), "/")
	id = strings.Trim(strings.TrimSpace(id), "/")
	if deploymentID == "" || kind == "" || id == "" {
		return ""
	}
	return Scheme + "://" + deploymentID + "/" + url.PathEscape(kind) + "/" + url.PathEscape(id)
}

// URIWithFragment renders a URI with an optional fragment.
func URIWithFragment(deploymentID, kind, id, fragment string) string {
	base := URI(deploymentID, kind, id)
	fragment = strings.TrimSpace(fragment)
	if base == "" || fragment == "" {
		return base
	}
	return base + "#" + url.PathEscape(fragment)
}

func ProjectURI(deploymentID string) string {
	return URI(deploymentID, KindProject, deploymentID)
}

// DeploymentURI renders the deployment self resource URI. The deployment self
// is currently modeled as the project resource for that deployment.
func DeploymentURI(deploymentID string) string {
	return ProjectURI(deploymentID)
}

// ChildDeploymentID returns a stable, URI-host-safe deployment id for a child
// control plane created under parentDeploymentID for name.
func ChildDeploymentID(parentDeploymentID, name string) string {
	parentDeploymentID = strings.TrimSpace(parentDeploymentID)
	name = strings.TrimSpace(name)
	if parentDeploymentID == "" || name == "" {
		return ""
	}
	base := safeDeploymentIDSegment(name)
	if base == "" {
		base = "child"
	}
	sum := sha256.Sum256([]byte(parentDeploymentID + "\x00" + name))
	return "child-" + base + "-" + hex.EncodeToString(sum[:4])
}

func CharterURI(deploymentID, charterID string) string {
	return URI(deploymentID, KindCharter, charterID)
}

func CapabilityURI(deploymentID, capabilityID string) string {
	return URI(deploymentID, KindCapability, capabilityID)
}

func AllocationURI(deploymentID, allocationID string) string {
	return URI(deploymentID, KindAllocation, allocationID)
}

func InstanceURI(deploymentID, instance string) string {
	return URI(deploymentID, KindInstance, instance)
}

func JobURI(deploymentID, jobID string) string {
	return URI(deploymentID, KindJob, jobID)
}

func OutcomeURI(deploymentID, jobID string) string {
	return URI(deploymentID, KindOutcome, jobID)
}

func StepURI(deploymentID, jobID, stepID string) string {
	return URIWithFragment(deploymentID, KindJob, jobID, "step="+strings.TrimSpace(stepID))
}

func WorkspaceURI(deploymentID, workspaceID string) string {
	return URI(deploymentID, KindWorkspace, workspaceID)
}

func WorkspaceURIFor(deploymentID, workspacePath, branch, jobID, instance string) string {
	return WorkspaceURI(deploymentID, WorkspaceID(workspacePath, branch, jobID, instance))
}

func StateURI(deploymentID, instance string) string {
	return URI(deploymentID, KindState, instance)
}

func LogURI(deploymentID, instance string) string {
	return URI(deploymentID, KindLog, instance)
}

func MailboxURI(deploymentID, instance string) string {
	return URI(deploymentID, KindMailbox, instance)
}

func ChannelURI(deploymentID, channel string) string {
	return URI(deploymentID, KindChannel, channel)
}

func QueueURI(deploymentID, id string) string {
	return URI(deploymentID, KindQueue, id)
}

func OutboxURI(deploymentID, id string) string {
	return URI(deploymentID, KindOutbox, id)
}

func LockURI(deploymentID, id string) string {
	return URI(deploymentID, KindLock, id)
}

func TopologyURI(deploymentID string) string {
	return URI(deploymentID, KindTopology, "current")
}

func LaunchEnvURI(deploymentID, instance string) string {
	if strings.TrimSpace(instance) != "" {
		return URIWithFragment(deploymentID, KindState, instance, "launch-env")
	}
	return URIWithFragment(deploymentID, KindProject, deploymentID, "launch-env")
}

func UsageURI(deploymentID, instance string, startedAt time.Time) string {
	fragment := ""
	if !startedAt.IsZero() {
		fragment = "started_at=" + startedAt.UTC().Format(time.RFC3339Nano)
	}
	return URIWithFragment(deploymentID, KindUsage, instance, fragment)
}

func safeDeploymentIDSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '-' || r == '_' || r == '.' || r == ' ' {
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	return out
}

// WorkspaceID returns a deterministic resource id for a workspace
// materialization. Branches are preferred because daemon-created worktrees
// already get a stable branch id; path hashes are a compatibility backfill for
// records that predate workspace ids.
func WorkspaceID(workspacePath, branch, jobID, instance string) string {
	if branch = strings.TrimSpace(branch); branch != "" {
		return "branch:" + branch
	}
	if jobID = strings.TrimSpace(jobID); jobID != "" {
		return "job:" + jobID
	}
	if instance = strings.TrimSpace(instance); instance != "" {
		return "instance:" + instance
	}
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(workspacePath))
	if clean == "." {
		clean = workspacePath
	}
	sum := sha256.Sum256([]byte(clean))
	return "path:" + hex.EncodeToString(sum[:8])
}

// DeploymentFromTeamDir reads the stable deployment id and optional parent URI
// from `.agent_team/config.toml`. Missing config behaves like ProjectID: it
// returns an empty deployment without error.
func DeploymentFromTeamDir(teamDir string) (Deployment, error) {
	cfg := filepath.Join(teamDir, "config.toml")
	if _, err := os.Stat(cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Deployment{}, nil
		}
		return Deployment{}, err
	}
	var raw map[string]any
	if _, err := toml.DecodeFile(cfg, &raw); err != nil {
		return Deployment{}, err
	}
	project, _ := raw["project"].(map[string]any)
	id, _ := project["id"].(string)
	parentURI, _ := project["parent_uri"].(string)
	deployment := Deployment{
		ID:        strings.TrimSpace(id),
		ParentURI: strings.TrimSpace(parentURI),
	}
	deployment.URI = DeploymentURI(deployment.ID)
	return deployment, nil
}

// Parse decodes and validates a canonical agt:// resource URI.
func Parse(raw string) (Parsed, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Parsed{}, errors.New("resource URI is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Parsed{}, err
	}
	if u.Scheme != Scheme {
		return Parsed{}, fmt.Errorf("resource URI scheme %q must be %q", u.Scheme, Scheme)
	}
	if u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery {
		return Parsed{}, errors.New("resource URI must be canonical agt://<deployment-id>/<kind>/<id>[#fragment]")
	}
	deploymentID := strings.TrimSpace(u.Host)
	if deploymentID == "" {
		return Parsed{}, errors.New("resource URI deployment id is required")
	}
	path := u.EscapedPath()
	if path == "" || !strings.HasPrefix(path, "/") {
		return Parsed{}, fmt.Errorf("resource URI path must be /<kind>/<id>")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Parsed{}, fmt.Errorf("resource URI path must be /<kind>/<id>")
	}
	kind, err := url.PathUnescape(parts[0])
	if err != nil {
		return Parsed{}, fmt.Errorf("resource URI kind: %w", err)
	}
	id, err := url.PathUnescape(parts[1])
	if err != nil {
		return Parsed{}, fmt.Errorf("resource URI id: %w", err)
	}
	if strings.TrimSpace(kind) == "" || strings.TrimSpace(id) == "" {
		return Parsed{}, errors.New("resource URI kind and id are required")
	}
	return Parsed{
		DeploymentID: deploymentID,
		Kind:         kind,
		ID:           id,
		Fragment:     u.Fragment,
	}, nil
}
