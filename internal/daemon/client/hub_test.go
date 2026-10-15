package client

import (
	"context"
	"testing"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

func TestHubStartAttachList(t *testing.T) {
	h := startDaemon(t)
	c := h.dial(t, "tui")
	hub := Hub{C: c}
	ctx := context.Background()

	s, id, err := hub.Start(ctx, claude.Options{CWD: t.TempDir()})
	if err != nil || s == nil || id == "" {
		t.Fatalf("start: %v %v %q", s, err, id)
	}
	f := h.proc(t)

	list, err := hub.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list: %+v %v", list, err)
	}

	other := h.dial(t, "tui")
	att, err := Hub{C: other}.Attach(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	f.line(t, assistantLine)
	select {
	case ev := <-att.Events():
		if ev.Message == nil {
			t.Fatalf("attached session got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attached session got nothing")
	}

	select {
	case fr := <-hub.Overview():
		if fr.Event != daemon.EventSessionUpdated {
			t.Fatalf("overview: %s", fr.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no overview push")
	}
}
