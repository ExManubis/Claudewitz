package tui

import (
	"encoding/base64"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/exmanubis/claudewitz/internal/daemon"
	"github.com/exmanubis/claudewitz/internal/safetext"
)

// Program status: tells the terminal what each session is doing with OSC
// 7501, the Program Status Protocol
// (https://www.superlogical.com/rex/docs/build/program-status), so a
// terminal that shows it (Rex, PrompTTY) can mark the tab as working,
// waiting on you or done. Terminals without support ignore the sequence.
//
// Each live session is a record of its own, so one in the background that
// needs you shows too. Without the daemon, the session on screen is
// claudewitz's own record.

const (
	statusMaxTitle = 192  // decoded bytes the protocol allows in a title
	statusMaxMsg   = 2048 // and in a message
	statusMaxID    = 32   // bytes in one segment of an id
)

// programStatus is the report for each record, keyed by id ("" for
// claudewitz's own record).
func (m *Model) programStatus() map[string]string {
	out := map[string]string{}
	if len(m.live) == 0 {
		if m.sessionView != nil {
			out[""] = m.ownStatus()
		}
		return out
	}
	labels := folderLabels(m.live)
	for _, s := range m.live {
		if id := statusID(s.ID); id != "" {
			out[id] = "id=" + id + ":" + sessionStatus(s, m.done[s.ID], labels[s.ID])
		}
	}
	return out
}

// sessionStatus is a daemon session's report, without its id.
func sessionStatus(s daemon.SessionSummary, done bool, label string) string {
	title := statusText("title", label, statusMaxTitle)
	switch stateOf(s, done) {
	case stWaiting:
		kind, msg := "permission", ""
		if w := s.Waiting; w != nil {
			msg = strings.TrimSpace(w.Tool + " " + w.Summary)
			if w.Tool == toolAskUser {
				kind, msg = "question", w.Summary
			}
		}
		return "state=blocked:kind=" + kind + title + statusText("msg", msg, statusMaxMsg)
	case stError:
		msg := ""
		if s.LastError != nil {
			msg = *s.LastError
		}
		return "state=error" + title + statusText("msg", msg, statusMaxMsg)
	case stWorking:
		msg := ""
		if s.Activity != nil {
			msg = s.Activity.Text
		}
		return "state=working" + title + statusText("msg", msg, statusMaxMsg)
	case stDone:
		return "state=done" + title
	}
	return "state=idle" + title
}

// ownStatus is the report for the session on screen when there's no daemon.
func (m *Model) ownStatus() string {
	if len(m.pending) > 0 {
		p := m.pending[0]
		if p.ToolName == toolAskUser {
			return "state=blocked:kind=question" + statusText("msg", summarizeInput(p.ToolName, p.Input, 200), statusMaxMsg)
		}
		msg := strings.TrimSpace(p.ToolName + " " + summarizeInput(p.ToolName, p.Input, 200))
		return "state=blocked:kind=permission" + statusText("msg", msg, statusMaxMsg)
	}
	if m.busy {
		return "state=working" + statusText("msg", m.activity, statusMaxMsg)
	}
	return "state=idle"
}

// statusID makes a session id a valid record id: 1 to 32 bytes of
// [A-Za-z0-9_.+-].
func statusID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r < utf8.RuneSelf && (r == '_' || r == '.' || r == '+' || r == '-' ||
			('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')) {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > statusMaxID {
		s = s[len(s)-statusMaxID:]
	}
	return s
}

// statusText is ":key=" and s, one line without control characters, cut to
// max bytes on a rune boundary and base64 encoded; empty for no text.
func statusText(key, s string, max int) string {
	s = strings.TrimSpace(safetext.Line(s))
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	if s == "" {
		return ""
	}
	return ":" + key + "=" + base64.StdEncoding.EncodeToString([]byte(s))
}

// statusSeq is what to write to bring the terminal from the reports sent to
// want: clears for records gone, then each report that changed.
func statusSeq(sent, want map[string]string) string {
	var b strings.Builder
	for _, id := range slices.Sorted(mapKeys(sent)) {
		if _, ok := want[id]; ok {
			continue
		}
		if id == "" {
			// Clearing without an id clears every record; say idle instead.
			b.WriteString(oscStatus("state=idle"))
		} else {
			b.WriteString(oscStatus("state=clear:id=" + id))
		}
	}
	for _, id := range slices.Sorted(mapKeys(want)) {
		if sent[id] != want[id] {
			b.WriteString(oscStatus(want[id]))
		}
	}
	return b.String()
}

func mapKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func oscStatus(body string) string {
	seq := "\x1b]7501;" + body + "\x1b\\"
	if os.Getenv("TMUX") != "" {
		// tmux forwards it only with `set -g allow-passthrough on`.
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return seq
}

// syncStatus sends the terminal whatever changed in the sessions' status.
func (m *Model) syncStatus() {
	want := m.programStatus()
	if seq := statusSeq(m.statusSent, want); seq != "" {
		writeStatus(seq)
	}
	if m.statusSent == nil {
		m.statusSent = map[string]string{}
	}
	clear(m.statusSent)
	for k, v := range want {
		m.statusSent[k] = v
	}
}

// ClearProgramStatus removes every status record claudewitz reported, as it
// quits.
func ClearProgramStatus() {
	statusMu.Lock()
	defer statusMu.Unlock()
	if statusSentAny {
		statusOut(oscStatus("state=clear"))
	}
}

var (
	statusMu      sync.Mutex
	statusSentAny bool
	statusOut     = writeTerminal // tests replace it
)

// writeStatus writes in order: reports must reach the terminal in the order
// they were made.
func writeStatus(seq string) {
	statusMu.Lock()
	defer statusMu.Unlock()
	statusSentAny = true
	statusOut(seq)
}
