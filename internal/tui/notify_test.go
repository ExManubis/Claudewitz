package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestNotifyModesHaveNoNative(t *testing.T) {
	if slices.Contains(notifyModes, "native") || notifyModeDesc["native"] != "" {
		t.Fatal("native notifications were removed")
	}
}

// Everything but the bell goes to the terminal, including a "native"
// setting saved by an older version.
func TestNotificationSequence(t *testing.T) {
	t.Setenv("TERM", "xterm-ghostty")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TMUX", "")
	for _, mode := range []string{"auto", "osc", "native"} {
		if got := notificationSeq(mode, "Claude finished", "done"); !strings.HasPrefix(got, "\x1b]9;Claude finished: done") {
			t.Errorf("%s: got %q", mode, got)
		}
	}
	if got := notificationSeq("bell", "t", "b"); got != "\a" {
		t.Errorf("bell: got %q", got)
	}
}
