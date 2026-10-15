package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/recap"
	"github.com/exmanubis/claudewitz/internal/sessions"
)

// The recap: a day of work with Claude, tallied from Claude Code's
// transcripts on this machine. ←→ walk back through the last weeks.

// recapDays is how far back the recap goes; the scanner reads a little
// further, for streaks.
const recapDays = 30

type recapState struct {
	date    time.Time // the day shown, at local midnight
	loading bool
	day     recap.Day
	shown   bool // day holds a tally (of some date)
}

type recapMsg struct {
	date time.Time
	day  recap.Day
}

func midnight(t time.Time) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, t.Location())
}

func (m *Model) openRecap() tea.Cmd {
	m.recapView = &recapState{date: midnight(time.Now()), loading: true}
	m.input.Blur()
	return m.loadRecap()
}

func (m *Model) loadRecap() tea.Cmd {
	if m.recaps == nil {
		m.recaps = recap.NewScanner(sessions.Root())
	}
	sc, date := m.recaps, m.recapView.date
	m.recapView.loading = true
	return func() tea.Msg { return recapMsg{date: date, day: sc.Recap(date, time.Local)} }
}

func (m *Model) gotRecap(msg recapMsg) {
	r := m.recapView
	if r == nil || !msg.date.Equal(r.date) {
		return // closed, or moved on to another day
	}
	r.day, r.loading, r.shown = msg.day, false, true
}

func (m *Model) handleRecapKey(k tea.KeyMsg) tea.Cmd {
	r := m.recapView
	today := midnight(time.Now())
	switch k.String() {
	case "left", "h":
		if r.date.After(today.AddDate(0, 0, -recapDays)) {
			r.date = r.date.AddDate(0, 0, -1)
			return m.loadRecap()
		}
	case "right", "l":
		if r.date.Before(today) {
			r.date = r.date.AddDate(0, 0, 1)
			return m.loadRecap()
		}
	case "t":
		if !r.date.Equal(today) {
			r.date = today
			return m.loadRecap()
		}
	case "c":
		if r.shown && !r.loading {
			return tea.Batch(copyToClipboard(recapMarkdown(r.day)), m.notify(toastSuccess, "Copied the recap", "as Markdown, ready to paste"))
		}
	case "esc", "q", "enter", "ctrl+c":
		m.recapView = nil
		if !m.chatFocus && len(m.pending) == 0 {
			m.input.Focus()
		}
	}
	return nil
}

// dayName is "today", "yesterday" or the weekday and date.
func dayName(date, today time.Time) string {
	switch days := int(math.Round(today.Sub(date).Hours() / 24)); days {
	case 0:
		return "Today"
	case 1:
		return "Yesterday"
	}
	return date.Format("Monday 2 January")
}

func (m Model) recapDialog() string {
	r := m.recapView
	th := currentTheme()
	w := dialogWidth(m.termW, 72) - 4 // inside the border and padding
	today := midnight(time.Now())

	title := lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render("Recap") +
		mutedStyle.Render(" · ") + textStyle.Bold(true).Render(dayName(r.date, today))
	if days := int(math.Round(today.Sub(r.date).Hours() / 24)); days >= 1 {
		title += mutedStyle.Render(" · " + r.date.Format("Mon 2 Jan"))
	}
	var nav []string
	if r.date.After(today.AddDate(0, 0, -recapDays)) {
		nav = append(nav, statusKey.Render("‹"))
	} else {
		nav = append(nav, mutedStyle.Render("‹"))
	}
	if r.date.Before(today) {
		nav = append(nav, statusKey.Render("›"))
	} else {
		nav = append(nav, mutedStyle.Render("›"))
	}
	navs := strings.Join(nav, " ")
	head := title + strings.Repeat(" ", max(1, w-lipgloss.Width(title)-lipgloss.Width(navs))) + navs

	hints := [][2]string{{"←→", "day"}, {"c", "copy"}, {"esc", "close"}}
	if !r.date.Equal(today) {
		hints = [][2]string{{"←→", "day"}, {"t", "today"}, {"c", "copy"}, {"esc", "close"}}
	}
	maxBody := max(8, m.termH-1-2-4) // border, title, hints and the gaps
	var body []string
	if !r.shown || (r.loading && r.day.Date != r.date.Format(time.DateOnly)) {
		body = recapLoading(w)
	} else {
		body = recapCard(r.day, w, maxBody, r.date.Equal(today))
	}
	lines := append([]string{head, ""}, body...)
	lines = append(lines, "", panelHints(hints))
	return popupBox.BorderForeground(th.accent).Width(w + 2).Render(strings.Join(lines, "\n"))
}

