package launchd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

// fakeLaunchctl is a launchctl that keeps the one job's state in a folder:
// "loaded" holds the running daemon's pid ("" while not running). Every
// call is appended to "calls".
const fakeLaunchctl = `#!/bin/sh
dir="$(dirname "$0")"
echo "$*" >> "$dir/calls"
case "$1" in
print)
	[ -f "$dir/loaded" ] || { echo "Could not find service" >&2; exit 113; }
	echo "	state = running"
	pid="$(cat "$dir/loaded")"
	[ -n "$pid" ] && echo "	pid = $pid"
	exit 0 ;;
bootstrap)
	[ -f "$dir/loaded" ] && { echo "Bootstrap failed: 5: Input/output error" >&2; exit 5; }
	cat "$dir/nextpid" > "$dir/loaded" 2>/dev/null || : > "$dir/loaded"
	exit 0 ;;
bootout)
	[ -f "$dir/loaded" ] || { echo "Boot-out failed: 3: No such process" >&2; exit 3; }
	rm "$dir/loaded"; exit 0 ;;
kickstart)
	[ -f "$dir/loaded" ] || exit 113
	exit 0 ;;
esac
exit 64
`

type harness struct {
	t   *testing.T
	a   *Agent
	bin string // the fake launchctl's folder
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell")
	}
	home, bin := t.TempDir(), t.TempDir()
	bin, _ = filepath.EvalSymlinks(bin) // /var → /private/var on macOS
	lc := filepath.Join(bin, "launchctl")
	if err := os.WriteFile(lc, []byte(fakeLaunchctl), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "claudewitz")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, bin: bin, a: &Agent{
		Home: home, UID: 501, GOOS: "darwin", Launchctl: lc, Exe: exe, PathEnv: "/opt/homebrew/bin:/usr/bin:/bin",
	}}
}

