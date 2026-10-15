package tui

import (
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxSuggestions = 8

type suggestion struct {
	label  string // what is shown
	insert string // what replaces the token
	desc   string
	score  int
}

// completer offers slash-command and @file completions for the input.
type completer struct {
	items  []suggestion
	sel    int
	offset int
	start  int // byte offset in the input value where the token begins
	token  string
	active bool
	closed string // position:token the user dismissed with esc
}

// localCommands are handled by claudewitz itself.
var localCommands = []suggestion{
	{label: "/help", desc: "show commands and keys"},
	{label: "/new", desc: "start a fresh session"},
	{label: "/resume", desc: "browse and resume sessions"},
	{label: "/model", desc: "switch model (opens the picker)"},
	{label: "/effort", desc: "set effort level"},
	{label: "/mode", desc: "permission mode"},
	{label: "/theme", desc: "switch colour theme"},
	{label: "/sidebar", desc: "toggle the sidebar"},
	{label: "/changes", desc: "review changed files as diffs"},
	{label: "/edit", desc: "write the prompt in $EDITOR"},
	{label: "/image", desc: "attach an image (ctrl+f)"},
	{label: "/notifications", desc: "desktop notification mode"},
	{label: "/icons", desc: "Nerd Font or plain Unicode icons"},
	{label: "/diff", desc: "side-by-side or unified diffs"},
	{label: "/session", desc: "show session id and cwd"},
	{label: "/daemon", desc: "daemon status, and whether it keeps running"},
	{label: "/pair", desc: "QR code to pair the iPhone app"},
	{label: "/quit", desc: "exit claudewitz"},
}

type filesIndexedMsg struct {
	dir   string
	files []string
}

// indexFiles lists project files, preferring git's view of the tree.
func indexFiles(dir string) tea.Cmd {
	return func() tea.Msg {
		orig := dir
		if dir == "" {
			dir = "."
		}
		cmd := exec.Command("git", "ls-files", "-co", "--exclude-standard")
		cmd.Dir = dir
		if out, err := cmd.Output(); err == nil && len(out) > 0 {
			files := strings.Split(strings.TrimSpace(string(out)), "\n")
			return filesIndexedMsg{dir: orig, files: withDirs(files)}
		}
		var files []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()
			if d.IsDir() && p != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist" || name == "target") {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				if r, err := filepath.Rel(dir, p); err == nil {
					files = append(files, r)
				}
			}
			if len(files) > 20000 {
				return fs.SkipAll
			}
			return nil
		})
		return filesIndexedMsg{dir: orig, files: withDirs(files)}
	}
}

func withDirs(files []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(files))
	for _, f := range files {
		for d := filepath.Dir(f); d != "." && d != "/" && !seen[d]; d = filepath.Dir(d) {
			seen[d] = true
			out = append(out, d+"/")
		}
	}
	return append(out, files...)
}

// update recomputes suggestions for the current input value.
func (c *completer) update(m *Model) {
	val := m.input.Value()
	c.active = false
	c.items = nil
	c.start, c.token = -1, ""

	// Slash commands: the whole input is "/cmd" or "/cmd arg".
	if strings.HasPrefix(val, "/") && !strings.Contains(val, "\n") {
		if sp := strings.IndexByte(val, ' '); sp < 0 {
			c.start, c.token = 0, val
			c.items = rank(val[1:], m.slashSuggestions(), func(s suggestion) string { return s.label[1:] })
		} else {
			cmd, arg := val[:sp], val[sp+1:]
			if !strings.Contains(arg, " ") {
				c.start, c.token = sp+1, arg
				c.items = rank(arg, m.argSuggestions(cmd), func(s suggestion) string { return s.label })
			}
		}
	} else {
		i := strings.LastIndexAny(val, " \n\t")
		if tok := val[i+1:]; strings.HasPrefix(tok, "@") {
			c.start, c.token = i+1, tok
			c.items = rankFiles(tok[1:], m.files)
		}
	}

	if key := c.key(); key == c.closed {
		c.items = nil
	} else {
		c.closed = ""
	}
	// Hide when the only match is exactly what was typed.
	if len(c.items) == 1 && c.items[0].insert == c.token {
		c.items = nil
	}
	c.active = len(c.items) > 0
	if c.sel >= len(c.items) {
		c.sel, c.offset = 0, 0
	}
}

func (m *Model) slashSuggestions() []suggestion {
	out := make([]suggestion, 0, len(localCommands))
	seen := map[string]bool{}
	for _, s := range localCommands {
		s.insert = s.label
		out = append(out, s)
		seen[s.label] = true
	}
	if m.initResp != nil {
		for _, cmd := range m.initResp.Commands {
			label := "/" + cmd.Name
			if seen[label] {
				continue
			}
			seen[label] = true
			desc := cmd.Description
			if cmd.ArgumentHint != "" {
				desc = cmd.ArgumentHint + " · " + desc
			}
			out = append(out, suggestion{label: label, insert: label, desc: desc})
		}
	}
	return out
}

