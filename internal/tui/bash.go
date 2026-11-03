package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/safetext"
)

// bashHeader splits a Bash call into its header summary and the command
// line shown under it. When Claude described the command, the description
// leads the header and the command moves under it; a leading `cd dir &&`
// becomes a folder chip after the summary. room is the header's space.
func (c *chat) bashHeader(e *entry, room, width int) (summary, cmdLine string) {
	var in map[string]any
	_ = json.Unmarshal(e.toolInput, &in)
	cmd, desc := str(in, "command"), safetext.Line(str(in, "description"))
	var chip string
	if d, rest := leadingCd(cmd); d != "" {
		cmd = rest
		if dir := c.bashDir(d); dir != "" {
			chip = " " + lipgloss.NewStyle().Foreground(currentTheme().user).Render("in "+truncateLeft(dir, 30))
		}
	}
	room = max(10, room-lipgloss.Width(chip))
	oneLine := safetext.Line(strings.ReplaceAll(strings.TrimSpace(cmd), "\n", " ⏎ "))
	if desc == "" {
		return mutedStyle.Render(truncate(oneLine, room)) + chip, ""
	}
	summary = agentStyle.UnsetBold().Render(truncate(desc, room)) + chip
	if c.open(e) {
		return summary, mutedStyle.Render("$ " + clipText(safetext.Clean(cmd), width-2, 40))
	}
	return summary, mutedStyle.Render("$ " + truncate(oneLine, width-2))
}

// bashDir is where a `cd` sends the command, relative to the project, or
// "" when that is the project itself.
func (c *chat) bashDir(d string) string {
	if d == "~" || strings.HasPrefix(d, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			d = filepath.Join(home, d[1:])
		}
	}
	if !filepath.IsAbs(d) {
		d = filepath.Join(c.cwd, d)
	}
	if r := relPath(filepath.Clean(d), c.cwd); r != "." {
		return r
	}
	return ""
}

// leadingCd splits `cd dir && rest` into dir and rest. Any other command,
// or a dir that needs the shell to expand it, gives "" and cmd unchanged.
func leadingCd(cmd string) (dir, rest string) {
	s := strings.TrimSpace(cmd)
	if !strings.HasPrefix(s, "cd ") {
		return "", cmd
	}
	s = strings.TrimLeft(s[3:], " \t")
	if s == "" {
		return "", cmd
	}
	if q := s[0]; q == '"' || q == '\'' {
		end := strings.IndexByte(s[1:], q)
		if end < 0 {
			return "", cmd
		}
		dir, s = s[1:1+end], s[2+end:]
	} else {
		end := strings.IndexAny(s, " \t;&|\n")
		if end < 0 {
			return "", cmd
		}
		dir, s = s[:end], s[end:]
	}
	s = strings.TrimLeft(s, " \t")
	if dir == "" || dir == "-" || strings.ContainsAny(dir, "$`*?") || !strings.HasPrefix(s, "&&") {
		return "", cmd
	}
	return dir, strings.TrimSpace(s[2:])
}
