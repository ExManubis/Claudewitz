package tui

import (
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	colorful "github.com/lucasb-eyer/go-colorful"
)

type toastKind int

const (
	toastInfo toastKind = iota
	toastSuccess
	toastWarn
	toastError
)

const (
	toastWidth   = 44
	toastMax     = 4
	toastSlideIn = 220 * time.Millisecond
	toastFadeOut = 450 * time.Millisecond
	toastFPS     = 30
)

type toast struct {
	kind  toastKind
	title string
	body  string
	born  time.Time
	ttl   time.Duration
}

type toastTickMsg struct{ id int }

func toastTick(id int) tea.Cmd {
	return tea.Tick(time.Second/toastFPS, func(time.Time) tea.Msg { return toastTickMsg{id} })
}

// notify shows a toast. Errors and warnings linger longer.
func (m *Model) notify(kind toastKind, title, body string) tea.Cmd {
	ttl := 3500 * time.Millisecond
	switch kind {
	case toastWarn:
		ttl = 6 * time.Second
	case toastError:
		ttl = 8 * time.Second
	}
	// Replace an identical toast instead of stacking duplicates.
	for i, t := range m.toasts {
		if t.title == title && t.kind == kind {
			m.toasts = append(m.toasts[:i], m.toasts[i+1:]...)
			break
		}
	}
	m.toasts = append(m.toasts, toast{kind: kind, title: title, body: body, born: time.Now(), ttl: ttl})
	if len(m.toasts) > toastMax {
		m.toasts = m.toasts[len(m.toasts)-toastMax:]
	}
	if m.toastTicking {
		return nil
	}
	m.toastTicking = true
	m.toastID++
	return toastTick(m.toastID)
}

// pruneToasts drops expired toasts and reports whether any remain.
func (m *Model) pruneToasts(now time.Time) bool {
	kept := m.toasts[:0]
	for _, t := range m.toasts {
		if now.Sub(t.born) < t.ttl {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
	return len(kept) > 0
}

func (k toastKind) style() (icon string, col lipgloss.AdaptiveColor) {
	th := currentTheme()
	switch k {
	case toastSuccess:
		return "✓", th.ok
	case toastWarn:
		return "▲", th.warn
	case toastError:
		return "✗", th.err
	}
	return "✻", th.accent
}

// backdrop approximates the terminal background, which fades blend into.
func backdrop() colorful.Color {
	if darkBG {
		c, _ := colorful.Hex("#1a1817")
		return c
	}
	c, _ := colorful.Hex("#fbfaf8")
	return c
}

// fade blends an adaptive colour toward the backdrop; t=1 is fully faded.
func fade(c lipgloss.AdaptiveColor, t float64) lipgloss.Color {
	return lipgloss.Color(resolve(c).BlendLab(backdrop(), t).Clamped().Hex())
}

// render draws one toast at animation time now. It returns the rendered
// box and how many cells it is still pushed right by the slide-in.
func (t toast) render(now time.Time) (string, int) {
	age := now.Sub(t.born)
	// Ease-out cubic slide from the right edge.
	slide := 0
	if age < toastSlideIn {
		p := float64(age) / float64(toastSlideIn)
		slide = int(math.Round(float64(toastWidth) * math.Pow(1-p, 3)))
	}
	f := 0.0
	if left := t.ttl - age; left < toastFadeOut {
		f = 1 - float64(left)/float64(toastFadeOut)
	}

	icon, col := t.kind.style()
	th := currentTheme()
	inner := toastWidth - 5 // border, accent bar, padding
	head := lipgloss.NewStyle().Foreground(fade(col, f)).Bold(true).Render(icon+" ") +
		lipgloss.NewStyle().Foreground(fade(th.text, f)).Bold(true).Render(truncate(t.title, inner-2))
	lines := []string{head}
	if t.body != "" {
		wrapped := lipgloss.NewStyle().Width(inner - 2).Render(t.body)
		for i, l := range strings.Split(wrapped, "\n") {
			if i == 2 {
				break
			}
			lines = append(lines, "  "+lipgloss.NewStyle().Foreground(fade(th.muted, f)).Render(l))
		}
	}
	// Accent bar on the left, gradient border elsewhere.
	bar := lipgloss.NewStyle().Foreground(fade(col, f)).Render("▌")
	for i, l := range lines {
		lines[i] = bar + " " + l + strings.Repeat(" ", max(0, inner-lipgloss.Width(l)))
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(fade(th.subtle, math.Max(f, 0))).
		Render(strings.Join(lines, "\n"))
	return box, slide
}

// drawToasts stacks live toasts at the top right of the chat column.
func (m Model) drawToasts(screen string) string {
	if len(m.toasts) == 0 {
		return screen
	}
	now := time.Now()
	right := m.mainWidth() - 1 // keep clear of the sidebar
	y := 0
	if m.showHeader() {
		y = 1
	}
	// Newest on top.
	for i := len(m.toasts) - 1; i >= 0; i-- {
		box, slide := m.toasts[i].render(now)
		w := lipgloss.Width(box)
		x := right - w + slide
		if visible := right - x; visible < w {
			// Clip the part still off the right edge while sliding in.
			var clipped []string
			for _, l := range strings.Split(box, "\n") {
				clipped = append(clipped, ansi.Truncate(l, max(0, visible), ""))
			}
			box = strings.Join(clipped, "\n")
		}
		if right-x > 0 {
			screen = overlay(screen, box, x, y)
		}
		y += lipgloss.Height(box)
	}
	return screen
}
