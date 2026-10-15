package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mdp/qrterminal/v3"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

// pairState is the "Pair an iPhone" dialog: the same QR code `claudewitz
// pair` prints, for the app to scan over Tailscale.
type pairState struct {
	loading   bool
	code      daemon.PairCodeResult
	qr        []string
	err       string
	remoteOff bool // pairing failed because remote access is off; enter turns it on
}

type pairCodeMsg struct {
	code daemon.PairCodeResult
	err  error
}

// openPair asks the daemon for a fresh single-use code and shows its QR.
func (m *Model) openPair() tea.Cmd {
	if m.daemon == nil {
		return nil
	}
	m.pair = &pairState{loading: true}
	m.input.Blur()
	return m.fetchPairCode(false)
}

// fetchPairCode gets a code, turning remote access on first when asked.
func (m *Model) fetchPairCode(enableRemote bool) tea.Cmd {
	d := m.daemon
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if enableRemote {
			if err := d.EnableRemote(ctx); err != nil {
				return pairCodeMsg{err: err}
			}
		}
		code, err := d.PairCode(ctx)
		return pairCodeMsg{code: code, err: err}
	}
}

func (m *Model) gotPairCode(msg pairCodeMsg) {
	if m.pair == nil {
		return // closed while it was on its way
	}
	if msg.err != nil {
		e := msg.err.Error()
		m.pair = &pairState{err: e, remoteOff: strings.Contains(e, "remote access is off")}
		return
	}
	m.pair = &pairState{code: msg.code, qr: pairQR(msg.code)}
}

func (m *Model) handlePairKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		if m.pair.remoteOff {
			m.pair = &pairState{loading: true}
			return m.fetchPairCode(true)
		}
		m.pair = nil
	case "esc", "q", "ctrl+c":
		m.pair = nil
	case "r":
		if !m.pair.loading {
			m.pair = &pairState{loading: true}
			return m.fetchPairCode(false)
		}
	}
	if m.pair == nil && !m.chatFocus {
		m.input.Focus()
	}
	return nil
}

// pairQR draws the code's JSON as a QR code in half blocks, as `claudewitz
// pair` does.
func pairQR(code daemon.PairCodeResult) []string {
	payload, _ := json.Marshal(struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		Code string `json:"code"`
	}{code.Host, code.Port, code.Code})
	var buf bytes.Buffer
	qrterminal.GenerateWithConfig(string(payload), qrterminal.Config{
		Level: qrterminal.M, Writer: &buf, HalfBlocks: true, QuietZone: 2,
		BlackChar: qrterminal.BLACK_BLACK, WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar: qrterminal.WHITE_WHITE, BlackWhiteChar: qrterminal.BLACK_WHITE,
	})
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

func (m Model) pairView() string {
	p := m.pair
	th := currentTheme()
	title := lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render("Pair an iPhone")
	var lines []string
	switch {
	case p.loading:
		lines = []string{title, "", mutedStyle.Render("Getting a pairing code…")}
	case p.err != "":
		lines = []string{title, "", errStyle.Render("✗ " + p.err), ""}
		if p.remoteOff {
			lines = append(lines,
				mutedStyle.Render("The app reaches this Mac over Tailscale, through the"),
				mutedStyle.Render("daemon's remote listener."), "",
				panelHints([][2]string{{"enter", "turn remote access on"}, {"esc", "close"}}))
		} else {
			lines = append(lines, panelHints([][2]string{{"r", "try again"}, {"esc", "close"}}))
		}
	default:
		// The modules are pinned to white on black so the code scans whatever the theme.
		qr := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#000000"))
		text := []string{"",
			textStyle.Render("Scan it with the claudewitz app on your iPhone."),
			mutedStyle.Render("Both need Tailscale on, signed in to the same tailnet."),
			mutedStyle.Render(p.code.Host + ":" + strconv.Itoa(p.code.Port) + " · works once · expires " + p.code.ExpiresAt.Local().Format("15:04")),
			"",
			panelHints([][2]string{{"r", "new code"}, {"esc", "close"}})}
		width := lipgloss.Width(strings.Join(text, "\n"))
		block := make([]string, len(p.qr))
		for i, l := range p.qr {
			block[i] = qr.Render(l)
		}
		lines = append([]string{title, "", lipgloss.PlaceHorizontal(width, lipgloss.Center, strings.Join(block, "\n"))}, text...)
	}
	return popupBox.BorderForeground(th.accent).Render(strings.Join(lines, "\n"))
}
