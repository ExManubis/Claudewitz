package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/exmanubis/claudewitz/internal/claude"
)

const paletteRows = 12

type action struct {
	group string
	label string
	desc  string
	key   string
	run   func(m *Model) tea.Cmd
}

// palette is the ctrl+p command palette.
type palette struct {
	all    []action
	shown  []action
	filter textinput.Model
	sel    int
	offset int
}

func (m *Model) openPalette() {
	ti := textinput.New()
	ti.Placeholder = "Type a command, model, theme…"
	ti.Prompt = "❯ "
	ti.PromptStyle = statusKey
	ti.Focus()
	m.palette = &palette{all: m.actions(), filter: ti}
	m.palette.apply()
	m.input.Blur()
}

func (m *Model) closePalette() {
	m.palette = nil
	if len(m.pending) == 0 && m.picker == nil && !m.chatFocus {
		m.input.Focus()
	}
}

// copyToClipboard uses OSC 52, which most modern terminals (and tmux with
// set-clipboard on) honour, including over SSH.
func copyToClipboard(s string) tea.Cmd {
	return func() tea.Msg {
		termenv.Copy(s)
		return nil
	}
}

// actions lists everything the palette can do in the current state.
func (m *Model) actions() []action {
	var a []action
	add := func(group, label, desc, key string, run func(m *Model) tea.Cmd) {
		a = append(a, action{group, label, desc, key, run})
	}
	if m.busy {
		add("Session", "Interrupt", "stop the current turn", "esc", func(m *Model) tea.Cmd {
			if m.proc != nil {
				m.interrupt()
			}
			return nil
		})
	}
	if m.daemon != nil {
		add("Session", "New session…", "in this folder or any other", "alt+n", func(m *Model) tea.Cmd { return m.openFolders() })
	} else {
		add("Session", "New session", "start fresh in this directory", "/new", func(m *Model) tea.Cmd {
			return m.restart(claude.Options{})
		})
	}
	add("Session", "Resume session…", "browse saved sessions", "ctrl+r", func(m *Model) tea.Cmd {
		return m.openPicker()
	})
	if m.daemon != nil {
		add("Sessions", "Next session that needs you", "", "ctrl+]", func(m *Model) tea.Cmd {
			cmd, _ := m.handleSessionKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
			return cmd
		})
		add("Sessions", "Kill session", "end the focused session", "ctrl+q", func(m *Model) tea.Cmd { return m.openKill() })
		labels := folderLabels(m.live)
		for i, r := range m.rows() {
			id, keyHint := r.ID, ""
			if i < 9 {
				keyHint = fmt.Sprintf("alt+%d", i+1)
			}
			add("Sessions", "Switch to "+labels[id], rowTitle(r), keyHint, func(m *Model) tea.Cmd { return m.focusSession(id) })
		}
		add("Sessions", "Toggle sessions panel", "", "alt+s", func(m *Model) tea.Cmd {
			m.panelOn = !m.panelOn
			m.resize()
			return nil
		})
	}
	m.alwaysOnActions(add)
	add("Session", "Copy session id", m.sessionID, "", func(m *Model) tea.Cmd {
		if m.sessionID == "" {
			return nil
		}
		return tea.Batch(copyToClipboard(m.sessionID), m.notify(toastSuccess, "Copied session id", m.sessionID))
	})

	add("Model", "Switch model…", "pick a model and effort level", "ctrl+t", func(m *Model) tea.Cmd {
		m.openModelPicker()
		return nil
	})
	for _, l := range defaultEffortLevels {
		l := l
		cur := ""
		if l == m.opts.Effort {
			cur = "current"
		}
		add("Effort", l, effortDesc[l], cur, func(m *Model) tea.Cmd { cmd, _ := m.slash("/effort " + l); return cmd })
	}
	if m.initResp != nil && len(m.initResp.Models) > 0 {
		for _, mi := range m.initResp.Models {
			mi := mi
			cur := ""
			if mi.Value == m.opts.Model || (m.opts.Model == "" && mi.Value == "default") {
				cur = "current"
			}
			add("Model", mi.DisplayName, firstNonEmpty(mi.Description, mi.Value), cur, func(m *Model) tea.Cmd {
				cmd, _ := m.slash("/model " + mi.Value)
				return cmd
			})
		}
	} else {
		for _, v := range []string{"opus", "sonnet", "haiku"} {
			v := v
			add("Model", v, "", "", func(m *Model) tea.Cmd { cmd, _ := m.slash("/model " + v); return cmd })
		}
	}

	modeDesc := map[string]string{"default": "ask before edits and commands", "acceptEdits": "auto-approve file edits", "plan": "read-only planning"}
	for _, pm := range permissionModes {
		pm := pm
		cur := ""
		if pm == m.permMode {
			cur = "current"
		}
		add("Mode", pm, modeDesc[pm], cur, func(m *Model) tea.Cmd { return m.setPermissionMode(pm) })
	}

	add("Git", "Review changes…", "changed files as diffs against HEAD", "ctrl+s", func(m *Model) tea.Cmd {
		return m.openChanges("")
	})

	add("Theme", "Choose theme…", "browse themes with a live preview", "/theme", func(m *Model) tea.Cmd {
		m.openThemePicker()
		return nil
	})
	for _, t := range themeNames() {
		t := t
		cur := ""
		if t == currentTheme().name {
			cur = "current"
		}
		add("Theme", t, "", cur, func(m *Model) tea.Cmd { cmd, _ := m.slash("/theme " + t); return cmd })
	}

	add("Prompt", "Edit prompt in $EDITOR", "write long prompts in your editor", "ctrl+x", func(m *Model) tea.Cmd {
		return m.openEditor()
	})
	add("Prompt", "Attach image…", "pick an image file from the project", "ctrl+f", func(m *Model) tea.Cmd {
		m.input.SetValue("/image ")
		m.input.CursorEnd()
		m.comp.update(m)
		return nil
	})
	add("Prompt", "Paste image", "from the clipboard", "ctrl+v", func(m *Model) tea.Cmd { return readClipboardImage() })
	if len(m.attachments) > 0 {
		add("Prompt", "Remove attached images", fmt.Sprintf("%d attached", len(m.attachments)), "", func(m *Model) tea.Cmd {
			m.attachments = nil
			return m.notify(toastInfo, "Removed attachments", "")
		})
	}
	for _, mode := range notifyModes {
		mode := mode
		cur := ""
		if mode == firstNonEmpty(m.cfg.Notifications, "auto") {
			cur = "current"
		}
		add("Notifications", mode, notifyModeDesc[mode], cur, func(m *Model) tea.Cmd { return m.setNotifications(mode) })
	}
	for _, mode := range iconModes {
		mode := mode
		cur := ""
		if mode == firstNonEmpty(m.cfg.Icons, "nerd") {
			cur = "current"
		}
		add("Icons", mode, iconModeDesc[mode], cur, func(m *Model) tea.Cmd { return m.setIcons(mode) })
	}
	for _, mode := range diffModes {
		mode := mode
		cur := ""
		if mode == diffMode {
			cur = "current"
		}
		add("Diffs", mode, diffModeDesc[mode], cur, func(m *Model) tea.Cmd { return m.setDiffMode(mode) })
	}
	add("View", "Toggle sidebar", "", "ctrl+b", func(m *Model) tea.Cmd {
		return m.toggleSidebar()
	})
	add("View", "Expand tool output", "show full tool results and diffs", "ctrl+o", func(m *Model) tea.Cmd {
		m.chat.expanded = !m.chat.expanded
		m.chat.markAllDirty()
		return nil
	})
	add("View", "Keyboard shortcuts", "", "ctrl+g", func(m *Model) tea.Cmd { m.showHelp = true; return nil })
	add("App", "Quit", "", "ctrl+d", func(m *Model) tea.Cmd { return m.quit() })

	// Claude Code's own commands and skills are inserted into the prompt so
	// arguments can be added before sending.
	if m.initResp != nil {
		cmds := append([]claude.Command(nil), m.initResp.Commands...)
		sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
		for _, c := range cmds {
			name := "/" + c.Name
			add("Claude Code", name, c.Description, c.ArgumentHint, func(m *Model) tea.Cmd {
				m.input.SetValue(name + " ")
				m.input.CursorEnd()
				return nil
			})
		}
	}
	return a
}

