# Daemon and client protocol (draft)

Status: built except APNs (`device.registerPush`, push notifications), git state and
`session.changes`/`session.diff`. This is the contract between `claudewitz daemon`, the
claudewitz TUI and the iPhone companion app (separate repo).

## Why

Today the TUI starts `claude` itself, so a session dies with the TUI and nothing else can see it.
The daemon owns the `claude` processes instead; the TUI and the phone are both clients of it.
Sessions keep running with no client attached (like herdr), but clients talk structured JSON
rather than a PTY (like `crush serve`, minus tearing the workspace down when the last client
leaves).

```
                    ┌─────────────────── claudewitz daemon ───────────────────┐
claudewitz TUI ────▶│ sessions: claude.Process per session, event log,        │
 (unix socket)      │ pending permission requests, git state per cwd          │
                    │                                                         │──▶ APNs
iPhone app ────────▶│ one WebSocket protocol on both listeners                │
 (WebSocket over    └──────────────┬──────────────────────────────▲───────────┘
  Tailscale)                stdin: stream-json            stdout: stream-json
                            ┌──────▼──────────────────────────────┴──────┐
                            │ claude -p … (one per session, unchanged)   │
                            └────────────────────────────────────────────┘
```

## Lifecycle

The daemon is part of the claudewitz binary (`claudewitz daemon`), not a separate install.

The TUI uses the daemon by default (`claudewitz -no-daemon` spawns `claude` directly) and shows
every live session in its sessions panel.

- **On demand (default).** Running `claudewitz` connects to `~/.claudewitz/daemon.sock`; if
  nothing answers it starts `claudewitz daemon` detached and connects. The daemon exits once
  it has had no clients and no live sessions for 10 min.
- **Always on.** `claudewitz daemon install` writes a launchd LaunchAgent
  (`~/Library/LaunchAgents/ai.claudewitz.daemon.plist`, `RunAtLoad` + `KeepAlive`) and the
  daemon never idles out. `claudewitz daemon uninstall` removes it.

Sessions are persistent agents, as with Claude Code Remote Control: quitting the TUI detaches
from the session rather than ending it, and it stays live in the daemon (visible on the phone,
reattachable from any TUI) until someone stops it with `session.stop`. Quitting the TUI detaches
from every session; ctrl+q (or the palette's Kill session) stops one.
- `claudewitz daemon status` shows pid, uptime, listeners, sessions and paired devices.
- `claudewitz daemon stop` ends the daemon; it refuses while sessions are live unless `-force`d.
- A TUI that finds a daemon of another version (after an upgrade it is the old binary) asks it to
  stop and starts a fresh one; if the old one has live sessions it is kept, and the TUI says so.

If the daemon is restarted, live `claude` processes go with it; sessions come back as resumable
(`--resume`) from their transcripts, so nothing is lost but the in-flight turn.

## Transport and auth

One protocol, two listeners:

| Listener | Address | Auth |
|---|---|---|
| Local | WebSocket over unix socket `~/.claudewitz/daemon.sock` (mode 0600) | File permissions |
| Remote | WebSocket on TCP, off by default; bound to the Tailscale address when enabled | Device token |

Remote is enabled with `claudewitz daemon remote on [-port N]` (default port 7745) and disabled
with `claudewitz daemon remote off`; the setting is kept in `~/.claudewitz/daemon.json` and
survives restarts. It binds to the machine's Tailscale IPv4 address (what `tailscale ip -4` says,
else the 100.64.0.0/10 address of a local interface), never `0.0.0.0`, so it's reachable from the
tailnet only, and Tailscale encrypts the link. `remote on` fails with a clear error when Tailscale
isn't up; a daemon that starts before Tailscale does keeps retrying every 30s. The on-demand
daemon still idles out with no clients and no live sessions, so the phone can only reach it while
there is something to see (or always, with `daemon install`).

