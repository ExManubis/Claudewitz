package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Documents that put blocks next to each other in ways where rendering
// the tail on its own could differ from rendering the whole.
var streamDocs = []string{
	"# Title\n\nFirst paragraph with **bold**, `code` and a [link](http://x).\n\n## Next\n\nSecond paragraph.\n\nThird.\n",
	"Intro.\n\n- one\n- two\n\n- loose three\n\n  continued under three\n\nAfter the list.\n\n1. first\n2. second\n\n3. third\n\nDone.\n",
	"Code:\n\n```go\nfunc main() {\n\n\tfmt.Println(\"blank line above\")\n}\n```\n\nAfter code.\n\n~~~\n```\nnot a close\n\n~~~\n\nEnd.\n",
	"> quoted\n\n> still quoted?\n\nPlain.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n| c | d |\n\nAfter table.\n",
	"Setext\n======\n\nPara.\n\nAnother\n---\n\n---\n\n***\n\n**Bold start** of a paragraph.\n\n*emph start* too.\n",
	"See [the docs][d] for more.\n\nMore text.\n\n[d]: http://example.com\n\nAfter.\n",
	"Footnote here[^1].\n\nText.\n\n[^1]: The note.\n",
	"<div>\n\nhtml block\n\n</div>\n\nAfter html.\n\n    indented code\n\n    more indented\n\nBack.\n",
	"- a\n\n  ```\n  code in list\n\n  more\n  ```\n\n- b\n\nOut.\n",
	strings.Repeat("A sentence that is long enough to wrap at narrow widths, with *emphasis*. ", 4) + "\n\n" +
		strings.Repeat("Another paragraph, plain words only. ", 6) + "\n\n## Heading\n\nTail.\n",
}

func TestStreamMDMatchesWholeRender(t *testing.T) {
	c := newChat(&tracker{})
	for _, width := range []int{36, 100} {
		for _, name := range []string{"md", "quiet"} {
			rend := c.renderer(width)
			if name == "quiet" {
				rend = c.quietRenderer(width)
			}
			for di, doc := range streamDocs {
				steps := []int{7, 40}
				if width == 36 && name == "md" {
					steps = append(steps, 1) // every byte, once
				}
				for _, step := range steps {
					var s streamMD
					for end := step; ; end += step {
						end = min(end, len(doc))
						got, err := s.render(doc[:end], width, rend)
						if err != nil {
							t.Fatal(err)
						}
						want, _ := rend.Render(doc[:end])
						if got != want {
							t.Fatalf("%s width %d doc %d step %d at %d (head %d): streamed render differs\nsource: %q\ngot:  %q\nwant: %q",
								name, width, di, step, end, len(s.head), doc[:end], got, want)
						}
						if end == len(doc) {
							break
						}
					}
				}
			}
		}
	}
}

// Plain prose settles, so streaming it doesn't re-render from the top.
func TestStreamMDSettlesHead(t *testing.T) {
	c := newChat(&tracker{})
	var b strings.Builder
	for i := range 20 {
		fmt.Fprintf(&b, "Paragraph %d of the reply.\n\n", i)
	}
	doc := b.String()
	var s streamMD
	for end := 10; end <= len(doc); end += 10 {
		s.render(doc[:end], 80, c.renderer(80))
	}
	if len(s.head) < len(doc)/2 {
		t.Fatalf("head covers %d of %d bytes", len(s.head), len(doc))
	}
}

// Text that stops being an extension of what came before (a retry, an
// edit) starts over instead of reusing a stale head.
func TestStreamMDResetsOnRewrite(t *testing.T) {
	c := newChat(&tracker{})
	r := c.renderer(80)
	var s streamMD
	s.render("First paragraph.\n\nSecond.\n\nThird.\n", 80, r)
	if s.head == "" {
		t.Fatal("expected a settled head")
	}
	got, _ := s.render("Different text.\n", 80, r)
	want, _ := r.Render("Different text.\n")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A theme switch re-renders replies in the new colours, not from a head
// rendered in the old ones.
func TestThemeSwitchDropsStreamHead(t *testing.T) {
	defer applyTheme(themes[0].name)
	applyTheme("claude")
	c := newChat(&tracker{})
	e := c.add(&entry{kind: kindAssistant, text: "## One\n\nFirst.\n\nSecond.\n\nThird.\n"})
	c.View(80)
	if e.md.head == "" {
		t.Fatal("expected a settled head")
	}
	applyTheme("flexoki")
	c.markAllDirty()
	got := c.View(80)
	fresh := newChat(&tracker{})
	fresh.add(&entry{kind: kindAssistant, text: e.text})
	if want := fresh.View(80); got != want {
		t.Fatalf("stale colours after theme switch:\ngot:  %q\nwant: %q", got, want)
	}
}
