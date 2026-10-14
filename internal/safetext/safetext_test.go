package safetext

import "testing"

func TestClean(t *testing.T) {
	cases := map[string]string{
		"plain\ttext\n":         "plain\ttext\n",
		"evil \x1b]0;PWNED\x07": "evil ␛]0;PWNED",
		"a\rb\x08c\x7fd\u009be": "abcde",
		"café 日本":               "café 日本",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Line("a\nb\tc"); got != "a b c" {
		t.Errorf("Line = %q", got)
	}
}

func TestScreen(t *testing.T) {
	cases := map[string]string{
		"\x1b[38;2;1;2;3mhi\x1b[0m\n": "\x1b[38;2;1;2;3mhi\x1b[0m\n",
		"a\x1b]0;title\x07b":          "ab",
		"a\x1b]52;c;aGk=\x1b\\b":      "ab",
		"a\x1b[2Jb\x1b[Hc":            "abc",
		"a\x1bPtmux;x\x1b\\b":         "ab",
		"x\u009b31my\rz":              "x31myz",
		"\x1b[1;4m日本\x1b[m":           "\x1b[1;4m日本\x1b[m",
		"cut\x1b[":                    "cut",
		"\x1b[?25l":                   "",
	}
	for in, want := range cases {
		if got := Screen(in); got != want {
			t.Errorf("Screen(%q) = %q, want %q", in, got, want)
		}
	}
}
