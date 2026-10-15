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
		{`{"type":"prompt_suggestion","suggestion":"Create these files.","uuid":"u1","session_id":"s1"}`,
			func(e Event) bool { return e.Suggestion == "Create these files." }},
	}
	for _, c := range cases {
		ev, ok := ParseLine([]byte(c.line))
		if !ok || !c.check(ev) {
			t.Errorf("parse failed for %s: %+v", c.line, ev)
		}
	}
	if _, ok := ParseLine([]byte(`{"type":"active_goal","value":null}`)); ok {
		t.Error("unknown event types should be ignored")
	}
}

func TestParseStatusPermissionMode(t *testing.T) {
	ev, ok := ParseLine([]byte(`{"type":"system","subtype":"status","status":null,"permissionMode":"acceptEdits"}`))
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
	ev, ok := ParseLine([]byte(`{"type":"user","message":{"role":"user","content":"Now say 'second done'.\nNow say 'third done'."},"parent_tool_use_id":null,"isReplay":true}`))
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
	ev, ok := ParseLine([]byte(`{"type":"result","subtype":"success","modelUsage":{"claude-haiku-4-5":{"contextWindow":200000},"claude-opus-5-5[1m]":{"contextWindow":1000000,"canonicalModel":"claude-opus-5-5"}}}`))
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

// Text and tool input from the CLI are cleaned for display, but a
// permission request's input, which goes back to the CLI, is kept as sent.
func TestParseCleansDisplayTextOnly(t *testing.T) {
	ev, _ := parseLine([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi\u001b]0;x\u0007"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls\u001b[8m"}}]}}`))
	blocks := ev.Message.Message.Content
	if blocks[0].Text != "hi␛]0;x" || strings.Contains(string(blocks[1].Input), `\u001b`) {
		t.Errorf("assistant message not cleaned: %q %s", blocks[0].Text, blocks[1].Input)
	}
	ev, _ = parseLine([]byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls\u001b[8m"},"description":"d\u001b[8m"}}`))
	if string(ev.Permission.Input) != `{"command":"ls\u001b[8m"}` || ev.Permission.Description != "d␛[8m" {
		t.Errorf("permission request: input %s, description %q", ev.Permission.Input, ev.Permission.Description)
	}
}

func TestRespondDropsAlwaysForSession(t *testing.T) {
	var buf bufCloser
	p := &Process{stdin: &buf}
	req := &PermissionRequest{RequestID: "r1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}
	if err := p.RespondAndInterrupt(req, PermissionDecision{Behavior: "allow", AlwaysForSession: true}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a response and an interrupt, got %q", lines)
	}
	if strings.Contains(lines[0], "alwaysForSession") || !strings.Contains(lines[0], `"updatedInput":{"command":"ls"}`) {
		t.Fatalf("response: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"subtype":"interrupt"`) {
		t.Fatalf("interrupt: %s", lines[1])
	}
}

func TestParseLineKeepsRaw(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","total_cost_usd":0.5}`)
	ev, ok := ParseLine(line)
	if !ok || ev.Result == nil || string(ev.Raw) != string(line) {
		t.Fatalf("got %+v", ev)
	}
	line[0] = 'x' // Raw is a copy
	if ev.Raw[0] != '{' {
		t.Fatal("Raw aliases the scanner's buffer")
	}
}
