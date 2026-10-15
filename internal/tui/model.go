// Package tui is the Bubble Tea front end.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/daemon"
	"github.com/exmanubis/claudewitz/internal/safetext"
	"github.com/exmanubis/claudewitz/internal/sessions"
)

var permissionModes = []string{"default", "acceptEdits", "plan"}

// Model is the root Bubble Tea model.
type Model struct {
	*sessionView // the focused session
	*workspace   // the focused session's folder

	views  []*sessionView        // every opened session, the focused one included
	spaces map[string]*workspace // by folder
	base   claude.Options        // flags every new session starts from

	daemon    Daemon                  // nil without the claudewitz daemon
	live      []daemon.SessionSummary // every live session, in start order
	done      map[string]bool         // turn ended while not focused
	since     map[string]time.Time    // when each session entered its state
	lastState map[string]rowState

	ctx   context.Context
	start Starter

	comp      *completer
	picker    *picker
	palette   *palette
	modelPick *modelPicker
	themePick *themePicker
	changes   *changesView // the changes viewer, when open
	cfg       config.Config

	focused   bool      // terminal focus, when the terminal reports it
	lastInput time.Time // last key, click or focus, for pausing the idle bop

	sparks       *sparkField // working indicator at the foot of the sidebar
	sparkTicking bool
	sparkID      int
	sparkLast    time.Time
	bottomH      int // height of the input area at the last layout

	textSel   *textSelection
	mouseDown bool // left button held in the transcript
	dragBar   bool // dragging the scrollbar

	toasts       []toast
	toastID      int
	toastTicking bool
	limitWarned  map[string]int // usage window -> highest % already warned about

	showHelp  bool
	quitAsk   bool   // asking whether to leave a daemon session running
	notice    string // shown in the chat at startup
	spin      spinner.Model
	width     int // what the chat gets
	height    int
	termW     int // the terminal
	termH     int
	panelOn   bool
	panelMode sessionsMode // last applied
	ready     bool
	sidebarOn bool
	drawer    bool // sidebar open over the chat, below sidebarMinWidth
	ticks     int
	animFrame int
	animID    int // generation of the running shimmer tick loop
	animating bool

	revealTicking bool // the streamed-text reveal loop is running
	revealID      int
	revealLast    time.Time

	limits map[string]claude.RateLimitWindow // account-wide usage windows
}

const inputPlaceholder = "Ask Claude anything…  / for commands · @ to mention files"

// New builds the model; the CLI is started from Init.
// The config's theme and appearance are applied here; callers that want a
// one-off theme (the -theme flag) set cfg.Theme without saving it.
// Starter starts (or attaches to) a session: claude.Launch runs the CLI
// directly, the daemon client asks the daemon.
type Starter func(ctx context.Context, opts claude.Options) (claude.Session, error)

// New builds the root model. start launches sessions; nil means claude.Launch.
func New(ctx context.Context, opts claude.Options, cfg config.Config, start Starter) Model {
	if start == nil {
		start = claude.Launch
	}
	setAppearance(cfg.Appearance)
	applyIcons(cfg.Icons)
	applyDiffMode(cfg.Diff)
	if !applyTheme(cfg.Theme) {
		applyTheme(themes[0].name)
	}
	if opts.CWD == "" {
		opts.CWD, _ = os.Getwd()
	}
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(currentTheme().accent)))
	v := newSessionView(opts)
	ws := newWorkspace(opts.CWD)
	return Model{
		sessionView: v,
		workspace:   ws,
		views:       []*sessionView{v},
		spaces:      map[string]*workspace{ws.dir: ws},
		done:        map[string]bool{},
		since:       map[string]time.Time{},
		lastState:   map[string]rowState{},
		base:        opts,
		ctx:         ctx,
		comp:        &completer{},
		spin:        sp,
		sidebarOn:   true,
		limits:      map[string]claude.RateLimitWindow{},
		limitWarned: map[string]int{},
		sparks:      newSparkField(sidebarWidth-3, sparkMinRows),
		focused:     true,
		lastInput:   time.Now(),
		cfg:         cfg,
		start:       start,
	}
}

// WithNotice shows msg in the chat once the TUI starts.
func (m Model) WithNotice(msg string) Model {
	m.notice = msg
	return m
}

type noticeMsg string

func (m Model) Init() tea.Cmd {
	first := m.startCmd(m.sessionView, m.opts)
	if m.daemon != nil && m.opts.Resume == "" && !m.opts.Continue {
		first = launchListCmd(m.daemon) // lists the sessions, then attaches or starts
	}
	cmds := []tea.Cmd{textarea.Blink, m.spin.Tick, first, indexFiles(m.opts.CWD), m.refreshGit(), gitPoll(), loadHistory(m.opts.CWD)}
	if m.notice != "" {
		notice := m.notice
		cmds = append(cmds, func() tea.Msg { return noticeMsg(notice) })
	}
	if m.opts.Resume != "" || m.opts.Continue {
		cmds = append(cmds, loadTranscript(m.sessionView, m.opts.CWD, m.opts.Resume))
	}
	if m.daemon != nil {
		if m.opts.Resume != "" || m.opts.Continue {
			cmds = append(cmds, listCmd(m.daemon))
		}
		cmds = append(cmds, waitOverview(m.daemon.Overview()))
	}
	return tea.Batch(cmds...)
}

type startedMsg struct {
	view *sessionView
	proc claude.Session
	id   string // the daemon's session id, when started there
	err  error
}

func (m *Model) startCmd(v *sessionView, opts claude.Options) tea.Cmd {
	if m.daemon != nil {
		return daemonStart(m.daemon, v, opts)
	}
	ctx, start := m.ctx, m.start
	return func() tea.Msg {
		p, err := start(ctx, opts)
		return startedMsg{view: v, proc: p, err: err}
	}
}

// procEvent tags events with their process so late events from a replaced
// process are ignored.
type procEvent struct {
	proc claude.Session
	ev   claude.Event
}

func waitEvent(p claude.Session) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-p.Events()
		if !ok {
			return nil
		}
		return procEvent{p, ev}
	}
}

type transcriptMsg struct {
	view      *sessionView
	info      sessions.Info
	msgs      []sessions.Message
	subagents map[string][]sessions.Message // by the Agent call's tool_use_id
	err       error
}

// loadTranscript reads a session from disk for replay. An empty id means the
// most recent session in cwd.
func loadTranscript(v *sessionView, cwd, id string) tea.Cmd {
	return func() tea.Msg {
		path, ok := sessions.Find(sessions.ProjectDir(cwd), id)
		if !ok {
			return transcriptMsg{view: v, err: fmt.Errorf("no saved session found")}
		}
		info, _ := sessions.Read(path)
		msgs, err := sessions.Load(path)
		return transcriptMsg{view: v, info: info, msgs: msgs, subagents: sessions.Subagents(path), err: err}
	}
}

