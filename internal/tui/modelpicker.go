package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
)

var defaultEffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

var effortDesc = map[string]string{
	"":       "the model's own default",
	"low":    "fastest, least thinking",
	"medium": "balanced",
	"high":   "thinks harder",
	"xhigh":  "extended reasoning",
	"max":    "deepest reasoning, slowest",
}

// modelPicker is the ctrl+t dialog for choosing a model and effort level.
type modelPicker struct {
	models []claude.ModelInfo
	sel    int
	// effort chosen per model row, so moving between rows keeps choices.
	effort  map[string]string
	current string // model value in use
	curEff  string // effort in use
}

func (m *Model) openModelPicker() {
	models := m.availableModels()
	p := &modelPicker{models: models, effort: map[string]string{}, current: m.modelValue(), curEff: m.opts.Effort}
	for i, mi := range models {
		if mi.Value == p.current {
			p.sel = i
		}
		p.effort[mi.Value] = m.opts.Effort
	}
	m.modelPick = p
	m.input.Blur()
}

func (m *Model) closeModelPicker() {
	m.modelPick = nil
	if len(m.pending) == 0 && !m.chatFocus {
		m.input.Focus()
	}
}

// availableModels comes from the CLI's initialize response, with a fallback
// for the moment before it arrives.
func (m *Model) availableModels() []claude.ModelInfo {
	if m.initResp != nil && len(m.initResp.Models) > 0 {
		return m.initResp.Models
	}
	var out []claude.ModelInfo
	for _, v := range []string{"opus", "sonnet", "haiku"} {
		out = append(out, claude.ModelInfo{Value: v, DisplayName: strings.ToUpper(v[:1]) + v[1:], SupportsEffort: true})
	}
	return out
}

// modelValue is the picker value matching the model in use.
func (m *Model) modelValue() string {
	if m.opts.Model != "" {
		return m.opts.Model
	}
	return "default"
}

func (p *modelPicker) levels() []string {
	mi := p.models[p.sel]
	if !mi.SupportsEffort {
		return nil
	}
	lv := mi.SupportedEffortLevels
	if len(lv) == 0 {
		lv = defaultEffortLevels
	}
	// "Default" is offered only while no explicit level is in force: the CLI
	// has no way to clear an effort setting once applied.
	if p.curEff == "" {
		return append([]string{""}, lv...)
	}
	return lv
}

func (p *modelPicker) shiftEffort(d int) {
	lv := p.levels()
	if len(lv) == 0 {
		return
	}
	v := p.models[p.sel].Value
	cur := 0
	for i, l := range lv {
		if l == p.effort[v] {
			cur = i
		}
	}
	p.effort[v] = lv[max(0, min(len(lv)-1, cur+d))]
}

func (m *Model) handleModelPickerKey(k tea.KeyMsg) tea.Cmd {
	p := m.modelPick
	switch k.String() {
	case "esc", "ctrl+c", "ctrl+t", "q":
		m.closeModelPicker()
	case "up", "k", "ctrl+p":
		p.sel = (p.sel - 1 + len(p.models)) % len(p.models)
	case "down", "j", "ctrl+n", "tab":
		p.sel = (p.sel + 1) % len(p.models)
	case "left", "h", "-":
		p.shiftEffort(-1)
	case "right", "l", "+", "=":
		p.shiftEffort(1)
	case "enter":
		mi := p.models[p.sel]
		eff := p.effort[mi.Value]
		if !mi.SupportsEffort {
			eff = p.curEff
		}
		m.closeModelPicker()
		return m.applyModel(mi, eff)
	}
	return nil
}

