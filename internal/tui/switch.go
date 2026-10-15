package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

func (m *Model) summary(id string) (daemon.SessionSummary, bool) {
	i := slices.IndexFunc(m.live, func(s daemon.SessionSummary) bool { return s.ID == id })
	if i < 0 {
		return daemon.SessionSummary{}, false
	}
	return m.live[i], true
}

// liveByClaudeID is the daemon id of the live session running Claude Code
// session sid, or "".
func (m *Model) liveByClaudeID(sid string) string {
	for _, s := range m.live {
		if s.ClaudeSessionID != nil && *s.ClaudeSessionID == sid && s.State != daemon.StateExited {
			return s.ID
		}
	}
	return ""
}

// attachCmd follows a live session from now on.
func attachCmd(d Daemon, v *sessionView, id string) tea.Cmd {
	return func() tea.Msg {
		p, err := d.Attach(context.Background(), id)
		return startedMsg{view: v, proc: p, id: id, err: err}
	}
}

// focusSession shows the session with daemon id id, attaching to it the
// first time: its history from the transcript, then its live events.
func (m *Model) focusSession(id string) tea.Cmd {
	prev := m.sessionView
	cmd := m.focusSessionView(id)
	// A view whose attach failed is dead once it loses focus (not one still starting).
	if prev != m.sessionView && prev.attachFailed {
		m.removeView(prev)
	}
	return cmd
}

func (m *Model) focusSessionView(id string) tea.Cmd {
	if v := m.viewByID(id); v != nil {
		delete(m.done, id)
		return m.focus(v)
	}
	s, ok := m.summary(id)
	if !ok || m.daemon == nil {
		return nil
	}
	opts := m.base
	opts.CWD, opts.Resume, opts.Continue = s.CWD, "", false
	opts.Model, opts.Effort, opts.PermissionMode = s.Model, s.Effort, s.PermissionMode
	v := newSessionView(opts)
	v.id = id
	if s.ClaudeSessionID != nil {
		v.sessionID = *s.ClaudeSessionID
	}
	v.sessionTitle = s.Title
	m.views = append(m.views, v)
	delete(m.done, id)
	cmds := []tea.Cmd{m.focus(v)}
	if s.State == daemon.StateExited {
		// The daemon has forgotten it: nothing to attach to. Show its history
		// and why it ended; ctrl+q dismisses it.
		why := "claude exited"
		if s.LastError != nil {
			why += ": " + *s.LastError
		}
		if v.sessionID == "" {
			m.chat.error("%s", why)
			m.refresh()
			return tea.Batch(cmds...)
		}
		load := loadTranscript(v, s.CWD, v.sessionID)
		return tea.Batch(append(cmds, func() tea.Msg {
			msg := load().(transcriptMsg)
			msg.exitErr = why
			return msg
		})...)
	}
	cmds = append(cmds, attachCmd(m.daemon, v, id))
	if v.sessionID != "" {
		cmds = append(cmds, loadTranscript(v, s.CWD, v.sessionID))
	}
	return tea.Batch(cmds...)
}

// nextNeedsYou is the session ctrl+] goes to: the one waiting longest
// (other than the focused one), else one stopped by an error.
func (m *Model) nextNeedsYou() string {
	var waiting []daemon.SessionSummary
	var errored string
	for _, s := range m.live {
		if s.ID == m.sessionView.id {
			continue
		}
		switch stateOf(s, false) {
		case stWaiting:
			waiting = append(waiting, s)
		case stError:
			if errored == "" {
				errored = s.ID
			}
		}
	}
	if len(waiting) == 0 {
		return errored
	}
	slices.SortStableFunc(waiting, func(a, b daemon.SessionSummary) int {
		if a.Waiting == nil || b.Waiting == nil {
			return 0
		}
		return a.Waiting.Since.Compare(b.Waiting.Since)
	})
	return waiting[0].ID
}

// handleSessionKey runs the sessions keys; ok is false for any other key.
func (m *Model) handleSessionKey(k tea.KeyMsg) (tea.Cmd, bool) {
	if m.daemon == nil {
		return nil, false
	}
	s := k.String()
	rows := m.rows()
	at := slices.IndexFunc(rows, func(r daemon.SessionSummary) bool { return r.ID == m.sessionView.id })
	switch {
	case s == "alt+n":
		return m.openFolders(), true
	case s == "alt+s":
		m.panelOn = !m.panelOn
		m.resize()
		return nil, true
	case s == "alt+up" || s == "alt+down":
		if len(rows) < 2 {
			return nil, true
		}
		step := 1
		if s == "alt+up" {
			step = -1
		}
		return m.focusSession(rows[(max(at, 0)+step+len(rows))%len(rows)].ID), true
	case strings.HasPrefix(s, "alt+") && len(s) == 5 && s[4] >= '1' && s[4] <= '9':
		n, _ := strconv.Atoi(s[4:])
		if n <= len(rows) {
			return m.focusSession(rows[n-1].ID), true
		}
		return nil, true
	case s == "ctrl+q":
		return m.openKill(), true
	case s == "ctrl+]":
		if id := m.nextNeedsYou(); id != "" {
			return m.focusSession(id), true
		}
		return m.notify(toastInfo, "Nothing needs you", ""), true
	}
	return nil, false
}

// pickLaunch is the live session a launch in cwd focuses: the most
// recently active one there, or "" to start a new one.
func pickLaunch(list []daemon.SessionSummary, cwd string) string {
	best := ""
	var bestAt daemon.SessionSummary
	for _, s := range list {
		if s.CWD != cwd || s.State == daemon.StateExited {
			continue
		}
		if best == "" || s.LastActivity.After(bestAt.LastActivity) {
			best, bestAt = s.ID, s
		}
	}
	return best
}

type launchListMsg struct {
	list []daemon.SessionSummary
	err  error
}

func launchListCmd(d Daemon) tea.Cmd {
	return func() tea.Msg {
		list, err := d.List(context.Background())
		return launchListMsg{list, err}
	}
}

// launch decides what the first view shows: the newest live session in
// this folder, else a new session here.
func (m *Model) launch(msg launchListMsg) tea.Cmd {
	if msg.err == nil {
		m.live = msg.list
		for _, s := range m.live {
			m.noteState(s)
		}
		if id := pickLaunch(msg.list, m.opts.CWD); id != "" {
			s, _ := m.summary(id)
			v := m.sessionView
			v.id = id
			if s.ClaudeSessionID != nil {
				v.sessionID = *s.ClaudeSessionID
			}
			cmds := []tea.Cmd{attachCmd(m.daemon, v, id)}
			if v.sessionID != "" {
				cmds = append(cmds, loadTranscript(v, s.CWD, v.sessionID))
			}
			m.resize()
			return tea.Batch(cmds...)
		}
	}
	m.resize()
	return m.startCmd(m.sessionView, m.opts)
}

// daemonStart starts a session in the daemon for v.
func daemonStart(d Daemon, v *sessionView, opts claude.Options) tea.Cmd {
	return func() tea.Msg {
		p, id, err := d.Start(context.Background(), opts)
		return startedMsg{view: v, proc: p, id: id, err: err}
	}
}
