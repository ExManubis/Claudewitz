package tui

import (
	"encoding/json"
	"time"

	"github.com/exmanubis/claudewitz/internal/recap"
)

var pokeQuips = map[sparkMode][]string{
	sparkIdle:    {"wheee!", "beep boop", "hi there", "*whirr*", "ready when you are", "again!", "✦"},
	sparkWorking: {"working on it!", "almost…", "busy busy", "*clack clack*", "hold on!"},
}

// poke answers a click on tty with a spin (or a bloom mid-turn) and a word.
func (m *Model) poke() {
	if m.sparks == nil || !m.sparks.poke() {
		return
	}
	quips := pokeQuips[m.sparks.mode]
	m.pokes++
	m.quip, m.quipAt = quips[m.pokes%len(quips)], time.Now()
	m.lastInput = time.Now() // and wake the bop
}

// celebrate marks what a finished shell command shipped: a commit, a pull
// request opened or merged, a release, or tests green after a red run. tty
// sets off fireworks, the caption under it says what happened, a toast
// says it in full, and tty shows a ✓ when it next rests.
func (m *Model) celebrate(input json.RawMessage, output string, failed bool) {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &in) != nil || in.Command == "" {
		return
	}
	for _, ms := range recap.Classify(in.Command, output, failed) {
		var title, body string
		big := false
		switch ms.Kind {
		case recap.Commit:
			title, body = "Committed", firstNonEmpty(ms.Text, "a commit")
		case recap.PR:
			title, body = "Pull request opened", shortPR(ms.Text)
		case recap.Merge:
			title, body, big = "Merged", firstNonEmpty(ms.Text, "the pull request"), true
		case recap.Release:
			title, body, big = "Released "+ms.Text, "out the door", true
		case recap.TestsFailed:
			m.testsRed = true
			continue
		case recap.Tests:
			if !m.testsRed {
				continue // green is the norm; only a recovery is news
			}
			m.testsRed = false
			title, body = "Tests green again", ms.Text
		default:
			continue
		}
		m.cheer, m.cheerAt = title, time.Now()
		m.cheers = append(m.cheers, m.notify(toastCheer, title, body))
		if m.sparks != nil {
			m.sparks.celebrate(big)
		}
	}
}
