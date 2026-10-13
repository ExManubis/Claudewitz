package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/config"
)

var appearances = []string{"auto", "dark", "light"}

// themePicker previews themes live; esc restores what was there before.
type themePicker struct {
	sel            int
	origTheme      string
	origAppearance string
}

func (m *Model) openThemePicker() {
	p := &themePicker{origTheme: currentTheme().name, origAppearance: appearance}
	for i, t := range themes {
		if t.name == p.origTheme {
			p.sel = i
		}
	}
	m.themePick = p
	m.input.Blur()
}

func (m *Model) closeThemePicker() {
	m.themePick = nil
	if len(m.pending) == 0 {
		m.input.Focus()
	}
}

// restyle pushes the active theme into everything that caches styles.
func (m *Model) restyle() {
	m.spin.Style = lipgloss.NewStyle().Foreground(currentTheme().accent)
	m.input.FocusedStyle.Placeholder = mutedStyle
	m.input.BlurredStyle.Placeholder = mutedStyle
	m.chat.markAllDirty()
	m.refresh()
}

// saveTheme persists the active theme and appearance.
func (m *Model) saveTheme() tea.Cmd {
	m.cfg.Theme = currentTheme().name
	m.cfg.Appearance = appearance
	if appearance == "auto" {
		m.cfg.Appearance = ""
	}
	if err := m.cfg.Save(); err != nil {
		return m.notify(toastError, "Couldn't save theme", err.Error())
	}
	return m.notify(toastSuccess, "Theme: "+currentTheme().name, "saved to "+prettyPath(config.Path()))
}

func (m *Model) handleThemePickerKey(k tea.KeyMsg) tea.Cmd {
	p := m.themePick
	switch k.String() {
	case "esc", "ctrl+c", "q":
		setAppearance(p.origAppearance)
		applyTheme(p.origTheme)
		m.closeThemePicker()
		m.restyle()
	case "up", "k", "ctrl+p", "shift+tab":
		p.sel = (p.sel - 1 + len(themes)) % len(themes)
		applyTheme(themes[p.sel].name)
		m.restyle()
	case "down", "j", "ctrl+n", "tab":
		p.sel = (p.sel + 1) % len(themes)
		applyTheme(themes[p.sel].name)
		m.restyle()
	case "left", "h", "right", "l":
		d := 1
		if s := k.String(); s == "left" || s == "h" {
			d = -1
		}
		i := 0
		for j, a := range appearances {
			if a == appearance {
				i = j
			}
		}
		setAppearance(appearances[(i+d+len(appearances))%len(appearances)])
		m.restyle()
	case "enter":
		m.closeThemePicker()
		return m.saveTheme()
	}
	return nil
}

// swatches shows a theme's key colours as blocks.
func swatches(t theme) string {
	var b strings.Builder
	for _, c := range []ac{t.accent, t.second, t.user, t.ok, t.warn, t.err} {
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("██"))
	}
	return b.String()
}

const previewCode = "func parse(k Kind) (*Node, error) {\n\treturn nil, errEmpty\n}"
const previewNew = "func parse(k Kind) (*Node, error) {\n\treturn &Node{Kind: k}, nil // 42\n}"

func (p *themePicker) view(width int) string {
	w := min(width-4, 76)
	inner := w - 6
	cur := currentTheme()
	lines := []string{gradientText("Theme", true), ""}

	for i, t := range themes {
		mark := "  "
		if t.name == p.origTheme {
			mark = okStyle.Render("● ")
		}
		name := lipgloss.NewStyle().Width(12).Render(t.name)
		desc := truncate(t.desc, inner-2-12-13-2)
		if i == p.sel {
			row := selStyle.Render(name) + " " + swatches(t) + " " + selStyle.Bold(false).Render(" "+desc)
			lines = append(lines, mark+row)
		} else {
			lines = append(lines, mark+textStyle.Render(name)+" "+swatches(t)+" "+mutedStyle.Render(" "+desc))
		}
	}

	// Appearance switch.
	var segs []string
	for _, a := range appearances {
		label := " " + a + " "
		if a == appearance {
			segs = append(segs, lipgloss.NewStyle().Background(cur.accent).Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#111111"}).Bold(true).Render(label))
		} else {
			segs = append(segs, mutedStyle.Render(label))
		}
	}
	detected := "light"
	if detectedBG {
		detected = "dark"
	}
	lines = append(lines, "",
		sectionTitle.MarginTop(0).Render("Appearance")+"  "+strings.Join(segs, "")+mutedStyle.Render("  terminal looks "+detected))

	// A small sample of what the chat looks like in this theme.
	lines = append(lines, "", mutedStyle.Render(strings.Repeat("─", inner)))
	lines = append(lines,
		assistLabel.Render("✻ ")+gradientText("Preview", true)+" "+stripes(max(0, inner-12), cur.accent, cur.subtle),
		userBlock.Render(textStyle.Render("Make parse return a node")),
		"  "+okStyle.Render("●")+" "+toolName.Render("Edit")+" "+mutedStyle.Render("parser.go ")+okStyle.Render("+1")+" "+errStyle.Render("-1"),
		indent(renderDiff(computeDiff(previewCode, previewNew), "parser.go", inner-4, 6), "    "),
		"  "+okStyle.Render("✓ tests pass")+mutedStyle.Render("  ·  ")+warnStyle.Render("▲ 82% of 5-hour")+mutedStyle.Render("  ·  ")+errStyle.Render("✗ 1 conflict")+mutedStyle.Render("  ·  ")+agentStyle.Render("Explore"),
	)

	lines = append(lines, "", helpLine([][2]string{{"↑↓", "theme"}, {"←→", "appearance"}, {"enter", "save"}, {"esc", "cancel"}}, inner))
	return dialogBox.Width(w).Render(strings.Join(lines, "\n"))
}
