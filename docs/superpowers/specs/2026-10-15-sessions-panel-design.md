# Sessions panel: one TUI for every live session

Date: 2026-10-15. Status: design approved in conversation, awaiting spec review.

## Purpose

`claudewitz -daemon` keeps sessions running after the TUI quits, but a TUI shows only the one
session it started. Detached sessions are invisible until you resume them one by one, which is
why v0.2.0 ships the daemon off by default.

This design makes one claudewitz window the place to watch and drive every live session in the
daemon, across all folders: see at a glance which sessions need you, switch to one and act on
it, start and kill sessions, and never lose track of detached ones. Once it has been used for a
while, the daemon becomes the default again.

**Success:**
- With several sessions in different folders, you can tell which is which and which need you
  without switching.
- Switching to a session and back keeps its draft, scroll position and open dialog.
- A session that needs you is reached with one key, and its permission is answered in its
  normal dialog.
- Sessions can be started in any folder and killed from the TUI.
- With one session, the TUI looks as it does today.

## Decisions

- **All folders.** One window manages the daemon's sessions in every folder. Git status, the
  `@` file index and prompt history follow the focused session's folder.
- **Answer by jumping, not from the panel.** A waiting session's permission, question or plan is
  answered in its normal dialog after focusing it (`ctrl+]`), never inline in the panel.
- **New sessions in any folder** (`alt+n`, with a folder picker).
- **Layout:** the accent-row panel on wide terminals, a one-line tab strip below 140 columns.
- **Rows show title and folder only.** The glyph and colour carry the state; no activity or
  command text. The folder label has its own colour.
- **Attention-first order**, stable within each state.
- **Kill session** is `ctrl+q`, behind a confirmation dialog that defaults to keeping it running.
- **Quitting the TUI detaches every session.** No per-quit "detach or stop" question.

Mockups (suggestions, in the Paper file "Claudewitz — Agents panel"): *Proposal · Accent rows +
jump*, *Alternative · Tab strip*, *Proposal · Kill session (ctrl+q)*.

## Architecture

The TUI's `Model` (internal/tui/model.go) holds one session today: about 30 of its fields
belong to that session and several to its folder. It splits into three layers.

### sessionView (one per opened session)

Everything that today is "the session", moved unchanged:

- the `claude.Session` handle
- the chat transcript (`*chat`) and tool tracker (`*tracker`)
- pending permissions and the open question, plan, edit panel and diff view
- queued messages, image attachments, the input draft and suggestion
- scroll position (`vp` offset, `follow`), transcript focus and selection
- turn state: `busy`, `stopped`, `turnFrom`, `lastTurn`, activity label
- session state: init and initialize responses, session id and title, model, permission mode,
  cost, last usage, context window, rate-limit windows, warned limits
- always-allow rules

Event handling is the existing `handleEvent`, run against the view the event's session belongs
to. Events for unfocused views update them without redrawing the chat.

### workspace (one per folder)

- git status, its poller and poll counter
- the `@` file index
- prompt history

Sessions in the same folder share one workspace. A workspace is created when a session in its
folder is first focused and dropped when no open view uses it. Git polling runs only for the
focused session's workspace.

### Shell (what stays in Model)

- layout, size, the sessions panel or tab strip, the sidebar toggle
- toasts, help, the command palette, pickers (session, folder, model, theme), changes viewer
- theme and config
- the overview: every live session's `daemon.SessionSummary`, kept current from the daemon's
  `session.updated` / `session.closed` pushes (`client.Overview()`)
- the focused session id, the panel's done/idle bookkeeping

Rendering and keys read the focused view and its workspace through one accessor.

### Without the daemon

