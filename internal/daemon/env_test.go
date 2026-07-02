package daemon

import (
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	scrubAgentTeamEnvForTestProcess()
	os.Exit(m.Run())
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
