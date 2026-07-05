package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/daemon"
)

const planShapeTopologyFixture = `
# Fixture owned by plan/sync/monitor/lifecycle/watch shape tests.
# Keep exact row/count assertions coupled to this local topology, not to the
# bundled template's evolving default instances.
[instances.manager]
agent = "manager"

[instances.ticket-manager]
agent = "ticket-manager"

[instances.feedback-triage]
agent = "manager"
ephemeral = true

[instances.harness-reviewer]
agent = "manager"
ephemeral = true

[instances.reviewer]
agent = "reviewer"
ephemeral = true

[instances.worker]
agent = "worker"
ephemeral = true
`

func writePlanShapeTopologyFixture(t *testing.T, root string) {
	t.Helper()
	teamDir := filepath.Join(root, ".agent_team")
	if err := os.WriteFile(filepath.Join(teamDir, "instances.toml"), []byte(planShapeTopologyFixture), 0o644); err != nil {
		t.Fatalf("write plan-shape instances.toml fixture: %v", err)
	}
}

// fakeSpawnerForTest returns a daemon.Spawner that runs `sleep <hold>` so
// the daemon's reaper has a real child to Wait() on. Mirrors the helper in
// internal/daemon's tests but lives in the cli package so the cli tests can
// drive an in-process daemon manager via daemon.NewInstanceManager directly.
func fakeSpawnerForTest(t *testing.T, hold time.Duration) daemon.Spawner {
	t.Helper()
	holdSecs := int(hold.Seconds())
	if holdSecs < 1 {
		holdSecs = 1
	}
	return func(args []string, env []string, workspace, stdoutPath, stderrPath, stdinContent string) (*os.Process, error) {
		bin, err := exec.LookPath("sleep")
		if err != nil {
			return nil, err
		}
		stdin, _ := os.Open(os.DevNull)
		stdout, _ := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		stderr, _ := os.OpenFile(stderrPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		defer stdin.Close()
		defer stdout.Close()
		defer stderr.Close()
		return os.StartProcess(bin, []string{"sleep", strconv.Itoa(holdSecs)}, &os.ProcAttr{
			Dir:   workspace,
			Env:   env,
			Files: []*os.File{stdin, stdout, stderr},
		})
	}
}

// writeChildLogForTest seeds the on-disk file the StreamLogs handler reads.
// Path matches the daemon's spawner contract: <daemonRoot>/<instance>/child.log.
func writeChildLogForTest(t *testing.T, daemonRoot, instance, content string) {
	t.Helper()
	dir := filepath.Join(daemonRoot, instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.log"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// stopAndWaitForTest signals the instance to stop and blocks until the
// daemon's reaper goroutine has finalised. Without this, t.TempDir's cleanup
// races the reaper's WriteMetadata rename.
func stopAndWaitForTest(t *testing.T, m *daemon.InstanceManager, instance string) {
	t.Helper()
	_, _ = m.Stop(instance)
	if err := m.WaitForReaper(instance, 10*time.Second); err != nil {
		t.Fatalf("wait reaper: %v", err)
	}
}
