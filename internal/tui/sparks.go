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

// The sidebar's working indicator: the headphone bean bops to a beat while
// Claude is idle, dissolves into a swirl of sparks while it works, and the
// sparks gather back into the bean when the turn ends.
//
// Positions are in Braille dot space: each character cell holds 2×4 dots,
// and since cells are about twice as tall as wide, dots come out square.

type sparkMode int

const (
	sparkIdle      sparkMode = iota // the bean, bopping
	sparkWorking                    // swirling
	sparkGathering                  // springing back into the bean
	sparkWaiting                    // the bean, still and wide-eyed: Claude needs you
)

const (
	sparkFPS      = 24
	sparkTrail    = 0.62 // how much of a dot's glow survives each frame
	sparkMinRows  = 5
	sparkMinTotal = 20 // sidebar rows needed before the indicator shows
)

type spark struct {
	x, y, vx, vy float64 // position and velocity
	hx, hy       float64 // home, in the bean
	// swirl orbit, so each spark has its own lane and pace
	angle, radius, speed, wobble, phase float64
	delay                               float64 // seconds before it lifts off when dissolving
	mat                                 int8
}

type sparkField struct {
	cols, rows int
	w, h       int         // dot dimensions
	frames     []beanFrame // the idle loop, rendered a few at a time
	wait       beanFrame   // the waiting pose, rendered on first use
	frame      int         // idle frame on screen
	clock      float64     // idle time, for the beat
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
}

type sparkTickMsg struct{ id int }

func sparkTick(id int, every time.Duration) tea.Cmd {
	return tea.Tick(every, func(time.Time) tea.Msg { return sparkTickMsg{id} })
}

func newSparkField(cols, rows int) *sparkField {
	f := &sparkField{rng: rand.New(rand.NewSource(7)), rest: sparkIdle}
	f.resize(cols, rows)
	return f
}

// resize re-renders the bean for a new area, keeping the current mode.
func (f *sparkField) resize(cols, rows int) {
	if cols == f.cols && rows == f.rows {
		return
	}
	f.cols, f.rows = cols, rows
	f.w, f.h = cols*2, rows*4
	f.glow = make([]float64, f.w*f.h)
	f.glowMat = make([]int8, f.w*f.h)
	f.frames = make([]beanFrame, beanLoop)
	f.frames[0] = renderBean(f.w, f.h, beanIdlePose(0))
	f.wait = beanFrame{}
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
			f.wait = renderBean(f.w, f.h, beanWaitPose())
			n--
		}
	}
	for i := range f.frames {
		if n <= 0 {
			return
		}
		if f.frames[i].mat == nil {
			f.frames[i] = renderBean(f.w, f.h, beanIdlePose(i))
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

// display is the frame to draw when the bean is resting.
func (f *sparkField) display() *beanFrame {
	if f.mode == sparkWaiting {
		if f.wait.mat == nil {
			f.wait = renderBean(f.w, f.h, beanWaitPose())
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
// short random delay so the bean comes apart rather than vanishing.
func (f *sparkField) dissolve(fr *beanFrame) {
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
	var fr *beanFrame
	if rest == sparkWaiting {
		if f.wait.mat == nil {
			f.wait = renderBean(f.w, f.h, beanWaitPose())
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

// settle ends a gather: the bean takes over from the sparks.
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
	return f.flash > 0 || !f.paused
}

// interval is the tick rate for the current mode: smooth for the swirl,
// the bean's own frame rate otherwise.
func (f *sparkField) interval() time.Duration {
	if f.mode == sparkWorking || f.mode == sparkGathering {
		return time.Second / sparkFPS
	}
	return time.Second / beanFPS
}

// step advances the animation by dt seconds. targetEnergy sets how fast
// the swirl turns while working.
func (f *sparkField) step(targetEnergy, dt float64) {
	f.t += dt
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
		f.frame = int(f.clock*beanFPS) % beanLoop
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
	for _, s := range f.sparks {
		x, y := int(s.x), int(s.y)
		if x >= 0 && x < f.w && y >= 0 && y < f.h {
			f.glow[y*f.w+x] = 1
			f.glowMat[y*f.w+x] = s.mat
		}
	}
}

// braille bit for each dot position in a 2×4 cell.
var brailleBits = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// beanColor is the theme colour for a material.
func beanColor(th theme, m int8) ac {
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
	}
	return th.accent
}

const beanShades = 6

type beanStyleSet [matCount][beanShades]lipgloss.Style

// beanStyles builds a shade ramp per material, from the subtle colour up to
// the material's own; tweak may adjust a material's colour first.
func beanStyles(th theme, tweak func(m int8, c ac) ac) *beanStyleSet {
	var st beanStyleSet
	for m := int8(0); m < matCount; m++ {
		c := beanColor(th, m)
		if tweak != nil {
			c = tweak(m, c)
		}
		for i, rc := range gradient(beanShades, th.subtle, c, c) {
			st[m][i] = lipgloss.NewStyle().Foreground(rc)
		}
	}
	return &st
}

// drawBean renders a frame as Braille: dithered dots, each cell coloured
// by its dominant material and shaded by how lit it is.
func drawBean(fr *beanFrame, w, cols, rows int, st *beanStyleSet) string {
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
					if m < 0 {
						continue
					}
					lit += float64(fr.inten[y*w+x])
					n++
					if m == matEye || m == matPink || m == matGold {
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
	return max(0, min(beanShades-1, int(math.Round(k*(beanShades-1)))))
}

// view renders the field: the bean's dithered frame while resting, the
// sparks and their trails otherwise. Waiting pulses the headphones in the
// warning colour; the done flash brightens everything for a moment.
func (f *sparkField) view() string {
	th := currentTheme()
	st := beanStyles(th, func(m int8, c ac) ac {
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
	if f.mode == sparkIdle || f.mode == sparkWaiting {
		return drawBean(f.display(), f.w, f.cols, f.rows, st)
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

// syncSparks updates the field's mode and starts its tick loop if needed.
func (m *Model) syncSparks() tea.Cmd {
	if m.sparks == nil || !m.sidebarVisible() || sparkRows(m.height-1) == 0 {
		return nil
	}
	mode, _ := m.sparkTarget()
	m.sparks.setMode(mode)
	m.sparks.paused = !m.focused
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
	m.sparks.paused = !m.focused
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
