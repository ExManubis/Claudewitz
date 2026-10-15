package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

const (
	panelWidth   = 40  // the sessions panel, border included
	panelMinTerm = 140 // narrower terminals get the tab strip
	panelTop     = 2   // rows above the first session box: header, blank
	boxHeight    = 4   // a session box: border, title, folder, border
	panelFooter  = 3   // rows below the boxes: "+n more" (or blank), blank, hint
)

type sessionsMode int

const (
	sessionsNone sessionsMode = iota
	sessionsPanel
	sessionsTabs
)

// sessionsMode is how the daemon's sessions are shown: a panel, a tab strip
// when narrow, or not at all with fewer than two or without the daemon.
func (m Model) sessionsMode() sessionsMode {
	n := len(m.live)
	if _, ok := m.summary(m.sessionView.id); !ok {
		n++ // the focused view has no row (dead, or not started yet)
	}
	if m.daemon == nil || !m.panelOn || n < 2 {
		return sessionsNone
	}
	if m.termW >= panelMinTerm {
		return sessionsPanel
	}
	return sessionsTabs
}

// resize gives today's layout what the panel or strip leaves of the
// terminal.
func (m *Model) resize() {
	m.width, m.height = m.termW, m.termH
	m.panelMode = m.sessionsMode()
	switch m.panelMode {
	case sessionsPanel:
		m.width -= panelWidth
	case sessionsTabs:
		m.height--
	}
	if m.width >= sidebarMinWidth {
		m.drawer = false
	}
	m.layout()
	m.refresh()
}

func (m Model) rowGlyph(st rowState) (string, lipgloss.Style) {
	th := currentTheme()
	switch st {
	case stWaiting:
		return icons.sessWaiting, lipgloss.NewStyle().Foreground(th.warn).Bold(true)
	case stError:
		return icons.sessError, lipgloss.NewStyle().Foreground(th.err)
	case stWorking:
		return icons.sessWorking, lipgloss.NewStyle().Foreground(th.accent)
	case stDone:
		return icons.sessDone, lipgloss.NewStyle().Foreground(th.ok)
	}
	return icons.sessIdle, mutedStyle
}

func rowTitle(s daemon.SessionSummary) string {
	if t := firstLine(s.Title); t != "" {
		return t
	}
	return "new session"
}

// sessionsView draws the sessions panel: a header, then two lines per
// session, most urgent first.
func (m Model) sessionsView(height int) string {
	th := currentTheme()
	inner := panelWidth - 2 // the right border and a gutter
	head := lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render("SESSIONS")
	count := mutedStyle.Render(fmt.Sprint(len(m.live)))
	if n := m.needYou(); n > 0 {
		count = warnStyle.Render(fmt.Sprintf("▲ %d need you", n))
	}
	lines := []string{spread(head, count, inner), ""}
	rows := m.rows()
	first, shown := m.boxWindow(len(rows), height)
	for _, s := range rows[first : first+shown] {
		lines = append(lines, strings.Split(m.sessionBox(s, inner), "\n")...)
	}
	hidden := ""
	if n := len(rows) - shown; n > 0 {
		hidden = mutedStyle.Render(fmt.Sprintf("+%d more · alt+↑↓", n))
	}
	lines = append(lines, hidden, "", mutedStyle.Render("ctrl+] next waiting · ctrl+q kill"))
	for len(lines) < height {
		lines = append(lines, "")
	}
	body := strings.Join(lines[:min(len(lines), height)], "\n")
	return lipgloss.NewStyle().Width(panelWidth-1).Height(height).
		Border(lipgloss.NormalBorder(), false, true, false, false).BorderForeground(th.subtle).
		Render(body)
}

