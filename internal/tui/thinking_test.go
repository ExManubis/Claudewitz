package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
	if v := m.chat.View(80); !strings.Contains(v, "Let me look at chat.go") || strings.Contains(v, "Thinking…") {
		t.Errorf("view = %q", v)
	}
}
