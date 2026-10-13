// Package tui is the Bubble Tea front end.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
)

var permissionModes = []string{"default", "acceptEdits", "plan"}

type eventMsg claude.Event

// Model is the root Bubble Tea model.
type Model struct {
	opts claude.Options
	ctx  context.Context
	proc *claude.Process

	chat     *chat
	vp       viewport.Model
	input    textarea.Model
	spin     spinner.Model
	width    int
	height   int
	ready    bool
	follow   bool // keep viewport pinned to bottom
	busy     bool
	stopped  bool // user interrupted the current turn
	status   string
	turnFrom time.Time

	// session state
	init      *claude.InitEvent
	sessionID string
	model     string
	permMode  string
	costUSD   float64
	lastUsage claude.Usage
	limits    map[string]claude.RateLimitWindow

	// permissions
	pending     []*claude.PermissionRequest
	alwaysAllow map[string]bool
}

// New builds the model; the CLI is started from Init.
func New(ctx context.Context, opts claude.Options) Model {
	ta := textarea.New()
	ta.Placeholder = "Ask Claude anything…  (enter to send, ctrl+j for newline, /help)"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")
	ta.Focus()

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(colAccent)))

	pm := opts.PermissionMode
	if pm == "" {
		pm = "default"
	}
	return Model{
		opts:        opts,
		ctx:         ctx,
		chat:        newChat(),
		input:       ta,
		spin:        sp,
		follow:      true,
		permMode:    pm,
		model:       opts.Model,
		limits:      map[string]claude.RateLimitWindow{},
		alwaysAllow: map[string]bool{},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spin.Tick, startCmd(m.ctx, m.opts))
}

type startedMsg struct {
	proc *claude.Process
	err  error
}

func startCmd(ctx context.Context, opts claude.Options) tea.Cmd {
	return func() tea.Msg {
		p, err := claude.Start(ctx, opts)
		return startedMsg{p, err}
	}
}

func waitEvent(p *claude.Process) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-p.Events()
		if !ok {
			return nil
		}
		return eventMsg(ev)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(msg.Width - 4)
		m.layout()
		m.ready = true
		m.refresh()

	case startedMsg:
		if msg.err != nil {
			m.chat.error("could not start claude: %v", msg.err)
			m.refresh()
			return m, nil
		}
		m.proc = msg.proc
		m.status = "starting"
		cmds = append(cmds, waitEvent(m.proc))

	case eventMsg:
		cmds = append(cmds, m.handleEvent(claude.Event(msg)))
		if m.proc != nil && !msg.Exited {
			cmds = append(cmds, waitEvent(m.proc))
		}
		m.refresh()

	case spinner.TickMsg:
		var c tea.Cmd
		m.spin, c = m.spin.Update(msg)
		cmds = append(cmds, c)

	case tea.MouseMsg:
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		m.follow = m.vp.AtBottom()
		cmds = append(cmds, c)

	case tea.KeyMsg:
		if len(m.pending) > 0 {
			return m, m.handlePermissionKey(msg)
		}
		switch msg.String() {
		case "ctrl+c":
			if m.busy && m.proc != nil {
				_ = m.proc.Interrupt()
				m.stopped = true
				m.chat.info("interrupted")
				m.refresh()
				return m, nil
			}
			return m, m.quit()
		case "ctrl+d":
			return m, m.quit()
		case "esc":
			if m.busy && m.proc != nil {
				_ = m.proc.Interrupt()
				m.stopped = true
				m.chat.info("interrupted")
				m.refresh()
			}
			return m, nil
		case "shift+tab":
			m.cyclePermissionMode()
			return m, nil
		case "ctrl+o":
			m.chat.showOutput = !m.chat.showOutput
			m.chat.markAllDirty()
			m.refresh()
			return m, nil
		case "pgup", "pgdown", "ctrl+u":
			var c tea.Cmd
			m.vp, c = m.vp.Update(msg)
			m.follow = m.vp.AtBottom()
			return m, c
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.Reset()
			return m, m.submit(text)
		}
		var c tea.Cmd
		m.input, c = m.input.Update(msg)
		cmds = append(cmds, c)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) quit() tea.Cmd {
	if m.proc != nil {
		_ = m.proc.Close()
	}
	return tea.Quit
}

