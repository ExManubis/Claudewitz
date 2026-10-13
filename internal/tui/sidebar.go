package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
)

const sidebarWidth = 38

type fileChange struct {
	path     string
	added    int
	removed  int
	created  bool
	lastEdit time.Time
}

type todo struct {
	id      string
	content string
	active  string
	status  string // pending, in_progress, completed
}

type agentState struct {
	toolUseID  string
	taskID     string
	desc       string
	kind       string
	status     string // running, completed, failed, stopped
	background bool
	summary    string
	lastTool   string
	toolUses   int
	tokens     int
	started    time.Time
	duration   time.Duration
	recent     []string
}

func (a *agentState) push(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	a.recent = append(a.recent, line)
	if len(a.recent) > 4 {
		a.recent = a.recent[len(a.recent)-4:]
	}
}

func (a *agentState) running() bool { return a.status == "" || a.status == "running" }

// tracker derives sidebar state from the tool calls flowing through.
type tracker struct {
	files     []*fileChange
	fileIdx   map[string]*fileChange
	todos     []todo
	agents    []*agentState
	agentIdx  map[string]*agentState // by tool_use_id
	taskIdx   map[string]*agentState // by task_id
	pendingIn map[string]claude.ContentBlock
}

func newTracker() *tracker {
	return &tracker{
		fileIdx:   map[string]*fileChange{},
		agentIdx:  map[string]*agentState{},
		taskIdx:   map[string]*agentState{},
		pendingIn: map[string]claude.ContentBlock{},
	}
}

func (t *tracker) agent(toolUseID string) *agentState { return t.agentIdx[toolUseID] }

func isAgentTool(name string) bool { return name == "Task" || name == "Agent" }

// toolUse records a main-thread tool call.
func (t *tracker) toolUse(b claude.ContentBlock) {
	t.pendingIn[b.ID] = b
	var in map[string]any
	_ = json.Unmarshal(b.Input, &in)
	switch {
	case b.Name == "TodoWrite":
		t.todos = t.todos[:0]
		items, _ := in["todos"].([]any)
		for i, it := range items {
			m, _ := it.(map[string]any)
			t.todos = append(t.todos, todo{id: fmt.Sprint(i + 1), content: str(m, "content"), active: str(m, "activeForm"), status: str(m, "status")})
		}
	case b.Name == "TaskUpdate":
		id := str(in, "taskId")
		for i := range t.todos {
			if t.todos[i].id == id {
				if s := str(in, "status"); s != "" {
					t.todos[i].status = s
				}
				if s := str(in, "subject"); s != "" {
					t.todos[i].content = s
				}
				if s := str(in, "activeForm"); s != "" {
					t.todos[i].active = s
				}
			}
		}
		if str(in, "status") == "deleted" {
			kept := t.todos[:0]
			for _, td := range t.todos {
				if td.id != id {
					kept = append(kept, td)
				}
			}
			t.todos = kept
		}
	case isAgentTool(b.Name):
		if _, ok := t.agentIdx[b.ID]; !ok {
			a := &agentState{toolUseID: b.ID, desc: str(in, "description"), kind: str(in, "subagent_type"), status: "running", started: time.Now()}
			if a.kind == "" {
				a.kind = "agent"
			}
			t.agents = append(t.agents, a)
			t.agentIdx[b.ID] = a
		}
	}
}

var taskNumRe = regexp.MustCompile(`#(\d+)`)

// toolResult records the outcome of a main-thread tool call.
func (t *tracker) toolResult(id string, output string, isErr bool) {
	b, ok := t.pendingIn[id]
	if !ok {
		return
	}
	delete(t.pendingIn, id)
	if a := t.agentIdx[id]; a != nil && a.running() {
		// Background agents return immediately; their task events finish them.
		if !isErr && strings.Contains(output, "Async agent launched") {
			a.background = true
		} else {
			a.status = "completed"
			if isErr {
				a.status = "failed"
			}
			a.duration = time.Since(a.started)
		}
	}
	if isErr {
		return
	}
	var in map[string]any
	_ = json.Unmarshal(b.Input, &in)
	switch b.Name {
	case "TaskCreate":
		td := todo{content: str(in, "subject"), active: str(in, "activeForm"), status: "pending"}
		if m := taskNumRe.FindStringSubmatch(output); m != nil {
			td.id = m[1]
		} else {
			td.id = fmt.Sprint(len(t.todos) + 1)
		}
		t.todos = append(t.todos, td)
	case "Edit":
		d := computeDiff(str(in, "old_string"), str(in, "new_string"))
		a, r := diffStats(d)
		t.touch(str(in, "file_path"), a, r, false)
	case "MultiEdit":
		edits, _ := in["edits"].([]any)
		for _, e := range edits {
			m, _ := e.(map[string]any)
			a, r := diffStats(computeDiff(str(m, "old_string"), str(m, "new_string")))
			t.touch(str(in, "file_path"), a, r, false)
		}
	case "Write":
		lines := strings.Count(strings.TrimRight(str(in, "content"), "\n"), "\n") + 1
		t.touch(str(in, "file_path"), lines, 0, strings.Contains(output, "created"))
	case "NotebookEdit":
		t.touch(str(in, "notebook_path"), 0, 0, false)
	}
}

