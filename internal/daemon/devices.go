package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Device is a paired phone. Its token is stored only as a hash.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash"`
	PairedAt  time.Time `json:"pairedAt"`
	LastSeen  time.Time `json:"lastSeen"`
}

// Devices is the paired devices file, ~/.claudewitz/devices.json (0600).
// Every operation reads the file afresh, so `claudewitz devices` can edit
// it while no daemon is running.
type Devices struct {
	Path string
	mu   sync.Mutex
}

type devicesFile struct {
	Devices []Device `json:"devices"`
}

// DevicesPath is devices.json next to the local socket.
func DevicesPath() string { return filepath.Join(filepath.Dir(SocketPath()), "devices.json") }

func (d *Devices) load() ([]Device, error) {
	b, err := os.ReadFile(d.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f devicesFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", d.Path, err)
	}
	return f.Devices, nil
}

func (d *Devices) save(all []Device) error {
	if all == nil {
		all = []Device{}
	}
	b, err := json.MarshalIndent(devicesFile{all}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(d.Path, append(b, '\n'))
}

// List returns the paired devices, oldest first.
func (d *Devices) List() ([]Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.load()
}

// Add pairs a new device and returns it with its token, which is shown
// to the device once and never stored.
func (d *Devices) Add(name string) (Device, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	all, err := d.load()
	if err != nil {
		return Device{}, "", err
	}
	token := randomString(32)
	t := now()
	dev := Device{ID: "d_" + hex.EncodeToString(randomBytes(4)), Name: name, TokenHash: hashToken(token), PairedAt: t, LastSeen: t}
	if err := d.save(append(all, dev)); err != nil {
		return Device{}, "", err
	}
	return dev, token, nil
}

// Authenticate finds the device a token belongs to and marks it seen.
func (d *Devices) Authenticate(token string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	all, err := d.load()
	if err != nil {
		return Device{}, false
	}
	h := hashToken(token)
	for i := range all {
		if subtle.ConstantTimeCompare([]byte(all[i].TokenHash), []byte(h)) == 1 {
			all[i].LastSeen = now()
			_ = d.save(all)
			return all[i], true
		}
	}
	return Device{}, false
}

// Seen updates a device's last seen time.
func (d *Devices) Seen(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	all, err := d.load()
	if err != nil {
		return
	}
	for i := range all {
		if all[i].ID == id {
			all[i].LastSeen = now()
			_ = d.save(all)
			return
		}
	}
}

// Revoke removes the device with id or name ref. A name shared by several
// devices is refused; revoke those by id.
func (d *Devices) Revoke(ref string) (Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	all, err := d.load()
	if err != nil {
		return Device{}, err
	}
	match := -1
	for i, dev := range all {
		if dev.ID == ref {
			match = i
			break
		}
	}
	if match < 0 {
		for i, dev := range all {
			if strings.EqualFold(dev.Name, ref) {
				if match >= 0 {
					return Device{}, &Error{CodeBadRequest, fmt.Sprintf("several devices are called %q; revoke one by id", ref)}
				}
				match = i
			}
		}
	}
	if match < 0 {
		return Device{}, &Error{CodeNotFound, "no paired device " + ref}
	}
	dev := all[match]
	all = append(all[:match:match], all[match+1:]...)
	return dev, d.save(all)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return b
}

func randomString(n int) string { return base64.RawURLEncoding.EncodeToString(randomBytes(n)) }

// writePrivate replaces path with data, readable by this user only.
func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
