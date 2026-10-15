// Package client talks to the claudewitz daemon over its local socket. A
// subscribed Session implements claude.Session, so the TUI drives a
// daemon-held session exactly as it drives a local claude process.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// callTimeout bounds a request that has no deadline of its own.
const callTimeout = 15 * time.Second

// startTimeout bounds session.start, which launches the CLI.
const startTimeout = time.Minute

// ErrClosed is returned once the connection to the daemon is gone.
var ErrClosed = errors.New("lost the connection to the claudewitz daemon")

// Client is one connection to the daemon.
type Client struct {
	ws     *websocket.Conn
	wmu    sync.Mutex
	nextID atomic.Int64

	mu       sync.Mutex
	calls    map[int64]chan daemon.Frame
	subs     map[string]*Session
	overview chan daemon.Frame
	done     chan struct{}

	// Hello is the daemon's answer to hello.
	Hello daemon.HelloResult
}

// Dial connects to the daemon's unix socket at path and says hello as
// client ("tui").
func Dial(ctx context.Context, path, client, version string) (*Client, error) {
	c, err := dial(ctx, path)
	if err != nil {
		return nil, err
	}
	err = c.Call(ctx, "hello", daemon.HelloParams{Protocol: daemon.Protocol, Client: client, ClientVersion: version}, &c.Hello)
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// dial connects without saying hello.
func dial(ctx context.Context, path string) (*Client, error) {
	d := websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var nd net.Dialer
			return nd.DialContext(ctx, "unix", path)
		},
		HandshakeTimeout: 5 * time.Second,
	}
	ws, _, err := d.DialContext(ctx, "ws://claudewitz/", http.Header{})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(64 << 20)
	c := &Client{
		ws:       ws,
		calls:    map[int64]chan daemon.Frame{},
		subs:     map[string]*Session{},
		overview: make(chan daemon.Frame, 256),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Close drops the connection. Sessions stay live in the daemon.
func (c *Client) Close() error { return c.ws.Close() }

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Overview delivers session.updated, session.closed and account.updated
// pushes. Pushes are dropped while nobody reads it and it is full.
func (c *Client) Overview() <-chan daemon.Frame { return c.overview }

// Call sends a request and decodes its result into result (if non-nil).
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	id := c.nextID.Add(1)
	ch := make(chan daemon.Frame, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return ErrClosed
	default:
	}
	c.calls[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.calls, id)
		c.mu.Unlock()
	}()

	b, _ := json.Marshal(daemon.Request{ID: id, Method: method, Params: p})
	c.wmu.Lock()
	err = c.ws.WriteMessage(websocket.TextMessage, b)
	c.wmu.Unlock()
	if err != nil {
		return ErrClosed
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}
	select {
	case f := <-ch:
		if f.Error != nil {
			return f.Error
		}
		if result != nil && len(f.Result) > 0 {
			return json.Unmarshal(f.Result, result)
		}
		return nil
	case <-c.done:
		return ErrClosed
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func (c *Client) readLoop() {
	defer func() {
		c.mu.Lock()
		close(c.done)
		subs := c.subs
		c.subs = map[string]*Session{}
		c.mu.Unlock()
		for _, s := range subs {
			s.end(ErrClosed)
		}
	}()
	for {
		_, b, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var f daemon.Frame
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		if f.Event == "" {
			c.mu.Lock()
			ch := c.calls[f.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- f
			}
			continue
		}
		c.dispatch(f)
	}
}

// dispatch routes a push to its session and/or the overview channel.
func (c *Client) dispatch(f daemon.Frame) {
	var target struct {
		Session string `json:"session"`
	}
	_ = json.Unmarshal(f.Params, &target)
	switch f.Event {
	case daemon.EventSessionUpdated, daemon.EventAccountUpdated:
		c.toOverview(f)
		return
	case daemon.EventSessionClosed:
		c.toOverview(f)
		var p daemon.SessionClosedParams
		_ = json.Unmarshal(f.Params, &p)
		if s := c.take(p.Session); s != nil {
			var err error
			if p.LastError != nil {
				err = errors.New(*p.LastError)
			}
			s.end(err)
		}
		return
	}
	c.mu.Lock()
	s := c.subs[target.Session]
	c.mu.Unlock()
	if s != nil {
		s.push(f)
	}
}

func (c *Client) toOverview(f daemon.Frame) {
	select {
	case c.overview <- f:
	default:
	}
}

func (c *Client) take(id string) *Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.subs[id]
	delete(c.subs, id)
	return s
}

