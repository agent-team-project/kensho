package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV05CompatibilitySurfacesStayRemoved(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range []string{
		"internal/linearwriteback/linearwriteback.go",
		"internal/linearwriteback/linearwriteback_test.go",
		"internal/cli/shortcuts.go",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Errorf("removed compatibility surface still exists: %s", rel)
		}
	}

	forbidden := []string{
		"Install" + "WithOptions",
		"WithAuthority" + "Allowlist",
		"AGENT_TEAM_AUTHORITY_" + "ALLOWLIST",
		"ParseFrontmatter" + "Rich",
		"parseYAML" + "Subset",
		"AgentLoad" + "Error",
		"ErrAgent" + "Load",
		"legacyRepo" + "TargetFlagHelp",
		"team." + "pm_tool",
	}
	for _, rel := range []string{"cmd", "internal", "scripts", "template", ".agent_team"} {
		base := filepath.Join(root, rel)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, token := range forbidden {
				if strings.Contains(string(body), token) {
					t.Errorf("removed compatibility token %q remains in %s", token, path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	entries, err := filepath.Glob(filepath.Join(root, "internal", "cli", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "Aliases:") {
			t.Errorf("Cobra command alias remains in %s", path)
		}
	}
}
