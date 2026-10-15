package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// iconSet is every symbolic glyph that has a Nerd Font version. Layout
// characters (borders, bars, ellipses, key hints) stay plain Unicode.
type iconSet struct {
	branch, image, search                                             string
	modeAuto, modeEdits, modePlan, modeBypass                         string
	queued, todos, agents, reset                                      string
	ahead, behind                                                     string
	conflict, staged, changed, clean                                  string
	toastSuccess, toastWarn, toastError                               string
	sessWaiting, sessError, sessWorking, sessDone, sessIdle, worktree string
}

// nerdIcons are Nerd Font codicons (plus Octicons arrows); they need a
// patched font and render as boxes without one.
var nerdIcons = iconSet{
	branch:       "\uec6f", // nf-cod-git_branch
	image:        "\ueaea", // nf-cod-file_media
	search:       "\uea6d", // nf-cod-search
	modeAuto:     "\uec10", // nf-cod-sparkle
	modeEdits:    "\uea73", // nf-cod-edit
	modePlan:     "\uead1", // nf-cod-debug_pause
	modeBypass:   "\uea6c", // nf-cod-warning
	queued:       "\ueb09", // nf-cod-inbox
	todos:        "\ueab3", // nf-cod-checklist
	agents:       "\ueb08", // nf-cod-hubot
	reset:        "\uea82", // nf-cod-history
	ahead:        "\uf431", // nf-oct-arrow_up
	behind:       "\uf433", // nf-oct-arrow_down
	conflict:     "\ueafe", // nf-cod-git_merge
	staged:       "\ueadc", // nf-cod-diff_added
	changed:      "\ueade", // nf-cod-diff_modified
	clean:        "\ueba4", // nf-cod-pass
	toastSuccess: "\ueba4", // nf-cod-pass
	toastWarn:    "\uea6c", // nf-cod-warning
	toastError:   "\uea87", // nf-cod-error
	sessWaiting:  "◌",
	sessError:    "✗",
	sessWorking:  "▶",
	sessDone:     "✓",
	sessIdle:     "○",
	worktree:     "⎇",
}

// unicodeIcons work in any font.
var unicodeIcons = iconSet{
	branch:       "⎇",
	image:        "▣",
	search:       "⌕",
	modeAuto:     "✦",
	modeEdits:    "⏵⏵",
	modePlan:     "⏸",
	modeBypass:   "⚠",
	queued:       "▲",
	todos:        "☐",
	agents:       "◐",
	reset:        "↻",
	ahead:        "↑",
	behind:       "↓",
	conflict:     "✗",
	staged:       "●",
	changed:      "✚",
	clean:        "✓",
	toastSuccess: "✓",
	toastWarn:    "▲",
	toastError:   "✗",
	sessWaiting:  "◌",
	sessError:    "✗",
	sessWorking:  "▶",
	sessDone:     "✓",
	sessIdle:     "○",
	worktree:     "⎇",
}

var iconModes = []string{"nerd", "unicode"}

var iconModeDesc = map[string]string{
	"nerd":    "Nerd Font icons (the default)",
	"unicode": "plain Unicode symbols, for fonts without Nerd Font glyphs",
}

// icons is the active set, like the theme a package-level setting.
var icons = nerdIcons

func applyIcons(mode string) {
	icons = nerdIcons
	if mode == "unicode" {
		icons = unicodeIcons
	}
}

// setIcons changes and saves the icon set; with no mode it reports the
// current one.
func (m *Model) setIcons(mode string) tea.Cmd {
	cur := firstNonEmpty(m.cfg.Icons, "nerd")
	if mode == "" {
		return m.notify(toastInfo, "Icons: "+cur, iconModeDesc[cur])
	}
	if _, ok := iconModeDesc[mode]; !ok {
		return m.notify(toastError, "Unknown icon set "+mode, strings.Join(iconModes, ", "))
	}
	applyIcons(mode)
	m.restyle()
	m.cfg.Icons = mode
	if mode == "nerd" {
		m.cfg.Icons = ""
	}
	if err := m.cfg.Save(); err != nil {
		return m.notify(toastError, "Couldn't save setting", err.Error())
	}
	return m.notify(toastSuccess, "Icons: "+mode, iconModeDesc[mode])
}
