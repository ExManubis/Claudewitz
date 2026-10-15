package daemon

import (
	"fmt"
	"testing"
	"time"
)

func seqs(b *buffer) string {
	var out []int64
	for _, e := range b.entries {
		out = append(out, e.seq)
	}
	return fmt.Sprint(out)
}

func TestBufferKeepsCurrentTurnAndSticky(t *testing.T) {
	b := &buffer{limit: 3}
	b.add(entry{seq: 1, sticky: "init"})
	for i := int64(2); i <= 4; i++ {
		b.add(entry{seq: i})
	}
	b.endTurn(4)
	// Over the limit: the oldest finished events go, the init stays.
	b.add(entry{seq: 5})
	if got := seqs(b); got != "[1 4 5]" {
		t.Fatalf("after eviction %s", got)
	}
	// The current turn is kept whatever its length.
	for i := int64(6); i <= 9; i++ {
		b.add(entry{seq: i})
	}
	if got := seqs(b); got != "[1 5 6 7 8 9]" {
		t.Fatalf("long turn %s", got)
	}
	// A newer init supersedes the old one, which may then go.
	b.add(entry{seq: 10, sticky: "init"})
	b.endTurn(10)
	if got := seqs(b); got != "[8 9 10]" {
		t.Fatalf("after new init %s", got)
	}
}

func TestBufferDropsDeltasOfFinishedMessage(t *testing.T) {
	b := newBuffer()
	b.add(entry{seq: 1, stream: true})
	b.add(entry{seq: 2, stream: true, parent: "agent"})
	b.add(entry{seq: 3, stream: true})
	b.dropDeltas("")
	if got := seqs(b); got != "[2]" {
		t.Fatalf("got %s", got)
	}
}

func TestBufferSince(t *testing.T) {
	b := newBuffer()
	b.add(entry{seq: 1, sticky: "init", frame: []byte("a")})
	b.add(entry{seq: 2, frame: []byte("b")})
	b.add(entry{seq: 3, frame: []byte("c")})
	frames, from := b.since(nil, 4)
	if len(frames) != 3 || from != 2 {
		t.Fatalf("all: %d frames from %d", len(frames), from)
	}
	two := int64(2)
	frames, from = b.since(&two, 4)
	if len(frames) != 1 || from != 3 {
		t.Fatalf("since 2: %d frames from %d", len(frames), from)
	}
	three := int64(3)
	if frames, from = b.since(&three, 4); len(frames) != 0 || from != 4 {
		t.Fatalf("since 3: %d frames from %d", len(frames), from)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("  \n  "+string(make([]rune, 0))+"abcdefghij", 5); got != "abcd…" {
		t.Fatalf("cut: %q", got)
	}
}

func TestSameSummary(t *testing.T) {
	id1, id2 := "a", "a"
	a := SessionSummary{ID: "s_1", ClaudeSessionID: &id1, Activity: &Activity{"tool", "ls"}, LastActivity: now()}
	b := a
	b.ClaudeSessionID, b.Activity = &id2, &Activity{"tool", "ls"}
	b.LastActivity = a.LastActivity.Add(time.Minute)
	if !sameSummary(a, b) {
		t.Fatal("equal values behind different pointers, or only lastActivity, should match")
	}
	b.Activity = &Activity{"tool", "pwd"}
	if sameSummary(a, b) {
		t.Fatal("a different activity should not match")
	}
	b = a
	b.CostUSD = 1
	if sameSummary(a, b) {
		t.Fatal("a different cost should not match")
	}
}
