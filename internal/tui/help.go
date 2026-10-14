package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpLine renders "key desc • key desc" pairs, dropping trailing pairs
// that don't fit in width.
func helpLine(pairs [][2]string, width int) string {
	sep := mutedStyle.Render(" • ")
	var out string
	for _, p := range pairs {
		item := statusKey.Render(p[0]) + " " + mutedStyle.Render(p[1])
		next := item
		if out != "" {
			next = out + sep + item
		}
		if lipgloss.Width(next) > width {
			break
		}
		out = next
	}
	return out
}

// helpPairs are the hints for whatever currently has focus.
func (m Model) helpPairs() [][2]string {
	switch {
	case m.showHelp:
		return [][2]string{{"esc", "close"}}
	case m.themePick != nil:
		return [][2]string{{"↑↓", "theme"}, {"←→", "appearance"}, {"enter", "save"}, {"esc", "cancel"}}
	case m.modelPick != nil:
		return [][2]string{{"↑↓", "model"}, {"←→", "effort"}, {"enter", "apply"}, {"esc", "cancel"}}
	case m.palette != nil:
		return [][2]string{{"↑↓", "move"}, {"enter", "run"}, {"esc", "close"}}
	case m.picker != nil:
		return [][2]string{{"↑↓", "move"}, {"enter", "resume"}, {"tab", "scope"}, {"esc", "close"}}
	case m.question != nil || m.plan != nil:
		return m.interactionHelp()
	case len(m.pending) > 0:
		pairs := [][2]string{{"y", "allow"}, {"a", "always"}, {"n", "deny"}, {"ctrl+c", "deny & stop"}}
		if isDiffTool(m.pending[0].ToolName) {
			pairs = append(pairs, [2]string{"t", "diff layout"})
		}
		return pairs
	case m.comp.active:
		return [][2]string{{"↑↓", "select"}, {"tab", "accept"}, {"esc", "dismiss"}}
	}
	// The rest follows Crush's help bar, trimmed: focus, mode and commands,
	// the chat keys when the transcript has focus, then help.
	var out [][2]string
	if m.busy {
		out = append(out, [2]string{"esc", "interrupt"})
	}
	focus := [2]string{"tab", "focus chat"}
	if m.chatFocus {
		focus = [2]string{"tab", "focus editor"}
	}
	commands := [2]string{"ctrl+p", "commands"}
	if !m.chatFocus && m.input.Value() == "" {
		commands = [2]string{"/ or ctrl+p", "commands"}
	}
	out = append(out, focus, [2]string{"shift+tab", "mode"}, commands)
	if m.chatFocus {
		out = append(out, [2]string{"↑↓", "select"}, [2]string{"space", "expand/collapse"}, [2]string{"d/u", "page"}, [2]string{"y", "copy"})
	}
	return append(out, [2]string{"ctrl+g", "more"})
}

var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"Prompt", [][2]string{
		{"enter", "send message"},
		{"ctrl+j", "insert newline"},
		{"/", "slash commands"},
		{"@", "mention a file"},
		{"tab", "accept completion"},
		{"↑/↓", "prompt history"},
		{"→", "take suggested prompt"},
		{"ctrl+x", "edit in $EDITOR"},
		{"ctrl+f", "attach image"},
		{"ctrl+v", "paste image or text"},
		{"ctrl+c", "clear input"},
	}},
	{"Session", [][2]string{
		{"esc", "interrupt Claude"},
		{"shift+tab", "cycle permission mode"},
		{"ctrl+r", "resume a session"},
		{"ctrl+t", "model & effort"},
		{"ctrl+p", "command palette"},
		{"ctrl+d", "quit"},
	}},
	{"View", [][2]string{
		{"ctrl+o", "expand tool output"},
		{"ctrl+b", "toggle sidebar"},
		{"pgup/pgdn", "scroll"},
		{"ctrl+l", "redraw"},
		{"ctrl+g", "this help"},
	}},
	{"Transcript (tab)", [][2]string{
		{"j/k ↑↓", "select message"},
		{"space/enter", "expand / collapse"},
		{"y", "copy message"},
		{"d/u f/b", "half / full page"},
		{"g/G", "top / bottom"},
		{"click", "select, again to expand"},
		{"tab/esc", "back to input"},
	}},
	{"Permissions", [][2]string{
		{"y", "allow once"},
		{"a", "always allow tool"},
		{"n", "deny"},
		{"ctrl+c", "deny and stop"},
	}},
}

// helpDialog is the full keyboard reference.
func helpDialog(width int) string {
	colW := 34
	cols := 2
	if width >= colW*4+8 {
		cols = 4
	}
	var blocks []string
	for _, s := range helpSections {
		lines := []string{sectionTitle.MarginTop(0).Render(s.title)}
		for _, k := range s.keys {
			key := lipgloss.NewStyle().Width(11).Render(statusKey.Render(k[0]))
			lines = append(lines, key+textStyle.Render(k[1]))
		}
		blocks = append(blocks, lipgloss.NewStyle().Width(colW).MarginBottom(1).Render(strings.Join(lines, "\n")))
	}
	var rows []string
	for i := 0; i < len(blocks); i += cols {
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, blocks[i:min(i+cols, len(blocks))]...))
	}
	title := gradientText("Keyboard shortcuts", true)
	body := title + "\n\n" + strings.Join(rows, "\n") + "\n" + mutedStyle.Render("esc to close")
	return dialogBox.Render(body)
}