// Update handles a message, then keeps the layout and the sidebar's spark
// indicator in step with whatever changed.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(Model)
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg, tea.FocusMsg:
		nm.lastInput = time.Now()
	}
	// The area under the chat grows and shrinks (the activity line, pills,
	// attachments); resize the chat to match, or the screen overflows the
	// terminal and everything scrolls up a line.
	if nm.ready && lipgloss.Height(nm.bottomView()) != nm.bottomH {
		nm.layout()
		if nm.follow {
			nm.vp.GotoBottom()
		}
	}
	return nm, tea.Batch(cmd, nm.syncSparks())
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
		m.ready = true
		m.resize()

	case noticeMsg:
		m.chat.info("%s", string(msg))
		m.refresh()

	case overviewListMsg:
		if msg.err == nil {
			m.live = msg.list
			for _, s := range m.live {
				m.noteState(s)
			}
		}
		if m.ready && m.sessionsMode() != m.panelMode {
			m.resize()
		}

	case overviewMsg:
		cmds = append(cmds, m.applyOverview(daemon.Frame(msg)), waitOverview(m.daemon.Overview()))
		if m.ready && m.sessionsMode() != m.panelMode {
			m.resize()
		}

	case launchListMsg:
		return m, m.launch(msg)
	case startedMsg:
		v := msg.view
		if msg.err != nil {
			m.inView(v, func() {
				m.chat.error("could not start claude: %v", msg.err)
				m.refresh()
			})
			if msg.id != "" && v.id == msg.id {
				// A failed attach: forget the id so focusing it again retries.
				v.id = ""
				if v != m.sessionView {
					m.removeView(v)
				}
			}
			return m, nil
		}
		v.proc = msg.proc
		if msg.id != "" {
			v.id = msg.id
		}
		cmds = append(cmds, waitEvent(v.proc))

	case procEvent:
		v := m.viewFor(msg.proc)
		if v == nil {
			return m, nil // stale process
		}
		if v != m.sessionView {
			var cmd tea.Cmd
			m.inView(v, func() { cmd = m.handleEvent(msg.ev) })
			cmds = append(cmds, cmd)
			if v.proc != nil && !msg.ev.Exited {
				cmds = append(cmds, waitEvent(v.proc))
			}
			break
		}
		cmds = append(cmds, m.handleEvent(msg.ev))
		if touchesTree(msg.ev) {
			cmds = append(cmds, m.refreshGit())
			if m.changes != nil && m.changes.session {
				cmds = append(cmds, m.refreshChanges())
			}
		}
		if m.proc != nil && !msg.ev.Exited {
			cmds = append(cmds, waitEvent(m.proc))
		}
		m.refresh()

	case transcriptMsg:
		m.inView(msg.view, func() {
			if msg.err != nil {
				m.chat.error("could not load transcript: %v", msg.err)
			} else {
				m.replay(msg)
			}
			m.refresh()
		})

	case filesIndexedMsg:
		if ws, ok := m.spaces[msg.dir]; ok {
			ws.files = msg.files
		}

	case gitStatusMsg:
		cmd := m.applyGit(msg)
		return m, tea.Batch(cmd, m.refreshChanges())

	case changeDiffMsg:
		m.applyChangeDiff(msg)

	case changeEditedMsg:
		if msg.err != nil {
			return m, tea.Batch(m.notify(toastError, "Editor exited with an error", msg.err.Error()), m.refreshGit())
		}
		return m, m.refreshGit()

	case historyLoadedMsg:
		if ws, ok := m.spaces[msg.project]; ok {
			ws.hist.items = dedupeKeepLast(append(msg.items, ws.hist.items...))
		}

	case editorDoneMsg:
		return m, m.editorDone(msg)

	case clipboardImageMsg:
		switch {
		case msg.err == nil:
			return m, m.attach(msg.att)
		case errors.Is(msg.err, errNoClipboardImage):
			// No image: paste text as the textarea normally would.
			var c tea.Cmd
			m.input, c = m.input.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
			m.comp.update(&m)
			m.layout()
			return m, c
		default:
			return m, m.notify(toastError, "Couldn't paste image", msg.err.Error())
		}

	case tea.FocusMsg:
		// Catch up on changes made while away (polling slows when idle).
		wasIdle := m.idle()
		m.focused = true
		if wasIdle {
			return m, m.refreshGit()
		}

	case tea.BlurMsg:
		m.focused = false

	case gitPollMsg:
		return m, m.onGitPoll()

	case sessionsLoadedMsg:
		if m.picker != nil && m.picker.all == msg.all {
			m.picker.setSessions(msg.list)
		}

	case spinner.TickMsg:
		var c tea.Cmd
		m.spin, c = m.spin.Update(msg)
		cmds = append(cmds, c)
		m.ticks++
		if m.ticks%6 == 0 && m.track.runningAgents() > 0 {
			m.chat.markAgentsDirty()
			m.refresh()
		}

	case sparkTickMsg:
		return m, m.onSparkTick(msg)

	case revealTickMsg:
		return m, m.onRevealTick(msg)

	case toastTickMsg:
		if msg.id != m.toastID {
			return m, nil
		}
		if m.pruneToasts(time.Now()) {
			return m, toastTick(m.toastID)
		}
		m.toastTicking = false
		return m, nil

	case animTickMsg:
		if msg.id != m.animID {
			return m, nil
		}
		if !m.busy {
			m.animating = false
			return m, nil
		}
		m.animFrame++
		return m, animTick(m.animID)

	case tea.MouseMsg:
		if id, handled := m.sessionsMouse(&msg); handled {
			if id != "" {
				return m, m.focusSession(id)
			}
			return m, nil
		}
		overlaid := m.picker != nil || m.palette != nil || m.modelPick != nil || m.themePick != nil || m.showHelp
		var path string // a changed file clicked in the sidebar
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && m.changes == nil && !overlaid {
			path, _ = m.sidebarFileAt(msg.X, msg.Y)
		}
		if m.changes != nil {
			cmds = append(cmds, m.handleChangesMouse(msg))
		} else if (m.diffView != nil || m.editPanel != nil && len(m.pending) > 0) && !overlaid {
			cmds = append(cmds, m.handlePanelMouse(msg))
		} else if path != "" {
			cmds = append(cmds, m.openChanges(path))
		} else if m.showDrawer() && msg.Action == tea.MouseActionPress && msg.Button != tea.MouseButtonWheelUp && msg.Button != tea.MouseButtonWheelDown {
			// A click closes the drawer rather than landing on the chat under it.
			m.drawer = false
		} else if m.picker == nil && m.palette == nil && m.modelPick == nil && m.themePick == nil && !m.showHelp {
			cmds = append(cmds, m.handleMouse(msg))
		}

	case tea.KeyMsg:
		if !m.mouseDown {
			m.textSel = nil // any key clears a mouse selection
		}
		if m.quitAsk {
			return m, m.handleQuitKey(msg)
		}
		if m.showHelp {
			switch msg.String() {
			case "esc", "ctrl+g", "q", "enter", "ctrl+c":
				m.showHelp = false
			}
			return m, nil
		}
		if m.changes != nil {
			return m, m.handleChangesKey(msg)
		}
		if m.diffView != nil {
			return m, m.handleDiffViewKey(msg)
		}
		if m.themePick != nil {
			return m, m.handleThemePickerKey(msg)
		}
		if m.modelPick != nil {
			return m, m.handleModelPickerKey(msg)
		}
		if m.palette != nil {
			return m, m.handlePaletteKey(msg)
		}
		if m.picker != nil {
			return m, m.handlePickerKey(msg)
		}
		if cmd, ok := m.handleSessionKey(msg); ok {
			return m, cmd
		}
		if len(m.pending) > 0 {
			return m, m.handlePermissionKey(msg)
		}
		if msg.Paste {
			// Files dragged into the terminal arrive as pasted paths.
			if paths := droppedPaths(string(msg.Runes), m.cwd()); paths != nil {
				if m.chatFocus {
					m.blurChat()
				}
				return m, m.attachPaths(paths)
			}
		}
		if m.chatFocus {
			if cmd, handled := m.handleChatKey(msg); handled {
				return m, cmd
			}
		}
		if m.comp.active && !(msg.String() == "enter" && m.comp.complete()) {
			switch msg.String() {
			case "up", "ctrl+p":
				m.comp.move(-1)
				return m, nil
			case "down", "ctrl+n":
				m.comp.move(1)
				return m, nil
			case "tab", "enter":
				m.comp.accept(&m)
				m.layout()
				return m, nil
			case "esc":
				m.comp.dismiss()
				m.comp.update(&m)
				m.layout()
				return m, nil
			}
		}
		switch msg.String() {
		case "ctrl+c":
			if m.busy && m.proc != nil {
				m.interrupt()
				return m, nil
			}
			if m.input.Value() != "" {
				m.input.Reset()
				m.comp.update(&m)
				m.layout()
				return m, nil
			}
			return m, m.quit()
		case "ctrl+d":
			return m, m.quit()
		case "esc":
			if m.busy && m.proc != nil {
				m.interrupt()
			}
			return m, nil
		case "shift+tab":
			return m, m.cyclePermissionMode()
		case "ctrl+o":
			m.chat.expanded = !m.chat.expanded
			m.chat.markAllDirty()
			m.refresh()
			if m.chat.expanded {
				return m, m.notify(toastInfo, "Tool output expanded", "ctrl+o to collapse")
			}
			return m, m.notify(toastInfo, "Tool output collapsed", "")
		case "ctrl+b":
			return m, m.toggleSidebar()
		case "ctrl+r":
			return m, m.openPicker()
		case "ctrl+p":
			m.openPalette()
			return m, nil
		case "ctrl+x":
			return m, m.openEditor()
		case "ctrl+v":
			return m, readClipboardImage()
		case "ctrl+f":
			m.input.SetValue("/image ")
			m.input.CursorEnd()
			m.comp.update(&m)
			m.layout()
			return m, nil
		case "backspace":
			if m.input.Value() == "" && len(m.attachments) > 0 {
				last := m.attachments[len(m.attachments)-1]
				m.attachments = m.attachments[:len(m.attachments)-1]
				m.layout()
				return m, m.notify(toastInfo, "Removed "+last.name, "")
			}
		case "tab":
			m.focusChat()
			return m, nil
		case "right":
			if m.input.Value() == "" && m.suggestion != "" {
				m.input.SetValue(m.suggestion)
				m.input.CursorEnd()
				m.setSuggestion("")
				m.comp.update(&m)
				m.layout()
				return m, nil
			}
		case "up", "down":
			recall := m.hist.prev
			if msg.String() == "down" {
				recall = m.hist.next
			}
			if text, ok := recall(m.input.Value()); ok {
				m.input.SetValue(text)
				m.input.CursorEnd()
				m.layout()
				return m, nil
			}
		case "ctrl+t":
			m.openModelPicker()
			return m, nil
		case "ctrl+s":
			return m, m.openChanges("")
		case "ctrl+g":
			m.showHelp = true
			return m, nil
		case "ctrl+l":
			m.follow = true
			m.refresh()
			return m, tea.ClearScreen
		case "pgup", "pgdown":
			var c tea.Cmd
			m.vp, c = m.vp.Update(msg)
			m.follow = m.vp.AtBottom()
			return m, c
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			if text == "" && len(m.attachments) == 0 {
				if m.suggestion == "" {
					return m, nil
				}
				text = m.suggestion
			}
			m.input.Reset()
			m.comp.update(&m)
			m.layout()
			return m, m.submit(text)
		}
		var c tea.Cmd
		before := m.input.Value()
		m.input, c = m.input.Update(msg)
		if m.input.Value() != before {
			m.comp.update(&m)
			m.layout()
		}
		cmds = append(cmds, c)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) interrupt() {
	_ = m.proc.Interrupt()
	m.stopped = true
	m.chat.info("interrupted")
	m.refresh()
}

