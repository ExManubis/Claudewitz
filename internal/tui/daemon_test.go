package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

func ptr[T any](v T) *T { return &v }

func sum(id, cwd, state string) daemon.SessionSummary {
	return daemon.SessionSummary{ID: id, CWD: cwd, State: state, Title: "t " + id}
}

func TestOrderRows(t *testing.T) {
	list := []daemon.SessionSummary{
		sum("idle", "/a", daemon.StateIdle),
		sum("work1", "/b", daemon.StateWorking),
		sum("wait", "/c", daemon.StateWaiting),
		sum("done", "/d", daemon.StateIdle),
		sum("start", "/e", daemon.StateStarting),
		{ID: "err", CWD: "/f", State: daemon.StateExited, LastError: ptr("auth expired")},
	}
	got := orderRows(list, map[string]bool{"done": true})
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	want := []string{"wait", "err", "work1", "start", "done", "idle"}
	if len(ids) != len(want) {
		t.Fatalf("order %v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order %v, want %v", ids, want)
		}
	}
}

func TestFolderLabels(t *testing.T) {
	list := []daemon.SessionSummary{
		sum("1", "/Users/x/code/api", daemon.StateIdle),
		sum("2", "/Users/x/work/api", daemon.StateIdle),
		sum("3", "/Users/x/code/claudewitz", daemon.StateIdle),
		{ID: "4", CWD: "/Users/x/code/claudewitz/.claude/worktrees/ring-tests", State: daemon.StateIdle,
			Repo: ptr("/Users/x/code/claudewitz"), Worktree: ptr("ring-tests")},
		sum("5", "/Users/x/code/claudewitz", daemon.StateIdle),
	}
	got := folderLabels(list)
	want := map[string]string{
		"1": "code/api", "2": "work/api", "3": "claudewitz",
		"4": "claudewitz " + icons.worktree + " ring-tests", "5": "claudewitz",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("label %s = %q, want %q", id, got[id], w)
		}
	}
}

func TestAgeLabel(t *testing.T) {
	for d, want := range map[time.Duration]string{40 * time.Second: "40s", 2 * time.Minute: "2m", 90 * time.Minute: "1h30m"} {
		if got := ageLabel(d); got != want {
			t.Errorf("ageLabel(%s) = %q, want %q", d, got, want)
		}
	}
}

func frame(event string, params any) daemon.Frame {
	b, _ := json.Marshal(params)
	return daemon.Frame{Event: event, Params: b}
}

// A session that starts waiting while another has focus raises a toast and
// counts as needing you; a turn that ends in the background marks it done.
func TestApplyOverview(t *testing.T) {
	m := readyModel(t)
	m.sessionView.id = "s_1"
	m.live = []daemon.SessionSummary{sum("s_1", "/a", daemon.StateIdle), sum("s_2", "/b", daemon.StateWorking)}
	for _, s := range m.live {
		m.noteState(s)
	}

	w := sum("s_2", "/b", daemon.StateWaiting)
	w.Waiting = &daemon.Waiting{RequestID: "r", Tool: "Bash", Since: time.Now()}
	if cmd := m.applyOverview(frame(daemon.EventSessionUpdated, daemon.SessionUpdatedParams{Summary: w})); cmd == nil {
		t.Fatal("no needs-you toast")
	}
	if m.needYou() != 1 {
		t.Fatalf("needYou %d", m.needYou())
	}

	d := sum("s_2", "/b", daemon.StateIdle)
	d.LastTurnEndedAt = ptr(time.Now())
	m.applyOverview(frame(daemon.EventSessionUpdated, daemon.SessionUpdatedParams{Summary: d}))
	if !m.done["s_2"] || m.needYou() != 0 {
		t.Fatalf("done %v needYou %d", m.done, m.needYou())
	}

	bg := newSessionView(m.base)
	bg.id = "s_2"
	m.views = append(m.views, bg)
	m.applyOverview(frame(daemon.EventSessionClosed, daemon.SessionClosedParams{Session: "s_2"}))
	if len(m.live) != 1 || m.viewByID("s_2") != nil {
		t.Fatalf("closed session kept: %+v, view %v", m.live, m.viewByID("s_2"))
	}
	m.applyOverview(frame(daemon.EventSessionClosed, daemon.SessionClosedParams{Session: "s_1", LastError: ptr("boom")}))
	if len(m.live) != 1 || m.live[0].State != daemon.StateExited || stateOf(m.live[0], false) != stError {
		t.Fatalf("errored session: %+v", m.live)
	}
}
