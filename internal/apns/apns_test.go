package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testKey(t *testing.T) (*Key, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	p8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	k, err := ParseKey(p8, "KEY1234567", "TEAM123456")
	if err != nil {
		t.Fatal(err)
	}
	return k, p8
}

// verify checks a JWT made by sign against the key's public half.
func verify(k *Key, jwt string) (header, claims map[string]any, ok bool) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil, nil, false
	}
	enc := base64.RawURLEncoding
	sig, err := enc.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, nil, false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&k.priv.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, nil, false
	}
	h, _ := enc.DecodeString(parts[0])
	c, _ := enc.DecodeString(parts[1])
	_ = json.Unmarshal(h, &header)
	_ = json.Unmarshal(c, &claims)
	return header, claims, true
}

func TestParseKeyRefusesOtherKeys(t *testing.T) {
	if _, err := ParseKey([]byte("hello"), "K", "T"); err == nil {
		t.Fatal("parsed something that isn't PEM")
	}
	rsaish := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}})
	if _, err := ParseKey(rsaish, "K", "T"); err == nil {
		t.Fatal("parsed garbage DER")
	}
	_, p8 := testKey(t)
	if _, err := ParseKey(p8, "", "T"); err == nil {
		t.Fatal("accepted a key without its id")
	}
}

func TestProviderTokenIsSignedAndReused(t *testing.T) {
	k, _ := testKey(t)
	at := time.Unix(1_792_836_000, 0)
	c := &Client{Key: k, Now: func() time.Time { return at }}
	tok, err := c.providerToken()
	if err != nil {
		t.Fatal(err)
	}
	h, claims, ok := verify(k, tok)
	if !ok {
		t.Fatal("signature doesn't verify")
	}
	if h["alg"] != "ES256" || h["kid"] != "KEY1234567" || claims["iss"] != "TEAM123456" || claims["iat"] != float64(at.Unix()) {
		t.Fatalf("header %v claims %v", h, claims)
	}
	at = at.Add(49 * time.Minute)
	if again, _ := c.providerToken(); again != tok {
		t.Fatal("signed a new token within the hour")
	}
	at = at.Add(2 * time.Minute)
	if again, _ := c.providerToken(); again == tok {
		t.Fatal("kept a token past 50 minutes")
	}
}

type seen struct {
	path    string
	header  http.Header
	payload string
}

func apnsServer(t *testing.T, status int, reason string) (*httptest.Server, *[]seen) {
	t.Helper()
	var mu sync.Mutex
	var got []seen
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("APNs needs HTTP/2, got %s", r.Proto)
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, seen{r.URL.Path, r.Header.Clone(), string(b)})
		mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"reason": reason})
		}
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestSendPostsToTheDevicesEnvironment(t *testing.T) {
	k, _ := testKey(t)
	prod, prodSeen := apnsServer(t, http.StatusOK, "")
	sandbox, sandboxSeen := apnsServer(t, http.StatusOK, "")
	c := &Client{Key: k, HTTP: prod.Client(), Production: prod.URL, Sandbox: sandbox.URL}
	// Both test servers share one certificate pool setup per server; trust both.
	c.HTTP.Transport.(*http.Transport).TLSClientConfig.RootCAs.AddCert(sandbox.Certificate())

	n := Notification{
		DeviceToken: "abc123", Topic: "ai.claudewitz.ios.push-type.liveactivity", PushType: "liveactivity",
		Priority: 5, Expiration: time.Unix(1_792_900_000, 0), Payload: []byte(`{"aps":{}}`),
	}
	if err := c.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	n.Sandbox = true
	if err := c.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(*prodSeen) != 1 || len(*sandboxSeen) != 1 {
		t.Fatalf("production got %d, sandbox %d", len(*prodSeen), len(*sandboxSeen))
	}
	s := (*prodSeen)[0]
	if s.path != "/3/device/abc123" || s.payload != `{"aps":{}}` {
		t.Fatalf("path %s payload %s", s.path, s.payload)
	}
	for h, want := range map[string]string{
		"apns-topic": "ai.claudewitz.ios.push-type.liveactivity", "apns-push-type": "liveactivity",
		"apns-priority": "5", "apns-expiration": "1792900000",
	} {
		if got := s.header.Get(h); got != want {
			t.Errorf("%s: %q, want %q", h, got, want)
		}
	}
	bearer, ok := strings.CutPrefix(s.header.Get("authorization"), "bearer ")
	if _, _, valid := verify(k, bearer); !ok || !valid {
		t.Fatalf("authorization %q", s.header.Get("authorization"))
	}
}

func TestRefusalsSayWhetherTheTokenIsGone(t *testing.T) {
	k, _ := testKey(t)
	for _, tc := range []struct {
		status int
		reason string
		gone   bool
	}{
		{http.StatusGone, "Unregistered", true},
		{http.StatusBadRequest, "BadDeviceToken", true},
		{http.StatusBadRequest, "DeviceTokenNotForTopic", true},
		{http.StatusTooManyRequests, "TooManyRequests", false},
		{http.StatusForbidden, "InvalidProviderToken", false},
	} {
		srv, _ := apnsServer(t, tc.status, tc.reason)
		c := &Client{Key: k, HTTP: srv.Client(), Production: srv.URL}
		err := c.Send(context.Background(), Notification{DeviceToken: "t", Topic: "x", Payload: []byte("{}")})
		var e *Error
		if !errors.As(err, &e) || e.Status != tc.status || e.Reason != tc.reason || IsGone(err) != tc.gone {
			t.Errorf("%d %s: err %v, gone %v", tc.status, tc.reason, err, IsGone(err))
		}
	}
}

func TestAnExpiredProviderTokenIsSignedAfresh(t *testing.T) {
	k, _ := testKey(t)
	srv, _ := apnsServer(t, http.StatusForbidden, "ExpiredProviderToken")
	c := &Client{Key: k, HTTP: srv.Client(), Production: srv.URL}
	_ = c.Send(context.Background(), Notification{DeviceToken: "t", Topic: "x", Payload: []byte("{}")})
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		t.Fatal("kept a token Apple called expired")
	}
}

func TestSendWithoutAKeyFails(t *testing.T) {
	if err := (&Client{}).Send(context.Background(), Notification{}); err == nil {
		t.Fatal("sent without a key")
	}
}
