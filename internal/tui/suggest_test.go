package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

func TestPromptSuggestion(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.width, m.height = 120, 40
	m.handleEvent(claude.Event{Suggestion: "Create these files."})
	if !strings.HasPrefix(m.input.Placeholder, "Create these files.") {
		t.Fatalf("placeholder: %q", m.input.Placeholder)
	}

	m.input.SetValue("typed")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.input.Value() != "typed" || m.suggestion == "" {
		t.Fatal("→ with text in the input should move the cursor, not accept")
	}

	m.input.Reset()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.input.Value() != "Create these files." {
		t.Fatalf("→ should accept the suggestion, got %q", m.input.Value())
	}
	if m.suggestion != "" || m.input.Placeholder != inputPlaceholder {
		t.Fatal("accepting should restore the default placeholder")
	}

	m.input.Reset()
	m.handleEvent(claude.Event{Suggestion: "Run the tests."})
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.suggestion != "" || m.hist.items[len(m.hist.items)-1] != "Run the tests." {
		t.Fatal("enter on an empty input should send the suggestion")
	}
}