`-no-daemon` (and today's default) uses the same code with a single local view. The overview is
empty, so the panel and tab strip are hidden. There is one code path, not two.

## The panel

### Rows

Two lines per row, with a 2-cell coloured bar on the left:

1. glyph, title, and on the right the time in the current state (`2m`, `40s`, `1h`)
2. the folder label, in the theme's `second` colour

Titles are the summary's `title` cut with `…` to fit; a session with no title yet shows
`new session`.

| State | Glyph | Bar and glyph colour | Title |
|---|---|---|---|
| waiting (permission, question or plan) | `◌` | warn | warn, bold |
| error (exited with `lastError`) | `✗` | err | err |
| working | `▶` | accent glyph, subtle bar | text |
| done (turn ended since last focused) | `✓` | ok glyph, subtle bar | text |
| idle | `○` | muted | muted |

The focused row has the `selBg` fill and an accent bar, whatever its state. Glyphs come from the
TUI's icon set, so `/icons unicode` and Nerd Font both work and ambiguous-width characters are
avoided where the icon set already does.

Header: `SESSIONS` on the left; on the right `▲ n need you` in warn when any session is waiting
or errored, otherwise the session count.

Footer hint inside the panel: `ctrl+] next waiting · ctrl+q kill`.

### Folder labels

- The folder's base name (`claudewitz`).
- When two live sessions are in different folders with the same base name, add the parent
  (`code/api`, `work/api`).
- For a git worktree, `repo ⎇ worktree` (`claudewitz ⎇ ring-tests`), using the daemon's new
  repository fields (see Daemon changes). The worktree part is the worktree's folder name.
- The full path and branch stay in the right-hand sidebar for the focused session.

### Order

Waiting, then error, then working, done, idle. Within a state, the order sessions started in.
Focus does not move rows. Rows never reorder on activity alone, so they don't jump while
sessions stream.

### Done and idle

A session becomes **done** when its turn ends (`lastTurnEndedAt` changes) while it isn't
focused. Focusing it makes it **idle**. This bookkeeping lives in the TUI; the daemon reports
`idle` for both.

### Errors

A session that exits with an error stays in the panel as `✗` (the TUI keeps its last summary
after `session.closed` with a `lastError`) until it is focused and dismissed (`ctrl+q` on an
exited session dismisses it without a dialog). A clean exit removes the row.

## Switching

- Click a row, `alt+↑`/`alt+↓` for the previous or next row, `alt+1`‥`alt+9` by position,
  `ctrl+]` for the next session that needs you (waiting first, oldest wait first, then errors).
- **First focus** of a session subscribes to it and builds its view the way attaching does
  today: the transcript from disk, then the live stream (`SubscribeLive`), plus its pending
  permissions from the subscribe result.
- **Later focus** is instant: the view is already current, with draft, scroll position and
  dialogs intact.
- A waiting session shows its normal permission, question or plan dialog when focused.
- Views stay subscribed while their session is live. They're dropped on `session.closed`
  (except errors, as above) and when the TUI quits.

## Noticing what needs you

- When an unfocused session starts waiting, a toast: `api-gateway needs you · ctrl+]`, the row
  turns warn and the header count updates.
- The desktop notification fires as it does today, for any session.
- In the tab strip, sessions that need you sort to the front.

## Layout

- **≥ 140 columns:** the panel (40 columns) on the left, the chat, and the existing sidebar on
  the right when there's room for it (`sidebarMinWidth` rules unchanged, measured on what's left).
- **< 140 columns:** a one-line tab strip above the chat instead of the panel. Each tab is glyph
  plus folder label; when two tabs share a label, a short title is added
  (`figment-eng · fonts`). The focused tab has the `selBg` fill and an accent underline. Tabs
  that don't fit scroll with the focused one in view.
- **One session:** neither the panel nor the strip is shown.
- `alt+s` toggles the panel (or strip); `ctrl+b` still toggles the sidebar.

## Kill session

- `ctrl+q` asks to kill the focused session; the command palette has **Kill session** too.
- The dialog, in the TUI's popup style with an err border:
  - `Kill session <title>?` and the folder (full path, `~` for home)
  - when the session is working or waiting: `▲ It's working right now; the current turn is
    lost.` in warn
  - `claude ends; the transcript is kept, ctrl+r resumes it.` in muted
  - choices: `❯ 1. Keep it running  n · esc` (selected by default) and `2. Kill session  y` in err
