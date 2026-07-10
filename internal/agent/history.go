package agent

import "github.com/ahmetb/krew-review-agent/internal/llm"

// MessageHistory manages the ordered list of chat messages exchanged with the
// LLM during a single review. It is not safe for concurrent use; each review
// owns its own history.
type MessageHistory struct {
	msgs []llm.Message
}

// NewMessageHistory creates an empty history.
func NewMessageHistory() *MessageHistory {
	return &MessageHistory{msgs: make([]llm.Message, 0, 8)}
}

// Messages returns the current message slice (shared backing array; callers
// must not mutate).
func (h *MessageHistory) Messages() []llm.Message {
	return h.msgs
}

// Len returns the number of messages.
func (h *MessageHistory) Len() int {
	return len(h.msgs)
}

// AddSystem appends a system message.
func (h *MessageHistory) AddSystem(content string) {
	h.msgs = append(h.msgs, llm.Message{Role: llm.RoleSystem, Content: content})
}

// AddUser appends a user message.
func (h *MessageHistory) AddUser(content string) {
	h.msgs = append(h.msgs, llm.Message{Role: llm.RoleUser, Content: content})
}

// AddAssistantText appends an assistant message containing conversational text
// (no tool calls).
func (h *MessageHistory) AddAssistantText(content string) {
	h.msgs = append(h.msgs, llm.Message{Role: llm.RoleAssistant, Content: content})
}

// AddAssistantToolCalls appends an assistant message that requests one or more
// tool calls. content is any accompanying text (may be empty).
func (h *MessageHistory) AddAssistantToolCalls(content string, calls []llm.ToolCall) {
	h.msgs = append(h.msgs, llm.Message{
		Role:      llm.RoleAssistant,
		Content:   content,
		ToolCalls: calls,
	})
}

// AddTool appends a tool-result message responding to the given tool call ID.
func (h *MessageHistory) AddTool(toolCallID, content string) {
	h.msgs = append(h.msgs, llm.Message{
		Role:       llm.RoleTool,
		Content:    content,
		ToolCallID: toolCallID,
	})
}
