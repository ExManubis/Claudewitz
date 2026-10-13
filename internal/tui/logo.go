package tui

import (
	"fmt"
	"math"
	"strings"
	"sync"

	colorful "github.com/lucasb-eyer/go-colorful"
)

// The claudewitz logo: the headphone bean next to the CLAUDEWITZ wordmark.
// In the terminal it's Braille and block letters; logoSVG draws the same
// lockup as dots for the README.

// logoPose is the bean as it appears in the logo: turned a little so it
// reads as 3D, eyes wide open, no notes.
func logoPose() beanPose {
	return beanPose{yaw: 0.38, pitch: -0.15, eyeOpen: 1.1}
}

var logoCache struct {
	sync.Mutex
	cols, rows int
	frame      beanFrame
}

// logoFrame renders (once per size) the logo bean.
func logoFrame(cols, rows int) *beanFrame {
	logoCache.Lock()
	defer logoCache.Unlock()
	if logoCache.cols != cols || logoCache.rows != rows {
		logoCache.cols, logoCache.rows = cols, rows
		logoCache.frame = renderBean(cols*2, rows*4, logoPose())
	}
	return &logoCache.frame
}

const logoBeanCols, logoBeanRows = 18, 8

// logoLockup is the bean beside the wordmark, for the welcome screen. It
// falls back to the wordmark alone when there's no room for both.
func logoLockup(width int) string {
	gap := 2
	if width < logoBeanCols+gap+42 {
		return bigLogo("CLAUDEWITZ", width)
	}
	word := strings.Split(bigLogo("CLAUDEWITZ", width-logoBeanCols-gap), "\n")
	bean := strings.Split(drawBean(logoFrame(logoBeanCols, logoBeanRows), logoBeanCols*2, logoBeanCols, logoBeanRows, beanStyles(currentTheme(), nil)), "\n")
	top := (logoBeanRows - len(word)) / 2
	out := make([]string, logoBeanRows)
	for i := range out {
		out[i] = bean[i]
		if j := i - top; j >= 0 && j < len(word) {
			out[i] += strings.Repeat(" ", gap) + word[j]
		}
	}
	return strings.Join(out, "\n")
}

// logoSVG draws the lockup as a dot matrix: the bean dithered from a larger
// render, the wordmark's block letters as solid dots in the accent → second
// gradient, and a tagline. dark picks the theme variant; the background is
// left transparent.
func logoSVG(th theme, dark bool) string {
	pick := func(c ac) colorful.Color {
		hex := c.Light
		if dark {
			hex = c.Dark
		}
		col, err := colorful.Hex(hex)
		if err != nil {
			return colorful.Color{R: 0.5, G: 0.5, B: 0.5}
		}
		return col
	}
	bg := pick(ac{Light: "#ffffff", Dark: "#141210"})
	if th.bg.Dark != "" {
		bg = pick(th.bg)
	}
	matCol := map[int8]colorful.Color{
		matBody:  pick(th.accent),
		matPhone: pick(th.second),
		matPink:  pick(th.err).BlendLab(pick(th.text), 0.4),
		matEye:   pick(th.text),
		matGold:  pick(th.warn),
	}
	const pitch, r = 6.0, 2.3
	var dots strings.Builder
	dot := func(x, y float64, c colorful.Color) {
		fmt.Fprintf(&dots, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s"/>`+"\n", x, y, r, c.Clamped().Hex())
	}

	// The bean: 64 dots tall.
	const bw, bh = 72, 64
	fr := renderBean(bw, bh, logoPose())
	for y := 0; y < bh; y++ {
		for x := 0; x < bw; x++ {
			if !dotOn(&fr, bw, x, y) {
				continue
			}
			m := fr.mat[y*bw+x]
			k := 0.45 + 0.55*float64(fr.inten[y*bw+x])
			if !dark {
				k = 0.7 + 0.3*k // pale tints vanish on white
			}
			dot(float64(x)*pitch+pitch/2, float64(y)*pitch+pitch/2, bg.BlendLab(matCol[m], k))
		}
	}

	// The wordmark: each half-block pixel of the glyphs becomes 3×3 dots.
	const scale = 3
	var px [][2]int // pixel coordinates in the glyph grid
	gx := 0
	for i, ch := range "CLAUDEWITZ" {
		g := glyphs[ch]
		if i > 0 {
			gx++ // letter spacing
		}
		width := 0
		for row, line := range g {
			for col, cell := range []rune(line) {
				width = max(width, col+1)
				top, bottom := cell == '█' || cell == '▀', cell == '█' || cell == '▄'
				if top {
					px = append(px, [2]int{gx + col, row * 2})
				}
				if bottom {
					px = append(px, [2]int{gx + col, row*2 + 1})
				}
			}
		}
		gx += width
	}
	wordW := float64(gx*scale) * pitch
	ox := float64(bw)*pitch + 4*pitch
	oy := float64(bh)*pitch/2 - float64(6*scale)*pitch*0.8
	a, b := pick(th.accent), pick(th.second)
	for _, p := range px {
		for dy := 0; dy < scale; dy++ {
			for dx := 0; dx < scale; dx++ {
				x := ox + float64(p[0]*scale+dx)*pitch + pitch/2
				y := oy + float64(p[1]*scale+dy)*pitch + pitch/2
				dot(x, y, a.BlendLab(b, math.Min(1, (x-ox)/wordW)))
			}
		}
	}
	tagY := oy + float64(6*scale)*pitch + 7*pitch
	w := ox + wordW + 2*pitch
	h := float64(bh) * pitch
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f">
<title>claudewitz</title>
%s<text x="%.1f" y="%.1f" font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, monospace" font-size="%.0f" fill="%s">Claude Code, in your terminal</text>
</svg>
`, w, h, w, h, dots.String(), ox+pitch/2, tagY, pitch*3.6, pick(th.muted).Hex())
}
