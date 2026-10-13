package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// longChat is a chat model with enough content to scroll.
func longChat(t *testing.T) *Model {
	m := chatModel(t)
	for i := 0; i < 30; i++ {
		m.chat.add(&entry{kind: kindAssistant, text: fmt.Sprintf("Paragraph %d of the answer.", i)})
	}
	m.follow = true
	m.refresh()
	return m
}

func mouse(action tea.MouseAction, x, y int) tea.MouseMsg {
	return tea.MouseMsg{Action: action, Button: tea.MouseButtonLeft, X: x, Y: y}
}

func TestScrollbarGeometry(t *testing.T) {
	m := longChat(t)
	bar := m.scrollbar()
	if len(bar) != m.vp.Height {
		t.Fatalf("bar rows %d, viewport %d", len(bar), m.vp.Height)
	}
	thumbRows := func() (first, n int) {
		first = -1
		for i, c := range m.scrollbar() {
			if strings.Contains(c, "┃") {
				if first < 0 {
					first = i
				}
				n++
			}
		}
		return
	}
	first, n := thumbRows()
	if n == 0 || n >= m.vp.Height || first+n != m.vp.Height {
		t.Fatalf("at the bottom the thumb should end at the last row: first=%d n=%d h=%d", first, n, m.vp.Height)
	}
	m.vp.GotoTop()
	if first, _ := thumbRows(); first != 0 {
		t.Fatalf("at the top the thumb should start at row 0, got %d", first)
	}

	short := chatModel(t)
	short.chat.entries = short.chat.entries[:1]
	short.refresh()
	for _, c := range short.scrollbar() {
		if strings.TrimSpace(c) != "" {
			t.Fatal("no scrollbar when everything fits")
		}
	}
}

func TestSelectionSpanAndText(t *testing.T) {
	s := textSelection{anchor: textPos{2, 5}, head: textPos{0, 3}, active: true}
	if a, b := s.ordered(); a != (textPos{0, 3}) || b != (textPos{2, 5}) {
		t.Fatalf("ordered: %v %v", a, b)
	}
	cases := []struct{ line, from, to int }{{0, 3, 10}, {1, 0, 10}, {2, 0, 6}}
	for _, c := range cases {
		from, to, ok := s.span(c.line, 10)
		if !ok || from != c.from || to != c.to {
			t.Fatalf("line %d: %d-%d %v, want %d-%d", c.line, from, to, ok, c.from, c.to)
		}
	}
	if _, _, ok := s.span(3, 10); ok {
		t.Fatal("line outside the selection")
	}

	m := chatModel(t)
	m.chatContent = "  hello world   \n  second line\nthird"
	m.textSel = &textSelection{anchor: textPos{0, 2}, head: textPos{1, 7}, active: true}
	if got := m.selectedText(); got != "hello world\n  second" { // end column is inclusive
		t.Fatalf("selected text: %q", got)
	}
}

func TestHighlightKeepsWidth(t *testing.T) {
	m := longChat(t)
	view := m.vp.View()
	m.textSel = &textSelection{anchor: textPos{m.vp.YOffset + 1, 4}, head: textPos{m.vp.YOffset + 3, 12}, active: true}
	hl := m.highlightSelection(view)
	a, b := strings.Split(view, "\n"), strings.Split(hl, "\n")
	if len(a) != len(b) {
		t.Fatal("line count changed")
	}
	for i := range a {
		if lipgloss.Width(a[i]) != lipgloss.Width(b[i]) {
			t.Fatalf("line %d width %d -> %d", i, lipgloss.Width(a[i]), lipgloss.Width(b[i]))
		}
	}
	if a[2] == b[2] || a[0] != b[0] {
		t.Fatal("only selected lines should change")
	}
}

func TestDragSelectCopiesAndClickSelects(t *testing.T) {
	m := longChat(t)
	m.handleMouse(mouse(tea.MouseActionPress, 2, 1))
	m.handleMouse(mouse(tea.MouseActionMotion, 20, 3))
	if m.textSel == nil || !m.textSel.active {
		t.Fatal("dragging should start a selection")
	}
	want := m.selectedText()
	cmd := m.handleMouse(mouse(tea.MouseActionRelease, 20, 3))
	if cmd == nil || want == "" || len(m.toasts) == 0 || !strings.HasPrefix(m.toasts[len(m.toasts)-1].title, "Copied") {
		t.Fatalf("release should copy %q and toast: %+v", want, m.toasts)
	}

	// A press and release without movement selects the entry instead.
	m.textSel = nil
	m.handleMouse(mouse(tea.MouseActionPress, 5, 2))
	m.handleMouse(mouse(tea.MouseActionRelease, 5, 2))
	if !m.chatFocus || m.textSel != nil {
		t.Fatalf("click should focus the transcript and select an entry: focus=%v", m.chatFocus)
	}
}

func TestDragScrollbar(t *testing.T) {
	m := longChat(t)
	bottom := m.vp.YOffset
	m.handleMouse(mouse(tea.MouseActionPress, m.chatWidth(), m.chatTop()))
	if !m.dragBar || m.vp.YOffset >= bottom {
		t.Fatalf("pressing the top of the track should scroll up: drag=%v offset %d -> %d", m.dragBar, bottom, m.vp.YOffset)
	}
	m.handleMouse(mouse(tea.MouseActionMotion, m.chatWidth(), m.chatTop()+m.vp.Height-1))
	if !m.vp.AtBottom() {
		t.Fatal("dragging to the bottom of the track should reach the end")
	}
	m.handleMouse(mouse(tea.MouseActionRelease, m.chatWidth(), m.chatTop()+m.vp.Height-1))
	if m.dragBar {
		t.Fatal("release should end the drag")
	}
}
