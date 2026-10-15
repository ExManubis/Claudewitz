package tui

import (
	"context"
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/gitstatus"
	"github.com/exmanubis/claudewitz/internal/sessions"
)

// readyModel is a Model sized like a normal terminal.
func readyModel(t *testing.T) Model {
	t.Helper()
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return next.(Model)
}

// assistantText is an assistant message whose tool call lands in the
// transcript as an entry (live text is streamed, not added here).
func assistantText(text string) claude.Event {
	raw, _ := json.Marshal(map[string]any{
		"type":    "assistant",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "tool_use", "id": text, "name": "Bash", "input": map[string]any{"command": "ls"}}}},
	})
	ev, _ := claude.ParseLine(raw)
	return ev
}

// A background session's events update its own view and leave the focused
// session's input, dialog and transcript alone.
func TestBackgroundEventsLeaveFocusedAlone(t *testing.T) {
	m := readyModel(t)
	m.proc = newFakeSession()
	m.input.SetValue("typing here")
	m.pending = []*claude.PermissionRequest{{RequestID: "r1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}}
	focusedEntries := len(m.chat.entries)

	bg := newSessionView(claude.Options{CWD: t.TempDir()})
	bgProc := newFakeSession()
	bg.proc = bgProc
	m.views = append(m.views, bg)

	next, _ := m.Update(procEvent{proc: bgProc, ev: assistantText("hello from the background")})
	m = next.(Model)
	if len(m.chat.entries) != focusedEntries || m.input.Value() != "typing here" || len(m.pending) != 1 {
		t.Fatalf("focused view changed: %d entries, input %q, %d pending", len(m.chat.entries), m.input.Value(), len(m.pending))
	}
	if len(bg.chat.entries) != 1 {
		t.Fatalf("background view got %d entries", len(bg.chat.entries))
	}
}

func TestEventsFromUnknownProcessesAreIgnored(t *testing.T) {
	m := readyModel(t)
	m.proc = newFakeSession()
	next, _ := m.Update(procEvent{proc: newFakeSession(), ev: assistantText("stale")})
	if len(next.(Model).chat.entries) != 0 {
		t.Fatal("a stale process's event reached the chat")
	}
}

// Git status, the file index and history land in the folder they were read
// for, even after focus has moved to another folder.
func TestLateResultsGoToTheirWorkspace(t *testing.T) {
	m := readyModel(t)
	oldDir := m.workspace.dir
	other := newSessionView(claude.Options{CWD: t.TempDir()})
	m.views = append(m.views, other)
	_ = m.focus(other)
	if m.workspace.dir == oldDir {
		t.Fatal("focus didn't switch the workspace")
	}

	next, _ := m.Update(filesIndexedMsg{dir: oldDir, files: []string{"old.go"}})
	m = next.(Model)
	next, _ = m.Update(gitStatusMsg{dir: oldDir, st: gitstatus.Status{IsRepo: true, Branch: "old-branch"}})
	m = next.(Model)
	next, _ = m.Update(historyLoadedMsg{project: oldDir, items: []string{"old prompt"}})
	m = next.(Model)

	if len(m.files) != 0 || m.git.Branch == "old-branch" || len(m.hist.items) != 0 {
		t.Fatalf("focused workspace got another folder's results: files %v, branch %q, history %v", m.files, m.git.Branch, m.hist.items)
	}
	old := m.spaces[oldDir]
	if len(old.files) != 1 || old.git.Branch != "old-branch" || len(old.hist.items) != 1 {
		t.Fatalf("old workspace: files %v, branch %q, history %v", old.files, old.git.Branch, old.hist.items)
	}
}

func TestFocusKeepsEachViewsDraft(t *testing.T) {
	m := readyModel(t)
	first := m.sessionView
	m.input.SetValue("first draft")
	second := newSessionView(claude.Options{CWD: t.TempDir()})
	m.views = append(m.views, second)
	_ = m.focus(second)
	m.input.SetValue("second draft")
	_ = m.focus(first)
	if m.input.Value() != "first draft" || second.input.Value() != "second draft" {
		t.Fatalf("drafts: %q / %q", m.input.Value(), second.input.Value())
	}
	if m.vp.Width == 0 {
		t.Fatal("the focused view wasn't laid out")
	}
}

func TestRemoveViewDropsUnusedWorkspace(t *testing.T) {
	m := readyModel(t)
	other := newSessionView(claude.Options{CWD: t.TempDir()})
	m.views = append(m.views, other)
	_ = m.focus(other)
	dir := m.workspace.dir
	_ = m.focus(m.views[0])
	m.removeView(other)
	if _, ok := m.spaces[dir]; ok || len(m.views) != 1 {
		t.Fatalf("workspace kept: %v, views %d", m.spaces, len(m.views))
	}
}

func TestResumeIntoAnotherFolderKeepsItsFileIndex(t *testing.T) {
	m := readyModel(t)
	m.start = func(context.Context, claude.Options) (claude.Session, error) { return newFakeSession(), nil }
	oldDir := m.workspace.dir
	newDir := t.TempDir()
	_ = m.resume(sessions.Info{ID: "abc", CWD: newDir, Path: "/nonexistent"})
	if m.workspace.dir != newDir {
		t.Fatalf("workspace is %q, want %q", m.workspace.dir, newDir)
	}
	if _, ok := m.spaces[oldDir]; ok {
		t.Fatal("old folder's workspace kept")
	}
	next, _ := m.Update(filesIndexedMsg{dir: newDir, files: []string{"new.go"}})
	m = next.(Model)
	if len(m.files) != 1 || m.files[0] != "new.go" {
		t.Fatalf("files %v", m.files)
	}
}
