package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// AlwaysOn keeps the claudewitz daemon running at login (a launchd agent on
// macOS), so the iPhone app can reach this Mac. It comes with the daemon
// connection; without it the palette has no such entries.
type AlwaysOn interface {
	Supported() bool
	Installed() bool
	Install(ctx context.Context) (string, error)
	Uninstall(ctx context.Context) (string, error)
	Status(ctx context.Context) string // a few lines for /daemon
}

// WithAlwaysOn offers keeping the daemon running from the palette.
func (m Model) WithAlwaysOn(a AlwaysOn) Model {
	m.alwaysOn = a
	return m
}

// alwaysOnState is the "Keep the daemon running?" dialog.
type alwaysOnState struct {
	cursor int // 0 not now (default), 1 keep it running
}

type alwaysOnDoneMsg struct {
	installed bool // what was asked
	msg       string
	err       error
}

type daemonStatusMsg string

// alwaysOnActions are the palette's entries for it, per the current state.
func (m *Model) alwaysOnActions(add func(group, label, desc, key string, run func(m *Model) tea.Cmd)) {
	if m.alwaysOn == nil || !m.alwaysOn.Supported() {
		return
	}
	if m.alwaysOn.Installed() {
		add("Daemon", "Stop keeping daemon running", "run it on demand again", "", func(m *Model) tea.Cmd {
			a := m.alwaysOn
			return func() tea.Msg {
				msg, err := a.Uninstall(context.Background())
				return alwaysOnDoneMsg{installed: false, msg: msg, err: err}
			}
		})
	} else {
		add("Daemon", "Keep daemon running at login", "so the iPhone app can reach this Mac", "", func(m *Model) tea.Cmd {
			m.openAlwaysOn()
			return nil
		})
	}
	add("Daemon", "Daemon status", "", "/daemon", func(m *Model) tea.Cmd { return m.daemonStatus() })
}

func (m *Model) openAlwaysOn() {
	m.alwaysOnAsk = &alwaysOnState{}
	m.input.Blur()
}

func (m *Model) handleAlwaysOnKey(k tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	switch k.String() {
	case "up", "down", "tab", "k", "j":
		m.alwaysOnAsk.cursor = 1 - m.alwaysOnAsk.cursor
		return nil
	case "1", "n", "esc":
		m.alwaysOnAsk = nil
	case "2", "y":
		m.alwaysOnAsk = nil
		cmd = m.installAlwaysOn()
	case "enter":
		if m.alwaysOnAsk.cursor == 1 {
			cmd = m.installAlwaysOn()
		}
		m.alwaysOnAsk = nil
	default:
		return nil
	}
	if !m.chatFocus {
		m.input.Focus()
	}
	return cmd
}

func (m *Model) installAlwaysOn() tea.Cmd {
	a := m.alwaysOn
	return func() tea.Msg {
		msg, err := a.Install(context.Background())
		return alwaysOnDoneMsg{installed: true, msg: msg, err: err}
	}
}

func (m *Model) alwaysOnDone(msg alwaysOnDoneMsg) tea.Cmd {
	title := "Daemon runs on demand again"
	if msg.installed {
		title = "Daemon kept running"
	}
	if msg.err != nil {
		title = "Couldn't change how the daemon runs"
		return m.notify(toastError, title, msg.err.Error())
	}
	m.chat.info("%s", msg.msg)
	return m.notify(toastSuccess, title, "")
}

// daemonStatus is /daemon.
func (m *Model) daemonStatus() tea.Cmd {
	if m.alwaysOn == nil {
		m.chat.info("Not using the claudewitz daemon: this session runs here and ends when you quit.")
		return nil
	}
	a := m.alwaysOn
	return func() tea.Msg { return daemonStatusMsg(a.Status(context.Background())) }
}

func (m Model) alwaysOnView() string {
	th := currentTheme()
	title := statusKey.Bold(true)
	lines := []string{
		title.Render("Keep the daemon running?"),
		"",
		textStyle.Render("The claudewitz daemon starts when you log in and keeps running,"),
		textStyle.Render("instead of exiting after 10 idle minutes."),
		textStyle.Render("With remote access on, the iPhone app can reach this Mac whenever"),
		textStyle.Render("it's awake."),
		"",
		mutedStyle.Render("Undo it here (Stop keeping daemon running) or with"),
		mutedStyle.Render("claudewitz daemon uninstall."),
		"",
	}
	choices := []struct{ label, keys string }{{"Not now", "n · esc"}, {"Keep it running", "y"}}
	for i, c := range choices {
		pointer := "  "
		style := textStyle
		if i == m.alwaysOnAsk.cursor {
			pointer = statusKey.Render("❯ ")
			style = style.Bold(true)
		}
		lines = append(lines, pointer+mutedStyle.Render(fmt.Sprintf("%d. ", i+1))+style.Render(c.label)+mutedStyle.Render("  "+c.keys))
	}
	return popupBox.BorderForeground(th.accent).Width(min(72, m.termW-4)).Render(strings.Join(lines, "\n"))
}