// detacher is a session that can keep running without this TUI: one held
// by the daemon.
type detacher interface{ Detach() error }

// quit ends a local session. A daemon session may outlive the TUI, so
// quitting asks whether to detach (the default) or stop it.
func (m *Model) quit() tea.Cmd {
	if _, ok := m.proc.(detacher); ok {
		m.quitAsk = true
		m.input.Blur()
		m.layout()
		return nil
	}
	if m.proc != nil {
		m.proc.Stop()
	}
	return tea.Quit
}

func (m *Model) handleQuitKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "enter", "d", "ctrl+d", "ctrl+c":
		if d, ok := m.proc.(detacher); ok {
			_ = d.Detach()
		}
		return tea.Quit
	case "s":
		if m.proc != nil {
			m.proc.Stop()
		}
		return tea.Quit
	case "esc", "n", "q":
		m.quitAsk = false
		if !m.chatFocus {
			m.input.Focus()
		}
		m.layout()
	}
	return nil
}

func (m Model) quitView() string {
	title := warnStyle.Bold(true).Render("Leave this session running?")
	detail := mutedStyle.Render("Detached, it keeps working in the claudewitz daemon; resume it with ctrl+r or claudewitz --continue.")
	keys := statusKey.Render("d") + mutedStyle.Render(" detach  ") +
		statusKey.Render("s") + mutedStyle.Render(" stop session  ") +
		statusKey.Render("esc") + mutedStyle.Render(" cancel")
	return permBox.Width(m.mainWidth() - 2).Render(title + "\n\n" + lipgloss.NewStyle().Width(m.mainWidth()-6).Render(detail) + "\n\n" + keys)
}

