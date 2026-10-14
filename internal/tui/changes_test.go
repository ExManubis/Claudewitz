package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/gitstatus"
)

// changesRepo makes a repository with one modified and one new file and a
// model sized w×h looking at it.
func changesRepo(t *testing.T, w, h int) Model {
	t.Helper()
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
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	run("add", ".")
	run("commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"changed line\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("brand new file\n"), 0o644)

	m := New(context.Background(), claude.Options{CWD: dir}, config.Config{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = next.(Model)
	st, err := gitstatus.Read(context.Background(), dir)
	if err != nil || len(st.Files) != 2 {
		t.Fatalf("status: %+v %v", st, err)
	}
	m.git, m.gitLoaded = st, true
	return m
}

// run executes cmd and feeds its message back, as Bubble Tea would.
func feed(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

func TestChangesViewerShowsDiffsAndSwitchesFiles(t *testing.T) {
	m := changesRepo(t, 140, 40)
	m = feed(t, m, m.openChanges("main.go"))
	if m.changes == nil {
		t.Fatal("viewer didn't open")
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, `println("changed line")`) || !strings.Contains(v, "notes.txt") || !strings.Contains(v, "1/2") {
		t.Fatalf("viewer:\n%s", v)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = feed(t, next.(Model), cmd)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "brand new file") || !strings.Contains(v, "2/2") {
		t.Fatalf("after tab:\n%s", v)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m = next.(Model); m.changes != nil {
		t.Fatal("esc should close the viewer")
	}
}

func TestChangesViewerNarrowHidesList(t *testing.T) {
	m := changesRepo(t, 80, 30)
	m = feed(t, m, m.openChanges(""))
	if _, _, listW, _, _ := m.changesLayout(); listW != 0 {
		t.Fatalf("list width %d at 80 columns", listW)
	}
	for _, l := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(l); w > 80 {
			t.Fatalf("line is %d wide: %q", w, ansi.Strip(l))
		}
	}
}

func TestClickingSidebarFileOpensIt(t *testing.T) {
	m := changesRepo(t, 140, 50)
	lines, files := m.sidebarLines(m.height - 1)
	row := -1
	for l, p := range files {
		if p == "notes.txt" {
			row = l
		}
	}
	if row < 0 {
		t.Fatalf("notes.txt not mapped: %v\n%s", files, ansi.Strip(strings.Join(lines, "\n")))
	}
	if !strings.Contains(ansi.Strip(lines[row]), "notes.txt") {
		t.Fatalf("row %d is %q", row, ansi.Strip(lines[row]))
	}
	x := m.width - sidebarWidth + 4
	next, cmd := m.Update(tea.MouseMsg{X: x, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = feed(t, next.(Model), cmd)
	if m.changes == nil {
		t.Fatal("click didn't open the viewer")
	}
	if f, _ := m.changes.file(); f.Path != "notes.txt" {
		t.Fatalf("opened %q", f.Path)
	}
}

func TestParseUnifiedSkipsGitHeaders(t *testing.T) {
	d := parseUnified("diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n keep\n--- old dashes\n+++ new pluses\n")
	if len(d) != 3 || d[1].kind != '-' || d[1].text != "-- old dashes" || d[2].kind != '+' || d[2].text != "++ new pluses" {
		t.Fatalf("got %+v", d)
	}
}
