package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// idleProc is a claude process that does nothing until stopped.
type idleProc struct {
	events chan claude.Event
	once   sync.Once
}

func (p *idleProc) Events() <-chan claude.Event                                        { return p.events }
func (p *idleProc) Send(string) error                                                  { return nil }
func (p *idleProc) SendWithImages(string, []claude.Image) error                        { return nil }
func (p *idleProc) Respond(*claude.PermissionRequest, claude.PermissionDecision) error { return nil }
func (p *idleProc) RespondAndInterrupt(*claude.PermissionRequest, claude.PermissionDecision) error {
	return nil
}
func (p *idleProc) Interrupt() error               { return nil }
func (p *idleProc) SetModel(string) error          { return nil }
func (p *idleProc) SetEffort(string) error         { return nil }
func (p *idleProc) SetPermissionMode(string) error { return nil }
func (p *idleProc) Stop() {
	p.once.Do(func() { p.events <- claude.Event{Exited: true}; close(p.events) })
}

type remoteHarness struct {
	dir    string
	sock   string
	srv    *Server
	ip     netip.Addr
	ipErr  error
	mu     sync.Mutex
	cancel func()
	done   chan struct{}
}

// startRemoteDaemon runs a daemon whose "Tailscale address" is loopback.
func startRemoteDaemon(t *testing.T, dir string) *remoteHarness {
	t.Helper()
	return startRemoteDaemonWith(t, dir, nil)
}

