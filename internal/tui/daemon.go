package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// Daemon is what the TUI needs from the claudewitz daemon to show and drive
// every live session (client.Hub). Without the daemon it is nil.
type Daemon interface {
	Start(ctx context.Context, opts claude.Options) (claude.Session, string, error)
	Attach(ctx context.Context, id string) (claude.Session, error)
	List(ctx context.Context) ([]daemon.SessionSummary, error)
	Overview() <-chan daemon.Frame
}

// WithDaemon runs sessions in the daemon and shows all of its sessions.
func (m Model) WithDaemon(d Daemon) Model {
	m.daemon = d
	m.panelOn = true
	m.done = map[string]bool{}
	m.since = map[string]time.Time{}
	m.lastState = map[string]rowState{}
	return m
}

// rowState is how a session shows in the panel, most urgent first.
type rowState int

const (
	stWaiting rowState = iota
	stError
	stWorking
	stDone
	stIdle
)

func stateOf(s daemon.SessionSummary, done bool) rowState {
	switch {
	case s.State == daemon.StateWaiting:
		return stWaiting
	case s.State == daemon.StateExited:
		return stError
	case s.State == daemon.StateWorking || s.State == daemon.StateStarting:
		return stWorking
	case done:
		return stDone
	}
	return stIdle
}

// orderRows sorts sessions most urgent first, keeping start order within a
// state so rows don't jump while sessions stream.
func orderRows(list []daemon.SessionSummary, done map[string]bool) []daemon.SessionSummary {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b daemon.SessionSummary) int {
		return int(stateOf(a, done[a.ID])) - int(stateOf(b, done[b.ID]))
	})
	return out
}

// folderLabels names each session's folder: the base name, with the parent
// when two different folders share it, and "repo ⎇ worktree" for linked
// worktrees.
func folderLabels(list []daemon.SessionSummary) map[string]string {
	base := map[string]map[string]bool{} // base name -> distinct folders
	for _, s := range list {
		if s.Worktree != nil {
			continue
		}
		b := filepath.Base(s.CWD)
		if base[b] == nil {
			base[b] = map[string]bool{}
		}
		base[b][s.CWD] = true
	}
	out := map[string]string{}
	for _, s := range list {
		switch {
		case s.Worktree != nil && s.Repo != nil:
			out[s.ID] = filepath.Base(*s.Repo) + " " + icons.worktree + " " + *s.Worktree
		case len(base[filepath.Base(s.CWD)]) > 1:
			out[s.ID] = filepath.Join(filepath.Base(filepath.Dir(s.CWD)), filepath.Base(s.CWD))
		default:
			out[s.ID] = filepath.Base(s.CWD)
		}
	}
	return out
}

// ageLabel is a short time-in-state: 40s, 2m, 1h30m.
func ageLabel(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return shortDur(d)
}

// viewByID is the view showing the session with daemon id id, if any.
func (m *Model) viewByID(id string) *sessionView {
	for _, v := range m.views {
		if v.id == id {
			return v
		}
	}
	return nil
}

// rows is every session for the panel, most urgent first.
func (m Model) rows() []daemon.SessionSummary { return orderRows(m.live, m.done) }

// needYou counts sessions waiting on you or stopped by an error.
func (m Model) needYou() int {
	n := 0
	for _, s := range m.live {
		if st := stateOf(s, m.done[s.ID]); st == stWaiting || st == stError {
			n++
		}
	}
	return n
}

type overviewListMsg struct {
	list []daemon.SessionSummary
	err  error
}

type overviewMsg daemon.Frame

func listCmd(d Daemon) tea.Cmd {
	return func() tea.Msg {
		list, err := d.List(context.Background())
		return overviewListMsg{list, err}
	}
}

func waitOverview(ch <-chan daemon.Frame) tea.Cmd {
	return func() tea.Msg {
		f, ok := <-ch
		if !ok {
			return nil
		}
		return overviewMsg(f)
	}
}

// noteState records when a session entered its current state, for the panel's
// time column, and whether it just started needing you.
func (m *Model) noteState(s daemon.SessionSummary) (nowNeedsYou bool) {
	st := stateOf(s, m.done[s.ID])
	prev, seen := m.lastState[s.ID]
	if !seen || prev != st {
		at := time.Now()
		switch {
		case st == stWaiting && s.Waiting != nil:
			at = s.Waiting.Since
		case !seen && s.LastTurnEndedAt != nil && (st == stIdle || st == stDone):
			at = *s.LastTurnEndedAt
		}
		m.since[s.ID] = at
		m.lastState[s.ID] = st
	}
	return seen && prev != stWaiting && st == stWaiting
}

// applyOverview keeps m.live current from a daemon push. It returns a toast
// when a session in the background starts needing you.
func (m *Model) applyOverview(f daemon.Frame) tea.Cmd {
	switch f.Event {
	case daemon.EventSessionUpdated:
		var p daemon.SessionUpdatedParams
		if json.Unmarshal(f.Params, &p) != nil {
			return nil
		}
		s := p.Summary
		i := slices.IndexFunc(m.live, func(x daemon.SessionSummary) bool { return x.ID == s.ID })
		if i < 0 {
			m.live = append(m.live, s)
		} else {
			prev := m.live[i]
			if s.ID != m.sessionView.id && s.LastTurnEndedAt != nil &&
				(prev.LastTurnEndedAt == nil || !s.LastTurnEndedAt.Equal(*prev.LastTurnEndedAt)) {
				m.done[s.ID] = true
			}
			if s.State == daemon.StateWaiting || s.State == daemon.StateWorking {
				delete(m.done, s.ID)
			}
			m.live[i] = s
		}
		if m.noteState(s) && s.ID != m.sessionView.id {
			return m.notify(toastWarn, folderLabels(m.live)[s.ID]+" needs you", "ctrl+] to switch")
		}
	case daemon.EventSessionClosed:
		var p daemon.SessionClosedParams
		if json.Unmarshal(f.Params, &p) != nil {
			return nil
		}
		i := slices.IndexFunc(m.live, func(x daemon.SessionSummary) bool { return x.ID == p.Session })
		if i < 0 {
			return nil
		}
		if p.LastError != nil {
			m.live[i].State = daemon.StateExited
			m.live[i].LastError = p.LastError
			m.noteState(m.live[i])
			return nil
		}
		m.live = append(m.live[:i:i], m.live[i+1:]...)
		if v := m.viewByID(p.Session); v != nil && v != m.sessionView {
			m.removeView(v) // its process has ended; nothing left to show
		}
		delete(m.done, p.Session)
		delete(m.since, p.Session)
		delete(m.lastState, p.Session)
	}
	return nil
}
