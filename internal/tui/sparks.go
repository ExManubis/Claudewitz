package tui

import (
	"math"
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The sidebar's working indicator: sparks that rest as a ✻, scatter into
// a swirl while Claude works, and gather back when the turn ends.
//
// Positions are in Braille dot space: each character cell holds 2×4 dots,
// and since cells are about twice as tall as wide, dots come out square.

type sparkMode int

const (
	sparkIdle      sparkMode = iota // formed ✻, nothing moving
	sparkWorking                    // swirling
	sparkGathering                  // springing home after a turn
	sparkWaiting                    // formed and pulsing: Claude needs you
)

const (
	sparkFPS      = 24
	sparkTrail    = 0.62 // how much of a dot's glow survives each frame
	sparkMinRows  = 5
	sparkMinTotal = 20 // sidebar rows needed before the indicator shows
)

type spark struct {
	x, y, vx, vy float64 // position and velocity
	hx, hy       float64 // home, in the ✻
	// swirl orbit, so each spark has its own lane and pace
	angle, radius, speed, wobble, phase float64
}

type sparkField struct {
	cols, rows int
	w, h       int // dot dimensions
	sparks     []spark
	glow       []float64 // per dot, decays every frame
	mode       sparkMode
	energy     float64 // swirl speed, eased toward the target
	t          float64 // seconds of animation
	flash      float64 // seconds left of the "done" flash
	rng        *rand.Rand
}

type sparkTickMsg struct{ id int }

func sparkTick(id int) tea.Cmd {
	return tea.Tick(time.Second/sparkFPS, func(time.Time) tea.Msg { return sparkTickMsg{id} })
}

func newSparkField(cols, rows int) *sparkField {
	f := &sparkField{rng: rand.New(rand.NewSource(7))}
	f.resize(cols, rows)
	return f
}

// resize rebuilds the ✻ for a new area, keeping the current mode.
func (f *sparkField) resize(cols, rows int) {
	if cols == f.cols && rows == f.rows {
		return
	}
	f.cols, f.rows = cols, rows
	f.w, f.h = cols*2, rows*4
	f.glow = make([]float64, f.w*f.h)
	homes := starPoints(f.w, f.h)
	f.sparks = make([]spark, len(homes))
	cx, cy := float64(f.w)/2, float64(f.h)/2
	maxR := math.Min(cx, cy)
	for i, p := range homes {
		s := &f.sparks[i]
		s.hx, s.hy = p[0], p[1]
		s.x, s.y = p[0], p[1]
		s.angle = math.Atan2(p[1]-cy, p[0]-cx)
		s.radius = maxR * (0.25 + 0.7*f.rng.Float64())
		s.speed = 0.6 + 0.8*f.rng.Float64()
		if f.rng.Intn(5) == 0 {
			s.speed = -s.speed * 0.6 // a few counter-rotating sparks
		}
		s.wobble = 0.08 + 0.12*f.rng.Float64()
		s.phase = f.rng.Float64() * 2 * math.Pi
	}
	f.plot()
}

// starPoints fills an eight-petal ✻ with points on a dot grid: tapered
// petals around a small hub, sized to the area.
func starPoints(w, h int) [][2]float64 {
	cx, cy := float64(w)/2, float64(h)/2
	r := math.Min(cx, cy) - 1
	var pts [][2]float64
	const petals = 8
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := math.Hypot(dx, dy)
			if d > r {
				continue
			}
			if d < r*0.16 { // hub
				pts = append(pts, [2]float64{float64(x) + 0.5, float64(y) + 0.5})
				continue
			}
			// Distance from the nearest petal's centre line, against the
			// petal's half-width at this radius (widest a third of the way out).
			a := math.Atan2(dy, dx) + math.Pi/petals/2
			sector := 2 * math.Pi / petals
			off := math.Mod(a+2*math.Pi, sector) - sector/2
			t := d / r
			half := 0.16 * r * math.Pow(math.Sin(math.Pi*math.Min(1, (t-0.1)/0.9)), 0.9)
			if math.Abs(d*math.Sin(off)) <= half && math.Cos(off) > 0 {
				pts = append(pts, [2]float64{float64(x) + 0.5, float64(y) + 0.5})
			}
		}
	}
	// Thin the fill so the sparks have room to move, keeping the outline.
	out := pts[:0]
	for i, p := range pts {
		if i%2 == 0 || math.Hypot(p[0]-cx, p[1]-cy) > r*0.85 {
			out = append(out, p)
		}
	}
	return out
}

// setMode changes behaviour; it reports whether the field needs ticking.
func (f *sparkField) setMode(m sparkMode) {
	if f.mode == m {
		return
	}
	if m == sparkGathering && f.mode != sparkWorking {
		m = sparkIdle // nothing to gather from
	}
	if m == sparkIdle && f.mode == sparkWorking {
		m = sparkGathering
	}
	f.mode = m
}

// animating reports whether the field changes from frame to frame.
func (f *sparkField) animating() bool {
	return f.mode != sparkIdle || f.flash > 0 || f.anyGlow()
}

