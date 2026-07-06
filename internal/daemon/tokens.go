package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-team-project/agent-team/internal/origin"
)

// DaemonTokenFileEnv points agent processes at their bearer-token file. The
// token value itself must never be exported in the environment.
const DaemonTokenFileEnv = "AGENT_TEAM_DAEMON_TOKEN_FILE"

const daemonTokenBytes = 32

// OperatorTokenPath returns the machine-local token file used by CLI clients
// when they must reach agent-teamd over loopback HTTP.
func OperatorTokenPath(teamDir string) string {
	return filepath.Join(DaemonRoot(teamDir), "operator.token")
}

// InstanceTokenPath returns the private per-instance daemon bearer token file.
func InstanceTokenPath(teamDir, instance string) string {
	return filepath.Join(teamDir, "state", instance, "daemon.token")
}

// EnsureOperatorToken creates the operator token file if missing and returns
// its path. Existing non-empty files are reused across daemon restarts.
func EnsureOperatorToken(teamDir string) (string, error) {
	return ensureTokenFile(OperatorTokenPath(teamDir))
}

// EnsureInstanceToken creates the per-instance token file if missing and
// returns its path. Existing non-empty files survive managed resumes.
func EnsureInstanceToken(teamDir, instance string) (string, error) {
	if strings.TrimSpace(instance) == "" {
		return "", errors.New("daemon token: instance is required")
	}
	return ensureTokenFile(InstanceTokenPath(teamDir, instance))
}

// MintInstanceToken writes a fresh per-instance token and returns its path.
func MintInstanceToken(teamDir, instance string) (string, error) {
	if strings.TrimSpace(instance) == "" {
		return "", errors.New("daemon token: instance is required")
	}
	token, err := newBearerToken()
	if err != nil {
		return "", err
	}
	path := InstanceTokenPath(teamDir, instance)
	if err := writeSecretFileAtomic(path, []byte(token+"\n")); err != nil {
		return "", err
	}
	return path, nil
}

func ensureTokenFile(path string) (string, error) {
	if token, err := ReadTokenFile(path); err == nil && token != "" {
		_ = os.Chmod(path, 0o600)
		return path, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	token, err := newBearerToken()
	if err != nil {
		return "", err
	}
	if err := writeSecretFileAtomic(path, []byte(token+"\n")); err != nil {
		return "", err
	}
	return path, nil
}

func newBearerToken() (string, error) {
	var buf [daemonTokenBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("daemon token: random: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// ReadTokenFile returns a trimmed daemon bearer token from path.
func ReadTokenFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func writeSecretFileAtomic(target string, body []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("daemon token: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "token-*.tmp")
	if err != nil {
		return fmt.Errorf("daemon token: tempfile: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("daemon token: chmod tempfile: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("daemon token: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("daemon token: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("daemon token: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("daemon token: rename: %w", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return fmt.Errorf("daemon token: chmod: %w", err)
	}
	return nil
}

type bearerTokenIdentity struct {
	Instance string
	Operator bool
	Origin   origin.Envelope
}

func lookupBearerToken(teamDir, daemonRoot, token string) (bearerTokenIdentity, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return bearerTokenIdentity{}, false
	}
	if operator, err := ReadTokenFile(OperatorTokenPath(teamDir)); err == nil && constantTimeEqual(operator, token) {
		return bearerTokenIdentity{Operator: true}, true
	}
	stateRoot := filepath.Join(teamDir, "state")
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		return bearerTokenIdentity{}, false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instance := entry.Name()
		stored, err := ReadTokenFile(InstanceTokenPath(teamDir, instance))
		if err != nil || !constantTimeEqual(stored, token) {
			continue
		}
		return bearerTokenIdentity{
			Instance: instance,
			Origin:   originForInstanceToken(teamDir, daemonRoot, instance),
		}, true
	}
	return bearerTokenIdentity{}, false
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func originForInstanceToken(teamDir, daemonRoot, instance string) origin.Envelope {
	out := origin.Envelope{Instance: instance, Project: projectIDForTeamDir(teamDir)}
	meta, err := ReadMetadata(daemonRoot, instance)
	if err != nil || meta == nil {
		return out.Clean()
	}
	metaOrigin := meta.Origin
	if metaOrigin.Instance == "" {
		metaOrigin.Instance = meta.Instance
	}
	if metaOrigin.Agent == "" {
		metaOrigin.Agent = meta.Agent
	}
	if metaOrigin.Job == "" {
		metaOrigin.Job = meta.Job
	}
	return origin.Merge(metaOrigin, out).Clean()
}
