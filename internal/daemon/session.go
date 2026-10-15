package daemon

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// activityPushEvery is how often a summary whose only change is
// lastActivity is pushed again; anything else is pushed at once.
const activityPushEvery = 5 * time.Second

// Interactive tools need an answer rather than a yes/no, so allow rules
// never apply to them.
var interactiveTools = map[string]bool{"AskUserQuestion": true, "ExitPlanMode": true}

// Edit tools are allowed for the session by switching to acceptEdits.
var editTools = map[string]bool{"Edit": true, "MultiEdit": true, "Write": true, "NotebookEdit": true}

// session is one live claude process and everything clients need of it.
type session struct {
	srv  *Server
	id   string
	proc claude.Session

	mu      sync.Mutex
	sum     SessionSummary
	started bool // init or initialize seen
	busy    bool // a turn is running
	seq     int64
	buf     *buffer
	pending []*pendingRequest
	rules   map[string]bool // tools allowed for the rest of the session
	subs    map[*conn]*subscriber
	// stopping is set by session.stop: the process is on its way out, so
	// resuming its transcript starts a new one rather than attaching.
	stopping bool

	pushed   SessionSummary // last summary pushed to clients
	pushedAt time.Time

	w writer
}

// subscriber is one client's subscription. While its replay is being
// sent, live frames are held back so they arrive after it.
type subscriber struct {
	replaying bool
	held      [][]byte
}

type pendingRequest struct {
	req   *claude.PermissionRequest
	since time.Time
}

func newSession(srv *Server, id string, proc claude.Session, sum SessionSummary) *session {
	return &session{
		srv:   srv,
		id:    id,
		proc:  proc,
		sum:   sum,
		buf:   newBuffer(),
		rules: map[string]bool{},
		subs:  map[*conn]*subscriber{},
	}
}

// run reads the process's events until it exits.
func (s *session) run() {
	go s.w.loop()
	defer s.w.close()
	for ev := range s.proc.Events() {
		s.handle(ev)
		if ev.Exited {
			return
		}
	}
	// Closed without an Exited event.
	s.handle(claude.Event{Exited: true})
}

func (s *session) handle(ev claude.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sum.State == StateExited {
		return
	}
	s.sum.LastActivity = now()
	switch {
	case ev.Exited:
		s.exit(ev.Err)
		return
	case ev.Permission != nil:
		s.permission(ev.Permission)
	case ev.Raw != nil:
		s.observe(ev)
		s.passThrough(ev)
	case ev.Err != nil:
		s.srv.logf("session %s: %v", s.id, ev.Err)
	}
	s.changed()
}

// observe updates the summary from an event.
func (s *session) observe(ev claude.Event) {
	switch {
	case ev.Init != nil:
		s.started = true
		if ev.Init.SessionID != "" {
			id := ev.Init.SessionID
			s.sum.ClaudeSessionID = &id
		}
		if ev.Init.Model != "" {
			s.sum.Model = ev.Init.Model
		}
		if ev.Init.PermissionMode != "" {
			s.sum.PermissionMode = ev.Init.PermissionMode
		}
	case ev.Initialize != nil:
		s.started = true
		if ev.Initialize.PermissionMode != "" {
			s.sum.PermissionMode = ev.Initialize.PermissionMode
		}
		s.srv.setAccount(ev.Initialize.Account.Email, ev.Initialize.Account.SubscriptionType)
	case ev.Message != nil:
		m := ev.Message
		if m.ParentToolUseID != nil {
			return // a sub-agent's message
		}
		if m.IsReplay {
			s.busy = true
			if s.sum.Title == "" {
				s.sum.Title = firstLine(m.Message.Text(), 80)
			}
			return
		}
		if m.Message.Role != "assistant" {
			return
		}
		if u := m.Message.Usage; u != nil {
			s.sum.ContextUsed = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
		}
		if a := activityOf(m.Message.Content); a != nil {
			s.sum.Activity = a
		}
	case ev.Result != nil:
		r := ev.Result
		s.busy = false
		// The turn is over, so the CLI no longer waits on its requests (an
		// interrupt, say, while one was open). Sub-agents' requests stay:
		// a background agent can still be waiting after the turn ends.
		for i := 0; i < len(s.pending); {
			if s.pending[i].req.AgentID == "" {
				s.resolve(i, "turn-ended")
				continue
			}
			i++
		}
		t := now()
		s.sum.LastTurnEndedAt = &t
		s.sum.CostUSD += r.TotalCostUSD
		if w := r.ContextWindow(s.sum.Model); w > 0 {
			s.sum.ContextWindow = w
		}
		if r.SessionID != "" {
			id := r.SessionID
			s.sum.ClaudeSessionID = &id
		}
	case ev.ContextWindow > 0:
		s.sum.ContextWindow = ev.ContextWindow
	case ev.PermissionMode != "":
		s.sum.PermissionMode = ev.PermissionMode
	case ev.RateLimit != nil:
		s.srv.setUsage(ev.RateLimit)
	}
}

