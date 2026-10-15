package daemon

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// repoOf finds the repository cwd belongs to: its main worktree's root, and
// the linked worktree's folder name when cwd is in one. Both are nil
// outside git.
func repoOf(cwd string) (repo, worktree *string) {
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--path-format=absolute",
		"--show-toplevel", "--git-common-dir").Output()
	if err != nil {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return nil, nil
	}
	top, common := filepath.Clean(lines[0]), filepath.Clean(lines[1])
	main := top
	if filepath.Base(common) == ".git" {
		main = filepath.Dir(common)
	}
	if main == top {
		return &main, nil
	}
	name := filepath.Base(top)
	return &main, &name
}