func (m *Model) handleEvent(ev claude.Event) tea.Cmd {
	switch {
	case ev.Init != nil:
		m.init = ev.Init
		m.sessionID = ev.Init.SessionID
		m.model = ev.Init.Model
		m.permMode = ev.Init.PermissionMode
		if m.status == "starting" {
			m.status = ""
		}

	case ev.Status != "":
		m.status = ev.Status

	case ev.Stream != nil:
		m.handleStream(ev.Stream)

	case ev.Message != nil:
		m.handleMessage(ev.Message)

	case ev.Result != nil:
		r := ev.Result
		m.busy = false
		m.status = ""
		m.chat.streaming = map[int]*entry{}
		m.costUSD += r.TotalCostUSD
		if r.SessionID != "" {
			m.sessionID = r.SessionID
		}
		if m.stopped {
			m.stopped = false
			return nil
		}
		if r.IsError && r.Subtype != "success" {
			m.chat.error("turn ended: %s %s", r.Subtype, r.Result)
		}
		m.chat.info("%s · %d turns · %s",
			(time.Duration(r.DurationMS) * time.Millisecond).Round(100*time.Millisecond),
			r.NumTurns, formatTokens(r.Usage))

	case ev.RateLimit != nil:
		for k, w := range ev.RateLimit.Info.Windows {
			m.limits[k] = w
		}
		if ev.RateLimit.Info.Status != "allowed" && ev.RateLimit.Info.Status != "" {
			m.chat.error("rate limit: %s (resets %s)", ev.RateLimit.Info.Status,
				time.Unix(ev.RateLimit.Info.ResetsAt, 0).Format("15:04"))
		}

	case ev.Permission != nil:
		p := ev.Permission
		if m.alwaysAllow[p.ToolName] && m.proc != nil {
			_ = m.proc.Respond(p, claude.PermissionDecision{Behavior: "allow"})
			return nil
		}
		m.pending = append(m.pending, p)
		m.input.Blur()
		m.layout()

	case ev.Exited:
		m.busy = false
		m.proc = nil
		if ev.Err != nil {
			m.chat.error("claude exited: %v", ev.Err)
		} else {
			m.chat.info("claude exited")
		}

	case ev.Err != nil:
		m.chat.error("%v", ev.Err)
	}
	return nil
}

func (m *Model) handleStream(s *claude.StreamEvent) {
	if s.ParentToolUseID != nil {
		return // sub-agent chatter
	}
	e := s.Event
	switch e.Type {
	case "message_start":
		m.chat.streaming = map[int]*entry{}
	case "content_block_start":
		if e.ContentBlock == nil {
			return
		}
		switch e.ContentBlock.Type {
		case "text":
			m.chat.streaming[e.Index] = m.chat.add(&entry{kind: kindAssistant, text: e.ContentBlock.Text})
		case "thinking":
			m.chat.streaming[e.Index] = m.chat.add(&entry{kind: kindThinking, text: e.ContentBlock.Thinking})
		}
	case "content_block_delta":
		live := m.chat.streaming[e.Index]
		if live == nil {
			return
		}
		switch e.Delta.Type {
		case "text_delta":
			live.setText(live.text + e.Delta.Text)
		case "thinking_delta":
			live.setText(live.text + e.Delta.Thinking)
		}
	}
}

