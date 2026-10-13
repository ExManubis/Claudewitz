package tui

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

// registerFlexoki adds chroma styles for Flexoki (stephango.com/flexoki),
// which chroma doesn't ship, following its editor token mapping.
func registerFlexoki() {
	type pal struct{ bg, tx, tx2, tx3, red, orange, yellow, green, cyan, blue, purple, magenta string }
	dark := pal{"#100F0F", "#CECDC3", "#878580", "#575653", "#D14D41", "#DA702C", "#D0A215", "#879A39", "#3AA99F", "#4385BE", "#8B7EC8", "#CE5D97"}
	light := pal{"#FFFCF0", "#100F0F", "#6F6E69", "#B7B5AC", "#AF3029", "#BC5215", "#AD8301", "#66800B", "#24837B", "#205EA6", "#5E409D", "#A02F6F"}
	for name, p := range map[string]pal{"flexoki-dark": dark, "flexoki-light": light} {
		styles.Register(chroma.MustNewStyle(name, chroma.StyleEntries{
			chroma.Background:          "bg:" + p.bg + " " + p.tx,
			chroma.Text:                p.tx,
			chroma.Comment:             "italic " + p.tx3,
			chroma.CommentPreproc:      p.magenta,
			chroma.Keyword:             p.green,
			chroma.KeywordConstant:     p.yellow,
			chroma.KeywordType:         p.yellow,
			chroma.Operator:            p.tx2,
			chroma.Punctuation:         p.tx2,
			chroma.Name:                p.tx,
			chroma.NameBuiltin:         p.yellow,
			chroma.NameFunction:        p.orange,
			chroma.NameClass:           p.yellow,
			chroma.NameConstant:        p.yellow,
			chroma.NameVariable:        p.blue,
			chroma.NameTag:             p.blue,
			chroma.NameAttribute:       p.yellow,
			chroma.NameDecorator:       p.magenta,
			chroma.LiteralString:       p.cyan,
			chroma.LiteralStringEscape: p.magenta,
			chroma.LiteralNumber:       p.purple,
			chroma.GenericInserted:     p.green,
			chroma.GenericDeleted:      p.red,
			chroma.GenericHeading:      "bold " + p.orange,
			chroma.GenericStrong:       "bold",
			chroma.GenericEmph:         "italic",
			chroma.Error:               p.red,
		}))
	}
}
