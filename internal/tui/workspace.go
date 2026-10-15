package tui

import "github.com/exmanubis/claudewitz/internal/gitstatus"

// workspace is what the TUI holds for one folder: its git status, the files
// @ completes and the prompt history. Sessions in the same folder share one;
// Model embeds the focused session's.
type workspace struct {
	dir       string
	files     []string
	git       gitstatus.Status
	gitLoaded bool
	gitBusy   bool
	gitPolls  int // poll ticks so far, for reading less often while idle
	hist      *history
}

func newWorkspace(dir string) *workspace {
	return &workspace{dir: dir, hist: newHistory(dir)}
}
