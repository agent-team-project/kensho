package daemon

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
	"github.com/agent-team-project/agent-team/internal/resource"
	"github.com/agent-team-project/agent-team/internal/runtimeotel"
)

// LaunchEnv is the daemon's boot-time process snapshot. Restart uses this to
// decouple relaunch from the shell environment of the operator running restart.
type LaunchEnv struct {
	URI                 string         `json:"uri,omitempty"`
	DeploymentURI       string         `json:"deployment_uri,omitempty"`
	DeploymentParentURI string         `json:"deployment_parent_uri,omitempty"`
	InstanceURI         string         `json:"instance_uri,omitempty"`
	WorkspaceURI        string         `json:"workspace_uri,omitempty"`
	StateURI            string         `json:"state_uri,omitempty"`
	Bin                 string         `json:"bin"`
	Args                []string       `json:"args,omitempty"`
	Dir                 string         `json:"dir,omitempty"`
	Env                 []string       `json:"env,omitempty"`
	Stripped            []string       `json:"stripped,omitempty"`
	RecordedAt          time.Time      `json:"recorded_at"`
	PID                 int            `json:"pid,omitempty"`
	Version             int            `json:"version"`
	Build               buildinfo.Info `json:"build,omitempty"`
	Assets              string         `json:"assets,omitempty"`
	ShimPath            string         `json:"shim_path,omitempty"`
	SkillsPath          string         `json:"skills_path,omitempty"`
}

var DefaultStrippedEnvKeys = []string{
	"OPENAI_API_KEY",
	"OTEL_EXPORTER_OTLP_HEADERS",
	"OTEL_EXPORTER_OTLP_TRACES_HEADERS",
	"OTEL_EXPORTER_OTLP_METRICS_HEADERS",
	"OTEL_EXPORTER_OTLP_LOGS_HEADERS",
}

const requiredLaunchEnvPrefix = "AGENT_TEAM_"
const requiredLaunchEnvMainRepo = "MAIN_REPO"

// LaunchEnvPath returns the active launch-env snapshot path for teamDir.
func LaunchEnvPath(teamDir string) string {
	return filepath.Join(DaemonRoot(teamDir), "launch-env.json")
}

// PrevLaunchEnvPath returns the previous launch-env snapshot path for teamDir.
func PrevLaunchEnvPath(teamDir string) string {
	return filepath.Join(DaemonRoot(teamDir), "launch-env.prev.json")
}

// InstanceLaunchEnvPath returns the per-instance launch-env snapshot path
// under the instance's daemon metadata directory.
func InstanceLaunchEnvPath(daemonRoot, instance string) string {
	return filepath.Join(instanceDir(daemonRoot, instance), "launch-env.json")
}

func launchEnvPathForRoot(daemonRoot string) string {
	return filepath.Join(daemonRoot, "launch-env.json")
}

func stripEnv(env []string, deny []string) []string {
	denySet := make(map[string]struct{}, len(deny))
	for _, key := range deny {
		denySet[key] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, item := range env {
		key := item
		if before, _, ok := strings.Cut(item, "="); ok {
			key = before
		}
		if _, denied := denySet[key]; denied {
			continue
		}
		out = append(out, item)
	}
	return out
}

func envEntryKey(item string) string {
	key := item
	if before, _, ok := strings.Cut(item, "="); ok {
		key = before
	}
	return key
}

func filterEnvAllow(env []string, allow []string) ([]string, error) {
	if allow == nil {
		return env, nil
	}
	patterns := make([]string, 0, len(allow))
	for i, raw := range allow {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			return nil, fmt.Errorf("env_allow[%d]: must be non-empty", i)
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("env_allow[%d]: invalid glob: %w", i, err)
		}
		patterns = append(patterns, pattern)
	}
	out := make([]string, 0, len(env))
	for _, item := range env {
		key := envEntryKey(item)
		if key == requiredLaunchEnvMainRepo || strings.HasPrefix(key, requiredLaunchEnvPrefix) || envKeyAllowed(key, patterns) {
			out = append(out, item)
		}
	}
	return out, nil
}

func envKeyAllowed(key string, patterns []string) bool {
	for _, pattern := range patterns {
		if ok, err := path.Match(pattern, key); err == nil && ok {
			return true
		}
	}
	return false
}

// StripEnv removes deny-listed KEY=VALUE entries by exact, case-sensitive key.
func StripEnv(env []string, deny []string) []string {
	return stripEnv(env, deny)
}

// WriteLaunchEnv writes le atomically with 0600 permissions. The default strip
// set is removed before serialization so denied keys are never persisted.
func WriteLaunchEnv(daemonRoot string, le *LaunchEnv) error {
	if le == nil {
		return fmt.Errorf("launch-env: nil snapshot")
	}
	if err := os.MkdirAll(daemonRoot, 0o755); err != nil {
		return fmt.Errorf("launch-env: mkdir: %w", err)
	}
	snapshot := sanitizedLaunchEnvSnapshot(le)
	backfillLaunchEnvResourceURIs(daemonRoot, "", &snapshot)
	body, err := json.MarshalIndent(&snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("launch-env: marshal: %w", err)
	}
	body = append(body, '\n')
	target := launchEnvPathForRoot(daemonRoot)
	if err := writeLaunchEnvFileAtomic(target, body); err != nil {
		return err
	}
	*le = snapshot
	return nil
}

