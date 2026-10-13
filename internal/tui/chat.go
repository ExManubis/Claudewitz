package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type entryKind int

const (
	kindUser entryKind = iota
	kindAssistant
	kindThinking
	kindTool
	kindInfo
	kindError
)

type toolStatus int

const (
	toolPending toolStatus = iota
	toolOK
	toolFailed
)

type entry struct {
	kind entryKind
	text string

	// tool calls
	toolID     string
	toolName   string
	toolInput  json.RawMessage
	toolStatus toolStatus
	toolOutput string

	// render cache
	rendered string
	width    int
	dirty    bool
}

func (e *entry) setText(s string) { e.text = s; e.dirty = true }

// chat holds the transcript and renders it.
type chat struct {
	entries    []*entry
	byToolID   map[string]*entry
	streaming  map[int]*entry // content block index -> live entry
	md         *glamour.TermRenderer
	mdWidth    int
	showOutput bool
}

func newChat() *chat {
	return &chat{byToolID: map[string]*entry{}, streaming: map[int]*entry{}}
}

func (c *chat) add(e *entry) *entry {
	e.dirty = true
	c.entries = append(c.entries, e)
	if e.toolID != "" {
		c.byToolID[e.toolID] = e
	}
	return e
}

func (c *chat) info(format string, a ...any) {
	c.add(&entry{kind: kindInfo, text: fmt.Sprintf(format, a...)})
}

func (c *chat) error(format string, a ...any) {
	c.add(&entry{kind: kindError, text: fmt.Sprintf(format, a...)})
}

func (c *chat) clear() {
	*c = chat{byToolID: map[string]*entry{}, streaming: map[int]*entry{}, md: c.md, mdWidth: c.mdWidth, showOutput: c.showOutput}
}

func (c *chat) markAllDirty() {
	for _, e := range c.entries {
		e.dirty = true
	}
}

func (c *chat) renderer(width int) *glamour.TermRenderer {
	if c.md == nil || c.mdWidth != width {
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(width),
		)
		if err == nil {
			c.md = r
			c.mdWidth = width
		}
	}
	return c.md
}

func (c *chat) View(width int) string {
	var b strings.Builder
	for i, e := range c.entries {
		if e.dirty || e.width != width {
			e.rendered = c.renderEntry(e, width)
			e.width = width
			e.dirty = false
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(e.rendered)
	}
	return b.String()
}

func (c *chat) renderEntry(e *entry, width int) string {
	wrap := lipgloss.NewStyle().Width(width - 2).PaddingLeft(2)
	switch e.kind {
	case kindUser:
		return userLabel.Render("❯ You") + "\n" + wrap.Render(e.text)
	case kindAssistant:
		body := e.text
		if r := c.renderer(width - 2); r != nil {
			if out, err := r.Render(e.text); err == nil {
				body = strings.Trim(out, "\n")
			}
		}
		return assistLabel.Render("✻ Claude") + "\n" + body
	case kindThinking:
		t := strings.TrimSpace(e.text)
		if t == "" {
			t = "thinking…"
		}
		return wrap.Render(thinkingStyle.Render("∴ " + t))
	case kindTool:
		return c.renderTool(e, width)
	case kindError:
		return wrap.Render(errStyle.Render("✗ " + e.text))
	default:
		return wrap.Render(mutedStyle.Render(e.text))
	}
}

func (c *chat) renderTool(e *entry, width int) string {
	var icon string
	switch e.toolStatus {
	case toolPending:
		icon = warnStyle.Render("●")
	case toolOK:
		icon = okStyle.Render("●")
	default:
		icon = errStyle.Render("●")
	}
	head := fmt.Sprintf("%s %s %s", icon, toolName.Render(e.toolName), mutedStyle.Render(summarizeInput(e.toolName, e.toolInput, width-len(e.toolName)-8)))
	out := lipgloss.NewStyle().Width(width - 2).PaddingLeft(2).Render(head)
	if e.toolOutput != "" {
		lines := strings.Split(strings.TrimRight(e.toolOutput, "\n"), "\n")
		max := 4
		if c.showOutput {
			max = 40
		}
		more := ""
		if len(lines) > max {
			more = fmt.Sprintf("\n… %d more lines (ctrl+o to toggle)", len(lines)-max)
			lines = lines[:max]
		}
		body := strings.Join(lines, "\n") + more
		st := mutedStyle
		if e.toolStatus == toolFailed {
			st = errStyle
		}
		out += "\n" + lipgloss.NewStyle().PaddingLeft(4).Width(width-2).Render(st.Render("⎿ "+body))
	}
	return out
}

// summarizeInput produces a one-line description of a tool call.
func summarizeInput(tool string, raw json.RawMessage, max int) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return ""
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	var s string
	switch tool {
	case "Bash":
		s = pick("command")
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		s = pick("file_path", "notebook_path")
	case "Glob", "Grep":
		s = pick("pattern")
		if p := pick("path"); p != "" {
			s += " in " + p
		}
	case "WebFetch":
		s = pick("url")
	case "WebSearch":
		s = pick("query")
	case "Task", "Agent":
		s = pick("description", "prompt")
	default:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
		}
		s = strings.Join(parts, " ")
	}
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if max > 10 && len([]rune(s)) > max {
		s = string([]rune(s)[:max-1]) + "…"
	}
	return s
}

// toolResultText flattens a tool_result content field to text.
func toolResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			} else {
				parts = append(parts, "["+b.Type+"]")
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}
