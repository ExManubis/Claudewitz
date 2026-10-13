// Package sessions reads Claude Code's on-disk session transcripts
// (~/.claude/projects/<encoded-cwd>/<session-id>.jsonl).
package sessions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// Info summarises one session file.
type Info struct {
	ID          string
	Path        string
	CWD         string
	Title       string // custom title or summary, if any
	FirstPrompt string
	LastPrompt  string
	Branch      string
	Updated     time.Time
	Messages    int
}

// Label is the best human-readable name for the session.
func (i Info) Label() string {
	for _, s := range []string{i.Title, i.FirstPrompt, i.LastPrompt} {
		if s != "" {
			return s
		}
	}
	return i.ID
}

// Root is Claude Code's projects directory.
func Root() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// ProjectDir is where sessions started in cwd are stored.
func ProjectDir(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err == nil {
		cwd = abs
	}
	enc := []byte(cwd)
	for i, c := range enc {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			enc[i] = '-'
		}
	}
	return filepath.Join(Root(), string(enc))
}

// List returns the sessions in one project dir, newest first.
func List(dir string) []Info {
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	out := make([]Info, 0, len(files))
	for _, f := range files {
		if info, ok := Read(f); ok {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// ListAll returns sessions across every project, newest first.
func ListAll() []Info {
	dirs, _ := os.ReadDir(Root())
	var out []Info
	for _, d := range dirs {
		if d.IsDir() {
			out = append(out, List(filepath.Join(Root(), d.Name()))...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Find locates a session file by id in dir, or the most recent one when id
// is empty.
func Find(dir, id string) (string, bool) {
	if id != "" {
		p := filepath.Join(dir, id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		return "", false
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	var best string
	var bestT time.Time
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.ModTime().After(bestT) {
			best, bestT = f, st.ModTime()
		}
	}
	return best, best != ""
}

type line struct {
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	CWD         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	Timestamp   string          `json:"timestamp"`
	Summary     string          `json:"summary"`
	CustomTitle string          `json:"customTitle"`
	LastPrompt  string          `json:"lastPrompt"`
	Message     json.RawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

const headTail = 256 << 10

// Read summarises a session file. Large files are sampled at the head and
// tail so listing stays fast.
func Read(path string) (Info, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Info{}, false
	}
	info := Info{
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:    path,
		Updated: st.ModTime(),
	}
	var data []byte
	if st.Size() <= 2*headTail {
		data, _ = io.ReadAll(f)
	} else {
		head := make([]byte, headTail)
		n, _ := io.ReadFull(f, head)
		tail := make([]byte, headTail)
		m, _ := f.ReadAt(tail, st.Size()-headTail)
		data = append(head[:n], '\n')
		if i := bytes.IndexByte(tail[:m], '\n'); i >= 0 {
			data = append(data, tail[i+1:m]...)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		switch l.Type {
		case "summary":
			if info.Title == "" {
				info.Title = l.Summary
			}
		case "custom-title":
			info.Title = l.CustomTitle
		case "last-prompt":
			info.LastPrompt = oneLine(l.LastPrompt)
		case "user", "assistant":
			if l.IsSidechain || l.IsMeta {
				continue
			}
			info.Messages++
			if l.CWD != "" {
				info.CWD = l.CWD
			}
			if l.GitBranch != "" && l.GitBranch != "HEAD" {
				info.Branch = l.GitBranch
			}
			if l.Type == "user" && info.FirstPrompt == "" {
				var m rawMessage
				if json.Unmarshal(l.Message, &m) == nil {
					if t := promptText(m.Content); t != "" {
						info.FirstPrompt = oneLine(t)
					}
				}
			}
		}
	}
	return info, info.Messages > 0
}

// promptText returns typed user text, skipping tool results and
// harness-injected content (which starts with '<').
func promptText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var blocks []claude.ContentBlock
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		for _, b := range blocks {
			if b.Type == "text" {
				s = b.Text
				break
			}
		}
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// Message is one main-thread message of a transcript.
type Message struct {
	Role    string
	Content []claude.ContentBlock
}

// Load reads the main-thread conversation of a session for replay.
func Load(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Message
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if (l.Type != "user" && l.Type != "assistant") || l.IsSidechain || l.IsMeta {
			continue
		}
		var m rawMessage
		if json.Unmarshal(l.Message, &m) != nil {
			continue
		}
		var blocks []claude.ContentBlock
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			if strings.HasPrefix(strings.TrimSpace(s), "<") {
				continue
			}
			blocks = []claude.ContentBlock{{Type: "text", Text: s}}
		} else if json.Unmarshal(m.Content, &blocks) != nil {
			continue
		}
		out = append(out, Message{Role: m.Role, Content: blocks})
	}
	return out, sc.Err()
}
