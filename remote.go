package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mdp/qrterminal/v3"

	"github.com/exmanubis/claudewitz/internal/daemon"
	"github.com/exmanubis/claudewitz/internal/daemon/client"
	"github.com/exmanubis/claudewitz/internal/daemon/launchd"
)

// The `claudewitz daemon status|remote`, `pair` and `devices` commands.
// They talk to the running daemon over the local socket; where that makes
// sense without one, they read or edit its files directly.

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "claudewitz: "+format+"\n", args...)
	os.Exit(1)
}

func warn(note string) {
	if note != "" {
		fmt.Fprintln(os.Stderr, "claudewitz: "+note)
	}
}

// runDaemonStop is `claudewitz daemon stop [-force]`.
func runDaemonStop(args []string) {
	fs := flag.NewFlagSet("daemon stop", flag.ExitOnError)
	force := fs.Bool("force", false, "stop even if sessions are live (they end with it)")
	_ = fs.Parse(args)
	ctx := context.Background()
	c := dialRunning(ctx)
	if c == nil {
		fmt.Println("no daemon is running")
		return
	}
	defer c.Close()
	call(ctx, c, "daemon.stop", daemon.StopParams{Force: *force}, nil)
	fmt.Println("daemon stopped")
}

// dialRunning connects to the daemon if one is running, without starting one.
func dialRunning(ctx context.Context) *client.Client {
	c, err := client.Dial(ctx, daemon.SocketPath(), "cli", versionString())
	if err != nil {
		return nil
	}
	return c
}

func call(ctx context.Context, c *client.Client, method string, params, result any) {
	if err := c.Call(ctx, method, params, result); err != nil {
		var e *daemon.Error
		if errors.As(err, &e) {
			fail("%s", e.Message)
		}
		fail("%v", err)
	}
}

// runDaemonRemote is `claudewitz daemon remote on|off`.
func runDaemonRemote(args []string) {
	fs := flag.NewFlagSet("daemon remote", flag.ExitOnError)
	port := fs.Int("port", 0, fmt.Sprintf("TCP port (default %d, or the one used last)", daemon.DefaultRemotePort))
	bin := fs.String("claude", "claude", "path to the claude CLI, if the daemon has to be started")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: claudewitz daemon remote on|off [-port N]")
		fs.PrintDefaults()
	}
	if len(args) == 0 || (args[0] != "on" && args[0] != "off") {
		fs.Usage()
		os.Exit(2)
	}
	on := args[0] == "on"
	_ = fs.Parse(args[1:])
	ctx := context.Background()

	c := dialRunning(ctx)
	if c == nil && !on {
		// Nothing is listening; just remember it for the next start.
		cfg, err := daemon.LoadConfig(daemon.ConfigPath())
		if err != nil {
			fail("%v", err)
		}
		cfg.Remote.Enabled = false
		if err := daemon.SaveConfig(daemon.ConfigPath(), cfg); err != nil {
			fail("%v", err)
		}
		fmt.Println("Remote access is off.")
		return
	}
	if c == nil {
		var err error
		var note string
		if c, note, err = connectDaemon(ctx, *bin); err != nil {
			fail("%v", err)
		}
		warn(note)
	}
	defer c.Close()
	var st daemon.RemoteStatus
	call(ctx, c, "remote.set", daemon.RemoteSetParams{Enabled: on, Port: *port}, &st)
	if !on {
		fmt.Println("Remote access is off.")
		return
	}
	fmt.Printf("Remote access is on: listening on %s:%d (Tailscale only).\nPair a phone with `claudewitz pair`.\n", st.Host, st.Port)
	suggestInstall()
}

