package tui

import (
	"testing"

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