// WriteInstanceLaunchEnv writes the resolved child process environment for one
// instance. It uses the same denied-key treatment as WriteLaunchEnv so secrets
// such as OPENAI_API_KEY are never serialized.
func WriteInstanceLaunchEnv(daemonRoot, instance string, le *LaunchEnv) error {
	if strings.TrimSpace(instance) == "" {
		return fmt.Errorf("launch-env: instance is required")
	}
	if le == nil {
		return fmt.Errorf("launch-env: nil snapshot")
	}
	dir := instanceDir(daemonRoot, instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("launch-env: mkdir: %w", err)
	}
	snapshot := sanitizedLaunchEnvSnapshot(le)
	backfillLaunchEnvResourceURIs(daemonRoot, instance, &snapshot)
	body, err := json.MarshalIndent(&snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("launch-env: marshal: %w", err)
	}
	body = append(body, '\n')
	if err := writeLaunchEnvFileAtomic(InstanceLaunchEnvPath(daemonRoot, instance), body); err != nil {
		return err
	}
	*le = snapshot
	return nil
}

func sanitizedLaunchEnvSnapshot(le *LaunchEnv) LaunchEnv {
	snapshot := *le
	snapshot.Args = runtimeotel.SanitizeArgs(le.Args)
	snapshot.Env = stripEnv(le.Env, DefaultStrippedEnvKeys)
	snapshot.Env = runtimeotel.StripGeneratedHeaderEnv(snapshot.Env)
	snapshot.Stripped = append([]string(nil), DefaultStrippedEnvKeys...)
	snapshot.Stripped = append(snapshot.Stripped, runtimeotel.CodexHeaderEnvPrefix+"*")
	return snapshot
}

// ReadLaunchEnv reads the active launch-env snapshot. Missing files wrap
// fs.ErrNotExist so callers can branch with errors.Is.
func ReadLaunchEnv(daemonRoot string) (*LaunchEnv, error) {
	path := launchEnvPathForRoot(daemonRoot)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("launch-env: read %s: %w", path, err)
	}
	var le LaunchEnv
	if err := json.Unmarshal(body, &le); err != nil {
		return nil, fmt.Errorf("launch-env: parse %s: %w", path, err)
	}
	backfillLaunchEnvResourceURIs(daemonRoot, "", &le)
	return &le, nil
}

// ReadInstanceLaunchEnv reads one instance's launch-env snapshot. Missing files
// wrap fs.ErrNotExist so callers can fall back for pre-SQU-52 metadata.
func ReadInstanceLaunchEnv(daemonRoot, instance string) (*LaunchEnv, error) {
	path := InstanceLaunchEnvPath(daemonRoot, instance)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("launch-env: read %s: %w", path, err)
	}
	var le LaunchEnv
	if err := json.Unmarshal(body, &le); err != nil {
		return nil, fmt.Errorf("launch-env: parse %s: %w", path, err)
	}
	backfillLaunchEnvResourceURIs(daemonRoot, instance, &le)
	return &le, nil
}

func backfillLaunchEnvResourceURIs(daemonRoot, instance string, le *LaunchEnv) {
	if le == nil {
		return
	}
	deployment, _ := resource.DeploymentFromTeamDir(filepath.Dir(daemonRoot))
	deploymentID := strings.TrimSpace(deployment.ID)
	if deploymentID == "" {
		return
	}
	if le.DeploymentURI == "" {
		le.DeploymentURI = deployment.URI
	}
	if le.DeploymentParentURI == "" {
		le.DeploymentParentURI = deployment.ParentURI
	}
	if le.URI == "" {
		le.URI = resource.LaunchEnvURI(deploymentID, instance)
	}
	if instance != "" {
		if le.InstanceURI == "" {
			le.InstanceURI = resource.InstanceURI(deploymentID, instance)
		}
		if le.StateURI == "" {
			le.StateURI = resource.StateURI(deploymentID, instance)
		}
	}
	if le.WorkspaceURI == "" {
		if envURI := envValue(le.Env, "AGENT_TEAM_WORKSPACE_URI"); envURI != "" {
			le.WorkspaceURI = envURI
		} else {
			le.WorkspaceURI = resource.WorkspaceURIFor(deploymentID, le.Dir, envValue(le.Env, "AGENT_TEAM_BRANCH"), envValue(le.Env, "AGENT_TEAM_JOB_ID"), instance)
		}
	}
}

func writeLaunchEnvFileAtomic(target string, body []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("launch-env: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "launch-env-*.json.tmp")
	if err != nil {
		return fmt.Errorf("launch-env: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("launch-env: chmod tempfile: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("launch-env: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("launch-env: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("launch-env: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("launch-env: rename: %w", err)
	}
	if err := os.Chmod(target, fs.FileMode(0o600)); err != nil {
		return fmt.Errorf("launch-env: chmod: %w", err)
	}
	return nil
}
