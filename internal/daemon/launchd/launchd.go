// Package launchd keeps the claudewitz daemon running at login on macOS,
// as a LaunchAgent: `claudewitz daemon install|uninstall` and the TUI's
// palette share it.
//
// The agent runs `claudewitz daemon -idle 0 -wait`. -wait makes it wait for
// an on-demand daemon that already holds the socket instead of failing, so
// installing never ends live sessions: launchd's daemon takes over when the
// on-demand one idles out. KeepAlive restarts it only when it fails, so
// `claudewitz daemon stop` still stops it (a TUI starts it again with
// Start); uninstalling hands a daemon that still has sessions back to the
// usual idle timeout rather than killing it.
package launchd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

// Label names the LaunchAgent, and its plist file.
const Label = "ai.claudewitz.daemon"

// IdleTimeout is how long an on-demand daemon stays up with nothing to do;
// an always-on daemon that is no longer kept running gets it back.
const IdleTimeout = 10 * time.Minute

var (
	ErrUnsupported  = errors.New("keeping the daemon running is only supported on macOS so far")
	ErrNotInstalled = errors.New("the daemon isn't set to keep running")
)

// Caller is a connection to the running daemon (*client.Client).
type Caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// Agent is the LaunchAgent of one user. The zero value is not usable; see
// New.
type Agent struct {
	Home      string // the user's home folder
	UID       int    // for launchctl's gui/<uid> domain
	GOOS      string
	Launchctl string // the launchctl binary
	Exe       string // the running claudewitz; "" is os.Executable
	PathEnv   string // PATH for the daemon, so it finds claude, node and git
}

// New is the current user's agent.
func New() *Agent {
	home, _ := os.UserHomeDir()
	return &Agent{
		Home:      home,
		UID:       os.Getuid(),
		GOOS:      runtime.GOOS,
		Launchctl: "launchctl",
		PathEnv:   os.Getenv("PATH"),
	}
}

// PlistPath is ~/Library/LaunchAgents/ai.claudewitz.daemon.plist.
func (a *Agent) PlistPath() string {
	return filepath.Join(a.Home, "Library", "LaunchAgents", Label+".plist")
}

// LogPath is where the agent's daemon writes, as an on-demand one does.
func (a *Agent) LogPath() string { return filepath.Join(a.Home, ".claudewitz", "daemon.log") }

// Supported reports whether this system has launchd.
func (a *Agent) Supported() bool { return a.GOOS == "darwin" }

// Installed reports whether the daemon is set to run at login.
func (a *Agent) Installed() bool {
	if !a.Supported() {
		return false
	}
	_, err := os.Stat(a.PlistPath())
	return err == nil
}

// Job is what launchd has loaded under Label.
type Job struct {
	Loaded bool
	PID    int // 0 while not running
}

// Job asks launchctl about the agent.
func (a *Agent) Job() Job {
	out, err := a.launchctl("print", a.target())
	if err != nil {
		return Job{}
	}
	j := Job{Loaded: true}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "pid = "); ok {
			j.PID, _ = strconv.Atoi(v)
			break
		}
	}
	return j
}

// Start asks launchd to start the installed daemon if it isn't running
// (after `claudewitz daemon stop`, or an upgrade). It reports whether it
// asked.
func (a *Agent) Start() bool {
	if !a.Installed() {
		return false
	}
	_, err := a.launchctl("kickstart", a.target())
	return err == nil
}

// Installed is what Install did.
type Installed struct {
	Exe     string // what the agent runs
	Already bool   // nothing changed
	// Handover: an on-demand daemon (PID) is running, and launchd's takes
	// over when it exits.
	Handover bool
	PID      int
	Stopped  bool // the on-demand daemon was idle and was stopped for launchd's
	Resumed  bool // a daemon that was going to idle out stays up again
}