// loaded makes launchd's job loaded, running pid (0: not running).
func (h *harness) loaded(pid int) {
	h.t.Helper()
	s := ""
	if pid > 0 {
		s = itoa(pid)
	}
	if err := os.WriteFile(filepath.Join(h.bin, "loaded"), []byte(s), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// nextPID is the pid the job runs once bootstrapped.
func (h *harness) nextPID(pid int) {
	_ = os.WriteFile(filepath.Join(h.bin, "nextpid"), []byte(itoa(pid)), 0o644)
}

func (h *harness) calls() []string {
	b, _ := os.ReadFile(filepath.Join(h.bin, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (h *harness) called(verb string) bool {
	for _, c := range h.calls() {
		if strings.HasPrefix(c, verb+" ") {
			return true
		}
	}
	return false
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// fakeDaemon answers daemon.status and records the rest.
type fakeDaemon struct {
	st    daemon.StatusResult
	calls []string
	idle  *int
}

func (d *fakeDaemon) Call(_ context.Context, method string, params, result any) error {
	d.calls = append(d.calls, method)
	switch method {
	case "daemon.status":
		b, _ := json.Marshal(d.st)
		return json.Unmarshal(b, result)
	case "daemon.setIdle":
		n := params.(daemon.IdleParams).Seconds
		d.idle = &n
		d.st.IdleSeconds = n
	}
	return nil
}

func (d *fakeDaemon) did(method string) bool {
	for _, c := range d.calls {
		if c == method {
			return true
		}
	}
	return false
}

func live(n int) []daemon.SessionSummary { return make([]daemon.SessionSummary, n) }

func TestInstallFresh(t *testing.T) {
	h := newHarness(t)
	res, err := h.a.Install(context.Background(), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Already || res.Handover || res.Exe != h.a.Exe {
		t.Fatalf("result %+v", res)
	}
	b, err := os.ReadFile(h.a.PlistPath())
	if err != nil {
		t.Fatal(err)
	}
	p := string(b)
	for _, want := range []string{
		"<string>ai.claudewitz.daemon</string>",
		"<string>" + h.a.Exe + "</string>",
		"<string>-idle</string>\n\t\t<string>0</string>\n\t\t<string>-wait</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>SuccessfulExit</key>\n\t\t<false/>",
		"<string>/opt/homebrew/bin:/usr/bin:/bin</string>",
		"<string>" + filepath.Join(h.a.Home, ".claudewitz", "daemon.log") + "</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
	if fi, err := os.Stat(filepath.Join(h.a.Home, ".claudewitz")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("log folder: %v %v", fi, err)
	}
	if got := h.calls(); got[len(got)-1] != "bootstrap gui/501 "+h.a.PlistPath() {
		t.Errorf("calls %q", got)
	}
	if !h.a.Installed() || !strings.HasPrefix(h.a.Mode(nil), "always on") {
		t.Errorf("Installed %v, Mode %q", h.a.Installed(), h.a.Mode(nil))
	}

	// Again: nothing to do.
	res, err = h.a.Install(context.Background(), nil, true)
	if err != nil || !res.Already {
		t.Fatalf("second install: %+v %v", res, err)
	}
}

func TestPlistLints(t *testing.T) {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("no plutil")
	}
	h := newHarness(t)
	h.a.PathEnv = "/a&b:/usr/bin"
	if _, err := h.a.Install(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(plutil, "-lint", h.a.PlistPath()).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v\n%s", err, out)
	}
}

func TestInstallEscapesXML(t *testing.T) {
	h := newHarness(t)
	h.a.PathEnv = "/a&b/<bin>"
	if _, err := h.a.Install(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(h.a.PlistPath())
	if !strings.Contains(string(b), "<string>/a&amp;b/&lt;bin&gt;</string>") {
		t.Fatalf("PATH not escaped:\n%s", b)
	}
}

func TestInstallKeepsOnDemandDaemonWithSessions(t *testing.T) {
	h := newHarness(t)
	h.nextPID(0) // launchd's daemon waits for the socket
	d := &fakeDaemon{st: daemon.StatusResult{PID: 4242, IdleSeconds: 600, Clients: 1, Sessions: live(2)}}
	res, err := h.a.Install(context.Background(), d, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Handover || res.PID != 4242 || res.Stopped {
		t.Fatalf("result %+v", res)
	}
	if d.did("daemon.stop") {
		t.Fatal("stopped a daemon with live sessions")
	}
	if !strings.Contains(res.Message(), "takes over") {
		t.Errorf("message %q", res.Message())
	}
	if m := h.a.Mode(&d.st); !strings.Contains(m, "takes over when it exits") {
		t.Errorf("mode %q", m)
	}
}

func TestInstallStopsIdleOnDemandDaemon(t *testing.T) {
	h := newHarness(t)
	d := &fakeDaemon{st: daemon.StatusResult{PID: 4242, IdleSeconds: 600, Clients: 1}}
	res, err := h.a.Install(context.Background(), d, true)
	if err != nil || !res.Stopped || res.Handover {
		t.Fatalf("result %+v %v", res, err)
	}

	// From the TUI (stopIdle false) it is left alone.
	h = newHarness(t)
	d = &fakeDaemon{st: daemon.StatusResult{PID: 4242, IdleSeconds: 600, Clients: 1}}
	res, err = h.a.Install(context.Background(), d, false)
	if err != nil || res.Stopped || !res.Handover || d.did("daemon.stop") {
		t.Fatalf("from the TUI: %+v %v %v", res, err, d.calls)
	}
}

func TestInstallReloadsChangedDefinition(t *testing.T) {
	h := newHarness(t)
	if _, err := h.a.Install(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}
	h.loaded(777)
	moved := filepath.Join(h.bin, "claudewitz2")
	if err := os.WriteFile(moved, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.a.Exe = moved

	// Its daemon has sessions: refused, nothing touched.
	d := &fakeDaemon{st: daemon.StatusResult{PID: 777, Sessions: live(1), Clients: 1}}
	if _, err := h.a.Install(context.Background(), d, true); err == nil || !strings.Contains(err.Error(), "live session") {
		t.Fatalf("reinstall over live sessions: %v", err)
	}
	if h.called("bootout") {
		t.Fatal("booted out a daemon with live sessions")
	}

	// Idle: reloaded.
	d.st.Sessions = nil
	if _, err := h.a.Install(context.Background(), d, true); err != nil {
		t.Fatal(err)
	}
	if !h.called("bootout") {
		t.Fatal("changed definition not reloaded")
	}
	b, _ := os.ReadFile(h.a.PlistPath())
	if !strings.Contains(string(b), moved) {
		t.Fatalf("plist not rewritten:\n%s", b)
	}
}

func TestUninstall(t *testing.T) {
	h := newHarness(t)
	if _, err := h.a.Uninstall(context.Background(), nil); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("uninstall with nothing installed: %v", err)
	}
	if _, err := h.a.Install(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}
	h.loaded(777)
	d := &fakeDaemon{st: daemon.StatusResult{PID: 777, Clients: 1}} // just the caller
	res, err := h.a.Uninstall(context.Background(), d)
	if err != nil || res.Lingers {
		t.Fatalf("result %+v %v", res, err)
	}
	if !h.called("bootout") || h.a.Installed() || h.a.Job().Loaded {
		t.Fatalf("not unloaded: %q", h.calls())
	}
	if !strings.HasPrefix(h.a.Mode(nil), "on demand") {
		t.Errorf("mode %q", h.a.Mode(nil))
	}
}

func TestUninstallKeepsSessions(t *testing.T) {
	h := newHarness(t)
	if _, err := h.a.Install(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}
	h.loaded(777)
	d := &fakeDaemon{st: daemon.StatusResult{PID: 777, Clients: 2, Sessions: live(3)}}
	res, err := h.a.Uninstall(context.Background(), d)
	if err != nil || !res.Lingers || res.Sessions != 3 {
		t.Fatalf("result %+v %v", res, err)
	}
	if h.called("bootout") {
		t.Fatal("booted out a daemon with live sessions")
	}
	if d.idle == nil || *d.idle != int(IdleTimeout.Seconds()) {
		t.Fatalf("idle timeout not restored: %v", d.idle)
	}
	if h.a.Installed() {
		t.Fatal("plist left behind")
	}
	if m := h.a.Mode(&d.st); !strings.Contains(m, "no longer kept running") {
		t.Errorf("mode %q", m)
	}

	// Installing again keeps that daemon instead of reloading it.
	ins, err := h.a.Install(context.Background(), d, true)
	if err != nil || !ins.Resumed {
		t.Fatalf("reinstall: %+v %v", ins, err)
	}
	if *d.idle != 0 || h.called("bootout") || !h.a.Installed() {
		t.Fatalf("not resumed: idle %d, calls %q", *d.idle, h.calls())
	}
}

func TestUninstallOnDemandDaemonUnaffected(t *testing.T) {
	// launchd's daemon is waiting (no pid) behind an on-demand one with
	// sessions: unloading only ends the waiting one.
	h := newHarness(t)
	if _, err := h.a.Install(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}
	h.loaded(0)
	d := &fakeDaemon{st: daemon.StatusResult{PID: 4242, IdleSeconds: 600, Clients: 2, Sessions: live(1)}}
	res, err := h.a.Uninstall(context.Background(), d)
	if err != nil || res.Lingers || d.did("daemon.setIdle") || !h.called("bootout") {
		t.Fatalf("result %+v %v, daemon calls %v", res, err, d.calls)
	}
}

func TestUnsupported(t *testing.T) {
	h := newHarness(t)
	h.a.GOOS = "linux"
	if _, err := h.a.Install(context.Background(), nil, true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("install on linux: %v", err)
	}
	if _, err := h.a.Uninstall(context.Background(), nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("uninstall on linux: %v", err)
	}
	if h.a.Installed() || h.a.Start() {
		t.Fatal("installed on linux")
	}
}

func TestStart(t *testing.T) {
	h := newHarness(t)
	if h.a.Start() {
		t.Fatal("started while not installed")
	}
	if _, err := h.a.Install(context.Background(), nil, true); err != nil {
		t.Fatal(err)
	}
	if !h.a.Start() || !h.called("kickstart") {
		t.Fatalf("not kickstarted: %q", h.calls())
	}
}

func TestStablePath(t *testing.T) {
	prefix := t.TempDir()
	cask := filepath.Join(prefix, "Caskroom", "claudewitz", "0.3.0")
	if err := os.MkdirAll(cask, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(cask, "claudewitz")
	if err := os.WriteFile(real, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	prefix, _ = filepath.EvalSymlinks(prefix) // /var → /private/var on macOS
	real = filepath.Join(prefix, "Caskroom", "claudewitz", "0.3.0", "claudewitz")

	// No bin link: the binary itself.
	if got := StablePath(real); got != real {
		t.Errorf("without a link: %s", got)
	}
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(prefix, "bin", "claudewitz")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if got := StablePath(real); got != link {
		t.Errorf("from the Caskroom: %s, want %s", got, link)
	}
	if got := StablePath(link); got != link {
		t.Errorf("from the link: %s, want %s", got, link)
	}
	other := filepath.Join(t.TempDir(), "claudewitz")
	_ = os.WriteFile(other, nil, 0o755)
	other, _ = filepath.EvalSymlinks(other)
	if got := StablePath(other); got != other {
		t.Errorf("elsewhere: %s", got)
	}
}
