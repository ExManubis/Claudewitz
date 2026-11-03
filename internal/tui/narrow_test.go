package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Every dialog draws without panicking however small the terminal gets.
func TestDialogsOnTinyTerminals(t *testing.T) {
	open := map[string]func(*Model){
		"palette": (*Model).openPalette,
		"model":   (*Model).openModelPicker,
		"theme":   (*Model).openThemePicker,
		"help":    func(m *Model) { m.showHelp = true },
		"session": func(m *Model) { m.picker = newPicker(m.sessionID, m.cwd()) },
	}
	for name, fn := range open {
		for _, w := range []int{1, 2, 4, 6, 8, 10, 12, 16, 20, 24, 30} {
			for _, h := range []int{1, 3, 8} {
				t.Run(fmt.Sprintf("%s/%dx%d", name, w, h), func(t *testing.T) {
					m := chatModel(t)
					fn(m)
					mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
					_ = mm.(Model).render()
				})
			}
		}
	}
}
