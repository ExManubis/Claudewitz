package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

func withSessions(t *testing.T, width int, list ...daemon.SessionSummary) Model {
	t.Helper()
	m := readyModel(t).WithDaemon(newFakeDaemon())
	m.live = list
	for _, s := range list {
		m.noteState(s)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return next.(Model)
}

func TestSessionsModeByWidth(t *testing.T) {
	two := []daemon.SessionSummary{sum("a", "/x/one", daemon.StateIdle), sum("b", "/x/two", daemon.StateWaiting)}
	m := withSessions(t, 160, two...)
	if m.sessionsMode() != sessionsPanel || m.width != 160-panelWidth || m.height != 40 {
		t.Fatalf("wide: mode %d width %d height %d", m.sessionsMode(), m.width, m.height)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	if m.sessionsMode() != sessionsTabs || m.width != 120 || m.height != 39 {
		t.Fatalf("narrow: mode %d width %d height %d", m.sessionsMode(), m.width, m.height)
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 170, Height: 40})
	m = next.(Model)
	if m.sessionsMode() != sessionsPanel || m.width != 170-panelWidth {
		t.Fatalf("wide again: mode %d width %d", m.sessionsMode(), m.width)
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(line); w > 170 {
			t.Fatalf("line %d wide: %q", w, line)
		}
	}
}

func TestNoPanelWithOneSessionOrWithoutDaemon(t *testing.T) {
	m := withSessions(t, 160, sum("a", "/x/one", daemon.StateIdle))
	m.sessionView.id = "a" // the one session is the focused one
	m.resize()
	if m.sessionsMode() != sessionsNone || m.width != 160 {
		t.Fatalf("one session: mode %d width %d", m.sessionsMode(), m.width)
	}
	plain := readyModel(t)
	if plain.sessionsMode() != sessionsNone {
		t.Fatal("panel without the daemon")
	}
}

func TestPanelShowsRowsInOrder(t *testing.T) {
	m := withSessions(t, 160,
		sum("a", "/x/idle-one", daemon.StateIdle),
		sum("b", "/x/waiting-one", daemon.StateWaiting),
	)
	view := ansi.Strip(m.sessionsView(40))
	wi, ii := strings.Index(view, "waiting-one"), strings.Index(view, "idle-one")
	if wi < 0 || ii < 0 || wi > ii {
		t.Fatalf("panel order:\n%s", view)
	}
	if !strings.Contains(view, "SESSIONS") || !strings.Contains(view, "1 need you") {
		t.Fatalf("panel header:\n%s", view)
	}
	if !strings.Contains(view, icons.sessWaiting+" t b") {
		t.Fatalf("waiting row:\n%s", view)
	}
}

func TestTabsLabelSharedFolders(t *testing.T) {
	a, b := sum("a", "/x/figment", daemon.StateIdle), sum("b", "/x/figment", daemon.StateIdle)
	a.Title, b.Title = "fonts", "index"
	m := withSessions(t, 120, a, b)
	tabs := ansi.Strip(m.tabsView(120))
	if !strings.Contains(tabs, "figment · fonts") || !strings.Contains(tabs, "figment · index") {
		t.Fatalf("tabs: %q", tabs)
	}
}

func TestPanelClickPicksRow(t *testing.T) {
	m := withSessions(t, 160, sum("a", "/x/one", daemon.StateIdle), sum("b", "/x/two", daemon.StateWaiting))
	// Row 0 is b (waiting first); rows start below the header line, two lines each.
	msg := tea.MouseMsg{X: 5, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	id, handled := m.sessionsMouse(&msg)
	if !handled || id != "b" {
		t.Fatalf("click: %q %v", id, handled)
	}
	chat := tea.MouseMsg{X: panelWidth + 10, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	if _, handled := m.sessionsMouse(&chat); handled || chat.X != 10 {
		t.Fatalf("chat click shifted to %d, handled %v", chat.X, handled)
	}
}

func TestReleaseOverPanelReachesChat(t *testing.T) {
	m := withSessions(t, 160, sum("a", "/x/one", daemon.StateIdle), sum("b", "/x/two", daemon.StateWaiting))
	m.mouseDown = true
	m.textSel = &textSelection{}
	rel := tea.MouseMsg{X: 5, Y: 5, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}
	if _, handled := m.sessionsMouse(&rel); handled || rel.X != 0 {
		t.Fatalf("release over panel: handled %v X %d", handled, rel.X)
	}
	next, _ := m.Update(tea.MouseMsg{X: 5, Y: 5, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if next.(Model).mouseDown {
		t.Fatal("release over the panel left the drag running")
	}
}

func TestTabClickWhenScrolled(t *testing.T) {
	var list []daemon.SessionSummary
	for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"} {
		list = append(list, sum(n, "/x/"+n+"-project-long-name", daemon.StateIdle))
	}
	m := withSessions(t, 60, list...)
	rows := m.rows()
	m.sessionView.id = rows[len(rows)-1].ID
	_, start := m.tabStrip(rows, m.termW)
	if start == 0 {
		t.Fatal("strip did not scroll")
	}
	msg := tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	if id, handled := m.sessionsMouse(&msg); !handled || id != rows[start].ID {
		t.Fatalf("click on first visible tab: %q %v, want %q", id, handled, rows[start].ID)
	}
}

// Rows show the session's whole folder path (~ for home), cut from the left
// when it doesn't fit, so the folder's own name stays visible.
func TestPanelShowsWholePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	long := filepath.Join(home, "Developer", "clients", "acme", "infrastructure", "terraform-modules", "networking")
	wt := daemon.SessionSummary{ID: "c", CWD: filepath.Join(home, "Developer", "claudewitz", ".claude", "worktrees", "ring-tests"),
		State: daemon.StateIdle, Repo: ptr(filepath.Join(home, "Developer", "claudewitz")), Worktree: ptr("ring-tests")}
	m := withSessions(t, 160,
		sum("a", filepath.Join(home, "Developer", "api"), daemon.StateIdle),
		sum("b", long, daemon.StateIdle),
		wt,
	)
	view := ansi.Strip(m.sessionsView(40))
	if !strings.Contains(view, "~/Developer/api") {
		t.Fatalf("short path:\n%s", view)
	}
	if !strings.Contains(view, "…") || !strings.Contains(view, "/networking") || strings.Contains(view, "~/Developer/clients") {
		t.Fatalf("long path not cut from the left:\n%s", view)
	}
	// Too long for the panel: cut from the left, keeping the repo and worktree names.
	if !strings.Contains(view, "/claudewitz "+icons.worktree+" ring-tests") {
		t.Fatalf("worktree path:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(line); w > panelWidth {
			t.Fatalf("line %d wide: %q", w, line)
		}
	}
}

// Each session is a rounded box; the focused one has the accent border and
// no background fill.
func TestPanelDrawsABoxPerSession(t *testing.T) {
	// Tests have no terminal, so lipgloss drops colour unless told otherwise.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	m := withSessions(t, 160, sum("a", "/x/one", daemon.StateIdle), sum("b", "/x/two", daemon.StateWaiting))
	m.sessionView.id = "a"
	raw := m.sessionsView(40)
	view := ansi.Strip(raw)
	if n := strings.Count(view, "╭"); n != 2 {
		t.Fatalf("%d boxes:\n%s", n, view)
	}
	// The escape sequence that turns a colour on, without the text after it.
	colour := func(st lipgloss.Style) string {
		r := st.Render("x")
		return r[:strings.Index(r, "x")]
	}
	accent, subtle := colour(lipgloss.NewStyle().Foreground(currentTheme().accent)), colour(lipgloss.NewStyle().Foreground(currentTheme().subtle))
	var accentTops, subtleTops int
	for _, line := range strings.Split(raw, "\n") {
		if i := strings.Index(line, "╭"); i >= 0 {
			switch {
			case strings.HasSuffix(line[:i], accent):
				accentTops++
			case strings.HasSuffix(line[:i], subtle):
				subtleTops++
			}
		}
	}
	if accentTops != 1 || subtleTops != 1 {
		t.Fatalf("box tops: %d accent, %d subtle; want 1 and 1:\n%q", accentTops, subtleTops, raw)
	}
	if strings.Contains(raw, colour(lipgloss.NewStyle().Background(currentTheme().selBg))) {
		t.Fatal("focused box still has a background fill")
	}
	for _, line := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(line); w > panelWidth {
			t.Fatalf("line %d wide: %q", w, line)
		}
	}
}

// With more sessions than fit, the list scrolls so the focused box is shown,
// says how many are hidden, and clicks pick the box under the pointer.
func TestPanelScrollsToFocusedBox(t *testing.T) {
	var list []daemon.SessionSummary
	for i := 0; i < 12; i++ {
		list = append(list, sum(fmt.Sprintf("s%02d", i), fmt.Sprintf("/x/p%02d", i), daemon.StateIdle))
	}
	m := withSessions(t, 160, list...)
	m.sessionView.id = "s11"
	view := ansi.Strip(m.sessionsView(m.termH))
	if !strings.Contains(view, "t s11") {
		t.Fatalf("focused box not shown:\n%s", view)
	}
	if strings.Contains(view, "t s00") || !strings.Contains(view, "more") {
		t.Fatalf("expected the top boxes hidden with a count:\n%s", view)
	}
	start, _ := m.boxWindow(len(m.rows()), m.termH)
	msg := tea.MouseMsg{X: 5, Y: panelTop + boxHeight, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	if id, _ := m.sessionsMouse(&msg); id != m.rows()[start+1].ID {
		t.Fatalf("click on the second visible box picked %q, want %q", id, m.rows()[start+1].ID)
	}
}

// dialogSpan finds a dialog on screen by its top border, a "╭───╮" run
// exactly as wide as the dialog, and returns its first and last column.
func dialogSpan(screen, dialog string) (left, right int) {
	want := ansi.StringWidth(strings.SplitN(ansi.Strip(dialog), "\n", 2)[0])
	for _, line := range strings.Split(ansi.Strip(screen), "\n") {
		r := []rune(line)
		for i := 0; i+want <= len(r); i++ {
			if r[i] == '╭' && r[i+want-1] == '╮' && strings.Trim(string(r[i+1:i+want-1]), "─") == "" {
				return i, i + want - 1
			}
		}
	}
	return -1, -1
}

// Dialogs centre on the whole terminal, not on the area beside the
// sessions panel.
func TestDialogsCentreOnTheWholeScreen(t *testing.T) {
	m := withSessions(t, 160, sum("a", "/x/one", daemon.StateIdle), sum("b", "/x/two", daemon.StateIdle))
	for name, open := range map[string]func(m *Model) string{
		"palette": func(m *Model) string { m.openPalette(); return m.palette.view(m.termW) },
		"help":    func(m *Model) string { m.showHelp = true; return helpDialog(m.termW-4, true) },
	} {
		mm := m
		dialog := open(&mm)
		left, right := dialogSpan(mm.View(), dialog)
		if left < 0 {
			t.Fatalf("%s: nothing drawn", name)
		}
		if gap := left - (m.termW - 1 - right); gap < -2 || gap > 2 {
			t.Errorf("%s not centred: columns %d-%d of %d", name, left, right, m.termW)
		}
	}
}

// The key hints sit on the panel's last two rows, however few sessions
// there are, and name alt+↑↓.
func TestPanelHintsPinnedToBottom(t *testing.T) {
	m := withSessions(t, 160, sum("a", "/x/a", daemon.StateIdle), sum("b", "/x/b", daemon.StateIdle))
	lines := strings.Split(ansi.Strip(m.sessionsView(40)), "\n")
	if len(lines) != 40 {
		t.Fatalf("%d lines", len(lines))
	}
	if !strings.Contains(lines[38], "alt+↑↓ switch") || !strings.Contains(lines[38], "ctrl+] next waiting") ||
		!strings.Contains(lines[39], "ctrl+q kill") {
		t.Fatalf("last rows:\n%s\n%s", lines[38], lines[39])
	}
}