// runDaemonAPNs is `claudewitz daemon apns -key K.p8 -key-id ID -team-id ID`
// (or `off`): the key the daemon signs pushes to the phone with.
func runDaemonAPNs(args []string) {
	fs := flag.NewFlagSet("daemon apns", flag.ExitOnError)
	key := fs.String("key", "", "the .p8 key file from developer.apple.com (Keys, with Apple Push Notifications service)")
	keyID := fs.String("key-id", "", "the key's id (10 characters, shown with the key)")
	teamID := fs.String("team-id", "", "your Apple developer team id")
	topic := fs.String("topic", "", "the app's bundle id (default "+daemon.DefaultAPNsTopic+")")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: claudewitz daemon apns -key AuthKey_ID.p8 -key-id ID -team-id ID\n       claudewitz daemon apns off")
		fs.PrintDefaults()
	}
	p := daemon.APNsSetParams{Off: len(args) == 1 && args[0] == "off"}
	if !p.Off {
		_ = fs.Parse(args)
		if *key == "" || *keyID == "" || *teamID == "" {
			fs.Usage()
			os.Exit(2)
		}
		p.KeyPath, p.KeyID, p.TeamID, p.Topic = *key, *keyID, *teamID, *topic
	}
	ctx := context.Background()
	var st daemon.APNsStatus
	if c := dialRunning(ctx); c != nil {
		defer c.Close()
		call(ctx, c, "apns.set", p, &st)
	} else {
		var err error
		if st, err = daemon.ConfigureAPNs(daemon.ConfigPath(), p); err != nil {
			fail("%v", err)
		}
	}
	if p.Off {
		fmt.Println("Push notifications are off.")
		return
	}
	if st.Error != "" {
		fail("the key is saved but can't be used: %s", st.Error)
	}
	fmt.Printf("The daemon pushes to the phone with key %s (team %s, app %s).\n", st.KeyID, st.TeamID, st.Topic)
	fmt.Printf("It keeps its own copy of the key; you can delete %s.\n", *key)
}

