package gitstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseStatus(t *testing.T) {
	out := `# branch.oid 1234567890abcdef
# branch.head feature/x
# branch.upstream origin/feature/x
# branch.ab +2 -1
1 .M N... 100644 100644 100644 aaa bbb internal/a b.go
1 A. N... 000000 100644 100644 000 ccc new.go
2 R. N... 100644 100644 100644 ddd eee R100 moved.go	old.go
u UU N... 100644 100644 100644 100644 f1 f2 f3 conflict.go
? notes.txt
`
	var st Status
	parseStatus(out, &st)
	if st.Branch != "feature/x" || st.Upstream != "origin/feature/x" || st.Ahead != 2 || st.Behind != 1 || st.Head == "" {
		t.Fatalf("branch info: %+v", st)
	}
	if len(st.Files) != 5 {
		t.Fatalf("files: %+v", st.Files)
	}
	if st.Files[0].Path != "internal/a b.go" || st.Files[0].Code() != 'M' {
		t.Fatalf("path with space: %+v", st.Files[0])
	}
	if st.Files[2].Path != "moved.go" || st.Files[2].Orig != "old.go" || st.Files[2].Code() != 'R' {
		t.Fatalf("rename: %+v", st.Files[2])
	}
	if !st.Files[3].Conflicted() || st.Files[3].Code() != 'U' {
		t.Fatalf("conflict: %+v", st.Files[3])
	}
	staged, modified, untracked, conflicts := st.Counts()
	if staged != 2 || modified != 1 || untracked != 1 || conflicts != 1 {
		t.Fatalf("counts %d %d %d %d", staged, modified, untracked, conflicts)
	}

	applyNumstat("3\t1\tinternal/a b.go\n-\t-\tnew.go\n", &st)
	if st.Files[0].Added != 3 || st.Files[0].Removed != 1 || !st.Files[1].Binary {
		t.Fatalf("numstat: %+v %+v", st.Files[0], st.Files[1])
	}
}

func TestReadRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Not a repo yet.
	if st, err := Read(context.Background(), dir); err != nil || st.IsRepo {
		t.Fatalf("plain dir: %+v %v", st, err)
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	run("add", "a.txt")
	run("commit", "-qm", "first commit")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644)

	st, err := Read(context.Background(), dir)
	if err != nil || !st.IsRepo || st.Branch != "main" {
		t.Fatalf("read: %+v %v", st, err)
	}
	if st.LastCommit == "" || st.LastWhen.IsZero() {
		t.Fatalf("last commit missing: %+v", st)
	}
	var a, b *File
	for i := range st.Files {
		switch st.Files[i].Path {
		case "a.txt":
			a = &st.Files[i]
		case "b.txt":
			b = &st.Files[i]
		}
	}
	if a == nil || a.Code() != 'M' || a.Added != 2 || a.Removed != 0 {
		t.Fatalf("a.txt: %+v", a)
	}
	if b == nil || !b.Untracked() {
		t.Fatalf("b.txt: %+v", b)
	}
}
