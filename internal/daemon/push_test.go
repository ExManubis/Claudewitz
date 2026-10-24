package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/exmanubis/claudewitz/internal/apns"
)

// writeTestKey writes a fresh .p8 key to dir and returns its path.
func writeTestKey(t *testing.T, dir string) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "AuthKey_TEST.p8")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeAPNs records pushes. A token in gone gets 410 Unregistered.
type fakeAPNs struct {
	srv  *httptest.Server
	mu   sync.Mutex
	got  []apnsPush
	gone map[string]bool
}

type apnsPush struct {
	Token, PushType, Topic, Priority string
	Payload                          map[string]any
}

func newFakeAPNs(t *testing.T) *fakeAPNs {
	t.Helper()
	f := &fakeAPNs{gone: map[string]bool{}}
	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p := apnsPush{
			Token: strings.TrimPrefix(r.URL.Path, "/3/device/"), PushType: r.Header.Get("apns-push-type"),
			Topic: r.Header.Get("apns-topic"), Priority: r.Header.Get("apns-priority"),
		}
		_ = json.Unmarshal(b, &p.Payload)
		f.mu.Lock()
		f.got = append(f.got, p)
		gone := f.gone[p.Token]
		f.mu.Unlock()
		if gone {
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
		}
	}))
	f.srv.EnableHTTP2 = true
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

// client is the Server.NewAPNs that sends here.
func (f *fakeAPNs) client(k *apns.Key) *apns.Client {
	return &apns.Client{Key: k, HTTP: f.srv.Client(), Production: f.srv.URL, Sandbox: f.srv.URL}
}

func (f *fakeAPNs) pushes() []apnsPush {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apnsPush(nil), f.got...)
}

func (f *fakeAPNs) setGone(token string) {
	f.mu.Lock()
	f.gone[token] = true
	f.mu.Unlock()
}

func TestConfigureAPNsKeepsAPrivateCopyOfTheKey(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "daemon.json")
	key := writeTestKey(t, dir)
	st, err := ConfigureAPNs(cfgPath, APNsSetParams{KeyPath: key, KeyID: "KEY1234567", TeamID: "TEAM123456"})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Configured || st.Topic != DefaultAPNsTopic {
		t.Fatalf("status %+v", st)
	}
	cfg, _ := LoadConfig(cfgPath)
	if cfg.APNs == nil || cfg.APNs.KeyPath != filepath.Join(dir, "apns.p8") {
		t.Fatalf("config %+v", cfg.APNs)
	}
	fi, err := os.Stat(cfg.APNs.KeyPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key copy: %v %v", fi, err)
	}
	// The original may go; the daemon's copy is what it uses.
	_ = os.Remove(key)
	if _, err := apns.LoadKey(cfg.APNs.KeyPath, "KEY1234567", "TEAM123456"); err != nil {
		t.Fatal(err)
	}

	if _, err := ConfigureAPNs(cfgPath, APNsSetParams{Off: true}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := LoadConfig(cfgPath); cfg.APNs != nil {
		t.Fatal("off kept the key")
	}
	if _, err := os.Stat(filepath.Join(dir, "apns.p8")); err == nil {
		t.Fatal("off left the key file behind")
	}
}

func TestConfigureAPNsRefusesABadKey(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.p8")
	_ = os.WriteFile(bad, []byte("not a key"), 0o600)
	if _, err := ConfigureAPNs(filepath.Join(dir, "daemon.json"), APNsSetParams{KeyPath: bad, KeyID: "K", TeamID: "T"}); err == nil {
		t.Fatal("accepted a bad key")
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon.json")); err == nil {
		t.Fatal("wrote daemon.json for a bad key")
	}
}

func TestAPNsSetOnARunningDaemon(t *testing.T) {
	h := startRemoteDaemon(t, "")
	local := h.local(t)
	var st StatusResult
	local.ok("daemon.status", struct{}{}, &st)
	if st.APNs.Configured {
		t.Fatal("configured from the start")
	}
	var set APNsStatus
	local.ok("apns.set", APNsSetParams{KeyPath: writeTestKey(t, t.TempDir()), KeyID: "KEY1234567", TeamID: "TEAM123456"}, &set)
	if !set.Configured || set.Error != "" {
		t.Fatalf("apns.set: %+v", set)
	}
	local.ok("daemon.status", struct{}{}, &st)
	if !st.APNs.Configured || st.APNs.KeyID != "KEY1234567" {
		t.Fatalf("status %+v", st.APNs)
	}
	if code := local.fails("apns.set", APNsSetParams{KeyPath: "/nonexistent.p8", KeyID: "K", TeamID: "T"}); code != CodeBadRequest {
		t.Fatalf("missing key file: %s", code)
	}
}

func TestAPNsSetIsLocalOnly(t *testing.T) {
	h := startRemoteDaemon(t, "")
	local := h.local(t)
	addr := remoteOn(t, local)
	phone, _ := h.dialRemote(t, addr, bearer(pairDevice(t, h, local, addr, "iPhone")))
	phone.hello()
	if code := phone.fails("apns.set", APNsSetParams{Off: true}); code != CodeLocalOnly {
		t.Fatalf("remote apns.set: %s", code)
	}
}

func TestRegisterPushStoresThePhonesToken(t *testing.T) {
	h := startRemoteDaemon(t, "")
	local := h.local(t)
	if code := local.fails("device.registerPush", RegisterPushParams{APNsToken: "abc"}); code != CodeBadRequest {
		t.Fatalf("local registerPush: %s", code)
	}
	addr := remoteOn(t, local)
	phone, _ := h.dialRemote(t, addr, bearer(pairDevice(t, h, local, addr, "iPhone")))
	phone.hello()
	phone.ok("device.registerPush", RegisterPushParams{APNsToken: "abc", Sandbox: true}, nil)
	devs, _ := h.srv.Devices.List()
	if len(devs) != 1 || devs[0].Push == nil || devs[0].Push.APNsToken != "abc" || !devs[0].Push.Sandbox {
		t.Fatalf("device %+v", devs)
	}
}

func TestSendForgetsADeadToken(t *testing.T) {
	fake := newFakeAPNs(t)
	h := startRemoteDaemonWith(t, "", func(s *Server) { s.NewAPNs = fake.client })
	local := h.local(t)
	local.ok("apns.set", APNsSetParams{KeyPath: writeTestKey(t, t.TempDir()), KeyID: "KEY1234567", TeamID: "TEAM123456"}, nil)

	n := apns.Notification{DeviceToken: "live", Topic: DefaultAPNsTopic, PushType: "alert", Payload: []byte(`{"aps":{}}`)}
	forgot := false
	if !h.srv.send(n, func() { forgot = true }) || forgot {
		t.Fatal("a good token didn't go out")
	}
	fake.setGone("dead")
	n.DeviceToken = "dead"
	if h.srv.send(n, func() { forgot = true }) || !forgot {
		t.Fatal("a dead token wasn't forgotten")
	}
	if got := fake.pushes(); len(got) != 2 || got[0].Token != "live" || got[0].PushType != "alert" {
		t.Fatalf("pushes %+v", got)
	}
}

func TestSendWithoutAPNsDoesNothing(t *testing.T) {
	h := startRemoteDaemon(t, "")
	if h.srv.send(apns.Notification{DeviceToken: "t"}, nil) {
		t.Fatal("sent with no key")
	}
}