// List returns the live sessions.
func (c *Client) List(ctx context.Context) ([]daemon.SessionSummary, error) {
	var r daemon.ListResult
	err := c.Call(ctx, "sessions.list", struct{}{}, &r)
	return r.Sessions, err
}

// Start starts a session (or finds the live one being resumed).
func (c *Client) Start(ctx context.Context, p daemon.StartParams) (daemon.StartResult, error) {
	var r daemon.StartResult
	err := c.Call(ctx, "session.start", p, &r)
	return r, err
}

// Subscribe attaches to a session's stream, replaying what the daemon still
// holds after since (everything when since is nil).
func (c *Client) Subscribe(ctx context.Context, id string, since *int64) (*Session, daemon.SubscribeResult, error) {
	return c.subscribe(ctx, daemon.SubscribeParams{Session: id, Since: since})
}

// SubscribeLive attaches to a session's stream without replaying earlier
// events, for a client that loads them from the transcript instead.
func (c *Client) SubscribeLive(ctx context.Context, id string) (*Session, daemon.SubscribeResult, error) {
	return c.subscribe(ctx, daemon.SubscribeParams{Session: id, Live: true})
}

func (c *Client) subscribe(ctx context.Context, p daemon.SubscribeParams) (*Session, daemon.SubscribeResult, error) {
	id := p.Session
	s := newSession(c, id)
	// Registered before asking, since the replay follows the reply at once.
	c.mu.Lock()
	c.subs[id] = s
	c.mu.Unlock()
	var r daemon.SubscribeResult
	if err := c.Call(ctx, "session.subscribe", p, &r); err != nil {
		c.take(id)
		return nil, r, err
	}
	s.havePending(r.Pending)
	return s, r, nil
}

// Starter starts a session in the daemon and subscribes to it, for
// tui.New. The claude binary is the daemon's, not opts.Binary. When
// resume or continue finds the session already live, it attaches without
// replay: the TUI shows the history from the transcript on disk.
func Starter(c *Client) func(ctx context.Context, opts claude.Options) (claude.Session, error) {
	return func(ctx context.Context, opts claude.Options) (claude.Session, error) {
		env := opts.Env
		if env == nil {
			env = os.Environ()
		}
		sctx, cancel := context.WithTimeout(ctx, startTimeout)
		defer cancel()
		r, err := c.Start(sctx, daemon.StartParams{
			Env:            env,
			CWD:            opts.CWD,
			Model:          opts.Model,
			Effort:         opts.Effort,
			PermissionMode: opts.PermissionMode,
			Resume:         opts.Resume,
			Continue:       opts.Continue,
			ExtraArgs:      opts.ExtraArgs,
		})
		if err != nil {
			return nil, err
		}
		subscribe := c.Subscribe
		if r.Attached {
			subscribe = func(ctx context.Context, id string, _ *int64) (*Session, daemon.SubscribeResult, error) {
				return c.SubscribeLive(ctx, id)
			}
		}
		s, _, err := subscribe(ctx, r.Session.ID, nil)
		if err != nil {
			if !r.Attached {
				// Don't leave a session nobody will see behind.
				_ = c.Call(context.Background(), "session.stop", daemon.SessionParams{Session: r.Session.ID}, nil)
			}
			return nil, err
		}
		return s, nil
	}
}
