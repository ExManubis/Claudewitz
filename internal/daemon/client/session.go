package client

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// Session is a subscription to one daemon session. It implements
// claude.Session.
type Session struct {
	c  *Client
	ID string

	mu       sync.Mutex
	cond     *sync.Cond
	queue    []claude.Event
	ended    bool
	seen     map[string]bool // permission requests already delivered
	answered map[string]bool // permission requests this client answered
	lastSeq  int64
	events   chan claude.Event
}

var _ claude.Session = (*Session)(nil)

func newSession(c *Client, id string) *Session {
	s := &Session{
		c:        c,
		ID:       id,
		seen:     map[string]bool{},
		answered: map[string]bool{},
		events:   make(chan claude.Event, 64),
	}
	s.cond = sync.NewCond(&s.mu)
	go s.pump()
	return s
}

// pump moves queued events to the channel, so a slow reader never stalls
// the connection's read loop.
func (s *Session) pump() {
	defer close(s.events)
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.ended {
			s.cond.Wait()
		}
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		ev := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		s.events <- ev
		if ev.Exited {
			return
		}
	}
}

func (s *Session) enqueue(ev claude.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.queue = append(s.queue, ev)
	if ev.Exited {
		s.ended = true
	}
	s.cond.Signal()
}

// end delivers the final Exited event.
func (s *Session) end(err error) {
	s.enqueue(claude.Event{Exited: true, Err: err})
}

// LastSeq is the seq of the latest event received, for resubscribing.
func (s *Session) LastSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeq
}

func (s *Session) noteSeq(seq int64) {
	s.mu.Lock()
	if seq > s.lastSeq {
		s.lastSeq = seq
	}
	s.mu.Unlock()
}

// firstSight reports whether a permission request is new to this client.
func (s *Session) firstSight(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[id] {
		return false
	}
	s.seen[id] = true
	return true
}

func (s *Session) havePending(ps []daemon.Permission) {
	for _, p := range ps {
		if p.Request != nil && s.firstSight(p.RequestID) {
			req := *p.Request
			req.RequestID = p.RequestID
			s.enqueue(claude.Event{Permission: &req})
		}
	}
}

func (s *Session) push(f daemon.Frame) {
	switch f.Event {
	case daemon.EventSessionEvent:
		var p daemon.SessionEventParams
		if json.Unmarshal(f.Params, &p) != nil {
			return
		}
		s.noteSeq(p.Seq)
		if ev, ok := claude.ParseLine(p.Data); ok {
			s.enqueue(ev)
		}
	case daemon.EventPermissionRequested:
		var p daemon.PermissionRequestedParams
		if json.Unmarshal(f.Params, &p) != nil || p.Request == nil {
			return
		}
		s.noteSeq(p.Seq)
		if s.firstSight(p.RequestID) {
			p.Request.RequestID = p.RequestID
			s.enqueue(claude.Event{Permission: p.Request})
		}
	case daemon.EventPermissionResolved:
		var p daemon.PermissionResolvedParams
		if json.Unmarshal(f.Params, &p) != nil {
			return
		}
		s.noteSeq(p.Seq)
		s.mu.Lock()
		mine := s.answered[p.RequestID]
		s.mu.Unlock()
		if !mine {
			s.enqueue(claude.Event{PermissionResolved: p.RequestID})
		}
	}
}

// Events returns the session's events: replay first, then live ones. It
// is closed after the Exited event.
func (s *Session) Events() <-chan claude.Event { return s.events }

func (s *Session) call(method string, params any) error {
	return s.c.Call(context.Background(), method, params, nil)
}

func (s *Session) Send(text string) error { return s.SendWithImages(text, nil) }

func (s *Session) SendWithImages(text string, images []claude.Image) error {
	p := daemon.SendParams{Session: s.ID, Text: text}
	for _, img := range images {
		p.Images = append(p.Images, daemon.Image{MediaType: img.MediaType, Data: img.Data})
	}
	return s.call("session.send", p)
}

func (s *Session) Respond(req *claude.PermissionRequest, d claude.PermissionDecision) error {
	return s.respond(req, d, false)
}

func (s *Session) RespondAndInterrupt(req *claude.PermissionRequest, d claude.PermissionDecision) error {
	return s.respond(req, d, true)
}

func (s *Session) respond(req *claude.PermissionRequest, d claude.PermissionDecision, interrupt bool) error {
	s.mu.Lock()
	s.answered[req.RequestID] = true
	s.mu.Unlock()
	return s.call("session.respond", daemon.RespondParams{
		Session: s.ID, RequestID: req.RequestID, Decision: d, Interrupt: interrupt,
	})
}

func (s *Session) Interrupt() error {
	return s.call("session.interrupt", daemon.SessionParams{Session: s.ID})
}

func (s *Session) SetModel(model string) error {
	return s.call("session.configure", daemon.ConfigureParams{Session: s.ID, Model: model})
}

func (s *Session) SetEffort(level string) error {
	return s.call("session.configure", daemon.ConfigureParams{Session: s.ID, Effort: level})
}

func (s *Session) SetPermissionMode(mode string) error {
	return s.call("session.configure", daemon.ConfigureParams{Session: s.ID, PermissionMode: mode})
}

// stopTimeout bounds Stop, which waits for the daemon to take the request
// so that quitting right after it can't lose it.
const stopTimeout = 2 * time.Second

// Stop ends the session in the daemon. Use Detach to leave it running.
func (s *Session) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	_ = s.c.Call(ctx, "session.stop", daemon.SessionParams{Session: s.ID}, nil)
}

// Detach unsubscribes and ends Events; the session stays live in the
// daemon.
func (s *Session) Detach() error {
	err := s.call("session.unsubscribe", daemon.SessionParams{Session: s.ID})
	if s.c.take(s.ID) == s {
		s.end(nil)
	}
	return err
}
