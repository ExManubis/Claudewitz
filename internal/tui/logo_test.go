package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIcon(t *testing.T) {
	if svg := iconSVG(themes[0]); strings.Count(svg, "<circle") < 300 {
		t.Errorf("icon SVG has only %d dots", strings.Count(svg, "<circle"))
	}
	for _, size := range []int{256, 32} {
		img := iconPNG(themes[0], size)
		if img.Bounds().Dx() != size {
			t.Fatalf("icon is %dpx, want %d", img.Bounds().Dx(), size)
		}
		// Corners are transparent, the middle is painted.
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Errorf("%dpx icon corner isn't transparent", size)
		}
		if _, _, _, a := img.At(size/2, size/2).RGBA(); a == 0 {
			t.Errorf("%dpx icon centre is empty", size)
		}
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
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("logo-dark.svg", []byte(logoSVG(themes[0], true)))
	write("logo-light.svg", []byte(logoSVG(themes[0], false)))
	write("icon.svg", []byte(iconSVG(themes[0])))
	for _, size := range []int{1024, 512, 256, 128, 64, 32, 16} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, iconPNG(themes[0], size)); err != nil {
			t.Fatal(err)
		}
		write(fmt.Sprintf("icon-%d.png", size), buf.Bytes())
	}
	write("icon-c.svg", []byte(cIconSVG()))
	for _, size := range []int{1024, 512, 256, 128, 64, 32, 16} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, cIconPNG(size)); err != nil {
			t.Fatal(err)
		}
		write(fmt.Sprintf("icon-c-%d.png", size), buf.Bytes())
	}
}

func TestLetterIcon(t *testing.T) {
	if svg := cIconSVG(); strings.Count(svg, "<rect") != 3*len(cIconRects()) || !strings.Contains(svg, strings.ToLower(cIconBase)) {
		t.Errorf("letter icon SVG: %d rects, base colour present %v", strings.Count(svg, "<rect"), strings.Contains(svg, strings.ToLower(cIconBase)))
	}
	for _, size := range []int{16, 256} {
		img := cIconPNG(size).(*image.NRGBA)
		if img.Bounds().Dx() != size {
			t.Fatalf("icon is %dpx, want %d", img.Bounds().Dx(), size)
		}
		if img.NRGBAAt(0, 0).A != 0 || img.NRGBAAt(size-1, size-1).A != 0 {
			t.Errorf("%dpx letter icon corners aren't transparent", size)
		}
		// The stem, a little in from the left at mid-height, is solid.
		if c := img.NRGBAAt(size/4, size/2); c.A < 200 {
			t.Errorf("%dpx letter icon stem is empty: %+v", size, c)
		}
	}
}