// passThrough buffers the CLI's line and sends it to subscribers.
func (s *session) passThrough(ev claude.Event) {
	s.seq++
	e := entry{seq: s.seq}
	switch {
	case ev.Init != nil:
		e.sticky = "init"
	case ev.Initialize != nil:
		e.sticky = "initialize"
	case ev.Stream != nil:
		e.stream = true
		if p := ev.Stream.ParentToolUseID; p != nil {
			e.parent = *p
		}
	case ev.Message != nil && ev.Message.Message.Role == "assistant":
		parent := ""
		if p := ev.Message.ParentToolUseID; p != nil {
			parent = *p
		}
		s.buf.dropDeltas(parent)
	}
	e.frame = push(EventSessionEvent, SessionEventParams{Session: s.id, Seq: s.seq, Data: ev.Raw})
	s.buf.add(e)
	if ev.Result != nil {
		s.buf.endTurn(s.seq)
	}
	s.toSubscribers(e.frame)
}

// emit buffers and sends a daemon-made event (permission requests and
// answers), numbered in the same sequence as the CLI's lines.
func (s *session) emit(event string, params func(seq int64) any) {
	s.seq++
	frame := push(event, params(s.seq))
	s.buf.add(entry{seq: s.seq, frame: frame})
	s.toSubscribers(frame)
}

func (s *session) toSubscribers(frame []byte) {
	for c, sub := range s.subs {
		if sub.replaying {
			sub.held = append(sub.held, frame)
		} else {
			c.send(frame)
		}
	}
}

func (s *session) permission(req *claude.PermissionRequest) {
	if s.rules[req.ToolName] && !interactiveTools[req.ToolName] {
		// Queued, not written here: the event loop must never wait on stdin.
		s.w.async(func() error {
			if err := s.proc.Respond(req, claude.PermissionDecision{Behavior: "allow"}); err != nil {
				s.srv.logf("session %s: auto-allow %s: %v", s.id, req.ToolName, err)
			}
			return nil
		})
		return
	}
	s.pending = append(s.pending, &pendingRequest{req: req, since: now()})
	s.emit(EventPermissionRequested, func(seq int64) any {
		return PermissionRequestedParams{Session: s.id, Seq: seq, RequestID: req.RequestID, Request: req}
	})
}

