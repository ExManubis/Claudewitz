package config

import (
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
