package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

// maxProseWidth caps how wide assistant and thinking text wraps, so
// replies stay readable in wide terminals.
const maxProseWidth = 120

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
	answers    [][2]string // AskUserQuestion: header → answer, as sent
	feedback   string      // ExitPlanMode: why the plan was sent back
	planText   string      // ExitPlanMode: plan as read from its file
	images     []string    // user messages: names of attached images

	// thinking: when the block started streaming, and how long it took
	// once it stopped (zero while running, and for replayed history)
	thinkStart time.Time
	thought    time.Duration

	expanded bool // opened individually from the focused chat

	// streamed replies and thinking: text is revealed at a steady pace,
	// shown bytes at a time (see reveal.go)
	paced bool
	shown int

	// render cache
	rendered string
	width    int
	dirty    bool
	md       streamMD // assistant and thinking Markdown
}

// setText replaces an entry's text. Paced entries redraw as the reveal
// catches up rather than on every change.
func (e *entry) setText(s string) {
	e.text = s
	e.dirty = e.dirty || !e.paced
}

// open reports whether an entry shows in full: everything is open with
// ctrl+o, or one entry at a time with space in the focused chat.
func (c *chat) open(e *entry) bool { return c.expanded || e.expanded }

// chat holds the transcript and renders it.
type chat struct {
	entries   []*entry
	byToolID  map[string]*entry
	streaming map[int]*entry                // content block index -> live entry
	md        map[int]*glamour.TermRenderer // by wrap width
	quiet     map[int]*glamour.TermRenderer // thinking, by wrap width
	expanded  bool
	track     *tracker
	cwd       string

	// Selection in the focused chat, and where each entry landed in the
	// last render (line offset and height), for scrolling and clicks.
	sel     int
	focused bool
	offsets []int
	heights []int
}

func newChat(t *tracker) *chat {
	return &chat{byToolID: map[string]*entry{}, streaming: map[int]*entry{}, track: t, sel: -1}
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

func (c *chat) reset(t *tracker) {
	c.entries = nil
	c.byToolID = map[string]*entry{}
	c.streaming = map[int]*entry{}
	c.track = t
	c.sel = -1
}

func (c *chat) markAllDirty() {
	for _, e := range c.entries {
		e.dirty, e.md = true, streamMD{}
	}
	c.md, c.quiet = nil, nil // rebuilt in the new theme
}

// markAgentsDirty forces re-rendering of running sub-agent entries.
func (c *chat) markAgentsDirty() {
	for _, e := range c.entries {
		if e.kind == kindTool && isAgentTool(e.toolName) {
			if a := c.track.agent(e.toolID); a != nil && a.running() {
				e.dirty = true
			}
		}
	}
}

func (c *chat) renderer(width int) *glamour.TermRenderer {
	return cachedRenderer(&c.md, width, glamour.WithStyles(markdownStyle()), glamour.WithChromaFormatter("terminal16m"))
}

func (c *chat) quietRenderer(width int) *glamour.TermRenderer {
	return cachedRenderer(&c.quiet, width, glamour.WithStyles(quietMarkdownStyle()))
}

// cachedRenderer returns the renderer for width from cache, building it
// with opts on first use. Replies, thinking and agent reports each wrap
// at their own width, so one renderer per width avoids rebuilding.
func cachedRenderer(cache *map[int]*glamour.TermRenderer, width int, opts ...glamour.TermRendererOption) *glamour.TermRenderer {
	if r, ok := (*cache)[width]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(append(opts, glamour.WithWordWrap(width))...)
	if err != nil {
		return nil
	}
	if len(*cache) >= 8 || *cache == nil { // resizing walks through many widths
		*cache = map[int]*glamour.TermRenderer{}
	}
	(*cache)[width] = r
	return r
}

func (c *chat) View(width int) string {
	var b strings.Builder
	var prev *entry
	line := 0
	c.offsets = c.offsets[:0]
	c.heights = c.heights[:0]
	for i, e := range c.entries {
		if e.dirty || e.width != width {
			e.rendered = c.renderEntry(e, width)
			e.width, e.dirty = width, false
		}
		if prev != nil {
			b.WriteString("\n")
			line++
			// Breathing room around user messages and between turns.
			if e.kind == kindUser || prev.kind == kindUser || (e.kind == kindAssistant) != (prev.kind == kindAssistant) {
				b.WriteString("\n")
				line++
			}
		}
		out := e.rendered
		if c.focused && i == c.sel {
			out = markSelected(out)
		}
		h := strings.Count(out, "\n") + 1
		c.offsets = append(c.offsets, line)
		c.heights = append(c.heights, h)
		b.WriteString(out)
		line += h - 1
		prev = e
	}
	return b.String()
}

// markSelected draws an accent bar down the left edge of a rendered entry.
func markSelected(s string) string {
	bar := lipgloss.NewStyle().Foreground(currentTheme().accent).Bold(true).Render("▌")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = bar + ansi.TruncateLeft(l, 1, "")
	}
	return strings.Join(lines, "\n")
}

