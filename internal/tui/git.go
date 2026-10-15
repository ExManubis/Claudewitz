package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/gitstatus"
)

const (
	gitPollEvery = 4 * time.Second
	// gitIdleEvery is how many polls to skip between reads while idle
	// (unfocused, or no input for a while): every ~30s instead of 4s.
	gitIdleEvery = 8
	gitMaxFiles  = 12
)

type gitStatusMsg struct {
	dir string
	st  gitstatus.Status
}

type gitPollMsg struct{}

func gitPoll() tea.Cmd {
	return tea.Tick(gitPollEvery, func(time.Time) tea.Msg { return gitPollMsg{} })
}

// onGitPoll reads the repository on every poll while you're around, and
// only now and then while idle, since a large repo makes each read costly.
func (m *Model) onGitPoll() tea.Cmd {
	m.gitPolls++
	if m.idle() && !m.busy && m.gitPolls%gitIdleEvery != 0 {
		return gitPoll()
	}
	return tea.Batch(m.refreshGit(), gitPoll())
}

// refreshGit reads the repository state in the background, unless a read
// is already in flight.
func (m *Model) refreshGit() tea.Cmd {
	if m.gitBusy {
		return nil
	}
	m.gitBusy = true
	dir, ctx := m.cwd(), m.ctx
	return func() tea.Msg {
		st, _ := gitstatus.Read(ctx, dir)
		return gitStatusMsg{dir: dir, st: st}
	}
}

// applyGit stores a fresh snapshot and toasts branch switches and new
// commits made since the previous one.
func (m *Model) applyGit(msg gitStatusMsg) tea.Cmd {
	if ws, ok := m.spaces[msg.dir]; ok && ws != m.workspace {
		ws.gitBusy = false
		ws.git, ws.gitLoaded = msg.st, true
		return nil // a folder in the background; no toasts for it
	}
	m.gitBusy = false
	if msg.dir != m.cwd() {
		return m.refreshGit() // directory changed (resume) while reading
	}
	prev, had := m.git, m.gitLoaded
	m.git, m.gitLoaded = msg.st, true
	if !had || !prev.IsRepo || !msg.st.IsRepo {
		return nil
	}
	var cmds []tea.Cmd
	switch {
	case prev.Branch != msg.st.Branch && msg.st.Branch != "":
		cmds = append(cmds, m.notify(toastInfo, "Switched to "+msg.st.Branch, ""))
	case prev.Branch != msg.st.Branch:
		cmds = append(cmds, m.notify(toastWarn, "Detached HEAD", shortSHA(msg.st.Head)))
	}
	// A new, recent commit (checking out an old one isn't "committed").
	if prev.Head != msg.st.Head && msg.st.LastCommit != "" && time.Since(msg.st.LastWhen) < time.Minute {
		cmds = append(cmds, m.notify(toastSuccess, "Committed on "+firstNonEmpty(msg.st.Branch, "HEAD"), msg.st.LastCommit))
	}
	return tea.Batch(cmds...)
}