func (m *Model) handleEvent(ev claude.Event) (cmd tea.Cmd) {
	var alert tea.Cmd // desktop notification, sent alongside whatever else
	defer func() {
		if alert != nil {
			cmd = tea.Batch(cmd, alert)
		}
	}()

	switch {
	case ev.Initialize != nil:
		m.initResp = ev.Initialize
		if ev.Initialize.PermissionMode != "" {
			m.permMode = ev.Initialize.PermissionMode
		}
		if m.model == "" {
			for _, mi := range ev.Initialize.Models {
				if mi.Value == "default" {
					m.model = mi.ResolvedModel
				}
			}
		}

	case ev.Init != nil:
		m.init = ev.Init
		m.sessionID = ev.Init.SessionID
		m.model = ev.Init.Model
		m.permMode = ev.Init.PermissionMode
		if ev.Init.CWD != "" {
			m.chat.cwd = ev.Init.CWD
		}

	case ev.Suggestion != "":
		m.setSuggestion(ev.Suggestion)

	case ev.ContextWindow > 0:
		m.ctxWindow = ev.ContextWindow

	case ev.Status != "" || ev.PermissionMode != "":
		if ev.PermissionMode != "" {
			m.permMode = ev.PermissionMode
		}
		if ev.Status == "" {
			return nil
		}
		m.status = ev.Status
		if ev.Status == "requesting" {
			return m.markBusy()
		}

	case ev.Stream != nil:
		m.handleStream(ev.Stream)
		return tea.Batch(m.markBusy(), m.startReveal())

	case ev.Message != nil:
		if ev.Message.ParentToolUseID != nil {
			m.track.subagentMessage(*ev.Message.ParentToolUseID, ev.Message.Message)
			m.chat.markAgentsDirty()
			return nil
		}
		if ev.Message.IsReplay {
			m.takeQueued(ev.Message.Message.Text())
			return nil
		}
		m.applyBlocks(ev.Message.Message.Role, ev.Message.Message.Content, true)
		if u := ev.Message.Message.Usage; u != nil && ev.Message.Message.Role == "assistant" {
			m.lastUsage = *u
		}

	case ev.Task != nil:
		before := m.agentStatus(ev.Task.ToolUseID, ev.Task.TaskID)
		m.track.task(ev.Task)
		m.chat.markAgentsDirty()
		if a := m.agentFor(ev.Task.ToolUseID, ev.Task.TaskID); a != nil && a.background && before == "running" && !a.running() {
			if a.status == "completed" {
				return m.notify(toastSuccess, a.kind+" agent finished", a.desc)
			}
			return m.notify(toastError, a.kind+" agent "+a.status, a.desc)
		}

	case ev.Result != nil:
		r := ev.Result
		m.busy = false
		m.activity = ""
		m.status = ""
		m.chat.streaming = map[int]*entry{}
		m.costUSD += r.TotalCostUSD
		if w := r.ContextWindow(m.model); w > 0 {
			m.ctxWindow = w
		}
		if r.SessionID != "" {
			m.sessionID = r.SessionID
		}
		if m.sessionTitle == "" {
			m.sessionTitle = m.firstPrompt()
		}
		if m.stopped {
			m.stopped = false
			return nil
		}
		alert = m.desktopNotify("Claude finished", firstNonEmpty(m.lastAssistantLine(), "Ready for your next message"))
		if r.IsError && r.Subtype != "success" {
			m.chat.error("turn ended: %s %s", r.Subtype, r.Result)
		}
		dur := (time.Duration(r.DurationMS) * time.Millisecond).Round(100 * time.Millisecond)
		m.lastTurn, m.lastTurnAt = dur, time.Now()
		m.chat.info("%s · %d turns · %s", dur, r.NumTurns, formatTokens(r.Usage))
		if r.IsError && r.Subtype != "success" {
			return m.notify(toastError, "Turn ended with an error", r.Subtype)
		}
		if dur >= 30*time.Second {
			return m.notify(toastSuccess, "Claude finished", fmt.Sprintf("took %s · %d turns", shortSecs(dur), r.NumTurns))
		}

	case ev.RateLimit != nil:
		for k, w := range ev.RateLimit.Info.Windows {
			m.limits[k] = w
		}
		if ev.RateLimit.Info.Status != "allowed" && ev.RateLimit.Info.Status != "" {
			reset := time.Unix(ev.RateLimit.Info.ResetsAt, 0).Format("15:04")
			m.chat.error("rate limit: %s (resets %s)", ev.RateLimit.Info.Status, reset)
			return m.notify(toastError, "Rate limited", fmt.Sprintf("%s · resets at %s", ev.RateLimit.Info.Status, reset))
		}
		if cmd := m.usageWarnings(); cmd != nil {
			return cmd
		}

	case ev.Permission != nil:
		p := ev.Permission
		if m.alwaysAllow[p.ToolName] && m.proc != nil && !isInteractiveTool(p.ToolName) {
			_ = m.proc.Respond(p, claude.PermissionDecision{Behavior: "allow"})
			return nil
		}
		m.pending = append(m.pending, p)
		m.diffView = nil // the request needs the panel
		if len(m.pending) == 1 {
			m.startInteraction()
		}
		// A prompt needs an answer; leave the transcript (keeping the selection).
		m.chatFocus, m.chat.focused = false, false
		m.input.Blur()
		m.layout()
		switch p.ToolName {
		case toolAskUser:
			alert = m.desktopNotify("Claude has a question", summarizeInput(p.ToolName, p.Input, 120))
		case toolExitPlan:
			alert = m.desktopNotify("Plan ready for review", "Approve it or keep planning")
		default:
			alert = m.desktopNotify("Claude needs your approval", strings.TrimSpace(p.ToolName+" "+summarizeInput(p.ToolName, p.Input, 100)))
		}

	case ev.PermissionResolved != "":
		// Another client answered; close the request here too.
		for i, p := range m.pending {
			if p.RequestID == ev.PermissionResolved {
				m.pending = append(m.pending[:i:i], m.pending[i+1:]...)
				if i == 0 {
					m.afterPermission()
				}
				break
			}
		}

	case ev.Exited:
		m.busy = false
		m.proc = nil
		if ev.Err != nil {
			m.chat.error("claude exited: %v", ev.Err)
			return m.notify(toastError, "Claude Code exited", "/new to start a fresh session")
		} else {
			m.chat.info("claude exited — /new to start again")
		}

	case ev.Err != nil:
		m.chat.error("%v", ev.Err)
	}
	return nil
}

func (m *Model) firstPrompt() string {
	for _, e := range m.chat.entries {
		if e.kind == kindUser {
			return firstLine(e.text)
		}
	}
	return ""
}

func (m *Model) handleStream(s *claude.StreamEvent) {
	if s.ParentToolUseID != nil {
		return // sub-agent chatter is tracked from complete messages
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
			m.activity = "Writing"
			m.chat.streaming[e.Index] = m.chat.add(&entry{kind: kindAssistant, text: e.ContentBlock.Text, paced: true})
		case "thinking":
			m.activity = "Thinking"
			// Joins the transcript with its first text: models that keep
			// their thinking hidden send none, and the activity line
			// already says Claude is thinking.
			th := &entry{kind: kindThinking, text: e.ContentBlock.Thinking, thinkStart: time.Now(), paced: true}
			if th.text != "" {
				m.chat.add(th)
			}
			m.chat.streaming[e.Index] = th
		case "tool_use":
			m.activity = "Running " + e.ContentBlock.Name
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
			if live.text == "" && e.Delta.Thinking != "" {
				m.chat.add(live)
			}
			live.setText(live.text + e.Delta.Thinking)
		}
	case "content_block_stop":
		live := m.chat.streaming[e.Index]
		if live == nil || live.kind != kindThinking || live.thinkStart.IsZero() {
			return
		}
		live.thought, live.dirty = time.Since(live.thinkStart), true
		// Hidden thinking never joined the transcript; it shows now as
		// just how long it took.
		if live.text == "" && live.thought >= time.Second {
			m.chat.add(live)
		}
	}
}

// applyBlocks adds a main-thread message to the chat and trackers. Live
// messages get their text from stream deltas, so only replayed history
// renders text blocks here.
func (m *Model) applyBlocks(role string, blocks []claude.ContentBlock, live bool) {
	// Images in replayed user messages: the transcript keeps no file names.
	var images []string
	var pics []*picture
	hasText := false
	for _, b := range blocks {
		switch b.Type {
		case "image":
			images = append(images, fmt.Sprintf("image %d", len(images)+1))
		case "text":
			hasText = true
		}
	}
	if !live && role == "user" {
		pics = imagePictures(blocks)
	}
	if !live && role == "user" && len(images) > 0 && !hasText {
		m.chat.add(&entry{kind: kindUser, images: images, pics: pics})
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if live {
				continue
			}
			if role == "user" && strings.HasPrefix(b.Text, "[Request interrupted") {
				m.chat.info("interrupted")
			} else if role == "user" {
				m.chat.add(&entry{kind: kindUser, text: b.Text, images: images, pics: pics})
				images, pics = nil, nil
			} else if strings.TrimSpace(b.Text) != "" {
				m.chat.add(&entry{kind: kindAssistant, text: b.Text})
			}
		case "tool_use":
			if _, seen := m.chat.byToolID[b.ID]; !seen {
				e := m.chat.add(&entry{kind: kindTool, toolID: b.ID, toolName: b.Name, toolInput: b.Input})
				if isDiffTool(b.Name) {
					// Live calls are read now, while the file is (usually)
					// still as it was.
					e.diff, _ = editDiff(b.Name, b.Input, live)
				}
				m.track.toolUse(b)
			}
		case "tool_result":
			out := toolResultText(b.Content)
			m.track.toolResult(b.ToolUseID, out, b.IsError)
			if t, ok := m.chat.byToolID[b.ToolUseID]; ok {
				t.toolOutput = out
				t.pics = toolResultPictures(b.Content)
				t.toolStatus = toolOK
				if b.IsError {
					t.toolStatus = toolFailed
				}
				t.dirty = true
			}
		}
	}
}

