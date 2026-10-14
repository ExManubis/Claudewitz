package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// When Claude Code asks to edit or write a file, the request opens as a
// panel in the middle of the screen, Crush style: what's asking, the file,
// the whole diff (scrollable, with real line numbers where the file lets
// us find them) and buttons to answer. Other permission requests keep the
// compact prompt above the input. Whether a request is made at all is up
// to Claude Code's permission mode: in accept-edits mode edits go through
// without asking.

// editPanel is the state of an open edit-permission panel.
type editPanel struct {
	req    *claude.PermissionRequest
	lines  []diffLine
	note   string // under the file name: "new file", "3 edits", …
	scroll int
	focus  int // index into editButtons

	// render cache, and where the buttons landed, for clicks
	rows     []string
	rowsW    int
	rowsMode string
	buttonsY int
	buttonsX [][2]int
}

type editButton struct {
	label string
	key   int        // index of the underlined hotkey in label, or -1
	press tea.KeyMsg // the prompt key it stands for
}

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

var editButtons = []editButton{
	{"Yes, allow", 0, runeKey('y')},
	{"Allow for session", 0, runeKey('a')},
	{"No, deny", 0, runeKey('n')},
	{"Deny & stop", -1, tea.KeyMsg{Type: tea.KeyCtrlC}},
}

// maxPanelFileBytes bounds the files read to place a diff in its file.
const maxPanelFileBytes = 4 << 20

func readSmallFile(path string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() > maxPanelFileBytes {
		return "", false
	}
	b, err := os.ReadFile(path)
	return string(b), err == nil
}

// lineOf is the 0-based line where s first occurs in content, or 0.
func lineOf(content, s string) int {
	if i := strings.Index(content, s); i > 0 && s != "" {
		return strings.Count(content[:i], "\n")
	}
	return 0
}

// newEditPanel opens the panel for a request.
func newEditPanel(p *claude.PermissionRequest) *editPanel {
	lines, note := editDiff(p.ToolName, p.Input, true)
	return &editPanel{req: p, lines: lines, note: note}
}

// editDiff works out the diff an Edit, MultiEdit or Write makes, with a
// header per hunk. Edits are placed in the file so their line numbers are
// the file's: by where the old text is, before the edit lands, or where
// the new text is, after. A Write over an existing file diffs against
// what's there, but only for a call that hasn't run (pending): in history
// the file may have changed since.
func editDiff(tool string, raw json.RawMessage, pending bool) (lines []diffLine, note string) {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	cur, exists := readSmallFile(filePathOf(raw))
	// place shifts one edit's lines into the file; delta is how many lines
	// earlier edits in the same call added.
	place := func(d []diffLine, old, new string, delta int) {
		if !exists {
			return
		}
		if strings.Contains(cur, old) && old != "" {
			off := lineOf(cur, old)
			shiftLines(d, off, off+delta)
		} else if strings.Contains(cur, new) && new != "" {
			off := lineOf(cur, new)
			shiftLines(d, off-delta, off)
		}
	}
	switch tool {
	case "Edit":
		old, new := str(in, "old_string"), str(in, "new_string")
		lines = computeDiff(old, new)
		place(lines, old, new, 0)
		if in["replace_all"] == true {
			note = "every occurrence"
		}
	case "MultiEdit":
		edits, _ := in["edits"].([]any)
		delta := 0
		for i, ed := range edits {
			e, _ := ed.(map[string]any)
			old, new := str(e, "old_string"), str(e, "new_string")
			d := computeDiff(old, new)
			place(d, old, new, delta)
			a, r := diffStats(d)
			delta += a - r
			if i > 0 {
				lines = append(lines, diffLine{kind: '@', text: "⋯"})
			}
			lines = append(lines, d...)
		}
		note = fmt.Sprintf("%d edits", len(edits))
	case "Write":
		content := str(in, "content")
		switch {
		case pending && exists && strings.TrimRight(cur, "\n") != strings.TrimRight(content, "\n"):
			lines, note = computeDiff(cur, content), "overwrites the file"
		case exists || !pending:
			lines = addedLines(content) // what it replaced is gone
		default:
			lines, note = addedLines(content), "new file"
		}
	}
	return hunkHeaders(lines), note
}

