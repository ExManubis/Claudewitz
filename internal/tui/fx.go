package tui

import (
	"math"
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	colorful "github.com/lucasb-eyer/go-colorful"
)

// resolve picks the light or dark variant of an adaptive colour.
func resolve(c lipgloss.AdaptiveColor) colorful.Color {
	hex := c.Dark
	if !darkBG {
		hex = c.Light
	}
	col, err := colorful.Hex(hex)
	if err != nil {
		return colorful.Color{R: 0.8, G: 0.5, B: 0.4}
	}
	return col
}

// gradient returns n colours blended through the given stops.
func gradient(n int, stops ...lipgloss.AdaptiveColor) []lipgloss.Color {
	if n <= 0 {
		return nil
	}
	cs := make([]colorful.Color, len(stops))
	for i, s := range stops {
		cs[i] = resolve(s)
	}
	out := make([]lipgloss.Color, n)
	for i := range out {
		if len(cs) == 1 || n == 1 {
			out[i] = lipgloss.Color(cs[0].Hex())
			continue
		}
		t := float64(i) / float64(n-1) * float64(len(cs)-1)
		seg := min(int(t), len(cs)-2)
		out[i] = lipgloss.Color(cs[seg].BlendLab(cs[seg+1], t-float64(seg)).Clamped().Hex())
	}
	return out
}

// gradientText colours each rune of s along accent → second.
func gradientText(s string, bold bool) string {
	th := currentTheme()
	runes := []rune(s)
	cols := gradient(len(runes), th.accent, th.second)
	var b strings.Builder
	for i, r := range runes {
		st := lipgloss.NewStyle().Foreground(cols[i])
		if bold {
			st = st.Bold(true)
		}
		b.WriteString(st.Render(string(r)))
	}
	return b.String()
}

// stripes renders a run of diagonal hatching fading from a to b.
func stripes(n int, a, b lipgloss.AdaptiveColor) string {
	cols := gradient(n, a, b)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(lipgloss.NewStyle().Foreground(cols[i]).Render("╱"))
	}
	return sb.String()
}

// ── Shimmer: the animated "working" indicator ──────────────────────────

const (
	shimmerFPS   = 20
	shimmerCells = 10
	scrambleSet  = "0123456789abcdefABCDEF~!@#$%^&*+=<>?/\\|░▒▓"
)

type animTickMsg struct{ id int }

func animTick(id int) tea.Cmd {
	return tea.Tick(time.Second/shimmerFPS, func(time.Time) tea.Msg { return animTickMsg{id} })
}

// shimmer renders scrambling glyphs over a moving gradient followed by the
// label, whose letters are lit by a travelling highlight.
func shimmer(frame int, label string) string {
	th := currentTheme()
	// A long cyclic palette accent → second → accent we slide a window over.
	pal := gradient(32, th.accent, th.second, th.accent)
	var b strings.Builder
	set := []rune(scrambleSet)
	for i := 0; i < shimmerCells; i++ {
		c := pal[(i*2+frame)%len(pal)]
		r := set[rand.Intn(len(set))]
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
	}
	b.WriteString(" ")

	runes := []rune(label)
	base := resolve(th.muted)
	hi := resolve(th.text)
	// Highlight sweeps across the label and pauses past its end.
	period := len(runes) + 12
	pos := float64(frame/2%period) - 3
	for i, r := range runes {
		d := math.Abs(float64(i) - pos)
		t := math.Max(0, 1-d/3)
		col := base.BlendLab(hi, t).Clamped().Hex()
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Bold(t > 0.6).Render(string(r)))
	}
	dots := []string{"   ", ".  ", ".. ", "..."}[frame/6%4]
	b.WriteString(mutedStyle.Render(dots))
	return b.String()
}

// ── Logo ────────────────────────────────────────────────────────────────

var glyphs = map[rune][3]string{
	'C': {"█▀▀", "█  ", "▀▀▀"},
	'L': {"█  ", "█  ", "▀▀▀"},
	'A': {"█▀█", "█▀█", "▀ ▀"},
	'U': {"█ █", "█ █", "▀▀▀"},
	'D': {"█▀▄", "█ █", "▀▀ "},
	'E': {"█▀▀", "█▀▀", "▀▀▀"},
	'W': {"█ █ █", "█ █ █", "▀▀▀▀▀"},
	'I': {"█", "█", "▀"},
	'T': {"▀█▀", " █ ", " ▀ "},
	'Z': {"▀▀█", "▄▀ ", "▀▀▀"},
}

// bigLogo renders word in block letters with a horizontal gradient and
// diagonal stripes trailing off to the right, up to width cells.
func bigLogo(word string, width int) string {
	rows := [3]string{}
	for i, r := range word {
		g, ok := glyphs[r]
		if !ok {
			continue
		}
		for j := range rows {
			if i > 0 {
				rows[j] += " "
			}
			rows[j] += g[j]
		}
	}
	w := len([]rune(rows[0]))
	th := currentTheme()
	cols := gradient(w, th.accent, th.second)
	var out []string
	for j, row := range rows {
		var b strings.Builder
		for i, r := range []rune(row) {
			b.WriteString(lipgloss.NewStyle().Foreground(cols[i]).Render(string(r)))
		}
		// Offset each row so the hatching reads as a slanted band.
		if tail := width - w - 2 - (2 - j); tail > 3 {
			b.WriteString(strings.Repeat(" ", 2+(2-j)))
			b.WriteString(stripes(min(tail, 40), th.second, th.subtle))
		}
		out = append(out, b.String())
	}
	return strings.Join(out, "\n")
}

// headerLine is the compact one-line brand strip shown above the chat when
// the sidebar is hidden.
func headerLine(width int, right string) string {
	th := currentTheme()
	left := assistLabel.Render("✻ ") + gradientText("CLAUDEWITZ", true) + " "
	r := " " + right
	fill := width - lipgloss.Width(left) - lipgloss.Width(r) - 1
	if fill < 3 {
		return ansi.Truncate(left, width, "")
	}
	return " " + left + stripes(fill, th.accent, th.subtle) + r
}

// ── Overlay ─────────────────────────────────────────────────────────────

// overlay draws fg on top of bg with its top-left corner at (x, y),
// preserving the background on either side.
func overlay(bg, fg string, x, y int) string {
	bl := strings.Split(bg, "\n")
	for i, l := range strings.Split(fg, "\n") {
		row := y + i
		if row < 0 || row >= len(bl) {
			continue
		}
		base := bl[row]
		if pad := x - ansi.StringWidth(base); pad > 0 {
			base += strings.Repeat(" ", pad)
		}
		left := ansi.Truncate(base, x, "")
		right := ansi.TruncateLeft(base, x+ansi.StringWidth(l), "")
		bl[row] = left + "\x1b[0m" + l + "\x1b[0m" + right
	}
	return strings.Join(bl, "\n")
}

// center places fg in the middle of bg (width × height).
func center(bg, fg string, width, height int) string {
	fw, fh := lipgloss.Width(fg), lipgloss.Height(fg)
	return overlay(bg, fg, max(0, (width-fw)/2), max(0, (height-fh)/3))
}
