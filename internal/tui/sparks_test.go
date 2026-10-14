package tui

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
)

func TestSparkRows(t *testing.T) {
	for _, c := range []struct{ h, want int }{{10, 0}, {19, 0}, {20, 5}, {24, 6}, {40, 10}, {60, 15}} {
		if got := sparkRows(c.h); got != c.want {
			t.Errorf("sparkRows(%d) = %d, want %d", c.h, got, c.want)
		}
	}
}

func TestSparkFieldView(t *testing.T) {
	f := newSparkField(30, 8)
	lines := strings.Split(f.view(), "\n")
	if len(lines) != 8 {
		t.Fatalf("got %d lines, want 8", len(lines))
	}
	lit := 0
	for _, l := range lines {
		if w := ansi.StringWidth(l); w != 30 {
			t.Errorf("line width %d, want 30", w)
		}
		for _, r := range ansi.Strip(l) {
			if r > 0x2800 && r <= 0x28ff {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Error("idle bean renders no dots")
	}
	f.resize(20, 5)
	if n := len(strings.Split(f.view(), "\n")); n != 5 {
		t.Errorf("after resize got %d lines, want 5", n)
	}
}

func TestSparkModes(t *testing.T) {
	f := newSparkField(30, 8)
	dt := 1.0 / sparkFPS
	if f.mode != sparkIdle || !f.animating() {
		t.Error("a fresh field should be bopping")
	}
	f.paused = true
	if f.animating() {
		t.Error("a paused idle field should be still")
	}
	f.paused = false
	for i := 0; i < beanLoop; i++ {
		f.step(0, dt)
	}
	if f.pending() {
		t.Error("idle loop frames still unrendered after a loop's worth of ticks")
	}

	f.setMode(sparkWorking)
	if f.mode != sparkWorking || len(f.sparks) == 0 {
		t.Fatalf("mode %v with %d sparks, want working with sparks", f.mode, len(f.sparks))
	}
	for i := 0; i < sparkFPS*2; i++ {
		f.step(1, dt)
	}
	moved := 0
	for _, s := range f.sparks {
		if math.Abs(s.x-s.hx)+math.Abs(s.y-s.hy) > 1 {
			moved++
		}
	}
	if moved < len(f.sparks)/2 {
		t.Errorf("only %d of %d sparks left the bean while working", moved, len(f.sparks))
	}

	// The model's "not busy" request gathers, then settles with a flash.
	f.setMode(sparkGathering)
	if f.mode != sparkGathering {
		t.Fatalf("mode = %v, want gathering", f.mode)
	}
	for i := 0; i < sparkFPS*10 && f.mode == sparkGathering; i++ {
		f.step(0, dt)
	}
	if f.mode != sparkIdle || f.flash <= 0 {
		t.Fatalf("mode = %v flash = %v, want idle with a flash", f.mode, f.flash)
	}

	// Waiting mid-turn gathers into the waiting pose instead.
	f.setMode(sparkWorking)
	f.setMode(sparkWaiting)
	for i := 0; i < sparkFPS*10 && f.mode == sparkGathering; i++ {
		f.step(0, dt)
	}
	if f.mode != sparkWaiting {
		t.Fatalf("mode = %v, want waiting", f.mode)
	}
	if !strings.ContainsFunc(ansi.Strip(f.view()), func(r rune) bool { return r > 0x2800 && r <= 0x28ff }) {
		t.Error("waiting bean renders no dots")
	}
}

func BenchmarkRenderBean(b *testing.B) {
	for i := 0; i < b.N; i++ {
		renderBean(70, 40, beanIdlePose(i%beanLoop))
	}
}

func TestSparkTarget(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	if mode, _ := m.sparkTarget(); mode != sparkGathering {
		t.Errorf("idle model: mode %v", mode)
	}
	m.busy = true
	m.activity = "Thinking"
	_, slow := m.sparkTarget()
	m.activity = "Running Bash"
	mode, fast := m.sparkTarget()
	if mode != sparkWorking || fast <= slow {
		t.Errorf("busy: mode %v energy %v (thinking %v)", mode, fast, slow)
	}
}

// Going busy adds the activity line under the chat; the screen must still
// fit the terminal, or it scrolls and the sidebar jumps up a line.
func TestBusyKeepsScreenHeight(t *testing.T) {
	m := New(context.Background(), claude.Options{CWD: t.TempDir()}, config.Config{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	m = next.(Model)
	height := func() int { return strings.Count(m.View(), "\n") + 1 }
	if h := height(); h != 44 {
		t.Fatalf("idle screen is %d lines, want 44", h)
	}
	m.busy = true
	m.activity = "Thinking"
	next, _ = m.Update(nil)
	m = next.(Model)
	if h := height(); h != 44 {
		t.Errorf("busy screen is %d lines, want 44", h)
	}
}

// The idle bop stops after a while without input, even when the terminal
// never reports losing focus, and starts again on the next key.
func TestIdleBopStopsWithoutInput(t *testing.T) {
	m := chatModel(t)
	m.width, m.height = 140, 40 // room for the sidebar and its sparks
	m.layout()
	m.lastInput = time.Now().Add(-2 * idleBop)
	m.syncSparks()
	if !m.sparks.paused || m.sparks.animating() {
		t.Fatal("bop should hold still after idleBop without input")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if nm := next.(Model); nm.sparks.paused {
		t.Fatal("a key should restart the bop")
	}
}
