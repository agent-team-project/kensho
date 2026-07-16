package runtimeshim

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agent-team-project/agent-team/internal/buildinfo"
)

const (
	AttestationSchema = "agent-team.shim-attestation.v1"

	ComparisonCoherent                = "coherent"
	ComparisonMismatch                = "mismatch"
	ComparisonMissingCLIProvenance    = "missing_cli_provenance"
	ComparisonMissingDaemonProvenance = "missing_daemon_provenance"
	ComparisonIncomparable            = "incomparable"
	ComparisonNotChecked              = "not_checked"

	attestationMarker = "# agent-team-shim-attestation-v1 "
)

// Attestation is immutable launch-time evidence baked into a generated shim.
// It is readable without asking Cobra to resolve (and potentially deny) the
// command whose provenance is being diagnosed.
type Attestation struct {
	Schema           string         `json:"schema"`
	Kind             string         `json:"kind"`
	Target           string         `json:"target"`
	CLI              buildinfo.Info `json:"cli"`
	Daemon           buildinfo.Info `json:"daemon,omitempty"`
	CLIHeader        string         `json:"cli_header,omitempty"`
	DaemonHeader     string         `json:"daemon_header,omitempty"`
	DaemonComparison string         `json:"daemon_comparison"`
	Reason           string         `json:"reason,omitempty"`
	Assets           string         `json:"assets,omitempty"`
	Skills           string         `json:"skills"`
}

func newAttestation(target string, cli, daemon buildinfo.Info, assets, skills string) Attestation {
	comparison, reason := attestationComparison(cli, daemon)
	return Attestation{
		Schema:           AttestationSchema,
		Kind:             "generated_shim",
		Target:           filepath.Clean(target),
		CLI:              cli,
		Daemon:           daemon,
		CLIHeader:        cli.HeaderValue(),
		DaemonHeader:     daemon.HeaderValue(),
		DaemonComparison: comparison,
		Reason:           reason,
		Assets:           strings.TrimSpace(assets),
		Skills:           strings.TrimSpace(skills),
	}
}

func attestationComparison(cli, daemon buildinfo.Info) (string, string) {
	if daemon.Empty() {
		comparison := buildinfo.Compare(cli, cli)
		if !comparison.Comparable {
			return ComparisonMissingCLIProvenance, comparison.Reason
		}
		return ComparisonNotChecked, "daemon build was not available at shim generation"
	}
	cliSelf := buildinfo.Compare(cli, cli)
	if !cliSelf.Comparable {
		return ComparisonMissingCLIProvenance, cliSelf.Reason
	}
	daemonSelf := buildinfo.Compare(daemon, daemon)
	if !daemonSelf.Comparable {
		return ComparisonMissingDaemonProvenance, daemonSelf.Reason
	}
	comparison := buildinfo.Compare(cli, daemon)
	if !comparison.Comparable {
		return ComparisonIncomparable, comparison.Reason
	}
	if !comparison.Equal {
		return ComparisonMismatch, fmt.Sprintf("CLI %s does not match daemon %s", cli.Display(), daemon.Display())
	}
	return ComparisonCoherent, ""
}

