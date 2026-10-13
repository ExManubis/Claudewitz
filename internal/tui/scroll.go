package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The transcript renders one column narrower than the chat area; that
// column holds the scrollbar.
func (m Model) chatWidth() int { return max(10, m.mainWidth()-1) }

// chatTop is the screen row where the transcript starts.
func (m Model) chatTop() int {
	if m.showHeader() {
		return 1
	}
	return 0
}

// scrollbar renders the bar column for the viewport: a thumb sized to the
// visible share of the transcript, or blanks when everything fits.
func (m Model) scrollbar() []string {
	h := m.vp.Height
	out := make([]string, h)
	total := m.vp.TotalLineCount()
	if total <= h || h <= 0 {
		for i := range out {
			out[i] = " "
		}
		return out
	}
	thumb := max(1, h*h/total)
	maxOff := total - h
	pos := 0
	if maxOff > 0 {
		pos = m.vp.YOffset * (h - thumb) / maxOff
	}
	th := currentTheme()
	track := lipgloss.NewStyle().Foreground(th.subtle).Render("│")
	bar := lipgloss.NewStyle().Foreground(th.muted).Render("┃")
	if m.dragBar {
		bar = lipgloss.NewStyle().Foreground(th.accent).Render("┃")
	}
	for i := range out {
		if i >= pos && i < pos+thumb {
			out[i] = bar
		} else {
			out[i] = track
		}
	}
	return out
}

// scrollToBar moves the view so the thumb centres on a screen row.
func (m *Model) scrollToBar(row int) {
	h := m.vp.Height
	total := m.vp.TotalLineCount()
	if total <= h {
		return
	}
	thumb := max(1, h*h/total)
	row = max(0, min(h-thumb, row-thumb/2))
	if h-thumb > 0 {
		m.vp.SetYOffset(row * (total - h) / (h - thumb))
	}
	m.follow = m.vp.AtBottom()
}

// ── Text selection ─────────────────────────────────────────────────────

// textPos is a cell in the transcript: content line and column.
type textPos struct{ line, col int }

func (a textPos) before(b textPos) bool {
	return a.line < b.line || (a.line == b.line && a.col < b.col)
}

type textSelection struct {
	anchor, head textPos
	active       bool // the mouse has moved since the press
}

// ordered returns the selection's start and end, start first.
func (s textSelection) ordered() (textPos, textPos) {
	if s.head.before(s.anchor) {
		return s.head, s.anchor
	}
	return s.anchor, s.head
}

// span returns the selected columns [from, to) on a content line, or
// ok=false when the line isn't selected.
func (s textSelection) span(line, width int) (from, to int, ok bool) {
	start, end := s.ordered()
	if line < start.line || line > end.line {
		return 0, 0, false
	}
	from, to = 0, width
	if line == start.line {
		from = start.col
	}
	if line == end.line {
		to = end.col + 1
	}
	return from, max(from, min(to, width)), to > from
}

// highlightSelection paints the selected cells of the visible lines.
func (m Model) highlightSelection(view string) string {
	if m.textSel == nil || !m.textSel.active {
		return view
	}
	th := currentTheme()
	style := lipgloss.NewStyle().Background(th.selBg).Foreground(th.text)
	w := m.chatWidth()
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		from, to, ok := m.textSel.span(m.vp.YOffset+i, w)
		if !ok {
			continue
		}
		if pad := w - ansi.StringWidth(l); pad > 0 {
			l += strings.Repeat(" ", pad)
		}
		mid := ansi.Strip(ansi.Cut(l, from, to))
		lines[i] = ansi.Truncate(l, from, "") + "\x1b[0m" + style.Render(mid) + "\x1b[0m" + ansi.TruncateLeft(l, to, "")
	}
	return strings.Join(lines, "\n")
}

// selectedText is the plain text under the selection, with trailing
// spaces trimmed from each line.
func (m Model) selectedText() string {
	if m.textSel == nil {
		return ""
	}
	start, end := m.textSel.ordered()
	lines := strings.Split(m.chatContent, "\n")
	var out []string
	for ln := start.line; ln <= end.line && ln < len(lines); ln++ {
		from, to, ok := m.textSel.span(ln, m.chatWidth())
		if !ok {
			out = append(out, "")
			continue
		}
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(lines[ln], from, to)), " "))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// posAt converts a screen cell to a transcript position, clamped to the
// visible rows.
func (m Model) posAt(x, y int) textPos {
	row := max(0, min(m.vp.Height-1, y-m.chatTop()))
	return textPos{line: m.vp.YOffset + row, col: max(0, min(m.chatWidth()-1, x))}
}

func (m Model) inChat(x, y int) bool {
	row := y - m.chatTop()
	return x >= 0 && x < m.chatWidth() && row >= 0 && row < m.vp.Height
}

func (m Model) onScrollbar(x, y int) bool {
	row := y - m.chatTop()
	return x == m.chatWidth() && row >= 0 && row < m.vp.Height
}

// handleMouse routes mouse events: wheel scrolling, dragging the
// scrollbar, drag-to-select (copied on release) and click-to-select.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if tea.MouseEvent(msg).IsWheel() {
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		return c
	}
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return nil
		}
		switch {
		case m.onScrollbar(msg.X, msg.Y):
			m.dragBar = true
			m.scrollToBar(msg.Y - m.chatTop())
		case m.inChat(msg.X, msg.Y):
			p := m.posAt(msg.X, msg.Y)
			m.textSel = &textSelection{anchor: p, head: p}
			m.mouseDown = true
		default:
			m.textSel = nil
			// A click in the prompt area returns focus to the input.
			if m.chatFocus && msg.Y > m.chatTop()+m.vp.Height && msg.X < m.mainWidth() {
				m.blurChat()
			}
		}
	case tea.MouseActionMotion:
		switch {
		case m.dragBar:
			m.scrollToBar(msg.Y - m.chatTop())
		case m.mouseDown && m.textSel != nil:
			// Dragging past the top or bottom edge scrolls.
			row := msg.Y - m.chatTop()
			if row < 0 {
				m.vp.LineUp(1)
				m.follow = false
			} else if row >= m.vp.Height {
				m.vp.LineDown(1)
				m.follow = m.vp.AtBottom()
			}
			m.textSel.head = m.posAt(msg.X, msg.Y)
			if m.textSel.head != m.textSel.anchor {
				m.textSel.active = true
				m.follow = false
			}
		}
	case tea.MouseActionRelease:
		if m.dragBar {
			m.dragBar = false
			return nil
		}
		if !m.mouseDown {
			return nil
		}
		m.mouseDown = false
		if m.textSel != nil && m.textSel.active {
			text := m.selectedText()
			if strings.TrimSpace(text) == "" {
				m.textSel = nil
				return nil
			}
			n := strings.Count(text, "\n") + 1
			label := fmt.Sprintf("%d characters", len([]rune(text)))
			if n > 1 {
				label = fmt.Sprintf("%d lines", n)
			}
			return tea.Batch(copyToClipboard(text), m.notify(toastSuccess, "Copied "+label, truncate(firstLine(text), 60)))
		}
		// No drag: an ordinary click selects the entry under it.
		anchor := m.textSel.anchor
		m.textSel = nil
		m.clickEntry(anchor.line)
	}
	return nil
}
