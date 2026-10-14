// Package claude drives the Claude Code CLI in headless stream-json mode.
//
// The CLI is spawned with --input-format/--output-format stream-json and
// --permission-prompt-tool stdio, so it authenticates with whatever login the
// user already has (including a Pro/Max subscription) and asks us, over
// stdin/stdout, before running tools.
package claude

import (
	"encoding/json"
	"strings"

	"github.com/exmanubis/claudewitz/internal/safetext"
)

// Envelope is the common shape of every line the CLI writes to stdout.
type Envelope struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	UUID      string `json:"uuid,omitempty"`
}

// InitEvent is emitted once at startup (type=system, subtype=init).
type InitEvent struct {
	CWD            string   `json:"cwd"`
	SessionID      string   `json:"session_id"`
	Model          string   `json:"model"`
	PermissionMode string   `json:"permissionMode"`
	Tools          []string `json:"tools"`
	SlashCommands  []string `json:"slash_commands"`
	Version        string   `json:"claude_code_version"`
}

// ContentBlock is one block of an assistant or user message.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *ImageSource    `json:"source,omitempty"` // image blocks
}

// UnmarshalJSON makes the block's text and tool input safe to display
// (see safetext). Blocks are only ever displayed: what goes back to the
// CLI, such as a permission request's input, is kept as sent.
func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	type alias ContentBlock
	if err := json.Unmarshal(data, (*alias)(b)); err != nil {
		return err
	}
	b.Text, b.Thinking = safetext.Clean(b.Text), safetext.Clean(b.Thinking)
	if len(b.Input) > 0 {
		b.Input = safetext.JSON(b.Input)
	}
	return nil
}

// ImageSource is the payload of an image block.
type ImageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// Usage is token accounting for a message or a whole turn.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// Message is the inner message of an assistant/user event.
type Message struct {
	ID      string         `json:"id"`
	Role    string         `json:"role"`
	Model   string         `json:"model"`
	Content []ContentBlock `json:"content"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// UnmarshalJSON accepts content as either blocks or a plain string (user
// messages echoed back by --replay-user-messages use the string form).
func (m *Message) UnmarshalJSON(b []byte) error {
	type alias Message
	var raw struct {
		alias
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*m = Message(raw.alias)
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw.Content, &s) == nil {
		m.Content = []ContentBlock{{Type: "text", Text: safetext.Clean(s)}}
		return nil
	}
	return json.Unmarshal(raw.Content, &m.Content)
}

// Text joins the message's text blocks.
func (m Message) Text() string {
	var parts []string
	for _, b := range m.Content {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// MessageEvent is a complete assistant or user (tool result) message.
type MessageEvent struct {
	Type            string  `json:"type"`
	Message         Message `json:"message"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	AgentID         string  `json:"agent_id,omitempty"`
	// IsReplay marks the CLI acknowledging a user message it has started
	// on (--replay-user-messages). Messages sent mid-turn are queued and
	// acknowledged together, joined by newlines, once the turn ends.
	IsReplay bool `json:"isReplay,omitempty"`
}

// StreamEvent wraps a raw Anthropic API streaming event
// (only sent with --include-partial-messages).
type StreamEvent struct {
	Event struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		ContentBlock *ContentBlock `json:"content_block"`
	} `json:"event"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
}

// ResultEvent ends a turn.
type ResultEvent struct {
	Subtype      string  `json:"subtype"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	DurationMS   int     `json:"duration_ms"`
	NumTurns     int     `json:"num_turns"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	StopReason   string  `json:"stop_reason"`
	SessionID    string  `json:"session_id"`
	Usage        Usage   `json:"usage"`
	// ModelUsage is keyed by model id, including any sub-agents' models.
	ModelUsage map[string]ModelUsage `json:"modelUsage"`
}

// ModelUsage is one model's share of a turn.
type ModelUsage struct {
	ContextWindow int `json:"contextWindow"`
}

// ContextWindow is the context size of model as reported for this turn, or
// 0 if it wasn't used. Keys can carry a suffix the model name lacks, as in
// "claude-opus-5-5[1m]" for "claude-opus-5-5".
func (r *ResultEvent) ContextWindow(model string) int {
	if u, ok := r.ModelUsage[model]; ok {
		return u.ContextWindow
	}
	for id, u := range r.ModelUsage {
		if base, _, ok := strings.Cut(id, "["); ok && base == model {
			return u.ContextWindow
		}
	}
	return 0
}

// RateLimitWindow is utilisation (0..1) of one subscription window.
type RateLimitWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

// RateLimitEvent reports subscription usage limits.
type RateLimitEvent struct {
	Info struct {
		Status         string                     `json:"status"`
		ResetsAt       int64                      `json:"resetsAt"`
		RateLimitType  string                     `json:"rateLimitType"`
		IsUsingOverage bool                       `json:"isUsingOverage"`
		Windows        map[string]RateLimitWindow `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

