package tui

import "github.com/charmbracelet/lipgloss"

type ac = lipgloss.AdaptiveColor

type theme struct {
	name   string
	accent ac // brand colour: labels, focus, keys
	second ac // secondary highlight: agents, selections
	muted  ac
	subtle ac // borders, separators
	text   ac
	user   ac
	ok     ac
	err    ac
	warn   ac
	addBg  ac
	delBg  ac
	selBg  ac
	chroma string
}

var themes = []theme{
	{
		name:   "claude",
		accent: ac{Light: "#C15F3C", Dark: "#DA7756"},
		second: ac{Light: "#8A5CF6", Dark: "#B497FF"},
		muted:  ac{Light: "#7A7570", Dark: "#8C8680"},
		subtle: ac{Light: "#D8D2CC", Dark: "#3A3633"},
		text:   ac{Light: "#2B2725", Dark: "#E8E2DC"},
		user:   ac{Light: "#2B6CB0", Dark: "#7AB8FF"},
		ok:     ac{Light: "#2F855A", Dark: "#7BD88F"},
		err:    ac{Light: "#C53030", Dark: "#FF7B72"},
		warn:   ac{Light: "#B7791F", Dark: "#F2CC60"},
		addBg:  ac{Light: "#E6FFEC", Dark: "#16301E"},
		delBg:  ac{Light: "#FFEBE9", Dark: "#3A1A1C"},
		selBg:  ac{Light: "#F3E6DF", Dark: "#3A2A23"},
	},
	{
		name:   "charm",
		accent: ac{Light: "#D6409F", Dark: "#FF5FD2"},
		second: ac{Light: "#6B50FF", Dark: "#8E7CFF"},
		muted:  ac{Light: "#7D7A8C", Dark: "#8A87A0"},
		subtle: ac{Light: "#DDD9EA", Dark: "#34314A"},
		text:   ac{Light: "#24212F", Dark: "#EAE7F7"},
		user:   ac{Light: "#0E8F86", Dark: "#5EE3D5"},
		ok:     ac{Light: "#16A34A", Dark: "#6EE7A0"},
		err:    ac{Light: "#DC2626", Dark: "#FF6B8B"},
		warn:   ac{Light: "#CA8A04", Dark: "#FFD166"},
		addBg:  ac{Light: "#E7FBEF", Dark: "#173026"},
		delBg:  ac{Light: "#FDEBEF", Dark: "#3A1A27"},
		selBg:  ac{Light: "#F1E8FF", Dark: "#2E2650"},
	},
	{
		name:   "mono",
		accent: ac{Light: "#111111", Dark: "#F5F5F5"},
		second: ac{Light: "#444444", Dark: "#C8C8C8"},
		muted:  ac{Light: "#777777", Dark: "#8A8A8A"},
		subtle: ac{Light: "#DDDDDD", Dark: "#333333"},
		text:   ac{Light: "#111111", Dark: "#EEEEEE"},
		user:   ac{Light: "#111111", Dark: "#FFFFFF"},
		ok:     ac{Light: "#3B7A3B", Dark: "#9BD49B"},
		err:    ac{Light: "#A33A3A", Dark: "#E49A9A"},
		warn:   ac{Light: "#8A6D1F", Dark: "#D9C27A"},
		addBg:  ac{Light: "#EEF6EE", Dark: "#1C261C"},
		delBg:  ac{Light: "#F8ECEC", Dark: "#2A1D1D"},
		selBg:  ac{Light: "#EAEAEA", Dark: "#2A2A2A"},
	},
}

var (
	activeTheme = themes[0]
	darkBG      = true

	userLabel, assistLabel, mutedStyle, thinkingStyle, toolName, agentStyle lipgloss.Style
	okStyle, errStyle, warnStyle, statusBar, statusKey, textStyle           lipgloss.Style
	inputBox, inputBoxFocused, permBox, userBlock, popupBox, selStyle       lipgloss.Style
	sectionTitle, sidebarStyle, pickerBox                                   lipgloss.Style
)

func init() {
	darkBG = lipgloss.HasDarkBackground()
	applyTheme(themes[0].name)
}

func currentTheme() theme { return activeTheme }

// applyTheme switches palette; it returns false for an unknown name.
func applyTheme(name string) bool {
	found := false
	for _, t := range themes {
		if t.name == name {
			activeTheme, found = t, true
		}
	}
	if !found {
		return false
	}
	t := &activeTheme
	if darkBG {
		t.chroma = "dracula"
		if t.name == "mono" {
			t.chroma = "bw"
		}
	} else {
		t.chroma = "github"
		if t.name == "mono" {
			t.chroma = "bw"
		}
	}

	s := lipgloss.NewStyle
	userLabel = s().Foreground(t.user).Bold(true)
	assistLabel = s().Foreground(t.accent).Bold(true)
	mutedStyle = s().Foreground(t.muted)
	textStyle = s().Foreground(t.text)
	thinkingStyle = s().Foreground(t.muted).Italic(true)
	toolName = s().Foreground(t.accent).Bold(true)
	agentStyle = s().Foreground(t.second).Bold(true)
	okStyle = s().Foreground(t.ok)
	errStyle = s().Foreground(t.err)
	warnStyle = s().Foreground(t.warn)
	statusBar = s().Foreground(t.muted).Padding(0, 1)
	statusKey = s().Foreground(t.accent).Bold(true)
	selStyle = s().Background(t.selBg).Foreground(t.text).Bold(true)
	sectionTitle = s().Foreground(t.accent).Bold(true).MarginTop(1)

	inputBox = s().Border(lipgloss.RoundedBorder()).BorderForeground(t.subtle).Padding(0, 1)
	inputBoxFocused = inputBox.BorderForeground(t.accent)
	permBox = s().Border(lipgloss.RoundedBorder()).BorderForeground(t.warn).Padding(0, 1)
	popupBox = s().Border(lipgloss.RoundedBorder()).BorderForeground(t.second).Padding(0, 1)
	pickerBox = s().Border(lipgloss.RoundedBorder()).BorderForeground(t.accent).Padding(0, 2)
	userBlock = s().Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(t.user).PaddingLeft(1)
	sidebarStyle = s().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(t.subtle).Padding(0, 1)
	return true
}

func themeNames() []string {
	out := make([]string, len(themes))
	for i, t := range themes {
		out[i] = t.name
	}
	return out
}
