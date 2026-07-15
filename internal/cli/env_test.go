package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	inheritedCLI := os.Getenv("AGENT_TEAM_TEST_MANAGED_CLI")
	scrubAgentTeamEnvForTestProcess()
	if inheritedCLI == "" && !testProcessRunsInModule() {
		os.Exit(m.Run())
	}
	cleanup := installManagedCLITestBinary(inheritedCLI)
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func testProcessRunsInModule() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func installManagedCLITestBinary(inherited string) func() {
	oldPath := os.Getenv("PATH")
	if inherited != "" {
		if err := os.Setenv("PATH", filepath.Dir(inherited)+string(os.PathListSeparator)+oldPath); err != nil {
			panic(err)
		}
		if err := os.Setenv("AGENT_TEAM_TEST_MANAGED_CLI", inherited); err != nil {
			panic(err)
		}
		return func() {
			_ = os.Setenv("PATH", oldPath)
			_ = os.Unsetenv("AGENT_TEAM_TEST_MANAGED_CLI")
		}
	}
	dir, err := os.MkdirTemp("", "cli-test-agent-team")
	if err != nil {
		panic(err)
	}
	out := filepath.Join(dir, "agent-team")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if body, err := exec.Command(goBinary, "build", "-o", out, "github.com/agent-team-project/agent-team/cmd/agent-team").CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		panic(string(body))
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		_ = os.RemoveAll(dir)
		panic(err)
	}
	if err := os.Setenv("AGENT_TEAM_TEST_MANAGED_CLI", out); err != nil {
		_ = os.RemoveAll(dir)
		panic(err)
	}
	return func() {
		_ = os.Setenv("PATH", oldPath)
		_ = os.Unsetenv("AGENT_TEAM_TEST_MANAGED_CLI")
		_ = os.RemoveAll(dir)
	}
}

// Tests should behave like CI even when launched from an agent-team worker.
func scrubAgentTeamEnvForTestProcess() {
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "AGENT_TEAM_") {
			_ = os.Unsetenv(key)
		}
	}
}
