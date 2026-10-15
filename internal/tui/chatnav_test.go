package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

func chatModel(t *testing.T) *Model {
	t.Helper()
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	mm := &m
	mm.width, mm.height = 90, 20
	mm.layout()
	mm.ready = true
	c := mm.chat
	c.add(&entry{kind: kindUser, text: "first question"})
	c.add(&entry{kind: kindThinking, text: strings.Repeat("a long thought that wraps around the line. ", 20)})
	c.add(&entry{kind: kindTool, toolID: "t1", toolName: "Bash", toolInput: json.RawMessage(`{"command":"ls -la"}`), toolStatus: toolOK, toolOutput: "a\nb\nc\nd\ne\nf\ng"})
	c.add(&entry{kind: kindInfo, text: "1.2s · 1 turns"})
	c.add(&entry{kind: kindAssistant, text: "Here is the answer."})
	mm.refresh()
	return mm
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestChatFocusAndNavigation(t *testing.T) {
	m := chatModel(t)
	m.focusChat()
	if !m.chatFocus || m.chat.sel != 4 {
		t.Fatalf("focus should select the last entry, sel=%d", m.chat.sel)
	}
	m.handleChatKey(key("k"))
	if m.chat.sel != 2 {
		t.Fatalf("k should skip the info line to the tool call, sel=%d", m.chat.sel)
	}
	m.handleChatKey(key("g"))
	if m.chat.sel != 0 {
		t.Fatalf("g should go to the top, sel=%d", m.chat.sel)
	}
	m.handleChatKey(key("k"))
	if m.chat.sel != 0 {
		t.Fatal("k at the top should stay put")
	}
	m.handleChatKey(key("G"))
	if m.chat.sel != 4 {
		t.Fatalf("G should go to the bottom, sel=%d", m.chat.sel)
	}
	if _, handled := m.handleChatKey(key("x")); handled || m.chatFocus {
		t.Fatal("typing should return to the input and pass the key through")
	}
}

func TestSelectionBarKeepsWidth(t *testing.T) {
	m := chatModel(t)
	plain := m.chat.View(90)
	m.focusChat()
	marked := m.chat.View(90)
	pl, ml := strings.Split(plain, "\n"), strings.Split(marked, "\n")
	if len(pl) != len(ml) {
		t.Fatal("selection must not change the line count")
	}
	changed := 0
	for i := range pl {
		if lipgloss.Width(pl[i]) != lipgloss.Width(ml[i]) && pl[i] != "" {
			t.Fatalf("line %d width changed: %q vs %q", i, pl[i], ml[i])
		}
		if pl[i] != ml[i] {
			changed++
		}
	}
	if changed == 0 {
		t.Fatal("the selected entry should be marked")
	}
}

func TestExpandOneEntry(t *testing.T) {
	m := chatModel(t)
	m.focusChat()
	m.chat.sel = 2 // Bash with 7 output lines, clipped to 4
	before := m.chat.heights[2]
	m.handleChatKey(key("space"))
	m.chat.View(90)
	if !m.chat.entries[2].expanded || m.chat.heights[2] <= before {
		t.Fatalf("space should expand the entry: %d -> %d", before, m.chat.heights[2])
	}
	if m.chat.entries[1].expanded {
		t.Fatal("other entries stay collapsed")
	}
}

func TestEntryAtAndScroll(t *testing.T) {
	m := chatModel(t)
	c := m.chat
	for i := range c.entries {
		if got := c.entryAt(c.offsets[i]); (c.selectable(i) && got != i) || (!c.selectable(i) && got != -1) {
			t.Fatalf("entryAt(%d) = %d for entry %d", c.offsets[i], got, i)
		}
	}
	// Selecting the first entry while scrolled to the bottom brings it into view.
	m.focusChat()
	m.handleChatKey(key("g"))
	if m.vp.YOffset > c.offsets[0] {
		t.Fatalf("view should scroll up to the selection, offset %d", m.vp.YOffset)
	}
}

func TestCopyText(t *testing.T) {
	e := &entry{kind: kindTool, toolName: "Bash", toolInput: json.RawMessage(`{"command":"go test ./..."}`), toolOutput: "ok"}
	if got := copyText(e); got != "$ go test ./...\n\nok" {
		t.Fatalf("bash copy: %q", got)
	}
	if got := copyText(&entry{kind: kindAssistant, text: "**md** source"}); got != "**md** source" {
		t.Fatalf("assistant copy should be the markdown source: %q", got)
	}
}

func TestBatchedKeys(t *testing.T) {
	m := chatModel(t)
	m.focusChat()
	// "kk" in one event, as fast typing or key repeat delivers it.
	m.handleChatKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("kk")})
	if !m.chatFocus || m.chat.sel != 1 {
		t.Fatalf("both k presses should move the selection: focus=%v sel=%d", m.chatFocus, m.chat.sel)
	}
	// A burst that starts typing hands the rest to the input.
	m.handleChatKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jhello")})
	if m.chatFocus || m.input.Value() != "hello" {
		t.Fatalf("typing after a nav key: focus=%v value=%q", m.chatFocus, m.input.Value())
	}
}
