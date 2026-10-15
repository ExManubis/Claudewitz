package tui

import (
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The sidebar's working indicator: tty bops to a beat while
// Claude is idle, dissolves into a swirl of sparks while it works, and the
// sparks gather back into tty when the turn ends.
//
// Positions are in Braille dot space: each character cell holds 2×4 dots,
// and since cells are about twice as tall as wide, dots come out square.

type sparkMode int

const (
	sparkIdle      sparkMode = iota // tty, bopping
	sparkWorking                    // swirling
	sparkGathering                  // springing back into tty
	sparkWaiting                    // tty, still and asking: Claude needs you
)

const (
	sparkFPS      = 24
	sparkTrail    = 0.62 // how much of a dot's glow survives each frame
	sparkMinRows  = 5
	sparkMinTotal = 20 // sidebar rows needed before the indicator shows
)

type spark struct {
	x, y, vx, vy float64 // position and velocity
	hx, hy       float64 // home, in tty
	// swirl orbit, so each spark has its own lane and pace
	angle, radius, speed, wobble, phase float64
	delay                               float64 // seconds before it lifts off when dissolving
	mat                                 int8
}

type sparkField struct {
	cols, rows int
	w, h       int           // dot dimensions
	frames     []mascotFrame // the idle loop, rendered a few at a time
	wait       mascotFrame   // the waiting pose, rendered on first use
	frame      int           // idle frame on screen
	clock      float64       // idle time, for the beat
	sparks     []spark
	glow       []float64 // per dot, decays every frame
	glowMat    []int8    // material of the spark that last lit each dot
	mode       sparkMode
	rest       sparkMode // what gathering settles into: idle or waiting
	energy     float64   // swirl speed, eased toward the target
	t          float64   // seconds of animation
	flash      float64   // seconds left of the "done" flash
	paused     bool      // the terminal isn't focused: hold the bop
	rng        *rand.Rand

	// Celebrations: fireworks over whatever tty is doing, then a ✓ on its
	// screen once it rests.
	embers   []ember
	fx       []float64 // embers' glow per dot
	fxMat    []int8
	launches []float64 // seconds until each queued rocket goes up
	bloom    float64   // seconds left of the swirl's bloom
	proud    mascotFrame
	proudFor float64 // seconds left of the ✓, counted while tty rests

	// A poke while resting: tty spins round once.
	spinFor    float64
	spinFrames []mascotFrame
}

const (
	spinTime  = 1.1
	spinSteps = 26
)

// ember is one spark of a firework: a rocket going up, or a star flung out
// of its burst, falling as it fades.
type ember struct {
	x, y, vx, vy float64
	life, ttl    float64
	mat          int8
	rocket       bool
}

const bloomTime = 1.4

// proudTime is how long tty shows its ✓ after something ships.
const proudTime = 4.0

type sparkTickMsg struct{ id int }

func sparkTick(id int, every time.Duration) tea.Cmd {
	return tea.Tick(every, func(time.Time) tea.Msg { return sparkTickMsg{id} })
}

func newSparkField(cols, rows int) *sparkField {
	f := &sparkField{rng: rand.New(rand.NewSource(7)), rest: sparkIdle}
	f.resize(cols, rows)
	return f
}

// resize re-renders tty for a new area, keeping the current mode.
func (f *sparkField) resize(cols, rows int) {
	if cols == f.cols && rows == f.rows {
		return
	}
	f.cols, f.rows = cols, rows
	f.w, f.h = cols*2, rows*4
	f.glow = make([]float64, f.w*f.h)
	f.glowMat = make([]int8, f.w*f.h)
	f.fx = make([]float64, f.w*f.h)
	f.fxMat = make([]int8, f.w*f.h)
	f.embers = f.embers[:0]
	f.proud = mascotFrame{}
	f.spinFrames = nil
	f.frames = make([]mascotFrame, mascotLoop)
	f.frames[0] = renderMascot(f.w, f.h, mascotIdlePose(0))
	f.wait = mascotFrame{}
	f.frame = 0
	switch f.mode {
	case sparkWorking:
		f.dissolve(&f.frames[0])
	case sparkGathering:
		f.mode = sparkWorking
		f.dissolve(&f.frames[0])
		f.gather(f.rest)
	}
}

// renderMore renders up to n frames that aren't ready yet, so the cost of
// a resize is spread over the first second of animation.
func (f *sparkField) renderMore(n int) {
	if f.mode == sparkWaiting || f.rest == sparkWaiting {
		if f.wait.mat == nil {
			f.wait = renderMascot(f.w, f.h, mascotWaitPose())
			n--
		}
	}
	for i := range f.frames {
		if n <= 0 {
			return
		}
		if f.frames[i].mat == nil {
			f.frames[i] = renderMascot(f.w, f.h, mascotIdlePose(i))
			n--
		}
	}
}

func (f *sparkField) pending() bool {
	for _, fr := range f.frames {
		if fr.mat == nil {
			return true
		}
	}
	return false
}

// display is the frame to draw when tty is resting.
func (f *sparkField) display() *mascotFrame {
	if f.mode == sparkIdle && f.spinFor > 0 {
		if f.spinFrames == nil {
			f.spinFrames = make([]mascotFrame, spinSteps)
		}
		i := min(spinSteps-1, int((1-f.spinFor/spinTime)*spinSteps))
		if f.spinFrames[i].mat == nil {
			// Eased, so the turn winds up and settles.
			k := float64(i+1) / spinSteps
			k = k * k * (3 - 2*k)
			f.spinFrames[i] = renderMascot(f.w, f.h, mascotPose{yaw: 2 * math.Pi * k, pitch: -0.15, cursor: true, led: i%4 < 2})
		}
		return &f.spinFrames[i]
	}
	if f.mode == sparkIdle && f.proudFor > 0 {
		if f.proud.mat == nil {
			f.proud = renderMascot(f.w, f.h, mascotPose{pitch: -0.15, face: faceCheck, led: true})
		}
		return &f.proud
	}
	if f.mode == sparkWaiting {
		if f.wait.mat == nil {
			f.wait = renderMascot(f.w, f.h, mascotWaitPose())
		}
		return &f.wait
	}
	if f.frames[f.frame].mat == nil {
		return &f.frames[0]
	}
	return &f.frames[f.frame]
}

// setMode changes behaviour. The model asks for "gathering" whenever Claude
// isn't busy; that means idle, by way of a gather if sparks are out.
func (f *sparkField) setMode(m sparkMode) {
	if m == sparkGathering {
		m = sparkIdle
	}
	switch {
	case m == sparkWorking:
		if f.mode == sparkIdle || f.mode == sparkWaiting {
			f.dissolve(f.display())
		}
		f.mode = sparkWorking
	case f.mode == sparkWorking || (f.mode == sparkGathering && f.rest != m):
		f.gather(m)
	case f.mode == sparkGathering:
		// already on the way
	default:
		f.mode = m
	}
}

// dissolve turns the dots of a frame into sparks, each lifting off after a
// short random delay so tty comes apart rather than vanishing.
func (f *sparkField) dissolve(fr *mascotFrame) {
	f.sparks = f.sparks[:0]
	cx, cy := float64(f.w)/2, float64(f.h)/2
	maxR := cy
	for y := 0; y < f.h; y++ {
		for x := 0; x < f.w; x++ {
			if !dotOn(fr, f.w, x, y) {
				continue
			}
			px, py := float64(x)+0.5, float64(y)+0.5
			s := spark{x: px, y: py, hx: px, hy: py, mat: fr.mat[y*f.w+x]}
			s.angle = math.Atan2((py-cy)*1.5, px-cx)
			s.radius = maxR * (0.25 + 0.7*f.rng.Float64())
			s.speed = 0.6 + 0.8*f.rng.Float64()
			if f.rng.Intn(5) == 0 {
				s.speed = -s.speed * 0.6 // a few counter-rotating sparks
			}
			s.wobble = 0.08 + 0.12*f.rng.Float64()
			s.phase = f.rng.Float64() * 2 * math.Pi
			s.delay = 0.45 * f.rng.Float64()
			f.sparks = append(f.sparks, s)
		}
	}
	for i := range f.glow {
		f.glow[i] = 0
	}
	f.plot()
}

// gather sends every spark to a dot of the resting frame. Sparks and homes
// are matched by angle around the centre, so the swirl winds down into the
// shape instead of crossing over itself.
func (f *sparkField) gather(rest sparkMode) {
	f.rest = rest
	f.mode = sparkGathering
	var fr *mascotFrame
	if rest == sparkWaiting {
		if f.wait.mat == nil {
			f.wait = renderMascot(f.w, f.h, mascotWaitPose())
		}
		fr = &f.wait
	} else {
		fr = &f.frames[0]
	}
	type home struct {
		x, y float64
		mat  int8
	}
	var homes []home
	for y := 0; y < f.h; y++ {
		for x := 0; x < f.w; x++ {
			if dotOn(fr, f.w, x, y) {
				homes = append(homes, home{float64(x) + 0.5, float64(y) + 0.5, fr.mat[y*f.w+x]})
			}
		}
	}
	if len(homes) == 0 || len(f.sparks) == 0 {
		f.settle()
		return
	}
	// Every home gets at least one spark.
	for len(f.sparks) < len(homes) {
		f.sparks = append(f.sparks, f.sparks[f.rng.Intn(len(f.sparks))])
	}
	cx, cy := float64(f.w)/2, float64(f.h)/2
	ang := func(x, y float64) float64 { return math.Atan2((y-cy)*1.5, x-cx) }
	sort.Slice(homes, func(i, j int) bool { return ang(homes[i].x, homes[i].y) < ang(homes[j].x, homes[j].y) })
	sort.Slice(f.sparks, func(i, j int) bool {
		return ang(f.sparks[i].x, f.sparks[i].y) < ang(f.sparks[j].x, f.sparks[j].y)
	})
	for k := range f.sparks {
		h := homes[k*len(homes)/len(f.sparks)]
		f.sparks[k].hx, f.sparks[k].hy, f.sparks[k].mat = h.x, h.y, h.mat
	}
}

// settle ends a gather: tty takes over from the sparks.
func (f *sparkField) settle() {
	f.mode = f.rest
	f.flash = 0.6
	f.clock, f.frame = 0, 0
	for i := range f.glow {
		f.glow[i] = 0
	}
}

// animating reports whether the field changes from frame to frame.
func (f *sparkField) animating() bool {
	switch f.mode {
	case sparkWorking, sparkGathering:
		return true
	}
	// The ✓ and a spin only run down while tty rests: waiting, they mustn't
	// keep the ticks going.
	resting := f.mode == sparkIdle
	return f.flash > 0 || !f.paused || f.celebrating() || (resting && (f.proudFor > 0 || f.spinFor > 0))
}

// poke is a click on tty: a spin when it rests, a bloom of the swirl while
// it works. It reports whether tty reacted.
func (f *sparkField) poke() bool {
	switch f.mode {
	case sparkIdle:
		f.spinFor = spinTime
	case sparkWorking:
		f.bloom = bloomTime
	default:
		return false
	}
	return true
}

// celebrate sets off fireworks: one burst, or a volley for a big moment.
func (f *sparkField) celebrate(big bool) {
	f.launches = append(f.launches, 0)
	if big {
		f.launches = append(f.launches, 0.3, 0.55)
	}
	f.bloom = bloomTime
	f.proudFor = proudTime
}

// celebrating reports whether fireworks are in the air or still fading.
func (f *sparkField) celebrating() bool {
	return len(f.embers) > 0 || len(f.launches) > 0 || f.fxLit()
}

var emberMats = []int8{matGold, matPrompt, matPink, matPhone, matBody}

// stepEmbers launches due rockets, bursts the ones at the top of their
// climb, and moves every ember on.
func (f *sparkField) stepEmbers(dt float64) {
	if f.spinFor > 0 {
		f.spinFor = math.Max(0, f.spinFor-dt)
	}
	if f.proudFor > 0 && f.mode == sparkIdle && f.spinFor == 0 {
		f.proudFor = math.Max(0, f.proudFor-dt)
	}
	if f.bloom > 0 {
		f.bloom = math.Max(0, f.bloom-dt)
	}
	if !f.celebrating() {
		return
	}
	w, h := float64(f.w), float64(f.h)
	due := f.launches[:0]
	for _, l := range f.launches {
		if l -= dt; l > 0 {
			due = append(due, l)
			continue
		}
		// Up from the bottom edge to a burst in the top half.
		x := w * (0.15 + 0.7*f.rng.Float64())
		top := h * (0.12 + 0.25*f.rng.Float64())
		const climb = 0.4
		f.embers = append(f.embers, ember{x: x, y: h - 1, vx: (f.rng.Float64() - 0.5) * w * 0.15, vy: -(h - top) / climb,
			ttl: climb, mat: matGold, rocket: true})
	}
	f.launches = due
	live := f.embers[:0]
	var bursts []ember
	for _, e := range f.embers {
		e.life += dt
		if e.life >= e.ttl {
			if e.rocket {
				bursts = append(bursts, e)
			}
			continue
		}
		if !e.rocket {
			drag := math.Pow(0.2, dt)
			e.vx *= drag
			e.vy = e.vy*drag + h*0.9*dt // and gravity
		}
		e.x += e.vx * dt
		e.y += e.vy * dt
		live = append(live, e)
	}
	f.embers = live
	for _, r := range bursts {
		mat := emberMats[f.rng.Intn(len(emberMats))]
		// Two rings, so the burst has depth.
		for ring, n := range []int{28, 14} {
			speed := h * (0.9 - 0.4*float64(ring))
			for i := 0; i < n; i++ {
				a := 2 * math.Pi * (float64(i) + f.rng.Float64()*0.3) / float64(n)
				v := speed * (0.9 + 0.2*f.rng.Float64())
				f.embers = append(f.embers, ember{x: r.x, y: r.y, vx: v * math.Cos(a) * 1.3, vy: v * math.Sin(a),
					ttl: 0.8 + 0.5*f.rng.Float64(), mat: mat})
			}
		}
	}
	for i := range f.fx {
		f.fx[i] *= 0.5
	}
	for _, e := range f.embers {
		x, y := int(e.x), int(e.y)
		if x >= 0 && x < f.w && y >= 0 && y < f.h {
			f.fx[y*f.w+x] = 1 - 0.5*e.life/e.ttl
			f.fxMat[y*f.w+x] = e.mat
		}
	}
}

// fxLit reports whether any ember glow is still on screen.
func (f *sparkField) fxLit() bool {
	for _, g := range f.fx {
		if g > 0.15 {
			return true
		}
	}
	return false
}

// interval is the tick rate for the current mode: smooth for the swirl,
// tty's own frame rate otherwise.
func (f *sparkField) interval() time.Duration {
	if f.mode == sparkWorking || f.mode == sparkGathering || f.celebrating() || f.spinFor > 0 {
		return time.Second / sparkFPS
	}
	return time.Second / mascotFPS
}

// step advances the animation by dt seconds. targetEnergy sets how fast
// the swirl turns while working.
func (f *sparkField) step(targetEnergy, dt float64) {
	f.t += dt
	f.stepEmbers(dt)
	f.energy += (targetEnergy - f.energy) * 0.08
	if f.flash > 0 {
		f.flash = math.Max(0, f.flash-dt)
	}
	f.renderMore(2)
	switch f.mode {
	case sparkIdle:
		if !f.paused {
			f.clock += dt
		}
		f.frame = int(f.clock*mascotFPS) % mascotLoop
		return
	case sparkWaiting:
		return
	}

	cx, cy := float64(f.w)/2, float64(f.h)/2
	settled := true
	for i := range f.sparks {
		s := &f.sparks[i]
		if f.mode == sparkWorking {
			if s.delay > 0 {
				s.delay -= dt
				continue
			}
			// Each spark eases toward its moving spot on its orbit.
			s.angle += s.speed * f.energy * dt * 2.2
			r := s.radius * (1 + s.wobble*math.Sin(f.t*2.3*s.speed+s.phase))
			if f.bloom > 0 {
				r *= 1 + 0.3*math.Sin(math.Pi*(1-f.bloom/bloomTime))
			}
			tx, ty := cx+1.5*r*math.Cos(s.angle), cy+r*math.Sin(s.angle)
			s.vx = (tx - s.x) * 0.18
			s.vy = (ty - s.y) * 0.18
		} else {
			// Damped spring home, with a little overshoot.
			s.vx = s.vx*0.78 + (s.hx-s.x)*0.09
			s.vy = s.vy*0.78 + (s.hy-s.y)*0.09
			if math.Abs(s.hx-s.x) > 0.35 || math.Abs(s.hy-s.y) > 0.35 || math.Abs(s.vx)+math.Abs(s.vy) > 0.1 {
				settled = false
			}
		}
		s.x += s.vx
		s.y += s.vy
	}
	if f.mode == sparkGathering && settled {
		f.settle()
		return
	}
	// Trails: fade every dot, then light the dots under the sparks.
	for i := range f.glow {
		f.glow[i] *= sparkTrail
	}
	f.plot()
}

func (f *sparkField) plot() {
	twinkle := int(f.t * 10)
	for i, s := range f.sparks {
		x, y := int(s.x), int(s.y)
		if x >= 0 && x < f.w && y >= 0 && y < f.h {
			f.glow[y*f.w+x] = 1
			f.glowMat[y*f.w+x] = s.mat
			if f.bloom > 0 {
				f.glowMat[y*f.w+x] = emberMats[(i+twinkle)%len(emberMats)]
			}
		}
	}
}

// braille bit for each dot position in a 2×4 cell.
var brailleBits = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// mascotColor is the theme colour for a material.
func mascotColor(th theme, m int8) ac {
	switch m {
	case matPhone:
		return th.second
	case matPink:
		c := resolve(th.err).BlendLab(resolve(th.text), 0.4).Clamped().Hex()
		return ac{Light: c, Dark: c}
	case matEye:
		return th.text
	case matGold:
		return th.warn
	case matDark:
		return th.subtle
	case matPrompt:
		return th.ok
	}
	return th.accent
}

const mascotShades = 6

type mascotStyleSet [matCount][mascotShades]lipgloss.Style

// mascotStyles builds a shade ramp per material, from the subtle colour up to
// the material's own; tweak may adjust a material's colour first.
func mascotStyles(th theme, tweak func(m int8, c ac) ac) *mascotStyleSet {
	var st mascotStyleSet
	for m := int8(0); m < matCount; m++ {
		c := mascotColor(th, m)
		if tweak != nil {
			c = tweak(m, c)
		}
		for i, rc := range gradient(mascotShades, th.subtle, c, c) {
			st[m][i] = lipgloss.NewStyle().Foreground(rc)
		}
	}
	return &st
}

// drawMascot renders a frame as Braille: dithered dots, each cell coloured
// by its dominant material and shaded by how lit it is.
func drawMascot(fr *mascotFrame, w, cols, rows int, st *mascotStyleSet) string {
	var b strings.Builder
	for row := 0; row < rows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		for col := 0; col < cols; col++ {
			var bits rune
			var weight [matCount]float64
			lit, n := 0.0, 0
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					x, y := col*2+dx, row*4+dy
					m := fr.mat[y*w+x]
					// The screen draws no dots, so it mustn't colour or dim the cells
					// it shares with the prompt.
					if m < 0 || m == matDark {
						continue
					}
					lit += float64(fr.inten[y*w+x])
					n++
					if m == matEye || m == matPink || m == matGold || m == matPrompt {
						weight[m] += 3
					} else {
						weight[m]++
					}
					if dotOn(fr, w, x, y) {
						bits |= brailleBits[dy][dx]
					}
				}
			}
			if bits == 0 {
				b.WriteByte(' ')
				continue
			}
			k := 0.35 + 0.65*lit/float64(n) // dithering already carries the shading
			b.WriteString(st[dominant(weight)][shadeIndex(k)].Render(string(0x2800 + bits)))
		}
	}
	return b.String()
}

