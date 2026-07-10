package llm

import "context"

// Role identifies the author of a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is a function call requested by the assistant.
type ToolCall struct {
	// ID is the tool call identifier assigned by the LLM, used to correlate
	// the subsequent RoleTool response.
	ID string

	// Name is the function/tool name to invoke.
	Name string

	// Arguments is the raw JSON arguments string produced by the LLM. Tools are
	// responsible for parsing it.
	Arguments string
}

// Message is the internal, provider-agnostic representation of a chat
// completion message.
type Message struct {
	// Role is the message author role.
	Role Role

	// Content is the textual content. For tool results this is the tool output.
	Content string

	// ToolCalls are the calls requested by an assistant. Only meaningful for
	// RoleAssistant messages.
	ToolCalls []ToolCall

	// ToolCallID identifies the tool call this message responds to. Only
	// meaningful for RoleTool messages.
	ToolCallID string
}

// Response is the parsed result of a single chat completion request.
type Response struct {
	// Content is the assistant's textual content, if any.
	Content string

	// ToolCalls are the function calls the assistant requested, if any.
	ToolCalls []ToolCall

	// FinishReason is the provider-reported stop reason (e.g. "stop",
	// "tool_calls").
	FinishReason string
}

// Client is the interface for an LLM chat-completions provider supporting
// function/tool calling. Implementations must be safe for concurrent use.
type Client interface {
	// Complete sends the message history and returns the assistant's response.
	Complete(ctx context.Context, messages []Message) (Response, error)
}
