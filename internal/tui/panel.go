package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Every diff that opens over the screen (an edit waiting for approval, an
// edit from the transcript, the changes viewer) uses one panel: centred,
// most of the width, a hatched title, the diff, and a line of hints.

// panelWidth is the panel's outer width on a screen this wide.
func panelWidth(screen int) int {
	return max(40, min(screen-4, max(80, screen*9/10)))
}

// panelTitle is the accent title, then hatching to the edge.
func panelTitle(title, extra string, innerW int) string {
	th := currentTheme()
	head := lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render(title) + extra
	return head + " " + stripes(max(0, innerW-lipgloss.Width(head)-1), th.accent, th.subtle)
}

// panelHints renders "key desc · key desc"; an empty key shows just the
// description (a scroll position, say).
func panelHints(pairs [][2]string) string {
	var hl []string
	for _, h := range pairs {
		hl = append(hl, strings.TrimSpace(statusKey.Render(h[0])+" "+mutedStyle.Render(h[1])))
	}
	return strings.Join(hl, mutedStyle.Render(" · "))
}

// panelFrame draws the rounded accent box around content lines.
func panelFrame(boxW, boxH int, lines []string) string {
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(currentTheme().accent).Padding(0, 1).Width(boxW - 2).Height(boxH - 2)
	return box.Render(strings.Join(lines, "\n"))
}

// layoutHint names what t switches the diff to.
func layoutHint(width int) string {
	if splitDiff(width) {
		return "unified view"
	}
	return "split view"
}

// scrollHint adds how far through rows the view reaches, when it overflows.
func scrollHint(pairs [][2]string, rows, from, height int) [][2]string {
	if rows > height {
		pairs = append(pairs, [2]string{"", fmt.Sprintf("%d%%", 100*min(rows, from+height)/rows)})
	}
	return pairs
}
