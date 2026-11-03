package tui

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

// registerKeanu adds chroma styles for the keanu theme: shades of green,
// with lime keywords and teal literals so code still has structure.
func registerKeanu() {
	type pal struct{ bg, tx, tx2, tx3, keyword, fn, str, num, red string }
	dark := pal{"#000000", "#00FF41", "#00D447", "#008A30", "#C8FF4D", "#00D78F", "#7DFFA6", "#00D9C3", "#FF5252"}
	light := pal{"#F0FFF2", "#0B3B18", "#2F6B3D", "#6E9B78", "#4D7A00", "#00802A", "#00663A", "#00796B", "#C62828"}
	for name, p := range map[string]pal{"keanu-dark": dark, "keanu-light": light} {
		styles.Register(chroma.MustNewStyle(name, chroma.StyleEntries{
			chroma.Background:          "bg:" + p.bg + " " + p.tx,
			chroma.Text:                p.tx,
			chroma.Comment:             "italic " + p.tx3,
			chroma.CommentPreproc:      p.keyword,
			chroma.Keyword:             "bold " + p.keyword,
			chroma.KeywordConstant:     p.num,
			chroma.KeywordType:         p.fn,
			chroma.Operator:            p.tx2,
			chroma.Punctuation:         p.tx2,
			chroma.Name:                p.tx,
			chroma.NameBuiltin:         p.fn,
			chroma.NameFunction:        p.fn,
			chroma.NameClass:           p.fn,
			chroma.NameTag:             p.keyword,
			chroma.NameAttribute:       p.str,
			chroma.LiteralString:       p.str,
			chroma.LiteralStringEscape: p.keyword,
			chroma.LiteralNumber:       p.num,
			chroma.GenericInserted:     p.tx,
			chroma.GenericDeleted:      p.red,
			chroma.GenericHeading:      "bold " + p.keyword,
			chroma.GenericStrong:       "bold",
			chroma.GenericEmph:         "italic",
			chroma.Error:               p.red,
		}))
	}
}
