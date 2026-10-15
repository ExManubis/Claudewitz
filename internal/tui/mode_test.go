package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

// Auto mode has its own label in the status bar and is first in the
// shift+tab cycle, which wraps back to it after plan.
func TestAutoMode(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir(), PermissionMode: "auto"}, config.Config{}, nil)
	if got := ansi.Strip(m.statusView()); !strings.Contains(got, "auto mode") {
		t.Fatalf("status %q", got)
	}
	var seen []string
	for range permissionModes {
		m.cyclePermissionMode()
		seen = append(seen, m.permMode)
	}
	if strings.Join(seen, ",") != "default,acceptEdits,plan,auto" {
		t.Fatalf("cycle %v", seen)
	}
}