func dominant(weight [matCount]float64) int8 {
	best := int8(0)
	for m := int8(1); m < matCount; m++ {
		if weight[m] > weight[best] {
			best = m
		}
	}
	return best
}

func shadeIndex(k float64) int {
	return max(0, min(mascotShades-1, int(math.Round(k*(mascotShades-1)))))
}

// view renders the field: tty's dithered frame while resting, the
// sparks and their trails otherwise. Waiting pulses the headphones in the
// warning colour; the done flash brightens everything for a moment.
func (f *sparkField) view() string {
	th := currentTheme()
	st := mascotStyles(th, func(m int8, c ac) ac {
		if m == matPhone && f.mode == sparkWaiting {
			k := 0.5 + 0.5*math.Sin(f.t*5)
			hx := resolve(th.second).BlendLab(resolve(th.warn), k).Clamped().Hex()
			c = ac{Light: hx, Dark: hx}
		}
		if f.flash > 0 {
			hx := resolve(c).BlendLab(resolve(th.text), f.flash/0.6*0.7).Clamped().Hex()
			c = ac{Light: hx, Dark: hx}
		}
		return c
	})
	if f.celebrating() {
		return f.composite(th, st)
	}
	if f.mode == sparkIdle || f.mode == sparkWaiting {
		return drawMascot(f.display(), f.w, f.cols, f.rows, st)
	}

	var b strings.Builder
	for row := 0; row < f.rows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		for col := 0; col < f.cols; col++ {
			var bits rune
			var weight [matCount]float64
			peak := 0.0
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					i := (row*4+dy)*f.w + col*2 + dx
					if g := f.glow[i]; g > 0.18 {
						bits |= brailleBits[dy][dx]
						weight[f.glowMat[i]] += g
						peak = math.Max(peak, g)
					}
				}
			}
			if bits == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(st[dominant(weight)][shadeIndex(peak)].Render(string(0x2800 + bits)))
		}
	}
	return b.String()
}

