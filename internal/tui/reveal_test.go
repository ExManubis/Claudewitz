package tui

import (
	"strings"
	"testing"
	"time"
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

// An entry that's cheap to render redraws on every reveal frame.
func TestRevealRedrawsCheapEntriesEveryFrame(t *testing.T) {
	c := newChat(newTracker())
	e := c.add(&entry{kind: kindAssistant, paced: true, renderCost: time.Millisecond})
	e.setText(strings.Repeat("word ", 400))
	e.dirty = false
	for range 5 {
		if !c.reveal(1.0/revealFPS) || !e.dirty {
			t.Fatal("a cheap entry should redraw every frame")
		}
		e.dirty = false
	}
}

// An entry that's costly to render (a long code block streaming, say)
// redraws only once enough time has passed to pay for the last render,
// so it can't hog the UI loop. The reveal still moves on underneath.
func TestRevealThrottlesCostlyEntries(t *testing.T) {
	c := newChat(newTracker())
	e := c.add(&entry{kind: kindAssistant, paced: true, renderCost: 50 * time.Millisecond})
	e.setText(strings.Repeat("word ", 4000))
	e.dirty = false
	dt := 1.0 / revealFPS
	frames := 0
	for !e.dirty {
		before := e.shown
		changed := c.reveal(dt)
		if e.shown <= before {
			t.Fatal("the reveal stalled while throttled")
		}
		if changed != e.dirty {
			t.Fatalf("reveal reported change=%v with dirty=%v", changed, e.dirty)
		}
		if frames++; frames > revealFPS {
			t.Fatal("a throttled entry never redrew")
		}
	}
	if gap := float64(frames) * dt; gap < revealCostRatio*0.05 {
		t.Errorf("redrew after %.3fs, want at least %.3fs for a 50ms render", gap, revealCostRatio*0.05)
	}
}

// However costly the entry, the frame that catches up with the stream
// draws, or the tick loop would stop with the tail undrawn.
func TestRevealAlwaysDrawsTheLastFrame(t *testing.T) {
	c := newChat(newTracker())
	e := c.add(&entry{kind: kindAssistant, paced: true, renderCost: time.Second})
	e.setText("short")
	e.dirty = false
	if !c.reveal(1) || !e.dirty || e.visibleText() != "short" {
		t.Fatalf("caught-up frame should draw: dirty=%v visible=%q", e.dirty, e.visibleText())
	}
}

// Rendering a paced entry records what it cost.
func TestViewRecordsRenderCostOfPacedEntries(t *testing.T) {
	c := newChat(newTracker())
	e := c.add(&entry{kind: kindAssistant, paced: true, text: "# Heading\n\nSome *text*.", shown: 23})
	_ = c.View(80)
	if e.renderCost <= 0 {
		t.Fatal("no render cost recorded")
	}
}
