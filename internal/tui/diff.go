package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/aymanbagabas/go-udiff"
	"github.com/charmbracelet/lipgloss"
)

type diffLine struct {
	kind  byte // ' ', '+', '-', '@'
	text  string
	oldNo int
	newNo int
}

// computeDiff returns unified-diff lines (3 lines of context) for old→new.
func computeDiff(old, new string) []diffLine {
	if !strings.HasSuffix(old, "\n") {
		old += "\n"
	}
	if !strings.HasSuffix(new, "\n") {
		new += "\n"
	}
	u := udiff.Unified("a", "b", old, new)
	var out []diffLine
	var o, n int
	for _, l := range strings.Split(u, "\n") {
		switch {
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"), l == "":
			continue
		case strings.HasPrefix(l, "@@"):
			o, n = parseHunk(l)
			if len(out) > 0 {
				out = append(out, diffLine{kind: '@', text: "⋯"})
			}
		case l[0] == '+':
			out = append(out, diffLine{kind: '+', text: l[1:], newNo: n})
			n++
		case l[0] == '-':
			out = append(out, diffLine{kind: '-', text: l[1:], oldNo: o})
			o++
		case l[0] == '\\':
			continue
		default:
			out = append(out, diffLine{kind: ' ', text: l[1:], oldNo: o, newNo: n})
			o++
			n++
		}
	}
	return out
}

func parseHunk(h string) (int, int) {
	// @@ -a,b +c,d @@
	var o, n int
	for _, f := range strings.Fields(h) {
		if len(f) > 1 && (f[0] == '-' || f[0] == '+') {
			num, _ := strconv.Atoi(strings.SplitN(f[1:], ",", 2)[0])
			if f[0] == '-' {
				o = num
			} else {
				n = num
			}
		}
	}
	return o, n
}

// addedLines renders a new file as an all-additions diff.
func addedLines(content string) []diffLine {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	out := make([]diffLine, len(lines))
	for i, l := range lines {
		out[i] = diffLine{kind: '+', text: l, newNo: i + 1}
	}
	return out
}

func diffStats(lines []diffLine) (add, del int) {
	for _, l := range lines {
		switch l.kind {
		case '+':
			add++
		case '-':
			del++
		}
	}
	return
}

// renderDiff draws diff lines with line numbers, +/- backgrounds and syntax
// highlighting chosen from the file name. max limits the number of lines.
func renderDiff(lines []diffLine, filename string, width, max int) string {
	if len(lines) == 0 {
		return ""
	}
	th := currentTheme()
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get(th.chroma)

	numW := 1
	for _, l := range lines {
		for _, x := range []int{l.oldNo, l.newNo} {
			if w := len(strconv.Itoa(x)); w > numW {
				numW = w
			}
		}
	}
	shown := lines
	more := 0
	if max > 0 && len(lines) > max {
		shown, more = lines[:max], len(lines)-max
	}

	var b strings.Builder
	for i, l := range shown {
		if i > 0 {
			b.WriteByte('\n')
		}
		var bg lipgloss.TerminalColor = lipgloss.NoColor{}
		sign := " "
		no := l.newNo
		signStyle := lipgloss.NewStyle().Foreground(th.muted)
		switch l.kind {
		case '+':
			bg, sign = th.addBg, "+"
			signStyle = lipgloss.NewStyle().Foreground(th.ok)
		case '-':
			bg, sign, no = th.delBg, "-", l.oldNo
			signStyle = lipgloss.NewStyle().Foreground(th.err)
		case '@':
			b.WriteString(lipgloss.NewStyle().Foreground(th.muted).Render(strings.Repeat(" ", numW+1) + l.text))
			continue
		}
		gutter := lipgloss.NewStyle().Foreground(th.muted).Background(bg).Render(fmt.Sprintf("%*d ", numW, no))
		signS := signStyle.Background(bg).Bold(true).Render(sign + " ")
		used := numW + 3
		code := highlightLine(lexer, style, expandTabs(l.text), bg, width-used)
		b.WriteString(gutter + signS + code)
	}
	if more > 0 {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(th.muted).Render(fmt.Sprintf("%s… %d more lines (ctrl+o to expand)", strings.Repeat(" ", numW+1), more)))
	}
	return b.String()
}

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

// highlightLine tokenises one line and paints each token over bg, padding
// (or truncating) to exactly width cells.
func highlightLine(lexer chroma.Lexer, style *chroma.Style, text string, bg lipgloss.TerminalColor, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(text)
	if len(runes) > width {
		text = string(runes[:width-1]) + "…"
	}
	var b strings.Builder
	used := 0
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		b.WriteString(lipgloss.NewStyle().Background(bg).Render(text))
		used = lipgloss.Width(text)
	} else {
		for _, tok := range it.Tokens() {
			v := strings.TrimRight(tok.Value, "\n")
			if v == "" {
				continue
			}
			st := lipgloss.NewStyle().Background(bg)
			e := style.Get(tok.Type)
			if e.Colour.IsSet() {
				st = st.Foreground(lipgloss.Color(e.Colour.String()))
			}
			if e.Bold == chroma.Yes {
				st = st.Bold(true)
			}
			if e.Italic == chroma.Yes {
				st = st.Italic(true)
			}
			b.WriteString(st.Render(v))
			used += lipgloss.Width(v)
		}
	}
	if pad := width - used; pad > 0 {
		b.WriteString(lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", pad)))
	}
	return b.String()
}
