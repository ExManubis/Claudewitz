package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps tests away from the real config and history files.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "claudewitz-test-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("CLAUDEWITZ_CONFIG", filepath.Join(dir, "config.json"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
