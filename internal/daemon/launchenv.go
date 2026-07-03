package daemon

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jamesaud/agent-team/internal/buildinfo"
)

// LaunchEnv is the daemon's boot-time process snapshot. Restart uses this to
// decouple relaunch from the shell environment of the operator running restart.
type LaunchEnv struct {
	Bin        string         `json:"bin"`
	Args       []string       `json:"args,omitempty"`
	Dir        string         `json:"dir,omitempty"`
	Env        []string       `json:"env,omitempty"`
	Stripped   []string       `json:"stripped,omitempty"`
	RecordedAt time.Time      `json:"recorded_at"`
	PID        int            `json:"pid,omitempty"`
	Version    int            `json:"version"`
	Build      buildinfo.Info `json:"build,omitempty"`
}

var DefaultStrippedEnvKeys = []string{"OPENAI_API_KEY"}

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
	snapshot.Args = append([]string(nil), le.Args...)
	snapshot.Env = stripEnv(le.Env, DefaultStrippedEnvKeys)
	snapshot.Stripped = append([]string(nil), DefaultStrippedEnvKeys...)
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
	return &le, nil
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
