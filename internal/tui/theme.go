package tui

import (
	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
)

type ac = lipgloss.AdaptiveColor

type theme struct {
	name   string
	desc   string
	accent ac // brand colour: labels, focus, keys
	second ac // secondary highlight: agents, selections
	muted  ac
	subtle ac // borders, separators
	text   ac
	user   ac
	ok     ac
	err    ac
	warn   ac
	// Tints; derived from ok/err/accent over bg when left empty.
	addBg ac
	delBg ac
	selBg ac
	// bg is the palette's intended background, used for tints and fades.
	// Empty means a neutral dark/light.
	bg ac
	// Syntax-highlighting (chroma) and Markdown (glamour) styles per
	// variant; empty falls back to dracula/github and dark/light.
	chromaDark, chromaLight   string
	glamourDark, glamourLight string

	chroma string // resolved for the current variant
}

var themes = []theme{
	{
		name:   "claude",
		desc:   "Warm orange and violet",
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
		desc:   "Hot pink and indigo, Crush-style",
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
		// https://stephango.com/flexoki — dark uses the 400 accents on
		// black, light the 600 accents on paper.
		name:         "flexoki",
		desc:         "Inky paper tones by Steph Ango",
		accent:       ac{Light: "#BC5215", Dark: "#DA702C"}, // orange
		second:       ac{Light: "#24837B", Dark: "#3AA99F"}, // cyan
		muted:        ac{Light: "#6F6E69", Dark: "#878580"}, // tx-2
		subtle:       ac{Light: "#DAD8CE", Dark: "#343331"}, // ui-2
		text:         ac{Light: "#100F0F", Dark: "#CECDC3"}, // tx
		user:         ac{Light: "#205EA6", Dark: "#4385BE"}, // blue
		ok:           ac{Light: "#66800B", Dark: "#879A39"}, // green
		err:          ac{Light: "#AF3029", Dark: "#D14D41"}, // red
		warn:         ac{Light: "#AD8301", Dark: "#D0A215"}, // yellow
		bg:           ac{Light: "#FFFCF0", Dark: "#100F0F"},
		chromaDark:   "flexoki-dark",
		chromaLight:  "flexoki-light",
		glamourDark:  "dark",
		glamourLight: "light",
	},
	{
		name:         "catppuccin",
		desc:         "Mocha when dark, Latte when light",
		accent:       ac{Light: "#8839EF", Dark: "#CBA6F7"}, // mauve
		second:       ac{Light: "#1E66F5", Dark: "#89B4FA"}, // blue
		muted:        ac{Light: "#6C6F85", Dark: "#A6ADC8"}, // subtext0
		subtle:       ac{Light: "#CCD0DA", Dark: "#45475A"}, // surface
		text:         ac{Light: "#4C4F69", Dark: "#CDD6F4"},
		user:         ac{Light: "#179299", Dark: "#94E2D5"}, // teal
		ok:           ac{Light: "#40A02B", Dark: "#A6E3A1"},
		err:          ac{Light: "#D20F39", Dark: "#F38BA8"},
		warn:         ac{Light: "#DF8E1D", Dark: "#F9E2AF"},
		bg:           ac{Light: "#EFF1F5", Dark: "#1E1E2E"},
		chromaDark:   "catppuccin-mocha",
		chromaLight:  "catppuccin-latte",
		glamourDark:  "dark",
		glamourLight: "light",
	},
	{
		name:         "tokyonight",
		desc:         "Night when dark, Day when light",
		accent:       ac{Light: "#2E7DE9", Dark: "#7AA2F7"}, // blue
		second:       ac{Light: "#9854F1", Dark: "#BB9AF7"}, // magenta
		muted:        ac{Light: "#848CB5", Dark: "#565F89"}, // comment
		subtle:       ac{Light: "#C4C8DA", Dark: "#292E42"},
		text:         ac{Light: "#3760BF", Dark: "#C0CAF5"},
		user:         ac{Light: "#007197", Dark: "#7DCFFF"}, // cyan
		ok:           ac{Light: "#587539", Dark: "#9ECE6A"},
		err:          ac{Light: "#F52A65", Dark: "#F7768E"},
		warn:         ac{Light: "#8C6C3E", Dark: "#E0AF68"},
		bg:           ac{Light: "#E1E2E7", Dark: "#1A1B26"},
		chromaDark:   "tokyonight-night",
		chromaLight:  "tokyonight-day",
		glamourDark:  "tokyo-night",
		glamourLight: "light",
	},
	{
		name:         "gruvbox",
		desc:         "Retro groove, warm and earthy",
		accent:       ac{Light: "#AF3A03", Dark: "#FE8019"}, // orange
		second:       ac{Light: "#B57614", Dark: "#FABD2F"}, // yellow
		muted:        ac{Light: "#928374", Dark: "#928374"},
		subtle:       ac{Light: "#D5C4A1", Dark: "#504945"},
		text:         ac{Light: "#3C3836", Dark: "#EBDBB2"},
		user:         ac{Light: "#076678", Dark: "#83A598"}, // blue
		ok:           ac{Light: "#79740E", Dark: "#B8BB26"},
		err:          ac{Light: "#9D0006", Dark: "#FB4934"},
		warn:         ac{Light: "#B57614", Dark: "#FABD2F"},
		bg:           ac{Light: "#FBF1C7", Dark: "#282828"},
		chromaDark:   "gruvbox",
		chromaLight:  "gruvbox-light",
		glamourDark:  "dark",
		glamourLight: "light",
	},
	{
		name:         "dracula",
		desc:         "Dracula when dark, Alucard when light",
		accent:       ac{Light: "#644AC9", Dark: "#BD93F9"}, // purple
		second:       ac{Light: "#A3144D", Dark: "#FF79C6"}, // pink
		muted:        ac{Light: "#635D97", Dark: "#6272A4"}, // comment
		subtle:       ac{Light: "#CFCFDE", Dark: "#44475A"},
		text:         ac{Light: "#1F1F1F", Dark: "#F8F8F2"},
		user:         ac{Light: "#036A96", Dark: "#8BE9FD"}, // cyan
		ok:           ac{Light: "#14710A", Dark: "#50FA7B"},
		err:          ac{Light: "#CB3A2A", Dark: "#FF5555"},
		warn:         ac{Light: "#846E15", Dark: "#F1FA8C"},
		bg:           ac{Light: "#FFFBEB", Dark: "#282A36"},
		chromaDark:   "dracula",
		chromaLight:  "github",
		glamourDark:  "dracula",
		glamourLight: "light",
	},
	{
		name:         "nord",
		desc:         "Arctic frost blues",
		accent:       ac{Light: "#5E81AC", Dark: "#88C0D0"}, // frost
		second:       ac{Light: "#9A6E94", Dark: "#B48EAD"}, // aurora purple
		muted:        ac{Light: "#4C566A", Dark: "#7B88A1"},
		subtle:       ac{Light: "#D8DEE9", Dark: "#3B4252"},
		text:         ac{Light: "#2E3440", Dark: "#ECEFF4"},
		user:         ac{Light: "#5E81AC", Dark: "#81A1C1"},
		ok:           ac{Light: "#5A7F4C", Dark: "#A3BE8C"},
		err:          ac{Light: "#A5434C", Dark: "#BF616A"},
		warn:         ac{Light: "#9C7A2B", Dark: "#EBCB8B"},
		bg:           ac{Light: "#ECEFF4", Dark: "#2E3440"},
		chromaDark:   "nord",
		chromaLight:  "github",
		glamourDark:  "dark",
		glamourLight: "light",
	},
	{
		name:        "mono",
		desc:        "Greyscale, minimal colour",
		accent:      ac{Light: "#111111", Dark: "#F5F5F5"},
		second:      ac{Light: "#444444", Dark: "#C8C8C8"},
		muted:       ac{Light: "#777777", Dark: "#8A8A8A"},
		subtle:      ac{Light: "#DDDDDD", Dark: "#333333"},
		text:        ac{Light: "#111111", Dark: "#EEEEEE"},
		user:        ac{Light: "#111111", Dark: "#FFFFFF"},
		ok:          ac{Light: "#3B7A3B", Dark: "#9BD49B"},
		err:         ac{Light: "#A33A3A", Dark: "#E49A9A"},
		warn:        ac{Light: "#8A6D1F", Dark: "#D9C27A"},
		addBg:       ac{Light: "#EEF6EE", Dark: "#1C261C"},
		delBg:       ac{Light: "#F8ECEC", Dark: "#2A1D1D"},
		selBg:       ac{Light: "#EAEAEA", Dark: "#2A2A2A"},
		chromaDark:  "bw",
		chromaLight: "bw",
	},
}

