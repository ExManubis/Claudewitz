package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/gitstatus"
)

// The changes viewer (ctrl+s, /changes, or a click on a file in the
// sidebar) shows the working tree's changed files beside the diff of the
// selected one against HEAD, drawn with the same renderer as edits in the
// chat. It follows the sidebar's git polling, so it stays live while
// Claude works.

// maxViewerDiffLines caps how much of a huge diff is drawn.
const maxViewerDiffLines = 5000

type changesView struct {
	root   string
	files  []gitstatus.File
	sel    int
	scroll int

	loadID  int // the latest diff request; older replies are dropped
	loading bool
	lines   []diffLine
	binary  bool
	err     string

	// render cache: rows of the selected diff at a width and layout
	rows     []string
	rowsW    int
	rowsMode string
}

type changeDiffMsg struct {
	id   int
	text string
	err  error
}

func (v *changesView) file() (gitstatus.File, bool) {
	if v.sel < 0 || v.sel >= len(v.files) {
		return gitstatus.File{}, false
	}
	return v.files[v.sel], true
}

// openChanges shows the viewer at path (relative to the repository root),
// or at the first changed file when path is empty or no longer changed.
func (m *Model) openChanges(path string) tea.Cmd {
	if !m.git.IsRepo {
		return m.notify(toastInfo, "Not a git repository", "the changes viewer reads git diffs")
	}
	if len(m.git.Files) == 0 {
		return m.notify(toastInfo, "No changes", "the working tree is clean")
	}
	v := &changesView{root: m.git.Root, files: m.git.Files}
	for i, f := range v.files {
		if f.Path == path {
			v.sel = i
		}
	}
	m.changes = v
	m.drawer = false
	m.input.Blur()
	return m.loadChange()
}

func (m *Model) closeChanges() {
	m.changes = nil
	if len(m.pending) == 0 && !m.chatFocus {
		m.input.Focus()
	}
}

// loadChange reads the selected file's diff in the background.
func (m *Model) loadChange() tea.Cmd {
	v := m.changes
	f, ok := v.file()
	if !ok {
		return nil
	}
	v.loadID++
	v.loading, v.err = true, ""
	id, ctx, root := v.loadID, m.ctx, v.root
	return func() tea.Msg {
		text, err := gitstatus.Diff(ctx, root, f)
		return changeDiffMsg{id: id, text: text, err: err}
	}
}

func (m *Model) applyChangeDiff(msg changeDiffMsg) {
	v := m.changes
	if v == nil || msg.id != v.loadID {
		return
	}
	v.loading, v.rows = false, nil
	v.lines, v.binary, v.err = nil, false, ""
	if msg.err != nil {
		v.err = msg.err.Error()
		return
	}
	v.lines = parseUnified(msg.text)
	v.binary = len(v.lines) == 0 && strings.Contains(msg.text, "Binary files")
	if n := len(v.lines); n > maxViewerDiffLines {
		v.lines = append(v.lines[:maxViewerDiffLines], diffLine{kind: '@', text: fmt.Sprintf("… %d more lines not shown", n-maxViewerDiffLines)})
	}
	v.scroll = min(v.scroll, max(0, len(v.lines)-1))
}

// refreshChanges takes a new git snapshot: the list follows it, keeping
// the selected file, and the diff reloads when that file changed.
func (m *Model) refreshChanges() tea.Cmd {
	v := m.changes
	if v == nil || !m.git.IsRepo {
		return nil
	}
	old, hadOld := v.file()
	if len(m.git.Files) == 0 {
		v.files, v.sel, v.lines, v.rows = nil, 0, nil, nil
		return nil
	}
	v.root, v.files, v.sel = m.git.Root, m.git.Files, 0
	for i, f := range v.files {
		if hadOld && f.Path == old.Path {
			v.sel = i
		}
	}
	if cur, _ := v.file(); !hadOld || cur != old {
		return m.loadChange()
	}
	return nil
}

func (m *Model) selectChange(d int) tea.Cmd {
	v := m.changes
	if len(v.files) == 0 {
		return nil
	}
	v.sel = (v.sel + d + len(v.files)) % len(v.files)
	v.scroll = 0
	return m.loadChange()
}