Pairing: `claudewitz pair` prints a QR code whose content is the JSON
`{"host": "100.x.y.z", "port": 7745, "code": "K7MQ2XRA9DFP"}`. The code is 12 characters from
`23456789ABCDEFGHJKLMNPQRSTUVWXYZ` (compared case-insensitively), single use, and expires after
5 min. The app connects with no `Authorization` header, sends `hello`, then
`pair {code, deviceName}` and gets back `{token, deviceId}`. From then on that same connection is
the paired device (it gets pushes and may call everything a remote client may), so the app can
carry on without reconnecting. Later connections send `Authorization: Bearer <token>` on the
WebSocket upgrade. Tokens are stored as SHA-256 hashes in `~/.claudewitz/devices.json` (0600),
with the device name, when it was paired and when it was last seen. `claudewitz devices` lists
paired devices and `claudewitz devices revoke <name|id>` unpairs one and drops its live
connections. `claudewitz daemon status` shows the listeners and paired devices.

Refusals on the remote listener:

- A token that is unknown or revoked, or an `Authorization` header that isn't `Bearer …`: the
  upgrade is refused with HTTP 401. The app should treat 401 as "unpaired" and offer to pair again.
- No `Authorization` header while no pairing code is outstanding: HTTP 401.
- Any `Origin` header (a browser on the tailnet): HTTP 403. The app must not send one.
- On an unpaired connection, anything but `hello` and `pair` gets `error.code = "unauthorized"`
  and the connection is closed. A wrong or expired code gets `unauthorized`; the third wrong code,
  or a minute without pairing, closes the connection. Unpaired connections get no pushes.
- `pair` on a local or already paired connection is `bad_request`.

## Framing

Each WebSocket text frame is one JSON object. Three kinds:

```jsonc
// client → daemon request; id is chosen by the client
{"id": 7, "method": "session.send", "params": {"session": "s_1", "text": "run the tests"}}

// daemon → client reply to that request
{"id": 7, "result": {}}
{"id": 7, "error": {"code": "not_found", "message": "no session s_1"}}

// daemon → client push, unprompted
{"event": "session.updated", "params": {...}}
```

Field names are camelCase. Unknown fields and unknown events must be ignored, so either side
can add things without breaking the other. Breaking changes bump `protocol` in `hello`.

## Methods

### Connection

| Method | Params | Result |
|---|---|---|
| `hello` | `{protocol: 1, client: "tui" \| "ios", clientVersion}` | `{protocol, daemonVersion, account: Account}` |
| `pair` | `{code, deviceName}` | `{token, deviceId}` (remote, unpaired connections only; see Transport and auth) |
| `device.registerPush` | `{apnsToken, sandbox}` | `{}` |

```jsonc
// Account. usage comes from the latest rate_limit_event of any session; null until one arrives.
{
  "email": "mikkel@…", "subscriptionType": "max",
  "usage": {
    "fiveHour": {"utilization": 0.17, "resetsAt": "2026-10-15T11:52:00Z"},
    "weekly":   {"utilization": 0.49, "resetsAt": "2026-10-17T16:00:00Z"}
  }
}
```

When usage changes the daemon pushes `account.updated {account}` to every client, so the phone's
usage bars stay current without subscribing to a session.

`hello` must be the first request; the daemon closes connections that send anything else first,
or that ask for a `protocol` it doesn't speak.

Management methods, all local only, used by the `claudewitz` CLI:

| Method | Params | Result |
|---|---|---|
| `daemon.status` | `{}` | `{pid, daemonVersion, startedAt, listeners: [{kind, address}], remote, clients, sessions, devices}` |
| `remote.set` | `{enabled, port?}` | `{enabled, host?, port?, error?}` |
| `pair.code` | `{}` | `{host, port, code, expiresAt}` |
| `devices.list` | `{}` | `{devices: [{id, name, pairedAt, lastSeen, connected}]}` |
| `devices.revoke` | `{device}` (id or name) | `{device}` |
| `daemon.stop` | `{force?}` | `{}`; the daemon exits just after replying. Without `force`, `busy` while sessions are live |

