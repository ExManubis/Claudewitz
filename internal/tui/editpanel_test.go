package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

// panelModel is a w×h model with an Edit request on a 60-line file waiting.
func panelModel(t *testing.T, w, h int) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 60; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	path := filepath.Join(dir, "notes.txt")
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	m := New(context.Background(), claude.Options{CWD: dir}, config.Config{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = next.(Model)
	in, _ := json.Marshal(map[string]any{"file_path": path, "old_string": "line 40\nline 41\n", "new_string": "line forty\nline 41\n"})
	m.pending = []*claude.PermissionRequest{{ToolName: "Edit", Input: in}}
	m.startInteraction()
	m.layout()
	return m, path
}

func TestEditPanelPlacesTheDiffInTheFile(t *testing.T) {
	m, _ := panelModel(t, 120, 40)
	if m.editPanel == nil {
		t.Fatal("no panel for an Edit request")
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"Permission Required", "notes.txt", "@@ -40,2 +40,2 @@", "40 - line 40", "40 + line forty", "Yes, allow", "Allow for session", "answer in the panel"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in:\n%s", want, v)
		}
	}
	for _, l := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(l); w > 120 {
			t.Fatalf("line is %d wide: %q", w, ansi.Strip(l))
		}
	}
}

func TestEditPanelButtonsAnswer(t *testing.T) {
	m, _ := panelModel(t, 120, 40)
	// Move focus to "No, deny" and confirm: the request is answered and
	// the tool call marked failed.
	m.chat.add(&entry{kind: kindTool, toolID: "t1", toolName: "Edit"})
	m.pending[0].ToolUseID = "t1"
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRight}, {Type: tea.KeyRight}, {Type: tea.KeyEnter}} {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	if len(m.pending) != 0 || m.editPanel != nil {
		t.Fatalf("still pending: %d, panel %v", len(m.pending), m.editPanel != nil)
	}
	if e := m.chat.byToolID["t1"]; e.toolStatus != toolFailed {
		t.Errorf("tool status = %v, want failed (denied)", e.toolStatus)
	}
}

func TestEditPanelClickButton(t *testing.T) {
	m, _ := panelModel(t, 120, 40)
	_ = m.View() // lays out the buttons
	ep := m.editPanel
	x := ep.buttonsX[1][0] + 1
	next, _ := m.Update(tea.MouseMsg{X: x, Y: ep.buttonsY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m = next.(Model); len(m.pending) != 0 || !m.alwaysAllow["Edit"] {
		t.Fatalf("clicking 'Allow for session' should answer and remember Edit (pending %d)", len(m.pending))
	}
}

func TestWriteOverExistingFileDiffsAgainstIt(t *testing.T) {
	m, path := panelModel(t, 120, 40)
	in, _ := json.Marshal(map[string]any{"file_path": path, "content": "just one line\n"})
	ep := newEditPanel(&claude.PermissionRequest{ToolName: "Write", Input: in})
	if a, r := diffStats(ep.lines); a != 1 || r != 60 || ep.note != "overwrites the file" {
		t.Fatalf("+%d -%d %q", a, r, ep.note)
	}
	_ = m
}

func TestHunkHeaders(t *testing.T) {
	d := computeDiff("a\nb\nc\n", "a\nB\nc\n")
	shiftLines(d, 9, 9)
	h := hunkHeaders(d)
	if h[0].kind != '@' || h[0].text != "@@ -10,3 +10,3 @@" {
		t.Fatalf("header %+v", h[0])
	}
}

// The chat places edits too: before they land (live) by the old text, and
// after (accept-edits, history) by the new text.
func TestChatDiffsUseFileLineNumbers(t *testing.T) {
	m, path := panelModel(t, 120, 40)
	in, _ := json.Marshal(map[string]any{"file_path": path, "old_string": "line 50\n", "new_string": "line fifty\n"})

	m.applyBlocks("assistant", []claude.ContentBlock{{Type: "tool_use", ID: "live", Name: "Edit", Input: in}}, true)
	os.WriteFile(path, []byte(strings.Replace(mustRead(t, path), "line 50\n", "line fifty\n", 1)), 0o644)
	m.applyBlocks("assistant", []claude.ContentBlock{{Type: "tool_use", ID: "replayed", Name: "Edit", Input: in}}, false)
	for _, id := range []string{"live", "replayed"} {
		e := m.chat.byToolID[id]
		e.toolStatus = toolOK
		if v := ansi.Strip(m.chat.renderEntry(e, 100)); !strings.Contains(v, "@@ -50,1 +50,1 @@") || !strings.Contains(v, "50 + line fifty") {
			t.Errorf("%s:\n%s", id, v)
		}
	}
}

func TestReplayedWriteDoesNotDiffAgainstDisk(t *testing.T) {
	_, path := panelModel(t, 120, 40)
	in, _ := json.Marshal(map[string]any{"file_path": path, "content": "something else\n"})
	if d, _ := editDiff("Write", in, false); len(d) != 2 || d[1].kind != '+' {
		t.Fatalf("got %+v", d)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