// changesLayout sizes the viewer: the box, the file list beside the diff
// (dropped when narrow), and how many diff rows fit.
func (m Model) changesLayout() (boxW, boxH, listW, diffW, bodyH int) {
	boxW, boxH = max(30, m.width), max(8, m.height-1)
	innerW := boxW - 4 // border and padding
	if innerW >= 90 {
		listW = min(34, innerW/4)
	}
	diffW = innerW
	if listW > 0 {
		diffW -= listW + 3 // a gap, the rule, a gap
	}
	bodyH = boxH - 2 - 2 // border, then the title and a blank row
	return
}

// diffRows renders the selected diff, cached per width and layout.
func (v *changesView) diffRows(width int) []string {
	if v.rows != nil && v.rowsW == width && v.rowsMode == diffMode {
		return v.rows
	}
	f, _ := v.file()
	v.rows = strings.Split(renderDiff(v.lines, f.Path, width, 0), "\n")
	if len(v.lines) == 0 {
		v.rows = nil
	}
	v.rowsW, v.rowsMode = width, diffMode
	return v.rows
}

// scrollChanges moves the diff by d rows, clamped to its length.
func (m *Model) scrollChanges(d int) {
	_, _, _, diffW, bodyH := m.changesLayout()
	v := m.changes
	v.scroll = max(0, min(v.scroll+d, len(v.diffRows(diffW))-bodyH))
}

func (m *Model) handleChangesKey(k tea.KeyMsg) tea.Cmd {
	_, _, _, diffW, bodyH := m.changesLayout()
	switch k.String() {
	case "esc", "q", "ctrl+s", "ctrl+c":
		m.closeChanges()
	case "j", "down":
		m.scrollChanges(1)
	case "k", "up":
		m.scrollChanges(-1)
	case "d", "ctrl+d":
		m.scrollChanges(bodyH / 2)
	case "u", "ctrl+u":
		m.scrollChanges(-bodyH / 2)
	case "f", " ", "pgdown":
		m.scrollChanges(bodyH)
	case "b", "pgup":
		m.scrollChanges(-bodyH)
	case "g", "home":
		m.changes.scroll = 0
	case "G", "end":
		m.scrollChanges(len(m.changes.lines) * 2)
	case "tab", "J", "]", "right", "l":
		return m.selectChange(1)
	case "shift+tab", "K", "[", "left", "h":
		return m.selectChange(-1)
	case "t":
		cmd := m.toggleDiffLayout(diffW)
		m.scrollChanges(0)
		return cmd
	case "e":
		return m.editChange()
	}
	return nil
}

func (m *Model) handleChangesMouse(msg tea.MouseMsg) tea.Cmd {
	ev := tea.MouseEvent(msg)
	switch {
	case ev.Button == tea.MouseButtonWheelUp:
		m.scrollChanges(-3)
	case ev.Button == tea.MouseButtonWheelDown:
		m.scrollChanges(3)
	case ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft:
		// Clicks on the file list pick a file: inside the border and
		// padding, under the title and a blank row.
		_, _, listW, _, bodyH := m.changesLayout()
		row := msg.Y - 3
		if listW > 0 && msg.X >= 2 && msg.X < 2+listW && row >= 0 && row < bodyH {
			if i := m.changes.listOffset(bodyH) + row; i < len(m.changes.files) && i != m.changes.sel {
				m.changes.sel, m.changes.scroll = i, 0
				return m.loadChange()
			}
		}
	}
	return nil
}

// listOffset is the first file shown, keeping the selection in view.
func (v *changesView) listOffset(rows int) int {
	return max(0, min(v.sel-rows/2, len(v.files)-rows))
}

type changeEditedMsg struct{ err error }

// editChange opens the selected file in $VISUAL/$EDITOR.
func (m *Model) editChange() tea.Cmd {
	f, ok := m.changes.file()
	if !ok || f.Code() == 'D' {
		return nil
	}
	argv := editorCommand()
	if argv == nil {
		return m.notify(toastError, "No editor found", "set $EDITOR, e.g. export EDITOR=nano")
	}
	cmd := exec.Command(argv[0], append(argv[1:], filepath.Join(m.changes.root, f.Path))...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return changeEditedMsg{err} })
}

