package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/sessions"
)

// Launcher starts a claude session; claude.Launch outside tests.
type Launcher func(ctx context.Context, opts claude.Options) (claude.Session, error)

// Server owns the live sessions and the connected clients.
type Server struct {
	// Version is reported in hello.
	Version string
	// Base is applied to every session.start (the claude binary, mostly).
	Base claude.Options
	// Launch starts processes; nil means claude.Launch.
	Launch Launcher
	// IdleTimeout ends Serve after this long with no clients and no live
	// sessions; 0 never idles out.
	IdleTimeout time.Duration
	// Log receives diagnostics; nil means the standard logger.
	Log *log.Logger

	ctx      context.Context // outlives requests; ends the processes
	mu       sync.RWMutex
	sessions map[string]*session
	order    []string // session ids by start
	nextID   int
	conns    map[*conn]bool
	account  Account
	idleFrom time.Time
}

// Serve accepts clients on ln until ctx ends or the daemon idles out.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.ctx = ctx
	s.sessions = map[string]*session{}
	s.conns = map[*conn]bool{}
	s.idleFrom = time.Now()
	s.mu.Unlock()
	if s.Launch == nil {
		s.Launch = claude.Launch
	}

	hs := &http.Server{Handler: http.HandlerFunc(s.serveWS)}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()

	tick := time.NewTicker(idleCheck(s.IdleTimeout))
	defer tick.Stop()
	for {
		select {
		case err := <-errc:
			return err
		case <-ctx.Done():
			hs.Close()
			return nil
		case <-tick.C:
			if s.idle() {
				s.logf("idle for %s, exiting", s.IdleTimeout)
				hs.Close()
				return nil
			}
		}
	}
}

func idleCheck(d time.Duration) time.Duration {
	if d <= 0 || d > 30*time.Second {
		return 30 * time.Second
	}
	return d / 4
}

func (s *Server) idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) > 0 || len(s.sessions) > 0 {
		s.idleFrom = time.Now()
		return false
	}
	return s.IdleTimeout > 0 && time.Since(s.idleFrom) >= s.IdleTimeout
}

// Listen opens the local unix socket at path, readable by this user only.
// A socket left behind by a daemon that died is removed; one that answers
// means a daemon is already running.
func Listen(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Anyone who can connect gets a shell (Bash, as this user), so the
	// folder is made private even if it already existed with looser
	// permissions, and the socket is created private (see listenPrivate)
	// rather than tightened after the fact.
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("a daemon is already listening on %s", path)
		}
		_ = os.Remove(path)
	}
	return listenPrivate(path)
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

var upgrader = websocket.Upgrader{
	// The local listener is a unix socket, which browsers can't reach.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 20) // images arrive inline
	c := newConn(s, ws)
	s.mu.Lock()
	s.conns[c] = true
	s.mu.Unlock()
	go c.writeLoop()
	c.readLoop()
	c.close()
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	for _, ss := range c.subscriptions() {
		ss.unsubscribe(c)
	}
}

// broadcast sends a push to every client that has said hello.
func (s *Server) broadcast(frame []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.conns {
		if c.ready() {
			c.send(frame)
		}
	}
}

func (s *Server) lookup(id string) (*session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ss, ok := s.sessions[id]; ok {
		return ss, nil
	}
	return nil, &Error{CodeNotFound, "no session " + id}
}

