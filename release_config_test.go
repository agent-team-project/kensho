package agentteam

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoReleaserVersionWiring(t *testing.T) {
	t.Parallel()

	configBytes, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	config := string(configBytes)

	for _, tc := range []struct {
		id           string
		linkerTarget string
	}{
		{id: "agent-team", linkerTarget: "github.com/agent-team-project/agent-team/internal/cli.Version"},
		{id: "agent-teamd", linkerTarget: "main.Version"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			block := goReleaserBuildBlock(t, config, tc.id)
			mainPackage := goReleaserBuildField(t, block, "main")
			binary := goReleaserBuildField(t, block, "binary")
			ldflag := goReleaserVersionLDFlag(t, block)

			const version = "9.8.7-release-wiring-test"
			wantLDFlag := "-X " + tc.linkerTarget + "={{.Version}}"
			if ldflag != wantLDFlag {
				t.Fatalf("release version ldflag = %q, want %q", ldflag, wantLDFlag)
			}

			outputPath := filepath.Join(t.TempDir(), binary)
			build := exec.Command(
				"go", "build",
				"-ldflags", strings.ReplaceAll(ldflag, "{{.Version}}", version),
				"-o", outputPath,
				mainPackage,
			)
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build %s with configured release ldflag: %v\n%s", tc.id, err, output)
			}

			versionOutput, err := exec.Command(outputPath, "--version").CombinedOutput()
			if err != nil {
				t.Fatalf("run %s --version: %v\n%s", binary, err, versionOutput)
			}
			if want := binary + " " + version; !strings.HasPrefix(string(versionOutput), want) {
				t.Fatalf("%s --version = %q, want prefix %q", binary, versionOutput, want)
			}
		})
	}
}

func goReleaserBuildBlock(t *testing.T, config, id string) string {
	t.Helper()

	marker := "  - id: " + id + "\n"
	start := strings.Index(config, marker)
	if start < 0 {
		t.Fatalf("GoReleaser build %q not found", id)
	}
	rest := config[start+len(marker):]
	end := len(rest)
	for _, next := range []string{"\n  - id: ", "\narchives:"} {
		if i := strings.Index(rest, next); i >= 0 && i < end {
			end = i
		}
	}
	return rest[:end]
}

func goReleaserBuildField(t *testing.T, block, name string) string {
	t.Helper()

	prefix := name + ":"
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	t.Fatalf("GoReleaser build field %q not found", name)
	return ""
}

func goReleaserVersionLDFlag(t *testing.T, block string) string {
	t.Helper()

	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- -X ") && strings.Contains(line, "={{.Version}}") {
			return strings.TrimPrefix(line, "- ")
		}
	}
	t.Fatal("GoReleaser release version ldflag not found")
	return ""
}
