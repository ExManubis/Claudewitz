package tui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"strings"

	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// Images in the transcript (attached to your messages, or returned by a
// tool such as Read on a PNG) are drawn inline with half blocks: each cell
// shows two pixels, the top one as the "▀" foreground and the bottom one
// as the background. Since cells are about twice as tall as wide, the
// pixels come out square.

const (
	// pictureEdge is the long edge images are decoded down to; plenty for
	// the largest inline size, and cheap to resample at any width.
	pictureEdge = 320

	pictureCols     = 40 // closed: the most cells across and down
	pictureRows     = 10
	pictureOpenCols = 100 // opened with space or ctrl+o
	pictureOpenRows = 32
)

// picture is an image in the transcript, decoded the first time it's drawn.
type picture struct {
	name    string
	data    []byte
	decoded bool
	img     image.Image // scaled to pictureEdge; nil when it can't be decoded
	w, h    int         // original size
}

func newPicture(name string, data []byte) *picture {
	return &picture{name: name, data: data}
}

func (p *picture) decode() {
	if p.decoded {
		return
	}
	p.decoded = true
	defer func() { p.data = nil }() // the bytes aren't needed once decoded
	img, _, err := image.Decode(bytes.NewReader(p.data))
	if err != nil {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(p.data)); err == nil {
			p.w, p.h = cfg.Width, cfg.Height
		}
		return
	}
	b := img.Bounds()
	p.w, p.h = b.Dx(), b.Dy()
	p.img = shrink(img, pictureEdge)
}

// caption is the name and original size.
func (p *picture) caption() string {
	if p.w > 0 {
		return fmt.Sprintf("%s · %d×%d", p.name, p.w, p.h)
	}
	return p.name
}

// imagePictures collects the base64 image blocks among blocks.
func imagePictures(blocks []claude.ContentBlock) []*picture {
	var out []*picture
	for _, b := range blocks {
		if b.Type != "image" || b.Source == nil || b.Source.Type != "base64" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(b.Source.Data)
		if err != nil {
			continue
		}
		out = append(out, newPicture(fmt.Sprintf("image %d", len(out)+1), data))
	}
	return out
}

// toolResultPictures collects the images in a tool_result's content.
func toolResultPictures(raw json.RawMessage) []*picture {
	var blocks []claude.ContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return imagePictures(blocks)
}

// pictureSize fits an image of w×h pixels into at most cols×rows cells,
// keeping its shape. Each cell is one pixel wide and two tall.
func pictureSize(w, h, cols, rows int) (int, int) {
	if w <= 0 || h <= 0 || cols <= 0 || rows <= 0 {
		return 0, 0
	}
	c := min(cols, w)
	r := (c*h/w + 1) / 2
	if r > rows {
		r = rows
		c = r * 2 * w / h
	}
	return max(1, min(c, cols)), max(1, r)
}

// renderPicture draws p in at most cols×rows cells, or a one-line
// placeholder when it can't be decoded (WebP, say).
func renderPicture(p *picture, cols, rows int) string {
	p.decode()
	if p.img == nil {
		return lipgloss.NewStyle().Foreground(currentTheme().user).Render(icons.image+" ") + mutedStyle.Render(truncate(p.caption(), cols-2))
	}
	b := p.img.Bounds()
	c, r := pictureSize(b.Dx(), b.Dy(), cols, rows)
	px := resample(p.img, c, r*2)
	bg := backdrop()
	at := func(x, y int) string {
		cr, cg, cb, ca := px.At(x, y).RGBA()
		col := colorful.Color{R: float64(cr) / 0xffff, G: float64(cg) / 0xffff, B: float64(cb) / 0xffff}
		if ca < 0xffff { // premultiplied: add the backdrop under what's left
			k := 1 - float64(ca)/0xffff
			col = colorful.Color{R: col.R + bg.R*k, G: col.G + bg.G*k, B: col.B + bg.B*k}
		}
		return col.Clamped().Hex()
	}
	lines := make([]string, r)
	var sb strings.Builder
	for y := 0; y < r; y++ {
		sb.Reset()
		for x := 0; x < c; x++ {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(at(x, 2*y))).Background(lipgloss.Color(at(x, 2*y+1))).Render("▀"))
		}
		lines[y] = sb.String()
	}
	return strings.Join(lines, "\n")
}

// picturesView lays images out left to right, wrapping to new rows when
// they don't fit width, each with its caption underneath. open draws them
// larger.
func picturesView(pics []*picture, width int, open bool) string {
	cols, rows := pictureCols, pictureRows
	if open {
		cols, rows = pictureOpenCols, pictureOpenRows
	}
	cols = max(8, min(cols, width))
	const gap = 2
	var out, row []string
	used := 0
	for _, p := range pics {
		img := renderPicture(p, cols, rows)
		w := max(lipgloss.Width(img), min(cols, 16))
		tile := img + "\n" + mutedStyle.Render(truncate(p.caption(), w))
		w = lipgloss.Width(tile)
		if len(row) > 0 && used+gap+w > width {
			out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, row...))
			row, used = nil, 0
		}
		if len(row) > 0 {
			row = append(row, strings.Repeat(" ", gap))
			used += gap
		}
		row = append(row, tile)
		used += w
	}
	if len(row) > 0 {
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return strings.Join(out, "\n")
}