func (c *chat) renderEntry(e *entry, width int) string {
	wrap := lipgloss.NewStyle().Width(width - 2).PaddingLeft(2)
	switch e.kind {
	case kindUser:
		body := textStyle.Render(e.text)
		if len(e.images) > 0 {
			var chips []string
			for _, name := range e.images {
				chips = append(chips, lipgloss.NewStyle().Foreground(currentTheme().user).Render(icons.image+" ")+mutedStyle.Render(name))
			}
			if strings.TrimSpace(e.text) == "" {
				body = strings.Join(chips, "  ")
			} else {
				body += "\n" + strings.Join(chips, "  ")
			}
		}
		return userBlock.Width(width - 2).Render(body)
	case kindAssistant:
		body := e.visibleText()
		w := min(width-2, maxProseWidth)
		if r := c.renderer(w); r != nil {
			if out, err := e.md.render(body, w, r); err == nil {
				body = strings.Trim(out, "\n")
			}
		}
		return body
	case kindThinking:
		return c.renderThinking(e, width)
	case kindTool:
		return c.renderTool(e, width)
	case kindError:
		return wrap.Render(errStyle.Render("✗ " + e.text))
	default:
		return wrap.Render(mutedStyle.Render(e.text))
	}
}

// maxThinkingLines is how much of the thinking box shows while closed:
// the newest lines, so a streaming block reads like a live tail.
const maxThinkingLines = 10

// renderThinking draws thinking as quiet Markdown in a tinted box, with
// how long it took underneath once it's done. Models that keep their
// thinking hidden get only that footer.
func (c *chat) renderThinking(e *entry, width int) string {
	var footer string
	if e.thought >= time.Second {
		footer = "  " + thinkingStyle.Render("∴ Thought for "+shortSecs(e.thought))
	}
	t := strings.TrimSpace(e.visibleText())
	if t == "" {
		if footer != "" {
			return footer
		}
		return "  " + thinkingStyle.Render("∴ Thinking…")
	}
	inner := min(width-4, maxProseWidth-2) - 2 // left margin, then 1 col of padding each side
	body := lipgloss.NewStyle().Width(inner).Render(t)
	if r := c.quietRenderer(inner); r != nil {
		if out, err := e.md.render(t, inner, r); err == nil {
			body = strings.Trim(out, "\n")
		}
	}
	lines := strings.Split(body, "\n")
	box := lipgloss.NewStyle().Background(currentTheme().thinkBg).Foreground(currentTheme().muted)
	if !c.open(e) && len(lines) > maxThinkingLines+1 {
		hint := box.Italic(true).Render(fmt.Sprintf("… %d earlier lines (ctrl+o)", len(lines)-maxThinkingLines))
		lines = append([]string{hint}, lines[len(lines)-maxThinkingLines:]...)
	}
	for i, l := range lines {
		fill := max(0, inner-ansi.StringWidth(l))
		lines[i] = "  " + box.Render(" ") + l + box.Render(strings.Repeat(" ", fill+1))
	}
	out := strings.Join(lines, "\n")
	if footer != "" {
		out += "\n" + footer
	}
	return out
}

