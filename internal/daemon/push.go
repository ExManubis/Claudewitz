package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/exmanubis/claudewitz/internal/apns"
)

// The daemon pushes to the iPhone app through APNs itself, signing with a
// .p8 key from the Apple developer account (`claudewitz daemon apns`), so
// no server sits between the Mac and the phone. See docs/daemon.md, Push
// notifications.

// DefaultAPNsTopic is the iPhone app's bundle id.
const DefaultAPNsTopic = "ai.claudewitz.ios"

// pushTimeout bounds one push, so a slow APNs never holds anything up.
const pushTimeout = 15 * time.Second

// APNsConfig is the signing key, in daemon.json. KeyPath is the daemon's
// own copy of the .p8 file (apns.p8 next to daemon.json).
type APNsConfig struct {
	KeyPath string `json:"keyPath"`
	KeyID   string `json:"keyId"`
	TeamID  string `json:"teamId"`
	Topic   string `json:"topic,omitempty"` // "": DefaultAPNsTopic
}

func (c *APNsConfig) topic() string {
	if c == nil || c.Topic == "" {
		return DefaultAPNsTopic
	}
	return c.Topic
}

// APNsSetParams configures APNs (apns.set); Off removes it.
type APNsSetParams struct {
	KeyPath string `json:"keyPath,omitempty"`
	KeyID   string `json:"keyId,omitempty"`
	TeamID  string `json:"teamId,omitempty"`
	Topic   string `json:"topic,omitempty"`
	Off     bool   `json:"off,omitempty"`
}

// APNsStatus is whether the daemon can push, for `claudewitz daemon status`.
type APNsStatus struct {
	Configured bool   `json:"configured"`
	KeyID      string `json:"keyId,omitempty"`
	TeamID     string `json:"teamId,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Error      string `json:"error,omitempty"` // why a configured key can't be used
}

// RegisterPushParams is the phone's alert device token (device.registerPush).
type RegisterPushParams struct {
	APNsToken string `json:"apnsToken"`
	Sandbox   bool   `json:"sandbox"`
}

// ConfigureAPNs checks the key in p, copies it next to configPath (0600)
// and records it in daemon.json; with p.Off it forgets the key and deletes
// the copy. It works
// with no daemon running; a running one is told through apns.set, which
// calls this.
func ConfigureAPNs(configPath string, p APNsSetParams) (APNsStatus, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return APNsStatus{}, err
	}
	ownCopy := filepath.Join(filepath.Dir(configPath), "apns.p8")
	if p.Off {
		cfg.APNs = nil
		if err := SaveConfig(configPath, cfg); err != nil {
			return APNsStatus{}, err
		}
		// A signing key shouldn't outlive its use.
		if err := os.Remove(ownCopy); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return APNsStatus{}, err
		}
		return APNsStatus{}, nil
	}
	if p.KeyPath == "" {
		return APNsStatus{}, errors.New("give the .p8 key file with -key")
	}
	b, err := os.ReadFile(p.KeyPath)
	if err != nil {
		return APNsStatus{}, err
	}
	if _, err := apns.ParseKey(b, p.KeyID, p.TeamID); err != nil {
		return APNsStatus{}, err
	}
	if err := writePrivate(ownCopy, b); err != nil {
		return APNsStatus{}, err
	}
	cfg.APNs = &APNsConfig{KeyPath: ownCopy, KeyID: p.KeyID, TeamID: p.TeamID, Topic: p.Topic}
	if err := SaveConfig(configPath, cfg); err != nil {
		return APNsStatus{}, err
	}
	return apnsStatus(cfg.APNs, ""), nil
}

func apnsStatus(c *APNsConfig, problem string) APNsStatus {
	if c == nil {
		return APNsStatus{}
	}
	return APNsStatus{Configured: true, KeyID: c.KeyID, TeamID: c.TeamID, Topic: c.topic(), Error: problem}
}

// loadAPNs builds the push client from the config. Callers hold remoteMu.
func (s *Server) loadAPNs() {
	s.apns, s.apnsErr = nil, ""
	c := s.config.APNs
	if c == nil {
		return
	}
	k, err := apns.LoadKey(c.KeyPath, c.KeyID, c.TeamID)
	if err != nil {
		s.apnsErr = err.Error()
		s.logf("APNs: %v", err)
		return
	}
	client := &apns.Client{Key: k}
	if s.NewAPNs != nil {
		client = s.NewAPNs(k)
	}
	s.apns = client
}

// setAPNs is apns.set: configure (or remove) the key and start using it.
func (s *Server) setAPNs(p APNsSetParams) (APNsStatus, error) {
	if s.ConfigPath == "" {
		return APNsStatus{}, &Error{CodeFailed, "this daemon has no daemon.json to keep the key in"}
	}
	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()
	if _, err := ConfigureAPNs(s.ConfigPath, p); err != nil {
		return APNsStatus{}, &Error{CodeBadRequest, err.Error()}
	}
	s.config = s.loadConfig()
	s.loadAPNs()
	if s.apns != nil {
		s.logf("APNs key %s (team %s)", s.config.APNs.KeyID, s.config.APNs.TeamID)
	}
	return apnsStatus(s.config.APNs, s.apnsErr), nil
}

func (s *Server) apnsStatus() APNsStatus {
	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()
	return apnsStatus(s.config.APNs, s.apnsErr)
}

// pusher is the APNs client and topic, or nil while APNs isn't set up.
func (s *Server) pusher() (*apns.Client, string) {
	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()
	return s.apns, s.config.APNs.topic()
}

// registerPush stores a paired phone's alert token (device.registerPush).
func (s *Server) registerPush(c *conn, p RegisterPushParams) (any, error) {
	dev := c.deviceID()
	if dev == "" {
		return nil, &Error{CodeBadRequest, "only a paired phone registers for pushes"}
	}
	if p.APNsToken == "" {
		return nil, &Error{CodeBadRequest, "apnsToken is required"}
	}
	err := s.devices().UpdatePush(dev, func(push *Push) {
		push.APNsToken, push.Sandbox = p.APNsToken, p.Sandbox
	})
	return struct{}{}, wrap(err)
}

// send pushes n and, when APNs says the token is dead, calls gone so the
// caller forgets it. It reports whether the push went out.
func (s *Server) send(n apns.Notification, gone func()) bool {
	client, _ := s.pusher()
	if client == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	err := client.Send(ctx, n)
	switch {
	case err == nil:
		return true
	case apns.IsGone(err):
		s.logf("APNs: dropping a dead token (%v)", err)
		if gone != nil {
			gone()
		}
	default:
		s.logf("APNs: %v", err)
	}
	return false
}