// composite draws tty or the swirl with fireworks over it. Where an ember
// glows brightly its colour wins the cell.
func (f *sparkField) composite(th theme, st *mascotStyleSet) string {
	fxSt := mascotStyles(th, func(m int8, c ac) ac {
		hx := resolve(c).BlendLab(resolve(th.text), 0.25).Clamped().Hex()
		return ac{Light: hx, Dark: hx}
	})
	resting := f.mode == sparkIdle || f.mode == sparkWaiting
	var fr *mascotFrame
	if resting {
		fr = f.display()
	}
	var b strings.Builder
	for row := 0; row < f.rows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		for col := 0; col < f.cols; col++ {
			var bits, fxBits rune
			var weight, fxWeight [matCount]float64
			lit, n, peak, fxPeak := 0.0, 0, 0.0, 0.0
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					x, y := col*2+dx, row*4+dy
					i := y*f.w + x
					if resting {
						if m := fr.mat[i]; m >= 0 && m != matDark {
							lit += float64(fr.inten[i])
							n++
							if m == matEye || m == matPink || m == matGold || m == matPrompt {
								weight[m] += 3
							} else {
								weight[m]++
							}
							if dotOn(fr, f.w, x, y) {
								bits |= brailleBits[dy][dx]
							}
						}
					} else if g := f.glow[i]; g > 0.18 {
						bits |= brailleBits[dy][dx]
						weight[f.glowMat[i]] += g
						peak = math.Max(peak, g)
					}
					if g := f.fx[i]; g > 0.15 {
						fxBits |= brailleBits[dy][dx]
						fxWeight[f.fxMat[i]] += g
						fxPeak = math.Max(fxPeak, g)
					}
				}
			}
			if bits|fxBits == 0 {
				b.WriteByte(' ')
				continue
			}
			var style lipgloss.Style
			switch {
			case fxBits != 0 && (fxPeak >= 0.3 || bits == 0):
				style = fxSt[dominant(fxWeight)][shadeIndex(0.4+0.6*fxPeak)]
			case resting && n > 0:
				style = st[dominant(weight)][shadeIndex(0.35+0.65*lit/float64(n))]
			default:
				style = st[dominant(weight)][shadeIndex(peak)]
			}
			b.WriteString(style.Render(string(0x2800 + (bits | fxBits))))
		}
	}
	return b.String()
}