// panelLayout sizes the panel: most of the width, and tall enough for the
// diff up to the room above the waiting line under the chat.
func (m Model) panelLayout() (boxW, boxH, innerW, bodyH int) {
	boxW = max(40, min(m.width-4, max(80, m.width*9/10)))
	innerW = boxW - 4 // border and padding
	// border 2, title + blank, details + blank, blank + buttons, blank + hints
	fixed := 2 + 2 + len(m.panelDetails(innerW)) + 1 + 2 + 2
	room := max(3, m.height-1-panelBelow-fixed)
	bodyH = max(3, min(room, len(m.editPanel.panelRows(innerW))))
	return boxW, bodyH + fixed, innerW, bodyH
}

// panelBelow is the rows kept clear under the panel for the waiting line.
const panelBelow = 4

// panelDetails are the label/value rows under the title.
func (m Model) panelDetails(w int) []string {
	p := m.pending[0]
	ep := m.editPanel
	label := func(k, v string) string {
		return mutedStyle.Render(fmt.Sprintf("%-5s ", k)) + textStyle.Render(truncateLeft(v, max(10, w-6)))
	}
	rows := []string{label("Tool", p.ToolName)}
	if p.AgentID != "" {
		for _, a := range m.track.agents {
			if a.taskID == p.AgentID {
				rows = append(rows, mutedStyle.Render("Agent ")+agentStyle.Render(truncate(a.kind+": "+a.desc, w-6)))
			}
		}
	}
	file := label("File", relPath(filePathOf(p.Input), m.cwd()))
	if ep.note != "" {
		file += mutedStyle.Render("  " + ep.note)
	}
	a, r := diffStats(ep.lines)
	file += "  " + okStyle.Render(fmt.Sprintf("+%d", a)) + " " + errStyle.Render(fmt.Sprintf("-%d", r))
	return append(rows, file)
}

// panelRows renders the request's diff, cached per width and layout.
func (ep *editPanel) panelRows(width int) []string {
	if ep.rows == nil || ep.rowsW != width || ep.rowsMode != diffMode {
		ep.rows = strings.Split(renderDiff(ep.lines, filePathOf(ep.req.Input), width, 0), "\n")
		ep.rowsW, ep.rowsMode = width, diffMode
	}
	return ep.rows
}

func (m *Model) scrollPanel(d int) {
	_, _, innerW, bodyH := m.panelLayout()
	ep := m.editPanel
	ep.scroll = max(0, min(ep.scroll+d, len(ep.panelRows(innerW))-bodyH))
}

// handlePanelKey drives the edit panel. Answers go through the same keys
// as the compact prompt; it reports false for keys it leaves to it.
func (m *Model) handlePanelKey(k tea.KeyMsg) (tea.Cmd, bool) {
	ep := m.editPanel
	_, _, _, bodyH := m.panelLayout()
	switch k.String() {
	case "left", "shift+tab", "h":
		ep.focus = (ep.focus + len(editButtons) - 1) % len(editButtons)
	case "right", "tab", "l":
		ep.focus = (ep.focus + 1) % len(editButtons)
	case "enter", " ":
		return m.handlePermissionKey(editButtons[ep.focus].press), true
	case "t":
		_, _, innerW, _ := m.panelLayout()
		cmd := m.toggleDiffLayout(innerW)
		m.scrollPanel(0)
		return cmd, true
	case "j", "down", "shift+down":
		m.scrollPanel(1)
	case "k", "up", "shift+up":
		m.scrollPanel(-1)
	case "d", "pgdown", "ctrl+d":
		m.scrollPanel(bodyH / 2)
	case "u", "pgup", "ctrl+u":
		m.scrollPanel(-bodyH / 2)
	case "g", "home":
		ep.scroll = 0
	case "G", "end":
		m.scrollPanel(len(ep.lines) * 2)
	default:
		return nil, false
	}
	return nil, true
}

func (m *Model) handlePanelMouse(msg tea.MouseMsg) tea.Cmd {
	ep := m.editPanel
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.scrollPanel(-3)
	case msg.Button == tea.MouseButtonWheelDown:
		m.scrollPanel(3)
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y == ep.buttonsY:
		for i, x := range ep.buttonsX {
			if msg.X >= x[0] && msg.X < x[1] {
				ep.focus = i
				return m.handlePermissionKey(editButtons[i].press)
			}
		}
	}
	return nil
}

