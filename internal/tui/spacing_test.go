package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

// A full transcript gets a blank row above it and another between it and
// the activity line, and the screen still fits the terminal exactly.
func TestChatHasRoomAboveAndBelow(t *testing.T) {
	for _, width := range []int{90, 140} { // header strip, sidebar
		m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
		var tm tea.Model = m
		tm, _ = tm.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		mm := tm.(Model)
		for i := range 30 {
			mm.chat.add(&entry{kind: kindAssistant, text: fmt.Sprintf("line %d", i)})
		}
		mm.busy, mm.activity, mm.turnFrom = true, "Thinking", time.Now()
		mm.layout()
		mm.refresh()

		lines := strings.Split(ansi.Strip(mm.View()), "\n")
		if len(lines) != 24 {
			t.Fatalf("width %d: view is %d rows, want 24", width, len(lines))
		}
		main := func(i int) string { return strings.TrimSpace(ansi.Cut(lines[i], 0, mm.mainWidth())) }
		top := mm.chatTop()
		if main(top-1) != "" || main(top) == "" {
			t.Errorf("width %d: want a blank row then the chat at row %d, got %q / %q", width, top, main(top-1), main(top))
		}
		act := -1
		for i := range lines {
			if strings.Contains(main(i), "Thinking") {
				act = i
			}
		}
		if act != top+mm.vp.Height+1 || main(act-1) != "" || main(act-2) == "" {
			t.Errorf("width %d: activity at row %d (chat %d+%d), rows above %q / %q", width, act, top, mm.vp.Height, main(act-2), main(act-1))
		}
	}
}
