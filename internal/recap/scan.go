package recap

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Horizon is how far back the scanner reads: transcripts untouched for
// longer are left alone, so a recap never wades through a year of history.
const Horizon = 35 * 24 * time.Hour

// idleGap is the longest pause inside a turn that still counts as Claude
// working. Longer ones are a permission prompt or a question waiting on you.
const idleGap = 10 * time.Minute

// quarter is the bucket activity is kept in: fine enough for any timezone.
const quarter = 15 * 60

// Scanner tallies Claude Code's transcripts. It keeps what it has read, so
// later recaps only read what was appended since.
type Scanner struct {
	root string
	now  func() time.Time

	mu    sync.Mutex
	files map[string]*tally
	seen  map[[16]byte]struct{} // message uuids, as resumed sessions copy their history
}

// NewScanner reads transcripts under root, Claude Code's projects folder.
func NewScanner(root string) *Scanner {
	return &Scanner{root: root, now: time.Now, files: map[string]*tally{}, seen: map[[16]byte]struct{}{}}
}

// turn is one prompt and Claude's work on it.
type turn struct {
	at      time.Time
	active  time.Duration
	prompt  string
	project string
}

type edit struct {
	at             time.Time
	path           string
	added, removed int
}

type pendingTool struct {
	name, command, path string
}

// tally is what one transcript file holds, read up to offset.
type tally struct {
	side    bool // a sub-agent's transcript: its work counts, it has no prompts
	session string
	offset  int64
	size    int64

	turns  []turn
	edits  []edit
	marks  []Milestone
	active map[int64]time.Duration // Claude working, per quarter hour (unix/900)
	latest map[int64]time.Time     // the last moment of work in each
	tokens map[int64]int           // output tokens, per quarter hour
	tools  map[int64]int           // tool calls, per quarter hour

	lastAt  time.Time // last activity in the open turn; zero between turns
	lastMsg string    // assistant message whose usage was counted last
	pending map[string]pendingTool
	claimed [][16]byte // the message uuids this file counted, to forget with it
}

func newTally(side bool, session string) *tally {
	return &tally{
		side: side, session: session,
		active: map[int64]time.Duration{}, latest: map[int64]time.Time{}, tokens: map[int64]int{}, tools: map[int64]int{},
		pending: map[string]pendingTool{},
	}
}

// refresh reads whatever was appended to the transcripts since last time,
// and forgets files that were deleted or have aged past the horizon, so a
// daemon that runs for weeks doesn't keep growing.
func (s *Scanner) refresh() {
	cutoff := s.now().Add(-Horizon)
	main, _ := filepath.Glob(filepath.Join(s.root, "*", "*.jsonl"))
	side, _ := filepath.Glob(filepath.Join(s.root, "*", "*", "subagents", "*.jsonl"))
	current := map[string]bool{}
	for i, path := range append(main, side...) {
		st, err := os.Stat(path)
		if err != nil || st.ModTime().Before(cutoff) {
			continue
		}
		current[path] = true
		t := s.files[path]
		if t != nil && st.Size() < t.size {
			s.forget(path, t) // rewritten from scratch
			t = nil
		}
		if t == nil {
			sess := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			isSide := i >= len(main)
			if isSide {
				sess = filepath.Base(filepath.Dir(filepath.Dir(path)))
			}
			t = newTally(isSide, sess)
			s.files[path] = t
		}
		if st.Size() > t.offset {
			s.read(path, t)
		}
		t.size = st.Size()
	}
	for path, t := range s.files {
		if !current[path] {
			s.forget(path, t)
		}
	}
}

func (s *Scanner) forget(path string, t *tally) {
	for _, id := range t.claimed {
		delete(s.seen, id)
	}
	delete(s.files, path)
}

func (s *Scanner) read(path string, t *tally) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 1<<20)
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return // a partial last line waits for the rest
		}
		t.offset += int64(len(buf))
		s.line(t, buf)
		buf = buf[:0]
	}
}

