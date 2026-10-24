package daemon

import (
	"testing"

	"github.com/exmanubis/claudewitz/internal/claude"
)

func line(t *testing.T, s string) claude.Event {
	t.Helper()
	ev, ok := claude.ParseLine([]byte(s))
	if !ok {
		t.Fatalf("bad line %s", s)
	}
	return ev
}

func TestLocalCommandEchoStartsNoTurn(t *testing.T) {
	s := newSession(nil, "s_1", nil, SessionSummary{ID: "s_1"})
	s.observe(line(t, `{"type":"system","subtype":"init","session_id":"c1","model":"claude-sonnet-5-5"}`))
	s.observe(line(t, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0,"duration_ms":1}`))
	s.observe(line(t, `{"type":"user","message":{"role":"user","content":"<local-command-stdout>Set model to `+"`claude-opus-5-5`"+`</local-command-stdout>"},"parent_tool_use_id":null,"isReplay":true}`))
	if got := s.summary(); got.State != StateIdle || got.Title != "" {
		t.Fatalf("after the echo: state %s, title %q", got.State, got.Title)
	}
	s.observe(line(t, `{"type":"user","message":{"role":"user","content":"run the tests"},"parent_tool_use_id":null,"isReplay":true}`))
	if got := s.summary(); got.State != StateWorking || got.Title != "run the tests" {
		t.Fatalf("after a prompt: state %s, title %q", got.State, got.Title)
	}
}
