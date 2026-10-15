package tui

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

// fakeSession is a claude.Session the tests feed events into by hand.
type fakeSession struct {
	mu       sync.Mutex
	events   chan claude.Event
	sent     []string
	stopped  bool
	detached bool
}

func newFakeSession() *fakeSession { return &fakeSession{events: make(chan claude.Event, 64)} }

func (f *fakeSession) emit(ev claude.Event)           { f.events <- ev }
func (f *fakeSession) Events() <-chan claude.Event    { return f.events }
func (f *fakeSession) Send(text string) error         { return f.SendWithImages(text, nil) }
func (f *fakeSession) Interrupt() error               { return nil }
func (f *fakeSession) SetModel(string) error          { return nil }
func (f *fakeSession) SetEffort(string) error         { return nil }
func (f *fakeSession) SetPermissionMode(string) error { return nil }
func (f *fakeSession) SendWithImages(text string, _ []claude.Image) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeSession) Respond(*claude.PermissionRequest, claude.PermissionDecision) error { return nil }
func (f *fakeSession) RespondAndInterrupt(*claude.PermissionRequest, claude.PermissionDecision) error {
	return nil
}
func (f *fakeSession) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
}
func (f *fakeSession) Detach() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detached = true
	return nil
}

type fakeDaemon struct {
	mu        sync.Mutex
	list      []daemon.SessionSummary
	overview  chan daemon.Frame
	attached  []string
	started   []claude.Options
	procs     map[string]*fakeSession
	attachErr error // returned (and cleared) by the next Attach
}

func newFakeDaemon(list ...daemon.SessionSummary) *fakeDaemon {
	return &fakeDaemon{list: list, overview: make(chan daemon.Frame, 16), procs: map[string]*fakeSession{}}
}

func (d *fakeDaemon) Start(_ context.Context, opts claude.Options) (claude.Session, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.started = append(d.started, opts)
	id := fmt.Sprintf("s_new%d", len(d.started))
	p := newFakeSession()
	d.procs[id] = p
	return p, id, nil
}

func (d *fakeDaemon) Attach(_ context.Context, id string) (claude.Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attached = append(d.attached, id)
	if err := d.attachErr; err != nil {
		d.attachErr = nil
		return nil, err
	}
	p := newFakeSession()
	d.procs[id] = p
	return p, nil
}

func (d *fakeDaemon) List(context.Context) ([]daemon.SessionSummary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.list), nil
}

func (d *fakeDaemon) Overview() <-chan daemon.Frame { return d.overview }

// run feeds cmd's messages (and those they lead to) through m, without
// blocking on commands that wait for events.
func run(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 50; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(50 * time.Millisecond):
			continue // waits on events or ticks; not part of this step
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if msg == nil {
			continue
		}
		next, c2 := m.Update(msg)
		m = next.(Model)
		queue = append(queue, c2)
	}
	return m
}
