package tui

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // register decoders for DecodeConfig
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/claude"
)

const (
	// The API accepts images up to 5 MB once base64-encoded and works best
	// with the long edge at most this many pixels; larger images are scaled
	// down before sending.
	maxImageBytes = 3_750_000
	maxImageEdge  = 1568
	maxAttached   = 10
)

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

func isImagePath(p string) bool { return imageExts[strings.ToLower(filepath.Ext(p))] }

type attachment struct {
	name      string
	mediaType string
	data      []byte
	w, h      int
	resized   bool
}

func (a attachment) label() string {
	var parts []string
	if a.w > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", a.w, a.h))
	}
	parts = append(parts, humanBytes(len(a.data)))
	if a.resized {
		parts = append(parts, "resized")
	}
	return a.name + " · " + strings.Join(parts, " · ")
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// newAttachment validates image bytes and scales them down when needed.
func newAttachment(name string, data []byte) (attachment, error) {
	mt := http.DetectContentType(data)
	switch mt {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return attachment{}, fmt.Errorf("%s isn't a PNG, JPEG, GIF or WebP image", name)
	}
	a := attachment{name: name, mediaType: mt, data: data}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		a.w, a.h = cfg.Width, cfg.Height
	}
	if len(data) <= maxImageBytes && max(a.w, a.h) <= maxImageEdge {
		return a, nil
	}
	// Too big: decode, shrink and re-encode as JPEG. WebP can't be decoded
	// with the standard library, so it can only pass if it's small enough.
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		if len(data) <= maxImageBytes {
			return a, nil
		}
		return attachment{}, fmt.Errorf("%s is %s; images must be under %s", name, humanBytes(len(data)), humanBytes(maxImageBytes))
	}
	small := shrink(img, maxImageEdge)
	for q := 85; q >= 40; q -= 15 {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: q}); err != nil {
			return attachment{}, err
		}
		if buf.Len() <= maxImageBytes {
			b := small.Bounds()
			return attachment{name: name, mediaType: "image/jpeg", data: buf.Bytes(), w: b.Dx(), h: b.Dy(), resized: true}, nil
		}
	}
	return attachment{}, fmt.Errorf("%s is too large to send even after resizing", name)
}

// shrink scales img so its long edge is at most edge pixels.
func shrink(img image.Image, edge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) <= edge {
		return img
	}
	nw, nh := edge, h*edge/w
	if h > w {
		nw, nh = w*edge/h, edge
	}
	return resample(img, nw, nh)
}

// resample scales img to nw×nh, averaging the source pixels under each
// destination pixel (a box filter).
func resample(img image.Image, nw, nh int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh = max(1, nw), max(1, nh)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+max((y+1)*h/nh, y*h/nh+1)
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+max((x+1)*w/nw, x*w/nw+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					r, g, bl, a, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), n+1
				}
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return dst
}

// expandPath resolves ~ and relative paths against cwd.
func expandPath(p, cwd string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return p
}

func loadAttachment(path, cwd string) (attachment, error) {
	full := expandPath(path, cwd)
	st, err := os.Stat(full)
	if err != nil {
		return attachment{}, fmt.Errorf("can't read %s", path)
	}
	if st.Size() > 50<<20 {
		return attachment{}, fmt.Errorf("%s is %s, too large to attach", path, humanBytes(int(st.Size())))
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return attachment{}, err
	}
	return newAttachment(filepath.Base(full), data)
}

// droppedPaths recognises text pasted by dragging files into the terminal:
// one or more image paths, possibly quoted or with backslash-escaped
// spaces. It returns nil unless every piece is an existing image file.
func droppedPaths(text, cwd string) []string {
	text = strings.TrimSpace(text)
	if text == "" || strings.Count(text, "\n") > maxAttached {
		return nil
	}
	var parts []string
	for _, m := range dropRe.FindAllString(text, -1) {
		m = strings.Trim(m, `"'`)
		m = strings.ReplaceAll(m, `\ `, " ")
		m = strings.TrimPrefix(m, "file://")
		parts = append(parts, m)
	}
	if len(parts) == 0 || len(parts) > maxAttached {
		return nil
	}
	for _, p := range parts {
		if !isImagePath(p) {
			return nil
		}
		if st, err := os.Stat(expandPath(p, cwd)); err != nil || st.IsDir() {
			return nil
		}
	}
	return parts
}

