package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Streaming into a long session costs the same per frame as into a short
// one: only the changed entry is rendered and joined again.
func BenchmarkRefreshLongSession(b *testing.B) {
	t := &testing.T{}
	m := chatModel(t)
	for i := 0; i < 3000; i++ {
		m.chat.add(&entry{kind: kindAssistant, text: fmt.Sprintf("Paragraph %d. %s", i, strings.Repeat("Some words here. ", 30))})
		m.chat.add(&entry{kind: kindTool, toolID: fmt.Sprint(i), toolName: "Bash", toolOutput: strings.Repeat("line\n", 8), toolStatus: toolOK})
	}
	m.refresh()
	live := m.chat.add(&entry{kind: kindAssistant, text: "streaming"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		live.setText(fmt.Sprintf("streaming reply, frame %d", i))
		m.refresh()
		_ = m.View()
	}
}
