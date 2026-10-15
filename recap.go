package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/exmanubis/claudewitz/internal/config"
	"github.com/exmanubis/claudewitz/internal/recap"
	"github.com/exmanubis/claudewitz/internal/sessions"
	"github.com/exmanubis/claudewitz/internal/tui"
)

// runRecap is `claudewitz recap [-date YYYY-MM-DD | -yesterday] [-md | -json]`:
// a day of work with Claude, tallied from Claude Code's transcripts.
func runRecap(args []string) {
	fs := flag.NewFlagSet("recap", flag.ExitOnError)
	date := fs.String("date", "", "the day to recap, YYYY-MM-DD (default today)")
	yesterday := fs.Bool("yesterday", false, "recap yesterday")
	md := fs.Bool("md", false, "print Markdown, to paste anywhere")
	asJSON := fs.Bool("json", false, "print the tally as JSON")
	fs.Parse(args)

	day := time.Now()
	switch {
	case *date != "":
		d, err := time.ParseInLocation(time.DateOnly, *date, time.Local)
		if err != nil {
			fail("-date wants YYYY-MM-DD, not %q", *date)
		}
		day = d
	case *yesterday:
		day = day.AddDate(0, 0, -1)
	}
	r := recap.NewScanner(sessions.Root()).Recap(day, time.Local)
	switch {
	case *asJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(r)
	case *md:
		fmt.Print(tui.RecapMarkdown(r))
	default:
		cfg, _ := config.Load()
		width := 80
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
			width = w
		}
		fmt.Println(tui.RecapCard(r, cfg, width))
	}
}