func (c *chat) renderTool(e *entry, width int) string {
	th := currentTheme()
	var icon string
	switch e.toolStatus {
	case toolPending:
		icon = lipgloss.NewStyle().Foreground(th.warn).Render("●")
	case toolOK:
		icon = okStyle.Render("●")
	default:
		icon = errStyle.Render("●")
	}
	name := e.toolName
	nameStyle := toolName
	if isAgentTool(name) {
		nameStyle = agentStyle
	}
	summary := summarizeInput(name, e.toolInput, width-lipgloss.Width(name)-8)
	if fp := filePathOf(e.toolInput); fp != "" {
		summary = truncateLeft(relPath(fp, c.cwd), max(10, width-lipgloss.Width(name)-8))
	}
	label := name
	switch name {
	case toolAskUser:
		label, nameStyle = "Question", agentStyle
	case toolExitPlan:
		label = "Plan"
	}
	head := "  " + icon + " " + nameStyle.Render(label) + " " + mutedStyle.Render(summary)
	body := c.toolBody(e, width-6)
	if body == "" {
		return head
	}
	return head + "\n" + indent(body, "    ")
}

// toolBody is what goes under a tool call header.
func (c *chat) toolBody(e *entry, width int) string {
	var in map[string]any
	_ = json.Unmarshal(e.toolInput, &in)
	maxLines := 10
	if c.open(e) {
		maxLines = 80
	}
	if isAgentTool(e.toolName) {
		return c.agentBody(e, width)
	}
	switch e.toolName {
	case toolAskUser:
		return c.questionBody(e, width)
	case toolExitPlan:
		return c.planBody(e, width)
	}
	if e.toolStatus == toolFailed {
		return errStyle.Render("⎿ " + clipText(e.toolOutput, width, 6))
	}
	if e.toolStatus == toolPending {
		return ""
	}
	fp := filePathOf(e.toolInput)
	switch e.toolName {
	case "Edit":
		d := computeDiff(str(in, "old_string"), str(in, "new_string"))
		a, r := diffStats(d)
		return mutedStyle.Render(fmt.Sprintf("⎿ %s %s", okStyle.Render(fmt.Sprintf("+%d", a)), errStyle.Render(fmt.Sprintf("-%d", r)))) + "\n" + renderDiff(d, fp, width, maxLines)
	case "MultiEdit":
		var all []diffLine
		edits, _ := in["edits"].([]any)
		for i, ed := range edits {
			m, _ := ed.(map[string]any)
			if i > 0 {
				all = append(all, diffLine{kind: '@', text: "⋯"})
			}
			all = append(all, computeDiff(str(m, "old_string"), str(m, "new_string"))...)
		}
		a, r := diffStats(all)
		return mutedStyle.Render(fmt.Sprintf("⎿ %d edits %s %s", len(edits), okStyle.Render(fmt.Sprintf("+%d", a)), errStyle.Render(fmt.Sprintf("-%d", r)))) + "\n" + renderDiff(all, fp, width, maxLines)
	case "Write":
		d := addedLines(str(in, "content"))
		return mutedStyle.Render(fmt.Sprintf("⎿ wrote %d lines", len(d))) + "\n" + renderDiff(d, fp, width, min(maxLines, 6))
	case "Read":
		n := strings.Count(strings.TrimRight(e.toolOutput, "\n"), "\n") + 1
		return mutedStyle.Render(fmt.Sprintf("⎿ read %d lines", n))
	case "TodoWrite", "TaskCreate", "TaskUpdate":
		return mutedStyle.Render("⎿ todos updated")
	}
	if strings.TrimSpace(e.toolOutput) == "" {
		return mutedStyle.Render("⎿ (no output)")
	}
	lim := 4
	if c.open(e) {
		lim = 60
	}
	return mutedStyle.Render("⎿ " + clipText(e.toolOutput, width-2, lim))
}

