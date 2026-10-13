package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var notifyModes = []string{"auto", "native", "osc", "bell", "off"}

var notifyModeDesc = map[string]string{
	"auto":   "native locally, terminal escape codes over SSH",
	"native": "the OS notification centre",
	"osc":    "terminal notifications (iTerm2, WezTerm, Ghostty, kitty, foot)",
	"bell":   "just ring the terminal bell",
	"off":    "no desktop notifications",
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
		sendNotification(mode, title, body)
		return nil
	}
}

func sendNotification(mode, title, body string) {
	if mode == "auto" {
		mode = "osc"
		if os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_TTY") == "" && nativeNotifier() != nil {
			mode = "native"
		}
	}
	switch mode {
	case "native":
		if argv := nativeNotifier(); argv != nil {
			args := argv(title, body)
			if exec.Command(args[0], args[1:]...).Run() == nil {
				return
			}
		}
		fallthrough
	case "osc":
		writeTerminal(oscNotification(title, body))
	case "bell":
		writeTerminal("\a")
	}
}

// nativeNotifier returns a command builder for the platform's notifier.
func nativeNotifier() func(title, body string) []string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("osascript"); err == nil {
			return func(title, body string) []string {
				q := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) }
				return []string{"osascript", "-e", fmt.Sprintf(`display notification "%s" with title "%s"`, q(body), q(title))}
			}
		}
	case "linux", "freebsd", "openbsd", "netbsd":
		if _, err := exec.LookPath("notify-send"); err == nil {
			return func(title, body string) []string {
				return []string{"notify-send", "--app-name=claudewitz", title, body}
			}
		}
	}
	return nil
}

// oscNotification picks the escape sequence the terminal understands, with
// a bell as well so terminals without notification support still alert.
func oscNotification(title, body string) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f || r == ';' {
				return ' '
			}
			return r
		}, s)
	}
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
