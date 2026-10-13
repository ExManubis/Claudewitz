package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

const twoQuestions = `{"questions":[
 {"question":"Which colour?","header":"Colour","multiSelect":false,"options":[{"label":"Red"},{"label":"Blue"},{"label":"Green"}]},
 {"question":"Which fruits?","header":"Fruit","multiSelect":true,"options":[{"label":"Apple"},{"label":"Pear"},{"label":"Plum"}]}]}`

// withRequest returns a model whose permission queue holds one request for
// tool, with a matching transcript entry.
func withRequest(t *testing.T, tool, input string) *Model {
	t.Helper()
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	m.width, m.height = 120, 40
	m.chat.add(&entry{kind: kindTool, toolID: "tu1", toolName: tool, toolInput: json.RawMessage(input)})
	m.pending = []*claude.PermissionRequest{{RequestID: "r1", ToolName: tool, ToolUseID: "tu1", Input: json.RawMessage(input)}}
	m.startInteraction()
	return &m
}

func keys(m *Model, ks ...string) {
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.handlePermissionKey(msg)
	}
}

func TestSingleQuestionSubmitsOnPick(t *testing.T) {
	m := withRequest(t, toolAskUser, `{"questions":[{"question":"Which colour?","header":"Colour","options":[{"label":"Red"},{"label":"Blue"}]}]}`)
	if m.question == nil {
		t.Fatal("question dialog not started")
	}
	keys(m, "2")
	if len(m.pending) != 0 || m.question != nil {
		t.Fatal("picking an option should submit a lone question")
	}
	if a := m.chat.byToolID["tu1"].answers; len(a) != 1 || a[0] != [2]string{"Colour", "Blue"} {
		t.Fatalf("answers: %v", a)
	}
}

func TestOtherAndMultiSelectWithReview(t *testing.T) {
	m := withRequest(t, toolAskUser, twoQuestions)
	// Q1: "Other…" is the row after the three options.
	keys(m, "down", "down", "down", "enter", "T", "e", "a", "l", "enter")
	if m.question.idx != 1 {
		t.Fatalf("should move to question 2, at %d", m.question.idx)
	}
	// Q2: toggle Apple (space on first row) and Plum (number key).
	keys(m, "space", "3", "tab")
	if !m.question.review() {
		t.Fatal("tab should reach the review step")
	}
	keys(m, "enter")
	if len(m.pending) != 0 {
		t.Fatal("review enter should submit")
	}
	want := [][2]string{{"Colour", "Teal"}, {"Fruit", "Apple, Plum"}}
	got := m.chat.byToolID["tu1"].answers
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("answers: %v", got)
	}
}

func TestUnansweredBlocksSubmit(t *testing.T) {
	m := withRequest(t, toolAskUser, twoQuestions)
	keys(m, "tab", "tab", "enter") // skip both questions, try to submit
	if len(m.pending) != 1 || m.question.idx != 0 || m.question.warn == "" {
		t.Fatalf("should jump back to the unanswered question with a warning: idx=%d warn=%q", m.question.idx, m.question.warn)
	}
}

func TestDeclineQuestions(t *testing.T) {
	m := withRequest(t, toolAskUser, twoQuestions)
	keys(m, "esc")
	if len(m.pending) != 0 || m.chat.byToolID["tu1"].toolStatus != toolFailed {
		t.Fatal("esc should decline")
	}
}

func TestPlanApproveModes(t *testing.T) {
	m := withRequest(t, toolExitPlan, `{"plan":"# Plan"}`)
	if m.plan == nil {
		t.Fatal("plan dialog not started")
	}
	keys(m, "1")
	if len(m.pending) != 0 || m.permMode != "acceptEdits" {
		t.Fatalf("option 1 should approve into acceptEdits, mode=%s", m.permMode)
	}
	m = withRequest(t, toolExitPlan, `{"plan":"# Plan"}`)
	m.permMode = "plan"
	keys(m, "2")
	if m.permMode != "default" {
		t.Fatalf("option 2 should approve into default, mode=%s", m.permMode)
	}
}

func TestPlanPrefersFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "plan.md")
	if got := readPlanFile(p, "stale"); got != "stale" {
		t.Fatalf("missing file should fall back, got %q", got)
	}
	os.WriteFile(p, []byte("# Fresh plan\n"), 0o644)
	m := withRequest(t, toolExitPlan, `{"plan":"stale","planFilePath":"`+p+`"}`)
	if m.plan.plan != "# Fresh plan\n" || m.chat.byToolID["tu1"].planText != "# Fresh plan\n" {
		t.Fatalf("plan text: %q", m.plan.plan)
	}
}
