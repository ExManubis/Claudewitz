package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/daemon"
	"github.com/exmanubis/claudewitz/internal/sessions"
)

func daemonModel(t *testing.T, d *fakeDaemon) Model {
	t.Helper()
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil).WithDaemon(d)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = next.(Model)
	m.live, _ = d.List(context.Background())
	for _, s := range m.live {
		m.noteState(s)
	}
	m.sessionView.id = m.live[0].ID
	m.resize()
	return m
}

// Focusing a session for the first time attaches to it; focusing it again
// reuses the view, draft included.
func TestFocusSessionAttachesOnce(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	first := m.sessionView

	m = run(t, m, m.focusSession("s_2"))
	if m.sessionView.id != "s_2" || len(d.attached) != 1 || m.proc == nil {
		t.Fatalf("focused %q attached %v proc %v", m.sessionView.id, d.attached, m.proc)
	}
	m.input.SetValue("draft for b")
	m = run(t, m, m.focusSession("s_1"))
	if m.sessionView != first {
		t.Fatal("didn't go back to the first view")
	}
	m = run(t, m, m.focusSession("s_2"))
	if len(d.attached) != 1 || m.input.Value() != "draft for b" {
		t.Fatalf("attached again %v, draft %q", d.attached, m.input.Value())
	}
}

func TestNextNeedsYouPicksOldestWaitingThenErrors(t *testing.T) {
	old, young := sum("s_2", "/x/b", daemon.StateWaiting), sum("s_3", "/x/c", daemon.StateWaiting)
	old.Waiting = &daemon.Waiting{Since: time.Now().Add(-time.Minute)}
	young.Waiting = &daemon.Waiting{Since: time.Now()}
	errd := daemon.SessionSummary{ID: "s_4", CWD: "/x/d", State: daemon.StateExited, LastError: ptr("x")}
	m := daemonModel(t, newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), young, old, errd))
	if id := m.nextNeedsYou(); id != "s_2" {
		t.Fatalf("next: %q", id)
	}
	m.sessionView.id = "s_2"
	if id := m.nextNeedsYou(); id != "s_3" {
		t.Fatalf("next after focusing s_2: %q", id)
	}
	m.live = m.live[:1]
	m.live = append(m.live, errd)
	if id := m.nextNeedsYou(); id != "s_4" {
		t.Fatalf("next with only an error: %q", id)
	}
}