func (p *palette) apply() {
	q := strings.TrimSpace(p.filter.Value())
	p.shown = p.shown[:0]
	type scored struct {
		a action
		s int
	}
	var tmp []scored
	for _, a := range p.all {
		s, ok := fuzzyScore(q, a.label)
		if !ok {
			// Allow "theme charm" / "model opus" style queries.
			if s2, ok2 := fuzzyScore(q, a.group+" "+a.label); ok2 {
				s, ok = s2-5, true
			}
		}
		if ok {
			tmp = append(tmp, scored{a, s})
		}
	}
	if q != "" {
		sort.SliceStable(tmp, func(i, j int) bool { return tmp[i].s > tmp[j].s })
	}
	for _, t := range tmp {
		p.shown = append(p.shown, t.a)
	}
	p.sel, p.offset = 0, 0
}

func (p *palette) move(d int) {
	if len(p.shown) == 0 {
		return
	}
	p.sel = (p.sel + d + len(p.shown)) % len(p.shown)
	if p.sel < p.offset {
		p.offset = p.sel
	}
	if p.sel >= p.offset+paletteRows {
		p.offset = p.sel - paletteRows + 1
	}
}

func (m *Model) handlePaletteKey(k tea.KeyMsg) tea.Cmd {
	p := m.palette
	switch k.String() {
	case "esc", "ctrl+c", "ctrl+p":
		m.closePalette()
		return nil
	case "up", "ctrl+k":
		p.move(-1)
		return nil
	case "down", "ctrl+j", "tab":
		p.move(1)
		return nil
	case "enter":
		if p.sel >= len(p.shown) {
			return nil
		}
		a := p.shown[p.sel]
		m.closePalette()
		cmd := a.run(m)
		m.layout()
		m.refresh()
		return cmd
	}
	var c tea.Cmd
	before := p.filter.Value()
	p.filter, c = p.filter.Update(k)
	if p.filter.Value() != before {
		p.apply()
	}
	return c
}

