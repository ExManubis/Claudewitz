package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRemotePort is the remote listener's TCP port unless one is given.
const DefaultRemotePort = 7745

// pairCodeTTL is how long a pairing code from `claudewitz pair` is good for.
const pairCodeTTL = 5 * time.Minute

// pairTimeout is how long an unauthenticated remote connection may take
// to pair before it is dropped.
const pairTimeout = time.Minute

// maxPairFailures is how many wrong codes one connection may try.
const maxPairFailures = 3

// Config is the daemon's persisted settings, ~/.claudewitz/daemon.json.
type Config struct {
	Remote RemoteConfig `json:"remote"`
}

// RemoteConfig says whether the remote (Tailscale) listener is on.
type RemoteConfig struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port,omitempty"` // 0: DefaultRemotePort
}

// ConfigPath is daemon.json next to the local socket.
func ConfigPath() string { return filepath.Join(filepath.Dir(SocketPath()), "daemon.json") }

// LoadConfig reads path; a missing file is the default (remote off).
func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// SaveConfig writes path, readable by this user only.
func SaveConfig(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path, append(b, '\n'))
}

// ── Finding the Tailscale address ───────────────────────────────────────

var tailnet = netip.MustParsePrefix("100.64.0.0/10")

// ErrNoTailscale means this machine has no Tailscale address right now.
var ErrNoTailscale = errors.New("Tailscale isn't up: this machine has no Tailscale address (100.64.0.0/10). Start Tailscale and log in, then try again")

// TailscaleIP is this machine's Tailscale IPv4 address: what `tailscale
// ip -4` says, if the CLI is installed and the address is on a local
// interface, else the 100.64.0.0/10 address of a local interface.
func TailscaleIP() (netip.Addr, error) {
	ifaces, err := interfaceAddrs()
	if err != nil {
		return netip.Addr{}, err
	}
	if ip, ok := tailscaleCLI(); ok {
		for _, a := range ifaces {
			if a.ip == ip {
				return ip, nil
			}
		}
	}
	if ip, ok := pickTailscale(ifaces); ok {
		return ip, nil
	}
	return netip.Addr{}, ErrNoTailscale
}

type ifaceAddr struct {
	iface string
	ip    netip.Addr
}

func interfaceAddrs() ([]ifaceAddr, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []ifaceAddr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if pn, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(pn.IP); ok {
					out = append(out, ifaceAddr{ifc.Name, ip.Unmap()})
				}
			}
		}
	}
	return out, nil
}

// pickTailscale is the address in Tailscale's range, preferring an
// interface named for Tailscale (Linux) over others (utunN on macOS).
func pickTailscale(addrs []ifaceAddr) (netip.Addr, bool) {
	var found netip.Addr
	for _, a := range addrs {
		if !a.ip.Is4() || !tailnet.Contains(a.ip) {
			continue
		}
		if strings.HasPrefix(a.iface, "tailscale") {
			return a.ip, true
		}
		if !found.IsValid() {
			found = a.ip
		}
	}
	return found, found.IsValid()
}

