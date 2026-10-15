package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/exmanubis/claudewitz/internal/claude"
	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/daemon/client"
	"github.com/exmanubis/claudewitz/internal/tui"
)

// version is set by release builds (-ldflags "-X main.version=…").
var version = ""

// versionString is the release version, else the module version go install
// recorded, else "dev".
func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "daemon":
			runDaemon(os.Args[2:])
			return
		case "pair":
			runPair(os.Args[2:])
			return
		case "devices":
			runDevices(os.Args[2:])
			return
		}
	}
	var opts claude.Options
	flag.StringVar(&opts.Binary, "claude", "claude", "path to the claude CLI")
	flag.StringVar(&opts.Model, "model", "", "model alias or full name (opus, sonnet, haiku, …)")
	flag.StringVar(&opts.Effort, "effort", "", "effort level (low, medium, high, xhigh, max)")
	flag.StringVar(&opts.PermissionMode, "mode", "", "initial permission mode (default, acceptEdits, plan)")
	flag.StringVar(&opts.Resume, "resume", "", "resume a session by id")
	flag.BoolVar(&opts.Continue, "continue", false, "continue the most recent session in this directory")
	flag.StringVar(&opts.CWD, "cwd", "", "working directory (defaults to current)")
	theme := flag.String("theme", "", "colour theme for this run (overrides the saved one)")
	noDaemon := flag.Bool("no-daemon", false, "run claude directly instead of in the claudewitz daemon")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("claudewitz", versionString())
		return
	}
	opts.ExtraArgs = flag.Args()

	if _, err := exec.LookPath(opts.Binary); err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz needs the Claude Code CLI (%q not found).\nInstall it: npm install -g @anthropic-ai/claude-code, then run `claude` once to log in.\n", opts.Binary)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if moved, err := config.Migrate(); err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz: couldn't move settings to %s: %v\n", config.Dir(), err)
	} else if len(moved) > 0 {
		fmt.Fprintf(os.Stderr, "claudewitz: moved %s to %s\n", strings.Join(moved, " and "), config.Dir())
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudewitz: ignoring unreadable config %s: %v\n", config.Path(), err)
	}
	if *theme != "" {
		cfg.Theme = *theme
	}

	var start tui.Starter // nil: run claude directly
	var notice string
	if !*noDaemon {
		c, err := connectDaemon(ctx, opts.Binary)
		if err != nil {
			notice = fmt.Sprintf("couldn't reach the claudewitz daemon (%v); this session runs here and ends when you quit", err)
		} else {
			defer c.Close()
			start = client.Starter(c)
		}
	}

	p := tea.NewProgram(tui.New(ctx, opts, cfg, start).WithNotice(notice), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