### Overview (every client gets this, no subscription needed)

| Method | Params | Result |
|---|---|---|
| `sessions.list` | `{}` | `{sessions: [SessionSummary]}`, live sessions only |
| `sessions.history` | `{cwd?, limit?}` | `{sessions: [PastSession]}`, from transcripts on disk (`sessions.ListAll`); local only |

After `hello` the daemon pushes `session.updated` whenever any live session's summary changes, and
`session.closed` when one ends, so a client can keep an overview of every project current without
subscribing to each session's full stream. The TUI uses this for an overview pane of every live
session across projects. `activity` exists for the same reason: a client shows every session's
latest line from the summary and subscribes only to the sessions it's showing in full. The phone
subscribes to the focused session and any waiting one, and unsubscribes from the rest, to save
data and battery over Tailscale.

**Remote clients see live sessions only.** Like Remote Control, the phone lists and drives the
persistent agents running in the daemon; it doesn't browse folders, start sessions or resume
past ones. Sessions are started from a TUI on the Mac. Methods marked *local only* are refused
on the remote listener with `error.code = "local_only"`; so are the CLI's management methods
(see below). (Decided 2026-10-15: live only for now.
Resuming or starting from the phone would need a way to pick a cwd, recent projects from
transcripts or configured roots, and `session.start` opened up on the remote listener.)

```jsonc
// SessionSummary
{
  "id": "s_1",                    // daemon id, stable for the session's life
  "claudeSessionId": "9f2c…",     // from the CLI's init event; null until it arrives
  "cwd": "/Users/mikkel/Developer/claudewitz",
  "title": "Add daemon protocol", // first prompt, as sessions.Info.Label
  "state": "working",             // starting | idle | working | waiting | exited
  "waiting": {"requestId": "r_4", "tool": "Bash", "summary": "go test ./...",
              "since": "2026-10-14T14:02:02Z"}, // when state=waiting; tool is "AskUserQuestion"
                                                // or "ExitPlanMode" for questions and plans
  "activity": {"kind": "tool", "text": "Grep \"claimAmount\" in src/"},
                                  // the latest line worth showing in an overview: kind is
                                  // tool | text | thinking; null before the first turn
  "model": "claude-opus-5-5",
  "effort": "high",
  "permissionMode": "default",
  "contextUsed": 41200, "contextWindow": 1000000,
  "costUSD": 1.92,
  "lastActivity": "2026-10-14T14:02:11Z",
  "lastTurnEndedAt": "2026-10-14T13:58:40Z", // null until a turn ends; "done · 14m" in overviews
  "lastError": null,              // why the session exited, when state=exited after a failure
  "clients": 2,                   // clients subscribed right now
  "repo": "/Users/mikkel/Developer/claudewitz", // the repository's main worktree; null outside git
  "worktree": null,               // a linked worktree's folder name, e.g. "ring-tests"
  "git": {"branch": "main", "dirty": 3, "added": 120, "removed": 14}
}
```

### Driving a session

| Method | Params | Result |
|---|---|---|
| `session.start` | `{cwd, model?, effort?, permissionMode?, resume?, continue?}` | `{session: SessionSummary}`; local only |
| `session.subscribe` | `{session, since?}` | `{summary, pending: [Permission], fromSeq}` |
| `session.unsubscribe` | `{session}` | `{}` |
| `session.send` | `{session, text, images?: [{mediaType, data}]}` | `{}` |
| `session.interrupt` | `{session}` | `{}` |
| `session.respond` | `{session, requestId, decision: PermissionDecision, interrupt?}` | `{}` |
| `session.configure` | `{session, model?, effort?, permissionMode?}` | `{}` |
| `session.stop` | `{session}` | `{}`, closes stdin and lets the CLI exit |
| `session.changes` | `{session}` | `{files: [{path, status, added, removed}]}` |
| `session.diff` | `{session, path}` | `{diff}`, unified diff for one file |

