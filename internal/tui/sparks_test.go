package tui

import (
	"context"
	"strings"
	"testing"

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
	if len(f.sparks) == 0 {
		t.Fatal("no sparks")
	}
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
		t.Error("idle star renders no dots")
	}
	f.resize(20, 5)
	if n := len(strings.Split(f.view(), "\n")); n != 5 {
		t.Errorf("after resize got %d lines, want 5", n)
	}
}

func TestSparkModes(t *testing.T) {
	f := newSparkField(30, 8)
	if f.animating() {
		t.Error("a fresh field should be still")
	}
	// Gathering with nothing to gather from stays idle.
	f.setMode(sparkGathering)
	if f.mode != sparkIdle {
		t.Errorf("mode = %v, want idle", f.mode)
	}

	f.setMode(sparkWorking)
	for i := 0; i < sparkFPS*2; i++ {
		f.step(1)
	}
	moved := 0
	for _, s := range f.sparks {
		if s.x != s.hx || s.y != s.hy {
			moved++
		}
	}
	if moved < len(f.sparks)/2 {
		t.Errorf("only %d of %d sparks left home while working", moved, len(f.sparks))
	}

	// Going idle from working gathers, then settles with a flash.
	f.setMode(sparkIdle)
	if f.mode != sparkGathering {
		t.Fatalf("mode = %v, want gathering", f.mode)
	}
	for i := 0; i < sparkFPS*10 && f.mode == sparkGathering; i++ {
		f.step(0)
	}
	if f.mode != sparkIdle || f.flash <= 0 {
		t.Fatalf("mode = %v flash = %v, want idle with a flash", f.mode, f.flash)
	}
	for i := 0; i < sparkFPS*3; i++ {
		f.step(0)
	}
	if f.animating() {
		t.Error("field still animating after settling")
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
