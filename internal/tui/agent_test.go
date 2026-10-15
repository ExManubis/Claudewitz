package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/sessions"
)

const agentInput = `{"description":"Find render code","subagent_type":"Explore","prompt":"look"}`

// agentChat starts an Agent call and has the sub-agent run n Reads, all
// answered except the last.
func agentChat(n int) (*chat, *entry) {
	tr := newTracker()
	c := newChat(tr)
	c.cwd = "/w"
	tr.toolUse(claude.ContentBlock{ID: "A", Name: "Agent", Input: json.RawMessage(agentInput)})
	e := c.add(&entry{kind: kindTool, toolID: "A", toolName: "Agent", toolInput: json.RawMessage(agentInput)})
	for i := range n {
		id := fmt.Sprint("s", i)
		tr.subagentMessage("A", claude.Message{Role: "assistant", Content: []claude.ContentBlock{
			{Type: "tool_use", ID: id, Name: "Read", Input: json.RawMessage(fmt.Sprintf(`{"file_path":"/w/f%d.go"}`, i))},
		}})
		if i < n-1 {
			tr.subagentMessage("A", claude.Message{Role: "user", Content: []claude.ContentBlock{
				{Type: "tool_result", ToolUseID: id, IsError: i == 1},
			}})
		}
	}
	return c, e
}

func TestAgentStepsRecordResults(t *testing.T) {
	c, _ := agentChat(3)
	steps := c.track.agent("A").steps
	if len(steps) != 3 {
		t.Fatalf("got %d steps", len(steps))
	}
	if steps[0].status != toolOK || steps[1].status != toolFailed || steps[2].status != toolPending {
		t.Fatalf("statuses %v %v %v", steps[0].status, steps[1].status, steps[2].status)
	}
}

func TestAgentTreeShowsNewestStepsUntilOpened(t *testing.T) {
	c, e := agentChat(8)
	v := ansi.Strip(c.View(90))
	if !strings.Contains(v, "⋯ 3 earlier steps") || strings.Contains(v, "f2.go") || !strings.Contains(v, "└─ ● Read f7.go") {
		t.Errorf("closed tree should show the newest %d steps:\n%s", maxAgentSteps, v)
	}
	e.expanded, e.dirty = true, true
	v = ansi.Strip(c.View(90))
	if strings.Contains(v, "earlier steps") || !strings.Contains(v, "├─ ● Read f0.go") {
		t.Errorf("open tree should show every step:\n%s", v)
	}
}

func TestAgentReportRendersMarkdown(t *testing.T) {
	c, e := agentChat(1)
	report := "## Findings\n\n- **glamour** renders replies\n"
	c.track.toolResult("A", report, false)
	e.toolStatus, e.toolOutput, e.dirty = toolOK, report, true
	v := ansi.Strip(c.View(90))
	if strings.Contains(v, "**glamour**") || !strings.Contains(v, "• glamour renders replies") {
		t.Errorf("report should render as Markdown:\n%s", v)
	}
}

// Resuming a session rebuilds each agent's steps from its own transcript.
func TestReplayLoadsAgentSteps(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	m.replay(transcriptMsg{
		info: sessions.Info{ID: "abcdef12"},
		msgs: []sessions.Message{
			{Role: "assistant", Content: []claude.ContentBlock{{Type: "tool_use", ID: "A", Name: "Agent", Input: json.RawMessage(agentInput)}}},
			{Role: "user", Content: []claude.ContentBlock{{Type: "tool_result", ToolUseID: "A", Content: json.RawMessage(`"done"`)}}},
		},
		subagents: map[string][]sessions.Message{"A": {
			{Role: "user", Content: []claude.ContentBlock{{Type: "text", Text: "look"}}},
			{Role: "assistant", Content: []claude.ContentBlock{{Type: "tool_use", ID: "s1", Name: "Grep", Input: json.RawMessage(`{"pattern":"renderEntry"}`)}}},
			{Role: "user", Content: []claude.ContentBlock{{Type: "tool_result", ToolUseID: "s1"}}},
		}},
	})
	a := m.track.agent("A")
	if a == nil || len(a.steps) != 1 || a.steps[0].name != "Grep" || a.steps[0].status != toolOK {
		t.Fatalf("agent steps not replayed: %+v", a)
	}
	if v := ansi.Strip(m.chat.View(90)); !strings.Contains(v, "└─ ● Grep renderEntry") {
		t.Errorf("view:\n%s", v)
	}
}
