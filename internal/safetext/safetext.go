// Package safetext makes untrusted text (file contents, commit messages,
// tool output, Claude's replies) safe to draw: terminal control characters
// in it could otherwise retitle the terminal, write the clipboard, redraw
// the screen or restyle text to hide it.
package safetext

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Clean replaces ESC with a visible ␛ and drops every other C0 and C1
// control character, keeping newlines and tabs.
func Clean(s string) string { return clean(s, false) }

// Line is Clean for single-line contexts: newlines and tabs become spaces.
func Line(s string) string { return clean(s, true) }

func clean(s string, oneLine bool) string {
	ok := func(r rune) bool { return !isControl(r) || (!oneLine && (r == '\n' || r == '\t')) }
	if !strings.ContainsFunc(s, func(r rune) bool { return !ok(r) }) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case ok(r):
			return r
		case r == '\n' || r == '\t':
			return ' '
		case r == 0x1b:
			return '␛'
		}
		return -1
	}, s)
}

func isControl(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) }

// JSON cleans every string in a JSON document, for displaying a tool
// call's input. Documents without control characters come back as they
// are; so does anything that doesn't parse.
func JSON(raw json.RawMessage) json.RawMessage {
	// Control characters can only appear escaped (\u001b, \r, ...) or as
	// raw UTF-8 C1 characters.
	if !bytes.Contains(raw, []byte(`\`)) && utf8.Valid(raw) && !bytes.ContainsFunc(raw, func(r rune) bool { return r >= 0x80 && r <= 0x9f }) {
		return raw
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	changed := false
	var walk func(any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			if c := Clean(t); c != t {
				changed = true
				return c
			}
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return v
	}
	v = walk(v)
	if !changed {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// Screen filters a rendered frame: it keeps SGR sequences (colours and
// text attributes), newlines and tabs, and removes every other escape
// sequence (OSC, DCS, cursor movement, ...) and control character. It is
// the last line of defence for control sequences that reached the frame
// from untrusted text. A clean frame costs one pass and no allocation.
func Screen(s string) string {
	var b *strings.Builder
	last := 0 // s[last:i] is yet to be copied to b
	drop := func(i, n int) {
		if b == nil {
			b = &strings.Builder{}
			b.Grow(len(s))
		}
		b.WriteString(s[last:i])
		last = i + n
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			n, keep := escapeLen(s[i:])
			if !keep {
				drop(i, n)
			}
			i += n
		case c < 0x20 && c != '\n' && c != '\t', c == 0x7f:
			drop(i, 1)
			i++
		case c == 0xc2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] <= 0x9f: // UTF-8 C1
			drop(i, 2)
			i += 2
		default:
			i++
		}
	}
	if b == nil {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// escapeLen measures the escape sequence at the start of s (which begins
// with ESC) and reports whether it is an SGR sequence worth keeping. It
// never splits a UTF-8 character.
func escapeLen(s string) (n int, sgr bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch s[1] {
	case '[': // CSI: parameter and intermediate bytes, then a final byte
		for i := 2; i < len(s); i++ {
			switch c := s[i]; {
			case c >= 0x40 && c <= 0x7e:
				return i + 1, c == 'm' && sgrParams(s[2:i])
			case c < 0x20 || c > 0x7e:
				return i, false // malformed: drop what came before
			}
		}
		return len(s), false
	case ']', 'P', '_', '^', 'X': // string sequences, ended by ST or BEL
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1, false
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2, false
			}
		}
		return len(s), false
	}
	if s[1] < 0x20 || s[1] >= 0x80 {
		return 1, false // a lone ESC: drop just that
	}
	return 2, false // ESC and one ASCII byte (ESC 7, ESC =, ...)
}

func sgrParams(p string) bool {
	for i := 0; i < len(p); i++ {
		if c := p[i]; (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}