type rawLine struct {
	Type      string    `json:"type"`
	UUID      string    `json:"uuid"`
	Timestamp time.Time `json:"timestamp"`
	IsMeta    bool      `json:"isMeta"`
	Compact   bool      `json:"isCompactSummary"`
	CWD       string    `json:"cwd"`
	Origin    *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

var (
	userType      = []byte(`"type":"user"`)
	assistantType = []byte(`"type":"assistant"`)
	toolUseID     = []byte(`"tool_use_id":"`)
)

func (s *Scanner) line(t *tally, b []byte) {
	isUser := bytes.Contains(b, userType)
	if !isUser && !bytes.Contains(b, assistantType) {
		return
	}
	// Results of tools nobody is waiting on (reads, greps) only need their
	// time, and they are the bulk of a transcript: skip decoding them.
	if isUser && bytes.Contains(b, toolUseID) && !t.awaits(b) {
		if at, ok := timestamp(b); ok {
			t.work(at)
		}
		return
	}
	var l rawLine
	if json.Unmarshal(b, &l) != nil || l.Timestamp.IsZero() {
		return
	}
	if id, ok := uuidKey(l.UUID); ok {
		if _, dup := s.seen[id]; dup {
			return
		}
		s.seen[id] = struct{}{}
		t.claimed = append(t.claimed, id)
	}
	if l.Type == "assistant" {
		t.assistant(&l)
	} else {
		t.user(&l)
	}
}

// awaits reports whether a tool-result line answers a tool being tracked.
func (t *tally) awaits(b []byte) bool {
	for len(t.pending) > 0 {
		i := bytes.Index(b, toolUseID)
		if i < 0 {
			return false
		}
		b = b[i+len(toolUseID):]
		j := bytes.IndexByte(b, '"')
		if j < 0 {
			return false
		}
		if _, ok := t.pending[string(b[:j])]; ok {
			return true
		}
	}
	return false
}

var timestampKey = []byte(`"timestamp":"`)

// timestamp finds a line's own time without decoding it. Keys nested in
// strings are escaped (\"timestamp\"), so the match is the line's.
func timestamp(b []byte) (time.Time, bool) {
	i := bytes.LastIndex(b, timestampKey)
	if i < 0 {
		return time.Time{}, false
	}
	rest := b[i+len(timestampKey):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, string(rest[:j]))
	return at, err == nil
}

func uuidKey(s string) ([16]byte, bool) {
	var k [16]byte
	h := strings.ReplaceAll(s, "-", "")
	if len(h) != 32 {
		return k, false
	}
	_, err := hex.Decode(k[:], []byte(h))
	return k, err == nil
}

// work notes Claude busy at "at", inside a turn.
func (t *tally) work(at time.Time) {
	if t.side || t.lastAt.IsZero() {
		return
	}
	if gap := at.Sub(t.lastAt); gap > 0 && gap <= idleGap {
		q := at.Unix() / quarter
		t.active[q] += gap
		if at.After(t.latest[q]) {
			t.latest[q] = at
		}
		t.turns[len(t.turns)-1].active += gap
	}
	if at.After(t.lastAt) {
		t.lastAt = at
	}
}

func (t *tally) assistant(l *rawLine) {
	t.work(l.Timestamp)
	q := l.Timestamp.Unix() / quarter
	if u := l.Message.Usage; u != nil && l.Message.ID != "" && l.Message.ID != t.lastMsg {
		// Each content block is its own line, repeating the message's usage.
		t.lastMsg = l.Message.ID
		t.tokens[q] += u.OutputTokens
	}
	var blocks []block
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		t.tools[q]++
		var in struct {
			Command  string `json:"command"`
			FilePath string `json:"file_path"`
			Notebook string `json:"notebook_path"`
		}
		_ = json.Unmarshal(b.Input, &in)
		switch b.Name {
		case "Bash":
			t.pending[b.ID] = pendingTool{name: b.Name, command: in.Command}
		case "Edit", "MultiEdit", "Write", "NotebookEdit":
			t.pending[b.ID] = pendingTool{name: b.Name, path: firstNonEmpty(in.FilePath, in.Notebook)}
		}
	}
}

func (t *tally) user(l *rawLine) {
	var blocks []block
	var texts []string
	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		texts = append(texts, s)
	} else if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return
	}
	results := false
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			results = true
			if p, ok := t.pending[b.ToolUseID]; ok {
				delete(t.pending, b.ToolUseID)
				t.finish(l, p, b)
			}
		case "text":
			texts = append(texts, b.Text)
		}
	}
	if results {
		t.work(l.Timestamp)
		return
	}
	if t.side || l.IsMeta || l.Compact || (l.Origin != nil && l.Origin.Kind != "human") {
		return
	}
	text := ""
	for _, s := range texts {
		if text = promptLine(s); text != "" {
			break
		}
	}
	if text == "" {
		return
	}
	t.turns = append(t.turns, turn{at: l.Timestamp, prompt: text, project: projectOf(l.CWD)})
	t.lastAt = l.Timestamp
}

// finish records what a tracked tool did once its result is in.
func (t *tally) finish(l *rawLine, p pendingTool, b block) {
	project := projectOf(l.CWD)
	var res struct {
		Type     string `json:"type"`
		FilePath string `json:"filePath"`
		Content  string `json:"content"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		Patch    []struct {
			Lines []string `json:"lines"`
		} `json:"structuredPatch"`
	}
	_ = json.Unmarshal(l.ToolUseResult, &res)
	if p.name == "Bash" {
		out := res.Stdout + "\n" + res.Stderr
		if strings.TrimSpace(out) == "" {
			out = resultText(b.Content)
		}
		for _, m := range Classify(p.command, out, b.IsError) {
			m.At, m.Project = l.Timestamp, project
			t.marks = append(t.marks, m)
		}
		return
	}
	if b.IsError {
		return
	}
	e := edit{at: l.Timestamp, path: firstNonEmpty(res.FilePath, p.path)}
	for _, h := range res.Patch {
		for _, ln := range h.Lines {
			switch {
			case strings.HasPrefix(ln, "+"):
				e.added++
			case strings.HasPrefix(ln, "-"):
				e.removed++
			}
		}
	}
	if res.Type == "create" && e.added == 0 && res.Content != "" {
		e.added = strings.Count(strings.TrimSuffix(res.Content, "\n"), "\n") + 1
	}
	if e.path != "" {
		t.edits = append(t.edits, e)
	}
}

// resultText is a tool result's text, whether a string or text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	_ = json.Unmarshal(raw, &blocks)
	var b strings.Builder
	for _, x := range blocks {
		b.WriteString(x.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// promptLine is what you typed, on one line; "" for harness messages
// (slash command echoes, which start with '<', and interruptions). System
// reminders ahead of a prompt are dropped.
func promptLine(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "<system-reminder>") {
		i := strings.Index(s, "</system-reminder>")
		if i < 0 {
			return ""
		}
		s = strings.TrimSpace(s[i+len("</system-reminder>"):])
	}
	if s == "" || strings.HasPrefix(s, "<") || strings.HasPrefix(s, "Caveat:") || strings.HasPrefix(s, "[Request interrupted") {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:159]) + "…"
	}
	return s
}

// projectOf names a session's folder, seeing through Claude's worktrees
// (repo/.claude/worktrees/name) to the repository.
func projectOf(cwd string) string {
	if i := strings.Index(cwd, "/.claude/worktrees/"); i >= 0 {
		cwd = cwd[:i]
	}
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