These map one-to-one onto `claude.Process` (`Start`, `Send`/`SendWithImages`, `Interrupt`,
`Respond`, `SetModel`/`SetEffort`/`SetPermissionMode`, `Close`), and `session.changes`/`diff` onto
what `gitstatus` and the changes viewer already do. `decision` is `claude.PermissionDecision`,
so AskUserQuestion answers and plan approval work the same from any client, with two additions
the daemon handles itself rather than passing to the CLI:

- `decision.alwaysForSession: true` (with `allow`) adds a session allow rule: later requests for
  the same tool in that session are allowed by the daemon without asking any client, as the TUI's
  "always allow" does today. For edit tools it sends `setMode acceptEdits` instead, also as the
  TUI does. Rules live in the daemon so they hold whichever client answered.
- `interrupt: true` on `session.respond` (with `deny`) denies and then interrupts the turn in one
  step: the phone's "Deny and stop", ctrl+c in the TUI's permission dialog. Doing both in the
  daemon avoids a race where the denied turn carries on before a separate interrupt lands.

AskUserQuestion answers are `updatedInput.answers`, question text → answer. An answer is usually
an option's label, but may be any text, for the "type something else" row.

`session.start` with `resume` reattaches to a live session instead of starting a second `claude`
on the same transcript, if one is already running.

### Session stream

While subscribed, the client gets:

```jsonc
{"event": "session.event", "params": {"session": "s_1", "seq": 412, "data": <claude event>}}
```

`data` is the CLI's stream-json line, passed through unchanged (`system`, `assistant`, `user`,
`stream_event`, `result`, `rate_limit_event`, task events). The TUI already parses these in
`claude.parseLine`; the phone parses the same shapes, documented by `internal/claude/protocol.go`.
Passing them through keeps the daemon thin and means new CLI features reach clients without
daemon changes.

Exceptions the daemon handles itself rather than passing through:

- `can_use_tool` control requests become `permission.requested`
  `{session, requestId, request: PermissionRequest}`; the daemon holds them until a client
  answers. Any subscribed client may answer; the first `session.respond` wins and everyone gets
  `permission.resolved {session, requestId, by: "ios"}` so other clients close their dialog.
- Control responses (`initialize`, `set_model`, …) are consumed by the daemon and reflected in
  `session.updated`.

Every event gets a per-session `seq`. The daemon keeps the current turn's events, plus the last
2,000 events, in memory. `subscribe` with `since` replays everything after that seq if it's still
held; otherwise `fromSeq` says where replay starts, and the client loads earlier history with
`sessions.history`/transcript replay, as the TUI does with `--resume` today. `stream_event`
deltas are dropped from the buffer once their complete message arrives, so a reconnecting phone
gets finished messages, not thousands of deltas.

## Push notifications

The daemon sends APNs pushes directly (token auth, `.p8` key configured with
`claudewitz daemon apns --key … --key-id … --team-id …`); no extra server. It pushes to every
registered device when:

- a permission request is waiting and no client has answered within 10s
  (category `PERMISSION` with Allow/Deny actions; the action calls `session.respond`)
- a turn ends (`result`) and no client is subscribed to that session
- a session exits with an error

Payloads carry session id, cwd name and a one-line summary; no code or transcript content.

## TUI migration

1. Put an interface in front of `claude.Process` (`Events`, `Send`, `SendWithImages`, `Respond`,
   `Interrupt`, `Set*`, `Close`) and have `internal/tui` use only that.
2. Add `internal/daemon` (server) and `internal/daemon/client`, whose session handle implements
   the same interface over the socket. The TUI picks local or daemon at startup (`-no-daemon`).
3. Move git polling into the daemon, per cwd, so the TUI and phone share one poller.
4. Add the remote listener, pairing and APNs, then build the app against this doc.

## Open questions

- Image attachments from the phone: inline base64 in `session.send` is simplest; large photos may
  want a separate upload method.