func (c *chat) agentBody(e *entry, width int) string {
	a := c.track.agent(e.toolID)
	var lines []string
	if a != nil {
		stats := []string{}
		if a.toolUses > 0 {
			stats = append(stats, fmt.Sprintf("%d tool uses", a.toolUses))
		}
		if a.tokens > 0 {
			stats = append(stats, humanTokens(a.tokens)+" tokens")
		}
		if a.running() {
			if a.lastTool != "" {
				stats = append(stats, "running "+a.lastTool)
			} else {
				stats = append(stats, "starting")
			}
		} else if a.duration > 0 {
			stats = append(stats, shortSecs(a.duration))
		}
		if len(stats) > 0 {
			lines = append(lines, mutedStyle.Render("⎿ "+strings.Join(stats, " · ")))
		}
		if len(a.steps) > 0 {
			lines = append(lines, c.agentSteps(e, a, width))
		} else if a.running() {
			for _, r := range a.recent {
				lines = append(lines, mutedStyle.Render("  "+truncate(r, width-2)))
			}
		}
	}
	if a != nil && a.background {
		if a.running() {
			lines = append(lines, mutedStyle.Render("⎿ running in the background"))
		} else if a.summary != "" {
			lim := 3
			if c.open(e) {
				lim = 40
			}
			lines = append(lines, mutedStyle.Render("⎿ "+clipText(a.summary, width-2, lim)))
		}
	} else if e.toolStatus == toolFailed && e.toolOutput != "" {
		lim := 3
		if c.open(e) {
			lim = 40
		}
		lines = append(lines, errStyle.Render("⎿ "+clipText(agentReport(e.toolOutput), width-2, lim)))
	} else if e.toolStatus == toolOK && e.toolOutput != "" {
		lines = append(lines, c.agentResult(e, width))
	}
	return strings.Join(lines, "\n")
}

// maxAgentSteps is how many of a sub-agent's tool calls show while its
// entry is closed: the newest, so a running agent reads as a live tail.
const maxAgentSteps = 5

// agentSteps draws a sub-agent's tool calls as a tree, one line each.
func (c *chat) agentSteps(e *entry, a *agentState, width int) string {
	th := currentTheme()
	steps := a.steps
	var lines []string
	if hidden := len(steps) - maxAgentSteps; hidden > 0 && !c.open(e) {
		steps = steps[hidden:]
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("├─ ⋯ %d earlier steps", hidden)))
	}
	for i, st := range steps {
		branch := "├─ "
		if i == len(steps)-1 {
			branch = "└─ "
		}
		var icon string
		switch {
		case st.status == toolOK:
			icon = okStyle.Render("●")
		case st.status == toolFailed:
			icon = errStyle.Render("●")
		case a.running():
			icon = lipgloss.NewStyle().Foreground(th.warn).Render("●")
		default: // never answered: the agent was stopped mid-call
			icon = mutedStyle.Render("●")
		}
		room := max(10, width-lipgloss.Width(st.name)-6)
		summary := summarizeInput(st.name, st.input, room)
		if fp := filePathOf(st.input); fp != "" {
			summary = truncateLeft(relPath(fp, c.cwd), room)
		}
		lines = append(lines, mutedStyle.Render(branch)+icon+" "+toolName.Render(st.name)+" "+mutedStyle.Render(summary))
	}
	return strings.Join(lines, "\n")
}

// maxAgentResultLines is how much of a sub-agent's report shows closed.
const maxAgentResultLines = 10

// agentResult renders a finished sub-agent's report as Markdown.
func (c *chat) agentResult(e *entry, width int) string {
	report := agentReport(e.toolOutput)
	body := mutedStyle.Render(clipText(report, width-2, maxAgentResultLines))
	if r := c.renderer(width); r != nil {
		if out, err := r.Render(report); err == nil {
			lines := strings.Split(strings.Trim(out, "\n"), "\n")
			if hidden := len(lines) - maxAgentResultLines; hidden > 0 && !c.open(e) {
				lines = append(lines[:maxAgentResultLines], mutedStyle.Render(fmt.Sprintf("  … %d more lines (ctrl+o)", hidden)))
			}
			body = strings.Join(lines, "\n")
		}
	}
	return body
}