// panelButton draws a button, filled when focused, its hotkey underlined.
func panelButton(b editButton, focused bool) string {
	th := currentTheme()
	st := lipgloss.NewStyle().Background(th.selBg).Foreground(th.text)
	if focused {
		st = lipgloss.NewStyle().Background(th.accent).Foreground(lipgloss.Color("#ffffff")).Bold(true)
	}
	if b.key < 0 {
		return st.Render(" " + b.label + " ")
	}
	r := []rune(b.label)
	return st.Render(" "+string(r[:b.key])) + st.Underline(true).Render(string(r[b.key])) + st.Render(string(r[b.key+1:])+" ")
}

// panelView draws the edit panel; x and y are where it will be placed, so
// the buttons can be found again for clicks.
func (m Model) panelView(x, y int) string {
	th := currentTheme()
	ep := m.editPanel
	boxW, boxH, innerW, bodyH := m.panelLayout()

	head := lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render("Permission Required")
	if n := len(m.pending) - 1; n > 0 {
		head += mutedStyle.Render(fmt.Sprintf("  +%d more", n))
	}
	head += " " + stripes(max(0, innerW-lipgloss.Width(head)-1), th.accent, th.subtle)

	rows := ep.panelRows(innerW)
	from := min(ep.scroll, len(rows))
	body := append([]string(nil), rows[from:min(len(rows), from+bodyH)]...)
	for len(body) < bodyH {
		body = append(body, "")
	}

	var buttons []string
	for i, b := range editButtons {
		buttons = append(buttons, panelButton(b, i == ep.focus))
	}
	row := strings.Join(buttons, "  ")
	pad := max(0, innerW-lipgloss.Width(row))
	// Remember where each button sits on screen: inside the border and
	// padding, after the right-aligning pad.
	bx := x + 2 + pad
	ep.buttonsX = ep.buttonsX[:0]
	for _, b := range buttons {
		w := lipgloss.Width(b)
		ep.buttonsX = append(ep.buttonsX, [2]int{bx, bx + w})
		bx += w + 2
	}
	details := m.panelDetails(innerW)
	ep.buttonsY = y + 1 + 2 + len(details) + 1 + bodyH + 1

	layout := "split view"
	if splitDiff(innerW) {
		layout = "unified view"
	}
	hints := [][2]string{{"←→", "choose"}, {"enter", "confirm"}, {"t", layout}, {"j/k", "scroll"}}
	if len(rows) > bodyH {
		hints = append(hints, [2]string{"", fmt.Sprintf("%d%%", 100*min(len(rows), from+bodyH)/len(rows))})
	}
	var hl []string
	for _, h := range hints {
		hl = append(hl, strings.TrimSpace(statusKey.Render(h[0])+" "+mutedStyle.Render(h[1])))
	}

	content := []string{head, ""}
	content = append(content, details...)
	content = append(content, "")
	content = append(content, body...)
	content = append(content, "", strings.Repeat(" ", pad)+row, "", strings.Join(hl, mutedStyle.Render(" · ")))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(th.accent).Padding(0, 1).Width(boxW - 2).Height(boxH - 2)
	return box.Render(strings.Join(content, "\n"))
}

// panelPos is where the panel sits: centred over the screen, above the
// waiting line and the status bar.
func (m Model) panelPos() (int, int) {
	boxW, boxH, _, _ := m.panelLayout()
	return max(0, (m.width-boxW)/2), max(0, (m.height-1-panelBelow-boxH)/2)
}

// panelWaiting replaces the prompt under the chat while the panel is open.
func (m Model) panelWaiting() string {
	p := m.pending[0]
	line := warnStyle.Bold(true).Render("Allow "+p.ToolName+"?") + " " +
		mutedStyle.Render(truncateLeft(relPath(filePathOf(p.Input), m.cwd()), max(10, m.mainWidth()/2))+" · answer in the panel above")
	return permBox.Width(m.mainWidth() - 2).Render(line)
}
