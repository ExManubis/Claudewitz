package claude

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Options configures how the CLI is launched.
type Options struct {
	Binary         string // defaults to "claude"
	Model          string
	Effort         string // low, medium, high, xhigh, max; empty for the model default
	PermissionMode string
	Resume         string // session ID to resume
	Continue       bool
	CWD            string
	ExtraArgs      []string
}

// Event is anything parsed off the CLI's stdout. Exactly one field is set.
type Event struct {
	Init       *InitEvent
	Message    *MessageEvent
	Stream     *StreamEvent
	Result     *ResultEvent
	RateLimit  *RateLimitEvent
	Permission *PermissionRequest
	Task       *TaskEvent
	Initialize *InitializeResponse
	// ContextWindow is the effective context size from autocompact_state.
	ContextWindow int
	Status        string // system/status payload, e.g. "requesting"
	// PermissionMode is set when the CLI changes mode itself (e.g. leaving
	// plan mode after a plan is approved).
	PermissionMode string
	Err            error // stdout closed or unparseable output
	Exited         bool
}

// Process is a running `claude` CLI.
type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan Event
	mu     sync.Mutex
	stderr *tailBuffer
}

// Start launches the CLI and begins streaming events.
func Start(ctx context.Context, opts Options) (*Process, error) {
	bin := opts.Binary
	if bin == "" {
		bin = "claude"
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
		"--forward-subagent-text",
		"--replay-user-messages",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	if opts.Resume != "" {
		args = append(args, "--resume", opts.Resume)
	} else if opts.Continue {
		args = append(args, "--continue")
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = opts.CWD
	cmd.Env = childEnv(os.Environ())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &Process{cmd: cmd, stdin: stdin, events: make(chan Event, 256), stderr: &tailBuffer{max: 8192}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	go p.readLoop(stdout)
	return p, nil
}

// Events returns the channel of parsed events. It is closed after Exited.
func (p *Process) Events() <-chan Event { return p.events }

func (p *Process) readLoop(r io.Reader) {
	defer close(p.events)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if ev, ok := parseLine(line); ok {
			p.events <- ev
		}
	}
	err := p.cmd.Wait()
	if err != nil {
		if tail := p.stderr.String(); tail != "" {
			err = fmt.Errorf("%w: %s", err, tail)
		}
	}
	p.events <- Event{Exited: true, Err: err}
}

func parseLine(line []byte) (Event, bool) {
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Event{Err: fmt.Errorf("bad line from claude: %s", line)}, true
	}
	switch env.Type {
	case "system":
		switch env.Subtype {
		case "init":
			var e InitEvent
			if json.Unmarshal(line, &e) == nil {
				return Event{Init: &e}, true
			}
		case "task_started", "task_progress", "task_updated", "task_notification":
			var e TaskEvent
			if json.Unmarshal(line, &e) == nil {
				return Event{Task: &e}, true
			}
		case "status":
			var s struct {
				Status         *string `json:"status"`
				PermissionMode string  `json:"permissionMode"`
			}
			if json.Unmarshal(line, &s) == nil && (s.Status != nil || s.PermissionMode != "") {
				ev := Event{PermissionMode: s.PermissionMode}
				if s.Status != nil {
					ev.Status = *s.Status
				}
				return ev, true
			}
		}
	case "autocompact_state":
		var a struct {
			Value struct {
				EffectiveWindow int `json:"effective_window"`
			} `json:"value"`
		}
		if json.Unmarshal(line, &a) == nil && a.Value.EffectiveWindow > 0 {
			return Event{ContextWindow: a.Value.EffectiveWindow}, true
		}
	case "assistant", "user":
		var e MessageEvent
		if json.Unmarshal(line, &e) == nil {
			return Event{Message: &e}, true
		}
	case "stream_event":
		var e StreamEvent
		if json.Unmarshal(line, &e) == nil {
			return Event{Stream: &e}, true
		}
	case "result":
		var e ResultEvent
		if json.Unmarshal(line, &e) == nil {
			return Event{Result: &e}, true
		}
	case "rate_limit_event":
		var e RateLimitEvent
		if json.Unmarshal(line, &e) == nil {
			return Event{RateLimit: &e}, true
		}
	case "control_response":
		var cr struct {
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
				Error     string          `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &cr) != nil {
			break
		}
		if strings.HasPrefix(cr.Response.RequestID, "initialize-") {
			if cr.Response.Error != "" {
				return Event{Err: fmt.Errorf("initialize: %s", cr.Response.Error)}, true
			}
			var ir InitializeResponse
			if json.Unmarshal(cr.Response.Response, &ir) == nil {
				return Event{Initialize: &ir}, true
			}
		}
	case "control_request":
		var cr controlRequest
		if json.Unmarshal(line, &cr) != nil {
			break
		}
		var sub struct {
			Subtype string `json:"subtype"`
		}
		_ = json.Unmarshal(cr.Request, &sub)
		if sub.Subtype == "can_use_tool" {
			var pr PermissionRequest
			if json.Unmarshal(cr.Request, &pr) == nil {
				pr.RequestID = cr.RequestID
				return Event{Permission: &pr}, true
			}
		}
	}
	return Event{}, false
}

func (p *Process) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err = p.stdin.Write(append(b, '\n'))
	return err
}

// Send submits a user prompt.
func (p *Process) Send(text string) error {
	return p.write(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	})
}

// Respond answers a permission request.
func (p *Process) Respond(req *PermissionRequest, d PermissionDecision) error {
	if d.Behavior == "allow" && d.UpdatedInput == nil {
		d.UpdatedInput = req.Input
	}
	var r controlResponse
	r.Type = "control_response"
	r.Response.Subtype = "success"
	r.Response.RequestID = req.RequestID
	r.Response.Response = d
	return p.write(r)
}

// control sends a host-initiated control request (interrupt, set_model, ...).
func (p *Process) control(req map[string]any) error {
	return p.write(map[string]any{
		"type":       "control_request",
		"request_id": newID(),
		"request":    req,
	})
}

// Initialize asks the CLI for its commands, models and account info; the
// answer arrives as an Event with Initialize set.
func (p *Process) Initialize() error {
	return p.write(map[string]any{
		"type":       "control_request",
		"request_id": "initialize-" + newID(),
		"request":    map[string]any{"subtype": "initialize"},
	})
}

// Interrupt stops the current turn.
func (p *Process) Interrupt() error {
	return p.control(map[string]any{"subtype": "interrupt"})
}

// SetPermissionMode switches permission mode mid-session.
func (p *Process) SetPermissionMode(mode string) error {
	return p.control(map[string]any{"subtype": "set_permission_mode", "mode": mode})
}

// SetModel switches model mid-session.
func (p *Process) SetModel(model string) error {
	return p.control(map[string]any{"subtype": "set_model", "model": model})
}

// SetEffort changes the effort level mid-session. The CLI has no dedicated
// request for it; effortLevel is applied as a flag setting.
func (p *Process) SetEffort(level string) error {
	return p.control(map[string]any{
		"subtype":  "apply_flag_settings",
		"settings": map[string]any{"effortLevel": level},
	})
}

// Close ends stdin, which lets the CLI exit cleanly.
func (p *Process) Close() error {
	return p.stdin.Close()
}

// sessionVars identify the Claude Code session that launched us. If
// claudewitz runs inside Claude Code (e.g. from its shell), passing them on
// would pin every child to the parent's session.
var sessionVars = map[string]bool{
	"CLAUDECODE":                    true,
	"CLAUDE_CODE_SESSION_ID":        true,
	"CLAUDE_CODE_CHILD_SESSION":     true,
	"CLAUDE_CODE_REMOTE_SESSION_ID": true,
	"CLAUDE_PID":                    true,
}

func childEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !sessionVars[k] {
			out = append(out, kv)
		}
	}
	return out
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
