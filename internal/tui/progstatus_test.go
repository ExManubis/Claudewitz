package tui

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// Tests never write status to the terminal running them.
func init() { statusOut = func(string) {} }

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestSessionStatus(t *testing.T) {
	waiting := sum("w", "/a", daemon.StateWaiting)
	waiting.Waiting = &daemon.Waiting{Tool: "Bash", Summary: "rm -rf build"}
	question := sum("q", "/a", daemon.StateWaiting)
	question.Waiting = &daemon.Waiting{Tool: toolAskUser, Summary: "Which branch?"}
	working := sum("k", "/a", daemon.StateWorking)
	working.Activity = &daemon.Activity{Kind: "tool", Text: "Reading main.go"}
	failed := daemon.SessionSummary{ID: "e", State: daemon.StateExited, LastError: ptr("auth expired")}
	cases := []struct {
		s    daemon.SessionSummary
		done bool
		want string
	}{
		{waiting, false, "state=blocked:kind=permission:title=" + b64("app") + ":msg=" + b64("Bash rm -rf build")},
		{question, false, "state=blocked:kind=question:title=" + b64("app") + ":msg=" + b64("Which branch?")},
		{working, false, "state=working:title=" + b64("app") + ":msg=" + b64("Reading main.go")},
		{failed, false, "state=error:title=" + b64("app") + ":msg=" + b64("auth expired")},
		{sum("d", "/a", daemon.StateIdle), true, "state=done:title=" + b64("app")},
		{sum("i", "/a", daemon.StateIdle), false, "state=idle:title=" + b64("app")},
	}
	for _, c := range cases {
		if got := sessionStatus(c.s, c.done, "app"); got != c.want {
			t.Errorf("%s: %q, want %q", c.s.ID, got, c.want)
		}
	}
}

func TestStatusIDAndText(t *testing.T) {
	if got := statusID("s_1f/../x y"); got != "s_1f..xy" {
		t.Errorf("id %q", got)
	}
	if got := statusID(strings.Repeat("a", 40)); len(got) != 32 {
		t.Errorf("id not cut to 32: %d", len(got))
	}
	// safetext shows an escape as ␛, never sends it.
	if got := statusText("msg", " \x1b[31m ", 10); got != ":msg="+b64("␛[31m") {
		t.Errorf("control characters kept: %q", got)
	}
	if statusText("msg", "", 10) != "" {
		t.Error("empty text sent")
	}
	// Cut on a rune boundary: "é" is two bytes.
	if got := statusText("title", "aé", 2); got != ":title="+b64("a") {
		t.Errorf("cut %q", got)
	}
}

func TestStatusSeqSendsOnlyChanges(t *testing.T) {
	sent := map[string]string{"a": "id=a:state=working", "b": "id=b:state=idle"}
	want := map[string]string{"a": "id=a:state=done", "b": "id=b:state=idle", "c": "id=c:state=working"}
	got := statusSeq(sent, want)
	wantSeq := "\x1b]7501;id=a:state=done\x1b\\" + "\x1b]7501;id=c:state=working\x1b\\"
	if got != wantSeq {
		t.Errorf("seq %q", got)
	}
	// A session gone is cleared; claudewitz's own record goes idle, as a
	// clear without an id would clear every record.
	got = statusSeq(map[string]string{"": "state=working", "a": "id=a:state=idle"}, map[string]string{})
	if got != "\x1b]7501;state=idle\x1b\\\x1b]7501;state=clear:id=a\x1b\\" {
		t.Errorf("clears %q", got)
	}
}

func TestOwnStatusWithoutDaemon(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	if got := m.programStatus()[""]; got != "state=idle" {
		t.Fatalf("idle: %q", got)
	}
	m.busy, m.activity = true, "Thinking"
	if got := m.programStatus()[""]; got != "state=working:msg="+b64("Thinking") {
		t.Errorf("working: %q", got)
	}
	m.pending = []*claude.PermissionRequest{{ToolName: toolAskUser}}
	if got := m.programStatus()[""]; !strings.HasPrefix(got, "state=blocked:kind=question") {
		t.Errorf("question: %q", got)
	}
	// With the daemon, each live session is its own record instead.
	m.live = []daemon.SessionSummary{sum("s_1", "/a", daemon.StateWorking)}
	st := m.programStatus()
	if _, own := st[""]; own || !strings.HasPrefix(st["s_1"], "id=s_1:state=working") {
		t.Errorf("daemon records %v", st)
	}
}
