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

// Streaming a long code block can't reuse a settled head until the fence
// closes, so each redraw renders the whole block; the reveal redraws it
// less often as it grows. One op streams a 30 KB block at 60 frames a
// second, about 100 bytes arriving per frame.
func BenchmarkRevealLongCodeBlock(b *testing.B) {
	var src strings.Builder
	src.WriteString("Here is the code:\n\n```go\n")
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&src, "func f%d(x int) int { return x*%d + len(\"abc\") }\n", i, i)
	}
	src.WriteString("```\n")
	text := src.String()
	t := &testing.T{}
	for i := 0; i < b.N; i++ {
		m := chatModel(t)
		live := m.chat.add(&entry{kind: kindAssistant, paced: true})
		for n := 0; m.chat.revealing() || n < len(text); n += 100 {
			live.setText(text[:min(n, len(text))])
			if m.chat.reveal(1.0 / revealFPS) {
				m.refresh()
				_ = m.View()
			}
		}
	}
}
