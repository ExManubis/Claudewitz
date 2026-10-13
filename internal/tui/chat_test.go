package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestAgentReport(t *testing.T) {
	in := "[Subagent hand-back] The text below is the final report. The report follows:\n  line one\n\n  line two\nagentId: abc"
	if got := agentReport(in); got != "line one\n\nline two" {
		t.Fatalf("got %q", got)
	}
	if got := agentReport("plain"); got != "plain" {
		t.Fatalf("got %q", got)
	}
}

func TestComputeDiff(t *testing.T) {
	d := computeDiff("a\nb\nc\n", "a\nB\nc\n")
	add, del := diffStats(d)
	if add != 1 || del != 1 {
		t.Fatalf("stats %d %d: %+v", add, del, d)
	}
	for _, l := range d {
		if l.kind == '+' && (l.text != "B" || l.newNo != 2) {
			t.Fatalf("bad add line %+v", l)
		}
	}
}

func TestFuzzyScore(t *testing.T) {
	if _, ok := fuzzyScore("cz", "internal/tui/complete.go"); ok {
		t.Fatal("cz should not match: no z after the c")
	}
	a, _ := fuzzyScore("mod", "/model")
	b, _ := fuzzyScore("mod", "/marketing:draft-content")
	if a <= b {
		t.Fatalf("prefix match should rank higher: %d vs %d", a, b)
	}
}

func TestProseWidthCap(t *testing.T) {
	c := newChat(&tracker{})
	long := strings.Repeat("word ", 100)
	for _, kind := range []entryKind{kindAssistant, kindThinking} {
		out := c.renderEntry(&entry{kind: kind, text: long}, 300)
		for _, l := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(strings.TrimRight(ansi.Strip(l), " ")); w > maxProseWidth {
				t.Fatalf("kind %d: line %d wide, want <= %d: %q", kind, w, maxProseWidth, ansi.Strip(l))
			}
		}
		if strings.Count(out, "\n") < 3 {
			t.Fatalf("kind %d: expected wrapping at the cap, got %q", kind, ansi.Strip(out))
		}
	}
}