// ── Model integration ───────────────────────────────────────────────────

// sparkRows is how many rows the indicator takes in a sidebar of the
// given height: about a quarter, or none when the sidebar is too short.
func sparkRows(sidebarHeight int) int {
	if sidebarHeight < sparkMinTotal {
		return 0
	}
	return max(sparkMinRows, sidebarHeight/4)
}

// sparkTarget derives the indicator's mode and swirl energy from the
// session state.
func (m Model) sparkTarget() (sparkMode, float64) {
	switch {
	case len(m.pending) > 0:
		return sparkWaiting, 0
	case m.busy:
		e := 0.7
		switch {
		case strings.HasPrefix(m.activity, "Running"):
			e = 1.3
		case m.activity == "Writing":
			e = 1.0
		}
		return sparkWorking, e + 0.25*float64(m.track.runningAgents())
	}
	return sparkGathering, 0
}

// idleBop is how long tty keeps bopping after the last input or
// turn. Not every terminal reports focus (tmux doesn't by default), so
// without a limit it would redraw the screen forever.
const idleBop = time.Minute

// idle reports whether idle tty should hold still: the terminal is
// unfocused, or nothing has happened for a while.
func (m *Model) idle() bool {
	last := m.lastInput
	if m.lastTurnAt.After(last) {
		last = m.lastTurnAt
	}
	return !m.focused || time.Since(last) > idleBop
}

