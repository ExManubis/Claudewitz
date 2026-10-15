package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

func ctrlQ() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlQ} }

func TestKillDialogDefaultsToKeep(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateWorking), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	p := newFakeSession()
	m.proc = p
	next, _ := m.Update(ctrlQ())
	m = next.(Model)
	if m.kill == nil {
		t.Fatal("no kill dialog")
	}
	view := ansi.Strip(m.killView())
	for _, want := range []string{"Kill session t s_1?", "It's working right now; the current turn is lost.", "Keep it running", "Kill session  y"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog lacks %q:\n%s", want, view)
		}
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.kill != nil || p.stopped {
		t.Fatalf("enter on the default killed it: dialog %v stopped %v", m.kill, p.stopped)
	}
}

// Killing the focused session moves focus to the next row, and killing the
// last one leaves an empty view.
func TestKillFocusesNext(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	p := newFakeSession()
	m.proc = p
	next, _ := m.Update(ctrlQ())
	next, cmd := next.(Model).Update(key("y"))
	m = run(t, next.(Model), cmd)
	if !p.stopped || m.sessionView.id != "s_2" || m.viewByID("s_1") != nil {
		t.Fatalf("stopped %v focused %q", p.stopped, m.sessionView.id)
	}
	next, _ = m.Update(ctrlQ())
	next, cmd = next.(Model).Update(key("y"))
	m = run(t, next.(Model), cmd)
	if m.sessionView.id != "" || m.proc != nil {
		t.Fatalf("after killing the last: focused %q proc %v", m.sessionView.id, m.proc)
	}
}

func TestCtrlQDismissesAnErroredSession(t *testing.T) {
	errd := daemon.SessionSummary{ID: "s_1", CWD: "/x/a", State: daemon.StateExited, LastError: ptr("boom")}
	m := daemonModel(t, newFakeDaemon(errd, sum("s_2", "/x/b", daemon.StateIdle)))
	next, cmd := m.Update(ctrlQ())
	m = run(t, next.(Model), cmd)
	if m.kill != nil || len(m.live) != 1 || m.sessionView.id != "s_2" {
		t.Fatalf("dismiss: dialog %v live %d focused %q", m.kill, len(m.live), m.sessionView.id)
	}
}

// A focused session with no process (mid-attach or attach failed) is killed
// in the daemon by id, and a late push doesn't bring its row back.
func TestKillWithoutProcAsksDaemon(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	m.proc = nil
	next, _ := m.Update(ctrlQ())
	next, cmd := next.(Model).Update(key("y"))
	m = run(t, next.(Model), cmd)
	if len(d.killed) != 1 || d.killed[0] != "s_1" {
		t.Fatalf("killed %v", d.killed)
	}
	before := len(m.live)
	params, _ := json.Marshal(daemon.SessionUpdatedParams{Summary: sum("s_1", "/x/a", daemon.StateIdle)})
	m.applyOverview(daemon.Frame{Event: daemon.EventSessionUpdated, Params: params})
	if len(m.live) != before {
		t.Fatalf("ghost row: live %d, was %d", len(m.live), before)
	}
}

// An errored session the daemon has already forgotten opens without an
// attach, shows its error, and ctrl+q dismisses it.
func TestFocusErroredSessionShowsErrorAndDismisses(t *testing.T) {
	errd := daemon.SessionSummary{ID: "s_1", CWD: "/x/a", State: daemon.StateExited, LastError: ptr("auth expired"),
		ClaudeSessionID: ptr("c_1")}
	d := newFakeDaemon(sum("s_2", "/x/b", daemon.StateIdle), errd)
	m := daemonModel(t, d)
	d.list = d.list[:1] // the daemon dropped s_1 when it exited
	d.strict = true
	m = run(t, m, m.focusSession("s_1"))
	if m.sessionView.id != "s_1" || len(d.attached) != 0 {
		t.Fatalf("focused %q attached %v", m.sessionView.id, d.attached)
	}
	if !strings.Contains(ansi.Strip(m.chatContent), "auth expired") {
		t.Fatalf("chat lacks the error:\n%s", ansi.Strip(m.chatContent))
	}
	next, cmd := m.Update(ctrlQ())
	m = run(t, next.(Model), cmd)
	if m.kill != nil || len(m.live) != 1 || m.sessionView.id != "s_2" {
		t.Fatalf("dismiss: dialog %v live %d focused %q", m.kill, len(m.live), m.sessionView.id)
	}
}
