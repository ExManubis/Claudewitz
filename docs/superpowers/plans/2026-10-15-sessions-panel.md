# Sessions Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One claudewitz TUI shows every live daemon session in a panel (or a tab strip when narrow), switches between them, starts new ones in any folder and kills them, so the daemon can become the default again.

**Architecture:** The TUI's `Model` keeps its fields but moves the per-session ones into `sessionView` and the per-folder ones into `workspace`, both embedded in `Model` as pointers, so existing code (`m.chat`, `m.busy`, `m.git`) compiles unchanged and switching sessions is swapping two pointers. A small `Daemon` interface (implemented by `client.Hub`) gives the TUI the session list, overview pushes, start and attach. When the panel or strip is shown, `m.width`/`m.height` become the area left for today's layout and the panel is joined on the left (or the strip on top) in `View`, with mouse coordinates shifted once on the way in.

**Tech Stack:** Go 1.24, Bubble Tea, Bubbles (textarea, viewport, textinput), Lip Gloss, gorilla/websocket (daemon client).

**Spec:** `docs/superpowers/specs/2026-10-15-sessions-panel-design.md`

## Global Constraints

- Panel width: 40 columns. Panel shown at ≥ 140 terminal columns; below that, a one-line tab strip.
- Neither panel nor strip with fewer than two sessions, without the daemon, or when toggled off (`alt+s`).
- Row order: waiting, error, working, done, idle; within a state, the order sessions started in. Focus never moves rows.
- Rows: line 1 glyph, title (or `new session`), time in state on the right; line 2 the folder label in the theme's `second` colour. No activity text.
- Glyphs: waiting `◌` (warn), error `✗` (err), working `▶` (accent), done `✓` (ok), idle `○` (muted).
- Folder label: base name; parent added on name clashes (`code/api`); `repo ⎇ worktree` for linked worktrees.
- Header: `SESSIONS`, and `▲ n need you` (warn) when any session is waiting or errored, else the count.
- Keys: `alt+s` toggle panel/strip, `alt+up`/`alt+down` previous/next, `alt+1`‥`alt+9` by position, `ctrl+]` next that needs you, `alt+n` new session, `ctrl+q` kill session. All also in the command palette.
- Kill dialog wording: `Kill session <title>?`, folder, `▲ It's working right now; the current turn is lost.` (only when working or waiting), `claude ends; the transcript is kept, ctrl+r resumes it.`, choices `1. Keep it running  n · esc` (default) and `2. Kill session  y`.
- Quit detaches every daemon session; exit line `n sessions keep running in the claudewitz daemon · claudewitz to come back`.
- Every test run uses `-race`: `go test -race ./...`.
- Commit messages: imperative, no prefixes, no attribution lines (match `git log`).

## Review Focus

- **A background session's event while a dialog is open on the focused one** must not touch the focused session's dialog, input or scroll. Pinned in Task 2 (`TestBackgroundEventsLeaveFocusedAlone`).
- **The focused session is killed or exits while another needs you**: focus moves to the next row, never to nothing while sessions remain. Pinned in Task 7 (`TestKillFocusesNext`).
- **Terminal resized across 140 columns with the panel open**: layout switches between panel and strip without the chat overflowing. Pinned in Task 5 (`TestSessionsModeByWidth` resizes both ways and checks `width`).
- **Two sessions in folders with the same name**, and a worktree whose folder name is random: labels stay distinct and meaningful. Pinned in Task 4 (`TestFolderLabels`).
- **A late git, file-index or history result for a folder that's no longer focused** must not land in the focused folder. Pinned in Task 2 (`TestLateResultsGoToTheirWorkspace`).

---

## File Structure

- Create `internal/tui/view.go`: `sessionView` (per-session state), `newSessionView`, `newInput`.
- Create `internal/tui/workspace.go`: `workspace` (per-folder state), `newWorkspace`.
- Create `internal/tui/route.go`: `viewFor`, `inView`, `focus`, workspace lookup and pruning.
- Create `internal/tui/daemon.go`: the `Daemon` interface, overview state, rows (`rowState`, `orderRows`, `folderLabels`, `ageLabel`), overview message handling.
- Create `internal/tui/sessionspanel.go`: `sessionsMode`, `resize`, `sessionsView`, `tabsView`, mouse translation.
- Create `internal/tui/switch.go`: `focusSession`, attach, launch focus, session keys, needs-you toast.
- Create `internal/tui/kill.go`: kill dialog.
- Create `internal/tui/folderpicker.go`: new-session folder picker.
- Create `internal/daemon/repo.go`: `repoOf`.
- Create `internal/daemon/client/hub.go`: `Hub`, the TUI's daemon adapter.
- Modify `internal/tui/model.go`, `internal/tui/git.go`, `internal/tui/complete.go`, `internal/tui/history.go`, `internal/tui/icons.go`, `internal/tui/help.go`, `internal/tui/palette.go`, `internal/daemon/protocol.go`, `internal/daemon/server.go`, `main.go`, `daemon.go`, `README.md`, `docs/daemon.md`.
- Tests: `internal/tui/view_test.go`, `internal/tui/route_test.go`, `internal/tui/daemon_test.go`, `internal/tui/sessionspanel_test.go`, `internal/tui/switch_test.go`, `internal/tui/kill_test.go`, `internal/tui/folderpicker_test.go`, `internal/daemon/repo_test.go`, `internal/daemon/client/hub_test.go`, and a shared fake in `internal/tui/fakes_test.go`.

---

### Task 1: Split per-session and per-folder state out of Model

**Files:**
- Create: `internal/tui/view.go`
- Create: `internal/tui/workspace.go`
- Modify: `internal/tui/model.go` (the `Model` struct at lines 29-124, `New` at 136-193, `layout` at 1515-1535)
- Test: `internal/tui/view_test.go`

**Interfaces:**
- Produces: `type sessionView struct` (fields below, plus `id string`, `opts claude.Options`, `proc claude.Session`); `func newSessionView(opts claude.Options) *sessionView`; `func newInput() textarea.Model`; `type workspace struct{ dir string; files []string; git gitstatus.Status; gitLoaded, gitBusy bool; gitPolls int; hist *history }`; `func newWorkspace(dir string) *workspace`; on `Model`: embedded `*sessionView` and `*workspace`, `views []*sessionView`, `spaces map[string]*workspace`, `base claude.Options`.

This is a pure move: no behaviour changes. The 104 existing TUI tests are the safety net.

- [ ] **Step 1: Write the failing test**

`internal/tui/view_test.go`:

```go
package tui

import (
	"context"
	"testing"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

// Each session view keeps its own draft, transcript and pending dialogs, so
// switching to another session and back loses nothing.
func TestViewsKeepSeparateState(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	first := m.sessionView
	m.input.SetValue("draft one")
	m.pending = append(m.pending, &claude.PermissionRequest{RequestID: "r1", ToolName: "Bash"})
	m.chat.info("first session")

	second := newSessionView(claude.Options{CWD: t.TempDir()})
	m.views = append(m.views, second)
	m.sessionView = second
	if m.input.Value() != "" || len(m.pending) != 0 || len(m.chat.entries) != 0 {
		t.Fatalf("a new view shares state: input %q, %d pending, %d entries", m.input.Value(), len(m.pending), len(m.chat.entries))
	}
	m.input.SetValue("draft two")

	m.sessionView = first
	if m.input.Value() != "draft one" || len(m.pending) != 1 || len(m.chat.entries) != 1 {
		t.Fatalf("switching back lost state: input %q, %d pending, %d entries", m.input.Value(), len(m.pending), len(m.chat.entries))
	}
	if second.input.Value() != "draft two" {
		t.Fatalf("second draft: %q", second.input.Value())
	}
}

func TestNewSessionViewDefaults(t *testing.T) {
	v := newSessionView(claude.Options{CWD: "/tmp/x"})
	if v.opts.CWD != "/tmp/x" || v.permMode != "default" || !v.follow || v.alwaysAllow == nil || v.chat == nil || v.track == nil {
		t.Fatalf("defaults: %+v", v)
	}
	if v.chat.cwd != "/tmp/x" {
		t.Fatalf("chat cwd %q", v.chat.cwd)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/tui -run 'TestViewsKeepSeparateState|TestNewSessionViewDefaults'`
Expected: FAIL to compile with `undefined: newSessionView` and `m.sessionView undefined`.

- [ ] **Step 3: Create `internal/tui/view.go`**

Move these fields out of `Model` into `sessionView`, keeping their comments. Every one of them must be deleted from `Model`; leaving a duplicate makes the embedded field unreachable.

```go
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
```

- [ ] **Step 4: Create `internal/tui/workspace.go`**

Move `files`, `git`, `gitLoaded`, `gitBusy`, `gitPolls` and `hist` out of `Model`:

```go
package tui

import "github.com/exmanubis/claudewitz/internal/gitstatus"

// workspace is what the TUI holds for one folder: its git status, the files
// @ completes and the prompt history. Sessions in the same folder share one;
// Model embeds the focused session's.
type workspace struct {
	dir       string
	files     []string
	git       gitstatus.Status
	gitLoaded bool
	gitBusy   bool
	gitPolls  int // poll ticks so far, for reading less often while idle
	hist      *history
}

func newWorkspace(dir string) *workspace {
	return &workspace{dir: dir, hist: newHistory(dir)}
}
```

- [ ] **Step 5: Embed them in `Model` and build it in `New`**

In the `Model` struct, delete every field moved in steps 3-4 (including `opts`, `proc`, `vp`, `input`, `follow`, `busy`, `stopped`, `status`, `activity`, `turnFrom`, `lastTurn`, `lastTurnAt`, the `// session state` block except `limits`, the `// permissions` block, `files`, `git`, `gitLoaded`, `gitBusy`, `gitPolls`, `hist`). Keep `limits` and `limitWarned` (account-wide). Add at the top of the struct:

```go
type Model struct {
	*sessionView // the focused session
	*workspace   // the focused session's folder

	views  []*sessionView        // every opened session, the focused one included
	spaces map[string]*workspace // by folder
	base   claude.Options        // flags every new session starts from

	ctx   context.Context
	start Starter
	// … the remaining shell fields, unchanged …
```

Replace the body of `New` after the `applyTheme` block with:

```go
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
```

In `layout`, the viewport now always exists, so replace

```go
	if !m.ready {
		m.vp = viewport.New(m.chatWidth(), h)
		m.vp.MouseWheelEnabled = true
	} else {
		m.vp.Width, m.vp.Height = m.chatWidth(), h
	}
```

with

```go
	m.vp.Width, m.vp.Height = m.chatWidth(), h
```

Remove imports `model.go` no longer uses (`textarea`, `viewport` if unused); `go build` lists them.

- [ ] **Step 6: Build and run all tests**

Run: `go build ./... && go vet ./internal/tui && go test -race ./internal/tui`
Expected: build succeeds; all tests pass, including the two new ones. A compile error like `ambiguous selector m.x` means a field exists in two places: delete the `Model` copy.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/view.go internal/tui/workspace.go internal/tui/model.go internal/tui/view_test.go
git commit -m "Split the TUI's per-session and per-folder state out of Model"
```

---

### Task 2: Route messages to their session and folder

**Files:**
- Create: `internal/tui/route.go`
- Create: `internal/tui/fakes_test.go`
- Modify: `internal/tui/model.go` (`startedMsg`, `startCmd`, `procEvent` handling, `transcriptMsg`, `loadTranscript`, `Init`, `restart`, the `/resume` slash command at line ~1369, `filesIndexedMsg` and `historyLoadedMsg` cases)
- Modify: `internal/tui/complete.go` (`filesIndexedMsg`, `indexFiles`)
- Modify: `internal/tui/git.go` (`applyGit`)
- Test: `internal/tui/route_test.go`

**Interfaces:**
- Consumes: `sessionView`, `workspace`, `Model.views`, `Model.spaces` from Task 1.
- Produces: `func (m *Model) viewFor(p claude.Session) *sessionView`; `func (m *Model) inView(v *sessionView, f func())`; `func (m *Model) focus(v *sessionView) tea.Cmd`; `func (m *Model) spaceFor(dir string) (*workspace, bool)` (creates when missing; bool reports created); `func (m *Model) removeView(v *sessionView)`; `startedMsg{view *sessionView; proc claude.Session; id string; err error}`; `func (m *Model) startCmd(v *sessionView, opts claude.Options) tea.Cmd`; `transcriptMsg.view *sessionView`; `func loadTranscript(v *sessionView, cwd, id string) tea.Cmd`; `filesIndexedMsg{dir string; files []string}`; test fake `fakeSession` with `newFakeSession() *fakeSession` and `func (f *fakeSession) emit(ev claude.Event)`.

- [ ] **Step 1: Write the fake session (test-only)**

`internal/tui/fakes_test.go`:

```go
package tui

