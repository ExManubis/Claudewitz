package tui

import "math"

// The sidebar mascot: tty, a robot with a monitor for a head, bopping to a
// beat. It's a small 3D scene of signed distance functions, ray-marched
// once per frame of the loop whenever the indicator changes size, then
// dithered into Braille dots at draw time so it follows the theme.

type vec3 struct{ x, y, z float64 }

func (a vec3) add(b vec3) vec3     { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec3) sub(b vec3) vec3     { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec3) mul(k float64) vec3  { return vec3{a.x * k, a.y * k, a.z * k} }
func (a vec3) dot(b vec3) float64  { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec3) length() float64     { return math.Sqrt(a.dot(a)) }
func (a vec3) div(b vec3) vec3     { return vec3{a.x / b.x, a.y / b.y, a.z / b.z} }
func (a vec3) norm() vec3          { return a.mul(1 / (a.length() + 1e-12)) }
func (a vec3) abs() vec3           { return vec3{math.Abs(a.x), math.Abs(a.y), math.Abs(a.z)} }
func (a vec3) maxc(k float64) vec3 { return vec3{math.Max(a.x, k), math.Max(a.y, k), math.Max(a.z, k)} }
func (a vec3) rotZ(r float64) vec3 {
	s, c := math.Sincos(r)
	return vec3{c*a.x - s*a.y, s*a.x + c*a.y, a.z}
}
func (a vec3) rotY(r float64) vec3 {
	s, c := math.Sincos(r)
	return vec3{c*a.x - s*a.z, a.y, s*a.x + c*a.z}
}
func (a vec3) rotX(r float64) vec3 {
	s, c := math.Sincos(r)
	return vec3{a.x, c*a.y + s*a.z, -s*a.y + c*a.z}
}

// Materials. Dark parts are cut-outs: they stop rays but draw no dots.
const (
	matNone int8 = iota - 1
	matBody
	matDark   // the screen: drawn as no dots, a black CRT
	matEye    // unused by tty, kept so the material set stays stable
	matPink   // unused by tty, kept so the material set stays stable
	matPhone  // antenna stalk and feet
	matGold   // antenna light
	matPrompt // what's on the screen
	matCount
)

func sdSphere(p, c vec3, r float64) float64 { return p.sub(c).length() - r }

func sdEllipsoid(p, c, r vec3) float64 {
	q := p.sub(c).div(r)
	return (q.length() - 1) * math.Min(r.x, math.Min(r.y, r.z))
}

// sdRoundBox is a box with half-extents b and corners rounded by r.
func sdRoundBox(p, c, b vec3, r float64) float64 {
	q := p.sub(c).abs().sub(b).add(vec3{r, r, r})
	return q.maxc(0).length() + math.Min(math.Max(q.x, math.Max(q.y, q.z)), 0) - r
}

func sdCapsule(p, a, b vec3, r float64) float64 {
	pa, ba := p.sub(a), b.sub(a)
	h := math.Max(0, math.Min(1, pa.dot(ba)/ba.dot(ba)))
	return pa.sub(ba.mul(h)).length() - r
}

// mascotFace is what tty's screen shows.
type mascotFace int

const (
	facePrompt mascotFace = iota // > and the cursor
	faceAsk                      // a steady ?, waiting for you
)

// mascotPose is everything that changes between frames.
type mascotPose struct {
	yaw, pitch float64
	bop        float64 // head tilt, radians, pivoting at the feet
	face       mascotFace
	cursor     bool // the prompt's cursor is lit
	led        bool // the antenna light is on
}

var mascotPivot = vec3{0, 1.15, 0}

// screenZ is the screen's front, where the glyphs sit.
const screenZ = 0.6