func recapLoading(w int) []string {
	art := strings.Split(recapArt(false), "\n")
	return joinBeside(art, []string{"", "", mutedStyle.Render("Reading today's transcripts…")}, w)
}

// recapArt is tty for the card: showing a ✓ on a day things shipped.
func recapArt(shipped bool) string {
	const cols, rows = 14, 7
	ps := mascotPose{yaw: 0.35, pitch: -0.15, cursor: true, led: true}
	if shipped {
		ps.face = faceCheck
	}
	fr := renderMascot(cols*2, rows*4, ps)
	return drawMascot(&fr, cols*2, cols, rows, mascotStyles(currentTheme(), nil))
}

// joinBeside puts text to the right of art, art's height or text's,
// whichever is taller.
func joinBeside(art, text []string, w int) []string {
	artW := 0
	for _, a := range art {
		artW = max(artW, lipgloss.Width(a))
	}
	textW := max(10, w-artW-3)
	n := max(len(art), len(text))
	out := make([]string, n)
	for i := 0; i < n; i++ {
		a, t := "", ""
		if i < len(art) {
			a = art[i]
		}
		if i < len(text) {
			t = ansi.Truncate(text[i], textW, "…")
		}
		out[i] = a + strings.Repeat(" ", artW-lipgloss.Width(a)+3) + t
	}
	return out
}

// recapCard lays out a day in at most maxH lines of w cells. Sections are
// dropped from the bottom of the priority list when the terminal is short.
func recapCard(d recap.Day, w, maxH int, isToday bool) []string {
	th := currentTheme()
	bold := textStyle.Bold(true)
	num := func(n int) string { return bold.Render(thousands(n)) }

	if d.Empty() {
		msg := []string{"", "", bold.Render("A quiet day."), mutedStyle.Render("No prompts, no tools. tty approves of rest.")}
		if isToday {
			msg = []string{"", "", bold.Render("Nothing yet today."), mutedStyle.Render("tty is ready when you are.")}
		}
		out := joinBeside(strings.Split(recapArt(false), "\n"), msg, w)
		if len(out)+3 <= maxH && len(d.Week) > 0 {
			out = append(append(out, ""), recapWeek(d)...)
		}
		return out
	}

	// Hero: tty and the headline numbers.
	text := []string{
		"",
		textStyle.Render("Claude worked ") + lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render(hoursMinutes(d.Working)),
		textStyle.Render("on ") + num(d.Prompts) + textStyle.Render(plural(d.Prompts, " prompt", " prompts")) +
			textStyle.Render(" in ") + num(d.Sessions) + textStyle.Render(plural(d.Sessions, " session", " sessions")),
	}
	if d.First != nil && d.Last != nil {
		span := d.First.Format("15:04") + " → " + d.Last.Format("15:04")
		if isToday {
			span = "since " + d.First.Format("15:04")
		}
		text = append(text, mutedStyle.Render(span+" · "+plural(len(d.Projects), "1 folder", fmt.Sprintf("%d folders", len(d.Projects)))))
	}
	text = append(text, "", mutedStyle.Render(humanTokens(d.OutputTokens)+" tokens written · "+thousands(d.ToolCalls)+" tool calls"))
	if d.Streak >= 2 {
		text = append(text, warnStyle.Render(fmt.Sprintf("✦ %d-day streak", d.Streak)))
	}
	shipped := d.Commits+d.PRsMerged+d.Releases > 0
	hero := joinBeside(strings.Split(recapArt(shipped), "\n"), text, w)

	type section struct {
		lines []string
		rank  int // lower survives longer
	}
	sections := []section{{hero, 0}}
	if chips := recapChips(d, w); len(chips) > 0 {
		sections = append(sections, section{chips, 1})
	}
	sections = append(sections, section{recapHours(d, w), 3})
	if len(d.Projects) > 1 {
		sections = append(sections, section{recapProjects(d, w), 4})
	}
	if hl := recapHighlights(d, w); len(hl) > 0 {
		sections = append(sections, section{hl, 5})
	}
	if d.Longest != nil && d.Longest.Seconds >= 60 {
		sections = append(sections, section{recapLongest(d, w), 6})
	}
	if len(d.Badges) > 0 {
		sections = append(sections, section{recapBadges(d, w), 2})
	}
	sections = append(sections, section{recapWeek(d), 7})

	height := func() int {
		n := len(sections) - 1 // gaps
		for _, s := range sections {
			n += len(s.lines)
		}
		return n
	}
	for height() > maxH && len(sections) > 1 {
		worst := 0
		for i, s := range sections {
			if s.rank > sections[worst].rank {
				worst = i
			}
		}
		sections = append(sections[:worst], sections[worst+1:]...)
	}
	var out []string
	for i, s := range sections {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, s.lines...)
	}
	return out
}

