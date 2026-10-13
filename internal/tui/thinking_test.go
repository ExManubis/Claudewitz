package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

func stream(t *testing.T, m *Model, js string) {
	t.Helper()
	var e claude.StreamEvent
	if err := json.Unmarshal([]byte(js), &e); err != nil {
		t.Fatal(err)
	}
	m.handleStream(&e)
}

// Thinking that arrives without text (only a signature) would leave a
// "Thinking…" placeholder in the transcript; the activity line covers it.
func TestEmptyThinkingAddsNoEntry(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	stream(t, &m, `{"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	stream(t, &m, `{"event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta"}}}`)
	if n := len(m.chat.entries); n != 0 {
		t.Fatalf("got %d entries, want none: %q", n, m.chat.View(80))
	}
	if m.activity != "Thinking" {
		t.Errorf("activity = %q, want Thinking", m.activity)
	}
}

func TestThinkingWithTextIsShown(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	stream(t, &m, `{"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	stream(t, &m, `{"event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me look"}}}`)
	stream(t, &m, `{"event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" at chat.go"}}}`)
	if n := len(m.chat.entries); n != 1 {
		t.Fatalf("got %d entries, want 1", n)
	}
	if v := ansi.Strip(m.chat.View(80)); !strings.Contains(v, "Let me look at chat.go") || strings.Contains(v, "Thinking…") {
		t.Errorf("view = %q", v)
	}
}

// Hidden thinking still says how long it took once the block stops.
func TestHiddenThinkingShowsDuration(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	stream(t, &m, `{"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	m.chat.streaming[0].thinkStart = time.Now().Add(-3 * time.Second)
	stream(t, &m, `{"event":{"type":"content_block_stop","index":0}}`)
	if n := len(m.chat.entries); n != 1 {
		t.Fatalf("got %d entries, want 1", n)
	}
	if v := ansi.Strip(m.chat.View(80)); !strings.Contains(v, "Thought for 3s") {
		t.Errorf("view = %q", v)
	}
}

// A sub-second hidden block isn't worth a line.
func TestQuickHiddenThinkingAddsNoEntry(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	stream(t, &m, `{"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	stream(t, &m, `{"event":{"type":"content_block_stop","index":0}}`)
	if n := len(m.chat.entries); n != 0 {
		t.Fatalf("got %d entries, want none", n)
	}
}

func TestThinkingShowsNewestLinesUntilOpened(t *testing.T) {
	c := newChat(&tracker{})
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	e := c.add(&entry{kind: kindThinking, text: strings.Join(lines, "\n\n"), thought: 5 * time.Second})
	v := ansi.Strip(c.View(80))
	if strings.Contains(v, "line 1 ") || !strings.Contains(v, "line 20") || !strings.Contains(v, "earlier lines (ctrl+o)") {
		t.Errorf("closed view should tail the thinking: %q", v)
	}
	if !strings.Contains(v, "Thought for 5s") {
		t.Errorf("missing duration footer: %q", v)
	}
	// Every box row is the same width, so the tint has a straight edge.
	for _, l := range strings.Split(v, "\n")[:maxThinkingLines] {
		if w := ansi.StringWidth(l); w != 78 {
			t.Errorf("box row %d wide, want 78: %q", w, l)
		}
	}
	e.expanded, e.dirty = true, true
	if v := ansi.Strip(c.View(80)); !strings.Contains(v, "line 1 ") || strings.Contains(v, "earlier lines") {
		t.Errorf("opened view should show everything: %q", v)
	}
}
