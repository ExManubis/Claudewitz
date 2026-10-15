package recap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// transcript writes Claude Code transcript lines for tests.
type transcript struct {
	t    *testing.T
	path string
	cwd  string
	n    int
}

func newTranscript(t *testing.T, root, project, session string) *transcript {
	dir := filepath.Join(root, "-x-"+project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &transcript{t: t, path: filepath.Join(dir, session+".jsonl"), cwd: "/x/" + project}
}

func (tr *transcript) write(line map[string]any) {
	tr.n++
	if _, ok := line["uuid"]; !ok {
		line["uuid"] = fmt.Sprintf("%08x-0000-0000-0000-%012x", len(tr.path), tr.n)
	}
	line["cwd"] = tr.cwd
	b, _ := json.Marshal(line)
	f, err := os.OpenFile(tr.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		tr.t.Fatal(err)
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

func (tr *transcript) prompt(at time.Time, text string) {
	tr.write(map[string]any{"type": "user", "timestamp": at, "origin": map[string]any{"kind": "human"},
		"message": map[string]any{"role": "user", "content": text}})
}

func (tr *transcript) say(at time.Time, msgID string, out int, blocks ...map[string]any) {
	if len(blocks) == 0 {
		blocks = []map[string]any{{"type": "text", "text": "ok"}}
	}
	tr.write(map[string]any{"type": "assistant", "timestamp": at,
		"message": map[string]any{"id": msgID, "role": "assistant", "content": blocks, "usage": map[string]any{"output_tokens": out}}})
}

func (tr *transcript) result(at time.Time, id string, isError bool, content string, toolUseResult any) {
	tr.write(map[string]any{"type": "user", "timestamp": at, "toolUseResult": toolUseResult,
		"message": map[string]any{"role": "user", "content": []map[string]any{
			{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": isError}}}})
}

func bash(id, command string) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": command}}
}

func scannerAt(root string, now time.Time) *Scanner {
	s := NewScanner(root)
	s.now = func() time.Time { return now }
	return s
}

var cph = func() *time.Location {
	l, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		panic(err)
	}
	return l
}()

func at(day, clock string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", day+" "+clock, cph)
	if err != nil {
		panic(err)
	}
	return t
}

func TestRecapTalliesADay(t *testing.T) {
	root := t.TempDir()
	tr := newTranscript(t, root, "app", "s1")
	d := "2026-10-15"
	tr.prompt(at(d, "09:00:00"), "Add a recap")
	tr.say(at(d, "09:00:20"), "m1", 100, bash("t1", "go test ./... && git commit -qm \"Add a recap\""))
	tr.say(at(d, "09:00:20"), "m1", 100) // the same message's next block repeats its usage
	tr.result(at(d, "09:01:00"), "t1", false, "ok", map[string]any{"stdout": "ok  x 0.1s", "stderr": ""})
	tr.say(at(d, "09:01:10"), "m2", 50, map[string]any{"type": "tool_use", "id": "t2", "name": "Edit",
		"input": map[string]any{"file_path": "/x/app/a.go"}})
	tr.result(at(d, "09:01:30"), "t2", false, "updated", map[string]any{"filePath": "/x/app/a.go",
		"structuredPatch": []map[string]any{{"lines": []string{" a", "-b", "+c", "+d"}}}})
	tr.say(at(d, "09:01:40"), "m3", 10, map[string]any{"type": "tool_use", "id": "t3", "name": "Write",
		"input": map[string]any{"file_path": "/x/app/new.swift"}})
	tr.result(at(d, "09:02:00"), "t3", false, "created", map[string]any{"type": "create", "filePath": "/x/app/new.swift",
		"content": "one\ntwo\nthree\n", "structuredPatch": []any{}})
	// A read nobody tracks, then a long wait for you: neither is work.
	tr.say(at(d, "09:02:10"), "m4", 5, map[string]any{"type": "tool_use", "id": "t4", "name": "Read", "input": map[string]any{}})
	tr.result(at(d, "09:02:15"), "t4", false, "file contents", map[string]any{})
	tr.say(at(d, "09:40:00"), "m5", 5)

	// Lines that aren't prompts.
	tr.write(map[string]any{"type": "user", "timestamp": at(d, "10:00:00"), "isMeta": true, "message": map[string]any{"role": "user", "content": "meta"}})
	tr.write(map[string]any{"type": "user", "timestamp": at(d, "10:00:00"), "origin": map[string]any{"kind": "peer"}, "message": map[string]any{"role": "user", "content": "agent report"}})
	tr.write(map[string]any{"type": "user", "timestamp": at(d, "10:00:00"), "message": map[string]any{"role": "user", "content": "[Request interrupted by user]"}})
	tr.write(map[string]any{"type": "user", "timestamp": at(d, "10:00:00"), "message": map[string]any{"role": "user", "content": []map[string]any{
		{"type": "text", "text": "<system-reminder>\nnote\n</system-reminder>"}, {"type": "text", "text": "Ship it"}}}})
	tr.say(at(d, "10:00:30"), "m6", 5, bash("t5", "gh pr merge 37 --merge"))
	tr.result(at(d, "10:00:40"), "t5", false, "", map[string]any{"stdout": "✓ Merged pull request a/b#37 (Recap)"})

	day := scannerAt(root, at(d, "23:00:00")).Recap(at(d, "12:00:00"), cph)
	if day.Prompts != 2 {
		t.Fatalf("prompts %d, want 2 (Add a recap, Ship it)", day.Prompts)
	}
	if day.Working != 135+40 {
		t.Errorf("working %ds, want 175 (the 38 min wait isn't work)", day.Working)
	}
	if day.OutputTokens != 175 {
		t.Errorf("tokens %d, want 175 (usage counted once per message)", day.OutputTokens)
	}
	if day.FilesEdited != 2 || day.LinesAdded != 5 || day.LinesRemoved != 1 {
		t.Errorf("edits: %d files +%d −%d, want 2 files +5 −1", day.FilesEdited, day.LinesAdded, day.LinesRemoved)
	}
	if day.Commits != 1 || day.TestsPassed != 1 || day.PRsMerged != 1 {
		t.Errorf("commits %d, tests %d, merges %d", day.Commits, day.TestsPassed, day.PRsMerged)
	}
	if len(day.Moments) != 2 || day.Moments[0].Text != "Add a recap" || day.Moments[1].Text != "a/b#37 (Recap)" {
		t.Errorf("moments %+v", day.Moments)
	}
	if day.ToolCalls != 5 || day.Sessions != 1 {
		t.Errorf("tool calls %d, sessions %d", day.ToolCalls, day.Sessions)
	}
	if day.Hours[9] != 135 || day.Hours[10] != 40 {
		t.Errorf("hours 9: %d, 10: %d", day.Hours[9], day.Hours[10])
	}
	if day.Longest == nil || day.Longest.Prompt != "Add a recap" || day.Longest.Seconds != 135 {
		t.Errorf("longest %+v", day.Longest)
	}
	if day.First.Format("15:04") != "09:00" || day.Last.Format("15:04:05") != "10:00:40" {
		t.Errorf("first %v last %v", day.First, day.Last)
	}
	if len(day.Projects) != 1 || day.Projects[0].Name != "app" || day.Projects[0].Prompts != 2 {
		t.Errorf("projects %+v", day.Projects)
	}
}

func TestRecapReadsOnlyWhatWasAppended(t *testing.T) {
	root := t.TempDir()
	tr := newTranscript(t, root, "app", "s1")
	d := "2026-10-15"
	tr.prompt(at(d, "09:00:00"), "one")
	s := scannerAt(root, at(d, "23:00:00"))
	if got := s.Recap(at(d, "12:00:00"), cph).Prompts; got != 1 {
		t.Fatalf("prompts %d", got)
	}
	tr.prompt(at(d, "09:10:00"), "two")
	// A line still being written waits for its newline.
	f, _ := os.OpenFile(tr.path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","timestamp":"2026-10-15T07:20:00Z","message":{"role":"user","content":"thr`)
	f.Close()
	if got := s.Recap(at(d, "12:00:00"), cph).Prompts; got != 2 {
		t.Fatalf("after appending: prompts %d, want 2", got)
	}
	f, _ = os.OpenFile(tr.path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`ee"},"uuid":"00000000-0000-0000-0000-0000000000ff"}` + "\n")
	f.Close()
	if got := s.Recap(at(d, "12:00:00"), cph).Prompts; got != 3 {
		t.Fatalf("after finishing the line: prompts %d, want 3", got)
	}
}

func TestRecapCountsResumedHistoryOnce(t *testing.T) {
	root := t.TempDir()
	d := "2026-10-15"
	a := newTranscript(t, root, "app", "s1")
	a.write(map[string]any{"type": "user", "uuid": "11111111-1111-1111-1111-111111111111", "timestamp": at(d, "09:00:00"),
		"message": map[string]any{"role": "user", "content": "hello"}})
	b := newTranscript(t, root, "app", "s2")
	b.write(map[string]any{"type": "user", "uuid": "11111111-1111-1111-1111-111111111111", "timestamp": at(d, "09:00:00"),
		"message": map[string]any{"role": "user", "content": "hello"}})
	b.prompt(at(d, "11:00:00"), "and more")
	if got := scannerAt(root, at(d, "23:00:00")).Recap(at(d, "12:00:00"), cph).Prompts; got != 2 {
		t.Fatalf("prompts %d, want 2", got)
	}
}

func TestRecapDaysFollowTheTimezone(t *testing.T) {
	root := t.TempDir()
	tr := newTranscript(t, root, "app", "s1")
	tr.prompt(at("2026-10-14", "23:30:00"), "late")  // 21:30 UTC
	tr.prompt(at("2026-10-15", "00:30:00"), "later") // 22:30 UTC the 14th
	s := scannerAt(root, at("2026-10-15", "23:00:00"))
	if got := s.Recap(at("2026-10-15", "12:00:00"), cph).Prompts; got != 1 {
		t.Fatalf("15th in Copenhagen: %d prompts, want 1", got)
	}
	if got := s.Recap(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), time.UTC).Prompts; got != 2 {
		t.Fatalf("14th in UTC: %d prompts, want 2", got)
	}
}

func TestRecapWeekStreakAndBadges(t *testing.T) {
	root := t.TempDir()
	tr := newTranscript(t, root, "app", "s1")
	for _, d := range []string{"2026-10-09", "2026-10-11", "2026-10-12", "2026-10-13", "2026-10-14"} {
		tr.prompt(at(d, "12:00:00"), "work")
	}
	tr.prompt(at("2026-10-15", "05:30:00"), "early start")
	tr.say(at("2026-10-15", "05:40:00"), "m1", 1)
	tr.prompt(at("2026-10-15", "23:20:00"), "late finish")
	tr.say(at("2026-10-15", "23:25:00"), "m2", 1)
	s := scannerAt(root, at("2026-10-15", "23:30:00"))
	day := s.Recap(at("2026-10-15", "12:00:00"), cph)
	var week []int
	for _, w := range day.Week {
		week = append(week, w.Prompts)
	}
	if fmt.Sprint(week) != "[1 0 1 1 1 1 2]" || day.Week[6].Date != "2026-10-15" {
		t.Fatalf("week %v (%+v)", week, day.Week)
	}
	if day.Streak != 5 {
		t.Fatalf("streak %d, want 5 (the 11th to the 15th)", day.Streak)
	}
	var ids []string
	for _, b := range day.Badges {
		ids = append(ids, b.ID)
	}
	if got := strings.Join(ids, ","); got != "early,owl,streak" {
		t.Fatalf("badges %s", got)
	}
	// Before today's first prompt, yesterday's streak still stands.
	if got := s.Recap(at("2026-10-16", "12:00:00"), cph).Streak; got != 5 {
		t.Fatalf("streak on a day without prompts yet: %d", got)
	}
}

func TestRecapCountsSubagentWorkButNotPrompts(t *testing.T) {
	root := t.TempDir()
	d := "2026-10-15"
	newTranscript(t, root, "app", "s1").prompt(at(d, "09:00:00"), "delegate")
	dir := filepath.Join(root, "-x-app", "s1", "subagents")
	os.MkdirAll(dir, 0o755)
	side := &transcript{t: t, path: filepath.Join(dir, "agent-1.jsonl"), cwd: "/x/app"}
	side.prompt(at(d, "09:00:05"), "the task, as the agent got it")
	side.say(at(d, "09:00:10"), "a1", 40, bash("b1", "git commit -qm 'From the agent'"))
	side.result(at(d, "09:00:20"), "b1", false, "", map[string]any{"stdout": ""})
	day := scannerAt(root, at(d, "23:00:00")).Recap(at(d, "12:00:00"), cph)
	if day.Prompts != 1 || day.Commits != 1 || day.OutputTokens != 40 {
		t.Fatalf("prompts %d commits %d tokens %d", day.Prompts, day.Commits, day.OutputTokens)
	}
}