// CheckActive verifies that the live shim still belongs to the active daemon,
// managed CLI, activation assets, and registered skill bundle.
func (a Attestation) CheckActive(daemon, cli buildinfo.Info, assets, skills string) error {
	if a.Schema != AttestationSchema || a.Kind != "generated_shim" {
		return fmt.Errorf("unsupported generated shim attestation %q", a.Schema)
	}
	if a.DaemonComparison != ComparisonCoherent {
		reason := strings.TrimSpace(a.Reason)
		if reason == "" {
			reason = "generated shim is not daemon-comparable"
		}
		return errors.New(reason)
	}
	for name, pair := range map[string]struct {
		got  buildinfo.Info
		want buildinfo.Info
	}{
		"CLI":    {a.CLI, cli},
		"daemon": {a.Daemon, daemon},
	} {
		comparison := buildinfo.Compare(pair.got, pair.want)
		if !comparison.Comparable {
			return fmt.Errorf("generated shim %s provenance is not comparable: %s", name, comparison.Reason)
		}
		if !comparison.Equal {
			return fmt.Errorf("generated shim %s %s differs from active %s", name, pair.got.Display(), pair.want.Display())
		}
	}
	if strings.TrimSpace(a.Assets) == "" {
		return errors.New("generated shim has no activation asset fingerprint")
	}
	if a.Assets != strings.TrimSpace(assets) {
		return errors.New("generated shim activation assets differ from the active tuple")
	}
	if strings.TrimSpace(a.Skills) == "" {
		return errors.New("generated shim has no skill asset fingerprint")
	}
	if a.Skills != strings.TrimSpace(skills) {
		return errors.New("generated shim skill assets differ from the running instance bundle")
	}
	return nil
}

func encodeAttestation(attestation Attestation) (marker, body string, err error) {
	raw, err := json.Marshal(attestation)
	if err != nil {
		return "", "", err
	}
	return attestationMarker + base64.RawStdEncoding.EncodeToString(raw), string(raw), nil
}

// ReadAttestation reads the immutable marker from a generated shim without
// executing either the shim or its target CLI.
func ReadAttestation(path string) (Attestation, error) {
	f, err := os.Open(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return Attestation{}, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for line := 0; line < 8 && scanner.Scan(); line++ {
		text := scanner.Text()
		if !strings.HasPrefix(text, attestationMarker) {
			continue
		}
		raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(text, attestationMarker))
		if err != nil {
			return Attestation{}, fmt.Errorf("decode generated shim attestation: %w", err)
		}
		var out Attestation
		if err := json.Unmarshal(raw, &out); err != nil {
			return Attestation{}, fmt.Errorf("parse generated shim attestation: %w", err)
		}
		if out.Schema != AttestationSchema {
			return Attestation{}, fmt.Errorf("unsupported generated shim attestation %q", out.Schema)
		}
		return out, nil
	}
	if err := scanner.Err(); err != nil {
		return Attestation{}, err
	}
	return Attestation{}, errors.New("generated shim has no build attestation")
}

func isGeneratedShim(path string) bool {
	if _, err := ReadAttestation(path); err == nil {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for line := 0; line < 16 && scanner.Scan(); line++ {
		text := scanner.Text()
		if strings.Contains(text, "Closed-world enforcement baked in at install time") || strings.HasPrefix(text, "REAL_AGENT_TEAM=") {
			return true
		}
	}
	return false
}

// SkillAssetsDigest fingerprints the exact named skill directories registered
// for a launch. Paths and file contents are both included; symlink locations
// are resolved before walking.
func SkillAssetsDigest(skillPaths map[string]string) (string, error) {
	names := make([]string, 0, len(skillPaths))
	for name := range skillPaths {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		root := strings.TrimSpace(skillPaths[name])
		if root == "" {
			return "", fmt.Errorf("skill %s path is empty", name)
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", fmt.Errorf("resolve skill %s: %w", name, err)
		}
		if err := hashSkillTree(hash, name, resolved); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// SkillAssetsDigestRoot fingerprints the skill symlinks actually registered
// under one runtime's .claude/skills directory.
func SkillAssetsDigestRoot(root string) (string, error) {
	entries, err := os.ReadDir(filepath.Clean(strings.TrimSpace(root)))
	if err != nil {
		return "", err
	}
	paths := make(map[string]string, len(entries))
	for _, entry := range entries {
		paths[entry.Name()] = filepath.Join(root, entry.Name())
	}
	return SkillAssetsDigest(paths)
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func hashSkillTree(hash hashWriter, name, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		logical := filepath.ToSlash(filepath.Join(name, rel))
		fmt.Fprintf(hash, "%d:%s\n%d:", len(logical), logical, len(body))
		_, _ = hash.Write(body)
		_, _ = hash.Write([]byte{'\n'})
		return nil
	})
}