import (
	"sync"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// fakeSession is a claude.Session the tests feed events into by hand.
type fakeSession struct {
	mu       sync.Mutex
	events   chan claude.Event
	sent     []string
	stopped  bool
	detached bool
}

func newFakeSession() *fakeSession { return &fakeSession{events: make(chan claude.Event, 64)} }

func (f *fakeSession) emit(ev claude.Event)          { f.events <- ev }
func (f *fakeSession) Events() <-chan claude.Event   { return f.events }
func (f *fakeSession) Send(text string) error        { return f.SendWithImages(text, nil) }
func (f *fakeSession) Interrupt() error              { return nil }
func (f *fakeSession) SetModel(string) error         { return nil }
func (f *fakeSession) SetEffort(string) error        { return nil }
func (f *fakeSession) SetPermissionMode(string) error { return nil }
func (f *fakeSession) SendWithImages(text string, _ []claude.Image) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeSession) Respond(*claude.PermissionRequest, claude.PermissionDecision) error { return nil }
func (f *fakeSession) RespondAndInterrupt(*claude.PermissionRequest, claude.PermissionDecision) error {
	return nil
}
func (f *fakeSession) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
}
func (f *fakeSession) Detach() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detached = true
	return nil
}
```

- [ ] **Step 2: Write the failing tests**

`internal/tui/route_test.go`:

```go
package tui

import (
	"context"
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/gitstatus"
)

// readyModel is a Model sized like a normal terminal.
func readyModel(t *testing.T) Model {
	t.Helper()
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return next.(Model)
}

func assistantText(text string) claude.Event {
	raw, _ := json.Marshal(map[string]any{
		"type":    "assistant",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}}},
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test -race ./internal/tui -run 'TestBackgroundEvents|TestEventsFromUnknown|TestLateResults|TestFocusKeeps|TestRemoveView'`
Expected: FAIL to compile (`m.focus undefined`, `filesIndexedMsg{dir:…}` unknown field, `removeView undefined`).

- [ ] **Step 4: Create `internal/tui/route.go`**

```go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// viewFor finds the view a process belongs to; nil for a stale process.
func (m *Model) viewFor(p claude.Session) *sessionView {
	if p == nil {
		return nil
	}
	for _, v := range m.views {
		if v.proc == p {
			return v
		}
	}
	return nil
}

// inView runs f with v standing in for the focused session, for a session
// in the background. What f renders lands in v's own viewport.
func (m *Model) inView(v *sessionView, f func()) {
	focused := m.sessionView
	m.sessionView = v
	defer func() { m.sessionView = focused }()
	f()
}

// spaceFor returns dir's workspace, creating it (and reporting so) when no
// view has used that folder yet.
func (m *Model) spaceFor(dir string) (*workspace, bool) {
	if ws, ok := m.spaces[dir]; ok {
		return ws, false
	}
	ws := newWorkspace(dir)
	m.spaces[dir] = ws
	return ws, true
}

// focus shows v: its transcript, input and dialogs, and its folder's git
// status, files and history.
func (m *Model) focus(v *sessionView) tea.Cmd {
	if v == m.sessionView {
		return nil
	}
	m.input.Blur()
	m.sessionView = v
	dir := m.cwd()
	ws, created := m.spaceFor(dir)
	m.workspace = ws
	var cmds []tea.Cmd
	if created {
		cmds = append(cmds, indexFiles(dir), loadHistory(dir))
	}
	cmds = append(cmds, m.refreshGit())
	if !m.chatFocus && len(m.pending) == 0 {
		m.input.Focus()
	}
	m.comp.update(m)
	m.layout()
	m.refresh()
	return tea.Batch(cmds...)
}

// removeView forgets v, and its folder's workspace when no other view
// uses it. v must not be the focused view.
func (m *Model) removeView(v *sessionView) {
	for i, x := range m.views {
		if x == v {
			m.views = append(m.views[:i:i], m.views[i+1:]...)
			break
		}
	}
	dir := v.opts.CWD
	if v.init != nil && v.init.CWD != "" {
		dir = v.init.CWD
	}
	for _, x := range m.views {
		xd := x.opts.CWD
		if x.init != nil && x.init.CWD != "" {
			xd = x.init.CWD
		}
		if xd == dir {
			return
		}
	}
	if m.workspace == nil || m.workspace.dir != dir {
		delete(m.spaces, dir)
	}
}
```

- [ ] **Step 5: Route process events, starts and transcripts in `model.go`**

Replace `startedMsg`, `startCmd` and their handling:

```go
type startedMsg struct {
	view *sessionView
	proc claude.Session
	id   string // the daemon's session id, when started there
	err  error
}