// applyModel switches model and effort in the running session and keeps
// them for restarts.
func (m *Model) applyModel(mi claude.ModelInfo, effort string) tea.Cmd {
	var changes []string
	if mi.Value != m.modelValue() {
		if m.proc != nil {
			_ = m.proc.SetModel(mi.Value)
		}
		m.opts.Model = mi.Value
		if mi.Value == "default" {
			m.opts.Model = ""
		}
		m.model = firstNonEmpty(mi.ResolvedModel, mi.Value)
		m.ctxWindow = 0 // the next result reports the new model's
		changes = append(changes, firstNonEmpty(mi.DisplayName, mi.Value))
	}
	if effort != m.opts.Effort && effort != "" {
		if m.proc != nil {
			_ = m.proc.SetEffort(effort)
		}
		m.opts.Effort = effort
		changes = append(changes, effort+" effort")
	}
	if len(changes) == 0 {
		return nil
	}
	m.refresh()
	return m.notify(toastSuccess, strings.Join(changes, " · "), "")
}

// effortMeter draws rising bars for a level, lit up to the chosen one.
func effortMeter(levels []string, chosen string) string {
	bars := []string{"▁", "▂", "▄", "▆", "█"}
	th := currentTheme()
	real := levels
	if len(real) > 0 && real[0] == "" {
		real = real[1:]
	}
	idx := -1
	for i, l := range real {
		if l == chosen {
			idx = i
		}
	}
	cols := gradient(max(1, len(real)), th.accent, th.second)
	var b strings.Builder
	for i := range real {
		bar := bars[min(i*len(bars)/max(1, len(real)), len(bars)-1)]
		if len(real) == len(bars) {
			bar = bars[i]
		}
		st := lipgloss.NewStyle().Foreground(th.subtle)
		if i <= idx {
			st = lipgloss.NewStyle().Foreground(cols[i])
		}
		b.WriteString(st.Render(bar + bar))
	}
	return b.String()
}

func (p *modelPicker) view(width int) string {
	w := min(width-4, 72)
	inner := w - 6
	th := currentTheme()
	lines := []string{gradientText("Switch model", true), ""}

	for i, mi := range p.models {
		name := firstNonEmpty(mi.DisplayName, mi.Value)
		mark := "  "
		if mi.Value == p.current {
			mark = okStyle.Render("● ")
		}
		tag := ""
		if mi.ResolvedModel != "" && mi.ResolvedModel != name {
			tag = mi.ResolvedModel
		}
		right := mutedStyle.Render(tag)
		if i == p.sel {
			label := selStyle.Render(" " + name + " ")
			gap := inner - 2 - lipgloss.Width(label) - lipgloss.Width(right)
			lines = append(lines, mark+label+strings.Repeat(" ", max(1, gap))+right)
		} else {
			label := textStyle.Render(" " + name + " ")
			gap := inner - 2 - lipgloss.Width(label) - lipgloss.Width(right)
			lines = append(lines, mark+label+strings.Repeat(" ", max(1, gap))+right)
		}
		if i == p.sel && mi.Description != "" {
			lines = append(lines, mutedStyle.Render("    "+truncate(mi.Description, inner-4)))
		}
	}

	// Effort control for the highlighted model.
	lines = append(lines, "", mutedStyle.Render(strings.Repeat("─", inner)))
	mi := p.models[p.sel]
	lv := p.levels()
	if len(lv) == 0 {
		lines = append(lines, sectionTitle.MarginTop(0).Render("Effort")+"  "+mutedStyle.Render("not adjustable for this model"))
	} else {
		chosen := p.effort[mi.Value]
		var segs []string
		for _, l := range lv {
			label := l
			if label == "" {
				label = "default"
			}
			if l == chosen {
				segs = append(segs, lipgloss.NewStyle().Background(th.accent).Foreground(lipgloss.Color("#1a1a1a")).Bold(true).Render(" "+label+" "))
			} else {
				segs = append(segs, mutedStyle.Render(" "+label+" "))
			}
		}
		lines = append(lines,
			sectionTitle.MarginTop(0).Render("Effort")+"  "+effortMeter(lv, chosen),
			strings.Join(segs, ""),
			mutedStyle.Render(effortDesc[chosen]),
		)
	}
	lines = append(lines, "", helpLine([][2]string{{"↑↓", "model"}, {"←→", "effort"}, {"enter", "apply"}, {"esc", "cancel"}}, inner))
	return dialogBox.Width(w).Render(strings.Join(lines, "\n"))
}