// startRemoteDaemonWith is startRemoteDaemon with setup run on the Server
// before it starts serving.
func startRemoteDaemonWith(t *testing.T, dir string, setup func(*Server)) *remoteHarness {
	t.Helper()
	if dir == "" {
		var err error
		// Unix socket paths are limited to ~104 bytes; t.TempDir is too long on macOS.
		if dir, err = os.MkdirTemp("/tmp", "cwr"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	h := &remoteHarness{dir: dir, sock: filepath.Join(dir, "d.sock"), ip: netip.MustParseAddr("127.0.0.1")}
	ln, err := Listen(h.sock)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = &Server{
		Version:    "test",
		Log:        log.New(io.Discard, "", 0),
		ConfigPath: filepath.Join(dir, "daemon.json"),
		Devices:    &Devices{Path: filepath.Join(dir, "devices.json")},
		TailscaleAddr: func() (netip.Addr, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.ip, h.ipErr
		},
		Launch: func(context.Context, claude.Options) (claude.Session, error) {
			return &idleProc{events: make(chan claude.Event, 4)}, nil
		},
	}
	if setup != nil {
		setup(h.srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan struct{})
	go func() { _ = h.srv.Serve(ctx, ln); close(h.done) }()
	t.Cleanup(h.stop)
	return h
}

func (h *remoteHarness) stop() {
	h.cancel()
	<-h.done
}

// rpc is a bare WebSocket client, as the phone app would be.
type rpc struct {
	t      *testing.T
	ws     *websocket.Conn
	nextID int64
}

func (r *rpc) call(method string, params any) Frame {
	r.t.Helper()
	r.nextID++
	p, _ := json.Marshal(params)
	if err := r.ws.WriteJSON(Request{ID: r.nextID, Method: method, Params: p}); err != nil {
		r.t.Fatalf("%s: %v", method, err)
	}
	for {
		_ = r.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		var f Frame
		if err := r.ws.ReadJSON(&f); err != nil {
			r.t.Fatalf("%s: %v", method, err)
		}
		if f.Event == "" && f.ID == r.nextID {
			return f
		}
	}
}

// ok calls method and decodes its result, failing on an error.
func (r *rpc) ok(method string, params, result any) {
	r.t.Helper()
	f := r.call(method, params)
	if f.Error != nil {
		r.t.Fatalf("%s: %v", method, f.Error)
	}
	if result != nil {
		if err := json.Unmarshal(f.Result, result); err != nil {
			r.t.Fatal(err)
		}
	}
}

// fails calls method and returns its error code.
func (r *rpc) fails(method string, params any) string {
	r.t.Helper()
	f := r.call(method, params)
	if f.Error == nil {
		r.t.Fatalf("%s succeeded: %s", method, f.Result)
	}
	return f.Error.Code
}

func (r *rpc) hello() {
	r.t.Helper()
	r.ok("hello", HelloParams{Protocol: Protocol, Client: "ios"}, nil)
}

// closed reports whether the daemon has dropped the connection.
func (r *rpc) closed() bool {
	_ = r.ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := r.ws.ReadMessage(); err != nil {
			var ne net.Error
			return !(errors.As(err, &ne) && ne.Timeout())
		}
	}
}

func (h *remoteHarness) local(t *testing.T) *rpc {
	t.Helper()
	d := websocket.Dialer{NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var nd net.Dialer
		return nd.DialContext(ctx, "unix", h.sock)
	}}
	ws, _, err := d.Dial("ws://claudewitz/", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	r := &rpc{t: t, ws: ws}
	r.ok("hello", HelloParams{Protocol: Protocol, Client: "cli"}, nil)
	return r
}

// dialRemote connects to the remote listener; status is the HTTP status
// of a refused upgrade.
func (h *remoteHarness) dialRemote(t *testing.T, addr string, header http.Header) (*rpc, int) {
	t.Helper()
	ws, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/", header)
	if err != nil {
		if resp == nil {
			t.Fatal(err)
		}
		return nil, resp.StatusCode
	}
	t.Cleanup(func() { ws.Close() })
	return &rpc{t: t, ws: ws}, http.StatusSwitchingProtocols
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// remoteOn turns remote access on and returns the listener's address.
func remoteOn(t *testing.T, local *rpc) string {
	t.Helper()
	var st RemoteStatus
	local.ok("remote.set", RemoteSetParams{Enabled: true, Port: freePort(t)}, &st)
	return net.JoinHostPort(st.Host, itoa(st.Port))
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// pairDevice pairs a phone and returns its token.
func pairDevice(t *testing.T, h *remoteHarness, local *rpc, addr, name string) string {
	t.Helper()
	var code PairCodeResult
	local.ok("pair.code", struct{}{}, &code)
	r, status := h.dialRemote(t, addr, nil)
	if r == nil {
		t.Fatalf("pairing connection refused: %d", status)
	}
	r.hello()
	var res PairResult
	r.ok("pair", PairParams{Code: code.Code, DeviceName: name}, &res)
	if res.Token == "" || res.DeviceID == "" {
		t.Fatalf("pair result %+v", res)
	}
	return res.Token
}

func TestRemoteBindsTailscaleAddressOnly(t *testing.T) {
	h := startRemoteDaemon(t, "")
	local := h.local(t)

	// No Tailscale: a clear error, and nothing is remembered.
	h.mu.Lock()
	h.ipErr = ErrNoTailscale
	h.mu.Unlock()
	f := local.call("remote.set", RemoteSetParams{Enabled: true, Port: freePort(t)})
	if f.Error == nil || !strings.Contains(f.Error.Message, "Tailscale isn't up") {
		t.Fatalf("remote on without Tailscale: %+v", f.Error)
	}
	if cfg, _ := LoadConfig(h.srv.ConfigPath); cfg.Remote.Enabled {
		t.Fatal("remote was saved as on")
	}

	h.mu.Lock()
	h.ipErr = nil
	h.mu.Unlock()
	port := freePort(t)
	var st RemoteStatus
	local.ok("remote.set", RemoteSetParams{Enabled: true, Port: port}, &st)
	if st.Host != "127.0.0.1" || st.Port != port || !st.Enabled {
		t.Fatalf("status %+v", st)
	}
	h.srv.remoteMu.Lock()
	bound := h.srv.remote.addr
	h.srv.remoteMu.Unlock()
	if bound != netip.AddrPortFrom(h.ip, uint16(port)) {
		t.Fatalf("bound to %s", bound)
	}
	var status StatusResult
	local.ok("daemon.status", struct{}{}, &status)
	if len(status.Listeners) != 2 || status.Listeners[1].Address != bound.String() {
		t.Fatalf("listeners %+v", status.Listeners)
	}
	if fi, err := os.Stat(h.srv.ConfigPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v %v", fi, err)
	}

	// The setting survives a restart.
	h.stop()
	h2 := startRemoteDaemon(t, h.dir)
	eventually(t, "remote listener after restart", func() bool { return h2.srv.remoteStatus().Port == port })

	// Off closes the listener and is remembered.
	local2 := h2.local(t)
	st = RemoteStatus{}
	local2.ok("remote.set", RemoteSetParams{Enabled: false}, &st)
	if st.Enabled || st.Host != "" {
		t.Fatalf("status after off %+v", st)
	}
	if _, err := net.DialTimeout("tcp", bound.String(), time.Second); err == nil {
		t.Fatal("remote listener still accepting")
	}
	if cfg, _ := LoadConfig(h2.srv.ConfigPath); cfg.Remote.Enabled || cfg.Remote.Port != port {
		t.Fatalf("saved config %+v", cfg)
	}
}

func TestPickTailscale(t *testing.T) {
	a := func(iface, ip string) ifaceAddr { return ifaceAddr{iface, netip.MustParseAddr(ip)} }
	cases := []struct {
		addrs []ifaceAddr
		want  string
	}{
		{[]ifaceAddr{a("lo0", "127.0.0.1"), a("en0", "192.168.1.4")}, ""},
		{[]ifaceAddr{a("en0", "192.168.1.4"), a("utun4", "100.101.102.103")}, "100.101.102.103"},
		{[]ifaceAddr{a("en0", "100.128.0.1")}, ""}, // just outside 100.64.0.0/10
		{[]ifaceAddr{a("utun3", "100.64.0.9"), a("tailscale0", "100.100.1.2")}, "100.100.1.2"},
		{[]ifaceAddr{a("utun4", "fd7a:115c:a1e0::1")}, ""},
	}
	for _, c := range cases {
		ip, ok := pickTailscale(c.addrs)
		got := ""
		if ok {
			got = ip.String()
		}
		if got != c.want {
			t.Errorf("pickTailscale(%v) = %q, want %q", c.addrs, got, c.want)
		}
	}
}

func TestRemoteAuth(t *testing.T) {
	h := startRemoteDaemon(t, "")
	local := h.local(t)
	addr := remoteOn(t, local)

	// No token and no pairing under way: refused at the upgrade.
	if _, status := h.dialRemote(t, addr, nil); status != http.StatusUnauthorized {
		t.Fatalf("no token: %d", status)
	}
	if _, status := h.dialRemote(t, addr, bearer("nonsense")); status != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", status)
	}
	if _, status := h.dialRemote(t, addr, http.Header{"Authorization": {"Basic eDp5"}}); status != http.StatusUnauthorized {
		t.Fatalf("basic auth: %d", status)
	}

	// While pairing, an unpaired connection may say hello and pair, nothing else.
	var code PairCodeResult
	local.ok("pair.code", struct{}{}, &code)
	if code.Host != "127.0.0.1" || len(code.Code) != 12 || time.Until(code.ExpiresAt) > pairCodeTTL {
		t.Fatalf("code %+v", code)
	}
	if _, status := h.dialRemote(t, addr, http.Header{"Origin": {"https://evil.example"}}); status != http.StatusForbidden {
		t.Fatalf("browser origin: %d", status)
	}
	r, _ := h.dialRemote(t, addr, nil)
	r.hello()
	if c := r.fails("sessions.list", struct{}{}); c != CodeUnauthorized {
		t.Fatalf("unpaired sessions.list: %s", c)
	}
	if !r.closed() {
		t.Fatal("unpaired connection left open after a refused call")
	}

	// Wrong codes; the third drops the connection.
	r, _ = h.dialRemote(t, addr, nil)
	r.hello()
	for i := 0; i < maxPairFailures; i++ {
		if c := r.fails("pair", PairParams{Code: "WRONGWRONGWR", DeviceName: "x"}); c != CodeUnauthorized {
			t.Fatalf("wrong code: %s", c)
		}
	}
	if !r.closed() {
		t.Fatal("still open after repeated wrong codes")
	}

	// The right code pairs, and that connection carries on as the device.
	r, _ = h.dialRemote(t, addr, nil)
	r.hello()
	var res PairResult
	r.ok("pair", PairParams{Code: strings.ToLower(code.Code), DeviceName: "Mikkel's iPhone"}, &res)
	r.ok("sessions.list", struct{}{}, nil)

	// The code is single use.
	if _, status := h.dialRemote(t, addr, nil); status != http.StatusUnauthorized {
		t.Fatalf("after the only code was used: %d", status)
	}
	var other PairCodeResult
	local.ok("pair.code", struct{}{}, &other)
	r2, _ := h.dialRemote(t, addr, nil)
	r2.hello()
	if c := r2.fails("pair", PairParams{Code: code.Code, DeviceName: "again"}); c != CodeUnauthorized {
		t.Fatalf("reused code: %s", c)
	}

	// An expired code is refused.
	h.srv.mu.Lock()
	h.srv.codes[other.Code] = time.Now().Add(-time.Second)
	h.srv.mu.Unlock()
	if c := r2.fails("pair", PairParams{Code: other.Code, DeviceName: "late"}); c != CodeUnauthorized {
		t.Fatalf("expired code: %s", c)
	}

	// The token works on later connections; the file holds only its hash.
	r3, status := h.dialRemote(t, addr, bearer(res.Token))
	if r3 == nil {
		t.Fatalf("token refused: %d", status)
	}
	r3.hello()
	r3.ok("sessions.list", struct{}{}, nil)
	b, err := os.ReadFile(h.srv.Devices.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), res.Token) || !strings.Contains(string(b), hashToken(res.Token)) {
		t.Fatal("devices.json should hold the token's hash, not the token")
	}
	if fi, _ := os.Stat(h.srv.Devices.Path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("devices.json mode %v", fi.Mode().Perm())
	}

	var devs DevicesResult
	local.ok("devices.list", struct{}{}, &devs)
	if len(devs.Devices) != 1 || devs.Devices[0].Name != "Mikkel's iPhone" || devs.Devices[0].Connected != 2 {
		t.Fatalf("devices %+v", devs.Devices)
	}

	// Revoking drops the device's live connections and its token.
	var rev RevokeResult
	local.ok("devices.revoke", RevokeParams{Device: "mikkel's iphone"}, &rev)
	if rev.Device.ID != res.DeviceID {
		t.Fatalf("revoked %+v", rev.Device)
	}
	if !r.closed() || !r3.closed() {
		t.Fatal("revoked device still connected")
	}
	if _, status := h.dialRemote(t, addr, bearer(res.Token)); status != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", status)
	}
	if c := local.fails("devices.revoke", RevokeParams{Device: res.DeviceID}); c != CodeNotFound {
		t.Fatalf("revoking twice: %s", c)
	}
}

func TestRemoteIsLiveSessionsOnly(t *testing.T) {
	h := startRemoteDaemon(t, "")
	local := h.local(t)
	addr := remoteOn(t, local)
	token := pairDevice(t, h, local, addr, "phone")

	var started StartResult
	local.ok("session.start", StartParams{CWD: h.dir}, &started)

	r, _ := h.dialRemote(t, addr, bearer(token))
	r.hello()
	var list ListResult
	r.ok("sessions.list", struct{}{}, &list)
	if len(list.Sessions) != 1 || list.Sessions[0].ID != started.Session.ID {
		t.Fatalf("remote sees %+v", list.Sessions)
	}
	for method, params := range map[string]any{
		"session.start":    StartParams{CWD: h.dir},
		"sessions.history": struct{}{},
		"daemon.status":    struct{}{},
		"remote.set":       RemoteSetParams{Enabled: false},
		"pair.code":        struct{}{},
		"devices.list":     struct{}{},
		"devices.revoke":   RevokeParams{Device: "phone"},
	} {
		if c := r.fails(method, params); c != CodeLocalOnly {
			t.Errorf("remote %s: %s, want local_only", method, c)
		}
	}
	// A paired connection can't pair again, nor can a local one.
	if c := r.fails("pair", PairParams{Code: "X"}); c != CodeBadRequest {
		t.Errorf("pair when paired: %s", c)
	}
	if c := local.fails("pair", PairParams{Code: "X"}); c != CodeBadRequest {
		t.Errorf("pair on local: %s", c)
	}
	// Driving a live session works remotely.
	r.ok("session.send", SendParams{Session: started.Session.ID, Text: "hi"}, nil)

	// Turning remote off drops remote clients.
	local.ok("remote.set", RemoteSetParams{Enabled: false}, nil)
	if !r.closed() {
		t.Fatal("remote client still connected after remote off")
	}
}

// The phone gets the day's recap, tallied from the transcripts on the Mac.
func TestRecapOverRemote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-x-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"11111111-1111-1111-1111-111111111111","timestamp":"2026-10-15T08:00:00Z","cwd":"/x/app","message":{"role":"user","content":"Add a recap"}}`
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := startRemoteDaemon(t, "")
	local := h.local(t)
	addr := remoteOn(t, local)
	token := pairDevice(t, h, local, addr, "phone")
	r, _ := h.dialRemote(t, addr, bearer(token))
	r.hello()

	var day struct {
		Date     string `json:"date"`
		Prompts  int    `json:"prompts"`
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	r.ok("recap.day", RecapParams{Date: "2026-10-15", TZ: "Europe/Copenhagen"}, &day)
	if day.Date != "2026-10-15" || day.Prompts != 1 || len(day.Projects) != 1 || day.Projects[0].Name != "app" {
		t.Fatalf("recap %+v", day)
	}
	r.ok("recap.day", RecapParams{Date: "2026-10-14", TZ: "Europe/Copenhagen"}, &day)
	if day.Prompts != 0 {
		t.Fatalf("the day before: %+v", day)
	}
	if c := r.fails("recap.day", RecapParams{TZ: "Mars/Olympus"}); c != CodeBadRequest {
		t.Fatalf("bad zone: %s", c)
	}
}

func TestPairCodeNeedsRemote(t *testing.T) {
	h := startRemoteDaemon(t, "")
	local := h.local(t)
	f := local.call("pair.code", struct{}{})
	if f.Error == nil || !strings.Contains(f.Error.Message, "remote on") {
		t.Fatalf("pair.code with remote off: %+v", f.Error)
	}
}

func TestDevicesRevokeAmbiguousName(t *testing.T) {
	dir := t.TempDir()
	d := &Devices{Path: filepath.Join(dir, "devices.json")}
	a, _, _ := d.Add("iPhone")
	if _, _, err := d.Add("iPhone"); err != nil {
		t.Fatal(err)
	}
	var e *Error
	if _, err := d.Revoke("iphone"); !errors.As(err, &e) || e.Code != CodeBadRequest {
		t.Fatalf("ambiguous revoke: %v", err)
	}
	if got, err := d.Revoke(a.ID); err != nil || got.ID != a.ID {
		t.Fatalf("revoke by id: %v %v", got, err)
	}
	if got, err := d.Revoke("iPhone"); err != nil || got.ID == a.ID {
		t.Fatalf("revoke by name: %v %v", got, err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSetIdle(t *testing.T) {
	h := startRemoteDaemon(t, "")
	local := h.local(t)
	var st StatusResult
	local.ok("daemon.status", struct{}{}, &st)
	if st.IdleSeconds != 0 {
		t.Fatalf("idleSeconds %d, want 0", st.IdleSeconds)
	}
	local.ok("daemon.setIdle", IdleParams{Seconds: 600}, nil)
	local.ok("daemon.status", struct{}{}, &st)
	if st.IdleSeconds != 600 {
		t.Fatalf("idleSeconds %d after setIdle, want 600", st.IdleSeconds)
	}
	if code := local.fails("daemon.setIdle", IdleParams{Seconds: -1}); code != CodeBadRequest {
		t.Fatalf("negative setIdle: %q", code)
	}
}