// closed drops an exited session and tells every client.
func (s *Server) closed(ss *session) {
	s.mu.Lock()
	delete(s.sessions, ss.id)
	for i, id := range s.order {
		if id == ss.id {
			s.order = append(s.order[:i:i], s.order[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	s.broadcast(push(EventSessionClosed, SessionClosedParams{Session: ss.id, LastError: ss.sum.LastError}))
}

func (s *Server) setAccount(email, sub string) {
	s.mu.Lock()
	changed := email != "" && (email != s.account.Email || sub != s.account.SubscriptionType)
	if changed {
		s.account.Email, s.account.SubscriptionType = email, sub
	}
	acc := s.account
	s.mu.Unlock()
	if changed {
		s.broadcast(push(EventAccountUpdated, AccountUpdatedParams{Account: acc}))
	}
}

func (s *Server) setUsage(ev *claude.RateLimitEvent) {
	window := func(key string) *UsageWindow {
		w, ok := ev.Info.Windows[key]
		if !ok {
			return nil
		}
		return &UsageWindow{Utilization: w.Utilization, ResetsAt: time.Unix(w.ResetsAt, 0).UTC()}
	}
	u := &Usage{FiveHour: window("five_hour"), Weekly: window("seven_day")}
	if u.FiveHour == nil && u.Weekly == nil {
		return
	}
	s.mu.Lock()
	old, _ := json.Marshal(s.account.Usage)
	nu, _ := json.Marshal(u)
	changed := string(old) != string(nu)
	if changed {
		s.account.Usage = u
	}
	acc := s.account
	s.mu.Unlock()
	if changed {
		s.broadcast(push(EventAccountUpdated, AccountUpdatedParams{Account: acc}))
	}
}

// ── Methods ─────────────────────────────────────────────────────────────

// handled is returned by methods that wrote their own reply.
var handled = errors.New("handled")

func (s *Server) call(c *conn, req Request) (any, error) {
	decode := func(v any) error {
		if len(req.Params) == 0 {
			return nil
		}
		if err := json.Unmarshal(req.Params, v); err != nil {
			return &Error{CodeBadRequest, err.Error()}
		}
		return nil
	}
	withSession := func(id string, f func(*session) error) (any, error) {
		ss, err := s.lookup(id)
		if err != nil {
			return nil, err
		}
		return struct{}{}, f(ss)
	}

	switch req.Method {
	case "hello":
		return nil, &Error{CodeProtocol, "hello was already sent"}

	case "sessions.list":
		return ListResult{Sessions: s.list()}, nil

	case "sessions.history":
		if c.remote {
			return nil, &Error{CodeLocalOnly, "sessions.history is local only"}
		}
		var p struct {
			CWD   string `json:"cwd"`
			Limit int    `json:"limit"`
		}
		if err := decode(&p); err != nil {
			return nil, err
		}
		return historyResult(p.CWD, p.Limit), nil

	case "session.start":
		if c.remote {
			return nil, &Error{CodeLocalOnly, "session.start is local only"}
		}
		var p StartParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		sum, attached, err := s.start(p)
		if err != nil {
			return nil, err
		}
		// A client that gave up waiting won't subscribe or stop it.
		if !attached && c.isClosed() {
			if ss, err := s.lookup(sum.ID); err == nil {
				ss.stop()
			}
			return nil, handled
		}
		return StartResult{Session: sum, Attached: attached}, nil

	case "session.subscribe":
		var p SubscribeParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		ss, err := s.lookup(p.Session)
		if err != nil {
			return nil, err
		}
		c.subscribe(ss)
		ss.subscribe(c, req.ID, p.Since, p.Live)
		return nil, handled

	case "session.unsubscribe":
		var p SessionParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return withSession(p.Session, func(ss *session) error {
			c.unsubscribe(ss)
			ss.unsubscribe(c)
			return nil
		})

	case "session.send":
		var p SendParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return withSession(p.Session, func(ss *session) error { return ss.send(p.Text, p.Images) })

	case "session.interrupt":
		var p SessionParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return withSession(p.Session, func(ss *session) error { return ss.interrupt() })

	case "session.respond":
		var p RespondParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return withSession(p.Session, func(ss *session) error { return ss.respond(p, c.client) })

	case "session.configure":
		var p ConfigureParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return withSession(p.Session, func(ss *session) error { return ss.configure(p) })

	case "session.stop":
		var p SessionParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return withSession(p.Session, func(ss *session) error { ss.stop(); return nil })
	}
	return nil, &Error{CodeUnknownMethod, "unknown method " + req.Method}
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	return &Error{CodeFailed, err.Error()}
}

func (s *Server) list() []SessionSummary {
	s.mu.RLock()
	all := make([]*session, 0, len(s.order))
	for _, id := range s.order {
		all = append(all, s.sessions[id])
	}
	s.mu.RUnlock()
	out := make([]SessionSummary, 0, len(all))
	for _, ss := range all {
		ss.mu.Lock()
		out = append(out, ss.summary())
		ss.mu.Unlock()
	}
	return out
}

// findLive is the live session on Claude Code session id, unless it is
// being stopped (a TUI restarting onto the same transcript stops the old
// process first, and must not get the dying one back).
func (s *Server) findLive(claudeID string) (SessionSummary, bool) {
	s.mu.RLock()
	all := make([]*session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		all = append(all, ss)
	}
	s.mu.RUnlock()
	for _, ss := range all {
		ss.mu.Lock()
		sum, stopping := ss.summary(), ss.stopping
		ss.mu.Unlock()
		if !stopping && sum.ClaudeSessionID != nil && *sum.ClaudeSessionID == claudeID {
			return sum, true
		}
	}
	return SessionSummary{}, false
}

func (s *Server) start(p StartParams) (SessionSummary, bool, error) {
	if !filepath.IsAbs(p.CWD) {
		return SessionSummary{}, false, &Error{CodeBadRequest, "cwd must be an absolute path"}
	}
	if fi, err := os.Stat(p.CWD); err != nil || !fi.IsDir() {
		return SessionSummary{}, false, &Error{CodeBadRequest, "no such directory " + p.CWD}
	}
	// Resuming (or continuing) a session that is already live reattaches
	// to it rather than starting a second claude on the same transcript.
	target := p.Resume
	if target == "" && p.Continue {
		if path, ok := sessions.Find(sessions.ProjectDir(p.CWD), ""); ok {
			target = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		}
	}
	if target != "" {
		if sum, ok := s.findLive(target); ok {
			return sum, true, nil
		}
	}

	opts := s.Base
	opts.CWD = p.CWD
	opts.Resume, opts.Continue = p.Resume, p.Continue
	opts.ExtraArgs = append(append([]string(nil), s.Base.ExtraArgs...), p.ExtraArgs...)
	if len(p.Env) > 0 {
		opts.Env = p.Env
	}
	if p.Model != "" {
		opts.Model = p.Model
	}
	if p.Effort != "" {
		opts.Effort = p.Effort
	}
	if p.PermissionMode != "" {
		opts.PermissionMode = p.PermissionMode
	}
	proc, err := s.Launch(s.ctx, opts)
	if err != nil {
		return SessionSummary{}, false, &Error{CodeFailed, err.Error()}
	}

	sum := SessionSummary{
		CWD:            p.CWD,
		State:          StateStarting,
		Model:          opts.Model,
		Effort:         opts.Effort,
		PermissionMode: opts.PermissionMode,
		LastActivity:   now(),
	}
	if sum.PermissionMode == "" {
		sum.PermissionMode = "default"
	}
	if p.Resume != "" {
		id := p.Resume
		sum.ClaudeSessionID = &id
		if path, ok := sessions.Find(sessions.ProjectDir(p.CWD), p.Resume); ok {
			if info, ok := sessions.Read(path); ok {
				sum.Title = firstLine(info.Label(), 80)
			}
		}
	}

	s.mu.Lock()
	s.nextID++
	sum.ID = fmt.Sprintf("s_%d", s.nextID)
	ss := newSession(s, sum.ID, proc, sum)
	s.sessions[ss.id] = ss
	s.order = append(s.order, ss.id)
	s.mu.Unlock()

	ss.mu.Lock()
	ss.changed()
	out := ss.summary()
	ss.mu.Unlock()
	go ss.run()
	return out, false, nil
}

// PastSession is a session read from its transcript on disk.
type PastSession struct {
	ID      string    `json:"id"` // Claude Code's session id
	CWD     string    `json:"cwd"`
	Title   string    `json:"title"`
	Branch  string    `json:"branch,omitempty"`
	Updated time.Time `json:"updated"`
}

func historyResult(cwd string, limit int) any {
	var infos []sessions.Info
	if cwd != "" {
		infos = sessions.List(sessions.ProjectDir(cwd))
	} else {
		infos = sessions.ListAll()
	}
	if limit > 0 && len(infos) > limit {
		infos = infos[:limit]
	}
	out := make([]PastSession, len(infos))
	for i, in := range infos {
		out[i] = PastSession{ID: in.ID, CWD: in.CWD, Title: firstLine(in.Label(), 120), Branch: in.Branch,
			Updated: in.Updated.UTC().Truncate(time.Second)}
	}
	return struct {
		Sessions []PastSession `json:"sessions"`
	}{out}
}