func (f *sparkField) anyGlow() bool {
	for _, g := range f.glow {
		if g > 0.05 && g < 0.99 {
			return true
		}
	}
	return false
}

// step advances the simulation by one frame. targetEnergy sets how fast the
// swirl turns while working.
func (f *sparkField) step(targetEnergy float64) {
	dt := 1.0 / sparkFPS
	f.t += dt
	f.energy += (targetEnergy - f.energy) * 0.08
	if f.flash > 0 {
		f.flash = math.Max(0, f.flash-dt)
	}
	cx, cy := float64(f.w)/2, float64(f.h)/2

	settled := true
	for i := range f.sparks {
		s := &f.sparks[i]
		switch f.mode {
		case sparkWorking:
			// Each spark eases toward its moving spot on its orbit.
			s.angle += s.speed * f.energy * dt * 2.2
			r := s.radius * (1 + s.wobble*math.Sin(f.t*2.3*s.speed+s.phase))
			tx, ty := cx+r*math.Cos(s.angle), cy+r*math.Sin(s.angle)
			s.vx = (tx - s.x) * 0.18
			s.vy = (ty - s.y) * 0.18
		default:
			// Damped spring home, with a little overshoot.
			hx, hy := s.hx, s.hy
			if f.mode == sparkWaiting {
				// Breathe: scale the star slightly in and out.
				k := 1 + 0.04*math.Sin(f.t*4)
				hx, hy = cx+(s.hx-cx)*k, cy+(s.hy-cy)*k
			}
			s.vx = s.vx*0.78 + (hx-s.x)*0.09
			s.vy = s.vy*0.78 + (hy-s.y)*0.09
			if math.Abs(hx-s.x) > 0.35 || math.Abs(hy-s.y) > 0.35 || math.Abs(s.vx)+math.Abs(s.vy) > 0.1 {
				settled = false
			}
		}
		s.x += s.vx
		s.y += s.vy
	}
	if f.mode == sparkGathering && settled {
		for i := range f.sparks {
			f.sparks[i].x, f.sparks[i].y = f.sparks[i].hx, f.sparks[i].hy
			f.sparks[i].vx, f.sparks[i].vy = 0, 0
		}
		f.mode = sparkIdle
		f.flash = 0.7
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
		}
	}
}

// settle snaps every spark home (used when there's nothing to animate).
func (f *sparkField) settle() {
	for i := range f.sparks {
		f.sparks[i].x, f.sparks[i].y = f.sparks[i].hx, f.sparks[i].hy
		f.sparks[i].vx, f.sparks[i].vy = 0, 0
	}
	for i := range f.glow {
		f.glow[i] = 0
	}
	f.plot()
}

// braille bit for each dot position in a 2×4 cell.
var brailleBits = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// view renders the field. Fresh dots take the accent colour, fading trails
// cool toward the secondary colour; waiting pulses in the warning colour and
// the done flash brightens everything briefly.
func (f *sparkField) view() string {
	th := currentTheme()
	const shades = 8
	var ramp []lipgloss.Color
	switch {
	case f.mode == sparkWaiting:
		k := 0.5 + 0.5*math.Sin(f.t*4)
		hi := fade(th.warn, 0.45*(1-k))
		ramp = gradient(shades, th.subtle, ac{Light: string(hi), Dark: string(hi)})
	case f.flash > 0:
		ramp = gradient(shades, th.second, th.text)
	default:
		ramp = gradient(shades, th.subtle, th.second, th.accent)
	}
	styles := make([]lipgloss.Style, shades)
	for i, c := range ramp {
		styles[i] = lipgloss.NewStyle().Foreground(c)
	}

	var b strings.Builder
	for row := 0; row < f.rows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		for col := 0; col < f.cols; col++ {
			var bits rune
			peak := 0.0
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					g := f.glow[(row*4+dy)*f.w+col*2+dx]
					if g > 0.18 {
						bits |= brailleBits[dy][dx]
					}
					peak = math.Max(peak, g)
				}
			}
			if bits == 0 {
				b.WriteByte(' ')
				continue
			}
			shade := int(peak * float64(shades-1))
			b.WriteString(styles[shade].Render(string(0x2800 + bits)))
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
	if m.sparks == nil || !m.showSidebar() || sparkRows(m.height-1) == 0 {
		return nil
	}
	mode, _ := m.sparkTarget()
	m.sparks.setMode(mode)
	if m.sparkTicking || !m.sparks.animating() {
		return nil
	}
	m.sparkTicking = true
	m.sparkID++
	return sparkTick(m.sparkID)
}

func (m *Model) onSparkTick(msg sparkTickMsg) tea.Cmd {
	if msg.id != m.sparkID {
		return nil
	}
	mode, energy := m.sparkTarget()
	m.sparks.setMode(mode)
	m.sparks.step(energy)
	if !m.sparks.animating() || !m.showSidebar() {
		m.sparkTicking = false
		return nil
	}
	return sparkTick(m.sparkID)
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
