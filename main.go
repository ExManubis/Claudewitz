package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/tui"
)

func main() {
	var opts claude.Options
	flag.StringVar(&opts.Binary, "claude", "claude", "path to the claude CLI")
	flag.StringVar(&opts.Model, "model", "", "model alias or full name (opus, sonnet, haiku, …)")
	flag.StringVar(&opts.Effort, "effort", "", "effort level (low, medium, high, xhigh, max)")
	flag.StringVar(&opts.PermissionMode, "mode", "", "initial permission mode (default, acceptEdits, plan)")
	flag.StringVar(&opts.Resume, "resume", "", "resume a session by id")
	flag.BoolVar(&opts.Continue, "continue", false, "continue the most recent session in this directory")
	flag.StringVar(&opts.CWD, "cwd", "", "working directory (defaults to current)")
	theme := flag.String("theme", "", "colour theme for this run (overrides the saved one)")
	flag.Parse()
	opts.ExtraArgs = flag.Args()

	if _, err := exec.LookPath(opts.Binary); err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz needs the Claude Code CLI (%q not found).\nInstall it: npm install -g @anthropic-ai/claude-code, then run `claude` once to log in.\n", opts.Binary)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz: ignoring unreadable config %s: %v\n", config.Path(), err)
	}
	if *theme != "" {
		cfg.Theme = *theme
	}

	p := tea.NewProgram(tui.New(ctx, opts, cfg), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
