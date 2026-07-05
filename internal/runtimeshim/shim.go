package runtimeshim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const BinDirName = "bin"

type Spec struct {
	Command string
	Skill   string
	Script  string
}

var DefaultSpecs = []Spec{
	{Command: "inbox", Skill: "inbox", Script: filepath.Join("scripts", "inbox.sh")},
	{Command: "channel.sh", Skill: "channel", Script: filepath.Join("scripts", "channel.sh")},
}

func Install(root string, skillPaths map[string]string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("runtime shim root is required")
	}
	binDir := filepath.Join(root, BinDirName)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("create runtime shim bin: %w", err)
	}
	for _, spec := range DefaultSpecs {
		skillDir := strings.TrimSpace(skillPaths[spec.Skill])
		if skillDir == "" {
			continue
		}
		target := filepath.Join(skillDir, spec.Script)
		if st, err := os.Stat(target); err != nil {
			return "", fmt.Errorf("runtime shim %s target: %w", spec.Command, err)
		} else if st.IsDir() {
			return "", fmt.Errorf("runtime shim %s target is a directory: %s", spec.Command, target)
		}
		link := filepath.Join(binDir, spec.Command)
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("replace runtime shim %s: %w", spec.Command, err)
		}
		body := "#!/bin/sh\nexec " + shellQuote(target) + " \"$@\"\n"
		if err := os.WriteFile(link, []byte(body), 0o755); err != nil {
			return "", fmt.Errorf("create runtime shim %s: %w", spec.Command, err)
		}
	}
	return binDir, nil
}

func PrependPath(env []string, dir string) []string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return append([]string(nil), env...)
	}
	out := append([]string(nil), env...)
	key := "PATH="
	for i, entry := range out {
		if strings.HasPrefix(entry, key) {
			current := strings.TrimPrefix(entry, key)
			if current == "" {
				out[i] = key + dir
			} else {
				out[i] = key + dir + string(os.PathListSeparator) + current
			}
			return out
		}
	}
	if current := os.Getenv("PATH"); current != "" {
		return append(out, key+dir+string(os.PathListSeparator)+current)
	}
	return append(out, key+dir)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