// respond answers a pending request on behalf of client by. The request
// is claimed under s.mu, so the first answer wins; the answer is written
// to the CLI after releasing it.
func (s *session) respond(p RespondParams, by string) error {
	s.mu.Lock()
	i := s.pendingIndex(p.RequestID)
	if i < 0 {
		s.mu.Unlock()
		return &Error{CodeNotFound, fmt.Sprintf("no pending request %s", p.RequestID)}
	}
	req := s.pending[i].req
	d := p.Decision
	rule := ""
	if d.Behavior == "allow" && d.AlwaysForSession && !interactiveTools[req.ToolName] {
		if editTools[req.ToolName] {
			d.UpdatedPermissions = append(d.UpdatedPermissions, claude.SetModeUpdate("acceptEdits"))
			s.sum.PermissionMode = "acceptEdits"
		} else {
			s.rules[req.ToolName] = true
			rule = req.ToolName
		}
	}
	s.resolve(i, by)
	// The new rule answers anything else already waiting on that tool.
	var also []*claude.PermissionRequest
	if rule != "" {
		for j := 0; j < len(s.pending); {
			if r := s.pending[j].req; r.ToolName == rule {
				also = append(also, r)
				s.resolve(j, "rule")
				continue
			}
			j++
		}
	}
	s.changed()
	s.mu.Unlock()

	return wrap(s.w.do(func() error {
		var err error
		if p.Interrupt {
			err = s.proc.RespondAndInterrupt(req, d)
		} else {
			err = s.proc.Respond(req, d)
		}
		for _, r := range also {
			if err == nil {
				err = s.proc.Respond(r, claude.PermissionDecision{Behavior: "allow"})
			}
		}
		return err
	}))
}

func (s *session) pendingIndex(id string) int {
	for i, p := range s.pending {
		if p.req.RequestID == id {
			return i
		}
	}
	return -1
}

func (s *session) resolve(i int, by string) {
	id := s.pending[i].req.RequestID
	s.pending = append(s.pending[:i:i], s.pending[i+1:]...)
	s.emit(EventPermissionResolved, func(seq int64) any {
		return PermissionResolvedParams{Session: s.id, Seq: seq, RequestID: id, By: by}
	})
}

func (s *session) exit(err error) {
	s.busy = false
	s.pending = nil
	s.sum.State = StateExited
	s.sum.Waiting = nil
	if err != nil {
		msg := err.Error()
		s.sum.LastError = &msg
	}
	s.srv.closed(s)
}

// refresh recomputes the derived parts of the summary.
func (s *session) refresh() {
	s.sum.Waiting = nil
	switch {
	case s.sum.State == StateExited:
		return
	case len(s.pending) > 0:
		s.sum.State = StateWaiting
		p := s.pending[0]
		s.sum.Waiting = &Waiting{
			RequestID: p.req.RequestID,
			Tool:      p.req.ToolName,
			Summary:   claude.SummarizeInput(p.req.ToolName, p.req.Input, 120),
			Since:     p.since,
		}
	case s.busy:
		s.sum.State = StateWorking
	case s.started:
		s.sum.State = StateIdle
	default:
		s.sum.State = StateStarting
	}
	s.sum.Clients = len(s.subs)
}

// summary returns the current summary. Callers hold s.mu.
func (s *session) summary() SessionSummary {
	s.refresh()
	return s.sum
}

// changed pushes the summary to every client if it changed: at once for
// real changes, at most every activityPushEvery for lastActivity alone.
// It runs on every event, stream deltas included, so it compares fields
// rather than encoding anything.
func (s *session) changed() {
	sum := s.summary()
	if sameSummary(sum, s.pushed) &&
		(sum.LastActivity.Equal(s.pushed.LastActivity) || time.Since(s.pushedAt) < activityPushEvery) {
		return
	}
	s.pushed, s.pushedAt = sum, time.Now()
	s.srv.broadcast(push(EventSessionUpdated, SessionUpdatedParams{Summary: sum}))
}

// sameSummary compares everything but LastActivity.
func sameSummary(a, b SessionSummary) bool {
	if !ptrEqual(a.ClaudeSessionID, b.ClaudeSessionID) || !ptrEqual(a.Waiting, b.Waiting) ||
		!ptrEqual(a.Activity, b.Activity) || !ptrEqual(a.LastTurnEndedAt, b.LastTurnEndedAt) ||
		!ptrEqual(a.LastError, b.LastError) {
		return false
	}
	a.ClaudeSessionID, a.Waiting, a.Activity, a.LastTurnEndedAt, a.LastError = nil, nil, nil, nil, nil
	b.ClaudeSessionID, b.Waiting, b.Activity, b.LastTurnEndedAt, b.LastError = nil, nil, nil, nil, nil
	a.LastActivity, b.LastActivity = time.Time{}, time.Time{}
	return a == b
}