func (m *Model) handleMessage(ev *claude.MessageEvent) {
	if ev.ParentToolUseID != nil {
		return
	}
	for _, b := range ev.Message.Content {
		switch b.Type {
		case "tool_use":
			if _, seen := m.chat.byToolID[b.ID]; !seen {
				m.chat.add(&entry{kind: kindTool, toolID: b.ID, toolName: b.Name, toolInput: b.Input})
			}
		case "tool_result":
			if t, ok := m.chat.byToolID[b.ToolUseID]; ok {
				t.toolOutput = toolResultText(b.Content)
				if b.IsError {
					t.toolStatus = toolFailed
				} else {
					t.toolStatus = toolOK
				}
				t.dirty = true
			}
		}
	}
	if ev.Message.Usage != nil && ev.Message.Role == "assistant" {
		m.lastUsage = *ev.Message.Usage
	}
}

func (m *Model) handlePermissionKey(k tea.KeyMsg) tea.Cmd {
	p := m.pending[0]
	var d *claude.PermissionDecision
	switch k.String() {
	case "y", "enter":
		d = &claude.PermissionDecision{Behavior: "allow"}
	case "a":
		m.alwaysAllow[p.ToolName] = true
		d = &claude.PermissionDecision{Behavior: "allow"}
	case "n", "esc":
		d = &claude.PermissionDecision{Behavior: "deny", Message: "The user denied this tool call."}
	case "ctrl+c":
		d = &claude.PermissionDecision{Behavior: "deny", Message: "The user denied this tool call and interrupted."}
		if m.proc != nil {
			_ = m.proc.Respond(p, *d)
			_ = m.proc.Interrupt()
			m.stopped = true
		}
		m.pending = m.pending[1:]
		m.afterPermission()
		return nil
	default:
		return nil
	}
	if m.proc != nil {
		_ = m.proc.Respond(p, *d)
	}
	if d.Behavior == "deny" {
		if t, ok := m.chat.byToolID[p.ToolUseID]; ok {
			t.toolStatus = toolFailed
			t.toolOutput = "denied"
			t.dirty = true
		}
	}
	m.pending = m.pending[1:]
	m.afterPermission()
	return nil
}

func (m *Model) afterPermission() {
	if len(m.pending) == 0 {
		m.input.Focus()
	}
	m.layout()
	m.refresh()
}

func (m *Model) cyclePermissionMode() {
	next := permissionModes[0]
	for i, pm := range permissionModes {
		if pm == m.permMode {
			next = permissionModes[(i+1)%len(permissionModes)]
		}
	}
	m.permMode = next
	if m.proc != nil {
		_ = m.proc.SetPermissionMode(next)
	}
}

func (m *Model) submit(text string) tea.Cmd {
	if strings.HasPrefix(text, "/") {
		if cmd, handled := m.slash(text); handled {
			m.refresh()
			return cmd
		}
	}
	if m.proc == nil {
		m.chat.error("claude is not running — /new to start a fresh session")
		m.refresh()
		return nil
	}
	m.chat.add(&entry{kind: kindUser, text: text})
	if err := m.proc.Send(text); err != nil {
		m.chat.error("send failed: %v", err)
	}
	if !m.busy {
		m.busy = true
		m.turnFrom = time.Now()
	}
	m.follow = true
	m.refresh()
	return nil
}

// slash handles commands owned by the front end. Unknown ones are passed
// through to Claude Code, which understands its own slash commands.
func (m *Model) slash(text string) (tea.Cmd, bool) {
	fields := strings.Fields(text)
	arg := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	switch fields[0] {
	case "/help":
		m.chat.info(helpText)
	case "/quit", "/exit":
		return m.quit(), true
	case "/new", "/clear":
		return m.restart(claude.Options{}), true
	case "/resume":
		if arg == "" {
			return m.restart(claude.Options{Continue: true}), true
		}
		return m.restart(claude.Options{Resume: arg}), true
	case "/model":
		if arg == "" {
			m.chat.info("model: %s", m.model)
			return nil, true
		}
		if m.proc != nil {
			_ = m.proc.SetModel(arg)
		}
		m.opts.Model = arg
		m.model = arg
		m.chat.info("model → %s", arg)
	case "/mode":
		if arg == "" {
			m.chat.info("permission mode: %s (modes: %s)", m.permMode, strings.Join(permissionModes, ", "))
			return nil, true
		}
		m.permMode = arg
		if m.proc != nil {
			_ = m.proc.SetPermissionMode(arg)
		}
		m.chat.info("permission mode → %s", arg)
	case "/session":
		m.chat.info("session %s\ncwd %s", m.sessionID, m.cwd())
	default:
		return nil, false
	}
	return nil, true
}