func (m *Model) replay(t transcriptMsg) {
	m.sessionTitle = t.info.Title
	if m.sessionTitle == "" {
		m.sessionTitle = t.info.FirstPrompt
	}
	if m.sessionID == "" {
		m.sessionID = t.info.ID
	}
	for _, msg := range t.msgs {
		m.applyBlocks(msg.Role, msg.Content, false)
	}
	for id, msgs := range t.subagents {
		for _, msg := range msgs {
			m.track.subagentMessage(id, claude.Message{Role: msg.Role, Content: msg.Content})
		}
	}
	// Agents in history are finished even if no completion event was
	// saved, and replay timings are meaningless.
	for _, a := range m.track.agents {
		if a.running() {
			a.status = "completed"
		}
		a.duration = 0
	}
	m.chat.info("resumed %s · %d messages", t.info.ID[:min(8, len(t.info.ID))], len(t.msgs))
	m.follow = true
}

func (m *Model) handlePermissionKey(k tea.KeyMsg) tea.Cmd {
	switch {
	case m.question != nil:
		return m.handleQuestionKey(k)
	case m.plan != nil:
		return m.handlePlanKey(k)
	case m.editPanel != nil:
		if cmd, ok := m.handlePanelKey(k); ok {
			return cmd
		}
	}
	p := m.pending[0]
	var d *claude.PermissionDecision
	switch k.String() {
	case "y", "enter":
		d = &claude.PermissionDecision{Behavior: "allow"}
	case "a":
		m.alwaysAllow[p.ToolName] = true
		d = &claude.PermissionDecision{Behavior: "allow", AlwaysForSession: true}
	case "n", "esc":
		d = &claude.PermissionDecision{Behavior: "deny", Message: "The user denied this tool call."}
	case "t":
		if !isDiffTool(p.ToolName) {
			return nil
		}
		cmd := m.toggleDiffLayout(m.mainWidth() - 6)
		m.layout()
		return cmd
	case "ctrl+c":
		d = &claude.PermissionDecision{Behavior: "deny", Message: "The user denied this tool call and interrupted."}
		if m.proc != nil {
			_ = m.proc.RespondAndInterrupt(p, *d)
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
			t.toolOutput = "denied by user"
			t.dirty = true
		}
	}
	m.pending = m.pending[1:]
	m.afterPermission()
	return nil
}

func (m *Model) afterPermission() {
	m.startInteraction()
	if len(m.pending) == 0 && !m.chatFocus {
		m.input.Focus()
	}
	m.layout()
	m.refresh()
}

func (m *Model) openPicker() tea.Cmd {
	m.picker = newPicker(m.sessionID, m.cwd())
	m.input.Blur()
	return loadSessions(m.opts.CWD, false)
}

func (m *Model) closePicker() {
	m.picker = nil
	if len(m.pending) == 0 && !m.chatFocus {
		m.input.Focus()
	}
}

func (m *Model) handlePickerKey(k tea.KeyMsg) tea.Cmd {
	p := m.picker
	rows := pickerRows(m.height - 1)
	switch k.String() {
	case "esc", "ctrl+c":
		m.closePicker()
		return nil
	case "up", "ctrl+p":
		p.move(-1, rows)
		return nil
	case "down", "ctrl+n":
		p.move(1, rows)
		return nil
	case "pgup":
		p.move(-rows, rows)
		return nil
	case "pgdown":
		p.move(rows, rows)
		return nil
	case "tab":
		p.all = !p.all
		p.loading = true
		return loadSessions(m.opts.CWD, p.all)
	case "enter":
		s, ok := p.selected()
		if !ok {
			return nil
		}
		current := p.isCurrent(s)
		m.closePicker()
		if current {
			return nil
		}
		return m.resume(s)
	}
	var c tea.Cmd
	before := p.filter.Value()
	p.filter, c = p.filter.Update(k)
	if p.filter.Value() != before {
		p.applyFilter()
	}
	return c
}

// resume switches to a saved session, moving to its directory if needed.
func (m *Model) resume(s sessions.Info) tea.Cmd {
	if s.CWD != "" && s.CWD != m.opts.CWD {
		if _, err := os.Stat(s.CWD); err == nil {
			m.opts.CWD = s.CWD
		}
	}
	cmd := m.restart(claude.Options{Resume: s.ID})
	msgs, err := sessions.Load(s.Path)
	if err != nil {
		m.chat.error("could not load transcript: %v", err)
	} else {
		m.replay(transcriptMsg{info: s, msgs: msgs, subagents: sessions.Subagents(s.Path)})
	}
	m.refresh()
	cmds := []tea.Cmd{cmd}
	if dir := m.cwd(); dir != m.workspace.dir {
		old := m.workspace.dir
		ws, created := m.spaceFor(dir)
		m.workspace = ws
		m.dropSpaceIfUnused(old)
		if created {
			cmds = append(cmds, indexFiles(dir), loadHistory(dir))
		}
	}
	m.gitLoaded = false
	return tea.Batch(append(cmds, m.refreshGit(), m.notify(toastSuccess, "Session resumed", s.Label()))...)
}

func (m *Model) cyclePermissionMode() tea.Cmd {
	next := permissionModes[0]
	for i, pm := range permissionModes {
		if pm == m.permMode {
			next = permissionModes[(i+1)%len(permissionModes)]
		}
	}
	return m.setPermissionMode(next)
}

var modeToast = map[string][2]string{
	"default":           {"Default mode", "Claude asks before edits and commands"},
	"acceptEdits":       {"Accept edits", "File edits are approved automatically"},
	"plan":              {"Plan mode", "Claude plans without changing anything"},
	"bypassPermissions": {"Bypassing permissions", "Nothing will ask for approval"},
}

func (m *Model) setPermissionMode(mode string) tea.Cmd {
	m.permMode = mode
	if m.proc != nil {
		_ = m.proc.SetPermissionMode(mode)
	}
	t, ok := modeToast[mode]
	if !ok {
		t = [2]string{"Permission mode: " + mode, ""}
	}
	kind := toastInfo
	if mode == "bypassPermissions" {
		kind = toastWarn
	}
	return m.notify(kind, t[0], t[1])
}

// usageWarnings toasts once when a subscription window crosses 80% and 90%.
func (m *Model) usageWarnings() tea.Cmd {
	names := map[string]string{"five_hour": "5-hour", "seven_day": "Weekly"}
	for key, label := range names {
		w, ok := m.limits[key]
		if !ok {
			continue
		}
		pct := int(w.Utilization * 100)
		for _, th := range []int{90, 80} {
			if pct >= th && m.limitWarned[key] < th {
				m.limitWarned[key] = th
				body := fmt.Sprintf("%d%% used", pct)
				if w.ResetsAt > 0 {
					body += " · resets in " + shortDur(time.Until(time.Unix(w.ResetsAt, 0)).Round(time.Minute))
				}
				kind := toastWarn
				if th == 90 {
					kind = toastError
				}
				return m.notify(kind, label+" usage above "+fmt.Sprint(th)+"%", body)
			}
		}
	}
	return nil
}

