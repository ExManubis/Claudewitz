package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// fakeProc stands in for a claude process: the test feeds it stream-json
// lines and reads back what clients asked of it.
type fakeProc struct {
	opts   claude.Options
	events chan claude.Event
	mu     sync.Mutex
	calls  []string
	resp   []claude.PermissionDecision
	closed bool
	// lingering makes Stop not exit, like a CLI that takes a while.
	lingering bool
	// sendGate, when set, holds SendWithImages until it is closed, like a
	// CLI that isn't reading stdin.
	sendGate chan struct{}
}

func newFake(opts claude.Options) *fakeProc {
	return &fakeProc{opts: opts, events: make(chan claude.Event, 64)}
}

func (f *fakeProc) line(t *testing.T, s string) {
	t.Helper()
	ev, ok := claude.ParseLine([]byte(s))
	if !ok {
		t.Fatalf("unparsed line %s", s)
	}
	f.events <- ev
}

func (f *fakeProc) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeProc) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeProc) Decisions() []claude.PermissionDecision {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]claude.PermissionDecision(nil), f.resp...)
}

func (f *fakeProc) Events() <-chan claude.Event { return f.events }
func (f *fakeProc) Send(text string) error      { f.record("send " + text); return nil }
func (f *fakeProc) SendWithImages(text string, imgs []claude.Image) error {
	if f.sendGate != nil {
		<-f.sendGate
	}
	f.record(fmt.Sprintf("send %s +%d", text, len(imgs)))
	return nil
}
func (f *fakeProc) Respond(req *claude.PermissionRequest, d claude.PermissionDecision) error {
	f.mu.Lock()
	f.resp = append(f.resp, d)
	f.mu.Unlock()
	f.record("respond " + req.RequestID + " " + d.Behavior)
	return nil
}
func (f *fakeProc) RespondAndInterrupt(req *claude.PermissionRequest, d claude.PermissionDecision) error {
	_ = f.Respond(req, d)
	return f.Interrupt()
}
func (f *fakeProc) Interrupt() error                 { f.record("interrupt"); return nil }
func (f *fakeProc) SetModel(m string) error          { f.record("model " + m); return nil }
func (f *fakeProc) SetEffort(e string) error         { f.record("effort " + e); return nil }
func (f *fakeProc) SetPermissionMode(m string) error { f.record("mode " + m); return nil }
func (f *fakeProc) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lingering {
		f.calls = append(f.calls, "stop")
		return
	}
	if !f.closed {
		f.closed = true
		f.events <- claude.Event{Exited: true}
		close(f.events)
	}
}

// exit ends the process with an error.
func (f *fakeProc) exit(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.events <- claude.Event{Exited: true, Err: err}
	close(f.events)
}

type harness struct {
	sock  string
	procs chan *fakeProc
	// prepare, if set, adjusts each fake before it is handed out.
	prepare func(*fakeProc)
}

