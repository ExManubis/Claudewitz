// Package daemon owns Claude Code sessions on behalf of every client: the
// claudewitz TUI over a local unix socket and, later, the iPhone app. The
// wire protocol is described in docs/daemon.md; this file is its Go form.
package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// Protocol is the protocol version this daemon speaks.
const Protocol = 1

// SocketPath is the local listener, ~/.claudewitz/daemon.sock, or
// $CLAUDEWITZ_SOCKET (for running a second daemon while developing).
func SocketPath() string {
	if p := os.Getenv("CLAUDEWITZ_SOCKET"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claudewitz", "daemon.sock")
	}
	return filepath.Join(home, ".claudewitz", "daemon.sock")
}

// Request is a client → daemon call. Ids are chosen by the client and are
// never 0, so a Frame with id 0 is a push.
type Request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Frame is anything the daemon sends: a reply (ID set) or a push (Event set).
type Frame struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
	Event  string          `json:"event,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Error codes.
const (
	CodeBadRequest    = "bad_request"
	CodeNotFound      = "not_found"
	CodeLocalOnly     = "local_only"
	CodeUnauthorized  = "unauthorized"
	CodeProtocol      = "protocol"
	CodeUnknownMethod = "unknown_method"
	CodeFailed        = "failed"
	CodeBusy          = "busy"
)

// Error is a failed request.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// ── Connection ──────────────────────────────────────────────────────────

type HelloParams struct {
	Protocol      int    `json:"protocol"`
	Client        string `json:"client"` // "tui" or "ios"
	ClientVersion string `json:"clientVersion"`
}

type HelloResult struct {
	Protocol      int     `json:"protocol"`
	DaemonVersion string  `json:"daemonVersion"`
	Account       Account `json:"account"`
}

// Account is the logged-in Claude account. Usage comes from the latest
// rate_limit_event of any session and is null until one arrives.
type Account struct {
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"`
	Usage            *Usage `json:"usage"`
}

type Usage struct {
	FiveHour *UsageWindow `json:"fiveHour,omitempty"`
	Weekly   *UsageWindow `json:"weekly,omitempty"`
}

type UsageWindow struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resetsAt"`
}

// ── Overview ────────────────────────────────────────────────────────────

// Session states.
const (
	StateStarting = "starting"
	StateIdle     = "idle"
	StateWorking  = "working"
	StateWaiting  = "waiting"
	StateExited   = "exited"
)

// SessionSummary is one live session as every client sees it.
type SessionSummary struct {
	ID              string     `json:"id"`
	ClaudeSessionID *string    `json:"claudeSessionId"`
	CWD             string     `json:"cwd"`
	Title           string     `json:"title"`
	State           string     `json:"state"`
	Waiting         *Waiting   `json:"waiting"`
	Activity        *Activity  `json:"activity"`
	Model           string     `json:"model"`
	Effort          string     `json:"effort,omitempty"`
	PermissionMode  string     `json:"permissionMode"`
	ContextUsed     int        `json:"contextUsed"`
	ContextWindow   int        `json:"contextWindow"`
	CostUSD         float64    `json:"costUSD"`
	LastActivity    time.Time  `json:"lastActivity"`
	LastTurnEndedAt *time.Time `json:"lastTurnEndedAt"`
	LastError       *string    `json:"lastError"`
	Clients         int        `json:"clients"`
	// Repo is the main worktree of the repository CWD is in; nil outside
	// git. Worktree is the linked worktree's folder name when CWD is in
	// one, for labels like "claudewitz ⎇ ring-tests".
	Repo     *string `json:"repo"`
	Worktree *string `json:"worktree"`
}

// Waiting is the request a waiting session needs answered.
type Waiting struct {
	RequestID string    `json:"requestId"`
	Tool      string    `json:"tool"`
	Summary   string    `json:"summary"`
	Since     time.Time `json:"since"`
}

// Activity is a session's latest line worth showing in an overview.
type Activity struct {
	Kind string `json:"kind"` // tool | text | thinking
	Text string `json:"text"`
}

type ListResult struct {
	Sessions []SessionSummary `json:"sessions"`
}

// ── Driving a session ───────────────────────────────────────────────────

