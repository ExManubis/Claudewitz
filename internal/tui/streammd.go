package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
)

// streamMD renders Markdown that only grows, as a reply does while it
// streams. It keeps the render of a settled head of the text and
// re-renders only the tail after it, so each delta costs the size of
// the last few paragraphs instead of the whole reply.
//
// The head always ends at a blank line where no block is open, and it
// only moves forward when glamour agrees: the tail rendered on its own
// must be an exact suffix of the head-plus-tail render. Anything else
// (new text that isn't an extension, a width change) starts over.
type streamMD struct {
	width int
	head  string // settled source
	out   string // its render, exactly as it appears in a whole render
}

// render returns r's render of content, reusing the settled head.
func (s *streamMD) render(content string, width int, r *glamour.TermRenderer) (string, error) {
	if s.width != width || !strings.HasPrefix(content, s.head) || refDefinition.MatchString(content) {
		*s = streamMD{width: width}
	}
	from := len(s.head)
	tail, err := r.Render(content[from:])
	if err != nil {
		return "", err
	}
	if b := settleBoundary(content, from); b > from && !refDefinition.MatchString(content) {
		if rest, err := r.Render(content[b:]); err == nil && strings.HasSuffix(tail, rest) {
			s.out += tail[:len(tail)-len(rest)]
			s.head = content[:b]
			return s.out + rest, nil
		}
	}
	return s.out + tail, nil
}

// refDefinition matches a link reference or footnote definition, which
// can change how text before it renders; caching is off when one shows up.
var refDefinition = regexp.MustCompile(`(?m)^ {0,3}\[[^\]]+\]:|\[\^`)

// settleBoundary is the last offset after from that starts a block right
// after a blank line, with no fence, list, quote, table, HTML block or
// indented code open across it. The block it starts must be complete
// too, since how glamour spaces the seam depends on that block's kind
// (a paragraph becomes a heading when "---" lands under it). It returns
// from when there is none. content[:from] must itself end at such a
// boundary (or be empty).
func settleBoundary(content string, from int) int {
	best, pending := from, 0
	var fence byte // '`' or '~' while inside a fenced code block
	prev := ""     // last non-blank line
	blank := false
	for i := from; i < len(content); {
		nl := strings.IndexByte(content[i:], '\n')
		if nl < 0 {
			break // incomplete last line
		}
		line := content[i : i+nl]
		if strings.TrimSpace(line) == "" {
			if fence == 0 && pending > 0 {
				best, pending = pending, 0
			}
			blank = true
		} else {
			if blank && i > from && fence == 0 && !openBlock(prev) && !openBlock(line) {
				pending = i
			}
			if c := fenceChar(line); c != 0 && (fence == 0 || c == fence) {
				fence ^= c
			}
			prev, blank = line, false
		}
		i += nl + 1
	}
	return best
}

// openBlock reports whether a line belongs to a block that a blank line
// may not end: lists, quotes, tables, HTML and indented continuation.
func openBlock(line string) bool {
	if line == "" {
		return false
	}
	switch line[0] {
	case ' ', '\t', '>', '<', '|':
		return true
	case '-', '*', '+': // bullet, but not **bold** or a --- rule
		if len(line) == 1 || line[1] == ' ' || line[1] == '\t' {
			return true
		}
	}
	if strings.Contains(line, "|") {
		return true
	}
	// Ordered list item: digits then '.' or ')'.
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i < len(line) && (line[i] == '.' || line[i] == ')')
}

// fenceChar is '`' or '~' for a code fence line, else 0.
func fenceChar(line string) byte {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) <= 3 && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
		return t[0]
	}
	return 0
}