func TestSwitchKeys(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle), sum("s_3", "/x/c", daemon.StateIdle))
	m := daemonModel(t, d)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	m = run(t, next.(Model), cmd)
	if m.sessionView.id != "s_2" {
		t.Fatalf("alt+down: %q", m.sessionView.id)
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3"), Alt: true})
	m = run(t, next.(Model), cmd)
	if m.sessionView.id != "s_3" {
		t.Fatalf("alt+3: %q", m.sessionView.id)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
	m = next.(Model)
	if m.sessionsMode() != sessionsNone || m.width != 160 {
		t.Fatalf("alt+s didn't hide the panel: mode %d width %d", m.sessionsMode(), m.width)
	}
}

func TestPickLaunch(t *testing.T) {
	a, b := sum("s_1", "/x/here", daemon.StateIdle), sum("s_2", "/x/here", daemon.StateIdle)
	a.LastActivity, b.LastActivity = time.Now().Add(-time.Hour), time.Now()
	if id := pickLaunch([]daemon.SessionSummary{a, b, sum("s_3", "/x/else", daemon.StateIdle)}, "/x/here"); id != "s_2" {
		t.Fatalf("launch picked %q", id)
	}
	if id := pickLaunch([]daemon.SessionSummary{sum("s_3", "/x/else", daemon.StateIdle)}, "/x/here"); id != "" {
		t.Fatalf("launch picked %q in another folder", id)
	}
}

// A failed attach leaves nothing behind, so focusing the session again retries.
func TestFocusSessionRetriesAfterFailedAttach(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	d.attachErr = errors.New("gone")

	m = run(t, m, m.focusSession("s_2"))
	m = run(t, m, m.focusSession("s_1"))
	m = run(t, m, m.focusSession("s_2"))
	if len(d.attached) != 2 || m.sessionView.id != "s_2" || m.proc == nil {
		t.Fatalf("attached %v, focused %q, proc %v", d.attached, m.sessionView.id, m.proc)
	}
}

// Switching away while a view's fresh start is in flight keeps the view, so
// the process that arrives afterwards still reaches it.
func TestFocusAwayFromStartingViewKeepsIt(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	starting := m.sessionView
	starting.id = ""

	m = run(t, m, m.focusSession("s_2"))
	p := newFakeSession()
	next, _ := m.Update(startedMsg{view: starting, proc: p, id: "s_new"})
	m = next.(Model)
	if m.viewFor(p) != starting || !slices.Contains(m.views, starting) || starting.id != "s_new" {
		t.Fatalf("starting view lost: viewFor %v, views %d", m.viewFor(p), len(m.views))
	}
}

func TestQuitDetachesEverySession(t *testing.T) {
	m := daemonModel(t, newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle)))
	a, b := newFakeSession(), newFakeSession()
	m.proc = a
	other := newSessionView(claude.Options{CWD: t.TempDir()})
	other.proc, other.id = b, "s_2"
	m.views = append(m.views, other)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil {
		t.Fatal("ctrl+d didn't quit")
	}
	if !a.detached || !b.detached || a.stopped || b.stopped {
		t.Fatalf("detached %v/%v stopped %v/%v", a.detached, b.detached, a.stopped, b.stopped)
	}
}

// /new in a daemon session detaches from it (it keeps running in the
// panel) and starts a fresh one.
func TestNewDetachesTheDaemonSession(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	p := newFakeSession()
	m.proc = p
	cmd, _ := m.slash("/new")
	m = run(t, m, cmd)
	if p.stopped || !p.detached {
		t.Fatalf("old proc stopped %v detached %v", p.stopped, p.detached)
	}
	if len(d.started) != 1 || m.sessionView.id != "s_new1" {
		t.Fatalf("started %d focused %q", len(d.started), m.sessionView.id)
	}
}

// Resuming a session that is live in the daemon focuses its view instead
// of starting a second one.
func TestResumeLiveSessionFocusesIt(t *testing.T) {
	b := sum("s_2", "/x/b", daemon.StateIdle)
	b.ClaudeSessionID = ptr("c_2")
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), b)
	m := daemonModel(t, d)
	p := newFakeSession()
	m.proc = p
	m = run(t, m, m.focusSession("s_2"))
	bview := m.sessionView
	m = run(t, m, m.focusSession("s_1"))
	m = run(t, m, m.resume(sessions.Info{ID: "c_2", CWD: "/x/b", Path: "/nonexistent"}))
	if m.sessionView != bview || len(d.started) != 0 || p.stopped {
		t.Fatalf("focused %q started %d old stopped %v", m.sessionView.id, len(d.started), p.stopped)
	}
	n := 0
	for _, v := range m.views {
		if v.id == "s_2" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d views for s_2", n)
	}
}

// A background session's toasts say which session they're about, and its
// exit doesn't suggest /new (that would act on the focused one).
func TestBackgroundToastsNameTheirSession(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/api", daemon.StateIdle), sum("s_2", "/x/web", daemon.StateIdle))
	m := daemonModel(t, d)
	m.proc = newFakeSession()
	m = run(t, m, m.focusSession("s_2"))
	bg := m.viewByID("s_1").proc.(*fakeSession)
	bg.emit(claude.Event{Exited: true, Err: errors.New("killed")})
	m = run(t, m, waitEvent(bg))
	if len(m.toasts) != 1 || m.toasts[0].title != "api · Claude Code exited" || strings.Contains(m.toasts[0].body, "/new") {
		t.Fatalf("toasts %+v", m.toasts)
	}
}
