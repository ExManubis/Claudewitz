package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPictureSize(t *testing.T) {
	for _, tc := range []struct{ w, h, cols, rows, wantC, wantR int }{
		{100, 50, 40, 10, 40, 10}, // wide: fills the width
		{50, 200, 40, 10, 5, 10},  // tall: limited by rows
		{8, 8, 40, 10, 8, 4},      // tiny: never scaled up
		{1000, 1, 40, 10, 40, 1},  // a sliver still gets a row
	} {
		c, r := pictureSize(tc.w, tc.h, tc.cols, tc.rows)
		if c != tc.wantC || r != tc.wantR {
			t.Errorf("pictureSize(%d×%d in %d×%d) = %d×%d, want %d×%d", tc.w, tc.h, tc.cols, tc.rows, c, r, tc.wantC, tc.wantR)
		}
	}
}

func TestRenderPicture(t *testing.T) {
	p := newPicture("shot.png", testPNG(t, 100, 50))
	out := renderPicture(p, 40, 10)
	if h := lipgloss.Height(out); h != 10 {
		t.Errorf("height = %d, want 10", h)
	}
	if w := lipgloss.Width(out); w != 40 {
		t.Errorf("width = %d, want 40", w)
	}
	if p.caption() != "shot.png · 100×50" {
		t.Errorf("caption = %q", p.caption())
	}
}

func TestUndecodablePictureIsAChip(t *testing.T) {
	p := newPicture("photo.webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "))
	if out := renderPicture(p, 40, 10); lipgloss.Height(out) != 1 || !strings.Contains(out, "photo.webp") {
		t.Errorf("got %q", out)
	}
}

func TestToolResultPictures(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(testPNG(t, 4, 4))
	raw := fmt.Sprintf(`[{"type":"text","text":"here"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]`, data)
	pics := toolResultPictures([]byte(raw))
	if len(pics) != 1 {
		t.Fatalf("got %d pictures, want 1", len(pics))
	}
	if toolResultPictures([]byte(`"plain text"`)) != nil {
		t.Error("a text result has no pictures")
	}
}

func TestToolEntryDrawsItsImage(t *testing.T) {
	c := newChat(newTracker())
	e := c.add(&entry{kind: kindTool, toolID: "t1", toolName: "Read", toolInput: []byte(`{"file_path":"/x/shot.png"}`), toolStatus: toolOK, toolOutput: "[image]"})
	e.pics = []*picture{newPicture("image 1", testPNG(t, 60, 30))}
	v := c.View(80)
	if !strings.Contains(v, "▀") || !strings.Contains(v, "image 1 · 60×30") {
		t.Errorf("view = %q", v)
	}
}