type StartParams struct {
	CWD            string `json:"cwd"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	Resume         string `json:"resume,omitempty"`
	Continue       bool   `json:"continue,omitempty"`
	// ExtraArgs are passed on to the CLI (the TUI's trailing arguments).
	ExtraArgs []string `json:"extraArgs,omitempty"`
	// Env is the client's environment, for the CLI ("KEY=value"); empty
	// means the daemon's own.
	Env []string `json:"env,omitempty"`
}

type StartResult struct {
	Session SessionSummary `json:"session"`
	// Attached is true when resume or continue found the session already
	// live, so no new claude was started.
	Attached bool `json:"attached,omitempty"`
}

type SessionParams struct {
	Session string `json:"session"`
}

type SubscribeParams struct {
	Session string `json:"session"`
	Since   *int64 `json:"since,omitempty"`
	// Live skips the replay of earlier events (the client has them from
	// the transcript on disk) except the CLI's init and initialize answer.
	Live bool `json:"live,omitempty"`
}

type SubscribeResult struct {
	Summary SessionSummary `json:"summary"`
	Pending []Permission   `json:"pending"`
	// FromSeq is where replay starts: the first replayed seq, or the next
	// seq when nothing is replayed.
	FromSeq int64 `json:"fromSeq"`
}

// Permission is a permission request waiting for an answer.
type Permission struct {
	RequestID string                    `json:"requestId"`
	Request   *claude.PermissionRequest `json:"request"`
}

type SendParams struct {
	Session string  `json:"session"`
	Text    string  `json:"text"`
	Images  []Image `json:"images,omitempty"`
}

// Image is an attached image; Data is base64 on the wire.
type Image struct {
	MediaType string `json:"mediaType"`
	Data      []byte `json:"data"`
}

type RespondParams struct {
	Session   string                    `json:"session"`
	RequestID string                    `json:"requestId"`
	Decision  claude.PermissionDecision `json:"decision"`
	// Interrupt stops the turn right after answering ("deny and stop").
	Interrupt bool `json:"interrupt,omitempty"`
}

type ConfigureParams struct {
	Session        string `json:"session"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
}

// ── Pushes ──────────────────────────────────────────────────────────────

const (
	EventSessionUpdated      = "session.updated"
	EventSessionClosed       = "session.closed"
	EventSessionEvent        = "session.event"
	EventPermissionRequested = "permission.requested"
	EventPermissionResolved  = "permission.resolved"
	EventAccountUpdated      = "account.updated"
)

type SessionUpdatedParams struct {
	Summary SessionSummary `json:"summary"`
}

type SessionClosedParams struct {
	Session   string  `json:"session"`
	LastError *string `json:"lastError"`
}

// SessionEventParams carries one stream-json line from the CLI, unchanged.
type SessionEventParams struct {
	Session string          `json:"session"`
	Seq     int64           `json:"seq"`
	Data    json.RawMessage `json:"data"`
}

type PermissionRequestedParams struct {
	Session   string                    `json:"session"`
	Seq       int64                     `json:"seq"`
	RequestID string                    `json:"requestId"`
	Request   *claude.PermissionRequest `json:"request"`
}

type PermissionResolvedParams struct {
	Session   string `json:"session"`
	Seq       int64  `json:"seq"`
	RequestID string `json:"requestId"`
	By        string `json:"by"` // the answering client ("tui", "ios"), or "rule"
}

type AccountUpdatedParams struct {
	Account Account `json:"account"`
}

// now is the current time at whole seconds: Swift's .iso8601 decoding
// rejects fractional seconds.
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// ── Remote access and pairing ───────────────────────────────────────────

// PairParams is sent by an unpaired remote client, with the code from the
// QR code `claudewitz pair` shows.
type PairParams struct {
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
}

type PairResult struct {
	// Token is the device's long-lived token, for "Authorization: Bearer"
	// on later connections. It is only ever sent this once.
	Token    string `json:"token"`
	DeviceID string `json:"deviceId"`
}

// The methods below are local only: the CLI's `claudewitz daemon …`,
// `pair` and `devices` commands.

type RemoteSetParams struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port,omitempty"` // 0: the saved port, else DefaultRemotePort
}

// RemoteStatus is the remote listener: Host and Port are set while it is
// listening; Error says why an enabled listener is down.
type RemoteStatus struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	Error   string `json:"error,omitempty"`
}

// PairCodeResult is what `claudewitz pair` puts in its QR code (host,
// port, code), plus when the code expires.
type PairCodeResult struct {
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// DeviceInfo is a paired device as `claudewitz devices` shows it.
type DeviceInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	PairedAt  time.Time `json:"pairedAt"`
	LastSeen  time.Time `json:"lastSeen"`
	Connected int       `json:"connected"` // live connections right now
}

type DevicesResult struct {
	Devices []DeviceInfo `json:"devices"`
}

// StopParams asks the daemon to exit. Live sessions end with it, so
// without Force it refuses while there are any.
type StopParams struct {
	Force bool `json:"force"`
}

type RevokeParams struct {
	Device string `json:"device"` // id or name
}

type RevokeResult struct {
	Device DeviceInfo `json:"device"`
}

type Listener struct {
	Kind    string `json:"kind"` // local | remote
	Address string `json:"address"`
}

type StatusResult struct {
	PID           int              `json:"pid"`
	DaemonVersion string           `json:"daemonVersion"`
	StartedAt     time.Time        `json:"startedAt"`
	Listeners     []Listener       `json:"listeners"`
	Remote        RemoteStatus     `json:"remote"`
	Clients       int              `json:"clients"`
	Sessions      []SessionSummary `json:"sessions"`
	Devices       []DeviceInfo     `json:"devices"`
}