// PermissionRequest is a can_use_tool control request from the CLI.
type PermissionRequest struct {
	RequestID   string          `json:"-"`
	ToolName    string          `json:"tool_name"`
	DisplayName string          `json:"display_name"`
	Input       json.RawMessage `json:"input"`
	Description string          `json:"description"`
	ToolUseID   string          `json:"tool_use_id"`
	BlockedPath string          `json:"blocked_path"`
	AgentID     string          `json:"agent_id,omitempty"`
	// RequiresUserInteraction marks tools that need an answer rather than a
	// yes/no (AskUserQuestion, ExitPlanMode).
	RequiresUserInteraction bool `json:"requires_user_interaction,omitempty"`
}

// TaskEvent tracks a sub-agent (system task_started / task_progress /
// task_updated / task_notification).
type TaskEvent struct {
	Subtype      string `json:"subtype"`
	TaskID       string `json:"task_id"`
	ToolUseID    string `json:"tool_use_id"`
	Description  string `json:"description"`
	SubagentType string `json:"subagent_type"`
	Status       string `json:"status"`
	LastToolName string `json:"last_tool_name"`
	Summary      string `json:"summary"`
	Usage        struct {
		TotalTokens int `json:"total_tokens"`
		ToolUses    int `json:"tool_uses"`
		DurationMS  int `json:"duration_ms"`
	} `json:"usage"`
	Patch struct {
		Status string `json:"status"`
	} `json:"patch"`
}

// Command is a slash command Claude Code knows about.
type Command struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ArgumentHint string   `json:"argumentHint"`
	Aliases      []string `json:"aliases"`
}

// ModelInfo is an entry of the CLI's model picker.
type ModelInfo struct {
	Value         string `json:"value"`
	ResolvedModel string `json:"resolvedModel"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	// Effort levels the model accepts (low, medium, high, xhigh, max).
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

// InitializeResponse answers the host's initialize control request.
type InitializeResponse struct {
	Commands []Command   `json:"commands"`
	Models   []ModelInfo `json:"models"`
	Agents   []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"agents"`
	Account struct {
		SubscriptionType string `json:"subscriptionType"`
		Email            string `json:"email"`
	} `json:"account"`
	Version        string `json:"claude_code_version"`
	PermissionMode string `json:"current_permission_mode"`
}

type controlRequest struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

type controlResponse struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Response  any    `json:"response,omitempty"`
		Error     string `json:"error,omitempty"`
	} `json:"response"`
}

// PermissionDecision answers a PermissionRequest.
type PermissionDecision struct {
	Behavior     string          `json:"behavior"` // "allow" or "deny"
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
	// UpdatedPermissions applies permission changes along with an allow,
	// e.g. SetModeUpdate("acceptEdits").
	UpdatedPermissions []PermissionUpdate `json:"updatedPermissions,omitempty"`
}

// PermissionUpdate is one entry of PermissionDecision.UpdatedPermissions.
type PermissionUpdate struct {
	Type        string `json:"type"` // "setMode"
	Mode        string `json:"mode,omitempty"`
	Destination string `json:"destination"` // "session"
}

// SetModeUpdate switches the session's permission mode.
func SetModeUpdate(mode string) PermissionUpdate {
	return PermissionUpdate{Type: "setMode", Mode: mode, Destination: "session"}
}

// Question is one entry of an AskUserQuestion tool call.
type Question struct {
	Question    string `json:"question"`
	Header      string `json:"header"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// ParseQuestions reads the questions from AskUserQuestion input.
func ParseQuestions(input json.RawMessage) ([]Question, error) {
	var in struct {
		Questions []Question `json:"questions"`
	}
	err := json.Unmarshal(input, &in)
	return in.Questions, err
}

// AnswerQuestions returns AskUserQuestion input with answers attached:
// question text → chosen label (several labels joined by ", ").
func AnswerQuestions(input json.RawMessage, answers map[string]string) (json.RawMessage, error) {
	var in map[string]any
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	in["answers"] = answers
	return json.Marshal(in)
}
