package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// outboxSize is how many frames a client may fall behind before it is
// dropped; it reconnects and replays from its last seq.
const outboxSize = 8192

const writeTimeout = 10 * time.Second

// conn is one connected client.
type conn struct {
	srv  *Server
	ws   *websocket.Conn
	out  chan []byte
	done chan struct{}
	once sync.Once

	mu     sync.Mutex
	hello  bool
	client string // "tui", "ios"
	remote bool   // on the remote listener (not built yet)
	subs   map[string]*session
}

func newConn(srv *Server, ws *websocket.Conn) *conn {
	return &conn{
		srv:  srv,
		ws:   ws,
		out:  make(chan []byte, outboxSize),
		done: make(chan struct{}),
		subs: map[string]*session{},
	}
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		c.ws.Close()
	})
}

func (c *conn) ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hello
}

// send queues a frame. A client too slow to keep up is disconnected
// rather than holding up the session.
func (c *conn) send(frame []byte) {
	select {
	case <-c.done:
	case c.out <- frame:
	default:
		c.srv.logf("client %s fell behind; disconnecting", c.client)
		c.close()
	}
}

func (c *conn) reply(id int64, result any) {
	b, err := json.Marshal(result)
	if err != nil {
		c.replyErr(id, &Error{CodeFailed, err.Error()})
		return
	}
	c.send(mustJSON(Frame{ID: id, Result: b}))
}

func (c *conn) replyErr(id int64, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{CodeFailed, err.Error()}
	}
	c.send(mustJSON(Frame{ID: id, Error: e}))
}

func (c *conn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case f := <-c.out:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteMessage(websocket.TextMessage, f); err != nil {
				c.close()
				return
			}
		}
	}
}

// readLoop handles requests one at a time, in order.
func (c *conn) readLoop() {
	for {
		_, b, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var req Request
		if err := json.Unmarshal(b, &req); err != nil || req.ID == 0 || req.Method == "" {
			c.replyErr(req.ID, &Error{CodeBadRequest, "expected {id, method, params}"})
			continue
		}
		if !c.ready() {
			if !c.handshake(req) {
				c.flushAndClose()
				return
			}
			continue
		}
		res, err := c.srv.call(c, req)
		switch {
		case err == handled:
		case err != nil:
			c.replyErr(req.ID, err)
		default:
			c.reply(req.ID, res)
		}
	}
}

// handshake answers the first request, which must be a hello in a protocol
// this daemon speaks.
func (c *conn) handshake(req Request) bool {
	if req.Method != "hello" {
		c.replyErr(req.ID, &Error{CodeProtocol, "the first request must be hello"})
		return false
	}
	var p HelloParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		c.replyErr(req.ID, &Error{CodeBadRequest, err.Error()})
		return false
	}
	if p.Protocol != Protocol {
		c.replyErr(req.ID, &Error{CodeProtocol, fmt.Sprintf("protocol %d not supported; this daemon speaks %d", p.Protocol, Protocol)})
		return false
	}
	c.srv.mu.RLock()
	acc := c.srv.account
	c.srv.mu.RUnlock()
	c.reply(req.ID, HelloResult{Protocol: Protocol, DaemonVersion: c.srv.Version, Account: acc})
	c.mu.Lock()
	c.hello, c.client = true, p.Client
	c.mu.Unlock()
	return true
}

// flushAndClose gives the writer a moment to send a final error reply.
func (c *conn) flushAndClose() {
	deadline := time.After(time.Second)
	for len(c.out) > 0 {
		select {
		case <-deadline:
			c.close()
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond)
	c.close()
}

func (c *conn) subscribe(ss *session) {
	c.mu.Lock()
	c.subs[ss.id] = ss
	c.mu.Unlock()
}

func (c *conn) unsubscribe(ss *session) {
	c.mu.Lock()
	delete(c.subs, ss.id)
	c.mu.Unlock()
}

func (c *conn) subscriptions() []*session {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*session, 0, len(c.subs))
	for _, ss := range c.subs {
		out = append(out, ss)
	}
	return out
}

// push encodes a daemon → client push.
func push(event string, params any) []byte {
	p, err := json.Marshal(params)
	if err != nil {
		panic(err) // params are our own types
	}
	return mustJSON(Frame{Event: event, Params: p})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
