package tui

import (
	"context"
	"testing"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

func TestContextWindowFromResult(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.model = "claude-opus-5-5"
	m.handleEvent(claude.Event{Result: &claude.ResultEvent{Subtype: "success", ModelUsage: map[string]claude.ModelUsage{
		"claude-opus-5-5[1m]": {ContextWindow: 1000000},
	}}})
	if got := m.contextWindow(); got != 1000000 {
		t.Fatalf("context window = %d, want 1000000", got)
	}
}

func TestContextWindowBeforeFirstTurn(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	for model, want := range map[string]int{"claude-opus-5-5[1m]": 1000000, "claude-sonnet-5-5": 200000, "": 200000} {
		m.model = model
		if got := m.contextWindow(); got != want {
			t.Errorf("model %q: context window = %d, want %d", model, got, want)
		}
	}
}

func TestContextWindowResetsOnModelSwitch(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.model = "claude-opus-5-5[1m]"
	m.ctxWindow = 1000000
	m.applyModel(claude.ModelInfo{Value: "haiku", ResolvedModel: "claude-haiku-4-5"}, "")
	if got := m.contextWindow(); got != 200000 {
		t.Fatalf("after switching to haiku: context window = %d, want 200000", got)
	}
}
