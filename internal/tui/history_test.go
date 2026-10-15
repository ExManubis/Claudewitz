package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

func TestHistoryBrowse(t *testing.T) {
	t.Setenv("CLAUDEWITZ_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	h := newHistory("/proj")
	h.add("one")
	h.add("two")
	h.add("one") // repeats move to the newest position

	if _, ok := h.prev("typed something"); ok {
		t.Fatal("↑ with text in the input should move the cursor, not recall")
	}
	got := []string{}
	cur := ""
	for i := 0; i < 3; i++ {
		s, ok := h.prev(cur)
		if !ok {
			t.Fatalf("prev %d failed", i)
		}
		cur = s
		got = append(got, s)
	}
	if strings.Join(got, ",") != "one,two,two" {
		t.Fatalf("browsing back: %v (should stop at the oldest)", got)
	}
	if s, _ := h.next(cur); s != "one" {
		t.Fatalf("next: %q", s)
	}
	if s, ok := h.next("one"); !ok || s != "" {
		t.Fatalf("past the newest should restore the empty draft, got %q %v", s, ok)
	}
	if _, ok := h.next(""); ok {
		t.Fatal("↓ when not browsing should fall through")
	}

	// Persisted to the history file, scoped by project.
	data, _ := os.ReadFile(config.HistoryPath())
	if strings.Count(string(data), `"project":"/proj"`) != 3 {
		t.Fatalf("history file: %s", data)
	}
	msg := loadHistory("/proj")().(historyLoadedMsg)
	if strings.Join(msg.items, ",") != "two,one" {
		t.Fatalf("reloaded: %v", msg.items)
	}
	if other := loadHistory("/elsewhere")().(historyLoadedMsg); len(other.items) != 0 {
		t.Fatalf("other projects should not see this history: %v", other.items)
	}
}

func TestQueuedMessagesJoinOnReplay(t *testing.T) {
	t.Setenv("CLAUDEWITZ_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.width, m.height = 120, 40
	m.queued = []queuedMsg{{text: "second"}, {text: "third"}}
	if p := m.pillsView(120); !strings.Contains(p, "2 queued") || !strings.Contains(p, "second") {
		t.Fatalf("queue pill: %q", p)
	}
	m.takeQueued("unrelated first message")
	if len(m.queued) != 2 {
		t.Fatal("a replay of an earlier message shouldn't consume the queue")
	}
	m.takeQueued("second\nthird")
	if len(m.queued) != 0 {
		t.Fatalf("queue not consumed: %v", m.queued)
	}
	n := 0
	for _, e := range m.chat.entries {
		if e.kind == kindUser {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("queued messages should join the transcript, got %d user entries", n)
	}
}

func TestTodoPill(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.track.todos = []todo{{content: "a", status: "completed"}, {content: "b", active: "Writing tests", status: "in_progress"}, {content: "c"}}
	if p := m.pillsView(120); !strings.Contains(p, "1/3") || !strings.Contains(p, "Writing tests") {
		t.Fatalf("todo pill: %q", p)
	}
	for i := range m.track.todos {
		m.track.todos[i].status = "completed"
	}
	if p := m.pillsView(120); p != "" {
		t.Fatalf("finished todos shouldn't show a pill: %q", p)
	}
}

func TestOSCNotification(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	if got := oscNotification("Claude; done", "all\ngood"); got != "\x1b]9;Claude  done: all good\x1b\\\a" {
		t.Fatalf("osc 9: %q", got)
	}
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	if got := oscNotification("a", "b"); !strings.HasPrefix(got, "\x1bPtmux;\x1b\x1b]9;") {
		t.Fatalf("tmux passthrough: %q", got)
	}
}

func TestNoNotificationWhileFocused(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{Notifications: "bell"}, nil)
	if m.desktopNotify("x", "y") != nil {
		t.Fatal("focused terminals shouldn't get notifications")
	}
	m.focused = false
	if m.desktopNotify("x", "y") == nil {
		t.Fatal("unfocused terminal should be notified")
	}
	m.cfg.Notifications = "off"
	if m.desktopNotify("x", "y") != nil {
		t.Fatal("off means off")
	}
}