// sdf returns the distance to tty and the material of the nearest part: a
// monitor for a head, its screen a black CRT with a green prompt, on a small
// boxy body. y points down.
func (ps *mascotPose) sdf(p vec3) (float64, int8) {
	d, m := math.Inf(1), matNone
	add := func(dd float64, mm int8) {
		if dd < d {
			d, m = dd, mm
		}
	}
	q := p.sub(mascotPivot).rotZ(ps.bop).add(mascotPivot)

	// Monitor head, the screen set into its front.
	add(sdRoundBox(q, vec3{0, -0.2, 0}, vec3{0.88, 0.58, 0.55}, 0.14), matBody)
	add(sdRoundBox(q, vec3{0, -0.2, 0.52}, vec3{0.7, 0.42, 0.07}, 0.07), matDark)
	// Antenna and its light.
	add(sdCapsule(q, vec3{0, -0.78, 0}, vec3{0, -1.02, 0}, 0.035), matPhone)
	if ps.led {
		add(sdSphere(q, vec3{0, -1.1, 0}, 0.1), matGold)
	} else {
		add(sdSphere(q, vec3{0, -1.1, 0}, 0.08), matPhone)
	}
	// Neck, body, arms and feet.
	add(sdCapsule(q, vec3{0, 0.38, 0}, vec3{0, 0.48, 0}, 0.13), matBody)
	add(sdRoundBox(q, vec3{0, 0.7, 0}, vec3{0.55, 0.24, 0.4}, 0.12), matBody)
	for _, s := range [2]float64{-1, 1} {
		add(sdSphere(q, vec3{s * 0.7, 0.64, 0}, 0.14), matBody)
		add(sdEllipsoid(q, vec3{s * 0.3, 1.05, 0.1}, vec3{0.2, 0.11, 0.26}), matPhone)
	}

	// The screen.
	stroke := func(pts ...[2]float64) {
		for i := 0; i+1 < len(pts); i++ {
			a, b := pts[i], pts[i+1]
			add(sdCapsule(q, vec3{a[0], a[1], screenZ}, vec3{b[0], b[1], screenZ}, 0.045), matPrompt)
		}
	}
	switch ps.face {
	case faceAsk:
		stroke([2]float64{-0.13, -0.38}, [2]float64{-0.05, -0.46}, [2]float64{0.08, -0.45},
			[2]float64{0.14, -0.36}, [2]float64{0.1, -0.27}, [2]float64{0, -0.2}, [2]float64{0, -0.1})
		add(sdSphere(q, vec3{0, 0.02, screenZ}, 0.05), matPrompt)
	default:
		// Big and bold, so the prompt keeps its shape at sidebar size.
		const r = 0.07
		add(sdCapsule(q, vec3{-0.5, -0.44, screenZ}, vec3{-0.2, -0.2, screenZ}, r), matPrompt)
		add(sdCapsule(q, vec3{-0.2, -0.2, screenZ}, vec3{-0.5, 0.04, screenZ}, r), matPrompt)
		if ps.cursor {
			add(sdRoundBox(q, vec3{0.08, -0.2, screenZ}, vec3{0.11, 0.2, 0.03}, 0.02), matPrompt)
		}
	}
	return d, m
}

// The scene's vertical extent, for framing.
const mascotTop, mascotBottom = -1.25, 1.2

// mascotFrame is one rendered frame: per dot, how lit it is and what it is.
type mascotFrame struct {
	inten []float32
	mat   []int8
}

