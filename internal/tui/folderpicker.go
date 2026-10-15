package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/sessions"
)

// folderPicker chooses the folder for a new session: folders with live
// sessions, recent projects, or a typed path.
type folderPicker struct {
	filter textinput.Model
	all    []string // live first, then recent, no duplicates
	shown  []string
	sel    int
}

type foldersLoadedMsg []string

// loadFolders lists the folders of past sessions, most recent first.
func loadFolders() tea.Cmd {
	return func() tea.Msg {
		var dirs []string
		for _, s := range sessions.ListAll() {
			if s.CWD != "" && !slices.Contains(dirs, s.CWD) {
				dirs = append(dirs, s.CWD)
			}
			if len(dirs) == 50 {
				break
			}
		}
		return foldersLoadedMsg(dirs)
	}
}

func newFolderPicker(live []string) *folderPicker {
	ti := textinput.New()
	ti.Placeholder = "type a folder or filter"
	ti.Prompt = icons.search + " "
	ti.Focus()
	p := &folderPicker{filter: ti}
	for _, d := range live {
		if !slices.Contains(p.all, d) {
			p.all = append(p.all, d)
		}
	}
	p.applyFilter()
	return p
}

func (p *folderPicker) setRecent(dirs []string) {
	for _, d := range dirs {
		if !slices.Contains(p.all, d) {
			p.all = append(p.all, d)
		}
	}
	p.applyFilter()
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// applyFilter keeps folders whose path contains the typed text; a typed
// path to an existing folder goes first.
func (p *folderPicker) applyFilter() {
	q := strings.TrimSpace(p.filter.Value())
	p.shown = p.shown[:0]
	if q != "" && (strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~")) {
		dir := filepath.Clean(expandHome(q))
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			p.shown = append(p.shown, dir)
		}
	}
	for _, d := range p.all {
		if (q == "" || strings.Contains(strings.ToLower(prettyPath(d)), strings.ToLower(q))) && !slices.Contains(p.shown, d) {
			p.shown = append(p.shown, d)
		}
	}
	p.sel = max(0, min(p.sel, len(p.shown)-1))
}

func (p *folderPicker) choice() string {
	if len(p.shown) == 0 {
		return ""
	}
	return p.shown[p.sel]
}

// completeDir extends a typed path to the longest prefix its folder's
// subfolders share, with a trailing slash when only one matches.
func completeDir(input string) string {
	path := expandHome(input)
	parent, prefix := filepath.Dir(path), filepath.Base(path)
	if strings.HasSuffix(input, "/") {
		parent, prefix = path, ""
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return input
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	switch len(names) {
	case 0:
		return input
	case 1:
		return filepath.Join(parent, names[0]) + "/"
	}
	common := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, common) {
			common = common[:len(common)-1]
		}
	}
	return filepath.Join(parent, common)
}

func (m *Model) openFolders() tea.Cmd {
	var live []string
	for _, s := range m.live {
		if s.CWD != "" {
			live = append(live, s.CWD)
		}
	}
	m.folders = newFolderPicker(live)
	m.input.Blur()
	return loadFolders()
}

func (m *Model) handleFoldersKey(k tea.KeyMsg) tea.Cmd {
	p := m.folders
	switch k.String() {
	case "esc", "ctrl+c":
		m.folders = nil
		m.input.Focus()
		return nil
	case "up", "ctrl+p":
		if len(p.shown) > 0 {
			p.sel = max(0, p.sel-1)
		}
		return nil
	case "down", "ctrl+n":
		if len(p.shown) > 0 {
			p.sel = min(len(p.shown)-1, p.sel+1)
		}
		return nil
	case "tab":
		p.filter.SetValue(completeDir(p.filter.Value()))
		p.filter.CursorEnd()
		p.applyFilter()
		return nil
	case "enter":
		dir := p.choice()
		m.folders = nil
		if dir == "" {
			m.input.Focus()
			return nil
		}
		return m.newSessionIn(dir)
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(k)
	p.applyFilter()
	return cmd
}

// newSessionIn starts a session in dir and focuses it.
func (m *Model) newSessionIn(dir string) tea.Cmd {
	opts := m.base
	opts.CWD, opts.Resume, opts.Continue = dir, "", false
	v := newSessionView(opts)
	m.views = append(m.views, v)
	return tea.Batch(m.focus(v), m.startCmd(v, opts))
}

func (p *folderPicker) view(width, height int) string {
	w := min(72, width-4)
	lines := []string{agentStyle.Render("New session in…"), p.filter.View(), ""}
	for i, d := range p.shown {
		if i == max(1, height-8) {
			break
		}
		row := "  " + prettyPath(d)
		if i == p.sel {
			row = selStyle.Render(truncate("❯ "+prettyPath(d), w-4))
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", mutedStyle.Render("enter start · tab complete · esc cancel"))
	return popupBox.Width(w).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
