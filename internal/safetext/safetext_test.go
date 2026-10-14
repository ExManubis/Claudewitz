package safetext

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

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
	if got := Line("a\nb\tc\x1b"); got != "a b c␛" {
		t.Errorf("Line = %q", got)
	}
}

func TestScreenKeepsUTF8Valid(t *testing.T) {
	for _, s := range []string{"\x1bé", "\x1b[é", "\x1b]0;é", "x\x1b日本", "\x1b\u0085y"} {
		if got := Screen(s); !utf8.ValidString(got) {
			t.Errorf("Screen(%q) = %q, not valid UTF-8", s, got)
		}
	}
}

func TestJSON(t *testing.T) {
	clean := `{"command":"ls -la","n":3}`
	if got := string(JSON(json.RawMessage(clean))); got != clean {
		t.Errorf("clean input changed: %s", got)
	}
	got := JSON(json.RawMessage(`{"command":"echo hi\u001b[8m; rm -rf ~","list":["a\rb"]}`))
	var v struct {
		Command string   `json:"command"`
		List    []string `json:"list"`
	}
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatal(err)
	}
	if v.Command != "echo hi␛[8m; rm -rf ~" || v.List[0] != "ab" {
		t.Errorf("JSON = %s", got)
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