// renderMascot ray-marches the scene into a w×h dot grid.
func renderMascot(w, h int, ps mascotPose) mascotFrame {
	f := mascotFrame{inten: make([]float32, w*h), mat: make([]int8, w*h)}
	depth := make([]float64, w*h)
	normals := make([]vec3, w*h)
	view := (mascotBottom - mascotTop) * 1.06
	cy0 := (mascotTop + mascotBottom) / 2
	const cam = 7.0
	// The camera orbits tty: rotate rays by the inverse pose.
	rot := func(v vec3) vec3 { return v.rotX(ps.pitch).rotY(ps.yaw) }
	ro := rot(vec3{0, 0, cam})
	light := rot(vec3{-0.45, -0.55, 0.75}.norm())
	const bound = 2.0 // bounding sphere radius around the origin

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			f.mat[i] = matNone
			depth[i] = math.Inf(1)
			u := (float64(x) + 0.5 - float64(w)/2) / float64(h) * view
			v := (float64(y)+0.5-float64(h)/2)/float64(h)*view + cy0
			rd := rot(vec3{u, v, -cam}.norm())
			// Skip rays that miss the bounding sphere.
			b := ro.dot(rd)
			disc := b*b - ro.dot(ro) + bound*bound
			if disc <= 0 {
				continue
			}
			t, far := -b-math.Sqrt(disc), -b+math.Sqrt(disc)
			for step := 0; step < 48 && t < far; step++ {
				d, m := ps.sdf(ro.add(rd.mul(t)))
				if d < 0.004 {
					f.mat[i], depth[i] = m, t
					break
				}
				t += d * 0.9
			}
			if f.mat[i] == matNone {
				continue
			}
			p := ro.add(rd.mul(t))
			const e = 0.004
			dx1, _ := ps.sdf(p.add(vec3{e, 0, 0}))
			dx2, _ := ps.sdf(p.sub(vec3{e, 0, 0}))
			dy1, _ := ps.sdf(p.add(vec3{0, e, 0}))
			dy2, _ := ps.sdf(p.sub(vec3{0, e, 0}))
			dz1, _ := ps.sdf(p.add(vec3{0, 0, e}))
			dz2, _ := ps.sdf(p.sub(vec3{0, 0, e}))
			n := vec3{dx1 - dx2, dy1 - dy2, dz1 - dz2}.norm()
			normals[i] = n
			lam := math.Max(0, n.dot(light))
			rim := math.Pow(math.Max(0, 1-math.Abs(n.dot(rd))), 3)
			var in float64
			switch f.mat[i] {
			case matDark:
				in = 0
			case matEye, matPink, matPrompt:
				in = 1
			case matGold, matPhone:
				in = 0.45 + 0.55*lam
			default:
				in = 0.12 + 0.7*math.Pow(lam, 1.3) + 0.2*rim
			}
			f.inten[i] = float32(math.Min(1, in))
		}
	}
	// Outlines: the silhouette and creases of the body light up fully.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if f.mat[i] != matBody {
				continue
			}
			for _, o := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+o[0], y+o[1]
				if nx < 0 || ny < 0 || nx >= w || ny >= h {
					continue
				}
				j := ny*w + nx
				if depth[j]-depth[i] > 0.12 || (f.mat[j] != matNone && normals[i].dot(normals[j]) < 0.6 && depth[j] >= depth[i]-0.02) {
					f.inten[i] = 1
					break
				}
			}
		}
	}
	return f
}

// mascotLoop is the number of frames in one idle loop: two seconds, four bops.
const (
	mascotLoop = 32
	mascotFPS  = 16
)

// mascotIdlePose is frame i of the idle loop: bopping and turning, the cursor
// and the antenna light blinking. The prompt stays put; a ^ ^ flash on the
// beat read as the prompt glitching at this size.
func mascotIdlePose(i int) mascotPose {
	ph := float64(i) / mascotLoop * 2 * math.Pi
	return mascotPose{yaw: 0.6 * math.Sin(ph), pitch: -0.15, bop: 0.1 * math.Sin(ph*4),
		cursor: (i/4)%2 == 0, led: (i/8)%2 == 0}
}

// mascotWaitPose faces you, still, asking.
func mascotWaitPose() mascotPose {
	return mascotPose{pitch: -0.15, face: faceAsk, led: true}
}

// bayer4 thresholds ordered dithering, so shading becomes dot density.
var bayer4 = [4][4]float32{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

func dotOn(f *mascotFrame, w, x, y int) bool {
	i := y*w + x
	return f.mat[i] != matNone && f.inten[i] > (bayer4[y%4][x%4]/16+1.0/32)*0.92
}