func (m *Model) agentFor(toolUseID, taskID string) *agentState {
	if a := m.track.agentIdx[toolUseID]; a != nil {
		return a
	}
	return m.track.taskIdx[taskID]
}

func (m *Model) agentStatus(toolUseID, taskID string) string {
	if a := m.agentFor(toolUseID, taskID); a != nil {
		if a.running() {
			return "running"
		}
		return a.status
	}
	return ""
}

// setSuggestion shows s as the input's placeholder, or restores the default
// when s is empty.
func (m *Model) setSuggestion(s string) {
	m.suggestion = strings.TrimSpace(s)
	m.input.Placeholder = inputPlaceholder
	if m.suggestion != "" {
		m.input.Placeholder = m.suggestion + "  → edit · enter send"
	}
}

func (m *Model) submit(text string) tea.Cmd {
	m.setSuggestion("")
	m.hist.add(text)
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
	if err := m.proc.SendWithImages(text, m.images()); err != nil {
		m.chat.error("send failed: %v", err)
		m.refresh()
		return nil
	}
	var names []string
	var pics []*picture
	for _, a := range m.attachments {
		names = append(names, a.name)
		pics = append(pics, newPicture(a.name, a.data))
	}
	m.attachments = nil
	if m.busy {
		// The CLI holds it until the turn ends; it joins the transcript when
		// the CLI acknowledges it (see takeQueued).
		m.queued = append(m.queued, queuedMsg{text: text, images: names, pics: pics})
		m.layout()
		return nil
	}
	m.chat.add(&entry{kind: kindUser, text: text, images: names, pics: pics})
	if !m.busy {
		m.busy = true
		m.turnFrom = time.Now()
	}
	m.follow = true
	m.refresh()
	return m.startAnim()
}

// queuedMsg is a message the CLI is holding until the current turn ends.
type queuedMsg struct {
	text   string
	images []string
	pics   []*picture
}

// takeQueued moves queued messages into the transcript once the CLI
// acknowledges them. Queued messages arrive together, joined by newlines.
func (m *Model) takeQueued(replay string) {
	kept := m.queued[:0]
	for _, q := range m.queued {
		// The replay is cleaned for display (see claude.ContentBlock), so
		// match against the cleaned text. Image-only messages have no text.
		if strings.Contains(replay, safetext.Clean(q.text)) {
			m.chat.add(&entry{kind: kindUser, text: q.text, images: q.images, pics: q.pics})
		} else {
			kept = append(kept, q)
		}
	}
	m.queued = kept
	m.follow = true
	m.layout()
}

func (m *Model) lastAssistantLine() string {
	for i := len(m.chat.entries) - 1; i >= 0; i-- {
		if e := m.chat.entries[i]; e.kind == kindAssistant {
			return truncate(firstLine(e.text), 120)
		}
	}
	return ""
}

// markBusy notes a turn the CLI started on its own, e.g. after a
// background agent finished.
func (m *Model) markBusy() tea.Cmd {
	if !m.busy {
		m.busy = true
		m.turnFrom = time.Now()
	}
	return m.startAnim()
}

// startAnim begins the shimmer tick loop if it isn't already running.
func (m *Model) startAnim() tea.Cmd {
	if m.animating {
		return nil
	}
	m.animating = true
	m.animID++
	return animTick(m.animID)
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
		return tea.Batch(m.restart(claude.Options{}), m.notify(toastInfo, "New session", prettyPath(m.opts.CWD))), true
	case "/resume", "/sessions":
		if arg == "" {
			return m.openPicker(), true
		}
		return tea.Batch(m.restart(claude.Options{Resume: arg}), loadTranscript(m.sessionView, m.opts.CWD, arg)), true
	case "/model":
		if arg == "" {
			m.openModelPicker()
			return nil, true
		}
		mi := claude.ModelInfo{Value: arg}
		for _, x := range m.availableModels() {
			if x.Value == arg {
				mi = x
			}
		}
		return m.applyModel(mi, m.opts.Effort), true
	case "/effort":
		if arg == "" {
			m.chat.info("effort: %s (levels: %s)", firstNonEmpty(m.opts.Effort, "default"), strings.Join(defaultEffortLevels, ", "))
			return nil, true
		}
		valid := false
		for _, l := range defaultEffortLevels {
			valid = valid || l == arg
		}
		if !valid {
			return m.notify(toastError, "Unknown effort "+arg, strings.Join(defaultEffortLevels, ", ")), true
		}
		return m.applyModel(claude.ModelInfo{Value: m.modelValue()}, arg), true
	case "/mode":
		if arg == "" {
			m.chat.info("permission mode: %s (modes: %s)", m.permMode, strings.Join(permissionModes, ", "))
			return nil, true
		}
		return m.setPermissionMode(arg), true
	case "/theme":
		if arg == "" {
			m.openThemePicker()
			return nil, true
		}
		if !applyTheme(arg) {
			return m.notify(toastError, "Unknown theme "+arg, strings.Join(themeNames(), ", ")), true
		}
		m.restyle()
		return m.saveTheme(), true
	case "/edit":
		return m.openEditor(), true
	case "/image":
		if arg == "" {
			return m.notify(toastInfo, "Attach an image", "/image path/to/file.png, ctrl+v to paste, or drag a file in"), true
		}
		return m.attachPaths([]string{strings.Trim(arg, `"'`)}), true
	case "/notifications":
		return m.setNotifications(arg), true
	case "/icons":
		return m.setIcons(arg), true
	case "/diff":
		return m.setDiffMode(arg), true
	case "/sidebar":
		return m.toggleSidebar(), true
	case "/changes":
		return m.openChanges(arg), true
	case "/session":
		m.chat.info("session %s\ncwd %s", m.sessionID, m.cwd())
	default:
		return nil, false
	}
	return nil, true
}

func (m *Model) restart(o claude.Options) tea.Cmd {
	if m.proc != nil {
		m.proc.Stop()
		m.proc = nil
	}
	opts := m.opts
	opts.Resume, opts.Continue = o.Resume, o.Continue
	m.track = newTracker()
	m.chat.reset(m.track)
	m.chat.cwd = opts.CWD
	m.pending = nil
	m.question, m.plan, m.editPanel, m.diffView = nil, nil, nil, nil
	m.queued = nil
	m.setSuggestion("")
	m.attachments = nil
	m.chatFocus = false
	m.busy = false
	m.costUSD = 0
	m.sessionID = o.Resume
	m.sessionTitle = ""
	m.lastUsage = claude.Usage{}
	m.alwaysAllow = map[string]bool{}
	m.init = nil
	m.refresh()
	return m.startCmd(m.sessionView, opts)
}

func (m *Model) cwd() string {
	if m.init != nil {
		return m.init.CWD
	}
	return m.opts.CWD
}

// contextWindow is the size the CLI reported with the last turn's result;
// before the first one it's guessed from the model name.
func (m *Model) contextWindow() int {
	if m.ctxWindow > 0 {
		return m.ctxWindow
	}
	if strings.HasSuffix(m.model, "[1m]") {
		return 1000000
	}
	return 200000
}

// sidebarMinWidth is the narrowest window that docks the sidebar.
const sidebarMinWidth = 100

func (m Model) showSidebar() bool { return m.sidebarOn && m.width >= sidebarMinWidth }

// showDrawer is true when the sidebar is open over the chat instead.
func (m Model) showDrawer() bool { return m.drawer && m.width < sidebarMinWidth }

