package tui

import (
	"os"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// sessionView is everything the TUI holds for one session: its transcript,
// input, dialogs and the CLI's state. Model embeds the focused one, so
// m.chat, m.busy and friends always mean the session on screen.
type sessionView struct {
	id   string         // the daemon's id for the session; "" when run directly
	opts claude.Options // how it was started (its folder, model, effort)
	proc claude.Session

	chat        *chat
	track       *tracker
	queued      []queuedMsg  // sent mid-turn; shown as a pill until the CLI takes them
	attachments []attachment // images for the next message
	suggestion  string       // predicted next prompt, shown in the empty input
	input       textarea.Model
	vp          viewport.Model
	follow      bool   // keep viewport pinned to bottom
	chatFocus   bool   // keys drive the transcript instead of the input
	chatContent string // last rendered transcript, for copying selections
	scrollToSel bool   // bring the selected entry into view on next refresh

	busy       bool
	stopped    bool // user interrupted the current turn
	status     string
	activity   string // what the live turn is doing, for the shimmer label
	turnFrom   time.Time
	lastTurn   time.Duration // how long the last turn took, for "Done in …"
	lastTurnAt time.Time

	init         *claude.InitEvent
	initResp     *claude.InitializeResponse
	sessionID    string
	sessionTitle string
	model        string
	permMode     string
	costUSD      float64
	lastUsage    claude.Usage
	ctxWindow    int

	pending     []*claude.PermissionRequest
	alwaysAllow map[string]bool
	question    *questionState // AskUserQuestion at the head of pending
	plan        *planState     // ExitPlanMode at the head of pending
	editPanel   *editPanel     // Edit, MultiEdit or Write at the head of pending
	diffView    *editPanel     // a transcript edit's diff, opened read-only
}

func newSessionView(opts claude.Options) *sessionView {
	if opts.CWD == "" {
		opts.CWD, _ = os.Getwd()
	}
	pm := opts.PermissionMode
	if pm == "" {
		pm = "default"
	}
	tr := newTracker()
	c := newChat(tr)
	c.cwd = opts.CWD
	vp := viewport.New(0, 0)
	vp.MouseWheelEnabled = true
	return &sessionView{
		opts:        opts,
		chat:        c,
		track:       tr,
		input:       newInput(),
		vp:          vp,
		follow:      true,
		permMode:    pm,
		model:       opts.Model,
		alwaysAllow: map[string]bool{},
	}
}

// newInput is the message box, the same for every session.
func newInput() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = inputPlaceholder
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.SetHeight(3)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = mutedStyle
	ta.BlurredStyle.Placeholder = mutedStyle
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")
	ta.Focus()
	return ta
}
