package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.json")
	t.Setenv("CLAUDEWITZ_CONFIG", p)

	c, err := Load()
	if err != nil || c != (Config{}) {
		t.Fatalf("missing file should load empty: %+v %v", c, err)
	}
	if err := (Config{Theme: "flexoki", Appearance: "light"}).Save(); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil || c.Theme != "flexoki" || c.Appearance != "light" {
		t.Fatalf("round trip: %+v %v", c, err)
	}
}

func TestDir(t *testing.T) {
	t.Setenv("CLAUDEWITZ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := Path(); got != filepath.Join("/xdg", "claudewitz", "config.json") {
		t.Errorf("with XDG_CONFIG_HOME: %s", got)
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := Path(); got != filepath.Join(home, ".config", "claudewitz", "config.json") {
		t.Errorf("default: %s", got)
	}
	if got := HistoryPath(); got != filepath.Join(home, ".config", "claudewitz", "history.jsonl") {
		t.Errorf("history: %s", got)
	}
}

func TestMigrate(t *testing.T) {
	old, dst := filepath.Join(t.TempDir(), "old"), filepath.Join(t.TempDir(), "new")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(old, "config.json"), []byte(`{"theme":"nord"}`), 0o644)
	os.WriteFile(filepath.Join(old, "history.jsonl"), []byte("{}\n"), 0o644)

	moved, err := migrate(old, dst)
	if err != nil || len(moved) != 2 {
		t.Fatalf("moved %v, err %v", moved, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "config.json")); string(data) != `{"theme":"nord"}` {
		t.Errorf("config not moved: %q", data)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("empty old folder left behind")
	}

	// Once the new place has files, nothing is touched.
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "config.json"), []byte(`{"theme":"mono"}`), 0o644)
	if moved, _ := migrate(old, dst); len(moved) != 0 {
		t.Errorf("migrated over an existing config: %v", moved)
	}
}