func (m *Model) restart(o claude.Options) tea.Cmd {
	if m.proc != nil {
		_ = m.proc.Close()
		m.proc = nil
	}
	opts := m.opts
	opts.Resume, opts.Continue = o.Resume, o.Continue
	m.chat.clear()
	m.pending = nil
	m.busy = false
	m.costUSD = 0
	m.alwaysAllow = map[string]bool{}
	if o.Resume != "" || o.Continue {
		m.chat.info("resuming session…")
	}
	return startCmd(m.ctx, opts)
}

func (m *Model) cwd() string {
	if m.init != nil {
		return m.init.CWD
	}
	return m.opts.CWD
}

// layout sizes the viewport to whatever the bottom panel leaves over.
func (m *Model) layout() {
	if m.width == 0 {
		return
	}
	bottom := lipgloss.Height(m.bottomView()) + 1 // + status bar
	h := m.height - bottom
	if h < 3 {
		h = 3
	}
	if !m.ready {
		m.vp = viewport.New(m.width, h)
		m.vp.MouseWheelEnabled = true
	} else {
		m.vp.Width, m.vp.Height = m.width, h
	}
}

func (m *Model) refresh() {
	if !m.ready {
		return
	}
	content := m.chat.View(m.width)
	if len(m.chat.entries) == 0 {
		content = m.splash()
	}
	m.vp.SetContent(content)
	if m.follow {
		m.vp.GotoBottom()
	}
}

func (m Model) View() string {
	if !m.ready {
		return ""
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.bottomView(), m.statusView())
}

func (m Model) bottomView() string {
	if len(m.pending) > 0 {
		return m.permissionView()
	}
	box := inputBox
	if m.input.Focused() {
		box = inputBoxFocused
	}
	var top string
	if m.busy {
		label := m.status
		if label == "" || label == "requesting" {
			label = "working"
		}
		top = m.spin.View() + " " + mutedStyle.Render(fmt.Sprintf("%s… %ds · esc to interrupt", label, int(time.Since(m.turnFrom).Seconds()))) + "\n"
	}
	return top + box.Width(m.width-2).Render(m.input.View())
}

func (m Model) permissionView() string {
	p := m.pending[0]
	title := warnStyle.Bold(true).Render("Allow " + p.ToolName + "?")
	if len(m.pending) > 1 {
		title += mutedStyle.Render(fmt.Sprintf("  (+%d queued)", len(m.pending)-1))
	}
	detail := prettyInput(p.ToolName, p.Input, m.width-6)
	if p.Description != "" {
		detail = mutedStyle.Render(p.Description) + "\n" + detail
	}
	keys := statusKey.Render("y") + " allow  " + statusKey.Render("a") + " always allow " + p.ToolName + " this session  " + statusKey.Render("n") + " deny"
	return permBox.Width(m.width - 2).Render(title + "\n" + detail + "\n\n" + keys)
}