func ptrEqual[T comparable](a, b *T) bool {
	return a == b || a != nil && b != nil && *a == *b
}

// send submits a prompt from a client.
func (s *session) send(text string, images []Image) error {
	imgs := make([]claude.Image, len(images))
	for i, img := range images {
		imgs[i] = claude.Image{MediaType: img.MediaType, Data: img.Data}
	}
	if err := s.w.do(func() error { return s.proc.SendWithImages(text, imgs) }); err != nil {
		return wrap(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = true
	if s.sum.Title == "" {
		s.sum.Title = firstLine(text, 80)
	}
	s.sum.LastActivity = now()
	s.changed()
	return nil
}

func (s *session) configure(p ConfigureParams) error {
	err := s.w.do(func() error {
		if p.Model != "" {
			if err := s.proc.SetModel(p.Model); err != nil {
				return err
			}
		}
		if p.Effort != "" {
			if err := s.proc.SetEffort(p.Effort); err != nil {
				return err
			}
		}
		if p.PermissionMode != "" {
			return s.proc.SetPermissionMode(p.PermissionMode)
		}
		return nil
	})
	if err != nil {
		return wrap(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Model != "" {
		s.sum.Model = p.Model
	}
	if p.Effort != "" {
		s.sum.Effort = p.Effort
	}
	if p.PermissionMode != "" {
		s.sum.PermissionMode = p.PermissionMode
	}
	s.changed()
	return nil
}

func (s *session) interrupt() error {
	return wrap(s.w.do(s.proc.Interrupt))
}

// stop ends the process; it may take a moment to exit.
func (s *session) stop() {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.proc.Stop()
}

// subscribe replies to c, then replays the buffer. The replay waits for
// room in c's outbox rather than overflowing it (a long turn can hold more
// events than the outbox), and live events that arrive meanwhile are held
// and sent after it, so c sees them in order.
func (s *session) subscribe(c *conn, id int64, since *int64, live bool) {
	s.mu.Lock()
	frames, from := s.buf.since(since, s.seq+1)
	if live {
		frames, from = s.buf.sticky(), s.seq+1
	}
	sub := &subscriber{replaying: len(frames) > 0}
	s.subs[c] = sub
	res := SubscribeResult{Summary: s.summary(), Pending: []Permission{}, FromSeq: from, LiveSeq: s.seq + 1}
	for _, p := range s.pending {
		res.Pending = append(res.Pending, Permission{RequestID: p.req.RequestID, Request: p.req})
	}
	c.reply(id, res)
	s.changed() // clients count
	s.mu.Unlock()
	if !sub.replaying {
		return
	}

	for _, f := range frames {
		if !c.sendWait(f) {
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range sub.held {
		c.send(f)
	}
	sub.replaying, sub.held = false, nil
}

func (s *session) unsubscribe(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs[c] == nil {
		return
	}
	delete(s.subs, c)
	if s.sum.State != StateExited {
		s.changed()
	}
}

// activityOf picks the overview line from an assistant message: its last
// tool call, text or thinking.
func activityOf(blocks []claude.ContentBlock) *Activity {
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		switch b.Type {
		case "tool_use":
			return &Activity{Kind: "tool", Text: strings.TrimSpace(b.Name + " " + claude.SummarizeInput(b.Name, b.Input, 100))}
		case "text":
			if t := firstLine(b.Text, 120); t != "" {
				return &Activity{Kind: "text", Text: t}
			}
		case "thinking":
			if t := firstLine(b.Thinking, 120); t != "" {
				return &Activity{Kind: "thinking", Text: t}
			}
		}
	}
	return nil
}

// firstLine is s's first non-blank line, cut to max runes.
func firstLine(s string, max int) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > max {
				return string(r[:max-1]) + "…"
			}
			return l
		}
	}
	return ""
}