// syncSparks updates the field's mode and starts its tick loop if needed.
func (m *Model) syncSparks() tea.Cmd {
	if m.sparks == nil || !m.sidebarVisible() || sparkRows(m.height-1) == 0 {
		return nil
	}
	mode, _ := m.sparkTarget()
	m.sparks.setMode(mode)
	m.sparks.paused = m.idle()
	if m.sparkTicking || !m.sparks.animating() {
		return nil
	}
	m.sparkTicking = true
	m.sparkID++
	m.sparkLast = time.Now()
	return sparkTick(m.sparkID, m.sparks.interval())
}

func (m *Model) onSparkTick(msg sparkTickMsg) tea.Cmd {
	if msg.id != m.sparkID {
		return nil
	}
	mode, energy := m.sparkTarget()
	m.sparks.setMode(mode)
	m.sparks.paused = m.idle()
	now := time.Now()
	dt := math.Min(0.1, now.Sub(m.sparkLast).Seconds())
	m.sparkLast = now
	m.sparks.step(energy, dt)
	if !m.sparks.animating() || !m.sidebarVisible() {
		m.sparkTicking = false
		return nil
	}
	return sparkTick(m.sparkID, m.sparks.interval())
}

// sparkCaption is the line under the indicator.
func (m Model) sparkCaption(w int) string {
	var s string
	switch {
	case len(m.pending) > 0:
		s = warnStyle.Bold(true).Render("Waiting for you")
	case m.cheer != "" && time.Since(m.cheerAt) < time.Duration(proudTime*float64(time.Second)):
		s = warnStyle.Bold(true).Render("✦ " + m.cheer)
	case m.quip != "" && time.Since(m.quipAt) < 2*time.Second:
		s = lipgloss.NewStyle().Foreground(currentTheme().second).Render(m.quip)
	case m.busy:
		label := firstNonEmpty(m.activity, "Working")
		s = statusKey.Render(label) + mutedStyle.Render(" · "+shortSecs(time.Since(m.turnFrom)))
	case m.lastTurn > 0 && time.Since(m.lastTurnAt) < 8*time.Second:
		s = okStyle.Render("Done") + mutedStyle.Render(" in "+shortSecs(m.lastTurn))
	default:
		s = mutedStyle.Render("Ready")
	}
	return lipgloss.PlaceHorizontal(w, lipgloss.Center, ansi.Truncate(s, w, "…"))
}