// Message says what happened, for the CLI and the TUI.
func (r Installed) Message() string {
	switch {
	case r.Already:
		return "The daemon is already kept running (it starts at login)."
	case r.Resumed:
		return "The daemon is kept running again (it starts at login)."
	case r.Handover:
		return fmt.Sprintf("The daemon will start at login and keep running. The one running now (pid %d) was started on demand "+
			"and keeps its sessions; launchd's takes over once it has been idle for %d minutes.", r.PID, int(IdleTimeout/time.Minute))
	default:
		return "The daemon now starts at login and keeps running."
	}
}

// Install writes the LaunchAgent and loads it. d is the running daemon, or
// nil; stopIdle lets Install stop it when nothing uses it (a TUI is using
// it, so only the CLI passes true). Live sessions are never ended.
func (a *Agent) Install(ctx context.Context, d Caller, stopIdle bool) (Installed, error) {
	if !a.Supported() {
		return Installed{}, ErrUnsupported
	}
	exe, err := a.exe()
	if err != nil {
		return Installed{}, err
	}
	res := Installed{Exe: exe}
	want := a.plist(exe)
	have, _ := os.ReadFile(a.PlistPath())
	job := a.Job()
	st := status(ctx, d)
	ours := st != nil && job.PID > 0 && st.PID == job.PID

	switch {
	case job.Loaded && bytes.Equal(have, want):
		res.Already = true
		return res, nil
	case ours && have == nil && st.IdleSeconds > 0:
		// Uninstalled while it had sessions, so it was left to idle out;
		// keep it instead.
		if err := a.writePlist(want); err != nil {
			return res, err
		}
		if err := d.Call(ctx, "daemon.setIdle", daemon.IdleParams{Seconds: 0}, nil); err != nil {
			return res, err
		}
		res.Resumed = true
		return res, nil
	case ours && len(st.Sessions) > 0:
		// The definition changed (claudewitz moved); reloading it would end
		// the running daemon and its sessions.
		return res, fmt.Errorf("the daemon launchd runs has %d live session(s), which reinstalling would end; "+
			"try again once they're done", len(st.Sessions))
	case job.Loaded:
		_, _ = a.launchctl("bootout", a.target())
		ours = false
	}
	if err := a.writePlist(want); err != nil {
		return res, err
	}
	if _, err := a.launchctl("bootstrap", a.domain(), a.PlistPath()); err != nil {
		return res, err
	}
	if st == nil || ours {
		return res, nil
	}
	// An on-demand daemon has the socket; launchd's waits for it.
	if stopIdle && len(st.Sessions) == 0 && st.Clients <= 1 {
		if err := d.Call(ctx, "daemon.stop", daemon.StopParams{}, nil); err == nil {
			res.Stopped = true
			return res, nil
		}
	}
	res.Handover, res.PID = true, st.PID
	return res, nil
}

// Uninstalled is what Uninstall did.
type Uninstalled struct {
	// Lingers: launchd's daemon had sessions or other clients, so it keeps
	// running and idles out as an on-demand daemon would.
	Lingers  bool
	Sessions int
}

func (r Uninstalled) Message() string {
	if r.Lingers {
		what := "other clients"
		if r.Sessions > 0 {
			what = fmt.Sprintf("%d live session(s)", r.Sessions)
		}
		return fmt.Sprintf("The daemon no longer starts at login. The one running now keeps its %s, "+
			"and exits once it has been idle for %d minutes.", what, int(IdleTimeout/time.Minute))
	}
	return "The daemon no longer starts at login; it runs on demand again."
}