// sessionBox draws one session as a rounded box, width wide: glyph, title
// and time in its state, then its folder. The focused box has the accent
// border; the others the subtle one.
func (m Model) sessionBox(s daemon.SessionSummary, width int) string {
	th := currentTheme()
	st := stateOf(s, m.done[s.ID])
	glyph, gs := m.rowGlyph(st)
	focused := s.ID == m.sessionView.id
	titleStyle := textStyle
	switch st {
	case stWaiting:
		titleStyle = warnStyle.Bold(true)
	case stError:
		titleStyle = errStyle
	case stIdle:
		titleStyle = mutedStyle
	}
	if focused {
		titleStyle = titleStyle.Bold(true)
	}
	content := width - 4 // the box's borders and a space of padding each side
	age := mutedStyle.Render(ageLabel(time.Since(m.since[s.ID])))
	title := truncate(rowTitle(s), content-3-lipgloss.Width(age))
	l1 := spread(gs.Render(glyph)+" "+titleStyle.Render(title), age, content)
	l2 := "  " + lipgloss.NewStyle().Foreground(th.second).Render(truncateLeft(folderPath(s), content-2))
	border := th.subtle
	if focused {
		border = th.accent
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(width - 2).Render(l1 + "\n" + l2)
}

// boxWindow is which session boxes fit in a panel height rows tall: from
// first, shown of them, scrolled just enough to keep the focused one in
// view.
func (m Model) boxWindow(n, height int) (first, shown int) {
	fit := max(1, (height-panelTop-panelFooter)/boxHeight)
	shown = min(n, fit)
	at := -1
	for i, s := range m.rows() {
		if s.ID == m.sessionView.id {
			at = i
		}
	}
	if at >= shown {
		first = at - shown + 1
	}
	return first, shown
}

// spread puts left and right at the two ends of a width-wide line.
func spread(left, right string, width int) string {
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

// tabLabels are the tab strip's labels: the folder, with a short title when
// two tabs share it.
func (m Model) tabLabels(rows []daemon.SessionSummary) []string {
	labels := folderLabels(m.live)
	count := map[string]int{}
	for _, s := range rows {
		count[labels[s.ID]]++
	}
	out := make([]string, len(rows))
	for i, s := range rows {
		l := labels[s.ID]
		if count[l] > 1 {
			l += " · " + truncate(rowTitle(s), 14)
		}
		out[i] = l
	}
	return out
}

// tabStrip renders each tab and picks the first one shown, scrolled so the
// focused tab stays in view within width.
func (m Model) tabStrip(rows []daemon.SessionSummary, width int) (tabs []string, start int) {
	th := currentTheme()
	labels := m.tabLabels(rows)
	focusedAt := 0
	for i, s := range rows {
		st := stateOf(s, m.done[s.ID])
		glyph, gs := m.rowGlyph(st)
		style := mutedStyle
		switch st {
		case stWaiting:
			style = warnStyle.Bold(true)
		case stError:
			style = errStyle
		}
		tab := " " + gs.Render(glyph) + " " + style.Render(labels[i]) + " "
		if s.ID == m.sessionView.id {
			tab = lipgloss.NewStyle().Background(th.selBg).Underline(true).Render(" " + glyph + " " + labels[i] + " ")
			focusedAt = i
		}
		tabs = append(tabs, tab)
	}
	for start < focusedAt && lipgloss.Width(strings.Join(tabs[start:focusedAt+1], "")) > width {
		start++
	}
	return tabs, start
}

// tabsView draws the one-line tab strip, scrolled to keep the focused tab
// in view.
func (m Model) tabsView(width int) string {
	tabs, start := m.tabStrip(m.rows(), width)
	return ansi.Truncate(strings.Join(tabs[start:], ""), width, "…")
}

// sessionsMouse handles clicks on the panel or strip and shifts other mouse
// events into the coordinates today's layout expects.
func (m *Model) sessionsMouse(msg *tea.MouseMsg) (id string, handled bool) {
	click := msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft
	wheel := msg.Action == tea.MouseActionPress &&
		(msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown)
	switch m.panelMode {
	case sessionsPanel:
		if msg.X < panelWidth && (click || wheel) {
			rows := m.rows()
			first, shown := m.boxWindow(len(rows), m.termH)
			box := (msg.Y - panelTop) / boxHeight
			if click && msg.Y >= panelTop && box < shown {
				return rows[first+box].ID, true
			}
			return "", true
		}
		// Releases and motion go on to the chat, so a drag that ends over
		// the panel still finishes.
		msg.X = max(0, msg.X-panelWidth)
	case sessionsTabs:
		if msg.Y == 0 && (click || wheel) {
			if click {
				rows := m.rows()
				tabs, start := m.tabStrip(rows, m.termW)
				x := 0
				for i := start; i < len(tabs); i++ {
					x += lipgloss.Width(tabs[i])
					if msg.X < x {
						return rows[i].ID, true
					}
				}
			}
			return "", true
		}
		msg.Y = max(0, msg.Y-1)
	}
	return "", false
}

// modalOpen is true while a dialog, picker or viewer is over the chat; a
// click on a session row then doesn't switch sessions under it.
func (m Model) modalOpen() bool {
	return m.kill != nil || m.alwaysOnAsk != nil || m.folders != nil || m.picker != nil || m.palette != nil ||
		m.modelPick != nil || m.themePick != nil || m.showHelp || m.changes != nil
}
