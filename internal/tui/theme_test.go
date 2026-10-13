package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

func TestThemesComplete(t *testing.T) {
	defer applyTheme(themes[0].name)
	for _, mode := range []string{"dark", "light"} {
		setAppearance(mode)
		for _, th := range themes {
			if !applyTheme(th.name) {
				t.Fatalf("%s: not found", th.name)
			}
			at := currentTheme()
			for name, c := range map[string]ac{"accent": at.accent, "second": at.second, "text": at.text, "addBg": at.addBg, "delBg": at.delBg, "selBg": at.selBg} {
				if c.Dark == "" || c.Light == "" {
					t.Errorf("%s/%s: %s unset", th.name, mode, name)
				}
			}
			// Unknown chroma names silently fall back, so check registration.
			if styles.Get(at.chroma) == styles.Fallback && at.chroma != "swapoff" {
				t.Errorf("%s/%s: chroma style %q not registered", th.name, mode, at.chroma)
			}
		}
	}
	setAppearance("auto")
}

func TestAppearanceOverride(t *testing.T) {
	defer setAppearance("auto")
	setAppearance("light")
	if darkBG || appearance != "light" {
		t.Fatal("light not applied")
	}
	setAppearance("bogus")
	if appearance != "auto" || darkBG != detectedBG {
		t.Fatal("unknown appearance should fall back to auto")
	}
}

// sgr is the 24-bit foreground escape for a hex colour.
func sgr(hex string) string {
	var r, g, b int
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
}

func TestMarkdownUsesTheme(t *testing.T) {
	defer setAppearance("auto")
	defer applyTheme(themes[0].name)
	setAppearance("dark")
	applyTheme("flexoki")
	c := newChat(&tracker{})
	out := c.renderEntry(&entry{kind: kindAssistant, text: "## Heading\n\n```go\nfunc main() { return }\n```"}, 80)
	th := currentTheme()
	if !strings.Contains(out, sgr(th.accent.Dark)) {
		t.Errorf("heading should use the accent %s", th.accent.Dark)
	}
	kw := styles.Get(th.chroma).Get(chroma.Keyword).Colour.String()
	if !strings.Contains(out, sgr(kw)) {
		t.Errorf("code block should use the %s keyword colour %s", th.chroma, kw)
	}
}