// touchesTree reports whether an event may have changed the working tree.
func touchesTree(ev claude.Event) bool {
	if ev.Result != nil {
		return true
	}
	if ev.Message == nil || ev.Message.ParentToolUseID != nil {
		return false
	}
	for _, b := range ev.Message.Message.Content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// branchLabel is "main", "main*" when dirty, or a detached sha, after the
// branch icon.
func (m Model) branchLabel(dirtyMark bool) string {
	if !m.git.IsRepo {
		return ""
	}
	b := m.git.Branch
	if b == "" {
		b = shortSHA(m.git.Head)
	}
	if dirtyMark && m.git.Dirty() {
		b += "*"
	}
	return icons.branch + " " + b
}

func gitCodeStyle(c byte) lipgloss.Style {
	switch c {
	case 'A':
		return okStyle
	case 'D', 'U':
		return errStyle.Bold(true)
	case 'R', 'C':
		return agentStyle
	case '?':
		return mutedStyle
	}
	return warnStyle
}

// gitSection renders the sidebar's Git and Changes blocks, and which of
// the returned rows show which changed file (by path in the repository).
func (m Model) gitSection(w int) ([]string, map[int]string) {
	st := m.git
	var sec []string

	head := agentStyle.Render(m.branchLabel(false))
	if st.Detached() {
		head += mutedStyle.Render(" (detached)")
	}
	var ab []string
	if st.Ahead > 0 {
		ab = append(ab, okStyle.Render(fmt.Sprintf("%s%d", icons.ahead, st.Ahead)))
	}
	if st.Behind > 0 {
		ab = append(ab, warnStyle.Render(fmt.Sprintf("%s%d", icons.behind, st.Behind)))
	}
	if len(ab) > 0 {
		head += " " + strings.Join(ab, " ")
	} else if st.Upstream == "" && st.Branch != "" {
		head += mutedStyle.Render(" no upstream")
	}
	sec = append(sec, sectionTitle.Render("Git"), truncate(head, w))

	staged, modified, untracked, conflicts := st.Counts()
	var counts []string
	if conflicts > 0 {
		counts = append(counts, errStyle.Bold(true).Render(fmt.Sprintf("%s %d conflict", icons.conflict, conflicts)))
	}
	if staged > 0 {
		counts = append(counts, okStyle.Render(fmt.Sprintf("%s %d staged", icons.staged, staged)))
	}
	if modified > 0 {
		counts = append(counts, warnStyle.Render(fmt.Sprintf("%s %d changed", icons.changed, modified)))
	}
	if untracked > 0 {
		counts = append(counts, mutedStyle.Render(fmt.Sprintf("? %d new", untracked)))
	}
	if len(counts) == 0 {
		counts = append(counts, okStyle.Render(icons.clean+" clean"))
	}
	sec = append(sec, strings.Join(counts, mutedStyle.Render("  ")))
	if st.LastCommit != "" {
		when := ""
		if !st.LastWhen.IsZero() {
			when = " " + relTime(st.LastWhen)
		}
		sec = append(sec, mutedStyle.Render(truncate(st.LastCommit, w-lipgloss.Width(when)))+mutedStyle.Faint(true).Render(when))
	}

	if len(st.Files) == 0 {
		return sec, nil
	}
	sec = append(sec, sectionTitle.Render("Changes")+mutedStyle.Render(fmt.Sprintf(" %d", len(st.Files)))+mutedStyle.Faint(true).Render(" · ctrl+s"))
	rows := map[int]string{}
	cwd := m.cwd()
	for i, f := range st.Files {
		if i == gitMaxFiles {
			sec = append(sec, mutedStyle.Render(fmt.Sprintf("… %d more", len(st.Files)-gitMaxFiles)))
			break
		}
		abs := filepath.Join(st.Root, f.Path)
		code := gitCodeStyle(f.Code()).Render(string(f.Code()))
		mark := " "
		if m.track.fileIdx[abs] != nil {
			mark = assistLabel.Render("✻")
		}
		var stats string
		switch {
		case f.Binary:
			stats = mutedStyle.Render("bin")
		case f.Untracked():
			stats = mutedStyle.Render("new")
		default:
			if f.Added > 0 {
				stats = okStyle.Render(fmt.Sprintf("+%d", f.Added))
			}
			if f.Removed > 0 {
				if stats != "" {
					stats += " "
				}
				stats += errStyle.Render(fmt.Sprintf("-%d", f.Removed))
			}
		}
		prefix := code + mark + " "
		nameW := w - lipgloss.Width(prefix) - lipgloss.Width(stats) - 1
		name := truncateLeft(relPath(abs, cwd), max(4, nameW))
		gap := w - lipgloss.Width(prefix) - lipgloss.Width(name) - lipgloss.Width(stats)
		rows[len(sec)] = f.Path
		sec = append(sec, prefix+textStyle.Render(name)+strings.Repeat(" ", max(1, gap))+stats)
	}
	return sec, rows
}
