package tui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	colorful "github.com/lucasb-eyer/go-colorful"
)

// The claudewitz logo, for the README: the headphone bean next to the
// CLAUDEWITZ wordmark, drawn as a dot matrix. The app itself keeps the
// plain wordmark, since the bean already lives in the sidebar.

// logoPose is the bean as it appears in the logo: turned a little so it
// reads as 3D, eyes wide open, no notes.
func logoPose() beanPose {
	return beanPose{yaw: 0.38, pitch: -0.15, eyeOpen: 1.1}
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

// ── App icon ────────────────────────────────────────────────────────────

// iconPose faces a little more forward than the logo, so the face reads at
// small sizes.
func iconPose() beanPose {
	return beanPose{yaw: 0.3, pitch: -0.12, eyeOpen: 1.1}
}

// iconPalette resolves the dark variant of a theme's colours for the icon.
type iconPalette struct {
	bg, edge colorful.Color
	mat      map[int8]colorful.Color
}

func newIconPalette(th theme) iconPalette {
	hex := func(c ac) colorful.Color {
		col, err := colorful.Hex(c.Dark)
		if err != nil {
			return colorful.Color{R: 0.5, G: 0.5, B: 0.5}
		}
		return col
	}
	bg, _ := colorful.Hex("#1a1715")
	if th.bg.Dark != "" {
		bg = hex(th.bg)
	}
	return iconPalette{
		bg:   bg,
		edge: bg.BlendLab(hex(th.second), 0.35),
		mat: map[int8]colorful.Color{
			matBody:  hex(th.accent),
			matPhone: hex(th.second),
			matPink:  hex(th.err).BlendLab(hex(th.text), 0.4),
			matEye:   hex(th.text),
			matGold:  hex(th.warn),
		},
	}
}

// Icon geometry, as fractions of its size.
const (
	iconRadius = 0.225 // corner radius of the rounded square
	iconInset  = 0.1   // margin around the bean
	iconDots   = 44    // dot grid across the bean, for the dotted style
)

// iconSVG draws the icon: the dotted bean on a rounded dark square.
func iconSVG(th theme) string {
	pal := newIconPalette(th)
	const size = 512.0
	inner := size * (1 - 2*iconInset)
	pitch := inner / iconDots
	fr := renderBean(iconDots, iconDots, iconPose())
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %[1]g %[1]g" width="%[1]g" height="%[1]g">
<title>claudewitz</title>
<rect x="4" y="4" width="%[2]g" height="%[2]g" rx="%[3]g" fill="%[4]s" stroke="%[5]s" stroke-width="8"/>
`, size, size-8, size*iconRadius, pal.bg.Hex(), pal.edge.Hex())
	for y := 0; y < iconDots; y++ {
		for x := 0; x < iconDots; x++ {
			if !dotOn(&fr, iconDots, x, y) {
				continue
			}
			i := y*iconDots + x
			c := pal.bg.BlendLab(pal.mat[fr.mat[i]], 0.45+0.55*float64(fr.inten[i]))
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s"/>`+"\n",
				size*iconInset+(float64(x)+0.5)*pitch, size*iconInset+(float64(y)+0.5)*pitch, pitch*0.4, c.Clamped().Hex())
		}
	}
	b.WriteString("</svg>\n")
	return b.String()
}

// iconPNG rasterises the icon at a pixel size. From 256px up it's dotted
// like the SVG; below that dots would turn to speckle, so the bean is drawn
// as solid shaded pixels instead.
func iconPNG(th theme, size int) image.Image {
	pal := newIconPalette(th)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	rad := s * iconRadius
	edgeW := math.Max(1, s/64)
	// Rounded square, anti-aliased by distance to its edge.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			qx := math.Max(math.Abs(px-s/2)-(s/2-rad), 0)
			qy := math.Max(math.Abs(py-s/2)-(s/2-rad), 0)
			d := math.Hypot(qx, qy) - rad // < 0 inside
			cover := math.Max(0, math.Min(1, 0.5-d))
			if cover == 0 {
				continue
			}
			c := pal.bg
			if d > -edgeW {
				c = pal.edge
			}
			setBlend(img, x, y, c, cover)
		}
	}
	inset := iconInset
	if size <= 32 {
		inset = 0.05 // every pixel counts
	}
	inner := s * (1 - 2*inset)
	off := s * inset
	if size >= 256 {
		pitch := inner / iconDots
		fr := renderBean(iconDots, iconDots, iconPose())
		for y := 0; y < iconDots; y++ {
			for x := 0; x < iconDots; x++ {
				if !dotOn(&fr, iconDots, x, y) {
					continue
				}
				i := y*iconDots + x
				c := pal.bg.BlendLab(pal.mat[fr.mat[i]], 0.45+0.55*float64(fr.inten[i]))
				drawDot(img, off+(float64(x)+0.5)*pitch, off+(float64(y)+0.5)*pitch, pitch*0.4, c)
			}
		}
		return img
	}
	n := int(math.Round(inner))
	fr := renderBean(n, n, iconPose())
	o := int(math.Round(off))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			i := y*n + x
			m := fr.mat[i]
			if m < 0 || m == matDark {
				continue
			}
			c := pal.bg.BlendLab(pal.mat[m], 0.5+0.5*float64(fr.inten[i]))
			setBlend(img, o+x, o+y, c, 1)
		}
	}
	return img
}

// drawDot paints an anti-aliased filled circle.
func drawDot(img *image.NRGBA, cx, cy, r float64, c colorful.Color) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - r
			if cover := math.Max(0, math.Min(1, 0.5-d)); cover > 0 {
				setBlend(img, x, y, c, cover)
			}
		}
	}
}

// setBlend paints c over the pixel with the given coverage.
func setBlend(img *image.NRGBA, x, y int, c colorful.Color, cover float64) {
	if !(image.Point{x, y}.In(img.Rect)) {
		return
	}
	old := img.NRGBAAt(x, y)
	a := float64(old.A) / 255
	base := colorful.Color{R: float64(old.R) / 255, G: float64(old.G) / 255, B: float64(old.B) / 255}
	outA := cover + a*(1-cover)
	if outA == 0 {
		return
	}
	mix := func(n, o float64) float64 { return (n*cover + o*a*(1-cover)) / outA }
	cc := c.Clamped()
	img.SetNRGBA(x, y, color.NRGBA{
		R: uint8(math.Round(mix(cc.R, base.R) * 255)),
		G: uint8(math.Round(mix(cc.G, base.G) * 255)),
		B: uint8(math.Round(mix(cc.B, base.B) * 255)),
		A: uint8(math.Round(outA * 255)),
	})
}
