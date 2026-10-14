package tui

import (
	"fmt"
	"image"
	"math"
	"strings"

	colorful "github.com/lucasb-eyer/go-colorful"
)

// The letter icon: a blocky "C" in the style of btop's "B", built from
// stacked horizontal slabs that shade from a light tint of #DA785E at the
// top to a dark one at the bottom, over a dark offset shadow with a grey
// rim. The background is transparent.

const (
	cIconBase   = "#DA785E"
	cIconShadow = "#15110f"
	cIconRim    = "#5c524d"
	cBandH      = 1.4  // slab height, in column widths
	cShadowOff  = 0.35 // the shadow's offset down and right
	cRimW       = 0.08 // the rim's width
)

// cRect is one slab piece, in column units.
type cRect struct {
	x0, y0, x1, y1 float64
	band           int
}

// cIconRects lays out the C on a six-column grid in five slabs: full arms
// at the top and bottom with their left corners cut, a two-column stem,
// and short terminals at the right of the second and fourth slabs.
func cIconRects() []cRect {
	spans := [5][][2]float64{
		{{1, 6}},
		{{0, 2}, {4, 6}},
		{{0, 2}},
		{{0, 2}, {4, 6}},
		{{1, 6}},
	}
	var out []cRect
	for b, row := range spans {
		for _, s := range row {
			out = append(out, cRect{s[0], float64(b) * cBandH, s[1], float64(b+1) * cBandH, b})
		}
	}
	return out
}

// cBandColors shades the slabs from a light tint of the base colour, through
// the base itself in the middle, to a dark shade at the bottom.
func cBandColors() [5]colorful.Color {
	base, _ := colorful.Hex(cIconBase)
	top := base.BlendLab(colorful.Color{R: 1, G: 1, B: 1}, 0.3)
	bottom := base.BlendLab(colorful.Color{}, 0.5)
	var out [5]colorful.Color
	for b := range out {
		t := float64(b) / 4
		if t < 0.5 {
			out[b] = top.BlendLab(base, t*2).Clamped()
		} else {
			out[b] = base.BlendLab(bottom, (t-0.5)*2).Clamped()
		}
	}
	return out
}

// cIconFit is the scale (pixels per column) and offset that centre the
// letter and its shadow on a square of size pixels with a margin.
func cIconFit(size, margin float64) (k, ox, oy float64) {
	w := 6 + cShadowOff + cRimW
	h := 5*cBandH + cShadowOff + cRimW
	k = size * (1 - 2*margin) / math.Max(w, h)
	return k, (size - w*k) / 2, (size - h*k) / 2
}

// cIconSVG draws the letter icon as vector rectangles.
func cIconSVG() string {
	const size = 512
	k, ox, oy := cIconFit(size, 0.08)
	rects := cIconRects()
	cols := cBandColors()
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d">
<title>claudewitz</title>
<g transform="translate(%.2f %.2f) scale(%.4f)">
`, size, size, size, size, ox, oy, k)
	// The shadow: stroked for its rim, then filled again without the stroke
	// so the rim only shows around the outside of the joined shape.
	for _, r := range rects {
		fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s" stroke="%s" stroke-width="%.2f"/>
`, r.x0+cShadowOff, r.y0+cShadowOff, r.x1-r.x0, r.y1-r.y0, cIconShadow, cIconRim, cRimW*2)
	}
	for _, r := range rects {
		fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s"/>
`, r.x0+cShadowOff, r.y0+cShadowOff, r.x1-r.x0, r.y1-r.y0, cIconShadow)
	}
	for _, r := range rects {
		fmt.Fprintf(&b, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s"/>
`, r.x0, r.y0, r.x1-r.x0, r.y1-r.y0, cols[r.band].Hex())
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

// cIconPNG rasterises the letter icon at a pixel size, supersampled so the
// edges are smooth at every size.
func cIconPNG(size int) image.Image {
	margin := 0.08
	if size <= 32 {
		margin = 0.03 // every pixel counts
	}
	k, ox, oy := cIconFit(float64(size), margin)
	rects := cIconRects()
	cols := cBandColors()
	shadow, _ := colorful.Hex(cIconShadow)
	rim, _ := colorful.Hex(cIconRim)

	// colorAt is what covers a point in column units, if anything.
	colorAt := func(u, v float64) (colorful.Color, bool) {
		for _, r := range rects {
			if u >= r.x0 && u < r.x1 && v >= r.y0 && v < r.y1 {
				return cols[r.band], true
			}
		}
		near := math.Inf(1)
		for _, r := range rects {
			dx := math.Max(math.Max(r.x0+cShadowOff-u, u-(r.x1+cShadowOff)), 0)
			dy := math.Max(math.Max(r.y0+cShadowOff-v, v-(r.y1+cShadowOff)), 0)
			near = math.Min(near, math.Hypot(dx, dy))
		}
		switch {
		case near == 0:
			return shadow, true
		case near <= cRimW:
			return rim, true
		}
		return colorful.Color{}, false
	}

	n := 4 // samples per pixel side
	if size <= 64 {
		n = 8
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, bl, hits float64
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/float64(n) - ox) / k
					v := (float64(y) + (float64(sy)+0.5)/float64(n) - oy) / k
					if c, ok := colorAt(u, v); ok {
						r, g, bl, hits = r+c.R, g+c.G, bl+c.B, hits+1
					}
				}
			}
			if hits == 0 {
				continue
			}
			setBlend(img, x, y, colorful.Color{R: r / hits, G: g / hits, B: bl / hits}, hits/float64(n*n))
		}
	}
	return img
}
