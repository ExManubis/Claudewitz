package tui

import "math"

// The sidebar mascot: a bean in headphones, bopping to a beat. It's a small
// 3D scene of signed distance functions, ray-marched once per frame of the
// loop whenever the indicator changes size, then dithered into Braille dots
// at draw time so it follows the theme.

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
	matDark
	matEye
	matPink  // cheeks and ear cups
	matPhone // headband and boots
	matGold  // music notes
	matCount
)

func sdSphere(p, c vec3, r float64) float64 { return p.sub(c).length() - r }

func sdEllipsoid(p, c, r vec3) float64 {
	q := p.sub(c).div(r)
	return (q.length() - 1) * math.Min(r.x, math.Min(r.y, r.z))
}

// sdTorusZ is a ring lying in the xy plane.
func sdTorusZ(p, c vec3, big, small float64) float64 {
	q := p.sub(c)
	return math.Hypot(math.Hypot(q.x, q.y)-big, q.z) - small
}

// sdCylX is a capped cylinder along the x axis.
func sdCylX(p, c vec3, r, h float64) float64 {
	q := p.sub(c)
	dx, dy := math.Hypot(q.y, q.z)-r, math.Abs(q.x)-h
	return math.Min(math.Max(dx, dy), 0) + math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
}

func sdCapsule(p, a, b vec3, r float64) float64 {
	pa, ba := p.sub(a), b.sub(a)
	h := math.Max(0, math.Min(1, pa.dot(ba)/ba.dot(ba)))
	return pa.sub(ba.mul(h)).length() - r
}

// beanPose is everything that changes between frames.
type beanPose struct {
	yaw, pitch float64
	bop        float64 // head tilt, radians, pivoting at the feet
	eyeOpen    float64 // 1 open, ~0 closed, >1 wide
	happy      bool    // ^ ^ eyes
	notes      []float64
}

var beanPivot = vec3{0, 1.2, 0}

// sdf returns the distance to the bean and the material of the nearest part.
func (ps *beanPose) sdf(p vec3) (float64, int8) {
	d, m := math.Inf(1), matNone
	add := func(dd float64, mm int8) {
		if dd < d {
			d, m = dd, mm
		}
	}
	// Music notes drift up beside the head, outside the bop.
	for _, t := range ps.notes {
		c := vec3{1.15 + 0.15*math.Sin(t*6), 0.5 - t*1.6, 0.2}
		add(sdEllipsoid(p, c, vec3{0.09, 0.07, 0.06}), matGold)
		add(sdCapsule(p, c.add(vec3{0.07, 0, 0}), c.add(vec3{0.07, -0.25, 0}), 0.02), matGold)
	}
	q := p.sub(beanPivot).rotZ(ps.bop).add(beanPivot)

	add(sdEllipsoid(q, vec3{0, 0.12, 0}, vec3{0.88, 1, 0.78}), matBody)
	for _, s := range [2]float64{-1, 1} {
		add(sdEllipsoid(q, vec3{s * 0.38, 1.1, 0.18}, vec3{0.2, 0.14, 0.27}), matPhone) // boots
		add(sdSphere(q, vec3{s * 0.9, 0.35, 0}, 0.14), matBody)                         // arms
		add(sdCylX(q, vec3{s * 0.92, 0, 0}, 0.26, 0.1), matPink)                        // ear cups
		add(sdEllipsoid(q, vec3{s * 0.62, 0.25, 0.68}, vec3{0.15, 0.08, 0.1}), matPink) // cheeks
	}
	// Headband: the top half of a ring around the head.
	add(math.Max(sdTorusZ(q, vec3{0, -0.05, 0}, 1, 0.06), q.y), matPhone)

	// Face: big eyes (or happy arcs) and a small smile.
	const fz, ey = 0.74, -0.05
	for _, s := range [2]float64{-1, 1} {
		if ps.happy {
			for i := 0; i < 5; i++ {
				a := -1.1 + 2.2*float64(i)/4
				add(sdSphere(q, vec3{s*0.34 + math.Sin(a)*0.13, ey + 0.06 - math.Cos(a)*0.12, fz - 0.02}, 0.04), matEye)
			}
		} else {
			add(sdEllipsoid(q, vec3{s * 0.34, ey, fz - 0.06}, vec3{0.16, math.Max(0.025, 0.23*ps.eyeOpen), 0.1}), matEye)
		}
	}
	for i := 0; i < 5; i++ {
		a := -0.8 + 1.6*float64(i)/4
		add(sdSphere(q, vec3{math.Sin(a) * 0.14, ey + 0.34 - math.Cos(a)*0.06, fz - 0.01}, 0.04), matEye)
	}
	return d, m
}

// The scene's vertical extent, for framing.
const beanTop, beanBottom = -1.25, 1.32

// beanFrame is one rendered frame: per dot, how lit it is and what it is.
type beanFrame struct {
	inten []float32
	mat   []int8
}

// renderBean ray-marches the scene into a w×h dot grid.
func renderBean(w, h int, ps beanPose) beanFrame {
	f := beanFrame{inten: make([]float32, w*h), mat: make([]int8, w*h)}
	depth := make([]float64, w*h)
	normals := make([]vec3, w*h)
	view := (beanBottom - beanTop) * 1.06
	cy0 := (beanTop + beanBottom) / 2
	const cam = 7.0
	// The camera orbits the bean: rotate rays by the inverse pose.
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
			case matEye, matPink:
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

// beanLoop is the number of frames in one idle loop: two seconds, four bops.
const (
	beanLoop = 32
	beanFPS  = 16
)

// beanIdlePose is frame i of the bopping loop.
func beanIdlePose(i int) beanPose {
	ph := float64(i) / beanLoop * 2 * math.Pi
	beat := math.Sin(ph * 4)
	ps := beanPose{yaw: 0.6 * math.Sin(ph), pitch: -0.15, bop: 0.12 * beat, eyeOpen: 1, happy: beat > 0.6}
	if i%16 == 10 { // a blink each second, off the beat
		ps.eyeOpen = 0.12
	}
	for k := 0; k < 2; k++ {
		ps.notes = append(ps.notes, math.Mod(float64(i)/beanLoop*2+float64(k)*0.5, 1))
	}
	return ps
}

// beanWaitPose faces you, still and wide-eyed.
func beanWaitPose() beanPose {
	return beanPose{pitch: -0.15, eyeOpen: 1.25}
}

// bayer4 thresholds ordered dithering, so shading becomes dot density.
var bayer4 = [4][4]float32{{0, 8, 2, 10}, {12, 4, 14, 6}, {3, 11, 1, 9}, {15, 7, 13, 5}}

func dotOn(f *beanFrame, w, x, y int) bool {
	i := y*w + x
	return f.mat[i] != matNone && f.inten[i] > (bayer4[y%4][x%4]/16+1.0/32)*0.92
}
