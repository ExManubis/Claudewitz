// Package apns sends push notifications through Apple's Push Notification
// service with token-based auth: a .p8 signing key from the Apple developer
// account signs a short-lived JWT, sent with every request over HTTP/2. The
// daemon uses it to reach the iPhone app directly, with no server between.
package apns

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// Apple's hosts. A device token belongs to one of them: development
// builds (Xcode) get sandbox tokens, TestFlight and the App Store
// production ones.
const (
	ProductionHost = "https://api.push.apple.com"
	SandboxHost    = "https://api.sandbox.push.apple.com"
)

// tokenTTL is how long a signed JWT is reused. Apple refuses ones older
// than an hour and throttles providers that sign a new one more often than
// every 20 minutes.
const tokenTTL = 50 * time.Minute

// Key is a token-auth signing key: the .p8 file and the ids Apple shows
// with it.
type Key struct {
	ID     string // the key id, 10 characters
	TeamID string
	priv   *ecdsa.PrivateKey
}

// LoadKey reads a .p8 key file.
func LoadKey(path, keyID, teamID string) (*Key, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseKey(b, keyID, teamID)
}

// ParseKey parses the PEM contents of a .p8 key file.
func ParseKey(p8 []byte, keyID, teamID string) (*Key, error) {
	if keyID == "" || teamID == "" {
		return nil, errors.New("an APNs key needs its key id and team id")
	}
	block, _ := pem.Decode(p8)
	if block == nil {
		return nil, errors.New("not a .p8 key: no PEM block")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("not a .p8 key: %w", err)
	}
	priv, ok := k.(*ecdsa.PrivateKey)
	if !ok || priv.Curve.Params().BitSize != 256 {
		return nil, errors.New("not an APNs key: expected a P-256 (ES256) key")
	}
	return &Key{ID: keyID, TeamID: teamID, priv: priv}, nil
}

// sign makes the provider token: an ES256 JWT naming the key and team.
func (k *Key) sign(at time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": k.ID})
	claims, _ := json.Marshal(map[string]any{"iss": k.TeamID, "iat": at.Unix()})
	unsigned := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, sum[:])
	if err != nil {
		return "", err
	}
	// JWS wants r and s as fixed 32-byte big-endian halves, not ASN.1.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return unsigned + "." + enc.EncodeToString(sig), nil
}

// Notification is one push.
type Notification struct {
	DeviceToken string // hex, as the app got it from iOS
	Sandbox     bool   // the token is a development one
	Topic       string // the app's bundle id, with .push-type.liveactivity for Live Activities
	PushType    string // "alert", "liveactivity", …
	Priority    int    // 10: now; 5: when convenient for the device (0 leaves it to Apple)
	// Expiration is when Apple stops trying to deliver it; zero means once only.
	Expiration time.Time
	Payload    []byte // the JSON body, {"aps": …}
}

// Error is APNs refusing a push.
type Error struct {
	Status int
	Reason string // e.g. BadDeviceToken, Unregistered, TooManyProviderTokenUpdates
}

func (e *Error) Error() string { return fmt.Sprintf("APNs %d %s", e.Status, e.Reason) }

// Gone reports whether the device token will never work again: the app was
// removed, the token was for the other environment, or (for a Live
// Activity) the activity has ended. The caller should forget the token.
func (e *Error) Gone() bool {
	return e.Status == http.StatusGone || e.Reason == "BadDeviceToken" || e.Reason == "Unregistered" ||
		e.Reason == "DeviceTokenNotForTopic"
}

// IsGone reports whether err says the device token is dead.
func IsGone(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Gone()
}

// Client sends pushes signed with one key. Safe for concurrent use.
type Client struct {
	Key *Key
	// HTTP sends the requests; nil means a client with a 15s timeout.
	HTTP *http.Client
	// Production and Sandbox override Apple's hosts (tests).
	Production, Sandbox string
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	token    string
	signedAt time.Time
}

// Send delivers n, or says why APNs refused it.
func (c *Client) Send(ctx context.Context, n Notification) error {
	token, err := c.providerToken()
	if err != nil {
		return err
	}
	host := c.Production
	if host == "" {
		host = ProductionHost
	}
	if n.Sandbox {
		host = c.Sandbox
		if host == "" {
			host = SandboxHost
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/3/device/"+n.DeviceToken, bytes.NewReader(n.Payload))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", n.Topic)
	if n.PushType != "" {
		req.Header.Set("apns-push-type", n.PushType)
	}
	if n.Priority != 0 {
		req.Header.Set("apns-priority", strconv.Itoa(n.Priority))
	}
	exp := "0"
	if !n.Expiration.IsZero() {
		exp = strconv.FormatInt(n.Expiration.Unix(), 10)
	}
	req.Header.Set("apns-expiration", exp)
	req.Header.Set("content-type", "application/json")

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body)
	if body.Reason == "ExpiredProviderToken" {
		c.mu.Lock()
		c.token = "" // sign afresh next time
		c.mu.Unlock()
	}
	return &Error{Status: resp.StatusCode, Reason: body.Reason}
}

// providerToken is the current JWT, signed afresh every tokenTTL.
func (c *Client) providerToken() (string, error) {
	if c.Key == nil {
		return "", errors.New("APNs isn't configured")
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && t.Sub(c.signedAt) < tokenTTL {
		return c.token, nil
	}
	tok, err := c.Key.sign(t)
	if err != nil {
		return "", err
	}
	c.token, c.signedAt = tok, t
	return tok, nil
}
