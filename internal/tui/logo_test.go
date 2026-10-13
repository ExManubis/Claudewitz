package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogoSVG(t *testing.T) {
	svg := logoSVG(themes[0], true)
	if !strings.HasPrefix(svg, "<svg") || strings.Count(svg, "<circle") < 500 {
		t.Fatalf("logo SVG looks wrong (%d dots)", strings.Count(svg, "<circle"))
	}
}

// Regenerate the README logos with:
//
//	CLAUDEWITZ_WRITE_LOGO=1 go test ./internal/tui -run TestWriteLogo
func TestWriteLogo(t *testing.T) {
	if os.Getenv("CLAUDEWITZ_WRITE_LOGO") == "" {
		t.Skip("set CLAUDEWITZ_WRITE_LOGO=1 to regenerate assets/")
	}
	dir := filepath.Join("..", "..", "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, dark := range map[string]bool{"logo-dark.svg": true, "logo-light.svg": false} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(logoSVG(themes[0], dark)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