func (p *palette) view(width int) string {
	w := dialogWidth(width, 76)
	inner := w - 4
	title := gradientText("Commands", true)
	count := mutedStyle.Render(fmt.Sprintf("%d", len(p.shown)))
	head := title + strings.Repeat(" ", max(1, inner-lipgloss.Width(title)-lipgloss.Width(count))) + count
	lines := []string{head, p.filter.View(), mutedStyle.Render(strings.Repeat("─", inner))}

	if len(p.shown) == 0 {
		lines = append(lines, mutedStyle.Render("no matches"))
	}
	end := min(len(p.shown), p.offset+paletteRows)
	lastGroup := ""
	for i := p.offset; i < end; i++ {
		a := p.shown[i]
		group := ""
		if a.group != lastGroup {
			group = a.group
			lastGroup = a.group
		}
		groupCol := lipgloss.NewStyle().Width(12).Foreground(currentTheme().second).Render(truncate(group, 11))
		key := a.key
		keyW := lipgloss.Width(key)
		labelW := inner - 12 - keyW - 2
		label := truncate(a.label, labelW)
		desc := ""
		if rest := labelW - lipgloss.Width(label) - 2; rest > 6 && a.desc != "" {
			desc = "  " + truncate(a.desc, rest)
		}
		body := label + desc
		gap := strings.Repeat(" ", max(1, inner-12-lipgloss.Width(body)-keyW))
		if i == p.sel {
			row := selStyle.Render(label) + selStyle.Bold(false).Foreground(currentTheme().muted).Render(desc+gap) + selStyle.Bold(false).Render(key)
			lines = append(lines, groupCol+row)
		} else {
			lines = append(lines, groupCol+textStyle.Render(label)+mutedStyle.Render(desc)+gap+statusKey.Render(key))
		}
	}
	lines = append(lines, "", helpLine([][2]string{{"↑↓", "move"}, {"enter", "run"}, {"esc", "close"}}, inner))
	return dialogBox.Width(w).Render(strings.Join(lines, "\n"))
}
