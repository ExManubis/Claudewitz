package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
	"github.com/exmanubis/claudewitz/internal/daemon/client"
)

// runDaemon is `claudewitz daemon`: it owns sessions for every client until
// it has had no clients and no sessions for -idle.
func runDaemon(args []string) {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	bin := fs.String("claude", "claude", "path to the claude CLI")
	sock := fs.String("socket", daemon.SocketPath(), "unix socket to listen on")
	idle := fs.Duration("idle", 10*time.Minute, "exit after this long with no clients and no sessions (0: never)")
	_ = fs.Parse(args)

	if _, err := exec.LookPath(*bin); err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz daemon: %q not found\n", *bin)
		os.Exit(1)
	}
	ln, err := daemon.Listen(*sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz daemon: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(*sock)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &daemon.Server{
		Version:     versionString(),
		Base:        claude.Options{Binary: *bin},
		IdleTimeout: *idle,
		Log:         log.New(os.Stderr, "claudewitz daemon: ", log.LstdFlags),
	}
	srv.Log.Printf("listening on %s (pid %d)", *sock, os.Getpid())
	if err := srv.Serve(ctx, ln); err != nil {
		srv.Log.Print(err)
	}
}

// connectDaemon connects to the local daemon, starting one in the
// background if nothing answers.
func connectDaemon(ctx context.Context, bin string) (*client.Client, error) {
	sock := daemon.SocketPath()
	if c, err := client.Dial(ctx, sock, "tui", versionString()); err == nil {
		return c, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(filepath.Dir(sock), "daemon.log")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon", "-claude", bin, "-socket", sock)
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd) // outlive this terminal
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the daemon: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := client.Dial(ctx, sock, "tui", versionString())
		if err == nil {
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the daemon didn't come up (see %s): %w", logPath, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
