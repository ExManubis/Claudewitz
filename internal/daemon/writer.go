package daemon

import (
	"errors"
	"sync"
)

var errStopped = errors.New("the session has ended")

// writer runs a session's writes to the CLI's stdin one at a time, in
// order, on its own goroutine. Writes can block (a CLI busy writing
// stdout reads stdin only when we read its stdout), so they never happen
// while holding the session's lock or on its event loop.
type writer struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []func()
	closed bool
}

func (w *writer) init() {
	if w.cond == nil {
		w.cond = sync.NewCond(&w.mu)
	}
}

// loop runs queued writes until close.
func (w *writer) loop() {
	w.mu.Lock()
	w.init()
	for {
		for len(w.queue) == 0 && !w.closed {
			w.cond.Wait()
		}
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return
		}
		f := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		f()
		w.mu.Lock()
	}
}

func (w *writer) enqueue(f func()) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.init()
	if w.closed {
		return false
	}
	w.queue = append(w.queue, f)
	w.cond.Signal()
	return true
}

// do runs f in turn and waits for it.
func (w *writer) do(f func() error) error {
	done := make(chan error, 1)
	if !w.enqueue(func() { done <- f() }) {
		return errStopped
	}
	return <-done
}

// async queues f without waiting.
func (w *writer) async(f func() error) {
	w.enqueue(func() { _ = f() })
}

// close lets the queue drain, then ends loop; later writes fail.
func (w *writer) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.init()
	w.closed = true
	w.cond.Broadcast()
}