func tailscaleCLI() (netip.Addr, bool) {
	for _, bin := range []string{"tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"} {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		out, err := exec.CommandContext(ctx, path, "ip", "-4").Output()
		cancel()
		if err != nil {
			continue
		}
		line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		if ip, err := netip.ParseAddr(strings.TrimSpace(line)); err == nil && tailnet.Contains(ip) {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// ── The remote listener ─────────────────────────────────────────────────

type remoteListener struct {
	addr netip.AddrPort
	hs   *http.Server
}

func (s *Server) loadConfig() Config {
	if s.ConfigPath == "" {
		return s.config
	}
	c, err := LoadConfig(s.ConfigPath)
	if err != nil {
		s.logf("%v", err)
	}
	return c
}

func (s *Server) saveConfig(c Config) error {
	s.config = c
	if s.ConfigPath == "" {
		return nil
	}
	return SaveConfig(s.ConfigPath, c)
}

// openRemote starts the remote listener on the Tailscale address. It is
// a no-op when the listener is already up on the right port.
func (s *Server) openRemote(port int) error {
	if port == 0 {
		port = DefaultRemotePort
	}
	addr := s.TailscaleAddr
	if addr == nil {
		addr = TailscaleIP
	}
	ip, err := addr()
	if err != nil {
		return err
	}
	if s.remote != nil {
		if s.remote.addr == netip.AddrPortFrom(ip, uint16(port)) {
			return nil
		}
		s.closeRemote()
	}
	ln, err := net.Listen("tcp", netip.AddrPortFrom(ip, uint16(port)).String())
	if err != nil {
		return fmt.Errorf("listening on %s: %w", ip, err)
	}
	r := &remoteListener{
		addr: netip.MustParseAddrPort(ln.Addr().String()),
		hs:   &http.Server{Handler: s.handler(true), ReadHeaderTimeout: 10 * time.Second},
	}
	go func() { _ = r.hs.Serve(ln) }()
	s.remote = r
	s.remoteErr = ""
	s.logf("remote listener on %s", r.addr)
	return nil
}

// closeRemote stops the remote listener and drops its connections.
func (s *Server) closeRemote() {
	if s.remote == nil {
		return
	}
	s.remote.hs.Close()
	s.remote = nil
	s.closeConns(func(c *conn) bool { return c.remote })
}

func (s *Server) closeConns(match func(*conn) bool) {
	s.mu.RLock()
	var drop []*conn
	for c := range s.conns {
		if match(c) {
			drop = append(drop, c)
		}
	}
	s.mu.RUnlock()
	for _, c := range drop {
		c.close()
	}
}

// retryRemote brings the remote listener up if it is enabled but down,
// as when the daemon started before Tailscale did.
func (s *Server) retryRemote() {
	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()
	if !s.config.Remote.Enabled || s.remote != nil {
		return
	}
	if err := s.openRemote(s.config.Remote.Port); err != nil {
		if err.Error() != s.remoteErr {
			s.logf("remote listener: %v", err)
		}
		s.remoteErr = err.Error()
	}
}

// setRemote turns the remote listener on or off and remembers it.
func (s *Server) setRemote(p RemoteSetParams) (RemoteStatus, error) {
	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()
	cfg := s.config
	if p.Enabled {
		port := p.Port
		if port == 0 {
			port = cfg.Remote.Port
		}
		if err := s.openRemote(port); err != nil {
			return RemoteStatus{}, &Error{CodeFailed, err.Error()}
		}
		cfg.Remote = RemoteConfig{Enabled: true, Port: port}
	} else {
		s.closeRemote()
		cfg.Remote.Enabled = false
		s.remoteErr = ""
	}
	if err := s.saveConfig(cfg); err != nil {
		return RemoteStatus{}, &Error{CodeFailed, "saving " + s.ConfigPath + ": " + err.Error()}
	}
	return s.remoteStatusLocked(), nil
}

func (s *Server) remoteStatus() RemoteStatus {
	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()
	return s.remoteStatusLocked()
}

func (s *Server) remoteStatusLocked() RemoteStatus {
	st := RemoteStatus{Enabled: s.config.Remote.Enabled, Error: s.remoteErr}
	if s.remote != nil {
		st.Host = s.remote.addr.Addr().String()
		st.Port = int(s.remote.addr.Port())
	}
	return st
}

// handler serves WebSocket upgrades on one listener. Remote connections
// carry a device token, or none while pairing.
func (s *Server) handler(remote bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var dev *Device
		if remote {
			// Browsers on the tailnet can open WebSockets anywhere; the
			// phone app sends no Origin.
			if r.Header.Get("Origin") != "" {
				http.Error(w, "browsers may not connect", http.StatusForbidden)
				return
			}
			if h := r.Header.Get("Authorization"); h != "" {
				token, ok := strings.CutPrefix(h, "Bearer ")
				d, known := s.devices().Authenticate(strings.TrimSpace(token))
				if !ok || !known {
					http.Error(w, "unknown device token", http.StatusUnauthorized)
					return
				}
				dev = &d
			} else if !s.pairing() {
				http.Error(w, "a device token is required (pair with `claudewitz pair`)", http.StatusUnauthorized)
				return
			}
		}
		s.serveWS(w, r, remote, dev)
	})
}

func (s *Server) devices() *Devices { return s.Devices }

// ── Pairing ─────────────────────────────────────────────────────────────

const codeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ" // 32: no 0/O, 1/I

// newPairCode makes a single-use pairing code for the remote listener.
func (s *Server) newPairCode() (PairCodeResult, error) {
	st := s.remoteStatus()
	if st.Host == "" {
		msg := "remote access is off; turn it on with `claudewitz daemon remote on`"
		if st.Enabled {
			msg = "the remote listener is down: " + st.Error
		}
		return PairCodeResult{}, &Error{CodeFailed, msg}
	}
	b := randomBytes(12)
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	code := string(b)
	exp := time.Now().Add(pairCodeTTL)
	s.mu.Lock()
	s.pruneCodes()
	s.codes[code] = exp
	s.mu.Unlock()
	return PairCodeResult{Host: st.Host, Port: st.Port, Code: code, ExpiresAt: exp.UTC().Truncate(time.Second)}, nil
}

func (s *Server) pruneCodes() {
	t := time.Now()
	for c, exp := range s.codes {
		if !t.Before(exp) {
			delete(s.codes, c)
		}
	}
}

// pairing reports whether a pairing code is outstanding; without one,
// unauthenticated remote connections are refused outright.
func (s *Server) pairing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCodes()
	return len(s.codes) > 0
}

