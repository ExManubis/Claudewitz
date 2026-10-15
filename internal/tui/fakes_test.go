package tui

import (
	"sync"

	"github.com/exmanubis/claudewitz/internal/claude"
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
