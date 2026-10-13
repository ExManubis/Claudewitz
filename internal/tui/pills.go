package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// pill is a compact status chip shown above the input.
func pill(icon string, iconColor lipgloss.TerminalColor, text string) string {
	th := currentTheme()
	bg := th.selBg
	return lipgloss.NewStyle().Background(bg).Foreground(iconColor).Bold(true).Render(" "+icon+" ") +
		lipgloss.NewStyle().Background(bg).Foreground(th.text).Render(text+" ")
}

// pillsView shows queued messages, todo progress and, when the sidebar is
// hidden, running sub-agents. Empty when there's nothing to show.
func (m Model) pillsView(width int) string {
	th := currentTheme()
	var pills []string

	if n := len(m.queued); n > 0 {
		label := fmt.Sprintf("%d queued", n)
		first := firstLine(m.queued[0].text)
		if first == "" && len(m.queued[0].images) > 0 {
			first = "▣ " + m.queued[0].images[0]
		}
		if first != "" {
			label += " · " + truncate(first, 30)
		}
		pills = append(pills, pill("▲", th.second, label))
	}

	if todos := m.track.todos; len(todos) > 0 {
		done, active := 0, ""
		for _, t := range todos {
			switch t.status {
			case "completed":
				done++
			case "in_progress":
				if active == "" {
					active = firstNonEmpty(t.active, t.content)
				}
			}
		}
		if done < len(todos) {
			label := fmt.Sprintf("%d/%d", done, len(todos))
			if active != "" {
				label += " · " + truncate(active, 40)
			}
			pills = append(pills, pill("☐", th.ok, label))
		}
	}

	if !m.showSidebar() {
		if n := m.track.runningAgents(); n > 0 {
			label := fmt.Sprintf("%d agent", n)
			if n > 1 {
				label += "s"
			}
			pills = append(pills, pill("◐", th.second, label))
		}
	}

	if len(pills) == 0 {
		return ""
	}
	// Drop pills from the end until the row fits.
	for len(pills) > 0 && lipgloss.Width(" "+strings.Join(pills, " ")) > width {
		pills = pills[:len(pills)-1]
	}
	return " " + strings.Join(pills, " ")
}