func (m *Model) startCmd(v *sessionView, opts claude.Options) tea.Cmd {
	ctx, start := m.ctx, m.start
	return func() tea.Msg {
		p, err := start(ctx, opts)
		return startedMsg{view: v, proc: p, err: err}
	}
}
```

```go
	case startedMsg:
		v := msg.view
		if msg.err != nil {
			m.inView(v, func() {
				m.chat.error("could not start claude: %v", msg.err)
				m.refresh()
			})
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
```

Add `view *sessionView` as the first field of `transcriptMsg` and change `loadTranscript`:

```go
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
```

Update the callers: in `Init`, `m.startCmd(m.opts)` → `m.startCmd(m.sessionView, m.opts)` and `loadTranscript(m.opts.CWD, m.opts.Resume)` → `loadTranscript(m.sessionView, m.opts.CWD, m.opts.Resume)`; in `Init` also `indexFiles(m.opts.CWD)` stays (its message now carries the dir). In `restart`, `return m.startCmd(opts)` → `return m.startCmd(m.sessionView, opts)`. In the `/resume` slash command, `loadTranscript(m.opts.CWD, arg)` → `loadTranscript(m.sessionView, m.opts.CWD, arg)`.

- [ ] **Step 6: Route folder results**

In `complete.go`, change the message and both returns in `indexFiles`:

```go
type filesIndexedMsg struct {
	dir   string
	files []string
}
```

```go
			return filesIndexedMsg{dir: dir, files: withDirs(files)}
```

(The function sets `dir = "."` when empty; capture the original first: add `orig := dir` at the top of the returned func and use `filesIndexedMsg{dir: orig, …}` in both returns.)

In `model.go`:

```go
	case filesIndexedMsg:
		if ws, ok := m.spaces[msg.dir]; ok {
			ws.files = msg.files
		}
```

```go
	case historyLoadedMsg:
		if ws, ok := m.spaces[msg.project]; ok {
			ws.hist.items = dedupeKeepLast(append(msg.items, ws.hist.items...))
		}
```

In `git.go`, start `applyGit` with:

```go
func (m *Model) applyGit(msg gitStatusMsg) tea.Cmd {
	if ws, ok := m.spaces[msg.dir]; ok && ws != m.workspace {
		ws.gitBusy = false
		ws.git, ws.gitLoaded = msg.st, true
		return nil // a folder in the background; no toasts for it
	}
	m.gitBusy = false
	// … the rest unchanged …
```

- [ ] **Step 7: Run all TUI tests**

Run: `go vet ./internal/tui && go test -race ./internal/tui`
Expected: PASS, including the five new tests.

- [ ] **Step 8: Commit**

```bash
git add internal/tui
git commit -m "Route each session's events and each folder's results to their own view"
```

---

### Task 3: Daemon repository fields and the TUI's daemon adapter

**Files:**
- Create: `internal/daemon/repo.go`, `internal/daemon/repo_test.go`
- Modify: `internal/daemon/protocol.go` (`SessionSummary`), `internal/daemon/server.go` (`start`, after `sum` is built)
- Create: `internal/daemon/client/hub.go`, `internal/daemon/client/hub_test.go`
- Modify: `docs/daemon.md` (SessionSummary example)

**Interfaces:**
- Produces: `SessionSummary.Repo *string` (`json:"repo"`) and `SessionSummary.Worktree *string` (`json:"worktree"`); `func repoOf(cwd string) (repo, worktree *string)`; `type Hub struct{ C *Client }` with `Start(ctx, claude.Options) (claude.Session, string, error)`, `Attach(ctx, id string) (claude.Session, error)`, `List(ctx) ([]daemon.SessionSummary, error)`, `Overview() <-chan daemon.Frame`.

- [ ] **Step 1: Write the failing `repoOf` test**

`internal/daemon/repo_test.go`:

```go
package daemon

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRepoOf(t *testing.T) {
	if r, w := repoOf(t.TempDir()); r != nil || w != nil {
		t.Fatalf("not a repo: %v %v", r, w)
	}

	main, _ := filepath.EvalSymlinks(t.TempDir())
	git(t, main, "init", "-q")
	git(t, main, "commit", "-q", "--allow-empty", "-m", "first")
	git(t, main, "worktree", "add", "-q", filepath.Join(main, ".claude", "worktrees", "ring-tests"))

	r, w := repoOf(main)
	if r == nil || *r != main || w != nil {
		t.Fatalf("main worktree: %v %v", r, w)
	}
	r, w = repoOf(filepath.Join(main, ".claude", "worktrees", "ring-tests"))
	if r == nil || *r != main || w == nil || *w != "ring-tests" {
		t.Fatalf("linked worktree: %v %v", r, w)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/daemon -run TestRepoOf`
Expected: FAIL to compile, `undefined: repoOf`.

- [ ] **Step 3: Implement `internal/daemon/repo.go`**

```go
package daemon

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// repoOf finds the repository cwd belongs to: its main worktree's root, and
// the linked worktree's folder name when cwd is in one. Both are nil
// outside git.
func repoOf(cwd string) (repo, worktree *string) {
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--path-format=absolute",
		"--show-toplevel", "--git-common-dir").Output()
	if err != nil {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return nil, nil
	}
	top, common := filepath.Clean(lines[0]), filepath.Clean(lines[1])
	main := top
	if filepath.Base(common) == ".git" {
		main = filepath.Dir(common)
	}
	if main == top {
		return &main, nil
	}
	name := filepath.Base(top)
	return &main, &name
}
```

- [ ] **Step 4: Add the fields and set them in `start`**

In `protocol.go`, after `LastError` in `SessionSummary`:

```go
	// Repo is the main worktree of the repository CWD is in; nil outside
	// git. Worktree is the linked worktree's folder name when CWD is in
	// one, for labels like "claudewitz ⎇ ring-tests".
	Repo     *string `json:"repo"`
	Worktree *string `json:"worktree"`
```

In `server.go` `start`, right after the `if sum.PermissionMode == "" { … }` block:

```go
	sum.Repo, sum.Worktree = repoOf(p.CWD)
```

In `docs/daemon.md`, in the SessionSummary example after `"lastError": null, …` add:

```jsonc
  "repo": "/Users/mikkel/Developer/claudewitz", // the repository's main worktree; null outside git
  "worktree": null,               // a linked worktree's folder name, e.g. "ring-tests"
```

Then copy it to the iOS repo: `cp docs/daemon.md ../claudewitz-ios/docs/protocol.md` (committed separately in that repo at the end of this task).

- [ ] **Step 5: Run the daemon tests**

Run: `go test -race ./internal/daemon/...`
Expected: PASS.

- [ ] **Step 6: Write the failing `Hub` test**

`internal/daemon/client/hub_test.go`:

```go
package client

import (
	"context"
	"testing"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

func TestHubStartAttachList(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	hub := Hub{C: c}
	ctx := context.Background()

	s, id, err := hub.Start(ctx, claude.Options{CWD: t.TempDir()})
	if err != nil || s == nil || id == "" {
		t.Fatalf("start: %v %v %q", s, err, id)
	}
	f := h.proc(t)

	list, err := hub.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list: %+v %v", list, err)
	}

	other := h.dial(t, "tui")
	att, err := Hub{C: other}.Attach(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	f.line(t, assistantLine)
	select {
	case ev := <-att.Events():
		if ev.Message == nil {
			t.Fatalf("attached session got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attached session got nothing")
	}

	select {
	case fr := <-hub.Overview():
		if fr.Event != daemon.EventSessionUpdated {
			t.Fatalf("overview: %s", fr.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no overview push")
	}
}
```

(`assistantLine` and `startDaemon`/`h.dial`/`h.proc` already exist in `client_test.go`.)

- [ ] **Step 7: Run to verify it fails**

Run: `go test -race ./internal/daemon/client -run TestHubStartAttachList`
Expected: FAIL to compile, `undefined: Hub`.

- [ ] **Step 8: Implement `internal/daemon/client/hub.go`**

```go
package client

import (
	"context"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// Hub is the TUI's view of the daemon: every live session, starting and
// attaching to them, and the overview pushes. It satisfies tui.Daemon.
type Hub struct{ C *Client }

// Start starts a session (or attaches to the live one being resumed) and
// returns it with the daemon's id for it.
func (h Hub) Start(ctx context.Context, opts claude.Options) (claude.Session, string, error) {
	s, err := Starter(h.C)(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	sess := s.(*Session)
	return sess, sess.ID, nil
}

// Attach follows a live session from now on; its history comes from the
// transcript on disk.
func (h Hub) Attach(ctx context.Context, id string) (claude.Session, error) {
	s, _, err := h.C.SubscribeLive(ctx, id)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// List returns every live session.
func (h Hub) List(ctx context.Context) ([]daemon.SessionSummary, error) { return h.C.List(ctx) }

// Overview delivers session.updated, session.closed and account.updated.
func (h Hub) Overview() <-chan daemon.Frame { return h.C.Overview() }
```

- [ ] **Step 9: Run the client tests**

Run: `go test -race ./internal/daemon/...`
Expected: PASS.

- [ ] **Step 10: Commit (both repos)**

```bash
git add internal/daemon docs/daemon.md
git commit -m "Report each session's repository and worktree, and add the TUI's daemon hub"
cd ../claudewitz-ios && git add docs/protocol.md && git commit -m "Sync protocol.md: repo and worktree in SessionSummary" && cd -
```

---

### Task 4: Overview state, row order and folder labels

**Files:**
- Create: `internal/tui/daemon.go`
- Modify: `internal/tui/model.go` (`Model` fields, `Init`, `update` cases)
- Modify: `internal/tui/icons.go` (session glyphs in both icon sets)
- Test: `internal/tui/daemon_test.go`

**Interfaces:**
- Consumes: `daemon.SessionSummary` incl. `Repo`, `Worktree` (Task 3); `Model.sessionView.id` (Task 1).
- Produces:
  - `type Daemon interface { Start(ctx context.Context, opts claude.Options) (claude.Session, string, error); Attach(ctx context.Context, id string) (claude.Session, error); List(ctx context.Context) ([]daemon.SessionSummary, error); Overview() <-chan daemon.Frame }`
  - `func (m Model) WithDaemon(d Daemon) Model`
  - Model fields: `daemon Daemon`, `live []daemon.SessionSummary` (start order), `done map[string]bool`, `since map[string]time.Time`, `lastState map[string]rowState`
  - `type rowState int` with `stWaiting, stError, stWorking, stDone, stIdle`
  - `func stateOf(s daemon.SessionSummary, done bool) rowState`
  - `func orderRows(list []daemon.SessionSummary, done map[string]bool) []daemon.SessionSummary`
  - `func folderLabels(list []daemon.SessionSummary) map[string]string`
  - `func ageLabel(d time.Duration) string`
  - `func (m Model) rows() []daemon.SessionSummary` (ordered `m.live`)
  - `func (m *Model) viewByID(id string) *sessionView`
  - `func (m Model) needYou() int`
  - messages `overviewListMsg{list []daemon.SessionSummary; err error}`, `overviewMsg daemon.Frame`; cmds `listCmd(d Daemon) tea.Cmd`, `waitOverview(ch <-chan daemon.Frame) tea.Cmd`
  - `func (m *Model) noteState(s daemon.SessionSummary) (nowNeedsYou bool)`
  - `func (m *Model) applyOverview(f daemon.Frame) tea.Cmd` (returns the needs-you toast)
  - test helpers in `fakes_test.go`: `newFakeDaemon(list ...daemon.SessionSummary) *fakeDaemon` (fields `list`, `overview`, `attached []string`, `started []claude.Options`, `procs map[string]*fakeSession`) and `run(t, m Model, cmd tea.Cmd) Model`
  - icon fields `sessWaiting, sessError, sessWorking, sessDone, sessIdle, worktree string`

- [ ] **Step 0: Add a fake Daemon and a command runner (test-only)**

Append to `internal/tui/fakes_test.go`:

```go
type fakeDaemon struct {
	mu       sync.Mutex
	list     []daemon.SessionSummary
	overview chan daemon.Frame
	attached []string
	started  []claude.Options
	procs    map[string]*fakeSession
}

func newFakeDaemon(list ...daemon.SessionSummary) *fakeDaemon {
	return &fakeDaemon{list: list, overview: make(chan daemon.Frame, 16), procs: map[string]*fakeSession{}}
}

func (d *fakeDaemon) Start(_ context.Context, opts claude.Options) (claude.Session, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.started = append(d.started, opts)
	id := fmt.Sprintf("s_new%d", len(d.started))
	p := newFakeSession()
	d.procs[id] = p
	return p, id, nil
}

func (d *fakeDaemon) Attach(_ context.Context, id string) (claude.Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attached = append(d.attached, id)
	p := newFakeSession()
	d.procs[id] = p
	return p, nil
}

func (d *fakeDaemon) List(context.Context) ([]daemon.SessionSummary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.list), nil
}

func (d *fakeDaemon) Overview() <-chan daemon.Frame { return d.overview }

// run feeds cmd's messages (and those they lead to) through m, without
// blocking on commands that wait for events.
func run(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 50; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(50 * time.Millisecond):
			continue // waits on events or ticks; not part of this step
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if msg == nil {
			continue
		}
		next, c2 := m.Update(msg)
		m = next.(Model)
		queue = append(queue, c2)
	}
	return m
}
```

Add imports `context`, `fmt`, `slices`, `testing`, `time`, `tea "github.com/charmbracelet/bubbletea"`, `github.com/exmanubis/claudewitz/internal/daemon` to `fakes_test.go`.

- [ ] **Step 1: Write the failing tests**

`internal/tui/daemon_test.go`:

```go
package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

func ptr[T any](v T) *T { return &v }

func sum(id, cwd, state string) daemon.SessionSummary {
	return daemon.SessionSummary{ID: id, CWD: cwd, State: state, Title: "t " + id}
}

func TestOrderRows(t *testing.T) {
	list := []daemon.SessionSummary{
		sum("idle", "/a", daemon.StateIdle),
		sum("work1", "/b", daemon.StateWorking),
		sum("wait", "/c", daemon.StateWaiting),
		sum("done", "/d", daemon.StateIdle),
		sum("start", "/e", daemon.StateStarting),
		{ID: "err", CWD: "/f", State: daemon.StateExited, LastError: ptr("auth expired")},
	}
	got := orderRows(list, map[string]bool{"done": true})
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	want := []string{"wait", "err", "work1", "start", "done", "idle"}
	if len(ids) != len(want) {
		t.Fatalf("order %v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order %v, want %v", ids, want)
		}
	}
}

func TestFolderLabels(t *testing.T) {
	list := []daemon.SessionSummary{
		sum("1", "/Users/x/code/api", daemon.StateIdle),
		sum("2", "/Users/x/work/api", daemon.StateIdle),
		sum("3", "/Users/x/code/claudewitz", daemon.StateIdle),
		{ID: "4", CWD: "/Users/x/code/claudewitz/.claude/worktrees/ring-tests", State: daemon.StateIdle,
			Repo: ptr("/Users/x/code/claudewitz"), Worktree: ptr("ring-tests")},
		sum("5", "/Users/x/code/claudewitz", daemon.StateIdle),
	}
	got := folderLabels(list)
	want := map[string]string{
		"1": "code/api", "2": "work/api", "3": "claudewitz",
		"4": "claudewitz " + icons.worktree + " ring-tests", "5": "claudewitz",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("label %s = %q, want %q", id, got[id], w)
		}
	}
}

func TestAgeLabel(t *testing.T) {
	for d, want := range map[time.Duration]string{40 * time.Second: "40s", 2 * time.Minute: "2m", 90 * time.Minute: "1h30m"} {
		if got := ageLabel(d); got != want {
			t.Errorf("ageLabel(%s) = %q, want %q", d, got, want)
		}
	}
}

func frame(event string, params any) daemon.Frame {
	b, _ := json.Marshal(params)
	return daemon.Frame{Event: event, Params: b}
}

// A session that starts waiting while another has focus raises a toast and
// counts as needing you; a turn that ends in the background marks it done.
func TestApplyOverview(t *testing.T) {
	m := readyModel(t)
	m.sessionView.id = "s_1"
	m.live = []daemon.SessionSummary{sum("s_1", "/a", daemon.StateIdle), sum("s_2", "/b", daemon.StateWorking)}
	for _, s := range m.live {
		m.noteState(s)
	}

	w := sum("s_2", "/b", daemon.StateWaiting)
	w.Waiting = &daemon.Waiting{RequestID: "r", Tool: "Bash", Since: time.Now()}
	if cmd := m.applyOverview(frame(daemon.EventSessionUpdated, daemon.SessionUpdatedParams{Summary: w})); cmd == nil {
		t.Fatal("no needs-you toast")
	}
	if m.needYou() != 1 {
		t.Fatalf("needYou %d", m.needYou())
	}

	d := sum("s_2", "/b", daemon.StateIdle)
	d.LastTurnEndedAt = ptr(time.Now())
	m.applyOverview(frame(daemon.EventSessionUpdated, daemon.SessionUpdatedParams{Summary: d}))
	if !m.done["s_2"] || m.needYou() != 0 {
		t.Fatalf("done %v needYou %d", m.done, m.needYou())
	}

	bg := newSessionView(m.base)
	bg.id = "s_2"
	m.views = append(m.views, bg)
	m.applyOverview(frame(daemon.EventSessionClosed, daemon.SessionClosedParams{Session: "s_2"}))
	if len(m.live) != 1 || m.viewByID("s_2") != nil {
		t.Fatalf("closed session kept: %+v, view %v", m.live, m.viewByID("s_2"))
	}
	m.applyOverview(frame(daemon.EventSessionClosed, daemon.SessionClosedParams{Session: "s_1", LastError: ptr("boom")}))
	if len(m.live) != 1 || m.live[0].State != daemon.StateExited || stateOf(m.live[0], false) != stError {
		t.Fatalf("errored session: %+v", m.live)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/tui -run 'TestOrderRows|TestFolderLabels|TestAgeLabel|TestApplyOverview'`
Expected: FAIL to compile (`undefined: orderRows`, …).

- [ ] **Step 3: Add the glyphs to `icons.go`**

Add a line to the `iconSet` struct:

```go
	sessWaiting, sessError, sessWorking, sessDone, sessIdle, worktree string
```

and the same values to both `nerdIcons` and `unicodeIcons` (they're plain Unicode, safe in both):

```go
	sessWaiting: "◌",
	sessError:   "✗",
	sessWorking: "▶",
	sessDone:    "✓",
	sessIdle:    "○",
	worktree:    "⎇",
```

- [ ] **Step 4: Create `internal/tui/daemon.go`**

```go
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// Daemon is what the TUI needs from the claudewitz daemon to show and drive
// every live session (client.Hub). Without the daemon it is nil.
type Daemon interface {
	Start(ctx context.Context, opts claude.Options) (claude.Session, string, error)
	Attach(ctx context.Context, id string) (claude.Session, error)
	List(ctx context.Context) ([]daemon.SessionSummary, error)
	Overview() <-chan daemon.Frame
}

// WithDaemon runs sessions in the daemon and shows all of its sessions.
func (m Model) WithDaemon(d Daemon) Model {
	m.daemon = d
	m.done = map[string]bool{}
	m.since = map[string]time.Time{}
	m.lastState = map[string]rowState{}
	return m
}

// rowState is how a session shows in the panel, most urgent first.
type rowState int

const (
	stWaiting rowState = iota
	stError
	stWorking
	stDone
	stIdle
)

func stateOf(s daemon.SessionSummary, done bool) rowState {
	switch {
	case s.State == daemon.StateWaiting:
		return stWaiting
	case s.State == daemon.StateExited:
		return stError
	case s.State == daemon.StateWorking || s.State == daemon.StateStarting:
		return stWorking
	case done:
		return stDone
	}
	return stIdle
}

// orderRows sorts sessions most urgent first, keeping start order within a
// state so rows don't jump while sessions stream.
func orderRows(list []daemon.SessionSummary, done map[string]bool) []daemon.SessionSummary {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b daemon.SessionSummary) int {
		return int(stateOf(a, done[a.ID])) - int(stateOf(b, done[b.ID]))
	})
	return out
}

// folderLabels names each session's folder: the base name, with the parent
// when two different folders share it, and "repo ⎇ worktree" for linked
// worktrees.
func folderLabels(list []daemon.SessionSummary) map[string]string {
	base := map[string]map[string]bool{} // base name -> distinct folders
	for _, s := range list {
		if s.Worktree != nil {
			continue
		}
		b := filepath.Base(s.CWD)
		if base[b] == nil {
			base[b] = map[string]bool{}
		}
		base[b][s.CWD] = true
	}
	out := map[string]string{}
	for _, s := range list {
		switch {
		case s.Worktree != nil && s.Repo != nil:
			out[s.ID] = filepath.Base(*s.Repo) + " " + icons.worktree + " " + *s.Worktree
		case len(base[filepath.Base(s.CWD)]) > 1:
			out[s.ID] = filepath.Join(filepath.Base(filepath.Dir(s.CWD)), filepath.Base(s.CWD))
		default:
			out[s.ID] = filepath.Base(s.CWD)
		}
	}
	return out
}

// ageLabel is a short time-in-state: 40s, 2m, 1h30m.
func ageLabel(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return shortDur(d)
}

// viewByID is the view showing the session with daemon id id, if any.
func (m *Model) viewByID(id string) *sessionView {
	for _, v := range m.views {
		if v.id == id {
			return v
		}
	}
	return nil
}

// rows is every session for the panel, most urgent first.
func (m Model) rows() []daemon.SessionSummary { return orderRows(m.live, m.done) }

// needYou counts sessions waiting on you or stopped by an error.
func (m Model) needYou() int {
	n := 0
	for _, s := range m.live {
		if st := stateOf(s, m.done[s.ID]); st == stWaiting || st == stError {
			n++
		}
	}
	return n
}

type overviewListMsg struct {
	list []daemon.SessionSummary
	err  error
}

type overviewMsg daemon.Frame

func listCmd(d Daemon) tea.Cmd {
	return func() tea.Msg {
		list, err := d.List(context.Background())
		return overviewListMsg{list, err}
	}
}

func waitOverview(ch <-chan daemon.Frame) tea.Cmd {
	return func() tea.Msg {
		f, ok := <-ch
		if !ok {
			return nil
		}
		return overviewMsg(f)
	}
}

// noteState records when a session entered its current state, for the panel's
// time column, and whether it just started needing you.
func (m *Model) noteState(s daemon.SessionSummary) (nowNeedsYou bool) {
	st := stateOf(s, m.done[s.ID])
	prev, seen := m.lastState[s.ID]
	if !seen || prev != st {
		at := time.Now()
		switch {
		case st == stWaiting && s.Waiting != nil:
			at = s.Waiting.Since
		case !seen && s.LastTurnEndedAt != nil && (st == stIdle || st == stDone):
			at = *s.LastTurnEndedAt
		}
		m.since[s.ID] = at
		m.lastState[s.ID] = st
	}
	return seen && prev != stWaiting && st == stWaiting
}

// applyOverview keeps m.live current from a daemon push. It returns a toast
// when a session in the background starts needing you.
func (m *Model) applyOverview(f daemon.Frame) tea.Cmd {
	switch f.Event {
	case daemon.EventSessionUpdated:
		var p daemon.SessionUpdatedParams
		if json.Unmarshal(f.Params, &p) != nil {
			return nil
		}
		s := p.Summary
		i := slices.IndexFunc(m.live, func(x daemon.SessionSummary) bool { return x.ID == s.ID })
		if i < 0 {
			m.live = append(m.live, s)
		} else {
			prev := m.live[i]
			if s.ID != m.sessionView.id && s.LastTurnEndedAt != nil &&
				(prev.LastTurnEndedAt == nil || !s.LastTurnEndedAt.Equal(*prev.LastTurnEndedAt)) {
				m.done[s.ID] = true
			}
			if s.State == daemon.StateWaiting || s.State == daemon.StateWorking {
				delete(m.done, s.ID)
			}
			m.live[i] = s
		}
		if m.noteState(s) && s.ID != m.sessionView.id {
			return m.notify(toastWarn, folderLabels(m.live)[s.ID]+" needs you", "ctrl+] to switch")
		}
	case daemon.EventSessionClosed:
		var p daemon.SessionClosedParams
		if json.Unmarshal(f.Params, &p) != nil {
			return nil
		}
		i := slices.IndexFunc(m.live, func(x daemon.SessionSummary) bool { return x.ID == p.Session })
		if i < 0 {
			return nil
		}
		if p.LastError != nil {
			m.live[i].State = daemon.StateExited
			m.live[i].LastError = p.LastError
			m.noteState(m.live[i])
			return nil
		}
		m.live = append(m.live[:i:i], m.live[i+1:]...)
		if v := m.viewByID(p.Session); v != nil && v != m.sessionView {
			m.removeView(v) // its process has ended; nothing left to show
		}
		delete(m.done, p.Session)
		delete(m.since, p.Session)
		delete(m.lastState, p.Session)
	}
	return nil
}
```

- [ ] **Step 5: Wire the overview into `Model`**

Add to the `Model` struct (shell section):

```go
	daemon    Daemon // nil without the claudewitz daemon
	live      []daemon.SessionSummary // every live session, in start order
	done      map[string]bool         // turn ended while not focused
	since     map[string]time.Time    // when each session entered its state
	lastState map[string]rowState
```

(import `github.com/exmanubis/claudewitz/internal/daemon` in `model.go`.)

In `Init`, after building `cmds`:

```go
	if m.daemon != nil {
		cmds = append(cmds, listCmd(m.daemon), waitOverview(m.daemon.Overview()))
	}
```

In `update`, add:

```go
	case overviewListMsg:
		if msg.err == nil {
			m.live = msg.list
			for _, s := range m.live {
				m.noteState(s)
			}
		}

	case overviewMsg:
		cmds = append(cmds, m.applyOverview(daemon.Frame(msg)), waitOverview(m.daemon.Overview()))
```

- [ ] **Step 6: Run the TUI tests**

Run: `go vet ./internal/tui && go test -race ./internal/tui`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tui
git commit -m "Keep the daemon's live sessions in the TUI, ordered by what needs you"
```

---

### Task 5: The sessions panel, the tab strip and layout

**Files:**
- Create: `internal/tui/sessionspanel.go`
- Modify: `internal/tui/model.go` (`Model` fields `termW`, `termH`, `panelOn`; `WindowSizeMsg` case; `View` split into `View` + `viewMain`; `MouseMsg` case start)
- Test: `internal/tui/sessionspanel_test.go`

**Interfaces:**
- Consumes: `m.rows()`, `folderLabels`, `stateOf`, `ageLabel`, `m.needYou()`, `m.since`, `m.done` (Task 4).
- Produces: `const panelWidth = 40`; `const panelMinTerm = 140`; `type sessionsMode int` with `sessionsNone, sessionsPanel, sessionsTabs`; `func (m Model) sessionsMode() sessionsMode`; `func (m *Model) resize()`; `func (m Model) sessionsView(height int) string`; `func (m Model) tabsView(width int) string`; `func (m *Model) sessionsMouse(msg *tea.MouseMsg) (id string, handled bool)` (id of a clicked row or tab); Model fields `termW, termH int`, `panelOn bool` (true by default in `WithDaemon`), `panelMode sessionsMode` (last applied).

- [ ] **Step 1: Write the failing tests**

`internal/tui/sessionspanel_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

func withSessions(t *testing.T, width int, list ...daemon.SessionSummary) Model {
	t.Helper()
	m := readyModel(t).WithDaemon(newFakeDaemon())
	m.live = list
	for _, s := range list {
		m.noteState(s)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return next.(Model)
}

func TestSessionsModeByWidth(t *testing.T) {
	two := []daemon.SessionSummary{sum("a", "/x/one", daemon.StateIdle), sum("b", "/x/two", daemon.StateWaiting)}
	m := withSessions(t, 160, two...)
	if m.sessionsMode() != sessionsPanel || m.width != 160-panelWidth || m.height != 40 {
		t.Fatalf("wide: mode %d width %d height %d", m.sessionsMode(), m.width, m.height)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	if m.sessionsMode() != sessionsTabs || m.width != 120 || m.height != 39 {
		t.Fatalf("narrow: mode %d width %d height %d", m.sessionsMode(), m.width, m.height)
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 170, Height: 40})
	m = next.(Model)
	if m.sessionsMode() != sessionsPanel || m.width != 170-panelWidth {
		t.Fatalf("wide again: mode %d width %d", m.sessionsMode(), m.width)
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if w := ansi.StringWidth(line); w > 170 {
			t.Fatalf("line %d wide: %q", w, line)
		}
	}
}

func TestNoPanelWithOneSessionOrWithoutDaemon(t *testing.T) {
	m := withSessions(t, 160, sum("a", "/x/one", daemon.StateIdle))
	if m.sessionsMode() != sessionsNone || m.width != 160 {
		t.Fatalf("one session: mode %d width %d", m.sessionsMode(), m.width)
	}
	plain := readyModel(t)
	if plain.sessionsMode() != sessionsNone {
		t.Fatal("panel without the daemon")
	}
}

func TestPanelShowsRowsInOrder(t *testing.T) {
	m := withSessions(t, 160,
		sum("a", "/x/idle-one", daemon.StateIdle),
		sum("b", "/x/waiting-one", daemon.StateWaiting),
	)
	view := ansi.Strip(m.sessionsView(40))
	wi, ii := strings.Index(view, "waiting-one"), strings.Index(view, "idle-one")
	if wi < 0 || ii < 0 || wi > ii {
		t.Fatalf("panel order:\n%s", view)
	}
	if !strings.Contains(view, "SESSIONS") || !strings.Contains(view, "1 need you") {
		t.Fatalf("panel header:\n%s", view)
	}
	if !strings.Contains(view, icons.sessWaiting+" t b") {
		t.Fatalf("waiting row:\n%s", view)
	}
}

func TestTabsLabelSharedFolders(t *testing.T) {
	a, b := sum("a", "/x/figment", daemon.StateIdle), sum("b", "/x/figment", daemon.StateIdle)
	a.Title, b.Title = "fonts", "index"
	m := withSessions(t, 120, a, b)
	tabs := ansi.Strip(m.tabsView(120))
	if !strings.Contains(tabs, "figment · fonts") || !strings.Contains(tabs, "figment · index") {
		t.Fatalf("tabs: %q", tabs)
	}
}

func TestPanelClickPicksRow(t *testing.T) {
	m := withSessions(t, 160, sum("a", "/x/one", daemon.StateIdle), sum("b", "/x/two", daemon.StateWaiting))
	// Row 0 is b (waiting first); rows start below the header line, two lines each.
	msg := tea.MouseMsg{X: 5, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	id, handled := m.sessionsMouse(&msg)
	if !handled || id != "b" {
		t.Fatalf("click: %q %v", id, handled)
	}
	chat := tea.MouseMsg{X: panelWidth + 10, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	if _, handled := m.sessionsMouse(&chat); handled || chat.X != 10 {
		t.Fatalf("chat click shifted to %d, handled %v", chat.X, handled)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/tui -run 'TestSessionsMode|TestNoPanel|TestPanelShows|TestTabsLabel|TestPanelClick'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `internal/tui/sessionspanel.go`**

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

const (
	panelWidth   = 40  // the sessions panel, border included
	panelMinTerm = 140 // narrower terminals get the tab strip
	panelTop     = 2   // rows above the first session row: header, blank
)

type sessionsMode int

const (
	sessionsNone sessionsMode = iota
	sessionsPanel
	sessionsTabs
)

// sessionsMode is how the daemon's sessions are shown: a panel, a tab strip
// when narrow, or not at all with fewer than two or without the daemon.
func (m Model) sessionsMode() sessionsMode {
	if m.daemon == nil || !m.panelOn || len(m.live) < 2 {
		return sessionsNone
	}
	if m.termW >= panelMinTerm {
		return sessionsPanel
	}
	return sessionsTabs
}

// resize gives today's layout what the panel or strip leaves of the
// terminal.
func (m *Model) resize() {
	m.width, m.height = m.termW, m.termH
	m.panelMode = m.sessionsMode()
	switch m.panelMode {
	case sessionsPanel:
		m.width -= panelWidth
	case sessionsTabs:
		m.height--
	}
	if m.width >= sidebarMinWidth {
		m.drawer = false
	}
	m.layout()
	m.refresh()
}

func (m Model) rowGlyph(st rowState) (string, lipgloss.Style) {
	th := currentTheme()
	switch st {
	case stWaiting:
		return icons.sessWaiting, lipgloss.NewStyle().Foreground(th.warn).Bold(true)
	case stError:
		return icons.sessError, lipgloss.NewStyle().Foreground(th.err)
	case stWorking:
		return icons.sessWorking, lipgloss.NewStyle().Foreground(th.accent)
	case stDone:
		return icons.sessDone, lipgloss.NewStyle().Foreground(th.ok)
	}
	return icons.sessIdle, mutedStyle
}

func rowTitle(s daemon.SessionSummary) string {
	if t := firstLine(s.Title); t != "" {
		return t
	}
	return "new session"
}

// sessionsView draws the sessions panel: a header, then two lines per
// session, most urgent first.
func (m Model) sessionsView(height int) string {
	th := currentTheme()
	inner := panelWidth - 2 // the right border and a gutter
	head := lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render("SESSIONS")
	count := mutedStyle.Render(fmt.Sprint(len(m.live)))
	if n := m.needYou(); n > 0 {
		count = warnStyle.Render(fmt.Sprintf("▲ %d need you", n))
	}
	lines := []string{spread(head, count, inner), ""}
	labels := folderLabels(m.live)
	for _, s := range m.rows() {
		st := stateOf(s, m.done[s.ID])
		glyph, gs := m.rowGlyph(st)
		focused := s.ID == m.sessionView.id
		titleStyle := textStyle
		switch st {
		case stWaiting:
			titleStyle = warnStyle.Bold(true)
		case stError:
			titleStyle = errStyle
		case stIdle:
			titleStyle = mutedStyle
		}
		if focused {
			titleStyle = titleStyle.Bold(true)
		}
		barColor := th.subtle
		switch {
		case focused:
			barColor = th.accent
		case st == stWaiting:
			barColor = th.warn
		case st == stError:
			barColor = th.err
		}
		bar := lipgloss.NewStyle().Foreground(barColor).Render("▎")
		age := mutedStyle.Render(ageLabel(time.Since(m.since[s.ID])))
		title := truncate(rowTitle(s), inner-6-lipgloss.Width(age))
		l1 := spread(bar+" "+gs.Render(glyph)+" "+titleStyle.Render(title), age, inner)
		l2 := bar + "   " + lipgloss.NewStyle().Foreground(th.second).Render(truncate(labels[s.ID], inner-4))
		if focused {
			bg := lipgloss.NewStyle().Background(th.selBg).Width(inner)
			l1, l2 = bg.Render(l1), bg.Render(l2)
		}
		lines = append(lines, l1, l2)
	}
	lines = append(lines, "", mutedStyle.Render("ctrl+] next waiting · ctrl+q kill"))
	for len(lines) < height {
		lines = append(lines, "")
	}
	body := strings.Join(lines[:min(len(lines), height)], "\n")
	return lipgloss.NewStyle().Width(panelWidth-1).Height(height).
		Border(lipgloss.NormalBorder(), false, true, false, false).BorderForeground(th.subtle).
		Render(body)
}

// spread puts left and right at the two ends of a width-wide line.
func spread(left, right string, width int) string {
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

// tabLabels are the tab strip's labels: the folder, with a short title when
// two tabs share it.
func (m Model) tabLabels(rows []daemon.SessionSummary) []string {
	labels := folderLabels(m.live)
	count := map[string]int{}
	for _, s := range rows {
		count[labels[s.ID]]++
	}
	out := make([]string, len(rows))
	for i, s := range rows {
		l := labels[s.ID]
		if count[l] > 1 {
			l += " · " + truncate(rowTitle(s), 14)
		}
		out[i] = l
	}
	return out
}

// tabsView draws the one-line tab strip, scrolled to keep the focused tab
// in view.
func (m Model) tabsView(width int) string {
	th := currentTheme()
	rows := m.rows()
	labels := m.tabLabels(rows)
	var tabs []string
	focusedAt := 0
	for i, s := range rows {
		st := stateOf(s, m.done[s.ID])
		glyph, gs := m.rowGlyph(st)
		style := mutedStyle
		switch st {
		case stWaiting:
			style = warnStyle.Bold(true)
		case stError:
			style = errStyle
		}
		tab := " " + gs.Render(glyph) + " " + style.Render(labels[i]) + " "
		if s.ID == m.sessionView.id {
			tab = lipgloss.NewStyle().Background(th.selBg).Underline(true).Render(" " + glyph + " " + labels[i] + " ")
			focusedAt = i
		}
		tabs = append(tabs, tab)
	}
	start := 0
	for start < focusedAt && lipgloss.Width(strings.Join(tabs[start:focusedAt+1], "")) > width {
		start++
	}
	line := strings.Join(tabs[start:], "")
	return ansi.Truncate(line, width, "…")
}

// sessionsMouse handles clicks on the panel or strip and shifts other mouse
// events into the coordinates today's layout expects.
func (m *Model) sessionsMouse(msg *tea.MouseMsg) (id string, handled bool) {
	click := msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft
	switch m.panelMode {
	case sessionsPanel:
		if msg.X < panelWidth {
			if !click {
				return "", true
			}
			row := (msg.Y - panelTop) / 2
			if rows := m.rows(); msg.Y >= panelTop && row < len(rows) {
				return rows[row].ID, true
			}
			return "", true
		}
		msg.X -= panelWidth
	case sessionsTabs:
		if msg.Y == 0 {
			if !click {
				return "", true
			}
			rows := m.rows()
			x := 0
			for i, l := range m.tabLabels(rows) {
				w := lipgloss.Width(" " + icons.sessIdle + " " + l + " ")
				if msg.X < x+w {
					return rows[i].ID, true
				}
				x += w
			}
			return "", true
		}
		msg.Y--
	}
	return "", false
}
```

(`truncate` and `firstLine` already exist in `sidebar.go`. The tab hit-test assumes no scrolling; when the strip is scrolled, a click picks by position from `start`, which is accepted as approximate for now.)

- [ ] **Step 4: Wire layout, view and mouse into `model.go`**

Add fields to `Model`: `termW, termH int` (the terminal; `width`/`height` are what the chat gets), `panelOn bool`, `panelMode sessionsMode`. In `WithDaemon`, set `m.panelOn = true`.

Replace the `tea.WindowSizeMsg` case:

```go
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
		m.ready = true
		m.resize()
```

Rename the current `View` to `viewMain` and drop its final `safetext.Screen` call (return `screen`). Add:

```go
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
```

At the start of the `tea.MouseMsg` case:

```go
	case tea.MouseMsg:
		if id, handled := m.sessionsMouse(&msg); handled {
			if id != "" {
				return m, m.focusSession(id)
			}
			return m, nil
		}
```

`focusSession` arrives in Task 6. Until then add this stub at the bottom of `sessionspanel.go` so the build passes, and delete it in Task 6:

```go
func (m *Model) focusSession(id string) tea.Cmd { return nil }
```

After `applyOverview` and `overviewListMsg` handling (Task 4 cases), re-lay out when the mode changes:

```go
		if m.ready && m.sessionsMode() != m.panelMode {
			m.resize()
		}
```

- [ ] **Step 5: Run the TUI tests**

Run: `go vet ./internal/tui && go test -race ./internal/tui`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui
git commit -m "Show the daemon's sessions in a panel, or a tab strip when narrow"
```

---

### Task 6: Focusing sessions, switching keys and launch

**Files:**
- Create: `internal/tui/switch.go`
- Modify: `internal/tui/sessionspanel.go` (delete the `focusSession` stub), `internal/tui/model.go` (`Init`, the start path, key handling before permissions, the `startCmd` for daemon mode), `main.go`
- Test: `internal/tui/switch_test.go`

**Interfaces:**
- Consumes: `Daemon` (Task 4), `focus`, `startedMsg`, `loadTranscript(v, cwd, id)` (Task 2), `m.rows()`, `m.live` (Task 4).
- Produces: `func (m *Model) focusSession(id string) tea.Cmd`; `func (m *Model) summary(id string) (daemon.SessionSummary, bool)`; `func (m *Model) nextNeedsYou() string`; `func (m *Model) handleSessionKey(k tea.KeyMsg) (tea.Cmd, bool)`; `func attachCmd(d Daemon, v *sessionView, id string) tea.Cmd` (returns `startedMsg`); `launchListMsg{list []daemon.SessionSummary; err error}`; `func pickLaunch(list []daemon.SessionSummary, cwd string) string`.

- [ ] **Step 1: Use the fakes**

The fake daemon and the `run` helper were added to `fakes_test.go` in Task 4; nothing to add here.

- [ ] **Step 2: Write the failing tests**

`internal/tui/switch_test.go`:

```go
package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

func daemonModel(t *testing.T, d *fakeDaemon) Model {
	t.Helper()
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{}, nil).WithDaemon(d)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = next.(Model)
	m.live, _ = d.List(context.Background())
	for _, s := range m.live {
		m.noteState(s)
	}
	m.sessionView.id = m.live[0].ID
	m.resize()
	return m
}

// Focusing a session for the first time attaches to it; focusing it again
// reuses the view, draft included.
func TestFocusSessionAttachesOnce(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	first := m.sessionView

	m = run(t, m, m.focusSession("s_2"))
	if m.sessionView.id != "s_2" || len(d.attached) != 1 || m.proc == nil {
		t.Fatalf("focused %q attached %v proc %v", m.sessionView.id, d.attached, m.proc)
	}
	m.input.SetValue("draft for b")
	m = run(t, m, m.focusSession("s_1"))
	if m.sessionView != first {
		t.Fatal("didn't go back to the first view")
	}
	m = run(t, m, m.focusSession("s_2"))
	if len(d.attached) != 1 || m.input.Value() != "draft for b" {
		t.Fatalf("attached again %v, draft %q", d.attached, m.input.Value())
	}
}

func TestNextNeedsYouPicksOldestWaitingThenErrors(t *testing.T) {
	old, young := sum("s_2", "/x/b", daemon.StateWaiting), sum("s_3", "/x/c", daemon.StateWaiting)
	old.Waiting = &daemon.Waiting{Since: time.Now().Add(-time.Minute)}
	young.Waiting = &daemon.Waiting{Since: time.Now()}
	errd := daemon.SessionSummary{ID: "s_4", CWD: "/x/d", State: daemon.StateExited, LastError: ptr("x")}
	m := daemonModel(t, newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), young, old, errd))
	if id := m.nextNeedsYou(); id != "s_2" {
		t.Fatalf("next: %q", id)
	}
	m.sessionView.id = "s_2"
	if id := m.nextNeedsYou(); id != "s_3" {
		t.Fatalf("next after focusing s_2: %q", id)
	}
	m.live = m.live[:1]
	m.live = append(m.live, errd)
	if id := m.nextNeedsYou(); id != "s_4" {
		t.Fatalf("next with only an error: %q", id)
	}
}

func TestSwitchKeys(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle), sum("s_3", "/x/c", daemon.StateIdle))
	m := daemonModel(t, d)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	m = run(t, next.(Model), cmd)
	if m.sessionView.id != "s_2" {
		t.Fatalf("alt+down: %q", m.sessionView.id)
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3"), Alt: true})
	m = run(t, next.(Model), cmd)
	if m.sessionView.id != "s_3" {
		t.Fatalf("alt+3: %q", m.sessionView.id)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
	m = next.(Model)
	if m.sessionsMode() != sessionsNone || m.width != 160 {
		t.Fatalf("alt+s didn't hide the panel: mode %d width %d", m.sessionsMode(), m.width)
	}
}

func TestPickLaunch(t *testing.T) {
	a, b := sum("s_1", "/x/here", daemon.StateIdle), sum("s_2", "/x/here", daemon.StateIdle)
	a.LastActivity, b.LastActivity = time.Now().Add(-time.Hour), time.Now()
	if id := pickLaunch([]daemon.SessionSummary{a, b, sum("s_3", "/x/else", daemon.StateIdle)}, "/x/here"); id != "s_2" {
		t.Fatalf("launch picked %q", id)
	}
	if id := pickLaunch([]daemon.SessionSummary{sum("s_3", "/x/else", daemon.StateIdle)}, "/x/here"); id != "" {
		t.Fatalf("launch picked %q in another folder", id)
	}
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test -race ./internal/tui -run 'TestFocusSession|TestNextNeedsYou|TestSwitchKeys|TestPickLaunch'`
Expected: FAIL (compile errors for `nextNeedsYou`, `pickLaunch`; the stub `focusSession` does nothing).

- [ ] **Step 4: Implement `internal/tui/switch.go`** (and delete the stub from `sessionspanel.go`)

```go
package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

func (m *Model) summary(id string) (daemon.SessionSummary, bool) {
	i := slices.IndexFunc(m.live, func(s daemon.SessionSummary) bool { return s.ID == id })
	if i < 0 {
		return daemon.SessionSummary{}, false
	}
	return m.live[i], true
}

// attachCmd follows a live session from now on.
func attachCmd(d Daemon, v *sessionView, id string) tea.Cmd {
	return func() tea.Msg {
		p, err := d.Attach(context.Background(), id)
		return startedMsg{view: v, proc: p, id: id, err: err}
	}
}

// focusSession shows the session with daemon id id, attaching to it the
// first time: its history from the transcript, then its live events.
func (m *Model) focusSession(id string) tea.Cmd {
	if v := m.viewByID(id); v != nil {
		delete(m.done, id)
		return m.focus(v)
	}
	s, ok := m.summary(id)
	if !ok || m.daemon == nil {
		return nil
	}
	opts := m.base
	opts.CWD, opts.Resume, opts.Continue = s.CWD, "", false
	opts.Model, opts.Effort, opts.PermissionMode = s.Model, s.Effort, s.PermissionMode
	v := newSessionView(opts)
	v.id = id
	if s.ClaudeSessionID != nil {
		v.sessionID = *s.ClaudeSessionID
	}
	v.sessionTitle = s.Title
	m.views = append(m.views, v)
	delete(m.done, id)
	cmds := []tea.Cmd{m.focus(v), attachCmd(m.daemon, v, id)}
	if v.sessionID != "" {
		cmds = append(cmds, loadTranscript(v, s.CWD, v.sessionID))
	}
	return tea.Batch(cmds...)
}

// nextNeedsYou is the session ctrl+] goes to: the one waiting longest
// (other than the focused one), else one stopped by an error.
func (m *Model) nextNeedsYou() string {
	var waiting []daemon.SessionSummary
	var errored string
	for _, s := range m.live {
		if s.ID == m.sessionView.id {
			continue
		}
		switch stateOf(s, false) {
		case stWaiting:
			waiting = append(waiting, s)
		case stError:
			if errored == "" {
				errored = s.ID
			}
		}
	}
	if len(waiting) == 0 {
		return errored
	}
	slices.SortStableFunc(waiting, func(a, b daemon.SessionSummary) int {
		if a.Waiting == nil || b.Waiting == nil {
			return 0
		}
		return a.Waiting.Since.Compare(b.Waiting.Since)
	})
	return waiting[0].ID
}

// handleSessionKey runs the sessions keys; ok is false for any other key.
func (m *Model) handleSessionKey(k tea.KeyMsg) (tea.Cmd, bool) {
	if m.daemon == nil {
		return nil, false
	}
	s := k.String()
	rows := m.rows()
	at := slices.IndexFunc(rows, func(r daemon.SessionSummary) bool { return r.ID == m.sessionView.id })
	switch {
	case s == "alt+s":
		m.panelOn = !m.panelOn
		m.resize()
		return nil, true
	case s == "alt+up" || s == "alt+down":
		if len(rows) < 2 {
			return nil, true
		}
		step := 1
		if s == "alt+up" {
			step = -1
		}
		return m.focusSession(rows[(max(at, 0)+step+len(rows))%len(rows)].ID), true
	case strings.HasPrefix(s, "alt+") && len(s) == 5 && s[4] >= '1' && s[4] <= '9':
		n, _ := strconv.Atoi(s[4:])
		if n <= len(rows) {
			return m.focusSession(rows[n-1].ID), true
		}
		return nil, true
	case s == "ctrl+]":
		if id := m.nextNeedsYou(); id != "" {
			return m.focusSession(id), true
		}
		return m.notify(toastInfo, "Nothing needs you", ""), true
	}
	return nil, false
}

// pickLaunch is the live session a launch in cwd focuses: the most
// recently active one there, or "" to start a new one.
func pickLaunch(list []daemon.SessionSummary, cwd string) string {
	best := ""
	var bestAt daemon.SessionSummary
	for _, s := range list {
		if s.CWD != cwd || s.State == daemon.StateExited {
			continue
		}
		if best == "" || s.LastActivity.After(bestAt.LastActivity) {
			best, bestAt = s.ID, s
		}
	}
	return best
}

type launchListMsg struct {
	list []daemon.SessionSummary
	err  error
}

func launchListCmd(d Daemon) tea.Cmd {
	return func() tea.Msg {
		list, err := d.List(context.Background())
		return launchListMsg{list, err}
	}
}

// launch decides what the first view shows: the newest live session in
// this folder, else a new session here.
func (m *Model) launch(msg launchListMsg) tea.Cmd {
	if msg.err == nil {
		m.live = msg.list
		for _, s := range m.live {
			m.noteState(s)
		}
		if id := pickLaunch(msg.list, m.opts.CWD); id != "" {
			s, _ := m.summary(id)
			v := m.sessionView
			v.id = id
			if s.ClaudeSessionID != nil {
				v.sessionID = *s.ClaudeSessionID
			}
			cmds := []tea.Cmd{attachCmd(m.daemon, v, id)}
			if v.sessionID != "" {
				cmds = append(cmds, loadTranscript(v, s.CWD, v.sessionID))
			}
			m.resize()
			return tea.Batch(cmds...)
		}
	}
	m.resize()
	return m.startCmd(m.sessionView, m.opts)
}

// daemonStart starts a session in the daemon for v.
func daemonStart(d Daemon, v *sessionView, opts claude.Options) tea.Cmd {
	return func() tea.Msg {
		p, id, err := d.Start(context.Background(), opts)
		return startedMsg{view: v, proc: p, id: id, err: err}
	}
}
```

- [ ] **Step 5: Wire it into `model.go`**

In `startCmd`, use the daemon when there is one:

```go
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
```

In `Init`, with the daemon and no `-resume`/`-continue`, decide by listing first. Replace the start of `Init` up to the notice handling, and the Task 4 daemon addition, with:

```go
func (m Model) Init() tea.Cmd {
	first := m.startCmd(m.sessionView, m.opts)
	if m.daemon != nil && m.opts.Resume == "" && !m.opts.Continue {
		first = launchListCmd(m.daemon) // lists the sessions, then attaches or starts
	}
	cmds := []tea.Cmd{textarea.Blink, m.spin.Tick, first, indexFiles(m.opts.CWD), m.refreshGit(), gitPoll(), loadHistory(m.opts.CWD)}
	if m.daemon != nil {
		if m.opts.Resume != "" || m.opts.Continue {
			cmds = append(cmds, listCmd(m.daemon))
		}
		cmds = append(cmds, waitOverview(m.daemon.Overview()))
	}
	// … the notice and transcript lines below stay as they are …
```

Add the case:

```go
	case launchListMsg:
		return m, m.launch(msg)
```

In `tea.KeyMsg` handling, right before `if len(m.pending) > 0 {`:

```go
		if cmd, ok := m.handleSessionKey(msg); ok {
			return m, cmd
		}
```

In `main.go`, pass the hub:

```go
		} else {
			defer c.Close()
			start = client.Starter(c)
			hub = client.Hub{C: c}
			notice = strings.TrimSpace(note + "\n" + liveNote(ctx, c))
		}
	}

	model := tui.New(ctx, opts, cfg, start).WithNotice(notice)
	if hub.C != nil {
		model = model.WithDaemon(hub)
	}
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
```

with `var hub client.Hub` declared next to `start`.

- [ ] **Step 6: Run all tests**

Run: `go vet ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 7: Try it**

Run: `go build -o /tmp/cw . && CLAUDEWITZ_SOCKET=/tmp/cw.sock /tmp/cw -daemon` in two folders (two terminals), then in a third terminal run it again. Expected: the third TUI's panel shows both sessions; `alt+↑/↓` switches, the transcript appears, and switching back keeps a typed draft. Quit all three and run `CLAUDEWITZ_SOCKET=/tmp/cw.sock /tmp/cw daemon stop -force`.

- [ ] **Step 8: Commit**

```bash
git add internal/tui main.go
git commit -m "Switch between the daemon's sessions from the TUI"
```

---

### Task 7: Kill session

**Files:**
- Create: `internal/tui/kill.go`
- Modify: `internal/tui/model.go` (`Model.kill *killState`, key routing, `View` overlay), `internal/tui/palette.go`
- Test: `internal/tui/kill_test.go`

**Interfaces:**
- Consumes: `focusSession`, `removeView`, `m.rows()`, `m.summary`, `stateOf`.
- Produces: `type killState struct{ id, title, cwd string; working bool; cursor int }`; `func (m *Model) openKill() tea.Cmd`; `func (m *Model) handleKillKey(k tea.KeyMsg) tea.Cmd`; `func (m Model) killView() string`; `func (m *Model) killFocused() tea.Cmd`; `func (m *Model) dismissFocused() tea.Cmd`; `func (m *Model) emptyView() tea.Cmd`.

- [ ] **Step 1: Write the failing tests**

`internal/tui/kill_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

func ctrlQ() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlQ} }

func TestKillDialogDefaultsToKeep(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateWorking), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	p := newFakeSession()
	m.proc = p
	next, _ := m.Update(ctrlQ())
	m = next.(Model)
	if m.kill == nil {
		t.Fatal("no kill dialog")
	}
	view := ansi.Strip(m.killView())
	for _, want := range []string{"Kill session t s_1?", "It's working right now; the current turn is lost.", "Keep it running", "Kill session  y"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog lacks %q:\n%s", want, view)
		}
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.kill != nil || p.stopped {
		t.Fatalf("enter on the default killed it: dialog %v stopped %v", m.kill, p.stopped)
	}
}

// Killing the focused session moves focus to the next row, and killing the
// last one leaves an empty view.
func TestKillFocusesNext(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	p := newFakeSession()
	m.proc = p
	next, _ := m.Update(ctrlQ())
	next, cmd := next.(Model).Update(key("y"))
	m = run(t, next.(Model), cmd)
	if !p.stopped || m.sessionView.id != "s_2" || m.viewByID("s_1") != nil {
		t.Fatalf("stopped %v focused %q", p.stopped, m.sessionView.id)
	}
	next, _ = m.Update(ctrlQ())
	next, cmd = next.(Model).Update(key("y"))
	m = run(t, next.(Model), cmd)
	if m.sessionView.id != "" || m.proc != nil {
		t.Fatalf("after killing the last: focused %q proc %v", m.sessionView.id, m.proc)
	}
}

func TestCtrlQDismissesAnErroredSession(t *testing.T) {
	errd := daemon.SessionSummary{ID: "s_1", CWD: "/x/a", State: daemon.StateExited, LastError: ptr("boom")}
	m := daemonModel(t, newFakeDaemon(errd, sum("s_2", "/x/b", daemon.StateIdle)))
	next, cmd := m.Update(ctrlQ())
	m = run(t, next.(Model), cmd)
	if m.kill != nil || len(m.live) != 1 || m.sessionView.id != "s_2" {
		t.Fatalf("dismiss: dialog %v live %d focused %q", m.kill, len(m.live), m.sessionView.id)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/tui -run 'TestKill|TestCtrlQ'`
Expected: FAIL to compile (`m.kill undefined`).

- [ ] **Step 3: Implement `internal/tui/kill.go`**

```go
package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

// killState is the Kill session dialog.
type killState struct {
	id, title, cwd string
	working        bool // mid-turn or waiting: the turn is lost
	cursor         int  // 0 keep (default), 1 kill
}

// openKill asks to kill the focused session; an errored one is dismissed
// straight away.
func (m *Model) openKill() tea.Cmd {
	s, ok := m.summary(m.sessionView.id)
	if !ok {
		return nil
	}
	if stateOf(s, false) == stError {
		return m.dismissFocused()
	}
	st := stateOf(s, false)
	m.kill = &killState{id: s.ID, title: rowTitle(s), cwd: s.CWD, working: st == stWorking || st == stWaiting}
	m.input.Blur()
	return nil
}

func (m *Model) handleKillKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "up", "down", "tab", "k", "j":
		m.kill.cursor = 1 - m.kill.cursor
	case "1", "n", "esc", "ctrl+q":
		m.kill = nil
	case "2", "y":
		return m.killFocused()
	case "enter":
		if m.kill.cursor == 1 {
			return m.killFocused()
		}
		m.kill = nil
	}
	if m.kill == nil && !m.chatFocus {
		m.input.Focus()
	}
	return nil
}

// killFocused stops the focused session in the daemon and moves on.
func (m *Model) killFocused() tea.Cmd {
	m.kill = nil
	if m.proc != nil {
		m.proc.Stop()
		m.proc = nil
	}
	return m.leaveFocused()
}

// dismissFocused forgets an errored session's row.
func (m *Model) dismissFocused() tea.Cmd {
	return m.leaveFocused()
}

// leaveFocused drops the focused session from the panel and focuses the
// next row, or an empty view when none is left.
func (m *Model) leaveFocused() tea.Cmd {
	gone := m.sessionView
	rows := m.rows()
	at := slices.IndexFunc(rows, func(r daemon.SessionSummary) bool { return r.ID == gone.id })
	m.live = slices.DeleteFunc(m.live, func(s daemon.SessionSummary) bool { return s.ID == gone.id })
	delete(m.done, gone.id)
	rows = m.rows()
	var cmd tea.Cmd
	if len(rows) > 0 {
		cmd = m.focusSession(rows[min(max(at, 0), len(rows)-1)].ID)
	} else {
		cmd = m.emptyView()
	}
	if m.sessionView != gone {
		m.removeView(gone)
	}
	if m.ready && m.sessionsMode() != m.panelMode {
		m.resize()
	}
	return cmd
}

// emptyView is what's left when no session runs: a view with no process
// in the launch folder.
func (m *Model) emptyView() tea.Cmd {
	opts := m.base
	opts.Resume, opts.Continue = "", false
	v := newSessionView(opts)
	m.views = append(m.views, v)
	cmd := m.focus(v)
	m.chat.info("No sessions running · alt+n starts one")
	m.refresh()
	return cmd
}

func (m Model) killView() string {
	k := m.kill
	th := currentTheme()
	lines := []string{
		errStyle.Bold(true).Render("Kill session ") + textStyle.Bold(true).Render(k.title) + errStyle.Bold(true).Render("?"),
		lipgloss.NewStyle().Foreground(th.second).Render(prettyPath(k.cwd)),
		"",
	}
	if k.working {
		lines = append(lines, warnStyle.Render("▲ It's working right now; the current turn is lost."))
	}
	lines = append(lines, mutedStyle.Render("claude ends; the transcript is kept, ctrl+r resumes it."), "")
	choices := []struct{ label, keys string }{{"Keep it running", "n · esc"}, {"Kill session", "y"}}
	for i, c := range choices {
		pointer := "  "
		style := textStyle
		if i == 1 {
			style = errStyle
		}
		if i == k.cursor {
			pointer = statusKey.Render("❯ ")
			style = style.Bold(true)
		}
		label := style.Render(c.label)
		lines = append(lines, pointer+mutedStyle.Render(fmt.Sprintf("%d. ", i+1))+label+mutedStyle.Render("  "+c.keys))
	}
	return popupBox.BorderForeground(th.err).Width(64).Render(strings.Join(lines, "\n"))
}
```

(`popupBox` and `statusKey` already exist in the TUI.)

- [ ] **Step 4: Wire it into `model.go` and the palette**

Add `kill *killState` to `Model`. In `tea.KeyMsg` handling, right after the `if m.showHelp { … }` block:

```go
		if m.kill != nil {
			return m, m.handleKillKey(msg)
		}
```

In `handleSessionKey` (switch.go), add:

```go
	case s == "ctrl+q":
		return m.openKill(), true
```

In `View` (Task 5), after the panel join:

```go
	if m.kill != nil {
		screen = center(screen, m.killView(), m.termW, m.termH-1)
	}
```

In `palette.go` `actions`, after "Resume session…", add when `m.daemon != nil`:

```go
	if m.daemon != nil {
		add("Sessions", "Next session that needs you", "", "ctrl+]", func(m *Model) tea.Cmd {
			cmd, _ := m.handleSessionKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
			return cmd
		})
		add("Sessions", "Kill session", "end the focused session", "ctrl+q", func(m *Model) tea.Cmd { return m.openKill() })
		labels := folderLabels(m.live)
		for i, r := range m.rows() {
			id, keyHint := r.ID, ""
			if i < 9 {
				keyHint = fmt.Sprintf("alt+%d", i+1)
			}
			add("Sessions", "Switch to "+labels[id], rowTitle(r), keyHint, func(m *Model) tea.Cmd { return m.focusSession(id) })
		}
		add("Sessions", "Toggle sessions panel", "", "alt+s", func(m *Model) tea.Cmd {
			m.panelOn = !m.panelOn
			m.resize()
			return nil
		})
	}
```

- [ ] **Step 5: Run all tests**

Run: `go vet ./internal/tui && go test -race ./internal/tui`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui
git commit -m "Kill a session from the TUI with ctrl+q, behind a confirmation"
```

---

### Task 8: New session in any folder

**Files:**
- Create: `internal/tui/folderpicker.go`
- Modify: `internal/tui/model.go` (`Model.folders *folderPicker`, key routing, `View` overlay, `foldersLoadedMsg` case), `internal/tui/switch.go` (`alt+n`), `internal/tui/palette.go`
- Test: `internal/tui/folderpicker_test.go`

**Interfaces:**
- Consumes: `newSessionView`, `focus`, `startCmd` (daemon path), `m.live`, `sessions.ListAll`.
- Produces: `type folderPicker struct{ filter textinput.Model; all, shown []string; sel int }`; `func newFolderPicker(live []string) *folderPicker`; `type foldersLoadedMsg []string`; `func loadFolders() tea.Cmd`; `func (p *folderPicker) setRecent(dirs []string)`; `func (p *folderPicker) choice() string`; `func completeDir(input string) string`; `func expandHome(p string) string`; `func (m *Model) openFolders() tea.Cmd`; `func (m *Model) handleFoldersKey(k tea.KeyMsg) tea.Cmd`; `func (m *Model) newSessionIn(dir string) tea.Cmd`.

- [ ] **Step 1: Write the failing tests**

`internal/tui/folderpicker_test.go`:

```go
package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

func TestFolderPickerListsLiveThenRecent(t *testing.T) {
	p := newFolderPicker([]string{"/x/live"})
	p.setRecent([]string{"/x/recent", "/x/live"})
	if len(p.shown) != 2 || p.shown[0] != "/x/live" || p.shown[1] != "/x/recent" {
		t.Fatalf("shown %v", p.shown)
	}
	p.filter.SetValue("rec")
	p.applyFilter()
	if len(p.shown) != 1 || p.choice() != "/x/recent" {
		t.Fatalf("filtered %v choice %q", p.shown, p.choice())
	}
}

func TestFolderPickerTypedPath(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "project-one"), 0o755)
	p := newFolderPicker(nil)
	p.filter.SetValue(filepath.Join(dir, "project-one"))
	p.applyFilter()
	if p.choice() != filepath.Join(dir, "project-one") {
		t.Fatalf("typed path choice %q", p.choice())
	}
	if got := completeDir(filepath.Join(dir, "proj")); got != filepath.Join(dir, "project-one")+"/" {
		t.Fatalf("complete: %q", got)
	}
}

func TestNewSessionStartsInChosenFolder(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle))
	m := daemonModel(t, d)
	dir := t.TempDir()
	m = run(t, m, m.newSessionIn(dir))
	if len(d.started) != 1 || d.started[0].CWD != dir || m.opts.CWD != dir || m.sessionView.id != "s_new1" {
		t.Fatalf("started %+v focused %q in %q", d.started, m.sessionView.id, m.opts.CWD)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -race ./internal/tui -run 'TestFolderPicker|TestNewSessionStarts'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `internal/tui/folderpicker.go`**

```go
package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/sessions"
)

// folderPicker chooses the folder for a new session: folders with live
// sessions, recent projects, or a typed path.
type folderPicker struct {
	filter textinput.Model
	all    []string // live first, then recent, no duplicates
	shown  []string
	sel    int
}

type foldersLoadedMsg []string

// loadFolders lists the folders of past sessions, most recent first.
func loadFolders() tea.Cmd {
	return func() tea.Msg {
		var dirs []string
		for _, s := range sessions.ListAll() {
			if s.CWD != "" && !slices.Contains(dirs, s.CWD) {
				dirs = append(dirs, s.CWD)
			}
			if len(dirs) == 50 {
				break
			}
		}
		return foldersLoadedMsg(dirs)
	}
}

func newFolderPicker(live []string) *folderPicker {
	ti := textinput.New()
	ti.Placeholder = "type a folder or filter"
	ti.Prompt = icons.search + " "
	ti.Focus()
	p := &folderPicker{filter: ti}
	for _, d := range live {
		if !slices.Contains(p.all, d) {
			p.all = append(p.all, d)
		}
	}
	p.applyFilter()
	return p
}

func (p *folderPicker) setRecent(dirs []string) {
	for _, d := range dirs {
		if !slices.Contains(p.all, d) {
			p.all = append(p.all, d)
		}
	}
	p.applyFilter()
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// applyFilter keeps folders whose path contains the typed text; a typed
// path to an existing folder goes first.
func (p *folderPicker) applyFilter() {
	q := strings.TrimSpace(p.filter.Value())
	p.shown = p.shown[:0]
	if q != "" && (strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~")) {
		dir := filepath.Clean(expandHome(q))
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			p.shown = append(p.shown, dir)
		}
	}
	for _, d := range p.all {
		if (q == "" || strings.Contains(strings.ToLower(prettyPath(d)), strings.ToLower(q))) && !slices.Contains(p.shown, d) {
			p.shown = append(p.shown, d)
		}
	}
	p.sel = min(p.sel, max(0, len(p.shown)-1))
}

func (p *folderPicker) choice() string {
	if len(p.shown) == 0 {
		return ""
	}
	return p.shown[p.sel]
}

// completeDir extends a typed path to the longest prefix its folder's
// subfolders share, with a trailing slash when only one matches.
func completeDir(input string) string {
	path := expandHome(input)
	parent, prefix := filepath.Dir(path), filepath.Base(path)
	if strings.HasSuffix(path, "/") {
		parent, prefix = path, ""
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return input
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	switch len(names) {
	case 0:
		return input
	case 1:
		return filepath.Join(parent, names[0]) + "/"
	}
	common := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, common) {
			common = common[:len(common)-1]
		}
	}
	return filepath.Join(parent, common)
}

func (m *Model) openFolders() tea.Cmd {
	var live []string
	for _, s := range m.live {
		live = append(live, s.CWD)
	}
	m.folders = newFolderPicker(live)
	m.input.Blur()
	return loadFolders()
}

func (m *Model) handleFoldersKey(k tea.KeyMsg) tea.Cmd {
	p := m.folders
	switch k.String() {
	case "esc", "ctrl+c":
		m.folders = nil
		m.input.Focus()
		return nil
	case "up", "ctrl+p":
		p.sel = max(0, p.sel-1)
		return nil
	case "down", "ctrl+n":
		p.sel = min(len(p.shown)-1, p.sel+1)
		return nil
	case "tab":
		p.filter.SetValue(completeDir(p.filter.Value()))
		p.filter.CursorEnd()
		p.applyFilter()
		return nil
	case "enter":
		dir := p.choice()
		m.folders = nil
		if dir == "" {
			m.input.Focus()
			return nil
		}
		return m.newSessionIn(dir)
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(k)
	p.applyFilter()
	return cmd
}

// newSessionIn starts a session in dir and focuses it.
func (m *Model) newSessionIn(dir string) tea.Cmd {
	opts := m.base
	opts.CWD, opts.Resume, opts.Continue = dir, "", false
	v := newSessionView(opts)
	m.views = append(m.views, v)
	return tea.Batch(m.focus(v), m.startCmd(v, opts))
}

func (p *folderPicker) view(width, height int) string {
	w := min(72, width-4)
	lines := []string{agentStyle.Render("New session in…"), p.filter.View(), ""}
	for i, d := range p.shown {
		if i == height-8 {
			break
		}
		row := "  " + prettyPath(d)
		if i == p.sel {
			row = selStyle.Render(truncate("❯ "+prettyPath(d), w-4))
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", mutedStyle.Render("enter start · tab complete · esc cancel"))
	return popupBox.Width(w).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
```

- [ ] **Step 4: Wire it in**

Add `folders *folderPicker` to `Model`. In `tea.KeyMsg` handling, after the `m.kill` check:

```go
		if m.folders != nil {
			return m, m.handleFoldersKey(msg)
		}
```

Add the case:

```go
	case foldersLoadedMsg:
		if m.folders != nil {
			m.folders.setRecent(msg)
		}
```

In `View` (after the kill overlay):

```go
	if m.folders != nil {
		screen = center(screen, m.folders.view(m.termW, m.termH), m.termW, m.termH-1)
	}
```

In `handleSessionKey` (switch.go):

```go
	case s == "alt+n":
		return m.openFolders(), true
```

In `palette.go`, inside the `m.daemon != nil` block:

```go
		add("Sessions", "New session…", "start one in any folder", "alt+n", func(m *Model) tea.Cmd { return m.openFolders() })
```

- [ ] **Step 5: Run all tests**

Run: `go vet ./internal/tui && go test -race ./internal/tui`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui
git commit -m "Start a session in any folder with alt+n"
```

---

### Task 9: Quit detaches everything; keys in help

**Files:**
- Modify: `internal/tui/model.go` (`quit`, remove `quitAsk`, `handleQuitKey`, `quitView` and their uses), `internal/tui/help.go`, `daemon.go` (remove `liveNote`), `main.go` (exit line)
- Test: `internal/tui/switch_test.go` (append)

**Interfaces:**
- Consumes: `m.views` (Task 1), `detacher` (existing).
- Produces: `func (m *Model) quit() tea.Cmd` (detaches all daemon views, stops a local one).

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/switch_test.go`:

```go
func TestQuitDetachesEverySession(t *testing.T) {
	m := daemonModel(t, newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle), sum("s_2", "/x/b", daemon.StateIdle)))
	a, b := newFakeSession(), newFakeSession()
	m.proc = a
	other := newSessionView(claude.Options{CWD: t.TempDir()})
	other.proc, other.id = b, "s_2"
	m.views = append(m.views, other)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil {
		t.Fatal("ctrl+d didn't quit")
	}
	if !a.detached || !b.detached || a.stopped || b.stopped {
		t.Fatalf("detached %v/%v stopped %v/%v", a.detached, b.detached, a.stopped, b.stopped)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -race ./internal/tui -run TestQuitDetaches`
Expected: FAIL (today `quit` opens the detach-or-stop question).

- [ ] **Step 3: Replace `quit` and remove the question**

In `model.go`, replace `quit`:

```go
// quit leaves: daemon sessions keep running (killing is ctrl+q); a session
// run directly ends with the TUI.
func (m *Model) quit() tea.Cmd {
	for _, v := range m.views {
		switch p := v.proc.(type) {
		case nil:
		case detacher:
			_ = p.Detach()
		default:
			p.Stop()
		}
	}
	return tea.Quit
}
```

Delete `handleQuitKey`, `quitView`, the `quitAsk` field, the `if m.quitAsk { … }` block in key handling and any `quitView()` use in `bottomView` (find it with `grep -n quitAsk internal/tui/*.go`).

- [ ] **Step 4: Help text, exit line, and remove `liveNote`**

In `help.go`, add a group before "View":

```go
	{"Sessions (with -daemon)", [][2]string{
		{"alt+s", "sessions panel"},
		{"alt+↑/↓", "previous / next session"},
		{"alt+1‥9", "session by position"},
		{"ctrl+]", "next that needs you"},
		{"alt+n", "new session in a folder"},
		{"ctrl+q", "kill session"},
		{"", "macOS: alt needs Option as Meta"},
	}},
```

In `daemon.go`, delete `liveNote`. In `main.go`, set `notice = note` instead of joining `liveNote`, and after `p.Run()` returns without error:

```go
	if hub.C != nil {
		if list, err := hub.List(context.Background()); err == nil && len(list) > 0 {
			what := "1 session keeps"
			if len(list) > 1 {
				what = fmt.Sprintf("%d sessions keep", len(list))
			}
			fmt.Printf("%s running in the claudewitz daemon · claudewitz to come back\n", what)
		}
	}
```

(The `defer c.Close()` runs after this, so the connection is still open.)

- [ ] **Step 5: Run all tests**

Run: `go vet ./... && go test -race ./...`
Expected: PASS. A test that drove the old quit question fails to compile; delete it (grep `quitAsk` in `*_test.go`).

- [ ] **Step 6: Commit**

```bash
git add internal/tui daemon.go main.go
git commit -m "Quitting the TUI detaches every daemon session; list the session keys in help"
```

---

### Task 10: The daemon on by default

**Files:**
- Modify: `main.go`, `README.md`, `docs/daemon.md`
- Then: `../claudewitz-ios/docs/protocol.md`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Flip the flag**

In `main.go`, replace

```go
	// Off by default until the TUI can show and switch between the daemon's sessions.
	useDaemon := flag.Bool("daemon", false, "experimental: run the session in the claudewitz daemon, so it outlives the TUI")
```

with

```go
	noDaemon := flag.Bool("no-daemon", false, "run claude directly instead of in the claudewitz daemon")
```

and `if *useDaemon {` with `if !*noDaemon {`.

- [ ] **Step 2: Update the docs**

In `README.md`, replace the `**Experimental:** …` paragraph with:

```markdown
Sessions run in a background `claudewitz daemon`, started on demand, so they outlive the TUI and
one window can drive them all. With two or more live sessions a panel lists them, most urgent
first (a tab strip on narrow terminals): `alt+↑/↓` switches, `ctrl+]` jumps to the next one that
needs you, `alt+n` starts one in any folder and `ctrl+q` kills one. Quitting leaves them running.
The daemon exits after 10 minutes with no clients and no sessions; `claudewitz daemon stop` ends
it sooner. `claudewitz -no-daemon` runs `claude` directly instead. On macOS, the alt keys need
"Use Option as Meta key" in Terminal and iTerm2.
```

In `docs/daemon.md`, replace the paragraph starting `For now the TUI uses the daemon only with` with:

```markdown
The TUI uses the daemon by default (`claudewitz -no-daemon` spawns `claude` directly) and shows
every live session in its sessions panel.
```

and change `- **On demand.** Running \`claudewitz -daemon\` connects` to `- **On demand (default).** Running \`claudewitz\` connects`, and `(\`-daemon\`)` in the TUI migration section to `(\`-no-daemon\`)`.

- [ ] **Step 3: Run everything**

Run: `go vet ./... && go test -race ./... && goreleaser check`
Expected: PASS, config valid.

- [ ] **Step 4: Commit (both repos)**

```bash
git add main.go README.md docs/daemon.md
git commit -m "Run sessions in the daemon by default again, now that the TUI shows them all"
cp docs/daemon.md ../claudewitz-ios/docs/protocol.md
cd ../claudewitz-ios && git add docs/protocol.md && git commit -m "Sync protocol.md: the daemon is the TUI's default again" && cd -
```

Release only after Mikkel has used the panel for a while (the spec's step 4).
