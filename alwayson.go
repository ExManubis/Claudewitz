package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/exmanubis/claudewitz/internal/daemon"
	"github.com/exmanubis/claudewitz/internal/daemon/client"
	"github.com/exmanubis/claudewitz/internal/daemon/launchd"
)

// `claudewitz daemon install|uninstall`: keeping the daemon running at
// login (launchd), so the iPhone app can reach this Mac. The TUI's palette
// does the same through alwaysOn.

func runDaemonInstall(args []string) {
	fs := flag.NewFlagSet("daemon install", flag.ExitOnError)
	_ = fs.Parse(args)
	ctx := context.Background()
	c := dialRunning(ctx)
	if c != nil {
		defer c.Close()
	}
	res, err := launchd.New().Install(ctx, caller(c), true)
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(res.Message())
	if !res.Already {
		fmt.Printf("It runs %s (logs in %s). Undo with `claudewitz daemon uninstall`.\n", res.Exe, launchd.New().LogPath())
	}
}

func runDaemonUninstall(args []string) {
	fs := flag.NewFlagSet("daemon uninstall", flag.ExitOnError)
	_ = fs.Parse(args)
	ctx := context.Background()
	c := dialRunning(ctx)
	if c != nil {
		defer c.Close()
	}
	res, err := launchd.New().Uninstall(ctx, caller(c))
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(res.Message())
}

// caller keeps a nil *client.Client a nil launchd.Caller.
func caller(c *client.Client) launchd.Caller {
	if c == nil {
		return nil
	}
	return c
}

// suggestInstall follows `daemon remote on` and `pair`: the phone only
// reaches this Mac while the daemon runs.
func suggestInstall() {
	if a := launchd.New(); a.Supported() && !a.Installed() {
		fmt.Printf("\nThe phone can only reach this Mac while the daemon runs; it exits after %d idle minutes.\n"+
			"To keep it running and start it at login: claudewitz daemon install\n", int(launchd.IdleTimeout.Minutes()))
	}
}

// alwaysOn is the TUI's palette entries and /daemon (tui.AlwaysOn): the
// launchd agent, driven over the TUI's daemon connection.
type alwaysOn struct {
	agent *launchd.Agent
	c     *client.Client
}

func (a alwaysOn) Supported() bool { return a.agent.Supported() }
func (a alwaysOn) Installed() bool { return a.agent.Installed() }

func (a alwaysOn) Install(ctx context.Context) (string, error) {
	res, err := a.agent.Install(ctx, a.c, false)
	if err != nil {
		return "", err
	}
	return res.Message(), nil
}

func (a alwaysOn) Uninstall(ctx context.Context) (string, error) {
	res, err := a.agent.Uninstall(ctx, a.c)
	if err != nil {
		return "", err
	}
	return res.Message(), nil
}

// Status is a few lines about the daemon for /daemon.
func (a alwaysOn) Status(ctx context.Context) string {
	var st daemon.StatusResult
	if err := a.c.Call(ctx, "daemon.status", struct{}{}, &st); err != nil {
		return "daemon: " + err.Error()
	}
	remote := "off"
	switch {
	case st.Remote.Host != "":
		remote = fmt.Sprintf("on, %s:%d (Tailscale)", st.Remote.Host, st.Remote.Port)
	case st.Remote.Enabled:
		remote = "on, but down: " + st.Remote.Error
	}
	s := fmt.Sprintf("daemon   pid %d, version %s, up %s\nmode     %s\nremote   %s\nsessions %d live\ndevices  %d paired",
		st.PID, st.DaemonVersion, ago(st.StartedAt), a.agent.Mode(&st), remote, len(st.Sessions), len(st.Devices))
	if !a.agent.Installed() && a.agent.Supported() {
		s += "\nctrl+p › Keep daemon running at login keeps it up for the phone"
	}
	return s
}
