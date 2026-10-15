package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/recap"
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
	// ConfigPath is daemon.json, where remote access is remembered; ""
	// keeps it in memory only.
	ConfigPath string
	// Devices are the paired phones; nil means DevicesPath().
	Devices *Devices
	// TailscaleAddr finds the address the remote listener binds to; nil
	// means TailscaleIP.
	TailscaleAddr func() (netip.Addr, error)

	ctx      context.Context    // outlives requests; ends the processes
	stop     context.CancelFunc // ends Serve, for daemon.stop
	mu       sync.RWMutex
	sessions map[string]*session
	order    []string // session ids by start
	nextID   int
	conns    map[*conn]bool
	account  Account
	idleFrom time.Time
	codes    map[string]time.Time // pairing code → expiry
	recaps   struct {
		once sync.Once
		s    *recap.Scanner // kept warm between recaps
	}
	started time.Time
	local   string // the local listener's address

	remoteMu  sync.Mutex // guards the fields below
	config    Config
	remote    *remoteListener // nil while off or down
	remoteErr string          // why an enabled remote listener is down
}

// Serve accepts clients on ln until ctx ends or the daemon idles out.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.ctx, s.stop = ctx, cancel
	s.sessions = map[string]*session{}
	s.conns = map[*conn]bool{}
	s.idleFrom = time.Now()
	s.codes = map[string]time.Time{}
	s.started = now()
	s.local = ln.Addr().String()
	s.mu.Unlock()
	if s.Launch == nil {
		s.Launch = claude.Launch
	}
	if s.Devices == nil {
		s.Devices = &Devices{Path: DevicesPath()}
	}

	hs := &http.Server{Handler: s.handler(false)}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()

	s.remoteMu.Lock()
	s.config = s.loadConfig()
	s.remoteMu.Unlock()
	s.retryRemote()
	defer func() {
		s.remoteMu.Lock()
		s.closeRemote()
		s.remoteMu.Unlock()
	}()

	s.mu.RLock()
	tick := time.NewTicker(idleCheck(s.IdleTimeout))
	s.mu.RUnlock()
	defer tick.Stop()
	for {
		select {
		case err := <-errc:
			return err
		case <-ctx.Done():
			hs.Close()
			return nil
		case <-tick.C:
			s.retryRemote()
			if d, idle := s.idle(); idle {
				s.logf("idle for %s, exiting", d)
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

// idle reports whether the daemon has idled out, and after how long.
func (s *Server) idle() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) > 0 || len(s.sessions) > 0 {
		s.idleFrom = time.Now()
		return 0, false
	}
	return s.IdleTimeout, s.IdleTimeout > 0 && time.Since(s.idleFrom) >= s.IdleTimeout
}

// setIdle changes the idle timeout of a running daemon (daemon.setIdle).
// The idle clock is not reset, but a daemon only idles with no clients, so
// the caller's connection already holds it up.
func (s *Server) setIdle(p IdleParams) (any, error) {
	if p.Seconds < 0 {
		return nil, &Error{CodeBadRequest, "seconds must not be negative"}
	}
	d := time.Duration(p.Seconds) * time.Second
	s.mu.Lock()
	s.IdleTimeout = d
	s.mu.Unlock()
	if d == 0 {
		s.logf("idle timeout off")
	} else {
		s.logf("idle timeout %s", d)
	}
	return struct{}{}, nil
}

// ErrInUse is Listen's error when another daemon answers on the socket.
var ErrInUse = errors.New("a daemon is already listening")

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
			return nil, fmt.Errorf("%w on %s", ErrInUse, path)
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
	// The local listener is a unix socket, which browsers can't reach;
	// the remote one refuses requests with an Origin (see handler).
	CheckOrigin: func(*http.Request) bool { return true },
}

// serveWS runs one client connection. Remote connections without a
// device may only pair, and are dropped if they don't within pairTimeout.
func (s *Server) serveWS(w http.ResponseWriter, r *http.Request, remote bool, dev *Device) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 20) // images arrive inline
	c := newConn(s, ws)
	c.remote = remote
	if dev != nil {
		c.device = dev.ID
		defer s.devices().Seen(dev.ID)
	} else if remote {
		ws.SetReadLimit(4 << 10)
		t := time.AfterFunc(pairTimeout, func() {
			if !c.authed() {
				c.close()
			}
		})
		defer t.Stop()
	}
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

	localOnly := func() error {
		if c.remote {
			return &Error{CodeLocalOnly, req.Method + " is local only"}
		}
		return nil
	}

	switch req.Method {
	case "hello":
		return nil, &Error{CodeProtocol, "hello was already sent"}

	case "pair":
		var p PairParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return s.pair(c, p)

	case "daemon.status":
		if err := localOnly(); err != nil {
			return nil, err
		}
		return s.status()

	case "remote.set":
		if err := localOnly(); err != nil {
			return nil, err
		}
		var p RemoteSetParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return s.setRemote(p)

	case "pair.code":
		if err := localOnly(); err != nil {
			return nil, err
		}
		return s.newPairCode()

	case "devices.list":
		if err := localOnly(); err != nil {
			return nil, err
		}
		devs, err := s.deviceList()
		return DevicesResult{Devices: devs}, err

	case "daemon.stop":
		if err := localOnly(); err != nil {
			return nil, err
		}
		var p StopParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return s.stopDaemon(p)

	case "daemon.setIdle":
		if err := localOnly(); err != nil {
			return nil, err
		}
		var p IdleParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		return s.setIdle(p)

	case "devices.revoke":
		if err := localOnly(); err != nil {
			return nil, err
		}
		var p RevokeParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		d, err := s.revoke(p.Device)
		return RevokeResult{Device: d}, err

	case "sessions.list":
		return ListResult{Sessions: s.list()}, nil

	case "recap.day":
		var p RecapParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		loc := time.Local
		if p.TZ != "" {
			l, err := time.LoadLocation(p.TZ)
			if err != nil {
				return nil, &Error{CodeBadRequest, "unknown time zone " + p.TZ}
			}
			loc = l
		}
		day := time.Now().In(loc)
		if p.Date != "" {
			d, err := time.ParseInLocation(time.DateOnly, p.Date, loc)
			if err != nil {
				return nil, &Error{CodeBadRequest, "date wants YYYY-MM-DD"}
			}
			day = d
		}
		s.recaps.once.Do(func() { s.recaps.s = recap.NewScanner(sessions.Root()) })
		// The first recap reads weeks of transcripts; don't hold up the
		// connection's other requests meanwhile. (The tally runs inside the
		// goroutine: `go c.reply(id, recap())` would run it here.)
		go func() { c.reply(req.ID, s.recaps.s.Recap(day, loc)) }()
		return nil, handled

	case "sessions.history":
		if err := localOnly(); err != nil {
			return nil, err
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
		if err := localOnly(); err != nil {
			return nil, err
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

// stopDaemon ends Serve shortly after the reply goes out.
func (s *Server) stopDaemon(p StopParams) (any, error) {
	if n := len(s.list()); n > 0 && !p.Force {
		return nil, &Error{CodeBusy, fmt.Sprintf("%d live session(s) would end; stop them first, or force it", n)}
	}
	s.mu.RLock()
	stop := s.stop
	s.mu.RUnlock()
	s.logf("stopping on request")
	time.AfterFunc(100*time.Millisecond, stop)
	return struct{}{}, nil
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
	sum.Repo, sum.Worktree = repoOf(p.CWD)
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
