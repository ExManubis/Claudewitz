//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListenIsPrivate(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "cwl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	dir := filepath.Join(base, "d")
	if err := os.Mkdir(dir, 0o755); err != nil { // an existing, too-open folder
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(sock); fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v", fi.Mode().Perm())
	}

	// A symlink in place of the folder is refused.
	link := filepath.Join(base, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(filepath.Join(link, "s2")); err == nil {
		t.Error("listened in a symlinked folder")
	}
}