// runDaemonStatus is `claudewitz daemon status`.
func runDaemonStatus() {
	ctx := context.Background()
	c := dialRunning(ctx)
	if c == nil {
		fmt.Printf("%-10s%s\n", "daemon", "not running ("+daemon.SocketPath()+")")
		fmt.Printf("%-10s%s\n", "mode", launchd.New().Mode(nil))
		cfg, _ := daemon.LoadConfig(daemon.ConfigPath())
		if cfg.Remote.Enabled {
			fmt.Printf("%-10s%s\n", "remote", "on (listens when the daemon starts)")
		} else {
			fmt.Printf("%-10s%s\n", "remote", "off")
		}
		if cfg.APNs != nil {
			fmt.Printf("%-10s%s\n", "apns", "key "+cfg.APNs.KeyID+", team "+cfg.APNs.TeamID)
		} else {
			fmt.Printf("%-10s%s\n", "apns", "off (`claudewitz daemon apns` to push to the phone)")
		}
		devs, err := (&daemon.Devices{Path: daemon.DevicesPath()}).List()
		if err != nil {
			fail("%v", err)
		}
		infos := make([]daemon.DeviceInfo, len(devs))
		for i, d := range devs {
			infos[i] = daemon.DeviceInfo{ID: d.ID, Name: d.Name, PairedAt: d.PairedAt, LastSeen: d.LastSeen}
		}
		printDevices(infos)
		return
	}
	defer c.Close()
	var st daemon.StatusResult
	call(ctx, c, "daemon.status", struct{}{}, &st)

	w := tabwriter.NewWriter(os.Stdout, 10, 0, 0, ' ', 0)
	fmt.Fprintf(w, "daemon\trunning, pid %d, version %s, up %s\n", st.PID, st.DaemonVersion, ago(st.StartedAt))
	fmt.Fprintf(w, "mode\t%s\n", launchd.New().Mode(&st))
	for _, l := range st.Listeners {
		fmt.Fprintf(w, "%s\t%s\n", l.Kind, l.Address)
	}
	switch {
	case st.Remote.Host != "":
	case st.Remote.Enabled:
		fmt.Fprintf(w, "remote\ton, but down: %s\n", st.Remote.Error)
	default:
		fmt.Fprintf(w, "remote\toff\n")
	}
	switch {
	case st.APNs.Error != "":
		fmt.Fprintf(w, "apns\tkey %s, but unusable: %s\n", st.APNs.KeyID, st.APNs.Error)
	case st.APNs.Configured:
		fmt.Fprintf(w, "apns\tkey %s, team %s\n", st.APNs.KeyID, st.APNs.TeamID)
	default:
		fmt.Fprintf(w, "apns\toff (`claudewitz daemon apns` to push to the phone)\n")
	}
	fmt.Fprintf(w, "clients\t%d\n", st.Clients-1) // not counting this one
	fmt.Fprintf(w, "sessions\t%d live\n", len(st.Sessions))
	w.Flush()
	if len(st.Sessions) > 0 {
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		home, _ := os.UserHomeDir()
		for _, s := range st.Sessions {
			cwd := s.CWD
			if home != "" && strings.HasPrefix(cwd, home) {
				cwd = "~" + cwd[len(home):]
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", s.ID, s.State, cwd, s.Title)
		}
		w.Flush()
	}
	printDevices(st.Devices)
}

func printDevices(devs []daemon.DeviceInfo) {
	fmt.Printf("%-10s%d paired\n", "devices", len(devs))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, d := range devs {
		seen := "last seen " + ago(d.LastSeen) + " ago"
		if d.Connected > 0 {
			seen = "connected"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\tpaired %s\n", d.ID, d.Name, seen, d.PairedAt.Local().Format("2006-01-02"))
	}
	w.Flush()
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// runPair is `claudewitz pair`: a QR code for the phone app to scan.
func runPair(args []string) {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	bin := fs.String("claude", "claude", "path to the claude CLI, if the daemon has to be started")
	_ = fs.Parse(args)
	ctx := context.Background()
	c, note, err := connectDaemon(ctx, *bin)
	if err != nil {
		fail("%v", err)
	}
	warn(note)
	defer c.Close()
	var code daemon.PairCodeResult
	call(ctx, c, "pair.code", struct{}{}, &code)
	payload, _ := json.Marshal(struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		Code string `json:"code"`
	}{code.Host, code.Port, code.Code})

	var buf bytes.Buffer
	qrterminal.GenerateWithConfig(string(payload), qrterminal.Config{
		Level: qrterminal.M, Writer: &buf, HalfBlocks: true, QuietZone: 2,
		BlackChar: qrterminal.BLACK_BLACK, WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar: qrterminal.WHITE_WHITE, BlackWhiteChar: qrterminal.BLACK_WHITE,
	})
	// Light modules are drawn in the foreground colour; pin it to white on
	// black so the code scans whatever the terminal's theme.
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Printf("\x1b[97;40m%s\x1b[0m\n", line)
	}
	fmt.Printf("\nScan this with the claudewitz app on your iPhone (over Tailscale).\n")
	fmt.Printf("Host %s, port %d, code %s; it works once and expires at %s.\n",
		code.Host, code.Port, code.Code, code.ExpiresAt.Local().Format("15:04"))
	suggestInstall()
}

// runDevices is `claudewitz devices [revoke <name|id>]`.
func runDevices(args []string) {
	ctx := context.Background()
	c := dialRunning(ctx)
	if c != nil {
		defer c.Close()
	}
	store := &daemon.Devices{Path: daemon.DevicesPath()}
	switch {
	case len(args) == 0:
		if c == nil {
			devs, err := store.List()
			if err != nil {
				fail("%v", err)
			}
			infos := make([]daemon.DeviceInfo, len(devs))
			for i, d := range devs {
				infos[i] = daemon.DeviceInfo{ID: d.ID, Name: d.Name, PairedAt: d.PairedAt, LastSeen: d.LastSeen}
			}
			printDevices(infos)
			return
		}
		var r daemon.DevicesResult
		call(ctx, c, "devices.list", struct{}{}, &r)
		printDevices(r.Devices)
	case args[0] == "revoke" && len(args) == 2:
		var d daemon.DeviceInfo
		if c == nil {
			dev, err := store.Revoke(args[1])
			if err != nil {
				var e *daemon.Error
				if errors.As(err, &e) {
					fail("%s", e.Message)
				}
				fail("%v", err)
			}
			d = daemon.DeviceInfo{ID: dev.ID, Name: dev.Name}
		} else {
			var r daemon.RevokeResult
			call(ctx, c, "devices.revoke", daemon.RevokeParams{Device: args[1]}, &r)
			d = r.Device
		}
		fmt.Printf("Revoked %s (%s).\n", d.Name, d.ID)
	default:
		fmt.Fprintln(os.Stderr, "usage: claudewitz devices [revoke <name|id>]")
		os.Exit(2)
	}
}
