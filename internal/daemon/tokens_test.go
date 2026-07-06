package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agent-team-project/agent-team/internal/origin"
)

func TestDaemonTokensAre0600AndMapToInstanceOrigin(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	daemonRoot := DaemonRoot(teamDir)

	operatorPath, err := EnsureOperatorToken(teamDir)
	if err != nil {
		t.Fatalf("EnsureOperatorToken: %v", err)
	}
	assertTokenFileMode(t, operatorPath)
	operatorToken, err := ReadTokenFile(operatorPath)
	if err != nil {
		t.Fatalf("ReadTokenFile operator: %v", err)
	}
	if len(operatorToken) != daemonTokenBytes*2 {
		t.Fatalf("operator token length = %d, want %d", len(operatorToken), daemonTokenBytes*2)
	}
	againPath, err := EnsureOperatorToken(teamDir)
	if err != nil {
		t.Fatalf("EnsureOperatorToken again: %v", err)
	}
	againToken, err := ReadTokenFile(againPath)
	if err != nil {
		t.Fatalf("ReadTokenFile operator again: %v", err)
	}
	if againToken != operatorToken {
		t.Fatal("EnsureOperatorToken rotated an existing token")
	}

	instance := "worker-squ-130"
	instancePath, err := EnsureInstanceToken(teamDir, instance)
	if err != nil {
		t.Fatalf("EnsureInstanceToken: %v", err)
	}
	assertTokenFileMode(t, instancePath)
	instanceToken, err := ReadTokenFile(instancePath)
	if err != nil {
		t.Fatalf("ReadTokenFile instance: %v", err)
	}
	if err := WriteMetadata(daemonRoot, &Metadata{
		Instance: instance,
		Agent:    "worker",
		Job:      "squ-130",
		Origin: origin.Envelope{
			Team:     "delivery",
			Agent:    "worker",
			Instance: instance,
			Job:      "squ-130",
		},
		Workspace: t.TempDir(),
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	identity, ok := lookupBearerToken(teamDir, daemonRoot, instanceToken)
	if !ok {
		t.Fatal("instance token did not resolve")
	}
	if identity.Operator || identity.Instance != instance {
		t.Fatalf("identity = %+v, want instance %s", identity, instance)
	}
	if identity.Origin.Agent != "worker" || identity.Origin.Team != "delivery" || identity.Origin.Job != "squ-130" {
		t.Fatalf("origin = %+v, want worker delivery squ-130", identity.Origin)
	}

	identity, ok = lookupBearerToken(teamDir, daemonRoot, operatorToken)
	if !ok || !identity.Operator || identity.Instance != "" {
		t.Fatalf("operator identity = %+v ok=%v", identity, ok)
	}
}

func TestBearerTokenLookupConcurrent(t *testing.T) {
	teamDir := filepath.Join(t.TempDir(), ".agent_team")
	daemonRoot := DaemonRoot(teamDir)
	tokenPath, err := EnsureInstanceToken(teamDir, "worker-race")
	if err != nil {
		t.Fatalf("EnsureInstanceToken: %v", err)
	}
	token, err := ReadTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}
	if err := WriteMetadata(daemonRoot, &Metadata{
		Instance:  "worker-race",
		Agent:     "worker",
		Workspace: t.TempDir(),
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				identity, ok := lookupBearerToken(teamDir, daemonRoot, token)
				if !ok || identity.Instance != "worker-race" {
					errCh <- "lookup failed"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
}

func assertTokenFileMode(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("%s mode = %s, want 0600", path, got)
	}
}
