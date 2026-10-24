package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/daemon"
	"github.com/exmanubis/claudewitz/internal/daemon/client"
	"github.com/exmanubis/claudewitz/internal/daemon/launchd"
)

// runDaemon is `claudewitz daemon`: it owns sessions for every client until
// it has had no clients and no sessions for -idle.
func runDaemon(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			runDaemonStatus()
			return
		case "remote":
			runDaemonRemote(args[1:])
			return
		case "apns":
			runDaemonAPNs(args[1:])
			return
		case "stop":
			runDaemonStop(args[1:])
			return
		case "install":
			runDaemonInstall(args[1:])
			return
		case "uninstall":
			runDaemonUninstall(args[1:])
			return
		}
	}
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	bin := fs.String("claude", "claude", "path to the claude CLI")
	sock := fs.String("socket", daemon.SocketPath(), "unix socket to listen on")
	idle := fs.Duration("idle", launchd.IdleTimeout, "exit after this long with no clients and no sessions (0: never)")
	wait := fs.Bool("wait", false, "if another daemon is listening, wait for it to exit instead of failing (for launchd)")
	_ = fs.Parse(args)

	if _, err := exec.LookPath(*bin); err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz daemon: %q not found\n", *bin)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := daemon.Listen(*sock)
	if errors.Is(err, daemon.ErrInUse) && *wait {
		ln, err = waitToListen(ctx, *sock)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz daemon: %v\n", err)
		os.Exit(1)
	}
	if ln == nil {
		return // stopped while waiting
	}
	// Remove the socket on the way out, unless a newer daemon has already
	// replaced it (see replaceStale).
	own, _ := os.Stat(*sock)
	defer func() {
		if cur, err := os.Stat(*sock); err == nil && own != nil && os.SameFile(own, cur) {
			_ = os.Remove(*sock)
		}
	}()

	srv := &daemon.Server{
		Version:     versionString(),
		Base:        claude.Options{Binary: *bin},
		IdleTimeout: *idle,
		ConfigPath:  filepath.Join(filepath.Dir(*sock), "daemon.json"),
		Devices:     &daemon.Devices{Path: filepath.Join(filepath.Dir(*sock), "devices.json")},
		Log:         log.New(os.Stderr, "claudewitz daemon: ", log.LstdFlags),
	}
	srv.Log.Printf("listening on %s (pid %d)", *sock, os.Getpid())
	if err := srv.Serve(ctx, ln); err != nil {
		srv.Log.Print(err)
	}
}

// waitToListen waits for the daemon on sock to exit, then listens in its
// place. launchd's always-on daemon does this when it starts behind an
// on-demand one, so installing doesn't end that one's sessions. It returns
// nil, nil if ctx ends first.
func waitToListen(ctx context.Context, sock string) (net.Listener, error) {
	log.Printf("claudewitz daemon: another daemon is listening on %s; waiting for it to exit", sock)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, nil
		case <-t.C:
		}
		ln, err := daemon.Listen(sock)
		if !errors.Is(err, daemon.ErrInUse) {
			return ln, err
		}
	}
}

// connectDaemon connects to the local daemon, starting one in the
// background if nothing answers. A daemon from another claudewitz version
// (after an upgrade it is the old binary) is replaced when it has no live
// sessions; otherwise it is kept, and note says so.
func connectDaemon(ctx context.Context, bin string) (c *client.Client, note string, err error) {
	sock := daemon.SocketPath()
	if c, err := client.Dial(ctx, sock, "tui", versionString()); err == nil {
		if c.Hello.DaemonVersion == versionString() {
			return c, "", nil
		}
		if note := replaceStale(ctx, c, sock); note != "" {
			return c, note, nil
		}
	}
	// The always-on daemon isn't running (stopped, or replaced above):
	// have launchd start it rather than starting one that idles out.
	if launchd.New().Start() {
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if c, err := client.Dial(ctx, sock, "tui", versionString()); err == nil {
				return c, "", nil
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	logPath := filepath.Join(filepath.Dir(sock), "daemon.log")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return nil, "", err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon", "-claude", bin, "-socket", sock)
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd) // outlive this terminal
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("starting the daemon: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := client.Dial(ctx, sock, "tui", versionString())
		if err == nil {
			return c, "", nil
		}
		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("the daemon didn't come up (see %s): %w", logPath, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// replaceStale asks a daemon of another version to exit and waits until it
// has. It returns a note, and leaves c open, if the daemon stays.
func replaceStale(ctx context.Context, c *client.Client, sock string) (note string) {
	old := c.Hello.DaemonVersion
	err := c.Call(ctx, "daemon.stop", daemon.StopParams{}, nil)
	var e *daemon.Error
	switch {
	case err == nil:
	case errors.As(err, &e) && e.Code == daemon.CodeBusy:
		return fmt.Sprintf("the claudewitz daemon is still %s (this is %s) because it has live sessions; "+
			"run `claudewitz daemon stop` once they're done", old, versionString())
	default:
		return fmt.Sprintf("the claudewitz daemon is %s (this is %s) and couldn't be replaced: %v", old, versionString(), err)
	}
	c.Close()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		conn, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
	}
	return ""
}
