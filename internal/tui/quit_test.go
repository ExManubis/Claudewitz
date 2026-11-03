package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestQuitStopsLocalSession(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	if !isQuit(m.quit()) {
		t.Fatal("a local session should quit at once")
	}
}