// changesStats is "+3 -1", "new", "bin" or "" for a file.
func changesStats(f gitstatus.File) string {
	switch {
	case f.Binary:
		return mutedStyle.Render("bin")
	case f.Untracked():
		return mutedStyle.Render("new")
	}
	var parts []string
	if f.Added > 0 {
		parts = append(parts, okStyle.Render(fmt.Sprintf("+%d", f.Added)))
	}
	if f.Removed > 0 {
		parts = append(parts, errStyle.Render(fmt.Sprintf("-%d", f.Removed)))
	}
	return strings.Join(parts, " ")
}

// changesView draws the viewer box. Paths are relative to the repository
// root, as git shows them.
func (m Model) changesView() string {
	v := m.changes
	th := currentTheme()
	boxW, boxH, listW, diffW, bodyH := m.changesLayout()
	innerW := boxW - 4
	f, _ := v.file()

	// Title: status, path and stats on the left; position and layout right.
	var title string
	if len(v.files) == 0 {
		title = okStyle.Render(icons.clean + " the working tree is clean")
	} else {
		name := f.Path
		if f.Orig != "" {
			name = f.Orig + " → " + name
		}
		left := gitCodeStyle(f.Code()).Bold(true).Render(string(f.Code())) + " " + textStyle.Bold(true).Render(name)
		if s := changesStats(f); s != "" {
			left += "  " + s
		}
		layout := "unified"
		if splitDiff(diffW) {
			layout = "split"
		}
		pos := fmt.Sprintf("%d/%d · %s", v.sel+1, len(v.files), layout)
		if rows := v.diffRows(diffW); len(rows) > bodyH {
			pos = fmt.Sprintf("%d%% · ", 100*min(len(rows), v.scroll+bodyH)/len(rows)) + pos
		}
		right := mutedStyle.Render(pos)
		left = truncate(left, max(10, innerW-lipgloss.Width(right)-2))
		title = left + strings.Repeat(" ", max(1, innerW-lipgloss.Width(left)-lipgloss.Width(right))) + right
	}

	// The diff, or why there isn't one.
	var body []string
	switch {
	case v.loading && v.rows == nil:
		body = []string{mutedStyle.Render("Loading diff…")}
	case v.err != "":
		body = []string{errStyle.Render("✗ " + truncate(v.err, diffW-2))}
	case v.binary:
		body = []string{mutedStyle.Render("Binary file; no text diff to show")}
	case len(v.files) > 0 && len(v.lines) == 0:
		body = []string{mutedStyle.Render("No content changes (mode or rename only)")}
	default:
		rows := v.diffRows(diffW)
		from := min(v.scroll, len(rows))
		body = rows[from:min(len(rows), from+bodyH)]
	}
	for len(body) < bodyH {
		body = append(body, "")
	}
	diffCol := lipgloss.NewStyle().Width(diffW).MaxWidth(diffW).Render(strings.Join(body, "\n"))

	content := diffCol
	if listW > 0 {
		var list []string
		off := v.listOffset(bodyH)
		for i := off; i < len(v.files) && len(list) < bodyH; i++ {
			lf := v.files[i]
			mark := " "
			if m.track.fileIdx[filepath.Join(v.root, lf.Path)] != nil {
				mark = assistLabel.Render("✻")
			}
			stats := changesStats(lf)
			lead, nameStyle := " ", textStyle
			if i == v.sel {
				lead, nameStyle = lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render("▌"), lipgloss.NewStyle().Foreground(th.accent).Bold(true)
			}
			prefix := lead + gitCodeStyle(lf.Code()).Render(string(lf.Code())) + mark + " "
			nameW := listW - lipgloss.Width(prefix) - lipgloss.Width(stats) - 1
			name := truncateLeft(lf.Path, max(4, nameW))
			gap := listW - lipgloss.Width(prefix) - lipgloss.Width(name) - lipgloss.Width(stats)
			list = append(list, prefix+nameStyle.Render(name)+strings.Repeat(" ", max(1, gap))+stats)
		}
		for len(list) < bodyH {
			list = append(list, "")
		}
		listCol := lipgloss.NewStyle().Width(listW).MaxWidth(listW).Render(strings.Join(list, "\n"))
		sep := lipgloss.NewStyle().Foreground(th.subtle).Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
		content = lipgloss.JoinHorizontal(lipgloss.Top, listCol, " ", sep, " ", diffCol)
	}

	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(th.accent).Padding(0, 1).Width(boxW - 2).Height(boxH - 2)
	return box.Render(title + "\n\n" + content)
}
