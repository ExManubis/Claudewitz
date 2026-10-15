package tui

import (
	"context"
	"testing"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/gitstatus"
)

func TestApplyGitToasts(t *testing.T) {
	dir := t.TempDir()
	m := New(context.Background(), claude.Options{CWD: dir}, config.Config{}, nil)
	base := gitstatus.Status{IsRepo: true, Branch: "main", Head: "aaa", LastCommit: "aaa old", LastWhen: time.Now().Add(-time.Hour)}

	// The first snapshot never toasts.
	m.applyGit(gitStatusMsg{dir: dir, st: base})
	if len(m.toasts) != 0 {
		t.Fatalf("first load toasted: %+v", m.toasts)
	}

	// New branch plus a fresh commit: both toasts.
	next := base
	next.Branch, next.Head, next.LastCommit, next.LastWhen = "feat", "bbb", "bbb new work", time.Now()
	m.applyGit(gitStatusMsg{dir: dir, st: next})
	if len(m.toasts) != 2 || m.toasts[0].title != "Switched to feat" || m.toasts[1].title != "Committed on feat" {
		t.Fatalf("toasts: %+v", m.toasts)
	}

	// Checking out an old commit is a detached HEAD, not a new commit.
	m.toasts = nil
	old := base
	old.Branch = ""
	m.applyGit(gitStatusMsg{dir: dir, st: old})
	if len(m.toasts) != 1 || m.toasts[0].title != "Detached HEAD" {
		t.Fatalf("detached: %+v", m.toasts)
	}

	// A snapshot for a directory we've since left is discarded.
	m.toasts = nil
	m.applyGit(gitStatusMsg{dir: "/elsewhere", st: next})
	if m.git.Branch != "" || len(m.toasts) != 0 {
		t.Fatalf("stale snapshot applied: %+v", m.git)
	}
}

// While idle, polls read the repository only every gitIdleEvery ticks.
func TestGitPollSlowsWhenIdle(t *testing.T) {
	m := chatModel(t)
	reads := func(n int) int {
		got := 0
		for range n {
			m.gitBusy = false
			m.onGitPoll()
			if m.gitBusy {
				got++
			}
		}
		return got
	}
	if got := reads(gitIdleEvery); got != gitIdleEvery {
		t.Errorf("active: %d reads in %d polls", got, gitIdleEvery)
	}
	m.focused = false
	if got := reads(gitIdleEvery * 2); got != 2 {
		t.Errorf("idle: %d reads in %d polls, want 2", got, gitIdleEvery*2)
	}
}