// dropRe splits pasted paths: quoted strings, or runs of non-space
// characters where "\ " counts as part of the path.
var dropRe = regexp.MustCompile(`"[^"]+"|'[^']+'|(?:\\ |[^\s])+`)

var errNoClipboardImage = errors.New("no image on the clipboard")

// clipboardImage reads an image from the system clipboard.
func clipboardImage() ([]byte, error) {
	try := func(name string, args ...string) ([]byte, bool) {
		if _, err := exec.LookPath(name); err != nil {
			return nil, false
		}
		out, err := exec.Command(name, args...).Output()
		if err != nil || len(out) == 0 {
			return nil, true
		}
		return out, true
	}
	switch runtime.GOOS {
	case "darwin":
		if out, ok := try("pngpaste", "-"); ok && out != nil {
			return out, nil
		}
		// osascript prints «data PNGf89504E47…»; decode the hex.
		if out, ok := try("osascript", "-e", "the clipboard as «class PNGf»"); ok && out != nil {
			s := strings.TrimSpace(string(out))
			if i := strings.Index(s, "PNGf"); i >= 0 {
				if b, err := hex.DecodeString(strings.TrimSuffix(s[i+4:], "»")); err == nil {
					return b, nil
				}
			}
		}
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if out, ok := try("wl-paste", "--no-newline", "--type", "image/png"); ok && out != nil {
				return out, nil
			}
		}
		if out, ok := try("xclip", "-selection", "clipboard", "-t", "image/png", "-o"); ok && out != nil {
			return out, nil
		}
	}
	return nil, errNoClipboardImage
}

type clipboardImageMsg struct {
	att attachment
	err error
}

func readClipboardImage() tea.Cmd {
	return func() tea.Msg {
		data, err := clipboardImage()
		if err != nil {
			return clipboardImageMsg{err: err}
		}
		a, err := newAttachment("pasted image", data)
		return clipboardImageMsg{att: a, err: err}
	}
}

// attach adds an image to the next message.
func (m *Model) attach(a attachment) tea.Cmd {
	if len(m.attachments) >= maxAttached {
		return m.notify(toastWarn, "Too many images", fmt.Sprintf("up to %d per message", maxAttached))
	}
	m.attachments = append(m.attachments, a)
	m.layout()
	if a.resized {
		return m.notify(toastInfo, "Attached "+a.name, fmt.Sprintf("scaled down to %d×%d", a.w, a.h))
	}
	return m.notify(toastSuccess, "Attached "+a.name, "⌫ on an empty prompt removes it")
}

func (m *Model) attachPaths(paths []string) tea.Cmd {
	var cmds []tea.Cmd
	for _, p := range paths {
		a, err := loadAttachment(p, m.cwd())
		if err != nil {
			cmds = append(cmds, m.notify(toastError, "Couldn't attach image", err.Error()))
			continue
		}
		cmds = append(cmds, m.attach(a))
	}
	return tea.Batch(cmds...)
}

func (m *Model) images() []claude.Image {
	out := make([]claude.Image, len(m.attachments))
	for i, a := range m.attachments {
		out[i] = claude.Image{MediaType: a.mediaType, Data: a.data}
	}
	return out
}

// attachmentsView is the row of image chips above the input.
func (m Model) attachmentsView(width int) string {
	if len(m.attachments) == 0 {
		return ""
	}
	th := currentTheme()
	var chips []string
	for _, a := range m.attachments {
		chips = append(chips, pill(icons.image, th.user, truncate(a.label(), 40)))
	}
	row := " " + strings.Join(chips, " ")
	if lipgloss.Width(row) < width-12 {
		row += mutedStyle.Render("  ⌫ remove")
	}
	return ansi.Truncate(row, width, "…")
}
