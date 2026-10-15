package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

// killState is the Kill session dialog.
type killState struct {
	id, title, cwd string
	working        bool // mid-turn or waiting: the turn is lost
	cursor         int  // 0 keep (default), 1 kill
}

// openKill asks to kill the focused session; an errored one is dismissed
// straight away.
func (m *Model) openKill() tea.Cmd {
	s, ok := m.summary(m.sessionView.id)
	if !ok {
		return nil
	}
	st := stateOf(s, false)
	if st == stError {
		return m.dismissFocused()
	}
	m.kill = &killState{id: s.ID, title: rowTitle(s), cwd: s.CWD, working: st == stWorking || st == stWaiting}
	m.input.Blur()
	return nil
}

func (m *Model) handleKillKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "up", "down", "tab", "k", "j":
		m.kill.cursor = 1 - m.kill.cursor
	case "1", "n", "esc", "ctrl+q":
		m.kill = nil
	case "2", "y":
		return m.killFocused()
	case "enter":
		if m.kill.cursor == 1 {
			return m.killFocused()
		}
		m.kill = nil
	}
	if m.kill == nil && !m.chatFocus {
		m.input.Focus()
	}
	return nil
}

// killFocused stops the session the dialog names in the daemon and moves
// on.
func (m *Model) killFocused() tea.Cmd {
	id := m.kill.id
	m.kill = nil
	v := m.viewByID(id)
	var kill tea.Cmd
	if v != nil && v.proc != nil {
		v.proc.Stop()
		v.proc = nil
	} else if m.daemon != nil {
		// Mid-attach or failed attach: no process to stop, so ask the daemon.
		d := m.daemon
		kill = func() tea.Msg { _ = d.Kill(context.Background(), id); return nil }
	}
	m.killed[id] = true
	if v != m.sessionView {
		// Not on screen (the dialog outlived a switch): just drop its row.
		m.dropRow(id)
		if v != nil {
			m.removeView(v)
		}
		if m.ready && m.sessionsMode() != m.panelMode {
			m.resize()
		}
		return kill
	}
	return tea.Batch(kill, m.leaveFocused())
}

// dropRow forgets session id's row and its state.
func (m *Model) dropRow(id string) {
	m.live = slices.DeleteFunc(m.live, func(s daemon.SessionSummary) bool { return s.ID == id })
	delete(m.done, id)
	delete(m.since, id)
	delete(m.lastState, id)
}

// dismissFocused forgets an errored session's row.
func (m *Model) dismissFocused() tea.Cmd {
	return m.leaveFocused()
}

// leaveFocused drops the focused session from the panel and focuses the
// next row, or an empty view when none is left.
func (m *Model) leaveFocused() tea.Cmd {
	gone := m.sessionView
	rows := m.rows()
	at := slices.IndexFunc(rows, func(r daemon.SessionSummary) bool { return r.ID == gone.id })
	m.dropRow(gone.id)
	rows = m.rows()
	var cmd tea.Cmd
	if len(rows) > 0 {
		cmd = m.focusSession(rows[min(max(at, 0), len(rows)-1)].ID)
	} else {
		cmd = m.emptyView()
	}
	if m.sessionView != gone {
		m.removeView(gone)
	}
	if m.ready && m.sessionsMode() != m.panelMode {
		m.resize()
	}
	return cmd
}

// emptyView is what's left when no session runs: a view with no process
// in the launch folder.
func (m *Model) emptyView() tea.Cmd {
	opts := m.base
	opts.Resume, opts.Continue = "", false
	v := newSessionView(opts)
	m.views = append(m.views, v)
	cmd := m.focus(v)
	m.chat.info("No sessions running · alt+n starts one")
	m.refresh()
	return cmd
}

func (m Model) killView() string {
	k := m.kill
	th := currentTheme()
	lines := []string{
		errStyle.Bold(true).Render("Kill session ") + textStyle.Bold(true).Render(k.title) + errStyle.Bold(true).Render("?"),
		lipgloss.NewStyle().Foreground(th.second).Render(prettyPath(k.cwd)),
		"",
	}
	if k.working {
		lines = append(lines, warnStyle.Render("▲ It's working right now; the current turn is lost."))
	}
	lines = append(lines, mutedStyle.Render("claude ends; the transcript is kept, ctrl+r resumes it."), "")
	choices := []struct{ label, keys string }{{"Keep it running", "n · esc"}, {"Kill session", "y"}}
	for i, c := range choices {
		pointer := "  "
		style := textStyle
		if i == 1 {
			style = errStyle
		}
		if i == k.cursor {
			pointer = statusKey.Render("❯ ")
			style = style.Bold(true)
		}
		label := style.Render(c.label)
		lines = append(lines, pointer+mutedStyle.Render(fmt.Sprintf("%d. ", i+1))+label+mutedStyle.Render("  "+c.keys))
	}
	return popupBox.BorderForeground(th.err).Width(min(64, m.termW-4)).Render(strings.Join(lines, "\n"))
}