// sidebarVisible is true when the sidebar is on screen either way.
func (m Model) sidebarVisible() bool { return m.showSidebar() || m.showDrawer() }

// toggleSidebar docks or hides the sidebar, or below sidebarMinWidth opens
// and closes it as a drawer over the chat.
func (m *Model) toggleSidebar() tea.Cmd {
	if m.width < sidebarMinWidth {
		m.drawer = !m.drawer
		return m.syncSparks()
	}
	m.sidebarOn = !m.sidebarOn
	m.drawer = false
	m.layout()
	m.refresh()
	return m.syncSparks()
}

func (m Model) mainWidth() int {
	if m.showSidebar() {
		return m.width - sidebarWidth
	}
	return m.width
}

// layout sizes the viewport to whatever the bottom panel leaves over.
func (m *Model) layout() {
	if m.width == 0 {
		return
	}
	m.input.SetWidth(m.mainWidth() - 4)
	if rows := sparkRows(m.height - 1); rows > 0 && m.sparks != nil {
		m.sparks.resize(sidebarWidth-3, rows)
	}
	m.bottomH = lipgloss.Height(m.bottomView())
	bottom := m.bottomH + 1 + 2 // + status bar, and a blank row above and below the chat
	if m.showHeader() {
		bottom++
	}
	h := max(3, m.height-bottom)
	m.vp.Width, m.vp.Height = m.chatWidth(), h
}

func (m *Model) refresh() {
	if !m.ready {
		return
	}
	content := m.chat.View(m.chatWidth())
	if len(m.chat.entries) == 0 {
		content = m.splash()
	}
	m.chatContent = content
	m.vp.SetContent(content)
	switch {
	case m.follow:
		m.vp.GotoBottom()
	case m.scrollToSel:
		m.ensureSelVisible()
	}
	m.scrollToSel = false
}

func (m Model) View() string {
	if !m.ready {
		return ""
	}
	screen := m.viewMain()
	switch m.panelMode {
	case sessionsPanel:
		screen = lipgloss.JoinHorizontal(lipgloss.Top, m.sessionsView(m.termH), screen)
	case sessionsTabs:
		screen = lipgloss.JoinVertical(lipgloss.Left, m.tabsView(m.termW), screen)
	}
	// Strip any control sequence that slipped in with file contents, tool
	// output or the like; only colours and text attributes get through.
	return safetext.Screen(screen)
}

func (m Model) viewMain() string {
	chatView := lipgloss.JoinHorizontal(lipgloss.Top, m.highlightSelection(m.vp.View()), strings.Join(m.scrollbar(), "\n"))
	if pop := m.comp.view(m.mainWidth()); pop != "" {
		chatView = overlayBottom(chatView, pop, m.vp.Height, m.mainWidth())
	}
	main := lipgloss.JoinVertical(lipgloss.Left, "", chatView, "", m.bottomView())
	if m.showHeader() {
		main = lipgloss.JoinVertical(lipgloss.Left, headerLine(m.mainWidth(), m.headerInfo()), main)
	}
	if m.showSidebar() {
		main = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(m.mainWidth()).Render(main), m.sidebarView(m.height-1))
	}
	screen := lipgloss.JoinVertical(lipgloss.Left, main, m.statusView())

	if m.showDrawer() {
		top := 0
		if m.showHeader() {
			top = 1
		}
		screen = overlay(screen, m.sidebarView(m.height-1-top), m.width-sidebarWidth, top)
	}
	screen = m.drawToasts(screen)

	// Floating dialogs.
	h := m.height - 1
	switch {
	case m.showHelp:
		screen = center(screen, helpDialog(m.width-4), m.width, h)
	case m.themePick != nil:
		// Pinned to the right so the live preview leaves the chat visible.
		box := m.themePick.view(m.width)
		screen = overlay(screen, box, max(0, m.width-lipgloss.Width(box)-1), max(0, (h-lipgloss.Height(box))/2))
	case m.modelPick != nil:
		screen = center(screen, m.modelPick.view(m.width), m.width, h)
	case m.palette != nil:
		screen = center(screen, m.palette.view(m.width), m.width, h)
	case m.picker != nil:
		screen = center(screen, m.picker.view(m.width, h), m.width, h)
	case m.changes != nil:
		x, y := m.changesPos()
		screen = overlay(screen, m.changesView(), x, y)
	case m.diffView != nil || m.editPanel != nil && len(m.pending) > 0:
		x, y := m.panelPos()
		screen = overlay(screen, m.panelView(x, y), x, y)
	}
	return screen
}

// showHeader is true when the compact brand strip replaces the sidebar.
func (m Model) showHeader() bool { return !m.showSidebar() && len(m.chat.entries) > 0 }

func (m Model) headerInfo() string {
	var parts []string
	if m.model != "" {
		parts = append(parts, strings.TrimSpace(m.model+" "+m.opts.Effort))
	}
	if b := m.branchLabel(true); b != "" {
		parts = append(parts, b)
	}
	parts = append(parts, prettyPath(m.cwd()))
	hint := " • " + statusKey.Render("ctrl+b") + mutedStyle.Render(" sidebar")
	if m.width < sidebarMinWidth {
		hint = " • " + statusKey.Render("ctrl+b") + mutedStyle.Render(" details")
		if m.drawer {
			hint = " • " + statusKey.Render("ctrl+b") + mutedStyle.Render(" close")
		}
	}
	info := truncateLeft(strings.Join(parts, " • "), max(10, m.mainWidth()/2-lipgloss.Width(hint)))
	return mutedStyle.Render(info) + hint
}

