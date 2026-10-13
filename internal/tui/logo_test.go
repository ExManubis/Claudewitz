package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestLogoLockup(t *testing.T) {
	wide := strings.Split(logoLockup(120), "\n")
	if len(wide) != logoBeanRows {
		t.Fatalf("lockup has %d rows, want %d", len(wide), logoBeanRows)
	}
	if !strings.ContainsFunc(ansi.Strip(wide[0]+wide[1]), func(r rune) bool { return r > 0x2800 && r <= 0x28ff }) {
		t.Error("no bean in the lockup")
	}
	for _, l := range wide {
		if w := ansi.StringWidth(l); w > 120 {
			t.Errorf("lockup row is %d wide, over 120", w)
		}
	}
	if n := len(strings.Split(logoLockup(40), "\n")); n != 3 {
		t.Errorf("narrow lockup has %d rows, want the 3-row wordmark", n)
	}
}

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