func (t *tracker) touch(path string, add, del int, created bool) {
	if path == "" {
		return
	}
	f := t.fileIdx[path]
	if f == nil {
		f = &fileChange{path: path, created: created}
		t.fileIdx[path] = f
		t.files = append(t.files, f)
	}
	f.added += add
	f.removed += del
	f.lastEdit = time.Now()
}

// task applies a sub-agent lifecycle event.
func (t *tracker) task(e *claude.TaskEvent) {
	a := t.agentIdx[e.ToolUseID]
	if a == nil && e.TaskID != "" {
		a = t.taskIdx[e.TaskID]
	}
	if a == nil {
		if e.ToolUseID == "" {
			return
		}
		a = &agentState{toolUseID: e.ToolUseID, desc: e.Description, kind: e.SubagentType, status: "running", started: time.Now()}
		t.agents = append(t.agents, a)
		t.agentIdx[e.ToolUseID] = a
	}
	if e.TaskID != "" {
		a.taskID = e.TaskID
		t.taskIdx[e.TaskID] = a
	}
	if e.SubagentType != "" {
		a.kind = e.SubagentType
	}
	if e.LastToolName != "" {
		a.lastTool = e.LastToolName
	}
	if e.Summary != "" {
		a.summary = e.Summary
	}
	if e.Usage.ToolUses > 0 {
		a.toolUses = e.Usage.ToolUses
	}
	if e.Usage.TotalTokens > 0 {
		a.tokens = e.Usage.TotalTokens
	}
	if e.Usage.DurationMS > 0 {
		a.duration = time.Duration(e.Usage.DurationMS) * time.Millisecond
	}
	switch status := firstNonEmpty(e.Patch.Status, e.Status); status {
	case "completed", "failed", "stopped", "killed":
		a.status = status
		if a.duration == 0 {
			a.duration = time.Since(a.started)
		}
	}
}

// subagentMessage records activity inside a sub-agent.
func (t *tracker) subagentMessage(parent string, msg claude.Message) {
	a := t.agentIdx[parent]
	if a == nil {
		return
	}
	for _, b := range msg.Content {
		switch b.Type {
		case "tool_use":
			a.lastTool = b.Name
			a.push(b.Name + " " + summarizeInput(b.Name, b.Input, 60))
		case "text":
			if msg.Role == "assistant" {
				a.push("“" + firstLine(b.Text) + "”")
			}
		}
	}
}