// overlayBottom draws pop over the last lines of base.
func overlayBottom(base, pop string, height, width int) string {
	lines := strings.Split(base, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	pl := strings.Split(pop, "\n")
	start := max(0, len(lines)-len(pl))
	for i, l := range pl {
		if start+i < len(lines) {
			lines[start+i] = l + strings.Repeat(" ", max(0, width-lipgloss.Width(l)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) bottomView() string {
	if m.quitAsk {
		return m.quitView()
	}
	if len(m.pending) > 0 {
		return m.permissionView()
	}
	box := inputBox
	if m.input.Focused() {
		box = inputBoxFocused
	}
	var top string
	if m.busy {
		label := firstNonEmpty(m.activity, "Working")
		if m.status != "" && m.status != "requesting" {
			label = strings.ToUpper(m.status[:1]) + m.status[1:]
		}
		extra := fmt.Sprintf(" %ds", int(time.Since(m.turnFrom).Seconds()))
		if n := m.track.runningAgents(); n > 0 {
			plural := ""
			if n > 1 {
				plural = "s"
			}
			extra += fmt.Sprintf(" · %d agent%s running", n, plural)
		}
		top = " " + shimmer(m.animFrame, label) + mutedStyle.Render(extra) + "\n"
	}
	if p := m.pillsView(m.mainWidth()); p != "" {
		top = p + "\n" + top
	}
	if a := m.attachmentsView(m.mainWidth()); a != "" {
		top += a + "\n"
	}
	return top + box.Width(m.mainWidth()-2).Render(m.input.View())
}

func (m Model) permissionView() string {
	switch {
	case m.question != nil:
		return m.questionView(m.mainWidth())
	case m.plan != nil:
		return m.planView(m.mainWidth())
	case m.editPanel != nil:
		return m.panelWaiting()
	}
	p := m.pending[0]
	w := m.mainWidth() - 6
	title := warnStyle.Bold(true).Render("Allow " + p.ToolName + "?")
	if p.AgentID != "" {
		for _, a := range m.track.agents {
			if a.taskID == p.AgentID {
				title += " " + agentStyle.Render("("+a.kind+": "+truncate(a.desc, 30)+")")
			}
		}
	}
	if len(m.pending) > 1 {
		title += mutedStyle.Render(fmt.Sprintf("  +%d queued", len(m.pending)-1))
	}
	detail := m.prettyInput(p.ToolName, p.Input, w)
	if p.Description != "" && filePathOf(p.Input) == "" {
		if p.ToolName == "Bash" {
			detail += "\n" + mutedStyle.Render(p.Description)
		} else {
			detail = mutedStyle.Render(p.Description) + "\n" + detail
		}
	}
	keys := statusKey.Render("y") + mutedStyle.Render(" allow  ") +
		statusKey.Render("a") + mutedStyle.Render(" always allow "+p.ToolName+"  ") +
		statusKey.Render("n") + mutedStyle.Render(" deny  ") +
		statusKey.Render("ctrl+c") + mutedStyle.Render(" deny & stop")
	if isDiffTool(p.ToolName) {
		layout := "split view"
		if splitDiff(w) {
			layout = "unified view"
		}
		keys += statusKey.Render("  t") + mutedStyle.Render(" "+layout)
	}
	return permBox.Width(m.mainWidth() - 2).Render(title + "\n\n" + detail + "\n\n" + keys)
}

func (m Model) prettyInput(tool string, raw json.RawMessage, width int) string {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	fp := filePathOf(raw)
	rel := relPath(fp, m.cwd())
	switch tool {
	case "Bash":
		return lipgloss.NewStyle().Width(width).Render(statusKey.Render("$ ") + textStyle.Render(safetext.Clean(str(in, "command"))))
	case "Edit":
		return textStyle.Render(rel) + "\n" + renderDiff(computeDiff(str(in, "old_string"), str(in, "new_string")), fp, width, 14)
	case "MultiEdit":
		var all []diffLine
		edits, _ := in["edits"].([]any)
		for i, ed := range edits {
			e, _ := ed.(map[string]any)
			if i > 0 {
				all = append(all, diffLine{kind: '@', text: "⋯"})
			}
			all = append(all, computeDiff(str(e, "old_string"), str(e, "new_string"))...)
		}
		return textStyle.Render(rel) + "\n" + renderDiff(all, fp, width, 14)
	case "Write":
		return textStyle.Render(rel) + "\n" + renderDiff(addedLines(str(in, "content")), fp, width, 12)
	case "WebFetch":
		return textStyle.Render(safetext.Line(str(in, "url")))
	}
	b, _ := json.MarshalIndent(in, "", "  ")
	return mutedStyle.Render(clipText(string(b), width, 12))
}

func (m Model) statusView() string {
	sep := mutedStyle.Render(" │ ")
	var parts []string
	mode := mutedStyle.Render("● default")
	switch m.permMode {
	case "acceptEdits":
		mode = okStyle.Render(icons.modeEdits + " accept edits")
	case "plan":
		mode = warnStyle.Render(icons.modePlan + " plan mode")
	case "bypassPermissions":
		mode = errStyle.Render(icons.modeBypass + " bypass permissions")
	}
	parts = append(parts, mode+mutedStyle.Render(" ⇧⇥"))
	if !m.showSidebar() {
		if m.model != "" {
			parts = append(parts, strings.TrimSpace(m.model+" "+mutedStyle.Render(m.opts.Effort)))
		}
		if b := m.branchLabel(true); b != "" {
			parts = append(parts, agentStyle.Render(b))
		}
		if ctx := contextTokens(m.lastUsage); ctx > 0 {
			parts = append(parts, "ctx "+humanTokens(ctx))
		}
		if w, ok := m.limits["five_hour"]; ok {
			parts = append(parts, usageGauge("5h", w))
		}
		if w, ok := m.limits["seven_day"]; ok {
			parts = append(parts, usageGauge("7d", w))
		}
	}
	if m.costUSD >= 0.01 && m.showSidebar() {
		parts = append(parts, mutedStyle.Render(fmt.Sprintf("≈$%.2f api-equiv", m.costUSD)))
	}
	left := strings.Join(parts, sep)
	hints := helpLine(m.helpPairs(), m.width-4-lipgloss.Width(left))
	gap := m.width - 2 - lipgloss.Width(left) - lipgloss.Width(hints)
	return statusBar.Width(m.width).Render(left + strings.Repeat(" ", max(1, gap)) + hints)
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
		if d := time.Until(time.Unix(w.ResetsAt, 0)).Round(time.Minute); d > 0 {
			reset = mutedStyle.Render(" " + icons.reset + shortDur(d))
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
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatTokens(u claude.Usage) string {
	return fmt.Sprintf("%s in · %s out", humanTokens(contextTokens(u)), humanTokens(u.OutputTokens))
}

func (m Model) splash() string {
	w := m.mainWidth()
	key := func(k, desc string) string { return statusKey.Render(k) + mutedStyle.Render(" "+desc) }
	lines := []string{""} // under the blank row View puts above the chat
	for _, l := range strings.Split(bigLogo("CLAUDEWITZ", w-2), "\n") {
		lines = append(lines, "  "+l)
	}
	lines = append(lines,
		"",
		mutedStyle.Render("  A terminal front end for Claude Code, running on your Claude subscription."),
		"",
		"  "+key("/", "commands")+"     "+key("@", "mention a file")+"     "+key("ctrl+p", "palette"),
		"  "+key("ctrl+r", "sessions")+"   "+key("ctrl+t", "model")+"      "+key("ctrl+g", "shortcuts"),
		"",
		"  "+textStyle.Render(prettyPath(m.opts.CWD)),
	)
	if m.initResp != nil {
		info := fmt.Sprintf("  claude %s • %d commands • %s", m.initResp.Version, len(m.initResp.Commands), m.model)
		if sub := m.initResp.Account.SubscriptionType; sub != "" {
			info += " • " + sub
		}
		lines = append(lines, mutedStyle.Render(info))
	}
	return strings.Join(lines, "\n")
}

const helpText = `commands
  /new, /clear        start a fresh session
  /resume [id]        browse sessions (or resume one by id)
  /model [name]       switch model (no name opens the picker, ctrl+t)
  /effort <level>     effort: low, medium, high, xhigh, max
  /mode <mode>        permission mode: default, acceptEdits, plan
  /theme [name]       colour theme (no name opens the picker); saved to config
  /sidebar            toggle the sidebar
  /changes            review changed files as diffs (ctrl+s)
  /edit               write the prompt in $EDITOR (ctrl+x)
  /image <path>       attach an image (ctrl+f; ctrl+v pastes one; or drag a file in)
  /notifications [m]  desktop notifications: auto, osc, bell, off
  /icons [set]        icon set: nerd (Nerd Font) or unicode
  /diff [layout]      diffs: auto, split (side by side) or unified
  /session            show session id and cwd
  /quit               exit
  other /commands are passed through to Claude Code
  ctrl+p opens the command palette, ctrl+g shows all shortcuts

keys
  enter      send               ctrl+j / alt+enter  newline
  esc        interrupt          shift+tab           cycle permission mode
  ctrl+r     sessions           ctrl+b              toggle sidebar
  ctrl+s     changed files      ctrl+t              model & effort
  ctrl+o     expand output      pgup/pgdown, wheel  scroll
  tab        accept completion  ctrl+c              interrupt / clear / quit`
