package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	got := ProjectDir("/tmp/claude-0/-home-user/x.y")
	want := "/cfg/projects/-tmp-claude-0--home-user-x-y"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestReadAndLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "abc.jsonl")
	data := `{"type":"queue-operation","operation":"enqueue"}
{"type":"user","isSidechain":false,"isMeta":true,"message":{"role":"user","content":"<command-name>/init</command-name>"},"cwd":"/w"}
{"type":"user","isSidechain":false,"message":{"role":"user","content":"fix the bug\nplease"},"cwd":"/w","gitBranch":"main"}
{"type":"assistant","isSidechain":false,"message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","isSidechain":true,"message":{"role":"user","content":"sub agent prompt"}}
{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"a.go"}]}}
{"type":"last-prompt","lastPrompt":"fix the bug please"}
`
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	info, ok := Read(p)
	if !ok || info.ID != "abc" || info.FirstPrompt != "fix the bug please" || info.Branch != "main" || info.Messages != 3 || info.CWD != "/w" {
		t.Fatalf("bad info: %+v", info)
	}
	msgs, err := Load(p)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("load: %v %+v", err, msgs)
	}
	if msgs[1].Content[0].Name != "Bash" || msgs[2].Content[0].ToolUseID != "t1" {
		t.Fatalf("bad messages: %+v", msgs)
	}
	if got, ok := Find(dir, ""); !ok || got != p {
		t.Fatalf("find latest: %s", got)
	}
}

func TestPrompts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, age time.Duration) {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		ts := time.Now().Add(-age)
		os.Chtimes(p, ts, ts)
	}
	write("old.jsonl", `{"type":"user","message":{"role":"user","content":"first ever"}}
{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}
`, time.Hour)
	write("new.jsonl", `{"type":"user","message":{"role":"user","content":"<command-name>/init</command-name>"}}
{"type":"user","message":{"role":"user","content":"latest one"}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"x"}]}}
`, time.Minute)
	got := Prompts(dir, 10)
	if len(got) != 2 || got[0] != "first ever" || got[1] != "latest one" {
		t.Fatalf("prompts: %q", got)
	}
	if got := Prompts(dir, 1); len(got) != 1 || got[0] != "latest one" {
		t.Fatalf("limit should keep the newest: %q", got)
	}
}

func TestSubagents(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "abc.jsonl")
	sub := filepath.Join(dir, "abc", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		p:                                        `{"type":"user","message":{"role":"user","content":"go"}}` + "\n",
		filepath.Join(sub, "agent-a1.meta.json"): `{"agentType":"Explore","toolUseId":"toolu_1"}`,
		filepath.Join(sub, "agent-a1.jsonl"): `{"type":"user","isSidechain":true,"message":{"role":"user","content":"find it"}}
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"s1","name":"Grep","input":{"pattern":"x"}}]}}
{"type":"user","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","content":"a.go"}]}}
`,
		// No meta, so no parent to hang it on.
		filepath.Join(sub, "agent-a2.jsonl"): `{"type":"user","isSidechain":true,"message":{"role":"user","content":"orphan"}}` + "\n",
	}
	for f, data := range files {
		if err := os.WriteFile(f, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Subagents(p)
	if len(got) != 1 || len(got["toolu_1"]) != 3 {
		t.Fatalf("got %+v", got)
	}
	if b := got["toolu_1"][1].Content[0]; b.Type != "tool_use" || b.Name != "Grep" {
		t.Fatalf("second message: %+v", got["toolu_1"][1])
	}
	if len(Subagents(filepath.Join(dir, "none.jsonl"))) != 0 {
		t.Fatal("a session without sub-agents should load none")
	}
}
