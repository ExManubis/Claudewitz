package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/sessions"
)

const historyMax = 500

// history recalls earlier prompts with ↑/↓, like a shell.
type history struct {
	items   []string // oldest first
	idx     int      // position while browsing; -1 when not browsing
	draft   string   // what was typed before browsing started
	project string
}

type historyEntry struct {
	Project string `json:"project"`
	Text    string `json:"text"`
	Time    int64  `json:"ts"`
}

type historyLoadedMsg struct {
	project string
	items   []string
}

// loadHistory merges prompts from past session transcripts with
// claudewitz's own history file, both scoped to the project.
func loadHistory(project string) tea.Cmd {
	return func() tea.Msg {
		items := sessions.Prompts(sessions.ProjectDir(project), 200)
		if f, err := os.Open(config.HistoryPath()); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
			for sc.Scan() {
				var e historyEntry
				if json.Unmarshal(sc.Bytes(), &e) == nil && e.Project == project && e.Text != "" {
					items = append(items, e.Text)
				}
			}
			f.Close()
		}
		return historyLoadedMsg{project: project, items: dedupeKeepLast(items)}
	}
}

// dedupeKeepLast drops earlier copies of repeated prompts and caps the list.
func dedupeKeepLast(items []string) []string {
	seen := map[string]bool{}
	var rev []string
	for i := len(items) - 1; i >= 0 && len(rev) < historyMax; i-- {
		if t := items[i]; !seen[t] {
			seen[t] = true
			rev = append(rev, t)
		}
	}
	out := make([]string, len(rev))
	for i, t := range rev {
		out[len(rev)-1-i] = t
	}
	return out
}

func newHistory(project string) *history { return &history{idx: -1, project: project} }

// add records a sent prompt in memory and on disk.
func (h *history) add(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	h.items = dedupeKeepLast(append(h.items, text))
	h.idx = -1
	p := config.HistoryPath()
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(historyEntry{Project: h.project, Text: text, Time: time.Now().Unix()})
	f.Write(append(line, '\n'))
}

// browsing reports whether the input still shows a recalled prompt.
func (h *history) browsing(current string) bool {
	return h.idx >= 0 && h.idx < len(h.items) && h.items[h.idx] == current
}

// prev steps back; ok is false when ↑ should go to the textarea instead.
func (h *history) prev(current string) (string, bool) {
	if len(h.items) == 0 {
		return "", false
	}
	switch {
	case h.browsing(current):
		if h.idx > 0 {
			h.idx--
		}
	case current == "":
		h.draft, h.idx = current, len(h.items)-1
	default:
		return "", false
	}
	return h.items[h.idx], true
}

// next steps forward, restoring the draft past the newest entry.
func (h *history) next(current string) (string, bool) {
	if !h.browsing(current) {
		return "", false
	}
	if h.idx < len(h.items)-1 {
		h.idx++
		return h.items[h.idx], true
	}
	h.idx = -1
	return h.draft, true
}
