package tui

import (
	"reflect"
	"testing"
)

func TestIconSetsComplete(t *testing.T) {
	for name, set := range map[string]iconSet{"nerd": nerdIcons, "unicode": unicodeIcons} {
		v := reflect.ValueOf(set)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).String() == "" {
				t.Errorf("%s icons: %s is empty", name, v.Type().Field(i).Name)
			}
		}
	}
}

func TestApplyIcons(t *testing.T) {
	defer applyIcons("")
	applyIcons("unicode")
	if icons != unicodeIcons {
		t.Fatal("unicode mode should use the Unicode icons")
	}
	for _, mode := range []string{"", "nerd", "bogus"} {
		applyIcons(mode)
		if icons != nerdIcons {
			t.Errorf("mode %q should fall back to Nerd Font icons", mode)
		}
	}
}
