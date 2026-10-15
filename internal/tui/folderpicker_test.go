package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

func TestFolderPickerListsLiveThenRecent(t *testing.T) {
	p := newFolderPicker([]string{"/x/live"})
	p.setRecent([]string{"/x/recent", "/x/live"})
	if len(p.shown) != 2 || p.shown[0] != "/x/live" || p.shown[1] != "/x/recent" {
		t.Fatalf("shown %v", p.shown)
	}
	p.filter.SetValue("rec")
	p.applyFilter()
	if len(p.shown) != 1 || p.choice() != "/x/recent" {
		t.Fatalf("filtered %v choice %q", p.shown, p.choice())
	}
}

func TestFolderPickerTypedPath(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "project-one"), 0o755)
	p := newFolderPicker(nil)
	p.filter.SetValue(filepath.Join(dir, "project-one"))
	p.applyFilter()
	if p.choice() != filepath.Join(dir, "project-one") {
		t.Fatalf("typed path choice %q", p.choice())
	}
	if got := completeDir(filepath.Join(dir, "proj")); got != filepath.Join(dir, "project-one")+"/" {
		t.Fatalf("complete: %q", got)
	}
}

func TestNewSessionStartsInChosenFolder(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	dir := t.TempDir()
	m = run(t, m, m.newSessionIn(dir))
	if len(d.started) != 1 || d.started[0].CWD != dir || m.opts.CWD != dir || m.sessionView.id != "s_new1" {
		t.Fatalf("started %+v focused %q in %q", d.started, m.sessionView.id, m.opts.CWD)
	}
}
