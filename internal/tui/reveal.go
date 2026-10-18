package tui

import (
	"math"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// Streamed text arrives in uneven bursts: a few characters, a pause, then a
// whole line. Instead of drawing each burst as it lands, live replies and
// thinking are revealed at a steady pace that speeds up as the backlog
// grows, so text flows rather than jumps while staying close behind the
// stream.

const (
	revealFPS = 60
	// revealTau is how quickly the reveal catches up: each second, the
	// shown text closes all but e^(-1/tau) of the gap to what's arrived.
	revealTau = 0.12
	// revealMinCPS keeps a small backlog moving at a readable pace.
	revealMinCPS = 90
	// revealCostRatio is how many times its last render's cost an entry
	// waits before redrawing, so rendering it takes at most about a
	// third of the loop's time.
	revealCostRatio = 3
)

type revealTickMsg struct{ id int }

func revealTick(id int) tea.Cmd {
	return tea.Tick(time.Second/revealFPS, func(time.Time) tea.Msg { return revealTickMsg{id} })
}

// visibleText is the part of an entry's text to draw: all of it, except
// while a streamed entry is still being revealed.
func (e *entry) visibleText() string {
	if !e.paced || e.shown >= len(e.text) {
		return e.text
	}
	return e.text[:e.shown]
}

// revealStep is how many bytes of backlog to reveal after dt seconds.
func revealStep(backlog int, dt float64) int {
	n := math.Ceil(float64(backlog) * (1 - math.Exp(-dt/revealTau)))
	n = math.Max(n, math.Ceil(revealMinCPS*dt))
	return min(backlog, max(1, int(n)))
}

// reveal advances every paced entry by dt seconds and reports whether any
// of them needs a redraw. An entry that's costly to render (a long code
// block can't be cached until its fence closes) redraws only every few
// times its cost, so it can't starve the loop of time for keys and the
// rest of the screen; the frame that catches up always draws.
func (c *chat) reveal(dt float64) bool {
	changed := false
	for _, e := range c.entries {
		if !e.paced || e.shown >= len(e.text) {
			continue
		}
		n := e.shown + revealStep(len(e.text)-e.shown, dt)
		for n < len(e.text) && !utf8.RuneStart(e.text[n]) {
			n++ // never split a character
		}
		e.shown = n
		e.sinceRender += dt
		if n == len(e.text) || e.sinceRender >= revealCostRatio*e.renderCost.Seconds() {
			e.dirty, e.sinceRender = true, 0
			changed = true
		}
	}
	return changed
}

// revealing reports whether any paced entry still has text to show.
func (c *chat) revealing() bool {
	for _, e := range c.entries {
		if e.paced && e.shown < len(e.text) {
			return true
		}
	}
	return false
}

// revealAll shows every paced entry's text as it stands, for a session
// that streamed while out of view: the reveal only paces the focused chat.
func (c *chat) revealAll() {
	for _, e := range c.entries {
		if e.paced && e.shown < len(e.text) {
			e.shown, e.dirty = len(e.text), true
		}
	}
}

// startReveal begins the reveal tick loop if it isn't already running.
func (m *Model) startReveal() tea.Cmd {
	if m.revealTicking || !m.chat.revealing() {
		return nil
	}
	m.revealTicking = true
	m.revealID++
	m.revealLast = time.Now()
	return revealTick(m.revealID)
}

func (m *Model) onRevealTick(msg revealTickMsg) tea.Cmd {
	if msg.id != m.revealID {
		return nil
	}
	now := time.Now()
	dt := now.Sub(m.revealLast).Seconds()
	m.revealLast = now
	if m.chat.reveal(dt) {
		m.refresh()
	}
	if !m.chat.revealing() {
		m.revealTicking = false
		return nil
	}
	return revealTick(m.revealID)
}