// agentReport strips the harness frame around a sub-agent's final report:
// a one-line preamble, then the report indented by two spaces, then
// optional metadata back at column zero.
func agentReport(s string) string {
	if !strings.HasPrefix(s, "[Subagent hand-back]") {
		return s
	}
	_, body, ok := strings.Cut(s, "\n")
	if !ok {
		return s
	}
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if l != "" && !strings.HasPrefix(l, "  ") {
			break
		}
		lines = append(lines, strings.TrimPrefix(l, "  "))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (c *chat) questionBody(e *entry, width int) string {
	switch {
	case len(e.answers) > 0:
		var lines []string
		for _, a := range e.answers {
			lines = append(lines, mutedStyle.Render("⎿ ")+textStyle.Render(truncate(a[0], width/2))+mutedStyle.Render(" → ")+okStyle.Render(truncate(a[1], width/2)))
		}
		return strings.Join(lines, "\n")
	case e.toolStatus == toolPending:
		return mutedStyle.Render("⎿ waiting for your answer")
	case e.toolStatus == toolFailed:
		return mutedStyle.Render("⎿ declined")
	}
	// Replayed history: only Claude Code's summary of the answers survives.
	return mutedStyle.Render("⎿ " + clipText(e.toolOutput, width-2, 4))
}

func (c *chat) planBody(e *entry, width int) string {
	var in struct {
		Plan string `json:"plan"`
	}
	_ = json.Unmarshal(e.toolInput, &in)
	plan := firstNonEmpty(e.planText, in.Plan)
	body := plan
	if r := c.renderer(width - 4); r != nil {
		if out, err := r.Render(plan); err == nil {
			body = strings.Trim(out, "\n")
		}
	}
	// Keep a pending plan whole; collapse settled ones unless expanded.
	if lines := strings.Split(body, "\n"); e.toolStatus != toolPending && !c.open(e) && len(lines) > 12 {
		body = strings.Join(lines[:12], "\n") + "\n" + mutedStyle.Render(fmt.Sprintf("  … %d more lines (ctrl+o to expand)", len(lines)-12))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(currentTheme().accent).Width(width - 2).Render(body)
	var status string
	switch {
	case e.toolStatus == toolPending:
		status = warnStyle.Render("⎿ waiting for your review")
	case e.feedback != "" || e.toolStatus == toolFailed:
		status = mutedStyle.Render("⎿ sent back: ") + textStyle.Render(truncate(firstNonEmpty(e.feedback, firstLine(e.toolOutput)), width-14))
	default:
		status = okStyle.Render("⎿ approved")
	}
	return box + "\n" + status
}

func clipText(s string, width, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	more := 0
	if len(lines) > maxLines {
		more = len(lines) - maxLines
		lines = lines[:maxLines]
	}
	for i, l := range lines {
		lines[i] = truncate(expandTabs(l), width)
	}
	out := strings.Join(lines, "\n  ")
	if more > 0 {
		out += fmt.Sprintf("\n  … %d more lines (ctrl+o to expand)", more)
	}
	return out
}

func indent(s, pre string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pre + l
	}
	return strings.Join(lines, "\n")
}

func filePathOf(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	return firstNonEmpty(str(m, "file_path"), str(m, "notebook_path"))
}

// summarizeInput produces a one-line description of a tool call.
func summarizeInput(tool string, raw json.RawMessage, max int) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return ""
	}
	var s string
	switch tool {
	case "Bash":
		s = str(m, "command")
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		s = filepath.Base(firstNonEmpty(str(m, "file_path"), str(m, "notebook_path")))
	case "Glob", "Grep":
		s = str(m, "pattern")
		if p := str(m, "path"); p != "" {
			s += " in " + p
		}
	case "WebFetch":
		s = str(m, "url")
	case "WebSearch":
		s = str(m, "query")
	case "Task", "Agent":
		s = firstNonEmpty(str(m, "description"), str(m, "prompt"))
		if t := str(m, "subagent_type"); t != "" {
			s = t + " · " + s
		}
	case "TodoWrite":
		items, _ := m["todos"].([]any)
		s = fmt.Sprintf("%d items", len(items))
	case "TaskCreate", "TaskUpdate":
		s = firstNonEmpty(str(m, "subject"), str(m, "status"))
	case "Skill":
		s = str(m, "skill")
	case toolAskUser:
		var qs []map[string]any
		if raw, err := json.Marshal(m["questions"]); err == nil {
			_ = json.Unmarshal(raw, &qs)
		}
		if len(qs) > 0 {
			s = str(qs[0], "question")
			if len(qs) > 1 {
				s += fmt.Sprintf(" (+%d more)", len(qs)-1)
			}
		}
	case toolExitPlan:
		s = "plan ready for review"
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
	if max > 10 {
		s = truncate(s, max)
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