func (m *Model) argSuggestions(cmd string) []suggestion {
	var out []suggestion
	switch cmd {
	case "/model":
		if m.initResp != nil {
			for _, mi := range m.initResp.Models {
				out = append(out, suggestion{label: mi.Value, insert: mi.Value, desc: firstNonEmpty(mi.Description, mi.DisplayName)})
			}
		}
		if len(out) == 0 {
			for _, v := range []string{"opus", "sonnet", "haiku"} {
				out = append(out, suggestion{label: v, insert: v})
			}
		}
	case "/mode":
		for _, pm := range permissionModes {
			out = append(out, suggestion{label: pm, insert: pm, desc: modeDescs[pm]})
		}
	case "/image":
		for _, f := range m.files {
			if isImagePath(f) {
				out = append(out, suggestion{label: f, insert: f})
			}
		}
	case "/notifications":
		for _, n := range notifyModes {
			out = append(out, suggestion{label: n, insert: n, desc: notifyModeDesc[n]})
		}
	case "/icons":
		for _, n := range iconModes {
			out = append(out, suggestion{label: n, insert: n, desc: iconModeDesc[n]})
		}
	case "/diff":
		for _, n := range diffModes {
			out = append(out, suggestion{label: n, insert: n, desc: diffModeDesc[n]})
		}
	case "/effort":
		for _, l := range defaultEffortLevels {
			out = append(out, suggestion{label: l, insert: l, desc: effortDesc[l]})
		}
	case "/theme":
		for _, t := range themes {
			out = append(out, suggestion{label: t.name, insert: t.name, desc: t.desc})
		}
	}
	return out
}

// rank filters and orders items by fuzzy match against query.
func rank(query string, items []suggestion, key func(suggestion) string) []suggestion {
	var out []suggestion
	for _, it := range items {
		if s, ok := fuzzyScore(query, key(it)); ok {
			it.score = s
			if it.insert == "" {
				it.insert = it.label
			}
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

func rankFiles(query string, files []string) []suggestion {
	var out []suggestion
	for _, f := range files {
		s, ok := fuzzyScore(query, f)
		if !ok {
			continue
		}
		// Prefer matches in the base name and shorter paths.
		if bs, ok := fuzzyScore(query, filepath.Base(strings.TrimSuffix(f, "/"))); ok && query != "" {
			s += bs
		}
		s -= len(f) / 8
		out = append(out, suggestion{label: f, insert: "@" + f, score: s})
		if len(out) > 2000 {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

// fuzzyScore matches query as a case-insensitive subsequence of target,
// rewarding prefixes, word boundaries and consecutive runs.
func fuzzyScore(query, target string) (int, bool) {
	if query == "" {
		return 0, true
	}
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(target))
	orig := []rune(target)
	score, qi, run := 0, 0, 0
	for ti := 0; ti < len(t) && qi < len(q); ti++ {
		if t[ti] != q[qi] {
			run = 0
			continue
		}
		bonus := 1
		if ti == 0 {
			bonus += 8
		} else if p := orig[ti-1]; p == '/' || p == '-' || p == '_' || p == '.' || p == ' ' || p == ':' || (unicode.IsLower(p) && unicode.IsUpper(orig[ti])) {
			bonus += 5
		}
		run++
		bonus += run * 2
		score += bonus
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	if strings.HasPrefix(strings.ToLower(target), strings.ToLower(query)) {
		score += 20
	}
	return score, true
}

func (c *completer) key() string { return fmt.Sprintf("%d:%s", c.start, c.token) }

// dismiss hides suggestions until the token changes.
func (c *completer) dismiss() { c.closed = c.key() }

// complete reports whether the highlighted suggestion is already typed out,
// in which case enter should submit rather than accept.
func (c *completer) complete() bool {
	return c.active && c.sel < len(c.items) && strings.TrimSpace(c.items[c.sel].insert) == strings.TrimSpace(c.token)
}

func (c *completer) move(d int) {
	if len(c.items) == 0 {
		return
	}
	c.sel = (c.sel + d + len(c.items)) % len(c.items)
	if c.sel < c.offset {
		c.offset = c.sel
	}
	if c.sel >= c.offset+maxSuggestions {
		c.offset = c.sel - maxSuggestions + 1
	}
}

// accept replaces the current token with the selected suggestion.
func (c *completer) accept(m *Model) {
	if !c.active || len(c.items) == 0 {
		return
	}
	it := c.items[c.sel]
	val := m.input.Value()
	newVal := val[:c.start] + it.insert
	if !strings.HasSuffix(it.insert, "/") {
		newVal += " "
	}
	m.input.SetValue(newVal)
	m.input.CursorEnd()
	c.sel, c.offset = 0, 0
	c.update(m)
}

func (c *completer) view(width int) string {
	if !c.active {
		return ""
	}
	w := min(width-2, 84)
	inner := w - 4
	end := min(len(c.items), c.offset+maxSuggestions)
	labelW := 0
	for _, it := range c.items[c.offset:end] {
		labelW = max(labelW, lipgloss.Width(it.label))
	}
	labelW = min(labelW, inner*2/3)
	var rows []string
	for i := c.offset; i < end; i++ {
		it := c.items[i]
		label := truncate(it.label, labelW)
		pad := strings.Repeat(" ", labelW-lipgloss.Width(label))
		desc := truncate(it.desc, inner-labelW-2)
		row := label + pad + "  " + desc
		row += strings.Repeat(" ", max(0, inner-lipgloss.Width(row)))
		if i == c.sel {
			rows = append(rows, selStyle.Render(row))
		} else {
			rows = append(rows, textStyle.Render(label)+pad+"  "+mutedStyle.Render(desc)+strings.Repeat(" ", max(0, inner-lipgloss.Width(row))))
		}
	}
	footer := mutedStyle.Render(truncate("↑↓ select · tab/enter accept · esc close", inner))
	if len(c.items) > maxSuggestions {
		footer = mutedStyle.Render(truncate(fmt.Sprintf("↑↓ %d/%d · tab/enter accept · esc close", c.sel+1, len(c.items)), inner))
	}
	return popupBox.Width(w).Render(strings.Join(rows, "\n") + "\n" + footer)
}