func prettyInput(tool string, raw json.RawMessage, width int) string {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	switch tool {
	case "Bash":
		if c, ok := m["command"].(string); ok {
			return lipgloss.NewStyle().Width(width).Render("$ " + c)
		}
	case "Edit":
		fp, _ := m["file_path"].(string)
		old, _ := m["old_string"].(string)
		nw, _ := m["new_string"].(string)
		return fp + "\n" + diffLines(old, nw, 12)
	case "Write":
		fp, _ := m["file_path"].(string)
		content, _ := m["content"].(string)
		return fp + "\n" + clip(okStyle, "+ ", content, 12)
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return clip(mutedStyle, "", string(b), 12)
}

func diffLines(old, nw string, max int) string {
	return clip(errStyle, "- ", old, max/2) + "\n" + clip(okStyle, "+ ", nw, max/2)
}

func clip(st lipgloss.Style, prefix, s string, max int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	extra := ""
	if len(lines) > max {
		extra = mutedStyle.Render(fmt.Sprintf("\n  … %d more lines", len(lines)-max))
		lines = lines[:max]
	}
	for i, l := range lines {
		lines[i] = st.Render(prefix + l)
	}
	return strings.Join(lines, "\n") + extra
}

func (m Model) statusView() string {
	sep := mutedStyle.Render(" │ ")
	parts := []string{statusKey.Render("claudewitz")}
	if m.model != "" {
		parts = append(parts, m.model)
	}
	mode := m.permMode
	switch mode {
	case "acceptEdits":
		mode = okStyle.Render("⏵⏵ accept edits")
	case "plan":
		mode = warnStyle.Render("⏸ plan")
	case "bypassPermissions":
		mode = errStyle.Render("⚠ bypass")
	}
	parts = append(parts, mode+mutedStyle.Render(" (⇧⇥)"))
	if ctx := contextTokens(m.lastUsage); ctx > 0 {
		parts = append(parts, fmt.Sprintf("ctx %s", humanTokens(ctx)))
	}
	if w, ok := m.limits["five_hour"]; ok {
		parts = append(parts, usageGauge("5h", w))
	}
	if w, ok := m.limits["seven_day"]; ok {
		parts = append(parts, usageGauge("7d", w))
	}
	if m.costUSD > 0 {
		parts = append(parts, mutedStyle.Render(fmt.Sprintf("≈$%.3f api-equiv", m.costUSD)))
	}
	return statusBar.Width(m.width).Render(strings.Join(parts, sep))
}

func usageGauge(label string, w claude.RateLimitWindow) string {
	pct := int(w.Utilization*100 + 0.5)
	st := okStyle
	switch {
	case pct >= 90:
		st = errStyle
	case pct >= 70:
		st = warnStyle
	}
	reset := ""
	if w.ResetsAt > 0 {
		d := time.Until(time.Unix(w.ResetsAt, 0)).Round(time.Minute)
		if d > 0 {
			reset = mutedStyle.Render(" ↻" + shortDur(d))
		}
	}
	return fmt.Sprintf("%s %s%s", label, st.Render(fmt.Sprintf("%d%%", pct)), reset)
}

func shortDur(d time.Duration) string {
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func contextTokens(u claude.Usage) int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

func humanTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatTokens(u claude.Usage) string {
	return fmt.Sprintf("%s in · %s out", humanTokens(contextTokens(u)), humanTokens(u.OutputTokens))
}

func (m Model) splash() string {
	logo := assistLabel.Render(`
   ✻  c l a u d e w i t z`)
	lines := []string{
		logo,
		"",
		mutedStyle.Render("   A terminal front end for Claude Code — runs on your Claude subscription."),
		"",
	}
	if m.init != nil {
		lines = append(lines,
			mutedStyle.Render("   cwd      ")+m.init.CWD,
			mutedStyle.Render("   model    ")+m.init.Model,
			mutedStyle.Render("   claude   ")+m.init.Version,
			mutedStyle.Render("   tools    ")+fmt.Sprintf("%d available", len(m.init.Tools)),
		)
	}
	lines = append(lines, "", mutedStyle.Render("   /help for commands"))
	return strings.Join(lines, "\n")
}

const helpText = `commands
  /new, /clear        start a fresh session
  /resume [id]        resume a session (most recent if no id)
  /model <name>       switch model (opus, sonnet, haiku, …)
  /mode <mode>        permission mode: default, acceptEdits, plan
  /session            show session id and cwd
  /quit               exit
  other /commands are passed through to Claude Code

keys
  enter               send          ctrl+j / alt+enter  newline
  esc                 interrupt     shift+tab           cycle permission mode
  ctrl+o              expand tool output
  pgup/pgdown, wheel  scroll        ctrl+c              interrupt / quit`
