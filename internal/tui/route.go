package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// viewFor finds the view a process belongs to; nil for a stale process.
func (m *Model) viewFor(p claude.Session) *sessionView {
	if p == nil {
		return nil
	}
	for _, v := range m.views {
		if v.proc == p {
			return v
		}
	}
	return nil
}

// inView runs f with v standing in for the focused session, for a session
// in the background. What f renders lands in v's own viewport.
func (m *Model) inView(v *sessionView, f func()) {
	focused := m.sessionView
	m.sessionView = v
	defer func() { m.sessionView = focused }()
	f()
}

// spaceFor returns dir's workspace, creating it (and reporting so) when no
// view has used that folder yet.
func (m *Model) spaceFor(dir string) (*workspace, bool) {
	if ws, ok := m.spaces[dir]; ok {
		return ws, false
	}
	ws := newWorkspace(dir)
	m.spaces[dir] = ws
	return ws, true
}

// focus shows v: its transcript, input and dialogs, and its folder's git
// status, files and history.
func (m *Model) focus(v *sessionView) tea.Cmd {
	if v == m.sessionView {
		return nil
	}
	m.input.Blur()
	m.sessionView = v
	dir := m.cwd()
	ws, created := m.spaceFor(dir)
	m.workspace = ws
	var cmds []tea.Cmd
	if created {
		cmds = append(cmds, indexFiles(dir), loadHistory(dir))
	}
	cmds = append(cmds, m.refreshGit())
	if !m.chatFocus && len(m.pending) == 0 {
		m.input.Focus()
	}
	m.comp.update(m)
	m.layout()
	m.refresh()
	return tea.Batch(cmds...)
}

// removeView forgets v, and its folder's workspace when no other view
// uses it. v must not be the focused view.
func (m *Model) removeView(v *sessionView) {
	for i, x := range m.views {
		if x == v {
			m.views = append(m.views[:i:i], m.views[i+1:]...)
			break
		}
	}
	dir := v.opts.CWD
	if v.init != nil && v.init.CWD != "" {
		dir = v.init.CWD
	}
	for _, x := range m.views {
		xd := x.opts.CWD
		if x.init != nil && x.init.CWD != "" {
			xd = x.init.CWD
		}
		if xd == dir {
			return
		}
	}
	if m.workspace == nil || m.workspace.dir != dir {
		delete(m.spaces, dir)
	}
}
