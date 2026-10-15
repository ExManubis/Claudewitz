package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

// /pair (and the palette's "Pair an iPhone…") shows the daemon's pairing
// code as a QR code, with where it connects and when it expires.
func TestPairShowsQRCode(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle))
	m := daemonModel(t, d)
	cmd, _ := m.slash("/pair")
	m = run(t, m, cmd)
	if m.pair == nil || len(m.pair.qr) < 15 {
		t.Fatalf("pair %+v", m.pair)
	}
	view := ansi.Strip(m.pairView())
	if !strings.Contains(view, "Pair an iPhone") || !strings.Contains(view, "100.64.0.1:7745") || !strings.Contains(view, "█") {
		t.Fatalf("view:\n%s", view)
	}
	m.handlePairKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.pair != nil {
		t.Fatal("esc didn't close it")
	}
}

// With remote access off, the dialog says so and enter turns it on, then
// shows the code.
func TestPairTurnsRemoteAccessOn(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle))
	d.remoteOff = true
	m := daemonModel(t, d)
	m = run(t, m, m.openPair())
	if m.pair == nil || !m.pair.remoteOff || !strings.Contains(ansi.Strip(m.pairView()), "turn remote access on") {
		t.Fatalf("pair %+v", m.pair)
	}
	m = run(t, m, m.handlePairKey(tea.KeyMsg{Type: tea.KeyEnter}))
	if d.enabled != 1 || m.pair == nil || len(m.pair.qr) == 0 {
		t.Fatalf("enabled %d, pair %+v", d.enabled, m.pair)
	}
}

func TestPaletteOffersPairing(t *testing.T) {
	m := daemonModel(t, newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle)))
	found := false
	for _, a := range m.actions() {
		found = found || a.label == "Pair an iPhone…"
	}
	if !found {
		t.Fatal("no Pair an iPhone… in the palette")
	}
}
