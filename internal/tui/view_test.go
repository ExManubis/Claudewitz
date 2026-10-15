package tui

import (
	"context"
	"testing"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

// Each session view keeps its own draft, transcript and pending dialogs, so
// switching to another session and back loses nothing.
func TestViewsKeepSeparateState(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	first := m.sessionView
	m.input.SetValue("draft one")
	m.pending = append(m.pending, &claude.PermissionRequest{RequestID: "r1", ToolName: "Bash"})
	m.chat.info("first session")

	second := newSessionView(claude.Options{CWD: t.TempDir()})
	m.views = append(m.views, second)
	m.sessionView = second
	if m.input.Value() != "" || len(m.pending) != 0 || len(m.chat.entries) != 0 {
		t.Fatalf("a new view shares state: input %q, %d pending, %d entries", m.input.Value(), len(m.pending), len(m.chat.entries))
	}
	m.input.SetValue("draft two")

	m.sessionView = first
	if m.input.Value() != "draft one" || len(m.pending) != 1 || len(m.chat.entries) != 1 {
		t.Fatalf("switching back lost state: input %q, %d pending, %d entries", m.input.Value(), len(m.pending), len(m.chat.entries))
	}
	if second.input.Value() != "draft two" {
		t.Fatalf("second draft: %q", second.input.Value())
	}
}

func TestNewSessionViewDefaults(t *testing.T) {
	v := newSessionView(claude.Options{CWD: "/tmp/x"})
	if v.opts.CWD != "/tmp/x" || v.permMode != "default" || !v.follow || v.alwaysAllow == nil || v.chat == nil || v.track == nil {
		t.Fatalf("defaults: %+v", v)
	}
	if v.chat.cwd != "/tmp/x" {
		t.Fatalf("chat cwd %q", v.chat.cwd)
	}
}
