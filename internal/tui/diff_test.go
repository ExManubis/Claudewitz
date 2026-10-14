package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

// Removals sit beside the additions that replace them; context and hunk
// separators fill both sides.
func TestPairDiff(t *testing.T) {
	d := computeDiff("a\nb\nc\nd\n", "a\nB\nB2\nd\n")
	rows := pairDiff(d)
	var got []string
	for _, r := range rows {
		side := func(l *diffLine) string {
			if l == nil {
				return "_"
			}
			return string(l.kind) + l.text
		}
		got = append(got, side(r.left)+"|"+side(r.right))
	}
	want := []string{" a| a", "-b|+B", "-c|+B2", " d| d"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}

	rows = pairDiff(computeDiff("a\n", "a\nb\nc\n"))
	if last := rows[len(rows)-1]; last.left != nil || last.right == nil || last.right.text != "c" {
		t.Fatalf("pure additions should leave the left side blank: %+v", last)
	}
}

func TestRenderDiffLayout(t *testing.T) {
	defer applyDiffMode("auto")
	d := computeDiff("one\ntwo\n", "one\nTWO\n")

	for _, tc := range []struct {
		mode  string
		width int
		split bool
	}{
		{"auto", 80, false},
		{"auto", 140, true},
		{"split", 80, true},
		{"unified", 140, false},
	} {
		applyDiffMode(tc.mode)
		out := renderDiff(d, "x.txt", tc.width, 0)
		lines := strings.Split(ansi.Strip(out), "\n")
		for _, l := range lines {
			if w := ansi.StringWidth(l); w != tc.width {
				t.Errorf("%s/%d: row is %d wide: %q", tc.mode, tc.width, w, l)
			}
		}
		// Unified: context, removal, addition. Split: context, then the
		// removal and addition on one row.
		want := 3
		if tc.split {
			want = 2
			if !strings.Contains(lines[1], "two") || !strings.Contains(lines[1], "TWO") {
				t.Errorf("%s/%d: changed row should show both sides: %q", tc.mode, tc.width, lines[1])
			}
		}
		if len(lines) != want {
			t.Errorf("%s/%d: %d rows, want %d:\n%s", tc.mode, tc.width, len(lines), want, ansi.Strip(out))
		}
	}
}

// Below the docking width ctrl+b opens the sidebar over the chat instead
// of doing nothing.
func TestSidebarDrawer(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	mm := tm.(Model)
	if !mm.showDrawer() || !mm.sidebarOn {
		t.Fatalf("ctrl+b at 80 columns should open the drawer and leave the docked setting alone")
	}
	lines := strings.Split(ansi.Strip(mm.View()), "\n")
	if len(lines) != 24 {
		t.Fatalf("view is %d rows, want 24", len(lines))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "CLAUDEWITZ") {
		t.Fatalf("drawer not drawn:\n%s", strings.Join(lines, "\n"))
	}

	// Widening the window docks the sidebar; the drawer no longer applies.
	tm, _ = mm.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	if mm = tm.(Model); mm.showDrawer() || !mm.showSidebar() {
		t.Fatalf("at 140 columns the sidebar should be docked, not a drawer")
	}
}
