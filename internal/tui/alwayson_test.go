package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/daemon"
)

type fakeAlwaysOn struct {
	installed   bool
	installs    int
	uninstalls  int
	unsupported bool
}

func (a *fakeAlwaysOn) Supported() bool { return !a.unsupported }
func (a *fakeAlwaysOn) Installed() bool { return a.installed }
func (a *fakeAlwaysOn) Install(context.Context) (string, error) {
	a.installs++
	a.installed = true
	return "The daemon now starts at login and keeps running.", nil
}
func (a *fakeAlwaysOn) Uninstall(context.Context) (string, error) {
	a.uninstalls++
	a.installed = false
	return "The daemon no longer starts at login; it runs on demand again.", nil
}
func (a *fakeAlwaysOn) Status(context.Context) string { return "mode     always on (starts at login)" }

func paletteLabels(m *Model) []string {
	var out []string
	for _, a := range m.actions() {
		if a.group == "Daemon" {
			out = append(out, a.label)
		}
	}
	return out
}

func runPaletteAction(t *testing.T, m Model, label string) Model {
	t.Helper()
	for _, a := range m.actions() {
		if a.label == label {
			return run(t, m, a.run(&m))
		}
	}
	t.Fatalf("no palette entry %q", label)
	return m
}

func TestAlwaysOnPaletteFollowsState(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle))
	a := &fakeAlwaysOn{}
	m := daemonModel(t, d).WithAlwaysOn(a)
	if got := strings.Join(paletteLabels(&m), ","); got != "Keep daemon running at login,Daemon status" {
		t.Fatalf("not installed: %s", got)
	}
	a.installed = true
	if got := strings.Join(paletteLabels(&m), ","); got != "Stop keeping daemon running,Daemon status" {
		t.Fatalf("installed: %s", got)
	}
	m = runPaletteAction(t, m, "Stop keeping daemon running")
	if a.uninstalls != 1 || a.installed {
		t.Fatalf("uninstall: %+v", a)
	}

	a.unsupported = true
	if got := paletteLabels(&m); len(got) != 0 {
		t.Fatalf("unsupported: %v", got)
	}
	m.alwaysOn = nil
	if got := paletteLabels(&m); len(got) != 0 {
		t.Fatalf("without the daemon: %v", got)
	}
}

func TestAlwaysOnDialogDefaultsToNotNow(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle))
	a := &fakeAlwaysOn{}
	m := daemonModel(t, d).WithAlwaysOn(a)
	m = runPaletteAction(t, m, "Keep daemon running at login")
	if m.alwaysOnAsk == nil {
		t.Fatal("no dialog")
	}
	view := ansi.Strip(m.alwaysOnView())
	for _, want := range []string{"Keep the daemon running?", "starts when you log in", "iPhone app", "claudewitz daemon uninstall", "❯ 1. Not now"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dialog lacks %q:\n%s", want, view)
		}
	}
	if !m.modalOpen() {
		t.Fatal("dialog isn't modal")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(t, next.(Model), cmd)
	if m.alwaysOnAsk != nil || a.installs != 0 {
		t.Fatalf("enter on the default installed it: dialog %v installs %d", m.alwaysOnAsk, a.installs)
	}

	// Down, enter: installed, and said so.
	m = runPaletteAction(t, m, "Keep daemon running at login")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next, cmd = next.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = run(t, next.(Model), cmd)
	if a.installs != 1 || m.alwaysOnAsk != nil {
		t.Fatalf("installs %d, dialog %v", a.installs, m.alwaysOnAsk)
	}
	if !strings.Contains(chatText(m), "starts at login and keeps running") {
		t.Fatal("install not reported in the chat")
	}
}

func TestDaemonSlashCommand(t *testing.T) {
	d := newFakeDaemon(sum("s_1", "/x/a", daemon.StateIdle))
	m := daemonModel(t, d).WithAlwaysOn(&fakeAlwaysOn{installed: true})
	cmd, handled := m.slash("/daemon")
	if !handled {
		t.Fatal("/daemon not handled")
	}
	m = run(t, m, cmd)
	if !strings.Contains(chatText(m), "always on (starts at login)") {
		t.Fatal("/daemon didn't show the status")
	}
}

func chatText(m Model) string {
	var b strings.Builder
	for _, e := range m.chat.entries {
		b.WriteString(e.text + "\n")
	}
	return b.String()
}
