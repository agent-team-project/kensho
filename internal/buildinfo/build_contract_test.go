package buildinfo

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRevisionlessIdentityIsImmutableAcrossSameCheckoutAdvance(t *testing.T) {
	if testing.Short() {
		t.Skip("builds production CLI and daemon binaries")
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source root")
	}
	sourceRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	buildRoot := filepath.Join(t.TempDir(), "source")
	copyBuildSource(t, sourceRoot, buildRoot)
	runBuildCommand(t, buildRoot, "git", "init", "-q")
	runBuildCommand(t, buildRoot, "git", "config", "user.name", "Build Identity Test")
	runBuildCommand(t, buildRoot, "git", "config", "user.email", "build-identity@example.invalid")
	runBuildCommand(t, buildRoot, "git", "add", ".")
	runBuildCommand(t, buildRoot, "git", "commit", "-qm", "stale source")
	staleRevision := strings.TrimSpace(runBuildCommand(t, buildRoot, "git", "rev-parse", "HEAD"))

	staleBin := t.TempDir()
	buildRevisionlessPair(t, buildRoot, staleBin)
	builtStaleCLI := filepath.Join(staleBin, "agent-team")
	builtStaleCLIInfo, err := os.Stat(builtStaleCLI)
	if err != nil {
		t.Fatal(err)
	}
	staleCLI := filepath.Join(t.TempDir(), "agent-team")
	copyBuildFile(t, builtStaleCLI, staleCLI, builtStaleCLIInfo.Mode())
	staleBefore, err := ReadFile(staleCLI)
	if err != nil {
		t.Fatal(err)
	}

	buildinfoPath := filepath.Join(buildRoot, "internal", "buildinfo", "buildinfo.go")
	body, err := os.ReadFile(buildinfoPath)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `var LinkedSourceIdentity = "unbound"`, `var LinkedSourceIdentity = "unbound-current-control"`, 1))
	if err := os.WriteFile(buildinfoPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	runBuildCommand(t, buildRoot, "git", "add", "internal/buildinfo/buildinfo.go")
	runBuildCommand(t, buildRoot, "git", "commit", "-qm", "current source")
	currentRevision := strings.TrimSpace(runBuildCommand(t, buildRoot, "git", "rev-parse", "HEAD"))
	if currentRevision == staleRevision {
		t.Fatal("checkout advance did not change revision")
	}

	currentBin := t.TempDir()
	currentCLI := filepath.Join(currentBin, "agent-team")
	currentDaemon := filepath.Join(currentBin, "agent-teamd")
	forgedMarker := linkedSourcePrefix + strings.Repeat("0", 40) + linkedSourceSuffix
	buildRevisionlessPair(t, buildRoot, currentBin,
		"AGENT_TEAM_EXTRA_LDFLAGS=-X github.com/agent-team-project/agent-team/internal/buildinfo.LinkedSourceIdentity="+forgedMarker,
	)
	unboundBin := t.TempDir()
	unboundCLI := filepath.Join(unboundBin, "renamed-cli")
	unboundDaemon := filepath.Join(unboundBin, "daemon-x")
	buildRevisionless(t, buildRoot, unboundCLI, "./cmd/agent-team", currentRevision, false)
	buildRevisionless(t, buildRoot, unboundDaemon, "./cmd/agent-teamd", currentRevision, false)

	staleAfter, err := ReadFile(staleCLI)
	if err != nil {
		t.Fatal(err)
	}
	currentCLIInfo, err := ReadFile(currentCLI)
	if err != nil {
		t.Fatal(err)
	}
	currentDaemonInfo, err := ReadFile(currentDaemon)
	if err != nil {
		t.Fatal(err)
	}
	unboundInfo, err := ReadFile(unboundCLI)
	if err != nil {
		t.Fatal(err)
	}
	if staleBefore.SourceID != staleAfter.SourceID || staleAfter.SourceID != "git:"+staleRevision {
		t.Fatalf("stale executable identity moved with checkout: before=%+v after=%+v", staleBefore, staleAfter)
	}
	if comparison := Compare(staleAfter, currentDaemonInfo); !comparison.Comparable || comparison.Equal {
		t.Fatalf("stale CLI impersonated current daemon: %+v", comparison)
	}
	if comparison := Compare(currentCLIInfo, currentDaemonInfo); !comparison.Comparable || !comparison.Equal {
		t.Fatalf("current siblings are not coherent: %+v", comparison)
	}
	if want := "git:" + currentRevision; currentCLIInfo.SourceID != want || currentDaemonInfo.SourceID != want {
		t.Fatalf("supported build script accepted a forged source override: cli=%+v daemon=%+v want=%s", currentCLIInfo, currentDaemonInfo, want)
	}
	if comparison := Compare(unboundInfo, currentDaemonInfo); comparison.Comparable {
		t.Fatalf("bypassed binding remained activation-capable: info=%+v comparison=%+v", unboundInfo, comparison)
	}

	consumer := filepath.Join(t.TempDir(), "consumer")
	if err := os.MkdirAll(consumer, 0o755); err != nil {
		t.Fatal(err)
	}
	runBuildCommandEnv(t, consumer, currentBin, currentCLI, "init", "--minimal", "--set", "pm.provider=none", "--no-input", "--target", consumer)
	blocked := runBuildCommandWantError(t, consumer, currentBin, unboundCLI,
		"run", "worker", "--target", consumer, "--no-daemon", "--runtime", "codex", "--runtime-bin", "/usr/bin/printf")
	if !strings.Contains(blocked, "activation needed") {
		t.Fatalf("unbound direct launch rejection = %s", blocked)
	}
	if strings.Contains(blocked, "exec") {
		t.Fatalf("renamed unbound CLI reached the runtime before rejection: %s", blocked)
	}
	unboundCLIInfo, err := os.Stat(unboundCLI)
	if err != nil {
		t.Fatal(err)
	}
	copyBuildFile(t, unboundCLI, filepath.Join(unboundBin, "agent-team"), unboundCLIInfo.Mode())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	blocked, err = runBuildCommandResultContext(ctx, consumer, currentBin, unboundDaemon, "--repo", consumer)
	if err == nil {
		t.Fatalf("renamed unbound daemon unexpectedly stayed activation-capable: %s", blocked)
	}
	if !strings.Contains(blocked, "activation needed") {
		t.Fatalf("renamed unbound daemon rejection = %s", blocked)
	}
	if _, err := os.Stat(filepath.Join(consumer, ".agent_team", "daemon")); !os.IsNotExist(err) {
		t.Fatalf("renamed unbound daemon created state before rejection: %v", err)
	}
	blocked = runBuildCommandWantError(t, consumer, currentBin, staleCLI, "--repo", consumer, "daemon", "start", "--json")
	if !strings.Contains(blocked, "activation needed") {
		t.Fatalf("stale CLI rejection = %s", blocked)
	}
	if _, err := os.Stat(filepath.Join(consumer, ".agent_team", "daemon")); !os.IsNotExist(err) {
		t.Fatalf("stale CLI created daemon state before rejection: %v", err)
	}
	runBuildCommandEnv(t, consumer, currentBin, currentCLI, "--repo", consumer, "daemon", "start", "--json")
	blocked = runBuildCommandWantError(t, consumer, currentBin, staleCLI, "--repo", consumer, "daemon", "reconcile")
	if !strings.Contains(blocked, "activation needed") {
		t.Fatalf("stale HTTP mutation rejection = %s", blocked)
	}
	runBuildCommandEnv(t, consumer, currentBin, currentCLI, "--repo", consumer, "daemon", "stop", "--json")

	renamedDaemon := filepath.Join(currentBin, "renamed-daemon")
	currentDaemonInfoFile, err := os.Stat(currentDaemon)
	if err != nil {
		t.Fatal(err)
	}
	copyBuildFile(t, currentDaemon, renamedDaemon, currentDaemonInfoFile.Mode())
	var daemonOutput bytes.Buffer
	daemonCmd := exec.Command(renamedDaemon, "--repo", consumer)
	daemonCmd.Dir = consumer
	daemonCmd.Env = buildContractEnv(currentBin)
	daemonCmd.Stdout = &daemonOutput
	daemonCmd.Stderr = &daemonOutput
	if err := daemonCmd.Start(); err != nil {
		t.Fatal(err)
	}
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- daemonCmd.Wait() }()
	daemonRunning := true
	t.Cleanup(func() {
		if daemonRunning {
			_ = daemonCmd.Process.Kill()
			<-daemonDone
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		blocked, err = runBuildCommandResult(consumer, currentBin, unboundCLI, "--repo", consumer, "daemon", "reconcile")
		if err == nil {
			t.Fatalf("renamed real daemon accepted mutation from renamed unbound CLI: %s", blocked)
		}
		if strings.Contains(blocked, "activation needed") {
			break
		}
		select {
		case daemonErr := <-daemonDone:
			daemonRunning = false
			t.Fatalf("renamed coherent daemon exited before serving: %v\n%s", daemonErr, daemonOutput.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("renamed coherent daemon did not become ready; last client output:\n%s", blocked)
		}
		time.Sleep(50 * time.Millisecond)
	}
	runBuildCommandEnv(t, consumer, currentBin, currentCLI, "--repo", consumer, "daemon", "stop", "--json")
	select {
	case daemonErr := <-daemonDone:
		daemonRunning = false
		if daemonErr != nil {
			t.Fatalf("renamed coherent daemon stop: %v\n%s", daemonErr, daemonOutput.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("renamed coherent daemon did not stop")
	}
}

func buildRevisionlessPair(t *testing.T, root, outputDir string, extraEnv ...string) {
	t.Helper()
	overrides := append([]string{"GOFLAGS=-buildvcs=false"}, extraEnv...)
	runBuildCommandWithEnv(t, root, overrides, filepath.Join(root, "scripts", "build.sh"), outputDir)
}

func buildRevisionless(t *testing.T, root, output, target, revision string, bind bool) {
	t.Helper()
	args := []string{"build", "-buildvcs=false"}
	if bind {
		marker := linkedSourcePrefix + revision + linkedSourceSuffix
		args = append(args, "-ldflags", "-X github.com/agent-team-project/agent-team/internal/buildinfo.LinkedSourceIdentity="+marker)
	}
	args = append(args, "-o", output, target)
	runBuildCommand(t, root, "go", args...)
}

func copyBuildSource(t *testing.T, sourceRoot, targetRoot string) {
	t.Helper()
	for _, rel := range []string{"cmd", "internal", "template", "scripts/build.sh", "embed.go", "go.mod", "go.sum"} {
		source := filepath.Join(sourceRoot, rel)
		target := filepath.Join(targetRoot, rel)
		info, err := os.Lstat(source)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			copyBuildFile(t, source, target, info.Mode())
			continue
		}
		if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relPath, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			destination := filepath.Join(target, relPath)
			if entry.IsDir() {
				return os.MkdirAll(destination, 0o755)
			}
			entryInfo, err := entry.Info()
			if err != nil {
				return err
			}
			copyBuildFile(t, path, destination, entryInfo.Mode())
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func copyBuildFile(t *testing.T, source, target string, mode fs.FileMode) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, body, mode.Perm()); err != nil {
		t.Fatal(err)
	}
}

func runBuildCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func runBuildCommandWithEnv(t *testing.T, dir string, overrides []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = environmentWithOverrides(overrides)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func runBuildCommandEnv(t *testing.T, dir, pathDir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = buildContractEnv(pathDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func runBuildCommandWantError(t *testing.T, dir, pathDir, name string, args ...string) string {
	t.Helper()
	out, err := runBuildCommandResult(dir, pathDir, name, args...)
	if err == nil {
		t.Fatalf("%s %s unexpectedly succeeded\n%s", name, strings.Join(args, " "), out)
	}
	return out
}

func runBuildCommandResult(dir, pathDir, name string, args ...string) (string, error) {
	return runBuildCommandResultContext(context.Background(), dir, pathDir, name, args...)
}

func runBuildCommandResultContext(ctx context.Context, dir, pathDir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = buildContractEnv(pathDir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func buildContractEnv(pathDir string) []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "AGENT_TEAM_") || strings.HasPrefix(entry, "PATH=") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "PATH="+pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func environmentWithOverrides(overrides []string) []string {
	keys := make(map[string]bool, len(overrides))
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		keys[key] = true
	}
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !keys[key] {
			out = append(out, entry)
		}
	}
	return append(out, overrides...)
}
