// Package config persists user preferences between runs.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Config is stored as JSON at Path().
type Config struct {
	Theme string `json:"theme,omitempty"`
	// Appearance forces the light or dark variant of the theme: "auto"
	// (the default, detected from the terminal), "dark" or "light".
	Appearance string `json:"appearance,omitempty"`
	// Notifications controls desktop notifications sent while the terminal
	// is unfocused: "auto" (default), "native", "osc", "bell" or "off".
	Notifications string `json:"notifications,omitempty"`
}

// HistoryPath is where claudewitz keeps prompt history, next to the config.
func HistoryPath() string {
	return filepath.Join(filepath.Dir(Path()), "history.jsonl")
}

// Path is $CLAUDEWITZ_CONFIG, else <user config dir>/claudewitz/config.json
// (~/.config/claudewitz/config.json on Linux, ~/Library/Application
// Support/claudewitz/config.json on macOS).
func Path() string {
	if p := os.Getenv("CLAUDEWITZ_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "claudewitz", "config.json")
}

// Load reads the config. A missing file is not an error.
func Load() (Config, error) {
	var c Config
	data, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(data, &c)
	return c, err
}

// Save writes the config atomically.
func (c Config) Save() error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".config-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}
