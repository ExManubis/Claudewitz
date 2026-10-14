// Package safetext makes untrusted text (file contents, commit messages,
// tool output) safe to draw: terminal control characters in it could
// otherwise retitle the terminal, write the clipboard or redraw the screen.
package safetext

import "strings"

// Clean replaces ESC with a visible ␛ and drops every other C0 and C1
// control character, keeping newlines and tabs.
func Clean(s string) string {
	if !needsClean(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == 0x1b:
			return '␛'
		case isControl(r):
			return -1
		}
		return r
	}, s)
}

// Line is Clean for single-line contexts: newlines and tabs become spaces.
func Line(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, Clean(s))
}

func isControl(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) }

func needsClean(s string) bool {
	for _, r := range s {
		if r != '\n' && r != '\t' && isControl(r) {
			return true
		}
	}
	return false
}

// Screen filters a rendered frame: it keeps SGR sequences (colours and
// text attributes), newlines and tabs, and removes every other escape
// sequence (OSC, DCS, cursor movement, ...) and control character. It is
// the last line of defence for control sequences that reached the frame
// from untrusted text.
func Screen(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r != '\n' && r != '\t' && r != 0x1b && isControl(r) }) &&
		!hasNonSGR(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b {
			n, keep := escapeLen(s[i:])
			if keep {
				b.WriteString(s[i : i+n])
			}
			i += n
			continue
		}
		if c < 0x20 && c != '\n' && c != '\t' || c == 0x7f {
			i++
			continue
		}
		if c == 0xc2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] <= 0x9f { // UTF-8 C1
			i += 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// hasNonSGR reports whether s holds an escape sequence other than SGR.
func hasNonSGR(s string) bool {
	for i := strings.IndexByte(s, 0x1b); i >= 0; {
		n, keep := escapeLen(s[i:])
		if !keep {
			return true
		}
		j := strings.IndexByte(s[i+n:], 0x1b)
		if j < 0 {
			return false
		}
		i += n + j
	}
	return false
}

// escapeLen measures the escape sequence at the start of s (which begins
// with ESC) and reports whether it is an SGR sequence worth keeping.
func escapeLen(s string) (n int, sgr bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch s[1] {
	case '[': // CSI: parameters and intermediates, then a final byte
		for i := 2; i < len(s); i++ {
			if c := s[i]; c >= 0x40 && c <= 0x7e {
				return i + 1, c == 'm' && sgrParams(s[2:i])
			} else if c < 0x20 {
				return i, false
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
	return 2, false
}

func sgrParams(p string) bool {
	for i := 0; i < len(p); i++ {
		if c := p[i]; (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}
