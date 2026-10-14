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
	// is unfocused: "auto" (default), "osc", "bell" or "off".
	Notifications string `json:"notifications,omitempty"`
	// Icons is "nerd" (default, needs a Nerd Font) or "unicode".
	Icons string `json:"icons,omitempty"`
	// Diff lays out edit diffs: "auto" (default, side by side when wide
	// enough), "split" or "unified".
	Diff string `json:"diff,omitempty"`
}

// HistoryPath is where claudewitz keeps prompt history, next to the config.
func HistoryPath() string {
	return filepath.Join(filepath.Dir(Path()), "history.jsonl")
}

// Path is $CLAUDEWITZ_CONFIG, else config.json in Dir().
func Path() string {
	if p := os.Getenv("CLAUDEWITZ_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Dir(), "config.json")
}

// Dir is claudewitz's folder under $XDG_CONFIG_HOME, or ~/.config/claudewitz
// on every platform (including macOS, where Go would otherwise pick
// ~/Library/Application Support).
func Dir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "claudewitz")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "claudewitz")
	}
	return filepath.Join(home, ".config", "claudewitz")
}

// legacyDir is where earlier versions kept their files: Go's user config
// dir, which differs from Dir on macOS and Windows.
func legacyDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "claudewitz")
}

// Migrate moves the config and prompt history from the old location into
// Dir, once: only when Dir has neither file yet. It reports what it moved.
func Migrate() ([]string, error) {
	if os.Getenv("CLAUDEWITZ_CONFIG") != "" {
		return nil, nil
	}
	return migrate(legacyDir(), Dir())
}

func migrate(from, to string) ([]string, error) {
	if from == "" || filepath.Clean(from) == filepath.Clean(to) {
		return nil, nil
	}
	files := []string{"config.json", "history.jsonl"}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(to, f)); err == nil {
			return nil, nil // already set up in the new place
		}
	}
	var moved []string
	for _, f := range files {
		src := filepath.Join(from, f)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.MkdirAll(to, 0o755); err != nil {
			return moved, err
		}
		dst := filepath.Join(to, f)
		if err := os.Rename(src, dst); err != nil {
			// Different filesystems: copy, then remove the original.
			data, rerr := os.ReadFile(src)
			if rerr != nil {
				return moved, rerr
			}
			if werr := os.WriteFile(dst, data, 0o644); werr != nil {
				return moved, werr
			}
			os.Remove(src)
		}
		moved = append(moved, f)
	}
	if len(moved) > 0 {
		os.Remove(from) // only succeeds if nothing else is left there
	}
	return moved, nil
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
