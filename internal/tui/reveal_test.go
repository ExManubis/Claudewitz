package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/claude"
)

func TestRevealStep(t *testing.T) {
	dt := 1.0 / revealFPS
	if n := revealStep(1, dt); n != 1 {
		t.Errorf("a one-byte backlog should finish in a frame, got %d", n)
	}
	if n := revealStep(5, 10); n != 5 {
		t.Errorf("the step must never pass the backlog, got %d", n)
	}
	small, big := revealStep(20, dt), revealStep(2000, dt)
	if big <= small*10 {
		t.Errorf("a large backlog should drain proportionally faster: 20→%d, 2000→%d", small, big)
	}
}

func TestRevealCatchesUpWithoutSplittingRunes(t *testing.T) {
	c := newChat(newTracker())
	e := c.add(&entry{kind: kindAssistant, paced: true})
	e.setText("héllo wörld — ✻ ünïcödé everywhere ✻")
	if got := e.visibleText(); got != "" {
		t.Fatalf("nothing should show before the first tick, got %q", got)
	}
	frames := 0
	for c.revealing() {
		if !c.reveal(1.0 / revealFPS) {
			t.Fatal("reveal reported no change while text was pending")
		}
		if v := e.visibleText(); !utf8.ValidString(v) {
			t.Fatalf("revealed text split a character: %q", v)
		}
		if frames++; frames > revealFPS*5 {
			t.Fatal("reveal never caught up")
		}
	}
	if e.visibleText() != e.text {
		t.Fatalf("visible %q, want %q", e.visibleText(), e.text)
	}
	if frames < 2 {
		t.Errorf("expected the text to be paced over several frames, took %d", frames)
	}
}

func TestUnpacedEntriesShowEverything(t *testing.T) {
	e := &entry{kind: kindAssistant, text: "replayed history"}
	if e.visibleText() != e.text {
		t.Fatal("history should render in full")
	}
}

// A reply that streamed in while its session was out of view shows in full
// as soon as the session is focused, instead of waiting on the next event.
func TestFocusShowsTextStreamedInTheBackground(t *testing.T) {
	m := readyModel(t)
	bg := newSessionView(claude.Options{CWD: t.TempDir()})
	m.views = append(m.views, bg)
	m.inView(bg, func() {
		stream(t, &m, `{"event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`)
		stream(t, &m, `{"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done while you were away"}}}`)
	})
	if bg.chat.entries[0].visibleText() != "" {
		t.Fatal("the hidden reply was revealed before focus")
	}
	_ = m.focus(bg)
	if got := m.chat.entries[0].visibleText(); got != "done while you were away" {
		t.Fatalf("after focus the reply shows %q", got)
	}
	if v := ansi.Strip(m.vp.View()); !strings.Contains(v, "done while you were away") {
		t.Fatalf("the reply isn't drawn:\n%s", v)
	}
}