func (t *tracker) runningAgents() int {
	n := 0
	for _, a := range t.agents {
		if a.running() {
			n++
		}
	}
	return n
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func truncate(s string, w int) string {
	if w <= 1 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// truncateLeft keeps the end of a path-like string.
func truncateLeft(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return "…" + string(r[len(r)-w+1:])
}

// sidebarView renders the right-hand panel.
func (m Model) sidebarView(height int) string {
	w := sidebarWidth - 3 // border + padding
	t := m.track
	var sec []string

	// Header
	th := currentTheme()
	title := assistLabel.Render("✻ ") + gradientText("CLAUDEWITZ", true) + " "
	sec = append(sec, title+stripes(max(0, w-lipgloss.Width(title)), th.second, th.subtle))
	if m.initResp != nil && m.initResp.Account.SubscriptionType != "" {
		sec = append(sec, mutedStyle.Render(m.initResp.Account.SubscriptionType))
	}
	if m.sessionTitle != "" {
		sec = append(sec, textStyle.Render(truncate(m.sessionTitle, w)))
	}
	cwd := m.cwd()
	if cwd != "" {
		sec = append(sec, mutedStyle.Render(truncateLeft(prettyPath(cwd), w)))
	}
	meta := []string{}
	if m.model != "" {
		meta = append(meta, strings.TrimSpace(m.model+" "+m.opts.Effort))
	}
	if m.sessionID != "" {
		meta = append(meta, m.sessionID[:min(8, len(m.sessionID))])
	}
	if len(meta) > 0 {
		sec = append(sec, mutedStyle.Render(truncate(strings.Join(meta, " · "), w)))
	}

	// Usage
	if len(m.limits) > 0 || contextTokens(m.lastUsage) > 0 {
		sec = append(sec, sectionTitle.Render("Usage"))
		if c := contextTokens(m.lastUsage); c > 0 {
			sec = append(sec, meter("context", float64(c)/float64(m.contextWindow()), humanTokens(c), w))
		}
		for _, k := range []struct{ key, label string }{{"five_hour", "5-hour"}, {"seven_day", "weekly"}} {
			if lim, ok := m.limits[k.key]; ok {
				extra := fmt.Sprintf("%d%%", int(lim.Utilization*100+0.5))
				if lim.ResetsAt > 0 {
					if d := time.Until(time.Unix(lim.ResetsAt, 0)); d > 0 {
						extra += " ↻" + shortDur(d.Round(time.Minute))
					}
				}
				sec = append(sec, meter(k.label, lim.Utilization, extra, w))
			}
		}
	}

	// Todos
	if len(t.todos) > 0 {
		done := 0
		for _, td := range t.todos {
			if td.status == "completed" {
				done++
			}
		}
		sec = append(sec, sectionTitle.Render("Todos")+mutedStyle.Render(fmt.Sprintf(" %d/%d", done, len(t.todos))))
		for _, td := range t.todos {
			var line string
			switch td.status {
			case "completed":
				line = okStyle.Render("✓ ") + mutedStyle.Strikethrough(true).Render(truncate(td.content, w-2))
			case "in_progress":
				label := firstNonEmpty(td.active, td.content)
				line = warnStyle.Render("◐ ") + textStyle.Bold(true).Render(truncate(label, w-2))
			default:
				line = mutedStyle.Render("○ ") + textStyle.Render(truncate(td.content, w-2))
			}
			sec = append(sec, line)
		}
	}

	// Agents
	if len(t.agents) > 0 {
		hdr := sectionTitle.Render("Agents")
		if n := t.runningAgents(); n > 0 {
			hdr += mutedStyle.Render(fmt.Sprintf(" %d running", n))
		}
		sec = append(sec, hdr)
		start := 0
		if len(t.agents) > 5 {
			start = len(t.agents) - 5
		}
		for _, a := range t.agents[start:] {
			sec = append(sec, m.agentCard(a, w))
		}
	}

	// Files
	if len(t.files) > 0 {
		sec = append(sec, sectionTitle.Render("Modified files")+mutedStyle.Render(fmt.Sprintf(" %d", len(t.files))))
		for _, f := range t.files {
			stats := ""
			if f.added > 0 {
				stats += okStyle.Render(fmt.Sprintf("+%d", f.added))
			}
			if f.removed > 0 {
				if stats != "" {
					stats += " "
				}
				stats += errStyle.Render(fmt.Sprintf("-%d", f.removed))
			}
			name := relPath(f.path, cwd)
			nameW := w - lipgloss.Width(stats) - 1
			label := truncateLeft(name, nameW)
			if f.created {
				label = okStyle.Render(label)
			} else {
				label = textStyle.Render(label)
			}
			gap := w - lipgloss.Width(label) - lipgloss.Width(stats)
			sec = append(sec, label+strings.Repeat(" ", max(1, gap))+stats)
		}
	}

	if len(t.todos) == 0 && len(t.agents) == 0 && len(t.files) == 0 {
		sec = append(sec, "", mutedStyle.Render("Todos, sub-agents and edited"), mutedStyle.Render("files will show up here."))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, sec...)
	lines := strings.Split(body, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	return sidebarStyle.Width(sidebarWidth - 1).Height(height).Render(strings.Join(lines, "\n"))
}

func (m Model) agentCard(a *agentState, w int) string {
	var icon string
	switch {
	case a.running():
		icon = m.spin.View()
	case a.status == "completed":
		icon = okStyle.Render("✓")
	default:
		icon = errStyle.Render("✗")
	}
	head := icon + " " + agentStyle.Render(a.kind) + " " + textStyle.Render(truncate(a.desc, w-lipgloss.Width(a.kind)-3))
	var stats []string
	if a.toolUses > 0 {
		stats = append(stats, fmt.Sprintf("%d tools", a.toolUses))
	}
	if a.tokens > 0 {
		stats = append(stats, humanTokens(a.tokens)+" tok")
	}
	d := a.duration
	if a.running() {
		d = time.Since(a.started)
	}
	if d > 0 {
		stats = append(stats, shortSecs(d))
	}
	lines := []string{head}
	if len(stats) > 0 {
		lines = append(lines, mutedStyle.Render("  "+truncate(strings.Join(stats, " · "), w-2)))
	}
	if a.running() {
		for _, r := range a.recent {
			lines = append(lines, mutedStyle.Render("  ⎿ "+truncate(r, w-4)))
		}
	}
	return strings.Join(lines, "\n")
}

func meter(label string, frac float64, right string, w int) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	th := currentTheme()
	col := th.ok
	switch {
	case frac >= 0.9:
		col = th.err
	case frac >= 0.7:
		col = th.warn
	}
	head := mutedStyle.Render(label)
	gap := w - lipgloss.Width(label) - lipgloss.Width(right)
	head += strings.Repeat(" ", max(1, gap)) + lipgloss.NewStyle().Foreground(col).Render(right)
	barW := w
	filled := int(frac*float64(barW) + 0.5)
	bar := lipgloss.NewStyle().Foreground(col).Render(strings.Repeat("━", filled)) +
		lipgloss.NewStyle().Foreground(th.subtle).Render(strings.Repeat("━", barW-filled))
	return head + "\n" + bar
}

func shortSecs(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func relPath(p, base string) string {
	if base != "" {
		if r, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return prettyPath(p)
}