// Uninstall unloads the LaunchAgent and removes its plist. A daemon of the
// agent's that still has sessions or other clients is left running with
// the usual idle timeout rather than ended.
func (a *Agent) Uninstall(ctx context.Context, d Caller) (Uninstalled, error) {
	var res Uninstalled
	if !a.Supported() {
		return res, ErrUnsupported
	}
	job := a.Job()
	if err := os.Remove(a.PlistPath()); errors.Is(err, fs.ErrNotExist) {
		if !job.Loaded {
			return res, ErrNotInstalled
		}
	} else if err != nil {
		return res, err
	}
	if !job.Loaded {
		return res, nil
	}
	if st := status(ctx, d); st != nil && job.PID > 0 && st.PID == job.PID && (len(st.Sessions) > 0 || st.Clients > 1) {
		// It exits 0 when idle, and launchd only restarts it on failure; it
		// stays loaded (doing nothing) until logout.
		if err := d.Call(ctx, "daemon.setIdle", daemon.IdleParams{Seconds: int(IdleTimeout / time.Second)}, nil); err != nil {
			return res, err
		}
		res.Lingers, res.Sessions = true, len(st.Sessions)
		return res, nil
	}
	if _, err := a.launchctl("bootout", a.target()); err != nil {
		return res, err
	}
	return res, nil
}

// Mode describes how the daemon runs, for `claudewitz daemon status` and
// /daemon. st is the running daemon's status, or nil.
func (a *Agent) Mode(st *daemon.StatusResult) string {
	onDemand := fmt.Sprintf("on demand (exits after %d idle minutes)", int(IdleTimeout/time.Minute))
	if !a.Supported() {
		return onDemand
	}
	job := a.Job()
	switch {
	case a.Installed():
		if st != nil && st.IdleSeconds > 0 {
			return "always on (starts at login); the daemon running now was started on demand, and launchd's takes over when it exits"
		}
		return "always on (starts at login)"
	case job.Loaded && st != nil && st.PID == job.PID && st.IdleSeconds > 0:
		return fmt.Sprintf("on demand; no longer kept running, the daemon exits after %d idle minutes", int(IdleTimeout/time.Minute))
	}
	return onDemand
}

func status(ctx context.Context, d Caller) *daemon.StatusResult {
	if d == nil {
		return nil
	}
	var st daemon.StatusResult
	if err := d.Call(ctx, "daemon.status", struct{}{}, &st); err != nil {
		return nil
	}
	return &st
}

func (a *Agent) domain() string { return "gui/" + strconv.Itoa(a.UID) }
func (a *Agent) target() string { return a.domain() + "/" + Label }

func (a *Agent) launchctl(args ...string) ([]byte, error) {
	out, err := exec.Command(a.Launchctl, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("launchctl %s: %s", args[0], msg)
	}
	return out, nil
}

func (a *Agent) exe() (string, error) {
	exe := a.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return "", err
		}
	}
	return StablePath(exe), nil
}

// StablePath is the path to run claudewitz from that survives upgrades:
// Homebrew installs each version under Caskroom/<version> (or Cellar), and
// links $(brew --prefix)/bin/claudewitz to the current one.
func StablePath(exe string) string {
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}
	for _, dir := range []string{"/Caskroom/", "/Cellar/"} {
		i := strings.Index(real, dir)
		if i < 0 {
			continue
		}
		link := filepath.Join(real[:i], "bin", filepath.Base(real))
		a, err1 := os.Stat(link)
		b, err2 := os.Stat(real)
		if err1 == nil && err2 == nil && os.SameFile(a, b) {
			return link
		}
	}
	return real
}

func (a *Agent) writePlist(b []byte) error {
	if err := os.MkdirAll(filepath.Dir(a.LogPath()), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.PlistPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(a.PlistPath(), b, 0o644)
}

// plist is the LaunchAgent definition for exe.
func (a *Agent) plist(exe string) []byte {
	var b bytes.Buffer
	str := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return "<string>" + e.String() + "</string>"
	}
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	%s
	<key>ProgramArguments</key>
	<array>
		%s
		<string>daemon</string>
		<string>-idle</string>
		<string>0</string>
		<string>-wait</string>
	</array>
`, str(Label), str(exe))
	if a.PathEnv != "" {
		fmt.Fprintf(&b, `	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		%s
	</dict>
`, str(a.PathEnv))
	}
	fmt.Fprintf(&b, `	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	%s
	<key>StandardErrorPath</key>
	%s
</dict>
</plist>
`, str(a.LogPath()), str(a.LogPath()))
	return b.Bytes()
}
