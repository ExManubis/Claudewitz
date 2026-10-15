package daemon

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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
	subs    map[*conn]bool

	pushed   SessionSummary // last summary pushed to clients
	pushedAt time.Time
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
		subs:  map[*conn]bool{},
	}
}

// run reads the process's events until it exits.
func (s *session) run() {
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
	for c := range s.subs {
		c.send(frame)
	}
}

func (s *session) permission(req *claude.PermissionRequest) {
	if s.rules[req.ToolName] && !interactiveTools[req.ToolName] {
		if err := s.proc.Respond(req, claude.PermissionDecision{Behavior: "allow"}); err != nil {
			s.srv.logf("session %s: auto-allow %s: %v", s.id, req.ToolName, err)
		}
		return
	}
	s.pending = append(s.pending, &pendingRequest{req: req, since: now()})
	s.emit(EventPermissionRequested, func(seq int64) any {
		return PermissionRequestedParams{Session: s.id, Seq: seq, RequestID: req.RequestID, Request: req}
	})
}

// respond answers a pending request on behalf of client by.
func (s *session) respond(p RespondParams, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.pendingIndex(p.RequestID)
	if i < 0 {
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
	var err error
	if p.Interrupt {
		err = s.proc.RespondAndInterrupt(req, d)
	} else {
		err = s.proc.Respond(req, d)
	}
	if err != nil {
		return &Error{CodeFailed, err.Error()}
	}
	s.resolve(i, by)
	// The new rule answers anything else already waiting on that tool.
	if rule != "" {
		for j := 0; j < len(s.pending); {
			if r := s.pending[j].req; r.ToolName == rule {
				_ = s.proc.Respond(r, claude.PermissionDecision{Behavior: "allow"})
				s.resolve(j, "rule")
				continue
			}
			j++
		}
	}
	s.changed()
	return nil
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
			Summary:   summarize(p.req.ToolName, p.req.Input, 120),
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
func (s *session) changed() {
	sum := s.summary()
	a, b := sum, s.pushed
	a.LastActivity, b.LastActivity = time.Time{}, time.Time{}
	if summaryEqual(a, b) && time.Since(s.pushedAt) < activityPushEvery {
		return
	}
	if summaryEqual(sum, s.pushed) {
		return
	}
	s.pushed, s.pushedAt = sum, time.Now()
	s.srv.broadcast(push(EventSessionUpdated, SessionUpdatedParams{Summary: sum}))
}

func summaryEqual(a, b SessionSummary) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// send submits a prompt from a client.
func (s *session) send(text string, images []Image) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	imgs := make([]claude.Image, len(images))
	for i, img := range images {
		imgs[i] = claude.Image{MediaType: img.MediaType, Data: img.Data}
	}
	if err := s.proc.SendWithImages(text, imgs); err != nil {
		return &Error{CodeFailed, err.Error()}
	}
	s.busy = true
	if s.sum.Title == "" {
		s.sum.Title = firstLine(text, 80)
	}
	s.sum.LastActivity = now()
	s.changed()
	return nil
}

func (s *session) configure(p ConfigureParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Model != "" {
		if err := s.proc.SetModel(p.Model); err != nil {
			return &Error{CodeFailed, err.Error()}
		}
		s.sum.Model = p.Model
	}
	if p.Effort != "" {
		if err := s.proc.SetEffort(p.Effort); err != nil {
			return &Error{CodeFailed, err.Error()}
		}
		s.sum.Effort = p.Effort
	}
	if p.PermissionMode != "" {
		if err := s.proc.SetPermissionMode(p.PermissionMode); err != nil {
			return &Error{CodeFailed, err.Error()}
		}
		s.sum.PermissionMode = p.PermissionMode
	}
	s.changed()
	return nil
}

// subscribe replies to c and replays the buffer under one lock, so nothing
// lands between the replay and the live stream.
func (s *session) subscribe(c *conn, id int64, since *int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	frames, from := s.buf.since(since, s.seq+1)
	s.subs[c] = true
	res := SubscribeResult{Summary: s.summary(), Pending: []Permission{}, FromSeq: from}
	for _, p := range s.pending {
		res.Pending = append(res.Pending, Permission{RequestID: p.req.RequestID, Request: p.req})
	}
	c.reply(id, res)
	for _, f := range frames {
		c.send(f)
	}
	s.changed() // clients count
}

func (s *session) unsubscribe(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.subs[c] {
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
			return &Activity{Kind: "tool", Text: strings.TrimSpace(b.Name + " " + summarize(b.Name, b.Input, 100))}
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

// summarize is a tool call's main argument on one line.
func summarize(tool string, raw json.RawMessage, max int) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	var s string
	switch tool {
	case "Bash":
		s = str("command")
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		s = filepath.Base(str("file_path") + str("notebook_path"))
	case "Glob", "Grep":
		s = fmt.Sprintf("%q", str("pattern"))
		if p := str("path"); p != "" {
			s += " in " + p
		}
	case "WebFetch":
		s = str("url")
	case "WebSearch":
		s = str("query")
	case "Task", "Agent":
		s = str("description")
	case "Skill":
		s = str("skill")
	case "AskUserQuestion":
		if qs, err := claude.ParseQuestions(raw); err == nil && len(qs) > 0 {
			s = qs[0].Question
		}
	case "ExitPlanMode":
		s = "plan ready for review"
	default:
		for _, k := range []string{"description", "command", "file_path", "path", "pattern", "query", "url"} {
			if s = str(k); s != "" {
				break
			}
		}
	}
	return firstLine(s, max)
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
