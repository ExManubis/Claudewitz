package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/sessions"
)

// picker is the modal session browser.
type picker struct {
	all      bool // all projects vs. the current one
	loading  bool
	sessions []sessions.Info
	shown    []sessions.Info
	filter   textinput.Model
	sel      int
	offset   int
	current  string // active session id
	cwd      string // active session directory
}

type sessionsLoadedMsg struct {
	all  bool
	list []sessions.Info
}

func loadSessions(cwd string, all bool) tea.Cmd {
	return func() tea.Msg {
		if all {
			return sessionsLoadedMsg{all, sessions.ListAll()}
		}
		return sessionsLoadedMsg{all, sessions.List(sessions.ProjectDir(cwd))}
	}
}

func newPicker(current, cwd string) *picker {
	ti := textinput.New()
	ti.Placeholder = "type to filter"
	ti.Prompt = icons.search + " "
	ti.Focus()
	return &picker{loading: true, filter: ti, current: current, cwd: cwd}
}

func (p *picker) setSessions(list []sessions.Info) {
	p.loading = false
	p.sessions = list
	p.applyFilter()
}

func (p *picker) applyFilter() {
	q := strings.TrimSpace(p.filter.Value())
	p.shown = p.shown[:0]
	type scored struct {
		s     sessions.Info
		score int
	}
	var tmp []scored
	lq := strings.ToLower(q)
	for _, s := range p.sessions {
		// Substring matches anywhere win; fuzzy matching only on the label,
		// since subsequences of long haystacks match almost anything.
		hay := strings.ToLower(s.Label() + " " + s.LastPrompt + " " + s.Branch + " " + s.CWD + " " + s.ID)
		if strings.Contains(hay, lq) {
			tmp = append(tmp, scored{s, 1000 - strings.Index(hay, lq)})
		} else if sc, ok := fuzzyScore(q, s.Label()); ok {
			tmp = append(tmp, scored{s, sc})
		}
	}
	// Keep recency order unless filtering.
	if q != "" {
		for i := 1; i < len(tmp); i++ {
			for j := i; j > 0 && tmp[j].score > tmp[j-1].score; j-- {
				tmp[j], tmp[j-1] = tmp[j-1], tmp[j]
			}
		}
	}
	for _, t := range tmp {
		p.shown = append(p.shown, t.s)
	}
	p.sel, p.offset = 0, 0
}

func (p *picker) selected() (sessions.Info, bool) {
	if p.sel < len(p.shown) {
		return p.shown[p.sel], true
	}
	return sessions.Info{}, false
}

func (p *picker) move(d, visible int) {
	if len(p.shown) == 0 {
		return
	}
	p.sel = max(0, min(len(p.shown)-1, p.sel+d))
	if p.sel < p.offset {
		p.offset = p.sel
	}
	if p.sel >= p.offset+visible {
		p.offset = p.sel - visible + 1
	}
}

// pickerRows is how many sessions fit, each taking two lines.
func pickerRows(height int) int { return max(1, (height-14)/2) }

func (p *picker) view(width, height int) string {
	w := min(width-4, 110)
	inner := w - 6
	scope := "this project"
	if p.all {
		scope = "all projects"
	}
	title := gradientText("Resume a session", true) + mutedStyle.Render("  "+scope)
	count := ""
	if !p.loading {
		count = mutedStyle.Render(fmt.Sprintf("%d sessions", len(p.shown)))
	}
	gap := inner - lipgloss.Width(title) - lipgloss.Width(count)
	lines := []string{title + strings.Repeat(" ", max(1, gap)) + count, p.filter.View(), ""}

	rows := pickerRows(height)
	switch {
	case p.loading:
		lines = append(lines, mutedStyle.Render("loading sessions…"))
	case len(p.shown) == 0:
		lines = append(lines, mutedStyle.Render("no sessions found"))
		if !p.all {
			lines = append(lines, mutedStyle.Render("press tab to search all projects"))
		}
	default:
		end := min(len(p.shown), p.offset+rows)
		for i := p.offset; i < end; i++ {
			s := p.shown[i]
			when := relTime(s.Updated)
			label := s.Label()
			if p.isCurrent(s) {
				label = "● " + label
			}
			top := truncate(label, inner-lipgloss.Width(when)-2)
			top += strings.Repeat(" ", max(1, inner-lipgloss.Width(top)-lipgloss.Width(when))) + when
			meta := []string{fmt.Sprintf("%d msgs", s.Messages)}
			if s.Branch != "" {
				meta = append(meta, icons.branch+" "+s.Branch)
			}
			if p.all && s.CWD != "" {
				meta = append(meta, prettyPath(s.CWD))
			}
			if s.LastPrompt != "" && !strings.HasPrefix(s.LastPrompt, s.FirstPrompt) && !strings.HasPrefix(s.FirstPrompt, s.LastPrompt) {
				meta = append(meta, "last: "+s.LastPrompt)
			}
			sub := truncate("  "+strings.Join(meta, " · "), inner)
			if i == p.sel {
				lines = append(lines, selStyle.Render(top), selStyle.Bold(false).Render(sub+strings.Repeat(" ", max(0, inner-lipgloss.Width(sub)))))
			} else {
				lines = append(lines, textStyle.Render(top), mutedStyle.Render(sub))
			}
		}
	}
	scopeHint := "all projects"
	if p.all {
		scopeHint = "this project"
	}
	lines = append(lines, "", helpLine([][2]string{{"↑↓", "move"}, {"enter", "resume"}, {"tab", scopeHint}, {"esc", "close"}}, inner))
	return pickerBox.Width(w).Render(strings.Join(lines, "\n"))
}

func (p *picker) isCurrent(s sessions.Info) bool {
	return s.ID == p.current && (s.CWD == "" || s.CWD == p.cwd)
}

func relTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("Jan 2")
}

func prettyPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
