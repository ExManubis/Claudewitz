package claude

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Lines captured from claude 2.1.x in stream-json mode.
func TestParseLine(t *testing.T) {
	cases := []struct {
		line  string
		check func(Event) bool
	}{
		{`{"type":"system","subtype":"init","cwd":"/w","session_id":"s1","model":"claude-haiku-5-5","permissionMode":"default","tools":["Bash"]}`,
			func(e Event) bool {
				return e.Init != nil && e.Init.Model == "claude-haiku-5-5" && e.Init.SessionID == "s1"
			}},
		{`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls /"},"tool_use_id":"toolu_1"}}`,
			func(e Event) bool {
				return e.Permission != nil && e.Permission.RequestID == "r1" && e.Permission.ToolName == "Bash" && string(e.Permission.Input) == `{"command":"ls /"}`
			}},
		{`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.02,"resetsAt":1791904200}}}}`,
			func(e Event) bool {
				return e.RateLimit != nil && e.RateLimit.Info.Windows["five_hour"].Utilization == 0.02
			}},
		{`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}},"parent_tool_use_id":null}`,
			func(e Event) bool { return e.Stream != nil && e.Stream.Event.Delta.Text == "hi" }},
		{`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":"hi","is_error":false}]},"parent_tool_use_id":null}`,
			func(e Event) bool { return e.Message != nil && e.Message.Message.Content[0].ToolUseID == "toolu_1" }},
		{`{"type":"result","subtype":"success","total_cost_usd":0.0038,"num_turns":3,"session_id":"s1"}`,
			func(e Event) bool { return e.Result != nil && e.Result.NumTurns == 3 }},
	}
	for _, c := range cases {
		ev, ok := parseLine([]byte(c.line))
		if !ok || !c.check(ev) {
			t.Errorf("parse failed for %s: %+v", c.line, ev)
		}
	}
	if _, ok := parseLine([]byte(`{"type":"active_goal","value":null}`)); ok {
		t.Error("unknown event types should be ignored")
	}
}

func TestParseStatusPermissionMode(t *testing.T) {
	ev, ok := parseLine([]byte(`{"type":"system","subtype":"status","status":null,"permissionMode":"acceptEdits"}`))
	if !ok || ev.PermissionMode != "acceptEdits" || ev.Status != "" {
		t.Fatalf("got %+v", ev)
	}
}

func TestAnswerQuestions(t *testing.T) {
	in := []byte(`{"questions":[{"question":"Colour?","header":"Colour","multiSelect":false,"options":[{"label":"Red"},{"label":"Blue"}]}]}`)
	qs, err := ParseQuestions(in)
	if err != nil || len(qs) != 1 || qs[0].Options[1].Label != "Blue" {
		t.Fatalf("parse: %+v %v", qs, err)
	}
	out, err := AnswerQuestions(in, map[string]string{"Colour?": "Blue"})
	if err != nil || !strings.Contains(string(out), `"answers":{"Colour?":"Blue"}`) || !strings.Contains(string(out), `"questions"`) {
		t.Fatalf("answer: %s %v", out, err)
	}
}

func TestParseReplayWithStringContent(t *testing.T) {
	ev, ok := parseLine([]byte(`{"type":"user","message":{"role":"user","content":"Now say 'second done'.\nNow say 'third done'."},"parent_tool_use_id":null,"isReplay":true}`))
	if !ok || ev.Message == nil || !ev.Message.IsReplay {
		t.Fatalf("replay not parsed: %+v", ev)
	}
	if got := ev.Message.Message.Text(); got != "Now say 'second done'.\nNow say 'third done'." {
		t.Fatalf("text: %q", got)
	}
}

type bufCloser struct{ bytes.Buffer }

func (bufCloser) Close() error { return nil }

func TestSendWithImages(t *testing.T) {
	var buf bufCloser
	p := &Process{stdin: &buf}
	if err := p.SendWithImages("what is this?", []Image{{MediaType: "image/png", Data: []byte{1, 2, 3}}}); err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Message struct {
			Content []map[string]any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	c := msg.Message.Content
	if len(c) != 2 || c[0]["type"] != "image" || c[1]["text"] != "what is this?" {
		t.Fatalf("content: %v", c)
	}
	src := c[0]["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "AQID" {
		t.Fatalf("source: %v", src)
	}
}

func TestResultContextWindow(t *testing.T) {
	ev, ok := parseLine([]byte(`{"type":"result","subtype":"success","modelUsage":{"claude-haiku-4-5":{"contextWindow":200000},"claude-opus-5-5[1m]":{"contextWindow":1000000,"canonicalModel":"claude-opus-5-5"}}}`))
	if !ok || ev.Result == nil {
		t.Fatalf("got %+v", ev)
	}
	for model, want := range map[string]int{
		"claude-opus-5-5[1m]": 1000000, // exact key
		"claude-opus-5-5":     1000000, // initialize reports the model without the suffix
		"claude-haiku-4-5":    200000,  // a sub-agent's model doesn't win over the main one
		"claude-sonnet-5-5":   0,       // no usage for this model this turn
	} {
		if got := ev.Result.ContextWindow(model); got != want {
			t.Errorf("ContextWindow(%q) = %d, want %d", model, got, want)
		}
	}
}
