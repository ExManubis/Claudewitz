package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type editorDoneMsg struct {
	path string
	err  error
}

// editorCommand is $VISUAL, then $EDITOR, then the first common editor
// found. It may carry arguments, e.g. "code --wait".
func editorCommand() []string {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(v)); len(f) > 0 {
			return f
		}
	}
	for _, e := range []string{"nvim", "vim", "nano", "vi"} {
		if _, err := exec.LookPath(e); err == nil {
			return []string{e}
		}
	}
	return nil
}

// openEditor suspends the UI and edits the prompt in an external editor.
func (m *Model) openEditor() tea.Cmd {
	argv := editorCommand()
	if argv == nil {
		return m.notify(toastError, "No editor found", "set $EDITOR, e.g. export EDITOR=nano")
	}
	f, err := os.CreateTemp("", "claudewitz-prompt-*.md")
	if err != nil {
		return m.notify(toastError, "Couldn't open editor", err.Error())
	}
	f.WriteString(m.input.Value())
	f.Close()
	cmd := exec.Command(argv[0], append(argv[1:], f.Name())...)
	path := f.Name()
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return editorDoneMsg{path: path, err: err} })
}

func (m *Model) editorDone(msg editorDoneMsg) tea.Cmd {
	defer os.Remove(msg.path)
	if msg.err != nil {
		return m.notify(toastError, "Editor exited with an error", msg.err.Error())
	}
	b, err := os.ReadFile(msg.path)
	if err != nil {
		return m.notify(toastError, "Couldn't read the edited prompt", err.Error())
	}
	m.input.SetValue(strings.TrimRight(string(b), "\n"))
	m.input.CursorEnd()
	m.comp.update(m)
	m.layout()
	return nil
}