- Killing calls `session.stop`. Focus moves to the next row in panel order; killing the last
  session leaves the empty state.
- `ctrl+q` is grabbed by VS Code's terminal on Linux and Windows (it quits the editor there);
  the palette entry covers that.

## New session

- `alt+n` (and palette **New session…**) opens a folder picker in the session picker's style:
  folders of live sessions, recent projects from transcripts on disk (`sessions.ListAll`), and a
  typed path with `~` expansion and tab completion.
- Picking a folder starts a session there through the daemon (`session.start` with this
  terminal's environment, as `client.Starter` does today). It joins the panel with focus.
- `/new` keeps starting a fresh session in the focused session's folder.

## Quitting and starting

- **Quit** (`ctrl+d`) detaches from every session; all keep running. The per-quit
  "detach or stop?" question is removed. After the TUI exits it prints
  `n sessions keep running in the claudewitz daemon · claudewitz to come back` when n > 0.
- **Launch** shows all live sessions in the panel. Focus goes to a live session in the current
  folder if there is one (most recently active), otherwise to a new session there.
  `-continue` / `-resume` work as today and attach when the session is live.
- The startup note about live sessions (`liveNote`) is replaced by the panel and removed.

## Keys

| Key | Action |
|---|---|
| `alt+s` | toggle the sessions panel / tab strip |
| `alt+↑` / `alt+↓` | previous / next session |
| `alt+1`‥`alt+9` | focus session by position |
| `ctrl+]` | next session that needs you |
| `alt+n` | new session (folder picker) |
| `ctrl+q` | kill the focused session (with confirmation) |

All of these are also in the command palette. On macOS, Alt keys need "Use Option as Meta key"
in Terminal and iTerm2; the help screen says so.

## Daemon changes

`SessionSummary` gains two fields, set once when the session starts from `git -C <cwd>
rev-parse --show-toplevel --git-common-dir`:

```jsonc
"repo": "/Users/mikkel/Developer/claudewitz",   // the repository's main worktree; null outside git
"worktree": "ring-tests"                        // the worktree's folder name when cwd is in a linked worktree, else null
```

docs/daemon.md (and claudewitz-ios docs/protocol.md) document them. No other protocol changes.

## Build order

Each step is mergeable and shippable on its own.

1. **Split the Model.** Move fields into `sessionView` and `workspace`; the Model holds one of
   each and reaches them through the focused accessor. No visible change.
2. **Panel and switching.** Overview in the shell, the panel, the tab strip, focusing with
   subscribe-on-first-use, `alt+↑↓`, `alt+1‥9`, `ctrl+]`, the needs-you toast, done/idle.
3. **Kill and new.** `ctrl+q` and its dialog, `alt+n` and the folder picker, quit detaches all
   with the exit line, the daemon's `repo`/`worktree` fields.
4. **Daemon on by default.** `-daemon` becomes the default and `-no-daemon` the opt-out; README
   and daemon.md updated. Released after the panel has been used for a while.

## Testing

- **Step 1:** the existing TUI tests (104) pass unchanged; a new test shows two views keep
  separate transcripts, drafts and pending dialogs.
- **Step 2:** row order across states and start order; folder labels for clashes and
  worktrees; panel vs tab strip by width and hidden with one session; switching away and back
  keeps the draft and an open dialog; `ctrl+]` picks the oldest waiting session; done becomes
  idle on focus; driven by the fake sessions the daemon client tests already use.
- **Step 3:** the kill dialog defaults to keep; killing the focused session moves focus to the
  next row; killing the last leaves the empty state; the folder picker lists live and recent
  folders; the daemon reports `repo`/`worktree` for a plain repo, a linked worktree and a
  non-git folder.
- Every run with `-race`. The real-CLI smoke test covers start, switch away, switch back.
  Mikkel tries it in a real terminal before step 4.

## Out of scope

- Answering permissions from the panel without focusing.
- Grouping rows under folder headers.
- Past (not live) sessions in the panel; `ctrl+r` stays the way to resume those.
- Changes to the iOS app; it already follows the same session summaries.
