package worktreecleanup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agent-team-project/agent-team/internal/job"
)

var ErrLiveProcessReference = errors.New("worktree has live process references")

// LiveProcessReferenceCheck is replaceable in tests.
var LiveProcessReferenceCheck = defaultLiveProcessReferenceCheck

type Options struct {
	ForceBranch bool
}

func CleanupJobOwnedWorktree(repoRoot string, j *job.Job, opts Options) (string, error) {
	if j == nil {
		return "", fmt.Errorf("job is required")
	}
	if strings.TrimSpace(j.Worktree) == "" && strings.TrimSpace(j.Branch) == "" {
		return "nothing to clean", nil
	}
	removed := make([]string, 0, 2)
	if strings.TrimSpace(j.Worktree) != "" {
		if err := ValidateJobOwnedWorktree(repoRoot, j.Worktree); err != nil {
			return "", err
		}
		if _, err := os.Stat(j.Worktree); err == nil {
			live, err := LiveProcessReferenceCheck(j.Worktree)
			if err != nil {
				return "", err
			}
			if live {
				return "", fmt.Errorf("%w: %s", ErrLiveProcessReference, j.Worktree)
			}
			if out, err := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", j.Worktree).CombinedOutput(); err != nil {
				return "", fmt.Errorf("remove worktree %s: %w: %s", j.Worktree, err, strings.TrimSpace(string(out)))
			}
			removed = append(removed, "worktree")
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if strings.TrimSpace(j.Branch) != "" {
		exists, err := GitBranchExists(repoRoot, j.Branch)
		if err != nil {
			return "", err
		}
		if exists {
			deleteFlag := "-d"
			if opts.ForceBranch {
				deleteFlag = "-D"
			}
			if out, err := exec.Command("git", "-C", repoRoot, "branch", deleteFlag, j.Branch).CombinedOutput(); err != nil {
				return "", fmt.Errorf("remove branch %s: %w: %s", j.Branch, err, strings.TrimSpace(string(out)))
			}
			removed = append(removed, removedBranchSummary(opts.ForceBranch))
		}
	}
	if len(removed) == 0 {
		return "nothing to clean", nil
	}
	return "removed " + strings.Join(removed, " and "), nil
}

func ValidateJobOwnedWorktree(repoRoot, worktreePath string) error {
	rawRoot, err := filepath.Abs(filepath.Join(repoRoot, ".claude", "worktrees"))
	if err != nil {
		return err
	}
	rawPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return err
	}
	if pathInsideDir(rawRoot, rawPath) {
		return nil
	}
	root := resolvePathWithExistingPrefix(rawRoot)
	path := resolvePathWithExistingPrefix(rawPath)
	if pathInsideDir(root, path) {
		return nil
	}
	return fmt.Errorf("refusing to remove worktree outside %s: %s", root, path)
}

func GitBranchExists(repoRoot, branch string) (bool, error) {
	out, err := exec.Command("git", "-C", repoRoot, "branch", "--list", branch, "--format", "%(refname:short)").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("list branch %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == branch {
			return true, nil
		}
	}
	return false, nil
}

func defaultLiveProcessReferenceCheck(path string) (bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, nil
	}
	if ok, err := procLiveProcessReference(path); err == nil {
		return ok, nil
	}
	return lsofLiveProcessReference(path)
}

func procLiveProcessReference(path string) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	root = resolvePathWithExistingPrefix(root)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		procDir := filepath.Join("/proc", entry.Name())
		for _, link := range []string{"cwd", "exe"} {
			if processLinkReferences(filepath.Join(procDir, link), root) {
				return true, nil
			}
		}
		fdDir := filepath.Join(procDir, "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
				continue
			}
			continue
		}
		for _, fd := range fds {
			if processLinkReferences(filepath.Join(fdDir, fd.Name()), root) {
				return true, nil
			}
		}
	}
	return false, nil
}

func processLinkReferences(link, root string) bool {
	target, err := os.Readlink(link)
	if err != nil {
		return false
	}
	target = strings.TrimSuffix(target, " (deleted)")
	if !filepath.IsAbs(target) {
		return false
	}
	target = resolvePathWithExistingPrefix(target)
	return pathInsideOrEqual(root, target)
}

func lsofLiveProcessReference(path string) (bool, error) {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return false, nil
	}
	out, err := exec.Command(lsof, "-t", "+D", path).CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out)) != "", nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(string(out)) == "" {
		return false, nil
	}
	return false, fmt.Errorf("check live worktree references with lsof: %w: %s", err, strings.TrimSpace(string(out)))
}

func removedBranchSummary(force bool) string {
	if force {
		return "branch (force)"
	}
	return "branch"
}

func resolvePathWithExistingPrefix(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	missing := []string{}
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathInsideDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func pathInsideOrEqual(root, path string) bool {
	if root == path {
		return true
	}
	return pathInsideDir(root, path)
}
