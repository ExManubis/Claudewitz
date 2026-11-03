package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestLeadingCd(t *testing.T) {
	for _, tc := range []struct{ cmd, dir, rest string }{
		{"cd internal/tui && go test ./...", "internal/tui", "go test ./..."},
		{`cd "My Dir" && ls`, "My Dir", "ls"},
		{"cd /tmp&&ls", "/tmp", "ls"},
		{"cd /tmp; ls", "", "cd /tmp; ls"},
		{"cd $HOME && ls", "", "cd $HOME && ls"},
		{"cd - && ls", "", "cd - && ls"},
		{"cd /tmp", "", "cd /tmp"},
		{"go test ./...", "", "go test ./..."},
	} {
		dir, rest := leadingCd(tc.cmd)
		if dir != tc.dir || rest != tc.rest {
			t.Errorf("leadingCd(%q) = %q, %q; want %q, %q", tc.cmd, dir, rest, tc.dir, tc.rest)
		}
	}
}

func TestBashHeader(t *testing.T) {
	m := chatModel(t)
	c := m.chat
	render := func(in string) []string {
		e := &entry{kind: kindTool, toolID: "b", toolName: "Bash", toolInput: json.RawMessage(in), toolStatus: toolOK, toolOutput: "ok"}
		return strings.Split(ansi.Strip(c.renderTool(e, 90)), "\n")
	}

	// No description: the command stays in the header.
	if got := render(`{"command":"ls -la"}`); !strings.Contains(got[0], "Bash ls -la") || len(got) != 2 {
		t.Fatalf("plain command: %q", got)
	}

	// A description leads, and the command moves under it.
	got := render(`{"command":"go test ./...","description":"Run the tests"}`)
	if !strings.Contains(got[0], "Bash Run the tests") || strings.Contains(got[0], "go test") {
		t.Fatalf("header should be the description: %q", got[0])
	}
	if strings.TrimSpace(got[1]) != "$ go test ./..." {
		t.Fatalf("command should follow the header: %q", got)
	}

	// A leading cd into a subfolder becomes a chip; into the project, nothing.
	in, _ := json.Marshal(map[string]string{"command": "cd " + filepath.Join(c.cwd, "sub") + " && make", "description": "Build"})
	got = render(string(in))
	if !strings.Contains(got[0], "Build in sub") || strings.TrimSpace(got[1]) != "$ make" {
		t.Fatalf("cd chip: %q", got)
	}
	in, _ = json.Marshal(map[string]string{"command": "cd " + c.cwd + " && make"})
	if got = render(string(in)); strings.Contains(got[0], " in ") || !strings.Contains(got[0], "Bash make") {
		t.Fatalf("cd into the project should drop the chip: %q", got)
	}
}
