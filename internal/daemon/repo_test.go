package daemon

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRepoOf(t *testing.T) {
	if r, w := repoOf(t.TempDir()); r != nil || w != nil {
		t.Fatalf("not a repo: %v %v", r, w)
	}

	main, _ := filepath.EvalSymlinks(t.TempDir())
	git(t, main, "init", "-q")
	git(t, main, "commit", "-q", "--allow-empty", "-m", "first")
	git(t, main, "worktree", "add", "-q", filepath.Join(main, ".claude", "worktrees", "ring-tests"))

	r, w := repoOf(main)
	if r == nil || *r != main || w != nil {
		t.Fatalf("main worktree: %v %v", r, w)
	}
	r, w = repoOf(filepath.Join(main, ".claude", "worktrees", "ring-tests"))
	if r == nil || *r != main || w == nil || *w != "ring-tests" {
		t.Fatalf("linked worktree: %v %v", r, w)
	}
}
