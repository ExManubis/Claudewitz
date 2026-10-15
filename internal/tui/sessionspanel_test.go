package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
