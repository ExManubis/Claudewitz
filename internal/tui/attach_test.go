package tui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Noise-ish pattern so large images don't compress to nothing.
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 13), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNewAttachment(t *testing.T) {
	a, err := newAttachment("small.png", pngBytes(t, 96, 64))
	if err != nil || a.mediaType != "image/png" || a.w != 96 || a.h != 64 || a.resized {
		t.Fatalf("small png: %+v %v", a, err)
	}
	big, err := newAttachment("big.png", pngBytes(t, 3000, 2000))
	if err != nil || !big.resized || big.mediaType != "image/jpeg" || big.w != maxImageEdge || big.h != 1045 {
		t.Fatalf("big png should be scaled to %dpx: %+v %v", maxImageEdge, big, err)
	}
	if len(big.data) > maxImageBytes {
		t.Fatalf("resized image still too big: %d", len(big.data))
	}
	if _, err := newAttachment("notes.txt", []byte("hello")); err == nil {
		t.Fatal("text should be rejected")
	}
}

func TestDroppedPaths(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "my shot.png"), pngBytes(t, 4, 4), 0o644)
	os.WriteFile(filepath.Join(dir, "b.jpg"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	cases := map[string]int{
		filepath.Join(dir, `my\ shot.png`):                              1,
		`"` + filepath.Join(dir, "my shot.png") + `"`:                   1,
		"file://" + filepath.Join(dir, "b.jpg"):                         1,
		filepath.Join(dir, "b.jpg") + " " + filepath.Join(dir, "b.jpg"): 2,
		filepath.Join(dir, "notes.txt"):                                 0,
		filepath.Join(dir, "b.jpg") + " and some words":                 0,
		"just some pasted text":                                         0,
		filepath.Join(dir, "missing.png"):                               0,
	}
	for in, want := range cases {
		if got := droppedPaths(in, dir); len(got) != want {
			t.Errorf("droppedPaths(%q) = %q, want %d paths", in, got, want)
		}
	}
}

func TestReplayedImageOnlyMessage(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	m.applyBlocks("user", []claude.ContentBlock{{Type: "image"}, {Type: "image"}}, false)
	m.applyBlocks("user", []claude.ContentBlock{{Type: "image"}, {Type: "text", Text: "what's this?"}}, false)
	if len(m.chat.entries) != 2 {
		t.Fatalf("entries: %d", len(m.chat.entries))
	}
	if e := m.chat.entries[0]; len(e.images) != 2 || e.text != "" {
		t.Fatalf("image-only message: %+v", e)
	}
	if e := m.chat.entries[1]; len(e.images) != 1 || e.text != "what's this?" {
		t.Fatalf("image with text: %+v", e)
	}
}