var (
	activeTheme = themes[0]
	darkBG      = true
	detectedBG  = true   // what the terminal reported
	appearance  = "auto" // auto, dark or light

	userLabel, assistLabel, mutedStyle, thinkingStyle, toolName, agentStyle lipgloss.Style
	okStyle, errStyle, warnStyle, statusBar, statusKey, textStyle           lipgloss.Style
	inputBox, inputBoxFocused, permBox, userBlock, popupBox, selStyle       lipgloss.Style
	sectionTitle, sidebarStyle, pickerBox, dialogBox                        lipgloss.Style
)

func init() {
	registerFlexoki()
	detectedBG = lipgloss.HasDarkBackground()
	darkBG = detectedBG
	applyTheme(themes[0].name)
}

func currentTheme() theme { return activeTheme }

func findTheme(name string) (theme, bool) {
	for _, t := range themes {
		if t.name == name {
			return t, true
		}
	}
	return theme{}, false
}

// setAppearance forces the dark or light variant ("auto" follows the
// terminal) and re-applies the current theme.
func setAppearance(mode string) {
	switch mode {
	case "dark":
		darkBG = true
	case "light":
		darkBG = false
	default:
		mode = "auto"
		darkBG = detectedBG
	}
	appearance = mode
	// AdaptiveColor resolves through lipgloss's own detection, so tell it.
	lipgloss.SetHasDarkBackground(darkBG)
	applyTheme(activeTheme.name)
}

