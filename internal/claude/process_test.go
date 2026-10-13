package claude

import "testing"

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
