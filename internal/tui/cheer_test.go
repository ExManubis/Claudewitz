package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

// shell runs a Bash call through m live: the call, then its result.
func shell(m *Model, id, command, output string, failed bool) {
	in, _ := json.Marshal(map[string]string{"command": command})
	m.handleEvent(claude.Event{Message: &claude.MessageEvent{Message: claude.Message{Role: "assistant", Content: []claude.ContentBlock{
		{Type: "tool_use", ID: id, Name: "Bash", Input: in}}}}})
	out, _ := json.Marshal(output)
	m.handleEvent(claude.Event{Message: &claude.MessageEvent{Message: claude.Message{Role: "user", Content: []claude.ContentBlock{
		{Type: "tool_result", ToolUseID: id, Content: out, IsError: failed}}}}})
}

func toastTitles(m *Model) string {
	var out []string
	for _, t := range m.toasts {
		out = append(out, t.title+": "+t.body)
	}
	return strings.Join(out, " | ")
}

func TestShippingSetsOffFireworks(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.busy = true
	shell(&m, "t1", `git add -A && git commit -m "Add the recap"`, "[main 1a2b3c4] Add the recap", false)
	if got := toastTitles(&m); got != "Committed: Add the recap" {
		t.Fatalf("toasts: %q", got)
	}
	if !m.sparks.celebrating() || m.sparks.proudFor == 0 {
		t.Fatal("no fireworks")
	}
	if c := ansi.Strip(m.sparkCaption(30)); !strings.Contains(c, "✦ Committed") {
		t.Fatalf("caption %q", c)
	}

	// A release is a bigger moment: a volley.
	m.sparks.launches = nil
	shell(&m, "t2", `git tag -a v1.0.0 -m v1.0.0 && git push -q origin v1.0.0`, "", false)
	if len(m.sparks.launches) != 3 || !strings.Contains(toastTitles(&m), "Released v1.0.0") {
		t.Fatalf("release: %d launches, toasts %q", len(m.sparks.launches), toastTitles(&m))
	}
}

func TestOnlyGreenAfterRedIsNews(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	shell(&m, "t1", `go test ./...`, "ok  x 0.1s", false)
	if len(m.toasts) != 0 {
		t.Fatalf("a green run on its own made a toast: %q", toastTitles(&m))
	}
	shell(&m, "t2", `go test ./...`, "--- FAIL: TestX\nFAIL", true)
	if len(m.toasts) != 0 {
		t.Fatalf("a red run made a toast: %q", toastTitles(&m))
	}
	shell(&m, "t3", `go test ./... | tail -3`, "ok  x 0.1s", false)
	if got := toastTitles(&m); got != "Tests green again: go test" {
		t.Fatalf("toasts %q", got)
	}
}

func TestHistoryDoesNotCelebrate(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	in, _ := json.Marshal(map[string]string{"command": `git commit -m old`})
	m.applyBlocks("assistant", []claude.ContentBlock{{Type: "tool_use", ID: "t1", Name: "Bash", Input: in}}, false)
	m.applyBlocks("user", []claude.ContentBlock{{Type: "tool_result", ToolUseID: "t1", Content: json.RawMessage(`"[main abc1234] old"`)}}, false)
	if len(m.toasts) != 0 || m.sparks.celebrating() {
		t.Fatal("replayed history set off fireworks")
	}
}

func TestProudFaceWaitsForRest(t *testing.T) {
	f := newSparkField(30, 8)
	f.setMode(sparkWorking)
	f.celebrate(false)
	for i := 0; i < 24*6; i++ { // six seconds of swirl
		f.step(1, 1.0/24)
	}
	if f.proudFor != proudTime {
		t.Fatalf("the ✓ ran down while working: %v left", f.proudFor)
	}
	if f.celebrating() {
		t.Fatal("fireworks still going after six seconds")
	}
	f.setMode(sparkIdle)
	for f.mode != sparkIdle {
		f.step(0, 1.0/24)
	}
	if f.display() != &f.proud {
		t.Fatal("resting tty doesn't show the ✓")
	}
	for i := 0; i < int(proudTime*24)+2; i++ {
		f.step(0, 1.0/24)
	}
	if f.display() == &f.proud {
		t.Fatal("the ✓ never went away")
	}
}

func TestPokingTtySpinsIt(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.termW, m.termH = 140, 50
	m.resize()
	if !m.showSidebar() {
		t.Skip("no sidebar at this size")
	}
	rows := sparkRows(m.height - 1)
	x, y := m.width-sidebarWidth/2, m.height-1-rows-3+rows/2
	if !m.mascotAt(x, y) || m.mascotAt(x, 2) || m.mascotAt(2, y) {
		t.Fatal("hit-test")
	}
	next, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = next.(Model)
	if m.sparks.spinFor == 0 || m.quip == "" {
		t.Fatalf("no spin (%v) or quip (%q)", m.sparks.spinFor, m.quip)
	}
	first := m.sparks.display()
	for i := 0; i < 12; i++ {
		m.sparks.step(0, 1.0/24)
	}
	if m.sparks.display() == first {
		t.Fatal("tty didn't turn")
	}
	if c := ansi.Strip(m.sparkCaption(30)); !strings.Contains(c, m.quip) {
		t.Fatalf("caption %q", c)
	}
}
