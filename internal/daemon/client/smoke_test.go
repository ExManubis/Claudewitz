//go:build smoke

package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
)

func TestSmokeRealClaude(t *testing.T) {
	dir, _ := os.MkdirTemp("/tmp", "cws")
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "d.sock")
	ln, err := daemon.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go (&daemon.Server{Version: "smoke"}).Serve(ctx, ln)
	c, err := Dial(ctx, sock, "tui", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Starter(c)(ctx, claude.Options{CWD: dir, Model: "haiku"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send("Reply with just the word: pong"); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(90 * time.Second)
	for {
		select {
		case ev := <-s.Events():
			switch {
			case ev.Initialize != nil:
				t.Logf("initialize: %d commands, %d models, account %s", len(ev.Initialize.Commands), len(ev.Initialize.Models), ev.Initialize.Account.SubscriptionType)
			case ev.Init != nil:
				t.Logf("init: session %s model %s", ev.Init.SessionID, ev.Init.Model)
			case ev.Message != nil && ev.Message.Message.Role == "assistant":
				t.Logf("assistant: %q", ev.Message.Message.Text())
			case ev.Result != nil:
				list, _ := c.List(ctx)
				t.Logf("result: cost %.4f; summary %+v activity %+v", ev.Result.TotalCostUSD, list[0], list[0].Activity)
				_ = s.Close()
			case ev.Exited:
				t.Logf("exited: %v; hello account %+v", ev.Err, c.Hello.Account)
				return
			}
		case <-timeout:
			t.Fatal("timeout")
		}
	}
}
