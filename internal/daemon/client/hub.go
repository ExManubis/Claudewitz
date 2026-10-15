package client

import (
	"context"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// Hub is the TUI's view of the daemon: every live session, starting and
// attaching to them, and the overview pushes. It satisfies tui.Daemon.
type Hub struct{ C *Client }

// Start starts a session (or attaches to the live one being resumed) and
// returns it with the daemon's id for it.
func (h Hub) Start(ctx context.Context, opts claude.Options) (claude.Session, string, error) {
	s, err := Starter(h.C)(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	sess := s.(*Session)
	return sess, sess.ID, nil
}

// Attach follows a live session from now on; its history comes from the
// transcript on disk.
func (h Hub) Attach(ctx context.Context, id string) (claude.Session, error) {
	s, _, err := h.C.SubscribeLive(ctx, id)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// List returns every live session.
func (h Hub) List(ctx context.Context) ([]daemon.SessionSummary, error) { return h.C.List(ctx) }

// Kill stops the session id in the daemon, attached or not.
func (h Hub) Kill(ctx context.Context, id string) error {
	return h.C.Call(ctx, "session.stop", daemon.SessionParams{Session: id}, nil)
}

// PairCode makes a single-use pairing code for the iPhone app, as `claudewitz pair` does.
func (h Hub) PairCode(ctx context.Context) (daemon.PairCodeResult, error) {
	var code daemon.PairCodeResult
	err := h.C.Call(ctx, "pair.code", struct{}{}, &code)
	return code, err
}

// EnableRemote turns on the remote listener, as `claudewitz daemon remote on` does.
func (h Hub) EnableRemote(ctx context.Context) error {
	return h.C.Call(ctx, "remote.set", daemon.RemoteSetParams{Enabled: true}, nil)
}

// Overview delivers session.updated, session.closed and account.updated.
func (h Hub) Overview() <-chan daemon.Frame { return h.C.Overview() }

// Done is closed when the connection to the daemon ends.
func (h Hub) Done() <-chan struct{} { return h.C.Done() }