// useCode consumes code if it is outstanding and unexpired.
func (s *Server) useCode(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneCodes()
	code = strings.ToUpper(strings.TrimSpace(code))
	for c := range s.codes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 {
			delete(s.codes, c)
			return true
		}
	}
	return false
}

func (s *Server) pair(c *conn, p PairParams) (PairResult, error) {
	if !c.remote || c.authed() {
		return PairResult{}, &Error{CodeBadRequest, "pair is only for unpaired remote connections"}
	}
	if !s.useCode(p.Code) {
		c.mu.Lock()
		c.pairFailures++
		n := c.pairFailures
		c.mu.Unlock()
		if n >= maxPairFailures {
			go c.flushAndClose()
		}
		return PairResult{}, &Error{CodeUnauthorized, "invalid or expired pairing code"}
	}
	name := strings.TrimSpace(p.DeviceName)
	if name == "" {
		name = "iPhone"
	}
	dev, token, err := s.devices().Add(firstLine(name, 60))
	if err != nil {
		return PairResult{}, &Error{CodeFailed, err.Error()}
	}
	s.logf("paired %s (%s)", dev.Name, dev.ID)
	// This connection carries on as the new device.
	c.mu.Lock()
	c.device = dev.ID
	c.mu.Unlock()
	c.ws.SetReadLimit(64 << 20)
	return PairResult{Token: token, DeviceID: dev.ID}, nil
}

// ── Devices and status ─────────────────────────────────────────────────

func (s *Server) deviceList() ([]DeviceInfo, error) {
	all, err := s.devices().List()
	if err != nil {
		return nil, &Error{CodeFailed, err.Error()}
	}
	live := map[string]int{}
	s.mu.RLock()
	for c := range s.conns {
		if id := c.deviceID(); id != "" {
			live[id]++
		}
	}
	s.mu.RUnlock()
	out := make([]DeviceInfo, len(all))
	for i, d := range all {
		out[i] = deviceInfo(d)
		out[i].Connected = live[d.ID]
	}
	return out, nil
}

func deviceInfo(d Device) DeviceInfo {
	return DeviceInfo{ID: d.ID, Name: d.Name, PairedAt: d.PairedAt, LastSeen: d.LastSeen}
}

// revoke unpairs a device and drops its live connections.
func (s *Server) revoke(ref string) (DeviceInfo, error) {
	d, err := s.devices().Revoke(ref)
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			return DeviceInfo{}, e
		}
		return DeviceInfo{}, &Error{CodeFailed, err.Error()}
	}
	s.closeConns(func(c *conn) bool { return c.deviceID() == d.ID })
	s.logf("revoked %s (%s)", d.Name, d.ID)
	return deviceInfo(d), nil
}

func (s *Server) status() (StatusResult, error) {
	devs, err := s.deviceList()
	if err != nil {
		return StatusResult{}, err
	}
	st := StatusResult{
		PID:           os.Getpid(),
		DaemonVersion: s.Version,
		StartedAt:     s.started,
		Listeners:     []Listener{{Kind: "local", Address: s.local}},
		Remote:        s.remoteStatus(),
		Sessions:      s.list(),
		Devices:       devs,
	}
	if st.Remote.Host != "" {
		st.Listeners = append(st.Listeners, Listener{Kind: "remote",
			Address: netip.AddrPortFrom(netip.MustParseAddr(st.Remote.Host), uint16(st.Remote.Port)).String()})
	}
	s.mu.RLock()
	for c := range s.conns {
		if c.ready() {
			st.Clients++
		}
	}
	s.mu.RUnlock()
	return st, nil
}