// recapChips are the day's outcomes, wrapped to the width.
func recapChips(d recap.Day, w int) []string {
	th := currentTheme()
	bold := textStyle.Bold(true)
	chip := func(icon string, col ac, n int, label string) string {
		return lipgloss.NewStyle().Foreground(col).Render(icon) + " " + bold.Render(thousands(n)) + " " + mutedStyle.Render(label)
	}
	var chips []string
	if d.Commits > 0 {
		chips = append(chips, chip("●", th.ok, d.Commits, plural(d.Commits, "commit", "commits")))
	}
	if d.PRsOpened > 0 {
		chips = append(chips, chip("⇡", th.accent, d.PRsOpened, plural(d.PRsOpened, "PR opened", "PRs opened")))
	}
	if d.PRsMerged > 0 {
		chips = append(chips, chip("✓", th.ok, d.PRsMerged, "merged"))
	}
	if d.Releases > 0 {
		chips = append(chips, chip("⚑", th.warn, d.Releases, plural(d.Releases, "release", "releases")))
	}
	if d.TestsPassed > 0 {
		chips = append(chips, chip("✔", th.ok, d.TestsPassed, plural(d.TestsPassed, "green test run", "green test runs")))
	}
	if d.TestsFailed > 0 {
		chips = append(chips, chip("✗", th.err, d.TestsFailed, "red"))
	}
	if d.FilesEdited > 0 {
		chips = append(chips, chip("✎", th.second, d.FilesEdited, plural(d.FilesEdited, "file", "files"))+" "+
			okStyle.Render("+"+thousands(d.LinesAdded))+" "+errStyle.Render("−"+thousands(d.LinesRemoved)))
	}
	var lines []string
	cur := ""
	for _, c := range chips {
		if cur != "" && lipgloss.Width(cur)+3+lipgloss.Width(c) > w {
			lines = append(lines, cur)
			cur = ""
		}
		if cur != "" {
			cur += "   "
		}
		cur += c
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

var eighths = []rune(" ▁▂▃▄▅▆▇█")

// recapHours charts when Claude worked: a column per hour, three rows of
// eighth blocks tall.
func recapHours(d recap.Day, w int) []string {
	th := currentTheme()
	const rows = 3
	cw := 2
	if w < 24*2+2 {
		cw = 1
	}
	peak := 0
	for _, s := range d.Hours {
		peak = max(peak, s)
	}
	cols := gradient(24, th.second, th.accent)
	out := make([]string, rows+1)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for h, s := range d.Hours {
			level := 0
			if peak > 0 && s > 0 {
				level = max(1, int(math.Round(float64(s)/float64(peak)*rows*8)))
			}
			fill := min(8, max(0, level-(rows-1-r)*8))
			cell := string(eighths[fill])
			if cw == 2 {
				cell += string(eighths[fill])
			}
			if fill == 0 {
				b.WriteString(strings.Repeat(" ", cw))
				continue
			}
			b.WriteString(lipgloss.NewStyle().Foreground(cols[h]).Render(cell))
		}
		out[r] = b.String()
	}
	// Labels every sixth hour.
	labels := strings.Repeat(" ", 24*cw)
	for _, h := range []int{0, 6, 12, 18} {
		l := fmt.Sprintf("%02d", h)
		labels = labels[:h*cw] + l + labels[h*cw+len(l):]
	}
	out[rows] = mutedStyle.Render(labels)
	if peakHour := busiest(d.Hours); peakHour >= 0 && w-24*cw >= 16 {
		out[0] += mutedStyle.Render(fmt.Sprintf("   busiest %02d:00", peakHour))
	}
	return out
}

func busiest(hours [24]int) int {
	best := -1
	for h, s := range hours {
		if s > 0 && (best < 0 || s > hours[best]) {
			best = h
		}
	}
	return best
}

// recapProjects is a bar per folder, by Claude's time in it.
func recapProjects(d recap.Day, w int) []string {
	th := currentTheme()
	shown := d.Projects
	if len(shown) > 4 {
		shown = shown[:4]
	}
	nameW := 0
	for _, p := range shown {
		nameW = max(nameW, min(20, lipgloss.Width(p.Name)))
	}
	barW := max(6, w-nameW-12)
	peak := max(1, shown[0].Working)
	out := []string{}
	for _, p := range shown {
		name := ansi.Truncate(p.Name, nameW, "…")
		name += strings.Repeat(" ", nameW-lipgloss.Width(name))
		out = append(out, textStyle.Render(name)+"  "+
			lipgloss.NewStyle().Foreground(th.second).Render(bar(float64(p.Working)/float64(peak), barW))+
			strings.Repeat(" ", barW-barWidth(float64(p.Working)/float64(peak), barW))+"  "+mutedStyle.Render(hoursMinutes(p.Working)))
	}
	if more := len(d.Projects) - len(shown); more > 0 {
		out = append(out, mutedStyle.Render(fmt.Sprintf("and %d more", more)))
	}
	return out
}

var partials = []rune(" ▏▎▍▌▋▊▉")

// bar is frac of width cells, in eighths.
func bar(frac float64, width int) string {
	e := int(math.Round(frac * float64(width) * 8))
	e = max(1, e)
	return strings.Repeat("█", e/8) + strings.TrimSpace(string(partials[e%8]))
}

func barWidth(frac float64, width int) int { return lipgloss.Width(bar(frac, width)) }

// recapHighlights are the day's last few ships: releases and merges first.
func recapHighlights(d recap.Day, w int) []string {
	th := currentTheme()
	rank := map[string]int{recap.Release: 0, recap.Merge: 1, recap.PR: 2, recap.Commit: 3}
	var picked []recap.Milestone
	for want := 0; want <= 3 && len(picked) < 3; want++ {
		for i := len(d.Moments) - 1; i >= 0 && len(picked) < 3; i-- {
			if m := d.Moments[i]; rank[m.Kind] == want {
				picked = append(picked, m)
			}
		}
	}
	out := []string{}
	for _, m := range picked {
		icon, col, text := "●", th.ok, m.Text
		switch m.Kind {
		case recap.Release:
			icon, col, text = "⚑", th.warn, "released "+m.Text
		case recap.Merge:
			icon, text = "✓", "merged "+firstNonEmpty(m.Text, "a pull request")
		case recap.PR:
			icon, col, text = "⇡", th.accent, "opened "+shortPR(m.Text)
		}
		when := mutedStyle.Render(" " + m.At.Format("15:04"))
		line := lipgloss.NewStyle().Foreground(col).Render(icon) + " " + textStyle.Render(ansi.Truncate(firstNonEmpty(text, "a commit"), w-10, "…")) + when
		out = append(out, line)
	}
	return out
}

// shortPR turns a pull request link into owner/repo#n.
func shortPR(url string) string {
	s := strings.TrimPrefix(url, "https://github.com/")
	if i := strings.Index(s, "/pull/"); i >= 0 {
		return s[:i] + "#" + s[i+len("/pull/"):]
	}
	return url
}

func recapLongest(d recap.Day, w int) []string {
	head := mutedStyle.Render("Longest stretch ") + textStyle.Bold(true).Render(hoursMinutes(d.Longest.Seconds)) + mutedStyle.Render(" on")
	quote := "“" + d.Longest.Prompt + "”"
	return []string{head, textStyle.Italic(true).Render(ansi.Truncate(quote, w, "…”"))}
}

var badgeIcons = map[string]string{
	"release": "⚑", "shipper": "⇡", "green": "✔", "fixer": "✚", "marathon": "⧗", "juggler": "◎",
	"tidy": "✂", "polyglot": "✱", "early": "☀", "owl": "☾", "streak": "✦",
}

func recapBadges(d recap.Day, w int) []string {
	out := []string{}
	for i, b := range d.Badges {
		if i == 4 {
			var rest []string
			for _, x := range d.Badges[4:] {
				rest = append(rest, firstNonEmpty(badgeIcons[x.ID], "★")+" "+x.Name)
			}
			out = append(out, warnStyle.Render(ansi.Truncate(strings.Join(rest, "   "), w, "…")))
			break
		}
		out = append(out, ansi.Truncate(warnStyle.Render(firstNonEmpty(badgeIcons[b.ID], "★"))+" "+textStyle.Bold(true).Render(b.Name)+mutedStyle.Render(" · "+b.Detail), w, "…"))
	}
	return out
}

// recapWeek is the seven days ending on the one shown: a bar each, by
// prompts.
func recapWeek(d recap.Day) []string {
	th := currentTheme()
	peak := 1
	for _, x := range d.Week {
		peak = max(peak, x.Prompts)
	}
	var bars, names []string
	for i, x := range d.Week {
		level := 0
		if x.Prompts > 0 {
			level = max(1, int(math.Round(float64(x.Prompts)/float64(peak)*8)))
		}
		col := th.second
		if i == len(d.Week)-1 {
			col = th.accent
		}
		cell := string(eighths[level]) + string(eighths[level])
		if level == 0 {
			cell = "··"
			col = th.subtle
		}
		bars = append(bars, lipgloss.NewStyle().Foreground(col).Render(cell))
		day := "  "
		if t, err := time.Parse(time.DateOnly, x.Date); err == nil {
			day = t.Format("Mon")[:2]
		}
		if i == len(d.Week)-1 {
			names = append(names, statusKey.Render(day))
		} else {
			names = append(names, mutedStyle.Render(day))
		}
	}
	return []string{strings.Join(bars, " "), strings.Join(names, " ")}
}

// recapMarkdown is the day as a few lines to paste anywhere.
func recapMarkdown(d recap.Day) string {
	date := d.Date
	if t, err := time.Parse(time.DateOnly, d.Date); err == nil {
		date = t.Format("Monday 2 January")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**My day with Claude · %s**\n\n", date)
	if d.Empty() {
		b.WriteString("- A quiet day.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "- Claude worked %s on %s in %s\n", hoursMinutes(d.Working),
		plural(d.Prompts, "1 prompt", thousands(d.Prompts)+" prompts"), plural(d.Sessions, "1 session", fmt.Sprintf("%d sessions", d.Sessions)))
	var ships []string
	add := func(n int, one, many string) {
		if n > 0 {
			ships = append(ships, plural(n, "1 "+one, thousands(n)+" "+many))
		}
	}
	add(d.Commits, "commit", "commits")
	add(d.PRsOpened, "pull request opened", "pull requests opened")
	add(d.PRsMerged, "merged", "merged")
	add(d.Releases, "release", "releases")
	if len(ships) > 0 {
		fmt.Fprintf(&b, "- %s\n", strings.Join(ships, " · "))
	}
	if d.TestsPassed+d.TestsFailed > 0 {
		fmt.Fprintf(&b, "- %s green, %s red\n", plural(d.TestsPassed, "1 test run", thousands(d.TestsPassed)+" test runs"), thousands(d.TestsFailed))
	}
	if d.FilesEdited > 0 {
		fmt.Fprintf(&b, "- %s edited (+%s −%s)\n", plural(d.FilesEdited, "1 file", thousands(d.FilesEdited)+" files"), thousands(d.LinesAdded), thousands(d.LinesRemoved))
	}
	if len(d.Badges) > 0 {
		var names []string
		for _, x := range d.Badges {
			names = append(names, x.Name)
		}
		fmt.Fprintf(&b, "- Badges: %s\n", strings.Join(names, ", "))
	}
	if d.Streak >= 2 {
		fmt.Fprintf(&b, "- %d-day streak\n", d.Streak)
	}
	b.WriteString("\n_via claudewitz_\n")
	return b.String()
}

// hoursMinutes is "7h 26m", "6h", "26m" or "45s".
func hoursMinutes(sec int) string {
	switch {
	case sec >= 3600 && sec%3600 < 60:
		return fmt.Sprintf("%dh", sec/3600)
	case sec >= 3600:
		return fmt.Sprintf("%dh %dm", sec/3600, sec%3600/60)
	case sec >= 60:
		return fmt.Sprintf("%dm", sec/60)
	}
	return fmt.Sprintf("%ds", sec)
}

// thousands is n with comma separators.
func thousands(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// plural picks one or many by n; many may hold the number already.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// RecapCard renders a day's recap the way the TUI's dialog shows it, for
// `claudewitz recap` to print. width is the terminal's.
func RecapCard(d recap.Day, cfg config.Config, width int) string {
	setAppearance(cfg.Appearance)
	if !applyTheme(cfg.Theme) {
		applyTheme(themes[0].name)
	}
	th := currentTheme()
	w := max(40, min(width, 76)) - 4
	date := d.Date
	if t, err := time.Parse(time.DateOnly, d.Date); err == nil {
		date = t.Format("Monday 2 January")
	}
	head := lipgloss.NewStyle().Foreground(th.accent).Bold(true).Render("Recap") + mutedStyle.Render(" · ") + textStyle.Bold(true).Render(date)
	isToday := d.Date == time.Now().Format(time.DateOnly)
	lines := append([]string{head, ""}, recapCard(d, w, 1000, isToday)...)
	return popupBox.BorderForeground(th.accent).Width(w + 2).Render(strings.Join(lines, "\n"))
}

// RecapMarkdown is a day's recap as Markdown.
func RecapMarkdown(d recap.Day) string { return recapMarkdown(d) }
