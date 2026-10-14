package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/aymanbagabas/go-udiff"
	tea "github.com/charmbracelet/bubbletea"
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
	return parseUnified(udiff.Unified("a", "b", old, new))
}

// parseUnified reads a unified diff of one file. Everything before the
// first hunk (git's diff, index and ---/+++ headers) is skipped.
func parseUnified(u string) []diffLine {
	var out []diffLine
	var o, n int
	inHunk := false
	for _, l := range strings.Split(u, "\n") {
		switch {
		case strings.HasPrefix(l, "@@"):
			o, n = parseHunk(l)
			if len(out) > 0 {
				out = append(out, diffLine{kind: '@', text: "⋯"})
			}
			inHunk = true
		case !inHunk, l == "":
			continue
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

// splitDiffMinWidth is the narrowest diff that auto mode draws side by side.
const splitDiffMinWidth = 120

// diffMode is "auto" (split when wide enough), "split" or "unified".
var diffMode = "auto"

var diffModes = []string{"auto", "split", "unified"}

var diffModeDesc = map[string]string{
	"auto":    "side by side when there's room",
	"split":   "always side by side",
	"unified": "always one column",
}

func applyDiffMode(mode string) {
	diffMode = "auto"
	if _, ok := diffModeDesc[mode]; ok {
		diffMode = mode
	}
}

// setDiffMode changes and saves the diff layout; with no mode it reports
// the current one.
func (m *Model) setDiffMode(mode string) tea.Cmd {
	if mode == "" {
		return m.notify(toastInfo, "Diffs: "+diffMode, diffModeDesc[diffMode])
	}
	if _, ok := diffModeDesc[mode]; !ok {
		return m.notify(toastError, "Unknown diff layout "+mode, strings.Join(diffModes, ", "))
	}
	applyDiffMode(mode)
	m.chat.markAllDirty()
	m.refresh()
	m.cfg.Diff = mode
	if mode == "auto" {
		m.cfg.Diff = ""
	}
	if err := m.cfg.Save(); err != nil {
		return m.notify(toastError, "Couldn't save setting", err.Error())
	}
	return m.notify(toastSuccess, "Diffs: "+mode, diffModeDesc[mode])
}

// toggleDiffLayout flips what a diff of this width shows: split if it's
// unified now, unified if it's split.
func (m *Model) toggleDiffLayout(width int) tea.Cmd {
	if splitDiff(width) {
		return m.setDiffMode("unified")
	}
	return m.setDiffMode("split")
}

// isDiffTool is true for the tools whose input is shown as a diff.
func isDiffTool(name string) bool {
	return name == "Edit" || name == "MultiEdit" || name == "Write"
}

// splitDiff reports whether a diff of this width is drawn side by side.
func splitDiff(width int) bool {
	switch diffMode {
	case "split":
		return width >= 40
	case "unified":
		return false
	}
	return width >= splitDiffMinWidth
}

// renderDiff draws diff lines with line numbers, +/- backgrounds and syntax
// highlighting chosen from the file name, side by side when splitDiff says
// so. max limits the number of rows.
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
	cell := func(l *diffLine, no, w int) string { return diffCell(l, no, numW, w, lexer, style) }

	var rows []string
	if splitDiff(width) {
		leftW := (width - 1) / 2
		rightW := width - 1 - leftW
		for _, r := range pairDiff(lines) {
			if r.left != nil && r.left.kind == '@' {
				rows = append(rows, cell(r.left, 0, leftW)+" "+cell(r.left, 0, rightW))
				continue
			}
			left, right := strings.Repeat(" ", leftW), strings.Repeat(" ", rightW)
			if r.left != nil {
				left = cell(r.left, r.left.oldNo, leftW)
			}
			if r.right != nil {
				right = cell(r.right, r.right.newNo, rightW)
			}
			rows = append(rows, left+" "+right)
		}
	} else {
		for i := range lines {
			l := &lines[i]
			no := l.newNo
			if l.kind == '-' {
				no = l.oldNo
			}
			rows = append(rows, cell(l, no, width))
		}
	}

	more := 0
	if max > 0 && len(rows) > max {
		rows, more = rows[:max], len(rows)-max
	}
	out := strings.Join(rows, "\n")
	if more > 0 {
		out += "\n" + lipgloss.NewStyle().Foreground(th.muted).Render(fmt.Sprintf("%s… %d more lines (ctrl+o to expand)", strings.Repeat(" ", numW+1), more))
	}
	return out
}

// diffCell draws one diff line in width cells: line number, sign, code.
func diffCell(l *diffLine, no, numW, width int, lexer chroma.Lexer, style *chroma.Style) string {
	th := currentTheme()
	if l.kind == '@' {
		return lipgloss.NewStyle().Foreground(th.muted).Width(width).MaxWidth(width).Render(strings.Repeat(" ", numW+1) + l.text)
	}
	var bg lipgloss.TerminalColor = lipgloss.NoColor{}
	sign := " "
	signStyle := lipgloss.NewStyle().Foreground(th.muted)
	switch l.kind {
	case '+':
		bg, sign = th.addBg, "+"
		signStyle = lipgloss.NewStyle().Foreground(th.ok)
	case '-':
		bg, sign = th.delBg, "-"
		signStyle = lipgloss.NewStyle().Foreground(th.err)
	}
	gutter := lipgloss.NewStyle().Foreground(th.muted).Background(bg).Render(fmt.Sprintf("%*d ", numW, no))
	signS := signStyle.Background(bg).Bold(true).Render(sign + " ")
	return gutter + signS + highlightLine(lexer, style, expandTabs(l.text), bg, width-numW-3)
}

// splitRow is one row of a side-by-side diff; a nil side is left blank.
type splitRow struct{ left, right *diffLine }

// pairDiff lines up a unified diff in two columns: context on both sides,
// and each run of removals beside the additions that follow it.
func pairDiff(lines []diffLine) []splitRow {
	var rows []splitRow
	for i := 0; i < len(lines); {
		l := &lines[i]
		switch l.kind {
		case '-', '+':
			var dels, adds []*diffLine
			for ; i < len(lines) && lines[i].kind == '-'; i++ {
				dels = append(dels, &lines[i])
			}
			for ; i < len(lines) && lines[i].kind == '+'; i++ {
				adds = append(adds, &lines[i])
			}
			for j := 0; j < max(len(dels), len(adds)); j++ {
				var r splitRow
				if j < len(dels) {
					r.left = dels[j]
				}
				if j < len(adds) {
					r.right = adds[j]
				}
				rows = append(rows, r)
			}
		default: // context and hunk separators
			rows = append(rows, splitRow{l, l})
			i++
		}
	}
	return rows
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
