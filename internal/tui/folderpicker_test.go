package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

func TestFolderPickerSelectionStaysInRange(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle))
	m := daemonModel(t, d)
	m.openFolders()
	m.folders.filter.SetValue("zzz-no-match")
	m.folders.applyFilter()
	m.handleFoldersKey(tea.KeyMsg{Type: tea.KeyDown})
	m.folders.filter.SetValue("/x/a")
	m.folders.applyFilter()
	if got := m.folders.choice(); got != "/x/a" {
		t.Fatalf("choice %q", got)
	}
}

func TestCompleteDirTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	if got := completeDir(dir + "/"); got != filepath.Join(dir, "sub")+"/" {
		t.Fatalf("abs: %q", got)
	}
	t.Setenv("HOME", dir)
	if got := completeDir("~/"); got != filepath.Join(dir, "sub")+"/" {
		t.Fatalf("home: %q", got)
	}
	two := t.TempDir()
	os.Mkdir(filepath.Join(two, "alpha"), 0o755)
	os.Mkdir(filepath.Join(two, "beta"), 0o755)
	if got := completeDir(two + "/"); got != two+"/" {
		t.Fatalf("no common prefix: %q", got)
	}
}

// With the daemon, /new and the palette's one "New session…" open the
// picker on the focused session's folder, so enter starts one right there.
func TestNewOpensPickerOnCurrentFolder(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	m.opts.CWD = "/x/b"
	m.slash("/new")
	if m.folders == nil || m.folders.choice() != "/x/b" {
		t.Fatalf("picker %v", m.folders)
	}
	news := 0
	for _, a := range m.actions() {
		if strings.HasPrefix(a.label, "New session") {
			news++
		}
	}
	if news != 1 {
		t.Fatalf("%d New session entries", news)
	}
	m = run(t, m, m.handleFoldersKey(tea.KeyMsg{Type: tea.KeyEnter}))
	if len(d.started) != 1 || d.started[0].CWD != "/x/b" {
		t.Fatalf("started %+v", d.started)
	}
}
