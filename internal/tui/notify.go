package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/safetext"
)

var notifyModes = []string{"auto", "osc", "bell", "off"}

var notifyModeDesc = map[string]string{
	"auto": "terminal notifications (the default)",
	"osc":  "terminal notifications (iTerm2, WezTerm, Ghostty, kitty, foot)",
	"bell": "just ring the terminal bell",
	"off":  "no desktop notifications",
}

// desktopNotify alerts the user when they've switched away from the
// terminal. It needs the terminal to report focus changes; without them we
// never think we're unfocused, so nothing is sent.
func (m *Model) desktopNotify(title, body string) tea.Cmd {
	mode := firstNonEmpty(m.cfg.Notifications, "auto")
	if mode == "off" || m.focused {
		return nil
	}
	return func() tea.Msg {
		writeTerminal(notificationSeq(mode, title, body))
		return nil
	}
}

// notificationSeq is what to write to the terminal for mode. Anything but
// the bell is a terminal notification, including "native" from configs
// saved before that mode was removed.
func notificationSeq(mode, title, body string) string {
	if mode == "bell" {
		return "\a"
	}
	return oscNotification(title, body)
}

// oscNotification picks the escape sequence the terminal understands, with
// a bell as well so terminals without notification support still alert.
func oscNotification(title, body string) string {
	// ';' separates the sequence's fields, and a ␛ would show literally.
	clean := func(s string) string { return strings.NewReplacer(";", " ", "␛", " ").Replace(safetext.Line(s)) }
	title, body = clean(title), clean(body)
	var seq string
	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "" || strings.Contains(os.Getenv("TERM"), "kitty"):
		seq = "\x1b]99;;" + title + ": " + body + "\x1b\\"
	case strings.HasPrefix(os.Getenv("TERM"), "foot") || strings.Contains(os.Getenv("TERM"), "rxvt"):
		seq = "\x1b]777;notify;" + title + ";" + body + "\x1b\\"
	default: // iTerm2, WezTerm, Ghostty, Windows Terminal and others
		seq = "\x1b]9;" + title + ": " + body + "\x1b\\"
	}
	if os.Getenv("TMUX") != "" {
		// tmux forwards it only with `set -g allow-passthrough on`.
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return seq + "\a"
}

// writeTerminal sends raw bytes to the terminal in one write, so they
// can't interleave with a frame being drawn.
func writeTerminal(s string) {
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		tty.WriteString(s)
		tty.Close()
		return
	}
	os.Stdout.WriteString(s)
}

// setNotifications changes and saves the notification mode; with no mode
// it reports the current one.
func (m *Model) setNotifications(mode string) tea.Cmd {
	cur := firstNonEmpty(m.cfg.Notifications, "auto")
	if mode == "" {
		return m.notify(toastInfo, "Notifications: "+cur, notifyModeDesc[cur])
	}
	if _, ok := notifyModeDesc[mode]; !ok {
		return m.notify(toastError, "Unknown notification mode "+mode, strings.Join(notifyModes, ", "))
	}
	m.cfg.Notifications = mode
	if mode == "auto" {
		m.cfg.Notifications = ""
	}
	if err := m.cfg.Save(); err != nil {
		return m.notify(toastError, "Couldn't save setting", err.Error())
	}
	return m.notify(toastSuccess, "Notifications: "+mode, notifyModeDesc[mode])
}
