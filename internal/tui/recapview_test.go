package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/recap"
)

func TestRecapDialogNavigates(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.termW, m.termH = 100, 50
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cmd := m.openRecap()
	if !m.modalOpen() {
		t.Fatal("recap isn't modal")
	}
	m.gotRecap(cmd().(recapMsg))
	view := ansi.Strip(m.recapDialog())
	for _, want := range []string{"Recap · Today", "Nothing yet today.", "←→ day"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog lacks %q:\n%s", want, view)
		}
	}
	m.handleRecapKey(tea.KeyMsg{Type: tea.KeyRight})
	if !m.recapView.date.Equal(midnight(time.Now())) {
		t.Fatal("moved past today")
	}
	cmd = m.handleRecapKey(tea.KeyMsg{Type: tea.KeyLeft})
	m.gotRecap(cmd().(recapMsg))
	view = ansi.Strip(m.recapDialog())
	if !strings.Contains(view, "Yesterday") || !strings.Contains(view, "A quiet day.") || !strings.Contains(view, "t today") {
		t.Fatalf("yesterday:\n%s", view)
	}
	m.handleRecapKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.recapView != nil {
		t.Fatal("esc didn't close")
	}
}

func TestRecapCardFitsAndShrinks(t *testing.T) {
	first := time.Date(2026, 10, 15, 8, 12, 0, 0, time.Local)
	last := first.Add(9 * time.Hour)
	d := recap.Day{Date: "2026-10-15", Prompts: 42, Sessions: 3, Working: 3*3600 + 120, First: &first, Last: &last,
		ToolCalls: 900, OutputTokens: 250_000, FilesEdited: 12, LinesAdded: 1204, LinesRemoved: 388,
		Commits: 9, PRsOpened: 2, PRsMerged: 2, Releases: 1, TestsPassed: 21, TestsFailed: 1, Streak: 5,
		Projects: []recap.Project{{Name: "claudewitz", Prompts: 30, Working: 7200}, {Name: "claudewitz-ios", Prompts: 12, Working: 3000}},
		Moments:  []recap.Milestone{{At: last, Kind: recap.Release, Text: "v0.4.0"}, {At: first, Kind: recap.Commit, Text: "Add the recap"}},
		Longest:  &recap.Turn{Seconds: 1500, Prompt: "Build the recap"},
		Badges:   []recap.Badge{{ID: "release", Name: "Release day", Detail: "1 release out the door"}},
		Week:     []recap.DayTotal{{Date: "2026-10-09"}, {Date: "2026-10-10", Prompts: 3}, {Date: "2026-10-11"}, {Date: "2026-10-12"}, {Date: "2026-10-13"}, {Date: "2026-10-14", Prompts: 9}, {Date: "2026-10-15", Prompts: 42}},
	}
	d.Hours[9], d.Hours[14] = 1800, 3600
	full := ansi.Strip(strings.Join(recapCard(d, 64, 1000, false), "\n"))
	for _, want := range []string{"Claude worked 3h 2m", "42 prompts in 3 sessions", "08:12 → 17:12", "9 commits", "+1,204", "−388",
		"busiest 14:00", "claudewitz-ios", "released v0.4.0", "Longest stretch 25m", "Release day", "✦ 5-day streak", "Th"} {
		if !strings.Contains(full, want) {
			t.Fatalf("card lacks %q:\n%s", want, full)
		}
	}
	for _, l := range strings.Split(full, "\n") {
		if w := ansi.StringWidth(l); w > 64 {
			t.Fatalf("line %d wide: %q", w, l)
		}
	}
	short := recapCard(d, 64, 16, false)
	if len(short) > 16 {
		t.Fatalf("card ignores the height: %d lines", len(short))
	}
	if s := ansi.Strip(strings.Join(short, "\n")); !strings.Contains(s, "Claude worked") || !strings.Contains(s, "9 commits") {
		t.Fatalf("a short card dropped the headline:\n%s", s)
	}
}

func TestRecapMarkdown(t *testing.T) {
	md := recapMarkdown(recap.Day{Date: "2026-10-15", Prompts: 1, Sessions: 1, Working: 90, Commits: 2, TestsPassed: 1,
		Badges: []recap.Badge{{Name: "All green"}}})
	for _, want := range []string{"**My day with Claude · Thursday 15 October**", "Claude worked 1m on 1 prompt in 1 session", "2 commits", "1 test run green, 0 red", "Badges: All green"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, md)
		}
	}
}
