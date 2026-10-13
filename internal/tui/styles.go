package tui

import "github.com/charmbracelet/lipgloss"

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#C15F3C", Dark: "#DA7756"} // Claude orange
	colMuted  = lipgloss.AdaptiveColor{Light: "#7A7A7A", Dark: "#8A8A8A"}
	colUser   = lipgloss.AdaptiveColor{Light: "#2B6CB0", Dark: "#7AB8FF"}
	colOK     = lipgloss.AdaptiveColor{Light: "#2F855A", Dark: "#68D391"}
	colErr    = lipgloss.AdaptiveColor{Light: "#C53030", Dark: "#FC8181"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#B7791F", Dark: "#F6E05E"}

	userLabel     = lipgloss.NewStyle().Foreground(colUser).Bold(true)
	assistLabel   = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	mutedStyle    = lipgloss.NewStyle().Foreground(colMuted)
	thinkingStyle = lipgloss.NewStyle().Foreground(colMuted).Italic(true)
	toolName      = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	okStyle       = lipgloss.NewStyle().Foreground(colOK)
	errStyle      = lipgloss.NewStyle().Foreground(colErr)
	warnStyle     = lipgloss.NewStyle().Foreground(colWarn)

	statusBar = lipgloss.NewStyle().Foreground(colMuted).Padding(0, 1)
	statusKey = lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	inputBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colMuted).
			Padding(0, 1)
	inputBoxFocused = inputBox.BorderForeground(colAccent)

	permBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colWarn).
		Padding(0, 1)
)
