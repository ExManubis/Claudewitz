package tui

import (
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// selectable reports whether entry i can be selected; info lines (turn
// stats, "interrupted") are skipped.
func (c *chat) selectable(i int) bool {
	return i >= 0 && i < len(c.entries) && c.entries[i].kind != kindInfo
}

// step moves the selection by d selectable entries, clamping at the ends.
func (c *chat) step(d int) {
	i := c.sel
	for {
		i += d
		if i < 0 || i >= len(c.entries) {
			return
		}
		if c.selectable(i) {
			c.sel = i
			return
		}
	}
}

func (c *chat) selectEdge(last bool) {
	if last {
		for i := len(c.entries) - 1; i >= 0; i-- {
			if c.selectable(i) {
				c.sel = i
				return
			}
		}
		return
	}
	for i := range c.entries {
		if c.selectable(i) {
			c.sel = i
			return
		}
	}
}

// entryAt returns the selectable entry covering a content line, or -1.
func (c *chat) entryAt(line int) int {
	for i := range c.offsets {
		if line >= c.offsets[i] && line < c.offsets[i]+c.heights[i] {
			if c.selectable(i) {
				return i
			}
			return -1
		}
	}
	return -1
}

// firstVisible returns the first selectable entry starting at or below
// the given line (or the last one when none do).
func (c *chat) firstVisible(top int) int {
	for i := range c.offsets {
		if c.offsets[i]+c.heights[i] > top && c.selectable(i) {
			return i
		}
	}
	return c.sel
}

// copyText is what y copies for an entry: the source, not the rendering.
func copyText(e *entry) string {
	switch e.kind {
	case kindTool:
		var in map[string]any
		_ = json.Unmarshal(e.toolInput, &in)
		var head string
		switch e.toolName {
		case "Bash":
			head = "$ " + str(in, "command")
		case "Edit", "MultiEdit", "Write", "Read", "NotebookEdit":
			head = filePathOf(e.toolInput)
			if s := str(in, "new_string"); s != "" {
				return head + "\n\n" + s
			}
			if s := str(in, "content"); s != "" {
				return head + "\n\n" + s
			}
		case toolExitPlan:
			return firstNonEmpty(e.planText, str(in, "plan"))
		default:
			b, _ := json.MarshalIndent(in, "", "  ")
			head = e.toolName + " " + string(b)
		}
		if strings.TrimSpace(e.toolOutput) == "" {
			return head
		}
		return head + "\n\n" + e.toolOutput
	}
	return e.text
}

func (m *Model) focusChat() {
	if len(m.chat.entries) == 0 {
		return
	}
	m.chatFocus = true
	m.chat.focused = true
	if !m.chat.selectable(m.chat.sel) {
		m.chat.selectEdge(true)
	}
	m.input.Blur()
	m.scrollToSel = true
	m.refresh()
}

func (m *Model) blurChat() {
	m.chatFocus = false
	m.chat.focused = false
	if len(m.pending) == 0 && !m.chatFocus {
		m.input.Focus()
	}
	m.refresh()
}

// handleChatKey drives the focused transcript. Keys it doesn't use fall
// through to the global bindings; plain typing returns to the input.
func (m *Model) handleChatKey(k tea.KeyMsg) (tea.Cmd, bool) {
	// Keys that arrive in one read (fast typing, key repeat) come as a
	// single multi-rune event; handle them one at a time.
	if k.Type == tea.KeyRunes && len(k.Runes) > 1 && !k.Paste {
		var cmds []tea.Cmd
		for i, r := range k.Runes {
			if !m.chatFocus {
				// Left the transcript part-way: the rest is typing.
				rest := tea.KeyMsg{Type: tea.KeyRunes, Runes: k.Runes[i:]}
				var c tea.Cmd
				m.input, c = m.input.Update(rest)
				return tea.Batch(append(cmds, c)...), true
			}
			cmd, handled := m.handleChatKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			if !handled {
				var c tea.Cmd
				m.input, c = m.input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: k.Runes[i:]})
				return tea.Batch(append(cmds, cmd, c)...), true
			}
			cmds = append(cmds, cmd)
		}
		return tea.Batch(cmds...), true
	}
	c := m.chat
	moved := func() {
		m.follow = false
		m.scrollToSel = true
		m.refresh()
	}
	switch k.String() {
	case "tab", "esc", "i":
		m.blurChat()
		return nil, true
	case "j", "down":
		c.step(1)
		moved()
	case "k", "up":
		c.step(-1)
		moved()
	case "g", "home":
		c.selectEdge(false)
		moved()
	case "G", "end":
		c.selectEdge(true)
		m.follow = true
		m.refresh()
	case " ", "enter":
		return m.toggleSelected(), true
	case "d", "u", "f", "b", "pgdown", "pgup":
		// Page the view and carry the selection along.
		switch k.String() {
		case "d":
			m.vp.HalfViewDown()
		case "u":
			m.vp.HalfViewUp()
		case "f", "pgdown":
			m.vp.ViewDown()
		default:
			m.vp.ViewUp()
		}
		c.sel = c.firstVisible(m.vp.YOffset)
		m.follow = m.vp.AtBottom()
		m.refresh()
	case "y", "c":
		if !c.selectable(c.sel) {
			return nil, true
		}
		text := copyText(c.entries[c.sel])
		return tea.Batch(copyToClipboard(text), m.notify(toastSuccess, "Copied", truncate(firstLine(text), 60))), true
	default:
		if len(k.Runes) > 0 && k.Type == tea.KeyRunes && !k.Alt {
			m.blurChat()
			return nil, false // type it into the input
		}
		return nil, false
	}
	return nil, true
}

func (m *Model) toggleSelected() tea.Cmd {
	c := m.chat
	if !c.selectable(c.sel) {
		return nil
	}
	e := c.entries[c.sel]
	e.expanded = !e.expanded
	e.dirty = true
	m.follow = false
	m.scrollToSel = true
	m.refresh()
	return nil
}

// ensureSelVisible scrolls just enough to show the selected entry, or its
// top when it's taller than the view.
func (m *Model) ensureSelVisible() {
	c := m.chat
	if !c.selectable(c.sel) || c.sel >= len(c.offsets) {
		return
	}
	top, h := c.offsets[c.sel], c.heights[c.sel]
	switch {
	case top < m.vp.YOffset || h > m.vp.Height:
		m.vp.SetYOffset(top)
	case top+h > m.vp.YOffset+m.vp.Height:
		m.vp.SetYOffset(top + h - m.vp.Height)
	}
}

// clickEntry selects the entry covering a transcript line; clicking the
// selected entry again expands it.
func (m *Model) clickEntry(line int) {
	i := m.chat.entryAt(line)
	if i < 0 {
		return
	}
	if i == m.chat.sel && m.chatFocus {
		m.toggleSelected()
		return
	}
	m.chat.sel = i
	m.chatFocus = true
	m.chat.focused = true
	m.input.Blur()
	m.follow = false
	m.refresh()
}