func startDaemon(t *testing.T) *harness {
	t.Helper()
	// Unix socket paths are limited to ~104 bytes; t.TempDir is too long on macOS.
	dir, err := os.MkdirTemp("/tmp", "cwd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	h := &harness{sock: filepath.Join(dir, "d.sock"), procs: make(chan *fakeProc, 8)}
	ln, err := daemon.Listen(h.sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &daemon.Server{
		Version: "test",
		Log:     log.New(io.Discard, "", 0),
		Launch: func(_ context.Context, opts claude.Options) (claude.Session, error) {
			f := newFake(opts)
			if h.prepare != nil {
				h.prepare(f)
			}
			h.procs <- f
			return f, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return h
}

func (h *harness) dial(t *testing.T, client string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), h.sock, client, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func (h *harness) proc(t *testing.T) *fakeProc {
	t.Helper()
	select {
	case f := <-h.procs:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no process started")
		return nil
	}
}

// next waits for an event matching ok, skipping others.
func next(t *testing.T, s *Session, what string, ok func(claude.Event) bool) claude.Event {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, open := <-s.Events():
			if !open {
				t.Fatalf("events closed waiting for %s", what)
			}
			if ok(ev) {
				return ev
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// eventually polls cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func summary(t *testing.T, c *Client, id string) daemon.SessionSummary {
	t.Helper()
	list, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %s not live", id)
	return daemon.SessionSummary{}
}

const (
	initLine      = `{"type":"system","subtype":"init","cwd":"/w","session_id":"cs-1","model":"claude-opus-5-5","permissionMode":"default","tools":["Bash"]}`
	replayLine    = `{"type":"user","isReplay":true,"message":{"role":"user","content":"run the tests\nplease"}}`
	assistantLine = `{"type":"assistant","parent_tool_use_id":null,"message":{"id":"m1","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Running them."},{"type":"tool_use","id":"tu1","name":"Grep","input":{"pattern":"claimAmount","path":"src/"}}],"usage":{"input_tokens":10,"cache_read_input_tokens":40000,"cache_creation_input_tokens":1200,"output_tokens":5}}}`
	resultLine    = `{"type":"result","subtype":"success","total_cost_usd":0.25,"session_id":"cs-1","modelUsage":{"claude-opus-5-5[1m]":{"contextWindow":1000000}}}`
	bashRequest   = `{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"go test ./..."},"tool_use_id":"tu2"}}`
)

func startSession(t *testing.T, h *harness, c *Client) (*Session, *fakeProc) {
	t.Helper()
	ctx := context.Background()
	s, err := Starter(c)(ctx, claude.Options{CWD: t.TempDir(), Model: "opus", Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	return s.(*Session), h.proc(t)
}

func TestHelloMustComeFirst(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	if c.Hello.Protocol != daemon.Protocol || c.Hello.DaemonVersion != "test" {
		t.Fatalf("hello: %+v", c.Hello)
	}
	// A second hello is refused.
	var e *daemon.Error
	if err := c.Call(context.Background(), "hello", daemon.HelloParams{Protocol: 1}, nil); !errors.As(err, &e) || e.Code != daemon.CodeProtocol {
		t.Fatalf("second hello: %v", err)
	}
	if _, err := Dial(context.Background(), h.sock, "tui", "test"); err != nil {
		t.Fatal(err)
	}
}

func TestWrongProtocolIsRefused(t *testing.T) {
	h := startDaemon(t)
	c2, err := dial(context.Background(), h.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	var e *daemon.Error
	if err := c2.Call(context.Background(), "hello", daemon.HelloParams{Protocol: 99}, nil); !errors.As(err, &e) || e.Code != daemon.CodeProtocol {
		t.Fatalf("protocol 99: %v", err)
	}
	select {
	case <-c2.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("connection not closed")
	}
}

func TestRequestBeforeHelloClosesConnection(t *testing.T) {
	h := startDaemon(t)
	c, err := dial(context.Background(), h.sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var e *daemon.Error
	if _, err := c.List(context.Background()); !errors.As(err, &e) || e.Code != daemon.CodeProtocol {
		t.Fatalf("list before hello: %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("connection not closed")
	}
}

func TestSessionStreamAndSummary(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	if f.opts.Model != "opus" || f.opts.Effort != "high" || f.opts.CWD == "" {
		t.Fatalf("launch opts: %+v", f.opts)
	}
	// The CLI gets the client's environment, not the daemon's.
	if len(f.opts.Env) == 0 || len(f.opts.Env) != len(os.Environ()) {
		t.Fatalf("env: %d vars, want the client's %d", len(f.opts.Env), len(os.Environ()))
	}
	if sum := summary(t, c, s.ID); sum.State != daemon.StateStarting || sum.Clients != 1 {
		t.Fatalf("new session: %+v", sum)
	}

	f.line(t, initLine)
	next(t, s, "init", func(ev claude.Event) bool { return ev.Init != nil && ev.Init.SessionID == "cs-1" })
	if err := s.Send("run the tests"); err != nil {
		t.Fatal(err)
	}
	f.line(t, replayLine)
	f.line(t, assistantLine)
	next(t, s, "assistant", func(ev claude.Event) bool { return ev.Message != nil && ev.Message.Message.Role == "assistant" })
	sum := summary(t, c, s.ID)
	if sum.State != daemon.StateWorking || sum.Title != "run the tests" {
		t.Fatalf("working: %+v", sum)
	}
	if sum.Activity == nil || sum.Activity.Kind != "tool" || sum.Activity.Text != `Grep claimAmount in src/` {
		t.Fatalf("activity: %+v", sum.Activity)
	}
	if sum.ContextUsed != 41210 || sum.ClaudeSessionID == nil || *sum.ClaudeSessionID != "cs-1" {
		t.Fatalf("context/session: %+v", sum)
	}

	f.line(t, resultLine)
	next(t, s, "result", func(ev claude.Event) bool { return ev.Result != nil })
	sum = summary(t, c, s.ID)
	if sum.State != daemon.StateIdle || sum.LastTurnEndedAt == nil || sum.CostUSD != 0.25 || sum.ContextWindow != 1000000 {
		t.Fatalf("after result: %+v", sum)
	}
	if got := f.Calls(); len(got) != 1 || got[0] != "send run the tests +0" {
		t.Fatalf("calls: %q", got)
	}

	// Times go out at whole seconds, which Swift's .iso8601 needs.
	var raw map[string]any
	b, _ := json.Marshal(sum)
	_ = json.Unmarshal(b, &raw)
	for _, k := range []string{"lastActivity", "lastTurnEndedAt"} {
		if ts, _ := raw[k].(string); ts == "" || strings.Contains(ts, ".") {
			t.Fatalf("%s = %q", k, ts)
		}
	}
}

func TestConfigureAndInterrupt(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	for _, err := range []error{s.SetModel("sonnet"), s.SetEffort("low"), s.SetPermissionMode("plan"), s.Interrupt()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"model sonnet", "effort low", "mode plan", "interrupt"}
	if got := f.Calls(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("calls %q, want %q", got, want)
	}
	sum := summary(t, c, s.ID)
	if sum.Model != "sonnet" || sum.Effort != "low" || sum.PermissionMode != "plan" {
		t.Fatalf("summary: %+v", sum)
	}
}

func TestPermissionFirstAnswerWins(t *testing.T) {
	h := startDaemon(t)
	tui := h.dial(t, "tui")
	s, f := startSession(t, h, tui)
	phone := h.dial(t, "ios")
	ps, _, err := phone.Subscribe(context.Background(), s.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	f.line(t, bashRequest)
	ev := next(t, s, "permission", func(ev claude.Event) bool { return ev.Permission != nil })
	if ev.Permission.RequestID != "r1" || ev.Permission.ToolName != "Bash" {
		t.Fatalf("permission: %+v", ev.Permission)
	}
	next(t, ps, "phone permission", func(ev claude.Event) bool { return ev.Permission != nil })
	sum := summary(t, tui, s.ID)
	if sum.State != daemon.StateWaiting || sum.Waiting == nil || sum.Waiting.Tool != "Bash" || sum.Waiting.Summary != "go test ./..." || sum.Clients != 2 {
		t.Fatalf("waiting: %+v %+v", sum, sum.Waiting)
	}

	// The phone answers; the TUI is told to close its dialog.
	if err := ps.Respond(ev.Permission, claude.PermissionDecision{Behavior: "allow"}); err != nil {
		t.Fatal(err)
	}
	next(t, s, "resolved", func(ev claude.Event) bool { return ev.PermissionResolved == "r1" })
	// A late answer from the TUI loses.
	var e *daemon.Error
	if err := s.Respond(ev.Permission, claude.PermissionDecision{Behavior: "deny"}); !errors.As(err, &e) || e.Code != daemon.CodeNotFound {
		t.Fatalf("second answer: %v", err)
	}
	if got := f.Calls(); fmt.Sprint(got) != "[respond r1 allow]" {
		t.Fatalf("calls: %q", got)
	}
	if sum := summary(t, tui, s.ID); sum.State == daemon.StateWaiting || sum.Waiting != nil {
		t.Fatalf("still waiting: %+v", sum)
	}
}

func TestAlwaysForSession(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)

	f.line(t, bashRequest)
	ev := next(t, s, "permission", func(ev claude.Event) bool { return ev.Permission != nil })
	if err := s.Respond(ev.Permission, claude.PermissionDecision{Behavior: "allow", AlwaysForSession: true}); err != nil {
		t.Fatal(err)
	}
	// The next Bash request is allowed by the daemon without asking.
	f.line(t, strings.Replace(bashRequest, `"r1"`, `"r2"`, 1))
	eventually(t, "auto-allow", func() bool { return len(f.Calls()) == 2 })
	if got := f.Calls(); fmt.Sprint(got) != "[respond r1 allow respond r2 allow]" {
		t.Fatalf("calls: %q", got)
	}

	// Always-allowing an edit switches to acceptEdits instead.
	f.line(t, `{"type":"control_request","request_id":"r3","request":{"subtype":"can_use_tool","tool_name":"Edit","input":{"file_path":"/w/a.go"}}}`)
	ev = next(t, s, "edit permission", func(ev claude.Event) bool { return ev.Permission != nil && ev.Permission.RequestID == "r3" })
	if err := s.Respond(ev.Permission, claude.PermissionDecision{Behavior: "allow", AlwaysForSession: true}); err != nil {
		t.Fatal(err)
	}
	d := f.Decisions()[2]
	if len(d.UpdatedPermissions) != 1 || d.UpdatedPermissions[0].Mode != "acceptEdits" {
		t.Fatalf("edit decision: %+v", d)
	}
	if sum := summary(t, c, s.ID); sum.PermissionMode != "acceptEdits" {
		t.Fatalf("mode: %s", sum.PermissionMode)
	}

	// Questions always reach a client, rule or not.
	f.line(t, `{"type":"control_request","request_id":"r4","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[]}}}`)
	next(t, s, "question", func(ev claude.Event) bool { return ev.Permission != nil && ev.Permission.RequestID == "r4" })
}

func TestDenyAndInterrupt(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	f.line(t, bashRequest)
	ev := next(t, s, "permission", func(ev claude.Event) bool { return ev.Permission != nil })
	if err := s.RespondAndInterrupt(ev.Permission, claude.PermissionDecision{Behavior: "deny", Message: "no"}); err != nil {
		t.Fatal(err)
	}
	if got := f.Calls(); fmt.Sprint(got) != "[respond r1 deny interrupt]" {
		t.Fatalf("calls: %q", got)
	}
}

func TestSubscribeReplaysSince(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	f.line(t, initLine)
	f.line(t, replayLine)
	f.line(t, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Run"}},"parent_tool_use_id":null}`)
	f.line(t, assistantLine)
	f.line(t, resultLine)
	next(t, s, "result", func(ev claude.Event) bool { return ev.Result != nil })
	mid := s.LastSeq() // 5
	f.line(t, bashRequest)
	next(t, s, "permission", func(ev claude.Event) bool { return ev.Permission != nil })

	// A full replay has the init, prompt, finished message and result, but
	// not the delta, and the pending request.
	other := h.dial(t, "ios")
	full, res, err := other.Subscribe(context.Background(), s.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The init is kept for late subscribers but doesn't count as the start.
	if res.FromSeq != 2 || len(res.Pending) != 1 || res.Pending[0].RequestID != "r1" {
		t.Fatalf("subscribe: %+v", res)
	}
	var kinds []string
	for len(kinds) < 5 {
		ev := next(t, full, "replay", func(claude.Event) bool { return true })
		switch {
		case ev.Init != nil:
			kinds = append(kinds, "init")
		case ev.Message != nil:
			kinds = append(kinds, ev.Message.Message.Role)
		case ev.Stream != nil:
			kinds = append(kinds, "delta")
		case ev.Result != nil:
			kinds = append(kinds, "result")
		case ev.Permission != nil:
			kinds = append(kinds, "permission")
		}
	}
	// The pending request is delivered once, from the subscribe result or
	// the replay, whichever lands first.
	sort.Strings(kinds)
	if want := "[assistant init permission result user]"; fmt.Sprint(kinds) != want {
		t.Fatalf("replay %v, want %s", kinds, want)
	}

	// Resubscribing since mid replays only the permission request.
	third := h.dial(t, "ios")
	_, res, err = third.Subscribe(context.Background(), s.ID, &mid)
	if err != nil {
		t.Fatal(err)
	}
	if res.FromSeq != mid+1 {
		t.Fatalf("fromSeq %d, want %d", res.FromSeq, mid+1)
	}
}

func TestOverviewPushes(t *testing.T) {
	h := startDaemon(t)
	watcher := h.dial(t, "ios")
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	f.line(t, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.17,"resetsAt":1760529120}}}}`)
	f.line(t, `{"type":"control_response","response":{"subtype":"success","request_id":"initialize-1","response":{"account":{"email":"m@example.com","subscriptionType":"max"}}}}`)

	var updated, account, closed bool
	timeout := time.After(2 * time.Second)
	s.Stop()
	for !(updated && account && closed) {
		select {
		case fr := <-watcher.Overview():
			switch fr.Event {
			case daemon.EventSessionUpdated:
				var p daemon.SessionUpdatedParams
				_ = json.Unmarshal(fr.Params, &p)
				updated = updated || p.Summary.ID == s.ID
			case daemon.EventAccountUpdated:
				var p daemon.AccountUpdatedParams
				_ = json.Unmarshal(fr.Params, &p)
				account = account || (p.Account.Usage != nil && p.Account.Usage.FiveHour != nil && p.Account.Email == "m@example.com")
			case daemon.EventSessionClosed:
				closed = true
			}
		case <-timeout:
			t.Fatalf("updated=%v account=%v closed=%v", updated, account, closed)
		}
	}
	next(t, s, "exit", func(ev claude.Event) bool { return ev.Exited && ev.Err == nil })
	if got := f.Calls(); len(got) != 0 {
		t.Fatalf("calls: %q", got)
	}
	// A new client's hello carries the account.
	if c3 := h.dial(t, "tui"); c3.Hello.Account.Email != "m@example.com" || c3.Hello.Account.Usage == nil {
		t.Fatalf("hello account: %+v", c3.Hello.Account)
	}
}

func TestExitWithError(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	f.exit(errors.New("exit status 1: not logged in"))
	ev := next(t, s, "exit", func(ev claude.Event) bool { return ev.Exited })
	if ev.Err == nil || !strings.Contains(ev.Err.Error(), "not logged in") {
		t.Fatalf("exit err: %v", ev.Err)
	}
	if list, _ := c.List(context.Background()); len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
}

func TestResumeReattachesToLiveSession(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	f.line(t, initLine)
	next(t, s, "init", func(ev claude.Event) bool { return ev.Init != nil })
	f.line(t, replayLine)
	f.line(t, bashRequest)
	next(t, s, "permission", func(ev claude.Event) bool { return ev.Permission != nil })

	// Another TUI resuming it attaches: no second process, no replay but
	// the init, and the pending request.
	other := h.dial(t, "tui")
	got, err := Starter(other)(context.Background(), claude.Options{CWD: f.opts.CWD, Resume: "cs-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.(*Session).ID != s.ID {
		t.Fatalf("resume gave %s, want %s", got.(*Session).ID, s.ID)
	}
	select {
	case <-h.procs:
		t.Fatal("a second process was started")
	default:
	}
	var kinds []string
	for len(kinds) < 2 {
		ev := next(t, got.(*Session), "attach", func(claude.Event) bool { return true })
		switch {
		case ev.Init != nil:
			kinds = append(kinds, "init")
		case ev.Permission != nil:
			kinds = append(kinds, "permission")
		default:
			kinds = append(kinds, "other")
		}
	}
	sort.Strings(kinds)
	if fmt.Sprint(kinds) != "[init permission]" {
		t.Fatalf("attach delivered %v", kinds)
	}
}

func TestDetachLeavesSessionRunning(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, _ := startSession(t, h, c)
	if err := s.Detach(); err != nil {
		t.Fatal(err)
	}
	next(t, s, "detached", func(ev claude.Event) bool { return ev.Exited })
	c.Close()
	c2 := h.dial(t, "tui")
	if sum := summary(t, c2, s.ID); sum.Clients != 0 || sum.State == daemon.StateExited {
		t.Fatalf("after detach: %+v", sum)
	}
}

func TestStartRejectsRelativeCWD(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	var e *daemon.Error
	if _, err := c.Start(context.Background(), daemon.StartParams{CWD: "rel"}); !errors.As(err, &e) || e.Code != daemon.CodeBadRequest {
		t.Fatalf("relative cwd: %v", err)
	}
}

// A turn that ends with a request still open (an interrupt while it was
// shown) takes the request with it; a sub-agent's request stays.
func TestTurnEndClearsPending(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	f.line(t, bashRequest)
	f.line(t, `{"type":"control_request","request_id":"r9","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"},"agent_id":"a1"}}`)
	ev := next(t, s, "permission", func(ev claude.Event) bool { return ev.Permission != nil && ev.Permission.RequestID == "r1" })
	next(t, s, "agent permission", func(ev claude.Event) bool { return ev.Permission != nil && ev.Permission.RequestID == "r9" })
	if err := s.Interrupt(); err != nil {
		t.Fatal(err)
	}
	f.line(t, resultLine)
	next(t, s, "resolved", func(ev claude.Event) bool { return ev.PermissionResolved == "r1" })
	sum := summary(t, c, s.ID)
	if sum.Waiting == nil || sum.Waiting.RequestID != "r9" {
		t.Fatalf("waiting: %+v", sum.Waiting)
	}
	var e *daemon.Error
	if err := s.Respond(ev.Permission, claude.PermissionDecision{Behavior: "allow"}); !errors.As(err, &e) || e.Code != daemon.CodeNotFound {
		t.Fatalf("answer after turn end: %v", err)
	}
	if got := f.Calls(); fmt.Sprint(got) != "[interrupt]" {
		t.Fatalf("calls: %q", got)
	}
}

// A replay bigger than a client's outbox is paced, not cut off.
func TestLongReplayIsPaced(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	const n = 9000 // more than the daemon's outbox; one turn, so none evicted
	go func() {
		for i := 0; i < n; i++ {
			f.line(t, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}},"parent_tool_use_id":null}`)
		}
		f.line(t, initLine)
	}()
	for wait := time.After(15 * time.Second); ; {
		select {
		case ev := <-s.Events():
			if ev.Exited {
				t.Fatalf("live client dropped: %v", ev.Err)
			}
		case <-wait:
			t.Fatal("timed out feeding events")
		}
		if s.LastSeq() >= n+1 {
			break
		}
	}

	late := h.dial(t, "ios")
	ls, _, err := late.Subscribe(context.Background(), s.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	timeout := time.After(15 * time.Second)
	for got < n {
		select {
		case ev, ok := <-ls.Events():
			if !ok || ev.Exited {
				t.Fatalf("dropped after %d events", got)
			}
			if ev.Stream != nil {
				got++
			}
		case <-timeout:
			t.Fatalf("replayed %d of %d", got, n)
		}
	}
	// And live events still follow the replay.
	f.line(t, resultLine)
	next(t, ls, "live result", func(ev claude.Event) bool { return ev.Result != nil })
}

// A write the CLI isn't reading doesn't stop the daemon from reading the
// CLI's output or answering other requests.
func TestBlockedWriteDoesNotStallSession(t *testing.T) {
	h := startDaemon(t)
	gate := make(chan struct{})
	h.prepare = func(f *fakeProc) { f.sendGate = gate }
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	sent := make(chan error, 1)
	go func() { sent <- s.Send("big image") }()
	time.Sleep(50 * time.Millisecond)

	other := h.dial(t, "ios")
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ { // more than the process's event buffer
			f.line(t, `{"type":"stream_event","event":{"type":"ping"},"parent_tool_use_id":null}`)
		}
		_, _ = other.List(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the session stalled behind a blocked write")
	}
	close(gate)
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}

// Resuming a session that is being stopped starts a new process rather
// than handing back the dying one.
func TestResumeSkipsStoppingSession(t *testing.T) {
	h := startDaemon(t)
	h.prepare = func(f *fakeProc) { f.lingering = true }
	c := h.dial(t, "tui")
	s, f := startSession(t, h, c)
	f.line(t, initLine)
	next(t, s, "init", func(ev claude.Event) bool { return ev.Init != nil })
	s.Stop()
	r, err := c.Start(context.Background(), daemon.StartParams{CWD: f.opts.CWD, Resume: "cs-1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Attached || r.Session.ID == s.ID {
		t.Fatalf("attached to the stopping session: %+v", r)
	}
	if f2 := h.proc(t); f2.opts.Resume != "cs-1" {
		t.Fatalf("new process opts: %+v", f2.opts)
	}
}
