package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

// daemonSession records how a daemon-held session was left.
type daemonSession struct {
	claude.Session   // nil; only Close and Detach are called
	closed, detached bool
}

func (d *daemonSession) Stop()         { d.closed = true }
func (d *daemonSession) Detach() error { d.detached = true; return nil }

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestQuitAsksToDetachDaemonSession(t *testing.T) {
	for _, tc := range []struct {
		key              tea.KeyMsg
		detached, closed bool
	}{
		{tea.KeyMsg{Type: tea.KeyEnter}, true, false},
		{tea.KeyMsg{Type: tea.KeyCtrlD}, true, false}, // ctrl+d twice detaches
		{key("s"), false, true},
	} {
		m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
		ds := &daemonSession{}
		m.proc = ds
		if cmd := m.quit(); cmd != nil || !m.quitAsk {
			t.Fatalf("quit didn't ask")
		}
		_, cmd := m.Update(tc.key)
		if !isQuit(cmd) || ds.detached != tc.detached || ds.closed != tc.closed {
			t.Errorf("%s: quit=%v detached=%v closed=%v", tc.key, isQuit(cmd), ds.detached, ds.closed)
		}
	}
}

func TestQuitAskCancels(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	ds := &daemonSession{}
	m.proc = ds
	m.quit()
	next, cmd := m.Update(key("esc"))
	if isQuit(cmd) || next.(Model).quitAsk || ds.detached || ds.closed {
		t.Fatal("esc should cancel")
	}
}

func TestQuitStopsLocalSession(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	if !isQuit(m.quit()) || m.quitAsk {
		t.Fatal("a local session should quit at once")
	}
}
