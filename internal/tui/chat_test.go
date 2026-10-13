package tui

import "testing"

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