// tint blends c over the theme background, keeping the given share of c
// for each variant (dark backgrounds need more to stay visible).
func tint(c, bg ac, dark, light float64) ac {
	blend := func(fg, back string, amount float64) string {
		f, err1 := colorful.Hex(fg)
		b, err2 := colorful.Hex(back)
		if err1 != nil || err2 != nil {
			return back
		}
		return b.BlendLab(f, amount).Clamped().Hex()
	}
	return ac{Light: blend(c.Light, bg.Light, light), Dark: blend(c.Dark, bg.Dark, dark)}
}

// applyTheme switches palette; it returns false for an unknown name.
func applyTheme(name string) bool {
	found, ok := findTheme(name)
	if !ok {
		return false
	}
	activeTheme = found
	t := &activeTheme
	bg := t.bg
	if bg.Dark == "" {
		bg = ac{Light: "#FBFAF8", Dark: "#1A1817"}
	}
	if t.addBg.Dark == "" {
		t.addBg = tint(t.ok, bg, 0.30, 0.22)
	}
	if t.delBg.Dark == "" {
		t.delBg = tint(t.err, bg, 0.30, 0.22)
	}
	if t.selBg.Dark == "" {
		t.selBg = tint(t.accent, bg, 0.38, 0.30)
	}
	if darkBG {
		t.chroma = firstNonEmpty(t.chromaDark, "dracula")
	} else {
		t.chroma = firstNonEmpty(t.chromaLight, "github")
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
	dialogBox = s().Border(lipgloss.RoundedBorder()).BorderForeground(t.accent).Padding(1, 2)
	pickerBox = dialogBox
	userBlock = s().Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(t.user).PaddingLeft(1)
	sidebarStyle = s().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(t.subtle).Padding(0, 1)
	return true
}

// glamourStyle is the Markdown style for the active theme and variant.
func glamourStyle() string {
	t := activeTheme
	if darkBG {
		return firstNonEmpty(t.glamourDark, "dark")
	}
	return firstNonEmpty(t.glamourLight, "light")
}

func themeNames() []string {
	out := make([]string, len(themes))
	for i, t := range themes {
		out[i] = t.name
	}
	return out
}
